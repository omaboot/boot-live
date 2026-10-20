package ui

import (
	"math"
	"strings"

	"github.com/omaboot/boot-live/internal/boot"
	"github.com/omaboot/boot-live/internal/command"
	g "github.com/omaboot/boot-live/internal/grid"
	"github.com/omaboot/boot-live/internal/i18n"
	"github.com/omaboot/boot-live/internal/icons"
	"github.com/omaboot/boot-live/internal/network"
	"github.com/omaboot/boot-live/internal/pace"
	"github.com/omaboot/boot-live/internal/sounds"
)

// Scene is one screen.
type Scene int

const (
	Hello Scene = iota
	WhichMac
	Network
	Quick
	Results
	Finish
	Report    // the read-only report, opened from Finish
	Attention // "Boot needs you", before the hands-on tests
	Test      // one hands-on test (State.HO says which)
	numScenes
)

// Step is the scene's place on the step track (0–5); the hands-on screens have none.
func (s Scene) Step() int {
	switch s {
	case Report:
		return int(Finish)
	case Attention, Test:
		return -1
	}
	return int(s)
}

// Overlay is drawn over a screen.
type Overlay int

const (
	NoOverlay      Overlay = iota
	LogOverlay             // the full log (L)
	ConfirmOverlay         // the quit question (Esc)
	StickOverlay           // the stick the system runs from came out (State.Gone)
	SwitchOverlay          // switch from one working connection to another? (State.SwitchVia, SwitchTo)
	EndOverlay             // Finish's Shutdown or Restart with the report neither sent nor saved (State.EndWhat)
)

// Quick pass playback: after the last line, the screen holds this long before Quick results.
const quickHold = 2.5

// When each screen's ▶ list appears (scene seconds); keys wait for it.
const (
	helloReady   = 3.0
	macReady     = 2.4 // after the search and the table (0.7.9)
	resultsReady = 2.4
	finishReady  = 0.8
)

// GPUWait is how long the GPU notice waits before the graphics check starts by itself.
const GPUWait = 15.0

// State is everything the screens draw from, besides the options and the run's facts.
type State struct {
	Scene      Scene
	T          float64 // seconds into the scene (the quick pass: paused time excluded)
	Paused     bool    // the quick pass's playback is paused (the quit question is up)
	Overlay    Overlay
	LogTop     int // first row of the full log
	ReportTop  int // first row of Show report's itemised tests
	ConfirmSel int
	EndWhat    Ending // what EndOverlay asks about: EndPoweroff or EndReboot
	EndSel     int
	Gone       bool // the stick the system runs from came out: nothing more is tested (stick.go)
	StickSel   int
	SwitchVia  string // the connection SwitchOverlay asks about, as NetState.Via names it ("Wi-Fi": a network, SwitchTo)
	SwitchTo   int    // the Wi-Fi network it asks about (an index into Net.Networks)
	SwitchSel  int

	HelloSel int
	PlanOff  bool // the run plan was ignored at Hello, or stopped there (runplan.go)
	PlanWait bool // the run plan waits for the console's plan run (start: console)

	// Finish's Send a bug report (PF-13): its form open, sending (saving first), and how it went:
	// the code, or that it waits on external storage, or why it didn't go.
	BugForm, BugSending bool
	BugSel              int
	BugCode, BugNote    string

	MacSel   int
	Resumed  bool   // the run was picked up from an unfinished run's copy (Facts.Resume)
	Answered bool   // the copy was answered (picked up or set aside): not offered again
	Picked   bool   // a configuration was picked from a list
	PickedID string // the one picked ("" none of these)
	Picking  bool   // "Pick from the list" is open
	PickSel  int

	Net     NetState
	LineSel int  // the found line with the cursor (LineEthernet…LineWiFi), -1 when it's below them
	NetSel  int  // the list view's cursor: the networks, then the actions
	NetPick int  // the network the cursor last sat on (•)
	NetAct  int  // the action the cursor last sat on: where Tab goes from the list
	UpSel   int  // the online view's cursor
	Browse  bool // online, but the Wi-Fi list is up again ("Use another network")
	Pw      bool // the password box is up
	PwLen   int  // dots in it
	PwShow  bool // Tab shows the password
	// PwShown is the password itself while Tab shows it, else nil: the Model's (m.password), never
	// a copy, so it's gone with it
	PwShown  []rune
	Form     StaticForm
	Offline  bool // "Skip, test offline" was picked
	PassDone bool // the quick pass has run: Network's Continue goes back to the results

	Remaining   float64 // the real pass: expected seconds of checks still running, as of RemainingAt
	RemainingAt float64
	Run         Running // the check running now (its result not in yet)
	GPU         GPUNotice

	ResultsSel int

	HO     *HandsOn // the hands-on tests, once Quick results has offered them
	AttSel int      // "Boot needs you"'s cursor

	FinishSel  int
	Guard      bool // Submit on a run changed in the console: the amber box instead of the form
	GuardSel   int
	Submitting bool    // Submit report's form is up
	Name       []rune  // name to credit
	SubFocus   int     // 0 the name field, 1 Send report, 2 Don't send report
	SubAt      float64 // when the form came up
	SendAt     float64
	SentAt     float64 // Finish's time the report was received, or < 0
	SendNote   string  // while sending: what it waits for first (the connection went away), or ""
	SendErr    string
	SendErrAt  float64 // when a send failed for want of a connection (the form closes; Save is next)
	Problems   []string
	Saving     bool
	SavedAt    float64 // Finish's time the last save ended, or < 0
	SaveErr    string

	Doze    bool    // the screensaver: Boot dozes in the frame
	DozeT   float64 // seconds dozing
	Blanked bool    // the console is blanked

	Con  *command.Console // the command console (console.go)
	Said Said             // Boot's line from the console (boot say), in his box on every screen

	Sound    sounds.Prefs // Boot's sounds: on or off (M), and the picks (sound.go)
	Sounds   bool         // the sounds screen is up (the console's sounds), over the screen
	SoundRow int          // its cursor: the moment
	Hush     bool         // nothing may play now: something is being measured (Model.quiet)
}

