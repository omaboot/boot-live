package ui

import (
	"fmt"
	"math"
	"strings"

	g "github.com/omaboot/boot-live/internal/grid"
	"github.com/omaboot/boot-live/internal/i18n"
	"github.com/omaboot/boot-live/internal/icons"
	"github.com/omaboot/boot-live/internal/network"
	"github.com/omaboot/boot-live/internal/pace"
	"github.com/omaboot/boot-live/internal/safety"
)

// ── Band animations for the screens that had none (owner-approved prototype v54, 2026-10-17) ──

// bandLeft is how wide the band's text stays on a screen with an animation on its right: the
// headline's note wraps and Boot's box keeps to it.
const bandLeft = 46

// bandRight is the band's part right of bandLeft text columns: from ax, aw wide.
func bandRight(l Layout, lw int) (ax, aw int) {
	ax = l.FieldX + lw + 3
	return ax, l.FieldX + l.FieldW - ax - 1
}

// shownBytes is a size as the Finder gives it: decimal units, "121 GB".
func shownBytes(n int64) string {
	switch {
	case n >= 1e12:
		return fmt.Sprintf("%.1f TB", float64(n)/1e12)
	case n >= 1e9:
		return fmt.Sprintf("%d GB", int64(math.Round(float64(n)/1e9)))
	case n >= 1e6:
		return fmt.Sprintf("%d MB", int64(math.Round(float64(n)/1e6)))
	case n >= 1e3:
		return fmt.Sprintf("%d KB", int64(math.Round(float64(n)/1e3)))
	}
	return fmt.Sprintf("%d B", n)
}

// writtenCache keeps Facts.Written's answer for a second: Hello's dot ticks once a second, and
// so does what it reads.
var writtenCache struct {
	f   *Facts
	sec int
	v   int64
}

func written(f *Facts, t float64) int64 {
	if f.Written == nil {
		return -1
	}
	if c := &writtenCache; c.f != f || c.sec != int(t) {
		c.f, c.sec, c.v = f, int(t), f.Written()
	}
	return writtenCache.v
}

// ── Hello: the disk locks ─────────────────────────────────────────────────────

