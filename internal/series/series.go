// Package series turns the archive of daily IEX summaries into one file per
// symbol: split-adjusted daily bars for the last four years, with every place
// the history could not be trusted cut off rather than guessed across.
//
// IEX's historical data is not adjusted for splits, and no free official
// split list we may redistribute exists, so splits are read from the prices:
//   - An overnight gap (first IEX trade of the day against the last one of
//     the previous session) of 10x or more either way, within 5% of a ratio
//     companies use (10, 12, 15, 20, 25, ...), is a split: no real stock
//     moves that much overnight.
//   - A gap of 30% or more that is within 8% of a common whole-number ratio
//     (2, 3, 1/2, 1/3, ...) is a split only if that day's IEX dollar volume
//     is at most 2.5x its usual level since the last break. A split leaves
//     the dollar value traded about the same; a real move of that size comes
//     with a burst of trading (the spike's false alarms ran 10x to 50x, the
//     real splits 0.1x to 3.8x).
//   - A -33% gap at normal volume could be a 3-for-2 split or an earnings
//     drop: it is never adjusted, the history is cut there instead.
//   - Any other overnight gap of 40% or more, and any stretch of more than 20
//     sessions without an IEX trade (a delisting, a ticker reused by another
//     company), is a break: the history starts again after it.
//
// splits-reviewed.json can confirm a split the rules missed, mark a big gap
// as a real move, or force a break; it wins over the rules.
package series

import (
	"fmt"
	"math"
	"regexp"
	"sort"
	"time"

	"github.com/vibistic-hq/vibistic-iex-data/internal/day"
)

const (
	Citation = "Data provided for free by IEX. By accessing or using IEX Historical Data, you agree to the IEX Historical Data Terms of Use."
	TermsURL = "https://iextrading.com/iex-historical-data-terms/"

	HistoryCalendarDays = 1461 // the app's four-year window
	CoverageSessions    = 60
	MinTradedShare      = 0.9
	MinMedianNotional   = 50_000 // dollars of IEX trading a day
	MaxQuietSessions    = 20
	BigGap              = 0.4 // a 40% move either way
	SplitGap            = 0.3
	SplitTolerance      = 0.08
	BigSplitTolerance   = 0.05
	SplitVolumeSpike    = 2.5
	ThreeForTwoVolume   = 2.0
	SplitVolumeSessions = 20
	SplitVolumeMinPrior = 5
)

// The app's symbol format (src/lib/storage.ts in vibistic-stocks): files are
// made only for symbols the app can ask for.
var SymbolRE = regexp.MustCompile(`^[A-Z][A-Z0-9]{0,5}(\.[A-Z0-9]{1,2})?$`)

// Day is one archived session.
type Day struct {
	Date  string
	Lines map[string]day.Line
}

// Review is one entry of splits-reviewed.json.
type Review struct {
	Symbol string `json:"symbol"`
	Date   string `json:"date"`
	Kind   string `json:"kind"` // "split", "real" or "break"
	New    int    `json:"new,omitempty"`
	Old    int    `json:"old,omitempty"`
	Note   string `json:"note"` // why; stays in the repo, never in the output
}

var dateRE = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)

// ValidateReviews checks splits-reviewed.json's entries.
func ValidateReviews(rs []Review) error {
	seen := map[string]bool{}
	for i, r := range rs {
		key := r.Symbol + " " + r.Date
		switch {
		case !SymbolRE.MatchString(r.Symbol):
			return fmt.Errorf("entry %d: bad symbol %q", i, r.Symbol)
		case !dateRE.MatchString(r.Date):
			return fmt.Errorf("entry %d: bad date %q", i, r.Date)
		case seen[key]:
			return fmt.Errorf("entry %d: %s listed twice", i, key)
		case r.Note == "":
			return fmt.Errorf("entry %d: a note saying why is required", i)
		}
		seen[key] = true
		switch r.Kind {
		case "split":
			if r.New < 1 || r.Old < 1 || r.New > 1000 || r.Old > 1000 || r.New == r.Old {
				return fmt.Errorf("entry %d: split needs new and old share counts (1 to 1000, different)", i)
			}
		case "real", "break":
			if r.New != 0 || r.Old != 0 {
				return fmt.Errorf("entry %d: %s takes no ratio", i, r.Kind)
			}
		default:
			return fmt.Errorf("entry %d: kind must be split, real or break", i)
		}
	}
	return nil
}

// Split: `New` shares for every `Old` share from Date on.
type Split struct {
	Date string `json:"date"`
	New  int    `json:"new"`
	Old  int    `json:"old"`
	How  string `json:"how"` // "detected" or "reviewed"
}

