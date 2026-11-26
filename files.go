package main

// Files, built in: the engine. Listing and sorting folders, kinds of files, copying and
// moving (with progress, cancel and new names when a name is taken), the desktop trash,
// zip archives in and out (and tar/tar.gz out), searching, folder sizes and text previews.

import (
	"archive/tar"
	"archive/zip"
	"bufio"
	"bytes"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
)

type fileKind int

const (
	kOther fileKind = iota
	kUp
	kDir
	kImage
	kAudio
	kVideo
	kText
	kCode
	kArchive
	kExec
	kPS1
	kDoc
)

var kindExts = map[string]fileKind{}

func init() {
	for k, list := range map[fileKind]string{
		kImage:   "jpg jpeg png gif webp bmp tif tiff heic heif avif svg ico",
		kAudio:   "mp3 wav flac ogg m4a opus aac wma mid midi",
		kVideo:   "mp4 mkv avi mov webm m4v mpg mpeg",
		kText:    "txt md log csv ini conf cfg toml yaml yml json xml rst nfo srt desktop service",
		kCode:    "go rs c h cpp hpp cc py js ts sh bash lua java kt rb php pl html css sql zig asm s mk",
		kArchive: "zip tar gz tgz bz2 xz 7z rar zst deb",
		kPS1:     "cue chd pbp m3u ccd",
		kDoc:     "pdf doc docx odt epub",
	} {
		for _, e := range strings.Fields(list) {
			kindExts["."+e] = k
		}
	}
}

func kindOf(name string, mode os.FileMode) fileKind {
	if mode.IsDir() {
		return kDir
	}
	if k, ok := kindExts[strings.ToLower(filepath.Ext(name))]; ok {
		return k
	}
	switch strings.ToLower(name) {
	case "makefile", "dockerfile", "readme", "license", "copying":
		return kText
	}
	if mode&0o111 != 0 && mode.IsRegular() {
		return kExec
	}
	return kOther
}

type fileEntry struct {
	name, path string
	dir, link  bool
	size       int64
	mod        time.Time
	mode       os.FileMode
	kind       fileKind
}

const (
	sortName = iota
	sortDate
	sortSize
	sortType
)

var sortNames = []string{"NAME", "DATE", "SIZE", "TYPE"}

// naturalLess compares names the way people expect: case-insensitive, "file2" before "file10".
func naturalLess(a, b string) bool {
	ra, rb := []rune(strings.ToLower(a)), []rune(strings.ToLower(b))
	i, j := 0, 0
	for i < len(ra) && j < len(rb) {
		if unicode.IsDigit(ra[i]) && unicode.IsDigit(rb[j]) {
			si, sj := i, j
			for i < len(ra) && unicode.IsDigit(ra[i]) {
				i++
			}
			for j < len(rb) && unicode.IsDigit(rb[j]) {
				j++
			}
			na, _ := strconv.ParseUint(string(ra[si:i]), 10, 64)
			nb, _ := strconv.ParseUint(string(rb[sj:j]), 10, 64)
			if na != nb {
				return na < nb
			}
			continue
		}
		if ra[i] != rb[j] {
			return ra[i] < rb[j]
		}
		i++
		j++
	}
	return len(ra)-i < len(rb)-j
}

// listDir lists a folder: folders first, then files, in the chosen order.
func listDir(dir string, hidden bool, by int) ([]fileEntry, error) {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	out := make([]fileEntry, 0, len(ents))
	for _, e := range ents {
		if !hidden && strings.HasPrefix(e.Name(), ".") {
			continue
		}
		p := filepath.Join(dir, e.Name())
		fe := fileEntry{name: e.Name(), path: p, link: e.Type()&os.ModeSymlink != 0}
		fi, err := os.Stat(p) // through links: a link to a folder is a folder
		if err != nil {
			fi, err = os.Lstat(p)
		}
		if err == nil {
			fe.size, fe.mod, fe.mode, fe.dir = fi.Size(), fi.ModTime(), fi.Mode(), fi.IsDir()
		}
		fe.kind = kindOf(fe.name, fe.mode)
		out = append(out, fe)
	}
	sortEntries(out, by)
	return out, nil
}

