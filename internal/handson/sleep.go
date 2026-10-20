package handson

import (
	"errors"
	"fmt"
	"math"
	"math/rand/v2"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/doesitomarchy/doesitomarchy/pkg/report"
	"github.com/omaboot/boot-live/internal/boot"
	"github.com/omaboot/boot-live/internal/checks"
	g "github.com/omaboot/boot-live/internal/grid"
	"github.com/omaboot/boot-live/internal/i18n"
	"github.com/omaboot/boot-live/internal/identify"
)

// power.sleep-wake, "Sleep and wake": the last test. The tool puts the Mac to sleep ("mem", in
// the mode /sys/power/mem_sleep picks) with the RTC alarm set to wake it 20 s later, then
// checks what came back. The person only starts it and watches. It never runs on a fixture tree.
//
//   - supported: the Mac slept until the alarm (at least 75 % of the 20 s), woke by itself,
//     and within 30 s had back every network interface, USB device, input device and connected
//     display it had before, and its network connection if it had one;
//   - partial: it slept and woke, but woke early (something else woke it), woke only long after
//     the alarm (a key or the lid did it), or something didn't come back (listed);
//   - woken early by the lid's own wake with the lid open (lidwake.go), it naps once more with the
//     lid's wake off, as OmaBoot?'s lid-wake fix does: on time then, it's supported with the fix
//     when the catalog registers it (else partial, the evidence saying so);
//   - failed: the kernel refused to sleep (the write failed, or a device failed to suspend), or
//     it didn't sleep at all (under a second asleep);
//   - not_tested (other): no RTC wake alarm to wake it by itself, or a fixture run.
//
// The connectors read "connected" after a wake even when the screen stays black, so once the
// back phase is done (after the last nap) the screen shows a 3-digit code, large, and the tester
// types it (a challenge, proof.go's typed codes; no false passes):
//
//   - typed right: the naps' verdict stands (a supported one then rests on the challenge);
//   - no answer within 60 s: the code stays up, and the screen adds "No code" (N): picked, a
//     supported verdict becomes partial, "no answer after waking: the screen may not have come
//     back" (a dark screen can't be answered); the code typed later still confirms it, "late" in
//     the evidence. Before 0.8.9 the test decided at 60 s, and a code typed after landed on the
//     held result's keys: its first digit, 2, redid the test (the iMac18,3's 0.8.8 run: a second
//     nap, and the Mac restarted);
//   - a wrong code twice (a new code after the first): partial;
//   - skipped at the code: a supported verdict becomes not_tested (uncertain), the screen not
//     confirmed; a partial one stays partial.
//
// The backlight's bl_power and brightness after waking go in the evidence either way.
//
// It doesn't start while the optical drive holds a disc or works one (discguard.go): it offers
// "Eject disc" and waits. Nor while a FireWire disk is attached (fwguard.go: it hung the
// iMac10,1's sleep): Start stays greyed until it's unplugged.
//
// Every nap runs with the kernel's suspend diagnostics on (sleepdiag.go); the kernel's step
// times go in the evidence.
//
// Picked up after deep sleep hung (0.7.5; the run's copy says the Mac didn't come back from this
// test, Env.Hung), and the Mac offers light sleep (s2idle, "freeze") besides deep: the test opens
// again offering "Try light sleep (s2idle)" first, or deep sleep again. The hang stays on record:
//
//   - light sleep, woke and everything right: partial, "light sleep works; deep sleep hung"
//     (an installed system sleeps deep by default);
//   - deep sleep again, woke and everything right: partial, "woke this time; hung on the last
//     run";
//   - anything less: failed, with both naps in the evidence;
//   - skipped: the failed result from the hang stands.
//
// On the Macs whose NVMe drive needs it, the test first sets the drive's d3cold_allowed to 0, as
// Omarchy's installer does (install/hardware/apple/fix-suspend-nvme.sh), so the result stands for
// an installed system; the evidence says so.

func init() {
	Register(Spec{ID: "power.sleep-wake", Group: Last, Order: 20, Sleeps: true, Title: i18n.M("Sleep and wake"),
		Brief: i18n.M("a 20-second nap: hands off, watch it come back"), Expect: 60 * time.Second,
		New: func(*Env) Run { return &sleepWake{} }, Demo: func(f *Fake) {
			f.SleepFor = 2.5
			f.Set("/sys/class/backlight/intel_backlight/bl_power", "0")
		}})
}

// sleepFor is how long the Mac sleeps; it must wake by itself within it.
const sleepFor = 20 * time.Second

