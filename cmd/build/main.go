// Command build turns the archive of day files into the published site:
// v1/s/{SYMBOL}.json for every symbol, v1/og/{SYMBOL}.png (the preview image
// of Vibistic Stocks share links) for every covered symbol, v1/manifest.json
// and an index page.
//
//	build -days days/ -reviewed splits-reviewed.json -out site/
package main

import (
	"compress/gzip"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"html"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/vibistic-hq/vibistic-iex-data/internal/day"
	"github.com/vibistic-hq/vibistic-iex-data/internal/og"
	"github.com/vibistic-hq/vibistic-iex-data/internal/series"
)

func main() {
	daysDir := flag.String("days", "days", "directory of YYYYMMDD.jsonl.gz day files")
	reviewed := flag.String("reviewed", "splits-reviewed.json", "reviewed splits and breaks")
	out := flag.String("out", "site", "output directory (created; must not exist)")
	validate := flag.Bool("validate", false, "only check the reviewed file")
	flag.Parse()
	if *validate {
		if _, err := readReviews(*reviewed); err != nil {
			fmt.Fprintln(os.Stderr, "build:", err)
			os.Exit(1)
		}
		return
	}
	if err := run(*daysDir, *reviewed, *out, time.Now()); err != nil {
		fmt.Fprintln(os.Stderr, "build:", err)
		os.Exit(1)
	}
}

func run(daysDir, reviewedPath, out string, now time.Time) error {
	reviews, err := readReviews(reviewedPath)
	if err != nil {
		return err
	}
	paths, err := filepath.Glob(filepath.Join(daysDir, "*.jsonl.gz"))
	if err != nil {
		return err
	}
	sort.Strings(paths)
	var days []series.Day
	for _, p := range paths {
		d, err := readDay(p)
		if errors.Is(err, day.ErrIncomplete) {
			// Archived before iexagg checked the session (2024-12-23): left
			// out, so the day has no bars rather than bars from part of it.
			fmt.Fprintf(os.Stderr, "build: skipping %s: %v\n", p, err)
			continue
		}
		if err != nil {
			return fmt.Errorf("%s: %w", p, err)
		}
		days = append(days, d)
	}
	files, err := series.Build(days, reviews, now)
	if err != nil {
		return err
	}
	if _, err := os.Stat(out); err == nil {
		return fmt.Errorf("%s already exists", out)
	}
	if err := os.MkdirAll(filepath.Join(out, "v1", "s"), 0o755); err != nil {
		return err
	}
	ok := 0
	for sym, f := range files {
		if f.Coverage == "ok" {
			ok++
		}
		if err := writeJSON(filepath.Join(out, "v1", "s", sym+".json"), f); err != nil {
			return err
		}
	}
	if err := writeImages(filepath.Join(out, "v1", "og"), files, now); err != nil {
		return err
	}
	asOf := days[len(days)-1].Date
	manifest := map[string]any{"v": 1, "source": "IEX", "citation": series.Citation, "termsUrl": series.TermsURL, "asOf": asOf, "generatedAt": now.UTC().Format(time.RFC3339), "sessions": len(days), "symbols": len(files), "covered": ok}
	if err := writeJSON(filepath.Join(out, "v1", "manifest.json"), manifest); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(out, ".nojekyll"), nil, 0o644); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "build: %d sessions to %s, %d symbols (%d with enough IEX trading)\n", len(days), asOf, len(files), ok)
	return os.WriteFile(filepath.Join(out, "index.html"), []byte(indexPage(asOf, len(files), ok)), 0o644)
}

// writeImages draws every covered symbol's preview image, in parallel. Any
// failure, or an image over og.MaxBytes, fails the build (and so the
// publish, which keeps the last deployment).
func writeImages(dir string, files map[string]*series.File, now time.Time) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	syms := make(chan string)
	errs := make(chan error, 1)
	var wg sync.WaitGroup
	for range runtime.NumCPU() {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for sym := range syms {
				if err := writeImage(dir, files[sym], now); err != nil {
					select {
					case errs <- fmt.Errorf("%s: %w", sym, err):
					default:
					}
				}
			}
		}()
	}
	for sym, f := range files {
		if f.Coverage == "ok" {
			syms <- sym
		}
	}
	close(syms)
	wg.Wait()
	select {
	case err := <-errs:
		return err
	default:
		return nil
	}
}

func writeImage(dir string, f *series.File, now time.Time) error {
	card, err := og.NewCard(f, now)
	if err != nil {
		return err
	}
	b, err := og.Render(card)
	if err != nil {
		return err
	}
	if len(b) > og.MaxBytes {
		return fmt.Errorf("preview image is %d bytes (limit %d)", len(b), og.MaxBytes)
	}
	return os.WriteFile(filepath.Join(dir, f.Symbol+".png"), b, 0o644)
}

func readReviews(path string) ([]series.Review, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var rs []series.Review
	dec := json.NewDecoder(strings.NewReader(string(b)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&rs); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return rs, series.ValidateReviews(rs)
}

func readDay(path string) (series.Day, error) {
	f, err := os.Open(path)
	if err != nil {
		return series.Day{}, err
	}
	defer f.Close()
	zr, err := gzip.NewReader(f)
	if err != nil {
		return series.Day{}, err
	}
	h, lines, err := day.Read(io.Reader(zr))
	if err != nil {
		return series.Day{}, err
	}
	if want := strings.TrimSuffix(filepath.Base(path), ".jsonl.gz"); strings.ReplaceAll(h.Date, "-", "") != want {
		return series.Day{}, fmt.Errorf("file name says %s, header says %s", want, h.Date)
	}
	if err := day.SessionTimes(h.Date, h.RegularStart, h.RegularEnd); err != nil {
		return series.Day{}, err
	}
	d := series.Day{Date: h.Date, Lines: make(map[string]day.Line, len(lines))}
	for _, l := range lines {
		d.Lines[l.S] = l
	}
	return d, nil
}

func writeJSON(path string, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o644)
}

func indexPage(asOf string, symbols, covered int) string {
	e := html.EscapeString
	return `<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1">
<title>IEX end-of-day data</title>
<style>body{font:16px/1.5 system-ui,sans-serif;max-width:40rem;margin:2rem auto;padding:0 1rem;color:#1a1a1a;background:#fff}@media (prefers-color-scheme:dark){body{color:#eee;background:#111}a{color:#8ab4f8}}code{font-size:.9em}</style>
</head><body>
<h1>IEX end-of-day data</h1>
<p>Daily bars built from IEX trades, for <a href="https://stocks.vibistic.com/">Vibistic Stocks</a>. Data as of the close of ` + e(asOf) + `: ` + fmt.Sprint(symbols) + ` symbols, ` + fmt.Sprint(covered) + ` with enough IEX trading to show numbers.</p>
<p>` + e(series.Citation) + ` <a href="` + e(series.TermsURL) + `">IEX Historical Data Terms of Use</a>.</p>
<p>The data reflects trading on IEX only, not other markets, and is a reference point only, not a basis for trading decisions. Not investment advice.</p>
<p>Files: <code>v1/manifest.json</code>, <code>v1/s/{SYMBOL}.json</code> and, for symbols with enough IEX trading, <code>v1/og/{SYMBOL}.png</code> (the preview image of a Vibistic Stocks share link). How they are made: <a href="https://github.com/vibistic-hq/vibistic-iex-data">github.com/vibistic-hq/vibistic-iex-data</a>.</p>
</body></html>
`
}
