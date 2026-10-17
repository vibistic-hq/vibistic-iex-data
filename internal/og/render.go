package og

import (
	"bytes"
	_ "embed"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"math"
	"strings"
	"sync"

	"golang.org/x/image/font"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/math/fixed"
	"golang.org/x/image/vector"

	"github.com/vibistic-hq/vibistic-iex-data/internal/series"
)

const (
	Width  = 1200
	Height = 630
	// The left column's width: up to 20 px before the chart.
	LeftWidth  = ChartLeft - 64 - 20
	ChartLeft  = 640
	statColumn = 270
	// MaxBytes: an image larger than this fails the build (social sites allow
	// 5 MB; the site's 1 GB Pages budget is the real limit).
	MaxBytes = 300 * 1024
)

// Montserrat (SIL Open Font License, fonts/OFL.txt), as on stocks.vibistic.com.
var (
	//go:embed fonts/Montserrat-Regular.ttf
	regularTTF []byte
	//go:embed fonts/Montserrat-SemiBold.ttf
	semiBoldTTF []byte
	//go:embed fonts/Montserrat-Bold.ttf
	boldTTF []byte
)

// The app's light palette (src/styles/global.css).
var (
	bg      = color.RGBA{0xef, 0xfc, 0xe8, 0xff} // --app-card-bg
	ink     = color.RGBA{0x1b, 0x3d, 0x0b, 0xff} // --app-ink
	dim     = color.RGBA{0x4b, 0x6b, 0x3a, 0xff}
	accent  = color.RGBA{0x37, 0x79, 0x15, 0xff} // --app-accent
	accent2 = color.RGBA{0x36, 0x1f, 0xad, 0xff} // --app-accent-2
	up      = color.RGBA{0x1f, 0x7a, 0x52, 0xff} // --color-success
	down    = color.RGBA{0xa1, 0x34, 0x1f, 0xff} // --color-danger
	rule    = color.RGBA{0xc9, 0xe3, 0xbb, 0xff}
	panel   = color.RGBA{0xff, 0xff, 0xff, 0xff}
)

// TermsAddress: the terms link, printed (an image cannot carry a link; the
// share page it opens carries the clickable one).
const TermsAddress = "iextrading.com/iex-historical-data-terms"

// Disclaimer is printed on every image.
const Disclaimer = "IEX trades only, end of day. A reference point only, not a basis for trading decisions. Not investment advice."

type faces struct{ regular, semiBold, bold *opentype.Font }

var loadFaces = sync.OnceValues(func() (faces, error) {
	var f faces
	var err error
	if f.regular, err = opentype.Parse(regularTTF); err != nil {
		return f, err
	}
	if f.semiBold, err = opentype.Parse(semiBoldTTF); err != nil {
		return f, err
	}
	f.bold, err = opentype.Parse(boldTTF)
	return f, err
})

type canvas struct {
	img   *image.RGBA
	faces faces
	cache map[string]font.Face
	// Every text drawn, for the layout tests.
	boxes []image.Rectangle
}

func (c *canvas) face(f *opentype.Font, name string, size float64) font.Face {
	key := fmt.Sprintf("%s/%.4f", name, size)
	if fc, ok := c.cache[key]; ok {
		return fc
	}
	fc, err := opentype.NewFace(f, &opentype.FaceOptions{Size: size, DPI: 72, Hinting: font.HintingNone})
	if err != nil {
		panic(err) // the embedded fonts parsed already
	}
	c.cache[key] = fc
	return fc
}

func (c *canvas) regular(size float64) font.Face  { return c.face(c.faces.regular, "r", size) }
func (c *canvas) semiBold(size float64) font.Face { return c.face(c.faces.semiBold, "s", size) }
func (c *canvas) bold(size float64) font.Face     { return c.face(c.faces.bold, "b", size) }

// text draws s with its baseline at y; returns the x after it.
func (c *canvas) text(face font.Face, col color.Color, x, y int, s string) int {
	d := &font.Drawer{Dst: c.img, Src: image.NewUniform(col), Face: face, Dot: fixed.P(x, y)}
	b, _ := d.BoundString(s)
	c.boxes = append(c.boxes, image.Rect(b.Min.X.Floor(), b.Min.Y.Floor(), b.Max.X.Ceil(), b.Max.Y.Ceil()))
	d.DrawString(s)
	return d.Dot.X.Round()
}

