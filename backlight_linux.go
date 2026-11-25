//go:build linux

package main

// The Cardputer's backlight has a second control besides the brightness: line 9 of M5's I/O
// expander (the gpiochip labelled "m5ioe1"). M5's own launcher pulls it low at boot and keeps it
// so; left alone (as with Duo as the home screen) the LED still glows at brightness 0. With it
// low, brightness works as before and 0 is really off. Duo holds it low for as long as it runs,
// through the GPIO character device: the gpio group is enough, no root.

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"unsafe"
)

const (
	gpioGetChipInfoIoctl = 0x8044B401 // GPIO_GET_CHIPINFO_IOCTL
	gpioV2GetLineIoctl   = 0xC250B407 // GPIO_V2_GET_LINE_IOCTL
	gpioV2FlagOutput     = 1 << 3     // GPIO_V2_LINE_FLAG_OUTPUT
	backlightChip        = "m5ioe1"
	backlightLine        = 9
)

type gpioChipInfo struct {
	Name, Label [32]byte
	Lines       uint32
}

type gpioV2LineAttribute struct {
	ID, Padding uint32
	Value       uint64
}

type gpioV2LineConfigAttribute struct {
	Attr gpioV2LineAttribute
	Mask uint64
}

type gpioV2LineConfig struct {
	Flags    uint64
	NumAttrs uint32
	Padding  [5]uint32
	Attrs    [10]gpioV2LineConfigAttribute
}

type gpioV2LineRequest struct {
	Offsets         [64]uint32
	Consumer        [32]byte
	Config          gpioV2LineConfig
	NumLines        uint32
	EventBufferSize uint32
	Padding         [5]uint32
	Fd              int32
}

var errNoBacklightChip = errors.New("no m5ioe1 expander")

// findGPIOChip opens the gpiochip with this label.
func findGPIOChip(label string) (*os.File, error) {
	paths, _ := filepath.Glob("/dev/gpiochip*")
	var last error = errNoBacklightChip
	for _, p := range paths {
		f, err := os.OpenFile(p, os.O_RDWR, 0)
		if err != nil {
			last = err
			continue
		}
		var info gpioChipInfo
		_, _, e := syscall.Syscall(syscall.SYS_IOCTL, f.Fd(), gpioGetChipInfoIoctl, uintptr(unsafe.Pointer(&info)))
		if e == 0 && strings.TrimRight(string(info.Label[:]), "\x00") == label && info.Lines > backlightLine {
			return f, nil
		}
		f.Close()
	}
	return nil, last
}

// requestBacklightLow asks for line 9 as an output, low; the line stays so while the returned
// file is open.
func requestBacklightLow() (*os.File, error) {
	chip, err := findGPIOChip(backlightChip)
	if err != nil {
		return nil, err
	}
	defer chip.Close()
	var req gpioV2LineRequest
	req.Offsets[0] = backlightLine
	req.NumLines = 1
	req.Config.Flags = gpioV2FlagOutput // output values default to 0: low
	copy(req.Consumer[:], "duo-launcher backlight")
	if _, _, e := syscall.Syscall(syscall.SYS_IOCTL, chip.Fd(), gpioV2GetLineIoctl, uintptr(unsafe.Pointer(&req))); e != 0 {
		return nil, e
	}
	return os.NewFile(uintptr(req.Fd), "m5ioe1-line9"), nil
}

var backlightHold *os.File

// holdBacklightLine pulls the backlight line low and keeps it so (until releaseBacklightLine).
// Safe to call again: it only tries while it doesn't hold the line yet.
func holdBacklightLine() bool {
	if backlightHold != nil {
		return true
	}
	f, err := requestBacklightLow()
	if err != nil {
		return false
	}
	backlightHold = f
	return true
}

func releaseBacklightLine() {
	if backlightHold != nil {
		backlightHold.Close()
		backlightHold = nil
	}
}

// backlightLineStatus is for the Doctor: "held" (by this Duo), "in use" (by another program,
// say the running Duo), "free" (it could be held), "" (no such expander here) or an error.
func backlightLineStatus() string {
	if backlightHold != nil {
		return "held"
	}
	f, err := requestBacklightLow()
	switch {
	case err == nil:
		f.Close()
		return "free"
	case errors.Is(err, syscall.EBUSY):
		return "in use"
	case errors.Is(err, errNoBacklightChip):
		return ""
	}
	return err.Error()
}
