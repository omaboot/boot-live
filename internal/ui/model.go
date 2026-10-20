package ui

import (
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/omaboot/boot-live/internal/boot"
	"github.com/omaboot/boot-live/internal/checks"
	"github.com/omaboot/boot-live/internal/command"
	g "github.com/omaboot/boot-live/internal/grid"
	"github.com/omaboot/boot-live/internal/handson"
	"github.com/omaboot/boot-live/internal/pace"
	"github.com/omaboot/boot-live/internal/sounds"
)

// FPS is the redraw rate: animations stay inside Boot's area at 10–15 fps (LIVE-PLAN §5.1).
const FPS = 15

type tickMsg time.Time

// Backend does the work behind the screens: the network step, the checks, sending and saving.
// The real run's (internal/app) does it on the Mac; the demo's (internal/demo) plays a script.
// Its methods return at once; results come back as messages (NetMsg, CheckMsg, …) through the
// send function it was given.
type Backend interface {
	SetSend(func(tea.Msg))
	Pick(config string) // the configuration the person picked ("" = not sure)
	NetStart()          // the Network step is on screen: get online if it can, scan, keep the lines live
	NetStop()           // the step is left: stop watching (a connection stays up)
	NetScan()
	NetClear()                              // a network is picked to join: the last join's error is over
	NetConnect(ssid, password string)       // a working cable or phone is turned off first (NetState.Parked)
	NetSwitch(via string)                   // switch to the Ethernet or phone line's connection; the one in use turns off
	StaticTarget() StaticTarget             // the interface a static address would go on
	NetStatic(iface, addr, gw, dns string)  // set a static address (already checked with network.ParseStatic)
	Quick() (total int, expect float64)     // start the quick pass: how many results, about how long
	GPUGo()                                 // the GPU notice is over: the graphics check may run
	Submit(handle, notice string)           // send the run's one report
	Save(handle, notice string)             // save a copy to the stick
	Keep(handle, notice string, k KeepInfo) // save a copy before a test that sleeps (KeptMsg); the final save replaces it
	// Resume answers the copy of an unfinished run Which Mac offers (Facts.Resume): picked up
	// (use) or set aside; either way it isn't offered again.
	Resume(use bool)
	// Woke: a test that sleeps is over (or was left) and the Mac is still here: the copy saved
	// before it isn't one to pick up from, so its note goes.
	Woke()
	// HandsOn is the hands-on tests for this Mac: the plan, and the IO each test runs on.
	HandsOn() handson.Setup
	// Record takes the hands-on results so far (all of them, replacing the last set) into the
	// run's one report; the backend answers with the summary (QuickDoneMsg) brought up to date.
	Record(rs []checks.Result)

	// What the console's commands ask of the run (LIVE-PLAN §24).
	IDs() []command.ID                                // this Mac's hardware IDs
	KernelLog() []string                              // "[    1.234] driver: message"
	ReportJSON(handle, notice string) ([]byte, error) // the report as it stands
	Note(text string)                                 // a note for the report's notes
	Bugreport(f BugFiles)                             // logs, report and screen to the stick (BugMsg)
	SendBug(name string)                              // a saved bug report to OmaBoot? (PF-13; "": this run's and the stick's waiting ones), a BugSentMsg each
	// the run plan (runplan.go): ignored at Hello; what it did, for the report (kind "counts"
	// sets the tests' counts); its last line to the serial port; the console's plan command
	PlanOff()
	PlanStart(at string) (*PlanFacts, []string) // the console's plan run: the file read again; problems when it can't start
	PlanStop(at string)                         // the console's plan stop
	PlanNote(kind, text string)
	PlanSerial(line string)
	PlanText(check bool) ([]string, error)
	TestDrives() []command.TestDrive // where testfile could write the test file, and where not
	WriteTestFile(dev string)        // testfile write: the test file to a drive, read back (TestFileMsg)
	// Rerun runs a quick-pass check again (RerunMsg, then the summary brought up to date) and
	// says roughly how long it takes; an error when this run has no such check.
	Rerun(id string) (string, error)
	Force(id, status string) error // dev: a result set by hand (pass, partial, fail, skip)
	Console(command, kind string)  // a redo or change command, for the report's console entries
	Target() command.Target        // where Submit sends
	Catalog() command.Catalog      // the catalog snapshot in use, for catalog (CatalogOf)

	// Boot's sounds (sound.go): Sound plays one (a WAV, internal/sounds) on the Mac's own
	// output, cutting off the one before it; SaveSounds keeps the settings for the next boot.
	Sound(wav []byte)
	SaveSounds(p sounds.Prefs)
}

