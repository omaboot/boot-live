package app

import (
	"time"

	"github.com/omaboot/boot-live/internal/handson"
	"github.com/omaboot/boot-live/internal/network"
	"github.com/omaboot/boot-live/internal/ui"
)

// The status line follows the connection in use live, on every screen (owner, 2026-10-13: a
// Thunderbolt Ethernet adapter pulled out still read "Ethernet up"). watchLink reads the link's
// carrier, operstate and address in sysfs once a second: when the link carrying the run goes
// (unplugged, cable out, address gone) the Mac is marked offline at once; when that same link
// comes back with an address and the default route, it is online again.

// linkEvery is how often the link in use is looked at.
const linkEvery = time.Second

// linkGone reports why the link iface no longer carries the run, or "" while it does: gone from
// sysfs, no carrier, down, or (had is its address when it came up) its IPv4 address gone.
func (b *Backend) linkGone(iface, had string) string {
	root := b.R.Opt.Root
	d := "/sys/class/net/" + iface
	switch op := root.Read(d + "/operstate"); {
	case !root.Exists(d):
		return "gone"
	case root.Read(d+"/carrier") == "0":
		return "no carrier"
	case op == "down" || op == "lowerlayerdown" || op == "notpresent":
		return op
	case had != "" && network.LinkAddr(root, iface) == "":
		return "no address"
	}
	return ""
}

// netNow is the network as the status line has it, for the hands-on tests (handson.IO.NetNow:
// the lid and sleep tests' "the network is back", the Thunderbolt test's wait for it): online
// through the link in use, and that link still there with its carrier, up and its address
// (linkGone, as watchLink checks it), so a link that just went reads offline even before the
// next tick takes the status line down.
func (b *Backend) netNow() handson.Net {
	n := b.state()
	switch {
	case !n.Online || n.Iface == "":
		return handson.Net{Why: "offline"}
	case n.Phase == ui.NetJoining || n.Phase == ui.NetDHCP || n.Phase == ui.NetApplying || n.Phase == ui.NetSwitching:
		return handson.Net{Why: "the connection is changing"}
	}
	if why := b.linkGone(n.Iface, n.IP); why != "" {
		return handson.Net{Why: n.Iface + ": " + why}
	}
	return handson.Net{Up: true, Via: n.Iface + ", " + n.Via}
}

// watchLink keeps the status line's network true to the link (see above). Not on a fixture tree,
// whose links never change.
func (b *Backend) watchLink() {
	r := b.R
	if r.Fixture() {
		return
	}
	var lost struct{ iface, ssid string } // the link that went, while the run is offline
	t := time.NewTicker(linkEvery)
	defer t.Stop()
	for {
		select {
		case <-r.ctx.Done():
			return
		case <-t.C:
		}
		n := b.state()
		if n.Phase == ui.NetJoining || n.Phase == ui.NetDHCP || n.Phase == ui.NetApplying || n.Phase == ui.NetSwitching {
			continue // the network step is changing it on purpose
		}
		switch {
		case n.Online && n.Iface != "":
			if b.linkGone(n.Iface, n.IP) != "" {
				lost.iface, lost.ssid = n.Iface, ""
				if n.Via == "Wi-Fi" {
					lost.ssid = n.SSID
				}
				b.down()
			}
		case !n.Online && lost.iface != "":
			if b.linkGone(lost.iface, "x") != "" {
				continue
			}
			rt, ok := network.DefaultRoute(r.Opt.Root)
			if !ok || rt.Iface != lost.iface {
				continue
			}
			if st := network.Online(r.ctx, r.Opt.Root, r.Opt.Target.URL); st.Online && st.Iface == lost.iface {
				b.up(st, lost.ssid)
				lost.iface = ""
			}
		}
	}
}
