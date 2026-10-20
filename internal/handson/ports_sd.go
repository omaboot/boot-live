package handson

import (
	"fmt"
	"path"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/omaboot/boot-live/internal/boot"
	"github.com/omaboot/boot-live/internal/checks"
	"github.com/omaboot/boot-live/internal/i18n"
)

// ports.sd-card, "SD card reader": the person puts a card in the Mac's slot; the machine sees
// the card's disk come up and reads its size (method automatic). Mac readers come two ways: on
// USB (Apple's internal Card Reader, most MacBook Pros and minis), a disk that's there from the
// start with no media (size 0) and gains a size when a card goes in; or on PCIe (Broadcom's
// SDXC reader, sdhci), an mmc host that gets a new mmcblk disk per card. Nothing is written
// to the card. With OmaBoot?'s test file on it (testfile.go), the file is read and checked
// byte for byte; without, nothing on the card is read but its file system's structures,
// looking for it.
//
//   - supported: a card's disk came up with a size: a new mmcblk disk, or the USB reader's
//     empty disk gaining media (or a new USB disk on a hard-wired port, the Mac's own reader),
//     and its test file, when it has one, read back right;
//   - partial: the mmc host saw the card (its card device appeared) but no disk came up
//     within 10 s; or the card came up and its test file read back wrong or not at all;
//   - failed: the tester put a card in and nothing came up ("Nothing shows up", method
//     observed, after 15 s);
//   - not_tested (no-equipment): skipped.
//
// With the Mac's port map (portmap.go), the card slot is ringed on it, and the evidence records
// which disk the drawn slot gave (right-1 = sdc on USB 2-1.3).
//
// A USB stick or an external card reader plugged in meanwhile is a new USB disk on a hotplug
// port: shown as not the slot, not counted. A card already in when the screen opens counts.
//
// The evidence keeps the timings from the test's start (the mmc host's card device, the card's
// disk, the test file read) and, for an mmc reader, how its host learns of a card: polled
// (MMC_CAP_NEEDS_POLL in debugfs' caps) or by interrupt (its /proc/interrupts count while the
// test ran), its PCI function and its runtime PM state at the start. 0.8.9 dev: the
// iMac18,x's Broadcom reader (14e4:16bc) seemed slow to see a card next to the iMac10,1's USB
// one, and nothing on record said where the time went.

func init() {
	Register(Spec{ID: "ports.sd-card", Group: Plug, Order: 60, Title: i18n.M("SD card reader"),
		Brief: i18n.M("insert an SD card"), Gear: i18n.M("an SD card"), Expect: 20 * time.Second,
		New: func(*Env) Run { return &sdCard{} }, Demo: func(f *Fake) {
			demoXHCI(f)
			// the Air's reader: Apple's Card Reader on the internal hub, an empty disk
			fakeUSBDev(f, "2-1.3", "05ac:8404", "Apple", "Internal Memory Card Reader", "480", "08")
			f.Set(usbDevices+"2-1.3:1.0/host2/target2:0:0/2:0:0:0/block/sdc/size", "0")
			f.Set("/sys/block/sdc/size", "0")
			f.Set("/sys/block/sdc/device/vendor", "APPLE")
			f.Set("/sys/block/sdc/device/model", "SD Card Reader")
			f.Set("/sys/block/sdc/ro", "0")
			f.SetAt(4.5, "/sys/block/sdc/size", "62333952")
			f.Disks["sdc"] = FakeDisk{TestFile: true}
		}})
}

// sdDisk is a block device that may be a card.
type sdDisk struct {
	Name  string // mmcblk0, sdc
	Bus   string // mmc or usb
	USB   string // its USB device, for usb
	Size  int64
	Model string // vendor and model (USB), or type and name (SD SD32G)
	RO    bool   // read-only: the card's lock switch
	Fixed bool   // on a hard-wired USB port: the Mac's own reader
}

var mmcSkip = regexp.MustCompile(`boot\d+$|rpmb$`)

// sdDisks are the disks a card can show up as: mmc disks and the USB ones.
func sdDisks(io IO) map[string]sdDisk {
	out := map[string]sdDisk{}
	usb := usbDisks(io)
	for _, b := range io.Glob("/sys/block/*") {
		name := path.Base(b)
		d := sdDisk{Name: name, Size: diskSize(io, name), RO: io.Read(b+"/ro") == "1"}
		switch {
		case strings.HasPrefix(name, "mmcblk") && !mmcSkip.MatchString(name):
			d.Bus = "mmc"
			d.Model = strings.TrimSpace(io.Read(b+"/device/type") + " " + io.Read(b+"/device/name"))
		case usb[name] != "":
			d.Bus, d.USB = "usb", usb[name]
			d.Model = strings.TrimSpace(io.Read(b+"/device/vendor") + " " + io.Read(b+"/device/model"))
			d.Fixed = io.Read(usbPortDir(d.USB)+"/connect_type") == "hard-wired"
		default:
			continue
		}
		out[name] = d
	}
	return out
}

