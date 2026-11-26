//go:build !linux

package main

// Stand-ins so the launcher builds (and its scripted previews run) on a PC.

func kernel() string  { return "?" }
func storage() string { return "?" }
func execSelf(string) {}
func reapOrphans()    {}

func kdMode() (int, bool) { return 0, false }

func evKeyBits(string) []byte { return nil }
