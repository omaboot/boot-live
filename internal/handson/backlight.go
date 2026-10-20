package handson

import (
	"fmt"
	"math/rand/v2"
	"path"
	"strings"
	"time"

	"github.com/omaboot/boot-live/internal/boot"
	"github.com/omaboot/boot-live/internal/checks"
	g "github.com/omaboot/boot-live/internal/grid"
	"github.com/omaboot/boot-live/internal/i18n"
)

// The display's backlight and the keyboard's light: the tool steps the light through a few
// levels, and the person says what they saw. The original level is always put back, whichever
// way the screen is left.
//
// display.brightness, "Display brightness control":
//
//   - supported: the tester saw the screen dim and brighten as the backlight was stepped
//     (/sys/class/backlight, the device the kernel recommends: firmware, then platform, then
//     raw);
//   - partial: the tester saw only some of the steps;
//   - failed: no backlight device on a Mac with a built-in display, the kernel refused the
//     writes, or the tester saw no change.
//
// The picture check that used to follow it is a test of its own now (display_native.go).
//
// input.keyboard-backlight, "Keyboard backlight": the keyboard's LED (/sys/class/leds/
// *kbd_backlight*) goes dark, blinks a random 2 to 4 times and stays dark until the tester picks
// the number of blinks (a challenge; owner, 2026-10-13: a registered LED is no proof the keys
// light). Only then does it go back to where it was: in 0.5.0 a dim step and the way back came
// right after the last blink and read as one more (owner, 2026-10-13). Nothing on screen follows
// the blinks, so the screen can't give the count away. On Macs before the T2 the LED is
// applesmc's (the SMC key LKSB): OmaBoot? writes it only through the kernel's LED class, as an
// installed system's brightness keys do, and puts it back after (owner approved writing it,
// 2026-10-13).
//
//   - supported: the tester picked the right number of blinks (challenge);
//   - partial: a wrong number twice (a new count after the first miss): the light changed, but
//     not as written;
//   - failed: no LED, the kernel refused the writes, or the tester saw no change (observed).
//
// The key-light keys test (keylight.go) feeds the same criterion. Whether the driver's own
// read-back followed each step goes into the evidence only: the person is the sensor here.

func init() {
	Register(Spec{ID: "display.brightness", Group: NoPlug, Order: 40,
		Title: i18n.M("Display brightness"), Brief: i18n.M("watch the screen dim and brighten"), Expect: 30 * time.Second,
		New: newLight(screenLight), Demo: func(f *Fake) {}})
	Register(Spec{ID: "input.keyboard-backlight", Group: NoPlug, Order: 50, Title: i18n.M("Keyboard backlight"),
		Brief: i18n.M("count the keys' blinks"), Expect: 30 * time.Second, New: newLight(keyLight), Demo: func(f *Fake) {},
		Applies: kbdLED})
}

// kbdLED: the keyboard light's tests are offered where the LED is registered; the quick pass
// fails the criterion where it isn't.
func kbdLED(e *Env) bool {
	return e.Checks == nil || len(e.Checks.Root.Glob("/sys/class/leds/*kbd_backlight*")) > 0
}

// lightKind is what differs between the two.
type lightKind struct {
	id    string
	find  func(io IO) (dir, read string, all []string)  // the device's directory, the file that reads its level back, every candidate
	steps func(mx, orig, blinks int) ([]int, []float64) // the levels to show and how long each
	blink bool                                          // the tester counts blinks (the keyboard): the light stays dark after them
	none  string                                        // evidence when there's no device
}

var screenLight = lightKind{
	id: "display.brightness",
	find: func(io IO) (string, string, []string) {
		rank := map[string]int{"firmware": 0, "platform": 1, "raw": 2}
		best, br := "", 99
		var all []string
		for _, d := range io.Glob("/sys/class/backlight/*") {
			t := io.Read(d + "/type")
			all = append(all, path.Base(d)+" ("+t+")")
			r, ok := rank[t]
			if !ok {
				r = 3
			}
			if r < br {
				best, br = d, r
			}
		}
		return best, "actual_brightness", all
	},
	// dim, bright, then back to where it was
	steps: func(mx, orig, _ int) ([]int, []float64) {
		return []int{max(1, mx*15/100), mx, orig}, []float64{hold, hold, backHold}
	},
	none: "no backlight device (/sys/class/backlight is empty)",
}

