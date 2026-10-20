package ui

import (
	"fmt"
	"math"
	"slices"
	"strings"

	"github.com/omaboot/boot-live/internal/boot"
	g "github.com/omaboot/boot-live/internal/grid"
	"github.com/omaboot/boot-live/internal/i18n"
	"github.com/omaboot/boot-live/internal/identify"
)

// ── Which Mac (0.7.9: prototype v53 B with the match table, owner 2026-10-17) ─────────────
//
// While Boot looks, the band's right side runs through the catalog and stops on this Mac's
// entry (or entries). Under the band: the six Intel Mac families, the ones ruled out going dim
// until this one lights up; 2006 to 2020 with the family's years and this Mac's; then what the
// Mac reported next to the configuration it matched (a column per configuration when several
// fit), and the list or the actions as before. On a short screen the timeline goes first, then
// the table, then the line-up.

const (
	macFound  = 1.4  // the search lands, the line-up settles
	macYears  = 1.8  // the timeline's bar has grown; the table starts
	tableStep = 0.15 // one more table row this often
)

// familyArt are the line-up's families, 20×12 pixels each: # body, s screen (the Mac Pro's grille),
// f Boot's face on the matched one's screen.
var familyArt = [6][12]string{
	{"..################..", "..#ssssssssssssss#..", "..#ssssssssssssss#..", "..#ssssssffssssss#..", "..#ssssssffssssss#..", "..#ssssssssssssss#..",
		"..#ssssssssssssss#..", "..################..", ".##################.", "####################", ".##################.", "...................."},
	{"..################..", "..#ssssssssssssss#..", "..#ssssssssssssss#..", "..#ssssssffssssss#..", "..#ssssssffssssss#..", "..#ssssssssssssss#..",
		"..#ssssssssssssss#..", "..################..", "####################", "..################..", "....................", "...................."},
	{"..################..", "..#ssssssssssssss#..", "..#ssssssssssssss#..", "..#ssssssffssssss#..", "..#ssssssffssssss#..", "..#ssssssssssssss#..",
		"..#ssssssssssssss#..", "..################..", "####################", "####################", "....................", "...................."},
	{"####################", "#ssssssssssssssssss#", "#ssssssssssssssssss#", "#ssssssssffssssssss#", "#ssssssssffssssssss#", "#ssssssssssssssssss#",
		"#ssssssssssssssssss#", "####################", "####################", "........####........", "........####........", "......########......"},
	{"....................", "....................", "....................", "....................", "....................", "....................",
		".##################.", "#..................#", "#..................#", "#.........s........#", ".##################.", "...................."},
	{".....##......##.....", "....############....", "....#..........#....", "....#.ssssssss.#....", "....#.ssssssss.#....", "....#..........#....",
		"....#..........#....", "....#..........#....", "....#..........#....", "....############....", ".....#........#.....", "...................."},
}

// famState is a family's look in the line-up.
type famState int

const (
	famOff   famState = iota // ruled out: a dim outline
	famOn                    // still in the running: grey
	famMatch                 // this Mac's: white, its screen violet, Boot on it
)

func drawFamily(gr *g.Grid, x, y, i int, st famState) {
	body, screen, face := byte('K'), byte('.'), byte('.')
	switch st {
	case famOn:
		body, screen, face = 'w', 'k', 'K'
	case famMatch:
		body, screen, face = 'W', 'm', 'C'
	}
	rows := make([]string, len(familyArt[i]))
	for r, s := range familyArt[i] {
		b := []byte(s)
		for k, c := range b {
			switch c {
			case '#':
				b[k] = body
			case 's':
				b[k] = screen
			case 'f':
				b[k] = face
			}
		}
		rows[r] = string(b)
	}
	boot.Pixels(gr, x, y, rows)
}