// Running is the check the backend is running now, as the quick pass shows it before its result
// is in: the log's spinner line and the band effect.
type Running struct {
	ID, Label string // label: msgid
	Effect    boot.Effect
	Params    boot.Params // its live readings so far (LiveMsg)
}

// StaticForm is "Set static IP address" on the Network step.
type StaticForm struct {
	Open      bool
	Fields    [3][]rune // address/prefix, gateway, DNS
	Focus     int       // 0–2 the fields, 3 and 4 the actions
	Err       string
	Requested bool // Apply was pressed: the backend's next answer is this form's
}

// GPUNotice is the notice before the first graphics check: the backend holds the check until
// it ends (Enter, or the countdown), so the person is warned before the screen may go dark.
type GPUNotice struct {
	Waiting   bool // the backend is holding the graphics check
	ID, Label string
	At, End   float64 // when it came up and when it ended (scene times), or < 0
}

// On reports whether the notice is on screen at t.
func (n GPUNotice) On(t float64) bool { return n.At >= 0 && t >= n.At && (n.End < 0 || t < n.End+0.5) }

// NewState is the start of a run.
func NewState() State {
	return State{LineSel: -1, SendAt: -1, SentAt: -1, SavedAt: -1, GPU: GPUNotice{At: -1, End: -1}, Con: command.New()}
}

func typed(s string, t, cps float64) string {
	r := []rune(s)
	return string(r[:max(0, min(len(r), int(math.Floor(t*cps))))])
}

func spinner(t float64) string { return string("|/-\\"[int(math.Floor(t*8))%4]) }

func blinkOn(t float64) bool { return int(math.Floor(t*2))%2 == 1 }

func omarchy(f *Facts) string {
	if f.Omarchy == "" {
		return i18n.T("Omarchy")
	}
	return i18n.T("Omarchy %s", f.Omarchy)
}

// viaUp is the header's "online" word: the phone by its name ("iPhone up").
func viaUp(n NetState) string {
	switch n.Via {
	case "Ethernet":
		return i18n.T("Ethernet up")
	case "Phone":
		return i18n.T("%s up", PhoneName(n))
	}
	return i18n.T("Wi-Fi up")
}

// netWord is the header's network state.
func netWord(st *State) string {
	n := st.Net
	asking := func(via string) string {
		if via == "Phone" {
			return icons.Phone + " " + i18n.T("%s: asking for an address…", PhoneName(n))
		}
		return icons.Ethernet + " " + i18n.T("Ethernet: asking for an address…")
	}
	switch {
	case n.Phase == NetJoining || n.Phase == NetDHCP:
		return icons.WiFi + " " + i18n.T("Wi-Fi: connecting…")
	case n.Phase == NetApplying:
		return i18n.T("%s: setting the address…", network.Shown(n.Iface, ""))
	case n.Phase == NetSwitching:
		return asking(n.To)
	case n.Online:
		return viaIcon(n.Via) + viaUp(n)
	case st.Offline:
		return i18n.T("offline")
	case n.TetLink == LinkCarrier && n.PhoneNeed == network.NeedNothing:
		return asking("Phone")
	case n.EthLink == LinkCarrier:
		return asking("Ethernet")
	}
	return i18n.T("no network")
}