func sortEntries(es []fileEntry, by int) {
	sort.SliceStable(es, func(i, j int) bool {
		a, b := es[i], es[j]
		if a.dir != b.dir {
			return a.dir
		}
		switch by {
		case sortDate:
			if !a.mod.Equal(b.mod) {
				return a.mod.After(b.mod)
			}
		case sortSize:
			if a.size != b.size && !a.dir {
				return a.size > b.size
			}
		case sortType:
			ea, eb := strings.ToLower(filepath.Ext(a.name)), strings.ToLower(filepath.Ext(b.name))
			if ea != eb {
				return ea < eb
			}
		}
		return naturalLess(a.name, b.name)
	})
}

// uniqueName is name, or "name (2).ext", "name (3).ext"... if that's taken in dir.
func uniqueName(dir, name string) string {
	if _, err := os.Lstat(filepath.Join(dir, name)); os.IsNotExist(err) {
		return name
	}
	ext := filepath.Ext(name)
	base := strings.TrimSuffix(name, ext)
	if strings.HasSuffix(strings.ToLower(base), ".tar") { // keep "x.tar.gz" together
		ext = base[len(base)-4:] + ext
		base = base[:len(base)-4]
	}
	for i := 2; ; i++ {
		n := fmt.Sprintf("%s (%d)%s", base, i, ext)
		if _, err := os.Lstat(filepath.Join(dir, n)); os.IsNotExist(err) {
			return n
		}
	}
}

// validName refuses names a file can't have.
func validName(n string) error {
	switch {
	case strings.TrimSpace(n) == "":
		return errors.New("the name is empty")
	case n == "." || n == "..":
		return errors.New("that name is taken by the system")
	case strings.ContainsAny(n, "/\x00"):
		return errors.New("a name can't have / in it")
	case len(n) > 255:
		return errors.New("the name is too long")
	}
	return nil
}

// within: p is base or inside it.
func within(p, base string) bool {
	rel, err := filepath.Rel(base, p)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// treeSize adds up a folder (stopping after the deadline; done says whether it finished).
func treeSize(path string, deadline time.Time) (size int64, files, dirs int, done bool) {
	done = true
	filepath.WalkDir(path, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if time.Now().After(deadline) {
			done = false
			return filepath.SkipAll
		}
		if d.IsDir() {
			if p != path {
				dirs++
			}
			return nil
		}
		if fi, err := d.Info(); err == nil {
			size += fi.Size()
		}
		files++
		return nil
	})
	return
}

// copier copies with a running byte count and a way to stop.
type copier struct {
	done, total int64
	progress    func(done, total int64)
	stop        func() bool
	buf         []byte
}

var errStopped = errors.New("stopped")

func (c *copier) file(src, dst string, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode.Perm()|0o200)
	if err != nil {
		return err
	}
	if c.buf == nil {
		c.buf = make([]byte, 256<<10)
	}
	for {
		if c.stop != nil && c.stop() {
			out.Close()
			os.Remove(dst)
			return errStopped
		}
		n, rerr := in.Read(c.buf)
		if n > 0 {
			if _, err := out.Write(c.buf[:n]); err != nil {
				out.Close()
				os.Remove(dst)
				return err
			}
			c.done += int64(n)
			if c.progress != nil {
				c.progress(c.done, c.total)
			}
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			out.Close()
			os.Remove(dst)
			return rerr
		}
	}
	if err := out.Close(); err != nil {
		return err
	}
	os.Chmod(dst, mode.Perm())
	if fi, err := os.Stat(src); err == nil {
		os.Chtimes(dst, fi.ModTime(), fi.ModTime())
	}
	return nil
}

// tree copies src (a file, folder or link) to dst, which must not exist.
func (c *copier) tree(src, dst string) error {
	fi, err := os.Lstat(src)
	if err != nil {
		return err
	}
	switch {
	case fi.Mode()&os.ModeSymlink != 0:
		target, err := os.Readlink(src)
		if err != nil {
			return err
		}
		return os.Symlink(target, dst)
	case fi.IsDir():
		if err := os.Mkdir(dst, fi.Mode().Perm()|0o700); err != nil {
			return err
		}
		ents, err := os.ReadDir(src)
		if err != nil {
			return err
		}
		for _, e := range ents {
			if err := c.tree(filepath.Join(src, e.Name()), filepath.Join(dst, e.Name())); err != nil {
				return err
			}
		}
		os.Chmod(dst, fi.Mode().Perm())
		return nil
	case fi.Mode().IsRegular():
		return c.file(src, dst, fi.Mode())
	}
	return nil // devices, sockets: skipped
}

