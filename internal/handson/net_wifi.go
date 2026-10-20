package handson

import (
	"fmt"
	"strings"
	"time"

	"github.com/omaboot/boot-live/internal/boot"
	"github.com/omaboot/boot-live/internal/bootwatch"
	"github.com/omaboot/boot-live/internal/checks"
	g "github.com/omaboot/boot-live/internal/grid"
	"github.com/omaboot/boot-live/internal/i18n"
)

// network.wifi, "Wi-Fi", a test of its own,
// whatever the Network screen did (an offline run too): the tester picks a network from a scan
// and types its password; the Mac joins it for the rest of the run (the run stays online
// through it, and the status line follows), and the boot.watch test moves data over the Wi-Fi
// interface alone (bound to it). A network the card joined on the Network screen is used as it
// is: the test goes straight to the data.
//
// The machine decides from the transfer, with the quick pass's own verdict (checks.WifiJoined):
// supported only with the data verified both ways over the Mac's own card and its counters
// agreeing; partial when it joins and the data won't flow while boot.watch answers; not_tested
// when it can't tell (a sign-in page, boot.watch down, the Mac's clock, a USB dongle). What the
// machine can't see, the tester answers (method observed):
//
//   - "It won't join" (after a failed join; a refused password is asked once more first):
//     partial: the card hears networks, but joining fails;
//   - "Wi-Fi networks are in range here" (the scan heard none): failed: a deaf radio;
//   - "My network isn't listed" scans again; "Skip this one" leaves it not tested.
//
// A scan that fails twice is a fail (as in the quick pass). The network's name and password
// never go into the report; the password lives only until the join is asked.

func init() {
	Register(Spec{ID: "network.wifi", Group: NoPlug, Order: 95, Title: i18n.M("Wi-Fi"),
		Brief: i18n.M("join your Wi-Fi network"), Gear: i18n.M("your Wi-Fi password"), Expect: 60 * time.Second,
		Applies: func(e *Env) bool {
			if e.Checks == nil {
				return true
			}
			_, ok := checks.FindWifi(e.Checks)
			return ok
		},
		New: func(*Env) Run { return &wifiTest{} }, Demo: demoWifi})
}

// demoWifi is the demo's Wi-Fi: the Air's BCM43224 hears 13 networks; any password of eight
// characters or more joins.
func demoWifi(f *Fake) {
	f.Set(fakeNet+"wifi", "BCM43224|14e4:4353|brcmsmac|wlan0")
	f.Set(fakeNet+"aps", strings.Join([]string{"maple-house-5G|-52|psk", "maple-house|-55|psk", "TELUS2381|-66|psk",
		"BELL442|-78|psk", "Shaw Open|-81|open", "HP-Print-3A-LaserJet|-83|psk", "linksys|-85|psk", "TELUS0719|-86|psk",
		"BELL118|-87|psk", "Rogers 5G|-88|psk", "NETGEAR52|-89|psk", "guest|-90|open", "TELUS2042|-91|psk"}, "\n"))
}

// wifiList is how many networks the list shows (each with its number key), strongest first.
const wifiList = 6

type wifiPhase int

const (
	wfScan     wifiPhase = iota // scanning
	wfList                      // the networks: pick one
	wfPassword                  // typing the password
	wfJoining                   // joining, then asking for an address
	wfMoving                    // the boot.watch test
)

type wifiTest struct {
	net     Netter
	card    checks.WifiCard
	phase   wifiPhase
	at      float64 // when the phase began
	scan    Future[WifiScan]
	scans   int
	errs    int    // scans that failed
	aps     []AP   // the last scan's networks
	pick    AP     // the network picked
	pw      []rune // the password typed so far
	show    bool   // the password shows as typed (Tab)
	again   bool   // the password is being asked once more ("It won't join")
	join    Future[WifiJoin]
	tries   int // joins tried
	fails   []string
	failed  string // the last join's problem, for the screen ("" after a success)
	refused bool   // the last join failed on the password
	joined  WifiJoin
	early   string // the network joined on the Network screen
	xfer    Transfer
	prog    bootwatch.Progress
	done    bool
	log     []Line
	say     string
	sayAt   float64
}

