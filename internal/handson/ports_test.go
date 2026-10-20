package handson

import (
	"strings"
	"testing"

	"github.com/omaboot/boot-live/internal/checks"
	"github.com/omaboot/boot-live/internal/identify"
)

// Hub ports and device paths: the names the USB tests build from each other.
func TestUSBPaths(t *testing.T) {
	for dev, want := range map[string]string{
		"1-2":     "/sys/bus/usb/devices/usb1/1-0:1.0/usb1-port2",
		"2-1.3":   "/sys/bus/usb/devices/2-1/2-1:1.0/2-1-port3",
		"3-2.1.4": "/sys/bus/usb/devices/3-2.1/3-2.1:1.0/3-2.1-port4",
	} {
		if got := usbPortDir(dev); got != want {
			t.Errorf("usbPortDir(%s) = %s", dev, got)
		}
	}
	for port, want := range map[string]string{"usb2-port2": "2-2", "2-1-port3": "2-1.3", "usb10-port12": "10-12"} {
		if got := usbPortDev(port); got != want {
			t.Errorf("usbPortDev(%s) = %s", port, got)
		}
	}
	for p, want := range map[string]bool{"1-2": true, "2-1.3": true, "usb1": false, "1-2:1.0": false, "1-0:1.0": false, "domain0": false} {
		if isUSBDevicePath(p) != want {
			t.Errorf("isUSBDevicePath(%s) != %v", p, want)
		}
	}
}

// A USB 2 and a USB 3 device in the same hole are one port; a new hub is its own port, once.
func TestUSBPortGrouping(t *testing.T) {
	f := NewFake()
	demoXHCI(f)
	if a, b := usbPortKey(f, "3-2"), usbPortKey(f, "4-2"); a != "3-2 / 4-2" || a != b {
		t.Errorf("peers: %q and %q", a, b)
	}
	if got := usbPortKey(f, "2-1.3"); got != "2-1.3" {
		t.Errorf("no peer: %q", got)
	}
	start := map[string]usbDev{"2-1": {}, "2-1.1": {}}
	for dev, want := range map[string]string{"2-1.3": "2-1.3", "3-2.1.4": "3-2", "2-1.1.2": "2-1.1.2"} {
		if got := usbPortOf(dev, start); got != want {
			t.Errorf("usbPortOf(%s) = %s, want %s", dev, got, want)
		}
	}
}

// The Mac's own devices never count as a port; the tethering phone is never one of them.
func TestUSBInternal(t *testing.T) {
	start := map[string]usbDev{"2-1.2": {ID: "05ac:024c"}, "1-3": {ID: "05ac:12a8"}}
	const phone = "05ac:12a8"
	cases := []struct {
		at   string
		d    usbDev
		want bool
	}{
		{"2-1.2", usbDev{ID: "05ac:024c"}, true},                          // the keyboard came back
		{"2-1.2", usbDev{ID: "05ac:024c", ConnectType: "hotplug"}, false}, // a hotplug port: someone replugged it
		{"2-1.2", usbDev{ID: "0781:5583"}, false},                         // something else there now
		{"1-4", usbDev{ID: "05ac:8510", ConnectType: "hard-wired"}, true},
		{"1-2", usbDev{ID: "0781:5583"}, false},
		{"1-3", usbDev{ID: phone, ConnectType: "unknown"}, false}, // the phone back in its own port
	}
	for _, c := range cases {
		if got := usbInternal(c.at, c.d, start, phone); got != c.want {
			t.Errorf("usbInternal(%s, %s, %s) = %v", c.at, c.d.ID, c.d.ConnectType, got)
		}
	}
}

// A failed read (unresolved) never lets the test pass, whatever else worked.
func TestUSBVerdict(t *testing.T) {
	cases := []struct {
		seen, failed, dead int
		want               string
	}{
		{2, 0, 0, checks.Supported + "/" + Automatic},
		{2, 0, 1, checks.Partial + "/" + Observed},
		{0, 0, 1, checks.Failed + "/" + Observed},
		{0, 0, 0, "/"},
		{3, 1, 0, checks.Partial + "/" + Automatic},
		{0, 1, 0, checks.Partial + "/" + Automatic},
		{1, 1, 1, checks.Partial + "/" + Observed},
	}
	for _, c := range cases {
		st, m := usbVerdict(c.seen, c.failed, c.dead)
		if st+"/"+m != c.want {
			t.Errorf("usbVerdict(%d, %d, %d) = %s/%s, want %s", c.seen, c.failed, c.dead, st, m, c.want)
		}
	}
}