// Break: the history starts again on Date.
type Break struct {
	Date     string  `json:"date"`
	Kind     string  `json:"kind"` // "quiet" (no IEX trade for Sessions sessions), "jump" (a Move overnight) or "reviewed"
	Sessions int     `json:"sessions,omitempty"`
	Move     float64 `json:"move,omitempty"` // overnight change as a fraction, e.g. -0.52
}

// Metrics say how much IEX trading there was to build the bars from. A
// session counts as priced when it had at least one last-sale-eligible IEX
// trade (not only odd lots): only those sessions give a bar.
type Metrics struct {
	Sessions       int     `json:"sessions"`       // sessions looked at (up to 60)
	PricedSessions int     `json:"pricedSessions"` // of which had an IEX price
	MedianNotional float64 `json:"medianNotional"` // dollars of IEX trading (odd lots too) on the median session, 0 when none
	LastPricedDate string  `json:"lastPricedDate"` // "" when none in the archive
}

// File is what the app downloads for one symbol.
type File struct {
	V           int          `json:"v"`
	Symbol      string       `json:"symbol"`
	Source      string       `json:"source"`
	Citation    string       `json:"citation"`
	TermsURL    string       `json:"termsUrl"`
	AsOf        string       `json:"asOf"`
	GeneratedAt string       `json:"generatedAt"`
	Coverage    string       `json:"coverage"` // "ok" or "sparse" (no bars then)
	Metrics     Metrics      `json:"metrics"`
	Splits      []Split      `json:"splits"`
	Breaks      []Break      `json:"breaks"`
	Bars        [][7]float64 `json:"bars"` // [yyyymmdd, o, h, l, c, v, vw], oldest first
}

type session struct {
	at                int // index into the archive's sessions
	date              string
	o, h, l, c, v, pv float64
}

// Build makes every symbol's file. days must be sorted by date.
func Build(days []Day, reviews []Review, generatedAt time.Time) (map[string]*File, error) {
	if len(days) == 0 {
		return nil, fmt.Errorf("no days")
	}
	for i := 1; i < len(days); i++ {
		if days[i].Date <= days[i-1].Date {
			return nil, fmt.Errorf("days out of order or repeated at %s", days[i].Date)
		}
	}
	if err := ValidateReviews(reviews); err != nil {
		return nil, err
	}
	review := map[string]Review{}
	for _, r := range reviews {
		review[r.Symbol+" "+r.Date] = r
	}
	asOf := days[len(days)-1].Date
	asOfT, err := time.Parse("2006-01-02", asOf)
	if err != nil {
		return nil, err
	}
	from := asOfT.AddDate(0, 0, -(HistoryCalendarDays - 1)).Format("2006-01-02")

	all := map[string][]session{}
	notional := map[string]map[int]float64{}
	for i, d := range days {
		for sym, l := range d.Lines {
			if !SymbolRE.MatchString(sym) {
				continue
			}
			if notional[sym] == nil {
				notional[sym] = map[int]float64{}
			}
			notional[sym][i] = l.PV
			if l.LN == 0 || l.O <= 0 || l.H <= 0 || l.L <= 0 || l.C <= 0 || l.V <= 0 {
				continue // odd lots only: no price for the day
			}
			all[sym] = append(all[sym], session{at: i, date: d.Date, o: l.O, h: l.H, l: l.L, c: l.C, v: float64(l.V), pv: l.PV})
		}
	}

	used := map[string]bool{}
	out := map[string]*File{}
	for sym, pv := range notional {
		f := &File{V: 1, Symbol: sym, Source: "IEX", Citation: Citation, TermsURL: TermsURL, AsOf: asOf, GeneratedAt: generatedAt.UTC().Format(time.RFC3339), Splits: []Split{}, Breaks: []Break{}, Bars: [][7]float64{}}
		f.Metrics = metrics(len(days), all[sym], pv)
		f.Coverage = "ok"
		if f.Metrics.PricedSessions < int(math.Ceil(MinTradedShare*float64(f.Metrics.Sessions))) || f.Metrics.MedianNotional < MinMedianNotional {
			f.Coverage = "sparse"
			out[sym] = f
			continue
		}
		sessions := all[sym]
		splits, breaks, start, err := events(sym, sessions, review, used)
		if err != nil {
			return nil, err
		}
		kept := adjust(sessions[start:], splits)
		first := ""
		for _, s := range kept {
			if s.date < from {
				continue
			}
			if first == "" {
				first = s.date
			}
			f.Bars = append(f.Bars, [7]float64{dateNum(s.date), round4(s.o), round4(s.h), round4(s.l), round4(s.c), publishedVolume(s.v), round4(s.pv / s.v)})
		}
		// Only the events that shaped the bars served: a split adjusts the
		// bars before it, so it counts only after the first one; the break
		// the history starts at is the one on the first bar.
		for _, s := range splits {
			if first != "" && s.Date > first {
				f.Splits = append(f.Splits, s)
			}
		}
		for _, b := range breaks {
			if first != "" && b.Date == first {
				f.Breaks = append(f.Breaks, b)
			}
		}
		out[sym] = f
	}
	// A reviewed entry that applies to nothing is a typo or a stale fix: fail
	// rather than publish the bars it was meant to correct. Entries older than
	// the archive, or for a stock whose IEX trading is too sparse to serve,
	// apply to nothing on purpose.
	for _, r := range reviews {
		key := r.Symbol + " " + r.Date
		if used[key] || r.Date < days[0].Date || r.Date > asOf {
			continue
		}
		if f, ok := out[r.Symbol]; ok && f.Coverage == "sparse" {
			continue
		}
		return nil, fmt.Errorf("splits-reviewed.json: %s matches no overnight gap in the archive", key)
	}
	return out, nil
}

