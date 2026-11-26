package main

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"dslauncher/cp0"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func write(t *testing.T, p, body string) {
	t.Helper()
	os.MkdirAll(filepath.Dir(p), 0o755)
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func read(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestFilesSortingAndNames(t *testing.T) {
	dir := t.TempDir()
	for _, n := range []string{"file10.txt", "file2.txt", "Apple.png", "banana.mp3", ".hidden"} {
		write(t, filepath.Join(dir, n), n)
	}
	os.Mkdir(filepath.Join(dir, "zeta"), 0o755)
	names := func(es []fileEntry) string {
		var out []string
		for _, e := range es {
			out = append(out, e.name)
		}
		return strings.Join(out, ",")
	}
	es, _ := listDir(dir, false, sortName)
	if got := names(es); got != "zeta,Apple.png,banana.mp3,file2.txt,file10.txt" {
		t.Errorf("by name: %s", got)
	}
	es, _ = listDir(dir, true, sortName)
	if !strings.Contains(names(es), ".hidden") {
		t.Error("hidden files should show when asked")
	}
	es, _ = listDir(dir, false, sortType)
	if got := names(es); got != "zeta,banana.mp3,Apple.png,file2.txt,file10.txt" {
		t.Errorf("by type: %s", got)
	}
	if k := kindOf("song.FLAC", 0); k != kAudio {
		t.Errorf("kind of song.FLAC: %d", k)
	}
	if got := uniqueName(dir, "file2.txt"); got != "file2 (2).txt" {
		t.Errorf("uniqueName: %s", got)
	}
	write(t, filepath.Join(dir, "x.tar.gz"), "")
	if got := uniqueName(dir, "x.tar.gz"); got != "x (2).tar.gz" {
		t.Errorf("uniqueName keeps .tar.gz: %s", got)
	}
	for _, bad := range []string{"", "..", "a/b"} {
		if validName(bad) == nil {
			t.Errorf("%q should be refused", bad)
		}
	}
}

func TestFilesCopyMove(t *testing.T) {
	root := t.TempDir()
	src := filepath.Join(root, "src")
	write(t, filepath.Join(src, "a.txt"), "alpha")
	write(t, filepath.Join(src, "sub", "b.txt"), "beta")
	if runtime.GOOS != "windows" {
		os.Symlink("a.txt", filepath.Join(src, "link"))
	}
	dst := filepath.Join(root, "dst")
	os.Mkdir(dst, 0o755)
	var last int64
	made, err := pasteInto([]string{src}, dst, false, func(d, tot int64) { last = d }, nil)
	if err != nil || len(made) != 1 {
		t.Fatalf("copy: %v %v", made, err)
	}
	if read(t, filepath.Join(dst, "src", "sub", "b.txt")) != "beta" || last != 9 {
		t.Fatalf("copied content, progress %d", last)
	}
	if runtime.GOOS != "windows" {
		if l, err := os.Readlink(filepath.Join(dst, "src", "link")); err != nil || l != "a.txt" {
			t.Errorf("the link wasn't copied as a link: %q %v", l, err)
		}
	}
	made, _ = pasteInto([]string{src}, dst, false, nil, nil) // again: a new name
	if filepath.Base(made[0]) != "src (2)" {
		t.Errorf("second copy: %v", made)
	}
	if _, err := pasteInto([]string{src}, filepath.Join(src, "sub"), false, nil, nil); err == nil {
		t.Error("a folder must not be pasted inside itself")
	}
	// stopping half way leaves no half-written file
	write(t, filepath.Join(root, "big.bin"), strings.Repeat("x", 1<<20))
	if _, err := pasteInto([]string{filepath.Join(root, "big.bin")}, dst, false, nil, func() bool { return true }); err != errStopped {
		t.Errorf("stop: %v", err)
	}
	if exists(filepath.Join(dst, "big.bin")) {
		t.Error("a stopped copy left a partial file")
	}
	// move
	made, err = pasteInto([]string{filepath.Join(root, "big.bin")}, dst, true, nil, nil)
	if err != nil || exists(filepath.Join(root, "big.bin")) || !exists(made[0]) {
		t.Fatalf("move: %v %v", made, err)
	}
}

func TestFilesArchives(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "docs", "one.txt"), "one")
	write(t, filepath.Join(root, "docs", "deep", "two.txt"), "two")
	zipPath := filepath.Join(root, "docs.zip")
	if err := zipPaths([]string{filepath.Join(root, "docs")}, zipPath, nil, nil); err != nil {
		t.Fatal(err)
	}
	os.RemoveAll(filepath.Join(root, "docs"))
	out, err := extractArchive(zipPath, nil, nil)
	if err != nil || filepath.Base(out) != "docs" {
		t.Fatalf("extract: %s %v", out, err)
	}
	if read(t, filepath.Join(out, "docs", "deep", "two.txt")) != "two" {
		t.Fatal("zip round trip lost a file")
	}
	// a zip that tries to write outside its folder
	var zb bytes.Buffer
	zw := zip.NewWriter(&zb)
	for _, n := range []string{"ok.txt", "../evil.txt", "/abs.txt", "a/../../evil2.txt"} {
		w, _ := zw.Create(n)
		w.Write([]byte("x"))
	}
	zw.Close()
	bad := filepath.Join(root, "bad.zip")
	os.WriteFile(bad, zb.Bytes(), 0o644)
	out, err = extractArchive(bad, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !exists(filepath.Join(out, "ok.txt")) || exists(filepath.Join(root, "evil.txt")) || exists(filepath.Join(root, "evil2.txt")) {
		t.Fatal("zip slip: something landed outside the folder")
	}
	// tar.gz
	var tb bytes.Buffer
	gz := gzip.NewWriter(&tb)
	tw := tar.NewWriter(gz)
	tw.WriteHeader(&tar.Header{Name: "pkg/readme.md", Mode: 0o644, Size: 5, Typeflag: tar.TypeReg})
	tw.Write([]byte("hello"))
	tw.WriteHeader(&tar.Header{Name: "../../escape", Mode: 0o644, Size: 1, Typeflag: tar.TypeReg})
	tw.Write([]byte("x"))
	tw.Close()
	gz.Close()
	tgz := filepath.Join(root, "pkg.tar.gz")
	os.WriteFile(tgz, tb.Bytes(), 0o644)
	out, err = extractArchive(tgz, nil, nil)
	if err != nil || filepath.Base(out) != "pkg" || read(t, filepath.Join(out, "pkg", "readme.md")) != "hello" {
		t.Fatalf("tar.gz: %s %v", out, err)
	}
	if exists(filepath.Join(filepath.Dir(root), "escape")) {
		t.Fatal("tar slip")
	}
}

func TestFilesSearchTrashPreview(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)
	write(t, filepath.Join(home, "a", "b", "Holiday-photo.jpg"), "x")
	write(t, filepath.Join(home, ".cache", "holiday-cache"), "x")
	write(t, filepath.Join(home, "notes.txt"), "a\nb\n")
	found := searchFiles(home, "holiday", 10, nil)
	if len(found) != 1 || found[0].name != "Holiday-photo.jpg" {
		t.Fatalf("search: %+v", found)
	}
	if err := moveToTrash(filepath.Join(home, "notes.txt")); err != nil {
		t.Fatal(err)
	}
	items := trashList()
	if len(items) != 1 || items[0].orig != filepath.Join(home, "notes.txt") {
		t.Fatalf("trash: %+v", items)
	}
	write(t, filepath.Join(home, "notes.txt"), "newer") // the name was taken meanwhile
	back, err := items[0].restore()
	if err != nil || filepath.Base(back) != "notes (2).txt" || read(t, back) != "a\nb\n" {
		t.Fatalf("restore: %s %v", back, err)
	}
	if len(trashList()) != 0 {
		t.Error("restored item still in the trash")
	}
	_, binary, _, _ := readPreview(filepath.Join(home, "notes.txt"), 1000)
	write(t, filepath.Join(home, "blob"), "\x00\x01\x02ELF")
	_, binary2, _, _ := readPreview(filepath.Join(home, "blob"), 1000)
	if binary || !binary2 {
		t.Errorf("binary detection: text %v, blob %v", binary, binary2)
	}
	if naturalLess("file10", "file2") || !naturalLess("File2", "file10") {
		t.Error("naturalLess")
	}
}

