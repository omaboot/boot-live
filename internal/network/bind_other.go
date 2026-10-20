//go:build !linux

package network

import (
	"errors"
	"syscall"
)

// bindTo: binding a socket to an interface is Linux's (SO_BINDTODEVICE).
func bindTo(string) func(network, address string, c syscall.RawConn) error {
	return func(string, string, syscall.RawConn) error {
		return errors.New("binding to an interface needs Linux")
	}
}
