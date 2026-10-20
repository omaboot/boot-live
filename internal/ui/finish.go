package ui

import (
	"math"
	"strings"

	"github.com/doesitomarchy/doesitomarchy/pkg/report"

	"github.com/omaboot/boot-live/internal/boot"
	g "github.com/omaboot/boot-live/internal/grid"
	"github.com/omaboot/boot-live/internal/i18n"
	"github.com/omaboot/boot-live/internal/pace"
)

// ── Finish ─────────────────────────────────────────────────────────────────────
//
// The one place to send or save. One run gives one report, covering everything done before
// Finish (later: the quick pass and the manual pass). Once it is sent, Submit keeps its place,
// greyed, with the code: a new report needs a new run.

// receivedAt is when, after the site's answer, the "Report received" line shows; "Thanks!" waits
// for it.
const receivedAt = 1.6

// sending reports whether the report is on its way.
func sending(st *State, f *Facts) bool { return st.SendAt >= 0 && f.Report == "" && st.SendErr == "" }

// minSending: the "sending to …" line stays up at least this long, however fast the site answers
// (over a LAN it's a fraction of a second, so it was never seen).
const minSending = 1.5

// sentAt is when the screen shows the report as sent: when the site answered, or once the
// sending line has been up minSending. Before that a sent report still shows as sending.
func sentAt(st *State) float64 {
	if st.SendAt >= 0 {
		return max(st.SentAt, st.SendAt+minSending)
	}
	return st.SentAt
}

// finishBack reports whether Backspace goes back to Quick results: until the report is sent,
// and not from the Submit report form (it edits the name there, and the list has "Don't send").
func finishBack(st *State, f *Facts) bool {
	return f.Report == "" && !st.Submitting && !sending(st, f) && !st.Saving
}

// finished reports whether the run's report is out: sent, or saved to external storage.
func finished(f *Facts) bool { return f.Report != "" || f.SavedFile != "" }

// finishActions are Finish's actions, by edition: only OmaBoot? Omarchy has an installer, and it
// stays first.
func finishActions(o *Options, st *State, f *Facts) []Action {
	submit := Action{Label: i18n.T("Submit report"), Does: i18n.T("send this run's report to %s", f.Site)}
	switch {
	case f.Report != "":
		submit = Action{Label: i18n.T("Submitted · %s", f.Report), Does: i18n.T("one report per run; a new one needs a new quick pass"), Off: true}
	case !f.CanSubmit:
		submit.Does, submit.Off = f.NoSubmit, true
	case !st.Net.Online:
		submit.Does, submit.Off = i18n.T("offline: go back (delete) to Network to get online, or save a copy"), true
	}
	// saving says "external storage" (a second drive labelled BOOTLIVE, or the OmaBoot? drive's own
	// reports partition), the drive OmaBoot? runs from is "the OmaBoot? drive"
	save := i18n.T("the report, in results/ on external storage; send it later")
	if f.Report != "" {
		save = i18n.T("the report and its code, in results/ on external storage")
	}
	if st.Gone {
		save = i18n.T("only to a second drive labelled BOOTLIVE: the OmaBoot? drive came out")
	}
	a := []Action{submit,
		{Label: i18n.T("Save a copy to external storage"), Does: save},
		{Label: i18n.T("Show report"), Does: i18n.T("the full report, read-only")},
		{Label: i18n.T("Shutdown"), Does: i18n.T("turn this Mac off")},
		{Label: i18n.T("Restart"), Does: i18n.T("boot something else, or run this again")}}
	if f.CanSendBug { // last, so the others keep their places
		bug := Action{Label: i18n.T("Send a bug report"), Does: i18n.T("something went wrong? the logs and this screen to OmaBoot?, scrubbed")}
		switch {
		case st.BugCode != "":
			bug = Action{Label: i18n.T("Bug report sent · %s", st.BugCode), Does: i18n.T("quote it when you tell us about the problem"), Off: true}
		case hung(st):
			bug.Does = i18n.T("the last run's Mac didn't come back: tell OmaBoot?, the logs scrubbed")
		}
		a = append(a, bug)
	}
	if o.Edition.Installer {
		inst := Action{Label: i18n.T("Install Omarchy now"), Does: i18n.T("the installer starts; OmaBoot? Quest takes over afterwards")}
		if st.Gone {
			inst.Does, inst.Off = i18n.T("the installer is on the drive that came out"), true
		}
		a = append([]Action{inst}, a...)
	}
	return a
}

