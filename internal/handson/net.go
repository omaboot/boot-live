package handson

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/omaboot/boot-live/internal/bootwatch"
	"github.com/omaboot/boot-live/internal/checks"
	g "github.com/omaboot/boot-live/internal/grid"
	"github.com/omaboot/boot-live/internal/i18n"
)

// The Wi-Fi and Ethernet tests (net_wifi.go, net_ethernet.go) work the Mac's network through
// the IO: on the Mac, the run's own plumbing (internal/app: iwd, networkd, the boot.watch test
// bound to one interface), so a network joined here keeps the run online and the status line
// follows; on the Fake, a script. Both tests are offered whatever the Network screen did, an
// offline run too: the test brings its interface up itself.

// Netter is an IO that can work the Mac's network. An IO without it (a test's own) gets the
// network tests decided not_tested.
type Netter interface {
	// WifiCard is the Mac's own Wi-Fi card and its interface (checks.FindWifi); false: none.
	WifiCard() (checks.WifiCard, bool)
	// WifiJoined is the network the card's interface joined earlier in this run (the Network
	// screen) and still has an address on, or "".
	WifiJoined() string
	// WifiScan scans for networks with the Wi-Fi interface.
	WifiScan() Future[WifiScan]
	// WifiJoin joins a network from the last scan (by its name as shown) for the rest of the
	// run, and waits for an address on it. The password lives only for the call.
	WifiJoin(ssid, password string) Future[WifiJoin]
	// EthPort is the Mac's own Ethernet port and its interface (checks.FindEthernet); false: none.
	EthPort() (checks.EthPort, bool)
	// Links are the wired and wireless network interfaces as they are now.
	Links() []NetLink
	// LinkUp brings back a link the Network screen turned off for another connection.
	LinkUp(iface string) Future[error]
	// Transfer runs the boot.watch test over iface alone (bound to it) and decides criterion id
	// from it as the checks do (checks.WifiJoined, checks.EthernetOver): every byte checked both
	// ways, and iface's counters agreeing. networks is how many the Wi-Fi scan heard.
	Transfer(id, iface string, networks int) Transfer
}

// AP is a Wi-Fi network in range, as the Wi-Fi test lists it.
type AP struct {
	SSID     string // as the screen shows it (control characters and the like cleaned)
	Signal   int    // dBm
	Bars     int    // 1–4
	Security string // open, psk, 8021x, wep
}

// Open reports whether it needs no password.
func (a AP) Open() bool { return a.Security == "open" }

// WifiScan is what a scan heard.
type WifiScan struct {
	APs []AP
	Err error
}

// WifiJoin is how joining a network went.
type WifiJoin struct {
	Iface, IP string
	Err       error // couldn't join, or joined and got no address
	Refused   bool  // the network refused the password
	NoAddr    bool  // joined, but no address came (DHCP)
	// Online is the connection the run is online through now, "wlan0, Wi-Fi", or "".
	Online string
}

// NetLink is one network interface as it is now.
type NetLink struct {
	Name     string
	Ethernet bool   // wired Ethernet (the Mac's port, or an adapter)
	USB      bool   // on a USB or Thunderbolt adapter, not the Mac's own controller
	Product  string // an adapter's name, if it gives one
	Carrier  bool
	Down     bool   // turned off (networkctl down)
	Addr     string // its IPv4 address, or ""
}

// Moved is a transfer's end: the criterion's result, and the boot.watch outcome it rests on.
type Moved struct {
	Result  checks.Result
	Outcome bootwatch.Outcome
	// Online is the connection the run is online through after it, "en0, Ethernet", or "".
	Online string
}

// Transfer is a boot.watch test under way.
type Transfer interface {
	Progress() bootwatch.Progress
	Ready() (Moved, bool)
	// Server is the bootwatchd it goes through, as the Output names it (WatchHost):
	// "boot.watch", "localhost:8470"; "" when there's none.
	Server() string
}