// The port and Bluetooth tests' rules, on scripted Macs.
func TestPortRules(t *testing.T) {
	demo := func(id string) func(*Fake) {
		s, _ := Lookup(id)
		return s.Demo
	}
	tb := "/sys/bus/thunderbolt/devices/"
	// tbNet: the run online through en1, on the Thunderbolt Ethernet adapter the demo plugs in
	// (0000:0a:00.0): unplugged at 1 s, its interface back at 6 s, the status line online again
	// through it at back (0: never)
	tbNet := func(back float64) func(*Fake) {
		return func(f *Fake) {
			demo("ports.thunderbolt")(f)
			f.Set(fakeNet+"online", "en1, Ethernet")
			f.Set("/sys/class/net/en1/carrier", "1")
			FakeLink(f, "/sys/class/net/en1/device", "../../../0000:0a:00.0")
			f.At(1, func(f *Fake) {
				delete(f.Files, "/sys/class/net/en1/carrier")
				delete(f.Files, fakeLink+"/sys/class/net/en1/device")
				f.Set(fakeNet+"offline", "en1: gone")
			})
			f.At(6, func(f *Fake) {
				f.Set("/sys/class/net/en1/carrier", "1")
				FakeLink(f, "/sys/class/net/en1/device", "../../../0000:0a:00.0")
			})
			if back > 0 {
				f.SetAt(back, fakeNet+"offline", "")
			}
		}
	}
	cases := []struct {
		name, id string
		until    float64
		script   func(*Fake)
		picks    map[float64]int
		want     string
		ev       string // in the evidence
	}{
		{"usb-a all worked", "ports.usb-a", 12, demo("ports.usb-a"), map[float64]int{11: 0}, "supported/automatic", "ports seen: 2"},
		{"usb-a one dead", "ports.usb-a", 12, demo("ports.usb-a"), map[float64]int{10: 1, 11: 0}, "partial/observed", "1 port(s) that didn't work"},
		{"usb-a nothing seen", "ports.usb-a", 12, nil, map[float64]int{5: 1, 6: 0}, "failed/observed", "ports seen: 0"},
		{"usb-a done greyed with nothing", "ports.usb-a", 8, nil, map[float64]int{6: 0}, "undecided", ""},
		{"usb-a skipped", "ports.usb-a", 3, nil, map[float64]int{2: -1}, "not_tested/no-equipment", ""},
		{"usb-c controller wakes", "ports.usb-c", 9, demo("ports.usb-c"), map[float64]int{8: 0}, "supported/automatic", "USB buses that appeared"},
		{"sd card", "ports.sd-card", 8, demo("ports.sd-card"), nil, "supported/automatic", "sdc on usb"},
		{"sd card, test file verified", "ports.sd-card", 8, demo("ports.sd-card"), nil, "supported/automatic", "every byte its pattern"},
		{"sd card, test file wrong", "ports.sd-card", 8, func(f *Fake) {
			demo("ports.sd-card")(f)
			f.Disks["sdc"] = FakeDisk{TestFile: true, BadFile: 3 << 20}
		}, nil, "partial/automatic", "didn't read right"},
		{"sd card, no test file", "ports.sd-card", 8, func(f *Fake) {
			demo("ports.sd-card")(f)
			f.Disks["sdc"] = FakeDisk{}
		}, nil, "supported/automatic", "no test file in the top folder"},
		{"firewire drive, test file verified", "ports.firewire", 12, demo("ports.firewire"), nil, "supported/automatic", "every byte its pattern"},
		{"firewire drive, test file wrong", "ports.firewire", 12, func(f *Fake) {
			demo("ports.firewire")(f)
			f.At(4.5, func(f *Fake) { f.Disks["sdc"] = FakeDisk{TestFile: true, BadFile: 1 << 20, MBps: 38} })
		}, nil, "partial/automatic", "didn't read right"},
		{"sd no card", "ports.sd-card", 17, nil, map[float64]int{16: 0}, "failed/observed", "no card reader seen"},
		{"sd stick isn't a card", "ports.sd-card", 17, func(f *Fake) {
			demoXHCI(f)
			f.At(3, func(f *Fake) {
				fakeUSBDev(f, "3-2", "0781:5583", "SanDisk", "Ultra Fit", "480", "08")
				f.Set(usbDevices+"3-2:1.0/host7/target7:0:0/7:0:0:0/block/sdd/size", "0")
				f.Set("/sys/block/sdd/size", "60437492")
			})
		}, map[float64]int{16: 0}, "failed/observed", ""},
		{"thunderbolt hotplug", "ports.thunderbolt", 10, demo("ports.thunderbolt"), nil, "supported/automatic", "tg3"},
		// the run's network on the adapter: the result waits for it to come back through it
		{"thunderbolt carrying the run, network not back yet", "ports.thunderbolt", 11.5, tbNet(12), nil, "undecided", ""},
		{"thunderbolt carrying the run, network back", "ports.thunderbolt", 14, tbNet(12), nil, "supported/automatic", "online through it again"},
		{"thunderbolt carrying the run, network never back", "ports.thunderbolt", 30, tbNet(0), nil, "undecided", ""},
		{"thunderbolt carrying the run, network never back: on after 30 s", "ports.thunderbolt", 40, tbNet(0), nil, "supported/automatic", "didn't come back through it within 30 s"},
		{"thunderbolt carrying the run, not waited for", "ports.thunderbolt", 14, tbNet(0), map[float64]int{9: -1}, "supported/automatic", "went on without waiting"},
		{"thunderbolt no tunnel", "ports.thunderbolt", 30, func(f *Fake) {
			f.Set(tb+"0-0/generation", "2")
			f.At(3, func(f *Fake) {
				f.Set(tb+"0-1/device_name", "Thunderbolt to Gigabit Ethernet Adapter")
				f.Set(tb+"0-1/authorized", "1")
			})
		}, nil, "partial/automatic", "no PCI device (other than bridges)"},
		{"thunderbolt bridges only", "ports.thunderbolt", 30, func(f *Fake) {
			f.At(3, func(f *Fake) { f.Set(tb+"0-1/device_name", "Dock"); f.Set(tb+"0-1/authorized", "1") })
			f.SetAt(4, "/sys/bus/pci/devices/0000:08:00.0/class", "0x060400")
		}, nil, "partial/automatic", "0000:08:00.0"},
		// held back, it waits for Let it in however long; let in and still unauthorized 20 s
		// later (the kernel didn't take it), partial
		{"thunderbolt unauthorized", "ports.thunderbolt", 50, func(f *Fake) {
			f.At(3, func(f *Fake) { f.Set(tb+"0-1/device_name", "Dock"); f.Set(tb+"0-1/authorized", "0") })
			f.SetAt(26, tb+"0-1/authorized", "0")
		}, map[float64]int{25: 0}, "partial/automatic", "stayed unauthorized for 20 s after Let it in"},
		{"thunderbolt let in", "ports.thunderbolt", 30, func(f *Fake) {
			f.Set(tb+"domain0/security", "user")
			f.At(3, func(f *Fake) { f.Set(tb+"0-1/device_name", "Dock"); f.Set(tb+"0-1/authorized", "0") })
			f.SetAt(8, "/sys/bus/pci/devices/0000:0a:00.0/class", "0x0c0330")
		}, map[float64]int{4: 0, 7: 0}, "supported/automatic", "the tester let it in"},
		{"thunderbolt nothing", "ports.thunderbolt", 17, func(f *Fake) { f.Set(tb+"0-0/generation", "1") }, map[float64]int{16: 0}, "failed/observed", "firmware only sets up"},
		{"thunderbolt at boot, no replug", "ports.thunderbolt", 25, func(f *Fake) {
			f.Set(tb+"0-1/device_name", "Thunderbolt Display")
			f.Set(tb+"0-1/authorized", "1")
			f.At(3, func(f *Fake) { delete(f.Files, tb+"0-1/device_name"); delete(f.Files, tb+"0-1/authorized") })
		}, map[float64]int{10: 0, 20: 0}, "partial/observed", "didn't come back"},
		{"thunderbolt display isn't a device", "ports.thunderbolt", 17, func(f *Fake) {
			f.SetAt(3, "/sys/class/drm/card1-DP-1/status", "connected")
		}, map[float64]int{16: 0}, "failed/observed", "DisplayPort, not counted"},
		{"bluetooth, the tester picks theirs", "network.bluetooth", 10, demo("network.bluetooth"), map[float64]int{8: 1}, "supported/challenge", "picked their own device from the 3 listed: phone (classic, -52 dBm)"},
		// strangers' devices alone never pass: no pick decides nothing, "Mine isn't listed" is partial
		{"bluetooth, strangers only", "network.bluetooth", 60, demo("network.bluetooth"), map[float64]int{8: 3}, "undecided", ""},
		{"bluetooth, mine isn't listed", "network.bluetooth", 35, demo("network.bluetooth"), map[float64]int{31: 3}, "partial/observed", "wasn't among the 3 listed"},
		{"bluetooth no adapter", "network.bluetooth", 1, nil, nil, "failed/automatic", "no Bluetooth adapter"},
		{"bluetooth quiet", "network.bluetooth", 32, func(f *Fake) {
			f.Set("/sys/class/bluetooth/hci0/type", "Primary")
			FakeBluetooth(f, "hci0", "Broadcom, Bluetooth 4.0", true)
		}, map[float64]int{20: 1, 31: 1}, "partial/observed", "found nothing"},
		{"bluetooth won't power on", "network.bluetooth", 2, func(f *Fake) {
			f.Set("/sys/class/bluetooth/hci0/type", "Primary")
			FakeBluetooth(f, "hci0", "Broadcom, Bluetooth 4.0", false)
			f.Set(fakeBT+"hci0/refuse", "rfkilled")
		}, nil, "failed/automatic", "wouldn't power on: rfkilled"},
		{"bluetooth won't scan", "network.bluetooth", 2, func(f *Fake) {
			f.Set("/sys/class/bluetooth/hci0/type", "Primary")
			FakeBluetooth(f, "hci0", "Broadcom, Bluetooth 4.0", false)
			f.Set(fakeBT+"hci0/noscan", "rejected")
		}, nil, "failed/automatic", "wouldn't start discovering (classic and LE, classic only, LE only): rejected"},
	}
	for _, c := range cases {
		o := play(t, c.id, c.until, c.script, c.picks)
		if got := status(o); got != c.want {
			t.Errorf("%s: %s, want %s", c.name, got, c.want)
			continue
		}
		if c.ev != "" && !strings.Contains(o.Results[0].Evidence, c.ev) {
			t.Errorf("%s: evidence %q lacks %q", c.name, o.Results[0].Evidence, c.ev)
		}
	}
}

