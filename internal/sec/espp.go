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
// Strongest evidence first (How "issued"): a 10-K, 10-Q or 10-KT filed in the
// last two years reports shares (unit "shares") or their value (unit "USD")
// issued under an employee stock purchase plan, above zero, for a well-formed
// period that ended in that time (us-gaap
// StockIssuedDuringPeriodSharesEmployeeStockPurchasePlans or
// StockIssuedDuringPeriodValueEmployeeStockPurchasePlan): shares actually
// issued under a plan lately. Expense recognized for a plan is not issuance
// (Codex review of PR #21). Without that, a plan named in the company's 10-K
// or Form S-8, from EDGAR full-text search (esppsearch.go, issue #22). A symbol
// not listed is unknown, never "no plan".
//
// No discount is published: the purchase-price tag is not specific to employee
// stock purchase plans, and the bulk facts cannot tie it to one (Codex review
// of PR #21). A company with no public market for its common stock (two newest
// annual reports with a public float of zero, as for the share counts) is not
// listed: its symbol is something else.

// EsppFile is espp.json in a secbuild snapshot and v1/espp.json.
type EsppFile struct {
	V           int             `json:"v"`
	Source      string          `json:"source"`      // "SEC"
	GeneratedAt string          `json:"generatedAt"` // the snapshot's
	Symbols     map[string]Espp `json:"symbols"`
}

// Espp is one listed symbol: its strongest evidence (How: shares issued, a
// plan named in its 10-K, or one named in a Form S-8; issue #22) and that
// filing.
type Espp struct {
	CIK   int64  `json:"cik"`
	Filed string `json:"filed"` // yyyy-mm-dd
	Accn  string `json:"accn"`
	How   string `json:"how"`
}

// EsppIssued are the concepts, each in its unit, whose positive value for a
// period means shares were issued under an ESPP in it.
var EsppIssued = map[string]string{
	"StockIssuedDuringPeriodSharesEmployeeStockPurchasePlans": "shares",
	"StockIssuedDuringPeriodValueEmployeeStockPurchasePlan":   "USD",
}

// The app's own limit (src/sec/espp.ts MAX_SYMBOLS).
const MaxEsppSymbols = 12000

// ExtractEspp reads one company's companyfacts JSON. Shares issued under a
// plan come first; otherwise the full-text search's 10-K or S-8 for the company
// (search may be nil). It returns nil with neither, or when the company has no
// public market for its common stock.
func ExtractEspp(b []byte, cik int64, now time.Time, search *SearchFile) (*Espp, error) {
	var cf struct {
		CIK   json.RawMessage `json:"cik"`
		Facts struct {
			DEI    map[string]json.RawMessage `json:"dei"`
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
	// No public market for the common stock: the listed symbol is something else.
	float, err := readUnit(cf.Facts.DEI, "EntityPublicFloat", "USD")
	if err != nil {
		return nil, err
	}
	if noFloat(float) {
		return nil, nil
	}
	oldest := now.Add(-time.Duration(esppHowAge[HowIssued]) * 24 * time.Hour)
	var best *Espp
	for concept, unit := range EsppIssued {
		facts, err := readUnit(cf.Facts.USGAAP, concept, unit)
		if err != nil {
			return nil, err
		}
		for _, f := range facts {
			if !forms[f.Form] || !accnRe.MatchString(f.Accn) || !finite(f.Val) || f.Val <= 0 {
				continue
			}
			// A duration: a real start on or before its end, the end in the window and
			// on or before the filing, the filing in the window.
			start, err0 := parseDate(f.Start)
			end, err1 := parseDate(f.End)
			filed, err2 := parseDate(f.Filed)
			if err0 != nil || err1 != nil || err2 != nil || start.After(end) || end.After(filed) || filed.After(now) || filed.Before(oldest) || end.Before(oldest) {
				continue
			}
			if best == nil || f.Filed > best.Filed || (f.Filed == best.Filed && f.Accn > best.Accn) {
				best = &Espp{CIK: cik, Filed: f.Filed, Accn: f.Accn, How: HowIssued}
			}
		}
	}
	if best == nil {
		if h, how, ok := search.Best(cik, now); ok {
			best = &Espp{CIK: cik, Filed: h.Filed, Accn: h.Accn, How: how}
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
		age, ok := esppHowAge[e.How]
		if !ok {
			return fmt.Errorf("%s: how %q", sym, e.How)
		}
		if filed, _ := parseDate(e.Filed); gen.Sub(filed) > time.Duration(age)*24*time.Hour {
			return fmt.Errorf("%s: filed %s", sym, e.Filed)
		}
	}
	return nil
}
