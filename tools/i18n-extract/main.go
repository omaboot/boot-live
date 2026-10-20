// i18n-extract writes the message catalogs from the Go source: locales/boot-live.pot and the
// English catalog. With -check it only reports whether they are up to date (exit 1 if not).
//
//	go run ./tools/i18n-extract [-check]
package main

import (
	"bytes"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/omaboot/boot-live/internal/i18n/potgen"
)

func main() {
	check := flag.Bool("check", false, "only check that the catalogs match the source")
	flag.Parse()
	msgs, err := potgen.Extract(".", "cmd", "internal")
	if err != nil {
		fmt.Fprintln(os.Stderr, "i18n-extract:", err)
		os.Exit(2)
	}
	files := map[string][]byte{
		filepath.Join("locales", "boot-live.pot"):                     potgen.Write(msgs, ""),
		filepath.Join("locales", "en", "LC_MESSAGES", "boot-live.po"): potgen.Write(msgs, "en"),
	}
	stale := false
	for path, want := range files {
		have, _ := os.ReadFile(path)
		if bytes.Equal(have, want) {
			continue
		}
		if *check {
			fmt.Fprintf(os.Stderr, "i18n-extract: %s is out of date: run make po\n", path)
			stale = true
			continue
		}
		if err := os.WriteFile(path, want, 0o644); err != nil {
			fmt.Fprintln(os.Stderr, "i18n-extract:", err)
			os.Exit(2)
		}
		fmt.Printf("wrote %s (%d messages)\n", path, len(msgs))
	}
	if stale {
		os.Exit(1)
	}
}
