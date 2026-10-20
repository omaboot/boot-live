// Package handson is the hands-on tests (LIVE-PLAN §21): after Quick results, "Keep going" leads
// to a "Boot needs you" screen and then one screen per test a person takes part in: what to do,
// what the machine sees live, and the person's answers. Tests without a plug-in come first, then
// the plug-in tests grouped, then the ones that put the Mac to sleep.
//
// A test is a Spec in the registry, one file per test (or per family of tests), registered from
// that file's init. The screens (internal/ui) drive the test's Run: Start when its screen opens,
// Step about fifteen times a second, View for what to draw, Act when the person picks one of its
// answers, Stop when the screen is left. A Run never blocks: slow work (a suspend, a network
// check) goes through the IO's futures, and input events through the IO's evdev reader. A Run
// decides by handing its Outcome to Ctx.Decide; the screen shows it with Boot's line and moves on.
//
// Each test's pass/partial/fail rule is written next to it, against the criterion's wording in
// doesitomarchy's data/capabilities.yaml, like the quick pass's checks.
package handson

import (
	"sort"
	"time"

	"github.com/omaboot/boot-live/internal/boot"
	"github.com/omaboot/boot-live/internal/checks"
	g "github.com/omaboot/boot-live/internal/grid"
	"github.com/omaboot/boot-live/internal/identify"
)

// Group is where a test goes in the flow (LIVE-PLAN §21.7).
type Group int

const (
	NoPlug Group = iota // nothing to plug in: first
	Plug                // something to plug in: grouped, so each plug-in happens once
	Last                // puts the Mac to sleep: last
)

// Methods a hands-on result can carry (pkg/report): Automatic when the machine decided,
// Observed when the person did, Challenge when the person answered a code the machine made.
const (
	Automatic = checks.Automatic
	Observed  = "observed"
	Challenge = "challenge"
)

// Spec is one hands-on test as the registry holds it.
type Spec struct {
	ID       string   // unique; the criterion it decides when it decides one
	Criteria []string // the criteria it decides, in order (default: ID)
	Group    Group
	Order    int // its place in its group (lower first)

	Title string // msgid: the frame's title, e.g. "Lid"
	Brief string // msgid: one line for "Boot needs you", e.g. "close the lid, then open it"
	// Gear is what the person needs at hand (msgid, e.g. "a USB stick"), or "" for nothing:
	// "Skip this one" then records no-equipment, and "Boot needs you" lists it under Grab.
	Gear string
	// Without is what skipping means when the gear isn't at hand (msgid, e.g. "no FireWire device
	// to plug in"): Boot's line and the report's evidence say it; "" says "it needs <Gear>".
	Without string
	Expect  time.Duration // roughly how long it takes with a person at the Mac

	// Applies, if set, narrows where the test is offered beyond its criteria applying to this
	// Mac (checks.Applies) and a live boot being allowed to decide them.
	Applies func(e *Env) bool
	// New starts a fresh run of the test (each visit to its screen gets a new one).
	New func(e *Env) Run
	// Demo sets up the --demo Mac for this test: the files it reads and what changes when (a
	// lid closing at 3 s). tools/screens and the demo play the real Run against it.
	Demo func(f *Fake)
	// Sleeps marks a test that puts the Mac to sleep: a Mac that doesn't wake loses the run, so
	// a copy of the report is saved to the stick before it starts.
	Sleeps bool
	// Stub marks a test that isn't built yet: kept out of the flow; its criteria stay
	// not_tested (not-in-profile) in the report.
	Stub bool
}

// IDs are the criteria the test decides.
func (s Spec) IDs() []string {
	if len(s.Criteria) > 0 {
		return s.Criteria
	}
	return []string{s.ID}
}

var registry = map[string]Spec{}

// Register adds a test; a test file calls it from its init. A second Spec with the same ID
// replaces the first (a phase-2 file replacing its stub).
func Register(s Spec) { registry[s.ID] = s }

// All are the registered tests in flow order: by group, then order, then ID.
func All() []Spec {
	out := make([]Spec, 0, len(registry))
	for _, s := range registry {
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.Group != b.Group {
			return a.Group < b.Group
		}
		if a.Order != b.Order {
			return a.Order < b.Order
		}
		return a.ID < b.ID
	})
	return out
}