// fit: the largest size from `size` down (in steps of 2, not under `least`)
// at which `width` holds the texts measured together.
func fit(face func(float64) font.Face, size, least float64, width int, measureAt func(f func(float64) font.Face, size float64) int) float64 {
	for ; size > least; size -= 2 {
		if measureAt(face, size) <= width {
			return size
		}
	}
	return least
}

func measure(face font.Face, s string) int { return font.MeasureString(face, s).Round() }

// wrap splits s into lines no wider than width.
func wrap(face font.Face, s string, width int) []string {
	var lines []string
	line := ""
	for _, w := range strings.Fields(s) {
		next := w
		if line != "" {
			next = line + " " + w
		}
		if line != "" && measure(face, next) > width {
			lines = append(lines, line)
			line = w
			continue
		}
		line = next
	}
	if line != "" {
		lines = append(lines, line)
	}
	return lines
}

func (c *canvas) rect(col color.Color, r image.Rectangle) {
	draw.Draw(c.img, r, image.NewUniform(col), image.Point{}, draw.Src)
}

// polyline strokes the points with anti-aliased edges and round joins. Each
// segment (and each joint's disc) is filled on its own: drawn as one path,
// overlapping pieces of opposite winding would cancel out and leave gaps.
func (c *canvas) polyline(col color.Color, pts [][2]float32, width float32) {
	h := width / 2
	src := image.NewUniform(col)
	fill := func(poly [][2]float32) {
		minX, minY, maxX, maxY := float32(math.Inf(1)), float32(math.Inf(1)), float32(math.Inf(-1)), float32(math.Inf(-1))
		for _, p := range poly {
			minX, minY = min(minX, p[0]), min(minY, p[1])
			maxX, maxY = max(maxX, p[0]), max(maxY, p[1])
		}
		box := image.Rect(int(math.Floor(float64(minX))), int(math.Floor(float64(minY))), int(math.Ceil(float64(maxX)))+1, int(math.Ceil(float64(maxY)))+1).Intersect(c.img.Bounds())
		if box.Empty() {
			return
		}
		r := vector.NewRasterizer(box.Dx(), box.Dy())
		for i, p := range poly {
			x, y := p[0]-float32(box.Min.X), p[1]-float32(box.Min.Y)
			if i == 0 {
				r.MoveTo(x, y)
			} else {
				r.LineTo(x, y)
			}
		}
		r.ClosePath()
		r.Draw(c.img, box, src, image.Point{})
	}
	for i := 1; i < len(pts); i++ {
		a, b := pts[i-1], pts[i]
		dx, dy := b[0]-a[0], b[1]-a[1]
		l := float32(math.Hypot(float64(dx), float64(dy)))
		if l == 0 {
			continue
		}
		nx, ny := -dy/l*h, dx/l*h
		fill([][2]float32{{a[0] + nx, a[1] + ny}, {b[0] + nx, b[1] + ny}, {b[0] - nx, b[1] - ny}, {a[0] - nx, a[1] - ny}})
	}
	for _, p := range pts {
		const k = 12
		disc := make([][2]float32, 0, k)
		for j := 0; j < k; j++ {
			t := float64(j) / k * 2 * math.Pi
			disc = append(disc, [2]float32{p[0] + h*float32(math.Cos(t)), p[1] + h*float32(math.Sin(t))})
		}
		fill(disc)
	}
}

func signColor(v *float64) color.Color {
	switch {
	case v == nil:
		return dim
	case Percent(v) == "0.0%":
		return dim
	case *v > 0:
		return up
	default:
		return down
	}
}

// Render draws the card as a PNG.
func Render(card *Card) ([]byte, error) {
	b, _, err := render(card)
	return b, err
}

