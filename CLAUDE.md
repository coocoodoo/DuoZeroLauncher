# Duo Launcher on this Cardputer

This folder is the source of **Duo Launcher**, which is this device's **home screen**. Read
README.md for what it does. Notes for changing it here:

## It is the default launcher

- `APPLaunch.service` (a systemd user service of `pi`) is redirected by
  `~/.config/systemd/user/APPLaunch.service.d/home-screen.conf` to `~/.local/bin/cardputer-home`,
  which runs `~/apps/duo-launcher/run.sh`.
- 0.4.0: holding Esc on the home screen no longer quits Duo (holding Esc to close an app used to
  carry over and quit it). The only way out is Settings > Launcher > "Back to the system
  launcher", which exits with code 10 (`exitLeave`); `cardputer-home` then starts the stock
  launcher. Any other exit restarts Duo; 3 quick failures in a row fall back to the stock
  launcher. Keep these exit codes in step with `cardputer-home`.
- Boot logo: the root service `fb_load.service` shows the M5Stack logo through DRM. With
  `--keep-open` it holds the panel until a process whose /proc/PID/exe is the stock APPLaunch
  binary appears, so with Duo as home the logo never went away (Duo drew behind it). Fixed outside
  Duo with `/etc/systemd/system/fb_load.service.d/duo.conf` (ExecStart without `--keep-open`).
  If the splash ever sticks after an update, check that file first.
- **You are probably running inside an app that Duo started.** Never kill Duo or restart
  `APPLaunch.service` from here: it would end your own session. Install a new build and let the
  user reopen it.

## Build and install

```bash
cd ~/apps/duo-launcher/src
~/.local/go/bin/go test -p 1 ./...
~/.local/go/bin/go build -p 1 -trimpath -ldflags="-s -w" -o ../app.new .
cp -p ../app ../app.prev && mv -f ../app.new ../app
```

The new build loads when Duo restarts: hold Esc on its home screen (the stock launcher starts),
then reboot, or ask the user to restart it.

## Local changes (0.3.1)

- `cp0/cp0.go`: `guardCorner` repairs the bottom-left 16 px every 8 ms, because the Linux console
  cursor blinks there under the app. It is switched off while another app has the screen
  (`GuardCorner` in `runApp`). Keep that, or apps get Duo's old pixels drawn over them.
- `sound.go`: the mixer paces itself to at most 60 ms ahead of playback. Without it, held keys
  filled pacat's pipe and sounds kept playing after the key was released. `sound_test.go` checks it.

## Local changes (0.3.2)

- `ds.go` `dsCalendar`: redrawn to match the DS calendar: month on a white plate (`dsPlate`)
  touching the grid, spaced digits, dark outer frame. Today (0.3.3): white cell, dark red number
  on any weekday, inside a dithered outline alternating dark slate and light blue-grey
  (`todayFrame`). 0.3.4: the outline is 2 px thick (the user asked for it thicker); the 1-px
  white gap is only kept when the number still fits inside. `dsClock`: 2-px black frame, 6 x 6 paper grid, steel-blue second hand.
- `home.go`: clock is 76 px with the time and date on a white plate attached under it.
  `DSL_NOW="2026-10-25 12:10:28"` pins the shown time for scripted screenshots.

## Local changes (0.3.5)

- `home.go` `drawStatus`: the battery is the DS status-bar battery as pixel art (`dsBattery`,
  copied from a DS screenshot the user sent). 0.3.6: green above 50%, yellow 21-50%, red 20% or
  less, grey when unknown. The percentage stays to its left.

## Local changes (0.5.0)

- Hold Tab (0.8 s, `cp0.TabHold`) anywhere in Duo: lock (PIN if set) and turn the backlight off
  (`App.sleepNow`); any key wakes to the lock screen. A short Tab now acts on **release**
  (`Keys.Releases`, `Key.Released`; Tab sends one `Repeat` "held" event instead of repeating), so a
  hold never also toggles the calendar or unlocks. A Tab whose press woke the screen is ignored
  (`tabArmed`). While the screen is off Duo polls every 500 ms and doesn't draw.
- Scripted keys: `TAB` = press + release (two screenshots), `TABHOLD` = press, held, release.

## Local changes (0.6.0)

- Calculator: the result screen is `calcLCD` (ds.go): dark dot mesh, black frame, orange outline,
  white digits, an orange underscore cursor (a bar read as a "1"), on a translucent drop shadow
  instead of the input field's striped end caps.
