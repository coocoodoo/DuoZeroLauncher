package main

// Files, built in: the browser and its screens.
//
// Browser: arrows move; Enter or C opens (a folder, or the file with what suits it), Z or
// Backspace goes up; 4 OPEN, 5 MARK (or Space), 6 MENU (everything you can do), 7 PLACES,
// 8 BACK (Esc first clears the filter, then the marks). "/" filters this folder, a letter jumps
// to the next name starting with it, Ctrl+C / Ctrl+X / Ctrl+V copy, cut and paste, Ctrl+A marks
// everything, Del moves to the trash, "." shows or hides hidden files.

import (
	"dslauncher/cp0"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"
)

var artPlaces = []string{
	"...........",
	"....###....",
	"...#####...",
	"..##...##..",
	"..##...##..",
	"...#####...",
	"...#####...",
	"....###....",
	".....#.....",
	"...........",
	"...........",
}

// the little pictures in front of each name, and their colours
var kindArt = map[fileKind][]string{
	kDir:     {"###......", "#..#.....", "#########", "#.......#", "#.......#", "#.......#", "#########"},
	kUp:      {"....#....", "...###...", "..#####..", ".#.###.#.", "...###...", "...###...", "...###..."},
	kImage:   {"#########", "#.......#", "#.....#.#", "#..#....#", "#.###.#.#", "########.", "#########"},
	kAudio:   {"...#####.", "...#...#.", "...#...#.", "...#...#.", ".###.###.", "####.####", ".##...##."},
	kVideo:   {"#########", "#.#.#.#.#", "#########", "#...#...#", "#...##..#", "#########", "#.#.#.#.#"},
	kText:    {"######...", "#....##..", "#.####.#.", "#......#.", "#.####.#.", "#......#.", "########."},
	kCode:    {".........", "..#...#..", ".#.....#.", "#...#...#", ".#..#..#.", "..#.#.#..", "....#...."},
	kArchive: {"#########", "#...#...#", "#########", "#...#...#", "#...#...#", "#...#...#", "#########"},
	kExec:    {"#########", "#.......#", "#.#.....#", "#..#....#", "#.#..##.#", "#.......#", "#########"},
	kPS1:     {"..#####..", ".#.....#.", "#...#...#", "#..#.#..#", "#...#...#", ".#.....#.", "..#####.."},
	kDoc:     {"######...", "#....##..", "#.##...#.", "#......#.", "#.####.#.", "#......#.", "########."},
	kOther:   {"######...", "#....##..", "#......#.", "#......#.", "#......#.", "#......#.", "########."},
}

var kindInk = map[fileKind]RGB{
	kDir: {216, 160, 32}, kUp: {80, 80, 80}, kImage: {40, 150, 80}, kAudio: {128, 80, 192}, kVideo: {200, 64, 64},
	kText: {104, 104, 104}, kCode: {48, 112, 208}, kArchive: {160, 106, 48}, kExec: {30, 30, 30}, kPS1: {32, 150, 170},
	kDoc: {208, 112, 32}, kOther: {128, 128, 128},
}

var kindNames = map[fileKind]string{
	kDir: "Folder", kImage: "Picture", kAudio: "Sound", kVideo: "Video", kText: "Text", kCode: "Program source",
	kArchive: "Archive", kExec: "Program", kPS1: "PS1 game", kDoc: "Document", kOther: "File",
}

// filesClip is what Copy or Cut picked up, waiting for Paste.
var filesClip struct {
	paths []string
	cut   bool
}

var filesApp = &Entry{Key: "builtin:files", Name: "Files", Builtin: "files"}

func (a *App) openFiles() {
	c := loadFilesConf()
	dir := c.dir
	if !isDir(dir) {
		dir = homeDir()
	}
	a.push(newFileBrowser(dir))
}

// ---- a pop-up menu ----

type menuItem struct {
	label, value string
	off          bool
	do           func(a *App)
}

type PopMenu struct {
	title    string
	items    []menuItem
	sel, top int
}

func popMenu(a *App, title string, items []menuItem) {
	a.snd.Play("modal")
	m := &PopMenu{title: title, items: items}
	for m.sel < len(items)-1 && items[m.sel].off {
		m.sel++
	}
	a.push(m)
}

const menuRows = 8

func (m *PopMenu) Key(a *App, k cp0.Key) {
	n := len(m.items)
	switch {
	case k.Back() || k.Slot() == 4:
		a.snd.Play("back")
		a.pop()
	case k.Name == "UP" && n > 0:
		m.sel = (m.sel + n - 1) % n
		a.snd.Play("move")
	case k.Name == "DOWN" && n > 0:
		m.sel = (m.sel + 1) % n
		a.snd.Play("move")
	case (k.Name == "ENTER" || k.Name == "RIGHT" || k.Slot() == 0) && n > 0 && !k.Repeat:
		it := m.items[m.sel]
		if it.off || it.do == nil {
			a.snd.Play("error")
			return
		}
		a.snd.Play("select")
		a.pop()
		it.do(a)
	}
}

func (m *PopMenu) Bar(a *App) []BarItem {
	return []BarItem{{"CHOOSE", artOK, false}, {}, {}, {}, {"BACK", artBack, false}}
}

func (m *PopMenu) Draw(a *App) {
	if len(a.stack) >= 2 {
		a.stack[len(a.stack)-2].Draw(a)
	}
	g := a.g
	g.noClip()
	g.shade(0, 0, W, 132, cBlack, 0.4)
	rows := min(menuRows, len(m.items))
	if m.sel < m.top {
		m.top = m.sel
	}
	if m.sel >= m.top+rows {
		m.top = m.sel - rows + 1
	}
	w, h := 210, 18+rows*12+4
	x, y := (W-w)/2, max(16, (132-h)/2)
	g.dsAlert(x, y, w, h)
	g.textC(W/2, y+5, m.title, cPictoEnter, 1, false)
	for i := 0; i < rows; i++ {
		it := m.items[m.top+i]
		ry := y + 18 + i*12
		ink := cWhite
		if it.off {
			ink = hex(0x707070)
		}
		if m.top+i == m.sel {
			g.rect(x+4, ry-1, w-8, 12, a.theme())
			ink = inkOn(a.theme())
			if it.off {
				ink = mix(ink, a.theme(), 0.5)
			}
		}
		g.text(x+10, ry+1, fit(it.label, w-80, 1, false), ink, 1, false)
		if it.value != "" {
			g.textR(x+w-10, ry+1, fit(it.value, 66, 1, false), ink, 1, false)
		}
	}
	if len(m.items) > rows {
		th := max(6, rows*12*rows/len(m.items))
		ty := y + 18 + (rows*12-th)*m.top/(len(m.items)-rows)
		g.rect(x+w-4, ty, 2, th, a.theme())
	}
}

