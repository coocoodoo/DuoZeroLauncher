package main

// System status and actions. Everything runs as the logged-in user with the tools the system
// launcher uses (nmcli, bluetoothctl, wpctl, timedatectl, /sys); actions that need an
// administrator go through sudo with the password typed on the device.

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

const sysRoot = "/sys/class"

func run(timeout time.Duration, name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	err := cmd.Run()
	if ctx.Err() != nil {
		err = errors.New("timed out")
	}
	return strings.TrimSpace(out.String()), err
}

// runSudo runs a command as root with the user's password.
func runSudo(password string, timeout time.Duration, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "sudo", append([]string{"-S", "-k", "-p", "", "--"}, args...)...)
	cmd.Stdin = strings.NewReader(password + "\n")
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	err := cmd.Run()
	text := strings.TrimSpace(out.String())
	if err != nil && strings.Contains(text, "incorrect password") || strings.Contains(text, "Sorry, try again") {
		return text, errors.New("wrong password")
	}
	return text, err
}

func readTrim(p string) string {
	b, err := os.ReadFile(p)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

func readInt(p string) (int, bool) {
	v, err := strconv.Atoi(readTrim(p))
	return v, err == nil
}

// ---- live status, refreshed in the background ----

type Status struct {
	mu        sync.Mutex
	Battery   int // percent, -1 unknown
	Charging  bool
	WifiOn    bool
	WifiLevel int // 0..4
	SSID      string
	IP        string
	BTOn      bool
}

func (s *Status) snapshot() Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	return Status{Battery: s.Battery, Charging: s.Charging, WifiOn: s.WifiOn, WifiLevel: s.WifiLevel,
		SSID: s.SSID, IP: s.IP, BTOn: s.BTOn}
}

func batteryDir() string {
	m, _ := filepath.Glob(filepath.Join(sysRoot, "power_supply", "*"))
	for _, d := range m {
		if readTrim(filepath.Join(d, "type")) == "Battery" {
			return d
		}
	}
	return ""
}

func (s *Status) refresh(slow bool) {
	bat, charging := -1, false
	if d := batteryDir(); d != "" {
		if v, ok := readInt(filepath.Join(d, "capacity")); ok {
			bat = v
		}
		st := readTrim(filepath.Join(d, "status"))
		charging = st == "Charging" || st == "Full"
	}
	level, wifiOn := 0, false
	if b, err := os.ReadFile("/proc/net/wireless"); err == nil {
		for _, l := range strings.Split(string(b), "\n") {
			f := strings.Fields(l)
			if len(f) >= 3 && strings.HasPrefix(f[0], "wl") {
				wifiOn = true
				q, _ := strconv.ParseFloat(strings.TrimSuffix(f[2], "."), 64)
				switch {
				case q >= 55:
					level = 4
				case q >= 40:
					level = 3
				case q >= 25:
					level = 2
				case q > 0:
					level = 1
				}
			}
		}
	}
	ip := primaryIP()
	s.mu.Lock()
	if v := os.Getenv("DSL_BATTERY"); v != "" { // scripted tests: a pretend charge level
		if n, err := strconv.Atoi(v); err == nil {
			bat, charging = n, false
		}
	}
	s.Battery, s.Charging, s.WifiLevel, s.IP = bat, charging, level, ip
	if wifiOn {
		s.WifiOn = true
	}
	s.mu.Unlock()
	if slow {
		on := wifiRadio()
		ssid := ""
		if out, err := run(4*time.Second, "nmcli", "-t", "-f", "ACTIVE,SSID", "dev", "wifi"); err == nil {
			for _, l := range strings.Split(out, "\n") {
				if a, b, ok := strings.Cut(l, ":"); ok && a == "yes" {
					ssid = unescapeNM(b)
				}
			}
		}
		bt := btShow()["Powered"] == "yes"
		s.mu.Lock()
		s.WifiOn, s.SSID, s.BTOn = on, ssid, bt
		s.mu.Unlock()
	}
}

