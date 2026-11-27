package main

import (
	"dslauncher/cp0"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func findCheck(cs []*docCheck, key string) *docCheck {
	for _, c := range cs {
		if c.Key == key {
			return c
		}
	}
	return nil
}

func TestDoctor(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)
	cs := doctorChecks()
	h := findCheck(cs, "home")
	if h == nil || h.State != docFix || h.Root {
		t.Fatalf("a fresh home folder should need the home-screen fix (no sudo): %+v", h)
	}
	for _, key := range []string{"bootlogo", "cursor", "console", "hosts", "wifi", "groups"} {
		if c := findCheck(cs, key); c == nil || !c.Root {
			t.Errorf("%s should be a sudo fix", key)
		}
	}
	for _, key := range []string{"go", "zerocode", "path", "swap"} {
		if findCheck(cs, key) != nil {
			t.Errorf("%s is outside the Doctor's scope (launcher only)", key)
		}
	}

	// a home-screen script the user changed is kept as .duo-bak
	script := filepath.Join(home, ".local/bin/cardputer-home")
	os.MkdirAll(filepath.Dir(script), 0o755)
	os.WriteFile(script, []byte("#!/bin/sh\necho mine\n"), 0o755)
	if c := findCheck(doctorChecks(), "home"); c.State != docFix { // the drop-in is still missing
		t.Fatalf("home with only the script: %v", c.State)
	}

	var calls [][]string
	sudo := func(args ...string) (string, error) {
		calls = append(calls, args)
		var out []string
		for _, a := range args[4:] {
			key, _, _ := strings.Cut(a, ":")
			out = append(out, "ok "+key)
		}
		return strings.Join(out, "\n") + "\n", nil
	}
	fix := []*docCheck{h,
		{Key: "bootlogo", Root: true, rootArg: "bootlogo"},
		{Key: "console", Root: true, rootArg: "console"},
		{Key: "hosts", Root: true, rootArg: "hosts:duo"},
		{Key: "groups", Root: true, rootArg: "groups:pi:video,lp"}}
	res := doctorFix(fix, sudo)
	if len(res.errs) > 0 {
		t.Fatal(res.errs)
	}
	for _, k := range []string{"home", "bootlogo", "console", "hosts", "groups"} {
		if !res.ok[k] {
			t.Errorf("%s not reported fixed", k)
		}
	}
	// every system change in one sudo call, the arguments after the script
	if len(calls) != 1 || calls[0][0] != "sh" || strings.Join(calls[0][4:], " ") != "bootlogo console hosts:duo groups:pi:video,lp" {
		t.Fatalf("sudo calls %v", calls)
	}
	if os.PathSeparator == '/' && !isExecutable(script) {
		t.Error("cardputer-home should be executable")
	}
	if b, _ := os.ReadFile(script + ".duo-bak"); string(b) != "#!/bin/sh\necho mine\n" {
		t.Errorf("the user's script wasn't kept: %q", b)
	}
	if c := findCheck(doctorChecks(), "home"); c.State != docOK && c.State != docLater {
		t.Errorf("home screen should check out after the fix: %v %s", c.State, c.Detail)
	}
	m := loadDocManifest()
	if _, ok := m.Applied["home"]; !ok || strings.Join(m.Groups, ",") != "lp,video" {
		t.Errorf("manifest %+v", m)
	}

	// Undo puts the user's script back and removes the drop-in the Doctor created
	if !canUndo(h) {
		t.Fatal("the home fix should be undoable")
	}
	if err := doctorUndo(h, sudo); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(script); string(b) != "#!/bin/sh\necho mine\n" {
		t.Errorf("undo didn't restore the script: %q", b)
	}
	if exists(filepath.Join(home, ".config/systemd/user/APPLaunch.service.d/home-screen.conf")) {
		t.Error("undo left the drop-in")
	}
	if _, ok := loadDocManifest().Applied["home"]; ok {
		t.Error("undo left home in the manifest")
	}

	// a failed root change is reported, the others still count
	res = doctorFix([]*docCheck{{Key: "wifi", Name: "Wi-Fi", Root: true, rootArg: "wifi:pi"}, {Key: "cursor", Root: true, rootArg: "cursor"}},
		func(args ...string) (string, error) { return "fail wifi\nok cursor\n", nil })
	if !res.ok["cursor"] || res.ok["wifi"] || len(res.errs) != 1 {
		t.Errorf("partial failure: %+v", res)
	}

	// the log holds the report
	doctorLog(doctorReport(doctorChecks())...)
	if b, _ := os.ReadFile(doctorLogPath()); !strings.Contains(string(b), "home") || !strings.Contains(string(b), "Duo "+version) {
		t.Errorf("log: %s", b)
	}
}

