package ui

import (
	"math"
	"strings"

	"github.com/omaboot/boot-live/internal/boot"
	g "github.com/omaboot/boot-live/internal/grid"
	"github.com/omaboot/boot-live/internal/i18n"
	"github.com/omaboot/boot-live/internal/pace"
)

// ── Quick pass ─────────────────────────────────────────────────────────────────

// QuickLen is how long the quick pass screen plays before Quick results.
func QuickLen(p *pace.Player) float64 { return p.End() + quickHold }

// CaughtUp reports whether the playback has shown every result that has arrived by t: the GPU
// notice waits for that, so it never covers results still to be read.
func CaughtUp(p *pace.Player, t float64) bool {
	fr := p.At(t)
	return fr.Current < 0 && fr.Shown == len(p.Slots()) && t >= p.End()
}

func drawQuick(gr *g.Grid, o *Options, st *State, f *Facts, p *pace.Player) Box {
	t := st.T
	notice := st.GPU.On(t)
	b := frame(gr, o, st, f.Mac, frameSpec{title: i18n.T("Quick pass"), step: 3, ho: st.HO, right: netRight(f, st), mac: true,
		keys: Keys{Esc: true, Log: true, EscClose: st.Overlay == LogOverlay}})
	l := layout(b, o.Plain)
	fr := p.At(t)
	slots := p.Slots()
	// the check shown as running: the playback's current slot, else the check really running now
	// (its result not in yet), from its start: its band effect runs as long as it does
	var cur *Check
	switch {
	case fr.Current >= 0:
		c := f.Check(slots[fr.Current].ID)
		cur = &c
	case fr.Running && st.Run.Label != "":
		cur = &Check{ID: st.Run.ID, Label: st.Run.Label, Effect: st.Run.Effect, Params: st.Run.Params}
	}
	label := ""
	switch {
	case notice:
		label = st.GPU.ID + "  ·  " + i18n.T("next")
	case cur != nil:
		label = cur.ID
	}
	// Boot's line: the notice's while it is up, and after it until Boot has a line from after the
	// notice came up (the graphics check's cue)
	say := fr.Say
	if st.GPU.At >= 0 && t >= st.GPU.At && fr.Say.At < st.GPU.At || notice {
		say = pace.Line{At: st.GPU.At, Text: i18n.T("graphics next. read this first.")}
	}
	if o.Plain {
		gr.Put(l.FieldX, l.BandTop, i18n.T("Automatic checks: no questions, about 2 minutes."), g.WHT)
		gr.Put(l.FieldX, l.BandTop+1, i18n.T("Nothing is written to this Mac's disks."), g.DIM)
		divider(gr, b, l.RuleY, label, b.X, g.BVIO)
	} else {
		// the band shows what the playback shows as running, not the raw checks
		fx, ft := boot.Plasma, t
		var params boot.Params
		if cur != nil {
			fx, params, ft = cur.Effect, cur.Params, fr.Local
		}
		face := boot.Expr(fr.Face)
		if notice {
			fx = boot.Plasma // cleared below: the notice takes the band's field
			if face == boot.Work {
				face = boot.Idle // nothing runs while he waits for you
			}
		}
		// Boot's line in a box next to him (1/3 of the field), the effect or the GPU notice in the
		// other 2/3; the log below keeps the full width (owner, 2026-10-13, prototype v44)
		sw, ax, aw := bandSplit(l)
		fx.Draw(gr, params, ax, l.FxTop, aw, l.FxH, ft)
		if notice {
			gr.Fill(ax, l.FxTop, aw, l.FxH, ' ', g.GRY, g.K)
			drawNotice(gr, st, ax, l.BandTop, aw, l.BandH, t)
		}
		boot.Draw(gr, l.BootX, l.BandTop+1, face, life(st, t))
		divider(gr, b, l.RuleY, label, b.X, g.BVIO)
		speech(gr, &st.Said, l.FieldX, l.BandTop+1, sw, 0, speechLines(l), say.Text, t-say.At, false, -1)
	}
	if st.Overlay == LogOverlay {
		var shown []Check
		for _, s := range slots[:fr.Shown] {
			shown = append(shown, f.Check(s.ID))
		}
		drawLog(gr, b, l, st, shown, f.Startup)
		return b
	}
	// the log: mark · check · status right after the name · evidence; newest at the bottom,
	// the running check under it
	lx := b.X + 1
	rows := l.Bottom - 2 - l.ContentY
	shown := slots[:fr.Shown]
	running := cur != nil
	if !running && !p.Complete() && t >= pace.Default.Start {
		running = true // a check is still running but nothing is shown as running yet
	}
	keep := rows
	if running || notice {
		keep--
	}
	if len(shown) > keep {
		shown = shown[len(shown)-max(0, keep):]
	}
	y := l.ContentY
	if o.Plain && notice { // no band in plain mode: the notice takes the log's place
		drawNotice(gr, st, lx, y, min(b.W-2, 110), min(12, l.Bottom-2-y), t)
		shown, y = nil, y+min(12, l.Bottom-2-y)
	}
	for _, s := range shown {
		logRow(gr, lx, y, b.X+b.W, f.Check(s.ID))
		y++
	}
	switch {
	case notice:
		left := noticeLeft(st, t)
		gr.Put(lx, y, "·", g.BVIO)
		gr.Put(lx+2, y, cut(padTo(i18n.S(st.GPU.Label), 38), 38), g.GRY)
		w := i18n.T("waiting for you, or %d s", left)
		if st.GPU.End >= 0 {
			w = i18n.T("starting")
		}
		gr.Put(lx+42, y, w, g.DIM)
	case cur != nil:
		gr.Put(lx, y, spinner(t), g.BVIO)
		gr.Put(lx+2, y, i18n.S(cur.Label), g.WHT)
	case running:
		gr.Put(lx, y, spinner(t), g.BVIO)
		gr.Put(lx+2, y, i18n.T("checking…"), g.GRY)
	}
	scale := f.TimeScale
	if scale == 0 {
		scale = 1
	}
	left := int(math.Round((p.End() - t) * scale))
	if !p.Complete() {
		left = max(0, left) + int(math.Round(max(0, st.Remaining-(t-st.RemainingAt-noticeHeld(st, t)))))
	}
	var when string
	switch {
	case st.Paused:
		when = i18n.T("paused")
	case left > 60:
		when = i18n.T("~%d min left", int(math.Ceil(float64(left)/60)))
	case left > 0:
		when = i18n.T("~%d s left", left)
	case !p.Complete():
		when = i18n.T("almost done")
	default:
		when = i18n.T("done")
	}
	total := len(f.Checks)
	if !p.Complete() || total == 0 {
		total = p.Total()
	}
	progress(gr, lx, l.Bottom-1, b.W-2, fr.Progress, i18n.T("%d/%d", fr.Shown, total)+"  ·  "+when)
	return b
}

