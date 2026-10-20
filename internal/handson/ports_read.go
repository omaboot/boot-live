package handson

import (
	"fmt"
	"strings"

	g "github.com/omaboot/boot-live/internal/grid"
	"github.com/omaboot/boot-live/internal/i18n"
)

// How the USB port tests know a port moves data, not only that something enumerated on it
// (owner, 2026-10-13): each device that shows up gets a check of its own.
//
//   - a disk (a stick, a card reader with a card): read raw, diskread.go. Read fine → the port
//     moved data. An I/O error, a short read, a stall or a mismatch → the port is partial
//     until something reads fine on it again. A disk that comes out mid-read leaves its port
//     waiting for it to come back (or, once something shows up on another port, enumerated
//     only);
//   - a mouse or keyboard: its input events, from its own evdev nodes (grabbed meanwhile, so
//     its keys don't reach the screen). One move or key → the port moved data. None in 20 s,
//     or unplugged first → enumerated only;
//   - anything else (a phone charging, a hub with nothing behind it): enumerated only, as before.
//
// Enumerated-only ports count as working, as they did; the evidence says which ports moved data.

const (
	settleAfter = 1.5 // s: a hub's devices show up behind it before it's judged
	diskWait    = 10  // s: for a disk device to come up, or a medium in it
	inputUp     = 3   // s: for a HID device's input devices to come up
	inputWait   = 20  // s: for a move or a key
	failGrace   = 1   // s: a failed read waits this long for its device to be seen gone
)

// proof is where a device's check is.
type proof int

const (
	proofWait   proof = iota // under way
	proofData                // it moved data: read fine, or input events
	proofEnum                // enumerated only
	proofFailed              // the read failed
	proofGone                // a disk that came out before its read was through
)

// devCheck is one device's check on a port.
type devCheck struct {
	At    string // its USB path: the device on the port (a stick, a hub)
	Dev   usbDev
	Since float64 // run time it showed up
	Kind  string  // "disk", "input", or "" for anything else
	Disk  string  // its disk (sdc), once one came up
	Stick bool    // the OmaBoot? stick, in since power-on
	job   DiskJob
	Read  *DiskRead // the read, once done
	in    Input
	Evs   int // input events (keys, moves) from it
	State proof
	Why   string // English: what enumerated-only rests on, or why it failed
	fail  float64
}

// newCheck starts the check of a device that just showed up at path at.
func newCheck(at string, d usbDev, t float64) *devCheck {
	ch := &devCheck{At: at, Dev: d, Since: t}
	switch {
	case d.Storage:
		ch.Kind = "disk"
	case d.HID:
		ch.Kind = "input"
	}
	return ch
}

// advance moves the check on from the USB devices now (now) and the disks on them (disks).
func (ch *devCheck) advance(c *Ctx, now map[string]usbDev, disks map[string]string) bool {
	if ch.State != proofWait {
		return false
	}
	d, here := now[ch.At]
	here = here && d.ID == ch.Dev.ID
	age := c.T - ch.Since
	if ch.Kind == "" && here { // a hub: what's behind it decides
		for p, x := range now {
			if strings.HasPrefix(p, ch.At+".") {
				switch {
				case x.Storage:
					ch.Kind = "disk"
				case x.HID && ch.Kind == "":
					ch.Kind = "input"
				}
			}
		}
	}
	switch ch.Kind {
	case "disk":
		return ch.disk(c, here, age, disks)
	case "input":
		return ch.input(c, here, age)
	}
	if !here || age >= settleAfter {
		ch.enum("enumeration only")
		return true
	}
	return false
}

func (ch *devCheck) enum(why string) {
	ch.State, ch.Why = proofEnum, why
	ch.close()
}

// close lets go of its input devices and stops its read.
func (ch *devCheck) close() {
	if ch.in != nil {
		ch.in.Close()
		ch.in = nil
	}
	if ch.job != nil && ch.Read == nil {
		ch.job.Stop()
	}
}

