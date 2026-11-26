package main

import (
	"dslauncher/cp0"
	"encoding/binary"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// writeTone writes a WAV file: secs of a sine at hz, 44.1 kHz stereo.
func writeTone(t *testing.T, path string, hz, secs float64) {
	t.Helper()
	n := int(secs * 44100)
	data := make([]byte, 44+n*4)
	copy(data, "RIFF")
	binary.LittleEndian.PutUint32(data[4:], uint32(36+n*4))
	copy(data[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(data[16:], 16)
	binary.LittleEndian.PutUint16(data[20:], 1)
	binary.LittleEndian.PutUint16(data[22:], 2)
	binary.LittleEndian.PutUint32(data[24:], 44100)
	binary.LittleEndian.PutUint32(data[28:], 44100*4)
	binary.LittleEndian.PutUint16(data[32:], 4)
	binary.LittleEndian.PutUint16(data[34:], 16)
	copy(data[36:], "data")
	binary.LittleEndian.PutUint32(data[40:], uint32(n*4))
	for i := 0; i < n; i++ {
		v := int16(12000 * math.Sin(2*math.Pi*hz*float64(i)/44100))
		binary.LittleEndian.PutUint16(data[44+i*4:], uint16(v))
		binary.LittleEndian.PutUint16(data[46+i*4:], uint16(v))
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func waitFor(t *testing.T, what string, d time.Duration, ok func() bool) {
	t.Helper()
	end := time.Now().Add(d)
	for !ok() {
		if time.Now().After(end) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestMusicPlayer(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("no ffmpeg here")
	}
	dir := t.TempDir()
	t.Setenv("DSL_CONFIG", filepath.Join(dir, "config"))
	a1, a2 := filepath.Join(dir, "a.wav"), filepath.Join(dir, "b.wav")
	writeTone(t, a1, 440, 1.2)
	writeTone(t, a2, 2000, 1.2)
	var mu sync.Mutex
	var notes []string
	p := newMusicPlayer(true, func(m string, bad bool) { mu.Lock(); notes = append(notes, m); mu.Unlock() })
	defer p.Stop()

	p.PlayList(audioFiles(dir), 0, dir)
	waitFor(t, "playback", 5*time.Second, func() bool { return p.state().pos > 0.3 })
	waitFor(t, "the spectrum", 5*time.Second, func() bool {
		b, _ := p.vis()
		for _, v := range b {
			if v > 0.2 {
				return true
			}
		}
		return false
	})
	waitFor(t, "the length", 5*time.Second, func() bool { return math.Abs(p.state().meta.dur-1.2) < 0.1 })
	if !p.active() {
		t.Fatal("should be playing")
	}

	p.Toggle() // pause: the position holds
	at := p.state().pos
	time.Sleep(300 * time.Millisecond)
	if st := p.state(); !st.paused || math.Abs(st.pos-at) > 0.06 {
		t.Fatalf("paused at %.2f, now %.2f (paused %v)", at, st.pos, st.paused)
	}
	p.Toggle()

	p.Step(1)
	if st := p.state(); st.cur != 1 || st.titles[1] != "b" {
		t.Fatalf("next: cur %d %v", st.cur, st.titles)
	}
	// the last song ends by itself and the queue wraps to the first
	waitFor(t, "the next song by itself", 6*time.Second, func() bool { st := p.state(); return st.cur == 0 && st.pos > 0.1 })

	p.Seek(0.8)
	if st := p.state(); math.Abs(st.pos-0.8) > 0.4 {
		t.Fatalf("seek shows %.2f", st.pos)
	}
	waitFor(t, "the seek", 3*time.Second, func() bool { st := p.state(); return st.pos >= 0.8 && st.pos < 1.2 })

	p.SetVol(140)
	if p.state().vol != 100 {
		t.Error("the volume should stop at 100")
	}
	b, _ := os.ReadFile(musicConfPath())
	if !strings.Contains(string(b), "vol=100") || !strings.Contains(string(b), "dir="+dir) {
		t.Errorf("saved %q", b)
	}
	p.Stop()
	if p.active() {
		t.Error("Stop should stop")
	}
}

// A folder of files that won't play stops with a message instead of looping through them.
func TestMusicBadFiles(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("no ffmpeg here")
	}
	dir := t.TempDir()
	t.Setenv("DSL_CONFIG", filepath.Join(dir, "config"))
	for _, n := range []string{"x.mp3", "y.mp3"} {
		os.WriteFile(filepath.Join(dir, n), []byte("not music"), 0o644)
	}
	var mu sync.Mutex
	var notes []string
	p := newMusicPlayer(true, func(m string, bad bool) { mu.Lock(); notes = append(notes, m); mu.Unlock() })
	defer p.Stop()
	p.PlayList(audioFiles(dir), 0, dir)
	waitFor(t, "giving up", 8*time.Second, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(notes) > 0 && strings.Contains(notes[len(notes)-1], "can't play")
	})
	if p.active() {
		t.Error("should have stopped")
	}
}

func TestMusicSpectrum(t *testing.T) {
	d := newMusicDSP()
	band := func(hz float64) int {
		var ring [musFFTN]float32
		for i := range ring {
			ring[i] = float32(0.5 * math.Sin(2*math.Pi*hz*float64(i)/(musRate/2)))
		}
		var b [fxNB]float32
		for i := 0; i < 8; i++ {
			b = d.spectrum(&ring, 0)
		}
		best := 0
		for i := range b {
			if b[i] > b[best] {
				best = i
			}
		}
		return best
	}
	low, mid, high := band(100), band(1000), band(6000)
	if !(low < mid && mid < high) {
		t.Errorf("bands for 100 Hz, 1 kHz, 6 kHz: %d %d %d", low, mid, high)
	}
}

func TestMusicASCII(t *testing.T) {
	for in, want := range map[string]string{"Café  del Mar": "Cafe del Mar", "Rock’n’Roll": "Rock'n'Roll",
		"ＡＢＣ": "ABC", "日本 song": "song", "a—b": "a-b"} {
		if got := musicASCII(in); got != want {
			t.Errorf("%q: %q, want %q", in, got, want)
		}
	}
}

// The three screens (DSL_DOC_SHOTS=<dir> saves them).
func TestMusicScreens(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("DSL_CONFIG", filepath.Join(dir, "config"))
	a := testApp(t)
	p := a.musicPlayer()
	p.setTracks([]string{"/m/Daft Punk - Something About Us.mp3", "/m/Lofi rain at the window, a very long title that scrolls.mp3", "/m/Ambient 03.flac"})
	p.cur, p.vol = 1, 70
	p.meta = musicMeta{dur: 214, artist: "Night Café"}
	p.changed = time.Now()
	for i := range p.bands {
		p.bands[i] = float32(0.25 + 0.7*math.Abs(math.Sin(float64(i)*0.35)))
	}
	for i := range p.scope {
		p.scope[i] = float32(0.6 * math.Sin(float64(i)*0.2))
	}
	p.start = 71
	now := &MusicNow{fx: newMusicFx(), last: time.Now()}
	a.push(now)
	for mode := 0; mode < len(fxModes); mode++ {
		p.vmode = mode
		p.stop = make(chan struct{}) // "playing", without a decode
		for i := 0; i < 60; i++ {    // music-like input: a pulse every 12 frames, bands moving
			beat := 0.0
			if i%12 < 2 {
				beat = 0.9
			}
			for j := range p.bands {
				v := 0.2 + 0.5*math.Abs(math.Sin(float64(i)*0.21+float64(j)*0.35))
				if j < 6 {
					v = max(v, beat)
				}
				p.bands[j] = float32(min(v, 1))
			}
			now.last = time.Now().Add(-33 * time.Millisecond)
			a.draw()
		}
		a.saveShot("music-now-" + strings.ToLower(fxModes[mode]))
		if fxModes[mode] == "CUBE" { // and in a quiet bar it comes back together
			p.bands = [fxNB]float32{}
			for i := 0; i < 45; i++ {
				now.last = time.Now().Add(-33 * time.Millisecond)
				a.draw()
			}
			a.saveShot("music-now-cube-calm")
		}
		p.stop = nil
	}
	p.vmode = 0
	a.replace(&MusicQueue{sel: 1})
	a.draw()
	a.saveShot("music-queue")
	os.MkdirAll(filepath.Join(dir, "Albums", "Night Drive"), 0o755)
	os.WriteFile(filepath.Join(dir, "Albums", "01 Intro.mp3"), nil, 0o644)
	os.WriteFile(filepath.Join(dir, "Albums", "02 City Lights.mp3"), nil, 0o644)
	os.WriteFile(filepath.Join(dir, "Albums", "notes.txt"), nil, 0o644)
	f := newMusicFiles(a, filepath.Join(dir, "Albums"))
	if got := strings.Join(f.names, ","); got != "..,Night Drive,01 Intro.mp3,02 City Lights.mp3" {
		t.Errorf("folder shows %s", got)
	}
	a.replace(f)
	a.draw()
	a.saveShot("music-folders")

	// the shared keys: 7 goes back to Now playing, Tab to the queue, Esc closes Music
	a.top().Key(a, cp0.Key{Code: 8})
	if _, ok := a.top().(*MusicNow); !ok {
		t.Fatalf("7 on the folders: %T", a.top())
	}
	a.top().Key(a, cp0.Key{Code: 15, Name: "TAB"})
	if _, ok := a.top().(*MusicQueue); !ok {
		t.Fatalf("Tab on Now playing: %T", a.top())
	}
	a.top().Key(a, cp0.Key{Code: 1, Name: "ESC"})
	if len(a.stack) != 1 {
		t.Fatalf("Esc should close Music: %d views", len(a.stack))
	}
}
