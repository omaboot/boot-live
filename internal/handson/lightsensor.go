package handson

import (
	"fmt"
	"math"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/omaboot/boot-live/internal/boot"
	"github.com/omaboot/boot-live/internal/checks"
	g "github.com/omaboot/boot-live/internal/grid"
	"github.com/omaboot/boot-live/internal/i18n"
)

// input.ambient-light-sensor, "Ambient light sensor": the tester covers the sensor for three
// seconds and uncovers it, and the tool watches applesmc's light reading follow (the quick pass
// only reads it once, which a stuck sensor passes; criteria review, 2026-10-13). The level is the
// sum of applesmc's two readings ("(left,right)"; one-sensor Macs read the left), polled five
// times a second. The machine decides (method automatic) from the readings:
//
//   - the uncovered level is the average over the first 1.5 s; under 3 the room is too dark to
//     see a drop, and the tester shines a phone's flashlight on the sensor instead;
//   - followed: the level drops by at least half and at least 2 counts (with the flashlight: rises
//     by at least its own level and at least 3 counts), stays there 3 s, and once uncovered (the
//     light taken away) comes back at least halfway and at least 2 counts;
//   - supported: it followed;
//   - partial: it followed the cover but didn't come back within 12 s of "uncover it";
//   - failed: no applesmc light reading, or the reading stopped during the test; or the level
//     never moved and the tester says they covered it ("I covered it, nothing changed"): with the
//     SMC's raw light bytes moving, the sensor sees and the driver's reading doesn't follow it (a
//     driver parse issue, method automatic); with nothing moving at all, raw bytes included, the
//     sensor didn't respond to Linux (method observed; 0.7.3 follow-ups: it was partial; the
//     evidence keeps the raw bytes).
//
// The raw bytes (0.7.2 review, iMac10,1: a reading that never moved, covered or lit by a
// flashlight): besides applesmc's parsed light file, the test reads the SMC's light keys (ALV0,
// ALV1 and any other AL… key) raw through applesmc's key_at_index interface (smc.go), five times
// a second, and the evidence carries them before, covered (or lit) and after. applesmc parses a
// 10-byte ALV0 the MacBook way (bytes 6–7, right = 0), which may be wrong for an iMac; the raw
// bytes tell. A byte that leaves its uncovered range by more than 2 while covered counts as moved.
//
// Live boots decide it only with OmaBoot?'s applesmc fix or the install's kernel (live: kernel):
// the test is offered only where checks.LiveLimited allows it (Plan, Decidable).

func init() {
	Register(Spec{ID: "input.ambient-light-sensor", Group: NoPlug, Order: 55, Title: i18n.M("Ambient light sensor"),
		Brief: i18n.M("cover the camera for three seconds"), Expect: 30 * time.Second,
		New: func(*Env) Run { return &lightSensor{} }, Demo: func(f *Fake) {
			l := "/sys/bus/acpi/devices/APP0001:00/light"
			f.Set(l, "(38,0)")
			DemoSMC(f, 38)
			f.At(3.2, func(f *Fake) { f.Set(l, "(2,0)"); DemoSMC(f, 2) }) // covered
			f.At(7.6, func(f *Fake) { f.Set(l, "(37,0)"); DemoSMC(f, 37) })
		}})
}

// DemoSMC is the demo Mac's SMC as applesmc's key_at_index shows it, its 10-byte ALV0 holding
// the light level where applesmc reads it (bytes 6–7, shifted left 2), ALV1 empty.
func DemoSMC(f *Fake, level int) {
	alv := []byte{0x00, 0x1c, 0x00, 0x00, 0x01, 0x00, byte(level << 2 >> 8), byte(level << 2), 0x00, 0x00}
	f.SMC = []FakeKey{{"ACCL", "ui8 ", []byte{0x00}}, {"ALI0", "{ali", []byte{0x03, 0x01, 0x00}}, {"ALSL", "ui8 ", []byte{0x01}},
		{"ALV0", "{alv", alv}, {"ALV1", "{alv", make([]byte, 10)}, {"B0AC", "si16", []byte{0xff, 0x38}}}
	f.Set("/sys/bus/acpi/devices/APP0001:00/key_count", "6")
}

