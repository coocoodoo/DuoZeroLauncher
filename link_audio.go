package main

// Duo Link's sound: what the Cardputer plays (everything: Music, videos, Duo's sounds, apps)
// goes to stereos whose app asked for it ("audio": true in HELLO), as lkAudio messages of 20 ms
// (s16le stereo 44.1 kHz). Stretches of silence aren't sent. Settings > Duo Link > Sound on the
// stereo turns it off.
//
// While a stereo takes the sound, the Cardputer's speaker is quiet: a silent sink "duolink"
// (module-null-sink, loaded with pactl) becomes the default output, every playing stream is moved
// onto it (and kept there, for streams the session manager puts back), and parec records its
// monitor. When the stereo goes, the old default comes back and the sink is unloaded, so the
// streams return to the speaker. Without pactl it records the speaker's monitor as before (and
// the speaker plays too).

import (
	"io"
	"os/exec"
	"strings"
	"sync"
	"time"
)

const (
	linkSinkName    = "duolink"
	linkDefaultTap  = "@DEFAULT_MONITOR@"
	linkSinkMonitor = linkSinkName + ".monitor"
)

// linkAudioSource is the capture command for a device (tests put a tone there instead).
var linkAudioSource = func(device string) *exec.Cmd {
	return exec.Command("parec", "--raw", "--format=s16le", "--rate=44100", "--channels=2",
		"--latency-msec=40", "--device="+device, "--client-name=Duo Link")
}

// linkAudioRoute moves the sound off the speaker; undo puts it back (tests replace it).
var linkAudioRoute = routeToLink

func (lc *linkConn) streamAudio() {
	device, undo := linkAudioRoute()
	defer undo()
	cmd := linkAudioSource(device)
	out, err := cmd.StdoutPipe()
	if err != nil || cmd.Start() != nil {
		return // no parec (or no sound server): the picture still works
	}
	go func() { <-lc.done; cmd.Process.Kill() }()
	defer cmd.Wait()
	buf := make([]byte, 44100*4/50) // 20 ms
	for {
		n, err := io.ReadFull(out, buf)
		if n > 0 && !silent(buf[:n]) {
			if lc.send(lkAudio, buf[:n]) != nil {
				return
			}
		}
		if err != nil {
			return
		}
	}
}

// silent: every sample is (nearly) zero.
func silent(b []byte) bool {
	for i := 0; i+1 < len(b); i += 2 {
		if v := int16(uint16(b[i]) | uint16(b[i+1])<<8); v > 8 || v < -8 {
			return false
		}
	}
	return true
}

// ---- the speaker, quiet while a stereo plays ----

var linkRoute struct {
	mu     sync.Mutex
	users  int
	prev   string // the default sink before
	module string // the null sink's module
	stop   chan struct{}
}

// routeToLink makes the silent sink the output and returns the device to record.
func routeToLink() (device string, undo func()) {
	if findTool("pactl") == "" {
		return linkDefaultTap, func() {}
	}
	r := &linkRoute
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.users == 0 {
		cleanupLinkSink() // one left behind by a Duo that didn't get to tidy up
		prev := defaultSink()
		out, err := run(5*time.Second, "pactl", "load-module", "module-null-sink", "sink_name="+linkSinkName,
			"sink_properties=device.description=Duo-Link")
		if err != nil {
			return linkDefaultTap, func() {}
		}
		r.prev, r.module = prev, strings.TrimSpace(out)
		run(3*time.Second, "pactl", "set-default-sink", linkSinkName)
		r.stop = make(chan struct{})
		go keepOnLinkSink(r.stop)
	}
	r.users++
	var once sync.Once
	return linkSinkMonitor, func() { once.Do(releaseLinkRoute) }
}

func releaseLinkRoute() {
	r := &linkRoute
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.users == 0 {
		return
	}
	if r.users--; r.users > 0 {
		return
	}
	restoreLinkRouteLocked()
}

// restoreLinkAudio puts the speaker back now (Duo is quitting).
func restoreLinkAudio() {
	r := &linkRoute
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.users > 0 {
		r.users = 0
		restoreLinkRouteLocked()
	}
}

func restoreLinkRouteLocked() {
	r := &linkRoute
	close(r.stop)
	if r.prev != "" && r.prev != linkSinkName {
		run(3*time.Second, "pactl", "set-default-sink", r.prev)
	}
	// unloading the sink sends its streams to the default (the speaker again)
	if r.module != "" {
		run(5*time.Second, "pactl", "unload-module", r.module)
	}
	cleanupLinkSink()
	r.prev, r.module = "", ""
}

// keepOnLinkSink moves every playing stream onto the silent sink, now and every second (new
// streams follow the default, but the session manager may put a remembered one on the speaker).
func keepOnLinkSink(stop chan struct{}) {
	t := time.NewTicker(time.Second)
	defer t.Stop()
	for {
		moveStreamsTo(linkSinkName)
		select {
		case <-stop:
			return
		case <-t.C:
		}
	}
}

func moveStreamsTo(sink string) {
	idx := ""
	out, _ := run(3*time.Second, "pactl", "list", "short", "sinks")
	for _, l := range strings.Split(out, "\n") {
		if f := strings.Fields(l); len(f) >= 2 && f[1] == sink {
			idx = f[0]
		}
	}
	if idx == "" {
		return
	}
	out, _ = run(3*time.Second, "pactl", "list", "short", "sink-inputs")
	for _, id := range streamsOffSink(out, idx) {
		run(3*time.Second, "pactl", "move-sink-input", id, sink)
	}
}

// streamsOffSink: from `pactl list short sink-inputs` (id, sink, client, ...), the streams not
// on the sink numbered idx.
func streamsOffSink(list, idx string) []string {
	var ids []string
	for _, l := range strings.Split(list, "\n") {
		if f := strings.Fields(l); len(f) >= 2 && f[1] != idx {
			ids = append(ids, f[0])
		}
	}
	return ids
}

func defaultSink() string {
	if out, err := run(3*time.Second, "pactl", "get-default-sink"); err == nil {
		return strings.TrimSpace(out)
	}
	out, _ := run(3*time.Second, "pactl", "info")
	for _, l := range strings.Split(out, "\n") {
		if v, ok := strings.CutPrefix(strings.TrimSpace(l), "Default Sink:"); ok {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

// cleanupLinkSink unloads any silent sink of Duo Link's still loaded.
func cleanupLinkSink() {
	if findTool("pactl") == "" {
		return
	}
	out, _ := run(3*time.Second, "pactl", "list", "short", "modules")
	for _, id := range linkSinkModules(out) {
		run(5*time.Second, "pactl", "unload-module", id)
	}
}

// linkSinkModules: from `pactl list short modules`, the ids of Duo Link's null sinks.
func linkSinkModules(list string) []string {
	var ids []string
	for _, l := range strings.Split(list, "\n") {
		f := strings.Fields(l)
		if len(f) >= 3 && f[1] == "module-null-sink" && strings.Contains(l, "sink_name="+linkSinkName) {
			ids = append(ids, f[0])
		}
	}
	return ids
}
