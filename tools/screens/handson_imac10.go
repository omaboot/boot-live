package main

import (
	"github.com/omaboot/boot-live/internal/demo"
	"github.com/omaboot/boot-live/internal/handson"
	"github.com/omaboot/boot-live/internal/identify"
	"github.com/omaboot/boot-live/internal/ui"
)

// The iMac10,1's tests (0.7.2 review → 0.7.3): its USB-A and SD tests drawn as a desktop (its
// back; the SD slot on its right edge), FireWire on its FireWire 800 port, the optical drive's
// band in each state, and the light sensor whose level is stuck while the SMC's raw bytes follow
// the hand. Played on the demo's scripted Mac with the iMac10,1's port map and frame.
func init() {
	answer := ui.Pick{At: 1, Action: 0}
	testShots = append(testShots, func() []shot {
		return []shot{
			imac10Shot("h9-imac10-usb-a-question", "ports.usb-a", 2, nil),
			imac10Shot("h9-imac10-usb-a-seen", "ports.usb-a", 4.6, nil, answer),
			imac10Shot("h9-imac10-sd-waiting", "ports.sd-card", 2.5, nil),
			imac10Shot("h9-imac10-sd-pass", "ports.sd-card", 7.6, nil),
			imac10Shot("h9-imac10-usb-a-skipped", "ports.usb-a", 2.6, nil, ui.Pick{At: 2, Action: -1}),
			imac10Shot("h9f-firewire-waiting", "ports.firewire", 2.5, nil),
			imac10Shot("h9f-firewire-seen", "ports.firewire", 5.2, nil),
			imac10Shot("h9f-firewire-pass", "ports.firewire", 7, nil),
			imac10Shot("h9f-firewire-nothing", "ports.firewire", 17, fwNone, ui.Pick{At: 16, Action: 0}),
			imac10Shot("h9f-firewire-skipped", "ports.firewire", 2.6, nil, ui.Pick{At: 2, Action: -1}),
			imac10Shot("h22-optical-waiting", "storage.optical", 2.2, nil),
			imac10Shot("h22-optical-spinning", "storage.optical", 3.6, nil),
			imac10Shot("h22-optical-reading", "storage.optical", 5.4, nil),
			imac10Shot("h22-optical-ejecting", "storage.optical", 6.6, nil),
			imac10Shot("h22-optical-pass", "storage.optical", 9, nil),
			imac10Shot("h22-optical-audio-pass", "storage.optical", 7.6, disc("audio")),
			imac10Shot("h22-optical-tray", "storage.optical", 2.2, disc("tray")),
			imac10Shot("h22-optical-retry", "storage.optical", 10, disc("bad")),
			imac10Shot("h22-optical-failed", "storage.optical", 11, disc("bad"), ui.Pick{At: 10.4, Action: 0}),
			imac10Shot("h22-optical-nothing", "storage.optical", 27, disc("none"), ui.Pick{At: 26, Action: 0}),
			imac10Shot("h22-optical-skipped", "storage.optical", 2.6, disc("none"), ui.Pick{At: 2, Action: -1}),
			// 0.7.4: "Eject disc" in the answers (greyed while reading), the eject checked, a disc
			// that won't come out, and the sleep test held while a disc is in the drive
			imac10Shot("h22-optical-eject-menu", "storage.optical", 2.4, disc("none"), ui.Pick{At: 2, Action: 1}),
			imac10Shot("h22-optical-stuck", "storage.optical", 9, disc("stuck")),
			imac10Shot("h22-optical-skipped-reading", "storage.optical", 5.4, nil, ui.Pick{At: 5, Action: -1}),
			imac10Shot("h23-sleep-disc-in", "power.sleep-wake", 2, withDisc("power.sleep-wake")),
			imac10Shot("h23-sleep-disc-ejecting", "power.sleep-wake", 2.8, withDisc("power.sleep-wake"), ui.Pick{At: 2.2, Action: 1}),
			imac10Shot("h23-sleep-disc-out", "power.sleep-wake", 5, withDisc("power.sleep-wake"), ui.Pick{At: 2.2, Action: 1}),
			// 0.7.6: no nap with a FireWire disk attached (it hung the iMac10,1's sleep); unplugged,
			// Start comes back; a camera on the bus doesn't hold it
			imac10Shot("h25-sleep-firewire-disk", "power.sleep-wake", 2, fwDisk("power.sleep-wake", 0, false)),
			imac10Shot("h25-sleep-firewire-unplugged", "power.sleep-wake", 5.5, fwDisk("power.sleep-wake", 4, false)),
			imac10Shot("h25-sleep-firewire-camera", "power.sleep-wake", 2, fwDisk("power.sleep-wake", -1, true)),
			imac10Shot("h25-sleep-firewire-and-disc", "power.sleep-wake", 2, func(f *handson.Fake) {
				fwDisk("power.sleep-wake", 0, false)(f)
				handson.DemoDisc(f, "in")
			}),
			// 0.7.5: picked up after deep sleep hung, Sleep and wake offers light sleep (s2idle)
			imac10HungShot("h24-sleep-after-hang", 2, nil),
			imac10HungShot("h24-sleep-after-hang-light", 7, nil, ui.Pick{At: 3, Action: 0}),
			imac10HungShot("h24-sleep-after-hang-light-pass", 13.5, nil, ui.Pick{At: 3, Action: 0}, ui.Pick{At: 12.6, Code: demoCode}),
			imac10HungShot("h24-sleep-after-hang-deep-only", 2, func(f *handson.Fake) { f.Set("/sys/power/mem_sleep", "[deep]"); f.Set("/sys/power/state", "mem disk") }),
			imac10Shot("h3b-lightsensor-nothing-moved", "input.ambient-light-sensor", 15.5, func(f *handson.Fake) {
				f.Set("/sys/bus/acpi/devices/APP0001:00/light", "(18,0)")
				handson.DemoSMC(f, 18)
			}, ui.Pick{At: 15, Action: 1}),
			imac10Shot("h3b-lightsensor-raw-moved", "input.ambient-light-sensor", 14.5, handson.DemoLightStuck),
			imac10Shot("h3b-lightsensor-raw-failed", "input.ambient-light-sensor", 15.5, handson.DemoLightStuck, ui.Pick{At: 15, Action: 1}),
		}
	})
}

