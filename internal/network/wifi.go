package network

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/godbus/dbus/v5"

	"github.com/omaboot/boot-live/internal/sysroot"
)

// AP is a Wi-Fi network in range.
type AP struct {
	SSID     string
	Signal   int    // dBm
	Security string // open, psk, 8021x, wep
	path     dbus.ObjectPath
}

// Bars is the signal as 1–4 bars.
func (a AP) Bars() int {
	switch {
	case a.Signal >= -60:
		return 4
	case a.Signal >= -70:
		return 3
	case a.Signal >= -80:
		return 2
	}
	return 1
}

// Open reports whether it needs no password.
func (a AP) Open() bool { return a.Security == "open" }

// Wifi scans and joins Wi-Fi networks.
type Wifi interface {
	Device() string // the wireless interface, e.g. wlan0
	Scan(ctx context.Context) ([]AP, error)
	// Connect joins a network; password is used only if the network asks for one, kept in
	// memory and never written anywhere.
	Connect(ctx context.Context, ap AP, password string) error
	// Disconnect leaves the joined network (switching back to a cable).
	Disconnect(ctx context.Context) error
	Close()
}

// ErrNoWifi means there's no wireless device iwd can use.
var ErrNoWifi = errors.New("no Wi-Fi device")

// ErrPassword means the network refused the password.
var ErrPassword = errors.New("the network refused the password")

// NewWifi connects to iwd on the system bus, or, on a fixture tree, simulates Wi-Fi from the
// tree's iwd-networks.txt (lines "ssid|dBm|security").
func NewWifi(r sysroot.Root) (Wifi, error) {
	if !r.IsSystem() {
		return newFake(r)
	}
	return newIWD()
}

// ── iwd over D-Bus ──────────────────────────────────────────────────────────────

const (
	iwdService   = "net.connman.iwd"
	ifStation    = "net.connman.iwd.Station"
	ifNetwork    = "net.connman.iwd.Network"
	ifDevice     = "net.connman.iwd.Device"
	ifAgentMgr   = "net.connman.iwd.AgentManager"
	ifAgent      = "net.connman.iwd.Agent"
	agentPath    = dbus.ObjectPath("/org/omaboot/bootlive/agent")
	iwdRoot      = dbus.ObjectPath("/net/connman/iwd")
	scanTimeout  = 15 * time.Second
	propsGetName = "org.freedesktop.DBus.Properties.Get"
)

type iwd struct {
	conn    *dbus.Conn
	station dbus.ObjectPath
	name    string
	agent   *agent
}

// agent answers iwd's password questions with the password the person typed.
type agent struct {
	mu       sync.Mutex
	password string
	asked    bool
}

func (a *agent) Release() *dbus.Error { return nil }
func (a *agent) RequestPassphrase(dbus.ObjectPath) (string, *dbus.Error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.asked = true
	if a.password == "" {
		return "", dbus.NewError("net.connman.iwd.Agent.Error.Canceled", nil)
	}
	return a.password, nil
}
func (a *agent) RequestPrivateKeyPassphrase(dbus.ObjectPath) (string, *dbus.Error) {
	return "", dbus.NewError("net.connman.iwd.Agent.Error.Canceled", nil)
}
func (a *agent) RequestUserNameAndPassword(dbus.ObjectPath) (string, string, *dbus.Error) {
	return "", "", dbus.NewError("net.connman.iwd.Agent.Error.Canceled", nil)
}
func (a *agent) RequestUserPassword(dbus.ObjectPath, string) (string, *dbus.Error) {
	return "", dbus.NewError("net.connman.iwd.Agent.Error.Canceled", nil)
}
func (a *agent) Cancel(string) *dbus.Error { return nil }

func newIWD() (*iwd, error) {
	conn, err := dbus.ConnectSystemBus()
	if err != nil {
		return nil, fmt.Errorf("D-Bus: %w", err)
	}
	w := &iwd{conn: conn, agent: &agent{}}
	var objs map[dbus.ObjectPath]map[string]map[string]dbus.Variant
	if err := conn.Object(iwdService, "/").Call("org.freedesktop.DBus.ObjectManager.GetManagedObjects", 0).Store(&objs); err != nil {
		conn.Close()
		return nil, fmt.Errorf("iwd: %w", err)
	}
	var paths []string
	for p, ifs := range objs {
		if _, ok := ifs[ifStation]; ok {
			paths = append(paths, string(p))
		}
	}
	if len(paths) == 0 {
		// a device that's powered off has no Station: power it on and look again
		for p, ifs := range objs {
			if dev, ok := ifs[ifDevice]; ok {
				if on, _ := dev["Powered"].Value().(bool); !on {
					conn.Object(iwdService, p).Call("org.freedesktop.DBus.Properties.Set", 0, ifDevice, "Powered", dbus.MakeVariant(true))
				}
			}
		}
		time.Sleep(time.Second)
		objs = nil
		if err := conn.Object(iwdService, "/").Call("org.freedesktop.DBus.ObjectManager.GetManagedObjects", 0).Store(&objs); err == nil {
			for p, ifs := range objs {
				if _, ok := ifs[ifStation]; ok {
					paths = append(paths, string(p))
				}
			}
		}
	}
	if len(paths) == 0 {
		conn.Close()
		return nil, ErrNoWifi
	}
	sort.Strings(paths)
	w.station = dbus.ObjectPath(paths[0])
	if dev, ok := objs[w.station][ifDevice]; ok {
		w.name, _ = dev["Name"].Value().(string)
	}
	if err := conn.Export(w.agent, agentPath, ifAgent); err != nil {
		conn.Close()
		return nil, err
	}
	if err := conn.Object(iwdService, iwdRoot).Call(ifAgentMgr+".RegisterAgent", 0, agentPath).Err; err != nil {
		conn.Close()
		return nil, fmt.Errorf("iwd agent: %w", err)
	}
	return w, nil
}

