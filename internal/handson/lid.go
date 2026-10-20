package handson

import (
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/omaboot/boot-live/internal/boot"
	"github.com/omaboot/boot-live/internal/checks"
	g "github.com/omaboot/boot-live/internal/grid"
	"github.com/omaboot/boot-live/internal/i18n"
)

// power.lid, "Lid close sleep and lid open wake": the person closes the lid, counts to five and
// opens it. The tool does what an installed system's logind does on a closed lid, puts the Mac
// to sleep (holding logind's own lid handling back meanwhile, so it doesn't do it twice), with
// the RTC alarm as a backstop a minute later; opening the lid should wake it before that. The
// machine decides (method automatic) from the lid switch (SW_LID on the lid's input device, or
// /proc/acpi/button/lid) and from how long the Mac slept.
//
//   - supported: the switch reported the lid closed and open again, the Mac slept on the close
//     and woke on the open, before the backstop, by the lid's own wake (lidwake.go's lidWokeIt:
//     the lid's PNP0C0D wakeup source fired and the RTC alarm didn't), and within 30 s had back
//     every device and the network it had before (comeback.go, as the sleep test);
//   - partial: the switch works but the Mac didn't go to sleep (the kernel refused), or it woke
//     only by the backstop alarm, with the lid already open (opening it didn't wake it), or the
//     wake sources were read and the lid's didn't fire ("woke, but not by the lid": something
//     else woke it as the lid opened), or something didn't come back (listed);
//   - where the wake sources couldn't be read (Sleep.Probed false), "woke on the open" is
//     inferred from the timing alone, as before 0.5.0, and the evidence says so;
//   - failed: no lid switch at all on a laptop, or the tester closed and opened the lid and the
//     switch reported nothing ("It didn't notice", method observed).
//
// It doesn't sleep while the optical drive holds a disc or works one (discguard.go): closing the
// lid does nothing then, and the test offers "Eject disc". Nor while a FireWire disk is attached
// (fwguard.go: it hung the iMac10,1's sleep): closing the lid does nothing until it's unplugged.
//
// Waking by the backstop with the lid still closed (kept closed too long) decides nothing: the
// person is asked to open it and try again.

func init() {
	Register(Spec{ID: "power.lid", Group: Last, Order: 10, Sleeps: true, Title: i18n.M("Lid"),
		Brief: i18n.M("close the lid, count to five, open it"), Expect: 40 * time.Second,
		New: func(*Env) Run { return &lid{} }, Demo: func(f *Fake) {
			f.SleepAs.Slept, f.SleepAs.Took, f.SleepFor = 6*time.Second, 7*time.Second, 3
			f.SleepAs.Probed, f.SleepAs.WokenBy, f.SleepAs.Events = true, []string{"LID0 (PNP0C0D:00)"}, []string{"GPE 0x23 +1"}
			f.Event(4, "event0", EvSw, SwLid, 1)
			f.SetAt(4, "/proc/acpi/button/lid/LID0/state", "state:      closed")
			f.Event(7.6, "event0", EvSw, SwLid, 0) // opened while asleep: delivered on the way back
			f.SetAt(7.6, "/proc/acpi/button/lid/LID0/state", "state:      open")
		}})
}

// lidBackstop is the RTC alarm that wakes the Mac if opening the lid doesn't.
const lidBackstop = 60 * time.Second

// lidHold is how long the lid stays closed before the Mac is put to sleep: a quick flap of the
// lid doesn't count.
const lidHold = 1.0

type lidPhase int

const (
	lidOpen     lidPhase = iota // waiting for the close
	lidClosed                   // closed, about to sleep
	lidSleeping                 // asleep (or trying)
	lidRetry                    // woke by the backstop with the lid closed: waiting for it to open
	lidRefused                  // the kernel didn't sleep: waiting for the open
	lidBack                     // awake, lid open: waiting for the devices and the network
	lidDone                     // decided
)

type lid struct {
	in      Input
	devs    []Device
	proc    string // /proc/acpi/button/lid/*/state
	release func()
	phase   lidPhase
	closed  bool
	at      float64 // when the phase began
	sleep   Future[Sleep]
	slept   Sleep
	polled  float64
	trail   []string
	flaps   int
	pre     before    // the network before the nap
	back    comeback  // what came back after it
	disc    discGuard // the optical drive: no nap with a disc in it or a read under way
	fw      fwGuard   // no nap with a FireWire disk attached
}