// lineUp draws the six families from x0, each cw wide, 9 rows: the ones that aren't this Mac's
// go dim one by one, farthest first, and this Mac's lights up in a violet box when the search
// lands (fam < 0: none does).
func lineUp(gr *g.Grid, x0, y, cw, fam int, t float64) {
	var out []int // farthest from this Mac's first
	for d := 5; d >= 1; d-- {
		for _, i := range []int{fam + d, fam - d} {
			if i >= 0 && i < 6 && (fam >= 0 || i != fam) {
				out = append(out, i)
			}
		}
	}
	if fam < 0 {
		out = []int{5, 4, 3, 2, 1, 0}
	}
	gone := map[int]bool{}
	for k, i := range out {
		if t > 0.3+0.2*float64(k) || t > macFound {
			gone[i] = true
		}
	}
	for i := range 6 {
		cx, st := x0+i*cw, famOn
		switch {
		case i == fam && t > macFound:
			st = famMatch
			box(gr, cx, y, cw-1, 9, g.VIO, "", g.VIO)
		case gone[i]:
			st = famOff
		}
		drawFamily(gr, cx+(cw-1-20)/2, y+1, i, st)
		name := i18n.S(identify.Families[i])
		c := map[famState]g.Color{famOff: g.DIM, famOn: g.GRY, famMatch: g.WHT}[st]
		gr.Put(cx+(cw-1-width(name))/2, y+7, name, c)
	}
}

// yearMark is one year on the timeline: a configuration's release, picked (white) or not.
type yearMark struct {
	when identify.When
	on   bool
}

// timeline draws 2006 to 2020 in w columns from x, 3 rows from y: the family's years as a bar
// (it grows as the search lands), this Mac's year (or the candidates') as a violet ◆, labelled
// above; "which year?" when there are several.
func timeline(gr *g.Grid, x, y, w int, v *identify.View, marks []yearMark, t float64) {
	if t <= macFound || w < 40 {
		return
	}
	yr := func(f float64) int { return x + int(math.Round((f-2006)/14*float64(w-1))) }
	gr.Put(x, y+1, strings.Repeat("─", w), g.DIM)
	for v := 2006; v <= 2020; v += 2 {
		gr.Put(yr(float64(v)), y+1, "┼", g.DIM)
		gr.Put(yr(float64(v))-1, y+2, fmt.Sprint(v), g.DIM)
	}
	if v.From > 0 {
		k := min(1, (t-macFound)/(macYears-macFound))
		gr.Put(yr(float64(v.From)), y+1, strings.Repeat("═", int(math.Round(float64(yr(float64(v.To))-yr(float64(v.From))+1)*k))), g.GRY)
	}
	if t <= macYears {
		return
	}
	// labels first, the picked one last so it wins an overlap
	type span struct{ a, b int }
	var taken []span
	put := func(m yearMark) {
		s := m.when.Label
		lx := yr(m.when.Year) - width(s)/2
		for _, sp := range taken {
			if lx <= sp.b+1 && lx+width(s) >= sp.a-1 {
				return
			}
		}
		c := g.GRY
		if m.on {
			c = g.WHT
		}
		gr.Put(lx, y, s, c)
		taken = append(taken, span{lx, lx + width(s)})
	}
	for _, m := range marks {
		if m.on {
			put(m)
		}
	}
	for _, m := range marks {
		if !m.on {
			put(m)
		}
	}
	for _, m := range marks {
		c := g.VIO
		if m.on {
			c = g.BVIO
		}
		gr.Put(yr(m.when.Year), y+1, "◆", c)
	}
	// which year (several), and the family's years: on the left when they fit before the labels
	var left []string
	var lc []g.Color
	if len(marks) > 1 {
		left, lc = append(left, i18n.T("which year?")), append(lc, g.AMB)
	}
	if v.Family >= 0 && v.From > 0 {
		left = append(left, i18n.T("Intel %[1]s: %[2]d to %[3]d", i18n.S(identify.Families[v.Family]), v.From, v.To))
		lc = append(lc, g.GRY)
	}
	lx := x
	for i, s := range left {
		free := true
		for _, sp := range taken {
			if lx <= sp.b+1 && lx+width(s) >= sp.a-1 {
				free = false
			}
		}
		if !free {
			break
		}
		lx = gr.Put(lx, y, s, lc[i]) + 3
	}
}

