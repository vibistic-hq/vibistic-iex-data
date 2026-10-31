package main

import (
	"archive/zip"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vibistic-hq/vibistic-iex-data/internal/earnings"
)

var now = time.Date(2026, 10, 20, 9, 30, 0, 0, time.UTC)

type table struct {
	Form       []string `json:"form"`
	FilingDate []string `json:"filingDate"`
	Items      []string `json:"items"`
}

func releases(dates ...string) table {
	var t table
	for _, d := range dates {
		t.Form = append(t.Form, "8-K")
		t.FilingDate = append(t.FilingDate, d)
		t.Items = append(t.Items, "2.02,9.01")
	}
	return t
}

func sub(cik any, recent table, pages ...map[string]string) []byte {
	b, _ := json.Marshal(map[string]any{"cik": cik, "name": "x", "filings": map[string]any{"recent": recent, "files": pages}})
	return b
}

// fixture: Apple with its whole history in "recent"; JPM with its older
// releases on a page; a company whose file names another CIK; one whose page is
// missing; an unmapped company.
func fixture(t *testing.T, extra map[string][]byte) (string, string, string) {
	t.Helper()
	tmp := t.TempDir()
	jpmPage, _ := json.Marshal(releases("2025-01-15", "2025-04-11", "2025-07-15", "2025-10-14"))
	oldF6, _ := json.Marshal(table{Form: []string{"F-6EF", "424B4"}, FilingDate: []string{"2019-03-01", "2019-03-02"}, Items: []string{"", ""}})
	// JPM's newest filing is a Form 4 of yesterday (sets asOf); Apple has a 10-Q.
	apple := releases("2025-07-31", "2025-10-30", "2026-01-29", "2026-04-30", "2026-07-30")
	apple.Form = append(apple.Form, "10-Q")
	apple.FilingDate = append(apple.FilingDate, "2026-07-31")
	apple.Items = append(apple.Items, "")
	jpm := releases("2026-01-13", "2026-04-14", "2026-07-14", "2026-10-13")
	jpm.Form = append(jpm.Form, "4")
	jpm.FilingDate = append(jpm.FilingDate, "2026-10-19")
	jpm.Items = append(jpm.Items, "")
	entries := map[string][]byte{
		"CIK0000320193.json":                 sub("320193", apple),
		"CIK0000019617.json":                 sub("0000019617", jpm, map[string]string{"name": "CIK0000019617-submissions-001.json", "filingTo": "2025-12-31"}),
		"CIK0000019617-submissions-001.json": jpmPage,
		"CIK0000000011.json":                 sub("12", releases("2025-07-31", "2025-10-30", "2026-01-29", "2026-04-30", "2026-07-30")),
		"CIK0000000013.json":                 sub(13, releases("2025-07-31", "2025-10-30", "2026-01-29", "2026-04-30", "2026-07-30"), map[string]string{"name": "CIK0000000013-submissions-001.json", "filingTo": "2025-06-30"}),
		"CIK0000000021.json":                 sub(21, releases("2026-08-04")),
		"CIK0000000099.json":                 sub(99, releases("2025-07-31", "2025-10-30", "2026-01-29", "2026-04-30", "2026-07-30")),
		// Depositary shares registered on an old page (issue #18); an old page missing.
		"CIK0000000031.json":                 sub(31, releases("2025-07-31", "2025-10-30", "2026-01-29", "2026-04-30", "2026-07-30"), map[string]string{"name": "CIK0000000031-submissions-001.json", "filingTo": "2019-06-30"}),
		"CIK0000000031-submissions-001.json": oldF6,
		"CIK0000000041.json":                 sub(41, releases("2025-07-31", "2025-10-30", "2026-01-29", "2026-04-30", "2026-07-30"), map[string]string{"name": "CIK0000000041-submissions-001.json", "filingTo": "2019-06-30"}),
	}
	for k, v := range extra {
		entries[k] = v
	}
	zpath := filepath.Join(tmp, "submissions.zip")
	zf, _ := os.Create(zpath)
	zw := zip.NewWriter(zf)
	for name, b := range entries {
		w, _ := zw.Create(name)
		w.Write(b)
	}
	zw.Close()
	zf.Close()
	tpath := filepath.Join(tmp, "tickers.json")
	os.WriteFile(tpath, []byte(`{"fields":["cik","name","ticker","exchange"],"data":[[320193,"Apple Inc.","AAPL","Nasdaq"],[19617,"JPMorgan","JPM","NYSE"],[11,"Wrong Cik","WRNG","NYSE"],[13,"Lost Page","LOST","NYSE"],[21,"New Listing","NEWCO","Nasdaq"],[31,"ADS Co","ADSC","Nasdaq"],[41,"Old Page","OLDP","NYSE"]]}`), 0o644)
	rpath := filepath.Join(tmp, "reviewed.json")
	os.WriteFile(rpath, []byte(`[]`), 0o644)
	return zpath, tpath, rpath
}