// diskBand draws the first internal disk as its partitions (by size, each at least its name
// wide) at x, y, w wide, 4 rows. A scan runs over it until at (when the disks went read-only:
// Hello's first setup line), then a violet lock sweeps across; from then on a sheen crosses now
// and then, and the line under it says what has been written to the internal disks since, its
// dot ticking once a second.
func diskBand(gr *g.Grid, x, y, w int, t, at float64, f *Facts) {
	d := f.Disk
	bw := min(w, 66)
	inner := bw - 2
	if d == nil || inner < 12 {
		return
	}
	// the head line: the disk, its model and size
	hx := gr.Put(x, y, icons.Disk, g.BCYN) + 1
	hx = gr.Put(hx, y, d.Name, g.WHT)
	var more []string
	if d.Model != "" {
		more = append(more, d.Model)
	}
	if d.Bytes > 0 {
		more = append(more, shownBytes(d.Bytes))
	}
	more = append(more, i18n.T("internal"))
	if f.MoreDisks > 0 {
		more = append(more, i18n.N("and %d more disk", "and %d more disks", f.MoreDisks, f.MoreDisks))
	}
	gr.Put(hx, y, cut("  ·  "+strings.Join(more, "  ·  "), x+w-hx), g.GRY)
	// the partitions: widths by size, the biggest giving back what the small ones took; a disk
	// with none is one partition, its own name
	parts := d.Parts
	if len(parts) == 0 {
		parts = []safety.Part{{Name: d.Name, Bytes: d.Bytes}}
	}
	if len(parts) > inner/6 {
		parts = parts[:inner/6]
	}
	room := inner - (len(parts) - 1)
	var total int64
	for _, p := range parts {
		total += p.Bytes
	}
	names := make([]string, len(parts))
	ws := make([]int, len(parts))
	sum, big := 0, 0
	for i, p := range parts {
		names[i] = p.Label
		switch {
		case names[i] == "":
			names[i] = p.Name
		case strings.Contains(names[i], "EFI"): // "EFI System Partition"
			names[i] = "EFI"
		}
		names[i] = cut(names[i], max(1, min(14, room/len(parts)-2)))
		share := room / len(parts)
		if total > 0 {
			share = int(math.Round(float64(p.Bytes) / float64(total) * float64(room)))
		}
		ws[i] = max(width(names[i])+2, share)
		sum += ws[i]
		if ws[i] > ws[big] {
			big = i
		}
	}
	ws[big] -= sum - room
	if ws[big] < width(names[big])+2 {
		return // too narrow to draw: leave the band to the wordmark
	}
	k := max(0, min(1, (t-at)/0.7))
	if f.DryRun {
		k = 0 // a fixture: nothing was set read-only, so nothing locks
	}
	locked := k >= 1
	edge := int(math.Round(k * float64(inner)))
	scan := math.Mod(t*40, float64(inner+20)) - 10
	sheen := math.Mod(t-at-0.7, 4)*26 - 6
	fc := g.DIM
	if locked {
		fc = g.VIO
	}
	var top, bot strings.Builder
	top.WriteString("┌")
	bot.WriteString("└")
	for i, pw := range ws {
		top.WriteString(strings.Repeat("─", pw))
		bot.WriteString(strings.Repeat("─", pw))
		if i < len(ws)-1 {
			top.WriteString("┬")
			bot.WriteString("┴")
		}
	}
	top.WriteString("┐")
	bot.WriteString("┘")
	by := y + 1
	gr.Put(x, by, top.String(), fc)
	gr.Put(x, by+2, bot.String(), fc)
	gr.Put(x, by+1, "│", fc)
	p0 := 0
	for i, pw := range ws {
		for c := 0; c < pw; c++ {
			col := p0 + c
			on := col < edge
			r, fg, bg := ' ', g.GRY, g.K
			if on {
				bg = g.VIO
			}
			switch {
			case !on && t < at && math.Abs(float64(col)-scan) < 1.5:
				r, fg = '░', g.BBLU
			case on && !locked && col == edge-1:
				r, fg = '▌', g.BVIO
			case locked && math.Abs(float64(col)-sheen) < 1:
				r, fg = '▒', g.BVIO
			}
			gr.SetBG(x+1+col, by+1, r, fg, bg)
		}
		lx := p0 + (pw-width(names[i]))/2
		if lx < edge {
			gr.PutBG(x+1+lx, by+1, names[i], g.WHT, g.VIO)
		} else {
			gr.Put(x+1+lx, by+1, names[i], g.GRY)
		}
		gr.Put(x+1+p0+pw, by+1, "│", fc)
		p0 += pw + 1
	}
	// what it says under it
	sy := by + 3
	switch {
	case t < at:
		gr.Put(x, sy, spinner(t), g.BVIO)
		gr.Put(x+2, sy, cut(i18n.T("looking at the internal disks…"), w-2), g.DIM)
	case f.DryRun:
		gr.Put(x, sy, cut(i18n.T("dry run: left as they are"), w), g.GRY)
	case !locked:
		gr.Put(x, sy, spinner(t), g.BVIO)
		gr.Put(x+2, sy, cut(i18n.T("setting them read-only…"), w-2), g.GRY)
	default:
		sx := gr.Put(x, sy, icons.Lock, g.BVIO) + 1
		sx = gr.Put(sx, sy, i18n.T("read-only"), g.WHT)
		if n := written(f, t); n >= 0 {
			sx = gr.Put(sx, sy, "  ·  ", g.DIM)
			sx = gr.Put(sx, sy, i18n.T("%s written", shownBytes(n)), g.WHT)
			dot := "·"
			if blinkOn(t) {
				dot = "•"
			}
			gr.Put(sx+2, sy, dot, g.BVIO)
		}
	}
}

// helloDisksAt is when Hello's first setup line (the disks set read-only) comes up.
const helloDisksAt = 1.4

// ── Network: the way out ───────────────────────────────────────────────────────

// pathView is what netPath draws: the network the Mac goes through and how far along it is.
type pathView struct {
	scan        bool      // Wi-Fi is scanning: rings out of the Mac, the networks where their signal puts them
	nets        []WifiNet // the networks found
	listAt      float64   // when the list arrived (they show from here, one by one)
	cur         int       // the list's cursor (its tag lights), -1: none
	fade        float64   // seconds since the scan gave way (its tags go), 0: it hasn't
	tags        []pathTag // lines over the path: a phone waiting, a connection turned off
	via, viaSub string    // the network's node and what's under it ("": none yet)
	link        string    // "wifi", "cable", "phone": how the first link is drawn
	lk          float64   // 0…1: how much of the first link is drawn
	talk        string    // what's going on on the first link (packets go back and forth under it)
	talkAt      float64
	up          float64 // seconds since online, < 0: not online
	mac         string  // under this Mac: its address
	site        string  // the site's name
	reached     int     // the site: 0 not tried yet, 1 checking, 2 reached, 3 not reached
	pkt         string  // a packet carried all the way (Finish: the report), "": none
	pktF        float64 // 0…1 how far
}

