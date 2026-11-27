package main

// Photos, built in: the engine. Images are decoded with Go's own decoders (JPEG, PNG, GIF);
// anything else ffmpeg can read (WebP, BMP, TIFF, HEIC...) goes through ffmpeg as a PNG. Big
// photos are shrunk while decoding with area averaging, so a 12-megapixel JPEG becomes a
// working copy of at most 1280 x 960. JPEG thumbnails come from the camera's own EXIF thumbnail
// when there is one (much faster than decoding the photo), and the EXIF orientation is honoured.
// Thumbnails are kept in ~/.cache/dslauncher/thumbs, keyed by path, size and time.

import (
	"bytes"
	"crypto/sha1"
	"encoding/binary"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	photoMaxW  = 1280 // the working copy of a photo is at most this big
	photoMaxH  = 960
	thumbW     = 60
	thumbH     = 45
	photoBytes = 80 << 20 // files bigger than this aren't opened
)

var goImageExts = map[string]bool{".jpg": true, ".jpeg": true, ".png": true, ".gif": true}
var ffImageExts = map[string]bool{".webp": true, ".bmp": true, ".tif": true, ".tiff": true, ".heic": true, ".heif": true, ".avif": true}

func isPhoto(name string) bool {
	e := strings.ToLower(filepath.Ext(name))
	return goImageExts[e] || ffImageExts[e]
}

// ---- EXIF ----

type exifInfo struct {
	orient       int // 1 normal .. 8, as EXIF numbers them
	thumb        []byte
	taken        string // "2026:11:25 14:03:11"
	camera       string
	thumbOrient  int
	haveExifData bool
}

// readExif finds the APP1 Exif block at the start of a JPEG and reads what Photos uses.
func readExif(head []byte) exifInfo {
	info := exifInfo{orient: 1}
	if len(head) < 4 || head[0] != 0xff || head[1] != 0xd8 {
		return info
	}
	for i := 2; i+4 <= len(head); {
		if head[i] != 0xff {
			return info
		}
		marker := head[i+1]
		n := int(binary.BigEndian.Uint16(head[i+2:]))
		if marker == 0xda || i+2+n > len(head) { // start of the picture data, or past what we read
			return info
		}
		seg := head[i+4 : i+2+n]
		if marker == 0xe1 && len(seg) > 14 && string(seg[:6]) == "Exif\x00\x00" {
			parseTIFF(seg[6:], &info)
			return info
		}
		i += 2 + n
	}
	return info
}

func parseTIFF(t []byte, info *exifInfo) {
	if len(t) < 8 {
		return
	}
	var bo binary.ByteOrder
	switch string(t[:2]) {
	case "II":
		bo = binary.LittleEndian
	case "MM":
		bo = binary.BigEndian
	default:
		return
	}
	info.haveExifData = true
	u16 := func(o int) int {
		if o < 0 || o+2 > len(t) {
			return 0
		}
		return int(bo.Uint16(t[o:]))
	}
	u32 := func(o int) int {
		if o < 0 || o+4 > len(t) {
			return 0
		}
		return int(bo.Uint32(t[o:]))
	}
	str := func(count, off int) string {
		if count <= 4 || off < 0 || off+count > len(t) {
			return ""
		}
		return strings.TrimRight(string(t[off:off+count]), "\x00 ")
	}
	ifd := func(off int, each func(tag, typ, count, val, at int)) int {
		n := u16(off)
		if n > 200 {
			return 0
		}
		for k := 0; k < n; k++ {
			e := off + 2 + k*12
			each(u16(e), u16(e+2), u32(e+4), u32(e+8), e+8)
		}
		return u32(off + 2 + n*12) // the next IFD
	}
	exifIFD := 0
	next := ifd(u32(4), func(tag, typ, count, val, at int) {
		switch tag {
		case 0x0112:
			info.orient = u16(at)
		case 0x0110:
			info.camera = str(count, val)
		case 0x8769:
			exifIFD = val
		}
	})
	if exifIFD > 0 {
		ifd(exifIFD, func(tag, typ, count, val, at int) {
			if tag == 0x9003 {
				info.taken = str(count, val)
			}
		})
	}
	if next > 0 { // IFD1: the thumbnail
		off, ln := 0, 0
		ifd(next, func(tag, typ, count, val, at int) {
			switch tag {
			case 0x0201:
				off = val
			case 0x0202:
				ln = val
			}
		})
		if off > 0 && ln > 0 && off+ln <= len(t) {
			info.thumb = t[off : off+ln]
		}
	}
	if info.orient < 1 || info.orient > 8 {
		info.orient = 1
	}
}

