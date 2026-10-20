package ui

import (
	"errors"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/omaboot/boot-live/internal/i18n"
	"github.com/omaboot/boot-live/internal/network"
)

// Keys: ↑↓ and Enter work the ▶ list on every screen (1–9 jump to an item); the hint bar's Esc
// quits (asking first once there are results to lose; on the read-only report and the full log
// it closes), Backspace ("delete") goes back a step where the bar shows it, L opens the full log.
// In a text field the keys type, and Backspace deletes.

func (m *Model) key(k tea.KeyMsg) tea.Cmd {
	key := k.String()
	st := &m.st
	if st.Overlay == StickOverlay {
		return m.stickKey(key)
	}
	if st.Overlay == SwitchOverlay {
		m.switchKey(key)
		return nil
	}
	if st.Scene == Test && st.HO != nil && st.HO.Run != nil && st.HO.Run.TakesKeys() {
		return m.testKey(key) // the test has the keyboard: every key is its, Ctrl+C too
	}
	if key == "ctrl+c" {
		return m.quit("")
	}
	if st.Doze || st.Blanked {
		m.wake() // the waking key does nothing else
		return nil
	}
	m.wake()
	if m.planKey() { // during Hello's countdown: the run plan is ignored, the key does nothing else
		return nil
	}
	if ok, cmd := m.conKey(k); ok { // the console: its key toggles it, and while open it takes the keys
		return cmd
	}
	if st.Sounds {
		m.soundsKey(key)
		return nil
	}
	switch st.Overlay {
	case ConfirmOverlay:
		return m.confirmKey(key)
	case EndOverlay:
		return m.endKey(key)
	case LogOverlay:
		m.logKey(key)
		return nil
	}
	if st.Scene == Network && st.Pw && !st.Form.Open {
		m.pwKey(k) // the password box has the keyboard: Esc goes back to the list
		return nil
	}
	if key == "esc" && st.Scene == Test {
		return m.testKey(key) // not while it's going to sleep
	}
	if key == "esc" {
		if st.Scene == Report {
			m.goTo(Finish)
			return nil
		}
		return m.askQuit()
	}
	if st.Scene == Report { // ↑↓ scroll the itemised tests; drawResults keeps it in range
		switch key {
		case "up", "k":
			st.ReportTop = max(0, st.ReportTop-1)
		case "down", "j":
			st.ReportTop++
		case "pgup":
			st.ReportTop = max(0, st.ReportTop-10)
		case "pgdown":
			st.ReportTop += 10
		}
	}
	if (key == "m" || key == "M") && soundKey(st) {
		m.toggleSound()
		return nil
	}
	if (key == "l" || key == "L") && m.logOn() && !m.textFocus() {
		st.Overlay, st.LogTop = LogOverlay, 0
		if st.Scene == Quick {
			st.LogTop = 1 << 20 // the newest at the bottom; drawLog keeps it in range
		}
		return nil
	}
	switch st.Scene {
	case Hello:
		return m.helloKey(key)
	case WhichMac:
		m.macKey(key)
	case Network:
		return m.networkKey(k)
	case Quick:
		if key == "enter" && st.GPU.Waiting && st.GPU.At >= 0 {
			m.gpuGo() // Start now
		}
	case Results:
		return m.resultsKey(key)
	case Finish:
		return m.finishKey(k)
	case Attention:
		m.attentionKey(key)
	case Test:
		return m.testKey(key)
	}
	return nil
}

// logOn reports whether L opens the full log here.
func (m *Model) logOn() bool {
	switch m.st.Scene {
	case Quick, Results, Finish, Report, Attention:
		return true
	case Test:
		h := m.st.HO
		return h.Paused || h.Out != nil || !h.Run.View().Busy
	}
	return false
}

// textFocus reports whether a text field has the focus: keys type there.
func (m *Model) textFocus() bool { return textFocusOf(&m.st) }

