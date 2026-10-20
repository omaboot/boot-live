package handson

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/omaboot/boot-live/internal/boot"
	"github.com/omaboot/boot-live/internal/checks"
	g "github.com/omaboot/boot-live/internal/grid"
	"github.com/omaboot/boot-live/internal/i18n"
)

// input.keyboard, "Built-in keyboard and function keys" (laptops only: the criterion's `when` is
// feature:builtin-keyboard, so desktops never get this test). The screen draws the Mac's
// keyboard in its physical shape (US ANSI, ISO or JIS, when the keyboard tells: keyboardShape;
// the legends are US ones whatever the shape) and lights each key as the built-in keyboard
// reports it through evdev. The keyboard is grabbed for the test, so no key reaches the console:
// no VT switch, no Ctrl+Alt+Del, nothing the screen would act on.
//
// The top row counts as its F-key or as the media key hid_apple (or applespi) sends for it by
// default (hid_apple's fnmode), from the fn table the kernel uses for that keyboard; fn+arrows,
// fn+delete and fn+return count for the arrow, delete and return keys. fn counts when the
// keyboard reports it (KEY_FN, as hid_apple and applespi do). Left out, drawn dim: the power key
// and Touch ID (pressing it would shut the Mac down), and the Touch Bar (input.touch-bar,
// decided apart; on 2016–2019 MacBook Pros without a physical Esc it holds Esc and the F-keys).
//
//   - supported: every key on the drawn layout registered (method automatic);
//   - partial: some keys registered and the tester says the dark ones don't respond (observed;
//     the keys that never registered are listed in the evidence);
//   - failed: no built-in keyboard device at all (automatic), or the tester says none of its
//     keys respond and none registered (observed).
//
// Getting out: every key seen ends the test by itself. Holding esc (or delete, where there's no
// physical esc) for 2 s, 20 s without a key, or Esc from another keyboard hand the keys back and
// show the answers: keep typing, or "some keys don't respond".

func init() {
	Register(Spec{ID: "input.keyboard", Group: NoPlug, Order: 10, Title: i18n.M("Keyboard"),
		Brief: i18n.M("press every key once"), Expect: 2 * time.Minute,
		New: func(*Env) Run { return &keyboard{} }, Demo: demoKeyboard})
}

// kbHold is how long esc (or delete) is held to stop; kbIdle how long without a key hands the
// keys back by itself.
const (
	kbHold = 2.0
	kbIdle = 20.0
)

// kbKey is one key of the drawn keyboard.
type kbKey struct {
	label string   // the legend: a msgid when word, else as printed (q, 5, F7)
	short string   // a shorter msgid for a narrow key
	word  bool     // label (and short) are msgids
	name  string   // the evidence's name, when not the label (space, up)
	w     float64  // width in key units: a letter key is 1
	codes []uint16 // the codes that light it
	off   string   // drawn, not part of the test: why (English, for the evidence)
	tall  bool     // its cap goes on down into the next row (the ISO and JIS return)
	part  bool     // the lower part of the tall key above: drawn with it, not a key of its own
}

func (k kbKey) id() string {
	if k.name != "" {
		return k.name
	}
	return k.label
}

// kbLayout is the drawn keyboard.
type kbLayout struct {
	name  string // for the evidence
	how   string // how the shape was told, for the evidence (keyboardShape)
	table string // the kernel's fn table for the top row (fnTables)
	exit  string // msgid: the key held to stop (esc, or delete without a physical esc)
	rows  [][]kbKey
	power bool // a power key (or Touch ID) is drawn: warn about it
}

// fKeys are KEY_F1–KEY_F12.
var fKeys = [12]uint16{59, 60, 61, 62, 63, 64, 65, 66, 67, 68, 87, 88}

// fnTables are the media keys the top row sends without fn (hid_apple's fn translation tables;
// applespi's is "modern"): brightness, Exposé (KEY_SCALE), Dashboard, keyboard light, previous,
// play, next, mute, volume; 0 where the key sends only its F-key.
var fnTables = map[string][12]uint16{
	"modern":    {224, 225, 120, 204, 229, 230, 165, 164, 163, 113, 114, 115},
	"powerbook": {224, 225, 113, 114, 115, 69, 227, 228, 229, 230, 0, 0},
	"air":       {224, 225, 120, 204, 0, 165, 164, 163, 113, 114, 115, 161},
}

var fnOrder = []string{"modern", "powerbook", "air"}

// fnTable is the table hid_apple picks for a USB Apple keyboard by product ID: the 2006
// MacBook and MacBook Pro keyboards (below 0x021d) the PowerBook one, the Late 2010 MacBook Air
// (0x023f–0x0244) its own; every other built-in keyboard (applespi and T2 too) the modern one.
func fnTable(id devID) string {
	if id.vendor == 0x05ac && id.bus == busUSB {
		switch p := id.product; {
		case p > 0 && p < 0x021d:
			return "powerbook"
		case p >= 0x023f && p <= 0x0244:
			return "air"
		}
	}
	return "modern"
}

// kbShape is the keyboard's physical shape.
type kbShape int

const (
	shapeANSI kbShape = iota // US
	shapeISO                 // UK and most of Europe: a key between the left shift and Z, a tall return
	shapeJIS                 // Japan: eisu, kana, yen and ro, a tall return, control left of A
)

