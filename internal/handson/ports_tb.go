package handson

import (
	"fmt"
	"path"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/omaboot/boot-live/internal/boot"
	"github.com/omaboot/boot-live/internal/checks"
	"github.com/omaboot/boot-live/internal/i18n"
)

// ports.thunderbolt, "Thunderbolt devices (storage, docks, hotplug)": the person plugs in a
// Thunderbolt device (a dock, a drive, an adapter, an Apple Thunderbolt Display); the machine
// watches the Thunderbolt bus (/sys/bus/thunderbolt) and the PCI devices (method automatic).
// On Macs the kernel's own connection manager does what macOS does: Apple's firmware only sets
// up devices present at power-on (research/findings/thunderbolt-hotplug-macs.md), so a device
// plugged in later works only if Linux finds it, authorizes it and tunnels its PCIe through. A
// device's PCI functions appearing is the tunnel working, and its endpoint (not just its
// switch's bridges) is the device's insides answering.
//
// The kernel may hold a new device back until userspace approves it (domain security "user"
// or "secure"): an installed Omarchy has bolt for that, the live image doesn't. When a device
// stays unauthorized for a few seconds, the person can let it in ("Let it in"), which writes 1
// to its "authorized" attribute, as boltctl authorize does: the kernel's approval flag, nothing
// on the device itself, and the evidence says so. Nothing else is written. An approval can't
// be taken back by the test; unplugging the device ends it.
//
//   - supported: a device appeared on the Thunderbolt bus after the screen opened (hotplug),
//     authorized, and a new PCI device other than a bridge (what's inside it: a network chip, a
//     disk controller, a dock's USB) appeared behind it;
//   - partial: it appeared but stayed unauthorized (the kernel refused to let it in; a device
//     held back waits for Let it in, with no time limit: linux-omarchy 7.2.8 holds even an
//     Apple adapter back, 7.2.5 didn't), or
//     brought no PCI device but bridges (the
//     tunnel came up, the device behind it didn't), within 20 s; or a
//     device that was in since power-on didn't come back after the tester unplugged and
//     replugged it ("It didn't come back", observed: works at power-on, not hotplug);
//   - failed: the tester plugged a device in and nothing appeared on the bus ("Nothing shows
//     up", observed, after 15 s), with whether the Mac has a Thunderbolt domain at all;
//   - not_tested (no-equipment): skipped.
//
// With the Mac's port map (portmap.go) the test asks for the Thunderbolt ports (Thunderbolt 1/2
// and Thunderbolt 3 sockets) one at a time, ringed on the map, and takes whatever device comes
// for the port asked for: each port gets the rule above on its own (supported: seen; partial:
// part; "Nothing shows up": dead), the device is unplugged before the next, and once every port
// has its state the test decides: supported when every port worked, failed when none did,
// partial otherwise. The evidence records which bus route each port gave (right-3 = 0-1).
//
// When the run's own connection is on the Thunderbolt device (a Thunderbolt to Gigabit Ethernet
// adapter carrying it, unplugged and plugged back in for the test), the result waits for the
// network to come back through it before it's handed over (Continue comes with it), as the
// status line has the network (IO.NetNow): up to tbNetWait seconds, then it goes on with a line
// saying it didn't come back; "Skip this one" stops the wait and keeps the result (owner, 0.6.0
// review). The connection is on the device when its interface's PCI device went away during the
// test, or is one of the PCI devices that came in behind a Thunderbolt device. Nothing is
// written for this: it only reads /sys/class/net and the status line's network.
//
// A device in since power-on isn't a hotplug test: the person is asked to unplug it and plug
// it back in. A plain Mini DisplayPort monitor in a Thunderbolt 1/2 port is a display, not a
// Thunderbolt device (the criterion is devices: storage, docks, hotplug): it shows up as a
// DisplayPort connector, the screen says so, and it isn't counted. An Apple Thunderbolt Display
// is a Thunderbolt device and counts.

