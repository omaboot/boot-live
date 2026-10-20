package handson

import (
	"fmt"

	"github.com/omaboot/boot-live/internal/checks"
	"github.com/omaboot/boot-live/internal/i18n"
)

// Setup is what the screens need to run the hands-on tests on this Mac.
type Setup struct {
	Env  *Env
	Plan []Spec
	// IO gives the IO a run of a test works on: the Mac's (or a fixture tree's, which writes
	// nothing), or in the demo a fresh scripted one for each visit.
	IO func(s Spec) IO
}

// Minutes is roughly how long the planned tests take, in whole minutes (at least 1).
func (s Setup) Minutes() int {
	sec := 0.0
	for _, t := range s.Plan {
		sec += t.Expect.Seconds()
	}
	return max(1, int(sec/60+0.5))
}

// Runner is one test on screen: its Spec, its Run and their Ctx.
type Runner struct {
	Spec    Spec
	Run     Run
	Ctx     *Ctx
	stopped bool
	left    bool // Leave has run
}

// Begin opens a test's screen: a new Run of it, started.
func Begin(s Spec, e *Env, io IO) *Runner {
	c := &Ctx{IO: io, Env: e, Spec: s}
	io.Advance(0)
	r := &Runner{Spec: s, Run: s.New(e), Ctx: c}
	r.Run.Start(c)
	return r
}

// Step moves the run on to t (seconds since its screen opened). Once it has decided, it is
// left alone.
func (r *Runner) Step(t float64) {
	if r.stopped {
		return
	}
	r.Ctx.T = t
	r.Ctx.IO.Advance(t)
	if r.Ctx.out == nil {
		r.Run.Step(r.Ctx)
	}
}

// View is what the run shows now.
func (r *Runner) View() Screen { return r.Run.View(r.Ctx) }

// Act passes the person's answer on, until the run has decided.
func (r *Runner) Act(i int) {
	if !r.stopped && r.Ctx.out == nil {
		r.Run.Act(r.Ctx, i)
	}
}

// TakesKeys reports whether the run takes the keyboard now.
func (r *Runner) TakesKeys() bool {
	k, ok := r.Run.(KeyTaker)
	return ok && !r.stopped && r.Ctx.out == nil && k.TakesKeys(r.Ctx)
}

// Key passes a console key to a run that takes the keyboard.
func (r *Runner) Key(key string) {
	if k, ok := r.Run.(KeyTaker); ok && r.TakesKeys() {
		k.Key(r.Ctx, key)
	}
}

// Codes reports whether the run takes a typed code now (its Screen has a Code field).
func (r *Runner) Codes() bool {
	_, ok := r.Run.(Coder)
	return ok && !r.stopped && r.Ctx.out == nil && r.View().Code != nil
}

// CodeKey passes a key typed into the code field to the run.
func (r *Runner) CodeKey(key string) {
	if k, ok := r.Run.(Coder); ok && r.Codes() {
		k.CodeKey(r.Ctx, key)
	}
}

// Skip is "Skip this one": not_tested for each criterion it would have decided, with reason
// no-equipment when the test needs gear the person may not have, else other.
// Boot always says why (a port test's box under the band was left blank, 0.7.2 review). A
// Skipper may take it as an answer instead; Skip reports whether the test was skipped.
func (r *Runner) Skip() bool {
	if k, ok := r.Run.(Skipper); ok && !r.stopped && r.Ctx.out == nil && k.Skip(r.Ctx) {
		return false
	}
	var rs []checks.Result
	say := i18n.T("skipped. noted as not tested.")
	for _, id := range Decidable(r.Ctx.Env, r.Spec) {
		x := NotTested(id, checks.Other, "skipped by the tester", i18n.T("skipped by you"))
		switch {
		case r.Spec.Without != "":
			x = NotTested(id, checks.NoEquipment, "skipped by the tester: "+r.Spec.Without, i18n.S(r.Spec.Without))
		case r.Spec.Gear != "":
			x = NotTested(id, checks.NoEquipment, "skipped by the tester (it needs "+r.Spec.Gear+")", i18n.T("needs %s", i18n.S(r.Spec.Gear)))
		}
		rs = append(rs, x)
	}
	switch {
	case r.Spec.Without != "":
		say = i18n.T("%s. skipped: noted as not tested.", i18n.S(r.Spec.Without))
	case r.Spec.Gear != "":
		say = i18n.T("skipped: it needs %s. noted as not tested.", i18n.S(r.Spec.Gear))
	}
	r.Ctx.out = nil
	r.Ctx.Decide(Outcome{Results: rs, Say: say})
	return true
}