// The browser and its screens (DSL_DOC_SHOTS=<dir> saves them).
func TestFilesScreens(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)
	t.Setenv("DSL_CONFIG", filepath.Join(home, ".config/dslauncher/config"))
	for _, n := range []string{"Pictures/beach.jpg", "Music/song.mp3", "Documents/notes.txt", "Documents/report.pdf",
		"Documents/main.go", "Documents/backup.zip", "Documents/setup.sh", "Documents/clip.mp4", "Documents/game.cue",
		"Documents/data.bin"} {
		body := "hello\n"
		if strings.HasSuffix(n, ".go") {
			body = "package main\n\nimport \"fmt\"\n\nfunc main() {\n\tfmt.Println(\"Hello from the Cardputer, with a long line that has to wrap around\")\n}\n"
		}
		if strings.HasSuffix(n, ".bin") {
			body = "\x7fELF\x02\x01\x01\x00\x00\x00binary data here"
		}
		write(t, filepath.Join(home, n), body)
	}
	os.Mkdir(filepath.Join(home, "Documents", "Old letters"), 0o755)
	a := testApp(t)
	a.openFiles()
	v := a.top().(*FileBrowser)
	if v.dir != homeDir() {
		t.Fatalf("starts in %s", v.dir)
	}
	// into Documents: Enter on it
	for i, e := range v.shown() {
		if e.name == "Documents" {
			v.sel = i
		}
	}
	v.Key(a, cp0.Key{Name: "ENTER"})
	if filepath.Base(v.dir) != "Documents" {
		t.Fatalf("Enter on a folder: %s", v.dir)
	}
	v.Key(a, cp0.Key{Name: "DOWN"})
	v.Key(a, cp0.Key{Name: "SPACE"}) // mark two
	v.Key(a, cp0.Key{Name: "SPACE"})
	if len(v.marks) != 2 {
		t.Fatalf("marks: %v", v.marks)
	}
	a.draw()
	a.saveShot("files-browser")
	v.Key(a, cp0.Key{Code: 7}) // 6: menu
	if m, ok := a.top().(*PopMenu); !ok || m.title != "FILES" {
		t.Fatalf("6 should open the menu: %T", a.top())
	}
	a.draw()
	a.saveShot("files-menu")
	a.pop()
	v.Key(a, cp0.Key{Ctrl: 'c'}) // copy the two marked
	if len(filesClip.paths) != 2 || len(v.marks) != 0 {
		t.Fatalf("copy: %v", filesClip.paths)
	}
	os.Mkdir(filepath.Join(home, "Copies"), 0o755)
	v.go_(a, filepath.Join(home, "Copies"))
	v.Key(a, cp0.Key{Ctrl: 'v'})
	drain(a, func() bool { return a.busy() == "" && len(v.shown()) > 2 })
	if n := len(v.shown()); n != 3 { // ".." and the two pasted
		t.Fatalf("after paste: %d entries", n)
	}
	v.go_(a, filepath.Join(home, "Documents"))
	v.Key(a, cp0.Key{Code: 8}) // 7: places
	if m, ok := a.top().(*PopMenu); !ok || m.title != "PLACES" {
		t.Fatalf("7 should open places: %T", a.top())
	}
	a.draw()
	a.saveShot("files-places")
	a.pop()
	for i, e := range v.shown() {
		if e.name == "main.go" {
			v.sel = i
		}
	}
	v.Key(a, cp0.Key{Name: "ENTER"})
	tv, ok := a.top().(*TextFileView)
	if !ok || tv.hex {
		t.Fatalf("main.go should open as text: %T", a.top())
	}
	a.draw()
	a.saveShot("files-text")
	a.pop()
	for i, e := range v.shown() {
		if e.name == "data.bin" {
			v.sel = i
		}
	}
	v.Key(a, cp0.Key{Name: "ENTER"})
	if tv, ok := a.top().(*TextFileView); !ok || !tv.hex {
		t.Fatalf("data.bin should open in hex: %T", a.top())
	}
	a.draw()
	a.saveShot("files-hex")
	a.pop()
	for i, e := range v.shown() {
		if e.name == "Old letters" {
			v.sel = i
		}
	}
	v.properties(a, *v.cur())
	drain(a, func() bool {
		d, ok := a.top().(*DocText)
		return ok && strings.Contains(strings.Join(d.paras, " "), "files,")
	})
	a.draw()
	a.saveShot("files-properties")
	a.pop()
	for i, e := range v.shown() {
		if e.name == "notes.txt" {
			v.sel = i
		}
	}
	v.Key(a, cp0.Key{Name: "DELETE"})
	a.top().Key(a, cp0.Key{Name: "ENTER"}) // yes
	if exists(filepath.Join(home, "Documents", "notes.txt")) || len(trashList()) != 1 {
		t.Fatal("Del should move to the trash")
	}
	a.push(&TrashView{items: trashList(), back: v})
	a.draw()
	a.saveShot("files-trash")
	a.top().Key(a, cp0.Key{Code: 5}) // 4: restore
	if !exists(filepath.Join(home, "Documents", "notes.txt")) {
		t.Fatal("restore didn't put it back")
	}
	time.Sleep(time.Millisecond)
}
