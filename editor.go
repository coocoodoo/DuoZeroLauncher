package main

// The text editor (Files > Edit, "Open with > Text editor", or E in the text viewer).
//
// Typing types (F/X/Z/C are letters here: the arrows are Fn+;  Fn+.  Fn+,  Fn+/ as printed on
// the keys). Enter keeps the line's indent, Tab indents, Backspace and Del delete.
// Ctrl+S save, Ctrl+Z undo, Ctrl+Y redo, Ctrl+F find, Ctrl+N find next, Ctrl+G go to a line,
// Ctrl+C / Ctrl+X copy or cut the line, Ctrl+V paste it below, Ctrl+D duplicate the line,
// Ctrl+A start / Ctrl+E end of the line (Home/End too), Esc closes (asking about unsaved work).
// Files are kept as they are: tab characters, Windows line endings and the final newline.

import (
	"dslauncher/cp0"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	edRows    = 9
	edMaxSize = 1 << 20
	edTab     = 4 // columns a tab takes on screen
)

type Editor struct {
	path      string
	lines     [][]rune
	row, col  int // the cursor (col in runes)
	top, left int // scroll (left in screen columns)
	want      int // the screen column Up/Down try to keep
	modified  bool
	crlf      bool
	finalNL   bool
	mode      os.FileMode
	undo      [][]string // snapshots: lines plus "row,col" at the end
	redo      [][]string
	lastEdit  time.Time
	lastKind  string // "type" or "delete": a change of kind starts a new undo step
	find      string
	clip      []rune
	err       string
	onSaved   func()
	t0        time.Time
}

func (v *Editor) wantsText() {}

// newEditor opens a file to edit (or an empty buffer for a file that doesn't exist yet).
func newEditor(path string) *Editor {
	v := &Editor{path: path, lines: [][]rune{{}}, finalNL: true, mode: 0o644, t0: time.Now()}
	fi, err := os.Stat(path)
	if os.IsNotExist(err) {
		return v
	}
	if err != nil {
		v.err = err.Error()
		return v
	}
	if fi.Size() > edMaxSize {
		v.err = "This file is too big to edit here (over 1 MB)"
		return v
	}
	v.mode = fi.Mode().Perm()
	data, binary, _, err := readPreview(path, edMaxSize)
	switch {
	case err != nil:
		v.err = err.Error()
		return v
	case binary:
		v.err = "This isn't a text file"
		return v
	}
	text := string(data)
	v.crlf = strings.Count(text, "\r\n") > strings.Count(text, "\n")/2 && strings.Contains(text, "\r\n")
	text = strings.ReplaceAll(text, "\r\n", "\n")
	v.finalNL = strings.HasSuffix(text, "\n") || text == ""
	text = strings.TrimSuffix(text, "\n")
	v.lines = nil
	for _, l := range strings.Split(text, "\n") {
		v.lines = append(v.lines, []rune(l))
	}
	return v
}

func (v *Editor) text() string {
	parts := make([]string, len(v.lines))
	for i, l := range v.lines {
		parts[i] = string(l)
	}
	nl := "\n"
	if v.crlf {
		nl = "\r\n"
	}
	s := strings.Join(parts, nl)
	if v.finalNL {
		s += nl
	}
	return s
}

func (v *Editor) save() error {
	if v.err != "" {
		return errors.New(v.err)
	}
	tmp := v.path + ".duo-save"
	if err := os.WriteFile(tmp, []byte(v.text()), v.mode); err != nil {
		return err
	}
	os.Chmod(tmp, v.mode)
	if err := os.Rename(tmp, v.path); err != nil {
		os.Remove(tmp)
		return err
	}
	v.modified = false
	if v.onSaved != nil {
		v.onSaved()
	}
	return nil
}

// ---- undo ----

func (v *Editor) snapshot() []string {
	s := make([]string, len(v.lines)+1)
	for i, l := range v.lines {
		s[i] = string(l)
	}
	s[len(v.lines)] = fmt.Sprintf("%d,%d", v.row, v.col)
	return s
}

func (v *Editor) restore(s []string) {
	v.lines = make([][]rune, len(s)-1)
	for i := range v.lines {
		v.lines[i] = []rune(s[i])
	}
	fmt.Sscanf(s[len(s)-1], "%d,%d", &v.row, &v.col)
	v.clamp()
}

