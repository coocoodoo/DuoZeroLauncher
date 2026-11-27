package main

// Set up a stereo (Settings > Duo Link): turns Duo Link and the Cardputer's own Wi-Fi on, then
// walks through the stereo's side one step at a time, moving on by itself when it sees the
// stereo join the Wi-Fi, download the app and connect:
//
//	1. join this Wi-Fi (name and password shown big)
//	2. in the stereo's browser type 10.42.0.1:47140, download and install Duo Link
//	3. open Duo Link (it finds the Cardputer; the pairing code pops up over this screen)
//	then: all set.

import (
	"dslauncher/cp0"
	"fmt"
	"sync/atomic"
	"time"
)

const (
	setupJoin = iota
	setupDownload
	setupOpen
	setupDone
)

type LinkSetup struct {
	ssid, pass string
	addr       string // 10.42.0.1:47140
	step       int
	stations   int  // devices on the Cardputer's Wi-Fi (last look)
	joined     bool // a device joined since the screen opened
	dl0        int  // app downloads when the screen opened
	gotApp     bool
	linked     bool
	closed     atomic.Bool
}

// startLinkSetup turns everything on (Wi-Fi first: it takes a few seconds) and opens the steps.
func startLinkSetup(a *App) {
	a.cfg.LinkOn, a.cfg.LinkWifi = true, true
	a.save()
	if err := a.link.start(); err != nil {
		alert(a, "DUO LINK", "Duo Link can't start: "+err.Error())
		return
	}
	open := func() {
		ssid, pass := a.link.hotspotCreds()
		v := newLinkSetup(a, ssid, pass)
		a.push(v)
		v.watch(a)
	}
	if a.linkWifiUp {
		open()
		return
	}
	a.startLinkWifi(true, func(err error) {
		if err != nil {
			alert(a, "DUO LINK WI-FI", "The Cardputer couldn't make its Wi-Fi: "+err.Error())
			return
		}
		open()
	})
}

func newLinkSetup(a *App, ssid, pass string) *LinkSetup {
	v := &LinkSetup{ssid: ssid, pass: pass, addr: fmt.Sprintf("%s:%d", hotspotIP, a.link.port()),
		dl0: a.link.apkDownloads()}
	if a.link.connected() != "" {
		v.linked, v.step = true, setupDone
	}
	return v
}

// watch looks at the Wi-Fi every 2 s while the screen is open (on its own goroutine; results go
// through a.work to the UI thread).
func (v *LinkSetup) watch(a *App) {
	go func() {
		for !v.closed.Load() && !a.quit {
			n := hotspotStations()
			a.work <- func() { v.update(a, n) }
			time.Sleep(2 * time.Second)
		}
	}()
}

// update moves on when something new happened on the stereo's side.
func (v *LinkSetup) update(a *App, stations int) {
	open := false
	for _, s := range a.stack {
		if s == View(v) {
			open = true
		}
	}
	if !open {
		v.closed.Store(true)
		return
	}
	v.stations = stations
	was := v.step
	if stations > 0 && !v.joined {
		v.joined = true
		if v.step == setupJoin {
			v.step = setupDownload
		}
	}
	if a.link.apkDownloads() > v.dl0 && !v.gotApp {
		v.gotApp = true
		if v.step < setupOpen {
			v.step = setupOpen
		}
	}
	if a.link.connected() != "" && !v.linked {
		v.linked = true
		v.step = setupDone
	}
	if v.step != was {
		a.snd.Play(map[bool]string{true: "launch", false: "notice"}[v.step == setupDone])
	}
	a.dirty = true
}

func (v *LinkSetup) close(a *App) {
	v.closed.Store(true)
	a.snd.Play("back")
	a.pop()
}

func (v *LinkSetup) Key(a *App, k cp0.Key) {
	switch {
	case k.Back() || k.Slot() == 4:
		v.close(a)
	case k.Name == "ENTER" || k.Slot() == 0 || k.Name == "RIGHT":
		if v.step == setupDone {
			v.close(a)
			return
		}
		v.step++
		a.snd.Play("select")
	case (k.Slot() == 1 || k.Name == "LEFT") && v.step > setupJoin:
		v.step--
		a.snd.Play("move")
	}
}

