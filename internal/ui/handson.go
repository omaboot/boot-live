package ui

import (
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/omaboot/boot-live/internal/boot"
	"github.com/omaboot/boot-live/internal/checks"
	g "github.com/omaboot/boot-live/internal/grid"
	"github.com/omaboot/boot-live/internal/handson"
	"github.com/omaboot/boot-live/internal/i18n"
	"github.com/omaboot/boot-live/internal/pace"
)

// ── Hands-on tests (LIVE-PLAN §21) ─────────────────────────────────────────────
//
// "Keep going" on Quick results opens "Boot needs you": what's coming, roughly how long, what to
// have at hand. Then one screen per test, in the registry's order (no plug-in first, then the
// plug-ins grouped, then sleep), all in one layout (owner-approved prototype v46, 2026-10-13):
// Boot and his line in the band with the test's animation, then what to do, what happens and
// the answers on the left, the output and the result on the right (handson.Screen says how).
//
// Once a test decides, its result shows and the test holds: 1 Continue goes to the next test,
// 2 Redo runs it again (a new result replaces this one); Backspace redoes it too. Backspace on a
// test that hasn't decided opens the one before, paused: nothing starts, its last result shows,
// Continue or Redo (owner, 2026-10-13; this replaces the beat before the next test and sleep
// and wake's own ▶ Continue). Esc asks, then ends the hands-on tests and goes to Finish with
// what's done. One report per run: the results join the quick pass's.

// hintAfter is how long a test waits with no key before Boot offers its hint (owner, 2026-10-13).
const hintAfter = 20.0

// outRows is the Output box's fixed height (prototype: 16, kept between 8 and what fits).
const outRows = 16

// attentionReady is when the "Boot needs you" list is up and its keys work.
const attentionReady = 2.4

// HandsOn is the hands-on tests as the screens run them.
type HandsOn struct {
	Setup   handson.Setup
	Cur     int                        // the test on screen: an index into Setup.Plan
	Run     *handson.Runner            // its run (nil before the first test opens, and while Paused)
	At      float64                    // the Test scene's time when it opened
	Out     *handson.Outcome           // its outcome, once decided
	OutAt   float64                    // the run's time it was decided
	Skipped bool                       // it was skipped
	Results map[string]handson.Outcome // by test ID: the latest outcome of each
	Sel     int                        // the answers' cursor (-1: none)
	// Paused: the test on screen was opened by Backspace from the one after it; nothing runs
	// (Run is nil), its last result shows, and the answers are Continue and Redo.
	Paused bool
	// KeyAt is the run's time of the last key on this test: Boot's hint counts from it.
	KeyAt float64
	// A test that sleeps waits for a copy of the report on the stick (LIVE-PLAN §21): Keeping
	// while it's saved, then where it went (Kept) or why it didn't (KeepErr; the test goes on).
	Keeping       bool
	Kept, KeepErr string

	stamps []stamped           // the Output lines on screen, by index: when each showed its text
	last   map[string]snapshot // by test ID: its screen when it last decided, for going back to it

	// Woke is called when a test that sleeps is left: the Mac came back (Backend.Woke).
	Woke func()
	woke bool // Woke was called for the run on screen

	// Again is, by test ID, the first run of a test measured again in the console, for its
	// results' evidence (owner, 2026-10-14); pending holds it (ID, note) until the new run decides.
	Again   map[string]string
	pending [2]string
	// retest is the test Resume opens first when the run was picked up after it hung (Sleep and
	// wake, offering light sleep; Restore), until it has.
	retest string
	// redoAsk: Redo was picked once on a result with a warning (Outcome.Warn); picked again, it
	// redoes the test.
	redoAsk bool
}

// stamped is an Output line's text and the run's time it first showed.
type stamped struct {
	key string
	at  float64
}

// snapshot is a decided test's screen as it was: what a paused test shows.
type snapshot struct {
	view   handson.Screen
	lines  []handson.Line
	stamps []stamped
	at     float64 // the run's time it decided
}

// NewHandsOn prepares the hands-on tests for a run.
func NewHandsOn(s handson.Setup) *HandsOn {
	return &HandsOn{Setup: s, Results: map[string]handson.Outcome{}, last: map[string]snapshot{}}
}

// Open opens test i at scene time at: a fresh run (the one on screen is stopped first).
func (h *HandsOn) Open(i int, at float64) {
	h.Stop()
	s := h.Setup.Plan[i]
	if h.pending[0] != s.ID { // a console rerun of another test, never run: forgotten
		h.pending = [2]string{}
	}
	h.Cur, h.At, h.Out, h.OutAt, h.Skipped, h.Sel, h.Paused, h.KeyAt = i, at, nil, 0, false, 0, false, 0
	h.Keeping, h.Kept, h.KeepErr, h.woke, h.redoAsk = false, "", "", false, false
	h.stamps = nil
	h.Run = handson.Begin(s, h.Setup.Env, h.Setup.IO(s))
	if i, ok := h.Run.Ctx.TakeSelect(); ok {
		h.Sel = i
	}
	h.stamp(0)
	h.take()
}

// OpenPaused opens test i paused (Backspace from the test after it): no run starts; its last
// result and output show until Continue or Redo.
func (h *HandsOn) OpenPaused(i int, at float64) {
	h.Stop()
	h.Run = nil
	h.Cur, h.At, h.Out, h.OutAt, h.Skipped, h.Sel, h.Paused, h.KeyAt = i, at, nil, 0, false, 0, true, 0
	h.Keeping, h.Kept, h.KeepErr, h.redoAsk = false, "", "", false
	h.stamps = nil
}

// Stop stops the run on screen, which puts back what it changed, and leaves it (a sound that
// outlasts its decision, the microphone's encore, stops here). Leaving a test that sleeps calls
// Woke once: the Mac is still here.
func (h *HandsOn) Stop() {
	if h.Run != nil {
		h.Run.Leave()
		if h.Run.Spec.Sleeps && h.Woke != nil && !h.woke {
			h.woke = true
			h.Woke()
		}
	}
}

