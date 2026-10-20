package handson

import (
	"path"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Fake is a scripted Mac for the demo, tools/screens and the unit tests: files in memory, a
// script of changes at set times (a charger unplugged at 3 s), scripted input events, and a
// suspend that comes back after a moment with a made-up Sleep. Its clock is the run's: Advance
// plays everything due by then, so a test stepped through the same times always draws the same.
type Fake struct {
	Files  map[string]string
	Writes []string            // every write, "path=value", in order
	Disks  map[string]FakeDisk // how each disk reads (diskread_fake.go); a disk not here reads fine
	// SMC are the keys of the SMC in index order, as applesmc's key_at_index gives them (smc.go)
	SMC []FakeKey
	// Drive is the optical drive (optical.go), nil for none
	Drive *FakeDrive

	SleepAs    Sleep   // what Suspend answers
	SleepFor   float64 // run seconds until it answers
	NetUp      bool    // the network is up (NetNow; "/fake/net/offline" set takes it down meanwhile)
	Inhibited  int     // InhibitLid calls not yet released
	now        float64
	script     []fakeStep
	sounds     []*fakePlayer // what Play started (audio_alsa.go): the microphone may hear it
	events     []fakeEvent
	sleepUntil float64
}

type fakeStep struct {
	at   float64
	fn   func(*Fake)
	done bool
}

type fakeEvent struct {
	at float64
	ev Event
}

// NewFake is an empty Mac.
func NewFake() *Fake {
	return &Fake{Files: map[string]string{}, Disks: map[string]FakeDisk{}, SleepFor: 2, NetUp: true,
		SleepAs: Sleep{Mode: "deep", Asked: 20 * time.Second, Slept: 20 * time.Second, Took: 22 * time.Second, Success: 1}}
}

// Set writes a file.
func (f *Fake) Set(p, v string) { f.Files[p] = v }

// At runs fn once the run's clock reaches t.
func (f *Fake) At(t float64, fn func(*Fake)) { f.script = append(f.script, fakeStep{at: t, fn: fn}) }

// SetAt changes a file at t.
func (f *Fake) SetAt(t float64, p, v string) { f.At(t, func(f *Fake) { f.Set(p, v) }) }

// Event queues an input event on a device (event name) at t.
func (f *Fake) Event(t float64, dev string, typ, code uint16, value int32) {
	f.events = append(f.events, fakeEvent{t, Event{Dev: dev, Type: typ, Code: code, Value: value}})
}

// Device adds an input device: its sysfs name and capabilities ("sw" → bit words as sysfs
// writes them).
func (f *Fake) Device(event, name string, caps map[string]string) {
	d := "/sys/class/input/" + event + "/device/"
	f.Set(d+"name", name)
	for k, v := range caps {
		f.Set(d+"capabilities/"+k, v)
	}
}

func (f *Fake) Advance(t float64) {
	f.now = t
	for i := range f.script {
		if s := &f.script[i]; !s.done && s.at <= t {
			s.done = true
			s.fn(f)
		}
	}
}

func (f *Fake) Real() bool { return false }

func (f *Fake) Read(p string) string { return strings.TrimSpace(f.Files[p]) }

func (f *Fake) Bytes(p string) []byte {
	v, ok := f.Files[p]
	if !ok {
		return nil
	}
	return []byte(v)
}

func (f *Fake) Int(p string) (int, bool) {
	n, err := strconv.Atoi(f.Read(p))
	return n, err == nil
}

// Glob matches the files and the directories they imply.
func (f *Fake) Glob(pattern string) []string {
	seen := map[string]bool{}
	var out []string
	for p := range f.Files {
		for q := p; q != "/" && q != "."; q = path.Dir(q) {
			if seen[q] {
				continue
			}
			seen[q] = true
			if ok, _ := path.Match(pattern, q); ok {
				out = append(out, q)
			}
		}
	}
	sort.Strings(out)
	return out
}

func (f *Fake) Write(p, v string) error {
	f.Writes = append(f.Writes, p+"="+v)
	f.Files[p] = v
	if strings.HasSuffix(p, "/key_at_index") {
		f.fakeSMC(p, v)
	}
	// a backlight follows what is written to it
	if strings.HasSuffix(p, "/brightness") && strings.HasPrefix(p, "/sys/class/backlight/") {
		f.Files[strings.TrimSuffix(p, "brightness")+"actual_brightness"] = v
	}
	return nil
}

func (f *Fake) Inputs() []Device { return devices(f) }

func (f *Fake) Open(d Device, _ bool) (Input, error) { return &fakeInput{f: f, dev: d.Event}, nil }

type fakeInput struct {
	f    *Fake
	dev  string
	next int // events before this index are delivered
	shut bool
}

func (in *fakeInput) Events() []Event {
	if in.shut {
		return nil
	}
	var out []Event
	for ; in.next < len(in.f.events); in.next++ {
		e := in.f.events[in.next]
		if e.at > in.f.now {
			break
		}
		if e.ev.Dev == in.dev {
			out = append(out, e.ev)
		}
	}
	return out
}

func (in *fakeInput) Err() error { return nil }
func (in *fakeInput) Close()     { in.shut = true }

// timed answers once the run's clock reaches at.
type timed[T any] struct {
	f  *Fake
	at float64
	v  T
}

func (t timed[T]) Ready() (T, bool) {
	if t.f.now >= t.at {
		return t.v, true
	}
	var z T
	return z, false
}

func (f *Fake) Suspend(wake time.Duration, how SleepHow) Future[Sleep] {
	s := f.SleepAs
	s.Asked = wake
	if how.Light {
		s.Mode, s.Light = "s2idle", true
	} else if how.Deep {
		s.Mode = "deep"
	}
	f.sleepUntil = f.now + f.SleepFor
	return timed[Sleep]{f, f.sleepUntil, s}
}

// NetNow is the Fake's network: up through "/fake/net/online" ("wlan0, Wi-Fi" when unset)
// while NetUp holds and "/fake/net/offline" is empty (a script takes the link away and gives it
// back with SetAt: a Thunderbolt adapter unplugged, re-enumerating after a wake).
func (f *Fake) NetNow() Net {
	if !f.NetUp || f.Read(fakeNet+"offline") != "" {
		why := f.Read(fakeNet + "offline")
		if why == "" || why == "1" {
			why = "no default route"
		}
		return Net{Why: why}
	}
	via := f.Read(fakeNet + "online")
	if via == "" {
		via = "wlan0, Wi-Fi"
	}
	return Net{Up: true, Via: via}
}

func (f *Fake) InhibitLid() func() {
	f.Inhibited++
	done := false
	return func() {
		if !done {
			done = true
			f.Inhibited--
		}
	}
}

// DemoMac is the demo's MacBook Air (13-inch, Mid 2012) [MacBookAir5,2] as the hands-on tests
// read it: a backlight, the keyboard's light, the charger and battery, the display connectors,
// the lid switch, the RTC and the sleep states, the network and USB devices. Each test's Demo
// adds what changes during it.
func DemoMac(f *Fake) {
	f.Set("/sys/bus/acpi/devices/PNP0C0D:00/path", `\_SB_.LID0`) // the lid's ACPI device (lidwake.go)
	bl := "/sys/class/backlight/intel_backlight/"
	f.Set(bl+"type", "raw")
	f.Set(bl+"max_brightness", "4882")
	f.Set(bl+"brightness", "3400")
	f.Set(bl+"actual_brightness", "3400")
	kb := "/sys/class/leds/smc::kbd_backlight/"
	f.Set(kb+"max_brightness", "255")
	f.Set(kb+"brightness", "40")
	f.Set("/sys/class/power_supply/ADP1/type", "Mains")
	f.Set("/sys/class/power_supply/ADP1/online", "1")
	bat := "/sys/class/power_supply/BAT0/"
	f.Set(bat+"type", "Battery")
	f.Set(bat+"present", "1")
	f.Set(bat+"status", "Charging")
	f.Set(bat+"capacity", "87")
	f.Set(bat+"charge_now", "4437000") // 87 % of full; full is 88 % of design (demo.go's 412 cycles)
	f.Set(bat+"charge_full", "5100000")
	f.Set(bat+"charge_full_design", "5800000")
	f.Set(bat+"cycle_count", "412")
	for _, c := range []struct{ name, status, modes string }{{"eDP-1", "connected", "1440x900"}, {"DP-1", "disconnected", ""}, {"DP-2", "disconnected", ""}} {
		d := "/sys/class/drm/card1-" + c.name + "/"
		f.Set(d+"status", c.status)
		f.Set(d+"modes", c.modes)
		f.Set(d+"enabled", map[bool]string{true: "enabled", false: "disabled"}[c.status == "connected"])
	}
	f.Device("event0", "Lid Switch", map[string]string{"ev": "21", "sw": "1"})
	f.Device("event1", "Power Button", map[string]string{"ev": "3", "key": "10000000000000 0"})
	f.Device("event2", "Apple Inc. Apple Internal Keyboard / Trackpad", map[string]string{"ev": "120013", "key": "1000000000007 ff9f207ac14057ff febeffdfffefffff fffffffffffffffe"})
	f.Device("event3", "bcm5974", map[string]string{"ev": "b", "key": "e520 10000 0 0 0 0", "abs": "6730000 11000003"})
	f.Set("/proc/acpi/button/lid/LID0/state", "state:      open")
	f.Set("/sys/class/rtc/rtc0/wakealarm", "")
	f.Set("/sys/power/state", "freeze mem")
	f.Set("/sys/power/mem_sleep", "s2idle [deep]")
	f.Set("/sys/power/suspend_stats/success", "0")
	f.Set("/sys/power/suspend_stats/fail", "0")
	for _, n := range []string{"lo", "wlan0"} {
		f.Set("/sys/class/net/"+n+"/operstate", "up")
	}
	for _, u := range []string{"usb1", "usb2", "2-1", "2-1.1", "2-1.2", "2-1.3", "1-1", "1-2"} {
		f.Set("/sys/bus/usb/devices/"+u+"/idVendor", "05ac")
	}
	// the EHCI controllers the internal hubs hang off (the card reader's, the keyboard's)
	for b, pci := range map[string]string{"1": "0000:00:1a.0", "2": "0000:00:1d.0"} {
		FakeLink(f, "/sys/bus/usb/devices/usb"+b, "../../../devices/pci0000:00/"+pci+"/usb"+b)
		f.Set("/sys/bus/usb/devices/usb"+b+"/speed", "480")
	}
}
