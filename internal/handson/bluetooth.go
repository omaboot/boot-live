package handson

import (
	"errors"
	"fmt"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/omaboot/boot-live/internal/boot"
	"github.com/omaboot/boot-live/internal/checks"
	"github.com/omaboot/boot-live/internal/i18n"
)

// network.bluetooth, "Bluetooth": the person makes a Bluetooth device of their own discoverable
// (a phone in its Bluetooth settings, headphones in pairing mode, a mouse); the machine powers
// the adapter on, scans (classic and Low Energy) and lists what it hears, and the person picks
// their own device from the list (method challenge): devices of strangers nearby show up too,
// so hearing something proves little until the person finds theirs among it (a person proves what
// they saw). No pairing, no connection. The scan goes straight to the
// kernel's Bluetooth management interface (the socket bluetoothd itself uses; btmgmt does the
// same), so it needs no bluez on the live image and works beside a running bluetoothd too. What
// it tests is the part that differs between Macs: the adapter, its driver and firmware, and the
// radio.
//
//   - supported: the adapter powered on, the scan found devices, and the tester picked their own
//     from the list (challenge);
//   - partial: the adapter powered on and scanned, but heard nothing in 30 s, and the tester
//     says a discoverable device was nearby ("Nothing shows up", observed); or it heard other
//     devices and the tester's own isn't among them ("Mine isn't listed", observed, with the
//     count found): the controller works, receiving the device doesn't (or it wasn't really
//     discoverable);
//   - failed: no Bluetooth adapter (nothing in /sys/class/bluetooth), it's hard-blocked, it
//     wouldn't power on, it wouldn't start discovering (not even classic only), or it went
//     away during the scan;
//   - not_tested (other): the tool couldn't reach the kernel's Bluetooth management at all
//     (no socket: not the Mac's fault); not_tested (no-equipment): skipped.
//
// The person is told to open their phone's Bluetooth settings first: a phone shows its name
// only while that screen is open (opened after the list appeared, every device showed unnamed). The
// list to pick from starts as the devices heard when the scan has had
// a few seconds after the first, nearest first; after that it only grows at its end (so nothing
// moves under the cursor): a device that turns up with a name, or gets one, joins it as it
// arrives, and names fill in on the lines already there. It shows btPickMax at a time, "the 8
// nearest of 22", and More devices (0) pages on through the rest (unnamed ones heard later
// included). The ▶ starts on "Mine isn't listed" (9), greyed until the 30 s window is over, so a
// plain Enter picks nothing; it goes off the list whenever a device joins the page. "Mine isn't
// listed" first shows the devices not yet seen, then decides. The pick goes into the report as
// its kind, maker, radio and signal, like everything heard, with how many were named.
//
// Device names show on the screen (so the person can spot theirs) and never go into the
// report: a phone's name is often the person's own name. Addresses are never shown. A device with
// no name counts all the same (an iPhone advertising over Low Energy has none; its maker shows).
// A soft-blocked adapter is unblocked for the test (rfkill), and Low Energy switched on if it's
// off (bluetoothd does that, and there's none); the scan stopped, the power, Low Energy and
// the block put back as they were when the screen is left. A Bluetooth firmware the kernel
// couldn't load (its log, from the quick pass) goes into the evidence and on the screen.

