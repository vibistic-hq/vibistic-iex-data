package main

import (
	"archive/zip"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vibistic-hq/vibistic-iex-data/internal/sec"
)

var now = time.Date(2026, 10, 18, 9, 0, 0, 0, time.UTC)

// fixture writes a companyfacts.zip from internal/sec's real reduced files, a
// broken entry and an unmapped company, and the matching ticker list.
func fixture(t *testing.T) (string, string, string, string) {
	t.Helper()
	tmp := t.TempDir()
	zpath := filepath.Join(tmp, "companyfacts.zip")
	zf, _ := os.Create(zpath)
	zw := zip.NewWriter(zf)
	for name, src := range map[string]string{"CIK0000320193.json": "AAPL", "CIK0001045810.json": "NVDA", "CIK0001652044.json": "GOOGL", "CIK0000000099.json": "", "CIK0000000077.json": "NET"} {
		w, _ := zw.Create(name)
		if src == "" {
			w.Write([]byte(`{"cik":99,"facts":`))
			continue
		}
		b, err := os.ReadFile("../../internal/sec/testdata/" + src + ".json")
		if err != nil {
			t.Fatal(err)
		}
		w.Write(withShares(t, b, src))
	}
	zw.Close()
	zf.Close()
	tpath := filepath.Join(tmp, "tickers.json")
	os.WriteFile(tpath, []byte(`{"fields":["cik","name","ticker","exchange"],"data":[
		[320193,"Apple Inc.","AAPL","Nasdaq"],[1045810,"NVIDIA CORP","NVDA","Nasdaq"],
		[1652044,"Alphabet Inc.","GOOGL","Nasdaq"],[1652044,"Alphabet Inc.","GOOG","Nasdaq"],
		[99,"Broken","BRKN","NYSE"],[77,"Mislabeled","MISL","NYSE"],[320193,"Apple Inc.","AAPL-P","NYSE"]]}`), 0o644)
	rpath := filepath.Join(tmp, "reviewed.json")
	os.WriteFile(rpath, []byte(`[{"symbol":"GOOG","cik":1652044},{"symbol":"AAPL.P","cik":320193}]`), 0o644)
	spath := filepath.Join(tmp, "shares-reviewed.json")
	os.WriteFile(spath, []byte(`[{"cik":1652044,"name":"Alphabet"}]`), 0o644)
	return zpath, tpath, rpath, spath
}

