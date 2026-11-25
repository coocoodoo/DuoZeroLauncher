package main

// The ds.css look on a 320x170 screen. The kit's textures (assets/, halved to native pixels by
// tools/mkassets.py) are drawn the way its CSS draws them: a base colour with the texture
// blended over it in hard-light mode, 1 px dark borders, the DS grid paper, the orange alert
// frame with the loading squares, the info header, the striped input field and the PictoChat
// frames. Text uses the kit's pixel font; clocks use its clock-digit font.

import (
	"bytes"
	"dslauncher/cp0"
	"embed"
	"image"
	"image/draw"
	"image/png"
	"math"
	"strings"
	"time"
)

//go:embed assets/*.png assets/icons/*.png assets/tiles/*.png
var assetFS embed.FS

type Tex struct {
	W, H int
	Pix  []uint8 // RGBA, straight alpha
}

var texCache = map[string]*Tex{}

func tex(name string) *Tex {
	if t, ok := texCache[name]; ok {
		return t
	}
	data, err := assetFS.ReadFile("assets/" + name)
	if err != nil {
		panic("missing asset " + name)
	}
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		panic("bad asset " + name)
	}
	b := img.Bounds()
	rgba := image.NewNRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	draw.Draw(rgba, rgba.Bounds(), img, b.Min, draw.Src)
	t := &Tex{W: b.Dx(), H: b.Dy(), Pix: rgba.Pix}
	texCache[name] = t
	return t
}

// ds.css colours
var (
	cBorder      = hex(0x494949) // --color-ds-darkgrey
	cButton      = hex(0x808080) // --color-ds-button-default
	cGridBG      = hex(0xe3e3e3)
	cGridLine    = hex(0xc3c3c3)
	cCalHeader   = hex(0x696969)
	cCalSat      = hex(0x82aafb)
	cCalSun      = hex(0xfba2eb)
	cCalBorder   = hex(0x929292)
	cCalHeadSat  = hex(0x0041c3)
	cCalHeadSun  = hex(0xd30000)
	cCalTextSat  = hex(0x000082)
	cCalTextSun  = hex(0x790000)
	cCalFrame    = hex(0x303030) // calendar outer frame and header separators
	cCalDay      = hex(0xf7f7f7) // weekday cells
	cCalToday    = hex(0x3e5c78) // today's dotted border: dark dots
	cCalTodayLt  = hex(0xa9bccd) // today's dotted border: light dots
	cPlateLine   = hex(0x9a9a9a) // outline of the white clock and calendar plates
	cBlack       = RGB{}
	cPictoEnter  = hex(0xe3f300)
	cPictoExit   = hex(0x00fbba)
	cDetailsLine = hex(0xa2a2a2)
)

func hardLight(b, t uint8) uint8 {
	bf, tf := float64(b)/255, float64(t)/255
	var r float64
	if tf <= 0.5 {
		r = 2 * tf * bf
	} else {
		r = 1 - 2*(1-tf)*(1-bf)
	}
	return uint8(r*255 + 0.5)
}

// mapSlice maps a destination offset d in [0,n) to a source offset in [0,src), keeping s pixels
// at each end and scaling (or tiling) the middle.
func mapSlice(d, n, src, s int, tile bool) int {
	if s*2 >= src {
		s = src / 2
	}
	switch {
	case d < s:
		return d
	case d >= n-s:
		return max(src-(n-d), 0)
	}
	mid := src - 2*s
	if mid <= 0 {
		return min(d, src-1)
	}
	if tile {
		return s + (d-s)%mid
	}
	return s + (d-s)*mid/max(n-2*s, 1)
}

// draw9 draws t into the rectangle with s-pixel fixed edges. With base, the texture is blended
// over that colour in hard-light mode (as background-blend-mode: hard-light does); hollow skips
// the centre (a CSS border-image without fill).
func (g *G) draw9(t *Tex, x, y, w, h, s int, base *RGB, tileX, hollow bool) {
	for dy := 0; dy < h; dy++ {
		sy := mapSlice(dy, h, t.H, s, false)
		for dx := 0; dx < w; dx++ {
			if hollow && dx >= s && dx < w-s && dy >= s && dy < h-s {
				continue
			}
			sx := mapSlice(dx, w, t.W, s, tileX)
			i := (sy*t.W + sx) * 4
			a := float64(t.Pix[i+3]) / 255
			c := RGB{t.Pix[i], t.Pix[i+1], t.Pix[i+2]}
			if base != nil {
				hl := RGB{hardLight(base.R, c.R), hardLight(base.G, c.G), hardLight(base.B, c.B)}
				g.blend(x+dx, y+dy, mix(*base, hl, a), 1)
			} else {
				g.blend(x+dx, y+dy, c, a)
			}
		}
	}
}

