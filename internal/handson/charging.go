package handson

import (
	"fmt"
	"path"
	"strings"
	"time"

	"github.com/omaboot/boot-live/internal/boot"
	"github.com/omaboot/boot-live/internal/checks"
	g "github.com/omaboot/boot-live/internal/grid"
	"github.com/omaboot/boot-live/internal/i18n"
)

// power.charging, "AC power and battery charging": the person unplugs the charger and plugs it
// back in; the machine decides from the power supplies (method automatic).
//
//   - supported: the adapter's power supply went offline when unplugged and online when plugged
//     back in, and the battery followed: Discharging while unplugged, then Charging (or Full, or
//     Not charging at 90 % or more: Macs hold a full battery) within 20 s of plugging in;
//   - partial: the adapter followed but the battery's status didn't, or the battery followed but
//     there is no adapter power supply to read;
//   - failed: the tester unplugged and replugged it and nothing changed ("Nothing changed",
//     method observed);
//   - not_tested (no-equipment): skipped, no charger at hand.

func init() {
	Register(Spec{ID: "power.charging", Group: Plug, Order: 10, Title: i18n.M("Charging"),
		Brief: i18n.M("unplug the charger, then plug it back in"), Gear: i18n.M("the charger"), Expect: 40 * time.Second,
		New: func(*Env) Run { return &charging{} }, Demo: func(f *Fake) {
			f.SetAt(3, "/sys/class/power_supply/ADP1/online", "0")
			f.SetAt(3.6, "/sys/class/power_supply/BAT0/status", "Discharging")
			f.SetAt(7, "/sys/class/power_supply/ADP1/online", "1")
			f.SetAt(8.2, "/sys/class/power_supply/BAT0/status", "Charging")
		}})
}

// supplies are the Mac's own adapter and battery directories (never a peripheral's: scope
// Device, or a hid-* name).
func supplies(io IO) (adapter, battery string) {
	for _, d := range io.Glob("/sys/class/power_supply/*") {
		if strings.EqualFold(io.Read(d+"/scope"), "Device") || strings.HasPrefix(path.Base(d), "hid-") {
			continue
		}
		switch t := io.Read(d + "/type"); {
		case t == "Battery" && battery == "":
			battery = d
		case (t == "Mains" || strings.HasPrefix(t, "USB")) && adapter == "":
			adapter = d
		}
	}
	return adapter, battery
}

type charging struct {
	ad, bat   string
	online    int // the adapter's online now (-1: no adapter)
	status    string
	level     int // the charge, % of full (checks.ChargeLevel)
	health    int // what it holds full, % of design (checks.Health; 0: not reported)
	cycles    int
	polled    float64
	sawOff    bool    // the adapter went (or was) offline
	offStatus string  // the battery's status while it was unplugged
	backAt    float64 // when it was plugged back in after being off (-1: not yet)
	trail     []string
	sayAt     float64
}

const chargeWait = 20.0 // seconds for the battery to follow a plug-in

func (ch *charging) Start(c *Ctx) {
	c.Select(-1) // the plug answers; a stray Enter mustn't
	ch.ad, ch.bat = supplies(c.IO)
	ch.backAt = -1
	ch.health, ch.cycles, _ = checks.Health(c.IO.Int, ch.bat)
	ch.read(c)
	ch.sawOff = ch.online == 0 || ch.ad == "" && ch.status == "Discharging"
	if ch.sawOff {
		ch.offStatus = ch.status
	}
	ch.trail = []string{ch.state()}
}

func (ch *charging) read(c *Ctx) {
	ch.online = -1
	if ch.ad != "" {
		if n, ok := c.IO.Int(ch.ad + "/online"); ok {
			ch.online = n
		}
	}
	ch.status = c.IO.Read(ch.bat + "/status")
	ch.level, _ = checks.ChargeLevel(c.IO.Int, ch.bat)
}

// plugged reports whether the charger is in, as best the supplies tell.
func (ch *charging) plugged() bool {
	if ch.online >= 0 {
		return ch.online == 1
	}
	return ch.status != "Discharging"
}

func (ch *charging) state() string {
	s := "battery " + ch.status
	if ch.online >= 0 {
		s = fmt.Sprintf("adapter online %d, %s", ch.online, s)
	}
	return s
}

// following reports whether the battery's status says it's on the charger again.
func (ch *charging) following() bool {
	return ch.status == "Charging" || ch.status == "Full" || ch.status == "Not charging" && ch.level >= 90
}

