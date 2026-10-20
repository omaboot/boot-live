package ui

import (
	"github.com/omaboot/boot-live/internal/checks"
	"strings"

	"github.com/omaboot/boot-live/internal/boot"
	g "github.com/omaboot/boot-live/internal/grid"
	"github.com/omaboot/boot-live/internal/i18n"
	"github.com/omaboot/boot-live/internal/icons"
	"github.com/omaboot/boot-live/internal/pace"
)

// ── Quick results, and the read-only report ───────────────────────────────────

// resultsActions: Keep going (the hands-on tests) is the default, then Finish and send, the one
// way to send or save, then "Quit without sending", which asks first. Keep
// going is greyed out when this Mac has no hands-on tests in this version.
func resultsActions(st *State) []Action {
	keep := Action{Label: i18n.T("Keep going"), Does: i18n.T("no hands-on tests for this Mac in this version"), Off: true}
	if h := st.HO; h != nil && len(h.Setup.Plan) > 0 {
		n, mins := len(h.Setup.Plan), h.Setup.Minutes()
		keep = Action{Label: i18n.T("Keep going"), Does: i18n.N("%d hands-on test, about %d min with you at the Mac", "%d hands-on tests, about %d min with you at the Mac", n, n, mins)}
		if len(h.Results) > 0 {
			keep.Does = i18n.N("the hands-on tests: %d of %d done", "the hands-on tests: %d of %d done", n, len(h.Results), n)
		}
	}
	return []Action{
		keep,
		{Label: i18n.T("Finish and send"), Does: i18n.T("send or save this run's report, then shut down or restart")},
		{Label: i18n.T("Quit without sending"), Does: i18n.T("leave; nothing is sent or saved")},
	}
}

// counts writes "n passed · n failed · n Omarchy required to test" from x, y, criteria only, so
// they add up to the groups' rows; live is the last part's wording. With untested, Finish's, the
// criteria this run didn't test (skipped by the tester, or never reached) come before it: the
// rows leave them out.
func counts(x, y int, gr *g.Grid, f *Facts, live string, untested bool) int {
	x = gr.Put(x, y, i18n.T("%d passed", f.Tally(pace.Pass)), g.BGRN)
	if n := f.Tally(pace.Partial); n > 0 {
		x = gr.Put(x, y, "  ·  ", g.DIM)
		x = gr.Put(x, y, i18n.T("%d partial", n), g.AMB)
	}
	x = gr.Put(x, y, "  ·  ", g.DIM)
	fc := g.DIM
	if f.Tally(pace.Fail) > 0 {
		fc = g.BRED
	}
	x = gr.Put(x, y, i18n.T("%d failed", f.Tally(pace.Fail)), fc)
	if n := f.Tally(pace.Skip); untested && n > 0 {
		x = gr.Put(x, y, "  ·  ", g.DIM)
		x = gr.Put(x, y, i18n.T("%d not tested", n), g.GRY)
	}
	x = gr.Put(x, y, "  ·  ", g.DIM)
	return gr.Put(x, y, live, g.YEL)
}

// groupsGrid draws the criteria groups in two columns: name, one mark per criterion, n/m passed.
// It returns how many rows of groups each column has.
// groupIcons are the categories' icons, by name (the catalog's msgids).
var groupIcons = map[string]string{
	"Boot": icons.Power, "Graphics": icons.GPU, "Display": icons.Display, "Input": icons.Keyboard,
	"Power": icons.Battery, "Audio": icons.Speaker, "Camera": icons.Camera, "Networking": icons.WiFi,
	"Ports": icons.USB, "Storage": icons.Disk, "Thermal": icons.Thermo,
}

