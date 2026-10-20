package ui

import (
	"fmt"
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/omaboot/boot-live/internal/checks"
	"github.com/omaboot/boot-live/internal/command"
	g "github.com/omaboot/boot-live/internal/grid"
	"github.com/omaboot/boot-live/internal/handson"
	"github.com/omaboot/boot-live/internal/i18n"
	"github.com/omaboot/boot-live/internal/pace"
	"github.com/omaboot/boot-live/internal/plan"
)

// The run plan (internal/plan, LIVE-PLAN §30) as the screens drive it: the frame clock answers
// each screen's question the way the plan says, once, and a person can still take over. Hello
// counts down 10 s, and any key in that time ignores the plan for this boot. Which Mac takes the
// plan's configuration (else the match; when several fit and nobody is there, the tool's best);
// a copy from a run that didn't come back is picked up; Network joins the plan's way (falling back to offline when nobody is there); the GPU notice
// goes at once when nobody is there; Quick results and Boot needs you go on by themselves;
// skipped tests are recorded without opening, setup tests go on 3 s after deciding (or are
// not tested after 2× their time, at least 60 s), hands-on tests wait for the person; Finish
// sends and/or saves, writes one line to the serial port and does the plan's finish. Bug
// reports go to the stick at the plan's moments. A plan with start: console waits on Hello
// instead of counting down; the console's plan run starts it (or one ignored at Hello, or
// stopped) from the screen the run is on, and plan stop hands the run back to the person.

// planRun is the plan this run follows, and how far the screens got with it.
type planRun struct {
	r       *plan.Run
	off     bool // any key during Hello's countdown: ignored for this boot
	wait    bool // start: console, not started yet
	stopped bool // the console's plan stop

	macDone, netDone, resultsDone, attDone, counted bool

	netAt, joinAt, rescanAt float64 // the Network step's clock: when it began, the join, the rescan (-1: not yet)
	joined, rescanned       bool

	decided  map[string]bool // the tests whose result the plan has looked at
	problems map[string]bool // the on-problem bug reports saved, by kind
	bugs     int             // bug reports asked for, waiting for the last one to be saved
	quickBug bool

	finish   int // Finish: 0 not started, 1 sent (or not), 2 saved, 3 bug report, 4 done, 5 nothing left
	finishAt float64
}

const (
	planHello    = 10.0 // Hello's countdown before the plan starts
	planGo       = 3.0  // how long a setup or skipped test's result shows before the next
	planNetWait  = 30.0 // how long the Network step waits for a cable, a phone or whatever comes up
	planJoinWait = 60.0 // how long a Wi-Fi join may take
)

// newPlanRun is the plan from the stick, or nil when there's none to run.
func newPlanRun(f *Facts) *planRun {
	if f.Plan == nil || f.Plan.Run == nil || f.Fatal != "" {
		return nil
	}
	return &planRun{r: f.Plan.Run, wait: f.Plan.Run.Start == "console", netAt: -1, joinAt: -1, rescanAt: -1, decided: map[string]bool{}, problems: map[string]bool{}}
}

// activePlan is the plan in use, or nil.
func (m *Model) activePlan() *planRun {
	if m.pl == nil || m.pl.off || m.pl.wait || m.pl.stopped {
		return nil
	}
	return m.pl
}

// planKey takes a key during Hello's countdown: the plan is ignored. It reports whether it did.
func (m *Model) planKey() bool {
	p := m.activePlan()
	if p == nil || m.st.Scene != Hello || m.st.T >= planHello {
		return false
	}
	p.off, m.st.PlanOff = true, true
	m.be.PlanOff()
	return true
}