type pathTag struct {
	text string
	col  g.Color
}

// netPath draws the way out at x, y, w×h: this Mac, the network it goes through (a Wi-Fi network,
// the cable's router, a phone, a gateway), then the site, as nodes on a line, and what's going on
// along it. Online, both links light and, once the site was reached, a packet goes there and back
// every 2 s.
func netPath(gr *g.Grid, x, y, w, h int, t float64, p pathView) {
	const macW = 12
	site := p.site
	siteW := width(site) + 4
	if w < macW+siteW+24 || h < 5 {
		return
	}
	ny := y + h - 4
	sx := x + w - siteW
	viaW := max(12, width(p.via)+4)
	vx := x + macW + (sx-x-macW-viaW)/2
	l1a, l1b, l2a, l2b := x+macW, vx, vx+viaW, sx
	cx, cy := x+macW, ny+1
	inVia := func(px int) bool { return p.via != "" && px >= vx && px < vx+viaW }
	dot := func(px int) {
		if !inVia(px) {
			gr.Put(px, cy, "■", g.BVIO)
		}
	}
	// the scan: rings out of the Mac's right side, then the networks by signal (4 bars nearest)
	if p.scan && p.fade < 0.6 {
		maxR := float64(w - macW + 6)
		const sp = 30.0
		for j := y; j <= cy; j++ {
			for i := cx + 1; i < x+w; i++ {
				d := math.Hypot(float64(i-cx), float64(j-cy)*2.1)
				for r := 0; r < 3; r++ {
					R := math.Mod(t*sp+float64(r)*maxR/3, maxR)
					if math.Abs(d-R) < 0.5 && (p.fade == 0 || hash(i, j) > p.fade/0.6) {
						if R < maxR/3 {
							gr.Set(i, j, '•', g.BBLU)
						} else {
							gr.Set(i, j, '·', g.DIM)
						}
					}
				}
			}
		}
		far, rows := w-macW-28, max(1, ny-y)
		order := []int{0, 2, 4, 1, 5, 3}
		for i, n := range p.nets {
			if i >= len(order) || t < p.listAt+float64(i)*0.1 {
				break
			}
			bars := max(1, min(4, n.Bars))
			tx := cx + 3 + int(math.Round(float64(4-bars)/3*float64(far))) + (i%2)*4
			ty := y + order[i]%rows
			if d := math.Hypot(float64(tx-cx), float64(ty-cy)*2.1); (t-p.listAt)*sp+20 < d && p.fade == 0 {
				continue
			}
			here, gone := i == p.cur, p.fade > 0
			col := g.GRY
			switch {
			case gone:
				col = g.DIM
			case here:
				col = g.WHT
			}
			if here && !gone {
				gr.Put(tx, ty, "▶", g.BVIO)
			}
			for k := 0; k < 4; k++ {
				if k < bars && !gone {
					gr.Put(tx+2+k, ty, "■", g.CYN)
				} else {
					gr.Put(tx+2+k, ty, "·", g.DIM)
				}
			}
			gr.Put(tx+7, ty, cut(n.SSID, x+w-tx-7), col)
		}
	}
	for i, tg := range p.tags {
		gr.Put(cx+3, y+i*2, cut(tg.text, x+w-cx-3), tg.col)
	}
	// the links
	up := p.up >= 0
	lit := 0.0
	if up {
		lit = min(1, p.up/0.5)
	}
	lr, gaps := '─', p.link != "cable" // Wi-Fi and a phone dashed, a cable solid
	if p.link == "cable" {
		lr = '═'
	}
	lc := g.GRY
	if up {
		lc = g.VIO
	}
	for i := l1a; i < l1b; i++ {
		k := float64(i-l1a) / float64(max(1, l1b-l1a-1))
		switch {
		case p.via != "" && k <= p.lk:
			if !gaps || (i-l1a)%2 == 0 {
				gr.Set(i, cy, lr, lc)
			}
		case (i-l1a)%2 == 1:
			gr.Set(i, cy, '·', g.DIM)
		}
	}
	for i := l2a; i < l2b; i++ {
		k := float64(i-l2a) / float64(max(1, l2b-l2a-1))
		switch {
		case up && k <= lit:
			gr.Set(i, cy, '─', g.VIO)
		case (i-l2a)%2 == 1:
			gr.Set(i, cy, '·', g.DIM)
		}
	}
	// the nodes
	node := func(nx, nw int, label string, col, tc g.Color) {
		box(gr, nx, ny, nw, 3, col, "", col)
		gr.Put(nx+(nw-width(label))/2, ny+1, cut(label, nw-2), tc)
	}
	if up {
		node(x, macW, i18n.T("this Mac"), g.VIO, g.WHT)
	} else {
		node(x, macW, i18n.T("this Mac"), g.GRY, g.GRY)
	}
	switch {
	case p.via != "" && up:
		node(vx, viaW, p.via, g.VIO, g.WHT)
	case p.via != "":
		node(vx, viaW, p.via, g.GRY, g.GRY)
	default:
		s := i18n.T("a network")
		gr.Put(vx+(viaW-width(s))/2, cy, s, g.DIM)
	}
	reached := up && lit >= 1 && p.reached == 2
	if reached {
		node(sx, siteW, site, g.VIO, g.WHT)
	} else {
		node(sx, siteW, site, g.DIM, g.DIM)
	}
	under := func(nx, nw int, s string, col g.Color) {
		if s != "" {
			gr.Put(nx+(nw-width(s))/2, ny+3, cut(s, nw+4), col)
		}
	}
	under(x, macW, p.mac, g.DIM)
	under(vx, viaW, p.viaSub, g.DIM)
	// what's going on on the first link: packets back and forth under its label
	if p.talk != "" {
		n := l1b - l1a - 2
		ph := math.Mod((t-p.talkAt)*1.4, 2)
		f := ph
		if ph > 1 {
			f = 2 - ph
		}
		gr.Put(l1a+max(1, (l1b-l1a-width(p.talk))/2), cy-2, cut(p.talk, w-macW), g.GRY) // over the nodes: it may be longer than the link
		gr.Put(l1a+1+int(math.Round(f*float64(n))), cy, "■", g.BVIO)
	}
	if p.pkt != "" { // a packet carried all the way: the report
		a, b := l1a+1, sx-width(p.pkt)-1
		px := a + int(math.Round(max(0, min(1, p.pktF))*float64(b-a)))
		if p.pktF < 1 {
			gr.Put(px, cy-2, p.pkt, g.WHT)
			dot(min(px+2, sx-1))
		} else {
			box(gr, sx, ny, siteW, 3, g.BVIO, "", g.BVIO)
			gr.Put(sx+(siteW-width(site))/2, ny+1, site, g.WHT)
			under(sx, siteW, i18n.T("received"), g.WHT)
		}
		return
	}
	switch {
	case reached: // a packet there and back every 2 s
		ph := math.Mod(p.up-0.5, 2) / 2
		a, b := l1a+1, sx-1
		switch {
		case ph < 0.4:
			dot(a + int(math.Round(ph/0.4*float64(b-a))))
		case ph < 0.8:
			dot(a + int(math.Round((1-(ph-0.4)/0.4)*float64(b-a))))
		}
		if ph >= 0.38 && ph < 0.5 {
			box(gr, sx, ny, siteW, 3, g.WHT, "", g.WHT) // it lands
		}
		under(sx, siteW, i18n.T("reached"), g.DIM)
	case up && p.reached == 1:
		under(sx, siteW, i18n.T("checking…"), g.DIM)
	case up && p.reached == 3:
		under(sx, siteW, i18n.T("not reached"), g.DIM)
	}
}