// DemoLightStuck is the iMac10,1 as the 0.7.2 review found it, for tools/screens: applesmc's
// light level stuck at 18 whatever the hand does, while the raw ALV0 bytes follow the cover
// (from 3.2 s) in bytes applesmc doesn't read.
func DemoLightStuck(f *Fake) {
	f.Set("/sys/bus/acpi/devices/APP0001:00/light", "(18,0)")
	DemoSMC(f, 18)
	f.At(3.2, func(f *Fake) { f.SMC[3].Data = []byte{0x00, 0x02, 0x00, 0x00, 0x01, 0x00, 0x00, 0x48, 0x00, 0x00} })
}

const (
	lsPoll    = 0.2  // seconds between readings
	lsBase    = 1.5  // seconds of uncovered readings first
	lsHold    = 3.0  // seconds the cover has to hold
	lsStuck   = 12.0 // seconds without a move before the tester is asked
	lsBackFor = 12.0 // seconds for the level to come back after "uncover it"
	lsDark    = 3    // an uncovered level under this is a dark room
)

type lsPhase int

const (
	lsMeasure lsPhase = iota // reading the uncovered level
	lsCover                  // waiting for the drop (or the flashlight's rise)
	lsHeld                   // moved: holding for lsHold
	lsUncover                // waiting for it to come back
)

type lightSensor struct {
	file   string // applesmc's light file
	phase  lsPhase
	at     float64 // when the phase began
	polled float64
	raw    string // the last reading as applesmc gives it
	now    int    // the last level
	bad    int    // readings in a row that failed
	sum    int    // the uncovered readings, for the average
	n      int
	base   int  // the uncovered level
	dark   bool // the flashlight instead of the cover
	ext    int  // the lowest (highest with the flashlight) level while held
	scale  int  // the highest level seen, for the bar
	tries  int  // covers that didn't hold, for the evidence
	held   float64
	keys   lsRaw // the SMC's light keys, raw
}

// lsRaw is the SMC's light keys read raw, sampled with each reading.
type lsRaw struct {
	scan Future[lsScan] // finding the keys (every name is read once)
	got  bool
	err  string
	keys []*rawKey
}

// lsScan is what finding the light keys gave.
type lsScan struct {
	dir  string
	keys []string
	idx  []int
	err  error
}

// rawKey is one light key's raw bytes over the test.
type rawKey struct {
	name, typ string
	idx       int
	base      [][]byte // uncovered, before the cover (at most 25)
	far       []byte   // covered (or lit): the sample furthest from the uncovered ones
	farD      int
	after     []byte // the newest once uncovered
	last      []byte
	moved     bool // a byte left its uncovered range by more than 2 while covered
}

// away is how far b is from the uncovered samples, byte by byte (the sum), and whether any
// byte left their range by more than 2.
func (k *rawKey) away(b []byte) (int, bool) {
	if len(k.base) == 0 {
		return 0, false
	}
	d, moved := 0, false
	for i, c := range b {
		lo, hi, seen := 255, 0, false
		for _, s := range k.base {
			if i < len(s) {
				lo, hi, seen = min(lo, int(s[i])), max(hi, int(s[i])), true
			}
		}
		if !seen {
			continue
		}
		off := max(0, lo-int(c), int(c)-hi)
		d += off
		moved = moved || off > 2
	}
	return d, moved
}

// sample takes one reading of each raw key in the test's phase.
func (r *lsRaw) sample(c *Ctx, phase lsPhase) {
	if !r.got {
		sc, ok := r.scan.Ready()
		if !ok {
			return
		}
		r.got = true
		if sc.err != nil {
			r.err = sc.err.Error()
		}
		for i, n := range sc.keys {
			r.keys = append(r.keys, &rawKey{name: n, idx: sc.idx[i]})
		}
		if len(r.keys) == 0 {
			return
		}
		r.scan = Now(sc) // the directory, for the reads
	}
	sc, _ := r.scan.Ready()
	for _, k := range r.keys {
		v, err := smcRead(c.IO, sc.dir, k.idx)
		if err != nil || v.Name != k.name {
			continue
		}
		k.typ, k.last = v.Type, v.Data
		switch {
		case phase == lsMeasure || len(k.base) == 0:
			if len(k.base) < 25 {
				k.base = append(k.base, v.Data)
			}
		case phase == lsCover || phase == lsHeld:
			if d, m := k.away(v.Data); d > k.farD || k.far == nil {
				k.far, k.farD = v.Data, d
				k.moved = k.moved || m
			}
		case phase == lsUncover:
			k.after = v.Data
		}
	}
}