// planClock moves the run on as the plan says; a tea.Cmd when it ends the run (finish).
func (m *Model) planClock() tea.Cmd {
	p := m.activePlan()
	st := &m.st
	if p == nil {
		return nil
	}
	if p.bugs > 0 && !m.bugging {
		p.bugs--
		m.bugging, m.bugSend = true, p.r.BugSend && m.f.CanSendBug
		m.be.Bugreport(BugFiles{Screen: m.screenText(), Log: fullLogText(m.f, st), Handle: m.handle(), Notice: ConsentNotice(&m.o, m.f)})
	}
	if st.Overlay != NoOverlay || conOpen(st) || st.Sounds { // a person is at it
		return nil
	}
	switch st.Scene {
	case Hello:
		if st.T >= planHello {
			m.goTo(WhichMac)
		}
	case WhichMac:
		if st.T > macReady && !p.macDone {
			m.planMac(p)
		}
	case Network:
		if !p.netDone && !st.Pw && !st.Form.Open {
			m.planNet(p)
		}
	case Quick:
		if p.r.Unattended() && st.GPU.Waiting && st.GPU.At >= 0 {
			m.gpuGo() // nobody to read it
		}
	case Results:
		if st.T > resultsReady && !p.resultsDone {
			p.resultsDone = true
			if p.r.Bugreport[plan.AfterQuick] && !p.quickBug {
				p.quickBug = true
				p.bugs++
			}
			for _, c := range m.f.Checks { // a quick-pass check that failed is a problem too (the after-quick one covers it)
				if c.Status == pace.Fail && !p.r.Bugreport[plan.AfterQuick] {
					m.planProblem(p, "failed")
					break
				}
			}
			m.keepGoing()
		}
	case Attention:
		if st.T > attentionReady && !p.attDone && st.HO != nil {
			p.attDone = true
			m.planCounts(p)
			m.planNext(st.HO.Resume())
		}
	case Test:
		m.planTest(p)
	case Finish:
		return m.planFinish(p)
	}
	return nil
}

// planBug asks for a bug report on a problem of this kind, once per kind.
func (m *Model) planProblem(p *planRun, kind string) {
	if p.r.Bugreport[plan.OnProblem] && !p.problems[kind] {
		p.problems[kind] = true
		p.bugs++
	}
}

// planMac answers Which Mac: a copy from a run that didn't come back is picked up; then the
// configuration.
func (m *Model) planMac(p *planRun) {
	st, f := &m.st, m.f
	p.macDone = true
	switch {
	case st.Answered:
	case f.Resume != nil: // a run of this Mac that didn't come back: picked up, so its fail stays on record
		st.Resumed, st.Answered = true, true
		m.be.Resume(true)
		m.be.PlanNote("event", "picked up the copy of a run that didn't come back")
	case f.OtherCopy != nil: // another version's copy: set aside, as a pick would
		st.Answered = true
		m.be.Resume(false)
	}
	if st.Picked { // picked by hand before the plan started (plan run): the pick stays
		if p.r.Configuration != "" && !strings.EqualFold(st.PickedID, p.r.Configuration) {
			m.be.PlanNote("event", "the configuration "+st.PickedID+" was picked by hand before the plan started, not the plan's "+p.r.Configuration)
		}
		m.goTo(Network)
		m.be.NetStart()
		return
	}
	if list := pickList(f, st); list != nil {
		id := ""
		for _, c := range list {
			if c.ID != "" && strings.EqualFold(c.ID, p.r.Configuration) {
				id = c.ID
			}
		}
		switch {
		case id != "":
			m.be.PlanNote("event", "the configuration was named by the plan")
		case p.r.Unattended() && f.Plan.Best != "":
			id = f.Plan.Best
			m.be.PlanNote("event", fmt.Sprintf("the configuration is the tool's best match of %d candidates: the plan named none of them", len(list)-1))
			m.planProblem(p, "mac")
		default:
			return // a person picks it; the plan goes on at the Network step
		}
		m.be.PlanNote("config", id)
		st.Picked, st.PickedID, st.MacSel = true, id, 0
		f.MatchNote = i18n.T("Picked by the run plan.")
	} else if p.r.Configuration != "" && f.Plan.Best != "" && !strings.EqualFold(p.r.Configuration, f.Plan.Best) {
		m.be.PlanNote("event", "the plan's configuration "+p.r.Configuration+" isn't this Mac's match "+f.Plan.Best+": the match was used")
	}
	m.goTo(Network)
	m.be.NetStart()
}