// sleepBack is how long after waking the devices and the network get to come back.
const sleepBack = 30.0

type sleepPhase int

const (
	sleepReady sleepPhase = iota
	sleepCount            // the countdown before it goes
	sleepAsleep
	sleepBackPhase // back: waiting for the devices and the network
	sleepCodePhase // back: the tester types the code on the screen
)

// codeWait is how long the code waits for an answer after waking.
const codeWait = 60.0

type sleepWake struct {
	phase    sleepPhase
	at       float64
	pre      before    // the network before the nap
	back     comeback  // what came back after it
	disc     discGuard // the optical drive: no nap with a disc in it or a read under way
	fw       fwGuard   // no nap with a FireWire disk attached
	sleep    Future[Sleep]
	slept    Sleep
	nvme     string // the NVMe drive set up as an install does, or ""
	alarmErr string
	// the lid-wake retry: the lid was open when the first nap began; again is the second nap,
	// with first's outcome kept; lidOff names the lid while its wake is turned off
	lidOpen  bool
	again    bool
	first    Sleep
	firstBad []string
	lidOff   string
	// picked up after deep sleep hung (Env.Hung): hung is the failed outcome on record, light
	// the nap's mode (light sleep, s2idle) once Start is picked
	hung  *Outcome
	light bool
	// the code after waking: the naps' outcome waits for it
	pending *Outcome
	rng     *rand.Rand
	code    int
	typed   typed
	askAt   float64 // when the first code showed
	late    bool    // codeWait passed with no answer: No code is offered, the code still taken
	lateBy  float64 // a late code: how long after it showed it was typed
	codeAt  float64 // when the current one did (or will: after a miss, once the red one goes)
	// how the code in the band looks: green once typed right, red once
	// wrong twice; after a first miss the missed code shows red for codeRed, then the new one
	right, failed bool
	missed        int
	missAt        float64
}

// codeRed is how long a missed code stays up in red before the new one shows.
const codeRed = 1.2

func (s *sleepWake) Start(c *Ctx) {
	if !c.IO.Real() && c.Env.Checks != nil { // a fixture tree: never
		c.Decide(Outcome{Results: []checks.Result{NotTested("power.sleep-wake", checks.Other, "fixture run: the Mac wasn't put to sleep", i18n.T("not run on a fixture"))},
			Say: i18n.T("a fixture can't sleep. skipping it.")})
		return
	}
	if len(c.IO.Glob("/sys/class/rtc/rtc0/wakealarm")) == 0 {
		c.Decide(Outcome{Results: []checks.Result{NotTested("power.sleep-wake", checks.Other, "no RTC wake alarm (/sys/class/rtc/rtc0/wakealarm): the Mac couldn't wake by itself", i18n.T("no RTC wake alarm"))},
			Say: i18n.T("no alarm clock to wake me. I won't nap without one.")})
		return
	}
	if o, ok := c.Env.Hung["power.sleep-wake"]; ok && len(o.Results) > 0 {
		s.hung = &o
	}
	s.pre.start(c)
	s.disc.start(c)
	s.fw.step(c)
}

// lightOffered reports whether this Mac sleeps deep by default and has light sleep too.
func lightOffered(io IO) bool {
	return memSleep(io) == "deep" && ReadPMDiag(io).Offers("freeze")
}

// Answers in the ready state, by what they do.
const (
	actStart = iota
	actLight
	actDeep
	actEject
)

// acts are the ready state's answers, in order: Start (or, picked up after a hang, light sleep
// then deep sleep again), then Eject disc while the drive holds one.
func (s *sleepWake) acts(c *Ctx) []int {
	var out []int
	switch {
	case s.hung != nil && lightOffered(c.IO):
		out = []int{actLight, actDeep}
	case s.hung != nil:
		out = []int{actDeep}
	default:
		out = []int{actStart}
	}
	if s.disc.action() != nil {
		out = append(out, actEject)
	}
	return out
}

// devicesNow lists what should come back after a resume, by kind.
func devicesNow(io IO) map[string][]string {
	m := map[string][]string{}
	for _, d := range io.Glob("/sys/class/net/*") {
		if n := path.Base(d); n != "lo" {
			m["network"] = append(m["network"], n)
		}
	}
	for _, d := range io.Glob("/sys/bus/usb/devices/*") {
		if n := path.Base(d); !strings.Contains(n, ":") {
			m["USB"] = append(m["USB"], n)
		}
	}
	for _, d := range io.Inputs() {
		m["input"] = append(m["input"], d.Name)
	}
	for _, d := range io.Glob("/sys/class/drm/card*-*") {
		if io.Read(d+"/status") == "connected" {
			m["display"] = append(m["display"], strings.SplitN(path.Base(d), "-", 2)[1])
		}
	}
	for k := range m {
		sort.Strings(m[k])
	}
	return m
}

