package main

// Smaller screens: app details, typing, confirmations, the lock screen, the alarm, the calculator
// and the IP panel.

import (
	"dslauncher/cp0"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
	"unicode"
)

// TextView screens type with the keyboard: F/X/Z/C and the number keys type instead of acting.
type TextView interface{ wantsText() }

func (a *App) syncText() {
	_, ok := a.top().(TextView)
	a.keys.setTextMode(ok)
}

// keycaps draws a strip of key hints as centred keycaps, deliberately not lined up with keys
// 4-8 (those type characters on these screens).
func keycaps(a *App, pairs ...string) {
	g := a.g
	y0 := 132
	g.dsBarLg(0, y0, W, H-y0, cButton, true)
	total := 0
	for i := 0; i < len(pairs); i += 2 {
		total += textW(pairs[i], 1, false) + 10 + textW(pairs[i+1], 1, false) + 12
	}
	x := (W - total + 12) / 2
	for i := 0; i < len(pairs); i += 2 {
		kw := textW(pairs[i], 1, false) + 8
		g.dsButton(x, 143, kw, 15, cButton, false)
		g.text(x+4, 146, pairs[i], cBlack, 1, false)
		x += kw + 4
		x = g.text(x, 146, pairs[i+1], cBlack, 1, false) + 12
	}
}

// ---- app details ----

type AppInfo struct{ e *Entry }

func (v *AppInfo) Key(a *App, k cp0.Key) {
	switch {
	case k.Back() || k.Slot() == 4:
		a.snd.Play("back")
		a.pop()
	case k.Name == "ENTER" || k.Slot() == 0:
		a.pop()
		a.open(v.e)
	case k.Slot() == 1:
		on := a.cfg.togglePin(v.e.Key)
		a.save()
		a.apps = orderApps(a.all, a.cfg, false)
		a.snd.Play(map[bool]string{true: "toggle-on", false: "toggle-off"}[on])
	case k.Slot() == 2:
		if v.e.Builtin == "settings" {
			a.notify("Settings cannot be hidden", true)
			return
		}
		a.cfg.Hidden[v.e.Key] = true
		a.save()
		a.apps = orderApps(a.all, a.cfg, false)
		if h, ok := a.stack[0].(*Home); ok {
			h.keep(a)
		}
		a.pop()
		a.snd.Play("toggle-off")
		a.notify("Hidden. Settings > Launcher shows it again", false)
	}
}

func (v *AppInfo) Bar(a *App) []BarItem {
	fave := "FAVE"
	if a.cfg.pinIndex(v.e.Key) >= 0 {
		fave = "UNPIN"
	}
	return []BarItem{{"OPEN", artOpen, false}, {fave, artStar, false}, {"HIDE", artEye, false}, {},
		{"BACK", artBack, false}}
}

func (v *AppInfo) Draw(a *App) {
	a.stack[0].Draw(a)
	g := a.g
	g.shade(0, 0, W, 132, cBlack, 0.4)
	e := v.e
	x, y, w := 8, 18, W-16
	g.dsInfo(x, y, w, 47, cButton)
	g.dsInfoSquare(x+2, y+2, 43, a.theme())
	if ic := e.Icon(38); ic != nil {
		g.icon(x+4, y+4, ic, 1)
	} else {
		placeholder(g, x+4, y+4, 38, e, a.theme())
	}
	lx := x + 48
	kind := "App"
	if e.Builtin != "" {
		kind = "Built in"
	}
	if a.cfg.pinIndex(e.Key) >= 0 {
		kind += " / pinned"
	}
	g.text(lx+3, y+3, fit(e.Name, w-60-textW(kind, 1, false), 1, false), cBlack, 1, false)
	g.textR(x+w-5, y+3, kind, cBorder, 1, false)
	g.dsInfoText(lx, y+15, w-50, 31)
	sub := e.Comment
	if sub == "" {
		sub = "No description"
	}
	for i, l := range wrap(sub, w-62, false) {
		if i < 2 {
			g.text(lx+5, y+19+i*12, l, cWhite, 1, false)
		}
	}
	rows := [][2]string{}
	if e.Exec != "" {
		rows = append(rows, [2]string{"Runs", e.Exec})
	}
	if e.Builtin == "" {
		rows = append(rows, [2]string{"From", e.Key})
	}
	last := "never"
	if t := a.cfg.Used[e.Key]; t > 0 {
		last = time.Unix(t, 0).Format("Jan 2, 15:04")
	}
	rows = append(rows, [2]string{"Last opened", last})
	dy := y + 52
	g.dsDetails(x, dy, w, 8+len(rows)*13)
	for i, r := range rows {
		ry := dy + 5 + i*13
		g.text(x+8, ry, r[0], cBorder, 1, false)
		g.text(x+78, ry, fit(r[1], w-88, 1, false), cBlack, 1, false)
	}
}

