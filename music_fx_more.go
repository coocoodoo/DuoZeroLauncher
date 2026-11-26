package main

// Five more visualizers for Music (written for Duo, not in the MP3 Player app):
//   CUBE      a spinning wireframe cube (with a smaller one inside) whose edges fly apart and
//             tumble with the bass and the beats, then snap back together when it's quiet
//   HYPER     a twisting hexagon tunnel rushing at you; each ring lights with its slice of the
//             spectrum, the bass sets the speed
//   HORIZON   a synthwave sunset: a striped sun behind spectrum mountains, a neon floor grid
//             scrolling in and flashing on the beat
//   HELIX     a double helix turning, its rungs filling in with the music; it throws sparks when
//             it "unzips" on a beat
//   FIREWORKS rockets go up on the beats and burst into falling sparks over a spectrum skyline

import "math"

// fxMore is the state the newer visualizers keep.
type fxMore struct {
	ready      bool
	ax, ay, az float32        // the cube's turn
	burst      float32        // how far it has come apart
	edgeDir    [12][3]float32 // which way each edge flies
	edgeSpin   [12]float32
	tunnel     [12]float32 // ring depths, 1 far .. 0 here
	tunRot     float32
	scroll     float32
	phase      float32
	rockets    []fxPart
}

var cubeEdges = func() (e [][2]int) {
	for a := 0; a < 8; a++ {
		for bit := 1; bit < 8; bit <<= 1 {
			if b := a | bit; b != a {
				e = append(e, [2]int{a, b})
			}
		}
	}
	return
}()

func cubeVertex(i int, s float32) [3]float32 {
	v := [3]float32{-s, -s, -s}
	for k := 0; k < 3; k++ {
		if i&(1<<k) != 0 {
			v[k] = s
		}
	}
	return v
}

func (f *musicFx) moreInit() {
	m := &f.more
	if m.ready {
		return
	}
	m.ready = true
	for i, e := range cubeEdges {
		a, b := cubeVertex(e[0], 1), cubeVertex(e[1], 1)
		d := [3]float32{(a[0]+b[0])/2 + (f.rnd()-0.5)*0.6, (a[1]+b[1])/2 + (f.rnd()-0.5)*0.6, (a[2]+b[2])/2 + (f.rnd()-0.5)*0.6}
		n := float32(math.Sqrt(float64(d[0]*d[0] + d[1]*d[1] + d[2]*d[2])))
		m.edgeDir[i] = [3]float32{d[0] / n, d[1] / n, d[2] / n}
		m.edgeSpin[i] = (f.rnd() - 0.5) * 5
	}
	for i := range m.tunnel {
		m.tunnel[i] = float32(i+1) / float32(len(m.tunnel))
	}
}

func fl(v float32) int { return int(math.Floor(float64(v))) }

// lineAll draws a line of any length (line stops at 24 steps, for short strokes).
func (f *musicFx) lineAll(x0, y0, x1, y1 float32, v uint8) {
	n := max(1, min(400, int(max(absf(x1-x0), absf(y1-y0)))))
	for i := 0; i <= n; i++ {
		t := float32(i) / float32(n)
		f.add(fl(x0+(x1-x0)*t), fl(y0+(y1-y0)*t), v)
	}
}

// set puts a value in the grid (to hide what's behind, where add would mix).
func (f *musicFx) set(x, y int, v uint8) {
	if x >= 0 && y >= 0 && x < fxVW && y < fxVH {
		f.heat[y*fxVW+x] = v
	}
}

func bandsEnergy(b *[fxNB]float32) float32 {
	var e float32
	for _, v := range b {
		e += v
	}
	return e / fxNB
}

// sparks moves the free particles: gravity g, drag, and how fast they fade.
func (f *musicFx) sparks(k, g, fade float32) {
	f.moveParts(func(p *fxPart) (int, int, uint8, bool) {
		p.vy += g * k
		p.vx *= float32(math.Pow(0.985, float64(k)))
		p.x += p.vx * k
		p.y += p.vy * k
		p.life -= fade * k
		return fl(p.x), fl(p.y), uint8(clampf(p.life, 0, 1) * 250), p.life > 0 && p.y < fxVH+2
	})
}

// ---- CUBE ----

