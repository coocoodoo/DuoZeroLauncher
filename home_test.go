package main

import (
	"math"
	"testing"
	"time"
)

// On the device the home screen redraws about once a second while idle, so a key press comes
// up to a second after the last frame. The first frame after it must still start the slide and
// the rise from the beginning, not jump to the end (0.6.5 did, so the animation was skipped).
func TestAnimationStartsAfterIdle(t *testing.T) {
	a := &App{apps: []*Entry{{Key: "a"}, {Key: "b"}, {Key: "c"}}}
	h := &Home{last: time.Now()}
	h.ease(a, h.frameDT()) // settled on "a"
	h.lift["a"] = 1
	for _, idle := range []time.Duration{300 * time.Millisecond, 900 * time.Millisecond} {
		h.last = time.Now().Add(-idle)  // the last frame was a clock redraw this long ago
		h.sel, h.slide = (h.sel+1)%3, 1 // what move() does
		dt := h.frameDT()
		h.slide *= math.Exp(-dt * 16)
		h.ease(a, dt)
		key := a.apps[h.sel].Key
		if h.slide < 0.3 || h.lift[key] > 0.5 {
			t.Errorf("after %v idle, the first frame jumped: slide %.2f, new tile lift %.2f", idle, h.slide, h.lift[key])
		}
		for i := 0; i < 30; i++ { // and it still settles
			dt := 1.0 / 30
			h.slide *= math.Exp(-dt * 16)
			h.ease(a, dt)
		}
		if h.lift[key] != 1 {
			t.Errorf("did not settle: lift %.2f", h.lift[key])
		}
	}
}
