package handson

import (
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/omaboot/boot-live/internal/boot"
	"github.com/omaboot/boot-live/internal/checks"
	g "github.com/omaboot/boot-live/internal/grid"
	"github.com/omaboot/boot-live/internal/i18n"
	"github.com/omaboot/boot-live/internal/identify"
)

// ports.usb-a, "USB-A ports", and ports.usb-c, "USB-C / Thunderbolt 3 ports (USB data)": the
// person plugs something into each port of the kind, one at a time; the machine watches the USB
// buses (sysfs, polled twice a second) and records each port a device shows up on: its bus
// path, the device and the speed it negotiated. Then it checks that the port moves data
// (ports_read.go): a disk is read raw, read-only (diskread.go: the OmaBoot?
// stick checked against its image's checksums, anything else read twice and compared), a mouse
// or keyboard has to send a move or a key, anything else counts by enumerating, as before.
// Nothing is ever written to any device.
//
// With the Mac's port map (portmap.go) the test asks for one port at a time, ringed on the map
// (USB-C: the usb-c and thunderbolt3 sockets), and matches whatever shows up on a hub port not
// seen before to the port asked for; the ring stays on it until its check is through, then the
// port takes its state (green: worked; amber: its read failed) and the next one is asked for.
// "A port didn't work" marks the ringed port dead and asks for the next. The OmaBoot? stick is
// in one of the ports since power-on and the machine can't tell which drawn port that is, so
// when there's more than one the test first asks the person; the stick's port counts, and its
// own read (against the image) runs meanwhile. Once every port has a state the test decides by
// itself, except with a failed read still standing: then it waits for a retry on that port or
// Done. Without a map, the person tries each port and picks Done.
//
// Booted with copytoram (the boot menu's "copy to RAM" entry) the system runs from memory and
// the stick is free: the steps have the person move the OmaBoot? stick itself from port to port,
// each port getting the verified read. (The stick watcher, internal/app/stick.go, doesn't run
// with copytoram, so taking it out doesn't stop the run.) Without copytoram the stick stays put
// and the steps ask for another device.
//
// A physical port is one hub port, or a USB 2 and a USB 3 hub port the kernel links as peers
// (usbPeer): a USB 2 stick and a USB 3 stick in the same hole count once. The Mac's own devices
// (keyboard, trackpad, camera, Bluetooth, internal hubs) were there before the screen opened
// and stay put, so they never count; neither does a hard-wired port. The port the OmaBoot?
// stick is in counts as a working port seen since power-on (the Mac booted from it), with a
// note in the evidence. A phone sharing its connection over USB (Env.Phone) is treated the same:
// in since before the test, its port counts as working, and with a map the test asks which port
// it's in after the stick's (not when only one port is left). Moved to another port, it's a new
// device on a new path and counts there too; unplugged and plugged back into the same port, it
// counts again on that port (usbInternal never takes it for one of the Mac's own devices).
//
// On Macs with both kinds (iMac 2017+, iMac Pro, Mac mini 2018, Mac Pro 2019), a device on a
// Thunderbolt 3 chip's USB controller is on a USB-C port, and the USB-A test doesn't count it.
// The USB-C test counts those, and also a device plugged in during it on the Mac's main USB
// controller, unless that hub port is a known USB-A port (this run's USB-A test, Env.USBA, or
// DoesItOmarchy.com's wiring): the iMac18,1's and iMac18,3's USB-C ports have lines there too
// (0.8.5 and 0.8.7 dev: an iPhone in a USB-C port came up as 1-10, USB 3 devices on 2-5,
// 1-13, 1-14, all taken for USB-A ports and the test failed). The OmaBoot? stick and the
// phone, in since before the test, keep the controller's word. Alpine Ridge's controller may
// only power up once something is plugged in, so a USB-C port can take a few seconds to show
// anything.
//
// Alpine Ridge's USB controller on a Mac sleeps when idle (runtime PM) and nothing wakes it for a
// plug-in: Linux doesn't take PCIe PME on Apple machines (drivers/acpi/pci_root.c, since
// "ACPI: Support _OSI("Darwin") correctly"), so a USB 3 device in a USB-C port is never seen
// (the iMac18,1's 0.8.8 dev run: a USB-C stick in both ports, not a line in the kernel log;
// research/findings/alpine-ridge-usb-c-sleep.md). When the test has waited tbWakeAfter with
// nothing happening while one of those controllers sleeps, it keeps them awake (power/control
// on, OmaBoot?'s Thunderbolt 3 USB wake fix) for the rest of the test and puts them back after.
// A port seen on one of them from then on rests on the fix: a pass becomes partial, with the
// fix's ID when DoesItOmarchy.com's registry has it.
//
// Known gap: Macs before 2012 hand USB 1.1 devices to a separate UHCI/OHCI companion bus that
// sysfs doesn't link to the EHCI port, so a mouse and a stick in the same hole there count as
// two ports (with a map: the mouse is taken for the next port asked for). The count is evidence
// only; the verdict doesn't depend on it.
//
//   - supported: at least one port worked, the tester didn't mark any as not working and no
//     port's read failed (automatic: the machine saw each device enumerate and move data, or
//     enumerate for devices that can't show more);
//   - partial: ports worked, and the tester marked n that didn't work (observed), or a port's
//     read failed and nothing read fine on it after (automatic);
//   - failed: nothing showed up on any port and the tester marked at least one as not working
//     (observed): a device plugged in and no bus saw it;
//   - not_tested (no-equipment): skipped. Done stays greyed until a port is seen or one is
//     marked, so "no device plugged" never reads as a dead port.

func init() {
	Register(Spec{ID: "ports.usb-a", Group: Plug, Order: 40, Title: i18n.M("USB-A ports"),
		Brief: i18n.M("plug something into each USB-A port"), Gear: i18n.M("a USB stick or any USB device"),
		Expect: time.Minute, New: func(*Env) Run { return &usbPorts{id: "ports.usb-a"} }, Demo: demoUSBA})
	Register(Spec{ID: "ports.usb-c", Group: Plug, Order: 50, Title: i18n.M("USB-C ports"),
		Brief: i18n.M("plug something into each USB-C port"), Gear: i18n.M("a USB-C device or adapter"),
		Expect: time.Minute, New: func(*Env) Run { return &usbPorts{id: "ports.usb-c"} }, Demo: demoUSBC})
}

// usbPort is one physical port seen during the test.
type usbPort struct {
	Key    string      // "3-2 / 4-2": its hub ports
	Devs   []usbDev    // what showed up on it, in order
	Checks []*devCheck // each one's check (nil: none, the phone in before the test, a port not counted)
	Speed  string      // the fastest speed seen on it
	Boot   bool        // the OmaBoot? stick's port, in since power-on
	Phone  bool        // the tethering phone's port, in since before the test
	Other  bool        // a port of the other kind (USB-A in the USB-C test, or the other way round): not counted
	Main   bool        // in the USB-C test: on the Mac's main USB controller, not the Thunderbolt 3 chip's
	In     string      // the device path on it now, "" when empty
	At     float64
	Drawn  string // the port map's port it was matched to (left-2), "" before
	// Wired is the connector DoesItOmarchy.com's wiring puts it on ("" unknown): the which-port
	// question starts on it, and the answer confirms or corrects it.
	Wired string
	Asked bool // the tester answered the port check for it
	Awake bool // seen on a Thunderbolt 3 controller the test kept awake (the fix)
}

type usbPorts struct {
	id      string
	start   map[string]usbDev // the devices there when the screen opened
	prev    map[string]usbDev // at the last poll
	ports   []*usbPort
	dead    int // ports the tester marked as not working
	polled  float64
	wake    []string // USB buses that appeared during the test (a Thunderbolt 3 controller waking)
	buses   map[string]bool
	hasA    bool // this Mac has USB-A ports too (for the USB-C test)
	marked  bool // the last thing that happened was the tester marking a port
	sayAt   float64
	gd      *guide  // the port map's guided order, nil without a map (Done ends the test then)
	asking  bool    // "Which port is the OmaBoot? drive in?" is up
	phone   *Phone  // the tethering phone, nil for none (or none on USB when the test began)
	phoneID string  // its idVendor:idProduct
	askPh   bool    // "Which port is your iPhone in?" is up
	askAt   float64 // when that question went up
	asked   bool    // the tester said which port the phone is in
	placed  bool    // the last thing that happened was the tester saying so
	stickQ  bool    // the stick's question went up (a step of its own)
	phoneQ  bool    // the phone's did
	ram     bool    // booted with copytoram: the stick is free to move from port to port
	stickID string  // the OmaBoot? stick's idVendor:idProduct
	// the Thunderbolt 3 chips' USB controllers (USB-C test): tb by PCI address; awake their
	// power/control before the test kept them awake (nil: it hasn't), awakeAt when it did
	tb      []string
	awake   map[string]string
	awakeAt float64
}

// tbWakeAfter is how long the USB-C test waits with nothing happening before it keeps a
// sleeping Thunderbolt 3 USB controller awake.
const tbWakeAfter = 15.0

// kinds are the port map's kinds this test asks for.
func (u *usbPorts) kinds() []string {
	if u.usbC() {
		return []string{"usb-c", "thunderbolt3"}
	}
	return []string{"usb-a"}
}

