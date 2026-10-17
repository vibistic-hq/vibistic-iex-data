package og

import (
	"math"
	"strconv"
	"strings"
	"time"
)

// Formatting as the app's table does (src/lib/format.ts), which uses
// JavaScript's toLocaleString('en-US'): the float's shortest decimal form
// (what JavaScript prints for it) rounded half away from zero, grouped by
// thousands. Not its exact binary value: 164.285 is stored as
// 164.28499999..., yet the app shows 164.29.

// CellPrice: four places under $1, whole dollars from $1,000, else cents.
func CellPrice(v *float64) string {
	if v == nil || math.IsNaN(*v) || math.IsInf(*v, 0) {
		return "-"
	}
	a := math.Abs(*v)
	digits := 2
	if a < 1 {
		digits = 4
	} else if a >= 1000 {
		digits = 0
	}
	return group(roundHalfAway(*v, digits))
}

// Percent: one place, a + or U+2212 minus sign, "0.0%" for zero.
func Percent(v *float64) string {
	if v == nil || math.IsNaN(*v) || math.IsInf(*v, 0) {
		return "-"
	}
	// JavaScript's Math.round(n * 10) / 10: halves round up (toward +infinity).
	r := math.Floor(float64(*v*10)+0.5) / 10
	if r == 0 {
		return "0.0%"
	}
	sign := "+"
	if r < 0 {
		sign = "−"
	}
	return sign + roundHalfAway(math.Abs(r), 1) + "%"
}

// Date: "Oct 16, 2026".
func Date(d string) string {
	t, err := time.Parse("2006-01-02", d)
	if err != nil {
		return d
	}
	return t.Format("Jan 2, 2006")
}

// roundHalfAway formats x with `digits` places, rounding its shortest
// decimal form half away from zero.
func roundHalfAway(x float64, digits int) string {
	neg := x < 0
	shortest := strconv.FormatFloat(math.Abs(x), 'f', -1, 64)
	intPart, frac, _ := strings.Cut(shortest, ".")
	frac += strings.Repeat("0", digits+1)
	keep := []byte(intPart + frac[:digits])
	if frac[digits] >= '5' {
		i := len(keep) - 1
		for ; i >= 0; i-- {
			if keep[i] == '9' {
				keep[i] = '0'
				continue
			}
			keep[i]++
			break
		}
		if i < 0 {
			keep = append([]byte{'1'}, keep...)
		}
	}
	s := string(keep)
	whole, fracOut := s[:len(s)-digits], s[len(s)-digits:]
	whole = strings.TrimLeft(whole, "0")
	if whole == "" {
		whole = "0"
	}
	out := whole
	if digits > 0 {
		out += "." + fracOut
	}
	if neg && strings.Trim(out, "0.") != "" {
		out = "-" + out
	}
	return out
}

// group puts commas between thousands of the whole part.
func group(s string) string {
	sign := ""
	if strings.HasPrefix(s, "-") {
		sign, s = "-", s[1:]
	}
	whole, frac, hasFrac := strings.Cut(s, ".")
	var b strings.Builder
	for i, ch := range whole {
		if i > 0 && (len(whole)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(ch)
	}
	out := sign + b.String()
	if hasFrac {
		out += "." + frac
	}
	return out
}