// KeepInfo is what the copy saved before a test that sleeps records besides the report, so a
// run whose Mac doesn't come back can be picked up again (Resume): the test about to run, and
// the hands-on tests decided so far.
type KeepInfo struct {
	Test  string                     // the test about to run (its ID)
	Title string                     // its title (msgid)
	Done  map[string]handson.Outcome // the hands-on tests decided so far, by test ID
	Of    int                        // the hands-on tests in the plan
}

// Resume is an unfinished run's copy on external storage, from this Mac and this boot-live
// version, not sent: Which Mac offers to pick the run up from it (owner, 0.7.3 review). The
// quick pass's results and the hands-on tests it decided come back; the test that was running
// when it was saved goes in as failed, the Mac not having come back from it; then the hands-on
// tests carry on (0.7.5: the quick pass doesn't run again, unless the copy lacks its results).
type Resume struct {
	Test, Title string                     // the test that was running (ID, title msgid)
	Saved       time.Time                  // when the copy was saved
	Version     string                     // the boot-live that saved it
	Done        map[string]handson.Outcome // the hands-on tests it decided, by test ID
	Of          int                        // the hands-on tests in its plan
	Quick       []checks.Result            // the quick pass's results; nil: the pass runs again
	QuickIn     string                     // how long that pass took, or ""
}

// OtherCopy is an unfinished run's copy from this Mac by another boot-live version: never picked
// up (a report names one version), only named on Which Mac.
type OtherCopy struct {
	Version string
	Saved   time.Time
	Copy    string // its path on external storage
}

// BugFiles is what bugreport saves besides the kernel log and the report: the screen under the
// console and the full log, as text; the name and notice go into the report as Submit would.
type BugFiles struct {
	Screen         string
	Log            []string
	Handle, Notice string
}

// Messages from the backend.
type (
	// NetMsg is the network step's state; times are filled in when it arrives.
	NetMsg struct{ Net NetState }
	// CheckMsg is one finished check, and the expected seconds of checks still to run.
	CheckMsg struct {
		Check     Check
		Remaining float64
	}
	// QuickDoneMsg ends the quick pass: what Quick results and Finish show.
	QuickDoneMsg struct {
		Groups              []Group
		Headline, AnswerSay string
		DoneWord            string
		QuickPassIn         string
		ReportKB            int
		Startup             []string // how the Mac's startup went, for the full log
		TestedOn, StartedIn string   // what the results stand for, how long the Mac took to start
		// BootLine is the boot timing, short ("Booted in 1 min 52 s: kernel 12 s, …"); SlowDrive
		// a warning when the OmaBoot? drive reads slowly; either "" (0.7.5)
		BootLine, SlowDrive string
	}
	// SentMsg is the site's answer: a report code and page, or the problem. Offline: it never
	// left, the connection is gone (Finish then points to saving a copy).
	SentMsg struct {
		Code, Page string
		Err        string
		Problems   []string
		Offline    bool
	}
	// SendWaitMsg: before sending, what the backend waits for (the connection went away), or ""
	// once it's back.
	SendWaitMsg struct{ Note string }
	// SavedMsg says where the report was saved on the stick, or why it wasn't.
	SavedMsg struct{ Path, Err string }
	// StickGoneMsg: the stick the system runs from was pulled out (the real run's watcher).
	StickGoneMsg struct{}
	// KeptMsg says where the copy saved before a sleeping test went, or why it didn't.
	KeptMsg struct{ Path, Err string }
	// RunningMsg is the check that just started: its ID, label (msgid), band effect, and when it
	// started (seconds into the pass, on the same clock as the results' Finish).
	RunningMsg struct {
		ID, Label string
		Effect    boot.Effect
		At        float64
	}
	// LiveMsg is a reading from the check running now (the thermal load's temperature): its band
	// effect shows it until the result comes.
	LiveMsg struct{ Params boot.Params }
	// GPUNoticeMsg: the backend holds the first graphics check (criterion ID, label msgid) until
	// GPUGo: the screen shows the notice first.
	GPUNoticeMsg struct{ ID, Label string }
)