// render also returns where every text went.
func render(card *Card) ([]byte, []image.Rectangle, error) {
	fs, err := loadFaces()
	if err != nil {
		return nil, nil, err
	}
	c := &canvas{img: image.NewRGBA(image.Rect(0, 0, Width, Height)), faces: fs, cache: map[string]font.Face{}}
	c.rect(bg, c.img.Bounds())

	// Left: the stock and its numbers, each shrunk to fit its space (a long
	// symbol, a price in the millions) so nothing runs under the chart.
	const left = 64
	c.text(c.semiBold(26), accent, left, 74, "Vibistic Stocks")
	symSize := fit(c.bold, 84, 36, LeftWidth, func(f func(float64) font.Face, size float64) int { return measure(f(size), card.Symbol) })
	c.text(c.bold(symSize), ink, left, 168, card.Symbol)
	c.text(c.regular(24), dim, left, 210, "IEX close, "+Date(card.AsOf))
	price := card.Close
	priceText, changeText := CellPrice(&price), Percent(card.Change)
	priceSize := fit(c.bold, 64, 30, LeftWidth, func(f func(float64) font.Face, size float64) int {
		return measure(f(size), priceText) + 18 + measure(c.semiBold(size*34/64), changeText)
	})
	x := c.text(c.bold(priceSize), ink, left, 284, priceText)
	c.text(c.semiBold(priceSize*34/64), signColor(card.Change), x+18, 284, changeText)

	stat := func(col, row int, label, value string, extra *float64) {
		sx, sy := left+col*statColumn, 340+row*72
		c.text(c.regular(20), dim, sx, sy, label)
		pct := ""
		if extra != nil {
			pct = Percent(extra)
		}
		size := fit(c.semiBold, 28, 14, statColumn-20, func(f func(float64) font.Face, size float64) int {
			w := measure(f(size), value)
			if pct != "" {
				w += 10 + measure(f(size*22/28), pct)
			}
			return w
		})
		vx := c.text(c.semiBold(size), ink, sx, sy+34, value)
		if pct != "" {
			c.text(c.semiBold(size*22/28), signColor(extra), vx+10, sy+34, pct)
		}
	}
	stat(0, 0, "52-week low", CellPrice(card.Low52), nil)
	stat(1, 0, "52-week high", CellPrice(card.High52), nil)
	stat(0, 1, "50-day SMA", CellPrice(card.SMA50), card.Vs50)
	stat(1, 1, "200-day SMA", CellPrice(card.SMA200), card.Vs200)

	// Right: six months of closes and the 50 SMA.
	chart := image.Rect(ChartLeft, 56, 1140, 468)
	c.rect(panel, chart)
	c.text(c.regular(18), dim, chart.Min.X+16, chart.Min.Y+28, "Last 6 months: close")
	lx := c.text(c.regular(18), dim, chart.Min.X+16, chart.Min.Y+28, "Last 6 months: close ")
	c.rect(accent, image.Rect(lx, chart.Min.Y+18, lx+18, chart.Min.Y+22))
	c.text(c.regular(18), dim, lx+26, chart.Min.Y+28, "and 50-day SMA")
	lx2 := c.text(c.regular(18), dim, lx+26, chart.Min.Y+28, "and 50-day SMA ")
	c.rect(accent2, image.Rect(lx2, chart.Min.Y+18, lx2+18, chart.Min.Y+22))
	if len(card.Chart) > 1 {
		lo, hi := math.Inf(1), math.Inf(-1)
		for _, p := range card.Chart {
			lo, hi = math.Min(lo, p.Close), math.Max(hi, p.Close)
			if p.SMA50 != nil {
				lo, hi = math.Min(lo, *p.SMA50), math.Max(hi, *p.SMA50)
			}
		}
		if hi == lo {
			hi, lo = hi*1.01, lo*0.99
		}
		plot := image.Rect(chart.Min.X+16, chart.Min.Y+48, chart.Max.X-90, chart.Max.Y-36)
		px := func(i int) float32 {
			return float32(plot.Min.X) + float32(i)/float32(len(card.Chart)-1)*float32(plot.Dx())
		}
		py := func(v float64) float32 {
			return float32(plot.Max.Y) - float32((v-lo)/(hi-lo))*float32(plot.Dy())
		}
		c.rect(rule, image.Rect(plot.Min.X, plot.Min.Y, plot.Max.X, plot.Min.Y+1))
		c.rect(rule, image.Rect(plot.Min.X, plot.Max.Y, plot.Max.X, plot.Max.Y+1))
		top, bottom := hi, lo
		topText, bottomText := CellPrice(&top), CellPrice(&bottom)
		axisSize := fit(c.regular, 18, 10, chart.Max.X-plot.Max.X-14, func(f func(float64) font.Face, size float64) int {
			return max(measure(f(size), topText), measure(f(size), bottomText))
		})
		c.text(c.regular(axisSize), dim, plot.Max.X+10, plot.Min.Y+6, topText)
		c.text(c.regular(axisSize), dim, plot.Max.X+10, plot.Max.Y+6, bottomText)
		var smaPts [][2]float32
		closePts := make([][2]float32, 0, len(card.Chart))
		for i, p := range card.Chart {
			closePts = append(closePts, [2]float32{px(i), py(p.Close)})
			if p.SMA50 != nil {
				smaPts = append(smaPts, [2]float32{px(i), py(*p.SMA50)})
			}
		}
		c.polyline(accent2, smaPts, 3)
		c.polyline(accent, closePts, 4)
		c.text(c.regular(18), dim, plot.Min.X, chart.Max.Y-10, Date(card.Chart[0].Date))
		end := Date(card.Chart[len(card.Chart)-1].Date)
		c.text(c.regular(18), dim, plot.Max.X-measure(c.regular(18), end), chart.Max.Y-10, end)
	} else {
		msg := "Not enough IEX history for a chart yet"
		c.text(c.regular(22), dim, chart.Min.X+(chart.Dx()-measure(c.regular(22), msg))/2, chart.Min.Y+chart.Dy()/2, msg)
	}

	// Bottom: IEX's citation (with the terms address) and what the numbers are.
	c.rect(rule, image.Rect(left, 500, Width-left, 501))
	small := c.regular(19)
	y := 532
	for _, line := range wrap(small, series.Citation+" Terms: "+TermsAddress+".", Width-2*left) {
		c.text(small, ink, left, y, line)
		y += 26
	}
	for _, line := range wrap(small, Disclaimer, Width-2*left-260) {
		c.text(small, dim, left, y, line)
		y += 26
	}
	site := "stocks.vibistic.com"
	c.text(c.semiBold(22), accent, Width-left-measure(c.semiBold(22), site), Height-30, site)

	// A fixed palette (every color of the card, and its blends with the two
	// backgrounds for the anti-aliased edges) keeps each file small: about
	// 4,900 of them share the site's 1 GB Pages limit with the data.
	out := image.NewPaletted(c.img.Bounds(), cardPalette)
	// Nearest palette color, remembered per color: most pixels share a few.
	nearest := map[color.RGBA]uint8{}
	for i := 0; i < len(c.img.Pix); i += 4 {
		px := color.RGBA{c.img.Pix[i], c.img.Pix[i+1], c.img.Pix[i+2], 0xff}
		idx, ok := nearest[px]
		if !ok {
			idx = uint8(cardPalette.Index(px))
			nearest[px] = idx
		}
		out.Pix[i/4] = idx
	}
	var buf bytes.Buffer
	if err := (&png.Encoder{CompressionLevel: png.DefaultCompression}).Encode(&buf, out); err != nil {
		return nil, nil, err
	}
	return buf.Bytes(), c.boxes, nil
}

var cardPalette = func() color.Palette {
	const levels = 12
	p := color.Palette{bg, panel, rule}
	blend := func(fg, back color.RGBA, t float64) color.RGBA {
		m := func(a, b uint8) uint8 { return uint8(math.Round(float64(a)*t + float64(b)*(1-t))) }
		return color.RGBA{m(fg.R, back.R), m(fg.G, back.G), m(fg.B, back.B), 0xff}
	}
	for _, back := range []color.RGBA{bg, panel} {
		for _, fg := range []color.RGBA{ink, dim, accent, accent2, up, down, rule} {
			for i := 1; i <= levels; i++ {
				p = append(p, blend(fg, back, float64(i)/levels))
			}
		}
	}
	return p
}()
