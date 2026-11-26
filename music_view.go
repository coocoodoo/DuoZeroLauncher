package main

// Music's screens: Now playing (visualizer, song, progress), the queue and the folders. Keys,
// as in the MP3 Player app: 4 PREV, 5 PLAY/PAUSE, 6 NEXT, 7 VISUAL (or back to Now playing),
// 8 the queue / the folders; Tab moves between the three, Esc closes Music (the music goes on).

import (
	"dslauncher/cp0"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

var (
	artMusic = []string{
		"...........",
		".....#####.",
		".....#...#.",
		".....#...#.",
		".....#...#.",
		".....#...#.",
		"...###.###.",
		"..########.",
		"..###.###..",
		"...........",
		"...........",
	}
	artNote = []string{ // the status-bar note, 7 x 9
		"...###",
		"...#.#",
		"...#.#",
		"...#.#",
		".###.#",
		"####.#",
		".##.##",
		"....##",
		"......",
	}
	artPrev = []string{
		"...........",
		"...........",
		".#....#...#",
		".#...##..##",
		".#..###.###",
		".#.########",
		".#..###.###",
		".#...##..##",
		".#....#...#",
		"...........",
		"...........",
	}
	artNext = []string{
		"...........",
		"...........",
		"#...#....#.",
		"##..##...#.",
		"###.###..#.",
		"########.#.",
		"###.###..#.",
		"##..##...#.",
		"#...#....#.",
		"...........",
		"...........",
	}
	artPlay = []string{
		"...........",
		"...#.......",
		"...##......",
		"...###.....",
		"...####....",
		"...#####...",
		"...####....",
		"...###.....",
		"...##......",
		"...#.......",
		"...........",
	}
	artPause = []string{
		"...........",
		"..###.###..",
		"..###.###..",
		"..###.###..",
		"..###.###..",
		"..###.###..",
		"..###.###..",
		"..###.###..",
		"..###.###..",
		"..###.###..",
		"...........",
	}
	artVisual = []string{
		"...........",
		".....#.....",
		"..#.....#..",
		"...........",
		"#...###...#",
		"....###....",
		"#...###...#",
		"...........",
		"..#.....#..",
		".....#.....",
		"...........",
	}
	artQueue = []string{
		"...........",
		"...........",
		"##.#######.",
		"...........",
		"##.#######.",
		"...........",
		"##.#######.",
		"...........",
		"##.#######.",
		"...........",
		"...........",
	}
	artFolder = []string{
		"...........",
		"...........",
		"####.......",
		"#...#......",
		"#########..",
		"#.......#..",
		"#.......#..",
		"#.......#..",
		"#########..",
		"...........",
		"...........",
	}
)

// musicPlayer is the App's player, made when first needed.
func (a *App) musicPlayer() *musicPlayer {
	if a.music == nil {
		a.music = newMusicPlayer(a.sim, func(msg string, bad bool) {
			a.work <- func() { a.notify(msg, bad) }
		})
	}
	return a.music
}

// openMusic opens Music on Now playing; the first time it plays the music folder, or shows the
// folders if there's nothing there.
func (a *App) openMusic() {
	p := a.musicPlayer()
	if len(p.state().tracks) == 0 {
		dir := p.musicDir()
		if songs := scanMusic(dir); len(songs) > 0 {
			p.PlayList(songs, 0, dir)
		} else {
			a.push(newMusicFiles(a, dir))
			a.notify("No songs in "+tilde(dir)+": pick a folder", false)
			return
		}
	}
	a.push(&MusicNow{fx: newMusicFx(), last: time.Now()})
}

// musicKeys are the keys every Music screen shares; true if k was one of them.
func musicKeys(a *App, k cp0.Key, here string) bool {
	p := a.musicPlayer()
	if k.Repeat && k.Slot() < 0 {
		return false
	}
	switch k.Slot() {
	case 0:
		p.Step(-1)
	case 1:
		p.Toggle()
	case 2:
		p.Step(1)
	case 3:
		if here == "now" {
			a.notify("Visual: "+p.NextVisual(), false)
		} else {
			a.replace(&MusicNow{fx: newMusicFx(), last: time.Now()})
		}
	case 4:
		if here == "queue" {
			a.replace(newMusicFiles(a, p.musicDir()))
		} else {
			openQueue(a)
		}
	default:
		switch {
		case k.Name == "SPACE" || k.Ch == ' ':
			p.Toggle()
		case k.Name == "TAB":
			switch here {
			case "now":
				openQueue(a)
			case "queue":
				a.replace(newMusicFiles(a, p.musicDir()))
			default:
				a.replace(&MusicNow{fx: newMusicFx(), last: time.Now()})
			}
		case k.Back():
			a.snd.Play("back")
			a.pop()
		default:
			return false
		}
	}
	a.dirty = true
	return true
}

func openQueue(a *App) {
	st := a.musicPlayer().state()
	if len(st.tracks) == 0 {
		a.notify("No songs yet: pick a folder", true)
		a.replace(newMusicFiles(a, a.music.musicDir()))
		return
	}
	a.replace(&MusicQueue{sel: st.cur})
}

func musicBar(a *App, four, five string, fourArt, fiveArt []string) []BarItem {
	st := a.musicPlayer().state()
	play := BarItem{"PLAY", artPlay, len(st.tracks) == 0}
	if st.playing && !st.paused {
		play = BarItem{"PAUSE", artPause, false}
	}
	return []BarItem{{"PREV", artPrev, len(st.tracks) == 0}, play, {"NEXT", artNext, len(st.tracks) == 0},
		{four, fourArt, false}, {five, fiveArt, false}}
}

// musicASCII keeps what Duo's font can draw: plain forms of common punctuation and accents.
func musicASCII(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= ' ' && r <= '~':
			b.WriteRune(r)
		case r >= 0xFF01 && r <= 0xFF5E:
			b.WriteRune(r - 0xFEE0)
		case r == 0x2018 || r == 0x2019:
			b.WriteByte('\'')
		case r == 0x201C || r == 0x201D:
			b.WriteByte('"')
		case r == 0x2013 || r == 0x2014 || r == 0x2212:
			b.WriteByte('-')
		case r == 0x2026:
			b.WriteString("...")
		case r == 0x3000 || r == '\t':
			b.WriteByte(' ')
		case strings.ContainsRune("éèêë", r):
			b.WriteByte('e')
		case strings.ContainsRune("áàâä", r):
			b.WriteByte('a')
		case strings.ContainsRune("óòôö", r):
			b.WriteByte('o')
		case strings.ContainsRune("úùûü", r):
			b.WriteByte('u')
		case strings.ContainsRune("íìîï", r):
			b.WriteByte('i')
		case r == 'ñ':
			b.WriteByte('n')
		case r == 'ç':
			b.WriteByte('c')
		}
	}
	return strings.Join(strings.Fields(b.String()), " ")
}

