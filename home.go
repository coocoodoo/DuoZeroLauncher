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
	"sort"
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

	tileRise   = 6  // px the chosen tile stands up
	launchRise = 24 // px more it rises when its app opens
	launchDur  = 0.45
)

// cShadow is the colour of the shadow under raised tiles.
var cShadow = hex(0x18202c)

type Home struct {
	sel      int
	slide    float64 // strip offset in tiles, eases to 0
	last     time.Time
	key      string
	calendar bool
	// recent: the strip shows the recently opened apps (Up) instead of all of them (Down).
	// allKey remembers the choice in the full list while recents are shown.
	recent bool
	allKey string
	// lift is how far each app's tile stands up, 0..1: the chosen one eases up, the one left
	// behind eases back down.
	lift map[string]float64
	// the open animation: launching is set while it plays; selX/selY is the chosen tile's
	// resting place (from the last drawLower).
	launching  bool
	selX, selY int
}

func newHome(a *App) *Home { return &Home{last: time.Now()} }

// recentApps are the apps opened before, newest first (at most 8).
func recentApps(a *App) []*Entry {
	var r []*Entry
	for _, e := range a.apps {
		if a.cfg.Used[e.Key] > 0 {
			r = append(r, e)
		}
	}
	sort.SliceStable(r, func(i, j int) bool { return a.cfg.Used[r[i].Key] > a.cfg.Used[r[j].Key] })
	if len(r) > 8 {
		r = r[:8]
	}
	return r
}

// list is what the strip shows: the recent apps or all of them.
func (h *Home) list(a *App) []*Entry {
	if h.recent {
		if r := recentApps(a); len(r) > 0 {
			return r
		}
		h.recent = false
	}
	return a.apps
}

// showRecent switches the strip between the recent apps and all of them.
func (h *Home) showRecent(a *App, on bool) {
	if on == h.recent {
		a.snd.Play("bump")
		return
	}
	if on {
		if len(recentApps(a)) == 0 {
			a.notify("No recent apps yet", false)
			return
		}
		if e := h.cur(a); e != nil {
			h.allKey = e.Key
		}
		h.recent, h.sel, h.slide = true, 0, 0
		h.key = h.list(a)[0].Key
	} else {
		h.recent = false
		h.key = h.allKey
		h.keep(a)
	}
	a.snd.Play("open")
}

func (h *Home) cur(a *App) *Entry {
	l := h.list(a)
	if len(l) == 0 {
		return nil
	}
	h.sel = (h.sel%len(l) + len(l)) % len(l)
	return l[h.sel]
}

// keep re-finds the selected app after the list changes.
func (h *Home) keep(a *App) {
	l := h.list(a)
	for i, e := range l {
		if e.Key == h.key {
			h.sel = i
			return
		}
	}
	h.sel = min(h.sel, max(len(l)-1, 0))
}

func (h *Home) move(a *App, d int) {
	l := h.list(a)
	n := len(l)
	if n == 0 {
		return
	}
	h.sel = ((h.sel+d)%n + n) % n
	h.slide += float64(d)
	h.slide = math.Max(-3, math.Min(3, h.slide))
	h.key = l[h.sel].Key
	a.snd.Play("move")
}

func (h *Home) Animating(a *App) bool {
	if math.Abs(h.slide) > 0.02 {
		return true
	}
	key := ""
	if e := h.cur(a); e != nil {
		key = e.Key
	}
	for id, v := range h.lift {
		if (id == key && v < 1) || (id != key && v > 0) {
			return true
		}
	}
	return key != "" && h.lift[key] < 1
}

// ease moves every tile's lift towards its target: up for the chosen app, down for the rest.
func (h *Home) ease(a *App, dt float64) {
	if h.lift == nil {
		h.lift = map[string]float64{}
	}
	key := ""
	if e := h.cur(a); e != nil {
		key = e.Key
		if _, ok := h.lift[key]; !ok {
			h.lift[key] = 0
		}
	}
	k := 1 - math.Exp(-dt*12)
	for id, v := range h.lift {
		target := 0.0
		if id == key {
			target = 1
		}
		v += (target - v) * k
		if math.Abs(target-v) < 0.01 {
			v = target
		}
		if v == 0 && id != key {
			delete(h.lift, id)
			continue
		}
		h.lift[id] = v
	}
}

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
		h.showRecent(a, true)
	case k.Name == "DOWN":
		h.showRecent(a, false)
	case k.Name == "SPACE" || k.Ch == '/':
		a.snd.Play("open")
		a.push(&Search{})
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

