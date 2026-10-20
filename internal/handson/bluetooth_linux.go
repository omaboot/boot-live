//go:build linux

package handson

import (
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/sys/unix"
)

// BluetoothScan scans with an adapter through the kernel's Bluetooth management socket, on its
// own goroutine: switch Low Energy on if it's off (bluetoothd does that when it starts; with no
// bluez on the image nobody has, and the kernel then rejects a classic-and-LE discovery), power
// on if off, discover (classic and LE; classic only, or LE only, when the kernel won't do both)
// again each time a round ends, and on Close stop discovering and put the power and Low Energy
// back. The goroutine ends within a few seconds of Close (it waits at most 3 s for each of the
// kernel's answers). A fixture run never opens the socket.
func (s *System) BluetoothScan(hci string) BTScan {
	if !s.Real() {
		return &staticScan{st: BTState{Err: ErrNotReal}}
	}
	idx, err := strconv.Atoi(strings.TrimPrefix(hci, "hci"))
	if err != nil {
		return &staticScan{st: BTState{Err: fmt.Errorf("odd adapter name %q", hci)}}
	}
	m := &mgmtScan{idx: uint16(idx), again: make(chan struct{}, 1), stop: make(chan struct{}), seen: map[string]int{}}
	go m.run()
	return m
}

type mgmtScan struct {
	idx   uint16
	again chan struct{}
	stop  chan struct{}
	once  sync.Once

	mu   sync.Mutex
	st   BTState
	seen map[string]int // device key → its index in st.Found

	// the scan goroutine's own
	f        *os.File
	buf      []byte
	wasOn    bool
	leOn     bool      // this scan switched Low Energy on
	ours     bool      // the discovery running is the one this scan started
	dtype    byte      // the discovery type the kernel took (0: none yet)
	scanning bool      // the kernel says a discovery is running
	tried    time.Time // the last Start Discovery
}

func (m *mgmtScan) State() BTState {
	m.mu.Lock()
	defer m.mu.Unlock()
	st := m.st
	st.Found = append([]BTDevice(nil), m.st.Found...)
	return st
}

func (m *mgmtScan) Again() {
	select {
	case m.again <- struct{}{}:
	default:
	}
}

func (m *mgmtScan) Close() { m.once.Do(func() { close(m.stop) }) }

func (m *mgmtScan) update(fn func(*BTState)) {
	m.mu.Lock()
	fn(&m.st)
	m.mu.Unlock()
}

func (m *mgmtScan) run() {
	fd, err := unix.Socket(unix.AF_BLUETOOTH, unix.SOCK_RAW|unix.SOCK_CLOEXEC|unix.SOCK_NONBLOCK, unix.BTPROTO_HCI)
	if err == nil {
		if err = unix.Bind(fd, &unix.SockaddrHCI{Dev: mgmtIndexNone, Channel: unix.HCI_CHANNEL_CONTROL}); err != nil {
			unix.Close(fd)
		}
	}
	if err != nil {
		m.update(func(s *BTState) { s.Err = err })
		return
	}
	m.f = os.NewFile(uintptr(fd), "bluetooth-mgmt") // non-blocking: in Go's poller, so deadlines work
	defer m.f.Close()
	m.buf = make([]byte, 1024)

	st, rp, err := m.cmd(mgmtReadInfo, nil)
	if err != nil {
		m.update(func(s *BTState) { s.Err = err })
		return
	}
	info, supported, current, ok := mgmtInfo(rp)
	if st != 0 || !ok {
		m.update(func(s *BTState) { s.PowerErr = "reading the controller's information: " + mgmtStatusText(st) })
		return
	}
	on := current&mgmtSettingPowered != 0
	m.wasOn = on
	m.update(func(s *BTState) { s.Info, s.WasOn, s.Powered = info, on, on })
	if supported&mgmtSettingLE != 0 && current&mgmtSettingLE == 0 {
		if st, _, err := m.cmd(mgmtSetLE, []byte{1}); err == nil && st == 0 {
			m.leOn = true
			m.update(func(s *BTState) { s.LEOn = true })
		}
	}
	if !on {
		st, _, err := m.cmd(mgmtSetPowered, []byte{1})
		if err != nil || st != 0 {
			why := mgmtStatusText(st)
			if err != nil {
				why = err.Error()
			}
			m.update(func(s *BTState) { s.PowerErr = why })
			<-m.stop
			m.restore()
			return
		}
		m.update(func(s *BTState) { s.Powered = true })
	}
	m.discover()
	for {
		select {
		case <-m.stop:
			m.restore()
			return
		case <-m.again:
			if !m.scanning {
				m.discover()
			}
		default:
		}
		if err := m.read(250 * time.Millisecond); err != nil {
			m.update(func(s *BTState) { s.Err = err })
			<-m.stop
			m.restore()
			return
		}
		// A round ended (the kernel stops after about 10 s), or the last start didn't take:
		// start the next.
		if !m.scanning && time.Since(m.tried) >= 2*time.Second {
			m.discover()
		}
	}
}

