package sec

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"time"
)

// Employee stock purchase plans for Vibistic Stocks' estimated ESPP column
// (vibistic-stocks#263, issue #20): which companies' SEC filings show one, in
// v1/espp.json.
//
// A symbol is listed only when its company's 10-K, 10-Q or 10-KT filed in the
// last two years reports shares or value issued under an employee stock
// purchase plan (us-gaap StockIssuedDuringPeriod...EmployeeStockPurchasePlan(s)
// or the ESPP paid-in-capital adjustment), above zero, for a period that ended
// in that time. That is evidence of a plan that issued shares lately. A symbol
// not listed is unknown, never "no plan": many companies with a plan (Apple)
// tag none of these (checked 2026-11-10: 822 of 6,054 listed companies).
//
// The discount is given only when the filings of the same two years state the
// plan's purchase price as exactly 85%, 90% or 95% of market, and only one of
// those (the tag also carries discounts, other plans' figures and slips such
// as 1.1: anything else, or two values, leaves it out).

// EsppFile is espp.json in a secbuild snapshot and v1/espp.json.
type EsppFile struct {
	V           int             `json:"v"`
	Source      string          `json:"source"`      // "SEC"
	GeneratedAt string          `json:"generatedAt"` // the snapshot's
	Symbols     map[string]Espp `json:"symbols"`
}

// Espp is one listed symbol: the newest filing that shows ESPP shares issued,
// and the discount when stated.
type Espp struct {
	CIK      int64    `json:"cik"`
	Filed    string   `json:"filed"` // yyyy-mm-dd
	Accn     string   `json:"accn"`
	Discount *float64 `json:"discount,omitempty"` // 0.05, 0.1 or 0.15
}

// EsppIssued are the concepts whose positive value means shares were issued
// under an ESPP in the period.
var EsppIssued = []string{
	"StockIssuedDuringPeriodSharesEmployeeStockPurchasePlans",
	"StockIssuedDuringPeriodValueEmployeeStockPurchasePlan",
	"AdjustmentsToAdditionalPaidInCapitalShareBasedCompensationEmployeeStockPurchaseProgramRequisiteServicePeriodRecognition",
}

// EsppPurchasePercent is the plan's purchase price as a share of market.
const EsppPurchasePercent = "SharebasedCompensationArrangementBySharebasedPaymentAwardPurchasePriceOfCommonStockPercent"

const (
	esppMaxAge = 730 // days from the filing to the snapshot
	// The app's own limit (src/sec/espp.ts MAX_SYMBOLS).
	MaxEsppSymbols = 12000
)

// The purchase prices accepted, and the discount each means.
var esppDiscounts = map[float64]float64{0.85: 0.15, 0.9: 0.1, 0.95: 0.05}

// ExtractEspp reads one company's companyfacts JSON. It returns nil when the
// filings show no recent ESPP issuance.
func ExtractEspp(b []byte, cik int64, now time.Time) (*Espp, error) {
	var cf struct {
		CIK   json.RawMessage `json:"cik"`
		Facts struct {
			USGAAP map[string]json.RawMessage `json:"us-gaap"`
		} `json:"facts"`
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	if err := dec.Decode(&cf); err != nil {
		return nil, err
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, fmt.Errorf("data after the document")
	}
	if got, err := parseCIK(cf.CIK); err != nil || got != cik {
		return nil, fmt.Errorf("cik %.64q in a file for %d", cf.CIK, cik)
	}
	all := func(concept string) ([]shareFact, error) {
		raw, ok := cf.Facts.USGAAP[concept]
		if !ok {
			return nil, nil
		}
		var c struct {
			Units map[string][]shareFact `json:"units"`
		}
		if err := json.Unmarshal(raw, &c); err != nil {
			return nil, fmt.Errorf("%s: %w", concept, err)
		}
		var out []shareFact
		for _, u := range c.Units {
			out = append(out, u...)
		}
		return out, nil
	}
	oldest := now.Add(-esppMaxAge * 24 * time.Hour)
	// A fact from a periodic report filed in the window, for a period that
	// ended in it, ending on or before its filing.
	recent := func(f shareFact) (time.Time, bool) {
		if !forms[f.Form] || !accnRe.MatchString(f.Accn) {
			return time.Time{}, false
		}
		filed, err0 := parseDate(f.Filed)
		end, err1 := parseDate(f.End)
		if err0 != nil || err1 != nil || filed.After(now) || filed.Before(oldest) || end.Before(oldest) || end.After(filed) {
			return time.Time{}, false
		}
		return filed, true
	}
	var best *Espp
	for _, concept := range EsppIssued {
		facts, err := all(concept)
		if err != nil {
			return nil, err
		}
		for _, f := range facts {
			if _, ok := recent(f); !ok || !finite(f.Val) || f.Val <= 0 {
				continue
			}
			if best == nil || f.Filed > best.Filed || (f.Filed == best.Filed && f.Accn > best.Accn) {
				best = &Espp{CIK: cik, Filed: f.Filed, Accn: f.Accn}
			}
		}
	}
	if best == nil {
		return nil, nil
	}
	pct, err := all(EsppPurchasePercent)
	if err != nil {
		return nil, err
	}
	seen := map[float64]bool{}
	clean := true
	for _, f := range pct {
		if _, ok := recent(f); !ok {
			continue
		}
		if _, ok := esppDiscounts[f.Val]; !ok {
			clean = false
		}
		seen[f.Val] = true
	}
	if clean && len(seen) == 1 {
		for v := range seen {
			d := esppDiscounts[v]
			best.Discount = &d
		}
	}
	return best, nil
}

// ValidateEspp checks an ESPP file the way the app does: any bad entry
// rejects the whole file.
func ValidateEspp(f *EsppFile) error {
	if f.V != 1 || f.Source != "SEC" {
		return fmt.Errorf("v/source")
	}
	gen, err := time.Parse(time.RFC3339, f.GeneratedAt)
	if err != nil {
		return fmt.Errorf("generatedAt: %w", err)
	}
	if len(f.Symbols) > MaxEsppSymbols {
		return fmt.Errorf("%d symbols", len(f.Symbols))
	}
	for sym, e := range f.Symbols {
		if !symbolRe.MatchString(sym) {
			return fmt.Errorf("symbol %q", sym)
		}
		if e.CIK < 1 || e.CIK > 9999999999 || !accnRe.MatchString(e.Accn) {
			return fmt.Errorf("%s: cik %d / accn %q", sym, e.CIK, e.Accn)
		}
		if err := validateFiled(e.Filed, gen); err != nil {
			return fmt.Errorf("%s: %w", sym, err)
		}
		if filed, _ := parseDate(e.Filed); gen.Sub(filed) > esppMaxAge*24*time.Hour {
			return fmt.Errorf("%s: filed %s", sym, e.Filed)
		}
		if e.Discount != nil && *e.Discount != 0.05 && *e.Discount != 0.1 && *e.Discount != 0.15 {
			return fmt.Errorf("%s: discount %v", sym, *e.Discount)
		}
	}
	return nil
}