// texAt draws a texture as-is at (x, y) with an extra fade.
func (g *G) texAt(t *Tex, x, y int, fade float64) {
	for dy := 0; dy < t.H; dy++ {
		for dx := 0; dx < t.W; dx++ {
			i := (dy*t.W + dx) * 4
			if a := float64(t.Pix[i+3]) / 255 * fade; a > 0 {
				g.blend(x+dx, y+dy, RGB{t.Pix[i], t.Pix[i+1], t.Pix[i+2]}, a)
			}
		}
	}
}

// texInk draws a texture's alpha as a silhouette in one colour (the icons are black masks).
func (g *G) texInk(t *Tex, x, y int, ink RGB, fade float64) {
	for dy := 0; dy < t.H; dy++ {
		for dx := 0; dx < t.W; dx++ {
			if a := float64(t.Pix[(dy*t.W+dx)*4+3]) / 255 * fade; a > 0 {
				g.blend(x+dx, y+dy, ink, a)
			}
		}
	}
}

// ---- components ----

// dsButton is a ds.css button: a 1 px dark border round the button texture over a base colour
// (grey normally, the favourite colour when chosen); pressed uses the "active" texture.
func (g *G) dsButton(x, y, w, h int, base RGB, pressed bool) {
	g.frame(x, y, w, h, cBorder)
	name := "button"
	switch iw := w - 2; {
	case iw <= 24 && h <= 30:
		name = "button_square"
	case iw > 90:
		name = "button_lg"
	}
	if pressed {
		name += "_active"
	}
	g.draw9(tex(name+".png"), x+1, y+1, w-2, h-2, 2, &base, false, false)
}

// dsSquare is the square button (app tiles, key caps).
func (g *G) dsSquare(x, y, w, h int, base RGB, pressed bool) {
	g.frame(x, y, w, h, cBorder)
	name := "button_square"
	if pressed {
		name += "_active"
	}
	g.draw9(tex(name+".png"), x+1, y+1, w-2, h-2, 2, &base, false, false)
}

// dsBar is the thin title bar (.bar) over a base colour, with its dark bottom line.
func (g *G) dsBar(x, y, w, h int, base RGB) {
	g.draw9(tex("top_bar.png"), x, y, w, h-1, 1, &base, true, false)
	g.hline(x, y+h-1, w, cBlack)
}

// dsBarLg is the large bar (.bar-lg-reverse when top is true: the line is on top).
func (g *G) dsBarLg(x, y, w, h int, base RGB, lineOnTop bool) {
	if lineOnTop {
		g.hline(x, y, w, cBlack)
		g.draw9(tex("bottom_bar_bottom.png"), x, y+1, w, h-1, 1, &base, true, false)
	} else {
		g.draw9(tex("bottom_bar_top.png"), x, y, w, h-1, 1, &base, true, false)
		g.hline(x, y+h-1, w, cBlack)
	}
}

// dsGrid is the DS grid paper: alternating white and light grey rows, a line every 16 px.
// Lines are on absolute screen positions so panes line up.
func (g *G) dsGrid(x, y, w, h int) {
	white, grey, line := cWhite.c565(), cGridBG.c565(), cGridLine.c565()
	r := image.Rect(x, y, x+w, y+h).Intersect(g.clip)
	for yy := r.Min.Y; yy < r.Max.Y; yy++ {
		row := g.s.Px[yy*W:]
		c := white
		if yy%2 == 1 {
			c = grey
		}
		for xx := r.Min.X; xx < r.Max.X; xx++ {
			if xx%16 == 15 || yy%16 == 15 {
				row[xx] = line
			} else {
				row[xx] = c
			}
		}
	}
}