// ---- decoding and shrinking ----

type photo struct {
	img      *image.RGBA // the working copy, upright
	w, h     int         // the original size
	frames   []*image.RGBA
	delays   []int // hundredths of a second, for animated GIFs
	exif     exifInfo
	size     int64
	modified time.Time
}

// shrink averages src down to fit maxW x maxH (never enlarging); a fast path for the
// decoders' own image types keeps a 12 MP JPEG to about a second on the Cardputer.
func shrink(src image.Image, maxW, maxH int) *image.RGBA {
	b := src.Bounds()
	sw, sh := b.Dx(), b.Dy()
	if sw <= 0 || sh <= 0 {
		return image.NewRGBA(image.Rect(0, 0, 1, 1))
	}
	ow, oh := sw, sh
	if ow > maxW || oh > maxH {
		if ow*maxH > oh*maxW {
			ow, oh = maxW, max(1, sh*maxW/sw)
		} else {
			ow, oh = max(1, sw*maxH/sh), maxH
		}
	}
	sum := make([]uint32, ow*oh*4)
	xm := make([]int, sw)
	for x := range xm {
		xm[x] = x * ow / sw
	}
	add := func(oy, x int, r, g, bl uint32) {
		i := (oy*ow + xm[x]) * 4
		sum[i] += r
		sum[i+1] += g
		sum[i+2] += bl
		sum[i+3]++
	}
	switch s := src.(type) {
	case *image.YCbCr:
		for y := 0; y < sh; y++ {
			oy := y * oh / sh
			for x := 0; x < sw; x++ {
				yi := s.YOffset(b.Min.X+x, b.Min.Y+y)
				ci := s.COffset(b.Min.X+x, b.Min.Y+y)
				r, g, bl := color.YCbCrToRGB(s.Y[yi], s.Cb[ci], s.Cr[ci])
				add(oy, x, uint32(r), uint32(g), uint32(bl))
			}
		}
	case *image.RGBA:
		for y := 0; y < sh; y++ {
			oy := y * oh / sh
			row := s.Pix[(y)*s.Stride:]
			for x := 0; x < sw; x++ {
				p := row[x*4:]
				add(oy, x, uint32(p[0]), uint32(p[1]), uint32(p[2]))
			}
		}
	case *image.NRGBA: // straight alpha: over white, like a page
		for y := 0; y < sh; y++ {
			oy := y * oh / sh
			row := s.Pix[y*s.Stride:]
			for x := 0; x < sw; x++ {
				p := row[x*4:]
				a := uint32(p[3])
				add(oy, x, (uint32(p[0])*a+255*(255-a))/255, (uint32(p[1])*a+255*(255-a))/255, (uint32(p[2])*a+255*(255-a))/255)
			}
		}
	case *image.Gray:
		for y := 0; y < sh; y++ {
			oy := y * oh / sh
			for x := 0; x < sw; x++ {
				v := uint32(s.Pix[y*s.Stride+x])
				add(oy, x, v, v, v)
			}
		}
	default:
		for y := 0; y < sh; y++ {
			oy := y * oh / sh
			for x := 0; x < sw; x++ {
				r, g, bl, a := src.At(b.Min.X+x, b.Min.Y+y).RGBA()
				// premultiplied: put it over white
				add(oy, x, (r+(0xffff-a))>>8, (g+(0xffff-a))>>8, (bl+(0xffff-a))>>8)
			}
		}
	}
	out := image.NewRGBA(image.Rect(0, 0, ow, oh))
	for i := 0; i < ow*oh; i++ {
		n := sum[i*4+3]
		if n == 0 {
			n = 1
		}
		out.Pix[i*4] = uint8(sum[i*4] / n)
		out.Pix[i*4+1] = uint8(sum[i*4+1] / n)
		out.Pix[i*4+2] = uint8(sum[i*4+2] / n)
		out.Pix[i*4+3] = 255
	}
	return out
}

