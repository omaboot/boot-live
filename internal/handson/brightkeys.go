package handson

import (
	"fmt"
	"path"
	"strings"
	"time"

	"github.com/omaboot/boot-live/internal/boot"
	"github.com/omaboot/boot-live/internal/checks"
	g "github.com/omaboot/boot-live/internal/grid"
	"github.com/omaboot/boot-live/internal/i18n"
)

// The brightness keys, a second test for display.brightness, "Display brightness control": the
// steps (backlight.go) show the backlight answers the kernel; this shows the keys made for it
// work. On a Mac's keyboard they're F1 (dimmer) and F2 (brighter), KEY_BRIGHTNESSDOWN/UP by
// hid_apple's fn table; Omarchy binds them to its display brightness command, the console has
// no such binding, so OmaBoot? moves the backlight on each press itself, a tenth of its range at
// a time, reading the keyboard's events (not grabbed: the number keys still answer). The
// keyboard is the Mac's own, or one of Apple's separate keyboards plugged into an iMac or a Mac
// mini (their top row is the same); another maker's keyboard isn't asked, as its keys say
// nothing about the Mac. The bar on screen follows the backlight, and it never goes dark: the
// lowest step is a tenth. The backlight goes back to where it was at the end.
//
//   - supported: a brighter press and a dimmer press each changed the backlight's level (read
//     back after the write), and the tester saw the screen follow (observed);
//   - partial: only one of the two changed it, and the tester says the other does nothing;
//   - failed: neither changed it and the tester says the keys do nothing, or both did and the
//     tester saw the screen stay the same (observed); the kernel refused the writes (automatic).
//
// A key that comes as its F-key (KEY_F1: the keyboard is set to F-keys first) moves nothing, as
// on Omarchy; the output says to hold fn. With the steps, Merge joins the two results into one
// (supported only when both are, failed only when both are, else partial).

func init() {
	Register(Spec{ID: "display.brightness.keys", Criteria: []string{"display.brightness"}, Group: NoPlug, Order: 41,
		Title: i18n.M("Brightness keys"), Brief: i18n.M("dim and brighten the screen with its own keys"), Expect: 30 * time.Second,
		Applies: brightKeyboard, New: func(*Env) Run { return &brightKeys{} }, Demo: demoBrightKeys})
}

// The brightness keys' codes (linux/input-event-codes.h).
const (
	keyBrightnessDown = 224
	keyBrightnessUp   = 225
)

// bkIdle is how long without a brightness key before "the keys do nothing" can be picked.
const bkIdle = 10.0

// brightKeyboard: offered when there's a backlight and a keyboard whose top row has the
// brightness keys: the Mac's own or an Apple one (vendor 05ac).
func brightKeyboard(e *Env) bool {
	if e.Checks == nil {
		return true
	}
	r := e.Checks.Root
	if len(r.Glob("/sys/class/backlight/*")) == 0 {
		return false
	}
	for _, d := range r.Glob("/sys/class/input/event*/device") {
		n := r.Read(d + "/name")
		if strings.Contains(n, "Internal Keyboard") || strings.Contains(n, "SPI Keyboard") ||
			strings.Contains(n, "Keyboard") && r.Read(d+"/id/vendor") == "05ac" {
			return true
		}
	}
	return false
}

// appleKeyboard: the Mac's built-in keyboard, or one of Apple's separate keyboards.
func appleKeyboard(io IO, d Device) bool {
	return isKeyboard(d) && (builtIn(io, d) || readID(io, d).vendor == 0x05ac)
}

type brightKeys struct {
	dir      string
	mx, orig int
	step     int // a tenth of the range, at least 1
	in       Input
	kbd      []Device
	id       devID // the first keyboard's, for the evidence
	upOK     bool  // a brighter press changed the backlight
	downOK   bool
	presses  int
	asFKey   map[uint16]bool // F-key codes seen instead
	lastKey  float64         // the last brightness key (or its F-key)
	pressAt  float64         // the last press, for the drawing
	pressUp  bool
	trail    []Line   // the Output: each press and what the backlight did
	log      []string // the same for the evidence, in English
	askAt    float64  // both seen: when the question went up (-1: not yet)
	said     string   // the answer, for the Output
}

