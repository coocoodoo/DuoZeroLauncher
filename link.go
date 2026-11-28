package main

// Duo Link: an Android car stereo shows this Cardputer's screen and sends keys back. The stereo
// runs the Duo Link app (android/duolink), which Duo itself serves at http://<address>:47140/.
//
// Protocol, TCP linkPort (numbers big-endian unless said otherwise):
//
//	The stereo opens with "DLK1", then both sides send messages [type u8][length u32][payload].
//	stereo:    HELLO {"name","token","v"} | PAIR {"code"} | KEY [code u16][down u8] | TEXT utf-8
//	           | PING | ACK [seq u32]
//	Cardputer: WELCOME {"name","w","h","token"} | NEEDPAIR {"name","left"} | WRONGCODE {"name","left"}
//	           | DENIED {"reason"} | FRAME [seq u32] zlib(tiles) | PONG
//	tiles:     repeated [x u16][y u16][w u16][h u16] then w*h RGB565 pixels, little-endian
//
// A connection that opens with "GET " is HTTP instead: "/" is a page, "/duolink.apk" the app.
// Discovery: "DUOLINK1 {"name","port","id"}" broadcast on UDP linkAnnouncePort every 2 s.
//
// A new stereo pairs with a 4-digit code shown on the Cardputer (5 tries, 2 minutes); it then
// gets a token, kept by the stereo; Duo keeps only its SHA-256 (duolink.json). One stereo is
// connected at a time. The screen is read back from the framebuffer, so apps Duo starts are shown
// too; keys are written into the keyboard's event device, so they reach those apps as well.
//
// Wi-Fi only: the stereo joins the Cardputer's own Wi-Fi (link_hotspot.go) or a shared one. A
// link over the USB cable (adbd's "tcp:47140" forward, or a USB network gadget) is refused: it
// kept dropping, so the cable only charges.

