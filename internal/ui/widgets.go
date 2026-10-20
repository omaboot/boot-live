package ui

import (
	"fmt"
	"strings"

	"github.com/omaboot/boot-live/internal/boot"
	g "github.com/omaboot/boot-live/internal/grid"
	"github.com/omaboot/boot-live/internal/handson"
	"github.com/omaboot/boot-live/internal/i18n"
	"github.com/omaboot/boot-live/internal/pace"
)

// ── Pieces of the hands-on layout (owner-approved prototype v46, 2026-10-13) ───────────────

// box draws a w×h box at x, y in col, with title set into its top edge ("┌ Result ──┐") in tcol.
func box(gr *g.Grid, x, y, w, h int, col g.Color, title string, tcol g.Color) {
	if w < 2 || h < 2 {
		return
	}
	gr.Put(x, y, "┌"+strings.Repeat("─", w-2)+"┐", col)
	for j := 1; j < h-1; j++ {
		gr.Put(x, y+j, "│", col)
		gr.Put(x+w-1, y+j, "│", col)
	}
	gr.Put(x, y+h-1, "└"+strings.Repeat("─", w-2)+"┘", col)
	if title != "" && w > 6 {
		gr.Put(gr.Put(x+2, y, " ", col), y, cut(title, w-6)+" ", tcol)
	}
}

// bandSplit shares the band's field: 1/3 for Boot's line (sw wide from the field's left), 2/3
// for the animation (aw wide from ax). Every test and the quick pass (owner, 2026-10-13).
func bandSplit(l Layout) (sw, ax, aw int) {
	sw = l.FieldW/3 - 2
	ax = l.FieldX + sw + 3
	return sw, ax, l.FieldX + l.FieldW - ax - 1
}

// speechLines is the most rows of text Boot's box in the band holds.
func speechLines(l Layout) int { return max(1, l.BandH-4) }

// sayRows is the fixed text rows of Boot's box under the band (the port tests): fixed, so
// nothing below it moves when his line changes.
const sayRows = 3

// speech draws Boot's line in a violet box, typed out lt seconds after it started; the text is
// wrapped first, so words never jump lines while it types. rows > 0 fixes the box's text rows
// (under the band); 0 sizes it to the line, up to maxRows. tail ≥ 0 puts a ▲ in the top edge at
// that column (the box under the band, pointing up at Boot); else ◀ on its left edge, at him.
// hint: amber, "hint" in its top edge (Boot offers a hint after 20 s with no key). A line from
// the console (cs) stands in for his while it's up.
func speech(gr *g.Grid, cs *Said, x, y, w, rows, maxRows int, text string, lt float64, hint bool, tail int) {
	if w < 6 {
		return
	}
	if cs != nil && cs.Text != "" {
		l := cs.over([]line{{-lt, text}}, 0)[0]
		text, lt, hint = l.text, -l.at, false
	}
	rs := typedRows(text, 0, w-4)
	if rows > 0 {
		maxRows = rows
	}
	if len(rs) > maxRows {
		rs = rs[:maxRows]
	}
	h := max(3, len(rs)+2)
	if rows > 0 {
		h = rows + 2
	}
	drawSpeech(gr, x, y, w, h, rs, lt, hint, tail)
}

// sayRow is one row of text in Boot's box and when it starts typing.
type sayRow struct {
	text string
	at   float64
}

// typedRows wraps text to w cells: the first row types from at, each next one once the row
// above it (and a space) is typed out.
func typedRows(text string, at float64, w int) []sayRow {
	var out []sayRow
	for _, l := range wrap(text, w) {
		l = cut(l, w)
		out = append(out, sayRow{l, at})
		at += float64(width(l)+1) / boot.TypeCPS
	}
	return out
}