func init() {
	Register(Spec{ID: "network.bluetooth", Group: NoPlug, Order: 90, Title: i18n.M("Bluetooth"),
		Brief: i18n.M("make a Bluetooth device nearby discoverable"), Gear: i18n.M("a Bluetooth phone or headphones"),
		Expect: 40 * time.Second, New: func(e *Env) Run { return &bluetooth{fw: btFirmware(e)} }, Demo: func(f *Fake) {
			f.Set("/sys/class/bluetooth/hci0/type", "Primary")
			f.Set("/sys/class/bluetooth/hci0/rfkill1/type", "bluetooth")
			f.Set("/sys/class/bluetooth/hci0/rfkill1/soft", "0")
			f.Set("/sys/class/bluetooth/hci0/rfkill1/hard", "0")
			f.Set(usbDevices+"2-1.1.1.1/idVendor", "05ac")
			f.Set(usbDevices+"2-1.1.1.1/idProduct", "821f")
			FakeLink(f, "/sys/class/bluetooth/hci0/device", "../../../2-1.1.1.1:1.0")
			FakeBluetooth(f, "hci0", "Broadcom, Bluetooth 4.0", false)
			f.Set(fakeBT+"hci0/le", "0") // as on the image: no bluetoothd has switched it on
			FakeBluetoothDevice(f, 3.2, "hci0", BTDevice{Key: "a", Maker: "Apple", LE: true, RSSI: -48})
			FakeBluetoothDevice(f, 4.5, "hci0", BTDevice{Key: "b", Name: "Pixel 7a", Kind: "phone", RSSI: -52})
			FakeBluetoothDevice(f, 6, "hci0", BTDevice{Key: "c", Name: "WH-1000XM4", Kind: "audio", RSSI: -61})
		}})
}

// ── what the test needs from the Mac ─────────────────────────────────────────

// btRadio is an IO that can scan with a Bluetooth adapter (System, Fake).
type btRadio interface {
	// BluetoothScan powers the adapter (hci0) on if it's off and keeps discovering until
	// Close; it never blocks: the scan runs on its own and State says how it goes.
	BluetoothScan(adapter string) BTScan
}

// BTScan is a running scan.
type BTScan interface {
	State() BTState
	Again() // discover again if discovery stopped
	Close() // stop discovering and put the adapter's power back; never blocks
}

// BTState is how a scan goes.
type BTState struct {
	Info     string // the controller, for the report: "Broadcom, Bluetooth 4.0"
	WasOn    bool   // powered before the test
	Powered  bool
	LEOn     bool // Low Energy was off and the scan switched it on
	Scanning bool
	Mode     string // what the discovery covers, for the report: "classic and LE", "classic only", "LE only"
	Found    []BTDevice
	PowerErr string // the adapter wouldn't power on: the kernel's answer
	ScanErr  string // it wouldn't start discovering: the kernel's answer
	Gone     bool   // the adapter went away
	Err      error  // the kernel's Bluetooth management couldn't be reached
}

// BTDevice is one device heard.
type BTDevice struct {
	Key   string // tells devices apart (the address): never shown, never reported
	Name  string // as it calls itself, or ""
	Kind  string // phone, computer, audio, input, watch, network, imaging, wearable, or ""
	Maker string // from its advertising: "Apple", "Samsung"…, or ""
	LE    bool   // heard over Low Energy (else classic)
	RSSI  int    // dBm
}

// ── the test ──────────────────────────────────────────────────────────────────

const (
	btWindow  = 30.0 // seconds of hearing nothing before the person is asked
	btPickMax = 8    // devices on the list at a time
)

type bluetooth struct {
	hci     string
	bus     string // where it hangs off, for the report
	rfkill  string // the adapter's rfkill, if the test unblocked it
	scan    BTScan
	st      BTState
	look    float64 // when the current listening window began
	firstAt float64 // when the first device was heard (-1: not yet)
	bad     string  // what made it fail, for the screen
	off     string  // why it couldn't test, for the screen
	fw      string  // the kernel's line about a Bluetooth firmware it couldn't load, or ""
	polled  float64
	list    []string        // the devices offered, by key, in the order they joined (nil: not asking yet)
	page    int             // the page of btPickMax on screen
	seen    map[string]bool // the devices the person has had on screen
	pickAt  float64         // when the list was made, or last changed
	grew    int             // how many devices joined the page last
	paged   bool            // the last change was a new page
	mixed   bool            // a named device took an unnamed one's place: the page isn't the nearest any more
}

// btFirmware is the quick pass's kernel log line about a Bluetooth firmware (a Broadcom patch
// file, brcm/*.hcd) that wasn't found or didn't load, or "". Apple's own Broadcom modules get
// no patch from Linux (btbcm_setup_apple), so there it's usually "": other adapters need theirs.
func btFirmware(e *Env) string {
	if e == nil || e.Checks == nil {
		return ""
	}
	for _, l := range e.Checks.Kmsg {
		low := strings.ToLower(l)
		if (strings.Contains(low, "bluetooth") || strings.Contains(low, ".hcd")) &&
			(strings.Contains(low, "firmware") || strings.Contains(low, "patch")) &&
			(strings.Contains(low, "not found") || strings.Contains(low, "failed")) {
			return strings.TrimSpace(l)
		}
	}
	return ""
}