func (l *lid) Start(c *Ctx) {
	if ps := c.IO.Glob("/proc/acpi/button/lid/*/state"); len(ps) > 0 {
		l.proc = ps[0]
	}
	l.in, l.devs = OpenAll(c.IO, func(d Device) bool { return d.Has("sw", SwLid) }, false)
	if len(l.devs) == 0 && l.proc == "" {
		c.Decide(Outcome{Results: []checks.Result{Result("power.lid", checks.Failed, Automatic,
			"no lid switch: no input device reports SW_LID and /proc/acpi/button/lid is empty", i18n.T("no lid switch"))},
			Say: i18n.T("I can't find a lid switch on this laptop. that's a fail.")})
		return
	}
	l.closed = strings.Contains(c.IO.Read(l.proc), "closed")
	l.release = c.IO.InhibitLid()
	l.pre.start(c)
	l.disc.start(c)
	l.fw.step(c)
	c.Select(-1) // waiting for the lid: a stray Enter answers nothing
}

// poll reads the lid: switch events first, then /proc (also what comes back after a resume).
func (l *lid) poll(c *Ctx) {
	for _, e := range l.in.Events() {
		if e.Type == EvSw && e.Code == SwLid {
			l.closed = e.Value == 1
		}
	}
	if l.proc != "" && c.T-l.polled >= 0.2 {
		l.polled = c.T
		l.closed = strings.Contains(c.IO.Read(l.proc), "closed")
	}
}

func (l *lid) note(s string) { l.trail = append(l.trail, s) }

func (l *lid) Step(c *Ctx) {
	if l.in == nil {
		return
	}
	was := l.closed
	l.poll(c)
	l.pre.step(c)
	if l.phase == lidOpen || l.phase == lidClosed {
		l.disc.step(c, l.phase == lidClosed)
		l.fw.step(c)
	}
	switch l.phase {
	case lidOpen:
		if l.closed && !was && !l.disc.holds() && !l.fw.holds() { // a disc in the drive or a FireWire disk: no nap (closing does nothing)
			l.phase, l.at = lidClosed, c.T
			l.note("closed")
		}
	case lidClosed:
		switch {
		case !l.closed:
			l.phase, l.at = lidOpen, c.T
			l.flaps++
			l.note("opened again at once")
		case l.fw.holds(): // a FireWire disk plugged in with the lid shut: no nap
			l.phase, l.at = lidOpen, c.T
		case c.T-l.at >= lidHold && l.disc.asked():
			l.phase, l.at = lidSleeping, c.T
			l.back.arm(c, l.pre.net.Up)
			l.sleep = c.IO.Suspend(lidBackstop, SleepHow{Test: "power.lid", Title: "Lid"})
		}
	case lidSleeping:
		sl, ok := l.sleep.Ready()
		if !ok {
			return
		}
		l.slept, l.at = sl, c.T
		c.Redraw()
		l.poll(c)
		if sl.Err != nil || sl.Slept < time.Second {
			l.note("didn't sleep")
			l.phase = lidRefused
			if !l.closed {
				l.decide(c)
			}
			return
		}
		l.note(fmt.Sprintf("slept %s", sl.Slept.Round(100*time.Millisecond)))
		if l.closed {
			l.phase = lidRetry
			if sl.Slept >= lidBackstop-3*time.Second {
				l.note("woke by the backstop with the lid closed")
			} else {
				l.note("woke early with the lid still closed")
			}
			return
		}
		l.phase = lidBack
		l.back.woke(c)
	case lidBack:
		if l.back.step(c) {
			l.decide(c)
		}
	case lidRetry:
		if !l.closed {
			l.phase, l.at = lidOpen, c.T
			l.note("opened")
		}
	case lidRefused:
		if !l.closed {
			l.decide(c)
		}
	}
}

func (l *lid) evidence(more string) string {
	src := "/proc/acpi/button/lid"
	if len(l.devs) > 0 {
		src = "SW_LID on " + l.devs[0].Name
	}
	ev := "lid switch: " + src + "; " + strings.Join(append(l.trail, "opened"), ", ")
	if l.slept.Mode != "" {
		ev += "; sleep mode " + l.slept.Mode
	}
	if w := wakeEvidence(l.slept); w != "" {
		ev += "; " + w
	}
	if l.slept.Timing != "" { // the kernel's suspend diagnostics (sleepdiag.go)
		ev += "; " + l.slept.Timing
	}
	return ev + more
}

