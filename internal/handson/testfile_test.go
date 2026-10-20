package handson

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/omaboot/boot-live/internal/fsread"
)

// memDisk is a disk in memory.
type memDisk []byte

func (m memDisk) Size() int64 { return int64(len(m)) }
func (m memDisk) ReadAt(p []byte, off int64) (int, error) {
	if off >= int64(len(m)) {
		return 0, io.EOF
	}
	n := copy(p, m[off:])
	if n < len(p) {
		return n, io.EOF
	}
	return n, nil
}

// The test file in three pieces at cluster (not page) boundaries, the header split across
// two: its pattern checks out, and a flipped byte is found where it is.
// TestTestFileFixed: OMABOOT-TEST.BIN is published once (get.omaboot.live/test/) and people keep
// their copies, and findTestFile wants its header byte for byte: a change here breaks every copy
// out there. A new format gets a new tag and a new name, and the old file keeps working.
func TestTestFileFixed(t *testing.T) {
	const want = "ccbabd4043a0f1a145b07802f793f09f0004e11230ba9b15ac4734d43974e21e"
	if sum := sha256.Sum256(TestFile()); hex.EncodeToString(sum[:]) != want {
		t.Fatalf("OMABOOT-TEST.BIN is now %x, was %s: the published file no longer matches", sum, want)
	}
}

func TestCheckTestFile(t *testing.T) {
	file := TestFile()
	if !bytes.HasPrefix(file, []byte(testFileTag)) || len(file) != testFileHead+testFileBody {
		t.Fatal("TestFile's layout")
	}
	disk := make(memDisk, 64<<20)
	cuts := []int64{0, 2560, 10<<20 + 512, int64(len(file))}
	at := []int64{1<<20 + 512, 40 << 20, 12<<20 + 1536}
	var ext []fsread.Extent
	for i := range at {
		n := cuts[i+1] - cuts[i]
		copy(disk[at[i]:], file[cuts[i]:cuts[i+1]])
		ext = append(ext, fsread.Extent{Off: at[i], Len: n})
	}
	f := &fsread.File{Name: testFileName, Size: int64(len(file)), Extents: ext, Vol: fsread.Volume{FS: "FAT32", Part: "the whole disk"}}
	if h, err := readSpans(disk, ext, testFileHead); err != nil || !bytes.Equal(h, testFileHeader()) {
		t.Fatalf("header: %v", err)
	}
	clock := func() time.Duration { return 0 }
	r := checkTestFile(disk, f, clock, func(ReadProgress) bool { return true })
	if !r.OK() || r.Way != WayFile || r.Bytes != testFileBody || r.FileAt != "OMABOOT-TEST.BIN (FAT32, the whole disk)" {
		t.Fatalf("good file: %+v", r)
	}
	flip := int64(10<<20 + 777) // into the third piece
	disk[at[2]+flip-cuts[2]] ^= 1
	r = checkTestFile(disk, f, clock, func(ReadProgress) bool { return true })
	if r.Kind != failFile || r.At != flip-testFileHead || r.OK() {
		t.Fatalf("flipped byte at %d: %+v", flip-testFileHead, r)
	}
}

// The stream in odd-sized pieces is the stream whole (the disc's check uses it whole).
func TestStreamPieces(t *testing.T) {
	whole := make([]byte, 100003)
	testDiscStream(whole)
	g, got := newStream(), []byte{}
	for _, n := range []int{1, 7, 8, 9, 4096, 3, 95879} {
		p := make([]byte, n)
		g.fill(p)
		got = append(got, p...)
	}
	if !bytes.Equal(got, whole) {
		t.Fatal("the stream in pieces differs")
	}
}

func TestTestFileNames(t *testing.T) {
	for name, want := range map[string]bool{"OMABOOT-TEST.BIN": true, "omaboot-test.bin": true, "OMABOOT-TEST (1).BIN": true,
		"OMABOOT-TEST.BIN.txt": false, "omaboot.bin": false, "TEST.BIN": false} {
		if isTestFileName(name) != want {
			t.Errorf("%q: %v", name, !want)
		}
	}
}

// On a real FAT16 image (fsread's testdata) whose OMABOOT-TEST.BIN is short, the disk reads as
// if there were none, and the note says why.
func TestFindTestFileShort(t *testing.T) {
	f, err := os.Open("../fsread/testdata/fat16.img.gz")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	z, _ := gzip.NewReader(f)
	img, _ := io.ReadAll(z)
	hit := findTestFile(memDisk(img), 512, func(ReadProgress) bool { return true })
	if hit.File != nil || !strings.Contains(hit.Note, "cut off") || !strings.Contains(hit.Note, "FAT16, the whole disk") {
		t.Fatalf("%+v", hit)
	}
	hit = findTestFile(memDisk(make([]byte, 1<<20)), 512, func(ReadProgress) bool { return true })
	if hit.File != nil || !strings.HasPrefix(hit.Note, "no test file") {
		t.Fatalf("blank: %+v", hit)
	}
}

// Through the whole read: the test file wins over reading twice; read for the test file only, a
// disk without it reads nothing more.
func TestReadTestFile(t *testing.T) {
	t.Parallel()
	read := func(d FakeDisk, only bool) DiskRead {
		d.MBps = 100
		v := &virtDisk{d: d, size: fakeStickSize, seen: map[int64]int{}}
		return readPasses(v, readHow{Base: "arch", Sector: 512, FileOnly: only}, func() time.Duration { return v.now }, func(ReadProgress) bool { return true })
	}
	if r := read(FakeDisk{TestFile: true}, false); !r.OK() || r.Way != WayFile {
		t.Errorf("USB, test file: %+v", r)
	}
	if r := read(FakeDisk{TestFile: true, BadFile: 5 << 20}, false); r.Kind != failFile || r.At != 5<<20-1 {
		t.Errorf("USB, bad test file: %+v", r)
	}
	if r := read(FakeDisk{}, false); !r.OK() || r.Way != WayTwice || r.Note != "" {
		t.Errorf("USB, no test file: %+v", r)
	}
	if r := read(FakeDisk{TestFile: true}, true); !r.OK() || r.Way != WayFile {
		t.Errorf("card, test file: %+v", r)
	}
	if r := read(FakeDisk{}, true); r.OK() || !r.NoFile || r.Way != "" || !strings.HasPrefix(r.Note, "no test file") {
		t.Errorf("card, no test file: %+v", r)
	}
}
