package handson

import (
	"fmt"
	"math"
	"math/rand/v2"
	"path"
	"strings"
	"time"

	"github.com/omaboot/boot-live/internal/boot"
	"github.com/omaboot/boot-live/internal/checks"
	g "github.com/omaboot/boot-live/internal/grid"
	"github.com/omaboot/boot-live/internal/i18n"
)

// display.native-resolution, "Internal display at native resolution", is the quick pass's
// (checks/gpu.go: the right mode on the panel), but a right mode can still show a split or
// garbled picture (the iMac10,1's radeon before its fix). So the test draws a black-and-white
// test pattern in the band, one of six, and the tester names it; then a new one, and again (two
// rounds, no retry: a blind guess passes 1 in 30):
//
//   - both named right: the quick pass's verdict stands, the evidence saying "picture confirmed"
//     (a supported one then rests on the challenge);
//   - a wrong name in either round, or "The picture is wrong": a supported verdict becomes
//     partial, "the tester couldn't read the picture"; a partial or failed one stays as it is,
//     with that noted;
//   - skipped: the quick pass's verdict stays as it is, noted as not checked.
//
// The patterns are white blocks on black, nothing like the rest of the screen, so they can't be
// told apart by colour.

func init() {
	Register(Spec{ID: nativeID, Group: NoPlug, Order: 35, Title: i18n.M("Native resolution"),
		Brief: i18n.M("name two black-and-white pictures"), Expect: 30 * time.Second,
		New: func(*Env) Run { return &native{} }, Demo: func(f *Fake) {}})
}

// nativeID is the criterion the picture check amends.
const nativeID = "display.native-resolution"

// nativeRounds is how many patterns the tester names.
const nativeRounds = 2

// The six test patterns, each unlike the others at a glance even on a split
// or shifted picture. The box's X and the lone line are named so a diagonal can't fit both ("a box
// with a cross" read as "a diagonal").
const (
	patChecker = iota
	patUpright
	patFlat
	patCross
	patDiagonal
	patNested
	patterns
)

// patternName is the pattern's name, as the choices offer it (translated).
func patternName(p int) string {
	return i18n.S([...]string{i18n.M("A checkerboard"), i18n.M("Vertical stripes"), i18n.M("Horizontal stripes"),
		i18n.M("A box with an X"), i18n.M("One diagonal line"), i18n.M("Nested boxes")}[p])
}

// patternID is the pattern's name in the report (English).
func patternID(p int) string {
	return [...]string{"checkerboard", "vertical stripes", "horizontal stripes", "box with an X", "one diagonal line", "nested boxes"}[p]
}

// patternOn reports whether cell i, j of a w×h pattern p is white.
func patternOn(p, i, j, w, h int) bool {
	u, v := float64(i)/float64(max(1, w-1)), float64(j)/float64(max(1, h-1))
	switch p {
	case patChecker:
		return (i/4+j/2)%2 == 0
	case patUpright:
		return (i/3)%2 == 0
	case patFlat:
		return j%2 == 0
	case patCross:
		return i < 2 || i >= w-2 || j == 0 || j == h-1 || math.Abs(u-v) < 0.045 || math.Abs(u-(1-v)) < 0.045
	case patDiagonal:
		return math.Abs(u-v) < 0.06
	}
	d := min(i/2, (w-1-i)/2, j, h-1-j) // nested boxes
	return d%2 == 0
}

// patternArt draws pattern p over x, y, w×h: white blocks on black, every cell set.
func patternArt(gr *g.Grid, x, y, w, h, p int) {
	for j := 0; j < h; j++ {
		for i := 0; i < w; i++ {
			r := ' '
			if patternOn(p, i, j, w, h) {
				r = '█'
			}
			gr.SetBG(x+i, y+j, r, g.WHT, g.K)
		}
	}
}

