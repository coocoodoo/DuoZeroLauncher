package main

// The home screen, laid out like the two screens of a folding handheld side by side: the left
// "upper screen" shows the square clock with the time and date (Tab swaps in the calendar); a
// hinge; the right "touch screen" holds the apps on grid paper: the info header for the chosen
// one and a sliding strip of square buttons.

import (
	"dslauncher/cp0"
	"fmt"
	"math"
	"os"
	"strings"
	"time"
)

const (
	paneL  = 128 // left pane width
	hingeW = 4
	paneRX = paneL + hingeW
	paneRW = W - paneRX
	tileW  = 34
	tileG  = 2
	barH   = 16 // title bar height
)

type Home struct {
	sel      int
	slide    float64 // strip offset in tiles, eases to 0
	last     time.Time
	key      string
	calendar bool
}

func newHome(a *App) *Home { return &Home{last: time.Now()} }

func (h *Home) cur(a *App) *Entry {
	if len(a.apps) == 0 {
		return nil
	}
	h.sel = (h.sel%len(a.apps) + len(a.apps)) % len(a.apps)
	return a.apps[h.sel]
}

// keep re-finds the selected app after the list changes.
func (h *Home) keep(a *App) {
	for i, e := range a.apps {
		if e.Key == h.key {
			h.sel = i
			return
		}
	}
	h.sel = min(h.sel, max(len(a.apps)-1, 0))
}

func (h *Home) move(a *App, d int) {
	n := len(a.apps)
	if n == 0 {
		return
	}
	h.sel = ((h.sel+d)%n + n) % n
	h.slide += float64(d)
	h.slide = math.Max(-3, math.Min(3, h.slide))
	h.key = a.apps[h.sel].Key
	a.snd.Play("move")
}

func (h *Home) Animating(a *App) bool { return math.Abs(h.slide) > 0.02 }

func (h *Home) Key(a *App, k cp0.Key) {
	e := h.cur(a)
	switch {
	case k.Quit():
		// Duo is the default launcher: holding Esc does not close it. (Holding Esc to close an
		// app used to carry over and quit Duo too.) Leave via Settings > Launcher instead.
	case k.Name == "LEFT":
		h.move(a, -1)
	case k.Name == "RIGHT":
		h.move(a, 1)
	case k.Name == "UP":
		h.move(a, -5)
	case k.Name == "DOWN":
		h.move(a, 5)
	case k.Name == "TAB":
		h.calendar = !h.calendar
		a.snd.Play("tick")
	case k.Name == "ENTER" || k.Slot() == 0:
		if e != nil {
			a.open(e)
		}
	case k.Slot() == 1 && e != nil:
		if a.cfg.togglePin(e.Key) {
			a.snd.Play("toggle-on")
			a.notify("Pinned "+e.Name+" to the front", false)
		} else {
			a.snd.Play("toggle-off")
			a.notify("Unpinned "+e.Name, false)
		}
		a.save()
		h.key = e.Key
		a.apps = orderApps(a.all, a.cfg, false)
		h.keep(a)
	case k.Slot() == 2 && e != nil:
		a.snd.Play("modal")
		a.push(&AppInfo{e: e})
	case k.Slot() == 3:
		a.snd.Play("open")
		a.push(newSettings(a))
	case k.Slot() == 4:
		a.push(newLock(a))
	}
}

func (h *Home) Bar(a *App) []BarItem {
	fave := "FAVE"
	if e := h.cur(a); e != nil && a.cfg.pinIndex(e.Key) >= 0 {
		fave = "UNPIN"
	}
	return []BarItem{{"OPEN", artOpen, false}, {fave, artStar, false}, {"INFO", artInfo, false},
		{"SETTINGS", artGear, false}, {"LOCK", artLock, false}}
}

func (h *Home) Draw(a *App) {
	dt := time.Since(h.last).Seconds()
	h.last = time.Now()
	h.slide *= math.Exp(-dt * 16)
	if math.Abs(h.slide) < 0.02 {
		h.slide = 0
	}
	drawUpper(a, h.calendar)
	drawHinge(a.g, paneL, 0, hingeW, 132)
	h.drawLower(a)
}

