// Package fsread finds a file in the top folder of a disk's file systems by reading the disk's
// bytes, never mounting it (owner, 2026-10-16: OmaBoot?'s test file goes on any stick, card or
// drive the person already has, so nothing has to be erased for the port tests). It gives back
// where the file's bytes are on the disk; the caller reads them.
//
// It reads, and only reads: the partition table (none, MBR, GPT or Apple's partition map), then
// each volume's own structures: FAT12, FAT16 and FAT32 (most sticks; SDHC cards ship FAT32),
// exFAT (SDXC cards, bigger sticks) and HFS+ or HFSX (a Mac's FireWire drive), the HFS+ inside an
// HFS wrapper too. Other file systems (NTFS, APFS, ext4…) are named, not read. Only the top
// folder is looked at, and only its entries' names, sizes and where their bytes are.
package fsread

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"strings"
	"unicode/utf16"
)

// Extent is a run of a file's bytes on the disk.
type Extent struct{ Off, Len int64 }

// File is a file found in a volume's top folder.
type File struct {
	Name    string   // as the file system has it
	Size    int64    // bytes
	Extents []Extent // where its bytes are, in order; nil when Err says why they can't be had
	Err     string   // why its bytes can't be found ("" when Extents has them)
	Vol     Volume
}

// Volume is a file system the disk holds.
type Volume struct {
	FS   string // "FAT32", "exFAT", "HFS+", "HFSX", or one not read: "NTFS", "APFS", "ext4", "unknown"
	Part string // "the whole disk", "MBR partition 1", "GPT partition 2", "APM partition 3"
	Off  int64  // where it starts on the disk
	Read bool   // its top folder was read
	Err  string // why it wasn't, when it's a kind that's read
}

func (v Volume) String() string { return v.FS + ", " + v.Part }

// Result is what a disk holds.
type Result struct {
	Files   []File   // the top-folder files whose names match, in disk order
	Volumes []Volume // every volume found
}

// Find looks through the disk's volumes for top-folder files whose names match. r reads the
// disk at offsets and lengths that are multiples of 4096 (an O_DIRECT device does); size is
// the disk's size and sector its logical block size (512 or 4096), which MBR and GPT count in.
func Find(r io.ReaderAt, size int64, sector int, match func(name string) bool) Result {
	d := &disk{r: r, size: size, pages: map[int64][]byte{}}
	if sector != 512 && sector != 4096 {
		sector = 512
	}
	var res Result
	for _, v := range volumes(d, sector) {
		files, err := topFiles(d, &v, match)
		if err != nil {
			v.Err = err.Error()
		}
		res.Volumes = append(res.Volumes, v)
		res.Files = append(res.Files, files...)
	}
	return res
}

// ── the disk, read in cached pages ────────────────────────────────────────────

const (
	pageLen  = 64 << 10
	pagesMax = 256 // 16 MiB of pages before they're let go
)

type disk struct {
	r     io.ReaderAt
	size  int64
	pages map[int64][]byte
}

var errRange = errors.New("past the end of the disk")

// read is n bytes at off (n small: metadata), from whole pages.
func (d *disk) read(off, n int64) ([]byte, error) {
	if off < 0 || n < 0 || off+n > d.size {
		return nil, errRange
	}
	out := make([]byte, 0, n)
	for n > 0 {
		at := off &^ (pageLen - 1)
		p, err := d.page(at)
		if err != nil {
			return nil, err
		}
		k := min(int64(len(p))-(off-at), n)
		if k <= 0 {
			return nil, io.ErrUnexpectedEOF
		}
		out = append(out, p[off-at:off-at+k]...)
		off, n = off+k, n-k
	}
	return out, nil
}

func (d *disk) page(at int64) ([]byte, error) {
	if p, ok := d.pages[at]; ok {
		return p, nil
	}
	n := min(int64(pageLen), d.size-at) &^ 4095
	if n <= 0 {
		return nil, errRange
	}
	if len(d.pages) >= pagesMax {
		clear(d.pages)
	}
	p := make([]byte, n)
	k, err := d.r.ReadAt(p, at)
	if int64(k) < n {
		if err == nil || errors.Is(err, io.EOF) {
			err = io.ErrUnexpectedEOF
		}
		return nil, err
	}
	d.pages[at] = p
	return p, nil
}

// ── partitions ────────────────────────────────────────────────────────────────

const partsMax = 128