func init() {
	Register(Spec{ID: "ports.thunderbolt", Group: Plug, Order: 70, Title: i18n.M("Thunderbolt"),
		Brief: i18n.M("plug in a Thunderbolt device"), Gear: i18n.M("a Thunderbolt device or display"), Expect: time.Minute,
		New: func(*Env) Run { return &thunderbolt{} }, Demo: func(f *Fake) {
			tb := "/sys/bus/thunderbolt/devices/"
			f.Set(tb+"domain0/security", "none")
			f.Set(tb+"0-0/device_name", "MacBook Air")
			f.Set(tb+"0-0/vendor_name", "Apple Inc.")
			f.Set(tb+"0-0/authorized", "1")
			f.Set(tb+"0-0/generation", "1")
			for _, p := range []string{"0000:00:00.0", "0000:00:02.0", "0000:00:14.0", "0000:00:1c.4", "0000:06:00.0", "0000:07:00.0"} {
				f.Set("/sys/bus/pci/devices/"+p+"/vendor", "0x8086")
			}
			f.At(4, func(f *Fake) {
				f.Set(tb+"0-1/device_name", "Thunderbolt to Gigabit Ethernet Adapter")
				f.Set(tb+"0-1/vendor_name", "Apple, Inc.")
				f.Set(tb+"0-1/authorized", "1")
				f.Set(tb+"0-1/rx_speed", "10.0 Gb/s")
				f.Set(tb+"0-1/rx_lanes", "1")
			})
			f.At(5.5, func(f *Fake) {
				for _, p := range []string{"0000:08:00.0", "0000:09:00.0"} {
					f.Set("/sys/bus/pci/devices/"+p+"/vendor", "0x8086")
					f.Set("/sys/bus/pci/devices/"+p+"/device", "0x1547")
					f.Set("/sys/bus/pci/devices/"+p+"/class", "0x060400")
					FakeLink(f, "/sys/bus/pci/devices/"+p+"/driver", "../../../bus/pci/drivers/pcieport")
				}
				p := "/sys/bus/pci/devices/0000:0a:00.0/"
				f.Set(p+"vendor", "0x14e4")
				f.Set(p+"device", "0x1682")
				f.Set(p+"class", "0x020000")
				FakeLink(f, p+"driver", "../../../bus/pci/drivers/tg3")
			})
		}})
}

const tbBus = "/sys/bus/thunderbolt/devices/"

// tbName: a Thunderbolt switch (0-0 the host, 0-1, 0-301…), not a domain, port or service.
var tbName = regexp.MustCompile(`^\d+-[0-9a-f]+$`)

// tbDev is one Thunderbolt switch as read now.
type tbDev struct {
	Name, Device, Vendor string
	Auth                 string // authorized: 0, 1 (2: secure), "" when the kernel doesn't say
	Gen                  string // generation (newer kernels)
	Speed, Lanes         string // rx_speed, rx_lanes (newer kernels)
}

func (d tbDev) host() bool   { return strings.HasSuffix(d.Name, "-0") }
func (d tbDev) authed() bool { return d.Auth == "" || d.Auth == "1" || d.Auth == "2" }
func (d tbDev) label() string {
	if l := strings.TrimSpace(d.Vendor + " " + d.Device); l != "" {
		return l
	}
	return d.Name
}

// readTB lists the Thunderbolt switches now, by name.
func readTB(io IO) map[string]tbDev {
	out := map[string]tbDev{}
	for _, p := range io.Glob(tbBus + "*") {
		n := path.Base(p)
		if !tbName.MatchString(n) {
			continue
		}
		out[n] = tbDev{Name: n, Device: io.Read(p + "/device_name"), Vendor: io.Read(p + "/vendor_name"), Auth: io.Read(p + "/authorized"),
			Gen: io.Read(p + "/generation"), Speed: io.Read(p + "/rx_speed"), Lanes: io.Read(p + "/rx_lanes")}
	}
	return out
}

func pciSet(io IO) map[string]bool {
	out := map[string]bool{}
	for _, p := range io.Glob("/sys/bus/pci/devices/*") {
		out[path.Base(p)] = true
	}
	return out
}

// pciLabel is a PCI device for the evidence: "0000:0a:00.0 [14e4:1682] tg3".
func pciLabel(io IO, p string) string {
	d := "/sys/bus/pci/devices/" + p
	s := fmt.Sprintf("%s [%s:%s]", p, strings.TrimPrefix(io.Read(d+"/vendor"), "0x"), strings.TrimPrefix(io.Read(d+"/device"), "0x"))
	if drv := link(io, d+"/driver"); drv != "" {
		s += " " + path.Base(drv)
	}
	return s
}

