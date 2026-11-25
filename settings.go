package main

// Settings: a grid of twelve big tiles, each opening a page of rows.

import (
	"dslauncher/cp0"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

func getenv(k string) string { return os.Getenv(k) }

type tile struct {
	name string
	art  []string
	open func(a *App) View
}

type Settings struct {
	sel   int
	tiles []tile
	t0    time.Time
}

func newSettings(a *App) *Settings {
	return &Settings{t0: time.Now(), tiles: []tile{
		{"Screen", artScreen, screenPage}, {"Speaker", artSpeaker, speakerPage},
		{"Wi-Fi", artWifi, wifiPage}, {"Ethernet", artEthernet, ethernetPage},
		{"Bluetooth", artBluetooth, bluetoothPage}, {"Ports", artPlug, portsPage},
		{"Battery", artBattery, batteryPage}, {"Launcher", artLauncher, launcherPage},
		{"Developer", artDev, developerPage}, {"User", artUser, userPage},
		{"Clock", artClock, clockPage}, {"System", artSystem, systemPage},
	}}
}

func (s *Settings) Animating(a *App) bool { return time.Since(s.t0) < 300*time.Millisecond }

func (s *Settings) Key(a *App, k cp0.Key) {
	n := len(s.tiles)
	move := func(d int) {
		s.sel = (s.sel + d + n) % n
		s.t0 = time.Now()
		a.snd.Play("move")
	}
	switch {
	case k.Back() || k.Slot() == 4:
		a.snd.Play("back")
		a.pop()
	case k.Name == "LEFT":
		move(-1)
	case k.Name == "RIGHT":
		move(1)
	case k.Name == "UP" || k.Name == "DOWN":
		move(6)
	case k.Name == "ENTER" || k.Slot() == 0:
		a.snd.Play("open")
		a.push(s.tiles[s.sel].open(a))
	}
}

func (s *Settings) Bar(a *App) []BarItem {
	return []BarItem{{"OPEN", artOpen, false}, {}, {}, {}, {"BACK", artBack, false}}
}

func (s *Settings) Draw(a *App) {
	g := a.g
	pageTitle(a, "Settings", artGear)
	for i, t := range s.tiles {
		col, row := i%6, i/6
		x, y := 6+col*52, 21+row*55
		focus := i == s.sel
		bounce := 0
		if focus {
			p := time.Since(s.t0).Seconds()
			if p < 0.25 {
				bounce = -int(3 * (1 - p/0.25))
			}
		}
		base := cButton
		if focus {
			base = a.theme()
		}
		g.dsButton(x, y+bounce, 48, 50, base, false)
		ink := inkOn(base)
		if ic := tileIcon(t.art); ic != nil {
			g.texInk(ic, x+12, y+8+bounce, ink, 1)
		} else {
			g.glyph(x+24, y+19+bounce, t.art, 2, ink)
		}
		g.textSmallFit(x+24, y+38+bounce, t.name, ink, 42)
	}
}

// ---- helpers ----

func page(title string, art []string, rows ...*Row) *RowList {
	return &RowList{Title: title, Art: art, Rows: rows}
}

func choiceIndex(vals []int, v int) int {
	for i, x := range vals {
		if x == v {
			return i
		}
	}
	return 0
}

func onOff(b bool) string { return map[bool]string{true: "on", false: "off"}[b] }

// ---- Screen ----

var dimVals = []int{0, 15, 30, 60, 120, 300}
var dimNames = []string{"NEVER", "15 SEC", "30 SEC", "1 MIN", "2 MIN", "5 MIN"}
var offVals = []int{0, 30, 60, 120, 300, 600}
var offNames = []string{"NEVER", "30 SEC", "1 MIN", "2 MIN", "5 MIN", "10 MIN"}

func screenPage(a *App) View {
	cur, maxv := backlight()
	pct := max(5, cur*100/maxv)
	return page("SCREEN", artScreen,
		&Row{Label: "Brightness", Kind: rowSlider, Num: pct, Min: 5, Max: 100, Step: 5, Unit: "%",
			Do: func(a *App, r *Row) {
				if err := setBacklight(r.Num * maxv / 100); err != nil {
					a.notify("Cannot change brightness", true)
				}
				a.savedLight = r.Num * maxv / 100
			}},
		&Row{Label: "Dim after", Kind: rowChoice, Choices: dimNames, Choice: choiceIndex(dimVals, a.cfg.DimAfter),
			Do: func(a *App, r *Row) { a.cfg.DimAfter = dimVals[r.Choice]; a.save() }},
		&Row{Label: "Then turn off after", Kind: rowChoice, Choices: offNames, Choice: choiceIndex(offVals, a.cfg.OffAfter),
			Do: func(a *App, r *Row) { a.cfg.OffAfter = offVals[r.Choice]; a.save() }},
		&Row{Label: "Lock when it wakes", Kind: rowToggle, On: a.cfg.LockOnWake,
			Do: func(a *App, r *Row) { a.cfg.LockOnWake = r.On; a.save() }},
		&Row{Label: "Lock now", Kind: rowAction, Do: func(a *App, r *Row) { a.push(newLock(a)) }},
	)
}

// ---- Speaker ----

func speakerPage(a *App) View {
	vol, muted, ok := volume()
	l := page("SPEAKER", artSpeaker,
		&Row{Label: "Volume", Kind: rowSlider, Num: vol, Min: 0, Max: 100, Step: 5, Unit: "%", Off: !ok,
			Do: func(a *App, r *Row) {
				a.vol.known = false // the volume keys read it again
				if setVolume(r.Num) != nil {
					a.notify("Cannot change the volume", true)
				}
			}},
		&Row{Label: "Mute", Kind: rowToggle, On: muted, Off: !ok,
			Do: func(a *App, r *Row) { a.vol.known = false; setMute(r.On) }},
		&Row{Label: "Menu sounds", Kind: rowToggle, On: a.cfg.Sounds,
			Do: func(a *App, r *Row) { a.cfg.Sounds = r.On; a.applySound(); a.save() }},
		&Row{Label: "Menu sound level", Kind: rowSlider, Num: a.cfg.SoundVol, Min: 0, Max: 10, Step: 1,
			Do: func(a *App, r *Row) { a.cfg.SoundVol = r.Num; a.applySound(); a.save(); a.snd.Play("select") }},
		&Row{Label: "Start-up chime", Kind: rowToggle, On: a.cfg.Jingle,
			Do: func(a *App, r *Row) { a.cfg.Jingle = r.On; a.save() }},
		&Row{Label: "Play the chime", Kind: rowAction, Do: func(a *App, r *Row) { a.snd.Play("startup") }},
	)
	return l
}

// ---- Wi-Fi ----

func wifiPage(a *App) View {
	l := page("WI-FI", artWifi)
	l.Refresh = func(a *App, l *RowList) {
		st := a.status.snapshot()
		on := wifiRadio()
		conn := "not connected"
		if st.SSID != "" {
			conn = st.SSID
		}
		l.Rows = []*Row{
			{Label: "Wi-Fi", Kind: rowToggle, On: on, Do: func(a *App, r *Row) {
				want := r.On
				r.On = !want // until it is done
				apply := func() {
					a.background(map[bool]string{true: "Turning Wi-Fi on...", false: "Turning Wi-Fi off..."}[want],
						func() error { return setWifiRadio(want) },
						func(err error) {
							if err != nil {
								a.notify("Wi-Fi: "+err.Error(), true)
							}
							a.status.refresh(true)
							l.Refresh(a, l)
						})
				}
				if !want {
					confirm(a, "TURN WI-FI OFF?", "Network apps and SSH over Wi-Fi stop until you turn it back on.", apply)
				} else {
					apply()
				}
			}},
			{Label: "Connected to", Value: conn},
			{Label: "Address", Value: orDash(st.IP)},
			{Label: "Networks nearby", Kind: rowLink, Off: !on, Do: func(a *App, r *Row) { openNetworks(a, l) }},
			{Label: "Join a hidden network", Kind: rowLink, Off: !on, Do: func(a *App, r *Row) { joinHidden(a, l) }},
			{Label: "Saved networks", Kind: rowLink, Do: func(a *App, r *Row) { openSaved(a) }},
		}
	}
	l.Refresh(a, l)
	return l
}

func orDash(s string) string {
	if s == "" {
		return "--"
	}
	return s
}

func openNetworks(a *App, parent *RowList) {
	var nets []Network
	a.background("Looking for networks...", func() error {
		var err error
		nets, err = scanWifi()
		return err
	}, func(err error) {
		if err != nil {
			a.notify("Scan failed: "+err.Error(), true)
			return
		}
		l := page("NETWORKS", artWifi)
		for _, n := range nets {
			n := n
			tag := fmt.Sprintf("%d%%", n.Signal)
			if n.Secure {
				tag += " LOCKED"
			}
			if n.Saved {
				tag += " SAVED"
			}
			if n.InUse {
				tag = "CONNECTED"
			}
			l.Rows = append(l.Rows, &Row{Label: n.SSID, Kind: rowAction, Value: tag, Do: func(a *App, r *Row) {
				join := func(pw string) {
					a.background("Connecting to "+n.SSID+"...", func() error {
						if n.Saved && pw == "" {
							return connectSaved(n.SSID)
						}
						return connectWifi(n.SSID, pw, false)
					}, func(err error) {
						if err != nil {
							a.notify(err.Error(), true)
							return
						}
						a.status.refresh(true)
						a.notify("Connected to "+n.SSID, false)
						a.pop()
						parent.Refresh(a, parent)
					})
				}
				switch {
				case n.InUse:
					a.notify("Already connected", false)
				case n.Saved || !n.Secure:
					join("")
				default:
					a.push(newInput("PASSWORD", "Password for "+n.SSID, "", 63, true, join))
				}
			}})
		}
		l.Footer = fmt.Sprintf("%d networks", len(nets))
		a.push(l)
	})
}

func joinHidden(a *App, parent *RowList) {
	a.push(newInput("HIDDEN NETWORK", "Network name (SSID)", "", 32, false, func(ssid string) {
		if strings.TrimSpace(ssid) == "" {
			return
		}
		a.push(newInput("PASSWORD", "Password for "+ssid+" (empty if open)", "", 63, true, func(pw string) {
			a.background("Connecting to "+ssid+"...", func() error { return connectWifi(ssid, pw, true) },
				func(err error) {
					if err != nil {
						a.notify(err.Error(), true)
						return
					}
					a.status.refresh(true)
					a.notify("Connected to "+ssid, false)
					parent.Refresh(a, parent)
				})
		}))
	}))
}

func openSaved(a *App) {
	l := page("SAVED NETWORKS", artWifi)
	l.Refresh = func(a *App, l *RowList) {
		saved := savedConnections()
		var names []string
		for n := range saved {
			names = append(names, n)
		}
		sort.Strings(names)
		l.Rows = nil
		for _, n := range names {
			n := n
			l.Rows = append(l.Rows, &Row{Label: n, Kind: rowAction, Value: "FORGET", Do: func(a *App, r *Row) {
				confirm(a, "FORGET NETWORK?", "Forget "+n+" and its password?", func() {
					if err := forgetWifi(n); err != nil {
						a.notify(err.Error(), true)
						return
					}
					a.notify("Forgot "+n, false)
					l.Refresh(a, l)
				})
			}})
		}
	}
	l.Refresh(a, l)
	a.push(l)
}

// ---- Ethernet ----

func ethernetPage(a *App) View {
	l := page("ETHERNET", artEthernet)
	l.Refresh = func(a *App, l *RowList) {
		l.Rows = nil
		for _, d := range netDevices() {
			if d.Type != "ethernet" {
				continue
			}
			d := d
			on := strings.HasPrefix(d.State, "connected")
			l.Rows = append(l.Rows,
				&Row{Label: d.Name, Kind: rowToggle, On: on, Value: d.State, Do: func(a *App, r *Row) {
					want := r.On
					a.background("Updating "+d.Name+"...", func() error { return setDevice(d.Name, want) },
						func(err error) {
							if err != nil {
								a.notify(err.Error(), true)
							}
							l.Refresh(a, l)
						})
				}})
			for _, i := range interfaces() {
				if i.Name == d.Name {
					l.Rows = append(l.Rows, &Row{Label: "  Address", Value: i.IP}, &Row{Label: "  MAC", Value: i.MAC})
				}
			}
		}
		if len(l.Rows) == 0 {
			l.Rows = []*Row{{Label: "Ethernet", Value: "no adapter plugged in"}}
		}
	}
	l.Refresh(a, l)
	return l
}

// ---- Bluetooth ----

func bluetoothPage(a *App) View {
	l := page("BLUETOOTH", artBluetooth)
	l.Refresh = func(a *App, l *RowList) {
		info := btShow()
		powered := info["Powered"] == "yes"
		l.Rows = []*Row{
			{Label: "Bluetooth", Kind: rowToggle, On: powered, Off: len(info) == 0, Do: func(a *App, r *Row) {
				want := r.On
				a.background("Bluetooth "+onOff(want)+"...", func() error { return bt("power", onOff(want)) },
					func(err error) {
						if err != nil {
							a.notify(err.Error(), true)
						}
						l.Refresh(a, l)
					})
			}},
			{Label: "Visible to others", Kind: rowToggle, On: info["Discoverable"] == "yes", Off: !powered,
				Do: func(a *App, r *Row) {
					if err := bt("discoverable", onOff(r.On)); err != nil {
						a.notify(err.Error(), true)
					}
					l.Refresh(a, l)
				}},
			{Label: "Name", Kind: rowLink, Value: info["Alias"], Off: len(info) == 0, Do: func(a *App, r *Row) {
				a.push(newInput("BLUETOOTH NAME", "How other devices see this one", info["Alias"], 30, false,
					func(name string) {
						if name = strings.TrimSpace(name); name == "" {
							return
						}
						if err := bt("system-alias", name); err != nil {
							a.notify(err.Error(), true)
						}
						l.Refresh(a, l)
					}))
			}},
			{Label: "Paired devices", Kind: rowLink, Off: !powered, Do: func(a *App, r *Row) { openPaired(a) }},
			{Label: "Find devices", Kind: rowLink, Off: !powered, Do: func(a *App, r *Row) { findBT(a) }},
		}
		if len(info) == 0 {
			l.Footer = "No Bluetooth adapter answered"
		} else {
			l.Footer = ""
		}
	}
	l.Refresh(a, l)
	return l
}

func openPaired(a *App) {
	l := page("PAIRED DEVICES", artBluetooth)
	l.Refresh = func(a *App, l *RowList) {
		l.Rows = nil
		for _, d := range btDevices("Paired") {
			d := d
			conn := btConnected(d.MAC)
			l.Rows = append(l.Rows, &Row{Label: d.Name, Kind: rowLink, Value: map[bool]string{true: "CONNECTED", false: ""}[conn],
				Do: func(a *App, r *Row) { a.push(btDevicePage(a, d, l)) }})
		}
	}
	l.Refresh(a, l)
	a.push(l)
}

func btDevicePage(a *App, d BTDevice, parent *RowList) *RowList {
	l := page(d.Name, artBluetooth)
	conn := btConnected(d.MAC)
	l.Rows = []*Row{
		{Label: "Address", Value: d.MAC},
		{Label: "Connected", Kind: rowToggle, On: conn, Do: func(a *App, r *Row) {
			want := r.On
			verb := map[bool]string{true: "connect", false: "disconnect"}[want]
			a.background(map[bool]string{true: "Connecting...", false: "Disconnecting..."}[want], func() error { return bt(verb, d.MAC) }, func(err error) {
				if err != nil {
					a.notify(err.Error(), true)
					r.On = !want
				}
				parent.Refresh(a, parent)
			})
		}},
		{Label: "Forget this device", Kind: rowAction, Do: func(a *App, r *Row) {
			confirm(a, "FORGET DEVICE?", "Unpair "+d.Name+"?", func() {
				if err := bt("remove", d.MAC); err != nil {
					a.notify(err.Error(), true)
					return
				}
				a.pop()
				parent.Refresh(a, parent)
			})
		}},
	}
	return l
}

func findBT(a *App) {
	var found []BTDevice
	a.background("Looking for devices (10 s)...", func() error {
		run(13*time.Second, "bluetoothctl", "--timeout", "10", "scan", "on")
		paired := map[string]bool{}
		for _, d := range btDevices("Paired") {
			paired[d.MAC] = true
		}
		for _, d := range btDevices("") {
			if !paired[d.MAC] {
				found = append(found, d)
			}
		}
		return nil
	}, func(err error) {
		l := page("DEVICES NEARBY", artBluetooth)
		for _, d := range found {
			d := d
			l.Rows = append(l.Rows, &Row{Label: d.Name, Kind: rowAction, Value: "PAIR", Do: func(a *App, r *Row) {
				a.background("Pairing with "+d.Name+"...", func() error {
					if err := bt("pair", d.MAC); err != nil {
						return err
					}
					bt("trust", d.MAC)
					return bt("connect", d.MAC)
				}, func(err error) {
					if err != nil {
						a.notify(err.Error(), true)
						return
					}
					a.notify("Paired with "+d.Name, false)
				})
			}})
		}
		l.Footer = fmt.Sprintf("%d found", len(found))
		a.push(l)
	})
}

// ---- external ports ----

func portsPage(a *App) View {
	row := func(label, led string) *Row {
		on, ok := portOn(led)
		return &Row{Label: label, Kind: rowToggle, On: on, Off: !ok, Do: func(a *App, r *Row) {
			if err := setPort(led, r.On); err != nil {
				a.notify("Cannot switch "+label, true)
				r.On = !r.On
			}
		}}
	}
	l := page("PORTS", artPlug, row("Ext 5V output", "ext_5v_out"), row("Grove 5V output", "grove_5v_out"))
	l.Footer = "Powers accessories plugged into the ports"
	return l
}

// ---- battery ----

func batteryPage(a *App) View {
	l := page("BATTERY", artBattery)
	l.Refresh = func(a *App, l *RowList) {
		l.Rows = nil
		for _, kv := range batteryInfo() {
			l.Rows = append(l.Rows, &Row{Label: kv[0], Value: kv[1]})
		}
	}
	l.Refresh(a, l)
	return l
}

// ---- launcher ----

func launcherPage(a *App) View {
	l := page("LAUNCHER", artLauncher)
	l.Refresh = func(a *App, l *RowList) {
		hidden := 0
		for k, v := range a.cfg.Hidden {
			if v && k != "" {
				hidden++
			}
		}
		l.Rows = []*Row{
			{Label: "Sort apps", Kind: rowChoice, Choices: []string{"BY NAME", "RECENT FIRST"}, Choice: a.cfg.Sort,
				Do: func(a *App, r *Row) {
					a.cfg.Sort = r.Choice
					a.save()
					a.apps = orderApps(a.all, a.cfg, false)
				}},
			{Label: "Show or hide apps", Kind: rowLink, Value: fmt.Sprintf("%d hidden", hidden),
				Do: func(a *App, r *Row) { a.push(appsVisibility(a, l)) }},
			{Label: "Uninstall apps", Kind: rowLink, Do: func(a *App, r *Row) { a.push(uninstallPage(a, l)) }},
			{Label: "Removed apps", Kind: rowLink, Value: removedCount(),
				Do: func(a *App, r *Row) { a.push(removedPage(a, l)) }},
			{Label: "Clear favourites", Kind: rowAction, Value: fmt.Sprintf("%d pinned", len(a.cfg.Pinned)),
				Do: func(a *App, r *Row) {
					a.cfg.Pinned = nil
					a.save()
					a.apps = orderApps(a.all, a.cfg, false)
					l.Refresh(a, l)
				}},
			{Label: "Version", Value: "Duo Launcher " + version},
			{Label: "Check for updates", Kind: rowAction, Value: "GITHUB", Do: func(a *App, r *Row) { checkForUpdates(a) }},
		}
		if fileExists(filepath.Join(appDir, "app.prev")) {
			l.Rows = append(l.Rows, &Row{Label: "Undo last update", Kind: rowAction, Do: func(a *App, r *Row) { undoUpdate(a) }})
		}
		l.Rows = append(l.Rows,
			&Row{Label: "Back to the system launcher", Kind: rowAction, Do: func(a *App, r *Row) {
				confirm(a, "LEAVE?", "Close Duo Launcher and return to the system launcher.", func() {
					a.leave, a.quit = true, true
				})
			}})
	}
	l.Refresh(a, l)
	return l
}

func appsVisibility(a *App, parent *RowList) *RowList {
	l := page("SHOW OR HIDE", artEye)
	for _, e := range orderApps(a.all, a.cfg, true) {
		e := e
		l.Rows = append(l.Rows, &Row{Label: e.Name, Kind: rowToggle, On: !a.cfg.Hidden[e.Key],
			Off: e.Builtin == "settings", Do: func(a *App, r *Row) {
				if r.On {
					delete(a.cfg.Hidden, e.Key)
				} else {
					a.cfg.Hidden[e.Key] = true
				}
				a.save()
				a.apps = orderApps(a.all, a.cfg, false)
			}})
	}
	l.OnBack = func(a *App) {
		if h, ok := a.stack[0].(*Home); ok {
			h.keep(a)
		}
		parent.Refresh(a, parent)
	}
	return l
}

// ---- developer ----

func developerPage(a *App) View {
	l := page("DEVELOPER", artDev)
	l.Refresh = func(a *App, l *RowList) {
		l.Rows = []*Row{
			{Label: "USB debugging (ADB)", Kind: rowToggle, On: adbActive(), Do: func(a *App, r *Row) {
				want := r.On
				r.On = !want
				serviceToggle(a, l, "adbd", want, "USB debugging")
			}},
			{Label: "SSH server", Kind: rowToggle, On: sshActive(), Do: func(a *App, r *Row) {
				want := r.On
				r.On = !want
				if !want {
					confirm(a, "STOP SSH?", "Remote logins stop, including any open now.", func() {
						serviceToggle(a, l, "ssh", false, "SSH")
					})
					return
				}
				serviceToggle(a, l, "ssh", true, "SSH")
			}},
			{Label: "Last app's output", Kind: rowLink, Do: func(a *App, r *Row) { a.push(logPage()) }},
		}
	}
	l.Refresh(a, l)
	return l
}

func serviceToggle(a *App, l *RowList, unit string, on bool, label string) {
	a.withSudo("Needed to switch "+label+" "+onOff(on), func(pw string) {
		verb := map[bool][]string{true: {"enable", "--now"}, false: {"disable", "--now"}}[on]
		a.background(label+" "+onOff(on)+"...", func() error {
			_, err := runSudo(pw, 20*time.Second, append([]string{"systemctl"}, append(verb, unit)...)...)
			return err
		}, func(err error) {
			if err != nil {
				a.notify(label+": "+err.Error(), true)
			} else {
				a.notify(label+" is "+onOff(on), false)
			}
			l.Refresh(a, l)
		})
	})
}

func logPage() *RowList {
	l := page("LAST APP OUTPUT", artDev)
	data := readTrim(cacheDir() + "/last-app.log")
	lines := strings.Split(data, "\n")
	if len(lines) > 60 {
		lines = lines[len(lines)-60:]
	}
	for _, s := range lines {
		if s != "" {
			l.Rows = append(l.Rows, &Row{Label: s})
		}
	}
	return l
}

// ---- user ----

func userPage(a *App) View {
	l := page("USER", artUser)
	l.Refresh = func(a *App, l *RowList) {
		pin := "off"
		if a.cfg.PIN != "" {
			pin = "on"
		}
		days := make([]string, 31)
		for i := range days {
			days[i] = strconv.Itoa(i + 1)
		}
		months := []string{"JAN", "FEB", "MAR", "APR", "MAY", "JUN", "JUL", "AUG", "SEP", "OCT", "NOV", "DEC"}
		l.Rows = []*Row{
			{Label: "Name", Kind: rowLink, Value: a.cfg.Name, Do: func(a *App, r *Row) {
				a.push(newInput("NAME", "Your name, up to 10 letters", a.cfg.Name, 10, false, func(s string) {
					if s = strings.TrimSpace(s); s != "" {
						a.cfg.Name = s
						a.save()
						l.Refresh(a, l)
					}
				}))
			}},
			{Label: "Favourite colour", Kind: rowLink, Value: themes[a.cfg.Color].Name,
				Do: func(a *App, r *Row) { a.push(&ColorPick{sel: a.cfg.Color, back: func() { l.Refresh(a, l) }}) }},
			{Label: "Birthday month", Kind: rowChoice, Choices: months, Choice: max(a.cfg.BirthMonth-1, 0),
				Do: func(a *App, r *Row) { a.cfg.BirthMonth = r.Choice + 1; a.save() }},
			{Label: "Birthday day", Kind: rowChoice, Choices: days, Choice: max(a.cfg.BirthDay-1, 0),
				Do: func(a *App, r *Row) { a.cfg.BirthDay = r.Choice + 1; a.save() }},
			{Label: "Message", Kind: rowLink, Value: a.cfg.Message, Do: func(a *App, r *Row) {
				a.push(newInput("MESSAGE", "Shown on the lock screen", a.cfg.Message, 26, false, func(s string) {
					a.cfg.Message = strings.TrimSpace(s)
					a.save()
					l.Refresh(a, l)
				}))
			}},
			{Label: "Lock screen PIN", Kind: rowLink, Value: pin, Do: func(a *App, r *Row) {
				a.push(newInput("PIN", "New PIN (leave empty to turn the PIN off)", "", 12, true, func(s string) {
					a.cfg.PIN = strings.TrimSpace(s)
					a.save()
					a.notify(map[bool]string{true: "PIN set", false: "PIN off"}[a.cfg.PIN != ""], false)
					l.Refresh(a, l)
				}))
			}},
			{Label: "Account", Value: getenv("USER")},
			{Label: "Device name", Kind: rowLink, Value: hostname(), Do: func(a *App, r *Row) {
				a.push(newInput("DEVICE NAME", "Hostname on the network (letters, digits, -)", hostname(), 30, false,
					func(s string) {
						s = strings.TrimSpace(s)
						if s == "" {
							return
						}
						if !hostRE.MatchString(s) {
							a.notify("Use letters, digits and - only", true)
							return
						}
						a.withSudo("Needed to rename the device", func(pw string) {
							a.background("Renaming...", func() error {
								if out, err := runSudo(pw, 60*time.Second, "hostnamectl", "set-hostname", s); err != nil {
									return errors.New(lastError(out, err))
								}
								// keep /etc/hosts in step, or sudo stalls looking the new name up
								return fixHosts(pw, s)
							}, func(err error) {
								if err != nil {
									a.notify(err.Error(), true)
								}
								l.Refresh(a, l)
							})
						})
					}))
			}},
		}
	}
	l.Refresh(a, l)
	return l
}

// ColorPick is a 4x4 grid of favourite colours with a preview.
type ColorPick struct {
	sel  int
	back func()
}

func (c *ColorPick) Key(a *App, k cp0.Key) {
	switch {
	case k.Back() || k.Slot() == 4:
		a.snd.Play("back")
		a.pop()
		c.back()
	case k.Name == "LEFT":
		c.sel = (c.sel + 15) % 16
		a.snd.Play("move")
	case k.Name == "RIGHT":
		c.sel = (c.sel + 1) % 16
		a.snd.Play("move")
	case k.Name == "UP":
		c.sel = (c.sel + 12) % 16
		a.snd.Play("move")
	case k.Name == "DOWN":
		c.sel = (c.sel + 4) % 16
		a.snd.Play("move")
	case k.Name == "ENTER" || k.Slot() == 0:
		a.cfg.Color = c.sel
		a.save()
		a.snd.Play("select")
		a.pop()
		c.back()
	}
}

func (c *ColorPick) Bar(a *App) []BarItem {
	return []BarItem{{"CHOOSE", artOK, false}, {}, {}, {}, {"BACK", artBack, false}}
}

func (c *ColorPick) Draw(a *App) {
	g := a.g
	pageTitle(a, "Favourite colour", artUser)
	for i, t := range themes {
		x, y := 12+(i%4)*31, 22+(i/4)*27
		g.rect(x, y, 28, 24, cBorder)
		g.rect(x+1, y+1, 26, 22, t.C)
		g.hline(x+1, y+1, 26, t.C.lighter(0.35))
		g.hline(x+1, y+22, 26, t.C.darker(0.25))
		if i == a.cfg.Color {
			g.glyph(x+14, y+12, artOK, 1, inkOn(t.C))
		}
		if i == c.sel {
			g.frame(x-2, y-2, 32, 28, cFocus)
			g.frame(x-1, y-1, 30, 26, cFocus)
		}
	}
	t := themes[c.sel].C
	px, py, pw := 142, 22, 170
	g.dsBar(px, py, pw, barH, t)
	g.text(px+5, py+4, a.cfg.Name, inkOn(t), 1, false)
	g.dsDetails(px, py+barH, pw, 86)
	g.textC(px+pw/2, py+24, themes[c.sel].Name, cBlack, 2, false)
	g.dsButton(px+20, py+50, pw-40, 18, t, false)
	g.textC(px+pw/2, py+54, "Like this", inkOn(t), 1, false)
	drawToggle(g, px+pw/2-13, py+76, true, t, false)
}

// ---- clock ----

func clockPage(a *App) View {
	l := page("CLOCK", artClock)
	l.Refresh = func(a *App, l *RowList) {
		info := timeInfo()
		ntp := info["NTP"] == "yes"
		l.Rows = []*Row{
			{Label: "Now", Value: time.Now().Format("Mon Jan 2 2006, 15:04")},
			{Label: "24-hour clock", Kind: rowToggle, On: a.cfg.Clock24,
				Do: func(a *App, r *Row) { a.cfg.Clock24 = r.On; a.save() }},
			{Label: "Set by the network", Kind: rowToggle, On: ntp, Off: info["CanNTP"] == "no",
				Do: func(a *App, r *Row) {
					want := r.On
					r.On = !want
					sudoRun(a, l, "Needed to change how the clock is set", []string{"timedatectl", "set-ntp", strconv.FormatBool(want)},
						"Network time "+onOff(want))
				}},
			{Label: "Set date and time", Kind: rowLink, Off: ntp, Value: map[bool]string{true: "network sets it", false: ""}[ntp],
				Do: func(a *App, r *Row) {
					a.push(newInput("DATE AND TIME", "YYYY-MM-DD HH:MM", time.Now().Format("2006-01-02 15:04"), 16, false,
						func(s string) {
							if _, err := time.Parse("2006-01-02 15:04", strings.TrimSpace(s)); err != nil {
								a.notify("Use the form 2026-11-25 07:30", true)
								return
							}
							sudoRun(a, l, "Needed to set the clock", []string{"timedatectl", "set-time", strings.TrimSpace(s) + ":00"}, "Clock set")
						}))
				}},
			{Label: "Time zone", Kind: rowLink, Value: info["Timezone"], Do: func(a *App, r *Row) { pickZone(a, l) }},
			{Label: "Alarm", Kind: rowToggle, On: a.cfg.AlarmOn, Value: fmtClock(a.cfg, a.cfg.AlarmH, a.cfg.AlarmM),
				Do: func(a *App, r *Row) { a.cfg.AlarmOn = r.On; a.save() }},
			{Label: "Alarm hour", Kind: rowSlider, Num: a.cfg.AlarmH, Min: 0, Max: 23, Step: 1,
				Do: func(a *App, r *Row) {
					a.cfg.AlarmH = r.Num
					a.alarmDay = ""
					a.save()
					l.Rows[5].Value = fmtClock(a.cfg, a.cfg.AlarmH, a.cfg.AlarmM)
				}},
			{Label: "Alarm minute", Kind: rowSlider, Num: a.cfg.AlarmM, Min: 0, Max: 59, Step: 1,
				Do: func(a *App, r *Row) {
					a.cfg.AlarmM = r.Num
					a.alarmDay = ""
					a.save()
					l.Rows[5].Value = fmtClock(a.cfg, a.cfg.AlarmH, a.cfg.AlarmM)
				}},
		}
		l.Footer = "The alarm rings while Duo Launcher is on screen"
	}
	l.Refresh(a, l)
	return l
}

func sudoRun(a *App, l *RowList, why string, argv []string, okMsg string) {
	a.withSudo(why, func(pw string) {
		a.background("Working...", func() error {
			out, err := runSudo(pw, 20*time.Second, argv...)
			if err != nil && out != "" {
				return fmt.Errorf("%s", firstLine(out, err))
			}
			return err
		}, func(err error) {
			if err != nil {
				a.notify(err.Error(), true)
			} else {
				a.notify(okMsg, false)
			}
			l.Refresh(a, l)
		})
	})
}

func pickZone(a *App, parent *RowList) {
	zones := timezones()
	if len(zones) == 0 {
		a.notify("No time zone list", true)
		return
	}
	regions := map[string][]string{}
	var order []string
	for _, z := range zones {
		reg, _, ok := strings.Cut(z, "/")
		if !ok {
			reg = "Other"
		}
		if _, seen := regions[reg]; !seen {
			order = append(order, reg)
		}
		regions[reg] = append(regions[reg], z)
	}
	l := page("TIME ZONE", artClock)
	for _, reg := range order {
		reg := reg
		l.Rows = append(l.Rows, &Row{Label: reg, Kind: rowLink, Value: fmt.Sprint(len(regions[reg])), Do: func(a *App, r *Row) {
			zl := page(strings.ToUpper(reg), artClock)
			for _, z := range regions[reg] {
				z := z
				zl.Rows = append(zl.Rows, &Row{Label: z, Kind: rowAction, Do: func(a *App, r *Row) {
					a.pop()
					a.pop()
					sudoRun(a, parent, "Needed to change the time zone", []string{"timedatectl", "set-timezone", z}, "Time zone "+z)
				}})
			}
			a.push(zl)
		}})
	}
	a.push(l)
}

// ---- system ----

func systemPage(a *App) View {
	l := page("SYSTEM", artSystem)
	l.Refresh = func(a *App, l *RowList) {
		l.Rows = []*Row{
			{Label: "Device", Value: "Cardputer Zero"},
			{Label: "System", Value: osName()},
			{Label: "Kernel", Value: kernel()},
			{Label: "Launcher", Value: "Duo Launcher " + version},
			{Label: "Running for", Value: uptime()},
			{Label: "Memory", Value: memory()},
			{Label: "Storage", Value: storage()},
			{Label: "Licences", Kind: rowLink, Do: func(a *App, r *Row) { a.push(licencesPage()) }},
			{Label: "Restart the launcher", Kind: rowAction, Do: func(a *App, r *Row) { restartSelf(a) }},
			{Label: "Restart the device", Kind: rowAction, Do: func(a *App, r *Row) {
				confirm(a, "RESTART?", "Restart the Cardputer now?", func() {
					a.snd.Play("lock")
					if err := reboot(); err != nil {
						a.notify("Restart failed: "+err.Error(), true)
					}
				})
			}},
			{Label: "Turn off", Kind: rowAction, Do: func(a *App, r *Row) {
				confirm(a, "TURN OFF?", "Shut the Cardputer down now?", func() {
					a.snd.Play("lock")
					if err := powerOff(); err != nil {
						a.notify("Shutdown failed: "+err.Error(), true)
					}
				})
			}},
		}
		if name := hostname(); !hostnameListed(name) {
			// the device name is missing from /etc/hosts: every admin action waits ~10 s
			fix := &Row{Label: "Fix slow admin actions", Kind: rowAction, Value: "/ETC/HOSTS", Do: func(a *App, r *Row) {
				a.withSudo("Needed to add "+name+" to /etc/hosts", func(pw string) {
					a.background("Fixing /etc/hosts...", func() error { return fixHosts(pw, name) }, func(err error) {
						if err != nil {
							alert(a, "NOT FIXED", err.Error())
						} else {
							a.notify(name+" added to /etc/hosts", false)
						}
						l.Refresh(a, l)
					})
				})
			}}
			l.Rows = append([]*Row{fix}, l.Rows...)
		}
	}
	l.Refresh(a, l)
	return l
}

func licencesPage() *RowList {
	l := page("LICENCES", artInfo)
	for _, s := range []string{
		"Duo Launcher: your project.",
		"Textures, palette and layout: ds.css",
		"  (MIT, (c) 2026 spiritov).",
		"Text font: NitroDS font, from ds.css.",
		"Clock digits: Nintendo DS - Clock Numbers",
		"  Font (c) 2026 zigaudrey, FontStruct",
		"  Non-Commercial License: personal use only.",
		"Sounds: Nintendo DSi UI Sounds pack",
		"  (Helljumper); tones in code for the rest.",
		"cp0 UI kit from ZeroCode (keyboard, screen).",
		"Go standard library (BSD-3-Clause).",
	} {
		l.Rows = append(l.Rows, &Row{Label: s})
	}
	return l
}

// restartSelf replaces this process with a fresh copy (after an update, for example).
func restartSelf(a *App) {
	self, err := os.Executable()
	if err != nil {
		a.notify("Cannot restart", true)
		return
	}
	a.shutdown()
	if _, err := exec.LookPath(self); err == nil {
		execSelf(self)
	}
}
