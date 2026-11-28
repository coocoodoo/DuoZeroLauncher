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
- Loading ticks: the DSi Internet Settings Seq #11 is two clicks ~71 ms apart. `mksounds.py`
  (`loading_loop`) turns it into a 133 ms gapless loop, each click one step of the yellow
  squares (`loadingTurn`/8) apart. `App.loadingSound` (from `draw`) starts it with
  `Sound.Loop` in phase with `dsLoading` when the busy screen appears and `Sound.Stop`s it when
  the squares go (job done, cancelled, or the screen turned off). Loop voices are never dropped
  for one-shot sounds (`Play` keeps at most 4 of those).
- The alarm loops the 14 s DS ring (`Sound.Length`) and stops it (`Sound.Stop`) with alarm-exit.

## Local changes (0.7.0)

- PictoChat, built in (`builtin:pictochat`, icon `assets/apps/pictochat.png` loaded as
  `embed:apps/pictochat.png`). `ws.go`: a small RFC 6455 WebSocket (client and server, stdlib
  only, so the on-device self-update still needs no module downloads). `picto_net.go`: the
  PictoChat Online JSON protocol (see its header comment), `pcClient`, `pcServer` (rooms A-D,
  16 each, names cleaned, the sender stamped by the server, 400 ms rate limit, sizes bounded by
  `pcClean`), and LAN discovery (`pcAnnounce` / `pcListen`, UDP 47121, "DUOPICTO1 <port> <name>").
  The host listens on 47120. `picto.go`: the chats list (`pictoMenu`), the rooms screen and the
  room (`PictoView`): log above (messages rendered once into `pcImage`), compose box 1:1 below
  (`pcCompose`), tool and button columns; TYPE/DRAW modes via the optional `typing()` that
  `syncText` now checks. Written from the protocol only: ayunpictojava has no licence, so none
  of its code or images are used. pict.chat itself needs a captcha token, which Duo never sends.
- Internet chats by room code: `mqtt.go` is a small MQTT 3.1.1 client (TLS, last will, QoS 0,
  keep-alive); `picto_mqtt.go` (`pcConnectCode` -> `pcNet`, a `pcLink` like `pcClient`) meets
  everyone with the same code on `pcBrokers` (tried in a fixed order so all land on the same
  one). `pcKeys`: PBKDF2-SHA256 (100k) of the code gives the AES-256-GCM key and the topic
  `duopicto1/<hex>`; `/p` carries presence (hello every 8 s, `ask` for an immediate reply, bye,
  and the last will), `/m/<room>` the messages; everything is sealed with the topic as AAD and
  stamped (more than 10 min off is dropped). Members unseen for 25 s count as gone.
  `picto_mqtt_test.go` has a fake broker; `DSL_MQTT_LIVE=1` tries the real ones.
- Scripted runs: `DSL_PICTO_NOLAN=1` skips UDP discovery, `DSL_PICTO_ADDR=127.0.0.1:0` hosts on
  localhost only, and the key `BOT` (in a room) has a second client "Rin" join and send a
  drawing. Tests: `picto_test.go` (WebSocket round trip, two clients through the host, compose,
  rendering).

## Local changes (0.7.1)

