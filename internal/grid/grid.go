// Package grid is the character grid every screen draws on: one rune and two palette colours
// per cell, the way the Linux console stores its screen. Screens draw into a Grid; Lines turns it
// into one escape-coded string per row for Bubble Tea, which redraws only the rows that changed.
package grid

import (
	"strconv"
	"strings"
)

// Color is a VT palette index, 0–15. Backgrounds can only be 0–7 on the Linux console.
type Color uint8

// Colour roles (VT indices).
const (
	K Color = iota // near-black: the background
	RED
	GRN
	AMB
	BLU
	VIO
	CYN
	GRY // body text
	DIM
	BRED
	BGRN // pass: the only green on screen
	YEL
	BBLU
	BVIO // the accent
	BCYN
	WHT // the console has no bold: bright white is its bold
)

// Palette is 16 colours as RGB.
type Palette [16][3]uint8

// OmaBoot is our palette: near-black, cool greys, one violet accent, Boot in cyan, green only for passes.
var OmaBoot = mustPalette("#0b0d10", "#c2453f", "#3fa173", "#c98f3a", "#5276b8", "#8a78e6", "#4fa6ad", "#b4bcc2",
	"#4a535b", "#f06a63", "#5fd39b", "#ffd479", "#89aef0", "#b8a9ff", "#8fe3e8", "#eef2f4")

func mustPalette(hex ...string) Palette {
	var p Palette
	for i, h := range hex {
		v, err := strconv.ParseUint(strings.TrimPrefix(h, "#"), 16, 32)
		if err != nil || i >= 16 {
			panic("bad palette entry " + h)
		}
		p[i] = [3]uint8{uint8(v >> 16), uint8(v >> 8), uint8(v)}
	}
	return p
}

// Hex gives colour i as "rrggbb".
func (p Palette) Hex(i int) string {
	c := p[i]
	const digits = "0123456789abcdef"
	b := make([]byte, 6)
	for k, v := range c {
		b[2*k], b[2*k+1] = digits[v>>4], digits[v&15]
	}
	return string(b)
}

// Cell is one character cell.
type Cell struct {
	R      rune
	FG, BG Color
}

// Grid is a screen of W×H cells.
type Grid struct {
	W, H  int
	cells []Cell
	clip  *[4]int // x0, y0, x1, y1: only cells inside are written (Clip), or nil
}

// Clip limits every write to the cells x0 ≤ x < x1, y0 ≤ y < y1 until Unclip: a section of a screen
// draws inside its own box and nothing spills into the next. A clip set
// inside another stays inside it (the frame shrunk for the console cuts everything below it). It
// returns the clip it replaced, for Restore.
func (g *Grid) Clip(x0, y0, x1, y1 int) *[4]int {
	old := g.clip
	if old != nil {
		x0, y0, x1, y1 = max(x0, old[0]), max(y0, old[1]), min(x1, old[2]), min(y1, old[3])
	}
	g.clip = &[4]int{x0, y0, x1, y1}
	return old
}

// Restore puts back a clip Clip returned (nil: none).
func (g *Grid) Restore(c *[4]int) { g.clip = c }

// Unclip lets writes reach the whole grid again.
func (g *Grid) Unclip() { g.clip = nil }

// in reports whether x, y is on the grid and inside the clip.
func (g *Grid) in(x, y int) bool {
	if x < 0 || y < 0 || x >= g.W || y >= g.H {
		return false
	}
	c := g.clip
	return c == nil || x >= c[0] && y >= c[1] && x < c[2] && y < c[3]
}

// New makes a cleared grid.
func New(w, h int) *Grid {
	g := &Grid{W: max(w, 0), H: max(h, 0)}
	g.cells = make([]Cell, g.W*g.H)
	g.Clear()
	return g
}

// Clear blanks every cell and drops the clip.
func (g *Grid) Clear() {
	g.clip = nil
	for i := range g.cells {
		g.cells[i] = Cell{' ', GRY, K}
	}
}

// At returns the cell at x, y (a blank cell outside the grid).
func (g *Grid) At(x, y int) Cell {
	if x < 0 || y < 0 || x >= g.W || y >= g.H {
		return Cell{' ', GRY, K}
	}
	return g.cells[y*g.W+x]
}

