// Package cp0 is a CardputerZero UI kit for Go. One file, standard library only.
//
//	scr := cp0.Open()            // 320x170 LCD
//	keys := cp0.NewKeys()        // keyboard with key repeat and Fn-free arrows
//	menu := cp0.NewMenu("Play", "Scores")
//	for !cp0.ShouldQuit() {
//		for _, k := range keys.Poll(30) {
//			if k.Quit() { return }                 // Esc held 1 s
//			if i, ok := menu.Key(k); ok { _ = i }  // item chosen with Enter
//		}
//		scr.Clear(cp0.BG)
//		scr.Text(8, 8, "hello", cp0.FG)
//		menu.Draw(scr, 4, 30, 312, 6)
//		scr.Show()
//	}
//
// Keys: while keys.TextMode is false (default) plain F/X/Z/C arrive as UP/DOWN/LEFT/RIGHT. TextField switches
// TextMode on while it is being typed in. Env CP0_FB / CP0_KBD override the devices (used by zc-test).
package cp0

import (
	"bytes"
	"encoding/binary"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
	"unicode/utf8"
)

const (
	W, H         = 320, 170
	CharW, CharH = 6, 8 // one character at scale 1
)

// RGB converts 8-bit color to the LCD's RGB565.
func RGB(r, g, b int) uint16 { return uint16((r>>3)<<11 | (g>>2)<<5 | b>>3) }

var (
	BG     = RGB(30, 24, 40)
	Panel  = RGB(62, 53, 70)
	FG     = RGB(240, 240, 240)
	Dim    = RGB(150, 140, 160)
	Accent = RGB(48, 225, 185)
	Sel    = RGB(77, 101, 180)
	Warn   = RGB(251, 185, 84)
	Bad    = RGB(232, 59, 59)
)

var quit atomic.Bool

// ShouldQuit is true after SIGINT/SIGTERM (the launcher's long Home press). Check it in your main loop.
func ShouldQuit() bool { return quit.Load() }

// Screen is the LCD. Draw into Px (RGB565) with the helpers, then call Show.
type Screen struct {
	f      *os.File
	stride int
	Px     []uint16
	buf    []byte
	// CP0_TEXTLOG: where to report the text of each frame (used by zc-test).
	logPath string
	log     []string

	mu        sync.Mutex  // guards buf and writes to f
	shown     atomic.Bool // a frame has been sent
	guard     atomic.Bool // repair the console-cursor corner (off while another app has the screen)
	guardWake chan struct{}
}

// Open opens the LCD (CP0_FB overrides the device) and starts listening for quit signals.
func Open() *Screen {
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	go func() { <-sig; quit.Store(true) }()
	path := os.Getenv("CP0_FB")
	stride := W * 2
	if path == "" {
		path = "/dev/fb0"
		if _, err := os.Stat("/dev/fb_lcd"); err == nil {
			path = "/dev/fb_lcd"
		}
		if b, err := os.ReadFile("/sys/class/graphics/fb0/stride"); err == nil {
			if s, err := strconv.Atoi(strings.TrimSpace(string(b))); err == nil {
				stride = s
			}
		}
	}
	real := path != os.Getenv("CP0_FB")
	f, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		f = nil
	}
	s := &Screen{f: f, stride: stride, Px: make([]uint16, W*H), buf: make([]byte, stride*H), logPath: os.Getenv("CP0_TEXTLOG"),
		guardWake: make(chan struct{}, 1)}
	s.guard.Store(true)
	if f != nil && real {
		go s.guardCorner()
	}
	return s
}

// Capture reads back what is on the panel now (whoever drew it) as RGB565 pixels.
func (s *Screen) Capture() ([]uint16, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.f == nil {
		return nil, os.ErrNotExist
	}
	raw := make([]byte, s.stride*H)
	if _, err := s.f.ReadAt(raw, 0); err != nil {
		return nil, err
	}
	px := make([]uint16, W*H)
	for y := 0; y < H; y++ {
		for x := 0; x < W; x++ {
			px[y*W+x] = binary.LittleEndian.Uint16(raw[y*s.stride+x*2:])
		}
	}
	return px, nil
}

