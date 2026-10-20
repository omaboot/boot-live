// Package network gets the Mac online for sending the report (LIVE-PLAN §4 step 4). Ethernet and
// USB tethering come up on their own (the live system runs systemd-networkd with DHCP on every
// wired and tethered interface, with route metrics Ethernet 100, phones 150, Wi-Fi 600: the
// lowest carries the run); Wi-Fi goes through iwd over D-Bus: scan, pick, password, connect,
// then wait for networkd's DHCP. "Skip, test offline" is always possible.
//
// On a fixture tree (--sysroot) nothing touches the system: links and routes are read from the
// tree, and Wi-Fi is simulated from its iwd-networks.txt.
package network

import (
	"context"
	"encoding/hex"
	"net"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/omaboot/boot-live/internal/sysroot"
)

// Kind of link.
type Kind string

const (
	Ethernet Kind = "ethernet"
	Tether   Kind = "tether" // a phone (or a router-style LTE stick) sharing its connection over USB
	Modem    Kind = "modem"  // an LTE modem that needs dialing (QMI, MBIM): OmaBoot? can't use it yet
	WiFi     Kind = "wifi"
)

// Link is one network interface on real hardware.
type Link struct {
	Name    string
	Kind    Kind
	Driver  string
	Carrier bool
	USB     string // the USB device it sits on ("1-2", "2-1.3"; sysfs's name under /sys/bus/usb/devices), or ""
	Product string // that USB device's product string ("iPhone", "Pixel 7"), or ""
	Vendor  string // that USB device's vendor ID ("05ac"), or ""
	Down    bool   // taken down (networkctl down): its IFF_UP flag is clear
}

// Apple reports whether the link is an iPhone's (or iPad's) Personal Hotspot.
func (l Link) Apple() bool { return l.Driver == "ipheth" || iDevice(l.Vendor, l.Product) }

func iDevice(vendor, product string) bool {
	return vendor == "05ac" && (strings.Contains(product, "iPhone") || strings.Contains(product, "iPad"))
}

// Shown is an interface's name as reports and screens show it: the name itself, unless it embeds
// a MAC address (12 hex digits: systemd's enx…/wlx… names, or any other), which would put the
// Mac's hardware address in the report. Such a name shows as its kind instead.
func Shown(name string, k Kind) string {
	run := 0
	for _, c := range name {
		if c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F' {
			if run++; run >= 12 {
				switch k {
				case Ethernet, WiFi:
					return string(k)
				}
				return "network interface"
			}
		} else {
			run = 0
		}
	}
	return name
}

// tetherDrivers are USB network drivers phones (and router-style LTE sticks) use for tethering.
var tetherDrivers = map[string]bool{"rndis_host": true, "cdc_ether": true, "cdc_ncm": true, "ipheth": true}

// modemDrivers are LTE modems' drivers: they need dialing (APN, SIM PIN) before they carry anything.
var modemDrivers = map[string]bool{"qmi_wwan": true, "cdc_mbim": true}

// adapterWords in a USB product string mark an Ethernet adapter, dock or hub on cdc_ether or
// cdc_ncm (the drivers phones use too).
var adapterWords = []string{"ethernet", "lan", "gigabit", "adapter", "dock", "hub"}

// kindOf classifies a network interface: its name, driver, type (ARPHRD: 1 Ethernet, 65534
// none, as raw-IP modems are), uevent DEVTYPE, whether it's wireless, and its USB product string.
func kindOf(name, driver, typ, devtype, product string, wireless bool) Kind {
	switch {
	case wireless || devtype == "wlan":
		return WiFi
	case devtype == "wwan" || strings.HasPrefix(name, "ww") || modemDrivers[driver] || typ == "65534":
		return Modem
	case tetherDrivers[driver]:
		if driver == "cdc_ether" || driver == "cdc_ncm" {
			p := strings.ToLower(product)
			for _, w := range adapterWords {
				if strings.Contains(p, w) {
					return Ethernet
				}
			}
		}
		return Tether
	}
	return Ethernet
}

// Links lists the network interfaces that sit on a device (not lo, not virtual ones).
func Links(r sysroot.Root) []Link {
	var out []Link
	for _, d := range r.Glob("/sys/class/net/*") {
		ue := r.Uevent(d + "/device/uevent")
		if len(ue) == 0 {
			continue
		}
		name, typ := path.Base(d), r.Read(d+"/type")
		l := Link{Name: name, Driver: ue["DRIVER"], Carrier: r.Read(d+"/carrier") == "1"}
		l.USB = usbDevice(r.Link(d + "/device"))
		if l.USB != "" {
			l.Product = r.Read("/sys/bus/usb/devices/" + l.USB + "/product")
			l.Vendor = r.Read("/sys/bus/usb/devices/" + l.USB + "/idVendor")
		}
		if f, err := strconv.ParseUint(strings.TrimPrefix(r.Read(d+"/flags"), "0x"), 16, 32); err == nil {
			l.Down = f&1 == 0
		}
		wireless := r.Exists(d+"/wireless") || r.Exists(d+"/phy80211")
		l.Kind = kindOf(name, l.Driver, typ, r.Uevent(d + "/uevent")["DEVTYPE"], l.Product, wireless)
		if typ != "1" && l.Kind != Modem {
			continue
		}
		out = append(out, l)
	}
	return out
}

