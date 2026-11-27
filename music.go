package main

// Music, built in (ported from the MP3 Player app): ffmpeg decodes to s16le stereo 44.1 kHz,
// Duo applies the volume (0-100) and feeds pacat, whose blocking input is the clock. A
// decimated copy of the samples drives the visualizers. The player lives on the App, so the
// music keeps playing when the Music screen is closed, the screen is off, or another app runs;
// it moves to the next song by itself.

import (
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	musRate   = 44100
	musBPS    = musRate * 4 // bytes a second (s16le stereo)
	musFFTN   = 512
	musHop    = 735 // of the 22 kHz copy: about 30 spectra a second
	musScopeN = 512
)

var musicExts = map[string]bool{".mp3": true, ".wav": true, ".flac": true, ".ogg": true, ".m4a": true, ".opus": true, ".aac": true}

func isAudio(name string) bool { return musicExts[strings.ToLower(filepath.Ext(name))] }

type musicMeta struct {
	dur    float64
	artist string
	cover  []uint16 // 15x15 RGB565, nil if the song has none
}

type musicPlayer struct {
	mu       sync.Mutex
	tracks   []string
	titles   []string
	cur      int
	vol      int
	vmode    int
	dir      string // the folder last played from
	meta     musicMeta
	gen      int // bumps with each new song (its meta)
	stop     chan struct{}
	done     chan struct{}
	start    float64 // seconds the current decode started at
	fails    int
	seekTo   float64
	seekAt   *time.Timer
	pausedAt time.Time
	changed  time.Time // the song changed (title scroll)
	silent   bool
	notify   func(msg string, bad bool)

	paused atomic.Bool
	played atomic.Int64 // bytes played of the current decode
	volAt  atomic.Int32

	visMu    sync.Mutex
	bands    [fxNB]float32
	scope    [musScopeN]float32
	scopePos int
}

func musicConfPath() string { return filepath.Join(filepath.Dir(configPath()), "music") }

func newMusicPlayer(silent bool, notify func(string, bool)) *musicPlayer {
	p := &musicPlayer{vol: 80, silent: silent, notify: notify}
	if b, err := os.ReadFile(musicConfPath()); err == nil {
		for _, l := range strings.Split(string(b), "\n") {
			k, v, _ := strings.Cut(l, "=")
			n, _ := strconv.Atoi(strings.TrimSpace(v))
			switch k {
			case "vol":
				p.vol = max(0, min(n, 100))
			case "vis":
				p.vmode = max(0, n) % len(fxModes)
			case "dir":
				p.dir = strings.TrimSpace(v)
			}
		}
	}
	p.volAt.Store(int32(p.vol))
	return p
}

func (p *musicPlayer) saveLocked() {
	writeFileAtomic(musicConfPath(), fmt.Sprintf("vol=%d\nvis=%d\ndir=%s\n", p.vol, p.vmode, p.dir), 0o644)
}

// musicDir is where Music starts looking: the folder last played, else ~/Music, else home.
func (p *musicPlayer) musicDir() string {
	p.mu.Lock()
	d := p.dir
	p.mu.Unlock()
	if isDir(d) {
		return d
	}
	if m := filepath.Join(homeDir(), "Music"); isDir(m) {
		return m
	}
	return homeDir()
}

// ---- reading state (any goroutine) ----

type musicState struct {
	tracks, titles []string
	cur, vol, mode int
	playing        bool // a song is loaded (playing or paused)
	paused         bool
	pos            float64
	meta           musicMeta
	changed        time.Time
	pausedAt       time.Time
}

func (p *musicPlayer) state() musicState {
	p.mu.Lock()
	defer p.mu.Unlock()
	s := musicState{tracks: p.tracks, titles: p.titles, cur: p.cur, vol: p.vol, mode: p.vmode,
		playing: p.stop != nil, paused: p.paused.Load(), meta: p.meta, changed: p.changed, pausedAt: p.pausedAt}
	s.pos = p.posLocked()
	return s
}

func (p *musicPlayer) posLocked() float64 {
	if p.seekAt != nil {
		return p.seekTo
	}
	return p.start + float64(p.played.Load())/musBPS
}

