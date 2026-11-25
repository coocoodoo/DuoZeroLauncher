package main

import (
	"bufio"
	"encoding/binary"
	"net"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeBroker is just enough of an MQTT broker: CONNECT (with a will), SUBSCRIBE to "x/#",
// PUBLISH to every matching subscriber (the sender too, like real brokers), PING, DISCONNECT;
// the will is published when a client drops without DISCONNECT.
type fakeBroker struct {
	ln   net.Listener
	mu   sync.Mutex
	subs map[net.Conn][]string
	pubs int
}

func newFakeBroker(t *testing.T) *fakeBroker {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	b := &fakeBroker{ln: ln, subs: map[net.Conn][]string{}}
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go b.serve(c)
		}
	}()
	t.Cleanup(func() { ln.Close() })
	return b
}

func (b *fakeBroker) port() int { return b.ln.Addr().(*net.TCPAddr).Port }

func readStr(p []byte) (string, []byte) {
	n := int(binary.BigEndian.Uint16(p))
	return string(p[2 : 2+n]), p[2+n:]
}

func (b *fakeBroker) publish(topic string, payload []byte) {
	pkt := mqttPacket(0x30, append(mqttString(nil, topic), payload...))
	b.mu.Lock()
	b.pubs++
	for c, filters := range b.subs {
		for _, f := range filters {
			if strings.HasPrefix(topic, strings.TrimSuffix(f, "#")) {
				c.Write(pkt)
				break
			}
		}
	}
	b.mu.Unlock()
}

func (b *fakeBroker) serve(c net.Conn) {
	r := bufio.NewReader(c)
	var willTopic string
	var willMsg []byte
	clean := false
	defer func() {
		b.mu.Lock()
		delete(b.subs, c)
		b.mu.Unlock()
		c.Close()
		if !clean && willTopic != "" {
			b.publish(willTopic, willMsg)
		}
	}()
	for {
		head, body, err := mqttRead(r)
		if err != nil {
			return
		}
		switch head >> 4 {
		case 1: // CONNECT
			_, rest := readStr(body) // "MQTT"
			flags := rest[1]
			_, rest = readStr(rest[4:]) // client id
			if flags&0x04 != 0 {
				willTopic, rest = readStr(rest)
				n := int(binary.BigEndian.Uint16(rest))
				willMsg = append([]byte{}, rest[2:2+n]...)
			}
			c.Write([]byte{0x20, 2, 0, 0})
		case 8: // SUBSCRIBE
			f, _ := readStr(body[2:])
			b.mu.Lock()
			b.subs[c] = append(b.subs[c], f)
			b.mu.Unlock()
			c.Write([]byte{0x90, 3, body[0], body[1], 0})
		case 3: // PUBLISH
			topic, rest := readStr(body)
			b.publish(topic, rest)
		case 12: // PINGREQ
			c.Write([]byte{0xD0, 0})
		case 14: // DISCONNECT
			clean = true
			return
		}
	}
}

func useFakeBroker(t *testing.T) *fakeBroker {
	b := newFakeBroker(t)
	old := pcBrokers
	pcBrokers = []struct {
		Host string
		Port int
		TLS  bool
	}{{"127.0.0.1", b.port(), false}}
	t.Cleanup(func() { pcBrokers = old })
	return b
}

func TestRoomCodes(t *testing.T) {
	c := pcNewCode()
	if len(c) != pcCodeLen+1 || c[5] != '-' || pcNormCode(c) != strings.ReplaceAll(c, "-", "") {
		t.Errorf("code %q", c)
	}
	if pcNormCode(" k7q2m-9xdrt ") != "K7Q2M9XDRT" || pcShowCode("k7q2m9xdrt") != "K7Q2M-9XDRT" {
		t.Error("codes should ignore case, spaces and dashes")
	}
	k1, t1, _ := pcKeys("K7Q2M-9XDRT")
	k2, t2, _ := pcKeys("k7q2m9xdrt")
	k3, t3, _ := pcKeys("K7Q2M-9XDRS")
	if string(k1) != string(k2) || t1 != t2 || string(k1) == string(k3) || t1 == t3 {
		t.Error("the same code must give the same key and topic, another code another")
	}
	if strings.Contains(t1, "K7Q2M") {
		t.Error("the topic must not show the code")
	}
}