type thunderbolt struct {
	start   map[string]tbDev
	left    map[string]float64 // devices in since power-on that were unplugged: when
	host    *tbDev
	domain  bool
	sec     string
	dev     *tbDev          // the device that came in
	devAt   float64         // when it came (or was let in: the 20 s count from then; held back, no count)
	letIn   string          // the tester let it in: "" not asked, "ok", or the kernel's error
	base    map[string]bool // PCI devices just before it came
	prevPCI map[string]bool
	newPCI  []string // PCI devices that appeared since it came
	ends    int      // of those, the ones that aren't bridges: what's inside the device
	pciAt   float64  // when the first of those appeared
	dp0     map[string]string
	display string // a DisplayPort connector that came up without a Thunderbolt device
	dispAt  float64
	polled  float64
	gd      *guide            // the port map's guided order, nil without a map (one device decides then)
	used    map[string]bool   // devices that already gave a port its state: not counted again until unplugged
	askAt   float64           // when the port asked for was asked for
	res     map[string]tbPort // by port ID: what each port gave
	marked  bool              // the last thing that happened was the tester marking a port
	markAt  float64

	// the run's connection, when it may be on the Thunderbolt device (see above)
	netIface string          // its interface when the test opened ("en1"), or ""
	netPCI   string          // that interface's PCI device then, or "" (none, or not on PCI)
	netGone  bool            // the interface went away during the test (its device unplugged)
	tbPCI    map[string]bool // every PCI device that came in behind a Thunderbolt device
	lastName string          // the last Thunderbolt device seen, for the wait's line
	held     *Outcome        // the result, held until the network is back through the device
	heldAt   float64
	netPoll  float64
	netLine  *Line // what the wait came to, once over
}

// tbNetWait is how long a result waits for the run's network to come back through the device.
const tbNetWait = 30.0

// pciAddr is a PCI device's name in sysfs, "0000:0a:00.0".
var pciAddr = regexp.MustCompile(`^[0-9a-f]{4}:[0-9a-f]{2}:[0-9a-f]{2}\.[0-7]$`)

// ifacePCI is the PCI device a network interface is on, "" when it's gone or not on PCI (a USB
// adapter's is a USB interface).
func ifacePCI(io IO, iface string) string {
	if p := path.Base(link(io, "/sys/class/net/"+iface+"/device")); pciAddr.MatchString(p) {
		return p
	}
	return ""
}

// viaIface is the interface of a connection as Env.Online and Net.Via write it ("en1, Ethernet").
func viaIface(v string) string {
	i, _, _ := strings.Cut(v, ", ")
	return i
}

// onTB: the run's connection is on a Thunderbolt device (see above).
func (x *thunderbolt) onTB(io IO) bool {
	return x.netIface != "" && (x.netGone || x.tbPCI[ifacePCI(io, x.netIface)])
}

// netBack: the run is online again, as the status line has it, through the device's interface
// (the same name, or one on a PCI device that came in behind it).
func (x *thunderbolt) netBack(io IO) bool {
	n := io.NetNow()
	if !n.Up {
		return false
	}
	i := viaIface(n.Via)
	return i == x.netIface || x.tbPCI[ifacePCI(io, i)]
}

// adapter names the device the connection is on, for the wait's line.
func (x *thunderbolt) adapter() string {
	if x.lastName != "" {
		return x.lastName
	}
	return i18n.T("the Thunderbolt device")
}

// conclude hands the outcome over, or holds it while the run's connection, on the Thunderbolt
// device, isn't back through it.
func (x *thunderbolt) conclude(c *Ctx, o Outcome) {
	if x.netLine == nil && x.onTB(c.IO) && !x.netBack(c.IO) {
		x.held, x.heldAt = &o, c.T
		c.Select(-1)
		return
	}
	c.Decide(o)
}

// release hands the held outcome over, the wait's word added to its evidence and to What I see.
func (x *thunderbolt) release(c *Ctx, line Line, ev string) {
	o := *x.held
	o.Results = append([]checks.Result(nil), o.Results...)
	for i := range o.Results {
		o.Results[i].Evidence += "; " + ev
	}
	x.netLine, x.held = &line, nil
	c.Decide(o)
}

// stepHeld waits for the network while a result is held.
func (x *thunderbolt) stepHeld(c *Ctx) {
	if c.T-x.netPoll < 0.5 {
		return
	}
	x.netPoll = c.T
	switch {
	case x.netBack(c.IO):
		x.release(c, Fact(i18n.T("the network is back through %s", x.adapter())),
			fmt.Sprintf("the run's connection (%s) was on this device: online through it again %.0f s after the result", x.netIface, c.T-x.heldAt))
	case c.T-x.heldAt >= tbNetWait:
		x.release(c, Bad(i18n.T("the network didn't come back through %s in %.0f s", x.adapter(), tbNetWait)),
			fmt.Sprintf("the run's connection (%s) was on this device and didn't come back through it within %.0f s", x.netIface, tbNetWait))
	}
}

// Skip during the wait stops it and keeps the result (Skipper); otherwise it skips the test.
func (x *thunderbolt) Skip(c *Ctx) bool {
	if x.held == nil {
		return false
	}
	x.release(c, Gone(i18n.T("didn't wait for the network to come back")),
		fmt.Sprintf("the run's connection (%s) was on this device; the tester went on without waiting for it (%.0f s)", x.netIface, c.T-x.heldAt))
	return true
}

// tbRate is a link speed as the kernel writes it, without a needless decimal: "20.0 Gb/s" →
// "20 Gb/s".
func tbRate(s string) string { return strings.Replace(s, ".0 Gb/s", " Gb/s", 1) }

