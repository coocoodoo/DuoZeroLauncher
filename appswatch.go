package main

// Apps added or removed outside Duo (zc-app over SSH, ZeroCode making an app, a package being
// installed) used to show only after Duo restarted or an app was closed. Every few seconds Duo
// now compares the change times of the launcher's folders and rescans when they moved: a
// couple of stat calls, skipped while the screen is off.

import (
	"os"
	"path/filepath"
	"strings"
	"time"
)

const appsCheckEvery = 3 * time.Second

// appsStamp sums up the launcher folders: an entry or an icon added, replaced or removed
// changes their modification time.
func appsStamp() string {
	var b strings.Builder
	for _, d := range []string{"applications", "share/images"} {
		if fi, err := os.Stat(filepath.Join(launchRoot(), d)); err == nil {
			b.WriteString(fi.ModTime().UTC().Format(time.RFC3339Nano))
		}
		b.WriteByte('|')
	}
	return b.String()
}

// checkApps rescans when the launcher folders changed since the last look.
func (a *App) checkApps() {
	if a.sim || a.dimLevel == 2 || a.launch != nil || time.Since(a.appsCheckAt) < appsCheckEvery {
		return
	}
	a.appsCheckAt = time.Now()
	s := appsStamp()
	switch {
	case a.appsStampV == "":
		a.appsStampV = s
	case s != a.appsStampV:
		a.appsStampV = s
		a.appsChanged()
		a.dirty = true
	}
}
