package main

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"
)

func TestParseOwner(t *testing.T) {
	p := "/usr/share/APPLaunch/applications/music.desktop"
	cases := map[string]string{
		"m5cardputerzero-music: " + p:                                "m5cardputerzero-music",
		"camera:arm64: " + p:                                         "camera",
		"pkg-a, pkg-b: " + p:                                         "pkg-a",
		"diversion by x from: " + p + "\ndoom-zero-dev0: " + p:       "doom-zero-dev0",
		"other: /usr/share/APPLaunch/applications/music.desktop.bak": "",
		"dpkg-query: no path found matching pattern " + p:            "",
	}
	for out, want := range cases {
		if got := parseOwner(out, p); got != want {
			t.Errorf("parseOwner(%q) = %q, want %q", out, got, want)
		}
	}
}

func TestParseSim(t *testing.T) {
	out := `NOTE: This is only a simulation!
The following packages will be REMOVED:
  factorytest stress-ng-gui
Remv factorytest [1.0.0-m5stack1]
Remv stress-ng-gui:arm64 [0.1]
Inst foo (1.0 Debian:13/stable [arm64])`
	rem, inst := parseSim(out)
	if !reflect.DeepEqual(rem, []string{"factorytest", "stress-ng-gui"}) {
		t.Errorf("remove = %v", rem)
	}
	if !reflect.DeepEqual(inst, []string{"foo"}) {
		t.Errorf("install = %v", inst)
	}
}

func TestDependents(t *testing.T) {
	// status, package, provides, depends, pre-depends
	row := func(f ...string) string { return strings.Join(f, "\t") }
	db := parseDpkgDB(strings.Join([]string{
		row("ii ", "app", "", "libfoo (>= 1), libc6", ""),
		row("ii ", "libfoo", "", "", "libc6"),
		row("ii ", "plugin", "", "app:any", ""),         // needs app: goes with it
		row("ii ", "either", "", "libfoo | libbar", ""), // libbar stays: keeps it
		row("ii ", "libbar", "", "", ""),
		row("ii ", "viewer", "", "pdf-reader", ""),           // virtual, only app-pdf provides it
		row("ii ", "app-pdf", "pdf-reader (= 1)", "app", ""), // needs app
		row("ii ", "other", "", "mta", ""),                   // mta provided by two: survives
		row("ii ", "mta-a", "mta", "", ""),
		row("ii ", "mta-b", "mta", "", ""),
		row("rc ", "old", "", "app", ""), // only settings left: ignored
		row("ii ", "libc6", "", "", ""),
	}, "\n"))
	got := dependents(db, "app")
	sort.Strings(got)
	want := []string{"app", "app-pdf", "plugin", "viewer"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("dependents(app) = %v, want %v", got, want)
	}
	if got := dependents(db, "mta-a"); !reflect.DeepEqual(got, []string{"mta-a"}) {
		t.Errorf("dependents(mta-a) = %v", got)
	}
	if got := dependents(db, "libfoo"); len(got) != 5 { // libfoo app app-pdf plugin viewer; either keeps libbar
		t.Errorf("dependents(libfoo) = %v", got)
	}
}

// On the device: DSL_APT_COMPARE="pkg ..." checks dependents against apt's own simulation.
func TestDependentsMatchApt(t *testing.T) {
	pkgs := strings.Fields(os.Getenv("DSL_APT_COMPARE"))
	if len(pkgs) == 0 {
		t.Skip("set DSL_APT_COMPARE to package names to compare with apt-get -s")
	}
	out, err := runC(30*time.Second, "dpkg-query", "-W", dpkgDBFormat)
	if err != nil {
		t.Fatal(err)
	}
	db := parseDpkgDB(out)
	for _, p := range pkgs {
		got := dependents(db, p)
		sim, _ := runC(3*time.Minute, "apt-get", "-s", "-q", "-o", "APT::Get::AutomaticRemove=false", "remove", p)
		apt, _ := parseSim(sim)
		sort.Strings(got)
		sort.Strings(apt)
		if !reflect.DeepEqual(got, apt) {
			t.Errorf("%s: dpkg says %v, apt says %v", p, got, apt)
		} else {
			t.Logf("%s: %d package(s), same as apt", p, len(got))
		}
	}
}

