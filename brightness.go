package main

// Brightness keys work on every screen of Duo, next to the volume keys: on the Cardputer
// keyboard the Fn layer sends BRIGHTNESSDOWN for Fn+U and BRIGHTNESSUP for Fn+I. They step the
// backlight in tenths (never below 5%, so the screen can't be turned black by accident) and
// show a meter like the volume one.

import (
	"dslauncher/cp0"
	"fmt"
	"math"
	"time"
)

const (
	briSteps = 10
	briMin   = 5 // percent
	briShow  = 1500 * time.Millisecond
)

type briState struct {
	pct       int
	known     bool
	shownTill time.Time
}

func isBrightnessKey(k cp0.Key) bool {
	return k.Name == "BRIGHTNESSUP" || k.Name == "BRIGHTNESSDOWN"
}

// brightnessKey handles a brightness key (presses and key repeats).
func (a *App) brightnessKey(k cp0.Key) {
	b := &a.bri
	cur, maxv := backlight()
	if !time.Now().Before(b.shownTill) || !b.known {
		switch {
		case maxv > 0 && cur > 0:
			b.pct, b.known = int(math.Round(float64(cur)*100/float64(maxv))), true
		case a.sim:
			b.pct, b.known = 70, true
		}
	}
	if !b.known {
		a.notify("No brightness control found", true)
		return
	}
	step := 100 / briSteps
	switch k.Name {
	case "BRIGHTNESSUP":
		b.pct = min(100, (b.pct/step+1)*step)
	case "BRIGHTNESSDOWN":
		b.pct = max(briMin, ((b.pct+step-1)/step-1)*step)
	}
	b.shownTill = time.Now().Add(briShow)
	if !a.sim && maxv > 0 {
		v := max(1, b.pct*maxv/100)
		if setBacklight(v) == nil {
			a.savedLight = v
		}
	}
	a.snd.Play("tick")
	if l, ok := a.top().(*RowList); ok && l.Title == "SCREEN" && len(l.Rows) >= 1 {
		l.Rows[0].Num = b.pct // keep Settings > Screen in step
	}
	a.dirty = true
}

// drawBrightness is the brightness meter: a sun, ten bars and the level.
func (a *App) drawBrightness() {
	g := a.g
	b := a.bri
	w, h := 214, 26
	x, y := (W-w)/2, 24
	g.dsStatus(x, y, w, h)
	drawSun(g, x+14, y+h/2, cWhite)
	filled := b.pct / (100 / briSteps)
	bx := x + 30
	for i := 0; i < briSteps; i++ {
		c := hex(0x505050)
		if i < filled {
			c = mix(hex(0x8a7a30), hex(0xfbd300), float64(i)/float64(briSteps-1))
		}
		g.rect(bx+i*14, y+7, 11, h-14, c)
	}
	g.textR(x+w-8, y+8, fmt.Sprintf("%d%%", b.pct), cWhite, 1, false)
}

// drawSun is a small sun: a disc with eight rays.
func drawSun(g *G, cx, cy int, c RGB) {
	g.disc(float64(cx)+0.5, float64(cy)+0.5, 3.2, c)
	for i := 0; i < 8; i++ {
		ang := float64(i) * math.Pi / 4
		sx, sy := math.Cos(ang), math.Sin(ang)
		g.aaline(float64(cx)+0.5+sx*5, float64(cy)+0.5+sy*5, float64(cx)+0.5+sx*7.5, float64(cy)+0.5+sy*7.5, 1.4, c)
	}
}
