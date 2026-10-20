package handson

import (
	"fmt"
	"path"
	"strings"
	"time"

	"github.com/omaboot/boot-live/internal/boot"
	"github.com/omaboot/boot-live/internal/checks"
	"github.com/omaboot/boot-live/internal/i18n"
	"github.com/omaboot/boot-live/internal/safety"
)

// ports.firewire, "FireWire": the person plugs in any FireWire device (a
// drive, a camera, another Mac in Target Disk Mode); the machine watches the FireWire bus
// (/sys/bus/firewire/devices, polled twice a second) for a node other than the Mac's own to
// appear, and takes its vendor and model from the config ROM the kernel read (method
// automatic). The quick pass has already looked at the controller, its driver and the Mac's own
// node (checks/firewire.go); the test is offered only when the bus is up.
//
// Nothing is mounted or written. A drive (SBP-2: a FireWire disk, a Mac in Target Disk Mode)
// becomes a SCSI disk: the safety rules count a disk on FireWire as one to protect, and the
// test has every such disk made read-only (BLKROSET, as at the start of the run) the moment it
// shows up; the evidence says so. Then, with OmaBoot?'s test file in the top folder of the
// drive's file system (testfile.go), the file is read and checked byte for byte; without,
// nothing on the drive is read but its file system's structures, looking for it.
//
//   - supported: a new node showed up on the bus after the screen opened (a device in since
//     before the test has to be unplugged and plugged back in), and its drive's test file,
//     when it has one, read back right;
//   - partial: a drive whose test file read back wrong or not at all;
//   - failed: the tester plugged a device in and no node showed up ("Nothing shows up",
//     observed, after 15 s);
//   - not_tested (no-equipment): skipped, "no FireWire device to plug in".
//
// With the Mac's port map (portmap.go) the FireWire 400 and 800 sockets are asked for one at a
// time, ringed, like the Thunderbolt test's: the device goes into the ringed port, shows up, is
// unplugged, and goes into the next; each port gets the rule above on its own, and once every
// port has its state the test decides: supported when every port worked, failed when none did,
// partial otherwise. A FireWire node says nothing of which socket it's on, so the ring decides.

func init() {
	Register(Spec{ID: "ports.firewire", Group: Plug, Order: 75, Title: i18n.M("FireWire"),
		Brief: i18n.M("plug in a FireWire device"), Gear: i18n.M("a FireWire device"), Without: i18n.M("no FireWire device to plug in"),
		Expect: time.Minute, Applies: fwApplies, New: func(*Env) Run { return &fireWire{} }, Demo: demoFireWire})
}

// fwApplies: offered where the bus is up (the quick pass fails it where it isn't); in the demo,
// on a Mac whose port map has a FireWire socket.
func fwApplies(e *Env) bool {
	if e.Checks == nil {
		for _, s := range e.Layout() {
			for _, p := range s.Ports {
				if strings.HasPrefix(p.Kind, "firewire") {
					return true
				}
			}
		}
		return false
	}
	c, ok := checks.FindFireWire(e.Checks)
	return ok && c.Ready()
}

const fwBus = "/sys/bus/firewire/devices/"

// demoFireWire is the iMac10,1's FireWire 800 bus: its own node, then a LaCie drive at 4 s, its
// disk at 4.5 s.
func demoFireWire(f *Fake) {
	f.Set(fwBus+"fw0/is_local", "1")
	f.Set(fwBus+"fw0/guid", "0x0026b0fffe4bd8a2")
	f.Set(fwBus+"fw0/vendor", "0x000a27")
	f.Set(fwBus+"fw0/vendor_name", "Apple Computer, Inc.")
	f.At(4, func(f *Fake) {
		f.Set(fwBus+"fw1/is_local", "0")
		f.Set(fwBus+"fw1/guid", "0x00d04b8e0a030ee1")
		f.Set(fwBus+"fw1/vendor", "0x00d04b")
		f.Set(fwBus+"fw1/vendor_name", "LaCie")
		f.Set(fwBus+"fw1/model", "0x00104b")
		f.Set(fwBus+"fw1/model_name", "d2 Quadra")
		f.Set(fwBus+"fw1/fw1.0/specifier_id", "0x00609e")
		f.Set(fwBus+"fw1/fw1.0/version", "0x010483")
	})
	f.At(4.5, func(f *Fake) {
		f.Disks["sdc"] = FakeDisk{TestFile: true, MBps: 38}
		f.Set("/sys/block/sdc/size", "1953525168")
		FakeLink(f, "/sys/block/sdc", "../devices/pci0000:00/0000:00:15.0/0000:04:00.0/fw1/fw1.0/host7/target7:0:0/7:0:0:0/block/sdc")
	})
}

