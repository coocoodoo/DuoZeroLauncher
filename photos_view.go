package main

// Photos, built in: the folder grid and the viewer.
//
// Grid:   arrows move, 4 OPEN (or Enter), 5 SLIDESHOW, 6 UP a folder, 7 INFO, 8 BACK (or Esc).
// Viewer: 4 PREV, 5 ZOOM (fit, 2x, 4x), 6 NEXT, 7 ROTATE, 8 INFO; Z/C (left/right) step through
//         the photos, or pan with all four arrows when zoomed; Enter hides the key bar (full
//         screen); Space starts or stops the slideshow; Del (or Backspace) moves the photo to the
//         trash after asking; Esc zooms out, then goes back.

import (
	"dslauncher/cp0"
	"fmt"
	"image"
	"path/filepath"
	"time"
)

var (
	artPhoto = []string{
		"...........",
		"###########",
		"#.........#",
		"#.......#.#",
		"#.........#",
		"#...#.....#",
		"#..###..#.#",
		"#.#####.###",
		"###########",
		"...........",
		"...........",
	}
	artZoom = []string{
		"...........",
		"..####.....",
		".#....#....",
		"#..##..#...",
		"#.####.#...",
		"#..##..#...",
		".#....#....",
		"..#####....",
		"......##...",
		".......##..",
		"........##.",
	}
	artRotate = []string{
		"...........",
		"...#####.#.",
		"..#.....##.",
		".#.....###.",
		".#.........",
		".#.........",
		".#.......#.",
		"..#.....#..",
		"...#####...",
		"...........",
		"...........",
	}
	artUp = []string{
		"...........",
		".....#.....",
		"....###....",
		"...#####...",
		"..#######..",
		".....#.....",
		".....#.....",
		".....#.....",
		".....#####.",
		"...........",
		"...........",
	}
)

// photoThumbs keeps the grid's thumbnails (nil: it couldn't be read); UI thread only.
var photoThumbs = map[string][]uint16{}

// openPhotos opens Photos on the folder last used.
func (a *App) openPhotos() { a.push(newPhotoGrid(photosStart())) }

// ---- the grid ----

const (
	gridCols = 5
	gridRows = 2
	gridX    = 2
	gridY    = 21
	gridGap  = 4
)

type PhotoGrid struct {
	dir      string
	items    []photoItem
	sel, top int // top: the first row shown
	loading  bool
	stale    bool // the viewer deleted something: read the folder again
}

func newPhotoGrid(dir string) *PhotoGrid {
	v := &PhotoGrid{dir: dir}
	v.load("")
	return v
}

// load reads the folder; select picks an item by path (the folder we came up from, say).
func (v *PhotoGrid) load(selectPath string) {
	v.items = photoFolder(v.dir)
	v.sel, v.top = 0, 0
	for i, it := range v.items {
		if it.path == selectPath {
			v.sel = i
		}
	}
	if v.sel == 0 && len(v.items) > 1 && v.items[0].name == ".." && selectPath == "" {
		v.sel = 1 // start on the first real thing, not ".."
	}
	savePhotosDir(v.dir)
}

func (v *PhotoGrid) pictures() ([]photoItem, int) {
	var out []photoItem
	at := 0
	for i, it := range v.items {
		if !it.dir {
			if i == v.sel {
				at = len(out)
			}
			out = append(out, it)
		}
	}
	return out, at
}

func (v *PhotoGrid) Animating(a *App) bool { return v.loading }

// fetch loads the thumbnails on screen (and the next row), one at a time off the UI thread.
func (v *PhotoGrid) fetch(a *App) {
	if v.loading {
		return
	}
	var want []string
	first := v.top * gridCols
	for i := first; i < first+gridCols*(gridRows+1) && i < len(v.items); i++ {
		it := v.items[i]
		if _, ok := photoThumbs[it.path]; !it.dir && !ok {
			want = append(want, it.path)
		}
	}
	if len(want) == 0 {
		return
	}
	v.loading = true
	go func() {
		for _, p := range want {
			px, err := makeThumb(p)
			if err != nil {
				px = nil
			}
			a.work <- func() { photoThumbs[p] = px; a.dirty = true }
		}
		a.work <- func() { v.loading = false; a.dirty = true }
	}()
}

