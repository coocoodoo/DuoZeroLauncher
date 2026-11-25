package main

// PictoChat networking: the JSON-over-WebSocket protocol of PictoChat Online (pict.chat, the
// ayunpictojava server), a client for it, a small host for it, and a LAN announcement so
// Cardputers on the same Wi-Fi find each other's chats.
//
// Protocol (text frames): the client sends "handshake", then JSON objects:
//   cl_verifyName {player:{name,color}, token}      -> sv_nameVerified {player}, sv_roomIds
//   cl_joinRoom   {player, id:"room_a".."room_d"}   -> sv_roomIds, sv_roomData {id}, sv_playerJoined
//   cl_sendMessage {message}                        -> others get sv_receivedMessage {message}
//   cl_leaveRoom                                    -> sv_leaveRoom; others get sv_playerLeft
// sv_roomIds = {count:[4 room sizes], ids:[room ids]}. The server sends "ping" now and then and
// wants "pong". A message is {player, drawing:[{x,y,type}], textboxes:[{text,x,y}], lines:1-5},
// in the coordinates of the DS bottom screen: the compose box starts at (22, 208) and is 232 x 83.
// Drawing types: 0 line to, 1 and 2 move to, 3 thick pen, 4 thin pen, 5 pen, 6 eraser, 7 rainbow.
// Public servers that want a captcha token (pict.chat) can't be joined from here.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

const (
	pcBoxX, pcBoxY = 22, 208 // where the compose box sits in the protocol's coordinates
	pcBoxW, pcBoxH = 232, 83
	pcTagW, pcTagH = 88, 18 // the name tag in the box's top-left corner
	pcRoomMax      = 16
	pcPort         = 47120 // the Duo host's port (PictoChat Online servers default to 8080)
	pcAnnouncePort = 47121
)

var pcRooms = []string{"room_a", "room_b", "room_c", "room_d"}

type pcPlayer struct {
	Name  string `json:"name"`
	Color int    `json:"color"`
}

type pcPoint struct {
	X    float64 `json:"x"`
	Y    float64 `json:"y"`
	Type int     `json:"type"`
}

type pcText struct {
	Text string  `json:"text"`
	X    float64 `json:"x"`
	Y    float64 `json:"y"`
}

type pcMessage struct {
	Player    pcPlayer  `json:"player"`
	Drawing   []pcPoint `json:"drawing"`
	Textboxes []pcText  `json:"textboxes"`
	Lines     int       `json:"lines"`
}

// pcEvent is one thing the server told us (or "closed" when the connection ended).
type pcEvent struct {
	Type    string
	Player  pcPlayer
	ID      string
	Count   []int
	Message *pcMessage
	Text    string // sv_error, closed: why
}

var pcNameRE = regexp.MustCompile(`[^A-Za-z0-9_]`)

// pcCleanPlayer applies the server's rules: name A-Z a-z 0-9 _ (at most 10), a colour that
// isn't too light to read.
func pcCleanPlayer(p pcPlayer) pcPlayer {
	p.Name = pcNameRE.ReplaceAllString(p.Name, "")
	if len(p.Name) > 10 {
		p.Name = p.Name[:10]
	}
	if p.Color < 0 || p.Color > 0xffffff {
		p.Color = 0xd5d5d5
	}
	if r, g, b := p.Color>>16, p.Color>>8&0xff, p.Color&0xff; r+g+b >= 637 {
		p.Color = 0xd5d5d5
	}
	return p
}

// pcValid says whether a message has something in it: text that isn't blank, or a stroke.
func pcValid(m *pcMessage) bool {
	for _, t := range m.Textboxes {
		if strings.TrimSpace(t.Text) != "" {
			return true
		}
	}
	for _, p := range m.Drawing {
		if p.Type == 0 {
			return true
		}
	}
	return false
}

// pcLines is how many ruled lines a message needs, from how low its content reaches
// (box coordinates).
func pcLines(lowest float64) int {
	switch {
	case lowest > 66:
		return 5
	case lowest > 48:
		return 4
	case lowest > 32:
		return 3
	case lowest > 18:
		return 2
	}
	return 1
}