// protector is an IO that can have the disks the safety rules protect made read-only again (a
// disk that showed up during the run): the Mac's (System); anything else needn't.
type protector interface {
	ProtectDisks() error
}

// ProtectDisks runs the safety rules' read-only pass again (safety.Protect): a fixture run
// changes nothing.
func (s *System) ProtectDisks() error {
	if !s.Real() {
		return nil
	}
	_, err := safety.Protect(s.Root)
	return err
}

type fireWire struct {
	start  map[string]checks.FWNode // the nodes when the screen opened
	local  *checks.FWNode
	dev    *checks.FWNode // the device on the cable now, once seen
	devAt  float64
	disks  []string // its disks, made read-only
	roErr  string   // why they couldn't be, or ""
	polled float64
	askAt  float64 // when the current port was asked for (or the screen opened)
	used   map[string]bool
	gd     *guide
	res    map[string]fwPort
	marked bool
	markAt float64
	job    DiskJob   // the drive's read for the test file
	read   *DiskRead // how it went
}

// fwDiskWait is how long a storage device (SBP-2) has for its disk to come up: the SBP-2 login
// takes a few seconds.
const fwDiskWait = 10

type fwPort struct {
	r    checks.Result
	line string
	say  string // what Boot says when it's the only port
}

// fwNoteW is the widest note a FireWire port can show on the map.
func fwNoteW() int {
	return max(width(seenNote(i18n.T("works"))), width(i18n.T("× dead")), width(i18n.T("~ bad read")), width(i18n.T("a moment…"))+2)
}

func (x *fireWire) Start(c *Ctx) {
	c.Select(-1) // waiting for a device: a stray Enter answers nothing
	x.start, x.used, x.res = map[string]checks.FWNode{}, map[string]bool{}, map[string]fwPort{}
	for _, n := range checks.FireWireNodes(c.IO) {
		x.start[n.Name] = n
		if n.Local && x.local == nil {
			n := n
			x.local = &n
		}
	}
	x.gd = newGuide(c.Env, fwNoteW(), "firewire400", "firewire800")
}

// PortAnswers is the ports' answers for the wiring (porter): a FireWire node has no path to
// place it by, so none are kept.
func (x *fireWire) PortAnswers(*Ctx) []checks.PortAnswer { return x.gd.PortAnswers() }

// atStart are the devices in since before the test and still in.
func (x *fireWire) atStart(now []checks.FWNode) []checks.FWNode {
	var out []checks.FWNode
	for _, n := range now {
		if s, ok := x.start[n.Name]; ok && !n.Local && s.GUID == n.GUID {
			out = append(out, n)
		}
	}
	return out
}

func (x *fireWire) Step(c *Ctx) {
	if c.T-x.polled < 0.5 {
		return
	}
	x.polled = c.T
	now := checks.FireWireNodes(c.IO)
	in := map[string]bool{}
	for _, n := range now {
		in[n.GUID+"/"+n.Name] = true
	}
	for k := range x.used { // unplugged: the next port can have it
		if !in[k] {
			delete(x.used, k)
		}
	}
	for name, s := range x.start { // a device in since before the test, unplugged: from now on like any other
		if !in[s.GUID+"/"+name] {
			delete(x.start, name)
		}
	}
	if x.dev != nil {
		if !in[x.dev.GUID+"/"+x.dev.Name] { // unplugged before it was counted
			x.drop()
			return
		}
		x.guard(c)
		if len(x.disks) > 0 && x.job == nil {
			x.job = readTestFile(c.IO, x.disks[0])
		}
		age := c.T - x.devAt
		switch {
		case age < 1.5:
		case x.job != nil:
			if r, ok := x.job.Ready(); ok {
				x.read = &r
				x.decide(c)
			}
		case !x.storage() || age >= fwDiskWait:
			x.decide(c)
		}
		return
	}
	for _, n := range now {
		if _, before := x.start[n.Name]; n.Local || before || x.used[n.GUID+"/"+n.Name] {
			continue
		}
		n := n
		x.dev, x.devAt = &n, c.T
		x.guard(c)
		return
	}
}