func (v *PhotoGrid) Key(a *App, k cp0.Key) {
	n := len(v.items)
	move := func(d int) {
		if n == 0 {
			return
		}
		to := v.sel + d
		if to < 0 || to >= n {
			if k.Repeat || d == gridCols || d == -gridCols {
				to = max(0, min(n-1, to))
			} else {
				to = (to + n) % n
			}
		}
		if to != v.sel {
			v.sel = to
			a.snd.Play("move")
		}
	}
	switch {
	case k.Back() || k.Slot() == 4:
		a.snd.Play("back")
		a.pop()
	case k.Name == "LEFT":
		move(-1)
	case k.Name == "RIGHT":
		move(1)
	case k.Name == "UP":
		move(-gridCols)
	case k.Name == "DOWN":
		move(gridCols)
	case (k.Name == "ENTER" || k.Slot() == 0) && n > 0:
		it := v.items[v.sel]
		if it.dir {
			from := v.dir
			v.dir = it.path
			if it.name == ".." {
				v.load(from)
			} else {
				v.load("")
			}
			a.snd.Play("select")
			return
		}
		pics, at := v.pictures()
		a.snd.Play("open")
		a.push(newPhotoView(v, pics, at, false))
	case k.Slot() == 1:
		pics, at := v.pictures()
		if len(pics) == 0 {
			a.notify("No photos in this folder", true)
			return
		}
		if v.items[v.sel].dir {
			at = 0
		}
		a.snd.Play("open")
		a.push(newPhotoView(v, pics, at, true))
	case k.Slot() == 2:
		if parent := filepath.Dir(v.dir); parent != v.dir {
			from := v.dir
			v.dir = parent
			v.load(from)
			a.snd.Play("back")
		}
	case k.Slot() == 3 && n > 0:
		it := v.items[v.sel]
		if it.dir {
			count := 0
			for _, x := range photoFolder(it.path) {
				if !x.dir {
					count++
				}
			}
			alert(a, "FOLDER", fmt.Sprintf("%s: %d photo%s. %s", musicASCII(it.name), count, map[bool]string{true: "", false: "s"}[count == 1], tilde(it.path)))
			return
		}
		pics, at := v.pictures()
		a.push(&PhotoView{grid: v, items: pics, idx: at, info: true, infoOnly: true, cache: map[int]*photo{}, loadFor: -1})
	}
}

func (v *PhotoGrid) Bar(a *App) []BarItem {
	return []BarItem{{"OPEN", artOpen, len(v.items) == 0}, {"SLIDESHOW", artPlay, false}, {"UP", artUp, filepath.Dir(v.dir) == v.dir},
		{"INFO", artInfo, len(v.items) == 0}, {"BACK", artBack, false}}
}

