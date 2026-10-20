package handson

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/omaboot/boot-live/internal/fsread"
)

// The demo's disks: a Fake reads a scripted disk through the same read (readPasses) as the Mac,
// on made-up bytes, at a made-up speed, in the run's own time, so the demo and tools/screens
// show reads in progress, their speeds and their failures the same way every time.

// FakeDisk is how a scripted disk reads.
type FakeDisk struct {
	MBps     float64 // how fast it reads (0: by its USB speed)
	Image    bool    // an OmaBoot? image with its checksum list on it (the stick)
	NoList   bool    // an image without the list (an older build): read twice
	BadChunk int     // an image whose chunk BadChunk (from 1) reads back wrong
	IOErrAt  int64   // an I/O error this many bytes in
	StallAt  int64   // it stops answering this many bytes in
	Flaky    int     // a second read of MiB Flaky (from 1) comes back different
	Short    int64   // it gives no bytes past this many, though it says it's bigger
	TestFile bool    // OmaBoot?'s test file is on it (FAT32), at fakeFileAt
	BadFile  int64   // the test file's pattern reads back wrong this many bytes in (from 1)
}

// FakeDiskOn puts disk name on the USB device at dev, size bytes, reading as d.
func FakeDiskOn(f *Fake, dev, name string, size int64, d FakeDisk) {
	f.Set(usbDevices+dev+":1.0/host9/target9:0:0/9:0:0:0/block/"+name+"/size", "0")
	f.Set("/sys/block/"+name+"/size", fmt.Sprint(size/512))
	f.Disks[name] = d
}

// fakeStickSize is the demo's sticks: 32 GB ones.
const fakeStickSize = 30944000000

// nextFakeDisk is the first sdX name not on the Fake yet (sdb is the OmaBoot? stick's).
func nextFakeDisk(f *Fake) string {
	for c := 'c'; c <= 'z'; c++ {
		if _, ok := f.Files["/sys/block/sd"+string(c)+"/size"]; !ok {
			return "sd" + string(c)
		}
	}
	return "sdz"
}

// fakeUSBSpeed is how fast a demo disk on a USB device of speed sp reads, MB/s.
func fakeUSBSpeed(sp string) float64 {
	switch sp {
	case "1.5":
		return 0.15
	case "12":
		return 1
	case "480":
		return 33
	case "5000":
		return 141
	case "10000", "20000":
		return 380
	}
	return 30
}

func (f *Fake) ReadDisk(name string, fileOnly bool) DiskJob {
	d := f.Disks[name]
	if d.MBps == 0 {
		d.MBps = fakeUSBSpeed(f.Read(usbDevices + usbDisks(f)[name] + "/speed"))
	}
	return &fakeJob{f: f, name: name, at: f.now, run: fakeRead(d, diskSize(f, name), fileOnly)}
}

// fakeTick is a read's progress at a time into it.
type fakeTick struct {
	t time.Duration
	p ReadProgress
}

// fakeRun is a scripted read played through once: its progress over time and its result (nil:
// it stalled after the last tick).
type fakeRun struct {
	ticks []fakeTick
	res   *DiskRead
	end   time.Duration
}

var fakeRuns sync.Map // "spec/size" → *fakeRun: each kind of disk is read once per process

// fakeRead plays a read of a disk like d, size bytes, through readPasses.
func fakeRead(d FakeDisk, size int64, fileOnly bool) *fakeRun {
	key := fmt.Sprintf("%+v/%d/%v", d, size, fileOnly)
	if r, ok := fakeRuns.Load(key); ok {
		return r.(*fakeRun)
	}
	v := &virtDisk{d: d, size: size, seen: map[int64]int{}}
	v.now = 80 * time.Millisecond // opening it
	run := &fakeRun{}
	r := readPasses(v, readHow{Base: "arch", Sector: 512, FileOnly: fileOnly}, func() time.Duration { return v.now }, func(p ReadProgress) bool {
		run.ticks = append(run.ticks, fakeTick{v.now, p})
		return true
	})
	if r.Kind != failStop {
		run.res, run.end = &r, v.now
	}
	fakeRuns.Store(key, run)
	return run
}

