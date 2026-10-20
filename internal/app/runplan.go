package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/omaboot/boot-live/internal/handson"
	"github.com/omaboot/boot-live/internal/plan"
	"github.com/omaboot/boot-live/internal/send"
	"github.com/omaboot/boot-live/internal/ui"
)

// The run plan (internal/plan, LIVE-PLAN §30): read from the stick in Setup, before Hello; the
// screens drive the run from it (ui/runplan.go) and tell the backend what happened, for the
// report's notes.

// runPlan is the plan as this run found it.
type runPlan struct {
	found    bool
	problems []string
	section  bool      // the file has a section for this Mac
	run      *plan.Run // this Mac's plan, nil when there's none to use
	off      bool      // the person ignored it at Hello
	waiting  bool      // start: console, not started yet
	stopped  bool      // the console's plan stop
	counts   string    // how many tests in each mode, as the report says it
	events   []string  // what fell back, in English
	bugs     []string  // the bug reports saved while it ran
}

// readPlan reads the plan file from the stick: nil and no error when there's none.
func (r *Run) readPlan() ([]byte, error) {
	var src []byte
	ctx, cancel := context.WithTimeout(r.ctx, 20*time.Second)
	defer cancel()
	err := send.WithStick(ctx, r.Opt.Root, r.Opt.StickDir, false, func(base string) error {
		var err error
		src, err = os.ReadFile(filepath.Join(base, plan.File))
		return err
	})
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	return src, err
}

// planKnown says which test IDs a plan may name: this build's hands-on tests.
var planKnown = plan.Known{Test: func(id string) bool { _, ok := handson.Lookup(id); return ok }}

// loadPlan reads the plan from the stick, checks it and picks this Mac's section.
func (r *Run) loadPlan() {
	if r.Fixture() && r.Opt.StickDir == "" {
		return
	}
	var src []byte
	ctx, cancel := context.WithTimeout(r.ctx, 20*time.Second)
	defer cancel()
	send.WithStick(ctx, r.Opt.Root, r.Opt.StickDir, false, func(base string) error {
		b, err := os.ReadFile(filepath.Join(base, plan.File))
		if err == nil {
			src = b
		}
		return nil
	})
	if src == nil {
		return
	}
	p, probs := plan.Parse(src, planKnown)
	rp := &runPlan{found: true}
	for _, x := range probs {
		rp.problems = append(rp.problems, x.String())
	}
	if p != nil {
		configs := []string{r.Ident.Config}
		for _, c := range r.Ident.Candidates {
			configs = append(configs, c.Config)
		}
		if run, ok := p.For(r.Ident.Identifier, configs...); ok {
			rp.section, rp.run, rp.waiting = true, &run, run.Start == "console"
		}
	}
	r.mu.Lock()
	r.plan = rp
	r.mu.Unlock()
}

// active is this Mac's plan when it's in use, or nil.
func (r *Run) activePlan() *plan.Run {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.plan == nil || r.plan.off || r.plan.waiting || r.plan.stopped {
		return nil
	}
	return r.plan.run
}

// planFacts is the plan for the screens, or nil without a file.
func (r *Run) planFacts() *ui.PlanFacts {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.plan == nil {
		return nil
	}
	pf := &ui.PlanFacts{File: plan.File, Problems: r.plan.problems, Run: r.plan.run, Best: r.Ident.Best()}
	if r.plan.problems == nil && !r.plan.section {
		pf.NoSection = r.Ident.Identifier
	}
	return pf
}

// planNote is the report's note on the plan, or "" when none ran.
func (r *Run) planNote() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	p := r.plan
	if p == nil || p.run == nil || p.off || p.waiting {
		return ""
	}
	how := "attended"
	if p.run.Unattended() {
		how = "unattended"
	}
	s := fmt.Sprintf("Run plan (%s, version 1, section %s): %s", plan.File, p.run.Section, how)
	if p.counts != "" {
		s += "; tests: " + p.counts
	}
	if len(p.events) > 0 {
		s += "; " + strings.Join(p.events, "; ")
	}
	if len(p.bugs) > 0 {
		s += "; bug reports saved: " + strings.Join(p.bugs, ", ")
	}
	return s + "."
}

// PlanOff: the person ignored the plan at Hello; the run goes on without it.
func (b *Backend) PlanOff() {
	b.R.mu.Lock()
	if b.R.plan != nil {
		b.R.plan.off = true
	}
	b.R.mu.Unlock()
}

// PlanStart is the console's plan run, at a screen: the file on the stick read again and
// checked, then this Mac's section is the plan in use. What the plan did before (a stop, its
// events) stays in the report's note. nil and the reasons when it can't start.
func (b *Backend) PlanStart(at string) (*ui.PlanFacts, []string) {
	r := b.R
	src, err := r.readPlan()
	switch {
	case err != nil:
		return nil, []string{"the run plan can't be read: " + err.Error()}
	case src == nil:
		return nil, []string{"no " + plan.File + " on the stick"}
	}
	p, probs := plan.Parse(src, planKnown)
	if p == nil {
		out := []string{plan.File + ": not usable, so not started"}
		for _, x := range probs {
			out = append(out, "  "+x.String())
		}
		return nil, out
	}
	configs := []string{r.Ident.Config}
	for _, c := range r.Ident.Candidates {
		configs = append(configs, c.Config)
	}
	run, ok := p.For(r.Ident.Identifier, configs...)
	if !ok {
		return nil, []string{plan.File + " has no section for this Mac (" + r.Ident.Identifier + "): not started"}
	}
	r.mu.Lock()
	rp := &runPlan{found: true, section: true, run: &run}
	if old := r.plan; old != nil {
		rp.counts, rp.events, rp.bugs = old.counts, old.events, old.bugs
	}
	rp.events = append(rp.events, "started from the console at "+at)
	r.plan = rp
	r.mu.Unlock()
	return r.planFacts(), nil
}