func metrics(n int, sessions []session, pv map[int]float64) Metrics {
	first := n - CoverageSessions
	if first < 0 {
		first = 0
	}
	m := Metrics{Sessions: n - first}
	values := make([]float64, 0, m.Sessions)
	traded := map[int]bool{}
	for _, s := range sessions {
		traded[s.at] = true
		m.LastPricedDate = s.date
	}
	for i := first; i < n; i++ {
		if traded[i] {
			m.PricedSessions++
		}
		values = append(values, pv[i])
	}
	sort.Float64s(values)
	if len(values) > 0 {
		mid := len(values) / 2
		if len(values)%2 == 1 {
			m.MedianNotional = values[mid]
		} else {
			m.MedianNotional = (values[mid-1] + values[mid]) / 2
		}
	}
	m.MedianNotional = math.Round(m.MedianNotional)
	return m
}

// events finds the splits and breaks in one symbol's sessions and returns the
// index of the first session after the last break.
func events(sym string, ss []session, review map[string]Review, used map[string]bool) ([]Split, []Break, int, error) {
	var splits []Split
	var breaks []Break
	start := 0
	for i := 1; i < len(ss); i++ {
		a, b := ss[i-1], ss[i]
		gap := b.o / a.c
		if r, ok := review[sym+" "+b.date]; ok {
			if err := reviewMatchesGap(r, gap); err != nil {
				return nil, nil, 0, err
			}
			used[sym+" "+b.date] = true
			switch r.Kind {
			case "split":
				splits = append(splits, Split{Date: b.date, New: r.New, Old: r.Old, How: "reviewed"})
			case "break":
				breaks = append(breaks, Break{Date: b.date, Kind: "reviewed"})
				start = i
			}
			continue // "real": a real move, nothing to do
		}
		if quiet := b.at - a.at - 1; quiet > MaxQuietSessions {
			breaks = append(breaks, Break{Date: b.date, Kind: "quiet", Sessions: quiet})
			start = i
			continue
		}
		prior, havePrior := priorNotional(ss[start:i])
		if newShares, oldShares, ok := detectSplit(gap, b.pv, prior, havePrior); ok {
			splits = append(splits, Split{Date: b.date, New: newShares, Old: oldShares, How: "detected"})
			continue
		}
		if gap >= 1+BigGap || gap <= 1-BigGap || maybeThreeForTwo(gap, b.pv, prior, havePrior) {
			breaks = append(breaks, Break{Date: b.date, Kind: "jump", Move: round4(gap - 1)})
			start = i
		}
	}
	return splits, breaks, start, nil
}

// Reviewed entries exist to correct an overnight discontinuity, so they must
// sit on one: a split on a gap near its ratio, a real move on a big gap. An
// entry typed onto a neighbouring normal day fails the build instead of
// adjusting the wrong bars. A forced break can go anywhere (a merger need not
// gap); on the wrong day it only cuts more history, which fails safe.
const (
	ReviewedSplitMinMove   = 0.2
	ReviewedSplitTolerance = 0.25
)

func reviewMatchesGap(r Review, gap float64) error {
	switch r.Kind {
	case "split":
		m := float64(r.Old) / float64(r.New)
		if math.Abs(gap-1) < ReviewedSplitMinMove || math.Abs(gap/m-1) > ReviewedSplitTolerance {
			return fmt.Errorf("splits-reviewed.json: %s %s: a %d-for-%d split does not fit the overnight change of %+.1f%%", r.Symbol, r.Date, r.New, r.Old, (gap-1)*100)
		}
	case "real":
		if gap > 1-SplitGap && gap < 1+SplitGap {
			return fmt.Errorf("splits-reviewed.json: %s %s: the overnight change of %+.1f%% is too small to need a review", r.Symbol, r.Date, (gap-1)*100)
		}
	}
	return nil
}

