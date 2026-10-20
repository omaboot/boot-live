package handson

import (
	"fmt"
	"time"

	"github.com/omaboot/boot-live/internal/boot"
	"github.com/omaboot/boot-live/internal/bootwatch"
	"github.com/omaboot/boot-live/internal/checks"
	g "github.com/omaboot/boot-live/internal/grid"
	"github.com/omaboot/boot-live/internal/i18n"
)

// network.ethernet, "Ethernet", a test of its own, modelled on Wi-Fi's,
// whatever the Network screen did: the tester plugs a cable from a router or switch into the
// Mac's Ethernet port; the test waits for the link (carrier) and an IPv4 address (networkd's
// DHCP: the run is online through the cable afterwards, and the status line follows), and the
// boot.watch test moves data over the port's interface alone (bound to it). A port the Network
// screen turned off for Wi-Fi is turned back on first.
//
// The machine decides from the transfer, with the quick pass's own verdict
// (checks.EthernetOver): supported only with the data verified both ways over the port and its
// counters agreeing; partial when the data won't flow while boot.watch answers; not_tested when
// it can't tell (a link and no address within 20 s, a sign-in page, boot.watch down). An
// Ethernet adapter on USB or Thunderbolt is shown when it gets a link, but it isn't the Mac's
// port and decides nothing (the criterion is the Mac's own port). What the machine can't see, the
// tester answers (method observed):
//
//   - "It's plugged in, no link" (after 10 s with no link): failed: a cable into a working
//     router or switch, and the port never sees it;
//   - "Skip this one": not tested (no-equipment: no cable at hand).

func init() {
	Register(Spec{ID: "network.ethernet", Group: Plug, Order: 5, Title: i18n.M("Ethernet"),
		Brief: i18n.M("plug in an Ethernet cable"), Gear: i18n.M("an Ethernet cable"), Expect: 40 * time.Second,
		Applies: func(e *Env) bool {
			if e.Checks == nil {
				return true
			}
			_, ok := checks.FindEthernet(e.Checks)
			return ok
		},
		New: func(*Env) Run { return &ethTest{} }, Demo: demoEthernet})
}

// demoEthernet is the demo's port: no cable, then one at 3 s, an address at 4.2 s.
func demoEthernet(f *Fake) {
	f.Set(fakeNet+"eth", "14e4:16b4|tg3|eth0")
	f.Set(fakeNet+"link/eth0", "ethernet")
	f.Set("/sys/class/net/eth0/carrier", "0")
	f.SetAt(3, "/sys/class/net/eth0/carrier", "1")
	f.SetAt(4.2, fakeNet+"addr/eth0", "192.168.1.41")
}

const (
	ethPoll   = 0.25 // seconds between looks at the links
	ethNoLink = 10.0 // seconds with no link before "It's plugged in, no link" can be picked
	ethDHCP   = 20.0 // seconds a link gets for an address
)

type ethPhase int

const (
	ethCable  ethPhase = iota // waiting for a link
	ethUp                     // the port was off: turning it back on
	ethAddr                   // a link: waiting for an address
	ethMoving                 // the boot.watch test
)

type ethTest struct {
	net     Netter
	port    checks.EthPort
	phase   ethPhase
	at      float64 // when the phase began
	polled  float64
	link    NetLink
	others  []NetLink // adapters with a link: not the Mac's port
	up      Future[error]
	wasDown bool
	xfer    Transfer
	prog    bootwatch.Progress
	done    bool
	links   int // links seen come and go, for the evidence
	log     []Line
	say     string
	sayAt   float64
}

