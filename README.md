# Duo Launcher

A home screen for the M5Stack Cardputer Zero in the style of the DS system menu, built on the
textures and fonts of [ds.css](https://github.com/spiritov/ds.css) (MIT, by spiritov): the grid
paper, buttons and bars blended over a favourite colour the way its CSS does, the info header,
the orange alert with the loading squares, the striped input field, PictoChat frames, the pixel
font and the clock-digit font. The left "upper screen" shows the square clock (Tab: the calendar),
the right "touch screen" the apps. Sounds are the Nintendo DS system sounds (the "Nintendo DS
System Sounds" gamerip on KHInsider; `tools/mksounds.py` converts its FLACs into `assets/sounds/`,
see `assets/sounds/SOURCE.md` for which plays when), mixed so they can overlap. While the yellow
loading squares show, the DSi's loading ticks loop with them, one tick per step.

`tools/mkassets.py <ds.css-main/src/assets>` halves the textures to native pixels into `assets/`
(embedded in the binary) and pre-renders the fonts into `fontdata.go`. The clock font is under the
FontStruct Non-Commercial License (personal use).

It is the device's home screen: `device/cardputer-home` (started by `APPLaunch.service` through
`device/home-screen.conf`) runs Duo, restarts it if it crashes, and starts the stock M5Stack
launcher only when you pick Settings > Launcher > "Back to the system launcher".

![Screens](docs/screens.png)

## What it does

- **Home:** every app the system launcher lists (its `.desktop` folder), plus built-ins: Store,
  CLI, Python, SSH, IP Panel, Calculator, PictoChat, Settings. Arrows move (Up/Down jump 5), keys 4–8:
  OPEN, FAVE (pin to the front), INFO (details, hide, uninstall), SETTINGS, LOCK. Tab shows the
  calendar. Leave via Settings > Launcher.
- **Tab is the power button:** hold it anywhere to lock and switch the screen off, fully dark.
  While it is off nothing but Tab wakes it, like a phone; the volume keys still change the
  volume. (Like M5's own launcher, Duo holds line 9 of M5's I/O expander low, without which the
  backlight still glows at 0%. With the Doctor's "terminal kept off the screen" fix it also
  powers the panel down.)
- **Saves battery while the screen is off:** the sound stream closes, the status bar stops
  polling Wi-Fi and Bluetooth, and Duo wakes the CPU every 2 s instead of hundreds of times a
  second; Tab brings everything back. Tab wakes it to the lock screen, and a second
  Tab unlocks (or asks for your PIN). The screen's own idle timeout switches it off the same way.
  The chosen app's tile eases up out of the strip in your favourite colour with a soft shadow
  around it, and the one you scrolled away from eases back down.
