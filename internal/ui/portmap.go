package ui

import (
	"math"
	"sort"
	"strings"

	g "github.com/omaboot/boot-live/internal/grid"
	"github.com/omaboot/boot-live/internal/handson"
	"github.com/omaboot/boot-live/internal/i18n"
	"github.com/omaboot/boot-live/internal/icons"
	"github.com/omaboot/boot-live/internal/identify"
)

// ── The port map ───────────────────────────────────────────────────────────────
//
// Above a port test's Found lines: the Mac's ports where DoesItOmarchy's drawing puts them, each
// in its state's colour (handson.PortState), the port to plug into ringed in pulsing violet.
// A laptop is its left and right sides next to each other, each as you see it looking at that
// side, at real scale, with "back" and "front" at the ends and a leader from each port down to
// its name. A desktop is its back (or front, or top) as one strip cropped to the ports, every
// port at its real spacing and its name under it; an iMac's back is mirrored, as the tester finds
// it reaching round from the front (identify.MirrorBack). The map's rows depend only on the Mac and
// the screen's width, never on the states, so Found always starts on the same row.

// drawPortMap draws m from x, y in w columns, using at most maxH rows; it returns the rows used.
// Each side view and each desktop face sits in a dim box with its title set into the top edge.
func drawPortMap(gr *g.Grid, m *handson.PortMap, x, y, w, maxH int, t float64) int {
	if m.Laptop() {
		return laptopSides(gr, m, x, y, w, maxH, t)
	}
	h := 0
	for _, f := range facePlan(m, w, maxH) {
		mapBox(gr, x+f.x, y+f.y, f.w, f.h, faceTitle(f.side))
		backStrip(gr, m, f.side, x+f.x+2, y+f.y, f.w-4, t)
		h = max(h, f.y+f.h)
	}
	return h
}

// placed is a desktop face's box on the map, from the map's top left.
type placed struct {
	side       identify.PortSide
	x, y, w, h int
}

// facePlan places a desktop's faces (faces): one under another while they fit in maxH rows,
// else side by side, each as wide as its share of the ports (a Mac Pro's USB ports on its
// back and front; 0.7.2 review: a face with the test's ports must never be left out).
func facePlan(m *handson.PortMap, w, maxH int) []placed {
	fs := faces(m)
	var out []placed
	h := 0
	for _, s := range fs {
		fh := faceH(m, s, w-4) + 2
		if h > 0 {
			h++
		}
		out = append(out, placed{side: s, y: h, w: w, h: fh})
		h += fh
	}
	if len(fs) < 2 || h <= maxH {
		return out
	}
	// each face gets the least width, and the rest goes by ports
	const gap, least = 2, 24
	n := 0
	for _, s := range fs {
		n += len(s.Ports)
	}
	inner := w - gap*(len(fs)-1)
	base := min(least, inner/len(fs))
	spare := inner - base*len(fs)
	out, x := out[:0], 0
	for i, s := range fs {
		fw := base + spare*len(s.Ports)/max(1, n)
		if i == len(fs)-1 {
			fw = w - x
		}
		out = append(out, placed{side: s, x: x, w: fw})
		x += fw + gap
	}
	fh := 0
	for i := range out {
		fh = max(fh, faceH(m, out[i].side, out[i].w-4)+2)
	}
	for i := range out {
		out[i].h = fh
	}
	return out
}

// mapBox is a dim box of w columns and h rows at x, y with its title set into the top edge:
// "┌─ Left side ──┐".
func mapBox(gr *g.Grid, x, y, w, h int, title string) {
	gr.Put(x, y, "┌"+strings.Repeat("─", max(0, w-2))+"┐", g.DIM)
	for yy := y + 1; yy < y+h-1; yy++ {
		gr.Put(x, yy, "│", g.DIM)
		gr.Put(x+w-1, yy, "│", g.DIM)
	}
	gr.Put(x, y+h-1, "└"+strings.Repeat("─", max(0, w-2))+"┘", g.DIM)
	if w >= 8 {
		cx := gr.Put(x+2, y, " ", g.DIM)
		gr.Put(gr.Put(cx, y, cut(title, w-6), g.GRY), y, " ", g.DIM)
	}
}

