package earnings

import (
	"testing"
	"time"
)

func d(s string) time.Time {
	t, err := ParseDate(s)
	if err != nil {
		panic(err)
	}
	return t
}

func ds(ss ...string) []time.Time {
	out := make([]time.Time, len(ss))
	for i, s := range ss {
		out[i] = d(s)
	}
	return out
}

func fmtDates(ts []time.Time) []string {
	out := make([]string, len(ts))
	for i, t := range ts {
		out[i] = t.Format(layout)
	}
	return out
}

// Apple's Item 2.02 filings, 2024-05 to 2026-07.
var apple = ds("2024-05-02", "2024-08-01", "2024-10-31", "2025-01-30", "2025-05-01", "2025-07-31", "2025-10-30", "2026-01-29", "2026-04-30", "2026-07-30")

func TestEstimateApple(t *testing.T) {
	next, last, basis, ok := Estimate(apple, d("2026-10-20"))
	if !ok || next.Format(layout) != "2026-10-29" || last.Format(layout) != "2026-07-30" || basis.Format(layout) != "2025-10-30" {
		t.Fatalf("got %v %v %v %v", next, last, basis, ok)
	}
	if next.Weekday() != basis.Weekday() {
		t.Fatal("52 weeks keeps the weekday")
	}
}

func TestReleases(t *testing.T) {
	got, err := Releases(
		[]string{"8-K", "8-K/A", "8-K", "10-Q", "8-K", "8-K"},
		[]string{"2026-01-02", "2026-01-03", "2026-01-04", "2026-01-05", "2026-01-06", "2026-01-07"},
		[]string{"2.02,9.01", "2.02", "7.01", "2.02", "12.02", " 2.02 "},
	)
	if err != nil {
		t.Fatal(err)
	}
	if g := fmtDates(got); len(g) != 2 || g[0] != "2026-01-02" || g[1] != "2026-01-07" {
		t.Fatalf("got %v (only 8-K, only item 2.02 exactly, not 12.02 or amendments)", g)
	}
	if _, err := Releases([]string{"8-K"}, []string{"2026-01-02", "2026-01-03"}, []string{"2.02"}); err == nil {
		t.Fatal("unaligned columns must fail")
	}
	if _, err := Releases([]string{"8-K"}, []string{"2026-02-30"}, []string{"2.02"}); err == nil {
		t.Fatal("a bad date must fail")
	}
}

func TestClusterKeepsLastBoundedByFirst(t *testing.T) {
	// Unsorted, a duplicate; Jan 28 and Jan 31 are within 30 days of the
	// cluster's first filing (Jan 2): one cluster, its last filing kept.
	if got := fmtDates(Cluster(ds("2026-01-28", "2026-01-02", "2026-01-02", "2026-01-31"))); len(got) != 1 || got[0] != "2026-01-31" {
		t.Fatalf("got %v", got)
	}
	if got := fmtDates(Cluster(ds("2026-01-02", "2026-02-02"))); len(got) != 2 {
		t.Fatalf("31 days apart: got %v", got)
	}
	// A filing every 20 days never chains into one cluster longer than MergeDays.
	chain := Cluster(ds("2026-01-01", "2026-01-21", "2026-02-10", "2026-03-02"))
	if g := fmtDates(chain); len(g) != 2 || g[0] != "2026-01-21" || g[1] != "2026-03-02" {
		t.Fatalf("chain: got %v", g)
	}
}

func TestEstimateIgnoresTodayAndLater(t *testing.T) {
	today := d("2026-10-20")
	want, _, _, _ := Estimate(apple, today)
	// Adding filings on or after today never changes the estimate (no look-ahead).
	for _, extra := range []string{"2026-10-20", "2026-10-21", "2026-12-01"} {
		got, _, _, ok := Estimate(append(append([]time.Time(nil), apple...), d(extra)), today)
		if !ok || !got.Equal(want) {
			t.Fatalf("filing on %s changed the estimate to %v", extra, got)
		}
	}
}

func TestEstimateCausalOverEveryDay(t *testing.T) {
	// For every day of a year, the estimate from all filings equals the one
	// from only the filings before that day.
	for day := d("2025-10-01"); day.Before(d("2026-10-20")); day = day.Add(24 * time.Hour) {
		var before []time.Time
		for _, x := range apple {
			if x.Before(day) {
				before = append(before, x)
			}
		}
		a, _, _, okA := Estimate(apple, day)
		b, _, _, okB := Estimate(before, day)
		if okA != okB || !a.Equal(b) {
			t.Fatalf("%s: %v %v vs %v %v", day.Format(layout), a, okA, b, okB)
		}
	}
}

func TestEstimateRightAfterReporting(t *testing.T) {
	// The day after the July release, the next one is October's anniversary,
	// not July's.
	next, _, _, ok := Estimate(apple, d("2026-07-31"))
	if !ok || next.Format(layout) != "2026-10-29" {
		t.Fatalf("got %v %v", next, ok)
	}
	// Reported 3 days earlier than last year: that anniversary (3 days after
	// the release) is the same quarter, skipped.
	early := append(append([]time.Time(nil), apple[:len(apple)-1]...), d("2026-07-27"))
	next, _, _, ok = Estimate(early, d("2026-07-28"))
	if !ok || next.Format(layout) != "2026-10-29" {
		t.Fatalf("early: got %v %v", next, ok)
	}
}

