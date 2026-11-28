package main

import (
	"testing"

	"dslauncher/cp0"
)

// draw9Plain is the shaded texture worked out pixel by pixel, as draw9 did before it kept the
// rendered blocks: what the cache must reproduce exactly.
func (g *G) draw9Plain(t *Tex, x, y, w, h, s int, base RGB, tileX bool) {
	src := shadeTex(t, s, curTheme.DarkChrome)
	for dy := 0; dy < h; dy++ {
		y0, y1, f := sliceY(dy, h, t.H, s)
		for dx := 0; dx < w; dx++ {
			sx := mapSlice(dx, w, t.W, s, tileX)
			i := (y0*t.W + sx) * 4
			p := src.Pix
			c := RGB{p[i], p[i+1], p[i+2]}
			a := float64(p[i+3]) / 255
			if f > 0 {
				j := (y1*t.W + sx) * 4
				c = mix(c, RGB{p[j], p[j+1], p[j+2]}, f)
				a = (a*(1-f) + float64(p[j+3])/255*f)
			}
			hl := RGB{hardLight(base.R, c.R), hardLight(base.G, c.G), hardLight(base.B, c.B)}
			g.pxSmooth(x+dx, y+dy, mix(base, hl, a))
		}
	}
}

// The bars, buttons and tiles come out of the cache exactly as they were drawn before: every
// texture, at sizes and places that move the dither pattern, clipped, in light and dark themes,
// the first time and from the cache.
func TestShadedTextureCache(t *testing.T) {
	defer applyUITheme(0)
	type shape struct {
		name       string
		s          int
		tile       bool
		x, y, w, h int
	}
	shapes := []shape{
		{"button.png", 2, false, 11, 7, 60, 22}, {"button_lg.png", 2, false, 3, 90, 200, 31},
		{"button_square.png", 2, false, 101, 33, 50, 50}, {"button_square_active.png", 2, false, 34, 142, 28, 20},
		{"top_bar.png", 1, true, 0, 0, W, 15}, {"bottom_bar_bottom.png", 1, true, 0, 133, W, 37},
		{"bottom_bar_top.png", 1, true, 5, 61, 130, 30}, {"info.png", 2, false, 40, 51, 240, 46},
		{"info_square.png", 3, false, 42, 53, 41, 41}, {"button.png", 2, false, -9, -5, 40, 20},
		{"button_square.png", 2, false, 300, 160, 40, 30}, {"button.png", 2, false, 20, 20, 3, 3},
	}
	for _, theme := range []int{0, 1, 5} {
		applyUITheme(theme)
		for _, base := range []RGB{cButton, hex(0xfb0018), hex(0x000092), hex(0xf3e300)} {
			for _, sh := range shapes {
				for pass := 0; pass < 2; pass++ {
					got, want := newG(&cp0.Screen{Px: make([]uint16, W*H)}), newG(&cp0.Screen{Px: make([]uint16, W*H)})
					if pass == 1 { // clipped, and from the cache
						got.setClip(sh.x+3, sh.y+2, sh.w/2, sh.h-3)
						want.setClip(sh.x+3, sh.y+2, sh.w/2, sh.h-3)
					}
					tx := tex(sh.name)
					got.draw9(tx, sh.x, sh.y, sh.w, sh.h, sh.s, &base, sh.tile, false)
					want.draw9Plain(tx, sh.x, sh.y, sh.w, sh.h, sh.s, base, sh.tile)
					for i := range want.s.Px {
						if got.s.Px[i] != want.s.Px[i] {
							t.Fatalf("theme %d %s %v pass %d: pixel (%d,%d) is %04x, want %04x", theme, sh.name, base, pass,
								i%W, i/W, got.s.Px[i], want.s.Px[i])
						}
					}
				}
			}
		}
	}
	// it stays bounded however many colours an animation goes through
	tx := tex("button_square.png")
	g := newG(&cp0.Screen{Px: make([]uint16, W*H)})
	for i := 0; i < 2000; i++ {
		base := RGB{uint8(i), uint8(i >> 8), 7}
		g.draw9(tx, 10, 10, 50, 50, 2, &base, false, false)
	}
	if ninePixels > nineCacheMax {
		t.Fatalf("the cache holds %d pixels, over its limit of %d", ninePixels, nineCacheMax)
	}
}

// shade with its lookup tables gives the pixels blending one by one gave.
func TestShadeMatchesBlend(t *testing.T) {
	for _, a := range []float64{0.12, 0.25, 0.5, 0.7, 0.999} {
		for _, c := range []RGB{cBlack, cWhite, hex(0xe3f300), hex(0x3e5c78)} {
			got, want := newG(&cp0.Screen{Px: make([]uint16, W*H)}), newG(&cp0.Screen{Px: make([]uint16, W*H)})
			for i := range got.s.Px {
				v := uint16(i*2654435761>>7) ^ uint16(i)
				got.s.Px[i], want.s.Px[i] = v, v
			}
			got.setClip(5, 3, 300, 150)
			want.setClip(5, 3, 300, 150)
			got.shade(-4, -4, W, H, c, a)
			for y := -4; y < H-4; y++ {
				for x := -4; x < W-4; x++ {
					want.blend(x, y, c, a)
				}
			}
			for i := range want.s.Px {
				if got.s.Px[i] != want.s.Px[i] {
					t.Fatalf("shade %v %.3f: pixel %d is %04x, want %04x", c, a, i, got.s.Px[i], want.s.Px[i])
				}
			}
		}
	}
}