// submitActions are the Submit report form's actions.
// hung reports whether this run picked up a run whose Mac didn't come back from sleep.
func hung(st *State) bool {
	h := st.HO
	return st.Resumed && h != nil && h.Setup.Env != nil && len(h.Setup.Env.Hung) > 0
}

// bugActions are the bug report form's choices: send, or back; once it's sent or waiting, back.
func bugActions(st *State) []Action {
	if st.BugCode != "" || st.BugNote != "" {
		return []Action{{Label: i18n.T("Back to Finish")}}
	}
	return []Action{{Label: i18n.T("Send bug report")}, {Label: i18n.T("Don't send (back to Finish)")}}
}

// bugWhat is what the bug report form says goes, and what happens to it (Privacy's Bug reports).
func bugWhat() []string {
	return []string{
		i18n.T("the full log, the kernel log, the report, this screen as text and the boot messages (and a sleep test's kernel lines)"),
		i18n.T("scrubbed first: serial numbers, network and IP addresses, the computer's and user names, e-mail addresses"),
		i18n.T("kept private for 180 days, then deleted; read by OmaBoot?'s maintainers and an AI model (Anthropic's Claude)"),
		i18n.T("a whole copy stays on external storage; it may name serial numbers"),
	}
}

// drawBugForm is Send a bug report, in the categories' place: what goes, then its choices, or
// how it went.
func drawBugForm(gr *g.Grid, st *State, x0, y, end int) {
	t := st.T
	gr.Put(x0, y, i18n.T("Send a bug report"), g.WHT)
	gr.Put(x0+width(i18n.T("Send a bug report")), y, cut("  ·  "+i18n.T("so OmaBoot?'s maintainers can look into what went wrong"), end-x0-20), g.DIM)
	y += 2
	for i, s := range bugWhat() {
		gr.Put(x0+2, y+i, "·", g.DIM)
		gr.Put(x0+4, y+i, cut(s, end-x0-4), g.GRY)
	}
	y += len(bugWhat())
	gr.Put(x0, y, cut(i18n.T("More at omaboot.live/privacy."), end-x0), g.DIM)
	y += 2
	switch {
	case st.BugSending:
		gr.Put(x0, y, spinner(t), g.BVIO)
		gr.Put(x0+2, y, i18n.T("saving it, then sending it, scrubbed…"), g.GRY)
		return
	case st.BugCode != "":
		x := gr.Put(x0, y, "■", g.BVIO)
		x = gr.Put(x+1, y, i18n.T("Sent as"), g.WHT)
		x = gr.Put(x+1, y, st.BugCode, g.BVIO)
		gr.Put(x, y, cut(".  "+i18n.T("Quote it when you tell us about the problem."), end-x), g.GRY)
		y += 2
	case st.BugNote != "":
		note := wrap(st.BugNote, min(end-x0, 110))
		for i, l := range note {
			gr.Put(x0, y+i, l, g.GRY)
		}
		y += len(note) + 1
	}
	actions(gr, x0, y, bugActions(st), st.BugSel, true, 0, end)
}

func submitActions() []Action {
	return []Action{{Label: i18n.T("Send report")}, {Label: i18n.T("Don't send report (back to results)")}}
}

// sentList is "What gets sent".
func sentList(o *Options, f *Facts) []string {
	return []string{
		i18n.T("this Mac's hardware IDs: model, board, CPU, GPU, PCI and USB IDs"),
		i18n.N("the %d result, how it was measured, and this %s version", "the %d results, how each was measured, and this %s version", len(f.All()), len(f.All()), o.Edition.Product()),
		i18n.T("never serial numbers, disk contents or personal files"),
	}
}

// ConsentNotice is what the Submit report form shows, word for word, for the report's
// consent_notice: DoesItOmarchy's notice, which every tool must show as it is (its
// pkg/report.ConsentNotice, in English), then what OmaBoot? sends.
func ConsentNotice(o *Options, f *Facts) string {
	return report.ConsentNotice + " " + i18n.T("What gets sent") + ": " + strings.Join(sentList(o, f), "; ") + "."
}

