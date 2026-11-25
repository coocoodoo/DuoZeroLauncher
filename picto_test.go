package main

import (
	"fmt"
	"net"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestWebSocketRoundTrip(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				ws, err := wsServe(c)
				if err != nil {
					c.Close()
					return
				}
				defer ws.Close()
				for {
					m, err := ws.Read()
					if err != nil {
						return
					}
					ws.writeFrame(0x9, []byte("are you there")) // a ping the client must answer by itself
					ws.WriteText("echo:" + m)
				}
			}()
		}
	}()
	// a plain HTTP request is refused politely
	if c, err := net.Dial("tcp", ln.Addr().String()); err == nil {
		fmt.Fprintf(c, "GET / HTTP/1.1\r\nHost: x\r\n\r\n")
		b := make([]byte, 64)
		n, _ := c.Read(b)
		if !strings.Contains(string(b[:n]), "426") {
			t.Errorf("plain HTTP got %q", b[:n])
		}
		c.Close()
	}
	u, err := wsURL(ln.Addr().String(), pcPort)
	if err != nil {
		t.Fatal(err)
	}
	c, err := wsDial(u, 3*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	for _, m := range []string{"hello", strings.Repeat("x", 300), strings.Repeat("y", 70000)} {
		if err := c.WriteText(m); err != nil {
			t.Fatal(err)
		}
		got, err := c.Read()
		if err != nil || got != "echo:"+m {
			t.Fatalf("got %d bytes, err %v", len(got), err)
		}
	}
}

func TestWsURL(t *testing.T) {
	for in, want := range map[string]string{
		"192.168.1.5":           "ws://192.168.1.5:47120/",
		"chat.local:8080":       "ws://chat.local:8080/",
		"http://example.com":    "ws://example.com:47120/",
		"wss://example.com/x":   "wss://example.com:443/x",
		"ws://[fe80::1]:9000/p": "ws://[fe80::1]:9000/p",
	} {
		u, err := wsURL(in, pcPort)
		if err != nil || u.String() != want {
			t.Errorf("wsURL(%q) = %v, %v; want %s", in, u, err, want)
		}
	}
	if _, err := wsURL("ftp://x", pcPort); err == nil {
		t.Error("ftp accepted")
	}
}

// eventLog collects a client's events.
type eventLog struct {
	mu sync.Mutex
	ev []pcEvent
}

func (l *eventLog) add(e pcEvent) { l.mu.Lock(); l.ev = append(l.ev, e); l.mu.Unlock() }

func (l *eventLog) wait(t *testing.T, typ string) pcEvent {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		l.mu.Lock()
		for i, e := range l.ev {
			if e.Type == typ {
				l.ev = append(l.ev[:i], l.ev[i+1:]...)
				l.mu.Unlock()
				return e
			}
		}
		l.mu.Unlock()
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("no %s", typ)
	return pcEvent{}
}

func TestPictoChatHost(t *testing.T) {
	srv, err := pcHost("127.0.0.1:0", "test")
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	addr := fmt.Sprintf("127.0.0.1:%d", srv.Port())
	var la, lb eventLog
	a, err := pcConnect(addr, pcPlayer{Name: "Pi Zero!!", Color: 0x3a7bd5}, la.add)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	if p := la.wait(t, "sv_nameVerified").Player; p.Name != "PiZero" {
		t.Errorf("name not cleaned: %q", p.Name)
	}
	if ids := la.wait(t, "sv_roomIds"); len(ids.Count) != 4 {
		t.Errorf("room counts %v", ids.Count)
	}
	b, err := pcConnect(addr, pcPlayer{Name: "Rin", Color: 0xffffff}, lb.add)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	if p := lb.wait(t, "sv_nameVerified").Player; p.Color == 0xffffff {
		t.Error("white is unreadable and should be replaced")
	}
	a.Join("room_b", pcPlayer{Name: "PiZero"})
	if la.wait(t, "sv_roomData").ID != "room_b" {
		t.Fatal("wrong room")
	}
	if j := la.wait(t, "sv_playerJoined"); j.Player.Name != "PiZero" { // you see yourself come in
		t.Errorf("own join seen as %q", j.Player.Name)
	}
	b.Join("room_b", pcPlayer{Name: "Rin"})
	lb.wait(t, "sv_roomData")
	if j := la.wait(t, "sv_playerJoined"); j.Player.Name != "Rin" {
		t.Errorf("join seen as %q", j.Player.Name)
	}
	if n := srv.People(); n[1] != 2 {
		t.Errorf("room B has %d", n[1])
	}

	c := newCompose()
	for _, r := range "hello there" {
		c.typeRune(r)
	}
	m := c.message(pcPlayer{Name: "Impostor", Color: 1})
	a.Say(m)
	got := lb.wait(t, "sv_receivedMessage").Message
	if got.Player.Name != "PiZero" {
		t.Errorf("the server must say who sent it, got %q", got.Player.Name)
	}
	if len(got.Textboxes) != 1 || got.Textboxes[0].Text != "hello there" || got.Lines != 1 {
		t.Errorf("message changed on the way: %+v", got)
	}
	a.Say(m) // too soon after the last one: dropped
	time.Sleep(200 * time.Millisecond)
	lb.mu.Lock()
	for _, e := range lb.ev {
		if e.Type == "sv_receivedMessage" {
			t.Error("rate limit let a second message through")
		}
	}
	lb.mu.Unlock()

	a.Leave()
	la.wait(t, "sv_leaveRoom")
	if l := lb.wait(t, "sv_playerLeft"); l.Player.Name != "PiZero" {
		t.Errorf("leave seen as %q", l.Player.Name)
	}
	b.Close()
	lb.wait(t, "closed")
}

