package main

import (
	"dslauncher/cp0"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// A real video (made with ffmpeg): it plays, pauses, seeks, fills the screen and ends.
func TestVideoPlayer(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("no ffmpeg here")
	}
	dir := t.TempDir()
	setHome(t, dir)
	path := filepath.Join(dir, "clip.mp4")
	if out, err := exec.Command("ffmpeg", "-hide_banner", "-loglevel", "error", "-y",
		"-f", "lavfi", "-i", "testsrc2=size=640x360:rate=24:duration=3",
		"-f", "lavfi", "-i", "sine=frequency=440:duration=3",
		"-c:v", "mpeg4", "-q:v", "5", "-c:a", "aac", "-shortest", path).CombinedOutput(); err != nil {
		t.Fatalf("making the test video: %v %s", err, out)
	}
	a := testApp(t)
	a.sim = true // no pacat
	v := newVideoView(a, path)
	a.push(v)
	defer v.stopPlayback()
	if v.err != "" || v.info.w != 640 || !v.info.audio || v.info.dur < 2.9 {
		t.Fatalf("probe: %+v err %q", v.info, v.err)
	}
	if v.fw != 320 || v.fh != 180&^1 && v.fh != 170 { // 16:9 fits 320 wide
		t.Logf("fitted %dx%d", v.fw, v.fh)
	}
	waitFor(t, "frames", 10*time.Second, func() bool { return v.pb.shownAt.Load() > 10 })
	a.draw()
	a.saveShot("video-playing")
	v.Key(a, cp0.Key{Code: 6}) // 5: pause
	shown := v.pb.shownAt.Load()
	time.Sleep(300 * time.Millisecond)
	if !v.isPaused() || v.pb.shownAt.Load() > shown+1 {
		t.Fatalf("pause: paused %v, frames %d -> %d", v.isPaused(), shown, v.pb.shownAt.Load())
	}
	at := v.pos()
	v.Key(a, cp0.Key{Code: 6})       // play again
	v.Key(a, cp0.Key{Name: "RIGHT"}) // +10 s: past the end, so near the end
	if p := v.pos(); p < at || p > v.info.dur {
		t.Fatalf("seek: %.2f (was %.2f)", p, at)
	}
	v.Key(a, cp0.Key{Code: 8}) // 7: fill the screen
	if !v.fill || v.fw != W || v.fh != H {
		t.Fatalf("fill: %v %dx%d", v.fill, v.fw, v.fh)
	}
	waitFor(t, "the end", 10*time.Second, func() bool { return v.pb.ended.Load() })
	a.draw()
	if !v.isPaused() || v.pos() < v.info.dur-0.01 {
		t.Fatalf("at the end: paused %v pos %.2f", v.isPaused(), v.pos())
	}
	v.Key(a, cp0.Key{Code: 9}) // 8: info
	a.draw()
	a.saveShot("video-info")
	v.Key(a, cp0.Key{Code: 1, Name: "ESC"}) // closes the info
	v.Key(a, cp0.Key{Code: 1, Name: "ESC"}) // and the player
	if a.top() == v {
		t.Fatal("Esc should leave")
	}
}