import (
	"bufio"
	"bytes"
	"compress/zlib"
	"crypto/rand"
	"crypto/sha256"
	"dslauncher/cp0"
	_ "embed"
	"encoding/binary"
	hexenc "encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

//go:embed assets/duolink.apk
var duoLinkAPK []byte

const (
	linkPort         = 47140
	linkAnnouncePort = 47141
	linkTries        = 5
	linkPairTime     = 2 * time.Minute
	linkFrameEvery   = 40 * time.Millisecond // up to 25 frames a second
	linkInFlight     = 2                     // frames sent but not yet acknowledged
	linkTileW        = 32
	linkTileH        = 10
	linkKeepAlive    = 3 * time.Second // a PONG at least this often, so the stereo knows the link is up
)

const (
	lkHello = 1 + iota
	lkPair
	lkKey
	lkText
	lkPing
	lkAck
)

const (
	lkWelcome = 0x81 + iota
	lkNeedPair
	lkDenied
	lkFrame
	lkPong
	lkWrongCode
	lkAudio // [s16le stereo 44.1 kHz]: what the Cardputer is playing, for stereos that asked
)

// linkHooks connect the server to the rest of Duo (and to fakes in tests).
type linkHooks struct {
	name    func() string                            // this Cardputer's name
	capture func(px []uint16) error                  // what's on the screen now (W*H RGB565)
	key     func(code int, down bool)                // press or release a key
	pair    func(stereo, code string, cancel func()) // show the pairing code
	paired  func()                                   // pairing over (either way): hide the code
	changed func()                                   // a stereo connected or left
	audio   func() bool                              // send the Cardputer's sound to stereos that ask
}

type linkStore struct {
	ID      string       `json:"id"`
	Stereos []linkStereo `json:"stereos"`
	SSID    string       `json:"ssid,omitempty"` // the Cardputer's own Wi-Fi for Duo Link
	Pass    string       `json:"pass,omitempty"` // and its password
}

// linkNoUSB is why a stereo linking over the USB cable is turned away.
const linkNoUSB = "Duo Link doesn't work over USB (the cable only charges the Cardputer). " +
	"Join the Cardputer's Wi-Fi instead: on the Cardputer, Settings > Duo Link > Set up a stereo."

type linkStereo struct {
	Name  string `json:"name"`
	Hash  string `json:"hash"` // SHA-256 of its token
	Added string `json:"added"`
}

func linkStorePath() string { return filepath.Join(filepath.Dir(configPath()), "duolink.json") }

func loadLinkStore() *linkStore {
	s := &linkStore{}
	if b, err := os.ReadFile(linkStorePath()); err == nil {
		json.Unmarshal(b, s)
	}
	if s.ID == "" {
		s.ID = randHex(8)
		s.save()
	}
	return s
}

func (s *linkStore) save() {
	b, _ := json.MarshalIndent(s, "", "  ")
	writeFileAtomic(linkStorePath(), string(b)+"\n", 0o600)
}

func tokenHash(t string) string { h := sha256.Sum256([]byte(t)); return hexenc.EncodeToString(h[:]) }

func randHex(n int) string {
	b := make([]byte, n)
	rand.Read(b)
	return hexenc.EncodeToString(b)
}

type linkServer struct {
	h    linkHooks
	addr string // listen address (":47140"; tests use 127.0.0.1:0)

	mu       sync.Mutex
	ln       net.Listener
	store    *linkStore
	client   *linkConn
	pairing  *linkConn
	blocked  map[string]time.Time
	wrong    map[string]linkWrong // wrong codes per address, across connections
	stop     chan struct{}
	running  bool
	announce bool
	alive    time.Duration // linkKeepAlive (shorter in tests)
	usbOK    bool          // tests: allow links from loopback (on the device that is adbd over USB)

	downloads atomic.Int32 // the app was downloaded this many times (the setup screen watches it)
}

// linkWrong counts an address's wrong pairing codes. The tries used to be counted per connection,
// so reconnecting gave five more guesses at a new code each time, with no wait.
type linkWrong struct {
	n    int
	last time.Time
}

// wrongCode notes a wrong code from ip and says whether the address must now wait.
func (s *linkServer) wrongCode(ip string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	w := s.wrong[ip]
	if time.Since(w.last) > 10*time.Minute {
		w.n = 0
	}
	w.n, w.last = w.n+1, time.Now()
	if w.n >= linkTries {
		delete(s.wrong, ip)
		s.blocked[ip] = time.Now().Add(time.Minute)
		return true
	}
	s.wrong[ip] = w
	return false
}

func (s *linkServer) apkDownloads() int { return int(s.downloads.Load()) }

// viaUSB: the connection came over the USB cable, through adbd's forward (from this machine's
// own loopback) or a USB network gadget (usb0, rndis0...).
func viaUSB(c net.Conn) bool {
	la, ok := c.LocalAddr().(*net.TCPAddr)
	if !ok {
		return false
	}
	if la.IP.IsLoopback() {
		return true
	}
	ifs, _ := net.Interfaces()
	for _, i := range ifs {
		if !isUSBIface(i.Name) {
			continue
		}
		addrs, _ := i.Addrs()
		for _, a := range addrs {
			if n, ok := a.(*net.IPNet); ok && n.IP.Equal(la.IP) {
				return true
			}
		}
	}
	return false
}

func isUSBIface(name string) bool {
	for _, p := range []string{"usb", "rndis", "ecm", "ncm"} {
		if strings.HasPrefix(name, p) {
			return true
		}
	}
	return false
}

func newLinkServer(h linkHooks) *linkServer {
	return &linkServer{h: h, addr: fmt.Sprintf(":%d", linkPort), blocked: map[string]time.Time{}, wrong: map[string]linkWrong{},
		announce: true,
		alive:    linkKeepAlive}
}

func (s *linkServer) start() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.running {
		return nil
	}
	ln, err := net.Listen("tcp4", s.addr)
	if err != nil {
		return err
	}
	s.ln, s.running, s.stop = ln, true, make(chan struct{})
	s.store = loadLinkStore()
	go s.accept(ln)
	if s.announce {
		go s.announceLoop(s.stop)
	}
	return nil
}

func (s *linkServer) shutdown() {
	s.mu.Lock()
	if !s.running {
		s.mu.Unlock()
		return
	}
	s.running = false
	close(s.stop)
	s.ln.Close()
	c, p := s.client, s.pairing
	s.client, s.pairing = nil, nil
	s.mu.Unlock()
	if c != nil {
		c.close()
	}
	if p != nil {
		p.close()
	}
}

func (s *linkServer) port() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ln == nil {
		return linkPort
	}
	return s.ln.Addr().(*net.TCPAddr).Port
}

// connected is the name of the stereo showing this screen, or "".
func (s *linkServer) connected() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.client == nil {
		return ""
	}
	return s.client.name
}