// drawUpper is the left pane: name bar, then the clock or the calendar.
// clockNow is the time shown on the home screen. DSL_NOW ("2006-01-02 15:04:05") pins it for
// scripted screenshots.
func clockNow() time.Time {
	if v := os.Getenv("DSL_NOW"); v != "" {
		if t, err := time.ParseInLocation("2006-01-02 15:04:05", v, time.Local); err == nil {
			return t
		}
	}
	return time.Now()
}

func drawUpper(a *App, calendar bool) {
	g := a.g
	now := clockNow()
	g.dsGrid(0, barH, paneL, 132-barH)
	g.dsBar(0, 0, paneL, barH, a.theme())
	ink := inkOn(a.theme())
	g.text(5, 4, fit(a.cfg.Name, paneL-24, 1, false), ink, 1, false)
	if a.cfg.AlarmOn {
		g.glyph(paneL-9, 8, artBell, 1, ink)
	}
	if calendar {
		g.dsCalendar(2, barH+3, paneL-4, 132-barH-4, now)
		return
	}
	const clockS = 76
	clockX := (paneL - clockS) / 2
	g.dsClock(clockX, barH+2, clockS, now, a.theme())
	hh := now.Hour()
	ampm := ""
	if !a.cfg.Clock24 {
		ampm = "AM"
		if hh >= 12 {
			ampm = "PM"
		}
		hh = (hh+11)%12 + 1
	}
	sep := ":"
	if now.Nanosecond() > 5e8 {
		sep = " "
	}
	// the time and date sit on a white plate attached under the clock, the way the month sits
	// on the calendar
	plateY := barH + 2 + clockS
	plateH := 132 - 1 - plateY
	g.dsPlate(clockX, plateY, clockS, plateH)
	t := fmt.Sprintf("%02d%s%02d", hh, sep, now.Minute())
	f := fontClock20
	width := func(f *bmFont) int {
		w := f.width(t)
		if ampm != "" {
			w += textW(ampm, 1, false) + 2
		}
		return w
	}
	if width(f) > clockS-6 {
		f = fontClock15
	}
	tw := width(f)
	tx := (paneL - tw) / 2
	ty := plateY + 3
	tx = g.fontText(f, tx, ty, t, cBlack)
	if ampm != "" {
		g.text(tx+2, ty+f.capHeight()-9, ampm, cBlack, 1, false)
	}
	date := now.Format("Mon 1/2")
	if a.cfg.isBirthday(now) {
		date = "Happy birthday!"
	}
	g.textC(paneL/2, plateY+plateH-12, fit(date, clockS-4, 1, false), cBlack, 1, false)
}

func fmtClock(c *Config, hh, mm int) string {
	if c.Clock24 {
		return fmt.Sprintf("%02d:%02d", hh, mm)
	}
	ap := "AM"
	if hh >= 12 {
		ap = "PM"
	}
	return fmt.Sprintf("%d:%02d %s", (hh+11)%12+1, mm, ap)
}

func drawHinge(g *G, x, y, w, h int) {
	for i := 0; i < w; i++ {
		g.vline(x+i, y, h, []RGB{cBlack, hex(0x8c8c8c), hex(0x6c6c6c), cBlack}[i%4])
	}
}