func (e *ethTest) Start(c *Ctx) {
	n, ok := c.IO.(Netter)
	if !ok {
		c.Decide(Outcome{Results: []checks.Result{noNet("network.ethernet")}})
		return
	}
	e.net = n
	p, ok := n.EthPort()
	if !ok {
		c.Decide(Outcome{Results: []checks.Result{NotTested("network.ethernet", checks.Uncertain, "no Ethernet port found", i18n.T("no Ethernet port found"))}})
		return
	}
	e.port = p
	if p.Decided != nil {
		e.log = append(e.log, Bad(p.PCI+": "+p.Decided.Detail))
		c.Decide(Outcome{Results: []checks.Result{*p.Decided}, Say: i18n.T("the port has no working driver here. nothing to plug into.")})
		return
	}
	e.log = append(e.log, Fact(fmt.Sprintf("%s on %s, %s", p.PCI, p.Driver, p.Iface)))
	e.look(c)
	switch {
	case e.link.Down:
		e.wasDown = true
		e.phase, e.at, e.up = ethUp, c.T, n.LinkUp(p.Iface)
		e.log = append(e.log, Gone(i18n.T("%s was turned off for another connection: turning it back on", p.Iface)))
	case e.link.Carrier:
		e.toAddr(c)
		e.talk(c, i18n.T("a cable's in already. asking for an address."))
		return
	}
	e.talk(c, i18n.T("plug me in. I'll see the link."))
	c.Select(-1)
}

func (e *ethTest) talk(c *Ctx, s string) { e.say, e.sayAt = s, c.T }

// look reads the port's link and the adapters' as they are now.
func (e *ethTest) look(c *Ctx) {
	e.polled = c.T
	e.others = nil
	found := false
	for _, l := range e.net.Links() {
		switch {
		case l.Name == e.port.Iface:
			e.link, found = l, true
		case l.Ethernet && l.Carrier:
			e.others = append(e.others, l)
		}
	}
	if !found {
		e.link = NetLink{Name: e.port.Iface, Ethernet: true, Carrier: c.IO.Read("/sys/class/net/"+e.port.Iface+"/carrier") == "1"}
	}
}

func (e *ethTest) toAddr(c *Ctx) {
	e.phase, e.at = ethAddr, c.T
	e.links++
	e.log = append(e.log, Fact(i18n.T("link up on %s", e.port.Iface)))
	c.Select(-1)
}

func (e *ethTest) Step(c *Ctx) {
	switch e.phase {
	case ethUp:
		err, ok := e.up.Ready()
		if !ok {
			return
		}
		e.up = nil
		if err != nil {
			e.log = append(e.log, Bad(i18n.T("couldn't turn %s back on: %s", e.port.Iface, err.Error())))
		}
		e.phase, e.at = ethCable, c.T
	case ethMoving:
		e.prog = e.xfer.Progress()
		if m, ok := e.xfer.Ready(); ok {
			e.decide(c, m)
		}
		return
	}
	if c.T-e.polled < ethPoll {
		return
	}
	e.look(c)
	switch e.phase {
	case ethCable:
		if e.link.Carrier {
			e.toAddr(c)
			e.talk(c, i18n.T("there's the link. asking for an address."))
		}
	case ethAddr:
		switch {
		case !e.link.Carrier:
			e.phase, e.at = ethCable, c.T
			e.log = append(e.log, Bad(i18n.T("the link went away")))
			e.talk(c, i18n.T("the link dropped. is the cable all the way in?"))
		case e.link.Addr != "":
			e.log = append(e.log, Fact(i18n.T("address %s", e.link.Addr)))
			e.move(c)
			e.talk(c, i18n.T("online. now a quick transfer."))
		case c.T-e.at >= ethDHCP: // no address: the checks' verdict says so (not tested)
			e.log = append(e.log, Bad(i18n.T("no address in %.0f s (no DHCP answer)", ethDHCP)))
			e.move(c)
		}
	}
}

func (e *ethTest) move(c *Ctx) {
	e.phase, e.at = ethMoving, c.T
	e.xfer = e.net.Transfer("network.ethernet", e.port.Iface, 0)
	c.Select(-1)
}

func (e *ethTest) decide(c *Ctx, m Moved) {
	e.done = true
	e.log = append(e.log, movedLines(m, serverName(e.xfer))...)
	if m.Online != "" {
		e.log = append(e.log, Fact(i18n.T("the run is online through %s", via(m.Online))))
	}
	r := m.Result
	if e.wasDown {
		r.Evidence += " (the port was turned off for Wi-Fi on the Network screen and turned back on for the test)"
	}
	var say string
	switch r.Status {
	case checks.Supported:
		say = i18n.T("ethernet works. every byte came back right.")
	case checks.Partial:
		say = i18n.T("a link and an address, but the data won't flow. partial.")
	default:
		say = i18n.T("couldn't tell this time: %s.", r.Detail)
	}
	c.Decide(Outcome{Results: []checks.Result{r}, Say: say})
}