// Lookup returns a registered test by ID.
func Lookup(id string) (Spec, bool) {
	s, ok := registry[id]
	return s, ok
}

// Env is what the tests work from.
type Env struct {
	// Checks is the quick pass's environment (hardware, identity, catalog, fixes); nil in the
	// demo, so tests must cope without it.
	Checks *checks.Env
	// Ports is the Mac's port map when set directly (the demo); otherwise Layout reads it from
	// the catalog.
	Ports []identify.PortSide
	// Wiring is this Mac's port wiring when set directly (the demo); otherwise wiring reads it
	// from the catalog (the snapshot's "wiring", from accepted reports, LIVE-PLAN §23):
	// connector → the stable paths seen on it ("left-2" → "usb2:0000:00:14.0/1", …).
	Wiring map[string][]string
	// USBA are the hub ports ("1-2", "2-2") the USB-A test counted as USB-A ports this run: the
	// USB-C test leaves them out (on Macs with both kinds a USB-C port's lines can run to the
	// same controller). Filled by that test; nil before.
	USBA map[string]bool
	// Phone is the phone sharing its connection over USB when the hands-on tests begin, or nil:
	// the USB port tests count its port like the OmaBoot? stick's.
	Phone *Phone
	// Online is the connection the run was using when the hands-on tests began ("en0, Ethernet"),
	// or "": a test that finds the Mac offline says the network hasn't come back since.
	Online string
	// Optical: the demo's Mac has an optical drive (on the Mac, the quick pass's look decides).
	Optical bool
	// Hung are the tests the run was picked up after (the Mac didn't come back from them), with
	// the failed outcome recorded for each (NoComeback): Sleep and wake offers light sleep then.
	Hung map[string]Outcome
}

// Phone is a phone on USB tethering, as the network step found it.
type Phone struct {
	USB    string // its USB device ("1-2", "2-1.3"; sysfs's name under /sys/bus/usb/devices)
	Name   string // its USB product string ("iPhone", "Pixel 7"), or ""
	Apple  bool   // an iPhone (or iPad): its tethering comes back by itself after a replug
	Online bool   // the run is online through it
}

// wiring is what DoesItOmarchy.com knows of the Mac's port wiring: Wiring, else the catalog's for
// the identified configuration; nil when nothing is known (every port check starts blank).
func (e *Env) wiring() map[string][]string {
	if e == nil {
		return nil
	}
	if len(e.Wiring) > 0 {
		return e.Wiring
	}
	if e.Checks != nil && e.Checks.Cat != nil {
		return e.Checks.Cat.Wired(e.Checks.Ident)
	}
	return nil
}

// Layout is the Mac's port map: Ports, else the catalog's for the identified configuration
// (identify.Catalog.Ports), an iMac's back mirrored as seen reaching round from the front
// (identify.MirrorBack; 0.7.3 review); nil when there's none. Ports is used as given (the demo
// and tools/screens mirror an iMac's themselves).
func (e *Env) Layout() []identify.PortSide {
	if e == nil {
		return nil
	}
	if len(e.Ports) > 0 {
		return e.Ports
	}
	if e.Checks != nil && e.Checks.Cat != nil {
		sides := e.Checks.Cat.Ports(e.Checks.Ident)
		if len(sides) > 0 && identify.AllInOne(e.Checks.Ident.Identifier) {
			return identify.MirrorBack(sides)
		}
		return sides
	}
	return nil
}

// Plan is the tests this Mac gets, in flow order: built (not stubs), and at least one of their
// criteria applies to this Mac and may be decided live; the Spec's own Applies narrows further.
func Plan(e *Env) []Spec {
	var out []Spec
	for _, s := range All() {
		if s.Stub || s.New == nil {
			continue
		}
		if s.Applies != nil && !s.Applies(e) {
			continue
		}
		if e.Checks != nil && len(Decidable(e, s)) == 0 {
			continue
		}
		out = append(out, s)
	}
	return out
}

// Decidable are the test's criteria that apply to this Mac and that a live boot may decide here.
func Decidable(e *Env, s Spec) []string {
	if e.Checks == nil {
		return s.IDs()
	}
	var out []string
	for _, id := range s.IDs() {
		if checks.Applies(id, e.Checks) && checks.Known(e.Checks, id) && !checks.LiveLimited(e.Checks, id) {
			out = append(out, id)
		}
	}
	return out
}

