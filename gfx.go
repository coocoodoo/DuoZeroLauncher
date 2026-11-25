package main

// Drawing on the 320x170 RGB565 screen: colours, blending, rounded bevelled shapes in the
// style of a handheld system menu, graph-paper backgrounds, a proportional pixel font and
// alpha-blended icons. Everything is drawn by hand; no image assets are needed except app icons.

import (
	"dslauncher/cp0"
	"image"
	"image/png"
	"math"
	"os"
	"strings"
)

const W, H = cp0.W, cp0.H

type RGB struct{ R, G, B uint8 }

func rgb(r, g, b int) RGB { return RGB{uint8(r), uint8(g), uint8(b)} }

func hex(v uint32) RGB { return RGB{uint8(v >> 16), uint8(v >> 8), uint8(v)} }

func (c RGB) c565() uint16 {
	return uint16(c.R>>3)<<11 | uint16(c.G>>2)<<5 | uint16(c.B>>3)
}

func from565(v uint16) RGB {
	r, g, b := v>>11, (v>>5)&63, v&31
	return RGB{uint8(r<<3 | r>>2), uint8(g<<2 | g>>4), uint8(b<<3 | b>>2)}
}

func mix(a, b RGB, t float64) RGB {
	t = math.Max(0, math.Min(1, t))
	f := func(x, y uint8) uint8 { return uint8(float64(x) + (float64(y)-float64(x))*t + 0.5) }
	return RGB{f(a.R, b.R), f(a.G, b.G), f(a.B, b.B)}
}

func (c RGB) lighter(t float64) RGB { return mix(c, RGB{255, 255, 255}, t) }
func (c RGB) darker(t float64) RGB  { return mix(c, RGB{0, 0, 0}, t) }

// The system palette (after the ds.css reference: paper, grid lines, bars, focus, info boxes).
var (
	cPaper    = hex(0xe3e3e3)
	cGrid     = hex(0xc3c3c3)
	cDark     = hex(0x494949)
	cInk      = hex(0x303030)
	cMuted    = hex(0x808080)
	cFocus    = hex(0xe36100)
	cInfoBG   = hex(0x282828)
	cWhite    = hex(0xffffff)
	cLoading  = hex(0xfbd300)
	cBtnTop   = hex(0xfbfbfb)
	cBtnBot   = hex(0xd3d3d3)
	cBtnEdge  = hex(0x8a8a8a)
	cGood     = hex(0x00a238)
	cBad      = hex(0xdb2020)
	cSunday   = hex(0xd81830)
	cSaturday = hex(0x2050d8)
)

// The sixteen favourite colours a user can pick (names and values from ds.css).
var themes = []struct {
	Name string
	C    RGB
}{
	{"SLATE", hex(0x61829a)}, {"BROWN", hex(0xba4900)}, {"RED", hex(0xfb0018)}, {"PINK", hex(0xfb8afb)},
	{"ORANGE", hex(0xfb9200)}, {"YELLOW", hex(0xf3e300)}, {"LIME", hex(0xaafb00)}, {"GREEN", hex(0x00fb00)},
	{"FOREST", hex(0x00a238)}, {"TEAL", hex(0x49db8a)}, {"SKY", hex(0x30baf3)}, {"BLUE", hex(0x0059f3)},
	{"NAVY", hex(0x000092)}, {"PURPLE", hex(0x8a00d3)}, {"MAGENTA", hex(0xd300eb)}, {"FUCHSIA", hex(0xfb0092)},
}

// G draws into a cp0 screen.
type G struct {
	s    *cp0.Screen
	clip image.Rectangle
}

func newG(s *cp0.Screen) *G { return &G{s: s, clip: image.Rect(0, 0, W, H)} }

func (g *G) setClip(x, y, w, h int) {
	g.clip = image.Rect(x, y, x+w, y+h).Intersect(image.Rect(0, 0, W, H))
}
func (g *G) noClip() { g.clip = image.Rect(0, 0, W, H) }

