package main

// PictoChat, built in: find a chat on this Wi-Fi (or host one, or join one by address), choose
// a room, then send drawings and text. The screen keeps the handheld's layout, without the
// on-screen keyboard (the Cardputer has one): the message log above, the compose box below at
// its real size (232 x 83, 1:1), tools on the left, Send / Copy / Clear on the right.
//
// Keys in a room. Tab switches TYPE and DRAW.
//   TYPE: keys type into the ruled lines (they wrap), Backspace erases, Left/Right pick a line,
//         Up/Down scroll the log, Enter sends, Del clears, Ctrl+R copies the message in view.
//   DRAW: arrows (or F X Z C) move the pen, Space lifts or puts it down, P pen, E eraser,
//         B thick/thin, W/S scroll the log, Backspace undoes a stroke, R copies, Enter sends.
// Esc leaves the room. All drawing here is Duo's own; the protocol is in picto_net.go.

import (
	"dslauncher/cp0"
	"fmt"
	"math"
	"net"
	"os"
	"strings"
	"time"
)

var (
	cPcInk   = hex(0x282828) // pen
	cPcPaper = hex(0xfcfcfc) // message box
	// room colours: A blue, B green, C orange, D purple
	pcRoomColor = []RGB{hex(0x3a7bd5), hex(0x3aa64f), hex(0xe0882a), hex(0x8d4cc8)}
)

// pcRowY is the top of each line's text in a message box; pcRowH the box height for n lines.
var pcRowY = [5]int{3, 19, 35, 51, 67}

func pcRowH(lines int) int { return 3 + 16*max(1, min(5, lines)) }

// the text start of each line: after the name tag on the first
func pcRowX(row int) int {
	if row == 0 {
		return pcTagW + 3
	}
	return 4
}

func pcRowWidth(row int) int { return pcBoxW - pcRowX(row) - 4 }

func rgbInt(c RGB) int { return int(c.R)<<16 | int(c.G)<<8 | int(c.B) }
func intRGB(v int) RGB { return RGB{uint8(v >> 16), uint8(v >> 8), uint8(v)} }
func pcRoomIndex(id string) int {
	for i, r := range pcRooms {
		if r == id {
			return i
		}
	}
	return 0
}

// ---- drawing a message box ----

// drawPcBox draws an empty box for a player at x,y: paper, ruled lines and outline in their
// colour, and their name tag.
func drawPcBox(g *G, x, y, lines int, p pcPlayer) {
	h := pcRowH(lines)
	col := intRGB(p.Color)
	g.rect(x+1, y+1, pcBoxW-2, h-2, cPcPaper)
	rule := mix(cPcPaper, col, 0.35)
	for k := 1; k < lines; k++ {
		ly := y + pcRowY[k] - 3
		for xx := x + 3; xx < x+pcBoxW-3; xx += 2 { // dotted, like ruled paper
			g.px(xx, ly, rule)
		}
	}
	g.frame(x, y, pcBoxW, h, col.darker(0.15))
	// name tag
	g.rect(x+1, y+1, pcTagW-1, pcTagH-1, mix(cPcPaper, col, 0.22))
	g.hline(x+1, y+pcTagH, pcTagW, col)
	g.vline(x+pcTagW, y+1, pcTagH, col)
	g.text(x+5, y+4, fit(p.Name, pcTagW-8, 1, false), col.darker(0.25), 1, false)
}

// pcPen draws one dot of the pen at box coordinates (clipped to the box).
func pcPen(g *G, x, y int, px, py int, size int, c RGB, h int) {
	for dy := 0; dy < size; dy++ {
		for dx := 0; dx < size; dx++ {
			bx, by := px+dx, py+dy
			if bx > 0 && by > 0 && bx < pcBoxW-1 && by < h-1 && !(bx <= pcTagW && by <= pcTagH) {
				g.px(x+bx, y+by, c)
			}
		}
	}
}

// drawPcStrokes replays a drawing (protocol coordinates) into a box at x,y.
func drawPcStrokes(g *G, x, y, h int, drawing []pcPoint) {
	size, mode := 1, 5 // thin black pen until told otherwise
	hue := 348.0
	cx, cy := pcTagW, pcTagH
	for _, p := range drawing {
		px := int(math.Max(0, math.Min(pcBoxW, p.X-pcBoxX)))
		py := int(math.Max(0, math.Min(pcBoxH, p.Y-pcBoxY)))
		if px <= pcTagW && py <= pcTagH {
			px, py = pcTagW, pcTagH
		}
		switch p.Type {
		case 0:
			c := cPcInk
			switch mode {
			case 6:
				c = cPcPaper
			case 7:
				hue = math.Mod(hue+12, 360)
				c = hsv(hue, 1, 1)
			}
			pcLine(g, x, y, cx, cy, px, py, size, c, h)
			cx, cy = px, py
		case 1, 2:
			cx, cy = px, py
		case 3:
			size = 2
		case 4:
			size = 1
		case 5, 6, 7:
			mode = p.Type
		}
	}
}