func (s *linkServer) isRunning() bool { s.mu.Lock(); defer s.mu.Unlock(); return s.running }

func (s *linkServer) stereos() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.store == nil {
		return len(loadLinkStore().Stereos)
	}
	return len(s.store.Stereos)
}

// forget unpairs every stereo (and drops the one connected).
func (s *linkServer) forget() {
	s.mu.Lock()
	if s.store == nil {
		s.store = loadLinkStore()
	}
	s.store.Stereos = nil
	s.store.save()
	c := s.client
	s.mu.Unlock()
	if c != nil {
		c.close()
	}
}

func (s *linkServer) announceLoop(stop chan struct{}) {
	c, err := net.ListenUDP("udp4", nil)
	if err != nil {
		return
	}
	defer c.Close()
	t := time.NewTicker(2 * time.Second)
	defer t.Stop()
	for {
		if s.connected() == "" {
			msg, _ := json.Marshal(map[string]any{"name": s.h.name(), "port": s.port(), "id": s.store.ID})
			m := append([]byte("DUOLINK1 "), msg...)
			c.WriteToUDP(m, &net.UDPAddr{IP: net.IPv4bcast, Port: linkAnnouncePort})
			for _, b := range broadcastAddrs() {
				c.WriteToUDP(m, &net.UDPAddr{IP: b, Port: linkAnnouncePort})
			}
		}
		select {
		case <-stop:
			return
		case <-t.C:
		}
	}
}

func (s *linkServer) accept(ln net.Listener) {
	for {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		go s.serve(c)
	}
}

// ---- one connection ----

type linkConn struct {
	s    *linkServer
	c    net.Conn
	r    *bufio.Reader
	name string
	ip   string

	wmu    sync.Mutex
	once   sync.Once
	done   chan struct{}
	acked  chan uint32
	heldMu sync.Mutex
	held   map[int]bool
	code   string // pairing code while pairing
	tries  int
}

func (lc *linkConn) close() {
	lc.once.Do(func() {
		close(lc.done)
		lc.c.Close()
	})
}

func (lc *linkConn) send(typ byte, payload []byte) error {
	lc.wmu.Lock()
	defer lc.wmu.Unlock()
	hdr := []byte{typ, 0, 0, 0, 0}
	binary.BigEndian.PutUint32(hdr[1:], uint32(len(payload)))
	lc.c.SetWriteDeadline(time.Now().Add(5 * time.Second))
	bufs := net.Buffers{hdr, payload} // one writev: the frame isn't copied first
	if _, err := bufs.WriteTo(lc.c); err != nil {
		lc.close()
		return err
	}
	return nil
}

func (lc *linkConn) sendJSON(typ byte, v any) error {
	b, _ := json.Marshal(v)
	return lc.send(typ, b)
}

func (lc *linkConn) read() (byte, []byte, error) {
	var hdr [5]byte
	if _, err := io.ReadFull(lc.r, hdr[:]); err != nil {
		return 0, nil, err
	}
	n := binary.BigEndian.Uint32(hdr[1:])
	if n > 64<<10 {
		return 0, nil, errors.New("message too big")
	}
	p := make([]byte, n)
	_, err := io.ReadFull(lc.r, p)
	return hdr[0], p, err
}

