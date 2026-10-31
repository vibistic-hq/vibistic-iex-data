// Command secbuild turns SEC EDGAR's bulk company facts into v1/sec files
// (issue #8): one {SYMBOL}.json per covered symbol and snapshot.json, in a new
// directory. It fails, leaving nothing to publish, when the result does not
// pass the acceptance checks.
//
// It also writes shares.json, every covered symbol's shares outstanding
// (issue #18).
//
//	secbuild -facts companyfacts.zip -tickers company_tickers_exchange.json -reviewed sec-reviewed.json -shares-reviewed shares-reviewed.json -out sec/
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
	sharesReviewedPath := flag.String("shares-reviewed", "shares-reviewed.json", "reviewed multi-class companies counted by their balance-sheet total")
	out := flag.String("out", "sec", "output directory (created; must not exist)")
	flag.Parse()
	if err := run(*factsPath, *tickersPath, *reviewedPath, *sharesReviewedPath, *out, time.Now()); err != nil {
		fmt.Fprintln(os.Stderr, "secbuild:", err)
		os.Exit(1)
	}
}

func run(factsPath, tickersPath, reviewedPath, sharesReviewedPath, out string, now time.Time) error {
	var reviewed []sec.Class
	if err := readReviewed(reviewedPath, &reviewed); err != nil {
		return err
	}
	var sharesReviewed []sec.SharesClass
	if err := readReviewed(sharesReviewedPath, &sharesReviewed); err != nil {
		return err
	}
	if err := sec.ValidateSharesClasses(sharesReviewed); err != nil {
		return fmt.Errorf("%s: %w", sharesReviewedPath, err)
	}
	balance := map[int64]bool{}
	for _, c := range sharesReviewed {
		balance[c.CIK] = true
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
	shares := map[string]sec.Shares{}
	skipped := 0
	var wg sync.WaitGroup
	for range runtime.NumCPU() {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for e := range entries {
				m := entryRe.FindStringSubmatch(e.Name)
				cik, _ := strconv.ParseInt(m[1], 10, 64)
				got, count, countErr, err := readEntry(e, byCIK[cik], cik, balance[cik], now)
				mu.Lock()
				if err != nil {
					// One company's bad file skips that company, not the snapshot.
					fmt.Fprintf(os.Stderr, "secbuild: %s: %v\n", e.Name, err)
					skipped++
				}
				for _, f := range got {
					files[f.Symbol] = f
				}
				// A company whose count could not be read has none; its
				// revenue and net income stand on their own.
				if countErr != nil {
					fmt.Fprintf(os.Stderr, "secbuild: %s: share count: %v\n", e.Name, countErr)
				} else if count.N != nil || count.Why != "" {
					for _, sym := range byCIK[cik] {
						shares[sym] = count
					}
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
	// The share counts are left out alone: the revenue and net income files
	// never wait on them (Codex review).
	sf := &sec.SharesFile{V: 1, Source: "SEC", GeneratedAt: now.UTC().Format(time.RFC3339), Symbols: shares}
	if err := sec.ValidateShares(sf); err != nil {
		fmt.Fprintf(os.Stderr, "secbuild: share counts left out: %v\n", err)
	} else if err := acceptShares(shares); err != nil {
		fmt.Fprintf(os.Stderr, "secbuild: share counts left out: %v\n", err)
	} else if err := writeJSON(filepath.Join(out, "shares.json"), sf); err != nil {
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

// readEntry extracts one company's facts once, for each of its symbols, and
// its share count; a failure of either leaves the other.
func readEntry(e *zip.File, symbols []string, cik int64, balance bool, now time.Time) (files []*sec.File, count sec.Shares, countErr, err error) {
	files, b, err := readFiles(e, symbols, cik, now)
	if b == nil {
		return nil, sec.Shares{}, nil, err
	}
	count, countErr = sec.ExtractShares(b, cik, len(symbols), balance, now)
	return files, count, countErr, err
}

func readFiles(e *zip.File, symbols []string, cik int64, now time.Time) ([]*sec.File, []byte, error) {
	rc, err := e.Open()
	if err != nil {
		return nil, nil, err
	}
	// Read to the end, so the zip reader checks the entry's checksum before
	// any number from it is used.
	b, err := io.ReadAll(io.LimitReader(rc, maxEntry+1))
	rc.Close()
	if err != nil {
		return nil, nil, err
	}
	if len(b) > maxEntry {
		return nil, nil, fmt.Errorf("over %d bytes", maxEntry)
	}
	// From here on the bytes are whole and checked: they go back with any
	// error, for the share count.
	f, err := sec.Extract(bytes.NewReader(b), symbols[0], now)
	if err != nil {
		return nil, b, err
	}
	// No revenue or net income to publish; the share count may still be there.
	if f == nil {
		return nil, b, nil
	}
	if f.CIK != cik {
		return nil, b, fmt.Errorf("cik %d in a file named for %d", f.CIK, cik)
	}
	var out []*sec.File
	for _, sym := range symbols {
		c := *f
		c.Symbol = sym
		if err := sec.Validate(&c); err != nil {
			return nil, b, fmt.Errorf("%s: %w", sym, err)
		}
		out = append(out, &c)
	}
	return out, b, nil
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

// Acceptance for the share counts: enough of them, and the largest filers'
// among them.
var (
	minShares      = 3000
	mustHaveShares = []string{"AAPL", "MSFT", "NVDA", "GOOGL"}
)

func acceptShares(shares map[string]sec.Shares) error {
	n := 0
	for _, s := range shares {
		if s.N != nil {
			n++
		}
	}
	if n < minShares {
		return fmt.Errorf("only %d share counts (want at least %d)", n, minShares)
	}
	for _, s := range mustHaveShares {
		if shares[s].N == nil {
			return fmt.Errorf("no share count for %s", s)
		}
	}
	return nil
}

func readReviewed(path string, v any) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return fmt.Errorf("%s: %w", path, err)
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
