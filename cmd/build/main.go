// Command build turns the archive of day files into the published site:
// v1/s/{SYMBOL}.json for every symbol, v1/og/{SYMBOL}.png (the preview image
// of Vibistic Stocks share links) for every covered symbol, v1/manifest.json
// and an index page. With -sec, it also publishes a checked SEC snapshot
// (cmd/secbuild) as v1/sec/{SYMBOL}.json (issue #8), and with -earnings a
// checked earnings estimates file (cmd/earningsbuild) as v1/earnings.json
// (issue #12).
//
//	build -days days/ -reviewed splits-reviewed.json [-sec sec/] [-earnings earnings.json] -out site/
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
	"github.com/vibistic-hq/vibistic-iex-data/internal/earnings"
	"github.com/vibistic-hq/vibistic-iex-data/internal/og"
	"github.com/vibistic-hq/vibistic-iex-data/internal/sec"
	"github.com/vibistic-hq/vibistic-iex-data/internal/series"
)

func main() {
	daysDir := flag.String("days", "days", "directory of YYYYMMDD.jsonl.gz day files")
	reviewed := flag.String("reviewed", "splits-reviewed.json", "reviewed splits and breaks")
	out := flag.String("out", "site", "output directory (created; must not exist)")
	secDir := flag.String("sec", "", "SEC snapshot directory from secbuild (optional)")
	earningsPath := flag.String("earnings", "", "earnings estimates file from earningsbuild (optional)")
	validate := flag.Bool("validate", false, "only check the reviewed file")
	flag.Parse()
	if *validate {
		if _, err := readReviews(*reviewed); err != nil {
			fmt.Fprintln(os.Stderr, "build:", err)
			os.Exit(1)
		}
		return
	}
	if err := run(*daysDir, *reviewed, *secDir, *earningsPath, *out, time.Now()); err != nil {
		fmt.Fprintln(os.Stderr, "build:", err)
		os.Exit(1)
	}
}

func run(daysDir, reviewedPath, secDir, earningsPath, out string, now time.Time) error {
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
	// The IEX files never wait on SEC's: a missing, stale or bad snapshot is
	// left out (the app then says SEC figures are unavailable).
	if secDir != "" {
		info, err := copySEC(secDir, filepath.Join(out, "v1", "sec"), now)
		if err != nil {
			fmt.Fprintf(os.Stderr, "build: SEC snapshot left out: %v\n", err)
			os.RemoveAll(filepath.Join(out, "v1", "sec"))
		} else {
			manifest["sec"] = info
		}
	}
	// Nor on the earnings estimates, which are left out alone.
	var filings *earnings.File
	if earningsPath != "" {
		info, f, err := copyEarnings(earningsPath, filepath.Join(out, "v1", "earnings.json"), now)
		if err != nil {
			fmt.Fprintf(os.Stderr, "build: earnings estimates left out: %v\n", err)
			os.Remove(filepath.Join(out, "v1", "earnings.json"))
		} else {
			manifest["earnings"] = info
			filings = f
		}
	}
	// The share counts (issue #18) are left out alone too, and without the
	// earnings file's list of companies whose filings were read for depositary
	// shares (its Filers), since an ADS issuer's count is not its listing's.
	if secDir != "" {
		info, err := copyShares(filepath.Join(secDir, "shares.json"), filepath.Join(out, "v1", "shares.json"), filings, now)
		if err != nil {
			fmt.Fprintf(os.Stderr, "build: share counts left out: %v\n", err)
			os.Remove(filepath.Join(out, "v1", "shares.json"))
		} else {
			manifest["shares"] = info
		}
	}
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
		if f.Coverage != "ok" {
			continue
		}
		// A file the app would refuse gets no image (its share page shows the
		// generic card); a slip in our own output, so say which.
		if err := og.Accepted(f); err != nil {
			fmt.Fprintf(os.Stderr, "build: no preview image for %s: the app would reject its file: %v\n", sym, err)
			continue
		}
		syms <- sym
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

// A snapshot older than this is not published: a company's ticker could
// have passed to another company since.
const secMaxAge = 10 * 24 * time.Hour

// copySEC checks every file of a secbuild snapshot again (strict decoding,
// sec.Validate, the symbol matching the file name) and copies them all, or
// none.
func copySEC(dir, dst string, now time.Time) (map[string]any, error) {
	var snap struct {
		V           int    `json:"v"`
		Source      string `json:"source"`
		GeneratedAt string `json:"generatedAt"`
		Files       int    `json:"files"`
	}
	if err := decodeStrict(filepath.Join(dir, "snapshot.json"), &snap, secMaxFile); err != nil {
		return nil, err
	}
	gen, err := time.Parse(time.RFC3339, snap.GeneratedAt)
	if err != nil || snap.V != 1 || snap.Source != "SEC" {
		return nil, fmt.Errorf("snapshot.json: bad header")
	}
	if age := now.Sub(gen); age > secMaxAge || age < -time.Hour {
		return nil, fmt.Errorf("snapshot from %s is too old or in the future", snap.GeneratedAt)
	}
	paths, err := filepath.Glob(filepath.Join(dir, "*.json"))
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return nil, err
	}
	n := 0
	for _, p := range paths {
		name := filepath.Base(p)
		if name == "snapshot.json" || name == "shares.json" {
			continue
		}
		var f sec.File
		if err := decodeStrict(p, &f, secMaxFile); err != nil {
			return nil, err
		}
		if err := sec.Validate(&f); err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		if f.Symbol+".json" != name || f.GeneratedAt != snap.GeneratedAt {
			return nil, fmt.Errorf("%s: symbol %s, generated %s", name, f.Symbol, f.GeneratedAt)
		}
		if err := writeJSON(filepath.Join(dst, name), &f); err != nil {
			return nil, err
		}
		n++
	}
	if n != snap.Files || n == 0 {
		return nil, fmt.Errorf("%d files, snapshot.json says %d", n, snap.Files)
	}
	return map[string]any{"source": "SEC", "generatedAt": snap.GeneratedAt, "files": n}, nil
}

