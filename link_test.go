package main

import (
	"bufio"
	"bytes"
	"compress/zlib"
	"dslauncher/cp0"
	"encoding/binary"
	"encoding/json"
	"io"
	"net"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// linkTestClient speaks Duo Link like the Android app does.
type linkTestClient struct {
	t  *testing.T
	c  net.Conn
	r  *bufio.Reader
	px []uint16
}

func dialLink(t *testing.T, s *linkServer, name, token string) *linkTestClient {
	t.Helper()
	c, err := net.Dial("tcp", "127.0.0.1:"+itoa(s.port()))
	if err != nil {
		t.Fatal(err)
	}
	c.SetDeadline(time.Now().Add(10 * time.Second))
	lc := &linkTestClient{t: t, c: c, r: bufio.NewReader(c), px: make([]uint16, W*H)}
	c.Write([]byte("DLK1"))
	lc.sendJSON(lkHello, map[string]any{"name": name, "token": token, "v": 1})
	return lc
}

func (lc *linkTestClient) send(typ byte, p []byte) {
	hdr := []byte{typ, 0, 0, 0, 0}
	binary.BigEndian.PutUint32(hdr[1:], uint32(len(p)))
	lc.c.Write(append(hdr, p...))
}

func (lc *linkTestClient) sendJSON(typ byte, v any) {
	b, _ := json.Marshal(v)
	lc.send(typ, b)
}

func (lc *linkTestClient) key(code int, down bool) {
	p := []byte{0, 0, 0}
	binary.BigEndian.PutUint16(p, uint16(code))
	if down {
		p[2] = 1
	}
	lc.send(lkKey, p)
}

// next reads messages until one that isn't a PONG; frames are applied to px and acknowledged.
func (lc *linkTestClient) next() (byte, map[string]any) {
	lc.t.Helper()
	for {
		var hdr [5]byte
		if _, err := io.ReadFull(lc.r, hdr[:]); err != nil {
			lc.t.Fatalf("read: %v", err)
		}
		p := make([]byte, binary.BigEndian.Uint32(hdr[1:]))
		io.ReadFull(lc.r, p)
		switch hdr[0] {
		case lkPong:
			continue
		case lkFrame:
			zr, err := zlib.NewReader(bytes.NewReader(p[4:]))
			if err != nil {
				lc.t.Fatal(err)
			}
			tiles, _ := io.ReadAll(zr)
			for i := 0; i+8 <= len(tiles); {
				x, y := int(binary.BigEndian.Uint16(tiles[i:])), int(binary.BigEndian.Uint16(tiles[i+2:]))
				w, h := int(binary.BigEndian.Uint16(tiles[i+4:])), int(binary.BigEndian.Uint16(tiles[i+6:]))
				i += 8
				for r := 0; r < h; r++ {
					for c := 0; c < w; c++ {
						lc.px[(y+r)*W+x+c] = binary.LittleEndian.Uint16(tiles[i:])
						i += 2
					}
				}
			}
			lc.send(lkAck, p[:4])
			return lkFrame, map[string]any{"bytes": float64(len(p))}
		}
		m := map[string]any{}
		json.Unmarshal(p, &m)
		return hdr[0], m
	}
}

type linkFakes struct {
	mu     sync.Mutex
	screen []uint16
	keys   []string
	code   string
	cancel func()
	paired int
}

func (f *linkFakes) keyLog() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return strings.Join(f.keys, " ")
}

func newTestLink(t *testing.T) (*linkServer, *linkFakes) {
	t.Helper()
	t.Setenv("DSL_CONFIG", filepath.Join(t.TempDir(), "config"))
	f := &linkFakes{screen: make([]uint16, W*H)}
	s := newLinkServer(linkHooks{
		name: func() string { return "zero" },
		capture: func(px []uint16) error {
			f.mu.Lock()
			copy(px, f.screen)
			f.mu.Unlock()
			return nil
		},
		key: func(code int, down bool) {
			f.mu.Lock()
			f.keys = append(f.keys, itoa(code)+map[bool]string{true: "v", false: "^"}[down])
			f.mu.Unlock()
		},
		pair: func(stereo, code string, cancel func()) {
			f.mu.Lock()
			f.code, f.cancel = code, cancel
			f.mu.Unlock()
		},
		paired:  func() { f.mu.Lock(); f.paired++; f.mu.Unlock() },
		changed: func() {},
	})
	s.addr, s.announce = "127.0.0.1:0", false
	if err := s.start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.shutdown)
	return s, f
}

