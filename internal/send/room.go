package send

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode/utf16"

	"github.com/omaboot/boot-live/internal/safety"
	"github.com/omaboot/boot-live/internal/sysroot"
)

// Room for reports on the OmaBoot? stick itself (LIVE-PLAN §19, decision 2): the image is a
// read-only ISO, so the first save with no BOOTLIVE drive around adds a small FAT partition
// labelled OMABOOT at the stick's end, past the image (Plan). Not BOOTLIVE: with a BOOTLIVE
// payload stick plugged in too, two drives of that name would leave the hook guessing.
//
// Our images are hybrid: xorriso writes an MBR (which Linux reads) and a GPT (which Mac firmware
// and macOS read), with the GPT's backup at the end of the image, not of the stick. The new
// partition goes into both: an empty MBR slot, and an empty GPT entry with the backup GPT moved
// to the stick's end. The image's own partitions are never changed.

const (
	sector     = 512
	align      = 2048              // 1 MiB
	roomMax    = 64 << 20 / sector // 64 MiB: thousands of reports with the 32 MiB test file beside them, and the image with it still fits a 2 GB stick (owner, 2026-10-17; was 32)
	roomMin    = 8 << 20 / sector  // below 8 MiB, don't bother
	reserve    = align             // kept free at the stick's very end, past any GPT's backup (at most 1024 entries: 256 sectors)
	fat16LBA   = 0x0e              // W95 FAT16 (LBA): FAT32 needs more room than 64 MiB
	mbrEntries = 446               // offset of the MBR's partition table
	gptHdrSize = 92
)

// basicData is the GPT type GUID of Microsoft basic data (FAT), in its on-disk byte order.
var basicData = []byte{0xa2, 0xa0, 0xd0, 0xeb, 0xe5, 0xb9, 0x33, 0x44, 0x87, 0xc0, 0x68, 0xb6, 0xb7, 0x26, 0x99, 0xc7}

// RoomLabel is the label of the results partition on the image's own stick.
const RoomLabel = "OMABOOT"

// ErrNoRoom means the stick has no space after the image.
var ErrNoRoom = errors.New("no free space on this drive after the image")

// BootDisk is the whole disk the image was booted from (sdb), from archiso's
// archisosearchuuid on the kernel command line.
func BootDisk(r sysroot.Root) (string, error) {
	uuid := ""
	for _, f := range strings.Fields(r.Read("/proc/cmdline")) {
		if v, ok := strings.CutPrefix(f, "archisosearchuuid="); ok {
			uuid = v
		}
	}
	if uuid == "" {
		return "", errors.New("not booted from an OmaBoot? image (no archisosearchuuid)")
	}
	dev, err := filepath.EvalSymlinks("/dev/disk/by-uuid/" + uuid)
	if err != nil {
		return "", fmt.Errorf("can't find the drive the image booted from: %w", err)
	}
	name := path.Base(dev)
	if r.Exists("/sys/class/block/" + name + "/partition") {
		name = path.Base(path.Dir(r.Link("/sys/class/block/" + name)))
	}
	return name, nil
}

// slot is one MBR partition entry.
type slot struct {
	Type         byte
	Start, Count uint32
}

// Write is one change to the disk: data at byte offset Off.
type Write struct {
	Off  int64
	Data []byte
}

// gpt is a parsed GPT header with its entry array.
type gpt struct {
	hdr   []byte // gptHdrSize bytes
	arr   []byte
	n, sz uint32 // entries, entry size
}

func (g gpt) u64(off int) uint64        { return binary.LittleEndian.Uint64(g.hdr[off:]) }
func (g gpt) entry(i uint32) []byte     { return g.arr[i*g.sz : (i+1)*g.sz] }
func (g gpt) arraySectors() uint64      { return (uint64(g.n)*uint64(g.sz) + sector - 1) / sector }
func put64(b []byte, off int, v uint64) { binary.LittleEndian.PutUint64(b[off:], v) }

// seal sets the array's and the header's CRCs.
func (g gpt) seal(hdr []byte) {
	binary.LittleEndian.PutUint32(hdr[88:], crc32.ChecksumIEEE(g.arr))
	binary.LittleEndian.PutUint32(hdr[16:], 0)
	binary.LittleEndian.PutUint32(hdr[16:], crc32.ChecksumIEEE(hdr[:gptHdrSize]))
}

