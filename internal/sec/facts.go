package sec

import (
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strconv"
	"time"
)

// Rows are kept for periods ending within this many days of the company's
// newest period of that kind (a concept last filed years ago must not come
// back as a recent row), and a company whose newest period ended more than
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

func (s span) key() string { return s.start.Format("2006-01-02") + "/" + s.end.Format("2006-01-02") }

// metric is one figure's facts by period.
type metric struct {
	codes    []string // in order of preference
	byPeriod map[span][]rec
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
	dec.UseNumber()
	if err := dec.Decode(&cf); err != nil {
		return nil, err
	}
	cik, err := parseCIK(cf.CIK)
	if err != nil {
		return nil, err
	}
	rev := metric{codes: RevenueCodes, byPeriod: map[span][]rec{}}
	ni := metric{codes: []string{"ni"}, byPeriod: map[span][]rec{}}
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
		m := &rev
		if code == "ni" {
			m = &ni
		}
		for _, f := range c.Units["USD"] {
			if !forms[f.Form] || !accnRe.MatchString(f.Accn) || !finite(f.Val) {
				continue
			}
			if _, err := parseDate(f.Filed); err != nil {
				continue
			}
			s, err1 := parseDate(f.Start)
			e, err2 := parseDate(f.End)
			if err1 != nil || err2 != nil || e.Before(s) {
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
	for _, m := range []*metric{&rev, &ni} {
		isRev := m == &rev
		for sp, recs := range m.byPeriod {
			k := recs[0].kind
			if k == kNine {
				continue
			}
			v, _ := m.original(sp)
			put(sp, k, v, isRev)
		}
		for sp, v := range m.fourthQuarters() {
			put(sp, kQuarter, v, isRev)
		}
	}
	checkYears(quarters, years)
	f := &File{V: 1, Source: "SEC", Symbol: symbol, CIK: cik, GeneratedAt: now.UTC().Format(time.RFC3339)}
	f.Quarters = keep(quarters, quarterWindow, MaxQuarters)
	f.Years = keep(years, yearWindow, MaxYears)
	newest := time.Time{}
	for _, ps := range [][]Period{f.Quarters, f.Years} {
		if len(ps) > 0 {
			if e, _ := parseDate(ps[len(ps)-1].End); e.After(newest) {
				newest = e
			}
		}
	}
	if newest.IsZero() || now.Sub(newest).Hours()/24 > staleDays {
		return nil, nil
	}
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

// original is a period's value as first reported: from the earliest filing
// that has the period under any of the metric's concepts, the first concept
// in order of preference that filing reports. The second result is that
// value's record (nil when null).
func (m *metric) original(sp span) (Value, *rec) {
	recs := m.byPeriod[sp]
	first := ""
	for _, r := range recs {
		if first == "" || r.filed < first {
			first = r.filed
		}
	}
	for _, code := range m.codes {
		v, r, found := m.at(sp, first, code)
		if found {
			return v, r
		}
	}
	return Value{Why: "none"}, nil
}

// at is the period's value under one concept in the filings of one day.
// found is false when there is none; two different values make it ambiguous.
func (m *metric) at(sp span, filed, code string) (v Value, r *rec, found bool) {
	var pick *rec
	for i := range m.byPeriod[sp] {
		x := &m.byPeriod[sp][i]
		if x.filed != filed || x.code != code {
			continue
		}
		if pick != nil && pick.val != x.val {
			return Value{Why: "ambiguous"}, nil, true
		}
		// Several filings on one day with the same value: the lowest accession number.
		if pick == nil || x.accn < pick.accn {
			pick = x
		}
	}
	if pick == nil {
		return Value{}, nil, false
	}
	val := pick.val
	revenue := pick.code != "ni"
	// A few filers report a negative revenue (returns or a fund's losses
	// under Revenues): not a revenue figure to show.
	if revenue && val < 0 {
		return Value{Why: "negative"}, nil, true
	}
	out := Value{Val: &val, C: pick.code, Form: pick.form, Filed: pick.filed, Accn: pick.accn}
	if p, ok := m.prev(pick); ok && (!revenue || p >= 0) {
		out.Prev = &p
	}
	return out, pick, true
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

// fourthQuarters derives each fiscal year's last quarter that no filing
// reports by itself: the year minus the nine months that start with it,
// each as first reported, under the first concept both filings report.
func (m *metric) fourthQuarters() map[span]Value {
	out := map[span]Value{}
	for y, yrecs := range m.byPeriod {
		if yrecs[0].kind != kYear {
			continue
		}
		for n, nrecs := range m.byPeriod {
			if nrecs[0].kind != kNine || !n.start.Equal(y.start) {
				continue
			}
			q := span{n.end.AddDate(0, 0, 1), y.end}
			if kindOf(days(q.start, q.end)) != kQuarter {
				continue
			}
			if _, direct := m.byPeriod[q]; direct {
				continue
			}
			yFirst, nFirst := firstFiled(yrecs), firstFiled(nrecs)
			for _, code := range m.codes {
				yv, yr, yok := m.at(y, yFirst, code)
				nv, nr, nok := m.at(n, nFirst, code)
				if !yok || !nok {
					continue
				}
				if yv.Val == nil || nv.Val == nil {
					out[q] = Value{Why: "ambiguous"}
					break
				}
				val := *yv.Val - *nv.Val
				if code != "ni" && val < 0 {
					out[q] = Value{Why: "check"}
					break
				}
				v := Value{Val: &val, C: code, Form: yr.form, Filed: yr.filed, Accn: yr.accn,
					Derived: &Derived{Year: Source{yr.accn, yr.filed}, Nine: Source{nr.accn, nr.filed}}}
				if yv.Prev != nil && nv.Prev != nil {
					p := *yv.Prev - *nv.Prev
					if code == "ni" || p >= 0 {
						v.Prev = &p
					}
				}
				out[q] = v
				break
			}
		}
	}
	return out
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

func firstFiled(recs []rec) string {
	first := ""
	for _, r := range recs {
		if first == "" || r.filed < first {
			first = r.filed
		}
	}
	return first
}

// keep returns the rows with a value, recent for the company, newest max,
// oldest first, without overlaps (the later-ending of two overlapping rows
// stays).
func keep(rows map[span]*Period, window, max int) []Period {
	var list []Period
	for _, p := range rows {
		if p.Revenue.Val != nil || p.NetIncome.Val != nil {
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
	var newest time.Time
	for _, p := range list {
		e, _ := parseDate(p.End)
		if newest.IsZero() {
			newest = e
		}
		if int(newest.Sub(e).Hours()/24) > window {
			break
		}
		if len(out) > 0 {
			if prevStart, _ := parseDate(out[len(out)-1].Start); !e.Before(prevStart) {
				continue
			}
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
