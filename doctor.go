package main

// The Doctor (Settings > System > Doctor, or `app --doctor [--fix | --undo KEY]` over SSH):
// checks that this Cardputer has every system change Duo relies on (device/TWEAKS.md), and
// puts in the missing ones, so any Cardputer can be brought to the same working state.
//
// - It checks content, not just existence: installed files are compared with the copies built
//   into Duo (device/) by SHA-256, units must be enabled (and doing their job), tty1's console
//   mode is read, Wi-Fi permissions are asked of NetworkManager, and group membership is
//   compared with what the running session has.
// - Fixes are idempotent and keep a backup (<file>.duo-bak) of anything they replace, so each
//   one can be undone. Changes to the user's own files are made directly; system changes run as
//   root in one script, so one password covers them all (typed on the device and passed to sudo
//   on its input, or at sudo's own prompt in a terminal; never in argv or the log).
// - After fixing it checks again, and says what only counts from the next boot or login.
// - Everything it finds and does goes to ~/.cache/dslauncher/doctor.log, which holds no
//   secrets and can be shared.
//
// It never restarts APPLaunch (that kills the open app), never edits /boot/firmware/cmdline.txt,
// and never reads ~/.zerocode/config.json, *credentials*.json or the saved Wi-Fi secrets.

import (
	"crypto/sha256"
	"dslauncher/cp0"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

//go:embed device/cardputer-home device/home-screen.conf device/fb_load-duo.conf device/90-bluetooth-no-seat.conf device/duo-console.service device/hide-console-cursor.service device/49-cardputer-wifi.rules
var deviceFS embed.FS

const (
	consoleMarker = "/run/duo-console" // exists while duo-console.service keeps tty1 in graphics mode
	logoDropIn    = "/etc/systemd/system/fb_load.service.d/duo.conf"
	consoleUnit   = "/etc/systemd/system/duo-console.service"
	cursorUnit    = "/etc/systemd/system/hide-console-cursor.service"
	wifiRule      = "/etc/polkit-1/rules.d/49-cardputer-wifi.rules"
)

// docGroups are the groups the user needs: video (brightness), gpio (the true black screen),
// input (keyboard), audio, lp (USB label printers). Only groups that exist are asked for.
var docGroups = []string{"video", "gpio", "input", "audio", "lp"}

type docState int

const (
	docOK     docState = iota
	docFix             // missing, and the Doctor can put it in
	docUpdate          // there, but different from Duo's copy (the Doctor can replace it)
	docLater           // in place, but only counts from the next boot or login
	docInfo            // missing, but not something the Doctor installs
	docNA              // doesn't apply here
)

func (s docState) label(later string) string {
	switch s {
	case docOK:
		return "OK"
	case docFix:
		return "FIX"
	case docUpdate:
		return "UPDATE"
	case docLater:
		return "AFTER " + strings.ToUpper(strings.TrimPrefix(later, "next "))
	case docInfo:
		return "MISSING"
	}
	return "NOT USED"
}

type docCheck struct {
	Key     string // what --fix/--undo and the log call it
	Name    string
	What    string // what it does for Duo, in one sentence
	Why     string // what goes wrong without it
	Help    string // what the user can do when the Doctor can't (docInfo)
	Detail  string // what was found
	Section string // "Duo", "System" or "Factory"
	State   docState
	Root    bool   // the fix needs sudo
	Later   string // when a fix counts: "next boot", "next login", "new terminals", or ""
	rootArg string // argument for the root script (Root fixes)
	undoArg string // argument for the root script's undo
	fix     func() error
	undo    func() error
}

func (c *docCheck) fixable() bool { return c.State == docFix || c.State == docUpdate }

func deviceFile(name string) string {
	b, _ := deviceFS.ReadFile("device/" + name)
	return string(b)
}

// ---- file helpers ----

func writeFileAtomic(path, data string, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".duo-new"
	if err := os.WriteFile(tmp, []byte(data), mode); err != nil {
		return err
	}
	os.Chmod(tmp, mode)
	return os.Rename(tmp, path)
}

// putUserFile writes one of the user's files, keeping the first version it replaces as
// <file>.duo-bak (so Undo goes back to how it was before the Doctor).
func putUserFile(path, data string, mode os.FileMode) error {
	if old, err := os.ReadFile(path); err == nil && string(old) != data && !exists(path+".duo-bak") {
		if err := os.WriteFile(path+".duo-bak", old, mode); err != nil {
			return err
		}
	}
	return writeFileAtomic(path, data, mode)
}

// restoreUserFile puts back the backup, or removes the file if the Doctor created it.
func restoreUserFile(path string) error {
	if exists(path + ".duo-bak") {
		return os.Rename(path+".duo-bak", path)
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func isExecutable(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && !fi.IsDir() && fi.Mode()&0o111 != 0
}

func canWriteFile(p string) bool {
	f, err := os.OpenFile(p, os.O_WRONLY, 0)
	if err != nil {
		return false
	}
	f.Close()
	return true
}

type fileCmp int

const (
	fileMissing fileCmp = iota
	fileSame
	fileDiffers
	fileUnreadable
)

func compareFile(path, want string) fileCmp {
	b, err := os.ReadFile(path)
	switch {
	case err == nil && string(b) == want:
		return fileSame
	case err == nil:
		return fileDiffers
	case os.IsNotExist(err):
		return fileMissing
	}
	if exists(path) {
		return fileUnreadable
	}
	return fileMissing
}

func shortSum(s string) string {
	h := sha256.Sum256([]byte(s))
	return fmt.Sprintf("%x", h[:6])
}

// unitEnabled asks systemd whether a unit is enabled ("" if systemd can't be asked).
func unitEnabled(unit string, userUnit bool) string {
	args := []string{"is-enabled", unit}
	if userUnit {
		args = append([]string{"--user"}, args...)
	}
	out, _ := run(5*time.Second, "systemctl", args...)
	return strings.TrimSpace(firstLineOf(out))
}

func firstLineOf(s string) string { l, _, _ := strings.Cut(s, "\n"); return l }

// ---- the user, groups, and the manifest of what the Doctor did ----

var nameRE = regexp.MustCompile(`^[a-z_][a-z0-9_-]{0,31}$`)

// groupState returns the wanted groups that exist here, those the user isn't in (per
// /etc/group), and those the running session doesn't have yet (needs a new login).
func groupState() (wanted, missing, notYet []string) {
	u, err := user.Current()
	if err != nil {
		return nil, nil, nil
	}
	member := map[string]bool{}
	if ids, err := u.GroupIds(); err == nil {
		for _, id := range ids {
			if g, err := user.LookupGroupId(id); err == nil {
				member[g.Name] = true
			}
		}
	}
	proc := map[string]bool{}
	if gids, err := os.Getgroups(); err == nil {
		for _, id := range gids {
			if g, err := user.LookupGroupId(strconv.Itoa(id)); err == nil {
				proc[g.Name] = true
			}
		}
	}
	for _, g := range docGroups {
		if _, err := user.LookupGroup(g); err != nil {
			continue
		}
		wanted = append(wanted, g)
		switch {
		case !member[g]:
			missing = append(missing, g)
		case !proc[g]:
			notYet = append(notYet, g)
		}
	}
	return
}

type docManifest struct {
	Applied map[string]string `json:"applied"`         // key -> when the Doctor put it in
	Groups  []string          `json:"groups"`          // groups the Doctor added the user to
	Asked   string            `json:"asked,omitempty"` // the problems the start-up check last asked about
}

func docManifestPath() string {
	return filepath.Join(filepath.Dir(configPath()), "doctor.json")
}

func loadDocManifest() *docManifest {
	m := &docManifest{Applied: map[string]string{}}
	if b, err := os.ReadFile(docManifestPath()); err == nil {
		json.Unmarshal(b, m)
	}
	if m.Applied == nil {
		m.Applied = map[string]string{}
	}
	return m
}

func (m *docManifest) save() {
	b, _ := json.MarshalIndent(m, "", "  ")
	writeFileAtomic(docManifestPath(), string(b)+"\n", 0o644)
}

// ---- the checks ----

// doctorChecks looks at this Cardputer. It asks systemd, NetworkManager and Go a few
// questions, so run it off the UI thread.
func doctorChecks() []*docCheck {
	home := homeDir()
	onDevice := exists("/etc/systemd")
	me := userName()
	var cs []*docCheck
	add := func(c *docCheck) *docCheck { cs = append(cs, c); return c }

	// 1. Duo is the home screen
	homeScript := filepath.Join(home, ".local/bin/cardputer-home")
	dropIn := filepath.Join(home, ".config/systemd/user/APPLaunch.service.d/home-screen.conf")
	hc := add(&docCheck{Key: "home", Section: "Duo", Name: "Duo is the home screen", Later: "next boot",
		What: "Makes Duo the launcher that starts when the Cardputer turns on, and brings it back if it closes.",
		Why:  "the stock launcher starts at boot instead of Duo",
		fix: func() error {
			if err := putUserFile(homeScript, deviceFile("cardputer-home"), 0o755); err != nil {
				return err
			}
			if err := putUserFile(dropIn, deviceFile("home-screen.conf"), 0o644); err != nil {
				return err
			}
			run(15*time.Second, "systemctl", "--user", "daemon-reload") // used from the next boot
			return nil
		},
		undo: func() error {
			err := errors.Join(restoreUserFile(dropIn), restoreUserFile(homeScript))
			run(15*time.Second, "systemctl", "--user", "daemon-reload")
			return err
		}})
	s, d := compareFile(homeScript, deviceFile("cardputer-home")), compareFile(dropIn, deviceFile("home-screen.conf"))
	switch {
	case s == fileMissing || d == fileMissing:
		hc.State, hc.Detail = docFix, "not installed"
	case s != fileSame || d != fileSame:
		hc.State, hc.Detail = docUpdate, "installed, but different from Duo's copy ("+shortSum(deviceFile("cardputer-home"))+")"
	case !isExecutable(homeScript) && onDevice:
		hc.State, hc.Detail = docFix, "cardputer-home isn't executable"
	default:
		hc.State, hc.Detail = docOK, "cardputer-home "+shortSum(deviceFile("cardputer-home"))+", same as Duo's copy"
		if out, err := run(5*time.Second, "systemctl", "--user", "show", "APPLaunch", "-p", "ExecStart", "--value"); err == nil &&
			!strings.Contains(out, "cardputer-home") {
			hc.State, hc.Detail = docLater, "installed; the launcher picks it up at the next boot"
		}
	}

	// 2. the boot logo lets go of the screen
	lc := add(&docCheck{Key: "bootlogo", Section: "System", Name: "Boot logo lets go", Root: true, Later: "next boot",
		What: "Lets the M5Stack boot logo go away once Duo starts.",
		Why:  "the M5Stack logo can stay over Duo after boot", rootArg: "bootlogo", undoArg: "undo-bootlogo"})
	switch {
	case !exists("/usr/sbin/fb_load"):
		lc.State, lc.Detail = docNA, "no fb_load on this system"
	default:
		switch compareFile(logoDropIn, deviceFile("fb_load-duo.conf")) {
		case fileMissing:
			lc.State, lc.Detail = docFix, "fb_load still waits for the stock launcher (--keep-open)"
		case fileSame:
			lc.State, lc.Detail = docOK, logoDropIn
		default:
			body := readTrim(logoDropIn)
			if strings.Contains(body, "ExecStart=/usr/sbin/fb_load") && !strings.Contains(body, "--keep-open") {
				lc.State, lc.Detail = docOK, logoDropIn+" (worded differently, same effect)"
			} else {
				lc.State, lc.Detail = docUpdate, logoDropIn+" differs from Duo's copy"
			}
		}
	}

	// 3. the console cursor is hidden; 4. the console is kept off the screen
	cc := add(&docCheck{Key: "cursor", Section: "System", Name: "Console cursor hidden", Root: true,
		What: "Hides the text console's blinking cursor, which shows as a black square in the bottom-left corner.",
		Why:  "a blinking cursor can show in the bottom-left corner", rootArg: "cursor", undoArg: "undo-cursor"})
	co := add(&docCheck{Key: "console", Section: "System", Name: "Terminal kept off the screen", Root: true,
		What:    "Stops the text console from drawing over Duo, and lets the screen switch fully off.",
		Why:     "the text console can show over Duo, and the screen can't switch fully off",
		rootArg: "console", undoArg: "undo-console"})
	if !exists("/dev/tty1") {
		cc.State, cc.Detail = docNA, "no console here"
		co.State, co.Detail = docNA, "no console here"
	} else {
		blink := readTrim("/sys/class/graphics/fbcon/cursor_blink")
		switch cmp := compareFile(cursorUnit, deviceFile("hide-console-cursor.service")); {
		case cmp == fileMissing:
			cc.State, cc.Detail = docFix, "hide-console-cursor.service isn't installed"
		case cmp == fileDiffers:
			cc.State, cc.Detail = docUpdate, "hide-console-cursor.service differs from Duo's copy"
		case unitEnabled("hide-console-cursor.service", false) != "enabled":
			cc.State, cc.Detail = docFix, "installed but not enabled"
		case blink != "" && blink != "0":
			cc.State, cc.Detail = docFix, "the cursor still blinks (cursor_blink="+blink+")"
		default:
			cc.State, cc.Detail = docOK, "enabled, cursor_blink="+orDash(blink)
		}
		mode, known := kdMode()
		switch cmp := compareFile(consoleUnit, deviceFile("duo-console.service")); {
		case cmp == fileMissing:
			co.State, co.Detail = docFix, "duo-console.service isn't installed"
		case cmp == fileDiffers:
			co.State, co.Detail = docUpdate, "duo-console.service differs from Duo's copy"
		case unitEnabled("duo-console.service", false) != "enabled":
			co.State, co.Detail = docFix, "installed but not enabled"
		case !exists(consoleMarker) || (known && mode != 1):
			co.State, co.Detail = docFix, fmt.Sprintf("enabled, but tty1 is in text mode (KD mode %d)", mode)
		default:
			co.State, co.Detail = docOK, "tty1 in graphics mode"
			if !known {
				co.Detail = "running (" + consoleMarker + ")"
			}
		}
	}

	// 5. the device name is in /etc/hosts
	name := hostname()
	ho := add(&docCheck{Key: "hosts", Section: "System", Name: "Device name in /etc/hosts", Root: true,
		What:    "Lists this Cardputer's name in /etc/hosts, so admin actions don't stall for about 10 seconds.",
		Why:     "every admin action waits ~10 s while the name is looked up",
		rootArg: "hosts:" + name, undoArg: "undo-hosts"})
	switch {
	case !exists(hostsFile):
		ho.State, ho.Detail = docNA, "no /etc/hosts"
	case hostnameListed(name):
		ho.State, ho.Detail = docOK, name+" is listed"
	case !hostRE.MatchString(name):
		ho.State, ho.Detail = docInfo, "the device name "+strconv.Quote(name)+" can't go in /etc/hosts"
		ho.Help = "Rename the Cardputer in Settings > User > Device name, using only letters, digits and dashes. Then open the Doctor again."
	default:
		ho.State, ho.Detail = docFix, name+" isn't listed (127.0.1.1 names another device)"
	}

	// 6. Bluetooth audio is allowed
	btConf := filepath.Join(home, ".config/wireplumber/wireplumber.conf.d/90-bluetooth-no-seat.conf")
	bt := add(&docCheck{Key: "bluetooth", Section: "Duo", Name: "Bluetooth audio allowed",
		What: "Lets Duo's sound and music play through Bluetooth speakers and car stereos.",
		Why:  "music can't play to Bluetooth speakers or a car stereo",
		fix: func() error {
			if err := putUserFile(btConf, deviceFile("90-bluetooth-no-seat.conf"), 0o644); err != nil {
				return err
			}
			run(15*time.Second, "systemctl", "--user", "restart", "wireplumber")
			return nil
		},
		undo: func() error {
			err := restoreUserFile(btConf)
			run(15*time.Second, "systemctl", "--user", "restart", "wireplumber")
			return err
		}})
	if _, err := exec.LookPath("wireplumber"); err != nil {
		bt.State, bt.Detail = docNA, "no WirePlumber here"
	} else {
		switch compareFile(btConf, deviceFile("90-bluetooth-no-seat.conf")) {
		case fileMissing:
			bt.State, bt.Detail = docFix, "seat monitoring is on, so Bluetooth audio stays off"
		case fileSame:
			bt.State, bt.Detail = docOK, "seat monitoring disabled"
		default:
			bt.State, bt.Detail = docUpdate, "the WirePlumber file differs from Duo's copy"
		}
	}

	// 7. Wi-Fi networks can be added from the launcher
	wf := add(&docCheck{Key: "wifi", Section: "System", Name: "Wi-Fi settings allowed", Root: true,
		What:    "Lets you add and join Wi-Fi networks from the launcher, and Duo Link make its own Wi-Fi.",
		Why:     "adding or joining Wi-Fi networks says \"service unavailable\", and Duo Link can't make its Wi-Fi",
		rootArg: "wifi:" + me, undoArg: "undo-wifi"})
	if _, err := exec.LookPath("nmcli"); err != nil {
		wf.State, wf.Detail = docNA, "no NetworkManager here"
	} else {
		out, _ := run(8*time.Second, "nmcli", "general", "permissions")
		perm := func(action string) string {
			for _, l := range strings.Split(out, "\n") {
				f := strings.Fields(l)
				if len(f) >= 2 && f[0] == action {
					return f[1]
				}
			}
			return "?"
		}
		nc, ms := perm("org.freedesktop.NetworkManager.network-control"), perm("org.freedesktop.NetworkManager.settings.modify.system")
		sh := perm("org.freedesktop.NetworkManager.wifi.share.protected")
		wf.Detail = "network-control " + nc + ", modify.system " + ms + ", wifi.share " + sh
		if nc == "yes" && ms == "yes" && sh == "yes" {
			wf.State = docOK
		} else if !nameRE.MatchString(me) {
			wf.State = docInfo
			wf.Help = "Your user name (" + me + ") can't go in the Wi-Fi rule. Put the rule in by hand: see item 7 of device/TWEAKS.md."
		} else {
			wf.State = docFix
		}
	}

	// 8. the user's groups
	gr := add(&docCheck{Key: "groups", Section: "System", Name: "User groups", Root: true, Later: "next login",
		What: "Gives you access to the brightness, the backlight switch, the keyboard, sound and USB printers.",
		Why:  "brightness, the true black screen, the keyboard, sound or printing can fail"})
	wanted, missing, notYet := groupState()
	switch {
	case !onDevice || len(wanted) == 0:
		gr.State, gr.Detail = docNA, "no such groups here"
	case len(missing) > 0 && nameRE.MatchString(me):
		gr.State, gr.Detail = docFix, me+" isn't in "+strings.Join(missing, ", ")
		gr.rootArg = "groups:" + me + ":" + strings.Join(missing, ",")
	case len(missing) > 0:
		gr.State, gr.Detail = docInfo, me+" isn't in "+strings.Join(missing, ", ")
		gr.Help = "Add yourself over SSH: sudo usermod -aG " + strings.Join(missing, ",") + " " + me + ", then restart the Cardputer."
	case len(notYet) > 0:
		gr.State, gr.Detail = docLater, "added to "+strings.Join(notYet, ", ")+"; counts from the next login"
	default:
		gr.State, gr.Detail = docOK, "in "+strings.Join(wanted, ", ")
	}
	if m := loadDocManifest(); len(m.Groups) > 0 && nameRE.MatchString(me) {
		gr.undoArg = "undo-groups:" + me + ":" + strings.Join(m.Groups, ",")
	}

	// what the Doctor shows but doesn't install
	bl := add(&docCheck{Key: "blackout", Section: "System", Name: "Screen goes fully dark",
		What: "Lets the screen go completely black when it's off, instead of glowing.",
		Why:  "Duo can't reach M5's backlight switch (line 9 of the m5ioe1 chip), so the screen still glows when off"})
	switch st := backlightLineStatus(); st {
	case "held", "in use", "free": // held by this Duo, by the running Duo, or free for it to hold
		bl.State, bl.Detail = docOK, "backlight line "+st
	case "":
		bl.State, bl.Detail = docNA, "no m5ioe1 chip here"
	default:
		bl.State, bl.Detail = docInfo, st
		bl.Help = "Make sure User groups is OK (the gpio group), then restart the Cardputer."
	}
	bk := add(&docCheck{Key: "backlight", Section: "System", Name: "Brightness control",
		What: "Lets Duo change the brightness and turn the screen off.",
		Why:  "brightness and screen off need write access (the video group)"})
	if p := filepath.Join(sysRoot, "backlight/backlight/brightness"); !exists(p) {
		bk.State, bk.Detail = docNA, "no backlight here"
	} else if canWriteFile(p) {
		bk.State, bk.Detail = docOK, "writable"
	} else {
		bk.State, bk.Detail = docInfo, "not writable (see User groups)"
		bk.Help = "Fix User groups (the video group), then restart the Cardputer."
	}

	// 9. factory setup Duo needs (read-only)
	if onDevice {
		uid := strconv.Itoa(os.Getuid())
		udev, _ := filepath.Glob("/etc/udev/rules.d/99-cardputer-zero-*.rules")
		reinstall := "It comes with M5's system (the applaunch package). Reinstall it over SSH: sudo apt install --reinstall applaunch, then restart the Cardputer."
		for _, f := range []struct {
			key, name, what, why, help string
			ok                         bool
		}{
			{"autologin", "Console login for " + me, "Logs " + me + " in on the screen at boot, which starts the launcher.",
				"nothing starts the launcher after boot",
				"Turn on console auto-login over SSH: sudo raspi-config, then System Options > Boot / Auto Login > Console Autologin. Restart the Cardputer.",
				exists("/etc/systemd/system/getty@tty1.service.d/autologin.conf")},
			{"linger", "Launcher runs without a login", "Lets the launcher's user service start at boot.",
				"the launcher's user service doesn't start at boot",
				"Run once over SSH: sudo loginctl enable-linger " + me + ", then restart the Cardputer.",
				exists("/var/lib/systemd/linger/" + me)},
			{"sound", "Sound server", "Plays Duo's sounds and music (PipeWire).", "Duo's sounds and music can't play",
				"Restart it over SSH: systemctl --user restart pipewire pipewire-pulse wireplumber. If it is still missing, restart the Cardputer.",
				exists("/run/user/" + uid + "/pulse/native")},
			{"keyboard", "Keyboard device", "The Cardputer's built-in keyboard, named for Duo.", "keys may not reach Duo", reinstall,
				exists("/dev/input/cardputer-zero-internal")},
			{"udev", "Cardputer device rules", "Names the keyboard and the screen at boot.", "the keyboard and screen may not get their names", reinstall,
				len(udev) > 0},
			{"adb", "USB debugging setup", "ADB over USB (with the USB-SW switch to the LEFT). Duo doesn't need it.",
				"ADB over USB isn't set up", "Only needed for USB debugging. " + reinstall,
				exists("/etc/systemd/system/adbd.service.d/cardputer.conf")},
		} {
			c := &docCheck{Key: f.key, Section: "Factory", Name: f.name, What: f.what, Why: f.why, State: docOK, Detail: "in place"}
			if !f.ok {
				c.State, c.Detail, c.Help = docInfo, "missing (part of M5's system, not something the Doctor installs)", f.help
			}
			cs = append(cs, c)
		}
	}
	order := map[string]int{"Duo": 0, "System": 1, "Factory": 2}
	sort.SliceStable(cs, func(i, j int) bool { return order[cs[i].Section] < order[cs[j].Section] })
	return cs
}

func docToFix(cs []*docCheck) []*docCheck {
	var out []*docCheck
	for _, c := range cs {
		if c.fixable() {
			out = append(out, c)
		}
	}
	return out
}

// ---- the root script ----

// doctorRootScript installs (or undoes) system changes. Run as root with the changes as
// arguments; it prints "ok <key>" or "fail <key>" for each, so one failure doesn't stop the rest.
func doctorRootScript() string {
	put := func(path, body string) string {
		if !strings.HasSuffix(body, "\n") {
			body += "\n"
		}
		return "bak " + path + "\ncat > " + path + ".duo-new <<'DUODOCTOREOF'\n" + body + "DUODOCTOREOF\nchmod 644 " + path +
			".duo-new\nmv -f " + path + ".duo-new " + path + "\n"
	}
	return `set -u
umask 022
# the first version a fix replaces is kept as <file>.duo-bak, for Undo
bak() { if [ -e "$1" ] && [ ! -e "$1.duo-bak" ]; then cp -p "$1" "$1.duo-bak"; fi; }
# undo: put the backup back, or remove a file the Doctor created
restore() { if [ -e "$1.duo-bak" ]; then mv -f "$1.duo-bak" "$1"; else rm -f "$1"; fi; }
okname() { case "$1" in ""|-*|*[!a-z0-9_-]*) echo "bad name: $1" >&2; return 1 ;; esac; }
for arg in "$@"; do
key=${arg%%:*}
# not "if ( ... )": set -e is ignored in a tested command, so a failed step would pass
( set -e
case "$arg" in
bootlogo)
mkdir -p /etc/systemd/system/fb_load.service.d
` + put(logoDropIn, deviceFile("fb_load-duo.conf")) + `systemctl daemon-reload ;;
undo-bootlogo)
restore ` + logoDropIn + `
rmdir /etc/systemd/system/fb_load.service.d 2>/dev/null || true
systemctl daemon-reload ;;
cursor)
` + put(cursorUnit, deviceFile("hide-console-cursor.service")) + `systemctl daemon-reload
systemctl enable hide-console-cursor.service
systemctl restart hide-console-cursor.service ;;
undo-cursor)
systemctl disable --now hide-console-cursor.service 2>/dev/null || true
restore ` + cursorUnit + `
systemctl daemon-reload ;;
console)
` + put(consoleUnit, deviceFile("duo-console.service")) + `systemctl daemon-reload
systemctl enable duo-console.service
systemctl restart duo-console.service ;;
undo-console)
systemctl disable --now duo-console.service 2>/dev/null || true
restore ` + consoleUnit + `
systemctl daemon-reload ;;
wifi:*)
user=${arg#wifi:}; okname "$user"
mkdir -p /etc/polkit-1/rules.d
` + put(wifiRule, deviceFile("49-cardputer-wifi.rules")) + `sed -i "s/subject.user == 'pi'/subject.user == '$user'/" ` + wifiRule + `
systemctl try-restart polkit ;;
undo-wifi)
restore ` + wifiRule + `
systemctl try-restart polkit ;;
hosts:*)
name=${arg#hosts:}
case "$name" in ""|-*|*[!A-Za-z0-9-]*) echo "bad device name" >&2; exit 1 ;; esac
f=/etc/hosts; bak "$f"
tmp=$(mktemp "$f.XXXXXX")
if grep -q '^127\.0\.1\.1[[:space:]]' "$f"; then
  sed "s/^127\.0\.1\.1[[:space:]].*/127.0.1.1\t$name/" "$f" > "$tmp"
else
  cat "$f" > "$tmp"; printf '127.0.1.1\t%s\n' "$name" >> "$tmp"
fi
chmod 644 "$tmp"; mv -f "$tmp" "$f" ;;
undo-hosts)
if [ -e /etc/hosts.duo-bak ]; then mv -f /etc/hosts.duo-bak /etc/hosts; fi ;;
groups:*)
rest=${arg#groups:}; user=${rest%%:*}; okname "$user"
for g in $(echo "${rest#*:}" | tr ',' ' '); do okname "$g"; if getent group "$g" >/dev/null; then usermod -aG "$g" "$user"; fi; done ;;
undo-groups:*)
rest=${arg#undo-groups:}; user=${rest%%:*}; okname "$user"
for g in $(echo "${rest#*:}" | tr ',' ' '); do okname "$g"; gpasswd -d "$user" "$g" >/dev/null 2>&1 || true; done ;;
*) echo "unknown change $arg" >&2; exit 1 ;;
esac
)
if [ $? -eq 0 ]; then echo "ok $key"; else echo "fail $key"; fi
done
`
}

// docResult is what happened to each change asked for.
type docResult struct {
	ok   map[string]bool
	errs []string
}

// doctorFix puts in the given fixes: the user's own files directly, everything else in one
// root script (one sudo). sudo runs a command as root.
func doctorFix(cs []*docCheck, sudo func(args ...string) (string, error)) docResult {
	res := docResult{ok: map[string]bool{}}
	m := loadDocManifest()
	now := time.Now().Format(time.RFC3339)
	var args []string
	byArg := map[string]*docCheck{}
	for _, c := range cs {
		switch {
		case c.fix != nil:
			if err := c.fix(); err != nil {
				res.errs = append(res.errs, c.Name+": "+err.Error())
			} else {
				res.ok[c.Key] = true
				m.Applied[c.Key] = now
			}
		case c.Root && c.rootArg != "":
			args = append(args, c.rootArg)
			byArg[c.rootArg[:strings.IndexAny(c.rootArg+":", ":")]] = c
		}
	}
	if len(args) > 0 {
		out, err := sudo(append([]string{"sh", "-c", doctorRootScript(), "doctor"}, args...)...)
		seen := map[string]bool{}
		for _, l := range strings.Split(out, "\n") {
			f := strings.Fields(l)
			if len(f) == 2 && (f[0] == "ok" || f[0] == "fail") {
				seen[f[1]] = true
				if c := byArg[f[1]]; c != nil {
					if f[0] == "ok" {
						res.ok[c.Key] = true
						m.Applied[c.Key] = now
						if c.Key == "groups" {
							_, added, _ := strings.Cut(strings.TrimPrefix(c.rootArg, "groups:"), ":")
							m.Groups = mergeGroups(m.Groups, strings.Split(added, ","))
						}
					} else {
						res.errs = append(res.errs, c.Name+": failed")
					}
				}
			}
		}
		if err != nil && len(seen) == 0 {
			res.errs = append(res.errs, "system changes: "+lastError(out, err))
		}
	}
	m.save()
	return res
}

func mergeGroups(a, b []string) []string {
	set := map[string]bool{}
	for _, g := range append(a, b...) {
		if g != "" {
			set[g] = true
		}
	}
	var out []string
	for g := range set {
		out = append(out, g)
	}
	sort.Strings(out)
	return out
}

// doctorUndo takes back one change the Doctor made.
func doctorUndo(c *docCheck, sudo func(args ...string) (string, error)) error {
	m := loadDocManifest()
	var err error
	switch {
	case c.undo != nil:
		err = c.undo()
	case c.undoArg != "":
		out, e := sudo("sh", "-c", doctorRootScript(), "doctor", c.undoArg)
		if !strings.Contains(out, "ok ") {
			err = errors.New(lastError(out, e))
		}
	default:
		return errors.New("the Doctor can't undo this")
	}
	if err == nil {
		delete(m.Applied, c.Key)
		if c.Key == "groups" {
			m.Groups = nil
		}
		m.save()
	}
	return err
}

// canUndo: the Doctor made this change (and it can be taken back).
func canUndo(c *docCheck) bool {
	_, did := loadDocManifest().Applied[c.Key]
	return did && (c.undo != nil || c.undoArg != "")
}

// ---- the log ----

func doctorLogPath() string { return filepath.Join(cacheDir(), "doctor.log") }

// doctorLog appends to the shareable log (no secrets: it only ever holds check results and the
// root script's ok/fail lines), keeping the last ~200 KB.
func doctorLog(lines ...string) {
	p := doctorLogPath()
	os.MkdirAll(filepath.Dir(p), 0o755)
	old, _ := os.ReadFile(p)
	if len(old) > 200<<10 {
		old = old[len(old)-150<<10:]
		if i := strings.Index(string(old), "\n== "); i >= 0 {
			old = old[i+1:]
		}
	}
	os.WriteFile(p, append(old, []byte(strings.Join(lines, "\n")+"\n")...), 0o644)
}

func doctorReport(cs []*docCheck) []string {
	out := []string{fmt.Sprintf("== %s  Duo %s  %s  %s", time.Now().Format("2006-01-02 15:04:05"), version, osName(), kernel())}
	for _, c := range cs {
		out = append(out, fmt.Sprintf("%-14s %-10s %s: %s", c.State.label(c.Later), c.Key, c.Name, c.Detail))
	}
	return out
}

// doctorSummaryLine sums up a check run for the log and the screen.
func doctorAfter(before map[string]docState, res docResult, after []*docCheck) (fixed, later, failed []string) {
	for _, c := range after {
		if _, asked := before[c.Key]; !asked {
			continue
		}
		switch {
		case c.State == docOK && c.Later == "":
			fixed = append(fixed, c.Name)
		case c.State == docOK || c.State == docLater:
			later = append(later, c.Name+" ("+c.Later+")")
		case res.ok[c.Key]:
			later = append(later, c.Name+" ("+orDash(c.Later)+")")
		default:
			failed = append(failed, c.Name)
		}
	}
	return
}

// ---- what to do ----

// laterText is what the user does for a change that counts from the next boot or login.
func laterText(later string) string {
	switch later {
	case "next boot":
		return "restart the Cardputer"
	case "next login":
		return "restart the Cardputer (or log out and back in)"
	}
	return ""
}

// todo says in plain words what the user has to do about a check. cli: worded for a terminal.
func (c *docCheck) todo(cli bool) string {
	switch c.State {
	case docOK:
		return "Nothing to do: it's already in place."
	case docFix, docUpdate:
		t := "The Doctor can put this in."
		if c.State == docUpdate {
			t = "The Doctor can replace it with Duo's copy (yours is kept as .duo-bak)."
		}
		if cli {
			t += " Run: app --doctor --fix"
		} else {
			t += " Choose FIX (or Fix everything)."
		}
		if c.Root {
			t += " It asks for your password once."
		}
		if l := laterText(c.Later); l != "" {
			t += " Then " + l + "."
		}
		return t
	case docLater:
		return "Already put in: " + laterText(c.Later) + " to finish."
	case docInfo:
		if c.Help != "" {
			return c.Help
		}
		return "The Doctor can't put this in; see device/TWEAKS.md."
	}
	return "Nothing to do: this Cardputer doesn't need it."
}

func (c *docCheck) status() string {
	switch c.State {
	case docOK:
		return "in place"
	case docFix:
		return "needs fixing"
	case docUpdate:
		return "different from Duo's copy"
	case docLater:
		return "put in, waiting for a restart"
	case docInfo:
		return "missing"
	}
	return "not needed here"
}

// docTally sorts the checks by what the user has to do.
type docTally struct {
	fix, later, manual, ok, na []*docCheck
}

func tally(cs []*docCheck) docTally {
	var t docTally
	for _, c := range cs {
		switch {
		case c.fixable():
			t.fix = append(t.fix, c)
		case c.State == docLater:
			t.later = append(t.later, c)
		case c.State == docInfo:
			t.manual = append(t.manual, c)
		case c.State == docOK:
			t.ok = append(t.ok, c)
		default:
			t.na = append(t.na, c)
		}
	}
	return t
}

func count(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return itoa(n) + " " + many
}

// headline sums up the checks in one line.
func (t docTally) headline() string {
	if len(t.fix)+len(t.later)+len(t.manual) == 0 {
		return "All set: Duo has everything it needs."
	}
	var parts []string
	if n := len(t.fix); n > 0 {
		parts = append(parts, count(n, "fix", "fixes")+" needed")
	}
	if n := len(t.later); n > 0 {
		parts = append(parts, count(n, "waits", "wait")+" for a restart")
	}
	if n := len(t.manual); n > 0 {
		parts = append(parts, count(n, "needs", "need")+" you")
	}
	return strings.Join(parts, ", ") + "."
}

func names(cs []*docCheck) string {
	var out []string
	for _, c := range cs {
		out = append(out, c.Name)
	}
	return strings.Join(out, ", ")
}

// doctorAdvice is the "what to do" text: what the Doctor will fix, what waits for a restart,
// what the user has to do, and what is already in place.
func doctorAdvice(cs []*docCheck, cli bool) []string {
	t := tally(cs)
	out := []string{t.headline()}
	if len(t.fix) > 0 {
		how := "Fix everything does them all"
		if cli {
			how = "app --doctor --fix does them all"
		}
		root := false
		for _, c := range t.fix {
			root = root || c.Root
		}
		if root {
			how += ", with one password"
		}
		out = append(out, "", "For Duo to work properly, these need fixing ("+how+"):")
		for _, c := range t.fix {
			out = append(out, "- "+c.Name+": without it "+c.Why+".")
		}
	}
	if len(t.later) > 0 {
		out = append(out, "", "Already put in; restart the Cardputer to finish:")
		for _, c := range t.later {
			out = append(out, "- "+c.Name+".")
		}
	}
	if len(t.manual) > 0 {
		out = append(out, "", "The Doctor can't do these; here is what to do:")
		for _, c := range t.manual {
			out = append(out, "- "+c.Name+": "+c.todo(cli))
		}
	}
	if len(t.ok) > 0 {
		out = append(out, "", "Already in place, nothing to do ("+itoa(len(t.ok))+"): "+names(t.ok)+".")
	}
	if len(t.na) > 0 {
		out = append(out, "", "Not needed on this Cardputer: "+names(t.na)+".")
	}
	return out
}

// ---- the screen ----

var lastDoctorSummary = "OPEN"

func doctorSummary() string { return lastDoctorSummary }

// summarize is the Doctor row's value in Settings > System.
func summarize(cs []*docCheck) string {
	t := tally(cs)
	switch {
	case len(t.fix) > 0:
		return itoa(len(t.fix)) + " TO FIX"
	case len(t.manual) > 0:
		return itoa(len(t.manual)) + " TO CHECK"
	case len(t.later) > 0:
		return "RESTART"
	}
	return "ALL OK"
}

// DocText is a page of wrapped text (the Doctor's advice and details), scrolled with Up/Down,
// with an optional action on key 4 / Enter.
type DocText struct {
	title string
	paras []string
	act   string
	do    func(a *App)
	top   int
	lines []docLine
}

type docLine struct {
	s    string
	bold bool
}

const (
	docTextTop    = 19
	docTextHeight = 110
	docLineH      = 13 // a line of text
	docGapH       = 6  // a blank line between paragraphs
)

func (l docLine) height() int {
	if l.s == "" {
		return docGapH
	}
	return docLineH
}

// maxTop is the first line from which the rest all fit on the page.
func (v *DocText) maxTop() int {
	lines := v.wrapped()
	room := docTextHeight - 8
	for i := len(lines) - 1; i >= 0; i-- {
		room -= lines[i].height()
		if room < 0 {
			return i + 1
		}
	}
	return 0
}

func (v *DocText) wrapped() []docLine {
	if v.lines == nil {
		for _, p := range v.paras {
			if p == "" {
				v.lines = append(v.lines, docLine{})
				continue
			}
			bold := strings.HasSuffix(p, ":")
			for i, l := range wrap(p, W-30, bold) {
				if i > 0 && strings.HasPrefix(p, "- ") {
					l = "  " + l
				}
				v.lines = append(v.lines, docLine{l, bold})
			}
		}
	}
	return v.lines
}

func (v *DocText) Key(a *App, k cp0.Key) {
	last := v.maxTop()
	switch {
	case k.Back() || k.Slot() == 4:
		a.snd.Play("back")
		a.pop()
	case (k.Name == "ENTER" || k.Slot() == 0) && v.do != nil:
		a.snd.Play("select")
		a.pop()
		v.do(a)
	case k.Name == "UP" || k.Name == "PGUP":
		if v.top > 0 {
			v.top = max(0, v.top-map[bool]int{true: 6, false: 1}[k.Name == "PGUP"])
			a.snd.Play("move")
		}
	case k.Name == "DOWN" || k.Name == "PGDN":
		if v.top < last {
			v.top = min(last, v.top+map[bool]int{true: 6, false: 1}[k.Name == "PGDN"])
			a.snd.Play("move")
		}
	}
}

func (v *DocText) Bar(a *App) []BarItem {
	first := BarItem{}
	if v.do != nil {
		first = BarItem{v.act, artOK, false}
	}
	return []BarItem{first, {}, {}, {}, {"BACK", artBack, false}}
}

func (v *DocText) Draw(a *App) {
	g := a.g
	pageTitle(a, v.title, artSystem)
	x, y, w, h := 6, docTextTop, W-12, docTextHeight
	g.dsDetails(x, y, w, h)
	lines := v.wrapped()
	ly := y + 4
	for i := v.top; i < len(lines) && ly+lines[i].height() <= y+h-4; i++ {
		if i == v.top && lines[i].s == "" {
			continue // no gap at the top
		}
		g.text(x+6, ly, lines[i].s, cText, 1, lines[i].bold)
		ly += lines[i].height()
	}
	if last := v.maxTop(); last > 0 { // a scroll bar
		th := max(10, (h-6)*(len(lines)-last)/len(lines))
		ty := y + 3 + (h-6-th)*v.top/last
		g.rect(x+w-5, y+3, 2, h-6, cGridBG)
		g.rect(x+w-5, ty, 2, th, a.theme())
	}
}

func doctorPage(a *App, parent *RowList) *RowList {
	l := page("DOCTOR", artSystem)
	var cs []*docCheck
	first := true
	fill := func() {
		t := tally(cs)
		l.Rows = []*Row{{Label: "What to do", Kind: rowLink, Value: summarize(cs), Do: func(a *App, r *Row) {
			v := &DocText{title: "DOCTOR: WHAT TO DO", paras: doctorAdvice(cs, false)}
			if len(t.fix) > 0 {
				fix := t.fix
				v.act, v.do = "FIX ALL", func(a *App) { doctorRun(a, l, fix) }
			}
			a.push(v)
		}}}
		if len(t.fix) > 0 {
			fix := t.fix
			l.Rows = append(l.Rows, &Row{Label: "Fix everything", Kind: rowAction, Value: count(len(fix), "FIX", "FIXES"),
				Do: func(a *App, r *Row) { doctorRun(a, l, fix) }})
		}
		section := ""
		for _, c := range cs {
			c := c
			if c.Section != section {
				section = c.Section
				l.Rows = append(l.Rows, &Row{Label: map[string]string{"Duo": "-- Duo --", "System": "-- System --",
					"Factory": "-- From M5's system --"}[section]})
			}
			r := &Row{Label: c.Name, Kind: rowLink, Value: c.State.label(c.Later)}
			if c.State == docOK {
				r.Value = "OK"
			}
			if c.fixable() {
				r.Kind = rowAction
				if c.Root {
					r.Value += " (SUDO)"
				}
			}
			r.Do = func(a *App, r *Row) { doctorDetail(a, l, c) }
			l.Rows = append(l.Rows, r)
		}
		l.Footer = t.headline()
	}
	l.Refresh = func(a *App, l *RowList) {
		a.background("Checking this Cardputer...", func() error {
			cs = doctorChecks()
			return nil
		}, func(error) {
			lastDoctorSummary = summarize(cs)
			doctorLog(doctorReport(cs)...)
			fill()
			if first { // say straight away whether anything is needed
				first = false
				a.notify(tally(cs).headline(), false)
			}
		})
	}
	l.Refresh(a, l)
	l.OnBack = func(a *App) {
		if parent != nil && parent.Refresh != nil {
			parent.Refresh(a, parent)
		}
	}
	return l
}

// doctorDetail explains one check: what it does, what was found, and what to do; with its fix
// or its undo on key 4.
func doctorDetail(a *App, l *RowList, c *docCheck) {
	paras := []string{"Status: " + c.status() + ".", "What to do: " + c.todo(false), "", "What it is: " + c.What,
		"Found: " + c.Detail + "."}
	if c.State != docOK && c.State != docNA && c.Why != "" {
		paras = append(paras, "Without it "+c.Why+".")
	}
	v := &DocText{title: strings.ToUpper(c.Name), paras: paras}
	switch {
	case c.fixable():
		v.act, v.do = "FIX", func(a *App) { doctorRun(a, l, []*docCheck{c}) }
	case c.State == docOK && canUndo(c):
		v.paras = append(v.paras, "", "The Doctor put this in. UNDO takes it back out (the file it replaced comes back).")
		v.act, v.do = "UNDO", func(a *App) { doctorUndoUI(a, l, c) }
	}
	a.snd.Play("modal")
	a.push(v)
}

func doctorUndoUI(a *App, l *RowList, c *docCheck) {
	confirm(a, "UNDO?", "Take out \""+c.Name+"\"? Without it "+c.Why+".", func() {
		do := func(pw string) {
			a.background("Undoing...", func() error {
				return doctorUndo(c, func(args ...string) (string, error) { return runSudo(pw, 90*time.Second, args...) })
			}, func(err error) {
				doctorLog("-- undo "+c.Key+": "+map[bool]string{true: "ok", false: "failed"}[err == nil], errText(err))
				if err != nil {
					alert(a, "NOT UNDONE", err.Error())
				} else {
					a.notify("Undone: "+c.Name, false)
				}
				l.Refresh(a, l)
			})
		}
		if c.Root {
			a.withSudo("Needed to change system settings", do)
		} else {
			do("")
		}
	})
}

func errText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// doctorRun puts in the fixes, checks everything again, and shows what worked, what waits for
// a restart, what didn't, and what (if anything) is still left to do.
func doctorRun(a *App, l *RowList, cs []*docCheck) {
	root := false
	before := map[string]docState{}
	for _, c := range cs {
		root = root || c.Root
		before[c.Key] = c.State
	}
	do := func(pw string) {
		var res docResult
		var after []*docCheck
		a.background("Fixing...", func() error {
			res = doctorFix(cs, func(args ...string) (string, error) { return runSudo(pw, 120*time.Second, args...) })
			after = doctorChecks()
			return nil
		}, func(error) {
			fixed, later, failed := doctorAfter(before, res, after)
			lines := []string{"-- fixed: " + strings.Join(fixed, ", "), "-- counts later: " + strings.Join(later, ", "),
				"-- not fixed: " + strings.Join(failed, ", ")}
			for _, e := range res.errs {
				lines = append(lines, "-- error: "+e)
			}
			doctorLog(append(lines, doctorReport(after)...)...)
			var paras []string
			if len(fixed) > 0 {
				paras = append(paras, "Fixed and checked: "+strings.Join(fixed, ", ")+".", "")
			}
			if len(later) > 0 {
				paras = append(paras, "Put in; restart the Cardputer to finish: "+strings.Join(later, ", ")+".", "")
			}
			if len(failed) > 0 {
				paras = append(paras, "Not fixed: "+strings.Join(failed, ", ")+".")
				for _, e := range res.errs {
					paras = append(paras, "- "+e)
				}
				paras = append(paras, "The log has the details: "+tilde(doctorLogPath())+".", "")
			}
			paras = append(paras, "Now: "+tally(after).headline())
			title := "DOCTOR: FIXED"
			if len(failed) > 0 {
				title = "DOCTOR: NOT ALL FIXED"
				a.snd.Play("error")
			} else {
				a.snd.Play("notice")
			}
			cs = after
			lastDoctorSummary = summarize(after)
			l.Refresh(a, l)
			a.push(&DocText{title: title, paras: paras})
		})
	}
	if root {
		a.withSudo("Needed to change system settings", do)
		return
	}
	do("")
}

// ---- at start-up ----

// doctorAtStart checks the Cardputer once, a little after Duo starts (so the sound server and
// the network are up), so Settings > System shows the real state; when something needs fixing
// or the user's attention it asks once whether to open the Doctor. The same set of problems is
// asked about only once (doctor.json "asked").
func (a *App) doctorAtStart() {
	time.Sleep(20 * time.Second)
	cs := doctorChecks()
	a.work <- func() {
		lastDoctorSummary = summarize(cs)
		doctorLog(append([]string{"-- at start-up"}, doctorReport(cs)...)...)
		t := tally(cs)
		need := append(append([]*docCheck{}, t.fix...), t.manual...)
		if len(need) == 0 {
			return
		}
		var keys []string
		for _, c := range need {
			keys = append(keys, c.Key+"="+itoa(int(c.State)))
		}
		if fp := strings.Join(keys, ","); loadDocManifest().Asked != fp {
			a.doctorAsk, a.doctorAskFP = need, fp
		}
	}
}

// checkDoctorAsk asks about the start-up check's findings once the home screen is showing.
func (a *App) checkDoctorAsk() {
	if a.doctorAsk == nil || a.dimLevel != 0 || a.launch != nil || a.busy() != "" {
		return
	}
	if _, home := a.top().(*Home); !home {
		return
	}
	need := a.doctorAsk
	a.doctorAsk = nil
	m := loadDocManifest()
	m.Asked = a.doctorAskFP
	m.save()
	list := names(need[:min(len(need), 3)])
	if len(need) > 3 {
		list += " and " + itoa(len(need)-3) + " more"
	}
	confirm(a, "DOCTOR", "For Duo to work properly, "+count(len(need), "thing needs", "things need")+
		" attention: "+list+". Open the Doctor to see what to do?", func() {
		a.push(doctorPage(a, nil))
	})
}

// ---- over SSH: app --doctor [--fix | --undo KEY] ----

func doctorCLI(args []string) int {
	fix := len(args) >= 1 && args[0] == "--fix"
	sudo := func(args ...string) (string, error) {
		cmd := exec.Command("sudo", args...) // sudo asks for the password at this terminal if it must
		cmd.Stdin, cmd.Stderr = os.Stdin, os.Stderr
		out, err := cmd.Output()
		return string(out), err
	}
	show := func(cs []*docCheck) int {
		section := ""
		for _, c := range cs {
			if c.Section != section {
				section = c.Section
				fmt.Println("--", section)
			}
			mark := ".."
			switch {
			case c.State == docOK:
				mark = "ok"
			case c.fixable():
				mark = "!!"
			case c.State == docLater:
				mark = "~~"
			case c.State == docInfo:
				mark = "??"
			}
			fmt.Printf("%s %-10s %s: %s\n", mark, c.Key, c.Name, c.Detail)
		}
		fmt.Println()
		for _, l := range doctorAdvice(cs, true) {
			fmt.Println(l)
		}
		return len(tally(cs).fix)
	}
	if len(args) >= 2 && args[0] == "--undo" {
		for _, c := range doctorChecks() {
			if c.Key == args[1] {
				if !canUndo(c) {
					fmt.Println("!! the Doctor didn't put", c.Key, "in, so it won't undo it")
					return 1
				}
				err := doctorUndo(c, sudo)
				doctorLog("-- undo "+c.Key+" (terminal): "+map[bool]string{true: "ok", false: "failed"}[err == nil], errText(err))
				if err != nil {
					fmt.Println("!!", err)
					return 1
				}
				fmt.Println("ok undone:", c.Name)
				return 0
			}
		}
		fmt.Println("!! no such check:", args[1])
		return 1
	}
	cs := doctorChecks()
	doctorLog(doctorReport(cs)...)
	bad := show(cs)
	if !fix || bad == 0 {
		fmt.Println("\nlog:", doctorLogPath())
		return min(bad, 1)
	}
	before := map[string]docState{}
	for _, c := range docToFix(cs) {
		before[c.Key] = c.State
	}
	fmt.Println("\n-- fixing")
	res := doctorFix(docToFix(cs), sudo)
	for _, e := range res.errs {
		fmt.Println("!!", e)
	}
	after := doctorChecks()
	fixed, later, failed := doctorAfter(before, res, after)
	doctorLog(append([]string{"-- fixed (terminal): " + strings.Join(fixed, ", "), "-- counts later: " + strings.Join(later, ", "),
		"-- not fixed: " + strings.Join(failed, ", ")}, doctorReport(after)...)...)
	if len(fixed) > 0 {
		fmt.Println("fixed and checked:", strings.Join(fixed, ", "))
	}
	if len(later) > 0 {
		fmt.Println("put in; restart the Cardputer to finish:", strings.Join(later, ", "))
	}
	if len(failed) > 0 {
		fmt.Println("not fixed:", strings.Join(failed, ", "))
	}
	fmt.Println("\n-- checked again")
	bad = show(after)
	fmt.Println("\nlog:", doctorLogPath())
	return min(bad, 1)
}