// ---- typing ----

type Input struct {
	title, prompt string
	text          []rune
	limit         int
	secret        bool
	done          func(string)
	shownAt       time.Time
}

func newInput(title, prompt, initial string, limit int, secret bool, done func(string)) *Input {
	return &Input{title: title, prompt: prompt, text: []rune(initial), limit: limit, secret: secret, done: done}
}

func (v *Input) wantsText() {}

func (v *Input) Key(a *App, k cp0.Key) {
	switch {
	case k.Back():
		a.snd.Play("back")
		a.pop()
		a.syncText()
	case k.Name == "ENTER":
		a.snd.Play("select")
		a.pop()
		a.syncText()
		v.done(string(v.text))
	case k.Name == "BACKSPACE" || k.Name == "DELETE":
		if len(v.text) > 0 {
			v.text = v.text[:len(v.text)-1]
			a.snd.Play("tick")
		}
	case k.Name == "SPACE" || k.Ch == ' ':
		v.add(a, ' ')
	case k.Ch != 0 && k.Ctrl == 0:
		v.add(a, k.Ch)
	}
}

func (v *Input) add(a *App, r rune) {
	if len(v.text) >= v.limit {
		a.snd.Play("error")
		return
	}
	v.text = append(v.text, r)
	v.shownAt = time.Now()
	a.snd.Play("tick")
}

func (v *Input) Animating(a *App) bool { return false }

func (v *Input) Bar(a *App) []BarItem { return nil }

func (v *Input) Draw(a *App) {
	a.syncText()
	g := a.g
	pageTitle(a, v.title, artEdit)
	g.dsLabel(16, 32, v.prompt, a.theme())
	x, y, w, h := 12, 50, W-24, 15
	g.dsInput(x, y, w, h, true, a.theme())
	shown := string(v.text)
	if v.secret {
		shown = strings.Repeat("*", len(v.text))
		if len(v.text) > 0 && time.Since(v.shownAt) < 800*time.Millisecond {
			shown = shown[:len(shown)-1] + string(v.text[len(v.text)-1])
		}
	}
	inner := w - 2*14 - 10
	for textW(shown, 1, false) > inner && len(shown) > 0 {
		shown = shown[1:]
	}
	cx := g.text(x+19, y+3, shown, cWhite, 1, false)
	if int(a.t*2)%2 == 0 {
		g.rect(cx+1, y+2, 2, 11, cFocus)
	}
	g.textR(x+w, y+20, fmt.Sprintf("%d/%d", len(v.text), v.limit), cBorder, 1, false)
	keycaps(a, "ENTER", "OK", "ESC", "Cancel", "DEL", "Erase")
}

// ---- yes / no ----

type Confirm struct {
	title, msg string
	yes        func()
}

func confirm(a *App, title, msg string, yes func()) {
	a.snd.Play("modal")
	a.push(&Confirm{title: title, msg: msg, yes: yes})
}

func (v *Confirm) Key(a *App, k cp0.Key) {
	switch {
	case k.Name == "ENTER" || k.Slot() == 0:
		a.snd.Play("select")
		a.pop()
		v.yes()
	case k.Back() || k.Slot() == 4:
		a.snd.Play("back")
		a.pop()
	}
}

func (v *Confirm) Bar(a *App) []BarItem {
	return []BarItem{{"YES", artOK, false}, {}, {}, {}, {"NO", artNo, false}}
}

