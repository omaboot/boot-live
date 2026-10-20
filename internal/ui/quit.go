package ui

import (
	"strings"

	"github.com/omaboot/boot-live/internal/boot"
	g "github.com/omaboot/boot-live/internal/grid"
	"github.com/omaboot/boot-live/internal/i18n"
)

// The quit question (Esc on any screen, and Quick results' "Quit without sending"): a box with a
// red border in the band's field, Boot outside it, like the GPU notice but with no countdown.
// Its words depend on what quitting would lose. "Keep testing" (or "Don't quit") is first and
// the default. During the quick pass the playback pauses while it is up.

// quitCase is what quitting would lose.
type quitCase int

const (
	quitBefore  quitCase = iota // nothing tested yet
	quitNetwork                 // on Network, nothing tested yet
	quitPass                    // during the quick pass
	quitUnsent                  // results, not sent or saved
	quitSaved                   // a copy is on the stick, not sent
	quitSent                    // the report was sent
	quitHandsOn                 // during the hands-on tests: Esc ends them (to Finish), not the tool
)

func quitCaseOf(st *State, f *Facts) quitCase {
	switch {
	case st.Scene == Attention || st.Scene == Test:
		return quitHandsOn
	case f.Report != "":
		return quitSent
	case f.SavedFile != "":
		return quitSaved
	case st.Scene == Quick && !st.PassDone:
		return quitPass
	case st.PassDone:
		return quitUnsent
	case st.Scene == Network:
		return quitNetwork
	}
	return quitBefore
}

// quitActions are the question's choices: stay (the default), quit to the prompt, then Shutdown
// and Restart as on Finish (owner, 2026-10-12). Ending the hands-on tests only goes to Finish.
func quitActions(o *Options, c quitCase) []Action {
	stay := Action{Label: i18n.T("Keep testing"), Does: i18n.T("back to where you were")}
	quit := Action{Label: i18n.T("Quit to prompt"), Does: i18n.T("close %s; the command line stays", o.Edition.Product())}
	if o.Edition.Installer {
		quit = Action{Label: i18n.T("Quit to the installer"), Does: i18n.T("close %s; the Omarchy installer starts", o.Edition.Product())}
	}
	switch c {
	case quitPass:
		stay.Does, quit.Does = i18n.T("the quick pass carries on"), i18n.T("stop here; nothing is sent or saved")
	case quitUnsent:
		stay.Label, quit.Does = i18n.T("Don't quit"), i18n.T("this run's results are lost")
	case quitSaved, quitSent:
		stay.Label = i18n.T("Don't quit")
	case quitHandsOn:
		stay.Does = i18n.T("back to the test you were on")
		quit = Action{Label: i18n.T("End the tests"), Does: i18n.T("go to Finish with the results so far")}
		return []Action{stay, quit}
	}
	return []Action{stay, quit, {Label: i18n.T("Shutdown"), Does: i18n.T("turn this Mac off")},
		{Label: i18n.T("Restart"), Does: i18n.T("boot something else, or run this again")}}
}

// quitWords are the question's heading and Boot's words: what quitting would lose, then what
// happens on the image once the tool has quit.
func quitWords(o *Options, st *State, f *Facts, c quitCase) (head, lose, after string) {
	switch c {
	case quitNetwork:
		head, lose = i18n.T("Leaving already?"), i18n.T("I haven't tested anything yet. No network? I can test offline too: pick Skip, test offline.")
	case quitPass:
		head = i18n.T("Stop the quick pass?")
		if n := len(f.Checks); n > 0 {
			lose = i18n.N("I'll wait while you decide. If you quit now, the %d result so far is lost: nothing is sent or saved.",
				"I'll wait while you decide. If you quit now, the %d results so far are lost: nothing is sent or saved.", n, n)
		} else {
			lose = i18n.T("I'll wait while you decide. If you quit now, nothing is tested, sent or saved.")
		}
	case quitUnsent:
		head = i18n.T("Quit without sending?")
		lose = i18n.T("Your results aren't sent or saved yet, so quitting loses them. Save a copy to external storage on Finish, and it can be sent later.")
		if st.Scene == Finish {
			lose = i18n.T("Your results aren't sent or saved yet, so quitting loses them. Save a copy to external storage first, and it can be sent later.")
		}
	case quitSaved:
		head, lose = i18n.T("Quit now?"), i18n.T("A copy of the report is on external storage, so nothing is lost. It isn't sent yet; it can be sent later.")
	case quitSent:
		head = i18n.T("All done?")
		lose = i18n.T("Your report is safe: %s has it (%s). Quitting just closes %s.", f.Site, f.Report, o.Edition.Product())
	case quitHandsOn:
		head = i18n.T("End the hands-on tests?")
		lose = i18n.T("Everything tested so far stays in this run's report; the tests you didn't do go in as not tested.")
		return head, lose, i18n.T("Finish comes next: send the report, save a copy, or both.")
	default:
		head, lose = i18n.T("Leaving already?"), i18n.T("I haven't tested anything yet, so nothing is lost.")
	}
	// what follows the tool's exit: OmaBoot? Live's tty1 script ends at the root shell;
	// OmaBoot? Omarchy's goes on to the Omarchy installer
	after = i18n.T("Quit to prompt leaves you at the command line, where reboot and poweroff work too.")
	if o.Edition.Installer {
		after = i18n.T("Quit to the installer starts the Omarchy installer.")
	}
	return head, lose, after
}