// edit is called before every change. Typing in one go (no pause over a second), or deleting in
// one go, is one step to undo; switching between them, or anything else, starts a new step.
func (v *Editor) edit(kind string) {
	typing := kind != ""
	if !typing || kind != v.lastKind || time.Since(v.lastEdit) > time.Second || len(v.undo) == 0 {
		v.undo = append(v.undo, v.snapshot())
		if len(v.undo) > 200 {
			v.undo = v.undo[1:]
		}
	}
	v.lastKind = kind
	if typing {
		v.lastEdit = time.Now()
	} else {
		v.lastEdit = time.Time{}
	}
	v.redo = nil
	v.modified = true
}

// ---- moving ----

func (v *Editor) clamp() {
	v.row = max(0, min(v.row, len(v.lines)-1))
	v.col = max(0, min(v.col, len(v.lines[v.row])))
}

// screenCol is where rune col of a line lands on screen (tabs expand).
func screenCol(line []rune, col int) int {
	x := 0
	for i := 0; i < col && i < len(line); i++ {
		if line[i] == '\t' {
			x += edTab - x%edTab
		} else {
			x++
		}
	}
	return x
}

// runeAt is the rune index nearest screen column x.
func runeAt(line []rune, x int) int {
	sx := 0
	for i, r := range line {
		w := 1
		if r == '\t' {
			w = edTab - sx%edTab
		}
		if sx+w > x {
			return i
		}
		sx += w
	}
	return len(line)
}

func (v *Editor) vertical(d int) {
	v.row = max(0, min(v.row+d, len(v.lines)-1))
	v.col = runeAt(v.lines[v.row], v.want)
}

func (v *Editor) keepWant() { v.want = screenCol(v.lines[v.row], v.col) }

// ---- changing ----

func (v *Editor) insert(rs ...rune) {
	l := v.lines[v.row]
	nl := make([]rune, 0, len(l)+len(rs))
	nl = append(append(append(nl, l[:v.col]...), rs...), l[v.col:]...)
	v.lines[v.row] = nl
	v.col += len(rs)
	v.keepWant()
}

func (v *Editor) newline() {
	l := v.lines[v.row]
	indent := 0
	for indent < len(l) && indent < v.col && (l[indent] == ' ' || l[indent] == '\t') {
		indent++
	}
	rest := append(append([]rune{}, l[:indent]...), l[v.col:]...)
	v.lines[v.row] = append([]rune{}, l[:v.col]...)
	v.lines = append(v.lines[:v.row+1], append([][]rune{rest}, v.lines[v.row+1:]...)...)
	v.row++
	v.col = indent
	v.keepWant()
}

func (v *Editor) backspace() {
	switch {
	case v.col > 0:
		l := v.lines[v.row]
		v.lines[v.row] = append(append([]rune{}, l[:v.col-1]...), l[v.col:]...)
		v.col--
	case v.row > 0:
		prev := v.lines[v.row-1]
		v.col = len(prev)
		v.lines[v.row-1] = append(append([]rune{}, prev...), v.lines[v.row]...)
		v.lines = append(v.lines[:v.row], v.lines[v.row+1:]...)
		v.row--
	}
	v.keepWant()
}

func (v *Editor) deleteForward() {
	l := v.lines[v.row]
	switch {
	case v.col < len(l):
		v.lines[v.row] = append(append([]rune{}, l[:v.col]...), l[v.col+1:]...)
	case v.row < len(v.lines)-1:
		v.lines[v.row] = append(append([]rune{}, l...), v.lines[v.row+1]...)
		v.lines = append(v.lines[:v.row+1], v.lines[v.row+2:]...)
	}
}

// indentUnit is a tab if the file indents with tabs, else four spaces.
func (v *Editor) indentUnit() []rune {
	tabs, spaces := 0, 0
	for _, l := range v.lines {
		if len(l) > 0 {
			switch l[0] {
			case '\t':
				tabs++
			case ' ':
				spaces++
			}
		}
	}
	if tabs > spaces {
		return []rune{'\t'}
	}
	return []rune("    ")
}

