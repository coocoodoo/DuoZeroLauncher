package main

import (
	"bytes"
	"dslauncher/cp0"
	"encoding/binary"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func solid(w, h int, f func(x, y int) color.RGBA) *image.RGBA {
	m := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			m.SetRGBA(x, y, f(x, y))
		}
	}
	return m
}

// jpegWithExif encodes main as a JPEG carrying an EXIF block: an orientation and a thumbnail.
func jpegWithExif(t *testing.T, main, thumb image.Image, orientation int) []byte {
	t.Helper()
	var mb, tb bytes.Buffer
	jpeg.Encode(&mb, main, &jpeg.Options{Quality: 90})
	jpeg.Encode(&tb, thumb, &jpeg.Options{Quality: 90})
	// TIFF: header, IFD0 (orientation), IFD1 (the thumbnail's offset and length), the thumbnail
	le := binary.LittleEndian
	tiff := []byte("II*\x00\x08\x00\x00\x00")
	entry := func(tag, typ uint16, count, val uint32) []byte {
		b := make([]byte, 12)
		le.PutUint16(b, tag)
		le.PutUint16(b[2:], typ)
		le.PutUint32(b[4:], count)
		le.PutUint32(b[8:], val)
		return b
	}
	ifd1 := 8 + 2 + 12 + 4
	thumbAt := ifd1 + 2 + 2*12 + 4
	tiff = append(tiff, 1, 0)
	tiff = append(tiff, entry(0x0112, 3, 1, uint32(orientation))...)
	tiff = binary.LittleEndian.AppendUint32(tiff, uint32(ifd1))
	tiff = append(tiff, 2, 0)
	tiff = append(tiff, entry(0x0201, 4, 1, uint32(thumbAt))...)
	tiff = append(tiff, entry(0x0202, 4, 1, uint32(tb.Len()))...)
	tiff = binary.LittleEndian.AppendUint32(tiff, 0)
	tiff = append(tiff, tb.Bytes()...)
	app1 := append([]byte("Exif\x00\x00"), tiff...)
	seg := []byte{0xff, 0xe1, byte((len(app1) + 2) >> 8), byte(len(app1) + 2)}
	m := mb.Bytes()
	return append(append(append([]byte{}, m[:2]...), append(seg, app1...)...), m[2:]...)
}

var (
	red   = color.RGBA{220, 30, 30, 255}
	blue  = color.RGBA{30, 30, 220, 255}
	green = color.RGBA{30, 200, 60, 255}
)

func near(c, want color.RGBA) bool {
	d := func(a, b uint8) int { return max(int(a)-int(b), int(b)-int(a)) }
	return d(c.R, want.R) < 40 && d(c.G, want.G) < 40 && d(c.B, want.B) < 40
}

func TestPhotoExifAndThumbs(t *testing.T) {
	dir := t.TempDir()
	setHome(t, dir)
	// a landscape photo, red on the left and blue on the right, taken with the camera turned:
	// EXIF orientation 6 means "turn it a quarter clockwise to see it upright"
	main := solid(80, 40, func(x, y int) color.RGBA {
		if x < 40 {
			return red
		}
		return blue
	})
	thumb := solid(80, 60, func(x, y int) color.RGBA { return green }) // so we can tell which was used
	path := filepath.Join(dir, "IMG_0001.jpg")
	os.WriteFile(path, jpegWithExif(t, main, thumb, 6), 0o644)

	ex := readExif(mustRead(t, path))
	if ex.orient != 6 || len(ex.thumb) == 0 {
		t.Fatalf("exif: orientation %d, thumbnail %d bytes", ex.orient, len(ex.thumb))
	}
	p, err := loadPhoto(path)
	if err != nil {
		t.Fatal(err)
	}
	if p.w != 40 || p.h != 80 || p.img.Bounds().Dx() != 40 {
		t.Fatalf("upright size %dx%d (working %v)", p.w, p.h, p.img.Bounds())
	}
	if top, bottom := p.img.RGBAAt(20, 5), p.img.RGBAAt(20, 75); !near(top, red) || !near(bottom, blue) {
		t.Fatalf("turned the wrong way: top %v, bottom %v", top, bottom)
	}
	px, err := makeThumb(path)
	if err != nil || len(px) != thumbW*thumbH {
		t.Fatalf("thumb: %v", err)
	}
	if c := px[thumbH/2*thumbW+thumbW/2]; c>>5&63 < 40 { // the EXIF thumbnail (green) was used
		t.Fatalf("the thumbnail should come from EXIF: %04x", c)
	}
	cached, _ := filepath.Glob(filepath.Join(thumbDir(), "*"))
	if len(cached) != 1 {
		t.Fatalf("thumbnail cache: %v", cached)
	}
	if again, err := makeThumb(path); err != nil || again[100] != px[100] {
		t.Fatal("the cached thumbnail differs")
	}
}