// Outcome is the run's outcome once decided, or nil: only the criteria that apply to this Mac
// and may be decided live, each with a label for the log.
func (r *Runner) Outcome() *Outcome {
	o := r.Ctx.out
	if o == nil {
		return nil
	}
	ok := map[string]bool{}
	for _, id := range Decidable(r.Ctx.Env, r.Spec) {
		ok[id] = true
	}
	out := Outcome{Say: o.Say}
	for _, x := range o.Results {
		if !ok[x.ID] {
			continue
		}
		if x.Label == "" {
			x.Label = r.Spec.Title
		}
		if x.Detail == "" {
			x.Detail = cut(x.Evidence, 60)
		}
		if x.Status == checks.NotTested {
			x.Method = ""
		}
		out.Results = append(out.Results, x)
	}
	if p, ok := r.Run.(porter); ok { // the port tests: their answers ride on the test's own result
		for i := range out.Results {
			if out.Results[i].ID == r.Spec.ID && out.Results[i].Ports == nil {
				out.Results[i].Ports = p.PortAnswers(r.Ctx)
			}
		}
	}
	return &out
}

// NoComeback is the outcome of a test the Mac didn't come back from: it hung (a sleep that
// never woke), the Mac was forced off, and the run was picked up from the copy saved before the
// test. Each criterion the test decides here is failed; from says which
// copy ("boot-live 0.7.3 dev, 2026-10-15 18:45 UTC").
func NoComeback(e *Env, s Spec, from string) Outcome {
	what := "the Mac didn't come back from the " + s.Title + " test"
	switch s.ID {
	case "power.sleep-wake":
		what = "the Mac didn't come back from sleep"
	case "power.lid":
		what = "the Mac didn't come back from the lid test's sleep"
	}
	ev := what + ": the run was picked up from the copy saved before it (" + from + ")"
	var rs []checks.Result
	for _, id := range Decidable(e, s) {
		x := Result(id, checks.Failed, Automatic, ev, i18n.T("the Mac didn't come back from it"))
		x.Label = s.Title
		rs = append(rs, x)
	}
	return Outcome{Results: rs, Say: i18n.T("the Mac didn't come back from this one last time. that's a fail.")}
}

// porter is a port test: it matched ports to the map's connectors.
type porter interface {
	PortAnswers(c *Ctx) []checks.PortAnswer
}

// Stop leaves the screen: the run undoes what it changed. Safe to call more than once.
func (r *Runner) Stop() {
	if r.stopped {
		return
	}
	r.stopped = true
	r.Run.Stop(r.Ctx)
}

// Leave is Stop, then the run's Leave (a Leaver) once: its screen is left for good. Safe to call
// more than once.
func (r *Runner) Leave() {
	r.Stop()
	if l, ok := r.Run.(Leaver); ok && !r.left {
		r.left = true
		l.Leave(r.Ctx)
	}
}

func cut(s string, n int) string {
	rs := []rune(s)
	if len(rs) <= n {
		return s
	}
	return string(rs[:n-1]) + "…"
}

// NotBuilt is a stub's New: it decides at once, not_tested (other) "not built yet". Stubs are
// also marked Stub, so the flow never shows them; this is only what one would say if it did.
func NotBuilt(e *Env) Run { return notBuilt{} }

type notBuilt struct{}

func (notBuilt) Start(c *Ctx) {
	var rs []checks.Result
	for _, id := range c.Spec.IDs() {
		rs = append(rs, NotTested(id, checks.Other, "not built yet", ""))
	}
	c.Decide(Outcome{Results: rs})
}
func (notBuilt) Step(*Ctx)        {}
func (notBuilt) View(*Ctx) Screen { return Screen{} }
func (notBuilt) Act(*Ctx, int)    {}
func (notBuilt) Stop(*Ctx)        {}

// PlanSkipped is the outcome of a test the run plan skips: it isn't opened, and
// each criterion it would decide here is not tested, reason other.
func PlanSkipped(e *Env, s Spec) Outcome {
	var rs []checks.Result
	for _, id := range Decidable(e, s) {
		x := NotTested(id, checks.Other, "skipped by the run plan", i18n.T("skipped by the run plan"))
		x.Label = s.Title
		rs = append(rs, x)
	}
	return Outcome{Results: rs, Say: i18n.T("the run plan skips this one.")}
}

// PlanTimeout is the outcome of a setup test (the run plan's: its equipment in place, no one
// at the Mac) that didn't decide within secs: not tested, reason uncertain.
func PlanTimeout(e *Env, s Spec, secs float64) Outcome {
	var rs []checks.Result
	for _, id := range Decidable(e, s) {
		x := NotTested(id, checks.Uncertain, fmt.Sprintf("the run plan's setup test didn't decide in %.0f s: its equipment wasn't in place, or it needed a person", secs),
			i18n.T("didn't decide in %.0f s", secs))
		x.Label = s.Title
		rs = append(rs, x)
	}
	return Outcome{Results: rs, Say: i18n.T("nothing to decide on in %.0f s. not tested.", secs)}
}

// Force decides the run with o, when it hasn't decided yet (the run plan's timeout).
func (r *Runner) Force(o Outcome) {
	if !r.stopped {
		r.Ctx.Decide(o)
	}
}