func pcLine(g *G, x, y, x0, y0, x1, y1, size int, c RGB, h int) {
	dx, dy := abs(x1-x0), -abs(y1-y0)
	sx, sy := 1, 1
	if x0 > x1 {
		sx = -1
	}
	if y0 > y1 {
		sy = -1
	}
	e := dx + dy
	for {
		pcPen(g, x, y, x0, y0, size, c, h)
		if x0 == x1 && y0 == y1 {
			return
		}
		e2 := 2 * e
		if e2 >= dy {
			e += dy
			x0 += sx
		}
		if e2 <= dx {
			e += dx
			y0 += sy
		}
	}
}

func hsv(h, s, v float64) RGB {
	c := v * s
	x := c * (1 - math.Abs(math.Mod(h/60, 2)-1))
	var r, g, b float64
	switch {
	case h < 60:
		r, g = c, x
	case h < 120:
		r, g = x, c
	case h < 180:
		g, b = c, x
	case h < 240:
		g, b = x, c
	case h < 300:
		r, b = x, c
	default:
		r, b = c, x
	}
	m := v - c
	return rgb(int((r+m)*255), int((g+m)*255), int((b+m)*255))
}

func drawPcTexts(g *G, x, y int, texts []pcText) {
	for _, t := range texts {
		tx, ty := int(t.X)-pcBoxX, int(t.Y)-pcBoxY
		g.text(x+tx, y+ty, t.Text, cPcInk, 1, false)
	}
}

// pcImage is a message box rendered once and kept, so the log scrolls cheaply.
type pcImage struct {
	w, h int
	px   []uint16
}

var pcScratch *G

func renderPcMessage(m *pcMessage) *pcImage {
	if pcScratch == nil {
		pcScratch = newG(&cp0.Screen{Px: make([]uint16, W*H)})
	}
	g := pcScratch
	g.noClip()
	h := pcRowH(m.Lines)
	g.setClip(0, 0, pcBoxW, h)
	drawPcBox(g, 0, 0, m.Lines, m.Player)
	drawPcStrokes(g, 0, 0, h, m.Drawing)
	drawPcTexts(g, 0, 0, m.Textboxes)
	img := &pcImage{w: pcBoxW, h: h, px: make([]uint16, pcBoxW*h)}
	for yy := 0; yy < h; yy++ {
		copy(img.px[yy*pcBoxW:(yy+1)*pcBoxW], g.s.Px[yy*W:yy*W+pcBoxW])
	}
	return img
}

func (g *G) blit(x, y int, img *pcImage) {
	for yy := 0; yy < img.h; yy++ {
		sy := y + yy
		if sy < g.clip.Min.Y || sy >= g.clip.Max.Y {
			continue
		}
		for xx := 0; xx < img.w; xx++ {
			if sx := x + xx; sx >= g.clip.Min.X && sx < g.clip.Max.X {
				g.s.Px[sy*W+sx] = img.px[yy*img.w+xx]
			}
		}
	}
}

// ---- composing ----

type pcCompose struct {
	lines   [5][]rune
	row     int
	extra   []pcText  // copied text that isn't on a line (protocol coordinates)
	draw    []pcPoint // protocol coordinates, tool changes included
	strokes []int     // where each stroke starts in draw, for undo
	px, py  int       // the pen, box coordinates
	down    bool
	thick   bool
	eraser  bool
}

func newCompose() *pcCompose {
	c := &pcCompose{px: pcBoxW / 2, py: 45, thick: true}
	c.reset()
	return c
}

// reset empties the box, keeping the pen where it is and the tool in hand.
func (c *pcCompose) reset() {
	c.lines = [5][]rune{}
	c.row = 0
	c.extra = nil
	c.strokes = nil
	c.down = false
	c.draw = []pcPoint{{Type: map[bool]int{true: 3, false: 4}[c.thick]}, {Type: map[bool]int{true: 6, false: 5}[c.eraser]}}
}

func (c *pcCompose) empty() bool { return !pcValid(c.message(pcPlayer{})) }

func (c *pcCompose) tool(t int) {
	c.draw = append(c.draw, pcPoint{Type: t})
}

func (c *pcCompose) at(t int) {
	c.draw = append(c.draw, pcPoint{X: float64(c.px + pcBoxX), Y: float64(c.py + pcBoxY), Type: t})
}

// type adds a character on the current line, wrapping to the next one when it is full.
func (c *pcCompose) typeRune(r rune) bool {
	for c.row < 5 {
		if textW(string(append(c.lines[c.row], r)), 1, false) <= pcRowWidth(c.row) {
			c.lines[c.row] = append(c.lines[c.row], r)
			return true
		}
		if c.row == 4 {
			return false
		}
		c.row++
		if r == ' ' {
			return true
		}
	}
	return false
}

