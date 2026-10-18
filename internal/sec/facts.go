package sec

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Rows are kept for periods ending within this many days of the company's
// newest period (quarter or year): a series last filed years ago must not
// come back as recent rows. A company whose newest period ended more than
// staleDays before the snapshot gets no file (it stopped filing).
const (
	quarterWindow = 1280
	yearWindow    = 1830
	staleDays     = 400
)

type kind int

const (
	kQuarter kind = iota + 1
	kNine
	kYear
)

func kindOf(n int) kind {
	switch {
	case n >= quarterMin && n <= quarterMax:
		return kQuarter
	case n >= nineMin && n <= nineMax:
		return kNine
	case n >= yearMin && n <= yearMax:
		return kYear
	}
	return 0
}

// fact is one reported value, as companyfacts lists it.
type fact struct {
	Start string  `json:"start"`
	End   string  `json:"end"`
	Val   float64 `json:"val"`
	Accn  string  `json:"accn"`
	Form  string  `json:"form"`
	Filed string  `json:"filed"`
}

type rec struct {
	code       string
	start, end time.Time
	kind       kind
	val        float64
	accn       string
	form       string
	filed      string
}

type span struct{ start, end time.Time }

// metric is one figure's facts by period.
type metric struct {
	revenue  bool
	codes    []string // in order of preference among equal values
	byPeriod map[span][]rec
	parts    map[span][]rec // revenue only: parts of revenue (partConcepts)
}

// cand is one concept's value in a period's original filing(s).
type cand struct {
	code      string
	r         *rec
	ambiguous bool
}

// Extract reads one company's companyfacts JSON. It returns nil (no file)
// when the company has no usable period or stopped filing.
func Extract(r io.Reader, symbol string, now time.Time) (*File, error) {
	var cf struct {
		CIK   json.RawMessage `json:"cik"` // a number, or for some companies a string of digits
		Facts struct {
			USGAAP map[string]json.RawMessage `json:"us-gaap"`
		} `json:"facts"`
	}
	dec := json.NewDecoder(r)
	if err := dec.Decode(&cf); err != nil {
		return nil, err
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, fmt.Errorf("data after the document")
	}
	cik, err := parseCIK(cf.CIK)
	if err != nil {
		return nil, err
	}
	rev := &metric{revenue: true, codes: RevenueCodes, byPeriod: map[span][]rec{}, parts: map[span][]rec{}}
	ni := &metric{codes: []string{"ni"}, byPeriod: map[span][]rec{}}
	read := map[string]string{}
	for code, concept := range Concepts {
		read[code] = concept
	}
	for _, concept := range partConcepts {
		read["part:"+concept] = concept
	}
	for code, concept := range read {
		raw, ok := cf.Facts.USGAAP[concept]
		if !ok {
			continue
		}
		var c struct {
			Units map[string][]fact `json:"units"`
		}
		if err := json.Unmarshal(raw, &c); err != nil {
			return nil, fmt.Errorf("%s: %w", concept, err)
		}
		m, into := rev, rev.byPeriod
		switch {
		case code == "ni":
			m, into = ni, ni.byPeriod
		case strings.HasPrefix(code, "part:"):
			into = rev.parts
		}
		for _, f := range c.Units["USD"] {
			if !forms[f.Form] || !accnRe.MatchString(f.Accn) || !finite(f.Val) {
				continue
			}
			filed, err0 := parseDate(f.Filed)
			s, err1 := parseDate(f.Start)
			e, err2 := parseDate(f.End)
			// A period must be over by the time it is filed.
			if err0 != nil || err1 != nil || err2 != nil || e.Before(s) || e.After(filed) {
				continue
			}
			k := kindOf(days(s, e))
			// A year only from an annual report (a 10-Q's trailing twelve
			// months is not a fiscal year), nine months only from a 10-Q.
			if k == 0 || (k == kYear && f.Form == "10-Q") || (k == kNine && f.Form != "10-Q") {
				continue
			}
			sp := span{s, e}
			into[sp] = append(into[sp], rec{code, s, e, k, f.Val, f.Accn, f.Form, f.Filed})
			_ = m
		}
	}
	quarters := map[span]*Period{}
	years := map[span]*Period{}
	put := func(sp span, k kind, v Value, revenue bool) {
		rows := quarters
		if k == kYear {
			rows = years
		}
		p := rows[sp]
		if p == nil {
			p = &Period{Start: sp.start.Format("2006-01-02"), End: sp.end.Format("2006-01-02"), Revenue: Value{Why: "none"}, NetIncome: Value{Why: "none"}}
			rows[sp] = p
		}
		if revenue {
			p.Revenue = v
		} else {
			p.NetIncome = v
		}
	}
	for _, m := range []*metric{rev, ni} {
		fourth, agreed, badYears := m.fourthQuarters()
		for sp, recs := range m.byPeriod {
			k := recs[0].kind
			if k == kNine {
				continue
			}
			v := m.value(m.pick(m.original(sp)))
			// A quarter from an annual report only when it agrees with the
			// year minus nine months; a year found inconsistent is not shown.
			if (k == kQuarter && v.Val != nil && v.Form != "10-Q" && !agreed[sp]) || (k == kYear && badYears[sp]) {
				v = Value{Why: "check"}
			}
			put(sp, k, v, m.revenue)
		}
		for sp, v := range fourth {
			put(sp, kQuarter, v, m.revenue)
		}
	}
	newest := newestEnd(quarters, years)
	qSince, ySince := newest.AddDate(0, 0, -quarterWindow), newest.AddDate(0, 0, -yearWindow)
	slip := checkLater(rev, quarters, qSince)
	slip = checkLater(rev, years, ySince) || slip
	checkYears(quarters, years)
	checkDerivedPrev(quarters)
	// A scaling slip in the periods shown means the company's revenue tags
	// cannot be trusted: none of its revenue is shown.
	if slip {
		for _, rows := range []map[span]*Period{quarters, years} {
			for _, p := range rows {
				p.Revenue = Value{Why: "check"}
			}
		}
	}
	newest = newestEnd(quarters, years)
	if newest.IsZero() || now.Sub(newest).Hours()/24 > staleDays {
		return nil, nil
	}
	f := &File{V: 1, Source: "SEC", Symbol: symbol, CIK: cik, GeneratedAt: now.UTC().Format(time.RFC3339)}
	f.Quarters = keep(quarters, newest.AddDate(0, 0, -quarterWindow), MaxQuarters)
	f.Years = keep(years, newest.AddDate(0, 0, -yearWindow), MaxYears)
	return f, nil
}

