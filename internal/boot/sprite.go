// Package boot is the Boot layer: Boot the character, his idle
// life and expressions, his typed lines, and the band effects that play beside him while checks
// run. The core screens work without it (--plain).
package boot

import (
	"math"

	g "github.com/omaboot/boot-live/internal/grid"
)

// Expr is one of Boot's expressions.
type Expr string

const (
	Idle      Expr = "idle" // neutral
	Work      Expr = "work" // thinking: eyes and mouth shift as he works
	Pleased   Expr = "pleased"
	Hmm       Expr = "hmm" // partial: one eye narrowed
	Concerned Expr = "concerned"
	Shrug     Expr = "shrug"
	Sleep     Expr = "sleep"
)

// Size of the sprite in cells (16 pixels wide, up to 13 pixel rows at two per cell).
const (
	W = 16
	H = 7
)

// TypeCPS is how fast Boot types, in characters per second.
const TypeCPS = 80

// Life is Boot's idle motion at one moment.
type Life struct {
	T      float64
	Blink  bool
	Glance int // -1, 0, 1: eyes left, centre, right
	Bob    bool
	Down   bool // eyes down: a glance at the console as it opens
	Gold   bool
	Cross  bool // eyes as ×
	Rise   int  // pixel rows up
}

// LifeAt gives the idle motion at t seconds: a blink every 3.1–4.6 s, a glance now and then, a
// one-pixel bob every second.
func LifeAt(t float64) Life {
	const cyc = 3.9
	k := math.Floor(t / cyc)
	ph := t - k*cyc
	jitter := Hash(int(k), 7, 0) * 1.4
	l := Life{T: t, Blink: math.Abs(ph-(1.2+jitter)) < 0.09}
	switch h := Hash(int(math.Floor(t/2.7)), 3, 0); {
	case h < 0.2:
		l.Glance = -1
	case h > 0.8:
		l.Glance = 1
	}
	l.Bob = int(math.Floor(t))%2 == 1
	return l
}

// Hash is a cheap integer hash to [0, 1).
func Hash(x, y, s int) float64 {
	h := uint32(x)*374761393 + uint32(y)*668265263 + uint32(s)*2147483647
	h = (h ^ (h >> 13)) * 1274126177
	return float64(h^(h>>16)) / 4294967296
}

// Draw paints Boot with his top-left cell at x, y.
func Draw(gr *g.Grid, x, y int, ex Expr, l Life) {
	e := l.Glance
	if ex == Work {
		e = 1
	}
	bl := l.Blink || ex == Sleep
	tip := byte('y')
	if ex != Sleep && int(math.Floor(l.T*2))%2 == 1 {
		tip = 'Y'
	}
	face := func() []byte { return []byte(".ckkkkkkkkkkkkc.") }
	r4, r5, r6, r7, r8 := face(), face(), face(), face(), face()
	L, R := 4+e, 9+e
	switch {
	case l.Cross:
		for _, p := range []int{L, R} {
			r4[p], r4[p+2], r5[p+1], r6[p], r6[p+2] = 'W', 'W', 'W', 'W', 'W'
		}
	case bl:
		for _, p := range []int{L, R} {
			for i := 0; i < 3; i++ {
				r5[p+i] = 'C'
			}
		}
	case ex == Pleased:
		for _, p := range []int{L, R} {
			r5[p], r4[p+1], r5[p+2] = 'W', 'W', 'W'
		}
	case ex == Concerned:
		for _, p := range []int{L, R} {
			r4[p], r5[p+1], r4[p+2], r6[p+1] = 'W', 'W', 'W', 'W'
		}
	case ex == Hmm:
		for i := 0; i < 3; i++ {
			r4[L+i], r5[L+i], r5[R+i] = 'W', 'W', 'W'
		}
	default:
		for _, p := range []int{L, R} {
			for i := 0; i < 3; i++ {
				r4[p+i], r5[p+i] = 'W', 'W'
			}
		}
	}
	if l.Down && ex != Sleep && !l.Cross {
		r4, r5, r6 = face(), r4, r5
	}
	m := 6 + e
	mouth := ex
	if l.Cross {
		mouth = Idle
	}
	switch mouth {
	case Pleased:
		r7[m], r8[m+1], r8[m+2], r7[m+3] = 'C', 'C', 'C', 'C'
	case Concerned:
		r8[m], r7[m+1], r7[m+2], r8[m+3] = 'C', 'C', 'C', 'C'
	case Shrug:
		r7[m], r7[m+1], r7[m+2], r8[m+3] = 'C', 'C', 'C', 'C'
	case Sleep:
		r7[m+1], r7[m+2] = 'c', 'c'
	default:
		r7[m+1], r7[m+2] = 'C', 'C'
	}
	feet := "....cc....cc...."
	if ex == Shrug {
		feet = ".c..cc....cc..c."
	}
	rows := []string{
		"......." + string([]byte{tip, tip}) + ".......", // the antenna tip blinks
		"........c.......",
		"..cccccccccccc..",
		".ckkkkkkkkkkkkc.",
		string(r4), string(r5), string(r6), string(r7), string(r8),
		".ckkkkkkkkkkkkc.",
		"..cccccccccccc..",
		feet,
	}
	if l.Gold {
		rows = gold(rows, l.T)
	}
	if ex != Sleep && l.Bob {
		rows = append([]string{"................"}, rows...)
	}
	if l.Rise%2 == 1 {
		rows = append([]string{"................"}, rows...)
	}
	Pixels(gr, x, y-(l.Rise+1)/2, rows)
}