func groupsGrid(gr *g.Grid, b Box, y0 int, groups []Group, t, from, step float64, skipShade bool) int {
	half := (len(groups) + 1) / 2
	for n, grp := range groups {
		if t < from+float64(n)*step {
			continue
		}
		col, row := 0, n%half
		if n >= half {
			col = 1
		}
		gx, gy := b.X+1+col*(b.W>>1), y0+row*2
		nx := gx
		if ic, ok := groupIcons[grp.Name]; ok { // the category's icon before its name
			nx = gr.Put(gx, gy, ic, g.BCYN) + 1
		}
		gr.Put(nx, gy, padTo(i18n.S(grp.Name), 15-(nx-gx)), g.GRY)
		pass := 0
		for i, r := range grp.Items {
			m, c, _ := mark(r)
			if r == pace.Skip && skipShade {
				m, c = "░", g.DIM // not in this run
			}
			gr.Put(gx+16+i*2, gy, m, c)
			if r == pace.Pass {
				pass++
			}
		}
		gr.Put(gx+16+len(grp.Items)*2+1, gy, i18n.T("%d/%d", pass, len(grp.Items)), g.DIM)
	}
	return half
}

// fromLine is the line under the counts: how many checks, with which fixes.
// The fixes count the hands-on tests' too (the lid wake fix), and stay once hands-on results
// come in.
func fromLine(f *Facts) string {
	s := i18n.T("From %d automatic checks", len(f.Checks))
	if n := len(f.HandsOn); n > 0 {
		s = i18n.N("From %d automatic checks and %d hands-on result", "From %d automatic checks and %d hands-on results", n, len(f.Checks), n)
	}
	if n, names := f.Fixed(); n > 0 {
		s += ", " + i18n.N("%d with an OmaBoot? fix (%s)", "%d with an OmaBoot? fix (%s)", n, n, strings.Join(names, ", "))
	}
	return s + ". " + i18n.T("L shows the full log.")
}

// testedLine is what the results stand for: on Quick results "On Omarchy … · Mesa …, GPU ·
// started in 38 s"; in the report "tested on …", with the image before the start-up time.
func testedLine(f *Facts, report bool) string {
	s := i18n.T("On %s", f.TestedOn)
	if report {
		s = i18n.T("tested on %s", f.TestedOn)
		if f.Image != "" {
			s += " · " + i18n.T("image %s", f.Image)
		}
	}
	if f.StartedIn != "" && (report || f.BootLine == "") { // Quick results' boot line says it
		s += " · " + i18n.T("started in %s", f.StartedIn)
	}
	return s
}