// dsDetails is the striped light box used for details text (white/grey rows, grey border).
func (g *G) dsDetails(x, y, w, h int) {
	for i := 0; i < h; i++ {
		c := cWhite
		if (y+i)%2 == 1 {
			c = cGridBG
		}
		g.hline(x, y+i, w, c)
	}
	g.frame(x, y, w, h, cBorder)
	g.frame(x+1, y+1, w-2, h-2, cWhite)
	g.frame(x+2, y+2, w-4, h-4, cDetailsLine)
}

// dsInfoText is the dark info text box with the dotted white frame.
func (g *G) dsInfoText(x, y, w, h int) {
	g.rect(x, y, w, h, hex(0x282828))
	g.draw9(tex("info_border.png"), x, y, w, h, 2, nil, false, true)
}

// dsInfoSquare is the square picture box of the info header.
func (g *G) dsInfoSquare(x, y, s int, base RGB) {
	g.frame(x, y, s, s, cBorder)
	g.draw9(tex("info_square.png"), x+1, y+1, s-2, s-2, 3, &base, false, false)
}

// dsInfo is the info header strip (.info) behind a square and a label/text pair.
func (g *G) dsInfo(x, y, w, h int, base RGB) {
	g.hline(x, y, w, cBlack)
	g.draw9(tex("info.png"), x, y+1, w, h-1, 2, &base, false, false)
}

// dsAlert is the alert box: black/dark-grey stripes inside the orange frame.
func (g *G) dsAlert(x, y, w, h int) {
	for i := 0; i < h; i++ {
		c := cBlack
		if (i/1)%2 == 1 {
			c = cBorder
		}
		g.hline(x, y+i, w, c)
	}
	g.draw9(tex("alert_border.png"), x, y, w, h, 7, nil, false, true)
}

// dsLoading draws the 3x3 loading squares (3 px, 1 px apart) at (x, y): the centre stays lit
// while a three-square gap chases round, one step every 1/15 s (533 ms a turn).
func (g *G) dsLoading(x, y int, t float64) {
	step := int(math.Mod(t, 0.5333)/0.5333*8) % 8
	// off steps for squares 1..9 (5 = centre, always on), from ds.css's keyframes
	off := map[int][]int{1: {3, 4, 5}, 2: {4, 5, 6}, 3: {5, 6, 7}, 4: {2, 3, 4}, 6: {0, 6, 7},
		7: {1, 2, 3}, 8: {0, 1, 2}, 9: {0, 1, 7}}
	for sq := 1; sq <= 9; sq++ {
		lit := true
		for _, s := range off[sq] {
			if s == step {
				lit = false
			}
		}
		if lit {
			col, row := (sq-1)%3, (sq-1)/3
			g.rect(x+col*4, y+row*4, 3, 3, cLoading)
		}
	}
}

// dsInput is the striped dark input field with its bumpers; focus adds the orange outline.
func (g *G) dsInput(x, y, w, h int, focus bool, base RGB) {
	bl, br := tex("input_before.png"), tex("input_after.png")
	g.draw9(bl, x, y, bl.W, h, 2, nil, false, false)
	g.draw9(br, x+w-br.W, y, br.W, h, 2, nil, false, false)
	ix, iw := x+bl.W+1, w-bl.W-br.W-2
	t := tex("input.png")
	for dy := 0; dy < h-2; dy++ {
		for dx := 0; dx < iw-2; dx++ {
			i := (((dy)%t.H)*t.W + (dx % t.W)) * 4
			g.px(ix+1+dx, y+1+dy, RGB{t.Pix[i], t.Pix[i+1], t.Pix[i+2]})
		}
	}
	g.frame(ix, y, iw, h, cBlack)
	if focus {
		g.frame(ix-1, y-1, iw+2, h+2, cFocus)
	}
}

// dsBubble is a PictoChat message frame filled white.
func (g *G) dsBubble(x, y, w, h int) {
	g.rect(x+2, y+2, w-4, h-4, cWhite)
	g.draw9(tex("pictochat_message_border.png"), x, y, w, h, 4, nil, false, true)
}

