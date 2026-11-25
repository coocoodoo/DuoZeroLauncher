#!/bin/sh
# Install or update Duo Launcher on the Cardputer Zero (run on the device as pi):
#   sh install.sh
set -e
here="$(cd "$(dirname "$0")" && pwd)"
dest="$HOME/apps/duo-launcher"
mkdir -p "$dest"

# keep the previous build so it can be put back
if [ -f "$dest/app" ]; then
    cp "$dest/app" "$dest/app.prev"
    echo "previous build kept as $dest/app.prev"
fi
for f in app app.conf run.sh icon.svg; do
    cp "$here/$f" "$dest/$f"
done
chmod +x "$dest/app" "$dest/run.sh"

# launcher icon from the SVG (keeps the existing icon.png if the tool is missing)
icon_tool="$HOME/.zerocode/kb/bin/zc-icon-svg"
if [ -x "$icon_tool" ]; then
    "$icon_tool" "$dest" >/dev/null 2>&1 || echo "icon: kept the existing icon.png"
fi
if [ ! -f "$dest/icon.png" ] && [ -f "$here/icon.png" ]; then
    cp "$here/icon.png" "$dest/icon.png"
fi

# put it on the launcher (needs the ZeroCode launcher helper, already set up on this device)
app_tool="$HOME/.zerocode/kb/bin/zc-app"
if [ -x "$app_tool" ]; then
    "$app_tool" "$dest"
else
    echo "zc-app not found: Duo Launcher is installed in $dest but not added to the launcher."
    echo "Run it from a terminal with: $dest/run.sh"
fi

# quick check that the binary runs on this device
if "$dest/app" --sounds /tmp/duo-check.wav >/dev/null 2>&1; then
    rm -f /tmp/duo-check.wav
    echo "Duo Launcher installed. Open it from the launcher (it may take a few seconds to appear)."
else
    echo "Warning: the app did not start its self-check. Put the old build back with:"
    echo "  cp $dest/app.prev $dest/app"
    exit 1
fi
