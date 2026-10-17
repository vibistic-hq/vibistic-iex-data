package og

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vibistic-hq/vibistic-iex-data/internal/series"
)

// golden.json is written by vibistic-stocks (scripts/og-golden.mts) with the
// app's engine from the same fixtures; copied here unchanged.
type goldenCard struct {
	Symbol    string            `json:"symbol"`
	AsOf      string            `json:"asOf"`
	Close     float64           `json:"close"`
	PrevClose *float64          `json:"prevClose"`
	Change    *float64          `json:"change"`
	Low52     *float64          `json:"low52"`
	High52    *float64          `json:"high52"`
	SMA50     *float64          `json:"sma50"`
	SMA200    *float64          `json:"sma200"`
	Vs50      *float64          `json:"vs50"`
	Vs200     *float64          `json:"vs200"`
	Text      map[string]string `json:"text"`
}

func readGolden(t *testing.T) (time.Time, map[string]*goldenCard) {
	t.Helper()
	b, err := os.ReadFile("testdata/golden.json")
	if err != nil {
		t.Fatal(err)
	}
	var g struct {
		Now   string                 `json:"now"`
		Cards map[string]*goldenCard `json:"cards"`
	}
	if err := json.Unmarshal(b, &g); err != nil {
		t.Fatal(err)
	}
	now, err := time.Parse(time.RFC3339, g.Now)
	if err != nil {
		t.Fatal(err)
	}
	return now, g.Cards
}

func readFixture(t *testing.T, path string) *series.File {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var f series.File
	if err := json.Unmarshal(b, &f); err != nil {
		t.Fatal(err)
	}
	return &f
}

func same(a, b *float64) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

func TestCardsMatchTheAppsEngine(t *testing.T) {
	now, golden := readGolden(t)
	paths, _ := filepath.Glob("testdata/*.json")
	n := 0
	for _, p := range paths {
		if filepath.Base(p) == "golden.json" {
			continue
		}
		n++
		f := readFixture(t, p)
		want, ok := golden[f.Symbol]
		if !ok {
			t.Fatalf("%s: not in golden.json", f.Symbol)
		}
		c, err := NewCard(f, now)
		if want == nil {
			// The app refuses this file: no card, so no image.
			if err == nil {
				t.Errorf("%s: the app refuses this file but NewCard made a card", f.Symbol)
			}
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		if c.Symbol != want.Symbol || c.AsOf != want.AsOf || c.Close != want.Close {
			t.Errorf("%s: symbol/asOf/close %s %s %v, want %s %s %v", f.Symbol, c.Symbol, c.AsOf, c.Close, want.Symbol, want.AsOf, want.Close)
		}
		for name, pair := range map[string][2]*float64{
			"prevClose": {c.PrevClose, want.PrevClose}, "change": {c.Change, want.Change}, "low52": {c.Low52, want.Low52}, "high52": {c.High52, want.High52},
			"sma50": {c.SMA50, want.SMA50}, "sma200": {c.SMA200, want.SMA200}, "vs50": {c.Vs50, want.Vs50}, "vs200": {c.Vs200, want.Vs200},
		} {
			if !same(pair[0], pair[1]) {
				t.Errorf("%s %s: %v, want %v", f.Symbol, name, deref(pair[0]), deref(pair[1]))
			}
		}
		close := c.Close
		got := map[string]string{
			"asOf": Date(c.AsOf), "close": CellPrice(&close), "change": Percent(c.Change), "low52": CellPrice(c.Low52), "high52": CellPrice(c.High52),
			"sma50": CellPrice(c.SMA50), "sma200": CellPrice(c.SMA200), "vs50": Percent(c.Vs50), "vs200": Percent(c.Vs200),
		}
		for k, v := range want.Text {
			if got[k] != v {
				t.Errorf("%s text %s: %q, want %q", f.Symbol, k, got[k], v)
			}
		}
	}
	if n != len(golden) || n < 8 {
		t.Fatalf("%d fixtures, %d golden cards", n, len(golden))
	}
}

func deref(v *float64) any {
	if v == nil {
		return nil
	}
	return *v
}

func TestFormatting(t *testing.T) {
	f := func(x float64) *float64 { return &x }
	for _, c := range []struct {
		got, want string
	}{
		{CellPrice(f(10.125)), "10.13"}, // an exact half rounds away from zero
		{CellPrice(f(2.675)), "2.68"},   // stored as 2.67499999..., shown as JavaScript shows it
		{CellPrice(f(164.285)), "164.29"},
		{CellPrice(f(1.005)), "1.01"},
		{CellPrice(f(8.345)), "8.35"},
		{CellPrice(f(1.0005)), "1.00"},
		{CellPrice(f(0.12345)), "0.1235"},
		{CellPrice(f(999.995)), "1,000.00"},
		{CellPrice(f(1234.5)), "1,235"},
		{CellPrice(f(1234567.4)), "1,234,567"},
		{CellPrice(f(0.99999)), "1.0000"},
		{CellPrice(nil), "-"},
		{Percent(f(-0.26)), "−0.3%"},
		{Percent(f(0.25)), "+0.3%"},  // Math.round(2.5) = 3
		{Percent(f(-0.25)), "−0.2%"}, // Math.round(-2.5) = -2
		{Percent(f(-0.04)), "0.0%"},
		{Percent(f(16.04)), "+16.0%"},
		{Percent(nil), "-"},
		{Date("2026-10-06"), "Oct 6, 2026"},
	} {
		if c.got != c.want {
			t.Errorf("got %q, want %q", c.got, c.want)
		}
	}
}

func TestChartIsTheLastSixMonthsWithTheSMAAsItStood(t *testing.T) {
	now, _ := readGolden(t)
	f := readFixture(t, "testdata/LONG.json")
	c, err := NewCard(f, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Chart) < 120 || len(c.Chart) > 135 {
		t.Fatalf("%d chart points", len(c.Chart))
	}
	if c.Chart[0].Date < addDays(c.AsOf, -183) || c.Chart[len(c.Chart)-1].Date != c.AsOf {
		t.Fatalf("chart runs %s to %s", c.Chart[0].Date, c.Chart[len(c.Chart)-1].Date)
	}
	if !same(c.Chart[len(c.Chart)-1].SMA50, c.SMA50) {
		t.Fatal("the chart's last 50 SMA is not the card's")
	}
	short := readFixture(t, "testdata/SHORT.json")
	cs, _ := NewCard(short, now)
	if cs.Chart[0].SMA50 != nil || cs.Chart[len(cs.Chart)-1].SMA50 == nil {
		t.Fatal("the 50 SMA starts once 50 closes exist")
	}
}

func TestNoAlpacaInFixtures(t *testing.T) {
	paths, _ := filepath.Glob("testdata/*")
	for _, p := range paths {
		b, _ := os.ReadFile(p)
		if strings.Contains(strings.ToLower(string(b)), "alpaca") {
			t.Errorf("%s mentions Alpaca", p)
		}
	}
}
