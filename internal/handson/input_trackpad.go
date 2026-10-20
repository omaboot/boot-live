package handson

import (
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/omaboot/boot-live/internal/boot"
	"github.com/omaboot/boot-live/internal/checks"
	g "github.com/omaboot/boot-live/internal/grid"
	"github.com/omaboot/boot-live/internal/i18n"
)

// input.trackpad, "Trackpad and multi-touch" (laptops: feature:trackpad). The person goes
// through a short list of things to do on the built-in trackpad; the machine reads the
// trackpad's own events (evdev: the multi-touch slots, the finger count, the button) and ticks
// each one off as it sees it (method automatic):
//
//  1. touch all four corners (the whole surface reports positions);
//  2. click (BTN_LEFT with one finger);
//  3. click with two fingers (BTN_LEFT with two: Omarchy's right click);
//  4. scroll with two fingers, up and down;
//  5. pinch with two fingers (their distance changes);
//  6. swipe sideways with three fingers.
//
// Trackpads without multi-touch slots (appletouch, 2006–2007) report one position and a finger
// count: there the list stops at the scroll (they have no pinch or three-finger swipe to test).
//
//   - supported: everything on the list registered;
//   - partial: some of it registered and the tester says the rest doesn't work (observed; the
//     evidence lists what never registered), or the trackpad works only as a mouse (relative
//     motion and a button, no touches: its multi-touch driver didn't bind; automatic);
//   - failed: no built-in trackpad device (automatic), or the tester says nothing happens and
//     nothing registered (observed).
//
// Nothing on screen follows the fingers but the drawing: the console has no pointer.

func init() {
	Register(Spec{ID: "input.trackpad", Group: NoPlug, Order: 20, Title: i18n.M("Trackpad"),
		Brief: i18n.M("touch the corners, click, scroll, pinch, swipe"), Expect: time.Minute,
		New: func(*Env) Run { return &trackpad{} }, Demo: demoTrackpad})
}

// ── fingers: a trackpad's contacts from its events ────────────────────────────

// contact is one finger in a multi-touch slot.
type contact struct {
	on      bool
	x, y, p int
}

// fingers follows a trackpad from its events: its multi-touch slots, or one position and a
// finger count (BTN_TOOL_*), or relative steps for a pad in mouse mode; the button; pressure.
type fingers struct {
	mt, rel  bool
	xr, yr   span // the position axes (seen ranges when they couldn't be read)
	fixed    bool // xr and yr came from the device
	slot     int
	slots    [16]contact
	x, y, p  int // one-position pads: position and ABS_PRESSURE
	tools    int // BTN_TOOL_* count
	button   bool
	pressed  bool // the button went down in this frame
	moved    int  // relative steps (mouse mode)
	most     int  // the most fingers seen at once
	clicks   int
	lastSeen float64
}

func newFingers(io IO, d Device) *fingers {
	f := &fingers{mt: d.Has("abs", absMtPositionX)}
	f.rel = !f.mt && !d.Has("abs", absX)
	xa, ya := absX, absY
	if f.mt {
		xa, ya = absMtPositionX, absMtPositionY
	}
	f.xr, f.yr = axis(io, d, xa), axis(io, d, ya)
	f.fixed = f.xr.ok && f.yr.ok
	return f
}

// feed takes one event; it reports true at the end of a frame (SYN_REPORT).
func (f *fingers) feed(e Event) bool {
	v := int(e.Value)
	switch e.Type {
	case EvSyn:
		if e.Code == evSynReport {
			f.most = max(f.most, f.count())
			f.learn()
			return true
		}
	case EvAbs:
		s := &f.slots[f.slot]
		switch e.Code {
		case absMtSlot:
			f.slot = min(max(v, 0), len(f.slots)-1)
		case absMtTrackingID:
			s.on = v >= 0
			if !s.on {
				s.p = 0
			}
		case absMtPositionX:
			s.x = v
		case absMtPositionY:
			s.y = v
		case absMtPressure:
			s.p = v
		case absX:
			f.x = v
		case absY:
			f.y = v
		case absPressure:
			f.p = v
		}
	case EvKey:
		n := map[uint16]int{btnToolFinger: 1, btnToolDoubletap: 2, btnToolTripletap: 3, btnToolQuadtap: 4, btnToolQuinttap: 5}[e.Code]
		switch {
		case e.Code == btnLeft:
			f.button = v != 0
			if v == 1 {
				f.pressed = true
				f.clicks++
			}
		case n > 0 && v == 1:
			f.tools = n
		case n > 0 && v == 0 && f.tools == n:
			f.tools = 0
		}
	case EvRel:
		if e.Code == relX || e.Code == relY {
			f.moved += max(v, -v)
		}
	}
	return false
}