// newestEnd is the end of the company's newest period with a value.
func newestEnd(quarters, years map[span]*Period) time.Time {
	newest := time.Time{}
	for _, rows := range []map[span]*Period{quarters, years} {
		for sp, p := range rows {
			if (p.Revenue.Val != nil || p.NetIncome.Val != nil) && sp.end.After(newest) {
				newest = sp.end
			}
		}
	}
	return newest
}

func parseCIK(raw json.RawMessage) (int64, error) {
	var n int64
	if err := json.Unmarshal(raw, &n); err != nil {
		var s string
		if json.Unmarshal(raw, &s) != nil || !cikRe.MatchString(s) {
			return 0, fmt.Errorf("cik %s", raw)
		}
		n, _ = strconv.ParseInt(s, 10, 64)
	}
	if n < 1 || n > 9999999999 {
		return 0, fmt.Errorf("cik %s", raw)
	}
	return n, nil
}

var cikRe = regexp.MustCompile(`^[0-9]{1,10}$`)

// original lists, for a period, each concept's value in the earliest
// filing(s) that report the period under any of the metric's concepts: the
// period as first reported. Two different values of one concept on that day
// make that concept ambiguous.
func (m *metric) original(sp span) []cand {
	first := ""
	for _, r := range m.byPeriod[sp] {
		if first == "" || r.filed < first {
			first = r.filed
		}
	}
	var out []cand
	for _, code := range m.codes {
		var c *cand
		for i := range m.byPeriod[sp] {
			x := &m.byPeriod[sp][i]
			if x.filed != first || x.code != code {
				continue
			}
			switch {
			case c == nil:
				c = &cand{code: code, r: x}
			case c.r.val != x.val:
				c.ambiguous = true
			case x.accn < c.r.accn:
				// The same value in several filings that day: the lowest accession number.
				c.r = x
			}
		}
		if c != nil {
			out = append(out, *c)
		}
	}
	return out
}

// order sorts unambiguous candidates largest first, equal values by
// preference.
func order(cs []cand) []cand {
	out := append([]cand{}, cs...)
	sort.SliceStable(out, func(i, j int) bool { return out[i].r.val > out[j].r.val })
	return out
}