// Step moves the run on to scene time t; it reports whether the test has just decided.
func (h *HandsOn) Step(t float64) bool {
	if h.Run == nil || h.Out != nil || h.Keeping {
		return false
	}
	h.Run.Step(t - h.At)
	if i, ok := h.Run.Ctx.TakeSelect(); ok { // a new round: the run's likeliest answer, or none
		h.Sel = i
	}
	h.stamp(t - h.At)
	return h.take()
}

// lines are the Output box's lines now: the copy saved before a sleeping test, then the run's.
func (h *HandsOn) lines(sc handson.Screen) []handson.Line {
	return append(keepLines(h), sc.Live...)
}

// stamp notes, for each Output line, the run's time rt when it first showed its current text.
func (h *HandsOn) stamp(rt float64) {
	if h.Run == nil {
		return
	}
	ls := h.lines(h.Run.View())
	for i, l := range ls {
		k := l.Mark + "\x00" + l.Side + "\x00" + l.Text
		if l.Mark == handson.Spin {
			k = "spin\x00" + l.Text
		}
		if i >= len(h.stamps) {
			h.stamps = append(h.stamps, stamped{k, rt})
		} else if h.stamps[i].key != k {
			h.stamps[i] = stamped{k, rt}
		}
	}
	h.stamps = h.stamps[:min(len(h.stamps), len(ls))]
}

// take records the run's outcome once there is one.
func (h *HandsOn) take() bool {
	if h.Out != nil || h.Run == nil {
		return false
	}
	o := h.Run.Outcome()
	if o == nil {
		return false
	}
	h.Out, h.OutAt = o, h.Run.Ctx.T
	h.Results[h.Run.Spec.ID] = *o
	if h.pending[0] == h.Run.Spec.ID { // measured again in the console: this run decides
		if h.Again == nil {
			h.Again = map[string]string{}
		}
		h.Again[h.pending[0]], h.pending = h.pending[1], [2]string{}
	}
	h.Sel = 0 // on Continue
	v := h.Run.View()
	ls := h.lines(v)
	v.Anim, v.Panel, v.Map, v.Side = nil, nil, nil, nil // a paused test shows no animation
	h.last[h.Run.Spec.ID] = snapshot{view: v, lines: ls, stamps: append([]stamped(nil), h.stamps...), at: h.OutAt}
	h.Run.Stop()                                       // decided: what it changed goes back now
	if h.Run.Spec.Sleeps && h.Woke != nil && !h.woke { // decided, and the Mac is here to say so
		h.woke = true
		h.Woke()
	}
	return true
}

// Skip is "Skip this one".
func (h *HandsOn) Skip() bool {
	if h.Run == nil || h.Out != nil {
		return false
	}
	h.Skipped = h.Run.Skip() // false: the run took it as an answer
	return h.take()
}

// Restore takes in an unfinished run's hands-on results (Resume): each test it decided, and the
// test that was running when its copy was saved as failed (handson.NoComeback). Resume then
// opens the test after it. Tests this version doesn't plan are left out.
func (h *HandsOn) Restore(r *Resume) {
	from := "boot-live " + r.Version + ", " + r.Saved.UTC().Format("2006-01-02 15:04") + " UTC"
	for _, s := range h.Setup.Plan {
		switch o, ok := r.Done[s.ID]; {
		case s.ID == r.Test:
			out := handson.NoComeback(h.Setup.Env, s, from)
			if ok { // a redo: the run before it, in the same boot (the iMac18,3's 0.8.8 run lost it)
				redone(&out, o)
			}
			h.Results[s.ID] = out
			if s.ID == "power.sleep-wake" && h.Setup.Env != nil { // it opens again, offering light sleep (0.7.5)
				if h.Setup.Env.Hung == nil {
					h.Setup.Env.Hung = map[string]handson.Outcome{}
				}
				h.Setup.Env.Hung[s.ID], h.retest = out, s.ID
			}
		case ok:
			h.Results[s.ID] = o
		}
	}
}

// redone puts the run before a redo that didn't come back in the fail's evidence.
func redone(out *handson.Outcome, before handson.Outcome) {
	for i, x := range out.Results {
		for _, b := range before.Results {
			if b.ID == x.ID {
				out.Results[i].Evidence += "; it was a redo: the run before it, in the same boot, was " + b.Status + ": " + b.Evidence
			}
		}
	}
}

// Resume is the test to start from: Sleep and wake again when the run was picked up after it
// hung (it offers light sleep), else the first without a result (the first, when all have one).
func (h *HandsOn) Resume() int {
	if id := h.retest; id != "" {
		h.retest = ""
		for i, s := range h.Setup.Plan {
			if s.ID == id {
				return i
			}
		}
	}
	for i, s := range h.Setup.Plan {
		if _, ok := h.Results[s.ID]; !ok {
			return i
		}
	}
	return 0
}

// All are the results so far, in the flow's order: one per criterion, where two tests decide one
// (the keyboard light's blinks and keys) joined by handson.Merge.
func (h *HandsOn) All() []checks.Result {
	if h == nil {
		return nil
	}
	var out []checks.Result
	for _, s := range h.Setup.Plan {
		if o, ok := h.Results[s.ID]; ok {
			for _, x := range o.Results {
				if note := h.Again[s.ID]; note != "" {
					x.Evidence = checks.AgainEvidence(x.Evidence, note)
				}
				out = append(out, x)
			}
		}
	}
	return handson.Merge(out)
}

// Checks are the results as the screens show them.
func (h *HandsOn) Checks() []Check {
	var out []Check
	for _, x := range h.All() {
		out = append(out, CheckOf(x))
	}
	return out
}

// Pick is a scripted answer for Play: at the run's time At, the Action-th answer (-1: skip), or
// with Code, those digits typed into the code field (then Enter unless Open).
type Pick struct {
	At     float64
	Action int
	Code   string
	Open   bool // Code typed without Enter: the field as it fills
}

