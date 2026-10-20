package handson

import (
	"strings"
	"testing"
	"time"

	"github.com/omaboot/boot-live/internal/checks"
	"github.com/omaboot/boot-live/internal/identify"
	"github.com/omaboot/boot-live/internal/sysroot"
)

// fixtureEnv is the hands-on tests' environment on a fixture tree, as the real mode makes it.
func fixtureEnv(t *testing.T, dir string) *Env {
	t.Helper()
	cat, err := identify.Embedded()
	if err != nil {
		t.Fatal(err)
	}
	r := sysroot.Root("../../testdata/" + dir)
	h := identify.Read(r)
	e := &checks.Env{Root: r, HW: h, Ident: cat.Identify(h), Cat: cat, Kmsg: checks.ReadKmsg(r)}
	e.Fixes = checks.DetectFixes(r, e.Kmsg, cat)
	return &Env{Checks: e}
}

// Which built tests each Mac gets: only criteria that apply and that a live boot may decide.
func TestPlanFixtures(t *testing.T) {
	cases := map[string]string{
		"macbookair5,2": "input.keyboard input.trackpad display.native-resolution display.brightness input.keyboard-backlight input.keyboard-backlight.keys input.ambient-light-sensor audio.speakers audio.microphone camera.builtin network.bluetooth network.wifi " +
			"power.charging graphics.external-display audio.display-out audio.headphone ports.usb-a ports.sd-card ports.thunderbolt power.lid power.sleep-wake",
		"imac10,1": "display.native-resolution display.brightness input.ambient-light-sensor audio.speakers audio.microphone camera.builtin network.bluetooth network.wifi " +
			"network.ethernet graphics.external-display audio.display-out audio.headphone ports.usb-a ports.sd-card ports.firewire storage.optical power.sleep-wake",
		"imac18,3": "display.native-resolution display.brightness input.ambient-light-sensor audio.speakers audio.microphone camera.builtin network.bluetooth network.wifi " +
			"network.ethernet graphics.external-display audio.display-out audio.headphone ports.usb-a ports.usb-c ports.sd-card ports.thunderbolt power.sleep-wake",
	}
	for dir, want := range cases {
		var got []string
		for _, s := range Plan(fixtureEnv(t, dir)) {
			got = append(got, s.ID)
		}
		if strings.Join(got, " ") != want {
			t.Errorf("%s: plan %v, want %s", dir, got, want)
		}
	}
}

// Picks for a challenge's answer: the right one, or a wrong one (proof.go's challenges).
const (
	pickRight = -2
	pickWrong = -3
)

// answer is the index of the right (or a wrong) answer to the challenge on screen.
func answer(r *Runner, right bool) int {
	n := 0
	switch x := r.Run.(type) {
	case *light: // Show it again, then 2, 3 or 4 blinks
		n = x.blinks - 1
		if right {
			return n
		}
		return 1 + n%3
	case *native:
		n = x.pats[x.round]
		if right {
			return n
		}
		return (n + 1) % patterns
	}
	if right {
		return n
	}
	return (n + 1) % 3
}

// play runs a test against the demo Mac plus its script to until, with answers at given times
// (-1: skip; pickRight, pickWrong: a challenge's answer), and returns its outcome.
func play(t *testing.T, id string, until float64, script func(*Fake), picks map[float64]int) *Outcome {
	t.Helper()
	return playOn(t, &Env{}, DemoMac, id, until, script, picks)
}

// playOn is play on another Mac: env (its port map) and mac, its files before the script.
func playOn(t *testing.T, env *Env, mac func(*Fake), id string, until float64, script func(*Fake), picks map[float64]int) *Outcome {
	t.Helper()
	s, ok := Lookup(id)
	if !ok {
		t.Fatalf("no test %s", id)
	}
	f := NewFake()
	mac(f)
	if script != nil {
		script(f)
	}
	r := Begin(s, env, f)
	defer r.Stop()
	for tt := 0.0; tt <= until; tt += 0.1 {
		for at, a := range picks {
			if at > tt-0.1 && at <= tt {
				switch {
				case (a == pickRight || a == pickWrong) && r.Codes(): // a typed code (code_test.go)
					typeCode(r, a == pickRight)
				case a == pickRight || a == pickWrong:
					r.Act(answer(r, a == pickRight))
				case a < 0:
					r.Skip()
				default:
					r.Act(a)
				}
			}
		}
		r.Step(tt)
		if o := r.Outcome(); o != nil {
			return o
		}
	}
	return nil
}