// kbdLEDFind is the keyboard light's LED (the first *kbd_backlight*, as omarchy-brightness-keyboard
// picks it), every candidate, and the file that reads its level back.
func kbdLEDFind(io IO) (string, string, []string) {
	ds := io.Glob("/sys/class/leds/*kbd_backlight*")
	var all []string
	for _, d := range ds {
		all = append(all, path.Base(d))
	}
	if len(ds) == 0 {
		return "", "", nil
	}
	return ds[0], "brightness", all
}

var keyLight = lightKind{
	id:   "input.keyboard-backlight",
	find: kbdLEDFind,
	// dark first, then n blinks at full, each as long as the others (the last one too) and long
	// enough for the SMC's fade; it stays dark after the last until the tester answers
	steps: func(mx, orig, n int) ([]int, []float64) {
		lv, hs := []int{0}, []float64{1.0}
		for i := 0; i < n; i++ {
			lv, hs = append(lv, mx, 0), append(hs, blinkOn, blinkOff)
		}
		return lv, hs
	},
	blink: true,
	none:  "no keyboard backlight LED registered (/sys/class/leds/*kbd_backlight*)",
}

// hold is how long each brightness level shows; blinkOn and blinkOff are a blink's halves.
const (
	hold     = 1.4
	blinkOn  = 0.8
	blinkOff = 0.8
	backHold = 0.6 // the screen's original level, last
)

type light struct {
	k     lightKind
	rng   *rand.Rand
	dir   string
	read  string
	all   []string
	mx    int
	orig  int
	steps []int     // the levels of a play
	holds []float64 // how long each shows
	from  float64   // when the play began (-1: between plays)
	shown int       // plays completed
	ended float64   // when the last play ended
	cur   int       // the level written last (-1: none)
	back  []bool    // whether the read-back followed each written level
	err   error

	blinks int      // the keyboard: blinks in the play
	missed []string // wrong counts picked, for the evidence ("picked 3 for 2")
	said   int      // the count the tester picked last (-1: none; 0: no change)
}

func newLight(k lightKind) func(e *Env) Run {
	return func(*Env) Run { return &light{k: k, cur: -1, said: -1} }
}

func (l *light) Start(c *Ctx) {
	c.Select(-1) // nothing to answer until the first play is over
	l.rng = proofRNG(c.IO)
	l.dir, l.read, l.all = l.k.find(c.IO)
	if l.dir == "" {
		c.Decide(Outcome{Results: []checks.Result{Result(l.k.id, checks.Failed, Automatic, l.k.none, i18n.T("no light device"))},
			Say: i18n.T("nothing to dim here: the kernel has no light device for it.")})
		return
	}
	mx, ok := c.IO.Int(l.dir + "/max_brightness")
	if !ok || mx <= 0 {
		c.Decide(Outcome{Results: []checks.Result{Result(l.k.id, checks.Failed, Automatic, path.Base(l.dir)+": no max_brightness", i18n.T("no brightness range"))},
			Say: i18n.T("the light has no brightness range. nothing to step through.")})
		return
	}
	l.mx = mx
	l.orig, _ = c.IO.Int(l.dir + "/brightness")
	if l.k.blink {
		l.blinks = 2 + l.rng.IntN(3)
	}
	l.plan()
	l.from = 1.0 // a moment to read the screen first
}

// plan lays out the play.
func (l *light) plan() {
	l.steps, l.holds = l.k.steps(l.mx, l.orig, l.blinks)
	l.back = nil
}

// at is the step showing at t, or -1 between plays.
func (l *light) at(t float64) int {
	if l.from < 0 || t < l.from {
		return -1
	}
	s := l.from
	for i, h := range l.holds {
		if t < s+h {
			return i
		}
		s += h
	}
	return -1
}

// stepStart is when step i of the play began.
func (l *light) stepStart(i int) float64 {
	s := l.from
	for _, h := range l.holds[:i] {
		s += h
	}
	return s
}

// playing: a play is under way, or about to start.
func (l *light) playing(t float64) bool { return l.from >= 0 }

// again starts the play over shortly: Show it again, or a new count after a miss.
func (l *light) again(c *Ctx, in float64) {
	l.from = c.T + in
	c.Select(-1)
}