// mmcCards are the cards the mmc hosts see now (mmc0:aaaa), whether or not a disk came up.
func mmcCards(io IO) []string {
	var out []string
	for _, c := range io.Glob("/sys/class/mmc_host/mmc*/mmc*:*") {
		out = append(out, path.Base(c))
	}
	return out
}

// cardReader looks like a card reader by its name (Apple's "SD Card Reader", "Card Reader",
// generic "SD/MMC").
var cardReader = regexp.MustCompile(`(?i)card|reader|sd/mmc|sdxc`)

type sdCard struct {
	start  map[string]sdDisk
	boot   string
	card   *sdDisk // the card, once seen
	cardAt float64
	host   string  // an mmc card the host saw, waiting for its disk
	hostAt float64 // when
	stray  []sdDisk
	polled float64
	gd     *guide    // the port map's slot, nil without a map
	job    DiskJob   // the card's read for the test file, once it's up
	read   *DiskRead // how it went
	seenAt float64   // when the mmc host's card device first showed (-1: not yet)
	readAt float64   // when the read was through
	hosts  []mmcHost // the mmc readers, at the start
}

// mmcHost is an mmc reader as the test found it at the start.
type mmcHost struct {
	Name, PCI, ID string // mmc0, 0000:04:00.1, 14e4:16bc
	Caps          string // debugfs' caps ("0x...") or ""
	Runtime       string // the PCI function's runtime PM state, or ""
	IRQ           int64  // its interrupts so far (-1: none named for it)
}

// mmcNeedsPoll is MMC_CAP_NEEDS_POLL (include/linux/mmc/host.h): the core polls for a card.
const mmcNeedsPoll = 1 << 5

// mmcHosts are the mmc readers there now.
func mmcHosts(io IO) []mmcHost {
	var out []mmcHost
	for _, h := range io.Glob("/sys/class/mmc_host/mmc*") {
		m := mmcHost{Name: path.Base(h), PCI: pciOf(link(io, h)), IRQ: irqCount(io, path.Base(h))}
		if m.PCI != "" {
			d := "/sys/bus/pci/devices/" + m.PCI + "/"
			m.ID = strings.TrimPrefix(io.Read(d+"vendor"), "0x") + ":" + strings.TrimPrefix(io.Read(d+"device"), "0x")
			m.Runtime = io.Read(d + "power/runtime_status")
		}
		m.Caps = io.Read("/sys/kernel/debug/" + m.Name + "/caps")
		out = append(out, m)
	}
	return out
}

// irqCount is the interrupts /proc/interrupts counts for name (all CPUs, all its lines), or -1.
func irqCount(io IO, name string) int64 {
	n, found := int64(0), false
	for _, l := range strings.Split(io.Read("/proc/interrupts"), "\n") {
		f := strings.Fields(l)
		if len(f) < 2 || !strings.HasSuffix(f[0], ":") || !slices.Contains(f, name) {
			continue
		}
		found = true
		for _, x := range f[1:] {
			v, err := strconv.ParseInt(x, 10, 64)
			if err != nil {
				break
			}
			n += v
		}
	}
	if !found {
		return -1
	}
	return n
}

// detect is how the host learns of a card: "polled", "by interrupt", or "" when nothing says.
func (m mmcHost) detect() string {
	if v, err := strconv.ParseInt(strings.TrimPrefix(m.Caps, "0x"), 16, 64); err == nil && m.Caps != "" {
		if v&mmcNeedsPoll != 0 {
			return "polled"
		}
		return "by interrupt"
	}
	return ""
}

