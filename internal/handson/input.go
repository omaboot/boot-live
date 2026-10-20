package handson

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/omaboot/boot-live/internal/boot"
	"github.com/omaboot/boot-live/internal/checks"
	"github.com/omaboot/boot-live/internal/i18n"
	"github.com/omaboot/boot-live/internal/identify"
)

// The Mac's own keyboard and trackpad (input.keyboard, input.trackpad, input.force-touch): which
// input devices are built in, and what the tests read from them. The tests are in
// input_keyboard.go, input_trackpad.go and input_force.go.
//
// Built in, by driver: a USB "Apple Internal Keyboard / Trackpad" (hid_apple for the keys,
// bcm5974 or appletouch for the trackpad) on most Macs; applespi's "Apple SPI Keyboard" and
// "Apple SPI Touchpad" on the 12-inch MacBook and the 2016–2017 MacBook Pros without a T2; on T2
// Macs the same USB names, reached through the T2 (apple-bce). A Magic Keyboard or Magic Trackpad
// is never the built-in one, even on USB.

// Key, button and axis codes (linux/input-event-codes.h) the input tests use.
const (
	evSynReport = 0

	keyEsc       = 1
	keyBackspace = 14
	keyA         = 30
	keySpace     = 57
	keyDelete    = 111
	keyFn        = 0x1d0

	btnLeft          = 0x110
	btnToolFinger    = 0x145
	btnToolDoubletap = 0x14d
	btnToolTripletap = 0x14e
	btnToolQuadtap   = 0x14f
	btnToolQuinttap  = 0x148

	absX            = 0x00
	absY            = 0x01
	absPressure     = 0x18
	absMtSlot       = 0x2f
	absMtPositionX  = 0x35
	absMtPositionY  = 0x36
	absMtTrackingID = 0x39
	absMtPressure   = 0x3a

	relX = 0x00
	relY = 0x01

	busUSB       = 0x03
	busBluetooth = 0x05
	busSPI       = 0x1c
)

// devID is an input device's bus, vendor and product (sysfs device/id).
type devID struct{ bus, vendor, product int }

func readID(io IO, d Device) devID {
	p := "/sys/class/input/" + d.Event + "/device/id/"
	h := func(f string) int {
		n, _ := strconv.ParseUint(io.Read(p+f), 16, 32)
		return int(n)
	}
	return devID{h("bustype"), h("vendor"), h("product")}
}

// String is the device's bus and IDs for the evidence: "usb 05ac:024c", "spi".
func (id devID) String() string {
	bus := map[int]string{busUSB: "usb", busBluetooth: "bluetooth", busSPI: "spi"}[id.bus]
	if bus == "" {
		bus = fmt.Sprintf("bus %#x", id.bus)
	}
	if id.vendor == 0 && id.product == 0 {
		return bus
	}
	return fmt.Sprintf("%s %04x:%04x", bus, id.vendor, id.product)
}

// separate: the names of Apple's own separate keyboards, trackpads and mice.
func separate(name string) bool {
	for _, s := range []string{"Magic Keyboard", "Magic Trackpad", "Magic Mouse", "Apple Keyboard", "Wireless Keyboard", "Bluetooth"} {
		if strings.Contains(name, s) {
			return true
		}
	}
	return false
}

// builtIn reports whether an input device is part of the Mac: by the names its drivers give, else
// an Apple device on USB or SPI that isn't one of Apple's separate ones.
func builtIn(io IO, d Device) bool {
	n := d.Name
	switch {
	case strings.Contains(n, "Internal Keyboard"), strings.Contains(n, "SPI Keyboard"), strings.Contains(n, "SPI Touchpad"),
		strings.HasPrefix(d.Phys, "applespi"), n == "bcm5974", n == "appletouch":
		return true
	case separate(n):
		return false
	}
	id := readID(io, d)
	return id.vendor == 0x05ac && (id.bus == busUSB || id.bus == busSPI)
}

// isKeyboard: a device with letter keys (not the power button, the lid or the display's keys).
func isKeyboard(d Device) bool { return d.Has("key", keyA) && d.Has("key", keySpace) }

// isTouchpad: a device reporting finger positions (multi-touch slots, or one position and a
// finger count).
func isTouchpad(d Device) bool {
	return d.Has("abs", absMtPositionX) || d.Has("abs", absX) && d.Has("key", btnToolFinger)
}