func (l *light) Step(c *Ctx) {
	if l.dir == "" || l.err != nil {
		return
	}
	i := l.at(c.T)
	if i < 0 {
		if l.from >= 0 && c.T >= l.from {
			l.from, l.ended = -1, c.T // the play is over
			l.shown++
			if l.k.blink {
				c.Select(0) // Show it again
			} else {
				c.Select(3)
			}
		}
		return
	}
	lvl := l.steps[i]
	if lvl == l.cur {
		if got, ok := c.IO.Int(l.dir + "/" + l.read); ok && c.T-l.stepStart(i) > 0.4 && len(l.back) == i {
			l.back = append(l.back, got == lvl)
		}
		return
	}
	if err := c.IO.Write(l.dir+"/brightness", fmt.Sprint(lvl)); err != nil {
		l.err = err
		l.restore(c)
		c.Decide(Outcome{Results: []checks.Result{Result(l.k.id, checks.Failed, Automatic,
			fmt.Sprintf("%s: the kernel refused brightness %d: %v", path.Base(l.dir), lvl, err), i18n.T("the kernel refused the change"))},
			Say: i18n.T("the kernel said no to the change. that's a fail.")})
		return
	}
	l.cur = lvl
	if len(l.back) > i { // a play again: the read-backs start over
		l.back = l.back[:i]
	}
}

func (l *light) restore(c *Ctx) {
	if l.dir != "" && l.cur >= 0 && l.cur != l.orig {
		c.IO.Write(l.dir+"/brightness", fmt.Sprint(l.orig))
		l.cur = l.orig
	}
}

func (l *light) Stop(c *Ctx) { l.restore(c) }

func (l *light) evidence(seen string) string {
	var ev string
	if l.k.blink {
		ev = fmt.Sprintf("%s went dark, blinked %d times between 0 and %d (written through the kernel's LED class) and stayed dark until the tester answered, then went back to %d; the tester %s",
			path.Base(l.dir), l.blinks, l.mx, l.orig, seen)
	} else {
		var lv []string
		for _, s := range l.steps {
			lv = append(lv, fmt.Sprint(s))
		}
		ev = fmt.Sprintf("%s stepped %s of %d and back (from %d); the tester %s", path.Base(l.dir), strings.Join(lv[:len(lv)-1], ", "), l.mx, l.orig, seen)
	}
	follow := 0
	for _, b := range l.back {
		if b {
			follow++
		}
	}
	if len(l.back) > 0 {
		ev += fmt.Sprintf("; the driver read back %d of %d levels as set", follow, len(l.back))
	}
	if len(l.all) > 1 {
		ev += "; devices: " + strings.Join(l.all, ", ")
	}
	return ev
}

// smcFixed marks a keyboard result that ran with OmaBoot?'s applesmc fix (it registers the LED
// before the T2).
func smcFixed(c *Ctx, r checks.Result) checks.Result {
	if e := c.Env.Checks; e != nil && e.Ident.Chip != "t2" {
		if f := e.Fixed(checks.FixApplesmc); f != nil {
			r.FixID, r.Fix = f.ID, f.Short
		}
	}
	return r
}

// answers: the keyboard's are "Show it again" (1), the counts (their own number keys) and
// "It didn't blink" (0), and only once the blinks are over (prototype hoKbd); the screen's are
// what was seen, then "Show it again", greyed while it plays.
func (l *light) answers(c *Ctx) []Action {
	busy := l.playing(c.T) || l.shown == 0
	if l.k.blink {
		if busy {
			return nil
		}
		a := []Action{{Label: i18n.T("Show it again"), Does: i18n.T("the same blinks"), Key: "1"}}
		for n := 2; n <= 4; n++ {
			a = append(a, Action{Label: i18n.N("It blinked %d time", "It blinked %d times", n, n), Key: fmt.Sprint(n)})
		}
		return append(a, Action{Label: i18n.T("It didn't blink"), Key: "0", Gap: true})
	}
	a := []Action{{Label: i18n.T("It dimmed and brightened")}, {Label: i18n.T("Only some steps"), Does: i18n.T("one way only")},
		{Label: i18n.T("No change")}, {Label: i18n.T("Show it again"), Gap: true}}
	for i := range a {
		a[i].Off = busy
	}
	return a
}