func (s *linkServer) serve(c net.Conn) {
	ip, _, _ := net.SplitHostPort(c.RemoteAddr().String())
	lc := &linkConn{s: s, c: c, r: bufio.NewReaderSize(c, 8192), ip: ip, done: make(chan struct{}),
		acked: make(chan uint32, 16), held: map[int]bool{}}
	defer lc.close()
	c.SetReadDeadline(time.Now().Add(10 * time.Second))
	magic := make([]byte, 4)
	if _, err := io.ReadFull(lc.r, magic); err != nil {
		return
	}
	if string(magic) == "GET " {
		s.serveHTTP(lc)
		return
	}
	if string(magic) != "DLK1" {
		return
	}
	// HELLO is read before any refusal: closing with it unread would reset the connection, and
	// the stereo could lose the reason
	typ, p, err := lc.read()
	if err != nil || typ != lkHello {
		return
	}
	s.mu.Lock()
	until, usbOK := s.blocked[ip], s.usbOK
	s.mu.Unlock()
	if !usbOK && viaUSB(c) {
		lc.sendJSON(lkDenied, map[string]string{"reason": linkNoUSB})
		return
	}
	if time.Now().Before(until) {
		lc.sendJSON(lkDenied, map[string]string{"reason": "Too many wrong codes. Wait a minute and try again."})
		return
	}
	var hello struct {
		Name  string `json:"name"`
		Token string `json:"token"`
		Audio bool   `json:"audio"` // the app can play the Cardputer's sound
	}
	json.Unmarshal(p, &hello)
	lc.name = cleanName(hello.Name)
	if lc.name == "" {
		lc.name = "Car stereo"
	}
	token := ""
	if hello.Token != "" && s.knownToken(hello.Token) {
		token = hello.Token
	} else if token, err = s.pair(lc); err != nil {
		return
	}
	s.mu.Lock()
	if !s.running {
		s.mu.Unlock()
		return
	}
	old := s.client
	s.client = lc
	s.mu.Unlock()
	if old != nil {
		old.close()
	}
	defer func() {
		lc.releaseAll()
		s.mu.Lock()
		if s.client == lc {
			s.client = nil
		}
		s.mu.Unlock()
		s.h.changed()
	}()
	welcome := map[string]any{"name": s.h.name(), "w": W, "h": H, "id": s.store.ID}
	if token != hello.Token {
		welcome["token"] = token // new: the stereo keeps it for next time
	}
	if lc.sendJSON(lkWelcome, welcome) != nil {
		return
	}
	s.h.changed()
	go lc.stream()
	if hello.Audio && s.h.audio != nil && s.h.audio() {
		go lc.streamAudio()
	}
	for {
		c.SetReadDeadline(time.Now().Add(20 * time.Second))
		typ, p, err := lc.read()
		if err != nil {
			return
		}
		switch typ {
		case lkKey:
			if len(p) == 3 {
				lc.key(int(binary.BigEndian.Uint16(p)), p[2] != 0)
			}
		case lkText:
			lc.text(string(p))
		case lkPing:
			lc.send(lkPong, nil)
		case lkAck:
			if len(p) == 4 {
				select {
				case lc.acked <- binary.BigEndian.Uint32(p):
				default:
				}
			}
		}
	}
}

func cleanName(s string) string {
	s = strings.Map(func(r rune) rune {
		if r < ' ' || r == 0x7f {
			return -1
		}
		return r
	}, strings.TrimSpace(s))
	if r := []rune(s); len(r) > 24 {
		s = string(r[:24])
	}
	return s
}

func (s *linkServer) knownToken(t string) bool {
	h := tokenHash(t)
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, st := range s.store.Stereos {
		if st.Hash == h {
			return true
		}
	}
	return false
}

// pair asks for the code shown on the Cardputer; on success the stereo is remembered.
func (s *linkServer) pair(lc *linkConn) (string, error) {
	s.mu.Lock()
	if s.pairing != nil {
		s.mu.Unlock()
		lc.sendJSON(lkDenied, map[string]string{"reason": "Another stereo is pairing right now."})
		return "", errors.New("busy")
	}
	n, _ := rand.Int(rand.Reader, big.NewInt(10000))
	lc.code, lc.tries = fmt.Sprintf("%04d", n.Int64()), linkTries
	s.pairing = lc
	s.mu.Unlock()
	cancelled := make(chan struct{})
	var cancelOnce sync.Once
	s.h.pair(lc.name, lc.code, func() { cancelOnce.Do(func() { close(cancelled) }) })
	defer func() {
		s.mu.Lock()
		if s.pairing == lc {
			s.pairing = nil
		}
		s.mu.Unlock()
		s.h.paired()
	}()
	deny := func(why string) (string, error) {
		lc.sendJSON(lkDenied, map[string]string{"reason": why})
		return "", errors.New(why)
	}
	if lc.sendJSON(lkNeedPair, map[string]any{"name": s.h.name(), "left": lc.tries}) != nil {
		return "", errors.New("gone")
	}
	type msg struct {
		p   []byte
		err error
	}
	msgs := make(chan msg, 1)
	// readPair reads until the next PAIR message (answering pings meanwhile): only one reader
	// runs at a time, and none once pairing is over
	readPair := func() {
		go func() {
			for {
				typ, p, err := lc.read()
				if err == nil && typ == lkPing {
					lc.send(lkPong, nil)
					continue
				}
				if err == nil && typ != lkPair {
					continue
				}
				select {
				case msgs <- msg{p, err}:
				case <-lc.done:
				}
				return
			}
		}()
	}
	lc.c.SetReadDeadline(time.Now().Add(linkPairTime + 10*time.Second))
	readPair()
	deadline := time.NewTimer(linkPairTime)
	defer deadline.Stop()
	alive := time.NewTicker(s.alive) // the app gives up after 15 s of silence; typing takes longer
	defer alive.Stop()
	for {
		select {
		case <-alive.C:
			lc.send(lkPong, nil)
		case <-cancelled:
			return deny("Refused on the Cardputer.")
		case <-deadline.C:
			return deny("The code wasn't typed in time.")
		case m := <-msgs:
			if m.err != nil {
				return "", m.err
			}
			var pr struct {
				Code string `json:"code"`
			}
			json.Unmarshal(m.p, &pr)
			if strings.TrimSpace(pr.Code) == lc.code {
				token := randHex(16)
				s.mu.Lock()
				delete(s.wrong, lc.ip)
				s.store.Stereos = append(s.store.Stereos, linkStereo{Name: lc.name, Hash: tokenHash(token),
					Added: time.Now().Format(time.RFC3339)})
				s.store.save()
				s.mu.Unlock()
				return token, nil
			}
			lc.tries--
			if s.wrongCode(lc.ip) || lc.tries <= 0 {
				return deny("Too many wrong codes.")
			}
			if lc.sendJSON(lkWrongCode, map[string]any{"name": s.h.name(), "left": lc.tries}) != nil {
				return "", errors.New("gone")
			}
			readPair()
		}
	}
}

