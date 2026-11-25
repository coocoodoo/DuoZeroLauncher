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