// usbNoteW is the widest note a USB port can show on the map; phone is the tethering phone's
// note ("" for none).
func usbNoteW(phone string) int {
	w := max(width(i18n.T("OmaBoot? drive")), width(i18n.T("× dead")), width(phone), readNoteW())
	for _, sp := range []string{"1.5", "12", "480", "5000", "10000", "20000"} {
		w = max(w, width(seenNote(usbRate(sp))))
	}
	for _, n := range busyNotes() { // the ringed port's, with its arrow
		w = max(w, width(n)+2)
	}
	return w
}

// busyNotes are the ringed port's notes while its check is under way.
func busyNotes() map[string]string {
	return map[string]string{"disk": i18n.T("reading…"), "input": i18n.T("move it…"), "gone": i18n.T("put it back"), "": i18n.T("a moment…")}
}

// seenNote is a port's note once something worked on it: "✓ 5 Gb/s".
func seenNote(what string) string { return i18n.T("✓ %s", what) }

func (u *usbPorts) usbC() bool { return u.id == "ports.usb-c" }

func (u *usbPorts) Start(c *Ctx) {
	c.Select(-1) // a question, or a port to wait for: a stray Enter answers nothing
	u.start = readUSB(c.IO)
	u.prev = u.start
	u.polled = -1 // the first step polls: the stick's read starts at once
	u.ram = copyToRAM(c.IO)
	u.buses = map[string]bool{}
	for _, b := range c.IO.Glob(usbDevices + "usb*") {
		u.buses[b] = true
	}
	u.hasA = c.Env != nil && c.Env.Checks != nil && checks.Applies("ports.usb-a", c.Env.Checks) || hasKind(c.Env.Layout(), "usb-a")
	if u.usbC() {
		u.tb = tbControllers(c.IO)
	}
	if b := bootStickUSB(c.IO); b != "" {
		if d, ok := u.start[b]; ok {
			u.stickID = d.ID
			p := &usbPort{Key: usbPortKey(c.IO, b), Devs: []usbDev{d}, Speed: d.Speed, Boot: true, In: b, Other: u.otherKind(c, b, false)}
			var ch *devCheck
			if !p.Other { // its own read, against the image, while the question is up
				ch = &devCheck{At: b, Dev: d, Kind: "disk", Disk: bootDisk(c.IO), Stick: true}
			}
			p.Checks = []*devCheck{ch}
			u.ports = append(u.ports, p)
		}
	}
	if ph := c.Env.phone(); ph != nil && ph.USB != bootStickUSB(c.IO) {
		if d, ok := u.start[ph.USB]; ok && u.port(usbPortKey(c.IO, ph.USB)) == nil {
			u.phone, u.phoneID = ph, d.ID
			u.ports = append(u.ports, &usbPort{Key: usbPortKey(c.IO, ph.USB), Devs: []usbDev{d}, Checks: []*devCheck{nil}, Speed: d.Speed,
				Phone: true, In: ph.USB, Other: u.otherKind(c, ph.USB, false)})
		}
	}
	note := ""
	if u.phone != nil {
		note = u.phoneNote()
	}
	u.gd = newGuide(c.Env, usbNoteW(note), u.kinds()...)
	if u.usbC() {
		u.gd.callUSBC()
	}
	if u.gd == nil {
		return
	}
	if b := u.bootPort(); b != nil {
		if len(u.gd.ports) == 1 { // nothing to ask: the stick is in the only one
			u.drawn(c, b, u.gd.ports[0].ID)
		} else {
			u.asking, u.stickQ = true, true
			u.preselect(c, b)
			return
		}
	}
	u.placePhone(c)
}

// phone is the tethering phone, or nil (also without an Env).
func (e *Env) phone() *Phone {
	if e == nil {
		return nil
	}
	return e.Phone
}

// placePhone asks which port the phone is in, after the stick's question: with one port left
// it's that one, with none it stays off the map.
func (u *usbPorts) placePhone(c *Ctx) {
	ph := u.phonePort()
	if ph == nil {
		return
	}
	switch left := u.gd.pending(); len(left) {
	case 0:
	case 1:
		u.drawn(c, ph, left[0].ID)
	default:
		u.askPh, u.askAt, u.phoneQ = true, max(c.T, 0.2), true
		u.preselect(c, ph)
	}
}

// preselect puts the which-port question's cursor (and the map's ring) on the port the wiring
// says p is in, when it knows: Enter confirms it.
func (u *usbPorts) preselect(c *Ctx, p *usbPort) {
	p.Wired = u.gd.wiredTo(c.Env, usbStables(c.IO, p.Key))
	if k := u.gd.index(p.Wired); k >= 0 {
		c.Select(k)
	}
}

// phonePort is the tethering phone's port when it's one of this test's kind, or nil.
func (u *usbPorts) phonePort() *usbPort {
	for _, p := range u.ports {
		if p.Phone && !p.Other {
			return p
		}
	}
	return nil
}

// question reports whether a which-port question is up (the stick's or the phone's).
func (u *usbPorts) question() bool { return u.asking || u.askPh }

// phoneName is the phone as the screen names it: its product string ("iPhone", "Pixel 7"), or
// "phone".
func (u *usbPorts) phoneName() string {
	if u.phone.Name != "" {
		return u.phone.Name
	}
	return i18n.T("phone")
}

// phoneNote is the phone's port's note on the map: its name, cut short for a long one.
func (u *usbPorts) phoneNote() string {
	n := []rune(u.phoneName())
	if len(n) > 14 {
		return string(n[:13]) + "…"
	}
	return string(n)
}

// phoneWord is the phone in Boot's lines: "iPhone" for an iPhone, else "phone".
func (u *usbPorts) phoneWord() string {
	if u.phone.Apple && u.phone.Name != "" {
		return u.phone.Name
	}
	return i18n.T("phone")
}

// phoneLeave is the hint once the phone's port is placed: moving it is fine (Android turns USB
// tethering off when it's unplugged).
func (u *usbPorts) phoneLeave() string {
	if u.phone.Apple {
		return i18n.T("Your %s's port counts already. Leave it there, or move it to test another one.", u.phoneName())
	}
	return i18n.T("Your %s's port counts already. Moving it? Turn USB tethering back on after.", u.phoneName())
}

// bootPort is the OmaBoot? stick's port when it's one of this test's kind, or nil.
func (u *usbPorts) bootPort() *usbPort {
	for _, p := range u.ports {
		if p.Boot && !p.Other {
			return p
		}
	}
	return nil
}

// drawn matches the stick's or the phone's port to the map's port id (the tester said which).
func (u *usbPorts) drawn(c *Ctx, p *usbPort, id string) {
	p.Drawn = id
	if p.Phone {
		u.gd.set(id, PortBoot, u.phoneNote(), c.T)
	} else {
		u.gd.set(id, PortBoot, i18n.T("OmaBoot? drive"), c.T)
	}
	u.mark(c, p) // its read may be through already
}

// match gives the ports seen since the last step, in the order they showed up, the ports asked
// for, one at a time: a port matched waits for its check before the next one is asked for. A
// port already matched keeps its state and note up to date with its checks.
func (u *usbPorts) match(c *Ctx) {
	for _, p := range u.ports {
		switch {
		case p.Other:
		case p.Drawn != "":
			u.mark(c, p)
		case p.Boot || p.Phone || u.question():
		default:
			if b := u.busy(); b != nil {
				if !b.settleGone() { // its check is under way: this one waits
					continue
				}
				u.mark(c, b) // its disk came out mid-read and the person moved on
			}
			if nx := u.gd.next(); nx != nil {
				p.Drawn = nx.ID
				u.mark(c, p)
			}
		}
	}
}

// busy is the port matched to the ring whose check isn't through (it has no state yet), or nil.
func (u *usbPorts) busy() *usbPort {
	if u.gd == nil {
		return nil
	}
	for _, p := range u.ports {
		if _, ok := u.gd.state[p.Drawn]; p.Drawn != "" && !p.Other && !ok {
			return p
		}
	}
	return nil
}

// mark gives a matched port its state on the map from its checks: worked (green), its read
// failed (amber); under way, no state yet (the ring stays on it), or the state it had.
func (u *usbPorts) mark(c *Ctx, p *usbPort) {
	id := p.Drawn
	st, had := u.gd.state[id]
	note := u.gd.note[id]
	switch p.proof() {
	case proofData, proofEnum:
		switch {
		case p.Phone:
			st, note = PortBoot, u.phoneNote()
		case p.Boot:
			st, note = PortBoot, i18n.T("OmaBoot? drive")
		default:
			st, note = PortSeen, seenNote(u.rate(p))
		}
	case proofFailed:
		st, note = PortPart, i18n.T("! read failed")
	default:
		if !had {
			return
		}
	}
	if st == PortDead { // the tester's word stands
		return
	}
	if !had || st != u.gd.state[id] {
		u.gd.set(id, st, note, c.T)
		return
	}
	u.gd.note[id] = note
}

