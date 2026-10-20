package handson

import (
	"fmt"
	"time"

	"github.com/omaboot/boot-live/internal/boot"
	"github.com/omaboot/boot-live/internal/checks"
	g "github.com/omaboot/boot-live/internal/grid"
	"github.com/omaboot/boot-live/internal/i18n"
)

// input.force-touch, "Force Touch click and pressure sensing" (Force Touch trackpads: the 2015
// and later MacBook Pros, the 12-inch MacBook, the T2 MacBook Airs). The trackpad doesn't move:
// its click is made by the trackpad itself (firmware and a haptic motor), and it measures how
// hard a finger presses. The person clicks once, then presses firmly; the machine reads the
// built-in trackpad's events (method automatic):
//
//   - supported: the click registered (BTN_LEFT) and the trackpad reports per-finger pressure
//     (ABS_MT_PRESSURE) that rose past half its range on the firm press;
//   - partial: the click registered but Linux gets no pressure levels: the trackpad has no
//     ABS_MT_PRESSURE axis (automatic; what applespi and bcm5974 give today, hence the catalog's
//     "clicks work, pressure levels don't"), or it has one and the tester says pressing harder
//     changes nothing (observed);
//   - failed: no built-in trackpad device (automatic), or the tester says clicking does nothing
//     and no click registered (observed).
//
// ABS_PRESSURE alone isn't counted: it's the single-touch axis, and bcm5974 makes it from the
// contact's size, not from force.

func init() {
	Register(Spec{ID: "input.force-touch", Group: NoPlug, Order: 30, Title: i18n.M("Force Touch"),
		Brief: i18n.M("click, then press the trackpad firmly"), Expect: 30 * time.Second,
		New: func(*Env) Run { return &forceTouch{} }, Demo: demoForceTouch})
}

// ftFirm is the share of the pressure range a firm press reaches.
const ftFirm = 0.5

type ftPhase int

const (
	ftClick ftPhase = iota // waiting for a click
	ftPress                // waiting for a firm press
)

type forceTouch struct {
	dev    Device
	id     string
	in     Input
	f      *fingers
	axis   bool // ABS_MT_PRESSURE
	pr     span
	phase  ftPhase
	at     float64
	now    int // the pressure now
	atClk  int // … at the click
	peak   int // the highest during the firm press
	single bool
	last   float64 // when the trackpad last sent anything
}

func (ft *forceTouch) Start(c *Ctx) {
	c.Select(-1) // the trackpad answers; a stray Enter mustn't
	if fixtureRun(c) {
		c.Decide(Outcome{Results: []checks.Result{notOnMac("input.force-touch")}, Say: i18n.T("a fixture tree has no trackpad to press.")})
		return
	}
	pads, mouse := touchpads(c.IO)
	if len(pads) == 0 || mouse {
		ev := "no built-in trackpad reporting touches among the input devices"
		if ps := pointers(c.IO); len(ps) > 0 {
			ev += "; pointing devices: " + devNames(c.IO, ps)
		}
		c.Decide(Outcome{Results: []checks.Result{Result("input.force-touch", checks.Failed, Automatic, ev, i18n.T("no built-in trackpad device"))},
			Say: i18n.T("no trackpad reporting touches, so nothing to press. that's a fail.")})
		return
	}
	in, devs := OpenAll(c.IO, func(d Device) bool { return d.Event == pads[0].Event }, false)
	if len(devs) == 0 {
		in.Close()
		c.Decide(Outcome{Results: []checks.Result{NotTested("input.force-touch", checks.Other,
			"couldn't open the built-in trackpad's input device: "+devNames(c.IO, pads[:1]), i18n.T("couldn't open the trackpad"))},
			Say: i18n.T("I couldn't open the trackpad to listen to it. not tested.")})
		return
	}
	ft.dev, ft.in = devs[0], in
	ft.id = readID(c.IO, ft.dev).String()
	ft.f = newFingers(c.IO, ft.dev)
	ft.axis = ft.dev.Has("abs", absMtPressure)
	ft.single = ft.dev.Has("abs", absPressure)
	if ft.axis {
		if ft.pr = axis(c.IO, ft.dev, absMtPressure); !ft.pr.ok {
			ft.pr = span{0, 255, false} // the range couldn't be read: a byte, as Apple's trackpads send it
		}
	}
	ft.at = 0.2
}