func TestDuoLink(t *testing.T) {
	s, f := newTestLink(t)
	for i := range f.screen {
		f.screen[i] = uint16(i * 7)
	}

	// a new stereo pairs with the code shown on the Cardputer
	c := dialLink(t, s, "Radio\x01Pro", "")
	if typ, m := c.next(); typ != lkNeedPair || m["name"] != "zero" || m["left"] != float64(linkTries) {
		t.Fatalf("want NEEDPAIR, got %#x %v", typ, m)
	}
	f.mu.Lock()
	code := f.code
	f.mu.Unlock()
	if len(code) != 4 {
		t.Fatalf("pairing code %q", code)
	}
	wrong := "0000"
	if code == wrong {
		wrong = "1111"
	}
	c.sendJSON(lkPair, map[string]string{"code": wrong})
	if typ, m := c.next(); typ != lkWrongCode || m["left"] != float64(linkTries-1) {
		t.Fatalf("wrong code: %#x %v", typ, m)
	}
	c.sendJSON(lkPair, map[string]string{"code": code})
	typ, m := c.next()
	token, _ := m["token"].(string)
	if typ != lkWelcome || token == "" || m["w"] != float64(W) || m["h"] != float64(H) {
		t.Fatalf("want WELCOME with a token, got %#x %v", typ, m)
	}
	if s.connected() != "RadioPro" || s.stereos() != 1 {
		t.Fatalf("connected %q, %d paired", s.connected(), s.stereos())
	}
	if b, _ := json.Marshal(loadLinkStore()); strings.Contains(string(b), token) {
		t.Error("the token itself is stored; only its hash should be")
	}

	// the first frame is the whole screen
	if typ, _ := c.next(); typ != lkFrame {
		t.Fatalf("want a frame, got %#x", typ)
	}
	for i := range f.screen {
		if c.px[i] != f.screen[i] {
			t.Fatalf("pixel %d: %d, want %d", i, c.px[i], f.screen[i])
		}
	}
	// then only what changed
	f.mu.Lock()
	for y := 50; y < 60; y++ {
		for x := 100; x < 140; x++ {
			f.screen[y*W+x] = 0xffff
		}
	}
	f.mu.Unlock()
	typ, m = c.next()
	if typ != lkFrame || m["bytes"].(float64) > 2000 {
		t.Fatalf("a small change sent %v bytes", m["bytes"])
	}
	if c.px[55*W+120] != 0xffff || c.px[0] != f.screen[0] {
		t.Fatal("the change didn't arrive")
	}

	// keys: only the Cardputer's own; Tab held is still held until released; text is typed
	c.key(15, true)
	c.key(116, true) // KEY_POWER: refused
	c.key(15, false)
	c.send(lkText, []byte("Hi!"))
	c.key(28, true) // Enter held when the stereo goes away: released
	time.Sleep(200 * time.Millisecond)
	c.c.Close()
	deadline := time.Now().Add(3 * time.Second)
	for s.connected() != "" && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if got, want := f.keyLog(), "15v 15^ 42v 35v 35^ 42^ 23v 23^ 42v 2v 2^ 42^ 28v 28^"; got != want {
		t.Errorf("keys %q, want %q", got, want)
	}

	// next time the token is enough
	c = dialLink(t, s, "Radio", token)
	if typ, m := c.next(); typ != lkWelcome || m["token"] != nil {
		t.Fatalf("with a token: %#x %v", typ, m)
	}
	c.c.Close()

	// a forgotten stereo has to pair again
	s.forget()
	c = dialLink(t, s, "Radio", token)
	if typ, _ := c.next(); typ != lkNeedPair {
		t.Fatalf("after forget: %#x", typ)
	}
	f.mu.Lock()
	cancel := f.cancel
	f.mu.Unlock()
	cancel() // refused on the Cardputer
	if typ, m := c.next(); typ != lkDenied || !strings.Contains(m["reason"].(string), "Refused") {
		t.Fatalf("refused: %#x %v", typ, m)
	}
	c.c.Close()
}

// While the code is being typed the Cardputer keeps talking, or the app would time out.
func TestDuoLinkKeepAliveWhilePairing(t *testing.T) {
	s, _ := newTestLink(t)
	s.alive = 50 * time.Millisecond
	c := dialLink(t, s, "Radio", "")
	c.next() // NEEDPAIR
	c.c.SetReadDeadline(time.Now().Add(time.Second))
	var hdr [5]byte
	if _, err := io.ReadFull(c.r, hdr[:]); err != nil || hdr[0] != lkPong {
		t.Fatalf("no keep-alive while pairing: %v %#x", err, hdr[0])
	}
	c.c.Close()
}