func TestEstimateAmbiguous(t *testing.T) {
	// Tesla-like: a deliveries filing on Oct 2, results ~Oct 22 last year. The
	// anniversary falls 19 days after the last filing: unclear, no estimate.
	tsla := ds("2024-10-23", "2025-01-29", "2025-04-22", "2025-07-23", "2025-10-22", "2026-01-28", "2026-04-22", "2026-07-22", "2026-10-02")
	if _, _, _, ok := Estimate(tsla, d("2026-10-05")); ok {
		t.Fatal("want no estimate")
	}
}

func TestEstimatePassedIsHidden(t *testing.T) {
	// The anniversary (Oct 29) has passed with no filing: the next candidate
	// is too far from the last release, so no estimate rather than a date in
	// the past.
	if next, _, _, ok := Estimate(apple, d("2026-10-30")); ok {
		t.Fatalf("got %v", next)
	}
	// On the day itself it still shows.
	if next, _, _, ok := Estimate(apple, d("2026-10-29")); !ok || next.Format(layout) != "2026-10-29" {
		t.Fatalf("got %v %v", next, ok)
	}
}

func TestEstimateIrregularOrStale(t *testing.T) {
	// Semi-annual filer.
	semi := ds("2024-03-01", "2024-09-01", "2025-03-01", "2025-09-01", "2026-03-01", "2026-09-01")
	if _, _, _, ok := Estimate(semi, d("2026-10-01")); ok {
		t.Fatal("semi-annual: want none")
	}
	// Too few releases.
	if _, _, _, ok := Estimate(apple[len(apple)-4:], d("2026-10-20")); ok {
		t.Fatal("four releases: want none")
	}
	// Last release more than MaxAge days ago.
	if _, _, _, ok := Estimate(apple[:len(apple)-2], d("2026-10-20")); ok {
		t.Fatal("stale: want none")
	}
	// Gap boundaries: exactly MinGap and MaxGap apart is regular.
	base := d("2024-01-01")
	var reg []time.Time
	for i, g := range []int{0, MinGap, MaxGap, MinGap, MaxGap} {
		if i == 0 {
			reg = append(reg, base)
			continue
		}
		reg = append(reg, reg[len(reg)-1].Add(time.Duration(g)*24*time.Hour))
	}
	if !regular(reg) {
		t.Fatal("boundaries are regular")
	}
	reg[len(reg)-1] = reg[len(reg)-1].Add(24 * time.Hour)
	if regular(reg) {
		t.Fatal("MaxGap+1 is not")
	}
}

func regular(rel []time.Time) bool {
	tail := rel[len(rel)-RegularCount:]
	for i := 1; i < len(tail); i++ {
		if g := gapDays(tail[i-1], tail[i]); g < MinGap || g > MaxGap {
			return false
		}
	}
	return true
}

func goodFile() *File {
	return &File{V: 1, Source: "SEC", GeneratedAt: "2026-10-20T09:30:00Z", Today: "2026-10-20", AsOf: "2026-10-19",
		Symbols: map[string]Entry{"AAPL": {CIK: 320193, Next: "2026-10-29", Last: "2026-07-30", Basis: "2025-10-30"}, "BRK.B": {CIK: 1067983, Next: "2026-11-02", Last: "2026-08-11", Basis: "2025-11-03"}}}
}

func TestValidate(t *testing.T) {
	if err := Validate(goodFile()); err != nil {
		t.Fatal(err)
	}
	bad := []func(f *File){
		func(f *File) { f.V = 2 },
		func(f *File) { f.Source = "X" },
		func(f *File) { f.GeneratedAt = "yesterday" },
		func(f *File) { f.AsOf = "2026-10-21" },
		func(f *File) { f.Today = "2026-13-01" },
		func(f *File) { f.Symbols["aapl"] = f.Symbols["AAPL"] },
		func(f *File) { e := f.Symbols["AAPL"]; e.CIK = 0; f.Symbols["AAPL"] = e },
		func(f *File) { e := f.Symbols["AAPL"]; e.Next = "2026-10-30"; f.Symbols["AAPL"] = e },
		func(f *File) { e := f.Symbols["AAPL"]; e.Last = "2026-10-20"; f.Symbols["AAPL"] = e },
		func(f *File) {
			e := f.Symbols["AAPL"]
			e.Next, e.Basis = "2026-10-19", "2025-10-20"
			f.Symbols["AAPL"] = e
		},
		func(f *File) { e := f.Symbols["AAPL"]; e.Last = "2026-05-01"; f.Symbols["AAPL"] = e },
		func(f *File) { e := f.Symbols["AAPL"]; e.Basis = "2025-1-30"; f.Symbols["AAPL"] = e },
	}
	for i, mod := range bad {
		f := goodFile()
		mod(f)
		if Validate(f) == nil {
			t.Errorf("case %d passed", i)
		}
	}
}

func TestNewYorkDay(t *testing.T) {
	// 02:00 UTC on Oct 21 is still Oct 20 in New York.
	if got := NewYorkDay(time.Date(2026, 10, 21, 2, 0, 0, 0, time.UTC)).Format(layout); got != "2026-10-20" {
		t.Fatal(got)
	}
}
