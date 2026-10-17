package main

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vibistic-hq/vibistic-iex-data/internal/day"
	"github.com/vibistic-hq/vibistic-iex-data/internal/og"
	"github.com/vibistic-hq/vibistic-iex-data/internal/series"
	"github.com/vibistic-hq/vibistic-iex-data/internal/tops"
)

// writeDay writes a day file whose session runs 09:30 to 16:00 New York time.
func writeDay(t *testing.T, dir, date string, syms ...tops.Summary) {
	t.Helper()
	writeSession(t, dir, date, 9, 30, syms...)
}

func writeSession(t *testing.T, dir, date string, startHour, startMinute int, syms ...tops.Summary) {
	t.Helper()
	ny, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	d, err := time.ParseInLocation("2006-01-02", date, ny)
	if err != nil {
		t.Fatal(err)
	}
	start := d.Add(time.Duration(startHour)*time.Hour + time.Duration(startMinute)*time.Minute).UnixNano()
	end := d.Add(16 * time.Hour).UnixNano()
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	if err := day.Write(zw, date, &tops.Day{RegularStart: start, RegularEnd: end, Symbols: syms}); err != nil {
		t.Fatal(err)
	}
	zw.Close()
	if err := os.WriteFile(filepath.Join(dir, strings.ReplaceAll(date, "-", "")+".jsonl.gz"), buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestRunBuildsTheSite(t *testing.T) {
	tmp := t.TempDir()
	days := filepath.Join(tmp, "days")
	os.Mkdir(days, 0o755)
	aapl := tops.Summary{Symbol: "AAPL", Open: 100_0000, High: 101_0000, Low: 99_0000, Close: 100_5000, Volume: 10_000, Notional: 1_005_000, Trades: 10, LotTrades: 10}
	// Odd lots only: no IEX price, so a sparse file and no preview image.
	odd := tops.Summary{Symbol: "ODD", Volume: 50, Notional: 500, Trades: 5}
	writeDay(t, days, "2026-10-08", aapl, odd)
	writeDay(t, days, "2026-10-09", aapl, odd)
	reviewed := filepath.Join(tmp, "reviewed.json")
	os.WriteFile(reviewed, []byte("[]"), 0o644)
	out := filepath.Join(tmp, "site")
	if err := run(days, reviewed, out, time.Date(2026, 10, 10, 7, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	var f series.File
	b, err := os.ReadFile(filepath.Join(out, "v1", "s", "AAPL.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, &f); err != nil {
		t.Fatal(err)
	}
	if f.AsOf != "2026-10-09" || f.Coverage != "ok" || len(f.Bars) != 2 || f.Bars[1] != [7]float64{20261009, 100, 101, 99, 100.5, 10000, 100.5} {
		t.Fatalf("file = %+v", f)
	}
	m, _ := os.ReadFile(filepath.Join(out, "v1", "manifest.json"))
	if !strings.Contains(string(m), `"asOf":"2026-10-09"`) || !strings.Contains(string(m), `"covered":1`) {
		t.Fatalf("manifest = %s", m)
	}
	idx, _ := os.ReadFile(filepath.Join(out, "index.html"))
	if !strings.Contains(string(idx), series.Citation) || !strings.Contains(string(idx), series.TermsURL) {
		t.Fatal("index page lacks the citation")
	}
	// The preview image of the covered symbol only (vibistic-stocks share links).
	pngBytes, err := os.ReadFile(filepath.Join(out, "v1", "og", "AAPL.png"))
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := png.DecodeConfig(bytes.NewReader(pngBytes))
	if err != nil || cfg.Width != og.Width || cfg.Height != og.Height {
		t.Fatalf("AAPL.png: %v %+v", err, cfg)
	}
	if _, err := os.Stat(filepath.Join(out, "v1", "og", "ODD.png")); !os.IsNotExist(err) {
		t.Fatalf("a sparse symbol got an image: %v", err)
	}
	if _, err := os.Stat(filepath.Join(out, "v1", "s", "ODD.json")); err != nil {
		t.Fatal(err)
	}
	if err := run(days, reviewed, out, time.Now()); err == nil {
		t.Fatal("must refuse to overwrite an existing site")
	}
}

func TestRunSkipsDaysThatMissPartOfTheSession(t *testing.T) {
	tmp := t.TempDir()
	days := filepath.Join(tmp, "days")
	os.Mkdir(days, 0o755)
	aapl := tops.Summary{Symbol: "AAPL", Open: 100_0000, High: 101_0000, Low: 99_0000, Close: 100_5000, Volume: 10_000, Notional: 1_005_000, Trades: 10, LotTrades: 10}
	partial := aapl
	partial.Close = 90_0000
	writeDay(t, days, "2024-12-20", aapl)
	writeSession(t, days, "2024-12-23", 11, 50, partial) // as IEX's real 2024-12-23 file
	writeDay(t, days, "2024-12-26", aapl)
	reviewed := filepath.Join(tmp, "reviewed.json")
	os.WriteFile(reviewed, []byte("[]"), 0o644)
	out := filepath.Join(tmp, "site")
	if err := run(days, reviewed, out, time.Date(2024, 12, 27, 7, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	var f series.File
	b, err := os.ReadFile(filepath.Join(out, "v1", "s", "AAPL.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, &f); err != nil {
		t.Fatal(err)
	}
	if len(f.Bars) != 2 || f.Bars[0][0] != 20241220 || f.Bars[1][0] != 20241226 {
		t.Fatalf("bars = %v, want 2024-12-20 and 2024-12-26 only", f.Bars)
	}
	m, _ := os.ReadFile(filepath.Join(out, "v1", "manifest.json"))
	if !strings.Contains(string(m), `"sessions":2`) {
		t.Fatalf("manifest = %s", m)
	}
}

func TestRunFailsWhenEveryDayIsIncomplete(t *testing.T) {
	tmp := t.TempDir()
	writeSession(t, tmp, "2024-12-23", 11, 50)
	reviewed := filepath.Join(tmp, "reviewed.json")
	os.WriteFile(reviewed, []byte("[]"), 0o644)
	if err := run(tmp, reviewed, filepath.Join(tmp, "site"), time.Now()); err == nil {
		t.Fatal("want an error with no complete days")
	}
}

func TestRunRejectsMisnamedDayFiles(t *testing.T) {
	tmp := t.TempDir()
	writeDay(t, tmp, "2026-10-09")
	os.Rename(filepath.Join(tmp, "20261009.jsonl.gz"), filepath.Join(tmp, "20261008.jsonl.gz"))
	reviewed := filepath.Join(tmp, "reviewed.json")
	os.WriteFile(reviewed, []byte("[]"), 0o644)
	if err := run(tmp, reviewed, filepath.Join(tmp, "site"), time.Now()); err == nil {
		t.Fatal("want an error")
	}
}

func TestReadReviewsRejectsUnknownFields(t *testing.T) {
	p := filepath.Join(t.TempDir(), "r.json")
	os.WriteFile(p, []byte(`[{"symbol":"AAPL","date":"2026-01-05","kind":"real","note":"x","extra":1}]`), 0o644)
	if _, err := readReviews(p); err == nil {
		t.Fatal("want an error")
	}
}
