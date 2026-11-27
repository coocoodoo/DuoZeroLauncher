package main

// Duo Link over the Cardputer's own Wi-Fi: Duo turns the Wi-Fi chip into an access point (a
// NetworkManager profile "Duo Link": WPA2, IPv4 shared, the Cardputer at 10.42.0.1) that the car
// stereo joins. The stereo downloads the app from http://10.42.0.1:47140 and then links straight
// to the Cardputer, with no other network in between. While it is up the Cardputer isn't on any
// other Wi-Fi (one radio); turning it off lets NetworkManager rejoin the usual network.
//
// Needs polkit's wifi.share.protected for pi (device/49-cardputer-wifi.rules, put in by the
// Doctor) and dnsmasq (NetworkManager runs it for DHCP on the shared network).

import (
	"crypto/rand"
	"errors"
	"math/big"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

const (
	hotspotConn = "Duo Link"  // the NetworkManager profile
	hotspotIP   = "10.42.0.1" // the Cardputer on its own Wi-Fi
)

// hotspotCreds is the network's name and password, made once and kept in duolink.json.
func (s *linkServer) hotspotCreds() (ssid, pass string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.store == nil {
		s.store = loadLinkStore()
	}
	if s.store.SSID == "" || len(s.store.Pass) < 8 {
		s.store.SSID = hotspotSSID(s.h.name())
		s.store.Pass = randDigits(8)
		s.store.save()
	}
	return s.store.SSID, s.store.Pass
}

// newHotspotPassword makes a new password (stereos that joined have to join again).
func (s *linkServer) newHotspotPassword() {
	s.mu.Lock()
	if s.store == nil {
		s.store = loadLinkStore()
	}
	s.store.Pass = randDigits(8)
	s.store.save()
	s.mu.Unlock()
}

// hotspotSSID: "DuoLink-<name>", letters, digits and dashes only, at most 32 bytes.
func hotspotSSID(name string) string {
	var b strings.Builder
	for _, r := range name {
		if r < 128 && (r == '-' || r >= '0' && r <= '9' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z') {
			b.WriteRune(r)
		}
	}
	n := b.String()
	if len(n) > 24 {
		n = n[:24]
	}
	if n == "" {
		return "DuoLink"
	}
	return "DuoLink-" + n
}

// randDigits: digits only, easy to type on a car stereo's keyboard (WPA2 wants 8 or more).
func randDigits(n int) string {
	b := make([]byte, n)
	for i := range b {
		d, _ := rand.Int(rand.Reader, big.NewInt(10))
		b[i] = byte('0' + d.Int64())
	}
	return string(b)
}

var (
	wifiIfOnce sync.Once
	wifiIfName = "wlan0"
)

// wifiIface is the Wi-Fi device NetworkManager manages (wlan0 on the Cardputer).
func wifiIface() string {
	wifiIfOnce.Do(func() {
		for _, d := range netDevices() {
			if d.Type == "wifi" {
				wifiIfName = d.Name
				return
			}
		}
	})
	return wifiIfName
}

// startHotspot (re)makes the "Duo Link" profile and brings it up. Slow (up to a minute).
func startHotspot(ssid, pass string) error {
	if _, err := exec.LookPath("nmcli"); err != nil {
		return errors.New("no NetworkManager (nmcli) on this system")
	}
	setWifiRadio(true)
	ifc := wifiIface()
	run(8*time.Second, "nmcli", "connection", "delete", "id", hotspotConn) // an older copy
	args := []string{"connection", "add", "type", "wifi", "ifname", ifc, "con-name", hotspotConn,
		"autoconnect", "no", "ssid", ssid,
		"802-11-wireless.mode", "ap", "802-11-wireless.band", "bg",
		"ipv4.method", "shared", "ipv4.addresses", hotspotIP + "/24", "ipv6.method", "disabled",
		"wifi-sec.key-mgmt", "wpa-psk", "wifi-sec.psk", pass,
		"wifi-sec.proto", "rsn", "wifi-sec.pairwise", "ccmp", "wifi-sec.group", "ccmp",
		"wifi-sec.pmf", "disable"} // some head units can't join with PMF
	if out, err := run(15*time.Second, "nmcli", args...); err != nil {
		return hotspotErr(out, err)
	}
	if out, err := run(45*time.Second, "nmcli", "connection", "up", "id", hotspotConn); err != nil {
		return hotspotErr(out, err)
	}
	return nil
}

// stopHotspot takes the Cardputer's Wi-Fi down; NetworkManager rejoins a saved network.
func stopHotspot() {
	if _, err := exec.LookPath("nmcli"); err != nil {
		return
	}
	run(15*time.Second, "nmcli", "connection", "down", "id", hotspotConn)
}

// hotspotActive: Duo Link's Wi-Fi is up (by this Duo or an earlier one).
func hotspotActive() bool {
	if _, err := exec.LookPath("nmcli"); err != nil {
		return false
	}
	return activeConnections()[hotspotConn]
}

// hotspotErr turns nmcli's complaint into something to act on.
func hotspotErr(out string, err error) error {
	msg := firstLine(out, err)
	low := strings.ToLower(msg)
	switch {
	case strings.Contains(low, "not authorized"), strings.Contains(low, "insufficient privileges"),
		strings.Contains(low, "permission"):
		msg += ". Fix \"Wi-Fi settings allowed\" in Settings > Doctor, then try again."
	case strings.Contains(low, "dnsmasq"):
		msg += ". Install dnsmasq-base (sudo apt install dnsmasq-base)."
	}
	return errors.New(msg)
}

// hotspotStations counts the devices joined to the Cardputer's Wi-Fi right now.
func hotspotStations() int {
	ifc := wifiIface()
	if iw := findTool("iw"); iw != "" {
		if out, err := run(3*time.Second, iw, "dev", ifc, "station", "dump"); err == nil {
			return strings.Count(out, "Station ")
		}
	}
	// no iw: the neighbours seen on the Wi-Fi lately
	b, _ := os.ReadFile("/proc/net/arp")
	n := 0
	for i, l := range strings.Split(string(b), "\n") {
		f := strings.Fields(l)
		if i > 0 && len(f) >= 6 && f[5] == ifc && f[2] == "0x2" {
			n++
		}
	}
	return n
}

// findTool looks in PATH and then the sbin folders a user's PATH often lacks.
func findTool(name string) string {
	if p, err := exec.LookPath(name); err == nil {
		return p
	}
	for _, d := range []string{"/usr/sbin/", "/sbin/"} {
		if _, err := os.Stat(d + name); err == nil {
			return d + name
		}
	}
	return ""
}
