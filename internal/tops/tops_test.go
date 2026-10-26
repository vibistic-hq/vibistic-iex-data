package tops

import (
	"bytes"
	"encoding/binary"
	"strings"
	"testing"
)

// Builders for a tiny pcapng file in the layout IEX publishes: a Section
// Header Block, then Enhanced Packet Blocks of Ethernet / IPv4 / UDP / IEX-TP.

func le16(b []byte, v uint16) []byte { return binary.LittleEndian.AppendUint16(b, v) }
func le32(b []byte, v uint32) []byte { return binary.LittleEndian.AppendUint32(b, v) }
func le64(b []byte, v uint64) []byte { return binary.LittleEndian.AppendUint64(b, v) }

func block(btype uint32, body []byte) []byte {
	for len(body)%4 != 0 {
		body = append(body, 0)
	}
	n := uint32(len(body) + 12)
	b := le32(le32(nil, btype), n)
	b = append(b, body...)
	return le32(b, n)
}

func sectionHeader() []byte {
	body := le32(nil, 0x1A2B3C4D)
	body = le16(le16(body, 1), 0)
	body = le64(body, ^uint64(0))
	return block(0x0A0D0D0A, body)
}

func packet(msgs ...[]byte) []byte {
	var payload []byte
	for _, m := range msgs {
		payload = append(le16(payload, uint16(len(m))), m...)
	}
	tp := []byte{1, 0}
	tp = le16(tp, 0x8003)
	tp = le32(tp, 1)
	tp = le32(tp, 1)
	tp = le16(tp, uint16(len(payload)))
	tp = le16(tp, uint16(len(msgs)))
	tp = append(tp, make([]byte, 24)...)
	tp = append(tp, payload...)
	eth := make([]byte, 12)
	eth = append(eth, 0x08, 0x00)
	ip := make([]byte, 20)
	ip[0] = 0x45
	ip[9] = 17
	udp := make([]byte, 8)
	frame := append(append(append(eth, ip...), udp...), tp...)
	body := le32(nil, 0)
	body = le32(le32(body, 0), 0)
	body = le32(le32(body, uint32(len(frame))), uint32(len(frame)))
	body = append(body, frame...)
	return block(6, body)
}

func systemEvent(code byte, ts int64) []byte {
	return le64([]byte{msgSystemEvent, code}, uint64(ts))
}

func tradeMsg(typ byte, flags byte, ts int64, sym string, size uint32, price int64, id int64) []byte {
	m := []byte{typ, flags}
	m = le64(m, uint64(ts))
	s := []byte(sym + strings.Repeat(" ", 8-len(sym)))
	m = append(m, s...)
	m = le32(m, size)
	m = le64(m, uint64(price))
	return le64(m, uint64(id))
}

const (
	open  = int64(1_000_000)
	close = int64(2_000_000)
)

func file(pkts ...[]byte) []byte {
	b := sectionHeader()
	for _, p := range pkts {
		b = append(b, p...)
	}
	return b
}

