package main

// Internet PictoChat through a free public MQTT broker, standard library only. Everyone with
// the same room code meets on the same broker topic. Nothing readable goes to the broker: the
// code is stretched with PBKDF2 into an AES-256-GCM key and an anonymous topic name, and every
// message (presence, chat) is sealed with that key and the topic as associated data.
//
// It behaves like the WebSocket client to the screens (the same pcEvents): presence "hello"s
// every 8 s carry each person's name, colour and room, which gives the room counts and the
// coming/leaving notices; a last will ("bye") covers anyone who vanishes.

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	hexenc "encoding/hex"
	"encoding/json"
	"errors"
	"math/big"
	"strings"
	"sync"
	"time"
)

// pcBrokers are tried in this order, so everyone with a code lands on the same one.
var pcBrokers = []struct {
	Host string
	Port int
	TLS  bool
}{
	{"broker.emqx.io", 8883, true},
	{"broker.hivemq.com", 8883, true},
	{"test.mosquitto.org", 8886, true},
}

const (
	pcCodeChars = "ABCDEFGHJKMNPQRSTUVWXYZ23456789" // no 0/O, 1/I/L
	pcCodeLen   = 10
	pcHelloGap  = 8 * time.Second
	pcGone      = 25 * time.Second
)

// pcNewCode makes a random room code like "K7Q2M-9XDRT".
func pcNewCode() string {
	b := make([]byte, pcCodeLen)
	for i := range b {
		n, _ := rand.Int(rand.Reader, big.NewInt(int64(len(pcCodeChars))))
		b[i] = pcCodeChars[n.Int64()]
	}
	return pcShowCode(string(b))
}

