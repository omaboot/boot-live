package handson

import (
	"strings"

	g "github.com/omaboot/boot-live/internal/grid"
	"github.com/omaboot/boot-live/internal/i18n"
)

// Small pictures the tests share: the band's animations (the right 2/3 of its field) and the
// pieces they're made of.

// levelBar is a level of max as a block bar (violet, 40 cells at most).
func levelBar(level, mx int) Panel {
	return func(gr *g.Grid, x, y, w, h int, _ float64) {
		n := min(40, w-4)
		if n <= 0 || mx <= 0 {
			return
		}
		k := min(n, max(0, level*n/mx))
		gr.Put(x, y, "[", g.DIM)
		for i := 0; i < n; i++ {
			if i < k {
				gr.SetBG(x+1+i, y, '█', g.BVIO, g.K)
			} else {
				gr.SetBG(x+1+i, y, '░', g.DIM, g.K)
			}
		}
		gr.Put(x+1+n, y, "]", g.DIM)
	}
}

// outline draws a w×h box-drawing outline in col.
func outline(gr *g.Grid, x, y, w, h int, col g.Color) {
	if w < 2 || h < 2 {
		return
	}
	gr.Put(x, y, "┌"+strings.Repeat("─", w-2)+"┐", col)
	for j := 1; j < h-1; j++ {
		gr.Put(x, y+j, "│", col)
		gr.Put(x+w-1, y+j, "│", col)
	}
	gr.Put(x, y+h-1, "└"+strings.Repeat("─", w-2)+"┘", col)
}

// keyboardArt is a laptop keyboard from above, keys as caps: four rows
// and the space bar, five rows in all, in col. It returns its width.
func keyboardArt(gr *g.Grid, x, y, w int, col g.Color) int {
	rows := []int{14, 14, 13, 12}
	kw := max(2, min(4, (w-2)/14))
	for r, n := range rows {
		off := r * (kw / 2)
		for k := 0; k < n; k++ {
			gr.Put(x+off+k*kw, y+r, strings.Repeat("▄", kw-1), col)
		}
	}
	gr.Put(x+3*kw, y+4, strings.Repeat("▄", kw*7-1), col)
	return 14 * kw
}

// kbdLightArt is the keyboard light test's animation: a pointer down to the keyboard and a dim
// keyboard. It never changes with the blinks (the screen mustn't give the count away); once decided
// it says how many there were.
func kbdLightArt(gr *g.Grid, x, y, w, h int, blinks int, decided bool) {
	kw := max(2, min(4, (w-2)/14))
	kx := x + max(0, (w-14*kw-2)/2)
	ky := y + max(1, (h-7)/2)
	if decided {
		gr.Put(kx, ky, i18n.N("the keys blinked %d time", "the keys blinked %d times", blinks, blinks), g.GRY)
	} else {
		at := gr.Put(kx, ky, i18n.T("on your keyboard, not here"), g.WHT)
		gr.Put(at+2, ky, "↓", g.BVIO)
	}
	keyboardArt(gr, kx, ky+2, w, g.DIM)
}

// screenArt is the brightness test's animation: a laptop screen with a fixed grey ramp, and the
// words to watch the screen itself. It doesn't follow the backlight: the screen does.
func screenArt(gr *g.Grid, x, y, w, h int, _ float64) {
	sw := min(26, w/2)
	if sw < 12 || h < 7 {
		return
	}
	sh := min(h-3, 8)
	sx, sy := x+2, y+(h-sh-2)/2
	outline(gr, sx, sy, sw, sh, g.GRY)
	shades := []rune("░▒▓█")
	for j := 1; j < sh-1; j++ {
		for i := 1; i < sw-1; i++ {
			gr.Set(sx+i, sy+j, shades[min(3, (i-1)*4/(sw-2))], g.GRY)
		}
	}
	gr.Put(sx-2, sy+sh, "▀"+strings.Repeat("▀", sw+2)+"▀", g.DIM) // the base
	tx := sx + sw + 4
	room := x + w - tx
	ls := []string{i18n.T("watch the screen,"), i18n.T("not this picture:"), i18n.T("it stays the same")}
	for k, s := range ls {
		col := g.GRY
		if k == 0 {
			col = g.WHT
		}
		gr.Put(tx, sy+1+k*2, cutRunes(s, room), col)
	}
}

// handPixels is a hand from the side, fingers up, as Pixels draws it.
var handPixels = []string{".w.w.w.w..", ".w.w.w.w..", ".w.w.w.w..", "wwwwwwwww.", "wwwwwwwwww", "wwwwwwwww.", ".wwwwwww..", ".wwwwwww.."}

// phonePixels is a phone with its flashlight on (the light sensor in a dark room).
var phonePixels = []string{"wwwwww", "wYwwww", "wwwwww", "wkkkkw", "wkkkkw", "wkkkkw", "wkkkkw", "wwwwww"}
