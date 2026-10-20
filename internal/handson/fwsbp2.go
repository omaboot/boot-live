package handson

import (
	"context"
	"fmt"
	"os/exec"
	"path"
	"slices"
	"strings"
	"time"

	"github.com/omaboot/boot-live/internal/checks"
)

// sleepcheck's nosbp2 experiment (0.7.6; owner, 2026-10-15): the iMac10,1 hangs at pm_test's
// devices level with a FireWire disk attached. Taking firewire_sbp2 off the disk before the test
// suspend (its SCSI disk goes with it) and giving it back after shows whether the disk's driver
// is what hangs. The disk is never mounted or read; when it comes back it is made read-only again
// (safety.Protect), as when it was plugged in.
//
// 0.7.7 (owner, 2026-10-15): unbinding avoided the devices-level hang, but a real light sleep
// still hung: the suspend resets the FireWire bus, the drive comes back (as a new node, fw1 →
// fw2) and the driver core binds firewire_sbp2 to it again, mid-suspend. So the driver is
// unbound, then the module is unloaded (modprobe -r firewire_sbp2) for the step and loaded again
// after; where it can't be (in use, built in), the firewire bus's driver autoprobe is turned off
// instead, the units left unbound, and turned back on after. Only the units that had a disk count
// toward "back", matched by the node's GUID and the unit's place in its config ROM (the node's
// name may change after a bus reset). Each step is told to note before it's done (the sleepcheck
// trail on external storage, synced).

const (
	sbp2Driver  = "/sys/bus/firewire/drivers/firewire_sbp2"
	sbp2Module  = "firewire_sbp2"
	sbp2Sys     = "/sys/module/firewire_sbp2"
	fwAutoprobe = "/sys/bus/firewire/drivers_autoprobe"
)

// How nosbp2 kept firewire_sbp2 off the disks for a step (SBP2Off.Way).
const (
	SBP2Unloaded  = "unloaded"  // modprobe -r firewire_sbp2
	SBP2Autoprobe = "autoprobe" // unbound, and the firewire bus's drivers_autoprobe 0
)

// FWDiskUnit is a FireWire unit that had a disk before nosbp2 took the driver off: one that must
// come back.
type FWDiskUnit struct {
	Key   string   // the node's GUID and the unit's place in its config ROM: kept across a bus reset
	Unit  string   // its name then, "fw1.0"
	Disks []string // its block devices then, "sdb"
}

// SBP2Off is what Unload did, for Back.
type SBP2Off struct {
	Units     []string     // the units firewire_sbp2 was bound to, "fw1.0", "fw1.1"
	Labels    []string     // the disks as the screens name them
	Want      []FWDiskUnit // the units that had a disk: what must come back
	Disks     []string     // their block devices
	Way       string       // SBP2Unloaded or SBP2Autoprobe; "" when neither worked
	Autoprobe string       // drivers_autoprobe before (SBP2Autoprobe), put back after
	Taken     bool         // the driver is off (Back puts it back), also when Unload failed after
	Problems  []string
}

// fwUnitKey names a FireWire unit so it is found again after a bus reset renames its node: the
// node's GUID (else its vendor and model, else its name) and the unit's rom_index (else its
// number on the node).
func fwUnitKey(io IO, d checks.FWDisk) string {
	node := d.Node.GUID
	if node == "" && d.Node.Vendor != "" {
		node = d.Node.Vendor + ":" + d.Node.Model
	}
	if node == "" {
		node = d.Node.Name
	}
	dir := io.Read(fwBus + d.Node.Name + "/" + d.Unit + "/rom_index")
	if dir == "" {
		_, n, _ := strings.Cut(d.Unit, ".")
		dir = "." + n
	}
	return node + "/" + dir
}

// fwDiskUnits are the FireWire units with a block device now.
func fwDiskUnits(io IO) []FWDiskUnit {
	disks, _ := fwOnBus(io)
	var out []FWDiskUnit
	for _, d := range disks {
		if len(d.Disks) > 0 {
			out = append(out, FWDiskUnit{Key: fwUnitKey(io, d), Unit: d.Unit, Disks: d.Disks})
		}
	}
	return out
}