// drawResults is Quick results, or (review) the read-only report opened from Finish: the
// report's sections, for now only "Quick pass"; "Manual pass" joins it once the hands-on tests
// exist.
func drawResults(gr *g.Grid, o *Options, st *State, f *Facts, review bool) Box {
	t := st.T
	tc := t
	if review {
		tc = 99 // the copy shows everything at once
	}
	spec := frameSpec{title: i18n.T("Quick results"), step: 4, ho: st.HO, right: netRight(f, st), mac: true,
		keys: Keys{Esc: true, Back: true, Log: true, EscClose: st.Overlay == LogOverlay}}
	if st.Overlay == LogOverlay {
		spec.keys.Back = false
	}
	if review {
		spec.title, spec.step, spec.keys = i18n.T("Report · read-only"), stepFinish, Keys{Esc: true, EscClose: true, Log: true}
	}
	b := frame(gr, o, st, f.Mac, spec)
	l := layout(b, o.Plain)
	hx, top := l.FieldX, l.BandTop+2
	if o.Plain {
		top = l.BandTop
	} else {
		ex := boot.Idle
		if !review && t < 1.5 {
			ex = boot.Pleased
		}
		boot.Draw(gr, l.BootX, l.BandTop+1, ex, life(st, t))
	}
	// the ring on the band's right: the lines under the headline wrap to its left
	ring := ringOf(f)
	rw := 24
	rx := l.FieldX + l.FieldW - rw - 3
	if o.Plain || len(ring) == 0 || rx-hx-3 < 60 {
		rx = 0
	}
	if rx > 0 {
		top = l.BandTop + 1
		t0 := 0.6
		if review {
			t0 = -1.2 // the copy: already drawn
		}
		resultRing(gr, rx, l.BandTop, rw, l.BandH-1, t, t0, ring)
	}
	gr.Put(hx, top, typed(f.Headline, tc, 50), g.WHT)
	if tc > 0.6 {
		counts(hx, top+1, gr, f, i18n.T("%d Omarchy required to test", f.Tally(pace.Live)), false)
	}
	if tc > 1.0 && rx > 0 {
		lw, y := rx-hx-3, top+2
		put := func(s string, col g.Color, mark string) {
			for i, r := range wrap(s, lw-width(mark)) {
				if y >= l.BandTop+7 {
					return
				}
				if i == 0 && mark != "" {
					gr.Put(hx, y, mark, col)
				}
				gr.Put(hx+width(mark), y, r, col)
				y++
			}
		}
		put(fromLine(f), g.DIM, "")
		if !review && f.TestedOn != "" {
			put(testedLine(f, false), g.DIM, "")
		}
		switch {
		case review:
		case f.SlowDrive != "":
			put(f.SlowDrive, g.AMB, "▲ ")
		case f.BootLine != "":
			put(f.BootLine, g.DIM, "")
		}
	}
	if tc > 1.0 && rx == 0 {
		gr.Put(hx, top+2, cut(fromLine(f), b.X+b.W-hx), g.DIM)
		if !review && f.TestedOn != "" { // what these results stand for
			gr.Put(hx, top+3, cut(testedLine(f, false), b.X+b.W-hx), g.DIM)
		}
		// the boot timing, or (amber) a warning when the OmaBoot? drive reads slowly (0.7.5)
		switch {
		case review:
		case f.SlowDrive != "":
			x := gr.Put(hx, top+4, "▲ ", g.AMB)
			gr.Put(x, top+4, cut(f.SlowDrive, b.X+b.W-x), g.AMB)
		case f.BootLine != "":
			gr.Put(hx, top+4, cut(f.BootLine, b.X+b.W-hx), g.DIM)
		}
	}
	if !o.Plain {
		sayW := l.FieldW - 1
		if rx > 0 {
			sayW = rx - hx - 3
		}
		switch {
		case review && t > 0.3:
			s := i18n.T("this run's report, not sent yet. Esc closes it.")
			if f.Report != "" {
				s = i18n.T("this run's report, as it was sent. Esc closes it.")
			}
			sayBox(gr, &st.Said, hx, l.BandTop+7, sayW, l.RuleY-(l.BandTop+7)-2, t, []line{{0.3, s}}, -1)
		case !review && t > 1.6 && f.AnswerSay != "":
			sayBox(gr, &st.Said, hx, l.BandTop+7, sayW, l.RuleY-(l.BandTop+7)-2, t, []line{{1.6, f.AnswerSay}}, -1)
		default: // none of his own yet: a line from the console still shows
			sayBox(gr, &st.Said, hx, l.BandTop+7, sayW, l.RuleY-(l.BandTop+7)-2, t, nil, -1)
		}
	}
	if st.Overlay == LogOverlay {
		drawLog(gr, b, l, st, f.All(), f.Startup)
		return b
	}
	if review {
		divider(gr, b, l.RuleY, i18n.T("Report Summary")+"  ·  "+i18n.T("quick pass and hands-on tests"), b.X, g.BVIO)
	} else {
		divider(gr, b, l.RuleY, "", 0, g.BVIO)
	}
	ox, end := b.X+1, b.X+b.W
	if review { // the report's state first, then the summary
		y := l.ContentY
		x := gr.Put(ox, y, "■", g.BVIO)
		switch {
		case f.Report != "":
			x = gr.Put(x+1, y, i18n.T("Submitted to %s · %s", f.Site, f.Report), g.WHT)
			x = gr.Put(x, y, "  ·  ", g.DIM)
			gr.Put(x, y, cut(f.ReportPage, end-x), g.BVIO)
		case f.SavedFile != "":
			x = gr.Put(x+1, y, i18n.T("Not sent; a copy is on external storage:"), g.WHT)
			gr.Put(x+1, y, cut(f.SavedFile, end-x-1), g.DIM)
		default:
			gr.Put(x+1, y, i18n.T("Not sent yet: Submit report on Finish sends it."), g.WHT)
		}
		// the report opens with what it covers, under its state
		if f.TestedOn != "" {
			y++
			gr.Put(ox+2, y, cut(testedLine(f, true), end-(ox+2)), g.DIM)
		}
		half := groupsGrid(gr, b, y+2, f.Criteria(), tc, 1.2, 0.12, true)
		// a dim rule one blank row under the summary, from under "Quick pass" to as far in from
		// the frame's right edge
		ry := y + 2 + half*2
		gr.Put(ox, ry, strings.Repeat("─", max(0, b.X1-(ox-b.X0)-ox+1)), g.DIM)
		reportItems(gr, b, l, st, f, ry+2)
		return b
	}
	half := groupsGrid(gr, b, l.ContentY, f.Criteria(), tc, 1.2, 0.12, true)
	ay := l.ContentY + half*2
	if t > resultsReady {
		actions(gr, ox, ay, resultsActions(st), firstOn(resultsActions(st), st.ResultsSel), true, 0, end)
	}
	if t > 3 {
		gr.Put(ox, l.Bottom, i18n.T("Installing Omarchy afterwards? OmaBoot? Quest picks up from there."), g.DIM)
	}
	return b
}