func (v *Editor) findNext(a *App, fromNext bool) {
	if v.find == "" {
		return
	}
	q := []rune(strings.ToLower(v.find))
	n := len(v.lines)
	for k := 0; k <= n; k++ {
		r := (v.row + k) % n
		line := []rune(strings.ToLower(string(v.lines[r])))
		start := 0
		if k == 0 {
			start = v.col
			if fromNext {
				start++
			}
		}
		for c := start; c+len(q) <= len(line); c++ {
			if string(line[c:c+len(q)]) == string(q) {
				v.row, v.col = r, c
				v.keepWant()
				return
			}
		}
	}
	a.notify("\""+musicASCII(v.find)+"\" isn't in here", true)
}

// ---- keys ----

func (v *Editor) Key(a *App, k cp0.Key) {
	if v.err != "" {
		if k.Back() || k.Name == "ENTER" {
			a.pop()
		}
		return
	}
	v.t0 = time.Now() // the cursor shows while working
	if k.Ctrl != 0 {
		v.ctrl(a, k.Ctrl)
		return
	}
	switch k.Name {
	case "ESC":
		if k.Repeat {
			return
		}
		v.close(a)
		return
	case "UP":
		v.vertical(-1)
	case "DOWN":
		v.vertical(1)
	case "PGUP":
		v.vertical(-edRows)
	case "PGDN":
		v.vertical(edRows)
	case "LEFT":
		if v.col > 0 {
			v.col--
		} else if v.row > 0 {
			v.row--
			v.col = len(v.lines[v.row])
		}
		v.keepWant()
	case "RIGHT":
		if v.col < len(v.lines[v.row]) {
			v.col++
		} else if v.row < len(v.lines)-1 {
			v.row++
			v.col = 0
		}
		v.keepWant()
	case "HOME":
		v.col = 0
		v.keepWant()
	case "END":
		v.col = len(v.lines[v.row])
		v.keepWant()
	case "ENTER":
		v.edit("")
		v.newline()
	case "BACKSPACE":
		v.edit("delete")
		v.backspace()
	case "DELETE":
		v.edit("delete")
		v.deleteForward()
	case "TAB":
		v.edit("")
		v.insert(v.indentUnit()...)
	default:
		if k.Ch != 0 && (k.Ch >= ' ' || k.Ch == '\t') {
			v.edit("type")
			v.insert(k.Ch)
		} else if k.Name == "SPACE" {
			v.edit("type")
			v.insert(' ')
		}
	}
	v.clamp()
}

func (v *Editor) ctrl(a *App, c rune) {
	switch c {
	case 's':
		if err := v.save(); err != nil {
			alert(a, "NOT SAVED", err.Error())
			return
		}
		a.notify("Saved "+musicASCII(filepath.Base(v.path)), false)
	case 'z':
		if len(v.undo) == 0 {
			a.notify("Nothing to undo", true)
			return
		}
		v.redo = append(v.redo, v.snapshot())
		v.restore(v.undo[len(v.undo)-1])
		v.undo = v.undo[:len(v.undo)-1]
		v.lastEdit = time.Time{}
		v.modified = true
	case 'y':
		if len(v.redo) == 0 {
			a.notify("Nothing to redo", true)
			return
		}
		v.undo = append(v.undo, v.snapshot())
		v.restore(v.redo[len(v.redo)-1])
		v.redo = v.redo[:len(v.redo)-1]
		v.modified = true
	case 'f':
		a.push(newInput("FIND", "Find", v.find, 60, false, func(q string) {
			v.find = q
			v.findNext(a, false)
		}))
	case 'n':
		v.findNext(a, true)
	case 'g':
		a.push(newInput("GO TO LINE", fmt.Sprintf("Line (1-%d)", len(v.lines)), "", 7, false, func(s string) {
			if n, err := strconv.Atoi(strings.TrimSpace(s)); err == nil {
				v.row, v.col = n-1, 0
				v.clamp()
				v.keepWant()
				v.top = max(0, v.row-edRows/2)
			}
		}))
	case 'c':
		v.clip = append([]rune{}, v.lines[v.row]...)
		a.notify("Line copied: Ctrl+V pastes it", false)
	case 'x':
		v.edit("")
		v.clip = append([]rune{}, v.lines[v.row]...)
		if len(v.lines) > 1 {
			v.lines = append(v.lines[:v.row], v.lines[v.row+1:]...)
		} else {
			v.lines[0] = nil
		}
		v.clamp()
	case 'v':
		if v.clip == nil {
			a.notify("Copy or cut a line first (Ctrl+C / Ctrl+X)", true)
			return
		}
		v.edit("")
		v.lines = append(v.lines[:v.row+1], append([][]rune{append([]rune{}, v.clip...)}, v.lines[v.row+1:]...)...)
		v.row++
		v.clamp()
	case 'd':
		v.edit("")
		dup := append([]rune{}, v.lines[v.row]...)
		v.lines = append(v.lines[:v.row+1], append([][]rune{dup}, v.lines[v.row+1:]...)...)
		v.row++
		v.clamp()
	case 'a':
		v.col = 0
		v.keepWant()
	case 'e':
		v.col = len(v.lines[v.row])
		v.keepWant()
	}
}

