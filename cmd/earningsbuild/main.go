// Command earningsbuild turns SEC EDGAR's bulk submissions (every company's
// filings index) into v1/earnings.json (issue #12): for each covered symbol,
// the estimated filing date of its next earnings release (internal/earnings).
// It fails, writing nothing, when the result does not pass the checks.
//
//	earningsbuild -submissions submissions.zip -tickers company_tickers_exchange.json -reviewed sec-reviewed.json -out earnings.json
package main

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"
	_ "time/tzdata"

	"github.com/vibistic-hq/vibistic-iex-data/internal/earnings"
	"github.com/vibistic-hq/vibistic-iex-data/internal/sec"
)

// The largest company's filings index is a few tens of MB.
const maxEntry = 256 << 20

// Lowered by the tests.
var minSymbols = earnings.MinSymbols

var (
	mainRe = regexp.MustCompile(`^CIK([0-9]{10})\.json$`)
	pageRe = regexp.MustCompile(`^CIK([0-9]{10})-submissions-[0-9]{3}\.json$`)
)

func main() {
	subsPath := flag.String("submissions", "submissions.zip", "SEC bulk submissions.zip")
	tickersPath := flag.String("tickers", "company_tickers_exchange.json", "SEC company_tickers_exchange.json")
	reviewedPath := flag.String("reviewed", "sec-reviewed.json", "reviewed extra share classes")
	out := flag.String("out", "earnings.json", "output file (must not exist)")
	flag.Parse()
	if err := run(*subsPath, *tickersPath, *reviewedPath, *out, time.Now()); err != nil {
		fmt.Fprintln(os.Stderr, "earningsbuild:", err)
		os.Exit(1)
	}
}

func run(subsPath, tickersPath, reviewedPath, out string, now time.Time) error {
	symbols, err := readSymbols(tickersPath, reviewedPath)
	if err != nil {
		return err
	}
	byCIK := map[int64][]string{}
	for s, c := range symbols {
		byCIK[c] = append(byCIK[c], s)
	}
	zr, err := zip.OpenReader(subsPath)
	if err != nil {
		return fmt.Errorf("submissions: %w", err)
	}
	defer zr.Close()
	if _, err := os.Stat(out); err == nil {
		return fmt.Errorf("%s already exists", out)
	}
	// One entry per name: a name twice means the archive cannot be trusted.
	entries := map[string]*zip.File{}
	for _, e := range zr.File {
		if !mainRe.MatchString(e.Name) && !pageRe.MatchString(e.Name) {
			continue
		}
		if entries[e.Name] != nil {
			return fmt.Errorf("%s twice in the archive", e.Name)
		}
		entries[e.Name] = e
	}
	today := earnings.NewYorkDay(now)
	f := &earnings.File{V: 1, Source: "SEC", GeneratedAt: now.UTC().Format(time.RFC3339), Today: today.Format("2006-01-02"), Symbols: map[string]earnings.Entry{}}
	var asOf time.Time
	skipped := 0
	for name, e := range entries {
		m := mainRe.FindStringSubmatch(name)
		if m == nil {
			continue
		}
		cik, _ := strconv.ParseInt(m[1], 10, 64)
		if len(byCIK[cik]) == 0 {
			continue
		}
		raw, newest, err := companyReleases(e, entries, cik, today)
		if err != nil {
			// One company's bad index skips that company, not the snapshot.
			fmt.Fprintf(os.Stderr, "earningsbuild: %s: %v\n", name, err)
			skipped++
			continue
		}
		if newest.After(asOf) {
			asOf = newest
		}
		next, last, basis, ok := earnings.Estimate(raw, today)
		if !ok {
			continue
		}
		for _, s := range byCIK[cik] {
			f.Symbols[s] = earnings.Entry{CIK: cik, Next: next.Format("2006-01-02"), Last: last.Format("2006-01-02"), Basis: basis.Format("2006-01-02")}
		}
	}
	if asOf.IsZero() {
		return fmt.Errorf("no filings read")
	}
	// Filings of today may not all be in yet; the index is SEC's from last night.
	if !asOf.Before(today) {
		asOf = today.Add(-24 * time.Hour)
	}
	f.AsOf = asOf.Format("2006-01-02")
	if err := earnings.Validate(f); err != nil {
		return err
	}
	if len(f.Symbols) < minSymbols {
		return fmt.Errorf("only %d symbols (want at least %d)", len(f.Symbols), minSymbols)
	}
	if gap := today.Sub(asOf); gap > 5*24*time.Hour {
		return fmt.Errorf("newest filing is from %s", f.AsOf)
	}
	b, err := json.Marshal(f)
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "earningsbuild: %d symbols, filings to %s (%d companies skipped)\n", len(f.Symbols), f.AsOf, skipped)
	return os.WriteFile(out, b, 0o644)
}