// reset starts the uncovered samples again (Try again).
func (r *lsRaw) reset() {
	for _, k := range r.keys {
		k.base, k.far, k.farD, k.after, k.moved = nil, nil, 0, nil, false
	}
}

// moved reports the keys whose bytes moved while covered.
func (r *lsRaw) moved() []string {
	var out []string
	for _, k := range r.keys {
		if k.moved {
			out = append(out, k.name)
		}
	}
	return out
}

func hexOf(b []byte) string { return SMCKey{Data: b}.Hex() }

// evidence is the raw keys' part of the report's evidence, in English; covered names the
// middle sample ("covered" or "lit").
func (r *lsRaw) evidence(covered string) string {
	switch {
	case !r.got:
		return "; raw SMC light keys: still being looked up"
	case r.err != "":
		return "; raw SMC light keys: none read (" + r.err + ")"
	case len(r.keys) == 0:
		return "; raw SMC light keys: the SMC has no AL… key"
	}
	var parts []string
	for _, k := range r.keys {
		s := fmt.Sprintf("%s %s %d bytes:", k.name, strings.TrimSpace(k.typ), len(k.last))
		if len(k.base) > 0 {
			s += " before " + hexOf(k.base[0])
		}
		if k.far != nil {
			s += "; " + covered + " " + hexOf(k.far)
		}
		if k.after != nil {
			s += "; after " + hexOf(k.after)
		}
		if k.moved {
			s += " (moved)"
		}
		parts = append(parts, s)
	}
	return "; raw SMC light keys (applesmc key_at_index): " + strings.Join(parts, "; ")
}

// lightFile is applesmc's light file: an ACPI device on recent kernels, a platform device on
// older ones (as checks.smcDir).
func lightFile(io IO) string {
	for _, pat := range []string{"/sys/bus/acpi/devices/APP0001:*/light", "/sys/devices/platform/applesmc.*/light"} {
		for _, p := range io.Glob(pat) {
			if io.Read(p) != "" {
				return p
			}
		}
	}
	return ""
}

// lightLevel reads "(left,right)" as their sum.
func lightLevel(s string) (int, bool) {
	s = strings.Trim(strings.TrimSpace(s), "()")
	if s == "" {
		return 0, false
	}
	sum := 0
	for _, f := range strings.Split(s, ",") {
		n, err := strconv.Atoi(strings.TrimSpace(f))
		if err != nil {
			return 0, false
		}
		sum += n
	}
	return sum, true
}

func (l *lightSensor) Start(c *Ctx) {
	c.Select(-1) // waiting for a hand: a stray Enter mustn't answer
	l.file = lightFile(c.IO)
	if l.file == "" {
		c.Decide(Outcome{Results: []checks.Result{l.result(c, checks.Failed, "applesmc has no light reading", i18n.T("no light reading"))},
			Say: i18n.T("no light reading from the SMC. nothing to cover.")})
		return
	}
	l.read(c)
	// the raw keys: every SMC key name is read once to find them, off the screen's goroutine on
	// the Mac (a few hundred SMC reads); a fixture or the demo answers at once
	scan := func() lsScan {
		dir, keys, idx, err := lightKeys(c.IO)
		return lsScan{dir, keys, idx, err}
	}
	if c.IO.Real() {
		l.keys.scan = Go(scan)
	} else {
		l.keys.scan = Now(scan())
	}
}

// read takes a reading; false after two seconds of failed ones (it has decided then).
func (l *lightSensor) read(c *Ctx) bool {
	l.polled = c.T
	l.raw = c.IO.Read(l.file)
	v, ok := lightLevel(l.raw)
	if !ok {
		if l.bad++; float64(l.bad)*lsPoll >= 2 {
			c.Decide(Outcome{Results: []checks.Result{l.result(c, checks.Failed, "the light reading stopped during the test (last read: \""+l.raw+"\")", i18n.T("the reading stopped"))},
				Say: i18n.T("the light reading went away mid-test. that's a fail.")})
		}
		return false
	}
	l.bad, l.now, l.scale = 0, v, max(l.scale, v)
	return true
}