// drawSpeech draws Boot's box (w×h at x, y) and its rows typed out by t: the violet block cursor
// where the typing is, blinking once it's all typed.
func drawSpeech(gr *g.Grid, x, y, w, h int, rows []sayRow, t float64, hint bool, tail int) {
	col, mc, title := g.VIO, g.BVIO, ""
	if hint {
		col, mc, title = g.AMB, g.YEL, i18n.T("hint")
	}
	gr.Fill(x, y, w, h, ' ', g.GRY, g.K)
	box(gr, x, y, w, h, col, title, mc)
	if tail >= 0 {
		gr.Put(tail, y, "▲", mc)
	} else {
		gr.Put(x, y+1, "◀", mc)
	}
	cx, cy, done := x+2, y+1, true
	for i, r := range rows {
		if i >= h-2 {
			break
		}
		if t < r.at {
			done = false
			break
		}
		s := []rune(r.text)
		k := min(len(s), max(0, int((t-r.at)*boot.TypeCPS)))
		gr.Put(x+2, y+1+i, string(s[:k]), g.WHT)
		cx, cy = x+2+k, y+1+i
		if k < len(s) {
			done = false
			break
		}
	}
	if !done || blinkOn(t) {
		gr.Put(cx, cy, "█", mc)
	}
}

// line is one of Boot's lines on a screen and when he starts it (the screen's seconds).
type line struct {
	at   float64
	text string
}

// sayWidth is the width of Boot's box for lines, at most maxW: as wide as the longest of them
// all, said yet or not, so the box never changes width as they come.
func sayWidth(maxW int, lines []line) int {
	mw := 1
	for _, l := range lines {
		for _, r := range wrap(l.text, maxW-4) {
			mw = max(mw, min(width(r), maxW-4))
		}
	}
	return min(maxW, mw+4)
}

// sayBox draws Boot's lines that have started by t in his violet box at x, y (the same box as
// on the tests, owner 2026-10-13: every screen): sayWidth wide, as tall as their rows, at most
// maxRows of text (the oldest lines make room). Each line types out from its start; the cursor
// sits on the newest. tail ≥ 0 puts the ▲ in the top edge at that column (the box under Boot);
// else ◀ on its left edge, at him. A line from the console (cs) stands in for his while it's
// up. It returns the box's height (0: nothing said yet).
func sayBox(gr *g.Grid, cs *Said, x, y, maxW, maxRows int, t float64, lines []line, tail int) int {
	if maxW < 6 || maxRows < 1 {
		return 0
	}
	lines = cs.over(lines, t)
	w := sayWidth(maxW, lines)
	var said [][]sayRow
	n := 0
	for _, l := range lines {
		if l.text == "" || t < l.at {
			continue
		}
		rs := typedRows(l.text, l.at, w-4)
		said = append(said, rs)
		n += len(rs)
	}
	for len(said) > 1 && n > maxRows {
		n -= len(said[0])
		said = said[1:]
	}
	var rows []sayRow
	for _, rs := range said {
		rows = append(rows, rs...)
	}
	if len(rows) == 0 {
		return 0
	}
	if len(rows) > maxRows {
		rows = rows[:maxRows]
	}
	h := len(rows) + 2
	drawSpeech(gr, x, y, w, h, rows, t, false, tail)
	return h
}

// answer is one row of a test's answers: its key (a digit, s), what it is, what it does.
type answer struct {
	Key, Label, Does string
	Off, Gap         bool
	Bars             int // a Wi-Fi network's signal, 1 to 4, drawn before Does (handson.Action.Bars)
}

// answers draws the answers from x, y in maxW columns: ▶ on the selected one (sel < 0: none),
// its key (dim; violet when selected), the label, and what it does, wrapped in its own column;
// when that column would be narrower than 18 it goes under the label instead (owner, 2026-10-13:
// nothing spills into the next section). It returns the row after them.
func answers(gr *g.Grid, x, y int, items []answer, sel, maxW int) int {
	lw := 0
	for _, a := range items {
		lw = max(lw, width(a.Label)+3)
	}
	dw := maxW - 5 - lw
	below := dw < 18
	for i, a := range items {
		if a.Gap && i > 0 {
			y++
		}
		on := i == sel && !a.Off
		if on {
			gr.Put(x, y, "▶", g.BVIO)
		}
		kc := g.DIM
		if on {
			kc = g.BVIO
		}
		gr.Put(x+2, y, a.Key, kc)
		lc := g.GRY
		switch {
		case a.Off:
			lc = g.DIM
		case on:
			lc = g.WHT
		}
		gr.Put(x+5, y, a.Label, lc)
		if a.Bars > 0 { // the signal as on the Network screen: ■ cyan for each bar, · dim for the rest
			bx := x + 5 + lw
			if below {
				bx = x + 5 + width(a.Label) + 2
			}
			for k := 0; k < 4; k++ {
				if k < a.Bars {
					gr.Put(bx+k, y, "■", g.CYN)
				} else {
					gr.Put(bx+k, y, "·", g.DIM)
				}
			}
		}
		if a.Does == "" {
			y++
			continue
		}
		if below {
			ls := wrap(a.Does, maxW-7)
			for k, l := range ls {
				gr.Put(x+7, y+1+k, l, g.DIM)
			}
			y += 1 + len(ls)
			continue
		}
		dx := 0
		if a.Bars > 0 {
			dx = 6 // after the signal's squares
		}
		ls := wrap(a.Does, dw-dx)
		for k, l := range ls {
			gr.Put(x+5+lw+dx, y+k, l, g.DIM)
		}
		y += max(1, len(ls))
	}
	return y
}

