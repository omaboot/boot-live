package main

import (
	"github.com/omaboot/boot-live/internal/checks"
	"github.com/omaboot/boot-live/internal/demo"
	"github.com/omaboot/boot-live/internal/handson"
	"github.com/omaboot/boot-live/internal/pace"
	"github.com/omaboot/boot-live/internal/ui"
)

// testShots are more hands-on shots, one function per test file (tools/screens/handson_<name>.go
// appends its own from init), so tests can be added in parallel without touching this file.
var testShots []func() []shot

// playShot is a test's screen: test id played on the demo's Mac to t (scene seconds), with the
// person's answers (picks) and any change to the state (mod: an overlay).
func playShot(name, id string, t float64, mod func(*ui.State), picks ...ui.Pick) shot {
	h := ui.NewHandsOn(demo.HandsOnSetup())
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
	if mod != nil {
		mod(&s)
	}
	f := demo.Finished()
	f.HandsOn = h.Checks()
	return shot{name: name, st: s, f: f}
}

// handsOnShots are the hands-on screens: "Boot needs you", then each test at chosen moments,
// played against the demo's scripted Mac (handson.DemoMac and each test's Demo script) with the
// person's answers given as picks.
func handsOnShots() []shot {
	full := demo.Finished()
	online := func(s *ui.State) { wifiUp(s) }
	ho := func() *ui.HandsOn { return ui.NewHandsOn(demo.HandsOnSetup()) }
	idx := func(id string) int {
		for i, s := range demo.HandsOnSetup().Plan {
			if s.ID == id {
				return i
			}
		}
		panic("no hands-on test " + id)
	}
	// test plays test id to t with the picks, and returns the shot's state and facts
	test := func(id string, t float64, mod func(*ui.State), picks ...ui.Pick) (ui.State, *ui.Facts) {
		h := ho()
		h.Play(idx(id), t, picks...)
		if h.Setup.Plan[idx(id)].Sleeps { // the copy saved before it is on the stick
			h.Kept = "results/" + demo.MacID + "-20261011T081502Z.json"
		}
		s := st(ui.Test, t, online, func(s *ui.State) { s.HO = h })
		if mod != nil {
			mod(&s)
		}
		f := demo.Finished()
		f.HandsOn = h.Checks()
		return s, f
	}
	// done is a HandsOn with every test played to its decision (the person's answers as the
	// demo gives them): for "Boot needs you" again, and Finish after the tests
	done := func(upto int) *ui.HandsOn {
		h := ho()
		for i, s := range h.Setup.Plan[:upto] {
			switch s.ID {
			case "display.native-resolution": // the demo's patterns: a box with an X, then nested boxes
				h.Play(i, 4, ui.Pick{At: 1.5, Action: 3}, ui.Pick{At: 3, Action: 5})
			case "display.brightness": // every step seen
				h.Play(i, 8, ui.Pick{At: 7.5, Action: 0})
			case "input.keyboard-backlight": // the demo blinks 3 times
				h.Play(i, 10, ui.Pick{At: 9.6, Action: 2})
			case "input.keyboard-backlight.keys": // F6 four times, F5 twice; the light followed
				h.Play(i, 10, ui.Pick{At: 8.5, Action: 0})
			case "graphics.external-display": // the demo's code (on the monitor only) is 630
				h.Play(i, 8, ui.Pick{At: 7, Code: demoCode})
			case "power.sleep-wake": // the demo's code after the nap is 630 too
				h.Play(i, 14, ui.Pick{At: 3, Action: 0}, ui.Pick{At: 12.6, Code: demoCode})
			case "input.keyboard":
				h.Play(i, 36, ui.Pick{At: 32.6, Action: 0})
			case "audio.speakers":
				h.Play(i, 13, ui.Pick{At: 4.4, Action: 4}, ui.Pick{At: 7, Action: 1}, ui.Pick{At: 11.3, Action: 0})
			case "audio.headphone":
				h.Play(i, 12, ui.Pick{At: 4, Action: 0}, ui.Pick{At: 7.4, Action: 4}, ui.Pick{At: 10.4, Action: 1})
			case "audio.display-out":
				h.Play(i, 8, ui.Pick{At: 7, Action: 4})
			case "ports.usb-a": // which port the stick is in; the rest decides itself
				h.Play(i, 12, ui.Pick{At: 1, Action: 0})
			case "ports.usb-c":
				h.Play(i, 12, ui.Pick{At: 11, Action: 0})
			case "camera.builtin":
				h.Play(i, 5, ui.Pick{At: 4.2, Action: 0})
			default:
				h.Play(i, 12)
			}
		}
		h.Stop()
		return h
	}
	var shots []shot
	add := func(name string, s ui.State, f *ui.Facts) { shots = append(shots, shot{name: name, st: s, f: f}) }

	add("h0-results-keep-going", st(ui.Results, 10, online, func(s *ui.State) { s.HO = ho() }), full)
	add("h1-attention-start", st(ui.Attention, 1.4, online, func(s *ui.State) { s.HO = ho() }), full)
	add("h1-attention", st(ui.Attention, 8, online, func(s *ui.State) { s.HO = ho() }), full)
	part := done(3)
	partFacts := demo.Finished()
	partFacts.HandsOn = part.Checks()
	add("h1-attention-again", st(ui.Attention, 8, online, func(s *ui.State) { s.HO = part }), partFacts)
	add("h1-attention-quit", st(ui.Attention, 8, online, func(s *ui.State) { s.HO = ho(); s.Overlay = ui.ConfirmOverlay }), full)

	s, f := test("display.brightness", 2.4, nil)
	add("h2-brightness-stepping", s, f)
	s, f = test("display.brightness", 6, nil)
	add("h2-brightness-ask", s, f)
	s, f = test("display.brightness", 7.8, nil, ui.Pick{At: 6.4, Action: 0}, ui.Pick{At: 7.3, Action: 0})
	add("h2-brightness-pass", s, f)
	s, f = test("display.brightness", 7.8, nil, ui.Pick{At: 6.4, Action: 1}, ui.Pick{At: 7.3, Action: 0})
	add("h2-brightness-partial", s, f)

	s, f = test("input.keyboard-backlight", 2.6, nil)
	add("h3-kbdlight-stepping", s, f)
	s, f = test("input.keyboard-backlight", 9.5, nil)
	add("h3-kbdlight-ask", s, f)

	s, f = test("power.charging", 1.5, nil)
	add("h4-charging-unplug", s, f)
	s, f = test("power.charging", 5, nil)
	add("h4-charging-replug", s, f)
	s, f = test("power.charging", 7.6, nil)
	add("h4-charging-waiting", s, f)
	s, f = test("power.charging", 9.5, nil)
	add("h4-charging-pass", s, f)
	s, f = test("power.charging", 2.6, nil, ui.Pick{At: 2, Action: -1})
	add("h4-charging-skipped", s, f)

	s, f = test("graphics.external-display", 2.5, nil)
	add("h5-display-waiting", s, f)
	s, f = test("graphics.external-display", 6.5, nil)
	add("h5-display-ask", s, f)
	s, f = test("graphics.external-display", 7.6, nil, ui.Pick{At: 7.2, Code: "63", Open: true})
	add("h5-display-typing", s, f)
	s, f = test("graphics.external-display", 8, nil, ui.Pick{At: 7.2, Code: demoCode})
	add("h5-display-pass", s, f)
	s, f = test("graphics.external-display", 8, nil, ui.Pick{At: 7.2, Code: "613"})
	add("h5-display-retry", s, f)

	s, f = test("power.lid", 0.6, func(s *ui.State) { s.HO.Keeping, s.HO.Kept = true, "" })
	add("h6-lid-copy", s, f)
	s, f = test("power.lid", 2.5, nil)
	add("h6-lid-waiting", s, f)
	s, f = test("power.lid", 6, nil)
	add("h6-lid-sleeping", s, f)
	s, f = test("power.lid", 10, nil)
	add("h6-lid-back", s, f)
	s, f = test("power.lid", 12, nil)
	add("h6-lid-pass", s, f)
	s, f = test("power.lid", 3, func(s *ui.State) { s.Overlay = ui.ConfirmOverlay })
	add("h6-lid-end-tests", s, f)
	s, f = test("power.lid", 3, func(s *ui.State) { s.Overlay = ui.LogOverlay })
	add("h6-lid-log", s, f)
	// 0.7.6: a FireWire disk attached holds the lid's nap (closing does nothing) until it's unplugged
	for _, x := range []struct {
		name string
		t    float64
	}{{"h6-lid-firewire-disk", 2.5}, {"h6-lid-firewire-closed", 6}} {
		setup := demo.HandsOnSetup()
		io := setup.IO
		setup.IO = func(sp handson.Spec) handson.IO {
			v := io(sp)
			if f, ok := v.(*handson.Fake); ok && sp.ID == "power.lid" {
				handson.DemoFWDisk(f, 0, false)
			}
			return v
		}
		h := ui.NewHandsOn(setup)
		h.Play(idx("power.lid"), x.t)
		s := st(ui.Test, x.t, online, func(s *ui.State) { s.HO = h })
		f := demo.Finished()
		f.HandsOn = h.Checks()
		add(x.name, s, f)
	}

	s, f = test("ports.usb-a", 6, func(s *ui.State) { s.Overlay, s.Gone, s.PassDone = ui.StickOverlay, true, true })
	add("h9-usb-stick-gone", s, f)
	s, f = test("power.sleep-wake", 2, nil)
	add("h7-sleep-ready", s, f)
	s, f = test("power.sleep-wake", 4, nil, ui.Pick{At: 3, Action: 0})
	add("h7-sleep-countdown", s, f)
	s, f = test("power.sleep-wake", 7, nil, ui.Pick{At: 3, Action: 0})
	add("h7-sleep-asleep", s, f)
	s, f = test("power.sleep-wake", 10, nil, ui.Pick{At: 3, Action: 0})
	add("h7-sleep-back", s, f)
	s, f = test("power.sleep-wake", 12.4, nil, ui.Pick{At: 3, Action: 0})
	add("h7-sleep-code", s, f)
	s, f = test("power.sleep-wake", 13, nil, ui.Pick{At: 3, Action: 0}, ui.Pick{At: 12.6, Code: "63", Open: true})
	add("h7-sleep-typing", s, f)
	s, f = test("power.sleep-wake", 13.5, nil, ui.Pick{At: 3, Action: 0}, ui.Pick{At: 12.6, Code: "613"})
	add("h7-sleep-wrong", s, f) // the missed code, red
	s, f = test("power.sleep-wake", 14.6, nil, ui.Pick{At: 3, Action: 0}, ui.Pick{At: 12.6, Code: "613"})
	add("h7-sleep-wrong-new", s, f) // then the new code
	s, f = test("power.sleep-wake", 13.5, nil, ui.Pick{At: 3, Action: 0}, ui.Pick{At: 12.6, Code: demoCode})
	add("h7-sleep-pass", s, f)
	s, f = test("power.sleep-wake", 74, nil, ui.Pick{At: 3, Action: 0})
	add("h7-sleep-late", s, f) // no answer in 60 s: the code stays up, No code offered
	s, f = test("power.sleep-wake", 76, nil, ui.Pick{At: 3, Action: 0}, ui.Pick{At: 75, Code: demoCode})
	add("h7-sleep-late-typed", s, f)

	all := done(len(demo.HandsOnSetup().Plan))
	fin := demo.Finished()
	fin.HandsOn = all.Checks()
	fin.Groups = demo.GroupsWith(paceOf(all.All()))
	add("h8-finish-after", st(ui.Finish, 6, online, func(s *ui.State) { s.HO = all }), fin)
	add("h8-report-after", st(ui.Report, 4, online, func(s *ui.State) { s.HO = all }), fin)

	plainShot := func(name string, s ui.State, f *ui.Facts) {
		shots = append(shots, shot{name: name, st: s, f: f, opt: func(o *ui.Options) { o.Plain = true }})
	}
	plainShot("plain-h1-attention", st(ui.Attention, 8, online, func(s *ui.State) { s.HO = ho() }), full)
	s, f = test("power.charging", 5, nil)
	plainShot("plain-h4-charging-replug", s, f)
	for _, more := range testShots {
		shots = append(shots, more()...)
	}
	return shots
}

// demoCode is the code the demo's sleep test shows after the nap and its external display's
// card shows (the Fake's fixed seed): what the shots type to pass.
const demoCode = "630"

func paceOf(rs []checks.Result) map[string]pace.Status {
	out := map[string]pace.Status{}
	for _, x := range rs {
		out[x.ID] = x.Pace()
	}
	return out
}
