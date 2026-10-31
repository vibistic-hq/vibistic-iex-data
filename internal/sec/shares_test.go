package sec

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
)

var sharesNow = time.Date(2026, 10, 18, 9, 0, 0, 0, time.UTC)

const appleCIK = 320193

// companyFacts builds a companyfacts document with the given dei and us-gaap
// share facts (each a JSON array of facts).
func companyFacts(cover, balance, weighted string) []byte {
	var dei, gaap []string
	if cover != "" {
		dei = append(dei, `"EntityCommonStockSharesOutstanding":{"units":{"shares":`+cover+`}}`)
	}
	if balance != "" {
		gaap = append(gaap, `"CommonStockSharesOutstanding":{"units":{"shares":`+balance+`}}`)
	}
	if weighted != "" {
		gaap = append(gaap, `"WeightedAverageNumberOfSharesOutstandingBasic":{"units":{"shares":`+weighted+`}}`)
	}
	return []byte(`{"cik":320193,"facts":{"dei":{` + strings.Join(dei, ",") + `},"us-gaap":{` + strings.Join(gaap, ",") + `}}}`)
}

func instant(end string, val float64, accn, form, filed string) string {
	return fmt.Sprintf(`{"end":%q,"val":%v,"accn":%q,"form":%q,"filed":%q}`, end, val, accn, form, filed)
}

func quarter(start, end string, val float64, accn, filed string) string {
	return fmt.Sprintf(`{"start":%q,"end":%q,"val":%v,"accn":%q,"form":"10-Q","filed":%q}`, start, end, val, accn, filed)
}

const (
	accnA = "0000320193-26-000013"
	accnB = "0000320193-26-000020"
)

func TestExtractSharesCover(t *testing.T) {
	cover := "[" + instant("2026-04-17", 14687356000, accnA, "10-Q", "2026-05-01") + "," +
		instant("2026-07-17", 14594180000, accnB, "10-Q", "2026-07-31") + "," +
		// An 8-K is not read; nor a count filed after the snapshot.
		instant("2026-08-01", 1, "0000320193-26-000030", "8-K", "2026-08-02") + "," +
		instant("2026-10-17", 2, "0000320193-26-000040", "10-K", "2026-10-31") + "]"
	weighted := "[" + quarter("2026-03-29", "2026-06-27", 14750000000, accnB, "2026-07-31") + "]"
	got, err := ExtractShares(companyFacts(cover, "", weighted), appleCIK, 1, false, sharesNow)
	if err != nil {
		t.Fatal(err)
	}
	if got.N == nil || *got.N != 14594180000 || got.CIK != appleCIK || got.AsOf != "2026-07-17" || got.Filed != "2026-07-31" || got.Accn != accnB || got.How != "cover" {
		t.Fatalf("got %+v", got)
	}
	f := &SharesFile{V: 1, Source: "SEC", GeneratedAt: "2026-10-18T09:00:00Z", Symbols: map[string]Shares{"AAPL": got}}
	if err := ValidateShares(f); err != nil {
		t.Fatal(err)
	}
}

// A file whose document names another company is refused, even with no
// revenue in it (Codex review: the share count alone must be checked too).
func TestExtractSharesChecksTheCIK(t *testing.T) {
	cover := "[" + instant("2026-07-17", 100, accnB, "10-Q", "2026-07-31") + "]"
	if _, err := ExtractShares(companyFacts(cover, "", ""), 1, 1, false, sharesNow); err == nil {
		t.Fatal("accepted a file for another company")
	}
}

