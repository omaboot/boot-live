package ui

import (
	"math"

	"github.com/omaboot/boot-live/internal/boot"
	g "github.com/omaboot/boot-live/internal/grid"
	"github.com/omaboot/boot-live/internal/i18n"
)

// The screensaver, on screens waiting for input (never during the quick pass): after DozeAfter
// Boot dozes in the frame at one frame per second, nothing else moving; after BlankAfter the
// console is blanked. The key that wakes it is swallowed, so it can't pick anything.
const (
	DozeAfter  = 5 * 60.0  // seconds without a key
	BlankAfter = 15 * 60.0 // seconds without a key
)

// remMinutes is the countdown to the blank screen while dozing, in whole minutes (rounded up).
func remMinutes(dozing float64) int {
	return max(1, int(math.Ceil((BlankAfter-DozeAfter-dozing)/60)))
}

// drawDoze clears the frame's inside and draws Boot asleep in its middle; s is seconds dozing,
// and only its whole part changes the picture.
func drawDoze(gr *g.Grid, o *Options, b Box, s float64) {
	gr.Fill(b.X0+1, b.Y0+1, b.X1-b.X0-1, b.Y1-b.Y0-1, ' ', g.GRY, g.K)
	for y := b.Y0 + 1; y < b.Y1; y++ { // the dividers' joins go with the content
		gr.Put(b.X0, y, "│", b.FC)
		gr.Put(b.X1, y, "│", b.FC)
	}
	n := int(math.Floor(s))
	bx, by := b.X+b.W/2-boot.W/2, b.Y+b.H/2-boot.H
	zx := bx + boot.W + 1
	if o.Plain {
		zx = b.X + b.W/2 - 2
	} else {
		boot.Draw(gr, bx, by, boot.Sleep, boot.Life{T: float64(n)})
	}
	zs := [][][2]int{{{0, 2}}, {{0, 2}, {2, 1}}, {{2, 1}, {4, 0}}}[n%3]
	for _, z := range zs {
		gr.Put(zx+z[0], by+z[1], "z", g.GRY)
	}
	mins := remMinutes(s)
	l1 := i18n.N("zzz. in %d minute I go into REM sleep, and the screen goes blank.",
		"zzz. in %d minutes I go into REM sleep, and the screen goes blank.", mins, mins)
	l2 := i18n.T("any key wakes me up. that key does nothing else.")
	if o.Plain {
		l1 = i18n.N("Dozing. In %d minute the screen goes blank.", "Dozing. In %d minutes the screen goes blank.", mins, mins)
		l2 = i18n.T("Any key wakes it up; that key does nothing else.")
	}
	if o.Plain {
		gr.Put(b.X+b.W/2-width(l1)/2, by+boot.H+3, l1, g.GRY)
		gr.Put(b.X+b.W/2-width(l2)/2, by+boot.H+4, l2, g.DIM)
		return
	}
	// his words in his box under him, its ▲ up at him (every screen); whole,
	// no cursor: he's asleep
	says := []line{{-1e6, l1}, {-1e6, l2}}
	w := sayWidth(b.W-2, says)
	sayBox(gr, nil, b.X+b.W/2-w/2, by+boot.H+2, w, 4, float64(n), says, bx+boot.W/2)
}