// firm is the pressure a firm press reaches.
func (ft *forceTouch) firm() int { return ft.pr.lo + int(float64(ft.pr.hi-ft.pr.lo)*ftFirm) }

func (ft *forceTouch) Step(c *Ctx) {
	if ft.in == nil {
		return
	}
	for _, e := range ft.in.Events() {
		ft.last = c.T
		if !ft.f.feed(e) {
			continue
		}
		ft.now = ft.f.pressure()
		switch ft.phase {
		case ftClick:
			if !ft.f.pressed {
				continue
			}
			ft.f.pressed = false
			ft.atClk = ft.now
			if !ft.axis {
				ft.decide(c, checks.Partial, Automatic, "; no ABS_MT_PRESSURE axis: Linux gets the click but no pressure levels"+ft.singleNote(),
					i18n.T("click works, no pressure levels"), i18n.T("the click works. pressure levels don't reach Linux. partial."))
				return
			}
			ft.phase, ft.at = ftPress, c.T
		case ftPress:
			ft.f.pressed = false
			ft.peak = max(ft.peak, ft.now)
			if ft.now >= ft.firm() {
				ft.decide(c, checks.Supported, Automatic, "", i18n.T("click and pressure both seen"),
					i18n.T("a click, then a firm press. it feels the force."))
				return
			}
		}
	}
}

func (ft *forceTouch) singleNote() string {
	if ft.single {
		return " (ABS_PRESSURE alone isn't counted: single-touch, and bcm5974 makes it from the contact's size)"
	}
	return ""
}

func (ft *forceTouch) evidence(tail string) string {
	ev := fmt.Sprintf("trackpad: %s (%s, %s)", ft.dev.Name, ft.dev.Event, ft.id)
	if ft.f.clicks > 0 {
		ev += fmt.Sprintf("; click registered (BTN_LEFT, %d)", ft.f.clicks)
	} else {
		ev += "; no click registered"
	}
	if ft.axis {
		rng := ft.pr.String()
		if !ft.pr.ok {
			rng = "range unreadable, taken as 0…255"
		}
		ev += fmt.Sprintf("; ABS_MT_PRESSURE %s: %d at the click, firm press peaked at %d (firm is %d)", rng, ft.atClk, ft.peak, ft.firm())
	}
	return ev + tail
}

func (ft *forceTouch) decide(c *Ctx, status, method, tail, detail, say string) {
	c.Decide(Outcome{Results: []checks.Result{Result("input.force-touch", status, method, ft.evidence(tail), detail)}, Say: say})
}

func (ft *forceTouch) answerable(c *Ctx) bool {
	if ft.phase == ftClick {
		return c.T >= 8
	}
	return c.T-ft.at >= 5
}

func (ft *forceTouch) Act(c *Ctx, i int) {
	if i != 0 || ft.in == nil || !ft.answerable(c) {
		return
	}
	if ft.phase == ftClick {
		ft.decide(c, checks.Failed, Observed, "; the tester says clicking the trackpad does nothing", i18n.T("no click registered"),
			i18n.T("pressed and pressed, and no click came through. that's a fail."))
		return
	}
	ft.decide(c, checks.Partial, Observed, "; the tester pressed firmly and says nothing changed", i18n.T("click works, pressure didn't rise"),
		i18n.T("the click works, but the pressure never climbed. partial."))
}

func (ft *forceTouch) Stop(*Ctx) {
	if ft.in != nil {
		ft.in.Close()
	}
}

