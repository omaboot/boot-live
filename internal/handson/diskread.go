package handson

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"sync"
	"time"
	"unsafe"

	"github.com/omaboot/boot-live/internal/fsread"
)

// The USB port tests' raw read: every USB disk a port sees is read straight
// off the whole-disk device, opened read-only and with O_DIRECT so each read reaches the device
// and not the page cache. Nothing is mounted and nothing is ever written (diskread_linux.go
// opens the device O_RDONLY; the package's tests check that no code path can write a disk).
//
// Two ways to know the bytes came back right:
//
//   - an OmaBoot? image (the stick, wherever it is plugged in): the image carries a checksum
//     list, arch/x86_64/airootfs.chunks, written by the image build next to airootfs.sfs:
//
//     # comments
//     omaboot-chunks 1
//     file airootfs.sfs
//     size <airootfs.sfs's size in bytes>
//     chunk <chunk size in bytes, 1048576>
//     <index from 0> <sha256 of that chunk of airootfs.sfs, lower-case hex>
//     …
//
//     one line per whole chunk of the file's first 64 MiB. The read finds airootfs.sfs and the
//     list in the ISO 9660 directory on the raw device (the boot medium isn't mounted with
//     copytoram), reads the first 32 MiB of airootfs.sfs and checks each chunk: "verified
//     against the image's checksums";
//   - OmaBoot?'s test file in the top folder of the disk's file system (testfile.go): the
//     file's 32 MiB of pattern read and checked byte for byte: "test file verified";
//   - anything else (or an older image without the list): the disk's first 32 MiB read twice
//     and the passes compared: "read twice, identical".
//
// The SD card and FireWire tests read a card or drive only for the test file (fileOnly): with
// none on it, nothing is read but its file system's structures.
//
// A read that errors, comes up short, makes no progress for 5 s or takes longer than 150 s in
// all, or whose passes (or chunks) don't match, failed: the port test marks its port partial.

const (
	readLen    = 32 << 20 // bytes read per pass
	chunkLen   = 1 << 20  // read twice: the size the passes are compared in
	blockLen   = 1 << 20  // one read call
	align      = 4096     // offsets, lengths and buffers, for O_DIRECT on any logical block size
	stallAfter = 5 * time.Second
	readLimit  = 150 * time.Second // both passes in all; a USB 1.1 disk reads about 1 MB/s
	listMax    = 64 << 10          // the most of a checksum list read
)

// Ways a disk was read (DiskRead.Way).
const (
	WayTwice = "twice" // its first 32 MiB read twice and compared
	WayImage = "image" // an OmaBoot? image's airootfs.sfs, checked against its checksum list
	WayFile  = "file"  // OmaBoot?'s test file, checked against its pattern
)

// Why a read failed (DiskRead.Kind), for the screen's words.
const (
	failOpen   = "open"    // the device couldn't be opened
	failIO     = "io"      // an I/O error
	failShort  = "short"   // the device gave fewer bytes than asked
	failStall  = "stall"   // no progress for stallAfter
	failSlow   = "timeout" // not done within readLimit
	failDiffer = "differ"  // the two passes differ
	failImage  = "image"   // a chunk doesn't match the image's checksum
	failFile   = "file"    // the test file's bytes aren't its pattern
	failStop   = "stopped" // the read was abandoned (the screen left, or it stalled)
)

// DiskRead is how a raw read of a disk went.
type DiskRead struct {
	Disk  string        // sdc
	Way   string        // WayTwice or WayImage; "" when nothing was read
	Bytes int64         // bytes read (per pass)
	Took  time.Duration // the first pass
	Took2 time.Duration // the second pass (read twice)
	Sum   string        // sha256 of the bytes read in the first pass
	Kind  string        // why it failed ("" when it went fine)
	Fail  string        // the same, in English for the report
	Pass  int           // the pass it failed in
	At    int64         // how far into that pass it got
	// NoMedium: the device has no medium (an empty card reader); nothing to read.
	NoMedium bool
	// Skipped says why nothing was read (a fixture run: not on the Mac); "" when it was.
	Skipped string
	Image   bool   // an OmaBoot? image is on it
	Note    string // English, for the report: why an image was read twice, or why a test file wasn't used
	FileAt  string // the test file read: "OMABOOT-TEST.BIN (FAT32, MBR partition 1)"
	// NoFile: a read for the test file only (fileOnly) found none; Note says why.
	NoFile bool
}

