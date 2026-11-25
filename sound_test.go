package main

import (
	"sync"
	"testing"
	"time"
)

// recorder stands in for pacat's stdin and counts what the mixer writes.
type recorder struct {
	mu sync.Mutex
	n  int
}

func (r *recorder) Write(b []byte) (int, error) {
	r.mu.Lock()
	r.n += len(b)
	r.mu.Unlock()
	return len(b), nil
}
func (r *recorder) Close() error { return nil }

func (r *recorder) bytes() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.n
}

// Holding a key repeats a sound every 60 ms. The mixer must never get far ahead of real
// time, or the sounds keep playing long after the key is released.
func TestHeldKeySoundsDoNotPileUp(t *testing.T) {
	rec := &recorder{}
	s := newSound()
	s.mu.Lock()
	s.in = rec // skip starting pacat
	s.mu.Unlock()
	start := time.Now()
	for time.Since(start) < 600*time.Millisecond {
		s.Play("launch")
		s.Play("move")
		time.Sleep(60 * time.Millisecond)
	}
	held := time.Since(start)
	written := time.Duration(rec.bytes()/2) * time.Second / sampleRate
	if ahead := written - held; ahead > 120*time.Millisecond {
		t.Fatalf("mixer ran %v ahead of real time after %v of key repeats", ahead, held)
	}
	// after the key is let go, playback must stop soon: no backlog
	time.Sleep(900 * time.Millisecond)
	total := time.Since(start)
	written = time.Duration(rec.bytes()/2) * time.Second / sampleRate
	if written > total+120*time.Millisecond {
		t.Fatalf("wrote %v of audio in %v", written, total)
	}
}

// A new sound must start right away, not after older ones finish.
func TestNewSoundStartsImmediately(t *testing.T) {
	rec := &recorder{}
	s := newSound()
	s.mu.Lock()
	s.in = rec
	s.mu.Unlock()
	s.Play("startup") // a long clip
	time.Sleep(300 * time.Millisecond)
	s.Play("select")
	s.mu.Lock()
	var pos int
	for _, v := range s.voices {
		if v.name == "select" {
			pos = v.pos
		}
	}
	s.mu.Unlock()
	time.Sleep(100 * time.Millisecond)
	s.mu.Lock()
	started := false
	for _, v := range s.voices {
		if v.name == "select" && v.pos > pos {
			started = true
		}
	}
	s.mu.Unlock()
	if !started {
		t.Fatal("new sound did not start within 100 ms")
	}
}