// WatchHost is a bootwatchd's URL as the screens name it: its host, with the port when the URL
// gives one ("https://boot.watch" is "boot.watch", "http://localhost:8470" is
// "localhost:8470"); "" for none.
func WatchHost(watch string) string {
	if u, err := url.Parse(watch); err == nil && u.Host != "" {
		return u.Host
	}
	return strings.TrimRight(watch, "/")
}

// serverName is a transfer's server for the screen: boot.watch when it doesn't say.
func serverName(x Transfer) string {
	if x != nil {
		if s := x.Server(); s != "" {
			return s
		}
	}
	return "boot.watch"
}

// via is a connection as the Output names it: "eth0, Ethernet" is "eth0 (Ethernet)".
func via(online string) string {
	if i, k, ok := strings.Cut(online, ", "); ok {
		return i + " (" + k + ")"
	}
	return online
}

// noNet is the result for a network test on an IO that can't work the network.
func noNet(id string) checks.Result {
	return NotTested(id, checks.Other, "no network access in this mode", i18n.T("no network access here"))
}

// size is a byte count for the screen: "64 KB", "1.3 MB".
func size(n int64) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%d KB", n>>10)
	}
	return fmt.Sprintf("%d B", n)
}

// progressText is what a transfer through server is doing, for the screen.
func progressText(p bootwatch.Progress, server string) string {
	switch p.Phase {
	case bootwatch.Receiving:
		return i18n.T("receiving %s of %s", size(p.Done), size(p.Total))
	case bootwatch.Sending:
		return i18n.T("sending %s of %s", size(p.Done), size(p.Total))
	}
	if p.Total > 0 {
		return i18n.T("reaching %s: %d of %d echoes", server, p.Done, p.Total)
	}
	return i18n.T("reaching %s", server)
}

// movingLine is the Output's line while a transfer goes through server over iface (the Output names
// the server the test uses).
func movingLine(server, iface string) Line {
	return Wait(i18n.T("testing through %s over %s", server, iface))
}

// movedLines are the Output's lines about a finished transfer through server.
func movedLines(m Moved, server string) []Line {
	o := m.Outcome
	out := []Line{Fact(i18n.T("tested through %s", server))}
	if o.Down > 0 || o.Up > 0 {
		s := i18n.T("%s down, %s up", size(o.Down), size(o.Up))
		if o.Speed > 0 {
			s += fmt.Sprintf(" (%s/s)", size(int64(o.Speed)))
		}
		out = append(out, Fact(s))
	}
	switch {
	case m.Result.Status == checks.Supported:
		out = append(out, Fact(i18n.T("every byte right, both ways")))
	case o.Problem != bootwatch.OK && o.Err != "":
		out = append(out, Bad(cut(o.Err, 120)))
	}
	return out
}

// transferArt is the band's picture of a transfer (net_wifi.go, net_ethernet.go): the Mac on
// the left, the far end on the right (the router while there's no transfer, boot.watch during
// it), the line between them, the data moving along it, and a line saying how far it got.
type transferArt struct {
	far     string // the right box's name
	line    int    // 0 no link (a dashed grey line), 1 a link, 2 data moving
	prog    bootwatch.Progress
	words   string // under the boxes, white
	under   string // under that, dim
	ok, bad bool   // it ended: the line in green, or red
}

