package handson

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/omaboot/boot-live/internal/checks"
)

// nosbp2 counts only the units that had a disk, and finds them again after a bus reset renamed
// their node (the iMac10,1's WD drive with 0.7.6: fw1.0 the disk, fw1.1 none; back as fw2); it
// never binds a unit the kernel bound already.
func TestSBP2UnitsBack(t *testing.T) {
	f := NewFake()
	f.Set(fwBus+"fw0/is_local", "1")
	wd := func(node, disk string) {
		f.Set(fwBus+node+"/is_local", "0")
		f.Set(fwBus+node+"/guid", "0x0090a9a1b2c3d4e5")
		for i, u := range []string{".0", ".1"} {
			f.Set(fwBus+node+"/"+node+u+"/specifier_id", checks.SBP2Spec)
			f.Set(fwBus+node+"/"+node+u+"/rom_index", []string{"15", "23"}[i])
			FakeLink(f, fwBus+node+"/"+node+u+"/driver", "../../../../bus/firewire/drivers/firewire_sbp2")
		}
		f.Set("/sys/block/"+disk+"/size", "1")
		FakeLink(f, "/sys/block/"+disk, "../devices/pci0000:00/0000:04:00.0/"+node+"/"+node+".0/host7/target7:0:0/7:0:0:0/block/"+disk)
	}
	wd("fw1", "sdb")
	want := fwDiskUnits(f)
	if len(want) != 1 || want[0].Unit != "fw1.0" || want[0].Key != "0x0090a9a1b2c3d4e5/15" {
		t.Fatalf("want = %+v", want)
	}

	for k := range f.Files { // the bus reset: the drive goes…
		if strings.Contains(k, "/fw1") || strings.Contains(k, "/sdb") {
			delete(f.Files, k)
		}
	}
	if back, missing := fwUnitsBack(f, want); len(back) != 0 || len(missing) != 1 {
		t.Fatalf("drive gone: back %v, missing %v", back, missing)
	}
	wd("fw2", "sdd") // …and comes back as a new node, the driver bound by the kernel
	if back, missing := fwUnitsBack(f, want); !reflect.DeepEqual(back, []string{"fw2.0 sdd (was fw1.0 sdb)"}) || len(missing) != 0 {
		t.Fatalf("drive back: back %v, missing %v", back, missing)
	}

	f.Set(sbp2Sys+"/refcnt", "0") // udev loaded the module for it
	mp := func(...string) (string, error) { t.Error("modprobe run with the module loaded"); return "", nil }
	msg, err := sbp2Back(f, SBP2Off{Want: want, Way: SBP2Unloaded}, func(string) {}, mp, time.Second)
	if err != nil || !strings.HasPrefix(msg, "back: fw2.0 sdd") {
		t.Fatalf("Back: %q, %v", msg, err)
	}
	for _, w := range f.Writes {
		if strings.Contains(w, "/bind=") {
			t.Errorf("bound a unit the kernel had bound: %s", w)
		}
	}
}