// ---- Now playing ----

const (
	musVisX = 3
	musVisY = 19
	musInfo = 94 // the song panel
)

type MusicNow struct {
	fx   *musicFx
	last time.Time
}

func (v *MusicNow) Animating(a *App) bool {
	st := a.musicPlayer().state()
	fading := !st.pausedAt.IsZero() && time.Since(st.pausedAt) < 1600*time.Millisecond
	return (st.playing && !st.paused) || fading
}

func (v *MusicNow) Key(a *App, k cp0.Key) {
	if musicKeys(a, k, "now") {
		return
	}
	p := a.musicPlayer()
	switch k.Name {
	case "ENTER":
		if !k.Repeat {
			p.Toggle()
		}
	case "LEFT":
		p.Seek(-10)
	case "RIGHT":
		p.Seek(10)
	case "UP":
		p.SetVol(p.state().vol + 5)
		a.snd.Play("tick")
	case "DOWN":
		p.SetVol(p.state().vol - 5)
		a.snd.Play("tick")
	}
}

func (v *MusicNow) Bar(a *App) []BarItem {
	return musicBar(a, "VISUAL", "QUEUE", artVisual, artQueue)
}

func (v *MusicNow) Draw(a *App) {
	g := a.g
	p := a.musicPlayer()
	st := p.state()
	now := time.Now()
	dt := float32(now.Sub(v.last).Seconds())
	v.last = now
	frozen := !st.pausedAt.IsZero() && time.Since(st.pausedAt) > 1600*time.Millisecond // a faded pause costs nothing
	if !frozen {
		bands, scope := p.vis()
		v.fx.step(st.mode, &bands, &scope, dt, st.playing && !st.paused)
	}
	pageTitle(a, "Music", artMusic)

	// the visualizer, in a dark frame with the visual's colour on the corners
	g.rect(0, musVisY-2, W, fxVH*2+4, hex(0x0a0814))
	v.fx.blit(a.g.s.Px, musVisX, musVisY, st.mode)
	acc := fxAccent(st.mode)
	g.frame(1, musVisY-2, W-2, fxVH*2+4, hex(0x3e3868))
	for _, c := range [][4]int{{1, musVisY - 2, 1, 1}, {W - 2, musVisY - 2, -1, 1}, {1, musVisY + fxVH*2 + 1, 1, -1}, {W - 2, musVisY + fxVH*2 + 1, -1, -1}} {
		for i := 0; i < 5; i++ {
			g.rect(c[0]+i*c[2], c[1], 1, 1, acc)
			g.rect(c[0], c[1]+i*c[3], 1, 1, acc)
		}
	}
	label := fmt.Sprintf("%s %d/%d", fxModes[st.mode], st.mode+1, len(fxModes))
	lw := textW(label, 1, false)
	g.rect(8, musVisY-2, lw+6, 11, hex(0x0a0814))
	g.text(11, musVisY-3, label, acc, 1, false)
	hint := "F/X VOLUME   Z/C SEEK"
	hw := textW(hint, 1, false)
	g.rect(W-hw-14, musVisY+fxVH*2-8, hw+6, 11, hex(0x0a0814))
	g.text(W-hw-11, musVisY+fxVH*2-9, hint, hex(0x847eb0), 1, false)

	// the song
	y := musInfo
	g.dsDetails(2, y, W-4, 37)
	cx, cy := 6, y+4
	if c := st.meta.cover; c != nil {
		for j := 0; j < 15; j++ {
			for i := 0; i < 15; i++ {
				g.s.Rect(cx+i*2, cy+j*2-1, 2, 2, c[j*15+i])
			}
		}
	} else {
		g.rect(cx, cy-1, 30, 30, a.theme())
		g.artIcon(cx+15, cy+14, artMusic, 22, inkOn(a.theme()))
	}
	tx := 42
	title := "No songs here"
	if st.cur < len(st.titles) {
		title = musicASCII(st.titles[st.cur])
	}
	win := W - 8 - tx
	if tw := textW(title, 1, false); tw <= win {
		g.text(tx, y+3, title, cBlack, 1, false)
	} else { // a long title scrolls while it plays
		el := time.Since(st.changed).Seconds()
		span := float64(tw + 36)
		t := mod(el, 1.6+span/22)
		off := 0
		if st.playing && !st.paused && t > 1.6 {
			off = int((t - 1.6) * 22)
		}
		g.setClip(tx, y+2, win, 12)
		g.text(tx-off, y+3, title, cBlack, 1, false)
		if off > 0 {
			g.text(tx-off+tw+36, y+3, title, cBlack, 1, false)
		}
		g.noClip()
	}
	n := fmt.Sprintf("VOL %d   %02d/%02d", st.vol, min(st.cur+1, len(st.tracks)), len(st.tracks))
	who := musicASCII(st.meta.artist)
	if who == "" {
		who = "Unknown artist"
	}
	g.text(tx, y+14, fit(who, win-textW(n, 1, false)-8, 1, false), cBorder, 1, false)
	g.textR(W-7, y+14, n, a.theme().darker(0.2), 1, false)

	// time and progress
	py := y + 25
	pos, dur := st.pos, st.meta.dur
	g.text(tx, py, songTime(pos), cBlack, 1, false)
	g.textR(W-7, py, songTime(dur), cBorder, 1, false)
	bx := tx + 44
	bw := W - 7 - textW("0:00:00", 1, false) - 6 - bx
	cells := bw / 3
	frac := 0.0
	if dur > 0.1 {
		frac = max(0, min(pos/dur, 1))
	}
	on := int(frac*float64(cells) + 0.5)
	for i := 0; i < cells; i++ {
		c := cGridBG.darker(0.15)
		if i < on {
			c = mix(a.theme(), a.theme().darker(0.35), float64(i)/float64(max(cells, 1)))
		}
		g.rect(bx+i*3, py+3, 2, 5, c)
	}
	if dur > 0.1 {
		g.rect(bx+min(on, cells-1)*3, py+1, 2, 9, cBlack)
	}
	if st.paused {
		g.textC(W/2, musVisY+fxVH-4, "PAUSED", hex(0xffc054), 1, false)
	}
}