// ---- the browser ----

const (
	fbRows = 8
	fbY    = 20
	fbRowH = 12
)

type FileBrowser struct {
	dir      string
	entries  []fileEntry
	sel, top int
	marks    map[string]bool
	filter   string
	conf     filesConf
	err      string
	results  []fileEntry // a search's results (nil: the folder)
	query    string
	free     int64
	stale    bool
}

func newFileBrowser(dir string) *FileBrowser {
	v := &FileBrowser{dir: dir, marks: map[string]bool{}, conf: loadFilesConf()}
	v.reload("")
	return v
}

// reload reads the folder again, keeping the selection on selectPath (or the same name).
func (v *FileBrowser) reload(selectPath string) {
	if selectPath == "" {
		if c := v.cur(); c != nil {
			selectPath = c.path
		}
	}
	v.err = ""
	es, err := listDir(v.dir, v.conf.hidden, v.conf.sortBy)
	if err != nil {
		v.err = err.Error()
		if os.IsPermission(err) {
			v.err = "You can't open this folder"
		}
	}
	if parent := filepath.Dir(v.dir); parent != v.dir {
		es = append([]fileEntry{{name: "..", path: parent, dir: true, kind: kUp}}, es...)
	}
	v.entries = es
	v.sel = 0
	for i, e := range v.shown() {
		if e.path == selectPath {
			v.sel = i
		}
	}
	if v.sel == 0 && selectPath == "" && len(v.shown()) > 1 && v.shown()[0].kind == kUp {
		v.sel = 1
	}
	for p := range v.marks {
		if !exists(p) {
			delete(v.marks, p)
		}
	}
	v.free, _ = diskSpace(v.dir)
	v.conf.dir = v.dir
	v.conf.save()
}

func (v *FileBrowser) go_(a *App, dir string) {
	from := v.dir
	v.dir, v.filter, v.results = dir, "", nil
	v.marks = map[string]bool{}
	sel := ""
	if filepath.Dir(from) == dir {
		sel = from // going up: land on the folder we came from
	}
	v.top = 0
	v.reload(sel)
	if sel == "" && len(v.shown()) > 1 && v.shown()[0].kind == kUp {
		v.sel = 1
	}
}

func (v *FileBrowser) shown() []fileEntry {
	src := v.entries
	if v.results != nil {
		src = v.results
	}
	if v.filter == "" {
		return src
	}
	q := strings.ToLower(v.filter)
	var out []fileEntry
	for _, e := range src {
		if e.kind == kUp || strings.Contains(strings.ToLower(e.name), q) {
			out = append(out, e)
		}
	}
	return out
}

func (v *FileBrowser) cur() *fileEntry {
	s := v.shown()
	if v.sel < 0 || v.sel >= len(s) {
		return nil
	}
	return &s[v.sel]
}

// targets are the marked files (in list order), or the one selected.
func (v *FileBrowser) targets() []string {
	var out []string
	for _, e := range v.shown() {
		if v.marks[e.path] {
			out = append(out, e.path)
		}
	}
	if len(out) == 0 {
		if c := v.cur(); c != nil && c.kind != kUp {
			out = []string{c.path}
		}
	}
	return out
}

func (v *FileBrowser) Animating(a *App) bool { return false }

func (v *FileBrowser) Key(a *App, k cp0.Key) {
	s := v.shown()
	n := len(s)
	move := func(d int) {
		if n == 0 {
			return
		}
		to := v.sel + d
		switch {
		case to < 0 && (k.Repeat || d < -1):
			to = 0
		case to >= n && (k.Repeat || d > 1):
			to = n - 1
		default:
			to = (to + n) % n
		}
		if to != v.sel {
			v.sel = to
			a.snd.Play("move")
		}
	}
	switch {
	case k.Back() || k.Slot() == 4:
		switch {
		case v.filter != "":
			v.filter = ""
			v.sel = 0
		case len(v.marks) > 0:
			v.marks = map[string]bool{}
			a.notify("Marks cleared", false)
		case v.results != nil:
			v.results, v.query = nil, ""
			v.reload("")
		default:
			a.snd.Play("back")
			a.pop()
		}
	case k.Name == "UP":
		move(-1)
	case k.Name == "DOWN":
		move(1)
	case k.Name == "PGUP":
		move(-fbRows)
	case k.Name == "PGDN":
		move(fbRows)
	case (k.Name == "ENTER" || k.Name == "RIGHT" || k.Slot() == 0) && !k.Repeat:
		if c := v.cur(); c != nil {
			v.open(a, *c)
		}
	case (k.Name == "LEFT" || k.Name == "BACKSPACE") && !k.Repeat:
		if v.results != nil {
			v.results, v.query = nil, ""
			v.reload("")
			return
		}
		if parent := filepath.Dir(v.dir); parent != v.dir {
			v.go_(a, parent)
			a.snd.Play("back")
		}
	case (k.Slot() == 1 || k.Name == "SPACE" || k.Ch == ' ') && !k.Repeat:
		if c := v.cur(); c != nil && c.kind != kUp {
			if v.marks[c.path] {
				delete(v.marks, c.path)
			} else {
				v.marks[c.path] = true
			}
			a.snd.Play("tick")
			move(1)
		}
	case k.Slot() == 2:
		v.menu(a)
	case k.Slot() == 3:
		v.places(a)
	case k.Ctrl == 'c':
		v.copy(a, false)
	case k.Ctrl == 'x':
		v.copy(a, true)
	case k.Ctrl == 'v':
		v.paste(a)
	case k.Ctrl == 'a':
		for _, e := range s {
			if e.kind != kUp {
				v.marks[e.path] = true
			}
		}
		a.notify(fmt.Sprintf("%d marked", len(v.marks)), false)
	case k.Name == "DELETE":
		v.trash(a)
	case k.Ch == '/':
		a.push(newInput("FILTER", "Show names containing", v.filter, 40, false, func(q string) {
			v.filter = strings.TrimSpace(q)
			v.sel, v.top = 0, 0
		}))
	case k.Ch == '.':
		v.conf.hidden = !v.conf.hidden
		v.reload("")
		a.notify(map[bool]string{true: "Showing hidden files", false: "Hiding hidden files"}[v.conf.hidden], false)
	case unicode.IsLetter(k.Ch) || unicode.IsDigit(k.Ch):
		// jump to the next name starting with that letter
		r := unicode.ToLower(k.Ch)
		for i := 1; i <= n; i++ {
			e := s[(v.sel+i)%n]
			if first := []rune(strings.ToLower(e.name)); len(first) > 0 && first[0] == r {
				v.sel = (v.sel + i) % n
				a.snd.Play("move")
				break
			}
		}
	}
}