// rate is a working port's note: the speed it read at, else the speed it negotiated.
func (u *usbPorts) rate(p *usbPort) string {
	if ch := p.good(); ch != nil && ch.Read != nil {
		return mbps(ch.Read)
	}
	return usbRate(p.Speed)
}

// otherKind reports whether a port is of the kind this test doesn't count; plugged: something
// was plugged into it during the test (not the stick or the phone, in since before it).
func (u *usbPorts) otherKind(c *Ctx, dev string, plugged bool) bool {
	tb := onThunderbolt3(c.IO, dev)
	switch {
	case !u.usbC():
		return tb
	case tb || !u.hasA:
		return false
	}
	return !plugged || u.knownA(c, dev)
}

// knownA reports whether a hub port is a USB-A port this run's USB-A test counted, or one
// DoesItOmarchy.com's wiring puts on a USB-A connector.
func (u *usbPorts) knownA(c *Ctx, dev string) bool {
	key := usbPortKey(c.IO, dev)
	if c.Env != nil {
		for _, k := range strings.Split(key, " / ") {
			if c.Env.USBA[k] {
				return true
			}
		}
	}
	return wiredKind(c.Env, usbStables(c.IO, key)) == "usb-a"
}

// hasKind reports whether a port map has a port of kind k.
func hasKind(sides []identify.PortSide, k string) bool {
	for _, s := range sides {
		for _, p := range s.Ports {
			if p.Kind == k {
				return true
			}
		}
	}
	return false
}

// wiredKind is the kind of the connector DoesItOmarchy.com's wiring puts any of paths on, or "".
func wiredKind(e *Env, paths []string) string {
	wiring := e.wiring()
	for _, s := range e.Layout() {
		for _, p := range s.Ports {
			for _, w := range wiring[p.ID] {
				if slices.Contains(paths, w) {
					return p.Kind
				}
			}
		}
	}
	return ""
}

func (u *usbPorts) port(key string) *usbPort {
	for _, p := range u.ports {
		if p.Key == key {
			return p
		}
	}
	return nil
}

func (u *usbPorts) Step(c *Ctx) {
	if c.T-u.polled < 0.5 {
		return
	}
	u.polled = c.T
	now := readUSB(c.IO)
	for _, b := range c.IO.Glob(usbDevices + "usb*") {
		if !u.buses[b] {
			u.buses[b] = true
			u.wake = append(u.wake, strings.TrimPrefix(b, usbDevices))
		}
	}
	for _, p := range u.ports { // unplugged
		if _, ok := now[p.In]; p.In != "" && !ok {
			p.In = ""
		}
	}
	var paths []string
	for p := range now {
		if _, was := u.prev[p]; !was {
			paths = append(paths, p)
		}
	}
	sort.Strings(paths) // a hub before what's behind it
	for _, p := range paths {
		at := usbPortOf(p, u.start)
		d, ok := now[at]
		if !ok || usbInternal(at, d, u.start, u.phoneID, u.stickID) {
			continue
		}
		key := usbPortKey(c.IO, at)
		pt := u.port(key)
		if pt == nil {
			pt = &usbPort{Key: key, Other: u.otherKind(c, at, true)}
			pt.Main = u.usbC() && !pt.Other && !onThunderbolt3(c.IO, at)
			u.ports = append(u.ports, pt)
		}
		if u.awake != nil && !pt.Other && onThunderbolt3(c.IO, at) {
			pt.Awake = true
		}
		if pt.In == at && at != p { // something behind a hub already counted
			continue
		}
		pt.In, pt.At = at, c.T
		pt.Devs = append(pt.Devs, d)
		var ch *devCheck
		if !pt.Other {
			ch = newCheck(at, d, c.T)
		}
		pt.Checks = append(pt.Checks, ch)
		if fasterUSB(d.Speed, pt.Speed) || pt.Speed == "" {
			pt.Speed = d.Speed
		}
		if !pt.Phone { // the phone back in its own port: nothing new to say
			u.sayAt, u.marked, u.placed = c.T, false, false
		}
	}
	disks := usbDisks(c.IO)
	for _, p := range u.ports {
		for _, ch := range p.Checks {
			if ch != nil && ch.advance(c, now, disks) && !ch.Stick && !p.Phone {
				u.sayAt, u.marked, u.placed = c.T, false, false
			}
		}
	}
	u.prev = now
	if u.usbC() && u.awake == nil && c.T-u.sayAt >= tbWakeAfter && !u.question() && u.busy() == nil &&
		(u.gd == nil || !u.gd.done()) && u.tbAsleep(c.IO) {
		u.keepAwake(c)
	}
	if u.gd != nil {
		u.match(c)
		// every port has its state (the green shows a moment first); a failed read waits for a
		// retry on its port or Done
		if u.gd.done() && !u.question() && !u.pending() && u.failed() == 0 && c.T-u.gd.at >= 1.5 {
			u.decide(c)
		}
	}
}

// counted are the ports of this test's kind seen so far.
func (u *usbPorts) counted() []*usbPort {
	var out []*usbPort
	for _, p := range u.ports {
		if !p.Other {
			out = append(out, p)
		}
	}
	return out
}

// mine are the counted ports the result rests on: with a map, those matched to it.
func (u *usbPorts) mine() []*usbPort {
	var out []*usbPort
	for _, p := range u.counted() {
		if u.gd == nil || p.Drawn != "" {
			out = append(out, p)
		}
	}
	return out
}

// pending reports whether a port's check is under way; stick: the OmaBoot? stick's own read
// counts too.
func (u *usbPorts) pending(stick ...bool) bool {
	for _, p := range u.mine() {
		if ch := p.latest(); ch != nil && ch.State == proofWait && (len(stick) == 0 || stick[0] || !ch.Stick) {
			return true
		}
	}
	return false
}

// reading reports whether a disk is being read.
func (u *usbPorts) reading() bool {
	for _, p := range u.counted() {
		if ch := p.latest(); ch != nil && ch.reading() {
			return true
		}
	}
	return false
}

// failed is how many ports' reads failed with nothing read fine on them after.
func (u *usbPorts) failed() int {
	n := 0
	for _, p := range u.mine() {
		if p.proof() == proofFailed {
			n++
		}
	}
	return n
}

// usbVerdict is the rule: ports that worked, ports whose read failed (unresolved), ports marked
// not working → status and method, or "" while there is nothing to decide on. A failed read
// never lets the test pass.
func usbVerdict(seen, failed, dead int) (status, method string) {
	switch {
	case seen > 0 && failed == 0 && dead == 0:
		return checks.Supported, Automatic
	case seen+failed > 0 && dead > 0:
		return checks.Partial, Observed
	case seen+failed > 0:
		return checks.Partial, Automatic
	case dead > 0:
		return checks.Failed, Observed
	}
	return "", ""
}

func (u *usbPorts) evidence(c *Ctx) string {
	var b strings.Builder
	seen := u.counted()
	fmt.Fprintf(&b, "ports seen: %d", len(seen))
	for _, p := range seen {
		b.WriteString("; " + p.Key + ": ")
		var ds []string
		for i, d := range p.Devs {
			s := fmt.Sprintf("%s (%s) at %s", d.Name, d.ID, usbSpeedEn(d.Speed))
			if i < len(p.Checks) && p.Checks[i] != nil {
				s += ": " + p.Checks[i].evidence()
			}
			ds = append(ds, s)
		}
		b.WriteString(strings.Join(ds, ", then "))
		if p.Boot {
			b.WriteString(" (the OmaBoot? drive, in since power-on)")
		}
		if p.Phone {
			b.WriteString(" (" + u.phoneEn() + ", in since before the test)")
		}
		if p.Main {
			b.WriteString(" (on the Mac's main USB controller, not the Thunderbolt 3 chip's)")
		}
		if p.Awake {
			b.WriteString(" (seen once the Thunderbolt 3 USB controller was kept awake)")
		}
		if ch := p.failedCheck(); ch != nil {
			b.WriteString(u.pointsAt(p, ch))
		}
		for _, f := range p.findings() {
			b.WriteString("; " + f)
		}
	}
	for _, p := range u.ports {
		if p.Other {
			kind := "a USB-C (Thunderbolt 3) port"
			if u.usbC() {
				kind = "a USB-A port"
			}
			fmt.Fprintf(&b, "; not counted: %s, %s", p.Key, kind)
		}
	}
	if len(u.wake) > 0 {
		fmt.Fprintf(&b, "; USB buses that appeared during the test: %s", strings.Join(u.wake, ", "))
	}
	if u.awake != nil {
		fmt.Fprintf(&b, "; the Thunderbolt 3 chip's USB controller (%s) was asleep (runtime-suspended) with nothing seen for %.0f s: "+
			"Linux doesn't take the PCIe wake signal (PME) on a Mac, so a plug-in can't wake it; kept awake from %.0f s into the test "+
			"(power/control on, OmaBoot?'s Thunderbolt 3 USB wake fix), put back after", u.tbNames(c.IO), tbWakeAfter, u.awakeAt)
	}
	if n := u.failed(); n > 0 {
		fmt.Fprintf(&b, "; %d port(s) whose read failed, nothing read fine there after", n)
	}
	if u.ram {
		b.WriteString("; booted with copytoram: the OmaBoot? drive was free to move between ports")
	}
	if u.dead > 0 {
		fmt.Fprintf(&b, "; the tester reports %d port(s) that didn't work", u.dead)
	} else {
		b.WriteString("; the tester tried every port")
	}
	if u.gd != nil {
		for _, p := range u.ports {
			if p.Drawn == "" {
				continue
			}
			m := p.Key
			switch {
			case p.Boot && !p.Asked:
				m += " (the OmaBoot? drive: the only port of its kind)"
			case p.Boot:
				m += " (the OmaBoot? drive: " + said(p) + ")"
			case p.Phone && u.asked:
				m += " (" + u.phoneEn() + ": " + said(p) + ")"
			case p.Phone:
				m += " (" + u.phoneEn() + ": the only port left)"
			}
			if fw := usbFirmwarePlace(c.IO, strings.Fields(p.Key)[0]); fw != "" {
				m += ", firmware says " + fw
			} else {
				m += ", no location from the firmware"
			}
			u.gd.match[p.Drawn] = m
		}
		b.WriteString("; " + u.gd.evidence())
	}
	return b.String()
}