// gone lists what before had and now hasn't, "USB 2-1.2".
func gone(before, now map[string][]string) []string {
	var out []string
	for _, k := range []string{"network", "USB", "input", "display"} {
		have := map[string]int{}
		for _, x := range now[k] {
			have[x]++
		}
		for _, x := range before[k] {
			if have[x] > 0 {
				have[x]--
				continue
			}
			out = append(out, k+" "+x)
		}
	}
	return out
}

// nvmeModel: the Macs Omarchy's installer gives the NVMe suspend setting.
func nvmeModel(id string) bool {
	fam, major := identify.Model(id)
	return fam == "MacBook" && major >= 8 && major <= 10 || fam == "MacBookPro" && major >= 13 && major <= 14
}

func (s *sleepWake) begin(c *Ctx) {
	if e := c.Env.Checks; e != nil && nvmeModel(e.Ident.Identifier) {
		const dev = "/sys/bus/pci/devices/0000:01:00.0/"
		if strings.HasPrefix(c.IO.Read(dev+"class"), "0x0108") && c.IO.Write(dev+"d3cold_allowed", "0") == nil {
			s.nvme = "0000:01:00.0"
		}
	}
	if !s.again {
		s.lidOpen = lidIsOpen(c.IO)
	}
	s.back.arm(c, s.pre.net.Up)
	s.phase, s.at = sleepAsleep, c.T
	s.sleep = c.IO.Suspend(sleepFor, SleepHow{Test: "power.sleep-wake", Title: "Sleep and wake", Light: s.light})
}

func (s *sleepWake) Step(c *Ctx) {
	s.pre.step(c)
	if s.phase == sleepReady || s.phase == sleepCount {
		s.disc.step(c, s.phase == sleepCount)
		s.fw.step(c)
	}
	switch s.phase {
	case sleepCount:
		switch {
		case s.disc.holds() || s.fw.holds(): // a disc went in, or a FireWire disk was plugged in, during the countdown
			s.phase, s.at = sleepReady, c.T
		case c.T-s.at >= s.countdown() && s.disc.asked():
			s.begin(c)
		}
	case sleepAsleep:
		sl, ok := s.sleep.Ready()
		if !ok {
			return
		}
		s.slept, s.phase, s.at = sl, sleepBackPhase, c.T
		c.Redraw()
		if sl.Err != nil || sl.Slept < time.Second {
			s.decide(c)
			return
		}
		s.back.woke(c)
	case sleepBackPhase:
		if s.back.step(c) {
			s.decide(c)
		}
	case sleepCodePhase:
		if !s.late && c.T-s.askAt >= codeWait {
			s.late, s.at = true, c.T
			c.Redraw()
		}
	}
}

// ask holds the naps' outcome back and shows the code: a Mac that didn't sleep decides at once
// (its screen never went dark).
func (s *sleepWake) ask(c *Ctx, o Outcome) {
	ev := backlightAfter(c.IO)
	for i := range o.Results {
		if ev != "" {
			o.Results[i].Evidence += "; " + ev
		}
	}
	if len(o.Results) == 0 || o.Results[0].Status == checks.Failed {
		c.Decide(o)
		return
	}
	if len(s.back.problems()) > 0 { // the 2017 iMacs lose their Thunderbolt 3 chip, and a nap after it restarts the Mac
		o.Warn = i18n.T("that nap lost devices it never got back. another nap may hang or restart the Mac.")
	}
	s.pending = &o
	s.rng = proofRNG(c.IO)
	s.code = proofCode(s.rng)
	s.phase, s.askAt, s.codeAt, s.at = sleepCodePhase, c.T, c.T, c.T
	c.Redraw()
}

// backlightAfter is the backlight's state after waking, for the evidence: each device's bl_power
// (0 is on) and brightness.
func backlightAfter(io IO) string {
	var parts []string
	for _, d := range io.Glob("/sys/class/backlight/*") {
		p := path.Base(d)
		if v := io.Read(d + "/bl_power"); v != "" {
			p += " bl_power " + v
			if v == "0" {
				p += " (on)"
			}
		}
		p += fmt.Sprintf(", brightness %s of %s", orUnknown(io.Read(d+"/brightness")), orUnknown(io.Read(d+"/max_brightness")))
		parts = append(parts, p)
	}
	if len(parts) == 0 {
		return ""
	}
	return "after waking, backlight: " + strings.Join(parts, "; ")
}