func readSymbols(tickersPath, reviewedPath string) (map[string]int64, error) {
	var reviewed []sec.Class
	b, err := os.ReadFile(reviewedPath)
	if err != nil {
		return nil, err
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&reviewed); err != nil {
		return nil, fmt.Errorf("%s: %w", reviewedPath, err)
	}
	tf, err := os.Open(tickersPath)
	if err != nil {
		return nil, err
	}
	defer tf.Close()
	symbols, err := sec.Tickers(tf, reviewed)
	if err != nil {
		return nil, fmt.Errorf("tickers: %w", err)
	}
	return symbols, nil
}

// columns is a filings table: SEC's "recent" object and each older page.
type columns struct {
	Form       []string `json:"form"`
	FilingDate []string `json:"filingDate"`
	Items      []string `json:"items"`
}

type submissions struct {
	CIK     json.RawMessage `json:"cik"`
	Filings struct {
		Recent columns `json:"recent"`
		Files  []struct {
			Name     string `json:"name"`
			FilingTo string `json:"filingTo"`
		} `json:"files"`
	} `json:"filings"`
}

// companyReleases reads a company's Item 2.02 filing dates from its index and
// the older pages that reach back into the history used, and the newest filing
// date of any form.
func companyReleases(e *zip.File, entries map[string]*zip.File, cik int64, today time.Time) ([]time.Time, time.Time, error) {
	var sub submissions
	if err := readJSON(e, &sub); err != nil {
		return nil, time.Time{}, err
	}
	if got, err := parseCIK(sub.CIK); err != nil || got != cik {
		return nil, time.Time{}, fmt.Errorf("cik %s in the file for %d", sub.CIK, cik)
	}
	from := today.Add(-earnings.HistoryDays * 24 * time.Hour).Format("2006-01-02")
	tables := []columns{sub.Filings.Recent}
	prefix := fmt.Sprintf("CIK%010d-submissions-", cik)
	for _, p := range sub.Filings.Files {
		if p.FilingTo < from {
			continue
		}
		// A page of this company only.
		if !strings.HasPrefix(p.Name, prefix) || !pageRe.MatchString(p.Name) {
			return nil, time.Time{}, fmt.Errorf("page %q", p.Name)
		}
		pe := entries[p.Name]
		if pe == nil {
			return nil, time.Time{}, fmt.Errorf("page %s missing", p.Name)
		}
		var c columns
		if err := readJSON(pe, &c); err != nil {
			return nil, time.Time{}, fmt.Errorf("%s: %w", p.Name, err)
		}
		tables = append(tables, c)
	}
	var raw []time.Time
	var newest time.Time
	for _, c := range tables {
		ds, err := earnings.Releases(c.Form, c.FilingDate, c.Items)
		if err != nil {
			return nil, time.Time{}, err
		}
		raw = append(raw, ds...)
		for _, s := range c.FilingDate {
			if d, err := earnings.ParseDate(s); err == nil && d.After(newest) && !d.After(today) {
				newest = d
			}
		}
	}
	return raw, newest, nil
}

func parseCIK(raw json.RawMessage) (int64, error) {
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		var n int64
		if err := json.Unmarshal(raw, &n); err != nil {
			return 0, err
		}
		return n, nil
	}
	return strconv.ParseInt(s, 10, 64)
}

// readJSON reads one entry to its end (so the zip reader checks its checksum)
// and decodes it.
func readJSON(e *zip.File, v any) error {
	if e.UncompressedSize64 > maxEntry {
		return fmt.Errorf("%d bytes", e.UncompressedSize64)
	}
	rc, err := e.Open()
	if err != nil {
		return err
	}
	b, err := io.ReadAll(io.LimitReader(rc, maxEntry+1))
	rc.Close()
	if err != nil {
		return err
	}
	if len(b) > maxEntry {
		return fmt.Errorf("over %d bytes", maxEntry)
	}
	return json.Unmarshal(b, v)
}