func (v *PhotoGrid) Draw(a *App) {
	if v.stale {
		v.stale = false
		sel := ""
		if v.sel < len(v.items) {
			sel = v.items[v.sel].path
		}
		v.load(sel)
	}
	g := a.g
	title := musicASCII(tilde(v.dir))
	if r := []rune(title); len(r) > 30 {
		title = ".." + string(r[len(r)-28:])
	}
	pageTitle(a, title, artPhoto)
	n := len(v.items)
	if v.sel/gridCols < v.top {
		v.top = v.sel / gridCols
	}
	if v.sel/gridCols >= v.top+gridRows {
		v.top = v.sel/gridCols - gridRows + 1
	}
	v.fetch(a)
	for r := 0; r < gridRows; r++ {
		for c := 0; c < gridCols; c++ {
			i := (v.top+r)*gridCols + c
			if i >= n {
				break
			}
			it := v.items[i]
			x, y := gridX+c*(thumbW+gridGap), gridY+r*(thumbH+gridGap)
			sel := i == v.sel
			if sel {
				g.softShadow(x, y, thumbW, thumbH, 3, 0.35, cBlack)
			}
			switch {
			case it.dir:
				base := cButton
				if sel {
					base = a.theme()
				}
				g.dsButton(x, y, thumbW, thumbH, base, false)
				art := artFolder
				label := musicASCII(it.name)
				if it.name == ".." {
					art, label = artUp, "Up"
				}
				ink := neutralInk(base, sel)
				g.artIcon(x+thumbW/2, y+15, art, 20, ink)
				g.textC(x+thumbW/2, y+thumbH-13, fit(label, thumbW-6, 1, false), ink, 1, false)
			default:
				px, ok := photoThumbs[it.path]
				switch {
				case ok && px != nil:
					for yy := 0; yy < thumbH; yy++ {
						copy(g.s.Px[(y+yy)*W+x:(y+yy)*W+x+thumbW], px[yy*thumbW:(yy+1)*thumbW])
					}
				case ok: // couldn't be read
					g.rect(x, y, thumbW, thumbH, hex(0x50505a))
					g.textC(x+thumbW/2, y+thumbH/2-4, "?", cWhite, 1, false)
				default:
					g.rect(x, y, thumbW, thumbH, cGridBG.darker(0.1))
					g.artIcon(x+thumbW/2, y+thumbH/2, artPhoto, 16, cBorder)
				}
				g.frame(x-1, y-1, thumbW+2, thumbH+2, cBorder)
			}
			if sel {
				g.frame(x-2, y-2, thumbW+4, thumbH+4, a.theme())
				g.frame(x-3, y-3, thumbW+6, thumbH+6, a.theme().darker(0.3))
			}
		}
	}
	// the caption: what's selected, and where in the folder
	cy := gridY + gridRows*(thumbH+gridGap) + 1
	if n == 0 {
		g.textC(W/2, 60, "No photos or folders here", cSub, 1, false)
		return
	}
	pics, at := v.pictures()
	it := v.items[v.sel]
	right := fmt.Sprintf("%d photo%s", len(pics), map[bool]string{true: "", false: "s"}[len(pics) == 1])
	if !it.dir {
		right = fmt.Sprintf("%d/%d", at+1, len(pics))
	}
	rw := textW(right, 1, false)
	g.text(5, cy, fit(musicASCII(it.name), W-rw-18, 1, false), cText, 1, false)
	g.textR(W-5, cy, right, cSub, 1, false)
}

// ---- the viewer ----

type PhotoView struct {
	grid    *PhotoGrid
	items   []photoItem
	idx     int
	ph      *photo
	err     string
	loadFor int // the index being loaded (-1: none)
	gen     int
	cache   map[int]*photo

	rot      int         // quarter turns the viewer added
	work     *image.RGBA // the photo turned by rot (or the current GIF frame)
	zoom     int         // 0 fit, 1 2x, 2 4x
	cx, cy   float64     // the middle of the view, in work's pixels
	full     bool        // key bar hidden
	info     bool
	infoOnly bool // opened for INFO from the grid: closing it goes back there
	slide    bool
	nextAt   time.Time
	frame    int
	frameAt  time.Time

	view     []uint16 // what's drawn in the photo area, ready to copy
	viewKey  string
	fadeFrom []uint16
	fadeAt   time.Time
}

func newPhotoView(grid *PhotoGrid, items []photoItem, idx int, slide bool) *PhotoView {
	v := &PhotoView{grid: grid, items: items, idx: idx, slide: slide, cache: map[int]*photo{}, loadFor: -1}
	if slide {
		v.full = true
		v.nextAt = time.Now().Add(slideEvery())
	}
	return v
}

func (v *PhotoView) areaH() int {
	if v.full {
		return H
	}
	return 132
}

// want makes sure photo i is loaded (or loading); the one after it is fetched next.
func (v *PhotoView) want(a *App, i int) {
	if i < 0 || i >= len(v.items) {
		return
	}
	if p := v.cache[i]; p != nil {
		if i == v.idx && v.ph != p {
			v.ph, v.err = p, ""
			v.setWork()
		}
		return
	}
	if v.loadFor >= 0 {
		return // one decode at a time: memory is tight
	}
	v.loadFor = i
	v.gen++
	gen, path := v.gen, v.items[i].path
	go func() {
		p, err := loadPhoto(path)
		a.work <- func() {
			if v.gen != gen { // the viewer moved on: a newer load owns loadFor
				return
			}
			v.loadFor = -1
			if err != nil {
				if i == v.idx {
					v.ph, v.err = nil, err.Error()
				}
				return
			}
			v.cache[i] = p
			for k := range v.cache { // keep this photo and its neighbours only
				if k < v.idx-1 || k > v.idx+1 {
					delete(v.cache, k)
				}
			}
			if i == v.idx {
				v.ph, v.err = p, ""
				v.setWork()
			}
			a.dirty = true
		}
	}()
}