var shapeNames = [...]string{"US ANSI laptop", "UK/ISO laptop", "JIS laptop"}

// kbShapes are the product IDs (vendor 05ac, USB) of the built-in keyboards, ANSI, ISO and JIS,
// one keyboard generation to a line: the USB_DEVICE_ID_APPLE_GEYSER3_ANSI … WELLSPRING9_JIS
// IDs of the Linux kernel's drivers/hid/hid-ids.h, whose names say the shape. The T2 keyboards
// (WELLSPRINGT2_*) have one ID for every shape and aren't here.
var kbShapes = [][3]int{
	{0x0217, 0x0218, 0x0219}, // GEYSER3
	{0x021a, 0x021b, 0x021c}, // GEYSER4
	{0x0229, 0x022a, 0x022b}, // GEYSER4_HF
	{0x0223, 0x0224, 0x0225}, // WELLSPRING
	{0x0230, 0x0231, 0x0232}, // WELLSPRING2
	{0x0236, 0x0237, 0x0238}, // WELLSPRING3
	{0x023f, 0x0240, 0x0241}, // WELLSPRING4
	{0x0242, 0x0243, 0x0244}, // WELLSPRING4A
	{0x0245, 0x0246, 0x0247}, // WELLSPRING5
	{0x0252, 0x0253, 0x0254}, // WELLSPRING5A
	{0x024c, 0x024d, 0x024e}, // WELLSPRING6
	{0x0249, 0x024a, 0x024b}, // WELLSPRING6A
	{0x0262, 0x0263, 0x0264}, // WELLSPRING7
	{0x0259, 0x025a, 0x025b}, // WELLSPRING7A
	{0x0290, 0x0291, 0x0292}, // WELLSPRING8
	{0x0272, 0x0273, 0x0274}, // WELLSPRING9
}

// HID country codes (the HID spec's bCountryCode) that tell a shape.
const (
	countryISO = 13 // "International (ISO)": what hid_apple takes for an ISO keyboard
	countryJIS = 15 // "Japan (Katakana)"
	countryUS  = 33
)

// kbBoard is what the built-in keyboard tells of itself.
type kbBoard struct {
	id      devID
	fn      bool // it reports the fn key
	country int  // its HID country code (sysfs country); -1 without one (applespi isn't HID)
	iso     int  // hid_apple's iso_layout parameter: -1 auto (the default, and when unread), 0 off, 1 on
}

// readBoard reads what keyboardLayout needs from the built-in keyboard's input devices.
func readBoard(io IO, ds []Device) kbBoard {
	b := kbBoard{country: -1, iso: -1}
	for i, d := range ds {
		if i == 0 {
			b.id = readID(io, d)
			if n, err := strconv.ParseUint(io.Read("/sys/class/input/"+d.Event+"/device/device/country"), 16, 8); err == nil {
				b.country = int(n)
			}
		}
		b.fn = b.fn || d.Has("key", keyFn)
	}
	if n, err := strconv.Atoi(io.Read("/sys/module/hid_apple/parameters/iso_layout")); err == nil {
		b.iso = n
	}
	return b
}

// keyboardShape tells the built-in keyboard's shape, and how it was told (for the evidence).
//
//   - A USB keyboard (2006–2015 MacBooks, MacBook Pros, MacBook Airs) by its product ID
//     (kbShapes).
//   - One not in the table (the T2 keyboards: one product ID for every shape) by the HID country
//     code in its descriptor, which usbhid keeps and /sys/class/input/eventN/device/device/country
//     shows: 13 is what hid_apple itself goes by for an ISO keyboard (hid-apple.c checks
//     hid->country against it before its ISO swap, and lists every T2 keyboard with that quirk),
//     15 the spec's Japan, 33 its US. Whether a T2 keyboard fills the code in isn't confirmed on
//     a Mac yet; one that leaves it 0 is drawn ANSI, as not detected.
//   - applespi's SPI keyboards (MacBook8,1–10,1, MacBookPro13,x–14,x) aren't HID devices: no
//     country code, nothing in /sys that tells the shape. Drawn ANSI, as not detected.
func keyboardShape(id devID, country int) (kbShape, string) {
	if id.vendor == 0x05ac && id.bus == busUSB {
		for _, ids := range kbShapes {
			for s, p := range ids {
				if p == id.product {
					return kbShape(s), fmt.Sprintf("shape from the product ID %04x:%04x", id.vendor, id.product)
				}
			}
		}
	}
	how := fmt.Sprintf("shape from the HID country code %d", country)
	switch country {
	case countryISO:
		return shapeISO, how
	case countryJIS:
		return shapeJIS, how
	case countryUS:
		return shapeANSI, how
	}
	return shapeANSI, "drawn as US ANSI: layout not detected"
}

