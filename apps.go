package main

// The app list: every .desktop entry in the system launcher's folder, plus the built-in tools
// (Store, CLI, Python, SSH, IP Panel, Calculator, Settings). Icons are scaled once and cached.

import (
	"bufio"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// Root of the system launcher (DSL_ROOT overrides it for testing on a PC).
func launchRoot() string {
	if r := os.Getenv("DSL_ROOT"); r != "" {
		return r
	}
	return "/usr/share/APPLaunch"
}

type Entry struct {
	Key      string // stable id: desktop file name or "builtin:x"
	Name     string
	Exec     string
	IconPath string
	Comment  string
	Terminal bool
	Builtin  string // "" for desktop apps
	Origin   string // a ZeroCode app's folder, from its "Made with ZeroCode (<folder>)" comment
	glyph    []string
	tint     RGB
	icons    map[int]*Icon
}

func (e *Entry) Icon(size int) *Icon {
	if e.icons == nil {
		e.icons = map[int]*Icon{}
	}
	if ic, ok := e.icons[size]; ok {
		return ic
	}
	var ic *Icon
	if e.IconPath != "" {
		ic = loadIcon(e.IconPath, size)
	}
	e.icons[size] = ic
	return ic
}

func builtins() []*Entry {
	store := filepath.Join(launchRoot(), "bin", "M5CardputerZero-AppStore")
	list := []*Entry{
		{Key: "builtin:store", Name: "Store", Builtin: "store", Exec: store, Comment: "Find and install apps",
			IconPath: filepath.Join(launchRoot(), "share/images/store_100.png")},
		{Key: "builtin:cli", Name: "CLI", Builtin: "cli", Comment: "A shell in Tiny Terminal",
			IconPath: filepath.Join(launchRoot(), "share/images/cli_100.png")},
		{Key: "builtin:python", Name: "Python", Builtin: "python", Comment: "An interactive Python prompt",
			IconPath: filepath.Join(launchRoot(), "share/images/python_100.png")},
		{Key: "builtin:ssh", Name: "SSH", Builtin: "ssh", Comment: "Connect to another computer",
			IconPath: filepath.Join(launchRoot(), "share/images/ssh_100.png")},
		{Key: "builtin:ip", Name: "IP Panel", Builtin: "ip", Comment: "Addresses, Wi-Fi and SSH at a glance",
			IconPath: filepath.Join(launchRoot(), "share/images/ip_panel_100.png")},
		{Key: "builtin:calc", Name: "Calculator", Builtin: "calc", Comment: "Type a sum, press Enter",
			IconPath: filepath.Join(launchRoot(), "share/images/math_100.png")},
		{Key: "builtin:pictochat", Name: "PictoChat", Builtin: "pictochat",
			Comment: "Draw and chat with Cardputers on this Wi-Fi", IconPath: "embed:apps/pictochat.png"},
		{Key: "builtin:files", Name: "Files", Builtin: "files",
			Comment: "Browse, copy, move and open your files", IconPath: "embed:apps/files.png"},
		{Key: "builtin:photos", Name: "Photos", Builtin: "photos",
			Comment: "Your pictures and screenshots", IconPath: "embed:apps/photos.png"},
		{Key: "builtin:music", Name: "Music", Builtin: "music",
			Comment: "Play songs from ~/Music, with visualizers", IconPath: "embed:apps/music.png"},
		{Key: "builtin:dlplay", Name: "Download Play", Builtin: "dlplay",
			Comment: "Share apps with a Cardputer on this Wi-Fi", IconPath: "embed:apps/dlplay.png"},
		{Key: "builtin:settings", Name: "Settings", Builtin: "settings", Comment: "Screen, sound, network, you",
			IconPath: filepath.Join(launchRoot(), "share/images/setting_100.png")},
	}
	if _, err := os.Stat(store); err != nil {
		list = list[1:]
	}
	return list
}

// scanApps reads the desktop entries.
func scanApps() []*Entry {
	dir := filepath.Join(launchRoot(), "applications")
	files, _ := filepath.Glob(filepath.Join(dir, "*.desktop"))
	var out []*Entry
	self, _ := os.Executable()
	for _, f := range files {
		e := parseDesktop(f)
		if e == nil || e.Name == "" || e.Exec == "" {
			continue
		}
		// do not list ourselves
		if strings.Contains(e.Exec, "zc-duo-launcher") || (self != "" && strings.Contains(e.Exec, self)) {
			continue
		}
		out = append(out, e)
	}
	return out
}

func parseDesktop(path string) *Entry {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	e := &Entry{Key: filepath.Base(path)}
	inEntry := false
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if strings.HasPrefix(line, "[") {
			inEntry = line == "[Desktop Entry]"
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !inEntry || !ok {
			continue
		}
		switch strings.TrimSpace(k) {
		case "Name":
			e.Name = strings.TrimSpace(v)
		case "Exec":
			e.Exec = strings.TrimSpace(v)
		case "Icon":
			v = strings.TrimSpace(v)
			if v != "" && !filepath.IsAbs(v) {
				v = filepath.Join(launchRoot(), v)
			}
			e.IconPath = v
		case "Comment":
			e.Comment = strings.TrimSpace(v)
		case "Terminal":
			e.Terminal = strings.EqualFold(strings.TrimSpace(v), "true")
		case "NoDisplay", "Hidden":
			if strings.EqualFold(strings.TrimSpace(v), "true") {
				return nil
			}
		}
	}
	if strings.HasPrefix(e.Comment, "Made with ZeroCode") {
		e.Origin = zcFolder(e.Comment)
		e.Comment = ""
	}
	return e
}

// orderApps puts favourites first (in the order pinned), then everything else by name;
// hidden apps are left out unless showHidden.
func orderApps(all []*Entry, cfg *Config, showHidden bool) []*Entry {
	var pinned, rest []*Entry
	for _, e := range all {
		if cfg.Hidden[e.Key] && !showHidden {
			continue
		}
		if cfg.pinIndex(e.Key) >= 0 {
			pinned = append(pinned, e)
		} else {
			rest = append(rest, e)
		}
	}
	sort.SliceStable(pinned, func(i, j int) bool { return cfg.pinIndex(pinned[i].Key) < cfg.pinIndex(pinned[j].Key) })
	sort.SliceStable(rest, func(i, j int) bool {
		if cfg.Sort == 1 {
			ti, tj := cfg.Used[rest[i].Key], cfg.Used[rest[j].Key]
			if ti != tj {
				return ti > tj
			}
		}
		return strings.ToLower(rest[i].Name) < strings.ToLower(rest[j].Name)
	})
	return append(pinned, rest...)
}

// splitExec splits a desktop Exec line into arguments (quotes respected, %f-style codes dropped).
func splitExec(s string) []string {
	var args []string
	var cur strings.Builder
	quote := rune(0)
	has := false
	for _, r := range s {
		switch {
		case quote != 0 && r == quote:
			quote = 0
		case quote == 0 && (r == '"' || r == '\''):
			quote, has = r, true
		case quote == 0 && (r == ' ' || r == '\t'):
			if has {
				args = append(args, cur.String())
				cur.Reset()
				has = false
			}
		default:
			cur.WriteRune(r)
			has = true
		}
	}
	if has {
		args = append(args, cur.String())
	}
	out := args[:0]
	for _, a := range args {
		if len(a) == 2 && a[0] == '%' {
			continue
		}
		out = append(out, a)
	}
	return out
}

// terminalPath finds Tiny Terminal, used by CLI, Python and SSH.
func terminalPath() string {
	for _, p := range []string{filepath.Join(launchRoot(), "bin", "zc-tiny-terminal")} {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	if p, err := exec.LookPath("zc-tiny-terminal"); err == nil {
		return p
	}
	return ""
}

// shellWrapper writes a tiny script that Tiny Terminal runs as its $SHELL, so it opens straight
// into a program (python3, ssh ...) instead of bash.
func shellWrapper(name string, argv ...string) (string, error) {
	dir := filepath.Join(cacheDir(), "shells")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	var q []string
	for _, a := range argv {
		q = append(q, "'"+strings.ReplaceAll(a, "'", `'\''`)+"'")
	}
	script := "#!/bin/sh\n# opened by Duo Launcher in Tiny Terminal\nexec " + strings.Join(q, " ") + "\n"
	p := filepath.Join(dir, name+".sh")
	if err := os.WriteFile(p, []byte(script), 0o755); err != nil {
		return "", err
	}
	return p, nil
}

func cacheDir() string {
	if d := os.Getenv("XDG_CACHE_HOME"); d != "" {
		return filepath.Join(d, "dslauncher")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".cache", "dslauncher")
}
