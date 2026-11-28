package main

// Duo Link inside Duo: the keys from the stereo, the pairing screen, the settings page.

import (
	"dslauncher/cp0"
	"encoding/binary"
	"fmt"
	"os"
	"strconv"
	"sync"
)

// linkKeyboard writes key events into the Cardputer keyboard's event device, so Duo and any app
// reading it get them as if typed. The kernel passes on only keys the keyboard declares, so
// those are read first (supports).
type linkKeyboard struct {
	mu    sync.Mutex
	f     *os.File
	tried bool
	caps  []byte // EV_KEY capability bits; nil if they couldn't be read
}

func (k *linkKeyboard) open() bool {
	if !k.tried {
		k.tried = true
		if f, err := os.OpenFile(cp0.KeyboardPath(), os.O_WRONLY, 0); err == nil {
			k.f = f
			k.caps = evKeyBits(cp0.KeyboardPath())
		}
	}
	return k.f != nil
}

func (k *linkKeyboard) supports(code int) bool {
	k.mu.Lock()
	defer k.mu.Unlock()
	if !k.open() {
		return false
	}
	if k.caps == nil {
		return true
	}
	return code/8 < len(k.caps) && k.caps[code/8]&(1<<(code%8)) != 0
}

func (k *linkKeyboard) write(code int, down bool) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	if !k.open() {
		return os.ErrNotExist
	}
	val := int32(0)
	if down {
		val = 1
	}
	ev := inputEvent(1, code, val) // EV_KEY
	ev = append(ev, inputEvent(0, 0, 0)...)
	_, err := k.f.Write(ev)
	return err
}

// inputEvent is a struct input_event: a timeval (two longs), type, code, value.
func inputEvent(typ, code int, val int32) []byte {
	tv := strconv.IntSize / 8 * 2
	b := make([]byte, tv+8)
	binary.LittleEndian.PutUint16(b[tv:], uint16(typ))
	binary.LittleEndian.PutUint16(b[tv+2:], uint16(code))
	binary.LittleEndian.PutUint32(b[tv+4:], uint32(val))
	return b
}

// arrowNav: the plain letter keys Duo and cp0 apps read as arrows, for a keyboard without arrows.
var arrowNav = map[int]int{103: 33, 108: 45, 105: 44, 106: 46}

// linkKey presses or releases a key for the stereo (called from the connection's goroutine).
func (a *App) linkKey(code int, down bool) {
	if a.linkKbd.supports(code) && a.linkKbd.write(code, down) == nil {
		return
	}
	if !a.appRunning.Load() { // Duo is in front: hand it the key directly
		if rk, ok := a.keys.(realKeys); ok {
			v := 0
			if down {
				v = 1
			}
			rk.k.Inject(code, v)
		}
		return
	}
	if n, ok := arrowNav[code]; ok && a.linkKbd.supports(n) { // another app: arrows as F/X/Z/C
		a.linkKbd.write(n, down)
	}
}

func (a *App) newLink() *linkServer {
	raw := a.scr.CaptureBuffer() // reused for every frame, 25 a second (CaptureInto holds the screen's lock while using it)
	return newLinkServer(linkHooks{
		name:    func() string { return hostname() },
		capture: func(px []uint16) error { return a.scr.CaptureInto(px, raw) },
		key:     a.linkKey,
		pair: func(stereo, code string, cancel func()) {
			a.work <- func() {
				if a.dimLevel > 0 {
					a.wake()
				}
				a.push(&LinkPair{stereo: stereo, code: code, cancel: cancel})
				a.snd.Play("notice")
			}
		},
		paired: func() {
			a.work <- func() {
				keep := a.stack[:1]
				for _, v := range a.stack[1:] {
					if _, ok := v.(*LinkPair); !ok {
						keep = append(keep, v)
					}
				}
				a.stack = keep
				a.dirty = true
			}
		},
		audio: func() bool { return a.cfg.LinkAudio },
		changed: func() {
			a.work <- func() {
				if n := a.link.connected(); n != "" {
					a.notify("Duo Link: "+n+" connected", false)
				}
				a.refreshLinkPage()
			}
		},
	})
}

// refreshLinkPage reloads Settings > Duo Link if it is on screen.
func (a *App) refreshLinkPage() {
	if l, ok := a.top().(*RowList); ok && l.Tag == "duolink" && l.Refresh != nil {
		l.Refresh(a, l)
	}
	a.dirty = true
}

