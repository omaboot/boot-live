// Package safety keeps the run non-destructive (LIVE-PLAN §6): the real mode starts only on
// the OmaBoot? live system, every internal disk is made read-only and checked before any test,
// and a thermal watchdog stops any load near the chips' own critical temperatures. Nothing here
// writes to NVRAM, EFI variables, the SMC or firmware.
package safety

import (
	"errors"
	"strings"

	"github.com/omaboot/boot-live/internal/sysroot"
)

// ErrNotLive is returned by Guard on any system that isn't the OmaBoot? live system.
var ErrNotLive = errors.New("this isn't the OmaBoot? live system: the real tests make the internal disks read-only, so they run only on a Mac booted from an OmaBoot? image (try --demo)")

// Guard reports whether root is the OmaBoot? live system: an archiso live boot (/run/archiso,
// or archisobasedir= on the kernel command line) that carries OmaBoot?'s files
// (/usr/share/omaboot/). Both must hold, so neither an installed Linux nor another Arch ISO
// passes. The real mode refuses to start otherwise; it never runs on a normal computer.
func Guard(r sysroot.Root) error {
	archiso := r.IsDir("/run/archiso") || strings.Contains(" "+r.Read("/proc/cmdline"), " archisobasedir=")
	omaboot := r.IsDir("/usr/share/omaboot")
	if archiso && omaboot {
		return nil
	}
	return ErrNotLive
}