// isoSwapped: whether hid_apple swaps KEY_GRAVE and KEY_102ND on this ISO keyboard: its
// iso_layout parameter on, or auto (the default) and the keyboard reporting country code 13
// (hid-apple.c, apple_event's apple_iso_keyboard translation; every built-in ISO keyboard in
// kbShapes and every T2 one has APPLE_ISO_TILDE_QUIRK). Apple's ISO keyboards send the key left
// of 1 (§) as the usage Linux reads as KEY_102ND and the key beside the left shift as
// KEY_GRAVE, the other way round from a PC's: the swap puts them back, § as KEY_GRAVE. Both
// codes are on the ISO drawing either way, so every key can register; a wrong guess here only
// lights the other one of the two.
func isoSwapped(b kbBoard) bool {
	return b.iso > 0 || b.iso < 0 && b.country == countryISO
}

// keyboardLayout is the keyboard of a Mac laptop in its shape: identifier and chip from the
// catalog ("" in the demo: a MacBook Air's), b what the keyboard tells (product ID for the shape
// and the fn table, country code, whether it reports the fn key).
func keyboardLayout(ident, chip string, b kbBoard) kbLayout {
	fam, major, minor := model(ident)
	shape, how := keyboardShape(b.id, b.country)
	l := kbLayout{name: shapeNames[shape], how: how, table: fnTable(b.id), exit: i18n.M("esc")}
	esc := kbKey{label: i18n.M("esc"), word: true, w: 1.5, codes: []uint16{keyEsc}}
	var top []kbKey
	switch {
	case fam == "MacBookPro" && (chip == "t1" || chip == "t2"):
		l.name += " with Touch Bar"
		bar := 13.5
		if major >= 16 { // the 2019–2020 MacBook Pros have a physical esc again
			top, bar = append(top, esc), 12
		} else {
			l.exit = i18n.M("delete")
		}
		top = append(top, kbKey{label: i18n.M("Touch Bar: not part of this test"), short: i18n.M("Touch Bar"), word: true, w: bar,
			off: "the Touch Bar (input.touch-bar, decided apart)"})
		top = append(top, kbKey{label: i18n.M("Touch ID"), short: i18n.M("ID"), word: true, w: 1, off: "Touch ID, the power key (pressing it would shut the Mac down)"})
		l.power = true
	default:
		top = append(top, esc)
		t := fnTables[l.table]
		for i, f := range fKeys {
			k := kbKey{label: fmt.Sprintf("F%d", i+1), w: 1, codes: []uint16{f}}
			if t[i] != 0 {
				k.codes = append(k.codes, t[i])
			}
			top = append(top, k)
		}
		eject := fam == "MacBook" && major <= 7 || fam == "MacBookPro" && major <= 9 || fam == "MacBookAir" && major <= 2
		switch {
		case eject && fam != "":
			top = append(top, kbKey{label: i18n.M("eject"), short: i18n.M("ej"), word: true, w: 1, codes: []uint16{161}})
		case chip == "t1" || chip == "t2":
			top = append(top, kbKey{label: i18n.M("Touch ID"), short: i18n.M("ID"), word: true, w: 1, off: "Touch ID, the power key (pressing it would shut the Mac down)"})
			l.power = true
		default:
			top = append(top, kbKey{label: i18n.M("power"), short: i18n.M("pwr"), word: true, w: 1, off: "the power key (pressing it would shut the Mac down)"})
			l.power = true
		}
	}
	keys := func(s string, first uint16) []kbKey { // letters or symbols with consecutive codes
		var out []kbKey
		for i, r := range s {
			out = append(out, kbKey{label: string(r), w: 1, codes: []uint16{first + uint16(i)}})
		}
		return out
	}
	one := func(s string, code uint16) kbKey { return kbKey{label: s, w: 1, codes: []uint16{code}} }
	cat := func(parts ...any) []kbKey {
		var out []kbKey
		for _, p := range parts {
			switch v := p.(type) {
			case kbKey:
				out = append(out, v)
			case []kbKey:
				out = append(out, v...)
			}
		}
		return out
	}
	wide := func(k kbKey, w float64) kbKey { k.w = w; return k }
	legacy := fam == "MacBook" && (major <= 4 || major == 5 && minor == 2) || fam == "MacBookPro" && major <= 4
	ropt := kbKey{label: i18n.M("option"), short: i18n.M("opt"), word: true, name: "right option", w: 1, codes: []uint16{100, 96}}
	if legacy { // the small enter key right of command, before the unibody keyboards
		ropt = kbKey{label: i18n.M("enter"), short: i18n.M("ent"), word: true, w: 1, codes: []uint16{96, 100}}
	}
	fnKey := kbKey{label: i18n.M("fn"), word: true, w: 1, codes: []uint16{keyFn}}
	if !b.fn {
		fnKey.codes, fnKey.off = nil, "fn (the keyboard doesn't report it as a key)"
	}
	del := kbKey{label: i18n.M("delete"), short: i18n.M("del"), word: true, w: 1.5, codes: []uint16{keyBackspace, keyDelete}}
	tab := kbKey{label: i18n.M("tab"), word: true, w: 1.5, codes: []uint16{15}}
	caps := kbKey{label: i18n.M("caps lock"), short: i18n.M("caps"), word: true, w: 1.75, codes: []uint16{58}}
	ret := kbKey{label: i18n.M("return"), short: i18n.M("ret"), word: true, w: 1.75, codes: []uint16{28, 110}}
	lshift := kbKey{label: i18n.M("shift"), word: true, name: "left shift", w: 2.25, codes: []uint16{42}}
	rshift := kbKey{label: i18n.M("shift"), short: i18n.M("sh"), word: true, name: "right shift", w: 1.25, codes: []uint16{54}}
	up := kbKey{label: "▲", name: "up", w: 1, codes: []uint16{103, 104}}
	ctrl := kbKey{label: i18n.M("control"), short: i18n.M("ctrl"), word: true, w: 1, codes: []uint16{29}}
	lopt := kbKey{label: i18n.M("option"), short: i18n.M("opt"), word: true, name: "left option", w: 1, codes: []uint16{56}}
	lcmd := kbKey{label: i18n.M("command"), short: i18n.M("cmd"), word: true, name: "left command", w: 1.25, codes: []uint16{125}}
	space := kbKey{name: "space", w: 5, codes: []uint16{keySpace}}
	rcmd := kbKey{label: i18n.M("command"), short: i18n.M("cmd"), word: true, name: "right command", w: 1.25, codes: []uint16{126}}
	arrows := []kbKey{{label: "◄", name: "left", w: 1, codes: []uint16{105, 102}}, {label: "▼", name: "down", w: 1, codes: []uint16{108, 109}},
		{label: "►", name: "right", w: 1, codes: []uint16{106, 107}}}
	letters := cat(keys("qwertyuiop", 16), one("[", 26), one("]", 27)) // with the two keys after p
	home := cat(keys("asdfghjkl", 30), one(";", 39), one("'", 40))
	lower := cat(keys("zxcvbnm", 44), one(",", 51), one(".", 52), one("/", 53))
	switch shape {
	case shapeISO:
		// The UK keyboard's shape (xkeyboard-config's applealu_iso geometry has the same rows):
		// § left of 1, the return two rows tall with \ left of its lower part, the left shift
		// short to make room for the key beside it (` on Apple's UK keyboards).
		sect, beside := uint16(86), uint16(41) // KEY_102ND, KEY_GRAVE as sent (isoSwapped)
		if isoSwapped(b) {
			sect, beside = beside, sect
		}
		l.how += fmt.Sprintf(", § counted as code %d and ` as %d", sect, beside)
		l.rows = [][]kbKey{
			top,
			cat(one("§", sect), keys("1234567890", 2), one("-", 12), one("=", 13), del),
			cat(tab, letters, kbKey{label: ret.label, short: ret.short, word: true, w: 1, codes: ret.codes, tall: true}),
			cat(caps, home, one("\\", 43), kbKey{part: true, w: 0.75}),
			cat(wide(lshift, 1.25), one("`", beside), lower, rshift, up),
			cat(fnKey, ctrl, lopt, lcmd, space, rcmd, ropt, arrows),
		}
	case shapeJIS:
		// Apple's JIS shape, after xkeyboard-config's applealu_jis geometry: no key left of 1 (a
		// wide 1 instead) and ¥ before a short delete; a short tab and the tall return; control
		// left of A; _ (ro) before a short right shift; eisu and kana beside a short space, caps
		// lock moved down. The JIS keys come through hid_apple's JIS report descriptor fix
		// (APPLE_RDESC_JIS) as the HID usages International3 (KEY_YEN), International1 (KEY_RO),
		// LANG2 (eisu, KEY_HANJA) and LANG1 (kana, KEY_HANGEUL). The legends are US ones (=, [,
		// ], ', \ where JIS has ^, @, [, :, ]); the console font has no kana, so eisu and kana
		// are written out. On the bottom row the place of fn and the right option isn't
		// confirmed on a Mac: the right option is left out (a key drawn but missing would keep
		// the test from passing; one there but not drawn only shows as a code not on the
		// drawing).
		l.rows = [][]kbKey{
			top,
			cat(kbKey{label: "1", w: 1.5, codes: []uint16{2}}, keys("234567890", 3), one("-", 12), one("=", 13),
				kbKey{label: "¥", name: "yen", w: 1, codes: []uint16{124}}, wide(del, 1)),
			cat(wide(tab, 1), letters, kbKey{label: ret.label, short: ret.short, word: true, w: 1.5, codes: ret.codes, tall: true}),
			cat(wide(ctrl, 1.25), home, one("\\", 43), kbKey{part: true, w: 1.25}),
			cat(wide(lshift, 1.75), lower, kbKey{label: "_", name: "ro", w: 1, codes: []uint16{89}}, wide(rshift, 0.75), up),
			cat(wide(caps, 1), lopt, lcmd, kbKey{label: i18n.M("eisu"), word: true, w: 1, codes: []uint16{123}}, wide(space, 4),
				kbKey{label: i18n.M("kana"), word: true, w: 1, codes: []uint16{122}}, rcmd, fnKey, arrows),
		}
	default:
		l.rows = [][]kbKey{
			top,
			cat(one("`", 41), keys("1234567890", 2), one("-", 12), one("=", 13), del),
			cat(tab, letters, one("\\", 43)),
			cat(caps, home, ret),
			cat(lshift, lower, rshift, up),
			cat(fnKey, ctrl, lopt, lcmd, space, rcmd, ropt, arrows),
		}
	}
	// the arrows: drawn as an inverted T (up beside the right shift), where Apple's laptops have
	// half-height up and down keys in the bottom row
	return l
}