func le16(b []byte, o int) int64 { return int64(binary.LittleEndian.Uint16(b[o:])) }
func le32(b []byte, o int) int64 { return int64(binary.LittleEndian.Uint32(b[o:])) }
func le64(b []byte, o int) int64 { return int64(binary.LittleEndian.Uint64(b[o:])) }
func be16(b []byte, o int) int64 { return int64(binary.BigEndian.Uint16(b[o:])) }
func be32(b []byte, o int) int64 { return int64(binary.BigEndian.Uint32(b[o:])) }

// volumes are the disk's volumes: a file system on the whole disk (a "superfloppy": many sticks
// and cards), or the partitions of its table.
func volumes(d *disk, sector int) []Volume {
	s0, err := d.read(0, 4096)
	if err != nil {
		return nil
	}
	if isExFAT(s0) || isFAT(s0) {
		return []Volume{probe(d, 0, "the whole disk")}
	}
	if bytes.Equal(s0[:2], []byte("ER")) {
		if vs := apm(d, s0); len(vs) > 0 {
			return vs
		}
	}
	if s0[510] == 0x55 && s0[511] == 0xaa {
		gpt := false
		var vs []Volume
		for i := range 4 {
			e := s0[446+16*i:]
			typ, start := e[4], le32(e, 8)
			switch {
			case typ == 0xee:
				gpt = true
			case typ == 0 || start == 0:
			case typ == 0x05 || typ == 0x0f || typ == 0x85: // extended: its logical partitions aren't read
				vs = append(vs, Volume{FS: "extended partition", Part: fmt.Sprintf("MBR partition %d", i+1), Off: start * int64(sector)})
			default:
				vs = append(vs, probe(d, start*int64(sector), fmt.Sprintf("MBR partition %d", i+1)))
			}
		}
		if gpt {
			for _, sec := range []int{sector, 512 + 4096 - sector} { // the other size too: a disk behind a bridge that reports 512
				if g := gptVolumes(d, sec); g != nil {
					return g
				}
			}
		}
		if len(vs) > 0 {
			return vs
		}
	}
	return []Volume{probe(d, 0, "the whole disk")}
}

func gptVolumes(d *disk, sector int) []Volume {
	h, err := d.read(int64(sector), 512)
	if err != nil || !bytes.Equal(h[:8], []byte("EFI PART")) {
		return nil
	}
	lba, n, size := le64(h, 72), le32(h, 80), le32(h, 84)
	if size < 128 || size > 4096 || n == 0 {
		return nil
	}
	vs := []Volume{}
	for i := range min(n, partsMax) {
		e, err := d.read(lba*int64(sector)+i*size, 128)
		if err != nil {
			break
		}
		if bytes.Equal(e[:16], make([]byte, 16)) {
			continue
		}
		vs = append(vs, probe(d, le64(e, 32)*int64(sector), fmt.Sprintf("GPT partition %d", i+1)))
	}
	return vs
}

// apm is an Apple partition map's volumes (a drive formatted on a PowerPC Mac): block 0 is the
// driver descriptor ("ER", its block size at 2), the map's entries ("PM") from block 1.
func apm(d *disk, s0 []byte) []Volume {
	for _, bs := range []int64{be16(s0, 2), 512} {
		if bs != 512 && bs != 1024 && bs != 2048 && bs != 4096 {
			continue
		}
		var vs []Volume
		count := int64(1)
		for i := int64(1); i <= count && i <= partsMax; i++ {
			e, err := d.read(i*bs, 512)
			if err != nil || !bytes.Equal(e[:2], []byte("PM")) {
				break
			}
			count = be32(e, 4)
			typ := cstr(e[48:80])
			if typ == "Apple_partition_map" || typ == "Apple_Free" || strings.HasPrefix(typ, "Apple_Driver") || typ == "Apple_Patches" {
				continue
			}
			vs = append(vs, probe(d, be32(e, 8)*bs, fmt.Sprintf("APM partition %d", i)))
		}
		if len(vs) > 0 {
			return vs
		}
	}
	return nil
}

func cstr(b []byte) string {
	if i := bytes.IndexByte(b, 0); i >= 0 {
		b = b[:i]
	}
	return string(b)
}