- Self-update (`update.go`): Settings > Launcher > "Check for updates" reads `const version` from
  main.go on GitHub (`updateRepo`), and if newer downloads the tarball, builds it on the device
  (`~/.local/go/bin/go`, `-p 1`, GOTOOLCHAIN=local) into ~/.cache/dslauncher/update/app.new,
  checks it runs with `--sounds` (old builds have no `--version`), then `installScript` keeps
  app.prev and src.prev and swaps in app, src and app.conf, and Duo re-execs itself.
  "Undo last update" (shown when app.prev exists) swaps them back. sudo (`withSudo`) is asked for
  only if Duo's folder isn't writable. Build log: ~/.cache/dslauncher/update/build.log.
- **Releasing:** bump `const version` in main.go and `VERSION` in pkg/app.conf, then push to
  `main`. Devices update from `main`, so only push code that builds and passes tests.
- `app --version` prints the version; `app --update-check` downloads and builds the latest
  without installing it.

## Local changes (0.6.1)

- Smoother gradients: `dither565` (gfx.go, 4x4 ordered dither) is used by `pxSmooth` for the
  texture fills in `draw9` (bars, buttons, tiles, selected rows) and the `rrect`/`bar` gradients,
  so they don't band on the 16-bit screen. Flat fills (`rect`) and text stay undithered.
- Small icons use the SVG art: `smallIcon` (art.go) scales the 24 px tile or 20 px key-bar render
  down with area averaging; `artIcon` draws it (pixel glyph only when there is no SVG). Used for
  every page title (13 px), the home alarm bell and favourite star (11 px) and the strip arrows.

## Local changes (0.6.2)

- Smooth shaded textures: the ds.css bar/button textures are a few flat shades joined by 1-px
  checker rows, which became big blocks when stretched (a 16 px tile drawn at ~50 px). For
  tinted textures (`draw9` with a base colour) `smoothTex` averages each row's interior (side
  bevel columns kept; the 2-px bars averaged across) and `sliceY` stretches the middle with
  linear interpolation instead of nearest pixels; the result is dithered (`pxSmooth`). Untinted
  textures (input, alert, PictoChat frames) are drawn as before.

## Local changes (0.6.3)

- Update progress bar: `App.progress` (0..1, < 0 hidden) is drawn by `drawProgress` under the
  busy alert. Jobs set it with `App.setProgress` (safe from the job's goroutine; ignored once the
  job ended or was cancelled); `background` resets it. `buildUpdate(want, prog)` reports:
  download 2-20% (file size vs ~650 KB), unpack 22%, build 24-90% timed against the last build
  (`~/.cache/dslauncher/update-build-seconds`, default 2 min, with "about m:ss left"), check
  92%, install 96%. The update runs in the installed version's code, so the bar shows from the
  update after the one that installs 0.6.3.
- Scripted key `BUSY:<0..1>` shows the progress screen for screenshots; `BUSY:-` hides it.

## Local changes (0.6.4)

- Uninstalling apps (`remove.go`): INFO > UNINSTALL (key 7), Settings > Launcher > "Uninstall
  apps" (every app, hidden too) and "Removed apps" (the bin: put back, delete for good).
  `planRemoval` sorts entries into ZeroCode apps (`zc-<slug>.desktop`; folder from the "Made with
  ZeroCode (<folder>)" comment, `Entry.Origin`), package apps (`dpkg-query -S` on the .desktop,
  then on the program) and refused ones (built-ins, Duo, `protectedPkgs`, essential/required/
  important packages, entries no package owns such as zerocode.desktop).
- ZeroCode: the folder moves into the bin, then `sudo -n /usr/local/sbin/zc-app-root remove
  <slug>`; on failure the folder moves back. Put back: move back, `zc-app-root install <folder>`.
  If sudo wants a password (`errNeedPassword`) Duo asks for it and tries again.
- Packages: `dependents` reads dpkg's database (`dpkgDBFormat`, tab-separated because
  alternatives use "|") to list what `apt-get remove` takes with it; `apt-get -s` takes 10-20 s
  here, so it only runs inside the uninstall job (`aptAgrees`) and stops it if apt disagrees.
  `repackScript` (as root) rebuilds each package's .deb from /var/lib/dpkg/info (files, control,
  maintainer scripts, conffiles) into the bin, checked with `dpkg-deb -f`; then
  `apt-get remove` (never purge or autoremove). Put back: `apt-get install <the .deb copies>`.
