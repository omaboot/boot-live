// Package keyboard is the console's keyboard layout: the layouts Hello offers, with the names and
// console keymaps of Omarchy's own installer form (its OMARCHY_KEYBOARD_LAYOUTS), and the one
// macOS used last, read from NVRAM, as the default. Loading a keymap is the console package's.
package keyboard

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/omaboot/boot-live/internal/i18n"
	"github.com/omaboot/boot-live/internal/sysroot"
)

// Layout is one keyboard layout.
type Layout struct {
	Name   string // Omarchy's name, e.g. "English (UK)" (a msgid: translated with i18n.S where drawn)
	Keymap string // the console keymap loadkeys takes, e.g. "uk"
	// Letters is where the letter keys sit: "" QWERTY, "qwertz" (Y and Z swapped) or "azerty"
	// (A and Q, Z and W swapped, M right of L), after the folder kbd keeps the keymap in
	// (keymaps/i386/qwertz, …/azerty). The keyboard test draws its legends by it.
	Letters string
	Main    bool // offered first; the rest are under "Other layouts…"
}

// Layouts are Omarchy's installer layouts that a console can do: the main ones first, then the
// others in Omarchy's order. Left out: the ones whose keymap is us with an input method
// (Chinese, Hong Kong, Japanese on a US keyboard, Korean, Lao, Taiwan), which the console can't
// type.
var Layouts = []Layout{
	{Name: i18n.M("English (US)"), Keymap: "us", Main: true},
	{Name: i18n.M("English (UK)"), Keymap: "uk", Main: true},
	{Name: i18n.M("German"), Keymap: "de", Letters: "qwertz", Main: true},
	{Name: i18n.M("French"), Keymap: "fr", Letters: "azerty", Main: true},
	{Name: i18n.M("Spanish"), Keymap: "es", Main: true},
	{Name: i18n.M("Italian"), Keymap: "it", Main: true},
	{Name: i18n.M("Swedish"), Keymap: "sv-latin1", Main: true},
	{Name: i18n.M("Danish"), Keymap: "dk-latin1", Main: true},
	{Name: i18n.M("Norwegian"), Keymap: "no-latin1", Main: true},
	{Name: i18n.M("Finnish"), Keymap: "fi", Main: true},
	{Name: i18n.M("German (Switzerland)"), Keymap: "de_CH-latin1", Letters: "qwertz", Main: true},
	{Name: i18n.M("French (Switzerland)"), Keymap: "fr_CH", Letters: "qwertz", Main: true},
	{Name: i18n.M("French (Canada)"), Keymap: "cf", Main: true},
	{Name: i18n.M("Japanese"), Keymap: "jp106", Main: true},
	{Name: i18n.M("Portuguese"), Keymap: "pt-latin1", Main: true},
	{Name: i18n.M("Dutch"), Keymap: "nl", Main: true},

	{Name: i18n.M("English (US, Dvorak)"), Keymap: "dvorak"},
	{Name: i18n.M("English (US, Colemak)"), Keymap: "colemak"},
	{Name: i18n.M("Belarusian"), Keymap: "by"},
	{Name: i18n.M("Belgian"), Keymap: "be-latin1", Letters: "azerty"},
	{Name: i18n.M("Bulgarian"), Keymap: "bg-cp1251"},
	{Name: i18n.M("Croatian"), Keymap: "croat", Letters: "qwertz"},
	{Name: i18n.M("Czech"), Keymap: "cz"},
	{Name: i18n.M("Estonian"), Keymap: "et"},
	{Name: i18n.M("Georgian"), Keymap: "ge"},
	{Name: i18n.M("Greek"), Keymap: "gr"},
	{Name: i18n.M("Hebrew"), Keymap: "il"},
	{Name: i18n.M("Hungarian"), Keymap: "hu", Letters: "qwertz"},
	{Name: i18n.M("Icelandic"), Keymap: "is-latin1"},
	{Name: i18n.M("Irish"), Keymap: "ie"},
	{Name: i18n.M("Kazakh"), Keymap: "kazakh"},
	{Name: i18n.M("Kyrgyz"), Keymap: "kyrgyz"},
	{Name: i18n.M("Latvian"), Keymap: "lv"},
	{Name: i18n.M("Lithuanian"), Keymap: "lt"},
	{Name: i18n.M("Macedonian"), Keymap: "mk-utf"},
	{Name: i18n.M("Polish"), Keymap: "pl"},
	{Name: i18n.M("Portuguese (Brazil)"), Keymap: "br-abnt2"},
	{Name: i18n.M("Romanian"), Keymap: "ro"},
	{Name: i18n.M("Russian"), Keymap: "ru"},
	{Name: i18n.M("Serbian"), Keymap: "sr-latin", Letters: "qwertz"},
	{Name: i18n.M("Slovak"), Keymap: "sk-qwertz", Letters: "qwertz"},
	{Name: i18n.M("Slovenian"), Keymap: "slovene", Letters: "qwertz"},
	{Name: i18n.M("Spanish (Latin American)"), Keymap: "la-latin1"},
	{Name: i18n.M("Tajik"), Keymap: "tj_alt-UTF8"},
	{Name: i18n.M("Turkish"), Keymap: "trq"},
	{Name: i18n.M("Ukrainian"), Keymap: "ua"},
}

