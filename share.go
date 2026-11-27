package main

// Sharing files between Cardputers on the same Wi-Fi: Files > MENU > Send to a Cardputer, and
// Receive files (in Files' menu and Settings > Sharing). It is Download Play for any files: the
// sender packs what's marked as a .tar.gz, shows a 4-digit code and announces "DUOFILES1 {json}"
// on Download Play's UDP port (older Duos only listen for "DUODL1", so they don't mistake
// files for an app); the receiver lists the offers, asks for the code, checks the size and
// SHA-256, and unpacks into ~/Downloads/From <sender>/, renaming rather than overwriting.

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	hexenc "encoding/hex"
)

const (
	filesMagic   = "DUOFILES1 "
	filesMaxSize = 1 << 30 // packed
)

// packFiles writes the paths (folders with what's in them) as a .tar.gz, each under its own
// name. Links and special files are left out.
func packFiles(paths []string, w io.Writer) error {
	gz := gzip.NewWriter(w)
	tw := tar.NewWriter(gz)
	for _, root := range paths {
		base := filepath.Dir(root)
		err := filepath.Walk(root, func(p string, fi os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			if fi.Mode()&os.ModeSymlink != 0 || (!fi.IsDir() && !fi.Mode().IsRegular()) {
				return nil
			}
			rel, _ := filepath.Rel(base, p)
			hdr, err := tar.FileInfoHeader(fi, "")
			if err != nil {
				return err
			}
			hdr.Name, hdr.Uname, hdr.Gname, hdr.Uid, hdr.Gid = filepath.ToSlash(rel), "", "", 0, 0
			if fi.IsDir() {
				hdr.Name += "/"
			}
			if err := tw.WriteHeader(hdr); err != nil {
				return err
			}
			if fi.IsDir() {
				return nil
			}
			f, err := os.Open(p)
			if err != nil {
				return err
			}
			_, err = io.Copy(tw, f)
			f.Close()
			return err
		})
		if err != nil {
			return err
		}
	}
	if err := tw.Close(); err != nil {
		return err
	}
	return gz.Close()
}

// unpackFilesTo unpacks a received .tar.gz into dest. Nothing lands outside it; a top-level
// name that's taken gets " (2)" and so on; only plain files and folders.
func unpackFilesTo(file, dest string) ([]string, error) {
	f, err := os.Open(file)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dest, 0o755); err != nil {
		return nil, err
	}
	tr := tar.NewReader(gz)
	renamed := map[string]string{} // top-level name -> the name used here
	var tops []string
	var total int64
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return tops, err
		}
		name := strings.TrimSuffix(h.Name, "/")
		if name == "" || strings.HasPrefix(name, "/") || strings.Contains(name, "\\") || filepath.IsAbs(name) {
			continue
		}
		parts := strings.Split(name, "/")
		bad := false
		for _, part := range parts {
			bad = bad || part == ".." || part == ""
		}
		if bad {
			continue
		}
		top := parts[0]
		use, ok := renamed[top]
		if !ok {
			use = uniqueName(dest, top)
			renamed[top] = use
			tops = append(tops, filepath.Join(dest, use))
		}
		parts[0] = use
		p := filepath.Join(append([]string{dest}, parts...)...)
		if !within(p, dest) {
			continue
		}
		switch h.Typeflag {
		case tar.TypeDir:
			os.MkdirAll(p, 0o755)
		case tar.TypeReg:
			total += h.Size
			if total > 2*filesMaxSize {
				return tops, errors.New("it unpacks too big")
			}
			os.MkdirAll(filepath.Dir(p), 0o755)
			mode := os.FileMode(0o644)
			if h.Mode&0o111 != 0 {
				mode = 0o755
			}
			out, err := os.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
			if err != nil {
				return tops, err
			}
			_, err = io.CopyN(out, tr, h.Size)
			if cerr := out.Close(); err == nil {
				err = cerr
			}
			if err != nil {
				return tops, err
			}
		}
	}
	return tops, nil
}

// startSendFiles packs the paths and offers them.
func startSendFiles(paths []string, sent func(to string)) (*dlSender, error) {
	os.MkdirAll(cacheDir(), 0o755)
	f, err := os.CreateTemp(cacheDir(), "share-*.tgz")
	if err != nil {
		return nil, err
	}
	h := sha256.New()
	if err := packFiles(paths, io.MultiWriter(f, h)); err != nil {
		f.Close()
		os.Remove(f.Name())
		return nil, err
	}
	size, _ := f.Seek(0, io.SeekCurrent)
	f.Close()
	if size > filesMaxSize {
		os.Remove(f.Name())
		return nil, fmt.Errorf("that's too much to send at once (%s; 1 GB at most)", humanSize(size))
	}
	label := filepath.Base(paths[0])
	if len(paths) > 1 {
		label = fmt.Sprintf("%d items", len(paths))
	}
	o := dlOffer{Host: hostname(), App: label, Slug: "files", Size: size, Sha: hexenc.EncodeToString(h.Sum(nil)),
		Kind: "files", Count: len(paths)}
	budget := 600 // keep the announcement in one small packet
	for _, p := range paths {
		n := filepath.Base(p)
		if len(n) > 40 {
			n = n[:40]
		}
		if budget -= len(n) + 4; budget < 0 || len(o.Files) >= 8 {
			break
		}
		o.Files = append(o.Files, n)
	}
	return serveOffer(o, f.Name(), filesMagic, dlPort, sent)
}

