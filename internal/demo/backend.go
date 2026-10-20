package demo

import (
	"strings"
	"sync"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/omaboot/boot-live/internal/checks"
	"github.com/omaboot/boot-live/internal/handson"
	"github.com/omaboot/boot-live/internal/i18n"
	"github.com/omaboot/boot-live/internal/pace"
	"github.com/omaboot/boot-live/internal/send"
	"github.com/omaboot/boot-live/internal/ui"
)

// Backend plays the demo's run behind the real screens: the same messages as the real run's
// backend, from a script and timers. Nothing touches the machine.
type Backend struct {
	// Audible: Boot's sounds play through the computer's speakers (the demo run; never the
	// screenshots')
	Audible bool

	send func(tea.Msg)
	out  chan tea.Msg // messages in order, sent off the program's goroutine (Send from Update would block)

	mu   sync.Mutex
	ns   ui.NetState
	gpu  chan struct{}
	code string

	// the console's (console.go): notes, recorded commands, results set by hand
	notes   []string
	console []map[string]string
	forced  map[string]string
	handsOn map[string]pace.Status // the hands-on results so far (Record)
}

// NewBackend makes the demo's backend.
func NewBackend() *Backend {
	return &Backend{gpu: make(chan struct{}, 1), out: make(chan tea.Msg, 256)}
}

// SetSend gives the backend the program's Send.
func (b *Backend) SetSend(f func(tea.Msg)) {
	b.send = f
	go func() {
		for m := range b.out {
			f(m)
		}
	}()
}

func (b *Backend) post(m tea.Msg) { b.out <- m }

// update changes the network state and sends it.
func (b *Backend) update(f func(n *ui.NetState)) {
	b.mu.Lock()
	defer b.mu.Unlock()
	f(&b.ns)
	b.post(ui.NetMsg{Net: b.ns}) // queued under the lock: states arrive in the order they were made
}

// after runs f after d, off the program's goroutine.
func after(d time.Duration, f func()) { time.AfterFunc(d, f) }

// Pick does nothing: the demo has one configuration.
func (b *Backend) Pick(string) {}

// NetStart: no cable, no phone; a Wi-Fi scan finds the prototype's networks.
func (b *Backend) NetStart() {
	b.mu.Lock()
	online := b.ns.Online
	b.mu.Unlock()
	if online {
		b.update(func(*ui.NetState) {})
		return
	}
	b.update(func(n *ui.NetState) {
		n.Phase, n.Ethernet, n.Tether, n.WifiChip, n.CanScan = ui.NetScanning, i18n.T("no adapter or cable"), i18n.T("no phone sharing its connection"), WifiChip, true
	})
	after(1900*time.Millisecond, func() { b.update(func(n *ui.NetState) { n.Phase, n.Networks = ui.NetList, Networks }) })
}

// NetStop does nothing: the demo's network needs no watching.
func (b *Backend) NetStop() {}

// NetScan finds the same networks again.
func (b *Backend) NetScan() {
	b.update(func(n *ui.NetState) { n.Phase = ui.NetScanning })
	after(1500*time.Millisecond, func() { b.update(func(n *ui.NetState) { n.Phase, n.Networks = ui.NetList, Networks }) })
}

// NetClear drops the last error: a network is picked to join.
func (b *Backend) NetClear() { b.update(func(n *ui.NetState) { n.Err = "" }) }