// Play opens test i at scene time 0 and steps it to until (scene seconds), at the frame rate,
// with the person's answers given: tools/screens draws a test's screen at a chosen moment.
func (h *HandsOn) Play(i int, until float64, picks ...Pick) {
	h.Open(i, 0)
	next := 0
	for t := 0.0; t <= until+1e-9; t += 1.0 / FPS {
		for next < len(picks) && picks[next].At <= t {
			if p := picks[next]; p.Code != "" {
				if h.Run != nil && h.Out == nil {
					for _, d := range p.Code {
						h.Run.CodeKey(string(d))
					}
					if !p.Open {
						h.Run.CodeKey("enter")
					}
					h.take()
				}
			} else if p.Action < 0 {
				h.Skip()
			} else if h.Run != nil && h.Out == nil {
				h.Run.Act(p.Action)
				h.take()
			}
			h.KeyAt = t
			next++
		}
		h.Step(t)
	}
}

// CheckOf is a result as the screens show it.
func CheckOf(x checks.Result) Check {
	return Check{ID: x.ID, Label: x.Label, Detail: x.Detail, Fix: x.Fix, Effect: x.Effect, Params: x.Params,
		Begin: x.Begin, Finish: x.Finish, Status: x.Pace(), Say: x.Say, Evidence: x.Evidence, Method: x.Method, Reason: x.Reason}
}

// testActions are the run's answers, each with its key (its own, else its place: 1, 2, 3 …),
// and "Skip this one" (s).
func testActions(h *HandsOn, sc handson.Screen) []answer {
	var a []answer
	for i, x := range sc.Actions {
		k := x.Key
		if k == "" {
			k = strconv.Itoa(i + 1)
		}
		a = append(a, answer{Key: k, Label: x.Label, Does: x.Does, Off: x.Off, Gap: x.Gap, Bars: x.Bars})
	}
	skip := i18n.T("it goes in the report as not tested")
	if g := h.Run.Spec.Gear; g != "" {
		skip = i18n.T("don't have %s? it goes in the report as not tested", i18n.S(g))
	}
	if sc.SkipDoes != "" {
		skip = sc.SkipDoes
	}
	return append(a, answer{Key: "s", Label: i18n.T("Skip this one"), Does: skip})
}

// heldActions are a decided (or paused) test's answers: on to the next test, or run it again.
func heldActions(h *HandsOn) []answer {
	next := i18n.T("that was the last one: on to Finish")
	if h.Cur+1 < len(h.Setup.Plan) {
		next = i18n.T("next: %s", i18n.S(h.Setup.Plan[h.Cur+1].Title))
	}
	redo := answer{Key: "2", Label: i18n.T("Redo this test"), Does: i18n.T("a new result replaces this one")}
	if h.redoAsk {
		redo = answer{Key: "2", Label: i18n.T("Redo anyway"), Does: i18n.T("the result so far stays on the stick if it doesn't come back")}
	}
	return []answer{{Key: "1", Label: i18n.T("Continue"), Does: next}, redo}
}

// heldWarn is the held result's warning before a redo (Outcome.Warn), or "".
func heldWarn(h *HandsOn) string {
	if h.Out != nil {
		return h.Out.Warn
	}
	if h.Paused && h.Cur < len(h.Setup.Plan) {
		return h.Results[h.Setup.Plan[h.Cur].ID].Warn
	}
	return ""
}

// ── Boot needs you ─────────────────────────────────────────────────────────────

func attentionActions(h *HandsOn) []Action {
	return []Action{
		{Label: i18n.T("Start now"), Does: i18n.N("about %d minute, with you at the Mac", "about %d minutes, with you at the Mac", h.Setup.Minutes(), h.Setup.Minutes())},
		{Label: i18n.T("Not now"), Does: i18n.T("back to Quick results; Keep going brings you here again")},
	}
}

