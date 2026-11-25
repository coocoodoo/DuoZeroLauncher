package main

// Low-battery warning, DS style: at 15% and again at 5% an alert box says so (once each, until
// the battery is charging or back above 20%). With the screen off it waits until you wake it.

import (
	"dslauncher/cp0"
	"fmt"
	"time"
)

const (
	batLow      = 15
	batCritical = 5
	batReset    = 20
)

// checkBattery shows the warning when the charge crosses a threshold.
func (a *App) checkBattery() {
	st := a.status.snapshot()
	if st.Battery < 0 {
		return
	}
	if st.Charging || st.Battery > batReset {
		a.batWarned = 0
		return
	}
	level := 0
	switch {
	case st.Battery <= batCritical:
		level = 2
	case st.Battery <= batLow:
		level = 1
	}
	if level <= a.batWarned || a.dimLevel == 2 || a.launch != nil || a.busy() != "" {
		return
	}
	if a.dimLevel == 1 {
		a.wake()
	}
	a.batWarned = level
	if b, ok := a.top().(*BatteryAlert); ok {
		b.pct, b.critical = st.Battery, level == 2
	} else {
		a.push(&BatteryAlert{pct: st.Battery, critical: level == 2, at: time.Now()})
	}
	a.snd.Play("notice")
	a.dirty = true
}

// BatteryAlert is the warning box over whatever was on screen.
type BatteryAlert struct {
	pct      int
	critical bool
	at       time.Time
}

func (v *BatteryAlert) Key(a *App, k cp0.Key) {
	if k.Name == "ENTER" || k.Back() || k.Slot() == 0 || k.Slot() == 4 {
		a.snd.Play("back")
		a.pop()
	}
}

func (v *BatteryAlert) Bar(a *App) []BarItem {
	return []BarItem{{"OK", artOK, false}, {}, {}, {}, {}}
}

func (v *BatteryAlert) Animating(a *App) bool { return true } // the battery blinks

func (v *BatteryAlert) Draw(a *App) {
	if len(a.stack) >= 2 {
		a.stack[len(a.stack)-2].Draw(a)
	}
	g := a.g
	g.noClip()
	g.shade(0, 0, W, 132, cBlack, 0.35)
	title := "BATTERY LOW"
	lines := []string{fmt.Sprintf("The battery is at %d%%.", v.pct), "Please charge your Cardputer."}
	if v.critical {
		title = "BATTERY ALMOST EMPTY"
		lines = []string{fmt.Sprintf("Only %d%% left.", v.pct), "Charge now, or the Cardputer", "will turn off soon."}
	}
	h := 38 + len(lines)*12
	x, y, w := 40, (132-h)/2, W-80
	g.dsAlert(x, y, w, h)
	g.textC(W/2, y+10, title, cPictoEnter, 1, false)
	for i, l := range lines {
		g.textC(W/2, y+24+i*12, l, cWhite, 1, false)
	}
	// a big red battery that blinks, like the DS power lamp
	if int(time.Since(v.at).Seconds()*2)%2 == 0 {
		bx, by := x+14, y+h/2-5
		g.rect(bx, by, 16, 10, hex(0x2c0d0d))
		g.rect(bx+16, by+3, 2, 4, hex(0x2c0d0d))
		g.rect(bx+1, by+1, max(2, 14*v.pct/100), 8, hex(0xf74a3a))
	}
}
