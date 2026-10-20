package handson

import (
	"encoding/binary"
	"fmt"
	"path"
	"strings"
)

// The kernel's Bluetooth management interface (bluez doc/mgmt-api.txt): packets on an
// AF_BLUETOOTH/HCI socket bound to the control channel. Each is a little-endian header, code
// (command or event), controller index and parameter length, then the parameters. The socket
// itself is in bluetooth_linux.go; what it says is read here.
const (
	mgmtIndexNone      = 0xffff
	mgmtReadInfo       = 0x0004
	mgmtSetPowered     = 0x0005
	mgmtSetLE          = 0x000d
	mgmtStartDiscovery = 0x0023
	mgmtStopDiscovery  = 0x0024
	mgmtConfirmName    = 0x0025

	mgmtEvComplete     = 0x0001
	mgmtEvStatus       = 0x0002
	mgmtEvIndexRemoved = 0x0005
	mgmtEvNewSettings  = 0x0006
	mgmtEvDeviceFound  = 0x0012
	mgmtEvDiscovering  = 0x0013

	mgmtDiscoverAll   = 0x07 // BR/EDR, LE public, LE random: interleaved
	mgmtDiscoverBREDR = 0x01
	mgmtDiscoverLE    = 0x06

	mgmtSettingPowered = 1 << 0
	mgmtSettingLE      = 1 << 9

	mgmtStatusBusy        = 0x0a
	mgmtStatusRejected    = 0x0b
	mgmtStatusUnsupported = 0x0c
	mgmtStatusInvalid     = 0x0d
)

// btModes are the discovery types as the report says them.
var btModes = map[byte]string{mgmtDiscoverAll: "classic and LE", mgmtDiscoverBREDR: "classic only", mgmtDiscoverLE: "LE only"}

// mgmtStatusText is a command status as the report says it.
func mgmtStatusText(s byte) string {
	if t, ok := map[byte]string{0x01: "unknown command", 0x03: "failed", 0x07: "no resources", 0x08: "timeout", 0x0a: "busy",
		0x0b: "rejected", 0x0c: "not supported", 0x0d: "invalid parameters", 0x0f: "not powered", 0x11: "invalid index",
		0x12: "rfkilled", 0x14: "permission denied"}[s]; ok {
		return t
	}
	return fmt.Sprintf("status 0x%02x", s)
}

// mgmtPacket builds a command.
func mgmtPacket(op, index uint16, params []byte) []byte {
	b := make([]byte, 6, 6+len(params))
	binary.LittleEndian.PutUint16(b[0:], op)
	binary.LittleEndian.PutUint16(b[2:], index)
	binary.LittleEndian.PutUint16(b[4:], uint16(len(params)))
	return append(b, params...)
}

// mgmtSplit reads a packet's header: event code, controller index, parameters.
func mgmtSplit(p []byte) (ev, index uint16, data []byte, ok bool) {
	if len(p) < 6 {
		return 0, 0, nil, false
	}
	n := int(binary.LittleEndian.Uint16(p[4:]))
	if len(p) < 6+n {
		return 0, 0, nil, false
	}
	return binary.LittleEndian.Uint16(p), binary.LittleEndian.Uint16(p[2:]), p[6 : 6+n], true
}

// mgmtInfo reads Read Controller Information's reply: the controller for the report, the
// settings it supports and those it has now (powered, Low Energy…).
func mgmtInfo(rp []byte) (info string, supported, current uint32, ok bool) {
	if len(rp) < 17 {
		return "", 0, 0, false
	}
	return btInfo(binary.LittleEndian.Uint16(rp[7:]), rp[6]), binary.LittleEndian.Uint32(rp[9:]), binary.LittleEndian.Uint32(rp[13:]), true
}

// btInfo is a controller's maker and Bluetooth version: "Broadcom, Bluetooth 4.0".
func btInfo(maker uint16, version byte) string {
	m, ok := map[uint16]string{2: "Intel", 10: "Qualcomm (CSR)", 15: "Broadcom", 29: "Qualcomm", 93: "Realtek", 305: "Cypress"}[maker]
	if !ok {
		m = fmt.Sprintf("maker %d", maker)
	}
	vs := []string{"1.0b", "1.1", "1.2", "2.0", "2.1", "3.0", "4.0", "4.1", "4.2", "5.0", "5.1", "5.2", "5.3", "5.4", "6.0"}
	if int(version) < len(vs) {
		return m + ", Bluetooth " + vs[version]
	}
	return m
}

