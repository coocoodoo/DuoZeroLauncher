package main

import (
	"dslauncher/cp0"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"
)

// The battery colour follows the level: green above 50%, yellow 21-50%, red 20% or less.
func TestBatteryColours(t *testing.T) {
	scr := &cp0.Screen{Px: make([]uint16, W*H)}
	g := newG(scr)
	bright := func(pal map[byte]RGB) RGB {
		for i := range scr.Px {
			scr.Px[i] = 0
		}
		drawDSBattery(g, 0, 0, pal)
		return from565(scr.Px[3*W+5]) // a 'B' pixel, the bright middle
	}
	if c := bright(batGreen); !(c.G > 200 && c.R < 100) {
		t.Fatalf("green battery is %v", c)
	}
	if c := bright(batYellow); !(c.R > 200 && c.G > 200 && c.B < 100) {
		t.Fatalf("yellow battery is %v", c)
	}
	if c := bright(batRed); !(c.R > 200 && c.G < 100) {
		t.Fatalf("red battery is %v", c)
	}

	out := os.Getenv("PREVIEW_DIR")
	if out == "" {
		return
	}
	for i := range scr.Px {
		scr.Px[i] = hex(0x30baf3).c565()
	}
	for i, pal := range []map[byte]RGB{batGreen, batYellow, batRed, batGrey} {
		drawDSBattery(g, 4+i*18, 4, pal)
	}
	const z = 10
	img := image.NewRGBA(image.Rect(0, 0, 80*z, 16*z))
	for y := 0; y < 16*z; y++ {
		for x := 0; x < 80*z; x++ {
			c := from565(scr.Px[(y/z)*W+x/z])
			img.SetRGBA(x, y, color.RGBA{c.R, c.G, c.B, 255})
		}
	}
	f, err := os.Create(filepath.Join(out, "batteries.png"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	png.Encode(f, img)
}