func (v *Confirm) Draw(a *App) {
	if len(a.stack) >= 2 {
		a.stack[len(a.stack)-2].Draw(a)
	}
	g := a.g
	g.noClip()
	g.shade(0, 0, W, 132, cBlack, 0.35)
	lines := wrap(v.msg, 220, false)
	h := 34 + len(lines)*12
	x, y, w := 34, (132-h)/2, W-68
	g.dsAlert(x, y, w, h)
	g.textC(W/2, y+10, v.title, cPictoEnter, 1, false)
	for i, l := range lines {
		if i < 4 {
			g.textC(W/2, y+24+i*12, l, cWhite, 1, false)
		}
	}
}

// ---- lock screen ----

type Lock struct{ asking bool }

func newLock(a *App) *Lock {
	a.snd.Play("lock")
	return &Lock{}
}

func (v *Lock) Key(a *App, k cp0.Key) {
	if k.Name != "TAB" && k.Name != "ENTER" {
		return
	}
	if a.cfg.PIN == "" {
		a.snd.Play("unlock")
		a.pop()
		return
	}
	a.push(newInput("UNLOCK", "Type your PIN", "", 12, true, func(pin string) {
		if pin == a.cfg.PIN {
			a.snd.Play("unlock")
			a.pop()
		} else {
			a.notify("Wrong PIN", true)
		}
	}))
}

func (v *Lock) Animating(a *App) bool { return true }

func (v *Lock) Bar(a *App) []BarItem { return nil }

func (v *Lock) Draw(a *App) {
	g := a.g
	now := time.Now()
	g.dsGrid(0, barH, W, H-barH)
	g.dsBar(0, 0, W, barH, a.theme())
	ink := inkOn(a.theme())
	g.text(5, 4, fit(a.cfg.Name, 150, 1, false), ink, 1, false)
	drawStatus(a, W-3, 3, ink)
	g.dsClock(10, 24, 96, now, a.theme())
	hh := now.Hour()
	if !a.cfg.Clock24 {
		hh = (hh+11)%12 + 1
	}
	t := fmt.Sprintf("%02d:%02d", hh, now.Minute())
	g.fontText(fontClock30, 120, 28, t, cBlack)
	g.text(122, 66, now.Format("Monday, January 2"), cBlack, 1, false)
	msg := a.cfg.Message
	if a.cfg.isBirthday(now) {
		msg = "Happy birthday, " + a.cfg.Name + "!"
	}
	if msg != "" {
		bx, by, bw := 116, 84, W-124
		g.dsBubble(bx, by, bw, 36)
		g.dsLabel(bx+3, by+3, a.cfg.Name, a.theme())
		g.text(bx+6, by+20, fit(msg, bw-12, 1, false), cBlack, 1, false)
	}
	g.dsBarLg(0, 132, W, H-132, a.theme(), true)
	if int(a.t*1.5)%2 == 0 {
		g.textC(W/2, 147, "Press TAB to unlock", inkOn(a.theme()), 1, false)
	}
}

// ---- alarm ----

type Alarm struct {
	started time.Time
	rung    time.Time
}

func newAlarm(a *App) *Alarm { return &Alarm{started: time.Now()} }

func (v *Alarm) Animating(a *App) bool {
	if time.Since(v.rung) > 1300*time.Millisecond && time.Since(v.started) < 90*time.Second {
		v.rung = time.Now()
		a.snd.Play("alarm")
	}
	return true
}

func (v *Alarm) Key(a *App, k cp0.Key) {
	switch {
	case k.Slot() == 1:
		t := time.Now().Add(5 * time.Minute)
		a.alarmDay = ""
		a.cfg.AlarmH, a.cfg.AlarmM = t.Hour(), t.Minute()
		a.notify("Snoozing for five minutes", false)
		a.pop()
	case k.Name == "ENTER" || k.Back() || k.Slot() == 0:
		a.snd.Play("back")
		a.pop()
	}
}

func (v *Alarm) Bar(a *App) []BarItem {
	return []BarItem{{"STOP", artOK, false}, {"SNOOZE", artBell, false}, {}, {}, {}}
}