// readGPT reads the primary GPT, or returns ok=false when there is none; a GPT that is there
// but broken is an error.
func readGPT(d io.ReaderAt) (gpt, bool, error) {
	s := make([]byte, sector)
	if _, err := d.ReadAt(s, sector); err != nil {
		return gpt{}, false, err
	}
	if string(s[:8]) != "EFI PART" {
		return gpt{}, false, nil
	}
	g := gpt{hdr: append([]byte(nil), s[:gptHdrSize]...)}
	if binary.LittleEndian.Uint32(g.hdr[12:]) != gptHdrSize {
		return g, true, errors.New("the drive's GPT header has an unexpected size")
	}
	want := binary.LittleEndian.Uint32(g.hdr[16:])
	h := append([]byte(nil), g.hdr...)
	binary.LittleEndian.PutUint32(h[16:], 0)
	if crc32.ChecksumIEEE(h) != want {
		return g, true, errors.New("the drive's GPT header is damaged")
	}
	g.n, g.sz = binary.LittleEndian.Uint32(g.hdr[80:]), binary.LittleEndian.Uint32(g.hdr[84:])
	if g.sz < 128 || g.n == 0 || g.n > 1024 {
		return g, true, errors.New("the drive's GPT entry array is unusual")
	}
	g.arr = make([]byte, int(g.n)*int(g.sz))
	if _, err := d.ReadAt(g.arr, int64(g.u64(72))*sector); err != nil {
		return g, true, err
	}
	if crc32.ChecksumIEEE(g.arr) != binary.LittleEndian.Uint32(g.hdr[88:]) {
		return g, true, errors.New("the drive's GPT entry array is damaged")
	}
	return g, true, nil
}

// Room is where the OMABOOT partition is, or goes, in sectors: Found when it's one already
// there that an image write left out of the partition table (see Plan).
type Room struct {
	Start, Count uint64
	Found        bool
}

// layout reads a disk laid out as our image: the first sector after every partition (end), the
// last sector a new partition may use (last), and its GPT if it has one. It refuses unless the
// MBR is our image's (an ISO partition, then an EFI one, the other two slots empty).
func layout(d io.ReaderAt, total uint64) (end, last uint64, g gpt, hasGPT bool, err error) {
	mbr := make([]byte, sector)
	if _, err := d.ReadAt(mbr, 0); err != nil {
		return 0, 0, g, false, err
	}
	if mbr[510] != 0x55 || mbr[511] != 0xaa {
		return 0, 0, g, false, errors.New("the drive has no MBR")
	}
	var s [4]slot
	for i := range s {
		e := mbr[mbrEntries+16*i:]
		s[i] = slot{Type: e[4], Start: binary.LittleEndian.Uint32(e[8:]), Count: binary.LittleEndian.Uint32(e[12:])}
	}
	if s[0].Type == 0xee {
		return 0, 0, g, false, errors.New("the drive has a plain GPT: not an OmaBoot? image layout")
	}
	if s[1].Type != 0xef || s[0].Count == 0 || s[2] != (slot{}) || s[3] != (slot{}) {
		return 0, 0, g, false, errors.New("the drive isn't laid out as an OmaBoot? image")
	}
	for _, p := range s {
		end = max(end, uint64(p.Start)+uint64(p.Count))
	}
	g, hasGPT, err = readGPT(d)
	if err != nil {
		return 0, 0, g, false, err
	}
	last = total - 1
	if hasGPT {
		for i := uint32(0); i < g.n; i++ {
			if e := g.entry(i); !bytes.Equal(e[:16], make([]byte, 16)) {
				end = max(end, binary.LittleEndian.Uint64(e[40:])+1)
			}
		}
		last = total - 2 - g.arraySectors() // the backup array and header go at the very end
	}
	return end, last, g, hasGPT, nil
}