// btAdapters are the adapters in sysfs (hci0), not their connections (hci0:3585).
func btAdapters(io IO) []string {
	var out []string
	for _, p := range io.Glob("/sys/class/bluetooth/hci*") {
		if n := path.Base(p); !strings.Contains(n, ":") {
			out = append(out, n)
		}
	}
	return out
}

// btUSBRadios counts USB Bluetooth interfaces (class e0, subclass 01, protocol 01): a module
// there with no adapter means the driver or its firmware didn't bring it up.
func btUSBRadios(io IO) int {
	n := 0
	for _, i := range io.Glob(usbDevices + "*:*") {
		if io.Read(i+"/bInterfaceClass") == "e0" && io.Read(i+"/bInterfaceSubClass") == "01" && io.Read(i+"/bInterfaceProtocol") == "01" {
			n++
		}
	}
	return n
}

func (b *bluetooth) fail(c *Ctx, ev, detail, say string) {
	b.bad = detail
	c.Decide(Outcome{Results: []checks.Result{Result("network.bluetooth", checks.Failed, Automatic, ev, detail)}, Say: say})
}

func (b *bluetooth) Start(c *Ctx) {
	b.firstAt = -1
	as := btAdapters(c.IO)
	if len(as) == 0 {
		ev := "no Bluetooth adapter: /sys/class/bluetooth is empty"
		if n := btUSBRadios(c.IO); n > 0 {
			ev += fmt.Sprintf("; %d USB Bluetooth interface(s) present without an adapter (driver or firmware)", n)
		}
		b.fail(c, ev, i18n.T("no Bluetooth adapter"), i18n.T("no bluetooth adapter showed up. nothing to listen with."))
		return
	}
	b.hci, b.bus = as[0], btBus(c.IO, as[0])
	for _, r := range c.IO.Glob("/sys/class/bluetooth/" + b.hci + "/rfkill*") {
		if c.IO.Read(r+"/hard") == "1" {
			b.fail(c, b.hci+" is hard-blocked (rfkill hard 1)", i18n.T("%s hard-blocked", b.hci), i18n.T("the adapter is blocked in hardware. no radio."))
			return
		}
		if c.IO.Read(r+"/soft") == "1" && c.IO.Write(r+"/soft", "0") == nil {
			b.rfkill = r
		}
	}
	radio, ok := c.IO.(btRadio)
	if !ok {
		b.off = i18n.T("no Bluetooth access here")
		c.Decide(Outcome{Results: []checks.Result{NotTested("network.bluetooth", checks.Other, "no Bluetooth access in this mode", b.off)}})
		return
	}
	b.scan = radio.BluetoothScan(b.hci)
}

