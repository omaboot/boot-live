package checks

import (
	"context"
	"fmt"
	"path"
	"strings"
	"time"

	"github.com/omaboot/boot-live/internal/boot"
	"github.com/omaboot/boot-live/internal/bootwatch"
	"github.com/omaboot/boot-live/internal/i18n"
	"github.com/omaboot/boot-live/internal/identify"
	"github.com/omaboot/boot-live/internal/network"
)

// WifiChips names the Wi-Fi chips Intel Macs shipped with, by PCI ID.
var WifiChips = map[string]string{
	"14e4:4311": "BCM4311", "14e4:4312": "BCM4311", "14e4:4328": "BCM4321", "14e4:432b": "BCM4322",
	"14e4:4353": "BCM43224", "14e4:4357": "BCM43225", "14e4:4331": "BCM4331", "14e4:43a0": "BCM4360",
	"14e4:43a3": "BCM4350", "14e4:43ba": "BCM43602", "14e4:4464": "BCM4364", "14e4:4488": "BCM4377",
	"168c:001c": "AR5418", "168c:0024": "AR5008", "168c:002a": "AR928X", "168c:0030": "AR93xx",
}

// noLiveDriver are the Wi-Fi chips whose only driver (broadcom-wl) comes with the installed
// system: a live boot can't decide them.
var noLiveDriver = map[string]bool{"14e4:4331": true, "14e4:43a0": true}

// netIface is a network interface and the device under it.
type netIface struct {
	Name, Driver, Slot string
	Wifi               bool
	Carrier            bool
	USB                bool // on a USB device (an adapter or dongle), not the Mac's own card
}

// shown is the interface's name as the report shows it (never a MAC address).
func (n netIface) shown() string {
	if n.Wifi {
		return network.Shown(n.Name, network.WiFi)
	}
	return network.Shown(n.Name, network.Ethernet)
}

func ifaces(e *Env) []netIface {
	var out []netIface
	for _, d := range e.Root.Glob("/sys/class/net/*") {
		if e.Root.Read(d+"/type") != "1" {
			continue
		}
		ue := e.Root.Uevent(d + "/device/uevent")
		if len(ue) == 0 {
			continue // virtual
		}
		n := netIface{Name: path.Base(d), Driver: ue["DRIVER"], Slot: ue["PCI_SLOT_NAME"],
			Wifi:    e.Root.Exists(d+"/wireless") || e.Root.Exists(d+"/phy80211") || e.Root.Uevent(d + "/uevent")["DEVTYPE"] == "wlan",
			Carrier: e.Root.Read(d+"/carrier") == "1", USB: ue["DEVTYPE"] == "usb_interface"}
		out = append(out, n)
	}
	return out
}

func wifiDevices(e *Env) []identify.PCIDev { return e.HW.PCIByClass("0280") }

// ethernetDevices are the Mac's own Ethernet controllers: the catalog's when the configuration
// is known (an adapter on Thunderbolt isn't the Mac's port), else every PCI Ethernet controller.
func ethernetDevices(e *Env) []identify.PCIDev {
	all := e.HW.PCIByClass("0200")
	if e.Cat == nil || e.Ident.Config == "" {
		return all
	}
	cfg := e.Cat.Config(e.Ident.Config)
	if cfg == nil {
		return all
	}
	own := map[string]bool{}
	for _, p := range cfg.Parts {
		if p.Kind == "ethernet" {
			for _, id := range p.IDs {
				own[strings.TrimPrefix(id, "pci:")] = true
			}
		}
	}
	var out []identify.PCIDev
	for _, d := range all {
		if own[d.ID()] {
			out = append(out, d)
		}
	}
	return out
}

// carry is what the boot.watch test over one interface showed (data verified both ways replaces
// "data moved").
type carry int

const (
	carryOK        carry = iota // data verified both ways, and the interface's counters grew by it
	carryBroken                 // boot.watch answers (over it, or over the run's own connection), but it didn't carry the test
	carryNothing                // it's the run's only connection and nothing worked over it: neither boot.watch nor the site
	carryNoServer               // no bootwatchd to test against (a fixture run)
	carryUncounted              // its byte counters can't be read
	carryElsewhere              // its counters didn't grow by what moved: the data went another way
	carrySignIn                 // the network wants a sign-in page
	carryClock                  // the Mac's clock is off, so boot.watch's certificate can't be checked
	carryBusy                   // boot.watch turned the test away (its limits)
	carryWatchDown              // boot.watch answered over no connection: its fault, not the Mac's
)

