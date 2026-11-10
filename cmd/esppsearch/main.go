// Command esppsearch asks EDGAR full-text search which companies' 10-K (last
// 400 days) and Form S-8 (last three years) name an "employee stock purchase
// plan" in their main document, for the ESPP list (issue #22), and writes
// espp-search.json for cmd/secbuild. It writes nothing and exits non-zero when
// any request fails for good or the result does not pass its checks, so
// secbuild then lists the shares-issued evidence only.
//
//	esppsearch -ua "Vibistic Stocks contact@example.com" -out espp-search.json
//
// About 50 requests a run (one or two per month and form), one at a time,
// four a second at most: well within SEC's fair-access limit of ten.
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"time"

	"github.com/vibistic-hq/vibistic-iex-data/internal/sec"
)

const (
	searchURL   = "https://efts.sec.gov/LATEST/search-index"
	phrase      = `"employee stock purchase plan"`
	maxRequests = 600
	// The search answers hits up to this many (offset 9,900 plus a page of 100);
	// a month with more would be cut (Codex review of PR #23).
	maxTotal = 10000
)

// Acceptance: a run that finds far fewer than on 2026-11-10 (1,658 and 997
// companies), or misses these large filers' 10-Ks, means something changed.
var (
	minTenK      = 800
	minS8        = 300
	mustHaveTenK = []int64{320193, 789019} // Apple, Microsoft
)

func main() {
	ua := flag.String("ua", "", "User-Agent naming who asks, with a contact address (SEC's fair-access policy)")
	out := flag.String("out", "espp-search.json", "output file")
	flag.Parse()
	if *ua == "" {
		fmt.Fprintln(os.Stderr, "esppsearch: -ua is required")
		os.Exit(2)
	}
	c := &client{base: searchURL, ua: *ua, http: &http.Client{Timeout: 60 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, pace: 250 * time.Millisecond, backoff: 3 * time.Second}
	if err := run(c, *out, time.Now()); err != nil {
		fmt.Fprintln(os.Stderr, "esppsearch:", err)
		os.Exit(1)
	}
}

type client struct {
	base     string
	ua       string
	http     *http.Client
	pace     time.Duration
	backoff  time.Duration
	requests int
	last     time.Time
}

// get asks for one page, retrying a failed request up to four more times.
func (c *client) get(form, start, end string, from int) (*sec.SearchPage, error) {
	q := url.Values{}
	q.Set("q", phrase)
	q.Set("forms", form)
	q.Set("dateRange", "custom")
	q.Set("startdt", start)
	q.Set("enddt", end)
	if from > 0 {
		q.Set("from", fmt.Sprint(from))
	}
	u := c.base + "?" + q.Encode()
	var lastErr error
	for attempt := 0; attempt < 5; attempt++ {
		if attempt > 0 {
			time.Sleep(c.backoff * time.Duration(attempt))
		}
		if c.requests >= maxRequests {
			return nil, fmt.Errorf("over %d requests", maxRequests)
		}
		if wait := c.pace - time.Since(c.last); wait > 0 {
			time.Sleep(wait)
		}
		c.last = time.Now()
		c.requests++
		page, err := c.once(u)
		if err == nil {
			return page, nil
		}
		lastErr = err
	}
	return nil, fmt.Errorf("%s %s..%s from %d: %w", form, start, end, from, lastErr)
}

func (c *client) once(u string) (*sec.SearchPage, error) {
	req, err := http.NewRequest(http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", c.ua)
	req.Header.Set("Accept", "application/json")
	res, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", res.StatusCode)
	}
	return sec.ParseSearchPage(res.Body)
}

// months splits [from, to] into calendar months (the first and last partial).
func months(from, to time.Time) [][2]string {
	var out [][2]string
	for d := from; !d.After(to); {
		next := time.Date(d.Year(), d.Month()+1, 1, 0, 0, 0, 0, time.UTC)
		end := next.AddDate(0, 0, -1)
		if end.After(to) {
			end = to
		}
		out = append(out, [2]string{d.Format("2006-01-02"), end.Format("2006-01-02")})
		d = next
	}
	return out
}

func run(c *client, out string, now time.Time) error {
	now = now.UTC()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	f := &sec.SearchFile{V: 1, GeneratedAt: now.Format(time.RFC3339), TenK: map[string]sec.SearchHit{}, S8: map[string]sec.SearchHit{}}
	for _, w := range []struct {
		form string
		days int
	}{{"10-K", 400}, {"S-8", 1100}} {
		for _, m := range months(today.AddDate(0, 0, -w.days), today) {
			from := 0
			for {
				page, err := c.get(w.form, m[0], m[1], from)
				if err != nil {
					return err
				}
				if page.Total > maxTotal {
					return fmt.Errorf("%s %s..%s: %d hits, more than one window can page", w.form, m[0], m[1], page.Total)
				}
				// Every hit counts toward paging, read or not.
				from += pageLen(page)
				for _, h := range page.Hits {
					f.Add(h, now)
				}
				if from >= page.Total {
					break
				}
				if pageLen(page) == 0 {
					return fmt.Errorf("%s %s..%s: an empty page at %d of %d", w.form, m[0], m[1], from, page.Total)
				}
			}
		}
	}
	if err := sec.ValidateSearch(f, now); err != nil {
		return err
	}
	if err := accept(f); err != nil {
		return err
	}
	b, err := json.Marshal(f)
	if err != nil {
		return err
	}
	tmp := out + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "esppsearch: %d companies with a 10-K, %d with an S-8, %d requests\n", len(f.TenK), len(f.S8), c.requests)
	return os.Rename(tmp, out)
}

// pageLen is how many hits the answer held, including any skipped as unreadable.
func pageLen(p *sec.SearchPage) int { return p.Returned }

func accept(f *sec.SearchFile) error {
	if len(f.TenK) < minTenK || len(f.S8) < minS8 {
		return fmt.Errorf("only %d companies with a 10-K and %d with an S-8 (want %d and %d)", len(f.TenK), len(f.S8), minTenK, minS8)
	}
	for _, c := range mustHaveTenK {
		if _, ok := f.TenK[fmt.Sprint(c)]; !ok {
			return errors.New(fmt.Sprint("no 10-K for CIK ", c))
		}
	}
	return nil
}
