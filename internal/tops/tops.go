// Package tops reads one day of IEX TOPS 1.6 market data (the HIST pcapng
// files) and sums up each symbol's regular-session IEX trading for the day.
//
// Only three message types matter here:
//   - System Event (0x53): 'R' starts and 'M' ends regular market hours, so
//     half days come out right without a holiday calendar.
//   - Trade Report (0x54): one IEX execution.
//   - Trade Break (0x42): a trade IEX later broke; it is removed.
//
// Which trades count follows the consolidated tape's conventions, which the
// spike checked against Alpaca's SIP bars: open, high, low and close come
// from trades that are neither odd lots (flag 0x20) nor extended hours
// (0x40); volume and VWAP include every regular-session trade, odd lots too.
package tops

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
)

const (
	msgSystemEvent = 0x53
	msgTrade       = 0x54
	msgTradeBreak  = 0x42

	flagExtendedHours = 0x40
	flagOddLot        = 0x20

	eventRegularStart = 'R'
	eventRegularEnd   = 'M'

	tradeLen = 38 // Trade Report and Trade Break share this layout
)

// Summary is one symbol's regular-session IEX trading for one day. Prices are
// in units of 1/10000 dollar, as IEX sends them.
type Summary struct {
	Symbol string
	// Open, High, Low, Close: from last-sale-eligible trades (not odd lots).
	// Zero when there was none.
	Open, High, Low, Close int64
	Volume                 int64   // shares, every regular-session trade
	Notional               float64 // dollars, sum of price * size over the same trades
	Trades                 int64   // regular-session trades
	LotTrades              int64   // of which last-sale eligible
}

// Day is everything Parse found in one file.
type Day struct {
	RegularStart, RegularEnd int64 // nanoseconds since the epoch, from System Events
	Trades, Breaks           int64 // trade reports and breaks seen in the whole file
	Symbols                  []Summary
}

type trade struct {
	sym   int32
	ts    int64
	price int64
	size  uint32
	flags uint8
	id    int64
}

