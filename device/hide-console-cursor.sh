#!/bin/sh
# Run as root: hide the Linux console cursor on the LCD at every boot, and right now.
# Undo: systemctl disable --now hide-console-cursor.service && rm /etc/systemd/system/hide-console-cursor.service && systemctl daemon-reload
set -eu
u=/etc/systemd/system/hide-console-cursor.service
[ -e "$u" ] && { echo "STOPPED: $u already exists"; exit 1; }
cat > "$u" <<'EOF'
[Unit]
Description=Hide the console cursor on the LCD (Duo Launcher is drawn over the console)
After=systemd-vconsole-setup.service fb_load.service
ConditionPathExists=/dev/tty1

[Service]
Type=oneshot
RemainAfterExit=yes
ExecStart=/bin/sh -c 'echo 0 > /sys/class/graphics/fbcon/cursor_blink; printf "\\033[?25l" > /dev/tty1'

[Install]
WantedBy=multi-user.target
EOF
chmod 644 "$u"
systemctl daemon-reload
systemctl enable --now hide-console-cursor.service
echo "active: $(systemctl is-active hide-console-cursor.service)  enabled: $(systemctl is-enabled hide-console-cursor.service)"
echo "cursor_blink: $(cat /sys/class/graphics/fbcon/cursor_blink)"