// probe names the file system at off.
func probe(d *disk, off int64, part string) Volume {
	v := Volume{FS: "unknown", Part: part, Off: off}
	b, err := d.read(off, 4096)
	if err != nil {
		v.Err = err.Error()
		return v
	}
	switch {
	case isExFAT(b):
		v.FS = "exFAT"
	case isFAT(b):
		v.FS = fatKind(b)
	case be16(b, 1024) == 0x482b:
		v.FS = "HFS+"
	case be16(b, 1024) == 0x4858:
		v.FS = "HFSX"
	case be16(b, 1024) == 0x4244 && be16(b, 1024+0x7c) == 0x482b:
		v.FS = "HFS+" // inside an HFS wrapper
	case be16(b, 1024) == 0x4244:
		v.FS = "HFS"
	case bytes.Equal(b[3:11], []byte("NTFS    ")):
		v.FS = "NTFS"
	case bytes.Equal(b[32:36], []byte("NXSB")):
		v.FS = "APFS"
	case le16(b, 1024+56) == 0xef53:
		v.FS = "ext4"
	}
	return v
}

// topFiles reads the volume's top folder for the files that match.
func topFiles(d *disk, v *Volume, match func(string) bool) ([]File, error) {
	var fs []File
	var err error
	switch v.FS {
	case "FAT12", "FAT16", "FAT32":
		fs, err = fatFiles(d, v.Off, match)
	case "exFAT":
		fs, err = exfatFiles(d, v.Off, match)
	case "HFS+", "HFSX":
		fs, err = hfsFiles(d, v.Off, match)
	default:
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	v.Read = true
	for i := range fs {
		fs[i].Vol = *v
	}
	return fs, nil
}

// chain turns a cluster chain into extents: next gives the cluster after c (end true at the
// chain's end), at a cluster's place on the disk; n bytes are wanted.
func chain(first, clusters, clusterLen, n int64, at func(c int64) int64, next func(c int64) (int64, bool, error)) ([]Extent, error) {
	var out []Extent
	c := first
	for steps := int64(0); n > 0; steps++ {
		if c < 2 || c >= clusters+2 || steps > clusters {
			return nil, fmt.Errorf("its cluster chain is broken (cluster %d)", c)
		}
		k := min(clusterLen, n)
		if l := len(out) - 1; l >= 0 && out[l].Off+out[l].Len == at(c) {
			out[l].Len += k
		} else {
			out = append(out, Extent{at(c), k})
		}
		n -= k
		if n == 0 {
			break
		}
		nc, end, err := next(c)
		if err != nil {
			return nil, err
		}
		if end {
			return nil, errors.New("its cluster chain ends before the file does")
		}
		c = nc
	}
	return out, nil
}

// ── FAT ───────────────────────────────────────────────────────────────────────

// isFAT: a boot sector with a sane BIOS parameter block.
func isFAT(b []byte) bool {
	if b[0] != 0xeb && b[0] != 0xe9 || b[510] != 0x55 || b[511] != 0xaa {
		return false
	}
	bps, spc, rsvd, fats, media := le16(b, 11), int64(b[13]), le16(b, 14), b[16], b[21]
	return (bps == 512 || bps == 1024 || bps == 2048 || bps == 4096) && spc > 0 && spc&(spc-1) == 0 &&
		rsvd > 0 && (fats == 1 || fats == 2) && (media == 0xf0 || media >= 0xf8)
}

type fat struct {
	off, bps, spc, rsvd, fats, fatSz, rootEnts, totSec, firstData, clusters, rootClus int64
	kind                                                                              string
}

func parseFAT(b []byte, off int64) fat {
	f := fat{off: off, bps: le16(b, 11), spc: int64(b[13]), rsvd: le16(b, 14), fats: int64(b[16]), rootEnts: le16(b, 17)}
	f.totSec, f.fatSz = le16(b, 19), le16(b, 22)
	if f.totSec == 0 {
		f.totSec = le32(b, 32)
	}
	if f.fatSz == 0 {
		f.fatSz, f.rootClus = le32(b, 36), le32(b, 44)
	}
	rootSecs := (f.rootEnts*32 + f.bps - 1) / f.bps
	f.firstData = f.rsvd + f.fats*f.fatSz + rootSecs
	if f.totSec > f.firstData {
		f.clusters = (f.totSec - f.firstData) / f.spc
	}
	switch {
	case f.clusters < 4085:
		f.kind = "FAT12"
	case f.clusters < 65525:
		f.kind = "FAT16"
	default:
		f.kind = "FAT32"
	}
	return f
}

func fatKind(b []byte) string { return parseFAT(b, 0).kind }

func (f fat) at(c int64) int64 { return f.off + (f.firstData+(c-2)*f.spc)*f.bps }

func (f fat) next(d *disk) func(int64) (int64, bool, error) {
	base := f.off + f.rsvd*f.bps
	return func(c int64) (int64, bool, error) {
		var v, eoc, bad int64
		switch f.kind {
		case "FAT12":
			b, err := d.read(base+c+c/2, 2)
			if err != nil {
				return 0, false, err
			}
			v = le16(b, 0)
			if c&1 == 1 {
				v >>= 4
			}
			v, eoc, bad = v&0xfff, 0xff8, 0xff7
		case "FAT16":
			b, err := d.read(base+2*c, 2)
			if err != nil {
				return 0, false, err
			}
			v, eoc, bad = le16(b, 0), 0xfff8, 0xfff7
		default:
			b, err := d.read(base+4*c, 4)
			if err != nil {
				return 0, false, err
			}
			v, eoc, bad = le32(b, 0)&0x0fffffff, 0x0ffffff8, 0x0ffffff7
		}
		switch {
		case v >= eoc:
			return 0, true, nil
		case v == bad || v < 2:
			return 0, false, fmt.Errorf("its cluster chain is broken (cluster %d)", c)
		}
		return v, false, nil
	}
}

const dirMax = 4 << 20 // the most of a top folder read

func fatFiles(d *disk, off int64, match func(string) bool) ([]File, error) {
	b, err := d.read(off, 512)
	if err != nil {
		return nil, err
	}
	f := parseFAT(b, off)
	cl := f.spc * f.bps
	var dir []byte
	if f.kind == "FAT32" {
		ext, err := dirChain(d, f, cl)
		if err != nil {
			return nil, err
		}
		for _, e := range ext {
			p, err := d.read(e.Off, e.Len)
			if err != nil {
				return nil, err
			}
			dir = append(dir, p...)
		}
	} else {
		dir, err = d.read(off+(f.rsvd+f.fats*f.fatSz)*f.bps, f.rootEnts*32)
		if err != nil {
			return nil, err
		}
	}
	var out []File
	var lfn []uint16
	sum := -1
	for i := 0; i+32 <= len(dir); i += 32 {
		e := dir[i : i+32]
		switch {
		case e[0] == 0:
			return out, nil
		case e[0] == 0xe5:
			lfn, sum = nil, -1
			continue
		case e[11]&0x3f == 0x0f: // a long-name piece, last first
			ord := int(e[0] & 0x1f)
			if e[0]&0x40 != 0 {
				lfn, sum = make([]uint16, 13*ord), int(e[13])
			}
			if ord == 0 || 13*ord > len(lfn) || int(e[13]) != sum {
				lfn, sum = nil, -1
				continue
			}
			for k, o := range []int{1, 3, 5, 7, 9, 14, 16, 18, 20, 22, 24, 28, 30} {
				lfn[13*(ord-1)+k] = binary.LittleEndian.Uint16(e[o:])
			}
			continue
		}
		long, cs := lfn, sum
		lfn, sum = nil, -1
		if e[11]&0x18 != 0 { // a folder or the volume's label
			continue
		}
		name := shortName(e)
		if long != nil && cs == int(shortSum(e[:11])) {
			name = lfnString(long)
		}
		if !match(name) {
			continue
		}
		file := File{Name: name, Size: le32(e, 28)}
		first := le16(e, 26)
		if f.kind == "FAT32" {
			first |= le16(e, 20) << 16
		}
		if file.Size > 0 {
			file.Extents, err = chain(first, f.clusters, cl, file.Size, f.at, f.next(d))
			if err != nil {
				file.Err = err.Error()
			}
		}
		out = append(out, file)
	}
	return out, nil
}

// dirChain is the FAT32 top folder's chain up to its end (chain wants a length; a folder has
// none): its clusters until the end mark.
func dirChain(d *disk, f fat, cl int64) ([]Extent, error) {
	var out []Extent
	c := f.rootClus
	next := f.next(d)
	for steps := int64(0); int64(len(out))*cl < dirMax; steps++ {
		if c < 2 || c >= f.clusters+2 || steps > f.clusters {
			return nil, fmt.Errorf("the top folder's cluster chain is broken (cluster %d)", c)
		}
		out = append(out, Extent{f.at(c), cl})
		nc, end, err := next(c)
		if err != nil {
			return nil, err
		}
		if end {
			break
		}
		c = nc
	}
	return out, nil
}

func shortSum(n []byte) byte {
	var s byte
	for _, c := range n {
		s = (s>>1 | s<<7) + c
	}
	return s
}

func shortName(e []byte) string {
	base, ext := strings.TrimRight(string(e[:8]), " "), strings.TrimRight(string(e[8:11]), " ")
	if e[12]&0x08 != 0 {
		base = strings.ToLower(base)
	}
	if e[12]&0x10 != 0 {
		ext = strings.ToLower(ext)
	}
	if ext == "" {
		return base
	}
	return base + "." + ext
}

func lfnString(u []uint16) string {
	for i, c := range u {
		if c == 0 || c == 0xffff {
			u = u[:i]
			break
		}
	}
	return string(utf16.Decode(u))
}

// ── exFAT ─────────────────────────────────────────────────────────────────────

func isExFAT(b []byte) bool { return bytes.Equal(b[3:11], []byte("EXFAT   ")) }

func exfatFiles(d *disk, off int64, match func(string) bool) ([]File, error) {
	b, err := d.read(off, 512)
	if err != nil {
		return nil, err
	}
	bpsShift, spcShift := int64(b[108]), int64(b[109])
	if bpsShift < 9 || bpsShift > 12 || spcShift > 25-bpsShift {
		return nil, errors.New("its boot sector doesn't add up")
	}
	bps := int64(1) << bpsShift
	cl := bps << spcShift
	fatOff, heap, clusters, root := off+le32(b, 80)*bps, off+le32(b, 88)*bps, le32(b, 92), le32(b, 96)
	at := func(c int64) int64 { return heap + (c-2)*cl }
	next := func(c int64) (int64, bool, error) {
		e, err := d.read(fatOff+4*c, 4)
		if err != nil {
			return 0, false, err
		}
		switch v := le32(e, 0); {
		case v == 0xffffffff:
			return 0, true, nil
		case v < 2 || v > clusters+1:
			return 0, false, fmt.Errorf("its cluster chain is broken (cluster %d)", c)
		default:
			return v, false, nil
		}
	}
	var dir []byte
	c := root
	for steps := int64(0); int64(len(dir)) < dirMax; steps++ {
		if c < 2 || c > clusters+1 || steps > clusters {
			return nil, fmt.Errorf("the top folder's cluster chain is broken (cluster %d)", c)
		}
		p, err := d.read(at(c), min(cl, dirMax))
		if err != nil {
			return nil, err
		}
		dir = append(dir, p...)
		nc, end, err := next(c)
		if err != nil {
			return nil, err
		}
		if end {
			break
		}
		c = nc
	}
	var out []File
	for i := 0; i+32 <= len(dir); i += 32 {
		e := dir[i : i+32]
		if e[0] == 0 {
			break
		}
		if e[0] != 0x85 { // a file's entry set starts here; anything else is skipped over
			continue
		}
		n := int(e[1])
		if n < 2 || i+32*(n+1) > len(dir) || dir[i+32] != 0xc0 {
			continue
		}
		attr := le16(e, 4)
		st := dir[i+32 : i+64]
		nameLen := int(st[3])
		var name []uint16
		for k := 2; k <= n && len(name) < nameLen; k++ {
			ne := dir[i+32*k : i+32*(k+1)]
			if ne[0] != 0xc1 {
				break
			}
			for o := 2; o < 32 && len(name) < nameLen; o += 2 {
				name = append(name, binary.LittleEndian.Uint16(ne[o:]))
			}
		}
		i += 32 * n
		if attr&0x10 != 0 { // a folder
			continue
		}
		file := File{Name: string(utf16.Decode(name)), Size: le64(st, 8)}
		if !match(file.Name) {
			continue
		}
		dataLen, first := le64(st, 24), le32(st, 20)
		if file.Size > dataLen {
			file.Size = dataLen
		}
		switch {
		case file.Size == 0:
		case st[1]&0x02 != 0: // contiguous: no chain in the FAT
			if first < 2 || first+(file.Size+cl-1)/cl > clusters+2 {
				file.Err = "its clusters are past the end of the volume"
			} else {
				file.Extents = []Extent{{at(first), file.Size}}
			}
		default:
			file.Extents, err = chain(first, clusters, cl, file.Size, at, next)
			if err != nil {
				file.Err = err.Error()
			}
		}
		out = append(out, file)
	}
	return out, nil
}

// ── HFS+ ──────────────────────────────────────────────────────────────────────

const (
	hfsLeavesMax = 4096
	rootFolderID = 2
)

func hfsFiles(d *disk, off int64, match func(string) bool) ([]File, error) {
	vh, err := d.read(off+1024, 512)
	if err != nil {
		return nil, err
	}
	if be16(vh, 0) == 0x4244 { // an HFS wrapper: the HFS+ volume is embedded in it
		alBlk, alSt := be32(vh, 0x14), be16(vh, 0x1c)
		off += alSt*512 + be16(vh, 0x7e)*alBlk
		if vh, err = d.read(off+1024, 512); err != nil {
			return nil, err
		}
	}
	if s := be16(vh, 0); s != 0x482b && s != 0x4858 {
		return nil, errors.New("no HFS+ volume header")
	}
	bs := be32(vh, 40)
	if bs < 512 || bs&(bs-1) != 0 {
		return nil, errors.New("its block size doesn't add up")
	}
	cat := forkExtents(vh[272:352], off, bs)
	readCat := func(at, n int64) ([]byte, error) { return readFork(d, cat, at, n) }
	hdr, err := readCat(0, 512)
	if err != nil {
		return nil, err
	}
	nodeSize, leaf := be16(hdr, 32), be32(hdr, 24)
	if nodeSize < 512 || nodeSize > 32768 || nodeSize&(nodeSize-1) != 0 {
		return nil, errors.New("its catalog's node size doesn't add up")
	}
	var out []File
	seen := map[int64]bool{}
	for steps := 0; leaf != 0 && steps < hfsLeavesMax; steps++ {
		if seen[leaf] {
			return nil, errors.New("its catalog's leaves loop")
		}
		seen[leaf] = true
		node, err := readCat(leaf*nodeSize, nodeSize)
		if err != nil {
			return nil, err
		}
		if int8(node[8]) != -1 { // not a leaf node
			return nil, errors.New("its catalog's leaf chain is broken")
		}
		recs := int(be16(node, 10))
		for i := range recs {
			o := int(be16(node, int(nodeSize)-2*(i+1)))
			if o < 14 || o+8 > int(nodeSize) {
				return nil, errors.New("its catalog has a record out of place")
			}
			keyLen, parent, nameLen := int(be16(node, o)), be32(node, o+2), int(be16(node, o+6))
			if parent > rootFolderID {
				return out, nil // sorted by parent: the top folder's entries are done
			}
			data := o + 2 + keyLen
			if parent < rootFolderID || 8+2*nameLen > keyLen+2 || data+88+80 > int(nodeSize) || be16(node, data) != 2 {
				continue // not a file in the top folder
			}
			name := make([]uint16, nameLen)
			for k := range name {
				name[k] = binary.BigEndian.Uint16(node[o+8+2*k:])
			}
			file := File{Name: string(utf16.Decode(name))}
			if !match(file.Name) {
				continue
			}
			fork := node[data+88 : data+168]
			file.Size = int64(binary.BigEndian.Uint64(fork))
			ext := forkExtents(fork, off, bs)
			have := int64(0)
			for _, e := range ext {
				have += e.Len
			}
			switch {
			case have < file.Size:
				file.Err = "it's in more than 8 pieces on the drive (its extents overflow), not read"
			default:
				file.Extents = trim(ext, file.Size)
			}
			out = append(out, file)
		}
		leaf = be32(node, 0)
	}
	return out, nil
}

// forkExtents is an HFS+ fork's first eight extents on the disk (the volume at off, blocks of bs).
func forkExtents(fork []byte, off, bs int64) []Extent {
	var out []Extent
	for i := range 8 {
		start, n := be32(fork, 16+8*i), be32(fork, 20+8*i)
		if n == 0 {
			break
		}
		out = append(out, Extent{off + start*bs, n * bs})
	}
	return out
}

func trim(ext []Extent, n int64) []Extent {
	var out []Extent
	for _, e := range ext {
		if n <= 0 {
			break
		}
		e.Len = min(e.Len, n)
		out = append(out, e)
		n -= e.Len
	}
	return out
}

// readFork reads n bytes at at into a fork laid out as ext.
func readFork(d *disk, ext []Extent, at, n int64) ([]byte, error) {
	var out []byte
	for _, e := range ext {
		if n == 0 {
			break
		}
		if at >= e.Len {
			at -= e.Len
			continue
		}
		k := min(e.Len-at, n)
		p, err := d.read(e.Off+at, k)
		if err != nil {
			return nil, err
		}
		out = append(out, p...)
		at, n = 0, n-k
	}
	if n > 0 {
		return nil, errors.New("its catalog is past its extents")
	}
	return out, nil
}