// moved: the level followed the cover (or the flashlight).
func (l *lightSensor) moved(v int) bool {
	if l.dark {
		return v-l.base >= max(3, l.base)
	}
	return l.base-v >= max(2, l.base/2)
}

// recovered: the level came back from where the cover took it.
func (l *lightSensor) recovered(v int) bool {
	if l.dark {
		return l.ext-v >= max(2, (l.ext-l.base)/2)
	}
	return v-l.ext >= max(2, (l.base-l.ext)/2)
}

func (l *lightSensor) Step(c *Ctx) {
	if l.file == "" || c.T-l.polled < lsPoll || !l.read(c) {
		return
	}
	l.keys.sample(c, l.phase)
	v := l.now
	switch l.phase {
	case lsMeasure:
		l.sum, l.n = l.sum+v, l.n+1
		if c.T-l.at >= lsBase {
			l.base = (l.sum + l.n/2) / l.n
			l.dark = l.base < lsDark
			l.phase, l.at = lsCover, c.T
		}
	case lsCover:
		if l.moved(v) {
			l.phase, l.at, l.ext = lsHeld, c.T, v
		}
	case lsHeld:
		if !l.moved(v) { // let go too soon
			l.tries++
			l.phase, l.at = lsCover, c.T
			return
		}
		if l.dark {
			l.ext = max(l.ext, v)
		} else {
			l.ext = min(l.ext, v)
		}
		if c.T-l.at >= lsHold {
			l.held = c.T - l.at
			l.phase, l.at = lsUncover, c.T
		}
	case lsUncover:
		switch {
		case l.recovered(v):
			l.decide(c, checks.Supported, Automatic, fmt.Sprintf("; back to %d", v), i18n.T("followed the cover and came back"),
				i18n.T("covered, dark; uncovered, light. the sensor sees."))
		case c.T-l.at >= lsBackFor:
			l.decide(c, checks.Partial, Automatic, fmt.Sprintf("; it didn't come back within %.0f s (still %d)", lsBackFor, v), i18n.T("followed the cover, didn't come back"),
				i18n.T("it saw the cover, but never came back. noted as partial."))
		}
	}
}

// evidence is the readings, in English.
func (l *lightSensor) evidence(more string) string {
	dev := path.Base(path.Dir(l.file))
	ev := fmt.Sprintf("applesmc light (%s): uncovered level %d (last read %s)", dev, l.base, l.raw)
	switch {
	case l.dark && l.phase >= lsHeld:
		ev += fmt.Sprintf("; dark room: a flashlight on the sensor took it to %d, held %.1f s", l.ext, l.held)
	case l.dark:
		ev += "; dark room: the tester was asked for a flashlight on the sensor"
	case l.phase >= lsHeld:
		ev += fmt.Sprintf("; covered, it dropped to %d, held %.1f s", l.ext, l.held)
	}
	if l.tries > 0 {
		ev += fmt.Sprintf("; %d cover(s) let go too soon first", l.tries)
	}
	covered := "covered"
	if l.dark {
		covered = "lit"
	}
	return ev + more + l.keys.evidence(covered)
}

func (l *lightSensor) result(c *Ctx, status, ev, detail string) checks.Result {
	r := Result("input.ambient-light-sensor", status, Automatic, ev, detail)
	if e := c.Env.Checks; e != nil && e.Ident.Chip != "t2" {
		if f := e.Fixed(checks.FixApplesmc); f != nil {
			r.FixID, r.Fix = f.ID, f.Short
		}
	}
	return r
}

func (l *lightSensor) decide(c *Ctx, status, method, more, detail, say string) {
	r := l.result(c, status, l.evidence(more), detail)
	r.Method = method
	c.Decide(Outcome{Results: []checks.Result{r}, Say: say})
}

// stuck: no move for a while; the tester is asked.
func (l *lightSensor) stuck(c *Ctx) bool { return l.phase == lsCover && c.T-l.at >= lsStuck }