func primaryIP() string {
	ifs, _ := net.Interfaces()
	for _, want := range []string{"wlan", "eth", "usb", ""} {
		for _, i := range ifs {
			if i.Flags&net.FlagUp == 0 || i.Flags&net.FlagLoopback != 0 || !strings.HasPrefix(i.Name, want) {
				continue
			}
			addrs, _ := i.Addrs()
			for _, a := range addrs {
				if n, ok := a.(*net.IPNet); ok && n.IP.To4() != nil {
					return n.IP.String()
				}
			}
		}
	}
	return ""
}

type IfInfo struct{ Name, IP, MAC string }

func interfaces() []IfInfo {
	var out []IfInfo
	ifs, _ := net.Interfaces()
	for _, i := range ifs {
		if i.Flags&net.FlagLoopback != 0 {
			continue
		}
		info := IfInfo{Name: i.Name, MAC: i.HardwareAddr.String()}
		addrs, _ := i.Addrs()
		for _, a := range addrs {
			if n, ok := a.(*net.IPNet); ok && n.IP.To4() != nil {
				info.IP = n.IP.String()
			}
		}
		if i.Flags&net.FlagUp == 0 {
			info.IP = "down"
		} else if info.IP == "" {
			info.IP = "no address"
		}
		out = append(out, info)
	}
	return out
}

// ---- screen ----

func backlight() (cur, maxv int) {
	cur, _ = readInt(filepath.Join(sysRoot, "backlight/backlight/brightness"))
	maxv, _ = readInt(filepath.Join(sysRoot, "backlight/backlight/max_brightness"))
	if maxv <= 0 {
		maxv = 255
	}
	return cur, maxv
}

func setBacklight(v int) error {
	_, maxv := backlight()
	v = max(0, min(v, maxv))
	return os.WriteFile(filepath.Join(sysRoot, "backlight/backlight/brightness"), []byte(strconv.Itoa(v)), 0)
}

// ---- sound ----

func volume() (pct int, muted bool, ok bool) {
	out, err := run(3*time.Second, "wpctl", "get-volume", "@DEFAULT_AUDIO_SINK@")
	if err != nil {
		return 0, false, false
	}
	f := strings.Fields(out) // Volume: 0.80 [MUTED]
	if len(f) < 2 {
		return 0, false, false
	}
	v, _ := strconv.ParseFloat(f[1], 64)
	return int(v*100 + 0.5), strings.Contains(out, "MUTED"), true
}

func setVolume(pct int) error {
	pct = max(0, min(pct, 100))
	_, err := run(3*time.Second, "wpctl", "set-volume", "@DEFAULT_AUDIO_SINK@", fmt.Sprintf("%.2f", float64(pct)/100))
	return err
}

func setMute(on bool) error {
	_, err := run(3*time.Second, "wpctl", "set-mute", "@DEFAULT_AUDIO_SINK@", map[bool]string{true: "1", false: "0"}[on])
	return err
}

// ---- Wi-Fi ----

func wifiRadio() bool {
	out, _ := run(3*time.Second, "nmcli", "radio", "wifi")
	return strings.TrimSpace(out) == "enabled"
}

func setWifiRadio(on bool) error {
	_, err := run(8*time.Second, "nmcli", "radio", "wifi", map[bool]string{true: "on", false: "off"}[on])
	return err
}

type Network struct {
	SSID     string
	Signal   int
	Secure   bool
	InUse    bool
	Saved    bool
	Security string
	Conn     string // the saved profile for this network, if any
}