func mod(a, b float64) float64 {
	if b <= 0 {
		return 0
	}
	return a - b*float64(int(a/b))
}

// ---- lists ----

const musRows = 9

// moveSel moves a list selection by d, wrapping on a fresh press and stopping on a repeat.
func moveSel(sel, n, d int, repeat bool) int {
	next := sel + d
	if next < 0 || next >= n {
		if repeat {
			return sel
		}
		return (next%n + n) % n
	}
	return next
}

// drawMusicList draws rows (label, folder?) with the selection and the playing song marked.
func drawMusicList(a *App, title string, labels []string, dirs []bool, sel, playing int, top *int, empty string) {
	g := a.g
	pageTitle(a, title, artMusic)
	x, y, w := 4, 19, W-8
	g.dsDetails(x, y, w, musRows*12+5)
	n := len(labels)
	if sel < *top {
		*top = sel
	}
	if sel >= *top+musRows {
		*top = sel - musRows + 1
	}
	*top = max(0, min(*top, n-musRows))
	if n == 0 {
		g.text(x+10, y+6, empty, cBorder, 1, false)
	}
	st := a.musicPlayer().state()
	for i := 0; i < musRows && *top+i < n; i++ {
		idx := *top + i
		ry := y + 3 + i*12
		ink := cBlack
		if idx == sel {
			g.rect(x+3, ry, w-10, 12, a.theme())
			ink = inkOn(a.theme())
		}
		lx, tx := x+8, x+22
		if dirs == nil { // the queue: the playing song's bars, the number, the title
			if idx == playing {
				frame := int(time.Since(st.changed).Milliseconds() / 160)
				for b := 0; b < 3; b++ {
					h := 2
					if st.playing && !st.paused {
						h = 2 + (frame+b*2)%5
					}
					g.rect(lx-3+b*3, ry+10-h, 2, h, ink)
				}
			}
			g.text(lx+8, ry+1, fmt.Sprintf("%02d", idx+1), mix(ink, cBorder, 0.5), 1, false)
			tx = lx + 26
		} else if dirs[idx] {
			g.glyph(lx+5, ry+6, artFolder, 1, mix(ink, hex(0xc08000), 0.6))
		}
		g.text(tx, ry+1, fit(labels[idx], x+w-12-tx, 1, false), ink, 1, false)
	}
	if n > musRows { // scroll bar
		h := musRows * 12
		th := max(6, h*musRows/n)
		ty := y + 3 + (h-th)**top/(n-musRows)
		g.rect(x+w-5, y+3, 2, h, cGridBG)
		g.rect(x+w-5, ty, 2, th, a.theme())
	}
}