func (ch *charging) Step(c *Ctx) {
	if c.T-ch.polled < 0.5 {
		return
	}
	ch.polled = c.T
	was := ch.plugged()
	ch.read(c)
	if s := ch.state(); s != ch.trail[len(ch.trail)-1] {
		ch.trail = append(ch.trail, s)
	}
	now := ch.plugged()
	switch {
	case was && !now:
		ch.sawOff, ch.backAt, ch.sayAt = true, -1, c.T
	case !was && now && ch.sawOff:
		ch.backAt, ch.sayAt = c.T, c.T
	}
	if ch.sawOff && !now && ch.status != "" {
		ch.offStatus = ch.status
	}
	if ch.backAt < 0 {
		return
	}
	if ch.following() || c.T-ch.backAt >= chargeWait {
		ch.decide(c)
	}
}

func (ch *charging) decide(c *Ctx) {
	ev := strings.Join(ch.trail, " → ")
	adOK := ch.ad != ""
	batOK := ch.offStatus == "Discharging" && ch.following()
	var r checks.Result
	var say string
	switch {
	case adOK && batOK:
		r = Result("power.charging", checks.Supported, Automatic, ev, i18n.T("%s off and on, battery %s", path.Base(ch.ad), strings.ToLower(ch.status)))
		say = i18n.T("power out, power in. the battery noticed both times.")
	case adOK:
		r = Result("power.charging", checks.Partial, Automatic, ev+"; the battery's status didn't follow", i18n.T("the battery's status didn't follow"))
		say = i18n.T("the adapter noticed, but the battery's status didn't follow.")
	default:
		r = Result("power.charging", checks.Partial, Automatic, ev+"; no adapter power supply to read", i18n.T("no adapter power supply"))
		say = i18n.T("the battery followed, but there's no adapter to read.")
	}
	c.Decide(Outcome{Results: []checks.Result{r}, Say: say})
}

func (ch *charging) Act(c *Ctx, i int) {
	if i != 0 || c.T < 10 {
		return
	}
	c.Decide(Outcome{Results: []checks.Result{Result("power.charging", checks.Failed, Observed,
		"the tester unplugged the charger and plugged it back in; the power supplies didn't change: "+strings.Join(ch.trail, " → "), i18n.T("nothing changed"))},
		Say: i18n.T("unplugged and back, and the power supplies didn't move.")})
}

func (ch *charging) Stop(*Ctx) {}

func (ch *charging) View(c *Ctx) Screen {
	s := Screen{Face: boot.Idle, SayAt: ch.sayAt, OutTitle: i18n.T("What I see"),
		Steps:   []string{i18n.T("Unplug the charger from the Mac."), i18n.T("Plug it back in."), i18n.T("Leave it in while the battery catches up.")},
		Happens: i18n.T("The Mac should notice both: on battery when it's out, charging again when it's back (up to 20 s)."),
		Hint:    i18n.T("pull it out at the Mac's end, not the wall. then back in.")}
	if c.T-ch.sayAt < 20 { // a plug moved lately: no nudge
		s.Hint = ""
	}
	switch {
	case ch.backAt >= 0:
		s.Step = 2
		s.Say, s.Face = i18n.T("plugged back in. watching the battery…"), boot.Work
		s.Waiting = "·  " + i18n.T("waiting for the battery")
	case ch.plugged():
		s.Say, s.SayAt = i18n.T("unplug the charger. I'm watching the battery."), 0.2
		if ch.sawOff {
			s.Say = i18n.T("in again. once more: out, then in.")
		}
		s.Waiting = "·  " + i18n.T("waiting for the charger to come out")
	default:
		s.Step = 1
		s.Say = i18n.T("on battery now. plug it back in.")
		if !ch.sawOff {
			s.Say = i18n.T("no charger in. plug it in, unplug it, plug it in.")
		}
		s.Waiting = "·  " + i18n.T("waiting for the charger to go back in")
	}
	if c.Outcome() != nil {
		s.Step = len(s.Steps)
	}
	if ch.ad != "" {
		w := i18n.T("plugged in")
		if ch.online == 0 {
			w = i18n.T("unplugged")
		}
		s.Live = append(s.Live, Fact(i18n.T("%s (the charger): %s", path.Base(ch.ad), w)))
	} else {
		s.Live = append(s.Live, Gone(i18n.T("no adapter power supply")))
	}
	if ch.bat != "" {
		s.Live = append(s.Live, Fact(i18n.T("%s: %s, %d%%", path.Base(ch.bat), strings.ToLower(ch.status), ch.level)))
		switch { // the battery's health (owner, 0.8.3 review)
		case ch.health > 0 && ch.cycles > 0:
			s.Live = append(s.Live, Fact(i18n.T("health: %d%% of design, %d cycles", ch.health, ch.cycles)))
		case ch.health > 0:
			s.Live = append(s.Live, Fact(i18n.T("health: %d%% of design", ch.health)))
		case ch.cycles > 0:
			s.Live = append(s.Live, Fact(i18n.T("health: %d cycles", ch.cycles)))
		}
	}
	switch {
	case ch.backAt >= 0:
		s.Live = append(s.Live, Wait(i18n.T("waiting for the battery to follow, %d s left", max(0, int(chargeWait-(c.T-ch.backAt))))))
	case ch.sawOff:
		s.Live = append(s.Live, Fact(i18n.T("seen unplugged")))
	}
	in, charging := ch.plugged(), ch.status == "Charging"
	s.Anim = func(gr *g.Grid, x, y, w, h int, t float64) { chargeArt(gr, x, y, w, h, t, in, charging, ch.level) }
	s.Actions = []Action{{Label: i18n.T("Nothing changed"), Does: i18n.T("I unplugged it and plugged it back in"), Off: c.T < 10}}
	return s
}