- The bin: `removedDir()` = `~/.local/share/dslauncher/removed` (`DSL_DATA` overrides). Each
  record says "removing"/"restoring" while working; `loadRemoved` → `reconcile` repairs one
  cut short. `removeMu` keeps these jobs one at a time. Config changes (unpin, re-pin) and the
  rescan (`appsChanged`) go through `a.work` so they happen even if the wait was cancelled.
- `alert` is a Confirm with only OK, for messages too long for a toast; Confirm shows 5 lines.
- sudo took ~10 s per call here because the hostname (chris, set with Duo's old rename) was not in
  /etc/hosts ("unable to resolve host"). Fixed in Duo, with the password typed on the device
  (`fixHosts`/`hostsScript` in sys.go: points the 127.0.1.1 line at the name, keeps
  /etc/hosts.duo-bak): Settings > User > Device name now does it after `hostnamectl`, and Settings
  > System shows "Fix slow admin actions" while `hostnameListed` is false. Never run sudo on the
  UI thread anyway.
- Scripted runs: `INFO:<entry key>` opens an app's details; a run ends only after its last
  background job. Tests: `remove_test.go`; `DSL_APT_COMPARE="pkg ..."` (on the device) checks
  `dependents` against `apt-get -s`.

## Local changes (0.6.5)

- Home strip (home.go): `Home.lift` holds each app's 0..1 "standing up", eased every frame
  (`ease`, rate 12/s) towards 1 for the chosen app and 0 for the rest; `Animating` keeps frames
  coming until they settle. `drawTile` raises a tile `tileRise` (6) px by `smoothstep(lift)`,
  fades its colour from grey to the favourite colour and casts `softShadow` (gfx.go: a dithered
  box-blurred rectangle) around it. Tiles are drawn lowest lift first. The strip moved down 3 px
  (`sy = iy+55`) to make room.
- Launch (`App.launchFromHome`, `Home.drawLaunch`): when an app opens from the home screen the
  tile rises `launchRise` (24) px more over `launchDur` (0.45 s) while a white wash covers
  everything, its shadow spreading and fading on the paper, and the name fades in under it; then
  the app starts (the launch sound's 450 ms overlap the animation). Opened from elsewhere, the
  old white wash and "Starting..." box remain.
- Scripted runs: a step ending in `~` films 10 frames (`App.film`); launches save
  `launch-25..100` (`saveShot`).

## Local changes (0.6.6)

- Volume keys (volume.go): the keyboard driver's Fn layer (keymap2) sends KEY_MUTE 113 (Fn+A),
  KEY_VOLUMEDOWN 114 (Fn+S) and KEY_VOLUMEUP 115 (Fn+D); cp0 names them MUTE/VOLUMEDOWN/VOLUMEUP.
  `App.handle` takes them before any view (so they work in text fields and on the lock screen,
  and wake a dimmed screen). `volumeKey` steps the cached level by 5 (re-read with wpctl when
  stale), `sendVolume` passes it to one `volumeWorker` goroutine (a 1-slot channel: newest
  request wins) that runs `wpctl` and plays a tick at the new level. `drawVolume` is the meter,
  shown for `volShow` after the last press; Settings > Speaker's rows follow it. Scripted runs
  never touch the real volume. Other Fn keys the driver sends and Duo ignores: Fn+U/I
  brightness, Fn+Q/W/E media.
- Fix: on 0.6.5 the strip's slide and rise were often skipped. The idle home screen redraws only
  about once a second, so the first frame after a key press had a dt of up to 1 s and the
  easing jumped to the end. `Home.frameDT` counts a gap over 0.1 s as one frame
  (`home_test.go`). Scripted runs never showed it: they draw right before every key.

## Local changes (0.6.7)

- Sounds are now the Nintendo DS system sounds (KHInsider gamerip "Nintendo DS System Sounds",
  FLACs converted by `tools/mksounds.py <folder> [ffmpeg]`; which plays when:
  assets/sounds/SOURCE.md). New events: increase/decrease (sliders and choices, `stepSound`),
  alarm-exit, startup-birthday (`App.startupSound`), shutdown (restart/turn off), loading.
- Loading blip: `App.loadingBlip` (called from `draw` while the busy screen shows) plays
  `loading` (DSi Internet Settings Seq #11, a 0.1 s blip) when the busy screen appears and once
  per turn of the yellow squares (`loadingTurn`, the same 0.533 s cycle `dsLoading` uses).
- The alarm loops the 14 s DS ring (`Sound.Length`) and stops it (`Sound.Stop`) with alarm-exit.

Don't use `pacat` with a volume flag in tests (the sound server remembers it per client name).
