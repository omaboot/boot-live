package ui

import (
	"fmt"
	"math"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/omaboot/boot-live/internal/boot"
	"github.com/omaboot/boot-live/internal/command"
	g "github.com/omaboot/boot-live/internal/grid"
	"github.com/omaboot/boot-live/internal/i18n"
	"github.com/omaboot/boot-live/internal/pace"
)

// ── The command console ───────────────────────────────────────────────────────────
//
// A drop-down console in its own full-width box above the hint bar, hidden until toggled: `
// (the key left of 1; § on ISO Macs) or Ctrl+` (NUL on the console, which works in text fields
// too). The frame shrinks to make room, so nothing is hidden and the step track stays. Six rows:
// its edges, three output lines and the command line; Ctrl+↑ makes it tall (half the screen),
// Ctrl+↓ short again; PgUp/PgDn scroll, and the bottom edge counts
// what's hidden. Esc closes it (its hint reads "close"), ↑↓ go through the history, Tab takes the
// dim completion, ? lists the commands. The screen under it keeps running. It slides up over 3
// frames, and Boot glances down at it as it opens. The commands are internal/command's.

// conSlide is how long the box takes to slide up: 3 frames at 15 fps.
const conSlide = 0.2

// conAge is how long the console has been open (a state with no opening time, as tools/screens
// draws, counts as long open).
func conAge(st *State) float64 {
	if st.Con == nil || st.Con.At.IsZero() {
		return 1e9
	}
	return time.Since(st.Con.At).Seconds()
}

// conOpen reports whether the console is up.
func conOpen(st *State) bool { return st.Con != nil && st.Con.Open }

// conShown reports whether the console's box is on screen: it's up, and the screensaver isn't
// covering it (Boot dozes over the whole screen, the console box too; it's open as it was once he
// wakes).
func conShown(st *State) bool { return conOpen(st) && !st.Doze }

// conHeight is the rows the box takes this frame, 0 when closed: 6, or half the screen when tall,
// empty or not, sliding up as it opens.
func conHeight(gr *g.Grid, st *State) int {
	if !conShown(st) {
		return 0
	}
	full := command.Short
	if st.Con.Tall {
		full = gr.H / 2
	}
	return max(2, int(math.Round(float64(full)*min(1, conAge(st)/conSlide))))
}

// conAvail reports whether the console key works here: not while a hands-on test has the
// keyboard (it keeps it; the key dims), nor over the box the pulled stick brings up.
func conAvail(st *State) bool {
	if st.Overlay == StickOverlay {
		return false
	}
	h := st.HO
	return !(st.Scene == Test && h != nil && h.Run != nil && h.Out == nil && h.Run.TakesKeys())
}

// life is Boot's idle motion at t, his eyes down at the console while it opens.
func life(st *State, t float64) boot.Life {
	l := boot.LifeAt(t)
	l.Down = conOpen(st) && conAge(st) < 1.4
	if st.Con != nil {
		x := st.Con.X
		l.Gold, l.Cross = x.God, since(x.Down) < 2
		if a := since(x.Lift); a < 2 {
			l.Rise = int(math.Round(12 * math.Sin(math.Pi*a/2)))
		}
	}
	return l
}

// since is the seconds since t; a long time for none.
func since(t time.Time) float64 {
	if t.IsZero() {
		return 1e9
	}
	return time.Since(t).Seconds()
}

// drawTiming puts the frame timing in the blank row under the header, at its right end.
func drawTiming(gr *g.Grid, f command.Frames) {
	s := fmt.Sprintf("%.0f fps  %.1f ms", f.FPS, f.RenderMS)
	gr.Put(gr.W-2-width(s), 1, s, g.DIM)
}

// sayFor is how long Boot keeps a line from the console (boot say) at most.
const sayFor = 8 * time.Second

// Said is Boot's line from the console (boot say): it stands in for his own in
// his box on whatever screen is showing, for sayFor from when it first shows or until his own
// line there changes, then his own is back. Every box of his goes through it (sayBox, speech).
// A screen with no room for his box (the smallest console's with the console open) shows it
// once there is room.
type Said struct {
	Text string
	At   time.Time // when it first showed; zero (tools/screens): typed out long ago, and it stays
	Wait bool      // not shown yet: its time starts when it is
	// his own lines on the first frame it showed (own), and on the last one (seen)
	own, seen   string
	based, drew bool
}