func textFocusOf(st *State) bool {
	switch {
	case st.Scene == Network && st.Form.Open:
		return st.Form.Focus < 3
	case st.Scene == Network && st.Pw:
		return true
	case st.Scene == Finish && st.Submitting:
		return st.SubFocus == 0
	}
	return false
}

// askQuit is Esc (and Quick results' "Quit without sending"): the quit question, on every
// screen; the quick pass pauses while it is up. A run that couldn't start safely quits at once
// (its only action is Quit).
func (m *Model) askQuit() tea.Cmd {
	st, f := &m.st, m.f
	if f.Fatal != "" {
		return m.end(EndRefused)
	}
	if st.Gone { // quitting to the command line is no use without the stick: restart or power off
		st.Overlay, st.StickSel = StickOverlay, 1
		return nil
	}
	m.pause(true)
	st.Overlay, st.ConfirmSel = ConfirmOverlay, 0
	return nil
}

func (m *Model) confirmKey(key string) tea.Cmd {
	st := &m.st
	if key == "esc" {
		st.Overlay = NoOverlay
		m.pause(false)
		return nil
	}
	c := quitCaseOf(st, m.f)
	if listKey(key, quitActions(&m.o, c), &st.ConfirmSel) {
		if st.ConfirmSel == 1 && c == quitHandsOn {
			st.Overlay = NoOverlay
			m.endHandsOn()
			return nil
		}
		switch st.ConfirmSel {
		case 1:
			return m.quit(quitNote(c, m.f))
		case 2:
			return m.end(EndPoweroff)
		case 3:
			return m.end(EndReboot)
		}
		st.Overlay = NoOverlay
		m.pause(false)
	}
	return nil
}

func (m *Model) logKey(key string) {
	st := &m.st
	n := len(m.f.All())
	if st.Scene == Quick {
		n = m.player.At(st.T).Shown
	}
	n += logExtra(m.f.Startup, &m.st)
	rows := 1
	if m.gr != nil {
		rows = logRows(layout(Box{Y: 3, H: m.gr.H - 6}, m.o.Plain))
	}
	st.LogTop = max(0, min(st.LogTop, n-rows))
	switch key {
	case "up", "k":
		st.LogTop = max(0, st.LogTop-1)
	case "down", "j":
		st.LogTop = max(0, min(n-rows, st.LogTop+1))
	case "pgup":
		st.LogTop = max(0, st.LogTop-10)
	case "pgdown":
		st.LogTop = max(0, min(n-rows, st.LogTop+10))
	case "l", "L", "esc":
		st.Overlay = NoOverlay
	}
}

// ── Hello and Which Mac ────────────────────────────────────────────────────────

func (m *Model) helloKey(key string) tea.Cmd {
	st := &m.st
	if st.T <= helloReady {
		return nil
	}
	if !listKey(key, helloActions(&m.o, m.f), &st.HelloSel) {
		return nil
	}
	switch {
	case m.f.Fatal != "":
		return m.end(EndRefused)
	case st.HelloSel == 1:
		return m.end(EndInstall) // OmaBoot? Omarchy: Go to the installer
	}
	m.goTo(WhichMac)
	return nil
}

func (m *Model) macKey(key string) {
	st, f := &m.st, m.f
	if list := pickList(f, st); list != nil {
		switch key {
		case "backspace":
			if st.Picking {
				st.Picking = false
			} else {
				m.goTo(Hello)
			}
		case "up", "k":
			st.PickSel = max(0, st.PickSel-1)
		case "down", "j":
			st.PickSel = min(len(list)-1, st.PickSel+1)
		case "enter":
			c := list[st.PickSel]
			m.be.Pick(c.ID)
			st.Picked, st.PickedID, st.Picking, st.MacSel = true, c.ID, false, 0
			f.MatchNote = c.Note
			if c.ID != "" {
				f.MatchNote = i18n.T("Picked from the list.")
			}
			if f.OtherCopy != nil && f.Resume == nil && !st.Answered { // another version's copy, named: picking answers it too (0.7.5 decision 3)
				st.Answered = true
				m.be.Resume(false)
			}
		}
		return
	}
	if key == "backspace" {
		m.goTo(Hello)
		return
	}
	as, what := macActions(f, st)
	if st.T <= macReady || !listKey(key, as, &st.MacSel) {
		return
	}
	switch what[st.MacSel] {
	case macPick:
		st.Picking, st.PickSel = true, 0
		return
	case macResume, macFresh: // answered once: the copy isn't offered again
		st.Resumed, st.Answered = what[st.MacSel] == macResume, true
		m.be.Resume(st.Resumed)
		if !st.Resumed {
			f.Resume = nil
		}
		st.MacSel = 0
	default:
		if f.OtherCopy != nil && !st.Answered { // another version's copy, named: its note is set aside
			st.Answered = true
			m.be.Resume(false)
		}
	}
	m.goTo(Network)
	m.be.NetStart()
}

