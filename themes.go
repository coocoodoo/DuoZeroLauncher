package main

// UI Themes: 10 surface palettes (5 light/mid, 5 dark) that change the launcher's paper, grid,
// chrome, clock, calendar and text colours while keeping the user's Favourite colour (a.theme())
// behaving the exact same way on top bars, selected rows/tiles, toggles, sliders and progress
// bars. Picked in Settings > Themes (with a live preview and a shortcut to Favourite colour).

import (
	"dslauncher/cp0"
	"fmt"
)

type UITheme struct {
	ID         string
	Name       string
	Tagline    string
	Dark       bool
	DarkChrome float64 // 0 = classic bright DS hard-light, 1 = centered bevel for dark chrome

	PaperEven RGB
	PaperOdd  RGB
	GridLine  RGB
	BoxEdge   RGB
	BoxInner  RGB
	PlateBG   RGB
	PlateLine RGB

	Button    RGB
	Border    RGB
	Focus     RGB
	Shadow    RGB
	HingeEdge RGB
	Hinge1    RGB
	Hinge2    RGB

	Text     RGB
	Sub      RGB
	Disabled RGB

	ClockBg1  RGB
	ClockBg2  RGB
	ClockGrid RGB
	ClockNum  RGB
	ClockDot  RGB
	ClockHand RGB
	ClockSec  RGB
	ClockPin  RGB

	CalFrame   RGB
	CalBorder  RGB
	CalHeader  RGB
	CalHeadSun RGB
	CalHeadSat RGB
	CalDay     RGB
	CalSun     RGB
	CalSat     RGB
	CalText    RGB
	CalTextSun RGB
	CalTextSat RGB
	CalToday   RGB
	CalTodayLt RGB
}

