package main

import (
	"dslauncher/cp0"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func typeText(v *Editor, a *App, s string) {
	for _, r := range s {
		if r == '\n' {
			v.Key(a, cp0.Key{Name: "ENTER"})
			continue
		}
		v.Key(a, cp0.Key{Ch: r})
	}
}

func TestEditor(t *testing.T) {
	dir := t.TempDir()
	setHome(t, dir)
	p := filepath.Join(dir, "main.go")
	// tabs, Windows line endings, no newline at the end: all kept
	os.WriteFile(p, []byte("func main() {\r\n\tx := 1\r\n}"), 0o640)
	a := testApp(t)
	v := newEditor(p)
	a.push(v)
	if v.err != "" || len(v.lines) != 3 || !v.crlf || v.finalNL {
		t.Fatalf("load: %q crlf %v final %v lines %d", v.err, v.crlf, v.finalNL, len(v.lines))
	}
	// to the end of line 2, then a new line keeps the tab indent
	v.Key(a, cp0.Key{Name: "DOWN"})
	v.Key(a, cp0.Key{Name: "END"})
	typeText(v, a, "\nprintln(x)")
	if got := string(v.lines[2]); got != "\tprintln(x)" {
		t.Fatalf("auto-indent: %q", got)
	}
	// Backspace at the start of a line joins it to the one above
	v.Key(a, cp0.Key{Name: "HOME"})
	v.Key(a, cp0.Key{Name: "BACKSPACE"})
	if len(v.lines) != 3 || string(v.lines[1]) != "\tx := 1\tprintln(x)" {
		t.Fatalf("join: %q", string(v.lines[1]))
	}
	v.Key(a, cp0.Key{Ctrl: 'z'}) // undo the join
	if len(v.lines) != 4 {
		t.Fatalf("undo: %d lines", len(v.lines))
	}
	v.Key(a, cp0.Key{Ctrl: 'y'}) // and redo it
	if len(v.lines) != 3 {
		t.Fatalf("redo: %d lines", len(v.lines))
	}
	v.Key(a, cp0.Key{Ctrl: 'z'})
	v.lastEdit = time.Time{}
	// duplicate, cut and paste lines
	v.row, v.col = 0, 0
	v.Key(a, cp0.Key{Ctrl: 'd'})
	if string(v.lines[1]) != "func main() {" || len(v.lines) != 5 {
		t.Fatalf("duplicate: %q", string(v.lines[1]))
	}
	v.Key(a, cp0.Key{Ctrl: 'x'})
	if len(v.lines) != 4 {
		t.Fatal("cut line")
	}
	v.find = "println"
	v.findNext(a, false)
	if v.row != 2 || v.col != 1 {
		t.Fatalf("find: %d,%d", v.row, v.col)
	}
	a.draw()
	a.saveShot("editor")
	v.Key(a, cp0.Key{Ctrl: 's'})
	b, _ := os.ReadFile(p)
	if got := string(b); got != "func main() {\r\n\tx := 1\r\n\tprintln(x)\r\n}" {
		t.Fatalf("saved %q", got)
	}
	if fi, _ := os.Stat(p); fi.Mode().Perm() != 0o640 && os.PathSeparator == '/' {
		t.Errorf("mode %v", fi.Mode())
	}
	if v.modified {
		t.Fatal("still marked unsaved after saving")
	}
	// unsaved work: Esc asks
	typeText(v, a, "// note")
	v.Key(a, cp0.Key{Name: "ESC"})
	m, ok := a.top().(*PopMenu)
	if !ok || m.title != "UNSAVED CHANGES" {
		t.Fatalf("Esc with unsaved work: %T", a.top())
	}
	a.draw()
	a.saveShot("editor-unsaved")
	m.sel = 1 // close without saving
	m.Key(a, cp0.Key{Name: "ENTER"})
	if _, still := a.top().(*Editor); still {
		t.Fatal("close without saving should close")
	}
	if b2, _ := os.ReadFile(p); string(b2) != string(b) {
		t.Fatal("closing without saving changed the file")
	}
	// binary files aren't opened for editing
	os.WriteFile(filepath.Join(dir, "blob"), []byte("\x00\x01ELF"), 0o644)
	if newEditor(filepath.Join(dir, "blob")).err == "" {
		t.Fatal("a binary file shouldn't be editable")
	}
}
