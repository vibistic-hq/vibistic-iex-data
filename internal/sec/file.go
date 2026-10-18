// Package sec turns SEC EDGAR's XBRL company facts into small per-symbol files
// of revenue and net income for Vibistic Stocks share pages (issue #8):
// numbers, dates and fixed codes only, never any text from a filing.
//
// Every period's values are as first reported: taken from the earliest 10-Q,
// 10-K or 10-KT that has them, so a fiscal year's quarters and the year itself
// are on one basis (later restatements, reclassifications and amendments are
// not reflected). The fourth quarter, which no 10-Q reports, is the year minus
// the first nine months, both from their own original filings and under the
// same concept. The year-earlier value (`prev`) comes from the same filing as
// the value, under the same concept, so a change between them is always like
// for like.
package sec

import (
	"fmt"
	"math"
	"regexp"
	"time"
)

// File is v1/sec/{SYMBOL}.json.
type File struct {
	V           int      `json:"v"`
	Source      string   `json:"source"` // "SEC"
	Symbol      string   `json:"symbol"`
	CIK         int64    `json:"cik"`
	GeneratedAt string   `json:"generatedAt"` // RFC 3339, when the snapshot was built
	Quarters    []Period `json:"quarters"`    // up to 12, oldest first
	Years       []Period `json:"years"`       // up to 5 fiscal years, oldest first
}

// Period is one fiscal quarter or year, by its first and last day.
type Period struct {
	Start     string `json:"start"`
	End       string `json:"end"`
	Revenue   Value  `json:"revenue"`
	NetIncome Value  `json:"netIncome"`
}

// Value is one figure in US dollars, or null with the reason.
type Value struct {
	Val     *float64 `json:"val"`
	Prev    *float64 `json:"prev,omitempty"`    // the year-earlier period's value, from the same filing and concept
	C       string   `json:"c,omitempty"`       // concept code, see Concepts
	Form    string   `json:"form,omitempty"`    // 10-Q, 10-K or 10-KT
	Filed   string   `json:"filed,omitempty"`   // yyyy-mm-dd
	Accn    string   `json:"accn,omitempty"`    // accession number of the filing
	Derived *Derived `json:"derived,omitempty"` // a fourth quarter: the year minus nine months
	Why     string   `json:"why,omitempty"`     // only when Val is null: none, ambiguous, check, negative or conflict
}

// Derived names the two filings a fourth quarter is computed from.
type Derived struct {
	Year Source `json:"year"`
	Nine Source `json:"nine"`
}

type Source struct {
	Accn  string `json:"accn"`
	Filed string `json:"filed"`
}

// Concepts: code to us-gaap concept. Revenue concepts in order of preference:
// the first one a filing reports for a period is used.
var Concepts = map[string]string{
	"rev":   "Revenues",
	"rfc":   "RevenueFromContractWithCustomerExcludingAssessedTax",
	"rfci":  "RevenueFromContractWithCustomerIncludingAssessedTax",
	"srn":   "SalesRevenueNet",
	"rnie":  "RevenuesNetOfInterestExpense",            // banks' total net revenue
	"rauor": "RegulatedAndUnregulatedOperatingRevenue", // utilities
	"rern":  "RealEstateRevenueNet",                    // real estate companies
	"ni":    "NetIncomeLoss",
}

var RevenueCodes = []string{"rev", "rfc", "rfci", "srn", "rnie", "rauor", "rern"}

// Parts of revenue, never published: a figure smaller than one of them in
// the same filing is not the total (a real estate company's contract revenue
// leaves out its rents, a bank's leaves out its interest income).
var partConcepts = []string{"OperatingLeaseLeaseIncome", "OperatingLeasesIncomeStatementLeaseRevenue", "InterestIncomeExpenseNet", "NoninterestIncome"}

const (
	MaxQuarters = 12
	MaxYears    = 5
	// Bounds of a period's length in days, both ends counted: quarters of
	// 12 to 17 weeks (Costco's 12-week quarters, Kroger's 16-week first
	// quarter), nine months of 36 to 41 weeks, years of 52 or 53 weeks.
	quarterMin, quarterMax = 83, 120
	nineMin, nineMax       = 250, 288
	yearMin, yearMax       = 358, 372
	maxAbs                 = 1e13 // dollars; the largest companies' revenue is under 1e12
)

var (
	symbolRe = regexp.MustCompile(`^[A-Z]{1,5}(\.[A-Z])?$`)
	accnRe   = regexp.MustCompile(`^[0-9]{10}-[0-9]{2}-[0-9]{6}$`)
	forms    = map[string]bool{"10-Q": true, "10-K": true, "10-KT": true}
	whys     = map[string]bool{"none": true, "ambiguous": true, "check": true, "negative": true, "conflict": true}
)

func parseDate(s string) (time.Time, error) {
	t, err := time.Parse("2006-01-02", s)
	if err != nil || t.Format("2006-01-02") != s {
		return time.Time{}, fmt.Errorf("bad date %q", s)
	}
	return t, nil
}

