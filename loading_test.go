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
	// two swells of four ticks (half a square turn) each, then a quarter second of silence
	if d := time.Duration(len(clip)) * time.Second / sampleRate; d < 770*time.Millisecond || d > 800*time.Millisecond {
		t.Fatalf("loop is %v, want 2 x 267 ms + 250 ms", d)
	}
	peak := func(from, to float64) int {
		m := 0
		for _, v := range clip[int(from*sampleRate):int(to*sampleRate)] {
			m = max(m, int(v), -int(v))
		}
		return m
	}
	edge, middle := peak(0, 0.06), peak(0.09, 0.18)
	if 2*middle < 3*edge || middle == 0 {
		t.Errorf("a swell should be loud in the middle (%d) and quiet at its start (%d)", middle, edge)
	}
	if p := peak(0.54, 0.78); p > 50 {
		t.Errorf("the quarter-second pause isn't silent (peak %d)", p)
	}
	// each swell circles the listener: the ticks behind the head (first and last) are muffled
	// next to the same click in front
	bright := func(from, to float64) float64 {
		seg := clip[int(from*sampleRate):int(to*sampleRate)]
		var d, e float64
		for i := 1; i < len(seg); i++ {
			x, p := float64(seg[i]), float64(seg[i-1])
			d += (x - p) * (x - p)
			e += x * x
		}
		return d / max(e, 1)
	}
	step := 0.5333 / 8
	for _, pair := range [][2]int{{0, 2}, {3, 1}} { // behind vs in front, same DSi click
		back := bright(float64(pair[0])*step, float64(pair[0]+1)*step)
		front := bright(float64(pair[1])*step, float64(pair[1]+1)*step)
		if back > 0.8*front {
			t.Errorf("tick %d (behind) should sound duller than tick %d (in front): %.3f vs %.3f", pair[0]+1, pair[1]+1, back, front)
		}
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