func (l *lightSensor) Act(c *Ctx, i int) {
	if !l.stuck(c) {
		return
	}
	switch i {
	case 0: // try again: a fresh uncovered level
		l.phase, l.at, l.sum, l.n = lsMeasure, c.T, 0, 0
		l.tries++
		l.keys.reset()
	case 1:
		what := "covered it"
		if l.dark {
			what = "shone a flashlight on it"
		}
		if ks := l.keys.moved(); len(ks) > 0 {
			// the sensor saw the hand, the driver's number didn't: logged in the evidence, which
			// the full log and the report carry
			l.decide(c, checks.Failed, Automatic, fmt.Sprintf("; the tester %s and the level never moved (%.0f s), but the raw bytes of %s did: "+
				"applesmc's light reading doesn't follow the sensor (a driver parse issue)", what, c.T-l.at, strings.Join(ks, ", ")),
				i18n.T("the driver's reading doesn't follow the sensor"), i18n.T("the sensor's raw bytes moved, the driver's number didn't. a driver problem: that's a fail."))
			return
		}
		// nothing moved at all (0.7.3 follow-ups): the sensor didn't respond to Linux, a fail;
		// the raw keys' bytes stay in the evidence
		raw := "neither applesmc's level nor the SMC's raw light bytes moved"
		if !l.keys.got || l.keys.err != "" || len(l.keys.keys) == 0 {
			raw = "applesmc's level never moved, and no raw SMC light key could be read to compare"
		}
		l.decide(c, checks.Failed, Observed, fmt.Sprintf("; the tester %s and the level never moved (%.0f s): the sensor didn't respond to Linux (%s)", what, c.T-l.at, raw),
			i18n.T("the sensor didn't respond to Linux"), i18n.T("nothing moved, not even the raw bytes. the sensor doesn't answer Linux: that's a fail."))
	}
}

func (l *lightSensor) Stop(*Ctx) {}