// setWork makes the working picture: the photo (or GIF frame) with the viewer's turns.
func (v *PhotoView) setWork() {
	if v.ph == nil {
		v.work = nil
		return
	}
	m := v.ph.img
	if len(v.ph.frames) > 1 {
		m = v.ph.frames[v.frame%len(v.ph.frames)]
	}
	for i := 0; i < v.rot; i++ {
		m = rotate90(m)
	}
	v.work = m
	v.viewKey = ""
	if v.zoom == 0 {
		v.cx, v.cy = float64(m.Bounds().Dx())/2, float64(m.Bounds().Dy())/2
	}
}

func (v *PhotoView) show(a *App, i int) {
	if len(v.items) == 0 {
		return
	}
	i = (i%len(v.items) + len(v.items)) % len(v.items)
	if i == v.idx && v.ph != nil {
		return
	}
	v.fadeFrom = append(v.fadeFrom[:0], a.g.s.Px...)
	v.fadeAt = time.Now()
	v.idx, v.ph, v.err, v.rot, v.zoom, v.frame = i, nil, "", 0, 0, 0
	v.work, v.viewKey = nil, ""
	v.gen++ // a decode for another photo is no longer wanted
	v.loadFor = -1
	v.want(a, i)
}

func (v *PhotoView) Animating(a *App) bool {
	return v.loadFor >= 0 || v.slide || time.Since(v.fadeAt) < 300*time.Millisecond ||
		(v.ph != nil && len(v.ph.frames) > 1)
}

func (v *PhotoView) pan(dx, dy float64) {
	if v.work == nil || v.zoom == 0 {
		return
	}
	s := v.scale()
	step := float64(W) / 4 / s
	v.cx += dx * step
	v.cy += dy * step
	v.viewKey = ""
}

// scale is how many screen pixels one working pixel takes.
func (v *PhotoView) scale() float64 {
	w, h := v.work.Bounds().Dx(), v.work.Bounds().Dy()
	fw, _ := fitIn(w, h, W, v.areaH())
	s := float64(fw) / float64(w)
	if w < W && h < v.areaH() { // small pictures are enlarged by whole steps
		s = float64(max(1, min(W/w, v.areaH()/h)))
	}
	return s * float64(int(1)<<v.zoom)
}

func (v *PhotoView) Key(a *App, k cp0.Key) {
	if v.info {
		if k.Back() || k.Slot() == 4 || k.Name == "ENTER" {
			v.info = false
			a.snd.Play("back")
			if v.infoOnly {
				a.pop()
			}
		}
		return
	}
	if v.slide && !k.Repeat && k.Name != "" && k.Name != "SPACE" {
		v.slide = false // any key stops the slideshow (and still does its job)
		a.notify("Slideshow stopped", false)
	}
	switch {
	case k.Back():
		if v.zoom > 0 {
			v.zoom = 0
			v.setWork()
			return
		}
		a.snd.Play("back")
		a.pop()
	case k.Name == "SPACE" || k.Ch == ' ':
		v.slide = !v.slide
		v.nextAt = time.Now().Add(slideEvery())
		a.notify(map[bool]string{true: fmt.Sprintf("Slideshow: a photo every %d seconds", int(slideEvery().Seconds())), false: "Slideshow stopped"}[v.slide], false)
	case k.Name == "ENTER":
		v.full = !v.full
		v.viewKey = ""
		if v.zoom == 0 {
			v.setWork()
		}
	case k.Slot() == 0:
		v.show(a, v.idx-1)
	case k.Slot() == 2:
		v.show(a, v.idx+1)
	case k.Slot() == 1 && v.work != nil:
		v.zoom = (v.zoom + 1) % 3
		if v.zoom == 0 {
			v.setWork()
		}
		v.viewKey = ""
		a.snd.Play("tick")
	case k.Slot() == 3 && v.ph != nil:
		v.rot = (v.rot + 1) % 4
		v.zoom = 0
		v.setWork()
		a.snd.Play("tick")
	case k.Slot() == 4:
		v.info = true
		a.snd.Play("modal")
	case k.Name == "LEFT":
		if v.zoom > 0 {
			v.pan(-1, 0)
		} else if !k.Repeat {
			v.show(a, v.idx-1)
		}
	case k.Name == "RIGHT":
		if v.zoom > 0 {
			v.pan(1, 0)
		} else if !k.Repeat {
			v.show(a, v.idx+1)
		}
	case k.Name == "UP":
		v.pan(0, -1)
	case k.Name == "DOWN":
		v.pan(0, 1)
	case (k.Name == "DELETE" || k.Name == "BACKSPACE") && !k.Repeat && len(v.items) > 0:
		it := v.items[v.idx]
		confirm(a, "DELETE?", "Move "+musicASCII(it.name)+" to the trash?", func() {
			if err := moveToTrash(it.path); err != nil {
				alert(a, "NOT DELETED", err.Error())
				return
			}
			a.notify("Moved to the trash", false)
			delete(photoThumbs, it.path)
			if v.grid != nil {
				v.grid.stale = true
			}
			v.items = append(v.items[:v.idx], v.items[v.idx+1:]...)
			v.cache = map[int]*photo{}
			if len(v.items) == 0 {
				a.pop()
				return
			}
			v.ph = nil
			v.show(a, min(v.idx, len(v.items)-1))
			v.want(a, v.idx)
		})
	}
}