func (g *G) in(x, y int) bool {
	return x >= g.clip.Min.X && y >= g.clip.Min.Y && x < g.clip.Max.X && y < g.clip.Max.Y
}

func (g *G) px(x, y int, c RGB) {
	if g.in(x, y) {
		g.s.Px[y*W+x] = c.c565()
	}
}

// bayer4 is a 4x4 ordered-dither matrix (0..15).
var bayer4 = [4][4]int{{0, 8, 2, 10}, {12, 4, 14, 6}, {3, 11, 1, 9}, {15, 7, 13, 5}}

// dither565 converts to RGB565 with ordered dithering, so a smooth gradient reads as smooth on
// the 16-bit screen instead of breaking into visible bands.
func dither565(x, y int, c RGB) uint16 {
	t := 2*bayer4[y&3][x&3] + 1 // 1..31, centred
	q := func(v uint8, levels int) uint16 {
		n := (int(v)*levels*32 + t*255) / (255 * 32)
		return uint16(min(n, levels))
	}
	return q(c.R, 31)<<11 | q(c.G, 63)<<5 | q(c.B, 31)
}

// pxSmooth writes one pixel of a gradient or texture with dithering.
func (g *G) pxSmooth(x, y int, c RGB) {
	if g.in(x, y) {
		g.s.Px[y*W+x] = dither565(x, y, c)
	}
}

func (g *G) get(x, y int) RGB {
	if x < 0 || y < 0 || x >= W || y >= H {
		return RGB{}
	}
	return from565(g.s.Px[y*W+x])
}

func (g *G) blend(x, y int, c RGB, a float64) {
	if a <= 0 || !g.in(x, y) {
		return
	}
	if a >= 1 {
		g.s.Px[y*W+x] = c.c565()
		return
	}
	g.s.Px[y*W+x] = mix(g.get(x, y), c, a).c565()
}

func (g *G) rect(x, y, w, h int, c RGB) {
	v := c.c565()
	r := image.Rect(x, y, x+w, y+h).Intersect(g.clip)
	for yy := r.Min.Y; yy < r.Max.Y; yy++ {
		row := g.s.Px[yy*W:]
		for xx := r.Min.X; xx < r.Max.X; xx++ {
			row[xx] = v
		}
	}
}

func (g *G) shade(x, y, w, h int, c RGB, a float64) {
	r := image.Rect(x, y, x+w, y+h).Intersect(g.clip)
	for yy := r.Min.Y; yy < r.Max.Y; yy++ {
		for xx := r.Min.X; xx < r.Max.X; xx++ {
			g.blend(xx, yy, c, a)
		}
	}
}

func (g *G) hline(x, y, w int, c RGB) { g.rect(x, y, w, 1, c) }
func (g *G) vline(x, y, h int, c RGB) { g.rect(x, y, 1, h, c) }

func (g *G) frame(x, y, w, h int, c RGB) {
	g.hline(x, y, w, c)
	g.hline(x, y+h-1, w, c)
	g.vline(x, y, h, c)
	g.vline(x+w-1, y, h, c)
}

// inside reports whether (px, py) lies inside a w x h rounded rectangle with radius r, and how
// much (for a soft edge).
func roundCover(px, py, w, h, r int) float64 {
	cx, cy := -1.0, -1.0
	fx, fy := float64(px)+0.5, float64(py)+0.5
	R := float64(r)
	switch {
	case fx < R && fy < R:
		cx, cy = R, R
	case fx > float64(w)-R && fy < R:
		cx, cy = float64(w)-R, R
	case fx < R && fy > float64(h)-R:
		cx, cy = R, float64(h)-R
	case fx > float64(w)-R && fy > float64(h)-R:
		cx, cy = float64(w)-R, float64(h)-R
	default:
		return 1
	}
	d := math.Hypot(fx-cx, fy-cy)
	return math.Max(0, math.Min(1, R-d+0.5))
}