func drawAttention(gr *g.Grid, o *Options, st *State, f *Facts) Box {
	t, h := st.T, st.HO
	hot := (t < 3 || t > 9 && t < 11) && int(t*3)%2 == 0
	b := frame(gr, o, st, f.Mac, frameSpec{title: i18n.T("Hands-on tests"), step: stepHandsOn, ho: st.HO, right: netRight(f, st), mac: true, hot: hot,
		keys: Keys{Esc: true, EscEnd: true, Back: st.Overlay != LogOverlay, Log: true, EscClose: st.Overlay == LogOverlay}})
	l := layout(b, o.Plain)
	mins := h.Setup.Minutes()
	gs := gear(h)
	if o.Plain {
		gr.Put(l.FieldX, l.BandTop, i18n.T("Hands-on tests"), g.WHT)
		gr.Put(l.FieldX, l.BandTop+1, i18n.N("About %d minute with you at the Mac.", "About %d minutes with you at the Mac.", mins, mins), g.DIM)
	} else {
		ex := boot.Pleased
		if t < 1.5 {
			ex = boot.Work
		}
		boot.Draw(gr, l.BootX, l.BandTop+1, ex, life(st, t))
		// the band split like the tests' (owner, 2026-10-13): the heading and Boot's box in the
		// 1/3, what to have at hand in the 2/3
		sw, ax, aw := bandSplit(l)
		gr.Put(l.FieldX, l.BandTop+1, cut(i18n.T("Boot needs you."), sw), g.WHT)
		sayBox(gr, &st.Said, l.FieldX, l.BandTop+3, sw, l.RuleY-(l.BandTop+3)-2, t, []line{
			{0.4, i18n.N("your undivided attention is required for the next ~%d minute.", "your undivided attention is required for the next ~%d minutes.", mins, mins)},
			{2.2, i18n.T("sit close. this part needs hands, eyes and ears.")}}, -1)
		if t > 1.2 && st.Overlay != LogOverlay {
			drawGear(gr, ax, l.BandTop+1, aw, l.RuleY-(l.BandTop+1), gs)
		}
	}
	if st.Overlay == LogOverlay {
		drawLog(gr, b, l, st, f.All(), f.Startup)
		return b
	}
	divider(gr, b, l.RuleY, "", 0, g.BVIO)
	x, y0, end := b.X+1, l.ContentY, b.X+b.W
	if t <= 0.8 {
		return b
	}
	if len(h.Setup.Plan) == 0 {
		gr.Put(x, y0, i18n.T("No hands-on tests for this Mac in this version."), g.GRY)
		return b
	}
	gr.Put(x, y0, i18n.T("Coming up"), g.GRY)
	gr.Put(x+12, y0, i18n.N("%d test", "%d tests", len(h.Setup.Plan), len(h.Setup.Plan)), g.DIM)
	// the tests, numbered in plan order, fill the rows above the actions, in columns of short
	// titles when their briefs don't fit one column (a 2016 MacBook Pro gets about 20 tests);
	// with no band (plain), the gear goes under them, a group a row
	var glines []string
	if o.Plain {
		for _, grp := range gs {
			for i, gl := range wrap(grp.Title+": "+strings.Join(grp.Items, ", "), end-(x+4)) {
				if i > 0 {
					gl = "  " + gl // under its group's title
				}
				glines = append(glines, gl)
			}
		}
	}
	free := l.Bottom - (y0 + 2) - 4
	if len(glines) > 0 {
		free -= len(glines) + 1
	}
	plan := h.Setup.Plan
	nw := len(strconv.Itoa(len(plan))) + 1 // "24."
	cols, colW := 1, end-x-4
	if len(plan) > free && free > 0 {
		cols = (len(plan) + free - 1) / free
		colW = (end - x - 4) / cols
	}
	rows := min(len(plan), max(0, free))
	if cols > 1 {
		rows = (len(plan) + cols - 1) / cols
	}
	// each test's criterion IDs, dim, after its time (owner, 0.6.1 review): the widest sets the
	// column, and it gives way (cut) before the titles do
	ids := make([]string, len(plan))
	idW, titleW := 0, 0
	for i, s := range plan {
		ids[i] = strings.Join(s.IDs(), ", ")
		idW = max(idW, width(ids[i]))
		titleW = max(titleW, width(i18n.S(s.Title)))
	}
	for i, s := range plan {
		if t < 1.0+float64(i)*0.06 || i >= rows*cols {
			break
		}
		y, cx := y0+2+i%rows, x+2+(i/rows)*colW
		m, mc := "", g.DIM // no bullet for a test still to come (owner, 0.6.1 review); a done one shows its mark
		if out, ok := h.Results[s.ID]; ok && len(out.Results) > 0 {
			m, mc, _ = mark(out.Results[0].Pace())
		}
		num := strconv.Itoa(i+1) + "."
		gr.Put(cx+nw-width(num), y, num, g.DIM)
		cx += nw + 1
		gr.Put(cx, y, m, mc)
		if cols == 1 {
			bw := min(60, end-cx-16-idW-2)
			gr.Put(cx+2, y, cut(padTo(i18n.S(s.Brief), bw), bw), g.GRY)
			gr.Put(cx+bw+4, y, cut(about(s.Expect.Seconds()), 14), g.DIM)
			gr.Put(cx+bw+4+15, y, cut(ids[i], end-(cx+bw+4+15)), g.DIM)
		} else {
			// title, time (6), criterion IDs: the titles stay whole when they can; the IDs give
			// way, cut with "…" (a narrow console: 120 columns)
			room := colW - (nw + 1) - 2 - 7 - 3 // a gap before the next column
			tw := min(titleW, room)
			iw := max(0, room-tw)
			gr.Put(cx+2, y, cut(padTo(i18n.S(s.Title), tw), tw), g.GRY)
			gr.Put(cx+2+tw+1, y, cut(short(s.Expect.Seconds()), 6), g.DIM)
			if id := ids[i]; iw > 1 {
				if width(id) > iw {
					id = cut(id, iw-1) + "…"
				}
				gr.Put(cx+2+tw+1+7, y, id, g.DIM)
			}
		}
	}
	ay := y0 + 2 + rows + 1
	if len(glines) > 0 && t > 1.8 {
		for _, gl := range glines {
			gr.Put(x+2, ay, cut(gl, end-(x+2)), g.GRY)
			ay++
		}
		ay++
	}
	if t > attentionReady {
		actions(gr, x, ay, attentionActions(h), st.AttSel, true, 0, end)
	}
	return b
}

// short is about without the "about": "2 min", "30 s", for the narrow columns.
func short(sec float64) string {
	if sec >= 60 {
		return i18n.T("%d min", int(sec/60+0.5))
	}
	return i18n.T("%d s", int(sec))
}

// about is a test's rough length: "about 2 min", "30 s".
func about(sec float64) string {
	if sec >= 90 {
		return i18n.T("about %d min", int(sec/60+0.5))
	}
	if sec >= 60 {
		return i18n.T("1 min")
	}
	return i18n.T("%d s", int(sec))
}

// ── A test's screen ────────────────────────────────────────────────────────────

// testView is what a test's screen draws: the run's screen now, or a paused test's as it last
// decided.
type testView struct {
	sc       handson.Screen
	lines    []handson.Line
	stamps   []stamped
	t        float64 // the run's time
	running  bool    // undecided and live: the newest output line bright, the cursor blinking
	busy     bool    // hands off: no answers
	result   []Check
	acts     []answer
	sel      int
	say      string
	sayAt    float64
	face     boot.Expr
	hint     bool
	outTitle string
	dimOut   bool
	step     int // the current step (-1: none; past the last: all done)
	full     bool
	restAt   float64 // the stamp of lines past the stamped ones (the results' lines)
}