// The Bluetooth test puts the adapter's power and block back; names stay out of the report.
func TestBluetoothRestored(t *testing.T) {
	s, _ := Lookup("network.bluetooth")
	f := NewFake()
	DemoMac(f)
	s.Demo(f)
	f.Set("/sys/class/bluetooth/hci0/rfkill1/soft", "1")
	r := Begin(s, &Env{}, f)
	for tt := 0.0; tt < 12 && r.Outcome() == nil; tt += 0.1 {
		if tt > 9 {
			r.Act(1) // the Pixel: the tester's own
		}
		r.Step(tt)
	}
	r.Stop()
	if got := f.Read(fakeBT + "hci0/power"); got != "0" {
		t.Errorf("power left at %s", got)
	}
	if got := f.Read("/sys/class/bluetooth/hci0/rfkill1/soft"); got != "1" {
		t.Errorf("soft block left at %s", got)
	}
	if o := r.Outcome(); o == nil || strings.Contains(o.Results[0].Evidence, "Pixel") {
		t.Errorf("outcome %+v", o)
	}
}

func TestParseEIR(t *testing.T) {
	eir := []byte{4, 0x0d, 0x0c, 0x02, 0x5a, 6, 0x09, 'P', 'i', 'x', 'e', 'l', 0, 0}
	if n, k, _ := parseEIR(eir); n != "Pixel" || k != "phone" {
		t.Errorf("parseEIR: %q %q", n, k)
	}
	if n, k, _ := parseEIR([]byte{3, 0x19, 0xc2, 0x03, 9, 0x08}); n != "" || k != "input" { // a mouse; a cut-off field after it
		t.Errorf("parseEIR LE: %q %q", n, k)
	}
	// an iPhone over LE: flags, then Apple's data (Nearby), no name
	if n, k, m := parseEIR([]byte{2, 0x01, 0x1a, 7, 0xff, 0x4c, 0x00, 0x10, 0x02, 0x0b, 0x00}); n != "" || k != "" || m != "Apple" {
		t.Errorf("parseEIR iPhone: %q %q %q", n, k, m)
	}
	if got := btClean("Carl’s iPhone \U0001F4F1"); got != "Carl's iPhone" {
		t.Errorf("btClean: %q", got)
	}
}

