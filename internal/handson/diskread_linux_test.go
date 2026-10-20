//go:build linux

package handson

import (
	"testing"

	"golang.org/x/sys/unix"
)

// The raw read opens disks read-only, whichever way it opens them; so does the optical drive test.
func TestRawFlagsReadOnly(t *testing.T) {
	for name, fl := range map[string]int{"rawFlags": rawFlags, "rawFlagsCached": rawFlagsCached, "opticalFlags": opticalFlags} {
		if fl&unix.O_ACCMODE != unix.O_RDONLY {
			t.Errorf("%s: access mode %#o", name, fl&unix.O_ACCMODE)
		}
		if fl&(unix.O_CREAT|unix.O_TRUNC|unix.O_APPEND) != 0 {
			t.Errorf("%s: %#o", name, fl)
		}
	}
	if rawFlags&unix.O_DIRECT == 0 {
		t.Error("rawFlags without O_DIRECT: the second pass would read the page cache")
	}
}
