//go:build linux

package console

import (
	"unsafe"

	"golang.org/x/sys/unix"
)

// Console ioctls (linux/kd.h).
const (
	kdgkbtype = 0x4B33 // get keyboard type: succeeds only on a virtual console
	gioCmap   = 0x4B70 // read the 16-colour map (48 bytes)
	pioCmap   = 0x4B71 // write it
	tioclinux = 0x541C // TIOCLINUX: console subcommands

	tioclUnblankScreen = 4  // TIOCL_UNBLANKSCREEN
	tioclBlankScreen   = 14 // TIOCL_BLANKSCREEN: blank until unblanked (a key press doesn't)
)

func isConsole(fd int) bool {
	var t byte
	_, _, e := unix.Syscall(unix.SYS_IOCTL, uintptr(fd), kdgkbtype, uintptr(unsafe.Pointer(&t)))
	return e == 0
}

func getCmap(fd int) ([]byte, error) {
	m := make([]byte, 48)
	if _, _, e := unix.Syscall(unix.SYS_IOCTL, uintptr(fd), gioCmap, uintptr(unsafe.Pointer(&m[0]))); e != 0 {
		return nil, e
	}
	return m, nil
}

func setCmap(fd int, m []byte) error {
	if _, _, e := unix.Syscall(unix.SYS_IOCTL, uintptr(fd), pioCmap, uintptr(unsafe.Pointer(&m[0]))); e != 0 {
		return e
	}
	return nil
}

// blank blanks (on) or unblanks the console the fd is on.
func blank(fd int, on bool) error {
	sub := [1]byte{tioclUnblankScreen}
	if on {
		sub[0] = tioclBlankScreen
	}
	if _, _, e := unix.Syscall(unix.SYS_IOCTL, uintptr(fd), tioclinux, uintptr(unsafe.Pointer(&sub[0]))); e != 0 {
		return e
	}
	return nil
}