// planConsent is the consent_notice of a report a run plan sends: nobody saw the form, so it
// says that the plan's author agreed.
func planConsent(o *Options, f *Facts) string {
	return i18n.T("Sent by a run plan (%s): whoever wrote it agreed to this notice.", "omaboot-plan.yaml") + " " + ConsentNotice(o, f)
}

// testedGroups are the groups with only what this run tested.
func testedGroups(f *Facts) []Group {
	var groups []Group
	for _, grp := range f.Criteria() {
		var items []pace.Status
		for _, r := range grp.Items {
			if r != pace.Skip {
				items = append(items, r)
			}
		}
		if len(items) > 0 {
			groups = append(groups, Group{Name: grp.Name, Items: items})
		}
	}
	return groups
}

func drawFinish(gr *g.Grid, o *Options, st *State, f *Facts) Box {
	t := st.T
	sent := f.Report != ""
	holding := sent && t < sentAt(st) // answered, but the sending line hasn't had its time yet
	if holding {
		sent = false
	}
	form := (st.Submitting || holding) && !sent
	snd := sending(st, f) || holding
	onField := form && st.SubFocus == 0 && !snd
	title := i18n.T("Finish")
	if finished(f) {
		title = i18n.T("All finished")
	}
	right := omarchy(f)
	if f.FinishedIn != "" {
		right += "    " + i18n.T("finished in %s", f.FinishedIn)
	}
	b := frame(gr, o, st, f.Mac, frameSpec{title: title, step: stepFinish, ho: st.HO, right: right, mac: true,
		keys: Keys{Esc: true, Back: finishBack(st, f) && st.Overlay != LogOverlay, Log: true, EscClose: st.Overlay == LogOverlay}})
	l := layout(b, o.Plain)
	base := 0.0
	switch {
	case sent:
		base = sentAt(st)
	case snd:
		base = st.SendAt
	case form:
		base = st.SubAt
	}
	u := t - base
	// "Thanks!" (and its confetti) only once the "Report received" line is up; a copy saved to
	// the stick alone keeps the done word: nothing has reached the site yet
	thanked := sent && u > receivedAt
	hx := l.FieldX
	word := f.DoneWord
	if word == "" {
		word = i18n.T("Yay!")
	}
	if thanked {
		word = i18n.T("Thanks!")
	}
	if o.Plain {
		gr.Put(hx, l.BandTop, word, g.WHT)
		gr.Put(hx, l.BandTop+1, f.Headline, g.GRY)
	} else {
		good := f.Count(pace.Fail) == 0 || sent
		cu := u // confetti over Boot when the step opens, and again once the report is received
		if sent {
			cu = u - receivedAt
		}
		if !form && !snd && cu >= 0 && cu < 4.5 && good {
			boot.Confetti(gr, b.X, l.BandTop, cu)
		}
		ex := boot.Pleased
		switch {
		case snd, sent && !thanked:
			ex = boot.Work
		case st.SendErr != "":
			ex = boot.Concerned
		case form:
			ex = boot.Idle
		case !good:
			ex = boot.Hmm
		}
		boot.Draw(gr, l.BootX, l.BandTop+1, ex, life(st, t))
		boot.BigWord(gr, hx, l.BandTop, word, 'G', 2)
		next := i18n.T("next steps: install Omarchy, then keep helping by installing OmaBoot? Quest to test what a live boot can't.")
		if o.Edition.Installer {
			next = i18n.T("next steps: install Omarchy now, then keep helping by installing OmaBoot? Quest to test what a live boot can't.")
		}
		var ls []line
		switch {
		case sent:
			ls = []line{{receivedAt, i18n.T("received. a human checks it before it goes up.")}, {receivedAt + 1.8, next}}
		case snd && st.SendNote != "":
			ls = []line{{0, i18n.T("the connection went away. giving it a moment to come back.")}}
		case snd:
			ls = []line{{0, i18n.N("sending. %d result, %d KB, no secrets.", "sending. %d results, %d KB, no secrets.", len(f.All()), len(f.All()), f.ReportKB)}}
		case form && st.SendErr != "":
			ls = []line{{0, i18n.T("the site didn't take it. try again, or save a copy instead.")}}
		case form:
			ls = []line{{0.2, i18n.T("a name to credit, if you like. then send it.")}}
		case f.SavedFile != "":
			ls = []line{{0.6, i18n.T("all tests done. send the report, save a copy, or both.")}, {st.SavedAt + 0.3, i18n.T("a copy is on external storage. it can be sent later.")}}
		case st.SendErr != "" && st.SendErrAt > 0:
			ls = []line{{0.6, i18n.T("all tests done. send the report, save a copy, or both.")}, {st.SendErrAt + 0.3, i18n.T("no connection to send it over. save a copy; it can be sent later.")}}
		default:
			ls = []line{{0.6, i18n.T("all tests done. send the report, save a copy, or both.")}}
		}
		if sent || snd || form {
			for i := range ls {
				ls[i].at += base
			}
		}
		// from Submit report until it's in, the way out next to the big word: the report rides it
		// to the site; Boot's box keeps left of it
		sayW := l.FieldW - 1
		if form || snd {
			px := hx + width(word)*12 + 1
			p := netPathOf(st, f)
			if aw := l.FieldX + l.FieldW - px - 1; p.up >= 0 && aw >= 60 {
				p.scan, p.tags, p.talk, p.up = false, nil, "", max(p.up, 1) // online all along: lit
				if snd {
					n := len(f.All())
					p.pkt = i18n.N("report  ·  %d result, %d KB", "report  ·  %d results, %d KB", n, n, f.ReportKB)
					p.pktF = math.Mod((t-st.SendAt)/1.8, 1)
					if holding {
						p.pktF = 1
					}
				}
				netPath(gr, px, l.BandTop, aw, l.BandH-1, t, p)
				sayW = px - hx - 3
			}
		}
		// his box right under the big word (it was two rows lower as bare lines): the same rows of
		// text fit
		sayBox(gr, &st.Said, hx, l.BandTop+7, sayW, l.RuleY-(l.BandTop+7)-2, t, ls, -1)
	}
	if st.Overlay == LogOverlay {
		drawLog(gr, b, l, st, f.All(), f.Startup)
		return b
	}
	divider(gr, b, l.RuleY, "", 0, g.BVIO)
	x0, sy, end := b.X+1, l.ContentY, b.X+b.W
	if t > 0.3 {
		n := f.Tally(pace.Live)
		x := counts(x0, sy, gr, f, i18n.N("%d needs Omarchy installed to test", "%d need Omarchy installed to test", n, n), true)
		s := i18n.T("no hands-on tests")
		if n := len(f.HandsOn); n > 0 {
			s = i18n.N("%d hands-on result", "%d hands-on results", n, n)
		}
		if f.QuickPassIn != "" {
			s = i18n.T("quick pass %s", f.QuickPassIn) + "  ·  " + s
			if x+3+width(s) > end {
				s = i18n.T("quick pass %s", f.QuickPassIn)
			}
		}
		gr.Put(x+3, sy, cut(s, end-x-3), g.DIM)
	}
	if form {
		drawSubmitForm(gr, o, st, f, x0, sy+2, end, onField, snd, l.Bottom)
		return b
	}
	if st.Guard {
		drawGuard(gr, st, f, x0, sy+2, end)
		return b
	}
	if st.BugForm {
		drawBugForm(gr, st, x0, sy+2, end)
		return b
	}
	groups := testedGroups(f)
	half := (len(groups) + 1) / 2
	if t > 0.5 { // all categories at once
		groupsGrid(gr, b, sy+2, groups, t, 0, 0, false)
	}
	ay := sy + 2 + half*2 // one blank line under the last group
	if sent {             // straight away, naming the server: no blank moment after sending
		if u >= 0 {
			x := gr.Put(x0, ay, "■", g.BVIO)
			x = gr.Put(x+1, ay, i18n.T("Report received by %s, pending review:", f.Site), g.WHT)
			x = gr.Put(x+2, ay, f.ReportPage, g.BVIO)
			gr.Put(x, ay, cut("  ·  "+i18n.T("shows where it stands"), end-x), g.DIM)
		}
		ay += 2
	}
	if sent && u <= 2.6 || !sent && t <= finishReady {
		return b
	}
	ey := actions(gr, x0, ay, finishActions(o, st, f), st.FinishSel, true, 0, end)
	ny := ey + 1
	if !o.Edition.Installer {
		gr.Put(x0, ny, i18n.T("To install Omarchy, use the ISO from omarchy.org; OmaBoot? Quest picks up afterwards."), g.DIM)
		ny += 2
	}
	switch {
	case st.Saving:
		gr.Put(x0, ny, spinner(t), g.BVIO)
		gr.Put(x0+2, ny, i18n.T("saving to external storage…"), g.GRY)
	case st.SaveErr != "":
		gr.Put(x0, ny, "×", g.BRED)
		gr.Put(x0+2, ny, cut(st.SaveErr, end-(x0+2)), g.GRY)
	case f.SavedFile != "":
		x := gr.Put(x0, ny, "■", g.BVIO)
		x = gr.Put(x+1, ny, i18n.T("a copy is on external storage:"), g.GRY)
		gr.Put(x+1, ny, cut(f.SavedFile, end-x-1), g.DIM)
	case st.SendErr != "" && st.SendErrAt > 0: // the send never left: no connection (SentMsg.Offline)
		gr.Put(x0, ny, "×", g.BRED)
		gr.Put(x0+2, ny, cut(st.SendErr, end-(x0+2)), g.GRY)
	}
	return b
}