- PictoChat opens with the launch animation too: `App.launchBuiltin` (main.go) plays it for a
  built-in opened from its home tile (the same `launchFromHome` apps get), then the view is
  pushed. 0.7.3: Calculator, IP Panel and Settings too (CLI, Python and the Store already had it
  through `runApp`; SSH asks for the host first, so it doesn't).

## Local changes (0.7.2)

- PictoChat's icon is the user's 9 x 9 pixel smiley in a square: `tools/mkpictoicon.py` writes
  `icons/pictochat.svg`, the app icon `assets/apps/pictochat.png` (9 x 9; `scaleIcon` scales it
  up nearest-neighbour, so it stays crisp) and the title-bar mask `assets/tiles/pictochat.png`
  (mapped to `artPicto` in `tileIcons`). Change the grid there and rerun it.

## Local changes (0.7.4)

- Screen off like a phone: `App.screenOff` (hold Tab via `sleepNow`, and the idle timeout) sets
  the backlight to 0, pauses the corner guard and powers the panel down with FBIOBLANK
  (`cp0.Screen.Blank`, cp0/blank_linux.go; a no-op elsewhere; if the driver refuses, only the
  backlight is off as before). While `dimLevel == 2` only Tab wakes it (`handle`); volume keys
  change the volume without waking. `wake` -> `screenOn` unblanks first; `shutdown` always
  unblanks so the next program (stock launcher, restarted Duo) gets a working panel.
  `sleep_test.go` drives it through `handle`.
- Locking closes only `*Input` boxes (a half-typed password) now, not every typing screen: the
  Calculator and PictoChat are still there after unlocking.
- 0.7.5: on the device, after FBIOBLANK unblank the user saw the Linux text console instead of
  Duo (fbcon repaints on unblank; maybe it also pans the framebuffer, so Duo's frames at offset
  0 aren't the ones shown). Panel power-off is now off by default (`panelPowerOff`,
  `DSL_PANEL_OFF=1` to try it); the backlight alone turns the screen off, as before 0.7.4.
  To make it work: check `fbset`/yoffset and `/sys/class/vtconsole/*/bind` on the device; likely
  fixes are FBIOPAN_DISPLAY back to 0 plus a full redraw after unblank, or putting tty1 in
  KD_GRAPHICS / unbinding fbcon (root).

## Local changes (0.7.6)

- Why the screen didn't go dark and the terminal showed: the backlight PWM is inverted, so
  brightness 0 (PWM stopped) still glows; and the kernel text console (fbcon on tty1, KD_TEXT)
  draws on the same framebuffer: it repaints when the panel is unblanked or the console is
  poked by a key, and while Duo sleeps nothing paints over it.
- `screenOff` now paints the screen black before the backlight goes to 0, and powers the panel
  down (FBIOBLANK) only when `/run/duo-console` exists (`panelPowerOff()`).
- The Doctor (`doctor.go`; Settings > System > Doctor; `app --doctor [--fix]`): checks and fixes
  home screen (user files), boot logo, console (`device/duo-console.service`: tty1 in KD_GRAPHICS
  via python3 ioctl 0x4B3A, marker `/run/duo-console`), /etc/hosts, Bluetooth audio (user file);
  reports Go, the ZeroCode helper and brightness. Root fixes are one script
  (`doctorRootScript`) run with `runSudo` (UI) or an interactive `sudo` (CLI). Device files are
  embedded from `device/` (`deviceFS`). Tests: `doctor_test.go` (`sh -n` on the root script runs
  where `sh` exists, e.g. on the device).

## Local changes (0.7.7)

- Truly dark: the backlight's supply (`backlight-vcc`) is a fixed always-on regulator, dimming is
  PWM channel 3 of M5's I/O expander (i2c 1-004f, "m5ioe1"), and M5's stock launcher logs
  "[BOOT] set m5ioe1 line 9 low" then restores the brightness. Without that (Duo as home
  screen) brightness 0 still glows. `backlight_linux.go` holds line 9 low for Duo's whole life
  (GPIO v2 chardev ioctl in pure Go, the gpio group is enough; struct layout checked by
  `backlight_linux_test.go`); `shutdown` lets it go. Tested by the user: line 9 low = fully dark
  at 0, normal brightness otherwise.
- Battery while the screen is off (`asleep`): the sound stream closes, `statusLoop` skips its
  nmcli/bluetoothctl polling (one refresh on wake), the main loop waits 2 s per turn, and the
  corner guard's ticker really stops (`cp0` guardWake; before, it ticked 125/s even when off:
  ~580 Duo wake-ups/s were measured while asleep). With the Doctor's console fix the guard isn't
  needed at all (`App.guardCorner`).
- `Sound.stopLocked` waits for pacat (300 ms, then kills): a self-update's in-place restart used
  to leave it a zombie; `reapOrphans` collects old ones at start-up.

## Local changes (0.8.0)

- Brightness keys (`brightness.go`): Fn+U `BRIGHTNESSDOWN` (224), Fn+I `BRIGHTNESSUP` (225), in
  tenths, never below 5%, with a meter like the volume one. Ignored while the screen is off;
  a dimmed screen wakes first. Settings > Screen follows.
- Screenshots (`screenshot.go`): Fn+J `PRINT` (KEY_SYSRQ 99 or KEY_PRINT 210) saves the frame as
  `~/Screenshots/duo-YYYYMMDD-HHMMSS.png` (`DSL_SCREENSHOTS` overrides) with a white flash.
  While another app runs, `runApp` starts `watchScreenshots`, which reads only the keyboard
  events Duo would drop anyway and copies the framebuffer (`cp0.Screen.Capture`). The key codes
  are the standard ones and still need checking on the device (`evtest`).
- Low battery (`battery_warn.go`): an alert at 15% and again at 5% (`batWarned`), reset when
  charging or above 20%; waits while the screen is off. `DSL_BATTERY=12` fakes the level.
- Home: Up shows the recently opened apps (`recentApps`, newest first, up to 8), Down all of
  them again (`Home.recent`, `allKey`); this replaces Up/Down jumping 5. Built-ins now count as
  used when opened. Space or "/" opens search (`search.go`): prefix, then word start, then
  substring, then letters in order; Tab next match, Enter open, Esc close.
- Download Play (`dlplay.go`, built-in `builtin:dlplay`): share a ZeroCode app (folder in
  ~/apps) with a Cardputer on the same Wi-Fi. The sender packs it (tar.gz, no `*.prev`, `.test`,
  `.git` or links), announces `DUODL1 {json}` on UDP 47131 every 2 s and serves `/info` and
  `/get?code=NNNN` on TCP 47130 (5 wrong codes per address, then refused). The receiver lists
  offers, asks for the 4-digit code, checks size and SHA-256, unpacks with path checks into
  ~/apps/<slug> (an existing copy is kept as `<slug>.prev-<time>` after a confirm) and runs
  `zc-app install`. "Receive from an address" for networks that block broadcasts.
  `dlplay_test.go` shares an app over loopback.

## Local changes (0.22.0)