func (b *bluetooth) Step(c *Ctx) {
	if b.scan == nil || c.T-b.polled < 0.25 {
		return
	}
	b.polled = c.T
	b.st = b.scan.State()
	st := b.st
	switch {
	case st.Err != nil:
		b.off = i18n.T("no Bluetooth management socket")
		c.Decide(Outcome{Results: []checks.Result{NotTested("network.bluetooth", checks.Other,
			"couldn't reach the kernel's Bluetooth management: "+st.Err.Error(), b.off)},
			Say: i18n.T("I couldn't talk to the bluetooth stack. that's on me, not the Mac.")})
	case st.PowerErr != "":
		b.fail(c, b.adapter()+" wouldn't power on: "+st.PowerErr, i18n.T("%s wouldn't power on", b.hci),
			i18n.T("the adapter is there and won't switch on."))
	case st.Gone:
		b.fail(c, b.adapter()+" went away during the scan", i18n.T("%s went away", b.hci),
			i18n.T("the adapter vanished mid-scan."))
	case st.ScanErr != "" && len(st.Found) == 0:
		b.fail(c, b.adapter()+" powered on and wouldn't start discovering (classic and LE, classic only, LE only): "+st.ScanErr+b.fwEvidence(),
			i18n.T("%s wouldn't scan", b.hci), i18n.T("the adapter switched on and won't scan."))
	case len(st.Found) > 0 && b.firstAt < 0:
		b.firstAt = c.T
	case b.firstAt >= 0 && c.T-b.firstAt >= 4 && b.list == nil: // a few seconds more, to hear what else is around
		b.list, b.pickAt, b.seen = []string{}, c.T, map[string]bool{}
		for _, d := range btSorted(st.Found) {
			b.list = append(b.list, d.Key)
		}
		b.look0()
		c.Select(min(len(b.list), btPickMax)) // on "Mine isn't listed" (greyed at first): a plain Enter picks nothing
	case b.list != nil:
		if n := b.join(); n > 0 {
			b.grew, b.paged, b.pickAt = n, false, c.T
			c.Select(-1) // the answers moved: no cursor, so a stray Enter picks nothing
		}
	}
	if b.list != nil {
		for _, k := range b.onPage() {
			b.seen[k] = true
		}
	}
}

// look0 notes the first page as seen.
func (b *bluetooth) look0() {
	for _, k := range b.onPage() {
		b.seen[k] = true
	}
}

// join puts the devices heard with a name that aren't on the list yet on it, nearest first,
// where the person sees them: at the end of the page on screen while it has room, else in the
// place of its weakest unnamed device (which moves to the list's end: an unnamed device is
// seldom the one picked); with the page all named, at the list's end (More devices). It returns
// how many joined the page on screen.
func (b *bluetooth) join() int {
	on := map[string]bool{}
	for _, k := range b.list {
		on[k] = true
	}
	n := 0
	for _, d := range btSorted(b.st.Found) {
		if on[d.Key] || btClean(d.Name) == "" {
			continue
		}
		on[d.Key] = true
		from := b.page * btPickMax
		end := min(from+btPickMax, len(b.list))
		if end-from < btPickMax && end == len(b.list) {
			b.list = append(b.list, d.Key)
			n++
			continue
		}
		weak := -1
		for i := from; i < end; i++ {
			if x := b.device(b.list[i]); btClean(x.Name) == "" && (weak < 0 || x.RSSI <= b.device(b.list[weak]).RSSI) {
				weak = i
			}
		}
		if weak < 0 {
			b.list = append(b.list, d.Key)
			continue
		}
		b.list = append(b.list, b.list[weak])
		b.list[weak] = d.Key
		b.mixed = true
		n++
	}
	return n
}

// onPage are the keys on the page on screen.
func (b *bluetooth) onPage() []string {
	from := min(b.page*btPickMax, len(b.list))
	return b.list[from:min(from+btPickMax, len(b.list))]
}

// device is a device heard, by key, as it is now (its name may have come since).
func (b *bluetooth) device(key string) BTDevice {
	for _, d := range b.st.Found {
		if d.Key == key {
			return d
		}
	}
	return BTDevice{Key: key}
}

// unlisted are the devices heard that aren't on the list, nearest first.
func (b *bluetooth) unlisted() []BTDevice {
	on := map[string]bool{}
	for _, k := range b.list {
		on[k] = true
	}
	var out []BTDevice
	for _, d := range btSorted(b.st.Found) {
		if !on[d.Key] {
			out = append(out, d)
		}
	}
	return out
}

// more is whether there's a page beyond what the list shows, or devices heard and not listed.
func (b *bluetooth) more() bool {
	return len(b.list) > btPickMax || len(b.unlisted()) > 0
}

// nextPage moves the list on a page, adding the devices heard and not listed (nearest first)
// when it reaches the end; past the last page it goes back to the nearest.
func (b *bluetooth) nextPage(c *Ctx) {
	if (b.page+1)*btPickMax >= len(b.list) {
		for _, d := range b.unlisted() {
			b.list = append(b.list, d.Key)
		}
	}
	b.page++
	if b.page*btPickMax >= len(b.list) {
		b.page = 0
	}
	b.grew, b.paged, b.pickAt = 0, true, c.T
	for _, k := range b.onPage() {
		b.seen[k] = true
	}
	c.Select(-1)
}