func (l *light) Act(c *Ctx, i int) {
	if l.shown == 0 || l.playing(c.T) {
		return
	}
	if l.k.blink {
		l.actBlinks(c, i)
		return
	}
	if i > 2 { // Show it again
		l.again(c, 0.3)
		return
	}
	l.restore(c)
	r, say := l.brightness(i)
	c.Decide(Outcome{Results: []checks.Result{r}, Say: say})
}

// actBlinks takes the keyboard's answer: again, a count (1–3 for 2–4 blinks), or no change.
func (l *light) actBlinks(c *Ctx, i int) {
	switch {
	case i == 0: // Show it again: the same blinks
		l.again(c, 0.3)
		return
	case i == 4:
		l.said = 0
		l.restore(c)
		c.Decide(Outcome{Results: []checks.Result{smcFixed(c, Result(l.k.id, checks.Failed, Observed, l.evidence("saw no change"), i18n.T("no change seen")))},
			Say: i18n.T("no change seen. the light didn't follow.")})
		return
	case i < 0 || i > 4:
		return
	}
	n := i + 1
	l.said = n
	if n == l.blinks {
		seen := "picked the right number of blinks (" + fmt.Sprint(n) + ")"
		if len(l.missed) > 0 {
			seen += " after " + strings.Join(l.missed, ", ")
		}
		l.restore(c)
		c.Decide(Outcome{Results: []checks.Result{smcFixed(c, Result(l.k.id, checks.Supported, Challenge, l.evidence(seen), i18n.T("%d blinks, counted right", n)))},
			Say: i18n.T("%d it was. the keys light up fine.", n)})
		return
	}
	l.missed = append(l.missed, fmt.Sprintf("picked %d for %d", n, l.blinks))
	if len(l.missed) >= 2 {
		l.restore(c)
		c.Decide(Outcome{Results: []checks.Result{smcFixed(c, Result(l.k.id, checks.Partial, Challenge,
			l.evidence(strings.Join(l.missed, ", then ")+": the light changed, but not as written"), i18n.T("blinks miscounted twice")))},
			Say: i18n.T("the light did something, but not what I asked. noted as partial.")})
		return
	}
	old := l.blinks
	for l.blinks == old {
		l.blinks = 2 + l.rng.IntN(3)
	}
	l.plan()
	l.again(c, 0.6)
}

// brightness is the display brightness result from the tester's answer.
func (l *light) brightness(seen int) (checks.Result, string) {
	switch seen {
	case 0:
		return Result(l.k.id, checks.Supported, Observed, l.evidence("saw every step"), i18n.T("every step seen")),
			i18n.T("brightness works. the panel listens.")
	case 1:
		return Result(l.k.id, checks.Partial, Observed, l.evidence("saw only some of the steps"), i18n.T("only some steps seen")),
			i18n.T("only some of it showed. noted as partial.")
	}
	return Result(l.k.id, checks.Failed, Observed, l.evidence("saw no change"), i18n.T("no change seen")),
		i18n.T("no change seen. the light didn't follow.")
}

func (l *light) View(c *Ctx) Screen {
	if l.k.blink {
		return l.viewKeys(c)
	}
	return l.viewScreen(c)
}

// step is where the play is for "What to do": 0 before it, 1 while it plays, 2 once it's over.
func (l *light) step(c *Ctx) int {
	switch {
	case l.shown == 0 && (l.from < 0 || c.T < l.from):
		return 0
	case l.playing(c.T):
		return 1
	}
	return 2
}

// device is the Output's first line: the light, its range and where it was. Read once at the
// start: nothing on screen follows the light while it plays.
func (l *light) device() Line {
	return Fact(i18n.T("%s: levels 0 to %d, now at %d", path.Base(l.dir), l.mx, l.orig))
}