// NetConnect takes any password: joining, DHCP, online, then the catalog refresh. Online
// through the cable, the cable is turned off first.
func (b *Backend) NetConnect(ssid, _ string) {
	b.update(func(n *ui.NetState) {
		if n.Online && n.Via == "Ethernet" {
			n.Parked, n.To = n.Via, "Wi-Fi"
			n.Ethernet, n.EthLink = ui.OffText(*n, n.Iface), ui.LinkOff
		}
		n.Phase, n.SSID, n.Err = ui.NetJoining, ssid, ""
	})
	after(1100*time.Millisecond, func() { b.update(func(n *ui.NetState) { n.Phase = ui.NetDHCP }) })
	after(2200*time.Millisecond, func() {
		b.update(func(n *ui.NetState) {
			n.Phase, n.Online, n.Via, n.Iface, n.IP, n.Static, n.To = ui.NetList, true, "Wi-Fi", "wlan0", IP, false, ""
			n.Info = ssid + "  ·  " + Band + "  ·  " + IP
		})
	})
	after(3000*time.Millisecond, func() {
		b.update(func(n *ui.NetState) {
			n.Refreshed = i18n.T("catalog snapshot refreshed: %s · %d configurations · from %s", RefreshedDate+" (c2c40d7b)", RefreshedConfs, "DoesItOmarchy.com")
			n.Stick = i18n.T("on OmaBoot? device: %s · %d configurations · not used", "2026-10-09 (8f31a0c2)", RefreshedConfs-4)
		})
	})
}

// NetSwitch switches back to the cable turned off for Wi-Fi (the demo has no phone): Wi-Fi
// disconnects once the cable is up.
func (b *Backend) NetSwitch(via string) {
	b.update(func(n *ui.NetState) {
		n.Phase, n.To, n.Err = ui.NetSwitching, via, ""
		n.Ethernet, n.EthLink = i18n.T("%s: cable in, asking for an address…", StaticIface), ui.LinkCarrier
	})
	after(1200*time.Millisecond, func() {
		b.update(func(n *ui.NetState) {
			n.Phase, n.Online, n.Via, n.Iface, n.IP, n.Info, n.SSID, n.To = ui.NetList, true, "Ethernet", StaticIface, IP, StaticIface+"  ·  "+IP, "", ""
			n.Ethernet, n.EthLink, n.Parked = i18n.T("%s: connected", StaticIface)+"  ·  "+IP, ui.LinkConnected, "Wi-Fi"
		})
	})
}

// StaticIface is the demo's USB Ethernet adapter.
const StaticIface = "enp0s20u1"

// StaticTarget: the joined Wi-Fi, else a USB Ethernet adapter with no DHCP on its cable.
func (b *Backend) StaticTarget() ui.StaticTarget {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.ns.Online && b.ns.Via == "Wi-Fi" {
		return ui.StaticTarget{Iface: "wlan0", What: i18n.T("Wi-Fi, joined %s", b.ns.SSID)}
	}
	return ui.StaticTarget{Iface: StaticIface, What: i18n.T("USB Ethernet adapter  ·  cable in, no address offered")}
}

// NetStatic pretends to set the address: a moment later the Mac is online with it.
func (b *Backend) NetStatic(iface, addr, gw, _ string) {
	ip, _, _ := strings.Cut(addr, "/")
	b.update(func(n *ui.NetState) { n.Phase, n.Iface, n.Err = ui.NetApplying, iface, "" })
	after(1800*time.Millisecond, func() {
		b.update(func(n *ui.NetState) {
			n.Phase, n.Online, n.Iface, n.IP, n.Gateway, n.Static = ui.NetList, true, iface, ip, gw, true
			n.Via = "Ethernet"
			if iface == "wlan0" {
				n.Via = "Wi-Fi"
			} else {
				n.Ethernet, n.EthLink = i18n.T("%s: connected", iface)+"  ·  "+ip, ui.LinkConnected
			}
			n.Info = iface + "  ·  " + ip
		})
	})
}

// Quick plays the demo's checks at their start and finish times, as a real run reports them:
// each check's start (RunningMsg), then its result. Before the first graphics check it waits for
// the GPU notice, and everything after it starts and finishes that much later.
func (b *Backend) Quick() (int, float64) {
	expect := Checks[len(Checks)-1].Finish
	go func() {
		start, shift, began, noticed := time.Now(), 0.0, -1.0, false
		until := func(s float64) { time.Sleep(time.Until(start.Add(time.Duration(s * float64(time.Second))))) }
		for _, c := range Checks {
			if !noticed && strings.HasPrefix(c.ID, "graphics.") {
				noticed = true
				b.post(ui.GPUNoticeMsg{ID: c.ID, Label: c.Label})
				<-b.gpu
				shift = time.Since(start).Seconds() - c.Begin
			}
			c.Begin += shift
			if c.Begin > began { // a new check (thermal.fans comes with the sensors)
				began = c.Begin
				until(c.Begin)
				b.post(ui.RunningMsg{ID: c.ID, Label: c.Label, Effect: c.Effect, At: c.Begin})
			}
			at := c.Finish + shift
			until(at)
			c.Finish = at
			b.post(ui.CheckMsg{Check: c, Remaining: max(0, expect+shift-time.Since(start).Seconds())})
		}
		b.post(Summary())
	}()
	return len(Checks), expect
}