// PlanStop is the console's plan stop, at a screen: the run is the person's from there.
func (b *Backend) PlanStop(at string) {
	r := b.R
	r.mu.Lock()
	if r.plan != nil {
		r.plan.stopped = true
		r.plan.events = append(r.plan.events, "stopped from the console at "+at)
	}
	r.mu.Unlock()
}

// PlanNote records what the plan did, for the report: kind "counts" sets the tests' counts
// (by mode), "config" sets the configuration (the plan's, or the tool's best match: not the
// tester's pick, as Pick would record), any other kind adds an event (English, never the
// network's name).
func (b *Backend) PlanNote(kind, text string) {
	r := b.R
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.plan == nil {
		return
	}
	switch kind {
	case "counts":
		r.plan.counts = text
	case "config":
		r.config = text
	default:
		r.plan.events = append(r.plan.events, text)
	}
}

// PlanSerial writes a plan run's last line to the serial port (as --auto does), when there is one.
func (b *Backend) PlanSerial(line string) {
	if b.R.Fixture() {
		return
	}
	if f, err := os.OpenFile(Serial, os.O_WRONLY|syscall.O_NOCTTY|syscall.O_NONBLOCK, 0); err == nil {
		fmt.Fprint(f, "OMABOOT-PLAN: "+line+"\r\n")
		f.Close()
	}
}

// planMask hides the plan's Wi-Fi network's name in text bound for a bug report.
func (r *Run) planMask(s string) string {
	r.mu.Lock()
	var p *plan.Run
	if r.plan != nil {
		p = r.plan.run // started, waiting or stopped: its network's name is the file's, never a report's
	}
	r.mu.Unlock()
	if p == nil || p.Network.SSID == "" {
		return s
	}
	return strings.ReplaceAll(s, p.Network.SSID, "(the plan's network)")
}

// planBug records a bug report saved while a plan ran.
func (r *Run) planBug(path string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.plan != nil && r.plan.run != nil && !r.plan.off && !r.plan.waiting {
		r.plan.bugs = append(r.plan.bugs, filepath.Base(path))
	}
}

// PlanText is the console's plan command: the plan in use (never its password), or with
// check, the file on the stick read again and checked.
func (b *Backend) PlanText(check bool) ([]string, error) {
	r := b.R
	if check {
		src, err := r.readPlan()
		switch {
		case err != nil:
			return nil, err
		case src == nil:
			return []string{"no " + plan.File + " on the stick"}, nil
		}
		p, probs := plan.Parse(src, planKnown)
		if p == nil {
			out := []string{plan.File + ": not usable"}
			for _, x := range probs {
				out = append(out, "  "+x.String())
			}
			return out, nil
		}
		out := []string{plan.File + ": OK"}
		if run, ok := p.For(r.Ident.Identifier, r.Ident.Config); ok {
			out = append(out, "  this Mac ("+r.Ident.Identifier+"): section "+run.Section)
		} else {
			out = append(out, "  no section for this Mac ("+r.Ident.Identifier+"): it would ignore the plan")
		}
		return out, nil
	}
	r.mu.Lock()
	p := r.plan
	r.mu.Unlock()
	switch {
	case p == nil:
		return []string{"no run plan: no " + plan.File + " on the stick"}, nil
	case p.problems != nil:
		return append([]string{plan.File + " found, not used:"}, p.problems...), nil
	case !p.section:
		return []string{plan.File + " found, with no section for " + r.Ident.Identifier + ": not used"}, nil
	}
	run := p.run
	state := "in use"
	switch {
	case p.off:
		state = "ignored at Hello (plan run starts it)"
	case p.waiting:
		state = "waiting for plan run (start: console)"
	case p.stopped:
		state = "stopped from the console (plan run starts it again)"
	}
	how := "attended (it has hands-on tests)"
	if run.Unattended() {
		how = "unattended"
	}
	out := []string{fmt.Sprintf("%s: section %s, %s, %s", plan.File, run.Section, state, how),
		"  configuration: " + or(run.Configuration, "the match"),
		"  network: " + run.Network.Text(),
		"  results: " + run.Results + ", tester: " + or(run.Tester, "none") + ", finish: " + run.Finish + ", start: " + run.Start,
		"  bug reports: " + or(strings.Join(keys(run.Bugreport), ", "), "none") + map[bool]string{true: ", sent to OmaBoot? too"}[run.BugSend],
		"  tests: default " + run.Default}
	ids := keysOf(run.Tests)
	for _, id := range ids {
		out = append(out, "    "+id+": "+run.Tests[id])
	}
	return out, nil
}

func or(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

func keys(m map[string]bool) []string {
	var out []string
	for k, v := range m {
		if v {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}

func keysOf(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
