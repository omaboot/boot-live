package app

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/doesitomarchy/doesitomarchy/pkg/report"

	"github.com/omaboot/boot-live/internal/boot"
	"github.com/omaboot/boot-live/internal/checks"
	"github.com/omaboot/boot-live/internal/handson"
	"github.com/omaboot/boot-live/internal/i18n"
	"github.com/omaboot/boot-live/internal/network"
	"github.com/omaboot/boot-live/internal/send"
	"github.com/omaboot/boot-live/internal/ui"
)

// Backend runs the real work behind the screens (ui.Backend). Every method returns at once;
// goroutines report back through Send.
type Backend struct {
	R    *Run
	send func(tea.Msg)
	out  chan tea.Msg

	mu      sync.Mutex
	ns      ui.NetState // the network step as last sent
	parked  []parked    // the connections turned off for the one in use, the one in use before first
	stopNet context.CancelFunc
	gpuGo   chan struct{}

	// the Mac's IO for the hands-on tests and sleepcheck (system), and the naps' kernel lines
	// kept for bugreport (sleepdiag.go)
	sysOnce sync.Once
	sys     *handson.System
	napMu   sync.Mutex
	naps    []nap

	netLogMu sync.Mutex
	netLines []string // the network log, for bugreport (netlog.go)

	snd soundState // Boot's sounds (sounds.go)
}

// parked is a connection turned off for another. A wired link is remembered by its kind and USB
// device, not only its name: an Android phone gets a new random MAC address, so a new enx… name,
// every time it's plugged in.
type parked struct {
	kind  network.Kind
	iface string // its name when it was turned off ("" for Wi-Fi)
	usb   string
}

// is reports whether l is the link p turned off (or the same device back under a new name).
func (p parked) is(l network.Link) bool {
	return p.kind == l.Kind && (p.iface == l.Name || p.usb != "" && p.usb == l.USB)
}

// NewBackend wraps a run for the screens.
func NewBackend(r *Run) *Backend {
	return &Backend{R: r, gpuGo: make(chan struct{}, 1), out: make(chan tea.Msg, 256)}
}

// SetSend gives the backend the program's Send. Messages go through a queue, in order, from
// one goroutine: Send blocks until the program takes the message.
func (b *Backend) SetSend(f func(tea.Msg)) {
	b.send = f
	go func() {
		for m := range b.out {
			f(m)
		}
	}()
	go b.watchStick()
	go b.watchLink()
	go b.R.measureDrive()
}

func (b *Backend) post(m tea.Msg) { b.out <- m }

// Pick records the picked configuration.
func (b *Backend) Pick(config string) { b.R.Pick(config) }

// update changes the network state and sends it.
func (b *Backend) update(f func(n *ui.NetState)) {
	b.mu.Lock()
	defer b.mu.Unlock()
	f(&b.ns)
	n := b.ns
	n.Networks = append([]ui.WifiNet(nil), b.ns.Networks...)
	b.post(ui.NetMsg{Net: n}) // queued under the lock: states arrive in the order they were made
}

func viaName(k network.Kind) string {
	switch k {
	case network.WiFi:
		return "Wi-Fi"
	case network.Tether:
		return "Phone"
	}
	return "Ethernet"
}

func viaKind(via string) network.Kind {
	switch via {
	case "Wi-Fi":
		return network.WiFi
	case "Phone":
		return network.Tether
	}
	return network.Ethernet
}

// NetStart looks at the wired links and the default route, then scans for Wi-Fi. While the step
// is on screen it keeps watching the links, so a cable or a phone plugged in later is noticed
// and the Ethernet and tethering lines follow what happens. Coming back to the step keeps the
// connection: it only refreshes the lines and watches again.
func (b *Backend) NetStart() {
	ctx, cancel := context.WithCancel(b.R.ctx)
	b.mu.Lock()
	if b.stopNet != nil {
		b.stopNet()
	}
	b.stopNet = cancel
	online := b.ns.Online
	b.mu.Unlock()
	go func() {
		r := b.R
		if online {
			b.lines()
			b.watch(ctx)
			return
		}
		wifiLink := describeLinks(r, b.state()).wifi
		chip := wifiChip(r)
		b.update(func(n *ui.NetState) {
			n.Phase, n.WifiChip, n.CanScan = ui.NetScanning, chip, wifiLink
			if !wifiLink {
				n.NoWifi = noWifiText(r)
			}
		})
		b.lines()
		if b.online(ctx, 3*time.Second) {
			b.watch(ctx)
			return
		}
		if n := b.state(); n.EthLink == ui.LinkCarrier || n.TetLink == ui.LinkCarrier && n.PhoneNeed == network.NeedNothing {
			if b.online(ctx, 12*time.Second) {
				b.watch(ctx)
				return
			}
		}
		if wifiLink {
			b.scan(ctx)
		} else {
			b.update(func(n *ui.NetState) { n.Phase = ui.NetList })
		}
		b.watch(ctx)
	}()
}