var uiThemes = []UITheme{
	{
		ID: "classic", Name: "Classic", Tagline: "Original DS grid paper",
		Dark: false, DarkChrome: 0.0,
		PaperEven: hex(0xffffff), PaperOdd: hex(0xe3e3e3), GridLine: hex(0xc3c3c3),
		BoxEdge: hex(0xa2a2a2), BoxInner: hex(0xffffff), PlateBG: hex(0xffffff), PlateLine: hex(0x9a9a9a),
		Button: hex(0x808080), Border: hex(0x494949), Focus: hex(0xe36100), Shadow: hex(0x18202c),
		HingeEdge: hex(0x000000), Hinge1: hex(0x8c8c8c), Hinge2: hex(0x6c6c6c),
		Text: hex(0x000000), Sub: hex(0x494949), Disabled: hex(0x8a8a8a),
		ClockBg1: hex(0xffffff), ClockBg2: hex(0xeeeeee), ClockGrid: hex(0xd6d6d6),
		ClockNum: hex(0xaaaaaa), ClockDot: hex(0x707070), ClockHand: hex(0x666666),
		ClockSec: hex(0x55799a), ClockPin: hex(0x3a3a3a),
		CalFrame: hex(0x303030), CalBorder: hex(0x929292), CalHeader: hex(0x696969),
		CalHeadSun: hex(0xd30000), CalHeadSat: hex(0x0041c3), CalDay: hex(0xf7f7f7),
		CalSun: hex(0xfba2eb), CalSat: hex(0x82aafb), CalText: hex(0x000000),
		CalTextSun: hex(0x790000), CalTextSat: hex(0x000082),
		CalToday: hex(0x3e5c78), CalTodayLt: hex(0xa9bccd),
	},
	{
		ID: "dark", Name: "Dark", Tagline: "Modern charcoal dark",
		Dark: true, DarkChrome: 1.0,
		PaperEven: hex(0x1e222a), PaperOdd: hex(0x181c23), GridLine: hex(0x2c323e),
		BoxEdge: hex(0x38404f), BoxInner: hex(0x272c36), PlateBG: hex(0x242933), PlateLine: hex(0x485266),
		Button: hex(0x303642), Border: hex(0x525c70), Focus: hex(0xff8c24), Shadow: hex(0x06080c),
		HingeEdge: hex(0x0b0d11), Hinge1: hex(0x3b4252), Hinge2: hex(0x2a303c),
		Text: hex(0xedf1f7), Sub: hex(0x9ca6b8), Disabled: hex(0x646e82),
		ClockBg1: hex(0x232833), ClockBg2: hex(0x1d212b), ClockGrid: hex(0x323948),
		ClockNum: hex(0x8d97ab), ClockDot: hex(0xa8b2c4), ClockHand: hex(0xdce3f0),
		ClockSec: hex(0x5fa8ff), ClockPin: hex(0xeef2fa),
		CalFrame: hex(0x525c70), CalBorder: hex(0x353c4b), CalHeader: hex(0x3a4252),
		CalHeadSun: hex(0xb5343f), CalHeadSat: hex(0x2e5cb8), CalDay: hex(0x222731),
		CalSun: hex(0x3b222a), CalSat: hex(0x1f2c42), CalText: hex(0xe8edf5),
		CalTextSun: hex(0xff9aa5), CalTextSat: hex(0x8ec0ff),
		CalToday: hex(0x7aa2f7), CalTodayLt: hex(0x3b4f7a),
	},
	{
		ID: "midnight", Name: "Midnight", Tagline: "Deep navy night sky",
		Dark: true, DarkChrome: 1.0,
		PaperEven: hex(0x10192b), PaperOdd: hex(0x0b1322), GridLine: hex(0x1d2d4a),
		BoxEdge: hex(0x2a4066), BoxInner: hex(0x17243d), PlateBG: hex(0x142038), PlateLine: hex(0x3a5687),
		Button: hex(0x1d2e4d), Border: hex(0x426094), Focus: hex(0xffaa2b), Shadow: hex(0x04070e),
		HingeEdge: hex(0x050a14), Hinge1: hex(0x2c436b), Hinge2: hex(0x1c2d4a),
		Text: hex(0xe6f0ff), Sub: hex(0x8aa4cc), Disabled: hex(0x526a91),
		ClockBg1: hex(0x142038), ClockBg2: hex(0x0f192d), ClockGrid: hex(0x233659),
		ClockNum: hex(0x7a98c7), ClockDot: hex(0x9cb6de), ClockHand: hex(0xd8e7ff),
		ClockSec: hex(0x56c8ff), ClockPin: hex(0xeef5ff),
		CalFrame: hex(0x426094), CalBorder: hex(0x243759), CalHeader: hex(0x283f66),
		CalHeadSun: hex(0xa8324a), CalHeadSat: hex(0x2660c4), CalDay: hex(0x142038),
		CalSun: hex(0x341d2e), CalSat: hex(0x162b4f), CalText: hex(0xe6f0ff),
		CalTextSun: hex(0xff9cb0), CalTextSat: hex(0x82c2ff),
		CalToday: hex(0x68b8ff), CalTodayLt: hex(0x284b7a),
	},
	{
		ID: "oled", Name: "OLED", Tagline: "Pure pitch black",
		Dark: true, DarkChrome: 1.0,
		PaperEven: hex(0x000000), PaperOdd: hex(0x0a0a0a), GridLine: hex(0x1d1d1d),
		BoxEdge: hex(0x2e2e2e), BoxInner: hex(0x141414), PlateBG: hex(0x0c0c0c), PlateLine: hex(0x444444),
		Button: hex(0x1a1a1a), Border: hex(0x484848), Focus: hex(0xff9100), Shadow: hex(0x000000),
		HingeEdge: hex(0x000000), Hinge1: hex(0x383838), Hinge2: hex(0x222222),
		Text: hex(0xf5f5f5), Sub: hex(0x9a9a9a), Disabled: hex(0x5c5c5c),
		ClockBg1: hex(0x0c0c0c), ClockBg2: hex(0x050505), ClockGrid: hex(0x222222),
		ClockNum: hex(0x888888), ClockDot: hex(0xaaaaaa), ClockHand: hex(0xe5e5e5),
		ClockSec: hex(0x4db8ff), ClockPin: hex(0xffffff),
		CalFrame: hex(0x484848), CalBorder: hex(0x282828), CalHeader: hex(0x2a2a2a),
		CalHeadSun: hex(0xa82424), CalHeadSat: hex(0x204ec2), CalDay: hex(0x0f0f0f),
		CalSun: hex(0x2e1214), CalSat: hex(0x101d33), CalText: hex(0xf2f2f2),
		CalTextSun: hex(0xff8a8a), CalTextSat: hex(0x80b8ff),
		CalToday: hex(0xd0d0d0), CalTodayLt: hex(0x484848),
	},
	{
		ID: "paper", Name: "Warm Paper", Tagline: "Cozy cream stationery",
		Dark: false, DarkChrome: 0.25,
		PaperEven: hex(0xfaf3e3), PaperOdd: hex(0xeee4ce), GridLine: hex(0xd5c7ab),
		BoxEdge: hex(0xb6a588), BoxInner: hex(0xfdf9ef), PlateBG: hex(0xfdf8eb), PlateLine: hex(0xa89779),
		Button: hex(0x9e8f77), Border: hex(0x594b38), Focus: hex(0xd95b00), Shadow: hex(0x2e2214),
		HingeEdge: hex(0x291f14), Hinge1: hex(0x9a8b74), Hinge2: hex(0x786a54),
		Text: hex(0x261d13), Sub: hex(0x5e4f3c), Disabled: hex(0x998a74),
		ClockBg1: hex(0xfdf8eb), ClockBg2: hex(0xf2e9d5), ClockGrid: hex(0xdcd0b8),
		ClockNum: hex(0xa6967c), ClockDot: hex(0x70614c), ClockHand: hex(0x594b38),
		ClockSec: hex(0xb85620), ClockPin: hex(0x382c1e),
		CalFrame: hex(0x473928), CalBorder: hex(0xb5a489), CalHeader: hex(0x786853),
		CalHeadSun: hex(0xbd2c24), CalHeadSat: hex(0x28569e), CalDay: hex(0xfbf6ea),
		CalSun: hex(0xf7cfc4), CalSat: hex(0xc7daf0), CalText: hex(0x261d13),
		CalTextSun: hex(0x801812), CalTextSat: hex(0x153566),
		CalToday: hex(0x8a5628), CalTodayLt: hex(0xd4b48c),
	},
	{
		ID: "gameboy", Name: "Game Boy", Tagline: "Classic olive LCD",
		Dark: false, DarkChrome: 0.55,
		PaperEven: hex(0xcadc9f), PaperOdd: hex(0xbacd8d), GridLine: hex(0x9ab368),
		BoxEdge: hex(0x6b8444), BoxInner: hex(0xd7e5b0), PlateBG: hex(0xd4e3ac), PlateLine: hex(0x526e32),
		Button: hex(0x88a45c), Border: hex(0x2b4a1e), Focus: hex(0x0f380f), Shadow: hex(0x0f260f),
		HingeEdge: hex(0x0f380f), Hinge1: hex(0x6b8c42), Hinge2: hex(0x4c6b2c),
		Text: hex(0x0f380f), Sub: hex(0x2d5420), Disabled: hex(0x5e7a42),
		ClockBg1: hex(0xd5e4ad), ClockBg2: hex(0xc5d897), ClockGrid: hex(0xa3bc6e),
		ClockNum: hex(0x4f7030), ClockDot: hex(0x306230), ClockHand: hex(0x1f4a1f),
		ClockSec: hex(0x0f380f), ClockPin: hex(0x0f380f),
		CalFrame: hex(0x0f380f), CalBorder: hex(0x6f8c44), CalHeader: hex(0x306230),
		CalHeadSun: hex(0x1f4d1f), CalHeadSat: hex(0x285928), CalDay: hex(0xd2e2a8),
		CalSun: hex(0xb8cf85), CalSat: hex(0xa8c474), CalText: hex(0x0f380f),
		CalTextSun: hex(0x0f380f), CalTextSat: hex(0x0f380f),
		CalToday: hex(0x0f380f), CalTodayLt: hex(0x8bac0f),
	},
	{
		ID: "sakura", Name: "Sakura", Tagline: "Soft cherry blossom",
		Dark: false, DarkChrome: 0.2,
		PaperEven: hex(0xfff5f8), PaperOdd: hex(0xf6e2ea), GridLine: hex(0xe2bccb),
		BoxEdge: hex(0xc493a7), BoxInner: hex(0xfff9fb), PlateBG: hex(0xfff8fa), PlateLine: hex(0xbd8ea2),
		Button: hex(0xab8295), Border: hex(0x663d50), Focus: hex(0xd92672), Shadow: hex(0x381828),
		HingeEdge: hex(0x381c2a), Hinge1: hex(0xb0889b), Hinge2: hex(0x8a6477),
		Text: hex(0x2e1522), Sub: hex(0x6e4558), Disabled: hex(0xa68293),
		ClockBg1: hex(0xfff8fa), ClockBg2: hex(0xfae8ef), ClockGrid: hex(0xe6c5d2),
		ClockNum: hex(0xb88a9d), ClockDot: hex(0x805266), ClockHand: hex(0x663b4e),
		ClockSec: hex(0xd94682), ClockPin: hex(0x3d1d2d),
		CalFrame: hex(0x522e40), CalBorder: hex(0xc798ab), CalHeader: hex(0x85576b),
		CalHeadSun: hex(0xcc2952), CalHeadSat: hex(0x4d5cb8), CalDay: hex(0xfff7fa),
		CalSun: hex(0xffd1e0), CalSat: hex(0xd4deff), CalText: hex(0x2e1522),
		CalTextSun: hex(0x8c1436), CalTextSat: hex(0x233075),
		CalToday: hex(0xb83b72), CalTodayLt: hex(0xe8a5c2),
	},
	{
		ID: "ocean", Name: "Ocean", Tagline: "Breezy coastal mist",
		Dark: false, DarkChrome: 0.25,
		PaperEven: hex(0xeef7fc), PaperOdd: hex(0xdcecf6), GridLine: hex(0xb4d4e6),
		BoxEdge: hex(0x84aec7), BoxInner: hex(0xf5fbff), PlateBG: hex(0xf4faff), PlateLine: hex(0x79a4bd),
		Button: hex(0x6f95ad), Border: hex(0x2d4e63), Focus: hex(0xe86a17), Shadow: hex(0x102536),
		HingeEdge: hex(0x112636), Hinge1: hex(0x6b91a8), Hinge2: hex(0x4b7087),
		Text: hex(0x0f2536), Sub: hex(0x355870), Disabled: hex(0x7492a6),
		ClockBg1: hex(0xf4faff), ClockBg2: hex(0xe3f0f8), ClockGrid: hex(0xc2dceb),
		ClockNum: hex(0x7aa3bd), ClockDot: hex(0x466e87), ClockHand: hex(0x2c4e66),
		ClockSec: hex(0x0088cc), ClockPin: hex(0x142f42),
		CalFrame: hex(0x224054), CalBorder: hex(0x89b2c9), CalHeader: hex(0x446d87),
		CalHeadSun: hex(0xc4314b), CalHeadSat: hex(0x1558b8), CalDay: hex(0xf2f9fd),
		CalSun: hex(0xf7cfd9), CalSat: hex(0xbedcfa), CalText: hex(0x0f2536),
		CalTextSun: hex(0x7d152a), CalTextSat: hex(0x0d3270),
		CalToday: hex(0x1c6b99), CalTodayLt: hex(0x8ec5e3),
	},
	{
		ID: "terminal", Name: "Terminal", Tagline: "Retro green phosphor",
		Dark: true, DarkChrome: 1.0,
		PaperEven: hex(0x08150c), PaperOdd: hex(0x040d07), GridLine: hex(0x13361d),
		BoxEdge: hex(0x1f5c30), BoxInner: hex(0x0c2112), PlateBG: hex(0x0a1c0f), PlateLine: hex(0x2a7840),
		Button: hex(0x13301c), Border: hex(0x2b7a42), Focus: hex(0x52ff88), Shadow: hex(0x010502),
		HingeEdge: hex(0x020804), Hinge1: hex(0x1f542e), Hinge2: hex(0x12331b),
		Text: hex(0x7dff9d), Sub: hex(0x43a85d), Disabled: hex(0x266336),
		ClockBg1: hex(0x0a1c0f), ClockBg2: hex(0x061209), ClockGrid: hex(0x153b20),
		ClockNum: hex(0x3b9452), ClockDot: hex(0x58c775), ClockHand: hex(0x7dff9d),
		ClockSec: hex(0xb8ffcb), ClockPin: hex(0x9cffb5),
		CalFrame: hex(0x2b7a42), CalBorder: hex(0x164022), CalHeader: hex(0x174524),
		CalHeadSun: hex(0x3d7a2b), CalHeadSat: hex(0x1a6650), CalDay: hex(0x0a1c0f),
		CalSun: hex(0x152b12), CalSat: hex(0x0b261f), CalText: hex(0x7dff9d),
		CalTextSun: hex(0xc4ff7d), CalTextSat: hex(0x7dffe6),
		CalToday: hex(0x6eff94), CalTodayLt: hex(0x1c542b),
	},
	{
		ID: "amber", Name: "Amber CRT", Tagline: "Vintage amber glow",
		Dark: true, DarkChrome: 1.0,
		PaperEven: hex(0x171009), PaperOdd: hex(0x0f0a05), GridLine: hex(0x362413),
		BoxEdge: hex(0x5e3f21), BoxInner: hex(0x21170d), PlateBG: hex(0x1d140b), PlateLine: hex(0x785129),
		Button: hex(0x302214), Border: hex(0x78522b), Focus: hex(0xffae2b), Shadow: hex(0x050301),
		HingeEdge: hex(0x080502), Hinge1: hex(0x54391e), Hinge2: hex(0x362413),
		Text: hex(0xffd48a), Sub: hex(0xba8a4a), Disabled: hex(0x6e502a),
		ClockBg1: hex(0x1d140b), ClockBg2: hex(0x140d07), ClockGrid: hex(0x3b2815),
		ClockNum: hex(0x9c7138), ClockDot: hex(0xcc974d), ClockHand: hex(0xffd48a),
		ClockSec: hex(0xff9c24), ClockPin: hex(0xffe4b3),
		CalFrame: hex(0x78522b), CalBorder: hex(0x3d2a16), CalHeader: hex(0x4a3219),
		CalHeadSun: hex(0x8c3d1e), CalHeadSat: hex(0x6e521e), CalDay: hex(0x1d140b),
		CalSun: hex(0x331a10), CalSat: hex(0x2b2110), CalText: hex(0xffd48a),
		CalTextSun: hex(0xff9c73), CalTextSat: hex(0xffe28a),
		CalToday: hex(0xffb338), CalTodayLt: hex(0x593b18),
	},
}