// tbPort is what one port of the map gave.
// PortAnswers are the Thunderbolt ports' answers for the wiring (porter).
func (x *thunderbolt) PortAnswers(*Ctx) []checks.PortAnswer { return x.gd.PortAnswers() }

type tbPort struct {
	r    checks.Result
	say  string
	line string // What I see's line for it (translated)
}

// tbNoteW is the widest note a Thunderbolt port can show on the map.
func tbNoteW() int {
	w := 0
	for _, n := range []string{seenNote(i18n.T("works")), seenNote("10 Gb/s"), seenNote("20 Gb/s"), seenNote("40 Gb/s"),
		i18n.T("~ not let in"), i18n.T("~ no devices"), i18n.T("× dead")} {
		w = max(w, width(n))
	}
	return w
}

func (x *thunderbolt) Start(c *Ctx) {
	c.Select(-1) // waiting for a device: a stray Enter answers nothing
	x.start, x.left = readTB(c.IO), map[string]float64{}
	for _, d := range x.start {
		if d.host() && x.host == nil {
			d := d
			x.host = &d
		}
	}
	if ds := c.IO.Glob(tbBus + "domain*"); len(ds) > 0 {
		x.domain, x.sec = true, c.IO.Read(ds[0]+"/security")
	}
	x.prevPCI = pciSet(c.IO)
	x.gd, x.used, x.res = newGuide(c.Env, tbNoteW(), "thunderbolt", "thunderbolt3"), map[string]bool{}, map[string]tbPort{}
	x.dp0 = map[string]string{}
	for _, k := range externalConnectors(c.IO) {
		x.dp0[k.name] = k.status
	}
	x.tbPCI = map[string]bool{}
	if n := c.IO.NetNow(); n.Up {
		x.netIface = viaIface(n.Via)
	} else if c.Env != nil {
		x.netIface = viaIface(c.Env.Online)
	}
	if x.netIface != "" {
		x.netPCI = ifacePCI(c.IO, x.netIface)
	}
}