func (k *brightKeys) Start(c *Ctx) {
	c.Select(-1)
	k.askAt, k.lastKey, k.pressAt, k.asFKey = -1, 0, -9, map[uint16]bool{}
	if fixtureRun(c) {
		c.Decide(Outcome{Results: []checks.Result{notOnMac("display.brightness")}, Say: i18n.T("a fixture tree has no keys to press.")})
		return
	}
	k.dir, _, _ = screenLight.find(c.IO)
	if k.dir == "" {
		c.Decide(Outcome{Results: []checks.Result{k.result(checks.Failed, Automatic, screenLight.none, i18n.T("no backlight device"))},
			Say: i18n.T("nothing to dim here: the kernel has no backlight for the screen.")})
		return
	}
	mx, ok := c.IO.Int(k.dir + "/max_brightness")
	if !ok || mx <= 0 {
		c.Decide(Outcome{Results: []checks.Result{k.result(checks.Failed, Automatic, path.Base(k.dir)+": no max_brightness", i18n.T("no brightness range"))},
			Say: i18n.T("the backlight has no brightness range. nothing to step through.")})
		return
	}
	k.mx, k.step = mx, max(1, mx/10)
	k.orig, _ = c.IO.Int(k.dir + "/brightness")
	in, devs := OpenAll(c.IO, func(d Device) bool { return appleKeyboard(c.IO, d) }, false)
	if len(devs) == 0 {
		in.Close()
		c.Decide(Outcome{Results: []checks.Result{NotTested("display.brightness", checks.Other, "couldn't open an Apple keyboard's input device",
			i18n.T("couldn't open the keyboard"))}, Say: i18n.T("I couldn't open the keyboard to listen to it. not tested.")})
		return
	}
	k.in, k.kbd, k.id = in, devs, readID(c.IO, devs[0])
}

func (k *brightKeys) result(status, method, ev, detail string) checks.Result {
	r := Result("display.brightness", status, method, ev, detail)
	r.Label = i18n.M("Display brightness")
	return r
}

