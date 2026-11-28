package main

import (
	"os"
	"sync"
	"testing"
	"time"
)

// fakeRadio stands in for nmcli and the fuel gauge.
type fakeRadio struct {
	mu          sync.Mutex
	wifi        bool
	pct         int
	discharging bool
	log         []bool
}

func (f *fakeRadio) install(t *testing.T) {
	old := saverRadio
	saverRadio.wifiOn = func() bool { f.mu.Lock(); defer f.mu.Unlock(); return f.wifi }
	saverRadio.setWifi = func(on bool) error {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.wifi = on
		f.log = append(f.log, on)
		return nil
	}
	saverRadio.onBattery = func() (int, bool) { f.mu.Lock(); defer f.mu.Unlock(); return f.pct, f.discharging }
	t.Cleanup(func() { saverRadio = old })
}

func (f *fakeRadio) on() bool { f.mu.Lock(); defer f.mu.Unlock(); return f.wifi }

// settleSaver runs what the saver's goroutines hand back, until Wi-Fi is as wanted.
func settleSaver(t *testing.T, a *App, f *fakeRadio, want bool) {
	t.Helper()
	for i := 0; i < 200; i++ {
		select {
		case fn := <-a.work:
			fn()
		default:
		}
		if f.on() == want && !a.saver.working {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("Wi-Fi on = %v, want %v", f.on(), want)
}

func saverApp(t *testing.T) (*App, *fakeRadio) {
	setHome(t, t.TempDir())
	f := &fakeRadio{wifi: true, pct: 80, discharging: true}
	f.install(t)
	a := testApp(t)
	a.cfg.Saver = true
	return a, f
}

// With the screen off on battery, Wi-Fi goes off after a moment and comes back with the screen.
func TestBatterySaver(t *testing.T) {
	a, f := saverApp(t)
	a.dimLevel = 2
	a.saverAsleep()
	a.checkSaver()
	if a.saver.working || !f.on() {
		t.Fatal("Wi-Fi went off at once: it should wait, for a quick look at the screen")
	}
	a.saver.offAt = time.Now().Add(-saverDelay - time.Second)
	a.checkSaver()
	settleSaver(t, a, f, false)
	if !a.saver.wifiOff || !fileExists(saverNote()) {
		t.Fatal("Wi-Fi is off: the saver should know, and leave its note")
	}
	a.checkSaver() // nothing more to do
	a.dimLevel = 0
	a.saverAwake()
	settleSaver(t, a, f, true)
	for i := 0; i < 100 && fileExists(saverNote()); i++ {
		time.Sleep(5 * time.Millisecond)
	}
	if fileExists(saverNote()) || len(f.log) != 2 {
		t.Fatalf("after waking: note still there or Wi-Fi switched %v", f.log)
	}
}

// Wi-Fi stays on when it's needed or the saver is off, and a Wi-Fi the user had off stays off.
func TestBatterySaverLeavesWifiAlone(t *testing.T) {
	asleepFor := func(a *App) {
		a.dimLevel = 2
		a.saverAsleep()
		a.saver.offAt = time.Now().Add(-time.Hour)
		a.checkSaver()
	}
	cases := map[string]func(a *App, f *fakeRadio){
		"on the charger":     func(a *App, f *fakeRadio) { f.discharging = false },
		"saver switched off": func(a *App, f *fakeRadio) { a.cfg.Saver = false },
		"Duo Link on":        func(a *App, f *fakeRadio) { a.cfg.LinkOn = true },
		"Duo Link's Wi-Fi":   func(a *App, f *fakeRadio) { a.linkWifiUp = true },
		"sending files":      func(a *App, f *fakeRadio) { a.stack = append(a.stack, &DLSend{}, &Lock{}) },
		"a job running":      func(a *App, f *fakeRadio) { a.busyText = "Updating..." },
	}
	for name, setup := range cases {
		a, f := saverApp(t)
		setup(a, f)
		asleepFor(a)
		if a.saver.working || !f.on() || len(f.log) != 0 {
			t.Errorf("%s: Wi-Fi should stay on", name)
		}
	}
	a, f := saverApp(t)
	f.wifi = false // the user turned it off
	asleepFor(a)
	settleSaver(t, a, f, false)
	a.dimLevel = 0
	a.saverAwake()
	time.Sleep(30 * time.Millisecond)
	if f.on() || len(f.log) != 0 {
		t.Error("Wi-Fi the user had off was switched on by waking")
	}
}

// Waking while Wi-Fi is being switched off still ends with it on; a Duo that stopped while
// asleep turns it back on at start-up, and so does leaving Duo.
func TestBatterySaverNeverStrandsWifi(t *testing.T) {
	a, f := saverApp(t)
	a.dimLevel = 2
	a.saverAsleep()
	a.saver.offAt = time.Now().Add(-time.Minute)
	a.checkSaver()
	a.dimLevel = 0 // woke before the switch-off reported back
	a.saverAwake()
	settleSaver(t, a, f, true)

	f.wifi = false
	os.MkdirAll(cacheDir(), 0o755)
	os.WriteFile(saverNote(), nil, 0o644)
	saverRecover()
	if !f.on() || fileExists(saverNote()) {
		t.Error("start-up after a crash should turn Wi-Fi back on")
	}

	a.dimLevel = 2
	a.saverAsleep()
	a.saver.offAt = time.Now().Add(-time.Minute)
	a.checkSaver()
	settleSaver(t, a, f, false)
	a.saverQuit()
	if !f.on() || fileExists(saverNote()) {
		t.Error("leaving Duo should turn Wi-Fi back on")
	}
}

// The last sleep is summed up when it was long enough to mean something.
func TestBatterySaverReport(t *testing.T) {
	a, f := saverApp(t)
	a.dimLevel = 2
	a.saverAsleep()
	a.saver.offAt = time.Now().Add(-(3*time.Hour + 10*time.Minute))
	f.pct = 74
	a.saverAwake()
	if a.saver.last != "3 h 10 min, 6% used" {
		t.Errorf("report %q", a.saver.last)
	}
	if roughTime(45*time.Minute) != "45 min" {
		t.Error("roughTime")
	}
}
