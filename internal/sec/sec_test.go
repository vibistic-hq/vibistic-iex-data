package sec

import (
	"encoding/json"
	"math"
	"os"
	"strings"
	"testing"
	"time"
)

var now = time.Date(2026, 10, 18, 9, 0, 0, 0, time.UTC)

func extractFile(t *testing.T, name string) *File {
	t.Helper()
	fh, err := os.Open("testdata/" + name + ".json")
	if err != nil {
		t.Fatal(err)
	}
	defer fh.Close()
	f, err := Extract(fh, name, now)
	if err != nil {
		t.Fatal(err)
	}
	if f == nil {
		t.Fatalf("%s: no file", name)
	}
	if err := Validate(f); err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return f
}

func period(t *testing.T, ps []Period, end string) Period {
	t.Helper()
	for _, p := range ps {
		if p.End == end {
			return p
		}
	}
	t.Fatalf("no period ending %s", end)
	return Period{}
}

func val(t *testing.T, v Value) float64 {
	t.Helper()
	if v.Val == nil {
		t.Fatalf("null (%s)", v.Why)
	}
	return *v.Val
}

// Real filings (reduced companyfacts in testdata/), values checked by hand
// against the companies' reported results.
func TestRealCompanies(t *testing.T) {
	cases := []struct {
		name, kind, end string
		revenue, net    float64
		c               string
		derived         bool
	}{
		// Apple FY2023 Q4: no 10-Q, the 10-K's year minus the Q3 10-Q's nine months.
		{"AAPL", "q", "2023-09-30", 89498e6, 22956e6, "rfc", true},
		{"AAPL", "y", "2025-09-27", 416161e6, 112010e6, "rfc", false},
		// Nvidia's fiscal year ends in January.
		{"NVDA", "q", "2025-01-26", 39331e6, 22091e6, "rev", true},
		{"NVDA", "y", "2025-01-26", 130497e6, 72880e6, "rev", false},
		{"NVDA", "q", "2026-07-26", 96221e6, 59688e6, "rev", false},
		// Cloudflare: losses stay negative.
		{"NET", "q", "2025-12-31", 614507e3, -12077e3, "rfc", true},
		// JPMorgan: quarterly revenue only as net of interest expense; its Q4 from that concept.
		{"JPM", "q", "2025-12-31", 45798e6, 13025e6, "rnie", true},
		{"JPM", "q", "2026-06-30", 57347e6, 21155e6, "rnie", false},
		// Alphabet Q1 2025: as first filed (contract revenue, 2025-04-25), not a later comparative.
		{"GOOGL", "q", "2025-03-31", 90234e6, 34540e6, "rfc", false},
		// HF Sinclair and NiSource tag the year's total with Q4's dates: the computed Q4 instead.
		{"DINO", "q", "2024-12-31", 6500e6, -213.5e6, "rfc", true},
		{"NI", "q", "2025-12-31", 1887e6, 257.8e6, "rfc", true},
		// Costco: 12-week quarters, a 16-week fourth quarter.
		{"COST", "q", "2026-05-10", 70530e6, 2192e6, "rfc", false},
		{"COST", "q", "2026-08-30", 95720e6, 2998e6, "rfc", true},
		// Kroger: a 16-week first quarter.
		{"KR", "q", "2026-05-23", 46120e6, 903e6, "rfc", false},
	}
	for _, c := range cases {
		f := extractFile(t, c.name)
		ps := f.Quarters
		if c.kind == "y" {
			ps = f.Years
		}
		p := period(t, ps, c.end)
		if p.Revenue.Val == nil || p.NetIncome.Val == nil {
			t.Errorf("%s %s: revenue %s / net income %s", c.name, c.end, p.Revenue.Why, p.NetIncome.Why)
			continue
		}
		// Exact, or to the rounding the expected figure is written with (4 significant digits).
		near := func(got, want float64) bool { return got == want || math.Abs(got-want) <= math.Abs(want)*5e-4 }
		if got := val(t, p.Revenue); !near(got, c.revenue) {
			t.Errorf("%s %s revenue %v, want %v", c.name, c.end, got, c.revenue)
		}
		if got := val(t, p.NetIncome); !near(got, c.net) {
			t.Errorf("%s %s net income %v, want %v", c.name, c.end, got, c.net)
		}
		if p.Revenue.C != c.c || (p.Revenue.Derived != nil) != c.derived {
			t.Errorf("%s %s: concept %s derived %v", c.name, c.end, p.Revenue.C, p.Revenue.Derived != nil)
		}
	}
	// JPM's quarterly Revenues stop in 2014: those quarters never come back as rows.
	for _, p := range extractFile(t, "JPM").Quarters {
		if p.End < "2023-01-01" {
			t.Errorf("JPM: old quarter %s kept", p.End)
		}
	}
	g := period(t, extractFile(t, "GOOGL").Quarters, "2025-03-31").Revenue
	if g.Filed != "2025-04-25" {
		t.Errorf("GOOGL Q1 2025 filed %s, want the original 2025-04-25", g.Filed)
	}
	// The year-earlier value from the same filing (Apple FY2023 Q4 against FY2022 Q4).
	a := period(t, extractFile(t, "AAPL").Quarters, "2023-09-30").Revenue
	if a.Prev == nil || *a.Prev != 90146e6 {
		t.Errorf("AAPL FY2023 Q4 prev %v", a.Prev)
	}
	f := extractFile(t, "NVDA")
	if len(f.Quarters) != MaxQuarters || len(f.Years) != MaxYears {
		t.Errorf("NVDA: %d quarters, %d years", len(f.Quarters), len(f.Years))
	}
	nullRevenueWhy := func(name, kind, end, why string) {
		t.Helper()
		f := extractFile(t, name)
		ps := f.Quarters
		if kind == "y" {
			ps = f.Years
		}
		if r := period(t, ps, end).Revenue; r.Val != nil || r.Why != why {
			t.Errorf("%s %s: %+v, want null (%s)", name, end, r, why)
		}
	}
	nullRevenue := func(name, kind, end string) { t.Helper(); nullRevenueWhy(name, kind, end, "check") }
	// Berkshire's Revenues and its (smaller) contract revenue, Republic Services'
	// Revenues (counting intercompany sales) and contract revenue: two totals, neither shown.
	nullRevenueWhy("BRK", "y", "2025-12-31", "conflict")
	nullRevenueWhy("RSG", "y", "2024-12-31", "conflict")
	// Better: Revenues and net revenue far apart; Plexus: its nine months' two
	// concepts a thousand times apart, so no fourth quarter.
	nullRevenueWhy("BETR", "y", "2024-12-31", "conflict")
	nullRevenueWhy("PLXS", "q", "2025-09-27", "conflict")
	// BlackRock's Revenues is smaller than its contract revenue (one should be part
	// of the other): which is the total cannot be told, so neither is shown.
	nullRevenueWhy("BLK", "y", "2024-12-31", "conflict")
	// AST SpaceMobile's 2024 10-K: contract revenue above the figure that includes taxes.
	nullRevenueWhy("ASTS", "y", "2024-12-31", "conflict")
	// Huntington and M&T tag only fee income as contract revenue: smaller than
	// their own net interest income, so not their revenue.
	nullRevenue("HBAN", "y", "2025-12-31")
	nullRevenue("MTB", "y", "2025-12-31")
	// NextEra Q3 2023: revenue shown, but its year-earlier figure is not, as the
	// filing's concepts disagree for the earlier quarter.
	nee := period(t, extractFile(t, "NEE").Quarters, "2023-09-30").Revenue
	if val(t, nee) != 7200e6 || nee.Prev != nil {
		t.Errorf("NEE Q3 2023 %+v", nee)
	}
	// AEP Q3 2023: net income tagged with the second quarter's figure; the next
	// year's 10-Q shows the quarter's real one.
	if n := period(t, extractFile(t, "AEP").Quarters, "2023-09-30").NetIncome; n.Val != nil || n.Why != "check" {
		t.Errorf("AEP Q3 2023 net income %+v", n)
	}
	// L3Harris: the year's total under Q4's dates, no year to check it against.
	nullRevenue("LHX", "q", "2026-01-02")
	// Playboy: a business reclassified mid-year, the nine months no longer the sum of the quarters.
	nullRevenue("PLBY", "q", "2024-12-31")
	// Better: a quarter restated to under a third of what was first filed.
	nullRevenue("BETR", "q", "2023-09-30")
	// TYGO: contract revenue in thousands tagged as dollars for 2022-2024, beside
	// a right Revenues: those periods are not shown; 2025 on (Revenues only) is.
	ty := extractFile(t, "TYGO")
	for _, p := range append(ty.Quarters, ty.Years...) {
		if p.Revenue.Val != nil && (*p.Revenue.Val > 1e9 || p.End < "2025-01-01") {
			t.Errorf("TYGO %s revenue shown: %v", p.End, *p.Revenue.Val)
		}
	}
	if val(t, period(t, ty.Quarters, "2025-03-31").Revenue) != 18839e3 {
		t.Error("TYGO Q1 2025")
	}
	if val(t, period(t, ty.Quarters, "2026-06-30").NetIncome) != 2174e3 {
		t.Error("TYGO net income")
	}
	// Costco's 2010-era quarters (with the old 85-99 day rule the only "quarters"
	// left) never come back: rows stay within the company's newest period's window.
	for _, n := range []string{"COST", "KR"} {
		f := extractFile(t, n)
		if len(f.Quarters) != MaxQuarters || f.Quarters[0].End < "2023-01-01" {
			t.Errorf("%s quarters %d from %s", n, len(f.Quarters), f.Quarters[0].End)
		}
	}
}

