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
	"github.com/vibistic-hq/vibistic-iex-data/internal/earnings"
	"github.com/vibistic-hq/vibistic-iex-data/internal/og"
	"github.com/vibistic-hq/vibistic-iex-data/internal/sec"
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
	if err := run(days, reviewed, "", "", out, time.Date(2026, 10, 10, 7, 0, 0, 0, time.UTC)); err != nil {
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
	if err := run(days, reviewed, "", "", out, time.Now()); err == nil {
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
	if err := run(days, reviewed, "", "", out, time.Date(2024, 12, 27, 7, 0, 0, 0, time.UTC)); err != nil {
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
	if err := run(tmp, reviewed, "", "", filepath.Join(tmp, "site"), time.Now()); err == nil {
		t.Fatal("want an error with no complete days")
	}
}

func TestRunRejectsMisnamedDayFiles(t *testing.T) {
	tmp := t.TempDir()
	writeDay(t, tmp, "2026-10-09")
	os.Rename(filepath.Join(tmp, "20261009.jsonl.gz"), filepath.Join(tmp, "20261008.jsonl.gz"))
	reviewed := filepath.Join(tmp, "reviewed.json")
	os.WriteFile(reviewed, []byte("[]"), 0o644)
	if err := run(tmp, reviewed, "", "", filepath.Join(tmp, "site"), time.Now()); err == nil {
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

// secSnapshot writes a secbuild-shaped snapshot of one file to dir.
func secSnapshot(t *testing.T, dir, symbol, generated string) {
	t.Helper()
	os.MkdirAll(dir, 0o755)
	val := 100.0
	f := sec.File{V: 1, Source: "SEC", Symbol: symbol, CIK: 320193, GeneratedAt: generated, Quarters: []sec.Period{{
		Start: "2026-03-29", End: "2026-06-27",
		Revenue:   sec.Value{Val: &val, C: "rfc", Form: "10-Q", Filed: "2026-07-31", Accn: "0000320193-26-000020"},
		NetIncome: sec.Value{Why: "none"},
	}}}
	b, _ := json.Marshal(f)
	os.WriteFile(filepath.Join(dir, symbol+".json"), b, 0o644)
	s, _ := json.Marshal(map[string]any{"v": 1, "source": "SEC", "generatedAt": generated, "files": 1})
	os.WriteFile(filepath.Join(dir, "snapshot.json"), s, 0o644)
}

func TestRunPublishesTheSECSnapshotOrLeavesItOut(t *testing.T) {
	when := time.Date(2026, 10, 10, 7, 0, 0, 0, time.UTC)
	build := func(t *testing.T, snapshot func(dir string)) string {
		tmp := t.TempDir()
		days := filepath.Join(tmp, "days")
		os.Mkdir(days, 0o755)
		aapl := tops.Summary{Symbol: "AAPL", Open: 100_0000, High: 101_0000, Low: 99_0000, Close: 100_5000, Volume: 10_000, Notional: 1_005_000, Trades: 10, LotTrades: 10}
		writeDay(t, days, "2026-10-09", aapl)
		reviewed := filepath.Join(tmp, "reviewed.json")
		os.WriteFile(reviewed, []byte("[]"), 0o644)
		secDir := filepath.Join(tmp, "sec")
		snapshot(secDir)
		out := filepath.Join(tmp, "site")
		if err := run(days, reviewed, secDir, "", out, when); err != nil {
			t.Fatal(err)
		}
		// The IEX files are there whatever happened to the SEC snapshot.
		if _, err := os.Stat(filepath.Join(out, "v1", "s", "AAPL.json")); err != nil {
			t.Fatal(err)
		}
		return out
	}
	t.Run("good", func(t *testing.T) {
		out := build(t, func(dir string) { secSnapshot(t, dir, "AAPL", "2026-10-09T09:30:00Z") })
		if _, err := os.Stat(filepath.Join(out, "v1", "earnings.json")); !os.IsNotExist(err) {
			t.Fatal("no -earnings, no earnings file")
		}
		if _, err := os.Stat(filepath.Join(out, "v1", "sec", "AAPL.json")); err != nil {
			t.Fatal(err)
		}
		m, _ := os.ReadFile(filepath.Join(out, "v1", "manifest.json"))
		if !strings.Contains(string(m), `"sec":{"files":1,"generatedAt":"2026-10-09T09:30:00Z","source":"SEC"}`) {
			t.Fatalf("manifest = %s", m)
		}
	})
	for name, snapshot := range map[string]func(dir string){
		"stale":   func(dir string) { secSnapshot(t, dir, "AAPL", "2026-09-20T09:30:00Z") },
		"missing": func(dir string) {},
		// A file under another symbol's name.
		"renamed": func(dir string) {
			secSnapshot(t, dir, "AAPL", "2026-10-09T09:30:00Z")
			os.Rename(filepath.Join(dir, "AAPL.json"), filepath.Join(dir, "MSFT.json"))
		},
		"invalid": func(dir string) {
			secSnapshot(t, dir, "AAPL", "2026-10-09T09:30:00Z")
			b, _ := os.ReadFile(filepath.Join(dir, "AAPL.json"))
			os.WriteFile(filepath.Join(dir, "AAPL.json"), []byte(strings.Replace(string(b), `"10-Q"`, `"8-K"`, 1)), 0o644)
		},
		"trailing data": func(dir string) {
			secSnapshot(t, dir, "AAPL", "2026-10-09T09:30:00Z")
			f, _ := os.OpenFile(filepath.Join(dir, "AAPL.json"), os.O_APPEND|os.O_WRONLY, 0)
			f.WriteString(`{"v":1}`)
			f.Close()
		},
		"too big": func(dir string) {
			secSnapshot(t, dir, "AAPL", "2026-10-09T09:30:00Z")
			b, _ := os.ReadFile(filepath.Join(dir, "AAPL.json"))
			os.WriteFile(filepath.Join(dir, "AAPL.json"), append(b, []byte(strings.Repeat(" ", 300<<10))...), 0o644)
		},
		"extra field": func(dir string) {
			secSnapshot(t, dir, "AAPL", "2026-10-09T09:30:00Z")
			b, _ := os.ReadFile(filepath.Join(dir, "AAPL.json"))
			os.WriteFile(filepath.Join(dir, "AAPL.json"), []byte(strings.Replace(string(b), `{"v":1`, `{"name":"<b>x</b>","v":1`, 1)), 0o644)
		},
	} {
		t.Run(name, func(t *testing.T) {
			out := build(t, snapshot)
			if _, err := os.Stat(filepath.Join(out, "v1", "sec")); !os.IsNotExist(err) {
				t.Fatalf("v1/sec published: %v", err)
			}
			m, _ := os.ReadFile(filepath.Join(out, "v1", "manifest.json"))
			if strings.Contains(string(m), `"sec"`) {
				t.Fatalf("manifest = %s", m)
			}
		})
	}
}

// earningsFile writes an earningsbuild-shaped file with n symbols.
func earningsFile(t *testing.T, path, generated string, n int) {
	t.Helper()
	f := earnings.File{V: 1, Source: "SEC", GeneratedAt: generated, Today: "2026-10-09", AsOf: "2026-10-08", Symbols: map[string]earnings.Entry{}}
	for i := 0; i < n; i++ {
		sym := string(rune('A'+i/676%26)) + string(rune('A'+i/26%26)) + string(rune('A'+i%26))
		f.Symbols[sym] = earnings.Entry{CIK: int64(1000 + i), Next: "2026-10-29", Last: "2026-07-30", Basis: "2025-10-30"}
	}
	b, _ := json.Marshal(f)
	os.WriteFile(path, b, 0o644)
}

func TestRunPublishesTheEarningsFileOrLeavesItOut(t *testing.T) {
	when := time.Date(2026, 10, 10, 7, 0, 0, 0, time.UTC)
	build := func(t *testing.T, write func(path string)) string {
		tmp := t.TempDir()
		days := filepath.Join(tmp, "days")
		os.Mkdir(days, 0o755)
		aapl := tops.Summary{Symbol: "AAPL", Open: 100_0000, High: 101_0000, Low: 99_0000, Close: 100_5000, Volume: 10_000, Notional: 1_005_000, Trades: 10, LotTrades: 10}
		writeDay(t, days, "2026-10-09", aapl)
		reviewed := filepath.Join(tmp, "reviewed.json")
		os.WriteFile(reviewed, []byte("[]"), 0o644)
		secDir := filepath.Join(tmp, "sec")
		secSnapshot(t, secDir, "AAPL", "2026-10-09T09:30:00Z")
		path := filepath.Join(tmp, "earnings.json")
		write(path)
		out := filepath.Join(tmp, "site")
		if err := run(days, reviewed, secDir, path, out, when); err != nil {
			t.Fatal(err)
		}
		// The IEX and SEC files are there whatever happened to the estimates.
		for _, p := range []string{"s/AAPL.json", "sec/AAPL.json"} {
			if _, err := os.Stat(filepath.Join(out, "v1", p)); err != nil {
				t.Fatal(err)
			}
		}
		return out
	}
	t.Run("good", func(t *testing.T) {
		out := build(t, func(p string) { earningsFile(t, p, "2026-10-09T09:30:00Z", earnings.MinSymbols) })
		var f earnings.File
		b, err := os.ReadFile(filepath.Join(out, "v1", "earnings.json"))
		if err != nil || json.Unmarshal(b, &f) != nil || len(f.Symbols) != earnings.MinSymbols {
			t.Fatalf("%v %d", err, len(f.Symbols))
		}
		m, _ := os.ReadFile(filepath.Join(out, "v1", "manifest.json"))
		if !strings.Contains(string(m), `"earnings":{"asOf":"2026-10-08","generatedAt":"2026-10-09T09:30:00Z","source":"SEC","symbols":1500}`) {
			t.Fatalf("manifest = %s", m)
		}
	})
	for name, write := range map[string]func(path string){
		"missing":     func(p string) {},
		"stale":       func(p string) { earningsFile(t, p, "2026-09-20T09:30:00Z", earnings.MinSymbols) },
		"few symbols": func(p string) { earningsFile(t, p, "2026-10-09T09:30:00Z", earnings.MinSymbols-1) },
		"invalid": func(p string) {
			earningsFile(t, p, "2026-10-09T09:30:00Z", earnings.MinSymbols)
			b, _ := os.ReadFile(p)
			os.WriteFile(p, []byte(strings.Replace(string(b), `"next":"2026-10-29"`, `"next":"2026-10-30"`, 1)), 0o644)
		},
		"null reason list": func(p string) {
			earningsFile(t, p, "2026-10-09T09:30:00Z", earnings.MinSymbols)
			b, _ := os.ReadFile(p)
			os.WriteFile(p, []byte(strings.Replace(string(b), `{"v":1`, `{"noEstimate":{"few":null},"v":1`, 1)), 0o644)
		},
		"extra field": func(p string) {
			earningsFile(t, p, "2026-10-09T09:30:00Z", earnings.MinSymbols)
			b, _ := os.ReadFile(p)
			os.WriteFile(p, []byte(strings.Replace(string(b), `{"v":1`, `{"note":"<b>x</b>","v":1`, 1)), 0o644)
		},
		"too big": func(p string) {
			earningsFile(t, p, "2026-10-09T09:30:00Z", earnings.MinSymbols)
			b, _ := os.ReadFile(p)
			os.WriteFile(p, append(b, []byte(strings.Repeat(" ", 3<<20))...), 0o644)
		},
	} {
		t.Run(name, func(t *testing.T) {
			out := build(t, write)
			if _, err := os.Stat(filepath.Join(out, "v1", "earnings.json")); !os.IsNotExist(err) {
				t.Fatalf("v1/earnings.json published: %v", err)
			}
			m, _ := os.ReadFile(filepath.Join(out, "v1", "manifest.json"))
			if strings.Contains(string(m), `"earnings"`) || !strings.Contains(string(m), `"sec"`) {
				t.Fatalf("manifest = %s", m)
			}
		})
	}
}

// One symbol whose every session opens 1000x its previous close is detected as a
// 1-for-1000 reverse split each session. Compounded, its earliest bars left the
// app's range (+Inf at about 103 splits, 1e150 at 50), and +Inf made json.Marshal
// fail the whole build. That symbol is now sparse and every other one publishes
// (security audit run-1).
func TestRunServesASymbolWithRunawaySplitAdjustmentAsSparse(t *testing.T) {
	for _, sessions := range []int{50, 110} {
		tmp := t.TempDir()
		days := filepath.Join(tmp, "days")
		if err := os.Mkdir(days, 0o755); err != nil {
			t.Fatal(err)
		}
		aapl := tops.Summary{Symbol: "AAPL", Open: 100_0000, High: 101_0000, Low: 99_0000, Close: 100_5000, Volume: 10_000, Notional: 1_005_000, Trades: 10, LotTrades: 10}
		zzz := tops.Summary{Symbol: "ZZZ", Open: 1000_0000, High: 1000_0000, Low: 1_0000, Close: 1_0000, Volume: 1000, Notional: 1_000_000, Trades: 10, LotTrades: 10}
		d := time.Date(2026, 1, 5, 0, 0, 0, 0, time.UTC)
		for n := 0; n < sessions; d = d.AddDate(0, 0, 1) {
			if wd := d.Weekday(); wd != time.Saturday && wd != time.Sunday {
				writeDay(t, days, d.Format("2006-01-02"), aapl, zzz)
				n++
			}
		}
		reviewed := filepath.Join(tmp, "reviewed.json")
		if err := os.WriteFile(reviewed, []byte("[]"), 0o644); err != nil {
			t.Fatal(err)
		}
		out := filepath.Join(tmp, "site")
		if err := run(days, reviewed, "", "", out, d.Add(7*time.Hour)); err != nil {
			t.Fatalf("%d sessions: %v", sessions, err)
		}
		for _, name := range []string{"manifest.json", "s/AAPL.json"} {
			if _, err := os.Stat(filepath.Join(out, "v1", name)); err != nil {
				t.Fatalf("%d sessions: %s not published: %v", sessions, name, err)
			}
		}
		var z series.File
		b, err := os.ReadFile(filepath.Join(out, "v1", "s", "ZZZ.json"))
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(b, &z); err != nil {
			t.Fatal(err)
		}
		if z.Coverage != "sparse" || len(z.Bars) != 0 || len(z.Splits) != 0 {
			t.Fatalf("%d sessions: ZZZ coverage %s, %d bars, %d splits", sessions, z.Coverage, len(z.Bars), len(z.Splits))
		}
	}
}

// Day files from release assets: a symlink is refused, and so is one that unpacks
// past the size cap (security audit run-1).
func TestReadDayRefusesLinksAndOversizedFiles(t *testing.T) {
	dir := t.TempDir()
	aapl := tops.Summary{Symbol: "AAPL", Open: 100_0000, High: 101_0000, Low: 99_0000, Close: 100_5000, Volume: 10_000, Notional: 1_005_000, Trades: 10, LotTrades: 10}
	writeDay(t, dir, "2026-01-05", aapl)
	real := filepath.Join(dir, "20260105.jsonl.gz")
	if _, err := readDay(real); err != nil {
		t.Fatalf("a normal day file: %v", err)
	}
	links := t.TempDir()
	link := filepath.Join(links, "20260105.jsonl.gz")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	if _, err := readDay(link); err == nil || !strings.Contains(err.Error(), "not a regular file") {
		t.Fatalf("symlinked day file: %v", err)
	}
	defer func(n int64) { maxDayBytes = n }(maxDayBytes)
	maxDayBytes = 64
	if _, err := readDay(real); err == nil || !strings.Contains(err.Error(), "unpacks to more than") {
		t.Fatalf("oversized day file: %v", err)
	}
}