// learn widens the seen ranges when the device's couldn't be read.
func (f *fingers) learn() {
	if f.fixed {
		return
	}
	for _, p := range f.raw() {
		f.xr = widen(f.xr, p[0])
		f.yr = widen(f.yr, p[1])
	}
}

func widen(s span, v int) span {
	if !s.ok && s.lo == 0 && s.hi == 0 {
		return span{v, v, false}
	}
	s.lo, s.hi = min(s.lo, v), max(s.hi, v)
	s.ok = s.hi > s.lo
	return s
}

// count is the fingers on the pad now.
func (f *fingers) count() int {
	if !f.mt {
		return f.tools
	}
	n := 0
	for _, s := range f.slots {
		if s.on {
			n++
		}
	}
	return n
}

func (f *fingers) raw() [][2]int {
	var out [][2]int
	if f.mt {
		for _, s := range f.slots {
			if s.on {
				out = append(out, [2]int{s.x, s.y})
			}
		}
	} else if f.tools > 0 && !f.rel {
		out = append(out, [2]int{f.x, f.y})
	}
	return out
}

// points are the fingers' positions, 0–1 across the pad.
func (f *fingers) points() [][2]float64 {
	var out [][2]float64
	for _, p := range f.raw() {
		out = append(out, [2]float64{f.xr.at(p[0]), f.yr.at(p[1])})
	}
	return out
}

// pressure is the highest per-finger pressure now (ABS_MT_PRESSURE).
func (f *fingers) pressure() int {
	p := 0
	for _, s := range f.slots {
		if s.on {
			p = max(p, s.p)
		}
	}
	return p
}

// touchpads are the built-in trackpads (the ones reporting positions), or else the built-in
// pointers (a trackpad in mouse mode). Those with pressure come first.
func touchpads(io IO) (pads []Device, mouse bool) {
	var ptrs []Device
	for _, d := range io.Inputs() {
		switch {
		case !builtIn(io, d):
		case isTouchpad(d):
			if d.Has("abs", absMtPressure) {
				pads = append([]Device{d}, pads...)
			} else {
				pads = append(pads, d)
			}
		case isPointer(d):
			ptrs = append(ptrs, d)
		}
	}
	if len(pads) == 0 {
		return ptrs, len(ptrs) > 0
	}
	return pads, false
}

// pointers are the input devices that move a pointer, for the evidence when there's no
// built-in trackpad.
func pointers(io IO) []Device {
	var out []Device
	for _, d := range io.Inputs() {
		if isTouchpad(d) || isPointer(d) {
			out = append(out, d)
		}
	}
	return out
}

// ── the trackpad test ─────────────────────────────────────────────────────────

type tpPart int

const (
	tpCorners tpPart = iota
	tpClick
	tpRight
	tpScroll
	tpPinch
	tpSwipe
)

var tpNames = map[tpPart]string{tpCorners: "four corners", tpClick: "click", tpRight: "two-finger click",
	tpScroll: "two-finger scroll up and down", tpPinch: "two-finger pinch", tpSwipe: "three-finger swipe"}

// Gesture sizes, as a share of the pad: scrolling moves the fingers this far up or down, a
// pinch changes their distance this much, a swipe moves three fingers this far sideways; a
// corner is this close to both edges.
const (
	tpScrollBy = 0.15
	tpPinchBy  = 0.2
	tpSwipeBy  = 0.2
	tpCorner   = 0.2
)

