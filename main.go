// Duo Launcher: a home screen for the Cardputer Zero in the style of a dual-screen handheld's
// system menu. For now it runs as an app from the system launcher; it lists the same apps,
// launches them, and carries the system settings.
//
// Testing without the hardware: DSL_SIM="RIGHT,RIGHT,ENTER,..." plays keys (ch:x types a
// character, wN waits N tenths of a second) and DSL_SHOTS=dir saves a PNG after each one.
package main

import (
	"dslauncher/cp0"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const version = "0.21.1"

// View is one screen (home, settings, a dialog...).
type View interface {
	Key(a *App, k cp0.Key)
	Draw(a *App)
	Bar(a *App) []BarItem // the 4-8 key bar; nil hides it
}

// Ticker views want frames while something moves.
type Ticker interface{ Animating(a *App) bool }

type BarItem struct {
	Label string
	Art   []string
	Off   bool
}

type App struct {
	scr    *cp0.Screen
	g      *G
	keys   keySource
	cfg    *Config
	snd    *Sound
	status *Status
	all    []*Entry
	apps   []*Entry
	stack  []View
	start  time.Time
	now    time.Time
	t      float64

	toast     string
	toastBad  bool
	toastTill time.Time

	lastKey    time.Time
	dimLevel   int // 0 normal, 1 dimmed, 2 off
	savedLight int

	pressSlot int
	pressTill time.Time

	tabArmed bool // a Tab press is waiting to see whether it is a tap or a hold
	tabHeld  bool // this Tab press already put Duo to sleep

	alarmDay  string
	sudoPW    string
	sudoUntil time.Time

	busyMu      sync.Mutex
	busyText    string
	progress    float64 // 0..1 shows a progress bar under the busy message; < 0 hides it
	jobGen      int     // bumped when Esc cancels a job, so its result is dropped
	work        chan func()
	quit        bool
	leave       bool // chosen "Back to the system launcher": exit with exitLeave
	dirty       bool
	sim         bool
	simShots    string
	shotN       int
	launch      *Entry // set while an app runs
	vol         volState
	bri         briState
	flashUntil  time.Time   // screenshot flash
	batWarned   int         // low-battery warning shown: 0 none, 1 low, 2 almost empty
	doctorAsk   []*docCheck // the start-up check found these; asked about once the home screen shows
	doctorAskFP string
	link        *linkServer  // Duo Link: a car stereo showing this screen
	linkKbd     linkKeyboard // writes the stereo's keys into the keyboard device
	linkWifiUp  bool         // Duo Link's own Wi-Fi (the hotspot) is up
	appRunning  atomic.Bool  // another app has the screen (runApp)
	music       *musicPlayer // Music (built in): plays on when its screen is closed
	appsCheckAt time.Time    // checkApps: when the launcher folders were last looked at
	appsStampV  string       // and what they looked like

	loadingOn bool        // the loading ticks are playing (the busy screen's squares are on screen)
	blanked   bool        // the panel is powered down (screenOff)
	asleep    atomic.Bool // the screen is off: background work pauses to save battery
}

func main() {
	if len(os.Args) == 2 && os.Args[1] == "--version" {
		fmt.Println(version)
		return
	}
	if len(os.Args) == 2 && os.Args[1] == "--update-check" {
		os.Exit(updateCheckCLI())
	}
	if len(os.Args) >= 2 && os.Args[1] == "--doctor" {
		os.Exit(doctorCLI(os.Args[2:]))
	}
	if len(os.Args) == 3 && os.Args[1] == "--sounds" {
		if err := writeSoundPreview(os.Args[2]); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	if os.Getenv("DSL_SIM") == "" {
		reapOrphans() // sound processes left over from before a self-update's restart
	}
	a := &App{cfg: loadConfig(), snd: newSound(), status: &Status{Battery: -1}, start: time.Now(),
		work: make(chan func(), 16), pressSlot: -1, progress: -1}
	applyUITheme(a.cfg.Theme)
	a.scr = cp0.Open()
	a.g = newG(a.scr)
	if os.Getenv("DSL_SIM") == "" {
		holdBacklightLine() // like M5's launcher: then brightness 0 is really dark
	}
	a.guardCorner(true)
	if sim := os.Getenv("DSL_SIM"); sim != "" {
		a.sim = true
		a.simShots = os.Getenv("DSL_SHOTS")
		a.keys = newSimKeys(sim)
		a.snd.On = false // scripted runs stay silent
	} else {
		k := cp0.NewKeys()
		k.Releases = true // Tab tap vs hold needs the release
		a.keys = realKeys{k}
	}
	a.applySound()
	a.rescan()
	a.status.refresh(true)
	go a.statusLoop()
	a.lastKey = time.Now()
	a.stack = []View{newHome(a)}
	if a.cfg.Jingle && !a.sim {
		a.snd.Play(a.startupSound())
	}
	a.link = a.newLink()
	if !a.sim {
		go a.doctorAtStart()
		go a.watchRemotes()
		go cleanupLinkSink() // a Duo that crashed while a stereo played could leave the speaker quiet
		if a.cfg.LinkOn {
			a.startLink()
		}
	}
	a.loop()
	a.shutdown()
	if a.leave {
		os.Exit(exitLeave)
	}
}

// exitLeave tells ~/.local/bin/cardputer-home that the user chose the stock launcher. Any other
// exit (a crash, a signal) makes it start Duo again.
const exitLeave = 10

func (a *App) applySound() {
	a.snd.On = a.cfg.Sounds && !a.sim
	a.snd.SetVolume(float64(a.cfg.SoundVol) / 10)
}

func (a *App) rescan() {
	a.all = append(builtins(), scanApps()...)
	a.apps = orderApps(a.all, a.cfg, false)
}

func (a *App) statusLoop() {
	n := 0
	for !a.quit {
		time.Sleep(5 * time.Second)
		if a.asleep.Load() {
			continue // screen off: nothing to show, no nmcli or bluetoothctl runs
		}
		n++
		a.status.refresh(n%3 == 0)
		a.dirty = true
	}
}

func (a *App) top() View { return a.stack[len(a.stack)-1] }

func (a *App) push(v View) { a.stack = append(a.stack, v); a.dirty = true; a.syncText() }

func (a *App) pop() {
	if len(a.stack) > 1 {
		a.stack = a.stack[:len(a.stack)-1]
	}
	a.dirty = true
	a.syncText()
}

// replace swaps the top view.
func (a *App) replace(v View) { a.stack[len(a.stack)-1] = v; a.dirty = true; a.syncText() }

func (a *App) theme() RGB { return a.cfg.theme() }

func (a *App) notify(msg string, bad bool) {
	if a.appRunning.Load() {
		return // an app has the screen and the speaker: nothing to show it on
	}
	a.toast, a.toastBad, a.toastTill = msg, bad, time.Now().Add(2500*time.Millisecond)
	if bad {
		a.snd.Play("error")
	} else {
		a.snd.Play("notice")
	}
	a.dirty = true
}

func (a *App) save() {
	if err := a.cfg.Save(); err != nil {
		a.notify("Could not save settings", true)
	}
}

// background runs slow work (nmcli, bluetoothctl...) off the UI thread with a busy message;
// done runs back on the UI thread.
func (a *App) background(msg string, fn func() error, done func(err error)) {
	a.busyMu.Lock()
	a.busyText = msg
	a.progress = -1
	a.jobGen++
	gen := a.jobGen
	a.busyMu.Unlock()
	a.dirty = true
	go func() {
		err := fn()
		a.work <- func() {
			a.busyMu.Lock()
			stale := gen != a.jobGen
			if !stale {
				a.busyText = ""
			}
			a.busyMu.Unlock()
			if stale {
				return
			}
			if done != nil {
				done(err)
			}
			a.dirty = true
		}
	}()
}

func (a *App) busy() string {
	a.busyMu.Lock()
	defer a.busyMu.Unlock()
	return a.busyText
}

// setProgress updates the running job's message (when msg isn't empty) and its progress bar
// (0..1). Safe from the job's goroutine; ignored once the job has finished or been cancelled.
func (a *App) setProgress(msg string, p float64) {
	a.busyMu.Lock()
	if a.busyText != "" {
		if msg != "" {
			a.busyText = msg
		}
		a.progress = p
	}
	a.busyMu.Unlock()
	a.dirty = true
}

// withSudo asks for the account password (remembered for ten minutes) and runs fn with it.
func (a *App) withSudo(why string, fn func(pw string)) {
	if a.sudoPW != "" && time.Now().Before(a.sudoUntil) {
		fn(a.sudoPW)
		return
	}
	a.push(newInput("ADMIN PASSWORD", why, "", 64, true, func(pw string) {
		a.background("Checking password...", func() error {
			_, err := runSudo(pw, 10*time.Second, "true")
			return err
		}, func(err error) {
			if err != nil {
				a.notify("Wrong password", true)
				return
			}
			a.sudoPW, a.sudoUntil = pw, time.Now().Add(10*time.Minute)
			fn(pw)
		})
	}))
}

func (a *App) loop() {
	frame := time.Now()
	wasAnim := false
	for !a.quit && !cp0.ShouldQuit() {
		wait := 33
		if !a.animating() {
			wait = 250
		}
		if a.dimLevel == 2 {
			wait = 2000 // asleep: nothing to animate; wake up rarely to save battery
		}
		keys := a.keys.Poll(wait)
		a.now = time.Now()
		a.t = a.now.Sub(a.start).Seconds()
		for {
			select {
			case fn := <-a.work:
				fn()
				continue
			default:
			}
			break
		}
		for _, k := range keys {
			a.handle(k)
			if a.quit {
				return
			}
			if a.sim {
				if a.keys.(*simKeys).film {
					a.film(k)
					continue
				}
				a.settle()
				a.shot(k)
			}
		}
		a.idle()
		a.checkBattery()
		a.checkDoctorAsk()
		a.checkApps()
		a.checkAlarm()
		// one more frame after the last animated one: it is the frame that takes a toast or the
		// volume meter away (they used to stay up to a second longer, until the idle redraw)
		anim := a.animating()
		if a.dimLevel < 2 && (a.dirty || anim || wasAnim || a.now.Sub(frame) >= time.Second) {
			a.draw()
			frame = a.now
		}
		wasAnim = anim
		if a.dimLevel == 2 {
			a.loadingSound(false) // screen off: the squares can't be seen
		}
		if a.sim && a.keys.(*simKeys).done() && a.busy() == "" {
			return // scripted run over, once the last background job has finished
		}
	}
}

func (a *App) animating() bool {
	if t, ok := a.top().(Ticker); ok && t.Animating(a) {
		return true
	}
	now := time.Now()
	return now.Before(a.toastTill) || now.Before(a.pressTill) || a.busy() != "" ||
		now.Before(a.vol.shownTill) || now.Before(a.bri.shownTill) || now.Before(a.flashUntil)
}

func (a *App) handle(k cp0.Key) {
	a.lastKey = time.Now()
	// Tab: a tap does what Tab does on the screen in front (on release); holding it locks and
	// turns the screen off. Only a Tab whose press got here counts, so the press that wakes the
	// screen can't put it straight back to sleep.
	if k.Name == "TAB" && k.Ctrl == 0 {
		switch {
		case k.Released:
			if a.tabArmed && !a.tabHeld && a.dimLevel == 0 {
				a.tabArmed = false
				a.dispatch(cp0.Key{Code: k.Code, Name: "TAB"})
			}
			a.tabArmed = false
			return
		case k.Repeat:
			if a.tabArmed && !a.tabHeld && k.Held >= cp0.TabHold.Seconds()-0.05 {
				a.tabHeld = true
				a.sleepNow()
			}
			return
		case a.dimLevel == 0:
			a.tabArmed, a.tabHeld = true, false
			return
		}
	}
	if k.Released {
		return
	}
	if isBrightnessKey(k) { // Fn+U / Fn+I: on every screen; not while it is off
		if a.dimLevel == 2 {
			return
		}
		if a.dimLevel == 1 {
			a.wake()
		}
		a.brightnessKey(k)
		return
	}
	if isScreenshotKey(k) { // Fn+J: on every screen that is showing
		if a.dimLevel == 0 {
			a.screenshot()
		}
		return
	}
	if isMediaKey(k) { // Fn+Q / Fn+W / Fn+E (and Bluetooth remotes): Music, from anywhere
		if a.dimLevel == 1 {
			a.wake()
		}
		if !k.Repeat {
			a.mediaKey(k.Name)
		}
		return
	}
	if isVolumeKey(k) { // Fn+S / Fn+D / Fn+A: on every screen; a dimmed one wakes to show the level
		if a.dimLevel == 1 {
			a.wake()
		}
		a.volumeKey(k) // with the screen off the volume changes without waking it, like a phone
		return
	}
	if a.dimLevel == 2 && k.Name != "TAB" {
		return // the screen is off: only Tab turns it back on (it is the power button)
	}
	if a.dimLevel > 0 {
		// any key wakes the screen and is used up doing so
		a.wake()
		return
	}
	a.dispatch(k)
}

// sleepNow locks Duo and turns the screen off; any key wakes it to the lock screen.
func (a *App) sleepNow() {
	if !a.lock() {
		a.snd.Play("lock")
	}
	a.draw()
	if a.sim {
		return
	}
	if a.dimLevel == 0 {
		cur, _ := backlight()
		if cur > 0 {
			a.savedLight = cur
		}
	}
	a.screenOff()
}

// lock shows the lock screen and says whether it did. A half-typed box on top (the PIN, a
// password) is closed first. If Duo is already locked somewhere down the stack (the PIN box, a
// low-battery alert or the alarm can sit over the lock screen) it adds nothing: checking only
// the top used to stack a new lock screen on every timeout or Tab hold, and each one asked for
// the PIN again.
func (a *App) lock() bool {
	for len(a.stack) > 1 {
		if _, ok := a.top().(*Input); !ok {
			break
		}
		a.pop()
	}
	if a.lockIndex() >= 0 {
		a.dirty = true
		return false
	}
	a.push(newLock(a))
	return true
}

// lockIndex is where the lock screen is in the stack (-1: not locked).
func (a *App) lockIndex() int {
	for i := len(a.stack) - 1; i >= 0; i-- {
		if _, ok := a.stack[i].(*Lock); ok {
			return i
		}
	}
	return -1
}

// panelPowerOff says whether the panel may be powered down (FBIOBLANK) when the screen goes off.
// Only with the text console in graphics mode (the Doctor's "console" fix, which leaves
// /run/duo-console): otherwise the kernel console counts itself blanked too, and the next key
// press switches the panel back on showing the terminal (0.7.4). DSL_PANEL_OFF=1 forces it.
func panelPowerOff() bool {
	return os.Getenv("DSL_PANEL_OFF") == "1" || fileExists(consoleMarker)
}

// screenOff turns the display off, like a phone's power button. On the Cardputer brightness 0
// still glows (the backlight PWM is inverted: stopping it leaves the LED dimly on), so the screen
// is painted black first, then the backlight goes to 0, then the panel is powered down when that
// is safe (panelPowerOff). The corner repair pauses so nothing wakes every 8 ms. Only Tab wakes.
func (a *App) screenOff() {
	a.g.noClip()
	a.g.rect(0, 0, W, H, cBlack)
	a.scr.Show()
	holdBacklightLine() // (if another program had it at start-up)
	setBacklight(0)
	a.guardCorner(false)
	if panelPowerOff() {
		a.blanked = a.scr.Blank(true)
	}
	a.dimLevel = 2
	a.loadingSound(false)
	// save battery while off: the sound stream closes (the audio device can sleep), the status
	// bar stops polling, the main loop wakes every 2 s instead of every 0.5 s
	a.snd.Close()
	a.asleep.Store(true)
}

// screenOn powers the panel back up (the brightness is restored by wake).
func (a *App) screenOn() {
	if a.blanked {
		a.scr.Blank(false)
		a.blanked = false
	}
	a.guardCorner(true)
	a.asleep.Store(false)
	go a.status.refresh(true) // the status bar was not polled while the screen was off
}

// guardCorner turns the corner repair on or off. It isn't needed at all once the Doctor's
// console fix is in (the console can't draw then), which saves its 125 wake-ups a second.
func (a *App) guardCorner(on bool) {
	a.scr.GuardCorner(on && !fileExists(consoleMarker))
}

func (a *App) dispatch(k cp0.Key) {
	if s := k.Slot(); s >= 0 && !a.keys.textMode() {
		a.pressSlot, a.pressTill = s, time.Now().Add(180*time.Millisecond)
	}
	if p, ok := strings.CutPrefix(k.Name, "BUSY:"); ok && a.sim { // scripted: show the update progress screen
		a.busyMu.Lock()
		a.busyText, a.progress = "", -1
		if v, err := strconv.ParseFloat(p, 64); err == nil {
			a.busyText, a.progress = "Building Duo Launcher 0.6.3... about 0:48 left", v
		}
		a.busyMu.Unlock()
		return
	}
	if a.busy() != "" {
		if k.Back() { // Esc gives up waiting; the job's result is ignored
			a.busyMu.Lock()
			a.busyText = ""
			a.progress = -1
			a.jobGen++
			a.busyMu.Unlock()
			a.snd.Play("back")
			a.notify("Cancelled", false)
		}
		return
	}
	if key, ok := strings.CutPrefix(k.Name, "OPEN:"); ok && a.sim { // scripted shortcut
		for _, e := range a.all {
			if e.Key == key {
				a.open(e)
			}
		}
		return
	}
	if key, ok := strings.CutPrefix(k.Name, "INFO:"); ok && a.sim { // scripted: an app's details
		for _, e := range a.all {
			if e.Key == key {
				a.push(&AppInfo{e: e})
			}
		}
		return
	}
	a.top().Key(a, k)
	a.dirty = true
}

// idle dims and then blanks the screen when nothing is pressed for a while.
func (a *App) idle() {
	if a.sim || a.cfg.DimAfter <= 0 || a.linkBusy() {
		return
	}
	quiet := time.Since(a.lastKey)
	switch {
	case a.dimLevel == 0 && quiet > time.Duration(a.cfg.DimAfter)*time.Second:
		cur, maxv := backlight()
		a.savedLight = cur
		if cur > 0 {
			setBacklight(max(1, min(cur/4, maxv/8)))
		}
		a.dimLevel = 1
	case a.dimLevel == 1 && a.cfg.OffAfter > 0 &&
		quiet > time.Duration(a.cfg.DimAfter+a.cfg.OffAfter)*time.Second:
		a.screenOff()
	}
}

func (a *App) wake() {
	off := a.dimLevel == 2
	if off {
		a.screenOn()
	}
	if a.savedLight > 0 {
		setBacklight(a.savedLight)
	}
	a.dimLevel = 0
	if off && a.cfg.LockOnWake {
		a.lock()
	}
	a.dirty = true
}

func (a *App) checkAlarm() {
	if !a.cfg.AlarmOn {
		return
	}
	now := time.Now()
	day := now.Format("2006-01-02")
	if now.Hour() == a.cfg.AlarmH && now.Minute() == a.cfg.AlarmM && a.alarmDay != day {
		a.alarmDay = day
		if a.dimLevel > 0 {
			a.wake()
		}
		a.push(newAlarm(a))
	}
}

// ---- drawing ----

func (a *App) draw() {
	a.dirty = false
	g := a.g
	g.noClip()
	v := a.top()
	v.Draw(a)
	g.noClip()
	if items := v.Bar(a); items != nil {
		a.drawBar(items)
	}
	msg := a.busy()
	a.loadingSound(msg != "")
	if msg != "" {
		a.drawBusy(msg)
	} else if time.Now().Before(a.toastTill) {
		a.drawToast()
	}
	if time.Now().Before(a.vol.shownTill) {
		a.drawVolume()
	}
	if time.Now().Before(a.bri.shownTill) {
		a.drawBrightness()
	}
	if time.Now().Before(a.flashUntil) { // screenshot taken: a white flash (not in the saved image)
		g.shade(0, 0, W, H, cWhite, 0.7)
	}
	a.scr.Show()
}

// loadingSound runs the DSi loading ticks for exactly as long as the yellow loading squares are
// on screen: the "loading" clip (one tick per square step, swelling in and out like a sine
// twice, each swell circling the listener, then a quarter second of silence) loops from its start when the squares appear, and
// stops when they go. Returns whether it changed.
func (a *App) loadingSound(showing bool) bool {
	switch {
	case showing && !a.loadingOn:
		a.snd.Loop("loading", 0)
	case !showing && a.loadingOn:
		a.snd.Stop("loading")
	default:
		return false
	}
	a.loadingOn = showing
	return true
}

// startupSound is the DS start-up chime, or its birthday version on the user's birthday.
func (a *App) startupSound() string {
	if a.cfg.isBirthday(time.Now()) {
		return "startup-birthday"
	}
	return "startup"
}

// barX are the centres of the key bar slots, over the number keys 4 5 6 7 8.
var barX = []int{49, 105, 161, 217, 273}

// drawBar is the bottom bar: the large DS bar with a square key cap per slot, its name above,
// and a tick down to the key.
func (a *App) drawBar(items []BarItem) {
	g := a.g
	y0 := 132
	g.dsBarLg(0, y0, W, H-y0, cButton, true)
	for i, it := range items {
		if i >= 5 || (it.Label == "" && it.Art == nil) {
			continue
		}
		x := barX[i]
		pressed := i == a.pressSlot && time.Now().Before(a.pressTill)
		ink := cText
		if it.Off {
			ink = cDisabled
		}
		g.textSmallC(x, y0+4, titleCase(it.Label), ink)
		bx, by := x-15, 144
		base := cButton
		if pressed {
			base = a.theme()
		}
		g.dsSquare(bx, by, 30, 22, base, pressed)
		if t := barIcon(it.Art); t != nil {
			ink := cText
			switch {
			case it.Off:
				ink = cDisabled
			case pressed:
				ink = inkOn(base)
			}
			g.texInk(t, x-10, by+1+btoi(pressed), ink, 1)
		} else if it.Art != nil {
			c := cText
			if it.Off {
				c = cDisabled
			}
			if pressed {
				c = inkOn(base)
			}
			g.glyph(x, by+10+btoi(pressed), it.Art, 2, c)
		}
		g.rect(x, 166, 1, 4, ink)
	}
}

func (a *App) drawToast() {
	g := a.g
	w := min(textW(a.toast, 1, false)+20, 300)
	x, y := (W-w)/2, 106
	g.dsStatus(x, y, w, 21)
	c := cWhite
	if a.toastBad {
		c = hex(0xff8080)
	}
	g.textC(W/2, y+6, fit(a.toast, w-12, 1, false), c, 1, false)
}

// drawBusy is the alert box with the loading squares either side, as the DS shows while it
// looks for something.
func (a *App) drawBusy(msg string) {
	g := a.g
	w := min(max(textW(msg, 1, false)+70, 170), 310)
	x, y := (W-w)/2, 44
	g.shade(0, 0, W, H, cBlack, 0.25)
	g.dsAlert(x, y, w, 44)
	g.dsLoading(x+14, y+16, a.t)
	g.dsLoading(x+w-25, y+16, a.t)
	lines := wrap(msg, w-64, false)
	for i, l := range lines[:min(2, len(lines))] {
		g.textC(W/2, y+17-6*(len(lines)-1)+i*12, l, cWhite, 1, false)
	}
	a.busyMu.Lock()
	p := a.progress
	a.busyMu.Unlock()
	if p >= 0 {
		a.drawProgress(x+6, y+51, w-12, 14, p)
	}
}

// drawProgress is the bar under the busy message: dark track, orange outline, a fill in the
// favourite colour (a smooth gradient) and the percentage.
func (a *App) drawProgress(x, y, w, h int, p float64) {
	g := a.g
	p = max(0, min(p, 1))
	g.frame(x-1, y-1, w+2, h+2, cFocus)
	g.rect(x, y, w, h, cBlack)
	g.rect(x+1, y+1, w-2, h-2, cInfoBG)
	if fw := int(float64(w-2)*p + 0.5); fw > 0 {
		g.rrect(x+1, y+1, fw, h-2, 0, a.theme().lighter(0.45), a.theme().darker(0.15))
	}
	label := strconv.Itoa(int(p*100+0.5)) + "%"
	g.textShadow(x+w/2-textW(label, 1, false)/2, y+3, label, cWhite, cBlack, 1, false)
}

// loader draws the DS loading squares centred on (cx, cy).
func loader(g *G, cx, cy int, t float64) { g.dsLoading(cx-5, cy-5, t) }

// ---- launching apps ----

func (a *App) open(e *Entry) {
	if e.Builtin != "" { // built-ins count as recently opened too (apps are marked in runApp)
		a.cfg.markUsed(e.Key)
		a.save()
	}
	switch e.Builtin {
	case "settings":
		if !a.launchBuiltin(e) {
			a.snd.Play("open")
		}
		a.push(newSettings(a))
		return
	case "ip":
		if !a.launchBuiltin(e) {
			a.snd.Play("select")
		}
		a.push(newIPPanel(a))
		return
	case "calc":
		if !a.launchBuiltin(e) {
			a.snd.Play("select")
		}
		a.push(newCalc(a))
		return
	case "dlplay":
		if !a.launchBuiltin(e) {
			a.snd.Play("open")
		}
		a.push(newDownloadPlay(a))
		return
	case "pictochat":
		if !a.launchBuiltin(e) {
			a.snd.Play("open")
		}
		a.push(pictoMenu(a))
		return
	case "music":
		if !a.launchBuiltin(e) {
			a.snd.Play("open")
		}
		a.openMusic()
		return
	case "photos":
		if !a.launchBuiltin(e) {
			a.snd.Play("open")
		}
		a.openPhotos()
		return
	case "files":
		if !a.launchBuiltin(e) {
			a.snd.Play("open")
		}
		a.openFiles()
		return
	case "cli":
		a.runTerminal(e, "")
		return
	case "python":
		a.runTerminal(e, "python", "python3")
		return
	case "ssh":
		a.snd.Play("select")
		a.push(newInput("SSH", "user@host to connect to", a.cfg.LastSSH, 60, false, func(target string) {
			target = strings.TrimSpace(target)
			if target == "" {
				return
			}
			a.cfg.LastSSH = target
			a.save()
			a.runTerminal(e, "ssh", "ssh", target)
		}))
		return
	}
	if e.Terminal {
		a.runTerminal(e, "app", splitExec(e.Exec)...)
		return
	}
	a.runApp(e, splitExec(e.Exec), nil)
}

func (a *App) runTerminal(e *Entry, name string, argv ...string) {
	term := terminalPath()
	if term == "" {
		a.notify("Tiny Terminal is not installed", true)
		return
	}
	var env []string
	if len(argv) > 0 {
		sh, err := shellWrapper(name, argv...)
		if err != nil {
			a.notify("Could not prepare the terminal", true)
			return
		}
		env = []string{"SHELL=" + sh}
	}
	a.runApp(e, []string{term}, env)
}

// runApp hands the screen and keyboard to an app and takes them back when it exits.
func (a *App) runApp(e *Entry, argv []string, env []string) {
	if len(argv) == 0 {
		a.notify("Nothing to run for "+e.Name, true)
		return
	}
	if _, err := exec.LookPath(argv[0]); err != nil {
		a.notify(e.Name+" is not installed here", true)
		return
	}
	a.snd.Play("launch")
	soundAt := time.Now()
	a.cfg.markUsed(e.Key)
	a.save()
	a.drawLaunching(e)
	if a.sim {
		return
	}
	if left := 450*time.Millisecond - time.Since(soundAt); left > 0 {
		time.Sleep(left) // let the launch sound finish
	}
	a.snd.Close()
	home, _ := os.UserHomeDir()
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Dir = home
	cmd.Env = append(os.Environ(), env...)
	logf, _ := os.Create(filepath.Join(cacheDir(), "last-app.log"))
	if logf != nil {
		cmd.Stdout, cmd.Stderr = logf, logf
		defer logf.Close()
	}
	started := time.Now()
	a.launch = e
	a.scr.GuardCorner(false) // the app owns the whole screen now
	err := cmd.Start()
	if err == nil {
		a.keys.pause(true)
		a.appRunning.Store(true)
		stopShots := a.watchScreenshots() // Fn+J still takes screenshots inside the app
		err = a.waitApp(cmd)
		stopShots()
		a.appRunning.Store(false)
		a.keys.pause(false)
	}
	a.guardCorner(true)
	a.launch = nil
	a.tabArmed, a.tabHeld = false, false
	a.lastKey = time.Now()
	a.rescan()
	if h, ok := a.stack[0].(*Home); ok {
		h.keep(a)
	}
	if err != nil && time.Since(started) < 2*time.Second {
		a.notify(e.Name+" could not start", true)
	}
	a.dirty = true
}

// waitApp waits for the app to exit, and meanwhile runs what background work hands to the UI
// thread (a.work). Nobody read that queue while an app ran: a stereo that reconnected a few
// times filled it and then got no picture until the app closed, a Bluetooth remote's buttons all
// arrived at once afterwards, and Music could stall telling about a file it skipped.
func (a *App) waitApp(cmd *exec.Cmd) error {
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()
	for {
		select {
		case err := <-exited:
			return err
		case fn := <-a.work:
			fn()
		}
	}
}

// drawLaunching plays the open animation before an app takes the screen: from the home screen
// the chosen tile rises while everything else fades to white (Home.drawLaunch); anywhere else
// the screen washes white and a "Starting..." box shows.
func (a *App) drawLaunching(e *Entry) {
	if h, ok := a.top().(*Home); ok && h.cur(a) == e {
		a.launchFromHome(h, e)
		return
	}
	g := a.g
	for i := 0; i < 6; i++ {
		g.shade(0, 0, W, H, curTheme.PaperEven, 0.25)
		a.scr.Show()
		if !a.sim {
			time.Sleep(25 * time.Millisecond)
		}
	}
	g.dsGrid(0, 0, W, H)
	x, y, w := 40, 50, 240
	g.dsInfo(x, y, w, 47, cButton)
	g.dsInfoSquare(x+2, y+2, 43, a.theme())
	if ic := e.Icon(38); ic != nil {
		g.icon(x+4, y+4, ic, 1)
	}
	g.text(x+51, y+3, fit(e.Name, w-60, 1, false), cText, 1, false)
	g.dsInfoText(x+48, y+15, w-50, 31)
	g.text(x+54, y+25, "Starting...", cWhite, 1, false)
	g.dsLoading(x+w-20, y+24, a.t)
	a.scr.Show()
}

// launchBuiltin plays the launch animation (tile rises, everything fades to white) for a
// built-in app opened from its tile on the home screen, like an app Duo starts. It reports
// whether it played.
func (a *App) launchBuiltin(e *Entry) bool {
	h, ok := a.top().(*Home)
	if !ok || h.cur(a) != e {
		return false
	}
	a.snd.Play("launch")
	a.cfg.markUsed(e.Key)
	a.save()
	a.launchFromHome(h, e)
	return true
}

func (a *App) launchFromHome(h *Home, e *Entry) {
	from := smoothstep(h.lift[e.Key]) * tileRise // where the tile stands now
	frame := func(p float64) {
		h.launching = true
		a.g.noClip()
		h.Draw(a)
		a.g.noClip()
		a.drawBar(h.Bar(a))
		h.drawLaunch(a, e, from, p)
		h.launching = false
		a.scr.Show()
	}
	if a.sim { // fixed steps, saved as screenshots
		for _, p := range []float64{0.25, 0.5, 0.75, 1} {
			frame(p)
			a.saveShot(fmt.Sprintf("launch-%d", int(p*100)))
		}
		return
	}
	start := time.Now()
	for {
		p := time.Since(start).Seconds() / launchDur
		if p >= 1 {
			frame(1)
			return
		}
		frame(p)
		time.Sleep(10 * time.Millisecond)
	}
}

func (a *App) shutdown() {
	a.music.Stop() // (also its ffmpeg and pacat)
	if a.link != nil {
		a.link.shutdown()
	}
	restoreLinkAudio()           // the speaker plays again for whatever runs next
	if a.leave && a.linkWifiUp { // the stock launcher gets the usual Wi-Fi back
		stopHotspot()
	}
	if a.blanked { // never leave whatever runs next with the panel powered down
		a.scr.Blank(false)
		a.blanked = false
	}
	if a.dimLevel > 0 && a.savedLight > 0 {
		setBacklight(a.savedLight)
	}
	a.quit = true
	a.scr.GuardCorner(false)
	a.snd.Close()
	releaseBacklightLine() // the stock launcher (or the next Duo) takes it from here
}

// ---- keys: the real keyboard or a script ----

type keySource interface {
	Poll(ms int) []cp0.Key
	textMode() bool
	setTextMode(bool)
	pause(bool)
}

type realKeys struct{ k *cp0.Keys }

func (r realKeys) Poll(ms int) []cp0.Key { return r.k.Poll(ms) }
func (r realKeys) textMode() bool        { return r.k.TextMode }
func (r realKeys) setTextMode(b bool)    { r.k.TextMode = b }

// pause: while an app runs the launcher stops reading; keys pressed meanwhile are dropped.
func (r realKeys) pause(on bool) {
	if !on {
		for len(r.k.Poll(0)) > 0 {
		}
		time.Sleep(150 * time.Millisecond)
		for len(r.k.Poll(30)) > 0 {
		}
	}
}

type simKeys struct {
	steps []string
	i     int
	text  bool
	film  bool // the step ended in "~": film its animation instead of waiting for the end
}

func newSimKeys(s string) *simKeys { return &simKeys{steps: strings.Split(s, ",")} }

func (s *simKeys) done() bool         { return s.i >= len(s.steps) }
func (s *simKeys) textMode() bool     { return s.text }
func (s *simKeys) setTextMode(b bool) { s.text = b }
func (s *simKeys) pause(bool)         {}

var slotCode = map[string]int{"4": 5, "5": 6, "6": 7, "7": 8, "8": 9}

func (s *simKeys) Poll(ms int) []cp0.Key {
	if s.done() {
		time.Sleep(time.Duration(ms) * time.Millisecond)
		return nil
	}
	step := strings.TrimSpace(s.steps[s.i])
	s.i++
	step, s.film = strings.CutSuffix(step, "~")
	switch {
	case step == "":
		return nil
	case strings.HasPrefix(step, "w") && len(step) > 1 && step[1] >= '0' && step[1] <= '9':
		var n int
		fmt.Sscanf(step[1:], "%d", &n)
		time.Sleep(time.Duration(n) * 100 * time.Millisecond)
		return nil
	case strings.HasPrefix(step, "ch:"):
		r := []rune(step[3:])
		if len(r) == 0 {
			r = []rune{','}
		}
		return []cp0.Key{{Ch: r[0]}}
	case step == "TAB":
		return []cp0.Key{{Code: 15, Name: "TAB"}, {Code: 15, Name: "TAB", Released: true, Held: 0.1}}
	case step == "TABHOLD":
		return []cp0.Key{{Code: 15, Name: "TAB"}, {Code: 15, Name: "TAB", Repeat: true, Held: 0.85},
			{Code: 15, Name: "TAB", Released: true, Held: 1.2}}
	case step == "ESCHOLD":
		return []cp0.Key{{Name: "ESC", Held: 1.2}}
	}
	if code, ok := slotCode[step]; ok {
		if s.text {
			return []cp0.Key{{Code: code, Ch: rune(step[0])}}
		}
		return []cp0.Key{{Code: code}}
	}
	return []cp0.Key{{Name: step}}
}

// settle lets animations finish before a scripted screenshot.
func (a *App) settle() {
	for i := 0; i < 20 && a.animatingForShot(); i++ {
		time.Sleep(25 * time.Millisecond)
		a.t = time.Since(a.start).Seconds()
		for {
			select {
			case fn := <-a.work:
				fn()
				continue
			default:
			}
			break
		}
	}
}

func (a *App) animatingForShot() bool {
	if t, ok := a.top().(Ticker); ok && t.Animating(a) {
		return true
	}
	return a.busy() != ""
}

func (a *App) shot(k cp0.Key) {
	if a.simShots == "" {
		return
	}
	a.draw()
	name := k.Name
	if name == "" {
		name = fmt.Sprintf("k%d", k.Code)
		if k.Ch != 0 {
			name = "ch"
		}
	}
	a.saveShot(name)
}

// film saves a frame every 40 ms for 400 ms after a scripted key whose step ends in "~", to
// check an animation's motion and not only where it ends.
func (a *App) film(k cp0.Key) {
	name := k.Name
	if name == "" {
		name = fmt.Sprintf("k%d", k.Code)
	}
	for i := 0; i < 10; i++ {
		a.now = time.Now()
		a.t = a.now.Sub(a.start).Seconds()
		a.draw()
		a.saveShot(fmt.Sprintf("%s-f%d", name, i))
		time.Sleep(40 * time.Millisecond)
	}
}

// saveShot saves the screen as it is (scripted runs with DSL_SHOTS).
func (a *App) saveShot(name string) {
	if a.simShots == "" {
		return
	}
	a.shotN++
	os.MkdirAll(a.simShots, 0o755)
	img := image.NewRGBA(image.Rect(0, 0, W*2, H*2))
	for y := 0; y < H; y++ {
		for x := 0; x < W; x++ {
			c := from565(a.scr.Px[y*W+x])
			col := color.RGBA{c.R, c.G, c.B, 255}
			img.SetRGBA(x*2, y*2, col)
			img.SetRGBA(x*2+1, y*2, col)
			img.SetRGBA(x*2, y*2+1, col)
			img.SetRGBA(x*2+1, y*2+1, col)
		}
	}
	f, err := os.Create(filepath.Join(a.simShots, fmt.Sprintf("%03d-%s.png", a.shotN, strings.NewReplacer(":", "-", "/", "-").Replace(strings.ToLower(name)))))
	if err == nil {
		png.Encode(f, img)
		f.Close()
	}
}