// fakeJob is a scripted read in the run's time.
type fakeJob struct {
	f    *Fake
	name string
	at   float64 // the run's time it began
	run  *fakeRun
}

func (j *fakeJob) since() time.Duration {
	return time.Duration((j.f.now - j.at) * float64(time.Second))
}

// tick is the last progress by now and when it was.
func (j *fakeJob) tick() fakeTick {
	e, t := j.since(), fakeTick{}
	for _, k := range j.run.ticks {
		if k.t > e {
			break
		}
		t = k
	}
	return t
}

func (j *fakeJob) Progress() ReadProgress { return j.tick().p }

func (j *fakeJob) Ready() (DiskRead, bool) {
	e := j.since()
	if j.run.res != nil && e >= j.run.end {
		r := *j.run.res
		r.Disk = j.name
		return r, true
	}
	t := j.tick()
	if _, ok := j.f.Files["/sys/block/"+j.name+"/size"]; !ok { // unplugged mid-read
		return DiskRead{Disk: j.name, Way: t.p.Way, Bytes: t.p.Total, Kind: failIO, Pass: max(t.p.Pass, 1), At: t.p.Done,
			Fail: fmt.Sprintf("an I/O error %d MiB into pass %d: no such device", t.p.Done>>20, max(t.p.Pass, 1))}, true
	}
	return stalled(j.name, t.p, 0, t.t, e)
}

func (j *fakeJob) Stop() {}

// virtDisk is a FakeDisk's bytes: made-up content (an ISO 9660 image with airootfs.sfs and its
// checksum list for an image), read on a clock that runs at the disk's speed.
type virtDisk struct {
	d    FakeDisk
	size int64
	now  time.Duration
	seen map[int64]int // reads of each block, for Flaky
}

var errFakeStall = errors.New("stalled")

func (v *virtDisk) Size() int64 { return v.size }

func (v *virtDisk) ReadAt(p []byte, off int64) (int, error) {
	n := int64(len(p))
	if v.d.StallAt > 0 && off+n > v.d.StallAt {
		v.now = 1 << 40 // never answers: the job's clock judges it
		return 0, errFakeStall
	}
	v.now += time.Duration(float64(n) / (v.d.MBps * 1e6) * float64(time.Second))
	if v.d.IOErrAt > 0 && off+n > v.d.IOErrAt {
		k := max(0, v.d.IOErrAt-off) &^ (align - 1)
		v.fill(p[:k], off)
		return int(k), errors.New("input/output error")
	}
	limit := v.size
	if v.d.Short > 0 {
		limit = v.d.Short
	}
	var err error
	if off+n > limit {
		n, err = max(0, limit-off), io.EOF
	}
	v.fill(p[:n], off)
	v.seen[off]++
	if fl := int64(v.d.Flaky-1) << 20; v.d.Flaky > 0 && v.seen[off] > 1 && off <= fl && fl < off+n {
		p[fl-off] ^= 0x5a
	}
	return int(n), err
}

// fakeSFSAt is where the image's airootfs.sfs starts (sector 32), fakeSFSSize its size.
const (
	fakeSFSAt   = 32 * isoSector
	fakeSFSSize = 1288490188
)

// fakeFileAt is where the demo's test file is on a disk that has one: one piece, 8 MiB in.
const fakeFileAt = 8 << 20

var (
	fakeTestFile     []byte
	fakeTestFileOnce sync.Once
)

// testFile is where the made-up disk's test file is (it has no file system to read).
func (v *virtDisk) testFile() testFileHit {
	if !v.d.TestFile || v.d.Image {
		return testFileHit{Note: "no test file in the top folder of FAT32, MBR partition 1"}
	}
	return testFileHit{File: &fsread.File{Name: testFileName, Size: testFileHead + testFileBody,
		Extents: []fsread.Extent{{Off: fakeFileAt, Len: testFileHead + testFileBody}}, Vol: fsread.Volume{FS: "FAT32", Part: "MBR partition 1", Read: true}}}
}