// orient turns an image upright for an EXIF orientation (1..8).
func orient(m *image.RGBA, o int) *image.RGBA {
	if o <= 1 || o > 8 {
		return m
	}
	w, h := m.Bounds().Dx(), m.Bounds().Dy()
	ow, oh := w, h
	if o >= 5 {
		ow, oh = h, w
	}
	out := image.NewRGBA(image.Rect(0, 0, ow, oh))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			var nx, ny int
			switch o {
			case 2:
				nx, ny = w-1-x, y
			case 3:
				nx, ny = w-1-x, h-1-y
			case 4:
				nx, ny = x, h-1-y
			case 5:
				nx, ny = y, x
			case 6:
				nx, ny = h-1-y, x
			case 7:
				nx, ny = h-1-y, w-1-x
			case 8:
				nx, ny = y, w-1-x
			}
			copy(out.Pix[(ny*ow+nx)*4:(ny*ow+nx)*4+4], m.Pix[(y*w+x)*4:(y*w+x)*4+4])
		}
	}
	return out
}

// rotate90 turns an image a quarter turn clockwise (the viewer's ROTATE).
func rotate90(m *image.RGBA) *image.RGBA { return orient(m, 6) }

// decodeAny decodes a file: Go's decoders for JPEG, PNG and GIF, ffmpeg for the rest.
func decodeAny(path string, data []byte) (image.Image, error) {
	ext := strings.ToLower(filepath.Ext(path))
	if goImageExts[ext] {
		m, _, err := image.Decode(bytes.NewReader(data))
		if err == nil {
			return m, nil
		}
		if ext != ".gif" { // some "jpg" files are something else: let ffmpeg try
			if m2, err2 := ffmpegDecode(path); err2 == nil {
				return m2, nil
			}
		}
		return nil, err
	}
	return ffmpegDecode(path)
}

func ffmpegDecode(path string) (image.Image, error) {
	cmd := exec.Command("ffmpeg", "-hide_banner", "-loglevel", "error", "-nostdin", "-i", path, "-frames:v", "1",
		"-vf", fmt.Sprintf("scale='min(%d,iw)':'min(%d,ih)':force_original_aspect_ratio=decrease", photoMaxW*2, photoMaxH*2),
		"-f", "image2pipe", "-vcodec", "png", "-")
	out, err := cmd.Output()
	if err != nil {
		return nil, errors.New("this kind of picture can't be opened")
	}
	return png.Decode(bytes.NewReader(out))
}

// photoDecode lets one big decode run at a time (the viewer's and the grid's thumbnails):
// a 12 MP JPEG takes tens of megabytes while it's being read.
var photoDecode sync.Mutex

