package main

// Music extras: shuffle and repeat, media keys (the Cardputer's Fn+Q / Fn+W / Fn+E, and
// Bluetooth headphones or a car stereo), and the now-playing panels on the lock and home
// screens.

import (
	"bufio"
	"dslauncher/cp0"
	"encoding/binary"
	"io"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	repeatOff = iota
	repeatAll
	repeatOne
)

var repeatNames = []string{"OFF", "ALL", "ONE"}

// ---- play order (called with p.mu held) ----

// orderFrom makes the play order start at song idx: shuffled after it, or in file order.
func (p *musicPlayer) orderFrom(idx int) {
	n := len(p.tracks)
	p.order = make([]int, n)
	for i := range p.order {
		p.order[i] = i
	}
	if n == 0 {
		p.opos = 0
		return
	}
	idx = (idx%n + n) % n
	if p.shuffle {
		rand.Shuffle(n, func(i, j int) { p.order[i], p.order[j] = p.order[j], p.order[i] })
		for i, v := range p.order { // the chosen song first
			if v == idx {
				p.order[0], p.order[i] = p.order[i], p.order[0]
			}
		}
		p.opos = 0
		return
	}
	p.opos = idx
}

func (p *musicPlayer) ensureOrder() {
	if len(p.order) != len(p.tracks) {
		p.orderFrom(p.cur)
		return
	}
	if p.opos >= len(p.order) || p.order[p.opos] != p.cur {
		for i, v := range p.order {
			if v == p.cur {
				p.opos = i
			}
		}
	}
}

// stepIndex is the song d steps from the current one in the play order (wrapping: a press of
// NEXT on the last song goes round).
func (p *musicPlayer) stepIndex(d int) int {
	p.ensureOrder()
	n := len(p.order)
	if n == 0 {
		return 0
	}
	p.opos = ((p.opos+d)%n + n) % n
	return p.order[p.opos]
}

// autoNext is the song after this one when a song ends; false at the end with repeat off.
func (p *musicPlayer) autoNext() (int, bool) {
	p.ensureOrder()
	n := len(p.order)
	if n == 0 {
		return 0, false
	}
	if p.opos+1 >= n {
		if p.repeat != repeatAll {
			return 0, false
		}
		if p.shuffle { // a new shuffle for the next time round
			last := p.cur
			p.orderFrom(p.order[rand.Intn(n)])
			if n > 1 && p.order[0] == last {
				p.order[0], p.order[1] = p.order[1], p.order[0]
			}
			p.opos = 0
			return p.order[0], true
		}
		p.opos = 0
		return p.order[0], true
	}
	p.opos++
	return p.order[p.opos], true
}

// SetShuffle turns shuffle on or off; the song playing stays where it is.
func (p *musicPlayer) SetShuffle(on bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.shuffle = on
	p.orderFrom(p.cur)
	p.saveLocked()
}

// SetRepeat picks repeatOff, repeatAll or repeatOne.
func (p *musicPlayer) SetRepeat(r int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.repeat = max(0, min(r, repeatOne))
	p.saveLocked()
}

func (p *musicPlayer) playOrder() (shuffle bool, repeat int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.shuffle, p.repeat
}

// musicPlayRows are Settings > Music's play-order rows.
func musicPlayRows(a *App, l *RowList) []*Row {
	p := a.musicPlayer()
	sh, rp := p.playOrder()
	return []*Row{
		{Label: "Shuffle", Kind: rowToggle, On: sh, Do: func(a *App, r *Row) { p.SetShuffle(r.On) }},
		{Label: "Repeat", Kind: rowChoice, Choices: repeatNames, Choice: rp, Do: func(a *App, r *Row) { p.SetRepeat(r.Choice) }},
	}
}

// ---- media keys ----

func isMediaKey(k cp0.Key) bool {
	switch k.Name {
	case "PLAYPAUSE", "NEXTSONG", "PREVIOUSSONG", "STOPCD":
		return true
	}
	return false
}

// mediaKey plays, pauses or skips from anywhere (also with the screen off). PLAY with nothing
// loaded starts the music folder.
func (a *App) mediaKey(name string) {
	p := a.musicPlayer()
	st := p.state()
	if len(st.tracks) == 0 {
		if name != "PLAYPAUSE" {
			return
		}
		dir := p.musicDir()
		songs := scanMusic(dir)
		if len(songs) == 0 {
			a.notify("No songs in "+tilde(dir), true)
			return
		}
		p.PlayList(songs, 0, dir)
	} else {
		switch name {
		case "PLAYPAUSE":
			p.Toggle()
		case "STOPCD":
			if st.playing && !st.paused {
				p.Toggle()
			}
		case "NEXTSONG":
			p.Step(1)
		case "PREVIOUSSONG":
			p.Step(-1)
		}
	}
	if a.dimLevel == 0 {
		st = p.state()
		title := ""
		if st.cur < len(st.titles) {
			title = musicASCII(st.titles[st.cur])
		}
		verb := map[bool]string{true: "Paused", false: "Playing"}[st.paused]
		a.notify(verb+": "+fit(title, 200, 1, false), false)
	}
	a.dirty = true
}

