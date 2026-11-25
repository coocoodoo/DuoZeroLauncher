# System tweaks on the Cardputer that Duo relies on

Every change made to the reference CardputerZero (Debian 13 trixie arm64, kernel
6.18.39+rpt-rpi-v8, M5's APPLaunch image) to make it work the way it does with Duo as the home
screen. It is written for the Doctor (`doctor.go`, Settings > System > Doctor, `app --doctor
[--fix]`), so each item says how to check it, how to fix it, and how to undo it. Surveyed on the
device on 2026-11-25 with Duo 0.7.10.

**Scope:** the launcher only (the user's call). Go for self-update, ZeroCode and its app helper,
and the shell PATH for the CLI tools are left out: don't add Doctor fixes for them.

**Legend.** *Doctor* says whether `doctor.go` covers it today. *Root* means the fix runs through
sudo. *Here* is the state on the reference device.

**Never touch, read or log:** `~/.zerocode/config.json` and its backups (API keys), any
`*credentials*.json` file in an app's config folder, and Wi-Fi
secrets in `/etc/NetworkManager/system-connections/`. Never restart `APPLaunch.service` while an
app is open: it kills that app (and any Claude session running in it).

## 1. Duo is the home screen

- **What:** a user drop-in `~/.config/systemd/user/APPLaunch.service.d/home-screen.conf`
  (`device/home-screen.conf`) replaces `ExecStart` with `~/.local/bin/cardputer-home`
  (`device/cardputer-home`, mode 755). That script runs `~/apps/duo-launcher/run.sh`, restarts
  Duo when it crashes, starts the stock launcher on exit code 10 (`exitLeave`) or after 3 quick
  failures, and honours `~/.config/cardputer-home/use-stock`.
- **Why:** without it the stock launcher starts at boot, and closing an app drops back to it.
- **Check:** the drop-in contains `cardputer-home`, and the script is executable. Robust: compare
  the installed script's sha256 with the embedded one and offer an update when they differ (the
  Doctor only checks that it exists today). Here: sha256 `959e9ceb…f502`, the same as the repo's.
- **Fix:** write both files atomically, then `systemctl --user daemon-reload`. It takes effect
  at the next boot, and it must not restart APPLaunch.
- **Undo:** delete the drop-in, then `systemctl --user daemon-reload`.
- **Doctor:** yes (`home`). **Root:** no. **Here:** in place.
- **Depends on factory setup:** tty1 autologin for pi
  (`/etc/systemd/system/getty@tty1.service.d/autologin.conf`), lingering for pi
  (`/var/lib/systemd/linger/pi`), and `APPLaunch.service` as a user unit. Worth a read-only check.

## 2. The boot logo lets go of the screen

- **What:** a root drop-in `/etc/systemd/system/fb_load.service.d/duo.conf`
  (`device/fb_load-duo.conf`) runs `/usr/sbin/fb_load` without `--keep-open`.
- **Why:** with `--keep-open`, fb_load holds the panel until a process whose `/proc/PID/exe` is
  the stock APPLaunch binary appears. With Duo as home that never happens, so the M5Stack logo
  stays over Duo.
- **Check:** the file exists. Robust: check its `ExecStart` line has no `--keep-open`.
- **Fix:** write the file, then `systemctl daemon-reload`. It takes effect at the next boot.
- **Undo:** `rm -r /etc/systemd/system/fb_load.service.d && systemctl daemon-reload`.
- **Doctor:** yes (`bootlogo`; "not used" if `/usr/sbin/fb_load` is missing). **Root:** yes.
  **Here:** in place.

## 3. The console cursor is hidden

- **What:** a root unit `/etc/systemd/system/hide-console-cursor.service`, enabled and installed
  by `device/hide-console-cursor.sh`. It writes 0 to `/sys/class/graphics/fbcon/cursor_blink`
  and sends `ESC[?25l` to tty1.
- **Why:** the Linux text console sits under every app on tty1, and its blinking cursor drew an
  8x8 black square at the bottom-left (x 0-7, y 160-167) over Duo and the apps.
- **Check:** the unit is enabled, and `cat /sys/class/graphics/fbcon/cursor_blink` gives `0`.
- **Fix:** run the script's steps: write the unit, `daemon-reload`, `enable --now`. The script
  stops if the unit already exists, so a Doctor fix has to be idempotent.
- **Undo:** `systemctl disable --now hide-console-cursor.service && rm
  /etc/systemd/system/hide-console-cursor.service && systemctl daemon-reload`.
- **Doctor:** **no**. Add it, or fold it into the `console` fix. **Root:** yes. **Here:** in
  place (enabled, active, `cursor_blink` 0). The user installed it by hand.
- **Not applied:** adding `vt.global_cursor_default=0` to `/boot/firmware/cmdline.txt` was tried
  and blocked. The file is unchanged, and a backup `cmdline.txt.before-duo` is identical to it.
  Leave the boot file alone, since a bad edit stops the device booting.

## 4. The console is kept off the screen (KD_GRAPHICS)

- **What:** a root unit `/etc/systemd/system/duo-console.service` (`device/duo-console.service`)
  switches tty1 to KD_GRAPHICS (ioctl `KDSETMODE` 0x4B3A, 1) with python3 and touches
  `/run/duo-console`.
- **Why:** in text mode (KD_TEXT), fbcon redraws the console over Duo whenever the panel is
  unblanked or a key pokes it ("I can see the terminal"). Graphics mode stops that and lets the
  screen power fully off (`DSL_PANEL_OFF`, FBIOBLANK). While the marker exists, Duo also turns
  off its corner guard, which reads the framebuffer 125 times a second (~6% CPU at idle).
- **Check:** `/run/duo-console` exists. Robust: also read the KD mode (`KDGETMODE` 0x4B3B on
  `/dev/tty1` gives 1) and check the unit is enabled.
- **Fix:** write the unit, `daemon-reload`, `enable`, then `restart`.
- **Undo:** `systemctl disable --now duo-console.service && rm
  /etc/systemd/system/duo-console.service && systemctl daemon-reload`, and switch tty1 back
  with KDSETMODE 0 (or reboot).
- **Doctor:** yes (`console`). **Root:** yes. **Here:** **not installed yet**: the unit is
  missing and the KD mode is 0. It waits for the user to run "Fix everything".

## 5. The hostname is in /etc/hosts

- **What:** add the current hostname to `/etc/hosts` (the `127.0.1.1` line), using `hostsScript`
  in `sys.go`.
- **Why:** this image lets the hostname be changed while `/etc/hosts` keeps
  `127.0.1.1 cardputerzero`. sudo then waits about 10 s on a name lookup before every admin
  action: Doctor fixes, updates, uninstalls.
- **Check:** `hostnameListed(hostname())`.
- **Fix:** root, through `hostsScript`. The name must match `hostRE`.
- **Undo:** remove the added name from the `127.0.1.1` line.
- **Doctor:** yes (`hosts`). Also Settings > System > "Fix slow admin actions". **Root:** yes.
  **Here:** **not fixed yet**: the hostname differs from the `/etc/hosts` entry.

## 6. Bluetooth audio is allowed

- **What:** a user file `~/.config/wireplumber/wireplumber.conf.d/90-bluetooth-no-seat.conf`
  (`device/90-bluetooth-no-seat.conf`) sets `monitor.bluez.seat-monitoring = disabled`.
- **Why:** the launcher runs as a background user service with no active local seat, and
  WirePlumber only turns on Bluetooth audio for a seat. Without this, pairing works but no audio
  goes to speakers or a car stereo.
- **Check:** the file exists. Robust: also check that `wpctl status` lists a bluez sink once a
  device is connected.
- **Fix:** write the file, then `systemctl --user restart wireplumber`.
- **Undo:** delete the file and restart wireplumber.
- **Doctor:** yes (`bluetooth`; "not used" without wireplumber). **Root:** no. **Here:** in
  place, with one car stereo paired and trusted.

## 7. Wi-Fi networks can be added from the launcher

- **What:** a root polkit rule `/etc/polkit-1/rules.d/49-cardputer-wifi.rules`, mode 644,
  followed by `systemctl restart polkit`:

  ```js
  polkit.addRule(function(action, subject) { if (subject.user == 'pi' && (action.id == 'org.freedesktop.NetworkManager.network-control' || action.id == 'org.freedesktop.NetworkManager.settings.modify.system')) { return polkit.Result.YES; } });
  ```

- **Why:** the launcher's session isn't an active local login, so polkit refuses to let pi add
  or join networks. The stock Wi-Fi screen shows that as "service unavailable".
- **Check:** `nmcli general permissions` as pi shows `yes` for
  `org.freedesktop.NetworkManager.network-control` and `...settings.modify.system`.
- **Fix:** write the rule and restart polkit.
- **Undo:** delete the rule and restart polkit.
- **Trade-off:** anything running as pi can change the network settings.
- **Doctor:** **no**. Add it. **Root:** yes. **Here:** in place (both `yes`).

## 8. pi's groups

- **What:** pi has to be in `video` (backlight `brightness` is `root:video` 664), `gpio`
  (`/dev/gpiochip*`, which Duo uses to hold m5ioe1 line 9 for a true black screen), `input`
  (keyboard), `audio`, and `lp`. `lp` was added on this device with `usermod -aG lp pi` for USB
  label printers (`/dev/usb/lp0`).
- **Why:** without `video`, brightness and screen-off fail. Without `gpio`, the screen glows when
  "off". Without `lp`, printing fails with permission denied.
- **Check:** `id -nG pi`. The Doctor already checks `backlight` by trying to open `brightness`
  for writing (info only), and `blackout` through `backlightLineStatus()`.
- **Fix:** `usermod -aG <group> pi`. It counts only after the next login or reboot, so say so.
- **Undo:** `gpasswd -d pi <group>`.
- **Doctor:** partial (info only). **Root:** yes. **Here:** in all of them, plus `i2c`, `spi`,
  `render`, `netdev`, `plugdev`, `dialout` and `sudo` from the image.

## 9. Factory setup that Duo needs, and isn't ours

These come with M5's image or the `applaunch` package. Check them read-only and report; don't
"fix" them:

- tty1 autologin for pi, and lingering for pi (both already listed under item 1).
- PipeWire with pipewire-pulse at `/run/user/1000/pulse/native`. Duo's sound is `pacat` at 32 kHz
  s16le mono.
- The ADB gadget: `/etc/systemd/system/adbd.service.d/cardputer.conf` and
  `cardputer-adb-hotplug.service`, from applaunch's `/usr/share/APPLaunch/adb/` scripts. ADB
  works only with the USB-SW switch to the LEFT.
- The udev rules in `/etc/udev/rules.d/99-cardputer-zero-*.rules` (keyboard at
  `/dev/input/cardputer-zero-internal`, LCD).
- The backlight: `m5stack,pwm-backlight` (`/sys/class/backlight/backlight`) on PWM channel 3 of
  the m5ioe1 I2C expander (i2c 1-004f). Brightness 0 still glows unless m5ioe1 line 9 is held
  low. Duo holds it while running (`backlight_linux.go`), as M5's launcher does at boot.
- `/etc/X11/xorg.conf.d/99-v3d.conf` and `/etc/default/keyboard` changed recently but aren't
  Duo's.

## 10. Left behind on purpose

- `~/hide-console-cursor.sh`, the script the user ran with sudo for item 3.
- `~/.local/bin/cardputer-home.prev`, the previous home script.
- `~/apps/duo-launcher/{app.prev, src.prev}`, the undo for the last update.
- The test package `duo-uninstall-test` from the 0.6.4 uninstall tests was purged. Nothing is
  left of it.

## Making the Doctor robust

These follow from the list above:

1. **Cover every item:** add the missing fixes (3 cursor, 7 Wi-Fi polkit, 8 groups), and
   read-only checks for item 9.
2. **Check content, not just existence:** compare installed files with the embedded copies by
   sha256, and offer to update when they differ. Check that units are enabled and active, and
   read the KD mode.
3. **Make every fix idempotent, with a backup:** before replacing a file, keep a
   `<file>.duo-bak` and record it, so every fix has an Undo.
4. **Verify after fixing:** re-run the checks and show which fixes worked, which didn't, and
   what only counts from the next login or boot (groups, boot logo, home screen).
5. **Ask once:** one sudo prompt for all root fixes in one script, as now, with a timeout and no
   password in logs or argv.
6. **Keep a log:** a log the user can share (`~/.cache/dslauncher/doctor.log`), without secrets.
7. **Stay safe:** never restart APPLaunch, never edit `/boot/firmware/cmdline.txt`, and never
   read the secret files above.