// drawLower is the right pane: title bar, the info header for the chosen app, the strip of
// square buttons and a scroll bar.
func (h *Home) drawLower(a *App) {
	g := a.g
	x0 := paneRX
	g.dsGrid(x0, barH, paneRW, 132-barH)
	g.dsBar(x0, 0, paneRW, barH, a.theme())
	ink := inkOn(a.theme())
	n := len(a.apps)
	ax := g.text(x0+5, 4, "Apps", ink, 1, false)
	if n > 0 {
		g.text(ax+5, 4, fmt.Sprintf("%d/%d", h.sel+1, n), ink, 1, false)
	}
	drawStatus(a, W-3, 3, ink)
	e := h.cur(a)
	if e == nil {
		g.textC(x0+paneRW/2, 60, "No apps found", cBlack, 1, false)
		return
	}
	h.key = e.Key

	// the info header: picture square, name label, dark text box
	iy := barH + 3
	g.dsInfo(x0, iy, paneRW, 47, cButton)
	g.dsInfoSquare(x0+2, iy+2, 43, cButton)
	if ic := e.Icon(38); ic != nil {
		g.icon(x0+4, iy+4, ic, 1)
	} else {
		placeholder(g, x0+4, iy+4, 38, e, a.theme())
	}
	lx := x0 + 48
	if a.cfg.pinIndex(e.Key) >= 0 {
		g.glyph(W-8, iy+7, artStar, 1, cFocus)
	}
	g.text(lx+3, iy+3, fit(e.Name, W-lx-14, 1, false), cBlack, 1, false)
	g.dsInfoText(lx, iy+15, W-lx-2, 31)
	sub := e.Comment
	if sub == "" {
		if e.Builtin != "" {
			sub = "Built in"
		} else {
			sub = "App"
		}
	}
	lines := wrap(sub, W-lx-12, false)
	if len(lines) <= 2 {
		for i, l := range lines {
			g.text(lx+5, iy+19+i*12, l, cWhite, 1, false)
		}
	} else {
		g.marquee(lx+5, iy+19, W-lx-12, sub, cWhite, a.t)
		last := "never opened"
		if t := a.cfg.Used[e.Key]; t > 0 {
			last = "opened " + time.Unix(t, 0).Format("Jan 2")
		}
		g.text(lx+5, iy+31, last, hex(0xb0b0b0), 1, false)
	}

	// the strip
	sy := iy + 52
	mid := x0 + paneRW/2 - tileW/2
	old := g.clip
	g.setClip(x0, sy-2, paneRW, tileW+4)
	for d := -4; d <= 4; d++ {
		i := ((h.sel+d)%n + n) % n
		if n < 9 && (d < -n/2 || d > (n-1)/2) {
			continue
		}
		pos := float64(d) + h.slide
		tx := mid + int(math.Round(pos*float64(tileW+tileG)))
		if tx < x0-tileW || tx > W {
			continue
		}
		base := cButton
		if d == 0 && math.Abs(h.slide) < 0.3 {
			base = a.theme()
		}
		g.dsSquare(tx, sy, tileW, tileW, base, false)
		it := a.apps[i]
		if ic := it.Icon(26); ic != nil {
			g.icon(tx+4, sy+4, ic, 1)
		} else {
			placeholder(g, tx+4, sy+4, 26, it, a.theme())
		}
	}
	g.clip = old

	// scroll bar with arrows
	by := sy + tileW + 6
	g.glyph(x0+8, by+4, artLeft, 1, cBorder)
	g.glyph(W-8, by+4, artRight, 1, cBorder)
	tx0, tw := x0+16, paneRW-32
	g.rect(tx0, by+2, tw, 5, cGridBG)
	g.frame(tx0, by+2, tw, 5, cBorder)
	thumb := max(tw*5/max(n, 5), 12)
	pos := 0
	if n > 1 {
		pos = (tw - thumb) * h.sel / (n - 1)
	}
	g.dsButton(tx0+pos, by, thumb, 9, a.theme(), false)
}

func placeholder(g *G, x, y, s int, e *Entry, theme RGB) {
	g.dsSquare(x, y, s, s, theme, false)
	r := []rune(strings.ToUpper(e.Name))
	if len(r) > 0 {
		scale := max(s/16, 1)
		g.textC(x+s/2, y+s/2-5*scale, string(r[0]), inkOn(theme), scale, false)
	}
}

// wrap splits s into lines no wider than w.
func wrap(s string, w int, bold bool) []string {
	words := strings.Fields(s)
	var lines []string
	cur := ""
	for _, wd := range words {
		try := strings.TrimSpace(cur + " " + wd)
		if textW(try, 1, bold) <= w || cur == "" {
			cur = try
			continue
		}
		lines = append(lines, cur)
		cur = wd
	}
	if cur != "" {
		lines = append(lines, cur)
	}
	for i, l := range lines {
		lines[i] = fit(l, w, 1, bold)
	}
	if len(lines) == 0 {
		return []string{""}
	}
	return lines
}

