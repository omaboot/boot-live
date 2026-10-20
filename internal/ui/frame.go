// Package ui draws the screens: every S1 screen sits in one violet frame, with the status line
// above it, the key hints and version below it, the screen's title set into its top edge and the
// step track set into its bottom edge. Screens draw into a grid.Grid at a scene time t, so the
// same code serves Bubble Tea and the screen dumps.
package ui

import (
	"strings"

	"github.com/omaboot/boot-live/internal/command"
	g "github.com/omaboot/boot-live/internal/grid"
	"github.com/omaboot/boot-live/internal/i18n"
	"github.com/omaboot/boot-live/internal/icons"
	"github.com/omaboot/boot-live/internal/pace"
)

// Edition is the image the tool runs in.
type Edition struct {
	Key       string // live, omarchy
	Name      string // shown after "OmaBoot?"
	Installer bool   // only OmaBoot? Omarchy has the Omarchy installer
}

// Editions, as named in LIVE-PLAN §15.
var (
	Live    = Edition{Key: "live", Name: "Live"}
	Omarchy = Edition{Key: "omarchy", Name: "Omarchy", Installer: true}
)

// Product is the edition's full name, e.g. "OmaBoot? Live".
func (e Edition) Product() string { return "OmaBoot? " + e.Name }

// Options are fixed for a run.
type Options struct {
	Edition Edition
	Plain   bool // Boot layer off: no sprite, no band effects, no Boot lines
	Mode    g.Mode
	Palette g.Palette
	Version string // e.g. "0.1.0 dev 4e1c9a2"
	Font    string // console font name, "" for a terminal emulator or the console's own font
	FontPx  int
	Term    bool // running in a terminal emulator
	// Blank blanks (true) or unblanks the console for the screensaver, and reports whether it
	// did; nil in a terminal emulator (the screen is drawn black instead).
	Blank func(on bool) bool
	// SetFont loads omaboot-v at another cell height (the console's font command, dev); nil in
	// a terminal emulator.
	SetFont func(px int) error
}

// Box is the frame's geometry: the outer edges (X0…Y1) and the content area inside them.
type Box struct {
	X, Y, W, H     int // content: one blank column of padding inside the edges
	X0, Y0, X1, Y1 int
	FC             g.Color
}

func width(s string) int { return len([]rune(s)) }

// header is the status line above the frame: product, Mac and configuration on the left;
// Omarchy version and network state on the right. No bullets between the items: four blank
// cells set them apart (owner, 2026-10-13, prototype v44).
func header(gr *g.Grid, o *Options, right string, mac bool, m MacInfo) {
	gr.Fill(0, 0, gr.W, 1, ' ', g.GRY, g.K)
	end := gr.W - width(right) - 3 // the left side stops short of the right one
	x := gr.Put(1, 0, "OmaBoot", g.WHT)
	x = gr.Put(x, 0, "?", g.BVIO)
	x = gr.Put(x, 0, " "+o.Edition.Name, g.WHT)
	x += 4
	if mac {
		x = gr.Put(x, 0, cut(m.Name+" ["+m.ID+"]", end-x), g.GRY)
		if x+4+width(m.Config) <= end {
			gr.Put(x+4, 0, m.Config, g.DIM)
		}
	} else {
		gr.Put(x, 0, i18n.T("finding this Mac…"), g.DIM)
	}
	x = gr.W - width(right) - 1
	for _, r := range right { // icons in Boot's cyan, the words dim
		c := g.DIM
		if r >= 0xe000 && r <= 0xf8ff || r >= 0xf0000 {
			c = g.BCYN
		}
		gr.Set(x, 0, r, c)
		x++
	}
}

// MacInfo names the Mac in the header.
type MacInfo struct{ Name, ID, Config string }

// Keys says which of the hint bar's keys work on a screen. The bar holds only the secondary keys,
// the same four in the same places on every framed screen; a key that works has its cap
// highlighted, one that doesn't stays in place, dimmed. Primary actions are never there: they
// are the ▶ list on the screen itself (↑↓, Enter).
type Keys struct {
	Esc, Back, Log bool
	EscClose       bool // Esc reads "close" (the read-only report, the full log, the console)
	EscEnd         bool // Esc reads "end tests" (the hands-on tests: it ends them, asking first)
	Con            bool // ` opens the console (frame() sets it: everywhere but a test that has the keyboard)
	Sound, SoundOn bool // M turns Boot's sounds on or off (frame() sets it, soundKey), and they're on
}

// frameSpec is what a screen puts in its frame.
type frameSpec struct {
	title string
	right string // the header's right side
	mac   bool   // the Mac is known
	keys  Keys
	step  int      // 0–6 (stepFinish is the last), or -1 for no step track
	ho    *HandsOn // the hands-on tests, for their step's "Hands on (x of N)"; nil before they're known
	hot   bool     // the frame in the bright accent: "Boot needs you" calling
}