func (v *FileBrowser) Bar(a *App) []BarItem {
	c := v.cur()
	mark := "MARK"
	if c != nil && v.marks[c.path] {
		mark = "UNMARK"
	}
	return []BarItem{{"OPEN", artOpen, c == nil}, {mark, artOK, c == nil || c.kind == kUp}, {"MENU", artQueue, false},
		{"PLACES", artPlaces, false}, {"BACK", artBack, false}}
}

func shortDate(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	now := time.Now()
	switch {
	case t.Year() == now.Year() && t.YearDay() == now.YearDay():
		return t.Format("15:04")
	case t.Year() == now.Year():
		return t.Format("Jan 2")
	}
	return t.Format("2006")
}

func shortSize(n int64) string {
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.1fG", float64(n)/(1<<30))
	case n >= 10<<20:
		return fmt.Sprintf("%dM", n>>20)
	case n >= 1<<20:
		return fmt.Sprintf("%.1fM", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%dK", n>>10)
	}
	return fmt.Sprintf("%dB", n)
}

func (v *FileBrowser) Draw(a *App) {
	if v.stale {
		v.stale = false
		v.reload("")
	}
	g := a.g
	title := musicASCII(tilde(v.dir))
	if v.results != nil {
		title = "Found \"" + musicASCII(v.query) + "\""
	}
	if v.filter != "" {
		title += "  [" + musicASCII(v.filter) + "]"
	}
	if r := []rune(title); len(r) > 32 {
		title = ".." + string(r[len(r)-30:])
	}
	pageTitle(a, title, artFolder)
	s := v.shown()
	n := len(s)
	if v.sel >= n {
		v.sel = max(0, n-1)
	}
	if v.sel < v.top {
		v.top = v.sel
	}
	if v.sel >= v.top+fbRows {
		v.top = v.sel - fbRows + 1
	}
	x, w := 3, W-6
	g.dsDetails(x, fbY-2, w, fbRows*fbRowH+4)
	for i := 0; i < fbRows && v.top+i < n; i++ {
		e := s[v.top+i]
		y := fbY + i*fbRowH
		ink, sub := cText, cSub
		switch {
		case v.top+i == v.sel:
			g.rect(x+2, y, w-4, fbRowH, a.theme())
			ink = inkOn(a.theme())
			sub = mix(ink, a.theme(), 0.35)
		case v.marks[e.path]:
			g.rect(x+2, y, w-4, fbRowH, mix(curTheme.PaperEven, a.theme(), 0.35))
		}
		art := kindArt[e.kind]
		ic := kindInk[e.kind]
		if v.top+i == v.sel {
			ic = ink
		}
		if v.marks[e.path] { // marked: a tick instead of the kind
			tick := a.theme().darker(0.45)
			if curTheme.Dark {
				tick = a.theme().lighter(0.3)
			}
			if v.top+i == v.sel {
				tick = ink
			}
			g.glyph(x+10, y+6, []string{"......##", ".....##.", "##..##..", ".####...", "..##...."}, 1, tick)
		} else {
			g.glyph(x+10, y+6, art, 1, ic)
		}
		name := musicASCII(e.name)
		if e.results(v) {
			if rel, err := filepath.Rel(v.dir, e.path); err == nil {
				name = musicASCII(rel)
			}
		}
		if e.dir && e.kind != kUp {
			name += "/"
		}
		if e.link {
			name += " @"
		}
		right := shortDate(e.mod)
		if !e.dir {
			right = shortSize(e.size) + "  " + right
		}
		if e.kind == kUp {
			right = ""
		}
		rw := textW(right, 1, false)
		g.text(x+19, y+2, fit(name, w-30-rw, 1, false), ink, 1, false)
		g.textR(x+w-6, y+2, right, sub, 1, false)
	}
	if n > fbRows {
		th := max(6, fbRows*fbRowH*fbRows/n)
		ty := fbY + (fbRows*fbRowH-th)*v.top/(n-fbRows)
		g.rect(x+w-3, ty, 2, th, a.theme())
	}
	if v.err != "" {
		g.textC(W/2, fbY+30, v.err, cSub, 1, false)
	} else if n == 0 || (n == 1 && s[0].kind == kUp) {
		msg := "This folder is empty"
		if v.filter != "" || v.results != nil {
			msg = "Nothing matches"
		}
		g.textC(W/2, fbY+30, msg, cSub, 1, false)
	}
	// the footer: how many, marked, waiting to paste, free space
	fy := fbY + fbRows*fbRowH + 3
	items := n
	if n > 0 && s[0].kind == kUp {
		items--
	}
	left := fmt.Sprintf("%d item%s", items, map[bool]string{true: "", false: "s"}[items == 1])
	if len(v.marks) > 0 {
		left += fmt.Sprintf(", %d marked", len(v.marks))
	}
	right := ""
	if len(filesClip.paths) > 0 {
		right = fmt.Sprintf("Ctrl+V: paste %d", len(filesClip.paths))
	} else if v.free >= 0 {
		right = humanSize(v.free) + " free"
	}
	g.text(6, fy, left, cText, 1, false)
	g.textR(W-6, fy, right, cSub, 1, false)
}