// guardActions are the "Changed in the console" box's: DoesItOmarchy.com never takes the run, so
// with it as the target only external storage is offered; a staging target (dev builds) still takes it.
func guardActions(st *State, f *Facts) []Action {
	// "external storage", not "the stick", in the guard box: a second drive or
	// the OmaBoot? drive's own reports partition, wherever Save finds room
	a := []Action{{Label: i18n.T("Save a copy to external storage"), Does: i18n.T("the report and these commands, in results/")},
		{Label: i18n.T("Don't send (back to results)")}}
	if f.Production {
		return a
	}
	send := Action{Label: i18n.T("Send to staging"), Does: i18n.T("dev builds: the test server, never shown on the site")}
	switch {
	case !f.CanSubmit:
		send.Does, send.Off = f.NoSubmit, true
	case !st.Net.Online:
		send.Does, send.Off = i18n.T("offline: go back (delete) to Network to get online, or save a copy"), true
	}
	return append([]Action{send}, a...)
}

// drawGuard is Submit on a run whose results were changed in the console: an amber box listing the
// commands instead of the send form.
func drawGuard(gr *g.Grid, st *State, f *Facts, x0, y, end int) {
	gr.Put(x0, y, i18n.T("Submit report"), g.WHT)
	gr.Put(x0+width(i18n.T("Submit report")), y, cut("  ·  "+i18n.N("%d result for %s", "%d results for %s", len(f.All()), len(f.All()), macLabel(f.Mac)), end-x0-14), g.DIM)
	y += 2
	cmds := st.Con.Marks
	const most = 6
	shown := cmds[:min(len(cmds), most)]
	rows := len(shown)
	if len(cmds) > most {
		rows++
	}
	bw, bh := min(end-x0-1, 96), rows+6
	box(gr, x0, y, bw, bh, g.AMB, i18n.T("Changed in the console"), g.AMB)
	gr.Put(x0+2, y+1, cut(i18n.T("Results in this run were set by hand, so DoesItOmarchy.com can't take its report."), bw-4), g.WHT)
	for i, c := range shown {
		gr.Put(x0+4, y+3+i, "·", g.DIM)
		gr.Put(x0+6, y+3+i, cut(c, bw-8), g.GRY)
	}
	if len(cmds) > most {
		gr.Put(x0+6, y+3+most, i18n.T("and %d more", len(cmds)-most), g.DIM)
	}
	gr.Put(x0+2, y+bh-2, cut(i18n.T("The report lists these commands. Staging and external storage still take it."), bw-4), g.DIM)
	y += bh + 1
	acts := guardActions(st, f)
	ey := actions(gr, x0, y, acts, st.GuardSel, true, 0, end)
	note := i18n.T("Release builds offer only external storage. To send a report to DoesItOmarchy.com, run the quick pass again.")
	if f.Production {
		note = i18n.T("Only external storage takes it. To send a report to DoesItOmarchy.com, run the quick pass again.")
	}
	gr.Put(x0, ey+1, cut(note, end-x0), g.DIM)
}