// NetStop stops watching the links: the step is left. A connection stays up.
func (b *Backend) NetStop() {
	b.mu.Lock()
	if b.stopNet != nil {
		b.stopNet()
		b.stopNet = nil
	}
	b.mu.Unlock()
}

func (b *Backend) state() ui.NetState {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.ns
}

// lines refreshes the Ethernet and phone lines from the links as they are now.
func (b *Backend) lines() {
	ls := describeLinks(b.R, b.state())
	b.update(func(n *ui.NetState) {
		n.Ethernet, n.EthLink, n.Tether, n.TetLink = ls.eth.text, ls.eth.link, ls.phone.text, ls.phone.link
		n.Phone, n.PhoneApple, n.PhoneNeed = ls.phone.name, ls.phone.apple, ls.phone.need
	})
}

// watch keeps the found lines live every two seconds until the step is left: a cable or phone
// plugged in shows straight away and brings the Mac online by itself (networkd's DHCP); when
// another link takes the default route (a cable plugged in beats a phone or Wi-Fi: the lowest
// metric wins) the step follows it; a wired connection whose cable or phone is pulled is dropped.
func (b *Backend) watch(ctx context.Context) {
	r := b.R
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(2 * time.Second):
		}
		n := b.state()
		if n.Phase == ui.NetJoining || n.Phase == ui.NetDHCP || n.Phase == ui.NetApplying || n.Phase == ui.NetSwitching {
			continue
		}
		b.lines()
		n = b.state()
		switch {
		case !n.Online && (n.EthLink == ui.LinkCarrier || n.EthLink == ui.LinkReady || n.TetLink == ui.LinkCarrier || n.TetLink == ui.LinkReady):
			if st := network.Online(ctx, r.Opt.Root, r.Opt.Target.URL); st.Online && st.Kind != network.WiFi {
				b.up(st, "")
			}
		case n.Online && !r.Fixture(): // a fixture's routes never change
			rt, ok := network.DefaultRoute(r.Opt.Root)
			switch {
			case !ok && n.Via != "Wi-Fi":
				b.down()
			case ok && rt.Iface != n.Iface:
				if st := network.Online(ctx, r.Opt.Root, r.Opt.Target.URL); st.Online {
					b.up(st, "")
				} else if n.Via != "Wi-Fi" {
					b.down()
				}
			}
		}
	}
}

// online waits up to d for a working connection; when it comes, the step is up.
func (b *Backend) online(ctx context.Context, d time.Duration) bool {
	wctx, cancel := context.WithTimeout(ctx, d)
	defer cancel()
	st := network.Wait(wctx, b.R.Opt.Root, b.R.Opt.Target.URL, time.Second)
	if !st.Online {
		return false
	}
	b.up(st, "")
	return true
}

// up marks the Mac online, asks boot.watch whether the connection wants a sign-in page, and
// refreshes the catalog snapshot.
func (b *Backend) up(st network.State, ssid string) { b.upWith(st, ssid, "", "") }

func (b *Backend) upWith(st network.State, ssid, static, gw string) {
	r := b.R
	r.mu.Lock()
	r.net = st
	r.netFacts.Online, r.netFacts.Iface = true, st.Iface
	if ssid != "" {
		r.netFacts.Joined = ssid
	}
	r.mu.Unlock()
	info := network.Shown(st.Iface, st.Kind)
	if ssid != "" {
		info = ssid
	}
	if st.IP != "" {
		info += "  ·  " + st.IP
	}
	refreshed := false
	b.update(func(n *ui.NetState) {
		n.Phase, n.Online, n.Via, n.Iface, n.IP, n.Info, n.Err, n.SignIn = ui.NetList, true, viaName(st.Kind), st.Iface, st.IP, info, "", false
		n.Static, n.Gateway, n.To = static != "", gw, ""
		if ssid != "" {
			n.SSID = ssid
		}
		refreshed = n.Refreshed != ""
	})
	b.lines()
	go func() { // once each time a connection comes up; it never takes the Mac offline
		if why := r.SignInPage(); why != "" {
			b.update(func(n *ui.NetState) {
				if n.Online && n.Iface == st.Iface { // still the same connection
					n.SignIn = true
				}
			})
		}
	}()
	if !r.CanRefresh() || refreshed { // once a run: switching connections needn't check again
		return
	}
	// it runs by itself, up to 15 s; the line says so meanwhile
	checking := i18n.T("checking %s for a newer catalog snapshot", r.Site())
	b.update(func(n *ui.NetState) { n.Refreshed, n.Checking = checking, true })
	go func() {
		s, ok, stick := r.RefreshSnapshot()
		b.update(func(n *ui.NetState) { n.Refreshed, n.Checking, n.RefreshFailed, n.Stick = s, false, !ok, stick })
	}()
}

