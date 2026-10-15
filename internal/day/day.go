// Package day is the on-disk form of one day's summary: JSON lines, a header
// line first, then one line per symbol. cmd/iexagg writes it; cmd/build reads
// the archive of them.
package day

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"time"
	_ "time/tzdata" // New York time without the system zone database

	"github.com/vibistic-hq/vibistic-iex-data/internal/tops"
)

// Header is the first line of a day file.
type Header struct {
	Date         string `json:"date"` // YYYY-MM-DD, the session's New York date
	RegularStart int64  `json:"regularStart"`
	RegularEnd   int64  `json:"regularEnd"`
	Trades       int64  `json:"trades"`
	Breaks       int64  `json:"breaks"`
}

// Line is one symbol's day. Prices in dollars (four decimals, as IEX sends
// them); o/h/l/c are zero when the symbol had only odd-lot trades.
type Line struct {
	S  string  `json:"s"`
	O  float64 `json:"o"`
	H  float64 `json:"h"`
	L  float64 `json:"l"`
	C  float64 `json:"c"`
	V  int64   `json:"v"`
	PV float64 `json:"pv"`
	N  int64   `json:"n"`
	LN int64   `json:"ln"`
}

var dateRE = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)

// Write writes a parsed day.
func Write(w io.Writer, date string, d *tops.Day) error {
	if !dateRE.MatchString(date) {
		return fmt.Errorf("bad date %q", date)
	}
	bw := bufio.NewWriter(w)
	enc := json.NewEncoder(bw)
	if err := enc.Encode(Header{Date: date, RegularStart: d.RegularStart, RegularEnd: d.RegularEnd, Trades: d.Trades, Breaks: d.Breaks}); err != nil {
		return err
	}
	for _, s := range d.Symbols {
		l := Line{S: s.Symbol, O: dollars(s.Open), H: dollars(s.High), L: dollars(s.Low), C: dollars(s.Close), V: s.Volume, PV: s.Notional, N: s.Trades, LN: s.LotTrades}
		if err := enc.Encode(l); err != nil {
			return err
		}
	}
	return bw.Flush()
}

func dollars(p int64) float64 { return float64(p) / 10000 }

// Read reads a day file.
func Read(r io.Reader) (Header, []Line, error) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 1<<16), 1<<20)
	var h Header
	var lines []Line
	first := true
	for sc.Scan() {
		if first {
			first = false
			if err := json.Unmarshal(sc.Bytes(), &h); err != nil {
				return h, nil, fmt.Errorf("header: %w", err)
			}
			if !dateRE.MatchString(h.Date) {
				return h, nil, fmt.Errorf("bad date %q", h.Date)
			}
			continue
		}
		var l Line
		if err := json.Unmarshal(sc.Bytes(), &l); err != nil {
			return h, nil, err
		}
		lines = append(lines, l)
	}
	if err := sc.Err(); err != nil {
		return h, nil, err
	}
	if first {
		return h, nil, errors.New("empty day file")
	}
	return h, lines, nil
}

// ErrIncomplete marks a day whose IEX file does not cover the whole regular
// session (IEX's 2024-12-23 file starts at 11:50 ET): it is skipped, never
// published, since its bars would come from a fraction of the day's trades.
var ErrIncomplete = errors.New("IEX file does not cover the regular session")

// SessionTimes is a day's regular session in New York time: it opens at 09:30
// and closes at 16:00, or at 13:00 on one of the exchanges' half days. A
// 13:00 close on any other day is a file cut short, not a half day.
func SessionTimes(date string, regularStart, regularEnd int64) error {
	ny, err := time.LoadLocation("America/New_York")
	if err != nil {
		return err
	}
	start := time.Unix(0, regularStart).In(ny)
	if got := start.Format("2006-01-02"); got != date {
		return fmt.Errorf("file is for %s, not %s", got, date)
	}
	at := func(h, m int) time.Time { return time.Date(start.Year(), start.Month(), start.Day(), h, m, 0, 0, ny) }
	// IEX stamps both events at most 0.07 s after the mark (every archived
	// day, 2022-10 to 2026-10): a later open or an earlier close would leave
	// out regular-session trades, an earlier open let early trades in.
	near := func(t, want time.Time) bool { d := t.Sub(want); return d >= 0 && d <= time.Second }
	end := time.Unix(0, regularEnd).In(ny)
	closes := at(16, 0)
	if HalfDay(start) {
		closes = at(13, 0)
	}
	if !near(start, at(9, 30)) || !near(end, closes) {
		return fmt.Errorf("%w: %s to %s ET", ErrIncomplete, start.Format("15:04:05"), end.Format("15:04:05"))
	}
	return nil
}

// HalfDay says whether the US stock exchanges close at 13:00 that day: July 3
// and December 24 when they fall Monday to Thursday (on a Friday the holiday
// itself is observed that day), and the day after Thanksgiving (the fourth
// Thursday of November). It matches every half day from 2022-10 to 2026-10.
func HalfDay(t time.Time) bool {
	weekday, day := t.Weekday(), t.Day()
	switch t.Month() {
	case time.July:
		return day == 3 && weekday >= time.Monday && weekday <= time.Thursday
	case time.December:
		return day == 24 && weekday >= time.Monday && weekday <= time.Thursday
	case time.November:
		return weekday == time.Friday && day >= 23 && day <= 29
	}
	return false
}