// confirmed: the tester typed the code; the naps' verdict stands.
func (s *sleepWake) confirmed(c *Ctx) {
	o := *s.pending
	o.Results = append([]checks.Result(nil), o.Results...)
	r := &o.Results[0]
	r.Evidence += fmt.Sprintf("; the tester typed the code shown after waking (%d)", s.code)
	if m := s.typed.evidence(); m != "" {
		r.Evidence += " after " + m
	}
	if s.late {
		s.lateBy = c.T - s.askAt
		r.Evidence += fmt.Sprintf(", late: %.0f s after it showed (it waits %.0f s before offering No code)", s.lateBy, codeWait)
		o.Say = i18n.T("late, but that's my code.") + " " + o.Say // on screen too: the 0.8.9 run's late code showed only in the report
	}
	if r.Status == checks.Supported {
		r.Method = Challenge
	}
	c.Decide(o)
}

// unconfirmed: the screen after waking isn't confirmed; a supported verdict becomes status.
func (s *sleepWake) unconfirmed(c *Ctx, status, method, why, detail, say string) {
	o := *s.pending
	o.Results = append([]checks.Result(nil), o.Results...)
	r := &o.Results[0]
	r.Evidence += "; " + why
	if r.Status == checks.Supported {
		r.Status, r.Method, r.Detail, o.Say = status, method, detail, say
		if status == checks.NotTested {
			r.Reason = checks.Uncertain
		}
	}
	c.Decide(o)
}

// Skip at the code: the screen isn't confirmed (see the top of the file).
func (s *sleepWake) Skip(c *Ctx) bool {
	if s.phase == sleepReady && s.hung != nil { // picked up after a hang: its failed result stands
		o := *s.hung
		o.Say = i18n.T("skipped: deep sleep hung last time, and that fail stands.")
		c.Decide(o)
		return true
	}
	if s.phase != sleepCodePhase {
		return false
	}
	s.unconfirmed(c, checks.NotTested, "", "the tester skipped the code shown after waking: the screen isn't confirmed",
		i18n.T("the screen after waking wasn't confirmed"), i18n.T("skipped the code: the screen after waking isn't confirmed."))
	return true
}

// SleepVerdict decides power.sleep-wake from the suspend and what didn't come back (bad: the
// comeback watch's problems, already worded).
func SleepVerdict(sl Sleep, back []string) (status, evidence string) {
	if sl.Err != nil || sl.Slept < time.Second {
		ev := fmt.Sprintf("didn't sleep (mode %s)", orUnknown(sl.Mode))
		if sl.Err != nil {
			ev += ": the kernel refused: " + sl.Err.Error()
		}
		if sl.Failed != "" {
			ev += "; suspend_stats last failed: " + sl.Failed
		}
		if sl.Timing != "" {
			ev += "; " + sl.Timing
		}
		return checks.Failed, ev
	}
	ev := fmt.Sprintf("slept %.1f s of a %s alarm (mode %s), back in %.1f s", sl.Slept.Seconds(), sl.Asked, orUnknown(sl.Mode), sl.Took.Seconds())
	switch {
	case sl.Light:
		ev += "; light sleep (s2idle) by choice, not the Mac's default (deep)"
	case sl.Mode == "s2idle":
		ev += "; s2idle is the shallow sleep (deep isn't offered)"
	}
	if sl.WakeIRQ != "" {
		ev += "; woken by IRQ " + sl.WakeIRQ
	}
	if w := wakeEvidence(sl); w != "" {
		ev += "; " + w
	}
	if sl.Timing != "" {
		ev += "; " + sl.Timing
	}
	var bad []string
	switch {
	case sl.Asked > 0 && sl.Slept < sl.Asked*3/4:
		bad = append(bad, "woke early: something else woke it")
	case sl.Asked > 0 && sl.Slept > sl.Asked+30*time.Second:
		bad = append(bad, "the alarm didn't wake it: something else did, much later")
	}
	bad = append(bad, back...)
	if len(bad) > 0 {
		return checks.Partial, ev + "; " + strings.Join(bad, "; ")
	}
	return checks.Supported, ev + "; every device and the network came back"
}

func orUnknown(s string) string {
	if s == "" {
		return "unknown"
	}
	return s
}

// countdown is the seconds before a nap: 3, or 6 before nap 2, so its reason can be read first.
func (s *sleepWake) countdown() float64 {
	if s.again {
		return 6
	}
	return 3
}

