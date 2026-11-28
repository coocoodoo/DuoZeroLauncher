package main

// The video player (Files opens videos with it). ffmpeg decodes the video scaled to the
// screen as raw RGB565 frames on its standard output, and the sound as s16le stereo 44.1 kHz on
// a second pipe, which goes to pacat with the player's volume. Frames are shown against a wall
// clock (paused time left out), which also paces ffmpeg, so picture and sound stay together; a
// late frame is shown at once rather than waited for.
//
// Keys: 4 back 10 s, 5 play/pause, 6 forward 10 s, 7 fit / fill the screen, 8 info; Z/C seek,
// F/X volume, Enter shows or hides the key bar (it hides itself after 3 s of playing), Esc
// leaves.

import (
	"dslauncher/cp0"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type videoInfo struct {
	w, h     int
	fps      float64
	dur      float64
	codec    string
	audio    bool
	audioCod string
}

// probeVideo asks ffprobe about a video's first picture stream, its sound and its length.
func probeVideo(path string) (videoInfo, error) {
	out, err := run(15*time.Second, "ffprobe", "-v", "error", "-show_entries",
		"stream=codec_type,codec_name,width,height,avg_frame_rate:format=duration", "-of", "json", path)
	if err != nil && out == "" {
		return videoInfo{}, errors.New("ffprobe can't read this file")
	}
	var j struct {
		Streams []struct {
			CodecType    string `json:"codec_type"`
			CodecName    string `json:"codec_name"`
			Width        int    `json:"width"`
			Height       int    `json:"height"`
			AvgFrameRate string `json:"avg_frame_rate"`
		} `json:"streams"`
		Format struct {
			Duration string `json:"duration"`
		} `json:"format"`
	}
	if err := json.Unmarshal([]byte(out), &j); err != nil {
		return videoInfo{}, errors.New("ffprobe can't read this file")
	}
	var vi videoInfo
	for _, s := range j.Streams {
		switch s.CodecType {
		case "video":
			if vi.w == 0 && s.Width > 0 {
				vi.w, vi.h, vi.codec = s.Width, s.Height, s.CodecName
				if n, d, ok := strings.Cut(s.AvgFrameRate, "/"); ok {
					a, _ := strconv.ParseFloat(n, 64)
					b, _ := strconv.ParseFloat(d, 64)
					if b > 0 {
						vi.fps = a / b
					}
				}
			}
		case "audio":
			if !vi.audio {
				vi.audio, vi.audioCod = true, s.CodecName
			}
		}
	}
	vi.dur, _ = strconv.ParseFloat(j.Format.Duration, 64)
	if vi.w == 0 {
		return vi, errors.New("there's no picture in this file")
	}
	if vi.fps <= 0 || vi.fps > 120 {
		vi.fps = 25
	}
	return vi, nil
}

// videoPlayback is one ffmpeg run (from one start time); seeking makes a new one.
type videoPlayback struct {
	cmd     *exec.Cmd
	stop    chan struct{}
	done    chan struct{}
	ended   atomic.Bool
	shownAt atomic.Int64 // frames shown
}

type VideoView struct {
	path    string
	info    videoInfo
	err     string
	fw, fh  int
	fill    bool
	vol     atomic.Int32
	silent  bool
	pb      *videoPlayback
	start   float64 // where this playback started (seconds)
	clock   time.Time
	paused  bool
	pausedT float64 // the position when paused
	frameMu sync.Mutex
	frame   []uint16
	barTill time.Time
	info8   bool
}

func newVideoView(a *App, path string) *VideoView {
	v := &VideoView{path: path, silent: a.sim, barTill: time.Now().Add(3 * time.Second)}
	v.vol.Store(100)
	vi, err := probeVideo(path)
	if err != nil {
		v.err = err.Error()
		return v
	}
	v.info = vi
	v.layout()
	v.play(0)
	return v
}

// layout fits the picture to the screen, or fills it (cutting the edges off).
func (v *VideoView) layout() {
	if v.fill {
		v.fw, v.fh = W, H
	} else {
		v.fw, v.fh = fitIn(v.info.w, v.info.h, W, H)
		v.fw &^= 1
		v.fh &^= 1
	}
	v.frameMu.Lock()
	v.frame = nil
	v.frameMu.Unlock()
}

// pos is where the video is now (seconds).
func (v *VideoView) pos() float64 {
	v.frameMu.Lock()
	defer v.frameMu.Unlock()
	t := v.pausedT
	if !v.paused && v.pb != nil {
		t = v.start + time.Since(v.clock).Seconds()
	}
	if v.info.dur > 0 {
		t = min(t, v.info.dur)
	}
	return t
}

func (v *VideoView) stopPlayback() {
	if v.pb == nil {
		return
	}
	close(v.pb.stop)
	if v.pb.cmd.Process != nil {
		v.pb.cmd.Process.Kill()
	}
	<-v.pb.done
	v.pb = nil
}

// play starts ffmpeg at t seconds.
func (v *VideoView) play(t float64) {
	v.stopPlayback()
	t = max(0, t)
	if v.info.dur > 1 {
		t = min(t, v.info.dur-0.5)
	}
	fps := min(v.info.fps, 30)
	vf := fmt.Sprintf("scale=%d:%d:flags=bilinear,fps=%.3f", v.fw, v.fh, fps)
	if v.fill {
		vf = fmt.Sprintf("scale=%d:%d:force_original_aspect_ratio=increase:flags=bilinear,crop=%d:%d,fps=%.3f", W, H, W, H, fps)
	}
	args := []string{"-hide_banner", "-loglevel", "error", "-nostdin"}
	if t > 0.05 {
		args = append(args, "-ss", fmt.Sprintf("%.3f", t))
	}
	args = append(args, "-i", v.path, "-map", "0:v:0", "-vf", vf, "-f", "rawvideo", "-pix_fmt", "rgb565le", "pipe:1")
	withSound := v.info.audio && !v.silent && runtime.GOOS != "windows"
	var ar, aw *os.File
	if withSound {
		var err error
		if ar, aw, err = os.Pipe(); err != nil {
			withSound = false
		} else {
			args = append(args, "-map", "0:a:0", "-f", "s16le", "-ac", "2", "-ar", "44100", "pipe:3")
		}
	}
	pb := &videoPlayback{cmd: exec.Command("ffmpeg", args...), stop: make(chan struct{}), done: make(chan struct{})}
	if withSound {
		pb.cmd.ExtraFiles = []*os.File{aw}
	}
	out, err := pb.cmd.StdoutPipe()
	if err == nil {
		err = pb.cmd.Start()
	}
	if aw != nil {
		aw.Close() // ffmpeg has its own copy
	}
	if err != nil {
		if ar != nil {
			ar.Close()
		}
		v.err = "ffmpeg didn't start: " + err.Error()
		close(pb.done)
		return
	}
	v.pb = pb
	v.frameMu.Lock()
	v.start, v.clock, v.paused = t, time.Now(), false
	v.frameMu.Unlock()
	fw, fh := v.fw, v.fh
	var wg sync.WaitGroup
	if withSound {
		wg.Add(1)
		go func() { defer wg.Done(); v.sound(pb, ar) }()
	}
	wg.Add(1)
	go func() { defer wg.Done(); v.pictures(pb, out, fps, fw, fh) }()
	go func() {
		wg.Wait()
		pb.cmd.Wait()
		close(pb.done)
	}()
}

// pictures reads frames and shows each when its time comes.
func (v *VideoView) pictures(pb *videoPlayback, out io.Reader, fps float64, w, h int) {
	raw := make([]byte, w*h*2)
	var spare []uint16 // the frame before last, used again (30 new 100 KB frames a second kept the collector busy)
	for n := 0; ; n++ {
		if _, err := io.ReadFull(out, raw); err != nil {
			pb.ended.Store(true)
			return
		}
		due := float64(n) / fps
		for { // wait for the frame's time (paused: wait for play)
			select {
			case <-pb.stop:
				return
			default:
			}
			el := v.elapsed(pb)
			if el >= due-0.004 {
				break
			}
			time.Sleep(time.Duration(min(due-el, 0.02) * float64(time.Second)))
		}
		px := spare
		if len(px) != w*h {
			px = make([]uint16, w*h)
		}
		for i := range px {
			px[i] = binary.LittleEndian.Uint16(raw[i*2:])
		}
		v.frameMu.Lock()
		v.frame, spare = px, v.frame // (Draw copies the frame while it holds the lock)
		v.frameMu.Unlock()
		pb.shownAt.Add(1)
	}
}

// elapsed is the time played since this playback started (paused time left out).
func (v *VideoView) elapsed(pb *videoPlayback) float64 {
	v.frameMu.Lock()
	defer v.frameMu.Unlock()
	if v.paused {
		return v.pausedT - v.start
	}
	return time.Since(v.clock).Seconds()
}

// sound feeds pacat, with the volume applied; it waits while paused.
func (v *VideoView) sound(pb *videoPlayback, ar *os.File) {
	defer ar.Close()
	sink := exec.Command("pacat", "--raw", "--format=s16le", "--rate=44100", "--channels=2",
		"--latency-msec=120", "--client-name=Duo Video")
	in, err := sink.StdinPipe()
	if err == nil {
		err = sink.Start()
	}
	if err != nil {
		io.Copy(io.Discard, ar)
		return
	}
	defer func() { in.Close(); sink.Process.Kill(); sink.Wait() }()
	buf := make([]byte, 8192)
	have := 0 // bytes of a sample cut in two by a read, kept for the next one
	for {
		select {
		case <-pb.stop:
			return
		default:
		}
		for v.isPaused() {
			select {
			case <-pb.stop:
				return
			case <-time.After(30 * time.Millisecond):
			}
		}
		n, err := ar.Read(buf[have:])
		// only whole left+right pairs go on: a read that ended inside a sample would shift every
		// later one by a byte (noise, at any volume below full)
		total := have + n
		whole := total / 4 * 4
		if whole > 0 {
			g := int(v.vol.Load())
			for i := 0; i < whole; i += 2 {
				s := int(int16(binary.LittleEndian.Uint16(buf[i:])))
				binary.LittleEndian.PutUint16(buf[i:], uint16(int16(max(-32768, min(32767, s*g/100)))))
			}
			if _, err := in.Write(buf[:whole]); err != nil {
				return
			}
		}
		have = copy(buf, buf[whole:total])
		if err != nil {
			return
		}
	}
}

func (v *VideoView) isPaused() bool {
	v.frameMu.Lock()
	defer v.frameMu.Unlock()
	return v.paused
}

func (v *VideoView) togglePause() {
	v.frameMu.Lock()
	defer v.frameMu.Unlock()
	if v.paused {
		v.paused = false
		v.clock = time.Now().Add(-time.Duration((v.pausedT - v.start) * float64(time.Second)))
	} else {
		v.pausedT = v.start + time.Since(v.clock).Seconds()
		v.paused = true
	}
}

func (v *VideoView) seek(d float64) {
	t := v.pos() + d
	wasPaused := v.isPaused()
	v.play(t)
	if wasPaused {
		v.togglePause()
	}
}

func (v *VideoView) Animating(a *App) bool { return v.pb != nil && !v.paused }

func (v *VideoView) Key(a *App, k cp0.Key) {
	if k.Name == "ENTER" && !v.info8 {
		if time.Now().Before(v.barTill) {
			v.barTill = time.Now()
		} else {
			v.barTill = time.Now().Add(3 * time.Second)
		}
		return
	}
	v.barTill = time.Now().Add(3 * time.Second)
	if v.info8 {
		if k.Back() || k.Slot() == 4 || k.Name == "ENTER" {
			v.info8 = false
		}
		return
	}
	switch {
	case k.Back():
		v.stopPlayback()
		a.snd.Play("back")
		a.pop()
	case v.err != "":
	case k.Slot() == 1 || k.Name == "SPACE" || k.Ch == ' ':
		if v.pb == nil || v.pb.ended.Load() {
			v.play(0)
		} else {
			v.togglePause()
		}
	case k.Slot() == 0 || k.Name == "LEFT":
		v.seek(-10)
	case k.Slot() == 2 || k.Name == "RIGHT":
		v.seek(10)
	case k.Slot() == 3:
		v.fill = !v.fill
		t := v.pos()
		v.stopPlayback() // before the size changes under it
		v.layout()
		v.play(t)
	case k.Slot() == 4:
		v.info8 = true
	case k.Name == "UP":
		v.vol.Store(min(100, v.vol.Load()+10))
		a.notify(fmt.Sprintf("Volume %d", v.vol.Load()), false)
	case k.Name == "DOWN":
		v.vol.Store(max(0, v.vol.Load()-10))
		a.notify(fmt.Sprintf("Volume %d", v.vol.Load()), false)
	}
}

func (v *VideoView) Bar(a *App) []BarItem {
	if time.Now().After(v.barTill) && !v.paused && v.err == "" {
		return nil
	}
	play := BarItem{"PAUSE", artPause, v.err != ""}
	if v.paused || v.pb == nil || v.pb.ended.Load() {
		play = BarItem{"PLAY", artPlay, v.err != ""}
	}
	return []BarItem{{"-10 S", artPrev, v.err != ""}, play, {"+10 S", artNext, v.err != ""},
		{map[bool]string{true: "FIT", false: "FILL"}[v.fill], artZoom, v.err != ""}, {"INFO", artInfo, false}}
}

func (v *VideoView) Draw(a *App) {
	g := a.g
	g.noClip()
	g.rect(0, 0, W, H, cBlack)
	if v.err != "" {
		g.textC(W/2, 50, "Can't play this video", cWhite, 1, false)
		g.textC(W/2, 64, fit(v.err, W-20, 1, false), hex(0xa0a0a0), 1, false)
		return
	}
	v.frameMu.Lock()
	fr := v.frame
	shown := fr != nil && len(fr) == v.fw*v.fh
	if shown {
		ox, oy := (W-v.fw)/2, (H-v.fh)/2
		for y := 0; y < v.fh; y++ {
			copy(g.s.Px[(oy+y)*W+ox:(oy+y)*W+ox+v.fw], fr[y*v.fw:(y+1)*v.fw])
		}
	}
	v.frameMu.Unlock()
	if !shown && v.pb != nil && !v.pb.ended.Load() {
		g.dsLoading(W/2-14, H/2-20, a.t)
	}
	if v.pb != nil && v.pb.ended.Load() { // the end: stop the clock there and show the bar
		v.frameMu.Lock()
		if !v.paused {
			v.pausedT, v.paused = v.info.dur, true
		}
		v.frameMu.Unlock()
	}
	if v.Bar(a) != nil { // the overlay: name, time and a progress line
		g.shade(0, 0, W, 12, cBlack, 0.55)
		pos, dur := min(v.pos(), max(v.info.dur, v.pos())), v.info.dur
		right := songTime(pos)
		if dur > 0 {
			right += " / " + songTime(dur)
		}
		rw := textW(right, 1, false)
		g.text(4, 1, fit(musicASCII(filepath.Base(v.path)), W-rw-14, 1, false), cWhite, 1, false)
		g.textR(W-4, 1, right, cWhite, 1, false)
		if dur > 0 {
			f := max(0, min(pos/dur, 1))
			g.rect(0, 130, W, 2, hex(0x303030))
			g.rect(0, 130, int(float64(W)*f), 2, a.theme())
		}
		if v.paused {
			g.textC(W/2, 60, "PAUSED", hex(0xffc054), 1, false)
		}
	}
	if v.info8 {
		vi := v.info
		lines := []string{musicASCII(filepath.Base(v.path)), fmt.Sprintf("%d x %d, %.3g fps, %s", vi.w, vi.h, vi.fps, strings.ToUpper(vi.codec))}
		if vi.audio {
			lines = append(lines, "Sound: "+strings.ToUpper(vi.audioCod))
		} else {
			lines = append(lines, "No sound")
		}
		if vi.dur > 0 {
			lines = append(lines, "Length: "+songTime(vi.dur))
		}
		if fi, err := os.Stat(v.path); err == nil {
			lines = append(lines, humanSize(fi.Size()))
		}
		h := 22 + len(lines)*12
		x, y, w := 24, (132-h)/2, W-48
		g.shade(0, 0, W, 132, cBlack, 0.35)
		g.dsAlert(x, y, w, h)
		g.textC(W/2, y+7, "VIDEO", cPictoEnter, 1, false)
		for i, l := range lines {
			g.textC(W/2, y+20+i*12, fit(l, w-14, 1, false), cWhite, 1, false)
		}
	}
	if !v.paused {
		a.lastKey = time.Now() // a playing video keeps the screen on
	}
}
