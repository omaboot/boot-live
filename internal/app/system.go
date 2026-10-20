package app

import (
	"path"
	"regexp"
	"strings"

	"github.com/omaboot/boot-live/internal/checks"
	"github.com/omaboot/boot-live/internal/i18n"
	"github.com/omaboot/boot-live/internal/send"
)

// What the results stand for (owner, 2026-10-12): Hello names the system and the image before
// anything runs; Quick results and the report repeat it with what the pass learned (the GPU as
// Mesa names it, how long the Mac took to start).

var leadingVersion = regexp.MustCompile(`^\d+(\.\d+)*`)

// kernel is the running kernel as its package and version (owner, 2026-10-15): "linux-omarchy
// 7.2.5-3" for release 7.2.5-3-omarchy, "linux-t2 7.2.4.arch1-3" for 7.2.4-arch1-Watanare-T2-3-t2,
// from the image's package database; without the package there, the release's leading version
// ("linux-omarchy 7.2.5"). The report keeps the release itself (uname -r).
func (r *Run) kernel() string {
	name := checks.KernelName(r.HW.Kernel)
	v := leadingVersion.FindString(r.HW.Kernel)
	if p := r.pkgVersion(name); p != "" && (v == "" || leadingVersion.FindString(p) == v) {
		return name + " " + p
	}
	if v == "" {
		v = r.HW.Kernel
	}
	return strings.TrimSpace(name + " " + v)
}

// pkgVersion is a package's version and release in the image's package database ("7.2.5-3",
// the epoch dropped), or "".
func (r *Run) pkgVersion(name string) string {
	if !strings.HasPrefix(name, "linux") {
		return ""
	}
	for _, d := range r.Opt.Root.Glob("/var/lib/pacman/local/" + name + "-[0-9]*") {
		v := strings.TrimPrefix(path.Base(d), name+"-")
		if i := strings.Index(v, ":"); i >= 0 {
			v = v[i+1:]
		}
		return v
	}
	return ""
}

// mesa is the image's Mesa version from its package database, or "".
func (r *Run) mesa() string {
	for _, d := range r.Opt.Root.Glob("/var/lib/pacman/local/mesa-[0-9]*") {
		v := strings.TrimPrefix(path.Base(d), "mesa-")
		if i := strings.LastIndex(v, "-"); i > 0 {
			v = v[:i] // drop the package release
		}
		if i := strings.Index(v, ":"); i >= 0 {
			v = v[i+1:] // and the epoch
		}
		return v
	}
	return ""
}

// SystemLine is Hello's line on the system: Omarchy, the kernel and whether it's the one an
// install gives this Mac, Mesa, and how the Mac booted.
func (r *Run) SystemLine() string {
	k := r.kernel()
	if checks.InstallKernel(r.Ident.Chip == "t2", r.HW.Kernel) {
		k = i18n.T("%s (the kernel an install gives this Mac)", k)
	} else {
		k = i18n.T("%s (not the kernel an install gives this Mac)", k)
	}
	parts := []string{"Omarchy " + r.omarchy(), k}
	if m := r.mesa(); m != "" {
		parts = append(parts, "Mesa "+m)
	}
	switch r.HW.EFIBits {
	case 64:
		parts = append(parts, i18n.T("64-bit EFI"))
	case 32:
		parts = append(parts, i18n.T("32-bit EFI"))
	default:
		parts = append(parts, i18n.T("BIOS compatibility mode"))
	}
	return strings.Join(parts, " · ")
}

// image names the image with its build date when the name doesn't carry it:
// "omaboot-live dev a79585d (2026-10-12)".
func (r *Run) image() string {
	img := send.Image(r.Opt.Root, r.Opt.Edition.Key)
	if b := strings.Fields(r.Opt.Root.Read("/usr/share/omaboot/build")); len(b) > 1 && !strings.Contains(img, b[1]) {
		img += " (" + b[1] + ")"
	}
	return img
}

// ImageLine is Hello's line on the image and the OmaBoot? fixes it applied.
func (r *Run) ImageLine() string {
	s := i18n.T("image %s", r.image())
	if len(r.Fixes) > 0 {
		var names []string
		for _, f := range r.Fixes {
			names = append(names, f.Short)
		}
		s += " · " + i18n.T("fixes: %s", strings.Join(names, ", "))
	}
	return s
}

// testedOn is what the results stand for, once the pass ran: Omarchy, the kernel, Mesa with the
// GPU it named, for Quick results and the report.
func (r *Run) testedOn(rs []checks.Result) string {
	parts := []string{"Omarchy " + r.omarchy(), r.kernel()}
	gpu := ""
	for _, x := range rs {
		if x.Renderer != "" {
			gpu = x.Renderer
			break
		}
	}
	switch m := r.mesa(); {
	case m != "" && gpu != "":
		parts = append(parts, "Mesa "+m+", "+gpu)
	case m != "":
		parts = append(parts, "Mesa "+m)
	case gpu != "":
		parts = append(parts, gpu)
	}
	return strings.Join(parts, " · ")
}