type trackpad struct {
	dev      Device
	id       string // its bus and IDs, for the evidence
	others   []Device
	in       Input
	f        *fingers
	parts    []tpPart
	done     map[tpPart]bool
	corners  [4]bool // top left, top right, bottom left, bottom right
	up, down bool    // the scroll's two directions
	n        int     // the finger count of the gesture under way
	c0       [2]float64
	d0       float64
	at       float64 // when the part on screen became the current one
	cur      tpPart
	final    string
	last     float64 // when the trackpad last sent anything
}

func (tp *trackpad) Start(c *Ctx) {
	c.Select(-1) // the fingers answer; a stray Enter mustn't
	tp.done = map[tpPart]bool{}
	if fixtureRun(c) {
		c.Decide(Outcome{Results: []checks.Result{notOnMac("input.trackpad")}, Say: i18n.T("a fixture tree has no trackpad to touch.")})
		return
	}
	pads, mouse := touchpads(c.IO)
	if len(pads) == 0 {
		ev := "no built-in trackpad among the input devices"
		if ps := pointers(c.IO); len(ps) > 0 {
			ev += "; pointing devices: " + devNames(c.IO, ps)
		}
		c.Decide(Outcome{Results: []checks.Result{Result("input.trackpad", checks.Failed, Automatic, ev, i18n.T("no built-in trackpad device"))},
			Say: i18n.T("the kernel shows no built-in trackpad. that's a fail.")})
		return
	}
	in, devs := OpenAll(c.IO, func(d Device) bool { return d.Event == pads[0].Event }, false)
	if len(devs) == 0 {
		in.Close()
		c.Decide(Outcome{Results: []checks.Result{NotTested("input.trackpad", checks.Other,
			"couldn't open the built-in trackpad's input device: "+devNames(c.IO, pads[:1]), i18n.T("couldn't open the trackpad"))},
			Say: i18n.T("I couldn't open the trackpad to listen to it. not tested.")})
		return
	}
	tp.dev, tp.others, tp.in = devs[0], pads[1:], in
	tp.id = readID(c.IO, tp.dev).String()
	tp.f = newFingers(c.IO, tp.dev)
	switch {
	case mouse:
		tp.parts = []tpPart{tpCorners, tpClick}
	case tp.f.mt:
		tp.parts = []tpPart{tpCorners, tpClick, tpRight, tpScroll, tpPinch, tpSwipe}
	default:
		tp.parts = []tpPart{tpCorners, tpClick, tpRight, tpScroll}
	}
	tp.at = 0.2
}

func (tp *trackpad) Step(c *Ctx) {
	if tp.in == nil {
		return
	}
	for _, e := range tp.in.Events() {
		tp.last = c.T
		if tp.f.feed(e) {
			tp.frame()
		}
	}
	if cur, ok := tp.next(); ok && cur != tp.cur {
		tp.cur, tp.at = cur, c.T
	}
	if _, left := tp.next(); left {
		return
	}
	if tp.f.rel {
		tp.final = checks.Partial
		c.Decide(Outcome{Results: []checks.Result{Result("input.trackpad", checks.Partial, Automatic,
			tp.evidence("; it works only as a mouse (relative motion and a button): no touches or finger positions, so no multi-touch"),
			i18n.T("mouse mode only: no multi-touch"))},
			Say: i18n.T("it moves and clicks, but only as a plain mouse. no multi-touch.")})
		return
	}
	tp.final = checks.Supported
	c.Decide(Outcome{Results: []checks.Result{Result("input.trackpad", checks.Supported, Automatic, tp.evidence(""), i18n.T("every gesture registered"))},
		Say: i18n.T("corners, clicks, scroll, pinch, swipe. all there.")})
}

// next is the first part not done yet.
func (tp *trackpad) next() (tpPart, bool) {
	for _, p := range tp.parts {
		if !tp.done[p] {
			return p, true
		}
	}
	return 0, false
}

