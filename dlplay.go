package main

// Download Play: share an app with another Cardputer on the same Wi-Fi, like the DS's
// Download Play. The sender packs the app's folder (~/apps/<slug>), announces it on the local
// network and shows a 4-digit code; the receiver picks it from a list, types the code, and Duo
// downloads it, checks its size and SHA-256, unpacks it safely into ~/apps/<slug> and adds it to
// the launcher. Only apps made on a Cardputer (with a folder in ~/apps) can be shared, and
// nothing is installed without the receiver choosing it and typing the sender's code.

import (
	"archive/tar"
	"compress/gzip"
	"crypto/rand"
	"crypto/sha256"
	"dslauncher/cp0"
	hexenc "encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	dlPort         = 47130 // the sender's HTTP port
	dlAnnouncePort = 47131
	dlMaxSize      = 96 << 20 // largest app accepted, packed
	dlMagic        = "DUODL1 "
)

// dlOffer is what a sender announces (and serves at /info).
type dlOffer struct {
	Host string `json:"host"` // the sender's device name
	App  string `json:"app"`  // the app's name
	Slug string `json:"slug"`
	Size int64  `json:"size"` // bytes, packed
	Sha  string `json:"sha256"`
	Port int    `json:"port"`
	// files offers (Files > Send to a Cardputer): Slug is "files"
	Kind  string   `json:"kind,omitempty"`
	Files []string `json:"files,omitempty"` // the first few names
	Count int      `json:"count,omitempty"` // how many top-level items
	Addr  string   `json:"-"`               // host:port it was heard from
	seen  time.Time
}

// ---- which apps can be shared ----

type shareable struct {
	e    *Entry
	slug string
	dir  string
	size int64
}

// shareableApps are the launcher's ZeroCode apps whose folder is in ~/apps.
func shareableApps(a *App) []shareable {
	var out []shareable
	for _, e := range a.all {
		slug, ok := zcSlug(e.Key)
		if !ok {
			continue
		}
		dir := filepath.Join(appsRoot(), slug)
		if !isDir(dir) || !exists(filepath.Join(dir, "app.conf")) {
			continue
		}
		out = append(out, shareable{e, slug, dir, dirSize(dir)})
	}
	sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i].e.Name) < strings.ToLower(out[j].e.Name) })
	return out
}

// ---- packing and unpacking ----

// dlSkip leaves out backups and test output when packing.
func dlSkip(rel string, fi os.FileInfo) bool {
	base := filepath.Base(rel)
	return base == ".test" || base == ".git" || strings.HasSuffix(base, ".prev") || base == "app.new" ||
		fi.Mode()&os.ModeSymlink != 0
}