// facts builds a companyfacts document from lines "concept start end val filed form [accn]".
func facts(lines ...string) string {
	units := map[string][]map[string]any{}
	for i, l := range lines {
		w := strings.Fields(l)
		accn := "0000000001-26-" + strings.Repeat("0", 5) + string(rune('0'+i%10))
		if len(w) > 6 {
			accn = w[6]
		}
		var v float64
		json.Unmarshal([]byte(w[3]), &v)
		units[w[0]] = append(units[w[0]], map[string]any{"start": w[1], "end": w[2], "val": v, "filed": w[4], "form": w[5], "accn": accn})
	}
	gaap := map[string]any{}
	for c, fs := range units {
		gaap[c] = map[string]any{"units": map[string]any{"USD": fs}}
	}
	b, _ := json.Marshal(map[string]any{"cik": 42, "facts": map[string]any{"us-gaap": gaap}})
	return string(b)
}

func extract(t *testing.T, doc string) *File {
	t.Helper()
	f, err := Extract(strings.NewReader(doc), "TEST", now)
	if err != nil {
		t.Fatal(err)
	}
	if f != nil {
		if err := Validate(f); err != nil {
			t.Fatal(err)
		}
	}
	return f
}

func TestFirstReportedWins(t *testing.T) {
	f := extract(t, facts(
		"Revenues 2026-01-01 2026-03-31 100 2026-05-01 10-Q 0000000001-26-000001",
		// Restated a year later as a comparative: not used.
		"Revenues 2026-01-01 2026-03-31 90 2027-05-01 10-Q 0000000001-27-000001",
		// An 8-K and an amendment are not read.
		"Revenues 2026-04-01 2026-06-30 50 2026-07-20 8-K 0000000001-26-000002",
		"Revenues 2026-04-01 2026-06-30 60 2026-08-01 10-Q/A 0000000001-26-000003",
	))
	if len(f.Quarters) != 1 || val(t, f.Quarters[0].Revenue) != 100 {
		t.Fatalf("%+v", f.Quarters)
	}
}

