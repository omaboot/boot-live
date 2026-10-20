//go:build linux

package handson

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"
	"unsafe"

	"golang.org/x/sys/unix"
)

// The optical drive, the Linux way (linux/cdrom.h): the device is opened read-only and
// non-blocking (no disc needed to open it, and the door isn't locked), asked with ioctls, read
// with pread, and ejected with CDROMEJECT after unlocking its door (CDROM_LOCKDOOR 0); the drive
// is then asked again until it reports the disc gone (0.7.3 review: an eject that returned fine
// left the disc in an iMac10,1's drive). diskread_test.go checks opticalFlags with the raw
// read's: read-only, never created, truncated or appended to.
//
// What works the drive is counted here, for the whole program (drives): a read still in the
// kernel after its test was left (a stalled read can't be called back; it stops at its next
// chunk), and an eject. The tests that sleep the Mac wait for both (discguard.go): an iMac10,1
// hung going to sleep after a slow read.
const opticalFlags = unix.O_RDONLY | unix.O_NONBLOCK | unix.O_CLOEXEC

// ioctl requests (linux/cdrom.h).
const (
	cdromEject       = 0x5309
	cdromLockDoor    = 0x5329
	cdromReadTOCHdr  = 0x5305
	cdromReadTOCEnt  = 0x5306
	cdromDriveStatus = 0x5326
	cdromDiscStatus  = 0x5327
	cdslCurrent      = 0x7fffffff // INT_MAX: the current slot
	cdromLBA         = 0x01
	cdromLeadout     = 0xaa
)

type tocHdr struct{ First, Last uint8 }

type tocEntry struct {
	Track, AdrCtrl, Format, _ uint8
	Addr                      int32 // the LBA, with Format cdromLBA
	Datamode                  uint8
	_                         [3]uint8
}

// openDrive opens an optical drive (sr0) with opticalFlags.
func openDrive(dev string) (*os.File, error) {
	if !diskName.MatchString(dev) || !strings.HasPrefix(dev, "sr") {
		return nil, fmt.Errorf("not an optical drive's name: %s", dev)
	}
	return os.OpenFile("/dev/"+dev, opticalFlags, 0)
}

func ioctlRet(fd int, req, arg uintptr) (int, error) {
	r, _, e := unix.Syscall(unix.SYS_IOCTL, uintptr(fd), req, arg)
	if e != 0 {
		return -1, e
	}
	return int(r), nil
}

// ioctlPtr is an ioctl that fills in a struct of ours.
func ioctlPtr(fd int, req uintptr, p unsafe.Pointer) error {
	if _, _, e := unix.Syscall(unix.SYS_IOCTL, uintptr(fd), req, uintptr(p)); e != 0 {
		return e
	}
	return nil
}

// DriveStatus asks the drive for its state (CDROM_DRIVE_STATUS). A fixture run asks nothing.
func (s *System) DriveStatus(dev string) (int, error) {
	if !s.Real() {
		return -1, ErrNotReal
	}
	f, err := openDrive(dev)
	if err != nil {
		return -1, err
	}
	defer f.Close()
	return ioctlRet(int(f.Fd()), cdromDriveStatus, cdslCurrent)
}

// drives counts what works each drive (by name): reads and ejects under way.
var drives = struct {
	sync.Mutex
	reading, ejecting map[string]int
}{reading: map[string]int{}, ejecting: map[string]int{}}

func driveBusy(m map[string]int, dev string, d int) {
	drives.Lock()
	m[dev] += d
	drives.Unlock()
}

// DiscBusy says what still works the drive: "reading" (a read in the kernel, its test left or
// not), "ejecting", or "".
func (s *System) DiscBusy(dev string) string {
	drives.Lock()
	defer drives.Unlock()
	switch {
	case drives.reading[dev] > 0:
		return "reading"
	case drives.ejecting[dev] > 0:
		return "ejecting"
	}
	return ""
}

func reading(dev string) bool {
	drives.Lock()
	defer drives.Unlock()
	return drives.reading[dev] > 0
}

// ReadDisc reads the disc in the drive on its own goroutine: its kind, then its first MB (data)
// or its table of contents (audio). The eject is EjectDisc's.
func (s *System) ReadDisc(dev string) DiscJob {
	if !s.Real() {
		return &discJob{res: &DiscRead{Skipped: "not on the Mac itself: nothing is read"}}
	}
	j := &discJob{start: time.Now()}
	j.last = j.start
	driveBusy(drives.reading, dev, 1)
	go func() {
		defer driveBusy(drives.reading, dev, -1)
		r := readDisc(dev, j.progress)
		j.mu.Lock()
		if j.res == nil {
			j.res = &r
		}
		j.mu.Unlock()
	}()
	return j
}