// ringPort is the ring round the port to plug into, inside the aluminium strip (sy its top
// row, the port's symbol at px, px+1): ▄ above, █ either side, ▀ below, pulsing between the
// two violets twice a second.
func ringPort(gr *g.Grid, px, sy int, t float64) {
	c := g.BVIO
	if int(t*2)%2 == 1 {
		c = g.VIO
	}
	for i := -1; i <= 2; i++ {
		gr.SetBG(px+i, sy, '▄', c, g.K)
		gr.SetBG(px+i, sy+2, '▀', c, g.K)
	}
	gr.SetBG(px-1, sy+1, '█', c, g.K)
	gr.SetBG(px+2, sy+1, '█', c, g.K)
}

// edgeStrip is the aluminium edge: three rows, half blocks above and below, the ports set into
// its middle row.
func edgeStrip(gr *g.Grid, x, y, w int) {
	for i := 0; i < w; i++ {
		gr.SetBG(x+i, y, '▄', g.GRY, g.K)
		gr.SetBG(x+i, y+1, ' ', g.GRY, g.GRY)
		gr.SetBG(x+i, y+2, '▀', g.GRY, g.K)
	}
}

// portShape is a port's two-cell symbol at px in its state's colour on black: a dark opening
// in the aluminium.
func portShape(gr *g.Grid, px, y int, kind string, st handson.PortState) {
	sym, ok := icons.Port(kind)
	if !ok {
		sym = [2]rune{'▄', '▄'}
	}
	gr.SetBG(px, y, sym[0], st.Color(), g.K)
	gr.SetBG(px+1, y, sym[1], st.Color(), g.K)
}

// portIcon is a port kind's Nerd icon as text ("" for a kind without one).
func portIcon(kind string) string {
	ic, _ := icons.PortIcon(kind)
	return ic
}

// nextNote is "plug in here" with its arrow pointing at the name: left, right or up.
func nextNote(dir string) string {
	switch dir {
	case "left":
		return i18n.T("← plug in here")
	case "right":
		return i18n.T("plug in here →")
	}
	return i18n.T("↑ plug in")
}

// busyNote is the ringed port's note while its check is under way ("reading…"), its arrow
// pointing at the name like "plug in here"'s.
func busyNote(dir, n string) string {
	switch dir {
	case "left":
		return i18n.T("← %s", n)
	case "right":
		return i18n.T("%s →", n)
	}
	return i18n.T("↑ %s", n)
}

// pickNote marks the port under the cursor while the stick question is up, pointing at its name;
// phone: the tethering phone's question instead ("iPhone").
func pickNote(dir, phone string) string {
	if phone != "" {
		switch dir {
		case "left":
			return i18n.T("← the %s?", phone)
		case "right":
			return i18n.T("the %s? →", phone)
		}
		return i18n.T("↑ the %s?", phone)
	}
	switch dir {
	case "left":
		return i18n.T("← the OmaBoot? drive?")
	case "right":
		return i18n.T("the OmaBoot? drive? →")
	}
	return i18n.T("↑ the drive?")
}

// stateOf is a port's state as drawn: the port under the stick question's cursor shows as next.
func stateOf(m *handson.PortMap, p identify.Port) handson.PortState {
	if m.Picked != "" && p.ID == m.Picked {
		return handson.PortNext
	}
	return m.State[p.ID]
}

// note is a port's note now: "plug in here" for the next port (or the stick question's pick),
// else the test's. dir is where the name is from the note: the arrow points at it.
func note(m *handson.PortMap, p identify.Port, dir string) string {
	switch {
	case m.Picked != "" && p.ID == m.Picked:
		if m.PickPhone {
			return pickNote(dir, m.Phone)
		}
		return pickNote(dir, "")
	case m.State[p.ID] == handson.PortNext && m.Note[p.ID] != "":
		return busyNote(dir, m.Note[p.ID])
	case m.State[p.ID] == handson.PortNext:
		return nextNote(dir)
	}
	return m.Note[p.ID]
}

// nameColor: a port of the test's in grey, any other dim.
func nameColor(st handson.PortState) g.Color {
	if st == handson.PortOther {
		return g.DIM
	}
	return g.GRY
}

// ── laptops ───────────────────────────────────────────────────────────────────

