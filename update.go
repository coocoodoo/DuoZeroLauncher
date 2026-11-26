package main

// Self-update from GitHub: Settings > Launcher > "Check for updates" reads the version in the
// repo's main.go, and if it is newer downloads the source, builds it on the device with the Go
// toolchain, checks the new binary runs, then swaps it in (keeping the old one as app.prev and
// the old source as src.prev) and restarts. "Undo last update" swaps them back. Only if Duo's
// folder isn't writable does it ask for the account password (sudo).
//
// From a shell: `app --update-check` downloads and builds the latest without installing it.

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	updateRepo   = "coocoodoo/DuoZeroLauncher"
	updateBranch = "main"
)

var versionRe = regexp.MustCompile(`const version = "([0-9]+(?:\.[0-9]+)*)"`)

// appDir is the folder Duo runs from (~/apps/duo-launcher), found at startup.
var appDir = func() string {
	self, err := os.Executable()
	if err != nil {
		return ""
	}
	if r, err := filepath.EvalSymlinks(self); err == nil {
		self = r
	}
	return filepath.Dir(self)
}()

// newerVersion reports whether a is a later version than b ("0.10.0" > "0.9.3").
func newerVersion(a, b string) bool {
	pa, pb := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < max(len(pa), len(pb)); i++ {
		var x, y int
		if i < len(pa) {
			x, _ = strconv.Atoi(pa[i])
		}
		if i < len(pb) {
			y, _ = strconv.Atoi(pb[i])
		}
		if x != y {
			return x > y
		}
	}
	return false
}

var shaRe = regexp.MustCompile(`^[0-9a-f]{40}$`)

// updateRef is what the last check found: the newest commit on the branch, or the branch's
// name when GitHub's API couldn't be asked. The download uses the same, so it builds exactly
// the version the check saw.
var (
	updateRefMu sync.Mutex
	updateRef   = updateBranch
)

// latestCommit asks GitHub's API for the newest commit on the branch. GitHub's raw-file server
// caches branch URLs for minutes (a push to main went unseen while it served the old main.go);
// a commit's files never change, so reading them by commit is always current. "" if the API
// can't be reached (it allows 60 unauthenticated requests an hour).
func latestCommit() string {
	out, err := run(15*time.Second, "curl", "-fsSL", "--max-time", "12", "-H", "Accept: application/vnd.github.sha",
		"https://api.github.com/repos/"+updateRepo+"/commits/"+updateBranch)
	if sha := strings.TrimSpace(out); err == nil && shaRe.MatchString(sha) {
		return sha
	}
	return ""
}

// updateURLs are where to read main.go and download the source for a ref (a commit or a branch).
func updateURLs(ref string) (mainGo, tarball string) {
	mainGo = "https://raw.githubusercontent.com/" + updateRepo + "/" + ref + "/main.go"
	tarball = "https://codeload.github.com/" + updateRepo + "/tar.gz/" + ref
	if !shaRe.MatchString(ref) {
		tarball = "https://codeload.github.com/" + updateRepo + "/tar.gz/refs/heads/" + ref
	}
	return
}

// latestVersion reads the version from main.go on GitHub, at the newest commit.
func latestVersion() (string, error) {
	ref := latestCommit()
	if ref == "" {
		ref = updateBranch
	}
	updateRefMu.Lock()
	updateRef = ref
	updateRefMu.Unlock()
	url, _ := updateURLs(ref)
	out, err := run(25*time.Second, "curl", "-fsSL", "--max-time", "20", url)
	if err != nil {
		return "", errors.New("can't reach GitHub")
	}
	m := versionRe.FindStringSubmatch(out)
	if m == nil {
		return "", errors.New("no version found on GitHub")
	}
	return m[1], nil
}

func goTool() string {
	home, _ := os.UserHomeDir()
	if p := filepath.Join(home, ".local", "go", "bin", "go"); fileExists(p) {
		return p
	}
	if p, err := exec.LookPath("go"); err == nil {
		return p
	}
	return ""
}

func fileExists(p string) bool { _, err := os.Stat(p); return err == nil }

// progressFn reports a stage message ("" keeps the current one) and overall progress 0..1.
type progressFn func(msg string, p float64)

func (f progressFn) report(msg string, p float64) {
	if f != nil {
		f(msg, p)
	}
}

// whileRunning calls tick every 250 ms until done is closed.
func whileRunning(done <-chan struct{}, tick func()) {
	t := time.NewTicker(250 * time.Millisecond)
	defer t.Stop()
	for {
		select {
		case <-done:
			return
		case <-t.C:
			tick()
		}
	}
}

// buildSecondsFile remembers how long the last on-device build took, for the progress bar.
func buildSecondsFile() string { return filepath.Join(cacheDir(), "update-build-seconds") }

func expectedBuild() time.Duration {
	if b, err := os.ReadFile(buildSecondsFile()); err == nil {
		if s, err := strconv.Atoi(strings.TrimSpace(string(b))); err == nil && s >= 10 && s < 3600 {
			return time.Duration(s) * time.Second
		}
	}
	return 2 * time.Minute
}