func (l *light) viewScreen(c *Ctx) Screen {
	s := Screen{Face: boot.Idle, OutTitle: i18n.T("What I see"),
		Steps:   []string{i18n.T("Look at the screen."), i18n.T("Watch it dim, then brighten."), i18n.T("Pick what you saw.")},
		Happens: i18n.T("The backlight goes down low, up to full, then back. About 4 seconds."),
		Say:     i18n.T("eyes on the screen. lights down, lights up."), SayAt: 0.2,
		Hint: i18n.T("missed it? Show it again plays the steps once more."),
		Anim: screenArt}
	if l.dir == "" {
		s.Steps, s.Happens = []string{i18n.T("Nothing to step through.")}, ""
		s.Live = []Line{Bad(i18n.T("no light device found"))}
		return s
	}
	s.Step = l.step(c)
	s.Live = []Line{l.device()}
	switch s.Step {
	case 0:
		s.Live = append(s.Live, Wait(i18n.T("starting in a moment")))
		s.Waiting = "·  " + i18n.T("starting")
	case 1:
		s.Face = boot.Work
		s.Live = append(s.Live, Wait(i18n.T("stepping the backlight")))
		s.Waiting = "·  " + i18n.T("stepping the backlight")
	default:
		s.Live = append(s.Live, Fact(i18n.T("back at %d, where it was", l.orig)))
		s.Say, s.SayAt = i18n.T("did you see it change?"), l.ended
		s.Waiting = "·  " + i18n.T("waiting for your answer")
	}
	if len(l.all) > 1 {
		s.Live = append(s.Live, Gone(i18n.T("also here: %s", strings.Join(l.all, ", "))))
	}
	s.Actions = l.answers(c)
	return s
}

func (l *light) viewKeys(c *Ctx) Screen {
	s := Screen{Face: boot.Idle, OutTitle: i18n.T("What I see"),
		Steps: []string{i18n.T("Dim the room if you can; look at the keyboard."), i18n.T("Count the blinks."),
			i18n.T("Pick how many: press 2, 3 or 4.")},
		Happens: i18n.T("The keys go dark, blink a few times and stay dark until you answer. About 8 seconds."),
		Say:     i18n.T("eyes on the keyboard, not on me."), SayAt: 0.3,
		Hint: i18n.T("lost count? Show it again plays the same blinks. a darker room helps.")}
	decided := c.Outcome() != nil
	s.Anim = func(gr *g.Grid, x, y, w, h int, t float64) { kbdLightArt(gr, x, y, w, h, l.blinks, decided) }
	if l.dir == "" {
		s.Steps, s.Happens = []string{i18n.T("Nothing to blink.")}, ""
		s.Live = []Line{Bad(i18n.T("no light device found"))}
		return s
	}
	s.Step = l.step(c)
	s.Live = []Line{l.device()}
	switch {
	case s.Step == 0:
		s.Live = append(s.Live, Wait(i18n.T("starting in a moment")))
		s.Waiting = "·  " + i18n.T("starting")
	case s.Step == 1:
		s.Face = boot.Work
		s.Live = append(s.Live, Wait(i18n.T("playing the blinks (the count stays hidden until you answer)")))
		s.Waiting = "·  " + i18n.T("blinking")
	default:
		s.Live = append(s.Live, Fact(i18n.T("done; the keys stay dark until you answer")))
		s.Say, s.SayAt = i18n.T("how many did you count?"), l.ended
		if len(l.missed) > 0 {
			s.Say = i18n.T("not quite. that was a new count: how many this time?")
		}
		s.Waiting = "·  " + i18n.T("waiting for your count")
	}
	if decided { // only now does the screen say how many there were
		if l.said > 0 {
			s.Live = append(s.Live, Fact(i18n.N("you said %[1]d; it blinked %[2]d time", "you said %[1]d; it blinked %[2]d times", l.blinks, l.said, l.blinks)))
		} else if l.said == 0 {
			s.Live = append(s.Live, Fact(i18n.N("you saw no change; it blinked %d time", "you saw no change; it blinked %d times", l.blinks, l.blinks)))
		}
		if len(l.back) > 0 {
			n := 0
			for _, b := range l.back {
				if b {
					n++
				}
			}
			s.Live = append(s.Live, Fact(i18n.T("the driver read back %d of %d levels as set", n, len(l.back))))
		}
		s.Live = append(s.Live, Fact(i18n.T("back at %d, where it was", l.orig)))
	}
	if len(l.missed) > 0 && !decided && s.Step < 2 {
		s.Say, s.SayAt = i18n.T("not that many. a new count, watch again."), l.from-0.6
	}
	if len(l.all) > 1 {
		s.Live = append(s.Live, Gone(i18n.T("also here: %s", strings.Join(l.all, ", "))))
	}
	s.Actions = l.answers(c)
	return s
}