// keys are the layout's keys in drawing order, with each one's row.
func (l kbLayout) keys() (out []kbKey, row []int) {
	for r, ks := range l.rows {
		for _, k := range ks {
			out = append(out, k)
			row = append(row, r)
		}
	}
	return out, row
}

// lookup maps codes to keys: what each key reports, then the other fn tables' media keys for the
// top row (a keyboard whose table isn't the one expected still lights the right F-key).
func (l kbLayout) lookup() (code, alt map[uint16]int) {
	ks, row := l.keys()
	code, alt = map[uint16]int{}, map[uint16]int{}
	fslot := map[int]int{} // F-key number (0–11) → key
	for i, k := range ks {
		if k.off != "" {
			continue
		}
		for _, c := range k.codes {
			if _, dup := code[c]; !dup {
				code[c] = i
			}
		}
		if row[i] == 0 {
			for n, f := range fKeys {
				if len(k.codes) > 0 && k.codes[0] == f {
					fslot[n] = i
				}
			}
		}
	}
	for _, t := range fnOrder {
		for n, c := range fnTables[t] {
			if i, ok := fslot[n]; ok && c != 0 {
				if _, taken := code[c]; !taken {
					if _, dup := alt[c]; !dup {
						alt[c] = i
					}
				}
			}
		}
	}
	return code, alt
}