// PortAnswers are the USB ports' answers for DoesItOmarchy.com's wiring (porter): each port matched
// to the map, by its stable paths; the stick's and the phone's as the tester answered the port
// check.
func (u *usbPorts) PortAnswers(c *Ctx) []checks.PortAnswer {
	if u.gd == nil {
		return nil
	}
	for _, p := range u.ports {
		if p.Drawn == "" || p.Other {
			continue
		}
		how, said := "ring", "" // matched by the run itself: the ring, or the only port left
		switch {
		case !p.Asked:
		case p.Wired == "":
			how = "asked"
		case p.Wired == p.Drawn:
			how = "confirmed"
		default:
			how, said = "corrected", p.Wired
		}
		u.gd.catch(p.Drawn, how, said, usbStables(c.IO, p.Key)...)
	}
	return u.gd.answers()
}

// said is how a which-port answer came about, for the evidence.
func said(p *usbPort) string {
	switch {
	case p.Wired == "":
		return "the tester said which port"
	case p.Wired == p.Drawn:
		return "the tester confirmed DoesItOmarchy.com's wiring"
	}
	return "the tester corrected DoesItOmarchy.com's wiring, which said " + p.Wired
}

// pointsAt says, for a port whose read failed, whether the same device (by its ID and serial)
// read fine on another port after: then the failure points at the port, not the device.
func (u *usbPorts) pointsAt(p *usbPort, bad *devCheck) string {
	for _, q := range u.counted() {
		if q == p {
			continue
		}
		for _, ch := range q.Checks {
			if ch != nil && ch.State == proofData && ch.Read != nil && ch.Since > bad.Since &&
				ch.Dev.ID == bad.Dev.ID && ch.Dev.Serial == bad.Dev.Serial {
				return fmt.Sprintf("; the same device (%s, by its ID and serial) then read fine on %s, so the failure points at this port", bad.Dev.Name, q.Key)
			}
		}
	}
	return ""
}

// phoneEn is the phone in the report: "the phone carrying the network".
func (u *usbPorts) phoneEn() string {
	if u.phone.Online {
		return "the phone carrying the network"
	}
	return "a phone on USB tethering"
}

// decide hands over the verdict, once there's something to decide on.
func (u *usbPorts) decide(c *Ctx) {
	if !u.usbC() && c.Env != nil { // the USB-A ports, for the USB-C test after it (knownA)
		if c.Env.USBA == nil {
			c.Env.USBA = map[string]bool{}
		}
		for _, p := range u.counted() {
			for _, k := range strings.Split(p.Key, " / ") {
				c.Env.USBA[k] = true
			}
		}
	}
	seen, failed := 0, 0
	for _, p := range u.mine() {
		p.settleGone() // with Done: a disk out mid-read counts as it enumerated
		if p.proof() == proofFailed {
			failed++
		} else {
			seen++
		}
	}
	if u.gd != nil {
		seen, failed = u.gd.count(PortSeen, PortBoot), u.gd.count(PortPart)
	}
	st, m := usbVerdict(seen, failed, u.dead)
	if st == "" {
		return
	}
	for _, p := range u.ports {
		for _, ch := range p.Checks {
			if ch != nil {
				ch.close()
			}
		}
	}
	u.putBack(c.IO)
	fixed := u.fixed()
	if st == checks.Supported && fixed {
		st = checks.Partial
	}
	r := Result(u.id, st, m, u.evidence(c), "")
	var say string
	switch {
	case fixed && st == checks.Partial && u.dead == 0 && failed == 0:
		r.Detail = i18n.T("works with the Thunderbolt 3 USB controller kept awake")
		say = i18n.T("they work, once I kept my Thunderbolt 3 USB controller awake. partial.")
		if c.Env != nil && c.Env.Checks != nil && c.Env.Checks.Cat != nil {
			if fix := c.Env.Checks.Cat.Fix(checks.FixTB3USBAwake); fix != nil {
				r.FixID, r.Fix = fix.ID, strings.TrimSuffix(fix.Name, " fix")
			}
		}
	case st == checks.Supported:
		r.Detail = i18n.N("%d port seen, all worked", "%d ports seen, all worked", seen, seen)
		say = i18n.N("%d port, and it answered.", "%d ports, all answered.", seen, seen)
	case st == checks.Partial && u.dead == 0:
		r.Detail = i18n.T("%[1]d worked, %[2]d failed a read", seen, failed)
		say = i18n.N("%d port couldn't move data. partial.", "%d ports couldn't move data. partial.", failed, failed)
	case st == checks.Partial:
		r.Detail = i18n.T("%[1]d seen, %[2]d didn't work", seen+failed, u.dead)
		say = i18n.N("%d port stayed silent. partial.", "%d ports stayed silent. partial.", u.dead, u.dead)
	default:
		r.Detail = i18n.T("no port saw a device")
		say = i18n.T("plugged in, and no bus saw a thing. that's a fail.")
	}
	c.Decide(Outcome{Results: []checks.Result{r}, Say: say})
}

func (u *usbPorts) Act(c *Ctx, i int) {
	switch {
	case u.asking: // which port the stick is in
		if b := u.bootPort(); b != nil && i < len(u.gd.ports) {
			u.drawn(c, b, u.gd.ports[i].ID)
			b.Asked = true
			u.asking, u.sayAt = false, c.T
			c.Select(-1) // the next question, or the port in the ring: nothing picked yet
			u.placePhone(c)
		}
	case u.askPh: // which port the phone is in: one of those left
		if ph, left := u.phonePort(), u.gd.pending(); ph != nil && i < len(left) {
			u.drawn(c, ph, left[i].ID)
			ph.Asked = true
			u.askPh, u.asked = false, true
			u.sayAt, u.placed = c.T, true
			c.Select(-1) // the next question, or the port in the ring: nothing picked yet
		}
	case u.gd != nil && i == 1, u.gd == nil && i == 2: // Read it again
		if p := u.rereadable(); p != nil {
			u.reread(c, p)
		}
	case u.gd != nil && u.gd.done(): // Done, with a failed read standing
		if i == 0 && u.failed() > 0 && !u.pending() {
			u.decide(c)
		}
	case u.gd != nil: // A port didn't work: the one asked for (not while its check is under way)
		b := u.busy()
		if nx := u.gd.next(); i == 0 && nx != nil && (b == nil || b.proof() == proofWait && b.latest().State == proofGone) {
			u.gd.set(nx.ID, PortDead, i18n.T("× dead"), c.T)
			u.dead++
			u.sayAt, u.marked = c.T, true
		}
	case i == 0:
		if !u.reading() {
			u.decide(c)
		}
	case i == 1:
		u.dead++
		u.sayAt, u.marked = c.T, true
	}
}

// rereadable is the port whose read failed with its disk still in, the newest first: Read it
// again reads it once more where it is (plugging it back in did, unsaid).
// nil when there's none.
func (u *usbPorts) rereadable() *usbPort {
	var found *usbPort
	for _, p := range u.mine() {
		ch := p.latest()
		if ch != nil && ch.State == proofFailed && ch.Kind == "disk" && p.In == ch.At && (found == nil || p.At >= found.At) {
			found = p
		}
	}
	return found
}

// reread starts a new check of the failed read's disk on its port: its result replaces the
// failed one's, as a replug's would.
func (u *usbPorts) reread(c *Ctx, p *usbPort) {
	old := p.latest()
	ch := newCheck(old.At, old.Dev, c.T)
	ch.Kind, ch.Disk, ch.Stick = old.Kind, old.Disk, old.Stick
	p.Checks, p.At = append(p.Checks, ch), c.T
	u.sayAt, u.marked, u.placed = c.T, false, false
}

func (u *usbPorts) Stop(c *Ctx) {
	u.putBack(c.IO)
	for _, p := range u.ports {
		for _, ch := range p.Checks {
			if ch != nil {
				ch.close()
			}
		}
	}
}

// lastCheck is the newest check on the port something showed up on last, or nil.
func (u *usbPorts) lastCheck() *devCheck {
	if l := u.last(); l != nil && !l.Other {
		return l.latest()
	}
	return nil
}

