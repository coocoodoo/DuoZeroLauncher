//go:build !linux

package cp0

// Blank is Linux only (FBIOBLANK); elsewhere the screen is a file and nothing happens.
func (s *Screen) Blank(off bool) bool { return false }