// frame judges one frame of the trackpad's events.
func (tp *trackpad) frame() {
	f := tp.f
	n := f.count()
	if f.pressed {
		f.pressed = false
		switch {
		case n <= 1:
			tp.done[tpClick] = true
		case n == 2:
			tp.done[tpRight] = true
		}
	}
	if f.rel {
		tp.done[tpCorners] = tp.done[tpCorners] || f.moved >= 400
		return
	}
	pts := f.points()
	if f.fixed || f.xr.ok && f.yr.ok {
		for _, p := range pts {
			l, r := p[0] <= tpCorner, p[0] >= 1-tpCorner
			t, b := p[1] <= tpCorner, p[1] >= 1-tpCorner
			tp.corners[0] = tp.corners[0] || t && l
			tp.corners[1] = tp.corners[1] || t && r
			tp.corners[2] = tp.corners[2] || b && l
			tp.corners[3] = tp.corners[3] || b && r
		}
		tp.done[tpCorners] = tp.corners == [4]bool{true, true, true, true}
	}
	c, d := centre(pts), spread(pts)
	if n != tp.n { // a new gesture: its starting point
		tp.n, tp.c0, tp.d0 = n, c, d
		return
	}
	switch {
	case n == 2 && len(pts) >= 1:
		switch dy := c[1] - tp.c0[1]; {
		case dy <= -tpScrollBy:
			tp.up, tp.c0 = true, c
		case dy >= tpScrollBy:
			tp.down, tp.c0 = true, c
		}
		tp.done[tpScroll] = tp.up && tp.down
		if len(pts) == 2 && math.Abs(d-tp.d0) >= tpPinchBy {
			tp.done[tpPinch] = true
		}
	case n >= 3 && len(pts) >= 3:
		if math.Abs(c[0]-tp.c0[0]) >= tpSwipeBy {
			tp.done[tpSwipe] = true
		}
	}
}

// centre is the fingers' middle; spread the distance between the first two.
func centre(pts [][2]float64) [2]float64 {
	var c [2]float64
	for _, p := range pts {
		c[0] += p[0] / float64(len(pts))
		c[1] += p[1] / float64(len(pts))
	}
	return c
}

func spread(pts [][2]float64) float64 {
	if len(pts) < 2 {
		return 0
	}
	return math.Hypot(pts[0][0]-pts[1][0], pts[0][1]-pts[1][1])
}

func (tp *trackpad) evidence(tail string) string {
	kind := "multi-touch slots"
	switch {
	case tp.f.rel:
		kind = "mouse mode"
	case !tp.f.mt:
		kind = "one position and a finger count (no multi-touch slots: no pinch or three-finger swipe to test)"
	}
	ev := fmt.Sprintf("trackpad: %s (%s, %s), %s", tp.dev.Name, tp.dev.Event, tp.id, kind)
	if !tp.f.rel {
		if tp.f.fixed {
			ev += fmt.Sprintf(", x %s, y %s", tp.f.xr, tp.f.yr)
		} else {
			ev += ", axis ranges unreadable: corners judged against the positions seen"
		}
	}
	var got, not []string
	for _, p := range tp.parts {
		if tp.done[p] {
			got = append(got, tpNames[p])
		} else {
			not = append(not, tpNames[p])
		}
	}
	if len(got) > 0 {
		ev += "; registered: " + strings.Join(got, ", ")
	}
	if len(not) > 0 {
		ev += "; not registered: " + strings.Join(not, ", ")
	}
	if tp.f.most > 0 {
		ev += fmt.Sprintf("; up to %d fingers at once", tp.f.most)
	}
	if len(tp.others) > 0 {
		ev += "; other built-in pads: " + strings.Join(names(tp.others), ", ")
	}
	return ev + tail
}

func names(ds []Device) []string {
	var out []string
	for _, d := range ds {
		out = append(out, d.Name+" ("+d.Event+")")
	}
	return out
}

