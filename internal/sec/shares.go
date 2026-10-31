package sec

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"time"
)

// Shares outstanding for Vibistic Stocks' market cap column (issue #18): one
// count per symbol, as the company last reported it, in v1/shares.json.
//
// The count is the cover page's dei:EntityCommonStockSharesOutstanding from
// the newest 10-K, 10-Q or 10-KT that reports it. A company with more than
// one class of common stock reports the cover count per class, which SEC's
// bulk file leaves out (it has no dimensioned facts); for a reviewed company
// whose classes are worth the same per share, the balance sheet's total
// us-gaap:CommonStockSharesOutstanding is used instead. Anything else gets a
// reason code, never a guess.

// SharesFile is shares.json in a secbuild snapshot and, once build has left
// out the depositary-share issuers, v1/shares.json.
type SharesFile struct {
	V           int               `json:"v"`
	Source      string            `json:"source"`      // "SEC"
	GeneratedAt string            `json:"generatedAt"` // the snapshot's
	Symbols     map[string]Shares `json:"symbols"`
}

// Shares is one symbol's count, or null with the reason. The count is as of
// AsOf: a split after that date is not in it (the app applies splits from the
// same source as its prices).
type Shares struct {
	N     *float64 `json:"n"`               // whole shares
	CIK   int64    `json:"cik,omitempty"`   // the company counted
	AsOf  string   `json:"asOf,omitempty"`  // the date the company counted them
	Filed string   `json:"filed,omitempty"` // yyyy-mm-dd
	Accn  string   `json:"accn,omitempty"`
	How   string   `json:"how,omitempty"` // "cover" or "balance" (reviewed multi-class total)
	Why   string   `json:"why,omitempty"` // only when N is null: none, classes, stale, check or depositary
}

// SharesClass is a reviewed company (shares-reviewed.json, by pull request
// only) with more than one class of common stock that are worth the same per
// share (same dividends and claim on earnings, only votes differ), so its
// balance-sheet total times any one class's price is its market cap.
type SharesClass struct {
	CIK  int64  `json:"cik"`
	Name string `json:"name"` // for the reviewer; never published
}

func ValidateSharesClasses(cs []SharesClass) error {
	seen := map[int64]bool{}
	for i, c := range cs {
		if c.CIK < 1 || c.CIK > 9999999999 || seen[c.CIK] || c.Name == "" {
			return fmt.Errorf("entry %d: %+v", i, c)
		}
		seen[c.CIK] = true
	}
	return nil
}

const (
	sharesMaxAge = 200 // days from the count's date to the snapshot
	maxShares    = 1e12
	// The cover count against the newest quarter's basic weighted-average
	// share count: further apart than this is a unit slip or a wrong tag.
	weightedRatio = 2
)

var sharesWhys = map[string]bool{"none": true, "classes": true, "stale": true, "check": true, "depositary": true}

type shareFact struct {
	Start string  `json:"start"`
	End   string  `json:"end"`
	Val   float64 `json:"val"`
	Accn  string  `json:"accn"`
	Form  string  `json:"form"`
	Filed string  `json:"filed"`
}

// ExtractShares reads one company's companyfacts JSON for its share count.
// cik is the company the file must be for; classes is how many of its classes
// are published as symbols; balance says it is in shares-reviewed.json.
func ExtractShares(b []byte, cik int64, classes int, balance bool, now time.Time) (Shares, error) {
	var cf struct {
		CIK   json.RawMessage `json:"cik"`
		Facts struct {
			DEI    map[string]json.RawMessage `json:"dei"`
			USGAAP map[string]json.RawMessage `json:"us-gaap"`
		} `json:"facts"`
	}
	if err := json.NewDecoder(bytes.NewReader(b)).Decode(&cf); err != nil {
		return Shares{}, err
	}
	if got, err := parseCIK(cf.CIK); err != nil || got != cik {
		return Shares{}, fmt.Errorf("cik %.64q in a file for %d", cf.CIK, cik)
	}
	read := func(m map[string]json.RawMessage, concept string) ([]shareFact, error) {
		raw, ok := m[concept]
		if !ok {
			return nil, nil
		}
		var c struct {
			Units map[string][]shareFact `json:"units"`
		}
		if err := json.Unmarshal(raw, &c); err != nil {
			return nil, fmt.Errorf("%s: %w", concept, err)
		}
		return c.Units["shares"], nil
	}
	var facts []shareFact
	how := "cover"
	var err error
	switch {
	case balance:
		how = "balance"
		facts, err = read(cf.Facts.USGAAP, "CommonStockSharesOutstanding")
	case classes > 1:
		// The cover count of a multi-class company is per class: whatever
		// undimensioned count it has is not the whole company.
		return Shares{Why: "classes"}, nil
	default:
		facts, err = read(cf.Facts.DEI, "EntityCommonStockSharesOutstanding")
	}
	if err != nil {
		return Shares{}, err
	}
	weighted, err := read(cf.Facts.USGAAP, "WeightedAverageNumberOfSharesOutstandingBasic")
	if err != nil {
		return Shares{}, err
	}
	best := newestCount(facts, now)
	if best == nil {
		return Shares{Why: "none"}, nil
	}
	if best.ambiguous {
		return Shares{Why: "check"}, nil
	}
	if now.Sub(best.end) > sharesMaxAge*24*time.Hour {
		return Shares{Why: "stale"}, nil
	}
	if w, ok := filingWeighted(weighted, best.accn); ok && (best.val > w*weightedRatio || best.val*weightedRatio < w) {
		return Shares{Why: "check"}, nil
	}
	n := best.val
	return Shares{N: &n, CIK: cik, AsOf: best.end.Format("2006-01-02"), Filed: best.filed, Accn: best.accn, How: how}, nil
}