// down marks the Mac offline again (its wired connection went away).
func (b *Backend) down() {
	r := b.R
	r.mu.Lock()
	r.net = network.State{}
	r.netFacts.Online, r.netFacts.Iface = false, ""
	r.mu.Unlock()
	b.update(func(n *ui.NetState) {
		n.Online, n.Via, n.IP, n.Info, n.Static, n.SignIn = false, "", "", "", false, false
	})
	b.lines()
}

// scan runs a Wi-Fi scan and shows the list; the Wi-Fi test gets what it heard too.
func (b *Backend) scan(ctx context.Context) ([]network.AP, error) {
	r := b.R
	b.update(func(n *ui.NetState) { n.Phase = ui.NetScanning })
	w, err := r.wifiDev()
	var aps []network.AP
	if err == nil {
		sctx, cancel := context.WithTimeout(ctx, 20*time.Second)
		aps, err = w.Scan(sctx)
		cancel()
	}
	r.mu.Lock()
	r.aps = aps
	r.netFacts.Scanned, r.netFacts.Networks = err == nil, len(aps)
	if err != nil {
		r.netFacts.ScanErr = err.Error()
	}
	r.mu.Unlock()
	if err != nil {
		b.netLog("scan failed: %v", err)
	} else {
		b.netLog("scan: %d networks", len(aps))
	}
	b.update(func(n *ui.NetState) {
		n.Phase, n.Networks, n.NoWifi = ui.NetList, nil, ""
		for _, a := range aps {
			sec := a.Security
			switch sec {
			case "psk":
				sec = "WPA2"
			case "8021x":
				sec = "802.1X"
			case "open":
				sec = i18n.M("open")
			}
			n.Networks = append(n.Networks, ui.WifiNet{SSID: clean(a.SSID), Bars: a.Bars(), Security: sec})
		}
		switch {
		case err != nil:
			n.NoWifi = i18n.T("scan failed: %s", err.Error())
		case len(aps) == 0:
			n.NoWifi = i18n.T("no networks in range")
		}
	})
	return aps, err
}

// NetScan scans again.
func (b *Backend) NetScan() { go b.scan(b.R.ctx) }

// NetClear drops the last join's error: a network is picked to join.
func (b *Backend) NetClear() {
	b.netLog("network step: a network picked")
	b.update(func(n *ui.NetState) { n.Err = "" })
}

// NetConnect joins a Wi-Fi network and waits for an address.
func (b *Backend) NetConnect(ssid, password string) {
	go func() {
		r := b.R
		r.mu.Lock()
		var ap *network.AP
		for i := range r.aps {
			if clean(r.aps[i].SSID) == ssid {
				ap = &r.aps[i]
			}
		}
		scanned := len(r.aps)
		r.mu.Unlock()
		if ap == nil {
			b.netLog("network step: the network asked for isn't in the last scan (%d networks; name given: %v)", scanned, ssid != "")
			b.update(func(n *ui.NetState) { n.Phase, n.Err = ui.NetList, i18n.T("%s is gone: scan again", ssid) })
			return
		}
		b.netLog("network step: joining a %s network (%d/4 bars), password given: %v", ap.Security, ap.Bars(), password != "")
		b.update(func(n *ui.NetState) { n.Phase, n.SSID, n.Err, n.To = ui.NetJoining, ssid, "", "Wi-Fi" })
		// online through a cable or a phone: the person confirmed the switch, so the wired links
		// go down and Wi-Fi carries the run alone (any of them up would beat it: its route
		// metric is the highest); they come back if the join fails
		r.mu.Lock()
		cur := r.net
		r.mu.Unlock()
		var off []parked
		if cur.Online && cur.Kind != network.WiFi && !r.Fixture() {
			off = b.parkWired(cur.Iface)
		}
		unpark := func(why string) {
			if len(off) == 0 {
				b.update(func(n *ui.NetState) { n.Phase, n.Err, n.To = ui.NetList, why, "" })
				return
			}
			b.restore(off, why)
		}
		w, err := r.wifiDev()
		var iface string
		if err == nil {
			cctx, cancel := context.WithTimeout(r.ctx, 40*time.Second)
			err = w.Connect(cctx, *ap, password)
			cancel()
			iface = w.Device()
		}
		password = ""
		if err != nil {
			b.netLog("network step: the join failed: %v", err)
			msg := i18n.T("couldn't join %s: %s", ssid, err.Error())
			if errors.Is(err, network.ErrPassword) {
				msg = i18n.T("%s refused the password", ssid)
			}
			unpark(msg)
			return
		}
		b.update(func(n *ui.NetState) { n.Phase = ui.NetDHCP })
		wctx, cancel := context.WithTimeout(r.ctx, 30*time.Second)
		st := network.Wait(wctx, r.Opt.Root, r.Opt.Target.URL, time.Second)
		cancel()
		if !st.Online {
			b.netLog("network step: joined, but not online within 30 s (no address from DHCP, or no way out)")
			unpark(i18n.T("joined %s, but got no address (DHCP)", ssid))
			return
		}
		b.netLog("network step: online on %s", st.Iface)
		if st.Kind == "" {
			st.Kind = network.WiFi
		}
		b.setParked(off)
		r.mu.Lock()
		r.netFacts.JoinedIface = iface
		r.mu.Unlock()
		b.up(st, ssid)
	}()
}