// fwUnitsBack matches the units that had a disk to the bus now: back ("fw2.0 sdd (was fw1.0
// sdb)") are those with a block device again, under whatever name, missing the rest. A unit
// that never had a disk (the WD drive's fw1.1) isn't asked for.
func fwUnitsBack(io IO, want []FWDiskUnit) (back []string, missing []FWDiskUnit) {
	now := fwDiskUnits(io)
	for _, w := range want {
		found := false
		for _, n := range now {
			if n.Key == w.Key {
				s := n.Unit + " " + strings.Join(n.Disks, " ")
				if n.Unit != w.Unit || strings.Join(n.Disks, " ") != strings.Join(w.Disks, " ") {
					s += " (was " + w.Unit + " " + strings.Join(w.Disks, " ") + ")"
				}
				back = append(back, s)
				found = true
				break
			}
		}
		if !found {
			missing = append(missing, w)
		}
	}
	return back, missing
}

// sbp2Loaded reports whether firewire_sbp2 is in the kernel (/sys/module/firewire_sbp2; a
// built-in one stays there too).
func sbp2Loaded(io IO) bool { return len(io.Glob(sbp2Sys)) > 0 }

// sbp2Bound are the SBP-2 units with a driver bound and those without one, on the nodes other
// than the Mac's own.
func sbp2Bound(io IO) (bound, free []string) {
	disks, _ := fwOnBus(io)
	for _, d := range disks {
		if io.Read(fwBus+d.Node.Name+"/"+d.Unit+"/specifier_id") != checks.SBP2Spec {
			continue
		}
		if d.Driver != "" {
			bound = append(bound, d.Unit)
		} else {
			free = append(free, d.Unit)
		}
	}
	return bound, free
}

// Unload takes firewire_sbp2 off every FireWire disk for a step: unbinds it from each unit,
// waits (up to 10 s) for their block devices to go, then unloads the module (modprobe -r) and
// checks /sys/module/firewire_sbp2 is gone. Where the module can't be removed, it turns the
// firewire bus's driver autoprobe off instead, so nothing binds the units again during the
// suspend (a real sleep resets the bus). note is called with each move, in English, before it's
// done. It fails when no unit has the driver, or when the driver can't be kept off (Taken: Back
// puts it back; don't sleep).
func (s *System) Unload(note func(string)) (SBP2Off, error) {
	var off SBP2Off
	if !s.Real() {
		return off, ErrNotReal
	}
	off = sbp2Unload(s, note, modprobe)
	if off.Way == "" {
		return off, fmt.Errorf("couldn't keep %s off the FireWire disks", sbp2Module)
	}
	return off, nil
}

// sbp2Unload is Unload on any IO (mp runs modprobe); its error is in Problems, Way "" when the
// driver couldn't be kept off.
func sbp2Unload(io IO, note func(string), mp func(...string) (string, error)) SBP2Off {
	var off SBP2Off
	disks, _ := fwOnBus(io)
	for _, d := range disks {
		if d.Driver == sbp2Module {
			off.Units = append(off.Units, d.Unit)
			off.Labels = append(off.Labels, d.Label())
			off.Disks = append(off.Disks, d.Disks...)
		}
	}
	off.Want = fwDiskUnits(io)
	if len(off.Units) == 0 {
		off.Problems = append(off.Problems, "no FireWire disk with "+sbp2Module+" bound to it")
		return off
	}
	off.Taken = true
	for _, u := range off.Units {
		note(fmt.Sprintf("unbinding %s from %s (echo %s > %s/unbind)", u, sbp2Module, u, sbp2Driver))
		if err := io.Write(sbp2Driver+"/unbind", u); err != nil {
			off.Problems = append(off.Problems, fmt.Sprintf("unbind %s: %v", u, err))
		}
	}
	released := waitFor(10*time.Second, func() bool { return fwReleased(io, off.Units) })
	if !released {
		note("the disk was still there 10 s after unbinding; modprobe -r takes it off anyway")
	}
	note("modprobe -r " + sbp2Module)
	out, err := mp("-r", sbp2Module)
	if err == nil && waitFor(5*time.Second, func() bool { return !sbp2Loaded(io) }) {
		off.Way = SBP2Unloaded
	} else {
		why := "/sys/module/" + sbp2Module + " still there"
		if err != nil {
			why = strings.TrimSpace(fmt.Sprintf("%v %s", err, out))
		}
		off.Problems = append(off.Problems, "modprobe -r "+sbp2Module+": "+why)
		off.Autoprobe = io.Read(fwAutoprobe)
		note("modprobe -r didn't work: " + why)
		note(fmt.Sprintf("turning the firewire bus's driver autoprobe off instead (echo 0 > %s)", fwAutoprobe))
		if err := io.Write(fwAutoprobe, "0"); err != nil {
			off.Problems = append(off.Problems, "drivers_autoprobe 0: "+err.Error())
			return off
		}
		off.Way = SBP2Autoprobe
		if bound, _ := sbp2Bound(io); len(bound) > 0 { // bound again before autoprobe went off
			for _, u := range bound {
				note(fmt.Sprintf("unbinding %s again (echo %s > %s/unbind)", u, u, sbp2Driver))
				if err := io.Write(sbp2Driver+"/unbind", u); err != nil {
					off.Problems = append(off.Problems, fmt.Sprintf("unbind %s: %v", u, err))
				}
			}
		}
	}
	if !waitFor(10*time.Second, func() bool { return fwReleased(io, nil) }) {
		off.Problems = append(off.Problems, "a FireWire disk still has its driver or its block device")
		off.Way = ""
	}
	return off
}

