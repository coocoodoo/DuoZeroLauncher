package main

// Music's visualizers, ported from the MP3 Player app (fx.rs). Everything lives in a
// half-resolution "heat" grid that fades each frame (that fade makes the trails), then goes
// through a colour ramp to chunky 2x2 pixels with a darker second row, like a CRT.

import "math"

const (
	fxNB       = 38  // spectrum bands
	fxVW       = 157 // heat grid, drawn 2x: 314 x 72
	fxVH       = 36
	fxScopeW   = fxVW
	fxMaxParts = 240
	fxStars    = 90
	fxRamp     = 64
)

var fxModes = []string{"EMBERS", "NEBULA", "WARP", "RAIN", "PHOSPHOR", "CUBE", "HYPER", "HORIZON", "HELIX", "FIREWORKS"}

const fxCount = 10

type fxPart struct{ x, y, vx, vy, life float32 }

type musicFx struct {
	heat     []uint8
	parts    []fxPart
	seed     uint32
	t        float32
	avg      float32
	bass     float32
	beat     float32
	cool     float32
	peaks    [fxNB]float32
	drops    [fxVW / 2]float32
	stars    [][3]float32
	rings    [3][2]float32
	lastMode int
	pal      [fxCount][fxRamp]uint16
	dim      [fxCount][fxRamp]uint16
	fade     [fxCount][256]uint8
	more     fxMore
}

func rgb565(r, g, b uint8) uint16 { return uint16(r>>3)<<11 | uint16(g>>2)<<5 | uint16(b>>3) }

func lerp565(a, b uint16, t float32) uint16 {
	ti := int32(clampf(t, 0, 1) * 256)
	mixc := func(x, y int32) int32 { return x + (y-x)*ti/256 }
	r := mixc(int32(a>>11), int32(b>>11))
	g := mixc(int32(a>>5&63), int32(b>>5&63))
	bl := mixc(int32(a&31), int32(b&31))
	return uint16(r)<<11 | uint16(g)<<5 | uint16(bl)
}

func clampf(v, lo, hi float32) float32 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// fxAccent is the colour each visual uses for its frame corners.
func fxAccent(mode int) RGB {
	return []RGB{{255, 150, 50}, {200, 110, 255}, {110, 200, 255}, {70, 255, 150}, {255, 210, 90},
		{60, 230, 255}, {255, 60, 160}, {255, 80, 200}, {90, 230, 170}, {255, 200, 80}}[mode%fxCount]
}

func fxRampOf(stops [][3]uint8) (out [fxRamp]uint16) {
	for i := range out {
		f := float32(i) / float32(fxRamp-1) * float32(len(stops)-1)
		k := min(int(f), len(stops)-2)
		t := f - float32(k)
		a, b := stops[k], stops[k+1]
		m := func(x, y uint8) uint8 { return uint8(float32(x) + (float32(y)-float32(x))*t) }
		out[i] = rgb565(m(a[0], b[0]), m(a[1], b[1]), m(a[2], b[2]))
	}
	return
}

func newMusicFx() *musicFx {
	bg := [3]uint8{10, 8, 20}
	f := &musicFx{heat: make([]uint8, fxVW*fxVH), seed: 0x2f6e2b1d, avg: 0.1, lastMode: -1}
	f.pal = [fxCount][fxRamp]uint16{
		fxRampOf([][3]uint8{bg, {60, 10, 40}, {170, 30, 40}, {255, 110, 30}, {255, 200, 70}, {255, 250, 220}}),
		fxRampOf([][3]uint8{bg, {36, 16, 90}, {110, 40, 190}, {240, 70, 180}, {130, 220, 255}, {250, 250, 255}}),
		fxRampOf([][3]uint8{bg, {12, 30, 90}, {30, 90, 200}, {80, 200, 255}, {200, 240, 255}, {255, 255, 255}}),
		fxRampOf([][3]uint8{bg, {6, 40, 34}, {10, 110, 70}, {30, 210, 110}, {140, 255, 170}, {240, 255, 240}}),
		fxRampOf([][3]uint8{bg, {70, 40, 10}, {190, 110, 20}, {255, 190, 60}, {255, 235, 150}, {255, 255, 240}}),
		fxRampOf([][3]uint8{bg, {0, 40, 60}, {0, 130, 170}, {60, 225, 255}, {200, 250, 255}, {255, 255, 255}}),  // CUBE
		fxRampOf([][3]uint8{bg, {50, 0, 60}, {150, 20, 140}, {255, 60, 120}, {255, 170, 90}, {255, 240, 200}}),  // HYPER
		fxRampOf([][3]uint8{bg, {40, 10, 70}, {120, 30, 160}, {255, 60, 170}, {255, 180, 80}, {255, 250, 200}}), // HORIZON
		fxRampOf([][3]uint8{bg, {10, 40, 30}, {30, 120, 90}, {90, 220, 170}, {220, 255, 150}, {255, 255, 230}}), // HELIX
		fxRampOf([][3]uint8{bg, {60, 20, 10}, {200, 60, 30}, {255, 160, 40}, {255, 230, 120}, {255, 255, 255}}), // FIREWORKS
	}
	for m := range f.pal {
		for i, c := range f.pal[m] {
			f.dim[m][i] = lerp565(c, rgb565(10, 8, 20), 0.42) // the dark half of each scanline
		}
	}
	for m, keep := range []float32{0.80, 0.87, 0.70, 0.84, 0.74, 0.66, 0.70, 0.34, 0.74, 0.86} {
		for v := 0; v < 256; v++ {
			f.fade[m][v] = uint8(max(0, float32(v)*keep-1))
		}
	}
	for i := range f.drops {
		f.drops[i] = -f.rnd() * 60
	}
	for i := 0; i < fxStars; i++ {
		s := f.newStar()
		s[2] = 0.1 + f.rnd()*0.9
		f.stars = append(f.stars, s)
	}
	return f
}