func TestAmbiguousAndPreference(t *testing.T) {
	f := extract(t, facts(
		"Revenues 2026-01-01 2026-03-31 100 2026-05-01 10-Q 0000000001-26-000001",
		"Revenues 2026-01-01 2026-03-31 101 2026-05-01 10-Q 0000000001-26-000002",
		"NetIncomeLoss 2026-01-01 2026-03-31 7 2026-05-01 10-Q 0000000001-26-000001",
		// Contract revenue and Revenues in one filing, equal: Revenues is preferred.
		"RevenueFromContractWithCustomerExcludingAssessedTax 2026-04-01 2026-06-30 95 2026-08-01 10-Q 0000000001-26-000004",
		"Revenues 2026-04-01 2026-06-30 95 2026-08-01 10-Q 0000000001-26-000004",
	))
	if f.Quarters[0].Revenue.Val != nil || f.Quarters[0].Revenue.Why != "ambiguous" || val(t, f.Quarters[0].NetIncome) != 7 {
		t.Errorf("%+v", f.Quarters[0])
	}
	if val(t, f.Quarters[1].Revenue) != 95 || f.Quarters[1].Revenue.C != "rev" {
		t.Errorf("%+v", f.Quarters[1].Revenue)
	}
}

func TestConflictingTotals(t *testing.T) {
	// Two revenue concepts more than 1% apart in one filing: which is the total
	// cannot be told, so neither is shown (owner's decision 2026-10-18).
	f := extract(t, facts(
		"RevenueFromContractWithCustomerExcludingAssessedTax 2026-04-01 2026-06-30 80 2026-08-01 10-Q 0000000001-26-000004",
		"Revenues 2026-04-01 2026-06-30 95 2026-08-01 10-Q 0000000001-26-000004",
		"NetIncomeLoss 2026-04-01 2026-06-30 9 2026-08-01 10-Q 0000000001-26-000004",
	))
	if r := f.Quarters[0].Revenue; r.Val != nil || r.Why != "conflict" || val(t, f.Quarters[0].NetIncome) != 9 {
		t.Errorf("%+v", f.Quarters[0])
	}
	// Within 1%: the same figure, shown.
	f = extract(t, facts(
		"RevenueFromContractWithCustomerExcludingAssessedTax 2026-04-01 2026-06-30 99.5 2026-08-01 10-Q 0000000001-26-000004",
		"Revenues 2026-04-01 2026-06-30 100 2026-08-01 10-Q 0000000001-26-000004",
	))
	if val(t, f.Quarters[0].Revenue) != 100 {
		t.Error("near-equal concepts")
	}
}