// unseen is whether the person hasn't had every device heard on screen yet.
func (b *bluetooth) unseen() bool {
	for _, d := range b.st.Found {
		if !b.seen[d.Key] {
			return true
		}
	}
	return false
}

// picked decides on the person's answer from the list: their device (i), none of them, or
// another page.
func (b *bluetooth) picked(c *Ctx, i int) {
	n, page := len(b.st.Found), b.onPage()
	switch {
	case i < len(page):
		d := b.device(page[i])
		c.Decide(Outcome{Results: []checks.Result{Result("network.bluetooth", checks.Supported, Challenge,
			b.evidence()+fmt.Sprintf("; the tester picked their own device from the %d listed: %s", len(b.seen), btDesc(d)),
			i18n.N("yours among %d device found", "yours among %d devices found", n, n))},
			Say: i18n.T("that one's yours. the radio works.")})
	case i == len(page)+1 && b.more(): // More devices
		b.nextPage(c)
	case i != len(page) || b.listening(c):
	case b.unseen(): // Mine isn't listed, with devices not shown yet: those first
		b.nextPage(c)
	default:
		c.Decide(Outcome{Results: []checks.Result{Result("network.bluetooth", checks.Partial, Observed,
			b.evidence()+fmt.Sprintf("; the tester's own discoverable device wasn't among the %d listed", len(b.seen)),
			i18n.N("%d device found, not yours", "%d devices found, not yours", n, n))},
			Say: i18n.T("it hears others, not yours. partial.")})
	}
}

// adapter is the adapter for the report: "hci0 on USB 2-1.1.1.1 [05ac:821f] (Broadcom, Bluetooth 4.0)".
func (b *bluetooth) adapter() string {
	s := b.hci
	if b.bus != "" {
		s += " on " + b.bus
	}
	return s + " (" + orQ(b.st.Info) + ")"
}

// evidence is what was heard, without names or addresses.
func (b *bluetooth) evidence() string {
	st := b.st
	ev := b.adapter()
	if st.WasOn {
		ev += " was already powered on"
	} else {
		ev += " powered on for the test"
	}
	ev += b.switched()
	var ds []string
	for _, d := range btSorted(st.Found) {
		ds = append(ds, btDesc(d))
	}
	named := btNamed(st.Found)
	return ev + fmt.Sprintf("; discovery (%s) found %d device(s), %d named and %d unnamed: %s", orQ(st.Mode), len(st.Found),
		named, len(st.Found)-named, strings.Join(ds, ", ")) + b.fwEvidence()
}

// btNamed counts the devices that gave a name.
func btNamed(ds []BTDevice) int {
	n := 0
	for _, d := range ds {
		if btClean(d.Name) != "" {
			n++
		}
	}
	return n
}

// btDesc is a device for the report, never by name or address: "phone (classic, -52 dBm)".
func btDesc(d BTDevice) string {
	k := d.Kind
	if k == "" {
		k = "unknown kind"
	}
	t := "classic"
	if d.LE {
		t = "LE"
	}
	if d.Maker != "" {
		t += ", " + d.Maker
	}
	return fmt.Sprintf("%s (%s, %d dBm)", k, t, d.RSSI)
}

// switched is what the test changed to scan, for the report.
func (b *bluetooth) switched() string {
	s := ""
	if b.rfkill != "" {
		s += " (unblocked: it was soft-blocked)"
	}
	if b.st.LEOn {
		s += " (Low Energy switched on: it was off)"
	}
	return s
}

// fwEvidence is the kernel's firmware complaint for the report, or "".
func (b *bluetooth) fwEvidence() string {
	if b.fw == "" {
		return ""
	}
	return "; kernel log: " + b.fw
}

// btSorted is the devices nearest first (strongest signal), ties by key.
func btSorted(ds []BTDevice) []BTDevice {
	out := append([]BTDevice(nil), ds...)
	sort.Slice(out, func(i, j int) bool {
		if out[i].RSSI != out[j].RSSI {
			return out[i].RSSI > out[j].RSSI
		}
		return out[i].Key < out[j].Key
	})
	return out
}