// drawSubmitForm is Submit report: the name to credit and what gets sent take the categories'
// place, then Send report and Don't send report.
func drawSubmitForm(gr *g.Grid, o *Options, st *State, f *Facts, x0, y, end int, onField, snd bool, bottom int) {
	t := st.T
	gr.Put(x0, y, i18n.T("Submit report"), g.WHT)
	gr.Put(x0+width(i18n.T("Submit report")), y, cut("  ·  "+i18n.N("%d result for %s", "%d results for %s", len(f.All()), len(f.All()), macLabel(f.Mac)), end-x0-14), g.DIM)
	y += 2
	const fw = 32
	gr.Put(x0, y, i18n.T("Name to credit"), g.GRY)
	gr.Put(x0+16, y, i18n.T("optional"), g.DIM)
	bc := g.DIM
	if onField {
		bc = g.BVIO
	}
	gr.Put(x0, y+1, "[", bc)
	gr.Put(x0+fw+1, y+1, "]", bc)
	name := st.Name
	if len(name) > fw-2 {
		name = name[len(name)-(fw-2):]
	}
	gr.Put(x0+2, y+1, string(name), g.WHT)
	if onField && blinkOn(t) {
		gr.Put(x0+2+len(name), y+1, "_", g.BVIO)
	}
	gr.Put(x0+fw+4, y+1, i18n.T("shown with your results; leave it empty to stay anonymous"), g.DIM)
	y += 3
	gr.Put(x0, y, i18n.T("What gets sent"), g.GRY)
	for i, s := range sentList(o, f) {
		gr.Put(x0+2, y+1+i, "·", g.DIM)
		gr.Put(x0+4, y+1+i, cut(s, end-x0-4), g.GRY)
	}
	notice := wrap(report.ConsentNotice, min(end-x0, 110)) // DoesItOmarchy's notice, as it is
	gap, more := 1, 0                                      // a short screen loses the blank lines around it
	if st.SendErr != "" {
		more = 2 + len(st.Problems)
	}
	if y+5+len(notice)+1+2+more > bottom {
		gap = 0
	}
	y += 4 + gap
	for i, l := range notice {
		gr.Put(x0, y+i, l, g.GRY)
	}
	y += len(notice) + gap
	switch {
	case snd && st.SendNote != "": // waiting for the connection first
		gr.Put(x0, y, spinner(t), g.BVIO)
		gr.Put(x0+2, y, cut(st.SendNote, end-(x0+2)), g.GRY)
	case snd:
		gr.Put(x0, y, spinner(t), g.BVIO)
		gr.Put(x0+2, y, i18n.T("sending to %s…", f.Site), g.GRY)
		const w = 24
		frac := 0.95 * (1 - math.Exp(-(t-st.SendAt)/1.5))
		k := int(max(0, min(1, frac)) * w)
		for i := 0; i < w; i++ {
			if i < k {
				gr.SetBG(x0+34+i, y, '█', g.BVIO, g.K)
			} else {
				gr.SetBG(x0+34+i, y, '░', g.DIM, g.K)
			}
		}
	default:
		if st.SendErr != "" {
			gr.Put(x0, y, "×", g.BRED)
			gr.Put(x0+2, y, cut(st.SendErr, end-(x0+2)), g.WHT)
			y++
			for _, p := range st.Problems {
				if y >= bottom-4 {
					break
				}
				gr.Put(x0+4, y, cut(p, end-(x0+4)), g.GRY)
				y++
			}
			y++
		}
		actions(gr, x0, y, submitActions(), st.SubFocus-1, !onField, 0, end)
	}
	if y+3 < bottom { // a short screen leaves it out: the notice above says where it goes
		gr.Put(x0, y+3, i18n.T("One report per run: this sends it to %s", f.Site), g.DIM)
	}
}