// OK reports whether the read went through and checked out.
func (r DiskRead) OK() bool { return r.Way != "" && r.Kind == "" && !r.NoMedium && r.Skipped == "" }

// MBps is the first pass's speed in MB/s (10⁶ bytes).
func (r DiskRead) MBps() float64 {
	if r.Took <= 0 {
		return 0
	}
	return float64(r.Bytes) / r.Took.Seconds() / 1e6
}

// BootRead is how fast the OmaBoot? drive read at a few places (BootDriveSpeed).
type BootRead struct {
	Disk  string
	Image bool // read inside airootfs.sfs
	Bytes int64
	Took  time.Duration
	Reads []string // each sample: "412 MiB in: 16.6 MB/s"
	Err   string
}

// MBps is the speed over the samples in MB/s (10⁶ bytes), 0 when nothing was read.
func (r BootRead) MBps() float64 {
	if r.Took <= 0 || r.Bytes == 0 {
		return 0
	}
	return float64(r.Bytes) / r.Took.Seconds() / 1e6
}

// SlowDrive is the speed below which the OmaBoot? drive is called slow: a USB 2 stick reads
// 10 MB/s or more; under 3, programs and drivers loading from it can make checks time out.
const SlowDrive = 3.0

// ReadProgress is where a read is.
type ReadProgress struct {
	Way   string // "" until it knows
	Pass  int    // 1, or 2 for read twice's second pass
	Done  int64  // bytes this pass
	Total int64
}

// DiskJob is a raw read under way; none of its methods block.
type DiskJob interface {
	Progress() ReadProgress
	Ready() (DiskRead, bool)
	Stop() // the screen left: the read stops at its next block, and the device is closed
}

// diskReader is an IO that can read a disk raw: the Mac's (System) and the demo's (Fake).
// fileOnly reads only OmaBoot?'s test file, if it's there (the SD card and FireWire tests).
type diskReader interface {
	ReadDisk(name string, fileOnly bool) DiskJob
}

// readDisk starts a raw read of disk name (sdc) on io; an IO that can't answers at once that
// nothing was read.
func readDisk(io IO, name string) DiskJob { return startRead(io, name, false) }

// readTestFile starts a read of the test file on disk name, if it has one.
func readTestFile(io IO, name string) DiskJob { return startRead(io, name, true) }

func startRead(io IO, name string, fileOnly bool) DiskJob {
	if r, ok := io.(diskReader); ok {
		return r.ReadDisk(name, fileOnly)
	}
	return doneJob{DiskRead{Disk: name, Skipped: "nothing is read here"}}
}

// doneJob is a read that answered at once.
type doneJob struct{ r DiskRead }

func (d doneJob) Progress() ReadProgress  { return ReadProgress{} }
func (d doneJob) Ready() (DiskRead, bool) { return d.r, true }
func (d doneJob) Stop()                   {}

// ── the job ───────────────────────────────────────────────────────────────────

// blockDev is a whole disk opened read-only. Reads are aligned (alignedBuf, align).
type blockDev interface {
	ReadAt(p []byte, off int64) (int, error)
	Size() int64
}

// cacheDropper is a blockDev opened without O_DIRECT (a device that refused it): its page
// cache is dropped before each pass, so the second pass reads the device again.
type cacheDropper interface {
	DropCache()
}

// rawJob runs a read on its own goroutine and is polled from the screen's. The stall and time
// limits are judged here, by the clock, so a read stuck inside the kernel still ends.
type rawJob struct {
	mu          sync.Mutex
	disk        string
	clock       func() time.Duration
	prog        ReadProgress
	start, last time.Duration
	res         *DiskRead
	stop        bool
}