// Run is one visit to a test's screen. The screen calls its methods from one goroutine, so a Run
// needs no locks; none of them may block (see IO for the slow things).
type Run interface {
	Start(c *Ctx)       // the screen opened: read the starting state, open what it watches
	Step(c *Ctx)        // about 15 times a second while on screen: poll, advance, maybe Decide
	View(c *Ctx) Screen // what the screen shows now
	Act(c *Ctx, i int)  // the person picked the i-th of View's Actions
	Stop(c *Ctx)        // the screen is left (decided, skipped, back, Esc): undo what it changed, close watchers
}

// Skipper is a Run that takes "Skip this one" as an answer at times (the speakers' word, once
// both sides' beeps have decided the result): Skip reports whether it did; when it didn't, the
// test is skipped as usual. The Screen's SkipDoes says what skipping does then.
type Skipper interface {
	Skip(c *Ctx) bool
}

// Coder is a Run that asks for a typed code (Screen.Code is set): while the field is up, the
// digits typed, Backspace (while the field holds a digit) and Enter (once it holds one) go to
// CodeKey as "0"–"9", "backspace" and "enter"; the run keeps the field's Value and checks it as
// soon as the last digit is typed (no Enter needed: Enter does nothing). "s" still skips, and
// Backspace on an empty field still goes back (owner, 2026-10-13).
type Coder interface {
	CodeKey(c *Ctx, key string)
}

// Leaver is a Run with something that outlasts its decision (the microphone's encore, which
// plays after the run has decided and been stopped): Leave runs once its screen is left.
type Leaver interface {
	Leave(c *Ctx)
}

// KeyTaker is a Run that takes the keyboard while its screen is up (the keyboard test): every
// key the console gets goes to Key instead of the screen's own handling (no Esc, no back, no
// list), until TakesKeys says false again. The run must give the person a way out (its own
// "done" rule, or an evdev chord): see Input's grab.
type KeyTaker interface {
	TakesKeys(c *Ctx) bool
	Key(c *Ctx, key string)
}

// Ctx is a run's link to its screen and to the Mac.
type Ctx struct {
	T    float64 // seconds since the screen opened
	IO   IO
	Env  *Env
	Spec Spec

	out    *Outcome
	redraw bool
	sel    int  // the answer to put the ▶ cursor on (-1: none)
	selSet bool // Select was called since the screen last took it
}

// Decide hands over the run's outcome: the screen shows it and moves on after a beat. Only the
// first call counts.
func (c *Ctx) Decide(o Outcome) {
	if c.out == nil {
		c.out = &o
	}
}

// Select puts the screen's ▶ cursor on the i-th of View's Actions: a run calls it when a round
// or phase starts, for the answer most likely wanted then (the audio tests' "Play it again"). The
// cursor stays where the person moves it until the next call. Select(-1) puts it on nothing:
// while the test waits for something physical (a port, a hand over the sensor), so a stray
// Enter can't answer; ↑↓ or a number key picks one then (owner, 2026-10-13).
func (c *Ctx) Select(i int) { c.sel, c.selSet = max(-1, i), true }

// TakeSelect reports (once) the answer a Select asked the cursor onto (-1: none).
func (c *Ctx) TakeSelect() (int, bool) {
	ok := c.selSet
	c.selSet = false
	return c.sel, ok
}

// Outcome is the outcome handed over, or nil.
func (c *Ctx) Outcome() *Outcome { return c.out }

// Redraw asks for a full repaint of the console (after a resume, the screen may have lost it).
func (c *Ctx) Redraw() { c.redraw = true }

// TakeRedraw reports (once) whether a full repaint was asked for.
func (c *Ctx) TakeRedraw() bool {
	r := c.redraw
	c.redraw = false
	return r
}

// Outcome is what a run decided: one result per criterion, and Boot's line about it.
type Outcome struct {
	Results []checks.Result // ID, Status, Method, Reason (not_tested only), Evidence (English), Detail (translated)
	Say     string          // Boot's line (translated): explains, never softens
	// Warn is why running the test again may cost the run (translated), or "": Redo asks first.
	Warn string
}

