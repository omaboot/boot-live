package handson

import (
	"math"
	"strings"

	g "github.com/omaboot/boot-live/internal/grid"
	"github.com/omaboot/boot-live/internal/i18n"
	"github.com/omaboot/boot-live/internal/icons"
)

// ── Band animations where a test had none (owner-approved prototype v54, 2026-10-17) ──

// plugArt is a port test on a Mac with no port map: the port's slot, big (square corners, owner
// 2026-10-17), and a plug that goes in and out every 3 s, the slot lighting while it's in; next to
// it (or under it, when narrow) a box per port seen so far.
func plugArt(kind string, seen int) Panel {
	return func(gr *g.Grid, x, y, w, h int, t float64) {
		const pw = 16
		if w < pw+24 || h < 7 {
			return
		}
		cx, cy := x+4, y+h/2-1
		p := math.Mod(t, 3)
		in := 0.0
		switch {
		case p < 0.8:
			in = p / 0.8
		case p < 2:
			in = 1
		case p < 2.6:
			in = 1 - (p-2)/0.6
		}
		col := g.GRY
		if in >= 1 {
			col = g.BVIO
		}
		outline(gr, cx, cy-2, pw+2, 5, col)
		if in < 0.9 { // the tongue, while the slot is empty
			gr.Put(cx+3, cy, strings.Repeat("▄", pw-4), g.DIM)
		}
		travel := min(34, w-pw-16)
		hx := cx + 2 + int(math.Round((1-in)*float64(pw+travel)))
		gr.Put(hx, cy-1, strings.Repeat("▄", pw-2), g.WHT)
		gr.Put(hx, cy, strings.Repeat("█", pw-2), g.WHT)
		gr.Put(hx, cy+1, strings.Repeat("▀", pw-2), g.WHT)
		for i := hx + pw - 2; i < hx+pw+12 && i < x+w; i++ {
			gr.Set(i, cy, '═', g.GRY)
		}
		tx, ty := cx+pw+travel+18, cy-1
		if tx > x+w-24 {
			tx, ty = cx, cy+3
		}
		label := i18n.T("%s ports seen", kind)
		gr.Put(gr.Put(tx, ty, icons.USB, g.GRY)+1, ty, cutRunes(label, x+w-tx-3), g.GRY)
		px := tx
		for i := 0; i < max(4, seen); i++ {
			if i < seen {
				px = gr.Put(px, ty+1, "██ ", g.BVIO)
			} else {
				px = gr.Put(px, ty+1, "░░ ", g.DIM)
			}
		}
	}
}

// lensShut is the camera when there's no video device: its lens, the iris trying to open and
// shutting again every 3 s; next to it what's there in its place and why it stays shut.
func lensShut(what string) Panel {
	return func(gr *g.Grid, x, y, w, h int, t float64) {
		const R = 4
		if w < 40 || h < 2*R+1 {
			return
		}
		cx, cy := x+R*2+2, y+h/2
		p := math.Mod(t, 3)
		open := 0.0
		switch {
		case p < 0.5:
			open = p / 0.5 * 0.45
		case p < 1.1:
			open = 0.45
		case p < 1.6:
			open = 0.45 * (1 - (p-1.1)/0.5)
		}
		for j := -R; j <= R; j++ {
			for i := -R * 2; i <= R*2; i++ {
				d := math.Hypot(float64(i)/2, float64(j))
				switch {
				case d > R+0.3:
				case d > R-0.9:
					gr.Set(cx+i, cy+j, '█', g.GRY)
				case d < open*R:
					gr.Set(cx+i, cy+j, '░', g.BBLU)
				default:
					gr.Set(cx+i, cy+j, '▓', g.DIM)
				}
			}
		}
		tx := cx + R*2 + 5
		if p < 1.6 {
			gr.Put(tx, cy-1, cutRunes(i18n.T("opening it…"), x+w-tx), g.DIM)
		} else {
			gr.Put(tx, cy-1, cutRunes(i18n.T("no video device"), x+w-tx), g.WHT)
		}
		for i, l := range wrapWords(what, x+w-tx) {
			if i < 3 {
				gr.Put(tx, cy+1+i, l, g.DIM)
			}
		}
	}
}