// frame starts a frame: the line ends once its time is up or his own line has changed.
func (s *Said) frame() {
	if s.Text == "" {
		return
	}
	if !s.Wait && !s.At.IsZero() && time.Since(s.At) >= sayFor || s.drew && s.based && s.seen != s.own {
		*s = Said{}
		return
	}
	if s.drew && !s.based {
		s.own, s.based = s.seen, true
	}
	s.seen, s.drew = "", false
}

// age is how long it has shown, in seconds.
func (s *Said) age() float64 {
	if s.At.IsZero() {
		return 1e6
	}
	return time.Since(s.At).Seconds()
}

// over is what a box of Boot's shows at t (the screen's clock): his own lines, or the console's
// line in their place while it's up. His own are noted either way, to see when they change.
func (s *Said) over(lines []line, t float64) []line {
	if s == nil || s.Text == "" {
		return lines
	}
	if s.Wait {
		s.At, s.Wait = time.Now(), false
	}
	s.drew = true
	for _, l := range lines {
		if l.text != "" && t >= l.at {
			s.seen += l.text + "\n"
		}
	}
	return []line{{t - s.age(), s.Text}}
}

// drawConsole draws the box under the shrunk frame: bright white while it has the focus (the
// other colours keep their meanings; cyan is Boot's), the keys in its top edge, the output, the
// count of hidden lines in its bottom edge, and the command line: a grey > (Boot's lines use a
// violet one), the command white (red when it can't be one), its arguments blue, the completion dim.
func drawConsole(gr *g.Grid, b Box, h int, st *State) {
	c := st.Con
	x, y, w := b.X0, gr.H-2-h, b.X1-b.X0+1 // under the frame's bottom edge (b may be laid out taller: conFloor)
	box(gr, x, y, w, h, g.WHT, i18n.T("Console"), g.WHT)
	tips := " " + i18n.T("Tab complete · ↑↓ history · Ctrl+↑ taller") + " "
	if c.Tall {
		tips = " " + i18n.T("Tab complete · ↑↓ history · Ctrl+↓ shorter") + " "
	}
	if h >= 2 {
		gr.Put(x+w-2-width(tips), y, tips, g.DIM)
	}
	if h < 3 {
		return
	}
	gr.Fill(x+1, y+1, w-2, h-2, ' ', g.GRY, g.K)
	rows, iw := h-3, w-4
	if conAge(st) >= conSlide { // the box at its full height: its rows bound the scroll (Ctrl+↑/↓ too)
		c.Rows = rows
		c.ScrollBy(0)
	}
	end := max(0, len(c.Out)-c.Scroll)
	start := max(0, end-rows)
	old := gr.Clip(x+2, y+1, x+2+iw, y+h-1) // long lines are cut at the box's inside
	for i, l := range c.Out[start:end] {
		cx := x + 2
		for _, s := range l {
			if s.BG != g.K {
				cx = gr.PutBG(cx, y+1+i, s.Text, s.FG, s.BG) // the help list's tags
			} else {
				cx = gr.Put(cx, y+1+i, s.Text, s.FG)
			}
		}
	}
	gr.Restore(old)
	var more []string
	if start > 0 {
		more = append(more, "PgUp  "+i18n.T("%d more", start))
	}
	if c.Scroll > 0 {
		more = append(more, "PgDn  "+i18n.T("%d newer", c.Scroll))
	}
	if len(more) > 0 {
		m := " " + strings.Join(more, "  ·  ") + " "
		gr.Put(x+w-2-width(m), y+h-1, m, g.DIM)
	}
	iy := y + h - 2
	cx := gr.Put(x+2, iy, "> ", g.GRY)
	in := string(c.Input)
	shown := []rune(in)
	if len(shown) > iw-4 {
		shown = shown[len(shown)-(iw-4):]
	}
	name, rest, spaced := strings.Cut(string(shown), " ")
	nc := g.WHT
	if !c.Known(in) {
		nc = g.BRED
	}
	cx = gr.Put(cx, iy, name, nc)
	if spaced {
		cx = gr.Put(cx, iy, " "+rest, command.Arg)
	}
	ghost := c.Ghost
	gr.Clip(x+2, iy, x+w-2, iy+1)
	if !blinkOn(st.T) {
		gr.Put(cx, iy, "█", g.WHT)
	} else if ghost != "" {
		gr.Put(cx, iy, string([]rune(ghost)[:1]), g.DIM)
	}
	if r := []rune(ghost); len(r) > 1 {
		gr.Put(cx+1, iy, string(r[1:]), g.DIM)
	}
	gr.Restore(old)
}