// viaIcon is the status line's icon for how the Mac is online, with its space.
func viaIcon(via string) string {
	switch via {
	case "Ethernet":
		return icons.Ethernet + " "
	case "Phone":
		return icons.Phone + " "
	}
	return icons.WiFi + " "
}

// netRight is the status line's right side: Omarchy's version, four blank cells, the network
// (no bullet between them).
func netRight(f *Facts, st *State) string { return omarchy(f) + "    " + netWord(st) }

// Render draws the screen for st into gr.
func Render(gr *g.Grid, o *Options, st *State, f *Facts, p *pace.Player) {
	gr.Clear()
	if gr.W < MinW || gr.H < MinH {
		tooSmall(gr)
		return
	}
	if st.Blanked {
		return // the console is off: nothing drawn until a key wakes it
	}
	st.Said.frame()
	var b Box
	switch {
	case st.Sounds: // over the screen on now
		b = drawSounds(gr, o, st, f)
	case st.Scene == Hello:
		b = drawHello(gr, o, st, f)
	case st.Scene == WhichMac:
		b = drawWhichMac(gr, o, st, f)
	case st.Scene == Network:
		if st.Form.Open {
			b = drawStatic(gr, o, st, f)
		} else {
			b = drawNetwork(gr, o, st, f)
		}
	case st.Scene == Quick:
		b = drawQuick(gr, o, st, f, p)
	case st.Scene == Results:
		b = drawResults(gr, o, st, f, false)
	case st.Scene == Finish:
		b = drawFinish(gr, o, st, f)
	case st.Scene == Report:
		b = drawResults(gr, o, st, f, true)
	case st.Scene == Attention:
		b = drawAttention(gr, o, st, f)
	case st.Scene == Test:
		b = drawTest(gr, o, st, f)
	}
	switch {
	case st.Doze:
		drawDoze(gr, o, b, st.DozeT)
	case st.Sounds: // the screen under it keeps its overlay for when it closes
	case st.Overlay == ConfirmOverlay:
		drawQuit(gr, o, b, st, f)
	case st.Overlay == StickOverlay:
		drawStick(gr, o, b, st, f)
	case st.Overlay == SwitchOverlay:
		drawSwitch(gr, o, b, st)
	case st.Overlay == EndOverlay:
		drawEnd(gr, o, b, st)
	}
	gr.Unclip() // frame() cut the screen above the console's box: the box itself goes under it
	if h := conHeight(gr, st); h > 0 {
		drawConsole(gr, b, h, st)
	}
	if st.Con != nil && st.Con.X.Speeds {
		drawTiming(gr, st.Con.X.Timing)
	}
}

// The smallest screen the layout fits (a 1280×800 MacBook at 16 px is 160×50; a 21.5-inch iMac
// at 24 px is 160×45).
const (
	MinW = 120
	MinH = 40
)

func tooSmall(gr *g.Grid) {
	gr.Put(1, 0, i18n.T("This screen is too small: the tester needs %d×%d characters, it has %d×%d.", MinW, MinH, gr.W, gr.H), g.YEL)
	gr.Put(1, 1, i18n.T("Ctrl+C quits."), g.DIM)
}

// ── Hello ──────────────────────────────────────────────────────────────────────

// helloActions: Start; the Omarchy edition adds the installer. A run that can't start safely
// offers only Quit.
func helloActions(o *Options, f *Facts) []Action {
	if f.Fatal != "" {
		return []Action{{Label: i18n.T("Quit"), Does: i18n.T("nothing was tested and nothing was written")}}
	}
	a := []Action{{Label: i18n.T("Start"), Does: i18n.T("find out which Mac this is, then test it")}}
	if o.Edition.Installer {
		a = append(a, Action{Label: i18n.T("Go to the installer"), Does: i18n.T("skip the tests and install Omarchy")})
	}
	return a
}