// GPUGo lets the graphics check run.
func (b *Backend) GPUGo() {
	select {
	case b.gpu <- struct{}{}:
	default:
	}
}

// Submit is received after a moment, with the prototype's code; a second one is refused.
func (b *Backend) Submit(string, string) {
	b.mu.Lock()
	sent := b.code != ""
	b.mu.Unlock()
	if sent {
		b.post(ui.SentMsg{Err: i18n.T("this run's report was already sent")})
		return
	}
	after(2400*time.Millisecond, func() {
		b.mu.Lock()
		b.code = Report
		b.mu.Unlock()
		b.post(ui.SentMsg{Code: Report, Page: Site + "/report/" + Report})
	})
}

// HandsOnSetup is the demo's hands-on tests: every built test with a Demo script, each visit
// run against a fresh scripted Mac (handson.DemoMac plus the test's own script).
func HandsOnSetup() handson.Setup {
	env := &handson.Env{Ports: handson.DemoAirPorts()} // the snapshot built in has no port maps yet
	var plan []handson.Spec
	for _, s := range handson.Plan(env) {
		if s.Demo != nil {
			plan = append(plan, s)
		}
	}
	return handson.Setup{Env: env, Plan: plan, IO: func(s handson.Spec) handson.IO {
		f := handson.NewFake()
		handson.DemoMac(f)
		s.Demo(f)
		return f
	}}
}

// HandsOn plays the hands-on tests on the scripted Mac.
func (b *Backend) HandsOn() handson.Setup { return HandsOnSetup() }

// Record brings the summary's groups up to date with the hands-on results.
func (b *Backend) Record(rs []checks.Result) {
	b.mu.Lock()
	b.handsOn = map[string]pace.Status{}
	for _, x := range rs {
		b.handsOn[x.ID] = x.Pace()
	}
	b.mu.Unlock()
	b.post(b.summary())
}

// summary is the demo's summary with the hands-on results and the results set by hand in place.
func (b *Backend) summary() ui.QuickDoneMsg {
	b.mu.Lock()
	over := map[string]pace.Status{}
	for k, v := range b.handsOn {
		over[k] = v
	}
	for k, v := range b.forced {
		over[k] = checks.Result{Status: v}.Pace()
	}
	b.mu.Unlock()
	s := Summary()
	s.Groups = GroupsWith(over)
	return s
}

// Resume: the demo has no copy to pick up.
func (b *Backend) Resume(bool) {}

// Woke: the demo keeps no note.
func (b *Backend) Woke() {}

// The demo has no stick, so no run plan.
func (b *Backend) PlanOff()        {}
func (b *Backend) PlanStop(string) {}
func (b *Backend) PlanStart(string) (*ui.PlanFacts, []string) {
	return nil, []string{"no run plan in the demo"}
}
func (b *Backend) PlanNote(string, string) {}
func (b *Backend) PlanSerial(string)       {}
func (b *Backend) PlanText(bool) ([]string, error) {
	return []string{"no run plan in the demo"}, nil
}

// Keep pretends to save a copy before a test that sleeps.
func (b *Backend) Keep(string, string, ui.KeepInfo) {
	after(900*time.Millisecond, func() { b.post(ui.KeptMsg{Path: send.FileName(MacID, "", time.Now())}) })
}

// Save pretends to write the report to the stick.
func (b *Backend) Save(string, string) {
	b.mu.Lock()
	code := b.code
	b.mu.Unlock()
	after(800*time.Millisecond, func() { b.post(ui.SavedMsg{Path: send.FileName(MacID, code, time.Now())}) })
}
