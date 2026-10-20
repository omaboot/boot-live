package network

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/omaboot/boot-live/internal/i18n"
)

// Static is a hand-set IPv4 address for a network without DHCP: the Network step's "Set static
// IP address". It goes into a runtime systemd-networkd file (/run is lost at shutdown), so it
// lasts for this run only.
type Static struct {
	Addr    netip.Prefix // e.g. 10.0.4.23/24
	Gateway netip.Addr
	DNS     []netip.Addr
}

// ErrStatic is a problem with what was typed; its text says which field and why.
type ErrStatic struct {
	Field int // 0 address/prefix, 1 gateway, 2 DNS
	Msg   string
}

func (e *ErrStatic) Error() string { return e.Msg }

func badField(field int, msg string) error { return &ErrStatic{Field: field, Msg: msg} }

// ParseStatic checks the three fields: an IPv4 address with its prefix length (CIDR), a gateway
// in that subnet, and one or more IPv4 name servers separated by spaces or commas.
func ParseStatic(addr, gw, dns string) (Static, error) {
	var s Static
	addr = strings.TrimSpace(addr)
	if addr == "" {
		return s, badField(0, i18n.T("Address/prefix is empty: for example 192.168.1.50/24"))
	}
	if !strings.Contains(addr, "/") {
		return s, badField(0, i18n.T("Address/prefix needs the prefix length after a /, for example %s/24", addr))
	}
	p, err := netip.ParsePrefix(addr)
	if err != nil || !p.Addr().Is4() {
		return s, badField(0, i18n.T("%s isn't an IPv4 address with a prefix, like 192.168.1.50/24", addr))
	}
	if p.Bits() < 1 || p.Bits() > 32 {
		return s, badField(0, i18n.T("the prefix length must be 1 to 32"))
	}
	a := p.Addr()
	if a.IsUnspecified() || a.IsLoopback() || a.IsMulticast() || a.IsLinkLocalUnicast() || a == netip.AddrFrom4([4]byte{255, 255, 255, 255}) {
		return s, badField(0, i18n.T("%s can't be this Mac's address", a))
	}
	net := p.Masked()
	if p.Bits() <= 30 && (a == net.Addr() || a == broadcast(net)) {
		return s, badField(0, i18n.T("%s is the network's own address or its broadcast, not one for this Mac", a))
	}
	s.Addr = p

	gw = strings.TrimSpace(gw)
	g, err := netip.ParseAddr(gw)
	switch {
	case gw == "":
		return s, badField(1, i18n.T("Gateway is empty: the router's address, for example %s", firstHost(net)))
	case err != nil || !g.Is4():
		return s, badField(1, i18n.T("%s isn't an IPv4 address", gw))
	case !net.Contains(g):
		return s, badField(1, i18n.T("the gateway %s isn't in %s", g, net))
	case g == a:
		return s, badField(1, i18n.T("the gateway can't be this Mac's own address"))
	case p.Bits() <= 30 && (g == net.Addr() || g == broadcast(net)):
		return s, badField(1, i18n.T("%s is the network's own address or its broadcast, not a gateway", g))
	}
	s.Gateway = g

	for _, f := range strings.FieldsFunc(dns, func(r rune) bool { return r == ' ' || r == ',' }) {
		d, err := netip.ParseAddr(f)
		if err != nil || !d.Is4() || d.IsUnspecified() || d.IsMulticast() {
			return s, badField(2, i18n.T("%s isn't an IPv4 address", f))
		}
		s.DNS = append(s.DNS, d)
	}
	if len(s.DNS) == 0 {
		return s, badField(2, i18n.T("DNS is empty: at least one name server, often the gateway (%s)", g))
	}
	return s, nil
}

func broadcast(p netip.Prefix) netip.Addr {
	b := p.Addr().As4()
	host := uint32(1)<<(32-p.Bits()) - 1
	v := uint32(b[0])<<24 | uint32(b[1])<<16 | uint32(b[2])<<8 | uint32(b[3]) | host
	return netip.AddrFrom4([4]byte{byte(v >> 24), byte(v >> 16), byte(v >> 8), byte(v)})
}

func firstHost(p netip.Prefix) netip.Addr { return p.Addr().Next() }

// ifaceName is what a kernel interface name may contain: it goes into a file name and a unit.
var ifaceName = regexp.MustCompile(`^[A-Za-z0-9_.:-]{1,15}$`)

// StaticPath is the runtime networkd file for iface. Its 10- prefix sorts before the live
// system's own wired and wireless DHCP files, so it wins for this interface.
func StaticPath(iface string) string {
	return "/run/systemd/network/10-omaboot-static-" + iface + ".network"
}

// Unit is the networkd file's text for iface.
func (s Static) Unit(iface string) string {
	var b strings.Builder
	b.WriteString("# OmaBoot? Live: a static address set on the Network step, for this run only.\n")
	fmt.Fprintf(&b, "[Match]\nName=%s\n\n[Network]\nAddress=%s\nGateway=%s\n", iface, s.Addr, s.Gateway)
	for _, d := range s.DNS {
		fmt.Fprintf(&b, "DNS=%s\n", d)
	}
	b.WriteString("DHCP=no\n")
	return b.String()
}

// SetLink takes a wired link down (networkctl down: to use Wi-Fi instead, the run's traffic must
// go over Wi-Fi) or brings it back up, where networkd configures it again.
func SetLink(ctx context.Context, iface string, up bool) error {
	if !ifaceName.MatchString(iface) {
		return errors.New("bad interface name " + iface)
	}
	verb := "down"
	if up {
		verb = "up"
	}
	if out, err := exec.CommandContext(ctx, "networkctl", verb, iface).CombinedOutput(); err != nil {
		msg := fmt.Sprintf("networkctl %s: %v %s", verb, err, strings.TrimSpace(string(out)))
		return errors.New(strings.ReplaceAll(msg, iface, Shown(iface, "")))
	}
	return nil
}

// ApplyStatic writes the runtime networkd file for iface and has networkd use it: networkctl
// reload, then reconfigure the interface. Waiting for the connection is the caller's.
func ApplyStatic(ctx context.Context, iface string, s Static) error {
	if !ifaceName.MatchString(iface) {
		return errors.New("bad interface name " + iface)
	}
	p := StaticPath(iface)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	tmp := p + ".part"
	if err := os.WriteFile(tmp, []byte(s.Unit(iface)), 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, p); err != nil {
		return err
	}
	for _, args := range [][]string{{"reload"}, {"reconfigure", iface}} {
		if out, err := exec.CommandContext(ctx, "networkctl", args...).CombinedOutput(); err != nil {
			msg := fmt.Sprintf("networkctl %s: %v %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
			return errors.New(strings.ReplaceAll(msg, iface, Shown(iface, "")))
		}
	}
	return nil
}