// ── Network ────────────────────────────────────────────────────────────────────

func (m *Model) networkKey(k tea.KeyMsg) tea.Cmd {
	st := &m.st
	key := k.String()
	switch {
	case st.Form.Open:
		m.staticKey(k)
		return nil
	case st.Pw:
		m.pwKey(k)
		return nil
	case netBusy(st.Net):
		return nil
	}
	if st.LineSel >= 0 { // the cursor is on the found lines
		switch key {
		case "up", "k", "down", "j", "enter":
			m.lineKey(key)
			return nil
		}
		st.LineSel = -1 // any other key works the view below as usual
	}
	if netUpView(st) {
		if key == "backspace" {
			m.leaveNetwork()
			return nil
		}
		acts := upActions(st)
		if (key == "up" || key == "k") && step(acts, firstOn(acts, st.UpSel), -1) == firstOn(acts, st.UpSel) {
			st.LineSel = lineStep(st, LineWiFi+1, -1) // ↑ from the first action runs up the lines
			return nil
		}
		if !listKey(key, acts, &st.UpSel) {
			return nil
		}
		wifi := st.Net.Via == "Wi-Fi"
		switch {
		case st.UpSel == 0:
			m.netContinue()
		case wifi && st.UpSel == 1: // Use another network
			m.showNetworks()
		case wifi || st.UpSel == 1: // Set static IP address
			m.openStatic()
		default: // Skip, test offline
			m.skipOffline()
		}
		return nil
	}
	// the list view: one cursor over the networks, then the actions
	if key == "backspace" {
		if st.Browse {
			st.Browse = false
		} else {
			m.leaveNetwork()
		}
		return nil
	}
	nets := st.Net.Networks
	acts := listActions(st)
	if key == "tab" || key == "shift+tab" { // between the networks and the actions: a long list needn't be scrolled through
		switch {
		case st.NetSel >= len(nets):
			if len(nets) > 0 {
				st.NetSel = min(st.NetPick, len(nets)-1)
			}
		default:
			st.NetSel = len(nets) + firstOn(acts, st.NetAct)
		}
		return nil
	}
	items := make([]Action, 0, len(nets)+len(acts))
	for _, n := range nets {
		items = append(items, Action{Label: n.SSID})
	}
	items = append(items, acts...)
	if (key == "up" || key == "k") && step(items, firstOn(items, st.NetSel), -1) == firstOn(items, st.NetSel) {
		st.LineSel = lineStep(st, LineWiFi+1, -1) // ↑ from the top runs up the lines
		return nil
	}
	enter := listKey(key, items, &st.NetSel)
	if st.NetSel < len(nets) {
		st.NetPick = st.NetSel
	} else {
		st.NetAct = st.NetSel - len(nets)
	}
	if !enter {
		return nil
	}
	if st.NetSel < len(nets) {
		m.join(st.NetSel)
		return nil
	}
	switch st.NetSel - len(nets) {
	case 0:
		m.join(st.NetPick)
	case 1:
		m.be.NetScan()
	case 2:
		m.openStatic()
	case 3:
		m.skipOffline()
	}
	return nil
}