func TestInternetChat(t *testing.T) {
	broker := useFakeBroker(t)
	var la, lb, lc eventLog
	a, err := pcConnectCode("ABCDE-FGHJK", pcPlayer{Name: "Pi", Color: 0x3a7bd5}, la.add)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	b, err := pcConnectCode("abcde fghjk", pcPlayer{Name: "Rin", Color: 0xe0882a}, lb.add)
	if err != nil {
		t.Fatal(err)
	}
	c, err := pcConnectCode("ZZZZZ-ZZZZZ", pcPlayer{Name: "Eve", Color: 1}, lc.add) // another code
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	la.wait(t, "sv_nameVerified")
	a.Join("room_c", pcPlayer{})
	if la.wait(t, "sv_roomData").ID != "room_c" {
		t.Fatal("wrong room")
	}
	if j := la.wait(t, "sv_playerJoined"); j.Player.Name != "Pi" {
		t.Errorf("own arrival %q", j.Player.Name)
	}
	b.Join("room_c", pcPlayer{})
	if j := la.wait(t, "sv_playerJoined"); j.Player.Name != "Rin" {
		t.Errorf("join seen as %q", j.Player.Name)
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		a.mu.Lock()
		n := a.counts[2]
		a.mu.Unlock()
		if n == 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("room C count %d, want 2", n)
		}
		time.Sleep(20 * time.Millisecond)
	}

	m := newCompose()
	for _, r := range "over the internet" {
		m.typeRune(r)
	}
	b.Say(m.message(pcPlayer{}))
	got := la.wait(t, "sv_receivedMessage").Message
	if got.Player.Name != "Rin" || got.Textboxes[0].Text != "over the internet" {
		t.Errorf("got %+v", got)
	}
	// garbage and forgeries on the topic are dropped
	broker.publish(a.topic+"/m/room_c", []byte("not even encrypted"))
	broker.publish(a.topic+"/m/room_c", c.seal(a.topic+"/m/room_c", pcWire{T: "msg", ID: "x", Msg: got}))
	time.Sleep(200 * time.Millisecond)
	la.mu.Lock()
	for _, e := range la.ev {
		if e.Type == "sv_receivedMessage" {
			t.Error("a message sealed with another code got through")
		}
	}
	la.mu.Unlock()
	lc.mu.Lock()
	for _, e := range lc.ev {
		if e.Type == "sv_receivedMessage" || e.Type == "sv_playerJoined" {
			t.Errorf("another code saw %s", e.Type)
		}
	}
	lc.mu.Unlock()

	// Rin's Cardputer drops off the network: the broker sends her last will
	b.mq.c.Close()
	if l := la.wait(t, "sv_playerLeft"); l.Player.Name != "Rin" {
		t.Errorf("leave seen as %q", l.Player.Name)
	}
	lb.wait(t, "closed")
}

// DSL_MQTT_LIVE=1: every fallback broker accepts a TLS login.
func TestBrokersLive(t *testing.T) {
	if os.Getenv("DSL_MQTT_LIVE") == "" {
		t.Skip("set DSL_MQTT_LIVE=1 to try the public brokers")
	}
	for _, b := range pcBrokers {
		start := time.Now()
		mq, err := mqttDial(b.Host, b.Port, b.TLS, "duotest"+pcNormCode(pcNewCode()), 30*time.Second, nil)
		if err != nil {
			t.Errorf("%s:%d: %v", b.Host, b.Port, err)
			continue
		}
		mq.Close()
		t.Logf("%s:%d ok in %v", b.Host, b.Port, time.Since(start))
	}
}

// DSL_MQTT_LIVE=1 go test -run Live: two chats meet on the real public broker.
func TestInternetChatLive(t *testing.T) {
	if os.Getenv("DSL_MQTT_LIVE") == "" {
		t.Skip("set DSL_MQTT_LIVE=1 to try the public brokers")
	}
	code := pcNewCode()
	var la, lb eventLog
	a, err := pcConnectCode(code, pcPlayer{Name: "LiveA", Color: 0x3a7bd5}, la.add)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	b, err := pcConnectCode(code, pcPlayer{Name: "LiveB", Color: 0x3aa64f}, lb.add)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	t.Logf("broker: %s", a.Broker)
	a.Join("room_a", pcPlayer{})
	b.Join("room_a", pcPlayer{})
	la.wait(t, "sv_roomData")
	lb.wait(t, "sv_roomData")
	time.Sleep(500 * time.Millisecond)
	m := newCompose()
	for _, r := range "hello from the test" {
		m.typeRune(r)
	}
	start := time.Now()
	a.Say(m.message(pcPlayer{}))
	got := lb.wait(t, "sv_receivedMessage").Message
	t.Logf("delivered in %v: %q from %s", time.Since(start), got.Textboxes[0].Text, got.Player.Name)
}
