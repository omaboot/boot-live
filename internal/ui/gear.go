package ui

import (
	"strings"

	g "github.com/omaboot/boot-live/internal/grid"
	"github.com/omaboot/boot-live/internal/i18n"
)

// ── What to have at hand ("Boot needs you") ────────────────────────────────────

// gearTier is how much a test's gear matters: 0 essential, 1 recommended;
// a test not listed here is optional.
var gearTier = map[string]int{
	"power.charging": 0, "network.wifi": 0, "ports.usb-a": 0,
	"audio.headphone": 1, "graphics.external-display": 1, "network.bluetooth": 1, "network.ethernet": 1,
	"audio.display-out": 2, "ports.sd-card": 2, "ports.thunderbolt": 2, "ports.usb-c": 2,
}

// gearGroup is one titled group of gear.
type gearGroup struct {
	Title string
	Items []string
}

// gear is what to have at hand for the planned tests: Essentials, Recommended, Optional, each in
// plan order, every thing once (in the highest group a test puts it in); empty groups left out.
func gear(h *HandsOn) []gearGroup {
	tier := map[string]int{}
	var order []string
	for _, s := range h.Setup.Plan {
		if s.Gear == "" {
			continue
		}
		k := 2
		if t, ok := gearTier[s.ID]; ok {
			k = t
		}
		if old, ok := tier[s.Gear]; !ok {
			tier[s.Gear] = k
			order = append(order, s.Gear)
		} else {
			tier[s.Gear] = min(old, k)
		}
	}
	gs := []gearGroup{{Title: i18n.T("Essentials")}, {Title: i18n.T("Recommended")}, {Title: i18n.T("Optional")}}
	for _, msg := range order {
		gs[tier[msg]].Items = append(gs[tier[msg]].Items, i18n.S(msg))
	}
	var out []gearGroup
	for _, grp := range gs {
		if len(grp.Items) > 0 {
			out = append(out, grp)
		}
	}
	return out
}

// gearCol is one column of the gear's layout: its groups (one after another, a blank row
// between) and its width.
type gearCol struct {
	groups []gearGroup
	w      int
}

// gearRows is a column's rows at width w: each group's title, then its items as "· item",
// wrapped under themselves.
func gearRows(groups []gearGroup, w int) int {
	n := 0
	for i, grp := range groups {
		if i > 0 {
			n++
		}
		n++
		for _, it := range grp.Items {
			n += max(1, len(wrap(it, w-2)))
		}
	}
	return n
}

// gearNatural is a column's width with nothing wrapped.
func gearNatural(groups []gearGroup) int {
	w := 0
	for _, grp := range groups {
		w = max(w, width(grp.Title))
		for _, it := range grp.Items {
			w = max(w, width(it)+2)
		}
	}
	return w
}

const gearGap = 3 // blank columns between the gear's columns

// gearLayout fits the groups in w×rows: one column if they fit, else two (the groups split in
// order, the split and the widths that need the fewest rows), else three. ok is false when
// nothing fits; the caller then lists them compactly.
func gearLayout(groups []gearGroup, w, rows int) (cols []gearCol, ok bool) {
	if len(groups) == 0 {
		return nil, true
	}
	if gearRows(groups, w) <= rows && gearNatural(groups) <= w {
		return []gearCol{{groups, w}}, true
	}
	const minW = 14
	// two columns: every split of the groups, every width of the first column
	best, bestH := []gearCol(nil), rows+1
	for k := 1; k < len(groups); k++ {
		a, b := groups[:k], groups[k:]
		na, nb := gearNatural(a), gearNatural(b)
		if na+gearGap+nb <= w { // nothing wraps: share the room evenly if both fit their half
			half := (w - gearGap) / 2
			wa := na
			if na <= half && nb <= half {
				wa = half
			}
			if h := max(gearRows(a, wa), gearRows(b, w-gearGap-wa)); h < bestH {
				best, bestH = []gearCol{{a, wa}, {b, w - gearGap - wa}}, h
			}
			continue
		}
		for wa := minW; wa <= w-gearGap-minW; wa++ {
			wb := w - gearGap - wa
			if h := max(gearRows(a, wa), gearRows(b, wb)); h < bestH {
				best, bestH = []gearCol{{a, wa}, {b, wb}}, h
			}
		}
	}
	if best != nil {
		return best, true
	}
	if len(groups) == 3 {
		cw := (w - 2*gearGap) / 3
		if cw >= minW {
			cs := []gearCol{{groups[:1], cw}, {groups[1:2], cw}, {groups[2:], w - 2*(cw+gearGap)}}
			h := 0
			for _, c := range cs {
				h = max(h, gearRows(c.groups, c.w))
			}
			if h <= rows {
				return cs, true
			}
		}
	}
	return nil, false
}

// drawGear draws the gear in w×rows at x, y (the band's 2/3 on "Boot needs you"): titled groups,
// in columns when one doesn't fit; as a last resort, each group on its own rows as
// "Title: a, b, c". Nothing is drawn past w or rows.
func drawGear(gr *g.Grid, x, y, w, rows int, groups []gearGroup) {
	old := gr.Clip(x, y, x+w, y+rows)
	defer gr.Restore(old)
	cols, ok := gearLayout(groups, w, rows)
	if !ok {
		yy := y
		for _, grp := range groups {
			for i, l := range wrap(grp.Title+": "+strings.Join(grp.Items, ", "), w) {
				gr.Put(x, yy, l, g.GRY)
				if i == 0 {
					gr.Put(x, yy, grp.Title+":", g.WHT)
				}
				yy++
			}
		}
		return
	}
	cx := x
	for _, c := range cols {
		yy := y
		for i, grp := range c.groups {
			if i > 0 {
				yy++
			}
			gr.Put(cx, yy, grp.Title, g.WHT)
			yy++
			for _, it := range grp.Items {
				gr.Put(cx, yy, "·", g.DIM)
				for _, l := range wrap(it, c.w-2) {
					gr.Put(cx+2, yy, cut(l, c.w-2), g.GRY)
					yy++
				}
			}
		}
		cx += c.w + gearGap
	}
}
