package sec

import (
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"strings"
)

// Class is a reviewed extra share class (sec-reviewed.json, by pull request
// only): a common stock of the company besides the ticker SEC lists first for
// it, bound to that company's CIK so a reused ticker cannot inherit it.
type Class struct {
	Symbol string `json:"symbol"`
	CIK    int64  `json:"cik"`
}

// ValidateClasses checks a reviewed list.
func ValidateClasses(cs []Class) error {
	seen := map[string]bool{}
	for i, c := range cs {
		if !symbolRe.MatchString(c.Symbol) || c.CIK < 1 || c.CIK > 9999999999 || seen[c.Symbol] {
			return fmt.Errorf("entry %d: %+v", i, c)
		}
		seen[c.Symbol] = true
	}
	return nil
}

// Funds and trusts that file 10-Qs (gold, oil, crypto and volatility
// products) have no business revenue; their "net income" is the change in
// the value of what they hold. Matched on the name SEC lists, which is used
// for nothing else and never published.
var fundRe = regexp.MustCompile(`(?i)\b(ETF|ETFs|Fund|Funds|Fund,? LP|Trust II|Commodity|ProShares|iShares|SPDR|Invesco DB|Grayscale|Teucrium)\b|\b(Bitcoin|Ether|Ethereum|Solana|XRP|Dogecoin|Litecoin|Avalanche|Chainlink|Stellar|Sui|Bittensor|Horizen|Zcash|Crypto|Gold|Silver|Platinum|Palladium|Precious Metals)\b.*\b(Trust|ETF)\b`)

// Tickers reads SEC's company_tickers_exchange.json ({"fields": [...],
// "data": [[cik, name, ticker, exchange], ...]}) into the app's symbols and
// their CIK. For each company only the ticker SEC lists first (its main
// common stock; the others are preferreds, notes, warrants, units and other
// share classes) and the reviewed classes, shaped like the app's symbols
// (AAPL, BRK.B; SEC writes BRK-B). Funds and trusts are left out, and a
// symbol under two companies is dropped.
func Tickers(r io.Reader, reviewed []Class) (map[string]int64, error) {
	if err := ValidateClasses(reviewed); err != nil {
		return nil, err
	}
	var raw struct {
		Fields []string            `json:"fields"`
		Data   [][]json.RawMessage `json:"data"`
	}
	if err := json.NewDecoder(r).Decode(&raw); err != nil {
		return nil, err
	}
	if strings.Join(raw.Fields, ",") != "cik,name,ticker,exchange" {
		return nil, fmt.Errorf("unexpected fields %v", raw.Fields)
	}
	extra := map[string]int64{}
	for _, c := range reviewed {
		extra[c.Symbol] = c.CIK
	}
	out := map[string]int64{}
	twice := map[string]bool{}
	first := map[int64]bool{}
	for i, row := range raw.Data {
		if len(row) != 4 {
			return nil, fmt.Errorf("row %d: %d fields", i, len(row))
		}
		var cik int64
		var name, ticker string
		if err := json.Unmarshal(row[0], &cik); err != nil || cik < 1 || cik > 9999999999 {
			return nil, fmt.Errorf("row %d: cik %s", i, row[0])
		}
		if err := json.Unmarshal(row[2], &ticker); err != nil {
			return nil, fmt.Errorf("row %d: ticker %s", i, row[2])
		}
		json.Unmarshal(row[1], &name) // a missing name only means no fund check
		sym := strings.ReplaceAll(strings.ToUpper(ticker), "-", ".")
		primary := !first[cik]
		first[cik] = true
		if !primary && extra[sym] != cik {
			continue
		}
		if !symbolRe.MatchString(sym) || instrument(sym) || fundRe.MatchString(name) {
			continue
		}
		if prev, ok := out[sym]; ok && prev != cik {
			twice[sym] = true
		}
		out[sym] = cik
	}
	for t := range twice {
		delete(out, t)
	}
	return out, nil
}

// instrument tells a warrant, unit or right listing by its ticker: a .W, .U
// or .R class, or a 5-letter Nasdaq ticker ending in W, U or R.
func instrument(sym string) bool {
	if i := strings.IndexByte(sym, '.'); i >= 0 {
		return strings.ContainsAny(sym[i+1:], "WUR")
	}
	return len(sym) == 5 && strings.ContainsAny(sym[4:], "WUR")
}