// GuardCorner turns the corner repair on or off. Turn it off before handing the screen to
// another program, and back on when it returns.
func (s *Screen) GuardCorner(on bool) {
	s.guard.Store(on)
	if on {
		select {
		case s.guardWake <- struct{}{}:
		default:
		}
	}
}

// guardCorner: the Linux text console still runs under the app, and its blinking cursor
// draws an 8x8 black cell in the bottom-left corner between frames. Every 8 ms, put back
// whatever the last frame had there. The region covers the console's last text rows. While it
// is off the ticker stops and the goroutine sleeps (it used to tick 125 times a second anyway).
func (s *Screen) guardCorner() {
	const y0, cols = 150, 16 * 2 // rows 150-169, the 16 left-most pixels
	region := make([]byte, (H-y0)*s.stride)
	t := time.NewTicker(8 * time.Millisecond)
	defer t.Stop()
	for {
		if !s.guard.Load() {
			t.Stop()
			<-s.guardWake
			t.Reset(8 * time.Millisecond)
			continue
		}
		<-t.C
		if !s.guard.Load() || !s.shown.Load() {
			continue
		}
		s.mu.Lock()
		if _, err := s.f.ReadAt(region, int64(y0*s.stride)); err == nil {
			for y := y0; y < H; y++ {
				got := region[(y-y0)*s.stride : (y-y0)*s.stride+cols]
				want := s.buf[y*s.stride : y*s.stride+cols]
				if !bytes.Equal(got, want) {
					s.f.WriteAt(want, int64(y*s.stride))
				}
			}
		}
		s.mu.Unlock()
	}
}

func (s *Screen) Clear(c uint16) {
	s.log = s.log[:0]
	for i := range s.Px {
		s.Px[i] = c
	}
}

func (s *Screen) Set(x, y int, c uint16) {
	if x >= 0 && y >= 0 && x < W && y < H {
		s.Px[y*W+x] = c
	}
}

func (s *Screen) Rect(x, y, w, h int, c uint16) {
	if x <= 0 && y <= 0 && x+w >= W && y+h >= H {
		s.log = s.log[:0]
	}
	for j := max(y, 0); j < min(y+h, H); j++ {
		for i := max(x, 0); i < min(x+w, W); i++ {
			s.Px[j*W+i] = c
		}
	}
}

// BarX are the x centers of the five key-bar labels above the number keys 4 5 6 7 8; BarY is their y center.
var BarX = [5]int{49, 105, 161, 217, 273}

const BarY = 153

// KeyBar draws M5Stack's recommended key bar on the bottom 28 px: one short label (max 8 chars, "" = empty slot)
// for each of the number keys 4 5 6 7 8, which sit right under the LCD, with a tick pointing at its key.
// active highlights the open page (-1 for none). Read the keys with Key.Slot.
func (s *Screen) KeyBar(labels []string, active int) {
	s.Rect(0, 142, W, 28, Panel)
	for i, lab := range labels {
		if i >= 5 || lab == "" {
			continue
		}
		x, on := BarX[i], i == active
		fg, tick := FG, Dim
		if on {
			s.Rect(x-26, 143, 53, 21, Accent)
			fg, tick = BG, Accent
		}
		n := min(utf8.RuneCountInString(lab), 8)
		s.TextClip(x-n*CharW/2, BarY-CharH/2, lab, fg, x+27)
		s.Rect(x, 166, 1, 4, tick)
	}
}

// KeyStrip is the text version of the bar, for when 4-8 are not the keys in use (typing, dialogs):
// scr.KeyStrip("ESC", "Cancel", "OK", "Confirm").
func (s *Screen) KeyStrip(pairs ...string) {
	s.Rect(0, 142, W, 28, Panel)
	n := min(len(pairs)/2, 5)
	if n == 0 {
		return
	}
	step := W / n
	for i := 0; i < n; i++ {
		key, what := pairs[2*i], pairs[2*i+1]
		w := (utf8.RuneCountInString(key) + 1 + utf8.RuneCountInString(what)) * CharW
		x := i*step + max(step-w, 0)/2
		x2 := s.Text(x, BarY-CharH/2, key, Accent)
		s.TextClip(x2+CharW, BarY-CharH/2, what, FG, (i+1)*step)
	}
}