// startLink turns Duo Link on (or says why it can't), with the Cardputer's own Wi-Fi if chosen.
func (a *App) startLink() {
	if err := a.link.start(); err != nil {
		a.notify("Duo Link can't start: "+err.Error(), true)
		return
	}
	if a.cfg.LinkWifi && !a.linkWifiUp {
		a.startLinkWifi(false, func(err error) {
			if err != nil {
				a.notify("Duo Link Wi-Fi: "+err.Error(), true)
			}
		})
	}
}

// stopLink turns Duo Link off, and its Wi-Fi.
func (a *App) stopLink() {
	a.link.shutdown()
	a.stopLinkWifi()
}

// startLinkWifi brings up the Cardputer's own Wi-Fi off the UI thread (with the busy screen if
// busy); done runs on the UI thread.
func (a *App) startLinkWifi(busy bool, done func(err error)) {
	ssid, pass := a.link.hotspotCreds()
	job := func() error { return startHotspot(ssid, pass) }
	finish := func(err error) {
		a.linkWifiUp = err == nil
		if done != nil {
			done(err)
		}
		a.refreshLinkPage()
	}
	if busy {
		a.background("Turning on the Cardputer's Wi-Fi...", job, finish)
		return
	}
	go func() {
		err := job()
		a.work <- func() { finish(err) }
	}()
}

func (a *App) stopLinkWifi() {
	if !a.linkWifiUp {
		return
	}
	a.linkWifiUp = false
	go stopHotspot()
}

// LinkPair shows the code a new stereo has to type.
type LinkPair struct {
	stereo, code string
	cancel       func()
}

func (v *LinkPair) Key(a *App, k cp0.Key) {
	if k.Back() || k.Slot() == 4 {
		a.snd.Play("back")
		v.cancel()
		a.pop()
	}
}

func (v *LinkPair) Bar(a *App) []BarItem {
	return []BarItem{{}, {}, {}, {}, {"REFUSE", artNo, false}}
}

func (v *LinkPair) Draw(a *App) {
	if len(a.stack) >= 2 {
		a.stack[len(a.stack)-2].Draw(a)
	}
	g := a.g
	g.noClip()
	g.shade(0, 0, W, 132, cBlack, 0.35)
	x, y, w, h := 30, 14, W-60, 106
	g.dsAlert(x, y, w, h)
	g.textC(W/2, y+9, "DUO LINK", cPictoEnter, 1, false)
	g.textC(W/2, y+23, fit(v.stereo, w-20, 1, false)+" wants to show this screen.", cWhite, 1, false)
	g.textC(W/2, y+36, "Type this code on it:", cWhite, 1, false)
	code := v.code[:2] + " " + v.code[2:]
	g.fontText(fontClock30, W/2-fontClock30.width(code)/2, y+50, code, cWhite)
	g.textC(W/2, y+92, "Not yours? Press 8 or Esc to refuse.", hex(0xb0b0b0), 1, false)
}