func TestParseSummarisesRegularSessionTrades(t *testing.T) {
	data := file(
		packet(systemEvent('R', open)),
		packet(
			tradeMsg(msgTrade, 0x40, open-10, "AAPL", 100, 99_0000, 1),     // before the open
			tradeMsg(msgTrade, 0, open+1, "AAPL", 100, 100_0000, 2),        // open
			tradeMsg(msgTrade, flagOddLot, open+2, "AAPL", 5, 120_0000, 3), // odd lot: volume only
			tradeMsg(msgTrade, 0, open+3, "AAPL", 200, 102_0000, 4),        // high
		),
		packet(
			tradeMsg(msgTrade, 0, open+4, "AAPL", 300, 98_0000, 5),     // low
			tradeMsg(msgTrade, 0, open+5, "AAPL", 50, 150_0000, 6),     // broken below
			tradeMsg(msgTrade, 0x80, open+6, "AAPL", 100, 101_0000, 7), // ISO, counts; close
			tradeMsg(msgTradeBreak, 0, open+7, "AAPL", 50, 150_0000, 6),
			tradeMsg(msgTrade, 0x40, close+1, "AAPL", 100, 90_0000, 8),    // after the close
			tradeMsg(msgTrade, flagOddLot, open+9, "TINY", 3, 10_0000, 9), // odd lots only
		),
		packet(systemEvent('M', close)),
	)
	day, err := Parse(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	if day.RegularStart != open || day.RegularEnd != close || day.Trades != 9 || day.Breaks != 1 {
		t.Fatalf("day header = %+v", day)
	}
	if len(day.Symbols) != 2 {
		t.Fatalf("symbols = %+v", day.Symbols)
	}
	a := day.Symbols[0]
	if a.Symbol != "AAPL" || a.Open != 100_0000 || a.High != 102_0000 || a.Low != 98_0000 || a.Close != 101_0000 {
		t.Fatalf("AAPL ohlc = %+v", a)
	}
	if a.Volume != 705 || a.Trades != 5 || a.LotTrades != 4 {
		t.Fatalf("AAPL volume/trades = %+v", a)
	}
	wantNotional := 100.0*100 + 120.0*5 + 102.0*200 + 98.0*300 + 101.0*100
	if a.Notional != wantNotional {
		t.Fatalf("AAPL notional = %v, want %v", a.Notional, wantNotional)
	}
	tiny := day.Symbols[1]
	if tiny.Symbol != "TINY" || tiny.LotTrades != 0 || tiny.Open != 0 || tiny.Volume != 3 {
		t.Fatalf("TINY = %+v", tiny)
	}
}

func TestParseHalfDayUsesTheExchangesClose(t *testing.T) {
	early := open + 100
	data := file(
		packet(systemEvent('R', open), systemEvent('M', early)),
		packet(tradeMsg(msgTrade, 0, open+50, "SPY", 100, 500_0000, 1), tradeMsg(msgTrade, 0, early+1, "SPY", 100, 510_0000, 2)),
	)
	day, err := Parse(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	if s := day.Symbols[0]; s.Close != 500_0000 || s.Volume != 100 {
		t.Fatalf("half day = %+v", s)
	}
}

func TestParseRejectsBadInput(t *testing.T) {
	good := file(packet(systemEvent('R', open), systemEvent('M', close)))
	cases := map[string][]byte{
		"empty":            nil,
		"not pcapng":       bytes.Repeat([]byte{1}, 64),
		"no session times": file(packet(tradeMsg(msgTrade, 0, open, "AAPL", 1, 1, 1))),
		"truncated":        good[:len(good)-6],
		"short trade":      file(packet(systemEvent('R', open), systemEvent('M', close), []byte{msgTrade, 0, 1, 2, 3})),
	}
	for name, data := range cases {
		if _, err := Parse(bytes.NewReader(data)); err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
	if _, err := Parse(bytes.NewReader(good)); err != nil {
		t.Fatalf("good file: %v", err)
	}
}

func TestParseRejectsMessageCountBeyondPayload(t *testing.T) {
	pkt := packet(systemEvent('R', open))
	// Claim two messages in a packet that holds one. The count sits 14 bytes
	// into the IEX-TP header, after the 28-byte EPB header and 42 bytes of
	// Ethernet, IPv4 and UDP.
	off := 8 + 20 + 42 + 14
	binary.LittleEndian.PutUint16(pkt[off:], 2)
	if _, err := Parse(bytes.NewReader(file(pkt, packet(systemEvent('M', close))))); err == nil {
		t.Fatal("want an error")
	}
}

// Security audit run-1: hostile files fail cleanly instead of exhausting memory or
// carrying odd symbol bytes into the public day files.
func TestParseRejectsHostileInput(t *testing.T) {
	withTrades := func(msgs ...[]byte) []byte {
		return file(packet(append([][]byte{systemEvent('R', open), systemEvent('M', close)}, msgs...)...))
	}
	for name, sym := range map[string]string{"blank": "", "NUL": "\x00AB", "high byte": "\xffX", "tab": "A\tB"} {
		if _, err := Parse(bytes.NewReader(withTrades(tradeMsg(msgTrade, 0, open+1, sym, 1, 10000, 1)))); err == nil {
			t.Errorf("symbol %s: want an error", name)
		}
	}
	if _, err := Parse(bytes.NewReader(withTrades(tradeMsg(msgTrade, 0, open+1, "BRK.B", 1, 10000, 1)))); err != nil {
		t.Fatalf("printable symbol: %v", err)
	}

	defer func(t0 int64, s0 int, b0 int64) { maxTrades, maxSymbols, maxBreaks = t0, s0, b0 }(maxTrades, maxSymbols, maxBreaks)
	maxTrades, maxSymbols, maxBreaks = 3, 2, 1
	three := withTrades(tradeMsg(msgTrade, 0, open+1, "A", 1, 10000, 1), tradeMsg(msgTrade, 0, open+2, "A", 1, 10000, 2), tradeMsg(msgTrade, 0, open+3, "A", 1, 10000, 3))
	if _, err := Parse(bytes.NewReader(three)); err != nil {
		t.Fatalf("at the trade limit: %v", err)
	}
	cases := map[string][]byte{
		"too many trades":  withTrades(tradeMsg(msgTrade, 0, open+1, "A", 1, 10000, 1), tradeMsg(msgTrade, 0, open+2, "A", 1, 10000, 2), tradeMsg(msgTrade, 0, open+3, "A", 1, 10000, 3), tradeMsg(msgTrade, 0, open+4, "A", 1, 10000, 4)),
		"too many symbols": withTrades(tradeMsg(msgTrade, 0, open+1, "A", 1, 10000, 1), tradeMsg(msgTrade, 0, open+2, "B", 1, 10000, 2), tradeMsg(msgTrade, 0, open+3, "C", 1, 10000, 3)),
		"too many breaks":  withTrades(tradeMsg(msgTradeBreak, 0, open+1, "A", 1, 10000, 1), tradeMsg(msgTradeBreak, 0, open+2, "A", 1, 10000, 2)),
	}
	for name, data := range cases {
		if _, err := Parse(bytes.NewReader(data)); err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
}

// A packet may not run into the block's trailing length (security audit run-1).
func TestParseRejectsPacketOverTrailingLength(t *testing.T) {
	pkt := packet(systemEvent('R', open), systemEvent('M', close))
	// The block is type, length, the 20-byte EPB header (capLen at 12:16 of the
	// body), the frame and the trailing length; claim 4 more captured bytes.
	capAt := 8 + 12
	capLen := binary.LittleEndian.Uint32(pkt[capAt : capAt+4])
	bad := append([]byte(nil), pkt...)
	binary.LittleEndian.PutUint32(bad[capAt:capAt+4], capLen+4)
	if _, err := Parse(bytes.NewReader(append(sectionHeader(), bad...))); err == nil {
		t.Fatal("want an error")
	}
}