func TestHostnameListed(t *testing.T) {
	old := hostsFile
	t.Cleanup(func() { hostsFile = old })
	hostsFile = filepath.Join(t.TempDir(), "hosts")
	os.WriteFile(hostsFile, []byte("127.0.0.1\tlocalhost\n# 127.0.1.1 chris\n127.0.1.1\t\tcardputerzero\n"), 0o644)
	if hostnameListed("chris") {
		t.Error("a commented-out name counts as missing")
	}
	if !hostnameListed("CardputerZero") || !hostnameListed("localhost") {
		t.Error("listed names not found")
	}
	for name, ok := range map[string]bool{"chris": true, "my-cardputer2": true, "-x": false, "a b": false, "x.y": false, "": false} {
		if hostRE.MatchString(name) != ok {
			t.Errorf("hostRE(%q) != %v", name, ok)
		}
	}
}

func TestParseStates(t *testing.T) {
	m := parseStates("camera|1.0.2-m5stack1|optional|no|ii |3097\nbash|5.2|required|yes|ii |7000\ngone|1|optional|no|rc |0\n")
	if s := m["camera"]; !s.Installed || s.Version != "1.0.2-m5stack1" || s.SizeKB != 3097 || protectedPkg(s) {
		t.Errorf("camera = %+v", s)
	}
	if !protectedPkg(m["bash"]) {
		t.Error("bash must be protected")
	}
	if m["gone"].Installed {
		t.Error("rc is not installed")
	}
	if !protectedPkg(pkgState{Name: "applaunch", Priority: "optional"}) {
		t.Error("applaunch must be protected")
	}
}

func TestZeroCodeNames(t *testing.T) {
	if s, ok := zcSlug("zc-tiny-terminal.desktop"); !ok || s != "tiny-terminal" {
		t.Errorf("slug = %q %v", s, ok)
	}
	for _, k := range []string{"music.desktop", "zc-.desktop", "zc-Bad.desktop", "zc-a/b.desktop", "zc-x.desk"} {
		if _, ok := zcSlug(k); ok {
			t.Errorf("%q is not a ZeroCode entry", k)
		}
	}
	abs := filepath.Join(os.TempDir(), "apps", "notepad") // absolute on this OS
	if d := zcFolder("Made with ZeroCode (" + abs + ")"); d != abs {
		t.Errorf("folder = %q", d)
	}
	if d := zcFolder("Made with ZeroCode (apps/x)"); d != "" {
		t.Errorf("relative folder accepted: %q", d)
	}
}

func TestInside(t *testing.T) {
	root := filepath.FromSlash("/home/pi/apps")
	for p, want := range map[string]bool{
		"/home/pi/apps/notepad":     true,
		"/home/pi/apps/games/x":     true,
		"/home/pi/apps":             false,
		"/home/pi/apps/../.ssh":     false,
		"/home/pi/apps2/x":          false,
		"/home/pi/apps/..x/ok-name": true,
	} {
		if got := inside(filepath.FromSlash(p), root); got != want {
			t.Errorf("inside(%q) = %v, want %v", p, got, want)
		}
	}
}

func TestLastError(t *testing.T) {
	apt := "Reading package lists...\nE: Could not get lock /var/lib/dpkg/lock-frontend\nE: Unable to acquire the dpkg frontend lock"
	if got := lastError(apt, errors.New("exit status 100")); got != "Unable to acquire the dpkg frontend lock" {
		t.Errorf("apt: %q", got)
	}
	if got := lastError("zc-app: no app zc-x", nil); got != "no app zc-x" {
		t.Errorf("helper: %q", got)
	}
	if got := lastError("", errors.New("timed out")); got != "timed out" {
		t.Errorf("empty: %q", got)
	}
}

// fakeHome points the home folder, the launcher root and the bin at a temp folder.
func fakeHome(t *testing.T) (home, root string) {
	base := t.TempDir()
	home, root = filepath.Join(base, "home"), filepath.Join(base, "launch")
	for _, d := range []string{filepath.Join(home, "apps"), filepath.Join(root, "applications")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("DSL_ROOT", root)
	t.Setenv("DSL_DATA", filepath.Join(base, "data"))
	old := zcHelper
	zcHelper = filepath.Join(base, "no-helper")
	t.Cleanup(func() { zcHelper = old })
	return home, root
}

func newZCApp(t *testing.T, home, root, slug string) *Entry {
	dir := filepath.Join(home, "apps", slug)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(dir, "run.sh"), []byte("#!/bin/sh\n"), 0o755)
	desk := filepath.Join(root, "applications", "zc-"+slug+".desktop")
	os.WriteFile(desk, []byte("[Desktop Entry]\nName=T\nExec=x\nComment=Made with ZeroCode ("+dir+")\n"), 0o644)
	return parseDesktop(desk)
}