- Battery saver (`saver.go`; Settings > Screen > Battery saver, `Config.Saver`, on by default):
  30 s (`saverDelay`) after the screen goes off, on battery, Duo switches Wi-Fi off
  (`nmcli radio wifi off`, no root needed) and back on when the screen wakes. Not on the
  charger, with Duo Link on or its Wi-Fi up, with a stereo connected, a job running, or a chat /
  transfer / stereo setup on the stack (`saverWanted`). A Wi-Fi the user had off stays off.
  `~/.cache/dslauncher/saver-wifi-off` marks it, so start-up (`saverRecover`) and leaving Duo
  (`saverQuit`) always give Wi-Fi back. "Last sleep" shows how long and how many % the last
  sleep on battery used (10 minutes or more). `saver_test.go`.
- Why only Wi-Fi, measured on the Cardputer with the screen off (charging-current change on
  the bq27220 gauge): the CPU is 99.7% idle at 600 MHz already; the kernel has no sleep states
  (`/sys/power/state` empty, no CPU hotplug, no cpuidle states); Wi-Fi off about 25 mA;
  Bluetooth off about 3 mA (not worth dropping headphones); `vcgencmd display_power 0`
  nothing. The governor and the ACT LED need root and wouldn't change an idle system.
- While asleep with the saver on, the Cardputer can't be reached over SSH on battery.

## Local changes (0.21.2)

- With a PIN set, Duo starts locked (`App.lockAtStart`, called in `main` right after the home
  screen is made). Before, only waking the screen ("Lock when it wakes") and holding Tab locked:
  a fresh boot, or Duo restarting after a crash or an update, opened the home screen without
  the PIN (the user: "a safety issue"). It doesn't depend on "Lock when it wakes".
  `TestLockedAtStart` (sleep_test.go). The PIN only guards Duo's screen: SSH and the stock
  launcher (after 3 failed starts) don't ask for it.

## Local changes (0.21.1)

A review pass for speed and bugs (profiled with `go test -bench Draw -cpuprofile`).

- Drawing is about 8x cheaper per frame (home 1.24 ms -> 0.15 ms on the PC; the same pixels).
  `draw9` with a base colour (every bar, button, tile, key cap) was hard-light + gradient +
  dither in floating point for each pixel, each frame: `nineBlock` (ds.go) renders a block once
  per (texture, size, colour, dither phase) and `blitBlock` copies rows; the cache holds at most
  `nineCacheMax` pixels and starts again when full. `shade` uses three small lookup tables.
  `ds_cache_test.go` compares both with the pixel-by-pixel versions.
- `App.waitApp`: while an app runs the UI thread keeps running `a.work`. Nobody read that queue
  before (16 slots): a stereo reconnecting during an app got no picture, a Bluetooth remote's
  buttons arrived all at once afterwards, Music could stall. `notify` does nothing while an app
  has the screen. Fn+Q/W/E are handled inside apps too (`watchScreenshots`).
- The loop draws one more frame after the last animated one, so toasts and the volume meter
  leave on time; the brightness meter and the screenshot flash count as animating.
- Duo Link: wrong pairing codes are counted per address across connections (`wrongCode`), not
  per connection; frames are sent without copying (`net.Buffers`) and captured into one reused
  buffer. Download Play / file sharing: the wrong-code count is under a lock; a failed write
  while receiving is an error (the checksum only covered what arrived).
- Photos: `decodeAny` reads the header first and refuses (or hands to ffmpeg) pictures over
  48 megapixels (`photoFits`); `gifFrames` counts a GIF's frames without decoding, so an
  animation is decoded only when all frames fit (20 million pixels). `guards_test.go`.
- Editor: undo copies are kept to about 8 MB (`undoSteps`; 200 steps of a 1 MB file was 200 MB);
  saving follows links and writes in place when the folder can't take a temporary file.
- Video: frame buffers are reused, and the sound is passed on in whole samples.
- Known, not fixed: the alarm doesn't ring while an app is open (the loop waits for the app).

## Local changes (0.21.0)

- Settings > **Themes** (`themes.go`, page 2 tile `icons/settings/themes.svg` /
  `assets/tiles/themes.png` and Settings > Launcher > Theme): 10 surface themes (`uiThemes`:
  Classic, Dark, Midnight, OLED, Warm Paper, Game Boy, Sakura, Ocean, Terminal, Amber CRT),
  persisted as `theme=<id>` in `Config.Theme`. `applyUITheme` updates the palette vars (`cPaper`,
  `cGridBG`, `cGridLine`, `cButton`, `cBorder`, `cFocus`, `cText`, `cSub`, `cDisabled`, clock and
  calendar colours) and `shadeTex` (`ds.go`) shifts bright-key bevel textures toward mid-grey for
  mid/dark themes (`DarkChrome` 0..1; 0 on Classic is pixel-identical). Every theme keeps the
  exact same Favourite colour (`a.theme()`) behaviour across bars, selections, toggles, sliders
  and progress bars. `ThemePick` previews live, restores on Back, saves on Choose, and key 5
  opens `ColorPick` directly. Tests: `themes_test.go`.

## Local changes (0.20.2)

