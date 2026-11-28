package main

import (
	"dslauncher/cp0"
	"testing"
	"time"
)

// Hold Tab: locked, screen off. While it is off only Tab turns it back on (volume keys change
// the volume without waking it); the Tab that wakes it shows the lock screen, it doesn't unlock.
func TestScreenOffLikeAPhone(t *testing.T) {
	scr := &cp0.Screen{Px: make([]uint16, W*H)}
	a := &App{cfg: defaultConfig(), snd: &Sound{cache: map[string][]int16{}}, scr: scr, keys: newSimKeys(""),
		status: &Status{Battery: -1}, work: make(chan func(), 16), start: time.Now(), progress: -1}
	a.g = newG(scr)
	a.stack = []View{&Home{last: time.Now()}}
	a.vol = volState{pct: 50, known: true, readAt: time.Now()}

	tab := cp0.Key{Code: 15, Name: "TAB"}
	a.handle(tab)
	a.handle(cp0.Key{Code: 15, Name: "TAB", Repeat: true, Held: cp0.TabHold.Seconds()})
	if a.dimLevel != 2 {
		t.Fatalf("holding Tab: dimLevel %d, want 2 (screen off)", a.dimLevel)
	}
	if _, ok := a.top().(*Lock); !ok {
		t.Fatal("holding Tab should lock")
	}
	a.handle(cp0.Key{Code: 15, Name: "TAB", Released: true, Held: 1.2})

	for _, k := range []cp0.Key{{Name: "ENTER"}, {Ch: 'a'}, {Name: "ESC"}, {Code: 5}} {
		a.handle(k)
		if a.dimLevel != 2 {
			t.Fatalf("%+v woke the screen; only Tab should", k)
		}
	}
	a.handle(cp0.Key{Name: "VOLUMEUP"})
	if a.dimLevel != 2 || a.vol.pct != 55 {
		t.Fatalf("volume up with the screen off: dimLevel %d, volume %d", a.dimLevel, a.vol.pct)
	}

	a.handle(tab) // the power button
	if a.dimLevel != 0 {
		t.Fatal("Tab should turn the screen back on")
	}
	a.handle(cp0.Key{Code: 15, Name: "TAB", Released: true, Held: 0.1})
	if _, ok := a.top().(*Lock); !ok {
		t.Fatal("the Tab that woke the screen must not also unlock")
	}
	a.handle(tab) // a fresh tap unlocks
	a.handle(cp0.Key{Code: 15, Name: "TAB", Released: true, Held: 0.1})
	if _, ok := a.top().(*Lock); ok {
		t.Fatal("a second Tab should unlock")
	}
}

// The PIN is asked once, however many times the screen timed out or Tab was held while the PIN
// box (or an alert) was over the lock screen: a second lock screen used to stack on each.
func TestLockNeverStacks(t *testing.T) {
	scr := &cp0.Screen{Px: make([]uint16, W*H)}
	a := &App{cfg: defaultConfig(), snd: &Sound{cache: map[string][]int16{}}, scr: scr, keys: newSimKeys(""),
		status: &Status{Battery: -1}, work: make(chan func(), 16), start: time.Now(), progress: -1}
	a.g = newG(scr)
	a.stack = []View{&Home{last: time.Now()}}
	a.cfg.PIN, a.cfg.LockOnWake = "1234", true
	locks := func() int {
		n := 0
		for _, v := range a.stack {
			if _, ok := v.(*Lock); ok {
				n++
			}
		}
		return n
	}
	tap := func() {
		a.handle(cp0.Key{Code: 15, Name: "TAB"})
		a.handle(cp0.Key{Code: 15, Name: "TAB", Released: true, Held: 0.1})
	}
	hold := func() {
		a.handle(cp0.Key{Code: 15, Name: "TAB"})
		a.handle(cp0.Key{Code: 15, Name: "TAB", Repeat: true, Held: cp0.TabHold.Seconds()})
		a.handle(cp0.Key{Code: 15, Name: "TAB", Released: true, Held: 1.2})
	}

	hold() // lock, screen off
	for i := 0; i < 3; i++ {
		tap() // wake to the lock screen
		tap() // the PIN box
		if _, ok := a.top().(*Input); !ok {
			t.Fatalf("round %d: Tab on the lock screen should ask for the PIN, top is %T", i, a.top())
		}
		a.handle(cp0.Key{Ch: '1'})
		if i%2 == 0 {
			a.screenOff() // the timeout, with the PIN half typed
		} else {
			hold()
		}
		if n := locks(); n != 1 {
			t.Fatalf("round %d: %d lock screens", i, n)
		}
	}
	tap() // wake
	a.push(&BatteryAlert{pct: 12, at: time.Now()})
	a.screenOff()
	tap()
	if n := locks(); n != 1 {
		t.Fatalf("an alert over the lock screen: %d lock screens after waking", n)
	}
	a.handle(cp0.Key{Name: "ENTER"}) // dismiss the alert
	tap()                            // the PIN box
	for _, c := range "1234" {
		a.handle(cp0.Key{Ch: c})
	}
	a.handle(cp0.Key{Name: "ENTER"})
	if n := locks(); n != 0 {
		t.Fatalf("one PIN should unlock; %d lock screens left (top %T)", n, a.top())
	}
	if _, ok := a.top().(*Home); !ok {
		t.Fatalf("unlocked to %T, want the home screen", a.top())
	}
}

// Turning the Cardputer on asks for the PIN: Duo starts locked when one is set (whatever "Lock
// when it wakes" says), nothing gets past the lock screen without it, and with no PIN it starts
// on the home screen.
func TestLockedAtStart(t *testing.T) {
	a := testApp(t)
	a.lockAtStart()
	if len(a.stack) != 1 {
		t.Fatal("no PIN: Duo should start on the home screen")
	}
	a.cfg.PIN, a.cfg.LockOnWake = "1234", false
	a.lockAtStart()
	a.lockAtStart()
	if len(a.stack) != 2 || a.lockIndex() != 1 {
		t.Fatalf("with a PIN Duo should start locked, once: %d screens", len(a.stack))
	}
	for _, name := range []string{"ESC", "SPACE", "UP", "DOWN", "LEFT", "RIGHT", "BACKSPACE"} {
		a.handle(cp0.Key{Name: name})
	}
	for code := 5; code <= 9; code++ { // the key bar's 4-8
		a.handle(cp0.Key{Code: code})
	}
	if a.lockIndex() != 1 || len(a.stack) != 2 {
		t.Fatal("a key got past the lock screen")
	}
	a.handle(cp0.Key{Name: "ENTER"})
	in, ok := a.top().(*Input)
	if !ok {
		t.Fatalf("Enter should ask for the PIN, got %T", a.top())
	}
	in.done("0000")
	if a.lockIndex() < 0 {
		t.Fatal("a wrong PIN unlocked it")
	}
}