func TestFourthQuarter(t *testing.T) {
	base := []string{
		"Revenues 2025-01-01 2025-09-30 300 2025-11-01 10-Q 0000000001-25-000003",
		"Revenues 2024-01-01 2024-09-30 270 2025-11-01 10-Q 0000000001-25-000003",
		"Revenues 2025-01-01 2025-12-31 420 2026-02-20 10-K 0000000001-26-000001",
		"Revenues 2024-01-01 2024-12-31 360 2026-02-20 10-K 0000000001-26-000001",
		"NetIncomeLoss 2025-01-01 2025-09-30 30 2025-11-01 10-Q 0000000001-25-000003",
		"NetIncomeLoss 2025-01-01 2025-12-31 20 2026-02-20 10-K 0000000001-26-000001",
	}
	f := extract(t, facts(base...))
	q := period(t, f.Quarters, "2025-12-31")
	if q.Start != "2025-10-01" || val(t, q.Revenue) != 120 || *q.Revenue.Prev != 90 || q.Revenue.Derived == nil || q.Revenue.Derived.Nine.Filed != "2025-11-01" {
		t.Errorf("%+v", q.Revenue)
	}
	// A loss in Q4: net income may go negative, revenue may not.
	if val(t, q.NetIncome) != -10 {
		t.Errorf("net %v", val(t, q.NetIncome))
	}
	neg := append([]string{}, base...)
	neg[2] = "Revenues 2025-01-01 2025-12-31 250 2026-02-20 10-K 0000000001-26-000001"
	if r := period(t, extract(t, facts(neg...)).Quarters, "2025-12-31").Revenue; r.Val != nil || r.Why != "check" {
		t.Errorf("negative Q4 revenue: %+v", r)
	}
	// Different concepts in the year and the nine months: no Q4 revenue.
	mixed := append([]string{}, base...)
	mixed[0] = "SalesRevenueNet 2025-01-01 2025-09-30 300 2025-11-01 10-Q 0000000001-25-000003"
	if r := period(t, extract(t, facts(mixed...)).Quarters, "2025-12-31").Revenue; r.Val != nil {
		t.Errorf("mixed concepts: %+v", r)
	}
	// Nine months first reported after the year (a later 10-Q's comparative): no Q4.
	late := append([]string{}, base[2:]...)
	late = append(late, "Revenues 2025-01-01 2025-09-30 300 2026-11-01 10-Q 0000000001-26-000009")
	if r := period(t, extract(t, facts(late...)).Quarters, "2025-12-31").Revenue; r.Val != nil {
		t.Errorf("late nine months: %+v", r)
	}
	// Nine months not starting with the year: no Q4.
	off := append([]string{}, base[2:4]...)
	off = append(off, "Revenues 2025-02-01 2025-10-31 300 2025-11-20 10-Q 0000000001-25-000004")
	if f := extract(t, facts(off...)); len(f.Quarters) != 0 {
		t.Errorf("offset nine months: %+v", f.Quarters)
	}
	// A filed fourth quarter wins over the derived one.
	direct := append(append([]string{}, base...), "Revenues 2025-10-01 2025-12-31 121 2026-02-20 10-K 0000000001-26-000001")
	if r := period(t, extract(t, facts(direct...)).Quarters, "2025-12-31").Revenue; val(t, r) != 121 || r.Derived != nil {
		t.Errorf("direct Q4: %+v", r)
	}
}

