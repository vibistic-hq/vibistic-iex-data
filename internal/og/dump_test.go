package og

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestDumpTexts writes every covered file's card text to OG_DUMP_OUT, for a
// one-off comparison with the app's engine over a whole built site
// (OG_DUMP_SITE). Skipped unless both are set.
func TestDumpTexts(t *testing.T) {
	site, out := os.Getenv("OG_DUMP_SITE"), os.Getenv("OG_DUMP_OUT")
	if site == "" || out == "" {
		t.Skip("set OG_DUMP_SITE and OG_DUMP_OUT")
	}
	now, err := time.Parse(time.RFC3339, os.Getenv("OG_DUMP_NOW"))
	if err != nil {
		t.Fatal(err)
	}
	paths, _ := filepath.Glob(filepath.Join(site, "v1", "s", "*.json"))
	texts := map[string]map[string]string{}
	for _, p := range paths {
		f := readFixture(t, p)
		if f.Coverage != "ok" {
			continue
		}
		c, err := NewCard(f, now)
		if err != nil {
			t.Fatal(err)
		}
		close := c.Close
		texts[f.Symbol] = map[string]string{
			"asOf": Date(c.AsOf), "close": CellPrice(&close), "change": Percent(c.Change), "low52": CellPrice(c.Low52), "high52": CellPrice(c.High52),
			"sma50": CellPrice(c.SMA50), "sma200": CellPrice(c.SMA200), "vs50": Percent(c.Vs50), "vs200": Percent(c.Vs200),
		}
	}
	b, _ := json.Marshal(texts)
	if err := os.WriteFile(out, b, 0o644); err != nil {
		t.Fatal(err)
	}
}