func (tp *trackpad) Act(c *Ctx, i int) {
	if i != 0 || tp.in == nil || c.T < 8 {
		return
	}
	got := 0
	for _, p := range tp.parts {
		if tp.done[p] {
			got++
		}
	}
	var o Outcome
	if got == 0 {
		o = Outcome{Results: []checks.Result{Result("input.trackpad", checks.Failed, Observed,
			tp.evidence("; the tester says nothing happens on the trackpad"), i18n.T("nothing registered"))},
			Say: i18n.T("fingers on, and not one event. that's a fail.")}
	} else {
		m := len(tp.parts) - got
		o = Outcome{Results: []checks.Result{Result("input.trackpad", checks.Partial, Observed,
			tp.evidence("; the tester says the rest doesn't work"), i18n.N("%d gesture didn't register", "%d gestures didn't register", m, m))},
			Say: i18n.N("%d thing on the list never showed up. partial.", "%d things on the list never showed up. partial.", m, m)}
	}
	tp.final = o.Results[0].Status
	c.Decide(o)
}

func (tp *trackpad) Stop(*Ctx) {
	if tp.in != nil {
		tp.in.Close()
	}
}

// label is a part's line in the list (translated).
func (tp *trackpad) label(p tpPart) string {
	switch p {
	case tpCorners:
		if tp.f != nil && tp.f.rel {
			return i18n.T("move a finger around")
		}
		n := 0
		for _, c := range tp.corners {
			if c {
				n++
			}
		}
		return i18n.T("touch all four corners (%d of 4)", n)
	case tpClick:
		return i18n.T("click")
	case tpRight:
		return i18n.T("click with two fingers")
	case tpScroll:
		switch {
		case tp.up && !tp.down:
			return i18n.T("scroll with two fingers: up seen, now down")
		case tp.down && !tp.up:
			return i18n.T("scroll with two fingers: down seen, now up")
		}
		return i18n.T("scroll with two fingers, up and down")
	case tpPinch:
		return i18n.T("pinch with two fingers")
	}
	return i18n.T("swipe sideways with three fingers")
}

func (tp *trackpad) View(c *Ctx) Screen {
	s := Screen{Face: boot.Idle, SayAt: tp.at, OutTitle: i18n.T("What I see")}
	if fixtureRun(c) {
		return fixtureScreen()
	}
	if tp.f == nil {
		s.Steps = []string{i18n.T("No built-in trackpad to test.")}
		s.Live = []Line{Bad(i18n.T("no built-in trackpad found"))}
		return s
	}
	cur, left := tp.next()
	for i, p := range tp.parts {
		s.Steps = append(s.Steps, tp.step(p))
		if p == cur && left {
			s.Step = i
		}
	}
	if !left {
		s.Step = len(s.Steps)
	}
	s.Happens = i18n.T("Any order works: each one ticks off when the trackpad reports it. Nothing on screen moves but the picture: I read the trackpad directly.")
	switch {
	case !left:
	case cur == tpCorners && tp.f.rel:
		s.Say = i18n.T("let's see what it reports. move around.")
	case cur == tpCorners:
		s.Say = i18n.T("corners first. all four.")
	case cur == tpClick:
		s.Say = i18n.T("now a click. press until it clicks.")
	case cur == tpRight:
		s.Say = i18n.T("two fingers, one click: that's a right click.")
	case cur == tpScroll:
		s.Say = i18n.T("two fingers. up, then down.")
	case cur == tpPinch:
		s.Say = i18n.T("pinch, like zooming a photo.")
	default:
		s.Say = i18n.T("three fingers, one swipe sideways.")
	}
	if tp.cur == tpCorners && tp.at < 1 {
		s.SayAt = 0.2
	}
	if c.T-tp.last >= 20 { // the fingers come through evdev, not the console: the run knows when they're idle
		s.Hint = i18n.T("stuck on one? the picture shows the move. any order works, and each ticks off as it comes.")
	}
	s.Live = []Line{Fact(i18n.T("trackpad: %s (%s)", tp.dev.Name, tp.dev.Event))}
	got := 0
	for _, p := range tp.parts {
		if tp.done[p] {
			got++
			s.Live = append(s.Live, Fact(i18n.T("seen: %s", tp.seen(p))))
		}
	}
	if n := tp.f.count(); n > 0 {
		s.Live = append(s.Live, Wait(i18n.N("%d finger on the pad", "%d fingers on the pad", n, n)))
	}
	s.Waiting = "·  " + i18n.T("%d of %d done", got, len(tp.parts))
	s.Anim = tp.draw
	a := Action{Label: i18n.T("The rest doesn't work"), Does: i18n.T("I tried what's still waiting; nothing ticks off"), Off: c.T < 8}
	if got == 0 {
		a.Label, a.Does = i18n.T("Nothing happens"), i18n.T("the trackpad doesn't react at all")
	}
	s.Actions = []Action{a}
	return s
}