func status(o *Outcome) string {
	if o == nil || len(o.Results) == 0 {
		return "undecided"
	}
	x := o.Results[0]
	if x.Status == checks.NotTested {
		return x.Status + "/" + x.Reason
	}
	return x.Status + "/" + x.Method
}

// The state machines' rules, on scripted Macs.
func TestRules(t *testing.T) {
	demo := func(id string) func(*Fake) {
		s, _ := Lookup(id)
		return s.Demo
	}
	cases := []struct {
		name, id string
		until    float64
		script   func(*Fake)
		picks    map[float64]int
		want     string
	}{
		{"charging follows", "power.charging", 15, demo("power.charging"), nil, "supported/automatic"},
		{"charging, battery stuck", "power.charging", 40, func(f *Fake) {
			f.SetAt(3, "/sys/class/power_supply/ADP1/online", "0")
			f.SetAt(6, "/sys/class/power_supply/ADP1/online", "1")
		}, nil, "partial/automatic"},
		{"charging, nothing changed", "power.charging", 15, nil, map[float64]int{12: 0}, "failed/observed"},
		{"charging skipped", "power.charging", 5, nil, map[float64]int{2: -1}, "not_tested/no-equipment"},
		{"lid sleeps and wakes", "power.lid", 15, demo("power.lid"), nil, "supported/automatic"},
		{"lid wakes only by the backstop", "power.lid", 15, func(f *Fake) {
			f.SleepAs.Slept, f.SleepFor = 60*time.Second, 3
			f.SetAt(4, "/proc/acpi/button/lid/LID0/state", "state:      closed")
			f.SetAt(7, "/proc/acpi/button/lid/LID0/state", "state:      open")
		}, nil, "partial/automatic"},
		{"lid, the Mac won't sleep", "power.lid", 15, func(f *Fake) {
			f.SleepAs.Err, f.SleepAs.Slept = ErrNotReal, 0
			f.SetAt(4, "/proc/acpi/button/lid/LID0/state", "state:      closed")
			f.SetAt(9, "/proc/acpi/button/lid/LID0/state", "state:      open")
		}, nil, "partial/automatic"},
		{"lid, no switch", "power.lid", 1, func(f *Fake) {
			delete(f.Files, "/proc/acpi/button/lid/LID0/state")
			delete(f.Files, "/sys/class/input/event0/device/capabilities/sw")
		}, nil, "failed/automatic"},
		{"lid skipped", "power.lid", 2, nil, map[float64]int{1: -1}, "not_tested/other"},
		{"display dark", "graphics.external-display", 10, demo("graphics.external-display"), map[float64]int{8: cardDark}, "partial/observed"}, // more in extdisplay_test.go
		{"display never came up", "graphics.external-display", 20, nil, map[float64]int{16: 0}, "failed/observed"},
		{"brightness seen", "display.brightness", 9, nil, map[float64]int{7: 0}, "supported/observed"},
		{"brightness, no device", "display.brightness", 1, func(f *Fake) {
			for p := range f.Files {
				if strings.HasPrefix(p, "/sys/class/backlight/") {
					delete(f.Files, p)
				}
			}
		}, nil, "failed/automatic"},
		{"sleep", "power.sleep-wake", 14, demo("power.sleep-wake"), map[float64]int{2: 0, 12: pickRight}, "supported/challenge"},
		{"sleep, wakes early", "power.sleep-wake", 14, func(f *Fake) { f.SleepAs.Slept = 3 * time.Second }, map[float64]int{2: 0, 12: pickRight}, "partial/automatic"},
		{"sleep, woken by the lid's wake: naps again with it off", "power.sleep-wake", 30, func(f *Fake) {
			f.Set("/sys/bus/acpi/devices/PNP0C0D:00/path", `\_SB_.LID0`)
			f.Set("/proc/acpi/button/lid/LID0/state", "state:      open")
			f.Set("/proc/acpi/wakeup", "LID0\t  S4\t*enabled   platform:PNP0C0D:00")
			f.SleepAs.Slept, f.SleepAs.WokenBy = 3*time.Second, []string{"LID0 (PNP0C0D:00)"}
		}, map[float64]int{2: 0, 24: pickRight}, "partial/automatic"},
		{"light sensor follows the cover", "input.ambient-light-sensor", 10, demo("input.ambient-light-sensor"), nil, "supported/automatic"},
		{"keyboard light: blinks counted", "input.keyboard-backlight", 12, nil, map[float64]int{11: pickRight}, "supported/challenge"},
		{"lid wakes by the lid's source", "power.lid", 15, demo("power.lid"), nil, "supported/automatic"},

		// false-pass routes (owner, 2026-10-13): none of these may come out supported
		{"light sensor stuck", "input.ambient-light-sensor", 20, func(f *Fake) { f.Set("/sys/bus/acpi/devices/APP0001:00/light", "(120,0)") },
			map[float64]int{15: 1}, "failed/observed"},
		{"light sensor stuck at 0, flashlight", "input.ambient-light-sensor", 20, func(f *Fake) { f.Set("/sys/bus/acpi/devices/APP0001:00/light", "(0,0)") },
			map[float64]int{15: 1}, "failed/observed"},
		{"light sensor stuck, no answer", "input.ambient-light-sensor", 30, func(f *Fake) { f.Set("/sys/bus/acpi/devices/APP0001:00/light", "(120,0)") },
			nil, "undecided"},
		{"firewire: a drive shows up", "ports.firewire", 8, demo("ports.firewire"), nil, "supported/automatic"},
		{"firewire: nothing shows up", "ports.firewire", 17, func(f *Fake) { f.Set(fwBus+"fw0/is_local", "1") }, map[float64]int{16: 0}, "failed/observed"},
		{"firewire skipped", "ports.firewire", 2, nil, map[float64]int{1: -1}, "not_tested/no-equipment"},
		{"disc: a DVD", "storage.optical", 10, func(f *Fake) { DemoDisc(f, "data") }, nil, "supported/automatic"},
		{"disc: a music CD", "storage.optical", 10, func(f *Fake) { DemoDisc(f, "audio") }, nil, "supported/automatic"},
		{"disc: a bad one, then a good one", "storage.optical", 20, func(f *Fake) { DemoDisc(f, "bad") }, nil, "supported/automatic"},
		{"disc: a bad one, no other", "storage.optical", 11, func(f *Fake) { DemoDisc(f, "bad") }, map[float64]int{10: 0}, "failed/automatic"},
		{"disc: the drive never sees it", "storage.optical", 27, func(f *Fake) { DemoDisc(f, "none") }, map[float64]int{26: 0}, "failed/observed"},
		{"disc: the tray", "storage.optical", 12, func(f *Fake) { DemoDisc(f, "tray") }, nil, "supported/automatic"},
		{"disc skipped", "storage.optical", 2, func(f *Fake) { DemoDisc(f, "none") }, map[float64]int{1: -1}, "not_tested/no-equipment"},
		{"disc: reads, won't come out", "storage.optical", 12, func(f *Fake) { DemoDisc(f, "stuck") }, nil, "partial/automatic"},
		// the iMac10,1 (0.7.2 review): the level stuck, the SMC's raw bytes following the hand
		{"light sensor stuck, raw bytes moved", "input.ambient-light-sensor", 20, DemoLightStuck, map[float64]int{15: 1}, "failed/automatic"},
		// nothing moves, raw bytes included (0.7.3 follow-ups): the sensor didn't respond to Linux
		{"light sensor stuck, raw bytes too", "input.ambient-light-sensor", 20, func(f *Fake) {
			f.Set("/sys/bus/acpi/devices/APP0001:00/light", "(18,0)")
			DemoSMC(f, 18)
		}, map[float64]int{15: 1}, "failed/observed"},
		{"keyboard light: no change", "input.keyboard-backlight", 12, nil, map[float64]int{11: 4}, "failed/observed"},
		{"keyboard light: wrong count twice", "input.keyboard-backlight", 24, nil, map[float64]int{11: pickWrong, 23: pickWrong}, "partial/challenge"},
		{"sleep, no answer to the code", "power.sleep-wake", 80, demo("power.sleep-wake"), map[float64]int{2: 0, 78: 0}, "partial/automatic"},
		{"sleep, wrong code twice", "power.sleep-wake", 16, demo("power.sleep-wake"), map[float64]int{2: 0, 12: pickWrong, 15: pickWrong}, "partial/challenge"}, // the second after the red code (codeRed)
		{"sleep, code skipped", "power.sleep-wake", 14, demo("power.sleep-wake"), map[float64]int{2: 0, 12: -1}, "not_tested/uncertain"},
		// a disc in the optical drive: no nap until it's out (0.7.3 review: an iMac10,1 hung)
		{"sleep held with a disc in", "power.sleep-wake", 16, func(f *Fake) { demo("power.sleep-wake")(f); DemoDisc(f, "in") },
			map[float64]int{2: 0, 12: pickRight}, "undecided"},
		{"sleep after Eject disc", "power.sleep-wake", 16, func(f *Fake) { demo("power.sleep-wake")(f); DemoDisc(f, "in") },
			map[float64]int{1: 1, 4: 0, 14: pickRight}, "supported/challenge"},
		{"lid held with a disc in", "power.lid", 15, func(f *Fake) { demo("power.lid")(f); DemoDisc(f, "in") }, nil, "undecided"},
		{"lid nap woken by a non-lid source", "power.lid", 15, func(f *Fake) {
			demo("power.lid")(f)
			f.SleepAs.WokenBy, f.SleepAs.Events = []string{"XHC1 (0000:00:14.0)"}, []string{"GPE 0x0D +1"}
		}, nil, "partial/automatic"},
	}
	for _, c := range cases {
		o := play(t, c.id, c.until, c.script, c.picks)
		if got := status(o); got != c.want {
			t.Errorf("%s: %s, want %s", c.name, got, c.want)
		}
		if strings.Contains(c.name, "naps again") && (o == nil || !strings.Contains(o.Results[0].Evidence, "nap 2")) {
			t.Errorf("%s: no second nap in the evidence", c.name)
		}
		if strings.Contains(c.name, "raw bytes too") && (o == nil || !strings.Contains(o.Results[0].Evidence, "didn't respond to Linux") ||
			!strings.Contains(o.Results[0].Evidence, "ALV0 {alv 10 bytes: before")) {
			t.Errorf("%s: the evidence lacks the raw bytes or the verdict: %v", c.name, o)
		}
	}
}