func (ch *devCheck) disk(c *Ctx, here bool, age float64, disks map[string]string) bool {
	if ch.job == nil {
		if !here {
			ch.State, ch.Why = proofGone, "it came out before it was read"
			return true
		}
		disk := ch.Disk
		if disk == "" {
			disk = diskUnder(c.IO, disks, ch.At)
		}
		switch {
		case disk == "" && age >= diskWait:
			ch.enum(fmt.Sprintf("enumeration only: no disk came up for it in %d s", diskWait))
			return true
		case disk == "":
			return false
		case diskSize(c.IO, disk) == 0 && age >= diskWait:
			ch.Disk = disk
			ch.enum("enumeration only: no medium in it")
			return true
		case diskSize(c.IO, disk) == 0:
			return false
		}
		ch.Disk, ch.job = disk, readDisk(c.IO, disk)
	}
	r, ok := ch.job.Ready()
	if !ok {
		return false
	}
	switch {
	case r.Skipped != "":
		ch.Read = &r
		ch.enum("enumeration only: not read (" + r.Skipped + ")")
	case r.NoMedium:
		ch.Read = &r
		ch.enum("enumeration only: no medium in it")
	case r.OK():
		ch.Read, ch.State = &r, proofData
	case !here:
		ch.Read = &r
		ch.State, ch.Why = proofGone, "it came out before its read was through"
	default: // failed: a moment for sysfs to say whether it was pulled out
		if ch.fail == 0 {
			ch.fail = c.T
		}
		if c.T-ch.fail < failGrace {
			return false
		}
		ch.Read = &r
		ch.State, ch.Why = proofFailed, r.Fail
	}
	return true
}

func (ch *devCheck) input(c *Ctx, here bool, age float64) bool {
	if ch.in == nil {
		devs := inputsUnder(c.IO, ch.At)
		if len(devs) == 0 {
			if !here || age >= inputUp {
				ch.enum("enumeration only: no input device came up for it")
				return true
			}
			return false
		}
		want := map[string]bool{}
		for _, d := range devs {
			want[d.Event] = true
		}
		// grabbed: its keys go to this test alone, not to the screen's own handling
		ch.in, _ = OpenAll(c.IO, func(d Device) bool { return want[d.Event] }, true)
	}
	for _, e := range ch.in.Events() {
		if e.Type == EvKey || e.Type == EvRel || e.Type == EvAbs {
			ch.Evs++
		}
	}
	switch {
	case ch.Evs > 0:
		ch.State = proofData
		ch.close()
	case !here:
		ch.enum("enumeration only: it came out before it moved or typed")
	case age >= inputWait:
		ch.enum(fmt.Sprintf("enumeration only: no move or key from it in %d s", inputWait))
	default:
		return false
	}
	return true
}

// reading reports whether the check is a disk read under way (the device in, its read going).
func (ch *devCheck) reading() bool {
	return ch.State == proofWait && ch.job != nil
}

// ── a port's checks ───────────────────────────────────────────────────────────

// proof is where a port is: its latest check under way → waiting; else the latest that read
// or failed decides (a failed port is resolved by a good read after); else enumerated only.
// The stick's and the phone's ports count since before the test even when their own check
// came to nothing.
func (p *usbPort) proof() proof {
	var last *devCheck
	for _, ch := range p.Checks {
		if ch != nil {
			last = ch
		}
	}
	if last != nil && last.State == proofWait {
		return proofWait
	}
	for i := len(p.Checks) - 1; i >= 0; i-- {
		if ch := p.Checks[i]; ch != nil && (ch.State == proofData || ch.State == proofFailed) {
			return ch.State
		}
	}
	for _, ch := range p.Checks {
		if ch != nil && ch.State == proofEnum {
			return proofEnum
		}
	}
	if p.Boot || p.Phone || last == nil {
		return proofEnum
	}
	return proofWait // came out before its read was through: waiting for it to come back
}

// latest is the port's newest check, or nil.
func (p *usbPort) latest() *devCheck {
	for i := len(p.Checks) - 1; i >= 0; i-- {
		if p.Checks[i] != nil {
			return p.Checks[i]
		}
	}
	return nil
}

// settleGone takes a port whose disk came out mid-read as enumerated only: something showed
// up on another port, so the person moved on.
func (p *usbPort) settleGone() bool {
	ch := p.latest()
	if ch == nil || ch.State != proofGone {
		return false
	}
	ch.State, ch.Why = proofEnum, "enumeration only: "+ch.Why
	return true
}

// good is the port's check that moved data last, or nil.
func (p *usbPort) good() *devCheck {
	for i := len(p.Checks) - 1; i >= 0; i-- {
		if ch := p.Checks[i]; ch != nil && ch.State == proofData {
			return ch
		}
	}
	return nil
}

// failed is the port's failed read deciding it (proof failed), or nil.
func (p *usbPort) failedCheck() *devCheck {
	for i := len(p.Checks) - 1; i >= 0; i-- {
		if ch := p.Checks[i]; ch != nil && (ch.State == proofData || ch.State == proofFailed) {
			if ch.State == proofFailed {
				return ch
			}
			return nil
		}
	}
	return nil
}

