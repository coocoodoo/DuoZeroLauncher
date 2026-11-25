package main

// Screenshots: Fn+J (the PrtSc legend on the Cardputer keyboard) saves the screen as a PNG in
// ~/Screenshots. In Duo it saves the frame on show, with a white flash; while another app runs,
// Duo still watches for the key (and only that key) and grabs the framebuffer itself.

import (
	"dslauncher/cp0"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"sync"
	"time"
)

func isScreenshotKey(k cp0.Key) bool { return k.Name == "PRINT" && !k.Repeat && !k.Released }

func screenshotDir() string {
	if d := os.Getenv("DSL_SCREENSHOTS"); d != "" {
		return d
	}
	return filepath.Join(homeDir(), "Screenshots")
}

// saveScreenshot writes a 320x170 RGB565 frame as a PNG and returns its path.
func saveScreenshot(px []uint16) (string, error) {
	dir := screenshotDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	img := image.NewRGBA(image.Rect(0, 0, W, H))
	for y := 0; y < H; y++ {
		for x := 0; x < W; x++ {
			c := from565(px[y*W+x])
			img.SetRGBA(x, y, color.RGBA{c.R, c.G, c.B, 255})
		}
	}
	name := "duo-" + time.Now().Format("20060102-150405")
	path := filepath.Join(dir, name+".png")
	for i := 2; exists(path); i++ { // two in the same second
		path = filepath.Join(dir, fmt.Sprintf("%s-%d.png", name, i))
	}
	f, err := os.Create(path)
	if err != nil {
		return "", err
	}
	if err := png.Encode(f, img); err != nil {
		f.Close()
		return "", err
	}
	return path, f.Close()
}

// screenshot saves what Duo is showing, flashes the screen white and says where it went.
func (a *App) screenshot() {
	px := append([]uint16(nil), a.scr.Px...)
	path, err := saveScreenshot(px)
	if err != nil {
		a.notify("Screenshot failed: "+briefLine(err.Error()), true)
		return
	}
	a.snd.Play("modal")
	a.flashUntil = time.Now().Add(120 * time.Millisecond)
	a.notify("Saved Screenshots/"+filepath.Base(path), false)
}

// watchScreenshots runs while another app has the screen: it reads only the keyboard events Duo
// would have dropped anyway, and on Fn+J copies the framebuffer to a PNG. Stop it with the
// returned function before Duo reads keys again.
func (a *App) watchScreenshots() (stop func()) {
	quit := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-quit:
				return
			default:
			}
			for _, k := range a.keys.Poll(100) {
				if !isScreenshotKey(k) {
					continue
				}
				px, err := a.scr.Capture()
				if err != nil {
					continue
				}
				if _, err := saveScreenshot(px); err == nil {
					a.snd.Play("modal")
				}
			}
		}
	}()
	return func() { close(quit); wg.Wait() }
}