func (w *wifiTest) Start(c *Ctx) {
	n, ok := c.IO.(Netter)
	if !ok {
		c.Decide(Outcome{Results: []checks.Result{noNet("network.wifi")}})
		return
	}
	w.net = n
	card, ok := n.WifiCard()
	if !ok {
		c.Decide(Outcome{Results: []checks.Result{NotTested("network.wifi", checks.Uncertain, "no Wi-Fi card found", i18n.T("no Wi-Fi card found"))}})
		return
	}
	w.card = card
	if card.Decided != nil {
		w.log = append(w.log, Bad(fmt.Sprintf("%s (%s) on %s: %s", card.Chip, card.PCI, card.Driver, card.Decided.Detail)))
		say := i18n.T("no wireless interface for the card. nothing to join with.")
		if card.Decided.Reason == checks.LiveLimit {
			say = i18n.T("this card's driver only comes with the installed system.")
		}
		c.Decide(Outcome{Results: []checks.Result{*card.Decided}, Say: say})
		return
	}
	w.log = append(w.log, Fact(card.Chip+" ("+card.PCI+") on "+card.Driver+", "+card.Iface))
	if ssid := n.WifiJoined(); ssid != "" {
		w.early = ssid
		w.log = append(w.log, Fact(i18n.T("joined %s on the Network screen", ssid)))
		w.move(c, card.Iface)
		w.talk(c, i18n.T("already on %s. now a quick transfer.", ssid))
		return
	}
	w.startScan(c)
}

func (w *wifiTest) talk(c *Ctx, s string) { w.say, w.sayAt = s, c.T }

func (w *wifiTest) startScan(c *Ctx) {
	w.phase, w.at, w.scan = wfScan, c.T, w.net.WifiScan()
	w.scans++
	c.Select(-1)
	if w.scans == 1 {
		w.talk(c, i18n.T("listening for networks."))
	}
}

// move starts the boot.watch test over iface.
func (w *wifiTest) move(c *Ctx, iface string) {
	w.phase, w.at = wfMoving, c.T
	w.xfer = w.net.Transfer("network.wifi", iface, len(w.aps))
	c.Select(-1)
}

func (w *wifiTest) Step(c *Ctx) {
	switch w.phase {
	case wfScan:
		s, ok := w.scan.Ready()
		if !ok {
			return
		}
		w.scan = nil
		if s.Err != nil {
			w.errs++
			w.log = append(w.log, Bad(i18n.T("scan failed: %s", s.Err.Error())))
			if w.errs >= 2 {
				ev := w.card.Evidence() + ": the scan failed twice (" + s.Err.Error() + ") and no network was joined"
				c.Decide(Outcome{Results: []checks.Result{Result("network.wifi", checks.Failed, Automatic, ev, i18n.T("%s, scan failed", w.card.Chip))},
					Say: i18n.T("the scan fails every time. the radio isn't working.")})
				return
			}
			w.startScan(c)
			return
		}
		w.aps = s.APs
		w.phase, w.at = wfList, c.T
		w.log = append(w.log, Fact(i18n.N("scanned: %d network", "scanned: %d networks", len(w.aps), len(w.aps))))
		switch {
		case len(w.aps) == 0:
			w.talk(c, i18n.T("I hear no networks at all. are there any here?"))
		case w.scans == 1:
			w.talk(c, i18n.N("%d in range. which one's yours?", "%d in range. which one's yours?", len(w.aps), len(w.aps)))
		default:
			w.talk(c, i18n.N("%d in range now.", "%d in range now.", len(w.aps), len(w.aps)))
		}
		c.Select(0)
	case wfJoining:
		j, ok := w.join.Ready()
		if !ok {
			return
		}
		w.join = nil
		if j.Err != nil {
			w.joinFailed(c, j)
			return
		}
		w.joined, w.failed = j, ""
		w.log = append(w.log, Fact(i18n.T("joined %s: signal %d dBm", w.pick.SSID, w.pick.Signal)), Fact(i18n.T("address %s", j.IP)))
		if j.Online != "" && c.Env != nil && c.Env.Online == "" {
			c.Env.Online = j.Online // the tests after this one see the run online through it
		}
		w.move(c, j.Iface)
		w.talk(c, i18n.T("in. now a quick transfer."))
	case wfMoving:
		w.prog = w.xfer.Progress()
		m, ok := w.xfer.Ready()
		if !ok {
			return
		}
		w.decide(c, m)
	}
}

