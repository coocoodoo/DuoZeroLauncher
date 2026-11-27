package main

import (
	"encoding/binary"
	"encoding/json"
	"io"
	"os/exec"
	"testing"
	"time"
)

// A stereo that asks for sound gets it, in 20 ms pieces, after the picture starts.
func TestDuoLinkAudio(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("no ffmpeg for a test tone")
	}
	old := linkAudioSource
	linkAudioSource = func() *exec.Cmd {
		return exec.Command("ffmpeg", "-hide_banner", "-loglevel", "error", "-f", "lavfi", "-i", "sine=frequency=440:duration=2",
			"-f", "s16le", "-ac", "2", "-ar", "44100", "-")
	}
	defer func() { linkAudioSource = old }()
	s, f := newTestLink(t)
	s.h.audio = func() bool { return true }
	c := dialLink(t, s, "Radio", "")
	c.c.Close()
	c = dialLinkAudio(t, s)
	f.mu.Lock()
	code := f.code
	f.mu.Unlock()
	c.sendJSON(lkPair, map[string]string{"code": code})
	got := 0
	deadline := time.Now().Add(8 * time.Second)
	for got < 10 && time.Now().Before(deadline) {
		var hdr [5]byte
		if _, err := io.ReadFull(c.r, hdr[:]); err != nil {
			t.Fatal(err)
		}
		p := make([]byte, binary.BigEndian.Uint32(hdr[1:]))
		io.ReadFull(c.r, p)
		if hdr[0] == lkAudio {
			if len(p) != 44100*4/50 {
				t.Fatalf("an audio piece of %d bytes", len(p))
			}
			got++
		}
		if hdr[0] == lkFrame {
			c.send(lkAck, p[:4])
		}
	}
	if got < 10 {
		t.Fatalf("only %d audio pieces", got)
	}
	c.c.Close()
}

func dialLinkAudio(t *testing.T, s *linkServer) *linkTestClient {
	t.Helper()
	lc := dialLinkRaw(t, s)
	b, _ := json.Marshal(map[string]any{"name": "Radio", "token": "", "v": 2, "audio": true})
	lc.send(lkHello, b)
	if typ, _ := lc.next(); typ != lkNeedPair {
		t.Fatalf("want NEEDPAIR, got %#x", typ)
	}
	return lc
}