func (b *bluetooth) listening(c *Ctx) bool { return c.T-b.look < btWindow }

func (b *bluetooth) Act(c *Ctx, i int) {
	if b.list != nil {
		b.picked(c, i)
		return
	}
	if b.scan == nil || b.listening(c) || len(b.st.Found) > 0 {
		return
	}
	switch i {
	case 0:
		b.scan.Again()
		b.look = c.T
	case 1:
		c.Decide(Outcome{Results: []checks.Result{Result("network.bluetooth", checks.Partial, Observed,
			fmt.Sprintf("%s powered on%s and scanned (%s) for %.0f s and found nothing; the tester had a discoverable device nearby%s",
				b.adapter(), b.switched(), orQ(b.st.Mode), c.T, b.fwEvidence()),
			i18n.T("adapter on, nothing found"))},
			Say: i18n.T("the radio's on and scanning, and hears nothing. partial.")})
	}
}

func (b *bluetooth) Stop(c *Ctx) {
	if b.scan != nil {
		b.scan.Close()
	}
	if b.rfkill != "" {
		c.IO.Write(b.rfkill+"/soft", "1")
	}
}

// btKinds are the kinds as the screen says them.
var btKinds = map[string]string{"phone": i18n.M("phone"), "computer": i18n.M("computer"), "audio": i18n.M("audio"),
	"input": i18n.M("mouse, keyboard"), "watch": i18n.M("watch"), "network": i18n.M("network"), "imaging": i18n.M("printer, camera"),
	"wearable": i18n.M("wearable")}

