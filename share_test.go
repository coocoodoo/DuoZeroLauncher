package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestShareFilesLoopback(t *testing.T) {
	send, recv := t.TempDir(), t.TempDir()
	setHome(t, send)
	write(t, filepath.Join(send, "Trip", "day1.txt"), "beach")
	write(t, filepath.Join(send, "Trip", "photos", "a.jpg"), "jpeg")
	write(t, filepath.Join(send, "notes.md"), "# notes")
	paths := []string{filepath.Join(send, "Trip"), filepath.Join(send, "notes.md")}
	s, err := startSendFiles(paths, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Stop()
	o, err := dlInfo("127.0.0.1:" + itoa(s.offer.Port))
	if err != nil || o.Kind != "files" || o.Count != 2 || strings.Join(o.Files, ",") != "Trip,notes.md" {
		t.Fatalf("offer %+v %v", o, err)
	}
	setHome(t, recv)
	if _, err := dlFetch(o, "0000x", nil); err == nil {
		t.Fatal("a wrong code was accepted")
	}
	dest := filepath.Join(downloadsDir(), "From test")
	for round := 1; round <= 2; round++ {
		file, err := dlFetch(o, s.code, nil)
		if err != nil {
			t.Fatal(err)
		}
		tops, err := unpackFilesTo(file, dest)
		os.Remove(file)
		if err != nil || len(tops) != 2 {
			t.Fatalf("round %d: %v %v", round, tops, err)
		}
	}
	if read(t, filepath.Join(dest, "Trip", "photos", "a.jpg")) != "jpeg" || read(t, filepath.Join(dest, "notes.md")) != "# notes" {
		t.Fatal("contents differ")
	}
	if !exists(filepath.Join(dest, "Trip (2)", "day1.txt")) || !exists(filepath.Join(dest, "notes (2).md")) {
		t.Fatal("a second copy should get new names, not overwrite")
	}
}

func TestUnpackFilesStaysInside(t *testing.T) {
	var b bytes.Buffer
	gz := gzip.NewWriter(&b)
	tw := tar.NewWriter(gz)
	for _, n := range []string{"ok.txt", "../evil.txt", "/abs.txt", "a/../../evil2.txt"} {
		tw.WriteHeader(&tar.Header{Name: n, Mode: 0o644, Size: 1, Typeflag: tar.TypeReg})
		tw.Write([]byte("x"))
	}
	tw.WriteHeader(&tar.Header{Name: "link", Linkname: "/etc/passwd", Typeflag: tar.TypeSymlink})
	tw.Close()
	gz.Close()
	root := t.TempDir()
	f := filepath.Join(root, "in.tgz")
	os.WriteFile(f, b.Bytes(), 0o644)
	dest := filepath.Join(root, "dest")
	if _, err := unpackFilesTo(f, dest); err != nil {
		t.Fatal(err)
	}
	if !exists(filepath.Join(dest, "ok.txt")) || exists(filepath.Join(root, "evil.txt")) || exists(filepath.Join(root, "evil2.txt")) || exists(filepath.Join(dest, "link")) {
		t.Fatal("something landed where it shouldn't")
	}
}
