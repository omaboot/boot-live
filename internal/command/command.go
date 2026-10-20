// Package command is the console's command registry (LIVE-PLAN §24, owner 2026-10-14, prototype
// v52): a drop-down console in its own box above the hint bar, on every screen, in dev and
// release builds. Each command is one file that registers itself from init, like the hands-on
// tests (internal/handson); the core here (parsing, completion, history, output, the run's
// marks) never changes when a command is added. Commands reach the tool only through Env.
//
// Every command has a kind: read-only commands only look (why, log, ids, report, note,
// bugreport); redo commands measure again (rerun: the report records it, the run stays
// sendable); commands that change results (force) mark the run: DoesItOmarchy.com won't take
// its report, staging and external storage still do; drive-write commands write a file to a drive
// the person names, after asking (testfile), and touch no result. Dev-only commands live in files built with the
// omaboot_dev tag, so release binaries don't contain them at all.
package command

import (
	"sort"
	"strings"

	g "github.com/omaboot/boot-live/internal/grid"
	"github.com/omaboot/boot-live/internal/i18n"
)

// Kind is what a command may do to the run (owner, 2026-10-14: three kinds; 2026-10-17: drive-write).
type Kind int

const (
	Read   Kind = iota // looks only: never recorded in the report
	Redo               // measures again: the report records it, the run stays sendable
	Change             // sets a result by hand: the run is marked, DoesItOmarchy.com refuses its report
	Write              // writes a file to a drive the person names, after asking; no result changes, never recorded
)

// Word is the kind as the report's console entries name it ("" for read-only: never recorded).
func (k Kind) Word() string {
	switch k {
	case Redo:
		return "redo"
	case Change:
		return "change"
	}
	return ""
}

// Cmd is one command's registry entry.
type Cmd struct {
	Name  string
	Args  string // the arguments' synopsis (msgid, marked with i18n.M), e.g. "<test>"
	Help  string // what it does (msgid, marked with i18n.M)
	Kind  Kind
	Dev   bool // dev builds only (its file carries the omaboot_dev build tag)
	Order int  // its place in the help list
	// Hidden commands work but aren't listed (help, which is ?).
	Hidden bool
	// Unlisted commands are neither listed nor offered by Tab.
	Unlisted bool
	// Off, if set, says when the command doesn't exist: typing it gets "no such command".
	Off func(c *Console) bool
	// Complete offers the next argument's values, given the arguments before it.
	Complete func(env Env, args []string) []string
	// Run does the command, printing to out. An error is printed in red under the command's
	// name; a command that fails is neither recorded in the report nor marks the run.
	Run func(env Env, args []string, out *Out) error
	// Marks, if set, says whether a Change command with these arguments changes a result (the
	// prototype's "fix list" looks, "fix off" changes); nil: every successful run does.
	Marks func(args []string) bool
}

var registry = map[string]*Cmd{}

// Register adds a command; each command's file calls it from init.
func Register(c Cmd) {
	if _, dup := registry[c.Name]; dup {
		panic("command: " + c.Name + " registered twice")
	}
	registry[c.Name] = &c
}

// Lookup finds a command by name.
func Lookup(name string) (*Cmd, bool) {
	c, ok := registry[name]
	return c, ok
}

// All are the registered commands in the help list's order.
func All() []*Cmd {
	out := make([]*Cmd, 0, len(registry))
	for _, c := range registry {
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Order != out[j].Order {
			return out[i].Order < out[j].Order
		}
		return out[i].Name < out[j].Name
	})
	return out
}

// Seg is a run of text in one colour: fg on bg (bg 0–7, the console's backgrounds).
type Seg struct {
	Text   string
	FG, BG g.Color
}

// Line is one output line.
type Line []Seg

// Text is the line without its colours, for the full log.
func (l Line) Text() string {
	var b strings.Builder
	for _, s := range l {
		b.WriteString(s.Text)
	}
	return b.String()
}

// Output colours (owner, 2026-10-14, v50): the command white (red when it's no command), its
// arguments blue (test IDs, paths, values), labels and timestamps dim, text grey, values white,
// results in their result colours (green only for a pass), the kernel log's driver blue with
// warnings amber and errors red, JSON keys grey, strings blue, numbers white, anything that marks
// the run amber, Boot's words cyan.
const (
	Arg   = g.BBLU
	Label = g.DIM
	Text  = g.GRY
	Value = g.WHT
	Bad   = g.BRED
	Mark  = g.AMB
	Boot  = g.BCYN
)

// S is a segment in a colour; T a grey one.
func S(text string, fg g.Color) Seg { return Seg{Text: text, FG: fg} }

// T is a segment of plain grey text.
func T(text string) Seg { return Seg{Text: text, FG: Text} }

// Out collects a command's output lines.
type Out struct {
	Lines []Line
	Clear bool // empty the box first (clear)
	Close bool // close the console after (close, quit)
	// Unlogged: the lines show in the box but stay out of the full log (L), which keeps the
	// command line itself: the help list (owner, 0.8.0 review).
	Unlogged bool
}

// Line adds a line.
func (o *Out) Line(segs ...Seg) { o.Lines = append(o.Lines, Line(segs)) }

// Dim adds a line of dim text.
func (o *Out) Dim(s string) { o.Line(S(s, Label)) }

// Error is a command's failure as the console shows it: in red, after the command's name.
type Error string

func (e Error) Error() string { return string(e) }

// Err makes an Error from text already translated (i18n.T).
func Err(s string) error { return Error(s) }

// Status words for results, as the console prints them, and their colours.
const (
	Pass    = "pass"
	Partial = "partial"
	Fail    = "fail"
	Live    = "live" // a live boot can't decide it: needs Omarchy installed
	Skip    = "skip"
)

// StatusColor is a result word's colour: green only for a pass.
func StatusColor(s string) g.Color {
	switch s {
	case Pass:
		return g.BGRN
	case Partial:
		return g.AMB
	case Fail:
		return g.BRED
	case Live:
		return g.YEL
	}
	return g.DIM
}

// StatusWord is a result word as shown (translated).
func StatusWord(s string) string {
	switch s {
	case Pass:
		return i18n.T("pass")
	case Partial:
		return i18n.T("partial")
	case Fail:
		return i18n.T("fail")
	case Live:
		return i18n.T("needs Omarchy installed")
	}
	return i18n.T("not tested")
}