// MusicQueue is the songs being played, in order.
type MusicQueue struct{ sel, top int }

func (v *MusicQueue) Animating(a *App) bool { return a.musicPlayer().active() }

func (v *MusicQueue) Key(a *App, k cp0.Key) {
	if musicKeys(a, k, "queue") {
		return
	}
	st := a.musicPlayer().state()
	n := len(st.tracks)
	if n == 0 {
		return
	}
	switch k.Name {
	case "UP":
		v.sel = moveSel(v.sel, n, -1, k.Repeat)
		a.snd.Play("move")
	case "DOWN":
		v.sel = moveSel(v.sel, n, 1, k.Repeat)
		a.snd.Play("move")
	case "PGUP":
		v.sel = max(0, v.sel-musRows)
	case "PGDN":
		v.sel = min(n-1, v.sel+musRows)
	case "ENTER":
		if !k.Repeat {
			a.musicPlayer().Play(v.sel)
			a.replace(&MusicNow{fx: newMusicFx(), last: time.Now()})
		}
	}
}

func (v *MusicQueue) Bar(a *App) []BarItem {
	return musicBar(a, "NOW", "FOLDERS", artMusic, artFolder)
}

func (v *MusicQueue) Draw(a *App) {
	st := a.musicPlayer().state()
	labels := make([]string, len(st.titles))
	for i, t := range st.titles {
		labels[i] = musicASCII(t)
	}
	v.sel = min(v.sel, max(0, len(labels)-1))
	drawMusicList(a, fmt.Sprintf("Queue %d/%d", min(v.sel+1, len(labels)), len(labels)), labels, nil, v.sel, st.cur, &v.top, "No songs")
}

