package og

import (
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/vibistic-hq/vibistic-iex-data/internal/series"
)

// maxVolume: the app's MAX_VOLUME.
const maxVolume = series.MaxVolume

// Accepted mirrors the app's parseIexFile (vibistic-stocks src/iex/data.ts):
// the app rejects a whole file over any of these, so a file it would refuse
// gets no image either (its share page shows no numbers). Rounding to four
// places can turn a split-adjusted price of a hundredth of a cent into 0,
// for one.
func Accepted(f *series.File) error {
	if f.V != 1 || f.Source != "IEX" || !series.SymbolRE.MatchString(f.Symbol) || !isDate(f.AsOf) {
		return errors.New("header")
	}
	m := f.Metrics
	if m.Sessions < 0 || m.Sessions > 60 || m.PricedSessions < 0 || m.PricedSessions > 60 || m.PricedSessions > m.Sessions {
		return errors.New("metrics: sessions")
	}
	if math.IsNaN(m.MedianNotional) || math.IsInf(m.MedianNotional, 0) || m.MedianNotional < 0 {
		return errors.New("metrics: median notional")
	}
	if m.LastPricedDate != "" && (!isDate(m.LastPricedDate) || m.LastPricedDate > f.AsOf) {
		return errors.New("metrics: last priced date")
	}
	if f.Coverage != "ok" {
		return errors.New("not covered")
	}
	if len(f.Bars) == 0 || len(f.Bars) > 2000 || len(f.Splits) > 200 || len(f.Breaks) > 200 {
		return errors.New("counts")
	}
	prev := ""
	for i, b := range f.Bars {
		d, ok := dateOf(b[0])
		if !ok {
			return fmt.Errorf("bar %d: date", i)
		}
		o, h, l, c, v, vw := b[1], b[2], b[3], b[4], b[5], b[6]
		if !positive(o) || !positive(h) || !positive(l) || !positive(c) || !positive(vw) {
			return fmt.Errorf("bar %d (%s): a price is not positive", i, d)
		}
		if math.IsNaN(v) || math.IsInf(v, 0) || v <= 0 || v > maxVolume {
			return fmt.Errorf("bar %d (%s): volume", i, d)
		}
		if h < l || h < math.Max(o, c)*0.999999 || l > math.Min(o, c)*1.000001 {
			return fmt.Errorf("bar %d (%s): range", i, d)
		}
		if prev != "" && d <= prev {
			return fmt.Errorf("bar %d (%s): out of order", i, d)
		}
		if d > f.AsOf {
			return fmt.Errorf("bar %d (%s): after asOf", i, d)
		}
		prev = d
	}
	for _, s := range f.Splits {
		if !isDate(s.Date) || s.New < 1 || s.Old < 1 || s.New > 1000 || s.Old > 1000 || s.New == s.Old || (s.How != "detected" && s.How != "reviewed") || s.Date > f.AsOf {
			return fmt.Errorf("split %s", s.Date)
		}
	}
	for _, b := range f.Breaks {
		ok := isDate(b.Date) && b.Date <= f.AsOf
		switch b.Kind {
		case "quiet":
			ok = ok && b.Sessions > 0 && b.Sessions < 100000
		case "jump":
			ok = ok && !math.IsNaN(b.Move) && !math.IsInf(b.Move, 0) && b.Move > -1 && b.Move < 1000
		case "reviewed":
		default:
			ok = false
		}
		if !ok {
			return fmt.Errorf("break %s", b.Date)
		}
	}
	return nil
}

func positive(v float64) bool { return series.UsablePrice(v) }

func isDate(s string) bool {
	if len(s) != 10 {
		return false
	}
	t, err := time.Parse("2006-01-02", s)
	return err == nil && t.Format("2006-01-02") == s
}

func dateOf(n float64) (string, bool) {
	if n != math.Trunc(n) || n < 19000101 || n > 21001231 {
		return "", false
	}
	i := int(n)
	d := fmt.Sprintf("%04d-%02d-%02d", i/10000, i/100%100, i%100)
	return d, isDate(d)
}