// rrect fills a rounded rectangle with a vertical gradient from top to bot.
func (g *G) rrect(x, y, w, h, r int, top, bot RGB) {
	for yy := 0; yy < h; yy++ {
		c := top
		if h > 1 {
			c = mix(top, bot, float64(yy)/float64(h-1))
		}
		for xx := 0; xx < w; xx++ {
			if a := roundCover(xx, yy, w, h, r); a >= 1 {
				g.pxSmooth(x+xx, y+yy, c)
			} else if a > 0 {
				g.blend(x+xx, y+yy, c, a)
			}
		}
	}
}

// rborder draws a rounded outline `t` pixels thick.
func (g *G) rborder(x, y, w, h, r, t int, c RGB) {
	for yy := 0; yy < h; yy++ {
		for xx := 0; xx < w; xx++ {
			outer := roundCover(xx, yy, w, h, r)
			if outer <= 0 {
				continue
			}
			inner := 0.0
			if xx >= t && yy >= t && xx < w-t && yy < h-t {
				inner = roundCover(xx-t, yy-t, w-2*t, h-2*t, max(r-t, 0))
			}
			if a := outer - inner; a > 0 {
				g.blend(x+xx, y+yy, c, a)
			}
		}
	}
}

// button draws a bevelled button: light gradient, grey edge, a white inner highlight.
// focus adds a coloured ring; pressed darkens it.
func (g *G) button(x, y, w, h, r int, focus bool, ring RGB, pressed bool) {
	top, bot := cBtnTop, cBtnBot
	if pressed {
		top, bot = cBtnBot.darker(0.05), cBtnTop.darker(0.08)
	}
	if focus {
		top, bot = mix(top, ring, 0.10), mix(bot, ring, 0.22)
	}
	g.rrect(x, y, w, h, r, top, bot)
	g.rborder(x, y, w, h, r, 1, cBtnEdge)
	if !pressed {
		g.hline(x+r, y+1, w-2*r, cWhite)
	}
	if focus {
		g.rborder(x-1, y-1, w+2, h+2, r+1, 2, ring)
	}
}

// paper fills an area with light graph paper.
func (g *G) paper(x, y, w, h int) {
	g.rect(x, y, w, h, cPaper)
	for yy := y; yy < y+h; yy++ {
		if (yy-y)%8 == 7 {
			g.hline(x, yy, w, mix(cPaper, cGrid, 0.75))
		}
	}
	for xx := x; xx < x+w; xx++ {
		if (xx-x)%8 == 7 {
			for yy := y; yy < y+h; yy++ {
				if (yy-y)%8 != 7 {
					g.px(xx, yy, mix(cPaper, cGrid, 0.55))
				}
			}
		}
	}
}

// bar draws a dark title bar with a soft gradient and a highlight line.
func (g *G) bar(x, y, w, h int, tint RGB) {
	top := mix(hex(0x6a6a6a), tint, 0.12)
	bot := mix(hex(0x3c3c3c), tint, 0.10)
	for i := 0; i < h; i++ {
		c := mix(top, bot, float64(i)/float64(max(h-1, 1)))
		for xx := x; xx < x+w; xx++ {
			g.pxSmooth(xx, y+i, c)
		}
	}
	g.hline(x, y, w, top.lighter(0.25))
	g.hline(x, y+h-1, w, hex(0x202020))
}

func (g *G) line(x0, y0, x1, y1 int, c RGB) {
	dx, dy := abs(x1-x0), -abs(y1-y0)
	sx, sy := 1, 1
	if x0 > x1 {
		sx = -1
	}
	if y0 > y1 {
		sy = -1
	}
	err := dx + dy
	for {
		g.px(x0, y0, c)
		if x0 == x1 && y0 == y1 {
			return
		}
		e2 := 2 * err
		if e2 >= dy {
			err += dy
			x0 += sx
		}
		if e2 <= dx {
			err += dx
			y0 += sy
		}
	}
}