// storage: the device is a drive (an SBP-2 unit), so a disk should come up for it.
func (x *fireWire) storage() bool {
	for _, k := range x.dev.UnitKinds() {
		if k == "SBP-2 storage" {
			return true
		}
	}
	return false
}

// drop lets go of the device on the cable: unplugged, or counted.
func (x *fireWire) drop() {
	if x.job != nil && x.read == nil {
		x.job.Stop()
	}
	x.dev, x.disks, x.roErr, x.job, x.read = nil, nil, "", nil, nil
}

// guard makes the device's disks read-only as they show up (SBP-2: a drive, a Mac in Target
// Disk Mode), before anything reads them.
func (x *fireWire) guard(c *Ctx) {
	var ds []string
	for _, b := range c.IO.Glob("/sys/block/sd*") {
		if strings.Contains(link(c.IO, b), "/"+x.dev.Name+"/") {
			ds = append(ds, path.Base(b))
		}
	}
	if len(ds) == len(x.disks) {
		return
	}
	x.disks = ds
	if p, ok := c.IO.(protector); ok {
		if err := p.ProtectDisks(); err != nil {
			x.roErr = err.Error()
		}
	}
}

// devText is the device in English, for the evidence.
func (x *fireWire) devText() string {
	d := x.dev
	s := fmt.Sprintf("%s: %s (vendor %s, model %s, guid %s)", d.Name, d.Label(), orQ(d.Vendor), orQ(d.Model), orQ(d.GUID))
	if ks := d.UnitKinds(); len(ks) > 0 {
		s += ", " + strings.Join(ks, ", ")
	}
	s += "; plugged in during the test"
	if len(x.disks) > 0 {
		s += "; its disk " + strings.Join(x.disks, ", ") + " made read-only at once, never mounted"
		if x.roErr != "" {
			s += " (the safety rules said: " + x.roErr + ")"
		}
	} else if x.storage() {
		s += fmt.Sprintf("; a storage device, and no disk came up for it in %d s", fwDiskWait)
	}
	return s + x.readText()
}

// readText is the drive's test file read, for the evidence.
func (x *fireWire) readText() string {
	r := x.read
	switch {
	case r == nil || r.Skipped != "" || r.NoMedium:
		return ""
	case r.OK():
		return "; " + testFileEvidence(r, r.Disk)
	case r.NoFile:
		return "; " + r.Note + " (only that file is read)"
	}
	return "; its test file " + r.FileAt + " didn't read right: " + r.Fail
}

// badRead: the drive's test file didn't read right.
func (x *fireWire) badRead() bool {
	r := x.read
	return r != nil && r.Skipped == "" && !r.NoMedium && !r.NoFile && !r.OK()
}

func (x *fireWire) busText() string {
	if x.local == nil {
		return "no local node on the FireWire bus"
	}
	return "local node " + x.local.Name + " (" + x.local.Label() + ")"
}

// decide takes the device on the port asked for (or, without a map, decides).
func (x *fireWire) decide(c *Ctx) {
	d := x.dev
	ev := x.busText() + "; " + x.devText()
	r := Result("ports.firewire", checks.Supported, Automatic, ev, i18n.T("%s on the bus", d.Label()))
	say, line, st, note := i18n.T("%s, on the bus. FireWire works.", d.Label()), d.Label(), PortSeen, seenNote(i18n.T("works"))
	switch {
	case x.badRead():
		r = Result("ports.firewire", checks.Partial, Automatic, ev, i18n.T("%[1]s on the bus, test file: %[2]s", d.Label(), failWords(x.read)))
		say = i18n.T("%s, on the bus, and its test file didn't read right. partial.", d.Label())
		line, st, note = i18n.T("%[1]s · test file: %[2]s", d.Label(), failWords(x.read)), PortPart, i18n.T("~ bad read")
	case x.read != nil && x.read.OK():
		r.Detail = i18n.T("%s on the bus, test file verified", d.Label())
		say = i18n.T("%s, on the bus, test file checks out. FireWire works.", d.Label())
		line = i18n.T("%[1]s · test file verified, %[2]s", d.Label(), mbps(x.read))
	}
	if x.gd == nil {
		c.Decide(Outcome{Results: []checks.Result{r}, Say: say})
		return
	}
	nx := x.gd.next()
	if nx == nil {
		return
	}
	x.gd.set(nx.ID, st, note, c.T)
	x.gd.match[nx.ID] = d.Name + " " + d.Label()
	x.res[nx.ID] = fwPort{r: r, line: line, say: say}
	x.used[d.GUID+"/"+d.Name] = true
	x.drop()
	x.askAt, x.marked = c.T, false
	x.finish(c)
}