// With a port map, each port asked for in turn: the stick's port as the tester says, a device
// matched to the port asked for, "A port didn't work" marking it, and the test deciding by
// itself once every port has its state; the evidence records the matches.
func TestPortMapGuided(t *testing.T) {
	demo := func(id string) func(*Fake) {
		s, _ := Lookup(id)
		return s.Demo
	}
	air, imac := &Env{Ports: DemoAirPorts()}, &Env{Ports: DemoIMacPorts()}
	imacMirrored := &Env{Ports: identify.MirrorBack(DemoIMacPorts())}
	iPhone := &Phone{USB: "3-2", Name: "iPhone", Apple: true, Online: true}
	airPhone, plainPhone := &Env{Ports: DemoAirPorts(), Phone: iPhone}, &Env{Phone: iPhone}
	imacPhone := &Env{Ports: DemoIMacPorts(), Phone: &Phone{USB: "1-3", Name: "iPhone", Apple: true, Online: true}}
	phoneOnAir := func(f *Fake) {
		demoXHCI(f)
		fakeUSB(f, "3-2", "05ac:12a8", "Apple Inc.", "iPhone", "480", false)
	}
	onIMac := func(id string) func(*Fake) { return func(f *Fake) { DemoIMac(f, id) } }
	imacUSBA := &Env{Ports: DemoIMacPorts(), USBA: map[string]bool{"1-2": true, "2-2": true}}
	cases := []struct {
		name, id string
		env      *Env
		until    float64
		script   func(*Fake)
		picks    map[float64]int
		want     string
		ev       []string // in the evidence
	}{
		{"usb-a both", "ports.usb-a", air, 8, demo("ports.usb-a"), map[float64]int{1: 0}, "supported/automatic",
			[]string{"left-2 USB 3 = 3-1 / 4-1 (the OmaBoot? drive: the tester said which port)", "right-2 USB 3 = 3-2 / 4-2, firmware says panel=right, vertical=center, horizontal=right"}},
		{"usb-a stick on the right", "ports.usb-a", air, 8, demo("ports.usb-a"), map[float64]int{1: 1}, "supported/automatic",
			[]string{"left-2 USB 3 = 3-2 / 4-2", "right-2 USB 3 = 3-1 / 4-1 (the OmaBoot? drive"}},
		{"usb-a no answer, no decision", "ports.usb-a", air, 8, demo("ports.usb-a"), nil, "undecided", nil},
		{"usb-a right dead", "ports.usb-a", air, 4, demo("ports.usb-a"), map[float64]int{1: 0, 2: 0}, "partial/observed",
			[]string{"right-2 USB 3: marked as not working by the tester"}},
		{"usb-a no Done with a map", "ports.usb-a", air, 3, nil, map[float64]int{1: 0}, "undecided", nil},
		// the stick in the first USB-A port (asked), a stick in the second, the third marked, a
		// mouse in the fourth: partial, never supported with a port marked dead
		{"usb-a on the iMac", "ports.usb-a", imac, 30, onIMac("ports.usb-a"), map[float64]int{1: 0, 6: 0}, "partial/observed",
			[]string{"back-3 USB 3 = 1-1 / 2-1 (the OmaBoot? drive: the tester said which port)", "back-4 USB 3 = 1-2 / 2-2", "back-5 USB 3: marked",
				"back-6 USB 3 = 1-4 / 2-4", "1 port(s) that didn't work"}},
		{"usb-a on the iMac, the stick in another kind of port", "ports.usb-a", imac, 30, func(f *Fake) {
			DemoIMac(f, "ports.usb-c")
			f.At(3, func(f *Fake) { fakeUSB(f, "2-1", "0781:5583", "SanDisk", "Ultra Fit", "5000", true) })
			f.At(8, func(f *Fake) { fakeUSB(f, "1-3", "046d:c52b", "Logitech", "USB Receiver", "12", false) })
			f.At(10, func(f *Fake) { fakeUSB(f, "2-4", "0781:5583", "SanDisk", "Ultra Fit", "5000", true) })
		}, map[float64]int{6: 0}, "partial/observed", []string{"back-3 USB 3 = 1-1 / 2-1", "back-4 USB 3: marked", "back-5 USB 3 = 1-3 / 2-3", "back-6 USB 3 = 1-4 / 2-4", "not counted: 3-1 / 4-1"}},
		// an iMac's back mirrored as seen from the front (0.7.3 review): asked from the viewer's
		// left (B1 is back-6), and the evidence keeps the ports' IDs
		{"usb-a on the mirrored iMac", "ports.usb-a", imacMirrored, 30, onIMac("ports.usb-a"), map[float64]int{1: 0, 6: 0}, "partial/observed",
			[]string{"in the order asked: back-6 USB 3 = 1-1 / 2-1 (the OmaBoot? drive: the tester said which port)", "back-5 USB 3 = 1-2 / 2-2",
				"back-4 USB 3: marked", "back-3 USB 3 = 1-4 / 2-4"}},
		// USB-C on an iMac with both kinds (the iMac18,1's 0.8.5 run): the stick in a USB-A port
		// stays out; an iPhone in a USB-C port comes up on the main controller (1-10) and counts
		// for the port asked for; a stick on a USB-A port the USB-A test counted (1-2) doesn't
		{"usb-c on the iMac's main controller", "ports.usb-c", imacUSBA, 30, func(f *Fake) {
			DemoIMac(f, "ports.usb-a")
			f.At(1, func(f *Fake) { fakeUSB(f, "2-2", "0781:5583", "SanDisk", "Ultra Fit", "5000", true) })
			f.At(4, func(f *Fake) { fakeUSB(f, "1-10", "05ac:12a8", "Apple Inc.", "iPhone", "480", false) })
		}, map[float64]int{9: 0}, "partial/observed", []string{"not counted: 1-1 / 2-1, a USB-A port", "not counted: 1-2 / 2-2, a USB-A port",
			"1-10: Apple Inc. iPhone (05ac:12a8) at 480 Mb/s (USB 2)", "(on the Mac's main USB controller, not the Thunderbolt 3 chip's)", "Thunderbolt 3 (USB-C) = 1-10"}},
		// USB-C with Alpine Ridge's controller asleep (the iMac18,1's 0.8.8 run): nothing happens
		// for 15 s, the test keeps it awake, and a stick in B8 counts with the fix; B7 marked dead
		{"usb-c, the Thunderbolt 3 controller asleep", "ports.usb-c", imacUSBA, 30, func(f *Fake) { DemoIMac(f, "usb-c-asleep") },
			map[float64]int{16: 0}, "partial/observed", []string{"3-2 / 4-2: SanDisk Ultra Dual Drive USB-C", "(seen once the Thunderbolt 3 USB controller was kept awake)",
				"controller (0000:07:00.0 (8086:15b6)) was asleep (runtime-suspended) with nothing seen for 15 s"}},
		{"usb-c asleep, both ports", "ports.usb-c", imacUSBA, 30, func(f *Fake) {
			DemoIMac(f, "usb-c-asleep")
			f.At(20, func(f *Fake) { fakeUnplug(f, "4-2") })
			f.At(21, func(f *Fake) { fakeUSB(f, "4-1", "0781:5596", "SanDisk", "Ultra Dual Drive USB-C", "5000", true) })
		}, nil, "partial/automatic", []string{"kept awake from 16 s into the test"}},
		{"thunderbolt one port", "ports.thunderbolt", air, 10, demo("ports.thunderbolt"), nil, "supported/automatic", []string{"tg3", "right-3 Thunderbolt = route 0-1"}},
		{"thunderbolt 3, one dead", "ports.thunderbolt", imac, 30, onIMac("ports.thunderbolt"), map[float64]int{25: 0}, "partial/observed",
			[]string{"back-7: 0-1: Apple Inc. Thunderbolt 3 to Thunderbolt 2 Adapter", "back-7 Thunderbolt 3 (USB-C) = route 0-1", "back-8 Thunderbolt 3 (USB-C): marked"}},
		{"sd slot", "ports.sd-card", air, 8, demo("ports.sd-card"), nil, "supported/automatic", []string{"right-1 SD card = sdc on USB 2-1.3"}},
		// the tethering phone: asked for after the stick, among the ports left; unplugged and
		// plugged back into the same port (connect_type unknown) it counts there, not as the
		// Mac's own
		{"usb-a phone on the iMac", "ports.usb-a", imacPhone, 30, func(f *Fake) {
			DemoIMac(f, "ports.usb-c") // the stick in a Thunderbolt 3 port: only the phone's question
			fakeUSB(f, "1-3", "05ac:12a8", "Apple Inc.", "iPhone", "480", false)
			f.Set(usbDevices+"usb1/1-0:1.0/usb1-port3/connect_type", "unknown")
			f.At(3, func(f *Fake) { fakeUSB(f, "2-1", "0781:5583", "SanDisk", "Ultra Fit", "5000", true) })
			f.At(5, func(f *Fake) { fakeUnplug(f, "1-3") })
			f.At(6, func(f *Fake) { fakeUSB(f, "1-3", "05ac:12a8", "Apple Inc.", "iPhone", "480", false) })
			f.At(7, func(f *Fake) { fakeUSB(f, "1-2", "046d:c52b", "Logitech", "USB Receiver", "12", false) })
			f.At(9, func(f *Fake) { fakeUSB(f, "2-4", "0781:5583", "SanDisk", "Ultra Fit", "5000", true) })
		}, map[float64]int{1: 2}, "supported/automatic", []string{"back-3 USB 3 = 1-1 / 2-1", "back-4 USB 3 = 1-2 / 2-2",
			"back-5 USB 3 = 1-3 / 2-3 (the phone carrying the network: the tester said which port)", "back-6 USB 3 = 1-4 / 2-4",
			"iPhone (05ac:12a8) at 480 Mb/s (USB 2), then Apple Inc. iPhone"}},
		{"usb-a phone in the last port", "ports.usb-a", airPhone, 4, phoneOnAir, map[float64]int{1: 0}, "supported/automatic",
			[]string{"right-2 USB 3 = 3-2 / 4-2 (the phone carrying the network: the only port left)"}},
		{"usb-a phone, no map", "ports.usb-a", plainPhone, 4, phoneOnAir, map[float64]int{2: 0}, "supported/automatic",
			[]string{"ports seen: 2", "(the phone carrying the network, in since before the test)"}},
	}
	airWired := &Env{Ports: DemoAirPorts(), Wiring: map[string][]string{"left-2": {"usb2:0000:00:14.0/1", "usb3:0000:00:14.0/1"},
		"right-2": {"usb2:0000:00:14.0/2", "usb3:0000:00:14.0/2"}}}
	cases = append(cases, []struct {
		name, id string
		env      *Env
		until    float64
		script   func(*Fake)
		picks    map[float64]int
		want     string
		ev       []string
	}{
		// the wiring puts the stick on left-2: Enter (the answer under the cursor) confirms it
		{"usb-a wiring confirmed", "ports.usb-a", airWired, 8, demo("ports.usb-a"), map[float64]int{1: 0}, "supported/automatic",
			[]string{"left-2 USB 3 = 3-1 / 4-1 (the OmaBoot? drive: the tester confirmed DoesItOmarchy.com's wiring)"}},
		{"usb-a wiring corrected", "ports.usb-a", airWired, 8, demo("ports.usb-a"), map[float64]int{1: 1}, "supported/automatic",
			[]string{"(the OmaBoot? drive: the tester corrected DoesItOmarchy.com's wiring, which said left-2)"}},
	}...)
	// the answers for the wiring, "connector how paths…" (a case not here isn't checked)
	wantPorts := map[string][]string{
		"usb-a wiring confirmed": {"left-2 confirmed usb2:0000:00:14.0/1 usb3:0000:00:14.0/1", "right-2 ring usb2:0000:00:14.0/2 usb3:0000:00:14.0/2"},
		"usb-a wiring corrected": {"left-2 ring usb2:0000:00:14.0/2 usb3:0000:00:14.0/2", "right-2 corrected:left-2 usb2:0000:00:14.0/1 usb3:0000:00:14.0/1"},
		"usb-a both":             {"left-2 asked usb2:0000:00:14.0/1 usb3:0000:00:14.0/1", "right-2 ring usb2:0000:00:14.0/2 usb3:0000:00:14.0/2"},
		"thunderbolt one port":   {"right-3 ring tb:0-1"},
		"sd slot":                {"right-1 ring usb2:0000:00:1d.0/1.3"},
		"usb-a phone on the iMac": {"back-3 ring usb2:0000:00:14.0/1 usb3:0000:00:14.0/1", "back-4 ring usb2:0000:00:14.0/2 usb3:0000:00:14.0/2",
			"back-5 asked usb2:0000:00:14.0/3 usb3:0000:00:14.0/3", "back-6 ring usb2:0000:00:14.0/4 usb3:0000:00:14.0/4"},
	}
	for _, c := range cases {
		script := c.script
		if c.name == "usb-a both" {
			script = func(f *Fake) {
				c.script(f)
				pl := usbDevices + "usb4/4-0:1.0/usb4-port2/physical_location/"
				f.Set(pl+"panel", "right")
				f.Set(pl+"vertical_position", "center")
				f.Set(pl+"horizontal_position", "right")
				f.Set(pl+"dock", "no")
			}
		}
		mac := DemoMac
		if c.env == imac || c.env == imacPhone || c.env == imacMirrored {
			mac = func(*Fake) {}
		}
		o := playOn(t, c.env, mac, c.id, c.until, script, c.picks)
		if got := status(o); got != c.want {
			t.Errorf("%s: %s, want %s", c.name, got, c.want)
			continue
		}
		for _, e := range c.ev {
			if !strings.Contains(o.Results[0].Evidence, e) {
				t.Errorf("%s: evidence %q lacks %q", c.name, o.Results[0].Evidence, e)
			}
		}
		if wp, ok := wantPorts[c.name]; ok {
			var got []string
			for _, p := range o.Results[0].Ports {
				how := p.How
				if p.WiringSaid != "" {
					how += ":" + p.WiringSaid
				}
				got = append(got, strings.Join(append([]string{p.Connector, how}, p.Paths...), " "))
			}
			if strings.Join(got, "; ") != strings.Join(wp, "; ") {
				t.Errorf("%s: ports\n got %q\nwant %q", c.name, got, wp)
			}
		}
	}
}