func (v *PhotoView) Bar(a *App) []BarItem {
	if v.full {
		return nil
	}
	none := v.work == nil
	return []BarItem{{"PREV", artPrev, len(v.items) < 2}, {"ZOOM", artZoom, none}, {"NEXT", artNext, len(v.items) < 2},
		{"ROTATE", artRotate, none}, {"INFO", artInfo, false}}
}

// render makes v.view: the photo fitted (smoothly scaled) or zoomed into the photo area.
func (v *PhotoView) render() {
	ah := v.areaH()
	key := fmt.Sprintf("%p %d %d %.1f %.1f %d", v.work, v.zoom, ah, v.cx, v.cy, v.rot)
	if key == v.viewKey && v.view != nil {
		return
	}
	v.viewKey = key
	if len(v.view) != W*ah {
		v.view = make([]uint16, W*ah)
	}
	clear(v.view)
	w, h := v.work.Bounds().Dx(), v.work.Bounds().Dy()
	if v.zoom == 0 {
		var m *image.RGBA
		fw, fh := fitIn(w, h, W, ah)
		if w < W && h < ah { // small: whole-step enlargement, sharp
			k := max(1, min(W/w, ah/h))
			fw, fh = w*k, h*k
			m = image.NewRGBA(image.Rect(0, 0, fw, fh))
			for y := 0; y < fh; y++ {
				for x := 0; x < fw; x++ {
					copy(m.Pix[(y*fw+x)*4:(y*fw+x)*4+4], v.work.Pix[((y/k)*w+x/k)*4:])
				}
			}
		} else {
			m = shrink(v.work, fw, fh)
		}
		fw, fh = m.Bounds().Dx(), m.Bounds().Dy() // shrink rounds its own way
		px := to565(m)
		ox, oy := (W-fw)/2, (ah-fh)/2
		for y := 0; y < fh; y++ {
			copy(v.view[(oy+y)*W+ox:(oy+y)*W+ox+fw], px[y*fw:(y+1)*fw])
		}
		return
	}
	s := v.scale()
	// keep the view inside the picture
	hw, hh := float64(W)/2/s, float64(ah)/2/s
	v.cx = max(min(v.cx, float64(w)-hw), hw)
	v.cy = max(min(v.cy, float64(h)-hh), hh)
	if float64(w) < 2*hw {
		v.cx = float64(w) / 2
	}
	if float64(h) < 2*hh {
		v.cy = float64(h) / 2
	}
	for sy := 0; sy < ah; sy++ {
		wy := int(v.cy + (float64(sy)-float64(ah)/2)/s)
		if wy < 0 || wy >= h {
			continue
		}
		for sx := 0; sx < W; sx++ {
			wx := int(v.cx + (float64(sx)-float64(W)/2)/s)
			if wx < 0 || wx >= w {
				continue
			}
			p := v.work.Pix[(wy*w+wx)*4:]
			v.view[sy*W+sx] = dither565(sx, sy, RGB{p[0], p[1], p[2]})
		}
	}
}

