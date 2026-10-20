package ui

import (
	"slices"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/omaboot/boot-live/internal/checks"
	"github.com/omaboot/boot-live/internal/command"
	g "github.com/omaboot/boot-live/internal/grid"
	"github.com/omaboot/boot-live/internal/handson"
	"github.com/omaboot/boot-live/internal/i18n"
	"github.com/omaboot/boot-live/internal/identify"
	"github.com/omaboot/boot-live/internal/pace"
)

// conEnv is the console commands' way into the run (command.Env): the screens' facts, and the
// backend behind them.
type conEnv struct{ m *Model }

// Tests are the run's results, then its hands-on tests, each ID once.
func (e conEnv) Tests() []string {
	var out []string
	seen := map[string]bool{}
	add := func(id string) {
		if id != "" && !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	for _, c := range e.m.f.All() {
		add(c.ID)
	}
	if h := e.m.st.HO; h != nil {
		for _, s := range h.Setup.Plan {
			add(s.ID)
		}
	}
	return out
}

func (e conEnv) Result(id string) (command.Result, bool) {
	for _, c := range e.m.f.All() {
		if c.ID != id {
			continue
		}
		_, forced := e.m.f.Forced[id]
		return command.Result{ID: c.ID, Label: i18n.S(c.Label), Status: statusWord(c.Status), Detail: i18n.S(c.Detail), Evidence: c.Evidence,
			Method: c.Method, Reason: c.Reason, Fix: c.Fix, Forced: forced}, true
	}
	return command.Result{}, false
}

func (e conEnv) IDs() []command.ID { return e.m.be.IDs() }

func (e conEnv) Summary() command.Summary {
	f := e.m.f
	s := command.Summary{Pass: f.Count(pace.Pass), Partial: f.Count(pace.Partial), Fail: f.Count(pace.Fail), Live: f.Count(pace.Live),
		HandsOn: len(f.HandsOn), Console: e.m.recorded}
	switch {
	case f.Report != "":
		s.State = i18n.T("sent: %s", f.ReportPage)
	case f.SavedFile != "":
		s.State = i18n.T("not sent; a copy is on external storage: %s", f.SavedFile)
	default:
		s.State = i18n.T("not sent yet; Finish sends or saves it")
	}
	return s
}

func (e conEnv) ReportJSON() ([]byte, error) {
	return e.m.be.ReportJSON(e.m.handle(), ConsentNotice(&e.m.o, e.m.f))
}

// Log is the full log as text: every result so far, the Mac's startup, and the console itself.
func (e conEnv) Log() []string {
	var out []string
	for _, c := range e.m.f.All() {
		mk, _, word := mark(c.Status)
		out = append(out, strings.TrimSpace(mk+" "+c.ID+"  "+word+"  "+i18n.S(c.Detail)))
	}
	return append(out, e.m.f.Startup...)
}

func (e conEnv) KernelLog() []string { return e.m.be.KernelLog() }

func (e conEnv) Note(text string) error {
	e.m.be.Note(text)
	return nil
}

// Bugreport saves the full log, the kernel log, the report and the screen under the console as
// text to the stick; the answer comes as a BugMsg.
func (e conEnv) Plan(check bool) ([]string, error) { return e.m.be.PlanText(check) }
func (e conEnv) PlanRun() ([]string, error)        { return e.m.planStart() }
func (e conEnv) PlanStop() ([]string, error)       { return e.m.planStop() }

// SendBug sends this run's saved bug reports and the stick's waiting ones to OmaBoot?; the
// answer comes as a BugSentMsg each.
func (e conEnv) SendBug() error {
	if !e.m.f.CanSendBug {
		return command.Err(i18n.T("this image can't send bug reports: bugreport saves them to external storage"))
	}
	e.m.be.SendBug("")
	return nil
}

func (e conEnv) Bugreport() error {
	m := e.m
	if m.bugging {
		return command.Err(i18n.T("still saving the last one"))
	}
	m.bugging = true
	m.be.Bugreport(BugFiles{Screen: m.screenText(), Log: fullLogText(m.f, &m.st), Handle: m.handle(), Notice: ConsentNotice(&m.o, m.f)})
	return nil
}

// TestDrives are where testfile could write the test file, and where not.
func (e conEnv) TestDrives() []command.TestDrive { return e.m.be.TestDrives() }

// WriteTestFile writes the test file to a drive; the answer comes as a TestFileMsg.
// Sounds opens the sounds screen.
func (e conEnv) Sounds() error {
	e.m.openSounds()
	return nil
}

func (e conEnv) WriteTestFile(dev string) error {
	m := e.m
	if m.writing != "" {
		return command.Err(i18n.T("still writing to %s", m.writing))
	}
	m.writing = dev
	m.be.WriteTestFile(dev)
	return nil
}

// Rerun measures a test again (the new measurement decides, the earlier one goes into its
// evidence). A hands-on test whose result this run has (or one the quick pass doesn't
// measure) opens again, as its own Redo does; a quick-pass check runs again in the background.
func (e conEnv) Rerun(id string) (string, error) {
	m := e.m
	st := &m.st
	switch {
	case st.Gone:
		return "", command.Err(i18n.T("the OmaBoot? drive came out: nothing more is tested"))
	case !st.PassDone:
		return "", command.Err(i18n.T("once the quick pass is done"))
	case m.rerunning != "":
		return "", command.Err(i18n.T("one at a time: %s is still running", m.rerunning))
	}
	quick := false
	for _, c := range m.f.Checks {
		quick = quick || c.ID == id
	}
	if h := st.HO; h != nil {
		for i, s := range h.Setup.Plan {
			if s.ID != id {
				continue
			}
			o, done := h.Results[s.ID]
			if !done && quick {
				break // not done by hand yet: the quick pass's check runs again
			}
			if done && len(o.Results) > 0 {
				h.again(s.ID, o.Results[0], m.decidedIn(s.ID))
			}
			st.Con.Open = false // the test needs the screen
			m.openTest(i)
			return i18n.T("opened again: do it on the screen"), nil
		}
	}
	if !quick {
		return "", command.Err(i18n.T("no test %s in this run (Tab lists them)", id))
	}
	what, err := m.be.Rerun(id)
	if err != nil {
		return "", command.Err(err.Error())
	}
	m.rerunning = id
	return what, nil
}

// decidedIn is how long a hands-on test took to decide when it last did, in seconds.
func (m *Model) decidedIn(id string) float64 {
	if s, ok := m.st.HO.last[id]; ok {
		return s.at
	}
	return 0
}

func (e conEnv) Force(id, status string) (string, error) {
	m := e.m
	var cur *Check
	all := m.f.All()
	for i := range all {
		if all[i].ID == id {
			cur = &all[i]
		}
	}
	if cur == nil {
		return "", command.Err(i18n.T("no result for %s in this run (Tab lists them)", id))
	}
	if err := m.be.Force(id, status); err != nil {
		return "", command.Err(err.Error())
	}
	if m.f.Forced == nil {
		m.f.Forced = map[string]pace.Status{}
	}
	m.f.Forced[id] = paceOf(status)
	return statusWord(cur.Status), nil
}

// goto's screens, by the name typed.
var screenNames = []struct {
	name  string
	scene Scene
}{{"hello", Hello}, {"which-mac", WhichMac}, {"network", Network}, {"quick-pass", Quick}, {"quick-results", Results},
	{"hands-on", Attention}, {"finish", Finish}, {"report", Report}}

// Screens are the screens goto opens: those seen in this run, any in the demo.
func (e conEnv) Screens() []string {
	var out []string
	for _, s := range screenNames {
		if e.m.f.Demo || e.m.seen[s.scene] {
			out = append(out, s.name)
		}
	}
	return out
}

func (e conEnv) Goto(name string) error {
	m := e.m
	ok := false
	var to Scene
	for _, s := range screenNames {
		if s.name == name && (m.f.Demo || m.seen[s.scene]) {
			ok, to = true, s.scene
		}
	}
	if !ok {
		return command.Err(i18n.T("no screen %s to go to (Tab lists them)", name))
	}
	switch m.st.Scene { // leaving: what the screen keeps running stops
	case Network:
		m.be.NetStop()
	case Test:
		m.st.HO.stopAll()
	}
	m.st.Overlay = NoOverlay
	switch {
	case to == Network:
		m.goTo(Network)
		m.be.NetStart()
	case to == Quick && !m.seen[Quick]: // the demo: the pass starts
		m.netContinue()
	case to == Attention:
		m.keepGoing()
	case (to == Results || to == Finish || to == Report) && !m.st.PassDone && m.f.Demo:
		return command.Err(i18n.T("after the quick pass"))
	default:
		m.goTo(to)
	}
	return nil
}

func (e conEnv) Font(px int) error {
	m := e.m
	if m.o.SetFont == nil {
		return command.Err(i18n.T("only on the Linux console"))
	}
	if err := m.o.SetFont(px); err != nil {
		return command.Err(err.Error())
	}
	m.o.FontPx = px
	m.conCmd = tea.ClearScreen // the console resizes and clears: repaint it whole
	return nil
}

func (e conEnv) Frames() command.Frames {
	fr := e.m.frames.last
	if !e.m.frames.measured && e.m.gr != nil {
		fr.W, fr.H = e.m.gr.W, e.m.gr.H
	}
	return fr
}

func (e conEnv) Target() command.Target { return e.m.be.Target() }

// Record notes a redo or change command for the report's console entries (read-only commands
// never are).
func (e conEnv) Record(line string, kind command.Kind) {
	e.m.recorded++
	e.m.be.Console(line, kind.Word())
}

// Width is the box's inside: the frame's width less its padding.
func (e conEnv) Width() int {
	if e.m.gr == nil {
		return MinW - 8
	}
	return e.m.gr.W - 8
}

// Say puts the console's line in Boot's box (boot say), on whatever screen shows (Said): typed
// out from when it first shows.
func (e conEnv) Say(text string) { e.m.st.Said = Said{Text: text, Wait: true} }

// End opens Esc's quit question with a choice selected, and closes the console: quit (to the
// prompt, or the installer), restart or shutdown. During the hands-on
// tests the question only offers to end them, so only quit opens it there; with the OmaBoot?
// drive gone, Esc's box is the drive's, which offers restart and shutdown.
func (e conEnv) End(choice string) error {
	m := e.m
	st := &m.st
	if h := st.HO; st.Scene == Test && h != nil && h.Run != nil && h.Out == nil && !h.Paused && (h.Run.View().Busy || h.Keeping) {
		return command.Err(i18n.T("not while the test is busy"))
	}
	if choice != "quit" {
		switch {
		case m.f.Fatal != "":
			return command.Err(i18n.T("only quit works here: nothing was tested"))
		case quitCaseOf(st, m.f) == quitHandsOn && !st.Gone:
			return command.Err(i18n.T("not during the hands-on tests: quit ends them first"))
		}
	}
	st.Con.Open = false
	m.conCmd = m.askQuit()
	switch st.Overlay {
	case ConfirmOverlay:
		st.ConfirmSel = map[string]int{"quit": 1, "shutdown": 2, "restart": 3}[choice]
	case StickOverlay:
		st.StickSel = map[string]int{"quit": 1, "restart": 1, "shutdown": 2}[choice]
	}
	return nil
}

func (e conEnv) Catalog() command.Catalog { return e.m.be.Catalog() }

// Gear is what the hands-on tests need at hand, in plan order, each once: the plan Quick results
// offered, or the one it would.
func (e conEnv) Gear() []string {
	var plan []handson.Spec
	if h := e.m.st.HO; h != nil {
		plan = h.Setup.Plan
	} else {
		plan = e.m.be.HandsOn().Plan
	}
	var out []string
	seen := map[string]bool{}
	for _, s := range plan {
		if s.Gear != "" && !seen[s.Gear] {
			seen[s.Gear] = true
			out = append(out, i18n.S(s.Gear))
		}
	}
	return out
}

func (e conEnv) Console() *command.Console { return e.m.st.Con }

// SMC reads a key on the Mac the light sensor's hands-on test reads (the demo's scripted one in
// the demo).
func (e conEnv) SMC(key string) (command.SMCKey, error) {
	setup := e.m.be.HandsOn()
	spec, _ := handson.Lookup("input.ambient-light-sensor")
	if setup.IO == nil {
		return command.SMCKey{}, command.Err(i18n.T("no Mac to read here"))
	}
	k, err := handson.ReadSMC(setup.IO(spec), key)
	return command.SMCKey{Name: k.Name, Type: k.Type, Data: k.Data}, err
}

// SleepChecker is a backend that can put the Mac to sleep from the console (sleepcheck): the
// real run's. The demo's can't.
type SleepChecker interface {
	SleepInfo() (command.SleepInfo, error)
	SleepCheck(what string) error
}

func (e conEnv) SleepInfo() (command.SleepInfo, error) {
	sc, ok := e.m.be.(SleepChecker)
	if !ok {
		return command.SleepInfo{}, command.Err(i18n.T("no Mac to put to sleep here"))
	}
	return sc.SleepInfo()
}

// SleepCheck starts sleepcheck: once the quick pass is done, with no hands-on test under way,
// one at a time. Its lines come as SleepCheckMsg.
func (e conEnv) SleepCheck(what string) error {
	m := e.m
	st := &m.st
	sc, ok := m.be.(SleepChecker)
	switch {
	case !ok:
		return command.Err(i18n.T("no Mac to put to sleep here"))
	case m.sleepChecking:
		return command.Err(i18n.T("still checking: one at a time"))
	case st.Scene == Quick && !st.PassDone:
		return command.Err(i18n.T("once the quick pass is done"))
	case st.Scene == Test && st.HO != nil && st.HO.Run != nil && st.HO.Out == nil:
		return command.Err(i18n.T("not during a hands-on test: finish or skip it first"))
	}
	if err := sc.SleepCheck(what); err != nil {
		return err
	}
	m.sleepChecking = true
	return nil
}

// CatalogOf is the catalog as the console's catalog command shows it: c matched this Mac as r,
// and the run reports config; ran are the fixes that took effect in this run. Where it came
// from (Site, Fetched, Bundled) is the caller's to fill in. The fixes it lists for config are
// the ones registered for it, for one of its parts, or for any Mac.
func CatalogOf(c *identify.Catalog, r identify.Result, config string, ran []string) command.Catalog {
	if c == nil {
		return command.Catalog{}
	}
	out := command.Catalog{Version: c.Snap.Version, Date: c.Date, Macs: len(c.Snap.Match.Macs), Configs: c.Configs()}
	m := command.CatalogMac{Identifier: r.Identifier, Known: r.Known, By: r.By, Config: config, Release: c.Release[config]}
	if m.Release == "" {
		m.Release = r.Name
	}
	for _, cd := range r.Candidates {
		if cd.Config == config {
			m.Matched = cd.Matched
		}
	}
	out.Mac = m
	for _, mac := range c.Snap.Match.Macs {
		for _, rel := range mac.Releases {
			name := rel.Name
			if name == "" {
				name = c.Names[mac.Identifier]
			}
			out.Releases = append(out.Releases, command.Release{Identifier: mac.Identifier, Name: name, Configs: len(rel.Configs)})
		}
	}
	// MacBookPro2,1 before MacBookPro10,1; a model's releases stay in the catalog's order
	slices.SortStableFunc(out.Releases, func(a, b command.Release) int { return modelCmp(a.Identifier, b.Identifier) })
	parts := map[string]bool{}
	if cf := c.Config(config); cf != nil {
		for _, p := range cf.Parts {
			for _, id := range p.IDs {
				parts[id] = true
			}
		}
	}
	did := map[string]bool{}
	for _, id := range ran {
		did[id] = true
	}
	for _, f := range c.Snap.Fixes {
		ok := len(f.Configs) == 0 && len(f.Devices) == 0
		for _, id := range f.Configs {
			ok = ok || config != "" && id == config
		}
		for _, d := range f.Devices {
			ok = ok || len(f.Configs) == 0 && parts[d.ID]
		}
		if ok {
			out.Fixes = append(out.Fixes, command.Fix{ID: f.ID, Name: f.Name, Ran: did[f.ID]})
		}
	}
	for _, side := range c.Ports(identify.Result{Identifier: r.Identifier, Config: config}) {
		for _, p := range side.Ports {
			out.Ports = append(out.Ports, command.Port{Side: side.Side, Name: p.Name})
		}
	}
	return out
}

// screenText is the screen under the console as text, for bugreport.
func (m *Model) screenText() string {
	if m.gr == nil {
		return ""
	}
	gr := g.New(m.gr.W, m.gr.H)
	st := m.st
	con := *st.Con
	con.Open = false
	st.Con = &con
	Render(gr, &m.o, &st, m.f, m.player)
	return gr.Text()
}

// fullLogText is the full log (L) as text: the results, the startup, the console.
func fullLogText(f *Facts, st *State) []string {
	var out []string
	for _, c := range f.All() {
		mk, _, word := mark(c.Status)
		out = append(out, strings.TrimSpace(mk+" "+c.ID+"  "+word+"  "+i18n.S(c.Detail)+"  "+c.Evidence))
	}
	if len(f.Startup) > 0 {
		out = append(append(out, "", i18n.T("Startup")), f.Startup...)
	}
	if st.Con != nil && len(st.Con.Log) > 0 {
		out = append(append(out, "", i18n.T("Console")), st.Con.Log...)
	}
	return out
}

// again notes that a hands-on test is about to be measured again in the console: once it decides,
// its results carry the first run in their evidence (checks.Again). A test redone again keeps
// its first run.
func (h *HandsOn) again(id string, first checks.Result, took float64) {
	if note, ok := h.Again[id]; ok {
		h.pending = [2]string{id, note}
		return
	}
	h.pending = [2]string{id, checks.Again(first, took)}
}

// modelCmp orders model identifiers with their numbers as numbers: MacBookPro2,1 < MacBookPro10,1.
func modelCmp(a, b string) int {
	for a != "" && b != "" {
		ka, ra := modelPart(a)
		kb, rb := modelPart(b)
		if c := ka.cmp(kb); c != 0 {
			return c
		}
		a, b = ra, rb
	}
	return len(a) - len(b)
}

type modelKey struct {
	num  bool
	n    int
	text string
}

func (k modelKey) cmp(o modelKey) int {
	switch {
	case k.num && o.num:
		return k.n - o.n
	case k.num != o.num: // digits before letters
		if k.num {
			return -1
		}
		return 1
	}
	return strings.Compare(k.text, o.text)
}

// modelPart splits off the leading run of digits or of other characters.
func modelPart(s string) (modelKey, string) {
	digit := func(r byte) bool { return r >= '0' && r <= '9' }
	i := 1
	for i < len(s) && digit(s[i]) == digit(s[0]) {
		i++
	}
	k := modelKey{num: digit(s[0]), text: s[:i]}
	if k.num {
		for _, c := range []byte(s[:i]) {
			k.n = k.n*10 + int(c-'0')
		}
	}
	return k, s[i:]
}
