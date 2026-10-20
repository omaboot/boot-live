package handson

import (
	"fmt"
	"testing"
)

// wantCode is the code the run on screen waits for (the sleep test's, the external display's).
func wantCode(r *Runner) string {
	switch x := r.Run.(type) {
	case *sleepWake:
		return fmt.Sprint(x.code)
	case *extDisplay:
		return x.code
	}
	return ""
}

// wrongCode is a code that isn't code: each digit one up.
func wrongCode(code string) string {
	b := []byte(code)
	for i := range b {
		b[i] = '0' + (b[i]-'0'+1)%10
	}
	return string(b)
}

// typeIn types code into the field: the last digit checks it, no Enter.
func typeIn(r *Runner, code string) {
	for _, d := range code {
		r.CodeKey(string(d))
	}
}

// typeCode types the right code, or a wrong one.
func typeCode(r *Runner, right bool) {
	code := wantCode(r)
	if !right {
		code = wrongCode(code)
	}
	typeIn(r, code)
}

// The typed codes: Backspace and Enter do nothing; the third digit checks the code, no Enter;
// a wrong code twice never passes, however it's typed; the right one after a miss does.
func TestTypedCode(t *testing.T) {
	var tc typed
	var got []string
	for _, k := range []string{"4", "1", "backspace", "enter", "2", "7", "enter", "9", "8", "enter"} {
		if v, ok := tc.key(k); ok {
			got = append(got, v)
		}
	}
	if len(got) != 1 || got[0] != "427" {
		t.Fatalf("typed %q, want [427] checked at its third digit", got)
	}
	if tc.value != "98" {
		t.Errorf("after the check the field holds %q, want the next digits 98", tc.value)
	}
	if v, ok := tc.key("enter"); ok {
		t.Errorf("Enter on a short field gave %q", v)
	}
	for range 1000 { // a new code always differs from the old in at least two digits
		rng := proofRNG(NewFake())
		a := proofCode(rng)
		if b := proofCode(rng, a); differ(a, b) < 2 || b < 100 || b > 999 {
			t.Fatalf("codes %d then %d", a, b)
		}
	}
}

// Sleep and the external display: wrong twice is never a pass; wrong then right is.
func TestTypedCodeRules(t *testing.T) {
	cases := []struct {
		name, id string
		picks    map[float64]int
		want     string
	}{
		{"sleep: right", "power.sleep-wake", map[float64]int{2: 0, 12: pickRight}, "supported/challenge"},
		{"sleep: wrong, then right", "power.sleep-wake", map[float64]int{2: 0, 12: pickWrong, 15: pickRight}, "supported/challenge"},
		{"sleep: wrong twice", "power.sleep-wake", map[float64]int{2: 0, 12: pickWrong, 15: pickWrong}, "partial/challenge"},
		{"display: right", "graphics.external-display", map[float64]int{8: pickRight}, "supported/challenge"},
		{"display: wrong, then right", "graphics.external-display", map[float64]int{8: pickWrong, 9: pickRight}, "supported/challenge"},
		{"display: wrong twice", "graphics.external-display", map[float64]int{8: pickWrong, 9: pickWrong}, "not_tested/uncertain"},
	}
	for _, c := range cases {
		s, _ := Lookup(c.id)
		o := play(t, c.id, 20, s.Demo, c.picks)
		if got := status(o); got != c.want {
			t.Errorf("%s: %s, want %s", c.name, got, c.want)
		}
	}
	// the same wrong code twice, or the first code again after a new one is up: never a pass
	for _, id := range []string{"power.sleep-wake", "graphics.external-display"} {
		s, _ := Lookup(id)
		f := NewFake()
		DemoMac(f)
		s.Demo(f)
		r := Begin(s, &Env{}, f)
		first, n := "", 0
		for tt := 0.0; tt <= 20 && r.Outcome() == nil; tt += 0.1 {
			if id == "power.sleep-wake" && tt > 1.95 && tt < 2.05 {
				r.Act(0)
			}
			r.Step(tt)
			if r.Codes() && (n == 0 && tt > 12 || n == 1 && tt > 13) {
				if first == "" {
					first = wantCode(r)
					typeIn(r, wrongCode(first))
				} else {
					typeIn(r, first) // the first code: not the one up now
				}
				n++
			}
		}
		if got := status(r.Outcome()); got == "supported/challenge" || n != 2 {
			t.Errorf("%s: the old code passed (%s, %d tries)", id, got, n)
		}
		r.Stop()
	}
}
