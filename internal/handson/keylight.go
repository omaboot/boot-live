package handson

import (
	"fmt"
	"path"
	"slices"
	"strings"
	"time"

	"github.com/omaboot/boot-live/internal/boot"
	"github.com/omaboot/boot-live/internal/checks"
	g "github.com/omaboot/boot-live/internal/grid"
	"github.com/omaboot/boot-live/internal/i18n"
)

// The key-light keys (prototype hoKeys, owner 2026-10-13), a second test for
// input.keyboard-backlight, "Keyboard backlight": the blinks (backlight.go) show the light
// answers the kernel; this shows the keys made for it work. Omarchy binds them
// (XF86KbdBrightnessUp/Down, KEY_KBDILLUMUP/DOWN: F6 and F5 on most Macs, F10 and F9 on the 2006
// ones, by hid_apple's fn table) to omarchy-brightness-keyboard, which moves the first
// *kbd_backlight* LED by a tenth of its range; the console has no such binding, so OmaBoot? does
// what the script does on each press, reading the built-in keyboard's events (not grabbed: the
// number keys still answer). The bar on screen follows the LED: the tester drives it, so it gives
// nothing away. The LED goes back to where it was at the end.
//
//   - supported: a brighter press and a dimmer press each changed the LED's level (read back
//     after the write), and the tester saw the keys' light follow (observed);
//   - partial: only one of the two changed it, and the tester says the other does nothing;
//   - failed: neither changed it and the tester says the keys do nothing, or both did and the
//     tester saw the light stay the same (observed); the kernel refused the writes (automatic).
//
// A key that comes as its F-key (KEY_F6: the keyboard is set to F-keys first) moves nothing, as
// on Omarchy; the output says to hold fn. The blinks and this test decide one criterion: Merge
// joins their results (supported only when both are, failed only when both are, else partial).

func init() {
	Register(Spec{ID: "input.keyboard-backlight.keys", Criteria: []string{"input.keyboard-backlight"}, Group: NoPlug, Order: 52,
		Title: i18n.M("Key-light keys"), Brief: i18n.M("brighten and dim the keys with their own keys"), Expect: 30 * time.Second,
		Applies: kbdLED, New: func(*Env) Run { return &keyLightKeys{} }, Demo: demoKeyLightKeys})
}

// The key-light keys' codes (linux/input-event-codes.h).
const (
	keyKbdIllumDown = 229
	keyKbdIllumUp   = 230
)

// klIdle is how long without a key-light key before "the keys do nothing" can be picked.
const klIdle = 10.0

type keyLightKeys struct {
	dir      string
	all      []string
	mx, orig int
	step     int // a tenth of the range, at least 1 (omarchy-brightness-keyboard)
	in       Input
	kbd      []Device
	up, down string // the keys' legends: F6, F5
	fUp      uint16 // their F-key codes, when they come as F-keys
	fDown    uint16
	upOK     bool // a brighter press changed the LED
	downOK   bool
	presses  int
	asFKey   map[uint16]bool // F-key codes seen instead
	lastKey  float64         // the last key-light key (or its F-key)
	pressAt  float64         // the last press, for the drawing
	pressUp  bool
	trail    []Line   // the Output: each press and what the LED did
	log      []string // the same for the evidence, in English
	askAt    float64  // both seen: when the question went up (-1: not yet)
	said     string   // the answer, for the Output
}