// US is the console's own layout, the default.
var US = Layouts[0]

// Main are the layouts offered first; Other the rest.
func Main() []Layout { return pick(true) }

// Other are the layouts under "Other layouts…".
func Other() []Layout { return pick(false) }

func pick(main bool) []Layout {
	var out []Layout
	for _, l := range Layouts {
		if l.Main == main {
			out = append(out, l)
		}
	}
	return out
}

// ByKeymap finds a layout by its keymap ("uk") or its name ("English (UK)"), any case.
func ByKeymap(s string) (Layout, bool) {
	for _, l := range Layouts {
		if strings.EqualFold(l.Keymap, s) || strings.EqualFold(l.Name, s) {
			return l, true
		}
	}
	return Layout{}, false
}

// How the layout in use was chosen.
const (
	FromMacOS   = "macos"   // macOS's last layout, from NVRAM (by its ID, or by its language)
	FromDefault = "default" // nothing to go by: English (US)
	FromTester  = "picked"  // picked on Hello
)

// Choice is the layout in use and how it came to be.
type Choice struct {
	Layout Layout
	How    string // FromMacOS, FromDefault or FromTester
	// NVRAM is macOS's prev-lang:kbd value as read ("en-GB:2"), "" when there's none; ByLanguage:
	// its layout ID isn't one we know, so its language decided.
	NVRAM      string
	ByLanguage bool
	// Loaded: the layout is the console's, loaded with loadkeys (false off the Linux console: a
	// fixture run, a terminal emulator, the demo). Failed is the last layout that didn't load and
	// why, "" when none: the one before it stays in use.
	Loaded bool
	Failed string
	Tried  Layout // the layout that didn't load, {} when none
}

// Detail is the report's line for the choice (English), e.g. "English (UK) (keymap uk), from
// macOS: prev-lang:kbd en-GB:2".
func (c Choice) Detail() string {
	s := fmt.Sprintf("%s (keymap %s)", c.Layout.Name, c.Layout.Keymap)
	switch c.How {
	case FromMacOS:
		s += ", from macOS: prev-lang:kbd " + c.NVRAM
		if c.ByLanguage {
			s += " (by its language: the layout ID isn't one OmaBoot? knows)"
		}
	case FromTester:
		s += ", picked by the tester"
		if c.NVRAM != "" {
			s += " (macOS's prev-lang:kbd was " + c.NVRAM + ")"
		}
	default:
		s += ", the default"
		if c.NVRAM != "" && c.Failed == "" {
			s += " (macOS's prev-lang:kbd " + c.NVRAM + " names no layout OmaBoot? offers)"
		}
	}
	if !c.Loaded {
		s += "; not loaded (not on the Linux console)"
	}
	if c.Failed != "" {
		s += "; " + c.Failed
	}
	return s
}

// EFIVar is the variable macOS keeps its last keyboard layout in: prev-lang:kbd, under Apple's
// boot vendor GUID, in efivarfs.
const EFIVar = "/sys/firmware/efi/efivars/prev-lang:kbd-7c436110-ab2a-4bbb-a880-fe41995c9f82"

// Default is the layout to start with: macOS's last one when NVRAM names one we offer, else
// English (US).
func Default(r sysroot.Root) Choice {
	raw := r.Bytes(EFIVar)
	if len(raw) <= 4 {
		return Choice{Layout: US, How: FromDefault}
	}
	val, lang, id, hasID := Parse(raw[4:]) // efivarfs: 4 bytes of attributes, then the value
	c := Choice{Layout: US, How: FromDefault, NVRAM: val}
	if l, ok := ByID(id); hasID && ok {
		c.Layout, c.How = l, FromMacOS
	} else if l, ok := ByLanguage(lang); ok {
		c.Layout, c.How, c.ByLanguage = l, FromMacOS, true
	}
	return c
}

// Parse reads prev-lang:kbd's value (after efivarfs's attributes): ASCII "language:ID", such as
// "en:0", "en-GB:2" or "cs:-14193", maybe NUL-terminated. val is the value as shown, lang the
// language tag, id Apple's keyboard layout ID when there is one.
func Parse(b []byte) (val, lang string, id int, hasID bool) {
	if i := strings.IndexByte(string(b), 0); i >= 0 {
		b = b[:i]
	}
	val = strings.TrimSpace(string(b))
	for _, r := range val {
		if r < 0x20 || r > 0x7e { // not the ASCII the variable holds: show nothing of it
			return "", "", 0, false
		}
	}
	lang, num, _ := strings.Cut(val, ":")
	n, err := strconv.Atoi(strings.TrimSpace(num))
	return val, strings.TrimSpace(lang), n, err == nil
}