// startJob runs read on its own goroutine; read reports each step to its argument, which
// answers false once the job is given up (stopped or stalled).
func startJob(disk string, clock func() time.Duration, read func(prog func(ReadProgress) bool) DiskRead) *rawJob {
	j := &rawJob{disk: disk, clock: clock}
	j.start = clock()
	j.last = j.start
	go func() {
		r := read(j.progress)
		r.Disk = disk
		j.mu.Lock()
		if j.res == nil && r.Kind != failStop {
			j.res = &r
		}
		j.mu.Unlock()
	}()
	return j
}

func (j *rawJob) progress(p ReadProgress) bool {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.stop || j.res != nil {
		return false
	}
	j.prog, j.last = p, j.clock()
	return true
}

func (j *rawJob) Progress() ReadProgress {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.prog
}

func (j *rawJob) Ready() (DiskRead, bool) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.res != nil {
		return *j.res, true
	}
	if r, ok := stalled(j.disk, j.prog, j.start, j.last, j.clock()); ok {
		j.res = &r
		return r, true
	}
	return DiskRead{}, false
}

func (j *rawJob) Stop() {
	j.mu.Lock()
	j.stop = true
	j.mu.Unlock()
}

// stalled is the rule for a read that doesn't end: no progress for stallAfter, or longer than
// readLimit in all, is a failed read; ok false while neither.
func stalled(disk string, p ReadProgress, start, last, now time.Duration) (DiskRead, bool) {
	r := DiskRead{Disk: disk, Way: p.Way, Bytes: p.Total, Pass: max(p.Pass, 1), At: p.Done}
	switch {
	case now-last >= stallAfter:
		r.Kind = failStall
		r.Fail = fmt.Sprintf("it stalled: no progress for %d s, %d MiB into pass %d", int(stallAfter.Seconds()), p.Done>>20, r.Pass)
	case now-start >= readLimit:
		r.Kind = failSlow
		r.Fail = fmt.Sprintf("it didn't finish within %d s (%d MiB into pass %d)", int(readLimit.Seconds()), p.Done>>20, r.Pass)
	default:
		return DiskRead{}, false
	}
	return r, true
}

// ── the read ──────────────────────────────────────────────────────────────────

// readHow is what a read looks for: the image's directory (archisobasedir, "arch"), the disk's
// logical block size (for its partition table), and whether it reads the test file only.
type readHow struct {
	Base     string
	Sector   int
	FileOnly bool
}

// readPasses reads dev the way it can (see the top of the file). clock times the passes; prog
// hears each step and stops the read by answering false.
func readPasses(dev blockDev, how readHow, clock func() time.Duration, prog func(ReadProgress) bool) DiskRead {
	var r DiskRead
	size := dev.Size()
	if size <= 0 {
		r.NoMedium = true
		return r
	}
	if !prog(ReadProgress{Pass: 1}) {
		r.Kind = failStop
		return r
	}
	img, isImg := findImage(dev, how.Base)
	r.Image = isImg
	var list *chunkList
	if isImg {
		switch {
		case img.ListSize == 0:
			r.Note = "an OmaBoot? image without checksums (an older build), so read twice"
		case img.ListSize > listMax:
			r.Note = "the image's checksum list is too big to be one, so read twice"
		default:
			b, err := readRange(dev, img.ListOff, img.ListSize)
			if err != nil {
				r.Kind, r.Pass = failIO, 1
				r.Fail = fmt.Sprintf("the image's checksum list didn't read: %v", err)
				return r
			}
			l, err := parseChunks(b)
			switch {
			case err != nil:
				r.Note = fmt.Sprintf("the image's checksum list didn't parse (%v), so read twice", err)
			case l.Size != img.Size:
				r.Note = "the image's checksum list is for another airootfs.sfs, so read twice"
			default:
				list = l
			}
		}
	}
	if list != nil {
		n := min(int64(readLen), int64(len(list.Sums))*list.Chunk, img.Size)
		n -= n % list.Chunk
		if n > 0 {
			r.Way, r.Bytes = WayImage, n
			h := hashRange(dev, img.Off, n, list.Chunk, 1, WayImage, clock, prog)
			r.Took, r.Sum = h.took, h.sum
			if h.kind != "" {
				return h.failed(r)
			}
			for i, s := range h.sums {
				if s != list.Sums[i] {
					r.Kind, r.Pass, r.At = failImage, 1, int64(i)*list.Chunk
					r.Fail = fmt.Sprintf("chunk %d of airootfs.sfs (%d MiB in) doesn't match the image's checksum", i, int64(i)*list.Chunk>>20)
					return r
				}
			}
			return r
		}
	}
	if !isImg {
		hit := findTestFile(dev, how.Sector, prog)
		if hit.File != nil {
			return checkTestFile(dev, hit.File, clock, prog)
		}
		if how.FileOnly || !strings.HasPrefix(hit.Note, "no test file") {
			r.Note = hit.Note
		}
	}
	if !prog(ReadProgress{Pass: 1}) {
		r.Kind = failStop
		return r
	}
	if how.FileOnly {
		r.NoFile = true
		return r
	}
	n := min(int64(readLen), size&^(align-1))
	r.Way, r.Bytes = WayTwice, n
	if d, ok := dev.(cacheDropper); ok {
		d.DropCache()
	}
	a := hashRange(dev, 0, n, chunkLen, 1, WayTwice, clock, prog)
	r.Took, r.Sum = a.took, a.sum
	if a.kind != "" {
		return a.failed(r)
	}
	if d, ok := dev.(cacheDropper); ok {
		d.DropCache()
	}
	b := hashRange(dev, 0, n, chunkLen, 2, WayTwice, clock, prog)
	r.Took2 = b.took
	if b.kind != "" {
		return b.failed(r)
	}
	if a.sum != b.sum {
		k := 0
		for k < len(a.sums) && k < len(b.sums) && a.sums[k] == b.sums[k] {
			k++
		}
		r.Kind, r.Pass, r.At = failDiffer, 2, int64(k)*chunkLen
		r.Fail = fmt.Sprintf("the two passes differ (sha256 %s… then %s…), first in the MiB at %d MiB", a.sum[:12], b.sum[:12], k)
	}
	return r
}

