package handson

import (
	"hash/fnv"
	"math"
	"strconv"

	g "github.com/omaboot/boot-live/internal/grid"
	"github.com/omaboot/boot-live/internal/i18n"
)

// btRadar is the Bluetooth test's band picture: a radar with the Mac in the middle and a blip
// per device heard, nearer the middle the stronger its signal, at a bearing of its own (made up:
// Bluetooth gives no direction). Named devices are violet squares, unnamed ones grey dots; the
// ones on the list on screen carry their number, so the person can match them. A sweep turns
// while it scans; beside it, how many it has heard.
func btRadar(ds []BTDevice, page []string, scanning bool) Panel {
	num := map[string]int{}
	for i, k := range page {
		num[k] = i + 1
	}
	named := btNamed(ds)
	return func(gr *g.Grid, x, y, w, h int, t float64) {
		ry := float64(h-1) / 2 // the radar's radius in rows
		rx := ry * 2           // and in columns (cells are twice as tall as wide)
		cx, cy := float64(x)+rx+1, float64(y)+ry
		sweep := math.Mod(t*1.6, 2*math.Pi)
		for j := 0; j < h; j++ {
			for i := 0; i <= int(2*rx)+1; i++ {
				px, py := float64(x+i), float64(y+j)
				dx, dy := (px-cx)/rx, (py-cy)/ry
				d := math.Hypot(dx, dy)
				if d > 1.04 {
					continue
				}
				if scanning && d > 0.08 && angDiff(math.Atan2(dy, dx), sweep) < 0.22 {
					gr.Put(x+i, y+j, "░", g.VIO)
				}
			}
		}
		for _, r := range []float64{1, 0.5} { // the rings, unbroken
			for a := 0.0; a < 2*math.Pi; a += 0.01 {
				gr.Put(int(math.Round(cx+r*rx*math.Cos(a))), int(math.Round(cy+r*ry*math.Sin(a))), "·", g.BLU)
			}
		}
		gr.Put(int(cx), int(cy), "■", g.BCYN) // the Mac
		for _, d := range ds {
			// −35 dBm or stronger at the middle's edge, −95 dBm at the rim
			r := 0.15 + 0.85*math.Max(0, math.Min(1, (float64(-d.RSSI)-35)/60))
			a := btBearing(d.Key)
			bx, by := int(math.Round(cx+r*rx*math.Cos(a))), int(math.Round(cy+r*ry*math.Sin(a)))
			switch n, ok := num[d.Key]; {
			case ok:
				gr.Put(bx, by, strconv.Itoa(n), g.WHT)
			case btClean(d.Name) != "":
				gr.Put(bx, by, "■", g.BVIO)
			default:
				gr.Put(bx, by, "•", g.GRY)
			}
		}
		// beside it: how many, named and not
		tx := x + int(2*rx) + 5
		tw := x + w - tx - 1
		if tw < 10 {
			return
		}
		ty := y + max(0, int(ry)-3)
		gr.Put(tx, ty, cut(i18n.N("%d device heard", "%d devices heard", len(ds), len(ds)), tw), g.WHT)
		gr.Put(tx, ty+2, "■", g.BVIO)
		gr.Put(tx+2, ty+2, cut(i18n.T("%d with a name", named), tw-2), g.GRY)
		gr.Put(tx, ty+3, "•", g.GRY)
		gr.Put(tx+2, ty+3, cut(i18n.T("%d without", len(ds)-named), tw-2), g.GRY)
		if len(page) > 0 {
			gr.Put(tx, ty+5, cut(i18n.T("numbers: the list"), tw), g.DIM)
		} else if scanning {
			gr.Put(tx, ty+5, cut(i18n.T("scanning…"), tw), g.DIM)
		}
	}
}

// btBearing is a device's made-up bearing on the radar, steady for its key.
func btBearing(key string) float64 {
	h := fnv.New32a()
	h.Write([]byte(key))
	return float64(h.Sum32()%3600) / 3600 * 2 * math.Pi
}

// angDiff is how far apart two angles are, 0 to π.
func angDiff(a, b float64) float64 {
	d := math.Mod(math.Abs(a-b), 2*math.Pi)
	if d > math.Pi {
		d = 2*math.Pi - d
	}
	return d
}