// gold crowns him in bright gold and turns his outline amber, with a bright band sweeping
// across it every 3 s.
func gold(rows []string, t float64) []string {
	rows[0], rows[1] = "....Y..YY..Y....", "....YYYYYYYY...."
	band := -40 // off him between sweeps
	if ph := math.Mod(t, 3); ph < 0.8 {
		band = int(ph/0.8*24) - 4
	}
	for i, row := range rows {
		b := []byte(row)
		for x := range b {
			if b[x] == 'c' {
				b[x] = 'y'
				if d := x + i/3 - band; d >= 0 && d <= 2 {
					b[x] = 'Y'
				}
			}
		}
		rows[i] = string(b)
	}
	return rows
}

var pix = map[byte]g.Color{'k': g.K, 'r': g.RED, 'g': g.GRN, 'y': g.AMB, 'b': g.BLU, 'm': g.VIO, 'c': g.CYN, 'w': g.GRY,
	'K': g.DIM, 'R': g.BRED, 'G': g.BGRN, 'Y': g.YEL, 'B': g.BBLU, 'M': g.BVIO, 'C': g.BCYN, 'W': g.WHT}

// Pixels draws one character per pixel, two pixel rows per cell with ▀ ▄ █ ('.' is transparent).
// A cell's background must be 0–7, so a cell never holds two bright colours.
func Pixels(gr *g.Grid, x, y int, rows []string) {
	for r := 0; r < len(rows); r += 2 {
		top, bot := rows[r], ""
		if r+1 < len(rows) {
			bot = rows[r+1]
		}
		for i := 0; i < max(len(top), len(bot)); i++ {
			a, aok := pixAt(top, i)
			b, bok := pixAt(bot, i)
			if !aok && !bok {
				continue
			}
			if aok && bok && a == b {
				gr.SetBG(x+i, y+r/2, '█', a, g.K)
				continue
			}
			if !aok {
				a = g.K
			}
			if !bok {
				b = g.K
			}
			if a > 7 && b > 7 {
				b -= 8
			}
			if a > 7 || b <= 7 {
				gr.SetBG(x+i, y+r/2, '▀', a, b)
			} else {
				gr.SetBG(x+i, y+r/2, '▄', b, a)
			}
		}
	}
}

func pixAt(row string, i int) (g.Color, bool) {
	if i >= len(row) {
		return 0, false
	}
	c, ok := pix[row[i]]
	return c, ok
}
