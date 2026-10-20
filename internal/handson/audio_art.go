package handson

import (
	"math"
	"strings"

	"github.com/omaboot/boot-live/internal/boot"
	g "github.com/omaboot/boot-live/internal/grid"
	"github.com/omaboot/boot-live/internal/i18n"
)

// The sound tests' band pictures (the test's animation takes 2/3 of the band's field): the Mac's
// two speakers with sound waves on the side playing,
// headphones with their plug and the ear playing, the monitor with its speakers, and the
// microphone's meter. The waves run the whole time a sound plays, never in step with the beeps,
// so the picture can't give a count away.

// sndWrap breaks s into lines of at most w runes, at spaces.
func sndWrap(s string, w int) []string {
	if w <= 0 {
		return nil
	}
	var out []string
	line := ""
	for _, word := range strings.Fields(s) {
		switch {
		case line == "":
			line = word
		case width(line)+1+width(word) <= w:
			line += " " + word
		default:
			out = append(out, line)
			line = word
		}
		for width(line) > w {
			r := []rune(line)
			out = append(out, string(r[:w]))
			line = string(r[w:])
		}
	}
	if line != "" {
		out = append(out, line)
	}
	return out
}

// sndText puts s wrapped to w from x, y, at most rows lines; it returns the row after it.
func sndText(gr *g.Grid, x, y, w, rows int, s string, c g.Color) int {
	ls := sndWrap(s, w)
	if len(ls) > rows {
		ls = ls[:rows]
	}
	for _, l := range ls {
		gr.Put(x, y, l, c)
		y++
	}
	return y
}

// sndCentre puts s centred on column cx.
func sndCentre(gr *g.Grid, cx, y int, s string, c g.Color) {
	gr.Put(cx-width(s)/2, y, s, c)
}

// sndPix is a small canvas of square pixels (a cell's half), drawn with boot.Pixels: '.' is
// transparent, the letters are boot's pixel colours ('M' bright violet, 'm' violet, 'w' grey,
// 'K' dim, 'W' white).
type sndPix struct {
	w, h int
	px   [][]byte
}

func newSndPix(w, h int) *sndPix {
	p := &sndPix{w: w, h: h, px: make([][]byte, h)}
	for i := range p.px {
		p.px[i] = []byte(strings.Repeat(".", w))
	}
	return p
}

func (p *sndPix) set(x, y int, c byte) {
	if x >= 0 && y >= 0 && x < p.w && y < p.h {
		p.px[y][x] = c
	}
}

// disc fills the pixels within r of (cx, cy) and outside r0.
func (p *sndPix) disc(cx, cy, r0, r float64, c byte) {
	for y := 0; y < p.h; y++ {
		for x := 0; x < p.w; x++ {
			if d := math.Hypot(float64(x)+0.5-cx, float64(y)+0.5-cy); d <= r && d > r0 {
				p.px[y][x] = c
			}
		}
	}
}

// oval fills the pixels within the ellipse at (cx, cy) with radii rx, ry.
func (p *sndPix) oval(cx, cy, rx, ry float64, c byte) {
	for y := 0; y < p.h; y++ {
		for x := 0; x < p.w; x++ {
			dx, dy := (float64(x)+0.5-cx)/rx, (float64(y)+0.5-cy)/ry
			if dx*dx+dy*dy <= 1 {
				p.px[y][x] = c
			}
		}
	}
}

// arc draws the pixels at radius r (±0.5) from (cx, cy) within half degrees of the direction
// at degrees (0 right, 90 down, 180 left, -90 up).
func (p *sndPix) arc(cx, cy, r, at, half float64, c byte) {
	for y := 0; y < p.h; y++ {
		for x := 0; x < p.w; x++ {
			dx, dy := float64(x)+0.5-cx, float64(y)+0.5-cy
			d := math.Hypot(dx, dy)
			if math.Abs(d-r) > 0.5 {
				continue
			}
			diff := math.Mod(math.Atan2(dy, dx)*180/math.Pi-at+540, 360) - 180
			if math.Abs(diff) <= half {
				p.px[y][x] = c
			}
		}
	}
}

func (p *sndPix) draw(gr *g.Grid, x, y int) {
	rows := make([]string, p.h)
	for i, r := range p.px {
		rows[i] = string(r)
	}
	boot.Pixels(gr, x, y, rows)
}

// waves adds n arcs of sound around (cx, cy), from radius from, 3 pixels apart, towards at
// degrees (0 right, 180 left), travelling outward with t.
func (p *sndPix) waves(cx, cy, from, at float64, n int, t float64) {
	lead := int(t*3) % n
	for k := 0; k < n; k++ {
		c := byte('m')
		if k == lead {
			c = 'M'
		}
		p.arc(cx, cy, from+3*float64(k), at, 38, c)
	}
}