func (e fileEntry) results(v *FileBrowser) bool { return v.results != nil }

// ---- doing things ----

// open opens a file with what suits it.
func (v *FileBrowser) open(a *App, e fileEntry) {
	switch {
	case e.dir:
		v.go_(a, e.path)
		a.snd.Play("select")
	case e.kind == kImage && isPhoto(e.name):
		openInPhotos(a, e.path)
	case e.kind == kAudio && isAudio(e.name):
		openInMusic(a, e.path)
	case e.kind == kPS1 && ps1Installed():
		openInPS1(a, e.path)
	case isArchive(e.name):
		confirm(a, "EXTRACT?", "Unpack "+musicASCII(e.name)+" into a new folder next to it?", func() { v.extract(a, e.path) })
	case e.kind == kExec || isScript(e.name):
		confirm(a, "RUN?", "Run "+musicASCII(e.name)+" in the terminal?", func() { runInTerminal(a, e.path) })
	case e.kind == kVideo:
		a.snd.Play("open")
		a.push(newVideoView(a, e.path))
	default:
		openText(a, e.path, false)
	}
}

func isScript(name string) bool {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".sh", ".py", ".bash":
		return true
	}
	return false
}

func ps1Run() string                          { return filepath.Join(homeDir(), "apps/ps1/run.sh") }
func ps1Installed() bool                      { return isExecutable(ps1Run()) }
func openText(a *App, p string, hexMode bool) { a.push(newTextFileView(p, hexMode)) }

func openInPhotos(a *App, path string) {
	var pics []photoItem
	at := 0
	for _, it := range photoFolder(filepath.Dir(path)) {
		if !it.dir {
			if it.path == path {
				at = len(pics)
			}
			pics = append(pics, it)
		}
	}
	a.snd.Play("open")
	a.push(newPhotoView(nil, pics, at, false))
}

func openInMusic(a *App, path string) {
	dir := filepath.Dir(path)
	songs := audioFiles(dir)
	at := 0
	for i, s := range songs {
		if s == path {
			at = i
		}
	}
	a.musicPlayer().PlayList(songs, at, dir)
	a.snd.Play("open")
	a.push(&MusicNow{fx: newMusicFx(), last: time.Now()})
}

func openInPS1(a *App, path string) {
	a.runApp(&Entry{Key: "zc-ps1.desktop", Name: "PS1"}, []string{ps1Run(), path}, nil)
}

func runInTerminal(a *App, path string) {
	argv := []string{path}
	switch strings.ToLower(filepath.Ext(path)) {
	case ".sh", ".bash":
		argv = []string{"sh", path}
	case ".py":
		argv = []string{"python3", path}
	}
	a.runTerminal(filesApp, filepath.Base(path), argv...)
}

func (v *FileBrowser) copy(a *App, cut bool) {
	t := v.targets()
	if len(t) == 0 {
		return
	}
	filesClip.paths, filesClip.cut = t, cut
	verb := map[bool]string{true: "Cut", false: "Copied"}[cut]
	a.notify(fmt.Sprintf("%s %d: open a folder, then Ctrl+V or MENU > Paste", verb, len(t)), false)
	v.marks = map[string]bool{}
}

// job runs a long file job with a progress bar; Esc stops it.
func job(a *App, msg string, fn func(progress func(done, total int64), stop func() bool) error, done func(error)) {
	last := time.Time{}
	progress := func(d, t int64) {
		if time.Since(last) < 100*time.Millisecond || t <= 0 {
			return
		}
		last = time.Now()
		a.setProgress(fmt.Sprintf("%s %s of %s", msg, humanSize(d), humanSize(t)), float64(d)/float64(t))
	}
	stop := func() bool { return a.busy() == "" } // Esc on the busy screen
	a.background(msg, func() error { return fn(progress, stop) }, done)
}

func (v *FileBrowser) paste(a *App) {
	if len(filesClip.paths) == 0 {
		a.notify("Nothing to paste: Copy or Cut something first", true)
		return
	}
	if v.results != nil {
		a.notify("Open a folder to paste into", true)
		return
	}
	paths, cut, dir := filesClip.paths, filesClip.cut, v.dir
	var made []string
	job(a, map[bool]string{true: "Moving...", false: "Copying..."}[cut], func(p func(int64, int64), stop func() bool) error {
		var err error
		made, err = pasteInto(paths, dir, cut, p, stop)
		return err
	}, func(err error) {
		if cut {
			filesClip.paths = nil
		}
		sel := ""
		if len(made) > 0 {
			sel = made[0]
		}
		v.reload(sel)
		if err != nil {
			alert(a, "NOT ALL DONE", err.Error())
			return
		}
		a.notify(fmt.Sprintf("%s %d here", map[bool]string{true: "Moved", false: "Pasted"}[cut], len(made)), false)
	})
	// a cancelled job (Esc) still has to show what got done
	go func() {
		for a.busy() != "" {
			time.Sleep(200 * time.Millisecond)
		}
		a.work <- func() { v.stale = true }
	}()
}

func (v *FileBrowser) trash(a *App) {
	t := v.targets()
	if len(t) == 0 {
		return
	}
	what := musicASCII(filepath.Base(t[0]))
	if len(t) > 1 {
		what = fmt.Sprintf("%d items", len(t))
	}
	confirm(a, "DELETE?", "Move "+what+" to the trash? (Places > Trash puts things back.)", func() {
		failed := 0
		var lastErr error
		for _, p := range t {
			if err := moveToTrash(p); err != nil {
				failed++
				lastErr = err
			}
		}
		v.marks = map[string]bool{}
		v.reload("")
		if failed > 0 {
			alert(a, "NOT ALL DELETED", fmt.Sprintf("%d couldn't go to the trash: %v", failed, lastErr))
			return
		}
		a.notify(fmt.Sprintf("Moved %d to the trash", len(t)), false)
	})
}

func (v *FileBrowser) rename(a *App, p string) {
	old := filepath.Base(p)
	a.push(newInput("RENAME", "New name for "+musicASCII(old), old, 120, false, func(name string) {
		name = strings.TrimSpace(name)
		if name == old {
			return
		}
		if err := validName(name); err != nil {
			alert(a, "NOT RENAMED", err.Error())
			return
		}
		dst := filepath.Join(filepath.Dir(p), name)
		if exists(dst) {
			alert(a, "NOT RENAMED", name+" already exists here")
			return
		}
		if err := os.Rename(p, dst); err != nil {
			alert(a, "NOT RENAMED", err.Error())
			return
		}
		v.reload(dst)
	}))
}