// reportRow is one row of Show report's itemised tests: a category's first row carries its
// icon and name; nil rows are the blank rows between categories.
type reportRow struct {
	cat  string // the category's name (msgid) on its first row, else ""
	icon string
	c    Check
}

// reportRows are checks grouped by category in the catalog's order, a blank row between
// categories.
func reportRows(cs []Check) []*reportRow {
	by := map[string][]Check{}
	for _, c := range cs {
		id, _ := checks.Category(c.ID)
		by[id] = append(by[id], c)
	}
	var rows []*reportRow
	for _, id := range checks.CategoryOrder {
		if len(by[id]) == 0 {
			continue
		}
		if len(rows) > 0 {
			rows = append(rows, nil)
		}
		_, name := checks.Category(id + ".x")
		for i, c := range by[id] {
			r := &reportRow{c: c}
			if i == 0 {
				r.cat, r.icon = name, groupIcons[name]
			}
			rows = append(rows, r)
		}
	}
	return rows
}

// reportItems itemises every test under Show report's summary: Quick pass and Hands-on side by
// side, grouped by category. ↑↓ scroll both columns together when they
// don't fit.
func reportItems(gr *g.Grid, b Box, l Layout, st *State, f *Facts, y0 int) {
	xs := []int{b.X + 1, b.X + 1 + b.W/2}
	cw := b.W/2 - 2
	qsub := i18n.N("%d automatic check", "%d automatic checks", len(f.Checks), len(f.Checks))
	if f.QuickPassIn != "" {
		qsub += "  ·  " + f.QuickPassIn
	}
	hsub := i18n.T("none in this run")
	if n := len(f.HandsOn); n > 0 {
		hsub = i18n.N("%d test", "%d tests", n, n)
	}
	cols := []struct {
		title, sub string
		rows       []*reportRow
	}{{i18n.T("Quick pass"), qsub, reportRows(f.Checks)}, {i18n.T("Hands-on"), hsub, reportRows(f.HandsOn)}}
	n := 0
	for i, c := range cols {
		x := gr.Put(xs[i], y0, c.title, g.WHT)
		gr.Put(x+2, y0, cut(c.sub, cw-(x+2-xs[i])), g.DIM)
		n = max(n, len(c.rows))
	}
	top := y0 + 2
	room := max(0, l.Bottom-top) // the last row is for the note; none on the smallest screen with the console open
	st.ReportTop = max(0, min(st.ReportTop, n-room))
	for i, c := range cols {
		for k := st.ReportTop; k < len(c.rows) && k < st.ReportTop+room; k++ {
			r, y, x := c.rows[k], top+k-st.ReportTop, xs[i]
			if r == nil {
				continue
			}
			if r.cat != "" {
				nx := x
				if r.icon != "" {
					nx = gr.Put(x, y, r.icon, g.BCYN) + 1
				}
				gr.Put(nx, y, cut(i18n.S(r.cat), 15-(nx-x)), g.GRY)
			}
			m, mc, word := mark(r.c.Status)
			gr.Put(x+15, y, m, mc)
			lc := g.GRY
			if r.c.Status == pace.Skip {
				lc = g.DIM
			}
			lx := gr.Put(x+17, y, cut(i18n.S(r.c.Label), cw-17), lc)
			if r.c.Status != pace.Pass && lx+2+width(word) <= x+cw {
				gr.Put(lx+2, y, word, mc)
			}
		}
	}
	note := i18n.T("Read-only. Esc goes back to Finish.")
	switch {
	case room == 0 && n > 0:
		note = i18n.N("%d row: close the console to see it", "%d rows: close the console to see them", n, n) + "  ·  " + note
	case n > room:
		note = i18n.T("%d–%d of %d rows", st.ReportTop+1, min(n, st.ReportTop+room), n) + "  ·  " + i18n.T("↑↓ scroll") + "  ·  " + note
	}
	gr.Put(b.X+1, l.Bottom, cut(note, b.W-2), g.DIM)
}