// speakerIcon puts a speaker, the usual sign (a box and a cone), 8 pixels wide and 12 tall, its
// top left at x, y, the cone opening towards dir (1 right, -1 left); lit while it plays, dim
// when muted. It returns where its sound comes from (the cone's root, centred).
func (p *sndPix) speakerIcon(x, y, dir int, lit, dim bool) (float64, float64) {
	box, cone := byte('w'), byte('m')
	switch {
	case dim:
		box, cone = 'K', 'K'
	case lit:
		cone = 'M'
	}
	col := func(i int) int { // the icon's i-th column from its back, on the canvas
		if dir > 0 {
			return x + i
		}
		return x + 7 - i
	}
	for i := 0; i < 3; i++ {
		for j := 3; j < 9; j++ {
			p.set(col(i), y+j, box)
		}
	}
	for i := 0; i < 5; i++ {
		for j := 3 - i - 1; j < 9+i+1; j++ {
			if j >= 0 && j < 12 {
				p.set(col(3+i), y+j, cone)
			}
		}
	}
	return float64(col(3)) + 0.5, float64(y) + 6
}

// speakersArt is the Mac's two speakers in the band, facing out: waves from the side(s) playing
// (side sideLeft, sideRight or sideBoth; playing false: none), the side asked about named in
// white. muted greys them out (headphones in the jack).
func speakersArt(side int, playing, muted bool) Panel {
	return func(gr *g.Grid, x, y, w, h int, t float64) {
		rows := min(h, 8)
		top := y + max(0, (h-rows-1)/2)
		p := newSndPix(w, 2*rows)
		cx := w / 2
		gap := min(14, max(4, w-16-30))
		lx, rx := cx-gap/2-8, cx+gap/2
		py := (2*rows - 12) / 2
		for i, bx := range []int{lx, rx} {
			s, dir := sideLeft, -1
			if i == 1 {
				s, dir = sideRight, 1
			}
			on := (side == s || side == sideBoth) && playing && !muted
			ox, oy := p.speakerIcon(bx, py, dir, on, muted)
			if on {
				at := 0.0
				if dir < 0 {
					at = 180
				}
				p.waves(ox, oy, 8.5, at, 3, t)
			}
		}
		p.draw(gr, x, top)
		for i, bx := range []int{lx, rx} {
			s := []int{sideLeft, sideRight}[i]
			label, lc := i18n.T("left"), g.DIM
			if s == sideRight {
				label = i18n.T("right")
			}
			if (side == s || side == sideBoth) && !muted {
				lc = g.WHT
			}
			sndCentre(gr, x+bx+4, top+rows, label, lc)
		}
		if muted {
			sndCentre(gr, x+cx, top+rows+1, i18n.T("muted: headphones in"), g.DIM)
		}
	}
}

// How the headphones' plug moves.
const (
	hpStill  = iota // where the jack says it is
	hpReach         // asking to plug them in: the plug moves toward the jack and back, never in
	hpUnplug        // asking to take them out: the plug in the jack, pulled out, a pause, again
)