// How an eject waits: for a read still in the kernel to come back, and for the drive to say the
// disc is out after each try.
const (
	ejectWaitRead = 60 * time.Second
	ejectWaitOut  = 8 * time.Second
)

// EjectDisc gives the disc back on its own goroutine: once no read works the drive, it unlocks
// the door and ejects (twice at most), each time asking the drive until it reports no disc or
// its tray open. With always false, only a disc the drive reports is ejected (an empty tray
// isn't opened).
func (s *System) EjectDisc(dev string, always bool) EjectJob {
	if !s.Real() {
		return &ejectJob{res: &EjectResult{Err: "not on the Mac itself: nothing is ejected"}}
	}
	j := &ejectJob{}
	driveBusy(drives.ejecting, dev, 1)
	go func() {
		defer driveBusy(drives.ejecting, dev, -1)
		r := ejectDisc(dev, always, j)
		j.mu.Lock()
		j.res = &r
		j.mu.Unlock()
	}()
	return j
}

// ejectJob is the Mac's eject, polled from the screen's goroutine.
type ejectJob struct {
	mu   sync.Mutex
	wait bool
	res  *EjectResult
}

func (j *ejectJob) Ready() (EjectResult, bool) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.res == nil {
		return EjectResult{}, false
	}
	return *j.res, true
}

func (j *ejectJob) Waiting() bool {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.wait && j.res == nil
}

func (j *ejectJob) waiting(w bool) {
	j.mu.Lock()
	j.wait = w
	j.mu.Unlock()
}

// driveState asks the drive for its state with a moment's fd of its own.
func driveState(dev string) (int, error) {
	f, err := openDrive(dev)
	if err != nil {
		return -1, err
	}
	defer f.Close()
	return ioctlRet(int(f.Fd()), cdromDriveStatus, cdslCurrent)
}

func ejectDisc(dev string, always bool, j *ejectJob) EjectResult {
	var r EjectResult
	end := time.Now().Add(ejectWaitRead)
	for reading(dev) { // a stalled read holds the drive (and the eject would be refused: EBUSY)
		j.waiting(true)
		if time.Now().After(end) {
			r.Err = fmt.Sprintf("a read was still stuck in the drive after %d s, so it wasn't ejected", int(ejectWaitRead.Seconds()))
			return r
		}
		time.Sleep(200 * time.Millisecond)
	}
	j.waiting(false)
	st, err := driveState(dev)
	if err == nil && discOutState(st) && !always {
		r.Out, r.State = true, statusWord(st)
		return r
	}
	for r.Tries < 2 {
		r.Tries++
		if err := ejectOnce(dev); err != nil {
			r.Err = err.Error()
		}
		// asked until the disc is out: CDROMEJECT can answer fine and leave it in
		deadline := time.Now().Add(ejectWaitOut)
		for {
			st, err = driveState(dev)
			if err == nil && discOutState(st) {
				r.Out, r.State, r.Err = true, statusWord(st), ""
				return r
			}
			if time.Now().After(deadline) {
				break
			}
			time.Sleep(500 * time.Millisecond)
		}
		r.State = statusWord(st)
		if err != nil {
			r.State = "unknown (" + err.Error() + ")"
		}
	}
	why := fmt.Sprintf("the drive still reports %s after %d ejects", r.State, r.Tries)
	if r.Err != "" {
		why += " (the eject said: " + r.Err + ")"
	}
	r.Err = why
	return r
}

// ejectOnce unlocks the door (CDROM_LOCKDOOR 0: allow medium removal, also clearing a lock kept
// by an earlier open) and ejects (CDROMEJECT), through its own fd: the eject is refused (EBUSY)
// while another fd has the drive open, so a moment's EBUSY (udev looking at the disc) is tried
// again.
func ejectOnce(dev string) error {
	f, err := openDrive(dev)
	if err != nil {
		return err
	}
	defer f.Close()
	fd := int(f.Fd())
	_, _ = ioctlRet(fd, cdromLockDoor, 0) // not every drive locks: its answer doesn't matter
	for try := 0; ; try++ {
		_, err = ioctlRet(fd, cdromEject, 0)
		if !errors.Is(err, unix.EBUSY) || try == 2 {
			return err
		}
		time.Sleep(time.Second)
	}
}

// discJob is the Mac's disc job, polled from the screen's goroutine; the stall and time limits
// are judged here, by the clock, so a read stuck in the kernel still ends.
type discJob struct {
	mu          sync.Mutex
	prog        DiscProgress
	start, last time.Time
	res         *DiscRead
	stop        bool
}

func (j *discJob) progress(p DiscProgress) bool {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.stop || j.res != nil {
		return false
	}
	j.prog, j.last = p, time.Now()
	return true
}