// fill writes the disk's bytes at off into p.
func (v *virtDisk) fill(p []byte, off int64) {
	if v.d.TestFile && !v.d.Image {
		fakeFill(p, off, 0xd15c)
		fakeTestFileOnce.Do(func() { fakeTestFile = TestFile() })
		lo, hi := max(off, fakeFileAt), min(off+int64(len(p)), fakeFileAt+int64(len(fakeTestFile)))
		if lo < hi {
			copy(p[lo-off:hi-off], fakeTestFile[lo-fakeFileAt:])
		}
		if bad := fakeFileAt + testFileHead + v.d.BadFile - 1; v.d.BadFile > 0 && bad >= off && bad < off+int64(len(p)) {
			p[bad-off] ^= 0xff
		}
		return
	}
	if !v.d.Image {
		fakeFill(p, off, 0xd15c)
		return
	}
	if off < fakeSFSAt {
		k := copy(p, fakeISOHead(!v.d.NoList)[off:])
		p, off = p[k:], off+int64(k)
	}
	if len(p) == 0 {
		return
	}
	s := off - fakeSFSAt
	fakeFill(p, s, 0x5f5)
	if v.d.BadChunk > 0 {
		if bad := int64(v.d.BadChunk-1)<<20 + 0x123; bad >= s && bad < s+int64(len(p)) {
			p[bad-s] ^= 0xff
		}
	}
}

// fakeFill is made-up content: each 8-byte word a splitmix64 of its place (base is p[0]'s).
func fakeFill(p []byte, base int64, seed uint64) {
	for i := 0; i < len(p); {
		o := base + int64(i)
		z := uint64(o>>3) + seed*0x9e3779b97f4a7c15
		z = (z ^ (z >> 30)) * 0xbf58476d1ce4e5b9
		z = (z ^ (z >> 27)) * 0x94d049bb133111eb
		z ^= z >> 31
		for k := o & 7; k < 8 && i < len(p); k++ {
			p[i] = byte(z >> (8 * uint(k)))
			i++
		}
	}
}

var (
	fakeHeads    [2][]byte
	fakeHeadOnce [2]sync.Once
)

// fakeISOHead is the demo image's first 32 sectors: the volume descriptors, the directories
// down to arch/x86_64 and, with list, airootfs.chunks for its airootfs.sfs.
func fakeISOHead(list bool) []byte {
	k := 0
	if list {
		k = 1
	}
	fakeHeadOnce[k].Do(func() {
		var l []byte
		if list {
			sfs := make([]byte, 64<<20)
			fakeFill(sfs, 0, 0x5f5)
			l = ChunkList(sfs, fakeSFSSize)
		}
		fakeHeads[k] = isoHead(l, fakeSFSAt/isoSector, fakeSFSSize)
	})
	return fakeHeads[k]
}

// ChunkList is an image's checksum list for airootfs.sfs (size bytes) whose first bytes are
// head: a line per whole 1 MiB chunk of head, up to 64 (the format in diskread.go; the image
// build writes the same with builder/omaboot/tools/chunk-manifest.sh).
func ChunkList(head []byte, size int64) []byte {
	var b strings.Builder
	b.WriteString("# OmaBoot? checksum list: sha256 of each 1 MiB chunk of airootfs.sfs's first 64 MiB\n")
	fmt.Fprintf(&b, "omaboot-chunks 1\nfile airootfs.sfs\nsize %d\nchunk %d\n", size, chunkLen)
	for i := 0; i < 64 && (i+1)*chunkLen <= len(head); i++ {
		s := sha256.Sum256(head[i*chunkLen : (i+1)*chunkLen])
		fmt.Fprintf(&b, "%d %s\n", i, hex.EncodeToString(s[:]))
	}
	return []byte(b.String())
}