// Ending is what the person chose to do when the tool ends.
type Ending string

const (
	EndQuit     Ending = ""
	EndPoweroff Ending = "poweroff"
	EndReboot   Ending = "reboot"
	EndInstall  Ending = "install" // OmaBoot? Omarchy: start the Omarchy installer
	EndRefused  Ending = "refused" // the run couldn't start safely
)

// Model is the S1 flow as a Bubble Tea program, driven by a Backend.
type Model struct {
	o      Options
	st     State
	f      *Facts
	be     Backend
	player *pace.Player
	gr     *g.Grid

	starts   [numScenes]time.Time // when each scene was first shown: going back keeps its clock
	seen     [numScenes]bool
	paused   time.Duration // the quick pass's time spent paused
	pausedAt time.Time
	began    time.Time // when the run started
	password []rune    // the Wi-Fi password being typed: only here, in memory, until it's handed to iwd
	oldErr   string    // the network error picking a network cleared, until the backend's state drops it too

	lastKey time.Time // the screensaver counts from here
	dozeAt  time.Time
	vtBlank bool   // the console itself is blanked (TIOCLINUX)
	drawn   bool   // the first frame was drawn
	frozen  string // the last frame, returned unchanged while the console is blanked

	// the console (console.go): a command's own tea.Cmd, the quick-pass check being measured
	// again, a bugreport on its way, how many commands the report records, the frame timing
	conCmd    tea.Cmd
	rerunning string
	bugging   bool
	bugSend   bool   // the bug report being saved goes to OmaBoot? too (a run plan's send: true, or Finish's form)
	bugFinish bool   // it's Finish's: how it goes shows in its form
	bugTold   bool   // the console said the stick's waiting bug reports
	writing   string // testfile write: the drive it's writing to
	// sleepChecking: sleepcheck is under way (its lines come as SleepCheckMsg)
	sleepChecking bool
	recorded      int
	frames        frameStats

	snd soundClock // what Boot's sounds have played (sound.go)
	pl  *planRun   // the run plan this run follows (runplan.go), nil for none

	// Exit is a note for the caller to print once the screen is restored.
	Exit string
	// End is what the person chose (shut down, restart, install), for the caller to do.
	End Ending
}

// NewModel starts at Hello.
func NewModel(o Options, f *Facts, be Backend) *Model {
	m := &Model{o: o, st: NewState(), f: f, be: be, began: time.Now(), lastKey: time.Now()}
	m.st.Sound, m.snd.saved = f.Sounds, f.Sounds
	m.pl = newPlanRun(f)
	m.st.PlanWait = m.pl != nil && m.pl.wait
	m.player = pace.New(pace.Default, 0, "", "")
	m.goTo(Hello)
	return m
}

func (m *Model) goTo(s Scene) {
	if !m.seen[s] {
		m.starts[s], m.seen[s] = time.Now(), true
	}
	m.st.Scene, m.st.Overlay, m.st.LogTop, m.st.ReportTop = s, NoOverlay, 0, 0
	m.st.T = m.timeIn(s)
	m.lastKey = time.Now()
	if s == Results {
		m.prepHandsOn()
	}
	if s == Finish && m.f.FinishedIn == "" {
		m.f.FinishedIn = Duration(time.Since(m.began))
	}
}

// timeIn is scene s's clock: seconds since it was first shown (the quick pass's without the
// time it was paused).
func (m *Model) timeIn(s Scene) float64 {
	if !m.seen[s] {
		return 0
	}
	el := time.Since(m.starts[s])
	if s == Quick {
		el -= m.paused
		if m.st.Paused {
			el -= time.Since(m.pausedAt)
		}
	}
	return el.Seconds()
}

