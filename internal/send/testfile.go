package send

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/sys/unix"

	"github.com/omaboot/boot-live/internal/i18n"
	"github.com/omaboot/boot-live/internal/safety"
	"github.com/omaboot/boot-live/internal/sysroot"
)

// The console's testfile: OmaBoot?'s test file written by
// boot-live itself to a drive the person names, after asking. The rules for where it may go are
// the safety rules' and a few of its own: only FAT and exFAT (Linux writes HFS+ only with its
// journal off, too risky on someone's drive), never a disk the safety rules protect (internal,
// FireWire), never the image's own partitions, only the OMABOOT partition on the image's stick.
// It goes in the top folder; nothing else on the drive changes, and a file of its name that isn't
// OmaBoot?'s is left alone.

// Drive is a drive as testfile lists it.
type Drive struct {
	Dev  string // the partition (sdc1), or the disk with no partition table (sdc)
	What string // "SanDisk Ultra · FAT32 · 15 GB"
	OK   bool
	Why  string // why not, when it can't take the file
}

// TestDrives lists the drives the test file could go on, and the ones it won't, with why.
func TestDrives(r sysroot.Root) []Drive {
	boot, _ := BootDisk(r)
	var out []Drive
	for _, d := range safety.Disks(r) {
		name := diskWhat(r, d.Name)
		switch {
		case d.Bus == safety.BusFW:
			out = append(out, Drive{Dev: d.Name, What: name, Why: i18n.T("FireWire: read-only for the whole run (it may be another Mac's disk)")})
			continue
		case d.Internal:
			out = append(out, Drive{Dev: d.Name, What: name, Why: i18n.T("internal: read-only for the whole run")})
			continue
		}
		parts := d.Parts
		if len(parts) == 0 {
			parts = []string{d.Name}
		}
		for _, p := range parts {
			fs, label := fsOf(r, p)
			what := strings.Join(nonEmpty(name, fsName(fs), size(r, p)), "  ·  ")
			if d.Name == boot { // the image's own stick: only its OMABOOT partition
				if label == RoomLabel {
					out = append(out, Drive{Dev: p, What: i18n.T("the OmaBoot? drive's OMABOOT partition") + "  ·  " + strings.Join(nonEmpty(fsName(fs), size(r, p)), "  ·  "), OK: true})
				}
				continue
			}
			switch fs {
			case "vfat", "exfat":
				out = append(out, Drive{Dev: p, What: what, OK: true})
			case "hfsplus":
				out = append(out, Drive{Dev: p, What: what, Why: i18n.T("Linux writes HFS+ only with its journal off")})
			case "":
				out = append(out, Drive{Dev: p, What: name, Why: i18n.T("no file system OmaBoot? knows")})
			default:
				out = append(out, Drive{Dev: p, What: what, Why: i18n.T("OmaBoot? writes only to FAT and exFAT")})
			}
		}
		if d.Name == boot && !hasRoom(r, d) {
			out = append(out, Drive{Dev: d.Name, What: i18n.T("the OmaBoot? drive"), Why: i18n.T("no OMABOOT partition yet: Save a report once to make it")})
		}
	}
	return out
}

func hasRoom(r sysroot.Root, d safety.Disk) bool {
	for _, p := range d.Parts {
		if _, l := fsOf(r, p); l == RoomLabel {
			return true
		}
	}
	return false
}

// fsOf is a block device's file system type and label, from udev's database.
func fsOf(r sysroot.Root, dev string) (fs, label string) {
	mm := strings.TrimSpace(r.Read("/sys/class/block/" + dev + "/dev"))
	for _, l := range r.Lines("/run/udev/data/b" + mm) {
		if v, ok := strings.CutPrefix(l, "E:ID_FS_TYPE="); ok {
			fs = v
		}
		if v, ok := strings.CutPrefix(l, "E:ID_FS_LABEL="); ok {
			label = v
		}
	}
	return fs, label
}