// withShares adds share counts to a reduced companyfacts file: a cover count
// for AAPL, a cover count and a balance-sheet total for GOOGL (whose two
// classes are reviewed), none for NVDA.
func withShares(t *testing.T, b []byte, src string) []byte {
	t.Helper()
	var doc map[string]any
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatal(err)
	}
	facts := doc["facts"].(map[string]any)
	gaap := facts["us-gaap"].(map[string]any)
	units := func(fs ...map[string]any) map[string]any {
		return map[string]any{"units": map[string]any{"shares": fs}}
	}
	switch src {
	case "AAPL":
		facts["dei"] = map[string]any{"EntityCommonStockSharesOutstanding": units(map[string]any{"end": "2026-07-17", "val": 14594180000, "accn": "0000320193-26-000020", "form": "10-Q", "filed": "2026-07-31"})}
		// Shares issued under an ESPP last quarter (issue #20).
		gaap["StockIssuedDuringPeriodSharesEmployeeStockPurchasePlans"] = units(map[string]any{"start": "2026-03-29", "end": "2026-06-27", "val": 3000000, "accn": "0000320193-26-000020", "form": "10-Q", "filed": "2026-07-31"})
	case "GOOGL":
		facts["dei"] = map[string]any{"EntityCommonStockSharesOutstanding": units(map[string]any{"end": "2026-07-15", "val": 5800000000, "accn": "0001652044-26-000071", "form": "10-Q", "filed": "2026-07-23"})}
		gaap["CommonStockSharesOutstanding"] = units(map[string]any{"end": "2026-06-30", "val": 12230000000, "accn": "0001652044-26-000071", "form": "10-Q", "filed": "2026-07-23"})
	}
	out, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestSecbuild(t *testing.T) {
	defer func(n int, m []string) { minFiles, mustHave = n, m }(minFiles, mustHave)
	minFiles, mustHave = 4, []string{"AAPL", "NVDA"}
	defer func(n int, m []string) { minShares, mustHaveShares = n, m }(minShares, mustHaveShares)
	minShares, mustHaveShares = 3, []string{"AAPL", "GOOGL"}
	defer func(n int, m []string) { minEspp, mustHaveEspp = n, m }(minEspp, mustHaveEspp)
	minEspp, mustHaveEspp = 1, []string{"AAPL"}
	zpath, tpath, rpath, spath := fixture(t)
	out := filepath.Join(t.TempDir(), "sec")
	if err := run(zpath, tpath, rpath, spath, out, now); err != nil {
		t.Fatal(err)
	}
	names, _ := filepath.Glob(filepath.Join(out, "*.json"))
	var got []string
	for _, n := range names {
		got = append(got, filepath.Base(n))
	}
	// Both Alphabet classes (GOOG reviewed); not the broken entry, nor a file whose CIK is not its name's.
	if strings.Join(got, ",") != "AAPL.P.json,AAPL.json,GOOG.json,GOOGL.json,NVDA.json,espp.json,shares.json,snapshot.json" {
		t.Fatalf("files %v", got)
	}
	b, _ := os.ReadFile(filepath.Join(out, "GOOG.json"))
	var f sec.File
	if err := json.Unmarshal(b, &f); err != nil || f.Symbol != "GOOG" || f.CIK != 1652044 || sec.Validate(&f) != nil {
		t.Fatalf("GOOG.json: %v %+v", err, f)
	}
	s, _ := os.ReadFile(filepath.Join(out, "snapshot.json"))
	if string(s) != `{"files":5,"generatedAt":"2026-10-18T09:00:00Z","source":"SEC","v":1}` {
		t.Fatalf("snapshot %s", s)
	}
	// Alphabet's two classes both count the balance-sheet total, never the
	// one class's cover count; NVDA has no count; MISL's file was refused.
	var sf sec.SharesFile
	b, _ = os.ReadFile(filepath.Join(out, "shares.json"))
	if err := json.Unmarshal(b, &sf); err != nil || sec.ValidateShares(&sf) != nil {
		t.Fatalf("shares.json: %v %s", err, b)
	}
	for sym, want := range map[string]float64{"AAPL": 14594180000, "GOOGL": 12230000000, "GOOG": 12230000000} {
		if c := sf.Symbols[sym]; c.N == nil || *c.N != want {
			t.Errorf("%s: %+v", sym, c)
		}
	}
	if c, ok := sf.Symbols["NVDA"]; !ok || c.Why != "none" {
		t.Errorf("NVDA: %+v", c)
	}
	// A preferred listing gets no count, and is not a second class of common stock for AAPL's.
	if c := sf.Symbols["AAPL.P"]; c.N != nil || c.Why != "notcommon" {
		t.Errorf("AAPL.P: %+v", c)
	}
	if _, ok := sf.Symbols["MISL"]; ok || len(sf.Symbols) != 5 {
		t.Errorf("symbols %v", sf.Symbols)
	}
	// Only AAPL's filings show ESPP shares issued: not its preferred listing, not NVDA.
	var ef sec.EsppFile
	b, _ = os.ReadFile(filepath.Join(out, "espp.json"))
	if err := json.Unmarshal(b, &ef); err != nil || sec.ValidateEspp(&ef) != nil {
		t.Fatalf("espp.json: %v %s", err, b)
	}
	if a, ok := ef.Symbols["AAPL"]; !ok || len(ef.Symbols) != 1 || a.CIK != 320193 || a.Filed != "2026-07-31" || a.Accn != "0000320193-26-000020" {
		t.Fatalf("espp %+v", ef.Symbols)
	}
	if err := run(zpath, tpath, rpath, spath, out, now); err == nil {
		t.Fatal("overwrote an existing directory")
	}
}

