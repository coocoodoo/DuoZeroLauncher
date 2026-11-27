package main

import (
	"dslauncher/cp0"
	"path/filepath"
	"testing"
)

func TestUIThemesCatalog(t *testing.T) {
	if len(uiThemes) != 10 {
		t.Fatalf("want 10 themes, got %d", len(uiThemes))
	}
	ids, names := map[string]bool{}, map[string]bool{}
	for i, th := range uiThemes {
		if th.ID == "" || th.Name == "" || th.Tagline == "" {
			t.Fatalf("theme %d missing metadata: %+v", i, th)
		}
		if ids[th.ID] {
			t.Fatalf("duplicate theme id %q", th.ID)
		}
		if names[th.Name] {
			t.Fatalf("duplicate theme name %q", th.Name)
		}
		ids[th.ID], names[th.Name] = true, true
		if got := uiThemeIndex(th.ID); got != i {
			t.Fatalf("uiThemeIndex(%q) = %d, want %d", th.ID, got, i)
		}
	}
	if uiThemeIndex("nonexistent") != 0 {
		t.Fatalf("unknown theme should fall back to Classic (0)")
	}
}

func TestUIThemeConfigRoundTrip(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "config")
	t.Setenv("DSL_CONFIG", cfgPath)
	c := loadConfig()
	if c.Theme != 0 {
		t.Fatalf("default theme = %d, want 0", c.Theme)
	}
	for i, th := range uiThemes {
		c.Theme = i
		c.Color = i % len(themes)
		if err := c.Save(); err != nil {
			t.Fatalf("save theme %s: %v", th.ID, err)
		}
		got := loadConfig()
		if got.Theme != i || got.Color != i%len(themes) {
			t.Fatalf("reloaded theme=%d color=%d, want %d %d", got.Theme, got.Color, i, i%len(themes))
		}
	}
}

// ThemePick previews themes live, restores on Back, saves on Choose, and preserves the exact same
// Favourite colour accent behaviour across every theme.
func TestThemePickerAndColorBehavior(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)
	t.Setenv("DSL_CONFIG", filepath.Join(home, ".config/dslauncher/config"))
	t.Cleanup(func() { applyUITheme(0) })

	a := testApp(t)
	a.cfg.path = configPath()
	a.all = builtins()
	a.apps = orderApps(a.all, a.cfg, false)
	a.link = a.newLink()

	// open Themes from Settings > Page 2
	s := newSettings(a)
	a.push(s)
	idx := -1
	for i, tl := range s.tiles {
		if tl.name == "Themes" {
			idx = i
			break
		}
	}
	if idx < 0 {
		t.Fatal("Themes tile not found in Settings")
	}
	s.sel = idx
	s.Key(a, cp0.Key{Name: "ENTER"})
	tp, ok := a.top().(*ThemePick)
	if !ok {
		t.Fatalf("expected *ThemePick, got %T", a.top())
	}

	// move down to Dark (1): live preview switches curTheme without saving yet
	tp.Key(a, cp0.Key{Name: "DOWN"})
	if tp.sel != 1 || curTheme.ID != "dark" || a.cfg.Theme != 0 {
		t.Fatalf("live preview: sel=%d cur=%s saved=%d", tp.sel, curTheme.ID, a.cfg.Theme)
	}

	// Esc restores the saved Classic theme
	tp.Key(a, cp0.Key{Code: 1, Name: "ESC"})
	if curTheme.ID != "classic" || a.cfg.Theme != 0 {
		t.Fatalf("after Esc: cur=%s saved=%d", curTheme.ID, a.cfg.Theme)
	}

	// reopen, pick Game Boy (5), open Colour picker (key 5 -> Slot 1), change colour, then Choose
	s.Key(a, cp0.Key{Name: "ENTER"})
	tp = a.top().(*ThemePick)
	for i := 0; i < 5; i++ {
		tp.Key(a, cp0.Key{Name: "DOWN"})
	}
	if tp.sel != 5 || curTheme.ID != "gameboy" {
		t.Fatalf("sel=%d cur=%s", tp.sel, curTheme.ID)
	}
	tp.Key(a, cp0.Key{Code: 6}) // key 5 -> COLOUR
	cp, ok := a.top().(*ColorPick)
	if !ok {
		t.Fatalf("expected *ColorPick, got %T", a.top())
	}
	cp.sel = 2 // RED
	cp.Key(a, cp0.Key{Name: "ENTER"})
	if a.cfg.Color != 2 || curTheme.ID != "gameboy" {
		t.Fatalf("after colour pick: color=%d cur=%s", a.cfg.Color, curTheme.ID)
	}
	tp.Key(a, cp0.Key{Name: "ENTER"})
	if a.cfg.Theme != 5 || curTheme.ID != "gameboy" {
		t.Fatalf("after choose: saved=%d cur=%s", a.cfg.Theme, curTheme.ID)
	}

	// Verify Favourite colour behaves identically on every single theme: changing a.cfg.Color
	// changes the top title bar pixels (y=8, x=160) in every one of the 10 themes.
	for i, th := range uiThemes {
		applyUITheme(i)
		a.cfg.Theme = i
		a.cfg.Color = 10 // SKY
		a.draw()
		skyBar := a.scr.Px[8*W+160]
		a.cfg.Color = 2 // RED
		a.draw()
		redBar := a.scr.Px[8*W+160]
		if skyBar == redBar {
			t.Errorf("theme %s: favourite colour did not tint top bar (both %#04x)", th.Name, skyBar)
		}
	}
}

// Render screenshots of the Themes picker and Home across light and dark themes when
// DSL_DOC_SHOTS is set.
func TestThemeScreens(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)
	t.Setenv("DSL_CONFIG", filepath.Join(home, ".config/dslauncher/config"))
	t.Cleanup(func() { applyUITheme(0) })

	a := testApp(t)
	a.all = builtins()
	a.apps = orderApps(a.all, a.cfg, false)
	a.link = a.newLink()

	// Settings page 2 showing the Themes tile
	s := newSettings(a)
	s.sel = tilesPerPage + 7 // Themes
	a.push(s)
	a.draw()
	a.saveShot("themes-settings-tile")

	// Themes picker on Classic, Dark, Game Boy, Terminal
	for _, idx := range []int{0, 1, 5, 8} {
		a.stack = a.stack[:1]
		a.cfg.Theme = idx
		applyUITheme(idx)
		a.draw()
		a.saveShot("themes-home-" + uiThemes[idx].ID)

		tp := &ThemePick{sel: idx}
		a.push(tp)
		a.draw()
		a.saveShot("themes-pick-" + uiThemes[idx].ID)
	}
}
