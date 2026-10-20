// Package i18n routes every user-facing string through a gettext catalog (locales/). The msgid
// is the English text, so a missing translation falls back to English. Formatting follows fmt;
// translators can reorder arguments with explicit indexes such as %[2]d.
package i18n

import (
	"io/fs"
	"path"

	"github.com/leonelquinteros/gotext"

	"github.com/omaboot/boot-live/locales"
)

var po = load("en")

func load(lang string) *gotext.Po {
	p := gotext.NewPo()
	b, err := fs.ReadFile(locales.FS, path.Join(lang, "LC_MESSAGES", locales.Domain+".po"))
	if err == nil {
		p.Parse(b)
	}
	return p
}

// Use switches to a language's catalog ("en", "de", …); unknown languages fall back to English.
func Use(lang string) {
	if _, err := fs.Stat(locales.FS, path.Join(lang, "LC_MESSAGES", locales.Domain+".po")); err != nil {
		lang = "en"
	}
	po = load(lang)
}

// T translates msgid and formats it with args (fmt verbs only when args are given).
func T(msgid string, args ...any) string {
	if msgid == "" {
		return "" // "" is the catalog's header entry
	}
	return po.Get(msgid, args...)
}

// S translates text kept in data (marked with M where it is defined); no formatting.
func S(msgid string) string {
	if msgid == "" {
		return ""
	}
	return lookup(po, msgid) // no args, so no formatting: a % in data stays as it is
}

// lookup is Po.Get as a value, so vet doesn't take data for a format string.
var lookup = (*gotext.Po).Get

// N translates a message with a plural form chosen by n.
func N(msgid, plural string, n int, args ...any) string { return po.GetN(msgid, plural, n, args...) }

// M marks a string for the catalog without translating it (gettext_noop): for text kept in data
// that is translated later with T.
func M(msgid string) string { return msgid }
