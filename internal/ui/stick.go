package ui

import (
	tea "github.com/charmbracelet/bubbletea"

	"github.com/omaboot/boot-live/internal/boot"
	g "github.com/omaboot/boot-live/internal/grid"
	"github.com/omaboot/boot-live/internal/i18n"
)

// The stick the system runs from came out (StickGoneMsg; only without copytoram). Anything
// tested from then on could fail for the wrong reason (a program or driver that can't load), so
// nothing more is tested or recorded: a hands-on test stops, the quick pass's later results are
// left out. A box like the quit question says so and stays up: Finish with what's done (once the
// quick pass is through), Restart, or Shutdown. Esc, and Keep going later, bring it back;
// quitting to the command line is no use without the stick.

// stickGone stops what's running and puts the box up.
func (m *Model) stickGone() {
	st := &m.st
	if st.Gone {
		return
	}
	st.Gone = true
	st.Sounds = false // the stick's box comes up over the screen itself
	if st.HO != nil {
		st.HO.stopAll()
	}
	if st.Scene == Network {
		m.be.NetStop()
	}
	m.pause(true)
	st.Overlay, st.StickSel = StickOverlay, 0
	if !stickActions(st, m.f)[0].Off {
		return
	}
	st.StickSel = 1
}

// stickActions are the box's choices.
func stickActions(st *State, f *Facts) []Action {
	fin := Action{Label: i18n.T("Finish with what's done"), Does: i18n.N("send or save the report with the %d result so far",
		"send or save the report with the %d results so far", len(f.All()), len(f.All()))}
	switch {
	case f.Report != "":
		fin.Does = i18n.T("the report was already sent (%s)", f.Report)
	case !st.PassDone:
		fin.Does, fin.Off = i18n.T("the quick pass didn't finish: there's nothing to send"), true
	}
	return []Action{fin,
		{Label: i18n.T("Restart"), Does: i18n.T("plug the drive back in first, to test again")},
		{Label: i18n.T("Shutdown"), Does: i18n.T("turn this Mac off")}}
}

func (m *Model) stickKey(key string) tea.Cmd {
	st := &m.st
	if !listKey(key, stickActions(st, m.f), &st.StickSel) {
		return nil
	}
	switch st.StickSel {
	case 0:
		m.pause(false)
		st.FinishSel = 0
		if m.o.Edition.Installer {
			st.FinishSel = 1 // Install Omarchy now is off: the installer was on the stick
		}
		m.goTo(Finish)
	case 1:
		return m.end(EndReboot)
	case 2:
		return m.end(EndPoweroff)
	}
	return nil
}

// drawStick draws the box over the screen.
func drawStick(gr *g.Grid, o *Options, b Box, st *State, f *Facts) {
	lose := i18n.T("I run from that drive, so anything I test now could fail for the wrong reason. I've stopped: what was tested before it came out is kept.")
	if len(f.All()) == 0 {
		lose = i18n.T("I run from that drive, so anything I test now could fail for the wrong reason. I've stopped; nothing was tested yet.")
	}
	after := i18n.T("Putting it back doesn't help this run. To test again, plug it back in and restart.")
	drawAsk(gr, o, b, st, boot.Concerned, i18n.T("The OmaBoot? drive came out"), lose, after, stickActions(st, f), st.StickSel, g.RED, g.BRED)
}