func (f *musicFx) rnd() float32 {
	x := f.seed
	x ^= x << 13
	x ^= x >> 17
	x ^= x << 5
	f.seed = x
	return float32(x&0xffff) / 65536
}

func (f *musicFx) newStar() [3]float32 {
	a := f.rnd() * 2 * math.Pi
	r := 8 + f.rnd()*60
	return [3]float32{float32(math.Cos(float64(a))) * r, float32(math.Sin(float64(a))) * r, 1}
}

func (f *musicFx) add(x, y int, v uint8) {
	if x >= 0 && y >= 0 && x < fxVW && y < fxVH {
		c := &f.heat[y*fxVW+x]
		*c = uint8(min(255, int(*c)+int(v)))
	}
}

func (f *musicFx) line(x0, y0, x1, y1 int, v uint8) {
	n := max(abs(x1-x0), abs(y1-y0))
	n = max(1, min(n, 24))
	for i := 0; i <= n; i++ {
		f.add(x0+(x1-x0)*i/n, y0+(y1-y0)*i/n, v)
	}
}

func (f *musicFx) spawn(x, y, vx, vy float32) {
	if len(f.parts) < fxMaxParts {
		f.parts = append(f.parts, fxPart{x, y, vx, vy, 1})
	}
}

// step runs one frame; dt is seconds, so the motion keeps its speed when a frame runs late.
func (f *musicFx) step(mode int, raw *[fxNB]float32, scope *[fxScopeW]float32, dt float32, live bool) {
	k := clampf(dt*30, 0.3, 2.5)
	f.t += k
	if mode != f.lastMode {
		f.lastMode = mode
		f.parts = f.parts[:0]
		f.more.rockets = f.more.rockets[:0]
		clear(f.heat)
		f.moreInit()
	}
	fade := &f.fade[mode]
	for i, c := range f.heat {
		f.heat[i] = fade[c]
	}
	// loudness-independent bands, and a beat from the bass
	var top float32
	for _, v := range raw {
		top = max(top, v)
	}
	gain := float32(1)
	if top > 0.15 {
		gain = 1 / top
	}
	var b [fxNB]float32
	if live {
		for i := range b {
			b[i] = clampf(raw[i]*gain, 0, 1)
		}
	}
	bass := (b[0] + b[1] + b[2] + b[3] + b[4]) / 5
	f.bass += (bass - f.bass) * 0.5
	f.cool -= k
	f.beat *= float32(math.Pow(0.86, float64(k)))
	hit := live && f.cool <= 0 && bass > f.avg*1.28 && bass > 0.3
	if hit {
		f.beat, f.cool = 1, 7
	}
	f.avg += (bass - f.avg) * 0.05 * k
	switch mode {
	case 0:
		f.embers(&b, k, hit)
	case 1:
		f.nebula(&b, k, hit, live)
	case 2:
		f.warp(k, hit, live)
	case 3:
		f.rain(&b, k, hit, live)
	case 4:
		f.phosphor(scope, k, hit, live)
	case 5:
		f.cube(&b, k, hit, live)
	case 6:
		f.hyper(&b, k, hit, live)
	case 7:
		f.horizon(&b, k, hit, live)
	case 8:
		f.helix(&b, k, hit, live)
	default:
		f.fireworks(&b, k, hit, live)
	}
}

func sinf(v float32) float32 { return float32(math.Sin(float64(v))) }
func cosf(v float32) float32 { return float32(math.Cos(float64(v))) }