// active: something is playing right now (not paused), for the status-bar note.
func (p *musicPlayer) active() bool {
	if p == nil {
		return false
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.stop != nil && !p.paused.Load()
}

func (p *musicPlayer) vis() ([fxNB]float32, [fxScopeW]float32) {
	p.visMu.Lock()
	defer p.visMu.Unlock()
	var s [fxScopeW]float32
	start := (p.scopePos + musScopeN - fxScopeW*2) % musScopeN
	for i := range s {
		s[i] = p.scope[(start+i*2)%musScopeN]
	}
	return p.bands, s
}

// ---- control ----

func (p *musicPlayer) setTracks(paths []string) {
	p.tracks = paths
	p.titles = make([]string, len(paths))
	for i, t := range paths {
		p.titles[i] = strings.TrimSuffix(filepath.Base(t), filepath.Ext(t))
	}
}

// killLocked stops the decode; it waits for it to end, so call it without holding up a
// goroutine that the decode itself waits on.
func (p *musicPlayer) killLocked() {
	if p.stop == nil {
		return
	}
	close(p.stop)
	done := p.done
	p.stop, p.done = nil, nil
	p.mu.Unlock()
	<-done
	p.mu.Lock()
}

// playLocked plays song idx from `from` seconds; fresh: a new song (reload its details).
func (p *musicPlayer) playLocked(idx int, from float64, fresh bool) {
	if len(p.tracks) == 0 {
		return
	}
	p.killLocked()
	p.cur = (idx%len(p.tracks) + len(p.tracks)) % len(p.tracks)
	p.start = from
	if p.seekAt != nil {
		p.seekAt.Stop()
		p.seekAt = nil
	}
	p.pausedAt = time.Time{}
	p.paused.Store(false)
	p.played.Store(0)
	p.visMu.Lock()
	p.bands, p.scope = [fxNB]float32{}, [musScopeN]float32{}
	p.visMu.Unlock()
	path := p.tracks[p.cur]
	if fresh {
		p.changed = time.Now()
		p.gen++
		p.meta = musicMeta{}
		go p.loadMeta(path, p.gen)
	}
	stop, done := make(chan struct{}), make(chan struct{})
	p.stop, p.done = stop, done
	go p.pump(path, from, stop, done)
}

func (p *musicPlayer) loadMeta(path string, gen int) {
	dur, artist := musicProbe(path)
	p.mu.Lock()
	if p.gen == gen {
		p.meta.dur, p.meta.artist = dur, artist
	}
	p.mu.Unlock()
	c := musicCover(path)
	p.mu.Lock()
	if p.gen == gen {
		p.meta.cover = c
	}
	p.mu.Unlock()
}

// Play starts song idx of the queue.
func (p *musicPlayer) Play(idx int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.fails = 0
	p.playLocked(idx, 0, true)
}

// PlayList replaces the queue and plays song idx; dir is remembered as the music folder.
func (p *musicPlayer) PlayList(paths []string, idx int, dir string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.setTracks(paths)
	p.dir = dir
	p.saveLocked()
	p.fails = 0
	p.playLocked(idx, 0, true)
}

// Toggle pauses or plays (and starts the song again after it was stopped).
func (p *musicPlayer) Toggle() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.tracks) == 0 {
		return
	}
	if p.stop == nil {
		p.playLocked(p.cur, p.start, false)
		return
	}
	now := !p.paused.Load()
	p.paused.Store(now)
	if now {
		p.pausedAt = time.Now()
	} else {
		p.pausedAt = time.Time{}
	}
}

// Step moves to the next (1) or previous (-1) song; the first Prev restarts the song.
func (p *musicPlayer) Step(d int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.tracks) == 0 {
		return
	}
	if d < 0 && p.posLocked() > 3 {
		p.playLocked(p.cur, 0, false)
		return
	}
	p.fails = 0
	p.playLocked(p.cur+d, 0, true)
}

