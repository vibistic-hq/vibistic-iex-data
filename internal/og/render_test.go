package og

import (
	"bytes"
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