// packApp writes dir as a .tar.gz to w (paths relative to dir; files and folders only).
func packApp(dir string, w io.Writer) error {
	gz := gzip.NewWriter(w)
	tw := tar.NewWriter(gz)
	err := filepath.Walk(dir, func(p string, fi os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil || rel == "." {
			return err
		}
		rel = filepath.ToSlash(rel)
		if dlSkip(rel, fi) {
			if fi.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if !fi.IsDir() && !fi.Mode().IsRegular() {
			return nil
		}
		hdr, err := tar.FileInfoHeader(fi, "")
		if err != nil {
			return err
		}
		hdr.Name, hdr.Uname, hdr.Gname, hdr.Uid, hdr.Gid = rel, "", "", 0, 0
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
	if err := tw.Close(); err != nil {
		return err
	}
	return gz.Close()
}

// unpackApp extracts a packed app into dst, refusing anything that would land outside it,
// links, devices and oversized content.
func unpackApp(r io.Reader, dst string) error {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return err
	}
	tr := tar.NewReader(gz)
	var total int64
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		name := filepath.FromSlash(strings.TrimSuffix(hdr.Name, "/"))
		if name == "" || filepath.IsAbs(name) || strings.HasPrefix(hdr.Name, "/") || strings.Contains(hdr.Name, "\\") {
			return fmt.Errorf("bad path %q", hdr.Name)
		}
		p := filepath.Join(dst, name)
		if !inside(p, dst) {
			return fmt.Errorf("path outside the app: %q", hdr.Name)
		}
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(p, 0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			total += hdr.Size
			if total > 4*dlMaxSize {
				return errors.New("the app unpacks too big")
			}
			if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
				return err
			}
			mode := os.FileMode(0o644)
			if hdr.Mode&0o111 != 0 {
				mode = 0o755
			}
			f, err := os.OpenFile(p, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
			if err != nil {
				return err
			}
			_, err = io.CopyN(f, tr, hdr.Size)
			if cerr := f.Close(); err == nil {
				err = cerr
			}
			if err != nil {
				return err
			}
		default:
			return fmt.Errorf("refusing %q (not a plain file or folder)", hdr.Name)
		}
	}
	if !exists(filepath.Join(dst, "app.conf")) {
		return errors.New("not an app (no app.conf)")
	}
	return nil
}

// ---- sending ----

type dlSender struct {
	offer dlOffer
	code  string
	file  string
	srv   *http.Server
	stop  chan struct{}
	once  sync.Once
	sent  func(to string) // called (from a server goroutine) when a download finishes
}

func newCode() string {
	n, err := rand.Int(rand.Reader, big.NewInt(10000))
	if err != nil {
		return fmt.Sprintf("%04d", time.Now().UnixNano()%10000)
	}
	return fmt.Sprintf("%04d", n.Int64())
}

// startSend packs the app and starts serving and announcing it.
func startSend(s shareable, port int, sent func(to string)) (*dlSender, error) {
	f, err := os.CreateTemp(cacheDir(), "dlplay-*.tgz")
	if err != nil {
		if os.MkdirAll(cacheDir(), 0o755) == nil {
			f, err = os.CreateTemp(cacheDir(), "dlplay-*.tgz")
		}
		if err != nil {
			return nil, err
		}
	}
	h := sha256.New()
	if err := packApp(s.dir, io.MultiWriter(f, h)); err != nil {
		f.Close()
		os.Remove(f.Name())
		return nil, err
	}
	size, _ := f.Seek(0, io.SeekCurrent)
	f.Close()
	if size > dlMaxSize {
		os.Remove(f.Name())
		return nil, fmt.Errorf("%s is too big to share (%s)", s.e.Name, humanBytes(size))
	}
	o := dlOffer{Host: hostname(), App: s.e.Name, Slug: s.slug, Size: size, Sha: hexenc.EncodeToString(h.Sum(nil))}
	return serveOffer(o, f.Name(), dlMagic, port, sent)
}

// serveOffer serves a packed file (at /info and /get?code=) and announces it with magic until
// stopped; it removes the file when stopped.
func serveOffer(o dlOffer, file, magic string, port int, sent func(to string)) (*dlSender, error) {
	ln, err := net.Listen("tcp4", fmt.Sprintf(":%d", port))
	if err != nil {
		ln, err = net.Listen("tcp4", ":0")
		if err != nil {
			os.Remove(file)
			return nil, err
		}
	}
	o.Port = ln.Addr().(*net.TCPAddr).Port
	d := &dlSender{offer: o, code: newCode(), file: file, stop: make(chan struct{}), sent: sent}
	mux := http.NewServeMux()
	mux.HandleFunc("/info", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(d.offer)
	})
	var tries sync.Map
	mux.HandleFunc("/get", func(w http.ResponseWriter, r *http.Request) {
		ip, _, _ := net.SplitHostPort(r.RemoteAddr)
		if n, _ := tries.LoadOrStore(ip, new(int)); *(n.(*int)) >= 5 {
			http.Error(w, "too many wrong codes", http.StatusTooManyRequests)
			return
		}
		if r.URL.Query().Get("code") != d.code {
			n, _ := tries.Load(ip)
			*(n.(*int))++
			time.Sleep(500 * time.Millisecond)
			http.Error(w, "wrong code", http.StatusForbidden)
			return
		}
		file, err := os.Open(d.file)
		if err != nil {
			http.Error(w, "gone", http.StatusGone)
			return
		}
		defer file.Close()
		w.Header().Set("Content-Type", "application/gzip")
		w.Header().Set("Content-Length", fmt.Sprint(d.offer.Size))
		if n, err := io.Copy(w, file); err == nil && n == d.offer.Size && d.sent != nil {
			d.sent(ip)
		}
	})
	d.srv = &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	go d.srv.Serve(ln)
	go dlAnnounce(d.offer, magic, d.stop)
	return d, nil
}

func (d *dlSender) Stop() {
	d.once.Do(func() {
		close(d.stop)
		d.srv.Close()
		os.Remove(d.file)
	})
}