// pick is the period's figure from its candidates: any ambiguity makes it
// ambiguous, as one of the ambiguous values might be the total.
func (m *metric) pick(cs []cand) (*cand, string) {
	if len(cs) == 0 {
		return nil, "none"
	}
	for _, c := range cs {
		if c.ambiguous {
			return nil, "ambiguous"
		}
	}
	c := order(cs)[0]
	if !m.revenue {
		return &c, ""
	}
	if c.r.val < 0 {
		// A few filers report a negative revenue (returns, or a fund's
		// losses under Revenues): not a revenue figure to show.
		return nil, "negative"
	}
	// Every revenue concept the filing reports for the period must agree
	// (within 1%): when two differ, which one is the total on the income
	// statement cannot be told from these facts (Berkshire's and Chevron's
	// larger figure is the total, Republic Services' counts intercompany
	// sales), so none is shown (owner's decision, 2026-10-18).
	for _, x := range cs {
		if x.r.val > 0 && math.Abs(x.r.val-c.r.val) > 0.01*c.r.val {
			return nil, "conflict"
		}
	}
	// Nor can a total be smaller than a part of it in the same filing.
	for _, p := range m.parts[span{c.r.start, c.r.end}] {
		if p.filed == c.r.filed && p.val > c.r.val*1.01 {
			return nil, "check"
		}
	}
	return &c, ""
}

func (m *metric) value(c *cand, why string) Value {
	if c == nil {
		return Value{Why: why}
	}
	v := c.r.val
	out := Value{Val: &v, C: c.code, Form: c.r.form, Filed: c.r.filed, Accn: c.r.accn}
	if p, ok := m.prev(c.r); ok && (!m.revenue || p >= 0) {
		out.Prev = &p
	}
	return out
}

// prev is the year-earlier period's value in the same filing under the same
// concept: one period of the same kind ending 351 to 379 days earlier.
func (m *metric) prev(r *rec) (float64, bool) {
	var vals []float64
	for sp, recs := range m.byPeriod {
		gap := int(r.end.Sub(sp.end).Hours() / 24)
		if gap < 351 || gap > 379 {
			continue
		}
		for _, x := range recs {
			if x.accn == r.accn && x.code == r.code && x.kind == r.kind {
				vals = append(vals, x.val)
			}
		}
	}
	if len(vals) == 0 {
		return 0, false
	}
	for _, v := range vals[1:] {
		if v != vals[0] {
			return 0, false
		}
	}
	return vals[0], true
}

// fourthQuarters gives each fiscal year's last quarter: the year minus the
// nine months that start with it, both as first reported. It also returns
// the quarters filed in an annual report that agree with that computation
// (any other quarter from a 10-K is not shown: some filers tag the year's
// total with the quarter's dates), and the years found inconsistent.
func (m *metric) fourthQuarters() (out map[span]Value, agreed, badYears map[span]bool) {
	out, agreed, badYears = map[span]Value{}, map[span]bool{}, map[span]bool{}
	for y, yrecs := range m.byPeriod {
		if yrecs[0].kind != kYear {
			continue
		}
		yc := m.original(y)
		yPick, _ := m.pick(yc)
		for n, nrecs := range m.byPeriod {
			if nrecs[0].kind != kNine || !n.start.Equal(y.start) {
				continue
			}
			q := span{n.end.AddDate(0, 0, 1), y.end}
			if kindOf(days(q.start, q.end)) != kQuarter {
				continue
			}
			derived, yearBad := m.derive(yc, m.original(n), n)
			if yearBad {
				badYears[y] = true
			}
			if _, direct := m.byPeriod[q]; !direct {
				out[q] = derived
				continue
			}
			dv := m.value(m.pick(m.original(q)))
			if dv.Val == nil || yPick == nil || derived.Val == nil {
				continue
			}
			switch {
			case math.Abs(*dv.Val-*derived.Val) <= 0.005*math.Abs(yPick.r.val):
				agreed[q] = true
			case m.revenue && math.Abs(*dv.Val-yPick.r.val) <= 0.001*math.Abs(yPick.r.val):
				// The year's total under the quarter's dates: the computed quarter is the one.
				out[q] = derived
			}
		}
	}
	return out, agreed, badYears
}