// seen is a part as the Output names it once seen (translated).
func (tp *trackpad) seen(p tpPart) string {
	switch p {
	case tpCorners:
		if tp.f != nil && tp.f.rel {
			return i18n.T("a finger moving")
		}
		return i18n.T("all four corners")
	case tpClick:
		return i18n.T("a click")
	case tpRight:
		return i18n.T("a two-finger click")
	case tpScroll:
		return i18n.T("a two-finger scroll, up and down")
	case tpPinch:
		return i18n.T("a pinch")
	}
	return i18n.T("a three-finger swipe")
}

// step is a part as a step under "What to do" (translated).
func (tp *trackpad) step(p tpPart) string {
	switch p {
	case tpCorners:
		if tp.f != nil && tp.f.rel {
			return i18n.T("Move a finger around the trackpad.")
		}
		return i18n.T("Touch all four corners of the trackpad.")
	case tpClick:
		return i18n.T("Click the trackpad.")
	case tpRight:
		return i18n.T("Click with two fingers.")
	case tpScroll:
		return i18n.T("Scroll with two fingers: slide up, then down.")
	case tpPinch:
		return i18n.T("Pinch: two fingers together, or apart.")
	}
	return i18n.T("Swipe sideways with three fingers.")
}

// draw is the trackpad (prototype: a pad with a finger path): its corner targets, the move to
// make now drawn faintly inside it, and the fingers on it as the trackpad reports them; beside
// it, the move's name and how many are done.
func (tp *trackpad) draw(gr *g.Grid, x, y, w, h int, t float64) {
	if h < 6 || w < 40 {
		return
	}
	pw := min(int(float64(h)*2.8), w/2) // a trackpad is about 1.4 times as wide as tall; a cell twice as tall as wide
	iw, ih := pw-2, h-2
	outline(gr, x, y, pw, h, g.DIM)
	cur, left := tp.next()
	if left && !tp.f.rel { // the move, faint: the fingers draw over it
		mx, my := x+pw/2, y+h/2
		blink := int(t*2)%2 == 0
		switch cur {
		case tpClick, tpRight:
			if blink {
				gr.Put(mx-1, my, "▄▄", g.DIM)
				if cur == tpRight {
					gr.Put(mx+2, my, "▄▄", g.DIM)
				}
			}
			gr.Put(mx-2, my+2, i18n.T("click"), g.DIM)
		case tpScroll:
			for j := 1; j < ih-1; j++ {
				gr.Put(mx, y+1+j, "│", g.DIM)
				gr.Put(mx+3, y+1+j, "│", g.DIM)
			}
			gr.Put(mx, y+1, "↑", g.GRY)
			gr.Put(mx+3, y+1, "↑", g.GRY)
			gr.Put(mx, y+ih, "↓", g.GRY)
			gr.Put(mx+3, y+ih, "↓", g.GRY)
		case tpPinch:
			gr.Put(mx-8, my, "←──", g.GRY)
			gr.Put(mx+6, my, "──→", g.GRY)
		case tpSwipe:
			for k := 0; k < 3; k++ {
				gr.Put(x+3, my-1+k, strings.Repeat("─", iw-6)+"→", g.DIM)
			}
		}
	}
	if !tp.f.rel {
		for i, at := range [4][2]int{{0, 0}, {iw - 2, 0}, {0, ih - 1}, {iw - 2, ih - 1}} { // the corner targets
			r, c := '░', g.DIM
			if tp.corners[i] {
				r, c = '█', g.BVIO
			}
			gr.Set(x+1+at[0], y+1+at[1], r, c)
			gr.Set(x+2+at[0], y+1+at[1], r, c)
		}
		for _, p := range tp.f.points() { // the fingers, two cells wide
			px := x + 1 + int(math.Round(p[0]*float64(iw-2)))
			py := y + 1 + int(math.Round(p[1]*float64(ih-1)))
			c := g.WHT
			if tp.f.button {
				c = g.BVIO
			}
			gr.Set(px, py, '█', c)
			gr.Set(px+1, py, '█', c)
		}
	}
	lx, room := x+pw+3, x+w-x-pw-3
	got := 0
	for _, p := range tp.parts {
		if tp.done[p] {
			got++
		}
	}
	if left {
		for k, l := range wrapWords(tp.label(cur), room) {
			if k < 3 {
				gr.Put(lx, y+2+k, l, g.WHT)
			}
		}
	}
	gr.Put(lx, y+6, cutRunes(i18n.T("%d of %d done", got, len(tp.parts)), room), g.GRY)
	for i := range tp.parts {
		r, c := '·', g.DIM
		if tp.done[tp.parts[i]] {
			r, c = '■', g.BVIO
		}
		gr.Set(lx+i*2, y+8, r, c)
	}
}