func (f *musicFx) cube(b *[fxNB]float32, k float32, hit, live bool) {
	m := &f.more
	energy := bandsEnergy(b)
	m.ay += (0.022 + energy*0.07 + f.beat*0.05) * k
	m.ax += (0.014 + energy*0.03) * k
	m.az += 0.005 * k
	target := float32(0)
	if live {
		target = f.bass*0.45 + f.beat*0.85
	}
	if target > m.burst {
		m.burst += (target - m.burst) * 0.45
	} else {
		m.burst += (target - m.burst) * min(1, 0.07*k) // drifts back together
	}
	sx, cx := sinf(m.ax), cosf(m.ax)
	sy, cy := sinf(m.ay), cosf(m.ay)
	sz, cz := sinf(m.az), cosf(m.az)
	turn := func(p [3]float32, flip bool) [3]float32 {
		x, y, z := p[0], p[1], p[2]
		if flip { // the inner cube turns the other way
			x, z = z, x
		}
		y, z = y*cx-z*sx, y*sx+z*cx
		x, z = x*cy+z*sy, -x*sy+z*cy
		x, y = x*cz-y*sz, x*sz+y*cz
		return [3]float32{x, y, z}
	}
	ox, oy := float32(fxVW)/2, float32(fxVH)/2
	project := func(p [3]float32) (float32, float32) {
		z := p[2] + 4
		return ox + p[0]/z*32, oy + p[1]/z*32
	}
	for c, size := range []float32{1, 0.42} {
		burst := m.burst * []float32{1, 0.55}[c]
		bright := uint8(clampf(150+energy*100, 0, 240))
		if c == 1 {
			bright = uint8(clampf(70+b[fxNB/2]*150, 0, 200))
		}
		for i, e := range cubeEdges {
			a, bb := cubeVertex(e[0], size), cubeVertex(e[1], size)
			mid := [3]float32{(a[0] + bb[0]) / 2, (a[1] + bb[1]) / 2, (a[2] + bb[2]) / 2}
			ang := m.edgeSpin[i] * burst
			sa, ca := sinf(ang), cosf(ang)
			piece := func(p [3]float32) [3]float32 {
				dx, dy, dz := p[0]-mid[0], p[1]-mid[1], p[2]-mid[2]
				dx, dy = dx*ca-dy*sa, dx*sa+dy*ca // each edge tumbles on its own
				d := m.edgeDir[i]
				out := burst * 1.1 * size
				return [3]float32{mid[0] + d[0]*out + dx, mid[1] + d[1]*out + dy, mid[2] + d[2]*out + dz}
			}
			x0, y0 := project(turn(piece(a), c == 1))
			x1, y1 := project(turn(piece(bb), c == 1))
			f.lineAll(x0, y0, x1, y1, bright)
		}
	}
	if hit { // sparks from the corners
		for v := 0; v < 8; v++ {
			px, py := project(turn(cubeVertex(v, 1), false))
			for j := 0; j < 3; j++ {
				a := f.rnd() * 2 * math.Pi
				s := 0.4 + f.rnd()*1.1
				f.spawn(px, py, cosf(a)*s, sinf(a)*s*0.6)
			}
		}
	}
	f.sparks(k, 0.01, 0.045)
}

// ---- HYPER ----

func (f *musicFx) hyper(b *[fxNB]float32, k float32, hit, live bool) {
	m := &f.more
	energy := bandsEnergy(b)
	speed := float32(0.0015)
	if live {
		speed = 0.004 + f.bass*0.03 + f.beat*0.03
	}
	m.tunRot += (0.008 + energy*0.04) * k
	cx, cy := float32(fxVW)/2, float32(fxVH)/2
	for i := range m.tunnel {
		z := m.tunnel[i] - speed*k
		if z < 0.06 {
			z += 1
		}
		m.tunnel[i] = z
		r := 4.2 / z
		rot := m.tunRot + z*2.4
		idx := min(fxNB-1, max(0, int((1-z)*fxNB)))
		v := uint8(clampf((45+b[idx]*190)*(1.15-z*0.7), 0, 255))
		var px, py float32
		for s := 0; s <= 6; s++ {
			a := rot + float32(s)*math.Pi/3
			x, y := cx+cosf(a)*r, cy+sinf(a)*r*0.55
			if s > 0 {
				f.lineAll(px, py, x, y, v)
			}
			px, py = x, y
		}
	}
	if hit {
		f.rings[0] = [2]float32{3, 1}
		f.rings = [3][2]float32{f.rings[1], f.rings[2], f.rings[0]}
	}
	f.drawRings(k, 5, 0.09)
	core := 1 + int(f.bass*2+f.beat*2)
	for dy := -core; dy <= core; dy++ {
		for dx := -core * 2; dx <= core*2; dx++ {
			f.add(int(cx)+dx, int(cy)+dy, 70)
		}
	}
}

// ---- HORIZON ----

func (f *musicFx) horizon(b *[fxNB]float32, k float32, hit, live bool) {
	m := &f.more
	hy := 14 // the horizon row
	cx := float32(fxVW) / 2
	if live {
		m.scroll += (0.012 + f.bass*0.05 + f.beat*0.04) * k
	} else {
		m.scroll += 0.003 * k
	}
	m.scroll -= float32(math.Floor(float64(m.scroll)))
	// the sun, striped near the bottom
	r := 9 + f.bass*2 + f.beat
	for y := hy - int(r); y < hy; y++ {
		dy := float32(hy - y)
		if y > hy-7 && (hy-y)%3 == 0 {
			continue
		}
		hw := float32(math.Sqrt(float64(max(0, r*r-dy*dy)))) * 1.25
		v := uint8(clampf(120+dy/r*110, 0, 255))
		for x := fl(cx - hw); x <= fl(cx+hw); x++ {
			f.add(x, y, v)
		}
	}
	// mountains of spectrum, mirrored, in front of the sun
	for x := 0; x < fxVW; x++ {
		d := absf(float32(x)-cx) / cx
		idx := min(fxNB-1, int(d*fxNB))
		h := (1 + b[idx]*9 + 2*sinf(float32(x)*0.27)) * (0.15 + 0.85*d) // a valley in the middle: the sun shows
		top := hy - fl(h)
		for y := top + 1; y < hy; y++ {
			f.set(x, y, 26)
		}
		f.add(x, top, 200)
	}
	for x := 0; x < fxVW; x++ {
		f.add(x, hy, 230)
	}
	// the floor: lines coming at you, and lines to the vanishing point
	flash := uint8(f.beat * 70)
	depth := float32(fxVH - hy - 1)
	for i := 0; i < 7; i++ {
		d := float32(math.Mod(float64(float32(i)/7+m.scroll), 1))
		y := hy + 1 + fl(depth*d*d)
		v := uint8(clampf(25+d*120, 0, 200)) + flash/2
		for x := 0; x < fxVW; x++ {
			f.add(x, y, v)
		}
	}
	for j := -9; j <= 9; j++ {
		f.lineAll(cx+float32(j)*2.2, float32(hy+1), cx+float32(j)*19, fxVH-1, 55+flash)
	}
}

