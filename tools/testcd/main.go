// testcd writes the files of the OMABOOT_TEST disc into a directory; burn it as an ISO 9660 CD-R
// labelled OMABOOT_TEST (tools/testcd/README in the directory says how).
//
//	go run ./tools/testcd OUTDIR
package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/omaboot/boot-live/internal/handson"
)

const readme = `OMABOOT_TEST disc
=================

This CD-R is a test disc for OmaBoot? Live (omaboot.live). Put it in a Mac's
optical drive during the Optical drive test: OmaBoot? reads the start of it,
checks the contents of OMABOOT-TEST.BIN against a fixed pattern, and ejects it.

Nothing on this disc is needed for anything else. Any CD or DVD works for the
test; this one also proves the drive returned the right data, not just data.

To make another: go run ./tools/testcd DIR, then
  xorriso -as mkisofs -V OMABOOT_TEST -iso-level 3 -o omaboot-test.iso DIR
  xorriso -as cdrecord -v dev=/dev/sr0 -eject omaboot-test.iso
`

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: testcd OUTDIR")
		os.Exit(2)
	}
	for name, data := range map[string][]byte{"OMABOOT-TEST.BIN": handson.TestDiscFile(), "README.TXT": []byte(readme)} {
		if err := os.WriteFile(filepath.Join(os.Args[1], name), data, 0o644); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}
}