// planNet connects the plan's way, then goes on to the quick pass.
func (m *Model) planNet(p *planRun) {
	st := &m.st
	if p.netAt < 0 {
		p.netAt = st.T
	}
	el := st.T - p.netAt
	n := p.r.Network
	unattended := p.r.Unattended()
	fail := func(why string) {
		p.netDone = true
		m.be.PlanNote("event", why)
		m.planProblem(p, "network")
		switch {
		case !unattended: // the person takes the Network screen from here
		case st.Net.Online && !netBusy(st.Net):
			m.netContinue()
		default:
			m.skipOffline()
		}
	}
	switch n.Kind {
	case "offline":
		p.netDone = true
		m.skipOffline()
	case "wifi":
		if st.Net.Online && st.Net.Via == "Wi-Fi" && st.Net.SSID == n.SSID && !netBusy(st.Net) {
			p.netDone = true
			m.netContinue()
			return
		}
		if !p.joined {
			scanned := len(st.Net.Networks) > 0 && st.Net.Phase != NetScanning
			if !scanned {
				if el > planNetWait {
					fail("the plan's Wi-Fi network: no scan in 30 s")
				}
				return
			}
			i := -1
			for k, ap := range st.Net.Networks {
				if ap.SSID == n.SSID {
					i = k
				}
			}
			switch {
			case i < 0 && !p.rescanned:
				p.rescanned, p.rescanAt = true, el
				m.be.NetScan()
				return
			case i < 0 && el-p.rescanAt > 4:
				fail("the plan's Wi-Fi network wasn't in the scan")
				return
			case i < 0:
				return
			}
			p.joined, p.joinAt = true, el
			st.NetPick, st.Net.SSID, st.Net.Err = i, st.Net.Networks[i].SSID, ""
			m.be.NetClear()
			m.be.NetConnect(st.Net.Networks[i].SSID, n.Password)
			return
		}
		switch {
		case st.Net.Err != "" && !netBusy(st.Net):
			fail("the plan's Wi-Fi network didn't join")
		case el-p.joinAt > planJoinWait:
			fail(fmt.Sprintf("the plan's Wi-Fi network didn't come up in %.0f s", planJoinWait))
		}
	default: // ethernet, phone, or whatever comes up by itself
		if st.Net.Online && !netBusy(st.Net) {
			p.netDone = true
			m.netContinue()
			return
		}
		if el <= planNetWait {
			return
		}
		if n.Kind == "" { // nothing came up by itself: no problem, just offline (attended: the person's choice)
			p.netDone = true
			if unattended {
				m.be.PlanNote("event", "no network came up by itself: offline")
				m.skipOffline()
			}
			return
		}
		fail(fmt.Sprintf("the plan's network (%s) didn't come up in %.0f s", n.Kind, planNetWait))
	}
}

// planCounts tells the backend how many tests run in each mode, for the report.
func (m *Model) planCounts(p *planRun) {
	if p.counted || m.st.HO == nil {
		return
	}
	p.counted = true
	n := map[string]int{}
	for _, s := range m.st.HO.Setup.Plan {
		n[p.r.Mode(s.ID)]++
	}
	var parts []string
	for _, mode := range []string{plan.Setup, plan.HandsOn, plan.Skip} {
		if n[mode] > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", n[mode], modeWord[mode]))
		}
	}
	m.be.PlanNote("counts", strings.Join(parts, ", "))
}

// modeWord is a mode as the report's note counts it.
var modeWord = map[string]string{plan.Setup: "setup", plan.HandsOn: "hands-on", plan.Skip: "skipped"}

