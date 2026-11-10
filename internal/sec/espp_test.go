package sec

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

var esppNow = time.Date(2026, 11, 10, 9, 0, 0, 0, time.UTC)

const msftCIK = 789019

// esppFacts builds a companyfacts document with the given us-gaap concepts,
// each a unit name and a JSON array of facts.
func esppFacts(cik int64, concepts map[string][2]string) []byte {
	var parts []string
	for c, u := range concepts {
		parts = append(parts, fmt.Sprintf(`%q:{"units":{%q:%s}}`, c, u[0], u[1]))
	}
	return []byte(fmt.Sprintf(`{"cik":%d,"facts":{"us-gaap":{%s}}}`, cik, strings.Join(parts, ",")))
}

func esppPeriod(start, end string, val float64, accn, form, filed string) string {
	return fmt.Sprintf(`{"start":%q,"end":%q,"val":%v,"accn":%q,"form":%q,"filed":%q}`, start, end, val, accn, form, filed)
}

const (
	esppSharesConcept = "StockIssuedDuringPeriodSharesEmployeeStockPurchasePlans"
	esppValueConcept  = "StockIssuedDuringPeriodValueEmployeeStockPurchasePlan"
	accnK             = "0001193125-26-323660"
	accnQ             = "0001193125-26-400000"
)

func TestExtractEsppListsRecentIssuance(t *testing.T) {
	b := esppFacts(msftCIK, map[string][2]string{
		esppSharesConcept: {"shares", "[" + esppPeriod("2025-07-01", "2026-06-30", 5000000, accnK, "10-K", "2026-07-29") + "]"},
		// A newer quarter's value: its filing is the one named.
		esppValueConcept: {"USD", "[" + esppPeriod("2026-07-01", "2026-09-30", 120000000, accnQ, "10-Q", "2026-10-28") + "]"},
	})
	got, err := ExtractEspp(b, msftCIK, esppNow)
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || got.CIK != msftCIK || got.Filed != "2026-10-28" || got.Accn != accnQ || got.Discount != nil {
		t.Fatalf("got %+v", got)
	}
}

func TestExtractEsppNeedsRecentPositiveIssuanceInAPeriodicReport(t *testing.T) {
	cases := map[string]string{
		"zero":                     esppPeriod("2025-07-01", "2026-06-30", 0, accnK, "10-K", "2026-07-29"),
		"negative":                 esppPeriod("2025-07-01", "2026-06-30", -5, accnK, "10-K", "2026-07-29"),
		"filed over 2y ago":        esppPeriod("2023-07-01", "2024-06-30", 5, accnK, "10-K", "2024-07-29"),
		"period ended long ago":    esppPeriod("2021-07-01", "2022-06-30", 5, accnK, "10-K", "2026-07-29"),
		"filed after the snapshot": esppPeriod("2026-07-01", "2026-09-30", 5, accnQ, "10-Q", "2026-11-11"),
		"an 8-K":                   esppPeriod("2025-07-01", "2026-06-30", 5, accnK, "8-K", "2026-07-29"),
		"a bad accession":          esppPeriod("2025-07-01", "2026-06-30", 5, "x", "10-K", "2026-07-29"),
		"ends after filing":        esppPeriod("2026-07-01", "2026-12-31", 5, accnK, "10-Q", "2026-10-28"),
	}
	for name, fact := range cases {
		got, err := ExtractEspp(esppFacts(msftCIK, map[string][2]string{esppSharesConcept: {"shares", "[" + fact + "]"}}), msftCIK, esppNow)
		if err != nil || got != nil {
			t.Errorf("%s: got %+v, %v", name, got, err)
		}
	}
	// No concept at all: unknown, not an error.
	if got, err := ExtractEspp(esppFacts(msftCIK, nil), msftCIK, esppNow); err != nil || got != nil {
		t.Fatalf("none: %+v, %v", got, err)
	}
}