- **PictoChat, built in:** draw and type messages to other Cardputers. See [PictoChat](#pictochat).
- **Volume keys everywhere in Duo:** Fn+S quieter, Fn+D louder (and unmute), Fn+A mute, in 5%
  steps (hold to keep going). A volume meter shows the level for a moment; it works on the lock
  screen too and wakes a dimmed screen.
- **Launching:** the chosen tile rises while everything else fades to white and the app's name
  fades in under it; then the app gets the screen and keyboard, and gives them back when it exits.
  CLI, Python and SSH open in Tiny Terminal (through a `$SHELL` wrapper).
- **Uninstalling apps,** with an undo: INFO > UNINSTALL, or Settings > Launcher > Uninstall apps
  (hidden apps too). Uninstalled apps wait in Settings > Launcher > Removed apps until you put
  them back or delete them for good. See [Uninstalling apps](#uninstalling-apps).
- **Settings:** Screen (brightness, dim/off timers, lock on wake), Speaker (volume, mute, menu
  sounds), Wi-Fi (on/off, scan and join, hidden networks, saved networks), Ethernet, Bluetooth
  (power, visibility, name, paired devices, pairing), Ports (Ext/Grove 5V), Battery, Launcher (sort,
  show/hide apps, uninstall apps, removed apps, favourites, updates), Developer (ADB, SSH server, last app's output), User (name, favourite
  colour, birthday, message, PIN, device name), Clock (24-hour, network time, set time, time zone,
  alarm), System (versions, memory, storage, licences, restart, power off). Admin actions ask for
  the account password on the device when they need it (kept in memory for 10 minutes); you never
  have to run sudo in a terminal.
- **Device name and /etc/hosts:** renaming the device also updates the `127.0.1.1` line of
  `/etc/hosts`. If the name is missing there (renamed some other way), sudo stalls ~10 s on every
  admin action; Settings > System then shows "Fix slow admin actions", which asks for the password
  and adds it (the old file is kept as `/etc/hosts.duo-bak`).
- **Lock screen** with an optional PIN, **alarm**, screen **dimming**, and a **calculator**.
- **Updates itself from GitHub:** Settings > Launcher > Check for updates downloads the latest
  source from this repository, builds it on the device and restarts into it (the previous
  version is kept; "Undo last update" puts it back). Needs Go at `~/.local/go`.

## Doctor: setting up a Cardputer

Settings > System > **Doctor** checks that the Cardputer has the system changes Duo relies on
and puts in what's missing (**Fix everything**, or one at a time). Changes that need root ask
for your password once. Over SSH: `~/apps/duo-launcher/app --doctor` reports, and
`app --doctor --fix` fixes (sudo asks at the terminal).

| Check | Fix | Root |
| --- | --- | --- |
| Duo is the home screen | `~/.local/bin/cardputer-home` + the APPLaunch drop-in (from the next boot) | no |
| Boot logo lets go | `fb_load.service.d/duo.conf` (no `--keep-open`) | yes |
| Terminal kept off the screen | `duo-console.service`: tty1 in graphics mode, so the text console never draws over Duo and the screen can power fully off | yes |
| Device name in /etc/hosts | the `127.0.1.1` line (sudo stops stalling ~10 s) | yes |
| Bluetooth audio allowed | WirePlumber `90-bluetooth-no-seat.conf` | no |
| Screen goes fully dark | Duo holds M5's backlight line itself (the gpio group, no root) | no |
| Go, ZeroCode helper, brightness | reported only | |

The files it installs are in `device/` (built into Duo).

## Uninstalling apps

![Uninstalling](docs/uninstall.png)

Duo works out what kind of app it is and only does what is safe for that kind:

| App | Uninstall does | Put back does |
| --- | --- | --- |
| **ZeroCode app** (`zc-<name>.desktop`, made from a folder in `~/apps`) | removes the launcher entry with the root helper `/usr/local/sbin/zc-app-root` (no password needed) and moves the folder into the bin | moves the folder back and adds the entry again |
| **Store / package app** (a dpkg package owns its `.desktop` or program) | asks for the admin password, keeps a copy of the installed package (rebuilt from dpkg's files, with its scripts and your edited settings files), then `apt-get remove` (settings kept, nothing auto-removed) | `apt-get install` of the kept copy, works offline |
| **Built-ins, Duo itself, the system launcher, essential packages** | refused: HIDE puts them away instead | |

- Before asking, it shows what goes: size, and any other package that would go with it (worked
  out from dpkg's database in a second, then double-checked with `apt-get -s` before anything
  changes; if apt disagrees, nothing is touched). It refuses if apt would remove a protected or
  essential package or install anything.
- If a package copy can't be made, it says why and asks whether to uninstall anyway.
- The bin is `~/.local/share/dslauncher/removed/<date>-<name>/` (`record.json` plus the app folder
  or the `.deb` copies). Every step is recorded first, so an uninstall or put-back cut short (power
  loss) is repaired the next time the bin is opened; a failed step is rolled back.
- Favourite pins come back with the app. Files the app keeps elsewhere (like `~/Notes`) are not
  touched.

## PictoChat

![PictoChat](docs/pictochat.png)

A chat with drawings, laid out like the handheld's: messages above, your box below at its real
size (232 x 83, ruled lines, your name tag in your favourite colour), tools on the left and
Send / Copy / Clear on the right. There is no on-screen keyboard: you type on the Cardputer's.

- **Find a chat:** PictoChat lists chats hosted on the same Wi-Fi (they announce themselves), or
  choose **Host a chat here** (four rooms of up to 16, port 47120), or **Join by address**.
- **Chat over the internet with a code:** **Start an internet chat** makes a room code (like
  `K7Q2M-9XDRT`) to share; friends pick **Join with a code**. It goes through a free public MQTT
  broker (broker.emqx.io, then broker.hivemq.com, then test.mosquitto.org if one is down), over
  TLS, so it works from any network, phone hotspots included. The broker never sees anything
  readable: the code is stretched (PBKDF2, 100 000 rounds) into an AES-256-GCM key and an
  anonymous topic, and every message, name and presence ping is encrypted with it; anyone
  without the code sees noise. Free public brokers have no uptime promise. Pure Go, standard
  library only (`mqtt.go`, `picto_mqtt.go`).
  It speaks the protocol of PictoChat Online (pict.chat), so it can also join a self-hosted
  [ayunpictojava](https://github.com/ayunami2000/ayunpictojava) server that has its captcha
  turned off; servers that want a captcha (pict.chat itself) can't be joined.
- **In a room**, Tab switches TYPE and DRAW:
  - TYPE: keys type into the lines (they wrap), Backspace erases, Left/Right pick a line,
    Up/Down scroll the messages.
  - DRAW: arrows (or F X Z C) move the pen, Space puts it down or lifts it, P pen, E eraser,
    B thick/thin, W/S scroll, Backspace undoes the last stroke.
  - Both: Enter sends, Del clears, Ctrl+R (R in DRAW) copies the message at the bottom of the
    view into your box, Esc leaves the room.

The network code (`ws.go`, `picto_net.go`) and the screens (`picto.go`) are Duo's own, written
from the protocol; no code or pictures come from ayunpictojava.

## Build and install

```sh
GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o dist/ds-launcher .
cp dist/ds-launcher pkg/app
scp pkg/* pi@DEVICE:apps/duo-launcher/
ssh pi@DEVICE '~/.zerocode/kb/bin/zc-icon-svg ~/apps/duo-launcher && ~/.zerocode/kb/bin/zc-app ~/apps/duo-launcher'
```

## Testing without touching the hardware

Scripted runs draw to a file and save a PNG after each key (sounds off, no backlight changes):

```sh
CP0_FB=fb.raw DSL_CONFIG=test-config XDG_CACHE_HOME=cache DSL_SHOTS=shots \
DSL_SIM="w3,RIGHT,6,ESC,7,ENTER,ESC,OPEN:builtin:calc,ch:2,ch:+,ch:2,ENTER" ./ds-launcher
```

Steps: key names (`RIGHT`, `ENTER`, `ESC`, `TAB`, `BACKSPACE`...), `4`–`8` for the key bar,
`ch:x` to type, `wN` to wait N tenths of a second, `OPEN:<entry key>` to open an entry,
`INFO:<entry key>` for its details; end a step with `~` (`RIGHT~`) to save 10 frames of its
animation, 40 ms apart, instead of only the end. Opening an app saves the launch animation as
`launch-25` ... `launch-100`. A run ends after its last step once background work is done;
`DSL_DATA=<dir>` keeps the bin of removed apps out of your real one.
`ds-launcher --sounds out.wav` renders every sound effect to a WAV file.

Settings live in `~/.config/dslauncher/config`; the last launched app's output in
`~/.cache/dslauncher/last-app.log`.