type native struct {
	rng     *rand.Rand
	verdict *checks.Result // the quick pass's verdict, made again (nil: none to amend)
	pats    [nativeRounds]int
	round   int      // the round on screen
	at      float64  // when it went up
	picked  []string // each round's answer, for the evidence
	named   []int    // … the pattern named (patterns: "the picture is wrong")
}

func (n *native) Start(c *Ctx) {
	c.Select(-1) // the answers take a number key or ↑↓: a stray Enter mustn't pick one
	n.rng = proofRNG(c.IO)
	if e := c.Env.Checks; e != nil {
		r := checks.NativeResolution(e)
		n.verdict = &r
	} else { // the demo: its panel stands in for the quick pass's verdict
		n.verdict = demoNative(c.IO)
	}
	if n.verdict == nil {
		c.Decide(Outcome{Results: []checks.Result{NotTested(nativeID, checks.Other, "no internal panel connected to draw on", i18n.T("no panel to draw on"))},
			Say: i18n.T("no panel to draw on. not tested.")})
		return
	}
	n.pats[0] = n.rng.IntN(patterns)
	for n.pats[1] = n.rng.IntN(patterns); n.pats[1] == n.pats[0]; n.pats[1] = n.rng.IntN(patterns) {
	}
	n.at = 0
}

func (n *native) Step(*Ctx) {}

// settled: the round's pattern has been up long enough for its answers to take a key.
func (n *native) settled(c *Ctx) bool { return c.T-n.at >= proofSettle }

func (n *native) Act(c *Ctx, i int) {
	if n.verdict == nil || !n.settled(c) || c.Outcome() != nil {
		return
	}
	p := n.pats[n.round]
	if i >= 0 && i <= patterns {
		n.named = append(n.named, i)
	}
	switch {
	case i == p:
		n.picked = append(n.picked, fmt.Sprintf("round %d: drew %s, picked it", n.round+1, patternID(p)))
		if n.round+1 < nativeRounds {
			n.round, n.at = n.round+1, c.T
			c.Select(-1)
			return
		}
		n.decide(c, true, false, "picture confirmed (two test patterns, both named by the tester: "+strings.Join(n.picked, "; ")+")")
	case i >= 0 && i < patterns:
		n.picked = append(n.picked, fmt.Sprintf("round %d: drew %s, picked %s", n.round+1, patternID(p), patternID(i)))
		n.decide(c, false, false, "the tester couldn't read the picture: "+strings.Join(n.picked, "; "))
	case i == patterns:
		note := fmt.Sprintf("the tester couldn't read the picture: something's wrong with it (split, shifted, blurry or stretched), round %d (%s drawn)", n.round+1, patternID(p))
		if len(n.picked) > 0 {
			note += "; first " + strings.Join(n.picked, "; ")
		}
		n.decide(c, false, true, note)
	}
}

// decide confirms the quick pass's verdict or marks it down.
func (n *native) decide(c *Ctx, read, told bool, note string) {
	r := *n.verdict
	r.Evidence += "; " + note
	say := i18n.T("native and crisp.")
	switch {
	case read:
		if r.Status == checks.Supported {
			r.Method = Challenge
		}
		r.Detail = i18n.T("%s; picture right twice", r.Detail)
	case r.Status == checks.Supported:
		r.Status, r.Method = checks.Partial, Challenge
		if told {
			r.Method = Observed
		}
		r.Detail = i18n.T("the tester couldn't read the picture")
		say = i18n.T("the picture didn't come through right. the resolution is partial.")
	default:
		r.Detail = i18n.T("%s; the picture wasn't readable", r.Detail)
		say = i18n.T("the picture didn't come through right either. noted.")
	}
	c.Decide(Outcome{Results: []checks.Result{r}, Say: say})
}

// Skip keeps the quick pass's verdict as it is, noted as not checked.
func (n *native) Skip(c *Ctx) bool {
	if n.verdict == nil {
		return false
	}
	r := *n.verdict
	r.Evidence += "; the tester skipped the picture check"
	r.Detail = i18n.T("%s; picture not checked", r.Detail)
	c.Decide(Outcome{Results: []checks.Result{r}, Say: i18n.T("skipped. the quick pass's result stands.")})
	return true
}