// mgmtFound reads a Device Found event: the device, and whether the kernel asks to have its
// name looked up (Confirm Name).
func mgmtFound(data []byte) (d BTDevice, confirm bool, ok bool) {
	if len(data) < 14 {
		return d, false, false
	}
	n := int(binary.LittleEndian.Uint16(data[12:]))
	eir := data[14:min(len(data), 14+n)]
	d.Key = fmt.Sprintf("%x/%d", data[0:7], data[6])
	d.LE = data[6] != 0
	d.RSSI = int(int8(data[7]))
	d.Name, d.Kind, d.Maker = parseEIR(eir)
	return d, binary.LittleEndian.Uint32(data[8:])&1 != 0, true
}

// parseEIR reads a device's name, kind and maker out of its extended inquiry or advertising
// data: length-prefixed fields, type then value. A phone advertising over Low Energy usually
// gives no name and no kind, only its maker's data (an iPhone: Apple's).
func parseEIR(b []byte) (name, kind, maker string) {
	for len(b) >= 2 {
		l := int(b[0])
		if l == 0 || l+1 > len(b) {
			break
		}
		t, v := b[1], b[2:l+1]
		switch t {
		case 0x09: // complete local name
			name = string(v)
		case 0x08: // shortened local name
			if name == "" {
				name = string(v)
			}
		case 0x0d: // class of device
			if len(v) >= 3 {
				kind = codKind(uint32(v[0]) | uint32(v[1])<<8 | uint32(v[2])<<16)
			}
		case 0x19: // appearance (LE)
			if len(v) >= 2 && kind == "" {
				kind = appearanceKind(binary.LittleEndian.Uint16(v))
			}
		case 0xff: // manufacturer data: the company's identifier first
			if len(v) >= 2 {
				maker = btMakers[binary.LittleEndian.Uint16(v)]
			}
		}
		b = b[l+1:]
	}
	return strings.TrimRight(name, "\x00"), kind, maker
}

// btMakers are the companies (Bluetooth SIG identifiers) whose phones, watches and earbuds
// are the likely ones nearby.
var btMakers = map[uint16]string{0x0006: "Microsoft", 0x004c: "Apple", 0x0075: "Samsung", 0x00e0: "Google"}

// codKind is a classic device's kind from its class of device's major class.
func codKind(cod uint32) string {
	return map[uint32]string{1: "computer", 2: "phone", 3: "network", 4: "audio", 5: "input", 6: "imaging", 7: "wearable"}[cod>>8&0x1f]
}

// appearanceKind is an LE device's kind from its appearance's category.
func appearanceKind(a uint16) string {
	return map[uint16]string{1: "phone", 2: "computer", 3: "watch", 0x0a: "audio", 0x0f: "input", 0x21: "audio", 0x22: "audio"}[a>>6]
}

// btClean keeps a device's name to what the console can draw: letters, digits and the usual
// marks (a curly apostrophe becomes a straight one); anything else (emoji) is dropped.
func btClean(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r == '’' || r == '‘':
			b.WriteRune('\'')
		case r >= 0x20 && r < 0x7f, r >= 0xa0 && r < 0x250:
			b.WriteRune(r)
		}
	}
	return strings.TrimSpace(b.String())
}

// btBus is where the adapter hangs off, for the report: "USB 2-1.1.1.1 [05ac:821f]", "PCI
// 0000:02:00.1", "UART serial0", or "".
func btBus(io IO, hci string) string {
	t := path.Base(link(io, "/sys/class/bluetooth/"+hci+"/device"))
	switch {
	case t == "." || t == "/":
		return ""
	case strings.Contains(t, ":") && isUSBDevicePath(strings.Split(t, ":")[0]):
		dev := strings.Split(t, ":")[0]
		return fmt.Sprintf("USB %s [%s:%s]", dev, io.Read(usbDevices+dev+"/idVendor"), io.Read(usbDevices+dev+"/idProduct"))
	case strings.Count(t, ":") == 2:
		return "PCI " + t
	}
	return "UART " + t
}

// staticScan is a scan that never got going: it only says why.
type staticScan struct{ st BTState }

func (s *staticScan) State() BTState { return s.st }
func (s *staticScan) Again()         {}
func (s *staticScan) Close()         {}