// pasteInto copies (or, with cut, moves) each path into dir, renaming when a name is taken.
// It returns the new paths. Moves are renames where possible; across drives they copy, then
// delete the original once the copy is complete.
func pasteInto(paths []string, dir string, cut bool, progress func(done, total int64), stop func() bool) ([]string, error) {
	c := &copier{progress: progress, stop: stop}
	for _, p := range paths {
		if within(dir, p) {
			return nil, fmt.Errorf("can't put %s inside itself", filepath.Base(p))
		}
		if !cut || !sameDevice(p, dir) {
			s, _, _, _ := treeSize(p, time.Now().Add(10*time.Second))
			c.total += s
		}
	}
	var out []string
	for _, p := range paths {
		if cut && filepath.Dir(p) == filepath.Clean(dir) {
			out = append(out, p) // moved to where it already is
			continue
		}
		dst := filepath.Join(dir, uniqueName(dir, filepath.Base(p)))
		if cut {
			if err := os.Rename(p, dst); err == nil {
				out = append(out, dst)
				continue
			}
		}
		if err := c.tree(p, dst); err != nil {
			os.RemoveAll(dst)
			return out, err
		}
		if cut {
			if err := os.RemoveAll(p); err != nil {
				return out, err
			}
		}
		out = append(out, dst)
	}
	return out, nil
}

// ---- archives ----

// zipPaths makes a zip of the paths (folders with what's in them) at out.
func zipPaths(paths []string, out string, progress func(done, total int64), stop func() bool) error {
	var total int64
	for _, p := range paths {
		s, _, _, _ := treeSize(p, time.Now().Add(10*time.Second))
		total += s
	}
	f, err := os.OpenFile(out, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return err
	}
	zw := zip.NewWriter(f)
	var done int64
	buf := make([]byte, 256<<10)
	fail := func(err error) error {
		zw.Close()
		f.Close()
		os.Remove(out)
		return err
	}
	for _, root := range paths {
		base := filepath.Dir(root)
		err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if stop != nil && stop() {
				return errStopped
			}
			if p == out {
				return nil
			}
			rel, _ := filepath.Rel(base, p)
			rel = filepath.ToSlash(rel)
			info, err := d.Info()
			if err != nil {
				return err
			}
			if d.IsDir() {
				_, err := zw.CreateHeader(&zip.FileHeader{Name: rel + "/", Modified: info.ModTime()})
				return err
			}
			if !info.Mode().IsRegular() {
				return nil
			}
			h, err := zip.FileInfoHeader(info)
			if err != nil {
				return err
			}
			h.Name, h.Method = rel, zip.Deflate
			w, err := zw.CreateHeader(h)
			if err != nil {
				return err
			}
			in, err := os.Open(p)
			if err != nil {
				return err
			}
			defer in.Close()
			for {
				n, rerr := in.Read(buf)
				if n > 0 {
					if _, err := w.Write(buf[:n]); err != nil {
						return err
					}
					done += int64(n)
					if progress != nil {
						progress(done, total)
					}
				}
				if rerr == io.EOF {
					return nil
				}
				if rerr != nil {
					return rerr
				}
			}
		})
		if err != nil {
			return fail(err)
		}
	}
	if err := zw.Close(); err != nil {
		return fail(err)
	}
	return f.Close()
}

func isArchive(name string) bool {
	n := strings.ToLower(name)
	for _, e := range []string{".zip", ".tar", ".tar.gz", ".tgz"} {
		if strings.HasSuffix(n, e) {
			return true
		}
	}
	return false
}

func archiveBase(name string) string {
	n := strings.ToLower(name)
	for _, e := range []string{".tar.gz", ".tgz", ".zip", ".tar"} {
		if strings.HasSuffix(n, e) {
			return name[:len(name)-len(e)]
		}
	}
	return name
}