func drawHello(gr *g.Grid, o *Options, st *State, f *Facts) Box {
	t := st.T
	fatal := f.Fatal != ""
	b := frame(gr, o, st, f.Mac, frameSpec{title: i18n.T("Hello"), step: 0, ho: st.HO, right: omarchy(f) + "    " + i18n.T("starting"), keys: Keys{Esc: true}})
	l := layout(b, o.Plain)
	if o.Plain {
		gr.Put(l.FieldX, l.BandTop, i18n.T("Test this Mac"), g.WHT)
		gr.Put(l.FieldX, l.BandTop+1, i18n.T("Checks this Mac's hardware and leaves its disks untouched."), g.DIM)
	} else {
		// Boot speaks first; the setup lines follow
		if t > 0.2 {
			ex := boot.Idle
			if fatal && t > 1.6 {
				ex = boot.Concerned
			}
			boot.Draw(gr, l.BootX, l.BandTop+1, ex, life(st, t))
			boot.Wordmark(gr, l.FieldX, l.BandTop+1, "OmaBoot?")
			if !fatal { // the disk locks, next to the wordmark
				diskBand(gr, l.FieldX+52, l.BandTop+1, l.FieldW-53, t, helloDisksAt, f)
			}
		}
		next := i18n.T("the disks are safe. next: which Mac this is.")
		if f.Omarchy != "" {
			next = i18n.T("the disks are safe, and this is real Omarchy %s. next: which Mac this is.", f.Omarchy)
		}
		if fatal {
			next = i18n.T("something's not right. I stop here: nothing was tested.")
		}
		sayBox(gr, &st.Said, l.FieldX, l.BandTop+7, l.FieldW-1, l.RuleY-(l.BandTop+7)-2, t, []line{
			{0.4, i18n.T("hi. I'm Boot. I test Macs and leave their disks untouched.")}, {3.0, next}}, -1)
	}
	divider(gr, b, l.RuleY, "", 0, g.BVIO)
	console := i18n.T("console: built-in font, %d×%d", gr.W, gr.H)
	switch {
	case o.Font != "":
		console = i18n.T("console: %s %d, %d×%d", o.Font, o.FontPx, gr.W, gr.H)
	case o.Term:
		console = i18n.T("console: terminal emulator, %d×%d", gr.W, gr.H)
	}
	type setupLine struct {
		at         float64
		mark, text string
		mc         g.Color
	}
	// whole lines, one after another (no typing); the system and the image
	// above the console
	setup := []setupLine{
		{helloDisksAt, "■", f.DisksLine, g.BVIO},
		{1.6, "■", f.SystemLine, g.BVIO},
		{1.8, "■", f.ImageLine, g.BVIO},
		{2.0, "■", console, g.BVIO},
		{2.2, "■", f.SnapshotLine, g.BVIO},
	}
	if fatal {
		setup = []setupLine{{1.4, "×", f.Fatal, g.BRED}}
	}
	// the setup lines in a terminal-like window, like a test's Output box:
	// as wide as its longest line, as tall as all of them, so it never moves as they come
	x, y := b.X+1, l.ContentY
	n, ww := 0, 0
	for _, s := range setup {
		if s.text != "" {
			n++
			ww = max(ww, width(s.text)+2)
		}
	}
	bw := min(b.W-2, max(ww+4, 48))
	bh := n + 2
	if t > 1.2 {
		box(gr, x, y, bw, bh, g.DIM, i18n.T("Setting up"), g.GRY)
		ly := y + 1
		for _, s := range setup {
			if t > s.at && s.text != "" {
				gr.Put(x+2, ly, s.mark, s.mc)
				gr.Put(x+4, ly, cut(s.text, bw-6), g.GRY)
				ly++
			}
		}
		if ly < y+bh-1 && blinkOn(t) { // more to come: the terminal's cursor
			gr.Put(x+2, ly, "█", g.GRY)
		}
	}
	if t > helloReady {
		actions(gr, x, y+bh+1, helloActions(o, f), st.HelloSel, true, 0, b.X+b.W)
	}
	if t > 1.2 { // the run plan on the stick: what it will do, or why it isn't used
		drawPlanNotice(gr, f, st, x, y+bh+1+len(helloActions(o, f))+1, bw, b.W-2)
	}
	return b
}

// ── Which Mac ──────────────────────────────────────────────────────────────────

// pickList is the configuration list Which Mac shows instead of its actions, if any: several
// candidates before a pick, or every configuration of the Mac after "Pick from the list".
func pickList(f *Facts, st *State) []Choice {
	switch {
	case st.Picking:
		return f.Configs
	case len(f.Candidates) > 1 && !st.Picked:
		return f.Candidates
	}
	return nil
}