func knownLinkKey(code int) bool                   { return cp0.KnownKey(code) }
func keyForRune(r rune) (code int, shift, ok bool) { return cp0.KeyFor(r) }

// ---- keys ----

func (lc *linkConn) key(code int, down bool) {
	if !knownLinkKey(code) {
		return
	}
	lc.heldMu.Lock()
	was := lc.held[code]
	if down {
		lc.held[code] = true
	} else {
		delete(lc.held, code)
	}
	lc.heldMu.Unlock()
	if !down && !was {
		return // a release for a press we never passed on
	}
	lc.s.h.key(code, down)
}

// text types each character with the key (and Shift) that makes it on the Cardputer.
func (lc *linkConn) text(t string) {
	n := 0
	for _, r := range t {
		if n++; n > 256 {
			return
		}
		code, shift, ok := keyForRune(r)
		if !ok {
			continue
		}
		if shift {
			lc.s.h.key(42, true)
		}
		lc.s.h.key(code, true)
		lc.s.h.key(code, false)
		if shift {
			lc.s.h.key(42, false)
		}
	}
}

// releaseAll lets go of every key the stereo still holds (it went away mid-press).
func (lc *linkConn) releaseAll() {
	lc.heldMu.Lock()
	var codes []int
	for c := range lc.held {
		codes = append(codes, c)
	}
	lc.held = map[int]bool{}
	lc.heldMu.Unlock()
	for _, c := range codes {
		lc.s.h.key(c, false)
	}
}

// ---- the picture ----

func (lc *linkConn) stream() {
	cur, prev := make([]uint16, W*H), make([]uint16, W*H)
	first := true
	var seq, acked uint32
	var buf bytes.Buffer
	zw, _ := zlib.NewWriterLevel(&buf, zlib.BestSpeed)
	t := time.NewTicker(linkFrameEvery)
	defer t.Stop()
	lastSent := time.Now()
	for {
		select {
		case <-lc.done:
			return
		case a := <-lc.acked:
			if a > acked {
				acked = a
			}
			continue
		case <-t.C:
		}
		if seq-acked >= linkInFlight {
			continue // the stereo is behind: skip this frame
		}
		if lc.s.h.capture(cur) != nil {
			continue
		}
		rects := changedRects(prev, cur, first)
		if len(rects) == 0 {
			if time.Since(lastSent) > lc.s.alive {
				lc.send(lkPong, nil) // keeps the connection alive
				lastSent = time.Now()
			}
			continue
		}
		first = false
		buf.Reset()
		seq++
		binary.Write(&buf, binary.BigEndian, seq)
		zw.Reset(&buf)
		writeTiles(zw, cur, rects)
		zw.Close()
		if lc.send(lkFrame, buf.Bytes()) != nil {
			return
		}
		lastSent = time.Now()
		copy(prev, cur)
	}
}

