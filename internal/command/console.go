package command

import (
	"strings"
	"time"

	g "github.com/omaboot/boot-live/internal/grid"
	"github.com/omaboot/boot-live/internal/i18n"
)

// Short is the box's height closed down: its edges, 3 output lines and the command line.
const Short = 6

// keep is how many output lines the box holds; the full log keeps them all.
const keep = 1000

// Console is the console's state: what the screens draw and the keys work on.
type Console struct {
	Open   bool
	At     time.Time // when it opened: it slides up over 3 frames, Boot glances down
	Tall   bool      // Ctrl+↑: as tall as its output, up to half the screen
	Input  []rune
	Hist   []string
	HistAt int // the history entry ↑↓ show, -1 for the line being typed
	Out    []Line
	Scroll int // lines scrolled back from the newest (PgUp, PgDn), at most MaxScroll
	Rows   int // output rows the box showed last (0: not drawn yet, the short box's)
	// Marks are the commands that changed results: the run is marked from the first one on
	// (the frame's top edge says so, Finish's Submit opens the amber box).
	Marks []string
	// Log is every command and its output as text, for the full log (L): clear doesn't touch it.
	Log []string
	// Ghost is what Tab would add to the line being typed, shown dim after the cursor (Refresh).
	Ghost string
	X     Extra
	// Ask is a command waiting for its y (testfile write): the next line answers it, nothing else.
	Ask *Ask
}

// Ask is a question a command asks before it does something that can't be taken back: y goes
// on (Then), any other line doesn't (No says so).
type Ask struct {
	Name string // the command asking, for its errors
	Then func(env Env, out *Out) error
	No   string
}

// New is a closed console.
func New() *Console { return &Console{HistAt: -1} }

// Marked reports whether a command changed a result in this run.
func (c *Console) Marked() bool { return c != nil && len(c.Marks) > 0 }

// Print adds output lines (a command's later answer, too) and logs them.
func (c *Console) Print(ls ...Line) {
	for _, l := range ls {
		c.Out = append(c.Out, l)
		c.Log = append(c.Log, l.Text())
	}
	if n := len(c.Out); n > keep {
		c.Out = append([]Line(nil), c.Out[n-keep:]...)
	}
	if c.Scroll > 0 { // scrolled back: what's on screen stays put
		c.Scroll = min(c.Scroll+len(ls), c.MaxScroll())
	}
}

// MaxScroll is how far back the box scrolls: until the oldest line is at its top, so a full
// page always shows (PgUp once cleared the box, 0.7.2 review).
func (c *Console) MaxScroll() int {
	rows := c.Rows
	if rows <= 0 {
		rows = Short - 3
	}
	return max(0, len(c.Out)-rows)
}

// ScrollBy scrolls back (d > 0) or forward, within 0..MaxScroll.
func (c *Console) ScrollBy(d int) { c.Scroll = max(0, min(c.MaxScroll(), c.Scroll+d)) }

// Clear empties the box (the full log keeps everything).
func (c *Console) Clear() { c.Out, c.Scroll = nil, 0 }

// Split is a command line's words: the command and its arguments. Runs of spaces count as one.
func Split(line string) (name string, args []string) {
	f := strings.Fields(line)
	if len(f) == 0 {
		return "", nil
	}
	return f[0], f[1:]
}

// lookup finds a command that exists now (Off).
func (c *Console) lookup(name string) (*Cmd, bool) {
	cmd, ok := Lookup(name)
	if ok && cmd.Off != nil && cmd.Off(c) {
		return nil, false
	}
	return cmd, ok
}

// Exec runs a command line: it echoes it, runs the command, records a redo or change command for
// the report, and marks the run after a change. "" does nothing.
func (c *Console) Exec(env Env, line string) {
	line = strings.Join(strings.Fields(line), " ")
	if line == "" {
		return
	}
	if n := len(c.Hist); n == 0 || c.Hist[n-1] != line {
		c.Hist = append(c.Hist, line)
	}
	c.HistAt, c.Scroll = -1, 0
	if a := c.Ask; a != nil { // a command asked: this line answers it
		c.Ask = nil
		c.Print(Line{S("> ", Label), S(line, Value)})
		if line != "y" && line != "yes" {
			c.Print(Line{S(a.No, Label)})
			return
		}
		var out Out
		err := a.Then(env, &out)
		c.Print(out.Lines...)
		if err != nil {
			c.Print(Line{S(a.Name+": "+err.Error(), Bad)})
		}
		return
	}
	name, args := Split(line)
	cmd, ok := c.lookup(name)
	echo := Line{S("> ", Label), S(name, Value)}
	if !ok {
		echo[1].FG = Bad
	}
	if len(args) > 0 {
		echo = append(echo, S(" "+strings.Join(args, " "), Arg))
	}
	c.Print(echo)
	if !ok {
		c.Print(Line{S(i18n.T("%s: no such command; ? lists them", name), Bad)})
		return
	}
	var out Out
	err := cmd.Run(env, args, &out)
	if out.Clear {
		c.Clear()
	}
	if out.Unlogged {
		n := len(c.Log)
		c.Print(out.Lines...)
		c.Log = c.Log[:n]
	} else {
		c.Print(out.Lines...)
	}
	if out.Close {
		c.Open = false
	}
	if err != nil {
		c.Print(Line{S(name+": "+err.Error(), Bad)})
		return
	}
	if cmd.Kind == Redo || cmd.Kind == Change {
		env.Record(line, cmd.Kind)
	}
	if cmd.Kind == Change && (cmd.Marks == nil || cmd.Marks(args)) {
		c.Marks = append(c.Marks, line)
		c.Print(Line{S(i18n.T("this run is marked: DoesItOmarchy.com won't take its report (staging and external storage will)"), Mark)})
	}
}