// joinFailed takes a join that didn't work back to the list, with what went wrong.
func (w *wifiTest) joinFailed(c *Ctx, j WifiJoin) {
	w.phase, w.at, w.refused = wfList, c.T, j.Refused
	switch {
	case j.Refused:
		w.failed = i18n.T("%s refused the password", w.pick.SSID)
		w.fails = append(w.fails, "the network refused the password")
		w.talk(c, i18n.T("it said no to that password. try again?"))
	case j.NoAddr:
		w.failed = i18n.T("joined %s, but no address came (DHCP)", w.pick.SSID)
		w.fails = append(w.fails, "joined, but no address came (DHCP)")
		w.talk(c, i18n.T("joined, but nobody handed me an address."))
	default:
		w.failed = i18n.T("couldn't join %s: %s", w.pick.SSID, j.Err.Error())
		w.fails = append(w.fails, "couldn't join: "+j.Err.Error())
		w.talk(c, i18n.T("that didn't take. try again, or tell me."))
	}
	w.log = append(w.log, Bad(w.failed))
	c.Select(0)
}

func (w *wifiTest) decide(c *Ctx, m Moved) {
	w.done = true
	w.log = append(w.log, movedLines(m, serverName(w.xfer))...)
	switch {
	case strings.HasSuffix(m.Online, ", Wi-Fi"):
		w.log = append(w.log, Fact(i18n.T("the run stays online through Wi-Fi")))
	case m.Online != "":
		w.log = append(w.log, Gone(i18n.T("the run stays online through %s; Wi-Fi stays joined", via(m.Online))))
	}
	r := m.Result
	ev := ""
	if w.early != "" {
		ev = " (joined on the Network screen)"
	}
	r.Evidence += ev
	var say string
	switch r.Status {
	case checks.Supported:
		say = i18n.T("wi-fi works. every byte came back right.")
		if m.Online != "" {
			say = i18n.T("wi-fi works. and we're online.")
		}
	case checks.Partial:
		say = i18n.T("it joins, but the data won't flow. partial.")
	case checks.Failed:
		say = i18n.T("that's a fail.")
	default:
		say = i18n.T("couldn't tell this time: %s.", r.Detail)
	}
	c.Decide(Outcome{Results: []checks.Result{r}, Say: say})
}

// wifiActs are the answers on the list, in order: the networks, then "My network isn't listed",
// then "It won't join" after a failed join; with no network heard, "Scan again" and "There are
// networks here".
type wifiAct int

const (
	waNetwork wifiAct = iota
	waRescan
	waWontJoin
	waInRange
)

func (w *wifiTest) acts() ([]Action, []wifiAct) {
	if w.phase != wfList {
		return nil, nil
	}
	var a []Action
	var k []wifiAct
	if len(w.aps) == 0 {
		a = append(a, Action{Label: i18n.T("Scan again"), Does: i18n.T("listen once more")},
			Action{Label: i18n.T("Wi-Fi networks are in range here"), Does: i18n.T("it heard none: noted as a fail")})
		return a, []wifiAct{waRescan, waInRange}
	}
	for _, ap := range w.aps[:min(wifiList, len(w.aps))] {
		a = append(a, Action{Label: ap.SSID, Bars: max(1, min(4, ap.Bars)), Does: secWord(ap.Security)})
		k = append(k, waNetwork)
	}
	a = append(a, Action{Label: i18n.T("My network isn't listed"), Does: i18n.T("scan again"), Gap: true})
	k = append(k, waRescan)
	if w.tries > 0 {
		does := i18n.T("it goes in the report: it hears networks, but won't join")
		if w.askAgain() {
			does = i18n.T("type the password once more first")
		}
		a = append(a, Action{Label: i18n.T("It won't join"), Does: does})
		k = append(k, waWontJoin)
	}
	return a, k
}

// askAgain: "It won't join" asks the password once more first, after the first refused one.
func (w *wifiTest) askAgain() bool { return !w.again && w.refused && w.tries == 1 }

func secWord(sec string) string {
	switch sec {
	case "psk":
		return "WPA2"
	case "8021x":
		return "802.1X"
	case "wep":
		return "WEP"
	case "open":
		return i18n.T("open")
	}
	return sec
}

