package safety

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/omaboot/boot-live/internal/sysroot"
)

// The real mode starts only on the OmaBoot? live system: an archiso boot that carries
// /usr/share/omaboot/. Anything else, this laptop included, is refused.
func TestGuard(t *testing.T) {
	mk := func(t *testing.T, files map[string]string) sysroot.Root {
		d := t.TempDir()
		for p, s := range files {
			full := filepath.Join(d, p)
			if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
				t.Fatal(err)
			}
			if s != "/" {
				if err := os.WriteFile(full, []byte(s), 0o644); err != nil {
					t.Fatal(err)
				}
			} else if err := os.MkdirAll(full, 0o755); err != nil {
				t.Fatal(err)
			}
		}
		return sysroot.Root(d)
	}
	plainArch := "BOOT_IMAGE=/boot/vmlinuz-linux root=UUID=1234 rw quiet"
	isoCmd := "BOOT_IMAGE=/arch/boot/x86_64/vmlinuz-linux-t2 archisobasedir=arch archisosearchuuid=x quiet"
	cases := []struct {
		name  string
		files map[string]string
		ok    bool
	}{
		{"installed Linux", map[string]string{"proc/cmdline": plainArch, "etc/os-release": "ID=arch"}, false},
		{"installed Linux with our files", map[string]string{"proc/cmdline": plainArch, "usr/share/omaboot/build": "live"}, false},
		{"another Arch ISO", map[string]string{"proc/cmdline": isoCmd, "run/archiso": "/"}, false},
		{"another Arch ISO, cmdline only", map[string]string{"proc/cmdline": isoCmd}, false},
		{"OmaBoot? live image", map[string]string{"proc/cmdline": isoCmd, "run/archiso": "/", "usr/share/omaboot/live-image": ""}, true},
		{"OmaBoot? image, no /run/archiso", map[string]string{"proc/cmdline": isoCmd, "usr/share/omaboot/allowed_signers": "k"}, true},
		{"OmaBoot? image, /run/archiso only", map[string]string{"proc/cmdline": "quiet", "run/archiso": "/", "usr/share/omaboot/build": "live"}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if err := Guard(mk(t, c.files)); (err == nil) != c.ok {
				t.Errorf("Guard = %v, want ok %v", err, c.ok)
			}
		})
	}
	for _, dir := range []string{"macbookair5,2", "imac10,1", "imac18,3", "qemu"} {
		if err := Guard(sysroot.Root("../../testdata/" + dir)); err != nil {
			t.Errorf("fixture %s isn't a live system: %v", dir, err)
		}
	}
}

// Internal disks are decided by bus: SATA and NVMe (and anything unknown) are internal; USB
// sticks and SD cards stay writable; loop devices and optical drives aren't disks to protect.
func TestDisks(t *testing.T) {
	cases := map[string]struct{ internal, external []string }{
		"macbookair5,2": {[]string{"sda"}, []string{"sdb", "sdc"}},
		"imac10,1":      {[]string{"sda"}, []string{"sdb"}},
		"imac18,3":      {[]string{"nvme0n1", "sda"}, []string{"mmcblk0", "sdb"}},
		"qemu":          {nil, []string{"sda", "sdb"}},
	}
	for dir, want := range cases {
		t.Run(dir, func(t *testing.T) {
			ds := Disks(sysroot.Root("../../testdata/" + dir))
			var in, ex []string
			for _, d := range ds {
				if d.Internal {
					in = append(in, d.Name)
				} else {
					ex = append(ex, d.Name)
				}
			}
			if !slices.Equal(in, want.internal) || !slices.Equal(ex, want.external) {
				t.Errorf("internal %v external %v, want %v and %v", in, ex, want.internal, want.external)
			}
			for _, d := range ds {
				if d.Name == "sda" && dir == "macbookair5,2" && !slices.Equal(d.Parts, []string{"sda1", "sda2", "sda3"}) {
					t.Errorf("sda partitions %v", d.Parts)
				}
			}
		})
	}
	// A disk on FireWire (a drive, a Mac in Target Disk Mode) is protected; one on USB isn't.
	for target, want := range map[string]Bus{
		"../devices/pci0000:00/0000:00:15.0/0000:04:00.0/fw1/fw1.0/host7/target7:0:0/7:0:0:0/block/sdc": BusFW,
		"../devices/pci0000:00/0000:00:04.1/usb2/2-4/2-4:1.0/host6/target6:0:0/6:0:0:0/block/sdb":       BusUSB,
	} {
		if got := busOf("sdc", target); got != want {
			t.Errorf("%s: %s, want %s", target, got, want)
		}
	}
	// Protect on a fixture tree is a dry run: nothing written, nothing refused.
	if _, err := Protect(sysroot.Root("../../testdata/macbookair5,2")); err != nil {
		t.Errorf("dry run: %v", err)
	}
}