type count struct {
	val         float64
	end         time.Time
	filed, accn string
	ambiguous   bool
}

// newestCount is the count from the newest filing (by filing date, then
// accession) that reports one; its latest date if it reports several (a
// balance sheet's comparative year-end). Two values for that date in that
// filing cannot be told apart.
func newestCount(facts []shareFact, now time.Time) *count {
	var best *count
	for _, f := range facts {
		if !forms[f.Form] || !accnRe.MatchString(f.Accn) || f.Start != "" {
			continue
		}
		filed, err0 := parseDate(f.Filed)
		e, err1 := parseDate(f.End)
		if err0 != nil || err1 != nil || e.After(filed) || filed.After(now) || filed.Year() < 2009 {
			continue
		}
		bad := !wholeShares(f.Val)
		switch {
		case best == nil || f.Filed > best.filed || (f.Filed == best.filed && f.Accn > best.accn):
		case f.Filed == best.filed && f.Accn == best.accn && e.After(best.end):
		case f.Filed == best.filed && f.Accn == best.accn && e.Equal(best.end):
			if bad || f.Val != best.val {
				best.ambiguous = true
			}
			continue
		default:
			continue
		}
		best = &count{val: f.Val, end: e, filed: f.Filed, accn: f.Accn, ambiguous: bad}
	}
	return best
}

// filingWeighted is the basic weighted-average count of the newest quarter in
// the filing the count is from: one filing, one share basis (a split before
// the filing is applied to both, or to neither).
func filingWeighted(facts []shareFact, accn string) (float64, bool) {
	best, val := "", 0.0
	for _, f := range facts {
		if f.Accn != accn || !finite(f.Val) || f.Val <= 0 {
			continue
		}
		s, err0 := parseDate(f.Start)
		e, err1 := parseDate(f.End)
		if err0 != nil || err1 != nil {
			continue
		}
		if n := days(s, e); n < quarterMin || n > quarterMax {
			continue
		}
		if f.End > best {
			best, val = f.End, f.Val
		}
	}
	return val, best != ""
}

func wholeShares(v float64) bool {
	return !math.IsNaN(v) && v >= 1 && v <= maxShares && v == math.Trunc(v)
}

// ValidateShares checks a shares file the way the app does: any bad entry
// rejects the whole file.
func ValidateShares(f *SharesFile) error {
	if f.V != 1 || f.Source != "SEC" {
		return fmt.Errorf("v/source")
	}
	gen, err := time.Parse(time.RFC3339, f.GeneratedAt)
	if err != nil {
		return fmt.Errorf("generatedAt: %w", err)
	}
	for sym, s := range f.Symbols {
		if err := validateShares(s, gen); err != nil {
			return fmt.Errorf("%s: %w", sym, err)
		}
		if !symbolRe.MatchString(sym) {
			return fmt.Errorf("symbol %q", sym)
		}
	}
	return nil
}

func validateShares(s Shares, gen time.Time) error {
	if s.N == nil {
		if !sharesWhys[s.Why] || s.CIK != 0 || s.AsOf != "" || s.Filed != "" || s.Accn != "" || s.How != "" {
			return fmt.Errorf("null count with fields or bad reason %q", s.Why)
		}
		return nil
	}
	if s.Why != "" || (s.How != "cover" && s.How != "balance") || !accnRe.MatchString(s.Accn) || s.CIK < 1 || s.CIK > 9999999999 {
		return fmt.Errorf("how %q / accn %q / cik %d / why %q", s.How, s.Accn, s.CIK, s.Why)
	}
	if !wholeShares(*s.N) {
		return fmt.Errorf("count %v", *s.N)
	}
	asOf, err := parseDate(s.AsOf)
	if err != nil {
		return err
	}
	if err := validateFiled(s.Filed, gen); err != nil {
		return err
	}
	if filed, _ := parseDate(s.Filed); asOf.After(filed) || gen.Sub(asOf) > sharesMaxAge*24*time.Hour {
		return fmt.Errorf("counted %s, filed %s", s.AsOf, s.Filed)
	}
	return nil
}