func TestRun(t *testing.T) {
	minSymbols = 1
	defer func() { minSymbols = earnings.MinSymbols }()
	z, tk, r := fixture(t, nil)
	out := filepath.Join(t.TempDir(), "earnings.json")
	if err := run(z, tk, r, out, now); err != nil {
		t.Fatal(err)
	}
	var f earnings.File
	b, _ := os.ReadFile(out)
	if err := json.Unmarshal(b, &f); err != nil {
		t.Fatal(err)
	}
	if err := earnings.Validate(&f); err != nil {
		t.Fatal(err)
	}
	want := map[string]earnings.Entry{
		"AAPL": {CIK: 320193, Next: "2026-10-29", Last: "2026-07-30", Basis: "2025-10-30"},
		// The older releases come from the page.
		"JPM": {CIK: 19617, Next: "2027-01-12", Last: "2026-10-13", Basis: "2026-01-13"},
		// Old pages give no dates: a missing one does not cost the estimate.
		"ADSC": {CIK: 31, Next: "2026-10-29", Last: "2026-07-30", Basis: "2025-10-30"},
		"OLDP": {CIK: 41, Next: "2026-10-29", Last: "2026-07-30", Basis: "2025-10-30"},
	}
	// Every company read in full, and the one with an F-6; not the one whose old page is missing.
	if got := fmt.Sprint(f.Filers, f.Depositary); got != "[21 31 19617 320193] [31]" {
		t.Fatalf("filers, depositary = %s", got)
	}
	if len(f.Symbols) != len(want) {
		t.Fatalf("symbols %v (a file naming another CIK and a missing page are skipped)", f.Symbols)
	}
	for s, e := range want {
		if f.Symbols[s] != e {
			t.Errorf("%s = %+v, want %+v", s, f.Symbols[s], e)
		}
	}
	// A recent listing with one release: no estimate, and why.
	if got := f.NoEstimate; len(got) != 1 || len(got[earnings.WhyFew]) != 1 || got[earnings.WhyFew][0] != "NEWCO" {
		t.Fatalf("noEstimate = %v (skipped companies are not listed)", got)
	}
	if f.Today != "2026-10-20" || f.AsOf != "2026-10-19" || f.GeneratedAt != "2026-10-20T09:30:00Z" {
		t.Fatalf("header %+v", f)
	}
	if err := run(z, tk, r, out, now); err == nil || !strings.Contains(err.Error(), "exists") {
		t.Fatal("must refuse to overwrite")
	}
}

func TestRunRefusesTooFewSymbols(t *testing.T) {
	z, tk, r := fixture(t, nil)
	out := filepath.Join(t.TempDir(), "earnings.json")
	if err := run(z, tk, r, out, now); err == nil {
		t.Fatal("want an error")
	}
	if _, err := os.Stat(out); !os.IsNotExist(err) {
		t.Fatal("wrote a file")
	}
}

func TestRunRefusesStaleFilings(t *testing.T) {
	minSymbols = 1
	defer func() { minSymbols = earnings.MinSymbols }()
	z, tk, r := fixture(t, nil)
	// Ten days on, the newest filing (Oct 19) is too old: SEC's index stopped.
	if err := run(z, tk, r, filepath.Join(t.TempDir(), "e.json"), now.Add(10*24*time.Hour)); err == nil {
		t.Fatal("want an error")
	}
}

// A page whose last filing date is missing or bad, a page listed twice, or
// pages over the company's byte budget: the company's history is an error
// (skipped), never read without them.
func TestCompanyReleasesRejectsBadPageLists(t *testing.T) {
	recent := releases("2025-07-31", "2025-10-30", "2026-01-29", "2026-04-30", "2026-07-30")
	page, _ := json.Marshal(releases("2025-04-30"))
	pageName := "CIK0000320193-submissions-001.json"
	read := func(t *testing.T, pages ...map[string]string) error {
		z, _, _ := fixture(t, map[string][]byte{"CIK0000320193.json": sub(320193, recent, pages...), pageName: page})
		zr, err := zip.OpenReader(z)
		if err != nil {
			t.Fatal(err)
		}
		defer zr.Close()
		entries := map[string]*zip.File{}
		for _, e := range zr.File {
			entries[e.Name] = e
		}
		_, _, _, _, err = companyReleases(entries["CIK0000320193.json"], entries, 320193, earnings.NewYorkDay(now))
		return err
	}
	if err := read(t, map[string]string{"name": pageName, "filingTo": "2025-04-30"}); err != nil {
		t.Fatalf("a good page: %v", err)
	}
	for name, pages := range map[string][]map[string]string{
		"no filingTo":  {{"name": pageName}},
		"bad filingTo": {{"name": pageName, "filingTo": "2026-13-01"}},
		"twice":        {{"name": pageName, "filingTo": "2025-04-30"}, {"name": pageName, "filingTo": "2025-04-30"}},
		"other cik":    {{"name": "CIK0000019617-submissions-001.json", "filingTo": "2025-04-30"}},
	} {
		t.Run(name, func(t *testing.T) {
			if err := read(t, pages...); err == nil {
				t.Fatal("want an error")
			}
		})
	}
	t.Run("over budget", func(t *testing.T) {
		maxCompany = uint64(len(page) - 1)
		defer func() { maxCompany = 512 << 20 }()
		if err := read(t, map[string]string{"name": pageName, "filingTo": "2025-04-30"}); err == nil {
			t.Fatal("want an error")
		}
	})
}

func TestRunSkipsUnalignedColumns(t *testing.T) {
	minSymbols = 1
	defer func() { minSymbols = earnings.MinSymbols }()
	bad := releases("2025-07-31", "2025-10-30", "2026-01-29", "2026-04-30", "2026-07-30")
	bad.Items = bad.Items[:4]
	z, tk, r := fixture(t, map[string][]byte{"CIK0000320193.json": sub(320193, bad)})
	out := filepath.Join(t.TempDir(), "earnings.json")
	if err := run(z, tk, r, out, now); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(out)
	if strings.Contains(string(b), "AAPL") {
		t.Fatal("a company with unaligned columns got an estimate")
	}
}