// Plan works out the writes that add the OMABOOT partition to a disk of total sectors, and where
// it goes. It goes at the stick's end, 1 MiB aligned, up to 64 MiB, with 1 MiB past it for any
// GPT's backup (owner, 2026-10-19; it went right after the image before 0.8.6). Writing a new
// image only covers the stick's first gigabyte or so: its partition table leaves OMABOOT out,
// but the partition itself is still there, and Plan finds it (Found) and only adds it back to
// the table, keeping the reports and settings. Plan refuses unless the MBR is our image's.
func Plan(d io.ReaderAt, total uint64) ([]Write, Room, error) {
	end, last, g, hasGPT, err := layout(d, total)
	if err != nil {
		return nil, Room{}, err
	}
	free := -1
	if hasGPT {
		for i := uint32(0); i < g.n; i++ {
			if bytes.Equal(g.entry(i)[:16], make([]byte, 16)) {
				free = int(i)
				break
			}
		}
		if free < 0 {
			return nil, Room{}, errors.New("the drive's GPT has no free entry")
		}
	}
	room, ok := locate(d, total, end, last)
	if !ok {
		lo := (end + align - 1) / align * align
		top := uint64(0)
		if last+1 > lo+reserve {
			top = (last + 1 - reserve) / align * align
		}
		count := min(top-min(top, lo), roomMax)
		if count < roomMin || top > 1<<32-1 {
			return nil, Room{}, ErrNoRoom
		}
		room = Room{Start: top - count, Count: count}
	}
	start, count := room.Start, room.Count

	var ws []Write
	if hasGPT {
		e := g.entry(uint32(free))
		copy(e[0:], basicData)
		if _, err := rand.Read(e[16:32]); err != nil {
			return nil, Room{}, err
		}
		e[16+7] = e[16+7]&0x0f | 0x40 // a version 4 GUID (mixed-endian on disk)
		e[16+8] = e[16+8]&0x3f | 0x80
		put64(e, 32, start)
		put64(e, 40, start+count-1)
		put64(e, 48, 0)
		name := utf16.Encode([]rune(RoomLabel))
		for i := 56; i < 128; i++ {
			e[i] = 0
		}
		for i, c := range name {
			binary.LittleEndian.PutUint16(e[56+2*i:], c)
		}
		backupArr := total - 1 - g.arraySectors()
		prim := append([]byte(nil), g.hdr...)
		put64(prim, 32, total-1) // the backup header's new place
		put64(prim, 48, last)    // last usable
		g.seal(prim)
		back := append([]byte(nil), prim...)
		put64(back, 24, total-1)
		put64(back, 32, 1)
		put64(back, 72, backupArr)
		g.seal(back)
		// the backup first: until the primary points at it, the old one still holds
		ws = append(ws, Write{int64(backupArr) * sector, g.arr}, Write{int64(total-1) * sector, pad(back)})
		ws = append(ws, Write{int64(g.u64(72)) * sector, g.arr}, Write{sector, pad(prim)})
	}
	m := make([]byte, 16) // MBR slot 3: not bootable, CHS "beyond 8 GB", FAT16 LBA
	copy(m, []byte{0x00, 0xfe, 0xff, 0xff, fat16LBA, 0xfe, 0xff, 0xff})
	binary.LittleEndian.PutUint32(m[8:], uint32(start))
	binary.LittleEndian.PutUint32(m[12:], uint32(count))
	ws = append(ws, Write{mbrEntries + 32, m})
	return ws, room, nil
}

// locate finds an OMABOOT partition left out of the partition table: a FAT16 volume labelled
// OMABOOT starting on a 1 MiB boundary after the image (end) in the stick's last 68 MiB, 8 to
// 64 MiB long, ending by last. Its place depends a little on the image's GPT
// (where last is), so it's searched for, not worked out.
func locate(d io.ReaderAt, total, end, last uint64) (Room, bool) {
	lo := (end + align - 1) / align * align
	b := make([]byte, sector)
	for s := total / align * align; s+roomMax+4*align >= total && s >= lo; s -= align {
		if _, err := d.ReadAt(b, int64(s)*sector); err != nil {
			continue
		}
		if n, ok := roomFAT(b); ok && s+n <= last+1 {
			return Room{Start: s, Count: n, Found: true}, true
		}
		if s < align {
			break
		}
	}
	return Room{}, false
}