// dlAnnounce broadcasts the offer every two seconds until stop closes.
func dlAnnounce(o dlOffer, magic string, stop chan struct{}) {
	c, err := net.ListenUDP("udp4", nil)
	if err != nil {
		return
	}
	defer c.Close()
	js, _ := json.Marshal(o)
	msg := append([]byte(magic), js...)
	t := time.NewTicker(2 * time.Second)
	defer t.Stop()
	for {
		c.WriteToUDP(msg, &net.UDPAddr{IP: net.IPv4bcast, Port: dlAnnouncePort})
		for _, b := range broadcastAddrs() {
			c.WriteToUDP(msg, &net.UDPAddr{IP: b, Port: dlAnnouncePort})
		}
		select {
		case <-t.C:
		case <-stop:
			return
		}
	}
}

// ---- receiving ----

// dlListen collects announced offers until stop closes; found gets the current list (from the
// listening goroutine) whenever it changes.
func dlListen(stop chan struct{}, found func([]dlOffer)) error {
	return dlListenFor(dlMagic, stop, found)
}

// dlListenFor is dlListen for offers announced with magic (apps, or files).
func dlListenFor(magic string, stop chan struct{}, found func([]dlOffer)) error {
	c, err := net.ListenUDP("udp4", &net.UDPAddr{Port: dlAnnouncePort})
	if err != nil {
		return err
	}
	go func() { <-stop; c.Close() }()
	go func() {
		seen := map[string]dlOffer{}
		buf := make([]byte, 2048)
		last := ""
		for {
			c.SetReadDeadline(time.Now().Add(time.Second))
			n, from, err := c.ReadFromUDP(buf)
			select {
			case <-stop:
				return
			default:
			}
			if err == nil {
				if body, ok := strings.CutPrefix(string(buf[:n]), magic); ok {
					var o dlOffer
					if json.Unmarshal([]byte(body), &o) == nil && slugRE.MatchString(o.Slug) && o.Port > 0 {
						o.Addr = net.JoinHostPort(from.IP.String(), fmt.Sprint(o.Port))
						o.seen = time.Now()
						seen[o.Addr+"/"+o.Slug] = o
					}
				}
			}
			var list []dlOffer
			for k, o := range seen {
				if time.Since(o.seen) > 7*time.Second { // the sender stopped
					delete(seen, k)
					continue
				}
				list = append(list, o)
			}
			sort.Slice(list, func(i, j int) bool { return list[i].Addr+list[i].App < list[j].Addr+list[j].App })
			key := ""
			for _, o := range list {
				key += o.Addr + o.Slug + o.Sha + ";"
			}
			if key != last {
				last = key
				found(list)
			}
		}
	}()
	return nil
}

// dlInfo asks a sender at host[:port] what it offers.
func dlInfo(addr string) (dlOffer, error) {
	if !strings.Contains(addr, ":") {
		addr = net.JoinHostPort(addr, fmt.Sprint(dlPort))
	}
	cl := http.Client{Timeout: 5 * time.Second}
	resp, err := cl.Get("http://" + addr + "/info")
	if err != nil {
		return dlOffer{}, err
	}
	defer resp.Body.Close()
	var o dlOffer
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4096)).Decode(&o); err != nil {
		return dlOffer{}, errors.New("that address isn't sharing an app")
	}
	if !slugRE.MatchString(o.Slug) {
		return dlOffer{}, errors.New("the sender's app has a bad name")
	}
	o.Addr = addr
	return o, nil
}

// dlFetch downloads an offer with the sender's code into a temp file and checks it.
func dlFetch(o dlOffer, code string, progress func(done int64)) (string, error) {
	limit, wait := int64(dlMaxSize), 10*time.Minute
	if o.Kind == "files" {
		limit, wait = filesMaxSize, time.Hour
	}
	if o.Size <= 0 || o.Size > limit {
		return "", errors.New("that's too big to receive")
	}
	cl := http.Client{Timeout: wait}
	resp, err := cl.Get("http://" + o.Addr + "/get?code=" + code)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusForbidden:
		return "", errors.New("wrong code")
	case http.StatusTooManyRequests:
		return "", errors.New("too many wrong codes; start again on the sender")
	default:
		return "", fmt.Errorf("the sender said %s", resp.Status)
	}
	os.MkdirAll(cacheDir(), 0o755)
	f, err := os.CreateTemp(cacheDir(), "dlplay-in-*.tgz")
	if err != nil {
		return "", err
	}
	h := sha256.New()
	var done int64
	buf := make([]byte, 64<<10)
	body := io.LimitReader(resp.Body, o.Size+1)
	for {
		n, rerr := body.Read(buf)
		if n > 0 {
			f.Write(buf[:n])
			h.Write(buf[:n])
			done += int64(n)
			if progress != nil {
				progress(done)
			}
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			f.Close()
			os.Remove(f.Name())
			return "", rerr
		}
	}
	f.Close()
	if done != o.Size || hexenc.EncodeToString(h.Sum(nil)) != o.Sha {
		os.Remove(f.Name())
		return "", errors.New("the download was damaged (size or checksum differ)")
	}
	return f.Name(), nil
}