// hostEvidence is what the test found about the mmc readers, now against the start.
func (x *sdCard) hostEvidence(io IO) string {
	var parts []string
	for _, m := range x.hosts {
		s := m.Name
		if m.PCI != "" {
			s += " (sdhci-pci " + m.PCI + " [" + m.ID + "])"
		}
		if d := m.detect(); d != "" {
			s += ": card detect " + d + " (caps " + m.Caps + ")"
		} else {
			s += ": card detect unknown (no debugfs caps)"
		}
		if now := irqCount(io, m.Name); m.IRQ >= 0 && now >= 0 {
			s += fmt.Sprintf(", %d interrupts while the test ran", now-m.IRQ)
		}
		if m.Runtime != "" {
			s += ", runtime PM " + m.Runtime + " at the start"
		}
		parts = append(parts, s)
	}
	if len(parts) == 0 {
		return ""
	}
	return "; mmc reader: " + strings.Join(parts, "; ")
}

// timings are the card's times from the test's start, for the evidence.
func (x *sdCard) timings() string {
	if x.cardAt == 0 {
		return ""
	}
	var parts []string
	if x.seenAt >= 0 {
		parts = append(parts, fmt.Sprintf("the reader's card device at %.1f s", x.seenAt))
	}
	parts = append(parts, fmt.Sprintf("its disk at %.1f s", x.cardAt))
	if x.readAt > 0 {
		parts = append(parts, fmt.Sprintf("the test file read by %.1f s", x.readAt))
	}
	return "; timings from the test's start (polled every 0.5 s): " + strings.Join(parts, ", ")
}

// sdNoteW is the widest note the slot can show on the map.
func sdNoteW() int {
	return max(width(seenNote(sizeText(999.9e9))), width(i18n.T("~ no disk")), width(i18n.T("~ bad read")), width(i18n.T("× dead")))
}

// slot gives the map's slot (the one asked for) its state, and the evidence its match.
func (x *sdCard) slot(c *Ctx, st PortState, note, match string) {
	if x.gd == nil {
		return
	}
	if nx := x.gd.next(); nx != nil {
		x.gd.set(nx.ID, st, note, c.T)
		if match != "" {
			x.gd.match[nx.ID] = match
		}
	}
}

// mapEvidence is the map's part of the evidence, or "".
func (x *sdCard) mapEvidence() string {
	if x.gd == nil {
		return ""
	}
	return "; " + x.gd.evidence()
}

// cardMatch is what the slot gave, for the evidence: "sdc on USB 2-1.3", "mmcblk0 on mmc".
// catch keeps the slot asked for's path for DoesItOmarchy.com's wiring: the USB reader's port, or
// the mmc host's PCI function.
func (x *sdCard) catch(c *Ctx, d *sdDisk) {
	if x.gd == nil {
		return
	}
	nx := x.gd.next()
	if nx == nil {
		return
	}
	p := ""
	if d.Bus == "usb" {
		p = usbStable(c.IO, d.USB)
	} else if pci := pciOf(link(c.IO, "/sys/block/"+d.Name)); pci != "" {
		p = "pci:" + pci
	}
	x.gd.catch(nx.ID, "ring", "", p)
}

// PortAnswers is the slot's answer for the wiring (porter).
func (x *sdCard) PortAnswers(*Ctx) []checks.PortAnswer { return x.gd.PortAnswers() }

func cardMatch(d *sdDisk) string {
	if d.Bus == "usb" {
		return d.Name + " on USB " + d.USB
	}
	return d.Name + " on " + d.Bus
}

func (x *sdCard) Start(c *Ctx) {
	c.Select(-1) // waiting for a card: a stray Enter answers nothing
	x.start = sdDisks(c.IO)
	x.boot = bootDisk(c.IO)
	x.seenAt, x.hosts = -1, mmcHosts(c.IO)
	for _, d := range x.start {
		if d.Size > 0 && d.Name != x.boot && (d.Bus == "mmc" || cardReader.MatchString(d.Model)) {
			d := d
			x.card, x.cardAt = &d, 0 // in before the test: that's the one
		}
	}
	x.gd = newGuide(c.Env, sdNoteW(), "sd")
	if x.card != nil {
		x.catch(c, x.card)
		x.slot(c, PortSeen, seenNote(sizeText(x.card.Size)), cardMatch(x.card))
	}
}

// readers are the card readers there now, for the screen and the evidence.
func (x *sdCard) readers(io IO) []string {
	var out []string
	for _, h := range io.Glob("/sys/class/mmc_host/mmc*") {
		out = append(out, path.Base(h))
	}
	for _, d := range x.start {
		if d.Bus == "usb" && d.Name != x.boot && (d.Size == 0 || cardReader.MatchString(d.Model)) {
			if d.Model != "" {
				out = append(out, d.Name+" ("+d.Model+")")
			} else {
				out = append(out, d.Name)
			}
		}
	}
	sort.Strings(out)
	return out
}

