package main

// The Doctor (Settings > System > Doctor, or `app --doctor [--fix]` over SSH): checks that this
// Cardputer has the system changes Duo relies on and puts in the missing ones, so a new
// Cardputer gets them in one go. Changes to the user's own files are made directly; system
// changes run as root through sudo, all in one script (one password: typed on the device, or at
// sudo's prompt in a terminal). The files it installs live in device/ and are built into Duo.

import (
	"embed"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

//go:embed device/cardputer-home device/home-screen.conf device/fb_load-duo.conf device/90-bluetooth-no-seat.conf device/duo-console.service
var deviceFS embed.FS

// consoleMarker exists while duo-console.service keeps tty1 in graphics mode.
const consoleMarker = "/run/duo-console"

type docState int

const (
	docOK   docState = iota
	docFix           // missing, and the Doctor can put it in
	docInfo          // missing, but not something the Doctor installs
	docNA            // doesn't apply here
)

type docCheck struct {
	Key   string // what --fix and the root script call it
	Name  string
	Why   string // what goes wrong without it
	State docState
	Root  bool         // the fix needs sudo
	fix   func() error // a fix in the user's own files (Root false)
}

func deviceFile(name string) string {
	b, _ := deviceFS.ReadFile("device/" + name)
	return string(b)
}

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

// doctorChecks looks at this Cardputer. Only file checks: quick enough for the UI thread.
func doctorChecks() []*docCheck {
	home := homeDir()
	state := func(ok bool, missing docState) docState {
		if ok {
			return docOK
		}
		return missing
	}
	var cs []*docCheck

	homeScript := filepath.Join(home, ".local/bin/cardputer-home")
	dropIn := filepath.Join(home, ".config/systemd/user/APPLaunch.service.d/home-screen.conf")
	cs = append(cs, &docCheck{Key: "home", Name: "Duo is the home screen",
		Why:   "the stock launcher starts at boot instead of Duo",
		State: state(strings.Contains(readTrim(dropIn), "cardputer-home") && isExecutable(homeScript), docFix),
		fix: func() error {
			if err := writeFileAtomic(homeScript, deviceFile("cardputer-home"), 0o755); err != nil {
				return err
			}
			if err := writeFileAtomic(dropIn, deviceFile("home-screen.conf"), 0o644); err != nil {
				return err
			}
			run(15*time.Second, "systemctl", "--user", "daemon-reload") // used from the next boot
			return nil
		}})

	logo := &docCheck{Key: "bootlogo", Name: "Boot logo lets go", Root: true,
		Why:   "the M5Stack logo can stay over Duo after boot",
		State: state(exists("/etc/systemd/system/fb_load.service.d/duo.conf"), docFix)}
	if !exists("/usr/sbin/fb_load") {
		logo.State = docNA
	}
	cs = append(cs, logo)

	con := &docCheck{Key: "console", Name: "Terminal kept off the screen", Root: true,
		Why:   "the text console can show over Duo, and the screen can't switch fully off",
		State: state(exists(consoleMarker), docFix)}
	if !exists("/dev/tty1") {
		con.State = docNA
	}
	cs = append(cs, con)

	cs = append(cs, &docCheck{Key: "hosts", Name: "Device name in /etc/hosts", Root: true,
		Why:   "every admin action waits ~10 s while the name is looked up",
		State: state(hostnameListed(hostname()), docFix)})

	btConf := filepath.Join(home, ".config/wireplumber/wireplumber.conf.d/90-bluetooth-no-seat.conf")
	bt := &docCheck{Key: "bluetooth", Name: "Bluetooth audio allowed",
		Why:   "music can't play to Bluetooth speakers or a car stereo",
		State: state(exists(btConf), docFix),
		fix: func() error {
			if err := writeFileAtomic(btConf, deviceFile("90-bluetooth-no-seat.conf"), 0o644); err != nil {
				return err
			}
			run(15*time.Second, "systemctl", "--user", "restart", "wireplumber")
			return nil
		}}
	if _, err := exec.LookPath("wireplumber"); err != nil {
		bt.State = docNA
	}
	cs = append(cs, bt)

	cs = append(cs,
		&docCheck{Key: "go", Name: "Go for updates", Why: "Check for updates builds Duo with Go in ~/.local/go",
			State: state(exists(filepath.Join(home, ".local/go/bin/go")), docInfo)},
		&docCheck{Key: "zerocode", Name: "ZeroCode app helper", Why: "uninstalling ZeroCode apps needs ZeroCode's root helper",
			State: state(exists(zcHelper), docInfo)},
		&docCheck{Key: "backlight", Name: "Brightness control", Why: "brightness and screen off need the video group",
			State: state(canWriteFile(filepath.Join(sysRoot, "backlight/backlight/brightness")), docInfo)},
	)
	return cs
}

func docToFix(cs []*docCheck) []*docCheck {
	var out []*docCheck
	for _, c := range cs {
		if c.State == docFix {
			out = append(out, c)
		}
	}
	return out
}

// doctorRootScript installs the system files for the given fixes. Run as root: $@ = fixes.
func doctorRootScript() string {
	heredoc := func(path, body string) string {
		if !strings.HasSuffix(body, "\n") {
			body += "\n"
		}
		return "cat > " + path + ".duo-new <<'DUODOCTOREOF'\n" + body + "DUODOCTOREOF\nmv -f " + path + ".duo-new " + path + "\n"
	}
	return "set -eu\numask 022\nfor fix in \"$@\"; do\ncase \"$fix\" in\n" +
		"bootlogo)\nmkdir -p /etc/systemd/system/fb_load.service.d\n" +
		heredoc("/etc/systemd/system/fb_load.service.d/duo.conf", deviceFile("fb_load-duo.conf")) + ";;\n" +
		"console)\n" + heredoc("/etc/systemd/system/duo-console.service", deviceFile("duo-console.service")) + ";;\n" +
		"esac\ndone\nsystemctl daemon-reload\n" +
		"for fix in \"$@\"; do\ncase \"$fix\" in\n" +
		"console) systemctl enable duo-console.service; systemctl restart duo-console.service ;;\n" +
		"esac\ndone\necho done\n"
}

// doctorFix puts in the given fixes. sudo runs a command as root (with the device password,
// or interactively in a terminal).
func doctorFix(cs []*docCheck, sudo func(args ...string) (string, error)) error {
	var errs []string
	var rootKeys []string
	hosts := false
	for _, c := range cs {
		switch {
		case c.fix != nil:
			if err := c.fix(); err != nil {
				errs = append(errs, c.Name+": "+err.Error())
			}
		case c.Key == "hosts":
			hosts = true
		case c.Root:
			rootKeys = append(rootKeys, c.Key)
		}
	}
	if len(rootKeys) > 0 {
		if out, err := sudo(append([]string{"sh", "-c", doctorRootScript(), "doctor"}, rootKeys...)...); err != nil {
			errs = append(errs, "system files: "+lastError(out, err))
		}
	}
	if hosts {
		if name := hostname(); hostRE.MatchString(name) {
			if out, err := sudo("sh", "-c", hostsScript, "hosts", name, hostsFile); err != nil {
				errs = append(errs, "/etc/hosts: "+lastError(out, err))
			}
		}
	}
	if len(errs) > 0 {
		return errors.New(strings.Join(errs, "; "))
	}
	return nil
}

// ---- the screen ----

func doctorSummary() string {
	n := len(docToFix(doctorChecks()))
	if n == 0 {
		return "ALL OK"
	}
	return fmt.Sprintf("%d TO FIX", n)
}

func doctorPage(a *App, parent *RowList) *RowList {
	l := page("DOCTOR", artSystem)
	l.Refresh = func(a *App, l *RowList) {
		cs := doctorChecks()
		fix := docToFix(cs)
		l.Rows = nil
		if len(fix) > 0 {
			l.Rows = append(l.Rows, &Row{Label: "Fix everything", Kind: rowAction, Value: fmt.Sprintf("%d TO FIX", len(fix)),
				Do: func(a *App, r *Row) { doctorRun(a, l, fix) }})
		}
		for _, c := range cs {
			c := c
			r := &Row{Label: c.Name}
			switch c.State {
			case docOK:
				r.Value = "OK"
			case docNA:
				r.Value = "NOT USED"
			case docInfo:
				r.Value = "MISSING"
				r.Do = func(a *App, r *Row) { alert(a, strings.ToUpper(c.Name), "Missing: "+c.Why+".") }
			case docFix:
				r.Kind, r.Value = rowAction, "FIX"
				if c.Root {
					r.Value = "FIX (SUDO)"
				}
				r.Do = func(a *App, r *Row) {
					confirm(a, "FIX?", "Without it "+c.Why+". Put it in now?", func() { doctorRun(a, l, []*docCheck{c}) })
				}
			}
			l.Rows = append(l.Rows, r)
		}
	}
	l.Refresh(a, l)
	l.Footer = "Fixes marked SUDO ask for your password once"
	l.OnBack = func(a *App) {
		if parent != nil && parent.Refresh != nil {
			parent.Refresh(a, parent)
		}
	}
	return l
}

func doctorRun(a *App, l *RowList, cs []*docCheck) {
	root := false
	for _, c := range cs {
		root = root || c.Root
	}
	do := func(pw string) {
		a.background("Fixing...", func() error {
			return doctorFix(cs, func(args ...string) (string, error) { return runSudo(pw, 90*time.Second, args...) })
		}, func(err error) {
			if err != nil {
				alert(a, "NOT ALL FIXED", err.Error())
			} else {
				a.notify("Fixed. Some changes count from the next boot", false)
			}
			l.Refresh(a, l)
		})
	}
	if root {
		a.withSudo("Needed to change system settings", do)
		return
	}
	do("")
}

// ---- over SSH: app --doctor [--fix] ----

func doctorCLI(fix bool) int {
	show := func(cs []*docCheck) int {
		bad := 0
		for _, c := range cs {
			switch c.State {
			case docOK:
				fmt.Println("ok", c.Name)
			case docNA:
				fmt.Println("..", c.Name+": not used here")
			case docInfo:
				fmt.Println("..", c.Name+": missing ("+c.Why+")")
			case docFix:
				bad++
				how := "fix: app --doctor --fix"
				if c.Root {
					how += " (asks for sudo)"
				}
				fmt.Printf("!! %s: %s; %s\n", c.Name, c.Why, how)
			}
		}
		return bad
	}
	cs := doctorChecks()
	bad := show(cs)
	if !fix || bad == 0 {
		return min(bad, 1)
	}
	fmt.Println("-- fixing")
	err := doctorFix(docToFix(cs), func(args ...string) (string, error) {
		cmd := exec.Command("sudo", args...) // sudo asks for the password at this terminal if it must
		cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
		return "", cmd.Run()
	})
	if err != nil {
		fmt.Println("!!", err)
	}
	fmt.Println("-- now")
	return min(show(doctorChecks()), 1)
}