// Which Mac's actions, by meaning (macActions' second answer).
const (
	macGo     = iota // That's my Mac / Continue
	macResume        // Pick up where you left off
	macFresh         // Start fresh
	macPick          // Pick from the list
)

// macActions are Which Mac's actions once the configuration is settled, and what each means. An
// unfinished run's copy (Facts.Resume) turns "That's my Mac" into its two answers: pick it up, or
// start fresh.
func macActions(f *Facts, st *State) ([]Action, []int) {
	if f.Resume != nil && !st.Answered && !st.PassDone {
		r := f.Resume
		a := []Action{
			{Label: i18n.T("Pick up where you left off"), Does: i18n.N("quick pass and %[1]d hands-on test kept; %[2]s goes in as failed",
				"quick pass and %[1]d hands-on tests kept; %[2]s goes in as failed", len(r.Done), len(r.Done), i18n.S(r.Title))},
			{Label: i18n.T("Start fresh"), Does: i18n.T("test everything again; the copy stays on external storage")},
		}
		if len(r.Quick) == 0 {
			a[0].Does = i18n.N("%[1]d hands-on test kept; %[2]s goes in as failed",
				"%[1]d hands-on tests kept; %[2]s goes in as failed", len(r.Done), len(r.Done), i18n.S(r.Title))
		}
		what := []int{macResume, macFresh}
		if len(f.Configs) > 1 && !f.Unknown {
			a = append(a, Action{Label: i18n.T("Pick from the list"), Does: i18n.T("all %d configurations of this model", len(f.Configs)-1)})
			what = append(what, macPick)
		}
		return a, what
	}
	if f.Unknown {
		return []Action{{Label: i18n.T("Continue"), Does: i18n.T("test it anyway; the report can still be sent or saved")}}, []int{macGo}
	}
	a := []Action{{Label: i18n.T("That's my Mac"), Does: i18n.T("network next, then the quick pass")}}
	what := []int{macGo}
	if len(f.Configs) > 1 {
		pick := Action{Label: i18n.T("Pick from the list"), Does: i18n.T("all %d configurations of this model", len(f.Configs)-1)}
		if st.PassDone {
			pick.Does, pick.Off = i18n.T("the quick pass ran with this configuration"), true
		}
		a = append(a, pick)
		what = append(what, macPick)
	}
	return a, what
}

// resumeLine is Which Mac's line on an unfinished run's copy: when it was saved, by which
// version, how far it got, and the test the Mac didn't come back from.
func resumeLine(r *Resume) string {
	if len(r.Quick) == 0 {
		return i18n.T("Found a copy of an unfinished run on external storage: saved %[1]s UTC, %[2]d of %[3]d hands-on tests done. The Mac didn't come back from %[4]s. The copy has no quick pass results, so picking up runs the quick pass again, then carries on with the hands-on tests.",
			r.Saved.UTC().Format("2006-01-02 15:04"), len(r.Done), r.Of, i18n.S(r.Title))
	}
	return i18n.T("Found a copy of an unfinished run on external storage: saved %[1]s UTC, %[2]d of %[3]d hands-on tests done. The Mac didn't come back from %[4]s. Picking up keeps its quick pass results and carries on with the hands-on tests: %[4]s goes in as failed, then the next one.",
		r.Saved.UTC().Format("2006-01-02 15:04"), len(r.Done), r.Of, i18n.S(r.Title))
}

// otherLine is Which Mac's line on another version's copy (never picked up).
func otherLine(o *OtherCopy, version string) string {
	return i18n.T("Found a copy of an unfinished run from boot-live %[1]s (saved %[2]s UTC). This is boot-live %[3]s, so every test runs again; the copy stays on external storage, and DoesItOmarchy.com can still import it.",
		o.Version, o.Saved.UTC().Format("2006-01-02 15:04"), version)
}

func macLabel(m MacInfo) string {
	if m.Name == "" {
		return m.ID
	}
	return m.Name + " [" + m.ID + "]"
}

// ── Helpers shared by several screens ─────────────────────────────────────────

// wrap breaks s into lines of at most w cells at spaces.
func wrap(s string, w int) []string {
	var out []string
	var cur strings.Builder
	n := 0
	for _, word := range strings.Fields(s) {
		ww := width(word)
		if n > 0 && n+1+ww > w {
			out = append(out, cur.String())
			cur.Reset()
			n = 0
		}
		if n > 0 {
			cur.WriteByte(' ')
			n++
		}
		cur.WriteString(word)
		n += ww
	}
	if n > 0 {
		out = append(out, cur.String())
	}
	return out
}