type kbPhase int

const (
	kbTyping  kbPhase = iota // the keys go to the test
	kbLetGo                  // done or stopping: waiting for the keys to be let go
	kbAnswers                // the keys are the console's again: the answers
)

type keyboard struct {
	lay       kbLayout
	keys      []kbKey
	row       []int
	code, alt map[uint16]int
	seen      []bool
	media     int // top-row keys first seen as their media key
	fkeys     int // … as their F-key
	stray     map[uint16]bool
	down      map[uint16]float64 // codes held now, since when
	builtin   []Device
	others    []Device
	in        Input
	phase     kbPhase
	at        float64 // when the phase began
	last      float64 // the last key from the built-in keyboard
	said      float64 // when Boot's line changed
	mile      int     // Boot's line: 0 start, 1 halfway, 2 nearly done
	console   int     // keys from elsewhere (another keyboard): not counted
	final     string  // the status decided (for the drawing)
	reopened  float64
}

func (k *keyboard) Start(c *Ctx) {
	k.down, k.stray = map[uint16]float64{}, map[uint16]bool{}
	if fixtureRun(c) {
		c.Decide(Outcome{Results: []checks.Result{notOnMac("input.keyboard")}, Say: i18n.T("a fixture tree has no keys to press.")})
		return
	}
	for _, d := range c.IO.Inputs() {
		switch {
		case !isKeyboard(d):
		case builtIn(c.IO, d):
			k.builtin = append(k.builtin, d)
		default:
			k.others = append(k.others, d)
		}
	}
	chip := ""
	if c.Env != nil && c.Env.Checks != nil {
		chip = c.Env.Checks.Ident.Chip
	}
	k.lay = keyboardLayout(identifier(c.Env), chip, readBoard(c.IO, k.builtin))
	k.keys, k.row = k.lay.keys()
	k.code, k.alt = k.lay.lookup()
	k.seen = make([]bool, len(k.keys))
	if len(k.builtin) == 0 {
		ev := "no built-in keyboard among the input devices"
		if len(k.others) > 0 {
			ev += "; other keyboards: " + devNames(c.IO, k.others)
		}
		k.final = checks.Failed
		c.Decide(Outcome{Results: []checks.Result{Result("input.keyboard", checks.Failed, Automatic, ev, i18n.T("no built-in keyboard device"))},
			Say: i18n.T("the kernel shows no built-in keyboard at all. that's a fail.")})
		return
	}
	if !k.open(c) {
		c.Decide(Outcome{Results: []checks.Result{NotTested("input.keyboard", checks.Other,
			"couldn't open the built-in keyboard's input device: "+devNames(c.IO, k.builtin), i18n.T("couldn't open the keyboard"))},
			Say: i18n.T("I couldn't open the keyboard to listen to it. not tested.")})
	}
	k.said = 0.2
}

// open takes the built-in keyboard for the test (grabbed: the console sees none of its keys).
func (k *keyboard) open(c *Ctx) bool {
	in, devs := OpenAll(c.IO, func(d Device) bool { return isKeyboard(d) && builtIn(c.IO, d) }, true)
	if len(devs) == 0 {
		in.Close()
		return false
	}
	k.in, k.phase, k.at, k.last = in, kbTyping, c.T, c.T
	k.down = map[uint16]float64{}
	return true
}

// release hands the keys back to the console and shows the answers.
func (k *keyboard) release(c *Ctx) {
	if k.in != nil {
		k.in.Close()
		k.in = nil
	}
	k.phase, k.at, k.down = kbAnswers, c.T, map[uint16]float64{}
}

// counted: a key to press (not one left out, not the lower part of a tall key).
func (x kbKey) counted() bool { return x.off == "" && !x.part }

func (k *keyboard) total() (n int) {
	for _, x := range k.keys {
		if x.counted() {
			n++
		}
	}
	return n
}