// Parse reads an uncompressed pcapng stream of IEX-TP packets carrying TOPS
// 1.6 messages. It fails when the file is malformed or when it does not say
// when regular hours started and ended: a day without them cannot be split
// into regular and extended hours, so it is better missing than wrong.
func Parse(r io.Reader) (*Day, error) {
	day := &Day{}
	symIndex := map[string]int32{}
	var symbols []string
	var trades []trade
	broken := map[int64]bool{}
	err := forEachMessage(r, func(msg []byte) error {
		switch msg[0] {
		case msgSystemEvent:
			if len(msg) < 10 {
				return errors.New("short system event")
			}
			ts := int64(binary.LittleEndian.Uint64(msg[2:10]))
			switch msg[1] {
			case eventRegularStart:
				day.RegularStart = ts
			case eventRegularEnd:
				day.RegularEnd = ts
			}
		case msgTrade, msgTradeBreak:
			if len(msg) < tradeLen {
				return fmt.Errorf("short trade message (%d bytes)", len(msg))
			}
			id := int64(binary.LittleEndian.Uint64(msg[30:38]))
			if msg[0] == msgTradeBreak {
				day.Breaks++
				broken[id] = true
				return nil
			}
			day.Trades++
			sym := strings.TrimRight(string(msg[10:18]), " ")
			idx, ok := symIndex[sym]
			if !ok {
				idx = int32(len(symbols))
				symIndex[sym] = idx
				symbols = append(symbols, sym)
			}
			trades = append(trades, trade{
				sym:   idx,
				flags: msg[1],
				ts:    int64(binary.LittleEndian.Uint64(msg[2:10])),
				size:  binary.LittleEndian.Uint32(msg[18:22]),
				price: int64(binary.LittleEndian.Uint64(msg[22:30])),
				id:    id,
			})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if day.RegularStart == 0 || day.RegularEnd <= day.RegularStart {
		return nil, errors.New("no regular-hours start and end in the file")
	}

	type acc struct {
		Summary
		firstTs, lastTs int64
		seen            bool
	}
	accs := make([]acc, len(symbols))
	for _, t := range trades {
		if broken[t.id] || t.ts < day.RegularStart || t.ts >= day.RegularEnd || t.flags&flagExtendedHours != 0 || t.price <= 0 || t.size == 0 {
			continue
		}
		a := &accs[t.sym]
		a.seen = true
		a.Volume += int64(t.size)
		a.Notional += float64(t.price) / 10000 * float64(t.size)
		a.Trades++
		if t.flags&flagOddLot != 0 {
			continue
		}
		if a.LotTrades == 0 || t.ts < a.firstTs {
			a.firstTs, a.Open = t.ts, t.price
		}
		if a.LotTrades == 0 || t.ts >= a.lastTs {
			a.lastTs, a.Close = t.ts, t.price
		}
		if a.LotTrades == 0 || t.price > a.High {
			a.High = t.price
		}
		if a.LotTrades == 0 || t.price < a.Low {
			a.Low = t.price
		}
		a.LotTrades++
	}
	for i, a := range accs {
		if !a.seen {
			continue
		}
		a.Summary.Symbol = symbols[i]
		day.Symbols = append(day.Symbols, a.Summary)
	}
	sort.Slice(day.Symbols, func(i, j int) bool { return day.Symbols[i].Symbol < day.Symbols[j].Symbol })
	return day, nil
}

// forEachMessage walks a pcapng stream: Enhanced Packet Blocks holding
// Ethernet / IPv4 / UDP / IEX-TP, and calls fn with each message.
func forEachMessage(r io.Reader, fn func([]byte) error) error {
	hdr := make([]byte, 8)
	buf := make([]byte, 1<<16)
	var order binary.ByteOrder = binary.LittleEndian
	first := true
	for {
		if _, err := io.ReadFull(r, hdr); err != nil {
			if err == io.EOF && !first {
				return nil
			}
			if err == io.EOF {
				return errors.New("empty input")
			}
			return fmt.Errorf("truncated block header: %w", err)
		}
		btype := order.Uint32(hdr[0:4])
		if first {
			if btype != 0x0A0D0D0A {
				return errors.New("not a pcapng file")
			}
		}
		blen := int(order.Uint32(hdr[4:8]))
		if first {
			// The byte-order magic follows; this reader handles little-endian files only.
			first = false
		}
		if blen < 12 || blen%4 != 0 || blen > 1<<24 {
			return fmt.Errorf("bad block length %d", blen)
		}
		body := blen - 8
		if body > len(buf) {
			buf = make([]byte, body)
		}
		if _, err := io.ReadFull(r, buf[:body]); err != nil {
			return fmt.Errorf("truncated block: %w", err)
		}
		if btype == 0x0A0D0D0A {
			if body < 4 || binary.LittleEndian.Uint32(buf[0:4]) != 0x1A2B3C4D {
				return errors.New("big-endian or malformed pcapng section")
			}
			continue
		}
		if btype != 6 { // Enhanced Packet Block
			continue
		}
		if body < 20 {
			return errors.New("short packet block")
		}
		capLen := int(order.Uint32(buf[12:16]))
		if 20+capLen > body {
			return errors.New("packet longer than its block")
		}
		if err := packetMessages(buf[20:20+capLen], fn); err != nil {
			return err
		}
	}
}

func packetMessages(pkt []byte, fn func([]byte) error) error {
	// Ethernet II carrying IPv4; anything else is not market data.
	if len(pkt) < 14+20 || pkt[12] != 0x08 || pkt[13] != 0x00 {
		return nil
	}
	ihl := int(pkt[14]&0x0f) * 4
	if ihl < 20 || pkt[14+9] != 17 { // UDP only
		return nil
	}
	p := pkt[14:]
	if len(p) < ihl+8 {
		return nil
	}
	p = p[ihl+8:]
	if len(p) < 40 {
		return nil
	}
	count := int(binary.LittleEndian.Uint16(p[14:16]))
	payloadLen := int(binary.LittleEndian.Uint16(p[12:14]))
	p = p[40:]
	if payloadLen > len(p) {
		return errors.New("IEX-TP payload longer than the packet")
	}
	p = p[:payloadLen]
	for i := 0; i < count; i++ {
		if len(p) < 2 {
			return errors.New("IEX-TP message count larger than its payload")
		}
		l := int(binary.LittleEndian.Uint16(p[0:2]))
		if l == 0 || 2+l > len(p) {
			return errors.New("IEX-TP message longer than its payload")
		}
		if err := fn(p[2 : 2+l]); err != nil {
			return err
		}
		p = p[2+l:]
	}
	return nil
}
