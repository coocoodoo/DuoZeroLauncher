package main

import (
	"dslauncher/cp0"
	"testing"
	"time"
)

func TestVolumeKeys(t *testing.T) {
	a := &App{sim: true, work: make(chan func(), 16), stack: []View{&Home{}}}
	a.vol = volState{pct: 63, known: true, readAt: time.Now()} // read just now: no wpctl call
	steps := []struct {
		key   string
		pct   int
		muted bool
	}{
		{"VOLUMEUP", 65, false}, // up to the next step of 5
		{"VOLUMEUP", 70, false},
		{"VOLUMEDOWN", 65, false},
		{"MUTE", 65, true},
		{"VOLUMEDOWN", 60, true}, // down keeps it muted
		{"VOLUMEUP", 65, false},  // up unmutes
	}
	for i, s := range steps {
		a.vol.known = true
		a.volumeKey(cp0.Key{Name: s.key})
		if a.vol.pct != s.pct || a.vol.muted != s.muted {
			t.Fatalf("step %d %s: got %d%% muted=%v, want %d%% muted=%v", i, s.key, a.vol.pct, a.vol.muted, s.pct, s.muted)
		}
	}
	a.vol.pct = 100
	a.volumeKey(cp0.Key{Name: "VOLUMEUP"})
	if a.vol.pct != 100 {
		t.Errorf("above 100: %d", a.vol.pct)
	}
	a.vol.pct = 2
	a.volumeKey(cp0.Key{Name: "VOLUMEDOWN"})
	if a.vol.pct != 0 {
		t.Errorf("below 0: %d", a.vol.pct)
	}
	muted := a.vol.muted
	a.volumeKey(cp0.Key{Name: "MUTE", Repeat: true})
	if a.vol.muted != muted {
		t.Error("a held Mute must not keep toggling")
	}
}
