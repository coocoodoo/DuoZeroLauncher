//go:build linux

package main

import (
	"fmt"
	"os"
	"syscall"
)

func kernel() string {
	var u syscall.Utsname
	if err := syscall.Uname(&u); err != nil {
		return "?"
	}
	b := make([]byte, 0, 65)
	for _, c := range u.Release {
		if c == 0 {
			break
		}
		b = append(b, byte(c))
	}
	return string(b)
}

func storage() string {
	var st syscall.Statfs_t
	if err := syscall.Statfs("/", &st); err != nil {
		return "?"
	}
	free := float64(st.Bavail) * float64(st.Bsize) / 1e9
	total := float64(st.Blocks) * float64(st.Bsize) / 1e9
	return fmt.Sprintf("%.1f GB free of %.0f", free, total)
}

func execSelf(self string) { syscall.Exec(self, os.Args, os.Environ()) }
