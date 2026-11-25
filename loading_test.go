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
	loopLen := len(s.samples("loading"))
	if d := time.Duration(loopLen) * time.Second / sampleRate; d < 130*time.Millisecond || d > 137*time.Millisecond {
		t.Fatalf("loop is %v, want two square steps (133 ms)", d)
	}
	time.Sleep(400 * time.Millisecond) // three times the clip: it must still be playing
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