func (s *Screen) Frame(x, y, w, h int, c uint16) {
	s.Rect(x, y, w, 1, c)
	s.Rect(x, y+h-1, w, 1, c)
	s.Rect(x, y, 1, h, c)
	s.Rect(x+w-1, y, 1, h, c)
}

// Text draws at scale 1 (6x8 px per char, 53 chars across) and returns x after the text.
func (s *Screen) Text(x, y int, t string, c uint16) int { return s.textScaled(x, y, t, c, 1, W) }

// TextClip draws text that stops before maxX.
func (s *Screen) TextClip(x, y int, t string, c uint16, maxX int) int {
	return s.textScaled(x, y, t, c, 1, maxX)
}

// TextBig draws scaled text: scale 2 = 12x16 px per char.
func (s *Screen) TextBig(x, y int, t string, c uint16, scale int) int {
	return s.textScaled(x, y, t, c, scale, W)
}

func (s *Screen) textScaled(x, y int, t string, c uint16, scale, maxX int) int {
	cx := x
	if s.logPath != "" {
		if n := utf8.RuneCountInString(t); n > 0 {
			width := n*CharW*scale - scale
			clipped := 0
			if x+width > maxX && maxX < W {
				clipped = 1
			}
			s.log = append(s.log, strconv.Itoa(x)+"\t"+strconv.Itoa(y)+"\t"+strconv.Itoa(width)+"\t"+strconv.Itoa(7*scale)+"\t"+strconv.Itoa(clipped)+"\t"+strings.Join(strings.Fields(t), " "))
		}
	}
	for _, r := range t {
		if cx+5*scale > maxX {
			break
		}
		g := [5]byte{0x7f, 0x41, 0x41, 0x41, 0x7f}
		if r >= 0x20 && r < 0x7f {
			g = font[r-0x20]
		}
		for col, bits := range g {
			for row := 0; row < 8; row++ {
				if bits>>uint(row)&1 == 1 {
					s.Rect(cx+col*scale, y+row*scale, scale, scale, c)
				}
			}
		}
		cx += CharW * scale
	}
	return cx
}

// Show pushes the frame to the LCD. Call it only when something changed.
func (s *Screen) Show() {
	if s.logPath != "" {
		l := s.log
		if len(l) > 200 {
			l = l[:200]
		}
		if os.WriteFile(s.logPath+".tmp", []byte(strings.Join(l, "\n")+"\n"), 0o644) == nil {
			os.Rename(s.logPath+".tmp", s.logPath)
		}
	}
	if s.f == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for y := 0; y < H; y++ {
		for x := 0; x < W; x++ {
			binary.LittleEndian.PutUint16(s.buf[y*s.stride+x*2:], s.Px[y*W+x])
		}
	}
	s.f.WriteAt(s.buf, 0)
	s.shown.Store(true)
}

// Key is one key press (or a repeat while held).
type Key struct {
	Code int
	// "UP" "DOWN" "LEFT" "RIGHT" "ENTER" "ESC" "BACKSPACE" "TAB" "SPACE" "DELETE" "HOME" "END" "PGUP" "PGDN",
	// or "" for plain text keys (see Ch).
	Name   string
	Ch     rune // typed character, 0 if none
	Shift  bool
	Ctrl   rune // letter held with Ctrl, 0 if none
	Alt    bool
	Repeat bool
	Held   float64 // seconds the key has been down
	// Released: the key was let go (only reported when Keys.Releases is on). Tab also sends one
	// Repeat event after TabHold while held, so a long press can be told from a tap.
	Released bool
}

// TabHold is how long Tab must be held before its single "held" event.
const TabHold = 800 * time.Millisecond

// Quit: Esc held for 1 second, leave the app.
func (k Key) Quit() bool { return k.Name == "ESC" && k.Held >= 1 }

// Slot: which key-bar slot (0-4) this press is for, the number keys 4 5 6 7 8; -1 for any other key.
// Ignore it while a text field is active.
func (k Key) Slot() int {
	if k.Code >= 5 && k.Code <= 9 && k.Ctrl == 0 && !k.Alt && !k.Repeat {
		return k.Code - 5
	}
	return -1
}