type linkRect struct{ x, y, w, h int }

// changedRects compares the screen in tiles and joins neighbouring changed tiles in a row.
func changedRects(prev, cur []uint16, all bool) []linkRect {
	if all {
		return []linkRect{{0, 0, W, H}}
	}
	var out []linkRect
	for ty := 0; ty < H; ty += linkTileH {
		th := min(linkTileH, H-ty)
		run := -1
		for tx := 0; tx <= W; tx += linkTileW {
			changed := false
			if tx < W {
				tw := min(linkTileW, W-tx)
			scan:
				for y := ty; y < ty+th; y++ {
					row := y * W
					for x := tx; x < tx+tw; x++ {
						if prev[row+x] != cur[row+x] {
							changed = true
							break scan
						}
					}
				}
			}
			switch {
			case changed && run < 0:
				run = tx
			case !changed && run >= 0:
				out = append(out, linkRect{run, ty, min(tx, W) - run, th})
				run = -1
			}
		}
	}
	return out
}

func writeTiles(w io.Writer, px []uint16, rects []linkRect) {
	for _, r := range rects {
		hdr := make([]byte, 8)
		binary.BigEndian.PutUint16(hdr[0:], uint16(r.x))
		binary.BigEndian.PutUint16(hdr[2:], uint16(r.y))
		binary.BigEndian.PutUint16(hdr[4:], uint16(r.w))
		binary.BigEndian.PutUint16(hdr[6:], uint16(r.h))
		w.Write(hdr)
		row := make([]byte, r.w*2)
		for y := r.y; y < r.y+r.h; y++ {
			for x := 0; x < r.w; x++ {
				binary.LittleEndian.PutUint16(row[x*2:], px[y*W+r.x+x])
			}
			w.Write(row)
		}
	}
}

// ---- the app, over HTTP ----

func (s *linkServer) serveHTTP(lc *linkConn) {
	line, err := lc.r.ReadString('\n')
	if err != nil {
		return
	}
	path := strings.Fields(line)
	for { // the headers
		l, err := lc.r.ReadString('\n')
		if err != nil || strings.TrimSpace(l) == "" {
			break
		}
	}
	lc.c.SetWriteDeadline(time.Now().Add(60 * time.Second))
	w := bufio.NewWriter(lc.c)
	defer w.Flush()
	if len(path) > 0 && strings.HasPrefix(path[0], "/duolink.apk") {
		s.downloads.Add(1)
		fmt.Fprintf(w, "HTTP/1.0 200 OK\r\nContent-Type: application/vnd.android.package-archive\r\n"+
			"Content-Disposition: attachment; filename=\"duolink.apk\"\r\nContent-Length: %d\r\nConnection: close\r\n\r\n", len(duoLinkAPK))
		w.Write(duoLinkAPK)
		return
	}
	page := `<!DOCTYPE html><html><head><meta charset="utf-8"><meta name="viewport" content="width=device-width">
<title>Duo Link</title><style>body{font-family:sans-serif;background:#101418;color:#fff;padding:2em;font-size:20px}
a{display:inline-block;background:#fbd300;color:#101418;padding:.7em 1.4em;border-radius:10px;text-decoration:none;font-weight:bold}
li{margin:.5em 0}</style></head><body><h1>Duo Link</h1>
<p>Shows ` + htmlEscape(s.h.name()) + `'s screen on this car stereo.</p>
<p><a href="/duolink.apk">Download Duo Link</a></p><ol>
<li>Open the downloaded file to install it. If Android asks, allow installing apps from this browser (or the file manager).</li>
<li>Open Duo Link. It finds the Cardputer on this Wi-Fi by itself.</li>
<li>Type the code the Cardputer shows.</li></ol>
<p style="font-size:16px;color:#aab">If Android says this Wi-Fi has no internet, choose to stay connected: Duo Link
talks straight to the Cardputer. Duo Link works over Wi-Fi only; a USB cable just charges the Cardputer.</p></body></html>`
	fmt.Fprintf(w, "HTTP/1.0 200 OK\r\nContent-Type: text/html; charset=utf-8\r\nContent-Length: %d\r\nConnection: close\r\n\r\n%s", len(page), page)
}

func htmlEscape(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;").Replace(s)
}
