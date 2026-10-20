package checks

import (
	"context"
	"fmt"
	"strings"

	"github.com/omaboot/boot-live/internal/boot"
	"github.com/omaboot/boot-live/internal/i18n"
	"github.com/omaboot/boot-live/internal/safety"
)

// bootEFI decides boot.installer-efi64, "Stock installer boots via 64-bit EFI: the unmodified
// Omarchy installer ISO boots natively from the Mac's startup manager". Booting
// OmaBoot?'s rebuilt ISO (Omarchy's ISO, its GRUB and kernels, plus our changes) counts.
//
//   - supported: this system booted through 64-bit EFI (/sys/firmware/efi, fw_platform_size 64);
//   - not_tested (other): it booted without EFI (BIOS compatibility), which says nothing about
//     the EFI path.
func bootEFI(_ context.Context, e *Env) []Result {
	kernel := e.HW.Kernel
	img := e.Root.Read("/usr/share/omaboot/build")
	if e.HW.EFIBits == 0 {
		r := skip(Other, "booted without EFI (BIOS compatibility mode); kernel "+kernel)
		r.Detail = i18n.T("booted without EFI")
		return []Result{r}
	}
	r := pass(fmt.Sprintf("booted the OmaBoot? image (%s) through 64-bit EFI and GRUB; kernel %s", orUnknown(img), kernel))
	r.Detail = i18n.T("booted via GRUB, %s", KernelName(kernel))
	var slots []string
	for _, d := range e.HW.PCI {
		if len(slots) < 10 {
			slots = append(slots, d.ShortSlot())
		}
	}
	r.Params = boot.Params{Items: slots}
	r.Say = i18n.T("we booted. that already counts for something.")
	return []Result{r}
}

// KernelName names the kernel package from its release: linux-t2 or linux-omarchy.
func KernelName(release string) string {
	switch {
	case strings.Contains(release, "-t2"):
		return "linux-t2"
	case strings.Contains(release, "omarchy"):
		return "linux-omarchy"
	}
	return release
}

func orUnknown(s string) string {
	if s == "" {
		return "unknown build"
	}
	return s
}

// bootInstall is boot.install, "Internal storage detected and installation completes": a live
// boot doesn't install (live: no), so it's always not_tested, reason live-limit. What storage
// the Mac has is recorded as evidence (and as an extra in the report).
func bootInstall(_ context.Context, e *Env) []Result {
	in := safety.Internal(safety.Disks(e.Root))
	ev := "a live boot doesn't install"
	if len(in) > 0 {
		var ds []string
		for _, d := range in {
			ds = append(ds, d.Name+" ("+string(d.Bus)+")")
		}
		ev += "; internal storage found: " + strings.Join(ds, ", ")
	} else {
		ev += "; no internal storage found"
	}
	r := skip(LiveLimit, ev)
	r.Detail = i18n.T("a live boot doesn't install")
	return []Result{r}
}

// bootPowerCycle is boot.power-cycle, "Survives a power cycle": only an installed system can be
// restarted and come back (live: no), so it's always not_tested, reason live-limit. It has a line
// of its own so the quick pass lists every Boot criterion the summary counts.
func bootPowerCycle(context.Context, *Env) []Result {
	r := skip(LiveLimit, "a live boot can't restart an installed system")
	r.Detail = i18n.T("a live boot can't restart an installed system")
	return []Result{r}
}