// The keyboard light goes back to where it was, whatever the answer.
func TestKeyLightRestored(t *testing.T) {
	s, _ := Lookup("input.keyboard-backlight")
	f := NewFake()
	DemoMac(f)
	r := Begin(s, &Env{}, f)
	for tt := 0.0; tt < 3; tt += 0.1 {
		r.Step(tt) // stopped mid-blink
	}
	r.Stop()
	if got := f.Read("/sys/class/leds/smc::kbd_backlight/brightness"); got != "40" {
		t.Errorf("keyboard light left at %s, want 40", got)
	}
}

// The brightness test always puts the original level back.
func TestBrightnessRestored(t *testing.T) {
	s, _ := Lookup("display.brightness")
	f := NewFake()
	DemoMac(f)
	r := Begin(s, &Env{}, f)
	for tt := 0.0; tt < 2.5; tt += 0.1 {
		r.Step(tt) // stopped mid-play, at the dim step
	}
	r.Stop()
	if got := f.Read("/sys/class/backlight/intel_backlight/brightness"); got != "3400" {
		t.Errorf("brightness left at %s, want 3400", got)
	}
}

func TestSleepVerdict(t *testing.T) {
	ok := Sleep{Mode: "deep", Asked: 20 * time.Second, Slept: 20 * time.Second, Took: 23 * time.Second}
	cases := []struct {
		sl   Sleep
		bad  []string
		want string
	}{
		{ok, nil, checks.Supported},
		{ok, []string{"not back after 30 s: USB 2-1.2"}, checks.Partial},
		{ok, []string{"the network didn't come back within 30 s"}, checks.Partial},
		{Sleep{Mode: "deep", Asked: 20 * time.Second, Slept: 2 * time.Second}, nil, checks.Partial},
		{Sleep{Mode: "deep", Asked: 20 * time.Second, Slept: 90 * time.Second}, nil, checks.Partial},
		{Sleep{Mode: "deep", Err: ErrNotReal}, nil, checks.Failed},
		{Sleep{Mode: "s2idle", Asked: 20 * time.Second}, nil, checks.Failed},
	}
	for i, c := range cases {
		if got, ev := SleepVerdict(c.sl, c.bad); got != c.want {
			t.Errorf("%d: %s (%s), want %s", i, got, ev, c.want)
		}
	}
}

