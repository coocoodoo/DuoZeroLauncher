//go:build linux

package cp0

import "syscall"

const (
	fbioBlank       = 0x4611 // FBIOBLANK
	fbBlankUnblank  = 0
	fbBlankPowerOff = 4 // FB_BLANK_POWERDOWN
)

// Blank powers the panel down (off true) or back up, through the framebuffer's FBIOBLANK, as
// Linux does for screen blanking: with the DRM panel driver the display itself switches off,
// not just the backlight. It reports whether the driver did it.
func (s *Screen) Blank(off bool) bool {
	if s.f == nil {
		return false
	}
	arg := uintptr(fbBlankUnblank)
	if off {
		arg = fbBlankPowerOff
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	_, _, e := syscall.Syscall(syscall.SYS_IOCTL, s.f.Fd(), fbioBlank, arg)
	return e == 0
}
