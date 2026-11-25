# Duo Launcher: install guide for an AI agent

You are installing **Duo Launcher** (a DS-style home screen, Go, static arm64 binary) on an
**M5Stack Cardputer Zero** for its owner. You have a shell on the device as user `pi` (for example
ZeroCode on the device, or an agent connected over SSH or ADB). Follow the steps in order, stop at
the first failure, and report back with the template at the end.

## What is in this package

```
duo-launcher/
  app            the launcher (static ELF, linux/arm64)
  app.conf       launcher entry: NAME, SLUG=duo-launcher, EXEC=run.sh, ICON=icon.png
  run.sh         starts ./app from its own folder
  icon.svg       launcher icon (rasterized on the device)
  install.sh     installer (copies, keeps a backup, adds to the launcher, self-checks)
  INSTALL.txt    the same steps for a person
  AGENT-INSTALL.md   this file
```

## Device facts you can rely on

- Home screen: `APPLaunch` runs as a **systemd user service** of `pi`. Launcher entries live in
  `/usr/share/APPLaunch/applications/*.desktop` (root-owned).
- Apps made on this device live in `~/apps/<slug>/` and are added to the launcher with
  `~/.zerocode/kb/bin/zc-app <dir>`, which calls a root helper allowed by a sudoers rule
  (`/usr/local/sbin/zc-app-root`, NOPASSWD). That is the **only** privileged step needed.
- `~/.zerocode/kb/bin/zc-icon-svg <dir>` turns `icon.svg` into a 100x100 `icon.png`.
- Screen 320x170 RGB565 at `/dev/fb_lcd`; keyboard at `/dev/input/cardputer-zero-internal`.
- `/tmp` is a small tmpfs (about 177 MB); use `~/tmp` for anything large.

## Rules

1. **No sudo** beyond what `zc-app` does for you. Never ask for, type or store the owner's password.
2. **Do not stop, restart or edit `APPLaunch`** or anything under `/usr/share/APPLaunch` by hand.
3. **Do not touch the owner's settings** in `~/.config/dslauncher/`. Point tests elsewhere with
   `DSL_CONFIG` and `XDG_CACHE_HOME` (see step 4).
4. **No sound tests through `pacat` with a volume flag.** The sound server remembers a volume per
   client name; a muted test stream would silence the launcher later. Scripted runs (step 4) are
   silent on their own.
5. **Do not draw on the real screen while the owner is using it.** Tests use a virtual screen file.
6. After any test, make sure no test copy is left running (step 5).
7. Keep the backup (`~/apps/duo-launcher/app.prev`) so the owner can roll back.

## Step 1: check the device

```sh
uname -m                                  # expect: aarch64
df -h ~ | tail -1                         # need about 10 MB free
ls ~/.zerocode/kb/bin/zc-app ~/.zerocode/kb/bin/zc-icon-svg
sudo -n -l 2>/dev/null | grep zc-app-root # expect a NOPASSWD line (no password prompt)
```

If `zc-app` or its sudo rule is missing, install to `~/apps/duo-launcher` anyway (step 3 does), and
tell the owner it can be started with `~/apps/duo-launcher/run.sh` but is not on the launcher yet.

## Step 2: unpack

```sh
cd ~
unzip -o duo-launcher-install.zip          # or: python3 -m zipfile -e duo-launcher-install.zip .
ls -l duo-launcher/                        # app, app.conf, run.sh, icon.svg, install.sh ...
file duo-launcher/app 2>/dev/null || head -c 4 duo-launcher/app | od -c   # an ELF for aarch64
```

## Step 3: install

```sh
sh ~/duo-launcher/install.sh
```

It copies the files into `~/apps/duo-launcher/` (previous `app` kept as `app.prev`), makes the
icon, runs `zc-app` to add **Duo Launcher** to the launcher, then runs a self-check
(`app --sounds /tmp/duo-check.wav`, which only writes a file). Expected last line:
`Duo Launcher installed. Open it from the launcher ...`

Confirm the launcher entry:

```sh
ls -l /usr/share/APPLaunch/applications/zc-duo-launcher.desktop /usr/share/APPLaunch/bin/zc-duo-launcher
```

## Step 4: verify without touching the real screen

Run a scripted session on a virtual screen with throwaway settings. It saves a PNG after each key.

```sh
mkdir -p ~/tmp/duo-test && cd ~/tmp/duo-test
head -c 108800 /dev/zero > fb.raw          # 320 x 170 x 2 bytes
CP0_FB=$PWD/fb.raw DSL_CONFIG=$PWD/config XDG_CACHE_HOME=$PWD/cache DSL_SHOTS=$PWD/shots \
DSL_SIM="w3,RIGHT,RIGHT,TAB,TAB,7,RIGHT,ENTER,ESC,ESC,OPEN:builtin:calc,ch:6,ch:*,ch:7,ENTER,ESC" \
timeout 60 ~/apps/duo-launcher/app; echo "exit=$?"
ls shots | wc -l                           # expect 17 screenshots and exit=0
```

Script steps: key names (`RIGHT`, `ENTER`, `ESC`; `TAB` is a tap = press + release, two screenshots;
`TABHOLD` holds it to lock), `4`-`8` for the key bar, `ch:x` types a
character, `wN` waits N tenths of a second, `OPEN:<key>` opens an entry (`builtin:calc`).
Look at a few PNGs if you can view images (home screen, settings grid, calculator showing 42).

## Step 5: clean up

```sh
pgrep -fa "[d]uo-launcher/app" || echo "no test copy running"
rm -rf ~/tmp/duo-test ~/duo-launcher /tmp/duo-check.wav
```

Keep `~/duo-launcher-install.zip` unless the owner asks you to remove it.

## Roll back or uninstall

```sh
cp ~/apps/duo-launcher/app.prev ~/apps/duo-launcher/app       # previous build
~/.zerocode/kb/bin/zc-app remove duo-launcher                  # take it off the launcher
rm -rf ~/apps/duo-launcher                                     # remove the app
# the owner's settings: ~/.config/dslauncher  (ask before deleting)
```

## Report back

```
Duo Launcher install
- device check: <aarch64 / space / zc-app + sudo rule present?>
- install.sh last line: <...>
- launcher entry: <present / missing>
- scripted test: <exit code, number of screenshots>
- cleanup: <no test copies running>
- notes: <anything unexpected>
```

The owner opens it from the launcher; hold Esc on its home screen to go back to the system launcher.