// linkPage is Settings > Launcher > Duo Link.
func linkPage(a *App) View {
	l := page("DUO LINK", artLauncher)
	l.Tag = "duolink"
	l.Refresh = func(a *App, l *RowList) {
		status := "OFF"
		if a.link.isRunning() {
			status = "WAITING FOR A STEREO"
			if a.cfg.LinkWifi && !a.linkWifiUp {
				status = "WI-FI NOT UP"
			}
			if n := a.link.connected(); n != "" {
				status = "SHOWING ON " + n
			}
		}
		addr := "NO NETWORK"
		if a.cfg.LinkWifi && a.linkWifiUp {
			addr = fmt.Sprintf("%s:%d", hotspotIP, a.link.port())
		} else if ip := primaryIP(); ip != "" {
			addr = fmt.Sprintf("%s:%d", ip, a.link.port())
		}
		ssid, pass := a.link.hotspotCreds()
		l.Rows = []*Row{
			{Label: "Duo Link", Kind: rowToggle, On: a.cfg.LinkOn, Do: func(a *App, r *Row) {
				a.cfg.LinkOn = r.On
				a.save()
				if r.On {
					a.startLink()
				} else {
					a.stopLink()
				}
				l.Refresh(a, l)
			}},
			{Label: "Set up a stereo", Kind: rowLink, Do: func(a *App, r *Row) { startLinkSetup(a) }},
			{Label: "Connect over", Kind: rowChoice, Choices: []string{"CARDPUTER WI-FI", "SHARED WI-FI"},
				Choice: btoi(!a.cfg.LinkWifi), Do: func(a *App, r *Row) {
					a.cfg.LinkWifi = r.Choice == 0
					a.save()
					switch {
					case a.cfg.LinkWifi && a.cfg.LinkOn:
						a.startLinkWifi(true, func(err error) {
							if err != nil {
								alert(a, "DUO LINK WI-FI", err.Error())
							}
						})
					case !a.cfg.LinkWifi:
						a.stopLinkWifi()
					}
					l.Refresh(a, l)
				}},
			{Label: "Sound on the stereo", Kind: rowToggle, On: a.cfg.LinkAudio, Do: func(a *App, r *Row) {
				a.cfg.LinkAudio = r.On
				a.save()
				a.notify("From the next time a stereo connects", false)
			}},
			{Label: "Status", Value: status},
		}
		if a.cfg.LinkWifi {
			l.Rows = append(l.Rows,
				&Row{Label: "Wi-Fi name", Value: ssid},
				&Row{Label: "Wi-Fi password", Value: pass})
		}
		l.Rows = append(l.Rows,
			&Row{Label: "Address", Value: addr},
			&Row{Label: "Paired stereos", Kind: rowAction, Value: itoa(a.link.stereos()), Do: func(a *App, r *Row) {
				if a.link.stereos() == 0 {
					a.notify("No stereos paired", false)
					return
				}
				confirm(a, "FORGET?", "Forget every paired stereo? Each one will need a new code.", func() {
					a.link.forget()
					l.Refresh(a, l)
				})
			}})
		if a.cfg.LinkWifi {
			l.Rows = append(l.Rows, &Row{Label: "New Wi-Fi password", Kind: rowAction, Do: func(a *App, r *Row) {
				confirm(a, "NEW PASSWORD?", "Make a new Wi-Fi password? Stereos that joined will have to join again with it.", func() {
					a.link.newHotspotPassword()
					if a.linkWifiUp {
						a.startLinkWifi(true, nil)
					}
					l.Refresh(a, l)
				})
			}})
		}
		l.Rows = append(l.Rows, &Row{Label: "How to set it up", Kind: rowLink, Do: func(a *App, r *Row) {
			a.push(&DocText{title: "DUO LINK", paras: linkHelp(addr)})
		}})
		l.Footer = "Shows this screen on an Android car stereo"
		if a.link.isRunning() && addr != "NO NETWORK" {
			l.Footer = "Get the app: http://" + addr
		}
	}
	l.Refresh(a, l)
	return l
}

func linkHelp(addr string) []string {
	url := "http://" + addr
	if addr == "NO NETWORK" {
		url = "http://" + hotspotIP + ":" + itoa(linkPort)
	}
	return []string{
		"Duo Link shows this screen on an Android car stereo, and the stereo's buttons press keys here. It works over Wi-Fi only: a USB cable just charges the Cardputer.",
		"",
		"The easy way: Set up a stereo (on this page) walks you through it:",
		"1. The Cardputer makes its own Wi-Fi and shows its name and password.",
		"2. On the stereo, join that Wi-Fi in Settings > Wi-Fi. If it says there's no internet, stay connected.",
		"3. In the stereo's browser, type " + url + " and install Duo Link from there.",
		"4. Open Duo Link on the stereo; it finds the Cardputer. Type the code this screen shows.",
		"",
		"After that, the stereo joins the Cardputer's Wi-Fi and Duo Link connects by itself whenever both are on.",
		"",
		"Connect over SHARED WI-FI instead if the stereo and the Cardputer are on the same network already (the stereo's own hotspot, say).",
		"",
		"On the stereo:",
		"- The buttons beside the picture are the arrows, OK (Enter), Back (Esc) and Tab. Hold Tab to lock.",
		"- Tap Duo's key bar in the picture for keys 4 to 8; swipe on the picture for the arrows.",
		"- Keys opens the stereo's keyboard for typing.",
		"",
		"While a stereo is connected this screen doesn't dim or turn off by itself. While the Cardputer's own Wi-Fi is on it isn't on any other Wi-Fi.",
	}
}

// linkSummary is the Duo Link row's value in Settings > Launcher.
func linkSummary(a *App) string {
	switch {
	case a.link == nil || !a.link.isRunning():
		return "OFF"
	case a.link.connected() != "":
		return "CONNECTED"
	}
	return "ON"
}

// linkBusy: no dimming while a stereo shows the screen (nobody is looking at the Cardputer).
func (a *App) linkBusy() bool {
	return a.link != nil && a.link.connected() != ""
}