// unescapeNM undoes nmcli's terse escaping of ':' and '\'.
func unescapeNM(s string) string {
	return strings.NewReplacer(`\:`, ":", `\\`, `\`).Replace(s)
}

// splitNM splits a terse nmcli line on unescaped colons.
func splitNM(l string) []string {
	var out []string
	var cur strings.Builder
	esc := false
	for _, r := range l {
		switch {
		case esc:
			cur.WriteRune(r)
			esc = false
		case r == '\\':
			esc = true
		case r == ':':
			out = append(out, cur.String())
			cur.Reset()
		default:
			cur.WriteRune(r)
		}
	}
	return append(out, cur.String())
}

func savedConnections() map[string]bool {
	saved := map[string]bool{}
	out, _ := run(4*time.Second, "nmcli", "-t", "-f", "NAME,TYPE", "connection", "show")
	for _, l := range strings.Split(out, "\n") {
		f := splitNM(l)
		if len(f) >= 2 && strings.Contains(f[1], "wireless") {
			saved[f[0]] = true
		}
	}
	return saved
}

// savedSSIDs maps each saved network's SSID to its profile name, which can differ (Raspberry Pi
// OS calls the network set up when the card was made "preconfigured"). Duo Link's own Wi-Fi is
// left out.
func savedSSIDs() map[string]string {
	m := map[string]string{}
	for name := range savedConnections() {
		if name == hotspotConn {
			continue
		}
		ssid := name
		if out, err := run(4*time.Second, "nmcli", "-g", "802-11-wireless.ssid", "connection", "show", "id", name); err == nil {
			if s := strings.TrimSpace(out); s != "" {
				ssid = unescapeNM(s)
			}
		}
		m[ssid] = name
	}
	return m
}

// activeConnections: the profiles up right now, by name.
func activeConnections() map[string]bool {
	act := map[string]bool{}
	out, _ := run(4*time.Second, "nmcli", "-t", "-f", "NAME", "connection", "show", "--active")
	for _, l := range strings.Split(out, "\n") {
		if l = unescapeNM(strings.TrimSpace(l)); l != "" {
			act[l] = true
		}
	}
	return act
}

func scanWifi() ([]Network, error) {
	out, err := run(20*time.Second, "nmcli", "-t", "-f", "IN-USE,SSID,SIGNAL,SECURITY", "dev", "wifi", "list", "--rescan", "yes")
	if err != nil { // a scan was refused (too soon after the last one, say): the last results
		out, err = run(20*time.Second, "nmcli", "-t", "-f", "IN-USE,SSID,SIGNAL,SECURITY", "dev", "wifi", "list", "--rescan", "auto")
	}
	if err != nil {
		return nil, errors.New(firstLine(out, err))
	}
	saved := savedSSIDs()
	seen := map[string]int{}
	var nets []Network
	for _, l := range strings.Split(out, "\n") {
		f := splitNM(l)
		if len(f) < 4 || f[1] == "" {
			continue
		}
		sig, _ := strconv.Atoi(f[2])
		n := Network{SSID: f[1], Signal: sig, InUse: f[0] == "*", Security: f[3],
			Secure: f[3] != "" && f[3] != "--", Conn: saved[f[1]]}
		n.Saved = n.Conn != ""
		if i, ok := seen[n.SSID]; ok {
			if n.Signal > nets[i].Signal || n.InUse {
				n.InUse = n.InUse || nets[i].InUse
				nets[i] = n
			}
			continue
		}
		seen[n.SSID] = len(nets)
		nets = append(nets, n)
	}
	return nets, nil
}

func connectWifi(ssid, password string, hidden bool) error {
	args := []string{"dev", "wifi", "connect", ssid}
	if password != "" {
		args = append(args, "password", password)
	}
	if hidden {
		args = append(args, "hidden", "yes")
	}
	out, err := run(45*time.Second, "nmcli", args...)
	if err != nil {
		return errors.New(firstLine(out, err))
	}
	return nil
}

// connectSaved brings up a saved profile by its name.
func connectSaved(name string) error {
	out, err := run(45*time.Second, "nmcli", "connection", "up", "id", name)
	if err != nil {
		return errors.New(firstLine(out, err))
	}
	return nil
}

func forgetWifi(ssid string) error {
	out, err := run(8*time.Second, "nmcli", "connection", "delete", "id", ssid)
	if err != nil {
		return errors.New(firstLine(out, err))
	}
	return nil
}

func firstLine(out string, err error) string {
	out = strings.TrimSpace(out)
	if out == "" {
		return err.Error()
	}
	l, _, _ := strings.Cut(out, "\n")
	l = strings.TrimPrefix(l, "Error: ")
	return l
}

// ---- Ethernet ----

type Device struct{ Name, Type, State, Conn string }

func netDevices() []Device {
	out, _ := run(4*time.Second, "nmcli", "-t", "-f", "DEVICE,TYPE,STATE,CONNECTION", "device")
	var ds []Device
	for _, l := range strings.Split(out, "\n") {
		f := splitNM(l)
		if len(f) >= 4 {
			ds = append(ds, Device{f[0], f[1], f[2], f[3]})
		}
	}
	return ds
}

func setDevice(name string, on bool) error {
	verb := map[bool]string{true: "connect", false: "disconnect"}[on]
	out, err := run(20*time.Second, "nmcli", "device", verb, name)
	if err != nil {
		return errors.New(firstLine(out, err))
	}
	return nil
}

// ---- Bluetooth ----

func btShow() map[string]string {
	out, err := run(3*time.Second, "bluetoothctl", "show")
	m := map[string]string{}
	if err != nil {
		return m
	}
	for _, l := range strings.Split(out, "\n") {
		k, v, ok := strings.Cut(strings.TrimSpace(l), ":")
		if ok {
			if _, dup := m[k]; !dup {
				m[k] = strings.TrimSpace(v)
			}
		}
	}
	return m
}

func bt(args ...string) error {
	out, err := run(12*time.Second, "bluetoothctl", args...)
	if err != nil {
		return errors.New(firstLine(out, err))
	}
	if strings.Contains(out, "Failed") || strings.Contains(out, "not available") {
		return errors.New(firstLine(out, errors.New("failed")))
	}
	return nil
}

type BTDevice struct {
	MAC, Name string
	Connected bool
	Paired    bool
}

func btDevices(filter string) []BTDevice {
	args := []string{"devices"}
	if filter != "" {
		args = append(args, filter)
	}
	out, _ := run(4*time.Second, "bluetoothctl", args...)
	var ds []BTDevice
	for _, l := range strings.Split(out, "\n") {
		f := strings.Fields(l)
		if len(f) >= 2 && f[0] == "Device" {
			d := BTDevice{MAC: f[1], Name: strings.Join(f[2:], " "), Paired: filter == "Paired"}
			if d.Name == "" {
				d.Name = d.MAC
			}
			ds = append(ds, d)
		}
	}
	return ds
}

func btConnected(mac string) bool {
	out, _ := run(3*time.Second, "bluetoothctl", "info", mac)
	return strings.Contains(out, "Connected: yes")
}

// ---- external 5V ports ----

func portOn(name string) (bool, bool) {
	v, ok := readInt(filepath.Join(sysRoot, "leds", name, "brightness"))
	return v > 0, ok
}

func setPort(name string, on bool) error {
	return os.WriteFile(filepath.Join(sysRoot, "leds", name, "brightness"), []byte(strconv.Itoa(btoi(on))), 0)
}

// ---- battery ----

func batteryInfo() [][2]string {
	d := batteryDir()
	if d == "" {
		return [][2]string{{"Battery", "not found"}}
	}
	get := func(k string) string { return readTrim(filepath.Join(d, k)) }
	num := func(k string, div float64, unit string, prec int) string {
		v, err := strconv.ParseFloat(get(k), 64)
		if err != nil {
			return "--"
		}
		return strconv.FormatFloat(v/div, 'f', prec, 64) + unit
	}
	rows := [][2]string{
		{"Charge", get("capacity") + "%"},
		{"Status", get("status")},
		{"Health", get("health")},
		{"Voltage", num("voltage_now", 1e6, " V", 2)},
		{"Current", num("current_now", 1e3, " mA", 0)},
		{"Charge now", num("charge_now", 1e3, " mAh", 0)},
		{"Full charge", num("charge_full", 1e3, " mAh", 0)},
		{"Design", num("charge_full_design", 1e3, " mAh", 0)},
		{"Cycles", get("cycle_count")},
		{"Type", get("technology")},
	}
	if t := get("temp"); t != "" {
		rows = append(rows, [2]string{"Temperature", num("temp", 10, " C", 1)})
	}
	return rows
}

// ---- developer ----

func adbActive() bool {
	out, _ := run(3*time.Second, "systemctl", "is-active", "adbd")
	return strings.TrimSpace(out) == "active"
}

// ---- date and time ----

func timeInfo() map[string]string {
	out, _ := run(3*time.Second, "timedatectl", "show")
	m := map[string]string{}
	for _, l := range strings.Split(out, "\n") {
		if k, v, ok := strings.Cut(l, "="); ok {
			m[k] = v
		}
	}
	return m
}

func timezones() []string {
	out, err := run(5*time.Second, "timedatectl", "list-timezones")
	if err != nil {
		return nil
	}
	return strings.Split(out, "\n")
}

// ---- system ----

func osName() string {
	b, _ := os.ReadFile("/etc/os-release")
	for _, l := range strings.Split(string(b), "\n") {
		if v, ok := strings.CutPrefix(l, "PRETTY_NAME="); ok {
			return strings.Trim(v, `"`)
		}
	}
	return "Linux"
}

