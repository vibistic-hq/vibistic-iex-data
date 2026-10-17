// Package og draws the preview image of a stock for Vibistic Stocks share
// links (stocks.vibistic.com/s/{SYMBOL}): v1/og/{SYMBOL}.png, 1200x630, from
// the same published bars the app downloads. Its numbers are the app's own:
// testdata/golden.json is written by the app's TypeScript engine
// (vibistic-stocks scripts/og-golden.mts) and the tests here must match it.
// Only numbers that do not depend on a link's settings are shown.
package og

import (
	"fmt"
	"time"

	"github.com/vibistic-hq/vibistic-iex-data/internal/series"
)

// HistoryCalendarDays is the app's four-year window (src/market/load.ts).
const HistoryCalendarDays = 1461

// Bar is one published session.
type Bar struct {
	Date       string
	O, H, L, C float64
}

// Card is what the image shows. Nil fields are a dash.
type Card struct {
	Symbol    string
	AsOf      string // the date of the last bar, not of the archive
	Close     float64
	PrevClose *float64
	Change    *float64 // percent from the previous close
	Low52     *float64
	High52    *float64
	SMA50     *float64
	SMA200    *float64
	Vs50      *float64 // percent from the 50 SMA
	Vs200     *float64
	// The chart: the last six months of closes and the 50 SMA as it stood each day.
	Chart []ChartPoint
}

type ChartPoint struct {
	Date  string
	Close float64
	SMA50 *float64
}

// Bars turns a file's bars into dated sessions.
func Bars(f *series.File) []Bar {
	out := make([]Bar, 0, len(f.Bars))
	for _, b := range f.Bars {
		n := int(b[0])
		out = append(out, Bar{Date: fmt.Sprintf("%04d-%02d-%02d", n/10000, n/100%100, n%100), O: b[1], H: b[2], L: b[3], C: b[4]})
	}
	return out
}

// NewCard computes the card the way the app computes a row (src/engine/row.ts
// computeRow with src/iex/facts.ts iexRowOf). `now` is the build time: it only
// decides whether a short history counts as a recent listing.
func NewCard(f *series.File, now time.Time) (*Card, error) {
	if err := Accepted(f); err != nil {
		return nil, fmt.Errorf("%s: the app would reject this file: %w", f.Symbol, err)
	}
	bars := Bars(f)
	last := bars[len(bars)-1]
	closes := make([]float64, len(bars))
	for i, b := range bars {
		closes[i] = b.C
	}
	c := &Card{Symbol: f.Symbol, AsOf: last.Date, Close: last.C}
	if len(bars) > 1 {
		p := bars[len(bars)-2].C
		c.PrevClose = &p
		c.Change = distance(last.C, p)
	}
	c.SMA50 = sma(closes, 50)
	c.SMA200 = sma(closes, 200)
	if c.SMA50 != nil {
		c.Vs50 = distance(last.C, *c.SMA50)
	}
	if c.SMA200 != nil {
		c.Vs200 = distance(last.C, *c.SMA200)
	}
	listed := startsAtListing(bars[0].Date, now) || len(f.Breaks) > 0
	c.Low52, c.High52 = range52(bars, listed)
	from := addDays(last.Date, -183)
	for i, b := range bars {
		if b.Date < from {
			continue
		}
		c.Chart = append(c.Chart, ChartPoint{Date: b.Date, Close: b.C, SMA50: sma(closes[:i+1], 50)})
	}
	return c, nil
}

// sma: the simple average of the last n values, summed in the same order as
// the app's (src/engine/indicators.ts), so the result is the same float.
func sma(values []float64, n int) *float64 {
	if len(values) < n {
		return nil
	}
	sum := 0.0
	for i := len(values) - n; i < len(values); i++ {
		sum += values[i]
	}
	v := sum / float64(n)
	return &v
}

// distance: how far price is above (+) or below (-) level, in percent. The
// float64 conversions keep each step rounded as in JavaScript (no fused
// multiply-add).
func distance(price, level float64) *float64 {
	if !(level > 0) {
		return nil
	}
	v := float64(float64(price/level)-1) * 100
	return &v
}

// range52: the lowest low and highest high of the 365 days up to the last bar,
// that day included; none when the history does not reach back a year and is
// not a recent listing (the real extremes could be older).
func range52(bars []Bar, listed bool) (*float64, *float64) {
	from := addDays(bars[len(bars)-1].Date, -365)
	if bars[0].Date > from && !listed {
		return nil, nil
	}
	lo, hi := -1, -1
	for i, b := range bars {
		if b.Date < from {
			continue
		}
		if lo < 0 || b.L <= bars[lo].L {
			lo = i
		}
		if hi < 0 || b.H >= bars[hi].H {
			hi = i
		}
	}
	if lo < 0 {
		return nil, nil
	}
	l, h := bars[lo].L, bars[hi].H
	return &l, &h
}

// startsAtListing: the history starts more than a week after the app's
// four-year window does (src/market/load.ts), so there was nothing earlier.
func startsAtListing(first string, now time.Time) bool {
	return first > addDays(addDays(nyDate(now), -HistoryCalendarDays), 7)
}

var newYork = func() *time.Location {
	l, err := time.LoadLocation("America/New_York")
	if err != nil {
		panic(err)
	}
	return l
}()

func nyDate(t time.Time) string { return t.In(newYork).Format("2006-01-02") }

func addDays(date string, days int) string {
	t, err := time.Parse("2006-01-02", date)
	if err != nil {
		panic(err)
	}
	return t.AddDate(0, 0, days).Format("2006-01-02")
}