func (m *Model) tick() tea.Cmd {
	d := time.Second / FPS
	if m.st.Doze || m.st.Blanked {
		d = time.Second // the screensaver moves once a second
	}
	return tea.Tick(d, func(t time.Time) tea.Msg { return tickMsg(t) })
}

// Init starts the frame clock.
func (m *Model) Init() tea.Cmd { return m.tick() }

// Update handles the clock, the window size, keys and the backend's messages.
func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	m.st.T = m.timeIn(m.st.Scene)
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.gr = g.New(msg.Width, msg.Height)
	case tickMsg:
		repaint := m.clock()
		cmds := []tea.Cmd{m.tick()}
		if c := m.planClock(); c != nil {
			cmds = append(cmds, c)
		}
		if repaint {
			cmds = append(cmds, tea.ClearScreen) // back from sleep: the console may have lost the picture
		}
		return m, tea.Batch(cmds...)
	case tea.KeyMsg:
		return m, m.key(msg)
	case NetMsg:
		m.netMsg(msg.Net)
	case StickGoneMsg:
		m.stickGone()
	case CheckMsg:
		if m.st.Gone {
			break // it may have failed for the wrong reason: the report leaves it out too
		}
		m.f.Checks = append(m.f.Checks, msg.Check)
		m.player.Add(Item(msg.Check))
		if id := m.snd.measuring; msg.Check.ID == id || strings.HasPrefix(msg.Check.ID, id+".") {
			m.snd.measuring = "" // its result is in: it's over
		}
		m.st.Remaining, m.st.RemainingAt = msg.Remaining, m.timeIn(Quick)
	case RunningMsg:
		m.st.Run = Running{ID: msg.ID, Label: msg.Label, Effect: msg.Effect}
		m.snd.measuring = ""
		if measures(msg.ID) {
			m.snd.measuring = msg.ID // no sound while it runs
		}
		m.player.Started(msg.At, Cue(msg.ID)) // shown running, its band effect on, until its result's line shows
	case LiveMsg:
		m.st.Run.Params = msg.Params
	case GPUNoticeMsg:
		m.st.GPU = GPUNotice{Waiting: true, ID: msg.ID, Label: msg.Label, At: -1, End: -1}
	case QuickDoneMsg:
		m.snd.measuring = ""
		m.f.Groups, m.f.Headline, m.f.AnswerSay, m.f.DoneWord = msg.Groups, msg.Headline, msg.AnswerSay, msg.DoneWord
		m.f.QuickPassIn, m.f.ReportKB, m.f.Startup = msg.QuickPassIn, msg.ReportKB, msg.Startup
		m.f.TestedOn, m.f.StartedIn = msg.TestedOn, msg.StartedIn
		m.f.BootLine, m.f.SlowDrive = msg.BootLine, msg.SlowDrive
	case SendWaitMsg:
		m.st.SendNote = msg.Note
	case SentMsg:
		st := &m.st
		st.SendNote = ""
		if msg.Offline { // the form closes; the next thing is Save a copy to stick
			st.Submitting, st.SendAt, st.SendErr, st.SendErrAt, st.FinishSel = false, -1, msg.Err, m.timeIn(Finish), 1
			if m.o.Edition.Installer {
				st.FinishSel = 2
			}
			break
		}
		if msg.Err != "" {
			st.SendAt, st.SendErr, st.Problems, st.SubFocus = -1, msg.Err, msg.Problems, 1
			break
		}
		m.f.Report, m.f.ReportPage = msg.Code, msg.Page
		st.SentAt, st.SendErr, st.Problems, st.Submitting = m.timeIn(Finish), "", nil, false
		st.FinishSel = 1 // the next thing: Save a copy (OmaBoot? Omarchy: Install Omarchy now)
		if m.o.Edition.Installer {
			st.FinishSel = 0
		}
	case RerunMsg:
		m.rerunDone(msg)
	case BugMsg:
		m.bugging = false
		m.bugDone(msg)
	case BugSentMsg:
		m.bugSentDone(msg)
	case TestFileMsg:
		m.writing = ""
		m.testFileDone(msg)
	case SleepCheckMsg:
		m.conPrint(msg.Lines...)
		if msg.Done {
			m.sleepChecking = false
		}
		if msg.Back { // back from a test suspend: the console may have lost the picture
			m.lastKey = time.Now()
			return m, tea.ClearScreen
		}
	case KeptMsg:
		if h := m.st.HO; h != nil {
			h.Keeping, h.Kept, h.KeepErr = false, msg.Path, msg.Err
		}
	case SavedMsg:
		m.st.Saving = false
		if msg.Err != "" {
			m.st.SaveErr = msg.Err
		} else {
			m.f.SavedFile, m.st.SaveErr, m.st.SavedAt = msg.Path, "", m.timeIn(Finish)
		}
	}
	return m, nil
}