// pcClean bounds a message from the network before it is kept, drawn or passed on.
func pcClean(m *pcMessage) {
	if len(m.Drawing) > 6000 {
		m.Drawing = m.Drawing[:6000]
	}
	for i := range m.Drawing {
		p := &m.Drawing[i]
		p.Type = max(-1, min(7, p.Type))
	}
	if len(m.Textboxes) > 40 {
		m.Textboxes = m.Textboxes[:40]
	}
	for i := range m.Textboxes {
		t := &m.Textboxes[i]
		if utf8.RuneCountInString(t.Text) > 120 {
			t.Text = string([]rune(t.Text)[:120])
		}
	}
	m.Lines = max(1, min(5, m.Lines))
}

// ---- client ----

type pcClient struct {
	ws     *wsConn
	out    chan string
	done   chan struct{}
	once   sync.Once
	events func(pcEvent) // called on the reading goroutine
}

// pcConnect connects, says hello and asks for the name to be checked. events gets everything
// the server says from then on, ending with a "closed" event.
func pcConnect(addr string, me pcPlayer, events func(pcEvent)) (*pcClient, error) {
	u, err := wsURL(addr, pcPort)
	if err != nil {
		return nil, err
	}
	ws, err := wsDial(u, 8*time.Second)
	if err != nil {
		return nil, err
	}
	c := &pcClient{ws: ws, out: make(chan string, 64), done: make(chan struct{}), events: events}
	go c.writer()
	c.out <- "handshake"
	c.send(map[string]any{"type": "cl_verifyName", "player": me, "token": ""})
	go c.reader()
	return c, nil
}

func (c *pcClient) send(v any) {
	b, err := json.Marshal(v)
	if err != nil {
		return
	}
	select {
	case c.out <- string(b):
	case <-c.done:
	default: // a stuck connection: drop rather than freeze the screen
	}
}

func (c *pcClient) writer() {
	for {
		select {
		case s := <-c.out:
			if c.ws.WriteText(s) != nil {
				c.Close()
				return
			}
		case <-c.done:
			return
		}
	}
}

func (c *pcClient) reader() {
	why := "the connection closed"
	for {
		c.ws.c.SetReadDeadline(time.Now().Add(100 * time.Second))
		s, err := c.ws.Read()
		if err != nil {
			select {
			case <-c.done:
				why = "" // we closed it
			default:
				if !errors.Is(err, net.ErrClosed) && !errors.Is(err, io.EOF) {
					why = "connection lost"
				}
			}
			break
		}
		if s == "ping" {
			c.out <- "pong"
			continue
		}
		var env struct {
			Type    string          `json:"type"`
			Player  pcPlayer        `json:"player"`
			ID      string          `json:"id"`
			Count   []int           `json:"count"`
			Message json.RawMessage `json:"message"`
		}
		if json.Unmarshal([]byte(s), &env) != nil {
			continue
		}
		ev := pcEvent{Type: env.Type, Player: env.Player, ID: env.ID, Count: env.Count}
		switch env.Type {
		case "sv_receivedMessage":
			m := &pcMessage{}
			if json.Unmarshal(env.Message, m) != nil {
				continue
			}
			pcClean(m)
			ev.Message = m
		case "sv_error":
			json.Unmarshal(env.Message, &ev.Text)
		}
		c.events(ev)
	}
	c.Close()
	c.events(pcEvent{Type: "closed", Text: why})
}

func (c *pcClient) Join(room string, me pcPlayer) {
	c.send(map[string]any{"type": "cl_joinRoom", "player": me, "id": room})
}

func (c *pcClient) Say(m *pcMessage) {
	c.send(map[string]any{"type": "cl_sendMessage", "message": m})
}

func (c *pcClient) Leave() { c.send(map[string]any{"type": "cl_leaveRoom"}) }

func (c *pcClient) Close() {
	c.once.Do(func() {
		close(c.done)
		c.ws.Close()
	})
}

// ---- host ----

type pcPeer struct {
	ws      *wsConn
	out     chan string
	player  *pcPlayer
	room    string
	lastMsg time.Time
	pongs   int
}

// pcServer is a PictoChat host: four rooms of up to 16, the same protocol as PictoChat Online.
type pcServer struct {
	srv   *http.Server
	ln    net.Listener
	name  string
	mu    sync.Mutex
	rooms map[string]map[*pcPeer]bool
	stop  chan struct{}
	once  sync.Once
}

func pcHost(addr, name string) (*pcServer, error) {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, err
	}
	s := &pcServer{ln: ln, name: name, rooms: map[string]map[*pcPeer]bool{}, stop: make(chan struct{})}
	for _, r := range pcRooms {
		s.rooms[r] = map[*pcPeer]bool{}
	}
	s.srv = &http.Server{Handler: http.HandlerFunc(s.serve), ReadHeaderTimeout: 10 * time.Second}
	go s.srv.Serve(ln)
	return s, nil
}