// byID maps Apple's keyboard layout IDs to the console keymap of the layout we offer for them.
// The IDs and their layouts are from OpenCore's list (OpenCorePkg,
// Utilities/AppleKeyboardLayouts/AppleKeyboardLayouts.txt, read from macOS's
// AppleKeyboardLayouts.bundle). Only layouts whose letters and symbols match a console keymap
// are here; one that only roughly does (Apple's Polish, QWERTZ, against kbd's QWERTY pl; Czech;
// Brazilian other than ABNT2; Bulgarian; Romanian; Turkish F) is left to its language, or to the
// default.
var byID = map[int]string{
	0: "us", 252: "us", 15000: "us", -2: "us", 15: "us", 29: "us", // U.S., ABC, US International PC, US Extended, Australian, Canadian (English)
	2: "uk", 250: "uk", // British, British PC
	3: "de", 253: "de", 92: "de", -18133: "de", // German, ABC QWERTZ, Austrian, German DIN 2137
	1: "fr", 60: "fr", 1111: "fr", 251: "fr", // French, French PC, French numerical, ABC AZERTY
	8: "es", 87: "es", // Spanish, Spanish ISO
	4: "it", 223: "it", // Italian, Italian Pro
	224: "sv-latin1", 7: "sv-latin1", // Swedish, Swedish Pro
	9:  "dk-latin1",                   // Danish
	12: "no-latin1", -12: "no-latin1", // Norwegian, Norwegian Extended
	17: "fi", -17: "fi", // Finnish, Finnish Extended
	19: "de_CH-latin1",     // Swiss German
	18: "fr_CH",            // Swiss French
	80: "cf", -19336: "cf", // Canadian CSA, Canadian French PC
	-3049: "jp106",     // Kana (a Japanese keyboard)
	10:    "pt-latin1", // Portuguese
	26:    "nl",        // Dutch
	16300: "dvorak", 12825: "colemak",
	6:      "be-latin1",                                 // Belgian
	128:    "br-abnt2",                                  // Brazilian ABNT2
	89:     "la-latin1",                                 // Latin American
	50:     "ie",                                        // Irish
	-21:    "is-latin1",                                 // Icelandic
	30788:  "pl",                                        // Polish Pro
	30778:  "cz",                                        // Czech QWERTY
	30763:  "hu",                                        // Hungarian
	-11013: "sk-qwertz",                                 // Slovak
	-68:    "croat", -66: "slovene", -19521: "sr-latin", // Croatian, Slovenian, Serbian Latin
	30764: "et", 30765: "lv", 30761: "lt", // Estonian, Latvian, Lithuanian
	-18944: "gr", -18432: "il", // Greek, Hebrew
	19456: "ru", 19518: "ua", 19517: "by", 19523: "mk-utf", // Russian, Ukrainian, Byelorussian, Macedonian
	-27650: "ge", -19501: "kazakh", 19459: "kyrgyz", 19460: "tj_alt-UTF8", // Georgian, Kazakh, Kyrgyz, Tajik
	-35: "trq", -36: "trq", // Turkish QWERTY, Turkish QWERTY PC
}

// ByID is the layout we offer for Apple's keyboard layout ID.
func ByID(id int) (Layout, bool) {
	if k, ok := byID[id]; ok {
		return ByKeymap(k)
	}
	return Layout{}, false
}

// byRegion and byLang are the layouts for a language tag when its layout ID isn't known: the
// language with its region first (en-GB, de-CH, fr-CA), then the language alone, only where one
// layout is the clear answer. "uk" as a language is Ukrainian, never the United Kingdom.
var (
	byRegion = map[string]string{
		"en-gb": "uk", "en-ie": "ie",
		"de-ch": "de_CH-latin1", "fr-ch": "fr_CH", "fr-ca": "cf", "fr-be": "be-latin1", "nl-be": "be-latin1",
		"pt-br": "br-abnt2", "es-es": "es",
	}
	byLang = map[string]string{
		"en": "us", "de": "de", "fr": "fr", "es": "es", "it": "it", "sv": "sv-latin1", "da": "dk-latin1",
		"nb": "no-latin1", "nn": "no-latin1", "no": "no-latin1", "fi": "fi", "ja": "jp106", "pt": "pt-latin1",
		"nl": "nl", "is": "is-latin1", "et": "et", "lv": "lv", "lt": "lt", "hu": "hu", "sk": "sk-qwertz",
		"hr": "croat", "sl": "slovene", "el": "gr", "he": "il", "ru": "ru", "uk": "ua",
	}
)

// ByLanguage is the layout for a language tag ("en-GB", "de", "pt_PT"), when one is clear. A
// Spanish speaker outside Spain gets the Latin American layout.
func ByLanguage(tag string) (Layout, bool) {
	t := strings.ToLower(strings.ReplaceAll(tag, "_", "-"))
	lang, region, _ := strings.Cut(t, "-")
	if region != "" {
		if k, ok := byRegion[lang+"-"+region]; ok {
			return ByKeymap(k)
		}
		if lang == "es" && len(region) == 2 || lang == "es" && region == "419" {
			return ByKeymap("la-latin1")
		}
	}
	if k, ok := byLang[lang]; ok {
		return ByKeymap(k)
	}
	return Layout{}, false
}