func mustRead(t *testing.T, p string) []byte {
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestPhotoShrinkAndRotate(t *testing.T) {
	big := image.NewGray(image.Rect(0, 0, 4000, 3000))
	for i := range big.Pix {
		big.Pix[i] = uint8(i % 2 * 200) // fine stripes: averaged, they're 100
	}
	m := shrink(big, photoMaxW, photoMaxH)
	if m.Bounds().Dx() != 1280 || m.Bounds().Dy() != 960 {
		t.Fatalf("shrunk to %v", m.Bounds())
	}
	var total int
	for i := 0; i < len(m.Pix); i += 4 {
		total += int(m.Pix[i])
	}
	if avg := total / (len(m.Pix) / 4); avg < 95 || avg > 105 { // fine stripes average to grey
		t.Fatalf("area average %d, want ~100", avg)
	}
	if small := shrink(image.NewRGBA(image.Rect(0, 0, 50, 40)), 1280, 960); small.Bounds().Dx() != 50 {
		t.Fatal("shrink must never enlarge")
	}
	r := rotate90(solid(4, 2, func(x, y int) color.RGBA {
		if x == 0 && y == 0 {
			return red
		}
		return blue
	}))
	if r.Bounds().Dx() != 2 || r.Bounds().Dy() != 4 || !near(r.RGBAAt(1, 0), red) {
		t.Fatalf("rotate90: %v, corner %v", r.Bounds(), r.RGBAAt(1, 0))
	}
	if w, h := fitIn(1280, 960, 320, 132); w != 176 || h != 132 {
		t.Fatalf("fitIn %dx%d", w, h)
	}
}

func TestPhotoFolderAndTrash(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)
	dir := filepath.Join(home, "Pictures")
	os.MkdirAll(filepath.Join(dir, "Trips"), 0o755)
	os.MkdirAll(filepath.Join(dir, ".hidden"), 0o755)
	for _, n := range []string{"b.PNG", "a.jpg", "notes.txt", ".secret.jpg"} {
		os.WriteFile(filepath.Join(dir, n), []byte("x"), 0o644)
	}
	var names []string
	for _, it := range photoFolder(dir) {
		names = append(names, it.name)
	}
	if got := strings.Join(names, ","); got != "..,Trips,a.jpg,b.PNG" {
		t.Fatalf("folder: %s", got)
	}
	if photosStart() != dir {
		t.Fatalf("starts in %s, want ~/Pictures", photosStart())
	}
	if err := moveToTrash(filepath.Join(dir, "a.jpg")); err != nil {
		t.Fatal(err)
	}
	if exists(filepath.Join(dir, "a.jpg")) || !exists(filepath.Join(home, ".local/share/Trash/files/a.jpg")) ||
		!exists(filepath.Join(home, ".local/share/Trash/info/a.jpg.trashinfo")) {
		t.Fatal("not in the trash")
	}
	os.WriteFile(filepath.Join(dir, "a.jpg"), []byte("y"), 0o644) // a second a.jpg doesn't overwrite the first
	moveToTrash(filepath.Join(dir, "a.jpg"))
	if !exists(filepath.Join(home, ".local/share/Trash/files/a.2.jpg")) {
		t.Fatal("the second a.jpg should be a.2.jpg in the trash")
	}
}

