// testfile writes OmaBoot?'s test file, OMABOOT-TEST.BIN, into a directory: copy it to the top
// folder of any USB stick, SD card or FireWire drive for the port tests (internal/handson/
// testfile.go has its layout). It's made once and published at get.omaboot.live/test/ with the
// test disc and their signed SHA256SUMS; it doesn't change with releases (TestTestFileFixed).
//
//	go run ./tools/testfile OUTDIR
package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/omaboot/boot-live/internal/handson"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: testfile OUTDIR")
		os.Exit(2)
	}
	if err := os.WriteFile(filepath.Join(os.Args[1], "OMABOOT-TEST.BIN"), handson.TestFile(), 0o644); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