// sayCheck is Boot's line about a check; end is the sentence after a good one ("next one.");
// ok false when he has nothing to say about it.
func (u *usbPorts) sayCheck(ch *devCheck, end string) (string, boot.Expr, bool) {
	name := ch.name()
	stick := ch.Stick || u.stickID != "" && ch.Dev.ID == u.stickID
	switch ch.State {
	case proofWait:
		switch {
		case ch.Kind == "disk" && stick:
			return i18n.T("my own drive. checking it against the image."), boot.Hmm, true
		case ch.Kind == "disk":
			return i18n.T("%s's in. reading it: leave it in a moment.", name), boot.Hmm, true
		case ch.Kind == "input":
			return i18n.T("%s's in. move it, or press a key on it.", name), boot.Hmm, true
		}
		return i18n.T("%s's in. a moment.", name), boot.Hmm, true
	case proofGone:
		return i18n.T("it came out before the read was through."), boot.Concerned, true
	case proofFailed:
		return i18n.T("that read failed. Read it again, or plug it back in: this port stays partial unless one reads fine."), boot.Concerned, true
	case proofData:
		first := i18n.T("%s answered.", name)
		switch {
		case ch.Read != nil && ch.Read.Way == WayImage:
			first = i18n.T("the drive reads true, %s.", mbps(ch.Read))
		case ch.Read != nil:
			first = i18n.T("%[1]s read fine, %[2]s.", name, mbps(ch.Read))
		}
		return first + " " + end, boot.Pleased, true
	}
	return "", "", false
}

func (u *usbPorts) View(c *Ctx) Screen {
	if u.gd != nil {
		return u.guided(c)
	}
	s := Screen{Face: boot.Idle, SayAt: u.sayAt, OutTitle: i18n.T("What I see"), SkipDoes: i18n.T("goes in as not tested")}
	seen := u.counted()
	switch {
	case u.ram && u.usbC():
		s.Steps = []string{i18n.T("Take the OmaBoot? drive out and put it in a USB-C port."),
			i18n.T("Leave it in while it's read, then move it to the next port.")}
	case u.ram:
		s.Steps = []string{i18n.T("Take the OmaBoot? drive out and put it in a USB-A port."),
			i18n.T("Leave it in while it's read, then move it to the next port.")}
	case u.usbC():
		s.Steps = []string{i18n.T("Plug a USB-C stick, adapter or phone into a USB-C port."),
			i18n.T("Leave a stick in while it's read; move a mouse or press a key.")}
	default:
		s.Steps = []string{i18n.T("Plug a stick, mouse or keyboard into a USB-A port."),
			i18n.T("Leave a stick in while it's read; move a mouse or press a key.")}
	}
	s.Steps = append(s.Steps, i18n.T("Pick Done once every port has had its turn."))
	last := u.last()
	switch {
	case u.pending() && last != nil:
		s.Step = 1
	case last != nil || u.dead > 0:
		s.Step = 2
	}
	s.Happens = i18n.T("Leave the OmaBoot? drive where it is: its port counts already.")
	if u.ram {
		s.Happens = i18n.T("The system runs from memory: the drive is free to move.")
	}
	if u.phonePort() != nil {
		s.Happens += " " + u.phoneLeave()
	}
	s.Hint = i18n.T("nothing showing? push it in all the way, or try another device.")
	if u.failed() > 0 {
		s.Hint = i18n.T("a worn stick fails reads too: try another one in that port.")
	}
	s.Waiting = i18n.T("·  waiting for a device")
	kind := "USB-A"
	if u.usbC() {
		kind = "USB-C"
	}
	s.Anim = plugArt(kind, len(seen)) // no port map: a plug going in, a box per port seen
	switch {
	case u.wokeNow():
		s.Say, s.Face = u.wokeSay(), boot.Hmm
	case u.marked:
		s.Say, s.Face = i18n.N("noted: %d port didn't work. try the next.", "noted: %d ports didn't work. try the next.", u.dead, u.dead), boot.Concerned
	case last != nil && !last.Other && last.latest() != nil && last.latest().State != proofEnum:
		s.Say, s.Face, _ = u.sayCheck(last.latest(), i18n.T("next port, or Done."))
	case last != nil && !last.Other:
		d := last.Devs[len(last.Devs)-1]
		s.Say, s.Face = i18n.T("%[1]s on %[2]s, %[3]s. next port, or Done.", d.Name, last.Key, usbSpeed(d.Speed)), boot.Pleased
	case last != nil:
		s.Say = i18n.T("that's a USB-A port. this one counts USB-C.")
		if !u.usbC() {
			s.Say = i18n.T("that's a USB-C port. this one counts USB-A.")
		}
		s.Face = boot.Hmm
	case u.ram:
		s.Say, s.SayAt = i18n.T("the system's in memory now: my drive is free to move."), 0.2
	default:
		s.Say, s.SayAt = i18n.T("plug something in. I'm watching the USB buses."), 0.2
	}
	var busy []Line
	for _, p := range u.ports {
		d := p.Devs[len(p.Devs)-1]
		ch := p.latest()
		switch {
		case p.Other && u.usbC():
			s.Live = append(s.Live, Gone(i18n.T("%[1]s: a USB-A port, not counted here", p.Key)))
		case p.Other:
			s.Live = append(s.Live, Gone(i18n.T("%[1]s: a USB-C port, not counted here", p.Key)))
		case p.proof() == proofFailed:
			s.Live = append(s.Live, Bad(p.Key+" · "+u.portText(p)))
		case p.Boot, p.Phone, ch != nil && ch.State == proofData:
			s.Live = append(s.Live, Fact(p.Key+" · "+u.portText(p)))
		case p.Speed != d.Speed:
			s.Live = append(s.Live, Fact(i18n.T("%[1]s · %[2]s · %[3]s · best %[4]s", p.Key, d.Name, usbSpeed(d.Speed), usbSpeed(p.Speed))))
		default:
			s.Live = append(s.Live, Fact(i18n.T("%[1]s · %[2]s · %[3]s", p.Key, d.Name, usbSpeed(d.Speed))))
		}
		if ch != nil {
			if l, ok := ch.progress(); ok {
				busy = append(busy, l)
			}
		}
	}
	if len(u.wake) > 0 {
		s.Live = append(s.Live, Fact(i18n.T("a USB controller woke up: %s", strings.Join(u.wake, ", "))))
	}
	if u.awake != nil {
		s.Live = append(s.Live, u.awakeLine())
	}
	if u.dead > 0 {
		s.Live = append(s.Live, Bad(i18n.N("%d port didn't work (your count)", "%d ports didn't work (your count)", u.dead, u.dead)))
	}
	switch {
	case len(busy) > 0:
		s.Live = append(s.Live, busy...)
	case len(seen) > 0:
		s.Live = append(s.Live, Wait(i18n.T("ports seen: %d. waiting for the next one", len(seen))))
	default:
		s.Live = append(s.Live, Wait(i18n.T("waiting for a device")))
	}
	s.Actions = []Action{{Label: i18n.T("Done"), Does: i18n.T("I've tried every port"), Off: len(seen) == 0 && u.dead == 0 || u.reading()},
		{Label: i18n.T("A port didn't work"), Does: i18n.T("nothing showed up here")}}
	if u.failed() > 0 {
		s.Actions = append(s.Actions, Action{Label: i18n.T("Read it again"), Does: i18n.T("the failed read, once more, with it left in"), Off: u.rereadable() == nil})
	}
	return s
}

// portText is What I see's text for a port, after its name: its latest device and what its
// check came to (the stick's and the phone's as such).
func (u *usbPorts) portText(p *usbPort) string {
	var ch *devCheck // the check the port's state rests on, else the newest
	for i := len(p.Checks) - 1; i >= 0 && ch == nil; i-- {
		if x := p.Checks[i]; x != nil && x.State != proofWait && x.State != proofGone {
			ch = x
		}
	}
	if ch == nil {
		ch = p.latest()
	}
	switch {
	case p.Phone && (ch == nil || ch.State == proofEnum) && u.phone.Online:
		return i18n.T("%[1]s · %[2]s · carrying the network", u.phoneName(), usbSpeed(p.Speed))
	case p.Phone && (ch == nil || ch.State == proofEnum):
		return i18n.T("%[1]s · %[2]s · in since before this test", u.phoneName(), usbSpeed(p.Speed))
	case ch == nil:
		d := p.Devs[len(p.Devs)-1]
		return i18n.T("%[1]s · %[2]s", d.Name, usbSpeed(d.Speed))
	case ch.State == proofEnum && !ch.Stick && p.Speed != ch.Dev.Speed:
		return i18n.T("%[1]s · %[2]s · best %[3]s", ch.Dev.Name, usbSpeed(ch.Dev.Speed), usbSpeed(p.Speed))
	}
	return ch.text(usbSpeed(ch.Dev.Speed))
}