// waitFor polls ok every 100 ms for up to d.
func waitFor(d time.Duration, ok func() bool) bool {
	for end := time.Now().Add(d); ; time.Sleep(100 * time.Millisecond) {
		if ok() {
			return true
		}
		if time.Now().After(end) {
			return false
		}
	}
}

// fwReleased reports whether none of units (every FireWire disk when nil) has a driver or a
// block device any more.
func fwReleased(io IO, units []string) bool {
	disks, _ := fwOnBus(io)
	for _, d := range disks {
		if units != nil && !slices.Contains(units, d.Unit) {
			continue
		}
		if d.Driver != "" || len(d.Disks) > 0 {
			return false
		}
	}
	return true
}

// SBP2Still says what undid nosbp2 since Unload (firewire_sbp2 loaded again, a disk back), or ""
// when the driver is still off: asked right before the step's suspend.
func SBP2Still(io IO, off SBP2Off) string {
	if off.Way == SBP2Unloaded && sbp2Loaded(io) {
		return sbp2Module + " is loaded again"
	}
	if !fwReleased(io, nil) {
		return "a FireWire disk has its driver again"
	}
	return ""
}

// Back gives firewire_sbp2 back after the step (modprobe after modprobe -r; drivers_autoprobe
// put back after autoprobe), binds it to the SBP-2 units that have no driver (never to one the
// kernel bound already), waits up to 60 s for every unit that had a disk to have one again
// (under whatever node name the bus reset gave it), makes the disks read-only as they appear,
// and says what came back, in English.
func (s *System) Back(off SBP2Off, note func(string)) (string, error) {
	if !s.Real() {
		return "", ErrNotReal
	}
	msg, err := sbp2Back(s, off, note, modprobe, 60*time.Second)
	ro := "made read-only"
	if perr := s.ProtectDisks(); perr != nil {
		ro = "NOT made read-only: " + perr.Error()
	}
	if err != nil {
		return msg, err
	}
	return msg + ", " + ro, nil
}