// planNext opens test i or the first after it the plan doesn't skip, recording the skipped
// ones; Finish after the last.
func (m *Model) planNext(i int) {
	h := m.st.HO
	p := m.activePlan()
	skipped := false
	for ; i < len(h.Setup.Plan); i++ {
		s := h.Setup.Plan[i]
		if p == nil || p.r.Mode(s.ID) != plan.Skip {
			break
		}
		if _, done := h.Results[s.ID]; !done {
			h.Results[s.ID] = handson.PlanSkipped(h.Setup.Env, s)
			skipped = true
		}
	}
	if skipped {
		m.recordHandsOn()
	}
	if i < len(h.Setup.Plan) {
		m.openTest(i)
		return
	}
	m.endHandsOn()
}

// planTest watches the test on screen: a decided one goes on (setup, after planGo), a setup
// one that takes too long is not tested.
func (m *Model) planTest(p *planRun) {
	h := m.st.HO
	if h == nil || h.Run == nil || h.Paused || h.Keeping {
		return
	}
	id := h.Run.Spec.ID
	mode := p.r.Mode(id)
	if h.Out != nil {
		if !p.decided[id] {
			p.decided[id] = true
			for _, x := range h.Out.Results {
				if x.Status == checks.Failed {
					m.be.PlanNote("event", id+" failed")
					m.planProblem(p, "failed")
					break
				}
			}
		}
		if mode != plan.HandsOn && m.timeIn(Test)-h.At-h.OutAt >= planGo { // the run's own clock stops once it has decided
			m.planNext(h.Cur + 1)
		}
		return
	}
	if mode != plan.Setup {
		return
	}
	lim := max(2*h.Run.Spec.Expect.Seconds(), 60)
	if h.Run.Ctx.T >= lim {
		h.Run.Force(handson.PlanTimeout(h.Setup.Env, h.Run.Spec, lim))
		if h.take() {
			m.recordHandsOn()
		}
		m.be.PlanNote("event", fmt.Sprintf("%s didn't decide in %.0f s", id, lim))
		m.planProblem(p, "setup")
	}
}

// planFinish sends and/or saves the report, saves the last bug report, writes the serial line,
// then does the plan's finish.
func (m *Model) planFinish(p *planRun) tea.Cmd {
	st, f := &m.st, m.f
	if st.T <= finishReady || sending(st, f) || st.Saving || st.Submitting || st.Guard {
		return nil
	}
	switch p.finish {
	case 0:
		p.finish = 1
		st.Name = []rune(p.r.Tester)
		if p.r.Results == "save" {
			return nil
		}
		switch {
		case !f.CanSubmit:
			m.be.PlanNote("event", "not sent: this image has no report key")
		case st.Con.Marked():
			m.be.PlanNote("event", "not sent: the run was changed in the console")
		case f.Report == "":
			st.SendAt, st.SentAt, st.SendErr, st.Problems = st.T, -1, "", nil
			m.be.Submit(m.handle(), planConsent(&m.o, f))
		}
	case 1:
		p.finish = 2
		if (p.r.Results != "send" || f.Report == "") && f.SavedFile == "" { // saved as asked, or when it didn't go (not again: plan run at Finish)
			if p.r.Results == "send" {
				m.be.PlanNote("event", "the report didn't go, so it was saved instead")
			}
			st.Saving, st.SaveErr = true, ""
			m.be.Save(m.handle(), planConsent(&m.o, f))
		}
	case 2:
		p.finish = 3
		if p.r.Bugreport[plan.AtFinish] {
			p.bugs++
		}
	case 3:
		if m.bugging || p.bugs > 0 {
			return nil
		}
		p.finish, p.finishAt = 4, st.T
		m.be.PlanSerial(planLine(f))
	case 4:
		if p.r.Finish == "wait" {
			p.finish = 5
			return nil
		}
		if h := st.HO; h != nil && h.Setup.Env != nil && len(h.Setup.Env.Hung) > 0 {
			p.finish = 5 // a sleep that hung: stay up so the screen can be read
			return nil
		}
		if st.T-p.finishAt >= 5 {
			p.finish = 5
			if p.r.Finish == "reboot" {
				return m.end(EndReboot)
			}
			return m.end(EndPoweroff)
		}
	}
	return nil
}