// steps are the guided screen's "What to do": the stick's question and the phone's when they
// went up, then plugging in and the port's check, then leaving the stick;
// with copytoram, moving the stick itself from ring to ring.
func (u *usbPorts) steps() (steps []string, step int) {
	plug := 0 // the which-port questions are a box of their own, not steps
	switch {
	case u.ram:
		steps = append(steps, i18n.T("Take the OmaBoot? drive out and put it in the port in the violet ring."),
			i18n.T("Leave it in while it's read, then on to the next ring."))
	case u.usbC():
		steps = append(steps, i18n.T("Plug a USB-C stick, adapter or phone into the port in the violet ring."),
			i18n.T("Leave a stick in while it's read; move a mouse or press a key."),
			i18n.T("Leave the OmaBoot? drive where it is."))
	default:
		steps = append(steps, i18n.T("Plug a stick, mouse or keyboard into the port in the violet ring."),
			i18n.T("Leave a stick in while it's read; move a mouse or press a key."),
			i18n.T("Leave the OmaBoot? drive where it is."))
	}
	switch {
	case u.question():
		return steps, -1
	case u.busy() != nil || u.pending(false): // a check under way (a retry on a port done too)
		return steps, plug + 1
	case u.gd.done() && u.ram:
		return steps, len(steps)
	case u.gd.done():
		return steps, plug + 2
	}
	return steps, plug
}

// portQuestion is the which-port question's box: separate from the test, it says what the
// answer is for and, when DoesItOmarchy.com's wiring knows the port, which one it says.
func (u *usbPorts) portQuestion(text string, p *usbPort) *Question {
	q := &Question{Title: i18n.T("Port check"), Text: text,
		Note: i18n.T("Your answer tells DoesItOmarchy.com how this Mac's ports are wired, and will help future OmaBoot? releases.")}
	if w := u.gd.port(p.Wired); w != nil {
		q.Note = i18n.T("DoesItOmarchy.com's wiring says %[1]s. Enter confirms it; pick another if it's wrong.", u.gd.choice(*w))
	}
	return q
}

// guided is the screen with the port map: the stick's port asked first, then the tethering
// phone's, then one port at a time.
func (u *usbPorts) guided(c *Ctx) Screen {
	gd := u.gd
	s := Screen{Face: boot.Idle, SayAt: u.sayAt, OutTitle: i18n.T("What I see"), Map: gd.view(u.question()),
		SkipDoes: i18n.T("goes in as not tested"), SayRows: portSayRows}
	s.Steps, s.Step = u.steps()
	if u.phonePort() != nil {
		s.Map.Phone, s.Map.PickPhone = u.phoneNote(), u.askPh
	}
	busy := u.busy()
	if busy != nil && !u.question() { // the ringed port's note while its check is under way
		kind := ""
		if ch := busy.latest(); ch != nil {
			kind = ch.Kind
			if ch.State == proofGone {
				kind = "gone"
			}
		}
		s.Map.Note[busy.Drawn] = busyNotes()[kind]
	}
	kind := i18n.T("USB-A")
	if u.usbC() {
		kind = i18n.T("USB-C")
	}
	if u.asking {
		b := u.bootPort()
		s.Say, s.SayAt, s.Face = i18n.T("one question before the test."), 0.2, boot.Hmm
		s.Hint = i18n.T("look for the drive. ↑↓ moves the ring to the port you mean.")
		s.Live = []Line{{Mark: "?", Color: g.BVIO, Text: i18n.T("the OmaBoot? drive · %[1]s · since power-on: which port?", usbSpeed(b.Speed))}}
		s.Question = u.portQuestion(i18n.T("Confirm which port the OmaBoot? drive is in."), b)
		for k, p := range gd.pending() {
			s.Actions = append(s.Actions, Action{Label: gd.choice(p), Key: fmt.Sprint(k + 1)})
		}
		s.Waiting = i18n.T("·  waiting for your answer")
		return s
	}
	if u.askPh {
		ph := u.phonePort()
		s.Say, s.SayAt, s.Face = i18n.T("one more question first."), u.askAt, boot.Hmm
		s.Hint = i18n.T("look for the %s's cable. ↑↓ moves the ring to the port you mean.", u.phoneWord())
		s.Live = u.boundLines()
		q := Line{Mark: "?", Color: g.BVIO, Text: i18n.T("%[1]s · %[2]s · in before this test: which port?", u.phoneName(), usbSpeed(ph.Speed))}
		if u.phone.Online {
			q.Text = i18n.T("%[1]s · %[2]s · in before this test, carrying the network: which port?", u.phoneName(), usbSpeed(ph.Speed))
		}
		s.Live = append(s.Live, q)
		s.Question = u.portQuestion(i18n.T("Confirm which port your %s is in.", u.phoneName()), ph)
		for k, p := range gd.pending() {
			s.Actions = append(s.Actions, Action{Label: gd.choice(p), Key: fmt.Sprint(k + 1)})
		}
		s.Waiting = i18n.T("·  waiting for your answer")
		return s
	}
	// one row each: on a 120×40 console the left column has no more
	s.Happens = i18n.T("Each %s port has to move data.", kind)
	s.Hint = i18n.T("nothing showing? push it in all the way, or try another device.")
	switch {
	case u.phonePort() != nil:
		s.Happens = i18n.T("Your %s's port counts already.", u.phoneName())
		s.Hint = i18n.T("moving your phone? turn USB tethering back on after.")
		if u.phone.Apple {
			s.Hint = i18n.T("your %s can move to another port: its connection comes back by itself.", u.phoneWord())
		}
	case u.ram:
		s.Happens = i18n.T("The system runs from memory: the drive is free.")
	}
	if u.failed() > 0 {
		s.Hint = i18n.T("a worn stick fails reads too: try another one in that port.")
	}
	s.Waiting = i18n.T("·  waiting for the port in the ring")
	last, lc := u.last(), u.lastCheck()
	switch {
	case u.wokeNow():
		s.Say, s.Face = u.wokeSay(), boot.Hmm
	case u.marked:
		s.Say, s.Face = i18n.T("noted: that port didn't work. try the next."), boot.Concerned
	case u.placed:
		s.Say, s.Face = i18n.T("that one counts. %d to go.", len(gd.pending())), boot.Pleased
	case gd.done() && u.failed() > 0 && !u.pending():
		s.Say, s.Face = i18n.N("%d port failed its read. Read it again, plug in there again, or pick Done.",
			"%d ports failed their reads. Read it again, plug in there again, or pick Done.", u.failed(), u.failed()), boot.Concerned
	case lc != nil && lc.State != proofEnum:
		end := i18n.T("next one.")
		if gd.done() {
			end = i18n.T("that's all of them.")
		}
		s.Say, s.Face, _ = u.sayCheck(lc, end)
	case last != nil && !last.Other:
		d := last.Devs[len(last.Devs)-1]
		s.Say, s.Face = i18n.T("%[1]s, %[2]s. next one.", d.Name, usbSpeed(d.Speed)), boot.Pleased
		if gd.done() {
			s.Say = i18n.T("%[1]s, %[2]s. that's all of them.", d.Name, usbSpeed(d.Speed))
		}
	case last != nil:
		s.Say = i18n.T("that's a USB-A port. this one counts USB-C.")
		if !u.usbC() {
			s.Say = i18n.T("that's a USB-C port. this one counts USB-A.")
		}
		s.Face = boot.Hmm
	case u.stickQ && u.ram && len(gd.pending()) == 1:
		s.Say, s.Face = i18n.T("that one counts already. now take me to the other one."), boot.Pleased
	case u.stickQ && u.ram && !gd.done():
		s.Say, s.Face = i18n.T("that one counts already. take me along: %d to go.", len(gd.pending())), boot.Pleased
	case u.stickQ && len(gd.pending()) == 1:
		s.Say, s.Face = i18n.T("that one counts already. now the other one."), boot.Pleased
	case u.stickQ && !gd.done():
		s.Say, s.Face = i18n.T("that one counts already. %d to go.", len(gd.pending())), boot.Pleased
	case u.ram:
		s.Say, s.SayAt = i18n.T("the system's in memory now: my drive is free to move."), 0.2
	default:
		s.Say, s.SayAt = i18n.T("plug something in. I'm watching the USB buses."), 0.2
	}
	s.Live = u.boundLines()
	switch { // before the ports of the other kind: on a small screen it's the line to keep
	case busy != nil && busy.latest() != nil:
		if l, ok := busy.latest().progress(); ok {
			s.Live = append(s.Live, l)
		}
	case !gd.done():
		s.Live = append(s.Live, gd.waiting())
	}
	for _, p := range u.ports {
		switch {
		case p.Other && u.usbC():
			s.Live = append(s.Live, Gone(i18n.T("%[1]s: a USB-A port, not counted here", p.Key)))
		case p.Other:
			s.Live = append(s.Live, Gone(i18n.T("%[1]s: a USB-C port, not counted here", p.Key)))
		case p.Drawn != "" && p != busy: // a check under way on a port with its state (the stick's own read, a retry)
			if ch := p.latest(); ch != nil && ch.State == proofWait {
				if l, ok := ch.progress(); ok {
					s.Live = append(s.Live, l)
				}
			}
		}
	}
	if len(u.wake) > 0 {
		s.Live = append(s.Live, Fact(i18n.T("a USB controller woke up: %s", strings.Join(u.wake, ", "))))
	}
	if u.awake != nil {
		s.Live = append(s.Live, u.awakeLine())
	}
	again := Action{Label: i18n.T("Read it again"), Does: i18n.T("the failed read, once more, with it left in"), Key: "2", Off: u.rereadable() == nil}
	if gd.done() && u.failed() > 0 {
		s.Actions = []Action{{Label: i18n.T("Done"), Does: i18n.T("keep it as partial"), Key: "1", Off: u.pending()}, again}
		s.Waiting = i18n.T("·  waiting for a retry, or Done")
		return s
	}
	s.Actions = []Action{{Label: i18n.T("A port didn't work"), Does: i18n.T("nothing showed up there"), Key: "1",
		Off: gd.done() || busy != nil && busy.proof() == proofWait && busy.latest().State != proofGone}}
	if u.failed() > 0 {
		s.Actions = append(s.Actions, again)
	}
	return s
}