func (v *LinkSetup) Bar(a *App) []BarItem {
	if v.step == setupDone {
		return []BarItem{{"DONE", artOK, false}, {}, {}, {}, {"CLOSE", artBack, false}}
	}
	return []BarItem{{"NEXT", artRight, false}, {"BACK", artLeft, v.step == setupJoin}, {}, {}, {"CLOSE", artBack, false}}
}

func (v *LinkSetup) Draw(a *App) {
	g := a.g
	pageTitle(a, "DUO LINK SETUP", artLinkTile)
	x, y, w, h := 6, docTextTop, W-12, docTextHeight
	g.dsDetails(x, y, w, h)
	accent := a.theme().darker(0.25)
	if curTheme.Dark {
		accent = a.theme().lighter(0.25)
	}
	good := hex(0x1f8a3a)
	if curTheme.Dark {
		good = hex(0x45d66b)
	}
	grey := cSub
	tx := x + 8

	// which step, with a dot for each
	head := map[int]string{setupJoin: "STEP 1 OF 3", setupDownload: "STEP 2 OF 3", setupOpen: "STEP 3 OF 3",
		setupDone: "ALL SET"}[v.step]
	g.text(tx, y+5, head, accent, 1, true)
	for i := 0; i < 3; i++ {
		c := cGridLine
		if i < v.step {
			c = good
		} else if i == v.step {
			c = accent
		}
		g.disc(float64(x+w-40+i*12), float64(y+11), 4, c)
	}

	status := func(done bool, wait, ok string) {
		if done {
			g.text(tx, y+h-17, "OK: "+ok, good, 1, true)
		} else {
			dots := []string{"", ".", "..", "..."}[int(a.t*2)%4]
			g.text(tx, y+h-17, wait+dots, grey, 1, false)
		}
	}
	big := func(cy int, label, val string) {
		g.text(tx, cy+6, label, cText, 1, false)
		g.text(tx+76, cy, fit(val, w-92, 2, false), cText, 2, false)
	}

	switch v.step {
	case setupJoin:
		g.text(tx, y+21, "On the stereo: Settings > Wi-Fi, join", cText, 1, false)
		big(y+38, "Wi-Fi", v.ssid)
		big(y+62, "Password", v.pass)
		status(v.joined, "Waiting for the stereo to join", "the stereo joined. Press NEXT.")
	case setupDownload:
		g.text(tx, y+21, "In the stereo's web browser, type:", cText, 1, false)
		g.textC(x+w/2, y+40, fit(v.addr, w-16, 2, false), cText, 2, false)
		g.text(tx, y+68, "Tap Download Duo Link, then install it.", cText, 1, false)
		status(v.gotApp, "Waiting for the download", "downloaded. Install it, then NEXT.")
	case setupOpen:
		g.text(tx, y+21, "Open Duo Link on the stereo.", cText, 1, false)
		g.text(tx, y+37, "It finds this Cardputer by itself. The", cText, 1, false)
		g.text(tx, y+50, "first time, a code shows here: type it", cText, 1, false)
		g.text(tx, y+63, "on the stereo.", cText, 1, false)
		status(v.linked, "Waiting for Duo Link", "connected.")
	case setupDone:
		n := a.link.connected()
		if n == "" {
			n = "The stereo"
		}
		g.text(tx, y+21, fit(n, w-20, 1, true)+" shows this screen now.", cText, 1, false)
		g.text(tx, y+40, "Next time, it joins the Cardputer's Wi-Fi", cText, 1, false)
		g.text(tx, y+53, "and Duo Link connects by itself.", cText, 1, false)
		g.text(tx, y+72, "USB only charges: Duo Link uses Wi-Fi.", grey, 1, false)
	}
}

func (v *LinkSetup) Animating(a *App) bool { return v.step != setupDone }