func TestDoctorRootScript(t *testing.T) {
	s := doctorRootScript()
	for _, want := range []string{logoDropIn, consoleUnit, cursorUnit, wifiRule,
		"ExecStart=/usr/sbin/fb_load", "0x4B3A, 1", "systemctl enable duo-console.service",
		"systemctl enable hide-console-cursor.service", "cursor_blink", "network-control",
		"usermod -aG", "gpasswd -d", "bak /etc/hosts", "restore " + consoleUnit, "echo \"ok $key\""} {
		if !strings.Contains(s, want) {
			t.Errorf("root script lacks %q", want)
		}
	}
	for _, never := range []string{"cmdline.txt", "restart APPLaunch", "system-connections", "zerocode"} {
		if strings.Contains(s, never) {
			t.Errorf("root script mentions %q", never)
		}
	}
	if strings.Count(s, "<<'DUODOCTOREOF'") != strings.Count(s, "\nDUODOCTOREOF\n") {
		t.Error("unbalanced here-documents")
	}
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("no sh here to check the syntax")
	}
	if out, err := exec.Command(sh, "-n", "-c", s).CombinedOutput(); err != nil {
		t.Errorf("root script doesn't parse: %v %s", err, out)
	}
	// a step that fails mid-way reports fail, and the next change still runs
	probe := strings.Replace(s, "bootlogo)\n", "bootlogo)\nfalse\necho should-not-run\n", 1)
	probe = strings.Replace(probe, "cursor)\n", "cursor)\necho cursor-ran; exit 0\n", 1)
	out, _ := exec.Command(sh, "-c", probe, "doctor", "bootlogo", "cursor").CombinedOutput()
	if o := string(out); !strings.Contains(o, "fail bootlogo") || strings.Contains(o, "should-not-run") || !strings.Contains(o, "ok cursor") {
		t.Errorf("failure handling: %s", o)
	}
	// bad names are refused before anything runs
	out, _ = exec.Command(sh, "-c", "okname() { "+strings.SplitN(strings.SplitN(s, "okname() { ", 2)[1], "\n", 2)[0]+"\nokname 'pi;rm' && echo passed", "x").CombinedOutput()
	if strings.Contains(string(out), "passed") {
		t.Error("okname let a bad name through")
	}
}

func TestDocStateLabels(t *testing.T) {
	if got := docLater.label("next boot"); got != "AFTER BOOT" {
		t.Errorf("later label %q", got)
	}
	if got := docUpdate.label(""); got != "UPDATE" {
		t.Errorf("update label %q", got)
	}
}

func testApp(t *testing.T) *App {
	t.Helper()
	scr := &cp0.Screen{Px: make([]uint16, W*H)}
	a := &App{cfg: defaultConfig(), snd: &Sound{cache: map[string][]int16{}}, scr: scr, keys: newSimKeys(""),
		status: &Status{Battery: -1}, work: make(chan func(), 16), start: time.Now(), progress: -1}
	applyUITheme(a.cfg.Theme)
	a.g = newG(scr)
	a.stack = []View{&Home{last: time.Now()}}
	a.simShots = os.Getenv("DSL_DOC_SHOTS") // set it to look at the pages
	return a
}

func sampleChecks() []*docCheck {
	return []*docCheck{
		{Key: "home", Section: "Duo", Name: "Duo is the home screen", State: docOK, Detail: "same as Duo's copy",
			What: "Makes Duo the launcher that starts when the Cardputer turns on."},
		{Key: "console", Section: "System", Name: "Terminal kept off the screen", State: docFix, Root: true,
			Why: "the text console can show over Duo", Detail: "duo-console.service isn't installed", What: "Stops the console."},
		{Key: "hosts", Section: "System", Name: "Device name in /etc/hosts", State: docUpdate, Root: true, Why: "admin actions stall"},
		{Key: "groups", Section: "System", Name: "User groups", State: docLater, Later: "next login"},
		{Key: "sound", Section: "Factory", Name: "Sound server", State: docInfo, Help: "Restart it over SSH."},
		{Key: "bluetooth", Section: "Duo", Name: "Bluetooth audio allowed", State: docNA},
	}
}