func (w *wifiTest) Act(c *Ctx, i int) {
	acts, kinds := w.acts()
	if i < 0 || i >= len(acts) {
		return
	}
	switch kinds[i] {
	case waNetwork:
		w.pick, w.again = w.aps[i], false
		if w.pick.Open() {
			w.log = append(w.log, Fact(i18n.T("%s: open, no password", w.pick.SSID)))
			w.startJoin(c, "")
			return
		}
		w.log = append(w.log, Fact(i18n.T("%s: %s, asks for a password", w.pick.SSID, secWord(w.pick.Security))))
		w.askPassword(c)
	case waRescan:
		w.failed = ""
		w.startScan(c)
	case waWontJoin:
		if w.askAgain() {
			w.again = true
			w.askPassword(c)
			w.talk(c, i18n.T("once more, slowly. capitals count."))
			return
		}
		ev := w.card.Evidence() + fmt.Sprintf(": the scan heard %d networks; joining failed %d time(s) (%s), and the tester says it won't join",
			len(w.aps), w.tries, strings.Join(w.fails, "; "))
		c.Decide(Outcome{Results: []checks.Result{Result("network.wifi", checks.Partial, Observed, ev, i18n.T("%s hears networks, won't join", w.card.Chip))},
			Say: i18n.T("it hears them, it just won't join. partial.")})
	case waInRange:
		ev := w.card.Evidence() + fmt.Sprintf(": %d scans heard no networks, and the tester says Wi-Fi networks are in range", w.scans)
		c.Decide(Outcome{Results: []checks.Result{Result("network.wifi", checks.Failed, Observed, ev, i18n.T("%s hears no networks", w.card.Chip))},
			Say: i18n.T("networks all around, and it hears none. that's a fail.")})
	}
}

func (w *wifiTest) askPassword(c *Ctx) {
	w.phase, w.at, w.pw, w.show = wfPassword, c.T, nil, false
	c.Select(-1)
	w.talk(c, i18n.T("the password stays in this run."))
}

func (w *wifiTest) startJoin(c *Ctx, password string) {
	w.phase, w.at = wfJoining, c.T
	w.tries++
	w.join = w.net.WifiJoin(w.pick.SSID, password)
	w.talk(c, i18n.T("joining. a moment."))
	c.Select(-1)
}

// TakesKeys: the password field has the keyboard while it's up.
func (w *wifiTest) TakesKeys(*Ctx) bool { return w.phase == wfPassword }

// Key types the password: Enter joins, Esc goes back to the list, Tab shows or hides it.
func (w *wifiTest) Key(c *Ctx, key string) {
	switch key {
	case "enter":
		if len(w.pw) > 0 {
			pw := string(w.pw)
			for i := range w.pw {
				w.pw[i] = 0
			}
			w.pw = nil
			w.startJoin(c, pw)
		}
	case "esc", "ctrl+c":
		w.pw, w.phase, w.at = nil, wfList, c.T
		c.Select(0)
	case "backspace":
		if len(w.pw) > 0 {
			w.pw = w.pw[:len(w.pw)-1]
		}
	case "tab":
		w.show = !w.show
	default:
		if s := textKey(key); s != "" && len(w.pw) < 63 {
			w.pw = append(w.pw, []rune(s)...)
		}
	}
}

func (w *wifiTest) Stop(*Ctx) { w.pw = nil }

func (w *wifiTest) View(c *Ctx) Screen {
	s := Screen{Say: w.say, SayAt: w.sayAt, Face: boot.Idle,
		Steps:   []string{i18n.T("Pick your network."), i18n.T("Type its password."), i18n.T("Wait while test data goes over it.")},
		Happens: i18n.T("Joins the network for the rest of this run, then moves test data to and from boot.watch through the Wi-Fi adapter alone, and checks every byte."),
		Hint:    i18n.T("not on the list? it may be far off, or only on 5 GHz. My network isn't listed scans again."),
		Anim:    w.art,
	}
	if w.net == nil {
		return s
	}
	s.Live = append([]Line(nil), w.log...)
	switch w.phase {
	case wfScan:
		s.Live = append(s.Live, Wait(i18n.T("scanning with %s", w.card.Iface)))
		s.Waiting, s.Face = i18n.T("·  scanning"), boot.Work
	case wfList:
		s.Actions, _ = w.acts()
		s.Waiting = i18n.T("·  waiting for you")
		if len(w.aps) > wifiList {
			s.Note = i18n.T("%d more, weaker: not listed", len(w.aps)-wifiList)
		}
	case wfPassword:
		s.Step = 1
		s.Panel, s.PanelH = w.field, 5
		s.Waiting = i18n.T("·  waiting for the password")
		s.SkipDoes = i18n.T("Esc first: the keys type the password") // s is a letter here
	case wfJoining:
		s.Step, s.Face = 2, boot.Work
		s.Live = append(s.Live, Wait(i18n.T("joining %s", w.pick.SSID)))
		s.Waiting = i18n.T("·  joining")
	case wfMoving:
		s.Step, s.Face = 2, boot.Work
		if !w.done {
			s.Live = append(s.Live, movingLine(serverName(w.xfer), w.card.Iface))
		}
		s.Waiting = i18n.T("·  moving data")
	}
	if w.done {
		s.Step = 3
	}
	if w.pick.Open() && s.Step >= 1 && s.Step < 3 {
		s.Step = 2
	}
	return s
}