// publishedVolume rounds a session's split-adjusted share volume to whole
// shares, but never below 1: after a large reverse split, an old session's
// adjusted volume can be a fraction of a share, and a 0 would make the app
// (and the preview check, internal/og) reject the whole file. The session
// did trade; its VWAP still uses the unrounded volume.
func publishedVolume(v float64) float64 {
	return math.Max(1, math.Round(v))
}

// priorNotional takes only the sessions since the last break (the caller
// passes ss[start:i]): a ticker reused by another company, or a history
// cut at a jump, must not lend its old trading level to the new one's
// split check.
func priorNotional(before []session) (float64, bool) {
	if len(before) > SplitVolumeSessions {
		before = before[len(before)-SplitVolumeSessions:]
	}
	if len(before) < SplitVolumeMinPrior {
		return 0, false
	}
	v := make([]float64, len(before))
	for i, s := range before {
		v[i] = s.pv
	}
	sort.Float64s(v)
	if len(v)%2 == 1 {
		return v[len(v)/2], true
	}
	return (v[len(v)/2-1] + v[len(v)/2]) / 2, true
}

// Split ratios looked for under 10, as new-shares/old-shares pairs (a 2-for-1
// split halves the price): whole numbers companies use. No 3:2 or 3:4: a
// -33% or +33% gap with them is far more often earnings than a split, and
// adjusting one rescales all of the history before it. No 9: a
// 1-for-10 seen through a day's drift looks like one.
var commonRatios = func() [][2]int {
	var r [][2]int
	for _, k := range []int{2, 3, 4, 5, 6, 7, 8, 10, 12, 15, 20} {
		r = append(r, [2]int{k, 1}, [2]int{1, k})
	}
	return r
}()

// Ratios of 10 or more that companies actually use (reverse splits are
// round numbers), matched within BigSplitTolerance: a gap near none of them
// is not taken for a split, and becomes a break.
var bigRatios = []int{10, 12, 15, 20, 25, 30, 40, 50, 60, 75, 80, 100, 120, 125, 150, 160, 200, 250, 300, 400, 500, 1000}

// detectSplit says whether an overnight price ratio (gap = open / previous
// close) is a split, and which.
func detectSplit(gap, dayNotional float64, prior float64, havePrior bool) (int, int, bool) {
	if gap >= 10 || gap <= 0.1 {
		g := math.Max(gap, 1/gap)
		best, bestErr := 0, math.Inf(1)
		for _, k := range bigRatios {
			if e := math.Abs(g/float64(k) - 1); e < bestErr {
				best, bestErr = k, e
			}
		}
		if bestErr > BigSplitTolerance {
			return 0, 0, false
		}
		if gap > 1 {
			return 1, best, true // reverse split: 1 new share for `best` old
		}
		return best, 1, true
	}
	if gap > 1-SplitGap && gap < 1+SplitGap {
		return 0, 0, false
	}
	if !havePrior || prior <= 0 || dayNotional > SplitVolumeSpike*prior {
		return 0, 0, false
	}
	best, bestErr := [2]int{}, math.Inf(1)
	for _, r := range commonRatios {
		m := float64(r[1]) / float64(r[0]) // price multiplier: old/new
		if e := math.Abs(gap/m - 1); e < bestErr {
			best, bestErr = r, e
		}
	}
	if bestErr <= SplitTolerance {
		return best[0], best[1], true
	}
	return 0, 0, false
}

// maybeThreeForTwo says whether a gap could be a 3-for-2 split, which is not
// adjusted: at normal dollar volume a -33% gap can as easily be an earnings
// drop as a split, and leaving a real split unadjusted puts a
// false -33% step into the history. So the history is cut there instead.
// With too little trading since the last break to judge the volume, a gap
// of that shape is cut too: the cautious reading.
func maybeThreeForTwo(gap, dayNotional, prior float64, havePrior bool) bool {
	if gap > 1-SplitGap || math.Abs(gap/(2.0/3.0)-1) > SplitTolerance {
		return false
	}
	return !havePrior || prior <= 0 || dayNotional <= ThreeForTwoVolume*prior
}

// adjust divides every price before a split by its ratio (and multiplies the
// volume), so the series reads in today's shares.
func adjust(ss []session, splits []Split) []session {
	out := make([]session, len(ss))
	copy(out, ss)
	for _, sp := range splits {
		m := float64(sp.Old) / float64(sp.New)
		for i := range out {
			if out[i].date >= sp.Date {
				break
			}
			out[i].o *= m
			out[i].h *= m
			out[i].l *= m
			out[i].c *= m
			out[i].v /= m
			// pv (dollars) does not change with a split.
		}
	}
	return out
}

func round4(x float64) float64 { return math.Round(x*10000) / 10000 }

func dateNum(d string) float64 {
	var y, m, dd int
	fmt.Sscanf(d, "%d-%d-%d", &y, &m, &dd)
	return float64(y*10000 + m*100 + dd)
}