// twoNaps reports whether this Mac may take a second nap: it has a lid (lidwake.go).
func (s *sleepWake) twoNaps(c *Ctx) bool { return lidName(c.IO) != "" }

// lidWoke reports whether the first nap ended early by the lid's own wake with the lid open.
func (s *sleepWake) lidWoke(c *Ctx) bool {
	sl := s.slept
	return !s.again && s.lidOpen && sl.Err == nil && sl.Asked > 0 && sl.Slept >= time.Second &&
		sl.Slept < sl.Asked*3/4 && lidWokeIt(sl) && lidName(c.IO) != ""
}

// retry starts the second nap with the lid's wake off.
func (s *sleepWake) retry(c *Ctx) bool {
	name := lidName(c.IO)
	if !setLidWake(c.IO, name, false) {
		return false
	}
	s.first, s.firstBad, s.lidOff, s.again = s.slept, s.back.problems(), name, true
	s.slept, s.back = Sleep{}, comeback{}
	s.phase, s.at = sleepCount, c.T
	c.Redraw()
	return true
}

// lidBack turns the lid's wake back on after the second nap (or when the test stops).
func (s *sleepWake) lidBack(c *Ctx) {
	if s.lidOff != "" {
		setLidWake(c.IO, s.lidOff, true)
		s.lidOff = ""
	}
}

func (s *sleepWake) decide(c *Ctx) {
	if s.lidWoke(c) && s.retry(c) {
		return
	}
	s.lidBack(c)
	if s.again {
		s.decideAgain(c)
		return
	}
	sl := s.slept
	st, ev := SleepVerdict(sl, s.back.problems())
	if s.nvme != "" {
		ev += "; NVMe " + s.nvme + " d3cold_allowed=0, as Omarchy's installer sets it"
	}
	if w := s.pre.evidence(c.Env); w != "" {
		ev += "; " + w
	}
	var o Outcome
	switch st {
	case checks.Supported:
		o = Outcome{Results: []checks.Result{Result("power.sleep-wake", st, Automatic, ev, i18n.T("slept %.0f s, woke on time, all back", sl.Slept.Seconds()))},
			Say: i18n.T("slept, woke on time, everything's back. good nap.")}
	case checks.Partial:
		o = Outcome{Results: []checks.Result{Result("power.sleep-wake", st, Automatic, ev, i18n.T("woke, but not everything is right"))},
			Say: i18n.T("I woke up, but not everything did. the log has the list.")}
		if len(s.back.missing) == 0 && !s.back.netLost() {
			o.Say = i18n.T("I woke up, but not on my alarm. noted.")
		}
	default:
		detail := i18n.T("didn't go to sleep")
		if errors.Is(sl.Err, ErrNotReal) {
			detail = i18n.T("not put to sleep")
		}
		o = Outcome{Results: []checks.Result{Result("power.sleep-wake", st, Automatic, ev, detail)},
			Say: i18n.T("the kernel wouldn't let me sleep. that's a fail.")}
	}
	s.ask(c, s.afterHang(o))
}

// afterHang keeps the hang on record when the run was picked up after deep sleep hung (see the
// top of the file): a nap that came out supported is partial, anything less failed, and the
// evidence starts with the hang.
func (s *sleepWake) afterHang(o Outcome) Outcome {
	if s.hung == nil || len(o.Results) == 0 {
		return o
	}
	o.Results = append([]checks.Result(nil), o.Results...)
	r := &o.Results[0]
	hung := s.hung.Results[0].Evidence
	if s.light {
		r.Evidence = "deep sleep (this Mac's default) hung on the last run: " + hung + "; this run tried light sleep (s2idle) instead: " + r.Evidence
	} else {
		r.Evidence = "deep sleep hung on the last run: " + hung + "; this run tried deep sleep again: " + r.Evidence
	}
	switch {
	case r.Status == checks.Supported && s.light:
		r.Status, r.Detail = checks.Partial, i18n.T("light sleep works; deep sleep hung")
		o.Say = i18n.T("light sleep worked. deep sleep hung last time, so it's partial.")
	case r.Status == checks.Supported:
		r.Status, r.Detail = checks.Partial, i18n.T("woke this time; hung on the last run")
		o.Say = i18n.T("I woke this time, but I hung last time. that's partial.")
	default:
		r.Status = checks.Failed
		o.Say = i18n.T("still not right, and deep sleep hung last time. that's a fail.")
	}
	return o
}