func (k *keyLightKeys) Start(c *Ctx) {
	c.Select(-1)
	k.askAt, k.lastKey, k.pressAt, k.asFKey = -1, 0, -9, map[uint16]bool{}
	if fixtureRun(c) {
		c.Decide(Outcome{Results: []checks.Result{notOnMac("input.keyboard-backlight")}, Say: i18n.T("a fixture tree has no keys to press.")})
		return
	}
	k.dir, _, k.all = kbdLEDFind(c.IO)
	if k.dir == "" {
		c.Decide(Outcome{Results: []checks.Result{k.result(c, checks.Failed, Automatic, keyLight.none, i18n.T("no light device"))},
			Say: i18n.T("nothing to dim here: the kernel has no light device for it.")})
		return
	}
	mx, ok := c.IO.Int(k.dir + "/max_brightness")
	if !ok || mx <= 0 {
		c.Decide(Outcome{Results: []checks.Result{k.result(c, checks.Failed, Automatic, path.Base(k.dir)+": no max_brightness", i18n.T("no brightness range"))},
			Say: i18n.T("the light has no brightness range. nothing to step through.")})
		return
	}
	k.mx, k.step = mx, max(1, mx/10)
	k.orig, _ = c.IO.Int(k.dir + "/brightness")
	in, devs := OpenAll(c.IO, func(d Device) bool { return isKeyboard(d) && builtIn(c.IO, d) }, false)
	if len(devs) == 0 {
		in.Close()
		c.Decide(Outcome{Results: []checks.Result{NotTested("input.keyboard-backlight", checks.Other, "couldn't open the built-in keyboard's input device",
			i18n.T("couldn't open the keyboard"))}, Say: i18n.T("I couldn't open the keyboard to listen to it. not tested.")})
		return
	}
	k.in, k.kbd = in, devs
	k.up, k.down, k.fUp, k.fDown = "F6", "F5", fKeys[5], fKeys[4]
	t := fnTables[fnTable(readID(c.IO, devs[0]))]
	if i := slices.Index(t[:], keyKbdIllumUp); i >= 0 {
		k.up, k.fUp = fmt.Sprintf("F%d", i+1), fKeys[i]
	}
	if i := slices.Index(t[:], keyKbdIllumDown); i >= 0 {
		k.down, k.fDown = fmt.Sprintf("F%d", i+1), fKeys[i]
	}
}

func (k *keyLightKeys) result(c *Ctx, status, method, ev, detail string) checks.Result {
	r := Result("input.keyboard-backlight", status, method, ev, detail)
	r.Label = i18n.M("Keyboard backlight")
	return smcFixed(c, r)
}

func (k *keyLightKeys) Step(c *Ctx) {
	if k.in == nil {
		return
	}
	for _, e := range k.in.Events() {
		if e.Type != EvKey || e.Value == 0 {
			continue
		}
		switch e.Code {
		case keyKbdIllumUp:
			k.press(c, true)
		case keyKbdIllumDown:
			k.press(c, false)
		case k.fUp, k.fDown:
			k.lastKey = c.T
			if !k.asFKey[e.Code] {
				k.asFKey[e.Code] = true
				name := k.up
				if e.Code == k.fDown {
					name = k.down
				}
				k.trail = append(k.trail, Gone(i18n.T("%s came as an F-key: hold fn and press it again", name)))
				k.log = append(k.log, name+" came as an F-key")
			}
		}
		if c.Outcome() != nil {
			return
		}
	}
	if k.upOK && k.downOK && k.askAt < 0 {
		k.askAt = c.T
		c.Select(-1)
	}
}

// press does what omarchy-brightness-keyboard does for one press, and checks the LED took it.
func (k *keyLightKeys) press(c *Ctx, up bool) {
	k.lastKey, k.pressAt, k.pressUp = c.T, c.T, up
	k.presses++
	name, word := k.down, i18n.T("dimmer")
	if up {
		name, word = k.up, i18n.T("brighter")
	}
	cur, _ := c.IO.Int(k.dir + "/brightness")
	want := max(0, cur-k.step)
	if up {
		want = min(k.mx, cur+k.step)
	}
	if want == cur {
		at := i18n.T("already at the bottom")
		if up {
			at = i18n.T("already at the top")
		}
		k.trail = append(k.trail, Gone(i18n.T("%s (%s): %s", name, word, at)))
		k.log = append(k.log, fmt.Sprintf("%s: already at %d", name, cur))
		return
	}
	if err := c.IO.Write(k.dir+"/brightness", fmt.Sprint(want)); err != nil {
		k.restore(c)
		c.Decide(Outcome{Results: []checks.Result{k.result(c, checks.Failed, Automatic,
			fmt.Sprintf("%s: the kernel refused brightness %d after a %s press: %v", path.Base(k.dir), want, name, err), i18n.T("the kernel refused the change"))},
			Say: i18n.T("the kernel said no to the change. that's a fail.")})
		return
	}
	got, _ := c.IO.Int(k.dir + "/brightness")
	switch {
	case up && got > cur:
		k.upOK = true
	case !up && got < cur:
		k.downOK = true
	default:
		k.trail = append(k.trail, Bad(i18n.T("%s (%s): the light stayed at %d", name, word, got)))
		k.log = append(k.log, fmt.Sprintf("%s: wrote %d, read back %d", name, want, got))
		return
	}
	k.trail = append(k.trail, Fact(i18n.T("%s (%s): %d of %d", name, word, got, k.mx)))
	k.log = append(k.log, fmt.Sprintf("%s: %d -> %d", name, cur, got))
}