func TestPhotoGIFAndFFmpeg(t *testing.T) {
	dir := t.TempDir()
	setHome(t, dir)
	pal := color.Palette{color.RGBA{0, 0, 0, 255}, red, blue, green}
	g := &gif.GIF{}
	for i := 0; i < 3; i++ {
		fr := image.NewPaletted(image.Rect(0, 0, 30, 20), pal)
		for j := range fr.Pix {
			fr.Pix[j] = uint8(i + 1)
		}
		g.Image = append(g.Image, fr)
		g.Delay = append(g.Delay, 20)
	}
	var b bytes.Buffer
	gif.EncodeAll(&b, g)
	os.WriteFile(filepath.Join(dir, "anim.gif"), b.Bytes(), 0o644)
	p, err := loadPhoto(filepath.Join(dir, "anim.gif"))
	if err != nil || len(p.frames) != 3 || !near(p.frames[2].RGBAAt(5, 5), green) {
		t.Fatalf("animated gif: %v, %d frames", err, len(p.frames))
	}

	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("no ffmpeg here for the other formats")
	}
	// a BMP (Go can't read it): through ffmpeg
	w, h := 8, 4
	row := (w*3 + 3) &^ 3
	bmp := make([]byte, 54+row*h)
	copy(bmp, "BM")
	binary.LittleEndian.PutUint32(bmp[2:], uint32(len(bmp)))
	binary.LittleEndian.PutUint32(bmp[10:], 54)
	binary.LittleEndian.PutUint32(bmp[14:], 40)
	binary.LittleEndian.PutUint32(bmp[18:], uint32(w))
	binary.LittleEndian.PutUint32(bmp[22:], uint32(h))
	binary.LittleEndian.PutUint16(bmp[26:], 1)
	binary.LittleEndian.PutUint16(bmp[28:], 24)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			copy(bmp[54+y*row+x*3:], []byte{220, 30, 30}) // BGR: blue
		}
	}
	os.WriteFile(filepath.Join(dir, "pic.bmp"), bmp, 0o644)
	p, err = loadPhoto(filepath.Join(dir, "pic.bmp"))
	if err != nil || p.w != 8 || !near(p.img.RGBAAt(2, 2), color.RGBA{30, 30, 220, 255}) {
		t.Fatalf("bmp through ffmpeg: %v", err)
	}
}

// drain runs the background work the views posted (thumbnails, decodes) until it's done.
func drain(a *App, done func() bool) {
	end := time.Now().Add(10 * time.Second)
	for !done() && time.Now().Before(end) {
		select {
		case f := <-a.work:
			f()
		case <-time.After(20 * time.Millisecond):
		}
		a.draw()
	}
}