func TestExtractEsppDiscountOnlyWhenCleanAndSingle(t *testing.T) {
	issued := [2]string{"shares", "[" + esppPeriod("2025-07-01", "2026-06-30", 5000000, accnK, "10-K", "2026-07-29") + "]"}
	pct := func(vals ...float64) [2]string {
		var fs []string
		for _, v := range vals {
			fs = append(fs, esppPeriod("2025-07-01", "2026-06-30", v, accnK, "10-K", "2026-07-29"))
		}
		return [2]string{"pure", "[" + strings.Join(fs, ",") + "]"}
	}
	for _, c := range []struct {
		vals []float64
		want float64 // 0: no discount
	}{
		{[]float64{0.85}, 0.15},
		{[]float64{0.9, 0.9}, 0.1},
		{[]float64{0.95}, 0.05},
		// A discount entered where the price belongs, a slip, two plans: none.
		{[]float64{0.15}, 0},
		{[]float64{1.1}, 0},
		{[]float64{0.85, 0.9}, 0},
		{[]float64{0.85, 1}, 0},
	} {
		got, err := ExtractEspp(esppFacts(msftCIK, map[string][2]string{esppSharesConcept: issued, EsppPurchasePercent: pct(c.vals...)}), msftCIK, esppNow)
		if err != nil || got == nil {
			t.Fatalf("%v: %+v, %v", c.vals, got, err)
		}
		if (c.want == 0) != (got.Discount == nil) || (got.Discount != nil && *got.Discount != c.want) {
			t.Errorf("%v: discount %v, want %v", c.vals, got.Discount, c.want)
		}
	}
	// An old stated price does not count.
	old := [2]string{"pure", "[" + esppPeriod("2022-07-01", "2023-06-30", 0.85, accnK, "10-K", "2023-07-29") + "]"}
	if got, _ := ExtractEspp(esppFacts(msftCIK, map[string][2]string{esppSharesConcept: issued, EsppPurchasePercent: old}), msftCIK, esppNow); got == nil || got.Discount != nil {
		t.Fatalf("old: %+v", got)
	}
}

func TestExtractEsppRefusesAnotherCompanysFile(t *testing.T) {
	if _, err := ExtractEspp(esppFacts(1, nil), msftCIK, esppNow); err == nil {
		t.Fatal("want an error")
	}
	if _, err := ExtractEspp([]byte(`{"cik":789019,"facts":{}} {}`), msftCIK, esppNow); err == nil {
		t.Fatal("want an error for trailing data")
	}
}

func TestValidateEspp(t *testing.T) {
	d := 0.15
	good := func() *EsppFile {
		return &EsppFile{V: 1, Source: "SEC", GeneratedAt: "2026-11-10T09:00:00Z", Symbols: map[string]Espp{
			"MSFT":  {CIK: msftCIK, Filed: "2026-07-29", Accn: accnK},
			"BRK.B": {CIK: 1067983, Filed: "2026-08-01", Accn: accnQ, Discount: &d},
		}}
	}
	if err := ValidateEspp(good()); err != nil {
		t.Fatal(err)
	}
	bad := 0.2
	for name, edit := range map[string]func(*EsppFile){
		"version":        func(f *EsppFile) { f.V = 2 },
		"source":         func(f *EsppFile) { f.Source = "x" },
		"generatedAt":    func(f *EsppFile) { f.GeneratedAt = "today" },
		"symbol":         func(f *EsppFile) { f.Symbols["bad sym"] = f.Symbols["MSFT"] },
		"cik":            func(f *EsppFile) { e := f.Symbols["MSFT"]; e.CIK = 0; f.Symbols["MSFT"] = e },
		"accn":           func(f *EsppFile) { e := f.Symbols["MSFT"]; e.Accn = "1"; f.Symbols["MSFT"] = e },
		"future filing":  func(f *EsppFile) { e := f.Symbols["MSFT"]; e.Filed = "2026-11-11"; f.Symbols["MSFT"] = e },
		"old filing":     func(f *EsppFile) { e := f.Symbols["MSFT"]; e.Filed = "2024-01-01"; f.Symbols["MSFT"] = e },
		"bad date":       func(f *EsppFile) { e := f.Symbols["MSFT"]; e.Filed = "2026-02-30"; f.Symbols["MSFT"] = e },
		"other discount": func(f *EsppFile) { e := f.Symbols["MSFT"]; e.Discount = &bad; f.Symbols["MSFT"] = e },
	} {
		f := good()
		edit(f)
		if err := ValidateEspp(f); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}
