package safety

import (
	"fmt"
	"path"
	"regexp"
	"sort"
	"strings"

	"github.com/omaboot/boot-live/internal/sysroot"
)

// Bus is how a block device is attached.
type Bus string

const (
	BusSATA    Bus = "sata"     // ATA/AHCI, incl. Apple's PCIe AHCI SSDs
	BusNVMe    Bus = "nvme"     // NVMe, incl. the T2's SSD
	BusUSB     Bus = "usb"      // sticks, USB disks and the SD readers Macs attach over USB
	BusMMC     Bus = "mmc"      // SD cards on a PCI reader
	BusFW      Bus = "firewire" // SBP-2 disks: a FireWire drive, a Mac in Target Disk Mode: protected like an internal one
	BusOther   Bus = "other"    // anything else: treated as internal
	BusVirtual Bus = "virtual"  // loop, ram, zram: not disks
)

// Disk is one whole block device.
type Disk struct {
	Name     string   // sda, nvme0n1
	Bus      Bus      // from its sysfs path, never from the removable flag
	Internal bool     // made read-only
	Parts    []string // its partitions
	Holders  []string // device-mapper/md devices built on it (FileVault, LVM, RAID)
	RO       bool     // the disk and every partition and holder read-only now
}

// Disks lists the block devices and decides which are internal: by the bus they hang off, not the
// removable flag, because Mac SD readers sit on USB and report non-removable media. USB and SD stay
// writable; everything else, including buses we don't recognise (Thunderbolt disks among them) and
// FireWire disks (another Mac in Target Disk Mode is someone's internal disk), is internal and gets
// made read-only. The boot stick is on USB, so it's never touched. A disk that shows up later is
// protected when Protect runs again (the FireWire hands-on test runs it as a FireWire disk
// appears).
func Disks(r sysroot.Root) []Disk {
	var out []Disk
	for _, p := range r.Glob("/sys/block/*") {
		name := path.Base(p)
		d := Disk{Name: name, Bus: busOf(name, r.Link(p))}
		if d.Bus == BusVirtual {
			continue
		}
		d.Internal = d.Bus != BusUSB && d.Bus != BusMMC
		for _, pp := range r.Glob(p + "/" + name + "*") {
			if r.Exists(pp + "/partition") {
				d.Parts = append(d.Parts, path.Base(pp))
			}
		}
		holders := map[string]bool{}
		for _, h := range append(r.Glob(p+"/holders/*"), r.Glob(p+"/"+name+"*/holders/*")...) {
			holders[path.Base(h)] = true
		}
		for h := range holders {
			d.Holders = append(d.Holders, h)
		}
		sort.Strings(d.Holders)
		d.RO = readOnly(r, d)
		out = append(out, d)
	}
	return out
}

// fwPath is a FireWire node in a sysfs path (…/0000:04:00.0/fw1/fw1.0/host7/…).
var fwPath = regexp.MustCompile(`/fw\d+/fw\d+\.\d+/`)

// busOf decides a block device's bus from its name and its /sys/block symlink target.
func busOf(name, target string) Bus {
	switch {
	case strings.HasPrefix(name, "loop"), strings.HasPrefix(name, "ram"), strings.HasPrefix(name, "zram"),
		strings.HasPrefix(name, "dm-"), strings.HasPrefix(name, "md"), strings.HasPrefix(name, "sr"),
		strings.Contains(target, "/virtual/"):
		// dm/md devices are handled as holders of the disks under them; sr is optical (read-only)
		return BusVirtual
	case fwPath.MatchString(target):
		return BusFW
	case strings.Contains(target, "/usb"):
		return BusUSB
	case strings.Contains(target, "/mmc_host/") || strings.HasPrefix(name, "mmcblk"):
		return BusMMC
	case strings.Contains(target, "/nvme/") || strings.HasPrefix(name, "nvme"):
		return BusNVMe
	case strings.Contains(target, "/ata"):
		return BusSATA
	}
	return BusOther
}

func readOnly(r sysroot.Root, d Disk) bool {
	if r.Read("/sys/block/"+d.Name+"/ro") != "1" {
		return false
	}
	for _, p := range d.Parts {
		if r.Read("/sys/block/"+d.Name+"/"+p+"/ro") != "1" {
			return false
		}
	}
	for _, h := range d.Holders {
		if r.Read("/sys/block/"+h+"/ro") != "1" {
			return false
		}
	}
	return true
}

// Internal returns the internal disks.
func Internal(ds []Disk) []Disk {
	var out []Disk
	for _, d := range ds {
		if d.Internal {
			out = append(out, d)
		}
	}
	return out
}

// Names lists the disks' names.
func Names(ds []Disk) []string {
	var out []string
	for _, d := range ds {
		out = append(out, d.Name)
	}
	return out
}

// mountedRW lists internal devices mounted read-write (/proc/mounts).
func mountedRW(r sysroot.Root, ds []Disk) []string {
	devs := map[string]bool{}
	for _, d := range ds {
		devs[d.Name] = true
		for _, p := range d.Parts {
			devs[p] = true
		}
		for _, h := range d.Holders {
			devs[h] = true
		}
	}
	var out []string
	for _, l := range r.Lines("/proc/mounts") {
		f := strings.Fields(l)
		if len(f) < 4 || !strings.HasPrefix(f[0], "/dev/") {
			continue
		}
		dev := strings.TrimPrefix(f[0], "/dev/")
		if devs[dev] && !strings.HasPrefix(f[3], "ro") {
			out = append(out, dev+" on "+f[1])
		}
	}
	return out
}

// Protect makes every internal disk, its partitions and the devices built on them read-only
// (BLKROSET, what `blockdev --setro` does), then reads every one back from sysfs. It returns the
// disks as they are now, and an error if any internal device isn't read-only or is mounted
// read-write: the run must not continue then.
//
// It acts only on the running OmaBoot? live system: on a fixture tree it changes nothing and only
// checks (a dry run); on any other system it refuses (ErrNotLive).
func Protect(r sysroot.Root) ([]Disk, error) {
	ds := Disks(r)
	in := Internal(ds)
	if rw := mountedRW(r, in); len(rw) > 0 {
		return ds, fmt.Errorf("an internal disk is mounted read-write (%s): stopping", strings.Join(rw, ", "))
	}
	if !r.IsSystem() {
		return ds, nil // a fixture tree: a dry run, nothing is written
	}
	if err := Guard(sysroot.System); err != nil {
		return ds, err // never writes outside the live system
	}
	var failed []string
	for _, d := range in {
		devs := append([]string{d.Name}, d.Parts...)
		devs = append(devs, d.Holders...)
		for _, dev := range devs {
			if err := setRO("/dev/" + dev); err != nil {
				failed = append(failed, dev+": "+err.Error())
			}
		}
	}
	ds = Disks(r)
	for _, d := range Internal(ds) {
		if !d.RO {
			failed = append(failed, d.Name+" is still writable")
		}
	}
	if len(failed) > 0 {
		return ds, fmt.Errorf("couldn't make the internal disks read-only (%s): stopping", strings.Join(failed, "; "))
	}
	return ds, nil
}