// catalogSearch draws the band's right side: the catalog runs past a ▶ ◀ reader line from the
// top, fast, then slowing, once round, and stops on this Mac's entry (every entry that fits
// lights up); the last line counts, then names the parts that matched as well.
func catalogSearch(gr *g.Grid, x, y, w int, t float64, v *identify.View, col string) {
	const h, rows, mid = 10, 7, 3
	n := len(v.Entries)
	if n == 0 {
		return
	}
	hit := 0
	if len(v.Hits) > 0 {
		hit = v.Hits[0]
	}
	k := max(0, min(1, (t-0.2)/(macFound-0.2)))
	e, done := 1-math.Pow(1-k, 3), t > macFound
	pos := int(math.Round(e*float64(n+hit))) % n
	bc, tc := g.DIM, g.GRY
	if done {
		bc, tc = g.VIO, g.WHT
	}
	box(gr, x, y, w, h, bc, i18n.T("catalog snapshot"), tc)
	lit := map[int]bool{}
	for _, i := range v.Hits {
		lit[i] = true
	}
	for i := range rows {
		j := ((pos-mid+i)%n + n) % n
		en := v.Entries[j]
		s := cut(fmt.Sprintf("%-22s%-16s%s", en.Board, en.Identifier, en.Release), w-6)
		cy := y + 1 + i
		switch {
		case done && lit[j]:
			gr.Put(x+1, cy, "▶", g.BVIO)
			gr.Put(x+w-2, cy, "◀", g.BVIO)
			gr.PutBG(x+2, cy, " "+s+strings.Repeat(" ", max(0, w-5-width(s))), g.WHT, g.VIO)
		case !done && i == mid:
			gr.Put(x+1, cy, "▶", g.BVIO)
			gr.Put(x+w-2, cy, "◀", g.BVIO)
			gr.Put(x+3, cy, s, g.WHT)
		case !done && (i == mid-1 || i == mid+1):
			gr.Put(x+3, cy, s, g.GRY)
		default:
			gr.Put(x+3, cy, s, g.DIM)
		}
	}
	sy, end := y+h-2, x+w-3
	if !done {
		if v.Board != "" {
			gr.Put(x+3, sy, cut(i18n.T("board ID %s", v.Board), w-24), g.GRY)
		}
		c := i18n.T("%[1]d of %[2]d checked", int(math.Round(e*float64(n))), n)
		gr.Put(end-width(c), sy, c, g.DIM)
		return
	}
	if len(v.Hits) == 0 {
		gr.Put(x+3, sy, i18n.T("none of the %d fits.", n), g.WHT)
		return
	}
	cx := gr.Put(x+3, sy, i18n.N("%[1]d of %[2]d fits.", "%[1]d of %[2]d fit.", len(v.Hits), len(v.Hits), n), g.WHT) + 2
	i := 0
	for _, p := range v.Parts {
		if p.Key == "Board ID" || p.Key == "Model ID" || !p.Theirs[col].Match {
			continue
		}
		s := i18n.S(p.Key)
		if t <= macFound+0.3+0.3*float64(i) || cx+width(s)+2 > end {
			break
		}
		cx = gr.Put(cx, sy, s+" ", g.GRY)
		cx = gr.Put(cx, sy, "◆", g.BVIO) + 3
		i++
	}
}