func TestExtractSharesReasons(t *testing.T) {
	good := "[" + instant("2026-07-17", 14594180000, accnB, "10-Q", "2026-07-31") + "]"
	w := func(val float64, accn string) string {
		return "[" + quarter("2026-03-29", "2026-06-27", val, accn, "2026-07-31") + "]"
	}
	for _, tc := range []struct {
		name                     string
		cover, balance, weighted string
		classes                  int
		reviewed                 bool
		want                     string
	}{
		{"no cover count", "", "", "", 1, false, "none"},
		{"multi-class, not reviewed", good, "", "", 2, false, "classes"},
		{"reviewed but no balance-sheet total", good, "", "", 2, true, "none"},
		{"stale", "[" + instant("2026-03-01", 100, accnB, "10-K", "2026-03-20") + "]", "", "", 1, false, "stale"},
		{"two values in one filing", "[" + instant("2026-07-17", 100, accnB, "10-Q", "2026-07-31") + "," + instant("2026-07-17", 200, accnB, "10-Q", "2026-07-31") + "]", "", "", 1, false, "check"},
		{"not whole shares", "[" + instant("2026-07-17", 100.5, accnB, "10-Q", "2026-07-31") + "]", "", "", 1, false, "check"},
		{"over 2x the weighted average (unit slip)", good, "", w(7000000000, accnB), 1, false, "check"},
		{"under half the weighted average", "[" + instant("2026-07-17", 7000000000, accnB, "10-Q", "2026-07-31") + "]", "", w(14750000000, accnB), 1, false, "check"},
	} {
		got, err := ExtractShares(companyFacts(tc.cover, tc.balance, tc.weighted), appleCIK, tc.classes, tc.reviewed, sharesNow)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if got.N != nil || got.Why != tc.want {
			t.Errorf("%s: got %+v, want why %q", tc.name, got, tc.want)
		}
		if err := validateShares(got, sharesNow); err != nil {
			t.Errorf("%s: %v", tc.name, err)
		}
	}
	// The weighted average of another filing is on its own share basis (a
	// split between the two filings): it is not compared (Codex review).
	got, _ := ExtractShares(companyFacts(good, "", w(3600000000, accnA)), appleCIK, 1, false, sharesNow)
	if got.N == nil {
		t.Fatalf("compared with another filing's weighted average: %+v", got)
	}
	// Within 2x of its own filing's newest quarter: kept.
	two := "[" + quarter("2025-12-28", "2026-03-28", 1000, accnB, "2026-07-31") + "," + quarter("2026-03-29", "2026-06-27", 14000000000, accnB, "2026-07-31") + "]"
	if got, _ := ExtractShares(companyFacts(good, "", two), appleCIK, 1, false, sharesNow); got.N == nil {
		t.Fatalf("not the newest quarter of the filing: %+v", got)
	}
}

// A reviewed multi-class company counts its balance-sheet total, the newest
// date in the newest filing (a 10-Q also reports the last year-end).
func TestExtractSharesBalance(t *testing.T) {
	balance := "[" + instant("2025-12-31", 12100000000, accnB, "10-Q", "2026-07-23") + "," + instant("2026-06-30", 12230000000, accnB, "10-Q", "2026-07-23") + "]"
	cover := "[" + instant("2026-07-15", 5800000000, accnB, "10-Q", "2026-07-23") + "]"
	got, err := ExtractShares(companyFacts(cover, balance, ""), appleCIK, 2, true, sharesNow)
	if err != nil {
		t.Fatal(err)
	}
	if got.N == nil || *got.N != 12230000000 || got.AsOf != "2026-06-30" || got.How != "balance" {
		t.Fatalf("got %+v", got)
	}
}