func hostname() string { h, _ := os.Hostname(); return h }

// hostsFile is /etc/hosts (a var for tests).
var hostsFile = "/etc/hosts"

var hostRE = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?$`)

// hostnameListed reports whether /etc/hosts names this device. Without it every sudo looks the
// name up on the network first and can stall ~10 s ("unable to resolve host").
func hostnameListed(name string) bool {
	if name == "" || strings.EqualFold(name, "localhost") {
		return true
	}
	b, err := os.ReadFile(hostsFile)
	if err != nil {
		return true // can't tell: don't offer a fix
	}
	for _, l := range strings.Split(string(b), "\n") {
		l, _, _ = strings.Cut(l, "#")
		f := strings.Fields(l)
		for i := 1; i < len(f); i++ {
			if strings.EqualFold(f[i], name) {
				return true
			}
		}
	}
	return false
}

// hostsScript points the 127.0.1.1 line of a hosts file at a device name (Debian's convention),
// adding the line if there is none, after keeping a copy in <file>.duo-bak. Run as root:
// $1 = name, $2 = the hosts file.
const hostsScript = `set -eu
name=$1; f=$2
case "$name" in ""|-*|*[!A-Za-z0-9-]*) echo "bad device name"; exit 1 ;; esac
cp -p "$f" "$f.duo-bak"
tmp=$(mktemp "$f.XXXXXX")
if grep -q '^127\.0\.1\.1[[:space:]]' "$f"; then
  sed "s/^127\.0\.1\.1[[:space:]].*/127.0.1.1\t$name/" "$f" > "$tmp"
