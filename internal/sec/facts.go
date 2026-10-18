package sec

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"regexp"
	"sort"
	"strconv"
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
	if err := json.NewDecoder(r).Decode(&cf); err != nil {
		return nil, err
	}
	cik, err := parseCIK(cf.CIK)
	if err != nil {
		return nil, err
	}
	rev := &metric{revenue: true, codes: RevenueCodes, byPeriod: map[span][]rec{}}
	ni := &metric{codes: []string{"ni"}, byPeriod: map[span][]rec{}}
	for code, concept := range Concepts {
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
		m := rev
		if code == "ni" {
			m = ni
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
			m.byPeriod[sp] = append(m.byPeriod[sp], rec{code, s, e, k, f.Val, f.Accn, f.Form, f.Filed})
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
		for sp, recs := range m.byPeriod {
			if k := recs[0].kind; k != kNine {
				put(sp, k, m.value(m.pick(m.original(sp))), m.revenue)
			}
		}
		for sp, v := range m.fourthQuarters() {
			put(sp, kQuarter, v, m.revenue)
		}
	}
	checkYears(quarters, years)
	newest := time.Time{}
	for _, rows := range []map[span]*Period{quarters, years} {
		for sp, p := range rows {
			if (p.Revenue.Val != nil || p.NetIncome.Val != nil) && sp.end.After(newest) {
				newest = sp.end
			}
		}
	}
	if newest.IsZero() || now.Sub(newest).Hours()/24 > staleDays {
		return nil, nil
	}
	f := &File{V: 1, Source: "SEC", Symbol: symbol, CIK: cik, GeneratedAt: now.UTC().Format(time.RFC3339)}
	f.Quarters = keep(quarters, newest.AddDate(0, 0, -quarterWindow), MaxQuarters)
	f.Years = keep(years, newest.AddDate(0, 0, -yearWindow), MaxYears)
	return f, nil
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

// order sorts unambiguous candidates largest first (total revenue is the
// largest top-line figure; some filers tag one line, not the total, as
// Revenues), equal values by preference.
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
	if m.revenue && c.r.val < 0 {
		// A few filers report a negative revenue (returns, or a fund's
		// losses under Revenues): not a revenue figure to show.
		return nil, "negative"
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
// nine months that start with it, both as first reported. A quarter filed by
// itself is kept only if it agrees with that; a filed revenue equal to the
// whole year (some filers tag the year's total with the quarter's dates) is
// replaced by the computed one, or not shown when there is none.
func (m *metric) fourthQuarters() map[span]Value {
	out := map[span]Value{}
	for y, yrecs := range m.byPeriod {
		if yrecs[0].kind != kYear {
			continue
		}
		yc := m.original(y)
		yPick, _ := m.pick(yc)
		checked := map[span]bool{}
		for n, nrecs := range m.byPeriod {
			if nrecs[0].kind != kNine || !n.start.Equal(y.start) {
				continue
			}
			q := span{n.end.AddDate(0, 0, 1), y.end}
			if kindOf(days(q.start, q.end)) != kQuarter {
				continue
			}
			checked[q] = true
			derived := m.derive(yc, m.original(n))
			if _, direct := m.byPeriod[q]; !direct {
				out[q] = derived
				continue
			}
			dv := m.value(m.pick(m.original(q)))
			if dv.Val == nil || yPick == nil {
				continue
			}
			tol := 0.005 * math.Abs(yPick.r.val)
			switch {
			case derived.Val != nil && math.Abs(*dv.Val-*derived.Val) <= tol:
				// Agrees: the filed quarter stays.
			case derived.Val != nil && m.revenue && math.Abs(*dv.Val-yPick.r.val) <= 0.001*math.Abs(yPick.r.val):
				// The year's total under the quarter's dates: the computed quarter is the one.
				out[q] = derived
			default:
				out[q] = Value{Why: "check"}
			}
		}
		if !m.revenue || yPick == nil {
			continue
		}
		for q, qrecs := range m.byPeriod {
			if qrecs[0].kind != kQuarter || !q.end.Equal(y.end) || checked[q] {
				continue
			}
			if dv := m.value(m.pick(m.original(q))); dv.Val != nil && math.Abs(*dv.Val-yPick.r.val) <= 0.001*math.Abs(yPick.r.val) {
				out[q] = Value{Why: "check"}
			}
		}
	}
	return out
}

// derive is year minus nine months under the first concept, largest year
// value first, that both report.
func (m *metric) derive(yc, nc []cand) Value {
	for _, c := range append(append([]cand{}, yc...), nc...) {
		if c.ambiguous {
			return Value{Why: "ambiguous"}
		}
	}
	for _, y := range order(yc) {
		for _, n := range nc {
			if n.code != y.code {
				continue
			}
			// The nine months as filed before the year (in a 10-Q of that
			// year), not a comparative in a later 10-Q on a newer basis.
			if n.r.filed > y.r.filed {
				return Value{Why: "none"}
			}
			val := y.r.val - n.r.val
			if m.revenue && (val < 0 || val > y.r.val*1.001) {
				return Value{Why: "check"}
			}
			v := Value{Val: &val, C: y.code, Form: y.r.form, Filed: y.r.filed, Accn: y.r.accn,
				Derived: &Derived{Year: Source{y.r.accn, y.r.filed}, Nine: Source{n.r.accn, n.r.filed}}}
			yp, ok1 := m.prev(y.r)
			np, ok2 := m.prev(n.r)
			if ok1 && ok2 && (!m.revenue || yp-np >= 0) {
				p := yp - np
				v.Prev = &p
			}
			return v
		}
	}
	return Value{Why: "none"}
}

// checkYears drops a year's revenue that is smaller than one of its own
// quarters (some filers tag a single line, not the total, as Revenues in
// their 10-K), and the fourth quarter computed from it. Which of the two is
// wrong cannot be told, so neither is shown; the other quarters stand on
// their own filings.
func checkYears(quarters, years map[span]*Period) {
	for ys, y := range years {
		if y.Revenue.Val == nil {
			continue
		}
		bad := false
		for qs, q := range quarters {
			if !qs.start.Before(ys.start) && !qs.end.After(ys.end) && q.Revenue.Val != nil && *q.Revenue.Val > *y.Revenue.Val*1.001 {
				bad = true
			}
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