// clock is the frame clock's work: the GPU notice, the end of the quick pass, the hands-on
// test on screen, the screensaver. It reports whether the screen needs a full repaint.
func (m *Model) clock() (repaint bool) {
	st := &m.st
	if st.Scene == Test && st.HO != nil && st.HO.Run != nil && !st.Gone {
		repaint = m.testClock()
	}
	if conOpen(st) && !conAvail(st) { // a test took the keyboard: it keeps it
		st.Con.Open = false
	}
	st.Hush = m.quiet()
	m.soundTick()
	if st.Scene == Quick {
		t := st.T
		switch {
		case st.GPU.Waiting && st.GPU.At < 0 && CaughtUp(m.player, t):
			st.GPU.At = t // the playback has caught up: the notice comes up
		case st.GPU.Waiting && st.GPU.At >= 0 && t-st.GPU.At >= GPUWait:
			m.gpuGo()
		case m.player.Complete() && m.player.Total() > 0 && t >= QuickLen(m.player) && !st.GPU.Waiting:
			st.PassDone = true
			m.goTo(Results)
		}
	}
	if !m.waiting() {
		m.lastKey = time.Now()
		return repaint
	}
	idle := time.Since(m.lastKey).Seconds()
	switch {
	case idle >= BlankAfter && !st.Blanked:
		m.frozen = m.render()
		if m.o.Blank != nil {
			m.vtBlank = m.o.Blank(true)
		}
		st.Blanked = true
	case idle >= DozeAfter && !st.Doze:
		st.Doze, m.dozeAt = true, time.Now()
	}
	if st.Doze {
		st.DozeT = time.Since(m.dozeAt).Seconds()
	}
	return repaint
}

// waiting reports whether the screen is waiting for input: the screensaver may start. Never
// during the quick pass, nor while something is on its way (a console command's answer too).
// An open console doesn't count: Boot dozes over it after the same idle time (owner, 0.7.2 review).
func (m *Model) waiting() bool {
	st := &m.st
	busy := st.Scene == Quick || st.Saving || sending(st, m.f) || netBusy(st.Net) || m.rerunning != "" || m.bugging || m.writing != "" || m.sleepChecking
	if st.Scene == Test && st.HO != nil && st.HO.Run != nil && st.HO.Out == nil {
		busy = busy || st.HO.Run.TakesKeys() || st.HO.Run.View().Busy // keys go to the test, or it's going to sleep
	}
	return !busy
}

// wake ends the screensaver.
func (m *Model) wake() {
	if m.vtBlank && m.o.Blank != nil {
		m.o.Blank(false)
	}
	m.st.Doze, m.st.Blanked, m.vtBlank = false, false, false
	m.lastKey = time.Now()
}

// gpuGo ends the GPU notice: the graphics check may run now.
func (m *Model) gpuGo() {
	if !m.st.GPU.Waiting {
		return
	}
	m.st.GPU.Waiting, m.st.GPU.End = false, m.st.T
	m.be.GPUGo()
}

