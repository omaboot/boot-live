package handson

import (
	"fmt"
	"math/rand/v2"
	"strings"
	"time"

	g "github.com/omaboot/boot-live/internal/grid"
	"github.com/omaboot/boot-live/internal/i18n"
)

// Challenges: where a person decides, they prove what they saw by answering something the
// machine made up (owner, 2026-10-13: "no false passes"): the keyboard backlight's blink count
// and the native resolution's test patterns (display_native.go) are picked from choices; the
// sleep test's code and the external display's are typed (see Typed codes below).

// proofSettle is how long a new challenge shows before its answers take a key: a quick second
// Enter on the answer before mustn't pick one blind.
const proofSettle = 0.8

// proofSeeder is an IO that gives the challenges' random seed: the Mac's changes every run, the
// Fake's is fixed, so the demo and the screens always draw the same.
type proofSeeder interface{ Seed() uint64 }

// proofRNG is a random source for a run's challenges.
func proofRNG(io IO) *rand.Rand {
	seed := uint64(time.Now().UnixNano())
	if s, ok := io.(proofSeeder); ok {
		seed = s.Seed()
	}
	return rand.New(rand.NewPCG(seed, seed^0x5851f42d4c957f2d))
}

// differ counts the digits two 3-digit codes differ in.
func differ(a, b int) int {
	n := 0
	for i := 0; i < 3; i++ {
		if a%10 != b%10 {
			n++
		}
		a, b = a/10, b/10
	}
	return n
}

// proofCode is a 3-digit code (100–999) differing from each of avoid in at least two digits, so
// a new code after a miss can't be mistaken for the old one at a glance.
func proofCode(rng *rand.Rand, avoid ...int) int {
	for {
		code, ok := 100+rng.IntN(900), true
		for _, a := range avoid {
			ok = ok && differ(code, a) >= 2
		}
		if ok {
			return code
		}
	}
}

// ── Typed codes ───────────────────────────────────────────────────────────────
//
// Where the person reads a code off a screen the machine can't see itself (the built-in screen
// after a nap, an external monitor), they type it in (owner, 2026-10-13: the 4-choice list with
// a retry let a blind guess pass about 44 % of the time). A code is 3 digits, 100–999, and it
// gets codeTries tries, a new code after a miss: a blind guess passes about 2 in 900.

// codeTries is how many tries a typed code gets.
const codeTries = 2

// codeLen is a typed code's digits.
const codeLen = 3

// typed is the code field as a run keeps it: the digits typed so far and the misses.
type typed struct {
	value  string
	misses []string // English, for the evidence: "typed 913 for 912"
	tried  []string // the wrong codes typed, for the screen
	wrong  string   // the last miss's line (translated), "" for none
}

// key takes a key typed into the field ("0"–"9", "backspace", "enter"). The code is checked as
// soon as its last digit is typed, no Enter (owner, 2026-10-13): then it empties the field and
// reports what was typed. Enter does nothing.
func (t *typed) key(k string) (string, bool) {
	switch {
	case k == "backspace":
		if n := len(t.value); n > 0 {
			t.value = t.value[:n-1]
		}
	case len(k) == 1 && k[0] >= '0' && k[0] <= '9':
		if len(t.value) < codeLen {
			t.value += k
		}
		if len(t.value) == codeLen {
			v := t.value
			t.value = ""
			return v, true
		}
	}
	return "", false
}

// miss notes a wrong try of v for code; it reports whether that was the last try.
func (t *typed) miss(v string, code int) bool {
	t.misses = append(t.misses, fmt.Sprintf("typed %s for %d", v, code))
	t.tried = append(t.tried, v)
	return len(t.misses) >= codeTries
}

// field is the Screen's code field, labelled label; wrong is the line after a miss
// (translated, "%s" the digits typed).
func (t *typed) field(label string) *CodeField {
	left := codeTries - len(t.misses)
	return &CodeField{Label: label, Value: t.value, Len: codeLen, Wrong: t.wrong,
		Tries: i18n.N("%d try", "%d tries", left, left)}
}

// evidence is the misses for the report, "" for none.
func (t *typed) evidence() string { return strings.Join(t.misses, ", then ") }

// bigDigits are 3×5 cell digits for a code drawn large: '#' is a full block.
var bigDigits = [10][5]string{
	{"###", "# #", "# #", "# #", "###"},
	{" # ", "## ", " # ", " # ", "###"},
	{"###", "  #", "###", "#  ", "###"},
	{"###", "  #", " ##", "  #", "###"},
	{"# #", "# #", "###", "  #", "  #"},
	{"###", "#  ", "###", "  #", "###"},
	{"###", "#  ", "###", "# #", "###"},
	{"###", "  #", " # ", " # ", " # "},
	{"###", "# #", "###", "# #", "###"},
	{"###", "# #", "###", "  #", "###"},
}

// bigCodeH is the rows bigCode takes.
const bigCodeH = 6

// bigCode draws a number large, five rows tall, each cell doubled in width (white), under a
// blank row so it stands clear of the lines above (bigCodeH rows in all).
func bigCode(n int) Panel { return bigCodeIn(n, g.WHT) }

// bigCodeIn is bigCode in col: a typed code turns green when it's right, red when it's wrong
// (owner, 0.6.2 review).
func bigCodeIn(n int, dc g.Color) Panel {
	s := fmt.Sprint(n)
	return func(gr *g.Grid, x, y, w, h int, _ float64) {
		y, h = y+1, h-1
		for i, ch := range s {
			d := int(ch - '0')
			if d < 0 || d > 9 {
				continue
			}
			dx := x + i*8
			if dx+6 > x+w {
				return
			}
			for row := 0; row < 5 && row < h; row++ {
				for col, c := range bigDigits[d][row] {
					if c == '#' {
						gr.Set(dx+2*col, y+row, '█', dc)
						gr.Set(dx+2*col+1, y+row, '█', dc)
					}
				}
			}
		}
	}
}