func (s *pcServer) Port() int { return s.ln.Addr().(*net.TCPAddr).Port }

func (s *pcServer) Close() {
	s.once.Do(func() {
		close(s.stop)
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		s.srv.Shutdown(ctx)
		s.mu.Lock()
		for _, room := range s.rooms {
			for p := range room {
				p.ws.Close()
			}
		}
		s.mu.Unlock()
	})
}

// People reports how many are in each room.
func (s *pcServer) People() []int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := make([]int, len(pcRooms))
	for i, r := range pcRooms {
		n[i] = len(s.rooms[r])
	}
	return n
}

func (s *pcServer) serve(w http.ResponseWriter, r *http.Request) {
	ws, err := wsUpgrade(w, r)
	if err != nil {
		return
	}
	p := &pcPeer{ws: ws, out: make(chan string, 128)}
	done := make(chan struct{})
	go func() { // writer and pinger
		t := time.NewTicker(30 * time.Second)
		defer t.Stop()
		for {
			select {
			case m := <-p.out:
				if ws.WriteText(m) != nil {
					ws.Close()
					return
				}
			case <-t.C:
				s.mu.Lock()
				idle := p.pongs < 0
				p.pongs = -1
				s.mu.Unlock()
				if idle || ws.WriteText("ping") != nil {
					ws.Close()
					return
				}
			case <-done:
				return
			case <-s.stop:
				return
			}
		}
	}()
	defer func() {
		close(done)
		s.leave(p, false)
		ws.Close()
	}()
	for {
		ws.c.SetReadDeadline(time.Now().Add(100 * time.Second))
		m, err := ws.Read()
		if err != nil {
			return
		}
		switch m {
		case "pong":
			s.mu.Lock()
			p.pongs = 1
			s.mu.Unlock()
			continue
		case "handshake":
			continue
		}
		var env struct {
			Type    string          `json:"type"`
			Player  *pcPlayer       `json:"player"`
			ID      string          `json:"id"`
			Message json.RawMessage `json:"message"`
		}
		if json.Unmarshal([]byte(m), &env) != nil {
			continue
		}
		s.mu.Lock()
		p.pongs = 1
		s.mu.Unlock()
		switch env.Type {
		case "cl_verifyName":
			if env.Player == nil {
				return
			}
			pl := pcCleanPlayer(*env.Player)
			if pl.Name == "" {
				pl.Name = "Guest" + strconv.Itoa(int(time.Now().UnixNano()%1000))
			}
			s.mu.Lock()
			p.player = &pl
			s.mu.Unlock()
			p.send(map[string]any{"type": "sv_nameVerified", "player": pl})
			p.send(s.roomIDs())
		case "cl_joinRoom":
			s.join(p, env.ID)
		case "cl_sendMessage":
			s.say(p, env.Message)
		case "cl_leaveRoom":
			s.leave(p, true)
		}
	}
}

func (p *pcPeer) send(v any) {
	b, err := json.Marshal(v)
	if err != nil {
		return
	}
	select {
	case p.out <- string(b):
	default: // too far behind: skip rather than hold everyone up
	}
}

func (s *pcServer) roomIDs() map[string]any {
	return map[string]any{"type": "sv_roomIds", "count": s.People(), "ids": append(append([]string{}, pcRooms...), "room_e")}
}

func (s *pcServer) join(p *pcPeer, room string) {
	s.mu.Lock()
	if p.player == nil || s.rooms[room] == nil {
		s.mu.Unlock()
		p.send(map[string]any{"type": "sv_error", "message": "no such room"})
		return
	}
	if len(s.rooms[room]) >= pcRoomMax {
		s.mu.Unlock()
		p.send(map[string]any{"type": "sv_error", "message": "that room is full"})
		return
	}
	s.mu.Unlock()
	s.leave(p, false)
	s.mu.Lock()
	p.room = room
	s.rooms[room][p] = true
	others := s.peersIn(room)
	pl := *p.player
	s.mu.Unlock()
	p.send(s.roomIDs())
	p.send(map[string]any{"type": "sv_roomData", "id": room, "private": false})
	for _, o := range others {
		o.send(map[string]any{"type": "sv_playerJoined", "player": pl, "id": room})
	}
}