func (c *pcCompose) backspace() bool {
	if n := len(c.lines[c.row]); n > 0 {
		c.lines[c.row] = c.lines[c.row][:n-1]
		return true
	}
	if c.row > 0 {
		c.row--
		return true
	}
	return false
}

// move steps the pen, drawing if it is down. It keeps out of the name tag.
func (c *pcCompose) move(dx, dy int) {
	nx := max(1, min(pcBoxW-3, c.px+dx))
	ny := max(1, min(pcBoxH-3, c.py+dy))
	if nx <= pcTagW && ny <= pcTagH {
		if c.py > pcTagH {
			ny = pcTagH + 1
		} else {
			nx = pcTagW + 1
		}
	}
	c.px, c.py = nx, ny
	if c.down {
		c.at(0)
	}
}

func (c *pcCompose) penDown(on bool) {
	if on == c.down {
		return
	}
	c.down = on
	if on {
		c.strokes = append(c.strokes, len(c.draw))
		c.at(2)
		c.at(0) // a dot even if the pen doesn't move
	} else {
		c.at(1)
	}
}

func (c *pcCompose) undo() bool {
	if len(c.strokes) == 0 {
		return false
	}
	c.down = false
	c.draw = c.draw[:c.strokes[len(c.strokes)-1]]
	c.strokes = c.strokes[:len(c.strokes)-1]
	return true
}

// message builds what is sent: the drawing, a text box per line, and how many lines it needs.
func (c *pcCompose) message(me pcPlayer) *pcMessage {
	m := &pcMessage{Player: me, Drawing: append([]pcPoint{}, c.draw...)}
	lowest := 0.0
	for _, p := range c.draw {
		if p.Type == 0 || p.Type == 2 {
			lowest = math.Max(lowest, p.Y-pcBoxY+2)
		}
	}
	for row, l := range c.lines {
		if len(l) == 0 {
			continue
		}
		m.Textboxes = append(m.Textboxes, pcText{Text: string(l), X: float64(pcRowX(row) + pcBoxX), Y: float64(pcRowY[row] + pcBoxY)})
		lowest = math.Max(lowest, float64(pcRowY[row]+12))
	}
	for _, t := range c.extra {
		m.Textboxes = append(m.Textboxes, t)
		lowest = math.Max(lowest, t.Y-pcBoxY+12)
	}
	m.Lines = pcLines(lowest)
	return m
}

// copyFrom puts a message from the log into the box (the handheld's COPY).
func (c *pcCompose) copyFrom(m *pcMessage) {
	c.reset()
	c.draw = append(c.draw, m.Drawing...)
	c.draw = append(c.draw, pcPoint{Type: map[bool]int{true: 3, false: 4}[c.thick]}, pcPoint{Type: map[bool]int{true: 6, false: 5}[c.eraser]})
	for _, t := range m.Textboxes {
		placed := false
		for row := range pcRowY {
			if math.Abs(t.X-float64(pcRowX(row)+pcBoxX)) <= 3 && math.Abs(t.Y-float64(pcRowY[row]+pcBoxY)) <= 3 && len(c.lines[row]) == 0 {
				c.lines[row] = []rune(t.Text)
				c.row = row
				placed = true
				break
			}
		}
		if !placed {
			c.extra = append(c.extra, t)
		}
	}
}

// ---- the chats list ----

// pcLink is a way into a chat: a PictoChat server (pcClient) or an internet room code (pcNet).
type pcLink interface {
	Join(room string, me pcPlayer)
	Say(m *pcMessage)
	Leave()
	Close()
}

type pcSession struct {
	client pcLink
	host   *pcServer
	hostIP string
	code   string // internet chat: the room code to share
	via    string // ... and the broker it goes through
}

func (a *App) pcMe() pcPlayer {
	return pcCleanPlayer(pcPlayer{Name: a.cfg.Name, Color: rgbInt(a.theme())})
}