// hashed is one pass: each chunk's sha256, the whole pass's, and how it went.
type hashed struct {
	sums       []string
	sum        string
	took       time.Duration
	kind, fail string
	pass       int
	at         int64
	cut        bool // the sink ended the read
}

// failed puts a pass's failure on r.
func (h hashed) failed(r DiskRead) DiskRead {
	r.Kind, r.Fail, r.Pass, r.At = h.kind, h.fail, h.pass, h.at
	return r
}

// hashRange reads n bytes from off in aligned blocks and hashes them, chunk by chunk.
func hashRange(dev blockDev, off, n, chunk int64, pass int, way string, clock func() time.Duration, prog func(ReadProgress) bool) hashed {
	whole, ch := sha256.New(), sha256.New()
	var inChunk int64
	var sums []string
	h := scanSpans(dev, []fsread.Extent{{Off: off, Len: n}}, pass, way, clock, prog, func(p []byte, _ int64) bool {
		whole.Write(p)
		for len(p) > 0 {
			take := min(chunk-inChunk, int64(len(p)))
			ch.Write(p[:take])
			inChunk += take
			p = p[take:]
			if inChunk == chunk {
				sums = append(sums, hex.EncodeToString(ch.Sum(nil)))
				ch.Reset()
				inChunk = 0
			}
		}
		return true
	})
	if h.kind != "" {
		return h
	}
	if inChunk > 0 {
		sums = append(sums, hex.EncodeToString(ch.Sum(nil)))
	}
	h.sums, h.sum = sums, hex.EncodeToString(whole.Sum(nil))
	return h
}