// lineKey is ↑↓ and Enter while the cursor is on the found lines: ↓ past the last one goes on
// to the networks or the actions.
func (m *Model) lineKey(key string) {
	st := &m.st
	switch key {
	case "up", "k":
		if i := lineStep(st, st.LineSel, -1); i >= 0 {
			st.LineSel = i
		}
	case "down", "j":
		if i := lineStep(st, st.LineSel, 1); i >= 0 {
			st.LineSel = i
			return
		}
		st.LineSel = -1
		if netUpView(st) {
			st.UpSel = firstOn(upActions(st), 0)
		} else {
			st.NetSel = 0
		}
	case "enter":
		m.lineEnter(st.LineSel)
	}
}

// lineEnter picks a found line: Wi-Fi shows its networks; a connection that's ready or off is
// switched to, asking first when another one carries the run.
func (m *Model) lineEnter(i int) {
	st := &m.st
	n := st.Net
	if i == LineWiFi {
		st.LineSel = -1
		if netUpView(st) {
			m.showNetworks()
		} else {
			st.NetSel = min(st.NetPick, max(0, len(n.Networks)-1))
		}
		return
	}
	via := lineVia[i]
	if n.Online && n.Via != via {
		st.Overlay, st.SwitchVia, st.SwitchSel = SwitchOverlay, via, 0
		return
	}
	st.LineSel = -1
	m.be.NetSwitch(via)
}

// showNetworks opens the Wi-Fi list while online (it's collapsed on a wired connection),
// scanning first if there's nothing in it yet.
func (m *Model) showNetworks() {
	st := &m.st
	st.Browse, st.NetSel, st.LineSel = true, 0, -1
	if len(st.Net.Networks) == 0 {
		m.be.NetScan()
	}
}

// join starts joining network i: an open one at once, else the password box first. Online
// through a cable or a phone, it asks first (SwitchOverlay): Wi-Fi then carries the run alone.
func (m *Model) join(i int) {
	st := &m.st
	if i < 0 || i >= len(st.Net.Networks) {
		return
	}
	if st.Net.Online && st.Net.Via != "Wi-Fi" {
		st.Overlay, st.SwitchVia, st.SwitchTo, st.SwitchSel = SwitchOverlay, "Wi-Fi", i, 0
		return
	}
	m.joinNow(i)
}

// joinNow joins network i. The list stays up (Browse) until it's joined, so a switch from a
// cable shows its progress there; the connected view follows (netMsg).
func (m *Model) joinNow(i int) {
	st := &m.st
	ap := st.Net.Networks[i]
	if st.Net.Err != "" {
		m.oldErr = st.Net.Err
	}
	st.NetPick, st.Net.SSID, st.Net.Err = i, ap.SSID, ""
	m.be.NetClear()
	if ap.Security == "open" {
		m.be.NetConnect(ap.SSID, "")
		return
	}
	m.closePw()
	st.Pw = true
}

func (m *Model) switchKey(key string) {
	st := &m.st
	if key == "esc" || key == "backspace" {
		st.Overlay = NoOverlay
		return
	}
	if !listKey(key, switchActions(st), &st.SwitchSel) {
		return
	}
	st.Overlay = NoOverlay
	switch {
	case st.SwitchSel != 0:
	case st.SwitchVia != "Wi-Fi":
		st.LineSel, st.Browse, st.UpSel = -1, false, 0
		m.be.NetSwitch(st.SwitchVia)
	case st.SwitchTo < len(st.Net.Networks):
		m.joinNow(st.SwitchTo)
	}
}

// pwKey types the password, as the Wi-Fi test's box does: Enter joins, Esc goes back to the
// list, Tab shows or hides it.
func (m *Model) pwKey(k tea.KeyMsg) {
	st := &m.st
	if netJoining(st.Net) {
		return
	}
	switch k.String() {
	case "enter":
		if len(m.password) == 0 {
			return
		}
		m.be.NetConnect(st.Net.SSID, string(m.password))
		m.clearPassword()
	case "esc":
		m.closePw()
		return
	case "tab":
		st.PwShow = !st.PwShow
	case "backspace":
		if len(m.password) > 0 {
			m.password = m.password[:len(m.password)-1]
		}
	default:
		switch k.Type {
		case tea.KeyRunes:
			m.password = append(m.password, k.Runes...)
		case tea.KeySpace:
			m.password = append(m.password, ' ')
		}
		m.password = m.password[:min(len(m.password), 63)] // WPA's longest passphrase
	}
	st.PwLen, st.PwShown = len(m.password), nil
	if st.PwShow {
		st.PwShown = m.password
	}
}