// dsStatus is the PictoChat status box: black with the white frame (used for notices).
func (g *G) dsStatus(x, y, w, h int) {
	g.rect(x+2, y+2, w-4, h-4, cBlack)
	g.draw9(tex("pictochat_status.png"), x, y, w, h, 4, nil, false, true)
}

// dsLabel is a PictoChat-style name tab: a small coloured box with a dark frame.
func (g *G) dsLabel(x, y int, s string, base RGB) int {
	w := textW(s, 1, false) + 8
	g.rect(x, y, w, 13, base.lighter(0.45))
	g.draw9(tex("pictochat_message_header_border.png"), x, y, w, 13, 2, nil, false, true)
	g.text(x+4, y+2, s, inkOn(base.lighter(0.45)), 1, false)
	return w
}

// inkOn picks black or white text for a background.
func inkOn(c RGB) RGB {
	if 0.299*float64(c.R)+0.587*float64(c.G)+0.114*float64(c.B) < 120 {
		return cWhite
	}
	return cBlack
}

// barBase is the colour a theme-tinted bar or chosen button is drawn over.
func barBase(theme RGB) RGB { return theme }

// ---- DS clock ----

// dsClock draws the square analog clock: grid face, grey 12/3/6/9 numerals in the clock font,
// dots for the other hours, grey hands and a coloured second hand.
func (g *G) dsClock(x, y, s int, now time.Time, accent RGB) {
	_ = accent // the DS second hand is always steel blue
	// 2-px black frame around a striped paper face with a 6 x 6 grid, as on the DS
	g.frame(x, y, s, s, cBlack)
	g.frame(x+1, y+1, s-2, s-2, cBlack)
	old := g.clip
	g.setClip(x+2, y+2, s-4, s-4)
	for i := 2; i < s-2; i++ {
		c := cWhite
		if i%2 == 0 {
			c = hex(0xeeeeee)
		}
		g.hline(x+2, y+i, s-4, c)
	}
	for i := 1; i < 6; i++ {
		g.vline(x+i*s/6, y+2, s-4, hex(0xd6d6d6))
		g.hline(x+2, y+i*s/6, s-4, hex(0xd6d6d6))
	}
	g.clip = old
	cx, cy := float64(x)+float64(s)/2, float64(y)+float64(s)/2
	numeral := hex(0xaaaaaa)
	f := fontClock10
	if s >= 72 {
		f = fontClock15
	}
	if s >= 130 {
		f = fontClock20
	}
	r := float64(s)/2 - 4
	nh := f.capHeight()
	for i := 0; i < 12; i++ {
		ang := float64(i) / 12 * 2 * math.Pi
		px, py := cx+math.Sin(ang)*(r-float64(nh)/2-1), cy-math.Cos(ang)*(r-float64(nh)/2-1)
		switch i {
		case 0, 3, 6, 9:
			label := map[int]string{0: "12", 3: "3", 6: "6", 9: "9"}[i]
			tw := f.width(label)
			g.fontText(f, int(px)-tw/2, int(py)-nh/2, label, numeral)
		default:
			d := 2 + btoi(s >= 72)
			g.rect(int(px)-d/2, int(py)-d/2, d, d, hex(0x707070))
		}
	}
	sec := float64(now.Second())
	mins := float64(now.Minute()) + sec/60
	hr := float64(now.Hour()%12) + mins/60
	hand := func(frac, length, width float64, c RGB) {
		ang := frac * 2 * math.Pi
		g.aaline(cx, cy, cx+math.Sin(ang)*length, cy-math.Cos(ang)*length, width, c)
	}
	hand(hr/12, r*0.55, 2.2, hex(0x666666))
	hand(mins/60, r*0.85, 1.8, hex(0x666666))
	hand(sec/60, r*0.85, 1.4, hex(0x55799a))
	g.rect(int(cx)-2, int(cy)-2, 5, 5, hex(0x3a3a3a))
}

// dsPlate is the white panel with a grey outline that labels a clock or calendar.
func (g *G) dsPlate(x, y, w, h int) {
	g.rect(x, y, w, h, cPlateLine)
	g.rect(x+1, y+1, w-2, h-2, cWhite)
}