func TestDuoLinkWrongCodes(t *testing.T) {
	s, f := newTestLink(t)
	c := dialLink(t, s, "Radio", "made-up-token")
	c.next()
	f.mu.Lock()
	code := f.code
	f.mu.Unlock()
	bad := "9999"
	if code == bad {
		bad = "8888"
	}
	for i := 0; i < linkTries-1; i++ {
		c.sendJSON(lkPair, map[string]string{"code": bad})
		if typ, _ := c.next(); typ != lkWrongCode {
			t.Fatalf("try %d: %#x", i, typ)
		}
	}
	c.sendJSON(lkPair, map[string]string{"code": bad})
	if typ, _ := c.next(); typ != lkDenied {
		t.Fatalf("after %d wrong codes: %#x", linkTries, typ)
	}
	c.c.Close()
	// that address waits a minute
	c = dialLink(t, s, "Radio", "")
	if typ, m := c.next(); typ != lkDenied || !strings.Contains(m["reason"].(string), "Wait") {
		t.Fatalf("blocked address: %#x %v", typ, m)
	}
	if s.stereos() != 0 {
		t.Error("nothing should be paired")
	}
}

func TestDuoLinkServesTheApp(t *testing.T) {
	s, _ := newTestLink(t)
	get := func(path string) string {
		c, err := net.Dial("tcp", "127.0.0.1:"+itoa(s.port()))
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
		c.SetDeadline(time.Now().Add(10 * time.Second))
		io.WriteString(c, "GET "+path+" HTTP/1.1\r\nHost: x\r\nUser-Agent: test\r\n\r\n")
		b, _ := io.ReadAll(c)
		return string(b)
	}
	if page := get("/"); !strings.Contains(page, "200 OK") || !strings.Contains(page, `href="/duolink.apk"`) {
		t.Fatalf("page: %.300s", page)
	}
	apk := get("/duolink.apk")
	i := strings.Index(apk, "\r\n\r\n")
	if !strings.Contains(apk, "application/vnd.android.package-archive") || i < 0 || apk[i+4:] != string(duoLinkAPK) {
		t.Fatal("the APK didn't come through whole")
	}
	if len(duoLinkAPK) < 1000 || string(duoLinkAPK[:2]) != "PK" {
		t.Fatal("assets/duolink.apk isn't a built app (run android/duolink/build.sh)")
	}
}

func TestChangedRects(t *testing.T) {
	a, b := make([]uint16, W*H), make([]uint16, W*H)
	if r := changedRects(a, b, false); len(r) != 0 {
		t.Fatalf("no change: %v", r)
	}
	b[5*W+40], b[5*W+70], b[165*W+319] = 1, 1, 1 // two neighbouring tiles in a row join; the corner stays apart
	r := changedRects(a, b, false)
	if len(r) != 2 || r[0] != (linkRect{32, 0, 64, 10}) || r[1] != (linkRect{288, 160, 32, 10}) {
		t.Fatalf("rects %v", r)
	}
}

func TestKeyForRune(t *testing.T) {
	for r, want := range map[rune][2]int{'a': {30, 0}, 'A': {30, 1}, '1': {2, 0}, '!': {2, 1}, ' ': {57, 0}} {
		code, shift, ok := keyForRune(r)
		if !ok || code != want[0] || (shift != (want[1] == 1)) {
			t.Errorf("%q: %d %v %v", r, code, shift, ok)
		}
	}
	if knownLinkKey(116) || !knownLinkKey(15) || !knownLinkKey(103) {
		t.Error("knownLinkKey")
	}
	if len(inputEvent(1, 30, 1)) != map[bool]int{true: 24, false: 16}[strings.Contains(itoa(1<<40), "1099511627776")] {
		t.Error("input_event size")
	}
}

// The pairing screen and Settings > Launcher > Duo Link (DSL_DOC_SHOTS=<dir> saves them).
func TestDuoLinkScreens(t *testing.T) {
	t.Setenv("DSL_CONFIG", filepath.Join(t.TempDir(), "config"))
	a := testApp(t)
	a.link = a.newLink()
	refused := false
	a.push(&LinkPair{stereo: "Car stereo", code: "0427", cancel: func() { refused = true }})
	a.draw()
	a.saveShot("link-pair")
	a.top().Key(a, cp0.Key{Code: 1, Name: "ESC"})
	if !refused || len(a.stack) != 1 {
		t.Fatal("Esc should refuse the stereo and close the code")
	}
	a.push(linkPage(a))
	a.draw()
	a.saveShot("link-settings")
	l := a.top().(*RowList)
	if l.Rows[0].Label != "Duo Link" || l.Rows[1].Value != "OFF" {
		t.Fatalf("rows %+v %+v", l.Rows[0], l.Rows[1])
	}
	a.push(&DocText{title: "DUO LINK", paras: linkHelp("192.168.1.20:47140")})
	a.draw()
	a.saveShot("link-help")
}
