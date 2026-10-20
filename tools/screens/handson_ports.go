package main

import (
	"github.com/omaboot/boot-live/internal/demo"
	"github.com/omaboot/boot-live/internal/handson"
	"github.com/omaboot/boot-live/internal/identify"
	"github.com/omaboot/boot-live/internal/ui"
)

// The port and Bluetooth tests' screens, played on the demo's MacBook Air (their Demo scripts
// in internal/handson/ports*.go and bluetooth.go) with its port map; the same Air without a map
// (the plain flow, with Done); and the iMac18,3's back (handson.DemoIMac).
func init() {
	answer := ui.Pick{At: 1, Action: 0}      // the OmaBoot? stick is in the left side's USB 3 port (the iMac's first)
	phonePort := ui.Pick{At: 1.6, Action: 1} // the phone is in the iMac's third USB-A port: the second of those left
	// online through a phone on USB tethering, in the iMac's third USB-A port
	iPhone := &handson.Phone{USB: "1-3", Name: "iPhone", Apple: true, Online: true}
	pixel := &handson.Phone{USB: "2-3", Name: "Pixel 7", Online: true}
	testShots = append(testShots, func() []shot {
		return []shot{
			playShot("h9-usb-a-stick-question", "ports.usb-a", 2.5, nil),
			playShot("h9-usb-a-plug-in-here", "ports.usb-a", 3, nil, answer),
			playShot("h9-usb-a-seen", "ports.usb-a", 4.5, nil, answer),
			playShot("h9-usb-a-pass", "ports.usb-a", 6, nil, answer),
			playShot("h9-usb-a-hint", "ports.usb-a", 22, nil), // 20 s with no key on the question: Boot's hint
			playShot("h9-usb-a-marked", "ports.usb-a", 2.6, nil, answer, ui.Pick{At: 2, Action: 0}),
			playShot("h9-usb-a-partial", "ports.usb-a", 4.5, nil, answer, ui.Pick{At: 2, Action: 0}),
			playShot("h9-usb-a-skipped", "ports.usb-a", 2.6, nil, ui.Pick{At: 2, Action: -1}),
			macShot("h9-usb-a-nomap-same-port", "ports.usb-a", 10.5, nil, nil),
			macShot("h9-usb-a-nomap-pass", "ports.usb-a", 12, nil, nil, ui.Pick{At: 11, Action: 0}),
			// the raw read (owner, 2026-10-13): a stick read twice, a read failing and retried, the
			// OmaBoot? stick moved with copytoram, a mouse counted by its moves
			playShot("h9-usb-a-read-twice", "ports.usb-a", 3.75, nil, answer),
			readShot("h9-usb-a-read-reading", "fail", 4.1, answer),
			readShot("h9-usb-a-read-failed", "fail", 7, answer),
			readShot("h9-usb-a-read-retried", "fail", 12.2, answer),
			readShot("h9-usb-a-read-retried-pass", "fail", 14, answer),
			readShot("h9-usb-a-ram-question", "ram", 0.6),
			readShot("h9-usb-a-ram-move", "ram", 4.5, answer),
			readShot("h9-usb-a-ram-checking", "ram", 6.9, answer),
			readShot("h9-usb-a-ram-pass", "ram", 10.5, answer),
			readShot("h9-usb-a-mouse-wait", "mouse", 4.6, answer),
			readShot("h9-usb-a-mouse-moved", "mouse", 6, answer),
			macShot("h9-usb-a-nomap-read-failed", "ports.usb-a", 7, nil, readIO("fail")),
			macShot("h9-usb-a-nomap-ram", "ports.usb-a", 6.9, nil, readIO("ram")),
			playShot("h9-usb-c-waking", "ports.usb-c", 4.8, nil),
			playShot("h9-usb-c-seen", "ports.usb-c", 7, nil),
			playShot("h9-usb-c-pass", "ports.usb-c", 9, nil, ui.Pick{At: 8, Action: 0}),
			playShot("h9-sd-waiting", "ports.sd-card", 2.5, nil),
			playShot("h9-sd-card", "ports.sd-card", 5.5, nil),
			playShot("h9-sd-pass", "ports.sd-card", 7.6, nil),
			playShot("h9-tb-waiting", "ports.thunderbolt", 2.5, nil),
			playShot("h9-tb-tunnel", "ports.thunderbolt", 5, nil),
			playShot("h9-tb-pass", "ports.thunderbolt", 8, nil),
			variantShot("h9-tb-net-wait", "ports.thunderbolt", 9, tbCarries),
			variantShot("h9-tb-net-back", "ports.thunderbolt", 13, tbCarries),
			variantShot("h9-tb-held", "ports.thunderbolt", 9, tbHeld),
			variantShot("h9-tb-held-long", "ports.thunderbolt", 40, tbHeld),
			imacShot("h9-imac-usb-a-question", "ports.usb-a", 2),
			imacShot("h9-imac-usb-a-waiting", "ports.usb-a", 2.4, answer),
			imacShot("h9-imac-usb-a-seen", "ports.usb-a", 4.6, answer),
			imacShot("h9-imac-usb-a-dead", "ports.usb-a", 7, answer, ui.Pick{At: 6, Action: 0}),
			imacShot("h9-imac-usb-a-partial", "ports.usb-a", 11.5, answer, ui.Pick{At: 6, Action: 0}),
			imacShot("h9-imac-usb-c-question", "ports.usb-c", 2),
			asleepShot("h9-imac-usb-c-asleep-waiting", 10),
			asleepShot("h9-imac-usb-c-asleep-woken", 16.5),
			asleepShot("h9-imac-usb-c-asleep-seen", 19.5),
			asleepShot("h9-imac-usb-c-asleep-result", 24, ui.Pick{At: 21, Action: 0}),
			imacShot("h9-imac-tb3-next", "ports.thunderbolt", 7),
			imacShot("h9-imac-tb3-dead", "ports.thunderbolt", 25, ui.Pick{At: 23, Action: 0}),
			phoneShot("h9-imac-usb-a-iphone-question", iPhone, 2, answer),
			phoneShot("h9-imac-usb-a-iphone-counted", iPhone, 2.8, answer, phonePort),
			phoneShot("h9-imac-usb-a-iphone-seen", iPhone, 4.5, answer, phonePort),
			phoneShot("h9-imac-usb-a-android-question", pixel, 2, answer),
			// with DoesItOmarchy.com's wiring for the Mac (LIVE-PLAN §23): the question starts on its port
			macShotOf("h9-usb-a-stick-wired", "ports.usb-a", 2.5, &handson.Env{Ports: handson.DemoAirPorts(), Wiring: airWiring}, nil, nil),
			wiredPhoneShot("h9-imac-usb-a-iphone-wired", iPhone, 2, answer),
			playShot("h9-bt-listening", "network.bluetooth", 2.5, nil),
			playShot("h9-bt-found", "network.bluetooth", 6.4, nil),
			playShot("h9-bt-pick", "network.bluetooth", 8, nil),
			playShot("h9-bt-pass", "network.bluetooth", 9, nil, ui.Pick{At: 8.2, Action: 1}), // the Pixel: the tester's own
		}
	})
}