// matchTable draws what this Mac reported (left) next to what each configuration in cols lists
// (right, a column each; sel is the picked one, -1 none), a violet ◆ joining each row where the
// picked configuration (or the only one) agrees. Rows appear one by one. It returns its height.
func matchTable(gr *g.Grid, x, y, end int, v *identify.View, cols []string, sel int, t float64) int {
	rows := v.Parts
	lw := 40
	for _, p := range rows {
		lw = max(lw, 12+width(p.Mine)+3)
	}
	lw = min(lw, 50)
	const gap = 9
	rx := x + lw + gap
	colw := 20
	for _, p := range rows {
		for _, c := range cols {
			colw = max(colw, width(p.Theirs[c].Text)+3)
		}
	}
	room := end - rx - 3
	if len(cols) == 1 { // one column: room for its title and the model identifier's note
		colw = max(colw, width(i18n.T("The catalog's configuration"))+6, width(i18n.T("names the model, not its configuration"))+4)
	}
	colw = min(colw, room)
	fit := max(1, room/colw)
	first := 0 // the columns shown: a window round the picked one
	if len(cols) > fit {
		first = max(0, min(sel-fit+1, len(cols)-fit))
		if sel < 0 {
			first = 0
		}
		cols = cols[first : first+fit]
		sel -= first
	}
	rw := 2 + len(cols)*colw
	shown := 0
	for i := range rows {
		if t > macYears+tableStep*float64(i) {
			shown = i + 1
		}
	}
	all := shown == len(rows)
	box(gr, x, y, lw, len(rows)+2, g.DIM, i18n.T("This Mac reports"), g.GRY)
	bc := g.DIM
	if all {
		bc = g.VIO
	}
	box(gr, rx, y, rw, len(rows)+2, bc, "", bc)
	if len(cols) == 1 && len(v.Fits) <= 1 {
		gr.Put(gr.Put(rx+2, y, " ", bc), y, cut(i18n.T("The catalog's configuration"), rw-6)+" ", g.WHT)
	} else {
		for j, c := range cols {
			s := colTitle(v, c, cols)
			tc := g.GRY
			if j == sel {
				tc = g.WHT
			}
			gr.Put(gr.Put(rx+1+j*colw, y, " ", bc), y, cut(s, colw-3)+" ", tc)
		}
	}
	ref := max(0, sel) // the column the ◆ speaks for
	for i, p := range rows[:shown] {
		ry := y + 1 + i
		gr.Put(x+2, ry, cut(i18n.S(p.Key), 9), g.DIM)
		if p.Key == "Model ID" {
			gr.Put(x+12, ry, cut(p.Mine, lw-14), g.GRY)
			gr.Put(x+lw+gap/2, ry, "·", g.DIM)
			gr.Put(rx+2, ry, cut(i18n.T("names the model, not its configuration"), rw-4), g.DIM)
			continue
		}
		match := len(cols) > 0 && p.Theirs[cols[ref]].Match
		mc := g.GRY
		if match {
			mc = g.WHT
			gr.Put(x+lw, ry, strings.Repeat("─", gap), g.VIO)
			gr.Put(x+lw+gap/2, ry, "◆", g.BVIO)
		} else {
			gr.Put(x+lw+gap/2, ry, "·", g.DIM)
		}
		mine := p.Mine
		if mine == "" {
			mine = i18n.T("not seen")
		}
		gr.Put(x+12, ry, cut(mine, lw-14), mc)
		for j, c := range cols {
			cell := p.Theirs[c]
			cc := g.GRY
			switch {
			case len(cols) > 1 && j != sel:
				cc = g.DIM
			case cell.Match:
				cc = g.WHT
			}
			gr.Put(rx+2+j*colw, ry, cut(cell.Text, colw-3), cc)
		}
	}
	return len(rows) + 2
}

// colTitle names a configuration's column: its release's season and year, or, when another
// column has the same (or there is none), the part of its ID that tells the columns apart.
func colTitle(v *identify.View, id string, cols []string) string {
	w, ok := v.When[id]
	for _, c := range cols {
		if !ok || c != id && v.When[c].Label == w.Label {
			return identify.Suffixes(cols)[slices.Index(cols, id)]
		}
	}
	return w.Label
}

// macCols are the configurations the table shows as columns, and the picked one's index (-1
// none): the list's (candidates, or all of the Mac's while picking), else the one settled on.
func macCols(f *Facts, st *State, list []Choice) ([]string, int) {
	v := f.View
	if list != nil {
		var cols []string
		for _, c := range list {
			if c.ID != "" {
				cols = append(cols, c.ID)
			}
		}
		sel := st.PickSel
		if sel >= len(cols) {
			sel = -1
		}
		return cols, sel
	}
	if st.Picked && st.PickedID != "" {
		return []string{st.PickedID}, 0
	}
	if len(v.Fits) == 1 {
		return v.Fits, 0
	}
	return v.Fits, -1
}