func (k *brightKeys) Step(c *Ctx) {
	if k.in == nil {
		return
	}
	for _, e := range k.in.Events() {
		if e.Type != EvKey || e.Value == 0 {
			continue
		}
		switch e.Code {
		case keyBrightnessUp:
			k.press(c, true)
		case keyBrightnessDown:
			k.press(c, false)
		case fKeys[0], fKeys[1]:
			k.lastKey = c.T
			if !k.asFKey[e.Code] {
				k.asFKey[e.Code] = true
				name := "F2"
				if e.Code == fKeys[0] {
					name = "F1"
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

// press moves the backlight a tenth for one press, never below a tenth (the screen stays
// readable), and checks the backlight took it.
func (k *brightKeys) press(c *Ctx, up bool) {
	k.lastKey, k.pressAt, k.pressUp = c.T, c.T, up
	k.presses++
	name, word := "F1", i18n.T("dimmer")
	if up {
		name, word = "F2", i18n.T("brighter")
	}
	cur, _ := c.IO.Int(k.dir + "/brightness")
	want := max(k.step, cur-k.step)
	if up {
		want = min(k.mx, cur+k.step)
	}
	if want == cur {
		at := i18n.T("already at the lowest step")
		if up {
			at = i18n.T("already at the top")
		}
		k.trail = append(k.trail, Gone(i18n.T("%s (%s): %s", name, word, at)))
		k.log = append(k.log, fmt.Sprintf("%s: already at %d", name, cur))
		return
	}
	if err := c.IO.Write(k.dir+"/brightness", fmt.Sprint(want)); err != nil {
		k.restore(c)
		c.Decide(Outcome{Results: []checks.Result{k.result(checks.Failed, Automatic,
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
		k.trail = append(k.trail, Bad(i18n.T("%s (%s): the backlight stayed at %d", name, word, got)))
		k.log = append(k.log, fmt.Sprintf("%s: wrote %d, read back %d", name, want, got))
		return
	}
	k.trail = append(k.trail, Fact(i18n.T("%s (%s): %d of %d", name, word, got, k.mx)))
	k.log = append(k.log, fmt.Sprintf("%s: %d -> %d", name, cur, got))
}

func (k *brightKeys) restore(c *Ctx) {
	if k.dir == "" {
		return
	}
	if now, ok := c.IO.Int(k.dir + "/brightness"); !ok || now != k.orig {
		c.IO.Write(k.dir+"/brightness", fmt.Sprint(k.orig))
	}
}

func (k *brightKeys) Stop(c *Ctx) {
	if k.in != nil {
		k.in.Close()
		k.in = nil
	}
	if k.presses > 0 {
		k.restore(c)
	}
}

func (k *brightKeys) evidence(tail string) string {
	ev := fmt.Sprintf("%s (0 to %d, from %d), stepped by %d per press, on KEY_BRIGHTNESSUP/DOWN (F2, F1) from %s (%s): brighter %s, dimmer %s",
		path.Base(k.dir), k.mx, k.orig, k.step, k.kbd[0].Name, k.id, movedBL(k.upOK), movedBL(k.downOK))
	if len(k.log) > 0 {
		ev += "; presses: " + strings.Join(k.log, ", ")
	}
	return ev + "; back to " + fmt.Sprint(k.orig) + tail
}

func movedBL(ok bool) string {
	if ok {
		return "changed the backlight"
	}
	return "never changed it"
}

func (k *brightKeys) Act(c *Ctx, i int) {
	if k.in == nil || i != 0 && i != 1 {
		return
	}
	var o Outcome
	switch {
	case k.askAt >= 0 && i == 0:
		k.said = i18n.T("you saw the screen follow")
		o = Outcome{Results: []checks.Result{k.result(checks.Supported, Observed, k.evidence("; the tester saw the screen follow the keys"), i18n.T("both keys work; the screen followed"))},
			Say: i18n.T("keys and screen agree.")}
	case k.askAt >= 0:
		k.said = i18n.T("you saw the screen stay the same")
		o = Outcome{Results: []checks.Result{k.result(checks.Failed, Observed, k.evidence("; the backlight took the levels, but the tester saw the screen stay the same"), i18n.T("the backlight moved, the screen didn't"))},
			Say: i18n.T("the numbers moved, the screen didn't. that's a fail.")}
	case i == 0 && !k.idle(c):
		return
	case i == 0 && (k.upOK || k.downOK):
		k.said = i18n.T("you said the other key does nothing")
		o = Outcome{Results: []checks.Result{k.result(checks.Partial, Observed, k.evidence("; the tester says the other key does nothing"), i18n.T("only one of the keys works"))},
			Say: i18n.T("one key works, the other doesn't. partial.")}
	case i == 0:
		k.said = i18n.T("you said the keys do nothing")
		o = Outcome{Results: []checks.Result{k.result(checks.Failed, Observed, k.evidence("; the tester says the keys do nothing"), i18n.T("the keys don't move the backlight"))},
			Say: i18n.T("no key reached the backlight. that's a fail.")}
	default:
		return
	}
	k.restore(c)
	c.Decide(o)
}

// idle: long enough without a brightness key that "they do nothing" can be the answer.
func (k *brightKeys) idle(c *Ctx) bool { return c.T-k.lastKey >= bkIdle }

func (k *brightKeys) View(c *Ctx) Screen {
	s := Screen{Face: boot.Idle, OutTitle: i18n.T("What I see"),
		Steps: []string{i18n.T("Press F2 a few times (on a Mac set to F-keys first, hold fn)."), i18n.T("Now press F1 a few times."),
			i18n.T("Pick what the screen did.")},
		Happens: i18n.T("Each press moves the backlight a tenth of the way; it never goes dark. It goes back to where it was after."),
		Say:     i18n.T("brighter, please."), SayAt: 0.3,
		Hint: i18n.T("nothing moving? F1 and F2, the keys with the little sun on them. some Macs need fn held.")}
	if c.T-k.lastKey < 20 { // the keys come through evdev, not the console: the run knows when they're idle
		s.Hint = ""
	}
	if len(k.kbd) == 0 {
		s.Steps, s.Happens = []string{i18n.T("Nothing to press here.")}, ""
		if k.dir == "" {
			s.Live = []Line{Bad(i18n.T("no backlight device found"))}
		}
		return s
	}
	lvl, _ := c.IO.Int(k.dir + "/brightness")
	pressed := c.T-k.pressAt < 0.25
	s.Anim = func(gr *g.Grid, x, y, w, h int, _ float64) {
		brightKeysArt(gr, x, y, w, h, pressed, k.pressUp, lvl, k.mx)
	}
	decided := c.Outcome() != nil
	switch {
	case decided:
		s.Step = len(s.Steps)
	case k.askAt >= 0:
		s.Step = 2
		s.Say, s.SayAt = i18n.T("did the screen follow along?"), k.askAt
	case k.upOK:
		s.Step = 1
		s.Say, s.SayAt = i18n.T("good. now take it down."), k.pressAt
	default:
		if k.downOK {
			s.Say, s.SayAt = i18n.T("down works. now up."), k.pressAt
		}
	}
	s.Live = append([]Line{Fact(i18n.T("%s: 0 to %d, now %d; steps of a tenth", path.Base(k.dir), k.mx, k.orig)),
		Fact(i18n.T("keyboard: %s", k.kbd[0].Name))}, k.trail...)
	if k.askAt >= 0 {
		s.Live = append(s.Live, Fact(i18n.T("both keys arrived and moved the backlight")))
	}
	if decided && k.said != "" {
		s.Live = append(s.Live, Fact(i18n.T("%s; back to %d, as it was", k.said, k.orig)))
	}
	switch {
	case decided:
	case k.askAt >= 0:
		s.Actions = []Action{{Label: i18n.T("The screen followed the keys"), Does: i18n.T("brighter with F2, dimmer with F1")},
			{Label: i18n.T("The keys did nothing to the screen"), Does: i18n.T("it stayed the same")}}
		s.Waiting = "·  " + i18n.T("waiting for your answer")
	default:
		a := Action{Label: i18n.T("The keys do nothing"), Does: i18n.T("I pressed them, with fn too, and the screen didn't change")}
		if k.upOK || k.downOK {
			a = Action{Label: i18n.T("The other key does nothing"), Does: i18n.T("I pressed it, with fn too, and the screen didn't change")}
		}
		a.Off = !k.idle(c)
		s.Actions = []Action{a}
		s.Waiting = "·  " + i18n.T("waiting for %s", "F2")
		if k.upOK {
			s.Waiting = "·  " + i18n.T("waiting for %s", "F1")
		}
	}
	return s
}

// brightKeysArt is the brightness keys' animation: esc, F1, F2 and F3, the one pressed lit, and
// the backlight's level as a bar that follows it.
func brightKeysArt(gr *g.Grid, x, y, w, h int, pressed, pressUp bool, lvl, mx int) {
	keys := []struct{ k, what string }{{"esc", ""}, {"F1", i18n.T("dimmer")}, {"F2", i18n.T("brighter")}, {"F3", ""}}
	kx, ky := x+4, y+max(0, (h-8)/2)
	for i, key := range keys {
		bx := kx + i*10
		on := pressed && (key.k == "F2" && pressUp || key.k == "F1" && !pressUp)
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
	at := gr.Put(kx, by, i18n.T("backlight"), g.GRY) + 2
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

// demoBrightKeys: on the demo's MacBook Air, F2 three times (as the media key, hid_apple's
// default), then F1 twice.
func demoBrightKeys(f *Fake) {
	for _, t := range []float64{2.0, 2.6, 3.2} {
		f.Event(t, "event2", EvKey, keyBrightnessUp, 1)
		f.Event(t+0.1, "event2", EvKey, keyBrightnessUp, 0)
	}
	for _, t := range []float64{5.5, 6.0} {
		f.Event(t, "event2", EvKey, keyBrightnessDown, 1)
		f.Event(t+0.1, "event2", EvKey, keyBrightnessDown, 0)
	}
}