// curTheme is the active surface theme, mirrored into the package palette vars by applyUITheme.
var curTheme = uiThemes[0]

// Semantic text colours driven by the active theme (Classic: black, dark grey, muted grey).
var (
	cText     = hex(0x000000)
	cSub      = hex(0x494949)
	cDisabled = hex(0x8a8a8a)
)

func uiThemeIndex(id string) int {
	for i, t := range uiThemes {
		if t.ID == id {
			return i
		}
	}
	return 0
}

func uiThemeByIndex(idx int) UITheme {
	if idx < 0 || idx >= len(uiThemes) {
		return uiThemes[0]
	}
	return uiThemes[idx]
}

// applyUITheme updates the global UI palette to match uiThemes[idx].
func applyUITheme(idx int) {
	t := uiThemeByIndex(idx)
	curTheme = t
	cPaper = t.PaperOdd
	cGrid = t.GridLine
	cDark = t.Sub
	cInk = t.Text
	cMuted = t.Disabled
	cFocus = t.Focus
	cBorder = t.Border
	cButton = t.Button
	cGridBG = t.PaperOdd
	cGridLine = t.GridLine
	cDetailsLine = t.BoxEdge
	cPlateLine = t.PlateLine
	cShadow = t.Shadow
	cText = t.Text
	cSub = t.Sub
	cDisabled = t.Disabled

	cCalFrame = t.CalFrame
	cCalBorder = t.CalBorder
	cCalHeader = t.CalHeader
	cCalHeadSun = t.CalHeadSun
	cCalHeadSat = t.CalHeadSat
	cCalDay = t.CalDay
	cCalSun = t.CalSun
	cCalSat = t.CalSat
	cCalTextSun = t.CalTextSun
	cCalTextSat = t.CalTextSat
	cCalToday = t.CalToday
	cCalTodayLt = t.CalTodayLt
}

