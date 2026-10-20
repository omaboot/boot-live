package checks

import (
	"context"
	"path"
	"strings"

	"github.com/omaboot/boot-live/internal/i18n"
)

// Drive is an optical drive: its block device and its name.
type Drive struct {
	Name  string // sr0
	Model string // "HL-DT-ST DVDRW GA11N": the SCSI vendor and model
	USB   bool   // on USB: an external drive, not the Mac's own
}

// linker reads a symlink's target (sysroot.Root, a hands-on test's IO through link()).
type linker interface {
	Link(p string) string
}

// OpticalDrives are the optical drives the kernel has a block device for (sr_mod, sr*), the
// Mac's own first. lk, if not nil, tells a USB drive from the internal one.
func OpticalDrives(r Reader, lk linker) []Drive {
	var own, usb []Drive
	for _, b := range r.Glob("/sys/block/sr*") {
		d := Drive{Name: path.Base(b), Model: strings.Join(strings.Fields(r.Read(b+"/device/vendor")+" "+r.Read(b+"/device/model")), " ")}
		if lk != nil && strings.Contains(lk.Link(b), "/usb") {
			d.USB = true
			usb = append(usb, d)
			continue
		}
		own = append(own, d)
	}
	return append(own, usb...)
}

// cdromTypes are the SCSI devices of type 5 (CD/DVD), driven by sr_mod or not.
func cdromTypes(r Reader) []string {
	var out []string
	for _, t := range r.Glob("/sys/class/scsi_device/*/device/type") {
		if r.Read(t) == "5" {
			out = append(out, path.Base(path.Dir(path.Dir(t))))
		}
	}
	return out
}

// optical looks at storage.optical, "Internal optical drive reads discs": the drive. Only a
// disc read shows it works: the hands-on test (internal/handson, optical.go) has the tester
// insert one, and its result replaces this one.
//
//   - not_tested (uncertain): the Mac's own drive has its block device (sr_mod): ready, the
//     hands-on test decides;
//   - failed: the SCSI layer sees a CD/DVD device with no block device (sr_mod isn't driving
//     it);
//   - not_tested (no-equipment): no optical drive at all on a Mac that came with one (0.7.3
//     follow-ups: missing hardware isn't a Linux failure; it may have been removed, or swapped
//     for a disk in the optical bay). A drive on USB is an external one, not counted.
func optical(_ context.Context, e *Env) []Result {
	ds := OpticalDrives(e.Root, e.Root)
	var r Result
	switch {
	case len(ds) > 0 && !ds[0].USB:
		d := ds[0]
		r = skip(Uncertain, "optical drive "+d.Name+": "+orDash(d.Model)+": ready; the hands-on test reads a disc and decides")
		r.Detail = i18n.T("Optical drive %s ready", orDash(d.Model))
	case len(cdromTypes(e.Root)) > 0:
		r = fail("a CD/DVD device on the SCSI bus (" + strings.Join(cdromTypes(e.Root), ", ") + ") with no block device: sr_mod isn't driving it")
		r.Detail = i18n.T("drive seen, no driver")
	default:
		ev := "no drive: no optical drive found (no /dev/sr*, no SCSI CD/DVD device) on a Mac that came with one; it may have been removed, or swapped for a disk in the optical bay, so nothing says Linux fails it"
		if len(ds) > 0 {
			ev += "; " + ds[0].Name + " (" + orDash(ds[0].Model) + ") is on USB: an external drive, not counted"
		}
		r = skip(NoEquipment, ev)
		r.Detail = i18n.T("no optical drive found")
	}
	return []Result{r}
}
