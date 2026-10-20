package handson

import (
	"strings"
	"testing"

	"github.com/omaboot/boot-live/internal/checks"
)

// The native resolution's picture check: two rounds, both named right, keep the quick pass's
// verdict; one wrong name in either round, or "The picture is wrong", marks it down; skipping
// leaves it as it is. Guessing one round right is never enough.
func TestNativePicture(t *testing.T) {
	env := fixtureEnv(t, "macbookair5,2") // the quick pass: supported
	cases := []struct {
		name  string
		picks map[float64]int
		want  string
	}{
		{"both rounds right", map[float64]int{1: pickRight, 2: pickRight}, "supported/challenge"},
		{"round 1 right, round 2 wrong", map[float64]int{1: pickRight, 2: pickWrong}, "partial/challenge"},
		{"round 1 wrong", map[float64]int{1: pickWrong}, "partial/challenge"},
		{"round 1 right, then no answer", map[float64]int{1: pickRight}, "undecided"},
		{"the picture is wrong", map[float64]int{1: patterns}, "partial/observed"},
		{"skipped", map[float64]int{1: -1}, "supported/automatic"},
		{"too quick: picked before the pattern settled", map[float64]int{0.3: pickRight, 0.5: pickRight}, "undecided"},
	}
	for _, c := range cases {
		o := playOn(t, env, DemoMac, nativeID, 10, nil, c.picks)
		if got := status(o); got != c.want {
			t.Errorf("%s: %s, want %s", c.name, got, c.want)
		}
	}
}

// The keyboard light stays dark after the blinks until the tester answers (owner, 2026-10-13: the
// way back right after the last blink read as one more), then goes back to where it was.
func TestKeyLightDarkUntilAnswered(t *testing.T) {
	s, _ := Lookup("input.keyboard-backlight")
	f := NewFake()
	DemoMac(f)
	r := Begin(s, &Env{}, f)
	defer r.Stop()
	led := "/sys/class/leds/smc::kbd_backlight/brightness"
	for tt := 0.0; tt < 20; tt += 0.1 {
		r.Step(tt)
	}
	if got := f.Read(led); got != "0" {
		t.Fatalf("after the blinks the light is at %s, want 0 until the answer", got)
	}
	l := r.Run.(*light)
	var lit []string // every level written: n blinks at full, nothing in between
	for _, w := range f.Writes {
		if v, ok := strings.CutPrefix(w, "/sys/class/leds/smc::kbd_backlight/brightness="); ok && v != "0" {
			lit = append(lit, v)
		}
	}
	if len(lit) != l.blinks {
		t.Errorf("lit %v for %d blinks", lit, l.blinks)
	}
	r.Act(answer(r, true))
	if got := f.Read(led); got != "40" || r.Outcome() == nil {
		t.Errorf("after the answer the light is at %s, want 40", got)
	}
}

// stuckLED is the demo Mac with a keyboard light that takes writes and never changes.
type stuckLED struct{ *Fake }

func (s stuckLED) Write(p, v string) error {
	if strings.Contains(p, "kbd_backlight") {
		s.Writes = append(s.Writes, p+"="+v)
		return nil
	}
	return s.Fake.Write(p, v)
}

// keyPresses scripts key-light presses on the demo's built-in keyboard.
func keyPresses(code uint16, at ...float64) func(*Fake) {
	return func(f *Fake) {
		for _, t := range at {
			f.Event(t, "event2", EvKey, code, 1)
			f.Event(t+0.1, "event2", EvKey, code, 0)
		}
	}
}

// The key-light keys pass only when a brighter and a dimmer press each changed the LED, and the
// tester saw the light follow; the LED goes back to where it was.
func TestKeyLightKeys(t *testing.T) {
	const id = "input.keyboard-backlight.keys"
	both := func(f *Fake) { demoKeyLightKeys(f) }
	cases := []struct {
		name   string
		stuck  bool
		script func(*Fake)
		picks  map[float64]int
		want   string
	}{
		{"both ways, the light followed", false, both, map[float64]int{9: 0}, "supported/observed"},
		{"both ways, the light stayed", false, both, map[float64]int{9: 1}, "failed/observed"},
		// false-pass routes: none of these may come out supported
		{"the LED never changes", true, both, map[float64]int{9: 0, 18: 0}, "failed/observed"},
		{"only brighter", false, keyPresses(keyKbdIllumUp, 2, 3), map[float64]int{9: 0, 14: 0}, "partial/observed"},
		{"only dimmer", false, keyPresses(keyKbdIllumDown, 2, 3), map[float64]int{14: 0}, "partial/observed"},
		{"F-keys only (fn not held)", false, func(f *Fake) {
			keyPresses(uint16(fKeys[5]), 2, 3)(f)
			keyPresses(uint16(fKeys[4]), 4, 5)(f)
		}, map[float64]int{9: 0, 16: 0}, "failed/observed"},
		{"nothing pressed, no answer", false, nil, nil, "undecided"},
	}
	for _, c := range cases {
		s, _ := Lookup(id)
		f := NewFake()
		DemoMac(f)
		if c.script != nil {
			c.script(f)
		}
		var io IO = f
		if c.stuck {
			io = stuckLED{f}
		}
		r := Begin(s, &Env{}, io)
		var o *Outcome
		for tt := 0.0; tt <= 25 && o == nil; tt += 0.1 {
			for at, a := range c.picks {
				if at > tt-0.1 && at <= tt {
					r.Act(a)
				}
			}
			r.Step(tt)
			o = r.Outcome()
		}
		r.Stop()
		if got := status(o); got != c.want {
			t.Errorf("%s: %s, want %s", c.name, got, c.want)
		}
		if got := f.Read("/sys/class/leds/smc::kbd_backlight/brightness"); got != "40" {
			t.Errorf("%s: the light left at %s, want 40", c.name, got)
		}
	}
}

// Two tests for one criterion join into one result: supported only when both are, failed only
// when both are, else partial; a not_tested one gives way.
func TestMerge(t *testing.T) {
	r := func(id, st, method string) checks.Result {
		return checks.Result{ID: id, Status: st, Method: method, Evidence: st, Detail: st}
	}
	kb := "input.keyboard-backlight"
	cases := []struct {
		a, b checks.Result
		want string
	}{
		{r(kb, checks.Supported, Challenge), r(kb, checks.Supported, Observed), "supported/challenge"},
		{r(kb, checks.Supported, Observed), r(kb, checks.Supported, Challenge), "supported/challenge"},
		{r(kb, checks.Supported, Challenge), r(kb, checks.Failed, Observed), "partial/observed"},
		{r(kb, checks.Failed, Observed), r(kb, checks.Supported, Observed), "partial/observed"},
		{r(kb, checks.Failed, Observed), r(kb, checks.Failed, Automatic), "failed/observed"},
		{r(kb, checks.Partial, Challenge), r(kb, checks.Supported, Observed), "partial/challenge"},
		{NotTested(kb, checks.Other, "", ""), r(kb, checks.Supported, Observed), "supported/observed"},
		{r(kb, checks.Failed, Observed), NotTested(kb, checks.Other, "", ""), "failed/observed"},
	}
	for i, c := range cases {
		out := Merge([]checks.Result{c.a, r("input.keyboard", checks.Supported, Automatic), c.b})
		if len(out) != 2 || out[0].ID != kb || out[1].ID != "input.keyboard" {
			t.Fatalf("%d: %+v", i, out)
		}
		if got := out[0].Status + "/" + out[0].Method; got != c.want {
			t.Errorf("%d: %s, want %s", i, got, c.want)
		}
	}
}