// pcNormCode keeps the letters and digits of a code, upper case (dashes and spaces don't count).
func pcNormCode(s string) string {
	var b strings.Builder
	for _, r := range strings.ToUpper(s) {
		if (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func pcShowCode(s string) string {
	s = pcNormCode(s)
	if len(s) == pcCodeLen {
		return s[:5] + "-" + s[5:]
	}
	return s
}

// pcKeys turns a code into the encryption key and the topic.
func pcKeys(code string) (key []byte, topic string, err error) {
	k, err := pbkdf2.Key(sha256.New, pcNormCode(code), []byte("duo-pictochat-v1"), 100000, 48)
	if err != nil {
		return nil, "", err
	}
	return k[:32], "duopicto1/" + hexenc.EncodeToString(k[32:]), nil
}

type pcWire struct {
	T      string     `json:"t"` // hello (ask: please say hello back), bye, msg
	ID     string     `json:"id"`
	Room   string     `json:"room,omitempty"`
	Ask    bool       `json:"ask,omitempty"`
	Player pcPlayer   `json:"player"`
	Msg    *pcMessage `json:"msg,omitempty"`
	At     int64      `json:"at"` // unix ms
}

type pcMember struct {
	player pcPlayer
	room   string
	seen   time.Time
}

type pcNet struct {
	mq      *mqttClient
	aead    cipher.AEAD
	topic   string
	id      string
	me      pcPlayer
	Broker  string
	events  func(pcEvent)
	mu      sync.Mutex
	room    string
	members map[string]*pcMember
	counts  []int
	lastMsg time.Time
	done    chan struct{}
	once    sync.Once
}

// pcConnectCode joins the internet chat for a code. events gets the same events the WebSocket
// client gives, from other goroutines.
func pcConnectCode(code string, me pcPlayer, events func(pcEvent)) (*pcNet, error) {
	key, topic, err := pcKeys(code)
	if err != nil {
		return nil, err
	}
	block, _ := aes.NewCipher(key)
	aead, _ := cipher.NewGCM(block)
	idb := make([]byte, 8)
	rand.Read(idb)
	n := &pcNet{aead: aead, topic: topic, id: hexenc.EncodeToString(idb), me: pcCleanPlayer(me), events: events,
		members: map[string]*pcMember{}, counts: make([]int, 4), done: make(chan struct{})}
	bye := n.seal(topic+"/p", pcWire{T: "bye", ID: n.id, Player: n.me})
	var lastErr error
	for _, b := range pcBrokers {
		mq, err := mqttDial(b.Host, b.Port, b.TLS, "duo"+n.id, 60*time.Second, &mqttWill{Topic: topic + "/p", Payload: bye})
		if err != nil {
			lastErr = err
			continue
		}
		if err := mq.Subscribe(topic + "/#"); err != nil {
			mq.Close()
			lastErr = err
			continue
		}
		n.mq, n.Broker = mq, b.Host
		break
	}
	if n.mq == nil {
		return nil, errors.New("no chat server answered: " + briefLine(lastErr.Error()))
	}
	go func() {
		err := n.mq.Run(60*time.Second, n.got)
		n.Close()
		why := ""
		if err != nil {
			why = "lost the connection to " + n.Broker
		}
		n.events(pcEvent{Type: "closed", Text: why})
	}()
	go n.heartbeat()
	n.events(pcEvent{Type: "sv_nameVerified", Player: n.me})
	n.events(pcEvent{Type: "sv_roomIds", Count: []int{0, 0, 0, 0}})
	n.hello(true)
	return n, nil
}

func (n *pcNet) seal(topic string, w pcWire) []byte {
	w.At = time.Now().UnixMilli()
	plain, _ := json.Marshal(w)
	nonce := make([]byte, n.aead.NonceSize())
	rand.Read(nonce)
	return n.aead.Seal(nonce, nonce, plain, []byte(topic))
}

func (n *pcNet) open(topic string, p []byte) (*pcWire, bool) {
	ns := n.aead.NonceSize()
	if len(p) < ns+n.aead.Overhead() {
		return nil, false
	}
	plain, err := n.aead.Open(nil, p[:ns], p[ns:], []byte(topic))
	if err != nil {
		return nil, false // not ours: another code, or tampered with
	}
	w := &pcWire{}
	if json.Unmarshal(plain, w) != nil || len(w.ID) > 32 {
		return nil, false
	}
	if d := time.Since(time.UnixMilli(w.At)); d > 10*time.Minute || d < -10*time.Minute {
		return nil, false // stale (a replay) or a very wrong clock
	}
	w.Player = pcCleanPlayer(w.Player)
	return w, true
}

func (n *pcNet) publish(sub string, w pcWire) {
	t := n.topic + "/" + sub
	if n.mq.Publish(t, n.seal(t, w)) != nil {
		n.mq.c.Close() // Run notices and reports it
	}
}

func (n *pcNet) hello(ask bool) {
	n.mu.Lock()
	w := pcWire{T: "hello", ID: n.id, Room: n.room, Ask: ask, Player: n.me}
	n.mu.Unlock()
	n.publish("p", w)
}

func (n *pcNet) heartbeat() {
	t := time.NewTicker(pcHelloGap)
	defer t.Stop()
	sweep := time.NewTicker(2 * time.Second)
	defer sweep.Stop()
	for {
		select {
		case <-t.C:
			n.hello(false)
		case <-sweep.C:
			n.mu.Lock()
			var gone []*pcMember
			for id, m := range n.members {
				if time.Since(m.seen) > pcGone {
					delete(n.members, id)
					gone = append(gone, m)
				}
			}
			n.mu.Unlock()
			for _, m := range gone {
				n.left(m)
			}
			if len(gone) > 0 {
				n.recount()
			}
		case <-n.done:
			return
		}
	}
}

func (n *pcNet) left(m *pcMember) {
	n.mu.Lock()
	here := m.room != "" && m.room == n.room
	n.mu.Unlock()
	if here {
		n.events(pcEvent{Type: "sv_playerLeft", Player: m.player, ID: m.room})
	}
}

func (n *pcNet) recount() {
	n.mu.Lock()
	c := make([]int, 4)
	for _, m := range n.members {
		if m.room != "" {
			c[pcRoomIndex(m.room)]++
		}
	}
	changed := false
	for i := range c {
		if c[i] != n.counts[i] {
			changed = true
		}
	}
	n.counts = c
	n.mu.Unlock()
	if changed {
		n.events(pcEvent{Type: "sv_roomIds", Count: c})
	}
}

// got handles everything that arrives on our topic.
func (n *pcNet) got(topic string, payload []byte) {
	w, ok := n.open(topic, payload)
	if !ok {
		return
	}
	switch {
	case topic == n.topic+"/p" && w.T == "hello":
		if w.Room != "" && !validRoom(w.Room) {
			return
		}
		n.mu.Lock()
		m := n.members[w.ID]
		old := ""
		if m == nil {
			m = &pcMember{}
			n.members[w.ID] = m
		} else {
			old = m.room
		}
		m.player, m.room, m.seen = w.Player, w.Room, time.Now()
		cur := n.room
		self := w.ID == n.id
		n.mu.Unlock()
		if !self && old != w.Room {
			if old != "" && old == cur {
				n.events(pcEvent{Type: "sv_playerLeft", Player: w.Player, ID: old})
			}
			if w.Room != "" && w.Room == cur {
				n.events(pcEvent{Type: "sv_playerJoined", Player: w.Player, ID: w.Room})
			}
		}
		n.recount()
		if w.Ask && !self {
			go func() { // say hello back so they see who is here, a little later
				time.Sleep(time.Duration(time.Now().UnixNano()%800) * time.Millisecond)
				n.hello(false)
			}()
		}
	case topic == n.topic+"/p" && w.T == "bye":
		n.mu.Lock()
		m := n.members[w.ID]
		delete(n.members, w.ID)
		n.mu.Unlock()
		if m != nil && w.ID != n.id {
			n.left(m)
		}
		n.recount()
	case strings.HasPrefix(topic, n.topic+"/m/") && w.T == "msg" && w.Msg != nil:
		room := strings.TrimPrefix(topic, n.topic+"/m/")
		n.mu.Lock()
		cur := n.room
		n.mu.Unlock()
		if w.ID == n.id || room != cur {
			return
		}
		pcClean(w.Msg)
		w.Msg.Player = w.Player
		if pcValid(w.Msg) {
			n.events(pcEvent{Type: "sv_receivedMessage", Message: w.Msg})
		}
	}
}

func validRoom(r string) bool {
	for _, x := range pcRooms {
		if x == r {
			return true
		}
	}
	return false
}

func (n *pcNet) Join(room string, me pcPlayer) {
	if !validRoom(room) {
		return
	}
	n.mu.Lock()
	n.room = room
	n.mu.Unlock()
	n.events(pcEvent{Type: "sv_roomData", ID: room})
	n.events(pcEvent{Type: "sv_playerJoined", Player: n.me, ID: room})
	n.hello(false)
}

func (n *pcNet) Say(m *pcMessage) {
	n.mu.Lock()
	room := n.room
	if room == "" || time.Since(n.lastMsg) < 400*time.Millisecond {
		n.mu.Unlock()
		return
	}
	n.lastMsg = time.Now()
	n.mu.Unlock()
	go n.publish("m/"+room, pcWire{T: "msg", ID: n.id, Room: room, Player: n.me, Msg: m})
}

func (n *pcNet) Leave() {
	n.mu.Lock()
	n.room = ""
	n.mu.Unlock()
	n.hello(false)
}

func (n *pcNet) Close() {
	n.once.Do(func() {
		close(n.done)
		if n.mq != nil {
			n.publish("p", pcWire{T: "bye", ID: n.id, Player: n.me})
			n.mq.Close()
		}
	})
}