var imac10Info = ui.MacInfo{Name: "iMac (21.5-inch, Late 2009)", ID: "iMac10,1", Config: "Core 2 Duo 3.06 GHz, Radeon HD 4670"}

// disc is the demo drive with another disc (handson.DemoDisc's kinds).
func disc(kind string) func(*handson.Fake) {
	return func(f *handson.Fake) { handson.DemoDisc(f, kind) }
}

// withDisc is test id's own script with a disc in the drive from the start.
func withDisc(id string) func(*handson.Fake) {
	s, _ := handson.Lookup(id)
	return func(f *handson.Fake) {
		s.Demo(f)
		handson.DemoDisc(f, "in")
	}
}

// fwDisk is test id's own script with the FireWire disk on the bus (handson.DemoFWDisk).
func fwDisk(id string, gone float64, camera bool) func(*handson.Fake) {
	s, _ := handson.Lookup(id)
	return func(f *handson.Fake) {
		s.Demo(f)
		handson.DemoFWDisk(f, gone, camera)
	}
}

// fwNone is the FireWire bus with nothing coming.
func fwNone(f *handson.Fake) { f.Set("/sys/bus/firewire/devices/fw0/is_local", "1") }

// imac10Shot is test id on the iMac10,1 at t: the plan its port map and drive give, each visit on
// the demo's scripted Mac with the test's script, or with script instead when set.
func imac10Shot(name, id string, t float64, script func(*handson.Fake), picks ...ui.Pick) shot {
	return imac10EnvShot(name, id, t, script, nil, picks...)
}

// imac10HungShot is Sleep and wake on the iMac10,1 when the run was picked up after deep sleep
// hung (Env.Hung), the Mac offering s2idle besides deep unless more changes that.
func imac10HungShot(name string, t float64, more func(*handson.Fake), picks ...ui.Pick) shot {
	s, _ := handson.Lookup("power.sleep-wake")
	script := func(f *handson.Fake) {
		s.Demo(f)
		f.Set("/sys/power/mem_sleep", "s2idle [deep]")
		f.Set("/sys/power/state", "freeze mem disk")
		if more != nil {
			more(f)
		}
	}
	hung := handson.NoComeback(&handson.Env{}, s, "boot-live 0.7.5 dev, 2026-10-15 21:02 UTC")
	return imac10EnvShot(name, "power.sleep-wake", t, script, func(e *handson.Env) {
		e.Hung = map[string]handson.Outcome{"power.sleep-wake": hung}
	}, picks...)
}

func imac10EnvShot(name, id string, t float64, script func(*handson.Fake), mod func(*handson.Env), picks ...ui.Pick) shot {
	env := &handson.Env{Ports: identify.MirrorBack(handson.DemoIMac10Ports()), Optical: true}
	if mod != nil {
		mod(env)
	}
	setup := demo.HandsOnSetup()
	setup.Env, setup.Plan = env, nil
	for _, s := range handson.Plan(env) {
		if s.Demo != nil {
			setup.Plan = append(setup.Plan, s)
		}
	}
	setup.IO = func(s handson.Spec) handson.IO {
		f := handson.NewFake()
		handson.DemoMac(f)
		if script != nil && s.ID == id {
			script(f)
		} else {
			s.Demo(f)
		}
		return f
	}
	h := ui.NewHandsOn(setup)
	i := -1
	for k, s := range h.Setup.Plan {
		if s.ID == id {
			i = k
		}
	}
	if i < 0 {
		panic("no hands-on test on the iMac10,1: " + id)
	}
	h.Play(i, t, picks...)
	s := st(ui.Test, t, wifiUp, func(s *ui.State) { s.HO = h })
	f := demo.Finished()
	f.HandsOn = h.Checks()
	f.Mac = imac10Info
	return shot{name: name, st: s, f: f}
}
