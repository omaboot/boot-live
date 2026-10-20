package handson

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/omaboot/boot-live/internal/sysroot"
)

// cardAnswer answers from what the screen shows now and what the monitor shows (the Fake's
// kms:code): an answer's index, or -1 when it typed a code instead.
type cardAnswer func(r *Runner, onMonitor string) int

// right types the code the monitor shows; wrong types one it doesn't.
func right(r *Runner, code string) int { typeIn(r, code); return -1 }

func wrong(r *Runner, code string) int {
	if code != "" {
		typeIn(r, wrongCode(code))
	}
	return -1
}

func fixed(i int) cardAnswer { return func(*Runner, string) int { return i } }

// playExt plays the external display test on the demo Mac (the monitor comes up at 4 s) with
// script's changes, answering at the given times; it returns the outcome and the Fake.
func playExt(t *testing.T, script func(*Fake), answers map[float64]cardAnswer) (*Outcome, *Fake) {
	t.Helper()
	s, _ := Lookup("graphics.external-display")
	f := NewFake()
	DemoMac(f)
	s.Demo(f)
	if script != nil {
		script(f)
	}
	r := Begin(s, &Env{}, f)
	defer r.Stop()
	for tt := 0.0; tt <= 20; tt += 0.1 {
		r.Step(tt)
		for at, a := range answers {
			if at > tt-0.05 && at <= tt+0.05 {
				if i := a(r, f.Read("kms:code")); i >= 0 {
					r.Act(i)
				}
			}
		}
		if o := r.Outcome(); o != nil {
			return o, f
		}
	}
	return nil, f
}

// The rules, and the routes to a false pass (none of which may come out supported): a yes with
// no code on the monitor, guessing, guessing again after a replug.
func TestExternalDisplay(t *testing.T) {
	const d = "/sys/class/drm/card1-DP-1/"
	noKMS := func(f *Fake) { f.Set("kms:fail", "DRM master refused") }
	cases := []struct {
		name    string
		script  func(*Fake)
		answers map[float64]cardAnswer
		want    string
	}{
		{"code read", nil, map[float64]cardAnswer{8: right}, "supported/challenge"},
		{"code read, no EDID", func(f *Fake) { f.SetAt(4.01, d+"edid", "") }, map[float64]cardAnswer{8: right}, "partial/challenge"},
		{"wrong, then right", nil, map[float64]cardAnswer{8: wrong, 9: right}, "supported/challenge"},
		{"wrong twice", nil, map[float64]cardAnswer{8: wrong, 9: wrong}, "not_tested/uncertain"},
		{"wrong, replugged, wrong", func(f *Fake) {
			f.SetAt(8.5, d+"status", "disconnected")
			f.SetAt(10, d+"status", "connected")
		}, map[float64]cardAnswer{8: wrong, 14: wrong}, "not_tested/uncertain"},
		{"dark", nil, map[float64]cardAnswer{8: fixed(cardDark)}, "partial/observed"},
		{"something's wrong", nil, map[float64]cardAnswer{8: fixed(cardWrong)}, "partial/observed"},
		{"no card: a yes isn't proof", noKMS, map[float64]cardAnswer{8: fixed(0)}, "not_tested/uncertain"},
		{"no card, dark", noKMS, map[float64]cardAnswer{8: fixed(1)}, "partial/observed"},
		{"no card, something's wrong", noKMS, map[float64]cardAnswer{8: fixed(2)}, "partial/observed"},
		{"answering before the card is up", nil, map[float64]cardAnswer{5.2: fixed(0), 5.3: fixed(cardDark)}, "undecided"},
	}
	for _, c := range cases {
		o, f := playExt(t, c.script, c.answers)
		if got := status(o); got != c.want {
			t.Errorf("%s: %s, want %s", c.name, got, c.want)
		}
		if n, _ := f.Int("kms:up"); n != 0 {
			t.Errorf("%s: %d cards left up", c.name, n)
		}
		if o != nil && c.want == "supported/challenge" && !strings.Contains(o.Results[0].Evidence, "EDID 128 bytes") {
			t.Errorf("%s: evidence %q", c.name, o.Results[0].Evidence)
		}
	}
	// any answer but the code, any number of times, never passes: the answers, wrong codes,
	// short ones, the old code again
	tries := []cardAnswer{fixed(0), fixed(1), fixed(2), fixed(3), wrong,
		func(r *Runner, code string) int { typeIn(r, code[:min(2, len(code))]); return -1 }}
	for i, a := range tries {
		for j, b := range tries {
			for _, script := range []func(*Fake){nil, noKMS} {
				old := ""
				first := func(r *Runner, code string) int { old = code; return a(r, code) }
				again := func(r *Runner, code string) int {
					if old != "" && old != code && j == len(tries)-1 { // the first code, after a new one went up
						typeIn(r, old)
						return -1
					}
					return b(r, code)
				}
				if o, _ := playExt(t, script, map[float64]cardAnswer{8: first, 9: again}); strings.HasPrefix(status(o), "supported") {
					t.Errorf("answers %d, %d: supported", i, j)
				}
			}
		}
	}
}

// On a fixture tree no card goes up: not tested, whatever the tester says.
func TestExternalDisplayFixture(t *testing.T) {
	dir := t.TempDir()
	c := filepath.Join(dir, "sys/class/drm/card1-DP-1")
	if err := os.MkdirAll(c, 0o755); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(c, "status"), []byte("connected\n"), 0o644)
	os.WriteFile(filepath.Join(c, "modes"), []byte("1920x1080\n"), 0o644)
	s, _ := Lookup("graphics.external-display")
	r := Begin(s, &Env{}, &System{Root: sysroot.Root(dir)})
	defer r.Stop()
	for tt := 0.0; tt <= 3 && r.Outcome() == nil; tt += 0.1 {
		r.Step(tt)
		r.Act(0)
	}
	if got := status(r.Outcome()); got != "not_tested/other" {
		t.Errorf("fixture: %s, want not_tested/other", got)
	}
}