// planLine is the serial port's last line: what became of the report, and the results' tally.
func planLine(f *Facts) string {
	what := "nothing kept"
	switch {
	case f.Report != "" && f.SavedFile != "":
		what = "submitted " + f.Report + ", saved " + f.SavedFile
	case f.Report != "":
		what = "submitted " + f.Report
	case f.SavedFile != "":
		what = "saved " + f.SavedFile
	}
	words := map[pace.Status]string{pace.Skip: "not tested", pace.Pass: "passed", pace.Live: "need Omarchy installed", pace.Partial: "partial", pace.Fail: "failed"}
	n := map[string]int{}
	for _, c := range append(append([]Check(nil), f.Checks...), f.HandsOn...) {
		n[words[c.Status]]++
	}
	var keys []string
	for k := range n {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var parts []string
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s %d", k, n[k]))
	}
	return what + "; " + strings.Join(parts, ", ")
}

// planHelloLines are Hello's notice about the plan: what it will do and the countdown, or why
// it isn't used (ok false); nil without a plan.
func planHelloLines(f *Facts, st *State) (lines []string, ok bool) {
	pf := f.Plan
	if pf == nil || f.Fatal != "" {
		return nil, false
	}
	switch {
	case len(pf.Problems) > 0:
		return append([]string{i18n.T("Run plan %s found, not used:", pf.File)}, pf.Problems...), false
	case pf.NoSection != "":
		return []string{i18n.T("Run plan %[1]s found, with no section for %[2]s: not used.", pf.File, pf.NoSection)}, false
	case st.PlanOff:
		return []string{i18n.T("Run plan ignored for this boot. plan run in the console starts it.")}, false
	}
	r := pf.Run
	how := i18n.T("unattended")
	if !r.Unattended() {
		how = i18n.T("with you at the hands-on tests")
	}
	var modes []string
	for _, mode := range []string{plan.Setup, plan.HandsOn, plan.Skip} {
		n := 0
		for _, x := range r.Tests {
			if x == mode {
				n++
			}
		}
		if n > 0 {
			modes = append(modes, fmt.Sprintf("%d %s", n, modeWord[mode]))
		}
	}
	tests := i18n.T("tests: %[1]s, the rest %[2]s", strings.Join(modes, ", "), modeWord[r.Default])
	if len(modes) == 0 {
		tests = i18n.T("tests: all %s", modeWord[r.Default])
	}
	report := map[string]string{"send": i18n.T("sent"), "save": i18n.T("saved"), "both": i18n.T("sent and saved")}[r.Results]
	finish := map[string]string{"wait": i18n.T("then the screen waits"), "power-off": i18n.T("then it powers off"), "reboot": i18n.T("then it restarts")}[r.Finish]
	lines = []string{i18n.T("Run plan %[1]s: %[2]s's section, %[3]s.", pf.File, r.Section, how),
		tests, i18n.T("network: %s", r.Network.Text()) + " · " + i18n.T("report %[1]s, %[2]s", report, finish)}
	if st.PlanWait {
		lines = append(lines, i18n.T("Waiting: type plan run in the console to start it."))
	} else if left := planHello - st.T; left > 0 {
		lines = append(lines, i18n.T("Starting in %d s. Press any key to ignore the plan.", int(left)+1))
	} else {
		lines = append(lines, i18n.T("Started."))
	}
	return lines, true
}

// drawPlanNotice is Hello's box about the run plan, at x, y, as wide as its longest line
// between minW and maxW: violet while it runs, amber when it isn't used.
func drawPlanNotice(gr *g.Grid, f *Facts, st *State, x, y, minW, maxW int) {
	lines, ok := planHelloLines(f, st)
	if len(lines) == 0 {
		return
	}
	w := minW
	for _, l := range lines {
		w = max(w, width(l)+6)
	}
	w = min(w, maxW)
	var rows []string // a long problem wraps, its next rows indented
	for _, l := range lines {
		for i, r := range wrap(l, w-8) {
			if i > 0 {
				r = "  " + r
			}
			rows = append(rows, r)
		}
	}
	col, mark := g.BVIO, "■"
	if !ok {
		col, mark = g.AMB, "!"
	}
	box(gr, x, y, w, len(rows)+2, col, i18n.T("Run plan"), col)
	for i, l := range rows {
		if i == 0 {
			gr.Put(x+2, y+1, mark, col)
		}
		gr.Put(x+4, y+1+i, cut(l, w-6), g.GRY)
	}
}