// todayFrame is the DS calendar's mark for today: a dithered outline t pixels thick whose
// pixels alternate dark slate and light blue-grey in a checkerboard.
func (g *G) todayFrame(x, y, w, h, t int) {
	for j := 0; j < h; j++ {
		for i := 0; i < w; i++ {
			if i >= t && i < w-t && j >= t && j < h-t {
				continue // inside the band
			}
			c := cCalTodayLt
			if (x+i+y+j)%2 == 0 {
				c = cCalToday
			}
			g.px(x+i, y+j, c)
		}
	}
}

// spacedText draws s centred on cx with `gap` extra pixels between letters (the DS month title).
func (g *G) spacedText(cx, y int, s string, c RGB, gap int) {
	n := 0
	for range s {
		n++
	}
	x := cx - (textW(s, 1, false)+gap*(n-1))/2
	for _, r := range s {
		g.fontText(fontText, x, y, string(r), c)
		x += fontText.glyph(r).Adv + gap
	}
}

// dsCalendar is the DS month calendar: the month on a white plate sitting right on top of the
// grid, a dark frame around the whole grid, red/grey/blue day headers, pink Sunday and blue
// Saturday columns, light grey lines between days, and today in a white cell with a checkered
// border.
func (g *G) dsCalendar(x, y, w, h int, now time.Time) {
	first := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location())
	days := first.AddDate(0, 1, -1).Day()
	lead := int(first.Weekday())
	rows := (lead + days + 6) / 7

	const plateH, headH = 16, 14
	colW := (w - 2) / 7
	gw := colW*7 + 1        // grid width, lines included
	gx := x + (w-gw-2)/2 + 1 // inside the 1-px dark frame
	rowH := min((y+h-(y+plateH)-headH-2)/rows, 16)

	// month plate: white, grey outline, same width as the grid and touching it
	g.dsPlate(gx-1, y, gw+2, plateH+1)
	g.spacedText(gx+gw/2, y+4, now.Format("1/2006"), cBlack, 2)

	top := y + plateH + 1 // first row of the header, under the frame line
	g.rect(gx-1, top-1, gw+2, headH+rows*rowH+2, cCalFrame)
	heads := []string{"Su", "Mo", "Tu", "We", "Th", "Fr", "Sa"}
	for i, d := range heads {
		c := cCalHeader
		if i == 0 {
			c = cCalHeadSun
		} else if i == 6 {
			c = cCalHeadSat
		}
		g.rect(gx+i*colW+btoi(i > 0), top, colW-btoi(i > 0), headH-1, c)
		g.textC(gx+i*colW+colW/2+1, top+(headH-1-9)/2+1, d, cWhite, 1, false)
	}
	gy := top + headH - 1 // day grid starts on the frame line under the header
	g.rect(gx, gy+1, gw-0, rows*rowH, cCalBorder)
	for r := 0; r < rows; r++ {
		for c := 0; c < 7; c++ {
			bg, ink := cCalDay, cBlack
			if c == 0 {
				bg, ink = cCalSun, cCalTextSun
			} else if c == 6 {
				bg, ink = cCalSat, cCalTextSat
			}
			// cells share 1-px grey lines; the outer edge is the dark frame
			cx0 := gx + c*colW + btoi(c > 0)
			cw := colW - btoi(c > 0)
			cy0 := gy + 1 + r*rowH + btoi(r > 0)
			ch := rowH - btoi(r > 0)
			d := r*7 + c - lead + 1
			today := d == now.Day()
			if today {
				bg, ink = cCalDay, cCalTextSun // today: white cell, dark red number, any weekday
			}
			g.rect(cx0, cy0, cw, ch, bg)
			if d < 1 || d > days {
				continue
			}
			if today {
				// DS today marker: a 2-px dithered border alternating dark slate and light
				// blue-grey, white inside, with a 1-px white gap from the cell lines when the
				// number still fits inside
				const band = 2
				gap := 1
				if cw-2*gap-2*band < textW(itoa(d), 1, false) || ch-2*gap-2*band < 9 {
					gap = 0
				}
				g.todayFrame(cx0+gap, cy0+gap, cw-2*gap, ch-2*gap, band)
			}
			g.textC(cx0+cw/2+1, cy0+(ch-9)/2+1, itoa(d), ink, 1, false)
		}
	}
}