func (b *bluetooth) View(c *Ctx) Screen {
	s := Screen{Face: boot.Idle, OutTitle: i18n.T("What I hear")}
	st := b.st
	s.Steps = []string{i18n.T("Open your phone's Bluetooth settings now."),
		i18n.T("Find it on the list; press its number.")}
	s.Happens = i18n.T("A phone shows its name only while its Bluetooth settings are open, and others nearby show up too. Nothing gets paired; names never go in the report.")
	s.Say, s.SayAt = i18n.T("radio on. listening for bluetooth."), 0.2
	s.Hint = i18n.T("headphones work too: put them in pairing mode.")
	s.Waiting = "·  " + i18n.T("listening")
	if st.Info != "" {
		s.Live = append(s.Live, Fact(i18n.T("%[1]s: %[2]s", b.hci, st.Info)))
	} else if b.hci != "" {
		s.Live = append(s.Live, Fact(b.hci))
	}
	switch {
	case b.bad != "":
		s.Live = append(s.Live, Bad(b.bad))
		return s
	case b.off != "":
		s.Live = append(s.Live, Gone(b.off))
		return s
	case b.scan == nil:
		return s
	case st.Powered && st.Scanning && st.Mode == btModes[mgmtDiscoverBREDR]:
		s.Live = append(s.Live, Fact(i18n.T("powered on, scanning: classic only")))
	case st.Powered && st.Scanning && st.Mode == btModes[mgmtDiscoverLE]:
		s.Live = append(s.Live, Fact(i18n.T("powered on, scanning: Low Energy only")))
	case st.Powered && st.Scanning:
		s.Live = append(s.Live, Fact(i18n.T("powered on, scanning: classic and Low Energy")))
	case st.Powered:
		s.Live = append(s.Live, Fact(i18n.T("powered on")))
	default:
		s.Live = append(s.Live, Wait(i18n.T("powering on")))
	}
	ds := btSorted(st.Found)
	page := b.onPage()
	if b.list == nil {
		page = nil
	}
	s.Anim = btRadar(ds, page, st.Scanning)
	if len(ds) > 0 {
		named := btNamed(ds)
		s.Live = append(s.Live, Fact(i18n.N("%[1]d device heard: %[2]d named, %[3]d unnamed", "%[1]d devices heard: %[2]d named, %[3]d unnamed",
			len(ds), len(ds), named, len(ds)-named)))
	}
	if b.list != nil { // the devices are the answers now, and need the room
		s.Step, s.Happens = 1, ""
		s.Waiting = "·  " + i18n.T("waiting for your pick")
		s.Hint = i18n.T("yours shows no name? open its Bluetooth settings again: the name comes with it.")
		if b.listening(c) && c.Outcome() == nil {
			s.Live = append(s.Live, Wait(i18n.T("still listening, %d s", max(0, int(btWindow-(c.T-b.look))))))
		}
		s.Say, s.SayAt, s.Face = i18n.T("which one is yours?"), b.pickAt, boot.Pleased
		switch {
		case b.paged:
			s.Say = i18n.T("more of them. is yours there?")
		case b.grew > 0:
			s.Say = i18n.N("%d more with a name. is yours there now?", "%d more with names. is yours there now?", b.grew, b.grew)
		}
		from := b.page*btPickMax + 1
		switch {
		case len(ds) <= btPickMax && len(b.list) == len(ds):
			s.Ask = i18n.N("%d device heard: which one is yours?", "%d devices heard: which one is yours?", len(ds), len(ds))
		case b.page == 0 && !b.mixed:
			s.Ask = i18n.T("The %[1]d nearest of %[2]d: which one is yours?", len(page), len(ds))
		default:
			s.Ask = i18n.T("%[1]d to %[2]d of %[3]d: which one is yours?", from, from+len(page)-1, len(ds))
		}
		for _, k := range page {
			name, rest := btLine(b.device(k))
			s.Actions = append(s.Actions, Action{Label: name, Does: rest})
		}
		does := i18n.T("not on any page")
		if b.listening(c) {
			does = i18n.T("after 30 seconds")
		}
		s.Actions = append(s.Actions, Action{Label: i18n.T("Mine isn't listed"), Does: does, Off: b.listening(c), Key: "9"})
		if b.more() {
			next := i18n.T("back to the nearest")
			if end := (b.page + 1) * btPickMax; end < len(b.list)+len(b.unlisted()) {
				next = i18n.T("%[1]d to %[2]d", end+1, min(end+btPickMax, len(b.list)+len(b.unlisted())))
			}
			s.Actions = append(s.Actions, Action{Label: i18n.T("More devices"), Does: next, Key: "0"})
		}
		return s
	}
	for i, d := range ds {
		if i == 5 {
			s.Live = append(s.Live, Fact(i18n.N("and %d more", "and %d more", len(ds)-5, len(ds)-5)))
			break
		}
		name, rest := btLine(d)
		s.Live = append(s.Live, Fact(name+" · "+rest))
	}
	switch {
	case len(ds) > 0:
		s.Say, s.SayAt, s.Face = i18n.N("heard %d. listening a moment longer.", "heard %d. listening a moment longer.", len(ds), len(ds)), b.firstAt, boot.Pleased
	case b.listening(c):
		s.Live = append(s.Live, Wait(i18n.T("listening, %d s", max(0, int(btWindow-(c.T-b.look))))))
	default:
		s.Live = append(s.Live, Gone(i18n.T("nothing heard yet")))
		s.Say, s.SayAt, s.Face = i18n.T("all quiet. is it really discoverable?"), b.look+btWindow, boot.Hmm
		s.Waiting = "·  " + i18n.T("waiting for your answer")
	}
	if b.fw != "" && len(ds) == 0 {
		s.Live = append(s.Live, Bad(i18n.T("the kernel couldn't load its Bluetooth firmware")))
	}
	quiet := !b.listening(c) && len(ds) == 0
	s.Actions = []Action{{Label: i18n.T("Look again"), Does: i18n.T("listen for another 30 seconds"), Off: !quiet},
		{Label: i18n.T("Nothing shows up"), Does: i18n.T("it's on and discoverable, and isn't listed"), Off: !quiet}}
	return s
}

