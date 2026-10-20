//go:build linux

package handson

import (
	"os"
	"unsafe"

	"golang.org/x/sys/unix"
)

// eviocgabs is EVIOCGABS(0), _IOR('E', 0x40 + axis, struct input_absinfo): add the axis.
const eviocgabs = 0x80184540

// AbsRange reads an axis's range from the device (EVIOCGABS: struct input_absinfo is value,
// minimum, maximum, fuzz, flat, resolution as s32). A fixture tree has no devices to ask.
func (s *System) AbsRange(d Device, code int) (int, int, bool) {
	if !s.Real() {
		return 0, 0, false
	}
	f, err := os.Open(d.Path())
	if err != nil {
		return 0, 0, false
	}
	defer f.Close()
	var info [6]int32
	rc, err := f.SyscallConn()
	if err != nil {
		return 0, 0, false
	}
	var errno unix.Errno
	rc.Control(func(fd uintptr) {
		_, _, errno = unix.Syscall(unix.SYS_IOCTL, fd, uintptr(eviocgabs+code), uintptr(unsafe.Pointer(&info[0])))
	})
	if errno != 0 {
		return 0, 0, false
	}
	return int(info[1]), int(info[2]), info[2] > info[1]
}