// extractArchive unpacks a .zip, .tar, .tar.gz or .tgz into a new folder next to it (named
// after it) and returns that folder. Nothing may land outside it: entries with absolute paths
// or "..", and links, are skipped.
func extractArchive(path string, progress func(done, total int64), stop func() bool) (string, error) {
	dir := filepath.Dir(path)
	dest := filepath.Join(dir, uniqueName(dir, archiveBase(filepath.Base(path))))
	if err := os.Mkdir(dest, 0o755); err != nil {
		return "", err
	}
	safe := func(name string) (string, bool) {
		name = filepath.FromSlash(strings.TrimPrefix(name, "./"))
		if name == "" || filepath.IsAbs(name) || strings.HasPrefix(name, string(filepath.Separator)) {
			return "", false
		}
		p := filepath.Join(dest, name)
		return p, within(p, dest) && p != dest
	}
	var done int64
	const maxTotal = 4 << 30
	write := func(p string, r io.Reader, mode os.FileMode) error {
		os.MkdirAll(filepath.Dir(p), 0o755)
		f, err := os.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, mode.Perm()|0o600)
		if err != nil {
			return err
		}
		n, err := io.Copy(f, io.LimitReader(r, maxTotal-done+1))
		done += n
		if cerr := f.Close(); err == nil {
			err = cerr
		}
		if done > maxTotal {
			return errors.New("the archive unpacks to more than 4 GB")
		}
		return err
	}
	fail := func(err error) (string, error) { os.RemoveAll(dest); return "", err }
	lower := strings.ToLower(path)
	if strings.HasSuffix(lower, ".zip") {
		zr, err := zip.OpenReader(path)
		if err != nil {
			return fail(err)
		}
		defer zr.Close()
		var total int64
		for _, f := range zr.File {
			total += int64(f.UncompressedSize64)
		}
		for _, f := range zr.File {
			if stop != nil && stop() {
				return fail(errStopped)
			}
			p, ok := safe(f.Name)
			if !ok || f.Mode()&os.ModeSymlink != 0 {
				continue
			}
			if f.FileInfo().IsDir() {
				os.MkdirAll(p, 0o755)
				continue
			}
			rc, err := f.Open()
			if err != nil {
				return fail(err)
			}
			err = write(p, rc, f.Mode())
			rc.Close()
			if err != nil {
				return fail(err)
			}
			if progress != nil {
				progress(done, total)
			}
		}
		return dest, nil
	}
	fh, err := os.Open(path)
	if err != nil {
		return fail(err)
	}
	defer fh.Close()
	var r io.Reader = bufio.NewReader(fh)
	if strings.HasSuffix(lower, ".gz") || strings.HasSuffix(lower, ".tgz") {
		gz, err := gzip.NewReader(r)
		if err != nil {
			return fail(err)
		}
		r = gz
	}
	fi, _ := fh.Stat()
	tr := tar.NewReader(r)
	for {
		if stop != nil && stop() {
			return fail(errStopped)
		}
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fail(err)
		}
		p, ok := safe(h.Name)
		if !ok {
			continue
		}
		switch h.Typeflag {
		case tar.TypeDir:
			os.MkdirAll(p, 0o755)
		case tar.TypeReg:
			if err := write(p, tr, os.FileMode(h.Mode)); err != nil {
				return fail(err)
			}
		} // links and devices: skipped
		if progress != nil && fi != nil {
			if pos, err := fh.Seek(0, io.SeekCurrent); err == nil {
				progress(pos, fi.Size())
			}
		}
	}
	return dest, nil
}

// ---- searching ----

// searchFiles finds names containing q (any case) below root: at most limit, 12 folders deep.
func searchFiles(root, q string, limit int, stop func() bool) []fileEntry {
	q = strings.ToLower(q)
	var out []fileEntry
	rootDepth := strings.Count(filepath.Clean(root), string(filepath.Separator))
	filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || (stop != nil && stop()) || len(out) >= limit {
			if len(out) >= limit || (stop != nil && stop()) {
				return filepath.SkipAll
			}
			return nil
		}
		if p == root {
			return nil
		}
		name := d.Name()
		if d.IsDir() && (strings.HasPrefix(name, ".") && !strings.HasPrefix(q, ".") ||
			strings.Count(p, string(filepath.Separator))-rootDepth > 12) {
			return filepath.SkipDir
		}
		if strings.Contains(strings.ToLower(name), q) {
			fe := fileEntry{name: name, path: p}
			if fi, err := d.Info(); err == nil {
				fe.size, fe.mod, fe.mode, fe.dir = fi.Size(), fi.ModTime(), fi.Mode(), fi.IsDir()
			}
			fe.kind = kindOf(name, fe.mode)
			out = append(out, fe)
		}
		return nil
	})
	return out
}

