//go:build !linux

package handson

import "errors"

// BluetoothScan: the kernel's Bluetooth management socket is Linux's.
func (s *System) BluetoothScan(string) BTScan {
	return &staticScan{st: BTState{Err: errors.New("Bluetooth scans need Linux")}}
}