func (w *iwd) Device() string { return w.name }

func (w *iwd) prop(p dbus.ObjectPath, iface, name string) (dbus.Variant, error) {
	var v dbus.Variant
	err := w.conn.Object(iwdService, p).Call(propsGetName, 0, iface, name).Store(&v)
	return v, err
}

func (w *iwd) Scan(ctx context.Context) ([]AP, error) {
	st := w.conn.Object(iwdService, w.station)
	if err := st.CallWithContext(ctx, ifStation+".Scan", 0).Err; err != nil && !strings.Contains(err.Error(), "InProgress") {
		return nil, fmt.Errorf("scan: %w", err)
	}
	deadline := time.Now().Add(scanTimeout)
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(400 * time.Millisecond):
		}
		v, err := w.prop(w.station, ifStation, "Scanning")
		if err != nil {
			return nil, err
		}
		if s, _ := v.Value().(bool); !s {
			break
		}
	}
	var ordered []struct {
		Path   dbus.ObjectPath
		Signal int16
	}
	if err := st.CallWithContext(ctx, ifStation+".GetOrderedNetworks", 0).Store(&ordered); err != nil {
		return nil, fmt.Errorf("networks: %w", err)
	}
	var out []AP
	for _, o := range ordered {
		name, _ := w.prop(o.Path, ifNetwork, "Name")
		typ, _ := w.prop(o.Path, ifNetwork, "Type")
		ssid, _ := name.Value().(string)
		sec, _ := typ.Value().(string)
		if ssid == "" {
			continue
		}
		out = append(out, AP{SSID: ssid, Signal: int(o.Signal) / 100, Security: sec, path: o.Path})
	}
	return out, nil
}

func (w *iwd) Connect(ctx context.Context, ap AP, password string) error {
	w.agent.mu.Lock()
	w.agent.password, w.agent.asked = password, false
	w.agent.mu.Unlock()
	defer func() { // the password lives only for the call
		w.agent.mu.Lock()
		w.agent.password = ""
		w.agent.mu.Unlock()
	}()
	err := w.conn.Object(iwdService, ap.path).CallWithContext(ctx, ifNetwork+".Connect", 0).Err
	if err != nil {
		var de dbus.Error
		if errors.As(err, &de) && (strings.HasSuffix(de.Name, ".Failed") || strings.HasSuffix(de.Name, ".Aborted")) && !ap.Open() {
			// iwd's own error goes along for the Wi-Fi log: it says Failed for more than a
			// wrong password
			return fmt.Errorf("%w (iwd: %s)", ErrPassword, de.Error())
		}
		return err
	}
	return nil
}

func (w *iwd) Disconnect(ctx context.Context) error {
	return w.conn.Object(iwdService, w.station).CallWithContext(ctx, ifStation+".Disconnect", 0).Err
}

func (w *iwd) Close() {
	w.conn.Object(iwdService, iwdRoot).Call(ifAgentMgr+".UnregisterAgent", 0, agentPath)
	w.conn.Close()
}

// ── fixture Wi-Fi ───────────────────────────────────────────────────────────────

type fake struct{ aps []AP }

func newFake(r sysroot.Root) (*fake, error) {
	f := &fake{}
	for _, l := range r.Lines("/iwd-networks.txt") {
		p := strings.Split(l, "|")
		if len(p) != 3 {
			continue
		}
		dbm, _ := strconv.Atoi(p[1])
		f.aps = append(f.aps, AP{SSID: p[0], Signal: dbm, Security: p[2]})
	}
	if len(f.aps) == 0 {
		return nil, ErrNoWifi
	}
	return f, nil
}

func (f *fake) Device() string { return "wlan0" }
func (f *fake) Scan(ctx context.Context) ([]AP, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-time.After(1500 * time.Millisecond):
	}
	return f.aps, nil
}

// Connect pretends to join: any password of 8 characters or more works.
func (f *fake) Connect(ctx context.Context, ap AP, password string) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(1500 * time.Millisecond):
	}
	if !ap.Open() && len(password) < 8 {
		return ErrPassword
	}
	fakeJoined.Store(true)
	return nil
}

// fakeJoined: the fixture's simulated Wi-Fi has "joined" a network, so the fixture is online.
var fakeJoined atomic.Bool

func (f *fake) Disconnect(context.Context) error { return nil }
func (f *fake) Close()                           {}