// tbAsleep reports whether one of the Thunderbolt 3 USB controllers is runtime-suspended.
func (u *usbPorts) tbAsleep(io IO) bool {
	for _, a := range u.tb {
		if io.Read("/sys/bus/pci/devices/"+a+"/power/runtime_status") == "suspended" {
			return true
		}
	}
	return false
}

// keepAwake keeps the Thunderbolt 3 USB controllers awake for the rest of the test (the fix): a
// device already in a USB-C port shows up as they wake.
func (u *usbPorts) keepAwake(c *Ctx) {
	u.awake = map[string]string{}
	for _, a := range u.tb {
		p := "/sys/bus/pci/devices/" + a + "/power/control"
		old := c.IO.Read(p)
		if old != "" && old != "on" && c.IO.Write(p, "on") == nil {
			u.awake[a] = old
		}
	}
	u.awakeAt, u.sayAt, u.marked, u.placed = c.T, c.T, false, false
}

// putBack gives the controllers kept awake their power/control back.
func (u *usbPorts) putBack(io IO) {
	for a, old := range u.awake {
		io.Write("/sys/bus/pci/devices/"+a+"/power/control", old)
		delete(u.awake, a)
	}
}

// fixed reports whether a port the result rests on was seen only thanks to the fix.
func (u *usbPorts) fixed() bool {
	for _, p := range u.mine() {
		if p.Awake && !p.Boot && !p.Phone {
			return true
		}
	}
	return false
}

// tbNames are the Thunderbolt 3 USB controllers for the evidence: "0000:07:00.0 (8086:15d4)".
func (u *usbPorts) tbNames(io IO) string {
	var out []string
	for _, a := range u.tb {
		d := "/sys/bus/pci/devices/" + a + "/"
		out = append(out, fmt.Sprintf("%s (%s:%s)", a, strings.TrimPrefix(io.Read(d+"vendor"), "0x"), strings.TrimPrefix(io.Read(d+"device"), "0x")))
	}
	return strings.Join(out, ", ")
}

// wokeNow reports whether the last thing that happened was the test keeping the controllers
// awake.
func (u *usbPorts) wokeNow() bool { return u.awakeAt > 0 && u.sayAt == u.awakeAt }

// wokeSay is Boot's line as the controllers are kept awake.
func (u *usbPorts) wokeSay() string {
	return i18n.T("my Thunderbolt 3 USB controller was asleep, and Linux doesn't wake it for a plug-in. it's awake now: plug it in again.")
}

// awakeLine is What I see's line while the fix is in use.
func (u *usbPorts) awakeLine() Line {
	return FixLine(i18n.T("OmaBoot? Thunderbolt 3 USB wake fix in use: its USB controller kept awake"))
}

// boundLines are What I see's lines for the map's ports that have their state, in drawing order.
func (u *usbPorts) boundLines() []Line {
	gd := u.gd
	var out []Line
	for _, dp := range gd.ports {
		switch gd.state[dp.ID] {
		case PortDead:
			out = append(out, gd.line(dp, i18n.T("marked as not working")))
		case PortBoot, PortSeen, PortPart:
			if p := u.onDrawn(dp.ID); p != nil {
				out = append(out, gd.line(dp, u.portText(p)))
			}
		}
	}
	return out
}

// onDrawn is the port matched to the map's port id, or nil.
func (u *usbPorts) onDrawn(id string) *usbPort {
	for _, p := range u.ports {
		if p.Drawn == id {
			return p
		}
	}
	return nil
}

// last is the port something showed up on most recently, or nil.
func (u *usbPorts) last() *usbPort {
	var l *usbPort
	for _, p := range u.ports {
		if !p.Boot && !p.Phone && (l == nil || p.At >= l.At) && len(p.Devs) > 0 && p.At > 0 {
			l = p
		}
	}
	return l
}

// ── the demo's Mac ────────────────────────────────────────────────────────────

// fakeUSB puts a USB device on the Fake at path dev: storage, with a disk of its own (the next
// free sdX, reading fine) or else a HID device without an input device.
func fakeUSB(f *Fake, dev, id, maker, product, speed string, storage bool) {
	if !storage {
		fakeUSBDev(f, dev, id, maker, product, speed, "03")
		return
	}
	fakeUSBDev(f, dev, id, maker, product, speed, "08")
	FakeDiskOn(f, dev, nextFakeDisk(f), fakeStickSize, FakeDisk{})
}

// fakeUnplug takes the device at dev (and anything behind it) off the Fake.
func fakeUnplug(f *Fake, dev string) {
	fakeGone(f, dev)
	for p := range f.Files {
		r := strings.TrimPrefix(p, usbDevices)
		if r != p && (r == dev || strings.HasPrefix(r, dev+"/") || strings.HasPrefix(r, dev+".") || strings.HasPrefix(r, dev+":")) {
			delete(f.Files, p)
		}
	}
}

// demoUUID is the demo image's archisosearchuuid.
const demoUUID = "2026-10-13-08-15-02-00"

// fakeBootStick puts the OmaBoot? stick at dev as sdb, the image on it; ram: booted with
// copytoram (archiso unmounted the stick; the image's UUID finds it).
func fakeBootStick(f *Fake, dev, id, maker, product, speed string, ram bool) {
	fakeUSBDev(f, dev, id, maker, product, speed, "08")
	FakeDiskOn(f, dev, "sdb", fakeStickSize, FakeDisk{Image: true})
	f.Set("/sys/class/block/sdb1/partition", "1")
	cmd := "BOOT_IMAGE=/arch/boot/x86_64/vmlinuz-linux-omarchy archisobasedir=arch archisosearchuuid=" + demoUUID + " quiet splash omaboot.live=1"
	if ram {
		f.Set("/proc/cmdline", cmd+" copytoram=y")
		FakeLink(f, "/dev/disk/by-uuid/"+demoUUID, "../../sdb1")
		return
	}
	f.Set("/proc/cmdline", cmd+" copytoram=n")
	f.Set("/proc/mounts", "/dev/sdb1 /run/archiso/bootmnt iso9660 ro,relatime 0 0")
}

// demoXHCI gives the demo's MacBook Air its USB 3 controller: bus 3 (USB 2 side) and bus 4
// (USB 3 side), two ports each, linked as peers, with the OmaBoot? stick in the left one.
func demoXHCI(f *Fake) {
	for _, b := range []string{"3", "4"} {
		f.Set(usbDevices+"usb"+b+"/idVendor", "1d6b")
		FakeLink(f, usbDevices+"usb"+b, "../../../devices/pci0000:00/0000:00:14.0/usb"+b)
		f.Set(usbDevices+"usb"+b+"/speed", map[string]string{"3": "480", "4": "5000"}[b])
		other := map[string]string{"3": "4", "4": "3"}[b]
		for _, n := range []string{"1", "2"} {
			port := usbDevices + "usb" + b + "/" + b + "-0:1.0/usb" + b + "-port" + n
			f.Set(port+"/connect_type", "hotplug")
			FakeLink(f, port+"/peer", "../../../usb"+other+"/"+other+"-0:1.0/usb"+other+"-port"+n)
		}
	}
	f.Set("/sys/bus/pci/devices/0000:00:14.0/vendor", "0x8086")
	f.Set("/sys/bus/pci/devices/0000:00:14.0/device", "0x1e31")
	fakeBootStick(f, "3-1", "0951:1666", "Kingston", "DataTraveler 3.0", "480", false)
}

// demoUSBA: a stick in the right port at 3.5 s (read twice), out at 7; a mouse at 9, moved at 10.2.
func demoUSBA(f *Fake) {
	demoXHCI(f)
	f.At(3.5, func(f *Fake) { fakeUSB(f, "4-2", "0781:5583", "SanDisk", "Ultra Fit", "5000", true) })
	f.At(7, func(f *Fake) { fakeUnplug(f, "4-2") })
	f.At(9, func(f *Fake) {
		fakeHID(f, "3-2", "046d:c077", "Logitech", "USB Optical Mouse", "1.5", "event7", 10.2, 10.4)
	})
}