// isPointer: a device moving a pointer by relative steps with a button (a trackpad in mouse mode,
// when its multi-touch driver didn't bind).
func isPointer(d Device) bool { return d.Has("rel", relX) && d.Has("key", btnLeft) }

// fixtureRun reports whether the IO is a fixture tree (--sysroot): its input devices can't be
// opened, so an input test decides nothing there.
func fixtureRun(c *Ctx) bool {
	s, ok := c.IO.(*System)
	return ok && !s.Real()
}

// notOnMac is the input tests' result on a fixture tree.
func notOnMac(id string) checks.Result {
	return NotTested(id, checks.Other, "not on the Mac itself: no input devices to read", i18n.T("not on the Mac itself"))
}

// fixtureScreen is an input test's screen on a fixture tree.
func fixtureScreen() Screen {
	return Screen{Face: boot.Idle, Do: i18n.T("Nothing to read here."), Live: []Line{Gone(i18n.T("a fixture tree: no input devices to open"))}}
}

// identifier is the Mac's model identifier, or "" (the demo).
func identifier(e *Env) string {
	if e == nil || e.Checks == nil {
		return ""
	}
	return e.Checks.Ident.Identifier
}

// model splits the identifier: "MacBookPro", 13, 2 for MacBookPro13,2.
func model(id string) (fam string, major, minor int) {
	fam, major = identify.Model(id)
	if _, m, ok := strings.Cut(id, ","); ok {
		minor, _ = strconv.Atoi(m)
	}
	return fam, major, minor
}

// devNames lists devices for the evidence: "bcm5974 (event3, usb 05ac:024c)".
func devNames(io IO, ds []Device) string {
	var out []string
	for _, d := range ds {
		out = append(out, fmt.Sprintf("%s (%s, %s)", d.Name, d.Event, readID(io, d)))
	}
	return strings.Join(out, "; ")
}

// absRanger reads an axis's range (EVIOCGABS): the Mac's System does, the Fake from its files.
type absRanger interface {
	AbsRange(d Device, code int) (lo, hi int, ok bool)
}

// AbsRange is the axis range the Fake keeps under /dev/input/<event>/absinfo/<code> ("lo hi"):
// not a real file, the Fake's stand-in for EVIOCGABS.
func (f *Fake) AbsRange(d Device, code int) (int, int, bool) {
	var lo, hi int
	_, err := fmt.Sscan(f.Read(fmt.Sprintf("/dev/input/%s/absinfo/%d", d.Event, code)), &lo, &hi)
	return lo, hi, err == nil && hi > lo
}

// span is an axis range; ok false when it couldn't be read.
type span struct {
	lo, hi int
	ok     bool
}

func axis(io IO, d Device, code int) span {
	if r, ok := io.(absRanger); ok {
		if lo, hi, ok := r.AbsRange(d, code); ok && hi > lo {
			return span{lo, hi, true}
		}
	}
	return span{}
}

// at is v's place in the range, 0–1.
func (s span) at(v int) float64 {
	if !s.ok {
		return 0.5
	}
	return min(1, max(0, float64(v-s.lo)/float64(s.hi-s.lo)))
}

func (s span) String() string {
	if !s.ok {
		return "range unknown"
	}
	return fmt.Sprintf("%d…%d", s.lo, s.hi)
}

// capWords writes a capability bitmap as sysfs does (64-bit words, highest first), for the
// demo's devices.
func capWords(codes ...int) string {
	n := 0
	for _, c := range codes {
		n = max(n, c/64+1)
	}
	w := make([]uint64, n)
	for _, c := range codes {
		w[c/64] |= 1 << (c % 64)
	}
	var out []string
	for i := n - 1; i >= 0; i-- {
		out = append(out, strconv.FormatUint(w[i], 16))
	}
	return strings.Join(out, " ")
}

// demoID gives a demo device its bus and IDs.
func demoID(f *Fake, event string, bus, vendor, product int) {
	p := "/sys/class/input/" + event + "/device/id/"
	f.Set(p+"bustype", fmt.Sprintf("%04x", bus))
	f.Set(p+"vendor", fmt.Sprintf("%04x", vendor))
	f.Set(p+"product", fmt.Sprintf("%04x", product))
}
