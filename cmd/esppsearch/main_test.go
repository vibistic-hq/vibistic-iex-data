package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/vibistic-hq/vibistic-iex-data/internal/sec"
)

var now = time.Date(2026, 11, 10, 9, 0, 0, 0, time.UTC)

// fakeSearch answers like EDGAR full-text search: for each form, the hits
// whose date is in the window, 100 a page. fail answers the first n requests
// with a 500.
type fakeSearch struct {
	hits  map[string][]map[string]any // by form
	fail  int32
	total string // "eq" unless set
	count int    // a total to report instead of the real one, when set
	seen  int32
	ua    atomic.Value
}

func (s *fakeSearch) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	n := atomic.AddInt32(&s.seen, 1)
	s.ua.Store(r.Header.Get("User-Agent"))
	if n <= s.fail {
		w.WriteHeader(500)
		return
	}
	q := r.URL.Query()
	if q.Get("q") != `"employee stock purchase plan"` || q.Get("dateRange") != "custom" {
		w.WriteHeader(400)
		return
	}
	var in []map[string]any
	for _, h := range s.hits[q.Get("forms")] {
		d := h["_source"].(map[string]any)["file_date"].(string)
		if d >= q.Get("startdt") && d <= q.Get("enddt") {
			in = append(in, h)
		}
	}
	from, _ := strconv.Atoi(q.Get("from"))
	end := min(from+100, len(in))
	if from > len(in) {
		from = len(in)
	}
	rel := s.total
	if rel == "" {
		rel = "eq"
	}
	count := len(in)
	if s.count > 0 {
		count = s.count
	}
	json.NewEncoder(w).Encode(map[string]any{"hits": map[string]any{"total": map[string]any{"value": count, "relation": rel}, "hits": in[from:end]}})
}

func hit(cik int64, form, date string, n int) map[string]any {
	return map[string]any{"_id": "x", "_source": map[string]any{"ciks": []string{fmt.Sprintf("%010d", cik)}, "form": form, "file_type": form, "file_date": date, "adsh": fmt.Sprintf("%010d-26-%06d", cik, n)}}
}

func fixture() *fakeSearch {
	s := &fakeSearch{hits: map[string][]map[string]any{}}
	// 250 10-Ks in one month (three pages), Apple and Microsoft among them; 120 S-8s.
	for i := 0; i < 250; i++ {
		s.hits["10-K"] = append(s.hits["10-K"], hit(int64(1000+i), "10-K", "2026-02-15", i))
	}
	s.hits["10-K"] = append(s.hits["10-K"], hit(320193, "10-K", "2026-11-03", 1), hit(789019, "10-K", "2026-07-29", 2))
	for i := 0; i < 120; i++ {
		s.hits["S-8"] = append(s.hits["S-8"], hit(int64(5000+i), "S-8", "2024-06-01", i))
	}
	return s
}

func testClient(url string) *client {
	return &client{base: url, ua: "Test test@example.com", http: &http.Client{Timeout: 5 * time.Second}, pace: 0, backoff: time.Millisecond}
}

func lower(t *testing.T) {
	t.Helper()
	old := []int{minTenK, minS8}
	t.Cleanup(func() { minTenK, minS8 = old[0], old[1] })
	minTenK, minS8 = 200, 100
}

func TestRunPagesEveryWindowAndWritesTheFile(t *testing.T) {
	lower(t)
	s := fixture()
	s.fail = 2 // two failures are retried
	srv := httptest.NewServer(s)
	defer srv.Close()
	out := filepath.Join(t.TempDir(), "espp-search.json")
	c := testClient(srv.URL)
	if err := run(c, out, now); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(out)
	var f sec.SearchFile
	if err := json.Unmarshal(b, &f); err != nil || sec.ValidateSearch(&f, now) != nil {
		t.Fatalf("%v %.200s", err, b)
	}
	if len(f.TenK) != 252 || len(f.S8) != 120 || f.TenK["320193"].Filed != "2026-11-03" {
		t.Fatalf("tenK %d s8 %d", len(f.TenK), len(f.S8))
	}
	if ua := s.ua.Load().(string); ua != "Test test@example.com" {
		t.Fatalf("user agent %q", ua)
	}
	// 14 months of 10-Ks (February with 3 pages) and 37 of S-8s (June 2024 with 2), plus the two retries.
	if c.requests != 14+2+37+1+2 {
		t.Fatalf("%d requests", c.requests)
	}
	if _, err := os.Stat(out + ".tmp"); !os.IsNotExist(err) {
		t.Fatal("temporary file left")
	}
}

func TestRunWritesNothingWhenItCannotBeTrusted(t *testing.T) {
	lower(t)
	for name, set := range map[string]func(*fakeSearch){
		"keeps failing":  func(s *fakeSearch) { s.fail = 1 << 20 },
		"count cut off":  func(s *fakeSearch) { s.total = "gte" },
		"too few":        func(s *fakeSearch) { s.hits["10-K"] = s.hits["10-K"][200:] },
		"Apple missing":  func(s *fakeSearch) { s.hits["10-K"] = s.hits["10-K"][:251] },
		"no S-8s at all": func(s *fakeSearch) { s.hits["S-8"] = nil },
		"S-8s only exhibits": func(s *fakeSearch) {
			for _, h := range s.hits["S-8"] {
				h["_source"].(map[string]any)["file_type"] = "EX-5.1"
			}
		},
	} {
		s := fixture()
		set(s)
		srv := httptest.NewServer(s)
		out := filepath.Join(t.TempDir(), "espp-search.json")
		if err := run(testClient(srv.URL), out, now); err == nil {
			t.Errorf("%s: no error", name)
		}
		if _, err := os.Stat(out); !os.IsNotExist(err) {
			t.Errorf("%s: file written", name)
		}
		srv.Close()
	}
}

func TestRunStopsAtTheRequestCap(t *testing.T) {
	lower(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(503) }))
	defer srv.Close()
	c := testClient(srv.URL)
	c.requests = maxRequests - 3
	err := run(c, filepath.Join(t.TempDir(), "x.json"), now)
	if err == nil || !strings.Contains(err.Error(), "requests") || c.requests != maxRequests {
		t.Fatalf("%v, %d requests", err, c.requests)
	}
}

func TestMonths(t *testing.T) {
	got := months(time.Date(2025, 10, 6, 0, 0, 0, 0, time.UTC), time.Date(2025, 12, 10, 0, 0, 0, 0, time.UTC))
	want := [][2]string{{"2025-10-06", "2025-10-31"}, {"2025-11-01", "2025-11-30"}, {"2025-12-01", "2025-12-10"}}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("%v", got)
	}
}

// Codex review of PR #23: up to 10,000 hits a month can be paged (offsets up to
// 9,900); more cannot.
func TestRunPagesUpToTenThousandHitsAMonth(t *testing.T) {
	lower(t)
	s := fixture()
	for i := len(s.hits["10-K"]); i < 10000; i++ {
		s.hits["10-K"] = append(s.hits["10-K"], hit(int64(100000+i), "10-K", "2026-02-15", i))
	}
	srv := httptest.NewServer(s)
	defer srv.Close()
	out := filepath.Join(t.TempDir(), "espp-search.json")
	if err := run(testClient(srv.URL), out, now); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(out)
	var f sec.SearchFile
	json.Unmarshal(b, &f)
	if len(f.TenK) != 10000 {
		t.Fatalf("%d 10-K companies", len(f.TenK))
	}
	s.count = 10001
	if err := run(testClient(srv.URL), filepath.Join(t.TempDir(), "x.json"), now); err == nil {
		t.Fatal("10,001 hits accepted")
	}
}