func TestSecbuildLeavesOutUnacceptedEsppList(t *testing.T) {
	defer func(n int, m []string) { minFiles, mustHave = n, m }(minFiles, mustHave)
	defer func(n int, m []string) { minShares, mustHaveShares = n, m }(minShares, mustHaveShares)
	defer func(n int, m []string) { minEspp, mustHaveEspp = n, m }(minEspp, mustHaveEspp)
	zpath, tpath, rpath, spath := fixture(t)
	for name, set := range map[string]func(){
		"too few":      func() { minEspp, mustHaveEspp = 2, nil },
		"MSFT missing": func() { minEspp, mustHaveEspp = 0, []string{"MSFT"} },
	} {
		minFiles, mustHave = 1, []string{"AAPL"}
		minShares, mustHaveShares = 0, nil
		set()
		out := filepath.Join(t.TempDir(), "sec")
		if err := run(zpath, tpath, rpath, spath, out, now); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if _, err := os.Stat(filepath.Join(out, "espp.json")); !os.IsNotExist(err) {
			t.Errorf("%s: espp.json written", name)
		}
		for _, f := range []string{"AAPL.json", "shares.json", "snapshot.json"} {
			if _, err := os.Stat(filepath.Join(out, f)); err != nil {
				t.Errorf("%s: %v", name, err)
			}
		}
	}
}

// zipOf writes a zip of the given entries (name, content, stored or deflated).
func zipOf(t *testing.T, entries [][2]string, store bool) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "facts.zip")
	zf, _ := os.Create(p)
	zw := zip.NewWriter(zf)
	for _, e := range entries {
		h := &zip.FileHeader{Name: e[0], Method: zip.Deflate}
		if store {
			h.Method = zip.Store
		}
		w, _ := zw.CreateHeader(h)
		w.Write([]byte(e[1]))
	}
	zw.Close()
	zf.Close()
	return p
}

func TestSecbuildRejectsDuplicateAndCorruptEntries(t *testing.T) {
	defer func(n int, m []string) { minFiles, mustHave = n, m }(minFiles, mustHave)
	minFiles, mustHave = 1, []string{"AAPL"}
	defer func(n int, m []string) { minShares, mustHaveShares = n, m }(minShares, mustHaveShares)
	minShares, mustHaveShares = 0, nil
	_, tpath, rpath, spath := fixture(t)
	aapl, _ := os.ReadFile("../../internal/sec/testdata/AAPL.json")
	nvda, _ := os.ReadFile("../../internal/sec/testdata/NVDA.json")
	// Two entries for Nvidia: neither is used.
	z := zipOf(t, [][2]string{{"CIK0000320193.json", string(aapl)}, {"CIK0001045810.json", string(nvda)}, {"CIK0001045810.json", string(nvda)}}, false)
	out := filepath.Join(t.TempDir(), "sec")
	if err := run(z, tpath, rpath, spath, out, now); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(out, "NVDA.json")); !os.IsNotExist(err) {
		t.Error("a duplicated company was published")
	}
	// A stored entry changed after its checksum was written: the company is skipped.
	z = zipOf(t, [][2]string{{"CIK0000320193.json", string(aapl)}, {"CIK0001045810.json", string(nvda)}}, true)
	b, _ := os.ReadFile(z)
	i := strings.Index(string(b), `"val":96221000000`)
	if i < 0 {
		t.Fatal("value not found")
	}
	b[i+7] = '9'
	os.WriteFile(z, b, 0o644)
	out = filepath.Join(t.TempDir(), "sec")
	if err := run(z, tpath, rpath, spath, out, now); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(out, "NVDA.json")); !os.IsNotExist(err) {
		t.Error("an entry with a bad checksum was published")
	}
}

func TestSecbuildReviewedFile(t *testing.T) {
	zpath, tpath, rpath, spath := fixture(t)
	bad := filepath.Join(t.TempDir(), "reviewed.json")
	for _, doc := range []string{`[{"symbol":"GOOG","cik":1652044,"note":"x"}]`, `[{"symbol":"goog","cik":1652044}]`, `[{"symbol":"GOOG","cik":0}]`, `{`} {
		os.WriteFile(bad, []byte(doc), 0o644)
		if err := run(zpath, tpath, bad, spath, filepath.Join(t.TempDir(), "sec"), now); err == nil {
			t.Errorf("%s accepted", doc)
		}
	}
	// A bad shares-reviewed.json (or none) costs the share counts only (Codex review).
	defer func(n int, m []string) { minFiles, mustHave = n, m }(minFiles, mustHave)
	minFiles, mustHave = 1, []string{"AAPL"}
	for _, doc := range []string{`[{"cik":1652044,"name":"Alphabet","note":"x"}]`, `[{"cik":1652044}]`, `[{"cik":0,"name":"x"}]`, `{`, ""} {
		path := bad
		if doc == "" {
			path = filepath.Join(t.TempDir(), "missing.json")
		} else {
			os.WriteFile(bad, []byte(doc), 0o644)
		}
		out := filepath.Join(t.TempDir(), "sec")
		if err := run(zpath, tpath, rpath, path, out, now); err != nil {
			t.Errorf("shares-reviewed %q: %v", doc, err)
		}
		if _, err := os.Stat(filepath.Join(out, "shares.json")); !os.IsNotExist(err) {
			t.Errorf("shares-reviewed %q: shares.json written", doc)
		}
		if _, err := os.Stat(filepath.Join(out, "AAPL.json")); err != nil {
			t.Errorf("shares-reviewed %q: %v", doc, err)
		}
	}
}