func TestCompose(t *testing.T) {
	c := newCompose()
	long := strings.Repeat("word ", 30)
	for _, r := range long {
		c.typeRune(r)
	}
	if c.row == 0 || len(c.lines[1]) == 0 {
		t.Fatal("text did not wrap onto the next line")
	}
	for row, l := range c.lines {
		if textW(string(l), 1, false) > pcRowWidth(row) {
			t.Errorf("line %d too wide", row)
		}
	}
	m := c.message(pcPlayer{Name: "a"})
	if m.Lines < 2 {
		t.Errorf("lines = %d", m.Lines)
	}
	if m.Textboxes[0].X != float64(pcRowX(0)+pcBoxX) || m.Textboxes[0].Y != float64(pcRowY[0]+pcBoxY) {
		t.Errorf("first line at %v,%v", m.Textboxes[0].X, m.Textboxes[0].Y)
	}
	// one line of text is one line; drawing low makes it five
	c = newCompose()
	c.typeRune('x')
	if n := c.message(pcPlayer{}).Lines; n != 1 {
		t.Errorf("one char = %d lines", n)
	}
	c.px, c.py = 100, 78
	c.penDown(true)
	c.move(5, 0)
	c.penDown(false)
	if n := c.message(pcPlayer{}).Lines; n != 5 {
		t.Errorf("a stroke at the bottom = %d lines", n)
	}
	if !c.undo() || c.undo() {
		t.Error("one stroke, one undo")
	}
	// the pen stays out of the name tag
	c.px, c.py = 120, 10
	c.move(-80, 0)
	if c.px <= pcTagW && c.py <= pcTagH {
		t.Errorf("pen went into the name tag: %d,%d", c.px, c.py)
	}
	// COPY puts lines back on lines
	c = newCompose()
	for _, r := range "copy me" {
		c.typeRune(r)
	}
	d := newCompose()
	d.copyFrom(c.message(pcPlayer{}))
	if string(d.lines[0]) != "copy me" || len(d.extra) != 0 {
		t.Errorf("copy: %q, extra %v", string(d.lines[0]), d.extra)
	}
	if newCompose().empty() == false {
		t.Error("a new box should be empty")
	}
}

func TestRenderMessage(t *testing.T) {
	c := newCompose()
	c.px, c.py = 120, 40
	c.penDown(true)
	c.move(30, 10)
	c.penDown(false)
	m := c.message(pcPlayer{Name: "Rin", Color: 0xe0882a})
	m.Drawing = append(m.Drawing, pcPoint{X: pcBoxX + 10, Y: pcBoxY + 5, Type: 2}, pcPoint{X: pcBoxX + 40, Y: pcBoxY + 8, Type: 0})
	img := renderPcMessage(m)
	if img.w != pcBoxW || img.h != pcRowH(m.Lines) {
		t.Fatalf("image %dx%d", img.w, img.h)
	}
	ink := cPcInk.c565()
	found := false
	for i, p := range img.px {
		x, y := i%img.w, i/img.w
		if p == ink && x > 0 && y > 0 && x < pcTagW && y < pcTagH {
			// the name text is drawn darker in the player's colour, never in ink: ink here is a stroke
			t.Fatalf("a stroke reached into the name tag at %d,%d", x, y)
		}
		if p == ink {
			found = true
		}
	}
	if !found {
		t.Error("the stroke wasn't drawn")
	}
}