// Seek moves by d seconds; held keys add up, and the jump happens once they stop.
func (p *musicPlayer) Seek(d float64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.tracks) == 0 || p.stop == nil {
		return
	}
	t := max(0, p.posLocked()+d)
	if p.meta.dur > 2 {
		t = min(t, p.meta.dur-2)
	}
	p.seekTo = t
	if p.seekAt != nil {
		p.seekAt.Stop()
	}
	p.seekAt = time.AfterFunc(320*time.Millisecond, func() {
		p.mu.Lock()
		defer p.mu.Unlock()
		if p.seekAt != nil {
			p.seekAt = nil
			p.playLocked(p.cur, p.seekTo, false)
		}
	})
}

// SetVol sets the volume (0-100: above 100 the samples clip, which sounds like stutter).
func (p *musicPlayer) SetVol(v int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.vol = max(0, min(v, 100))
	p.volAt.Store(int32(p.vol))
	p.saveLocked()
}

// NextVisual switches the visualizer.
func (p *musicPlayer) NextVisual() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.vmode = (p.vmode + 1) % len(fxModes)
	p.saveLocked()
	return fxModes[p.vmode]
}

// SetVisual picks a visualizer (Settings > Music).
func (p *musicPlayer) SetVisual(i int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.vmode = max(0, i) % len(fxModes)
	p.saveLocked()
}

// Stop ends playback (Duo is closing).
func (p *musicPlayer) Stop() {
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.seekAt != nil {
		p.seekAt.Stop()
		p.seekAt = nil
	}
	p.killLocked()
}

// ended is called by the decode when a song finished on its own: the next one starts.
func (p *musicPlayer) ended(done chan struct{}, heard bool, err error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.done != done { // it was stopped or replaced meanwhile
		return
	}
	p.stop, p.done = nil, nil
	if heard {
		p.fails = 0
	} else {
		p.fails++
	}
	if err != nil || p.fails >= max(1, len(p.tracks)) {
		p.notify("Music: can't play these files", true) // never spin through a folder of bad files
		return
	}
	if !heard {
		p.notify("Music: skipped a file that won't play", true)
	}
	p.playLocked(p.cur+1, 0, true)
}

// ---- the decode ----

func (p *musicPlayer) pump(path string, from float64, stop, done chan struct{}) {
	var err error
	heard := false
	natural := false
	defer func() {
		close(done)
		if natural {
			go p.ended(done, heard, err)
		}
	}()
	args := []string{"-nostdin", "-v", "error"}
	if from > 0.05 {
		args = append(args, "-ss", fmt.Sprintf("%.3f", from))
	}
	args = append(args, "-i", path, "-map", "0:a:0", "-f", "s16le", "-ac", "2", "-ar", "44100", "-")
	ff := exec.Command("ffmpeg", args...)
	out, e := ff.StdoutPipe()
	if e == nil {
		e = ff.Start()
	}
	if e != nil {
		err, natural = e, true
		return
	}
	defer func() { ff.Process.Kill(); ff.Wait() }()
	var sink io.WriteCloser
	var player *exec.Cmd
	if !p.silent {
		// pacat, not pw-play: pw-play reading a pipe drops out hundreds of times a minute on the
		// Cardputer (measured with pw-top in the MP3 Player app)
		player = exec.Command("pacat", "--raw", "--format=s16le", "--rate=44100", "--channels=2",
			"--latency-msec=200", "--client-name=Duo Music")
		if sink, e = player.StdinPipe(); e == nil {
			e = player.Start()
		}
		if e != nil {
			player, sink = nil, nil
		}
	}
	defer func() {
		if player != nil {
			if !natural {
				player.Process.Kill()
			}
			sink.Close()
			player.Wait()
		}
	}()
	dsp := newMusicDSP()
	buf := make([]byte, 8192)
	have := 0
	pcm := make([]byte, 0, 8192)
	mono := make([]float32, 0, 2048)
	var ring [musFFTN]float32
	at, since := 0, 0
	for {
		select {
		case <-stop:
			return
		default:
		}
		if p.paused.Load() {
			select {
			case <-stop:
				return
			case <-time.After(25 * time.Millisecond):
			}
			continue
		}
		n, e := out.Read(buf[have:])
		if n == 0 && e != nil {
			natural = true
			return
		}
		total := have + n
		whole := total / 4 * 4
		g := int(p.volAt.Load())
		pcm, mono = pcm[:0], mono[:0]
		for i := 0; i < whole; i += 4 {
			l := int(int16(binary.LittleEndian.Uint16(buf[i:])))
			r := int(int16(binary.LittleEndian.Uint16(buf[i+2:])))
			if i/4%2 == 0 {
				mono = append(mono, float32(l+r)/65536)
			}
			pcm = binary.LittleEndian.AppendUint16(pcm, uint16(int16(max(-32768, min(32767, l*g/100)))))
			pcm = binary.LittleEndian.AppendUint16(pcm, uint16(int16(max(-32768, min(32767, r*g/100)))))
		}
		copy(buf, buf[whole:total])
		have = total - whole
		if sink != nil {
			if _, e := sink.Write(pcm); e != nil {
				natural = true
				return
			}
		} else { // silent (scripted runs): keep real time
			select {
			case <-stop:
				return
			case <-time.After(time.Duration(float64(len(pcm)) / musBPS * float64(time.Second))):
			}
		}
		var fresh *[fxNB]float32
		for _, s := range mono {
			ring[at] = s
			at = (at + 1) % musFFTN
			if since++; since >= musHop {
				since = 0
				b := dsp.spectrum(&ring, at) // outside the lock: the screen never waits for it
				fresh = &b
			}
		}
		p.played.Add(int64(whole))
		heard = heard || whole > 0
		p.visMu.Lock()
		sp := p.scopePos
		for _, s := range mono {
			p.scope[sp] = s
			sp = (sp + 1) % musScopeN
		}
		p.scopePos = sp
		if fresh != nil {
			p.bands = *fresh
		}
		p.visMu.Unlock()
	}
}