// parkWired turns off the wired links that carry or could carry the run, the one in use (cur)
// first, and returns them; the step's Parked follows.
func (b *Backend) parkWired(cur string) []parked {
	r := b.R
	var off []parked
	for _, l := range network.Links(r.Opt.Root) {
		if l.Kind != network.Ethernet && l.Kind != network.Tether || l.Down || l.Name != cur && network.LinkAddr(r.Opt.Root, l.Name) == "" {
			continue
		}
		pctx, cancel := context.WithTimeout(r.ctx, 10*time.Second)
		err := network.SetLink(pctx, l.Name, false)
		cancel()
		if err != nil {
			continue
		}
		p := parked{kind: l.Kind, iface: l.Name, usb: l.USB}
		if l.Name == cur {
			off = append([]parked{p}, off...)
		} else {
			off = append(off, p)
		}
	}
	b.setParked(off)
	return off
}

// setParked records the connections turned off for the one in use.
func (b *Backend) setParked(off []parked) {
	b.mu.Lock()
	b.parked = off
	b.mu.Unlock()
	b.update(func(n *ui.NetState) {
		n.Parked = ""
		if len(off) > 0 {
			n.Parked = viaName(off[0].kind)
		}
	})
}

// NetSwitch switches to the Ethernet or phone line's connection (a link that's ready, or one
// turned off for the one in use): a wired one in use is turned off first (a cable's route would
// beat a phone's), Wi-Fi only once the new one carries the run (any wired link beats it). If the
// new one doesn't come online, the old one comes back.
func (b *Backend) NetSwitch(via string) {
	go func() {
		r := b.R
		kind := viaKind(via)
		b.update(func(n *ui.NetState) { n.Phase, n.To, n.Err = ui.NetSwitching, via, "" })
		r.mu.Lock()
		cur := r.net
		r.mu.Unlock()
		target, ok := b.linkFor(kind)
		if !ok {
			b.update(func(n *ui.NetState) { n.Phase, n.To, n.Err = ui.NetList, "", i18n.T("%s is gone", ui.Used(*n, via)) })
			return
		}
		if r.Fixture() { // a fixture only pretends
			time.Sleep(1500 * time.Millisecond)
			b.setParked(nil)
			b.up(network.State{Online: true, Iface: target.Name, Kind: kind, IP: "192.0.2.10"}, "")
			return
		}
		var off []parked
		if cur.Online && cur.Kind != network.WiFi && cur.Iface != target.Name {
			pctx, cancel := context.WithTimeout(r.ctx, 10*time.Second)
			err := network.SetLink(pctx, cur.Iface, false)
			cancel()
			if err != nil {
				b.update(func(n *ui.NetState) { n.Phase, n.To, n.Err = ui.NetList, "", err.Error() })
				return
			}
			for _, l := range network.Links(r.Opt.Root) {
				if l.Name == cur.Iface {
					off = append(off, parked{kind: l.Kind, iface: l.Name, usb: l.USB})
				}
			}
			b.setParked(off)
		}
		why := i18n.T("%s didn't come online", ui.Used(b.state(), via))
		if target.Down {
			uctx, cancel := context.WithTimeout(r.ctx, 10*time.Second)
			err := network.SetLink(uctx, target.Name, true)
			cancel()
			if err != nil {
				why = err.Error()
			}
		}
		st, ok := b.waitKind(kind, 30*time.Second)
		if !ok {
			if len(off) == 0 {
				b.update(func(n *ui.NetState) { n.Phase, n.To, n.Err = ui.NetList, "", why })
				return
			}
			b.restore(off, why)
			return
		}
		if cur.Online && cur.Kind == network.WiFi { // the new one carries the run: Wi-Fi goes
			if w, err := r.wifiDev(); err == nil {
				dctx, cancel := context.WithTimeout(r.ctx, 10*time.Second)
				w.Disconnect(dctx)
				cancel()
			}
			off = append(off, parked{kind: network.WiFi})
		}
		b.setParked(off)
		b.up(st, "")
		if cur.Kind == network.WiFi {
			b.update(func(n *ui.NetState) { n.SSID = "" })
		}
	}()
}

// linkFor is the link a line stands for now: one turned off for the one in use (found by its
// USB device if it came back under a new name), else one that's off, has an address, or a
// carrier, in that order.
func (b *Backend) linkFor(kind network.Kind) (network.Link, bool) {
	b.mu.Lock()
	pk := append([]parked(nil), b.parked...)
	b.mu.Unlock()
	root := b.R.Opt.Root
	var best network.Link
	rank := 0
	for _, l := range network.Links(root) {
		if l.Kind != kind {
			continue
		}
		k := 1
		switch {
		case isParked(pk, l):
			k = 5
		case l.Down:
			k = 4
		case network.LinkAddr(root, l.Name) != "":
			k = 3
		case l.Carrier:
			k = 2
		}
		if k > rank {
			best, rank = l, k
		}
	}
	return best, rank > 0
}

