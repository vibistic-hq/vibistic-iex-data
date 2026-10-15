package day

import (
	"bytes"
	"errors"
	"testing"
	"time"

	"github.com/vibistic-hq/vibistic-iex-data/internal/tops"
)

func TestWriteThenRead(t *testing.T) {
	d := &tops.Day{RegularStart: 1, RegularEnd: 2, Trades: 3, Breaks: 1, Symbols: []tops.Summary{
		{Symbol: "AAPL", Open: 1001234, High: 1020000, Low: 990000, Close: 1010000, Volume: 700, Notional: 70700.5, Trades: 5, LotTrades: 4},
	}}
	var buf bytes.Buffer
	if err := Write(&buf, "2026-10-09", d); err != nil {
		t.Fatal(err)
	}
	h, lines, err := Read(&buf)
	if err != nil {
		t.Fatal(err)
	}
	if h.Date != "2026-10-09" || h.Breaks != 1 || len(lines) != 1 {
		t.Fatalf("got %+v %+v", h, lines)
	}
	if l := lines[0]; l.S != "AAPL" || l.O != 100.1234 || l.C != 101 || l.V != 700 || l.PV != 70700.5 || l.LN != 4 {
		t.Fatalf("line = %+v", l)
	}
}

func TestBadInput(t *testing.T) {
	if err := Write(&bytes.Buffer{}, "20261009", &tops.Day{}); err == nil {
		t.Error("bad date written")
	}
	for name, in := range map[string]string{"empty": "", "bad header": "{\"date\":\"x\"}\n", "bad line": "{\"date\":\"2026-10-09\"}\nnot json\n"} {
		if _, _, err := Read(bytes.NewBufferString(in)); err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
}

func TestSessionTimes(t *testing.T) {
	ok := []struct {
		date       string
		start, end int64
	}{
		{"2024-12-20", 1734705000000147000, 1734728400000268000}, // 09:30 to 16:00, standard time
		{"2024-12-24", 1735050600000365000, 1735063200000174000}, // early close, 13:00
		{"2026-10-09", 1791552600000000000, 1791576000000000000}, // daylight time
	}
	for _, c := range ok {
		if err := SessionTimes(c.date, c.start, c.end); err != nil {
			t.Errorf("%s: %v", c.date, err)
		}
	}
	// IEX's real 2024-12-23 file: the session starts at 11:50:19 ET.
	if err := SessionTimes("2024-12-23", 1734972619749895540, 1734987600000174677); !errors.Is(err, ErrIncomplete) {
		t.Errorf("partial day: err = %v, want ErrIncomplete", err)
	}
	bad := []struct {
		name       string
		start, end int64
	}{
		{"late start by two minutes", 1734705120000000000, 1734728400000000000},
		{"late start by two seconds", 1734705002000000000, 1734728400000000000},
		{"start a second early", 1734704999000000000, 1734728400000000000},
		{"end a second early", 1734705000000000000, 1734728399000000000},
		{"end two seconds late", 1734705000000000000, 1734728402000000000},
		{"early end at 15:00", 1734705000000000000, 1734724800000000000},
		{"13:00 close on a full day", 1734705000000000000, 1734717600000000000},
		{"end on another day", 1734705000000000000, 1734814800000000000},
	}
	for _, c := range bad {
		if err := SessionTimes("2024-12-20", c.start, c.end); !errors.Is(err, ErrIncomplete) {
			t.Errorf("%s: err = %v, want ErrIncomplete", c.name, err)
		}
	}
	// A 16:00 close on a half day is not one either.
	if err := SessionTimes("2024-12-24", 1735050600000365000, 1735074000000000000); !errors.Is(err, ErrIncomplete) {
		t.Errorf("16:00 on a half day: err = %v, want ErrIncomplete", err)
	}
	// The wrong date is a different fault, not a skippable day.
	if err := SessionTimes("2024-12-19", 1734705000000147000, 1734728400000268000); err == nil || errors.Is(err, ErrIncomplete) {
		t.Errorf("wrong date: err = %v", err)
	}
}

func TestHalfDay(t *testing.T) {
	// Every 13:00 close in the archive (2022-10 to 2026-10), and no other day.
	half := map[string]bool{"2022-11-25": true, "2023-07-03": true, "2023-11-24": true, "2024-07-03": true, "2024-11-29": true, "2024-12-24": true, "2025-07-03": true, "2025-11-28": true, "2025-12-24": true}
	for d := time.Date(2022, 10, 1, 12, 0, 0, 0, time.UTC); d.Before(time.Date(2026, 10, 13, 0, 0, 0, 0, time.UTC)); d = d.AddDate(0, 0, 1) {
		if d.Weekday() == time.Saturday || d.Weekday() == time.Sunday {
			continue
		}
		if got, want := HalfDay(d), half[d.Format("2006-01-02")]; got != want {
			t.Errorf("%s: HalfDay = %v, want %v", d.Format("2006-01-02"), got, want)
		}
	}
}

func TestHalfDayAhead(t *testing.T) {
	// The rest of 2026 and 2027: the day after Thanksgiving and Christmas Eve
	// 2026 (a Thursday); 2027's July 3 is a Saturday and its December 24 a
	// Friday (both holidays observed), so only the day after Thanksgiving.
	for _, c := range []struct {
		date string
		want bool
	}{{"2026-11-27", true}, {"2026-12-24", true}, {"2026-07-03", false}, {"2027-07-02", false}, {"2027-11-26", true}, {"2027-12-24", false}, {"2027-12-23", false}} {
		d, _ := time.Parse("2006-01-02", c.date)
		if got := HalfDay(d); got != c.want {
			t.Errorf("%s: HalfDay = %v, want %v", c.date, got, c.want)
		}
	}
}