func (k *keyboard) count() (n int) {
	for i, x := range k.keys {
		if x.counted() && k.seen[i] {
			n++
		}
	}
	return n
}

// missing are the keys not seen yet: their names for the evidence, or their legends to show.
func (k *keyboard) missing(show bool) []string {
	var out []string
	for i, x := range k.keys {
		if !x.counted() || k.seen[i] {
			continue
		}
		if show {
			out = append(out, legend(x, 99))
		} else {
			out = append(out, x.id())
		}
	}
	return out
}

func (k *keyboard) light(code uint16) {
	i, ok := k.code[code]
	if !ok {
		i, ok = k.alt[code]
	}
	if !ok {
		k.stray[code] = true
		return
	}
	if k.seen[i] {
		return
	}
	k.seen[i] = true
	if k.row[i] == 0 && strings.HasPrefix(k.keys[i].label, "F") {
		if code == k.keys[i].codes[0] {
			k.fkeys++
		} else {
			k.media++
		}
	}
}

func (k *keyboard) read(c *Ctx) {
	if k.in == nil {
		return
	}
	for _, e := range k.in.Events() {
		if e.Type != EvKey {
			continue
		}
		switch e.Value {
		case 1:
			k.down[e.Code] = c.T
			k.last = c.T
			k.light(e.Code)
		case 0:
			delete(k.down, e.Code)
		}
	}
	if k.in.Err() != nil && c.T-k.reopened >= 1 { // the keyboard went away (a USB reset): take it again
		k.reopened = c.T
		k.in.Close()
		ph := k.phase
		if !k.open(c) {
			k.in = nil
			k.release(c)
			return
		}
		k.phase = ph
	}
}

func (k *keyboard) Step(c *Ctx) {
	if k.phase == kbAnswers {
		return
	}
	k.read(c)
	if k.phase == kbAnswers {
		return
	}
	n, total := k.count(), k.total()
	m := 0 // Boot's line: halfway, then nearly done
	switch {
	case n > 0 && total-n <= 5:
		m = 2
	case n*2 >= total:
		m = 1
	}
	if m != k.mile {
		k.mile, k.said = m, c.T
	}
	switch k.phase {
	case kbTyping:
		for _, x := range []uint16{keyEsc, keyBackspace, keyDelete} {
			if t, ok := k.down[x]; ok && c.T-t >= kbHold {
				k.phase, k.at = kbLetGo, c.T
			}
		}
		if n == total {
			k.phase, k.at = kbLetGo, c.T
		}
		if k.phase == kbTyping && c.T-k.last >= kbIdle && len(k.down) == 0 {
			k.release(c)
		}
	case kbLetGo: // the keys still down would reach the console once they're handed back
		if len(k.down) > 0 && c.T-k.at < 3 {
			return
		}
		if n == total {
			k.decide(c, false)
			return
		}
		k.release(c)
	}
}

func (k *keyboard) TakesKeys(*Ctx) bool { return k.phase != kbAnswers }

// Key is a key the console still got while the test has the keyboard: one from another
// keyboard (the built-in one is grabbed), or the demo's terminal. Esc (or Ctrl+C) hands the
// keys back; the rest aren't counted.
func (k *keyboard) Key(c *Ctx, key string) {
	if key == "esc" || key == "ctrl+c" {
		k.release(c)
		return
	}
	k.console++
}

func (k *keyboard) Act(c *Ctx, i int) {
	if k.phase != kbAnswers {
		return
	}
	switch i {
	case 0:
		k.open(c)
	case 1:
		k.decide(c, true)
	}
}

// decide: every key seen (the machine's), or the tester's "they don't respond".
func (k *keyboard) decide(c *Ctx, told bool) {
	n, total := k.count(), k.total()
	var o Outcome
	switch {
	case n == total:
		o = Outcome{Results: []checks.Result{Result("input.keyboard", checks.Supported, Automatic, k.evidence(c, ""), i18n.T("all %d keys registered", total))},
			Say: i18n.T("all %d keys answered, top row included. tidy.", total)}
	case n == 0:
		o = Outcome{Results: []checks.Result{Result("input.keyboard", checks.Failed, Observed,
			k.evidence(c, "; the tester says none of the keys respond"), i18n.T("no key registered"))},
			Say: i18n.T("not one key reached me. that's a fail.")}
	default:
		m := total - n
		o = Outcome{Results: []checks.Result{Result("input.keyboard", checks.Partial, Observed,
			k.evidence(c, "; the tester says the keys not registered don't respond"), i18n.N("%d key didn't register", "%d keys didn't register", m, m))},
			Say: i18n.N("%d key stayed dark. partial.", "%d keys stayed dark. partial.", m, m)}
	}
	k.final = o.Results[0].Status
	if k.in != nil {
		k.in.Close()
		k.in = nil
	}
	c.Decide(o)
}

