// Package buildinfo carries the tool's version, set at build time:
//
//	go build -ldflags "-X 'github.com/omaboot/boot-live/internal/buildinfo.Version=0.1.0 dev' \
//	  -X github.com/omaboot/boot-live/internal/buildinfo.Commit=4e1c9a2"
package buildinfo

import "runtime/debug"

// Version is the release, e.g. "0.1.0", or "0.1.0 dev" for a development build.
var Version = "dev"

// Commit is the boot-live commit it was built from (short hash).
var Commit = ""

// Omarchy is the Omarchy release the images are built from, reported when the image itself
// doesn't say (/usr/share/omaboot/omarchy-version, /etc/os-release). Set with -ldflags.
var Omarchy = "4.0.6"

// String is the version as shown at the bottom right of every screen, e.g. "0.1.0 dev 4e1c9a2".
func String() string {
	c := Commit
	if c == "" {
		if bi, ok := debug.ReadBuildInfo(); ok {
			for _, s := range bi.Settings {
				if s.Key == "vcs.revision" && len(s.Value) >= 7 {
					c = s.Value[:7]
				}
			}
		}
	}
	if c == "" {
		return Version
	}
	return Version + " " + c
}