// Complete is what Tab would add to the line being typed (the common start of every match,
// and a space after a single one), and the matches. The first word completes over the commands
// (not ?), the others over the command's own values.
func Complete(env Env, input string) (rest string, matches []string) {
	parts := strings.Split(input, " ")
	word := parts[len(parts)-1]
	var pool []string
	if len(parts) == 1 {
		if word == "" {
			return "", nil
		}
		for _, c := range All() {
			if c.Name != "?" && !c.Unlisted {
				pool = append(pool, c.Name)
			}
		}
	} else {
		name, args := Split(strings.Join(parts[:len(parts)-1], " "))
		if c, ok := Lookup(name); ok && !c.Unlisted && c.Complete != nil {
			pool = c.Complete(env, args)
		}
	}
	for _, p := range pool {
		if strings.HasPrefix(p, word) {
			matches = append(matches, p)
		}
	}
	if len(matches) == 0 {
		return "", nil
	}
	pre := matches[0]
	for _, m := range matches[1:] {
		for !strings.HasPrefix(m, pre) {
			pre = pre[:len(pre)-1]
		}
	}
	rest = pre[len(word):]
	if len(matches) == 1 {
		rest += " "
	}
	return rest, matches
}

// Tab takes the completion; with nothing to add and several matches, it lists them.
func (c *Console) Tab(env Env) {
	rest, m := Complete(env, string(c.Input))
	switch {
	case rest != "":
		c.Input = append(c.Input, []rune(rest)...)
	case len(m) > 1:
		c.Print(Line{S(strings.Join(m, "  "), Label)})
	}
}

// History moves through the commands typed before: d = -1 older (↑), +1 newer (↓); past the
// newest the line is empty again.
func (c *Console) History(d int) {
	n := len(c.Hist)
	if n == 0 {
		return
	}
	switch {
	case d < 0 && c.HistAt < 0:
		c.HistAt = n - 1
	case d < 0:
		c.HistAt = max(0, c.HistAt-1)
	case c.HistAt < 0:
		return
	case c.HistAt+1 >= n:
		c.HistAt = -1
	default:
		c.HistAt++
	}
	c.Input = nil
	if c.HistAt >= 0 {
		c.Input = []rune(c.Hist[c.HistAt])
	}
}

// Refresh works out the completion shown after the cursor, after a key: none while ↑↓ show the
// history.
func (c *Console) Refresh(env Env) {
	c.Ghost = ""
	if c.HistAt < 0 {
		r, _ := Complete(env, string(c.Input))
		c.Ghost = strings.TrimRight(r, " ")
	}
}

// Known reports how the command word being typed reads: a command (or the start of one, before
// the first space) shows white, anything else red.
func (c *Console) Known(input string) bool {
	name, _, spaced := strings.Cut(input, " ")
	if _, ok := c.lookup(name); ok || spaced {
		return ok
	}
	for _, cmd := range All() {
		if !cmd.Unlisted && strings.HasPrefix(cmd.Name, name) {
			return true
		}
	}
	return false
}

// Tag is a kind's tag in the help list (owner, v51/v52): read-only plain dim text, redo white on
// blue, modifies results (inadmissible) black on amber (amber marks the run), drive-write white on
// violet (v55).
func Tag(k Kind) Seg {
	switch k {
	case Write:
		return Seg{Text: " " + i18n.T("drive-write") + " ", FG: g.WHT, BG: g.VIO}
	case Redo:
		return Seg{Text: " " + i18n.T("redo") + " ", FG: g.WHT, BG: g.BLU}
	case Change:
		return Seg{Text: " " + i18n.T("modifies results (inadmissible)") + " ", FG: g.K, BG: g.AMB}
	}
	return Seg{Text: " " + i18n.T("read-only") + " ", FG: Label}
}