func (a transferArt) draw(gr *g.Grid, x, y, w, h int, t float64) {
	if w < 30 || h < 5 {
		return
	}
	top := y + max(0, (h-7)/2)
	far := a.far
	fw := width(far) + 4
	mac, rt := x+2, x+w-fw-2
	frame := func(bx, bw int, name string) {
		gr.Put(bx, top, "┌"+strings.Repeat("─", bw-2)+"┐", g.GRY)
		gr.Put(bx, top+1, "│", g.GRY)
		gr.Put(bx+2, top+1, name, g.WHT)
		gr.Put(bx+bw-1, top+1, "│", g.GRY)
		gr.Put(bx, top+2, "└"+strings.Repeat("─", bw-2)+"┘", g.GRY)
	}
	frame(mac, 7, "Mac")
	frame(rt, fw, far)
	l0, l1 := mac+8, rt-2 // the lane
	lc := g.DIM
	switch {
	case a.ok:
		lc = g.BGRN
	case a.bad:
		lc = g.BRED
	case a.line > 0:
		lc = g.BVIO
	}
	for i := l0; i <= l1; i++ {
		r := "─"
		if a.line == 0 && (i-l0)%2 == 1 {
			r = " "
		}
		gr.Put(i, top+1, r, lc)
	}
	if a.line == 2 && !a.ok && !a.bad {
		lane := l1 - l0
		for k := 0; k < 5 && lane > 4; k++ {
			p := int(t*18+float64(k)*float64(lane)/5) % lane
			switch a.prog.Phase {
			case bootwatch.Receiving: // toward the Mac
				gr.Put(l1-p, top+1, "■", g.BCYN)
			case bootwatch.Sending:
				gr.Put(l0+p, top+1, "■", g.BCYN)
			default: // echoes, both ways
				if k%2 == 0 {
					gr.Put(l1-p, top+1, "·", g.BCYN)
				} else {
					gr.Put(l0+p, top+1, "·", g.BCYN)
				}
			}
		}
	}
	gr.Put(mac, top+4, cut(a.words, w-4), g.WHT)
	if a.under != "" && top+5 < y+h {
		gr.Put(mac, top+5, cut(a.under, w-4), g.DIM)
	}
}

// textKey is a key typed into a text field (the Wi-Fi password): the runes it types, or "" for
// a key that types nothing (a named key, a chord).
func textKey(key string) string {
	if strings.HasPrefix(key, "[") && strings.HasSuffix(key, "]") && len(key) > 2 { // a paste
		return key[1 : len(key)-1]
	}
	if utf8.RuneCountInString(key) == 1 {
		return key
	}
	if strings.Contains(key, "+") {
		return ""
	}
	switch key {
	case "up", "down", "left", "right", "home", "end", "pgup", "pgdown", "delete", "insert", "enter", "esc",
		"backspace", "tab", "space":
		return ""
	}
	if key[0] == 'f' {
		if _, err := strconv.Atoi(key[1:]); err == nil {
			return "" // f1–f20
		}
	}
	return key // runes typed together
}

// ── the Fake's network ──────────────────────────────────────────────────────

// The Fake keeps its network in files under fakeNet, so a Demo scripts it with Set and SetAt:
//
//	wifi              "chip|pci|driver|iface": the Mac's own card (iface "": none)
//	aps               one network a line, "ssid|dBm|security"
//	scan-s, scanerr   how long a scan takes (default 2.4 s); why it fails
//	joined            the network the card joined on the Network screen
//	join              how joining goes: "" (any password of 8 characters or more, after 1.4 s),
//	                  "refuse", "fail", "noaddr"
//	eth               "pci|driver|iface": the Mac's own Ethernet port
//	link/<iface>      "ethernet" or "usb-ethernet|product": a wired link (its carrier is
//	                  /sys/class/net/<iface>/carrier)
//	addr/<iface>      its address; down/<iface> "1": turned off
//	watch, watch-s    how the boot.watch test goes ("ok", "broken", "dead", "signin", "none": no
//	                  test server) and how long it takes (default 1.8 s)
//	server            the bootwatchd's URL (default https://boot.watch)
const fakeNet = "/fake/net/"

func (f *Fake) WifiCard() (checks.WifiCard, bool) {
	v := strings.Split(f.Read(fakeNet+"wifi"), "|")
	if len(v) != 4 {
		return checks.WifiCard{}, false
	}
	c := checks.WifiCard{Chip: v[0], PCI: v[1], Driver: v[2], Iface: v[3], Shown: v[3]}
	if c.Iface == "" {
		r := Result("network.wifi", checks.Failed, Automatic, fmt.Sprintf("%s (%s, %s): no wireless interface", c.Chip, c.PCI, c.Driver), i18n.T("%s: no wireless interface", c.Chip))
		c.Decided = &r
	}
	return c, true
}