// moveParts moves each particle with fn and drops it when fn says so.
func (f *musicFx) moveParts(fn func(p *fxPart) (x, y int, v uint8, keep bool)) {
	for i := 0; i < len(f.parts); {
		x, y, v, keep := fn(&f.parts[i])
		if !keep {
			f.parts[i] = f.parts[len(f.parts)-1]
			f.parts = f.parts[:len(f.parts)-1]
			continue
		}
		f.add(x, y, v)
		i++
	}
}

func (f *musicFx) embers(b *[fxNB]float32, k float32, hit bool) {
	floor := fxVH - 1
	for i := 0; i < fxNB; i++ {
		x := 2 + i*4
		h := int(b[i] * (fxVH - 9))
		f.peaks[i] = max(f.peaks[i]-0.018*k, b[i])
		for r := 0; r < h; r += 2 {
			v := uint8(50 + r*150/fxVH)
			for dx := 0; dx < 3; dx++ {
				f.add(x+dx, floor-r, v)
			}
		}
		py := floor - int(f.peaks[i]*(fxVH-9)) - 1
		if f.peaks[i] > 0.04 {
			for dx := 0; dx < 3; dx++ {
				f.add(x+dx, py, 235)
			}
		}
		if b[i] > 0.2 && f.rnd() < b[i]*0.42*k {
			jx, jv := f.rnd(), f.rnd()
			f.spawn(float32(x)+jx*3, float32(floor-h), (jv-0.5)*0.7, -(0.35 + b[i] + jx*0.3))
		}
	}
	if hit {
		for i := 0; i < 22; i++ {
			x, a, s := f.rnd()*fxVW, f.rnd()-0.5, 0.8+f.rnd()*1.4
			f.spawn(x, float32(floor)-2, a*1.8, -s)
		}
	}
	t := f.t
	f.moveParts(func(p *fxPart) (int, int, uint8, bool) {
		p.vx += sinf(p.y*0.22+t*0.09) * 0.025 * k
		p.x += p.vx * k
		p.y += p.vy * k
		p.vy *= 0.995
		p.life -= 0.024 * k
		return int(p.x), int(p.y), uint8(p.life * 250), p.life > 0 && p.y >= 0
	})
}

func (f *musicFx) nebula(b *[fxNB]float32, k float32, hit, live bool) {
	cx, cy := float32(fxVW)/2, float32(fxVH)/2
	var energy float32
	for _, v := range b {
		energy += v
	}
	energy /= fxNB
	if live {
		n := int((1 + energy*9) * k)
		for j := 0; j < n; j++ {
			arm := float32(j%3) * 2 * math.Pi / 3
			a := f.t*0.055 + arm + f.rnd()*0.5
			s := 0.55 + energy*1.3 + f.rnd()*0.3
			f.spawn(cosf(a)*3, sinf(a)*3, cosf(a)*s, sinf(a)*s)
		}
	}
	if hit {
		for i := 0; i < 34; i++ {
			a := f.rnd() * 2 * math.Pi
			s := 1.3 + f.rnd()*1.6
			f.spawn(cosf(a)*4, sinf(a)*4, cosf(a)*s, sinf(a)*s)
		}
	}
	sn, cs := sinf(0.055*k), cosf(0.055*k)
	drag := float32(math.Pow(0.992, float64(k)))
	f.moveParts(func(p *fxPart) (int, int, uint8, bool) {
		vx, vy := p.vx*cs-p.vy*sn, p.vx*sn+p.vy*cs
		p.vx, p.vy = vx*drag, vy*drag
		p.x += p.vx * k
		p.y += p.vy * k
		p.life -= 0.0135 * k
		sx, sy := cx+p.x, cy+p.y*0.52
		keep := p.life > 0 && sx >= 0 && sx < fxVW && sy >= 0 && sy < fxVH
		return int(sx), int(sy), uint8(p.life*200 + 40), keep
	})
	r := 1 + int(f.bass*3+f.beat*3)
	for dy := -r; dy <= r; dy++ {
		for dx := -r * 2; dx <= r*2; dx++ {
			if dx*dx+dy*dy*4 <= r*r*4 {
				f.add(int(cx)+dx, int(cy)+dy, 90)
			}
		}
	}
}