// avrcpCodes are the keys a Bluetooth remote sends (BlueZ turns AVRCP into an input device).
var avrcpCodes = map[int]string{163: "NEXTSONG", 164: "PLAYPAUSE", 165: "PREVIOUSSONG", 166: "STOPCD",
	200: "PLAYPAUSE", 201: "PLAYPAUSE", 207: "PLAYPAUSE"}

// watchRemotes listens to Bluetooth remotes (headphones, a car): every few seconds it looks in
// /proc/bus/input/devices for "AVRCP" devices and reads their media keys.
func (a *App) watchRemotes() {
	var mu sync.Mutex
	open := map[string]bool{}
	for {
		for _, dev := range avrcpDevices() {
			mu.Lock()
			busy := open[dev]
			open[dev] = true
			mu.Unlock()
			if busy {
				continue
			}
			go func(dev string) {
				defer func() { mu.Lock(); delete(open, dev); mu.Unlock() }()
				f, err := os.Open(dev)
				if err != nil {
					return
				}
				defer f.Close()
				size := 24 // struct input_event on 64-bit
				if len(inputEvent(0, 0, 0)) == 16 {
					size = 16
				}
				buf := make([]byte, size*16)
				for {
					n, err := f.Read(buf)
					if err != nil {
						return // the remote went away
					}
					for i := 0; i+size <= n; i += size {
						ev := buf[i : i+size]
						typ := binary.LittleEndian.Uint16(ev[size-8:])
						code := int(binary.LittleEndian.Uint16(ev[size-6:]))
						val := int32(binary.LittleEndian.Uint32(ev[size-4:]))
						if name, ok := avrcpCodes[code]; ok && typ == 1 && val == 1 {
							a.work <- func() { a.mediaKey(name) }
						}
					}
				}
			}(dev)
		}
		time.Sleep(5 * time.Second)
	}
}

// avrcpDevices are the event devices whose name says AVRCP.
func avrcpDevices() []string {
	f, err := os.Open("/proc/bus/input/devices")
	if err != nil {
		return nil
	}
	defer f.Close()
	return parseAVRCP(f)
}

// parseAVRCP reads /proc/bus/input/devices' format.
func parseAVRCP(r io.Reader) []string {
	var out []string
	name := ""
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		l := sc.Text()
		switch {
		case strings.HasPrefix(l, "N: Name="):
			name = l
		case strings.HasPrefix(l, "H: Handlers=") && strings.Contains(strings.ToUpper(name), "AVRCP"):
			for _, h := range strings.Fields(strings.TrimPrefix(l, "H: Handlers=")) {
				if strings.HasPrefix(h, "event") {
					out = append(out, filepath.Join("/dev/input", h))
				}
			}
		case l == "":
			name = ""
		}
	}
	return out
}

// ---- now playing on the lock and home screens ----

// nowPlaying is the song for the panels: its title and whether music is loaded at all.
func (a *App) nowPlaying() (title, artist string, st musicState, ok bool) {
	if a.music == nil {
		return "", "", st, false
	}
	st = a.music.state()
	if !st.playing || st.cur >= len(st.titles) {
		return "", "", st, false
	}
	return musicASCII(st.titles[st.cur]), musicASCII(st.meta.artist), st, true
}

// drawLockMusic is the lock screen's now-playing bubble (instead of the message); keys 4 5 6
// work there without unlocking.
func drawLockMusic(a *App, x, y, w int) bool {
	title, artist, st, ok := a.nowPlaying()
	if !ok {
		return false
	}
	g := a.g
	g.dsBubble(x, y, w, 40)
	label := "Now playing"
	if st.paused {
		label = "Paused"
	}
	g.dsLabel(x+3, y+3, label, a.theme())
	g.text(x+6, y+18, fit(title, w-12, 1, false), cBlack, 1, false)
	sub := artist
	if sub == "" {
		sub = songTime(st.pos)
		if st.meta.dur > 0 {
			sub += " / " + songTime(st.meta.dur)
		}
	}
	g.text(x+6, y+29, fit(sub, w-70, 1, false), cBorder, 1, false)
	g.textR(x+w-6, y+29, "4 5 6", a.theme().darker(0.25), 1, false)
	if st.meta.dur > 0 { // a thin progress line along the bottom
		f := max(0, min(st.pos/st.meta.dur, 1))
		g.rect(x+4, y+38, int(float64(w-8)*f), 1, a.theme())
	}
	return true
}

// lockMusicKey handles 4 / 5 / 6 on the lock screen while music is loaded.
func lockMusicKey(a *App, k cp0.Key) bool {
	if _, _, _, ok := a.nowPlaying(); !ok {
		return false
	}
	switch k.Slot() {
	case 0:
		a.music.Step(-1)
	case 1:
		a.music.Toggle()
	case 2:
		a.music.Step(1)
	default:
		return false
	}
	a.dirty = true
	return true
}

// homeTitle is what the home screen's title bar says: your name, or the song while one plays.
func homeTitle(a *App) string {
	if title, _, st, ok := a.nowPlaying(); ok && !st.paused {
		return "~ " + title
	}
	return a.cfg.Name
}

func musicFlags(sh bool, rp int) string {
	var parts []string
	if sh {
		parts = append(parts, "SHUF")
	}
	switch rp {
	case repeatOne:
		parts = append(parts, "RPT1")
	case repeatOff:
		parts = append(parts, "NO RPT")
	}
	return strings.Join(parts, " ")
}