func (x *sdCard) Step(c *Ctx) {
	if c.T-x.polled < 0.5 {
		return
	}
	x.polled = c.T
	if x.seenAt < 0 && (x.card == nil || x.cardAt > 0) && len(mmcCards(c.IO)) > 0 {
		x.seenAt = c.T // the host's card device: before the disk, or with it
	}
	if x.card != nil {
		if x.job == nil {
			x.job = readTestFile(c.IO, x.card.Name)
		}
		if _, ok := x.job.Ready(); ok && x.readAt == 0 {
			x.readAt = c.T
		}
		if r, ok := x.job.Ready(); ok && c.T-x.cardAt >= 1.5 {
			x.read = &r
			x.decide(c)
		}
		return
	}
	for _, d := range sdDisks(c.IO) {
		s, was := x.start[d.Name]
		switch {
		case d.Size == 0 || d.Name == x.boot:
		case d.Bus == "mmc" || was && s.Size == 0 || !was && d.Fixed:
			d := d
			x.card, x.cardAt = &d, c.T
			x.catch(c, &d)
			x.slot(c, PortSeen, seenNote(sizeText(d.Size)), cardMatch(&d))
			return
		case !was && !x.strayed(d.Name):
			x.stray = append(x.stray, d)
		}
	}
	if cs := mmcCards(c.IO); len(cs) > 0 && x.host == "" {
		x.host, x.hostAt = cs[0], c.T
	}
	if x.host != "" && c.T-x.hostAt >= 10 {
		x.slot(c, PortPart, i18n.T("~ no disk"), x.host+" (no disk)")
		c.Decide(Outcome{Results: []checks.Result{Result("ports.sd-card", checks.Partial, Automatic,
			fmt.Sprintf("the card reader saw the card (%s) %.1f s into the test but no disk came up in 10 s", x.host, x.hostAt)+x.hostEvidence(c.IO)+x.mapEvidence(), i18n.T("card seen, no disk came up"))},
			Say: i18n.T("the slot felt the card, and no disk came up. partial.")})
	}
}

func (x *sdCard) strayed(name string) bool {
	for _, d := range x.stray {
		if d.Name == name {
			return true
		}
	}
	return false
}

func (x *sdCard) decide(c *Ctx) {
	d := x.card
	ev := fmt.Sprintf("card seen: %s on %s", d.Name, d.Bus)
	if d.Bus == "usb" {
		ev += " (USB " + d.USB + ")"
	}
	if d.Model != "" {
		ev += ", " + d.Model
	}
	ev += ", " + gb(d.Size)
	if d.RO {
		ev += ", read-only (the card's lock switch)"
	}
	if x.cardAt == 0 {
		ev += "; it was in before the test began"
	}
	r, gone := x.read, diskSize(c.IO, d.Name) == 0
	switch {
	case r == nil || r.Skipped != "" || r.NoMedium:
	case r.OK():
		ev += "; " + testFileEvidence(r, d.Name)
	case r.NoFile:
		ev += "; " + r.Note + " (only that file is read)"
	case gone:
		ev += "; it came out before its test file was read through"
	default:
		x.slot(c, PortPart, i18n.T("~ bad read"), "")
		ev += "; its test file " + r.FileAt + " didn't read right: " + r.Fail + x.timings() + x.hostEvidence(c.IO) + x.mapEvidence()
		c.Decide(Outcome{Results: []checks.Result{Result("ports.sd-card", checks.Partial, Automatic, ev, i18n.T("%[1]s, test file: %[2]s", d.Name, failWords(r)))},
			Say: i18n.T("the card came up, and its test file didn't read right. partial.")})
		return
	}
	ev += x.timings() + x.hostEvidence(c.IO) + x.mapEvidence()
	line, say := i18n.T("%[1]s, %[2]s", d.Name, sizeText(d.Size)), i18n.T("%s card, read its size. the slot works.", sizeText(d.Size))
	if r != nil && r.OK() {
		line = i18n.T("%[1]s, %[2]s, test file verified", d.Name, sizeText(d.Size))
		say = i18n.T("%s card, test file checks out. the slot works.", sizeText(d.Size))
	}
	c.Decide(Outcome{Results: []checks.Result{Result("ports.sd-card", checks.Supported, Automatic, ev, line)}, Say: say})
}

func sizeText(n int64) string { return i18n.T("%.1f GB", float64(n)/1e9) }