// chargeArt is the charging test's animation (owner, 0.6.1 review), in square pixels: the
// battery with its charge and, while the charger is in, a bolt in its middle; then the Mac's side
// and a MagSafe-style connector, its strain relief and the cord. Plugged in, the battery, the
// bolt, the connector and the cord are green; pulled out, the connector stands off the Mac, it and
// the cord dim red, and the charge's edge blinks: it's draining.
func chargeArt(gr *g.Grid, x, y, w, h int, t float64, in, charging bool, level int) {
	bh := 16 // the battery, 2:1 like a real one; smaller when the band is
	if w < 64 || h < 11 {
		bh = 12
	}
	bw := 2 * bh
	pw, ph := w-1, 2*(h-2) // the last two rows hold the words
	if pw < bw+30 || ph < bh+2 {
		return
	}
	p := newSndPix(pw, ph)
	by := (ph - bh) / 2
	body, fill, cord := byte('w'), byte('w'), byte('r')
	if in {
		body, fill, cord = 'G', 'g', 'G'
	}
	// the battery: a thin outline, a gap, the charge, and the tip
	for i := 0; i < bw; i++ {
		p.set(i, by, body)
		p.set(i, by+bh-1, body)
	}
	for j := 0; j < bh; j++ {
		p.set(0, by+j, body)
		p.set(bw-1, by+j, body)
	}
	for j := bh / 3; j < bh-bh/3; j++ {
		p.set(bw, by+j, body)
		p.set(bw+1, by+j, body)
	}
	iw := bw - 4
	n := min(iw, max(0, level*iw/100))
	blink := int(t*2)%2 == 0
	for i := 0; i < iw; i++ {
		on := i < n
		switch {
		case !in && on && i >= n-2 && blink: // draining: the charge's edge blinks
			on = false
		case in && charging && i == n && blink: // charging: the next column lights up
			on = true
		}
		if on {
			for j := 2; j < bh-2; j++ {
				p.set(2+i, by+j, fill)
			}
		}
	}
	if in { // the bolt, centred, bright on the charge
		bolt := []string{"....GGGG", "...GGGG.", "..GGGG..", ".GGGG...", "GGGGGGGG", "....GGG.", "...GGG..", "..GGG...", ".GGG....", "GG......"}
		if bh < 16 {
			bolt = []string{"...GGG", "..GGG.", ".GGGGG", "...GG.", "..GG..", ".GG...", "GG...."}
		}
		ox, oy := (bw-len(bolt[0]))/2, by+(bh-len(bolt))/2
		for j, row := range bolt {
			for i := range row {
				if row[i] == 'G' {
					p.set(ox+i, oy+j, 'G')
				}
			}
		}
	}
	// the Mac's side, then the connector: flat against it, or pulled away
	mx := bw + 8
	for j := by - 2; j < by+bh+2; j++ {
		p.set(mx, j, 'w')
		p.set(mx+1, j, 'w')
	}
	cx := mx + 2
	if !in {
		cx += 8
	}
	ch := bh / 2 // the connector: a flat block with rounded corners
	cy := by + (bh-ch)/2
	for j := 0; j < ch; j++ {
		for i := 0; i < 5; i++ {
			if (j == 0 || j == ch-1) && i == 4 {
				continue
			}
			p.set(cx+i, cy+j, cord)
		}
	}
	mid := by + bh/2
	for j := mid - 2; j < mid+2; j++ { // the strain relief
		for i := 5; i < 9; i++ {
			p.set(cx+i, j, cord)
		}
	}
	for i := cx + 9; i < pw; i++ { // the cord
		p.set(i, mid-1, cord)
		p.set(i, mid, cord)
	}
	p.draw(gr, x, y)
	ty := y + (by+bh)/2 + 1
	pc, word := g.GRY, i18n.T("draining")
	switch {
	case in && charging:
		pc, word = g.BGRN, i18n.T("charging")
	case in:
		pc, word = g.BGRN, i18n.T("on the charger")
	}
	gr.Put(x, ty, fmt.Sprintf("%d%%", level), g.WHT)
	gr.Put(x+5, ty, word, pc)
	lc, label := g.RED, i18n.T("unplugged")
	if in {
		lc, label = g.BGRN, i18n.T("plugged in")
	}
	gr.Put(x+mx, ty, label, lc)
}
