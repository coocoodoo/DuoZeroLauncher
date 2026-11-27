package main

import (
	"dslauncher/cp0"
	"path/filepath"
	"testing"
)

// Settings has a second page; 5 turns to it and every tile there opens.
func TestSettingsPage2(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)
	t.Setenv("DSL_CONFIG", filepath.Join(home, ".config/dslauncher/config"))
	a := testApp(t)
	a.link = a.newLink()
	s := newSettings(a)
	a.push(s)
	if s.pages() != 2 {
		t.Fatalf("%d pages", s.pages())
	}
	s.Key(a, cp0.Key{Code: 6}) // 5: next page
	if s.sel != tilesPerPage {
		t.Fatalf("page 2 starts at %d", s.sel)
	}
	a.draw()
	a.saveShot("settings-page2")
	for i := tilesPerPage; i < len(s.tiles); i++ {
		s.sel = i
		s.Key(a, cp0.Key{Name: "ENTER"})
		if a.top() == s {
			t.Fatalf("%s didn't open", s.tiles[i].name)
		}
		a.draw()
		if s.tiles[i].name == "Photos" || s.tiles[i].name == "Music" {
			a.saveShot("settings-" + s.tiles[i].name)
		}
		for a.top() != s {
			a.pop()
		}
	}
	s.sel = tilesPerPage + 6 // the second row of page 2: Updates
	s.Key(a, cp0.Key{Name: "UP"})
	if s.sel != tilesPerPage {
		t.Fatalf("up from the second row: %d", s.sel)
	}
	s.Key(a, cp0.Key{Name: "DOWN"})
	if s.sel != tilesPerPage+6 {
		t.Fatalf("down: %d", s.sel)
	}
	s.Key(a, cp0.Key{Code: 6}) // back to page 1, same place
	if s.sel != 6 {
		t.Fatalf("page 1: %d", s.sel)
	}
}