// ---- the trash ----

type trashItem struct {
	name, file, info, orig string
	deleted                time.Time
	dir                    bool
	size                   int64
}

func trashDir() string { return filepath.Join(homeDir(), ".local/share/Trash") }

func trashList() []trashItem {
	var out []trashItem
	ents, _ := os.ReadDir(filepath.Join(trashDir(), "files"))
	for _, e := range ents {
		it := trashItem{name: e.Name(), file: filepath.Join(trashDir(), "files", e.Name()),
			info: filepath.Join(trashDir(), "info", e.Name()+".trashinfo")}
		if b, err := os.ReadFile(it.info); err == nil {
			for _, l := range strings.Split(string(b), "\n") {
				if v, ok := strings.CutPrefix(l, "Path="); ok {
					it.orig = strings.TrimSpace(v)
				} else if v, ok := strings.CutPrefix(l, "DeletionDate="); ok {
					it.deleted, _ = time.ParseInLocation("2006-01-02T15:04:05", strings.TrimSpace(v), time.Local)
				}
			}
		}
		if fi, err := os.Lstat(it.file); err == nil {
			it.dir, it.size = fi.IsDir(), fi.Size()
		}
		out = append(out, it)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].deleted.After(out[j].deleted) })
	return out
}

// restore puts a trashed file back where it was (under a new name if that's taken now).
func (t trashItem) restore() (string, error) {
	dir := filepath.Dir(t.orig)
	if t.orig == "" {
		dir = homeDir()
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	name := filepath.Base(t.orig)
	if t.orig == "" {
		name = t.name
	}
	dst := filepath.Join(dir, uniqueName(dir, name))
	if err := os.Rename(t.file, dst); err != nil {
		return "", err
	}
	os.Remove(t.info)
	return dst, nil
}

func (t trashItem) erase() error {
	if err := os.RemoveAll(t.file); err != nil {
		return err
	}
	os.Remove(t.info)
	return nil
}

// ---- reading files ----

// readPreview reads the start of a file (up to max bytes) and says whether it looks binary.
func readPreview(path string, max int) (data []byte, binary, truncated bool, err error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, false, false, err
	}
	defer f.Close()
	data, err = io.ReadAll(io.LimitReader(f, int64(max)+1))
	if err != nil {
		return nil, false, false, err
	}
	if len(data) > max {
		data, truncated = data[:max], true
	}
	probe := data
	if len(probe) > 8192 {
		probe = probe[:8192]
	}
	if bytes.IndexByte(probe, 0) >= 0 {
		return data, true, truncated, nil
	}
	ctrl := 0
	for _, c := range probe {
		if c < 0x20 && c != '\n' && c != '\r' && c != '\t' && c != 0x1b && c != '\f' {
			ctrl++
		}
	}
	return data, ctrl > len(probe)/20, truncated, nil
}

// ---- settings ----

type filesConf struct {
	dir       string
	sortBy    int
	hidden    bool
	bookmarks []string
}

func filesConfPath() string { return filepath.Join(filepath.Dir(configPath()), "files") }

func loadFilesConf() filesConf {
	c := filesConf{}
	b, _ := os.ReadFile(filesConfPath())
	for _, l := range strings.Split(string(b), "\n") {
		k, v, _ := strings.Cut(l, "=")
		switch k {
		case "dir":
			c.dir = v
		case "sort":
			c.sortBy, _ = strconv.Atoi(v)
			c.sortBy = max(0, min(c.sortBy, len(sortNames)-1))
		case "hidden":
			c.hidden = v == "1"
		case "mark":
			if v != "" {
				c.bookmarks = append(c.bookmarks, v)
			}
		}
	}
	return c
}

func (c filesConf) save() {
	var b strings.Builder
	hidden := 0
	if c.hidden {
		hidden = 1
	}
	fmt.Fprintf(&b, "dir=%s\nsort=%d\nhidden=%d\n", c.dir, c.sortBy, hidden)
	for _, m := range c.bookmarks {
		fmt.Fprintf(&b, "mark=%s\n", m)
	}
	writeFileAtomic(filesConfPath(), b.String(), 0o644)
}