// hash is a cell's fixed noise in 0…1 (the scan's tags fading out cell by cell).
func hash(x, y int) float64 {
	h := uint32(x*374761393 + y*668265263)
	h = (h ^ (h >> 13)) * 1274126177
	return float64(h^(h>>16)) / 4294967296
}

// netPathOf is the way out as the Network screen's state has it.
func netPathOf(st *State, f *Facts) pathView {
	t, n := st.T, st.Net
	p := pathView{cur: -1, up: -1, site: f.Site, nets: n.Networks, listAt: n.ListAt}
	busy := st.Pw || netBusy(n)
	if !busy && st.LineSel < 0 && st.NetSel < len(n.Networks) {
		p.cur = st.NetSel
	}
	switch {
	case n.Checking:
		p.reached = 1
	case n.RefreshFailed:
		p.reached = 3
	case n.Refreshed != "":
		p.reached = 2
	}
	phone := PhoneName(n)
	via := func(v string) {
		switch v {
		case "Wi-Fi":
			p.via, p.viaSub, p.link = n.SSID, i18n.T("Wi-Fi"), "wifi"
		case "Phone":
			p.via, p.viaSub, p.link = phone, "USB", "phone"
		default:
			p.via, p.viaSub, p.link = i18n.T("router"), i18n.T("Ethernet"), "cable"
		}
		if n.Static {
			p.via, p.viaSub = i18n.T("gateway"), n.Gateway
		}
		p.lk = 1
	}
	// lines over the path: a phone on its way, one that's ready, a connection turned off
	switch {
	case n.TetLink == LinkLimit:
		p.tags = append(p.tags, pathTag{icons.Phone + " ▲ " + i18n.T("an LTE modem: it needs dialing, which OmaBoot? can't do yet"), g.YEL})
	case n.TetLink == LinkCarrier && n.PhoneNeed == network.NeedTrust:
		p.tags = append(p.tags, pathTag{icons.Phone + " " + i18n.T("%s  ·  waiting for Trust and its passcode", phone) + " " + spinner(t), g.GRY})
	case n.TetLink == LinkCarrier && n.PhoneNeed == network.NeedTethering:
		p.tags = append(p.tags, pathTag{icons.Phone + " " + i18n.T("%s  ·  waiting for USB tethering", phone) + " " + spinner(t), g.GRY})
	case n.TetLink == LinkCarrier && n.PhoneNeed == network.NeedModeswitch:
		p.tags = append(p.tags, pathTag{icons.Phone + " " + i18n.T("an LTE stick  ·  switching it to network mode") + " " + spinner(t), g.GRY})
	case n.TetLink == LinkReady && n.Online:
		p.tags = append(p.tags, pathTag{icons.Phone + " " + i18n.T("%s ready  ·  its line switches to it", phone), g.WHT})
	}
	if n.Parked != "" {
		p.tags = append(p.tags, pathTag{i18n.T("%s: off while another carries the run", connName(n, n.Parked)), g.DIM})
	}
	switch {
	case netUpView(st) && n.Phase != NetSwitching:
		via(n.Via)
		p.up, p.mac = t-n.At, n.IP
	case n.Phase == NetSwitching:
		via(n.To)
		p.lk = max(0, min(1, (t-n.At)/0.4))
		p.talk, p.talkAt = i18n.T("DHCP: asking for an address"), n.At
	case netJoining(n) && n.SSID != "":
		via("Wi-Fi")
		p.viaSub = icons.Lock + " " + i18n.T("Wi-Fi")
		p.talk, p.talkAt = i18n.T("joining"), n.At
		if n.Phase == NetDHCP {
			p.talk = i18n.T("DHCP: asking for an address")
		}
	case n.EthLink == LinkCarrier:
		via("Ethernet")
		p.talk, p.talkAt = i18n.T("DHCP: asking for an address"), n.At
	case n.TetLink == LinkCarrier && n.PhoneNeed == network.NeedNothing:
		via("Phone")
		p.talk, p.talkAt = i18n.T("DHCP: asking for an address"), n.At
	case st.Pw:
		via("Wi-Fi")
		p.via, p.viaSub, p.lk = n.SSID, icons.Lock+" "+i18n.T("password"), 0
	}
	if p.via == "" && n.CanScan && !n.Online {
		p.scan = true
	}
	if p.via != "" && n.CanScan && !n.Online {
		p.scan, p.fade = true, max(0.01, t-n.At) // the scan's tags fade as the network takes the middle
	}
	return p
}