func (k *keyboard) evidence(c *Ctx, tail string) string {
	ev := fmt.Sprintf("built-in keyboard: %s; layout drawn: %s (%s; top row as F-keys or the %s fn table's media keys); %d of %d keys registered",
		devNames(c.IO, k.builtin), k.lay.name, k.lay.how, k.lay.table, k.count(), k.total())
	if k.media+k.fkeys > 0 {
		ev += fmt.Sprintf(" (top row: %d as media keys, %d as F-keys)", k.media, k.fkeys)
	}
	if m := k.missing(false); len(m) > 0 {
		ev += "; not registered: " + strings.Join(m, " ")
	}
	var off []string
	for _, x := range k.keys {
		if x.off != "" {
			off = append(off, x.off)
		}
	}
	if len(off) > 0 {
		ev += "; left out: " + strings.Join(off, ", ")
	}
	if len(k.stray) > 0 {
		var cs []int
		for code := range k.stray {
			cs = append(cs, int(code))
		}
		sort.Ints(cs)
		ev += "; codes not on the drawn layout: " + strings.Trim(fmt.Sprint(cs), "[]")
	}
	if k.console > 0 {
		ev += fmt.Sprintf("; %d keys came from another keyboard (not counted)", k.console)
	}
	if len(k.others) > 0 {
		ev += "; other keyboards: " + devNames(c.IO, k.others)
	}
	return ev + tail
}

func (k *keyboard) Stop(*Ctx) {
	if k.in != nil {
		k.in.Close()
		k.in = nil
	}
}

func (k *keyboard) View(c *Ctx) Screen {
	s := Screen{Face: boot.Idle, SayAt: k.said, OutTitle: i18n.T("What I see")}
	if fixtureRun(c) {
		return fixtureScreen()
	}
	if len(k.builtin) == 0 {
		s.Steps = []string{i18n.T("No built-in keyboard to test.")}
		s.Live = []Line{Bad(i18n.T("no built-in keyboard found"))}
		return s
	}
	n, total := k.count(), k.total()
	exit := i18n.S(k.lay.exit)
	s.Steps = []string{i18n.T("Press every key once, the top row too."), i18n.T("Let go of the keys."),
		i18n.T("Answer: do the dark keys respond?")}
	s.Happens = i18n.T("Keys light up above as they report in; the top row counts as F-key or media key. Hold %s 2 s to stop early.", exit)
	if k.lay.power {
		s.Happens += " " + i18n.T("Not the power key: it shuts the Mac down.")
	}
	if k.phase == kbAnswers && c.T-k.at >= 20 { // the keys came through evdev: the run knows when they went quiet
		s.Hint = i18n.T("the dark keys up top are the ones I haven't seen yet. keep typing, or tell me they're dead.")
	}
	switch k.phase {
	case kbTyping:
		s.Step, s.Face = 0, boot.Work
		switch k.mile {
		case 0:
			s.Say = i18n.T("every key, once. I'm counting.")
		case 1:
			s.Say = i18n.T("halfway. the keyboard keeps up.")
		default:
			s.Say = i18n.T("nearly there. the last few are the sneaky ones.")
		}
		s.Waiting = "·  " + i18n.T("%d of %d keys", n, total)
	case kbLetGo:
		s.Step = 1
		s.Say, s.SayAt = i18n.T("got it. let go, and the keys are yours again."), k.at
		s.Waiting = "·  " + i18n.T("waiting for the keys to be let go")
	case kbAnswers:
		m := total - n
		s.Step = 2
		s.Say, s.SayAt = i18n.N("%d key still dark. dead, or not pressed yet?", "%d keys still dark. dead, or not pressed yet?", m, m), k.at
		if n == 0 {
			s.Say = i18n.T("not a single key so far. is the keyboard dead?")
		}
		s.Waiting = "·  " + i18n.T("waiting for your answer")
	}
	if k.final != "" {
		s.Step = len(s.Steps)
	}
	d := k.builtin[0]
	s.Live = []Line{Fact(i18n.T("built-in keyboard: %s (%s)", d.Name, d.Event))}
	if k.phase == kbAnswers || k.final != "" {
		seen := Fact(i18n.T("%d of %d keys seen", n, total))
		if m := k.missing(true); len(m) > 0 && len(m) <= 12 { // more show in the drawing
			seen = Fact(i18n.T("%d of %d keys seen; still dark: %s", n, total, strings.Join(m, " ")))
		}
		s.Live = append(s.Live, seen)
	} else {
		s.Live = append(s.Live, Wait(i18n.T("%d of %d keys seen", n, total)))
	}
	if k.console > 0 {
		s.Live = append(s.Live, Gone(i18n.N("%d key from another keyboard, not counted", "%d keys from another keyboard, not counted", k.console, k.console)))
	}
	s.Anim = k.draw
	typing := k.phase != kbAnswers
	dead := Action{Label: i18n.T("Some keys are dead"), Does: i18n.T("the dark ones do nothing"), Off: typing}
	if n == 0 {
		dead.Label, dead.Does = i18n.T("No key responds"), i18n.T("the keyboard is dead")
	}
	s.Actions = []Action{{Label: i18n.T("Keep typing"), Does: i18n.T("back to the test"), Off: typing}, dead}
	if typing {
		s.Note = i18n.T("The keys are the test's now: hold %s 2 s to get them back.", exit)
	}
	return s
}