func isParked(pk []parked, l network.Link) bool {
	for _, p := range pk {
		if p.is(l) {
			return true
		}
	}
	return false
}

// waitKind waits up to d for the Mac to be online through a link of this kind.
func (b *Backend) waitKind(kind network.Kind, d time.Duration) (network.State, bool) {
	r := b.R
	ctx, cancel := context.WithTimeout(r.ctx, d)
	defer cancel()
	for {
		if st := network.Online(ctx, r.Opt.Root, r.Opt.Target.URL); st.Online && st.Kind == kind {
			return st, true
		}
		select {
		case <-ctx.Done():
			return network.State{}, false
		case <-time.After(time.Second):
		}
	}
}

// restore brings back the wired links turned off for a switch that failed, and waits for the Mac
// to be online again; why is the problem to show. A phone is found by its USB device: it may be
// back under a new name.
func (b *Backend) restore(off []parked, why string) {
	r := b.R
	links := network.Links(r.Opt.Root)
	for _, p := range off {
		for _, l := range links {
			if p.kind != network.WiFi && p.is(l) {
				uctx, cancel := context.WithTimeout(r.ctx, 10*time.Second)
				network.SetLink(uctx, l.Name, true)
				cancel()
			}
		}
	}
	back := ui.Used(b.state(), viaName(off[0].kind))
	wctx, cancel := context.WithTimeout(r.ctx, 30*time.Second)
	st := network.Wait(wctx, r.Opt.Root, r.Opt.Target.URL, time.Second)
	cancel()
	b.setParked(nil)
	if st.Online {
		b.up(st, "")
		b.update(func(n *ui.NetState) {
			n.SSID = ""
			if why != "" {
				n.Err = why + "  ·  " + i18n.T("back on %s", back)
			}
		})
		return
	}
	b.update(func(n *ui.NetState) {
		n.Phase, n.Online, n.To, n.Err = ui.NetList, false, "", why
		if why == "" {
			n.Err = i18n.T("%s didn't come back online", back)
		}
	})
	b.lines()
}

// StaticTarget is the interface a static address goes on: the wired link with a cable in, else
// the Wi-Fi interface if a network is joined, else the first wired link.
func (b *Backend) StaticTarget() ui.StaticTarget {
	r := b.R
	n := b.state()
	var first, carrier, wifi *network.Link
	links := network.Links(r.Opt.Root)
	for i := range links {
		l := &links[i]
		switch l.Kind {
		case network.Ethernet:
			if first == nil {
				first = l
			}
			if l.Carrier && carrier == nil {
				carrier = l
			}
		case network.WiFi:
			if wifi == nil {
				wifi = l
			}
		}
	}
	what := func(l *network.Link) string {
		kind := i18n.T("Ethernet")
		if l.Driver != "" && strings.Contains(r.Opt.Root.Read("/sys/class/net/"+l.Name+"/device/uevent"), "usb") {
			kind = i18n.T("USB Ethernet adapter")
		}
		state := i18n.T("no cable")
		switch {
		case n.Online && n.Iface == l.Name:
			state = i18n.T("connected")
		case l.Carrier:
			state = i18n.T("cable in, no address offered")
		}
		return kind + "  ·  " + state
	}
	switch {
	case carrier != nil:
		return ui.StaticTarget{Iface: carrier.Name, What: what(carrier)}
	case wifi != nil && n.Online && n.Via == "Wi-Fi":
		return ui.StaticTarget{Iface: wifi.Name, What: i18n.T("Wi-Fi, joined %s", n.SSID)}
	case first != nil:
		return ui.StaticTarget{Iface: first.Name, What: what(first)}
	}
	return ui.StaticTarget{What: i18n.T("no Ethernet port or adapter, and no Wi-Fi network joined")}
}

// NetStatic sets a static address on iface (a runtime networkd file, then networkctl), and waits
// for the connection like the DHCP path. A fixture run only pretends.
func (b *Backend) NetStatic(iface, addr, gw, dns string) {
	go func() {
		r := b.R
		s, err := network.ParseStatic(addr, gw, dns)
		if err != nil {
			b.update(func(n *ui.NetState) { n.Phase, n.Err = ui.NetList, err.Error() })
			return
		}
		b.update(func(n *ui.NetState) { n.Phase, n.Iface, n.Err = ui.NetApplying, iface, "" })
		kind := network.Ethernet
		for _, l := range network.Links(r.Opt.Root) {
			if l.Name == iface {
				kind = l.Kind
			}
		}
		ip := s.Addr.Addr().String()
		if r.Fixture() {
			time.Sleep(1500 * time.Millisecond)
			b.upWith(network.State{Online: true, Iface: iface, Kind: kind, IP: ip}, "", ip, s.Gateway.String())
			return
		}
		actx, cancel := context.WithTimeout(r.ctx, 15*time.Second)
		err = network.ApplyStatic(actx, iface, s)
		cancel()
		if err != nil {
			b.update(func(n *ui.NetState) { n.Phase, n.Err = ui.NetList, i18n.T("couldn't set the address: %s", err.Error()) })
			return
		}
		wctx, cancel := context.WithTimeout(r.ctx, 20*time.Second)
		st := network.Wait(wctx, r.Opt.Root, r.Opt.Target.URL, time.Second)
		cancel()
		if !st.Online {
			b.update(func(n *ui.NetState) {
				n.Phase, n.Err = ui.NetList, i18n.T("the address is set, but %s can't be found: check the gateway and DNS", r.Site())
			})
			return
		}
		if st.IP == "" {
			st.IP = ip
		}
		b.upWith(st, "", ip, s.Gateway.String())
	}()
}