// sbp2Back is Back on any IO (mp runs modprobe), without the read-only pass.
func sbp2Back(io IO, off SBP2Off, note func(string), mp func(...string) (string, error), wait time.Duration) (string, error) {
	problems := append([]string(nil), off.Problems...)
	if off.Way == SBP2Autoprobe || off.Autoprobe != "" && io.Read(fwAutoprobe) == "0" {
		v := off.Autoprobe
		if v == "" {
			v = "1"
		}
		note(fmt.Sprintf("turning the firewire bus's driver autoprobe back on (echo %s > %s)", v, fwAutoprobe))
		if err := io.Write(fwAutoprobe, v); err != nil {
			problems = append(problems, "drivers_autoprobe "+v+": "+err.Error())
		}
	}
	if !sbp2Loaded(io) {
		note("modprobe " + sbp2Module)
		if out, err := mp(sbp2Module); err != nil {
			problems = append(problems, strings.TrimSpace(fmt.Sprintf("modprobe %s: %v %s", sbp2Module, err, out)))
		}
	} else if off.Way == SBP2Unloaded {
		note(sbp2Module + " is loaded already (udev loaded it for the drive)")
	}
	tried := map[string]bool{}
	bind := func() { // the units with no driver; one the kernel bound itself is left alone
		_, free := sbp2Bound(io)
		for _, u := range free {
			if tried[u] {
				continue
			}
			tried[u] = true
			note(fmt.Sprintf("binding %s to %s (echo %s > %s/bind)", u, sbp2Module, u, sbp2Driver))
			if err := io.Write(sbp2Driver+"/bind", u); err != nil {
				problems = append(problems, fmt.Sprintf("bind %s: %v", u, err))
			}
		}
	}
	bind()
	var back []string
	var missing []FWDiskUnit
	start := time.Now()
	waitFor(wait, func() bool {
		back, missing = fwUnitsBack(io, off.Want)
		if len(missing) > 0 && time.Since(start) > 5*time.Second {
			bind() // a unit that showed up unbound since (a node the bus reset renamed)
		}
		return len(missing) == 0
	})
	if len(missing) > 0 {
		var ms []string
		for _, m := range missing {
			ms = append(ms, m.Unit+" "+strings.Join(m.Disks, " "))
		}
		msg := fmt.Sprintf("back with a disk after %d s: %d of %d (back: %s; missing: %s)", int(wait.Seconds()), len(back), len(off.Want),
			orNone(strings.Join(back, ", ")), strings.Join(ms, ", "))
		if len(problems) > 0 {
			msg += "; " + strings.Join(problems, "; ")
		}
		return msg + "; unplug the drive and plug it in again", fmt.Errorf("not every FireWire disk came back")
	}
	msg := "back: " + strings.Join(back, ", ") + ", bound to " + sbp2Module
	if len(off.Want) == 0 {
		msg = sbp2Module + " back; no unit had a disk before"
	}
	if len(problems) > len(off.Problems) {
		msg += " (" + strings.Join(problems[len(off.Problems):], "; ") + ")"
	}
	return msg, nil
}

func orNone(s string) string {
	if s == "" {
		return "none"
	}
	return s
}

// SBP2Units lists the FireWire disks firewire_sbp2 drives now, by label ("" when none): what
// nosbp2 would take the driver off.
func SBP2Units(io IO) []string {
	disks, _ := fwOnBus(io)
	var out []string
	for _, d := range disks {
		if d.Driver == sbp2Module {
			out = append(out, d.Label())
		}
	}
	return out
}

// FWBusText is the FireWire bus for the pre-sleep diagnostics: each node with its IDs and units
// (specifier:version, driver), then the disks the kernel made of them.
func FWBusText(r checks.Reader, lk interface{ Link(string) string }) []string {
	var out []string
	for _, n := range checks.FireWireNodes(r) {
		l := fmt.Sprintf("%s local %v, vendor %s %q, model %s %q, guid %s", n.Name, n.Local, orQ(n.Vendor), n.VendorName, orQ(n.Model), n.ModelName, orQ(n.GUID))
		out = append(out, l)
		for _, u := range r.Glob("/sys/bus/firewire/devices/" + n.Name + "/" + n.Name + ".*") {
			drv := "(none)"
			if lk != nil {
				if t := lk.Link(u + "/driver"); t != "" {
					drv = path.Base(t)
				}
			}
			out = append(out, fmt.Sprintf("  %s %s:%s driver %s", path.Base(u), orQ(r.Read(u+"/specifier_id")), orQ(r.Read(u+"/version")), drv))
		}
	}
	for _, d := range checks.FireWireDisks(r, lk) {
		out = append(out, fmt.Sprintf("disk: %s, driver %s, block devices %s", d.Label(), orQ(d.Driver), orQ(strings.Join(d.Disks, " "))))
	}
	if len(out) == 0 {
		out = append(out, "(no FireWire bus)")
	}
	return out
}

func modprobe(args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	b, err := exec.CommandContext(ctx, "modprobe", args...).CombinedOutput()
	return strings.TrimSpace(string(b)), err
}