// ---- HELIX ----

func (f *musicFx) helix(b *[fxNB]float32, k float32, hit, live bool) {
	m := &f.more
	energy := bandsEnergy(b)
	m.phase += (0.035 + energy*0.12) * k
	mid := float32(fxVH) / 2
	amp := 9 + f.bass*5 + f.beat*3
	for x := 0; x < fxVW; x++ {
		a := float32(x)*0.11 + m.phase
		s, c := sinf(a), cosf(a)
		y1, y2 := mid+s*amp, mid-s*amp
		v1 := uint8(clampf(115+c*95, 20, 230))
		v2 := uint8(clampf(115-c*95, 20, 230))
		f.add(x, fl(y1), v1)
		f.add(x, fl(y1)+1, v1/3)
		f.add(x, fl(y2), v2)
		f.add(x, fl(y2)+1, v2/3)
		if x%6 == 3 { // a rung, filled in from both strands as loud as its band
			e := b[min(fxNB-1, x*fxNB/fxVW)]
			if !live {
				e = 0.15
			}
			lo, hi := min(y1, y2), max(y1, y2)
			n := int(hi - lo)
			for t := 1; t < n; t++ {
				u := float32(t) / float32(n)
				if u < e/2+0.08 || u > 1-e/2-0.08 {
					f.add(x, fl(lo)+t, uint8(40+e*170))
				}
			}
			if hit && f.rnd() < 0.5 {
				f.spawn(float32(x), y1, (f.rnd()-0.5)*1.2, -0.3-f.rnd()*0.8)
				f.spawn(float32(x), y2, (f.rnd()-0.5)*1.2, 0.3+f.rnd()*0.8)
			}
		}
	}
	f.sparks(k, 0, 0.05)
}

// ---- FIREWORKS ----

func (f *musicFx) fireworks(b *[fxNB]float32, k float32, hit, live bool) {
	m := &f.more
	energy := bandsEnergy(b)
	if live && len(m.rockets) < 6 && (hit || f.rnd() < energy*0.05*k) {
		m.rockets = append(m.rockets, fxPart{x: 14 + f.rnd()*(fxVW-28), y: fxVH - 1,
			vx: (f.rnd() - 0.5) * 0.5, vy: -(0.85 + f.rnd()*0.35 + f.bass*0.35), life: 1})
	}
	for i := 0; i < len(m.rockets); {
		r := &m.rockets[i]
		r.vy += 0.03 * k
		r.x += r.vx * k
		r.y += r.vy * k
		f.add(fl(r.x), fl(r.y), 220)
		f.add(fl(r.x), fl(r.y)+1, 90)
		if r.vy >= -0.08 || r.y < 3 { // the top: burst
			n := 24 + int(energy*30)
			for j := 0; j < n; j++ {
				a := f.rnd() * 2 * math.Pi
				s := 0.35 + f.rnd()*0.9
				f.spawn(r.x, r.y, cosf(a)*s*1.4, sinf(a)*s*0.8)
			}
			m.rockets[i] = m.rockets[len(m.rockets)-1]
			m.rockets = m.rockets[:len(m.rockets)-1]
			continue
		}
		i++
	}
	f.moveParts(func(p *fxPart) (int, int, uint8, bool) {
		p.vy += 0.025 * k
		p.vx *= float32(math.Pow(0.97, float64(k)))
		p.x += p.vx * k
		p.y += p.vy * k
		p.life -= 0.02 * k
		v := clampf(p.life, 0, 1) * 250
		if p.life < 0.35 && f.rnd() < 0.3 { // crackle
			v = 255
		}
		return fl(p.x), fl(p.y), uint8(v), p.life > 0 && p.y < fxVH
	})
	// a skyline of the spectrum along the bottom
	for i := 0; i < fxNB; i++ {
		h := 1 + int(b[i]*5)
		for dx := 0; dx < 3; dx++ {
			for r := 0; r < h; r++ {
				f.set(2+i*4+dx, fxVH-1-r, 34)
			}
		}
	}
}