// Back: a fresh Esc press, go back one level.
func (k Key) Back() bool { return k.Name == "ESC" && !k.Repeat }

const (
	lower = "\x00\x001234567890\x00\x00\x00\x00qwertyuiop\x00\x00\x00\x00asdfghjkl\x00\x00\x00\x00\x00zxcvbnm\x00\x00\x00\x00\x00\x00 "
	upper = "\x00\x00!@#$%^&*()\x00\x00\x00\x00QWERTYUIOP\x00\x00\x00\x00ASDFGHJKL\x00\x00\x00\x00\x00ZXCVBNM\x00\x00\x00\x00\x00\x00 "

	repeatDelay = 400 * time.Millisecond
	repeatRate  = 60 * time.Millisecond
)

// Sym layer + punctuation (the device's TCA8418 keymap).
var sym = map[int]rune{26: '!', 27: '@', 39: '#', 40: '$', 41: '%', 43: '^', 51: '&', 52: '*', 53: '(', 94: ')',
	55: '~', 69: '`', 70: '_', 71: '-', 72: '+', 73: '=', 74: '[', 75: ']', 76: '{', 77: '}', 79: ';', 80: ':',
	81: '\'', 82: '"', 83: '<', 85: '>', 86: '\\', 89: '|', 90: ',', 91: '.', 92: '/', 93: '?'}

var special = map[int]string{1: "ESC", 14: "BACKSPACE", 15: "TAB", 28: "ENTER", 96: "ENTER", 57: "SPACE", 103: "UP",
	108: "DOWN", 105: "LEFT", 106: "RIGHT", 102: "HOME", 107: "END", 104: "PGUP", 109: "PGDN", 111: "DELETE",
	113: "MUTE", 114: "VOLUMEDOWN", 115: "VOLUMEUP", // Fn+A, Fn+S, Fn+D on the Cardputer
	224: "BRIGHTNESSDOWN", 225: "BRIGHTNESSUP", // Fn+U, Fn+I
	99: "PRINT", 210: "PRINT"} // Fn+J (PrtSc): KEY_SYSRQ or KEY_PRINT depending on the keymap

var nav = map[int]string{33: "UP", 45: "DOWN", 44: "LEFT", 46: "RIGHT"} // plain F, X, Z, C

type held struct{ since, next time.Time }

// Keys reads the keyboard. TextMode false: plain F/X/Z/C are arrows; true: they type letters.
type Keys struct {
	ch       chan [2]int
	TextMode bool
	Releases bool // also report key releases (Key.Released)
	mods     map[int]bool
	down     map[int]*held
}

// NewKeys opens the keyboard (CP0_KBD overrides the device).
func NewKeys() *Keys {
	path := os.Getenv("CP0_KBD")
	if path == "" {
		path = "/dev/input/by-path/platform-3f804000.i2c-event"
	}
	k := &Keys{ch: make(chan [2]int, 128), mods: map[int]bool{}, down: map[int]*held{}}
	go func() {
		f, err := os.Open(path)
		if err != nil {
			return
		}
		buf := make([]byte, 24*32)
		for {
			n, err := f.Read(buf)
			if err != nil {
				return
			}
			for i := 0; i+24 <= n; i += 24 {
				if binary.LittleEndian.Uint16(buf[i+16:]) == 1 {
					k.ch <- [2]int{int(binary.LittleEndian.Uint16(buf[i+18:])), int(int32(binary.LittleEndian.Uint32(buf[i+20:])))}
				}
			}
		}
	}()
	return k
}

