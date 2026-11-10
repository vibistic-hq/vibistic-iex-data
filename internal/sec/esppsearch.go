package sec

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"time"
)

// ESPP evidence from EDGAR full-text search (issue #22): companies whose 10-K
// of the last 400 days, or Form S-8 of the last three years, names an
// "employee stock purchase plan" in its main document. Read by
// cmd/esppsearch, merged into espp.json by cmd/secbuild beside the XBRL
// issuance evidence (espp.go), which ranks first.
//
// Checked 2026-11-10: 1,658 companies' 10-Ks name one (30 of 30 sampled refer
// to the company's own plan, in notes, exhibit lists or cash flows; no 10-K in
// the last 13 months says it has none), and 997 companies' S-8s.

// Evidence kinds, strongest first, with how old the filing may be.
const (
	HowIssued = "issued"
	HowTenK   = "10-K"
	HowS8     = "S-8"
)

var esppHowAge = map[string]int{HowIssued: 730, HowTenK: 400, HowS8: 1100}

// SearchFile is espp-search.json: the newest 10-K and S-8 that name a plan,
// per company (CIK as a decimal string).
type SearchFile struct {
	V           int                  `json:"v"`
	GeneratedAt string               `json:"generatedAt"`
	TenK        map[string]SearchHit `json:"tenK"`
	S8          map[string]SearchHit `json:"s8"`
}

type SearchHit struct {
	Filed string `json:"filed"` // yyyy-mm-dd
	Accn  string `json:"accn"`
}

// SearchPage is one answer of efts.sec.gov/LATEST/search-index: only the
// fields read here. Untrusted: every hit is checked before use.
type SearchPage struct {
	Total int
	// Hits in the answer, read or skipped: what paging moves on by.
	Returned int
	Hits     []PageHit
}

type PageHit struct {
	CIKs []int64
	// Standard industry codes of the filer(s); 6770 is a blank check (SPAC).
	SICs     []string
	Form     string // the filing's form
	FileType string // this document's type: the main document has the form's
	Filed    string
	Accn     string
}

// MaxSearchPage is far above a 100-hit answer (about 150 KB).
const MaxSearchPage = 8 << 20

var digitsRe = regexp.MustCompile(`^[0-9]{1,10}$`)

