package main

// Type to search: Space or "/" on the home screen opens a search box. Typing filters the apps
// (name starts with what you typed first, then a word in the name, then anywhere in it, then
// the letters in order, so "gz" finds GridZero); Tab moves to the next match, Enter opens it,
// Esc closes the box.

import (
	"dslauncher/cp0"
	"sort"
	"strings"
	"unicode"
)

type Search struct {
	q   []rune
	sel int
}

func (v *Search) wantsText() {}

// searchApps ranks the apps that match q (case-insensitive); an empty q matches nothing.
func searchApps(list []*Entry, q string) []*Entry {
	q = strings.ToLower(strings.TrimSpace(q))
	if q == "" {
		return nil
	}
	type hit struct {
		e     *Entry
		score int
		at    int
	}
	var hits []hit
	for i, e := range list {
		name := strings.ToLower(e.Name)
		score := -1
		switch {
		case strings.HasPrefix(name, q):
			score = 0
		case wordPrefix(name, q):
			score = 1
		case strings.Contains(name, q):
			score = 2
		case subsequence(name, q):
			score = 3
		}
		if score >= 0 {
			hits = append(hits, hit{e, score, i})
		}
	}
	sort.SliceStable(hits, func(i, j int) bool {
		if hits[i].score != hits[j].score {
			return hits[i].score < hits[j].score
		}
		return hits[i].at < hits[j].at
	})
	out := make([]*Entry, len(hits))
	for i, h := range hits {
		out[i] = h.e
	}
	return out
}

// wordPrefix: q starts one of the words in name ("terminal" in "Tiny Terminal", "zero" in
// "GridZero" counts too, at the capital in the original name).
func wordPrefix(name, q string) bool {
	runes := []rune(name)
	for i := 1; i < len(runes); i++ {
		if !unicode.IsLetter(runes[i-1]) && !unicode.IsDigit(runes[i-1]) && strings.HasPrefix(string(runes[i:]), q) {
			return true
		}
	}
	return false
}

// subsequence: the letters of q appear in name in order.
func subsequence(name, q string) bool {
	qr := []rune(q)
	j := 0
	for _, r := range name {
		if j < len(qr) && r == qr[j] {
			j++
		}
	}
	return j == len(qr)
}

func (v *Search) results(a *App) []*Entry {
	r := searchApps(a.apps, string(v.q))
	if v.sel >= len(r) {
		v.sel = 0
	}
	return r
}

func (v *Search) Key(a *App, k cp0.Key) {
	switch {
	case k.Back():
		a.snd.Play("back")
		a.pop()
	case k.Name == "ENTER":
		r := v.results(a)
		if len(r) == 0 {
			a.snd.Play("error")
			return
		}
		e := r[v.sel]
		a.pop()
		if h, ok := a.top().(*Home); ok { // the home strip follows, so Esc from the app lands on it
			h.recent = false
			h.key = e.Key
			h.keep(a)
		}
		a.open(e)
	case k.Name == "TAB":
		if r := v.results(a); len(r) > 1 {
			v.sel = (v.sel + 1) % len(r)
			a.snd.Play("move")
		}
	case k.Name == "BACKSPACE" || k.Name == "DELETE":
		if len(v.q) > 0 {
			v.q = v.q[:len(v.q)-1]
			v.sel = 0
			a.snd.Play("tick")
		}
	case k.Name == "SPACE" || k.Ch == ' ':
		if len(v.q) > 0 && len(v.q) < 24 {
			v.q = append(v.q, ' ')
			a.snd.Play("tick")
		}
	case k.Ch != 0 && k.Ctrl == 0 && len(v.q) < 24:
		v.q = append(v.q, k.Ch)
		v.sel = 0
		a.snd.Play("tick")
	}
}

func (v *Search) Bar(a *App) []BarItem { return nil }

func (v *Search) Draw(a *App) {
	a.syncText()
	g := a.g
	pageTitle(a, "Search apps", nil)
	x, w := 8, W-16
	g.dsInput(x, 20, w, 15, true, a.theme())
	q := string(v.q)
	cx := g.text(x+19, 23, q, cWhite, 1, false)
	if int(a.t*2)%2 == 0 {
		g.rect(cx+1, 22, 2, 11, cFocus)
	}
	r := v.results(a)
	switch {
	case len(v.q) == 0:
		g.textC(W/2, 60, "Type part of an app's name", cText, 1, false)
		g.textC(W/2, 74, "Tab: next match   Enter: open", cSub, 1, false)
	case len(r) == 0:
		g.textC(W/2, 66, "No app matches \""+fit(q, 160, 1, false)+"\"", cText, 1, false)
	default:
		top := 0
		if v.sel >= 5 {
			top = v.sel - 4
		}
		for i := top; i < len(r) && i < top+5; i++ {
			y := 40 + (i-top)*18
			sel := i == v.sel
			base := cButton
			if sel {
				base = a.theme()
			}
			g.dsButton(x, y, w, 16, base, false)
			if ic := r[i].Icon(12); ic != nil {
				g.icon(x+3, y+2, ic, 1)
			}
			ink := neutralInk(base, sel)
			g.text(x+20, y+3, fit(r[i].Name, w-90, 1, false), ink, 1, false)
			if sel {
				g.textR(x+w-6, y+3, "Enter", ink, 1, false)
			}
		}
		if len(r) > 5 {
			g.textR(W-8, 131-10, itoa(len(r))+" matches", cSub, 1, false)
		}
	}
	keycaps(a, "ENTER", "Open", "TAB", "Next", "ESC", "Close")
}