// laptopSides is a laptop's left and right sides next to each other. A side's label rows are
// one per port; when a side has more ports than there's room for, only the test's ports get a
// label (the others keep their symbol). Sides other than left and right (none on the Intel
// laptops drawn so far) aren't shown.
func laptopSides(gr *g.Grid, m *handson.PortMap, x, y, w, maxH int, t float64) int {
	var sides []identify.PortSide
	for _, side := range []string{"left", "right"} {
		for _, s := range m.Sides {
			if s.Side == side {
				sides = append(sides, s)
			}
		}
	}
	if len(sides) == 0 {
		return 0
	}
	all := true
	for _, s := range sides {
		if 8+len(s.Ports) > maxH { // the box's two border rows too
			all = false
		}
	}
	const gap = 2
	bw := (w - gap*(len(sides)-1)) / len(sides) // a box's width
	h := 0
	for i, s := range sides {
		h = max(h, sideView(gr, m, s, x+i*(bw+gap)+2, y, bw-4, all, t))
	}
	if h == 0 {
		return 0
	}
	h += 2 // the blank row under the labels and the bottom border
	for i, s := range sides {
		title := i18n.T("Left side")
		if s.Side == "right" {
			title = i18n.T("Right side")
		}
		mapBox(gr, x+i*(bw+gap), y, bw, h, title)
	}
	return h
}

// sideView is one side of a laptop as you see it looking at that side: its edge at real scale,
// "back" and "front" at its ends (the side's start on the left), and a leader from each port to
// its name (the box with the side's title is laptopSides'), stepped so none cross: names to the right when the side's ports sit in its left
// half, else to the left, the port furthest that way on the top row. Each name has the port's
// icon at its leader's end; the next port's "plug in here" points back at the name. It returns
// the rows used.
func sideView(gr *g.Grid, m *handson.PortMap, s identify.PortSide, x, y, w int, all bool, t float64) int {
	ex, ew, sy := x+6, w-13, y+2
	if ew < 8 || s.Length <= 0 {
		return 0
	}
	edgeStrip(gr, ex, sy, ew)
	near, far := i18n.T("back"), i18n.T("front")
	if s.Start == "front" {
		near, far = far, near
	}
	gr.Put(x, sy+1, cut(near, 5), g.DIM)
	gr.Put(ex+ew+1, sy+1, cut(far, 5), g.DIM)
	col := func(mm float64) int { return ex + int(math.Round(mm/s.Length*float64(ew-2))) }
	var labelled []identify.Port
	sum := 0
	for _, p := range s.Ports {
		st := stateOf(m, p)
		portShape(gr, col(p.X), sy+1, p.Kind, st)
		if st == handson.PortNext {
			ringPort(gr, col(p.X), sy, t)
		}
		if all || st != handson.PortOther {
			labelled = append(labelled, p)
			sum += col(p.X)
		}
	}
	if len(labelled) == 0 {
		return 5
	}
	right := sum*2/len(labelled) < 2*ex+ew // the ports sit in the left half: names to the right
	sort.SliceStable(labelled, func(i, j int) bool {
		if right {
			return labelled[i].X > labelled[j].X
		}
		return labelled[i].X < labelled[j].X
	})
	dir := "right" // the note sits before the name: its arrow points right, at the name
	if right {
		dir = "left" // after the name: pointing back left at it
	}
	for row, p := range labelled {
		st := stateOf(m, p)
		lc, nc := st.Color(), nameColor(st)
		c, ly := col(p.X), sy+4+row
		if !right {
			c++
		}
		for yy := sy + 3; yy < ly; yy++ {
			gr.Put(c, yy, "│", lc)
		}
		n, ic, name := note(m, p, dir), portIcon(p.Kind), m.Label(p)
		if right {
			cx := gr.Put(c, ly, "└─ ", lc)
			if ic != "" {
				cx = gr.Put(cx, ly, ic, lc) + 1
			}
			cx = gr.Put(cx, ly, name, nc)
			if n != "" {
				gr.Put(cx+2, ly, n, lc)
			}
			continue
		}
		lw := width(name) + 2 // name, then " ─┘" with the ┘ under the leader
		if n != "" {
			lw += width(n) + 2
		}
		if ic != "" {
			lw += width(ic) + 1
		}
		cx := c - lw
		if n != "" {
			cx = gr.Put(cx, ly, n, lc) + 2
		}
		cx = gr.Put(cx, ly, name, nc)
		if ic != "" {
			cx = gr.Put(cx+1, ly, ic, lc)
		}
		gr.Put(cx, ly, " ─┘", lc)
	}
	return 6 + len(labelled)
}