// Share counts that fail their acceptance are left out, alone: the revenue
// and net income files are still written (Codex review).
func TestSecbuildLeavesOutUnacceptedShareCounts(t *testing.T) {
	defer func(n int, m []string) { minFiles, mustHave = n, m }(minFiles, mustHave)
	defer func(n int, m []string) { minShares, mustHaveShares = n, m }(minShares, mustHaveShares)
	zpath, tpath, rpath, spath := fixture(t)
	for name, set := range map[string]func(){
		"too few counts": func() { minShares, mustHaveShares = 4, nil },
		"count missing":  func() { minShares, mustHaveShares = 0, []string{"NVDA"} },
	} {
		minFiles, mustHave = 1, []string{"AAPL"}
		set()
		out := filepath.Join(t.TempDir(), "sec")
		if err := run(zpath, tpath, rpath, spath, out, now); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if _, err := os.Stat(filepath.Join(out, "shares.json")); !os.IsNotExist(err) {
			t.Errorf("%s: shares.json written", name)
		}
		for _, f := range []string{"AAPL.json", "snapshot.json"} {
			if _, err := os.Stat(filepath.Join(out, f)); err != nil {
				t.Errorf("%s: %v", name, err)
			}
		}
	}
}

func TestSharesReviewedFileInRepo(t *testing.T) {
	var cs []sec.SharesClass
	if err := readReviewed("../../shares-reviewed.json", &cs); err != nil || sec.ValidateSharesClasses(cs) != nil || len(cs) == 0 {
		t.Fatalf("shares-reviewed.json: %v", err)
	}
}