func (v *Alarm) Draw(a *App) {
	g := a.g
	g.dsGrid(0, barH, W, 132-barH)
	g.dsBar(0, 0, W, barH, a.theme())
	g.text(5, 4, "Alarm", inkOn(a.theme()), 1, false)
	g.dsClock(16, 24, 96, time.Now(), a.theme())
	sw := math.Sin(a.t*18) * 3
	g.glyph(214+int(sw), 44, artBell, 3, a.theme().darker(0.2))
	g.fontText(fontClock20, 176, 70, fmtClock(a.cfg, time.Now().Hour(), time.Now().Minute()), cBlack)
	g.textC(214, 104, "Good morning, "+a.cfg.Name, cBlack, 1, false)
}

// ---- calculator ----

type Calc struct {
	expr    []rune
	history [][2]string
	err     string
}

func newCalc(a *App) *Calc { return &Calc{} }

func (v *Calc) wantsText() {}

func (v *Calc) Key(a *App, k cp0.Key) {
	switch {
	case k.Back():
		a.snd.Play("back")
		a.pop()
		a.syncText()
	case k.Name == "ENTER":
		src := strings.TrimSpace(string(v.expr))
		if src == "" {
			return
		}
		val, err := evalExpr(src)
		if err != nil {
			v.err = err.Error()
			a.snd.Play("error")
			return
		}
		v.err = ""
		res := formatNum(val)
		v.history = append(v.history, [2]string{src, res})
		if len(v.history) > 4 {
			v.history = v.history[1:]
		}
		v.expr = []rune(res)
		a.snd.Play("select")
	case k.Name == "BACKSPACE" || k.Name == "DELETE":
		if len(v.expr) > 0 {
			v.expr = v.expr[:len(v.expr)-1]
			a.snd.Play("tick")
		}
	case k.Name == "TAB":
		v.expr, v.err = nil, ""
		a.snd.Play("toggle-off")
	case k.Ch != 0 && k.Ctrl == 0 && len(v.expr) < 60:
		v.expr = append(v.expr, k.Ch)
		v.err = ""
		a.snd.Play("tick")
	}
}

func (v *Calc) Bar(a *App) []BarItem { return nil }

func (v *Calc) Draw(a *App) {
	a.syncText()
	g := a.g
	pageTitle(a, "Calculator", artPlus)
	x, w := 8, W-16
	g.dsInfoText(x, 20, w, 62)
	for i, h := range v.history {
		y := 24 + i*13
		g.textR(x+w-8, y, fit(h[0]+" = "+h[1], w-16, 1, false), hex(0xb0b0b0), 1, false)
	}
	ix, iw := g.calcLCD(x, 88, w, 24)
	s := string(v.expr)
	for textW(s, 2, false) > iw-22 && len(s) > 0 {
		s = s[1:]
	}
	g.text(ix+iw-15-textW(s, 2, false), 92, s, cWhite, 2, false)
	if int(a.t*2)%2 == 0 { // an underscore after the number, so it can't read as a digit "1"
		g.rect(ix+iw-12, 106, 7, 2, cFocus)
	}
	if v.err != "" {
		g.text(x+2, 118, v.err, cBad, 1, false)
	} else {
		g.text(x+2, 118, "+ - * / ^ % ( )  sqrt sin cos tan log ln pi", cBorder, 1, false)
	}
	keycaps(a, "ENTER", "=", "DEL", "Erase", "TAB", "Clear", "ESC", "Back")
}

func formatNum(v float64) string {
	if math.IsInf(v, 0) || math.IsNaN(v) {
		return "undefined"
	}
	if v == math.Trunc(v) && math.Abs(v) < 1e15 {
		return strconv.FormatFloat(v, 'f', 0, 64)
	}
	s := strconv.FormatFloat(v, 'g', 12, 64)
	return s
}

// evalExpr evaluates arithmetic with precedence, parentheses, ^, %, and a few functions.
func evalExpr(src string) (float64, error) {
	p := &parser{s: []rune(strings.ToLower(src))}
	v, err := p.expr()
	if err != nil {
		return 0, err
	}
	p.space()
	if p.i < len(p.s) {
		return 0, fmt.Errorf("unexpected %q", string(p.s[p.i]))
	}
	return v, nil
}

type parser struct {
	s []rune
	i int
}

func (p *parser) space() {
	for p.i < len(p.s) && p.s[p.i] == ' ' {
		p.i++
	}
}

func (p *parser) peek() rune {
	p.space()
	if p.i < len(p.s) {
		return p.s[p.i]
	}
	return 0
}