// Need is what a USB phone or LTE stick needs before it can share a connection.
type Need int

const (
	NeedNothing    Need = iota
	NeedTrust           // an iPhone: Trust on the phone, its passcode, Personal Hotspot on
	NeedTethering       // an Android phone that only charges: unlock it, turn on USB tethering
	NeedModeswitch      // an LTE stick still posing as a CD of drivers: usb_modeswitch is switching it
)

// Pending is a USB phone or LTE stick that is plugged in but isn't a network interface yet.
type Pending struct {
	USB, Vendor, Product string
	Need                 Need
}

// phoneVendors are USB vendor IDs of phone makers: one of their devices that is neither storage,
// input nor a hub is taken for a phone that only charges.
var phoneVendors = map[string]bool{
	"18d1": true, "04e8": true, "22b8": true, "2a70": true, "2717": true, "0fce": true, "1004": true,
	"0bb4": true, "2e04": true, "22d9": true, "12d1": true, "19d2": true, "2ae5": true, "1bbb": true,
}

// modemVendors make LTE sticks that start as a CD of Windows drivers (Huawei, ZTE).
var modemVendors = map[string]bool{"12d1": true, "19d2": true}

// usbIf is one USB interface: class, subclass, protocol (two hex digits each) and its name.
type usbIf struct{ class, sub, proto, name string }

// needOf tells what a USB device with no network interface needs to share a connection, from
// its vendor ID, product string and interfaces; NeedNothing when it isn't a phone or LTE stick.
func needOf(vendor, product string, ifs []usbIf) Need {
	if iDevice(vendor, product) {
		return NeedTrust
	}
	storage, other := len(ifs) > 0, false
	for _, f := range ifs {
		if f.class != "08" {
			storage = false
		}
		switch {
		case f.class == "06", f.class == "ff" && f.sub == "42" && f.proto == "01", f.name == "MTP":
			return NeedTethering // MTP or PTP (files and photos), or ADB
		case f.class == "03", f.class == "08", f.class == "09":
			other = true
		}
	}
	switch {
	case storage && modemVendors[vendor]:
		return NeedModeswitch
	case phoneVendors[vendor] && !other:
		return NeedTethering
	}
	return NeedNothing
}

// PendingDevices lists the USB phones and LTE sticks that have no network interface among links
// yet: an iPhone before Trust, an Android phone that only charges, a stick still in CD mode.
func PendingDevices(r sysroot.Root, links []Link) []Pending {
	has := map[string]bool{}
	for _, l := range links {
		has[l.USB] = true
	}
	var out []Pending
	for _, d := range r.Glob("/sys/bus/usb/devices/*") {
		dev := path.Base(d)
		if strings.Contains(dev, ":") || strings.HasPrefix(dev, "usb") || has[dev] {
			continue
		}
		var ifs []usbIf // its interfaces ("1-2:1.0") sit beside it
		for _, i := range r.Glob("/sys/bus/usb/devices/" + dev + ":*") {
			ifs = append(ifs, usbIf{r.Read(i + "/bInterfaceClass"), r.Read(i + "/bInterfaceSubClass"), r.Read(i + "/bInterfaceProtocol"), r.Read(i + "/interface")})
		}
		p := Pending{USB: dev, Vendor: r.Read(d + "/idVendor"), Product: r.Read(d + "/product")}
		if p.Need = needOf(p.Vendor, p.Product, ifs); p.Need != NeedNothing {
			out = append(out, p)
		}
	}
	return out
}

// usbDevice is the USB device a net device's "device" link points at, or "": the link ends in
// the USB interface ("../../../1-2:1.0"), and the device is the part before the colon.
func usbDevice(target string) string {
	dev, _, ok := strings.Cut(path.Base(target), ":")
	if !ok || !strings.Contains(dev, "-") {
		return ""
	}
	return dev
}

// Route is the default route, if there is one.
type Route struct {
	Iface   string
	Gateway string
}

