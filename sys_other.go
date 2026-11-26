//go:build !linux

package main

// Stand-ins so the launcher builds (and its scripted previews run) on a PC.

import "path/filepath"

func kernel() string  { return "?" }
func storage() string { return "?" }
func execSelf(string) {}
func reapOrphans()    {}

func kdMode() (int, bool) { return 0, false }

func evKeyBits(string) []byte { return nil }

func diskSpace(string) (int64, int64) { return -1, -1 }
func sameDevice(a, b string) bool     { return filepath.VolumeName(a) == filepath.VolumeName(b) }