// frame draws the header, the frame, its title, the step track, the hint bar and the version.
// An open console takes rows above the hint bar: the frame shrinks to make room, and what the
// screen draws below its bottom edge is cut (owner, v49: nothing hidden, the step track stays;
// tall mode cuts a full screen). Render draws the box itself.
func frame(gr *g.Grid, o *Options, st *State, m MacInfo, f frameSpec) Box {
	if st.Sound.On { // the speaker, while Boot's sounds are on
		f.right = icons.Speaker + "    " + f.right
	}
	header(gr, o, f.right, f.mac, m)
	ch := conHeight(gr, st)
	x0, y0, x1, y1 := 2, 2, gr.W-3, gr.H-3-ch
	fc := g.VIO
	if f.hot {
		fc = g.BVIO
	}
	gr.Put(x0, y0, "┌"+strings.Repeat("─", max(0, x1-x0-1))+"┐", fc)
	for y := y0 + 1; y < y1; y++ {
		gr.Put(x0, y, "│", fc)
		gr.Put(x1, y, "│", fc)
	}
	gr.Put(x0, y1, "└"+strings.Repeat("─", max(0, x1-x0-1))+"┘", fc)
	if f.title != "" {
		gr.Put(x0+2, y0, " ", fc)
		gr.Put(gr.Put(x0+3, y0, f.title, g.WHT), y0, " ", fc)
	}
	// the version at the right end of the hint bar
	ver := o.Edition.Product() + " " + o.Version
	gr.Put(x1-width(ver)+1, gr.H-1, ver, g.DIM)
	f.keys.Con = conAvail(st)
	f.keys.Sound, f.keys.SoundOn = soundKey(st), st.Sound.On
	if conShown(st) { // Esc closes the console first
		f.keys.Esc, f.keys.EscClose, f.keys.EscEnd = true, true, false
	}
	hintBar(gr, x0+1, gr.H-1, f.keys)
	b := Box{X: x0 + 2, Y: y0 + 1, W: x1 - x0 - 3, H: y1 - y0 - 1, X0: x0, Y0: y0, X1: x1, Y1: y1, FC: fc}
	conMark(gr, b, st)
	if f.step >= 0 {
		steps(gr, b, f.step, f.ho)
	}
	if ch > 0 {
		gr.Clip(0, 0, gr.W, y1)
		// a frame shrunk below what the screens are laid out for (the smallest screen with the
		// short console) keeps that layout and is cut at its bottom edge: what's lowest is hidden,
		// never drawn onto another line's row (owner, 2026-10-14)
		if b.H < conFloor {
			b.H = conFloor
			b.Y1 = b.Y + b.H
		}
	}
	return b
}

// conFloor is the least content height the screens are laid out for with the console open: the
// smallest screen's frame (MinH) with the short console. A taller console cuts the screen below it.
const conFloor = MinH - 6 - command.Short

// hintBar draws Esc, delete (Backspace), L, ` and M with what they do (M's label says whether
// Boot's sounds are on). A key that works here has its
// cap in reverse video (dark on the frame's violet); one that doesn't keeps its place, all dim.
// omaboot-v has no Backspace symbol (U+232B), so its cap reads "delete", the word printed on that
// key on Mac keyboards. ` is the console's, on every screen (owner, 2026-10-14).
func hintBar(gr *g.Grid, x, y int, k Keys) {
	esc := i18n.T("quit")
	switch {
	case k.EscClose:
		esc = i18n.T("close")
	case k.EscEnd:
		esc = i18n.T("end tests")
	}
	sound := i18n.T("sound off")
	if k.SoundOn {
		sound = i18n.T("sound on")
	}
	hints := []struct {
		cap, label string
		on         bool
		labels     []string // every label the key can show: its slot fits the longest, so keys never move
	}{
		{"Esc", esc, k.Esc, []string{i18n.T("quit"), i18n.T("close"), i18n.T("end tests")}},
		{i18n.T("delete"), i18n.T("back"), k.Back, []string{i18n.T("back")}},
		{"L", i18n.T("full log"), k.Log, []string{i18n.T("full log")}},
		{string(icons.ConsoleKey), i18n.T("console"), k.Con, []string{i18n.T("console")}},
		{"M", sound, k.Sound, []string{i18n.T("sound on"), i18n.T("sound off")}},
	}
	for _, h := range hints {
		if h.on {
			gr.PutBG(x, y, " "+h.cap+" ", g.K, g.VIO)
		} else {
			gr.PutBG(x, y, " "+h.cap+" ", g.DIM, g.K)
		}
		gr.Put(x+width(h.cap)+3, y, h.label, g.DIM)
		lw := 8
		for _, l := range h.labels {
			lw = max(lw, width(l))
		}
		x += width(h.cap) + 3 + lw + 4
	}
}