// A failed helper must leave the app exactly where it was, with no record in the bin.
func TestRemoveRollsBack(t *testing.T) {
	home, root := fakeHome(t)
	e := newZCApp(t, home, root, "scratch")
	r, err := planRemoval(e)
	if err != nil {
		t.Fatal(err)
	}
	if r.kind != kindZC || r.folder != filepath.Join(home, "apps", "scratch") || r.bytes == 0 {
		t.Fatalf("plan = %+v", r)
	}
	a := &App{work: make(chan func(), 4)}
	if _, err := r.do(a, ""); err == nil {
		t.Fatal("uninstall without the helper should fail")
	}
	if !isDir(r.folder) || !exists(filepath.Join(r.folder, "run.sh")) {
		t.Fatal("app folder was not put back")
	}
	if recs := loadRemoved(); len(recs) != 0 {
		t.Fatalf("bin should be empty, has %d", len(recs))
	}
}

// An uninstall cut short is put right when the bin is read.
func TestReconcile(t *testing.T) {
	home, root := fakeHome(t)
	e := newZCApp(t, home, root, "half")
	folder := filepath.Join(home, "apps", "half")

	// 1: the folder moved but the launcher entry is still there: move it back, drop the record.
	rec := &removedApp{ID: newRecordID("half"), Kind: kindZC, Name: "T", Key: e.Key, Slug: "half", Folder: folder, State: "removing"}
	os.MkdirAll(rec.dir(), 0o700)
	writeRecord(rec)
	if err := os.Rename(folder, filepath.Join(rec.dir(), "app")); err != nil {
		t.Fatal(err)
	}
	if recs := loadRemoved(); len(recs) != 0 {
		t.Fatalf("record kept: %+v", recs)
	}
	if !isDir(folder) {
		t.Fatal("folder not put back")
	}

	// 2: the launcher entry is gone too: the uninstall finished, keep it in the bin.
	os.Remove(filepath.Join(root, "applications", "zc-half.desktop"))
	rec = &removedApp{ID: newRecordID("half"), Kind: kindZC, Name: "T", Key: e.Key, Slug: "half", Folder: folder, State: "removing"}
	os.MkdirAll(rec.dir(), 0o700)
	writeRecord(rec)
	os.Rename(folder, filepath.Join(rec.dir(), "app"))
	recs := loadRemoved()
	if len(recs) != 1 || recs[0].State != "" || !recs[0].canRestore() {
		t.Fatalf("want one restorable record, got %+v", recs)
	}

	// 3: a restore stopped after moving the folder out, before the helper ran: back into the bin.
	recs[0].State = "restoring"
	writeRecord(recs[0])
	if err := os.Rename(filepath.Join(rec.dir(), "app"), folder); err != nil {
		t.Fatal(err)
	}
	recs = loadRemoved()
	if len(recs) != 1 || recs[0].State != "" || !recs[0].canRestore() || exists(folder) {
		t.Fatalf("restore not undone: %+v, folder there: %v", recs, exists(folder))
	}
	if err := recs[0].deleteForGood(); err != nil || exists(rec.dir()) {
		t.Fatalf("delete: %v", err)
	}
}

func TestBuiltinsAndSelfStay(t *testing.T) {
	fakeHome(t)
	if _, err := planRemoval(&Entry{Key: "builtin:calc", Builtin: "calc"}); err == nil {
		t.Error("built-in accepted")
	}
	if _, err := planRemoval(&Entry{Key: "zc-duo-launcher.desktop"}); err == nil {
		t.Error("Duo accepted")
	}
	if _, err := planRemoval(&Entry{Key: "zc-x.desktop", Origin: filepath.FromSlash("/etc/x")}); err == nil {
		t.Error("folder outside ~/apps accepted")
	}
}