// noticeHeld is how long the GPU notice has held the pass since RemainingAt, by t: the time
// left stands still while it's up (owner, 0.8.8 runs: it counted down under the notice), and
// goes on once it's dismissed or its own countdown ends.
func noticeHeld(st *State, t float64) float64 {
	if st.GPU.At < 0 {
		return 0
	}
	end := t
	if st.GPU.End >= 0 {
		end = min(t, st.GPU.End)
	}
	return max(0, end-max(st.GPU.At, st.RemainingAt))
}

// noticeLeft is the countdown's whole seconds left (at least 1 while it runs).
func noticeLeft(st *State, t float64) int {
	return max(1, int(GPUWait)-int(math.Floor(t-st.GPU.At)))
}

// drawNotice is the GPU notice: an amber box in the band's field (Boot stays outside it), in
// Boot's voice, with a countdown bar and one action. w and h are the room it has.
func drawNotice(gr *g.Grid, st *State, bx, by, w, h int, t float64) {
	inner := w - 6
	text1 := wrap(i18n.T("Your screen may go dark for up to half a minute while I test the graphics. That's normal."), inner)
	text2 := wrap(i18n.T("Leave the Mac alone until it comes back: I carry on by myself."), inner)
	bh := min(h, 7+len(text1)+len(text2))
	gr.Put(bx, by, "┌"+strings.Repeat("─", max(0, w-2))+"┐", g.AMB)
	for y := by + 1; y < by+bh-1; y++ {
		gr.Put(bx, y, "│", g.AMB)
		gr.Put(bx+w-1, y, "│", g.AMB)
	}
	gr.Put(bx, by+bh-1, "└"+strings.Repeat("─", max(0, w-2))+"┘", g.AMB)
	nx, y := bx+3, by+1
	gr.Put(nx, y, i18n.T("Dark screen ahead?"), g.YEL)
	y++ // the text right under the title: the notice fits the band's 2/3 (owner, 2026-10-13)
	for _, s := range text1 {
		gr.Put(nx, y, s, g.GRY)
		y++
	}
	for _, s := range text2 {
		gr.Put(nx, y, s, g.DIM)
		y++
	}
	y++
	starting := st.GPU.End >= 0
	left := noticeLeft(st, t)
	bw := int(GPUWait) * 2
	for i := 0; i < bw; i++ {
		if !starting && i < left*2 {
			gr.SetBG(nx+i, y, '█', g.BVIO, g.K)
		} else {
			gr.SetBG(nx+i, y, '░', g.DIM, g.K)
		}
	}
	if starting {
		gr.Put(nx+bw+2, y, i18n.T("starting now…"), g.WHT)
	} else {
		gr.Put(nx+bw+2, y, i18n.T("starting in %d s", left), g.GRY)
	}
	if y+2 < by+bh-1 {
		actions(gr, nx, y+2, []Action{{Label: i18n.T("Start now"), Does: i18n.T("or wait: it starts by itself")}}, 0, !starting, 0, bx+w-2)
	}
}