func viewOf(h *HandsOn, sceneT float64) testView {
	spec := h.Setup.Plan[h.Cur]
	v := testView{t: sceneT - h.At, sel: h.Sel}
	v.restAt = v.t
	if h.Paused {
		snap, ok := h.last[spec.ID]
		if ok {
			v.sc = snap.view
		} else {
			v.sc = handson.Screen{Do: i18n.S(spec.Brief)}
		}
		v.lines, v.stamps, v.dimOut = snap.lines, snap.stamps, true
		v.say, v.sayAt, v.face = i18n.T("not run yet. run it, or go on?"), 0.3, boot.Idle
		if out, ok := h.Results[spec.ID]; ok {
			for _, x := range out.Results {
				v.result = append(v.result, CheckOf(x))
			}
			v.say = i18n.T("this one's done. run it again?")
			if h.redoAsk {
				v.say, v.sayAt, v.face = heldWarn(h)+" "+i18n.T("Redo anyway, or Continue."), v.t, boot.Concerned
			}
			v.lines, v.restAt = append(append([]handson.Line(nil), v.lines...), resultLines(v.result)...), snap.at
		}
		v.acts, v.step = heldActions(h), -1
		v.outTitle = i18n.T("%s, last run", outTitle(v.sc))
		return v
	}
	v.sc = h.Run.View()
	v.lines, v.stamps = h.lines(v.sc), h.stamps
	v.full = v.sc.Full()
	v.outTitle = outTitle(v.sc)
	v.say, v.sayAt, v.face, v.step = v.sc.Say, v.sc.SayAt, v.sc.Face, v.sc.Step
	if h.Out != nil {
		v.say, v.sayAt, v.face = h.Out.Say, h.OutAt, outcomeFace(h)
		if h.redoAsk {
			v.say, v.face = h.Out.Warn+" "+i18n.T("Redo anyway, or Continue."), boot.Concerned
		}
		for _, x := range h.Out.Results {
			v.result = append(v.result, CheckOf(x))
		}
		v.lines, v.restAt = append(v.lines, resultLines(v.result)...), h.OutAt
		v.acts, v.step = heldActions(h), 1<<20
		return v
	}
	v.running = true
	v.busy = v.sc.Busy || h.Keeping
	v.acts = testActions(h, v.sc)
	if v.sc.Code != nil {
		v.sel = -1 // the keys type the code: the answers take their own keys only (s)
	}
	if v.sc.Hint != "" && !v.busy && !h.Run.TakesKeys() && v.t-h.KeyAt >= hintAfter {
		v.say, v.sayAt, v.hint, v.face = v.sc.Hint, h.KeyAt+hintAfter, true, boot.Hmm
	}
	return v
}

// resultLines are a decision's results as Output lines, when it decided more than one
// criterion (the result box has room for one): mark, label, word, detail.
func resultLines(cs []Check) []handson.Line {
	if len(cs) < 2 {
		return nil
	}
	var out []handson.Line
	for _, c := range cs {
		m, col, word := mark(c.Status)
		out = append(out, handson.Line{Mark: m, Color: col, Text: i18n.S(c.Label) + ": " + word + "  ·  " + i18n.S(c.Detail), Fixed: c.Fix != ""})
	}
	return out
}

// outTitle is the Output box's title: the run's, else its older Heading, else "What I see".
func outTitle(sc handson.Screen) string {
	switch {
	case sc.OutTitle != "":
		return sc.OutTitle
	case sc.Heading != "":
		return sc.Heading
	}
	return i18n.T("What I see")
}

func drawTest(gr *g.Grid, o *Options, st *State, f *Facts) Box {
	h := st.HO
	spec := h.Setup.Plan[h.Cur]
	v := viewOf(h, st.T)
	// the hint keys grey out while the test is busy or holds the keyboard (its text says how out)
	held := v.busy || v.running && h.Run.TakesKeys()
	b := frame(gr, o, st, f.Mac, frameSpec{title: i18n.S(spec.Title), step: stepHandsOn, ho: st.HO, right: netRight(f, st), mac: true,
		keys: Keys{Esc: !held, EscEnd: true, Back: !held && st.Overlay != LogOverlay, Log: !held, EscClose: st.Overlay == LogOverlay}})
	l := layout(b, o.Plain)
	x := b.X + 1
	lw := (b.W - 4) * 46 / 100
	rx := x + lw + 4
	rw := b.X + b.W - rx - 1
	sw, ax, aw := bandSplit(l)
	if v.full {
		ax, aw = l.FieldX, l.FieldW-1
	}
	face := v.face
	if face == "" {
		face = boot.Idle
	}
	inBand := false      // the animation (or the panel) is in the band
	panelInBand := false // the panel is the band's animation
	if o.Plain {
		gr.Put(l.FieldX, l.BandTop, cut(currentStep(v), l.FieldW), g.WHT)
	} else {
		boot.Draw(gr, l.BootX, l.BandTop+1, face, life(st, st.T))
		old := gr.Clip(ax, l.FxTop+1, ax+aw, l.RuleY)
		switch {
		case v.sc.Map != nil:
			drawPortMap(gr, ringed(h, v.sc.Map), ax, l.BandTop, aw, l.BandH, v.t)
			inBand = true
		case v.sc.Anim != nil:
			v.sc.Anim(gr, ax, l.BandTop, aw, l.BandH, v.t)
			inBand = true
		case v.sc.Panel != nil && v.sc.PanelH > 0 && v.sc.PanelH <= l.BandH:
			v.sc.Panel(gr, ax, l.BandTop+(l.BandH-v.sc.PanelH)/2, aw, v.sc.PanelH, v.t)
			inBand, panelInBand = true, true
		}
		gr.Restore(old)
		if v.full { // under the band, at the top of the left column, ▲ up at Boot; always there, so nothing below moves
			speech(gr, &st.Said, x, l.ContentY, lw, sayRowsOf(v.sc), 0, v.say, v.t-v.sayAt, v.hint, l.BootX+8)
		} else if v.say != "" || st.Said.Text != "" {
			speech(gr, &st.Said, l.FieldX, l.BandTop+1, sw, 0, speechLines(l), v.say, v.t-v.sayAt, v.hint, -1)
		}
	}
	if st.Overlay == LogOverlay {
		drawLog(gr, b, l, st, f.All(), f.Startup)
		return b
	}
	label := strings.Join(handson.Decidable(h.Setup.Env, spec), " · ") + "  ·  " +
		i18n.T("test %d of %d", h.Cur+1, len(h.Setup.Plan))
	divider(gr, b, l.RuleY, label, b.X, g.BVIO)

	// the left column: what to do, what happens, the code field, the answers
	old := gr.Clip(x, l.ContentY, x+lw+1, l.Bottom+1)
	y := l.ContentY
	if v.full && !o.Plain {
		y += sayRowsOf(v.sc) + 3
	}
	if q := v.sc.Question; q != nil && v.running && !v.busy {
		drawQuestion(gr, x, y, lw, q, v.acts, v.sel)
		gr.Restore(old)
		drawTestOutput(gr, rx, rw, l, v)
		return b
	}
	y = drawSteps(gr, x, y, lw, v)
	happens := v.sc.Happens
	if happens == "" && len(v.sc.More) > 0 {
		happens = strings.Join(v.sc.More, " ")
	}
	if happens != "" {
		gr.Put(x, y, i18n.T("What happens"), g.GRY)
		y++
		for _, s := range wrap(happens, lw) {
			gr.Put(x, y, s, g.DIM)
			y++
		}
		y++
	}
	if !h.Paused { // a picture too tall for the band, no band (plain), or a panel beside the animation
		switch {
		case !inBand && o.Plain && v.sc.Map != nil:
			y += drawPortMap(gr, ringed(h, v.sc.Map), x, y, lw, max(0, l.Bottom-y-4), v.t) + 1
		case !inBand && o.Plain && v.sc.Anim != nil:
			ah := min(l.BandH, max(0, l.Bottom-y-4))
			v.sc.Anim(gr, x, y, lw, ah, v.t)
			y += ah + 1
		case v.sc.Panel != nil && v.sc.PanelH > 0 && !panelInBand:
			ph := min(v.sc.PanelH, max(0, l.Bottom-y-3))
			v.sc.Panel(gr, x, y, lw, ph, v.t)
			y += ph + 1
		}
	}
	if v.sc.Code != nil && v.running {
		y = codeField(gr, x, y, v.sc.Code, v.t) + 1
	}
	if v.busy {
		for _, s := range wrap(i18n.T("Hands off for now: keys wait until this is done."), lw) {
			gr.Put(x, y, s, g.DIM)
			y++
		}
	} else {
		if v.sc.Ask != "" && v.running {
			for _, s := range wrap(v.sc.Ask, lw) {
				gr.Put(x, y, s, g.GRY)
				y++
			}
		}
		y = answers(gr, x, y, v.acts, v.sel, lw)
		if v.sc.Note != "" && v.running {
			y++
			for _, s := range wrap(v.sc.Note, lw) {
				gr.Put(x, y, s, g.DIM)
				y++
			}
		}
	}
	gr.Restore(old)
	drawTestOutput(gr, rx, rw, l, v)
	return b
}