func (l *lightSensor) View(c *Ctx) Screen {
	s := Screen{Face: boot.Idle, SayAt: l.at, OutTitle: i18n.T("What I see"),
		Steps: []string{i18n.T("Find the camera, at the top of the screen."), i18n.T("Cover it with your hand and hold for 3 seconds."),
			i18n.T("Take your hand away.")},
		Happens: i18n.T("The light sensor sits next to the camera. Covered, its reading should drop to about 0, then come back."),
		Hint:    i18n.T("it's a small window by the camera. a flat hand over the top edge does it.")}
	if l.file == "" {
		s.Steps, s.Happens = []string{i18n.T("No light reading to watch.")}, ""
		s.Live = []Line{Bad(i18n.T("applesmc has no light reading"))}
		return s
	}
	if l.dark {
		s.Steps[1], s.Steps[2] = i18n.T("Shine a phone's flashlight on it and hold for 3 seconds."), i18n.T("Take the light away.")
		s.Happens = i18n.T("The room is too dark to see a shadow, so a light on the sensor has to raise its reading instead, then let it drop back.")
	}
	decided := c.Outcome() != nil
	switch l.phase {
	case lsMeasure:
		s.Step = 0
		s.Say, s.SayAt = i18n.T("let me see the room first."), 0.2
		s.Waiting = "·  " + i18n.T("reading the room")
	case lsCover:
		s.Step = 1
		s.Say = i18n.T("I'm reading the light. %d out here.", l.base)
		if l.dark {
			s.Say = i18n.T("too dark in here for a shadow. light me up instead.")
		}
		if l.tries > 0 {
			s.Say = i18n.T("a bit longer, please: hold it for three seconds.")
		}
		if l.stuck(c) {
			s.Say, s.Face = i18n.T("the reading isn't moving. did you cover it?"), boot.Hmm
		}
		s.Waiting = "·  " + i18n.T("waiting for your hand")
	case lsHeld:
		s.Step, s.Face, s.Hint = 1, boot.Work, ""
		s.Say = i18n.T("dark. hold it there.")
		if l.dark {
			s.Say = i18n.T("bright. hold it there.")
		}
		s.Waiting = "·  " + i18n.T("measuring")
	case lsUncover:
		s.Step, s.Face, s.Hint = 2, boot.Work, ""
		s.Say = i18n.T("3 seconds. let go.")
		s.Waiting = "·  " + i18n.T("waiting for the light to come back")
	}
	if decided {
		s.Step = len(s.Steps)
	}
	s.Live = []Line{Fact(i18n.T("light sensor: %s", path.Base(path.Dir(l.file))))}
	if l.phase == lsMeasure {
		s.Live = append(s.Live, Wait(i18n.T("reading the room: %d", l.now)))
	} else {
		s.Live = append(s.Live, Fact(i18n.T("uncovered: %d", l.base)))
	}
	switch {
	case l.phase == lsHeld:
		s.Live = append(s.Live, Fact(i18n.T("covered: %d", l.ext)), Wait(i18n.T("held %d s of 3", int(c.T-l.at))))
	case l.phase == lsUncover:
		s.Live = append(s.Live, Fact(i18n.T("covered: %d", l.ext)), Fact(i18n.T("held 3 s: long enough")))
		if decided {
			s.Live = append(s.Live, Fact(i18n.T("uncovered: back to %d", l.now)))
		} else {
			s.Live = append(s.Live, Wait(i18n.T("now %d", l.now)))
		}
	case l.phase == lsCover:
		s.Live = append(s.Live, Wait(i18n.T("now %d", l.now)))
	}
	if l.dark && l.phase != lsMeasure {
		s.Live = append(s.Live, Gone(i18n.T("a dark room: a light on the sensor has to raise it")))
	}
	for i, k := range l.keys.keys { // the SMC's own bytes, for anyone looking closer (smc in the console too)
		if i == 2 || k.last == nil {
			break
		}
		s.Live = append(s.Live, Gone(i18n.T("%[1]s raw: %[2]s", k.name, hexOf(k.last))))
	}
	if len(l.keys.moved()) > 0 && l.phase == lsCover {
		s.Live = append(s.Live, Bad(i18n.T("the raw bytes moved, the light level didn't")))
	}
	// the animation: where the sensor is, a hand (or a phone's light) over it, the reading, and
	// how long is left to hold in big digits
	a := lsArt{lvl: l.now, scale: max(1, l.scale), dark: l.dark, hand: -1, left: 3}
	switch l.phase {
	case lsCover: // the hand shows the way in, over and over, until the reading moves
		k := math.Mod(c.T-l.at, 3.2)
		switch {
		case k < 0.8:
			a.hand = k / 0.8 * 0.85
		case k < 1.8:
			a.hand = 0.85
		case k < 2.3:
			a.hand = 0.85 * (1 - (k-1.8)/0.5)
		}
		a.label = i18n.T("cover it to start")
		if l.dark {
			a.label = i18n.T("light it to start")
		}
	case lsHeld:
		a.hand, a.live = 1, true
		a.left = max(1, int(math.Ceil(lsHold-(c.T-l.at)-1e-6)))
		a.label = i18n.T("keep it covered")
		if l.dark {
			a.label = i18n.T("keep it lit")
		}
	case lsUncover:
		a.hand, a.left, a.live, a.label = max(0, 1-(c.T-l.at)/0.5), 0, true, i18n.T("let go")
		if a.hand == 0 {
			a.hand = -1
		}
		if decided {
			a.hand, a.live, a.label, a.done = -1, false, i18n.T("held long enough"), true
		}
	default:
		a.label = i18n.T("reading the room")
	}
	s.Anim = a.draw
	if l.phase == lsCover {
		off := !l.stuck(c)
		none := i18n.T("I covered it, nothing changed")
		if l.dark {
			none = i18n.T("I lit it up, nothing changed")
		}
		s.Actions = []Action{{Label: i18n.T("Try again"), Does: i18n.T("read the room again, then try once more"), Off: off},
			{Label: none, Does: i18n.T("the level stayed the same"), Off: off}}
	}
	return s
}