// DefaultRoute reads /proc/net/route for the IPv4 default route with the lowest metric (and
// /proc/net/ipv6_route for an IPv6 one): with a cable and a phone both up, that is the one
// carrying the traffic.
func DefaultRoute(r sysroot.Root) (Route, bool) {
	best, found := Route{}, false
	low := uint64(1<<64 - 1)
	for i, l := range r.Lines("/proc/net/route") {
		f := strings.Fields(l)
		if i == 0 || len(f) < 8 || f[1] != "00000000" || f[7] != "00000000" {
			continue
		}
		m, err := strconv.ParseUint(f[6], 10, 64)
		if err != nil || found && m >= low {
			continue
		}
		gw := ""
		if b, err := hex.DecodeString(f[2]); err == nil && len(b) == 4 {
			gw = net.IPv4(b[3], b[2], b[1], b[0]).String()
		}
		best, found, low = Route{Iface: f[0], Gateway: gw}, true, m
	}
	if found {
		return best, true
	}
	for _, l := range r.Lines("/proc/net/ipv6_route") {
		f := strings.Fields(l)
		if len(f) < 10 || f[0] != strings.Repeat("0", 32) || f[1] != "00" || f[9] == "lo" {
			continue
		}
		m, err := strconv.ParseUint(f[5], 16, 64)
		if err != nil || found && m >= low {
			continue
		}
		best, found, low = Route{Iface: f[9]}, true, m
	}
	return best, found
}

// LinkAddr is a link's first IPv4 address on the running system, or "" (and always "" on a
// fixture tree, whose interfaces aren't this machine's).
func LinkAddr(r sysroot.Root, iface string) string {
	if !r.IsSystem() {
		return ""
	}
	return Addr(iface)
}

// Addr is an interface's first IPv4 address, or "".
func Addr(iface string) string {
	i, err := net.InterfaceByName(iface)
	if err != nil {
		return ""
	}
	as, _ := i.Addrs()
	for _, a := range as {
		if ip, _, err := net.ParseCIDR(a.String()); err == nil && ip.To4() != nil {
			return ip.String()
		}
	}
	return ""
}

// Where is an interface's place on its network, for the bugreport's network log:
// "192.168.1.47/24, gateway 192.168.1.1", "no IPv4 address" (IPv6 only, or none).
func Where(r sysroot.Root, iface string) string {
	if !r.IsSystem() {
		return ""
	}
	i, err := net.InterfaceByName(iface)
	if err != nil {
		return "gone"
	}
	as, _ := i.Addrs()
	where := ""
	for _, a := range as {
		if ip, _, err := net.ParseCIDR(a.String()); err == nil && ip.To4() != nil {
			where = a.String()
			break
		}
	}
	if where == "" {
		return "no IPv4 address"
	}
	for k, l := range r.Lines("/proc/net/route") {
		f := strings.Fields(l)
		if k == 0 || len(f) < 8 || f[0] != iface || f[1] != "00000000" || f[7] != "00000000" {
			continue
		}
		if b, err := hex.DecodeString(f[2]); err == nil && len(b) == 4 {
			return where + ", gateway " + net.IPv4(b[3], b[2], b[1], b[0]).String()
		}
	}
	return where + ", no default route of its own"
}

// State is whether and how the Mac is online.
type State struct {
	Online bool
	Iface  string
	Kind   Kind
	IP     string
	Why    string // when not online: why ("no default route", "doesitomarchy.com didn't resolve")
}

// Online checks for a routed default and working DNS: the target's host name resolves (or, with
// no target, any name server answers for it). On a fixture tree, the tree's route alone decides.
func Online(ctx context.Context, r sysroot.Root, target string) State {
	rt, ok := DefaultRoute(r)
	if !ok && !r.IsSystem() && fakeJoined.Load() {
		return State{Online: true, Iface: "wlan0", Kind: WiFi, IP: "192.0.2.10"}
	}
	if !ok {
		return State{Why: "no default route"}
	}
	st := State{Iface: rt.Iface, Kind: Ethernet}
	for _, l := range Links(r) {
		if l.Name == rt.Iface {
			st.Kind = l.Kind
		}
	}
	if !r.IsSystem() {
		st.Online, st.IP = true, "192.0.2.10"
		return st
	}
	st.IP = Addr(rt.Iface)
	host := "doesitomarchy.com"
	if u, err := url.Parse(target); err == nil && u.Hostname() != "" {
		host = u.Hostname()
	}
	if net.ParseIP(host) != nil {
		st.Online = true
		return st
	}
	lctx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	if _, err := net.DefaultResolver.LookupHost(lctx, host); err == nil {
		st.Online = true
	} else {
		st.Why = host + " didn't resolve"
		if lctx.Err() != nil {
			st.Why += " in time"
		}
	}
	return st
}

// Wait polls Online until the Mac is online or the context ends.
func Wait(ctx context.Context, r sysroot.Root, target string, every time.Duration) State {
	for {
		if st := Online(ctx, r, target); st.Online {
			return st
		}
		select {
		case <-ctx.Done():
			return State{}
		case <-time.After(every):
		}
	}
}