// aaline draws an anti-aliased line of thickness t (used for clock hands).
func (g *G) aaline(x0, y0, x1, y1, t float64, c RGB) {
	minx, maxx := int(math.Min(x0, x1)-t-1), int(math.Max(x0, x1)+t+1)
	miny, maxy := int(math.Min(y0, y1)-t-1), int(math.Max(y0, y1)+t+1)
	dx, dy := x1-x0, y1-y0
	l2 := dx*dx + dy*dy
	for y := miny; y <= maxy; y++ {
		for x := minx; x <= maxx; x++ {
			px, py := float64(x)+0.5, float64(y)+0.5
			u := 0.0
			if l2 > 0 {
				u = math.Max(0, math.Min(1, ((px-x0)*dx+(py-y0)*dy)/l2))
			}
			d := math.Hypot(px-(x0+u*dx), py-(y0+u*dy))
			g.blend(x, y, c, math.Max(0, math.Min(1, t/2-d+0.5)))
		}
	}
}

func (g *G) disc(cx, cy, r float64, c RGB) {
	for y := int(cy - r - 1); y <= int(cy+r+1); y++ {
		for x := int(cx - r - 1); x <= int(cx+r+1); x++ {
			d := math.Hypot(float64(x)+0.5-cx, float64(y)+0.5-cy)
			g.blend(x, y, c, math.Max(0, math.Min(1, r-d+0.5)))
		}
	}
}

