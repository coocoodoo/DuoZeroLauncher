package main

// Row lists: the settings pages and pickers are lists of rounded bars on graph paper. A row is a
// toggle, a slider, a choice, an action/link, or plain information.

import (
	"dslauncher/cp0"
)

type RowKind int

const (
	rowInfo RowKind = iota
	rowToggle
	rowSlider
	rowChoice
	rowAction
	rowLink
)

type Row struct {
	Label   string
	Kind    RowKind
	Value   string // info text, or text shown after an action/link
	On      bool   // toggle state
	Num     int    // slider value
	Min     int
	Max     int
	Step    int
	Unit    string
	Choices []string
	Choice  int
	Off     bool // greyed out
	// called when the row is used; for sliders and choices after the value changed
	Do func(a *App, r *Row)
}

// RowList is a scrolling list of rows under a title bar.
type RowList struct {
	Title   string
	Art     []string
	Rows    []*Row
	Sel     int
	top     int
	Footer  string
	OnBack  func(a *App)
	Refresh func(a *App, l *RowList) // reload values (also on 4/Enter of an info row)
	Extra   func(a *App, l *RowList, k cp0.Key) bool
	BarFn   func(a *App, l *RowList) []BarItem
}

const rowsVisible = 6

func (l *RowList) cur() *Row {
	if len(l.Rows) == 0 {
		return nil
	}
	l.Sel = max(0, min(l.Sel, len(l.Rows)-1))
	return l.Rows[l.Sel]
}

func (l *RowList) Key(a *App, k cp0.Key) {
	if l.Extra != nil && l.Extra(a, l, k) {
		return
	}
	r := l.cur()
	switch {
	case k.Back() || k.Slot() == 4:
		a.snd.Play("back")
		if l.OnBack != nil {
			l.OnBack(a)
		}
		a.pop()
	case k.Name == "UP":
		if l.Sel > 0 {
			l.Sel--
			a.snd.Play("move")
		} else {
			a.snd.Play("bump")
		}
	case k.Name == "DOWN":
		if l.Sel < len(l.Rows)-1 {
			l.Sel++
			a.snd.Play("move")
		} else {
			a.snd.Play("bump")
		}
	case r == nil || r.Off:
		if k.Name == "ENTER" || k.Slot() == 0 {
			a.snd.Play("error")
		}
	case k.Name == "LEFT" || k.Slot() == 1:
		l.adjust(a, r, -1)
	case k.Name == "RIGHT" || k.Slot() == 2:
		l.adjust(a, r, 1)
	case k.Name == "ENTER" || k.Slot() == 0:
		switch r.Kind {
		case rowToggle:
			r.On = !r.On
			a.snd.Play(map[bool]string{true: "toggle-on", false: "toggle-off"}[r.On])
			if r.Do != nil {
				r.Do(a, r)
			}
		case rowAction, rowLink:
			a.snd.Play("select")
			if r.Do != nil {
				r.Do(a, r)
			}
		case rowChoice:
			l.adjust(a, r, 1)
		case rowInfo:
			if r.Do != nil {
				a.snd.Play("select")
				r.Do(a, r)
			} else if l.Refresh != nil {
				a.snd.Play("tick")
				l.Refresh(a, l)
			}
		}
	case k.Slot() == 3 && l.Refresh != nil:
		a.snd.Play("tick")
		l.Refresh(a, l)
	}
}

func (l *RowList) adjust(a *App, r *Row, d int) {
	switch r.Kind {
	case rowSlider:
		step := max(r.Step, 1)
		n := max(r.Min, min(r.Max, r.Num+d*step))
		if n == r.Num {
			a.snd.Play("bump")
			return
		}
		r.Num = n
		a.snd.Play("move")
		if r.Do != nil {
			r.Do(a, r)
		}
	case rowChoice:
		if len(r.Choices) == 0 {
			return
		}
		r.Choice = (r.Choice + d + len(r.Choices)) % len(r.Choices)
		a.snd.Play("move")
		if r.Do != nil {
			r.Do(a, r)
		}
	case rowToggle:
		want := d > 0
		if r.On != want {
			r.On = want
			a.snd.Play(map[bool]string{true: "toggle-on", false: "toggle-off"}[r.On])
			if r.Do != nil {
				r.Do(a, r)
			}
		}
	}
}

func (l *RowList) Bar(a *App) []BarItem {
	if l.BarFn != nil {
		return l.BarFn(a, l)
	}
	r := l.cur()
	items := []BarItem{{}, {}, {}, {}, {"BACK", artBack, false}}
	if l.Refresh != nil {
		items[3] = BarItem{"REFRESH", artRefresh, false}
	}
	if r == nil {
		return items
	}
	switch r.Kind {
	case rowToggle:
		items[0] = BarItem{map[bool]string{true: "TURN OFF", false: "TURN ON"}[r.On], artPower, r.Off}
	case rowSlider:
		items[0] = BarItem{"", nil, false}
		items[1] = BarItem{"LESS", artMinus, r.Off}
		items[2] = BarItem{"MORE", artPlus, r.Off}
	case rowChoice:
		items[1] = BarItem{"PREV", artLeft, r.Off}
		items[2] = BarItem{"NEXT", artRight, r.Off}
	case rowAction:
		items[0] = BarItem{"DO IT", artOK, r.Off}
	case rowLink:
		items[0] = BarItem{"OPEN", artOpen, r.Off}
	case rowInfo:
		if r.Do != nil {
			items[0] = BarItem{"OPEN", artOpen, false}
		}
	}
	return items
}