// derive is year minus nine months, under the nine months' own figure's
// concept, which must also be the year's (or one equal to it): two concepts
// can differ in scale or scope. It is checked against the first three
// quarters when all three were filed (their sum must be the nine months), and
// for revenue against a year more than ten times the nine months (a scaling
// slip in the year's tag), which also marks the year bad.
func (m *metric) derive(yc, nc []cand, n span) (Value, bool) {
	for _, c := range append(append([]cand{}, yc...), nc...) {
		if c.ambiguous {
			return Value{Why: "ambiguous"}, false
		}
	}
	top, why := m.pick(yc)
	if top == nil {
		return Value{Why: why}, false
	}
	nine, why := m.pick(nc)
	if nine == nil {
		return Value{Why: why}, false
	}
	var year *cand
	for i := range yc {
		if yc[i].code == nine.code && math.Abs(yc[i].r.val-top.r.val) <= 1e-4*math.Abs(top.r.val) {
			year = &yc[i]
		}
	}
	if year == nil {
		return Value{Why: "none"}, false
	}
	// Nine months the company later restated (shown again a year on, in the
	// next third-quarter 10-Q, more than 1% off): the year's 10-K may already
	// be on the new basis (PPG's divested business), so the difference would
	// not be a quarter.
	for _, x := range m.byPeriod[n] {
		if x.code == nine.code && x.filed > nine.r.filed && math.Abs(x.val-nine.r.val) > 0.01*math.Abs(nine.r.val) {
			return Value{Why: "check"}, false
		}
	}
	// The nine months as filed before the year (in a 10-Q of that year), not
	// a comparative in a later 10-Q on a newer basis.
	if nine.r.filed > year.r.filed {
		return Value{Why: "none"}, false
	}
	if m.revenue && nine.r.val > 0 && year.r.val > 10*nine.r.val {
		return Value{Why: "check"}, true
	}
	if sum, ok := m.firstThree(n); ok && math.Abs(nine.r.val-sum) > 0.005*math.Max(math.Abs(nine.r.val), math.Abs(sum)) {
		return Value{Why: "check"}, false
	}
	val := year.r.val - nine.r.val
	if m.revenue && (val < 0 || val > year.r.val*1.001) {
		return Value{Why: "check"}, false
	}
	v := Value{Val: &val, C: year.code, Form: year.r.form, Filed: year.r.filed, Accn: year.r.accn,
		Derived: &Derived{Year: Source{year.r.accn, year.r.filed}, Nine: Source{nine.r.accn, nine.r.filed}}}
	yp, ok1 := m.prev(year.r)
	np, ok2 := m.prev(nine.r)
	if ok1 && ok2 && (!m.revenue || yp-np >= 0) {
		p := yp - np
		v.Prev = &p
	}
	return v, false
}

// firstThree is the sum of the three quarters that tile the nine months, each
// as first reported; false unless all three are there with a value.
func (m *metric) firstThree(n span) (float64, bool) {
	sum, at := 0.0, n.start
	for range 3 {
		found := false
		for q, recs := range m.byPeriod {
			if recs[0].kind != kQuarter || !q.start.Equal(at) || q.end.After(n.end) {
				continue
			}
			c, _ := m.pick(m.original(q))
			if c == nil {
				return 0, false
			}
			sum += c.r.val
			at = q.end.AddDate(0, 0, 1)
			found = true
			break
		}
		if !found {
			return 0, false
		}
	}
	return sum, at.AddDate(0, 0, -1).Equal(n.end)
}

// checkYears drops a year's revenue that is smaller than one of its own
// quarters (some filers tag a single line, not the total, as Revenues in
// their 10-K), or over ten times three or more of them (then those quarters
// are not shown either), and the fourth quarter computed from it. Which of the two is
// wrong cannot be told, so neither is shown; the other quarters stand on
// their own filings.
func checkYears(quarters, years map[span]*Period) {
	for ys, y := range years {
		if y.Revenue.Val == nil {
			continue
		}
		bad := false
		sum, n := 0.0, 0
		for qs, q := range quarters {
			if !qs.start.Before(ys.start) && !qs.end.After(ys.end) && q.Revenue.Val != nil {
				if *q.Revenue.Val > *y.Revenue.Val*1.001 {
					bad = true
				}
				sum += *q.Revenue.Val
				n++
			}
		}
		// Three or more of its quarters known: a year over ten times their sum
		// does not fit them, nor they it; none of them is shown.
		if n >= 3 && sum > 0 && *y.Revenue.Val > 10*sum {
			for qs, q := range quarters {
				if !qs.start.Before(ys.start) && !qs.end.After(ys.end) {
					q.Revenue = Value{Why: "check"}
				}
			}
			bad = true
		}
		if !bad {
			continue
		}
		for _, q := range quarters {
			if d := q.Revenue.Derived; d != nil && d.Year.Accn == y.Revenue.Accn {
				q.Revenue = Value{Why: "check"}
			}
		}
		y.Revenue = Value{Why: "check"}
	}
}

