package main

import (
	"dslauncher/cp0"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func orderPlayer(n int) *musicPlayer {
	p := &musicPlayer{notify: func(string, bool) {}, repeat: repeatAll}
	var tracks []string
	for i := 0; i < n; i++ {
		tracks = append(tracks, filepath.Join("m", string(rune('a'+i))+".mp3"))
	}
	p.setTracks(tracks)
	return p
}

func TestMusicPlayOrder(t *testing.T) {
	p := orderPlayer(6)
	// in order, wrapping with repeat all
	p.orderFrom(4)
	p.cur = 4
	var got []int
	for i := 0; i < 4; i++ {
		n, ok := p.autoNext()
		if !ok {
			t.Fatal("repeat all should go round")
		}
		p.cur = n
		got = append(got, n)
	}
	if strings.Trim(strings.Join(strings.Fields(strings.Repeat(" ", 0)+fmtInts(got)), ","), ",") != "5,0,1,2" {
		t.Errorf("in order: %v", got)
	}
	// repeat off: the end of the queue stops
	p.repeat = repeatOff
	p.orderFrom(5)
	p.cur = 5
	if _, ok := p.autoNext(); ok {
		t.Error("repeat off should stop after the last song")
	}
	// shuffle: every song once per round, starting with the one chosen
	p.shuffle, p.repeat = true, repeatAll
	p.orderFrom(2)
	p.cur = 2
	if p.order[0] != 2 {
		t.Fatalf("shuffle starts with the chosen song: %v", p.order)
	}
	seen := map[int]bool{2: true}
	for i := 0; i < 5; i++ {
		n, _ := p.autoNext()
		p.cur = n
		if seen[n] {
			t.Fatalf("song %d twice in one round: %v", n, p.order)
		}
		seen[n] = true
	}
	if len(seen) != 6 {
		t.Fatalf("a round missed songs: %v", seen)
	}
	if _, ok := p.autoNext(); !ok {
		t.Fatal("shuffle with repeat all starts a new round")
	}
	// PREV/NEXT step through the same order
	p.orderFrom(0)
	p.cur = 0
	a := p.stepIndex(1)
	p.cur = a // (playing it sets cur)
	if b := p.stepIndex(-1); b != 0 || a == 0 {
		t.Errorf("step: next %d, back %d", a, b)
	}
}

func fmtInts(v []int) string {
	var s []string
	for _, x := range v {
		s = append(s, itoa(x))
	}
	return strings.Join(s, ",")
}

func TestParseAVRCP(t *testing.T) {
	devs := parseAVRCP(strings.NewReader(`I: Bus=0018 Vendor=0000 Product=0000 Version=0000
N: Name="tca8418"
H: Handlers=sysrq kbd event0

I: Bus=0005 Vendor=0000 Product=0000 Version=0000
N: Name="JBL Flip 6 (AVRCP)"
H: Handlers=kbd event4
`))
	if len(devs) != 1 || filepath.Base(devs[0]) != "event4" {
		t.Fatalf("avrcp devices: %v", devs)
	}
}

// Media keys and the lock/home panels (DSL_DOC_SHOTS=<dir> saves them).
func TestMusicMediaKeysAndPanels(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("no ffmpeg here")
	}
	home := t.TempDir()
	setHome(t, home)
	t.Setenv("DSL_CONFIG", filepath.Join(home, ".config/dslauncher/config"))
	os.MkdirAll(filepath.Join(home, "Music"), 0o755)
	writeTone(t, filepath.Join(home, "Music", "Night Drive.wav"), 330, 3)
	writeTone(t, filepath.Join(home, "Music", "Sunrise.wav"), 520, 3)
	a := testApp(t)
	a.music = newMusicPlayer(true, func(string, bool) {})
	defer a.music.Stop()
	a.handle(cp0.Key{Code: 164, Name: "PLAYPAUSE"}) // nothing loaded: starts the music folder
	waitFor(t, "music from a media key", 5*time.Second, func() bool { return a.music.active() })
	a.handle(cp0.Key{Code: 163, Name: "NEXTSONG"})
	if st := a.music.state(); st.cur != 1 {
		t.Fatalf("NEXTSONG: cur %d", st.cur)
	}
	if !strings.Contains(homeTitle(a), "Sunrise") {
		t.Fatalf("home title: %q", homeTitle(a))
	}
	a.draw()
	a.saveShot("music-home")
	a.push(newLock(a))
	a.draw()
	a.saveShot("music-lock")
	a.top().Key(a, cp0.Key{Code: 6}) // 5 on the lock screen: pause, still locked
	if st := a.music.state(); !st.paused {
		t.Fatal("5 on the lock screen should pause")
	}
	if _, ok := a.top().(*Lock); !ok {
		t.Fatal("music keys must not unlock")
	}
	// S and R in Music
	now := &MusicNow{fx: newMusicFx(), last: time.Now()}
	a.push(now)
	now.Key(a, cp0.Key{Ch: 's'})
	now.Key(a, cp0.Key{Ch: 'r'}) // all -> one
	if sh, rp := a.music.playOrder(); !sh || rp != repeatOne {
		t.Fatalf("S/R: shuffle %v repeat %d", sh, rp)
	}
	a.draw()
	a.saveShot("music-flags")
}
