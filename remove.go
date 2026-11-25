package main

// Uninstalling apps, and putting them back.
//
// Each launcher entry is one of:
//   - a ZeroCode app: zc-<slug>.desktop, written by the root helper /usr/local/sbin/zc-app-root
//     from an app folder in ~/apps (the user may run that helper with sudo and no password).
//     Uninstalling asks the helper to remove the entry, and the folder moves into the bin.
//   - a package app: a dpkg package owns the .desktop file (or the program it runs), as with the
//     Store's apps. Uninstalling first rebuilds a copy of the installed package into the bin
//     (repackScript, as root), then runs `apt-get remove` (settings kept, nothing auto-removed).
//   - built in, Duo itself, or part of the system: never uninstalled; HIDE puts those away.
//
// Every uninstall leaves a record in the bin (removedDir) until it is restored or deleted for
// good from Settings > Launcher > Removed apps. A record says "removing" until the work is done,
// so one left half-way (power cut) is put right the next time the bin is read (reconcile).

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

var zcHelper = "/usr/local/sbin/zc-app-root" // a var so tests can point it elsewhere

const (
	kindZC  = "zerocode"
	kindPkg = "package"
)

// removeMu keeps uninstalls, restores and bin clean-ups one at a time.
var removeMu sync.Mutex

// protectedPkgs are never removed, not even as a side effect of removing an app: the system
// launcher and what Duo, uninstalling, Wi-Fi, sound and remote logins rely on. Packages marked
// essential, required or important are refused as well.
var protectedPkgs = map[string]bool{
	"applaunch": true, "apt": true, "dpkg": true, "sudo": true, "systemd": true, "bash": true,
	"coreutils": true, "network-manager": true, "openssh-server": true, "openssh-client": true,
	"bluez": true, "pipewire": true, "wireplumber": true, "pulseaudio-utils": true,
	"python3": true, "fakeroot": true, "tar": true, "gzip": true,
}

