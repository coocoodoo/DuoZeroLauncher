package main

// The second page of Settings (the first page's 6 x 2 tiles were full): Duo Link, Music,
// Photos, Files, Sharing, Doctor and Updates. Key 5 turns the page; the arrows also move
// across pages.

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

var (
	artLinkTile = []string{
		"...........",
		"#########..",
		"#.......#..",
		"#.......#..",
		"#.......#..",
		"##......#..",
		"..#.....#..",
		"##.#######.",
		"..#........",
		"#..#.......",
		"...........",
	}
	artDoctor = []string{
		"...........",
		"...#####...",
		"...#...#...",
		"####...####",
		"#.........#",
		"#.#.#.#.#.#",
		"#.........#",
		"####...####",
		"...#...#...",
		"...#####...",
		"...........",
	}
	artSharing = []string{
		"...........",
		".....#.....",
		".....#.....",
		"...#.#.#...",
		"....###....",
		".....#.....",
		"###########",
		"#.........#",
		"#.......#.#",
		"###########",
		"...........",
	}
)

const tilesPerPage = 12

func settingsPage2() []tile {
	return []tile{
		{"Duo Link", artLinkTile, func(a *App) View { return linkPage(a) }},
		{"Music", artMusic, musicSettingsPage},
		{"Photos", artPhoto, photosSettingsPage},
		{"Files", artFolder, filesSettingsPage},
		{"Sharing", artSharing, sharingPage},
		{"Doctor", artDoctor, func(a *App) View { return doctorPage(a, nil) }},
		{"Updates", artRefresh, updatesPage},
	}
}

func (s *Settings) pages() int { return (len(s.tiles) + tilesPerPage - 1) / tilesPerPage }

// ---- Updates ----

func updatesPage(a *App) View {
	l := page("UPDATES", artRefresh)
	l.Refresh = func(a *App, l *RowList) {
		l.Rows = []*Row{
			{Label: "Version", Value: "Duo Launcher " + version},
			{Label: "Check for updates", Kind: rowAction, Value: "GITHUB", Do: func(a *App, r *Row) { checkForUpdates(a) }},
		}
		if fileExists(filepath.Join(appDir, "app.prev")) {
			l.Rows = append(l.Rows, &Row{Label: "Undo last update", Kind: rowAction, Do: func(a *App, r *Row) { undoUpdate(a) }})
		}
		l.Rows = append(l.Rows, &Row{Label: "Comes from", Value: "github.com/" + updateRepo})
		l.Footer = "Updates are built on this Cardputer"
	}
	l.Refresh(a, l)
	return l
}

// ---- Files ----

func filesSettingsPage(a *App) View {
	l := page("FILES", artFolder)
	l.Refresh = func(a *App, l *RowList) {
		c := loadFilesConf()
		trash := len(trashList())
		l.Rows = []*Row{
			{Label: "Show hidden files", Kind: rowToggle, On: c.hidden, Do: func(a *App, r *Row) {
				c := loadFilesConf()
				c.hidden = r.On
				c.save()
			}},
			{Label: "Sort by", Kind: rowChoice, Choices: sortNames, Choice: c.sortBy, Do: func(a *App, r *Row) {
				c := loadFilesConf()
				c.sortBy = r.Choice
				c.save()
			}},
			{Label: "Bookmarks", Kind: rowAction, Value: itoa(len(c.bookmarks)), Do: func(a *App, r *Row) {
				if len(c.bookmarks) == 0 {
					a.notify("No bookmarks: MENU > Bookmark this folder adds one", false)
					return
				}
				confirm(a, "CLEAR?", "Remove all bookmarks from Places?", func() {
					c := loadFilesConf()
					c.bookmarks = nil
					c.save()
					l.Refresh(a, l)
				})
			}},
			{Label: "Trash", Kind: rowLink, Value: itoa(trash), Do: func(a *App, r *Row) { a.push(&TrashView{items: trashList()}) }},
		}
	}
	l.Refresh(a, l)
	return l
}

// ---- Photos ----

var slideChoices = []int{3, 5, 10, 20, 60}

// photosConf is Photos' settings: the folder last open and the slideshow's pace.
type photosConf struct {
	dir   string
	slide int // seconds
}

func loadPhotosConf() photosConf {
	c := photosConf{slide: 5}
	b, _ := os.ReadFile(photosConfPath())
	for _, l := range strings.Split(string(b), "\n") {
		k, v, _ := strings.Cut(l, "=")
		switch k {
		case "dir":
			c.dir = v
		case "slide":
			if n, err := strconv.Atoi(v); err == nil && n >= 1 && n <= 600 {
				c.slide = n
			}
		}
	}
	return c
}

func (c photosConf) save() {
	writeFileAtomic(photosConfPath(), fmt.Sprintf("dir=%s\nslide=%d\n", c.dir, c.slide), 0o644)
}

func photosSettingsPage(a *App) View {
	l := page("PHOTOS", artPhoto)
	l.Refresh = func(a *App, l *RowList) {
		c := loadPhotosConf()
		var names []string
		choice := 1
		for i, s := range slideChoices {
			names = append(names, fmt.Sprintf("%d S", s))
			if s == c.slide {
				choice = i
			}
		}
		size, files, _, _ := treeSize(thumbDir(), time.Now().Add(3*time.Second))
		l.Rows = []*Row{
			{Label: "Slideshow, each photo", Kind: rowChoice, Choices: names, Choice: choice, Do: func(a *App, r *Row) {
				c := loadPhotosConf()
				c.slide = slideChoices[r.Choice]
				c.save()
			}},
			{Label: "Opens in", Value: musicASCII(tilde(photosStart()))},
			{Label: "Thumbnails kept", Kind: rowAction, Value: fmt.Sprintf("%d, %s", files, humanSize(size)), Do: func(a *App, r *Row) {
				os.RemoveAll(thumbDir())
				photoThumbs = map[string][]uint16{}
				a.notify("Thumbnails cleared: they're made again as needed", false)
				l.Refresh(a, l)
			}},
		}
	}
	l.Refresh(a, l)
	return l
}

// ---- Music ----

func musicSettingsPage(a *App) View {
	l := page("MUSIC", artMusic)
	l.Refresh = func(a *App, l *RowList) {
		p := a.musicPlayer()
		st := p.state()
		l.Rows = []*Row{
			{Label: "Volume", Kind: rowSlider, Num: st.vol, Min: 0, Max: 100, Step: 5, Unit: "%", Do: func(a *App, r *Row) { p.SetVol(r.Num) }},
			{Label: "Visualizer", Kind: rowChoice, Choices: fxModes, Choice: st.mode, Do: func(a *App, r *Row) { p.SetVisual(r.Choice) }},
			{Label: "Music folder", Value: musicASCII(tilde(p.musicDir()))},
		}
		l.Rows = append(l.Rows, musicPlayRows(a, l)...)
	}
	l.Refresh(a, l)
	return l
}
