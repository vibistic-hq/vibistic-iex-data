package sec

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

var esppNow = time.Date(2026, 11, 10, 9, 0, 0, 0, time.UTC)

const msftCIK = 789019

// esppFacts builds a companyfacts document with the given us-gaap and dei
// concepts, each a unit name and a JSON array of facts.
func esppFacts(cik int64, gaap, dei map[string][2]string) []byte {
	join := func(m map[string][2]string) string {
		var parts []string
		for c, u := range m {
			parts = append(parts, fmt.Sprintf(`%q:{"units":{%q:%s}}`, c, u[0], u[1]))
		}
		return strings.Join(parts, ",")
	}
	return []byte(fmt.Sprintf(`{"cik":%d,"facts":{"dei":{%s},"us-gaap":{%s}}}`, cik, join(dei), join(gaap)))
}

func esppPeriod(start, end string, val float64, accn, form, filed string) string {
	return fmt.Sprintf(`{"start":%q,"end":%q,"val":%v,"accn":%q,"form":%q,"filed":%q}`, start, end, val, accn, form, filed)
}

const (
	esppSharesConcept = "StockIssuedDuringPeriodSharesEmployeeStockPurchasePlans"
	esppValueConcept  = "StockIssuedDuringPeriodValueEmployeeStockPurchasePlan"
	esppAPICConcept   = "AdjustmentsToAdditionalPaidInCapitalShareBasedCompensationEmployeeStockPurchaseProgramRequisiteServicePeriodRecognition"
	accnK             = "0001193125-26-323660"
	accnQ             = "0001193125-26-400000"
)

