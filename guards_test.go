package main

import (
	"bytes"
	"encoding/binary"
	"hash/crc32"
	"image"
	"image/color"
	"image/gif"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A small file that claims to be an enormous picture is refused before any decoding (it would
// take gigabytes and end Duo); an ordinary one still opens.
func TestHugePictureRefused(t *testing.T) {
	var buf bytes.Buffer
	png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 8, 8)))
	b := buf.Bytes()
	dir := t.TempDir()
	ok := filepath.Join(dir, "ok.png")
	os.WriteFile(ok, b, 0o644)
	if _, err := loadPhoto(ok); err != nil {
		t.Fatal("a small picture should open:", err)
	}
	// IHDR is the first chunk: length(4) "IHDR" width(4) height(4) ... crc(4), after the 8-byte signature
	huge := append([]byte(nil), b...)
	binary.BigEndian.PutUint32(huge[16:], 30000)
	binary.BigEndian.PutUint32(huge[20:], 30000)
	binary.BigEndian.PutUint32(huge[29:], crc32.ChecksumIEEE(huge[12:29]))
	if cfg, err := png.DecodeConfig(bytes.NewReader(huge)); err != nil || cfg.Width != 30000 {
		t.Fatalf("the test picture isn't what it should be: %v %v", cfg, err)
	}
	bad := filepath.Join(dir, "huge.png")
	os.WriteFile(bad, huge, 0o644)
	if _, err := decodeAny(bad, huge); err == nil {
		t.Fatal("a 30000 x 30000 picture was decoded")
	}
	if photoFits(8000, 6000) != true || photoFits(30000, 30000) || photoFits(0, 5) || photoFits(1<<20, 1) {
		t.Error("photoFits")
	}
}

// A GIF's frames are counted without decoding, so an animation too big for memory shows only
// its first frame.
func TestGIFFrameCount(t *testing.T) {
	mk := func(w, h, n int) []byte {
		g := &gif.GIF{}
		pal := color.Palette{color.Black, color.White}
		for i := 0; i < n; i++ {
			g.Image = append(g.Image, image.NewPaletted(image.Rect(0, 0, w, h), pal))
			g.Delay = append(g.Delay, 5)
		}
		var buf bytes.Buffer
		if err := gif.EncodeAll(&buf, g); err != nil {
			t.Fatal(err)
		}
		return buf.Bytes()
	}
	for _, n := range []int{1, 2, 7, 40} {
		if got := gifFrames(mk(20, 10, n)); got != n {
			t.Errorf("%d frames counted as %d", n, got)
		}
	}
	if gifFrames([]byte("not a gif at all")) != 0 || gifFrames(nil) != 0 {
		t.Error("rubbish has no frames")
	}
	d := mk(20, 10, 5)
	for cut := 0; cut < len(d); cut += 7 { // cut short anywhere: no panic
		gifFrames(d[:cut])
	}
	dir := t.TempDir()
	small := filepath.Join(dir, "small.gif")
	os.WriteFile(small, mk(40, 30, 6), 0o644)
	if p, err := loadPhoto(small); err != nil || len(p.frames) != 6 {
		t.Fatalf("a small animation should play: %v", err)
	}
	big := filepath.Join(dir, "big.gif") // 3000 x 2400 x 4 frames = 28.8 million pixels
	os.WriteFile(big, mk(3000, 2400, 4), 0o644)
	p, err := loadPhoto(big)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.frames) != 0 || p.img == nil {
		t.Fatalf("a too-big animation should show its first frame only (%d frames)", len(p.frames))
	}
}

// Undo copies are kept to a few megabytes however big the file is.
func TestEditorUndoMemory(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "big.txt")
	os.WriteFile(path, []byte(strings.Repeat(strings.Repeat("x", 99)+"\n", 9000)), 0o644) // 900 KB
	v := newEditor(path)
	if v.err != "" {
		t.Fatal(v.err)
	}
	for i := 0; i < 60; i++ {
		v.edit("")
		v.insert('a')
	}
	if n := len(v.undo); n > 9 || n < 3 {
		t.Fatalf("%d undo steps kept for a 900 KB file", n)
	}
	small := newEditor(filepath.Join(dir, "new.txt"))
	for i := 0; i < 250; i++ {
		small.edit("")
		small.insert('a')
	}
	if len(small.undo) != 200 {
		t.Fatalf("a small file keeps 200 steps, not %d", len(small.undo))
	}
}

// Saving a file opened through a link changes the real file and leaves the link a link.
func TestEditorSavesThroughLinks(t *testing.T) {
	dir := t.TempDir()
	real := filepath.Join(dir, "real.txt")
	link := filepath.Join(dir, "link.txt")
	os.WriteFile(real, []byte("one\n"), 0o644)
	if err := os.Symlink(real, link); err != nil {
		t.Skip("no links here:", err)
	}
	v := newEditor(link)
	v.edit("")
	v.insert('X')
	if err := v.save(); err != nil {
		t.Fatal(err)
	}
	if fi, _ := os.Lstat(link); fi.Mode()&os.ModeSymlink == 0 {
		t.Fatal("the link was replaced by a file")
	}
	if b, _ := os.ReadFile(real); string(b) != "Xone\n" {
		t.Fatalf("the real file has %q", b)
	}
}