// finish decides once every port of the map has its state (the Thunderbolt test's rule).
func (x *fireWire) finish(c *Ctx) {
	gd := x.gd
	if !gd.done() {
		return
	}
	if len(gd.ports) == 1 {
		p := x.res[gd.ports[0].ID]
		p.r.Evidence += "; " + gd.evidence()
		say := p.say
		if p.r.Status == checks.Failed {
			say = i18n.T("plugged in, and the FireWire bus saw nothing. that's a fail.")
		}
		c.Decide(Outcome{Results: []checks.Result{p.r}, Say: say})
		return
	}
	var ev []string
	for _, p := range gd.ports {
		ev = append(ev, p.ID+": "+strings.TrimPrefix(x.res[p.ID].r.Evidence, x.busText()+"; "))
	}
	evidence := x.busText() + "; " + strings.Join(ev, "; ") + "; " + gd.evidence()
	n, ok, dead := len(gd.ports), gd.count(PortSeen), gd.count(PortDead)
	var r checks.Result
	var say string
	switch {
	case ok == n:
		r = Result("ports.firewire", checks.Supported, Automatic, evidence, i18n.N("%d port works", "all %d ports work", n, n))
		say = i18n.T("every port saw the device. FireWire works.")
	case dead == n:
		r = Result("ports.firewire", checks.Failed, Observed, evidence, i18n.T("nothing appeared on the bus"))
		say = i18n.T("plugged in, and the FireWire bus saw nothing. that's a fail.")
	default:
		r = Result("ports.firewire", checks.Partial, Observed, evidence, i18n.T("%[1]d of %[2]d ports work", ok, n))
		say = i18n.T("not every port saw it. partial.")
	}
	c.Decide(Outcome{Results: []checks.Result{r}, Say: say})
}

func (x *fireWire) Act(c *Ctx, i int) {
	if i != 0 || x.dev != nil || c.T-x.askAt < 15 || len(x.atStart(checks.FireWireNodes(c.IO))) > 0 {
		return
	}
	ev := x.busText() + "; the tester plugged a FireWire device in and no node appeared on the bus"
	r := Result("ports.firewire", checks.Failed, Observed, ev, i18n.T("nothing appeared on the bus"))
	if x.gd == nil {
		c.Decide(Outcome{Results: []checks.Result{r}, Say: i18n.T("plugged in, and the FireWire bus saw nothing. that's a fail.")})
		return
	}
	if nx := x.gd.next(); nx != nil { // the port asked for: marked, and on to the next
		x.gd.set(nx.ID, PortDead, i18n.T("× dead"), c.T)
		x.res[nx.ID] = fwPort{r: r}
		x.askAt, x.marked, x.markAt = c.T, true, c.T
		x.finish(c)
	}
}

func (x *fireWire) Stop(*Ctx) {
	if x.job != nil && x.read == nil {
		x.job.Stop()
	}
}