// pictoMenu lists chats found on this network, hosting one here, and joining by address.
func pictoMenu(a *App) *RowList {
	l := page("PICTOCHAT", artPicto)
	l.Tag = "picto-menu"
	var found []pcFound
	stop := make(chan struct{})
	if os.Getenv("DSL_PICTO_NOLAN") == "" {
		go pcListen(stop, func(f []pcFound) {
			a.work <- func() {
				found = f
				if l.Refresh != nil {
					l.Refresh(a, l)
				}
				a.dirty = true
			}
		})
	}
	l.Refresh = func(a *App, l *RowList) {
		l.Rows = nil
		for _, f := range found {
			f := f
			l.Rows = append(l.Rows, &Row{Label: f.Name + "'s chat", Kind: rowLink, Value: "NEARBY",
				Do: func(a *App, r *Row) { pcJoin(a, f.Addr, f.Name+"'s chat", nil) }})
		}
		l.Rows = append(l.Rows,
			&Row{Label: "Host a chat here", Kind: rowAction, Value: "THIS WI-FI", Do: func(a *App, r *Row) { pcStartHost(a) }},
			&Row{Label: "Start an internet chat", Kind: rowAction, Value: "NEW CODE", Do: func(a *App, r *Row) { pcJoinCode(a, pcNewCode()) }},
			&Row{Label: "Join with a code", Kind: rowLink, Do: func(a *App, r *Row) {
				a.push(newInput("ROOM CODE", "The code your friend shared", "", 16, false, func(s string) {
					if len(pcNormCode(s)) < 6 {
						a.notify("Codes are 10 letters and digits", true)
						return
					}
					pcJoinCode(a, s)
				}))
			}},
		)
		if a.cfg.PictoCode != "" {
			code := a.cfg.PictoCode
			l.Rows = append(l.Rows, &Row{Label: "Rejoin " + pcShowCode(code), Kind: rowAction, Value: "INTERNET",
				Do: func(a *App, r *Row) { pcJoinCode(a, code) }})
		}
		l.Rows = append(l.Rows,
			&Row{Label: "Join by address", Kind: rowLink, Value: a.cfg.PictoAddr, Do: func(a *App, r *Row) {
				a.push(newInput("JOIN A CHAT", "Address of a PictoChat server (host:port)", a.cfg.PictoAddr, 60, false,
					func(s string) {
						s = strings.TrimSpace(s)
						if s == "" {
							return
						}
						a.cfg.PictoAddr = s
						a.save()
						pcJoin(a, s, s, nil)
					}))
			}},
			&Row{Label: "You", Value: a.pcMe().Name},
		)
	}
	l.Refresh(a, l)
	l.Footer = "Chats on this Wi-Fi show up here; codes work anywhere"
	l.OnBack = func(a *App) { close(stop) }
	return l
}

func pcStartHost(a *App) {
	me := a.pcMe()
	addr := os.Getenv("DSL_PICTO_ADDR") // tests bind to 127.0.0.1:0
	if addr == "" {
		addr = fmt.Sprintf(":%d", pcPort)
	}
	srv, err := pcHost(addr, me.Name)
	if err != nil {
		alert(a, "CAN'T HOST", "Couldn't open port "+fmt.Sprint(pcPort)+": "+briefLine(err.Error()))
		return
	}
	if os.Getenv("DSL_PICTO_NOLAN") == "" {
		go pcAnnounce(me.Name, srv.Port(), srv.stop)
	}
	pcJoin(a, fmt.Sprintf("127.0.0.1:%d", srv.Port()), me.Name+"'s chat", srv)
}

// pcJoin connects to a PictoChat server (with the loading squares while it does) and opens the
// rooms screen.
func pcJoin(a *App, addr, label string, host *pcServer) {
	v := &PictoView{label: label, addr: addr, me: a.pcMe(), compose: newCompose(), counts: make([]int, 4)}
	if host != nil {
		v.sess.host = host
		v.sess.hostIP = primaryIP()
	}
	pcOpen(a, v, label, func() error {
		c, err := pcConnect(addr, v.me, func(ev pcEvent) { a.work <- func() { v.event(a, ev) } })
		if err != nil {
			return err
		}
		v.sess.client = c
		return nil
	})
}

// pcJoinCode joins the internet chat for a room code (see picto_mqtt.go).
func pcJoinCode(a *App, code string) {
	code = pcShowCode(code)
	v := &PictoView{label: "Code " + code, me: a.pcMe(), compose: newCompose(), counts: make([]int, 4)}
	v.sess.code = code
	pcOpen(a, v, "the internet chat", func() error {
		n, err := pcConnectCode(code, v.me, func(ev pcEvent) { a.work <- func() { v.event(a, ev) } })
		if err != nil {
			return err
		}
		v.sess.client, v.sess.via = n, n.Broker
		a.work <- func() {
			a.cfg.PictoCode = pcNormCode(code)
			a.save()
		}
		return nil
	})
}

func pcOpen(a *App, v *PictoView, label string, connect func() error) {
	host := v.sess.host
	a.background("Connecting to "+label+"...", connect, func(err error) {
		if err != nil {
			if host != nil {
				host.Close()
			}
			alert(a, "CAN'T CONNECT", label+": "+briefLine(err.Error()))
			return
		}
		a.snd.Play("open")
		a.push(v)
	})
}

// ---- the rooms screen and the room ----

type pcItem struct {
	kind int // 0 message, 1 came in, 2 left
	who  pcPlayer
	msg  *pcMessage
	img  *pcImage
}

func (it *pcItem) height() int {
	if it.kind == 0 {
		return it.img.h
	}
	return 15
}

