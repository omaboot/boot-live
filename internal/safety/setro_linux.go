package safety

import (
	"os"

	"golang.org/x/sys/unix"
)

// setRO marks a block device read-only in the kernel (BLKROSET), as `blockdev --setro` does.
func setRO(dev string) error {
	f, err := os.OpenFile(dev, os.O_RDONLY, 0)
	if err != nil {
		return err
	}
	defer f.Close()
	return unix.IoctlSetPointerInt(int(f.Fd()), unix.BLKROSET, 1)
}
