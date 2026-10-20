package main

import (
	"github.com/omaboot/boot-live/internal/demo"
	"github.com/omaboot/boot-live/internal/handson"
	"github.com/omaboot/boot-live/internal/ui"
)

// The hands-on layout's own pieces: a test opened
// paused by Backspace and Boot's hint after 20 s with no key (dressed onto a real run's screen).
// The typed-code field is in the sleep and external display tests' own shots (h5, h7).
func init() {
	testShots = append(testShots, func() []shot {
		return []shot{
			pausedShot("h16-back-paused", "input.ambient-light-sensor", "power.charging", 2),
			dressedShot("h16-hint", "input.keyboard-backlight", 30, func(s *handson.Screen) {
				s.Hint = "lost count? Show it again plays the same blinks. a darker room helps."
			}),
		}
	})
}

// pausedShot is test id opened paused by Backspace from the test after it: before (played to
// its result first), with the demo's answers none.
func pausedShot(name, id, after string, t float64) shot {
	h := ui.NewHandsOn(demo.HandsOnSetup())
	i := planIndex(h, id)
	h.Play(i, 12)
	h.Play(planIndex(h, after), 1)
	h.Stop()
	h.OpenPaused(i, 0)
	s := st(ui.Test, t, wifiUp, func(s *ui.State) { s.HO = h })
	f := demo.Finished()
	f.HandsOn = h.Checks()
	return shot{name: name, st: s, f: f}
}

// dressed is a run whose screen gets a change on top (a hint, a code field).
type dressed struct {
	handson.Run
	dress func(*handson.Screen)
}

func (d dressed) View(c *handson.Ctx) handson.Screen {
	s := d.Run.View(c)
	d.dress(&s)
	return s
}

// dressedShot is test id played to t with its screen dressed.
func dressedShot(name, id string, t float64, dress func(*handson.Screen), picks ...ui.Pick) shot {
	h := ui.NewHandsOn(demo.HandsOnSetup())
	i := planIndex(h, id)
	spec := h.Setup.Plan[i]
	orig := spec.New
	spec.New = func(e *handson.Env) handson.Run { return dressed{orig(e), dress} }
	h.Setup.Plan = append([]handson.Spec(nil), h.Setup.Plan...)
	h.Setup.Plan[i] = spec
	h.Play(i, t, picks...)
	s := st(ui.Test, t, wifiUp, func(s *ui.State) { s.HO = h })
	f := demo.Finished()
	f.HandsOn = h.Checks()
	return shot{name: name, st: s, f: f}
}

func planIndex(h *ui.HandsOn, id string) int {
	for k, s := range h.Setup.Plan {
		if s.ID == id {
			return k
		}
	}
	panic("no hands-on test " + id)
}