func (g *G) ring(cx, cy, r, t float64, c RGB) {
	for y := int(cy - r - 1); y <= int(cy+r+1); y++ {
		for x := int(cx - r - 1); x <= int(cx+r+1); x++ {
			d := math.Hypot(float64(x)+0.5-cx, float64(y)+0.5-cy)
			a := math.Min(r-d+0.5, d-(r-t)+0.5)
			g.blend(x, y, c, math.Max(0, math.Min(1, a)))
		}
	}
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

// ---- text: the ds.css pixel font (bold is accepted for old call sites and ignored) ----

// textW is the width of s in pixels at scale.
func textW(s string, scale int, bold bool) int { return fontText.width(s) * scale }

// text draws s with the top of its capitals at y and returns the x after it.
func (g *G) text(x, y int, s string, c RGB, scale int, bold bool) int {
	return g.fontTextScaled(fontText, x, y, s, c, scale)
}

// textShadow draws text with a soft drop shadow, the way system menus label buttons.
func (g *G) textShadow(x, y int, s string, c, shadow RGB, scale int, bold bool) int {
	g.text(x+1, y+1, s, shadow, scale, bold)
	return g.text(x, y, s, c, scale, bold)
}

func (g *G) textC(cx, y int, s string, c RGB, scale int, bold bool) {
	g.text(cx-textW(s, scale, bold)/2, y, s, c, scale, bold)
}

func (g *G) textR(rx, y int, s string, c RGB, scale int, bold bool) {
	g.text(rx-textW(s, scale, bold), y, s, c, scale, bold)
}

// textTight draws text centred on cx, closing the gaps between letters if it would be wider than w.
func (g *G) textTight(cx, y int, s string, c RGB, w int) {
	if textW(s, 1, false) <= w {
		g.textC(cx, y, s, c, 1, false)
		return
	}
	tw := 0
	for _, r := range s {
		tw += fontText.glyph(r).Adv - 1
	}
	x := cx - tw/2
	for _, r := range s {
		g.fontText(fontText, x, y, string(r), c)
		x += fontText.glyph(r).Adv - 1
	}
}

// fit shortens s with "..." so it is at most w pixels wide.
func fit(s string, w, scale int, bold bool) string {
	if textW(s, scale, bold) <= w {
		return s
	}
	r := []rune(s)
	for len(r) > 0 && textW(string(r)+"..", scale, bold) > w {
		r = r[:len(r)-1]
	}
	return strings.TrimRight(string(r), " ") + ".."
}

// marquee returns a window of s that scrolls with time t when it is wider than w.
func (g *G) marquee(x, y, w int, s string, c RGB, t float64) {
	tw := textW(s, 1, false)
	if tw <= w {
		g.text(x, y, s, c, 1, false)
		return
	}
	old := g.clip
	g.clip = image.Rect(x, y-1, x+w, y+12).Intersect(old)
	span := float64(tw - w + 20)
	p := math.Mod(t*22, span*2+60)
	off := 0.0
	switch {
	case p < 30:
	case p < 30+span:
		off = p - 30
	case p < 60+span:
		off = span
	default:
		off = math.Max(0, span-(p-60-span)*3)
	}
	g.text(x-int(off), y, s, c, 1, false)
	g.clip = old
}

func btoi(b bool) int {
	if b {
		return 1
	}
	return 0
}

// ---- pixel glyphs (11x11 art, drawn at 2x for the key bar and settings) ----

// glyph draws ASCII art ('#' = ink, '+' = light ink) scaled by s, centred on (cx, cy).
// artIcon draws the glyph's SVG render at size x size centred on (cx, cy), or the pixel glyph
// when it has no SVG.
func (g *G) artIcon(cx, cy int, art []string, size int, ink RGB) {
	if t := smallIcon(art, size); t != nil {
		g.texInk(t, cx-size/2, cy-size/2, ink, 1)
		return
	}
	g.glyph(cx, cy, art, 1, ink)
}

func (g *G) glyph(cx, cy int, art []string, s int, ink RGB) {
	h := len(art)
	w := 0
	for _, l := range art {
		w = max(w, len(l))
	}
	x0, y0 := cx-w*s/2, cy-h*s/2
	for y, l := range art {
		for x, ch := range l {
			switch ch {
			case '#':
				g.rect(x0+x*s, y0+y*s, s, s, ink)
			case '+':
				g.shade(x0+x*s, y0+y*s, s, s, ink, 0.45)
			}
		}
	}
}

// ---- icons ----

// Icon is an RGBA image scaled for the screen.
type Icon struct {
	W, H int
	Pix  []uint8 // RGBA, straight alpha
}

// loadIcon reads a PNG and box-filters it to size x size.
func loadIcon(path string, size int) *Icon {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	img, err := png.Decode(f)
	if err != nil {
		return nil
	}
	return scaleIcon(img, size)
}

func scaleIcon(img image.Image, size int) *Icon {
	b := img.Bounds()
	sw, sh := b.Dx(), b.Dy()
	if sw == 0 || sh == 0 {
		return nil
	}
	ic := &Icon{W: size, H: size, Pix: make([]uint8, size*size*4)}
	for y := 0; y < size; y++ {
		y0, y1 := y*sh/size, max((y+1)*sh/size, y*sh/size+1)
		for x := 0; x < size; x++ {
			x0, x1 := x*sw/size, max((x+1)*sw/size, x*sw/size+1)
			var r, gg, bb, a, n float64
			for sy := y0; sy < y1; sy++ {
				for sx := x0; sx < x1; sx++ {
					cr, cg, cb, ca := img.At(b.Min.X+sx, b.Min.Y+sy).RGBA()
					// premultiplied 16-bit
					r += float64(cr)
					gg += float64(cg)
					bb += float64(cb)
					a += float64(ca)
					n++
				}
			}
			i := (y*size + x) * 4
			if a > 0 {
				ic.Pix[i] = uint8(r / a * 255)
				ic.Pix[i+1] = uint8(gg / a * 255)
				ic.Pix[i+2] = uint8(bb / a * 255)
			}
			ic.Pix[i+3] = uint8(a / n / 257)
		}
	}
	return ic
}

func (g *G) icon(x, y int, ic *Icon, fade float64) {
	if ic == nil {
		return
	}
	for yy := 0; yy < ic.H; yy++ {
		for xx := 0; xx < ic.W; xx++ {
			i := (yy*ic.W + xx) * 4
			a := float64(ic.Pix[i+3]) / 255 * fade
			if a > 0 {
				g.blend(x+xx, y+yy, RGB{ic.Pix[i], ic.Pix[i+1], ic.Pix[i+2]}, a)
			}
		}
	}
}