// discover starts a discovery round: classic and LE, else classic only, else LE only (then
// always the type that took). Busy means one is running already (bluetoothd's, or the last
// round still winding down): its results reach this socket too. When no type takes before any
// ever did, ScanErr says the kernel's answer.
func (m *mgmtScan) discover() {
	m.tried = time.Now()
	types := []byte{mgmtDiscoverAll, mgmtDiscoverBREDR, mgmtDiscoverLE}
	if m.dtype != 0 {
		types = []byte{m.dtype}
	}
	why := ""
	for _, t := range types {
		st, _, err := m.cmd(mgmtStartDiscovery, []byte{t})
		if err != nil {
			why = err.Error()
			break
		}
		switch st {
		case 0:
			m.ours, m.dtype, m.scanning = true, t, true
			m.update(func(s *BTState) { s.Scanning, s.Mode, s.ScanErr = true, btModes[t], "" })
			return
		case mgmtStatusBusy:
			m.scanning = true
			m.update(func(s *BTState) { s.Scanning = true })
			return
		}
		why = mgmtStatusText(st)
		if st != mgmtStatusRejected && st != mgmtStatusUnsupported && st != mgmtStatusInvalid {
			break
		}
	}
	if m.dtype == 0 {
		m.update(func(s *BTState) { s.ScanErr = why })
	}
}

// restore stops the discovery this scan started, powers the adapter off if it was off, and
// switches Low Energy off again if this scan switched it on.
func (m *mgmtScan) restore() {
	if m.ours && m.scanning {
		m.cmd(mgmtStopDiscovery, []byte{m.dtype})
	}
	if !m.wasOn {
		m.cmd(mgmtSetPowered, []byte{0})
	}
	if m.leOn {
		m.cmd(mgmtSetLE, []byte{0})
	}
}

// cmd sends a command to the adapter and waits up to 3 s for its answer (Command Complete or
// Command Status), handling the events that arrive meanwhile. It answers the status and the
// reply's parameters.
func (m *mgmtScan) cmd(op uint16, params []byte) (byte, []byte, error) {
	if _, err := m.f.Write(mgmtPacket(op, m.idx, params)); err != nil {
		return 0, nil, err
	}
	until := time.Now().Add(3 * time.Second)
	for time.Now().Before(until) {
		m.f.SetReadDeadline(until)
		n, err := m.f.Read(m.buf)
		if errors.Is(err, os.ErrDeadlineExceeded) {
			break
		}
		if err != nil {
			return 0, nil, err
		}
		ev, idx, data, ok := mgmtSplit(m.buf[:n])
		if !ok {
			continue
		}
		if (ev == mgmtEvComplete || ev == mgmtEvStatus) && idx == m.idx && len(data) >= 3 && binary.LittleEndian.Uint16(data) == op {
			return data[2], append([]byte(nil), data[3:]...), nil
		}
		m.handle(ev, idx, data)
	}
	return 0, nil, fmt.Errorf("no answer from the kernel to mgmt command 0x%04x", op)
}

// read waits up to d for one event and handles it.
func (m *mgmtScan) read(d time.Duration) error {
	m.f.SetReadDeadline(time.Now().Add(d))
	n, err := m.f.Read(m.buf)
	if errors.Is(err, os.ErrDeadlineExceeded) {
		return nil
	}
	if err != nil {
		return err
	}
	if ev, idx, data, ok := mgmtSplit(m.buf[:n]); ok {
		m.handle(ev, idx, data)
	}
	return nil
}

func (m *mgmtScan) handle(ev, idx uint16, data []byte) {
	if idx != m.idx {
		return
	}
	switch ev {
	case mgmtEvDeviceFound:
		d, confirm, ok := mgmtFound(data)
		if !ok {
			return
		}
		if confirm && d.Name == "" { // ask the kernel to look its name up (a later Device Found brings it)
			p := append(append([]byte(nil), data[0:7]...), 0)
			m.f.Write(mgmtPacket(mgmtConfirmName, m.idx, p))
		}
		m.update(func(s *BTState) {
			i, was := m.seen[d.Key]
			if !was {
				m.seen[d.Key] = len(s.Found)
				s.Found = append(s.Found, d)
				return
			}
			old := s.Found[i]
			if d.Name == "" {
				d.Name = old.Name
			}
			if d.Kind == "" {
				d.Kind = old.Kind
			}
			s.Found[i] = d
		})
	case mgmtEvDiscovering:
		if len(data) >= 2 {
			on := data[1] == 1
			m.scanning = on
			m.update(func(s *BTState) { s.Scanning = on })
		}
	case mgmtEvNewSettings:
		if len(data) >= 4 {
			on := binary.LittleEndian.Uint32(data)&1 != 0
			m.update(func(s *BTState) { s.Powered = on })
		}
	case mgmtEvIndexRemoved:
		m.update(func(s *BTState) { s.Gone = true })
	}
}