// btLine is a device as the screen shows it: its name ("unnamed" without one, cut to btNameMax),
// then its kind (or maker) and signal: "WH-1000XM4", "audio · -61 dBm". Short, so a list of them
// keeps to one row each; the radio (classic or LE) is in the report.
func btLine(d BTDevice) (name, rest string) {
	name = btClean(d.Name)
	if name == "" {
		name = i18n.T("unnamed")
	}
	if r := []rune(name); len(r) > btNameMax {
		name = string(r[:btNameMax-1]) + "…"
	}
	kind := i18n.T("device")
	if k, ok := btKinds[d.Kind]; ok {
		kind = i18n.S(k)
	} else if d.Maker != "" {
		kind = d.Maker
	}
	return name, i18n.T("%[1]s · %[2]d dBm", kind, d.RSSI)
}

// btNameMax is the most of a device's name the list shows.
const btNameMax = 16

// ── the Fake's adapter ────────────────────────────────────────────────────────

// The Fake keeps its Bluetooth adapter in files under fakeBT/<hci>/: info, power (0/1), le
// (0: Low Energy off, the scan switches it on), refuse (why it won't power on), noscan (why it
// won't discover), and found/<key> (a device heard: name|kind|maker|le|rssi), so a Demo
// scripts it with SetAt like anything else.
const fakeBT = "/fake/bluetooth/"

// FakeBluetooth gives the Fake an adapter (it must also have /sys/class/bluetooth/<hci>).
func FakeBluetooth(f *Fake, hci, info string, on bool) {
	f.Set(fakeBT+hci+"/info", info)
	f.Set(fakeBT+hci+"/power", map[bool]string{true: "1", false: "0"}[on])
}

// FakeBluetoothDevice makes the Fake's adapter hear a device from t on.
func FakeBluetoothDevice(f *Fake, t float64, hci string, d BTDevice) {
	f.SetAt(t, fakeBT+hci+"/found/"+d.Key, fmt.Sprintf("%s|%s|%s|%t|%d", d.Name, d.Kind, d.Maker, d.LE, d.RSSI))
}

type fakeScan struct {
	f      *Fake
	hci    string
	wasOn  bool
	leOn   bool
	closed bool
}

// ErrNoFakeAdapter: the Fake has no scripted adapter of that name.
var ErrNoFakeAdapter = errors.New("no such adapter on the scripted Mac")

func (f *Fake) BluetoothScan(hci string) BTScan {
	s := &fakeScan{f: f, hci: hci, wasOn: f.Read(fakeBT+hci+"/power") == "1", leOn: f.Read(fakeBT+hci+"/le") == "0"}
	if s.leOn {
		f.Set(fakeBT+hci+"/le", "1")
	}
	if f.Read(fakeBT+hci+"/refuse") == "" && f.Read(fakeBT+hci+"/info") != "" {
		f.Set(fakeBT+hci+"/power", "1")
	}
	return s
}

func (s *fakeScan) State() BTState {
	d := fakeBT + s.hci + "/"
	if s.f.Read(d+"info") == "" {
		return BTState{Err: ErrNoFakeAdapter}
	}
	st := BTState{Info: s.f.Read(d + "info"), WasOn: s.wasOn, LEOn: s.leOn, PowerErr: s.f.Read(d + "refuse"), ScanErr: s.f.Read(d + "noscan")}
	st.Powered = s.f.Read(d+"power") == "1"
	st.Scanning = st.Powered && !s.closed && st.ScanErr == ""
	if st.Scanning {
		st.Mode = btModes[mgmtDiscoverAll]
	}
	for _, p := range s.f.Glob(d + "found/*") {
		v := strings.Split(s.f.Read(p), "|")
		if len(v) != 5 {
			continue
		}
		rssi, _ := strconv.Atoi(v[4])
		st.Found = append(st.Found, BTDevice{Key: path.Base(p), Name: v[0], Kind: v[1], Maker: v[2], LE: v[3] == "true", RSSI: rssi})
	}
	return st
}

func (s *fakeScan) Again() {}

func (s *fakeScan) Close() {
	if !s.closed {
		s.closed = true
		s.f.Set(fakeBT+s.hci+"/power", map[bool]string{true: "1", false: "0"}[s.wasOn])
		if s.leOn {
			s.f.Set(fakeBT+s.hci+"/le", "0")
		}
	}
}