// Steps of the S1 flow, shown in the frame's bottom edge.
// The step track's steps; the hands-on tests are a step of their own (owner, 2026-10-11).
const (
	stepHandsOn = 5
	stepFinish  = 6
)

func stepNames(ho *HandsOn) []string {
	return []string{i18n.T("Hello"), i18n.T("Which Mac"), i18n.T("Network"), i18n.T("Quick pass"),
		i18n.T("Quick results"), handsOnStep(ho), i18n.T("Finish")}
}

// handsOnStep is the hands-on step's label: "Hands on (x of N)", x the test on screen while
// they run, else how many have a result.
func handsOnStep(h *HandsOn) string {
	if h == nil || len(h.Setup.Plan) == 0 {
		return i18n.T("Hands on")
	}
	x := len(h.Results)
	if h.Paused || h.Run != nil && h.Out == nil && !h.Skipped { // a paused test counts as the one on screen
		x = h.Cur + 1
	}
	return i18n.T("Hands on (%d of %d)", min(x, len(h.Setup.Plan)), len(h.Setup.Plan))
}

// steps sets the step track into the frame's bottom edge: ✓ done (bright green, joined by a
// bright green ═ up to the step on now), ▶ now (violet marker, bright label), ■ to come (dim,
// joined by a dim ─), and "step n of 7" at the right (owner, 2026-10-13, prototype v44: green
// ticks for done steps). The hands-on step passed without a single result (straight to Finish)
// keeps a dim ·. On a narrow screen the joins shorten, then the "step n of 7" goes.
func steps(gr *g.Grid, b Box, cur int, ho *HandsOn) {
	names := stepNames(ho)
	r := " " + i18n.T("step %d of %d", cur+1, len(names)) + " "
	room := b.X1 - 2 - (b.X0 + 2)
	join, right := 4, true
	need := func() int {
		n := 2
		for i, name := range names {
			n += width(name) + 2
			if i > 0 {
				n += join
			}
		}
		if right {
			n += width(r) + 1
		}
		return n
	}
	if need() > room {
		join = 3
	}
	if need() > room {
		right = false
	}
	skipped := cur > stepHandsOn && (ho == nil || len(ho.Results) == 0)
	x := gr.Put(b.X0+2, b.Y1, " ", b.FC)
	for i, name := range names {
		if i > 0 {
			done, ln := i <= cur, " ── "
			if done {
				ln = " ══ "
			}
			if join == 3 {
				ln = " ─ "
				if done {
					ln = " ═ "
				}
			}
			c := g.DIM
			if done {
				c = g.BGRN
			}
			x = gr.Put(x, b.Y1, ln, c)
		}
		m, mc, lc := "■", g.DIM, g.DIM
		switch {
		case i == stepHandsOn && i < cur && skipped:
			m = "·"
		case i < cur:
			m, mc, lc = "✓", g.BGRN, g.GRY
		case i == cur:
			m, mc, lc = "▶", g.BVIO, g.WHT
		}
		x = gr.Put(gr.Put(x, b.Y1, m+" ", mc), b.Y1, name, lc)
	}
	gr.Put(x, b.Y1, " ", b.FC)
	if right {
		gr.Put(b.X1-2-width(r), b.Y1, r, g.DIM)
	}
}

// Action is one entry of a screen's ▶ list: what it is, what it does, and whether it is greyed
// out (Off: shown, never selected), and whether a blank row sets it apart from the ones before
// (Gap; the number keys still count entries, not rows).
type Action struct {
	Label, Does string
	Off, Gap    bool
}

// actions draws a ▶ list right under the text being read: ↑↓ moves ▶, Enter picks. active is
// false while the focus is elsewhere (a text field): the marker turns into a dim ·. lw is the
// label column's width (0: the longest label + 3). It returns the row after the list.
func actions(gr *g.Grid, x, y int, items []Action, sel int, active bool, lw, end int) int {
	if lw == 0 {
		for _, a := range items {
			lw = max(lw, width(a.Label)+3)
		}
	}
	for i, a := range items {
		if a.Gap && i > 0 {
			y++
		}
		on := i == sel && !a.Off
		if on {
			if active {
				gr.Put(x, y+i, "▶", g.BVIO)
			} else {
				gr.Put(x, y+i, "·", g.DIM)
			}
		}
		c := g.GRY
		switch {
		case a.Off:
			c = g.DIM
		case on && active:
			c = g.WHT
		}
		gr.Put(x+2, y+i, a.Label, c)
		if a.Does != "" {
			gr.Put(x+2+lw, y+i, cut(a.Does, end-(x+2+lw)), g.DIM)
		}
	}
	return y + len(items)
}

