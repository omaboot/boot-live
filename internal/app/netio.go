package app

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/omaboot/boot-live/internal/bootwatch"
	"github.com/omaboot/boot-live/internal/checks"
	"github.com/omaboot/boot-live/internal/handson"
	"github.com/omaboot/boot-live/internal/network"
	"github.com/omaboot/boot-live/internal/ui"
)

// netIO is the IO the Wi-Fi and Ethernet hands-on tests get (handson.Netter): the Mac's, plus
// the run's own network plumbing, so a network the Wi-Fi test joins is the run's (it stays
// online through it, the status line follows), and the boot.watch test and its verdict are the
// checks' (checks.WifiJoined, checks.EthernetOver) with the transfer's progress for the band.
type netIO struct {
	*handson.System
	b *Backend
}

var _ handson.Netter = (*netIO)(nil)

func (n *netIO) WifiCard() (checks.WifiCard, bool) { return checks.FindWifi(n.b.R.env()) }

// WifiJoined is the network the Network screen joined on the card's interface, while it still
// has an address there.
func (n *netIO) WifiJoined() string {
	r := n.b.R
	c, ok := n.WifiCard()
	r.mu.Lock()
	ssid, iface := r.netFacts.Joined, r.netFacts.JoinedIface
	r.mu.Unlock()
	if !ok || ssid == "" || iface != c.Iface || !r.Fixture() && network.Addr(iface) == "" {
		return ""
	}
	return clean(ssid)
}

func (n *netIO) WifiScan() handson.Future[handson.WifiScan] {
	return handson.Go(func() handson.WifiScan {
		aps, err := n.b.scan(n.b.R.ctx)
		s := handson.WifiScan{Err: err}
		for _, a := range aps {
			s.APs = append(s.APs, handson.AP{SSID: clean(a.SSID), Signal: a.Signal, Bars: a.Bars(), Security: a.Security})
		}
		return s
	})
}

func (n *netIO) WifiJoin(ssid, password string) handson.Future[handson.WifiJoin] {
	return handson.Go(func() handson.WifiJoin { return n.b.testJoin(ssid, password) })
}

// testJoin joins a network from the last scan for the Wi-Fi test and waits for an address on
// the Wi-Fi interface. When Wi-Fi carries the run (or nothing does) the run is online through
// it afterwards, and the status line shows the join as it goes; a cable or phone carrying the
// run keeps carrying it (its route wins), with Wi-Fi joined beside it. Nothing is turned off.
func (b *Backend) testJoin(ssid, password string) handson.WifiJoin {
	r := b.R
	r.mu.Lock()
	var ap *network.AP
	for i := range r.aps {
		if clean(r.aps[i].SSID) == ssid {
			ap = &r.aps[i]
		}
	}
	r.mu.Unlock()
	if ap == nil {
		b.netLog("Wi-Fi test: the network asked for isn't in the last scan (name given: %v)", ssid != "")
		return handson.WifiJoin{Err: errors.New("the network is gone: scan again")} // English, and no name: it may reach the evidence
	}
	w, err := r.wifiDev()
	if err != nil {
		b.netLog("Wi-Fi test: no Wi-Fi device: %v", err)
		return handson.WifiJoin{Err: err}
	}
	b.netLog("Wi-Fi test: joining a %s network (%d/4 bars), password given: %v", ap.Security, ap.Bars(), password != "")
	cur := b.state()
	carries := !cur.Online || cur.Via == "Wi-Fi" // the join changes the run's connection
	back := func() {
		if carries {
			b.update(func(n *ui.NetState) { n.Phase, n.To = ui.NetList, "" })
		}
	}
	if carries {
		b.update(func(n *ui.NetState) { n.Phase, n.SSID, n.Err, n.To = ui.NetJoining, ssid, "", "Wi-Fi" })
	}
	cctx, cancel := context.WithTimeout(r.ctx, 40*time.Second)
	err = w.Connect(cctx, *ap, password)
	cancel()
	password = ""
	j := handson.WifiJoin{Iface: w.Device()}
	if err != nil {
		b.netLog("Wi-Fi test: the join failed: %v", err)
		back()
		j.Err, j.Refused = err, errors.Is(err, network.ErrPassword)
		if j.Refused {
			j.Err = errors.New("the network refused the password")
		}
		return j
	}
	if carries {
		b.update(func(n *ui.NetState) { n.Phase = ui.NetDHCP })
	}
	if r.Fixture() {
		j.IP = "192.0.2.10"
	} else {
		for end := time.Now().Add(30 * time.Second); time.Now().Before(end) && r.ctx.Err() == nil; time.Sleep(500 * time.Millisecond) {
			if j.IP = network.Addr(j.Iface); j.IP != "" {
				break
			}
		}
	}
	if j.IP == "" {
		b.netLog("Wi-Fi test: joined, but no address from DHCP in 30 s")
		back()
		j.Err, j.NoAddr = errors.New("joined, but got no address (DHCP)"), true
		return j
	}
	b.netLog("Wi-Fi test: joined, an address on %s", j.Iface)
	r.mu.Lock()
	r.netFacts.Joined, r.netFacts.JoinedIface = ssid, j.Iface
	r.mu.Unlock()
	if !carries {
		return j
	}
	wctx, cancel := context.WithTimeout(r.ctx, 15*time.Second)
	st := network.Wait(wctx, r.Opt.Root, r.Opt.Target.URL, time.Second)
	cancel()
	if !st.Online {
		back()
		return j
	}
	if st.Kind == "" {
		st.Kind = network.WiFi
	}
	b.up(st, ssid)
	j.Online = st.Iface + ", " + viaName(st.Kind)
	return j
}