// macName is the Mac's name and model identifier under the table: the configuration's own name
// once one is picked from a list (the Mac's name alone may be just its family's).
func macName(f *Facts, st *State) string {
	if st.Picked && st.PickedID != "" {
		for _, c := range slices.Concat(f.Candidates, f.Configs) {
			if c.ID == st.PickedID {
				return c.Label + " [" + f.Mac.ID + "]"
			}
		}
	}
	return macLabel(f.Mac)
}

func drawWhichMac(gr *g.Grid, o *Options, st *State, f *Facts) Box {
	t := st.T
	list := pickList(f, st)
	v := f.View
	right := omarchy(f) + "    " + i18n.T("starting")
	if st.Net.Online || st.Offline {
		right = netRight(f, st) // back from the Network step
	}
	found := v == nil || t > macFound
	b := frame(gr, o, st, f.Mac, frameSpec{title: i18n.T("Which Mac"), step: 1, ho: st.HO, right: right, mac: found,
		keys: Keys{Esc: true, Back: true}})
	l := layout(b, o.Plain)
	x, y, end := b.X+1, l.ContentY, b.X+b.W
	var cols []string
	sel := -1
	if v != nil {
		cols, sel = macCols(f, st, list)
	}
	col := ""
	if len(cols) > 0 {
		col = cols[max(0, sel)]
	}
	if o.Plain {
		gr.Put(l.FieldX, l.BandTop, i18n.T("Which Mac"), g.WHT)
		gr.Put(l.FieldX, l.BandTop+1, i18n.T("Matched by board ID and parts against the catalog."), g.DIM)
	} else {
		ex := boot.Idle
		switch {
		case !found:
			ex = boot.Work
		case t < 2.6 && !f.Unknown && list != nil && !st.Picking:
			ex = boot.Hmm
		case t < 2.6 && !f.Unknown:
			ex = boot.Pleased
		}
		boot.Draw(gr, l.BootX, l.BandTop+1, ex, life(st, t))
		boot.Wordmark(gr, l.FieldX, l.BandTop+1, "OmaBoot?")
		sx := l.FieldX + 60
		sw := l.FieldX + l.FieldW - sx - 1
		search := v != nil && sw >= 44
		said := l.FieldW - 1
		if search {
			said = 56
			catalogSearch(gr, sx, l.BandTop+1, sw, t, v, col)
		}
		found0, next := i18n.T("found it. network next, then a 2-minute quick pass."), i18n.T("correct Mac? Enter, and we're off.")
		switch {
		case list != nil && st.Picking:
			found0, next = i18n.T("these are this model's configurations."), i18n.T("pick yours with ↑↓, then Enter.")
		case list != nil:
			found0, next = i18n.T("a few configurations fit. which one is yours?"), i18n.T("pick it with ↑↓, then Enter.")
		case f.Resume != nil && !st.Answered && !st.PassDone:
			found0, next = i18n.T("found it. and a copy from last time: the Mac didn't come back."), i18n.T("pick up where you left off, or start fresh?")
		case f.OtherCopy != nil && !st.Answered && !st.PassDone:
			found0 = i18n.T("found it. and a copy from another version: this run tests everything again.")
		case f.Unknown:
			found0, next = i18n.T("I don't know this one yet. I'll test it anyway."), i18n.T("Enter, and we're off.")
		}
		lines := []line{{0.3, found0}, {2.4, next}}
		if v != nil {
			lines = []line{{macFound, found0}, {macReady, next}}
			if !found {
				lines = []line{{0.2, i18n.T("which Mac are you? let me look.")}}
			}
		}
		sayBox(gr, &st.Said, l.FieldX, l.BandTop+7, said, l.RuleY-(l.BandTop+7)-2, t, lines, -1)
	}
	divider(gr, b, l.RuleY, "", 0, g.BVIO)
	if t <= 0.2 {
		return b
	}

	// what fits above the list: the line-up, the timeline, the table, dropped in reverse order
	// (the table and the timeline only for a Mac in the catalog)
	cw := min(24, (b.W-2)/6)
	lineup := v != nil && v.Family >= 0 && cw >= 22
	var marks []yearMark
	if v != nil && v.From > 0 {
		seen := map[string]bool{}
		for j, c := range cols {
			if w, ok := v.When[c]; ok && !seen[w.Label] {
				seen[w.Label] = true
				marks = append(marks, yearMark{when: w, on: j == sel || len(cols) == 1})
			} else if ok && j == sel {
				for k := range marks {
					marks[k].on = marks[k].when.Label == w.Label
				}
			}
		}
	}
	years := lineup && len(marks) > 0
	table := v != nil && len(v.Parts) > 0 && len(cols) > 0 && end-x >= 40+9+24
	// below them: the note or the question, a blank row, then the list (at least a few rows of
	// it) or the actions, with a copy's line over them
	lower := 2 + min(len(list), 4)
	if list == nil {
		as, _ := macActions(f, st)
		lower = 2 + len(as)
		if (f.Resume != nil || f.OtherCopy != nil) && !st.Answered && !st.PassDone {
			lower += 4
		}
	}
	gap := 1 // a blank row under the line-up and the timeline, when there's room
	need := func() int {
		n := lower
		if !table || list == nil {
			n += 2 // the name and parts lines: under the table, or in its place
		}
		for _, s := range []struct {
			on bool
			h  int
		}{{lineup, 9 + gap}, {years, 3 + gap}, {table, len(v.Parts) + 3}} {
			if s.on {
				n += s.h
			}
		}
		return n
	}
	avail := l.Bottom - y + 1
	if need() > avail {
		gap = 0
	}
	if need() > avail {
		years = false
	}
	if need() > avail {
		table = false
	}
	if need() > avail {
		lineup = false
	}
	if lineup {
		lineUp(gr, x+max(0, (b.W-2-cw*6)/2), y, cw, v.Family, t)
		y += 9 + gap
	}
	if years {
		timeline(gr, x+max(0, (b.W-2-cw*6)/2)+2, y, cw*6-5, v, marks, t)
		y += 3 + gap
	}
	if table {
		if t <= macYears {
			return b
		}
		y += matchTable(gr, x, y, end, v, cols, sel, t) + 1
		if list == nil { // the Mac's name and parts under the table, as in the prototype's line-up (owner, 0.7.14 review)
			if t <= macReady {
				return b
			}
			gr.Put(x, y, cut(macName(f, st), end-x), g.WHT)
			gr.Put(x, y+1, cut(f.Parts, end-x), g.GRY)
			y += 2
		}
	} else {
		if v != nil && t <= macFound {
			return b
		}
		gr.Put(x, y, cut(macName(f, st), end-x), g.WHT)
		gr.Put(x, y+1, cut(f.Parts, end-x), g.GRY)
		y += 2
	}
	if list != nil {
		if t <= macReady {
			return b
		}
		q := i18n.T("Several configurations fit. Which one is yours?")
		if st.Picking {
			q = i18n.T("This model's configurations. Which one is yours?")
		}
		gr.Put(x, y, q, g.DIM)
		items := make([]Action, len(list))
		lw := 0
		for i, c := range list {
			items[i] = Action{Label: cut(c.Label, 40), Does: c.Note}
			lw = max(lw, width(items[i].Label)+3)
		}
		rows := max(1, l.Bottom-(y+2)+1)
		top := max(0, min(st.PickSel-rows+1, len(items)-rows))
		last := min(len(items), top+rows)
		actions(gr, x, y+2, items[top:last], st.PickSel-top, true, lw, end)
		return b
	}
	if t <= macReady {
		return b
	}
	gr.Put(x, y, cut(f.MatchNote, end-x), g.DIM)
	ay := y + 2
	line := ""
	switch {
	case f.Resume != nil && !st.Answered && !st.PassDone:
		line = resumeLine(f.Resume)
	case f.OtherCopy != nil && !st.Answered && !st.PassDone:
		line = otherLine(f.OtherCopy, o.Version)
	}
	for i, s := range wrap(line, end-x-1) {
		gr.Put(x, y+2+i, s, g.GRY)
		ay = y + 4 + i
	}
	as, _ := macActions(f, st)
	actions(gr, x, ay, as, st.MacSel, true, 0, end)
	return b
}