// MusicFiles finds songs: folders and the songs in them.
type MusicFiles struct {
	dir      string
	names    []string // real names
	dirs     []bool
	sel, top int
}

func newMusicFiles(a *App, dir string) *MusicFiles {
	v := &MusicFiles{dir: dir}
	v.load()
	return v
}

func (v *MusicFiles) load() {
	v.names, v.dirs = nil, nil
	if parent := filepath.Dir(v.dir); parent != v.dir {
		v.names, v.dirs = append(v.names, ".."), append(v.dirs, true)
	}
	var ds, fs []string
	ents, _ := os.ReadDir(v.dir)
	for _, e := range ents {
		if strings.HasPrefix(e.Name(), ".") {
			continue
		}
		if e.IsDir() {
			ds = append(ds, e.Name())
		} else if isAudio(e.Name()) {
			fs = append(fs, e.Name())
		}
	}
	for _, d := range ds {
		v.names, v.dirs = append(v.names, d), append(v.dirs, true)
	}
	for _, f := range fs {
		v.names, v.dirs = append(v.names, f), append(v.dirs, false)
	}
	v.sel, v.top = 0, 0
}

func (v *MusicFiles) Animating(a *App) bool { return false }

func (v *MusicFiles) Key(a *App, k cp0.Key) {
	if musicKeys(a, k, "files") {
		return
	}
	n := len(v.names)
	switch {
	case k.Name == "UP" && n > 0:
		v.sel = moveSel(v.sel, n, -1, k.Repeat)
		a.snd.Play("move")
	case k.Name == "DOWN" && n > 0:
		v.sel = moveSel(v.sel, n, 1, k.Repeat)
		a.snd.Play("move")
	case k.Name == "PGUP":
		v.sel = max(0, v.sel-musRows)
	case k.Name == "PGDN" && n > 0:
		v.sel = min(n-1, v.sel+musRows)
	case (k.Name == "ENTER" || k.Name == "RIGHT") && !k.Repeat && n > 0:
		v.open(a)
	case k.Name == "LEFT" && !k.Repeat:
		if parent := filepath.Dir(v.dir); parent != v.dir {
			from := filepath.Base(v.dir)
			v.dir = parent
			v.load()
			for i, nm := range v.names {
				if nm == from {
					v.sel = i
				}
			}
			a.snd.Play("back")
		}
	case (k.Ch == 'a' || k.Ch == 'A') && !k.Repeat:
		songs := scanMusic(v.dir)
		if len(songs) == 0 {
			a.notify("No songs in this folder", true)
			return
		}
		a.musicPlayer().PlayList(songs, 0, v.dir)
		a.notify(fmt.Sprintf("Playing all %d songs", len(songs)), false)
		a.replace(&MusicNow{fx: newMusicFx(), last: time.Now()})
	}
}

func (v *MusicFiles) open(a *App) {
	name, folder := v.names[v.sel], v.dirs[v.sel]
	if folder {
		next := filepath.Join(v.dir, name)
		if name == ".." {
			next = filepath.Dir(v.dir)
		}
		if isDir(next) {
			v.dir = next
			v.load()
			a.snd.Play("select")
		}
		return
	}
	files := audioFiles(v.dir)
	idx := 0
	for i, f := range files {
		if filepath.Base(f) == name {
			idx = i
		}
	}
	a.musicPlayer().PlayList(files, idx, v.dir)
	a.replace(&MusicNow{fx: newMusicFx(), last: time.Now()})
}

func (v *MusicFiles) Bar(a *App) []BarItem {
	return musicBar(a, "NOW", "QUEUE", artMusic, artQueue)
}

func (v *MusicFiles) Draw(a *App) {
	labels := make([]string, len(v.names))
	for i, nm := range v.names {
		labels[i] = musicASCII(nm)
		if v.dirs[i] && nm != ".." {
			labels[i] += "/"
		}
	}
	path := musicASCII(tilde(v.dir))
	if r := []rune(path); len(r) > 26 {
		path = ".." + string(r[len(r)-24:])
	}
	drawMusicList(a, path, labels, v.dirs, v.sel, -1, &v.top, "Nothing here (A plays every song below this folder)")
}