// Poll waits up to ms for input and returns the keys pressed (plus repeats of held keys).
func (k *Keys) Poll(ms int) []Key {
	wait := time.Duration(ms) * time.Millisecond
	t0 := time.Now()
	for _, d := range k.down {
		if w := d.next.Sub(t0); w < wait {
			wait = max(w, 0)
		}
	}
	var evs [][2]int
	timer := time.NewTimer(wait)
	select {
	case e := <-k.ch:
		evs = append(evs, e)
	drain:
		for {
			select {
			case e := <-k.ch:
				evs = append(evs, e)
			default:
				break drain
			}
		}
	case <-timer.C:
	}
	timer.Stop()
	now := time.Now()
	for code, d := range k.down {
		if !now.Before(d.next) {
			evs = append(evs, [2]int{code, 2})
			d.next = now.Add(repeatRate)
			if code == 15 { // Tab: one "held" event, then quiet
				d.next = now.Add(24 * time.Hour)
			}
		}
	}
	var out []Key
	for _, e := range evs {
		code, val := e[0], e[1]
		switch code {
		case 42, 54, 29, 97, 56, 100:
			k.mods[code] = val != 0
			continue
		}
		if val == 0 {
			if d, ok := k.down[code]; ok && k.Releases {
				out = append(out, Key{Code: code, Name: special[code], Released: true, Held: now.Sub(d.since).Seconds()})
			}
			delete(k.down, code)
			continue
		}
		if _, ok := k.down[code]; val == 2 && !ok {
			continue // let go in this same poll: no extra repeat after the release
		}
		if _, ok := k.down[code]; val == 1 && !ok {
			next := now.Add(repeatDelay)
			if code == 28 || code == 96 { // Enter never repeats
				next = now.Add(24 * time.Hour)
			}
			if code == 15 { // Tab doesn't repeat either; it reports being held once
				next = now.Add(TabHold)
			}
			k.down[code] = &held{now, next}
		}
		key := Key{Code: code, Name: special[code], Repeat: val == 2}
		if d, ok := k.down[code]; ok {
			key.Held = now.Sub(d.since).Seconds()
		}
		key.Shift = k.mods[42] || k.mods[54]
		key.Alt = k.mods[56] || k.mods[100]
		if r, ok := sym[code]; ok {
			key.Ch = r
		} else if code < len(lower) && lower[code] != 0 {
			if key.Shift {
				key.Ch = rune(upper[code])
			} else {
				key.Ch = rune(lower[code])
			}
		}
		isLetter := (key.Ch >= 'a' && key.Ch <= 'z') || (key.Ch >= 'A' && key.Ch <= 'Z')
		if (k.mods[29] || k.mods[97]) && isLetter {
			key.Ctrl = key.Ch | 0x20
			key.Ch = 0
		} else if n, ok := nav[code]; ok && !k.TextMode && !key.Alt {
			key.Name, key.Ch = n, 0
		}
		out = append(out, key)
	}
	return out
}

// Menu is a scrolling list: Up/Down move, Enter chooses.
type Menu struct {
	Items    []string
	Sel, top int
}

func NewMenu(items ...string) *Menu { return &Menu{Items: items} }

// Key feeds a key; ok is true when an item was chosen with Enter.
func (m *Menu) Key(k Key) (index int, ok bool) {
	n := len(m.Items)
	if n == 0 {
		return 0, false
	}
	switch {
	case k.Name == "UP":
		m.Sel = (m.Sel + n - 1) % n
	case k.Name == "DOWN":
		m.Sel = (m.Sel + 1) % n
	case k.Name == "ENTER" && !k.Repeat:
		return m.Sel, true
	}
	return 0, false
}

// Draw shows `rows` rows (12 px each) at x,y with width w.
func (m *Menu) Draw(s *Screen, x, y, w, rows int) {
	if m.Sel < m.top {
		m.top = m.Sel
	}
	if m.Sel >= m.top+rows {
		m.top = m.Sel + 1 - rows
	}
	for i := m.top; i < len(m.Items) && i < m.top+rows; i++ {
		ry := y + (i-m.top)*12
		if i == m.Sel {
			s.Rect(x, ry, w, 12, Sel)
			s.Rect(x, ry, 2, 12, Accent)
		}
		s.TextClip(x+6, ry+2, m.Items[i], FG, x+w-2)
	}
}

// TextField is one line of input. Begin starts typing (F/X/Z/C become letters); Enter or Esc ends it.
type TextField struct {
	Text   string
	Active bool
	Max    int
}