// noLink: long enough without a link that the tester may say the cable is in.
func (e *ethTest) noLink(c *Ctx) bool { return e.phase == ethCable && c.T-e.at >= ethNoLink }

func (e *ethTest) Act(c *Ctx, i int) {
	if i != 0 || !e.noLink(c) {
		return
	}
	ev := e.port.Evidence() + fmt.Sprintf(": no link; the tester says a cable is plugged in to a working router or switch (waited %.0f s)", c.T-e.at)
	c.Decide(Outcome{Results: []checks.Result{Result("network.ethernet", checks.Failed, Observed, ev, i18n.T("%s, cable in, no link", e.port.Driver))},
		Say: i18n.T("a cable's in and the port never saw it. that's a fail.")})
}

func (e *ethTest) Stop(*Ctx) {}

func (e *ethTest) View(c *Ctx) Screen {
	s := Screen{Say: e.say, SayAt: e.sayAt, Face: boot.Idle,
		Steps: []string{i18n.T("Plug a cable from your router or switch into the Mac's Ethernet port."),
			i18n.T("Wait for the link and an address."), i18n.T("Wait while test data goes over it.")},
		Happens: i18n.T("The port gets an address for the rest of this run, then test data goes to and from boot.watch through the port alone, and every byte is checked."),
		Hint:    i18n.T("no link yet? push the plug in until it clicks, and check the router end's light."),
		Anim:    e.art,
	}
	if e.net == nil {
		return s
	}
	s.Live = append([]Line(nil), e.log...)
	for _, l := range e.others {
		what := i18n.T("an Ethernet adapter")
		if l.Product != "" {
			what = l.Product
		}
		s.Live = append(s.Live, Gone(i18n.T("%s (%s): link up, but it isn't the Mac's own port", l.Name, what)))
	}
	switch e.phase {
	case ethUp:
		s.Waiting = i18n.T("·  turning the port on")
	case ethCable:
		s.Live = append(s.Live, Wait(i18n.T("waiting for a link on %s", e.port.Iface)))
		s.Waiting = i18n.T("·  waiting for a cable")
		s.Actions = []Action{{Label: i18n.T("It's plugged in, no link"), Does: i18n.T("its other end is in a working router"), Off: !e.noLink(c)}}
	case ethAddr:
		s.Step, s.Face = 1, boot.Work
		s.Live = append(s.Live, Wait(i18n.T("asking for an address (DHCP)")))
		s.Waiting = i18n.T("·  asking for an address")
	case ethMoving:
		s.Step, s.Face = 2, boot.Work
		if !e.done {
			s.Live = append(s.Live, movingLine(serverName(e.xfer), e.port.Iface))
		}
		s.Waiting = i18n.T("·  moving data")
	}
	if e.done {
		s.Step = 3
	}
	return s
}

// art is the band's picture: the Mac, the cable to the router (dashed until there's a link),
// then the transfer to boot.watch.
func (e *ethTest) art(gr *g.Grid, x, y, w, h int, t float64) {
	a := transferArt{far: i18n.T("router"), under: e.port.Iface + "  ·  " + e.port.Driver}
	switch e.phase {
	case ethUp, ethCable:
		a.words = i18n.T("no cable yet")
		if e.links > 0 {
			a.words = i18n.T("the link went away")
		}
	case ethAddr:
		a.line, a.words = 1, i18n.T("link up  ·  asking for an address…")
	case ethMoving:
		a.far, a.line, a.prog, a.words = serverName(e.xfer), 2, e.prog, progressText(e.prog, serverName(e.xfer))
		if m, ok := e.xfer.Ready(); ok {
			a.ok, a.bad = m.Result.Status == checks.Supported, m.Result.Status == checks.Partial || m.Result.Status == checks.Failed
			a.words = i18n.T("%s down, %s up", size(m.Outcome.Down), size(m.Outcome.Up))
			if a.ok {
				a.words += "  ·  " + i18n.T("every byte right")
			}
		}
	}
	a.draw(gr, x, y, w, h, t)
}