// roomFAT reads a boot sector as mkfs.fat makes OMABOOT's (FAT16, 512-byte sectors, the label
// in its extended boot record): the partition's size in sectors, the volume's rounded up to 1 MiB.
func roomFAT(b []byte) (uint64, bool) {
	if len(b) < sector || b[510] != 0x55 || b[511] != 0xaa || b[0] != 0xeb && b[0] != 0xe9 ||
		binary.LittleEndian.Uint16(b[11:]) != sector || b[38] != 0x29 ||
		string(b[43:54]) != fmt.Sprintf("%-11s", RoomLabel) || string(b[54:62]) != "FAT16   " {
		return 0, false
	}
	n := uint64(binary.LittleEndian.Uint16(b[19:]))
	if n == 0 {
		n = uint64(binary.LittleEndian.Uint32(b[32:]))
	}
	n = (n + align - 1) / align * align // mkfs.fat trims the volume to whole tracks of 63 sectors
	if n < roomMin || n > roomMax {
		return 0, false
	}
	return n, true
}

// FoundRoom is an OMABOOT partition on the boot stick that a new image's write left out of the
// partition table (Plan): the stick (sdb) and where it is. Reading it needs no table entry (a
// loop device at its offset); the first save adds it back (MakeRoom).
func FoundRoom(r sysroot.Root) (string, Room, bool) {
	name, err := BootDisk(r)
	if err != nil {
		return "", Room{}, false
	}
	total, err := strconv.ParseUint(r.Read("/sys/block/"+name+"/size"), 10, 64)
	if err != nil {
		return "", Room{}, false
	}
	fh, err := os.Open("/dev/" + name)
	if err != nil {
		return "", Room{}, false
	}
	defer fh.Close()
	end, last, _, _, err := layout(fh, total)
	if err != nil {
		return "", Room{}, false
	}
	room, ok := locate(fh, total, end, last)
	return name, room, ok
}

func pad(b []byte) []byte { return append(append([]byte(nil), b...), make([]byte, sector-len(b))...) }

// MakeRoom adds the OMABOOT partition to the boot stick and formats it, or adds back the one a
// new image's write left out of the table (Plan), as it is. It refuses on any disk the safety
// rules call internal, and on any layout but our image's.
func MakeRoom(ctx context.Context, r sysroot.Root) error {
	name, err := BootDisk(r)
	if err != nil {
		return err
	}
	for _, d := range safety.Disks(r) {
		if d.Name == name && d.Internal {
			return fmt.Errorf("%s is an internal disk: not touching it", name)
		}
	}
	_, err = RoomOn(ctx, "/dev/"+name, true)
	return err
}

// ErrNoneFound means RoomOn was asked only to put back an OMABOOT partition, and found none.
var ErrNoneFound = errors.New("no OMABOOT partition left out of the table")