// headphonesArt is headphones on a cable to the jack: the plug in it once the jack reports
// them (jack: "" no jack control, "in", "out"), waves in the ear playing; quiet adds the Mac's
// speakers on the right, which should stay quiet. The headphones, the cable and the plug are
// green while the plug is in, red while it's out (grey with no jack sensor); motion moves the
// plug (hpReach, hpUnplug), and the colours follow where it is.
func headphonesArt(side int, playing bool, jack string, quiet bool, motion int) Panel {
	return func(gr *g.Grid, x, y, w, h int, t float64) {
		jx := x + 3 + 30 + 4 // the jack (the canvas is 30 pixels wide)
		plugX, in := jx-3, jack == "in"
		switch motion {
		case hpReach: // toward the jack and back, stopping two cells short
			plugX, in = jx-5-int(math.Round(3.5*(1+math.Cos(2*math.Pi*t/1.6)))), false
		case hpUnplug: // in for 1 s, out over 0.6 s, out for 1.4 s
			ph := math.Mod(t, 3.0)
			d := 0.0
			switch {
			case ph >= 1.6:
				d = 1
			case ph >= 1.0:
				d = (ph - 1.0) / 0.6
			}
			plugX = jx - 3 - int(math.Round(9*d))
			in = plugX == jx-3
		default:
			if !in {
				plugX = jx - 8
			}
		}
		pix, cc := byte('r'), g.RED // out: red
		switch {
		case in:
			pix, cc = 'G', g.BGRN
		case jack == "" && motion == hpStill:
			pix, cc = 'w', g.GRY
		}
		const pw, ph = 30, 17 // the headphones' canvas, pixels
		hx, top := x+3, y+max(0, (h-12)/2)
		p := newSndPix(pw, ph)
		p.arc(pw/2, 12.5, 12.5, -90, 90, pix) // the band
		p.arc(pw/2, 12.5, 11.5, -90, 90, 'K')
		cups := []float64{2.5, pw - 2.5}
		for i, cx := range cups {
			s := []int{sideLeft, sideRight}[i]
			on := playing && (side == s || side == sideBoth)
			p.oval(cx, 12.5, 2.4, 3.6, pix) // the waves say which ear plays
			if on {
				at := 0.0
				if s == sideRight {
					at = 180
				}
				p.waves(cx, 12.5, 4.5, at, 3, t)
			}
		}
		p.draw(gr, hx, top)
		for i, cx := range cups {
			s := []int{sideLeft, sideRight}[i]
			label, lc := i18n.T("left"), g.DIM
			if s == sideRight {
				label = i18n.T("right")
			}
			if playing && (side == s || side == sideBoth) {
				lc = g.WHT
			}
			sndCentre(gr, hx+int(cx), top+9, label, lc)
		}
		// the cable from the left cup down to the plug, and the jack
		cy := top + 10
		for j := top + 7; j < cy; j++ { // as thick as the band's pixels, so its colour shows
			gr.Put(hx-1, j, "█", cc)
		}
		gr.Put(hx-1, cy, strings.Repeat("▀", max(0, plugX-hx+1)), cc)
		gr.Put(plugX, cy, "██═", cc)
		for j := -1; j <= 1; j++ {
			gr.Put(jx, cy+j, "▐", g.GRY)
		}
		state, sc := i18n.T("jack: empty"), g.DIM
		switch jack {
		case "in":
			state, sc = i18n.T("jack: plugged in"), g.WHT
		case "":
			state = i18n.T("jack: no sensor")
		}
		gr.Put(jx+2, cy, cut(state, max(0, x+w-jx-3)), sc)
		if !quiet || x+w-(jx+2) < 18 {
			return
		}
		// the Mac's own speakers: they should stay quiet
		mx := jx + 2
		for i := 0; i < 2; i++ {
			bx := mx + i*7
			gr.Put(bx, top+1, "┌───┐", g.DIM)
			gr.Put(bx, top+2, "│▄█▄│", g.DIM)
			gr.Put(bx, top+3, "│▀█▀│", g.DIM)
			gr.Put(bx, top+4, "└───┘", g.DIM)
		}
		sndText(gr, mx, top+5, x+w-mx-1, 2, i18n.T("Mac speakers: quiet?"), g.GRY)
	}
}

// monitorArt is the monitor with its speakers: waves on both sides while it plays, its name
// once it has described itself (ELD).
func monitorArt(name string, eld, playing bool) Panel {
	return func(gr *g.Grid, x, y, w, h int, t float64) {
		mw := min(28, w-16)
		if mw < 12 {
			return
		}
		mx, top := x+(w-mw)/2, y+max(0, (h-9)/2)
		c := g.GRY
		if !eld && !playing {
			c = g.DIM
		}
		if playing { // from its sides, where its speakers are
			wv := newSndPix(w, 2*min(h, 9))
			cy := 2*3.5 + 1
			wv.waves(float64(mx-x)-0.5, cy, 2.5, 180, 3, t)
			wv.waves(float64(mx-x+mw)+0.5, cy, 2.5, 0, 3, t)
			wv.draw(gr, x, top-1)
		}
		gr.Put(mx, top, "┌"+strings.Repeat("─", mw-2)+"┐", c)
		for j := 1; j < 6; j++ {
			gr.Put(mx, top+j, "│", c)
			gr.Put(mx+mw-1, top+j, "│", c)
		}
		gr.Put(mx, top+6, "└"+strings.Repeat("─", mw-2)+"┘", c)
		gr.Put(mx+mw/2-1, top+7, "██", c)
		gr.Put(mx+mw/2-4, top+8, "▀▀▀▀▀▀▀▀", c)
		if name == "" {
			name = i18n.T("monitor")
		}
		sndCentre(gr, mx+mw/2, top+2, cut(name, mw-4), g.WHT)
		state, sc := i18n.T("no sound description yet"), g.DIM
		if eld {
			state, sc = i18n.T("takes sound"), g.GRY
		}
		sndCentre(gr, mx+mw/2, top+4, cut(state, mw-4), sc)

	}
}

// micArt is the microphone, 7 pixels wide and 13 tall (7 rows), its grille lit when it hears
// more than the room.
func micArt(gr *g.Grid, x, y int, lit bool) {
	grille := "K"
	if lit {
		grille = "C"
	}
	k := grille
	boot.Pixels(gr, x, y, []string{
		"..www..",
		".wwwww.",
		".w" + k + "w" + k + "w.",
		".wwwww.",
		".w" + k + "w" + k + "w.",
		".wwwww.",
		"..www..",
		"w.....w",
		".w...w.",
		"..www..",
		"...w...",
		"...w...",
		".wwwww.",
	})
}
