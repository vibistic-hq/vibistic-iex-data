// Package earnings estimates each company's next earnings release date from
// its SEC filings (issue #12), for the date Vibistic Stocks shows under a
// symbol when the visitor has no calendar of their own. Dates only, never any
// text from a filing.
//
// A company furnishes its earnings release to SEC on Form 8-K under Item 2.02
// ("Results of Operations and Financial Condition"), nearly always the day it
// is published. The estimate is the matching filing a year earlier plus 52
// weeks (the same weekday), for regular quarterly filers only, and nothing when
// the history leaves it unclear. What is estimated is that 8-K's filing date.
//
// Backtested on SEC's whole submissions.zip with no look-ahead (each day's
// estimate from filings before that day only, against the first Item 2.02
// filing on or after it): for the year to 2025-09-30, which the rule was not
// tuned on, the estimate held 14 days before the filing was within 7 days of it
// 91.2% of the time and within 14 days 96.7% (median 1 day).
package earnings

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"
)

// The rule. Changing any of these needs the backtest run again (issue #12).
const (
	// A filing at most this many days after the first one of a cluster
	// replaces it: a pre-announcement, a correction or a second report, then
	// the results. Bounded by the cluster's first filing, so a chain of
	// filings never spans months.
	MergeDays = 30
	// Quarterly: the last RegularCount releases are MinGap to MaxGap days apart.
	RegularCount = 5
	MinGap       = 56
	MaxGap       = 140
	// The last release is at most this old.
	MaxAge = 135
	// An anniversary falling this many days or fewer after the last release
	// is that quarter, already reported; more than this but under MinGap is
	// unclear (the last filing may have been a pre-announcement or another
	// Item 2.02 report, such as Tesla's quarterly deliveries): no estimate.
	SameQuarter = 10
	// 52 weeks.
	YearDays = 364
	// History used: filings on or after this many days before the build day.
	HistoryDays = 800
)

// File is v1/earnings.json.
type File struct {
	V           int              `json:"v"`
	Source      string           `json:"source"`      // "SEC"
	GeneratedAt string           `json:"generatedAt"` // RFC 3339
	Today       string           `json:"today"`       // the New York day the estimates are for
	AsOf        string           `json:"asOf"`        // the newest filing date in SEC's data
	Symbols     map[string]Entry `json:"symbols"`
	// Covered symbols without an estimate, by reason (one of Whys; issue #14).
	// Optional: files from before it have none.
	NoEstimate map[string][]string `json:"noEstimate,omitempty"`
	// For the share counts (issue #18), never published: every company whose
	// whole filing index was read (Filers), and those among them that ever
	// registered depositary shares on Form F-6 (Depositary), whose US listing
	// is an ADS, not the shares their 10-Q counts. Build strips both.
	Filers     []int64 `json:"filers,omitempty"`
	Depositary []int64 `json:"depositary,omitempty"`
}

// Entry is one symbol's estimate.
type Entry struct {
	CIK   int64  `json:"cik"`
	Next  string `json:"next"`  // estimated filing date of the next earnings release
	Last  string `json:"last"`  // the last one's filing date
	Basis string `json:"basis"` // the filing a year earlier that Next is 52 weeks after
}

const day = 24 * time.Hour

const layout = "2006-01-02"