// decideAgain decides from both naps: the first woken early by the lid, the second with its
// wake off.
func (s *sleepWake) decideAgain(c *Ctx) {
	_, ev1 := SleepVerdict(s.first, s.firstBad)
	st2, ev2 := SleepVerdict(s.slept, s.back.problems())
	ev := ev1 + "; cause: the lid's wake fired with the lid open (Linux arms a Mac's lid wake-on-open before every sleep)" +
		"; nap 2, with the lid's wake off for a lid-open sleep as OmaBoot?'s lid-wake fix does: " + ev2
	if s.nvme != "" {
		ev += "; NVMe " + s.nvme + " d3cold_allowed=0, as Omarchy's installer sets it"
	}
	if w := s.pre.evidence(c.Env); w != "" {
		ev += "; " + w
	}
	var fix *report.BootFix
	if e := c.Env.Checks; e != nil && e.Cat != nil {
		fix = e.Cat.Fix(checks.FixLidWake)
	}
	var r checks.Result
	var say string
	switch {
	case st2 == checks.Supported && fix != nil:
		r = Result("power.sleep-wake", checks.Supported, Automatic, ev, i18n.T("on time with the lid-wake fix"))
		r.FixID, r.Fix = fix.ID, strings.TrimSuffix(fix.Name, " fix") // "lid wake"
		say = i18n.T("my lid woke me early. with its wake off, I slept on time.")
	case st2 == checks.Supported:
		r = Result("power.sleep-wake", checks.Partial, Automatic, ev+"; an installed system needs the fix to stay asleep with the lid open",
			i18n.T("woke early by the lid; on time with its wake off"))
		say = i18n.T("my lid woke me early. with its wake off, I slept on time.")
	default:
		r = Result("power.sleep-wake", checks.Partial, Automatic, ev, i18n.T("woke early, also with the lid's wake off"))
		say = i18n.T("I woke early, and again with the lid's wake off. the log has both naps.")
	}
	s.ask(c, s.afterHang(Outcome{Results: []checks.Result{r}, Say: say}))
}

// CodeKey takes the code typed after waking (Coder): right, the naps' verdict stands; wrong, a
// new code and one more try; wrong twice, partial. Keys in the first moment after a code shows
// are dropped, so nothing typed before the nap answers it.
func (s *sleepWake) CodeKey(c *Ctx, key string) {
	if s.phase != sleepCodePhase || c.T-s.codeAt < proofSettle {
		return
	}
	v, ok := s.typed.key(key)
	if !ok {
		return
	}
	if v == fmt.Sprint(s.code) {
		s.right = true
		s.confirmed(c)
		return
	}
	if s.typed.miss(v, s.code) {
		s.failed = true
		s.unconfirmed(c, checks.Partial, Challenge, "the tester typed the wrong code after waking twice: "+s.typed.evidence(),
			i18n.T("wrong code twice after waking"), i18n.T("two wrong codes. I can't be sure the screen came back right."))
		return
	}
	s.typed.wrong = i18n.T("not %s. a new code is up: one more try", v)
	s.missed, s.missAt = s.code, c.T
	s.code = proofCode(s.rng, s.code)
	s.codeAt, s.at = c.T+codeRed, c.T // keys wait for the new code to show
}

func (s *sleepWake) Act(c *Ctx, i int) {
	if s.phase == sleepCodePhase { // No code, once it has waited codeWait
		if s.late && i == 0 {
			s.unconfirmed(c, checks.Partial, Automatic, fmt.Sprintf("no answer to the code on the screen within %.0f s of waking, then the tester picked No code: the screen may not have come back", codeWait),
				i18n.T("no answer after waking: the screen may not have come back"), i18n.T("no code. the screen may have stayed dark."))
		}
		return
	}
	acts := s.acts(c)
	if s.phase != sleepReady || i < 0 || i >= len(acts) {
		return
	}
	switch a := acts[i]; {
	case a == actEject:
		s.disc.ejectNow()
	case s.pre.done && !s.disc.holds() && !s.fw.holds():
		s.light = a == actLight
		s.phase, s.at = sleepCount, c.T
	}
}

func (s *sleepWake) Stop(c *Ctx) { s.lidBack(c) }