// loadPhoto reads a picture for the viewer: upright, at most photoMaxW x photoMaxH; animated
// GIFs keep their frames.
func loadPhoto(path string) (*photo, error) {
	photoDecode.Lock()
	defer photoDecode.Unlock()
	fi, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if fi.Size() > photoBytes {
		return nil, errors.New("this picture is too big to open")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	p := &photo{size: fi.Size(), modified: fi.ModTime(), exif: readExif(data)}
	if strings.EqualFold(filepath.Ext(path), ".gif") {
		if g, err := gif.DecodeAll(bytes.NewReader(data)); err == nil && len(g.Image) > 1 &&
			g.Config.Width*g.Config.Height*len(g.Image) <= 40_000_000 {
			p.w, p.h = g.Config.Width, g.Config.Height
			canvas := image.NewRGBA(image.Rect(0, 0, p.w, p.h))
			for i := range canvas.Pix {
				canvas.Pix[i] = 255
			}
			for i, fr := range g.Image {
				if i >= 300 {
					break
				}
				prev := image.NewRGBA(canvas.Bounds())
				copy(prev.Pix, canvas.Pix)
				drawOver(canvas, fr)
				p.frames = append(p.frames, shrink(canvas, photoMaxW, photoMaxH))
				d := 10
				if i < len(g.Delay) && g.Delay[i] > 1 {
					d = g.Delay[i]
				}
				p.delays = append(p.delays, d)
				if i < len(g.Disposal) {
					switch g.Disposal[i] {
					case gif.DisposalBackground:
						r := fr.Bounds()
						for y := r.Min.Y; y < r.Max.Y; y++ {
							for x := r.Min.X; x < r.Max.X; x++ {
								canvas.SetRGBA(x, y, color.RGBA{255, 255, 255, 255})
							}
						}
					case gif.DisposalPrevious:
						canvas = prev
					}
				}
			}
			p.img = p.frames[0]
			return p, nil
		}
	}
	m, err := decodeAny(path, data)
	if err != nil {
		return nil, err
	}
	p.w, p.h = m.Bounds().Dx(), m.Bounds().Dy()
	p.img = orient(shrink(m, photoMaxW, photoMaxH), p.exif.orient)
	if p.exif.orient >= 5 {
		p.w, p.h = p.h, p.w
	}
	return p, nil
}

// drawOver paints a GIF frame onto the canvas, leaving transparent pixels alone.
func drawOver(dst *image.RGBA, fr *image.Paletted) {
	r := fr.Bounds().Intersect(dst.Bounds())
	for y := r.Min.Y; y < r.Max.Y; y++ {
		for x := r.Min.X; x < r.Max.X; x++ {
			c := fr.Palette[fr.ColorIndexAt(x, y)]
			cr, cg, cb, ca := c.RGBA()
			if ca < 0x8000 {
				continue
			}
			dst.SetRGBA(x, y, color.RGBA{uint8(cr >> 8), uint8(cg >> 8), uint8(cb >> 8), 255})
		}
	}
}

// ---- pixels for the screen ----

// to565 turns an RGBA picture into dithered RGB565 pixels.
func to565(m *image.RGBA) []uint16 {
	w, h := m.Bounds().Dx(), m.Bounds().Dy()
	out := make([]uint16, w*h)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			p := m.Pix[(y*w+x)*4:]
			out[y*w+x] = dither565(x, y, RGB{p[0], p[1], p[2]})
		}
	}
	return out
}

// fitIn is the size of a w x h picture fitted inside bw x bh (keeping its shape).
func fitIn(w, h, bw, bh int) (int, int) {
	if w*bh > h*bw {
		return bw, max(1, h*bw/w)
	}
	return max(1, w*bh/h), bh
}

// coverCrop scales m to fill w x h exactly, cutting off what sticks out (thumbnails).
func coverCrop(m *image.RGBA, w, h int) *image.RGBA {
	sw, sh := m.Bounds().Dx(), m.Bounds().Dy()
	cw, ch := sw, sh
	if sw*h > sh*w {
		cw = sh * w / h
	} else {
		ch = sw * h / w
	}
	sub := m.SubImage(image.Rect((sw-cw)/2, (sh-ch)/2, (sw-cw)/2+cw, (sh-ch)/2+ch)).(*image.RGBA)
	c := image.NewRGBA(image.Rect(0, 0, cw, ch))
	for y := 0; y < ch; y++ {
		copy(c.Pix[y*c.Stride:y*c.Stride+cw*4], sub.Pix[y*sub.Stride:y*sub.Stride+cw*4])
	}
	return shrink(c, w, h)
}

// ---- thumbnails ----

func thumbDir() string { return filepath.Join(cacheDir(), "thumbs") }

func thumbKey(path string, fi os.FileInfo) string {
	h := sha1.Sum([]byte(fmt.Sprintf("%s|%d|%d", path, fi.Size(), fi.ModTime().UnixNano())))
	return fmt.Sprintf("%x", h[:10])
}