// ── desktops ──────────────────────────────────────────────────────────────────

// faces are the desktop faces the map shows: those with a port of the test's, else every face
// with ports.
func faces(m *handson.PortMap) []identify.PortSide {
	var mine, any []identify.PortSide
	for _, s := range m.Sides {
		if len(s.Ports) == 0 {
			continue
		}
		any = append(any, s)
		for _, p := range s.Ports {
			if m.State[p.ID] != handson.PortOther {
				mine = append(mine, s)
				break
			}
		}
	}
	if len(mine) > 0 {
		return mine
	}
	return any
}

// faceLayout is where a face's ports and names go at a width: fixed for the Mac, whatever the
// states (room is planned for the widest note any port can show).
type faceLayout struct {
	ports   []identify.Port // left to right
	cols    []int           // each port's centre, from the strip's left end
	names   []string        // "" for a port left unlabelled (a crowded face: not this test's)
	low     []bool          // the name a row lower (staggered)
	stagger bool            // every other name a row lower
}

// tightName is a name made shorter for a crowded face: "Thunderbolt 3" → "TB 3", "Gigabit
// Ethernet" → "Ethernet".
func tightName(s string) string {
	s = strings.Replace(s, "Thunderbolt", "TB", 1)
	return strings.TrimPrefix(s, "Gigabit ")
}

func layoutFace(m *handson.PortMap, s identify.PortSide, w int) faceLayout {
	ps := append([]identify.Port(nil), s.Ports...)
	// a face with ports in several rows (a Mac mini's, a Mac Pro's) is drawn on one strip, by x
	sort.SliceStable(ps, func(i, j int) bool {
		if ps[i].X != ps[j].X {
			return ps[i].X < ps[j].X
		}
		return ps[i].Y < ps[j].Y
	})
	noteW := max(m.NoteW, width(nextNote("up")), width(pickNote("up", "")))
	if m.Phone != "" {
		noteW = max(noteW, width(pickNote("up", m.Phone)))
	}
	// level 0: the names with their tags; 1: the shorter names; 2: the shorter names, and only
	// the tag for a port that isn't this test's; 3: no label at all for those (only their symbol,
	// as on a crowded laptop side), so the test's own ports' names never touch (0.7.2 review)
	try := func(level int) (faceLayout, bool, bool) {
		f := faceLayout{ports: ps}
		widest := noteW // the margin: room for the first and last names and notes
		for _, p := range ps {
			n := m.Name(p)
			if level > 0 {
				n = tightName(n)
			}
			n += " (" + handson.PortTag(p) + ")"
			if level > 1 && m.State[p.ID] == handson.PortOther {
				n = handson.PortTag(p)
				if level > 2 {
					n = ""
				}
			}
			f.names = append(f.names, n)
			if n != "" {
				widest = max(widest, labelW(p.Kind, n))
			}
		}
		margin := widest/2 + 1
		lo, hi := ps[0].X, ps[len(ps)-1].X
		for _, p := range ps {
			x := margin + (w-1-2*margin)/2
			if hi > lo {
				x = margin + int(math.Round((p.X-lo)/(hi-lo)*float64(w-1-2*margin)))
			}
			f.cols = append(f.cols, x)
		}
		var lab []int // the labelled ports
		for k := range ps {
			if f.names[k] != "" {
				lab = append(lab, k)
			}
		}
		wide := func(k int) int { // a note only ever goes under the test's own ports
			if m.State[ps[k].ID] == handson.PortOther {
				return labelW(ps[k].Kind, f.names[k])
			}
			return max(labelW(ps[k].Kind, f.names[k]), noteW)
		}
		fits := func(step int) bool {
			for i := step; i < len(lab); i++ {
				a, b := lab[i-step], lab[i]
				if f.cols[b]-f.cols[a] < (wide(a)+wide(b)+1)/2+2 { // two blanks between
					return false
				}
			}
			return true
		}
		f.low = make([]bool, len(ps))
		if fits(1) {
			return f, true, false
		}
		f.stagger = true
		for i, k := range lab {
			f.low[k] = i%2 == 1
		}
		if level == 3 && !fits(2) { // ports one above another (a Mac Pro's): set apart along the strip
			for k := 1; k < len(ps); k++ {
				f.cols[k] = max(f.cols[k], f.cols[k-1]+3)
			}
			for i := 2; i < len(lab); i++ {
				a, b := lab[i-2], lab[i]
				f.cols[b] = max(f.cols[b], f.cols[a]+(wide(a)+wide(b)+1)/2+2)
				for k := b + 1; k < len(ps); k++ {
					f.cols[k] = max(f.cols[k], f.cols[k-1]+3)
				}
			}
			// then back from the right end, where the pushing may have run past the strip
			f.cols[len(ps)-1] = min(f.cols[len(ps)-1], w-1-margin)
			for k := len(ps) - 2; k >= 0; k-- {
				f.cols[k] = min(f.cols[k], f.cols[k+1]-3)
			}
			for i := len(lab) - 3; i >= 0; i-- {
				a, b := lab[i], lab[i+2]
				f.cols[a] = min(f.cols[a], f.cols[b]-(wide(a)+wide(b)+1)/2-2)
				for k := a - 1; k >= 0; k-- {
					f.cols[k] = min(f.cols[k], f.cols[k+1]-3)
				}
			}
		}
		return f, false, fits(2)
	}
	for level := 0; level < 3; level++ {
		if f, flat, ok := try(level); flat || ok {
			return f
		}
	}
	f, _, _ := try(3) // the shortest, staggered (touching, if even they don't fit)
	return f
}