// wrapWords breaks s into lines of at most n cells, at spaces.
func wrapWords(s string, n int) []string {
	var out []string
	line := ""
	for _, w := range strings.Fields(s) {
		switch {
		case line == "":
			line = w
		case len([]rune(line))+1+len([]rune(w)) <= n:
			line += " " + w
		default:
			out = append(out, line)
			line = w
		}
	}
	if line != "" {
		out = append(out, line)
	}
	for i, l := range out {
		out[i] = cutRunes(l, n)
	}
	return out
}

func cutRunes(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:max(0, n)])
	}
	return s
}

// ── the demo ──────────────────────────────────────────────────────────────────

// demoPad scripts a multi-touch trackpad: each frame sets the fingers (0–1 across the pad) and
// the button, the way bcm5974 reports them.
type demoPad struct {
	f      *Fake
	dev    string
	xr, yr span
	n      int // fingers in the last frame
	button bool
	p      []int // pressure per finger, when set
}

func (d *demoPad) frame(t float64, button bool, pts ...[2]float64) {
	ev := func(typ, code uint16, v int) { d.f.Event(t, d.dev, typ, code, int32(v)) }
	for i, p := range pts {
		ev(EvAbs, absMtSlot, i)
		ev(EvAbs, absMtTrackingID, 100+i)
		ev(EvAbs, absMtPositionX, d.xr.lo+int(p[0]*float64(d.xr.hi-d.xr.lo)))
		ev(EvAbs, absMtPositionY, d.yr.lo+int(p[1]*float64(d.yr.hi-d.yr.lo)))
		if i < len(d.p) {
			ev(EvAbs, absMtPressure, d.p[i])
		}
	}
	for i := len(pts); i < d.n; i++ {
		ev(EvAbs, absMtSlot, i)
		ev(EvAbs, absMtTrackingID, -1)
	}
	tools := []uint16{0, btnToolFinger, btnToolDoubletap, btnToolTripletap, btnToolQuadtap}
	if len(pts) != d.n {
		if d.n > 0 {
			ev(EvKey, tools[d.n], 0)
		}
		if len(pts) > 0 {
			ev(EvKey, tools[len(pts)], 1)
		}
	}
	if button != d.button {
		ev(EvKey, btnLeft, map[bool]int{true: 1, false: 0}[button])
	}
	ev(EvSyn, evSynReport, 0)
	d.n, d.button = len(pts), button
}