// conMark is the frame's top edge on a run whose results were changed in the console, from the
// first such command on.
func conMark(gr *g.Grid, b Box, st *State) {
	if st.Con.Marked() {
		m := " " + i18n.T("results changed in the console") + " "
		gr.Put(b.X1-2-width(m), b.Y0, m, g.AMB)
	}
}

// conKey works the console's keys. ` and § toggle it except in a text field, where they type;
// Ctrl+` (NUL, "ctrl+@") toggles it anywhere. While it's open it takes every key but Ctrl+C.
// It reports whether the key was the console's.
func (m *Model) conKey(k tea.KeyMsg) (bool, tea.Cmd) {
	st := &m.st
	key := k.String()
	c := st.Con
	toggle := key == "ctrl+@" || (key == "`" || key == "§") && (c.Open || !m.textFocus())
	if !c.Open {
		if !toggle || !conAvail(st) {
			return false, nil
		}
		c.Open, c.At, c.Input, c.HistAt = true, time.Now(), nil, -1
		m.bugWaiting()
		return true, nil
	}
	env := conEnv{m}
	switch {
	case toggle, key == "esc":
		c.Open = false
	case key == "enter":
		line := string(c.Input)
		c.Input = nil
		c.Exec(env, line)
	case key == "tab":
		c.Tab(env)
	case key == "backspace":
		if n := len(c.Input); n > 0 {
			c.Input = c.Input[:n-1]
		}
		c.HistAt = -1
	case key == "ctrl+up":
		c.Tall = true
	case key == "ctrl+down":
		c.Tall = false
	case key == "up", key == "down":
		d := 1
		if key == "up" {
			d = -1
		}
		c.History(d)
	case key == "pgup":
		c.ScrollBy(3)
	case key == "pgdown":
		c.ScrollBy(-3)
	case k.Type == tea.KeyRunes:
		for _, r := range k.Runes {
			if r >= ' ' && r != 0x7f && len(c.Input) < 200 {
				c.Input = append(c.Input, r)
			}
		}
		c.HistAt = -1
	case k.Type == tea.KeySpace:
		c.Input = append(c.Input, ' ')
		c.HistAt = -1
	}
	c.Refresh(env)
	cmd := m.conCmd // a command's own (font: the console repaints)
	m.conCmd = nil
	return true, cmd
}

// conPrint prints a later answer into the console (rerun, bugreport).
func (m *Model) conPrint(ls ...command.Line) { m.st.Con.Print(ls...) }

// Messages for the console's commands that answer later.
type (
	// RerunMsg is a quick-pass check measured again: its new results (the earlier one is in
	// their evidence), or why it couldn't run.
	RerunMsg struct {
		ID     string
		Checks []Check
		Err    string
	}
	// SleepCheckMsg is sleepcheck's progress: lines for the console; Back after a step that
	// suspended (the screen repaints), Done once it's over.
	SleepCheckMsg struct {
		Lines      []command.Line
		Back, Done bool
	}
	// BugMsg says where bugreport saved its file on the stick, and how big it is, or why it didn't.
	BugMsg struct {
		Path  string
		Bytes int
		Err   string
	}
	// BugSentMsg says how sending a bug report to OmaBoot? went (PF-13): its code, or that it
	// waits on the stick for an online run (Later), or why it didn't go.
	BugSentMsg struct {
		Path  string
		Code  string
		Later bool
		Err   string
	}
	// TestFileMsg says how testfile write went: where the test file was read back from, how long
	// it took, whether it was there already, or why it didn't go.
	TestFileMsg struct {
		Dev, Where string
		Took       time.Duration
		Already    bool
		Err        string
	}
)

// testFileDone says how testfile write went.
func (m *Model) testFileDone(msg TestFileMsg) {
	switch {
	case msg.Err != "":
		m.conPrint(command.Line{command.S("testfile: "+msg.Err, command.Bad)})
	case msg.Already:
		m.conPrint(command.Line{command.S(msg.Dev, command.Arg), command.T(" " + i18n.T("already has the test file, byte for byte: left as it is"))})
	default:
		m.conPrint(command.Line{command.S(i18n.T("written")+" ", command.Value), command.S(msg.Where, command.Arg),
			command.T("  ·  " + i18n.T("32 MiB in %.1f s  ·  read back from the drive: identical  ·  unmounted", msg.Took.Seconds()))})
		m.conPrint(command.Line{command.S(i18n.T("the USB, SD card and FireWire tests will read it."), command.Label)})
	}
}

