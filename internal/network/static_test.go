package network

import (
	"errors"
	"testing"
)

func TestParseStatic(t *testing.T) {
	ok := []struct{ addr, gw, dns string }{
		{"10.0.4.23/24", "10.0.4.1", "10.0.4.1"},
		{" 192.168.1.50/24 ", "192.168.1.1", "1.1.1.1 9.9.9.9"},
		{"172.16.0.10/16", "172.16.255.254", "172.16.0.1,8.8.8.8"},
		{"10.1.1.2/31", "10.1.1.3", "10.1.1.3"}, // point-to-point: no network or broadcast address
	}
	for _, c := range ok {
		s, err := ParseStatic(c.addr, c.gw, c.dns)
		if err != nil {
			t.Errorf("ParseStatic(%q, %q, %q): %v", c.addr, c.gw, c.dns, err)
			continue
		}
		if len(s.DNS) == 0 {
			t.Errorf("ParseStatic(%q, %q, %q): no DNS", c.addr, c.gw, c.dns)
		}
	}
	bad := []struct {
		addr, gw, dns string
		field         int
	}{
		{"", "10.0.4.1", "10.0.4.1", 0},
		{"10.0.4.23", "10.0.4.1", "10.0.4.1", 0},                // no prefix
		{"10.0.4.300/24", "10.0.4.1", "10.0.4.1", 0},            // not an address
		{"10.0.4.23/33", "10.0.4.1", "10.0.4.1", 0},             // prefix too long
		{"fe80::1/64", "10.0.4.1", "10.0.4.1", 0},               // IPv6
		{"10.0.4.0/24", "10.0.4.1", "10.0.4.1", 0},              // the network's own address
		{"10.0.4.255/24", "10.0.4.1", "10.0.4.1", 0},            // broadcast
		{"127.0.0.2/8", "127.0.0.1", "127.0.0.1", 0},            // loopback
		{"10.0.4.23/24", "", "10.0.4.1", 1},                     // no gateway
		{"10.0.4.23/24", "10.0.5.1", "10.0.4.1", 1},             // gateway outside the subnet
		{"10.0.4.23/24", "10.0.4.23", "10.0.4.1", 1},            // gateway = own address
		{"10.0.4.23/24", "10.0.4.255", "10.0.4.1", 1},           // gateway = broadcast
		{"10.0.4.23/24", "10.0.4.1/24", "10.0.4.1", 1},          // gateway with a prefix
		{"10.0.4.23/24", "10.0.4.1", "", 2},                     // no DNS
		{"10.0.4.23/24", "10.0.4.1", "dns.example", 2},          // a name, not an address
		{"10.0.4.23/24", "10.0.4.1", "10.0.4.1 2001:db8::1", 2}, // IPv6 DNS
	}
	for _, c := range bad {
		_, err := ParseStatic(c.addr, c.gw, c.dns)
		var es *ErrStatic
		if !errors.As(err, &es) {
			t.Errorf("ParseStatic(%q, %q, %q) = %v, want a field error", c.addr, c.gw, c.dns, err)
			continue
		}
		if es.Field != c.field {
			t.Errorf("ParseStatic(%q, %q, %q): field %d, want %d (%s)", c.addr, c.gw, c.dns, es.Field, c.field, es.Msg)
		}
	}
}

func TestStaticUnit(t *testing.T) {
	s, err := ParseStatic("10.0.4.23/24", "10.0.4.1", "10.0.4.1 1.1.1.1")
	if err != nil {
		t.Fatal(err)
	}
	want := "# OmaBoot? Live: a static address set on the Network step, for this run only.\n" +
		"[Match]\nName=enp0s20u1\n\n[Network]\nAddress=10.0.4.23/24\nGateway=10.0.4.1\nDNS=10.0.4.1\nDNS=1.1.1.1\nDHCP=no\n"
	if got := s.Unit("enp0s20u1"); got != want {
		t.Errorf("Unit:\n%s\nwant:\n%s", got, want)
	}
	if p := StaticPath("enp0s20u1"); p != "/run/systemd/network/10-omaboot-static-enp0s20u1.network" {
		t.Errorf("StaticPath = %s", p)
	}
}