// A port marked dead never lets the USB-A test pass, whenever it's marked and whatever shows up
// after: on the Air's map, the iMac's back, and without a map (then Done).
func TestUSBDeadNeverPasses(t *testing.T) {
	s, _ := Lookup("ports.usb-a")
	air, imac := &Env{Ports: DemoAirPorts()}, &Env{Ports: DemoIMacPorts()}
	onIMac := func(f *Fake) { DemoIMac(f, "ports.usb-a") }
	partial := map[string]int{}
	for at := 1.5; at <= 12; at += 0.5 {
		for _, c := range []struct {
			name   string
			env    *Env
			mac    func(*Fake)
			script func(*Fake)
			picks  map[float64]int
		}{
			{"air", air, DemoMac, s.Demo, map[float64]int{1: 0, at: 0}},
			{"imac", imac, func(*Fake) {}, onIMac, map[float64]int{1: 0, at: 0}},
			{"no map", &Env{}, DemoMac, s.Demo, map[float64]int{at: 1, 13: 0}},
		} {
			o := playOn(t, c.env, c.mac, "ports.usb-a", 30, c.script, c.picks)
			switch got := status(o); {
			case got == "partial/observed":
				partial[c.name]++
			case strings.HasPrefix(got, "supported") && !strings.Contains(o.Results[0].Evidence, "the tester tried every port"):
				t.Errorf("%s, marked at %.1f: %s with a port marked dead", c.name, at, got)
			}
		}
	}
	for _, n := range []string{"air", "imac", "no map"} { // the mark counted while a port was still asked for
		if partial[n] == 0 {
			t.Errorf("%s: never partial", n)
		}
	}
}
