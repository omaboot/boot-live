package boot

import (
	"math"
	"strconv"

	g "github.com/omaboot/boot-live/internal/grid"
	"github.com/omaboot/boot-live/internal/i18n"
)

// Effect is a band effect, one per check category: textmode-style per-cell maths → a character
// ramp from glyphs omaboot-v has → a palette colour.
type Effect string

const (
	Plasma  Effect = ""        // idle: a slow plasma
	PCI     Effect = "pci"     // a scanning sweep, bus addresses ticking in
	Bars    Effect = "bars"    // colour bars and the panel's mode
	Torus   Effect = "torus"   // a spinning torus for the GPU
	Battery Effect = "battery" // a battery filling to its charge
	Radar   Effect = "radar"   // Wi-Fi rings and blips
	Wave    Effect = "wave"    // an audio waveform
	Heat    Effect = "heat"    // heat shimmer and the rising temperature
	Nap     Effect = "sleep"   // suspend: z's, then a white flash on wake
)

// Params carry each effect's real values.
type Params struct {
	Items      []string // PCI: bus addresses
	Label      string   // Bars: the mode, e.g. "1440 × 900"
	Level      float64  // Battery: charge, 0–1
	From, Peak float64  // Heat: °C at the start and at the peak
	Now        float64  // Heat: °C now, while the load runs (0: no reading yet)
	Count      int      // Radar: networks found
}

// Ramps, all from omaboot-v's glyphs.
var (
	Ramp  = []rune(" .:-=+*#%@")
	Shade = []rune(" ░▒▓█")
	Lum   = []rune(".,-~:;=!*#$@")
)

func clamp(v, a, b int) int { return max(a, min(b, v)) }

// Noise is smooth value noise in [0, 1).
func Noise(x, y float64, s int) float64 {
	xi, yi := math.Floor(x), math.Floor(y)
	xf, yf := x-xi, y-yi
	u, v := xf*xf*(3-2*xf), yf*yf*(3-2*yf)
	ix, iy := int(xi), int(yi)
	a, b := Hash(ix, iy, s), Hash(ix+1, iy, s)
	c, d := Hash(ix, iy+1, s), Hash(ix+1, iy+1, s)
	return a + (b-a)*u + (c-a)*v + (a-b-c+d)*u*v
}