// neutralInk picks the text/icon colour for a button or tile: the theme's text colour on neutral
// controls, and inkOn(base) on accent-tinted ones.
func neutralInk(base RGB, accent bool) RGB {
	if accent {
		return inkOn(base)
	}
	return cText
}

// ---- Settings > Themes ----

var artThemes = []string{
	"...#####...",
	".##.....##.",
	"#..##.##..#",
	"#..##.##..#",
	"#.........#",
	"#.##...##.#",
	"#.##.....#.",
	"#......##..",
	".##...#....",
	"...###.....",
	"...........",
}

func themesPage(a *App) View {
	return &ThemePick{sel: a.cfg.Theme}
}

// ThemePick lists all 10 UI themes on the left with a live full-screen preview and a mini card on
// the right showing how the theme looks with the user's Favourite colour.
type ThemePick struct {
	sel  int
	top  int
	back func()
}

const themeListRows = 6

func (v *ThemePick) Key(a *App, k cp0.Key) {
	n := len(uiThemes)
	switch {
	case k.Back() || k.Slot() == 4:
		applyUITheme(a.cfg.Theme)
		a.snd.Play("back")
		a.pop()
		if v.back != nil {
			v.back()
		}
	case k.Name == "UP":
		v.sel = (v.sel + n - 1) % n
		applyUITheme(v.sel)
		a.snd.Play("move")
	case k.Name == "DOWN":
		v.sel = (v.sel + 1) % n
		applyUITheme(v.sel)
		a.snd.Play("move")
	case k.Name == "LEFT":
		v.sel = max(0, v.sel-themeListRows)
		applyUITheme(v.sel)
		a.snd.Play("move")
	case k.Name == "RIGHT":
		v.sel = min(n-1, v.sel+themeListRows)
		applyUITheme(v.sel)
		a.snd.Play("move")
	case k.Slot() == 1:
		a.snd.Play("open")
		a.push(&ColorPick{sel: a.cfg.Color, back: func() { applyUITheme(v.sel) }})
	case k.Name == "ENTER" || k.Slot() == 0:
		a.cfg.Theme = v.sel
		applyUITheme(a.cfg.Theme)
		a.save()
		a.snd.Play("select")
		a.notify("Theme: "+uiThemes[v.sel].Name, false)
		a.pop()
		if v.back != nil {
			v.back()
		}
	}
}