func mmss(d time.Duration) string {
	s := int(d.Round(time.Second) / time.Second)
	return fmt.Sprintf("%d:%02d", s/60, s%60)
}

// buildUpdate downloads the repo and builds it into <work>/app.new; it returns <work>.
// Progress: download 2-20%, unpack 22%, build 24-90% (timed against the last build), check 92%.
func buildUpdate(want string, prog progressFn) (string, error) {
	gt := goTool()
	if gt == "" {
		return "", errors.New("Go is not installed (~/.local/go)")
	}
	work := filepath.Join(cacheDir(), "update")
	os.RemoveAll(work)
	src, tmp := filepath.Join(work, "src"), filepath.Join(work, "tmp")
	if err := os.MkdirAll(src, 0o755); err != nil {
		return "", err
	}
	os.MkdirAll(tmp, 0o755)
	tgz := filepath.Join(work, "src.tar.gz")
	updateRefMu.Lock()
	_, url := updateURLs(updateRef)
	updateRefMu.Unlock()
	prog.report("Downloading Duo Launcher "+want+"...", 0.02)
	done := make(chan struct{})
	go whileRunning(done, func() {
		if st, err := os.Stat(tgz); err == nil {
			prog.report("", 0.02+0.18*min(float64(st.Size())/700e3, 1)) // the source is ~650 KB
		}
	})
	_, err := run(3*time.Minute, "curl", "-fsSL", "--max-time", "170", "-o", tgz, url)
	close(done)
	if err != nil {
		return "", errors.New("download failed")
	}
	prog.report("Unpacking...", 0.22)
	if out, err := run(time.Minute, "tar", "-xzf", tgz, "-C", src, "--strip-components=1"); err != nil {
		return "", fmt.Errorf("unpack failed: %s", briefLine(out))
	}
	if b, err := os.ReadFile(filepath.Join(src, "main.go")); err != nil || !strings.Contains(string(b), `const version = "`+want+`"`) {
		return "", errors.New("downloaded source is not version " + want)
	}
	cmd := exec.Command(gt, "build", "-p", "1", "-trimpath", "-ldflags=-s -w", "-o", filepath.Join(work, "app.new"), ".")
	cmd.Dir = src
	cmd.Env = append(os.Environ(), "GOTOOLCHAIN=local", "CGO_ENABLED=0", "TMPDIR="+tmp, "GOTMPDIR="+tmp)
	expect, start := expectedBuild(), time.Now()
	report := func() {
		el := time.Since(start)
		msg := "Building Duo Launcher " + want + "... almost done"
		if left := expect - el; left > 5*time.Second {
			msg = "Building Duo Launcher " + want + "... about " + mmss(left) + " left"
		}
		prog.report(msg, 0.24+0.66*min(float64(el)/float64(expect), 0.97))
	}
	report()
	done = make(chan struct{})
	go whileRunning(done, report)
	out, err := runCmd(20*time.Minute, cmd)
	close(done)
	os.WriteFile(filepath.Join(work, "build.log"), []byte(out), 0o644)
	if err != nil {
		return "", fmt.Errorf("build failed: %s", briefLine(out))
	}
	os.WriteFile(buildSecondsFile(), []byte(strconv.Itoa(int(time.Since(start).Seconds()+0.5))), 0o644)
	prog.report("Checking the new build...", 0.92)
	// Check the new binary runs. Use --sounds, which every Duo version has and which only writes
	// a file (older builds don't know --version and would start a whole launcher instead).
	wav := filepath.Join(tmp, "check.wav")
	if _, err := run(30*time.Second, filepath.Join(work, "app.new"), "--sounds", wav); err != nil {
		return "", errors.New("the new build didn't start")
	}
	if st, err := os.Stat(wav); err != nil || st.Size() < 1000 {
		return "", errors.New("the new build didn't start")
	}
	os.Remove(wav)
	return work, nil
}

func runCmd(timeout time.Duration, cmd *exec.Cmd) (string, error) {
	var b strings.Builder
	cmd.Stdout, cmd.Stderr = &b, &b
	if err := cmd.Start(); err != nil {
		return "", err
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		return b.String(), err
	case <-time.After(timeout):
		cmd.Process.Kill()
		return b.String(), errors.New("timed out")
	}
}

func briefLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if len(s) > 80 {
		s = s[:80]
	}
	return s
}

// installScript swaps in the new build and source. $1 = Duo's folder, $2 = the update folder.
// Files keep the folder owner, so it works the same with or without sudo.
const installScript = `set -e
d="$1"; w="$2"; o=$(stat -c %u:%g "$d")
cp -p "$d/app" "$d/app.prev"
cp "$w/app.new" "$d/app.new"
chmod 755 "$d/app.new"
mv -f "$d/app.new" "$d/app"
[ -f "$w/src/pkg/app.conf" ] && cp "$w/src/pkg/app.conf" "$d/app.conf"
if [ -d "$d/src" ]; then rm -rf "$d/src.prev"; mv "$d/src" "$d/src.prev"; fi
cp -a "$w/src" "$d/src"
chown -R "$o" "$d/app" "$d/app.prev" "$d/src" 2>/dev/null || true
[ -f "$d/app.conf" ] && chown "$o" "$d/app.conf" 2>/dev/null || true
sync`