// The Doctor says whether anything is needed, and what to do about each thing.
func TestDoctorAdvice(t *testing.T) {
	cs := sampleChecks()
	if got := tally(cs).headline(); got != "2 fixes needed, 1 waits for a restart, 1 needs you." {
		t.Errorf("headline %q", got)
	}
	if got := summarize(cs); got != "2 TO FIX" {
		t.Errorf("summary %q", got)
	}
	advice := strings.Join(doctorAdvice(cs, false), "\n")
	for _, want := range []string{"these need fixing (Fix everything does them all, with one password)",
		"- Terminal kept off the screen: without it the text console can show over Duo.",
		"restart the Cardputer to finish:\n- User groups.", "- Sound server: Restart it over SSH.",
		"Already in place, nothing to do (1): Duo is the home screen.", "Not needed on this Cardputer: Bluetooth audio allowed."} {
		if !strings.Contains(advice, want) {
			t.Errorf("advice lacks %q:\n%s", want, advice)
		}
	}
	if !strings.Contains(strings.Join(doctorAdvice(cs, true), "\n"), "app --doctor --fix does them all") {
		t.Error("the terminal advice should name the command")
	}
	for _, c := range cs {
		if c.todo(false) == "" {
			t.Errorf("%s: no advice", c.Key)
		}
	}
	if got := cs[0].todo(false); got != "Nothing to do: it's already in place." {
		t.Errorf("OK todo %q", got)
	}
	if got := cs[3].todo(false); !strings.Contains(got, "restart the Cardputer") {
		t.Errorf("later todo %q", got)
	}
	ok := []*docCheck{cs[0], cs[5]}
	if got := tally(ok).headline(); got != "All set: Duo has everything it needs." || summarize(ok) != "ALL OK" {
		t.Errorf("all-OK headline %q / %q", got, summarize(ok))
	}

	a := testApp(t)
	v := &DocText{title: "DOCTOR: WHAT TO DO", paras: doctorAdvice(cs, false)}
	a.push(v)
	a.draw()
	a.saveShot("doctor-advice")
	n := len(v.wrapped())
	for i := 0; i < n+3; i++ {
		v.Key(a, cp0.Key{Name: "DOWN"})
	}
	if v.top != v.maxTop() || v.top == 0 {
		t.Errorf("scrolled to %d of %d lines (last top %d)", v.top, n, v.maxTop())
	}
	a.draw()
	a.saveShot("doctor-advice-end")
	doctorDetail(a, page("DOCTOR", artSystem), cs[1])
	if d, ok := a.top().(*DocText); !ok || d.act != "FIX" {
		t.Fatalf("a check that needs fixing should offer FIX: %#v", a.top())
	}
	a.draw()
	a.saveShot("doctor-detail")
}

// At start-up the Doctor asks once, on the home screen, and not again for the same problems.
func TestDoctorAsksOnce(t *testing.T) {
	setHome(t, t.TempDir())
	a := testApp(t)
	need := sampleChecks()[1:3]
	a.doctorAsk, a.doctorAskFP = need, "console=1,hosts=2"
	a.push(&DocText{title: "X"})
	a.checkDoctorAsk()
	if _, ok := a.top().(*Confirm); ok {
		t.Fatal("asked over another screen")
	}
	a.pop()
	a.checkDoctorAsk()
	c, ok := a.top().(*Confirm)
	if !ok || !strings.Contains(c.msg, "2 things need attention: Terminal kept off the screen, Device name in /etc/hosts") {
		t.Fatalf("start-up question: %#v", a.top())
	}
	a.draw()
	a.saveShot("doctor-ask")
	if a.doctorAsk != nil || loadDocManifest().Asked != "console=1,hosts=2" {
		t.Error("the question should be remembered")
	}
}