- The speaker is quiet while a stereo takes the sound (`link_audio.go`): `routeToLink` loads a
  `module-null-sink` "duolink" with pactl, makes it the default sink, moves every sink-input onto
  it (again every second, `keepOnLinkSink`, for streams WirePlumber restores to the speaker) and
  parec records `duolink.monitor`. When the stereo goes, the old default is set back and the
  module unloaded (streams fall back to the speaker); `restoreLinkAudio` does it on quit and
  `cleanupLinkSink` at start-up after a crash. No pactl: the old `@DEFAULT_MONITOR@` capture.
  While routed, the volume keys change the silent sink's volume (the stereo has its own knob).

## Local changes (0.20.1)

- Switching Wi-Fi networks (`settings.go`): Settings > Wi-Fi > "Switch network" lists what's in
  range; picking one connects (a saved one without asking, by its profile `Network.Conn` from
  `savedSSIDs`, since profile names can differ from SSIDs, e.g. "preconfigured"). "Saved
  networks" opens Connect / Forget per network (`savedActions`), so a saved network can be
  joined even if the scan missed it. `joinNetwork` takes Duo Link's own Wi-Fi down first (same
  radio) and sets Duo Link back to shared Wi-Fi (`leftLinkWifi`); scanning while that Wi-Fi is
  up asks to turn it off (`errLinkWifiOn`). A refused rescan falls back to `--rescan auto`.

## Local changes (0.20.0)

- Duo Link over the Cardputer's own Wi-Fi (`link_hotspot.go`): `startHotspot` (re)makes the
  NetworkManager profile "Duo Link" (AP, band bg, WPA2/CCMP, PMF off for head units, ipv4
  shared at `hotspotIP` 10.42.0.1/24, ipv6 off, autoconnect no) and brings it up; `stopHotspot`
  takes it down so NM rejoins a saved network. Name `DuoLink-<host>` and an 8-digit password
  are made once and kept in duolink.json (`SSID`, `Pass`). `Config.LinkWifi` (`link_wifi`):
  `startLink` brings the hotspot up too (at boot as well); `App.linkWifiUp` tracks it; leaving
  for the stock launcher takes it down. Needs `wifi.share.protected` in the polkit rule (Doctor
  item 7 now checks it) and dnsmasq.
- Set up a stereo (`link_setup.go`, `LinkSetup`): turns Duo Link and the Wi-Fi on, then three
  steps (join the Wi-Fi, type 10.42.0.1:47140 in the stereo's browser, open Duo Link) that move
  on by themselves: stations from `iw dev <wlan> station dump` (or /proc/net/arp), app
  downloads (`linkServer.downloads`), a connected stereo. NEXT/BACK step by hand.
- No Duo Link over USB: `viaUSB` (loopback = adbd's forward, or usb*/rndis*/ecm*/ncm* gadget
  addresses) gets DENIED `linkNoUSB`; tests set `usbOK`. HELLO is now read before any refusal
  (closing with it unread reset the connection on Windows and lost the reason). WELCOME carries
  the Cardputer's `id`.
- App 1.2: `AdbTransport`, `UsbReceiver` and the USB intent filter are gone. Sockets are made by
  the Wi-Fi `Network` (`WifiNet`), so a stereo with mobile data still reaches 10.42.0.1. With
  nothing heard it quietly tries the last address and 10.42.0.1 in turn, and connects by itself
  to the only Cardputer heard the first time. Tokens are also kept by host:port.

## Local changes (0.19.0)

- Duo Link step 2. Sound on the stereo (`link_audio.go`): a stereo whose app sends
  `"audio": true` in HELLO gets lkAudio (0x87) messages, 20 ms of s16le stereo 44.1 kHz each,
  captured from PipeWire's monitor of the speaker (`parec --device=@DEFAULT_MONITOR@`, so
  everything the Cardputer plays); silence isn't sent. Settings > Duo Link > Sound on the
  stereo (`Config.LinkAudio`, on by default). The app plays it with an AudioTrack, dropping
  backlog over ~0.3 s (`AudioPlayer.java`).
- Over USB (`AdbTransport.java`, app 1.1; removed in 0.20.0, it kept dropping): with the Cardputer's USB switch to the LEFT it is an
  ADB device, so the app speaks ADB over the stereo's USB host port (CNXN, AUTH with its own RSA
  key: signature first, then the public key in adbd's format if asked, OPEN "tcp:47140", then
  WRTE/OKAY) and tunnels the same Duo Link protocol; nothing changes on the Cardputer, and the
  cable charges it. The app lists "Cardputer (USB)", asks Android for USB permission, opens
  itself when the Cardputer is plugged in (`res/xml/device_filter.xml`), and pairs over USB
  once (token "usb"). Untested on a real stereo: if adbd requires keys, the app's key must be
  accepted on the Cardputer once.
- The app is plain Java with top-level or static nested classes only (this SDK's d8 crashes on
  inner and anonymous classes from newer javac). `link_audio_test.go` streams a tone.

## Local changes (0.18.0)

