package safety

import (
	"path"
	"strconv"
	"strings"

	"github.com/omaboot/boot-live/internal/sysroot"
)

// Part is one partition as Hello draws it: its device name, its GPT name if it has one, its size.
type Part struct {
	Name, Label string
	Bytes       int64
}

// Layout is a disk as Hello draws it: its model, size and partitions, from sysfs
// only. Anything sysfs doesn't say is left empty or 0.
type Layout struct {
	Name, Model string
	Bytes       int64
	Parts       []Part
}

// DiskLayout reads d's model, size and partitions.
func DiskLayout(r sysroot.Root, d Disk) Layout {
	p := "/sys/block/" + d.Name
	l := Layout{Name: d.Name, Model: strings.TrimSpace(r.Read(p + "/device/model")), Bytes: sectors(r, p+"/size")}
	for _, name := range d.Parts {
		pp := p + "/" + name
		pt := Part{Name: name, Bytes: sectors(r, pp+"/size")}
		for _, kv := range strings.Split(r.Read(pp+"/uevent"), "\n") {
			if v, ok := strings.CutPrefix(kv, "PARTNAME="); ok {
				pt.Label = strings.TrimSpace(v)
			}
		}
		l.Parts = append(l.Parts, pt)
	}
	return l
}

// sectors is a sysfs size file (512-byte sectors, whatever the disk's own sector size) in bytes.
func sectors(r sysroot.Root, p string) int64 {
	n, err := strconv.ParseInt(strings.TrimSpace(r.Read(p)), 10, 64)
	if err != nil || n < 0 {
		return 0
	}
	return n * 512
}

// Written is how many bytes the kernel has written to disk name since it started: the stat file's
// sectors written (its 7th field). -1 when sysfs doesn't say.
func Written(r sysroot.Root, name string) int64 {
	f := strings.Fields(r.Read(path.Join("/sys/block", name, "stat")))
	if len(f) < 7 {
		return -1
	}
	n, err := strconv.ParseInt(f[6], 10, 64)
	if err != nil {
		return -1
	}
	return n * 512
}