// stroke moves the fingers from a to b over dur seconds, ten frames a second.
func (d *demoPad) stroke(t, dur float64, a, b [][2]float64) float64 {
	steps := max(1, int(dur*10))
	for s := 0; s <= steps; s++ {
		k := float64(s) / float64(steps)
		pts := make([][2]float64, len(a))
		for i := range a {
			pts[i] = [2]float64{a[i][0] + (b[i][0]-a[i][0])*k, a[i][1] + (b[i][1]-a[i][1])*k}
		}
		d.frame(t+float64(s)/10, false, pts...)
	}
	return t + float64(steps)/10
}

// demoBcm5974 is the demo Mac's trackpad as bcm5974 describes it: multi-touch slots, the
// pointer-emulation axes, a button and the finger counts.
func demoBcm5974(f *Fake, event, name string, abs ...int) *demoPad {
	f.Device(event, name, map[string]string{"ev": "b",
		"key": capWords(btnLeft, btnToolFinger, 0x14a, btnToolDoubletap, btnToolTripletap, btnToolQuadtap, btnToolQuinttap),
		"abs": capWords(append([]int{absX, absY, absPressure, 0x1c, absMtSlot, 0x30, 0x31, 0x32, 0x33, 0x34, absMtPositionX, absMtPositionY, absMtTrackingID}, abs...)...)})
	d := &demoPad{f: f, dev: event, xr: span{-4828, 5345, true}, yr: span{-203, 6803, true}}
	for _, a := range []struct{ code, lo, hi int }{{absX, -4828, 5345}, {absY, -203, 6803}, {absMtPositionX, -4828, 5345}, {absMtPositionY, -203, 6803}} {
		f.Set(fmt.Sprintf("/dev/input/%s/absinfo/%d", event, a.code), fmt.Sprintf("%d %d", a.lo, a.hi))
	}
	return d
}

// demoTrackpad: the demo's MacBook Air trackpad goes through the list in about 11 s.
func demoTrackpad(f *Fake) {
	d := demoBcm5974(f, "event3", "bcm5974")
	demoID(f, "event3", busUSB, 0x05ac, 0x024c)
	one := func(x, y float64) [][2]float64 { return [][2]float64{{x, y}} }
	t := d.stroke(1.0, 0.6, one(0.08, 0.1), one(0.92, 0.1)) // along the top
	t = d.stroke(t, 0.5, one(0.92, 0.1), one(0.9, 0.9))     // down the right
	t = d.stroke(t, 0.6, one(0.9, 0.9), one(0.1, 0.92))     // along the bottom
	d.frame(t+0.3, false)
	d.frame(4.0, false, [2]float64{0.5, 0.7}) // a click
	d.frame(4.2, true, [2]float64{0.5, 0.7})
	d.frame(4.35, false, [2]float64{0.5, 0.7})
	d.frame(4.5, false)
	two := func(x, y, gap float64) [][2]float64 { return [][2]float64{{x - gap, y}, {x + gap, y}} }
	d.frame(5.4, false, two(0.5, 0.6, 0.08)...) // a two-finger click
	d.frame(5.6, true, two(0.5, 0.6, 0.08)...)
	d.frame(5.75, false, two(0.5, 0.6, 0.08)...)
	d.frame(5.9, false)
	t = d.stroke(6.6, 0.5, two(0.5, 0.7, 0.08), two(0.5, 0.35, 0.08)) // scroll up, then down
	t = d.stroke(t, 0.5, two(0.5, 0.35, 0.08), two(0.5, 0.7, 0.08))
	d.frame(t+0.1, false)
	t = d.stroke(8.3, 0.6, two(0.5, 0.5, 0.05), two(0.5, 0.5, 0.25)) // pinch apart
	d.frame(t+0.1, false)
	three := func(x float64) [][2]float64 { return [][2]float64{{x - 0.08, 0.5}, {x, 0.45}, {x + 0.08, 0.5}} }
	t = d.stroke(9.6, 0.6, three(0.3), three(0.65)) // swipe right
	d.frame(t+0.1, false)
}
