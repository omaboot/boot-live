package checks

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/omaboot/boot-live/internal/sysroot"
)

// smcTree is a fixture tree with one applesmc fan (minimum 2000, maximum 6500 rpm), written to
// for the test (fanWrite swapped for a plain write) and put back as it was afterwards.
func smcTree(t *testing.T) (*Env, string) {
	return smcTreeAt(t, "/sys/bus/acpi/devices/APP0001:00")
}

func smcTreeAt(t *testing.T, dir string) (*Env, string) {
	t.Helper()
	root := t.TempDir()
	for f, v := range map[string]string{"fan1_min": "2000", "fan1_max": "6500", "fan1_input": "2004", "fan1_label": "Exhaust"} {
		p := filepath.Join(root, dir, f)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(v+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	old := fanWrite
	fanWrite = func(r sysroot.Root, p, v string) error { return os.WriteFile(r.P(p), []byte(v), 0o644) }
	t.Cleanup(func() {
		fanWrite = old
		fanUndo.mu.Lock()
		fanUndo.closed, fanUndo.orig = false, nil
		fanUndo.mu.Unlock()
	})
	return &Env{Root: sysroot.Root(root)}, dir
}

// The raised fan minimum goes back on every way out: after the raise, when the check panics
// mid-raise, and when the tool exits mid-raise (RestoreFans), after which nothing is raised.
func TestFanRestore(t *testing.T) {
	e, dir := smcTree(t)
	min1 := dir + "/fan1_min"
	rs, err := raiseFans(e, dir, []int{2004}, 3)
	if err != nil || len(rs) != 1 || rs[0].To != 4200 || e.Root.Read(min1) != "4200" {
		t.Fatalf("raise: %+v %v, fan1_min %s", rs, err, e.Root.Read(min1))
	}
	restoreFans()
	if got := e.Root.Read(min1); got != "2000" {
		t.Errorf("after the raise: fan1_min %s", got)
	}
	func() { // a panic mid-raise: thermal's deferred restore
		defer func() { _ = recover() }()
		defer restoreFans()
		raiseFans(e, dir, []int{2004}, 3)
		panic("mid-raise")
	}()
	if got := e.Root.Read(min1); got != "2000" {
		t.Errorf("after a panic: fan1_min %s", got)
	}
	raiseFans(e, dir, []int{2004}, 3) // the tool exits mid-raise
	RestoreFans()
	if got := e.Root.Read(min1); got != "2000" {
		t.Errorf("after exiting: fan1_min %s", got)
	}
	if rs, err := raiseFans(e, dir, []int{2004}, 3); err == nil || len(rs) > 0 || e.Root.Read(min1) != "2000" {
		t.Errorf("raised after exiting: %+v", rs)
	}
}

// The fans are found in applesmc's own directory (the older driver) and in its hwmon device
// (linux-omarchy 7.2.8, Omarchy 4.0.5), where the light sensor stays behind.
func TestFanDir(t *testing.T) {
	for _, dir := range []string{"/sys/devices/platform/applesmc.768", "/sys/devices/platform/applesmc.768/hwmon/hwmon3"} {
		e, _ := smcTreeAt(t, dir)
		if got := fanDir(e); got != dir {
			t.Errorf("fans in %s: fanDir %q", dir, got)
		}
		if f, names := readFans(e, fanDir(e)); len(f) != 1 || f[0] != 2004 || names[0] != "Exhaust" {
			t.Errorf("fans in %s: read %v %v", dir, f, names)
		}
	}
}

// The fan verdict with a raised minimum: a fan that follows it passes even when the load alone
// didn't move it; one that doesn't stays uncertain; a fan already near its top isn't raised.
func TestFanRaiseVerdict(t *testing.T) {
	raised := []FanRaise{{Fan: 0, From: 2000, To: 4200, At: 3}}
	follows := Samples{Fans: [][]int{{2000}, {2004}, {2001}, {2900}, {3800}, {4150}}, Raised: raised}
	if r := FanVerdict(follows); r.Status != Supported {
		t.Errorf("followed the minimum: %s (%s)", r.Status, r.Evidence)
	}
	stuck := Samples{Fans: [][]int{{2000}, {2004}, {2001}, {2010}, {2003}, {2008}}, Raised: raised}
	if r := FanVerdict(stuck); r.Status != NotTested {
		t.Errorf("didn't follow: %s (%s)", r.Status, r.Evidence)
	}
	if _, ok := fanTarget(2000, 6500, 6300); ok {
		t.Error("raised a fan already near its top")
	}
	if to, ok := fanTarget(1000, 2600, 1100); !ok || to > 2500 {
		t.Errorf("iMac fan: %d %v", to, ok)
	}
}