// days is a period's length with both ends counted.
func days(start, end time.Time) int {
	return int(end.Sub(start).Hours()/24) + 1
}

// Validate checks a file the way the app does before showing it: any bad
// field rejects the whole file.
func Validate(f *File) error {
	if f.V != 1 || f.Source != "SEC" {
		return fmt.Errorf("v/source")
	}
	if !symbolRe.MatchString(f.Symbol) {
		return fmt.Errorf("symbol %q", f.Symbol)
	}
	if f.CIK < 1 || f.CIK > 9999999999 {
		return fmt.Errorf("cik %d", f.CIK)
	}
	gen, err := time.Parse(time.RFC3339, f.GeneratedAt)
	if err != nil {
		return fmt.Errorf("generatedAt: %w", err)
	}
	if len(f.Quarters) > MaxQuarters || len(f.Years) > MaxYears || len(f.Quarters)+len(f.Years) == 0 {
		return fmt.Errorf("%d quarters, %d years", len(f.Quarters), len(f.Years))
	}
	for _, list := range []struct {
		ps       []Period
		min, max int
		year     bool
	}{{f.Quarters, quarterMin, quarterMax, false}, {f.Years, yearMin, yearMax, true}} {
		var prevEnd time.Time
		for i, p := range list.ps {
			s, err := parseDate(p.Start)
			if err != nil {
				return err
			}
			e, err := parseDate(p.End)
			if err != nil {
				return err
			}
			if n := days(s, e); n < list.min || n > list.max {
				return fmt.Errorf("period %s..%s is %d days", p.Start, p.End, n)
			}
			if i > 0 && !s.After(prevEnd) {
				return fmt.Errorf("period %s..%s overlaps or is out of order", p.Start, p.End)
			}
			prevEnd = e
			if err := validateValue(p.Revenue, true, list.year, e, gen); err != nil {
				return fmt.Errorf("%s revenue: %w", p.End, err)
			}
			if err := validateValue(p.NetIncome, false, list.year, e, gen); err != nil {
				return fmt.Errorf("%s net income: %w", p.End, err)
			}
			if p.Revenue.Val == nil && p.NetIncome.Val == nil {
				return fmt.Errorf("%s: no value", p.End)
			}
		}
	}
	return nil
}

func finite(v float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) && math.Abs(v) <= maxAbs }

// validateValue checks one figure of a period ending on `end`: a year's
// comes from an annual report and is never derived, a derived quarter's year
// is an annual report, and no period ends after the filing that reports it.
func validateValue(v Value, revenue, year bool, end, gen time.Time) error {
	if v.Val == nil {
		if !whys[v.Why] || v.Prev != nil || v.C != "" || v.Form != "" || v.Filed != "" || v.Accn != "" || v.Derived != nil {
			return fmt.Errorf("null value with fields or bad reason %q", v.Why)
		}
		return nil
	}
	if v.Why != "" {
		return fmt.Errorf("value with a reason")
	}
	if !finite(*v.Val) || (revenue && *v.Val < 0) {
		return fmt.Errorf("value %v", *v.Val)
	}
	if v.Prev != nil && (!finite(*v.Prev) || (revenue && *v.Prev < 0)) {
		return fmt.Errorf("prev %v", *v.Prev)
	}
	if _, ok := Concepts[v.C]; !ok || (v.C == "ni") == revenue {
		return fmt.Errorf("concept %q", v.C)
	}
	if !forms[v.Form] || !accnRe.MatchString(v.Accn) {
		return fmt.Errorf("form %q / accn %q", v.Form, v.Accn)
	}
	if err := validateFiled(v.Filed, gen); err != nil {
		return err
	}
	if filed, _ := parseDate(v.Filed); end.After(filed) {
		return fmt.Errorf("period ends after its filing %s", v.Filed)
	}
	annual := v.Form == "10-K" || v.Form == "10-KT"
	if year && (!annual || v.Derived != nil) {
		return fmt.Errorf("a year from %s (derived %v)", v.Form, v.Derived != nil)
	}
	if v.Derived != nil && !annual {
		return fmt.Errorf("derived quarter from %s", v.Form)
	}
	if d := v.Derived; d != nil {
		for _, s := range []Source{d.Year, d.Nine} {
			if !accnRe.MatchString(s.Accn) {
				return fmt.Errorf("derived accn %q", s.Accn)
			}
			if err := validateFiled(s.Filed, gen); err != nil {
				return err
			}
		}
		if d.Year.Accn != v.Accn || d.Year.Filed != v.Filed || d.Nine.Filed > d.Year.Filed {
			return fmt.Errorf("derived: filings out of place")
		}
	}
	return nil
}

func validateFiled(s string, gen time.Time) error {
	t, err := parseDate(s)
	if err != nil {
		return err
	}
	if t.Year() < 2009 || t.After(gen) {
		return fmt.Errorf("filed %s", s)
	}
	return nil
}