// Dates of form 8-K filings listing Item 2.02, from the columns of a
// submissions file. Amendments (8-K/A) are left out. Unaligned columns or a bad
// date are an error.
func Releases(forms, dates, items []string) ([]time.Time, error) {
	if len(forms) != len(dates) || len(forms) != len(items) {
		return nil, fmt.Errorf("columns of %d, %d and %d entries", len(forms), len(dates), len(items))
	}
	var out []time.Time
	for i, f := range forms {
		if f != "8-K" || !hasItem(items[i], "2.02") {
			continue
		}
		d, err := ParseDate(dates[i])
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, nil
}

func hasItem(list, item string) bool {
	for _, it := range strings.Split(list, ",") {
		if strings.TrimSpace(it) == item {
			return true
		}
	}
	return false
}

// Cluster sorts and deduplicates the dates and keeps the last of each cluster
// (filings within MergeDays of the cluster's first).
func Cluster(dates []time.Time) []time.Time {
	ds := append([]time.Time(nil), dates...)
	sort.Slice(ds, func(i, j int) bool { return ds[i].Before(ds[j]) })
	var kept []time.Time
	var first time.Time
	for _, d := range ds {
		if len(kept) > 0 && d.Sub(first) <= MergeDays*day {
			kept[len(kept)-1] = d
			continue
		}
		kept = append(kept, d)
		first = d
	}
	return kept
}

// Why a symbol has no estimate (v1/earnings.json noEstimate), fixed codes the app words.
const (
	WhyNever     = "never"     // no earnings release (8-K Item 2.02) on file
	WhyFew       = "few"       // fewer than RegularCount releases: a recent listing, most often
	WhyIrregular = "irregular" // the last releases are not about a quarter apart
	WhyStale     = "stale"     // the last release is over MaxAge days old
	WhyUnclear   = "unclear"   // another Item 2.02 filing came shortly before the expected date
	WhyNoMatch   = "nomatch"   // last year's matching release does not give a date ahead
)

// Whys lists every code.
var Whys = []string{WhyNever, WhyFew, WhyIrregular, WhyStale, WhyUnclear, WhyNoMatch}

// Estimate gives the next release's estimated date for a build on today (a
// New York day at midnight UTC), from raw Item 2.02 filing dates. Filings on
// or after today are ignored, so the estimate never depends on what came later.
func Estimate(raw []time.Time, today time.Time) (next, last, basis time.Time, ok bool) {
	next, last, basis, why := Explain(raw, today)
	return next, last, basis, why == ""
}

// Explain is Estimate with the reason when there is none (one of Whys).
func Explain(raw []time.Time, today time.Time) (next, last, basis time.Time, why string) {
	var before []time.Time
	for _, d := range raw {
		if d.Before(today) {
			before = append(before, d)
		}
	}
	past := Cluster(before)
	if len(past) == 0 {
		return next, last, basis, WhyNever
	}
	if len(past) < RegularCount {
		return next, last, basis, WhyFew
	}
	tail := past[len(past)-RegularCount:]
	for i := 1; i < len(tail); i++ {
		if g := gapDays(tail[i-1], tail[i]); g < MinGap || g > MaxGap {
			return next, last, basis, WhyIrregular
		}
	}
	last = past[len(past)-1]
	if gapDays(last, today) > MaxAge {
		return next, time.Time{}, basis, WhyStale
	}
	for _, d := range past {
		e := d.Add(YearDays * day)
		if e.Before(today) {
			continue
		}
		g := gapDays(last, e)
		if g < MinGap {
			if g > SameQuarter {
				return next, time.Time{}, basis, WhyUnclear
			}
			continue
		}
		if g > MaxGap {
			break
		}
		return e, last, d, ""
	}
	return next, time.Time{}, basis, WhyNoMatch
}

func gapDays(a, b time.Time) int { return int(b.Sub(a) / day) }

// ParseDate reads yyyy-mm-dd as midnight UTC.
func ParseDate(s string) (time.Time, error) {
	t, err := time.Parse(layout, s)
	if err != nil || t.Format(layout) != s {
		return time.Time{}, fmt.Errorf("bad date %q", s)
	}
	return t, nil
}

// NewYorkDay is the New York calendar day of t, as midnight UTC.
func NewYorkDay(t time.Time) time.Time {
	ny, err := time.LoadLocation("America/New_York")
	if err != nil {
		panic(err) // time/tzdata is embedded by the commands
	}
	y, m, d := t.In(ny).Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

var symbolRe = regexp.MustCompile(`^[A-Z]{1,5}(\.[A-Z])?$`)

// MinSymbols: a snapshot with fewer estimates means something upstream
// changed (about 2,600 on 2026-10-20).
const MinSymbols = 1500

// MaxSymbols bounds the file (the app refuses more).
const MaxSymbols = 12000

// MaxSourceAge: the newest filing read is at most this many days before the
// build day (SEC's index stopped updating otherwise).
const MaxSourceAge = 5

// Validate checks a file the way the app does: any bad field rejects it.
func Validate(f *File) error {
	if f.V != 1 || f.Source != "SEC" {
		return fmt.Errorf("v/source")
	}
	gen, err := time.Parse(time.RFC3339, f.GeneratedAt)
	if err != nil {
		return fmt.Errorf("generatedAt: %w", err)
	}
	today, err := ParseDate(f.Today)
	if err != nil {
		return fmt.Errorf("today: %w", err)
	}
	// The estimates are for the New York day they were made on, from SEC
	// filings at most MaxSourceAge days older.
	if !today.Equal(NewYorkDay(gen)) {
		return fmt.Errorf("today %s is not the New York day of %s", f.Today, f.GeneratedAt)
	}
	asOf, err := ParseDate(f.AsOf)
	if err != nil {
		return fmt.Errorf("asOf: %w", err)
	}
	if !asOf.Before(today) || gapDays(asOf, today) > MaxSourceAge {
		return fmt.Errorf("asOf %s for today %s", f.AsOf, f.Today)
	}
	if len(f.Symbols) > MaxSymbols {
		return fmt.Errorf("%d symbols", len(f.Symbols))
	}
	for s, e := range f.Symbols {
		if !symbolRe.MatchString(s) {
			return fmt.Errorf("symbol %q", s)
		}
		if err := validateEntry(e, today); err != nil {
			return fmt.Errorf("%s: %w", s, err)
		}
	}
	// Each symbol once, in one place: with an estimate or under one reason.
	seen := map[string]bool{}
	n := len(f.Symbols)
	for why, syms := range f.NoEstimate {
		// null decodes to a nil slice; the app wants a list.
		if !knownWhy(why) || syms == nil {
			return fmt.Errorf("noEstimate: reason %q", why)
		}
		for _, s := range syms {
			if !symbolRe.MatchString(s) || seen[s] {
				return fmt.Errorf("noEstimate %s: symbol %q", why, s)
			}
			if _, ok := f.Symbols[s]; ok {
				return fmt.Errorf("noEstimate %s: %s also has an estimate", why, s)
			}
			seen[s] = true
			n++
		}
	}
	if n > MaxSymbols {
		return fmt.Errorf("%d symbols", n)
	}
	filers := map[int64]bool{}
	for _, c := range f.Filers {
		if c < 1 || c > 9999999999 || filers[c] {
			return fmt.Errorf("filers: cik %d", c)
		}
		filers[c] = true
	}
	seenDep := map[int64]bool{}
	for _, c := range f.Depositary {
		if !filers[c] || seenDep[c] {
			return fmt.Errorf("depositary: cik %d", c)
		}
		seenDep[c] = true
	}
	return nil
}

func knownWhy(w string) bool {
	for _, k := range Whys {
		if k == w {
			return true
		}
	}
	return false
}

func validateEntry(e Entry, today time.Time) error {
	if e.CIK < 1 || e.CIK > 9999999999 {
		return fmt.Errorf("cik %d", e.CIK)
	}
	next, err := ParseDate(e.Next)
	if err != nil {
		return err
	}
	last, err := ParseDate(e.Last)
	if err != nil {
		return err
	}
	basis, err := ParseDate(e.Basis)
	if err != nil {
		return err
	}
	if !next.Equal(basis.Add(YearDays * day)) {
		return fmt.Errorf("next %s is not 52 weeks after %s", e.Next, e.Basis)
	}
	if !last.Before(today) || next.Before(today) || !basis.Before(last) {
		return fmt.Errorf("dates out of order: basis %s, last %s, today %s, next %s", e.Basis, e.Last, today.Format(layout), e.Next)
	}
	if g := gapDays(last, next); g < MinGap || g > MaxGap {
		return fmt.Errorf("next is %d days after last", g)
	}
	return nil
}
