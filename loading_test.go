package main

import (
	"testing"
	"time"
)

// The DSi loading ticks loop with no gaps for exactly as long as the yellow squares show.
func TestLoadingLoop(t *testing.T) {
	rec := &recorder{}
	s := newSound()
	s.mu.Lock()
	s.in = rec // skip starting pacat
	s.mu.Unlock()
	a := &App{snd: s}
	a.t = 1.0
	if !a.loadingSound(true) || a.loadingSound(true) {
		t.Fatal("should start once when the squares appear, then keep going")
	}
	clip := s.samples("loading")
	// two swells of one square turn each, then half a second of silence
	if d := time.Duration(len(clip)) * time.Second / sampleRate; d < 1550*time.Millisecond || d > 1580*time.Millisecond {
		t.Fatalf("loop is %v, want 2 x 533 ms + 500 ms", d)
	}
	peak := func(from, to float64) int {
		m := 0
		for _, v := range clip[int(from*sampleRate):int(to*sampleRate)] {
			m = max(m, int(v), -int(v))
		}
		return m
	}
	edge, middle := peak(0, 0.06), peak(0.22, 0.32)
	if middle < 3*edge || middle == 0 {
		t.Errorf("a swell should be loud in the middle (%d) and quiet at its start (%d)", middle, edge)
	}
	if p := peak(1.08, 1.55); p > 50 {
		t.Errorf("the half-second pause isn't silent (peak %d)", p)
	}
	time.Sleep(400 * time.Millisecond) // it must still be playing (the clip loops)
	s.Play("move")                     // other sounds don't cut it
	s.Play("select")
	s.Play("back")
	s.Play("open")
	s.Play("tick")
	s.mu.Lock()
	playing := false
	for _, v := range s.voices {
		if v.name == "loading" && v.loop {
			playing = true
		}
	}
	s.mu.Unlock()
	if !playing {
		t.Fatal("the loop stopped while the squares were still showing")
	}
	if !a.loadingSound(false) {
		t.Fatal("should stop when the squares go")
	}
	s.mu.Lock()
	for _, v := range s.voices {
		if v.name == "loading" {
			t.Error("still playing after the squares went")
		}
	}
	s.mu.Unlock()
	for _, ev := range []string{"loading", "increase", "decrease", "alarm-exit", "startup-birthday", "shutdown"} {
		if _, err := soundFS.ReadFile("assets/sounds/" + ev + ".pcm"); err != nil {
			t.Errorf("sound %s missing: %v", ev, err)
		}
	}
}