func TestEDIDName(t *testing.T) {
	if got := EDIDName([]byte(DemoEDID("DELL U2412M"))); got != "DELL U2412M" {
		t.Errorf("name %q", got)
	}
	b := []byte(DemoEDID(""))
	b[72+3] = 0 // no name descriptor: the manufacturer's letters
	if got := EDIDName(b); got != "DEL" {
		t.Errorf("manufacturer %q", got)
	}
}

// "The network is back" after a nap (lid, sleep) is the status line's network (IO.NetNow),
// held for netSettle: never said while the status line is offline, nor in the moment a link
// reads up before it drops; a network that never comes back is a problem in the evidence.
func TestNetBack(t *testing.T) {
	var w netWatch
	w.see(0, Net{Up: true})
	w.see(1, Net{})
	w.see(2, Net{Up: true})
	if w.back(4.5, netSettle) || !w.back(5, netSettle) {
		t.Errorf("up at 2 after a drop: back at 4.5 %v, at 5 %v", w.back(4.5, netSettle), w.back(5, netSettle))
	}
	for _, c := range []struct {
		name     string
		id       string
		upAgain  float64 // when the status line is online again (0: never)
		picks    map[float64]int
		until    float64
		want     string
		backFrom float64 // "the network is back" may not show before this
	}{
		{"lid: the adapter comes back at 14 s", "power.lid", 14, nil, 30, "supported/automatic", 17},
		{"lid: it never comes back", "power.lid", 0, nil, 45, "partial/automatic", 1e9},
		{"sleep: it comes back at 12 s", "power.sleep-wake", 12, map[float64]int{2: 0, 16: pickRight}, 30, "supported/challenge", 15},
	} {
		s, _ := Lookup(c.id)
		f := NewFake()
		DemoMac(f)
		s.Demo(f)
		f.Set(fakeNet+"online", "en1, Ethernet")
		f.SetAt(6, fakeNet+"offline", "en1: no carrier") // goes with the nap, as a Thunderbolt adapter does
		if c.upAgain > 0 {
			f.SetAt(c.upAgain, fakeNet+"offline", "")
		}
		r := Begin(s, &Env{Online: "en1, Ethernet"}, f)
		for tt := 0.0; tt <= c.until && r.Outcome() == nil; tt += 0.1 {
			for at, a := range c.picks {
				if at > tt-0.1 && at <= tt {
					if a == pickRight && r.Codes() {
						typeCode(r, true)
					} else {
						r.Act(a)
					}
				}
			}
			r.Step(tt)
			for _, l := range r.View().Live {
				if strings.HasPrefix(l.Text, "the network is back") && (tt < c.backFrom-0.05 || !f.NetNow().Up) {
					t.Fatalf("%s: %q at %.1f s, with the status line %+v", c.name, l.Text, tt, f.NetNow())
				}
			}
		}
		o := r.Outcome()
		if got := status(o); got != c.want {
			t.Errorf("%s: %s, want %s", c.name, got, c.want)
		}
		if c.upAgain == 0 && (o == nil || !strings.Contains(o.Results[0].Evidence, "the network didn't come back")) {
			t.Errorf("%s: the evidence doesn't say the network didn't come back", c.name)
		}
		r.Stop()
	}
}

// Every test's "Skip this one" leaves Boot something to say (0.7.2 review: after skipping the
// USB ports, his box under the band was blank).
func TestSkipSays(t *testing.T) {
	for _, s := range All() {
		if s.Stub || s.New == nil {
			continue
		}
		f := NewFake()
		DemoMac(f)
		if s.Demo != nil {
			s.Demo(f)
		}
		r := Begin(s, &Env{Ports: DemoAirPorts()}, f)
		r.Step(0.5)
		if !r.Skip() {
			continue // a Skipper took it as an answer
		}
		o := r.Outcome()
		r.Leave()
		if o == nil || strings.TrimSpace(o.Say) == "" {
			t.Errorf("%s: skipped, and Boot says nothing", s.ID)
		}
	}
}