// netMsg takes the network step's new state, stamping when its parts changed.
func (m *Model) netMsg(n NetState) {
	st := &m.st
	old, t := st.Net, m.timeIn(Network)
	n.At, n.ListAt, n.RefreshedAt = old.At, old.ListAt, old.RefreshedAt
	// The error picking a network cleared (joinNow) is still in the states already on their way:
	// it isn't new, and it would close the password box (owner's Air, 0.8.1 dev: after a failed
	// join, every pick's box closed within two seconds). The backend drops it (NetClear) before
	// it can send another.
	if m.oldErr != "" {
		if n.Err == m.oldErr {
			n.Err = ""
		} else {
			m.oldErr = ""
		}
	}
	if n.Phase != old.Phase || n.Online != old.Online || n.Via != old.Via || n.Err != "" && n.Err != old.Err || n.SignIn && !old.SignIn {
		n.At = t
	}
	if len(n.Networks) > 0 && (len(old.Networks) == 0 || old.Phase == NetScanning && n.Phase == NetList) {
		n.ListAt = t
	}
	if n.Refreshed != "" && n.Refreshed != old.Refreshed {
		n.RefreshedAt = t
	}
	n.PhoneAt = old.PhoneAt
	if n.TetLink != old.TetLink || n.PhoneNeed != old.PhoneNeed || n.Phone != old.Phone {
		n.PhoneAt = t
	}
	if n.Online && !old.Online || n.Err != "" && n.Err != old.Err {
		m.closePw() // the password box goes once it has done its job
	}
	// The network picked for the password box is the screen's until the join starts: the backend
	// only learns it then, and its state comes every two seconds (the links' watch), so taking
	// its SSID wiped the pick while the password was typed, and Join asked for "" ("… is gone:
	// scan again", owner's Air, 0.7.13 dev).
	if st.Pw && !netBusy(n) {
		n.SSID = old.SSID
	}
	if st.Scene == Network { // a connection made or failed: its blip (owner, 0.8.4 review)
		switch {
		case n.Err != "" && n.Err != old.Err:
			m.sound("fail")
		case n.Online && (!old.Online || netBusy(old) && n.Phase == NetList && n.Err == ""):
			m.sound("pass")
		}
	}
	if n.Online {
		st.Offline = false
		// newly online, or a join or a switch has just finished: the connected view
		if !old.Online || netBusy(old) && n.Phase == NetList && n.Err == "" {
			st.Browse, st.UpSel = false, 0
		}
	}
	if len(old.Networks) > 0 && st.NetSel >= len(old.Networks) { // on the actions: stays on the same action when the list changes
		st.NetSel = len(n.Networks) + st.NetAct
	}
	st.NetSel = min(st.NetSel, len(n.Networks)+len(listActions(st))-1)
	if st.NetPick >= len(n.Networks) {
		st.NetPick = 0
	}
	st.Net = n
	if st.LineSel >= 0 && (!linePick(st, st.LineSel) || netBusy(n)) { // its line changed under it
		st.LineSel = -1
	}
}

func (m *Model) quit(note string) tea.Cmd {
	m.st.HO.stopAll()
	m.Exit = note
	return tea.Quit
}

func (m *Model) end(e Ending) tea.Cmd {
	m.st.HO.stopAll()
	m.End = e
	return tea.Quit
}

func (m *Model) pause(on bool) {
	st := &m.st
	if st.Scene != Quick || st.Paused == on {
		return
	}
	if on {
		m.pausedAt = time.Now()
	} else {
		m.paused += time.Since(m.pausedAt)
	}
	st.Paused = on
}

func (m *Model) handle() string { return strings.TrimSpace(string(m.st.Name)) }

func (m *Model) render() string {
	t0 := time.Now()
	if c := m.st.Con; c != nil && c.X.Speeds {
		c.X.Timing = conEnv{m}.Frames()
	}
	Render(m.gr, &m.o, &m.st, m.f, m.player)
	lines := m.gr.Lines(m.o.Mode, &m.o.Palette)
	m.frames.add(lines, time.Since(t0), m.gr.W, m.gr.H) // the console's fps (dev)
	return strings.Join(lines, "\n")
}

// View draws the current frame. Bubble Tea's renderer compares it with the last frame line by
// line and rewrites only the lines that changed; while the console is blanked nothing changes.
func (m *Model) View() string {
	if m.gr == nil {
		return ""
	}
	if m.st.Blanked && m.vtBlank {
		return m.frozen
	}
	m.st.T = m.timeIn(m.st.Scene)
	out := m.render()
	if !m.drawn { // the first frame: the backend notes when, for the boot timing
		m.drawn = true
		if d, ok := m.be.(interface{ Drawn() }); ok {
			d.Drawn()
		}
	}
	return out
}
