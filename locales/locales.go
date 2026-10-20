// Package locales holds the message catalogs (gettext .po), embedded in the binary.
// boot-live.pot is the template made from the source by `make po`; each language has
// <lang>/LC_MESSAGES/boot-live.po. English is the source language: its msgstrs stay empty.
package locales

import "embed"

// FS holds every catalog.
//
//go:embed boot-live.pot */LC_MESSAGES/boot-live.po
var FS embed.FS

// Domain is the catalog's file name without extension.
const Domain = "boot-live"