func (x *sdCard) Act(c *Ctx, i int) {
	if i != 0 || c.T < 15 || x.card != nil {
		return
	}
	ev := "the tester put a card in and no card disk came up"
	if rs := x.readers(c.IO); len(rs) > 0 {
		ev += "; card readers seen: " + strings.Join(rs, ", ")
	} else {
		ev += "; no card reader seen (no mmc host, no empty USB card disk)"
	}
	x.slot(c, PortDead, i18n.T("× dead"), "")
	ev += fmt.Sprintf("; picked %.0f s into the test", c.T) + x.hostEvidence(c.IO) + x.mapEvidence()
	c.Decide(Outcome{Results: []checks.Result{Result("ports.sd-card", checks.Failed, Observed, ev, i18n.T("no card came up"))},
		Say: i18n.T("card in, and nothing came up. that's a fail.")})
}

func (x *sdCard) Stop(*Ctx) {
	if x.job != nil && x.read == nil {
		x.job.Stop()
	}
}

func (x *sdCard) View(c *Ctx) Screen {
	s := Screen{Face: boot.Idle, OutTitle: i18n.T("What I see"), SkipDoes: i18n.T("goes in as not tested")}
	s.Steps = []string{i18n.T("Put an SD card into the card slot."), i18n.T("Leave it in until it shows up under What I see.")}
	if x.gd != nil {
		s.Map, s.SayRows = x.gd.view(false), portSayRows
		s.Steps[0] = i18n.T("Put an SD card into the slot in the violet ring.")
	}
	s.Happens = i18n.T("Label up, contacts first, all the way in. Nothing is written to it; OmaBoot?'s test file, if it's on the card, is read and checked.")
	s.Hint = i18n.T("all the way in, label up. some slots want a firm push.")
	if d := x.card; d != nil {
		s.Step = 1
		s.Say, s.SayAt, s.Face = i18n.T("a card. %s of it.", sizeText(d.Size)), x.cardAt, boot.Pleased
		s.Live = []Line{Fact(i18n.T("%[1]s: %[2]s", d.Name, sizeText(d.Size)))}
		if d.Model != "" {
			s.Live = []Line{Fact(i18n.T("%[1]s (%[2]s): %[3]s", d.Name, d.Model, sizeText(d.Size)))}
		}
		if x.gd != nil {
			s.Live = []Line{x.gd.line(x.gd.ports[0], s.Live[0].Text)}
		}
		if d.RO {
			s.Live = append(s.Live, Fact(i18n.T("read-only: the card's lock switch is on")))
		}
		if x.cardAt > 0 {
			t := i18n.T("seen %.1f s into the test", x.cardAt)
			if x.seenAt >= 0 && x.seenAt < x.cardAt {
				t = i18n.T("seen %[1].1f s into the test (the reader felt it at %[2].1f s)", x.cardAt, x.seenAt)
			}
			if x.readAt > 0 {
				t += i18n.T("; read through at %.1f s", x.readAt)
			}
			s.Live = append(s.Live, Fact(t))
		}
		if l, ok := testFileProgress(x.job); ok {
			s.Live = append(s.Live, l)
		}
		s.Actions = []Action{{Label: i18n.T("Nothing shows up"), Does: i18n.T("the card is in: nothing here"), Off: true}}
		s.Waiting = i18n.T("·  reading the card")
		return s
	}
	s.Say, s.SayAt = i18n.T("card in, please. I'm watching the slot."), 0.2
	rs := x.readers(c.IO)
	for _, m := range x.hosts {
		if d := m.detect(); d != "" {
			s.Live = append(s.Live, Fact(i18n.T("%[1]s: card detect %[2]s", m.Name, d)))
		}
	}
	if len(rs) > 0 {
		s.Live = append(s.Live, Fact(i18n.T("card reader: %s", strings.Join(rs, ", "))))
	} else {
		s.Live = append(s.Live, Gone(i18n.T("no card reader seen yet")))
	}
	for _, d := range x.stray {
		s.Live = append(s.Live, Gone(i18n.T("%s: a USB drive, not the card slot", strings.TrimSpace(d.Name+" "+d.Model))))
	}
	if x.host != "" {
		s.Live = append(s.Live, Wait(i18n.T("%s: a card, waiting for its disk", x.host)))
		s.Step = 1
	} else {
		s.Live = append(s.Live, Wait(i18n.T("waiting for a card")))
	}
	s.Actions = []Action{{Label: i18n.T("Nothing shows up"), Does: i18n.T("the card is in: nothing here"), Off: c.T < 15}}
	s.Waiting = i18n.T("·  waiting for a card")
	return s
}