// frameDT is how far the animations move this frame. While the home screen sits still it is
// drawn only about once a second (for the clock), so a long gap since the last frame means it
// was idle: count it as one frame. Otherwise the first frame after a key press, up to a second
// after the last one, would jump the slide and the tile's rise straight to their end.
func (h *Home) frameDT() float64 {
	now := time.Now()
	dt := now.Sub(h.last).Seconds()
	h.last = now
	if dt > 0.1 || dt < 0 {
		dt = 1.0 / 30
	}
	return dt
}

func (h *Home) Draw(a *App) {
	dt := h.frameDT()
	h.slide *= math.Exp(-dt * 16)
	if math.Abs(h.slide) < 0.02 {
		h.slide = 0
	}
	h.ease(a, dt)
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
		g.artIcon(paneL-9, 8, artBell, 11, ink)
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
	list := h.list(a)
	n := len(list)
	title := "Apps"
	if h.recent {
		title = "Recent"
	}
	ax := g.text(x0+5, 4, title, ink, 1, false)
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
		g.artIcon(W-8, iy+7, artStar, 11, cFocus)
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

	// the strip: resting tiles first, then the raised ones over their shadows
	sy := iy + 55
	by := sy + tileW + 6 // scroll bar
	mid := x0 + paneRW/2 - tileW/2
	old := g.clip
	g.setClip(x0, iy+48, paneRW, by-(iy+48))
	type slot struct {
		e    *Entry
		x    int
		lift float64
	}
	var slots []slot
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
		it := list[i]
		slots = append(slots, slot{it, tx, smoothstep(h.lift[it.Key])})
	}
	sort.SliceStable(slots, func(p, q int) bool { return slots[p].lift < slots[q].lift })
	for _, s := range slots {
		if s.e == e {
			h.selX, h.selY = s.x, sy
			if h.launching {
				continue // drawLaunch draws it, over the white
			}
		}
		drawTile(a, s.e, s.x, sy, s.lift*tileRise, s.lift)
	}
	g.clip = old

	// scroll bar with arrows
	g.artIcon(x0+8, by+4, artLeft, 10, cBorder)
	g.artIcon(W-8, by+4, artRight, 10, cBorder)
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

// drawTile draws an app's square button resting at x,y, raised by rise px. lift (0..1) fades
// its colour from grey to the favourite colour and casts its shadow on the paper below.
func drawTile(a *App, e *Entry, x, y int, rise, lift float64) {
	g := a.g
	if lift > 0.01 {
		// a soft shadow all round, heavier below: it marks the chosen app
		sy := y + 2 - int(math.Round(rise/2))
		g.softShadow(x, sy, tileW, tileW, 2+4*lift, 0.55*lift, cShadow)
	}
	ty := y - int(math.Round(rise))
	g.dsSquare(x, ty, tileW, tileW, mix(cButton, a.theme(), lift), false)
	if ic := e.Icon(26); ic != nil {
		g.icon(x+4, ty+4, ic, 1)
	} else {
		placeholder(g, x+4, ty+4, 26, e, a.theme())
	}
}

// drawLaunch draws the open animation over a normal frame (drawn with h.launching set, so the
// chosen tile is left out) at p (0..1): everything fades to white while the tile rises from
// where it stood, its shadow spreading below; the app's name fades in under it.
func (h *Home) drawLaunch(a *App, e *Entry, from, p float64) {
	g := a.g
	g.noClip()
	g.shade(0, 0, W, H, cWhite, smoothstep(p))
	up := 1 - math.Pow(1-p, 3) // ease out
	rise := from + (tileRise+launchRise-from)*up
	x, y := h.selX, h.selY
	// the shadow stays on the paper, spreading and fading as the tile lifts away from it
	g.softShadow(x+2, y+2, tileW-4, tileW-4, 6+10*up, 0.5-0.32*up, cShadow)
	ty := y - int(math.Round(rise))
	g.dsSquare(x, ty, tileW, tileW, a.theme(), false)
	if ic := e.Icon(26); ic != nil {
		g.icon(x+4, ty+4, ic, 1)
	} else {
		placeholder(g, x+4, ty+4, 26, e, a.theme())
	}
	if p > 0.4 {
		t := smoothstep((p - 0.4) / 0.6)
		g.textC(x+tileW/2, y+tileW+16, fit(e.Name, 180, 1, false), mix(cWhite, cDark, t), 1, false)
	}
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