// rerunDone takes a check measured again: the new result replaces the old on the screens (the
// quick pass's timing stays), and the console says how it came out.
func (m *Model) rerunDone(msg RerunMsg) {
	m.rerunning = ""
	if msg.Err != "" {
		m.conPrint(command.Line{command.S("rerun "+msg.ID+": "+msg.Err, command.Bad)})
		return
	}
	for _, c := range msg.Checks {
		was, found := pace.Skip, false
		for i := range m.f.Checks {
			if m.f.Checks[i].ID == c.ID {
				was, found = m.f.Checks[i].Status, true
				c.Begin, c.Finish = m.f.Checks[i].Begin, m.f.Checks[i].Finish
				m.f.Checks[i] = c
			}
		}
		if !found {
			m.f.Checks = append(m.f.Checks, c)
		}
		delete(m.f.Forced, c.ID) // the new measurement decides
		note := i18n.T("was %s; the report keeps both", command.StatusWord(statusWord(was)))
		if was == c.Status {
			note = i18n.T("same as before; the report keeps both")
		}
		m.conPrint(command.Line{command.S(c.ID, command.Arg), command.S("  ", command.Label),
			command.S(command.StatusWord(statusWord(c.Status)), command.StatusColor(statusWord(c.Status))), command.S("  ("+note+")", command.Label)})
	}
}

// bugDone says where bugreport's file went.
func (m *Model) bugDone(msg BugMsg) {
	if msg.Err != "" && m.bugFinish {
		m.bugFinish, m.bugSend = false, false
		m.st.BugSending, m.st.BugNote = false, i18n.T("not saved, so not sent: %s", msg.Err)
	}
	if msg.Err != "" {
		m.conPrint(command.Line{command.S("bugreport: "+msg.Err, command.Bad)})
		return
	}
	m.conPrint(command.Line{command.S(i18n.T("saved:")+" ", command.Label), command.S(msg.Path, command.Arg),
		command.T(" " + i18n.T("on external storage, %s", size(msg.Bytes)))})
	send := m.bugSend
	m.bugSend = false
	switch {
	case !m.f.CanSendBug: // bugreport keeps the kernel log whole: it may name serial numbers
		m.conPrint(command.Line{command.S(i18n.T("saved to external storage only; may contain serial numbers"), command.Label)})
	case send:
		m.be.SendBug(msg.Path)
	default:
		m.conPrint(command.Line{command.S(i18n.T("the copy on the stick is whole (it may name serial numbers)."), command.Label)},
			command.Line{command.S("bugreport send", command.Arg), command.T(" " + i18n.T("sends it to OmaBoot? to look into: scrubbed of serial numbers, addresses and names first; kept private for 180 days; read by OmaBoot?'s maintainers and an AI bug hunt (omaboot.live/privacy)."))})
	}
}

// bugSentDone prints how sending a bug report went (PF-13).
func (m *Model) bugSentDone(msg BugSentMsg) {
	if m.bugFinish {
		m.bugFinish = false
		st := &m.st
		st.BugSending, st.BugSel = false, 0
		switch {
		case msg.Code != "":
			st.BugCode = msg.Code
		case msg.Later:
			st.BugNote = i18n.T("Not sent yet (%s): it waits on external storage, and the console's bugreport send sends it from an online run.", msg.Err)
		default:
			st.BugNote = i18n.T("Not sent: %s. A whole copy is on external storage.", msg.Err)
		}
	}
	name := msg.Path
	switch {
	case msg.Code != "":
		m.conPrint(command.Line{command.S(i18n.T("sent:")+" ", command.Label), command.S(name, command.Arg), command.T(" " + i18n.T("as") + " "), command.S(msg.Code, command.Arg)},
			command.Line{command.S(i18n.T("Quote %s when you tell us about the problem.", msg.Code), command.Label)})
	case msg.Later:
		m.conPrint(command.Line{command.S(i18n.T("not sent yet:")+" ", command.Label), command.S(name, command.Arg), command.T(" " + i18n.T("(%s); it waits on the stick: bugreport send in an online run sends it", msg.Err))})
	default:
		m.conPrint(command.Line{command.S("bugreport send: "+msg.Err, command.Bad)})
	}
}