// close leaves, asking first if there's unsaved work.
func (v *Editor) close(a *App) {
	if !v.modified {
		a.snd.Play("back")
		a.pop()
		return
	}
	popMenu(a, "UNSAVED CHANGES", []menuItem{
		{label: "Save and close", do: func(a *App) {
			if err := v.save(); err != nil {
				alert(a, "NOT SAVED", err.Error())
				return
			}
			a.notify("Saved "+musicASCII(filepath.Base(v.path)), false)
			a.pop()
		}},
		{label: "Close without saving", do: func(a *App) { a.pop() }},
		{label: "Keep editing", do: func(a *App) {}},
	})
}

func (v *Editor) Bar(a *App) []BarItem { return nil }

func (v *Editor) Animating(a *App) bool { return true } // the cursor blinks

func (v *Editor) Draw(a *App) {
	a.syncText()
	g := a.g
	title := musicASCII(filepath.Base(v.path))
	if v.modified {
		title = "* " + title
	}
	pageTitle(a, title, artEdit)
	x, y0, w := 3, 18, W-6
	g.dsDetails(x, y0, w, edRows*10+4)
	if v.err != "" {
		g.textC(W/2, 60, v.err, cSub, 1, false)
		keycaps(a, "ESC", "Back")
		return
	}
	numW := textW(fmt.Sprint(len(v.lines)), 1, false) + 6
	cols := (w - numW - 10) / 6
	// keep the cursor in view
	if v.row < v.top {
		v.top = v.row
	}
	if v.row >= v.top+edRows {
		v.top = v.row - edRows + 1
	}
	cx := screenCol(v.lines[v.row], v.col)
	if cx < v.left {
		v.left = max(0, cx-8)
	}
	if cx >= v.left+cols {
		v.left = cx - cols + 8
	}
	g.rect(x+2, y0+2, numW, edRows*10, cGridBG)
	tx := x + numW + 4
	for i := 0; i < edRows && v.top+i < len(v.lines); i++ {
		r := v.top + i
		y := y0 + 3 + i*10
		if r == v.row {
			g.rect(x+2+numW, y-1, w-4-numW, 10, mix(curTheme.PaperEven, a.theme(), 0.25))
		}
		g.textR(x+numW-1, y, fmt.Sprint(r+1), cSub, 1, false)
		// expand tabs, then show the columns in view
		var b strings.Builder
		for _, c := range v.lines[r] {
			if c == '\t' {
				b.WriteString(strings.Repeat(" ", edTab-b.Len()%edTab))
			} else if c < ' ' || c > '~' {
				b.WriteByte('?')
			} else {
				b.WriteRune(c)
			}
		}
		s := b.String()
		if v.left < len(s) {
			s = s[v.left:]
		} else {
			s = ""
		}
		g.setClip(tx, y0, w-numW-8, edRows*10+4)
		g.text(tx, y, s, cText, 1, false)
		g.noClip()
	}
	if int(time.Since(v.t0).Seconds()*2)%2 == 0 || time.Since(v.t0) < 600*time.Millisecond {
		px := tx + (cx-v.left)*6
		cursorC := a.theme().darker(0.3)
		if curTheme.Dark {
			cursorC = a.theme().lighter(0.25)
		}
		g.rect(px-1, y0+2+(v.row-v.top)*10, 2, 10, cursorC)
	}
	status := fmt.Sprintf("Ln %d, Col %d", v.row+1, cx+1)
	if v.crlf {
		status += "  CRLF"
	}
	g.text(6, y0+edRows*10+6, status, cSub, 1, false)
	if v.modified {
		g.textR(W-6, y0+edRows*10+6, "not saved", hex(0xc04040), 1, false)
	}
	keycaps(a, "^S", "Save", "^Z", "Undo", "^F", "Find", "ESC", "Close")
}