// sendFiles is Files' "Send to a Cardputer".
func sendFiles(a *App, paths []string) {
	var d *DLSend
	label := filepath.Base(paths[0])
	if len(paths) > 1 {
		label = fmt.Sprintf("%d items", len(paths))
	}
	a.background("Packing "+musicASCII(label)+"...", func() error {
		v := &DLSend{name: musicASCII(label), started: time.Now(), title: "Sending files",
			how: [2]string{"On the other Cardputer: Files > MENU >", "Receive files, then type this code:"}}
		s, err := startSendFiles(paths, func(to string) {
			a.work <- func() {
				v.sentTo = append(v.sentTo, to)
				a.snd.Play("notice")
				a.dirty = true
			}
		})
		v.s = s
		d = v
		return err
	}, func(err error) {
		if err != nil {
			alert(a, "NOT SENT", err.Error())
			return
		}
		a.push(d)
	})
}

func downloadsDir() string { return filepath.Join(homeDir(), "Downloads") }

var stopReceiveFiles = func() {}

// newReceiveFiles lists the files offered nearby while it is open.
func newReceiveFiles(a *App) View {
	stopReceiveFiles()
	l := page("RECEIVE FILES", artSharing)
	stop := make(chan struct{})
	var once sync.Once
	stopReceiveFiles = func() { once.Do(func() { close(stop) }) }
	l.Footer = "Looking for Cardputers sending files..."
	err := dlListenFor(filesMagic, stop, func(list []dlOffer) {
		a.work <- func() {
			l.Rows = nil
			for _, o := range list {
				o := o
				if o.Kind != "files" {
					continue
				}
				l.Rows = append(l.Rows, &Row{Label: musicASCII(o.App), Kind: rowAction,
					Value: "from " + musicASCII(o.Host) + ", " + humanSize(o.Size), Do: func(a *App, r *Row) { receiveFiles(a, o) }})
			}
			if len(l.Rows) == 0 {
				l.Footer = "Looking for Cardputers sending files..."
			} else {
				l.Footer = "Choose, then type the sender's code"
			}
			a.dirty = true
		}
	})
	if err != nil {
		l.Footer = "Can't listen on the network: " + briefLine(err.Error())
	}
	l.OnBack = func(a *App) { stopReceiveFiles() }
	return l
}

// receiveFiles asks for the code, then downloads, checks and unpacks into Downloads.
func receiveFiles(a *App, o dlOffer) {
	what := musicASCII(o.App)
	if len(o.Files) > 0 && o.Count > 1 {
		what = musicASCII(strings.Join(o.Files, ", "))
	}
	a.push(newInput("SENDER'S CODE", "Code shown on "+musicASCII(o.Host)+" for "+what, "", 4, false, func(code string) {
		code = strings.TrimSpace(code)
		dest := filepath.Join(downloadsDir(), "From "+cleanName(o.Host))
		if free, _ := diskSpace(homeDir()); free >= 0 && free < 2*o.Size+(50<<20) {
			alert(a, "NO ROOM", fmt.Sprintf("%s needs %s free; there's %s.", what, humanSize(2*o.Size), humanSize(free)))
			return
		}
		var tops []string
		job(a, "Receiving...", func(progress func(done, total int64), stop func() bool) error {
			file, err := dlFetch(o, code, func(done int64) { progress(done, o.Size) })
			if err != nil {
				return err
			}
			defer os.Remove(file)
			tops, err = unpackFilesTo(file, dest)
			return err
		}, func(err error) {
			if err != nil {
				alert(a, "NOT RECEIVED", briefLine(err.Error()))
				return
			}
			stopReceiveFiles()
			a.snd.Play("launch")
			a.notify(fmt.Sprintf("Received %d item%s", len(tops), map[bool]string{true: "", false: "s"}[len(tops) == 1]), false)
			for a.top() != nil && len(a.stack) > 1 {
				if _, ok := a.top().(*FileBrowser); ok {
					break
				}
				a.pop()
			}
			fb, ok := a.top().(*FileBrowser)
			if !ok {
				fb = newFileBrowser(dest)
				a.push(fb)
			} else {
				fb.go_(a, dest)
			}
			if len(tops) > 0 {
				fb.reload(tops[0])
			}
		})
	}))
}

// sharingPage is Settings > Sharing.
func sharingPage(a *App) View {
	l := page("SHARING", artSharing)
	l.Rows = []*Row{
		{Label: "Receive files", Kind: rowLink, Do: func(a *App, r *Row) { a.push(newReceiveFiles(a)) }},
		{Label: "Send files", Kind: rowLink, Value: "IN FILES", Do: func(a *App, r *Row) {
			alert(a, "SEND FILES", "Open Files, mark what to send (5), then MENU > Send to a Cardputer.")
		}},
		{Label: "Received files go to", Value: musicASCII(tilde(downloadsDir()))},
		{Label: "Share an app", Kind: rowLink, Value: "DOWNLOAD PLAY", Do: func(a *App, r *Row) { a.push(newDownloadPlay(a)) }},
	}
	l.Footer = "Both Cardputers on the same Wi-Fi"
	return l
}
