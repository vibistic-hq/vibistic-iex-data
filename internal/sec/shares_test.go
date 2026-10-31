package sec

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
)

var sharesNow = time.Date(2026, 10, 18, 9, 0, 0, 0, time.UTC)

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
		// An 8-K or a 20-F is not read; nor a count filed after the snapshot.
		instant("2026-08-01", 1, "0000320193-26-000030", "8-K", "2026-08-02") + "," +
		instant("2026-10-17", 2, "0000320193-26-000040", "10-K", "2026-10-31") + "]"
	weighted := "[" + quarter("2026-03-29", "2026-06-27", 14750000000, accnB, "2026-07-31") + "]"
	got, err := ExtractShares(companyFacts(cover, "", weighted), 1, false, sharesNow)
	if err != nil {
		t.Fatal(err)
	}
	if got.N == nil || *got.N != 14594180000 || got.AsOf != "2026-07-17" || got.Filed != "2026-07-31" || got.Accn != accnB || got.How != "cover" {
		t.Fatalf("got %+v", got)
	}
	f := &SharesFile{V: 1, Source: "SEC", GeneratedAt: "2026-10-18T09:00:00Z", Symbols: map[string]Shares{"AAPL": got}}
	if err := ValidateShares(f, false); err != nil {
		t.Fatal(err)
	}
}

func TestExtractSharesReasons(t *testing.T) {
	good := "[" + instant("2026-07-17", 14594180000, accnB, "10-Q", "2026-07-31") + "]"
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
		{"1000x the weighted average (unit slip)", "[" + instant("2026-07-17", 14594180000000, accnB, "10-Q", "2026-07-31") + "]", "", "[" + quarter("2026-03-29", "2026-06-27", 14750000000, accnB, "2026-07-31") + "]", 1, false, "check"},
		{"under half the weighted average", "[" + instant("2026-07-17", 7000000000, accnB, "10-Q", "2026-07-31") + "]", "", "[" + quarter("2026-03-29", "2026-06-27", 14750000000, accnB, "2026-07-31") + "]", 1, false, "check"},
	} {
		got, err := ExtractShares(companyFacts(tc.cover, tc.balance, tc.weighted), tc.classes, tc.reviewed, sharesNow)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if got.N != nil || got.Why != tc.want {
			t.Errorf("%s: got %+v, want why %q", tc.name, got, tc.want)
		}
		if err := validateShares(got, sharesNow, false); err != nil {
			t.Errorf("%s: %v", tc.name, err)
		}
	}
}

// A reviewed multi-class company counts its balance-sheet total, the newest
// date in the newest filing (a 10-Q also reports the last year-end).
func TestExtractSharesBalance(t *testing.T) {
	balance := "[" + instant("2025-12-31", 12100000000, accnB, "10-Q", "2026-07-23") + "," + instant("2026-06-30", 12230000000, accnB, "10-Q", "2026-07-23") + "]"
	cover := "[" + instant("2026-07-15", 5800000000, accnB, "10-Q", "2026-07-23") + "]"
	got, err := ExtractShares(companyFacts(cover, balance, ""), 2, true, sharesNow)
	if err != nil {
		t.Fatal(err)
	}
	if got.N == nil || *got.N != 12230000000 || got.AsOf != "2026-06-30" || got.How != "balance" {
		t.Fatalf("got %+v", got)
	}
}

func TestValidateShares(t *testing.T) {
	n := 14594180000.0
	ok := Shares{N: &n, AsOf: "2026-07-17", Filed: "2026-07-31", Accn: accnB, How: "cover"}
	gen := "2026-10-18T09:00:00Z"
	check := func(s Shares, built bool) error {
		return ValidateShares(&SharesFile{V: 1, Source: "SEC", GeneratedAt: gen, Symbols: map[string]Shares{"AAPL": s}}, built)
	}
	if err := check(ok, false); err != nil {
		t.Fatal(err)
	}
	withSplit := ok
	withSplit.Split = &Factor{New: 4, Old: 1}
	withSplit.Ref = &Ref{Date: "2026-07-17", Close: 210.5}
	if err := check(withSplit, true); err != nil {
		t.Fatal(err)
	}
	if check(withSplit, false) == nil {
		t.Error("split and ref accepted before build")
	}
	big := 2e12
	frac := 10.5
	for name, s := range map[string]Shares{
		"too many":        {N: &big, AsOf: ok.AsOf, Filed: ok.Filed, Accn: ok.Accn, How: "cover"},
		"fraction":        {N: &frac, AsOf: ok.AsOf, Filed: ok.Filed, Accn: ok.Accn, How: "cover"},
		"bad how":         {N: &n, AsOf: ok.AsOf, Filed: ok.Filed, Accn: ok.Accn, How: "guess"},
		"counted late":    {N: &n, AsOf: "2026-08-01", Filed: ok.Filed, Accn: ok.Accn, How: "cover"},
		"stale":           {N: &n, AsOf: "2026-03-01", Filed: "2026-03-20", Accn: ok.Accn, How: "cover"},
		"null with field": {Why: "none", AsOf: ok.AsOf},
		"unknown reason":  {Why: "guess"},
		"value and why":   {N: &n, AsOf: ok.AsOf, Filed: ok.Filed, Accn: ok.Accn, How: "cover", Why: "none"},
		"split 1:1":       {N: &n, AsOf: ok.AsOf, Filed: ok.Filed, Accn: ok.Accn, How: "cover", Split: &Factor{New: 2, Old: 2}},
		"ref after asOf":  {N: &n, AsOf: ok.AsOf, Filed: ok.Filed, Accn: ok.Accn, How: "cover", Ref: &Ref{Date: "2026-07-18", Close: 1}},
		"ref long before": {N: &n, AsOf: ok.AsOf, Filed: ok.Filed, Accn: ok.Accn, How: "cover", Ref: &Ref{Date: "2026-06-01", Close: 1}},
		"ref zero":        {N: &n, AsOf: ok.AsOf, Filed: ok.Filed, Accn: ok.Accn, How: "cover", Ref: &Ref{Date: "2026-07-17", Close: 0}},
	} {
		if check(s, true) == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if ValidateShares(&SharesFile{V: 1, Source: "SEC", GeneratedAt: gen, Symbols: map[string]Shares{"aapl": ok}}, false) == nil {
		t.Error("lowercase symbol accepted")
	}
	b, _ := json.Marshal(ok)
	if string(b) != `{"n":14594180000,"asOf":"2026-07-17","filed":"2026-07-31","accn":"0000320193-26-000020","how":"cover"}` {
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