- Sharing files between Cardputers (`share.go`): Files > MENU > Send to a Cardputer packs the
  marked items (`packFiles`, .tar.gz under their own names, no links) and serves them with
  Download Play's server (`serveOffer`, refactored out of `startSend`), announced as
  "DUOFILES1 {json}" on UDP 47131 (offer `Kind: "files"`, `Slug: "files"`, a few names,
  count); older Duos listen only for "DUODL1", so they never try to install files as an app.
  Receive files (Files menu, Settings > Sharing) lists offers (`dlListenFor`), asks the code,
  checks free space, downloads with progress (1 GB packed at most, an hour), checks size and
  SHA-256 (`dlFetch`), and unpacks into `~/Downloads/From <sender>/` (`unpackFilesTo`: nothing
  outside it, no links, a taken top-level name gets " (2)"), then opens it in Files.
- `DLSend` takes a title and two lines of instructions (files or apps). Settings > Sharing:
  receive, how to send, where files go, Download Play. `share_test.go`.

## Local changes (0.17.0)

- Video player (`video.go`, `VideoView`; Files opens videos with it, and Open with > Video
  player). `probeVideo` (ffprobe JSON: size, fps, codec, sound, length). One ffmpeg per play
  or seek: the picture scaled to fit (or, with FILL, cropped to fill) 320x170 at most 30 fps
  as raw RGB565 on stdout, the sound as s16le 44.1 kHz stereo on fd 3 (`ExtraFiles`, not on
  Windows) to pacat "Duo Video" with the player's volume. `pictures` shows each frame when the
  wall clock (paused time left out) reaches it, which also paces ffmpeg; late frames show at
  once. Keys: 4/6 -/+10 s (Z/C too), 5 play/pause (Space), 7 fit/fill, 8 info, F/X volume,
  Enter hides/shows the bar (it hides after 3 s of playing), Esc leaves. The end stops the
  clock and shows the bar; a playing video keeps the screen on.
- `video_test.go` makes a clip with ffmpeg (testsrc2 + sine) and plays, pauses, seeks, fills,
  ends; run it with -race (two races were fixed: change the size only after stopping the old
  playback, and set the clock under the lock).

## Local changes (0.16.0)

- Music: shuffle and repeat (`music_extras.go`; off / all / one, saved in the music config;
  `orderFrom` builds the play order starting with the chosen song, `autoNext` follows it at
  the end of a song: repeat one replays, repeat off stops after the last, shuffle with repeat
  all reshuffles each round; NEXT/PREV step through the same order). S and R in Music; SHUF /
  RPT1 / NO RPT shown in the song panel; Settings > Music has the rows.
- Media keys: cp0 names 164 PLAYPAUSE, 163 NEXTSONG, 165 PREVIOUSSONG, 166 STOPCD (the
  keyboard's Fn+Q / Fn+E / Fn+W, still to confirm on the device); `App.handle` sends them to
  `mediaKey` from any screen, with the screen off too; PLAY with nothing loaded plays the music
  folder. `watchRemotes` reads Bluetooth remotes (headphones, cars): every 5 s it looks for
  "AVRCP" devices in /proc/bus/input/devices and reads their media keys (BlueZ needs the
  uinput module to make that device).
- Now playing: on the lock screen a bubble (title, artist or time, progress) replaces the
  message, and 4 / 5 / 6 work there without unlocking (`lockMusicKey`); on the home screen the
  title bar shows the song after a note while it plays (`homeTitle`).
- Tests: `music_extras_test.go` (play order, AVRCP parsing, media keys, panels).

## Local changes (0.15.0)

- Text editor (`editor.go`, `Editor`, a TextView so letters type; the arrows are the Fn
  arrows): Enter keeps the indent, Tab indents with the file's own unit (tab or 4 spaces),
  Backspace/Del join lines; Ctrl+S save (atomic, keeps the mode), Ctrl+Z/Y undo/redo
  (snapshots; a run of typing or of deleting is one step), Ctrl+F/N find, Ctrl+G go to line,
  Ctrl+C/X/V/D line copy/cut/paste/duplicate, Ctrl+A/E line start/end, Home/End/PgUp/PgDn;
  Esc asks Save and close / Close without saving / Keep editing. Files keep their tabs, CRLF
  and final newline. Refuses binary files and files over 1 MB. Opened from Files (MENU >
  Edit, Open with > Text editor, E in the text viewer; a new text file opens in it).
  `editor_test.go`.

## Local changes (0.14.0)

- Settings has a second page (`settings2.go`, `tilesPerPage` 12): Duo Link, Music (volume,
  visualizer, folder, play order), Photos (slideshow pace, thumbnail cache), Files (hidden
  files, sort, bookmarks, trash), Sharing, Doctor, Updates (version, check, undo). Key 5 turns
  the page; Up/Down switch rows within a page. Tile icons `icons/settings/{link,music,photos,
  files,sharing,doctor,updates}.svg` -> `assets/tiles/*.png` (24 px). Photos' settings file is
  now `dir=` and `slide=` lines (`loadPhotosConf`). `TestSettingsPage2`.

## Local changes (0.13.1)

- Check for updates asks GitHub's API for the newest commit on main (`latestCommit`, header
  `Accept: application/vnd.github.sha`) and reads `main.go` and downloads the source at that
  commit (`updateURLs`, `updateRef`). The raw-file server caches branch URLs: after 0.13.0 was
  pushed it kept serving 0.11.0's main.go for over ten minutes, so the Cardputer saw no update.
  Without the API (60 requests an hour) it falls back to the branch. `update_urls_test.go`
  (`DSL_UPDATE_LIVE=1` asks GitHub for real).

