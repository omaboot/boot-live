package network

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/omaboot/boot-live/internal/sysroot"
)

// An interface name that embeds a MAC address never reaches a report.
func TestShown(t *testing.T) {
	cases := []struct {
		name string
		kind Kind
		want string
	}{
		{"wlan0", WiFi, "wlan0"},
		{"enp4s0f0", Ethernet, "enp4s0f0"},
		{"enp0s20u1", Ethernet, "enp0s20u1"},
		{"enx001122aabbcc", Ethernet, "ethernet"},
		{"wlx001122AABBCC", WiFi, "wifi"},
		{"enx001122aabbcc", Tether, "network interface"},
		{"eth-001122aabbcc", "", "network interface"},
		{"enx001122aabbc", Ethernet, "enx001122aabbc"}, // 11 hex digits: not a MAC
	}
	for _, c := range cases {
		if got := Shown(c.name, c.kind); got != c.want {
			t.Errorf("Shown(%q, %q) = %q, want %q", c.name, c.kind, got, c.want)
		}
	}
}

func TestUSBDevice(t *testing.T) {
	for in, want := range map[string]string{
		"../../../1-2:1.0":          "1-2",
		"../../../2-1.3:1.1":        "2-1.3",
		"../../../0000:02:00.0":     "",
		"../../../../virtual/net/x": "",
		"":                          "",
	} {
		if got := usbDevice(in); got != want {
			t.Errorf("usbDevice(%q) = %q, want %q", in, got, want)
		}
	}
}

// Phones, LTE sticks and Ethernet adapters share USB network drivers: the product string and
// the interface's type tell them apart.
func TestKindOf(t *testing.T) {
	cases := []struct {
		name, driver, typ, devtype, product string
		wireless                            bool
		want                                Kind
	}{
		{"wlan0", "brcmsmac", "1", "wlan", "", true, WiFi},
		{"enp0s20u1", "ax88179_178a", "1", "", "AX88179", false, Ethernet},
		{"enx001122aabbcc", "ipheth", "1", "", "iPhone", false, Tether},
		{"enx001122aabbcc", "rndis_host", "1", "", "Pixel 7", false, Tether},
		{"enx001122aabbcc", "cdc_ether", "1", "", "HUAWEI_MOBILE", false, Tether},
		{"enx001122aabbcc", "cdc_ncm", "1", "", "USB 10/100/1000 LAN", false, Ethernet},
		{"enx001122aabbcc", "cdc_ether", "1", "", "Thunderbolt Dock", false, Ethernet},
		{"wwan0", "qmi_wwan", "65534", "wwan", "EM7455", false, Modem},
		{"wwp0s20u4i12", "cdc_mbim", "1", "", "", false, Modem},
		{"usb0", "qmi_wwan", "1", "", "", false, Modem},
	}
	for _, c := range cases {
		if got := kindOf(c.name, c.driver, c.typ, c.devtype, c.product, c.wireless); got != c.want {
			t.Errorf("kindOf(%q, %q, %q) = %q, want %q", c.name, c.driver, c.product, got, c.want)
		}
	}
}

// A phone with no network interface yet is told by its USB interfaces and vendor.
func TestNeedOf(t *testing.T) {
	mtp := usbIf{"06", "01", "01", ""}
	adb := usbIf{"ff", "42", "01", "ADB Interface"}
	storage := usbIf{"08", "06", "50", ""}
	cases := []struct {
		vendor, product string
		ifs             []usbIf
		want            Need
	}{
		{"05ac", "iPhone", []usbIf{mtp, {"ff", "fe", "02", ""}}, NeedTrust},
		{"05ac", "Magic Keyboard", []usbIf{{"03", "01", "01", ""}}, NeedNothing},
		{"18d1", "Pixel 7", []usbIf{{"ff", "ff", "00", "MTP"}}, NeedTethering},
		{"18d1", "Pixel 7", nil, NeedTethering},
		{"1234", "Some phone", []usbIf{mtp}, NeedTethering},
		{"1234", "Some phone", []usbIf{adb}, NeedTethering},
		{"12d1", "HUAWEI_MOBILE", []usbIf{storage}, NeedModeswitch},
		{"04e8", "Portable SSD T7", []usbIf{storage}, NeedNothing},
		{"0781", "Cruzer", []usbIf{storage}, NeedNothing},
	}
	for _, c := range cases {
		if got := needOf(c.vendor, c.product, c.ifs); got != c.want {
			t.Errorf("needOf(%q, %q) = %d, want %d", c.vendor, c.product, got, c.want)
		}
	}
}

// With a cable and a phone both up, the route with the lowest metric carries the traffic,
// whichever is listed first.
func TestDefaultRouteMetric(t *testing.T) {
	dir := t.TempDir()
	must := func(err error) {
		if err != nil {
			t.Fatal(err)
		}
	}
	must(os.MkdirAll(filepath.Join(dir, "proc/net"), 0o755))
	route := "Iface\tDestination\tGateway \tFlags\tRefCnt\tUse\tMetric\tMask\t\tMTU\tWindow\tIRTT\n" +
		"enx001122aabbcc\t00000000\t010A14AC\t0003\t0\t0\t150\t00000000\t0\t0\t0\n" +
		"enp0s20u1\t00000000\t0101000A\t0003\t0\t0\t100\t00000000\t0\t0\t0\n" +
		"wlan0\t00000000\t0101A8C0\t0003\t0\t0\t600\t00000000\t0\t0\t0\n"
	must(os.WriteFile(filepath.Join(dir, "proc/net/route"), []byte(route), 0o644))
	rt, ok := DefaultRoute(sysroot.Root(dir))
	if !ok || rt.Iface != "enp0s20u1" || rt.Gateway != "10.0.1.1" {
		t.Errorf("DefaultRoute = %+v, %v; want enp0s20u1 via 10.0.1.1", rt, ok)
	}
	z := "00000000000000000000000000000000"
	v6 := z + " 00 " + z + " 00 fe800000000000000000000000000001 00000258 00000000 00000000 00000003 wlan0\n" +
		z + " 00 " + z + " 00 fe800000000000000000000000000001 00000096 00000000 00000000 00000003 enx001122aabbcc\n"
	must(os.WriteFile(filepath.Join(dir, "proc/net/route"), nil, 0o644))
	must(os.WriteFile(filepath.Join(dir, "proc/net/ipv6_route"), []byte(v6), 0o644))
	if rt, ok := DefaultRoute(sysroot.Root(dir)); !ok || rt.Iface != "enx001122aabbcc" {
		t.Errorf("IPv6 DefaultRoute = %+v, %v; want enx001122aabbcc", rt, ok)
	}
}