func TestExtractEsppListsRecentIssuance(t *testing.T) {
	b := esppFacts(msftCIK, map[string][2]string{
		esppSharesConcept: {"shares", "[" + esppPeriod("2025-07-01", "2026-06-30", 5000000, accnK, "10-K", "2026-07-29") + "]"},
		// A newer quarter's value: its filing is the one named.
		esppValueConcept: {"USD", "[" + esppPeriod("2026-07-01", "2026-09-30", 120000000, accnQ, "10-Q", "2026-10-28") + "]"},
	}, nil)
	got, err := ExtractEspp(b, msftCIK, esppNow, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || *got != (Espp{CIK: msftCIK, Filed: "2026-10-28", Accn: accnQ, How: HowIssued}) {
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
		// Codex review of PR #21: a duration needs a real start on or before its end.
		"no start":       `{"end":"2026-06-30","val":5,"accn":"` + accnK + `","form":"10-K","filed":"2026-07-29"}`,
		"bad start":      esppPeriod("2026-02-30", "2026-06-30", 5, accnK, "10-K", "2026-07-29"),
		"reversed dates": esppPeriod("2026-07-01", "2026-06-30", 5, accnK, "10-K", "2026-07-29"),
	}
	for name, fact := range cases {
		got, err := ExtractEspp(esppFacts(msftCIK, map[string][2]string{esppSharesConcept: {"shares", "[" + fact + "]"}}, nil), msftCIK, esppNow, nil)
		if err != nil || got != nil {
			t.Errorf("%s: got %+v, %v", name, got, err)
		}
	}
	// No concept at all: unknown, not an error.
	if got, err := ExtractEspp(esppFacts(msftCIK, nil, nil), msftCIK, esppNow, nil); err != nil || got != nil {
		t.Fatalf("none: %+v, %v", got, err)
	}
}

// Codex review of PR #21.
func TestExtractEsppRefusesWrongUnitsAndExpenseAlone(t *testing.T) {
	recent := "[" + esppPeriod("2025-07-01", "2026-06-30", 5000000, accnK, "10-K", "2026-07-29") + "]"
	for name, gaap := range map[string]map[string][2]string{
		"share count in USD": {esppSharesConcept: {"USD", recent}},
		"value in shares":    {esppValueConcept: {"shares", recent}},
		// Expense recognized for a plan is not shares issued.
		"expense only": {esppAPICConcept: {"USD", recent}},
	} {
		if got, err := ExtractEspp(esppFacts(msftCIK, gaap, nil), msftCIK, esppNow, nil); err != nil || got != nil {
			t.Errorf("%s: got %+v, %v", name, got, err)
		}
	}
}

// Codex review of PR #21: no public market for the common stock, as for the share counts.
func TestExtractEsppSkipsCompaniesWithNoPublicFloat(t *testing.T) {
	issued := map[string][2]string{esppSharesConcept: {"shares", "[" + esppPeriod("2025-07-01", "2026-06-30", 5000000, accnK, "10-K", "2026-07-29") + "]"}}
	zero := func(accn, filed string) string {
		return fmt.Sprintf(`{"end":"2025-12-31","val":0,"accn":%q,"form":"10-K","filed":%q}`, accn, filed)
	}
	none := map[string][2]string{"EntityPublicFloat": {"USD", "[" + zero("0000000001-25-000001", "2025-03-01") + "," + zero("0000000001-26-000001", "2026-03-01") + "]"}}
	if got, err := ExtractEspp(esppFacts(msftCIK, issued, none), msftCIK, esppNow, nil); err != nil || got != nil {
		t.Fatalf("no float: %+v, %v", got, err)
	}
	// One such report alone (a new listing's first 10-K) is kept.
	one := map[string][2]string{"EntityPublicFloat": {"USD", "[" + zero("0000000001-26-000001", "2026-03-01") + "]"}}
	if got, err := ExtractEspp(esppFacts(msftCIK, issued, one), msftCIK, esppNow, nil); err != nil || got == nil {
		t.Fatalf("one report: %+v, %v", got, err)
	}
}

func TestExtractEsppRefusesAnotherCompanysFile(t *testing.T) {
	if _, err := ExtractEspp(esppFacts(1, nil, nil), msftCIK, esppNow, nil); err == nil {
		t.Fatal("want an error")
	}
	if _, err := ExtractEspp([]byte(`{"cik":789019,"facts":{}} {}`), msftCIK, esppNow, nil); err == nil {
		t.Fatal("want an error for trailing data")
	}
}

func TestValidateEspp(t *testing.T) {
	good := func() *EsppFile {
		return &EsppFile{V: 1, Source: "SEC", GeneratedAt: "2026-11-10T09:00:00Z", Symbols: map[string]Espp{
			"MSFT":  {CIK: msftCIK, Filed: "2026-07-29", Accn: accnK, How: HowIssued},
			"BRK.B": {CIK: 1067983, Filed: "2026-08-01", Accn: accnQ, How: HowTenK},
		}}
	}
	if err := ValidateEspp(good()); err != nil {
		t.Fatal(err)
	}
	for name, edit := range map[string]func(*EsppFile){
		"version":       func(f *EsppFile) { f.V = 2 },
		"source":        func(f *EsppFile) { f.Source = "x" },
		"generatedAt":   func(f *EsppFile) { f.GeneratedAt = "today" },
		"symbol":        func(f *EsppFile) { f.Symbols["bad sym"] = f.Symbols["MSFT"] },
		"cik":           func(f *EsppFile) { e := f.Symbols["MSFT"]; e.CIK = 0; f.Symbols["MSFT"] = e },
		"accn":          func(f *EsppFile) { e := f.Symbols["MSFT"]; e.Accn = "1"; f.Symbols["MSFT"] = e },
		"future filing": func(f *EsppFile) { e := f.Symbols["MSFT"]; e.Filed = "2026-11-11"; f.Symbols["MSFT"] = e },
		"old filing":    func(f *EsppFile) { e := f.Symbols["MSFT"]; e.Filed = "2024-01-01"; f.Symbols["MSFT"] = e },
		"bad date":      func(f *EsppFile) { e := f.Symbols["MSFT"]; e.Filed = "2026-02-30"; f.Symbols["MSFT"] = e },
	} {
		f := good()
		edit(f)
		if err := ValidateEspp(f); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}