// Result makes one criterion's result: method is Automatic, Observed or Challenge (ignored for
// not_tested); evidence is English, for the report; detail is the screen's short version
// (translated).
func Result(id, status, method, evidence, detail string) checks.Result {
	r := checks.Result{ID: id, Status: status, Method: method, Evidence: evidence, Detail: detail}
	if status == checks.NotTested {
		r.Method = ""
	}
	return r
}

// NotTested makes a not_tested result with a reason (checks.NoEquipment, checks.Uncertain,
// checks.Other).
func NotTested(id, reason, evidence, detail string) checks.Result {
	return checks.Result{ID: id, Status: checks.NotTested, Reason: reason, Evidence: evidence, Detail: detail}
}

// Question is a Screen's separate question: Title on the box's edge, Text in white, Note dim
// under it.
type Question struct {
	Title, Text, Note string
}

// Screen is what a test's screen shows. The frame, Boot, the divider, the answers' keys,
// "Skip this one", the result box and the hint bar are the screen's; the rest comes from here.
//
// The layout (owner-approved prototype v46, 2026-10-13): the band keeps Boot on the left; its
// field is 1/3 Boot's line in a violet box (◀ at him) and 2/3 the test's animation (Anim). Under
// the divider, the left column (about 46%) has "What to do" (Steps), "What happens", the code
// field (Code) and the answers; the right column has the Output box (Live, fixed height, each
// line with the m:ss it first showed) and the Result box under it. The port tests (Map, or
// FullBand) give the animation the band's whole field and Boot's box moves under the band, to
// the top of the left column (▲ at him).
//
// Every field has a fallback, so a test written for the older layout draws well unchanged:
// Do becomes the one current step, More becomes "What happens", Heading titles the Output box,
// a Panel that fits the band becomes its animation.
type Screen struct {
	// Steps are "What to do", numbered: one short sentence each, in order. Step is the one to do
	// now (0-based): those before it get a green ✓, it gets a violet number and white text, those
	// after it stay grey; Step = len(Steps) marks every one done (owner, 2026-10-13). With no
	// Steps, Do is the one current step.
	Steps []string
	Step  int
	Do    string   // what to do now: one short sentence, e.g. "Close the lid, then open it." (fallback for Steps)
	More  []string // details, what to expect: "What happens" when Happens is empty
	// Happens is "What happens", dim and wrapped: what the test does and what to expect
	// ("The keys go dark, blink a few times, then go back to how they were.").
	Happens string

	Say   string    // Boot's line now, lower case, in his voice; typed out from SayAt. He reacts; he never gives the instructions
	SayAt float64   // when the line started (the run's T)
	Face  boot.Expr // Boot's face ("" idle)
	// Hint is what Boot offers after 20 s with no key on the test (the box turns amber, "hint" in
	// its edge): a nudge, never the instructions themselves (owner, 2026-10-13). "" for none.
	Hint string

	// Anim draws the test's animation in the band, 2/3 of its field (the whole field with
	// FullBand), at the run's time: the keyboard, the sensor and the hand, the code to read.
	// nil: a Panel that fits the band's height draws there instead.
	Anim Panel
	// FullBand gives the animation the band's whole field and moves Boot's line under the band
	// (the port tests, owner, 2026-10-13). Implied by Map.
	FullBand bool
	// SayRows are the text rows of Boot's box under the band (FullBand, Map): as many as the
	// test's longest line needs at the narrowest console, fixed so nothing below it moves
	// (prototype: sized to the longest line); 0 for 3.
	SayRows int
	// Map is the Mac's port map with each port's state, drawn across the band's whole field (the
	// port tests); nil for none. Its height depends only on the Mac, so nothing moves.
	Map *PortMap

	// OutTitle is the Output box's title (translated): "What I see" when empty, or Heading.
	OutTitle string
	// Heading is the older name for OutTitle: "Found" for ports and devices, "What I hear" for
	// the microphone (owner, 2026-10-11); OutTitle wins when both are set.
	Heading string
	// Live are the Output box's lines, the machine's view, refreshed every step. The screen
	// stamps each line with the run's m:ss when it first showed its current text, draws the
	// newest bright while the test runs, and wraps a long line under itself.
	Live []Line
	// Panel is the test's own picture (a keyboard, a camera) PanelH rows tall: drawn as the band's
	// animation when Anim is nil and it fits the band, else in the left column above the answers.
	Panel  Panel
	PanelH int

	// Side is a small picture SideH rows tall at the top of the right column, over What I see,
	// which gives up those rows (the external display's cable and monitor, with its port map in
	// the band: owner, 2026-10-19). nil for none.
	Side  Panel
	SideH int

	// Code is a typed-code field (three boxed digits) above the answers, for a code the person
	// reads off the screen after a nap or from an external display; its keys go to the run's
	// CodeKey (Coder). nil for none.
	Code *CodeField

	// Question is a question that isn't part of the test (which port the OmaBoot? stick is in):
	// drawn in a violet box of its own in place of "What to do" and "What happens", with the
	// Actions as its answers and "Skip this one" under the box (owner, 2026-10-14). nil for none.
	Question *Question

	Ask     string   // a line over the answers: what they're about (the audio tests' "Left channel")
	Actions []Action // the person's answers; the screen adds "Skip this one" (key s) after them
	Note    string   // a dim line under the answers
	// SkipDoes is what "Skip this one" does now, when the run takes it as an answer (a Skipper):
	// "" for the usual "goes in the report as not tested".
	SkipDoes string
	// Waiting is the Result box's line before there is a result, dim: "·  waiting for your
	// count" ("" is "·  no result yet").
	Waiting string
	// Busy: something is under way that mustn't be interrupted (going to sleep): the screen
	// takes no keys and the screensaver waits.
	Busy bool
}