// isoHead builds an ISO 9660 image's first 32 sectors: the primary volume descriptor and its
// terminator, the root, arch and arch/x86_64 directories, airootfs.chunks (list; none when nil)
// at sector 21 and airootfs.sfs's record (sfsSize bytes at sector sfsLBA).
func isoHead(list []byte, sfsLBA, sfsSize int64) []byte {
	b := make([]byte, 32*isoSector)
	pvd := b[16*isoSector:]
	pvd[0], pvd[6] = 1, 1
	copy(pvd[1:], "CD001")
	copy(pvd[156:], isoRec("\x00", 18, isoSector, true))
	term := b[17*isoSector:]
	term[0], term[6] = 255, 1
	copy(term[1:], "CD001")
	dir := func(lba int64, recs ...[]byte) {
		o := lba * isoSector
		recs = append([][]byte{isoRec("\x00", lba, isoSector, true), isoRec("\x01", 18, isoSector, true)}, recs...)
		for _, r := range recs {
			o += int64(copy(b[o:], r))
		}
	}
	dir(18, isoRec("ARCH", 19, isoSector, true))
	dir(19, isoRec("X86_64", 20, isoSector, true))
	files := [][]byte{}
	if list != nil {
		files = append(files, isoRec("AIROOTFS.CHUNKS;1", 21, int64(len(list)), false))
		copy(b[21*isoSector:], list)
	}
	dir(20, append(files, isoRec("AIROOTFS.SFS;1", sfsLBA, sfsSize, false))...)
	return b
}

// isoRec is a directory record: name at sector lba, size bytes.
func isoRec(name string, lba, size int64, dir bool) []byte {
	n := len(name)
	l := 33 + n
	if l%2 == 1 {
		l++
	}
	r := make([]byte, l)
	r[0] = byte(l)
	binary.LittleEndian.PutUint32(r[2:], uint32(lba))
	binary.BigEndian.PutUint32(r[6:], uint32(lba))
	binary.LittleEndian.PutUint32(r[10:], uint32(size))
	binary.BigEndian.PutUint32(r[14:], uint32(size))
	if dir {
		r[25] = 2
	}
	r[32] = byte(n)
	copy(r[33:], name)
	return r
}

// ── the demo's USB devices ────────────────────────────────────────────────────

// fakeUSBDev puts a USB device on the Fake at path dev with one interface of class cls (08
// storage, 03 HID, ff vendor).
func fakeUSBDev(f *Fake, dev, id, maker, product, speed, cls string) {
	d := usbDevices + dev + "/"
	v, p, _ := strings.Cut(id, ":")
	f.Set(d+"idVendor", v)
	f.Set(d+"idProduct", p)
	f.Set(d+"manufacturer", maker)
	f.Set(d+"product", product)
	f.Set(d+"speed", speed)
	f.Set(d+"bDeviceClass", "00")
	switch speed {
	case "5000", "10000", "20000":
		f.Set(d+"version", "3.00")
	default:
		f.Set(d+"version", "2.00")
	}
	f.Set(d+dev+":1.0/bInterfaceClass", cls)
}

// fakeHID puts a mouse (or keyboard) on the Fake at dev, its input device event, and moves
// it at each of the times given.
func fakeHID(f *Fake, dev, id, maker, product, speed, event string, moves ...float64) {
	fakeUSBDev(f, dev, id, maker, product, speed, "03")
	bus, _, _ := strings.Cut(dev, "-")
	FakeLink(f, "/sys/class/input/"+event, "../../devices/pci0000:00/0000:00:14.0/usb"+bus+"/"+dev+"/"+dev+
		":1.0/0003:"+strings.ToUpper(id)+".0001/input/input9/"+event)
	f.Device(event, strings.TrimSpace(maker+" "+product), map[string]string{"ev": "17", "rel": "1803", "key": "1f0000 0 0 0 0"})
	for _, t := range moves {
		f.Event(t, event, EvRel, relX, 3)
		f.Event(t, event, EvRel, relY, -2)
		f.Event(t, event, EvSyn, evSynReport, 0)
	}
}

// fakeGone takes what a device leaves behind off the Fake: its disks and its input devices.
func fakeGone(f *Fake, dev string) {
	for disk, on := range usbDisks(f) {
		if on == dev || strings.HasPrefix(on, dev+".") {
			delete(f.Files, "/sys/block/"+disk+"/size")
		}
	}
	for p, t := range f.Files {
		ev, ok := strings.CutPrefix(p, fakeLink+"/sys/class/input/")
		if ok && (strings.Contains(t, "/"+dev+"/") || strings.Contains(t, "/"+dev+".")) {
			delete(f.Files, p)
			for q := range f.Files {
				if strings.HasPrefix(q, "/sys/class/input/"+path.Base(ev)+"/") {
					delete(f.Files, q)
				}
			}
		}
	}
}