func (l *RowList) Draw(a *App) {
	g := a.g
	pageTitle(a, l.Title, l.Art)
	if len(l.Rows) == 0 {
		g.textC(W/2, 60, "Nothing here yet", cBlack, 1, false)
		if l.Footer != "" {
			g.textC(W/2, 122, l.Footer, cDark, 1, false)
		}
		return
	}
	if l.Sel < l.top {
		l.top = l.Sel
	}
	if l.Sel >= l.top+rowsVisible {
		l.top = l.Sel - rowsVisible + 1
	}
	x, w := 8, W-22
	for i := l.top; i < len(l.Rows) && i < l.top+rowsVisible; i++ {
		y := barH + 3 + (i-l.top)*17
		drawRow(a, x, y, w, l.Rows[i], i == l.Sel)
	}
	// scroll marker
	if len(l.Rows) > rowsVisible {
		th := 100 * rowsVisible / len(l.Rows)
		ty := 18 + (100-th)*l.top/max(len(l.Rows)-rowsVisible, 1)
		g.rect(W-10, 18, 5, 100, cGridBG)
		g.frame(W-10, 18, 5, 100, cBorder)
		g.dsButton(W-11, ty, 7, th, a.theme(), false)
	}
	if l.Footer != "" {
		g.textC(W/2, 122, fit(l.Footer, W-16, 1, false), cDark, 1, false)
	}
}

func drawRow(a *App, x, y, w int, r *Row, sel bool) {
	g := a.g
	ink := cBlack
	if r.Off {
		ink = hex(0x8a8a8a)
	}
	base := cButton
	if sel {
		base = a.theme()
	}
	if r.Kind == rowInfo && r.Do == nil {
		g.dsDetails(x, y, w, 15)
		if sel {
			g.frame(x-1, y-1, w+2, 17, a.theme())
		}
	} else {
		g.dsButton(x, y, w, 15, base, false)
		if sel {
			ink = inkOn(base)
			if r.Off {
				ink = mix(ink, base, 0.5)
			}
		}
	}
	g.text(x+7, y+3, fit(r.Label, w/2-4, 1, false), ink, 1, false)
	rx := x + w - 7
	sub := cBorder
	if sel && !(r.Kind == rowInfo && r.Do == nil) {
		sub = ink
	}
	switch r.Kind {
	case rowInfo:
		g.textR(rx, y+3, fit(r.Value, w/2-10, 1, false), sub, 1, false)
	case rowToggle:
		drawToggle(g, rx-26, y+2, r.On, a.theme(), r.Off)
		if r.Value != "" {
			g.textR(rx-32, y+3, fit(r.Value, w/2-40, 1, false), sub, 1, false)
		}
	case rowSlider:
		tw := 90
		tx := rx - tw - 32
		g.rect(tx, y+5, tw, 5, cGridBG)
		g.frame(tx, y+5, tw, 5, cBorder)
		f := 0
		if r.Max > r.Min {
			f = (tw - 8) * (r.Num - r.Min) / (r.Max - r.Min)
		}
		if f > 0 {
			g.rect(tx+1, y+6, f, 3, a.theme())
		}
		g.dsButton(tx+f, y+2, 8, 11, cButton, false)
		g.textR(rx, y+3, itoa(r.Num)+r.Unit, sub, 1, false)
	case rowChoice:
		v := ""
		if r.Choice >= 0 && r.Choice < len(r.Choices) {
			v = r.Choices[r.Choice]
		}
		vw := textW(v, 1, false)
		g.glyph(rx-vw-11, y+7, []string{"..#", ".##", "###", ".##", "..#"}, 1, sub)
		g.textR(rx-6, y+3, v, sub, 1, false)
		g.glyph(rx-1, y+7, []string{"#..", "##.", "###", "##.", "#.."}, 1, sub)
	case rowAction:
		if r.Value != "" {
			g.textR(rx, y+3, fit(r.Value, w/2-10, 1, false), sub, 1, false)
		}
	case rowLink:
		if r.Value != "" {
			g.textR(rx-8, y+3, fit(r.Value, w/2-16, 1, false), sub, 1, false)
		}
		g.glyph(rx-1, y+7, []string{"#..", "##.", "###", "##.", "#.."}, 1, sub)
	}
}

// drawToggle is a pair of small buttons, ON in the favourite colour when on.
func drawToggle(g *G, x, y int, on bool, theme RGB, off bool) {
	onBase, offBase := cButton, cButton
	if on {
		onBase = theme
	} else {
		offBase = hex(0x606060)
	}
	if off {
		onBase, offBase = mix(onBase, cWhite, 0.5), mix(offBase, cWhite, 0.5)
	}
	g.dsSquare(x, y, 13, 11, offBase, !on)
	g.dsSquare(x+13, y, 13, 11, onBase, on)
	g.glyph(x+7, y+5, []string{"#####"}, 1, inkOn(offBase))
	g.glyph(x+20, y+5, []string{"..#..", "..#..", "#####", "..#..", "..#.."}, 1, inkOn(onBase))
}

// pageTitle draws a full-width title bar and the grid paper below it.
func pageTitle(a *App, title string, art []string) {
	g := a.g
	g.dsGrid(0, barH, W, 132-barH)
	g.dsBar(0, 0, W, barH, a.theme())
	ink := inkOn(a.theme())
	x := 5
	if art != nil {
		g.glyph(x+5, 8, art, 1, ink)
		x += 14
	}
	g.text(x, 4, title, ink, 1, false)
	drawStatus(a, W-3, 3, ink)
}