// scanSpans reads the spans' bytes in order, in aligned blocks, handing each piece to sink with
// how far into them it starts; sink answers false to end the read there (h.cut).
func scanSpans(dev blockDev, spans []fsread.Extent, pass int, way string, clock func() time.Duration, prog func(ReadProgress) bool, sink func(p []byte, at int64) bool) hashed {
	h := hashed{pass: pass}
	var n int64
	for _, sp := range spans {
		n += sp.Len
	}
	buf := alignedBuf(blockLen)
	var done int64
	t0 := clock()
	if !prog(ReadProgress{Way: way, Pass: pass, Total: n}) {
		h.kind = failStop
		return h
	}
	for _, sp := range spans {
		start := sp.Off &^ (align - 1)
		skip := sp.Off - start
		end := (sp.Off + sp.Len + align - 1) &^ (align - 1)
		left := sp.Len
		for pos := start; pos < end; {
			want := min(int64(blockLen), end-pos)
			k, err := dev.ReadAt(buf[:want], pos)
			if int64(k) < want {
				h.at = done
				if err == nil || errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
					h.kind = failShort
					h.fail = fmt.Sprintf("a short read: the device answered %d of %d bytes at %d MiB in", k, want, done>>20)
				} else {
					h.kind = failIO
					h.fail = fmt.Sprintf("an I/O error %d MiB into pass %d: %v", done>>20, pass, err)
				}
				return h
			}
			p := buf[:want]
			if pos == start {
				p = p[skip:]
			}
			if int64(len(p)) > left {
				p = p[:left]
			}
			if !sink(p, done) {
				h.cut, h.took = true, clock()-t0
				return h
			}
			done += int64(len(p))
			left -= int64(len(p))
			pos += want
			if !prog(ReadProgress{Way: way, Pass: pass, Done: done, Total: n}) {
				h.kind = failStop
				return h
			}
		}
	}
	h.took = clock() - t0
	return h
}

// checkTestFile reads the test file's stream (f is the file, its header already checked) and
// compares it with the pattern, byte for byte.
func checkTestFile(dev blockDev, f *fsread.File, clock func() time.Duration, prog func(ReadProgress) bool) DiskRead {
	r := DiskRead{Way: WayFile, Bytes: testFileBody, FileAt: f.Name + " (" + f.Vol.String() + ")"}
	g, want := newStream(), make([]byte, blockLen)
	bad := int64(-1)
	h := scanSpans(dev, testFileSpans(f.Extents), 1, WayFile, clock, prog, func(p []byte, at int64) bool {
		w := want[:len(p)]
		g.fill(w)
		if i := firstDiff(p, w); i >= 0 {
			bad = at + int64(i)
			return false
		}
		return true
	})
	r.Took = h.took
	if h.kind != "" {
		return h.failed(r)
	}
	if bad >= 0 {
		r.Kind, r.Pass, r.At = failFile, 1, bad
		r.Fail = fmt.Sprintf("the test file's bytes aren't its pattern from %d KiB into its 32 MiB (%s)", bad>>10, r.FileAt)
	}
	return r
}

// alignedBuf is an n-byte buffer starting on an align boundary, as O_DIRECT wants.
func alignedBuf(n int) []byte {
	b := make([]byte, n+align)
	k := int(uintptr(unsafe.Pointer(&b[0])) & (align - 1))
	if k != 0 {
		k = align - k
	}
	return b[k : k+n : k+n]
}

// readRange reads n bytes at off (any offset; the device is read in aligned blocks).
func readRange(dev blockDev, off, n int64) ([]byte, error) {
	start := off &^ (align - 1)
	end := (off + n + align - 1) &^ (align - 1)
	buf := alignedBuf(int(end - start))
	k, err := dev.ReadAt(buf, start)
	if k < len(buf) {
		if err == nil || errors.Is(err, io.EOF) {
			err = io.ErrUnexpectedEOF
		}
		return nil, err
	}
	return buf[off-start : off-start+n], nil
}

// ── the image ─────────────────────────────────────────────────────────────────

// isoImage is where an OmaBoot? image's airootfs.sfs and its checksum list sit on a disk.
type isoImage struct {
	Off, Size         int64 // airootfs.sfs
	ListOff, ListSize int64 // airootfs.chunks; ListSize 0 when there's none
}

// isoEntry is one ISO 9660 directory record.
type isoEntry struct {
	lba, size int64
	dir       bool
}

const isoSector = 2048

