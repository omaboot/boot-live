// Package assets holds the files built into the binary: the catalog snapshot (GET
// /api/v1/snapshot) and the Mac names (GET /api/v1/macs), fetched by `make snapshot` when the
// tool is built, so a Mac can be identified with no network. The fonts in fonts/ are not built
// in: the images carry them in /usr/share/omaboot/fonts/.
package assets

import _ "embed"

// Snapshot is the catalog snapshot the tool was built with.
//
//go:embed snapshot.json
var Snapshot []byte

// Macs is the catalog's list of Macs, for their names.
//
//go:embed macs.json
var Macs []byte