// clearPassword zeroes the password typed and lets it go.
func (m *Model) clearPassword() {
	for i := range m.password {
		m.password[i] = 0
	}
	m.password, m.st.PwShown = nil, nil
}

// closePw takes the password box down (back to the list), the password with it.
func (m *Model) closePw() {
	st := &m.st
	m.clearPassword()
	st.Pw, st.PwLen, st.PwShow = false, 0, false
}

func (m *Model) openStatic() {
	st := &m.st
	st.Form.Open, st.Form.Focus, st.Form.Err, st.Form.Requested = true, 0, "", false
	m.f.Static = m.be.StaticTarget()
}

// staticRune reports whether r can be typed into the static address fields.
func staticRune(r rune, field int) bool {
	return r >= '0' && r <= '9' || r == '.' || field == 0 && r == '/' || field == 2 && (r == ' ' || r == ',')
}

func (m *Model) staticKey(k tea.KeyMsg) {
	st := &m.st
	fm := &st.Form
	key := k.String()
	if st.Net.Phase == NetApplying {
		return
	}
	done := staticDone(st)
	if fm.Focus < 3 && !done {
		fld := &fm.Fields[fm.Focus]
		switch key {
		case "enter", "tab", "down":
			fm.Focus++
		case "shift+tab", "up":
			fm.Focus = max(0, fm.Focus-1)
		case "backspace":
			if len(*fld) > 0 {
				*fld = (*fld)[:len(*fld)-1]
			}
		default:
			if (k.Type == tea.KeyRunes || k.Type == tea.KeySpace) && len(*fld) < 40 {
				for _, r := range k.Runes {
					if staticRune(r, fm.Focus) {
						*fld = append(*fld, r)
					}
				}
				if k.Type == tea.KeySpace && fm.Focus == 2 {
					*fld = append(*fld, ' ')
				}
			}
		}
		return
	}
	acts := staticActions(st, m.f.Static)
	sel := max(0, fm.Focus-3)
	switch key {
	case "backspace":
		fm.Open = false
		return
	case "tab":
		if !done {
			fm.Focus = 0
		}
		return
	case "up", "k":
		if sel == 0 && !done {
			fm.Focus = 2
			return
		}
	}
	if !listKey(key, acts, &sel) {
		fm.Focus = sel + 3
		return
	}
	fm.Focus = sel + 3
	switch {
	case done && sel == 0:
		fm.Open = false
		m.netContinue()
	case done:
		fm.Requested, fm.Focus = false, 0 // Change the settings
	case sel == 0: // Apply
		_, err := network.ParseStatic(string(fm.Fields[0]), string(fm.Fields[1]), string(fm.Fields[2]))
		var es *network.ErrStatic
		if errors.As(err, &es) {
			fm.Err, fm.Focus = es.Msg, es.Field
			return
		}
		fm.Err, fm.Requested = "", true
		st.Net.Err = ""
		m.be.NetStatic(m.f.Static.Iface, string(fm.Fields[0]), string(fm.Fields[1]), string(fm.Fields[2]))
	default: // Cancel
		fm.Open = false
	}
}

// leaveNetwork goes back to Which Mac; a connection stays up.
func (m *Model) leaveNetwork() {
	m.be.NetStop()
	m.goTo(WhichMac)
}