// quitNote is what the console says once the tool has quit.
func quitNote(c quitCase, f *Facts) string {
	switch c {
	case quitBefore, quitNetwork:
		return i18n.T("Quit: nothing was tested.")
	case quitSaved:
		return i18n.T("Quit: the report is on external storage, not sent.")
	case quitSent:
		return i18n.T("Quit: report %s was sent to %s.", f.Report, f.Site)
	}
	return i18n.T("Quit: nothing was sent or saved.")
}

// endActions are Finish's question before Shutdown or Restart with the report neither sent nor
// saved (owner, 0.6.1 review): go back (the default), or go ahead and lose the run's results.
func endActions(what Ending) []Action {
	anyway := Action{Label: i18n.T("Shut down anyway"), Does: i18n.T("this run's results are lost")}
	if what == EndReboot {
		anyway.Label = i18n.T("Restart anyway")
	}
	return []Action{{Label: i18n.T("Go back"), Does: i18n.T("send the report or save a copy first")}, anyway}
}

// drawEnd draws that question over Finish.
func drawEnd(gr *g.Grid, o *Options, b Box, st *State) {
	head := i18n.T("Shut down without sending?")
	if st.EndWhat == EndReboot {
		head = i18n.T("Restart without sending?")
	}
	lose := i18n.T("Your results aren't sent or saved yet, so this loses them. Save a copy to external storage first, and it can be sent later.")
	drawAsk(gr, o, b, st, boot.Concerned, head, lose, "", endActions(st.EndWhat), st.EndSel, g.RED, g.BRED)
}

// drawQuit draws the quit question over the screen.
func drawQuit(gr *g.Grid, o *Options, b Box, st *State, f *Facts) {
	c := quitCaseOf(st, f)
	ex := boot.Idle
	if c == quitPass || c == quitUnsent || c == quitHandsOn {
		ex = boot.Concerned
	}
	head, lose, after := quitWords(o, st, f, c)
	drawAsk(gr, o, b, st, ex, head, lose, after, quitActions(o, c), st.ConfirmSel, g.RED, g.BRED)
}

// drawAsk draws a question over the screen: a box in the band's field with a border (red when
// something is lost, violet for a plain choice), Boot (ex) outside it; the heading (hc), what
// comes after, and the choices. What it means is Boot's to say: in his box under the band, its ▲
// up at him, as on the port tests (owner, 2026-10-13: his lines in his box on every screen);
// with no Boot (plain), in the question's box.
func drawAsk(gr *g.Grid, o *Options, b Box, st *State, ex boot.Expr, head, lose, after string, acts []Action, sel int, bc, hc g.Color) {
	l := layout(b, o.Plain)
	x, y, w, h := l.FieldX, l.BandTop, l.FieldW-1, l.BandH
	if o.Plain { // no band: the question takes the content's place
		x, y, w = b.X+1, l.ContentY, min(b.W-2, 110)
		h = min(14, l.Bottom-y)
		gr.Fill(b.X, l.RuleY+1, b.W, l.Bottom-l.RuleY, ' ', g.GRY, g.K)
	} else {
		gr.Fill(l.FieldX, l.FxTop, l.FieldW, l.FxH, ' ', g.GRY, g.K)
		gr.Fill(l.BootX, l.BandTop, boot.W+2, boot.H+2, ' ', g.GRY, g.K)
		boot.Draw(gr, l.BootX, l.BandTop+1, ex, life(st, st.T))
	}
	inner := w - 6
	l1, l2 := wrap(lose, inner), wrap(after, inner)
	if !o.Plain {
		l1 = nil
		// the screen under the band clears (half a screen showing round his box reads as broken);
		// his words whole at once: the quick pass's time stops while it's up
		says := []line{{0, lose}}
		gr.Fill(b.X0+1, l.RuleY+1, b.X1-b.X0-1, b.Y1-l.RuleY-1, ' ', g.GRY, g.K)
		sayBox(gr, &st.Said, b.X+1, l.ContentY, b.W-2, max(1, l.Bottom-l.ContentY-2), 1e6+st.T, says, l.BootX+8)
	}
	// rows: border, heading, [blank], lose, after, [blank], actions, border; the blanks go first
	// when the room is short
	gapTop, gapAct := 1, 1
	need := func() int { return 3 + gapTop + len(l1) + len(l2) + gapAct + len(acts) }
	if need() > h {
		gapTop = 0
	}
	if need() > h {
		gapAct = 0
	}
	bh := min(h, need())
	gr.Fill(x, y, w, bh, ' ', g.GRY, g.K)
	gr.Put(x, y, "┌"+strings.Repeat("─", max(0, w-2))+"┐", bc)
	for yy := y + 1; yy < y+bh-1; yy++ {
		gr.Put(x, yy, "│", bc)
		gr.Put(x+w-1, yy, "│", bc)
	}
	gr.Put(x, y+bh-1, "└"+strings.Repeat("─", max(0, w-2))+"┘", bc)
	nx, yy := x+3, y+1
	gr.Put(nx, yy, head, hc)
	yy += 1 + gapTop
	for _, s := range l1 {
		gr.Put(nx, yy, s, g.GRY)
		yy++
	}
	for _, s := range l2 {
		gr.Put(nx, yy, s, g.DIM)
		yy++
	}
	yy += gapAct
	if yy+len(acts) <= y+bh-1 {
		actions(gr, nx, yy, acts, sel, true, 0, x+w-2)
	}
	esc := st.Overlay == ConfirmOverlay || st.Overlay == EndOverlay // the stick box has no way out but its choices
	gr.Fill(b.X0+1, gr.H-1, gr.W/2, 1, ' ', g.GRY, g.K)             // the screen's own keys don't apply
	hintBar(gr, b.X0+1, gr.H-1, Keys{Esc: esc, EscClose: esc, Con: conAvail(st)})
}
