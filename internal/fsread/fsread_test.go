package fsread

import (
	"bytes"
	"compress/gzip"
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"
)

// The images in testdata were made by real tools (testdata/make-images.sh: mkfs.fat, mkfs.exfat,
// hfsprogs' mkfs.hfsplus, sfdisk), the files copied in by Linux's own drivers: a.bin, b.bin and
// c.bin, b.bin deleted, then OMABOOT-TEST.BIN (so on FAT it's in pieces) and a decoy of the same
// name in a folder, sub/.

// testContent is OMABOOT-TEST.BIN in the images: each 512-byte block names its own number.
func testContent() []byte {
	var out bytes.Buffer
	for i := 0; out.Len() < 300*1024+123; i++ {
		line := fmt.Sprintf("OMABOOT-TEST block %06d ", i)
		blk := strings.Repeat(line, 512/len(line)+1)[:511] + "\n"
		out.WriteString(blk)
	}
	return out.Bytes()[:300*1024+123]
}

func image(t *testing.T, name string) []byte {
	t.Helper()
	f, err := os.Open("testdata/" + name + ".img.gz")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	z, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	b, err := io.ReadAll(z)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// direct reads like an O_DIRECT device: only 4096-aligned offsets and lengths.
type direct []byte

func (d direct) ReadAt(p []byte, off int64) (int, error) {
	if off%4096 != 0 || len(p)%4096 != 0 {
		return 0, fmt.Errorf("unaligned read: %d bytes at %d", len(p), off)
	}
	if off >= int64(len(d)) {
		return 0, io.EOF
	}
	n := copy(p, d[off:])
	if n < len(p) {
		return n, io.EOF
	}
	return n, nil
}

func isTest(name string) bool { return strings.EqualFold(name, "OMABOOT-TEST.BIN") }

func bytesOf(img []byte, ext []Extent) []byte {
	var out []byte
	for _, e := range ext {
		out = append(out, img[e.Off:e.Off+e.Len]...)
	}
	return out
}

func TestFind(t *testing.T) {
	want := testContent()
	for _, c := range []struct {
		img, fs, part string
		pieces        bool // the file is in more than one piece
	}{
		{"fat12", "FAT12", "the whole disk", true},
		{"fat16", "FAT16", "the whole disk", true},
		{"mbr-fat32", "FAT32", "MBR partition 1", true},
		{"exfat", "exFAT", "the whole disk", true},
		{"gpt-exfat", "exFAT", "GPT partition 1", true},
		{"gpt-hfsplus", "HFS+", "GPT partition 1", true},
		{"hfsx", "HFSX", "the whole disk", true},
		{"hfs-wrap", "HFS+", "the whole disk", true},
	} {
		t.Run(c.img, func(t *testing.T) {
			img := image(t, c.img)
			r := Find(direct(img), int64(len(img)), 512, isTest)
			if len(r.Volumes) != 1 || r.Volumes[0].FS != c.fs || r.Volumes[0].Part != c.part || !r.Volumes[0].Read {
				t.Fatalf("volumes %+v, want one %s on %s, read", r.Volumes, c.fs, c.part)
			}
			if len(r.Files) != 1 {
				t.Fatalf("files %+v, want the top folder's OMABOOT-TEST.BIN only (not sub/'s)", r.Files)
			}
			f := r.Files[0]
			if f.Err != "" || f.Name != "OMABOOT-TEST.BIN" || f.Size != int64(len(want)) {
				t.Fatalf("file %q, %d bytes, err %q; want OMABOOT-TEST.BIN, %d bytes", f.Name, f.Size, f.Err, len(want))
			}
			if got := bytesOf(img, f.Extents); !bytes.Equal(got, want) {
				t.Fatalf("its extents %v hold the wrong bytes", f.Extents)
			}
			if c.pieces && len(f.Extents) < 2 {
				t.Errorf("extents %v: the image has it in pieces", f.Extents)
			}
		})
	}
}

// TestAPM: an Apple partition map (a drive formatted on a PowerPC Mac) around the HFS+ volume.
func TestAPM(t *testing.T) {
	src := image(t, "gpt-hfsplus")
	vol := src[1<<20 : 1<<20+4<<20]
	const at = 64 // blocks of 512
	img := make([]byte, at*512+len(vol))
	copy(img, "ER")
	binary.BigEndian.PutUint16(img[2:], 512)
	entry := func(i int, start, n uint32, typ string) {
		e := img[512*i:]
		copy(e, "PM")
		binary.BigEndian.PutUint32(e[4:], 3)
		binary.BigEndian.PutUint32(e[8:], start)
		binary.BigEndian.PutUint32(e[12:], n)
		copy(e[48:], typ)
	}
	entry(1, 1, 63, "Apple_partition_map")
	entry(2, 1, 0, "Apple_Free")
	entry(3, at, uint32(len(vol)/512), "Apple_HFS")
	copy(img[at*512:], vol)
	r := Find(direct(img), int64(len(img)), 512, isTest)
	if len(r.Volumes) != 1 || r.Volumes[0].FS != "HFS+" || r.Volumes[0].Part != "APM partition 3" || len(r.Files) != 1 {
		t.Fatalf("volumes %+v files %+v", r.Volumes, r.Files)
	}
	if !bytes.Equal(bytesOf(img, r.Files[0].Extents), testContent()) {
		t.Fatal("wrong bytes")
	}
}

// TestNotRead: a disk with nothing it reads says what it saw, and a file system it doesn't
// read is named.
func TestNotRead(t *testing.T) {
	img := make([]byte, 1<<20)
	r := Find(direct(img), int64(len(img)), 512, isTest)
	if len(r.Files) != 0 || len(r.Volumes) != 1 || r.Volumes[0].FS != "unknown" || r.Volumes[0].Read {
		t.Fatalf("blank disk: %+v", r)
	}
	copy(img[3:], "NTFS    ")
	if r := Find(direct(img), int64(len(img)), 512, isTest); r.Volumes[0].FS != "NTFS" || r.Volumes[0].Read {
		t.Fatalf("NTFS: %+v", r)
	}
}

// TestBrokenChain: a FAT chain that runs into a free cluster is the file's error, not a panic
// or wrong bytes.
func TestBrokenChain(t *testing.T) {
	img := image(t, "fat16")
	r := Find(direct(img), int64(len(img)), 512, isTest)
	f := r.Files[0]
	b := img[:512]
	rsvd, bps, spc := int64(binary.LittleEndian.Uint16(b[14:])), int64(binary.LittleEndian.Uint16(b[11:])), int64(b[13])
	fatSz := int64(binary.LittleEndian.Uint16(b[22:]))
	rootSecs := (int64(binary.LittleEndian.Uint16(b[17:]))*32 + bps - 1) / bps
	first := (f.Extents[0].Off/bps-(rsvd+2*fatSz+rootSecs))/spc + 2
	binary.LittleEndian.PutUint16(img[rsvd*bps+2*first:], 0) // its first cluster now leads nowhere
	r = Find(direct(img), int64(len(img)), 512, isTest)
	if len(r.Files) != 1 || r.Files[0].Err == "" || r.Files[0].Extents != nil {
		t.Fatalf("broken chain: %+v", r.Files)
	}
}
