package main

// Duo Link's sound: what the Cardputer plays (everything: Music, videos, Duo's sounds, apps),
// captured from PipeWire's monitor of the speaker with parec, goes to stereos whose app asked
// for it ("audio": true in HELLO), as lkAudio messages of 20 ms (s16le stereo 44.1 kHz).
// Stretches of silence aren't sent. Settings > Duo Link > Sound on the stereo turns it off.

import (
	"io"
	"os/exec"
)

// linkAudioSource is the capture command (tests put a tone there instead).
var linkAudioSource = func() *exec.Cmd {
	return exec.Command("parec", "--raw", "--format=s16le", "--rate=44100", "--channels=2",
		"--latency-msec=40", "--device=@DEFAULT_MONITOR@", "--client-name=Duo Link")
}

func (lc *linkConn) streamAudio() {
	cmd := linkAudioSource()
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