## Local changes (0.13.0)

- Files, built in (`builtin:files`, icon from `icons/files-app.svg`). `files.go`: `listDir`
  (folders first; name in natural order, date, size or type), `kindOf` (by extension; the
  executable bit), `pasteInto` (copy, or move: rename on the same drive, else copy then delete;
  links copied as links; "name (2).ext" when taken, keeping .tar.gz together; never into
  itself; progress and stop, no half-written file left), `zipPaths`, `extractArchive` (.zip,
  .tar, .tar.gz, .tgz into a new folder; absolute paths, ".." and links skipped; 4 GB cap),
  `searchFiles` (names below a folder, 12 deep, 500 at most), the desktop trash (`trashList`,
  `restore`, `erase`), `readPreview` (binary detection), settings in
  `~/.config/dslauncher/files` (dir, sort, hidden, bookmarks). `diskSpace`/`sameDevice` are
  in sys_linux.go (stand-ins in sys_other.go).
- `files_view.go`: `FileBrowser` (4 OPEN, 5 MARK or Space, 6 MENU, 7 PLACES, 8 BACK; Enter/C
  open, Z/Backspace up; "/" filter, a letter jumps, Ctrl+C/X/V/A, Del to the trash, "."
  hidden files; Esc clears the filter, then the marks, then leaves), `PopMenu` (a reusable
  pop-up list), `menu` (open, open with, copy, cut, paste, rename, delete, new folder/file,
  zip, extract, search, filter, mark all, sort, hidden, bookmark, properties), `places`
  (home folders that exist, bookmarks, /media and /mnt drives, /, trash), `TrashView` (restore,
  delete for good, empty), `TextFileView` (text with line numbers, wrap or sideways scroll,
  find and find again, top/end; hex for binary files; first 512 KB), properties as a `DocText`
  with the folder size added up in the background. Opening: pictures in Photos, sounds in
  Music, PS1 games in the PS1 app (`~/apps/ps1/run.sh <game>`), scripts and programs in Tiny
  Terminal (after asking), archives extracted (after asking), the rest in the text viewer.
  Long jobs (`job`) show progress and stop on Esc.
- Tests: `files_test.go` (sorting, names, copy/move/stop, zip and tar.gz with path attacks,
  search, trash, preview, screens).

## Local changes (0.12.0)

- Photos, built in (`builtin:photos`, icon `assets/apps/photos.png` from `icons/photos-app.svg`).
  `photos.go` is the engine: Go's decoders for JPEG/PNG/GIF, ffmpeg (to PNG) for WebP, BMP, TIFF,
  HEIC, AVIF; `shrink` area-averages while reading (fast paths for YCbCr/RGBA/NRGBA/Gray), so the
  working copy is at most 1280x960; EXIF (`readExif`) gives the orientation, date, camera and
  the camera's own thumbnail, which the grid uses instead of decoding the photo. Thumbnails
  (60x45, cover-cropped, dithered RGB565) are cached in `~/.cache/dslauncher/thumbs` keyed by
  path, size and time. `photoDecode` lets one big decode run at a time (memory). Delete moves to
  the desktop trash (`~/.local/share/Trash`, with a .trashinfo).
- `photos_view.go`: `PhotoGrid` (5x2 thumbnails, folders as tiles; 4 OPEN, 5 SLIDESHOW, 6 UP,
  7 INFO, 8 BACK; starts in the folder last used, else ~/Pictures, ~/Screenshots, home) and
  `PhotoView` (fit with area averaging, small pictures enlarged by whole steps; 4 PREV, 5 ZOOM
  fit/2x/4x with arrow panning, 6 NEXT, 7 ROTATE, 8 INFO; Enter full screen, Space slideshow
  every 5 s which keeps the screen on, Del to the trash, Esc zooms out then leaves; animated
  GIFs play; a 0.3 s fade between photos; the next photo is prefetched).
- Key-bar icons `icons/{photo,zoom,rotate,up}.svg`. Tests: `photos_test.go` (EXIF orientation
  and thumbnail, shrink, rotate, folders, trash, GIF, BMP via ffmpeg, screens).

## Local changes (0.11.1)

- New apps show up by themselves (`appswatch.go`): Duo used to rescan only at start, after an
  app closed, and after its own installs and uninstalls, so an app registered from outside
  (`zc-app` over SSH, ZeroCode making one, a package) stayed missing until a restart. Every 3 s
  (not while the screen is off or an app runs) `checkApps` compares the modification times of
  `applications/` and `share/images/` and calls `appsChanged` when they moved.
  `TestNewAppsShowUp`.

## Local changes (0.10.0)