// netBand draws the Network screens' band right of Boot (prototype v54): the headline and its
// note wrapped to the left part, Boot's lines in his box under them, the way out on the right.
// Without room for the way out, it's the band as before. It returns the y of Boot's box.
func netBand(gr *g.Grid, l Layout, head, note string, p pathView, t float64) (sayY, sayW int) {
	ax, aw := bandRight(l, bandLeft)
	if aw < 60 {
		gr.Put(l.FieldX, l.BandTop+2, head, g.WHT)
		gr.Put(l.FieldX, l.BandTop+3, note, g.DIM)
		return l.BandTop + 5, l.FieldW - 1
	}
	gr.Put(l.FieldX, l.BandTop+1, head, g.WHT)
	ns := wrap(note, bandLeft)
	for i, s := range ns {
		gr.Put(l.FieldX, l.BandTop+2+i, s, g.DIM)
	}
	netPath(gr, ax, l.BandTop, aw, l.BandH-1, t, p)
	return l.BandTop + 3 + len(ns), bandLeft
}

// ── Quick results and the report: the ring ──────────────────────────────────────

// resultRing draws the run's results as a ring at x, y, w×h: one segment per result in the order
// they ran, in its result's colour (green only for a pass), drawn round once from t0, the pass
// count in the middle; then a darker shade goes round. In half cells, so its pixels are square.
func resultRing(gr *g.Grid, x, y, w, h int, t, t0 float64, rs []pace.Status) {
	n := len(rs)
	if n == 0 || w < 16 || h < 6 {
		return
	}
	R := math.Min(float64(h), float64(w)/2) - 0.5
	r0 := R * 0.64
	sweep := max(0, min(1, (t-t0)/1.2)) * 2 * math.Pi
	orbit := math.Mod((t-t0-1.2)*0.8, 2*math.Pi)
	px := func(i, k int) (g.Color, bool) {
		dx, dy := float64(i)+0.5-float64(w)/2, float64(k)+0.5-float64(h)
		d := math.Hypot(dx, dy)
		if d > R || d < r0 {
			return 0, false
		}
		a := math.Mod(math.Atan2(dx, -dy)+2*math.Pi, 2*math.Pi)
		if a > sweep {
			return 0, false
		}
		fs := a / (2 * math.Pi) * float64(n)
		seg := int(fs)
		if fs-float64(seg) < 0.16 { // a gap between results
			return 0, false
		}
		_, c, _ := mark(rs[min(seg, n-1)])
		if sweep >= 2*math.Pi && math.Abs(math.Mod(a-orbit+3*math.Pi, 2*math.Pi)-math.Pi) < 0.3 {
			c = darker(c)
		}
		return c, true
	}
	for j := 0; j < h; j++ {
		for i := 0; i < w; i++ {
			top, okT := px(i, j*2)
			bot, okB := px(i, j*2+1)
			switch {
			case okT && okB:
				gr.Set(x+i, y+j, '█', top)
			case okT:
				gr.Set(x+i, y+j, '▀', top)
			case okB:
				gr.Set(x+i, y+j, '▄', bot)
			}
		}
	}
	if sweep < 2*math.Pi {
		return
	}
	pass := 0
	for _, r := range rs {
		if r == pace.Pass {
			pass++
		}
	}
	c, s := fmt.Sprintf("%d/%d", pass, n), i18n.T("passed")
	cy := y + h/2
	gr.Put(x+(w-width(c))/2, cy-1, c, g.WHT)
	gr.Put(x+(w-width(s))/2, cy, s, g.DIM)
}

