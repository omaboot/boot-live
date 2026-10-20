package ui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// joinStub records what the Network screen asks the backend to join.
type joinStub struct {
	Backend
	ssid, password string
	cleared        bool
}

func (s *joinStub) NetConnect(ssid, password string) { s.ssid, s.password = ssid, password }
func (s *joinStub) NetClear()                        { s.cleared = true }

// The backend's state comes every two seconds while the password is typed, with no SSID (it
// learns it when the join starts): the network picked is still the one joined ("… is gone: scan
// again").
func TestJoinKeepsThePick(t *testing.T) {
	be := &joinStub{}
	m := &Model{st: NewState(), f: &Facts{}, be: be}
	m.st.Scene = Network
	nets := []WifiNet{{SSID: "Cafe", Bars: 2, Security: "WPA2"}, {SSID: "Home", Bars: 4, Security: "WPA2"}}
	m.st.Net = NetState{Phase: NetList, Networks: nets}
	m.join(1)
	if !m.st.Pw {
		t.Fatal("no password box for a WPA2 network")
	}
	for _, r := range "secret" {
		m.pwKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		m.netMsg(NetState{Phase: NetList, Networks: nets, Ethernet: "no adapter or cable"}) // the links' watch
	}
	m.pwKey(tea.KeyMsg{Type: tea.KeyEnter})
	if be.ssid != "Home" || be.password != "secret" {
		t.Errorf("joined %q with %q, want Home with secret", be.ssid, be.password)
	}
}

// After a failed join the backend's state still carries the error until it drops it (NetClear),
// and a state already on its way when the network is picked again carries it too: neither
// closes the password box. The same error from the next join does.
func TestOldErrorKeepsThePasswordBox(t *testing.T) {
	be := &joinStub{}
	m := &Model{st: NewState(), f: &Facts{}, be: be}
	m.st.Scene = Network
	nets := []WifiNet{{SSID: "Home", Bars: 4, Security: "WPA2"}}
	const refused = "the network refused the password"
	m.netMsg(NetState{Phase: NetList, Networks: nets, Err: refused})
	m.join(0)
	if !m.st.Pw || !be.cleared {
		t.Fatalf("box %v, backend told to clear %v: want both", m.st.Pw, be.cleared)
	}
	m.pwKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("s")})
	m.netMsg(NetState{Phase: NetList, Networks: nets, Err: refused}) // on its way before NetClear
	m.netMsg(NetState{Phase: NetList, Networks: nets})               // NetClear's
	m.netMsg(NetState{Phase: NetList, Networks: nets})               // the links' watch
	if !m.st.Pw || m.st.Net.Err != "" {
		t.Fatalf("box %v with error %q: want the box and no error", m.st.Pw, m.st.Net.Err)
	}
	m.pwKey(tea.KeyMsg{Type: tea.KeyEnter})
	m.netMsg(NetState{Phase: NetJoining, Networks: nets, SSID: "Home"})
	m.netMsg(NetState{Phase: NetList, Networks: nets, Err: refused})
	if m.st.Pw || m.st.Net.Err != refused {
		t.Errorf("box %v with error %q after the next join failed: want no box and %q", m.st.Pw, m.st.Net.Err, refused)
	}
}
