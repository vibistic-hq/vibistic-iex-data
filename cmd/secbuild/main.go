// Command secbuild turns SEC EDGAR's bulk company facts into v1/sec files
// (issue #8): one {SYMBOL}.json per covered symbol and snapshot.json, in a new
// directory. It fails, leaving nothing to publish, when the result does not
// pass the acceptance checks.
//
//	secbuild -facts companyfacts.zip -tickers company_tickers_exchange.json -reviewed sec-reviewed.json -out sec/
package main

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/vibistic-hq/vibistic-iex-data/internal/sec"
)

// The largest companyfacts file is under 100 MB; a bigger entry is skipped.
const maxEntry = 256 << 20

// Acceptance: enough files, and these large filers present with a recent quarter.
var minFiles = 4000

var mustHave = []string{"AAPL", "MSFT", "NVDA"}

var entryRe = regexp.MustCompile(`^CIK([0-9]{10})\.json$`)

func main() {
	factsPath := flag.String("facts", "companyfacts.zip", "SEC bulk companyfacts.zip")
	tickersPath := flag.String("tickers", "company_tickers_exchange.json", "SEC company_tickers_exchange.json")
	reviewedPath := flag.String("reviewed", "sec-reviewed.json", "reviewed extra share classes")
	out := flag.String("out", "sec", "output directory (created; must not exist)")
	flag.Parse()
	if err := run(*factsPath, *tickersPath, *reviewedPath, *out, time.Now()); err != nil {
		fmt.Fprintln(os.Stderr, "secbuild:", err)
		os.Exit(1)
	}
}

func run(factsPath, tickersPath, reviewedPath, out string, now time.Time) error {
	var reviewed []sec.Class
	b, err := os.ReadFile(reviewedPath)
	if err != nil {
		return err
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&reviewed); err != nil {
		return fmt.Errorf("%s: %w", reviewedPath, err)
	}
	tf, err := os.Open(tickersPath)
	if err != nil {
		return err
	}
	symbols, err := sec.Tickers(tf, reviewed)
	tf.Close()
	if err != nil {
		return fmt.Errorf("tickers: %w", err)
	}
	byCIK := map[int64][]string{}
	for s, c := range symbols {
		byCIK[c] = append(byCIK[c], s)
	}
	zr, err := zip.OpenReader(factsPath)
	if err != nil {
		return fmt.Errorf("facts: %w", err)
	}
	defer zr.Close()
	if _, err := os.Stat(out); err == nil {
		return fmt.Errorf("%s already exists", out)
	}
	if err := os.MkdirAll(out, 0o755); err != nil {
		return err
	}

	// Two entries for one company: neither is used (which one is right
	// cannot be told).
	count := map[int64]int{}
	for _, e := range zr.File {
		if m := entryRe.FindStringSubmatch(e.Name); m != nil {
			cik, _ := strconv.ParseInt(m[1], 10, 64)
			count[cik]++
		}
	}
	entries := make(chan *zip.File)
	var mu sync.Mutex
	files := map[string]*sec.File{}
	skipped := 0
	var wg sync.WaitGroup
	for range runtime.NumCPU() {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for e := range entries {
				m := entryRe.FindStringSubmatch(e.Name)
				cik, _ := strconv.ParseInt(m[1], 10, 64)
				got, err := readEntry(e, byCIK[cik], cik, now)
				mu.Lock()
				if err != nil {
					// One company's bad file skips that company, not the snapshot.
					fmt.Fprintf(os.Stderr, "secbuild: %s: %v\n", e.Name, err)
					skipped++
				}
				for _, f := range got {
					files[f.Symbol] = f
				}
				mu.Unlock()
			}
		}()
	}
	for _, e := range zr.File {
		m := entryRe.FindStringSubmatch(e.Name)
		if m == nil {
			continue
		}
		cik, _ := strconv.ParseInt(m[1], 10, 64)
		if len(byCIK[cik]) == 0 {
			continue
		}
		if count[cik] > 1 || e.UncompressedSize64 > maxEntry {
			fmt.Fprintf(os.Stderr, "secbuild: %s: %d entries for the company or %d bytes, skipped\n", e.Name, count[cik], e.UncompressedSize64)
			mu.Lock()
			skipped++
			mu.Unlock()
			continue
		}
		entries <- e
	}
	close(entries)
	wg.Wait()

	if err := accept(files, now); err != nil {
		return err
	}
	syms := make([]string, 0, len(files))
	for s, f := range files {
		syms = append(syms, s)
		if err := writeJSON(filepath.Join(out, s+".json"), f); err != nil {
			return err
		}
	}
	sort.Strings(syms)
	snap := map[string]any{"v": 1, "source": "SEC", "generatedAt": now.UTC().Format(time.RFC3339), "files": len(files)}
	fmt.Fprintf(os.Stderr, "secbuild: %d files (%d companies skipped)\n", len(files), skipped)
	return writeJSON(filepath.Join(out, "snapshot.json"), snap)
}

// readEntry extracts one company's facts once, for each of its symbols.
func readEntry(e *zip.File, symbols []string, cik int64, now time.Time) ([]*sec.File, error) {
	rc, err := e.Open()
	if err != nil {
		return nil, err
	}
	// Read to the end, so the zip reader checks the entry's checksum before
	// any number from it is used.
	b, err := io.ReadAll(io.LimitReader(rc, maxEntry+1))
	rc.Close()
	if err != nil {
		return nil, err
	}
	if len(b) > maxEntry {
		return nil, fmt.Errorf("over %d bytes", maxEntry)
	}
	f, err := sec.Extract(bytes.NewReader(b), symbols[0], now)
	if err != nil || f == nil {
		return nil, err
	}
	if f.CIK != cik {
		return nil, fmt.Errorf("cik %d in a file named for %d", f.CIK, cik)
	}
	var out []*sec.File
	for _, sym := range symbols {
		c := *f
		c.Symbol = sym
		if err := sec.Validate(&c); err != nil {
			return nil, fmt.Errorf("%s: %w", sym, err)
		}
		out = append(out, &c)
	}
	return out, nil
}

// accept fails a snapshot that is too small or misses the largest filers'
// recent quarters: something upstream changed, and the last snapshot stays.
func accept(files map[string]*sec.File, now time.Time) error {
	if len(files) < minFiles {
		return fmt.Errorf("only %d files (want at least %d)", len(files), minFiles)
	}
	for _, s := range mustHave {
		f := files[s]
		if f == nil || len(f.Quarters) == 0 {
			return fmt.Errorf("%s missing or without quarters", s)
		}
		end, _ := time.Parse("2006-01-02", f.Quarters[len(f.Quarters)-1].End)
		if now.Sub(end) > 200*24*time.Hour {
			return fmt.Errorf("%s: newest quarter ends %s", s, end.Format("2006-01-02"))
		}
	}
	return nil
}

func writeJSON(path string, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o644)
}
