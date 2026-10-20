package send

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/doesitomarchy/doesitomarchy/pkg/report"

	"github.com/omaboot/boot-live/internal/safety"
	"github.com/omaboot/boot-live/internal/sysroot"
)

// StickLabel is the label of the drive results are saved to.
const StickLabel = "BOOTLIVE"

// ErrNoStick means no BOOTLIVE drive is plugged in.
var ErrNoStick = errors.New("no BOOTLIVE drive plugged in")

// FileName is results/<identifier>-<code or timestamp>.json.
func FileName(identifier, code string, at time.Time) string {
	id := strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune(",.-_", r) {
			return r
		}
		return '_'
	}, identifier)
	if id == "" {
		id = "unknown"
	}
	tag := code
	if tag == "" {
		tag = at.UTC().Format("20060102T150405Z")
	}
	return "results/" + id + "-" + tag + ".json"
}

// Save writes the report to results/ on the BOOTLIVE stick (else the OMABOOT partition on the
// image's own stick, see MakeRoom) and returns the file's path on the stick. Once it's written,
// drop (a path like name's, or "") is removed: an earlier copy this one replaces. On the live system it mounts the stick if it isn't mounted (never formats it), writes
// and syncs the file, and unmounts it again if it mounted it. With dir set (--stick, for
// development) it writes under that directory instead and mounts nothing; on a fixture tree it
// needs dir.
func Save(ctx context.Context, r sysroot.Root, dir string, f *report.File, name, drop string) (string, error) {
	b, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return "", err
	}
	return SaveBytes(ctx, r, dir, append(b, '\n'), name, drop)
}

// SaveBytes is Save for any file: b written as name (under results/) on the stick, the same way
// (the console's bugreport saves its archive with it).
func SaveBytes(ctx context.Context, r sysroot.Root, dir string, b []byte, name, drop string) (string, error) {
	stickMu.Lock()
	defer stickMu.Unlock()
	if dir != "" {
		err := write(filepath.Join(dir, name), b)
		if err == nil && drop != "" && drop != name {
			os.Remove(filepath.Join(dir, drop))
		}
		return name, err
	}
	if !r.IsSystem() {
		return "", errors.New("a fixture run saves only with --stick DIR")
	}
	// a BOOTLIVE stick first, else the results partition on the image's own stick, made the
	// first time it's needed (or put back in the table, after a new image's write: MakeRoom)
	dev, err := filepath.EvalSymlinks("/dev/disk/by-label/" + StickLabel)
	if err != nil {
		dev, err = filepath.EvalSymlinks("/dev/disk/by-label/" + RoomLabel)
	}
	if err != nil {
		if rerr := MakeRoom(ctx, r); rerr != nil {
			return "", fmt.Errorf("%w (%v)", ErrNoStick, rerr)
		}
		if dev, err = roomDev(); err != nil {
			return "", ErrNoStick
		}
	}
	if name := wholeDisk(r, dev); internal(r, name) { // a BOOTLIVE label on a FireWire or internal disk: not ours
		return "", fmt.Errorf("%s is on a disk the safety rules protect (%s): not writing to it", dev, name)
	}
	mp, mounted := mountpoint(r, dev)
	if !mounted {
		mp = "/run/boot-live/stick"
		if err := os.MkdirAll(mp, 0o755); err != nil {
			return "", err
		}
		if err := mountDrive(ctx, dev, mp, "rw,noatime"); err != nil {
			return "", fmt.Errorf("couldn't mount the drive: %s", err)
		}
	}
	remounted := false
	if mounted && ro(r, dev) { // the BOOTLIVE hook may have it mounted read-only: rw just for the write
		if out, err := exec.CommandContext(ctx, "mount", "-o", "remount,rw", mp).CombinedOutput(); err != nil {
			return "", fmt.Errorf("the BOOTLIVE drive is mounted read-only: %s", strings.TrimSpace(string(out)))
		}
		remounted = true
	}
	werr := write(filepath.Join(mp, name), b)
	if werr == nil && drop != "" && drop != name {
		os.Remove(filepath.Join(mp, drop))
	}
	exec.Command("sync").Run()
	if remounted {
		exec.Command("mount", "-o", "remount,ro", mp).Run()
	}
	if !mounted {
		uctx, cancel := context.WithTimeout(ctx, 20*time.Second)
		defer cancel()
		if out, err := exec.CommandContext(uctx, "umount", mp).CombinedOutput(); err != nil && werr == nil {
			werr = fmt.Errorf("saved, but couldn't unmount the drive: %s", strings.TrimSpace(string(out)))
		}
	}
	return name, werr
}

func write(p string, b []byte) error {
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	tmp := p + ".part"
	fh, err := os.Create(tmp)
	if err != nil {
		return err
	}
	if _, err := fh.Write(b); err != nil {
		fh.Close()
		return err
	}
	if err := fh.Sync(); err != nil {
		fh.Close()
		return err
	}
	if err := fh.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp, p); err != nil {
		return err
	}
	// the directory too, so the new name survives a forced power-off (the diagnostics saved right
	// before a nap that may never end)
	if d, err := os.Open(filepath.Dir(p)); err == nil {
		d.Sync()
		d.Close()
	}
	return nil
}

// mountpoint finds where a device is mounted (/proc/mounts).
func mountpoint(r sysroot.Root, dev string) (string, bool) {
	for _, l := range r.Lines("/proc/mounts") {
		f := strings.Fields(l)
		if len(f) >= 2 && f[0] == dev {
			return strings.ReplaceAll(f[1], `\040`, " "), true
		}
	}
	return "", false
}

func ro(r sysroot.Root, dev string) bool {
	for _, l := range r.Lines("/proc/mounts") {
		f := strings.Fields(l)
		if len(f) >= 4 && f[0] == dev {
			return strings.HasPrefix(f[3], "ro")
		}
	}
	return false
}

// WholeDisk is the disk a device (/dev/sdc1) is on: sdc.
func WholeDisk(r sysroot.Root, dev string) string { return wholeDisk(r, dev) }

// wholeDisk is the disk a device (/dev/sdc1) is on: sdc.
func wholeDisk(r sysroot.Root, dev string) string {
	name := filepath.Base(dev)
	if r.Exists("/sys/class/block/" + name + "/partition") {
		name = filepath.Base(filepath.Dir(r.Link("/sys/class/block/" + name)))
	}
	return name
}

// internal reports whether the safety rules protect disk name (internal, FireWire, anything
// not on USB or SD).
func internal(r sysroot.Root, name string) bool {
	for _, d := range safety.Disks(r) {
		if d.Name == name {
			return d.Internal
		}
	}
	return false
}