// makeThumb is the thumbWxthumbH picture for the grid (from the cache when it's there).
func makeThumb(path string) ([]uint16, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	cache := filepath.Join(thumbDir(), thumbKey(path, fi))
	if b, err := os.ReadFile(cache); err == nil && len(b) == thumbW*thumbH*2 {
		px := make([]uint16, thumbW*thumbH)
		for i := range px {
			px[i] = binary.LittleEndian.Uint16(b[i*2:])
		}
		return px, nil
	}
	if fi.Size() > photoBytes {
		return nil, errors.New("too big")
	}
	var m *image.RGBA
	ext := strings.ToLower(filepath.Ext(path))
	if ext == ".jpg" || ext == ".jpeg" { // the camera's own thumbnail, when there is one
		f, err := os.Open(path)
		if err == nil {
			head := make([]byte, 128<<10)
			n, _ := io.ReadFull(f, head)
			f.Close()
			if ex := readExif(head[:n]); len(ex.thumb) > 0 {
				if t, err := jpeg.Decode(bytes.NewReader(ex.thumb)); err == nil && t.Bounds().Dx() >= thumbW {
					m = orient(shrink(t, thumbW*3, thumbH*3), ex.orient)
				}
			}
		}
	}
	if m == nil {
		p, err := loadPhoto(path)
		if err != nil {
			return nil, err
		}
		m = p.img
	}
	px := to565(coverCrop(m, thumbW, thumbH))
	b := make([]byte, len(px)*2)
	for i, v := range px {
		binary.LittleEndian.PutUint16(b[i*2:], v)
	}
	os.MkdirAll(thumbDir(), 0o755)
	writeFileAtomic(cache, string(b), 0o644)
	return px, nil
}

// ---- folders ----

type photoItem struct {
	name, path string
	dir        bool
}

// photoFolder lists a folder: ".." (unless at /), folders, then pictures, by name.
func photoFolder(dir string) []photoItem {
	var dirs, pics []photoItem
	ents, _ := os.ReadDir(dir)
	for _, e := range ents {
		if strings.HasPrefix(e.Name(), ".") {
			continue
		}
		p := filepath.Join(dir, e.Name())
		if e.IsDir() || (e.Type()&os.ModeSymlink != 0 && isDir(p)) {
			dirs = append(dirs, photoItem{e.Name(), p, true})
		} else if isPhoto(e.Name()) {
			pics = append(pics, photoItem{e.Name(), p, false})
		}
	}
	by := func(s []photoItem) {
		sort.Slice(s, func(i, j int) bool { return strings.ToLower(s[i].name) < strings.ToLower(s[j].name) })
	}
	by(dirs)
	by(pics)
	var out []photoItem
	if parent := filepath.Dir(dir); parent != dir {
		out = append(out, photoItem{"..", parent, true})
	}
	return append(append(out, dirs...), pics...)
}

// photosStart is where Photos opens: the folder last used, else ~/Pictures, ~/Screenshots
// (Duo's screenshots), or home.
func photosStart() string {
	if d := loadPhotosConf().dir; isDir(d) {
		return d
	}
	for _, d := range []string{filepath.Join(homeDir(), "Pictures"), screenshotDir()} {
		if isDir(d) {
			return d
		}
	}
	return homeDir()
}

func photosConfPath() string { return filepath.Join(filepath.Dir(configPath()), "photos") }

func savePhotosDir(d string) {
	c := loadPhotosConf()
	c.dir = d
	c.save()
}

// moveToTrash puts a file in the desktop trash (~/.local/share/Trash), where file managers
// can put it back.
func moveToTrash(path string) error {
	trash := filepath.Join(homeDir(), ".local/share/Trash")
	if err := os.MkdirAll(filepath.Join(trash, "files"), 0o700); err != nil {
		return err
	}
	os.MkdirAll(filepath.Join(trash, "info"), 0o700)
	base := filepath.Base(path)
	name := base
	for i := 2; exists(filepath.Join(trash, "files", name)); i++ {
		ext := filepath.Ext(base)
		name = fmt.Sprintf("%s.%d%s", strings.TrimSuffix(base, ext), i, ext)
	}
	info := fmt.Sprintf("[Trash Info]\nPath=%s\nDeletionDate=%s\n", path, time.Now().Format("2006-01-02T15:04:05"))
	if err := os.WriteFile(filepath.Join(trash, "info", name+".trashinfo"), []byte(info), 0o600); err != nil {
		return err
	}
	if err := os.Rename(path, filepath.Join(trash, "files", name)); err != nil {
		os.Remove(filepath.Join(trash, "info", name+".trashinfo"))
		return err
	}
	return nil
}

func humanSize(n int64) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%d KB", n>>10)
	}
	return fmt.Sprintf("%d bytes", n)
}