func (n *netIO) EthPort() (checks.EthPort, bool) { return checks.FindEthernet(n.b.R.env()) }

func (n *netIO) Links() []handson.NetLink {
	root := n.b.R.Opt.Root
	var out []handson.NetLink
	for _, l := range network.Links(root) {
		if l.Kind != network.Ethernet && l.Kind != network.WiFi {
			continue
		}
		out = append(out, handson.NetLink{Name: l.Name, Ethernet: l.Kind == network.Ethernet, USB: l.USB != "", Product: devName(l.Product, ""),
			Carrier: l.Carrier, Down: l.Down, Addr: network.LinkAddr(root, l.Name)})
	}
	return out
}

// LinkUp turns a link back on (networkctl up) and forgets it as turned off for another
// connection.
func (n *netIO) LinkUp(iface string) handson.Future[error] {
	b := n.b
	return handson.Go(func() error {
		if b.R.Fixture() {
			return nil
		}
		ctx, cancel := context.WithTimeout(b.R.ctx, 10*time.Second)
		err := network.SetLink(ctx, iface, true)
		cancel()
		if err != nil {
			return err
		}
		b.mu.Lock()
		var keep []parked
		for _, p := range b.parked {
			if p.iface != iface {
				keep = append(keep, p)
			}
		}
		b.mu.Unlock()
		b.setParked(keep)
		return nil
	})
}

// Transfer runs the boot.watch test over iface and decides id with the checks' own verdict.
func (n *netIO) Transfer(id, iface string, networks int) handson.Transfer {
	b := n.b
	x := &transfer{ch: make(chan handson.Moved, 1), server: handson.WatchHost(b.R.Opt.Target.Watch)}
	go func() {
		r := b.R
		e := r.env()
		if e.Net.Watch != nil {
			watch := r.Opt.Target.Watch
			e.Net.Watch = func(ctx context.Context, iface string) bootwatch.Outcome {
				c := network.Watch(watch, iface)
				c.Progress = x.set
				o := c.Test(ctx)
				out := o.String()
				if o.Err != "" {
					out += "; " + o.Err
				}
				b.netLog("%s test: boot.watch over %s (%s): %s", testName(id), iface, network.Where(r.Opt.Root, iface), out)
				x.mu.Lock()
				x.o = o
				x.mu.Unlock()
				return o
			}
		}
		var res checks.Result
		if id == "network.wifi" {
			c, _ := checks.FindWifi(e)
			res = checks.WifiJoined(r.ctx, e, c, iface, networks)
		} else {
			p, _ := checks.FindEthernet(e)
			res = checks.EthernetOver(r.ctx, e, p)
		}
		x.mu.Lock()
		o := x.o
		x.mu.Unlock()
		x.ch <- handson.Moved{Result: res, Outcome: o, Online: b.follow()}
	}()
	return x
}

// transfer is a boot.watch test under way for a hands-on test.
type transfer struct {
	mu     sync.Mutex
	p      bootwatch.Progress
	o      bootwatch.Outcome
	ch     chan handson.Moved
	m      *handson.Moved // once it ended (read on the screen's goroutine only)
	server string         // the bootwatchd's host[:port] (handson.WatchHost)
}

func (x *transfer) Server() string { return x.server }

func (x *transfer) set(p bootwatch.Progress) {
	x.mu.Lock()
	x.p = p
	x.mu.Unlock()
}

func (x *transfer) Progress() bootwatch.Progress {
	x.mu.Lock()
	defer x.mu.Unlock()
	return x.p
}

func (x *transfer) Ready() (handson.Moved, bool) {
	if x.m == nil {
		select {
		case m := <-x.ch:
			x.m = &m
		default:
			return handson.Moved{}, false
		}
	}
	return *x.m, true
}

// follow brings the network state up to date with the default route after a hands-on test
// changed the links (a cable plugged in carries the run now: its route wins), and says what the
// run is online through ("en0, Ethernet"), or "". A connection that went away is watchLink's.
func (b *Backend) follow() string {
	r := b.R
	n := b.state()
	if r.Fixture() { // a fixture's routes never change
		if n.Online {
			return n.Iface + ", " + n.Via
		}
		return ""
	}
	st := network.Online(r.ctx, r.Opt.Root, r.Opt.Target.URL)
	if !st.Online {
		return ""
	}
	if !n.Online || n.Iface != st.Iface {
		ssid := ""
		if st.Kind == network.WiFi {
			r.mu.Lock()
			ssid = r.netFacts.Joined
			r.mu.Unlock()
		}
		b.up(st, ssid)
	}
	return st.Iface + ", " + viaName(st.Kind)
}

// testName is how the network log names a hands-on network test.
func testName(id string) string {
	if id == "network.wifi" {
		return "Wi-Fi"
	}
	return "Ethernet"
}
