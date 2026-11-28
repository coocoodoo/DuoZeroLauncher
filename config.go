package main

// Launcher settings and the user profile, kept as key=value lines in ~/.config/dslauncher/config.

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	Name       string
	Color      int // index into themes (favourite colour)
	Theme      int // index into uiThemes (launcher surface theme)
	BirthMonth int
	BirthDay   int
	Message    string
	Clock24    bool
	Sounds     bool
	SoundVol   int // 0..10
	Jingle     bool
	DimAfter   int // seconds, 0 = never
	OffAfter   int // seconds after dimming, 0 = never
	Sort       int // 0 name, 1 recently used
	Pinned     []string
	Hidden     map[string]bool
	Used       map[string]int64
	LastSSH    string
	PictoAddr  string // the last PictoChat server joined by address
	PictoCode  string // the last internet PictoChat room code
	AlarmOn    bool
	AlarmH     int
	AlarmM     int
	PIN        string
	LockOnWake bool
	LinkOn     bool // Duo Link: show the screen on a car stereo
	LinkAudio  bool // and send it the sound
	Saver      bool // Wi-Fi off while the screen is off, on battery (saver.go)
	LinkWifi   bool // over the Cardputer's own Wi-Fi (a hotspot the stereo joins)
	path       string
}

func defaultConfig() *Config {
	user := os.Getenv("USER")
	if user == "" {
		user = "ZERO"
	}
	return &Config{
		Name: strings.ToUpper(user), Color: 10, BirthMonth: 1, BirthDay: 1, Message: "HELLO FROM MY CARDPUTER",
		Clock24: true, Sounds: true, SoundVol: 6, Jingle: true, DimAfter: 60, OffAfter: 120,
		Hidden: map[string]bool{}, Used: map[string]int64{}, AlarmH: 7, AlarmM: 0, LinkAudio: true, Saver: true,
	}
}

func configPath() string {
	if p := os.Getenv("DSL_CONFIG"); p != "" {
		return p
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "dslauncher", "config")
}

func loadConfig() *Config {
	c := defaultConfig()
	c.path = configPath()
	data, err := os.ReadFile(c.path)
	if err != nil {
		return c
	}
	for _, line := range strings.Split(string(data), "\n") {
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		n, _ := strconv.Atoi(v)
		b := v == "1"
		switch k {
		case "name":
			c.Name = v
		case "color":
			if n >= 0 && n < len(themes) {
				c.Color = n
			}
		case "theme":
			if idx := uiThemeIndex(v); idx > 0 || v == uiThemes[0].ID {
				c.Theme = idx
			} else if n >= 0 && n < len(uiThemes) {
				c.Theme = n
			}
		case "birth_month":
			c.BirthMonth = n
		case "birth_day":
			c.BirthDay = n
		case "message":
			c.Message = v
		case "clock24":
			c.Clock24 = b
		case "sounds":
			c.Sounds = b
		case "sound_vol":
			c.SoundVol = n
		case "jingle":
			c.Jingle = b
		case "dim_after":
			c.DimAfter = n
		case "off_after":
			c.OffAfter = n
		case "sort":
			c.Sort = n
		case "pinned":
			c.Pinned = splitList(v)
		case "hidden":
			for _, h := range splitList(v) {
				c.Hidden[h] = true
			}
		case "used":
			for _, kv := range splitList(v) {
				a, t, ok := strings.Cut(kv, ":")
				if ok {
					ts, _ := strconv.ParseInt(t, 10, 64)
					c.Used[a] = ts
				}
			}
		case "last_ssh":
			c.LastSSH = v
		case "picto_addr":
			c.PictoAddr = v
		case "picto_code":
			c.PictoCode = v
		case "alarm_on":
			c.AlarmOn = b
		case "alarm_h":
			c.AlarmH = n
		case "alarm_m":
			c.AlarmM = n
		case "pin":
			c.PIN = v
		case "lock_on_wake":
			c.LockOnWake = b
		case "duo_link":
			c.LinkOn = b
		case "link_audio":
			c.LinkAudio = b
		case "saver":
			c.Saver = b
		case "link_wifi":
			c.LinkWifi = b
		}
	}
	return c
}

func splitList(v string) []string {
	var out []string
	for _, s := range strings.Split(v, ",") {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out
}

func (c *Config) Save() error {
	bit := func(b bool) string { return strconv.Itoa(btoi(b)) }
	var hidden, used []string
	for k, v := range c.Hidden {
		if v {
			hidden = append(hidden, k)
		}
	}
	sort.Strings(hidden)
	for k, v := range c.Used {
		used = append(used, fmt.Sprintf("%s:%d", k, v))
	}
	sort.Strings(used)
	lines := []string{
		"name=" + c.Name, "color=" + strconv.Itoa(c.Color), "theme=" + uiThemeByIndex(c.Theme).ID,
		"birth_month=" + strconv.Itoa(c.BirthMonth), "birth_day=" + strconv.Itoa(c.BirthDay),
		"message=" + c.Message, "clock24=" + bit(c.Clock24), "sounds=" + bit(c.Sounds),
		"sound_vol=" + strconv.Itoa(c.SoundVol), "jingle=" + bit(c.Jingle),
		"dim_after=" + strconv.Itoa(c.DimAfter), "off_after=" + strconv.Itoa(c.OffAfter),
		"sort=" + strconv.Itoa(c.Sort), "pinned=" + strings.Join(c.Pinned, ","),
		"hidden=" + strings.Join(hidden, ","), "used=" + strings.Join(used, ","),
		"last_ssh=" + c.LastSSH, "picto_addr=" + c.PictoAddr, "picto_code=" + c.PictoCode, "alarm_on=" + bit(c.AlarmOn),
		"alarm_h=" + strconv.Itoa(c.AlarmH), "alarm_m=" + strconv.Itoa(c.AlarmM),
		"pin=" + c.PIN, "lock_on_wake=" + bit(c.LockOnWake), "duo_link=" + bit(c.LinkOn), "link_audio=" + bit(c.LinkAudio),
		"link_wifi=" + bit(c.LinkWifi), "saver=" + bit(c.Saver),
	}
	if err := os.MkdirAll(filepath.Dir(c.path), 0o755); err != nil {
		return err
	}
	tmp := c.path + ".tmp"
	if err := os.WriteFile(tmp, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, c.path)
}

func (c *Config) pinIndex(key string) int {
	for i, p := range c.Pinned {
		if p == key {
			return i
		}
	}
	return -1
}

func (c *Config) togglePin(key string) bool {
	if i := c.pinIndex(key); i >= 0 {
		c.Pinned = append(c.Pinned[:i], c.Pinned[i+1:]...)
		return false
	}
	c.Pinned = append(c.Pinned, key)
	return true
}

func (c *Config) theme() RGB { return themes[c.Color%len(themes)].C }

func (c *Config) markUsed(key string) { c.Used[key] = time.Now().Unix() }

func (c *Config) isBirthday(t time.Time) bool {
	return int(t.Month()) == c.BirthMonth && t.Day() == c.BirthDay
}
