package handson

import (
	"strings"

	"github.com/omaboot/boot-live/internal/checks"
	"github.com/omaboot/boot-live/internal/i18n"
)

// fwGuard keeps a test that puts the Mac to sleep (sleep and wake, the lid) from starting while a
// FireWire disk is attached (an SBP-2 unit: a FireWire drive, a Mac in Target Disk Mode): the
// iMac10,1 hung going to sleep with a WD FireWire 800 drive plugged in, at pm_test's devices
// level too, and came back without it (0.7.5). It reads the FireWire bus once a second (sysfs
// only: nothing asks the disk itself), so unplugging the disk lets the test start. Other FireWire
// devices (a camera, an audio interface) don't hold the nap; What I see names them.
type fwGuard struct {
	disks  []checks.FWDisk
	others []checks.FWNode // devices on the bus that aren't disks
	polled float64
	asked  bool
	seen   string // the disks' block devices last made read-only
	had    bool   // a FireWire disk held the nap, and was unplugged
}

// step reads the bus at once the first time, then once a second.
func (f *fwGuard) step(c *Ctx) {
	if f.asked && c.T-f.polled < 1 {
		return
	}
	f.asked, f.polled = true, c.T
	held := f.holds()
	f.disks, f.others = fwOnBus(c.IO)
	if held && !f.holds() {
		f.had = true
	}
	var blk []string
	for _, d := range f.disks {
		blk = append(blk, d.Disks...)
	}
	if s := strings.Join(blk, " "); s != f.seen { // a disk that just showed up is made read-only, as at the start
		f.seen = s
		if p, ok := c.IO.(protector); ok && s != "" {
			p.ProtectDisks()
		}
	}
}

// fwOnBus is the FireWire disks on the bus and the other devices on it (the Mac's own node left
// out).
func fwOnBus(io IO) ([]checks.FWDisk, []checks.FWNode) {
	disks := checks.FireWireDisks(io, ioLinker{io})
	has := map[string]bool{}
	for _, d := range disks {
		has[d.Node.Name] = true
	}
	var others []checks.FWNode
	for _, n := range checks.FireWireNodes(io) {
		if !n.Local && !has[n.Name] {
			others = append(others, n)
		}
	}
	return disks, others
}

// holds reports whether the nap must wait: a FireWire disk is attached.
func (f *fwGuard) holds() bool { return len(f.disks) > 0 }

// lines are What I see's lines on the FireWire bus: each disk holding the nap, and the other
// devices on it, which don't.
func (f *fwGuard) lines() []Line {
	var out []Line
	if f.had && !f.holds() {
		out = append(out, Fact(i18n.T("the FireWire disk is unplugged: the nap can go ahead")))
	}
	for _, d := range f.disks {
		out = append(out, Bad(i18n.T("a FireWire disk is attached: %s. Unplug it first", d.Label())))
	}
	for _, n := range f.others {
		k := strings.Join(n.UnitKinds(), ", ")
		if k == "" {
			k = i18n.T("no units")
		}
		out = append(out, Fact(i18n.T("on FireWire: %s (%s), not a disk: the nap can go ahead", n.Label(), k)))
	}
	return out
}

// say is Boot's line while a FireWire disk holds the nap ("" when none does).
func (f *fwGuard) say() string {
	if !f.holds() {
		return ""
	}
	return i18n.T("a FireWire disk is plugged in. I won't nap with it: unplug it first.")
}

// note explains the wait, under the answers: lid for the lid test (it has no Start).
func (f *fwGuard) note(lid bool) string {
	switch {
	case !f.holds():
		return ""
	case lid:
		return i18n.T("A FireWire disk can hang a Mac's sleep under Linux (it did on an iMac10,1). Unplug it first: closing the lid puts the Mac to sleep once it's gone.")
	}
	return i18n.T("A FireWire disk can hang a Mac's sleep under Linux (it did on an iMac10,1). Unplug it first: Start comes back once it's gone.")
}

// FireWireHeld says why the Mac shouldn't sleep now because of a FireWire disk, in words, or ""
// (the console's sleepcheck asks it before each step).
func FireWireHeld(io IO) string {
	disks, _ := fwOnBus(io)
	if len(disks) == 0 {
		return ""
	}
	var ls []string
	for _, d := range disks {
		ls = append(ls, d.Label())
	}
	return i18n.T("a FireWire disk is attached (%s)", strings.Join(ls, "; "))
}

// DemoFWDisk puts the iMac10,1's FireWire disk on the demo's bus (a WD drive on FireWire 800,
// firewire_sbp2 bound, its disk sdc), unplugged at gone when gone > 0 and left out when gone < 0;
// camera adds a camcorder (AV/C), which doesn't hold a nap. For tools/screens.
func DemoFWDisk(f *Fake, gone float64, camera bool) {
	f.Set(fwBus+"fw0/is_local", "1")
	f.Set(fwBus+"fw0/vendor_name", "Apple Computer, Inc.")
	disk := map[string]string{
		fwBus + "fw1/is_local":                "0",
		fwBus + "fw1/guid":                    "0x0090a9a1b2c3d4e5",
		fwBus + "fw1/vendor":                  "0x0090a9",
		fwBus + "fw1/vendor_name":             "Western Digital",
		fwBus + "fw1/model":                   "0x001003",
		fwBus + "fw1/model_name":              "My Book Studio",
		fwBus + "fw1/fw1.0/specifier_id":      checks.SBP2Spec,
		fwBus + "fw1/fw1.0/version":           "0x010483",
		fakeLink + fwBus + "fw1/fw1.0/driver": "../../../../../bus/firewire/drivers/firewire_sbp2",
		"/sys/block/sdc/size":                 "1953525168",
		fakeLink + "/sys/block/sdc":           "../devices/pci0000:00/0000:00:15.0/0000:04:00.0/fw1/fw1.0/host7/target7:0:0/7:0:0:0/block/sdc",
	}
	for k, v := range disk {
		if gone >= 0 {
			f.Set(k, v)
		}
	}
	if gone > 0 {
		f.At(gone, func(f *Fake) {
			for k := range disk {
				delete(f.Files, k)
			}
		})
	}
	if camera {
		f.Set(fwBus+"fw2/is_local", "0")
		f.Set(fwBus+"fw2/vendor_name", "Canon")
		f.Set(fwBus+"fw2/model_name", "HV20")
		f.Set(fwBus+"fw2/fw2.0/specifier_id", "0x00a02d")
		f.Set(fwBus+"fw2/fw2.0/version", "0x010001")
	}
}