// DemoUSBRead sets up the demo Air's USB-A test for the screens of the raw read (tools/screens):
//
//   - "fail": a USB 2 stick in the right port at 3.5 s whose read hits an I/O error 18 MB in;
//     at 8 s the same stick reads fine in the left port… which holds the OmaBoot? stick, so it
//     goes back into the right one at 10 s and reads fine there;
//   - "ram": booted with copytoram: the OmaBoot? stick moves from the left port to the right
//     one (out at 4 s, in at 6 s) and is checked against its image there;
//   - "mouse": a mouse in the right port at 3.5 s, moved at 5.2 s.
func DemoUSBRead(f *Fake, variant string) {
	demoXHCI(f)
	cruzer := func(f *Fake, dev string, d FakeDisk) {
		fakeUSBDev(f, dev, "0781:5567", "SanDisk", "Cruzer Blade", "480", "08")
		f.Set(usbDevices+dev+"/serial", "4C530001220528115034")
		FakeDiskOn(f, dev, nextFakeDisk(f), 15631122432, d)
	}
	switch variant {
	case "fail":
		f.At(3.5, func(f *Fake) { cruzer(f, "3-2", FakeDisk{MBps: 21, IOErrAt: 18 << 20}) })
		f.At(9, func(f *Fake) { fakeUnplug(f, "3-2") })
		f.At(10, func(f *Fake) { cruzer(f, "3-2", FakeDisk{MBps: 21}) })
	case "ram":
		delete(f.Files, "/proc/mounts") // archiso unmounted the stick
		fakeBootStick(f, "3-1", "0951:1666", "Kingston", "DataTraveler 3.0", "480", true)
		f.At(4, func(f *Fake) { fakeUnplug(f, "3-1") })
		f.At(6, func(f *Fake) {
			fakeUSBDev(f, "3-2", "0951:1666", "Kingston", "DataTraveler 3.0", "480", "08")
			FakeDiskOn(f, "3-2", "sdc", fakeStickSize, FakeDisk{Image: true, MBps: 21})
		})
	case "mouse":
		f.At(3.5, func(f *Fake) {
			fakeHID(f, "3-2", "046d:c077", "Logitech", "USB Optical Mouse", "1.5", "event7", 5.2, 5.4)
		})
	}
}

// demoUSBC plays the USB-C test on the same Mac (the demo has no USB-C Mac): a Thunderbolt 3
// controller's buses (5 and 6) appear with the device, as Alpine Ridge does.
func demoUSBC(f *Fake) {
	demoXHCI(f)
	f.At(4, func(f *Fake) {
		f.Set("/sys/bus/pci/devices/0000:3a:00.0/vendor", "0x8086")
		f.Set("/sys/bus/pci/devices/0000:3a:00.0/device", "0x15b6")
		for _, b := range []string{"5", "6"} {
			f.Set(usbDevices+"usb"+b+"/idVendor", "1d6b")
			FakeLink(f, usbDevices+"usb"+b, "../../../devices/pci0000:00/0000:00:1c.4/0000:06:00.0/0000:07:02.0/0000:3a:00.0/usb"+b)
			other := map[string]string{"5": "6", "6": "5"}[b]
			port := usbDevices + "usb" + b + "/" + b + "-0:1.0/usb" + b + "-port1"
			f.Set(port+"/connect_type", "hotplug")
			FakeLink(f, port+"/peer", "../../../usb"+other+"/"+other+"-0:1.0/usb"+other+"-port1")
		}
	})
	f.At(5.5, func(f *Fake) { fakeUSB(f, "6-1", "0781:5596", "SanDisk", "Ultra Dual Drive USB-C", "5000", true) })
}

// DemoPhone puts a tethering phone on the Fake at p.USB, for the screens of the phone's
// question: an iPhone at USB 2 speed, any other phone as a Pixel at USB 3 speed.
func DemoPhone(f *Fake, p *Phone) {
	if p.Apple {
		fakeUSBDev(f, p.USB, "05ac:12a8", "Apple Inc.", p.Name, "480", "ff")
		return
	}
	fakeUSBDev(f, p.USB, "18d1:4eeb", "Google", p.Name, "5000", "ff")
}

// DemoIMac sets up the iMac (Retina 5K, 27-inch, 2017) [iMac18,3] (DemoIMacPorts) for test id,
// for the screens of a desktop's port map (tools/screens): the chipset's USB 3 controller
// (buses 1 and 2, the four USB-A ports), Alpine Ridge's (buses 3 and 4, the two Thunderbolt 3
// ports) and the Thunderbolt 3 host. The OmaBoot? stick is a USB-A one in the first USB-A port
// for the USB-A test (the test asks which port), else a USB-C one in the
// first Thunderbolt 3 port. "usb-c-asleep" is the USB-C test with the USB-A stick and
// Alpine Ridge's controller asleep (the iMac18,1's 0.8.8 run): a USB-C stick shows up in the
// second Thunderbolt 3 port at 17 s, once the test has kept the controller awake.
func DemoIMac(f *Fake, id string) {
	ctl := map[string]string{"1": "0000:00:14.0", "2": "0000:00:14.0", "3": "0000:07:00.0", "4": "0000:07:00.0"}
	ports := map[string]int{"1": 4, "2": 4, "3": 2, "4": 2}
	peer := map[string]string{"1": "2", "2": "1", "3": "4", "4": "3"}
	for _, b := range []string{"1", "2", "3", "4"} {
		f.Set(usbDevices+"usb"+b+"/idVendor", "1d6b")
		up := "../../../devices/pci0000:00/" + ctl[b] + "/usb" + b
		if ctl[b] != "0000:00:14.0" {
			up = "../../../devices/pci0000:00/0000:00:1c.4/0000:05:00.0/0000:06:02.0/" + ctl[b] + "/usb" + b
		}
		FakeLink(f, usbDevices+"usb"+b, up)
		f.Set(usbDevices+"usb"+b+"/speed", map[string]string{"1": "480", "2": "10000", "3": "480", "4": "10000"}[b])
		for n := 1; n <= ports[b]; n++ {
			p := fmt.Sprintf("%susb%s/%s-0:1.0/usb%s-port%d", usbDevices, b, b, b, n)
			f.Set(p+"/connect_type", "hotplug")
			FakeLink(f, p+"/peer", fmt.Sprintf("../../../usb%s/%s-0:1.0/usb%s-port%d", peer[b], peer[b], peer[b], n))
		}
	}
	f.Set("/sys/bus/pci/devices/0000:00:14.0/vendor", "0x8086")
	f.Set("/sys/bus/pci/devices/0000:00:14.0/device", "0xa2af")
	f.Set("/sys/bus/pci/devices/0000:07:00.0/vendor", "0x8086")
	f.Set("/sys/bus/pci/devices/0000:07:00.0/device", "0x15b6")
	if id == "ports.usb-a" || id == "usb-c-asleep" {
		fakeBootStick(f, "1-1", "0951:1666", "Kingston", "DataTraveler 3.0", "480", false)
	} else {
		fakeBootStick(f, "4-1", "0781:5596", "SanDisk", "Ultra Dual Drive USB-C", "5000", false)
	}
	tb := "/sys/bus/thunderbolt/devices/"
	f.Set(tb+"domain0/security", "user")
	f.Set(tb+"0-0/device_name", "iMac")
	f.Set(tb+"0-0/vendor_name", "Apple Inc.")
	f.Set(tb+"0-0/authorized", "1")
	f.Set(tb+"0-0/generation", "3")
	for _, p := range []string{"0000:00:00.0", "0000:00:02.0", "0000:00:14.0", "0000:00:1c.4", "0000:05:00.0", "0000:06:02.0", "0000:07:00.0"} {
		f.Set("/sys/bus/pci/devices/"+p+"/vendor", "0x8086")
	}
	switch id {
	case "usb-c-asleep":
		f.Set("/sys/bus/pci/devices/0000:07:00.0/power/runtime_status", "suspended")
		f.Set("/sys/bus/pci/devices/0000:07:00.0/power/control", "auto")
		f.At(17, func(f *Fake) { fakeUSB(f, "4-2", "0781:5596", "SanDisk", "Ultra Dual Drive USB-C", "5000", true) })
	case "ports.usb-a": // a stick in the second port, a mouse in the fourth (moved at 10.5 s); the person marks the third as not working
		f.At(3, func(f *Fake) { fakeUSB(f, "2-2", "0781:5583", "SanDisk", "Ultra Fit", "5000", true) })
		f.At(9, func(f *Fake) { fakeHID(f, "1-4", "05ac:0304", "Apple", "Apple Mouse", "1.5", "event7", 10.5, 10.7) })
	case "ports.thunderbolt": // an adapter in the first port, let in by the kernel at once
		f.At(3, func(f *Fake) {
			f.Set(tb+"0-1/device_name", "Thunderbolt 3 to Thunderbolt 2 Adapter")
			f.Set(tb+"0-1/vendor_name", "Apple Inc.")
			f.Set(tb+"0-1/authorized", "1")
			f.Set(tb+"0-1/generation", "3")
			f.Set(tb+"0-1/rx_speed", "20.0 Gb/s")
		})
		f.At(4, func(f *Fake) {
			p := "/sys/bus/pci/devices/0000:0a:00.0/"
			f.Set(p+"vendor", "0x14e4")
			f.Set(p+"device", "0x1682")
			f.Set(p+"class", "0x020000")
			FakeLink(f, p+"driver", "../../../bus/pci/drivers/tg3")
		})
	}
}