func (ft *forceTouch) View(c *Ctx) Screen {
	s := Screen{Face: boot.Idle, SayAt: ft.at, OutTitle: i18n.T("What I see"),
		Steps:   []string{i18n.T("Click the trackpad once."), i18n.T("Now press firmly, harder than a click, and hold it.")},
		Happens: i18n.T("A Force Touch trackpad doesn't move: it makes the click itself, and measures how hard a finger presses.")}
	if fixtureRun(c) {
		return fixtureScreen()
	}
	if ft.f == nil {
		s.Steps, s.Happens = []string{i18n.T("No built-in trackpad to test.")}, ""
		s.Live = []Line{Bad(i18n.T("no built-in trackpad found"))}
		return s
	}
	switch ft.phase {
	case ftClick:
		s.Step, s.Say = 0, i18n.T("a normal click first.")
		s.Waiting = "·  " + i18n.T("waiting for a click")
	default:
		s.Step = 1
		s.Say, s.Face = i18n.T("click seen. now lean on it."), boot.Work
		s.Waiting = "·  " + i18n.T("waiting for a firm press")
	}
	if c.Outcome() != nil {
		s.Step = len(s.Steps)
	}
	if c.T-ft.last >= 20 { // the trackpad comes through evdev, not the console: the run knows when it's idle
		s.Hint = i18n.T("a firm press goes past the click: lean in until the trackpad clicks a second time.")
	}
	s.Live = []Line{Fact(i18n.T("trackpad: %s (%s)", ft.dev.Name, ft.dev.Event))}
	if ft.axis {
		s.Live = append(s.Live, Fact(i18n.T("per-finger pressure: %s", ft.pr)))
	} else {
		s.Live = append(s.Live, Gone(i18n.T("no per-finger pressure axis: Linux would see clicks only")))
	}
	if ft.phase == ftClick {
		s.Live = append(s.Live, Wait(i18n.T("waiting for a click")))
	} else {
		s.Live = append(s.Live, Fact(i18n.T("click seen")), Wait(i18n.T("pressure %d, firm is %d", ft.now, ft.firm())))
	}
	s.Anim = ft.draw
	a := Action{Label: i18n.T("Clicking does nothing"), Does: i18n.T("I clicked and nothing registered here")}
	if ft.phase == ftPress {
		a = Action{Label: i18n.T("Pressing harder changes nothing"), Does: i18n.T("I pressed firmly and the bar barely moved")}
	}
	a.Off = !ft.answerable(c)
	s.Actions = []Action{a}
	return s
}

// draw is the Force Touch test's animation: the trackpad with a finger on it (violet while the
// click is down), and the pressure it reports as a bar with the firm mark under it.
func (ft *forceTouch) draw(gr *g.Grid, x, y, w, h int, t float64) {
	if h < 7 || w < 40 {
		return
	}
	ph := min(h-2, 8)
	pw := min(int(float64(ph)*2.8), w/3)
	py := y + (h-ph)/2
	outline(gr, x+2, py, pw, ph, g.DIM)
	fx, fy := x+2+pw/2-1, py+ph/2
	fc := g.DIM // the finger: dim until it's on the pad
	switch {
	case ft.f.button:
		fc = g.BVIO
	case ft.f.count() > 0:
		fc = g.WHT
	}
	gr.Put(fx, fy, "██", fc)
	if ft.phase == ftClick && ft.f.count() == 0 && int(t*2)%2 == 0 {
		gr.Put(fx, fy-1, "↓", g.GRY)
	}
	bx := x + 2 + pw + 4
	gr.Put(bx, py+1, i18n.T("pressure"), g.GRY)
	if !ft.axis {
		gr.Put(bx, py+3, cutRunes(i18n.T("no pressure levels reach Linux"), x+w-bx), g.DIM)
		return
	}
	rng := ft.pr.hi - ft.pr.lo
	n := min(40, x+w-bx-2)
	levelBar(ft.now-ft.pr.lo, rng)(gr, bx, py+3, n+4, 1, t)
	if rng > 0 && n > 0 {
		at := bx + 1 + (ft.firm()-ft.pr.lo)*min(40, n)/rng
		gr.Put(at, py+4, "▲ "+i18n.T("firm"), g.DIM)
	}
}

// demoForceTouch: a Force Touch trackpad on the demo Mac (as a T2 Mac's reports it: per-finger
// pressure 0–253): a light touch, a click, then a press that climbs past half.
func demoForceTouch(f *Fake) {
	d := demoBcm5974(f, "event3", "Apple Inc. Apple Internal Keyboard / Trackpad", absMtPressure)
	demoID(f, "event3", busUSB, 0x05ac, 0x027a)
	f.Set(fmt.Sprintf("/dev/input/event3/absinfo/%d", absMtPressure), "0 253")
	at := [2]float64{0.5, 0.6}
	d.p = []int{18}
	d.frame(1.2, false, at)
	d.p = []int{64}
	d.frame(2.4, true, at)
	d.p = []int{40}
	d.frame(2.6, false, at)
	for i, p := range []int{40, 62, 88, 110, 134, 152, 168, 181, 190} {
		d.p = []int{p}
		d.frame(4+float64(i)*0.2, false, at)
	}
}