func (x *fireWire) View(c *Ctx) Screen {
	s := Screen{Face: boot.Idle, OutTitle: i18n.T("What I see"), SkipDoes: i18n.T("goes in as not tested")}
	gd := x.gd
	s.Steps = []string{i18n.T("Plug a FireWire device into the FireWire port: a drive, a camera, or another Mac in Target Disk Mode."),
		i18n.T("Wait for it to show up under What I see.")}
	s.Happens = i18n.T("The device has to show up on the FireWire bus. Nothing on it is written; on a drive, OmaBoot?'s test file, if it's there, is read and checked.")
	if gd != nil {
		s.Map, s.SayRows = gd.view(false), portSayRows
		s.Steps[0] = i18n.T("Plug a FireWire device into the port in the violet ring: a drive, a camera, or another Mac in Target Disk Mode.")
		if len(gd.ports) > 1 {
			s.Steps = []string{s.Steps[0], i18n.T("Move it to the next port in the ring."), i18n.T("Wait for it to show up under What I see.")}
			s.Happens = i18n.T("Each port has to see the device; one device does for all of them. Nothing on it is written; on a drive, OmaBoot?'s test file, if it's there, is read and checked.")
			if len(gd.ports)-len(gd.pending()) > 0 {
				s.Step = 1
			}
		}
	}
	if x.dev != nil {
		s.Step = len(s.Steps) - 1
	}
	s.Hint = i18n.T("a FireWire 800 port takes a 400 device through a 400-to-800 cable.")
	if x.local != nil {
		s.Live = append(s.Live, Fact(i18n.T("FireWire bus: %s (this Mac)", x.local.Name)))
	} else {
		s.Live = append(s.Live, Gone(i18n.T("no FireWire bus seen")))
	}
	if gd != nil {
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
	if d := x.dev; d != nil {
		text := d.Label()
		if ks := d.UnitKinds(); len(ks) > 0 {
			text += " · " + strings.Join(ks, ", ")
		}
		if gd != nil && gd.next() != nil { // on the port asked for
			s.Live = append(s.Live, gd.line(*gd.next(), text))
			s.Map.Note[gd.next().ID] = i18n.T("a moment…")
		} else {
			s.Live = append(s.Live, Fact(i18n.T("%[1]s: %[2]s", d.Name, text)))
		}
		if len(x.disks) > 0 {
			s.Live = append(s.Live, Fact(i18n.T("its disk %s: made read-only", strings.Join(x.disks, ", "))))
		}
		if l, ok := testFileProgress(x.job); ok {
			s.Live = append(s.Live, l)
		} else if len(x.disks) == 0 && x.storage() && c.T-x.devAt >= 1.5 {
			s.Live = append(s.Live, Wait(i18n.T("waiting for its disk…")))
		}
		s.Say, s.SayAt, s.Face = i18n.T("%s. on the bus.", d.Label()), x.devAt, boot.Pleased
		s.Waiting = i18n.T("·  a moment…")
		s.Actions = []Action{{Label: i18n.T("Nothing shows up"), Does: i18n.T("it's plugged in: nothing here"), Off: true}}
		return s
	}
	if bs := x.atStart(checks.FireWireNodes(c.IO)); len(bs) > 0 {
		s.Steps = []string{i18n.T("Unplug %s.", bs[0].Label()), i18n.T("Plug it back in.")}
		s.Happens = i18n.T("It was in before the test began: plugged in now, the bus has to find it.")
		s.Say, s.SayAt = i18n.T("that one was here before me. unplug it, plug it back."), 0.2
		for _, d := range bs {
			s.Live = append(s.Live, Fact(i18n.T("%[1]s: %[2]s, in before the test", d.Name, d.Label())))
		}
		s.Actions = []Action{{Label: i18n.T("Nothing shows up"), Does: i18n.T("it's plugged in: nothing here"), Off: true}}
		s.Waiting = i18n.T("·  waiting for the replug")
		return s
	}
	s.Say, s.SayAt = i18n.T("plug it in. I'm watching the FireWire bus."), 0.2
	switch {
	case x.marked:
		s.Say, s.SayAt, s.Face = i18n.T("noted: that port didn't work. try the next."), x.markAt, boot.Concerned
	case len(x.res) > 0:
		s.Say, s.SayAt, s.Face = i18n.T("that port's done. now the one in the ring."), x.askAt, boot.Pleased
	}
	s.Waiting = i18n.T("·  waiting for a FireWire device")
	if c.Outcome() != nil {
		s.Step = len(s.Steps)
	}
	if gd != nil {
		if !gd.done() {
			s.Live = append(s.Live, gd.waiting())
		}
		s.Actions = []Action{{Label: i18n.T("A port didn't work"), Does: i18n.T("nothing showed up there"), Key: "1", Off: c.T-x.askAt < 15}}
		return s
	}
	s.Live = append(s.Live, Wait(i18n.T("waiting for a FireWire device")))
	s.Actions = []Action{{Label: i18n.T("Nothing shows up"), Does: i18n.T("it's plugged in: nothing here"), Off: c.T-x.askAt < 15}}
	return s
}