// counterLag is how long carried keeps reading an interface's counters for what the test moved.
var counterLag = 2 * time.Second

// carried runs the boot.watch test over iface, bound to it (15 s at most), and reads iface's
// byte counters around it. When the test fails, it tells a broken interface from boot.watch
// being down: boot.watch answered over iface before it broke, or answers over the run's own
// connection when that's another; on the run's only connection, the site's catalog refresh
// having worked over it says the interface carries data, so boot.watch is the one at fault.
// The second value is the evidence.
func carried(ctx context.Context, e *Env, iface string) (carry, string) {
	if e.Net.Watch == nil {
		return carryNoServer, "no test server (boot.watch) to move data through"
	}
	read := func() (uint64, bool) {
		if e.Net.Bytes != nil {
			return e.Net.Bytes(iface)
		}
		return network.Bytes(e.Root, iface)
	}
	before, ok := read()
	if !ok {
		return carryUncounted, "its byte counters can't be read, so what moved over it is unknown"
	}
	tctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	o := e.Net.Watch(tctx, iface)
	cancel()
	after, ok := read()
	if !ok {
		return carryUncounted, "its byte counters can't be read, so what moved over it is unknown"
	}
	ev := o.String()
	grew := delta(after, before)
	switch o.Problem {
	case bootwatch.OK:
		// some drivers refresh their counters only every second or so (tg3: the Mac mini's 1.3 MB
		// at 51.8 MB/s read +0 KB, 0.8.5 dev): read again for a while before the data went
		// another way
		for wait := time.Now().Add(counterLag); grew < uint64(o.Moved()) && time.Now().Before(wait); {
			select {
			case <-ctx.Done():
				wait = time.Time{}
				continue
			case <-time.After(counterLag / 8):
			}
			if now, ok := read(); ok {
				grew = delta(now, before)
			}
		}
		if grew < uint64(o.Moved()) {
			return carryElsewhere, ev + "; but its counters grew by only " + kb(grew) + ": the data went another way"
		}
		return carryOK, ev + "; its counters +" + kb(grew)
	case bootwatch.SignIn:
		return carrySignIn, ev + "; " + o.Err
	case bootwatch.Clock:
		return carryClock, ev + "; " + o.Err
	case bootwatch.Busy:
		return carryBusy, ev + "; " + o.Err
	}
	ev += "; " + o.Err
	if o.Reached {
		return carryBroken, ev + " (boot.watch answered over it before that)"
	}
	if e.Net.Reach != nil && e.Net.Online && e.Net.Iface != iface {
		rctx, cancel := context.WithTimeout(ctx, 8*time.Second)
		r := e.Net.Reach(rctx)
		cancel()
		if r.Reached {
			return carryBroken, ev + "; boot.watch answers over the run's own connection"
		}
		return carryWatchDown, ev + "; boot.watch didn't answer over the run's own connection either (" + r.Err + ")"
	}
	if e.Net.SiteVia == iface {
		return carryWatchDown, ev + "; the catalog refresh from the site worked over it, so boot.watch is the one not answering"
	}
	return carryNothing, ev + "; nothing else worked over it either"
}

// undecided is the not-tested reason and the screen's few words for a test that couldn't
// decide; false for the outcomes that decide (carryOK, carryBroken, carryNothing).
func undecided(c carry) (reason, words string, ok bool) {
	switch c {
	case carryNoServer:
		return Uncertain, i18n.T("no test server"), true
	case carryUncounted:
		return Uncertain, i18n.T("data not measured"), true
	case carryElsewhere:
		return Uncertain, i18n.T("data went another way"), true
	case carrySignIn:
		return Uncertain, i18n.T("network wants a sign-in page"), true
	case carryClock:
		return Other, i18n.T("the Mac's clock is off"), true
	case carryBusy:
		return Other, i18n.T("boot.watch busy"), true
	case carryWatchDown:
		return Other, i18n.T("boot.watch didn't answer"), true
	}
	return "", "", false
}

// delta is what a counter added since since; a counter below it started over (the interface
// came back: a USB adapter replugged), so all of it is new.
func delta(now, since uint64) uint64 {
	if now < since {
		return now
	}
	return now - since
}

// addr is an interface's IPv4 address, or "".
func addr(e *Env, iface string) string {
	if e.Net.Addr != nil {
		return e.Net.Addr(iface)
	}
	return network.LinkAddr(e.Root, iface)
}

