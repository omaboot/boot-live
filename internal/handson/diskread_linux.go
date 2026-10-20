//go:build linux

package handson

import (
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"golang.org/x/sys/unix"
)

// The only way this package opens a disk: read-only, the whole device, with O_DIRECT (the
// page cache bypassed, so each pass reaches the device). A device that refuses O_DIRECT is
// opened read-only without it, its cache dropped before each pass. diskread_test.go checks both
// flag sets and that nothing else in the package opens a file for writing.
const (
	rawFlags       = unix.O_RDONLY | unix.O_DIRECT | unix.O_CLOEXEC
	rawFlagsCached = unix.O_RDONLY | unix.O_CLOEXEC
)

// diskName is a whole disk's name as the kernel gives it: sdb, mmcblk0, nvme0n1.
var diskName = regexp.MustCompile(`^[a-z][a-z0-9]{1,15}$`)

// rawDisk is a whole disk opened by openRaw.
type rawDisk struct {
	f      *os.File
	size   int64
	cached bool // O_DIRECT refused: DropCache before each pass
}

// openRaw opens a whole-disk device read-only (rawFlags, else rawFlagsCached).
func openRaw(p string) (*rawDisk, error) {
	f, err := os.OpenFile(p, rawFlags, 0)
	cached := false
	if errors.Is(err, unix.EINVAL) {
		f, err = os.OpenFile(p, rawFlagsCached, 0)
		cached = true
	}
	if err != nil {
		return nil, err
	}
	size, err := f.Seek(0, io.SeekEnd)
	if err != nil {
		f.Close()
		return nil, err
	}
	return &rawDisk{f: f, size: size, cached: cached}, nil
}

func (d *rawDisk) ReadAt(p []byte, off int64) (int, error) { return d.f.ReadAt(p, off) }
func (d *rawDisk) Size() int64                             { return d.size }
func (d *rawDisk) Close() error                            { return d.f.Close() }

// DropCache drops the device's page cache (only without O_DIRECT; a hint, never a write).
func (d *rawDisk) DropCache() {
	if !d.cached {
		return
	}
	if rc, err := d.f.SyscallConn(); err == nil {
		rc.Control(func(fd uintptr) { unix.Fadvise(int(fd), 0, 0, unix.FADV_DONTNEED) })
	}
}

// ReadDisk reads disk name raw on its own goroutine (diskread.go); fileOnly, only the test file
// on it. A fixture run reads nothing.
func (s *System) ReadDisk(name string, fileOnly bool) DiskJob {
	if !s.Real() {
		return doneJob{DiskRead{Disk: name, Skipped: "not on the Mac itself: nothing is read"}}
	}
	if !diskName.MatchString(name) {
		return doneJob{DiskRead{Disk: name, Kind: failOpen, Fail: "not a disk's name: " + name}}
	}
	how := readHow{Base: cmdlineArg(s, "archisobasedir"), FileOnly: fileOnly}
	if how.Base == "" {
		how.Base = "arch"
	}
	how.Sector, _ = strconv.Atoi(s.Read("/sys/block/" + name + "/queue/logical_block_size"))
	t0 := time.Now()
	clock := func() time.Duration { return time.Since(t0) }
	return startJob(name, clock, func(prog func(ReadProgress) bool) DiskRead {
		dev, err := openRaw("/dev/" + name)
		if err != nil {
			return DiskRead{Kind: failOpen, Pass: 1, Fail: "couldn't open it read-only: " + strings.TrimPrefix(err.Error(), "open ")}
		}
		defer dev.Close()
		return readPasses(dev, how, clock, prog)
	})
}

// BootDriveSpeed reads the OmaBoot? drive (disk, sdb) raw at a few places, for the boot timing
// (0.7.5; an iMac10,1 booted 4× slower one day, its stick reading 0.7 MB/s against 16.6
// the run before): 2 MiB at 15, 40, 65 and 90 % into airootfs.sfs (base is archisobasedir), or
// into the disk when no image is found, with O_DIRECT like the USB test, so the device answers
// and not the page cache. It gives up after 20 s with what it has.
func BootDriveSpeed(disk, base string) BootRead {
	r := BootRead{Disk: disk}
	if !diskName.MatchString(disk) {
		r.Err = "not a disk's name: " + disk
		return r
	}
	dev, err := openRaw("/dev/" + disk)
	if err != nil {
		r.Err = "couldn't open it read-only: " + strings.TrimPrefix(err.Error(), "open ")
		return r
	}
	defer dev.Close()
	dev.DropCache()
	off, size := int64(0), dev.Size()
	if img, ok := findImage(dev, base); ok {
		off, size, r.Image = img.Off, img.Size, true
	}
	const sample = 2 << 20
	if size < sample {
		r.Err = "too small to read"
		return r
	}
	buf := alignedBuf(sample)
	give := time.Now().Add(20 * time.Second)
	for _, frac := range []float64{0.15, 0.40, 0.65, 0.90} {
		at := (off + int64(float64(size-sample)*frac)) &^ (align - 1)
		t0 := time.Now()
		n, err := dev.ReadAt(buf, at)
		took := time.Since(t0)
		if n > 0 {
			r.Bytes += int64(n)
			r.Took += took
			r.Reads = append(r.Reads, fmt.Sprintf("%d MiB in: %.1f MB/s", (at-off)>>20, float64(n)/took.Seconds()/1e6))
		}
		if err != nil {
			r.Err = err.Error()
			break
		}
		if time.Now().After(give) {
			break
		}
	}
	return r
}

// CheckTestFile reads the test file back from disk name raw, read-only and past the page cache,
// as the port tests find it, and checks every byte (the console's testfile, after writing it).
func CheckTestFile(name string) (string, error) {
	if !diskName.MatchString(name) {
		return "", errors.New("not a disk's name: " + name)
	}
	d, err := openRaw("/dev/" + name)
	if err != nil {
		return "", err
	}
	defer d.Close()
	d.DropCache()
	b, _ := os.ReadFile("/sys/block/" + name + "/queue/logical_block_size")
	sector, _ := strconv.Atoi(strings.TrimSpace(string(b)))
	return verifyTestFile(d, sector)
}