func (s *sleepWake) View(c *Ctx) Screen {
	v := Screen{Face: boot.Idle, SayAt: s.at, OutTitle: i18n.T("What I see")}
	two := s.twoNaps(c)
	v.Steps = []string{i18n.T("Start, then hands off: it naps 20 seconds. Leave the lid open."),
		i18n.T("When it wakes, read the code on the right."), i18n.T("Type the code: no Enter needed.")}
	v.Happens = i18n.T("The screen goes dark while it sleeps, then the alarm wakes it.")
	if two {
		v.Happens = i18n.T("The screen goes dark while it sleeps, then the alarm wakes it. Nap 2 uses the lid wake fix.")
	}
	v.Anim = s.anim(c, two)
	switch s.phase {
	case sleepReady:
		v.Say, v.SayAt = i18n.T("last one: a 20-second nap. I wake up by myself."), 0.2
		v.Hint = i18n.T("whenever you're ready. after Start, I need the keys left alone.")
		off := !s.pre.done || s.disc.holds() || s.fw.holds()
		v.Waiting = i18n.T("·  waiting for Start")
		for _, a := range s.acts(c) {
			switch a {
			case actStart:
				v.Actions = append(v.Actions, Action{Label: i18n.T("Start"), Does: i18n.T("sleep now, wake in 20 s"), Off: off})
			case actLight:
				v.Actions = append(v.Actions, Action{Label: i18n.T("Try light sleep (s2idle)"), Does: i18n.T("a 20 s nap in the lighter sleep"), Off: off})
			case actDeep:
				v.Actions = append(v.Actions, Action{Label: i18n.T("Deep sleep again"), Does: i18n.T("the nap that hung, its steps shown"), Off: off})
			case actEject:
				v.Actions = append(v.Actions, *s.disc.action())
			}
		}
		if s.hung != nil {
			v.Say, v.SayAt, v.Face = i18n.T("deep sleep hung last time. want to try light sleep?"), 0.2, boot.Hmm
			v.Note = i18n.T("Deep sleep didn't come back on the last run: that fail stays on record. Light sleep (s2idle) keeps more of the Mac awake; the result says which sleep it used.")
			if !lightOffered(c.IO) {
				v.Say = i18n.T("deep sleep hung last time. this Mac has no lighter sleep to try.")
				v.Note = i18n.T("Deep sleep didn't come back on the last run: that fail stays on record. This time the kernel's steps print on the screen: if it hangs again, photograph it.")
			}
			v.SkipDoes = i18n.T("the fail from the last run stands")
		}
		if s.disc.action() != nil { // a disc in the drive: no nap until it's out (0.7.3 review)
			v.Say, v.SayAt, v.Face, v.Note = s.disc.say(), 0.2, boot.Hmm, s.disc.note()
		} else if s.fw.holds() { // a FireWire disk: no nap until it's unplugged (0.7.5)
			v.Say, v.SayAt, v.Face, v.Note = s.fw.say(), 0.2, boot.Hmm, s.fw.note(false)
		}
	case sleepCount:
		v.Busy, v.Face, v.Waiting = true, boot.Work, i18n.T("·  going to sleep")
		v.Say = i18n.T("going to sleep in %d…", max(1, int(s.countdown())-int(c.T-s.at)))
		if s.again {
			v.Say = i18n.T("my lid woke me, not my alarm. nap 2, without it, in %d…", max(1, int(s.countdown())-int(c.T-s.at)))
		}
	case sleepAsleep:
		v.Busy, v.Waiting = true, i18n.T("·  asleep")
		v.Say, v.Face = i18n.T("zzz."), boot.Sleep
	case sleepBackPhase:
		v.Busy, v.Waiting = true, i18n.T("·  checking what came back")
		v.Say, v.Face = i18n.T("and I'm back. counting what woke up with me…"), boot.Work
	case sleepCodePhase:
		v.Step = 1
		if s.typed.value != "" || len(s.typed.misses) > 0 {
			v.Step = 2
		}
		v.Say, v.SayAt = i18n.T("back! read me the code."), s.codeAt
		if len(s.typed.misses) > 0 {
			v.Say = i18n.T("not that one. a new code: read me this one.")
		}
		v.Hint = i18n.T("the big digits on the right. a dark screen? then skip it.")
		v.Code = s.typed.field(i18n.T("The code on the right"))
		v.SkipDoes = i18n.T("it goes in unconfirmed")
		v.Waiting = i18n.T("·  waiting for the code")
		if s.late {
			v.Say, v.SayAt, v.Face, v.Hint = i18n.T("no answer yet. see the code? type it. if not, press N."), s.at, boot.Hmm, "" // the hint would hide it
			v.Actions = []Action{{Label: i18n.T("No code"), Does: i18n.T("I can't see one: it goes in as partial"), Key: "n"}}
			v.Waiting = i18n.T("·  waiting for the code, or No code")
		}
	}
	if s.again {
		v.Live = append(v.Live, Fact(i18n.T("nap 1: woke after %.1f s, by the lid's wake", s.first.Slept.Seconds())),
			FixLine(i18n.T("nap 2: OmaBoot? lid wake fix in use: the lid's wake is off for this nap")))
	}
	if s.slept.Saved != "" {
		v.Live = append(v.Live, Fact(i18n.T("before the nap: %s", s.slept.Saved)))
	}
	if s.slept.Slept > 0 {
		v.Live = append(v.Live, Fact(i18n.T("slept %.1f s (mode %s)", s.slept.Slept.Seconds(), orUnknown(s.slept.Mode))))
		if len(s.slept.WokenBy) > 0 {
			v.Live = append(v.Live, Fact(i18n.T("woken by: %s", strings.Join(s.slept.WokenBy, ", "))))
		}
	} else if s.light {
		v.Live = append(v.Live, Fact(i18n.T("sleep mode: s2idle (light sleep, picked after deep sleep hung)")))
	} else if m := memSleep(c.IO); m != "" {
		v.Live = append(v.Live, Fact(i18n.T("sleep mode: %s", m)))
	}
	if s.phase == sleepBackPhase || s.phase == sleepCodePhase {
		v.Live = append(v.Live, s.back.lines()...)
	} else {
		v.Live = append(v.Live, s.pre.line(c.Env))
		if l := s.disc.line(); l != nil && s.phase == sleepReady {
			v.Live = append(v.Live, *l)
		}
		if s.phase == sleepReady {
			v.Live = append(v.Live, s.fw.lines()...)
		}
	}
	for _, m := range s.typed.tried {
		v.Live = append(v.Live, Bad(i18n.T("you typed %s: not the code shown", m)))
	}
	if s.lateBy > 0 {
		v.Live = append(v.Live, Fact(i18n.T("the code, typed late: %.0f s after it showed. it still counts.", s.lateBy)))
	}
	return v
}