func (v *FileBrowser) create(a *App, folder bool) {
	title, def := "NEW FILE", "notes.txt"
	if folder {
		title, def = "NEW FOLDER", "New folder"
	}
	a.push(newInput(title, "Name", uniqueName(v.dir, def), 120, false, func(name string) {
		name = strings.TrimSpace(name)
		if err := validName(name); err != nil {
			alert(a, "NOT MADE", err.Error())
			return
		}
		p := filepath.Join(v.dir, name)
		var err error
		if folder {
			err = os.Mkdir(p, 0o755)
		} else {
			var f *os.File
			if f, err = os.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644); err == nil {
				f.Close()
			}
		}
		if err != nil {
			alert(a, "NOT MADE", err.Error())
			return
		}
		v.reload(p)
		if !folder {
			v.edit(a, p)
		}
	}))
}

// editable: small enough, and not something that's clearly not text.
func editable(e fileEntry) bool {
	if e.dir || e.size > edMaxSize {
		return false
	}
	switch e.kind {
	case kImage, kAudio, kVideo, kArchive, kPS1, kDoc:
		return false
	}
	return true
}

func (v *FileBrowser) edit(a *App, p string) {
	ed := newEditor(p)
	ed.onSaved = func() { v.stale = true }
	a.snd.Play("open")
	a.push(ed)
}

func (v *FileBrowser) compress(a *App) {
	t := v.targets()
	if len(t) == 0 {
		return
	}
	def := "Archive.zip"
	if len(t) == 1 {
		def = strings.TrimSuffix(filepath.Base(t[0]), filepath.Ext(t[0])) + ".zip"
	}
	dir := v.dir
	a.push(newInput("COMPRESS", "Zip file name", uniqueName(dir, def), 120, false, func(name string) {
		name = strings.TrimSpace(name)
		if !strings.HasSuffix(strings.ToLower(name), ".zip") {
			name += ".zip"
		}
		if err := validName(name); err != nil {
			alert(a, "NOT MADE", err.Error())
			return
		}
		out := filepath.Join(dir, uniqueName(dir, name))
		job(a, "Compressing...", func(p func(int64, int64), stop func() bool) error {
			return zipPaths(t, out, p, stop)
		}, func(err error) {
			v.marks = map[string]bool{}
			v.reload(out)
			if err != nil {
				alert(a, "NOT COMPRESSED", err.Error())
				return
			}
			a.notify("Made "+musicASCII(filepath.Base(out)), false)
		})
	}))
}

func (v *FileBrowser) extract(a *App, p string) {
	var dest string
	job(a, "Extracting...", func(pr func(int64, int64), stop func() bool) error {
		var err error
		dest, err = extractArchive(p, pr, stop)
		return err
	}, func(err error) {
		if err != nil {
			alert(a, "NOT EXTRACTED", err.Error())
			v.reload("")
			return
		}
		v.reload(dest)
		a.notify("Unpacked into "+musicASCII(filepath.Base(dest)), false)
	})
}

func (v *FileBrowser) search(a *App) {
	a.push(newInput("SEARCH", "Find names below "+musicASCII(filepath.Base(v.dir)), v.query, 60, false, func(q string) {
		q = strings.TrimSpace(q)
		if q == "" {
			return
		}
		var found []fileEntry
		root := v.dir
		a.background("Searching...", func() error {
			found = searchFiles(root, q, 500, func() bool { return a.busy() == "" })
			return nil
		}, func(error) {
			if len(found) == 0 {
				a.notify("Nothing found for \""+musicASCII(q)+"\"", true)
				return
			}
			v.results, v.query, v.filter = found, q, ""
			v.sel, v.top = 0, 0
			if len(found) >= 500 {
				a.notify("Showing the first 500", false)
			}
		})
	}))
}

// properties shows what a file or folder is; a folder's size is added up in the background.
func (v *FileBrowser) properties(a *App, e fileEntry) {
	fi, err := os.Lstat(e.path)
	if err != nil {
		alert(a, "PROPERTIES", err.Error())
		return
	}
	kind := kindNames[e.kind]
	if ext := strings.TrimPrefix(strings.ToUpper(filepath.Ext(e.name)), "."); ext != "" && !e.dir {
		kind += " (" + ext + ")"
	}
	size := fmt.Sprintf("%s (%d bytes)", humanSize(fi.Size()), fi.Size())
	if e.dir {
		size = "adding up..."
	}
	doc := &DocText{title: "PROPERTIES"}
	build := func() {
		doc.paras = []string{musicASCII(e.name), "", "Kind: " + kind, "Size: " + size,
			"Modified: " + fi.ModTime().Format("Jan 2, 2006  15:04"), "Permissions: " + fi.Mode().String()}
		if fi.Mode()&os.ModeSymlink != 0 {
			if t, err := os.Readlink(e.path); err == nil {
				doc.paras = append(doc.paras, "Link to: "+musicASCII(t))
			}
		}
		doc.paras = append(doc.paras, "Where: "+musicASCII(tilde(filepath.Dir(e.path))))
		if free, total := diskSpace(filepath.Dir(e.path)); free >= 0 {
			doc.paras = append(doc.paras, fmt.Sprintf("Drive: %s free of %s", humanSize(free), humanSize(total)))
		}
		doc.lines = nil
	}
	build()
	a.push(doc)
	if e.dir {
		go func() {
			s, files, dirs, done := treeSize(e.path, time.Now().Add(20*time.Second))
			a.work <- func() {
				size = fmt.Sprintf("%s in %d files, %d folders", humanSize(s), files, dirs)
				if !done {
					size = "more than " + size
				}
				build()
				a.dirty = true
			}
		}()
	}
}

