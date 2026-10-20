package handson

import (
	"reflect"
	"testing"

	"github.com/omaboot/boot-live/internal/checks"
)

// The FireWire disk guard decides from sysfs alone whether a nap may hang the Mac: an SBP-2 unit
// (bound or not) and a block device under a FireWire unit hold it; the Mac's own node and a
// camera don't.
func TestFireWireDisks(t *testing.T) {
	f := NewFake()
	DemoFWDisk(f, 0, true)
	disks, others := fwOnBus(f)
	if len(disks) != 1 || disks[0].Unit != "fw1.0" || disks[0].Driver != "firewire_sbp2" || !reflect.DeepEqual(disks[0].Disks, []string{"sdc"}) {
		t.Fatalf("disks = %+v", disks)
	}
	if len(others) != 1 || others[0].Name != "fw2" {
		t.Fatalf("others = %+v", others)
	}
	if FireWireHeld(f) == "" {
		t.Error("a FireWire disk attached doesn't hold the nap")
	}

	// a block device on a FireWire unit that isn't SBP-2 still counts; no driver link is fine
	g := NewFake()
	g.Set(fwBus+"fw0/is_local", "1")
	g.Set(fwBus+"fw3/is_local", "0")
	g.Set("/sys/block/sdd/size", "1")
	FakeLink(g, "/sys/block/sdd", "../devices/pci0000:00/0000:04:00.0/fw3/fw3.1/host9/target9:0:0/9:0:0:0/block/sdd")
	if d := checks.FireWireDisks(g, ioLinker{g}); len(d) != 1 || d[0].Unit != "fw3.1" || d[0].Node.Name != "fw3" {
		t.Fatalf("block device on FireWire: %+v", d)
	}

	// the camera alone, and an empty bus, hold nothing
	h := NewFake()
	DemoFWDisk(h, -1, true)
	if FireWireHeld(h) != "" || FireWireHeld(NewFake()) != "" {
		t.Error("held without a FireWire disk")
	}
}