// ---- spectrum ----

type musicDSP struct {
	cos, sin, hann [musFFTN]float32
	bins           [fxNB][2]int
	re, im         [musFFTN]float32
	smooth         [fxNB]float32
	gmax           float32
}

func newMusicDSP() *musicDSP {
	d := &musicDSP{gmax: 1e-4}
	for i := 0; i < musFFTN; i++ {
		a := 2 * math.Pi * float64(i) / musFFTN
		if i < musFFTN/2 {
			d.cos[i], d.sin[i] = float32(math.Cos(a)), -float32(math.Sin(a))
		}
		d.hann[i] = float32(0.5 - 0.5*math.Cos(a))
	}
	// log-spaced bins, low bass on the left (of the 22 kHz copy)
	binHz := float64(musRate/2) / musFFTN
	prev := 1
	for c := 0; c < fxNB; c++ {
		a, b := float64(c)/fxNB, float64(c+1)/fxNB
		lo := int(math.Floor(50 * math.Pow(200, a) / binHz))
		hi := int(math.Ceil(50 * math.Pow(200, b) / binHz))
		lo = min(max(lo, prev, 1), musFFTN/2-2)
		hi = min(max(hi, lo+1), musFFTN/2)
		prev = lo
		d.bins[c] = [2]int{lo, hi}
	}
	return d
}

func (d *musicDSP) fft() {
	n := musFFTN
	j := 0
	for i := 1; i < n; i++ {
		bit := n >> 1
		for j&bit != 0 {
			j ^= bit
			bit >>= 1
		}
		j |= bit
		if i < j {
			d.re[i], d.re[j] = d.re[j], d.re[i]
			d.im[i], d.im[j] = d.im[j], d.im[i]
		}
	}
	for ln := 2; ln <= n; ln <<= 1 {
		half, step := ln/2, n/ln
		for i := 0; i < n; i += ln {
			for k := 0; k < half; k++ {
				wr, wi := d.cos[k*step], d.sin[k*step]
				a, b := i+k, i+k+half
				vr := d.re[b]*wr - d.im[b]*wi
				vi := d.re[b]*wi + d.im[b]*wr
				d.re[b], d.im[b] = d.re[a]-vr, d.im[a]-vi
				d.re[a] += vr
				d.im[a] += vi
			}
		}
	}
}