// logRows is how many rows the full log has on screen.
func logRows(l Layout) int { return max(1, l.Bottom-1-(l.RuleY+2)) }

// logExtra is the full log's rows after the results: the startup and the console, each after a
// blank row and its heading.
func logExtra(startup []string, st *State) int {
	n := 0
	if len(startup) > 0 {
		n += 2 + len(startup)
	}
	if st.Con != nil && len(st.Con.Log) > 0 {
		n += 2 + len(st.Con.Log)
	}
	return n
}

// drawLog is the full log (L): every check of the run so far, then how the Mac's startup went
// (once the quick pass is done), scrollable.
func drawLog(gr *g.Grid, b Box, l Layout, st *State, checks []Check, startup []string) {
	divider(gr, b, l.RuleY, i18n.T("Full log"), b.X+1, g.WHT)
	gr.Fill(b.X, l.RuleY+1, b.W, l.Bottom-l.RuleY, ' ', g.GRY, g.K)
	n := len(checks) + logExtra(startup, st)
	var con []string // every console command and its output
	if st.Con != nil {
		con = st.Con.Log
	}
	rows := logRows(l)
	top := max(0, min(st.LogTop, n-rows))
	y := l.RuleY + 2
	idW := 0 // the criteria's IDs, in a column of their own
	for _, c := range checks {
		idW = max(idW, width(c.ID))
	}
	idW = min(idW, 32)
	for i := top; i < n && i < top+rows; i++ {
		j := i - len(checks)
		if len(startup) > 0 {
			switch {
			case j == 1:
				gr.Put(b.X+1, y, i18n.T("Startup"), g.WHT)
			case j > 1 && j-2 < len(startup):
				gr.Put(b.X+3, y, cut(startup[j-2], b.W-4), g.DIM)
			}
			j -= 2 + len(startup)
		}
		switch {
		case i < len(checks):
			logRowID(gr, b.X+1, y, b.X+b.W, checks[i], idW)
		case j == 1 && len(con) > 0:
			gr.Put(b.X+1, y, i18n.T("Console"), g.WHT)
		case j > 1 && j-2 < len(con):
			gr.Put(b.X+3, y, cut(con[j-2], b.W-4), g.GRY)
		}
		y++
	}
	if n == 0 {
		gr.Put(b.X+1, y, i18n.T("nothing yet"), g.DIM)
	}
	note := i18n.T("↑↓ scroll  ·  L or Esc closes")
	if n > rows {
		note = i18n.T("%d–%d of %d", top+1, min(n, top+rows), n) + "  ·  " + note
	}
	gr.Put(b.X+1, l.Bottom, note, g.DIM)
}