// ── words ─────────────────────────────────────────────────────────────────────

// mbps is a read's speed as the screen says it: "141 MB/s", "4.2 MB/s".
func mbps(r *DiskRead) string {
	v := r.MBps()
	if v < 10 {
		return i18n.T("%.1f MB/s", v)
	}
	return i18n.T("%.0f MB/s", v)
}

// failWords is a failed read in the screen's words.
func failWords(r *DiskRead) string {
	mb := r.At >> 20
	switch r.Kind {
	case failOpen:
		return i18n.T("it wouldn't open")
	case failShort:
		return i18n.T("it ended early, at %d MB", mb)
	case failStall:
		return i18n.T("it stalled at %d MB", mb)
	case failSlow:
		return i18n.T("too slow: not done in 150 s")
	case failDiffer:
		return i18n.T("the two reads differ")
	case failImage:
		return i18n.T("it doesn't match the image")
	case failFile:
		return i18n.T("the test file came back wrong at %d MB", mb)
	}
	return i18n.T("an I/O error at %d MB", mb)
}

// noteW is the widest map note a read gives a port.
func readNoteW() int {
	return max(width(seenNote(i18n.T("%.0f MB/s", 9999.0))), width(i18n.T("! read failed")))
}

// text is What I see's text for the device and its check, after the port: "SanDisk Ultra Fit ·
// 5 Gb/s (USB 3) · 141 MB/s, read twice".
func (ch *devCheck) text(speed string) string {
	d := ch.Dev
	switch {
	case ch.State == proofData && ch.Read != nil && ch.Read.Way == WayImage && ch.Stick:
		return i18n.T("the OmaBoot? drive · %[1]s · since power-on · %[2]s, matches the image", speed, mbps(ch.Read))
	case ch.State == proofData && ch.Read != nil && ch.Read.Way == WayImage:
		return i18n.T("the OmaBoot? drive · %[1]s · %[2]s, matches the image", speed, mbps(ch.Read))
	case ch.State == proofData && ch.Read != nil && ch.Read.Way == WayFile:
		return i18n.T("%[1]s · %[2]s · %[3]s, test file verified", d.Name, speed, mbps(ch.Read))
	case ch.State == proofData && ch.Read != nil && ch.Stick:
		return i18n.T("the OmaBoot? drive · %[1]s · since power-on · %[2]s, read twice", speed, mbps(ch.Read))
	case ch.State == proofData && ch.Read != nil:
		return i18n.T("%[1]s · %[2]s · %[3]s, read twice", d.Name, speed, mbps(ch.Read))
	case ch.State == proofData:
		return i18n.N("%[2]s · %[3]s · %[1]d input event", "%[2]s · %[3]s · %[1]d input events", ch.Evs, ch.Evs, d.Name, speed)
	case ch.State == proofFailed && ch.Stick:
		return i18n.T("the OmaBoot? drive · %[1]s · its read failed: %[2]s", speed, failWords(ch.Read))
	case ch.State == proofFailed:
		return i18n.T("%[1]s · %[2]s · read failed: %[3]s", d.Name, speed, failWords(ch.Read))
	case ch.Stick:
		return i18n.T("the OmaBoot? drive · %[1]s · since power-on", speed)
	}
	return i18n.T("%[1]s · %[2]s", d.Name, speed)
}

// progress is the spinner's line while the check is under way, or false when it isn't.
func (ch *devCheck) progress() (Line, bool) {
	if ch.State == proofGone {
		return Line{Mark: "!", Color: g.AMB, Text: i18n.T("%s came out before its read was through", ch.name())}, true
	}
	if ch.State != proofWait {
		return Line{}, false
	}
	name := ch.name()
	switch {
	case ch.Kind == "input" && ch.in != nil:
		return Wait(i18n.T("%s: move it, or press a key on it", name)), true
	case ch.Kind == "input":
		return Wait(i18n.T("%s: waiting for its input device", name)), true
	case ch.Kind == "disk" && ch.job == nil:
		return Wait(i18n.T("%s: waiting for its disk", name)), true
	case ch.Kind == "disk":
		p := ch.job.Progress()
		done, total := int(p.Done>>20), int(max(p.Total, readLen)>>20)
		switch {
		case p.Way == WayImage:
			return Wait(i18n.T("checking %[1]d of %[2]d MB of the OmaBoot? drive…", done, total)), true
		case p.Way == WayFile:
			return Wait(i18n.T("checking %[1]d of %[2]d MB of the test file on %[3]s…", done, total, name)), true
		case p.Pass == 2:
			return Wait(i18n.T("reading %[1]d of %[2]d MB from %[3]s again…", done, total, name)), true
		case p.Way == WayTwice:
			return Wait(i18n.T("reading %[1]d of %[2]d MB from %[3]s…", done, total, name)), true
		}
		return Wait(i18n.T("opening %s…", name)), true
	}
	return Wait(i18n.T("%s: a moment", name)), true
}