// dlInstall unpacks a checked download into ~/apps/<slug>. An app already there is kept as
// ~/apps/<slug>.prev-<time> (only when replace is true).
func dlInstall(file, slug string, replace bool) (string, error) {
	if !slugRE.MatchString(slug) {
		return "", errors.New("bad app name")
	}
	root := appsRoot()
	if err := os.MkdirAll(root, 0o755); err != nil {
		return "", err
	}
	dst := filepath.Join(root, slug)
	if exists(dst) && !replace {
		return "", errors.New("already installed")
	}
	tmp, err := os.MkdirTemp(root, ".dlplay-"+slug+"-")
	if err != nil {
		return "", err
	}
	f, err := os.Open(file)
	if err != nil {
		os.RemoveAll(tmp)
		return "", err
	}
	err = unpackApp(f, tmp)
	f.Close()
	if err != nil {
		os.RemoveAll(tmp)
		return "", err
	}
	if exists(dst) {
		if err := os.Rename(dst, fmt.Sprintf("%s.prev-%d", dst, time.Now().Unix())); err != nil {
			os.RemoveAll(tmp)
			return "", err
		}
	}
	if err := os.Rename(tmp, dst); err != nil {
		os.RemoveAll(tmp)
		return "", err
	}
	os.Remove(file)
	return dst, nil
}

// ---- screens ----

// newDownloadPlay is the Download Play menu.
func newDownloadPlay(a *App) View {
	l := page("DOWNLOAD PLAY", artWifi)
	l.Rows = []*Row{
		{Label: "Send an app", Kind: rowLink, Do: func(a *App, r *Row) { a.push(newSendPick(a)) }},
		{Label: "Receive an app", Kind: rowLink, Do: func(a *App, r *Row) { a.push(newReceive(a)) }},
		{Label: "Receive from an address", Kind: rowLink, Do: func(a *App, r *Row) {
			a.push(newInput("SENDER'S ADDRESS", "IP address shown on the sending Cardputer", "", 40, false, func(addr string) {
				addr = strings.TrimSpace(addr)
				if addr == "" {
					return
				}
				var o dlOffer
				a.background("Asking "+addr+"...", func() error {
					var err error
					o, err = dlInfo(addr)
					return err
				}, func(err error) {
					if err != nil {
						a.notify(briefLine(err.Error()), true)
						return
					}
					askCode(a, o)
				})
			}))
		}},
		{Label: "Your address", Value: orDash(primaryIP())},
	}
	l.Footer = "Both Cardputers on the same Wi-Fi"
	return l
}

func newSendPick(a *App) View {
	l := page("SEND AN APP", artWifi)
	for _, s := range shareableApps(a) {
		s := s
		l.Rows = append(l.Rows, &Row{Label: s.e.Name, Kind: rowAction, Value: humanBytes(s.size), Do: func(a *App, r *Row) {
			var d *DLSend
			a.background("Packing "+s.e.Name+"...", func() error {
				v := &DLSend{name: s.e.Name, started: time.Now()}
				snd, err := startSend(s, dlPort, func(to string) {
					a.work <- func() {
						v.sentTo = append(v.sentTo, to)
						a.snd.Play("notice")
						a.dirty = true
					}
				})
				v.s = snd
				d = v
				return err
			}, func(err error) {
				if err != nil {
					a.notify(briefLine(err.Error()), true)
					return
				}
				a.push(d)
			})
		}})
	}
	if len(l.Rows) == 0 {
		l.Footer = "No shareable apps (apps made on a Cardputer, in ~/apps)"
	}
	return l
}

// DLSend shows the code while the app is offered; Esc stops sharing.
type DLSend struct {
	s       *dlSender
	name    string
	started time.Time
	sentTo  []string
	title   string    // "" for Download Play
	how     [2]string // the two lines telling the other Cardputer what to do
}

func (v *DLSend) Animating(a *App) bool { return true }

func (v *DLSend) Key(a *App, k cp0.Key) {
	if k.Back() || k.Slot() == 4 {
		v.s.Stop()
		a.snd.Play("back")
		a.pop()
	}
}

func (v *DLSend) Bar(a *App) []BarItem {
	return []BarItem{{}, {}, {}, {}, {"STOP", artNo, false}}
}