// RoomOn is MakeRoom on the stick dev (/dev/sdb), with no checks of which disk it is: the OMABOOT
// partition put back in the table if a new image's write left it out, else, with create, a new
// one made and formatted. A stick writer uses it too, to keep a stick's reports (through
// tools/omaboot-room): one implementation.
func RoomOn(ctx context.Context, dev string, create bool) (Room, error) {
	name := path.Base(dev)
	b, err := os.ReadFile("/sys/block/" + name + "/size")
	if err != nil {
		return Room{}, fmt.Errorf("size of %s: %w", name, err)
	}
	total, err := strconv.ParseUint(strings.TrimSpace(string(b)), 10, 64)
	if err != nil {
		return Room{}, fmt.Errorf("size of %s: %w", name, err)
	}
	fh, err := os.OpenFile(dev, os.O_RDWR|os.O_SYNC, 0)
	if err != nil {
		return Room{}, err
	}
	defer fh.Close()
	ws, room, err := Plan(fh, total)
	if err != nil {
		return Room{}, err
	}
	if !room.Found && !create {
		return Room{}, ErrNoneFound
	}
	for _, w := range ws {
		if _, err := fh.WriteAt(w.Data, w.Off); err != nil {
			return Room{}, fmt.Errorf("writing the partition table: %w", err)
		}
	}
	if err := fh.Sync(); err != nil {
		return Room{}, err
	}
	part := dev + "3"
	if c := name[len(name)-1]; c >= '0' && c <= '9' {
		part = dev + "p3" // a disk name ending in a digit: mmcblk0p3
	}
	run := func(d time.Duration, name string, args ...string) (string, error) {
		c, cancel := context.WithTimeout(ctx, d)
		defer cancel()
		out, err := exec.CommandContext(c, name, args...).CombinedOutput()
		if err != nil {
			return "", fmt.Errorf("%s: %s", name, strings.TrimSpace(string(out)))
		}
		return strings.TrimSpace(string(out)), nil
	}
	// the image's partitions are in use, so the kernel can't re-read the whole table: add the
	// new one on its own (Linux reads the MBR: its slot 3 is partition 3)
	if _, err := run(10*time.Second, "partx", "--add", "--nr", "3", dev); err != nil {
		return Room{}, err
	}
	run(10*time.Second, "udevadm", "settle", "--timeout=10")
	if room.Found { // the reports and settings from before a new image was written: kept
		return room, nil
	}
	// Format it through a loop device on the partition. mkfs.fat opens what it formats
	// exclusively, and the kernel refuses that on a partition of a disk that's claimed whole:
	// the live system mounts its image from the stick, from the whole disk on some boots (the
	// room smoke test, 0.6.4 dev, 2026-10-14: busy for 30 s with nothing holding the partition
	// itself). A loop device opens the partition without claiming it.
	loop, err := run(10*time.Second, "losetup", "--find", "--show", part)
	if err != nil {
		return Room{}, err
	}
	detached := false
	detach := func() {
		if !detached {
			detached = true
			run(10*time.Second, "losetup", "--detach", loop)
		}
	}
	defer detach()
	// udev's probe of the new loop device can still hold it for a moment: try again for up to
	// 30 s, waiting a little longer each time; if it's still busy, the error says what holds it
	give := time.Now().Add(30 * time.Second)
	for try := 1; ; try++ {
		_, err := run(60*time.Second, "mkfs.fat", "-F", "16", "-n", RoomLabel, loop)
		if err == nil {
			break
		}
		if !strings.Contains(err.Error(), "busy") {
			return Room{}, err
		}
		if time.Now().After(give) {
			return Room{}, fmt.Errorf("%w (%d tries, through %s; %s)", err, try, loop, heldBy(dev, part, loop))
		}
		select {
		case <-ctx.Done():
			return Room{}, err
		case <-time.After(time.Duration(min(try, 4)) * 750 * time.Millisecond):
		}
		run(10*time.Second, "udevadm", "settle", "--timeout=10")
	}
	detach() // flushes it to the partition
	run(10*time.Second, "udevadm", "settle", "--timeout=10")
	return room, nil
}

// heldBy says what holds the partition part (/dev/sda3), its disk dev or the loop device on it
// open: where they're mounted, the devices stacked on the partition (holders), and the
// processes with them open, "/dev/sda mounted on /run/archiso/bootmnt; /dev/loop1 open in
// systemd-udevd (412)", or "nothing seen holding it".
func heldBy(dev, part, loop string) string {
	var seen []string
	if b, err := os.ReadFile("/proc/self/mounts"); err == nil {
		for _, l := range strings.Split(string(b), "\n") {
			if f := strings.Fields(l); len(f) > 1 && (f[0] == dev || f[0] == part || f[0] == loop) {
				seen = append(seen, f[0]+" mounted on "+f[1])
			}
		}
	}
	if hs, err := os.ReadDir("/sys/class/block/" + path.Base(part) + "/holders"); err == nil {
		for _, h := range hs {
			seen = append(seen, path.Base(part)+" under "+h.Name())
		}
	}
	fds, _ := filepath.Glob("/proc/[0-9]*/fd/*")
	open := map[string]bool{}
	for _, fd := range fds {
		if t, err := os.Readlink(fd); err == nil && t == part {
			pid := strings.Split(fd, "/")[2]
			comm, _ := os.ReadFile("/proc/" + pid + "/comm")
			if w := fmt.Sprintf("open in %s (%s)", strings.TrimSpace(string(comm)), pid); !open[w] {
				open[w] = true
				seen = append(seen, w)
			}
		}
	}
	if len(seen) == 0 {
		return "nothing seen holding it"
	}
	return strings.Join(seen, "; ")
}