func (n *native) Stop(*Ctx) {}

// demoNative is the demo's stand-in for the quick pass's native resolution verdict: its panel at
// its first mode.
func demoNative(io IO) *checks.Result {
	for _, d := range io.Glob("/sys/class/drm/card*-eDP-*") {
		if io.Read(d+"/status") == "connected" {
			name, mode := strings.SplitN(path.Base(d), "-", 2)[1], strings.Fields(io.Read(d+"/modes"))
			if len(mode) == 0 {
				break
			}
			r := Result(nativeID, checks.Supported, Automatic, name+" connected, preferred mode "+mode[0]+" (the demo's quick pass)",
				i18n.T("%s, %s preferred mode", name, strings.Replace(mode[0], "x", "×", 1)))
			r.Label = i18n.M("Internal display, native resolution")
			return &r
		}
	}
	return nil
}

func (n *native) View(c *Ctx) Screen {
	decided := c.Outcome() != nil
	s := Screen{Face: boot.Idle, OutTitle: i18n.T("What I see"),
		Steps: []string{i18n.T("Look at the black-and-white pattern."), i18n.T("Pick the pattern you see (round 1 of 2)."),
			i18n.T("A new pattern: pick again (round 2 of 2).")},
		Happens: i18n.T("The console runs at the panel's own resolution; this checks the picture is whole and sharp."),
		Say:     i18n.T("sharp and whole, or not?"), SayAt: 0.3,
		Hint: i18n.T("look at the black-and-white picture by me: stripes, boxes, a checkerboard? the number keys pick.")}
	if n.verdict == nil {
		s.Steps, s.Happens = []string{i18n.T("Nothing to draw on.")}, ""
		s.Live = []Line{Bad(i18n.T("no internal panel found"))}
		return s
	}
	s.Live = []Line{Fact(i18n.S(n.verdict.Detail))}
	for k := range n.named {
		s.Live = append(s.Live, Fact(n.roundLine(k)))
	}
	round, p := n.round, n.pats[n.round]
	switch {
	case decided:
		s.Step = len(s.Steps)
	case round == 0 && c.T < 1.5:
		s.Step = 0
	default:
		s.Step = 1 + round
	}
	if round > 0 && !decided {
		s.Say, s.SayAt = i18n.T("right. a new one."), n.at
	}
	s.Anim = func(gr *g.Grid, x, y, w, h int, _ float64) {
		if !decided {
			patternArt(gr, x+2, y, w-4, h, p)
			return
		}
		if len(n.named) == nativeRounds && n.named[1] == n.pats[1] {
			gr.Put(x+2, y+2, cutRunes(i18n.T("both patterns came out right"), w-4), g.GRY)
		}
	}
	if !decided {
		s.Waiting = "·  " + i18n.T("round %d of %d", round+1, nativeRounds)
		off := !n.settled(c)
		for i := 0; i < patterns; i++ {
			s.Actions = append(s.Actions, Action{Label: patternName(i), Off: off})
		}
		s.Actions = append(s.Actions, Action{Label: i18n.T("The picture is wrong"), Does: i18n.T("split, shifted or blurry"), Off: off})
	}
	s.SkipDoes = i18n.T("the quick pass's result stands")
	return s
}

// roundLine is round k's line in the Output, once answered: what was drawn and what was named.
func (n *native) roundLine(k int) string {
	p, i := n.pats[k], n.named[k]
	switch {
	case i == p:
		return i18n.T("round %d: %s, and you named it", k+1, patternName(p))
	case i == patterns:
		return i18n.T("round %d: %s; you said the picture is wrong", k+1, patternName(p))
	}
	return i18n.T("round %d: %s; you named %s", k+1, patternName(p), patternName(i))
}