// bugWaiting tells the console, once, about bug reports on the stick that wait to be sent.
func (m *Model) bugWaiting() {
	if m.bugTold || !m.f.CanSendBug || m.f.BugsWaiting == 0 {
		return
	}
	m.bugTold = true
	m.conPrint(command.Line{command.S(i18n.N("%d bug report on the stick waits to be sent:", "%d bug reports on the stick wait to be sent:", m.f.BugsWaiting, m.f.BugsWaiting)+" ", command.Label), command.S("bugreport send", command.Arg)})
}

func size(n int) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%d KB", (n+1023)/1024)
	}
	return fmt.Sprintf("%d B", n)
}

// statusWord is a screen status as the console's commands name it.
func statusWord(s pace.Status) string {
	switch s {
	case pace.Pass:
		return command.Pass
	case pace.Partial:
		return command.Partial
	case pace.Fail:
		return command.Fail
	case pace.Live:
		return command.Live
	}
	return command.Skip
}

// paceOf is a console status word as the screens show it.
func paceOf(w string) pace.Status {
	switch w {
	case command.Pass:
		return pace.Pass
	case command.Partial:
		return pace.Partial
	case command.Fail:
		return pace.Fail
	case command.Live:
		return pace.Live
	}
	return pace.Skip
}

// frameStats measures the frames for fps: drawing time and rows redrawn, averaged over a second.
type frameStats struct {
	prev     []string
	n, rows  int
	draw     time.Duration
	since    time.Time
	last     command.Frames
	measured bool
}

// add counts one frame drawn in d whose rows are lines.
func (s *frameStats) add(lines []string, d time.Duration, w, h int) {
	if s.since.IsZero() {
		s.since = time.Now()
	}
	changed := 0
	for i, l := range lines {
		if i >= len(s.prev) || s.prev[i] != l {
			changed++
		}
	}
	s.prev = lines
	s.n, s.rows, s.draw = s.n+1, s.rows+changed, s.draw+d
	if el := time.Since(s.since); el >= time.Second {
		s.last = command.Frames{RenderMS: float64(s.draw.Microseconds()) / 1000 / float64(s.n), FPS: float64(s.n) / el.Seconds(),
			Rows: s.rows / s.n, W: w, H: h}
		s.n, s.rows, s.draw, s.since, s.measured = 0, 0, 0, time.Now(), true
	}
}

// ScriptConsole types console input on a model of this state, for tools/screens: each step is
// typed as it is, except "\t" (Tab), "\n" (Enter), "^up"/"^down" (Ctrl+↑/↓), "pgup", "pgdown" and
// "esc". The console opens first. It returns the state afterwards.
func ScriptConsole(o Options, st State, f *Facts, be Backend, steps ...string) State {
	m := &Model{o: o, st: st, f: f, be: be, began: time.Now(), lastKey: time.Now()}
	m.player = NewPlayer(f.Checks, len(f.Checks))
	if m.st.Con == nil {
		m.st.Con = command.New()
	}
	m.st.Con.Open, m.st.Con.HistAt = true, -1
	for i := range m.seen {
		m.seen[i] = true
	}
	for _, s := range steps {
		var k tea.KeyMsg
		switch s {
		case "\t":
			k = tea.KeyMsg{Type: tea.KeyTab}
		case "\n":
			k = tea.KeyMsg{Type: tea.KeyEnter}
		case "^up":
			k = tea.KeyMsg{Type: tea.KeyCtrlUp}
		case "^down":
			k = tea.KeyMsg{Type: tea.KeyCtrlDown}
		case "pgup":
			k = tea.KeyMsg{Type: tea.KeyPgUp}
		case "pgdown":
			k = tea.KeyMsg{Type: tea.KeyPgDown}
		case "esc":
			k = tea.KeyMsg{Type: tea.KeyEsc}
		default:
			for _, r := range s {
				if r == ' ' {
					m.conKey(tea.KeyMsg{Type: tea.KeySpace, Runes: []rune{' '}})
				} else {
					m.conKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
				}
			}
			continue
		}
		m.conKey(k)
	}
	return m.st
}