func (v *PhotoView) Draw(a *App) {
	g := a.g
	now := time.Now()
	if v.ph == nil && v.err == "" {
		v.want(a, v.idx)
	} else if v.loadFor < 0 && v.idx+1 < len(v.items) {
		v.want(a, v.idx+1) // the next one, ready before it's asked for
	}
	if v.slide {
		a.lastKey = now // a slideshow keeps the screen on
		if now.After(v.nextAt) && v.ph != nil {
			v.nextAt = now.Add(slideEvery())
			v.show(a, v.idx+1)
		}
	}
	if v.ph != nil && len(v.ph.frames) > 1 { // an animated GIF
		d := time.Duration(v.ph.delays[v.frame%len(v.ph.delays)]) * 10 * time.Millisecond
		if now.Sub(v.frameAt) >= d {
			v.frame = (v.frame + 1) % len(v.ph.frames)
			v.frameAt = now
			zoom, cx, cy := v.zoom, v.cx, v.cy
			v.setWork()
			v.zoom, v.cx, v.cy = zoom, cx, cy
		}
	}
	g.noClip()
	g.rect(0, 0, W, H, cBlack)
	ah := v.areaH()
	switch {
	case v.work != nil:
		v.render()
		copy(g.s.Px[:W*ah], v.view)
	case v.err != "":
		g.textC(W/2, ah/2-10, "Can't show this picture", cWhite, 1, false)
		g.textC(W/2, ah/2+4, fit(v.err, W-20, 1, false), hex(0xa0a0a0), 1, false)
	default:
		g.dsLoading(W/2-14, ah/2-14, a.t)
	}
	if f := now.Sub(v.fadeAt).Seconds(); f < 0.3 && len(v.fadeFrom) == len(g.s.Px) {
		t := 1 - f/0.3 // the old picture fading into the new
		for i := 0; i < W*ah; i++ {
			g.s.Px[i] = blend565(g.s.Px[i], v.fadeFrom[i], t)
		}
	}
	if !v.full && len(v.items) > 0 { // a strip with where we are
		label := fmt.Sprintf("%d/%d  %s", v.idx+1, len(v.items), musicASCII(v.items[v.idx].name))
		if v.zoom > 0 {
			label = fmt.Sprintf("%dx  ", 1<<v.zoom) + label
		}
		if v.slide {
			label = "> " + label
		}
		g.shade(0, 0, W, 12, cBlack, 0.55)
		g.text(4, 1, fit(label, W-8, 1, false), cWhite, 1, false)
	}
	if v.info {
		v.drawInfo(a)
	}
}

func blend565(a, b uint16, t float64) uint16 {
	k := int(t * 32)
	ch := func(sh, m int) uint16 {
		ca, cb := int(a>>sh)&m, int(b>>sh)&m
		return uint16((ca*(32-k)+cb*k)/32) << sh
	}
	return ch(11, 31) | ch(5, 63) | ch(0, 31)
}

func (v *PhotoView) drawInfo(a *App) {
	g := a.g
	if len(v.items) == 0 {
		return
	}
	it := v.items[v.idx]
	lines := []string{musicASCII(it.name)}
	if p := v.ph; p != nil {
		lines = append(lines, fmt.Sprintf("%d x %d pixels, %s", p.w, p.h, humanSize(p.size)))
		when := p.modified.Format("Jan 2, 2006  15:04")
		if t, err := time.Parse("2006:01:02 15:04:05", p.exif.taken); err == nil {
			when = "Taken " + t.Format("Jan 2, 2006  15:04")
		}
		lines = append(lines, when)
		if p.exif.camera != "" {
			lines = append(lines, musicASCII(p.exif.camera))
		}
		if len(p.frames) > 1 {
			lines = append(lines, fmt.Sprintf("Animated, %d frames", len(p.frames)))
		}
	} else if v.err != "" {
		lines = append(lines, v.err)
	} else {
		lines = append(lines, "Loading...")
	}
	lines = append(lines, tilde(filepath.Dir(it.path)))
	h := 22 + len(lines)*12
	x, y, w := 24, (132-h)/2, W-48
	g.shade(0, 0, W, 132, cBlack, 0.35)
	g.dsAlert(x, y, w, h)
	g.textC(W/2, y+7, "PHOTO", cPictoEnter, 1, false)
	for i, l := range lines {
		g.textC(W/2, y+20+i*12, fit(l, w-14, 1, false), cWhite, 1, false)
	}
}

// slideEvery is the slideshow's pace (Settings > Photos).
func slideEvery() time.Duration { return time.Duration(loadPhotosConf().slide) * time.Second }