func (v *DLSend) Draw(a *App) {
	g := a.g
	title, how := "Download Play: sending", [2]string{"On the other Cardputer: Download Play,", "Receive an app, then type this code:"}
	if v.title != "" {
		title, how = v.title, v.how
	}
	pageTitle(a, title, artWifi)
	g.dsInfo(0, 20, W, 47, cButton)
	g.text(8, 23, fit(v.name, 200, 1, false), cText, 1, false)
	g.textR(W-8, 23, humanBytes(v.s.offer.Size), cSub, 1, false)
	g.dsInfoText(4, 35, W-8, 31)
	g.text(10, 39, how[0], cWhite, 1, false)
	g.text(10, 51, how[1], cWhite, 1, false)
	code := v.s.code
	cw := fontClock30.width(code)
	g.dsPlate((W-cw)/2-8, 72, cw+16, 38)
	g.fontText(fontClock30, (W-cw)/2, 76, code, cText)
	msg := "Waiting...  " + orDash(primaryIP())
	if n := len(v.sentTo); n > 0 {
		msg = fmt.Sprintf("Sent to %s", v.sentTo[n-1])
		if n > 1 {
			msg = fmt.Sprintf("Sent %d times, last to %s", n, v.sentTo[n-1])
		}
	} else {
		g.dsLoading(W-26, 88, a.t)
	}
	g.textC(W/2, 116, msg, cSub, 1, false)
}

// stopReceive ends the current receive list's network listener (there is at most one).
var stopReceive = func() {}

// newReceive lists the apps offered nearby while it is open.
func newReceive(a *App) View {
	stopReceive()
	l := page("RECEIVE AN APP", artWifi)
	stop := make(chan struct{})
	var once sync.Once
	stopReceive = func() { once.Do(func() { close(stop) }) }
	l.Footer = "Looking for Cardputers that are sending..."
	err := dlListen(stop, func(list []dlOffer) {
		a.work <- func() {
			l.Rows = nil
			for _, o := range list {
				o := o
				l.Rows = append(l.Rows, &Row{Label: o.App, Kind: rowAction,
					Value: "from " + o.Host + ", " + humanBytes(o.Size), Do: func(a *App, r *Row) { askCode(a, o) }})
			}
			if len(list) == 0 {
				l.Footer = "Looking for Cardputers that are sending..."
			} else {
				l.Footer = "Choose an app, then type the sender's code"
			}
			a.dirty = true
		}
	})
	if err != nil {
		l.Footer = "Can't listen on the network: " + briefLine(err.Error())
	}
	l.OnBack = func(a *App) { stopReceive() }
	return l
}

// askCode asks for the sender's code, then downloads and installs the app.
func askCode(a *App, o dlOffer) {
	prompt := fmt.Sprintf("Code shown on %s for %s", o.Host, o.App)
	a.push(newInput("SENDER'S CODE", prompt, "", 4, false, func(code string) {
		code = strings.TrimSpace(code)
		dst := filepath.Join(appsRoot(), o.Slug)
		install := func(replace bool) {
			var file string
			a.background(fmt.Sprintf("Downloading %s (%s)...", o.App, humanBytes(o.Size)), func() error {
				var err error
				file, err = dlFetch(o, code, nil)
				if err != nil {
					return err
				}
				dir, err := dlInstall(file, o.Slug, replace)
				if err != nil {
					os.Remove(file)
					return err
				}
				if _, err := zcRun("", "install", dir); err != nil && !errors.Is(err, errNeedPassword) {
					return fmt.Errorf("unpacked to %s, but adding it to the launcher failed: %s", tilde(dir), briefLine(err.Error()))
				} else if errors.Is(err, errNeedPassword) {
					return fmt.Errorf("unpacked to %s; add it to the launcher with zc-app", tilde(dir))
				}
				return nil
			}, func(err error) {
				if err != nil {
					a.notify(briefLine(err.Error()), true)
					return
				}
				stopReceive()
				a.rescan()
				a.snd.Play("launch")
				a.notify("Installed "+o.App, false)
				for len(a.stack) > 1 {
					a.pop()
				}
				if h, ok := a.stack[0].(*Home); ok {
					h.recent = false
					h.key = "zc-" + o.Slug + ".desktop"
					h.keep(a)
				}
			})
		}
		if exists(dst) {
			confirm(a, "REPLACE APP?", o.App+" is already installed. Replace it? The old copy is kept next to it.",
				func() { install(true) })
			return
		}
		install(false)
	}))
}