// ParseSearchPage reads one answer. A hit that cannot be read whole is
// skipped (it is never evidence), not an error; a malformed answer is.
func ParseSearchPage(r io.Reader) (*SearchPage, error) {
	b, err := io.ReadAll(io.LimitReader(r, MaxSearchPage+1))
	if err != nil {
		return nil, err
	}
	if len(b) > MaxSearchPage {
		return nil, fmt.Errorf("answer over %d bytes", MaxSearchPage)
	}
	var raw struct {
		TimedOut *bool `json:"timed_out"`
		Shards   *struct {
			Failed int `json:"failed"`
		} `json:"_shards"`
		Hits struct {
			Total struct {
				Value    int    `json:"value"`
				Relation string `json:"relation"`
			} `json:"total"`
			Hits []struct {
				Source struct {
					CIKs     []string `json:"ciks"`
					SICs     []string `json:"sics"`
					Form     string   `json:"form"`
					FileType string   `json:"file_type"`
					FileDate string   `json:"file_date"`
					Adsh     string   `json:"adsh"`
				} `json:"_source"`
			} `json:"hits"`
		} `json:"hits"`
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	if err := dec.Decode(&raw); err != nil {
		return nil, err
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, fmt.Errorf("data after the answer")
	}
	// A search that timed out or lost a shard can answer 200 with part of the
	// hits and an exact-looking total (Codex review of PR #23): an error, so
	// the request is tried again.
	if raw.TimedOut != nil && *raw.TimedOut {
		return nil, fmt.Errorf("search timed out")
	}
	if raw.Shards != nil && raw.Shards.Failed != 0 {
		return nil, fmt.Errorf("%d shards failed", raw.Shards.Failed)
	}
	if raw.Hits.Total.Value < 0 || raw.Hits.Total.Relation != "eq" {
		// "gte" means the count was cut off: paging would miss companies.
		return nil, fmt.Errorf("total %d %q", raw.Hits.Total.Value, raw.Hits.Total.Relation)
	}
	page := &SearchPage{Total: raw.Hits.Total.Value, Returned: len(raw.Hits.Hits)}
	for _, h := range raw.Hits.Hits {
		s := h.Source
		if _, err := parseDate(s.FileDate); err != nil || !accnRe.MatchString(s.Adsh) || len(s.CIKs) == 0 || len(s.CIKs) > 20 {
			continue
		}
		var ciks []int64
		for _, c := range s.CIKs {
			if !digitsRe.MatchString(c) {
				ciks = nil
				break
			}
			n, _ := strconv.ParseInt(c, 10, 64)
			if n < 1 {
				ciks = nil
				break
			}
			ciks = append(ciks, n)
		}
		if ciks == nil {
			continue
		}
		page.Hits = append(page.Hits, PageHit{CIKs: ciks, SICs: s.SICs, Form: s.Form, FileType: s.FileType, Filed: s.FileDate, Accn: s.Adsh})
	}
	return page, nil
}

// Add records a hit when it is the main document of a filing of that form,
// filed within the kind's window and not after now, keeping each company's
// newest (by filing date, then accession).
func (f *SearchFile) Add(h PageHit, now time.Time) {
	var into map[string]SearchHit
	var how string
	switch {
	case h.Form == "10-K" && h.FileType == "10-K":
		into, how = f.TenK, HowTenK
	case h.Form == "S-8" && h.FileType == "S-8":
		into, how = f.S8, HowS8
	default:
		return
	}
	// A blank check company (SPAC) writes of the plan the merged company "will
	// adopt", not one of its own (Codex review of PR #23: DRDB).
	for _, s := range h.SICs {
		if s == "6770" {
			return
		}
	}
	filed, err := parseDate(h.Filed)
	if err != nil || filed.After(now) || now.Sub(filed) > time.Duration(esppHowAge[how])*24*time.Hour {
		return
	}
	for _, c := range h.CIKs {
		k := strconv.FormatInt(c, 10)
		if old, ok := into[k]; ok && (old.Filed > h.Filed || (old.Filed == h.Filed && old.Accn >= h.Accn)) {
			continue
		}
		into[k] = SearchHit{Filed: h.Filed, Accn: h.Accn}
	}
}

// ValidateSearch checks a search file the way secbuild reads it.
func ValidateSearch(f *SearchFile, now time.Time) error {
	if f.V != 1 {
		return fmt.Errorf("v")
	}
	gen, err := time.Parse(time.RFC3339, f.GeneratedAt)
	if err != nil || gen.After(now.Add(time.Hour)) || now.Sub(gen) > 3*24*time.Hour {
		return fmt.Errorf("generatedAt %q", f.GeneratedAt)
	}
	for how, m := range map[string]map[string]SearchHit{HowTenK: f.TenK, HowS8: f.S8} {
		if len(m) > 50000 {
			return fmt.Errorf("%s: %d companies", how, len(m))
		}
		for k, h := range m {
			if !digitsRe.MatchString(k) || k[0] == '0' || !accnRe.MatchString(h.Accn) {
				return fmt.Errorf("%s: %q %+v", how, k, h)
			}
			filed, err := parseDate(h.Filed)
			if err != nil || filed.After(gen) || gen.Sub(filed) > time.Duration(esppHowAge[how])*24*time.Hour {
				return fmt.Errorf("%s: %q filed %q", how, k, h.Filed)
			}
		}
	}
	return nil
}

// Best is a company's strongest search evidence still within its kind's age at
// `now` (the build may run after the search: Codex review of PR #23): its
// 10-K, else its S-8.
func (f *SearchFile) Best(cik int64, now time.Time) (SearchHit, string, bool) {
	if f == nil {
		return SearchHit{}, "", false
	}
	k := strconv.FormatInt(cik, 10)
	fresh := func(h SearchHit, how string) bool {
		filed, err := parseDate(h.Filed)
		return err == nil && !filed.After(now) && now.Sub(filed) <= time.Duration(esppHowAge[how])*24*time.Hour
	}
	if h, ok := f.TenK[k]; ok && fresh(h, HowTenK) {
		return h, HowTenK, true
	}
	if h, ok := f.S8[k]; ok && fresh(h, HowS8) {
		return h, HowS8, true
	}
	return SearchHit{}, "", false
}