// step moves a ▶ list's selection by d, over greyed-out items; it stays put at the ends.
func step(items []Action, sel, d int) int {
	for i := sel + d; i >= 0 && i < len(items); i += d {
		if !items[i].Off {
			return i
		}
	}
	return sel
}

// firstOn is the first item that isn't greyed out (from i on), or i.
func firstOn(items []Action, i int) int {
	for k := max(0, i); k < len(items); k++ {
		if !items[k].Off {
			return k
		}
	}
	for k := min(i, len(items)-1); k >= 0; k-- {
		if !items[k].Off {
			return k
		}
	}
	return i
}

// listKey handles a ▶ list's keys: ↑↓ and 1–9 move the selection. It reports whether Enter was
// pressed on an item that can be picked.
func listKey(key string, items []Action, sel *int) bool {
	*sel = firstOn(items, *sel)
	switch key {
	case "up", "k":
		*sel = step(items, *sel, -1)
	case "down", "j":
		*sel = step(items, *sel, 1)
	case "enter":
		return *sel >= 0 && *sel < len(items) && !items[*sel].Off
	default:
		if len(key) == 1 && key[0] >= '1' && key[0] <= '9' {
			if i := int(key[0] - '1'); i < len(items) && !items[i].Off {
				*sel = i
			}
		}
	}
	return false
}

// divider draws a line across the frame (├───┤), optionally with a label in it at column lx.
// The line is dim; its ├ ┤ ends are three-way joins with the frame, so they take the frame's
// violet (owner, 2026-10-10, night).
func divider(gr *g.Grid, b Box, y int, label string, lx int, lc g.Color) {
	gr.Put(b.X0, y, "├", b.FC)
	gr.Put(b.X0+1, y, strings.Repeat("─", max(0, b.X1-b.X0-1)), g.DIM)
	gr.Put(b.X1, y, "┤", b.FC)
	if label != "" {
		gr.Put(lx, y, " "+label+" ", lc)
	}
}

// progress is the test progress bar: a violet block bar and a label after it.
func progress(gr *g.Grid, x, y, maxW int, frac float64, label string) {
	w := max(4, min(maxW-30, 72))
	n := frac * float64(w)
	gr.Put(x, y, "[", g.DIM)
	for i := 0; i < w; i++ {
		switch {
		case float64(i) < float64(int(n)):
			gr.SetBG(x+1+i, y, '█', g.BVIO, g.K)
		case float64(i) < n:
			gr.SetBG(x+1+i, y, '▒', g.BVIO, g.K)
		default:
			gr.SetBG(x+1+i, y, '░', g.DIM, g.K)
		}
	}
	gr.Put(x+1+w, y, "]", g.DIM)
	gr.Put(x+3+w, y, label, g.GRY)
}

// Layout is the inside of the frame: Boot's top area (Boot on the left, the band effect or the
// screen's headline to his right), a divider under it, Boot's line, then the content.
type Layout struct {
	BandTop, BandH int
	BootX, FieldX  int
	FieldW         int
	RuleY          int
	FxTop, FxH     int // the band effect's rows: from the frame's top edge down to the divider
	SayY, ContentY int
	LogTop, Bottom int
}

func layout(b Box, plain bool) Layout {
	l := Layout{BandTop: b.Y + 1, BootX: b.X + 1}
	if plain { // no Boot: a short top area for the headline, content right under it
		l.BandH, l.FieldX = 4, b.X+1
	} else {
		l.BandH, l.FieldX = max(8, min(12, b.H-23)), b.X+21
	}
	l.FieldW = b.X + b.W - l.FieldX
	l.RuleY = l.BandTop + l.BandH
	l.FxTop, l.FxH = l.BandTop-1, l.BandH+1
	l.SayY, l.ContentY = l.RuleY+2, l.RuleY+2
	l.LogTop = l.RuleY + 4
	if plain {
		l.LogTop = l.RuleY + 2
	}
	l.Bottom = b.Y + b.H - 1
	return l
}

// mark is a result's mark, colour and word.
func mark(s pace.Status) (string, g.Color, string) {
	switch s {
	case pace.Pass:
		return "✓", g.BGRN, i18n.T("pass")
	case pace.Partial:
		return "◐", g.AMB, i18n.T("partial") // a half-filled circle (owner, 2026-10-13; it was ~)
	case pace.Fail:
		return "×", g.BRED, i18n.T("fail")
	case pace.Live:
		return "▲", g.YEL, i18n.T("Omarchy required to test")
	}
	return "·", g.DIM, i18n.T("skipped")
}

func padTo(s string, n int) string {
	if w := width(s); w < n {
		return s + strings.Repeat(" ", n-w)
	}
	return s
}

func cut(s string, n int) string {
	r := []rune(s)
	if len(r) > n {
		return string(r[:max(0, n)])
	}
	return s
}