// checkLater checks each revenue figure against the same period as the next
// year's filing shows it under the same concept (a figure the company filed
// again): a restatement moves a figure a little, while a
// scaling slip (thousands tagged as dollars) or a partial line tagged as the
// total does not survive the comparison. A figure off by more than 3 times
// is not shown, and
// one off by about a thousand or a million times (thousands tagged as
// dollars) is reported as a slip when the period is one shown (ending on or
// after `since`). The later figures are read before any row changes.
func checkLater(rev *metric, rows map[span]*Period, since time.Time) bool {
	// The later figure: in the original filing of the period a year on,
	// that filing's year-earlier value under this figure's concept (read
	// before any row changes, whether or not the later period is shown).
	later := map[span]float64{}
	for sp, p := range rows {
		if p.Revenue.Val == nil {
			continue
		}
		for sp2, recs := range rev.byPeriod {
			gap := int(sp2.end.Sub(sp.end).Hours() / 24)
			if gap < 351 || gap > 379 || recs[0].kind != kindOf(days(sp.start, sp.end)) {
				continue
			}
			for _, c := range rev.original(sp2) {
				if c.code != p.Revenue.C || c.ambiguous {
					continue
				}
				if l, ok := rev.prev(c.r); ok && l > 0 {
					later[sp] = l
				}
			}
		}
	}
	off := func(a, b float64) float64 { return math.Max(a, b) / math.Min(a, b) }
	slip := false
	for sp, p := range rows {
		v := p.Revenue
		l, ok := later[sp]
		if v.Val == nil || *v.Val <= 0 || !ok || off(*v.Val, l) <= 1.1 {
			continue
		}
		switch {
		case scaled(off(*v.Val, l)) && !sp.end.Before(since):
			// Thousands (or millions) tagged as dollars in a period shown.
			slip = true
		case off(*v.Val, l) > 3:
			// A large restatement or a partial line: this figure is not shown.
			p.Revenue = Value{Why: "check"}
		}
	}
	return slip
}

// scaled tells a ratio of about a thousand or a million: a figure tagged in
// the wrong unit.
func scaled(r float64) bool {
	return (r >= 900 && r <= 1100) || (r >= 9e5 && r <= 1.1e6)
}

// checkDerivedPrev keeps a computed fourth quarter's year-earlier value only
// when it matches that quarter as first reported a year before (the two
// comparatives come from two filings, and one may be on a reclassified basis).
func checkDerivedPrev(quarters map[span]*Period) {
	for sp, p := range quarters {
		for _, get := range []func(*Period) *Value{func(p *Period) *Value { return &p.Revenue }, func(p *Period) *Value { return &p.NetIncome }} {
			v := get(p)
			if v.Derived == nil || v.Prev == nil {
				continue
			}
			ok := false
			for sp2, p2 := range quarters {
				gap := int(sp.end.Sub(sp2.end).Hours() / 24)
				if gap < 351 || gap > 379 {
					continue
				}
				if w := get(p2); w.Val != nil && math.Abs(*w.Val-*v.Prev) <= 0.01*math.Max(math.Abs(*w.Val), math.Abs(*v.Prev)) {
					ok = true
				}
			}
			if !ok {
				v.Prev = nil
			}
		}
	}
}

// keep returns the rows with a value ending on or after `since`, the newest
// max, oldest first, without overlaps (the later-ending of two overlapping
// rows stays).
func keep(rows map[span]*Period, since time.Time, max int) []Period {
	var list []Period
	for sp, p := range rows {
		if (p.Revenue.Val != nil || p.NetIncome.Val != nil) && !sp.end.Before(since) {
			list = append(list, *p)
		}
	}
	sort.Slice(list, func(i, j int) bool {
		if list[i].End != list[j].End {
			return list[i].End > list[j].End
		}
		return list[i].Start > list[j].Start
	})
	out := []Period{} // [] in the file, never null
	for _, p := range list {
		if len(out) > 0 && p.End >= out[len(out)-1].Start {
			continue
		}
		out = append(out, p)
		if len(out) == max {
			break
		}
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out
}