// macShot is a test's screen on another Mac: its port map (nil: none) and, for each visit, its
// IO (nil: the demo's Air), with the frame naming mac (nil: the Air).
func macShot(name, id string, t float64, ports []identify.PortSide, io func(handson.Spec) handson.IO, picks ...ui.Pick) shot {
	return macShotOf(name, id, t, &handson.Env{Ports: ports}, io, nil, picks...)
}

func macShotOf(name, id string, t float64, env *handson.Env, io func(handson.Spec) handson.IO, mac *ui.MacInfo, picks ...ui.Pick) shot {
	setup := demo.HandsOnSetup()
	setup.Env = env
	if io != nil {
		setup.IO = io
	}
	h := ui.NewHandsOn(setup)
	i := -1
	for k, s := range h.Setup.Plan {
		if s.ID == id {
			i = k
		}
	}
	if i < 0 {
		panic("no hands-on test with a Demo script: " + id)
	}
	h.Play(i, t, picks...)
	s := st(ui.Test, t, wifiUp, func(s *ui.State) { s.HO = h })
	f := demo.Finished()
	f.HandsOn = h.Checks()
	if mac != nil {
		f.Mac = *mac
	}
	return shot{name: name, st: s, f: f}
}

// readIO is the demo Air for the raw read's screens (handson.DemoUSBRead's variants).
func readIO(variant string) func(handson.Spec) handson.IO {
	return func(handson.Spec) handson.IO {
		f := handson.NewFake()
		handson.DemoMac(f)
		handson.DemoUSBRead(f, variant)
		return f
	}
}

// readShot is the Air's USB-A test, with its map, in one of the raw read's variants.
func readShot(name, variant string, t float64, picks ...ui.Pick) shot {
	return macShotOf(name, "ports.usb-a", t, &handson.Env{Ports: handson.DemoAirPorts()}, readIO(variant), nil, picks...)
}

// imacShot is a test's screen on the iMac (Retina 5K, 27-inch, 2017) [iMac18,3].
func imacShot(name, id string, t float64, picks ...ui.Pick) shot {
	io := func(handson.Spec) handson.IO {
		f := handson.NewFake()
		handson.DemoIMac(f, id)
		return f
	}
	return macShotOf(name, id, t, &handson.Env{Ports: identify.MirrorBack(handson.DemoIMacPorts())}, io, &imacInfo, picks...)
}