// logRow is one finished check in the log table.
func logRow(gr *g.Grid, lx, y, end int, c Check) { logRowID(gr, lx, y, end, c, 0) }

// logRowID is logRow with the criterion's ID (network.ethernet) in a column idW wide after the
// name, as the full log shows it (owner, 0.9.0 dev); idW 0 leaves it out.
func logRowID(gr *g.Grid, lx, y, end int, c Check, idW int) {
	sx, sw := lx+42, 33
	if idW > 0 { // the status column only as wide as its longest word, so the evidence keeps room
		gr.Put(sx, y, cut(c.ID, idW), g.DIM)
		sx, sw = sx+idW+2, 26
	}
	ex := sx + sw
	m, col, word := mark(c.Status)
	gr.Put(lx, y, m, col)
	lc := g.GRY
	if c.Status == pace.Live {
		lc = g.DIM
	}
	gr.Put(lx+2, y, cut(padTo(i18n.S(c.Label), 38), 38), lc)
	wx := gr.Put(sx, y, word, col)
	if c.Fix != "" { // a FIX tag after the word, so a result that ran with a fix is plain to see
		fixTag(gr, wx+1, y)
	}
	e := gr.Put(ex, y, cut(i18n.S(c.Detail), end-ex), g.DIM)
	if c.Fix != "" {
		note := "  ·  " + i18n.T("with OmaBoot? %s fix", c.Fix)
		if e+width(note) <= end {
			gr.Put(e, y, note, g.GRY)
		}
	}
}

// fixTag draws " FIX ", white on violet (the console has 8 backgrounds: the dark violet), at x, y
// and returns the column after it.
func fixTag(gr *g.Grid, x, y int) int {
	for i, r := range " FIX " {
		gr.SetBG(x+i, y, r, g.WHT, g.VIO)
	}
	return x + 5
}