// kb is a byte count for the report: "412 KB".
func kb(n uint64) string { return fmt.Sprintf("%d KB", n>>10) }

// Wi-Fi and Ethernet are decided by their hands-on tests (internal/handson, net_wifi.go and
// net_ethernet.go): the person picks a network and types its password, or
// plugs in a cable, whatever the Network screen did, and the test moves data over that
// interface alone through boot.watch. The quick pass only reports what it sees (the card and
// what it hears, the port and its link) and decides the cases no person can change: no
// interface, no driver. Never a pass: supported needs the verified transfer, which only the
// hands-on tests run (WifiJoined, EthernetOver), so a hands-on result always replaces this one.

// WifiCard is the Mac's own Wi-Fi card as the Wi-Fi check finds it: its chip, PCI ID and driver,
// and its wireless interface. Without an interface, Decided is the result that decides
// network.wifi already: failed (no driver, or its firmware didn't load), or not_tested
// (live-limit) for a chip whose driver comes only with the installed system.
type WifiCard struct {
	Chip, PCI, Driver string // "BCM43224", "14e4:4353", "brcmsmac"
	Iface             string // "wlan0", or ""
	Shown             string // Iface as the report shows it (network.Shown)
	Decided           *Result
}

// Evidence is the card as the report names it: "BCM43224 (14e4:4353) on brcmsmac, wlan0".
func (c WifiCard) Evidence() string {
	return fmt.Sprintf("%s (%s) on %s, %s", c.Chip, c.PCI, c.Driver, c.Shown)
}

// FindWifi finds the Mac's own Wi-Fi card and its interface: by its PCI slot, else the first
// wireless interface not on USB. False when the Mac shows no Wi-Fi card at all.
func FindWifi(e *Env) (WifiCard, bool) {
	devs := wifiDevices(e)
	if len(devs) == 0 {
		return WifiCard{}, false
	}
	dev := devs[0]
	c := WifiCard{Chip: WifiChips[dev.ID()], PCI: dev.ID(), Driver: dev.Driver}
	if c.Chip == "" {
		c.Chip = dev.ID()
	}
	var w *netIface
	for _, n := range ifaces(e) {
		if n.Wifi && (n.Slot != "" && n.Slot == dev.Slot || w == nil && !n.USB) {
			nn := n
			w = &nn
		}
	}
	if w == nil {
		var r Result
		if noLiveDriver[dev.ID()] {
			r = skip(LiveLimit, fmt.Sprintf("%s (%s): its driver (broadcom-wl) comes only with the installed system", c.Chip, dev.ID()))
			r.Detail = i18n.T("%s: driver only when installed", c.Chip)
		} else {
			r = fail(fmt.Sprintf("%s (%s, %s): no wireless interface (driver missing or firmware didn't load)", c.Chip, dev.ID(), orNone(dev.Driver)))
			r.Detail = i18n.T("%s: no wireless interface", c.Chip)
		}
		r.ID = "network.wifi"
		c.Decided = &r
		return c, true
	}
	c.Driver, c.Iface, c.Shown = w.Driver, w.Name, w.shown()
	return c, true
}

// wifi looks at network.wifi, "Wi-Fi", in the quick pass: the card, its interface and what a
// scan hears. Hearing networks is only the start: a scan works on many a card that can't join
// or stay joined (older Broadcom chips), so it never passes (supported means exercised end to end);
// the hands-on test joins and moves the data, and its result replaces
// this one.
//
//   - not_tested (uncertain): an interface; the scan heard networks, or none (nothing in range,
//     or a deaf radio): the hands-on test decides;
//   - not_tested (live-limit): BCM4331/BCM4360 without a driver: theirs comes only with the
//     installed system;
//   - failed: no wireless interface for the card (no driver, or its firmware didn't load), or
//     the scan itself failed.
//
// The joined network's name never goes into the report.
func wifi(ctx context.Context, e *Env) []Result {
	c, _ := FindWifi(e)
	if c.Decided != nil {
		return []Result{*c.Decided}
	}
	ev := c.Evidence()
	n, serr := e.Net.Networks, e.Net.ScanErr
	if !e.Net.Scanned && e.Net.Scan != nil {
		var err error
		n, err = e.Net.Scan(ctx)
		if err != nil {
			serr = err.Error()
		}
	}
	var r Result
	switch {
	case serr != "" && n == 0:
		r = fail(ev + ": scan failed: " + serr)
		r.Detail = i18n.T("%s, %s, scan failed", c.Chip, c.Driver)
	case e.Net.Joined != "" && e.Net.JoinedIface == c.Iface:
		r = skip(Uncertain, ev+": joined a network on the Network screen; the hands-on Wi-Fi test moves data over it and decides")
		r.Detail = i18n.T("%s, %s, joined; the hands-on test decides", c.Chip, c.Driver)
	case n == 0:
		r = skip(Uncertain, ev+": the scan found no networks (none in range, or the radio hears nothing); the hands-on Wi-Fi test decides")
		r.Detail = i18n.T("%s, %s, no networks heard", c.Chip, c.Driver)
	default:
		r = skip(Uncertain, ev+fmt.Sprintf(": scanned %d networks; the hands-on Wi-Fi test joins one, moves data over it and decides", n))
		r.Detail = i18n.N("%s, %d network heard; the hands-on test decides", "%s, %d networks heard; the hands-on test decides", n, c.Chip, n)
	}
	r.Params = boot.Params{Count: n}
	return []Result{r}
}