// spectrum: ring holds the newest samples, oldest at `at`.
func (d *musicDSP) spectrum(ring *[musFFTN]float32, at int) [fxNB]float32 {
	for i := 0; i < musFFTN; i++ {
		d.re[i] = ring[(at+i)%musFFTN] * d.hann[i]
		d.im[i] = 0
	}
	d.fft()
	var e [fxNB]float32
	top := float32(1e-6)
	for c := 0; c < fxNB; c++ {
		lo, hi := d.bins[c][0], d.bins[c][1]
		var s float32
		for b := lo; b < hi; b++ {
			s += d.re[b]*d.re[b] + d.im[b]*d.im[b]
		}
		v := float32(math.Sqrt(float64(s/(float32(hi-lo)*musFFTN)))) * (1 + float32(c)*0.04)
		e[c] = v
		top = max(top, v)
	}
	d.gmax = max(d.gmax*0.99, top, 1e-5)
	for c := 0; c < fxNB; c++ {
		x := float32(math.Sqrt(float64(clampf(e[c]/d.gmax, 0, 1))))
		if x < 0.06 {
			x = 0
		}
		old := d.smooth[c]
		if x > old {
			d.smooth[c] = old*0.2 + x*0.8
		} else {
			d.smooth[c] = old*0.72 + x*0.28
		}
	}
	return d.smooth
}

// ---- files ----

// musicProbe is the song's length in seconds and its artist.
func musicProbe(path string) (float64, string) {
	out, err := run(8*time.Second, "ffprobe", "-v", "error", "-show_entries", "format=duration:format_tags=artist",
		"-of", "default=nw=1", path)
	if err != nil && out == "" {
		return 0, ""
	}
	dur, artist := 0.0, ""
	for _, l := range strings.Split(out, "\n") {
		if v, ok := strings.CutPrefix(strings.TrimSpace(l), "duration="); ok {
			dur, _ = strconv.ParseFloat(v, 64)
		} else if v, ok := strings.CutPrefix(l, "TAG:artist="); ok {
			artist = strings.TrimSpace(v)
		}
	}
	return dur, artist
}

// musicCover is the song's embedded cover as 15x15 pixels (drawn 2x), or nil.
func musicCover(path string) []uint16 {
	cmd := exec.Command("ffmpeg", "-hide_banner", "-nostdin", "-loglevel", "error", "-i", path, "-map", "0:v:0",
		"-frames:v", "1", "-vf", "scale=15:15:force_original_aspect_ratio=increase,crop=15:15",
		"-f", "rawvideo", "-pix_fmt", "rgb565le", "pipe:1")
	b, err := cmd.Output()
	if err != nil || len(b) != 15*15*2 {
		return nil
	}
	px := make([]uint16, 15*15)
	for i := range px {
		px[i] = binary.LittleEndian.Uint16(b[i*2:])
	}
	return px
}

// audioFiles are the songs directly in dir, sorted.
func audioFiles(dir string) []string {
	var out []string
	ents, _ := os.ReadDir(dir)
	for _, e := range ents {
		if !e.IsDir() && isAudio(e.Name()) {
			out = append(out, filepath.Join(dir, e.Name()))
		}
	}
	sort.Strings(out)
	return out
}

// scanMusic finds the songs under dir (5 folders deep, at most 500, no hidden folders).
func scanMusic(dir string) []string {
	var found []string
	var walk func(d string, depth int)
	walk = func(d string, depth int) {
		if depth > 5 || len(found) > 500 {
			return
		}
		ents, _ := os.ReadDir(d)
		for _, e := range ents {
			if strings.HasPrefix(e.Name(), ".") {
				continue
			}
			p := filepath.Join(d, e.Name())
			if e.IsDir() {
				walk(p, depth+1)
			} else if isAudio(e.Name()) {
				found = append(found, p)
			}
		}
	}
	walk(dir, 0)
	sort.Strings(found)
	return found
}

func songTime(t float64) string {
	if math.IsNaN(t) || math.IsInf(t, 0) || t < 0 {
		return "-:--"
	}
	s := int(t)
	if s >= 3600 {
		return fmt.Sprintf("%d:%02d:%02d", s/3600, s/60%60, s%60)
	}
	return fmt.Sprintf("%d:%02d", s/60, s%60)
}