// dsBattery is the DS status-bar battery, pixel for pixel: slate top edge, black bottom edge,
// a shaded body (dark rim, bright middle) and a dark nub. Green above 50%, yellow from 21 to
// 50%, red at 20% or less, grey when the level is unknown.
var dsBattery = []string{
	"OOOOOOOOOOOO  ",
	"KDDDDDDDDDDD  ",
	"KMGGGGGGGGGEN ",
	"KMBBBBBBBBBENn",
	"KMBBBBBBBBBENn",
	"KMbbbbbbbbbEN ",
	"KLLLLLLLLLLL  ",
	"XXXXXXXXXXXX  ",
}

var (
	batGreen = map[byte]RGB{'O': hex(0x2f3e45), 'K': hex(0x0d2c12), 'D': hex(0x14421c), 'M': hex(0x299139),
		'G': hex(0x2bb33f), 'B': hex(0x30f749), 'b': hex(0x2fe947), 'E': hex(0x20682d), 'L': hex(0x278236),
		'X': hex(0x020803), 'N': hex(0x1d5e28), 'n': hex(0x192126)}
	batYellow = map[byte]RGB{'O': hex(0x2f3e45), 'K': hex(0x2c2a0d), 'D': hex(0x423f14), 'M': hex(0x918a29),
		'G': hex(0xb3a82b), 'B': hex(0xf7e43a), 'b': hex(0xe9d735), 'E': hex(0x686020), 'L': hex(0x827a27),
		'X': hex(0x080802), 'N': hex(0x5e581d), 'n': hex(0x262419)}
	batRed = map[byte]RGB{'O': hex(0x2f3e45), 'K': hex(0x2c0d0d), 'D': hex(0x421414), 'M': hex(0x912929),
		'G': hex(0xb32b2b), 'B': hex(0xf74a3a), 'b': hex(0xe94235), 'E': hex(0x682020), 'L': hex(0x822727),
		'X': hex(0x080202), 'N': hex(0x5e1d1d), 'n': hex(0x261919)}
	batGrey = map[byte]RGB{'O': hex(0x3a3a3a), 'K': hex(0x202020), 'D': hex(0x3a3a3a), 'M': hex(0x707070),
		'G': hex(0x8a8a8a), 'B': hex(0xb0b0b0), 'b': hex(0xa8a8a8), 'E': hex(0x555555), 'L': hex(0x666666),
		'X': hex(0x080808), 'N': hex(0x444444), 'n': hex(0x222222)}
)

func drawDSBattery(g *G, x, y int, pal map[byte]RGB) {
	for j, row := range dsBattery {
		for i := 0; i < len(row); i++ {
			if c, ok := pal[row[i]]; ok {
				g.px(x+i, y+j, c)
			}
		}
	}
}

// drawStatus draws Wi-Fi bars and the battery, right-aligned at rx, in ink.
func drawStatus(a *App, rx, y int, ink RGB) {
	g := a.g
	st := a.status.snapshot()
	bw := len(dsBattery[0])
	bx := rx - bw
	pal := batGrey // level unknown
	switch {
	case st.Battery > 50:
		pal = batGreen // full
	case st.Battery > 20:
		pal = batYellow // middle
	case st.Battery >= 0:
		pal = batRed // close to empty
	}
	drawDSBattery(g, bx, y+1, pal)
	if st.Battery >= 0 {
		label := fmt.Sprintf("%d%%", st.Battery)
		if st.Charging {
			label = "+" + label
		}
		g.textR(bx-3, y, label, ink, 1, false)
		bx -= textW(label, 1, false) + 5
	}
	wx := bx - 13
	for i := 0; i < 4; i++ {
		hgt := 2 + i*2
		if st.WifiOn && i < st.WifiLevel {
			g.rect(wx+i*3, y+9-hgt, 2, hgt, ink)
		} else {
			g.rect(wx+i*3, y+8, 2, 1, ink)
		}
	}
}