func (s *pcServer) leave(p *pcPeer, tell bool) {
	s.mu.Lock()
	room := p.room
	if room == "" {
		s.mu.Unlock()
		return
	}
	delete(s.rooms[room], p)
	p.room = ""
	others := s.peersIn(room)
	var pl pcPlayer
	if p.player != nil {
		pl = *p.player
	}
	s.mu.Unlock()
	if tell {
		p.send(map[string]any{"type": "sv_leaveRoom", "id": room})
	}
	for _, o := range others {
		o.send(map[string]any{"type": "sv_playerLeft", "player": pl, "id": room})
	}
}

func (s *pcServer) peersIn(room string) []*pcPeer { // with s.mu held
	var out []*pcPeer
	for o := range s.rooms[room] {
		out = append(out, o)
	}
	return out
}

func (s *pcServer) say(p *pcPeer, raw json.RawMessage) {
	m := &pcMessage{}
	if json.Unmarshal(raw, m) != nil {
		return
	}
	s.mu.Lock()
	if p.room == "" || p.player == nil || time.Since(p.lastMsg) < 400*time.Millisecond {
		s.mu.Unlock()
		return
	}
	p.lastMsg = time.Now()
	m.Player = *p.player // who sent it is the server's to say
	room := p.room
	var others []*pcPeer
	for o := range s.rooms[room] {
		if o != p {
			others = append(others, o)
		}
	}
	s.mu.Unlock()
	pcClean(m)
	if !pcValid(m) {
		return
	}
	for _, o := range others {
		o.send(map[string]any{"type": "sv_receivedMessage", "message": m})
	}
}

// ---- finding chats on the local network ----

// pcAnnounce tells the local network about a hosted chat every two seconds until stop closes.
func pcAnnounce(name string, port int, stop chan struct{}) {
	c, err := net.ListenUDP("udp4", nil)
	if err != nil {
		return
	}
	defer c.Close()
	msg := []byte(fmt.Sprintf("DUOPICTO1 %d %s", port, name))
	dst := &net.UDPAddr{IP: net.IPv4bcast, Port: pcAnnouncePort}
	t := time.NewTicker(2 * time.Second)
	defer t.Stop()
	for {
		c.WriteToUDP(msg, dst)
		for _, b := range broadcastAddrs() {
			c.WriteToUDP(msg, &net.UDPAddr{IP: b, Port: pcAnnouncePort})
		}
		select {
		case <-t.C:
		case <-stop:
			return
		}
	}
}

// broadcastAddrs are the IPv4 broadcast addresses of the networks we are on.
func broadcastAddrs() []net.IP {
	var out []net.IP
	ifs, _ := net.Interfaces()
	for _, i := range ifs {
		if i.Flags&net.FlagUp == 0 || i.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, _ := i.Addrs()
		for _, a := range addrs {
			n, ok := a.(*net.IPNet)
			if !ok || n.IP.To4() == nil {
				continue
			}
			ip := n.IP.To4()
			b := make(net.IP, 4)
			for k := range b {
				b[k] = ip[k] | ^n.Mask[k]
			}
			out = append(out, b)
		}
	}
	return out
}

type pcFound struct {
	Name, Addr string
	Seen       time.Time
}

// pcListen collects announced chats until stop closes; found is called (on the listening
// goroutine) whenever the list changes.
func pcListen(stop chan struct{}, found func([]pcFound)) {
	c, err := net.ListenUDP("udp4", &net.UDPAddr{Port: pcAnnouncePort})
	if err != nil {
		return
	}
	go func() { <-stop; c.Close() }()
	seen := map[string]pcFound{}
	buf := make([]byte, 256)
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
			f := strings.SplitN(string(buf[:n]), " ", 3)
			if len(f) == 3 && f[0] == "DUOPICTO1" {
				if port, err := strconv.Atoi(f[1]); err == nil && port > 0 && port < 65536 {
					addr := net.JoinHostPort(from.IP.String(), f[1])
					seen[addr] = pcFound{Name: pcCleanPlayer(pcPlayer{Name: f[2]}).Name, Addr: addr, Seen: time.Now()}
				}
			}
		}
		var list []pcFound
		var keys []string
		for k, v := range seen {
			if time.Since(v.Seen) > 7*time.Second {
				delete(seen, k)
				continue
			}
			list = append(list, v)
			keys = append(keys, k)
		}
		sort.Slice(list, func(i, j int) bool { return list[i].Addr < list[j].Addr })
		sort.Strings(keys)
		if k := strings.Join(keys, ","); k != last {
			last = k
			found(list)
		}
	}
}