func (f *musicFx) warp(k float32, hit, live bool) {
	cx, cy := fxVW/2, fxVH/2
	speed := float32(0.0015)
	if live {
		speed = 0.006 + f.bass*0.035 + f.beat*0.03
	}
	for i := range f.stars {
		x, y, z := f.stars[i][0], f.stars[i][1], f.stars[i][2]
		nz := z - speed*k
		ax, ay := x/z*0.5, y/z*0.27
		bx, by := x/max(nz, 0.02)*0.5, y/max(nz, 0.02)*0.27
		if nz <= 0.03 || absf(bx) > fxVW/2 || absf(by) > fxVH/2 {
			f.stars[i] = f.newStar()
			continue
		}
		f.stars[i][2] = nz
		f.line(cx+int(ax), cy+int(ay), cx+int(bx), cy+int(by), uint8((1-nz)*215+40))
	}
	if hit {
		f.rings[0] = [2]float32{3, 1}
		f.rings = [3][2]float32{f.rings[1], f.rings[2], f.rings[0]}
	}
	f.drawRings(k, 4, 0.08)
}

func absf(v float32) float32 {
	if v < 0 {
		return -v
	}
	return v
}

func (f *musicFx) rain(b *[fxNB]float32, k float32, hit, live bool) {
	n := fxVW / 2
	for i := 0; i < n; i++ {
		e := b[i*fxNB/n]
		was := f.drops[i]
		sp := float32(0.08)
		if live {
			sp = 0.22 + e*1.45
		}
		y := was + sp*k
		f.drops[i] = y
		if y >= 0 {
			x := i*2 + 1
			f.add(x, int(y), uint8(150+e*105))
			if int(was) != int(y) {
				f.add(x, int(y)-1, 80)
			}
		}
		if y >= fxVH {
			if e > 0.42 {
				a, s := f.rnd()-0.5, 0.4+e*0.7
				f.spawn(float32(i)*2+1, fxVH-2, a*1.3, -s)
			}
			f.drops[i] = -f.rnd() * (34 - e*28)
		}
	}
	if hit {
		for j := 0; j < 12; j++ {
			f.drops[int(f.rnd()*float32(n))%n] = 0
		}
	}
	f.moveParts(func(p *fxPart) (int, int, uint8, bool) {
		p.vy += 0.07 * k
		p.x += p.vx * k
		p.y += p.vy * k
		p.life -= 0.05 * k
		return int(p.x), int(p.y), uint8(p.life * 220), p.life > 0 && p.y < fxVH
	})
}

func (f *musicFx) phosphor(s *[fxScopeW]float32, k float32, hit, live bool) {
	mid := float32(fxVH) / 2
	amp := mid - 2
	peak := float32(0.03)
	for _, v := range s {
		peak = max(peak, absf(v))
	}
	g := float32(0)
	if live {
		g = min(0.95/peak, 10)
	}
	prev := int(mid)
	for x := 0; x < fxVW; x++ {
		v := clampf(s[x]*g, -1, 1)
		y := int(mid - v*amp)
		if x > 0 {
			f.line(x-1, prev, x, y, 190)
		}
		if absf(v) > 0.72 && f.rnd() < 0.12*k {
			a, b2 := f.rnd()-0.5, f.rnd()
			f.spawn(float32(x), float32(y), a*1.2, -v*(0.5+b2))
		}
		prev = y
	}
	if hit {
		f.rings[0] = [2]float32{2, 1}
		f.rings = [3][2]float32{f.rings[1], f.rings[2], f.rings[0]}
	}
	f.drawRings(k, 3.2, 0.055)
	f.moveParts(func(p *fxPart) (int, int, uint8, bool) {
		p.x += p.vx * k
		p.y += p.vy * k
		p.life -= 0.045 * k
		return int(p.x), int(p.y), uint8(p.life * 240), p.life > 0
	})
}

func (f *musicFx) drawRings(k, grow, fade float32) {
	cx, cy := float32(fxVW)/2, float32(fxVH)/2
	for j := range f.rings {
		r, l := f.rings[j][0], f.rings[j][1]
		if l <= 0 {
			continue
		}
		n := max(16, min(int(r*2.4), 110))
		v := uint8(l * 150)
		for i := 0; i < n; i++ {
			a := float32(i) / float32(n) * 2 * math.Pi
			f.add(int(cx+cosf(a)*r), int(cy+sinf(a)*r*0.5), v)
		}
		f.rings[j] = [2]float32{r + grow*k, l - fade*k}
	}
}

// blit draws the grid as 2x2 pixels at (ox, oy) of a W-wide RGB565 screen.
func (f *musicFx) blit(px []uint16, ox, oy, mode int) {
	pal, dim := &f.pal[mode], &f.dim[mode]
	for y := 0; y < fxVH; y++ {
		row := f.heat[y*fxVW : (y+1)*fxVW]
		a := px[(oy+y*2)*W : (oy+y*2+1)*W]
		b := px[(oy+y*2+1)*W : (oy+y*2+2)*W]
		for x, h := range row {
			c, d := pal[h>>2], dim[h>>2]
			sx := ox + x*2
			a[sx], a[sx+1] = c, c
			b[sx], b[sx+1] = d, d
		}
	}
}
