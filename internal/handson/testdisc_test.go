package handson

import (
	"strings"
	"testing"
)

// disc lays the test file where a burned disc would: label in the volume descriptor, the file
// at a sector boundary inside the first MB.
func testDisc(fileAt int) []byte {
	b := make([]byte, discLen)
	copy(b[pvdLabelAt:], testDiscLabel)
	copy(b[fileAt:], TestDiscFile())
	return b
}

func TestCheckTestDisc(t *testing.T) {
	if got := checkTestDisc(testDisc(100 * testDiscSector)); got != "ok" {
		t.Fatalf("good disc: %q", got)
	}
	bad := testDisc(100 * testDiscSector)
	bad[100*testDiscSector+testDiscSector+5000] ^= 1
	if got := checkTestDisc(bad); !strings.HasPrefix(got, "bad: the data differs") {
		t.Fatalf("flipped byte: %q", got)
	}
	nofile := make([]byte, discLen)
	copy(nofile[pvdLabelAt:], testDiscLabel)
	if got := checkTestDisc(nofile); !strings.HasPrefix(got, "bad") {
		t.Fatalf("label without file: %q", got)
	}
	if got := checkTestDisc(make([]byte, discLen)); got != "" {
		t.Fatalf("any other disc: %q", got)
	}
}