var slugRE = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,31}$`)
var pkgRE = regexp.MustCompile(`^[a-z0-9][a-z0-9+.-]+$`)

func homeDir() string { h, _ := os.UserHomeDir(); return h }

// appsRoot is where ZeroCode app folders live (zc-app-root's APPS_ROOT).
func appsRoot() string { return filepath.Join(homeDir(), "apps") }

// removedDir is the bin: one folder per uninstalled app.
func removedDir() string {
	if d := os.Getenv("DSL_DATA"); d != "" {
		return filepath.Join(d, "removed")
	}
	return filepath.Join(homeDir(), ".local", "share", "dslauncher", "removed")
}

// zcSlug is the ZeroCode slug of a launcher entry, if it is one.
func zcSlug(key string) (string, bool) {
	s, ok := strings.CutPrefix(key, "zc-")
	if !ok {
		return "", false
	}
	s, ok = strings.CutSuffix(s, ".desktop")
	return s, ok && slugRE.MatchString(s)
}

// zcFolder parses the folder out of a ZeroCode entry's "Made with ZeroCode (<folder>)" comment.
func zcFolder(comment string) string {
	rest, ok := strings.CutPrefix(comment, "Made with ZeroCode (")
	if !ok {
		return ""
	}
	dir, ok := strings.CutSuffix(strings.TrimSpace(rest), ")")
	if !ok || !filepath.IsAbs(dir) {
		return ""
	}
	return filepath.Clean(dir)
}

// inside reports whether p is strictly inside root (not root itself).
func inside(p, root string) bool {
	rel, err := filepath.Rel(root, p)
	return err == nil && rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) &&
		!filepath.IsAbs(rel)
}

func isDir(p string) bool { fi, err := os.Lstat(p); return err == nil && fi.IsDir() }

func exists(p string) bool { _, err := os.Lstat(p); return err == nil }

func dirSize(p string) int64 {
	var n int64
	filepath.WalkDir(p, func(_ string, d fs.DirEntry, err error) error {
		if err == nil && d.Type().IsRegular() {
			if fi, err := d.Info(); err == nil {
				n += fi.Size()
			}
		}
		return nil
	})
	return n
}

func humanBytes(n int64) string {
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.1f GB", float64(n)/(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%d KB", n>>10)
	}
	return fmt.Sprintf("%d B", n)
}

// tilde shortens a path in the home folder for display.
func tilde(p string) string {
	if h := homeDir(); h != "" && (p == h || inside(p, h)) {
		return "~" + strings.TrimPrefix(p, h)
	}
	return p
}

// runC runs a command with the C locale, so its output can be parsed.
func runC(timeout time.Duration, name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Env = append(os.Environ(), "LC_ALL=C")
	if t := os.Getenv("TMPDIR"); t != "" && !isDir(t) {
		cmd.Env = append(cmd.Env, "TMPDIR=/var/tmp") // dpkg-deb needs a temp folder that exists
	}
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	err := cmd.Run()
	if ctx.Err() != nil {
		err = errors.New("timed out")
	}
	return strings.TrimSpace(out.String()), err
}

// lastError picks the line of a tool's output that explains a failure: apt's last "E:" line,
// the helper's "zc-app:" line, else the last line.
func lastError(out string, err error) string {
	lines := strings.Split(strings.TrimSpace(out), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		l := strings.TrimSpace(lines[i])
		if s, ok := strings.CutPrefix(l, "E: "); ok {
			return s
		}
		if s, ok := strings.CutPrefix(l, "zc-app: "); ok {
			return s
		}
	}
	for i := len(lines) - 1; i >= 0; i-- {
		if l := strings.TrimSpace(lines[i]); l != "" {
			return briefLine(l)
		}
	}
	if err != nil {
		return err.Error()
	}
	return "failed"
}

// ---- the ZeroCode helper ----

// errNeedPassword: sudo wants the admin password for the helper; ask, then try again.
var errNeedPassword = errors.New("needs the admin password")

// zcRun runs the helper through sudo: without a password when pw is "" (the usual NOPASSWD rule;
// errNeedPassword if there is none). sudo can take ~10 s when the hostname doesn't resolve.
func zcRun(pw string, args ...string) (string, error) {
	if !exists(zcHelper) {
		return "", errors.New("the ZeroCode helper " + zcHelper + " is missing")
	}
	if pw == "" {
		out, err := run(90*time.Second, "sudo", append([]string{"-n", zcHelper}, args...)...)
		if err != nil && strings.Contains(out, "password is required") {
			return out, errNeedPassword
		}
		return out, err
	}
	return runSudo(pw, 90*time.Second, append([]string{zcHelper}, args...)...)
}

// ---- packages ----

// parseOwner reads `dpkg-query -S <path>` output: "pkg[:arch][, pkg2]: <path>".
func parseOwner(out, path string) string {
	for _, l := range strings.Split(out, "\n") {
		if strings.HasPrefix(l, "diversion ") {
			continue
		}
		pk, f, ok := strings.Cut(l, ": ")
		if !ok || strings.TrimSpace(f) != path {
			continue
		}
		pk, _, _ = strings.Cut(pk, ",")
		pk, _, _ = strings.Cut(strings.TrimSpace(pk), ":")
		if pkgRE.MatchString(pk) {
			return pk
		}
	}
	return ""
}

func pkgOwner(path string) string {
	out, err := runC(15*time.Second, "dpkg-query", "-S", path)
	if err != nil {
		return ""
	}
	return parseOwner(out, path)
}

type pkgState struct {
	Name, Version, Priority string
	Essential, Installed    bool
	SizeKB                  int64
}

// pkgStates asks dpkg about packages; unknown ones are left out.
func pkgStates(names ...string) map[string]pkgState {
	m := map[string]pkgState{}
	if len(names) == 0 {
		return m
	}
	args := append([]string{"-W", "-f=${Package}|${Version}|${Priority}|${Essential}|${db:Status-Abbrev}|${Installed-Size}\n"}, names...)
	out, _ := runC(15*time.Second, "dpkg-query", args...)
	return parseStates(out)
}

func parseStates(out string) map[string]pkgState {
	m := map[string]pkgState{}
	for _, l := range strings.Split(out, "\n") {
		f := strings.Split(strings.TrimSpace(l), "|")
		if len(f) < 6 || f[0] == "" {
			continue
		}
		kb, _ := strconv.ParseInt(f[5], 10, 64)
		m[f[0]] = pkgState{Name: f[0], Version: f[1], Priority: f[2], Essential: f[3] == "yes",
			Installed: strings.HasPrefix(f[4], "ii") || strings.HasPrefix(f[4], "iU") || strings.HasPrefix(f[4], "iF"),
			SizeKB:    kb}
	}
	return m
}

func pkgInstalled(name string) bool { return pkgStates(name)[name].Installed }

func protectedPkg(s pkgState) bool {
	return protectedPkgs[s.Name] || s.Essential || s.Priority == "required" || s.Priority == "important"
}

// dpkgPkg is an installed package as dpkg's database describes it.
type dpkgPkg struct {
	name     string
	provides []string
	deps     [][]string // Depends and Pre-Depends: clauses of alternatives
}

// tab-separated: dependency alternatives use "|"
const dpkgDBFormat = "-f=${db:Status-Abbrev}\t${Package}\t${Provides}\t${Depends}\t${Pre-Depends}\n"

// parseDpkgDB reads `dpkg-query -W` output in dpkgDBFormat; packages not installed (or only
// their settings left) are skipped.
func parseDpkgDB(out string) map[string]*dpkgPkg {
	db := map[string]*dpkgPkg{}
	for _, l := range strings.Split(out, "\n") {
		f := strings.SplitN(l, "\t", 5)
		if len(f) < 5 || len(f[0]) < 2 || f[1] == "" {
			continue
		}
		if c := f[0][1]; c == 'n' || c == 'c' {
			continue
		}
		p := &dpkgPkg{name: f[1], deps: append(depClauses(f[3]), depClauses(f[4])...)}
		for _, c := range depClauses(f[2]) {
			p.provides = append(p.provides, c...)
		}
		db[p.name] = p
	}
	return db
}

// depClauses splits "a (>= 1), b | c:any" into [[a] [b c]], dropping versions and arches.
func depClauses(s string) [][]string {
	var out [][]string
	for _, c := range strings.Split(s, ",") {
		var alts []string
		for _, a := range strings.Split(c, "|") {
			a = strings.TrimSpace(a)
			if i := strings.IndexAny(a, " ("); i >= 0 {
				a = a[:i]
			}
			if a, _, _ = strings.Cut(a, ":"); a != "" {
				alts = append(alts, a)
			}
		}
		if len(alts) > 0 {
			out = append(out, alts)
		}
	}
	return out
}

// dependents is what `apt-get remove pkg` takes with it: pkg, then every installed package
// that needs it (or something only it provides), and so on. It reads dpkg's database in a
// fraction of a second, where apt needs 10-20 s here; the uninstall asks apt to confirm it.
func dependents(db map[string]*dpkgPkg, pkg string) []string {
	gone := map[string]bool{pkg: true}
	order := []string{pkg}
	providers := map[string][]string{}
	var names []string
	for n, p := range db {
		names = append(names, n)
		for _, v := range p.provides {
			providers[v] = append(providers[v], n)
		}
	}
	sort.Strings(names)
	stays := func(name string) bool {
		if _, ok := db[name]; ok && !gone[name] {
			return true
		}
		for _, pr := range providers[name] {
			if !gone[pr] {
				return true
			}
		}
		return false
	}
	leaves := func(name string) bool {
		if gone[name] {
			return true
		}
		for _, pr := range providers[name] {
			if gone[pr] {
				return true
			}
		}
		return false
	}
	for changed := true; changed; {
		changed = false
		for _, n := range names {
			if gone[n] {
				continue
			}
			for _, clause := range db[n].deps {
				hit, ok := false, false
				for _, alt := range clause {
					hit = hit || leaves(alt)
					ok = ok || stays(alt)
				}
				if hit && !ok {
					gone[n], changed = true, true
					order = append(order, n)
					break
				}
			}
		}
	}
	return order
}

// parseSim reads `apt-get -s` output: which packages it would remove and install.
func parseSim(out string) (remove, install []string) {
	for _, l := range strings.Split(out, "\n") {
		f := strings.Fields(l)
		if len(f) < 2 {
			continue
		}
		name, _, _ := strings.Cut(f[1], ":")
		switch f[0] {
		case "Remv", "Purg":
			remove = append(remove, name)
		case "Inst":
			install = append(install, name)
		}
	}
	return remove, install
}

// ---- planning ----

// removal is a checked plan to uninstall one launcher entry.
type removal struct {
	e        *Entry
	kind     string
	slug     string   // ZeroCode
	folder   string   // ZeroCode: the app folder ("" if it is gone)
	pkg      string   // package that owns the entry
	pkgs     []string // every package apt will remove, pkg first
	versions map[string]string
	bytes    int64
	needPW   bool
	pinned   bool
	noCopy   bool // uninstall a package without keeping a copy (the user agreed)
	warn     string
}

func (r *removal) id() string {
	if r.kind == kindZC {
		return r.slug
	}
	return r.pkg
}

// planRemoval checks what uninstalling e would do. It only reads; it runs off the UI thread.
func planRemoval(e *Entry) (*removal, error) {
	if e.Builtin != "" {
		return nil, errors.New("Built-in apps stay. HIDE puts one away")
	}
	desktop := filepath.Join(launchRoot(), "applications", e.Key)
	if slug, ok := zcSlug(e.Key); ok {
		if slug == "duo-launcher" {
			return nil, errors.New("Duo Launcher can't uninstall itself")
		}
		r := &removal{e: e, kind: kindZC, slug: slug}
		dir := e.Origin
		if dir == "" {
			dir = filepath.Join(appsRoot(), slug)
		}
		if !inside(dir, appsRoot()) {
			return nil, errors.New("Its folder is outside ~/apps")
		}
		if isDir(dir) {
			r.folder, r.bytes = dir, dirSize(dir)
		}
		if slug == "tiny-terminal" {
			r.warn = "CLI, Python and SSH need it."
		}
		return r, nil
	}
	pkg := pkgOwner(desktop)
	if pkg == "" {
		if args := splitExec(e.Exec); len(args) > 0 && filepath.IsAbs(args[0]) {
			pkg = pkgOwner(args[0])
		}
	}
	if pkg == "" {
		return nil, errors.New(e.Name + " isn't from a package. HIDE it instead")
	}
	r := &removal{e: e, kind: kindPkg, pkg: pkg, needPW: true, versions: map[string]string{}}
	if st := pkgStates(pkg)[pkg]; protectedPkg(st) {
		return nil, errors.New(e.Name + " is part of the system. HIDE it instead")
	}
	out, err := runC(30*time.Second, "dpkg-query", "-W", dpkgDBFormat)
	if err != nil {
		return nil, errors.New("dpkg: " + lastError(out, err))
	}
	db := parseDpkgDB(out)
	if db[pkg] == nil {
		return nil, errors.New(pkg + " isn't installed")
	}
	r.pkgs = dependents(db, pkg)
	states := pkgStates(r.pkgs...)
	for _, p := range r.pkgs {
		st, ok := states[p]
		if !ok || !st.Installed {
			if p == pkg {
				return nil, errors.New(pkg + " isn't installed")
			}
			continue
		}
		if protectedPkg(st) {
			return nil, errors.New("It would also remove " + p + ", which the system needs")
		}
		r.versions[p] = st.Version
		r.bytes += st.SizeKB << 10
	}
	return r, nil
}

func (r *removal) message() string {
	if r.kind == kindZC {
		if r.folder == "" {
			return r.e.Name + " leaves the launcher. Its folder is already gone, so this can't be undone."
		}
		m := fmt.Sprintf("%s (%s) moves to Removed apps; Settings > Launcher can put it back.", r.e.Name, humanBytes(r.bytes))
		if r.warn != "" {
			m += " " + r.warn
		}
		return m
	}
	size := ""
	if r.bytes > 0 {
		size = " (" + humanBytes(r.bytes) + ")"
	}
	m := fmt.Sprintf("Removes package %s%s. A copy is kept in Removed apps to put it back.", r.pkg, size)
	if len(r.pkgs) > 1 {
		m += " Also removes " + strings.Join(r.pkgs[1:], ", ") + "."
	}
	return m
}

// ---- the bin ----

type pkgCopy struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	Deb     string `json:"deb,omitempty"` // file in the record's folder; "" when no copy was kept
}

type removedApp struct {
	ID       string    `json:"id"`
	Kind     string    `json:"kind"`
	Name     string    `json:"name"`
	Key      string    `json:"key"`
	Slug     string    `json:"slug,omitempty"`
	Folder   string    `json:"folder,omitempty"`
	Packages []pkgCopy `json:"packages,omitempty"`
	Pinned   bool      `json:"pinned,omitempty"`
	Bytes    int64     `json:"bytes"`
	Removed  int64     `json:"removed"`
	State    string    `json:"state,omitempty"` // "removing" until the uninstall finished
}

func (rec *removedApp) dir() string { return filepath.Join(removedDir(), rec.ID) }

func (rec *removedApp) kindName() string {
	switch rec.Kind {
	case kindZC:
		return "ZeroCode app"
	case kindPkg:
		return "Package"
	}
	return "Unknown"
}

// canRestore says whether the bin holds what's needed to put the app back.
func (rec *removedApp) canRestore() bool {
	switch rec.Kind {
	case kindZC:
		return isDir(filepath.Join(rec.dir(), "app"))
	case kindPkg:
		return len(rec.Packages) > 0 && rec.Packages[0].Deb != "" && exists(filepath.Join(rec.dir(), rec.Packages[0].Deb))
	}
	return false
}

func writeRecord(rec *removedApp) error {
	b, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return err
	}
	p := filepath.Join(rec.dir(), "record.json")
	if err := os.WriteFile(p+".tmp", append(b, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(p+".tmp", p)
}

var idSafe = regexp.MustCompile(`[^a-z0-9.+-]`)

func newRecordID(name string) string {
	base := time.Now().Format("20060102-150405") + "-" + idSafe.ReplaceAllString(strings.ToLower(name), "_")
	id := base
	for i := 2; exists(filepath.Join(removedDir(), id)); i++ {
		id = fmt.Sprintf("%s-%d", base, i)
	}
	return id
}

// removedCount says how many apps wait in the bin, for the Launcher page.
func removedCount() string {
	ents, _ := os.ReadDir(removedDir())
	n := 0
	for _, d := range ents {
		if d.IsDir() && !strings.HasPrefix(d.Name(), ".") {
			n++
		}
	}
	switch n {
	case 0:
		return "none"
	case 1:
		return "1 app"
	}
	return fmt.Sprintf("%d apps", n)
}

// loadRemoved lists the bin, newest first, after putting right any uninstall that was cut short.
func loadRemoved() []*removedApp {
	ents, _ := os.ReadDir(removedDir())
	var out []*removedApp
	for _, d := range ents {
		if !d.IsDir() || strings.HasPrefix(d.Name(), ".") {
			continue
		}
		rec := &removedApp{}
		b, err := os.ReadFile(filepath.Join(removedDir(), d.Name(), "record.json"))
		if err != nil || json.Unmarshal(b, rec) != nil {
			rec = &removedApp{Name: d.Name(), Kind: "unknown"}
			if fi, err := d.Info(); err == nil {
				rec.Removed = fi.ModTime().Unix()
			}
		}
		rec.ID = d.Name()
		if rec.State != "" && !reconcile(rec) {
			continue
		}
		out = append(out, rec)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Removed > out[j].Removed })
	return out
}

func (rec *removedApp) desktop() string {
	return filepath.Join(launchRoot(), "applications", "zc-"+rec.Slug+".desktop")
}

// reconcile puts right a record whose uninstall or restore stopped half-way (Duo was killed or
// the power went). If the app is installed after all, its record is dropped and false returned;
// otherwise the record (with the app's files) stays in the bin.
func reconcile(rec *removedApp) bool {
	if !removeMu.TryLock() {
		return true // an uninstall is running right now; leave its record alone
	}
	defer removeMu.Unlock()
	installed := false
	switch rec.Kind {
	case kindZC:
		src := filepath.Join(rec.dir(), "app")
		okFolder := rec.Folder != "" && inside(rec.Folder, appsRoot())
		installed = exists(rec.desktop())
		switch {
		case installed && isDir(src):
			// the uninstall stopped after moving the folder: move it back
			if !okFolder || exists(rec.Folder) || os.Rename(src, rec.Folder) != nil {
				return true // keep the record: the app's files are in it
			}
		case !installed && !isDir(src) && okFolder && isDir(rec.Folder):
			// the restore stopped after moving the folder out: back into the bin
			if os.Rename(rec.Folder, src) != nil {
				return true
			}
		}
	case kindPkg:
		installed = len(rec.Packages) > 0 && pkgInstalled(rec.Packages[0].Name)
	}
	if installed {
		os.Remove(filepath.Join(rec.dir(), "record.json"))
		os.RemoveAll(rec.dir())
		return false
	}
	rec.State = ""
	writeRecord(rec)
	return true
}

// ---- uninstalling ----

// noCopyError: the package's copy couldn't be made; the user may choose to go on without it.
type noCopyError struct{ why string }

func (e *noCopyError) Error() string { return "couldn't keep a copy: " + e.why }

// errKept: the uninstall failed but the app's files are safe in the bin.
type errKept struct{ msg string }

func (e *errKept) Error() string { return e.msg }

func (r *removal) do(a *App, pw string) (*removedApp, error) {
	if !removeMu.TryLock() {
		return nil, errors.New("Another uninstall is still running")
	}
	defer removeMu.Unlock()
	rec := &removedApp{ID: newRecordID(r.id()), Kind: r.kind, Name: r.e.Name, Key: r.e.Key, Slug: r.slug,
		Folder: r.folder, Pinned: r.pinned, Bytes: r.bytes, Removed: time.Now().Unix(), State: "removing"}
	if r.kind == kindZC && r.folder == "" {
		rec.Folder = filepath.Join(appsRoot(), r.slug)
	}
	if err := os.MkdirAll(rec.dir(), 0o700); err != nil {
		return nil, err
	}
	if err := writeRecord(rec); err != nil {
		os.RemoveAll(rec.dir())
		return nil, err
	}
	var err error
	if r.kind == kindZC {
		err = r.removeZC(rec, pw)
	} else {
		err = r.removePkg(a, rec, pw)
	}
	var kept *errKept
	if err != nil && !errors.As(err, &kept) {
		os.RemoveAll(rec.dir())
		return nil, err
	}
	rec.State = ""
	writeRecord(rec)
	key := r.e.Key
	a.work <- func() {
		a.cfg.forget(key)
		a.save()
		a.appsChanged()
	}
	return rec, err
}

func (r *removal) removeZC(rec *removedApp, pw string) error {
	moved := filepath.Join(rec.dir(), "app")
	if r.folder != "" {
		if err := os.Rename(r.folder, moved); err != nil {
			return fmt.Errorf("can't move %s: %v", tilde(r.folder), errors.Unwrap(err))
		}
	}
	out, err := zcRun(pw, "remove", r.slug)
	if err == nil {
		return nil
	}
	msg := lastError(out, err)
	if r.folder != "" {
		if rerr := os.Rename(moved, r.folder); rerr != nil {
			return &errKept{msg + "; its folder is in Removed apps"}
		}
	}
	if errors.Is(err, errNeedPassword) {
		return err
	}
	return errors.New(msg)
}

// aptAgrees asks apt (slow: 10-20 s) what removing the package really takes, and refuses when
// that isn't exactly what the user was told.
func (r *removal) aptAgrees() error {
	out, err := runC(3*time.Minute, "apt-get", "-s", "-q", "-o", "APT::Get::AutomaticRemove=false", "remove", r.pkg)
	if err != nil {
		return errors.New("apt: " + lastError(out, err))
	}
	rem, inst := parseSim(out)
	if len(inst) > 0 {
		return errors.New("apt would also install " + strings.Join(inst, ", ") + ". Nothing was changed")
	}
	want := map[string]bool{}
	for _, p := range r.pkgs {
		want[p] = true
	}
	var extra []string
	for _, p := range rem {
		if !want[p] {
			extra = append(extra, p)
		}
		delete(want, p)
	}
	if len(extra) > 0 || len(want) > 0 {
		return errors.New("apt would remove " + strings.Join(rem, ", ") + ". Nothing was changed")
	}
	return nil
}

func (r *removal) removePkg(a *App, rec *removedApp, pw string) error {
	a.setProgress("Checking with apt...", 0.03)
	if err := r.aptAgrees(); err != nil {
		return err
	}
	if r.noCopy {
		for _, p := range r.pkgs {
			rec.Packages = append(rec.Packages, pkgCopy{Name: p, Version: r.versions[p]})
		}
	} else {
		a.setProgress("Keeping a copy of "+r.e.Name+"...", 0.1)
		debs, err := repack(pw, rec.dir(), r.pkgs)
		if err != nil {
			return &noCopyError{err.Error()}
		}
		rec.Packages = debs
	}
	if err := writeRecord(rec); err != nil {
		return err
	}
	a.setProgress("Uninstalling "+r.e.Name+"...", 0.55)
	args := append([]string{"env", "DEBIAN_FRONTEND=noninteractive", "LC_ALL=C", "apt-get", "-y", "-q",
		"-o", "APT::Get::AutomaticRemove=false", "remove"}, r.pkgs...)
	out, err := runSudo(pw, 10*time.Minute, args...)
	if pkgInstalled(r.pkg) {
		return errors.New("apt: " + lastError(out, err))
	}
	a.setProgress("", 0.97)
	if err != nil {
		return &errKept{"Uninstalled, but apt said: " + lastError(out, err)}
	}
	return nil
}

// repackScript rebuilds .deb files of installed packages from dpkg's database, like
// dpkg-repack. Run as root: $1 = output folder, $2 = owner (uid:gid) for the files, then the
// package names. Prints "deb <package> <version> <file>" per package.
const repackScript = `set -eu
umask 022
TMPDIR=/var/tmp; export TMPDIR
out=$1; owner=$2; shift 2
info=/var/lib/dpkg/info
for pkg in "$@"; do
  base=$info/$pkg
  if [ ! -f "$base.list" ]; then
    l=$(ls "$info/$pkg":*.list 2>/dev/null | head -n 1)
    [ -n "$l" ] || { echo "no file list for $pkg"; exit 1; }
    base=${l%.list}
  fi
  ver=$(dpkg-query -W -f='${Version}' "$pkg")
  arch=$(dpkg-query -W -f='${Architecture}' "$pkg")
  st=$(mktemp -d /var/tmp/duo-repack.XXXXXX)
  trap 'rm -rf "$st"' EXIT
  mkdir "$st/DEBIAN"
  chmod 755 "$st" "$st/DEBIAN"
  dpkg-query -s "$pkg" | awk '/^[^ \t]/ { skip = ($1 == "Status:" || $1 == "Config-Version:" || $1 == "Conffiles:") } !skip' > "$st/DEBIAN/control"
  while IFS= read -r f; do
    case "$f" in ""|/.) continue ;; esac
    if [ -L "$f" ] || [ -f "$f" ]; then
      mkdir -p "$st$(dirname "$f")"
      cp -a "$f" "$st$f"
    elif [ -d "$f" ]; then
      mkdir -p "$st$f"
      chmod --reference="$f" "$st$f"
      chown --reference="$f" "$st$f"
    fi
  done < "$base.list"
  for s in preinst postinst prerm postrm config templates triggers shlibs symbols md5sums; do
    if [ -f "$base.$s" ]; then cp -p "$base.$s" "$st/DEBIAN/$s"; fi
  done
  if [ -f "$base.conffiles" ]; then
    while read -r line; do
      f=${line##* }
      if [ -e "$st$f" ]; then echo "$line"; fi
    done < "$base.conffiles" > "$st/DEBIAN/conffiles"
    if [ ! -s "$st/DEBIAN/conffiles" ]; then rm -f "$st/DEBIAN/conffiles"; fi
  fi
  name=$(printf '%s_%s_%s.deb' "$pkg" "$ver" "$arch" | tr ':/' '__')
  dpkg-deb -Zgzip -z1 --build "$st" "$out/$name.part" >/dev/null
  chown "$owner" "$out/$name.part"
  mv -f "$out/$name.part" "$out/$name"
  rm -rf "$st"
  trap - EXIT
  echo "deb $pkg $ver $name"
done
`

// repack keeps a copy of each package in dir and checks each one is the package it should be.
func repack(pw, dir string, pkgs []string) ([]pkgCopy, error) {
	if _, err := exec.LookPath("dpkg-deb"); err != nil {
		return nil, errors.New("dpkg-deb is missing")
	}
	owner := fmt.Sprintf("%d:%d", os.Getuid(), os.Getgid())
	out, err := runSudo(pw, 10*time.Minute, append([]string{"sh", "-c", repackScript, "repack", dir, owner}, pkgs...)...)
	if err != nil {
		return nil, errors.New(lastError(out, err))
	}
	var copies []pkgCopy
	for _, l := range strings.Split(out, "\n") {
		f := strings.Fields(l)
		if len(f) == 4 && f[0] == "deb" {
			copies = append(copies, pkgCopy{Name: f[1], Version: f[2], Deb: f[3]})
		}
	}
	if len(copies) != len(pkgs) {
		return nil, errors.New("the copy is incomplete")
	}
	for _, c := range copies {
		got, err := runC(20*time.Second, "dpkg-deb", "-f", filepath.Join(dir, c.Deb), "Package", "Version")
		if err != nil || !strings.Contains(got, "Package: "+c.Name) || !strings.Contains(got, "Version: "+c.Version) {
			return nil, errors.New("the copy of " + c.Name + " doesn't check out")
		}
	}
	return copies, nil
}

// ---- restoring and deleting ----

var errNoCopy = errors.New("no copy was kept; get it again from the Store")

func (rec *removedApp) restore(pw string) error {
	if !removeMu.TryLock() {
		return errors.New("Another uninstall is still running")
	}
	defer removeMu.Unlock()
	// "restoring" until it is back, so a restore cut short is put right by reconcile
	rec.State = "restoring"
	if err := writeRecord(rec); err != nil {
		return err
	}
	done := false
	defer func() {
		if !done {
			rec.State = ""
			writeRecord(rec)
		}
	}()
	switch rec.Kind {
	case kindZC:
		src, dst := filepath.Join(rec.dir(), "app"), rec.Folder
		switch {
		case !isDir(src):
			return errors.New("its folder is missing from the bin")
		case dst == "" || !inside(dst, appsRoot()):
			return errors.New("its folder was outside ~/apps")
		case exists(dst):
			return errors.New(tilde(dst) + " exists again; move it first")
		case exists(rec.desktop()):
			return errors.New("an app named " + rec.Slug + " is installed again")
		}
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		if err := os.Rename(src, dst); err != nil {
			return err
		}
		if out, err := zcRun(pw, "install", dst); err != nil {
			os.Rename(dst, src)
			if errors.Is(err, errNeedPassword) {
				return err
			}
			return errors.New(lastError(out, err))
		}
	case kindPkg:
		if len(rec.Packages) == 0 {
			return errNoCopy
		}
		if pkgInstalled(rec.Packages[0].Name) {
			return errors.New(rec.Packages[0].Name + " is installed again")
		}
		var debs []string
		for _, c := range rec.Packages {
			if c.Deb != "" && exists(filepath.Join(rec.dir(), c.Deb)) {
				debs = append(debs, filepath.Join(rec.dir(), c.Deb))
			}
		}
		if len(debs) == 0 {
			return errNoCopy
		}
		args := append([]string{"env", "DEBIAN_FRONTEND=noninteractive", "LC_ALL=C", "apt-get", "-y", "-q", "install"}, debs...)
		out, err := runSudo(pw, 10*time.Minute, args...)
		if !pkgInstalled(rec.Packages[0].Name) {
			return errors.New("apt: " + lastError(out, err))
		}
	default:
		return errors.New("Duo doesn't know how to put this back")
	}
	// It is back: drop the record first, so a left-over file can't make it look restorable.
	done = true
	os.Remove(filepath.Join(rec.dir(), "record.json"))
	os.RemoveAll(rec.dir())
	return nil
}

func (rec *removedApp) deleteForGood() error {
	if !removeMu.TryLock() {
		return errors.New("Another uninstall is still running")
	}
	defer removeMu.Unlock()
	if !inside(rec.dir(), removedDir()) {
		return errors.New("not in the bin")
	}
	if err := os.RemoveAll(rec.dir()); err != nil {
		return errors.New("some files couldn't be deleted")
	}
	return nil
}

// ---- screens ----

// uninstallApp checks what uninstalling e involves, asks, then does it.
func uninstallApp(a *App, e *Entry) {
	if e.Builtin != "" {
		a.notify("Built-in apps stay. HIDE puts one away", true)
		return
	}
	pinned := a.cfg.pinIndex(e.Key) >= 0
	var r *removal
	a.background("Checking "+e.Name+"...", func() error {
		var err error
		r, err = planRemoval(e)
		return err
	}, func(err error) {
		if err != nil {
			alert(a, "CAN'T UNINSTALL", err.Error())
			return
		}
		r.pinned = pinned
		confirm(a, "UNINSTALL "+strings.ToUpper(fit(e.Name, 140, 1, false))+"?", r.message(), func() {
			if r.needPW {
				a.withSudo("Needed to uninstall "+e.Name, func(pw string) { r.run(a, pw) })
			} else {
				r.run(a, "")
			}
		})
	})
}

func (r *removal) run(a *App, pw string) {
	a.background("Uninstalling "+r.e.Name+"...", func() error {
		_, err := r.do(a, pw)
		return err
	}, func(err error) {
		var nc *noCopyError
		var kept *errKept
		switch {
		case errors.Is(err, errNeedPassword) && pw == "":
			a.withSudo("Needed to uninstall "+r.e.Name, func(pw string) { r.run(a, pw) })
		case errors.As(err, &nc):
			confirm(a, "NO COPY", "Couldn't keep a copy ("+nc.why+"). Uninstall anyway? Only the Store could put it back.", func() {
				r.noCopy = true
				r.run(a, pw)
			})
		case errors.As(err, &kept):
			alert(a, "UNINSTALLED", kept.msg)
		case err != nil:
			alert(a, "NOT UNINSTALLED", err.Error())
		default:
			a.notify(r.e.Name+" moved to Removed apps", false)
		}
	})
}

// appsChanged rescans the launcher after apps came or went, and closes details of gone apps.
func (a *App) appsChanged() {
	a.rescan()
	have := map[string]bool{}
	for _, e := range a.all {
		have[e.Key] = true
	}
	keep := a.stack[:1]
	for _, v := range a.stack[1:] {
		if ai, ok := v.(*AppInfo); ok && !have[ai.e.Key] {
			continue
		}
		keep = append(keep, v)
	}
	a.stack = keep
	if h, ok := a.stack[0].(*Home); ok {
		h.keep(a)
	}
	if l, ok := a.top().(*RowList); ok && l.Refresh != nil {
		l.Refresh(a, l)
	}
	a.dirty = true
	a.syncText()
}

// dropTagged closes the pages tagged tag.
func (a *App) dropTagged(tag string) {
	keep := a.stack[:1]
	for _, v := range a.stack[1:] {
		if l, ok := v.(*RowList); ok && l.Tag == tag {
			continue
		}
		keep = append(keep, v)
	}
	a.stack = keep
	a.dirty = true
}

// forget drops a removed app from favourites, hidden apps and the recent list.
func (c *Config) forget(key string) {
	if i := c.pinIndex(key); i >= 0 {
		c.Pinned = append(c.Pinned[:i], c.Pinned[i+1:]...)
	}
	delete(c.Hidden, key)
	delete(c.Used, key)
}

// uninstallPage lists every app that isn't built in, hidden ones too.
func uninstallPage(a *App, parent *RowList) *RowList {
	l := page("UNINSTALL APPS", artTrash)
	l.Refresh = func(a *App, l *RowList) {
		l.Rows = nil
		for _, e := range orderApps(a.all, a.cfg, true) {
			if e.Builtin != "" {
				continue
			}
			e := e
			kind := ""
			if _, ok := zcSlug(e.Key); ok {
				kind = "ZEROCODE"
			}
			l.Rows = append(l.Rows, &Row{Label: e.Name, Kind: rowAction, Value: kind,
				Do: func(a *App, r *Row) { uninstallApp(a, e) }})
		}
	}
	l.Refresh(a, l)
	l.Footer = "Removed apps can put them back"
	l.OnBack = func(a *App) { parent.Refresh(a, parent) }
	l.BarFn = func(a *App, l *RowList) []BarItem {
		return []BarItem{{"UNINSTALL", artTrash, len(l.Rows) == 0}, {}, {}, {}, {"BACK", artBack, false}}
	}
	return l
}

// removedPage is the bin.
func removedPage(a *App, parent *RowList) *RowList {
	l := page("REMOVED APPS", artTrash)
	l.Refresh = func(a *App, l *RowList) {
		l.Rows = nil
		recs := loadRemoved()
		var total int64
		for _, rec := range recs {
			rec := rec
			total += dirSize(rec.dir())
			l.Rows = append(l.Rows, &Row{Label: rec.Name, Kind: rowLink,
				Value: time.Unix(rec.Removed, 0).Format("Jan 2"),
				Do:    func(a *App, r *Row) { a.push(removedItemPage(a, rec, l)) }})
		}
		if len(recs) > 0 {
			l.Rows = append(l.Rows, &Row{Label: "Delete all for good", Kind: rowAction, Value: humanBytes(total),
				Do: func(a *App, r *Row) {
					confirm(a, "DELETE ALL?", fmt.Sprintf("The %d apps here and their files are deleted. This can't be undone.", len(recs)), func() {
						a.background("Deleting...", func() error {
							var first error
							for _, rec := range recs {
								if err := rec.deleteForGood(); err != nil && first == nil {
									first = err
								}
							}
							return first
						}, func(err error) {
							if err != nil {
								alert(a, "NOT ALL DELETED", err.Error())
							} else {
								a.notify("Removed apps deleted", false)
							}
							l.Refresh(a, l)
						})
					})
				}})
		}
	}
	l.Refresh(a, l)
	l.Footer = "Apps you uninstall wait here"
	l.OnBack = func(a *App) { parent.Refresh(a, parent) }
	return l
}

func removedItemPage(a *App, rec *removedApp, parent *RowList) *RowList {
	l := page("REMOVED APP", artTrash)
	l.Tag = "removed:" + rec.ID
	where := tilde(rec.Folder)
	if rec.Kind == kindPkg && len(rec.Packages) > 0 {
		where = rec.Packages[0].Name + " " + rec.Packages[0].Version
	}
	size := dirSize(rec.dir())
	can := rec.canRestore()
	restoreLabel := "Put it back"
	if rec.Kind == kindPkg && !can {
		restoreLabel = "Put it back (no copy)"
	}
	l.Rows = []*Row{
		{Label: rec.Name, Value: rec.kindName()},
		{Label: restoreLabel, Kind: rowAction, Off: !can, Do: func(a *App, r *Row) { restoreApp(a, rec) }},
		{Label: "Delete for good", Kind: rowAction, Value: humanBytes(size), Do: func(a *App, r *Row) {
			confirm(a, "DELETE?", rec.Name+" and its files in the bin are deleted. This can't be undone.", func() {
				a.background("Deleting "+rec.Name+"...", rec.deleteForGood, func(err error) {
					if err != nil {
						alert(a, "NOT DELETED", err.Error())
						return
					}
					a.notify(rec.Name+" deleted", false)
					a.pop()
					parent.Refresh(a, parent)
				})
			})
		}},
		{Label: "Removed", Value: time.Unix(rec.Removed, 0).Format("Jan 2, 15:04")},
		{Label: "Was", Value: where},
	}
	if rec.Kind == kindPkg && len(rec.Packages) > 1 {
		var names []string
		for _, c := range rec.Packages[1:] {
			names = append(names, c.Name)
		}
		l.Rows = append(l.Rows, &Row{Label: "With", Value: strings.Join(names, ", ")})
	}
	l.Sel = 1
	return l
}

func restoreApp(a *App, rec *removedApp) {
	var do func(pw string)
	do = func(pw string) {
		a.background("Putting "+rec.Name+" back...", func() error {
			if err := rec.restore(pw); err != nil {
				return err
			}
			a.work <- func() { // also when the wait was cancelled
				if rec.Pinned && a.cfg.pinIndex(rec.Key) < 0 {
					a.cfg.Pinned = append(a.cfg.Pinned, rec.Key)
					a.save()
				}
				a.dropTagged("removed:" + rec.ID)
				a.appsChanged()
			}
			return nil
		}, func(err error) {
			switch {
			case errors.Is(err, errNeedPassword) && pw == "":
				a.withSudo("Needed to put "+rec.Name+" back", do)
			case err != nil:
				alert(a, "NOT PUT BACK", err.Error())
			default:
				a.notify(rec.Name+" is back", false)
			}
		})
	}
	if rec.Kind == kindZC {
		do("") // the helper needs no password (asked for only if it does)
		return
	}
	a.withSudo("Needed to put "+rec.Name+" back", do)
}