type PictoView struct {
	label, addr string
	me          pcPlayer
	sess        pcSession
	inRoom      bool
	room        string
	sel         int // rooms screen
	counts      []int
	log         []*pcItem
	scroll      int // px up from the newest
	compose     *pcCompose
	drawing     bool // DRAW mode (else TYPE)
	gone        bool
	bot         *pcClient // scripted runs only
}

func (v *PictoView) typing() bool { return v.inRoom && !v.drawing }

func (v *PictoView) Animating(a *App) bool { return v.inRoom } // the caret and pen blink

func (v *PictoView) close() {
	if v.gone {
		return
	}
	v.gone = true
	if v.sess.client != nil {
		v.sess.client.Close()
	}
	if v.bot != nil {
		v.bot.Close()
	}
	if v.sess.host != nil {
		v.sess.host.Close()
	}
}

func (v *PictoView) leaveView(a *App) {
	v.close()
	a.dropView(v)
	pcMenuRefresh(a)
}

// pcMenuRefresh updates the chats list under a closing chat (a new "Rejoin" row, say).
func pcMenuRefresh(a *App) {
	if l, ok := a.top().(*RowList); ok && l.Tag == "picto-menu" && l.Refresh != nil {
		l.Refresh(a, l)
	}
}

// event handles what the server said (on the UI thread).
func (v *PictoView) event(a *App, ev pcEvent) {
	if v.gone {
		return
	}
	switch ev.Type {
	case "sv_nameVerified":
		v.me = ev.Player
	case "sv_roomIds":
		if len(ev.Count) >= 4 {
			v.counts = ev.Count[:4]
		}
	case "sv_roomData":
		v.inRoom, v.room = true, ev.ID
		v.log, v.scroll = nil, 0
		v.compose.reset()
		a.syncText()
	case "sv_playerJoined":
		if v.inRoom {
			v.add(&pcItem{kind: 1, who: ev.Player})
			a.snd.Play("pc-enter")
			if ev.Player.Name != v.me.Name { // our own arrival is already counted
				v.counts[pcRoomIndex(v.room)]++
			}
		}
	case "sv_playerLeft":
		if v.inRoom {
			v.add(&pcItem{kind: 2, who: ev.Player})
			a.snd.Play("pc-leave")
			if i := pcRoomIndex(v.room); v.counts[i] > 1 {
				v.counts[i]--
			}
		}
	case "sv_receivedMessage":
		if v.inRoom && ev.Message != nil {
			v.add(&pcItem{msg: ev.Message, img: renderPcMessage(ev.Message)})
			a.snd.Play("pc-receive")
		}
	case "sv_error":
		if ev.Text != "" {
			a.notify(ev.Text, true)
		}
	case "closed":
		v.close()
		if a.hasView(v) {
			a.dropView(v)
			pcMenuRefresh(a)
			if ev.Text != "" {
				alert(a, "DISCONNECTED", v.label+": "+ev.Text)
			}
		}
	}
	a.dirty = true
}

// add puts an item at the bottom of the log; if you had scrolled up, the view stays put.
func (v *PictoView) add(it *pcItem) {
	v.log = append(v.log, it)
	if len(v.log) > 60 {
		v.log = v.log[len(v.log)-60:]
	}
	if v.scroll > 0 {
		v.scroll += it.height() + 2
	}
}

func (v *PictoView) Bar(a *App) []BarItem {
	if v.inRoom {
		return nil // the room screen has its own buttons
	}
	return []BarItem{{"JOIN", artOpen, false}, {}, {}, {}, {"LEAVE", artBack, false}}
}

func (v *PictoView) Key(a *App, k cp0.Key) {
	if !v.inRoom {
		v.roomsKey(a, k)
		return
	}
	c := v.compose
	switch {
	case k.Name == "BOT" && a.sim: // scripted: someone else says something
		v.botSay(a)
		return
	case k.Back():
		a.snd.Play("modal")
		confirm(a, "LEAVE ROOM?", "Leave "+pcRoomName(v.room)+"? Your box is cleared.", func() {
			v.sess.client.Leave()
			v.inRoom = false
			a.snd.Play("pc-leave")
			a.syncText()
		})
		return
	case k.Name == "TAB":
		v.drawing = !v.drawing
		c.penDown(false)
		a.snd.Play("select")
		a.syncText()
		return
	case k.Name == "ENTER":
		v.send(a)
		return
	case k.Name == "DELETE":
		c.reset()
		a.snd.Play("pc-clear")
		return
	case k.Ctrl == 'r' || (v.drawing && k.Ch == 'r'):
		v.copy(a)
		return
	}
	if v.drawing {
		v.drawKey(a, k)
		return
	}
	switch {
	case k.Name == "UP":
		v.scrollBy(a, 16)
	case k.Name == "DOWN":
		v.scrollBy(a, -16)
	case k.Name == "LEFT":
		if c.row > 0 {
			c.row--
			a.snd.Play("move")
		}
	case k.Name == "RIGHT":
		if c.row < 4 {
			c.row++
			a.snd.Play("move")
		}
	case k.Name == "BACKSPACE":
		if c.backspace() {
			a.snd.Play("tick")
		} else {
			a.snd.Play("bump")
		}
	case k.Name == "SPACE" || (k.Ch != 0 && k.Ctrl == 0):
		r := k.Ch
		if k.Name == "SPACE" {
			r = ' '
		}
		if c.typeRune(r) {
			a.snd.Play("tick")
		} else {
			a.snd.Play("bump")
		}
	}
}