// atBoot are the devices in since power-on and still in, by name.
func (x *thunderbolt) atBoot() []tbDev {
	var out []tbDev
	for _, d := range x.start {
		if _, gone := x.left[d.Name]; !d.host() && !gone {
			out = append(out, d)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func (x *thunderbolt) Step(c *Ctx) {
	if x.held != nil {
		x.stepHeld(c)
		return
	}
	if c.T-x.polled < 0.5 {
		return
	}
	x.polled = c.T
	if x.netPCI != "" && len(c.IO.Glob("/sys/class/net/"+x.netIface)) == 0 {
		x.netGone = true
	}
	now, pci := readTB(c.IO), pciSet(c.IO)
	for n, d := range x.start {
		if _, in := now[n]; !in && !d.host() {
			if _, was := x.left[n]; !was {
				x.left[n] = c.T
			}
		}
	}
	for n := range x.used {
		if _, in := now[n]; !in {
			delete(x.used, n)
		}
	}
	if x.dev == nil {
		var names []string
		for n, d := range now {
			_, was := x.start[n]
			_, back := x.left[n]
			if !d.host() && (!was || back) && !x.used[n] {
				names = append(names, n)
			}
		}
		if len(names) > 0 {
			sort.Strings(names)
			d := now[names[0]]
			x.dev, x.devAt, x.base = &d, c.T, x.prevPCI
		}
	} else if d, ok := now[x.dev.Name]; ok {
		x.dev = &d
	} else { // unplugged again: wait for the next one
		x.dev, x.newPCI, x.ends, x.letIn = nil, nil, 0, ""
	}
	x.prevPCI = pci
	if x.dev == nil {
		for _, k := range externalConnectors(c.IO) {
			if k.status == "connected" && x.dp0[k.name] != "connected" && strings.HasPrefix(k.name, "DP") && x.display != k.name {
				x.display, x.dispAt = k.name, c.T
			}
		}
		return
	}
	var fresh []string
	for p := range pci {
		if !x.base[p] {
			fresh = append(fresh, p)
		}
	}
	sort.Strings(fresh)
	ends := 0
	for _, p := range fresh {
		if !strings.HasPrefix(c.IO.Read("/sys/bus/pci/devices/"+p+"/class"), "0x0604") {
			ends++
		}
	}
	if ends > 0 && x.ends == 0 {
		x.pciAt = c.T
	}
	x.newPCI, x.ends = fresh, ends
	for _, p := range fresh {
		x.tbPCI[p] = true
	}
	if x.lastName = x.dev.Device; x.lastName == "" {
		x.lastName = x.dev.label()
	}
	switch {
	case x.dev.authed() && x.ends > 0 && c.T-x.pciAt >= 1.5:
		x.decide(c, checks.Supported)
	case c.T-x.devAt >= 20 && (x.dev.authed() || x.letIn != ""): // held back, it waits for Let it in (owner, 0.8.0 review)
		x.decide(c, checks.Partial)
	}
}

func (x *thunderbolt) hostText() string {
	if x.host == nil {
		if x.domain {
			return "a Thunderbolt domain but no host router"
		}
		return "no Thunderbolt domain (the controller isn't driven, or is powered off)"
	}
	s := "host " + x.host.label()
	if x.host.Gen != "" {
		s += ", Thunderbolt " + x.host.Gen
	}
	if x.sec != "" {
		s += ", security " + x.sec
	}
	return s
}

func (x *thunderbolt) devText(io IO) string {
	d := x.dev
	s := fmt.Sprintf("%s: %s, authorized %s", d.Name, d.label(), orQ(d.Auth))
	if d.Speed != "" {
		s += ", link " + d.Speed
		if d.Lanes != "" {
			s += " x" + d.Lanes
		}
	}
	if _, back := x.left[d.Name]; back {
		s += "; in since power-on, unplugged and plugged back in"
	} else {
		s += "; plugged in after boot"
	}
	switch x.letIn {
	case "ok":
		s += "; the kernel held it back and the tester let it in (authorized written, as boltctl authorize does)"
	case "":
	default:
		s += "; the tester tried to let it in and the kernel refused: " + x.letIn
	}
	if len(x.newPCI) > 0 {
		var ps []string
		for _, p := range x.newPCI {
			ps = append(ps, pciLabel(io, p))
		}
		s += "; new PCI devices: " + strings.Join(ps, ", ")
	} else {
		s += "; no new PCI devices"
	}
	return s
}

func orQ(s string) string {
	if s == "" {
		return "?"
	}
	return s
}

func (x *thunderbolt) decide(c *Ctx, status string) {
	ev := x.hostText() + "; " + x.devText(c.IO)
	d := x.dev
	var r checks.Result
	var say string
	switch {
	case status == checks.Supported:
		r = Result("ports.thunderbolt", checks.Supported, Automatic, ev, i18n.N("%[2]s, %[1]d PCI device behind it", "%[2]s, %[1]d PCI devices behind it", x.ends, x.ends, d.Name))
		say = i18n.T("hot-plugged, authorized, tunnelled. thunderbolt works.")
	case !d.authed():
		r = Result("ports.thunderbolt", checks.Partial, Automatic, ev+"; it stayed unauthorized for 20 s after Let it in", i18n.T("%s seen, not authorized", d.Name))
		say = i18n.T("the bus sees it, and the kernel wouldn't let it in. partial.")
	default:
		r = Result("ports.thunderbolt", checks.Partial, Automatic, ev+"; no PCI device (other than bridges) came up behind it in 20 s", i18n.T("%s seen, no PCI devices", d.Name))
		say = i18n.T("it's on the bus, and nothing came through it. partial.")
	}
	if x.gd == nil {
		x.conclude(c, Outcome{Results: []checks.Result{r}, Say: say})
		return
	}
	nx := x.gd.next()
	if nx == nil {
		return
	}
	st, note := PortSeen, seenNote(i18n.T("works"))
	if d.Speed != "" {
		note = seenNote(tbRate(d.Speed))
	}
	line := i18n.T("%[1]s · authorized", d.label())
	switch {
	case status == checks.Supported:
		if d.Speed != "" {
			line = i18n.T("%[1]s · authorized · %[2]s", d.label(), tbRate(d.Speed))
		}
	case !d.authed():
		st, note, line = PortPart, i18n.T("~ not let in"), i18n.T("%[1]s · not authorized", d.label())
	default:
		st, note, line = PortPart, i18n.T("~ no devices"), i18n.T("%[1]s · no PCI devices behind it", d.label())
	}
	x.gd.set(nx.ID, st, note, c.T)
	x.gd.match[nx.ID] = "route " + d.Name
	x.gd.catch(nx.ID, "ring", "", "tb:"+d.Name)
	x.res[nx.ID] = tbPort{r: r, say: say, line: line}
	if x.gd.done() { // the device stays on screen with the result
		x.finish(c)
		return
	}
	x.used[d.Name] = true
	delete(x.start, d.Name) // in since power-on and replugged: from now on a device like any other
	delete(x.left, d.Name)
	x.dev, x.newPCI, x.ends, x.letIn, x.askAt, x.marked = nil, nil, 0, "", c.T, false
}

// finish decides once every port of the map has its state: one port's own result, or for
// several, supported when every port worked, failed when none did, partial otherwise.
func (x *thunderbolt) finish(c *Ctx) {
	gd := x.gd
	if !gd.done() {
		return
	}
	if len(gd.ports) == 1 {
		p := x.res[gd.ports[0].ID]
		p.r.Evidence += "; " + gd.evidence()
		x.conclude(c, Outcome{Results: []checks.Result{p.r}, Say: p.say})
		return
	}
	var ev []string
	for _, p := range gd.ports {
		ev = append(ev, p.ID+": "+strings.TrimPrefix(x.res[p.ID].r.Evidence, x.hostText()+"; "))
	}
	evidence := x.hostText() + "; " + strings.Join(ev, "; ") + "; " + gd.evidence()
	n, ok, dead := len(gd.ports), gd.count(PortSeen), gd.count(PortDead)
	var r checks.Result
	var say string
	switch {
	case ok == n:
		r = Result("ports.thunderbolt", checks.Supported, Automatic, evidence, i18n.N("%d port works, hot-plugged", "all %d ports work, hot-plugged", n, n))
		say = i18n.T("every port hot-plugged and tunnelled. thunderbolt works.")
	case dead == n:
		r = Result("ports.thunderbolt", checks.Failed, Observed, evidence, i18n.T("nothing appeared on the bus"))
		say = i18n.T("plugged in, and the thunderbolt bus stayed empty. that's a fail.")
	default:
		m := Automatic
		if dead > 0 {
			m = Observed
		}
		r = Result("ports.thunderbolt", checks.Partial, m, evidence, i18n.T("%[1]d of %[2]d ports work", ok, n))
		say = i18n.T("not every port came through. partial.")
	}
	x.conclude(c, Outcome{Results: []checks.Result{r}, Say: say})
}

// waitingForReplug: a device was in since power-on, it's been unplugged, nothing came back yet.
func (x *thunderbolt) waitingForReplug() (string, float64, bool) {
	var ns []string
	for n := range x.left {
		ns = append(ns, n)
	}
	if len(ns) == 0 {
		return "", 0, false
	}
	sort.Strings(ns)
	return ns[0], x.left[ns[0]], true
}

// canLetIn: the device has been waiting unauthorized for a few seconds.
func (x *thunderbolt) canLetIn(c *Ctx) bool {
	return x.dev != nil && !x.dev.authed() && x.letIn == "" && c.T-x.devAt >= 3
}

func (x *thunderbolt) Act(c *Ctx, i int) {
	if i != 0 {
		return
	}
	if x.dev != nil {
		if x.canLetIn(c) {
			x.letIn, x.devAt = "ok", c.T
			if err := c.IO.Write(tbBus+x.dev.Name+"/authorized", "1"); err != nil {
				x.letIn = err.Error()
			}
		}
		return
	}
	if n, at, ok := x.waitingForReplug(); ok {
		if c.T-at < 15 {
			return
		}
		d := x.start[n]
		ev := fmt.Sprintf("%s; %s: %s, in since power-on, authorized %s; after the tester unplugged it and plugged it back in, it didn't come back (hotplug)",
			x.hostText(), d.Name, d.label(), orQ(d.Auth))
		x.conclude(c, Outcome{Results: []checks.Result{Result("ports.thunderbolt", checks.Partial, Observed, ev, i18n.T("works at power-on, not hotplug"))},
			Say: i18n.T("there at power-on, gone after a replug. partial.")})
		return
	}
	if len(x.atBoot()) > 0 || c.T-x.askAt < 15 {
		return
	}
	ev := x.hostText() + "; the tester plugged a Thunderbolt device in and nothing appeared on the Thunderbolt bus"
	if x.display != "" {
		ev += "; a display came up on " + x.display + " (DisplayPort, not counted)"
	}
	if x.host != nil && (x.host.Gen == "1" || x.host.Gen == "2") {
		ev += "; on Thunderbolt 1/2 Macs the firmware only sets up devices present at power-on, hotplug is up to Linux"
	}
	if x.gd != nil { // the port asked for: marked, and on to the next
		if nx := x.gd.next(); nx != nil {
			x.gd.set(nx.ID, PortDead, i18n.T("× dead"), c.T)
			x.res[nx.ID] = tbPort{r: Result("ports.thunderbolt", checks.Failed, Observed, ev, i18n.T("nothing appeared on the bus")),
				say: i18n.T("plugged in, and the thunderbolt bus stayed empty. that's a fail.")}
			x.askAt, x.marked, x.markAt = c.T, true, c.T
			x.finish(c)
		}
		return
	}
	x.conclude(c, Outcome{Results: []checks.Result{Result("ports.thunderbolt", checks.Failed, Observed, ev, i18n.T("nothing appeared on the bus"))},
		Say: i18n.T("plugged in, and the thunderbolt bus stayed empty. that's a fail.")})
}

func (x *thunderbolt) Stop(*Ctx) {}

// steps are "What to do" with the port map (prototype hoPortsAirTb, hoPortsImacTb): one port,
// or each in turn; the step to do now follows the ports done.
func (x *thunderbolt) steps() ([]string, int) {
	gd := x.gd
	if len(gd.ports) == 1 {
		st := 0
		if x.dev != nil {
			st = 1
		}
		return []string{i18n.T("Plug a Thunderbolt device into the port in the violet ring: a display, a dock or an adapter."),
			i18n.T("Wait for it to show up under What I see.")}, st
	}
	st := 0
	switch done := len(gd.ports) - len(gd.pending()); {
	case done > 0 && len(gd.pending()) == 1 && x.dev != nil:
		st = 2
	case done > 0:
		st = 1
	}
	return []string{i18n.T("Plug a Thunderbolt device into the port in the violet ring."), i18n.T("Move it to the next port in the ring."),
		i18n.T("Wait for it to show up under What I see.")}, st
}

func (x *thunderbolt) View(c *Ctx) Screen {
	s := x.view(c)
	switch {
	case x.held != nil: // the result is in; the network isn't back through the device yet
		s.Step, s.Actions, s.Face = len(s.Steps), nil, boot.Work
		s.Live = append(s.Live, Wait(i18n.T("waiting for the network to come back through %s", x.adapter())))
		s.Say, s.SayAt = i18n.T("got my answer. now the network has to come back through it."), x.heldAt
		s.Waiting = i18n.T("·  waiting for the network (up to %.0f s)", tbNetWait)
		s.SkipDoes = i18n.T("don't wait: the result stands")
		s.Hint = i18n.T("the adapter carries the network: leave it plugged in for a few seconds.")
	case x.netLine != nil:
		s.Live = append(s.Live, *x.netLine)
	}
	return s
}

func (x *thunderbolt) view(c *Ctx) Screen {
	s := Screen{Face: boot.Idle, OutTitle: i18n.T("What I see"), SkipDoes: i18n.T("goes in as not tested")}
	gd := x.gd
	old := x.host != nil && (x.host.Gen == "1" || x.host.Gen == "2")
	s.Happens = i18n.T("The device has to show up and be let in; its speed goes in the report.")
	if old {
		s.Happens = i18n.T("It has to show up and be let in. Thunderbolt 1 and 2 Macs may only see one in at power-on.")
	}
	if gd != nil {
		s.Steps, s.Step = x.steps()
		if len(gd.ports) > 1 {
			s.Happens = i18n.T("Each port has to see the device, let in; one device does for all of them.")
		}
	} else {
		s.Steps = []string{i18n.T("Plug a Thunderbolt device into a Thunderbolt port: a display, a dock or an adapter."),
			i18n.T("Wait for it to show up under What I see.")}
		if x.dev != nil {
			s.Step = 1
		}
	}
	s.Hint = i18n.T("a plain Mini DisplayPort monitor won't do: it needs a Thunderbolt device.")
	s.Waiting = i18n.T("·  waiting for a Thunderbolt device")
	// with a map, the host's line only until a port has a state or a device comes (What I see's
	// rows are few under a map)
	switch {
	case gd != nil && (len(x.res) > 0 || x.dev != nil):
	case x.host != nil:
		h := i18n.T("host: %s", x.host.label())
		if x.host.Gen != "" {
			h = i18n.T("host: %[1]s, Thunderbolt %[2]s", x.host.label(), x.host.Gen)
		}
		s.Live = append(s.Live, Fact(h))
	default:
		s.Live = append(s.Live, Gone(i18n.T("no Thunderbolt controller seen yet")))
	}
	if gd != nil {
		s.Map, s.SayRows = gd.view(false), portSayRows
		for _, p := range gd.ports {
			switch r, ok := x.res[p.ID]; {
			case !ok:
			case gd.state[p.ID] == PortDead:
				s.Live = append(s.Live, gd.line(p, i18n.T("marked as not working")))
			default:
				s.Live = append(s.Live, gd.line(p, r.line))
			}
		}
	}
	if d := x.dev; d != nil && gd != nil { // one line for the device, on the port asked for
		auth := i18n.T("not authorized yet")
		if d.authed() {
			auth = i18n.T("authorized")
		}
		if nx := gd.next(); nx != nil {
			s.Live = append(s.Live, gd.line(*nx, i18n.T("%[1]s · %[2]s", d.label(), auth)))
		}
	}
	if d := x.dev; d != nil {
		name := d.Device
		if name == "" {
			name = d.Name
		}
		s.Say, s.SayAt, s.Face = i18n.T("%s. on the bus. watching the tunnel…", name), x.devAt, boot.Work
		s.Waiting = i18n.T("·  waiting for what's inside it (up to 20 s)")
		if gd == nil {
			s.Live = append(s.Live, Fact(i18n.T("%[1]s: %[2]s", d.Name, d.label())))
			if d.authed() {
				s.Live = append(s.Live, Fact(i18n.T("authorized")))
			} else {
				s.Live = append(s.Live, Wait(i18n.T("not authorized yet")))
			}
		}
		if x.ends > 0 {
			s.Live = append(s.Live, Fact(i18n.N("%d new PCI device behind it", "%d new PCI devices behind it", x.ends, x.ends)))
		} else {
			s.Live = append(s.Live, Wait(i18n.T("waiting for its PCI devices")))
		}
		s.Actions = []Action{{Label: i18n.T("Let it in"), Does: i18n.T("approve it, as bolt would"), Off: !x.canLetIn(c)}}
		if !d.authed() && x.letIn == "" && c.T-x.devAt >= 3 {
			s.Steps = append(s.Steps[:s.Step:s.Step], i18n.T("Let it in: Linux holds new Thunderbolt devices back until someone approves them."))
			s.Happens = i18n.T("An installed Omarchy asks through bolt. Here, you approve it.")
			s.Say, s.SayAt, s.Face = i18n.T("it's waiting at the door. your call."), x.devAt+3, boot.Hmm
			s.Waiting = i18n.T("·  waiting for you to let it in")
		}
		return s
	}
	if n, at, ok := x.waitingForReplug(); ok {
		d := x.start[n]
		s.Steps = []string{i18n.T("Unplug %s.", d.label()), i18n.T("Plug it back in, same port or another Thunderbolt port.")}
		s.Step = 1
		s.Happens = i18n.T("Plugged in now, Linux has to set it up, not the firmware: the hotplug part.")
		s.Say, s.SayAt = i18n.T("unplugged. now back in: that's the hotplug part."), at
		s.Live = append(s.Live, Gone(i18n.T("%[1]s: %[2]s, unplugged", d.Name, d.label())), Wait(i18n.T("waiting for it to come back")))
		s.Actions = []Action{{Label: i18n.T("It didn't come back"), Does: i18n.T("it's back in: nothing here"), Off: c.T-at < 15}}
		s.Waiting = i18n.T("·  waiting for it to come back")
		return s
	}
	if bs := x.atBoot(); len(bs) > 0 {
		s.Steps = []string{i18n.T("Unplug %s.", bs[0].label()), i18n.T("Plug it back in, same port or another Thunderbolt port.")}
		s.Step = 0
		s.Happens = i18n.T("It was in at power-on, when the firmware sets it up. Plugged in now, Linux has to.")
		s.Say, s.SayAt = i18n.T("that one was here before me. unplug it, plug it back."), 0.2
		for _, d := range bs {
			s.Live = append(s.Live, Fact(i18n.T("%[1]s: %[2]s, in since power-on", d.Name, d.label())))
		}
		s.Actions = []Action{{Label: i18n.T("Nothing shows up"), Does: i18n.T("it's plugged in: nothing here"), Off: true}}
		s.Waiting = i18n.T("·  waiting for the replug")
		return s
	}
	s.Say, s.SayAt = i18n.T("plug it in. I'm watching for Thunderbolt."), 0.2
	if gd != nil {
		switch {
		case x.marked:
			s.Say, s.SayAt, s.Face = i18n.T("noted: that port didn't work. try the next."), x.markAt, boot.Concerned
		case len(x.used) > 0:
			s.Say, s.SayAt, s.Face = i18n.T("that port's done. now the one in the ring."), x.askAt, boot.Pleased
		}
	}
	if x.display != "" {
		s.Live = append(s.Live, Gone(i18n.T("%s: a display on DisplayPort, not a Thunderbolt device", x.display)))
		s.Say, s.SayAt, s.Face = i18n.T("that's a display on the DisplayPort side. I need a thunderbolt device."), x.dispAt, boot.Hmm
	}
	if gd != nil {
		if !gd.done() {
			s.Live = append(s.Live, gd.waiting())
			s.Waiting = i18n.T("·  waiting for the port in the ring")
		}
		s.Actions = []Action{{Label: i18n.T("A port didn't work"), Does: i18n.T("nothing showed up there"), Key: "1", Off: c.T-x.askAt < 15}}
		return s
	}
	s.Live = append(s.Live, Wait(i18n.T("waiting for a Thunderbolt device")))
	s.Actions = []Action{{Label: i18n.T("Nothing shows up"), Does: i18n.T("it's plugged in: nothing here"), Off: c.T-x.askAt < 15}}
	return s
}
