package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func setHome(t *testing.T, dir string) {
	t.Helper()
	t.Setenv("HOME", dir)
	t.Setenv("USERPROFILE", dir)
	t.Setenv("XDG_CACHE_HOME", filepath.Join(dir, ".cache"))
}

func TestDownloadPlayLoopback(t *testing.T) {
	send, recv := t.TempDir(), t.TempDir()
	setHome(t, send)
	app := filepath.Join(send, "apps", "demo")
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(os.MkdirAll(filepath.Join(app, "data"), 0o755))
	must(os.WriteFile(filepath.Join(app, "app.conf"), []byte("NAME=Demo\nSLUG=demo\n"), 0o644))
	must(os.WriteFile(filepath.Join(app, "app"), []byte("#!/bin/sh\necho hi\n"), 0o755))
	must(os.WriteFile(filepath.Join(app, "data", "level.txt"), []byte("level one"), 0o644))
	must(os.WriteFile(filepath.Join(app, "app.prev"), []byte("old build"), 0o755)) // left out

	var sentTo atomic.Value
	s, err := startSend(shareable{e: &Entry{Name: "Demo"}, slug: "demo", dir: app}, 0, func(to string) { sentTo.Store(to) })
	must(err)
	defer s.Stop()
	addr := "127.0.0.1:" + itoa(s.offer.Port)

	o, err := dlInfo(addr)
	must(err)
	if o.App != "Demo" || o.Slug != "demo" || o.Size != s.offer.Size || o.Sha != s.offer.Sha {
		t.Fatalf("offer %+v, want %+v", o, s.offer)
	}

	setHome(t, recv)
	if _, err := dlFetch(o, "nope", nil); err == nil || !strings.Contains(err.Error(), "wrong code") {
		t.Fatalf("wrong code accepted: %v", err)
	}
	file, err := dlFetch(o, s.code, nil)
	must(err)
	dir, err := dlInstall(file, o.Slug, false)
	must(err)
	if dir != filepath.Join(recv, "apps", "demo") {
		t.Fatalf("installed to %s", dir)
	}
	got, err := os.ReadFile(filepath.Join(dir, "data", "level.txt"))
	must(err)
	if string(got) != "level one" {
		t.Fatalf("level.txt = %q", got)
	}
	if exists(filepath.Join(dir, "app.prev")) {
		t.Fatal("app.prev should not be shared")
	}
	if runtime.GOOS != "windows" {
		fi, err := os.Stat(filepath.Join(dir, "app"))
		must(err)
		if fi.Mode()&0o111 == 0 {
			t.Fatal("app lost its execute bit")
		}
	}
	for i := 0; i < 50 && sentTo.Load() == nil; i++ {
		time.Sleep(10 * time.Millisecond)
	}
	if sentTo.Load() == nil {
		t.Fatal("the sender wasn't told the app was sent")
	}
	// a second copy needs replace=true, and keeps the first one
	file2, err := dlFetch(o, s.code, nil)
	must(err)
	if _, err := dlInstall(file2, o.Slug, false); err == nil {
		t.Fatal("installed over an existing app without asking")
	}
	if _, err := dlInstall(file2, o.Slug, true); err != nil {
		t.Fatal(err)
	}
	prev, _ := filepath.Glob(filepath.Join(recv, "apps", "demo.prev-*"))
	if len(prev) != 1 {
		t.Fatalf("old copy not kept: %v", prev)
	}
}

func TestUnpackRefusesEscapes(t *testing.T) {
	for _, name := range []string{"../evil", "/abs/evil", "ok/../../evil"} {
		var buf bytes.Buffer
		gz := gzip.NewWriter(&buf)
		tw := tar.NewWriter(gz)
		tw.WriteHeader(&tar.Header{Name: "app.conf", Mode: 0o644, Size: 2, Typeflag: tar.TypeReg})
		tw.Write([]byte("x\n"))
		tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: 4, Typeflag: tar.TypeReg})
		tw.Write([]byte("evil"))
		tw.Close()
		gz.Close()
		dst := t.TempDir()
		if err := unpackApp(&buf, dst); err == nil {
			t.Errorf("%q was unpacked", name)
		}
	}
	// a link is refused too
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	tw.WriteHeader(&tar.Header{Name: "link", Linkname: "/etc/passwd", Typeflag: tar.TypeSymlink})
	tw.Close()
	gz.Close()
	if err := unpackApp(&buf, t.TempDir()); err == nil {
		t.Error("a symlink was unpacked")
	}
}

func TestSearchRanking(t *testing.T) {
	var list []*Entry
	for _, n := range []string{"PictoChat", "Calculator", "CLI", "GridZero", "Tiny Terminal", "Settings"} {
		list = append(list, &Entry{Key: n, Name: n})
	}
	names := func(q string) string {
		var out []string
		for _, e := range searchApps(list, q) {
			out = append(out, e.Name)
		}
		return strings.Join(out, ",")
	}
	cases := map[string]string{
		"c":    "Calculator,CLI,PictoChat",
		"term": "Tiny Terminal",
		"gz":   "GridZero",
		"set":  "Settings",
		"":     "",
		"zzz":  "",
	}
	for q, want := range cases {
		if got := names(q); got != want {
			t.Errorf("search %q = %q, want %q", q, got, want)
		}
	}
}