func itoa(v int) string {
	if v == 0 {
		return "0"
	}
	neg := v < 0
	if neg {
		v = -v
	}
	var b []byte
	for v > 0 {
		b = append([]byte{byte('0' + v%10)}, b...)
		v /= 10
	}
	if neg {
		b = append([]byte{'-'}, b...)
	}
	return string(b)
}

// ---- bitmap fonts (generated into fontdata.go) ----

type bmGlyph struct {
	Adv, OX, OY, W, H int
	Rows              []uint64
}

type bmFont struct {
	Ascent, Descent int
	G               map[rune]bmGlyph
	capTop          int
	capH            int
}

func (f *bmFont) init() {
	if f.capH != 0 {
		return
	}
	ref := '0'
	if g, ok := f.G['H']; ok {
		f.capTop, f.capH = g.OY, g.H
		return
	}
	g := f.G[ref]
	f.capTop, f.capH = g.OY, g.H
}

func (f *bmFont) capHeight() int { f.init(); return f.capH }

func (f *bmFont) glyph(r rune) bmGlyph {
	if g, ok := f.G[r]; ok {
		return g
	}
	if g, ok := f.G['?']; ok {
		return g
	}
	return f.G[' ']
}

func (f *bmFont) width(s string) int {
	w := 0
	for _, r := range s {
		w += f.glyph(r).Adv
	}
	return w
}

// fontText draws s with the font's cap top at y.
func (g *G) fontText(f *bmFont, x, y int, s string, c RGB) int {
	return g.fontTextScaled(f, x, y, s, c, 1)
}

func (g *G) fontTextScaled(f *bmFont, x, y int, s string, c RGB, scale int) int {
	f.init()
	for _, r := range s {
		gl := f.glyph(r)
		for row, bits := range gl.Rows {
			for col := 0; col < gl.W; col++ {
				if bits>>uint(col)&1 == 1 {
					px := x + (gl.OX+col)*scale
					py := y + (gl.OY-f.capTop+row)*scale
					if scale == 1 {
						g.px(px, py, c)
					} else {
						g.rect(px, py, scale, scale, c)
					}
				}
			}
		}
		x += gl.Adv * scale
	}
	return x
}

// ---- small labels: the 5x7 kit font, set proportionally (tile and key bar captions) ----

func smallCols(r rune) (cols [5]byte, first, w int) {
	cols = cp0.Glyph(r)
	if r == ' ' {
		return cols, 0, 2
	}
	first, last := -1, -1
	for i, c := range cols {
		if c != 0 {
			if first < 0 {
				first = i
			}
			last = i
		}
	}
	if first < 0 {
		return cols, 0, 2
	}
	return cols, first, last - first + 1
}

func smallW(s string) int {
	w := 0
	for _, r := range s {
		_, _, cw := smallCols(r)
		w += cw + 1
	}
	return max(w-1, 0)
}

// textSmall draws s with its top at y (7 px capitals) and returns the x after it.
func (g *G) textSmall(x, y int, s string, c RGB) int {
	for _, r := range s {
		cols, first, cw := smallCols(r)
		for i := 0; i < cw && r != ' '; i++ {
			for row := 0; row < 8; row++ {
				if cols[first+i]>>uint(row)&1 == 1 {
					g.px(x+i, y+row, c)
				}
			}
		}
		x += cw + 1
	}
	return x
}

func (g *G) textSmallC(cx, y int, s string, c RGB) { g.textSmall(cx-smallW(s)/2, y, s, c) }

// textSmallFit centres s on cx, dropping the gaps between letters if it is wider than w.
func (g *G) textSmallFit(cx, y int, s string, c RGB, w int) {
	if smallW(s) <= w {
		g.textSmallC(cx, y, s, c)
		return
	}
	tw := 0
	for _, r := range s {
		_, _, cw := smallCols(r)
		tw += cw
	}
	x := cx - tw/2
	for _, r := range s {
		_, _, cw := smallCols(r)
		g.textSmall(x, y, string(r), c)
		x += cw
	}
}

// titleCase turns "TURN OFF" into "Turn off" for captions.
func titleCase(s string) string {
	if s == "" {
		return s
	}
	l := strings.ToLower(s)
	return strings.ToUpper(l[:1]) + l[1:]
}