// Draw paints one frame of an effect into the w×h band at x0, y0, t seconds into it.
func (e Effect) Draw(gr *g.Grid, p Params, x0, y0, w, h int, t float64) {
	gr.Fill(x0, y0, w, h, ' ', g.GRY, g.K)
	fw, fh := float64(w), float64(h)
	switch e {
	case PCI:
		sx := math.Mod(t*fw*0.55, fw+12) - 6
		for j := 0; j < h; j++ {
			for i := 0; i < w; i++ {
				d := math.Abs(float64(i) - sx)
				v := math.Exp(-d/3.2) + 0.12*Noise(float64(i)*0.4, float64(j)*0.9+t, 2)
				if k := clamp(int(v*9), 0, 9); k > 0 {
					c := g.DIM
					if d < 1.5 {
						c = g.BVIO
					} else if k > 4 {
						c = g.VIO
					}
					gr.Set(x0+i, y0+j, Ramp[k], c)
				}
			}
		}
		n := min(len(p.Items), int(t*3.2))
		for a := 0; a < n && a < h; a++ {
			c := g.DIM
			if a == n-1 {
				c = g.WHT
			}
			gr.Put(x0+1, y0+a%h, p.Items[a], c)
		}
	case Bars:
		cols := []g.Color{g.GRY, g.AMB, g.CYN, g.GRN, g.VIO, g.RED, g.BLU}
		bw := max(1, w/len(cols))
		scan := int(math.Mod(t*6, fh))
		for j := 0; j < h; j++ {
			for i := 0; i < w; i++ {
				c := cols[min(len(cols)-1, i/bw)]
				r := ' '
				if j == scan {
					r = '▒'
				}
				gr.SetBG(x0+i, y0+j, r, g.WHT, c)
			}
		}
		if p.Label != "" {
			l := " " + p.Label + " "
			gr.PutBG(x0+max(0, w/2-6), y0+h/2, l, g.WHT, g.K)
		}
	case Torus:
		a, b := t*1.1, t*0.6
		zb := make([]float64, w*h)
		out := make([]int, w*h)
		for i := range out {
			out[i] = -1
		}
		ca, sa, cb, sb := math.Cos(a), math.Sin(a), math.Cos(b), math.Sin(b)
		const k2 = 5.0
		k1 := fw * k2 * 0.30 / 3
		for th := 0.0; th < 6.283; th += 0.07 {
			ct, st := math.Cos(th), math.Sin(th)
			for ph := 0.0; ph < 6.283; ph += 0.02 {
				cp, sp := math.Cos(ph), math.Sin(ph)
				cx, cy := 2+ct, st
				x := cx*(cb*cp+sa*sb*sp) - cy*ca*sb
				y := cx*(sb*cp-sa*cb*sp) + cy*ca*cb
				z := k2 + ca*cx*sp + cy*sa
				oz := 1 / z
				xp := int(math.Floor(fw/2 + k1*oz*x))
				yp := int(math.Floor(fh/2 - k1*oz*y*0.5))
				l := cp*ct*sb - ca*ct*sp - sa*st + cb*(ca*st-ct*sa*sp)
				if xp >= 0 && xp < w && yp >= 0 && yp < h && oz > zb[yp*w+xp] {
					zb[yp*w+xp] = oz
					out[yp*w+xp] = clamp(int(math.Floor(l*8)), 0, 11)
				}
			}
		}
		for j := 0; j < h; j++ {
			for i := 0; i < w; i++ {
				l := out[j*w+i]
				if l < 0 {
					continue
				}
				c := g.DIM
				switch {
				case l > 8:
					c = g.WHT
				case l > 5:
					c = g.BVIO
				case l > 2:
					c = g.VIO
				}
				gr.Set(x0+i, y0+j, Lum[l], c)
			}
		}
	case Battery:
		bw, bh := min(w-6, 34), min(h-2, 5)
		bx, by := x0+(w-bw)/2, y0+(h-bh)/2
		gr.Put(bx, by, "┌", g.GRY)
		gr.Put(bx+bw-1, by, "┐", g.GRY)
		gr.Put(bx, by+bh-1, "└", g.GRY)
		gr.Put(bx+bw-1, by+bh-1, "┘", g.GRY)
		for i := 1; i < bw-1; i++ {
			gr.Set(bx+i, by, '─', g.GRY)
			gr.Set(bx+i, by+bh-1, '─', g.GRY)
		}
		for j := 1; j < bh-1; j++ {
			gr.Set(bx, by+j, '│', g.GRY)
			gr.Set(bx+bw-1, by+j, '│', g.GRY)
		}
		gr.Set(bx+bw, by+bh/2, '█', g.GRY)
		lvl := math.Min(p.Level, t/1.6)
		n := int(math.Round(float64(bw-2) * lvl))
		for j := 1; j < bh-1; j++ {
			for i := 0; i < n; i++ {
				r := '█'
				if i == n-1 {
					r = '▓'
				}
				gr.Set(bx+1+i, by+j, r, g.CYN)
			}
		}
		if p.Level > 0 { // 0: still running, no charge read yet
			gr.Put(bx+bw/2-2, by+bh, " "+i18n.T("%d%%", int(math.Round(lvl*100)))+" ", g.WHT)
		}
	case Radar:
		cx, cy := fw/2, fh/2
		for j := 0; j < h; j++ {
			for i := 0; i < w; i++ {
				d := math.Hypot((float64(i)-cx)*0.5, float64(j)-cy)
				v := math.Cos(d*1.2-t*5) * math.Exp(-d/14)
				if k := clamp(int(math.Floor((v+0.1)*5)), 0, 4); k > 0 {
					c := g.DIM
					if k > 2 {
						c = g.BLU
					}
					gr.Set(x0+i, y0+j, Shade[k], c)
				}
			}
		}
		blips := [][2]float64{{0.2, 0.3}, {0.75, 0.25}, {0.6, 0.8}, {0.15, 0.75}, {0.85, 0.6}, {0.4, 0.15}}
		for n, bp := range blips {
			if n < p.Count && t > 0.4+float64(n)*0.35 {
				gr.Put(x0+int(bp[0]*fw), y0+int(bp[1]*fh), "■", g.BBLU)
			}
		}
	case Wave:
		mid := fh / 2
		f0 := 0.25 + t*0.35
		for i := 0; i < w; i++ {
			env := math.Sin(math.Pi * float64(i) / fw)
			a := math.Sin(float64(i)*f0-t*9) * env * (fh/2 - 0.6) * (0.35 + 0.65*math.Min(1, t/1.2))
			y := int(math.Round(mid + a))
			m := int(math.Round(mid))
			for j := min(y, m); j <= max(y, m); j++ {
				if j == y {
					gr.Set(x0+i, y0+clamp(j, 0, h-1), '█', g.BCYN)
				} else {
					gr.Set(x0+i, y0+clamp(j, 0, h-1), '░', g.CYN)
				}
			}
		}
	case Heat:
		// the fire fills the band's whole height; the temperature sits in big digits in the middle
		// of it, on a dark plate
		for j := 0; j < h; j++ {
			for i := 0; i < w; i++ {
				v := Noise(float64(i)*0.18, float64(j)*0.35+t*2.4, 5) * (0.45 + 0.75*float64(j+1)/fh) * 1.35 * math.Min(1, 0.25+t/1.5)
				if k := clamp(int(v*5), 0, 4); k > 0 {
					c := g.DIM
					switch {
					case k > 3:
						c = g.YEL
					case k > 2:
						c = g.AMB
					}
					gr.Set(x0+i, y0+j, Shade[k], c)
				}
			}
		}
		var temp int
		switch {
		case p.Peak > 0: // the result: from the start to the peak
			temp = int(math.Round(p.From + math.Min(1, t/2.4)*(p.Peak-p.From)))
		case p.Now > 0: // still running: the reading now, once a second, so it's seen to rise
			temp = int(math.Round(p.Now))
		default:
			return // no reading yet
		}
		ds := strconv.Itoa(temp)
		dw := len(ds)*BigDigitStep + 3
		tx, ty := x0+(w-dw)/2, y0+(h-BigDigitH)/2
		gr.Fill(tx-1, ty-1, dw+1, BigDigitH+2, ' ', g.GRY, g.K) // the plate, so the digits read over the flames
		BigDigits(gr, tx, ty, ds, g.WHT)
		gr.Put(tx+len(ds)*BigDigitStep, ty, i18n.T("°C"), g.WHT)
	case Nap:
		if t > 0.6 && t < 3.0 {
			for n := 0; n < 4; n++ {
				ph := math.Mod(t*0.7+float64(n)*0.25, 1)
				z := "Z"
				if n%2 == 1 {
					z = "z"
				}
				gr.Put(x0+int(fw*0.3+float64(n)*5+ph*6), y0+int(fh-1-ph*(fh-1)), z, g.DIM)
			}
			gr.Put(x0+2, y0+h-1, i18n.T("suspended"), g.DIM)
		} else if t >= 3.0 && t < 3.25 {
			gr.Fill(x0, y0, w, h, '░', g.WHT, g.K)
		}
	default:
		for j := 0; j < h; j++ {
			for i := 0; i < w; i++ {
				if v := 0.5 + 0.5*math.Sin(float64(i)*0.12+t*0.8)*math.Sin(float64(j)*0.4-t*0.5); v > 0.78 {
					gr.Set(x0+i, y0+j, '░', g.DIM)
				}
			}
		}
	}
}
