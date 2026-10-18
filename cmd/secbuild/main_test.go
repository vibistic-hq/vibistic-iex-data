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
func fixture(t *testing.T) (string, string, string) {
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
		w.Write(b)
	}
	zw.Close()
	zf.Close()
	tpath := filepath.Join(tmp, "tickers.json")
	os.WriteFile(tpath, []byte(`{"fields":["cik","name","ticker","exchange"],"data":[
		[320193,"Apple Inc.","AAPL","Nasdaq"],[1045810,"NVIDIA CORP","NVDA","Nasdaq"],
		[1652044,"Alphabet Inc.","GOOGL","Nasdaq"],[1652044,"Alphabet Inc.","GOOG","Nasdaq"],
		[99,"Broken","BRKN","NYSE"],[77,"Mislabeled","MISL","NYSE"]]}`), 0o644)
	rpath := filepath.Join(tmp, "reviewed.json")
	os.WriteFile(rpath, []byte(`[{"symbol":"GOOG","cik":1652044}]`), 0o644)
	return zpath, tpath, rpath
}

func TestSecbuild(t *testing.T) {
	defer func(n int, m []string) { minFiles, mustHave = n, m }(minFiles, mustHave)
	minFiles, mustHave = 4, []string{"AAPL", "NVDA"}
	zpath, tpath, rpath := fixture(t)
	out := filepath.Join(t.TempDir(), "sec")
	if err := run(zpath, tpath, rpath, out, now); err != nil {
		t.Fatal(err)
	}
	names, _ := filepath.Glob(filepath.Join(out, "*.json"))
	var got []string
	for _, n := range names {
		got = append(got, filepath.Base(n))
	}
	// Both Alphabet classes (GOOG reviewed); not the broken entry, nor a file whose CIK is not its name's.
	if strings.Join(got, ",") != "AAPL.json,GOOG.json,GOOGL.json,NVDA.json,snapshot.json" {
		t.Fatalf("files %v", got)
	}
	b, _ := os.ReadFile(filepath.Join(out, "GOOG.json"))
	var f sec.File
	if err := json.Unmarshal(b, &f); err != nil || f.Symbol != "GOOG" || f.CIK != 1652044 || sec.Validate(&f) != nil {
		t.Fatalf("GOOG.json: %v %+v", err, f)
	}
	s, _ := os.ReadFile(filepath.Join(out, "snapshot.json"))
	if string(s) != `{"files":4,"generatedAt":"2026-10-18T09:00:00Z","source":"SEC","v":1}` {
		t.Fatalf("snapshot %s", s)
	}
	if err := run(zpath, tpath, rpath, out, now); err == nil {
		t.Fatal("overwrote an existing directory")
	}
}

func TestSecbuildReviewedFile(t *testing.T) {
	zpath, tpath, _ := fixture(t)
	bad := filepath.Join(t.TempDir(), "reviewed.json")
	for _, doc := range []string{`[{"symbol":"GOOG","cik":1652044,"note":"x"}]`, `[{"symbol":"goog","cik":1652044}]`, `[{"symbol":"GOOG","cik":0}]`, `{`} {
		os.WriteFile(bad, []byte(doc), 0o644)
		if err := run(zpath, tpath, bad, filepath.Join(t.TempDir(), "sec"), now); err == nil {
			t.Errorf("%s accepted", doc)
		}
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
	zpath, tpath, rpath := fixture(t)
	for name, set := range map[string]func(){
		"too few":   func() { minFiles, mustHave = 5, []string{"AAPL"} },
		"missing":   func() { minFiles, mustHave = 1, []string{"MSFT"} },
		"not fresh": func() { minFiles, mustHave = 1, []string{"AAPL"}; now = now.AddDate(1, 0, 0) },
	} {
		before := now
		set()
		if err := run(zpath, tpath, rpath, filepath.Join(t.TempDir(), "sec"), now); err == nil {
			t.Errorf("%s: accepted", name)
		}
		now = before
	}
}
