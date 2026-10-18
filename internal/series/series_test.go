package series

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/vibistic-hq/vibistic-iex-data/internal/day"
)

var gen = time.Date(2026, 10, 13, 7, 30, 0, 0, time.UTC)

// sessions returns n consecutive weekday dates starting 2026-01-05.
func sessionDates(n int) []string {
	var out []string
	d := time.Date(2026, 1, 5, 0, 0, 0, 0, time.UTC)
	for len(out) < n {
		if d.Weekday() != time.Saturday && d.Weekday() != time.Sunday {
			out = append(out, d.Format("2006-01-02"))
		}
		d = d.AddDate(0, 0, 1)
	}
	return out
}

// bar is one symbol's day: open, close, IEX dollar volume. High/low bracket
// open and close by 1%.
type bar struct{ o, c, notional float64 }

func line(sym string, b bar) day.Line {
	h, l := b.o, b.c
	if l > h {
		h, l = l, h
	}
	v := int64(b.notional / ((b.o + b.c) / 2))
	return day.Line{S: sym, O: b.o, H: h * 1.01, L: l * 0.99, C: b.c, V: v, PV: b.notional, N: 10, LN: 10}
}

// build makes days from per-symbol bar lists (nil = no trade that day).
func build(t *testing.T, n int, bars map[string][]*bar, reviews []Review) map[string]*File {
	t.Helper()
	dates := sessionDates(n)
	days := make([]Day, n)
	for i, d := range dates {
		days[i] = Day{Date: d, Lines: map[string]day.Line{}}
		for sym, bs := range bars {
			if bs[i] != nil {
				days[i].Lines[sym] = line(sym, *bs[i])
			}
		}
	}
	out, err := Build(days, reviews, gen)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// steady is n sessions at a price with $1M of IEX trading each.
func steady(n int, price float64) []*bar {
	out := make([]*bar, n)
	for i := range out {
		out[i] = &bar{price, price, 1e6}
	}
	return out
}

func TestTwoForOneSplitIsAdjusted(t *testing.T) {
	bs := steady(30, 100)
	for i := 20; i < 30; i++ {
		bs[i] = &bar{50, 50, 1.2e6}
	}
	f := build(t, 30, map[string][]*bar{"APH": bs}, nil)["APH"]
	if f.Coverage != "ok" || len(f.Splits) != 1 || len(f.Breaks) != 0 {
		t.Fatalf("file = %+v", f)
	}
	if s := f.Splits[0]; s.New != 2 || s.Old != 1 || s.How != "detected" || s.Date != sessionDates(30)[20] {
		t.Fatalf("split = %+v", s)
	}
	if len(f.Bars) != 30 || f.Bars[0][4] != 50 || f.Bars[29][4] != 50 {
		t.Fatalf("bars not adjusted: first %v last %v", f.Bars[0], f.Bars[29])
	}
	// Volume doubles before the split: same dollars, twice the shares.
	if f.Bars[0][5] != 2*float64(int64(1e6/100)) {
		t.Fatalf("volume = %v", f.Bars[0][5])
	}
}

func TestReverseSplitOfTenOrMoreNeedsNoVolumeCheck(t *testing.T) {
	bs := steady(30, 1)
	for i := 3; i < 30; i++ { // too few prior sessions for the volume check
		bs[i] = &bar{19.6, 19.6, 40e6}
	}
	f := build(t, 30, map[string][]*bar{"GOSS": bs}, nil)["GOSS"]
	if len(f.Splits) != 1 || f.Splits[0].New != 1 || f.Splits[0].Old != 20 || len(f.Breaks) != 0 {
		t.Fatalf("splits %+v breaks %+v", f.Splits, f.Breaks)
	}
}

func TestCrashOnHeavyTradingIsABreakNotASplit(t *testing.T) {
	// RARE fell 48% overnight on 10x its usual trading: a real move that
	// looks like a 2-for-1 split. It must never be smoothed over.
	bs := steady(30, 100)
	bs[20] = &bar{52, 53, 10e6}
	for i := 21; i < 30; i++ {
		bs[i] = &bar{53, 53, 2e6}
	}
	f := build(t, 30, map[string][]*bar{"RARE": bs}, nil)["RARE"]
	if len(f.Splits) != 0 || len(f.Breaks) != 1 || f.Breaks[0].Kind != "jump" || f.Breaks[0].Move != -0.48 {
		t.Fatalf("splits %+v breaks %+v", f.Splits, f.Breaks)
	}
	if len(f.Bars) != 10 || f.Bars[0][0] != dateNum(sessionDates(30)[20]) {
		t.Fatalf("history should start at the break, got %d bars from %v", len(f.Bars), f.Bars[0][0])
	}
}

func TestPossibleThreeForTwoCutsTheHistory(t *testing.T) {
	// -33% at normal dollar volume: a 3-for-2 split (PCAR 2023-02-08) or an
	// earnings drop, no way to tell. Neither adjusted nor left in: cut.
	bs := steady(30, 150)
	for i := 20; i < 30; i++ {
		bs[i] = &bar{100, 100, 1.5e6}
	}
	f := build(t, 30, map[string][]*bar{"PCAR": bs}, nil)["PCAR"]
	if len(f.Splits) != 0 || len(f.Breaks) != 1 || f.Breaks[0].Kind != "jump" || len(f.Bars) != 10 {
		t.Fatalf("splits %+v breaks %+v bars %d", f.Splits, f.Breaks, len(f.Bars))
	}
	// The same gap on heavy trading is an earnings drop (TTD 2025-08-08):
	// under the 40% break line, it stays as a real move.
	bs = steady(30, 150)
	bs[20] = &bar{100, 100, 4e6}
	for i := 21; i < 30; i++ {
		bs[i] = &bar{100, 100, 1e6}
	}
	f = build(t, 30, map[string][]*bar{"TTD": bs}, nil)["TTD"]
	if len(f.Splits) != 0 || len(f.Breaks) != 0 || len(f.Bars) != 30 || f.Bars[0][4] != 150 {
		t.Fatalf("splits %+v breaks %+v bars %d", f.Splits, f.Breaks, len(f.Bars))
	}
}

func TestDropUnderThirtyPercentIsNeverCut(t *testing.T) {
	// -29% is within 8% of 2/3, but under the 30% line no gap is a split.
	bs := steady(30, 100)
	for i := 20; i < 30; i++ {
		bs[i] = &bar{71, 71, 1e6}
	}
	f := build(t, 30, map[string][]*bar{"KOD": bs}, nil)["KOD"]
	if len(f.Splits) != 0 || len(f.Breaks) != 0 || len(f.Bars) != 30 {
		t.Fatalf("splits %+v breaks %+v bars %d", f.Splits, f.Breaks, len(f.Bars))
	}
}

func TestEarningsJumpIsNotAReverseSplit(t *testing.T) {
	// +33% at normal volume once passed for a 3-for-4 reverse split.
	bs := steady(30, 75)
	for i := 20; i < 30; i++ {
		bs[i] = &bar{100, 100, 1.2e6}
	}
	f := build(t, 30, map[string][]*bar{"AAOI": bs}, nil)["AAOI"]
	if len(f.Splits) != 0 || len(f.Breaks) != 0 || f.Bars[0][4] != 75 {
		t.Fatalf("splits %+v breaks %+v", f.Splits, f.Breaks)
	}
}

func TestSmallerMovesStayAsTheyAre(t *testing.T) {
	bs := steady(30, 100)
	for i := 20; i < 30; i++ {
		bs[i] = &bar{75, 75, 8e6} // -25%: real, below both thresholds
	}
	f := build(t, 30, map[string][]*bar{"KOD": bs}, nil)["KOD"]
	if len(f.Splits) != 0 || len(f.Breaks) != 0 || len(f.Bars) != 30 || f.Bars[0][4] != 100 {
		t.Fatalf("file = %+v", f)
	}
}

func TestLongQuietStretchBreaksTheHistory(t *testing.T) {
	bs := steady(100, 10)      // the gap is before the 60 sessions coverage looks at
	for i := 10; i < 32; i++ { // 22 sessions with no IEX trade
		bs[i] = nil
	}
	f := build(t, 100, map[string][]*bar{"REUSE": bs}, nil)["REUSE"]
	if len(f.Breaks) != 1 || f.Breaks[0].Kind != "quiet" || f.Breaks[0].Sessions != 22 {
		t.Fatalf("breaks = %+v", f.Breaks)
	}
	if len(f.Bars) != 68 {
		t.Fatalf("bars = %d", len(f.Bars))
	}
}

func TestReviewedEntriesWin(t *testing.T) {
	dates := sessionDates(30)
	crash := steady(30, 100)
	crash[20] = &bar{52, 53, 10e6}
	for i := 21; i < 30; i++ {
		crash[i] = &bar{53, 53, 2e6}
	}
	split := steady(30, 100)
	for i := 20; i < 30; i++ {
		split[i] = &bar{34, 34, 20e6} // a 3-for-1 split on heavy trading the rule rejects
	}
	quiet := steady(30, 100)
	quiet[25] = &bar{101, 101, 1e6}
	reviews := []Review{
		{Symbol: "CRASH", Date: dates[20], Kind: "real", Note: "trial failure, a real move"},
		{Symbol: "SPLIT", Date: dates[20], Kind: "split", New: 3, Old: 1, Note: "3-for-1, company press release"},
		{Symbol: "FORCE", Date: dates[25], Kind: "break", Note: "merger"},
	}
	out := build(t, 30, map[string][]*bar{"CRASH": crash, "SPLIT": split, "FORCE": quiet}, reviews)
	if f := out["CRASH"]; len(f.Breaks) != 0 || len(f.Splits) != 0 || len(f.Bars) != 30 {
		t.Fatalf("CRASH = %+v", f)
	}
	if f := out["SPLIT"]; len(f.Splits) != 1 || f.Splits[0].How != "reviewed" || len(f.Breaks) != 0 || f.Bars[0][4] != 33.3333 {
		t.Fatalf("SPLIT = %+v %v", f.Splits, f.Bars[0])
	}
	if f := out["FORCE"]; len(f.Breaks) != 1 || f.Breaks[0].Kind != "reviewed" || len(f.Bars) != 5 {
		t.Fatalf("FORCE = %+v", f.Breaks)
	}
}

func TestSparseCoverageCarriesNoBars(t *testing.T) {
	thin := steady(60, 10)
	for i := range thin {
		thin[i].notional = 20_000 // under $50k a day
	}
	gappy := steady(60, 10)
	for i := 0; i < 60; i += 5 { // misses 12 of 60 sessions
		gappy[i] = nil
	}
	out := build(t, 60, map[string][]*bar{"THIN": thin, "GAPPY": gappy, "OK": steady(60, 10)}, nil)
	for _, sym := range []string{"THIN", "GAPPY"} {
		if f := out[sym]; f.Coverage != "sparse" || len(f.Bars) != 0 {
			t.Fatalf("%s = %+v", sym, f)
		}
	}
	if m := out["GAPPY"].Metrics; m.Sessions != 60 || m.PricedSessions != 48 {
		t.Fatalf("GAPPY metrics = %+v", m)
	}
	if f := out["OK"]; f.Coverage != "ok" || len(f.Bars) != 60 || f.Metrics.MedianNotional != 1e6 {
		t.Fatalf("OK = %+v", f.Metrics)
	}
}

func TestOnlyAppShapedSymbolsAndFourYears(t *testing.T) {
	days := []Day{
		{Date: "2022-01-03", Lines: map[string]day.Line{"AAPL": line("AAPL", bar{1, 1, 1e6})}},
		{Date: "2026-10-12", Lines: map[string]day.Line{"AAPL": line("AAPL", bar{1, 1, 1e6}), "AHL-F": line("AHL-F", bar{1, 1, 1e6}), "BRK.B": line("BRK.B", bar{1, 1, 1e6})}},
	}
	out, err := Build(days, nil, gen)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := out["AHL-F"]; ok || out["BRK.B"] == nil {
		t.Fatalf("symbols = %v", keys(out))
	}
	// 2022-01-03 is more than 1461 calendar days before 2026-10-12.
	if f := out["AAPL"]; len(f.Bars) != 1 || f.AsOf != "2026-10-12" || f.Citation != Citation {
		t.Fatalf("AAPL = %+v", f)
	}
}

func TestValidateReviews(t *testing.T) {
	ok := Review{Symbol: "AAPL", Date: "2026-01-05", Kind: "real", Note: "why"}
	bad := []Review{
		{Symbol: "aapl", Date: "2026-01-05", Kind: "real", Note: "x"},
		{Symbol: "AAPL", Date: "5 Jan", Kind: "real", Note: "x"},
		{Symbol: "AAPL", Date: "2026-01-05", Kind: "maybe", Note: "x"},
		{Symbol: "AAPL", Date: "2026-01-05", Kind: "split", New: 2, Old: 2, Note: "x"},
		{Symbol: "AAPL", Date: "2026-01-05", Kind: "real", New: 2, Old: 1, Note: "x"},
		{Symbol: "AAPL", Date: "2026-01-05", Kind: "real"},
	}
	for i, r := range bad {
		if err := ValidateReviews([]Review{r}); err == nil {
			t.Errorf("case %d: want an error", i)
		}
	}
	if err := ValidateReviews([]Review{ok, ok}); err == nil || !strings.Contains(err.Error(), "twice") {
		t.Errorf("duplicate: %v", err)
	}
	if err := ValidateReviews([]Review{ok}); err != nil {
		t.Error(err)
	}
}

func TestDetectSplitRatios(t *testing.T) {
	cases := []struct {
		gap      float64
		spike    float64
		new, old int
		ok       bool
	}{
		{0.501, 1, 2, 1, true},
		{0.335, 1, 3, 1, true},
		{3.915, 2.2, 1, 4, true},
		{3.915, 3.8, 0, 0, false}, // over 2.5x the usual dollars: a real move
		{0.5, 2.8, 0, 0, false},   // a -50% crash (NTLA 2025-10-27 traded 2.8x)
		{0.67, 1, 0, 0, false},    // 3:2 is not looked for: mostly earnings drops (TTD 2025-08-08)
		{1.333, 1, 0, 0, false},   // nor 3:4
		{9.0, 1, 0, 0, false},     // nor 1:9; 10% from 1:10, 12.5% from 1:8
		{0.142, 1, 7, 1, true},
		{0.524, 10, 0, 0, false}, // trading burst: real move
		{1.88, 13.6, 0, 0, false},
		{1.2, 1, 0, 0, false},   // too small to be a split
		{0.445, 1, 0, 0, false}, // 11% from 1/2
		{78.9, 30, 1, 80, true}, // 1-for-80 needs no volume check
		{0.0505, 1, 20, 1, true},
		{15.6, 1, 1, 15, true}, // no 1:16: such gaps were 1:15 or 1:20
		{23, 1, 0, 0, false},   // 8% from 25: an odd ratio is a break, not a guess
		{110, 1, 0, 0, false},  // 9% from 100 and 120
	}
	for _, c := range cases {
		n, o, ok := detectSplit(c.gap, c.spike, 1, true)
		if ok != c.ok || n != c.new || o != c.old {
			t.Errorf("gap %v spike %v: got %d/%d %v, want %d/%d %v", c.gap, c.spike, n, o, ok, c.new, c.old, c.ok)
		}
	}
	if _, _, ok := detectSplit(0.5, 1, 0, false); ok {
		t.Error("no prior trading: a 2x gap must not count as a split")
	}
}

func keys(m map[string]*File) string {
	var s []string
	for k := range m {
		s = append(s, k)
	}
	return fmt.Sprint(s)
}

func TestOnlyEventsInsideTheServedHistoryAreListed(t *testing.T) {
	// A 2-for-1 split, then a crash that cuts the history: the split did not
	// shape any bar served, so it is not listed; the crash is.
	bs := steady(40, 100)
	for i := 10; i < 40; i++ {
		bs[i] = &bar{50, 50, 1e6}
	}
	bs[25] = &bar{20, 20, 30e6}
	for i := 26; i < 40; i++ {
		bs[i] = &bar{20, 20, 2e6}
	}
	f := build(t, 40, map[string][]*bar{"X": bs}, nil)["X"]
	dates := sessionDates(40)
	if len(f.Splits) != 0 || len(f.Breaks) != 1 || f.Breaks[0].Date != dates[25] || len(f.Bars) != 15 {
		t.Fatalf("splits %+v breaks %+v bars %d", f.Splits, f.Breaks, len(f.Bars))
	}
}

func TestOddLotOnlySessionsGiveNoBar(t *testing.T) {
	dates := sessionDates(3)
	days := []Day{}
	for i, d := range dates {
		l := line("BRK.A", bar{700000, 700000, 1e6})
		if i == 1 {
			l = day.Line{S: "BRK.A", V: 3, PV: 2.1e6, N: 3, LN: 0} // odd lots only: no price
		}
		days = append(days, Day{Date: d, Lines: map[string]day.Line{"BRK.A": l}})
	}
	out, err := Build(days, nil, gen)
	if err != nil {
		t.Fatal(err)
	}
	f := out["BRK.A"]
	if f.Metrics.PricedSessions != 2 || f.Metrics.LastPricedDate != dates[2] || f.Metrics.MedianNotional != 1e6 {
		t.Fatalf("metrics = %+v", f.Metrics)
	}
	// Two of three sessions priced is under 90%: sparse.
	if f.Coverage != "sparse" {
		t.Fatalf("coverage = %s", f.Coverage)
	}
}

func TestReviewedEntriesThatApplyToNothingFailTheBuild(t *testing.T) {
	dates := sessionDates(30)
	days := make([]Day, 30)
	for i, d := range dates {
		days[i] = Day{Date: d, Lines: map[string]day.Line{"AAPL": line("AAPL", bar{100, 100, 1e6}), "THIN": line("THIN", bar{1, 1, 100})}}
	}
	for name, r := range map[string]Review{
		"symbol typo":   {Symbol: "APPL", Date: dates[10], Kind: "real", Note: "x"},
		"weekend date":  {Symbol: "AAPL", Date: "2026-01-10", Kind: "real", Note: "x"},
		"first session": {Symbol: "AAPL", Date: dates[0], Kind: "break", Note: "x"},
	} {
		if _, err := Build(days, []Review{r}, gen); err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
	for name, r := range map[string]Review{
		"before the archive": {Symbol: "AAPL", Date: "2020-01-02", Kind: "real", Note: "x"},
		"sparse stock":       {Symbol: "THIN", Date: dates[10], Kind: "real", Note: "x"},
		"applies":            {Symbol: "AAPL", Date: dates[10], Kind: "break", Note: "x"},
	} {
		if _, err := Build(days, []Review{r}, gen); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestReviewedEntriesMustFitTheGap(t *testing.T) {
	dates := sessionDates(30)
	bs := steady(30, 100)
	for i := 20; i < 30; i++ {
		bs[i] = &bar{34, 34, 20e6}
	}
	for name, r := range map[string]Review{
		"split on a normal day":     {Symbol: "X", Date: dates[19], Kind: "split", New: 3, Old: 1, Note: "x"},
		"wrong ratio":               {Symbol: "X", Date: dates[20], Kind: "split", New: 1, Old: 3, Note: "x"},
		"real move on a normal day": {Symbol: "X", Date: dates[21], Kind: "real", Note: "x"},
	} {
		days := make([]Day, 30)
		for i, d := range dates {
			days[i] = Day{Date: d, Lines: map[string]day.Line{"X": line("X", *bs[i])}}
		}
		if _, err := Build(days, []Review{r}, gen); err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
	// A forced break can go on any day.
	out := build(t, 30, map[string][]*bar{"X": bs}, []Review{{Symbol: "X", Date: dates[22], Kind: "break", Note: "x"}})
	if f := out["X"]; len(f.Breaks) != 1 || f.Breaks[0].Date != dates[22] {
		t.Fatalf("breaks = %+v", f.Breaks)
	}
}

// Issue #10: the split check compares with trading since the last break only.
// A reused ticker's old, much busier company must not make a crash on heavy
// trading (for the new company) look like a split.
func TestPriorVolumeComesOnlyFromAfterTheBreak(t *testing.T) {
	bs := make([]*bar, 0, 150)
	for i := 0; i < 50; i++ {
		bs = append(bs, &bar{10, 10, 50e6}) // the old company: $50M a day
	}
	for i := 0; i < 22; i++ {
		bs = append(bs, nil) // a quiet stretch: the ticker is reused
	}
	for i := 0; i < 10; i++ {
		bs = append(bs, &bar{100, 100, 1e6}) // the new company: $1M a day
	}
	bs = append(bs, &bar{52, 53, 10e6}) // -48% on 10x its own usual trading
	for len(bs) < 150 {
		bs = append(bs, &bar{53, 53, 1e6})
	}
	f := build(t, 150, map[string][]*bar{"REUSE": bs}, nil)["REUSE"]
	if len(f.Splits) != 0 {
		t.Fatalf("a crash was taken for a split: %+v", f.Splits)
	}
	if n := len(f.Breaks); n != 1 || f.Breaks[0].Kind != "jump" {
		t.Fatalf("breaks = %+v", f.Breaks)
	}
}

// Issue #10: right after a break there is too little trading to judge a
// -33% gap, so it is cut, as at normal volume.
func TestThreeForTwoShapedGapRightAfterABreakIsCut(t *testing.T) {
	bs := steady(100, 10)
	for i := 10; i < 32; i++ {
		bs[i] = nil
	}
	bs[32] = &bar{150, 150, 1e6}
	bs[33] = &bar{150, 150, 1e6}
	for i := 34; i < 100; i++ {
		bs[i] = &bar{100, 100, 1e6}
	}
	f := build(t, 100, map[string][]*bar{"NEW": bs}, nil)["NEW"]
	if len(f.Splits) != 0 || len(f.Breaks) != 1 || f.Breaks[0].Kind != "jump" || len(f.Bars) != 66 {
		t.Fatalf("splits %+v breaks %+v bars %d", f.Splits, f.Breaks, len(f.Bars))
	}
}

// Issue #10: after a big reverse split, an old session's adjusted volume can
// be under half a share. It is published as that fraction, never 0 (the app
// rejects a file with a zero-volume bar) and never rounded up to 1.
func TestTinyAdjustedVolumeIsNeverZero(t *testing.T) {
	bs := make([]*bar, 30)
	for i := range bs {
		bs[i] = &bar{1, 1, 5} // 5 shares a day at $1
	}
	for i := 10; i < 30; i++ {
		bs[i] = &bar{20, 20, 5e6} // 1-for-20: 5 old shares are 0.25 new ones
	}
	f := build(t, 30, map[string][]*bar{"TINY": bs}, nil)["TINY"]
	if len(f.Splits) != 1 {
		t.Fatalf("splits = %+v", f.Splits)
	}
	if v := f.Bars[0][5]; v != 0.25 {
		t.Fatalf("first bar volume = %v, want 0.25", v)
	}
	if v := f.Bars[29][5]; v != float64(int64(5e6/20)) {
		t.Fatalf("last bar volume = %v", v)
	}
}