// darker is a result colour's darker shade, for the ring's shade going round.
func darker(c g.Color) g.Color {
	switch c {
	case g.BGRN:
		return g.GRN
	case g.YEL:
		return g.AMB
	case g.BRED:
		return g.RED
	}
	return g.DIM
}

// ringOf is the results the ring shows: every result decided, in the order they ran.
func ringOf(f *Facts) []pace.Status {
	var rs []pace.Status
	for _, c := range f.All() {
		if c.Status == pace.Pass || c.Status == pace.Partial || c.Status == pace.Fail || c.Status == pace.Live {
			rs = append(rs, c.Status)
		}
	}
	return rs
}

// staticPathOf is the way out as the static IP form has it: the address under this Mac and the
// gateway in the middle as they're typed; while Apply sets them, packets go back and forth; once
// it's online, the path lights through to the site.
func staticPathOf(st *State, f *Facts, applying, done bool) pathView {
	t, n, fm := st.T, st.Net, st.Form
	p := pathView{cur: -1, up: -1, site: f.Site, link: "cable", lk: 1}
	if strings.HasPrefix(f.Static.Iface, "wl") {
		p.link = "wifi"
	}
	if f.Static.Iface != "" {
		p.via, p.viaSub = i18n.T("gateway"), string(fm.Fields[1])
		if p.viaSub == "" {
			p.viaSub = "?"
		}
	}
	p.mac = string(fm.Fields[0])
	switch {
	case n.Checking:
		p.reached = 1
	case n.RefreshFailed:
		p.reached = 3
	case n.Refreshed != "":
		p.reached = 2
	}
	switch {
	case done:
		p.up, p.mac, p.viaSub = t-n.At, n.IP, n.Gateway
	case applying:
		p.talk, p.talkAt = i18n.T("setting the address, route and DNS"), n.At
	}
	return p
}