func (v *PictoView) drawKey(a *App, k cp0.Key) {
	c := v.compose
	step := 1
	if k.Repeat {
		step = 2
		if k.Held > 1.2 {
			step = 3
		}
	}
	switch {
	case k.Name == "UP":
		c.move(0, -step)
	case k.Name == "DOWN":
		c.move(0, step)
	case k.Name == "LEFT":
		c.move(-step, 0)
	case k.Name == "RIGHT":
		c.move(step, 0)
	case k.Name == "SPACE" || k.Ch == ' ':
		c.penDown(!c.down)
		a.snd.Play(map[bool]string{true: "increase", false: "decrease"}[c.down])
	case k.Ch == 'p':
		if c.eraser {
			c.eraser = false
			c.tool(5)
		}
		a.snd.Play("select")
	case k.Ch == 'e':
		if !c.eraser {
			c.eraser = true
			c.tool(6)
		}
		a.snd.Play("select")
	case k.Ch == 'b':
		c.thick = !c.thick
		c.tool(map[bool]int{true: 3, false: 4}[c.thick])
		a.snd.Play("select")
	case k.Ch == 'w':
		v.scrollBy(a, 16)
	case k.Ch == 's':
		v.scrollBy(a, -16)
	case k.Name == "BACKSPACE":
		if c.undo() {
			a.snd.Play("back")
		} else {
			a.snd.Play("bump")
		}
	}
}

func (v *PictoView) scrollBy(a *App, d int) {
	total := 0
	for _, it := range v.log {
		total += it.height() + 2
	}
	n := max(0, min(max(0, total-84), v.scroll+d))
	if n == v.scroll {
		a.snd.Play("bump")
		return
	}
	v.scroll = n
	a.snd.Play("move")
}

func (v *PictoView) send(a *App) {
	m := v.compose.message(v.me)
	if !pcValid(m) {
		a.snd.Play("error")
		return
	}
	v.sess.client.Say(m)
	v.add(&pcItem{msg: m, img: renderPcMessage(m)})
	v.scroll = 0
	v.compose.reset()
	a.snd.Play("pc-send")
}

// focused is the message at the bottom of the log view (what COPY takes).
func (v *PictoView) focused() *pcMessage {
	off := 0
	for i := len(v.log) - 1; i >= 0; i-- {
		it := v.log[i]
		off += it.height() + 2
		if off > v.scroll && it.kind == 0 {
			return it.msg
		}
	}
	return nil
}

func (v *PictoView) copy(a *App) {
	m := v.focused()
	if m == nil {
		a.snd.Play("error")
		return
	}
	v.compose.copyFrom(m)
	a.snd.Play("select")
}

func pcRoomName(id string) string { return "Chat Room " + string(rune('A'+pcRoomIndex(id))) }

func (v *PictoView) roomsKey(a *App, k cp0.Key) {
	switch {
	case k.Back() || k.Slot() == 4:
		a.snd.Play("back")
		v.leaveView(a)
	case k.Name == "UP":
		v.sel = (v.sel + 3) % 4
		a.snd.Play("move")
	case k.Name == "DOWN":
		v.sel = (v.sel + 1) % 4
		a.snd.Play("move")
	case k.Name == "ENTER" || k.Slot() == 0:
		if v.sess.client == nil {
			return
		}
		if v.counts[v.sel] >= pcRoomMax {
			a.notify("That room is full", true)
			return
		}
		a.snd.Play("select")
		v.drawing = false
		v.sess.client.Join(pcRooms[v.sel], v.me)
	}
}

// botSay (scripted runs) has a second client join the room and send a drawing with text.
func (v *PictoView) botSay(a *App) {
	if v.bot == nil {
		ready := make(chan struct{}, 1)
		b, err := pcConnect(v.addr, pcPlayer{Name: "Rin", Color: 0xe0882a}, func(ev pcEvent) {
			if ev.Type == "sv_roomData" {
				ready <- struct{}{}
			}
		})
		if err != nil {
			return
		}
		v.bot = b
		time.Sleep(150 * time.Millisecond)
		b.Join(v.room, pcPlayer{Name: "Rin", Color: 0xe0882a})
		select {
		case <-ready:
		case <-time.After(2 * time.Second):
		}
	}
	c := newCompose()
	for _, r := range "hi! drew you a star" {
		c.typeRune(r)
	}
	c.px, c.py = 150, 72
	c.penDown(true)
	for i := 1; i <= 5; i++ { // a five-pointed star
		ang := -math.Pi/2 + float64(i)*4*math.Pi/5
		tx, ty := 150+int(16*math.Cos(ang)), 52+int(16*math.Sin(ang))
		c.move(tx-c.px, ty-c.py)
	}
	c.penDown(false)
	time.Sleep(450 * time.Millisecond) // the server's message rate limit
	v.bot.Say(c.message(pcPlayer{Name: "Rin", Color: 0xe0882a}))
	time.Sleep(200 * time.Millisecond)
}