// lsArt is the light sensor's animation (prototype lightArt): the top of the screen with the
// camera (the sensor sits by it), a hand sliding over it, the reading as a bar, and a big
// countdown of the seconds left to hold (3, 2, 1, then 0: let go).
//
// Laid out right-aligned in the band's 2/3 (owner, 0.6.0 review): the reading, the countdown and
// their labels in a column against the right edge, sized to the longest label this mode can
// show (so nothing moves when one changes); the screen's top to their left, with a lane between
// the two as wide as the hand, where it waits before sliding in. The hand only ever moves from
// that lane over the camera, so it never covers the reading, the countdown or a label.
type lsArt struct {
	hand       float64 // -1: none; 0–1 on its way in (1: over the camera)
	lvl, scale int
	left       int    // seconds left to hold
	live       bool   // the countdown is running (or at 0, let go)
	done       bool   // held long enough: decided
	dark       bool   // a phone's light instead of a hand
	label      string // what the countdown says
}

// lsHandW is the hand's width in cells (handPixels; the phone is narrower, drawn 2 in).
const lsHandW = 10

// lsReadW is the readout column's width: the bar's row ("light ██████████ 38") and the
// countdown's (big digit, then the longest label this mode can show).
func (a lsArt) lsReadW() int {
	lbls := []string{i18n.T("cover it to start"), i18n.T("keep it covered"), i18n.T("let go"),
		i18n.T("held long enough"), i18n.T("reading the room"), i18n.T("seconds to hold")}
	if a.dark {
		lbls[0], lbls[1] = i18n.T("light it to start"), i18n.T("keep it lit")
	}
	w := 0
	for _, l := range lbls {
		w = max(w, len([]rune(l)))
	}
	digits := max(2, len(fmt.Sprint(max(a.scale, a.lvl))))
	return max(len([]rune(i18n.T("light")))+1+10+2+digits, 9+w)
}

func (a lsArt) draw(gr *g.Grid, x, y, w, h int, _ float64) {
	rw := a.lsReadW()
	// right-aligned: [the screen's top][the hand's lane][the readout]
	bw := min(32, w-1-(lsHandW+2)-rw-1)
	if bw < 16 || h < 8 {
		return
	}
	rx := x + w - 1 - rw          // the readout column
	bx := rx - (lsHandW + 2) - bw // the screen's top
	cx := bx + bw/2
	gr.Put(bx, y+1, "┌"+strings.Repeat("─", bw-2)+"┐", g.GRY)
	for j := 2; j < h; j++ {
		gr.Put(bx, y+j, "│", g.GRY)
		gr.Put(bx+bw-1, y+j, "│", g.GRY)
	}
	cc := g.BCYN
	if a.hand >= 1 {
		cc = g.DIM
	}
	gr.Put(cx, y+2, "•", cc)
	gr.Put(cx, y+h-3, "↑", g.DIM)
	lbl := i18n.T("camera, sensor")
	gr.Put(cx-len([]rune(lbl))/2, y+h-2, lbl, g.DIM)
	if a.hand >= 0 { // from the lane (just right of the screen's top) over the camera
		start := bx + bw + 1
		hx := int(math.Round(float64(start) - a.hand*float64(start-(cx-4))))
		if a.dark {
			boot.Pixels(gr, hx+2, y+1, phonePixels)
		} else {
			boot.Pixels(gr, hx, y+1, handPixels)
		}
	}
	lw := len([]rune(i18n.T("light")))
	gr.Put(rx, y+2, i18n.T("light"), g.GRY)
	n := min(10, (a.lvl*10+a.scale/2)/a.scale)
	for i := 0; i < 10; i++ {
		if i < n {
			gr.Set(rx+lw+1+i, y+2, '█', g.BCYN)
		} else {
			gr.Set(rx+lw+1+i, y+2, '░', g.DIM)
		}
	}
	gr.Put(rx+lw+13, y+2, fmt.Sprint(a.lvl), g.WHT)
	dc, lc := g.DIM, g.DIM
	switch {
	case a.live && a.left == 0:
		dc, lc = g.WHT, g.WHT
	case a.live:
		dc, lc = g.BVIO, g.WHT
	}
	boot.BigDigits(gr, rx, y+4, fmt.Sprint(a.left), dc)
	gr.Put(rx+9, y+5, a.label, lc)
	if !a.done {
		gr.Put(rx+9, y+7, i18n.T("seconds to hold"), g.DIM)
	}
}
