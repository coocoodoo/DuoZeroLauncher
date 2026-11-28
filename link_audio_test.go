package main

import (
	"encoding/binary"
	"encoding/json"
	"io"
	"os/exec"
	"sync/atomic"
	"testing"
	"time"
)

// A stereo that asks for sound gets it, in 20 ms pieces, after the picture starts.
func TestDuoLinkAudio(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("no ffmpeg for a test tone")
	}
	old, oldRoute := linkAudioSource, linkAudioRoute
	var routed, undone atomic.Int32
	linkAudioRoute = func() (string, func()) { routed.Add(1); return "test.monitor", func() { undone.Add(1) } }
	linkAudioSource = func(device string) *exec.Cmd {
		if device != "test.monitor" {
			t.Errorf("recording %q, not the silent sink's monitor", device)
		}
		return exec.Command("ffmpeg", "-hide_banner", "-loglevel", "error", "-f", "lavfi", "-i", "sine=frequency=440:duration=2",
			"-f", "s16le", "-ac", "2", "-ar", "44100", "-")
	}
	defer func() { linkAudioSource, linkAudioRoute = old, oldRoute }()
	s, f := newTestLink(t)
	s.h.audio = func() bool { return true }
	c := dialLink(t, s, "Radio", "")
	c.next()
	c.c.Close()
	waitNoPairing(s) // (dialling again at once was sometimes told "another stereo is pairing")
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
	for i := 0; i < 50 && undone.Load() == 0; i++ {
		time.Sleep(100 * time.Millisecond)
	}
	if routed.Load() != 1 || undone.Load() != 1 {
		t.Fatalf("the speaker should be quieted once and given back once: %d, %d", routed.Load(), undone.Load())
	}
}

// Reading pactl's lists for the streams to move and the sinks to unload.
func TestLinkSinkLists(t *testing.T) {
	inputs := "12\t3\t40\tprotocol-native.c\ts16le 2ch 44100Hz\n15\t7\t41\tprotocol-native.c\ts16le 1ch 32000Hz\n"
	if got := streamsOffSink(inputs, "7"); len(got) != 1 || got[0] != "12" {
		t.Fatalf("streamsOffSink %v", got)
	}
	mods := "536870913\tmodule-null-sink\tsink_name=duolink sink_properties=device.description=Duo-Link\n" +
		"536870914\tmodule-null-sink\tsink_name=other\n7\tmodule-always-sink\t\n"
	if got := linkSinkModules(mods); len(got) != 1 || got[0] != "536870913" {
		t.Fatalf("linkSinkModules %v", got)
	}
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