func (f *Fake) WifiJoined() string {
	c, _ := f.WifiCard()
	if f.Read(fakeNet+"addr/"+c.Iface) == "" {
		return ""
	}
	return f.Read(fakeNet + "joined")
}

// fakeAPs are the networks the Fake hears.
func (f *Fake) fakeAPs() []AP {
	var out []AP
	for _, l := range strings.Split(f.Files[fakeNet+"aps"], "\n") {
		p := strings.Split(strings.TrimSpace(l), "|")
		if len(p) != 3 {
			continue
		}
		dbm, _ := strconv.Atoi(p[1])
		out = append(out, AP{SSID: p[0], Signal: dbm, Bars: bars(dbm), Security: p[2]})
	}
	return out
}

// bars is a signal as 1–4 bars (network.AP.Bars).
func bars(dbm int) int {
	switch {
	case dbm >= -60:
		return 4
	case dbm >= -70:
		return 3
	case dbm >= -80:
		return 2
	}
	return 1
}

func (f *Fake) secs(name string, def float64) float64 {
	if v, err := strconv.ParseFloat(f.Read(fakeNet+name), 64); err == nil {
		return v
	}
	return def
}

func (f *Fake) WifiScan() Future[WifiScan] {
	s := WifiScan{APs: f.fakeAPs()}
	if e := f.Read(fakeNet + "scanerr"); e != "" {
		s = WifiScan{Err: errors.New(e)}
	}
	return timed[WifiScan]{f, f.now + f.secs("scan-s", 2.4), s}
}

func (f *Fake) WifiJoin(ssid, password string) Future[WifiJoin] {
	c, _ := f.WifiCard()
	var ap *AP
	for _, a := range f.fakeAPs() {
		if a.SSID == ssid {
			ap = &a
		}
	}
	j := WifiJoin{Iface: c.Iface}
	switch how := f.Read(fakeNet + "join"); {
	case ap == nil:
		j.Err = fmt.Errorf("%s is gone", ssid)
	case how == "refuse", how == "" && !ap.Open() && len(password) < 8:
		j.Err, j.Refused = errors.New("the network refused the password"), true
	case how == "fail":
		j.Err = errors.New("the connection attempt failed")
	case how == "noaddr":
		j.Err, j.NoAddr = errors.New("no address came (DHCP)"), true
	default:
		j.IP, j.Online = "192.168.1.47", c.Iface+", Wi-Fi"
	}
	at := f.now + 1.4
	if j.Err == nil {
		f.At(at, func(f *Fake) {
			f.Set(fakeNet+"addr/"+c.Iface, j.IP)
			f.Set(fakeNet+"joined", ssid)
		})
	}
	return timed[WifiJoin]{f, at, j}
}

func (f *Fake) EthPort() (checks.EthPort, bool) {
	v := strings.Split(f.Read(fakeNet+"eth"), "|")
	if len(v) != 3 {
		return checks.EthPort{}, false
	}
	return checks.EthPort{PCI: v[0], Driver: v[1], Iface: v[2], Shown: v[2]}, true
}

func (f *Fake) Links() []NetLink {
	var out []NetLink
	for _, p := range f.Glob(fakeNet + "link/*") {
		name := p[len(fakeNet+"link/"):]
		v := strings.SplitN(f.Read(p), "|", 2)
		l := NetLink{Name: name, Ethernet: true, USB: v[0] == "usb-ethernet", Carrier: f.Read("/sys/class/net/"+name+"/carrier") == "1",
			Down: f.Read(fakeNet+"down/"+name) == "1", Addr: f.Read(fakeNet + "addr/" + name)}
		if len(v) == 2 {
			l.Product = v[1]
		}
		out = append(out, l)
	}
	return out
}