func TestNegativeRevenue(t *testing.T) {
	f := extract(t, facts(
		"Revenues 2026-04-01 2026-06-30 -5 2026-08-01 10-Q 0000000001-26-000001",
		"Revenues 2025-04-01 2025-06-30 -3 2026-08-01 10-Q 0000000001-26-000001",
		"NetIncomeLoss 2026-04-01 2026-06-30 -9 2026-08-01 10-Q 0000000001-26-000001",
		"Revenues 2026-07-01 2026-09-30 4 2026-10-01 10-Q 0000000001-26-000002",
		"Revenues 2025-07-01 2025-09-30 -2 2026-10-01 10-Q 0000000001-26-000002",
	))
	q := period(t, f.Quarters, "2026-06-30")
	if q.Revenue.Val != nil || q.Revenue.Why != "negative" || val(t, q.NetIncome) != -9 {
		t.Errorf("%+v", q)
	}
	// A negative year-earlier revenue is left out, the value stays.
	if r := period(t, f.Quarters, "2026-09-30").Revenue; val(t, r) != 4 || r.Prev != nil {
		t.Errorf("%+v", r)
	}
}

func TestYearOverTenTimesItsQuarters(t *testing.T) {
	lines := []string{
		// Quarters tagged with a partial line (a bank's fees), the year with the total.
		"Revenues 2025-01-01 2025-03-31 1 2025-05-01 10-Q 0000000001-25-000001",
		"Revenues 2025-04-01 2025-06-30 1 2025-08-01 10-Q 0000000001-25-000002",
		"Revenues 2025-07-01 2025-09-30 1 2025-11-01 10-Q 0000000001-25-000003",
		"Revenues 2025-01-01 2025-12-31 400 2026-02-20 10-K 0000000001-26-000001",
		"Revenues 2026-01-01 2026-03-31 100 2026-05-01 10-Q 0000000001-26-000002",
	}
	f := extract(t, facts(lines...))
	for _, p := range append(f.Quarters, f.Years...) {
		if p.End <= "2025-12-31" && p.Revenue.Val != nil {
			t.Errorf("%s shown: %v", p.End, *p.Revenue.Val)
		}
	}
	// The other periods stand (not a unit slip: the company keeps its revenue).
	if val(t, period(t, f.Quarters, "2026-03-31").Revenue) != 100 {
		t.Error("2026 Q1")
	}
	// Quarters of zero revenue are no evidence against a year.
	zero := append([]string{}, lines...)
	for i := 0; i < 3; i++ {
		zero[i] = strings.Replace(zero[i], " 1 20", " 0 20", 1)
	}
	if val(t, period(t, extract(t, facts(zero...)).Years, "2025-12-31").Revenue) != 400 {
		t.Error("zero quarters voided the year")
	}
}