// drawTestOutput is the right column: the output in a dim terminal box, fixed height, the result
// under it.
func drawTestOutput(gr *g.Grid, rx, rw int, l Layout, v testView) {
	by := l.ContentY
	bh := max(3, min(outRows, l.Bottom-by-2))
	if v.sc.Side != nil && v.sc.SideH > 0 && bh-v.sc.SideH-1 >= 6 { // the picture over What I see, the Result box where it was
		v.sc.Side(gr, rx, by, rw, v.sc.SideH, v.t)
		by, bh = by+v.sc.SideH+1, bh-v.sc.SideH-1
	}
	box(gr, rx, by, rw, bh, g.DIM, v.outTitle, g.GRY)
	old := gr.Clip(rx+1, by+1, rx+rw-1, by+bh-1)
	drawOutput(gr, rx, by, rw, bh, v)
	gr.Restore(old)
	waiting := ""
	if v.running {
		waiting = v.sc.Waiting
	}
	resultBox(gr, rx, by+bh, rw, v.result, waiting)
}

// drawQuestion is a question that isn't part of the test, in a violet box of its own in place
// of "What to do": its text white, its note dim, its answers (all but the last of acts) inside;
// "Skip this one" (the last) under the box, since it skips the whole test (owner, 2026-10-14).
func drawQuestion(gr *g.Grid, x, y, w int, q *handson.Question, acts []answer, sel int) {
	text, note := wrap(q.Text, w-4), wrap(q.Note, w-4)
	own := acts[:len(acts)-1]
	h := 2 + len(text) + 1 + len(own)
	if len(note) > 0 {
		h += len(note) + 1
	}
	box(gr, x, y, w, h, g.BVIO, q.Title, g.BVIO)
	yy := y + 1
	for _, s := range text {
		gr.Put(x+2, yy, s, g.WHT)
		yy++
	}
	if len(note) > 0 {
		yy++
		for _, s := range note {
			gr.Put(x+2, yy, s, g.DIM)
			yy++
		}
	}
	yy++
	answers(gr, x+2, yy, own, sel, w-4)
	answers(gr, x, y+h+1, acts[len(acts)-1:], sel-len(own), w)
}

// sayRowsOf is the text rows of Boot's box under the band: the test's, else sayRows.
func sayRowsOf(sc handson.Screen) int {
	if sc.SayRows > 0 {
		return sc.SayRows
	}
	return sayRows
}

// currentStep is the step to do now, for the plain layout's headline.
func currentStep(v testView) string {
	steps := stepsOf(v)
	if v.step >= 0 && v.step < len(steps) {
		return steps[v.step]
	}
	return ""
}

func stepsOf(v testView) []string {
	if len(v.sc.Steps) > 0 {
		return v.sc.Steps
	}
	if v.sc.Do != "" {
		return []string{v.sc.Do}
	}
	return nil
}

// drawSteps is "What to do": numbered steps, a done one with a green ✓, the current one's
// number violet and its text white, the ones to come grey (owner, 2026-10-13). It returns the
// row after them and a blank row.
func drawSteps(gr *g.Grid, x, y, w int, v testView) int {
	steps := stepsOf(v)
	if len(steps) == 0 {
		return y
	}
	gr.Put(x, y, i18n.T("What to do"), g.WHT)
	y += 2
	for i, s := range steps {
		done, cur := i < v.step, i == v.step
		for k, ln := range wrap(s, w-4) {
			if k == 0 {
				switch {
				case done:
					gr.Put(x, y, "✓", g.BGRN)
				case cur:
					gr.Put(x, y, strconv.Itoa(i+1), g.BVIO)
				default:
					gr.Put(x, y, strconv.Itoa(i+1), g.DIM)
				}
			}
			tc := g.GRY
			switch {
			case cur:
				tc = g.WHT
			case done:
				tc = g.DIM
			}
			gr.Put(x+3, y, ln, tc)
			y++
		}
	}
	return y + 1
}

// ringed is the port map with the ring on the port the cursor names, while the stick question
// is up.
func ringed(h *HandsOn, pm *handson.PortMap) *handson.PortMap {
	if len(pm.Choices) > 0 && h.Sel >= 0 && h.Sel < len(pm.Choices) && h.Out == nil && !h.Paused {
		cp := *pm
		cp.Picked = pm.Choices[h.Sel]
		return &cp
	}
	return pm
}