// anim is the band's picture: each nap and how it went, then the z's while it sleeps, the
// countdown before it, and the code once it's back.
func (s *sleepWake) anim(c *Ctx, two bool) Panel {
	type row struct {
		name, text string
		now        bool
	}
	var rows []row
	state := func(sl Sleep, phase sleepPhase) string {
		switch phase {
		case sleepReady:
			return i18n.T("20 s, the alarm set to wake it")
		case sleepCount:
			return i18n.T("starting")
		case sleepAsleep:
			return i18n.T("asleep")
		}
		return i18n.T("slept %.1f s", sl.Slept.Seconds())
	}
	switch {
	case s.again:
		rows = append(rows, row{i18n.T("nap 1 of 2"), i18n.T("the lid woke it at %.1f s", s.first.Slept.Seconds()), false},
			row{i18n.T("nap 2 of 2"), i18n.T("lid's wake off: %s", state(s.slept, s.phase)), true})
	case two:
		rows = append(rows, row{i18n.T("nap 1 of 2"), state(s.slept, s.phase), true},
			row{i18n.T("nap 2 of 2"), i18n.T("only if the lid wakes it early"), false})
	default:
		rows = append(rows, row{i18n.T("the nap"), state(s.slept, s.phase), true})
	}
	phase, code, at := s.phase, s.code, s.at
	codeCol := g.WHT
	switch {
	case s.right:
		codeCol = g.BGRN
	case s.failed:
		codeCol = g.BRED
	case s.missAt > 0 && c.T-s.missAt < codeRed: // the missed code, red, before the new one
		code, codeCol = s.missed, g.BRED
	}
	left := max(1, int(s.countdown())-int(c.T-s.at))
	return func(gr *g.Grid, x, y, w, h int, t float64) {
		nw := 0
		for _, r := range rows {
			nw = max(nw, width(r.name))
		}
		for i, r := range rows {
			nc, tc := g.GRY, g.DIM
			if r.now && phase != sleepCodePhase {
				nc, tc = g.WHT, g.BVIO
			}
			gr.Put(x+2, y+i, r.name, nc)
			gr.Put(x+4+nw, y+i, cut(r.text, max(0, w-6-nw)), tc)
		}
		top := y + len(rows) + 1
		switch phase {
		case sleepCount:
			gr.Put(x+2, top, i18n.T("going to sleep in"), g.DIM)
			bigCode(left)(gr, x+2, top+1, w-2, bigCodeH, t)
		case sleepAsleep:
			boot.Nap.Draw(gr, boot.Params{}, x, top, w, y+h-top, 1.2+math.Mod(t-at, 1.5))
		case sleepCodePhase:
			gr.Put(x+2, top, i18n.T("the code"), g.DIM)
			bigCodeIn(code, codeCol)(gr, x+2, top+1, w-2, bigCodeH, t)
		}
	}
}