func TestUnitSlipAgainstALaterFiling(t *testing.T) {
	lines := []string{
		// Q1 2025 tagged in thousands as dollars; the 2026 Q1 10-Q shows it right.
		"Revenues 2025-01-01 2025-03-31 9802000 2025-05-01 10-Q 0000000001-25-000001",
		"Revenues 2026-01-01 2026-03-31 12000 2026-05-01 10-Q 0000000001-26-000001",
		"Revenues 2025-01-01 2025-03-31 9802 2026-05-01 10-Q 0000000001-26-000001",
		"NetIncomeLoss 2026-01-01 2026-03-31 -5 2026-05-01 10-Q 0000000001-26-000001",
		"NetIncomeLoss 2025-01-01 2025-03-31 -3 2025-05-01 10-Q 0000000001-25-000001",
	}
	f := extract(t, facts(lines...))
	for _, p := range f.Quarters {
		if p.Revenue.Val != nil {
			t.Errorf("%s revenue shown after a unit slip: %v", p.End, *p.Revenue.Val)
		}
	}
	if val(t, period(t, f.Quarters, "2026-03-31").NetIncome) != -5 {
		t.Error("net income lost")
	}
	// Off by 5 times: a restatement or a partial line, only that period goes.
	lines[0] = "Revenues 2025-01-01 2025-03-31 49010 2025-05-01 10-Q 0000000001-25-000001"
	f = extract(t, facts(lines...))
	if r := period(t, f.Quarters, "2025-03-31").Revenue; r.Val != nil || r.Why != "check" {
		t.Errorf("Q1 2025 %+v", r)
	}
	if val(t, period(t, f.Quarters, "2026-03-31").Revenue) != 12000 {
		t.Error("2026 Q1")
	}
}

func TestFiftyThreeWeekYear(t *testing.T) {
	// 53-week fiscal year (371 days) with a 14-week fourth quarter (98 days).
	f := extract(t, facts(
		"Revenues 2025-09-28 2026-06-27 300 2026-08-01 10-Q 0000000001-26-000003",
		"Revenues 2025-09-28 2026-10-03 420 2026-10-10 10-K 0000000001-26-000004",
	))
	q := period(t, f.Quarters, "2026-10-03")
	if q.Start != "2026-06-28" || val(t, q.Revenue) != 120 {
		t.Errorf("%+v", q)
	}
	if len(f.Years) != 1 {
		t.Errorf("years %+v", f.Years)
	}
}

func TestCIKAsString(t *testing.T) {
	// Some companyfacts files give the CIK as a zero-padded string.
	doc := strings.Replace(facts("Revenues 2026-04-01 2026-06-30 5 2026-08-01 10-Q 0000000001-26-000001"), `"cik":42`, `"cik":"0000000042"`, 1)
	if f := extract(t, doc); f == nil || f.CIK != 42 {
		t.Fatalf("%+v", f)
	}
	for _, bad := range []string{`"cik":"42x"`, `"cik":0`, `"cik":"12345678901"`, `"cik":null`} {
		doc := strings.Replace(facts("Revenues 2026-04-01 2026-06-30 5 2026-08-01 10-Q 0000000001-26-000001"), `"cik":42`, bad, 1)
		if _, err := Extract(strings.NewReader(doc), "TEST", now); err == nil {
			t.Errorf("%s accepted", bad)
		}
	}
}

func TestDataAfterTheDocument(t *testing.T) {
	doc := facts("Revenues 2026-04-01 2026-06-30 5 2026-08-01 10-Q 0000000001-26-000001") + `{"cik":1}`
	if _, err := Extract(strings.NewReader(doc), "TEST", now); err == nil {
		t.Error("trailing data accepted")
	}
}

func TestNoQuartersIsAnEmptyList(t *testing.T) {
	f := extract(t, facts("Revenues 2025-07-01 2026-06-30 5 2026-08-01 10-K 0000000001-26-000001"))
	b, _ := json.Marshal(f)
	if !strings.Contains(string(b), `"quarters":[]`) {
		t.Fatalf("%s", b)
	}
}