// field is the password box (the left column's panel): what it's for, the box, the keys.
func (w *wifiTest) field(gr *g.Grid, x, y, wd, h int, t float64) {
	label := i18n.T("Password for %s", w.pick.SSID)
	if w.again {
		label = i18n.T("Password for %s, once more", w.pick.SSID)
	}
	PasswordBox(gr, x, y, wd, h, t, label, w.pw, w.show, true)
}

// PasswordBox draws a Wi-Fi password field, here and on the Network step: the label, the box
// with the password as dots (or shown: Tab), a blinking cursor, and the keys under it. While
// it doesn't type (joining) the cursor and the keys line go: the row is the caller's.
func PasswordBox(gr *g.Grid, x, y, wd, h int, t float64, label string, pw []rune, show, typing bool) {
	gr.Put(x, y, cut(label, wd), g.GRY)
	bw := min(36, wd)
	if bw < 8 || h < 4 {
		return
	}
	bc := g.BVIO
	if !typing {
		bc = g.DIM
	}
	gr.Put(x, y+1, "┌"+strings.Repeat("─", bw-2)+"┐", bc)
	gr.Put(x, y+2, "│", bc)
	gr.Put(x+bw-1, y+2, "│", bc)
	gr.Put(x, y+3, "└"+strings.Repeat("─", bw-2)+"┘", bc)
	shown := strings.Repeat("•", len(pw))
	if show {
		shown = string(pw)
	}
	if r := []rune(shown); len(r) > bw-5 {
		shown = "…" + string(r[len(r)-(bw-6):])
	}
	end := gr.Put(x+2, y+2, shown, g.WHT)
	if !typing {
		return
	}
	if int(t*2)%2 == 0 {
		gr.Put(end, y+2, "█", g.GRY)
	}
	if h >= 5 {
		keys := i18n.T("Enter joins  ·  Tab shows  ·  Esc back")
		if show {
			keys = i18n.T("Enter joins  ·  Tab hides  ·  Esc back")
		}
		gr.Put(x, y+4, cut(keys, wd), g.DIM)
	}
}

// art is the band's picture: the radar while it listens and the list is up, then the transfer.
func (w *wifiTest) art(gr *g.Grid, x, y, wd, h int, t float64) {
	switch w.phase {
	case wfScan, wfList, wfPassword:
		boot.Radar.Draw(gr, boot.Params{Count: len(w.aps)}, x, y, wd, h, t)
		return
	}
	a := transferArt{far: i18n.T("router"), line: 1, under: w.pick.SSID + "  ·  " + w.card.Iface}
	if w.early != "" {
		a.under = w.early + "  ·  " + w.card.Iface
	}
	switch {
	case w.phase == wfJoining:
		a.line, a.words = 0, i18n.T("joining %s…", w.pick.SSID)
	default:
		a.far, a.line, a.prog, a.words = serverName(w.xfer), 2, w.prog, progressText(w.prog, serverName(w.xfer))
		if m, ok := w.xfer.Ready(); ok {
			a.ok, a.bad = m.Result.Status == checks.Supported, m.Result.Status == checks.Partial || m.Result.Status == checks.Failed
			a.words = i18n.T("%s down, %s up", size(m.Outcome.Down), size(m.Outcome.Up))
			if a.ok {
				a.words += "  ·  " + i18n.T("every byte right")
			}
		}
	}
	a.draw(gr, x, y, wd, h, t)
}