func TestValidateShares(t *testing.T) {
	n := 14594180000.0
	ok := Shares{N: &n, CIK: appleCIK, AsOf: "2026-07-17", Filed: "2026-07-31", Accn: accnB, How: "cover"}
	gen := "2026-10-18T09:00:00Z"
	check := func(s Shares) error {
		return ValidateShares(&SharesFile{V: 1, Source: "SEC", GeneratedAt: gen, Symbols: map[string]Shares{"AAPL": s}})
	}
	if err := check(ok); err != nil {
		t.Fatal(err)
	}
	if err := check(Shares{Why: "depositary"}); err != nil {
		t.Fatal(err)
	}
	big := 2e12
	frac := 10.5
	with := func(f func(*Shares)) Shares {
		s := ok
		f(&s)
		return s
	}
	for name, s := range map[string]Shares{
		"too many":        with(func(s *Shares) { s.N = &big }),
		"fraction":        with(func(s *Shares) { s.N = &frac }),
		"bad how":         with(func(s *Shares) { s.How = "guess" }),
		"no cik":          with(func(s *Shares) { s.CIK = 0 }),
		"counted late":    with(func(s *Shares) { s.AsOf = "2026-08-01" }),
		"stale":           with(func(s *Shares) { s.AsOf, s.Filed = "2026-03-01", "2026-03-20" }),
		"value and why":   with(func(s *Shares) { s.Why = "none" }),
		"null with field": {Why: "none", AsOf: ok.AsOf},
		"null with cik":   {Why: "none", CIK: 1},
		"unknown reason":  {Why: "guess"},
	} {
		if check(s) == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if ValidateShares(&SharesFile{V: 1, Source: "SEC", GeneratedAt: gen, Symbols: map[string]Shares{"aapl": ok}}) == nil {
		t.Error("lowercase symbol accepted")
	}
	b, _ := json.Marshal(ok)
	if string(b) != `{"n":14594180000,"cik":320193,"asOf":"2026-07-17","filed":"2026-07-31","accn":"0000320193-26-000020","how":"cover"}` {
		t.Errorf("json %s", b)
	}
}

func TestValidateSharesClasses(t *testing.T) {
	if err := ValidateSharesClasses([]SharesClass{{CIK: 1652044, Name: "Alphabet"}}); err != nil {
		t.Fatal(err)
	}
	for _, cs := range [][]SharesClass{{{CIK: 0, Name: "x"}}, {{CIK: 1, Name: ""}}, {{CIK: 1, Name: "a"}, {CIK: 1, Name: "b"}}} {
		if ValidateSharesClasses(cs) == nil {
			t.Errorf("accepted %+v", cs)
		}
	}
}

// One document only: bytes the revenue reader refused for trailing data give
// no count either (Codex review).
func TestExtractSharesRefusesTrailingData(t *testing.T) {
	b := companyFacts("["+instant("2026-07-17", 100, accnB, "10-Q", "2026-07-31")+"]", "", "")
	if _, err := ExtractShares(append(b, []byte(`{"cik":1}`)...), appleCIK, 1, false, sharesNow); err == nil {
		t.Fatal("accepted a second document")
	}
}

// The app refuses a file with more symbols than this whole (Codex review).
func TestValidateSharesSymbolCap(t *testing.T) {
	f := &SharesFile{V: 1, Source: "SEC", GeneratedAt: "2026-10-18T09:00:00Z", Symbols: map[string]Shares{}}
	for i := 0; len(f.Symbols) <= MaxShareSymbols; i++ {
		f.Symbols[string(rune('A'+i/676%26))+string(rune('A'+i/26%26))+string(rune('A'+i%26))+string(rune('A'+i/17576%26))] = Shares{Why: "none"}
	}
	if ValidateShares(f) == nil {
		t.Fatal("accepted more symbols than the app takes")
	}
}

// No public market for the common shares in the last two 10-Ks (a listed
// preferred or note): no count. A new listing's single zero is kept.
func TestExtractSharesNoFloat(t *testing.T) {
	good := "[" + instant("2026-07-17", 203805, accnB, "10-Q", "2026-07-31") + "]"
	withFloat := func(vals ...float64) []byte {
		var fs []string
		for i, v := range vals {
			fs = append(fs, fmt.Sprintf(`{"end":"%d-06-30","val":%v,"accn":"0000320193-%02d-000001","form":"10-K","filed":"%d-02-25"}`, 2024+i, v, 24+i, 2025+i))
		}
		b := companyFacts(good, "", "")
		return []byte(strings.Replace(string(b), `"dei":{`, `"dei":{"EntityPublicFloat":{"units":{"USD":[`+strings.Join(fs, ",")+`]}},`, 1))
	}
	for name, tc := range map[string]struct {
		vals []float64
		want string
	}{
		"two zeros":         {[]float64{0, 0}, "notcommon"},
		"zero then a float": {[]float64{0, 5e9}, ""},
		"a float then zero": {[]float64{5e9, 0}, ""},
		"one zero (new)":    {[]float64{0}, ""},
	} {
		got, err := ExtractShares(withFloat(tc.vals...), appleCIK, 1, false, sharesNow)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if tc.want == "" && got.N == nil || tc.want != "" && got.Why != tc.want {
			t.Errorf("%s: %+v", name, got)
		}
	}
}

func TestPreferred(t *testing.T) {
	for sym, want := range map[string]bool{"PHXE.P": true, "ETI.P": true, "BRK.B": false, "AAPL": false, "P": false, "PP": false} {
		if Preferred(sym) != want {
			t.Errorf("%s: %v", sym, !want)
		}
	}
}