func (p *parser) expr() (float64, error) {
	v, err := p.term()
	for err == nil {
		switch p.peek() {
		case '+':
			p.i++
			var r float64
			r, err = p.term()
			v += r
		case '-':
			p.i++
			var r float64
			r, err = p.term()
			v -= r
		default:
			return v, nil
		}
	}
	return v, err
}

func (p *parser) term() (float64, error) {
	v, err := p.power()
	for err == nil {
		switch p.peek() {
		case '*', 'x':
			p.i++
			var r float64
			r, err = p.power()
			v *= r
		case '/':
			p.i++
			var r float64
			r, err = p.power()
			if err == nil && r == 0 {
				return 0, fmt.Errorf("cannot divide by zero")
			}
			v /= r
		case '%':
			p.i++
			var r float64
			r, err = p.power()
			v = math.Mod(v, r)
		default:
			return v, nil
		}
	}
	return v, err
}

func (p *parser) power() (float64, error) {
	b, err := p.unary()
	if err != nil {
		return 0, err
	}
	if p.peek() == '^' {
		p.i++
		e, err := p.power()
		if err != nil {
			return 0, err
		}
		return math.Pow(b, e), nil
	}
	return b, nil
}

func (p *parser) unary() (float64, error) {
	switch p.peek() {
	case '-':
		p.i++
		v, err := p.unary()
		return -v, err
	case '+':
		p.i++
		return p.unary()
	}
	return p.atom()
}

func (p *parser) atom() (float64, error) {
	c := p.peek()
	switch {
	case c == '(':
		p.i++
		v, err := p.expr()
		if err != nil {
			return 0, err
		}
		if p.peek() != ')' {
			return 0, fmt.Errorf("missing )")
		}
		p.i++
		return v, nil
	case unicode.IsDigit(c) || c == '.':
		start := p.i
		for p.i < len(p.s) && (unicode.IsDigit(p.s[p.i]) || p.s[p.i] == '.') {
			p.i++
		}
		return strconv.ParseFloat(string(p.s[start:p.i]), 64)
	case unicode.IsLetter(c):
		start := p.i
		for p.i < len(p.s) && unicode.IsLetter(p.s[p.i]) {
			p.i++
		}
		name := string(p.s[start:p.i])
		switch name {
		case "pi":
			return math.Pi, nil
		case "e":
			return math.E, nil
		}
		fns := map[string]func(float64) float64{"sqrt": math.Sqrt, "sin": math.Sin, "cos": math.Cos,
			"tan": math.Tan, "log": math.Log10, "ln": math.Log, "abs": math.Abs, "round": math.Round}
		f, ok := fns[name]
		if !ok {
			return 0, fmt.Errorf("unknown %q", name)
		}
		v, err := p.atom()
		if err != nil {
			return 0, err
		}
		return f(v), nil
	}
	if c == 0 {
		return 0, fmt.Errorf("unfinished")
	}
	return 0, fmt.Errorf("unexpected %q", string(c))
}

// ---- IP panel ----

func newIPPanel(a *App) *RowList {
	l := &RowList{Title: "IP PANEL", Art: artWifi}
	l.Refresh = func(a *App, l *RowList) {
		var rows []*Row
		rows = append(rows, &Row{Label: "Hostname", Value: hostname()})
		st := a.status.snapshot()
		if st.SSID != "" {
			rows = append(rows, &Row{Label: "Wi-Fi", Value: st.SSID})
		}
		for _, i := range interfaces() {
			rows = append(rows, &Row{Label: i.Name, Value: i.IP})
			if i.MAC != "" {
				rows = append(rows, &Row{Label: "  " + i.Name + " MAC", Value: i.MAC})
			}
		}
		ssh := "off"
		if sshActive() {
			ssh = "on"
		}
		rows = append(rows, &Row{Label: "SSH server", Value: ssh})
		l.Rows = rows
		l.Footer = ""
		if ip := primaryIP(); ip != "" {
			l.Footer = "Connect: ssh " + userName() + "@" + ip
		}
	}
	l.Refresh(a, l)
	return l
}

func userName() string {
	if n := strings.TrimSpace(getenv("USER")); n != "" {
		return n
	}
	return "pi"
}
