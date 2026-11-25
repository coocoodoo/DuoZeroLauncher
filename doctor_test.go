package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
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
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	cs := doctorChecks()
	h := findCheck(cs, "home")
	if h == nil || h.State != docFix || h.Root {
		t.Fatalf("a fresh home folder should need the home-screen fix (no sudo): %+v", h)
	}
	for _, key := range []string{"bootlogo", "console", "hosts"} {
		if c := findCheck(cs, key); c == nil || !c.Root {
			t.Errorf("%s should be a sudo fix", key)
		}
	}
	var calls [][]string
	sudo := func(args ...string) (string, error) { calls = append(calls, args); return "", nil }
	fix := []*docCheck{h, {Key: "bootlogo", Root: true}, {Key: "console", Root: true}, {Key: "hosts", Root: true}}
	if err := doctorFix(fix, sudo); err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(home, ".local/bin/cardputer-home")
	if !isExecutable(script) && os.PathSeparator == '/' {
		t.Error("cardputer-home should be executable")
	}
	if b, _ := os.ReadFile(filepath.Join(home, ".config/systemd/user/APPLaunch.service.d/home-screen.conf")); !strings.Contains(string(b), "cardputer-home") {
		t.Error("home-screen.conf not written")
	}
	if c := findCheck(doctorChecks(), "home"); c.State != docOK && os.PathSeparator == '/' {
		t.Error("home screen should check out after the fix")
	}
	// one sudo call for the system files (both fixes), one for /etc/hosts
	if len(calls) < 1 || calls[0][0] != "sh" || calls[0][len(calls[0])-2] != "bootlogo" || calls[0][len(calls[0])-1] != "console" {
		t.Fatalf("sudo calls %v", calls)
	}
}

func TestDoctorRootScript(t *testing.T) {
	s := doctorRootScript()
	for _, want := range []string{"/etc/systemd/system/fb_load.service.d/duo.conf", "/etc/systemd/system/duo-console.service",
		"ExecStart=/usr/sbin/fb_load", "0x4B3A, 1", "systemctl enable duo-console.service"} {
		if !strings.Contains(s, want) {
			t.Errorf("root script lacks %q", want)
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
}