func (t *TextField) Begin(k *Keys) { t.Active, k.TextMode = true, true }
func (t *TextField) End(k *Keys)   { t.Active, k.TextMode = false, false }

// Key feeds a key while active; it returns true when typing just ended.
func (t *TextField) Key(key Key, keys *Keys) bool {
	if !t.Active {
		return false
	}
	if (key.Name == "ENTER" && !key.Repeat) || key.Back() {
		t.End(keys)
		return true
	}
	if key.Name == "BACKSPACE" {
		if len(t.Text) > 0 {
			_, n := utf8.DecodeLastRuneInString(t.Text)
			t.Text = t.Text[:len(t.Text)-n]
		}
	} else if key.Ch != 0 && (t.Max == 0 || utf8.RuneCountInString(t.Text) < t.Max) {
		t.Text += string(key.Ch)
	}
	return false
}

func (t *TextField) Draw(s *Screen, x, y, w int) {
	c := Dim
	if t.Active {
		c = Accent
	}
	s.Frame(x, y, w, 14, c)
	cols := max((w-8)/CharW, 2) - 1
	r := []rune(t.Text)
	if len(r) > cols {
		r = r[len(r)-cols:]
	}
	end := s.TextClip(x+4, y+3, string(r), FG, x+w-3)
	if t.Active {
		s.Rect(end, y+3, 5, 8, Accent)
	}
}

