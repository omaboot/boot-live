package handson

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"strings"

	"github.com/omaboot/boot-live/internal/fsread"
)

// OmaBoot?'s test file: OMABOOT-TEST.BIN, copied to the top folder of any
// USB stick, SD card or FireWire drive the person already has, so nothing has to be erased for
// the port tests (tools/testfile writes it; made once, it's at get.omaboot.live/test/, and
// TestTestFileFixed keeps it byte for byte what's there). The port tests find it by reading the
// disk's own file system structures (internal/fsread: FAT, exFAT, HFS+; nothing is mounted,
// nothing written) and check every byte of it:
//
//	4096 bytes  the header: "OMABOOT-TEST-FILE 1\n", a few lines saying what the file is, zeros
//	32 MiB      the stream: the OMABOOT_TEST disc's (testdisc.go), from its start
//
// A file of the name whose header isn't this one, or that's shorter (a copy cut off), isn't
// the test file: the disk reads as if it weren't there. Names match without case, and a
// browser's " (1)" before ".BIN" is fine.
const (
	testFileName = "OMABOOT-TEST.BIN"
	testFileTag  = "OMABOOT-TEST-FILE 1\n"
	testFileHead = 4096
	testFileBody = 32 << 20
)

const testFileText = testFileTag + `
This is OmaBoot? Live's test file (omaboot.live). Copy it as it is to the
top folder of any USB stick, SD card or FireWire drive, and plug that in
during OmaBoot?'s port tests: OmaBoot? finds this file, reads it and checks
every byte against a fixed pattern, so a port that hands back wrong data is
caught. Nothing is written to the drive, and of its files only this one is
read.

Delete it whenever you like.
`

// TestFile is OMABOOT-TEST.BIN: the header, then the stream.
func TestFile() []byte {
	b := make([]byte, testFileHead+testFileBody)
	copy(b, testFileHeader())
	testDiscStream(b[testFileHead:])
	return b
}

func testFileHeader() []byte {
	h := make([]byte, testFileHead)
	copy(h, testFileText)
	return h
}

// isTestFileName: OMABOOT-TEST.BIN in any case, "OMABOOT-TEST (1).BIN" too.
func isTestFileName(name string) bool {
	n := strings.ToUpper(name)
	return strings.HasPrefix(n, "OMABOOT-TEST") && strings.HasSuffix(n, ".BIN")
}

// streamGen gives the stream (testdisc.go's) from its start, a piece at a time.
type streamGen struct {
	s   uint64
	w   [8]byte
	pos int // bytes of w given
}

func newStream() *streamGen { return &streamGen{s: 0x4F4D41424F4F5421, pos: 8} }

func (g *streamGen) fill(p []byte) {
	for i := range p {
		if g.pos == 8 {
			g.s ^= g.s >> 12
			g.s ^= g.s << 25
			g.s ^= g.s >> 27
			binary.LittleEndian.PutUint64(g.w[:], g.s*0x2545F4914F6CDD1D)
			g.pos = 0
		}
		p[i] = g.w[g.pos]
		g.pos++
	}
}

// testFileHit is a test file on a disk, or why there's none.
type testFileHit struct {
	File *fsread.File // nil: none (Note says why)
	Note string       // English, for the report
}

// testFileFinder is a blockDev that knows where its test file is without its file system
// being read (the demo's made-up disks).
type testFileFinder interface {
	testFile() testFileHit
}

// findTestFile finds the test file on dev (sector: its logical block size): the first file of
// the name whose header is right and that's long enough. prog hears each read of the file
// system's structures (a slow disk isn't taken for a stalled one), and stops them by answering
// false.
func findTestFile(dev blockDev, sector int, prog func(ReadProgress) bool) testFileHit {
	if f, ok := dev.(testFileFinder); ok {
		return f.testFile()
	}
	res := fsread.Find(devAt{dev, prog}, dev.Size(), sector, isTestFileName)
	var why []string
	for i := range res.Files {
		f := &res.Files[i]
		where := f.Name + " (" + f.Vol.String() + ")"
		switch {
		case f.Err != "":
			why = append(why, where+": "+f.Err)
		case f.Size < testFileHead+testFileBody:
			why = append(why, fmt.Sprintf("%s is %d of %d bytes: a copy that was cut off", where, f.Size, testFileHead+testFileBody))
		default:
			h, err := readSpans(dev, f.Extents, testFileHead)
			switch {
			case err != nil:
				why = append(why, where+": its first bytes didn't read: "+err.Error())
			case !bytes.Equal(h, testFileHeader()):
				why = append(why, where+" isn't OmaBoot?'s test file (its header is another)")
			default:
				return testFileHit{File: f}
			}
		}
	}
	if len(why) > 0 {
		return testFileHit{Note: strings.Join(why, "; ")}
	}
	var vols []string
	for _, v := range res.Volumes {
		switch {
		case v.Read:
			vols = append(vols, v.String())
		case v.Err != "":
			vols = append(vols, v.String()+", not read: "+v.Err)
		default:
			vols = append(vols, v.String()+", a file system this doesn't read")
		}
	}
	if len(vols) == 0 {
		return testFileHit{Note: "no test file: no file system found"}
	}
	return testFileHit{Note: "no test file in the top folder of " + strings.Join(vols, "; ")}
}

// devAt reads a blockDev for fsread (which reads whole, aligned pages).
type devAt struct {
	dev  blockDev
	prog func(ReadProgress) bool
}

var errStopped = errors.New("stopped")

func (d devAt) ReadAt(p []byte, off int64) (int, error) {
	if !d.prog(ReadProgress{Pass: 1}) {
		return 0, errStopped
	}
	b, err := readRange(d.dev, off, int64(len(p)))
	if err != nil {
		return 0, err
	}
	return copy(p, b), nil
}

// readSpans is the first n bytes of a file laid out as ext (n small: a header).
func readSpans(dev blockDev, ext []fsread.Extent, n int64) ([]byte, error) {
	var out []byte
	for _, e := range ext {
		if int64(len(out)) >= n {
			break
		}
		b, err := readRange(dev, e.Off, min(e.Len, n-int64(len(out))))
		if err != nil {
			return nil, err
		}
		out = append(out, b...)
	}
	return out, nil
}

// testFileSpans are where the stream's bytes are: the file's extents past its header.
func testFileSpans(ext []fsread.Extent) []fsread.Extent {
	var out []fsread.Extent
	skip, want := int64(testFileHead), int64(testFileBody)
	for _, e := range ext {
		if skip >= e.Len {
			skip -= e.Len
			continue
		}
		e.Off, e.Len = e.Off+skip, min(e.Len-skip, want)
		skip = 0
		out = append(out, e)
		if want -= e.Len; want == 0 {
			break
		}
	}
	return out
}

// verifyTestFile finds the test file on dev as the port tests do and compares every byte of it
// with TestFile (the console's testfile reads back what it wrote). It says where it found it.
func verifyTestFile(dev blockDev, sector int) (string, error) {
	hit := findTestFile(dev, sector, func(ReadProgress) bool { return true })
	if hit.File == nil {
		return "", errors.New(hit.Note)
	}
	want := TestFile()
	got, err := readSpans(dev, hit.File.Extents, int64(len(want)))
	switch {
	case err != nil:
		return "", fmt.Errorf("it didn't read back: %w", err)
	case !bytes.Equal(got, want):
		return "", errors.New("it read back different")
	}
	return hit.File.Name + " (" + hit.File.Vol.String() + ")", nil
}
