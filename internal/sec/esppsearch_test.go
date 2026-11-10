package sec

import (
	"strconv"
	"strings"
	"testing"
	"time"
)

func searchAnswer(total int, relation string, hits ...string) string {
	return `{"hits":{"total":{"value":` + strconv.Itoa(total) + `,"relation":"` + relation + `"},"hits":[` + strings.Join(hits, ",") + `]}}`
}

func searchHit(ciks, form, fileType, date, adsh string) string {
	return `{"_id":"x","_source":{"ciks":[` + ciks + `],"form":"` + form + `","file_type":"` + fileType + `","file_date":"` + date + `","adsh":"` + adsh + `"}}`
}

func TestParseSearchPage(t *testing.T) {
	page, err := ParseSearchPage(strings.NewReader(searchAnswer(5, "eq",
		searchHit(`"0000320193"`, "10-K", "10-K", "2026-11-03", "0000320193-26-000026"),
		// Unreadable hits are skipped, but still counted for paging.
		searchHit(`"x"`, "10-K", "10-K", "2026-11-03", "0000320193-26-000026"),
		searchHit(`"0000320193"`, "10-K", "10-K", "2026-02-30", "0000320193-26-000026"),
		searchHit(`"0000320193"`, "10-K", "10-K", "2026-11-03", "bad"),
		searchHit(``, "10-K", "10-K", "2026-11-03", "0000320193-26-000026"),
	)))
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 5 || page.Returned != 5 || len(page.Hits) != 1 || page.Hits[0].CIKs[0] != 320193 || page.Hits[0].Accn != "0000320193-26-000026" {
		t.Fatalf("page %+v", page)
	}
	// A cut-off count, a negative one, malformed JSON or a huge answer is an error.
	for name, body := range map[string]string{
		"gte":       searchAnswer(10000, "gte"),
		"negative":  searchAnswer(-1, "eq"),
		"malformed": `{"hits":`,
		"huge":      `{"x":"` + strings.Repeat("a", MaxSearchPage) + `"}`,
	} {
		if _, err := ParseSearchPage(strings.NewReader(body)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestSearchFileAddKeepsMainDocumentsInWindowNewestFirst(t *testing.T) {
	now := time.Date(2026, 11, 10, 9, 0, 0, 0, time.UTC)
	f := &SearchFile{V: 1, GeneratedAt: now.Format(time.RFC3339), TenK: map[string]SearchHit{}, S8: map[string]SearchHit{}}
	add := func(cik int64, form, fileType, filed, accn string) {
		f.Add(PageHit{CIKs: []int64{cik}, Form: form, FileType: fileType, Filed: filed, Accn: accn}, now)
	}
	add(1, "10-K", "10-K", "2026-02-01", "0000000001-26-000001")
	add(1, "10-K", "10-K", "2026-03-01", "0000000001-26-000002")
	add(1, "10-K", "10-K", "2026-01-01", "0000000001-26-000003") // older: not kept
	// An exhibit of a 10-K, another form, too old, or in the future: never evidence.
	add(2, "10-K", "EX-10.1", "2026-03-01", "0000000002-26-000001")
	add(3, "10-Q", "10-Q", "2026-03-01", "0000000003-26-000001")
	add(4, "10-K", "10-K", "2025-10-05", "0000000004-25-000001")
	add(5, "10-K", "10-K", "2026-11-11", "0000000005-26-000001")
	add(6, "S-8", "S-8", "2023-11-10", "0000000006-23-000001")
	add(7, "S-8", "S-8", "2023-11-01", "0000000007-23-000001")
	add(8, "S-8", "EX-99.1", "2026-03-01", "0000000008-26-000001")
	if len(f.TenK) != 1 || f.TenK["1"].Accn != "0000000001-26-000002" || len(f.S8) != 1 || f.S8["6"].Filed != "2023-11-10" {
		t.Fatalf("tenK %+v s8 %+v", f.TenK, f.S8)
	}
	if err := ValidateSearch(f, now); err != nil {
		t.Fatal(err)
	}
	// Best: the 10-K first, else the S-8, else none (and a nil file has none).
	f.S8["1"] = SearchHit{Filed: "2026-01-01", Accn: "0000000001-26-000009"}
	if h, how, ok := f.Best(1); !ok || how != HowTenK || h.Accn != "0000000001-26-000002" {
		t.Fatalf("best 1: %+v %s", h, how)
	}
	if _, how, ok := f.Best(6); !ok || how != HowS8 {
		t.Fatalf("best 6: %s", how)
	}
	if _, _, ok := f.Best(99); ok {
		t.Fatal("best 99")
	}
	var none *SearchFile
	if _, _, ok := none.Best(1); ok {
		t.Fatal("nil file")
	}
}

func TestValidateSearch(t *testing.T) {
	now := time.Date(2026, 11, 10, 9, 0, 0, 0, time.UTC)
	good := func() *SearchFile {
		return &SearchFile{V: 1, GeneratedAt: "2026-11-10T08:00:00Z",
			TenK: map[string]SearchHit{"320193": {Filed: "2026-11-03", Accn: "0000320193-26-000026"}},
			S8:   map[string]SearchHit{"50863": {Filed: "2026-07-24", Accn: "0000050863-26-000100"}}}
	}
	if err := ValidateSearch(good(), now); err != nil {
		t.Fatal(err)
	}
	for name, edit := range map[string]func(*SearchFile){
		"version":     func(f *SearchFile) { f.V = 2 },
		"stale":       func(f *SearchFile) { f.GeneratedAt = "2026-11-06T08:00:00Z" },
		"future":      func(f *SearchFile) { f.GeneratedAt = "2026-11-10T11:00:00Z" },
		"cik":         func(f *SearchFile) { f.TenK["0320193"] = f.TenK["320193"] },
		"cik letters": func(f *SearchFile) { f.TenK["abc"] = f.TenK["320193"] },
		"accn":        func(f *SearchFile) { f.TenK["1"] = SearchHit{Filed: "2026-11-03", Accn: "x"} },
		"old 10-K":    func(f *SearchFile) { f.TenK["1"] = SearchHit{Filed: "2025-10-01", Accn: "0000000001-25-000001"} },
		"old S-8":     func(f *SearchFile) { f.S8["1"] = SearchHit{Filed: "2023-11-01", Accn: "0000000001-23-000001"} },
		"after file":  func(f *SearchFile) { f.S8["1"] = SearchHit{Filed: "2026-11-11", Accn: "0000000001-26-000001"} },
	} {
		f := good()
		edit(f)
		if err := ValidateSearch(f, now); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestExtractEsppFallsBackToSearchEvidence(t *testing.T) {
	search := &SearchFile{V: 1, GeneratedAt: "2026-11-10T08:00:00Z",
		TenK: map[string]SearchHit{"789019": {Filed: "2026-07-29", Accn: accnK}},
		S8:   map[string]SearchHit{"789019": {Filed: "2026-01-01", Accn: accnQ}, "5": {Filed: "2025-01-01", Accn: accnQ}}}
	// No issuance: the 10-K.
	got, err := ExtractEspp(esppFacts(msftCIK, nil, nil), msftCIK, esppNow, search)
	if err != nil || got == nil || *got != (Espp{CIK: msftCIK, Filed: "2026-07-29", Accn: accnK, How: HowTenK}) {
		t.Fatalf("10-K: %+v, %v", got, err)
	}
	// Only an S-8.
	got, _ = ExtractEspp(esppFacts(5, nil, nil), 5, esppNow, search)
	if got == nil || got.How != HowS8 {
		t.Fatalf("S-8: %+v", got)
	}
	// Issuance wins over both.
	issued := map[string][2]string{esppSharesConcept: {"shares", "[" + esppPeriod("2025-07-01", "2026-06-30", 5, accnQ, "10-K", "2026-08-01") + "]"}}
	got, _ = ExtractEspp(esppFacts(msftCIK, issued, nil), msftCIK, esppNow, search)
	if got == nil || got.How != HowIssued || got.Filed != "2026-08-01" {
		t.Fatalf("issued: %+v", got)
	}
	// No public market for the common stock: not listed, whatever the search found.
	zero := func(accn, filed string) string {
		return `{"end":"2025-12-31","val":0,"accn":"` + accn + `","form":"10-K","filed":"` + filed + `"}`
	}
	none := map[string][2]string{"EntityPublicFloat": {"USD", "[" + zero("0000000001-25-000001", "2025-03-01") + "," + zero("0000000001-26-000001", "2026-03-01") + "]"}}
	if got, _ := ExtractEspp(esppFacts(msftCIK, nil, none), msftCIK, esppNow, search); got != nil {
		t.Fatalf("no float: %+v", got)
	}
}

func TestValidateEsppChecksEachKindsAge(t *testing.T) {
	file := func(e Espp) *EsppFile {
		return &EsppFile{V: 1, Source: "SEC", GeneratedAt: "2026-11-10T00:00:00Z", Symbols: map[string]Espp{"MSFT": e}}
	}
	e := func(filed, how string) Espp { return Espp{CIK: msftCIK, Filed: filed, Accn: accnK, How: how} }
	for _, c := range []struct {
		e  Espp
		ok bool
	}{
		{e("2025-10-06", HowTenK), true}, {e("2025-10-05", HowTenK), false},
		{e("2023-11-06", HowS8), true}, {e("2023-11-05", HowS8), false},
		{e("2024-11-10", HowIssued), true}, {e("2024-11-09", HowIssued), false},
		{e("2026-01-01", ""), false}, {e("2026-01-01", "10-Q"), false},
	} {
		if err := ValidateEspp(file(c.e)); (err == nil) != c.ok {
			t.Errorf("%+v: %v", c.e, err)
		}
	}
}
