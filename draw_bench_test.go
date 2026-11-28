package main

import (
	"testing"
	"time"
)

// How long a frame takes to draw: go test -run xxx -bench Draw -cpuprofile cpu.prof
// (the Cardputer is roughly 20 times slower than a PC).
func benchDraw(b *testing.B, setup func(a *App)) {
	a := testApp(&testing.T{})
	a.simShots, a.sim = "", true
	a.rescan()
	a.stack = []View{newHome(a)}
	if setup != nil {
		setup(a)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		a.now = time.Now()
		a.t = float64(i) / 30
		a.draw()
	}
}

func BenchmarkDrawHome(b *testing.B)     { benchDraw(b, nil) }
func BenchmarkDrawSettings(b *testing.B) { benchDraw(b, func(a *App) { a.push(newSettings(a)) }) }
func BenchmarkDrawBusy(b *testing.B) {
	benchDraw(b, func(a *App) { a.busyText, a.progress = "Checking for updates...", 0.4 })
}