// ---- drawing the screens ----

func (v *PictoView) Draw(a *App) {
	if !v.inRoom {
		v.drawRooms(a)
		return
	}
	g := a.g
	g.dsGrid(0, 0, W, H)
	// the log
	logH := 86
	g.setClip(pcBoxX, 0, pcBoxW, logH)
	y := logH - 1 + v.scroll
	for i := len(v.log) - 1; i >= 0 && y > 0; i-- {
		it := v.log[i]
		h := it.height()
		y -= h
		switch it.kind {
		case 0:
			g.blit(pcBoxX, y, it.img)
		default:
			col := pcRoomColor[pcRoomIndex(v.room)]
			g.dsButton(pcBoxX, y, pcBoxW, h, mix(col, cWhite, 0.55), false)
			what := "Now entering"
			if it.kind == 2 {
				what = "Now leaving"
			}
			x := g.text(pcBoxX+6, y+3, what+": ", cDark, 1, false)
			g.text(x, y+3, it.who.Name, intRGB(it.who.Color).darker(0.2), 1, false)
		}
		y -= 2
	}
	g.noClip()
	if len(v.log) == 0 {
		g.textC(pcBoxX+pcBoxW/2, 38, "Say hi: type, or Tab to draw", cDark, 1, false)
	}
	// the compose box
	c := v.compose
	bx, by := pcBoxX, H-pcBoxH
	g.setClip(bx, by, pcBoxW, pcBoxH)
	drawPcBox(g, bx, by, 5, v.me)
	drawPcStrokes(g, bx, by, pcBoxH, c.draw)
	for row, l := range c.lines {
		g.text(bx+pcRowX(row), by+pcRowY[row], string(l), cPcInk, 1, false)
	}
	drawPcTexts(g, bx, by, c.extra)
	blink := int(a.t*2.5)%2 == 0
	if v.drawing {
		pc := cPcInk
		if c.eraser {
			pc = hex(0xd03030)
		}
		if c.down || blink {
			for d := -3; d <= 3; d++ {
				if d < -1 || d > 1 {
					g.px(bx+c.px+d, by+c.py, pc)
					g.px(bx+c.px, by+c.py+d, pc)
				}
			}
			if c.down {
				g.px(bx+c.px, by+c.py, pc)
			}
		}
	} else if blink {
		cx := bx + pcRowX(c.row) + textW(string(c.lines[c.row]), 1, false) + 1
		g.vline(cx, by+pcRowY[c.row]-1, 11, a.theme())
	}
	g.noClip()
	v.drawTools(a)
	v.drawSide(a)
}

// drawTools is the left column: scroll arrows, the room's letter, then pen, eraser, thick, thin.
func (v *PictoView) drawTools(a *App) {
	g := a.g
	g.rect(0, 0, pcBoxX, H, hex(0x3a3a3a))
	g.vline(pcBoxX-1, 0, H, cBlack)
	btn := func(y int, on bool, art []string) {
		base := cButton
		if on {
			base = a.theme()
		}
		g.dsSquare(2, y, 18, 18, base, on)
		g.glyph(11, y+9, art, 1, inkOn(base))
	}
	btn(3, false, artPcUp)
	btn(23, false, artPcDown)
	col := pcRoomColor[pcRoomIndex(v.room)]
	g.dsSquare(2, 48, 18, 18, col, false)
	g.textC(11, 52, string(rune('A'+pcRoomIndex(v.room))), cWhite, 1, false)
	c := v.compose
	btn(89, v.drawing && !c.eraser, artPcPen)
	btn(109, v.drawing && c.eraser, artPcEraser)
	btn(129, v.drawing && c.thick, artPcThick)
	btn(149, v.drawing && !c.thick, artPcThin)
}