// labelW is a name's width with its icon.
func labelW(kind, name string) int {
	if ic := portIcon(kind); ic != "" {
		return width(ic) + 1 + width(name)
	}
	return width(name)
}

// faceH is the rows a face takes: title, a blank, the strip, names and notes (two rows more
// when staggered).
func faceH(m *handson.PortMap, s identify.PortSide, w int) int {
	if layoutFace(m, s, w).stagger {
		return 9
	}
	return 7
}

// faceTitle names a face as drawn: the back as seen from behind, with where its ports are when
// you stand in front; an iMac's back mirrored, as seen reaching round from the front (0.7.3
// review), with where its ports are behind the screen.
func faceTitle(s identify.PortSide) string {
	switch s.Side {
	case "back":
		mid := 0.0
		for _, p := range s.Ports {
			mid += p.X / float64(len(s.Ports))
		}
		if s.Mirrored {
			switch {
			case s.Length <= 0:
			case mid < s.Length/3:
				return i18n.T("Back, reaching round from the front (the ports are behind the left side)")
			case mid > s.Length*2/3:
				return i18n.T("Back, reaching round from the front (the ports are behind the right side)")
			}
			return i18n.T("Back, reaching round from the front")
		}
		switch {
		case s.Length <= 0:
		case mid < s.Length/3: // as seen from behind, the left: the right from the front
			return i18n.T("Back, seen from behind (from the front, the ports are behind the right side)")
		case mid > s.Length*2/3:
			return i18n.T("Back, seen from behind (from the front, the ports are behind the left side)")
		}
		return i18n.T("Back, seen from behind")
	case "front":
		return i18n.T("Front")
	case "top":
		return i18n.T("Top")
	case "left": // a desktop's edge (an iMac's SD slot)
		return i18n.T("Left side")
	case "right":
		return i18n.T("Right side")
	}
	return s.Side
}

// backStrip is a desktop's face (inside its box, which has the title): the edge cropped to the ports, every port at its real spacing,
// names (icon and name) under the ports and notes under the names; every other one a row
// lower when the widest they can get would touch.
func backStrip(gr *g.Grid, m *handson.PortMap, s identify.PortSide, x, y, w int, t float64) {
	f := layoutFace(m, s, w)
	sy := y + 2
	edgeStrip(gr, x, sy, w)
	for k, p := range f.ports {
		st, c := stateOf(m, p), x+f.cols[k]
		portShape(gr, c-1, sy+1, p.Kind, st)
		if st == handson.PortNext {
			ringPort(gr, c-1, sy, t)
		}
		r := sy + 3
		if f.low[k] {
			r += 2
		}
		if f.names[k] == "" {
			continue
		}
		lx := c - labelW(p.Kind, f.names[k])/2
		if ic := portIcon(p.Kind); ic != "" {
			lx = gr.Put(lx, r, ic, st.Color()) + 1
		}
		gr.Put(lx, r, f.names[k], nameColor(st))
		if n := note(m, p, "up"); n != "" {
			gr.Put(c-width(n)/2, r+1, n, st.Color())
		}
	}
}