// Classic 5x7 font, column-major, bit0 = top row. ASCII 0x20..0x7e.
var font = [95][5]byte{
	{0x00, 0x00, 0x00, 0x00, 0x00}, {0x00, 0x00, 0x5f, 0x00, 0x00}, {0x00, 0x07, 0x00, 0x07, 0x00},
	{0x14, 0x7f, 0x14, 0x7f, 0x14}, {0x24, 0x2a, 0x7f, 0x2a, 0x12}, {0x23, 0x13, 0x08, 0x64, 0x62},
	{0x36, 0x49, 0x56, 0x20, 0x50}, {0x00, 0x05, 0x03, 0x00, 0x00}, {0x00, 0x1c, 0x22, 0x41, 0x00},
	{0x00, 0x41, 0x22, 0x1c, 0x00}, {0x14, 0x08, 0x3e, 0x08, 0x14}, {0x08, 0x08, 0x3e, 0x08, 0x08},
	{0x00, 0x80, 0x60, 0x00, 0x00}, {0x08, 0x08, 0x08, 0x08, 0x08}, {0x00, 0x60, 0x60, 0x00, 0x00},
	{0x20, 0x10, 0x08, 0x04, 0x02}, {0x3e, 0x51, 0x49, 0x45, 0x3e}, {0x00, 0x42, 0x7f, 0x40, 0x00},
	{0x42, 0x61, 0x51, 0x49, 0x46}, {0x21, 0x41, 0x45, 0x4b, 0x31}, {0x18, 0x14, 0x12, 0x7f, 0x10},
	{0x27, 0x45, 0x45, 0x45, 0x39}, {0x3c, 0x4a, 0x49, 0x49, 0x30}, {0x01, 0x71, 0x09, 0x05, 0x03},
	{0x36, 0x49, 0x49, 0x49, 0x36}, {0x06, 0x49, 0x49, 0x29, 0x1e}, {0x00, 0x36, 0x36, 0x00, 0x00},
	{0x00, 0x56, 0x36, 0x00, 0x00}, {0x08, 0x14, 0x22, 0x41, 0x00}, {0x14, 0x14, 0x14, 0x14, 0x14},
	{0x00, 0x41, 0x22, 0x14, 0x08}, {0x02, 0x01, 0x51, 0x09, 0x06}, {0x32, 0x49, 0x79, 0x41, 0x3e},
	{0x7e, 0x11, 0x11, 0x11, 0x7e}, {0x7f, 0x49, 0x49, 0x49, 0x36}, {0x3e, 0x41, 0x41, 0x41, 0x22},
	{0x7f, 0x41, 0x41, 0x22, 0x1c}, {0x7f, 0x49, 0x49, 0x49, 0x41}, {0x7f, 0x09, 0x09, 0x09, 0x01},
	{0x3e, 0x41, 0x49, 0x49, 0x7a}, {0x7f, 0x08, 0x08, 0x08, 0x7f}, {0x00, 0x41, 0x7f, 0x41, 0x00},
	{0x20, 0x40, 0x41, 0x3f, 0x01}, {0x7f, 0x08, 0x14, 0x22, 0x41}, {0x7f, 0x40, 0x40, 0x40, 0x40},
	{0x7f, 0x02, 0x0c, 0x02, 0x7f}, {0x7f, 0x04, 0x08, 0x10, 0x7f}, {0x3e, 0x41, 0x41, 0x41, 0x3e},
	{0x7f, 0x09, 0x09, 0x09, 0x06}, {0x3e, 0x41, 0x51, 0x21, 0x5e}, {0x7f, 0x09, 0x19, 0x29, 0x46},
	{0x46, 0x49, 0x49, 0x49, 0x31}, {0x01, 0x01, 0x7f, 0x01, 0x01}, {0x3f, 0x40, 0x40, 0x40, 0x3f},
	{0x1f, 0x20, 0x40, 0x20, 0x1f}, {0x3f, 0x40, 0x38, 0x40, 0x3f}, {0x63, 0x14, 0x08, 0x14, 0x63},
	{0x07, 0x08, 0x70, 0x08, 0x07}, {0x61, 0x51, 0x49, 0x45, 0x43}, {0x00, 0x7f, 0x41, 0x41, 0x00},
	{0x02, 0x04, 0x08, 0x10, 0x20}, {0x00, 0x41, 0x41, 0x7f, 0x00}, {0x04, 0x02, 0x01, 0x02, 0x04},
	{0x40, 0x40, 0x40, 0x40, 0x40}, {0x00, 0x01, 0x02, 0x04, 0x00}, {0x20, 0x54, 0x54, 0x54, 0x78},
	{0x7f, 0x48, 0x44, 0x44, 0x38}, {0x38, 0x44, 0x44, 0x44, 0x20}, {0x38, 0x44, 0x44, 0x48, 0x7f},
	{0x38, 0x54, 0x54, 0x54, 0x18}, {0x08, 0x7e, 0x09, 0x01, 0x02}, {0x18, 0xa4, 0xa4, 0xa4, 0x7c},
	{0x7f, 0x08, 0x04, 0x04, 0x78}, {0x00, 0x44, 0x7d, 0x40, 0x00}, {0x40, 0x80, 0x84, 0x7d, 0x00},
	{0x7f, 0x10, 0x28, 0x44, 0x00}, {0x00, 0x41, 0x7f, 0x40, 0x00}, {0x7c, 0x04, 0x18, 0x04, 0x78},
	{0x7c, 0x08, 0x04, 0x04, 0x78}, {0x38, 0x44, 0x44, 0x44, 0x38}, {0xfc, 0x24, 0x24, 0x24, 0x18},
	{0x18, 0x24, 0x24, 0x24, 0xfc}, {0x7c, 0x08, 0x04, 0x04, 0x08}, {0x48, 0x54, 0x54, 0x54, 0x20},
	{0x04, 0x3f, 0x44, 0x40, 0x20}, {0x3c, 0x40, 0x40, 0x20, 0x7c}, {0x1c, 0x20, 0x40, 0x20, 0x1c},
	{0x3c, 0x40, 0x30, 0x40, 0x3c}, {0x44, 0x28, 0x10, 0x28, 0x44}, {0x1c, 0xa0, 0xa0, 0xa0, 0x7c},
	{0x44, 0x64, 0x54, 0x4c, 0x44}, {0x00, 0x08, 0x36, 0x41, 0x00}, {0x00, 0x00, 0x7f, 0x00, 0x00},
	{0x00, 0x41, 0x36, 0x08, 0x00}, {0x02, 0x01, 0x02, 0x04, 0x02},
}

// Glyph returns the 5x7 font columns for r (bit 0 = top row); a box for runes the font lacks.
func Glyph(r rune) [5]byte {
	if r >= 0x20 && r < 0x7f {
		return font[r-0x20]
	}
	return [5]byte{0x7f, 0x41, 0x41, 0x41, 0x7f}
}