func (k *keyLightKeys) restore(c *Ctx) {
	if k.dir == "" {
		return
	}
	if now, ok := c.IO.Int(k.dir + "/brightness"); !ok || now != k.orig {
		c.IO.Write(k.dir+"/brightness", fmt.Sprint(k.orig))
	}
}

func (k *keyLightKeys) Stop(c *Ctx) {
	if k.in != nil {
		k.in.Close()
		k.in = nil
	}
	if k.presses > 0 {
		k.restore(c)
	}
}

func (k *keyLightKeys) evidence(tail string) string {
	ev := fmt.Sprintf("%s (0 to %d, from %d), stepped by %d per press as omarchy-brightness-keyboard does, on KEY_KBDILLUMUP/DOWN (%s, %s) from the built-in keyboard (%s): brighter %s, dimmer %s",
		path.Base(k.dir), k.mx, k.orig, k.step, k.up, k.down, k.kbd[0].Name, moved(k.upOK), moved(k.downOK))
	if len(k.log) > 0 {
		ev += "; presses: " + strings.Join(k.log, ", ")
	}
	return ev + "; back to " + fmt.Sprint(k.orig) + tail
}

func moved(ok bool) string {
	if ok {
		return "changed the LED"
	}
	return "never changed it"
}

func (k *keyLightKeys) Act(c *Ctx, i int) {
	if k.in == nil || i != 0 && i != 1 {
		return
	}
	var o Outcome
	switch {
	case k.askAt >= 0 && i == 0:
		k.said = i18n.T("you saw the light follow")
		o = Outcome{Results: []checks.Result{k.result(c, checks.Supported, Observed, k.evidence("; the tester saw the keys' light follow"), i18n.T("both keys work; the light followed"))},
			Say: i18n.T("keys and light agree.")}
	case k.askAt >= 0:
		k.said = i18n.T("you saw the light stay the same")
		o = Outcome{Results: []checks.Result{k.result(c, checks.Failed, Observed, k.evidence("; the LED took the levels, but the tester saw the keys' light stay the same"), i18n.T("the LED moved, the light didn't"))},
			Say: i18n.T("the numbers moved, the light didn't. that's a fail.")}
	case i == 0 && !k.idle(c):
		return
	case i == 0 && (k.upOK || k.downOK):
		k.said = i18n.T("you said the other key does nothing")
		o = Outcome{Results: []checks.Result{k.result(c, checks.Partial, Observed, k.evidence("; the tester says the other key does nothing"), i18n.T("only one of the keys works"))},
			Say: i18n.T("one key works, the other doesn't. partial.")}
	case i == 0:
		k.said = i18n.T("you said the keys do nothing")
		o = Outcome{Results: []checks.Result{k.result(c, checks.Failed, Observed, k.evidence("; the tester says the keys do nothing"), i18n.T("the keys don't move the light"))},
			Say: i18n.T("no key reached the light. that's a fail.")}
	default:
		return
	}
	k.restore(c)
	c.Decide(o)
}

// idle: long enough without a key-light key that "they do nothing" can be the answer.
func (k *keyLightKeys) idle(c *Ctx) bool { return c.T-k.lastKey >= klIdle }