// The grid and the viewer (DSL_DOC_SHOTS=<dir> saves the screens).
func TestPhotoScreens(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)
	t.Setenv("DSL_CONFIG", filepath.Join(home, ".config/dslauncher/config"))
	dir := filepath.Join(home, "Pictures")
	os.MkdirAll(filepath.Join(dir, "Holidays"), 0o755)
	// a few "photos": a sunset, sky and hills, a checker test card, a portrait
	sunset := solid(640, 480, func(x, y int) color.RGBA {
		t := float64(y) / 480
		c := color.RGBA{uint8(255 - t*120), uint8(140 - t*110), uint8(60 + t*120), 255}
		if dx, dy := float64(x-320), float64(y-300); dx*dx+dy*dy < 60*60 {
			c = color.RGBA{255, 220, 90, 255}
		}
		if float64(y) > 360+20*math.Sin(float64(x)/40) {
			c = color.RGBA{30, 40, 60, 255}
		}
		return c
	})
	hills := solid(800, 600, func(x, y int) color.RGBA {
		if float64(y) > 380+60*math.Sin(float64(x)/90) {
			return color.RGBA{60, uint8(150 + (x+y)%30), 70, 255}
		}
		return color.RGBA{uint8(110 + y/8), uint8(170 + y/10), 240, 255}
	})
	card := solid(400, 300, func(x, y int) color.RGBA {
		if (x/25+y/25)%2 == 0 {
			return color.RGBA{240, 240, 240, 255}
		}
		return color.RGBA{uint8(x * 255 / 400), 60, uint8(y * 255 / 300), 255}
	})
	portrait := solid(300, 500, func(x, y int) color.RGBA { return color.RGBA{uint8(80 + y/4), 90, uint8(200 - y/4), 255} })
	for name, m := range map[string]image.Image{"Sunset at the pier.jpg": sunset, "Hills.jpg": hills, "Test card.png": card, "Portrait.jpg": portrait} {
		f, _ := os.Create(filepath.Join(dir, name))
		if strings.HasSuffix(name, ".png") {
			png.Encode(f, m)
		} else {
			jpeg.Encode(f, m, &jpeg.Options{Quality: 85})
		}
		f.Close()
	}
	a := testApp(t)
	a.openPhotos()
	grid := a.top().(*PhotoGrid)
	drain(a, func() bool { return !grid.loading && len(photoThumbs) >= 4 })
	a.draw()
	a.saveShot("photos-grid")
	if grid.items[grid.sel].name != "Holidays" {
		t.Fatalf("selected %q first, want the first real item", grid.items[grid.sel].name)
	}
	grid.Key(a, cp0.Key{Name: "RIGHT"})
	grid.Key(a, cp0.Key{Name: "RIGHT"}) // Hills.jpg -> Portrait.jpg
	grid.Key(a, cp0.Key{Name: "ENTER"})
	view, ok := a.top().(*PhotoView)
	if !ok {
		t.Fatalf("Enter on a photo: %T", a.top())
	}
	drain(a, func() bool { return view.ph != nil })
	view.fadeAt = time.Time{}
	a.draw()
	a.saveShot("photos-view")
	if view.items[view.idx].name != "Portrait.jpg" {
		t.Fatalf("opened %s", view.items[view.idx].name)
	}
	view.Key(a, cp0.Key{Code: 6}) // 5: zoom
	a.draw()
	a.saveShot("photos-zoom")
	if view.zoom != 1 {
		t.Fatal("5 should zoom")
	}
	view.Key(a, cp0.Key{Code: 1, Name: "ESC"}) // Esc zooms out first
	if view.zoom != 0 || a.top() != view {
		t.Fatal("Esc should zoom out, not leave")
	}
	view.Key(a, cp0.Key{Code: 7}) // 6: next
	drain(a, func() bool { return view.ph != nil })
	view.Key(a, cp0.Key{Code: 9}) // 8: info
	view.fadeAt = time.Time{}
	a.draw()
	a.saveShot("photos-info")
	if !view.info || view.items[view.idx].name != "Sunset at the pier.jpg" {
		t.Fatalf("next + info: %s, info %v", view.items[view.idx].name, view.info)
	}
	view.Key(a, cp0.Key{Code: 1, Name: "ESC"})
	view.Key(a, cp0.Key{Name: "ENTER"}) // full screen
	view.viewKey = ""
	a.draw()
	a.saveShot("photos-full")
	if view.Bar(a) != nil {
		t.Fatal("full screen hides the key bar")
	}
	view.Key(a, cp0.Key{Code: 1, Name: "ESC"})
	if a.top() != grid {
		t.Fatalf("Esc should go back to the grid: %T", a.top())
	}
}