func (l *lid) decide(c *Ctx) {
	l.phase = lidDone
	sl := l.slept
	var o Outcome
	switch {
	case sl.Err != nil || sl.Slept < time.Second:
		why := "it didn't go to sleep"
		if sl.Err != nil {
			why += ": " + sl.Err.Error()
		}
		o = Outcome{Results: []checks.Result{Result("power.lid", checks.Partial, Automatic, l.evidence("; the switch works but "+why), i18n.T("switch works, no sleep"))},
			Say: i18n.T("the lid switch works, but the Mac didn't go to sleep.")}
	case sl.Slept >= lidBackstop-3*time.Second:
		o = Outcome{Results: []checks.Result{Result("power.lid", checks.Partial, Automatic,
			l.evidence(fmt.Sprintf("; woke only by the %s backstop alarm, not by opening the lid", lidBackstop)+l.backNote(c.Env)), i18n.T("opening the lid didn't wake it"))},
			Say: i18n.T("it slept, but opening the lid didn't wake it. my alarm did.")}
	case sl.Probed && !lidWokeIt(sl):
		what := strings.Join(append(append([]string(nil), sl.WokenBy...), sl.Events...), ", ")
		if what == "" {
			what = "no wakeup source or ACPI event counted"
		}
		o = Outcome{Results: []checks.Result{Result("power.lid", checks.Partial, Automatic,
			l.evidence("; woke, but not by the lid: "+what+l.backNote(c.Env)), i18n.T("woke, but not by the lid"))},
			Say: i18n.T("I woke up, but the lid's wake didn't do it. noted as partial.")}
	case len(l.back.problems()) > 0:
		o = Outcome{Results: []checks.Result{Result("power.lid", checks.Partial, Automatic, l.evidence("; woke when the lid opened"+l.wokeBy()+l.backNote(c.Env)),
			i18n.T("woke on open, but not everything came back"))}, Say: i18n.T("lid up, awake, but not everything woke with me. the log has the list.")}
	default:
		o = Outcome{Results: []checks.Result{Result("power.lid", checks.Supported, Automatic, l.evidence("; woke when the lid opened"+l.wokeBy()+l.backNote(c.Env)),
			i18n.T("slept on close, woke on open"))}, Say: i18n.T("lid down, asleep. lid up, awake. textbook.")}
	}
	c.Decide(o)
}

// wokeBy says how "woke when the lid opened" is known.
func (l *lid) wokeBy() string {
	if l.slept.Probed {
		return ", by the lid's own wake source (no RTC alarm)"
	}
	return " (inferred from the timing: the wake sources couldn't be read)"
}

// backNote is what didn't come back, and the network before the nap, for the evidence.
func (l *lid) backNote(e *Env) string {
	var parts []string
	parts = append(parts, l.back.problems()...)
	if w := l.pre.evidence(e); w != "" {
		parts = append(parts, w)
	}
	if len(parts) == 0 {
		return ""
	}
	return "; " + strings.Join(parts, "; ")
}

func (l *lid) Act(c *Ctx, i int) {
	if i == 1 && l.phase == lidOpen {
		l.disc.ejectNow()
		return
	}
	if i != 0 || l.phase != lidOpen || c.T < 15 || l.disc.holds() || l.fw.holds() {
		return
	}
	c.Decide(Outcome{Results: []checks.Result{Result("power.lid", checks.Failed, Observed,
		l.evidenceNone(), i18n.T("the switch reported nothing"))}, Say: i18n.T("lid shut and open, and the switch never said a word.")})
}

func (l *lid) evidenceNone() string {
	return "the tester closed and opened the lid; the lid switch reported nothing (" + strings.Join(l.trail, ", ") + ")"
}

func (l *lid) Stop(*Ctx) {
	if l.in != nil {
		l.in.Close()
	}
	if l.release != nil {
		l.release()
	}
}