// resultBox is the result under the Output box: three rows outlined in the status colour,
// "Result" in its top edge, the mark, the word and the text inside; outline only (owner,
// 2026-10-13). With no result yet it is dim and says what the test waits for.
func resultBox(gr *g.Grid, x, y, w int, cs []Check, waiting string) {
	if len(cs) == 0 {
		box(gr, x, y, w, 3, g.DIM, i18n.T("Result"), g.DIM)
		if waiting == "" {
			waiting = "·  " + i18n.T("no result yet")
		}
		gr.Put(x+2, y+1, cut(waiting, w-4), g.DIM)
		return
	}
	worst := cs[0]
	for _, c := range cs[1:] {
		if rank(c.Status) > rank(worst.Status) {
			worst = c
		}
	}
	m, col, word := mark(worst.Status)
	box(gr, x, y, w, 3, col, i18n.T("Result"), col)
	end := x + w - 2
	cx := gr.Put(x+2, y+1, m, col)
	cx = gr.Put(cx+1, y+1, word, col)
	if worst.Fix != "" {
		cx = fixTag(gr, cx+1, y+1)
	}
	text := i18n.S(worst.Detail)
	if len(cs) > 1 { // several criteria: the worst one's, named; all of them are in the output
		text = i18n.S(worst.Label) + ": " + text
	}
	gr.Put(cx+3, y+1, cut(text, end-cx-3), g.WHT)
}

// rank orders statuses for the result box: the worst shows.
func rank(s pace.Status) int {
	switch s {
	case pace.Pass:
		return 1
	case pace.Live:
		return 2
	case pace.Skip:
		return 3
	case pace.Partial:
		return 4
	case pace.Fail:
		return 5
	}
	return 0
}

// codeField draws a typed code's field from x, y (prototype codeField): the label, three boxed
// digits with the next one's box violet and a blinking _ in it, "type the 3 digits"
// beside them, and the tries or the wrong try under them. It returns the row after it.
func codeField(gr *g.Grid, x, y int, c *handson.CodeField, t float64) int {
	n := c.Len
	if n <= 0 {
		n = 3
	}
	gr.Put(x, y, c.Label, g.GRY)
	val := []rune(c.Value)
	for i := 0; i < n; i++ {
		bx, cur := x+i*6, i == len(val)
		col := g.DIM
		if cur {
			col = g.BVIO
		}
		box(gr, bx, y+1, 5, 3, col, "", col)
		switch {
		case i < len(val):
			gr.Put(bx+2, y+2, string(val[i]), g.WHT)
		case cur && blinkOn(t):
			gr.Put(bx+2, y+2, "_", g.BVIO)
		}
	}
	gr.Put(x+n*6+2, y+2, i18n.T("type the %d digits", n), g.DIM)
	if c.Wrong != "" {
		gr.Put(x, y+5, c.Wrong, g.AMB)
	} else {
		gr.Put(x, y+5, c.Tries, g.DIM)
	}
	return y + 6
}

// stamp writes a run's time as m:ss.
func stamp(t float64) string {
	s := max(0, int(t))
	return fmt.Sprintf("%d:%02d", s/60, s%60)
}