func (k *keyLightKeys) View(c *Ctx) Screen {
	s := Screen{Face: boot.Idle, OutTitle: i18n.T("What I see"),
		Steps: []string{i18n.T("Press %s a few times (on a Mac set to F-keys first, hold fn).", k.up), i18n.T("Now press %s a few times.", k.down),
			i18n.T("Pick what the keys' light did.")},
		Happens: i18n.T("Each press moves the light a tenth of the way, as Omarchy does. The light goes back to where it was after."),
		Say:     i18n.T("brighter, please."), SayAt: 0.3,
		Hint: i18n.T("nothing moving? the keys with the little light on them. some Macs need fn held.")}
	if c.T-k.lastKey < 20 { // the keys come through evdev, not the console: the run knows when they're idle
		s.Hint = ""
	}
	if len(k.kbd) == 0 {
		s.Steps, s.Happens = []string{i18n.T("Nothing to press here.")}, ""
		if k.dir == "" {
			s.Live = []Line{Bad(i18n.T("no light device found"))}
		}
		return s
	}
	lvl, _ := c.IO.Int(k.dir + "/brightness")
	pressed := c.T-k.pressAt < 0.25
	up, down := k.up, k.down
	s.Anim = func(gr *g.Grid, x, y, w, h int, _ float64) {
		keyLightArt(gr, x, y, w, h, up, down, pressed, k.pressUp, lvl, k.mx)
	}
	decided := c.Outcome() != nil
	switch {
	case decided:
		s.Step = len(s.Steps)
	case k.askAt >= 0:
		s.Step = 2
		s.Say, s.SayAt = i18n.T("did the keys glow along?"), k.askAt
	case k.upOK:
		s.Step = 1
		s.Say, s.SayAt = i18n.T("good. now take it down."), k.pressAt
	default:
		if k.downOK {
			s.Say, s.SayAt = i18n.T("down works. now up."), k.pressAt
		}
	}
	s.Live = append([]Line{Fact(i18n.T("%s: 0 to %d, now %d; steps of a tenth, as Omarchy's keys", path.Base(k.dir), k.mx, k.orig))}, k.trail...)
	if k.askAt >= 0 {
		s.Live = append(s.Live, Fact(i18n.T("both keys arrived and moved the light")))
	}
	if decided && k.said != "" {
		s.Live = append(s.Live, Fact(i18n.T("%s; back to %d, as it was", k.said, k.orig)))
	}
	switch {
	case decided:
	case k.askAt >= 0:
		s.Actions = []Action{{Label: i18n.T("The light followed the keys"), Does: i18n.T("brighter with %s, dimmer with %s", k.up, k.down)},
			{Label: i18n.T("The keys did nothing to the light"), Does: i18n.T("it stayed the same")}}
		s.Waiting = "·  " + i18n.T("waiting for your answer")
	default:
		a := Action{Label: i18n.T("The keys do nothing"), Does: i18n.T("I pressed them, with fn too, and the light didn't move")}
		if k.upOK || k.downOK {
			a = Action{Label: i18n.T("The other key does nothing"), Does: i18n.T("I pressed it, with fn too, and the light didn't move")}
		}
		a.Off = !k.idle(c)
		s.Actions = []Action{a}
		s.Waiting = "·  " + i18n.T("waiting for %s", k.up)
		if k.upOK {
			s.Waiting = "·  " + i18n.T("waiting for %s", k.down)
		}
	}
	return s
}