func TestReviewedFileInRepo(t *testing.T) {
	b, err := os.ReadFile("../../sec-reviewed.json")
	if err != nil {
		t.Fatal(err)
	}
	var cs []sec.Class
	dec := json.NewDecoder(strings.NewReader(string(b)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&cs); err != nil || sec.ValidateClasses(cs) != nil || len(cs) == 0 {
		t.Fatalf("sec-reviewed.json: %v", err)
	}
}

func TestSecbuildAcceptance(t *testing.T) {
	defer func(n int, m []string) { minFiles, mustHave = n, m }(minFiles, mustHave)
	defer func(n int, m []string) { minShares, mustHaveShares = n, m }(minShares, mustHaveShares)
	zpath, tpath, rpath, spath := fixture(t)
	for name, set := range map[string]func(){
		"too few":   func() { minFiles, mustHave = 6, []string{"AAPL"} },
		"missing":   func() { minFiles, mustHave = 1, []string{"MSFT"} },
		"not fresh": func() { minFiles, mustHave = 1, []string{"AAPL"}; now = now.AddDate(1, 0, 0) },
	} {
		before := now
		minShares, mustHaveShares = 0, nil
		set()
		if err := run(zpath, tpath, rpath, spath, filepath.Join(t.TempDir(), "sec"), now); err == nil {
			t.Errorf("%s: accepted", name)
		}
		now = before
	}
}

// Issue #22: the full-text search evidence fills in companies without issuance
// facts; issuance stays first; a bad or missing search file leaves it out.
func TestSecbuildMergesSearchEvidence(t *testing.T) {
	defer func(n int, m []string) { minFiles, mustHave = n, m }(minFiles, mustHave)
	defer func(n int, m []string) { minShares, mustHaveShares = n, m }(minShares, mustHaveShares)
	defer func(n int, m []string) { minEspp, mustHaveEspp = n, m }(minEspp, mustHaveEspp)
	defer func(p string) { esppSearchPath = p }(esppSearchPath)
	minFiles, mustHave = 1, []string{"AAPL"}
	minShares, mustHaveShares = 0, nil
	minEspp, mustHaveEspp = 1, []string{"AAPL"}
	zpath, tpath, rpath, spath := fixture(t)
	searchFile := func(body string) string {
		p := filepath.Join(t.TempDir(), "espp-search.json")
		os.WriteFile(p, []byte(body), 0o644)
		return p
	}
	good := `{"v":1,"generatedAt":"2026-10-18T08:00:00Z",
		"tenK":{"1045810":{"filed":"2026-02-25","accn":"0001045810-26-000021"},"320193":{"filed":"2025-10-31","accn":"0000320193-25-000079"}},
		"s8":{"1652044":{"filed":"2024-06-01","accn":"0001652044-24-000060"}}}`
	read := func(out string) sec.EsppFile {
		var ef sec.EsppFile
		b, err := os.ReadFile(filepath.Join(out, "espp.json"))
		if err != nil || json.Unmarshal(b, &ef) != nil || sec.ValidateEspp(&ef) != nil {
			t.Fatalf("espp.json: %v %s", err, b)
		}
		return ef
	}

	esppSearchPath = searchFile(good)
	out := filepath.Join(t.TempDir(), "sec")
	if err := run(zpath, tpath, rpath, spath, out, now); err != nil {
		t.Fatal(err)
	}
	ef := read(out)
	if a := ef.Symbols["AAPL"]; a.How != sec.HowIssued || a.Filed != "2026-07-31" {
		t.Fatalf("AAPL %+v", a)
	}
	if n := ef.Symbols["NVDA"]; n.How != sec.HowTenK || n.Filed != "2026-02-25" || n.CIK != 1045810 {
		t.Fatalf("NVDA %+v", n)
	}
	// Both Alphabet classes from its S-8; never the preferred listing.
	if g, gg := ef.Symbols["GOOGL"], ef.Symbols["GOOG"]; g.How != sec.HowS8 || gg.How != sec.HowS8 {
		t.Fatalf("GOOGL %+v GOOG %+v", g, gg)
	}
	if _, ok := ef.Symbols["AAPL.P"]; ok || len(ef.Symbols) != 4 {
		t.Fatalf("symbols %+v", ef.Symbols)
	}

	for name, body := range map[string]string{
		"malformed":     `{"v":1`,
		"unknown field": strings.Replace(good, `"v":1`, `"v":1,"x":1`, 1),
		"stale":         strings.Replace(good, "2026-10-18T08:00:00Z", "2026-10-10T08:00:00Z", 1),
		"bad hit":       strings.Replace(good, "0001045810-26-000021", "x", 1),
		// Codex review of PR #23: the whole file or nothing.
		"trailing data": good + " x",
		"two documents": good + good,
	} {
		esppSearchPath = searchFile(body)
		out := filepath.Join(t.TempDir(), "sec")
		if err := run(zpath, tpath, rpath, spath, out, now); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if ef := read(out); len(ef.Symbols) != 1 || ef.Symbols["AAPL"].How != sec.HowIssued {
			t.Errorf("%s: %+v", name, ef.Symbols)
		}
	}
	// Over the size cap: the same, read no further than the cap (Codex review of PR #23).
	defer func(n int64) { maxSearchFile = n }(maxSearchFile)
	maxSearchFile = int64(len(good)) - 1
	esppSearchPath = searchFile(good)
	out = filepath.Join(t.TempDir(), "sec")
	if err := run(zpath, tpath, rpath, spath, out, now); err != nil {
		t.Fatal(err)
	}
	if ef := read(out); len(ef.Symbols) != 1 {
		t.Errorf("oversized: %+v", ef.Symbols)
	}
	maxSearchFile = 64 << 20
	// No file at all: the same.
	esppSearchPath = filepath.Join(t.TempDir(), "missing.json")
	out = filepath.Join(t.TempDir(), "sec")
	if err := run(zpath, tpath, rpath, spath, out, now); err != nil {
		t.Fatal(err)
	}
	if ef := read(out); len(ef.Symbols) != 1 {
		t.Errorf("missing: %+v", ef.Symbols)
	}
}