// WifiJoined decides network.wifi, "Wi-Fi", once the hands-on test joined a network on iface
// and got an address there: the Mac's own card joins a network and carries data, every byte
// checked both ways by the boot.watch test over iface, bound to it (carried). networks is how
// many the scan heard (0: unknown).
//
//   - supported: iface is the Mac's own card's (not a USB dongle), at least 256 KB down and
//     64 KB up verified over it, and its counters agree;
//   - partial: it still has its address, but it didn't carry the test while boot.watch answers
//     (over it before it broke, or over the run's own connection), or it's the only connection
//     and nothing worked over it: the card joins and doesn't carry data;
//   - not_tested (uncertain): joined on another interface (a USB dongle); joined, then left
//     before the test was done; the network wants a sign-in page; no test server; its counters
//     can't be read or disagree;
//   - not_tested (other): boot.watch didn't answer over any connection or turned the test away,
//     or the Mac's clock is off: not the card's fault.
//
// The joined network's name never goes into the report.
func WifiJoined(ctx context.Context, e *Env, c WifiCard, iface string, networks int) Result {
	if c.Decided != nil {
		return *c.Decided
	}
	ev := c.Evidence()
	if iface != c.Iface {
		r := skip(Uncertain, ev+": the network was joined on "+network.Shown(iface, network.WiFi)+", not the Mac's own card, so not tested")
		r.ID, r.Detail = "network.wifi", i18n.T("joined on another adapter, not %s", c.Chip)
		return r
	}
	if networks > 0 {
		ev += fmt.Sprintf(": scan found %d networks; joined one and got an address", networks)
	} else {
		ev += ": joined a network and got an address"
	}
	k, how := carried(ctx, e, c.Iface)
	ev += "; " + how
	var r Result
	if reason, words, ok := undecided(k); ok {
		r = skip(reason, ev+", so not tested")
		r.Detail = i18n.T("%s joined, %s", c.Chip, words)
	} else {
		switch {
		case k == carryOK:
			r = pass(ev)
			r.Detail = i18n.T("%s joined, data verified both ways", c.Chip)
		case addr(e, c.Iface) == "":
			r = skip(Uncertain, ev+"; then it left the network, so not tested")
			r.Detail = i18n.T("%s joined, left before data moved", c.Chip)
		default: // carryBroken, or carryNothing: a card that joins and moves nothing is partial
			r = part(ev)
			r.Detail = i18n.T("%s joined, data didn't move", c.Chip)
		}
	}
	r.ID = "network.wifi"
	return r
}

// EthPort is the Mac's own Ethernet port as the Ethernet check finds it: its controller's PCI
// ID and driver, and its interface. Without an interface, Decided is the failed result that
// decides network.ethernet already (no driver bound, or no interface for it).
type EthPort struct {
	PCI, Driver string
	Iface       string // "enp2s0f0", or ""
	Shown       string // Iface as the report shows it
	Decided     *Result
}

// Evidence is the port as the report names it: "14e4:16b4 (tg3, enp2s0f0)".
func (p EthPort) Evidence() string { return fmt.Sprintf("%s (%s, %s)", p.PCI, p.Driver, p.Shown) }

