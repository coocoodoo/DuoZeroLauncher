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
	Why     string // what goes wrong without it
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
	Applied map[string]string `json:"applied"` // key -> when the Doctor put it in
	Groups  []string          `json:"groups"`  // groups the Doctor added the user to
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
		Why: "the stock launcher starts at boot instead of Duo",
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
		Why: "the M5Stack logo can stay over Duo after boot", rootArg: "bootlogo", undoArg: "undo-bootlogo"})
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
		Why: "a blinking cursor can show in the bottom-left corner", rootArg: "cursor", undoArg: "undo-cursor"})
	co := add(&docCheck{Key: "console", Section: "System", Name: "Terminal kept off the screen", Root: true,
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
		Why:     "every admin action waits ~10 s while the name is looked up",
		rootArg: "hosts:" + name, undoArg: "undo-hosts"})
	switch {
	case !exists(hostsFile):
		ho.State, ho.Detail = docNA, "no /etc/hosts"
	case hostnameListed(name):
		ho.State, ho.Detail = docOK, name+" is listed"
	case !hostRE.MatchString(name):
		ho.State, ho.Detail = docInfo, "the device name "+strconv.Quote(name)+" can't go in /etc/hosts"
	default:
		ho.State, ho.Detail = docFix, name+" isn't listed (127.0.1.1 names another device)"
	}

	// 6. Bluetooth audio is allowed
	btConf := filepath.Join(home, ".config/wireplumber/wireplumber.conf.d/90-bluetooth-no-seat.conf")
	bt := add(&docCheck{Key: "bluetooth", Section: "Duo", Name: "Bluetooth audio allowed",
		Why: "music can't play to Bluetooth speakers or a car stereo",
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
		Why:     "adding or joining Wi-Fi networks says \"service unavailable\"",
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
		wf.Detail = "network-control " + nc + ", modify.system " + ms
		if nc == "yes" && ms == "yes" {
			wf.State = docOK
		} else if !nameRE.MatchString(me) {
			wf.State = docInfo
		} else {
			wf.State = docFix
		}
	}

	// 8. the user's groups
	gr := add(&docCheck{Key: "groups", Section: "System", Name: "User groups", Root: true, Later: "next login",
		Why: "brightness, the true black screen, the keyboard, sound or printing can fail"})
	wanted, missing, notYet := groupState()
	switch {
	case !onDevice || len(wanted) == 0:
		gr.State, gr.Detail = docNA, "no such groups here"
	case len(missing) > 0 && nameRE.MatchString(me):
		gr.State, gr.Detail = docFix, me+" isn't in "+strings.Join(missing, ", ")
		gr.rootArg = "groups:" + me + ":" + strings.Join(missing, ",")
	case len(missing) > 0:
		gr.State, gr.Detail = docInfo, me+" isn't in "+strings.Join(missing, ", ")
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
		Why: "Duo can't reach M5's backlight switch (line 9 of the m5ioe1 chip), so the screen still glows when off"})
	switch st := backlightLineStatus(); st {
	case "held", "in use", "free": // held by this Duo, by the running Duo, or free for it to hold
		bl.State, bl.Detail = docOK, "backlight line "+st
	case "":
		bl.State, bl.Detail = docNA, "no m5ioe1 chip here"
	default:
		bl.State, bl.Detail = docInfo, st
	}
	bk := add(&docCheck{Key: "backlight", Section: "System", Name: "Brightness control",
		Why: "brightness and screen off need write access (the video group)"})
	if p := filepath.Join(sysRoot, "backlight/backlight/brightness"); !exists(p) {
		bk.State, bk.Detail = docNA, "no backlight here"
	} else if canWriteFile(p) {
		bk.State, bk.Detail = docOK, "writable"
	} else {
		bk.State, bk.Detail = docInfo, "not writable (see User groups)"
	}

	// 9. factory setup Duo needs (read-only)
	if onDevice {
		uid := strconv.Itoa(os.Getuid())
		udev, _ := filepath.Glob("/etc/udev/rules.d/99-cardputer-zero-*.rules")
		for _, f := range []struct {
			key, name, why string
			ok             bool
		}{
			{"autologin", "Console login for " + me, "nothing starts the launcher after boot", exists("/etc/systemd/system/getty@tty1.service.d/autologin.conf")},
			{"linger", "Launcher runs without a login", "the launcher's user service doesn't start at boot", exists("/var/lib/systemd/linger/" + me)},
			{"sound", "Sound server", "Duo's sounds and music can't play", exists("/run/user/" + uid + "/pulse/native")},
			{"keyboard", "Keyboard device", "keys may not reach Duo", exists("/dev/input/cardputer-zero-internal")},
			{"udev", "Cardputer device rules", "the keyboard and screen may not get their names", len(udev) > 0},
			{"adb", "USB debugging setup", "ADB over USB isn't set up (USB-SW to the LEFT)", exists("/etc/systemd/system/adbd.service.d/cardputer.conf")},
		} {
			c := &docCheck{Key: f.key, Section: "Factory", Name: f.name, Why: f.why, State: docOK, Detail: "in place"}
			if !f.ok {
				c.State, c.Detail = docInfo, "missing (part of M5's image, not something the Doctor installs)"
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

// ---- the screen ----

var lastDoctorSummary = "OPEN"

func doctorSummary() string { return lastDoctorSummary }

func summarize(cs []*docCheck) string {
	if n := len(docToFix(cs)); n > 0 {
		return fmt.Sprintf("%d TO FIX", n)
	}
	return "ALL OK"
}

func doctorPage(a *App, parent *RowList) *RowList {
	l := page("DOCTOR", artSystem)
	var cs []*docCheck
	fill := func() {
		l.Rows = nil
		fix := docToFix(cs)
		if len(fix) > 0 {
			l.Rows = append(l.Rows, &Row{Label: "Fix everything", Kind: rowAction, Value: fmt.Sprintf("%d TO FIX", len(fix)),
				Do: func(a *App, r *Row) { doctorRun(a, l, fix) }})
		}
		section := ""
		for _, c := range cs {
			c := c
			if c.Section != section {
				section = c.Section
				l.Rows = append(l.Rows, &Row{Label: map[string]string{"Duo": "-- Duo --", "System": "-- System --",
					"Factory": "-- From M5's image --"}[section]})
			}
			r := &Row{Label: c.Name, Value: c.State.label(c.Later)}
			if c.Root && c.fixable() {
				r.Value += " (SUDO)"
			}
			if c.fixable() {
				r.Kind = rowAction
			}
			r.Do = func(a *App, r *Row) { doctorDetail(a, l, c) }
			l.Rows = append(l.Rows, r)
		}
	}
	l.Refresh = func(a *App, l *RowList) {
		a.background("Checking this Cardputer...", func() error {
			cs = doctorChecks()
			return nil
		}, func(error) {
			lastDoctorSummary = summarize(cs)
			doctorLog(doctorReport(cs)...)
			fill()
		})
	}
	l.Refresh(a, l)
	l.Footer = "Log: " + tilde(doctorLogPath())
	l.OnBack = func(a *App) {
		if parent != nil && parent.Refresh != nil {
			parent.Refresh(a, parent)
		}
	}
	return l
}

// doctorDetail explains a check and offers its fix or its undo.
func doctorDetail(a *App, l *RowList, c *docCheck) {
	msg := c.Detail + "."
	if c.State != docOK && c.State != docNA {
		msg += " Without it " + c.Why + "."
	}
	switch {
	case c.fixable():
		when := ""
		if c.Later != "" {
			when = " It counts from the " + c.Later + "."
		}
		verb := "Put it in now?"
		if c.State == docUpdate {
			verb = "Replace it with Duo's copy (the old one is kept)?"
		}
		confirm(a, strings.ToUpper(c.Name), msg+when+" "+verb, func() { doctorRun(a, l, []*docCheck{c}) })
	case c.State == docOK && canUndo(c):
		confirm(a, "UNDO?", msg+" The Doctor put this in. Undo it?", func() {
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
	default:
		alert(a, strings.ToUpper(c.Name), msg)
	}
}

func errText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

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
			var msg []string
			if len(fixed) > 0 {
				msg = append(msg, "Fixed: "+strings.Join(fixed, ", ")+".")
			}
			if len(later) > 0 {
				msg = append(msg, "Counts later: "+strings.Join(later, ", ")+".")
			}
			if len(failed) > 0 {
				msg = append(msg, "Not fixed: "+strings.Join(failed, ", ")+". See the log.")
			}
			title := "FIXED"
			if len(failed) > 0 {
				title = "NOT ALL FIXED"
			}
			alert(a, title, strings.Join(msg, " "))
			cs = after
			lastDoctorSummary = summarize(after)
			l.Refresh(a, l)
		})
	}
	if root {
		a.withSudo("Needed to change system settings", do)
		return
	}
	do("")
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
		bad, section := 0, ""
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
				mark, bad = "!!", bad+1
			case c.State == docLater:
				mark = "~~"
			}
			fmt.Printf("%s %-12s %s: %s\n", mark, c.Key, c.Name, c.Detail)
		}
		return bad
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
		if bad > 0 {
			fmt.Println("fix with: app --doctor --fix (asks for sudo once)")
		}
		fmt.Println("log:", doctorLogPath())
		return min(bad, 1)
	}
	before := map[string]docState{}
	for _, c := range docToFix(cs) {
		before[c.Key] = c.State
	}
	fmt.Println("-- fixing")
	res := doctorFix(docToFix(cs), sudo)
	for _, e := range res.errs {
		fmt.Println("!!", e)
	}
	after := doctorChecks()
	fixed, later, failed := doctorAfter(before, res, after)
	doctorLog(append([]string{"-- fixed (terminal): " + strings.Join(fixed, ", "), "-- counts later: " + strings.Join(later, ", "),
		"-- not fixed: " + strings.Join(failed, ", ")}, doctorReport(after)...)...)
	fmt.Println("-- now")
	bad = show(after)
	if len(later) > 0 {
		fmt.Println("counts later:", strings.Join(later, ", "))
	}
	fmt.Println("log:", doctorLogPath())
	return min(bad, 1)
}