// netContinue leaves the step forward: the quick pass, or back to the results once it has run.
func (m *Model) netContinue() {
	m.be.NetStop()
	if m.st.PassDone {
		m.goTo(Results)
		return
	}
	if r := m.f.Resume; m.st.Resumed && r != nil && len(r.Quick) > 0 { // picked up: the copy's quick pass, no new one
		m.restoreQuick(r)
		return
	}
	total, expect := m.be.Quick()
	m.player = NewPlayer(nil, total)
	m.st.Remaining, m.st.RemainingAt = expect, 0
	m.st.GPU = GPUNotice{At: -1, End: -1}
	m.goTo(Quick)
}

// restoreQuick takes the quick pass's results from the copy picked up (0.7.5; picking up doesn't
// run the pass again) and goes on to the hands-on tests: "Boot
// needs you", then the test the Mac didn't come back from (failed) or the one after it.
func (m *Model) restoreQuick(r *Resume) {
	m.f.Checks = m.f.Checks[:0]
	for _, x := range r.Quick {
		m.f.Checks = append(m.f.Checks, CheckOf(x))
	}
	if r.QuickIn != "" {
		m.f.QuickPassIn = r.QuickIn
	}
	m.player = NewPlayer(m.f.Checks, len(m.f.Checks))
	m.st.PassDone = true
	m.keepGoing()
}

func (m *Model) skipOffline() {
	m.st.Offline = true
	m.netContinue()
}

// ── Quick results and Finish ───────────────────────────────────────────────────

func (m *Model) resultsKey(key string) tea.Cmd {
	st := &m.st
	if key == "backspace" {
		st.UpSel, st.NetSel, st.LineSel, st.Browse, st.Form.Open = 0, 0, -1, false, false
		m.goTo(Network)
		m.be.NetStart()
		return nil
	}
	acts := resultsActions(st)
	if st.T <= resultsReady || !listKey(key, acts, &st.ResultsSel) {
		return nil
	}
	switch st.ResultsSel {
	case 0:
		m.keepGoing()
	case 1:
		m.goTo(Finish)
	case 2:
		return m.askQuit()
	}
	return nil
}

func (m *Model) finishKey(k tea.KeyMsg) tea.Cmd {
	st, f := &m.st, m.f
	key := k.String()
	if sending(st, f) || st.Saving {
		return nil
	}
	if st.Submitting {
		m.submitKey(k)
		return nil
	}
	if st.Guard {
		m.guardKey(key)
		return nil
	}
	if st.BugForm {
		m.bugFormKey(key)
		return nil
	}
	if key == "backspace" {
		if finishBack(st, f) {
			m.goTo(Results)
		}
		return nil
	}
	if f.Report != "" && st.T-sentAt(st) <= 2.6 || f.Report == "" && st.T <= finishReady {
		return nil
	}
	acts := finishActions(&m.o, st, f)
	if !listKey(key, acts, &st.FinishSel) {
		return nil
	}
	i := st.FinishSel
	if m.o.Edition.Installer {
		if i == 0 {
			return m.end(EndInstall)
		}
		i--
	}
	switch i {
	case 0: // Submit report; a run changed in the console gets the amber box instead
		if st.Con.Marked() {
			st.Guard, st.GuardSel = true, 0
			return nil
		}
		st.Submitting, st.SubFocus, st.SubAt, st.SendErr, st.SendErrAt, st.Problems = true, 0, st.T, "", 0, nil
	case 1:
		st.Saving, st.SaveErr = true, ""
		m.be.Save(m.handle(), ConsentNotice(&m.o, f))
	case 2:
		m.goTo(Report)
	case 3, 4: // Shutdown, Restart: asked first while the report is neither sent nor saved
		what := EndPoweroff
		if i == 4 {
			what = EndReboot
		}
		if !finished(f) {
			st.Overlay, st.EndWhat, st.EndSel = EndOverlay, what, 0
			return nil
		}
		return m.end(what)
	case 5: // Send a bug report (PF-13)
		st.BugForm, st.BugSel = true, 0
	}
	return nil
}