// drawSide is the right column: the room and mode up top, SEND / COPY / CLEAR below.
func (v *PictoView) drawSide(a *App) {
	g := a.g
	x, w := pcBoxX+pcBoxW, W-pcBoxX-pcBoxW
	g.rect(x, 0, w, H, hex(0x3a3a3a))
	g.vline(x, 0, H, cBlack)
	n := 0
	if i := pcRoomIndex(v.room); i < len(v.counts) {
		n = v.counts[i]
	}
	g.textC(x+w/2, 4, "ROOM "+string(rune('A'+pcRoomIndex(v.room))), cWhite, 1, false)
	if n > 0 {
		g.textSmallC(x+w/2, 17, fmt.Sprintf("%d/%d here", n, pcRoomMax), hex(0xb0b0b0))
	}
	mode, hint := "TYPE", "TAB: DRAW"
	if v.drawing {
		mode, hint = "DRAW", "TAB: TYPE"
	}
	g.dsButton(x+4, 28, w-8, 16, a.theme(), false)
	g.textC(x+w/2, 31, mode, inkOn(a.theme()), 1, false)
	g.textSmallC(x+w/2, 47, hint, hex(0xb0b0b0))
	if v.drawing {
		pen := "UP"
		if v.compose.down {
			pen = "DOWN"
		}
		g.textSmallC(x+w/2, 56, "SPACE: PEN", hex(0xb0b0b0))
		g.textSmallC(x+w/2, 65, "PEN "+pen, hex(0xd0d0d0))
	}
	if v.sess.host != nil {
		g.textSmallC(x+w/2, 77, "HOSTING", hex(0x9ad09a))
	}
	button := func(y, h int, label, key string, on bool) {
		base := cButton
		if on {
			base = a.theme()
		}
		g.dsButton(x+4, y, w-8, h, base, false)
		g.textC(x+w/2, y+3, label, inkOn(base), 1, false)
		g.textSmallC(x+w/2, y+h-9, key, mix(inkOn(base), base, 0.4))
	}
	button(90, 26, "SEND", "ENTER", !v.compose.empty())
	button(119, 24, "COPY", map[bool]string{true: "R", false: "CTRL+R"}[v.drawing], false)
	button(146, 22, "CLEAR", "DEL", false)
}

func (v *PictoView) drawRooms(a *App) {
	g := a.g
	pageTitle(a, "PictoChat", artPicto)
	g.textC(W/2, barH+4, "Choose a Chat Room", cText, 1, false)
	for i := 0; i < 4; i++ {
		y := barH + 17 + i*21
		x, w := 44, W-88
		sel := i == v.sel
		base := cButton
		if sel {
			base = mix(pcRoomColor[i], cWhite, 0.35)
		}
		g.dsButton(x, y, w, 19, base, false)
		g.dsSquare(x+3, y+2, 15, 15, pcRoomColor[i], false)
		g.textC(x+10, y+5, string(rune('A'+i)), cWhite, 1, false)
		ink, cntInk := neutralInk(base, sel), cSub
		if sel {
			cntInk = ink
		}
		g.text(x+26, y+5, "Chat Room "+string(rune('A'+i)), ink, 1, false)
		g.textR(x+w-8, y+5, fmt.Sprintf("%d/%d", v.counts[i], pcRoomMax), cntInk, 1, false)
	}
	info := v.label
	if v.sess.code != "" {
		info = "Code " + v.sess.code + " - share it - via " + v.sess.via
	}
	if v.sess.host != nil {
		ip := v.sess.hostIP
		if ip == "" {
			ip = "this Cardputer"
		}
		info = fmt.Sprintf("Hosting: others join %s", net.JoinHostPort(ip, fmt.Sprint(v.sess.host.Port())))
	}
	g.textSmallC(W/2, 120, strings.ToUpper(fit(info, W-16, 1, false)), cDark)
}

// ---- small pictures for the tool column (drawn here, 7 x 7) ----

var (
	// PictoChat's smiley (icons/pictochat.svg, from tools/mkpictoicon.py)
	artPicto = []string{
		"#########",
		"#.......#",
		"#..#.#..#",
		"#..#.#..#",
		"#.......#",
		"#.#...#.#",
		"#..###..#",
		"#.......#",
		"#########",
	}
	artPcUp     = []string{"...#...", "..###..", ".#####.", "#######", "..###..", "..###..", "..###.."}
	artPcDown   = []string{"..###..", "..###..", "..###..", "#######", ".#####.", "..###..", "...#..."}
	artPcPen    = []string{".....##", "....###", "...###.", "..###..", ".###...", "##.....", "#......"}
	artPcEraser = []string{"...###.", "..#####", ".#####.", "#.###..", "#..#...", ".##....", "......."}
	artPcThick  = []string{".......", ".......", "#######", "#######", "#######", ".......", "......."}
	artPcThin   = []string{".......", ".......", ".......", "#######", ".......", ".......", "......."}
)

// dropView closes a view wherever it is in the stack.
func (a *App) dropView(v View) {
	keep := a.stack[:1]
	for _, s := range a.stack[1:] {
		if s != v {
			keep = append(keep, s)
		}
	}
	a.stack = keep
	a.dirty = true
	a.syncText()
}

func (a *App) hasView(v View) bool {
	for _, s := range a.stack {
		if s == v {
			return true
		}
	}
	return false
}