- Music, built in (`builtin:music`, icon `assets/apps/music.png`): a Go port of the user's MP3
  Player app (Rust, `../mp3-player`). `music.go` is the engine: ffmpeg decodes to s16le stereo
  44.1 kHz, Duo applies the volume (0-100, never above: it clips) and writes to its own pacat
  ("Duo Music", separate from Duo's UI sound stream); a 512-point FFT of a 22 kHz mono copy gives
  38 log-spaced bands, plus a scope ring. ffprobe gives the length and artist, ffmpeg a 15x15
  cover. The player is `App.music`: it keeps playing when the screen is closed, the screen is
  off or another app runs, and moves on by itself (`ended`; a folder of unplayable files stops
  with a message). Held Z/C seeks add up and jump 320 ms after the last press.
- `music_fx.go`: the five visualizers (EMBERS NEBULA WARP RAIN PHOSPHOR), a 157x36 heat grid
  drawn 2x with CRT rows (the original was 44 rows tall; Duo's title bar takes the space).
- `music_view.go`: Now playing, Queue, Folders. Keys as in the original: 4 PREV, 5 PLAY/PAUSE,
  6 NEXT, 7 VISUAL (on the lists: back to Now playing), 8 QUEUE/FOLDERS; Tab cycles; F/X volume,
  Z/C seek, Enter or Space play/pause; Folders: Enter/C opens, Z goes up, A plays every song
  below. Esc closes Music; the music goes on, shown by a note in the status bar
  (`musicPlayer.active`). Settings in `~/.config/dslauncher/music` (vol, vis, dir).
- 0.10.1 (the user): no visualizer name, no "F/X VOLUME  Z/C SEEK" hint and no "Visual: ..."
  toast on Now playing; the keys still work (Fn+S/Fn+D are the volume keys people use).
- 0.11.0: five more visualizers in `music_fx_more.go` (state in `fxMore`): CUBE (wireframe cube
  with a smaller one inside; its 12 edges fly apart and tumble with bass and beats, sparks from
  the corners on a hit, and drift back together when it's quiet), HYPER (hexagon tunnel),
  HORIZON (synthwave sun, spectrum mountains with a valley for the sun, scrolling grid),
  HELIX (double helix, rungs fill with their band, sparks on beats), FIREWORKS (rockets on beats,
  spectrum skyline). `fxCount` = 10; `lineAll` draws long lines, `set` hides what's behind.
- Key-bar icons: `icons/{prev,play,pause,next,visual,queue,folder,music}.svg`, rendered to
  `assets/icons/*.png` (20x20) by drawing each SVG on a 20x20 canvas in headless Chrome
  (`chrome --headless=new --virtual-time-budget=5000 --dump-dom page.html`, the page writing the
  PNGs' base64 into itself) and mapped in `barIcons`. This PC's ffmpeg has no librsvg.
- Tests: `music_test.go` (real ffmpeg on generated tones when it's installed: play, pause,
  next, auto-advance, seek, bad files; spectrum; screens with `DSL_DOC_SHOTS`).

## Local changes (0.9.0)

- Duo Link (`link.go`, `link_app.go`; Settings > Launcher > Duo Link): an Android car stereo
  shows this screen and sends keys back. The protocol is in `link.go`'s header (TCP 47140,
  "DLK1", typed messages; discovery "DUOLINK1 {json}" on UDP 47141). Frames are read back from
  the framebuffer (`cp0.Screen.CaptureInto`), so apps Duo starts show too; 32x10 tiles are
  compared and changed runs sent zlib'd, at most 25 fps with 2 frames unacknowledged.
- Keys from the stereo are written into the keyboard's event device (`linkKeyboard`, evdev
  write), so Duo and every app reading it get them. The kernel only passes keys the keyboard
  declares (`evKeyBits`); others go straight into Duo (`cp0.Keys.Inject`) while Duo is in front,
  and arrows become F/X/Z/C in other apps. Only the Cardputer's own keys are accepted
  (`cp0.KnownKey`, never power); keys still held when a stereo leaves are released. Text is
  typed with `cp0.KeyFor` (Shift + key).
- Pairing: a 4-digit code on the Cardputer (`LinkPair`, Esc/8 refuses), 5 tries then the
  address waits a minute, 2 minutes to type it; the stereo keeps a token, Duo only its SHA-256
  (`~/.config/dslauncher/duolink.json`). One stereo at a time. While one is connected the
  screen doesn't dim or turn off by itself (`linkBusy`).
- The Android app is `android/duolink` (plain Java, no libraries, minSdk 21). `build.sh` builds
  it without Gradle (aapt2, javac --release 8, d8, zipalign, apksigner; key in
  `~/.android/duolink.keystore`) into `assets/duolink.apk`, which is embedded and served at
  `http://<address>:47140/` for the stereo's browser. Rebuild and commit the APK after changing
  the app. This SDK's d8 crashes on inner/anonymous classes from newer javac: use static nested
  classes and lambdas.
- Tests: `link_test.go` (pairing, frames, keys, tokens, wrong codes, HTTP, screens).

## Local changes (0.8.2)

- The Doctor explains itself (the user: verify what's already done, say whether anything is
  needed, and explain what to do for Duo to work properly). Every check has `What` (what it does),
  `Why` (what breaks without it) and, for things the Doctor can't put in, `Help` (the manual
  steps); `docCheck.todo` turns the state into what to do ("Nothing to do: it's already in
  place", "Choose FIX...", "restart the Cardputer to finish", or `Help`).
- `tally` sorts checks into fix / later / manual / ok / not needed; `headline` sums them up
  ("All set: Duo has everything it needs." or "2 fixes needed, 1 waits for a restart, 1 needs
  you."), shown as a toast when the Doctor opens and as its footer. `doctorAdvice` is the "What
  to do" page (also printed by `app --doctor`).
- `DocText` (doctor.go): a scrolling text page (Up/Down, PgUp/PgDn) with an optional key-4
  action, used for "What to do" (FIX ALL), each check's details (FIX or UNDO) and the result
  after fixing (fixed and checked / restart to finish / not fixed, then the new headline).
- Start-up check (`doctorAtStart`, 20 s after start, not in scripted runs): updates the
  Settings > System > Doctor value and, when something needs fixing or the user's attention,
  `checkDoctorAsk` asks once on the home screen whether to open the Doctor. The same set of
  problems is asked about only once (`doctor.json` "asked").
- Tests: `TestDoctorAdvice`, `TestDoctorAsksOnce`; `DSL_DOC_SHOTS=<dir>` saves the pages.

## Local changes (0.8.1)

- The Doctor covers every launcher tweak in `device/TWEAKS.md` (scope: launcher only; no Go,
  ZeroCode or shell PATH checks). Checks with fixes: `home`, `bluetooth` (user files);
  `bootlogo`, `cursor` (`device/hide-console-cursor.service`), `console` (unit enabled, marker,
  tty1 KD mode via `kdMode`), `hosts`, `wifi` (`device/49-cardputer-wifi.rules`, checked with
  `nmcli general permissions`), `groups` (video gpio input audio lp, those that exist). Read-only:
  `blackout`, `backlight`, and the factory pieces (autologin, linger, sound, keyboard, udev, adb).
- Content, not existence: installed files are compared with the embedded copies (UPDATE when they
  differ); units must be enabled. States: OK, FIX, UPDATE, AFTER BOOT/LOGIN (`docLater`), MISSING
  (`docInfo`), NOT USED.
- Every fix keeps the first file it replaces as `<file>.duo-bak` and records itself in
  `~/.config/dslauncher/doctor.json`; tapping an OK row the Doctor put in offers Undo
  (`app --doctor --undo KEY` over SSH). Root changes run in one `doctorRootScript` call (one
  sudo, password on stdin), each key in its own subshell printing `ok KEY` / `fail KEY`.
- After fixing it checks again and reports fixed / counts later / not fixed. Log:
  `~/.cache/dslauncher/doctor.log` (check results and ok/fail only, no secrets, capped ~200 KB).
- `.gitattributes` keeps every file LF: the embedded device files must match the installed ones
  byte for byte.
- Fix: the PIN was asked several times after locking. `sleepNow` (hold Tab) and `wake` (lock on
  wake, after the timeout) only checked whether the *top* view was the `Lock`, so with the PIN box,
  a low-battery alert or the alarm over it they stacked another lock screen each time. All locking
  now goes through `App.lock()`: it closes a half-typed box on top and adds a lock screen only
  when none is in the stack (`lockIndex`). `TestLockNeverStacks` (sleep_test.go).

## Local changes (0.7.10)

- The loading ticks revolve around the listener on the mono speaker (the user: "Bingo"):
  `mksounds.py` `orbit` runs each swell through a travel delay that swings +-5% pitch
  (`LOADING_DOPPLER`; up coming round to the front, down going away) and a two-pole low-pass
  that opens from 2 kHz behind the head to 14 kHz in front (`LOADING_BACK_HZ`/`LOADING_FRONT_HZ`).
  One lap per swell, starting and ending behind, lined up with the sine swell.
  `TestLoadingLoop` checks the ticks behind are duller than the same click in front.

## Local changes (0.7.9)

- Loading ticks: each swell is now four ticks (half a turn, `LOADING_SWELL`) and the pause
  `LOADING_PAUSE` 0.25 s, so the loop is 0.783 s. The user tried swells twice as fast again
  (ticks every half step) and rejected them; this is the speed they kept.

## Local changes (0.7.8)

- Loading ticks, like the DS: `mksounds.py` `loading_loop` builds a 1.567 s pattern: a swell of
  eight ticks (one per square step, half a step in, the two DSi clicks taking turns) under a
  sin(pi t / T) envelope over one turn T = 0.5333 s, twice, then 0.5 s of silence. The peak is
  the menu sound volume (the mixer applies it). `loadingSound` loops it from its start.
- Measured on the device with 0.7.7, screen off: 4 Duo wake-ups/s and ~0.05% CPU (it was ~580/s
  and ~2% before). Screen on, idle home: ~870/s and ~6% CPU, mostly the corner guard reading
  the framebuffer 125 times a second; the Doctor's console fix switches the guard off.

Don't use `pacat` with a volume flag in tests (the sound server remembers it per client name).
