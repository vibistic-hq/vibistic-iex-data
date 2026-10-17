package og

import (
	"bytes"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"testing"
)

func TestRenderEveryFixture(t *testing.T) {
	now, _ := readGolden(t)
	paths, _ := filepath.Glob("testdata/*.json")
	out := os.Getenv("OG_PREVIEW_DIR") // set to look at the images
	for _, p := range paths {
		if filepath.Base(p) == "golden.json" {
			continue
		}
		f := readFixture(t, p)
		card, err := NewCard(f, now)
		if Accepted(f) != nil {
			continue // refused, no image (TestCardsMatchTheAppsEngine)
		}
		if err != nil {
			t.Fatal(err)
		}
		b, err := Render(card)
		if err != nil {
			t.Fatal(err)
		}
		img, err := png.Decode(bytes.NewReader(b))
		if err != nil {
			t.Fatal(err)
		}
		if img.Bounds().Dx() != Width || img.Bounds().Dy() != Height {
			t.Fatalf("%s: %v", f.Symbol, img.Bounds())
		}
		if len(b) > MaxBytes {
			t.Fatalf("%s: %d bytes", f.Symbol, len(b))
		}
		again, _ := Render(card)
		if !bytes.Equal(b, again) {
			t.Fatalf("%s: not deterministic", f.Symbol)
		}
		if out != "" {
			os.WriteFile(filepath.Join(out, f.Symbol+".png"), b, 0o644)
		}
	}
}

// Every text sits in its own place: inside the image, and either clear of
// the chart or wholly inside it (its own labels), whatever the symbol and
// the size of the numbers.
func TestLayoutFitsLongSymbolsAndHugeNumbers(t *testing.T) {
	big := func(x float64) *float64 { return &x }
	chart := image.Rect(ChartLeft, 56, 1140, 468)
	points := []ChartPoint{{Date: "2026-04-16", Close: 987654321, SMA50: big(900000000)}, {Date: "2026-10-16", Close: 1234, SMA50: big(950000000)}}
	for _, card := range []*Card{
		{Symbol: "WWWWWW.WW", AsOf: "2026-10-16", Close: 987654321.5, Change: big(-99.94), Low52: big(123456789), High52: big(999999999), SMA50: big(987654321), Vs50: big(-123.4), SMA200: big(987654321), Vs200: big(9999.9), Chart: points},
		{Symbol: "MMMMMM", AsOf: "2026-10-16", Close: 0.0001, Change: big(1234.5), Chart: points[:1]},
		{Symbol: "A", AsOf: "2026-10-16", Close: 10},
	} {
		_, boxes, err := render(card)
		if err != nil {
			t.Fatal(err)
		}
		for _, b := range boxes {
			if !b.In(image.Rect(0, 0, Width, Height)) {
				t.Errorf("%s: text %v leaves the image", card.Symbol, b)
			}
			if b.Overlaps(chart) && !b.In(chart) {
				t.Errorf("%s: text %v runs into the chart %v", card.Symbol, b, chart)
			}
		}
	}
}