// Quick starts the quick pass: each result goes to the screen as it finishes, then the summary.
// The first graphics check waits for the screen's GPU notice (GPUGo): the person is warned before
// the screen may go dark, and the check really runs that much later.
func (b *Backend) Quick() (int, float64) {
	b.NetStop() // the network step is over either way
	total, expect := b.R.Plan()
	start := time.Now()
	b.R.started = func(c checks.Check, at float64) {
		b.post(ui.RunningMsg{ID: c.ID, Label: c.Label, Effect: c.Effect, At: at})
	}
	b.R.live = func(p boot.Params) { b.post(ui.LiveMsg{Params: p}) }
	b.R.gate = func(ctx context.Context, c checks.Check) error {
		if c.ID != "graphics" { // the GPU check: the only one that draws on the GPU
			return nil
		}
		ids := c.ItemIDs(b.R.env())
		if len(ids) == 0 {
			return nil
		}
		b.post(ui.GPUNoticeMsg{ID: ids[0], Label: c.LabelOf(ids[0])})
		select {
		case <-b.gpuGo:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	go func() {
		b.R.Quick(func(x checks.Result) {
			b.post(ui.CheckMsg{Check: toCheck(x), Remaining: max(0, expect-time.Since(start).Seconds())})
		})
		b.post(b.R.Summary())
	}()
	return total, expect
}

// GPUGo lets the graphics check run.
func (b *Backend) GPUGo() {
	select {
	case b.gpuGo <- struct{}{}:
	default:
	}
}

// HandsOn is this Mac's hands-on tests, on the Mac itself (or, with --sysroot, on the fixture
// tree, which writes nothing and never sleeps).
func (b *Backend) HandsOn() handson.Setup {
	r := b.R
	io := b.system()
	env := &handson.Env{Checks: r.env(), Phone: r.tetherPhone()}
	r.mu.Lock()
	if r.netFacts.Online && r.net.Iface != "" {
		env.Online = r.net.Iface + ", " + viaName(r.net.Kind)
	}
	r.mu.Unlock()
	nio := &netIO{System: io, b: b} // the Wi-Fi and Ethernet tests work the run's own network
	return handson.Setup{Env: env, Plan: handson.Plan(env), IO: func(s handson.Spec) handson.IO {
		if s.ID == "network.wifi" || s.ID == "network.ethernet" {
			return nio
		}
		return io
	}}
}

// Record takes the hands-on results into the report and sends the summary brought up to date.
func (b *Backend) Record(rs []checks.Result) {
	b.R.Record(rs)
	go func() { b.post(b.R.Summary()) }()
}

// Submit sends the report, once the Mac is online (reconnect).
func (b *Backend) Submit(handle, notice string) {
	go func() {
		if why := b.reconnect(); why != "" {
			b.post(ui.SentMsg{Err: why, Offline: true})
			return
		}
		f := b.R.Report(handle, notice, "")
		rc, err := b.R.Submit(f)
		if err != nil {
			b.post(ui.SentMsg{Err: errText(err), Problems: problems(err)})
			return
		}
		b.post(ui.SentMsg{Code: rc.Code, Page: b.R.Site() + "/report/" + rc.Code})
		b.R.noteOp(b.R.noteTicket(), b.R.dropNote) // sent: the copy kept before a test that sleeps isn't offered to pick up
		// a copy saved before sending now carries the code
		if p, ok, err := b.R.Resave(f); ok && err == nil {
			b.post(ui.SavedMsg{Path: p})
		}
	}()
}

// reconnect makes sure the Mac is still online before sending: the link carrying the run may
// have gone (a phone unplugged during the port tests). It waits up to 15 s for it to come back
// (Finish says so), then brings back a cable turned off for it; "" once online, else why not.
func (b *Backend) reconnect() string {
	r := b.R
	if r.Fixture() || network.Online(r.ctx, r.Opt.Root, r.Opt.Target.URL).Online {
		return ""
	}
	n := b.state()
	note := i18n.T("the connection went away: waiting up to 15 s for it to come back…")
	if n.Via == "Phone" {
		note = i18n.T("the %s went away: waiting up to 15 s for it to come back…", ui.PhoneName(n))
		if !n.PhoneApple {
			note += "  ·  " + i18n.T("turn USB tethering back on: Android turns it off when unplugged")
		}
	}
	b.post(ui.SendWaitMsg{Note: note})
	wctx, cancel := context.WithTimeout(r.ctx, 15*time.Second)
	st := network.Wait(wctx, r.Opt.Root, r.Opt.Target.URL, time.Second)
	cancel()
	b.mu.Lock()
	off := append([]parked(nil), b.parked...)
	b.mu.Unlock()
	if !st.Online {
		for _, p := range off {
			if p.kind == network.WiFi {
				continue
			}
			l, ok := b.linkFor(p.kind)
			if !ok || !p.is(l) {
				continue
			}
			b.post(ui.SendWaitMsg{Note: i18n.T("bringing %s back…", ui.Used(n, viaName(p.kind)))})
			uctx, cancel := context.WithTimeout(r.ctx, 10*time.Second)
			network.SetLink(uctx, l.Name, true)
			cancel()
			wctx, cancel := context.WithTimeout(r.ctx, 30*time.Second)
			st = network.Wait(wctx, r.Opt.Root, r.Opt.Target.URL, time.Second)
			cancel()
			if st.Online {
				b.setParked(nil)
			}
			break
		}
	}
	if !st.Online {
		b.down()
		return i18n.T("offline: the connection went away and didn't come back. Save a copy to external storage and send it later.")
	}
	b.up(st, "")
	b.post(ui.SendWaitMsg{})
	return ""
}

// Save writes the report to the stick.
func (b *Backend) Save(handle, notice string) {
	go func() {
		f := b.R.Report(handle, notice, "")
		p, err := b.R.Save(f)
		if err != nil {
			b.post(ui.SavedMsg{Err: errText(err)})
			return
		}
		b.post(ui.SavedMsg{Path: p})
	}()
}

// Resume answers Which Mac's offer of an unfinished run's copy (the note is rewritten off the
// screen's goroutine; failing that, it's only offered again).
func (b *Backend) Resume(use bool) {
	go func() {
		if restored, _ := b.R.Resume(use); restored { // the quick pass's results are back: the screens get its summary
			b.post(b.R.Summary())
		}
	}()
}

// Woke drops the note beside the copy saved before a test that sleeps: it's over, the Mac is back.
func (b *Backend) Woke() {
	t := b.R.noteTicket()
	go b.R.Woke(t)
}

// Keep saves a copy to the stick before a test that sleeps.
func (b *Backend) Keep(handle, notice string, k ui.KeepInfo) {
	t := b.R.noteTicket()
	go func() {
		p, err := b.R.Keep(b.R.Report(handle, notice, ""), k, t)
		if err != nil {
			b.post(ui.KeptMsg{Err: errText(err)})
			return
		}
		b.post(ui.KeptMsg{Path: p})
	}()
}

// clean makes a network name drawable in omaboot-v: control characters and anything beyond
// the Latin letters (emoji, CJK) become '?'. Only the screen sees it; iwd gets the real network.
func clean(s string) string {
	return strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f || r > 0x24f {
			return '?'
		}
		return r
	}, s)
}