// planScreen names a screen for the report's note on where the plan started or stopped.
func (m *Model) planScreen() string {
	switch m.st.Scene {
	case Hello:
		return "Hello"
	case WhichMac:
		return "Which Mac"
	case Network:
		return "Network"
	case Quick:
		return "the quick pass"
	case Results:
		return "Quick results"
	case Attention:
		return "Boot needs you"
	case Test:
		if h := m.st.HO; h != nil && h.Run != nil {
			return "the " + h.Run.Spec.ID + " test"
		}
		return "a hands-on test"
	case Report:
		return "the report"
	}
	return "Finish"
}

// planStart is the console's plan run: the file on the stick read again and checked, then the
// plan drives the run from the screen it's on. Steps already behind stay as they were: tests
// decided keep their results, a configuration picked by hand stays (noted when it isn't the
// plan's), and so does the network. Its lines are for the console.
func (m *Model) planStart() ([]string, error) {
	st := &m.st
	if m.activePlan() != nil {
		return nil, command.Err(i18n.T("the run plan is already running (plan stop hands the run back)"))
	}
	if st.Gone {
		return nil, command.Err(i18n.T("the OmaBoot? drive came out: the plan can't be read"))
	}
	at := m.planScreen()
	pf, problems := m.be.PlanStart(at)
	if pf == nil {
		return problems, nil
	}
	prev := m.pl // a plan stopped earlier in this run: the steps it did are its own, not by hand
	m.f.Plan = pf
	m.pl = newPlanRun(m.f)
	m.pl.wait = false
	st.PlanOff, st.PlanWait = false, false
	p := m.pl
	switch st.Scene {
	case Hello, WhichMac:
	default:
		p.macDone = true
		if prev != nil && prev.macDone {
			break
		}
		if st.Picked && p.r.Configuration != "" && !strings.EqualFold(st.PickedID, p.r.Configuration) {
			m.be.PlanNote("event", "the configuration "+st.PickedID+" was picked by hand before the plan started, not the plan's "+p.r.Configuration)
		}
	}
	if st.Scene != Hello && st.Scene != WhichMac && st.Scene != Network {
		p.netDone = true
	}
	if p.netDone && (prev == nil || !prev.netDone) {
		how := "offline"
		if st.Net.Online {
			how = "online by " + or(st.Net.Via, "a network")
		}
		m.be.PlanNote("event", "the network was set by hand before the plan started ("+how+")")
	}
	if st.HO != nil && st.Scene != Attention {
		m.planCounts(p)
	}
	st.Con.Open = false // the plan needs the screen: it waits while the console is open
	how := i18n.T("unattended")
	if !p.r.Unattended() {
		how = i18n.T("with you at the hands-on tests")
	}
	return []string{i18n.T("Run plan started from %[1]s: %[2]s's section, %[3]s.", at, p.r.Section, how)}, nil
}

// planStop is the console's plan stop: the run is the person's from the screen it's on.
func (m *Model) planStop() ([]string, error) {
	p := m.activePlan()
	if p == nil {
		return nil, command.Err(i18n.T("no run plan is running"))
	}
	if m.st.Scene == Hello { // before it began: the same as ignoring it
		p.off, m.st.PlanOff = true, true
		m.be.PlanOff()
		return []string{i18n.T("Run plan ignored for this boot.")}, nil
	}
	p.stopped = true
	m.be.PlanStop(m.planScreen())
	return []string{i18n.T("Run plan stopped: the run is yours from this screen.")}, nil
}

func or(s, def string) string {
	if s == "" {
		return def
	}
	return s
}