// menu is everything you can do here.
func (v *FileBrowser) menu(a *App) {
	t := v.targets()
	c := v.cur()
	one := len(t) == 1
	isFile := c != nil && !c.dir
	var items []menuItem
	add := func(label, value string, off bool, do func(a *App)) {
		items = append(items, menuItem{label, value, off, do})
	}
	add("Open", "", c == nil, func(a *App) { v.open(a, *c) })
	add("Open with...", "", !isFile, func(a *App) { v.openWith(a, *c) })
	add("Edit", "", !isFile || !editable(*c), func(a *App) { v.edit(a, c.path) })
	add("Copy", countOf(t), len(t) == 0, func(a *App) { v.copy(a, false) })
	add("Cut", countOf(t), len(t) == 0, func(a *App) { v.copy(a, true) })
	add("Paste", countOf(filesClip.paths), len(filesClip.paths) == 0 || v.results != nil, func(a *App) { v.paste(a) })
	add("Rename", "", !one, func(a *App) { v.rename(a, t[0]) })
	add("Delete", countOf(t), len(t) == 0, func(a *App) { v.trash(a) })
	add("New folder", "", v.results != nil, func(a *App) { v.create(a, true) })
	add("New text file", "", v.results != nil, func(a *App) { v.create(a, false) })
	add("Compress to zip", countOf(t), len(t) == 0, func(a *App) { v.compress(a) })
	add("Extract here", "", !(isFile && isArchive(c.name)), func(a *App) { v.extract(a, c.path) })
	add("Send to a Cardputer", countOf(t), len(t) == 0, func(a *App) { sendFiles(a, t) })
	add("Receive files", "", false, func(a *App) { a.push(newReceiveFiles(a)) })
	add("Search below here", "", false, func(a *App) { v.search(a) })
	add("Filter this folder", v.filter, false, func(a *App) {
		a.push(newInput("FILTER", "Show names containing", v.filter, 40, false, func(q string) { v.filter, v.sel, v.top = strings.TrimSpace(q), 0, 0 }))
	})
	if len(v.marks) > 0 {
		add("Clear marks", countOf(t), false, func(a *App) { v.marks = map[string]bool{} })
	} else {
		add("Mark all", "", false, func(a *App) {
			for _, e := range v.shown() {
				if e.kind != kUp {
					v.marks[e.path] = true
				}
			}
		})
	}
	add("Sort by", sortNames[v.conf.sortBy], false, func(a *App) {
		v.conf.sortBy = (v.conf.sortBy + 1) % len(sortNames)
		v.reload("")
		a.notify("Sorted by "+strings.ToLower(sortNames[v.conf.sortBy]), false)
	})
	add("Hidden files", map[bool]string{true: "SHOWN", false: "HIDDEN"}[v.conf.hidden], false, func(a *App) {
		v.conf.hidden = !v.conf.hidden
		v.reload("")
	})
	marked := false
	for _, b := range v.conf.bookmarks {
		marked = marked || b == v.dir
	}
	if marked {
		add("Remove bookmark", "", false, func(a *App) {
			var keep []string
			for _, b := range v.conf.bookmarks {
				if b != v.dir {
					keep = append(keep, b)
				}
			}
			v.conf.bookmarks = keep
			v.conf.save()
			a.notify("Bookmark removed", false)
		})
	} else {
		add("Bookmark this folder", "", v.results != nil, func(a *App) {
			v.conf.bookmarks = append(v.conf.bookmarks, v.dir)
			v.conf.save()
			a.notify("Bookmarked: it's in PLACES", false)
		})
	}
	add("Properties", "", c == nil || c.kind == kUp, func(a *App) { v.properties(a, *c) })
	popMenu(a, "FILES", items)
}

func countOf(p []string) string {
	if len(p) <= 1 {
		return ""
	}
	return fmt.Sprintf("%d", len(p))
}

func (v *FileBrowser) openWith(a *App, e fileEntry) {
	items := []menuItem{
		{"Photos", "", !isPhoto(e.name), func(a *App) { openInPhotos(a, e.path) }},
		{"Music", "", !isAudio(e.name), func(a *App) { openInMusic(a, e.path) }},
		{"Video player", "", e.kind != kVideo && e.kind != kAudio, func(a *App) { a.push(newVideoView(a, e.path)) }},
		{"PS1", "", e.kind != kPS1 || !ps1Installed(), func(a *App) { openInPS1(a, e.path) }},
		{"Text viewer", "", false, func(a *App) { openText(a, e.path, false) }},
		{"Text editor", "", !editable(e), func(a *App) { v.edit(a, e.path) }},
		{"Hex viewer", "", false, func(a *App) { openText(a, e.path, true) }},
		{"Run in terminal", "", !(e.kind == kExec || isScript(e.name)), func(a *App) { runInTerminal(a, e.path) }},
		{"Extract here", "", !isArchive(e.name), func(a *App) { v.extract(a, e.path) }},
	}
	popMenu(a, "OPEN WITH", items)
}

// places are the folders worth jumping to: the usual ones that exist, bookmarks, drives, trash.
func (v *FileBrowser) places(a *App) {
	var items []menuItem
	goTo := func(p string) func(a *App) {
		return func(a *App) {
			v.go_(a, p)
			a.snd.Play("select")
		}
	}
	h := homeDir()
	for _, p := range []struct{ name, path string }{
		{"Home", h}, {"Pictures", filepath.Join(h, "Pictures")}, {"Screenshots", screenshotDir()},
		{"Music", filepath.Join(h, "Music")}, {"Downloads", filepath.Join(h, "Downloads")},
		{"Documents", filepath.Join(h, "Documents")}, {"PS1 games", filepath.Join(h, "PS1")}, {"Apps", filepath.Join(h, "apps")},
	} {
		if isDir(p.path) {
			items = append(items, menuItem{label: p.name, do: goTo(p.path)})
		}
	}
	for _, b := range v.conf.bookmarks {
		if isDir(b) {
			items = append(items, menuItem{label: musicASCII(filepath.Base(b)), value: "BOOKMARK", do: goTo(b)})
		}
	}
	for _, root := range []string{filepath.Join("/media", userName()), "/mnt"} {
		ents, _ := os.ReadDir(root)
		for _, e := range ents {
			if p := filepath.Join(root, e.Name()); e.IsDir() {
				items = append(items, menuItem{label: musicASCII(e.Name()), value: "DRIVE", do: goTo(p)})
			}
		}
	}
	items = append(items, menuItem{label: "Whole system", value: "/", do: goTo(string(filepath.Separator))})
	n := len(trashList())
	items = append(items, menuItem{label: "Trash", value: fmt.Sprintf("%d", n), do: func(a *App) {
		a.push(&TrashView{items: trashList(), back: v})
	}})
	popMenu(a, "PLACES", items)
}

