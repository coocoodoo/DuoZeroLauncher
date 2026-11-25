# Duo Launcher

A home screen for the M5Stack Cardputer Zero in the style of the DS system menu, built on the
textures and fonts of [ds.css](https://github.com/spiritov/ds.css) (MIT, by spiritov): the grid
paper, buttons and bars blended over a favourite colour the way its CSS does, the info header,
the orange alert with the loading squares, the striped input field, PictoChat frames, the pixel
font and the clock-digit font. The left "upper screen" shows the square clock (Tab: the calendar),
the right "touch screen" the apps. Sounds come from the Nintendo DSi UI Sounds pack (by
Helljumper; `tools/mksounds.py` converts it into `assets/sounds/`), mixed so they can overlap.

`tools/mkassets.py <ds.css-main/src/assets>` halves the textures to native pixels into `assets/`
(embedded in the binary) and pre-renders the fonts into `fontdata.go`. The clock font is under the
FontStruct Non-Commercial License (personal use).

For now it runs as an app from the system launcher (APPLaunch). Taking over as the device's
launcher is the next phase.

![Screens](docs/screens.png)

## What it does

- **Home:** every app the system launcher lists (its `.desktop` folder), plus built-ins: Store,
  CLI, Python, SSH, IP Panel, Calculator, Settings. Arrows move (Up/Down jump 5), keys 4–8:
  OPEN, FAVE (pin to the front), INFO (details, hide), SETTINGS, LOCK. Tab shows the calendar;
  hold Tab anywhere to lock and turn the screen off (sleep). Leave via Settings > Launcher.
- **Launching:** hands the screen and keyboard to the app and takes them back when it exits.
  CLI, Python and SSH open in Tiny Terminal (through a `$SHELL` wrapper).
- **Settings:** Screen (brightness, dim/off timers, lock on wake), Speaker (volume, mute, menu
  sounds), Wi-Fi (on/off, scan and join, hidden networks, saved networks), Ethernet, Bluetooth
  (power, visibility, name, paired devices, pairing), Ports (Ext/Grove 5V), Battery, Launcher (sort,
  show/hide apps, favourites), Developer (ADB, SSH server, last app's output), User (name, favourite
  colour, birthday, message, PIN, device name), Clock (24-hour, network time, set time, time zone,
  alarm), System (versions, memory, storage, licences, restart, power off). Admin actions ask for
  the account password on the device (kept in memory for 10 minutes).
- **Lock screen** with an optional PIN, **alarm**, screen **dimming**, and a **calculator**.
- **Updates itself from GitHub:** Settings > Launcher > Check for updates downloads the latest
  source from this repository, builds it on the device and restarts into it (the previous
  version is kept; "Undo last update" puts it back). Needs Go at `~/.local/go`.

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
`ch:x` to type, `wN` to wait N tenths of a second, `OPEN:<entry key>` to open an entry.
`ds-launcher --sounds out.wav` renders every sound effect to a WAV file.

Settings live in `~/.config/dslauncher/config`; the last launched app's output in
`~/.cache/dslauncher/last-app.log`.