// drawOutput fills the Output box (rx, by, rw × bh): each line with the m:ss it first showed,
// its mark, a FIX tag when a fix is in use, a port's line as "left side → [icon] …"; a long line
// wraps under itself without a stamp. The newest line is bright while the test runs, with a
// blinking cursor under the last; the newest lines stay in view.
func drawOutput(gr *g.Grid, rx, by, rw, bh int, v testView) {
	type seg struct {
		x      int
		text   string
		fg, bg g.Color
	}
	type row struct {
		stamp string
		segs  []seg
	}
	end := rx + rw - 2
	newest, at := -1, -1.0
	for i := range v.lines {
		if a := lineAt(v, i); a >= at {
			newest, at = i, a
		}
	}
	sw := 0 // a port line's side, padded so the arrows line up
	for _, ln := range v.lines {
		sw = max(sw, width(ln.Side))
	}
	var rows []row
	for i, ln := range v.lines {
		tc := g.GRY
		switch {
		case ln.Dim || v.dimOut:
			tc = g.DIM
		case v.running && i == newest:
			tc = g.WHT
		}
		mc := ln.Color
		if v.dimOut {
			mc = g.DIM
		}
		r := row{stamp: stamp(lineAt(v, i))}
		tx := rx + 8
		if ln.Side != "" { // "left side → [icon] USB 3 · …", the icon in the port's state colour
			r.segs = append(r.segs, seg{tx, ln.Side, g.GRY, g.K})
			ax := tx + sw + 1
			r.segs = append(r.segs, seg{ax, "→", g.BVIO, g.K})
			tx = ax + 2
			if ic := portIcon(ln.Kind); ic != "" {
				r.segs = append(r.segs, seg{tx, ic, mc, g.K})
				tx += width(ic) + 1
			}
		} else {
			m := ln.Mark
			if m == handson.Spin {
				m = spinner(v.t)
				if !v.running {
					m = "·"
				}
			}
			r.segs = append(r.segs, seg{tx, m, mc, g.K})
			tx += 2
			if ln.Fixed {
				r.segs = append(r.segs, seg{tx, " FIX ", g.WHT, g.VIO})
				tx += 6
				if !v.dimOut {
					tc = g.WHT
				}
			}
		}
		parts := wrap(ln.Text, max(8, end-tx))
		if len(parts) == 0 {
			parts = []string{""}
		}
		for k, p := range parts {
			if k > 0 {
				r = row{}
			}
			r.segs = append(r.segs, seg{tx, p, tc, g.K})
			rows = append(rows, r)
		}
	}
	room := bh - 2
	if v.running {
		room-- // the cursor's row
	}
	if len(rows) > room {
		rows = rows[len(rows)-max(0, room):]
	}
	for i, r := range rows {
		y := by + 1 + i
		gr.Put(rx+2, y, r.stamp, g.DIM)
		for _, s := range r.segs {
			gr.PutBG(s.x, y, s.text, s.fg, s.bg)
		}
	}
	if v.running && blinkOn(v.t) {
		gr.Put(rx+8, by+1+len(rows), "█", g.GRY)
	}
}

// lineAt is when Output line i first showed its text (the run's time).
func lineAt(v testView, i int) float64 {
	if i < len(v.stamps) {
		return v.stamps[i].at
	}
	return v.restAt
}

// keepLines are the What I see lines about the copy saved before a test that sleeps.
func keepLines(h *HandsOn) []handson.Line {
	switch {
	case h.Keeping:
		return []handson.Line{handson.Wait(i18n.T("saving a copy of the report to external storage first, in case the Mac doesn't wake"))}
	case h.KeepErr != "":
		return []handson.Line{handson.Bad(i18n.T("couldn't save a copy first: %s", h.KeepErr))}
	case h.Kept != "":
		return []handson.Line{handson.Fact(i18n.T("a copy of the report is on external storage: %s", h.Kept))}
	}
	return nil
}

// outcomeFace is Boot's face for the test's outcome: pleased on a pass, hmm on partial,
// concerned on a fail, a shrug on a skip.
func outcomeFace(h *HandsOn) boot.Expr {
	worst := pace.Pass
	for _, x := range h.Out.Results {
		switch p := x.Pace(); {
		case p == pace.Fail:
			worst = pace.Fail
		case p == pace.Partial && worst != pace.Fail:
			worst = pace.Partial
		case p == pace.Skip && worst == pace.Pass:
			worst = pace.Skip
		}
	}
	switch worst {
	case pace.Fail:
		return boot.Concerned
	case pace.Partial:
		return boot.Hmm
	case pace.Skip:
		return boot.Shrug
	}
	return boot.Pleased
}

// ── The flow ───────────────────────────────────────────────────────────────────

// stopAll stops the run on screen (quitting, or leaving for Finish).
func (h *HandsOn) stopAll() {
	if h != nil {
		h.Stop()
	}
}

// prepHandsOn asks the backend once for this Mac's hands-on tests (Quick results shows how many).
func (m *Model) prepHandsOn() {
	if m.st.HO == nil {
		m.st.HO = NewHandsOn(m.be.HandsOn())
		m.st.HO.Woke = m.be.Woke
		if m.st.Resumed && m.f.Resume != nil { // picked up from an unfinished run's copy
			m.st.HO.Restore(m.f.Resume)
			m.recordHandsOn()
		}
	}
}

// keepGoing is Quick results' Keep going: "Boot needs you".
func (m *Model) keepGoing() {
	if m.st.Gone {
		m.st.Overlay, m.st.StickSel = StickOverlay, 0
		return
	}
	m.prepHandsOn()
	st := &m.st
	st.AttSel = 0
	m.seen[Attention] = false // its call comes again each time
	m.goTo(Attention)
}

// openTest opens hands-on test i: a fresh run.
func (m *Model) openTest(i int) {
	m.goTo(Test)
	h := m.st.HO
	h.Open(i, m.timeIn(Test))
	if h.Out != nil { // decided as it opened (no lid switch at all)
		m.recordHandsOn()
		return
	}
	if h.Run.Spec.Sleeps { // a Mac that doesn't wake would lose the run: a copy goes on the stick first
		h.Keeping = true
		k := KeepInfo{Test: h.Run.Spec.ID, Title: h.Run.Spec.Title, Done: map[string]handson.Outcome{}, Of: len(h.Setup.Plan)}
		for id, o := range h.Results { // a redo's own last result too: picked up, it goes in the fail's evidence (Restore)
			k.Done[id] = o
		}
		m.be.Keep(m.handle(), ConsentNotice(&m.o, m.f), k)
	}
}