func TestStoppedFiling(t *testing.T) {
	if f := extract(t, facts("Revenues 2024-01-01 2024-03-31 100 2024-05-01 10-Q 0000000001-24-000001")); f != nil {
		t.Errorf("stale company got a file: %+v", f)
	}
}

func TestTickers(t *testing.T) {
	doc := `{"fields":["cik","name","ticker","exchange"],"data":[
		[3,"Alphabet Inc.","GOOGL","Nasdaq"],[4,"BERKSHIRE","BRK-B","NYSE"],[5,"Brighthouse","BHF","Nasdaq"],
		[5,"Brighthouse","BHFAN","Nasdaq"],[5,"Brighthouse","BHFAP","Nasdaq"],[3,"Alphabet Inc.","GOOG","Nasdaq"],
		[4,"BERKSHIRE","BRK-A","NYSE"],[6,"Dup One","DUP","NYSE"],[7,"Dup Two","DUP","NYSE"],
		[8,"SPAC Corp","SPACW","Nasdaq"],[9,"Rights Co","RGHT-R","NYSE"],
		[10,"SPDR GOLD TRUST","GLD","NYSE"],[11,"ProShares Trust II","UVXY","NYSE"],[12,"United States Oil Fund, LP","USO","NYSE"],
		[13,"iShares Bitcoin Trust ETF","IBIT","Nasdaq"],[14,"American Bitcoin Corp.","ABTC","Nasdaq"],
		[15,"U S PHYSICAL THERAPY INC /NV","USPH","NYSE"],[16,"Redwood Trust Inc","RWT","NYSE"],
		[17,"Old Co","GOOG","NYSE"],[18,"Beat Co","BEATW","Nasdaq"],[18,"Beat Co","BEAT","Nasdaq"],
		[19,"Franklin Templeton Digital Holdings Trust","EZBC","Cboe"],[20,"VS Trust","UVIX","Cboe"],
		[21,"Invesco CurrencyShares Euro Trust","FXE","NYSE"],
		[22,"COTWO ADVISORS PHYSICAL EUROPEAN CARBON ALLOWANCE TRUST","CTWO","NYSE"]]}`
	reviewed := []Class{{"BRK.A", 4}, {"GOOG", 3}}
	m, err := Tickers(strings.NewReader(doc), reviewed)
	if err != nil {
		t.Fatal(err)
	}
	// GOOG is reviewed for CIK 3, but also listed by another company: dropped as ambiguous.
	// BEAT: the first ticker that is not a warrant, though SEC lists BEATW first.
	want := map[string]int64{"GOOGL": 3, "BRK.B": 4, "BRK.A": 4, "BHF": 5, "ABTC": 14, "USPH": 15, "RWT": 16, "BEAT": 18}
	if len(m) != len(want) {
		t.Errorf("got %v", m)
	}
	for s, c := range want {
		if m[s] != c {
			t.Errorf("%s: %d, want %d (all: %v)", s, m[s], c, m)
		}
	}
	// A reviewed class bound to another CIK does not pass.
	if m, _ := Tickers(strings.NewReader(doc), []Class{{"BRK.A", 99}}); m["BRK.A"] != 0 {
		t.Error("reviewed class under the wrong CIK kept")
	}
	if _, err := Tickers(strings.NewReader(`{"fields":["cik","ticker"],"data":[]}`), nil); err == nil {
		t.Error("unexpected fields accepted")
	}
	if _, err := Tickers(strings.NewReader(doc), []Class{{"GOOG", 3}, {"GOOG", 3}}); err == nil {
		t.Error("duplicate reviewed entry accepted")
	}
}

func TestYearsOnlyFromAnnualReports(t *testing.T) {
	f := extract(t, facts(
		// A 10-Q's trailing twelve months: not a fiscal year.
		"Revenues 2025-07-01 2026-06-30 400 2026-08-01 10-Q 0000000001-26-000001",
		"Revenues 2026-04-01 2026-06-30 100 2026-08-01 10-Q 0000000001-26-000001",
		"Revenues 2025-01-01 2025-12-31 380 2026-02-20 10-K 0000000001-26-000002",
	))
	if len(f.Years) != 1 || f.Years[0].End != "2025-12-31" {
		t.Errorf("years %+v", f.Years)
	}
}