// legend is a key's legend in room cells: the full one, the short one, or the full one cut.
func legend(k kbKey, room int) string {
	t := k.label
	if k.word {
		t = i18n.S(k.label)
	}
	if len([]rune(t)) > room && k.short != "" {
		t = i18n.S(k.short)
	}
	if r := []rune(t); len(r) > room {
		t = string(r[:max(0, room)])
	}
	return t
}

// draw is the keyboard in the band (the 19-row keyboard didn't fit the band and crowded the
// answers): one row of caps per row of keys, each
// key's legend over its cap where there's room. A cap is dim until the key is seen, violet once
// seen, white while held, red when it stayed dark in a decided partial or fail; the keys not part
// of the test are shaded. A tall key (the ISO and JIS return) is one cap down through the gap
// between its two rows.
func (k *keyboard) draw(gr *g.Grid, x, y, w, h int, _ float64) {
	rows := k.lay.rows
	n := len(rows)
	if h < n || w < 30 {
		return
	}
	u := min(6.0, float64(w-2)/14.5) // cells per key unit: a row is 14.5 units
	pitch, legends := 1, false       // rows apart; a legend row over each row of caps
	switch {
	case h >= 2*n:
		pitch, legends = 2, true
	case h >= 2*n-1:
		pitch = 2
	}
	wc := int(math.Round(14.5 * u))
	x0 := x + max(0, (w-wc)/2)
	y0 := y + max(0, (h-(pitch*n-pitch+1)-btoi(legends))/2)
	i, tall, tcy := 0, -1, 0 // the tall key and its row of caps
	for r, row := range rows {
		cy := y0 + r*pitch + btoi(legends)
		cum := 0.0
		for _, key := range row {
			a := x0 + int(math.Round(cum*u))
			cum += key.w
			b := x0 + int(math.Round(cum*u)) - 1 // a blank cell between keys
			if key.part && tall >= 0 {           // the tall key's lower part, joined to its cap above
				cap, _ := k.capColors(tall)
				end := '█' // its last row reaches as far down as the row's other caps
				if legends {
					end = '▀'
				}
				for cx := a; cx < b; cx++ {
					if legends {
						gr.Set(cx, tcy, '█', cap)
					}
					for jy := tcy + 1; jy < cy; jy++ {
						gr.Set(cx, jy, '█', cap)
					}
					gr.Set(cx, cy, end, cap)
				}
				i++
				continue
			}
			if key.tall {
				tall, tcy = i, cy
			}
			cap, lc := k.capColors(i)
			r := '▄'
			if legends { // the cap under its legend, apart from the row below
				r = '▀'
			}
			if key.off != "" {
				r = '░'
			}
			for cx := a; cx < b; cx++ {
				gr.Set(cx, cy, r, cap)
			}
			if legends {
				lbl := legend(key, b-a)
				gr.Put(a+(b-a-len([]rune(lbl)))/2, cy-1, lbl, lc)
			}
			i++
		}
	}
}

func btoi(b bool) int {
	if b {
		return 1
	}
	return 0
}

// capColors is how key i shows: its cap and its legend.
func (k *keyboard) capColors(i int) (cap, lbl g.Color) {
	x := k.keys[i]
	switch {
	case x.off != "":
		return g.DIM, g.DIM
	case k.held(x):
		return g.WHT, g.WHT
	case k.seen[i]:
		return g.BVIO, g.DIM
	case k.final == checks.Partial || k.final == checks.Failed:
		return g.BRED, g.BRED
	}
	return g.DIM, g.GRY
}

func (k *keyboard) held(x kbKey) bool {
	for _, c := range x.codes {
		if _, ok := k.down[c]; ok {
			return true
		}
	}
	return false
}

// demoKeyboard: the demo's MacBook Air keyboard reports its keys row by row, the top row as
// media keys (hid_apple's default) with two pressed with fn, all but the right option; then esc
// is held to stop (the answers show), and the right option comes last, for "Keep typing".
func demoKeyboard(f *Fake) {
	l := keyboardLayout("", "", kbBoard{id: devID{busUSB, 0x05ac, 0x024c}, fn: true, country: -1, iso: -1})
	ks, row := l.keys()
	all := []int{keyFn}
	for _, k := range ks {
		for _, c := range k.codes {
			all = append(all, int(c))
		}
	}
	for _, t := range fnTables {
		for _, c := range t {
			if c != 0 {
				all = append(all, int(c))
			}
		}
	}
	f.Device("event2", "Apple Inc. Apple Internal Keyboard / Trackpad", map[string]string{"ev": "120013", "key": capWords(all...)})
	demoID(f, "event2", busUSB, 0x05ac, 0x024c)
	press := func(t float64, code uint16, hold float64) {
		f.Event(t, "event2", EvKey, code, 1)
		f.Event(t+hold, "event2", EvKey, code, 0)
	}
	t := 1.2
	for i, k := range ks {
		if k.off != "" || len(k.codes) == 0 || row[i] == 5 && k.label == i18n.M("option") && k.codes[0] == 100 {
			continue
		}
		code := k.codes[0]
		if row[i] == 0 && len(k.codes) > 1 && k.label != "F3" && k.label != "F8" {
			code = k.codes[1] // the media key
		}
		press(t, code, 0.12)
		t += 0.35
	}
	press(t+0.8, keyEsc, 2.2)
	press(t+7, 100, 0.15)
}