// ---- the trash ----

type TrashView struct {
	items    []trashItem
	sel, top int
	back     *FileBrowser
}

func (v *TrashView) Key(a *App, k cp0.Key) {
	n := len(v.items)
	switch {
	case k.Back() || k.Slot() == 4:
		a.snd.Play("back")
		if v.back != nil {
			v.back.stale = true
		}
		a.pop()
	case k.Name == "UP" && n > 0:
		v.sel = (v.sel + n - 1) % n
		a.snd.Play("move")
	case k.Name == "DOWN" && n > 0:
		v.sel = (v.sel + 1) % n
		a.snd.Play("move")
	case (k.Slot() == 0 || k.Name == "ENTER") && n > 0 && !k.Repeat:
		it := v.items[v.sel]
		dst, err := it.restore()
		if err != nil {
			alert(a, "NOT RESTORED", err.Error())
			return
		}
		a.notify("Put back in "+musicASCII(tilde(filepath.Dir(dst))), false)
		v.items = trashList()
	case k.Slot() == 1 && n > 0:
		it := v.items[v.sel]
		confirm(a, "DELETE FOR GOOD?", musicASCII(it.name)+" will be gone for good. This can't be undone.", func() {
			if err := it.erase(); err != nil {
				alert(a, "NOT DELETED", err.Error())
			}
			v.items = trashList()
		})
	case k.Slot() == 2 && n > 0:
		confirm(a, "EMPTY THE TRASH?", fmt.Sprintf("All %d items will be gone for good. This can't be undone.", n), func() {
			for _, it := range v.items {
				it.erase()
			}
			v.items = trashList()
			a.notify("The trash is empty", false)
		})
	}
	v.sel = max(0, min(v.sel, len(v.items)-1))
}

func (v *TrashView) Bar(a *App) []BarItem {
	none := len(v.items) == 0
	return []BarItem{{"RESTORE", artBack, none}, {"DELETE", artTrash, none}, {"EMPTY", artNo, none}, {}, {"BACK", artBack, false}}
}

func (v *TrashView) Draw(a *App) {
	g := a.g
	pageTitle(a, "Trash", artTrash)
	n := len(v.items)
	if v.sel < v.top {
		v.top = v.sel
	}
	if v.sel >= v.top+fbRows {
		v.top = v.sel - fbRows + 1
	}
	x, w := 3, W-6
	g.dsDetails(x, fbY-2, w, fbRows*fbRowH+4)
	if n == 0 {
		g.textC(W/2, fbY+30, "The trash is empty", cSub, 1, false)
	}
	for i := 0; i < fbRows && v.top+i < n; i++ {
		it := v.items[v.top+i]
		y := fbY + i*fbRowH
		ink, sub := cText, cSub
		if v.top+i == v.sel {
			g.rect(x+2, y, w-4, fbRowH, a.theme())
			ink = inkOn(a.theme())
			sub = mix(ink, a.theme(), 0.35)
		}
		k := kindOf(it.name, 0)
		if it.dir {
			k = kDir
		}
		g.glyph(x+10, y+6, kindArt[k], 1, ink)
		from := ""
		if it.orig != "" {
			from = "from " + musicASCII(tilde(filepath.Dir(it.orig)))
		}
		rw := min(130, textW(from, 1, false))
		g.text(x+19, y+2, fit(musicASCII(it.name), w-34-rw, 1, false), ink, 1, false)
		g.textR(x+w-6, y+2, fit(from, 130, 1, false), sub, 1, false)
	}
	g.text(6, fbY+fbRows*fbRowH+3, fmt.Sprintf("%d item%s", n, map[bool]string{true: "", false: "s"}[n == 1]), cText, 1, false)
	if n > 0 && v.sel < n {
		g.textR(W-6, fbY+fbRows*fbRowH+3, "deleted "+shortDate(v.items[v.sel].deleted), cSub, 1, false)
	}
}

// ---- the text and hex viewer ----

const tvRows = 11

type TextFileView struct {
	path      string
	data      []byte
	lines     []string
	hex       bool
	wrap      bool
	truncated bool
	top, left int
	find      string
	hit       int // the line with the last match (-1 none)
	err       string
	cache     []tvRow // rows(), kept until wrap or hex changes
	cacheFor  string
}

func newTextFileView(path string, hexMode bool) *TextFileView {
	v := &TextFileView{path: path, hex: hexMode, hit: -1, wrap: true}
	data, binary, trunc, err := readPreview(path, 512<<10)
	if err != nil {
		v.err = err.Error()
		return v
	}
	v.data, v.truncated = data, trunc
	if binary {
		v.hex = true
	}
	text := strings.ReplaceAll(strings.ReplaceAll(string(data), "\r\n", "\n"), "\t", "    ")
	v.lines = strings.Split(text, "\n")
	return v
}

func (v *TextFileView) numW() int {
	if v.hex {
		return 0
	}
	return textW(fmt.Sprint(len(v.lines)), 1, false) + 6
}

func (v *TextFileView) cols() int { return (W - 10 - v.numW()) / 6 }

// rows are the lines as drawn: wrapped (each with its line number on the first part), or hex.
type tvRow struct {
	num  int // 0: a continuation
	text string
}

func (v *TextFileView) rows() []tvRow {
	key := fmt.Sprint(v.hex, v.wrap)
	if v.cacheFor != key {
		v.cache, v.cacheFor = v.buildRows(), key
	}
	return v.cache
}

