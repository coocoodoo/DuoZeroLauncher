# Duo Launcher on the Cardputer: status report

Date: 2026-11-25. Device: CardputerZero-307a74b9 (Debian 13 aarch64, 414 MB RAM, 320x170 LCD).

## Current state (checked on the device)

| What | State |
|---|---|
| Duo Launcher | **0.5.0** installed and running as the home screen (app sha256 `13c9529f40ee2c3e…`) |
| Backup build | `~/apps/duo-launcher/app.prev` = 0.4.0 (`9c07248a62dcb499…`) |
| Source on device | `~/apps/duo-launcher/src`, 0.5.0, LF line endings, identical to this zip (26 text files checked) |
| Boot | M5Stack logo, then Duo. Confirmed by the user after a reboot |
| Hold Tab | Locks and turns the screen off. Confirmed by the user on hardware |
| Console cursor | Hidden by `hide-console-cursor.service` (active, enabled, cursor_blink=0). Installed by the user |

## How Duo is the home screen

1. `~/.config/systemd/user/APPLaunch.service.d/home-screen.conf` (user-level) points the stock
   `APPLaunch.service` at `~/.local/bin/cardputer-home`.
2. `cardputer-home` runs Duo. Exit code 10 (Settings > Launcher > "Back to the system launcher")
   starts the stock launcher. Any other exit restarts Duo, and 3 quick failures fall back to the
   stock launcher.
3. Holding Esc no longer quits Duo (0.4.0). Before, holding Esc to close an app also quit Duo.

Files for all of this are in `device/` in the zip.

## Fixed today

| Version | Change |
|---|---|
| 0.3.1 | Bottom-left console-cursor corner repaired by Duo (`guardCorner`); held-key sounds no longer pile up (mixer paced 60 ms ahead) |
| 0.3.2–0.3.4 | Calendar and clock redrawn to match the DS reference: month plate, dark frame, today in red with a 2-px dithered blue-grey border, 6x6 clock grid, time plate |
| 0.3.5–0.3.6 | DS pixel-art battery: green >50%, yellow 21–50%, red ≤20%, grey unknown |
| 0.4.0 | Duo is the default launcher (no Esc-hold quit; restart on crash) |
| 0.5.0 | From the other agent: hold Tab to lock and sleep the screen; Tab acts on release |

### Stuck M5Stack splash at boot

- **Cause:** root `fb_load.service` (`/usr/sbin/fb_load --keep-open`) shows the logo through DRM
  and holds the panel until a process whose `/proc/PID/exe` is the stock APPLaunch binary
  appears. Duo never matches, so it drew to fb0 behind the logo. It was not a framebuffer
  detection problem in `cp0`, and the proposed `cp0` patch was not applied.
- **Fix:** `/etc/systemd/system/fb_load.service.d/duo.conf` runs `fb_load` without
  `--keep-open` (`device/fb_load-duo.conf`). Undo: remove that folder and `systemctl daemon-reload`.

### Console cursor still blinking in the corner

- **Cause:** fbcon sends each cursor blink to the panel at once, so Duo's 8 ms repair can't
  always hide it.
- **Fix:** `device/hide-console-cursor.sh` (run once with sudo) installs
  `hide-console-cursor.service`, which sets `cursor_blink` to 0 and hides the tty1 cursor at boot.

## Notes for the next change

- Base patches on the source in this zip (0.5.0). `main.go` used to have CRLF endings from a
  Windows-side edit. It is LF now.
- Never restart `APPLaunch.service` from inside Duo or an app it started. Install the new build
  (`app.prev` kept) and have the user reboot.
- Root system changes are blocked for the Windows-side agent. Give the user a script to run with
  sudo over SSH.
- Exchange changes as unified diffs (`a/` and `b/` paths) plus the sha256 of each changed file.