// findImage finds airootfs.sfs (and airootfs.chunks) under base/x86_64 in the ISO 9660 file
// system at the start of the disk (archiso images are written to the whole stick), from the
// primary volume descriptor's root directory down.
func findImage(dev blockDev, base string) (isoImage, bool) {
	var root []byte
	for s := int64(16); s < 32 && root == nil; s++ {
		d, err := readRange(dev, s*isoSector, isoSector)
		if err != nil || string(d[1:6]) != "CD001" || d[0] == 255 {
			return isoImage{}, false
		}
		if d[0] == 1 {
			root = d[156 : 156+34]
		}
	}
	if root == nil {
		return isoImage{}, false
	}
	dir := isoEntry{lba: int64(binary.LittleEndian.Uint32(root[2:])), size: int64(binary.LittleEndian.Uint32(root[10:])), dir: true}
	for _, name := range []string{base, "x86_64"} {
		e, ok := isoDir(dev, dir)[strings.ToUpper(name)]
		if !ok || !e.dir {
			return isoImage{}, false
		}
		dir = e
	}
	ents := isoDir(dev, dir)
	sfs, ok := ents["AIROOTFS.SFS"]
	if !ok || sfs.dir || sfs.size == 0 {
		return isoImage{}, false
	}
	img := isoImage{Off: sfs.lba * isoSector, Size: sfs.size}
	if l, ok := ents["AIROOTFS.CHUNKS"]; ok && !l.dir {
		img.ListOff, img.ListSize = l.lba*isoSector, l.size
	}
	return img, true
}

// isoDir reads a directory's records by name (upper case, without ";1"). A file in several
// extents (over 4 GiB) keeps its first extent's place and the extents' sizes added up.
func isoDir(dev blockDev, d isoEntry) map[string]isoEntry {
	out := map[string]isoEntry{}
	b, err := readRange(dev, d.lba*isoSector, min(d.size, 1<<20))
	if err != nil {
		return out
	}
	last := ""
	for off := 0; off < len(b); {
		l := int(b[off])
		if l == 0 { // the rest of this sector is padding
			off = (off/isoSector + 1) * isoSector
			continue
		}
		if l < 34 || off+l > len(b) {
			break
		}
		rec := b[off : off+l]
		off += l
		nl := int(rec[32])
		if 33+nl > len(rec) {
			break
		}
		name := string(rec[33 : 33+nl])
		if name == "\x00" || name == "\x01" {
			continue
		}
		name, _, _ = strings.Cut(name, ";")
		name = strings.ToUpper(strings.TrimSuffix(name, "."))
		e := isoEntry{lba: int64(binary.LittleEndian.Uint32(rec[2:])), size: int64(binary.LittleEndian.Uint32(rec[10:])), dir: rec[25]&2 != 0}
		if prev, ok := out[name]; ok && name == last { // the next extent of a multi-extent file
			prev.size += e.size
			out[name] = prev
			continue
		}
		out[name], last = e, name
	}
	return out
}

// chunkList is an image's checksum list (airootfs.chunks).
type chunkList struct {
	File  string
	Size  int64
	Chunk int64
	Sums  []string
}

// parseChunks reads a checksum list (the format at the top of the file).
func parseChunks(b []byte) (*chunkList, error) {
	l := &chunkList{}
	ver := false
	for i, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || line[0] == '#' {
			continue
		}
		f := strings.Fields(line)
		if len(f) != 2 {
			return nil, fmt.Errorf("line %d isn't two fields", i+1)
		}
		switch f[0] {
		case "omaboot-chunks":
			if f[1] != "1" {
				return nil, fmt.Errorf("format %s, not 1", f[1])
			}
			ver = true
		case "file":
			l.File = f[1]
		case "size", "chunk":
			n, err := strconv.ParseInt(f[1], 10, 64)
			if err != nil || n <= 0 {
				return nil, fmt.Errorf("line %d: bad %s", i+1, f[0])
			}
			if f[0] == "size" {
				l.Size = n
			} else {
				l.Chunk = n
			}
		default:
			k, err := strconv.Atoi(f[0])
			if err != nil || k != len(l.Sums) {
				return nil, fmt.Errorf("line %d: chunk %s out of order", i+1, f[0])
			}
			if _, err := hex.DecodeString(f[1]); err != nil || len(f[1]) != 64 {
				return nil, fmt.Errorf("line %d: not a sha256", i+1)
			}
			l.Sums = append(l.Sums, strings.ToLower(f[1]))
		}
	}
	if !ver || l.Size <= 0 || l.Chunk <= 0 || len(l.Sums) == 0 {
		return nil, errors.New("incomplete")
	}
	return l, nil
}
