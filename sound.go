package main

// Menu sounds: the Nintendo DS system sounds (assets/sounds/*.pcm, made by tools/mksounds.py;
// see assets/sounds/SOURCE.md) for the events they cover, synthesized tones for anything else. A small mixer plays overlapping sounds
// into one pacat stream named after the launcher (so the sound server keeps a volume of its own),
// writing 20 ms blocks only while something is playing.

import (
	"embed"
	"encoding/binary"
	"io"
	"math"
	"os"
	"os/exec"
	"sync"
	"time"
)

const sampleRate = 32000

//go:embed assets/sounds/*.pcm
var soundFS embed.FS

type voice struct {
	name string
	pcm  []int16
	pos  int
	loop bool // starts again at the end, with no gap, until Stop
}

type Sound struct {
	mu     sync.Mutex
	cmd    *exec.Cmd
	in     io.WriteCloser
	failed bool
	cache  map[string][]int16
	Volume float64 // 0..1
	On     bool
	voices []*voice
	wake   chan struct{}
}

func newSound() *Sound {
	s := &Sound{cache: map[string][]int16{}, Volume: 0.6, On: true, wake: make(chan struct{}, 1)}
	go s.mixer()
	return s
}

// samples returns the clip for an event: the sound pack's if it has one, else a synthesized tone.
func (s *Sound) samples(name string) []int16 {
	if pcm, ok := s.cache[name]; ok {
		return pcm
	}
	var pcm []int16
	if data, err := soundFS.ReadFile("assets/sounds/" + name + ".pcm"); err == nil {
		pcm = make([]int16, len(data)/2)
		for i := range pcm {
			pcm[i] = int16(binary.LittleEndian.Uint16(data[i*2:]))
		}
	} else {
		raw := render(synth(name), 1)
		pcm = make([]int16, len(raw)/2)
		for i := range pcm {
			pcm[i] = int16(binary.LittleEndian.Uint16(raw[i*2:]))
		}
	}
	s.cache[name] = pcm
	return pcm
}

// mixer sums the playing voices into 20 ms blocks and feeds pacat. It paces itself against
// the clock and stays at most `lead` ahead of what is playing: pacat's stdin pipe holds about a
// second of audio, so without pacing a held key filled it and the sounds kept playing long
// after the key was let go. Now a new sound starts within about 60 ms.
func (s *Sound) mixer() {
	const block = sampleRate / 50
	const lead = 60 * time.Millisecond
	buf := make([]byte, block*2)
	var t0 time.Time // when the audio written so far started playing
	sent := 0        // samples written since t0
	for range s.wake {
		for {
			now := time.Now()
			written := time.Duration(sent) * time.Second / sampleRate
			if t0.IsZero() || now.Sub(t0) > written { // first sound, or everything played out
				t0, sent, written = now, 0, 0
			}
			if ahead := written - now.Sub(t0); ahead > lead {
				time.Sleep(ahead - lead)
			}
			s.mu.Lock()
			if len(s.voices) == 0 {
				s.mu.Unlock()
				break
			}
			if s.in == nil && !s.failed {
				s.start()
			}
			in, vol := s.in, s.Volume
			for i := 0; i < block; i++ {
				sum := 0.0
				for _, v := range s.voices {
					if v.loop && v.pos >= len(v.pcm) {
						v.pos = 0
					}
					if v.pos < len(v.pcm) {
						sum += float64(v.pcm[v.pos])
						v.pos++
					}
				}
				out := math.Max(-32768, math.Min(32767, sum*vol))
				binary.LittleEndian.PutUint16(buf[i*2:], uint16(int16(out)))
			}
			live := s.voices[:0]
			for _, v := range s.voices {
				if v.loop || v.pos < len(v.pcm) {
					live = append(live, v)
				}
			}
			s.voices = live
			s.mu.Unlock()
			if in == nil {
				s.mu.Lock()
				s.voices = nil
				s.mu.Unlock()
				break
			}
			if _, err := in.Write(buf); err != nil {
				s.mu.Lock()
				s.stopLocked()
				s.voices = nil
				s.mu.Unlock()
				break
			}
			sent += block
		}
	}
}

func (s *Sound) start() {
	cmd := exec.Command("pacat", "--playback", "--raw", "--format=s16le", "--rate=32000", "--channels=1",
		"--latency-msec=40", "--client-name=Duo Launcher", "--stream-name=Duo Launcher")
	in, err := cmd.StdinPipe()
	if err != nil {
		s.failed = true
		return
	}
	if err := cmd.Start(); err != nil {
		s.failed = true
		return
	}
	s.cmd, s.in = cmd, in
}