else
  cat "$f" > "$tmp"
  printf '127.0.1.1\t%s\n' "$name" >> "$tmp"
fi
chmod 644 "$tmp"
mv -f "$tmp" "$f"
`

// fixHosts makes /etc/hosts name the device, with the admin password.
func fixHosts(pw, name string) error {
	if !hostRE.MatchString(name) {
		return errors.New("bad device name")
	}
	out, err := runSudo(pw, 60*time.Second, "sh", "-c", hostsScript, "hosts", name, hostsFile)
	if err != nil {
		return errors.New("/etc/hosts: " + lastError(out, err))
	}
	return nil
}

func uptime() string {
	f := strings.Fields(readTrim("/proc/uptime"))
	if len(f) == 0 {
		return "?"
	}
	s, _ := strconv.ParseFloat(f[0], 64)
	d := time.Duration(s) * time.Second
	h := int(d.Hours())
	return fmt.Sprintf("%dh %02dm", h, int(d.Minutes())%60)
}

func memory() string {
	var total, avail int
	for _, l := range strings.Split(readTrim("/proc/meminfo"), "\n") {
		f := strings.Fields(l)
		if len(f) >= 2 {
			v, _ := strconv.Atoi(f[1])
			switch f[0] {
			case "MemTotal:":
				total = v
			case "MemAvailable:":
				avail = v
			}
		}
	}
	if total == 0 {
		return "?"
	}
	return fmt.Sprintf("%d MB free of %d", avail/1024, total/1024)
}

func reboot() error {
	_, err := run(10*time.Second, "systemctl", "reboot")
	return err
}

func powerOff() error {
	_, err := run(10*time.Second, "systemctl", "poweroff")
	return err
}

func sshActive() bool {
	for _, u := range []string{"ssh", "sshd"} {
		out, _ := run(3*time.Second, "systemctl", "is-active", u)
		if strings.TrimSpace(out) == "active" {
			return true
		}
	}
	return false
}
