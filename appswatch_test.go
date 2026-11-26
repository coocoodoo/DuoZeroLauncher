package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// An app registered from outside Duo shows up without restarting Duo.
func TestNewAppsShowUp(t *testing.T) {
	root := t.TempDir()
	t.Setenv("DSL_ROOT", root)
	t.Setenv("DSL_CONFIG", filepath.Join(t.TempDir(), "config"))
	apps := filepath.Join(root, "applications")
	os.MkdirAll(apps, 0o755)
	os.MkdirAll(filepath.Join(root, "share/images"), 0o755)
	a := testApp(t)
	a.rescan()
	has := func(key string) bool {
		for _, e := range a.all {
			if e.Key == key {
				return true
			}
		}
		return false
	}
	a.checkApps() // the first look only remembers
	os.WriteFile(filepath.Join(apps, "zc-ps1.desktop"), []byte("[Desktop Entry]\nName=PS1\nExec=/bin/true\nType=Application\n"), 0o644)
	later := time.Now().Add(time.Minute)
	os.Chtimes(apps, later, later)
	a.checkApps()
	if has("zc-ps1.desktop") {
		t.Fatal("looked again before appsCheckEvery")
	}
	a.appsCheckAt = time.Now().Add(-appsCheckEvery)
	a.checkApps()
	if !has("zc-ps1.desktop") {
		t.Fatal("the new app didn't show up")
	}
	os.Remove(filepath.Join(apps, "zc-ps1.desktop"))
	gone := time.Now().Add(2 * time.Minute)
	os.Chtimes(apps, gone, gone)
	a.appsCheckAt = time.Time{}
	a.dimLevel = 2 // asleep: no looking
	a.checkApps()
	if !has("zc-ps1.desktop") {
		t.Fatal("rescanned while the screen was off")
	}
	a.dimLevel = 0
	a.checkApps()
	if has("zc-ps1.desktop") {
		t.Fatal("the removed app is still listed")
	}
}