func (v *TextFileView) buildRows() []tvRow {
	var out []tvRow
	if v.hex {
		for off := 0; off < len(v.data); off += 8 {
			end := min(off+8, len(v.data))
			var hx, asc strings.Builder
			for i := off; i < off+8; i++ {
				if i < end {
					fmt.Fprintf(&hx, "%02x ", v.data[i])
					c := v.data[i]
					if c < 0x20 || c > 0x7e {
						c = '.'
					}
					asc.WriteByte(c)
				} else {
					hx.WriteString("   ")
				}
			}
			out = append(out, tvRow{num: off/8 + 1, text: fmt.Sprintf("%06x  %s %s", off, hx.String(), asc.String())})
		}
		return out
	}
	cols := v.cols()
	for i, l := range v.lines {
		l = musicASCIIKeepSpaces(l)
		if !v.wrap || len(l) <= cols {
			out = append(out, tvRow{i + 1, l})
			continue
		}
		for j := 0; j < len(l); j += cols {
			n := 0
			if j == 0 {
				n = i + 1
			}
			out = append(out, tvRow{n, l[j:min(len(l), j+cols)]})
		}
	}
	return out
}

// musicASCIIKeepSpaces is musicASCII without folding the spaces (code keeps its indentation).
func musicASCIIKeepSpaces(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= ' ' && r <= '~':
			b.WriteRune(r)
		case r == 0x2018 || r == 0x2019:
			b.WriteByte('\'')
		case r == 0x201C || r == 0x201D:
			b.WriteByte('"')
		case r == 0x2013 || r == 0x2014:
			b.WriteByte('-')
		default:
			if r >= 0x80 {
				b.WriteByte('?')
			}
		}
	}
	return b.String()
}

func (v *TextFileView) findNext(a *App, from int) {
	if v.find == "" {
		return
	}
	rows := v.rows()
	q := strings.ToLower(v.find)
	for i := 1; i <= len(rows); i++ {
		r := (from + i) % len(rows)
		if strings.Contains(strings.ToLower(rows[r].text), q) {
			v.hit = r
			v.top = max(0, r-tvRows/3)
			a.snd.Play("tick")
			return
		}
	}
	v.hit = -1
	a.notify("\""+musicASCII(v.find)+"\" isn't in here", true)
}

func (v *TextFileView) Key(a *App, k cp0.Key) {
	rows := len(v.rows())
	last := max(0, rows-tvRows)
	switch {
	case k.Back() || k.Slot() == 4:
		a.snd.Play("back")
		a.pop()
	case k.Name == "UP":
		v.top = max(0, v.top-1)
	case k.Name == "DOWN":
		v.top = min(last, v.top+1)
	case k.Name == "PGUP":
		v.top = max(0, v.top-tvRows)
	case k.Name == "PGDN" || k.Name == "SPACE" || k.Ch == ' ':
		v.top = min(last, v.top+tvRows)
	case k.Name == "LEFT":
		if v.wrap || v.hex {
			v.top = max(0, v.top-tvRows)
		} else {
			v.left = max(0, v.left-8)
		}
	case k.Name == "RIGHT":
		if v.wrap || v.hex {
			v.top = min(last, v.top+tvRows)
		} else {
			v.left += 8
		}
	case k.Slot() == 0 && v.err == "":
		a.push(newInput("FIND", "Find in this file", v.find, 40, false, func(q string) {
			v.find = q
			v.findNext(a, v.top-1)
		}))
	case k.Slot() == 1:
		if v.find == "" {
			a.notify("FIND something first", true)
			return
		}
		v.findNext(a, max(v.hit, v.top))
	case k.Slot() == 2 && !v.hex:
		v.wrap = !v.wrap
		v.left, v.top, v.hit = 0, 0, -1
		a.notify(map[bool]string{true: "Long lines wrap", false: "Long lines scroll sideways (Z/C)"}[v.wrap], false)
	case k.Slot() == 3:
		if v.top > 0 {
			v.top = 0
		} else {
			v.top = last
		}
	case (k.Ch == 'e' || k.Ch == 'E') && !v.hex && v.err == "":
		a.replace(newEditor(v.path))
	}
}

func (v *TextFileView) Bar(a *App) []BarItem {
	end := "END"
	if v.top > 0 {
		end = "TOP"
	}
	return []BarItem{{"FIND", artZoom, v.err != ""}, {"AGAIN", artNext, v.find == ""}, {"WRAP", artQueue, v.hex},
		{end, artUp, v.err != ""}, {"BACK", artBack, false}}
}

func (v *TextFileView) Draw(a *App) {
	g := a.g
	title := musicASCII(filepath.Base(v.path))
	if v.hex {
		title += " (hex)"
	}
	pageTitle(a, title, artFolder)
	x, y0, w := 3, 18, W-6
	g.dsDetails(x, y0, w, tvRows*10+4)
	if v.err != "" {
		g.textC(W/2, 60, fit(v.err, W-20, 1, false), cSub, 1, false)
		return
	}
	rows := v.rows()
	nw := v.numW()
	if nw > 0 {
		g.rect(x+2, y0+2, nw, tvRows*10, cGridBG)
	}
	for i := 0; i < tvRows && v.top+i < len(rows); i++ {
		r := rows[v.top+i]
		y := y0 + 3 + i*10
		if v.top+i == v.hit {
			g.rect(x+2+nw, y-1, w-4-nw, 10, mix(curTheme.PaperEven, a.theme(), 0.4))
		}
		if nw > 0 && r.num > 0 {
			g.textR(x+nw-1, y, fmt.Sprint(r.num), cSub, 1, false)
		}
		text := r.text
		if !v.wrap && !v.hex {
			if v.left < len(text) {
				text = text[v.left:]
			} else {
				text = ""
			}
		}
		g.setClip(x+nw+4, y0, w-nw-8, tvRows*10+4)
		g.text(x+nw+4, y, text, cText, 1, false)
		g.noClip()
	}
	if len(rows) > tvRows {
		th := max(6, tvRows*10*tvRows/len(rows))
		ty := y0 + 2 + (tvRows*10-th)*v.top/(len(rows)-tvRows)
		g.rect(x+w-3, ty, 2, th, a.theme())
	}
	info := fmt.Sprintf("%d lines", len(v.lines))
	if v.hex {
		info = humanSize(int64(len(v.data)))
	}
	if v.truncated {
		info += ", first 512 KB"
	}
	g.textR(W-6, y0+tvRows*10+5, info, cSub, 1, false)
	if !v.wrap && !v.hex && v.left > 0 {
		g.text(6, y0+tvRows*10+5, fmt.Sprintf("column %d", v.left+1), cSub, 1, false)
	}
}
