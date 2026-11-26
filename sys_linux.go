//go:build linux

package main

import (
	"fmt"
	"os"
	"syscall"
	"unsafe"
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

// reapOrphans collects children that already ended but were never waited for: after Duo
// restarts itself in place (a self-update), the old program's sound process is left a zombie
// under the new one. Call it before Duo starts any process of its own.
func reapOrphans() {
	for {
		var ws syscall.WaitStatus
		pid, err := syscall.Wait4(-1, &ws, syscall.WNOHANG, nil)
		if err != nil || pid <= 0 {
			return
		}
	}
}

// kdMode reads tty1's console mode (KDGETMODE): 0 text, 1 graphics; ok is false if tty1 can't
// be opened (not ours, or no console).
func kdMode() (mode int, ok bool) {
	f, err := os.OpenFile("/dev/tty1", os.O_RDONLY|syscall.O_NOCTTY, 0)
	if err != nil {
		return 0, false
	}
	defer f.Close()
	var m int32
	if _, _, e := syscall.Syscall(syscall.SYS_IOCTL, f.Fd(), 0x4B3B, uintptr(unsafe.Pointer(&m))); e != 0 {
		return 0, false
	}
	return int(m), true
}

// evKeyBits reads which keys an input device declares (EVIOCGBIT(EV_KEY)); nil if it can't.
func evKeyBits(path string) []byte {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	bits := make([]byte, 96) // KEY_MAX 0x2ff
	req := uintptr(0x80000000 | len(bits)<<16 | 'E'<<8 | 0x20 + 1)
	if _, _, e := syscall.Syscall(syscall.SYS_IOCTL, f.Fd(), req, uintptr(unsafe.Pointer(&bits[0]))); e != 0 {
		return nil
	}
	return bits
}

// diskSpace is the free and total space of the drive holding path (-1 if unknown).
func diskSpace(path string) (free, total int64) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return -1, -1
	}
	return int64(st.Bavail) * int64(st.Bsize), int64(st.Blocks) * int64(st.Bsize)
}

// sameDevice: a and b are on the same drive (so a move is a rename).
func sameDevice(a, b string) bool {
	var sa, sb syscall.Stat_t
	if syscall.Stat(a, &sa) != nil || syscall.Stat(b, &sb) != nil {
		return false
	}
	return sa.Dev == sb.Dev
}