func (l *lid) View(c *Ctx) Screen {
	s := Screen{Face: boot.Idle, SayAt: l.at, OutTitle: i18n.T("What I see"), Anim: l.anim()}
	s.Steps = []string{i18n.T("Close the lid."), i18n.T("Count to five, then open it."), i18n.T("Wait while I check what woke up.")}
	s.Happens = i18n.T("It sleeps when the lid closes, as an installed system does, and should wake when it opens.")
	switch l.phase {
	case lidOpen:
		s.Say, s.SayAt = i18n.T("close the lid. I'll nap until you open it."), 0.2
		if l.flaps > 0 {
			s.Say, s.SayAt = i18n.T("a bit longer, please: keep it closed until five."), l.at
		}
		s.Hint = i18n.T("all the way shut, until it clicks. I'll be asleep till you open it.")
		s.Waiting = i18n.T("·  waiting for the lid")
	case lidClosed, lidSleeping:
		s.Step = 1
		s.Say, s.Face, s.Busy = i18n.T("lid's down. going to sleep…"), boot.Sleep, true
		s.Waiting = i18n.T("·  asleep")
	case lidRetry:
		s.Step = 1
		s.Steps[1] = i18n.T("Open the lid, then try again: close, count to five, open.")
		s.Happens = i18n.T("My alarm woke the Mac with the lid still closed: it was closed too long.")
		s.Say, s.Face = i18n.T("my alarm woke me first. once more, a bit quicker."), boot.Hmm
		s.Hint = i18n.T("open it, then shut it again. five seconds is plenty.")
		s.Waiting = i18n.T("·  waiting for the lid to open")
	case lidRefused:
		s.Step = 1
		s.Steps[1] = i18n.T("Open the lid.")
		s.Say, s.Face = i18n.T("the lid closed, but the Mac wouldn't sleep."), boot.Concerned
		s.Waiting = i18n.T("·  waiting for the lid to open")
	case lidBack:
		s.Step, s.Busy = 2, true
		s.Say, s.Face = i18n.T("and I'm back. counting what woke up with me…"), boot.Work
		s.Waiting = i18n.T("·  checking what came back")
	case lidDone:
		s.Step = 3
	}
	src := i18n.T("lid switch: /proc/acpi/button/lid")
	if len(l.devs) > 0 {
		src = i18n.T("lid switch: %s (%s)", l.devs[0].Name, l.devs[0].Event)
	}
	if len(l.devs) == 0 && l.proc == "" {
		s.Live = []Line{Bad(i18n.T("no lid switch found"))}
		return s
	}
	st := i18n.T("open")
	if l.closed {
		st = i18n.T("closed")
	}
	s.Live = []Line{Fact(src), Fact(i18n.T("the lid is %s", st))}
	switch l.phase {
	case lidOpen:
		s.Live = append(s.Live, Wait(i18n.T("waiting for the lid to close")))
	case lidSleeping:
		s.Live = append(s.Live, Wait(i18n.T("going to sleep; the alarm wakes me in %d s if the lid doesn't", int(lidBackstop.Seconds()))))
	}
	if l.slept.Slept > 0 {
		s.Live = append(s.Live, Fact(i18n.T("slept %.1f s", l.slept.Slept.Seconds())))
		if len(l.slept.WokenBy) > 0 {
			s.Live = append(s.Live, Fact(i18n.T("woken by: %s", strings.Join(l.slept.WokenBy, ", "))))
		}
	}
	if l.phase == lidBack || l.phase == lidDone && l.back.checked {
		s.Live = append(s.Live, l.back.lines()...)
	} else if l.phase == lidOpen || l.phase == lidClosed {
		s.Live = append(s.Live, l.pre.line(c.Env))
	}
	s.Actions = []Action{{Label: i18n.T("It didn't notice"), Does: i18n.T("I closed and opened the lid: nothing here"), Off: c.T < 15 || l.phase != lidOpen || l.disc.holds() || l.fw.holds()}}
	if a := l.disc.action(); a != nil && l.phase == lidOpen { // a disc in the drive: no nap until it's out (0.7.3 review)
		s.Actions = append(s.Actions, *a)
		s.Say, s.SayAt, s.Face, s.Note = l.disc.say(), 0.2, boot.Hmm, l.disc.note()
		s.Steps[0] = i18n.T("Take the disc out of the optical drive first (Eject disc).")
	} else if l.fw.holds() && l.phase == lidOpen { // a FireWire disk: no nap until it's unplugged (0.7.5)
		s.Say, s.SayAt, s.Face, s.Note = l.fw.say(), 0.2, boot.Hmm, l.fw.note(true)
		s.Steps[0] = i18n.T("Unplug the FireWire disk first.")
	}
	if d := l.disc.line(); d != nil && l.phase == lidOpen {
		s.Live = append(s.Live, *d)
	}
	if l.phase == lidOpen {
		s.Live = append(s.Live, l.fw.lines()...)
	}
	return s
}

// anim is the band's picture: the Mac from the side, its lid open or shut as the switch says,
// and the z's while it sleeps.
func (l *lid) anim() Panel {
	closed, phase, at := l.closed, l.phase, l.at
	return func(gr *g.Grid, x, y, w, h int, t float64) {
		bx, by := x+4, y+h-3 // the base's left end, its row
		const bw = 26
		col := g.GRY
		gr.Put(bx, by, strings.Repeat("▀", bw), col)
		if closed {
			gr.Put(bx, by-1, strings.Repeat("▄", bw-1), g.BVIO)
			gr.Put(bx+bw+3, by-1, i18n.T("lid closed"), g.BVIO)
		} else {
			for j := 1; j <= min(8, h-3); j++ { // the lid, tilted back from the hinge
				gr.Set(bx+(8-j)/3, by-j, '█', col)
			}
			gr.Put(bx+bw+3, by-1, i18n.T("lid open"), g.GRY)
		}
		if phase == lidSleeping || phase == lidClosed && t-at > 0.5 {
			boot.Nap.Draw(gr, boot.Params{}, bx+bw+16, y, max(0, w-(bx+bw+16-x)), h, 1.2+math.Mod(t-at, 1.5))
		}
	}
}