// FindEthernet finds the Mac's own Ethernet port and its interface (an adapter on Thunderbolt or
// USB isn't the Mac's port). False when the Mac shows no Ethernet controller of its own.
func FindEthernet(e *Env) (EthPort, bool) {
	devs := ethernetDevices(e)
	if len(devs) == 0 {
		return EthPort{}, false
	}
	dev := devs[0]
	p := EthPort{PCI: dev.ID(), Driver: dev.Driver}
	for _, n := range ifaces(e) {
		if n.Slot == dev.Slot {
			p.Driver, p.Iface, p.Shown = n.Driver, n.Name, n.shown()
		}
	}
	if p.Iface != "" {
		return p, true
	}
	r := fail(fmt.Sprintf("%s: %s bound but no network interface", dev.ID(), dev.Driver))
	r.Detail = i18n.T("%s bound, no interface", dev.Driver)
	if dev.Driver == "" {
		r = fail(dev.ID() + ": no driver bound")
		r.Detail = i18n.T("%s: no driver", dev.ID())
	}
	r.ID = "network.ethernet"
	p.Decided = &r
	return p, true
}

// ethernet looks at network.ethernet, "Ethernet", in the quick pass: the port's driver, its
// interface and its link. A link alone isn't a working port: the hands-on test
// waits for a cable, a link and an address and moves the data, and its result replaces this one.
//
//   - not_tested (uncertain): a link (a cable in): the hands-on test decides;
//   - not_tested (no-equipment): no cable plugged in, so no link to see;
//   - failed: no driver bound, or no interface for the port.
func ethernet(_ context.Context, e *Env) []Result {
	p, _ := FindEthernet(e)
	if p.Decided != nil {
		return []Result{*p.Decided}
	}
	carrier := e.Root.Read("/sys/class/net/"+p.Iface+"/carrier") == "1"
	if !carrier {
		r := skip(NoEquipment, p.Evidence()+": no link: no cable plugged in; the hands-on Ethernet test decides")
		r.Detail = i18n.T("%s, no cable", p.Driver)
		return []Result{r}
	}
	ev := p.Evidence() + ": link up"
	if addr(e, p.Iface) != "" {
		ev += ", an address"
	}
	r := skip(Uncertain, ev+"; the hands-on Ethernet test moves data over it and decides")
	r.Detail = i18n.T("%s, link up; the hands-on test decides", p.Driver)
	return []Result{r}
}

// EthernetOver decides network.ethernet, "Ethernet", once the hands-on test saw a link on the
// port (a cable in): the Mac's Ethernet port has a working driver, a link comes up with a cable
// in, and it carries data: an address, and the boot.watch test over the port's interface, bound
// to it (carried): every byte checked, both ways.
//
//   - supported: an IPv4 address on the port's interface, at least 256 KB down and 64 KB up
//     verified over it, and its counters agree;
//   - partial: an address, but it didn't carry the test while boot.watch answers (over it
//     before it broke, or over the run's own connection);
//   - not_tested (uncertain): a link but no address (no DHCP answer); it's the only connection
//     and nothing worked over it (a network with no way out, maybe); the network wants a
//     sign-in page; no test server; its counters can't be read or disagree: presence alone
//     never passes;
//   - not_tested (other): boot.watch didn't answer over any connection or turned the test away,
//     or the Mac's clock is off: not the port's fault.
func EthernetOver(ctx context.Context, e *Env, p EthPort) Result {
	if p.Decided != nil {
		return *p.Decided
	}
	r := ethernetOver(ctx, e, p)
	r.ID = "network.ethernet"
	return r
}

func ethernetOver(ctx context.Context, e *Env, p EthPort) Result {
	ev := p.Evidence() + ": link up"
	if addr(e, p.Iface) == "" {
		r := skip(Uncertain, ev+", but no IPv4 address on it (no DHCP answer), so no data could move: a link alone isn't tested")
		r.Detail = i18n.T("%s, link up, no address", p.Driver)
		return r
	}
	ev += ", an address"
	if e.Net.Online && e.Net.Iface == p.Iface {
		ev += ", online through it"
	}
	k, how := carried(ctx, e, p.Iface)
	ev += "; " + how
	if reason, words, ok := undecided(k); ok {
		r := skip(reason, ev+", so not tested")
		r.Detail = i18n.T("%s, link up, %s", p.Driver, words)
		return r
	}
	switch k {
	case carryOK:
		r := pass(ev)
		r.Detail = i18n.T("%s, data verified both ways", p.Driver)
		return r
	case carryBroken:
		r := part(ev)
		r.Detail = i18n.T("%s, link up, data doesn't flow", p.Driver)
		return r
	}
	r := skip(Uncertain, ev+", so not tested") // carryNothing: a network with no way out, maybe
	r.Detail = i18n.T("%s, link up, data didn't move", p.Driver)
	return r
}
