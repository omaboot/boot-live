package network

import (
	"strconv"

	"github.com/omaboot/boot-live/internal/bootwatch"
	"github.com/omaboot/boot-live/internal/sysroot"
)

// Bytes is an interface's received plus sent bytes since it appeared, from its sysfs counters
// (statistics/rx_bytes and tx_bytes); false when they can't be read.
func Bytes(r sysroot.Root, iface string) (uint64, bool) {
	d := "/sys/class/net/" + iface + "/statistics/"
	rx, err1 := strconv.ParseUint(r.Read(d+"rx_bytes"), 10, 64)
	tx, err2 := strconv.ParseUint(r.Read(d+"tx_bytes"), 10, 64)
	if err1 != nil || err2 != nil {
		return 0, false
	}
	return rx + tx, true
}

// Watch is the boot.watch test (bootwatch.Client) against server, over iface alone (each
// socket bound to it, SO_BINDTODEVICE), or over the run's own routes when iface is "". The
// Wi-Fi and Ethernet checks run it on the interface they test. DNS goes the usual way; only the
// transfer is bound.
func Watch(server, iface string) *bootwatch.Client {
	c := &bootwatch.Client{URL: server}
	if iface != "" {
		c.Control = bindTo(iface)
	}
	return c
}
