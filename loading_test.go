package main

import "testing"

// The DS loading blip: once as the busy screen appears, then once per turn of the squares.
func TestLoadingBlip(t *testing.T) {
	a := &App{snd: &Sound{cache: map[string][]int16{}}} // Sound off: Play does nothing
	blips := 0
	for f := 0; f <= 36; f++ { // 1.2 s at 30 frames a second, starting mid-turn
		a.t = 0.3 + float64(f)/30
		if a.loadingBlip() {
			blips++
		}
	}
	// appears at 0.3 s (blip), turns at 0.533 and 1.067 s (blips)
	if blips != 3 {
		t.Errorf("blips = %d, want 3", blips)
	}
	a.loadingOn = false // busy screen gone and back: blips again at once
	if !a.loadingBlip() {
		t.Error("no blip when the busy screen comes back")
	}
	for _, ev := range []string{"loading", "increase", "decrease", "alarm-exit", "startup-birthday", "shutdown"} {
		if _, err := soundFS.ReadFile("assets/sounds/" + ev + ".pcm"); err != nil {
			t.Errorf("sound %s missing: %v", ev, err)
		}
	}
	if l := a.snd.Length("loading"); l <= 0 || l > 200e6 {
		t.Errorf("loading blip length %v", l)
	}
}