// name is the device as the screen names it.
func (ch *devCheck) name() string {
	if ch.Stick || ch.Read != nil && ch.Read.Image {
		return i18n.T("the OmaBoot? drive")
	}
	return ch.Dev.Name
}

// evidence is the check in the report: "read the first 32 MiB of sdc twice, identical …".
func (ch *devCheck) evidence() string {
	r := ch.Read
	switch ch.State {
	case proofData:
		if r == nil {
			return fmt.Sprintf("%d input events from it (a move or a key)", ch.Evs)
		}
		if r.Way == WayImage {
			return fmt.Sprintf("the OmaBoot? drive: %d MiB of airootfs.sfs read from %s, verified against the image's checksums, %.1f MB/s",
				r.Bytes>>20, ch.Disk, r.MBps())
		}
		if r.Way == WayFile {
			return testFileEvidence(r, ch.Disk)
		}
		s := fmt.Sprintf("the first %d MiB of %s read twice, identical (sha256 %s…), %.1f MB/s", r.Bytes>>20, ch.Disk, r.Sum[:16], r.MBps())
		if r.Note != "" {
			s += " (" + r.Note + ")"
		}
		return s
	case proofFailed:
		return fmt.Sprintf("enumerated, but a %d MB read of %s failed: %s", max(r.Bytes, readLen)>>20, ch.Disk, r.Fail)
	case proofGone:
		return ch.Why
	case proofWait:
		return "still being checked when the test ended"
	}
	return ch.Why
}

// testFileProgress is the line for a read for the test file under way (the SD card and FireWire
// tests), or false when there's none.
func testFileProgress(job DiskJob) (Line, bool) {
	if job == nil {
		return Line{}, false
	}
	if _, done := job.Ready(); done {
		return Line{}, false
	}
	p := job.Progress()
	if p.Way == WayFile {
		return Wait(i18n.T("checking %[1]d of %[2]d MB of the test file…", int(p.Done>>20), int(max(p.Total, testFileBody)>>20))), true
	}
	return Wait(i18n.T("looking for the test file…")), true
}

// testFileEvidence is a test file read that went through, for the report.
func testFileEvidence(r *DiskRead, disk string) string {
	return fmt.Sprintf("OmaBoot?'s test file %s on %s read: %d MiB, every byte its pattern, %.1f MB/s", r.FileAt, disk, r.Bytes>>20, r.MBps())
}

// findings are what a good read says about the port beyond working: a USB 3 device that came up
// at USB 2 speed on a USB 3 port (the OmaBoot? drive's too: load doesn't change a link's speed),
// or read slowly at 5 Gb/s. Evidence, not a fail. Not slow on
// the OmaBoot? drive's own port (the Air's 0.7.9 run: 35.6 MB/s there, 124 MB/s at boot): the
// running system reads from that drive during the test, so its speed there says little, and
// boot.timing has its speed measured at boot.
func (p *usbPort) findings() []string {
	var out []string
	usb3 := strings.Contains(p.Key, " / ")
	for _, ch := range p.Checks {
		if ch == nil || ch.State != proofData || ch.Read == nil {
			continue
		}
		d := ch.Dev
		switch {
		case usb3 && d.usb3Capable() && d.Speed == "480":
			out = append(out, fmt.Sprintf("finding: %s is a USB 3 port and %s (a USB 3 device) came up at USB 2 speed", p.Key, d.Name))
		case p.Main && d.usb3Capable() && d.Speed == "480": // a USB-C port's USB 2 lines: its USB 3 side, on the Thunderbolt 3 chip, didn't come up
			out = append(out, fmt.Sprintf("finding: %s (a USB 3 device) came up at USB 2 speed on the main controller's %s: the USB-C port's USB 3 side, on the Thunderbolt 3 chip, didn't come up", d.Name, p.Key))
		}
		if d.Speed == "5000" && ch.Read.MBps() < 40 && !p.Boot {
			out = append(out, fmt.Sprintf("finding: %s read at %.1f MB/s at 5 Gb/s (below 40 MB/s)", d.Name, ch.Read.MBps()))
		}
	}
	return out
}