// keyLightArt is the key-light keys' animation (prototype hoKeys): the keys around the two, the
// one pressed lit, and the light's level as a bar that follows the LED.
func keyLightArt(gr *g.Grid, x, y, w, h int, up, down string, pressed, pressUp bool, lvl, mx int) {
	var n int
	fmt.Sscanf(down, "F%d", &n)
	keys := []struct{ k, what string }{{fmt.Sprintf("F%d", n-1), ""}, {down, i18n.T("dimmer")}, {up, i18n.T("brighter")}, {fmt.Sprintf("F%d", n+2), ""}}
	kx, ky := x+4, y+max(0, (h-8)/2)
	for i, key := range keys {
		bx := kx + i*10
		on := pressed && (key.k == up && pressUp || key.k == down && !pressUp)
		live := key.what != ""
		col := g.DIM
		if live {
			col = g.VIO
		}
		outline(gr, bx, ky, 8, 3, col)
		fg, bg := g.DIM, g.K
		switch {
		case on:
			fg, bg = g.K, g.VIO
		case live:
			fg = g.WHT
		}
		gr.PutBG(bx+1, ky+1, "      ", fg, bg)
		gr.PutBG(bx+4-len(key.k)/2, ky+1, key.k, fg, bg)
		if key.what != "" {
			c := g.DIM
			if on {
				c = g.BVIO
			}
			gr.Put(bx+4-len([]rune(key.what))/2, ky+3, key.what, c)
		}
	}
	by := ky + 6
	at := gr.Put(kx, by, i18n.T("key light"), g.GRY) + 2
	k := 0
	if mx > 0 {
		k = (lvl*10 + mx/2) / mx
	}
	for i := 0; i < 10; i++ {
		if i < k {
			gr.Set(at+i*2, by, '█', g.BVIO)
		} else {
			gr.Set(at+i*2, by, '░', g.DIM)
		}
	}
	pct := 0
	if mx > 0 {
		pct = lvl * 100 / mx
	}
	gr.Put(at+21, by, fmt.Sprintf("%d %%", pct), g.WHT)
}

// demoKeyLightKeys: on the demo's MacBook Air, F6 four times (as the media key, hid_apple's
// default), then F5 twice.
func demoKeyLightKeys(f *Fake) {
	for _, t := range []float64{2.0, 2.6, 3.2, 3.8} {
		f.Event(t, "event2", EvKey, keyKbdIllumUp, 1)
		f.Event(t+0.1, "event2", EvKey, keyKbdIllumUp, 0)
	}
	for _, t := range []float64{6.0, 6.5} {
		f.Event(t, "event2", EvKey, keyKbdIllumDown, 1)
		f.Event(t+0.1, "event2", EvKey, keyKbdIllumDown, 0)
	}
}

// Merge joins results for the same criterion from more than one test (the keyboard light's
// blinks and its keys) into one, where the first of them stood: the report takes one result per
// criterion. A not_tested one gives way to a decided one; decided ones join as supported when
// all are, failed when all are, else partial (part of it works). The method and detail are those
// of the result that set the status (a supported one rests on a challenge when any did), the
// evidence is each one's, joined.
func Merge(rs []checks.Result) []checks.Result {
	at := map[string]int{}
	var out []checks.Result
	for _, x := range rs {
		i, ok := at[x.ID]
		if !ok {
			at[x.ID] = len(out)
			out = append(out, x)
			continue
		}
		out[i] = join(out[i], x)
	}
	return out
}

func join(a, b checks.Result) checks.Result {
	switch {
	case b.Status == checks.NotTested:
		return a
	case a.Status == checks.NotTested:
		b.Label = a.Label
		return b
	}
	r := a
	r.Evidence = a.Evidence + " | " + b.Evidence
	if r.Fix == "" {
		r.FixID, r.Fix = b.FixID, b.Fix
	}
	switch {
	case a.Status == checks.Supported && b.Status == checks.Supported:
		if b.Method == Challenge {
			r.Method = Challenge
		}
	case a.Status == checks.Failed && b.Status == checks.Failed:
	default:
		low := a // the one that keeps it from passing: the lower of the two
		if rankOf(b.Status) < rankOf(a.Status) {
			low = b
		}
		r.Status, r.Method, r.Detail = checks.Partial, low.Method, low.Detail
	}
	return r
}

// rankOf orders decided statuses: failed lowest.
func rankOf(s string) int {
	switch s {
	case checks.Failed:
		return 0
	case checks.Partial:
		return 1
	}
	return 2
}