// found is one of the network step's found lines.
type found struct {
	text  string
	link  int          // ui.LinkNone … ui.LinkLimit
	name  string       // the phone line: the device's USB name, or ""
	apple bool         // the phone line: an iPhone
	need  network.Need // the phone line: what the device needs before it shares a connection
}

// rank orders a line's candidates: the one in use first, then one that's ready, off, getting
// there, a known limitation, nothing.
var rank = map[int]int{ui.LinkConnected: 6, ui.LinkReady: 5, ui.LinkOff: 4, ui.LinkCarrier: 3, ui.LinkLimit: 2, ui.LinkNone: 1}

// lineSet is the network step's Ethernet and phone lines as the links are now, and whether
// there's a Wi-Fi interface.
type lineSet struct {
	eth, phone found
	wifi       bool
}

// describeLinks is the network step's Ethernet and phone lines as the links are now (n is the
// step's state: which interface it is online through). A link turned off for another is off
// (networkctl down); one that's up with an address but doesn't carry the run is ready.
func describeLinks(r *Run, n ui.NetState) lineSet {
	root := r.Opt.Root
	ls := lineSet{eth: found{text: i18n.T("no adapter or cable")}, phone: found{text: i18n.T("no phone sharing its connection")}}
	// Apple Thunderbolt 1/2 Macs see a Thunderbolt adapter only if it was plugged in before the
	// Mac started (research/findings/thunderbolt-hotplug-macs.md)
	for _, d := range r.HW.PCIByClass("0880") {
		if d.Driver == "thunderbolt" {
			ls.eth.text = i18n.T("no adapter or cable (a Thunderbolt adapter works only if plugged in before the Mac starts)")
			break
		}
	}
	connected := func(name string) string {
		s := i18n.T("%s: connected", name)
		if n.IP != "" {
			s += "  ·  " + n.IP
		}
		if n.Static {
			s += "  " + i18n.T("(static)")
		}
		return s
	}
	keep := func(cur *found, f found) {
		if rank[f.link] > rank[cur.link] {
			*cur = f
		}
	}
	switching := func(k network.Kind) bool { return n.Phase == ui.NetSwitching && viaKind(n.To) == k }
	links := network.Links(root)
	ethSeen := false
	for _, l := range links {
		on := n.Online && n.Iface == l.Name && !l.Down
		addr := ""
		if !on && !l.Down {
			addr = network.LinkAddr(root, l.Name)
		}
		switch l.Kind {
		case network.WiFi:
			ls.wifi = true
		case network.Modem:
			name := devName(l.Product, i18n.T("modem"))
			keep(&ls.phone, found{text: i18n.T("%s  ·  an LTE modem: it needs dialing, which OmaBoot? can't do yet", name), link: ui.LinkLimit, name: name})
		case network.Tether:
			f := found{name: devName(l.Product, ""), apple: l.Apple()}
			name := ui.PhoneName(ui.NetState{Phone: f.name})
			switch {
			case l.Down:
				f.text, f.link = ui.OffText(n, name), ui.LinkOff
			case on:
				f.text, f.link = connected(name), ui.LinkConnected
			case f.apple && !l.Carrier: // no hotspot yet: before Trust, or Personal Hotspot is off
				f.text, f.link, f.need = i18n.T("%s found  ·  on the phone: tap Trust, type its passcode, Personal Hotspot on", name), ui.LinkCarrier, network.NeedTrust
			case addr != "" && !switching(l.Kind):
				f.text, f.link = i18n.T("%s ready", name)+"  ·  "+addr, ui.LinkReady
			default:
				f.text, f.link = i18n.T("%s: asking for an address…", name), ui.LinkCarrier
			}
			keep(&ls.phone, f)
		case network.Ethernet:
			name := network.Shown(l.Name, l.Kind)
			f := found{text: i18n.T("%s: no cable", name), link: ui.LinkNone}
			switch {
			case l.Down:
				f.text, f.link = ui.OffText(n, name), ui.LinkOff
			case on:
				f.text, f.link = connected(name), ui.LinkConnected
			case addr != "" && l.Carrier && !switching(l.Kind):
				f.text, f.link = i18n.T("%s ready", name)+"  ·  "+addr, ui.LinkReady
			case l.Carrier && (!n.Online || switching(l.Kind)):
				f.text, f.link = i18n.T("%s: cable in, asking for an address…", name), ui.LinkCarrier
			case l.Carrier:
				f.text, f.link = i18n.T("%s: cable in, no address", name), ui.LinkCarrier
			}
			if !ethSeen || rank[f.link] > rank[ls.eth.link] {
				ls.eth = f // the first adapter says "no cable" over "no adapter or cable"
			}
			ethSeen = true
		}
	}
	// phones and LTE sticks that aren't network devices yet: what they need
	for _, p := range network.PendingDevices(root, links) {
		name := devName(p.Product, "")
		shown := ui.PhoneName(ui.NetState{Phone: name})
		f := found{link: ui.LinkCarrier, name: name, apple: p.Need == network.NeedTrust, need: p.Need}
		switch p.Need {
		case network.NeedTrust:
			f.text = i18n.T("%s found  ·  on the phone: tap Trust, type its passcode, Personal Hotspot on", shown)
		case network.NeedTethering:
			f.text = i18n.T("%s found  ·  charging only: unlock it, turn on USB tethering", shown)
		case network.NeedModeswitch:
			f.text = i18n.T("%s found  ·  switching it to network mode…", shown)
		}
		keep(&ls.phone, f)
	}
	return ls
}

// devName is a USB device's product string as the screens show it (cleaned, at most 24 cells),
// or def.
func devName(product, def string) string {
	p := strings.TrimSpace(clean(product))
	if p == "" {
		return def
	}
	if r := []rune(p); len(r) > 24 {
		p = string(r[:24])
	}
	return p
}

func wifiChip(r *Run) string {
	for _, d := range r.HW.PCIByClass("0280") {
		if n := checks.WifiChips[d.ID()]; n != "" {
			return n
		}
		return d.ID()
	}
	return ""
}

func noWifiText(r *Run) string {
	for _, d := range r.HW.PCIByClass("0280") {
		if d.ID() == "14e4:4331" || d.ID() == "14e4:43a0" {
			return i18n.T("no live driver (comes with the installed system)")
		}
		return i18n.T("no driver loaded")
	}
	return ""
}

// problems are the server's itemised problems, or the report's own check's.
func problems(err error) []string {
	var rej *send.Rejected
	if errors.As(err, &rej) {
		return rej.Problems
	}
	var errs report.Errors
	if errors.As(err, &errs) {
		return errs
	}
	return nil
}
