package main

// Volume keys work on every screen of Duo: on the Cardputer keyboard the Fn layer sends
// VOLUMEDOWN for Fn+S, VOLUMEUP for Fn+D and MUTE for Fn+A. They change the speaker volume
// with wpctl (off the UI thread, newest request wins) and show a volume meter for a moment.

import (
	"dslauncher/cp0"
	"fmt"
	"time"
)

const (
	volStep = 5
	volShow = 1500 * time.Millisecond
)

type volState struct {
	pct       int
	muted     bool
	known     bool // pct/muted read from the system recently enough to trust
	readAt    time.Time
	shownTill time.Time
	req       chan volReq // to volumeWorker; holds at most the newest request
}

type volReq struct {
	pct   int
	muted bool
}

func isVolumeKey(k cp0.Key) bool {
	return k.Name == "VOLUMEUP" || k.Name == "VOLUMEDOWN" || k.Name == "MUTE"
}

// volumeKey handles a volume key (presses and key repeats).
func (a *App) volumeKey(k cp0.Key) {
	v := &a.vol
	showing := time.Now().Before(v.shownTill)
	if !v.known || (!showing && time.Since(v.readAt) > 5*time.Second) {
		// read it again unless we are in the middle of a burst of presses
		if pct, muted, ok := volume(); ok {
			v.pct, v.muted, v.known = pct, muted, true
		} else if a.sim {
			v.pct, v.known = 50, true
		}
		v.readAt = time.Now()
	}
	if !v.known {
		a.notify("No volume control found", true)
		return
	}
	switch k.Name {
	case "VOLUMEUP":
		v.pct = min(100, (v.pct/volStep+1)*volStep)
		v.muted = false
	case "VOLUMEDOWN":
		v.pct = max(0, ((v.pct+volStep-1)/volStep-1)*volStep)
	case "MUTE":
		if k.Repeat {
			return
		}
		v.muted = !v.muted
	}
	v.shownTill = time.Now().Add(volShow)
	a.sendVolume(volReq{v.pct, v.muted})
	if l, ok := a.top().(*RowList); ok && l.Title == "SPEAKER" && len(l.Rows) >= 2 {
		l.Rows[0].Num, l.Rows[1].On = v.pct, v.muted // keep Settings > Speaker in step
	}
	a.dirty = true
}

// sendVolume hands the wanted volume to the worker, replacing a request it hasn't taken yet.
// Only the UI thread sends, so after the drain the send can't block.
func (a *App) sendVolume(r volReq) {
	if a.vol.req == nil {
		a.vol.req = make(chan volReq, 1)
		go a.volumeWorker(a.vol.req)
	}
	select {
	case <-a.vol.req:
	default:
	}
	a.vol.req <- r
}

func (a *App) volumeWorker(ch chan volReq) {
	var last volReq
	first := true
	for r := range ch {
		if a.sim {
			continue // scripted runs leave the real volume alone
		}
		if first || r.pct != last.pct {
			setVolume(r.pct)
		}
		if first || r.muted != last.muted {
			setMute(r.muted)
		}
		last, first = r, false
		if !r.muted {
			a.work <- func() { a.snd.Play("tick") } // heard at the new volume
		}
	}
}

// drawVolume is the volume meter: a speaker, a staircase of twenty bars and the level.
func (a *App) drawVolume() {
	g := a.g
	v := a.vol
	const n = 20
	w, h := 214, 26
	x, y := (W-w)/2, 24
	g.dsStatus(x, y, w, h)
	ink := cWhite
	if v.muted {
		ink = hex(0x9a9a9a)
	}
	g.artIcon(x+14, y+h/2, artSpeaker, 13, ink)
	if v.muted { // a slash through the speaker
		for i := -6; i <= 6; i++ {
			g.px(x+14+i, y+h/2+i, hex(0xff6060))
			g.px(x+15+i, y+h/2+i, hex(0xff6060))
		}
	}
	filled := v.pct / volStep
	bx := x + 28
	for i := 0; i < n; i++ {
		bh := 4 + i*10/(n-1) // 4..14 px, rising
		c := hex(0x505050)
		if i < filled {
			c = a.theme()
			if v.muted {
				c = hex(0x7a7a7a)
			}
		}
		g.rect(bx+i*7, y+h-6-bh, 5, bh, c)
	}
	label := fmt.Sprintf("%d%%", v.pct)
	if v.muted {
		label = "MUTE"
	}
	g.textR(x+w-8, y+8, label, ink, 1, false)
}