// openPaused opens hands-on test i paused: nothing starts.
func (m *Model) openPaused(i int) {
	m.goTo(Test)
	m.st.HO.OpenPaused(i, m.timeIn(Test))
}

// testClock steps the test on screen. It reports whether the screen needs a full repaint (back
// from sleep).
func (m *Model) testClock() bool {
	h := m.st.HO
	t := m.timeIn(Test)
	if h.Step(t) {
		m.recordHandsOn()
	}
	repaint := h.Run.Ctx.TakeRedraw()
	if repaint {
		m.lastKey = time.Now()
	}
	return repaint
}

// recordHandsOn hands the results so far to the backend (the one report) and the screens.
func (m *Model) recordHandsOn() {
	if m.st.Gone {
		return
	}
	h := m.st.HO
	m.f.HandsOn = h.Checks()
	m.be.Record(h.All())
}

// nextTest opens the next test, or Finish after the last.
func (m *Model) nextTest() {
	h := m.st.HO
	if m.activePlan() != nil { // the plan's skipped tests aren't opened
		m.planNext(h.Cur + 1)
		return
	}
	if h.Cur+1 < len(h.Setup.Plan) {
		m.openTest(h.Cur + 1)
		return
	}
	m.endHandsOn()
}

// endHandsOn leaves the hands-on tests for Finish, with what's done.
func (m *Model) endHandsOn() {
	m.st.HO.stopAll()
	m.st.FinishSel = 0
	m.goTo(Finish)
}

func (m *Model) attentionKey(key string) {
	st := &m.st
	if key == "backspace" {
		m.goTo(Results)
		return
	}
	if st.T <= attentionReady || len(st.HO.Setup.Plan) == 0 || !listKey(key, attentionActions(st.HO), &st.AttSel) {
		return
	}
	if st.AttSel == 0 {
		m.openTest(st.HO.Resume())
		return
	}
	m.goTo(Results)
}

// answerKey moves the answers' cursor or picks one: ↑↓ move it (from none, onto the first),
// Enter picks the one under it, a number key or s picks the answer showing it. It returns the
// answer picked, or -1.
func answerKey(key string, acts []answer, sel *int) int {
	first := func() int {
		for i, a := range acts {
			if !a.Off {
				return i
			}
		}
		return -1
	}
	switch key {
	case "up", "k", "down", "j":
		d := 1
		if key == "up" || key == "k" {
			d = -1
		}
		if *sel < 0 || *sel >= len(acts) {
			*sel = first()
			return -1
		}
		for i := *sel + d; i >= 0 && i < len(acts); i += d {
			if !acts[i].Off {
				*sel = i
				break
			}
		}
	case "enter":
		if *sel >= 0 && *sel < len(acts) && !acts[*sel].Off {
			return *sel
		}
	default:
		k := strings.ToLower(key)
		for i, a := range acts {
			if a.Key == k && !a.Off {
				*sel = i
				return i
			}
		}
	}
	return -1
}

// testKey handles a test screen's keys. Undecided: its answers and Skip (keys, ↑↓ Enter), the
// code field's digits, Backspace to the test before it, paused. A test that takes the keyboard
// gets every key instead; a test going to sleep takes none. Decided or paused: heldKey.
func (m *Model) testKey(key string) tea.Cmd {
	h := m.st.HO
	if h.Paused || h.Out != nil {
		return m.heldKey(key)
	}
	if h.Run == nil {
		return nil
	}
	if h.Run.TakesKeys() {
		h.Run.Key(key)
		return nil
	}
	sc := h.Run.View()
	if sc.Busy || h.Keeping {
		return nil
	}
	h.KeyAt = m.timeIn(Test) - h.At
	if key == "esc" {
		return m.askQuit()
	}
	if h.Run.Codes() { // the digits, Backspace and Enter type the code; an answer only by its key (s)
		v := sc.Code.Value
		switch {
		case len(key) == 1 && key[0] >= '0' && key[0] <= '9', (key == "backspace" || key == "enter") && v != "":
			h.Run.CodeKey(key)
			if h.take() {
				m.recordHandsOn()
			}
			return nil
		case key == "enter", key == "up", key == "down", key == "k", key == "j":
			return nil
		}
	}
	if key == "backspace" {
		h.Stop()
		if h.Cur > 0 {
			m.openPaused(h.Cur - 1)
		} else {
			m.goTo(Attention)
		}
		return nil
	}
	acts := testActions(h, sc)
	i := answerKey(key, acts, &h.Sel)
	switch {
	case i < 0:
		return nil
	case i == len(acts)-1: // Skip this one
		if h.Skip() {
			m.recordHandsOn()
		}
	default:
		h.Run.Act(i)
		if h.take() {
			m.recordHandsOn()
		}
	}
	return nil
}

// heldKey handles a decided or paused test's keys: 1 Continue, 2 Redo; Backspace redoes a
// decided test and goes on back from a paused one.
func (m *Model) heldKey(key string) tea.Cmd {
	h := m.st.HO
	switch key {
	case "esc":
		return m.askQuit()
	case "backspace":
		if !h.Paused {
			if heldWarn(h) != "" && !h.redoAsk { // a redo, as 2 is: say why first
				h.redoAsk = true
				return nil
			}
			m.openTest(h.Cur)
			return nil
		}
		if h.Cur > 0 {
			m.openPaused(h.Cur - 1)
		} else {
			m.goTo(Attention)
		}
		return nil
	}
	switch answerKey(key, heldActions(h), &h.Sel) {
	case 0:
		m.nextTest()
	case 1:
		if heldWarn(h) != "" && !h.redoAsk { // say why first (the iMac18,3's 0.8.8 run: the second nap restarted it)
			h.redoAsk = true
			return nil
		}
		m.openTest(h.Cur)
	}
	return nil
}