// Full reports whether the animation takes the band's whole field (Map or FullBand).
func (s Screen) Full() bool { return s.FullBand || s.Map != nil }

// CodeField is a typed code's field, as the run keeps it (prototype codeField): three boxed
// digits, the next one with the violet cursor, "type the 3 digits" beside them, and a
// line under them: Wrong (amber) after a wrong try, else Tries (dim, "2 tries").
type CodeField struct {
	Label string // what to type, over the boxes: "The code on the right"
	Value string // the digits typed so far
	Len   int    // how many digits (0: 3)
	Tries string // e.g. "2 tries"
	Wrong string // e.g. "not 913: one more try", or ""
}

// Line is one row of "What I see".
type Line struct {
	Mark  string // one cell; Spin draws the spinner
	Color g.Color
	Text  string
	Dim   bool // the text in dim grey
	Fixed bool // an OmaBoot? fix is in use: drawn with a FIX tag (owner, 0.4.5 review)
	// Side and Kind make a port's line (guide.line): "left side → [icon] USB 3 · …", the sides
	// padded so the arrows line up, the port kind's icon in Color (its state's).
	Side, Kind string
}

// Spin as a Line's Mark draws the spinner.
const Spin = "spin"

// Fact is a line the machine sees now (■, violet).
func Fact(text string) Line { return Line{Mark: "■", Color: g.BVIO, Text: text} }

// Wait is a line still waiting for something (the spinner).
func Wait(text string) Line { return Line{Mark: Spin, Color: g.BVIO, Text: text} }

// Gone is a line for something absent or not seen (·, dim).
func Gone(text string) Line { return Line{Mark: "·", Color: g.DIM, Text: text, Dim: true} }

// FixLine says an OmaBoot? fix is in use for what the test does now (a FIX tag before it).
func FixLine(text string) Line { return Line{Mark: "■", Color: g.BVIO, Text: text, Fixed: true} }

// Bad is a line for something wrong (×, red): use it for what makes a test fail.
func Bad(text string) Line { return Line{Mark: "×", Color: g.BRED, Text: text} }

// Panel draws a test's own picture into the box x, y, w×h at the run's time t.
type Panel func(gr *g.Grid, x, y, w, h int, t float64)

// Action is one of the person's answers on a test's screen. Each shows its key: Key, else its
// place among the answers (1, 2, 3 …); pressing the key picks it (owner, 2026-10-13: "2" picks
// "It blinked 2 times", whatever its place).
type Action struct {
	Label, Does string
	Off         bool   // shown greyed, never picked
	Gap         bool   // a blank row above it, setting it apart from the answers before it
	Key         string // its key, one digit ("2"); "" for its place. "s" is Skip's
	// Bars, 1 to 4, draws a Wi-Fi network's signal before Does as four squares, the strong ones
	// cyan, like the Network screen's list (owner, 0.6.1 review); 0 draws none.
	Bars int
}