func (f *Fake) LinkUp(iface string) Future[error] {
	at := f.now + 0.6
	f.At(at, func(f *Fake) { f.Set(fakeNet+"down/"+iface, "") })
	return timed[error]{f, at, nil}
}

func (f *Fake) Transfer(id, iface string, networks int) Transfer {
	return &fakeTransfer{f: f, id: id, iface: iface, networks: networks, at: f.now, dur: f.secs("watch-s", 1.8)}
}

type fakeTransfer struct {
	f         *Fake
	id, iface string
	networks  int
	at, dur   float64
	moved     *Moved
}

func (x *fakeTransfer) Server() string {
	if x.f.Read(fakeNet+"watch") == "none" {
		return ""
	}
	if u := x.f.Read(fakeNet + "server"); u != "" {
		return WatchHost(u)
	}
	return WatchHost("https://boot.watch")
}

func (x *fakeTransfer) Progress() bootwatch.Progress {
	p := (x.f.now - x.at) / x.dur
	const down, up = 1344 << 10, bootwatch.UpSize
	switch {
	case p < 0.15:
		return bootwatch.Progress{Phase: bootwatch.Reaching, Done: int64(p / 0.15 * 6), Total: 6}
	case p < 0.85:
		return bootwatch.Progress{Phase: bootwatch.Receiving, Done: int64((p - 0.15) / 0.7 * down), Total: down}
	}
	return bootwatch.Progress{Phase: bootwatch.Sending, Done: min(up, int64((p-0.85)/0.15*up)), Total: up}
}

// Ready decides through the checks' own verdict (checks.WifiJoined, checks.EthernetOver), with
// the scripted outcome standing in for boot.watch.
func (x *fakeTransfer) Ready() (Moved, bool) {
	if x.moved != nil {
		return *x.moved, true
	}
	if x.f.now < x.at+x.dur {
		return Moved{}, false
	}
	const kib = 1 << 10
	var o bootwatch.Outcome
	switch x.f.Read(fakeNet + "watch") {
	case "", "ok":
		o = bootwatch.Outcome{Reached: true, Family: "IPv4", RTT: []time.Duration{18 * time.Millisecond, 19 * time.Millisecond, 24 * time.Millisecond},
			Down: 1344 * kib, DownOK: 1344 * kib, Speed: 740 * kib, Up: 64 * kib, UpOK: 64 * kib}
	case "broken":
		o = bootwatch.Outcome{Reached: true, Family: "IPv4", Down: 64 * kib, DownOK: 900, Problem: bootwatch.Broken, Err: "byte 900 of the 64 KB part was wrong"}
	case "dead":
		o = bootwatch.Outcome{Problem: bootwatch.Broken, Err: "dial tcp: connect: network is unreachable"}
	case "signin":
		o = bootwatch.Outcome{Problem: bootwatch.SignIn, Err: "someone else answered for boot.watch: a sign-in page or a proxy"}
	}
	counter := uint64(10 * kib)
	e := &checks.Env{Net: checks.NetFacts{Online: true, Iface: x.iface,
		Addr: func(i string) string { return x.f.Read(fakeNet + "addr/" + i) },
		Bytes: func(string) (uint64, bool) {
			v := counter
			counter += uint64(o.Moved()) + 4*kib
			return v, true
		}}}
	if x.f.Read(fakeNet+"watch") != "none" {
		e.Net.Watch = func(context.Context, string) bootwatch.Outcome { return o }
	}
	var r checks.Result
	if x.id == "network.wifi" {
		c, _ := x.f.WifiCard()
		r = checks.WifiJoined(context.Background(), e, c, x.iface, x.networks)
	} else {
		p, _ := x.f.EthPort()
		r = checks.EthernetOver(context.Background(), e, p)
	}
	m := Moved{Result: r, Outcome: o, Online: x.iface + ", " + map[bool]string{true: "Wi-Fi", false: "Ethernet"}[x.id == "network.wifi"]}
	x.moved = &m
	return m, true
}
