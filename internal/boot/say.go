package boot

import (
	"math"

	g "github.com/omaboot/boot-live/internal/grid"
)

// 5×7 pixel letters for the wordmark and the big headline.
var big = map[rune][7]string{
	'O': {".111.", "1...1", "1...1", "1...1", "1...1", "1...1", ".111."},
	'm': {".....", ".....", "11.1.", "1.1.1", "1.1.1", "1.1.1", "1.1.1"},
	'a': {".....", ".....", ".111.", "....1", ".1111", "1...1", ".1111"},
	'B': {"1111.", "1...1", "1...1", "1111.", "1...1", "1...1", "1111."},
	'o': {".....", ".....", ".111.", "1...1", "1...1", "1...1", ".111."},
	't': {".1...", ".1...", "1111.", ".1...", ".1...", ".1..1", "..11."},
	'?': {".111.", "1...1", "....1", "...1.", "..1..", ".....", "..1.."},
	'Y': {"1...1", "1...1", ".1.1.", "..1..", "..1..", "..1..", "..1.."},
	'y': {".....", ".....", "1...1", "1...1", ".1111", "....1", ".111."},
	'!': {"..1..", "..1..", "..1..", "..1..", "..1..", ".....", "..1.."},
	'T': {"11111", "..1..", "..1..", "..1..", "..1..", "..1..", "..1.."},
	'h': {"1....", "1....", "1.11.", "11..1", "1...1", "1...1", "1...1"},
	'n': {".....", ".....", "1.11.", "11..1", "1...1", "1...1", "1...1"},
	'k': {"1....", "1....", "1..1.", "1.1..", "11...", "1.1..", "1..1."},
	's': {".....", ".....", ".1111", "1....", ".111.", "....1", "1111."},
	'D': {"1111.", "1...1", "1...1", "1...1", "1...1", "1...1", "1111."},
	'e': {".....", ".....", ".111.", "1...1", "11111", "1....", ".111."},
	'.': {".....", ".....", ".....", ".....", ".....", ".....", "..1.."},
}

// Wordmark draws "OmaBoot?" in pixel letters: white, the ? in the accent.
func Wordmark(gr *g.Grid, x, y int, word string) {
	rows := make([]string, 8)
	for _, ch := range word {
		glyph := big[ch]
		for r := 0; r < 8; r++ {
			line := "....."
			if r < 7 {
				line = glyph[r]
			}
			px := byte('W')
			if ch == '?' {
				px = 'M'
			}
			b := []byte(line)
			for i := range b {
				if b[i] == '1' {
					b[i] = px
				}
			}
			rows[r] += string(b) + "."
		}
	}
	Pixels(gr, x, y, rows)
}

// BigWord draws a word in pixel letters scaled up, in one pixel colour (a pix letter).
func BigWord(gr *g.Grid, x, y int, word string, px byte, scale int) {
	rows := make([]string, 8*scale)
	for _, ch := range word {
		glyph, ok := big[ch]
		if !ok {
			glyph = big['?']
		}
		for r := 0; r < 8; r++ {
			line := "....."
			if r < 7 {
				line = glyph[r]
			}
			var b []byte
			for _, c := range []byte(line) {
				p := byte('.')
				if c == '1' {
					p = px
				}
				for k := 0; k < scale; k++ {
					b = append(b, p)
				}
			}
			for k := 0; k < scale; k++ {
				b = append(b, '.')
			}
			for k := 0; k < scale; k++ {
				rows[r*scale+k] += string(b)
			}
		}
	}
	Pixels(gr, x, y, rows)
}

// bigDigits are 3×5 pixel digits: '#' is a pixel.
var bigDigits = [10][5]string{
	{"###", "# #", "# #", "# #", "###"},
	{" # ", "## ", " # ", " # ", "###"},
	{"###", "  #", "###", "#  ", "###"},
	{"###", "  #", " ##", "  #", "###"},
	{"# #", "# #", "###", "  #", "  #"},
	{"###", "#  ", "###", "  #", "###"},
	{"###", "#  ", "###", "# #", "###"},
	{"###", "  #", " # ", " # ", " # "},
	{"###", "# #", "###", "# #", "###"},
	{"###", "# #", "###", "  #", "###"},
}

// BigDigitW and BigDigitH are one big digit's cells; digits sit BigDigitStep apart.
const (
	BigDigitW    = 6
	BigDigitH    = 5
	BigDigitStep = 8
)

// BigDigits draws the digits of s large, in c: 3×5 pixels each, every pixel a full block two
// cells wide (a typed code, a countdown, the quick pass's temperature).
// Anything but a digit leaves its place blank. It returns the column after the last digit.
func BigDigits(gr *g.Grid, x, y int, s string, c g.Color) int {
	for i, ch := range s {
		dx := x + i*BigDigitStep
		if ch < '0' || ch > '9' {
			continue
		}
		for row, line := range bigDigits[ch-'0'] {
			for col, p := range line {
				if p == '#' {
					gr.SetBG(dx+2*col, y+row, '█', c, g.K)
					gr.SetBG(dx+2*col+1, y+row, '█', c, g.K)
				}
			}
		}
	}
	return x + len([]rune(s))*BigDigitStep - 2
}

// Confetti falls over Boot's corner (x, y; 18 wide, 10 high) for the first seconds of the end screen.
func Confetti(gr *g.Grid, x, y int, t float64) {
	marks := []rune("*+·o")
	cols := []g.Color{g.BVIO, g.BCYN, g.YEL, g.BGRN, g.WHT}
	for n := 0; n < 26; n++ {
		fx := Hash(n, 1, 0)
		sp := 3 + Hash(n, 2, 0)*5
		yy := y + int(math.Mod(t*sp+Hash(n, 3, 0)*12, 11))
		xx := x + int(math.Floor(fx*18+math.Sin(t*3+float64(n))*1.5))
		if yy < y+10 && xx >= x {
			gr.Set(xx, yy, marks[n%4], cols[n%5])
		}
	}
}
