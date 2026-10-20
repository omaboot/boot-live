package handson

import (
	"bytes"
	"fmt"
)

// The OMABOOT_TEST disc (tools/testcd builds it): an ISO 9660 CD-R labelled OMABOOT_TEST holding
// OMABOOT-TEST.BIN, whose first sector is the header below and whose remaining bytes are a fixed
// pseudo-random stream. The disc read (optical.go) takes the first MB of any data disc; when
// that MB holds the header, the bytes after it are checked against the stream, so a drive that
// answers with wrong data (rather than failing) is caught. Any other disc still reads as before.
const (
	testDiscLabel  = "OMABOOT_TEST"
	testDiscHeader = "OMABOOT-TEST-CD 1\n"
	testDiscSector = 2048
	testDiscBody   = 2<<20 - testDiscSector // bytes of stream after the header sector
	testDiscMin    = 64 << 10               // the least stream that must be in the first MB to judge
	pvdLabelAt     = 16*testDiscSector + 40 // the volume id in the primary volume descriptor
)

// TestDiscFile is OMABOOT-TEST.BIN: the header sector, then the stream.
func TestDiscFile() []byte {
	b := make([]byte, testDiscSector+testDiscBody)
	copy(b, testDiscHeader)
	testDiscStream(b[testDiscSector:])
	return b
}

// testDiscStream fills p with the stream (xorshift64*, fixed seed; streamGen in testfile.go)
// from its start.
func testDiscStream(p []byte) { newStream().fill(p) }

// checkTestDisc looks for the OMABOOT_TEST disc in the first MB of a data disc: "" when it isn't
// one, "ok" when its header is there and the stream after it is right, otherwise "bad: ..." with
// where the data went wrong.
func checkTestDisc(buf []byte) string {
	labelled := len(buf) >= pvdLabelAt+32 && bytes.HasPrefix(bytes.TrimSpace(buf[pvdLabelAt:pvdLabelAt+32]), []byte(testDiscLabel))
	at := -1
	for o := 0; o+len(testDiscHeader) <= len(buf); o += testDiscSector {
		if bytes.HasPrefix(buf[o:], []byte(testDiscHeader)) {
			at = o
			break
		}
	}
	if at < 0 {
		if labelled {
			return "bad: the label is there but the test file's header wasn't in the first MB"
		}
		return ""
	}
	got := buf[at+testDiscSector:]
	if len(got) > testDiscBody {
		got = got[:testDiscBody]
	}
	if len(got) < testDiscMin {
		return "bad: too little of the test file in the first MB to check"
	}
	want := make([]byte, len(got))
	testDiscStream(want)
	if i := firstDiff(got, want); i >= 0 {
		return "bad: the data differs " + sizeKB(i) + " into the test file"
	}
	return "ok"
}

func firstDiff(a, b []byte) int {
	for i := range a {
		if a[i] != b[i] {
			return i
		}
	}
	return -1
}

func sizeKB(n int) string {
	return fmt.Sprintf("%d KB", n>>10)
}