// Set writes one cell and keeps its background. Cells outside the grid (or the clip) are ignored.
func (g *Grid) Set(x, y int, r rune, fg Color) {
	if !g.in(x, y) {
		return
	}
	c := &g.cells[y*g.W+x]
	c.R, c.FG = r, fg
}

// SetBG writes one cell with a background (0–7 only: the console can't show bright backgrounds).
func (g *Grid) SetBG(x, y int, r rune, fg, bg Color) {
	if !g.in(x, y) {
		return
	}
	g.cells[y*g.W+x] = Cell{r, fg, bg & 7}
}

// Put writes s from x, y in one colour, keeping backgrounds, and returns the column after it.
func (g *Grid) Put(x, y int, s string, fg Color) int {
	for _, r := range s {
		g.Set(x, y, r, fg)
		x++
	}
	return x
}

// PutBG is Put with a background.
func (g *Grid) PutBG(x, y int, s string, fg, bg Color) int {
	for _, r := range s {
		g.SetBG(x, y, r, fg, bg)
		x++
	}
	return x
}

// Fill sets a w×h block to one rune and colours.
func (g *Grid) Fill(x, y, w, h int, r rune, fg, bg Color) {
	for j := 0; j < h; j++ {
		for i := 0; i < w; i++ {
			g.SetBG(x+i, y+j, r, fg, bg)
		}
	}
}

// Text is the grid as plain text, one line per row, trailing blanks trimmed (for dumps).
func (g *Grid) Text() string {
	var b strings.Builder
	for y := 0; y < g.H; y++ {
		var l strings.Builder
		for x := 0; x < g.W; x++ {
			l.WriteRune(g.cells[y*g.W+x].R)
		}
		b.WriteString(strings.TrimRight(l.String(), " "))
		b.WriteByte('\n')
	}
	return b.String()
}

// Mode says how colours are written.
type Mode int

const (
	// VT writes palette indices (SGR 30–37, 90–97, 40–47): the console's palette holds our colours.
	VT Mode = iota
	// TrueColor writes our palette's RGB values, for development in a terminal emulator.
	TrueColor
)

// Lines renders each row with SGR colour codes, changing colour only where it changes and
// dropping trailing blank cells; each row ends with a reset. Background 0 is written as the
// default background (SGR 49), which is colour 0 on the console and set to it in a terminal.
func (g *Grid) Lines(mode Mode, pal *Palette) []string {
	out := make([]string, g.H)
	var b strings.Builder
	for y := 0; y < g.H; y++ {
		row := g.cells[y*g.W : (y+1)*g.W]
		end := len(row)
		for end > 0 && row[end-1].R == ' ' && row[end-1].BG == K {
			end--
		}
		b.Reset()
		cur := Cell{FG: 255, BG: 255}
		for _, c := range row[:end] {
			fgVisible := c.R != ' '
			if c.BG != cur.BG || (fgVisible && c.FG != cur.FG) {
				b.WriteString("\x1b[")
				b.WriteString(sgr(c.FG, c.BG, mode, pal))
				b.WriteByte('m')
				cur.FG, cur.BG = c.FG, c.BG
			}
			b.WriteRune(c.R)
		}
		if end > 0 {
			b.WriteString("\x1b[0m")
		}
		out[y] = b.String()
	}
	return out
}

func sgr(fg, bg Color, mode Mode, pal *Palette) string {
	var s strings.Builder
	s.WriteString("0;")
	if mode == TrueColor {
		c := pal[fg]
		s.WriteString("38;2;" + strconv.Itoa(int(c[0])) + ";" + strconv.Itoa(int(c[1])) + ";" + strconv.Itoa(int(c[2])))
	} else if fg < 8 {
		s.WriteString(strconv.Itoa(30 + int(fg)))
	} else {
		s.WriteString(strconv.Itoa(90 + int(fg) - 8))
	}
	s.WriteByte(';')
	switch {
	case bg == K:
		s.WriteString("49")
	case mode == TrueColor:
		c := pal[bg]
		s.WriteString("48;2;" + strconv.Itoa(int(c[0])) + ";" + strconv.Itoa(int(c[1])) + ";" + strconv.Itoa(int(c[2])))
	default:
		s.WriteString(strconv.Itoa(40 + int(bg&7)))
	}
	return s.String()
}