// bugFormKey works Send a bug report: send it (saved to external storage first, then sent,
// scrubbed), or go back.
func (m *Model) bugFormKey(key string) {
	st := &m.st
	if st.BugSending {
		return
	}
	if key == "esc" || key == "backspace" {
		st.BugForm = false
		return
	}
	acts := bugActions(st)
	if !listKey(key, acts, &st.BugSel) {
		return
	}
	if len(acts) == 1 || st.BugSel == 1 || m.bugging {
		st.BugForm = false
		return
	}
	st.BugForm = false // the screen it keeps is Finish's, not this form
	screen := m.screenText()
	st.BugForm, st.BugSending, st.BugNote = true, true, ""
	m.bugging, m.bugSend, m.bugFinish = true, true, true
	m.be.Bugreport(BugFiles{Screen: screen, Log: fullLogText(m.f, st), Handle: m.handle(), Notice: ConsentNotice(&m.o, m.f)})
}

// guardKey works the "Changed in the console" box: send to staging (never to DoesItOmarchy.com),
// save a copy to the stick, or go back.
func (m *Model) guardKey(key string) {
	st, f := &m.st, m.f
	if key == "esc" || key == "backspace" {
		st.Guard = false
		return
	}
	acts := guardActions(st, f)
	if !listKey(key, acts, &st.GuardSel) {
		return
	}
	i := st.GuardSel
	if f.Production {
		i++ // no Send to staging
	}
	st.Guard = false
	switch i {
	case 0:
		st.Submitting, st.SubFocus, st.SubAt, st.Problems = true, 1, st.T, nil
		st.SendAt, st.SentAt, st.SendErr = st.T, -1, ""
		m.be.Submit(m.handle(), ConsentNotice(&m.o, f))
	case 1:
		st.FinishSel, st.Saving, st.SaveErr = 1, true, ""
		if m.o.Edition.Installer {
			st.FinishSel = 2
		}
		m.be.Save(m.handle(), ConsentNotice(&m.o, f))
	}
}

// endKey answers EndOverlay: go back (the default; Esc too), or shut down or restart anyway.
func (m *Model) endKey(key string) tea.Cmd {
	st := &m.st
	if key == "esc" {
		st.Overlay = NoOverlay
		return nil
	}
	if listKey(key, endActions(st.EndWhat), &st.EndSel) {
		st.Overlay = NoOverlay
		if st.EndSel == 1 {
			return m.end(st.EndWhat)
		}
	}
	return nil
}

func (m *Model) submitKey(k tea.KeyMsg) {
	st, f := &m.st, m.f
	key := k.String()
	if st.SubFocus == 0 {
		switch key {
		case "enter", "tab", "down":
			st.SubFocus = 1
		case "backspace":
			if len(st.Name) > 0 {
				st.Name = st.Name[:len(st.Name)-1]
			}
		default:
			if len(st.Name) < 39 {
				switch k.Type {
				case tea.KeyRunes:
					for _, r := range k.Runes {
						if handleRune(r, len(st.Name) == 0) {
							st.Name = append(st.Name, r)
						}
					}
				case tea.KeySpace:
					if len(st.Name) > 0 {
						st.Name = append(st.Name, '_')
					}
				}
			}
		}
		return
	}
	switch key {
	case "tab":
		st.SubFocus = 0
		return
	case "up", "k":
		if st.SubFocus == 1 {
			st.SubFocus = 0
			return
		}
	}
	sel := st.SubFocus - 1
	enter := listKey(key, submitActions(), &sel)
	st.SubFocus = sel + 1
	if !enter {
		return
	}
	if sel == 1 { // Don't send report
		st.Submitting = false
		return
	}
	if f.Report != "" { // one report per run
		st.Submitting = false
		return
	}
	st.SendAt, st.SentAt, st.SendErr, st.Problems = st.T, -1, "", nil
	m.be.Submit(m.handle(), ConsentNotice(&m.o, f))
}

// handleRune: the name to credit takes what the site accepts in a handle: letters, digits,
// '.', '_' and '-', starting with a letter or digit (a space types '_').
func handleRune(r rune, first bool) bool {
	alnum := r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9'
	return alnum || !first && (r == '.' || r == '_' || r == '-')
}