func (s *Sound) stopLocked() {
	if s.in != nil {
		s.in.Close()
		s.in = nil
	}
	if s.cmd != nil {
		go s.cmd.Wait()
		s.cmd = nil
	}
}

// Close ends the stream (before an app takes over the device, and at exit).
func (s *Sound) Close() {
	s.mu.Lock()
	s.voices = nil
	s.stopLocked()
	s.failed = false
	s.mu.Unlock()
}

// Play starts a named effect. Repeating a sound restarts it rather than piling copies up.
func (s *Sound) Play(name string) {
	if !s.On || s.Volume <= 0 {
		return
	}
	pcm := s.samples(name)
	s.mu.Lock()
	kept := s.voices[:0]
	for _, v := range s.voices {
		if v.name != name {
			kept = append(kept, v)
		}
	}
	s.voices = append(kept, &voice{name: name, pcm: pcm})
	// at most 4 sounds at once: drop the oldest one-shot sounds (loops stay)
	for n := len(s.voices); n > 4; n-- {
		for i, v := range s.voices {
			if !v.loop {
				s.voices = append(s.voices[:i], s.voices[i+1:]...)
				break
			}
		}
	}
	s.mu.Unlock()
	s.kick()
}

func (s *Sound) kick() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

// Loop plays an effect over and over with no gap until Stop, starting `from` (0..1) of the way
// in (to line it up with an animation). While it plays, calling Loop again changes nothing.
func (s *Sound) Loop(name string, from float64) {
	if !s.On || s.Volume <= 0 {
		return
	}
	pcm := s.samples(name)
	if len(pcm) == 0 {
		return
	}
	s.mu.Lock()
	for _, v := range s.voices {
		if v.name == name && v.loop {
			s.mu.Unlock()
			return
		}
	}
	pos := int(math.Max(0, math.Min(0.999, from)) * float64(len(pcm)))
	s.voices = append(s.voices, &voice{name: name, pcm: pcm, pos: pos, loop: true})
	s.mu.Unlock()
	s.kick()
}

// Stop cuts a playing effect off.
func (s *Sound) Stop(name string) {
	s.mu.Lock()
	kept := s.voices[:0]
	for _, v := range s.voices {
		if v.name != name {
			kept = append(kept, v)
		}
	}
	s.voices = kept
	s.mu.Unlock()
}

// Length is how long an effect plays.
func (s *Sound) Length(name string) time.Duration {
	return time.Duration(len(s.samples(name))) * time.Second / sampleRate // UI thread, like Play
}

func (s *Sound) SetVolume(v float64) { s.Volume = math.Max(0, math.Min(1, v)) }

// note is one voice: start (s), frequency, length (s), loudness, timbre.
type note struct {
	at, f, dur, amp float64
	kind            int // 0 bell, 1 soft sine blip, 2 square buzz, 3 glassy (bell + fifth shimmer)
}

func synth(name string) []note {
	const (
		C5 = 523.25
		D5 = 587.33
		E5 = 659.26
		G5 = 783.99
		A5 = 880.00
		C6 = 1046.50
		D6 = 1174.66
		E6 = 1318.51
		G6 = 1567.98
		A6 = 1760.00
		C7 = 2093.00
	)
	switch name {
	case "move":
		return []note{{0, 1975.5, 0.045, 0.30, 1}}
	case "tick":
		return []note{{0, 2637.0, 0.025, 0.18, 1}}
	case "select":
		return []note{{0, E6, 0.10, 0.45, 0}, {0.055, A6, 0.22, 0.45, 0}}
	case "back":
		return []note{{0, A5, 0.08, 0.40, 0}, {0.05, E5, 0.18, 0.38, 0}}
	case "toggle-on":
		return []note{{0, G5, 0.07, 0.35, 0}, {0.04, D6, 0.14, 0.35, 0}}
	case "toggle-off":
		return []note{{0, D6, 0.07, 0.35, 0}, {0.04, G5, 0.14, 0.35, 0}}
	case "launch":
		return []note{{0, C6, 0.14, 0.40, 3}, {0.05, E6, 0.14, 0.40, 3}, {0.10, G6, 0.16, 0.40, 3},
			{0.15, C7, 0.40, 0.42, 3}}
	case "error":
		return []note{{0, 196, 0.09, 0.30, 2}, {0.11, 165, 0.16, 0.30, 2}}
	case "open":
		return []note{{0, D6, 0.12, 0.40, 0}, {0.06, G6, 0.26, 0.40, 3}}
	case "lock":
		return []note{{0, G5, 0.09, 0.35, 0}, {0.07, D5, 0.09, 0.35, 0}, {0.14, G5 / 2, 0.30, 0.35, 0}}
	case "unlock":
		return []note{{0, G5 / 2, 0.09, 0.35, 0}, {0.07, D5, 0.09, 0.35, 0}, {0.14, G5, 0.30, 0.35, 3}}
	case "alarm":
		var n []note
		for i := 0; i < 4; i++ {
			t := float64(i) * 0.22
			n = append(n, note{t, A6, 0.10, 0.45, 0}, note{t + 0.10, E6, 0.10, 0.45, 0})
		}
		return n
	case "startup":
		// an original rising figure that settles on a bright chord
		return []note{
			{0.00, G5, 0.20, 0.30, 3}, {0.12, C6, 0.20, 0.32, 3}, {0.24, E6, 0.22, 0.34, 3},
			{0.36, D6, 0.18, 0.30, 3}, {0.48, G6, 0.30, 0.34, 3},
			{0.66, C6, 1.10, 0.26, 3}, {0.66, E6, 1.10, 0.24, 3}, {0.66, G6, 1.10, 0.22, 3}, {0.66, C7, 1.20, 0.16, 0},
		}
	}
	return nil
}