// The shares file is a few hundred KB.
const sharesMaxFile = 4 << 20

// copyShares checks the snapshot's share counts again and publishes them,
// leaving out (null, "depositary") every company that ever registered
// depositary shares (Form F-6): its 10-Q counts the underlying shares, while
// its US listing is an ADS that may stand for several of them. A count is
// null too ("check") unless the earnings file has the same symbol under the
// same company, its filings all read.
func copyShares(path, dst string, filings *earnings.File, now time.Time) (map[string]any, error) {
	if filings == nil {
		return nil, fmt.Errorf("no earnings file to tell depositary shares by")
	}
	if err := earnings.ValidatePrivate(filings); err != nil {
		return nil, fmt.Errorf("earnings file's filing lists: %w", err)
	}
	var f sec.SharesFile
	if err := decodeStrict(path, &f, sharesMaxFile); err != nil {
		return nil, err
	}
	if err := sec.ValidateShares(&f); err != nil {
		return nil, err
	}
	gen, _ := time.Parse(time.RFC3339, f.GeneratedAt)
	if age := now.Sub(gen); age > secMaxAge || age < -time.Hour {
		return nil, fmt.Errorf("share counts from %s are too old or in the future", f.GeneratedAt)
	}
	depositary := map[int64]bool{}
	for _, c := range filings.Depositary {
		depositary[c] = true
	}
	counted, ads, unread := 0, 0, 0
	for sym, s := range f.Symbols {
		if s.N == nil {
			continue
		}
		switch {
		case depositary[s.CIK]:
			f.Symbols[sym] = sec.Shares{Why: "depositary"}
			ads++
		// Read in full, and under the same symbol: the two files come from
		// separate downloads of SEC's ticker list, a day apart at most, and a
		// symbol that moved to another company in between gets no count.
		case filings.Filers[sym] != s.CIK:
			f.Symbols[sym] = sec.Shares{Why: "check"}
			unread++
		default:
			counted++
		}
	}
	if err := sec.ValidateShares(&f); err != nil {
		return nil, err
	}
	if counted == 0 {
		return nil, fmt.Errorf("no share counts")
	}
	if err := writeJSON(dst, &f); err != nil {
		return nil, err
	}
	fmt.Fprintf(os.Stderr, "build: %d share counts (%d depositary-share issuers and %d unread companies left out)\n", counted, ads, unread)
	return map[string]any{"source": "SEC", "generatedAt": f.GeneratedAt, "symbols": len(f.Symbols), "counted": counted}, nil
}

