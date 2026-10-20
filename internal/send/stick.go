package send

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/omaboot/boot-live/internal/sysroot"
)

// WithStick runs fn with the directory the reports' drive is at: the BOOTLIVE stick, else the
// results partition on the image's own stick (never made here: reading or removing needs none;
// one a new image's write left out of the table is read where it is, and put back for rw),
// or dir when set (--stick). On the live system it mounts the drive if it isn't mounted (read-only
// unless rw; one mounted read-only is remounted rw for rw and back after), and unmounts it again
// if it mounted it. ErrNoStick when there's no such drive.
func WithStick(ctx context.Context, r sysroot.Root, dir string, rw bool, fn func(base string) error) error {
	stickMu.Lock()
	defer stickMu.Unlock()
	if dir != "" {
		return fn(dir)
	}
	if !r.IsSystem() {
		return errors.New("a fixture run reads and writes the stick only with --stick DIR")
	}
	dev, err := filepath.EvalSymlinks("/dev/disk/by-label/" + StickLabel)
	if err != nil {
		dev, err = filepath.EvalSymlinks("/dev/disk/by-label/" + RoomLabel)
	}
	at := "" // the mount options that find OMABOOT on the whole stick, when it isn't in the table
	if err != nil {
		disk, room, ok := FoundRoom(r) // left out by a new image's write
		switch {
		case !ok:
			return ErrNoStick
		case rw: // back into the table, as a save would
			if err := MakeRoom(ctx, r); err != nil {
				return fmt.Errorf("%w (%v)", ErrNoStick, err)
			}
			if dev, err = roomDev(); err != nil {
				return ErrNoStick
			}
		default:
			dev, at = "/dev/"+disk, fmt.Sprintf(",loop,offset=%d,sizelimit=%d", room.Start*sector, room.Count*sector)
		}
	}
	if name := wholeDisk(r, dev); internal(r, name) {
		return fmt.Errorf("%s is on a disk the safety rules protect (%s): not using it", dev, name)
	}
	mp, mounted := mountpoint(r, dev)
	if at != "" {
		mp, mounted = "", false // the whole stick may be mounted (the image): never that
	}
	if !mounted {
		mp = "/run/boot-live/stick"
		if err := os.MkdirAll(mp, 0o755); err != nil {
			return err
		}
		opts := "ro,noatime"
		if rw {
			opts = "rw,noatime"
		}
		if err := mountDrive(ctx, dev, mp, opts+at); err != nil {
			return fmt.Errorf("couldn't mount the drive: %s", err)
		}
	}
	remounted := false
	if rw && mounted && ro(r, dev) {
		if out, err := exec.CommandContext(ctx, "mount", "-o", "remount,rw", mp).CombinedOutput(); err != nil {
			return fmt.Errorf("the drive is mounted read-only: %s", strings.TrimSpace(string(out)))
		}
		remounted = true
	}
	ferr := fn(mp)
	if rw {
		exec.Command("sync").Run()
	}
	if remounted {
		exec.Command("mount", "-o", "remount,ro", mp).Run()
	}
	if !mounted {
		uctx, cancel := context.WithTimeout(ctx, 20*time.Second)
		defer cancel()
		if out, err := exec.CommandContext(uctx, "umount", mp).CombinedOutput(); err != nil && ferr == nil {
			ferr = fmt.Errorf("couldn't unmount the drive: %s", strings.TrimSpace(string(out)))
		}
	}
	return ferr
}

// stickMu takes the drive's users one at a time (WithStick, SaveBytes): two mounting it at once
// would share its mount point, and the first one done would unmount it under the other (the
// sound settings are saved whenever M is pressed, a report copy whenever the run says).
var stickMu sync.Mutex

// Remove removes files (paths like FileName's) from the reports' drive; a file already gone is
// no error.
func Remove(ctx context.Context, r sysroot.Root, dir string, names ...string) error {
	return WithStick(ctx, r, dir, true, func(base string) error {
		for _, n := range names {
			if err := os.Remove(filepath.Join(base, filepath.Clean("/"+n))); err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
		}
		return nil
	})
}

// roomDev waits for udev to name the OMABOOT partition MakeRoom just added.
func roomDev() (string, error) {
	var err error
	for i := 0; i < 20; i++ {
		var dev string
		if dev, err = filepath.EvalSymlinks("/dev/disk/by-label/" + RoomLabel); err == nil {
			return dev, nil
		}
		time.Sleep(500 * time.Millisecond)
	}
	return "", err
}

// mountDrive mounts dev at mp with opts. When the kernel won't open the partition on its own,
// it mounts it through a loop device, the way MakeRoom formats it: on some boots the live system
// mounts its image from the whole stick, the disk is claimed whole, and a mount of its OMABOOT
// partition fails with "Can't open blockdev" (the room smoke test, 0.7.13 dev, 2026-10-17: the
// partition made and formatted, the save refused). The loop device is freed on unmount.
func mountDrive(ctx context.Context, dev, mp, opts string) error {
	mctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	out, err := exec.CommandContext(mctx, "mount", "-o", opts, dev, mp).CombinedOutput()
	if err == nil {
		return nil
	}
	if _, lerr := exec.CommandContext(mctx, "mount", "-o", "loop,"+opts, dev, mp).CombinedOutput(); lerr == nil {
		return nil
	}
	return errors.New(strings.TrimSpace(string(out)))
}