func (j *discJob) Progress() DiscProgress {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.prog
}

func (j *discJob) Ready() (DiscRead, bool) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.res != nil {
		return *j.res, true
	}
	switch {
	case time.Since(j.last) >= opStall:
		j.res = &DiscRead{Fail: fmt.Sprintf("it stalled: no progress for %d s (%s, %d KB read)", int(opStall.Seconds()), j.prog.Phase, j.prog.Done>>10)}
	case time.Since(j.start) >= opLimit:
		j.res = &DiscRead{Fail: fmt.Sprintf("it didn't finish within %d s", int(opLimit.Seconds()))}
	default:
		return DiscRead{}, false
	}
	return *j.res, true
}

func (j *discJob) Stop() {
	j.mu.Lock()
	j.stop = true
	j.mu.Unlock()
}

// readDisc does the job; prog hears each step and stops it by answering false: the read stops
// at its next chunk and the drive's fd is closed.
func readDisc(dev string, prog func(DiscProgress) bool) DiscRead {
	var r DiscRead
	if !prog(DiscProgress{Phase: "kind"}) {
		return r
	}
	f, err := openDrive(dev)
	if err != nil {
		r.Fail = "couldn't open it read-only: " + strings.TrimPrefix(err.Error(), "open ")
		return r
	}
	defer f.Close()
	fd := int(f.Fd())
	ks, err := ioctlRet(fd, cdromDiscStatus, 0)
	if err != nil {
		r.Fail = "the drive wouldn't say what disc it has: " + err.Error()
		return r
	}
	if r.Kind = discKind(ks); r.Kind == "" {
		r.Fail = "no readable track on it (a blank disc?)"
	} else {
		readKind(f, dev, &r, prog) // stopped (the screen left) or not, the job ends here
	}
	return r
}

// readKind reads the disc its kind's way: the table of contents of an audio CD, the first MB
// of anything with data. It answers false when stopped.
func readKind(f *os.File, dev string, r *DiscRead, prog func(DiscProgress) bool) bool {
	fd := int(f.Fd())
	t0 := time.Now()
	defer func() { r.Took = time.Since(t0) }()
	if r.Kind == "audio" {
		var h tocHdr
		if err := ioctlPtr(fd, cdromReadTOCHdr, unsafe.Pointer(&h)); err != nil {
			r.Fail = "its table of contents didn't read: " + err.Error()
			return true
		}
		e := tocEntry{Track: cdromLeadout, Format: cdromLBA}
		if err := ioctlPtr(fd, cdromReadTOCEnt, unsafe.Pointer(&e)); err != nil {
			r.Fail = "its table of contents didn't read: " + err.Error()
			return true
		}
		r.Tracks, r.Secs = int(h.Last)-int(h.First)+1, int(e.Addr)/75
		return prog(DiscProgress{Phase: "read"})
	}
	size, err := unix.IoctlGetInt(fd, unix.BLKGETSIZE64)
	if err == nil && size == 0 { // opened non-blocking, the size may not be known yet: once more, blocking
		if f2, err2 := os.OpenFile("/dev/"+dev, unix.O_RDONLY|unix.O_CLOEXEC, 0); err2 == nil {
			defer f2.Close()
			f = f2
			size, err = unix.IoctlGetInt(int(f.Fd()), unix.BLKGETSIZE64)
		}
	}
	if err != nil || size <= 0 {
		r.Fail = "the drive gives the disc no size"
		return true
	}
	total := int64(min(size, discLen))
	buf := make([]byte, 64<<10)
	got := make([]byte, 0, total)
	for r.Bytes < total {
		if !prog(DiscProgress{Phase: "read", Done: r.Bytes, Total: total}) {
			return false
		}
		n, err := f.ReadAt(buf[:min(int64(len(buf)), total-r.Bytes)], r.Bytes)
		got = append(got, buf[:n]...)
		r.Bytes += int64(n)
		switch {
		case errors.Is(err, io.EOF) && r.Bytes < total, err == nil && n == 0:
			r.Fail = fmt.Sprintf("it ended %d KB in", r.Bytes>>10)
			return true
		case err != nil && !errors.Is(err, io.EOF):
			r.Fail = fmt.Sprintf("a read error %d KB in: %v", r.Bytes>>10, err)
			return true
		}
	}
	if r.TestDisc = checkTestDisc(got); strings.HasPrefix(r.TestDisc, "bad") {
		r.Fail = "the " + testDiscLabel + " disc came back with wrong data: " + strings.TrimPrefix(r.TestDisc, "bad: ")
	}
	return prog(DiscProgress{Phase: "read", Done: r.Bytes, Total: total})
}