func TestYearSmallerThanAQuarter(t *testing.T) {
	// The 10-K tags one line as Revenues: smaller than a quarter of the same year.
	f := extract(t, facts(
		"Revenues 2025-01-01 2025-03-31 200 2025-05-01 10-Q 0000000001-25-000001",
		"Revenues 2025-01-01 2025-09-30 600 2025-11-01 10-Q 0000000001-25-000003",
		"Revenues 2025-01-01 2025-12-31 9 2026-02-20 10-K 0000000001-26-000001",
		"NetIncomeLoss 2025-01-01 2025-12-31 50 2026-02-20 10-K 0000000001-26-000001",
	))
	y := period(t, f.Years, "2025-12-31")
	if y.Revenue.Val != nil || y.Revenue.Why != "check" || val(t, y.NetIncome) != 50 {
		t.Errorf("year %+v", y)
	}
	// Its derived fourth quarter would be negative anyway; the first quarter stays.
	if q := period(t, f.Quarters, "2025-03-31"); val(t, q.Revenue) != 200 {
		t.Errorf("Q1 %+v", q)
	}
	for _, q := range f.Quarters {
		if q.End == "2025-12-31" && q.Revenue.Val != nil {
			t.Errorf("Q4 %+v", q)
		}
	}
}

func TestValidateRejects(t *testing.T) {
	good := func() *File { return extractFile(t, "NET") }
	for name, spoil := range map[string]func(f *File){
		"source":         func(f *File) { f.Source = "IEX" },
		"symbol":         func(f *File) { f.Symbol = "net" },
		"cik":            func(f *File) { f.CIK = 0 },
		"generated":      func(f *File) { f.GeneratedAt = "yesterday" },
		"order":          func(f *File) { f.Quarters[0], f.Quarters[1] = f.Quarters[1], f.Quarters[0] },
		"length":         func(f *File) { f.Quarters[0].Start = "2023-01-01" },
		"date":           func(f *File) { f.Quarters[0].End = "2023-02-30" },
		"negative":       func(f *File) { v := -1.0; f.Quarters[0].Revenue.Val = &v },
		"concept":        func(f *File) { f.Quarters[0].Revenue.C = "ni" },
		"form":           func(f *File) { f.Quarters[0].Revenue.Form = "8-K" },
		"accn":           func(f *File) { f.Quarters[0].Revenue.Accn = "x" },
		"future":         func(f *File) { f.Quarters[0].Revenue.Filed = "2027-01-01" },
		"null+fields":    func(f *File) { f.Quarters[0].Revenue = Value{Why: "none", C: "rev"} },
		"reason":         func(f *File) { f.Quarters[0].Revenue = Value{Why: "because"} },
		"too many":       func(f *File) { f.Years = append(f.Years, f.Years...) },
		"year from 10-Q": func(f *File) { f.Years[0].Revenue.Form = "10-Q" },
		"derived year": func(f *File) {
			r := &f.Years[0].Revenue
			r.Derived = &Derived{Year: Source{r.Accn, r.Filed}, Nine: Source{r.Accn, r.Filed}}
		},
		"derived from 10-Q": func(f *File) {
			for i := range f.Quarters {
				if r := &f.Quarters[i].Revenue; r.Derived != nil {
					r.Form = "10-Q"
					return
				}
			}
			panic("no derived quarter")
		},
		"ends after filing": func(f *File) { f.Quarters[0].Revenue.Filed = f.Quarters[0].Start },
		"nine after year": func(f *File) {
			for i := range f.Quarters {
				if r := &f.Quarters[i].Revenue; r.Derived != nil {
					r.Derived.Nine.Filed = "2026-10-01"
					return
				}
			}
		},
	} {
		f := good()
		spoil(f)
		if err := Validate(f); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}