// asleepShot is the iMac's USB-C test with Alpine Ridge's USB controller asleep: the test keeps
// it awake at 15 s, and a USB-C stick shows up in the second port (handson.DemoIMac).
func asleepShot(name string, t float64, picks ...ui.Pick) shot {
	io := func(handson.Spec) handson.IO {
		f := handson.NewFake()
		handson.DemoIMac(f, "usb-c-asleep")
		return f
	}
	return macShotOf(name, "ports.usb-c", t, &handson.Env{Ports: identify.MirrorBack(handson.DemoIMacPorts())}, io, &imacInfo, picks...)
}

var imacInfo = ui.MacInfo{Name: "iMac (Retina 5K, 27-inch, 2017)", ID: "iMac18,3", Config: "Core i5 3.8 GHz, Radeon Pro 580"}

// phoneShot is the iMac's USB-A test with the run online through phone on USB tethering: which
// port it's in, after which it counts like the stick's.
func phoneShot(name string, phone *handson.Phone, t float64, picks ...ui.Pick) shot {
	io := func(handson.Spec) handson.IO {
		f := handson.NewFake()
		handson.DemoIMac(f, "ports.usb-a")
		handson.DemoPhone(f, phone)
		return f
	}
	return macShotOf(name, "ports.usb-a", t, &handson.Env{Ports: identify.MirrorBack(handson.DemoIMacPorts()), Phone: phone}, io, &imacInfo, picks...)
}

// airWiring and imacWiring are DoesItOmarchy.com's wiring for the demo's Macs, as accepted
// reports would give it.
var (
	airWiring  = map[string][]string{"left-2": {"usb2:0000:00:14.0/1", "usb3:0000:00:14.0/1"}, "right-2": {"usb2:0000:00:14.0/2", "usb3:0000:00:14.0/2"}}
	imacWiring = map[string][]string{"back-3": {"usb2:0000:00:14.0/1", "usb3:0000:00:14.0/1"}, "back-4": {"usb2:0000:00:14.0/2", "usb3:0000:00:14.0/2"},
		"back-5": {"usb2:0000:00:14.0/3", "usb3:0000:00:14.0/3"}, "back-6": {"usb2:0000:00:14.0/4", "usb3:0000:00:14.0/4"}}
)

// wiredPhoneShot is phoneShot with the iMac's wiring known.
func wiredPhoneShot(name string, phone *handson.Phone, t float64, picks ...ui.Pick) shot {
	io := func(handson.Spec) handson.IO {
		f := handson.NewFake()
		handson.DemoIMac(f, "ports.usb-a")
		handson.DemoPhone(f, phone)
		return f
	}
	return macShotOf(name, "ports.usb-a", t, &handson.Env{Ports: identify.MirrorBack(handson.DemoIMacPorts()), Phone: phone, Wiring: imacWiring}, io, &imacInfo, picks...)
}

// tbCarries is the demo Mac online through en1 on the Thunderbolt to Gigabit Ethernet adapter
// the demo plugs in (0000:0a:00.0): unplugged at 1 s, its interface back at 6 s, the status
// line online through it again at 12 s; the result waits for that (ports_tb.go).
// tbHeld is the Thunderbolt device held back by the kernel (domain security "user", as
// linux-omarchy 7.2.8 has it on the MacBookAir5,2): no PCI devices until it's let in.
func tbHeld(f *handson.Fake) {
	tb := "/sys/bus/thunderbolt/devices/"
	f.Set(tb+"domain0/security", "user")
	f.Set(tb+"0-0/device_name", "MacBook Air")
	f.Set(tb+"0-0/vendor_name", "Apple Inc.")
	f.Set(tb+"0-0/authorized", "1")
	f.Set(tb+"0-0/generation", "1")
	f.At(4, func(f *handson.Fake) {
		f.Set(tb+"0-1/device_name", "Thunderbolt to Gigabit Ethernet Adapter")
		f.Set(tb+"0-1/vendor_name", "Apple, Inc.")
		f.Set(tb+"0-1/authorized", "0")
		f.Set(tb+"0-1/rx_speed", "10.0 Gb/s")
		f.Set(tb+"0-1/rx_lanes", "1")
	})
}

func tbCarries(f *handson.Fake) {
	s, _ := handson.Lookup("ports.thunderbolt")
	s.Demo(f) // its device and PCI devices, at 4 and 5.5 s
	f.Set("/fake/net/online", "en1, Ethernet")
	f.Set("/sys/class/net/en1/carrier", "1")
	handson.FakeLink(f, "/sys/class/net/en1/device", "../../../0000:0a:00.0")
	f.At(1, func(f *handson.Fake) {
		delete(f.Files, "/sys/class/net/en1/carrier")
		delete(f.Files, "link:/sys/class/net/en1/device")
		f.Set("/fake/net/offline", "en1: gone")
	})
	f.At(6, func(f *handson.Fake) {
		f.Set("/sys/class/net/en1/carrier", "1")
		handson.FakeLink(f, "/sys/class/net/en1/device", "../../../0000:0a:00.0")
	})
	f.SetAt(12, "/fake/net/offline", "")
}