// A symbol's SEC file is a few KB; anything near this is not one.
const secMaxFile = 256 << 10

// The earnings file is a few hundred KB (the app refuses over 2 MB).
const earningsMaxFile = 2 << 20

// copyEarnings checks the earnings file again (strict decoding,
// earnings.Validate, at least earnings.MinSymbols, no older than an SEC
// snapshot may be) and writes it to dst.
// It returns the file as read, with the lists for the share counts (which
// the published file leaves out).
func copyEarnings(path, dst string, now time.Time) (map[string]any, *earnings.File, error) {
	var f earnings.File
	if err := decodeStrict(path, &f, earningsMaxFile); err != nil {
		return nil, nil, err
	}
	if err := earnings.Validate(&f); err != nil {
		return nil, nil, err
	}
	gen, _ := time.Parse(time.RFC3339, f.GeneratedAt)
	if age := now.Sub(gen); age > secMaxAge || age < -time.Hour {
		return nil, nil, fmt.Errorf("estimates from %s are too old or in the future", f.GeneratedAt)
	}
	if len(f.Symbols) < earnings.MinSymbols {
		return nil, nil, fmt.Errorf("only %d symbols", len(f.Symbols))
	}
	// The estimates never wait on the share counts' lists: published without
	// them, whatever they hold (ValidatePrivate is copyShares' concern).
	pub := f
	pub.Filers, pub.Depositary = nil, nil
	if err := writeJSON(dst, &pub); err != nil {
		return nil, nil, err
	}
	return map[string]any{"source": "SEC", "generatedAt": f.GeneratedAt, "asOf": f.AsOf, "symbols": len(f.Symbols)}, &f, nil
}

// decodeStrict reads one small JSON document of at most max bytes: known
// fields only, nothing after it.
func decodeStrict(path string, v any, max int64) error {
	st, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !st.Mode().IsRegular() || st.Size() > max {
		return fmt.Errorf("%s: not a regular file under %d bytes", filepath.Base(path), max)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	dec := json.NewDecoder(strings.NewReader(string(b)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return fmt.Errorf("%s: %w", filepath.Base(path), err)
	}
	if _, err := dec.Token(); err != io.EOF {
		return fmt.Errorf("%s: data after the document", filepath.Base(path))
	}
	return nil
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

// A real day file unpacks to a few MB; the cap keeps a corrupt or hostile release
// asset from filling memory (security audit run-1). A variable so a test can lower it.
var maxDayBytes int64 = 64 << 20

func readDay(path string) (series.Day, error) {
	// A regular file only, like decodeStrict's inputs: never a link out of days/.
	st, err := os.Lstat(path)
	if err != nil {
		return series.Day{}, err
	}
	if !st.Mode().IsRegular() {
		return series.Day{}, fmt.Errorf("%s is not a regular file", path)
	}
	f, err := os.Open(path)
	if err != nil {
		return series.Day{}, err
	}
	defer f.Close()
	zr, err := gzip.NewReader(f)
	if err != nil {
		return series.Day{}, err
	}
	lr := &io.LimitedReader{R: zr, N: maxDayBytes + 1}
	h, lines, err := day.Read(lr)
	// Checked first: a file cut off at the cap usually also fails to parse.
	if lr.N <= 0 {
		return series.Day{}, fmt.Errorf("%s unpacks to more than %d bytes", path, maxDayBytes)
	}
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
<p>Files: <code>v1/manifest.json</code>, <code>v1/s/{SYMBOL}.json</code> and, for symbols with enough IEX trading, <code>v1/og/{SYMBOL}.png</code> (the preview image of a Vibistic Stocks share link), and <code>v1/sec/{SYMBOL}.json</code> (revenue and net income from the company's SEC filings, as first reported), <code>v1/shares.json</code> (each company's shares outstanding as last reported on the cover of its SEC filings), and <code>v1/earnings.json</code> (each company's next earnings release date, estimated from its past SEC filings: a year after the matching one, so it can be off). How they are made: <a href="https://github.com/vibistic-hq/vibistic-iex-data">github.com/vibistic-hq/vibistic-iex-data</a>.</p>
</body></html>
`
}