// fsName is a file system type as people know it.
func fsName(fs string) string {
	switch fs {
	case "vfat":
		return "FAT"
	case "exfat":
		return "exFAT"
	case "hfsplus":
		return "HFS+"
	case "ntfs", "ntfs3":
		return "NTFS"
	case "apfs":
		return "APFS"
	}
	return fs
}

// diskWhat is a disk's maker and model ("SanDisk Ultra"), or its name.
func diskWhat(r sysroot.Root, disk string) string {
	s := strings.Join(nonEmpty(strings.TrimSpace(r.Read("/sys/block/"+disk+"/device/vendor")), strings.TrimSpace(r.Read("/sys/block/"+disk+"/device/model"))), " ")
	if s == "" {
		return disk
	}
	return s
}

// size is a block device's size, decimal as the Finder gives it.
func size(r sysroot.Root, dev string) string {
	n, ok := r.Int("/sys/class/block/" + dev + "/size")
	if !ok || n <= 0 {
		return ""
	}
	b := float64(n) * 512
	switch {
	case b >= 1e9:
		return fmt.Sprintf("%.0f GB", b/1e9)
	default:
		return fmt.Sprintf("%.0f MB", b/1e6)
	}
}

func nonEmpty(ss ...string) []string {
	var out []string
	for _, s := range ss {
		if s != "" {
			out = append(out, s)
		}
	}
	return out
}

// ErrAlready means the drive already has OmaBoot?'s test file, byte for byte.
var ErrAlready = errors.New("already there")

// WriteTestFile writes b as name in the top folder of dev, a drive TestDrives allows: it mounts
// it if it isn't mounted (never formats it), checks there's room, leaves a file of the name that
// isn't b alone, writes and syncs, and unmounts it again if it mounted it. The caller reads it
// back from the disk itself.
func WriteTestFile(ctx context.Context, r sysroot.Root, dev, name string, b []byte) error {
	if !r.IsSystem() {
		return errors.New(i18n.T("a fixture run writes nothing"))
	}
	ok := false
	for _, d := range TestDrives(r) {
		if d.Dev == dev {
			if !d.OK {
				return errors.New(d.Why)
			}
			ok = true
		}
	}
	if !ok {
		return fmt.Errorf("%s", i18n.T("no drive %s", dev))
	}
	node := "/dev/" + dev
	mp, mounted := mountpoint(r, node)
	if mounted && ro(r, node) {
		return errors.New(i18n.T("it's mounted read-only"))
	}
	if !mounted {
		mp = "/run/boot-live/testfile"
		if err := os.MkdirAll(mp, 0o755); err != nil {
			return err
		}
		if err := mountDrive(ctx, node, mp, "rw,noatime"); err != nil {
			return fmt.Errorf("%s", i18n.T("couldn't mount it: %s", err.Error()))
		}
		defer func() {
			uctx, cancel := context.WithTimeout(ctx, 20*time.Second)
			defer cancel()
			exec.CommandContext(uctx, "umount", mp).Run()
		}()
	}
	werr := func() error {
		ents, err := os.ReadDir(mp)
		if err != nil {
			return err
		}
		for _, e := range ents { // FAT and exFAT don't mind case: any OMABOOT-TEST.BIN is this one
			if !strings.EqualFold(e.Name(), name) {
				continue
			}
			have, err := os.ReadFile(filepath.Join(mp, e.Name()))
			if err == nil && bytes.Equal(have, b) {
				return ErrAlready
			}
			return errors.New(i18n.T("there's another %s on it: left alone", e.Name()))
		}
		var st unix.Statfs_t
		if err := unix.Statfs(mp, &st); err == nil {
			if free := int64(st.Bavail) * int64(st.Bsize); free < int64(len(b))+1<<20 {
				return errors.New(i18n.T("not enough room: %d MB free, 33 MB needed", free/1e6))
			}
		}
		return write(path.Join(mp, name), b)
	}()
	exec.Command("sync").Run()
	return werr
}
