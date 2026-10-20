//go:build linux

package network

import "syscall"

// bindTo binds a socket to one interface (SO_BINDTODEVICE): its traffic goes over that
// interface and no other, whatever the routes prefer.
func bindTo(iface string) func(network, address string, c syscall.RawConn) error {
	return func(_, _ string, c syscall.RawConn) error {
		var serr error
		if err := c.Control(func(fd uintptr) {
			serr = syscall.SetsockoptString(int(fd), syscall.SOL_SOCKET, syscall.SO_BINDTODEVICE, iface)
		}); err != nil {
			return err
		}
		return serr
	}
}
