package checks

import (
	"context"
	"fmt"
	"path"
	"regexp"
	"sort"
	"strings"

	"github.com/omaboot/boot-live/internal/i18n"
)

// Reader is what the FireWire and optical drive lookups read: the Mac's files (sysroot.Root)
// or a hands-on test's IO.
type Reader interface {
	Read(p string) string
	Glob(pattern string) []string
}

// FWNode is one node on the FireWire bus (/sys/bus/firewire/devices/fwN): the Mac's own (local)
// or a device on the cable.
type FWNode struct {
	Name          string // fw0
	Local         bool
	Vendor, Model string // the config ROM's IDs, "0x000a27"
	VendorName    string // "LaCie"; "" when the ROM has none
	ModelName     string
	GUID          string
	Units         []string // each unit's specifier_id:version, "0x00609e:0x010483" (SBP-2 storage)
}

// Label is the node as the screens and the evidence name it: "LaCie d2 Quadra", else its IDs.
func (n FWNode) Label() string {
	if s := strings.TrimSpace(n.VendorName + " " + n.ModelName); s != "" {
		return s
	}
	if n.Vendor != "" {
		return "vendor " + n.Vendor + ", model " + orDash(n.Model)
	}
	return n.Name
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

// UnitKinds names the node's units by their specifier: SBP-2 storage (a drive, a Mac in Target
// Disk Mode), AV/C (a camera, a deck), IP over FireWire (another computer).
func (n FWNode) UnitKinds() []string {
	var out []string
	for _, u := range n.Units {
		spec, _, _ := strings.Cut(u, ":")
		k := map[string]string{"0x00609e": "SBP-2 storage", "0x00a02d": "AV/C", "0x00005e": "IP over FireWire"}[spec]
		if k == "" {
			k = "unit " + u
		}
		if !contains(out, k) {
			out = append(out, k)
		}
	}
	return out
}

// FireWireNodes are the nodes on the FireWire buses now, by name.
func FireWireNodes(r Reader) []FWNode {
	var out []FWNode
	for _, d := range r.Glob("/sys/bus/firewire/devices/fw*") {
		name := path.Base(d)
		if strings.Contains(name, ".") { // a unit (fw1.0), not a node
			continue
		}
		n := FWNode{Name: name, Local: r.Read(d+"/is_local") == "1", Vendor: r.Read(d + "/vendor"), Model: r.Read(d + "/model"),
			VendorName: r.Read(d + "/vendor_name"), ModelName: r.Read(d + "/model_name"), GUID: r.Read(d + "/guid")}
		for _, u := range r.Glob(d + "/" + name + ".*") {
			if s := r.Read(u + "/specifier_id"); s != "" {
				n.Units = append(n.Units, s+":"+r.Read(u+"/version"))
			}
		}
		out = append(out, n)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// SBP2Spec is the SBP-2 unit's specifier_id: storage (a FireWire drive, a Mac in Target Disk
// Mode), driven by firewire_sbp2.
const SBP2Spec = "0x00609e"

// FWDisk is a FireWire disk: an SBP-2 unit on a node other than the Mac's own, with the driver
// bound to it and the block devices the kernel made of it. A block device on the FireWire bus
// whose unit isn't SBP-2 is one too.
type FWDisk struct {
	Unit   string // the unit, "fw1.0"
	Node   FWNode
	Driver string   // the unit's driver, "firewire_sbp2"; "" when none is bound
	Disks  []string // its block devices, "sdb"
}

// Label is the disk as the screens name it: "LaCie d2 Quadra (fw1.0, sdb)".
func (d FWDisk) Label() string {
	in := append([]string{d.Unit}, d.Disks...)
	return d.Node.Label() + " (" + strings.Join(in, ", ") + ")"
}

// fwUnitPath is a FireWire unit in a sysfs path (…/fw1/fw1.0/host7/…): the unit's name.
var fwUnitPath = regexp.MustCompile(`/fw\d+/(fw\d+\.\d+)/`)

// FireWireDisks are the FireWire disks on the bus now (sleeping with one attached hung an
// iMac10,1, 0.7.5). lk reads the units' driver links and the block devices' targets; nil
// leaves those out.
func FireWireDisks(r Reader, lk linker) []FWDisk {
	var out []FWDisk
	at := map[string]int{}
	nodes := map[string]FWNode{}
	for _, n := range FireWireNodes(r) {
		nodes[n.Name] = n
		if n.Local {
			continue
		}
		for _, u := range r.Glob("/sys/bus/firewire/devices/" + n.Name + "/" + n.Name + ".*") {
			if r.Read(u+"/specifier_id") != SBP2Spec {
				continue
			}
			d := FWDisk{Unit: path.Base(u), Node: n}
			if lk != nil {
				if l := lk.Link(u + "/driver"); l != "" {
					d.Driver = path.Base(l)
				}
			}
			at[d.Unit] = len(out)
			out = append(out, d)
		}
	}
	if lk == nil {
		return out
	}
	for _, b := range r.Glob("/sys/block/*") {
		m := fwUnitPath.FindStringSubmatch(lk.Link(b))
		if m == nil {
			continue
		}
		i, ok := at[m[1]]
		if !ok {
			node, _, _ := strings.Cut(m[1], ".")
			i, at[m[1]] = len(out), len(out)
			out = append(out, FWDisk{Unit: m[1], Node: nodes[node]})
			if out[i].Node.Name == "" {
				out[i].Node.Name = node
			}
		}
		out[i].Disks = append(out[i].Disks, path.Base(b))
	}
	return out
}

// fwVendors names the FireWire controllers' makers the Intel Macs used, by PCI vendor ID.
var fwVendors = map[string]string{"104c": "Texas Instruments", "11c1": "LSI", "1106": "VIA", "1033": "NEC", "10de": "NVIDIA", "8086": "Intel"}

// FWController is the Mac's FireWire OHCI controller as the quick pass found it.
type FWController struct {
	Slot, ID, Name, Driver string
	OHCI                   string // "OHCI v1.10", from the driver's kernel log line; "" when not seen
	Local                  *FWNode
}

// Ready reports whether the bus is up: the controller driven, and the Mac's own node on it.
func (c FWController) Ready() bool { return c.Driver == "firewire_ohci" && c.Local != nil }

// Evidence is the controller in English, for the report.
func (c FWController) Evidence() string {
	s := fmt.Sprintf("FireWire OHCI controller %s (%s) at %s", c.Name, c.ID, c.Slot)
	if c.OHCI != "" {
		s += ", " + c.OHCI
	}
	if c.Driver != "" {
		s += ", driver " + c.Driver
	} else {
		s += ", no driver bound"
	}
	if c.Local != nil {
		s += ", local node " + c.Local.Name
		if c.Local.GUID != "" {
			s += " (guid " + c.Local.GUID + ")"
		}
	}
	return s
}

// FindFireWire finds the FireWire controller (PCI class 0c0010) and the bus's local node; false
// when the Mac has no FireWire controller at all.
func FindFireWire(e *Env) (FWController, bool) {
	ctrls := e.HW.PCIByClass("0c0010")
	if len(ctrls) == 0 {
		return FWController{}, false
	}
	d := ctrls[0]
	c := FWController{Slot: d.Slot, ID: d.ID(), Driver: d.Driver, Name: fwVendors[d.Vendor]}
	if c.Name == "" {
		c.Name = "vendor " + d.Vendor
	}
	for _, l := range e.Kmsg {
		if strings.Contains(l, "firewire_ohci "+d.Slot) {
			if i := strings.Index(l, "added OHCI v"); i >= 0 {
				if f := strings.Fields(l[i+len("added "):]); len(f) >= 2 {
					c.OHCI = f[0] + " " + f[1] // "OHCI v1.10"
				}
			}
		}
	}
	for _, n := range FireWireNodes(e.Root) {
		if n.Local {
			n := n
			c.Local = &n
			break
		}
	}
	return c, true
}

// fireWire looks at ports.firewire, "FireWire": the controller, its driver and the bus. Only
// a device on the cable shows the port works: the hands-on test (internal/handson,
// firewire.go) has the tester plug one in, and its result replaces this one.
//
//   - not_tested (uncertain): the controller is driven and the Mac's own node is on the bus:
//     ready, the hands-on test decides;
//   - failed: no FireWire controller on a Mac that has FireWire, no driver bound to it, or the
//     driver up and no local node on the bus.
func fireWire(_ context.Context, e *Env) []Result {
	c, ok := FindFireWire(e)
	var r Result
	switch {
	case !ok:
		r = fail("no FireWire OHCI controller (PCI class 0c0010) on a Mac that has FireWire")
		r.Detail = i18n.T("no FireWire controller")
	case c.Driver == "":
		r = fail(c.Evidence() + ": firewire_ohci isn't bound to it")
		r.Detail = i18n.T("FireWire controller, no driver")
	case c.Local == nil:
		r = fail(c.Evidence() + ", and no local node on the bus (/sys/bus/firewire/devices)")
		r.Detail = i18n.T("FireWire driver up, no bus")
	default:
		r = skip(Uncertain, c.Evidence()+": ready; the hands-on test plugs a device in and decides")
		r.Detail = i18n.T("FireWire controller %s ready", c.Name)
	}
	return []Result{r}
}
