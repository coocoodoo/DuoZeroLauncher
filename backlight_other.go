//go:build !linux

package main

// The backlight line is on the Cardputer's M5 expander (backlight_linux.go); nothing to do here.

func holdBacklightLine() bool     { return false }
func releaseBacklightLine()       {}
func backlightLineStatus() string { return "" }