// render mixes notes into 16-bit little-endian PCM.
func render(notes []note, vol float64) []byte {
	end := 0.0
	for _, n := range notes {
		end = math.Max(end, n.at+n.dur)
	}
	count := int((end + 0.03) * sampleRate)
	mixbuf := make([]float64, count)
	for _, n := range notes {
		start := int(n.at * sampleRate)
		length := int(n.dur * sampleRate)
		for i := 0; i < length && start+i < count; i++ {
			t := float64(i) / sampleRate
			attack := math.Min(1, t/0.004)
			var v float64
			switch n.kind {
			case 0: // bell: a struck tone with a quieter octave and a fading twelfth
				env := attack * math.Exp(-t*9/n.dur)
				v = env * (math.Sin(2*math.Pi*n.f*t) + 0.35*math.Sin(2*math.Pi*2*n.f*t)*math.Exp(-t*18) +
					0.12*math.Sin(2*math.Pi*3*n.f*t)*math.Exp(-t*30))
			case 1: // blip
				env := attack * math.Exp(-t*7/n.dur)
				v = env * math.Sin(2*math.Pi*n.f*t)
			case 2: // soft square
				env := attack * math.Min(1, (n.dur-t)/0.02)
				s := math.Sin(2 * math.Pi * n.f * t)
				v = env * 0.6 * math.Tanh(s*4)
			case 3: // glassy: bell with a gentle vibrato shimmer
				env := attack * math.Exp(-t*5/n.dur)
				vib := 1 + 0.003*math.Sin(2*math.Pi*6*t)
				v = env * (math.Sin(2*math.Pi*n.f*vib*t) + 0.25*math.Sin(2*math.Pi*1.5*n.f*t)*math.Exp(-t*10) +
					0.18*math.Sin(2*math.Pi*2*n.f*t)*math.Exp(-t*12))
			}
			mixbuf[start+i] += v * n.amp
		}
	}
	out := make([]byte, count*2)
	for i, v := range mixbuf {
		s := math.Tanh(v*vol*1.1) * 30000
		binary.LittleEndian.PutUint16(out[i*2:], uint16(int16(s)))
	}
	return out
}

// writeSoundPreview renders every effect, with a pause between them, to a WAV file.
func writeSoundPreview(path string) error {
	var pcm []byte
	gap := make([]byte, sampleRate/2*2)
	s := &Sound{cache: map[string][]int16{}}
	for _, name := range []string{"startup", "move", "move", "move", "bump", "select", "back", "open", "modal",
		"toggle-on", "toggle-off", "increase", "decrease", "launch", "notice", "tick", "error", "lock", "unlock",
		"loading", "loading", "loading", "shutdown", "alarm-exit", "startup-birthday", "alarm"} {
		for _, v := range s.samples(name) {
			pcm = binary.LittleEndian.AppendUint16(pcm, uint16(v))
		}
		pcm = append(pcm, gap...)
	}
	h := make([]byte, 44)
	copy(h, "RIFF")
	binary.LittleEndian.PutUint32(h[4:], uint32(36+len(pcm)))
	copy(h[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(h[16:], 16)
	binary.LittleEndian.PutUint16(h[20:], 1)
	binary.LittleEndian.PutUint16(h[22:], 1)
	binary.LittleEndian.PutUint32(h[24:], sampleRate)
	binary.LittleEndian.PutUint32(h[28:], sampleRate*2)
	binary.LittleEndian.PutUint16(h[32:], 2)
	binary.LittleEndian.PutUint16(h[34:], 16)
	copy(h[36:], "data")
	binary.LittleEndian.PutUint32(h[40:], uint32(len(pcm)))
	return os.WriteFile(path, append(h, pcm...), 0o644)
}