// undoScript swaps app/app.prev and src/src.prev back. $1 = Duo's folder.
const undoScript = `set -e
d="$1"
[ -f "$d/app.prev" ] || { echo "no previous version"; exit 1; }
mv -f "$d/app" "$d/app.swap"; mv -f "$d/app.prev" "$d/app"; mv -f "$d/app.swap" "$d/app.prev"
if [ -d "$d/src.prev" ]; then mv "$d/src" "$d/src.swap"; mv "$d/src.prev" "$d/src"; mv "$d/src.swap" "$d/src.prev"; fi
[ -f "$d/src/pkg/app.conf" ] && cp "$d/src/pkg/app.conf" "$d/app.conf"
sync`

// canWrite reports whether Duo can change its own folder without sudo.
func canWrite(dir string) bool {
	f, err := os.CreateTemp(dir, ".write-test-*")
	if err != nil {
		return false
	}
	f.Close()
	os.Remove(f.Name())
	return true
}

// runPrivileged runs a script on Duo's folder, asking for the password only if it must.
func (a *App) runPrivileged(why, busy, script string, args []string, done func(error)) {
	if canWrite(appDir) {
		a.background(busy, func() error {
			out, err := run(2*time.Minute, "sh", append([]string{"-c", script, "sh"}, args...)...)
			if err != nil {
				return errors.New(briefLine(out))
			}
			return nil
		}, done)
		return
	}
	a.withSudo(why, func(pw string) {
		a.background(busy, func() error {
			out, err := runSudo(pw, 2*time.Minute, append([]string{"sh", "-c", script, "sh"}, args...)...)
			if err != nil {
				return errors.New(briefLine(out))
			}
			return nil
		}, done)
	})
}

// checkForUpdates is the Settings action.
func checkForUpdates(a *App) {
	var latest string
	a.background("Checking GitHub for updates...", func() error {
		v, err := latestVersion()
		latest = v
		return err
	}, func(err error) {
		switch {
		case err != nil:
			a.notify(err.Error(), true)
		case !newerVersion(latest, version):
			a.notify("Duo Launcher "+version+" is up to date", false)
		default:
			confirm(a, "UPDATE?", fmt.Sprintf("Install Duo Launcher %s (you have %s)? It downloads and builds on the device, which takes a few minutes, then restarts.", latest, version), func() {
				startUpdate(a, latest)
			})
		}
	})
}

func startUpdate(a *App, want string) {
	var work string
	a.background("Downloading Duo Launcher "+want+"...", func() error {
		w, err := buildUpdate(want, a.setProgress)
		work = w
		return err
	}, func(err error) {
		if err != nil {
			a.notify("Update failed: "+err.Error(), true)
			return
		}
		a.runPrivileged("Install the Duo Launcher update", "Installing Duo Launcher "+want+"...",
			installScript, []string{appDir, work}, func(err error) {
				if err != nil {
					a.notify("Install failed: "+err.Error(), true)
					return
				}
				a.notify("Updated to "+want+", restarting", false)
				a.draw()
				time.Sleep(1200 * time.Millisecond)
				restartSelf(a)
			})
		a.setProgress("", 0.96) // installing (no bar if it first asks for the password)
	})
}

// undoUpdate is the Settings action that puts the previous version back.
func undoUpdate(a *App) {
	confirm(a, "UNDO UPDATE?", "Go back to the previous Duo Launcher version and restart.", func() {
		a.runPrivileged("Undo the Duo Launcher update", "Restoring the previous version...",
			undoScript, []string{appDir}, func(err error) {
				if err != nil {
					a.notify("Undo failed: "+err.Error(), true)
					return
				}
				a.notify("Previous version restored, restarting", false)
				a.draw()
				time.Sleep(1200 * time.Millisecond)
				restartSelf(a)
			})
	})
}

// updateCheckCLI is `app --update-check`: download and build the latest, don't install.
func updateCheckCLI() int {
	v, err := latestVersion()
	if err != nil {
		fmt.Println("check failed:", err)
		return 1
	}
	fmt.Printf("installed %s, GitHub %s, newer: %v\n", version, v, newerVersion(v, version))
	w, err := buildUpdate(v, func(msg string, p float64) {
		if msg != "" {
			fmt.Printf("%3.0f%% %s\n", p*100, msg)
		}
	})
	if err != nil {
		fmt.Println("build failed:", err)
		return 1
	}
	fmt.Println("built", filepath.Join(w, "app.new"), "(not installed)")
	return 0
}
