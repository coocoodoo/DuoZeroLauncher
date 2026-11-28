package main

// Battery saver: while the screen is off and the Cardputer runs on its battery, Wi-Fi is
// switched off, and comes back when the screen does.
//
// Why only that: measured on the Cardputer (fuel gauge, screen off), the system is already
// 99.7% idle at its lowest clock speed, the kernel offers no sleep states (/sys/power/state is
// empty, CPUs can't be taken offline), and switching the video output off changes nothing.
// Wi-Fi off is worth about 25 mA; Bluetooth off about 3 mA, not worth dropping headphones for.
//
// Wi-Fi stays on when it is in use: on the charger, with a stereo connected or Duo Link on
// (stereos find the Cardputer over Wi-Fi), in a chat, or while sending files or apps.
//
// A note in the cache folder says the saver has Wi-Fi off, so a Duo that crashed or was
// restarted while asleep turns it back on at start-up.

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// saverDelay is how long the screen has to be off first: a quick look at the time shouldn't
// drop the Wi-Fi.
const saverDelay = 30 * time.Second

// saverRadio switches Wi-Fi (tests put fakes here).
var saverRadio = struct {
	wifiOn    func() bool
	setWifi   func(on bool) error
	onBattery func() (pct int, discharging bool)
}{wifiRadio, setWifiRadio, batteryNow}

// batteryNow reads the fuel gauge directly (the status bar isn't polled while the screen is off).
func batteryNow() (pct int, discharging bool) {
	d := batteryDir()
	if d == "" {
		return -1, false
	}
	pct = -1
	if v, ok := readInt(filepath.Join(d, "capacity")); ok {
		pct = v
	}
	return pct, readTrim(filepath.Join(d, "status")) == "Discharging"
}

type saverState struct {
	mu      sync.Mutex // held while the radio is being switched: off and on never overlap
	offAt   time.Time  // when the screen went off (zero: it's on)
	wifiOff bool       // the saver switched Wi-Fi off
	working bool       // a switch-off is under way

	bat0 int // charge when the screen went off (-1: unknown, or it was charging)
	last string
}

func saverNote() string { return filepath.Join(cacheDir(), "saver-wifi-off") }

// saverAsleep notes that the screen went off (screenOff).
func (a *App) saverAsleep() {
	s := &a.saver
	s.offAt = time.Now()
	s.bat0 = -1
	if pct, dis := saverRadio.onBattery(); dis {
		s.bat0 = pct
	}
}

// saverWanted says whether Wi-Fi may go now.
func (a *App) saverWanted() bool {
	if !a.cfg.Saver || a.sim || a.cfg.LinkOn || a.linkBusy() || a.linkWifiUp || a.busy() != "" {
		return false
	}
	for _, v := range a.stack { // a chat, a transfer or a stereo being set up needs the network
		switch v.(type) {
		case *PictoView, *DLSend, *LinkSetup, *LinkPair:
			return false
		}
	}
	_, discharging := saverRadio.onBattery()
	return discharging
}

// checkSaver runs from the main loop while the screen is off.
func (a *App) checkSaver() {
	s := &a.saver
	if a.dimLevel != 2 || s.offAt.IsZero() || s.wifiOff || s.working || time.Since(s.offAt) < saverDelay || !a.saverWanted() {
		return
	}
	s.working = true
	offAt := s.offAt
	go func() {
		s.mu.Lock()
		off := false
		if saverRadio.wifiOn() {
			os.MkdirAll(cacheDir(), 0o755)
			os.WriteFile(saverNote(), []byte(time.Now().Format(time.RFC3339)+"\n"), 0o644) // before, in case Duo dies in between
			if saverRadio.setWifi(false) == nil {
				off = true
			} else {
				os.Remove(saverNote())
			}
		}
		s.mu.Unlock()
		a.work <- func() {
			s.working = false
			if !off {
				return
			}
			if a.dimLevel == 2 && s.offAt.Equal(offAt) {
				s.wifiOff = true
				return
			}
			a.saverRestore() // the screen came back on meanwhile
		}
	}()
}

// saverAwake runs when the screen comes back on: Wi-Fi returns, and the sleep is summed up.
func (a *App) saverAwake() {
	s := &a.saver
	if !s.offAt.IsZero() {
		slept := time.Since(s.offAt)
		if pct, dis := saverRadio.onBattery(); dis && s.bat0 >= 0 && pct >= 0 && slept >= 10*time.Minute {
			s.last = fmt.Sprintf("%s, %d%% used", roughTime(slept), max(0, s.bat0-pct))
		}
	}
	s.offAt = time.Time{}
	if s.wifiOff {
		s.wifiOff = false
		a.saverRestore()
	}
}

// saverRestore switches Wi-Fi back on, off the UI thread, and refreshes the status bar when
// the network has had time to come back.
func (a *App) saverRestore() {
	s := &a.saver
	go func() {
		s.mu.Lock()
		saverRadio.setWifi(true)
		os.Remove(saverNote())
		s.mu.Unlock()
		if a.sim {
			return
		}
		for _, wait := range []time.Duration{3 * time.Second, 6 * time.Second} {
			time.Sleep(wait)
			a.status.refresh(true)
			a.poke.Store(true)
		}
	}()
}

// saverRecover runs at start-up: a Duo that stopped while the saver had Wi-Fi off left a note.
func saverRecover() {
	if fileExists(saverNote()) {
		saverRadio.setWifi(true)
		os.Remove(saverNote())
	}
}

// saverQuit runs when Duo leaves: whatever runs next gets its Wi-Fi back.
func (a *App) saverQuit() {
	s := &a.saver
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.wifiOff || fileExists(saverNote()) {
		saverRadio.setWifi(true)
		os.Remove(saverNote())
		s.wifiOff = false
	}
}

// roughTime: "45 min", "3 h 10 min".
func roughTime(d time.Duration) string {
	m := int(d.Minutes() + 0.5)
	if m < 60 {
		return fmt.Sprintf("%d min", m)
	}
	return fmt.Sprintf("%d h %d min", m/60, m%60)
}

// saverLast is Settings > Screen's "Last sleep" value.
func saverLast(a *App) string {
	if a.saver.last == "" {
		return "not measured yet"
	}
	return a.saver.last
}