func (v *ThemePick) Bar(a *App) []BarItem {
	return []BarItem{{"CHOOSE", artOK, false}, {"COLOUR", artThemes, false}, {}, {}, {"BACK", artBack, false}}
}

func (v *ThemePick) Draw(a *App) {
	applyUITheme(v.sel)
	g := a.g
	pageTitle(a, fmt.Sprintf("THEMES  %d/%d", v.sel+1, len(uiThemes)), artThemes)

	if v.sel < v.top {
		v.top = v.sel
	}
	if v.sel >= v.top+themeListRows {
		v.top = v.sel - themeListRows + 1
	}

	// left: scrollable list of the 10 themes with two-tone colour swatches
	lx, lw := 6, 134
	for i := v.top; i < len(uiThemes) && i < v.top+themeListRows; i++ {
		t := uiThemes[i]
		y := barH + 4 + (i-v.top)*18
		sel := i == v.sel
		base := cButton
		if sel {
			base = a.theme()
		}
		g.dsButton(lx, y, lw, 16, base, false)
		ink := neutralInk(base, sel)

		// mini 2-swatch preview (paper + button)
		g.rect(lx+4, y+3, 12, 10, t.Border)
		g.rect(lx+5, y+4, 5, 8, t.PaperEven)
		g.rect(lx+10, y+4, 5, 8, t.Button)

		nameW := lw - 24
		if i == a.cfg.Theme {
			nameW -= 12
		}
		g.text(lx+19, y+3, fit(t.Name, nameW, 1, false), ink, 1, false)
		if i == a.cfg.Theme {
			g.glyph(lx+lw-9, y+8, artOK, 1, ink)
		}
	}
	// scroll marker for the list
	if len(uiThemes) > themeListRows {
		th := 104 * themeListRows / len(uiThemes)
		ty := barH + 4 + (104-th)*v.top/max(len(uiThemes)-themeListRows, 1)
		g.rect(lx+lw+2, barH+4, 4, 104, cGridBG)
		g.frame(lx+lw+2, barH+4, 4, 104, cBorder)
		g.dsButton(lx+lw+1, ty, 6, th, a.theme(), false)
	}

	// right: preview card showing the theme with the user's Favourite colour
	px, py, pw, ph := 150, barH+4, W-156, 106
	th := uiThemes[v.sel]
	acc := a.theme()
	g.frame(px-1, py-1, pw+2, ph+2, cBorder)
	g.dsBar(px, py, pw, barH, acc)
	g.text(px+5, py+4, fit(th.Name, pw-52, 1, false), inkOn(acc), 1, false)
	mode := "Light"
	if th.Dark {
		mode = "Dark"
	}
	g.textR(px+pw-5, py+4, mode, inkOn(acc), 1, false)
	g.dsGrid(px, py+barH, pw, ph-barH)

	// sample neutral row and accent-selected row + toggle
	g.dsButton(px+8, py+barH+7, pw-16, 15, cButton, false)
	g.text(px+14, py+barH+10, "Normal item", cText, 1, false)
	g.dsButton(px+8, py+barH+26, pw-16, 15, acc, false)
	g.text(px+14, py+barH+29, "Selected item", inkOn(acc), 1, false)
	drawToggle(g, px+pw-36, py+barH+28, true, acc, false)

	// tagline & colour hint
	g.textC(px+pw/2, py+barH+49, fit(th.Tagline, pw-10, 1, false), cText, 1, false)
	g.textC(px+pw/2, py+barH+64, "Colour: "+themes[a.cfg.Color].Name, cSub, 1, false)
	g.textC(px+pw/2, py+barH+76, "5 changes accent colour", cSub, 1, false)
}
