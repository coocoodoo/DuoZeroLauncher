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
