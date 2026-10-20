package handson

import (
	"strings"
	"testing"

	"github.com/omaboot/boot-live/internal/checks"
)

// netStep is one thing the tester does during a network test: at At, pick answer Act (-1:
// skip), or type Keys (each rune a key; "\n" is Enter).
type netStep struct {
	At   float64
	Act  int
	Keys string
}

// playNet plays a network test on the demo Mac plus its Demo and script, with the tester's
// steps, until it decides or until; it returns the outcome (nil: undecided).
func playNet(t *testing.T, id string, until float64, script func(*Fake), steps ...netStep) *Outcome {
	t.Helper()
	s, _ := Lookup(id)
	f := NewFake()
	DemoMac(f)
	s.Demo(f)
	if script != nil {
		script(f)
	}
	env := &Env{}
	r := Begin(s, env, f)
	defer r.Stop()
	next := 0
	for tt := 0.0; tt <= until; tt += 0.1 {
		for next < len(steps) && steps[next].At <= tt+1e-9 {
			st := steps[next]
			switch {
			case st.Keys != "":
				for _, k := range st.Keys {
					key := string(k)
					if k == '\n' {
						key = "enter"
					}
					r.Key(key)
				}
			case st.Act < 0:
				r.Skip()
			default:
				r.Act(st.Act)
			}
			next++
		}
		r.Step(tt)
		if o := r.Outcome(); o != nil {
			return o
		}
	}
	return nil
}

// The network tests' verdicts: supported only with the boot.watch transfer
// verified over the interface under test; a join, a link or an address alone never pass, and
// what the tester answers is observed, never supported.
func TestNetworkRules(t *testing.T) {
	join := []netStep{{At: 3, Act: 0}, {At: 3.5, Keys: "correct horse\n"}}
	set := func(k, v string) func(*Fake) { return func(f *Fake) { f.Set(fakeNet+k, v) } }
	cases := []struct {
		name, id string
		script   func(*Fake)
		steps    []netStep
		until    float64
		want     string
	}{
		{"wifi: joined, data verified both ways", "network.wifi", nil, join, 20, "supported/automatic"},
		{"wifi: joined, wrong bytes", "network.wifi", set("watch", "broken"), join, 20, "partial/automatic"},
		{"wifi: joined, no test server", "network.wifi", set("watch", "none"), join, 20, "not_tested/uncertain"},
		{"wifi: joined, a sign-in page", "network.wifi", set("watch", "signin"), join, 20, "not_tested/uncertain"},
		{"wifi: joined on the Network screen", "network.wifi", func(f *Fake) { f.Set(fakeNet+"joined", "maple-house"); f.Set(fakeNet+"addr/wlan0", "192.168.1.9") }, nil, 10, "supported/automatic"},
		{"wifi: scan only, nothing picked", "network.wifi", nil, nil, 60, "undecided"},
		{"wifi: picked, no password typed", "network.wifi", nil, []netStep{{At: 3, Act: 0}}, 60, "undecided"},
		{"wifi: refused twice, then it won't join", "network.wifi", set("join", "refuse"),
			[]netStep{{At: 3, Act: 0}, {At: 3.5, Keys: "abcdefgh\n"}, {At: 6, Act: 7}, {At: 6.5, Keys: "abcdefgh\n"}, {At: 9, Act: 7}}, 20, "partial/observed"},
		{"wifi: no address", "network.wifi", set("join", "noaddr"), append(join, netStep{At: 7, Act: 7}), 20, "partial/observed"},
		{"wifi: hears nothing, networks in range", "network.wifi", set("aps", ""), []netStep{{At: 3, Act: 1}}, 10, "failed/observed"},
		{"wifi: scan fails twice", "network.wifi", set("scanerr", "the radio is off"), nil, 10, "failed/automatic"},
		{"wifi: skipped", "network.wifi", nil, []netStep{{At: 3, Act: -1}}, 10, "not_tested/no-equipment"},
		{"ethernet: a cable, an address, data verified", "network.ethernet", nil, nil, 20, "supported/automatic"},
		{"ethernet: wrong bytes", "network.ethernet", set("watch", "broken"), nil, 20, "partial/automatic"},
		{"ethernet: dead, nowhere else to ask", "network.ethernet", set("watch", "dead"), nil, 20, "not_tested/uncertain"},
		{"ethernet: no test server", "network.ethernet", set("watch", "none"), nil, 20, "not_tested/uncertain"},
		{"ethernet: a link, no address", "network.ethernet", func(f *Fake) { f.script = f.script[:1] }, nil, 40, "not_tested/uncertain"},
		{"ethernet: no cable, said plugged in too early", "network.ethernet", func(f *Fake) { f.script = nil }, []netStep{{At: 2, Act: 0}}, 5, "undecided"},
		{"ethernet: no cable, said plugged in", "network.ethernet", func(f *Fake) { f.script = nil }, []netStep{{At: 11, Act: 0}}, 15, "failed/observed"},
		{"ethernet: an adapter's link isn't the port's", "network.ethernet", func(f *Fake) {
			f.script = nil
			f.Set(fakeNet+"link/eth1", "usb-ethernet|USB 10/100 LAN")
			f.Set("/sys/class/net/eth1/carrier", "1")
			f.Set(fakeNet+"addr/eth1", "10.0.0.5")
		}, nil, 30, "undecided"},
		{"ethernet: turned off for Wi-Fi, back on", "network.ethernet", set("down/eth0", "1"), nil, 20, "supported/automatic"},
	}
	for _, c := range cases {
		o := playNet(t, c.id, c.until, c.script, c.steps...)
		if got := status(o); got != c.want {
			ev := ""
			if o != nil && len(o.Results) > 0 {
				ev = o.Results[0].Evidence
			}
			t.Errorf("%s: %s (%s), want %s", c.name, got, ev, c.want)
		}
		if o != nil {
			for _, x := range o.Results {
				for _, secret := range []string{"maple-house", "correct horse", "abcdefgh"} {
					if strings.Contains(x.Evidence, secret) {
						t.Errorf("%s: %q in the evidence: %s", c.name, secret, x.Evidence)
					}
				}
				if x.ID != c.id {
					t.Errorf("%s: a result for %s", c.name, x.ID)
				}
			}
		}
	}
}

// Both network tests are offered whatever the Network screen did: an offline run (no
// connection, nothing joined) still gets them.
func TestNetworkTestsOffline(t *testing.T) {
	env := fixtureEnv(t, "imac10,1")
	env.Checks.Net = checks.NetFacts{} // "Skip, test offline"
	var ids []string
	for _, s := range Plan(env) {
		ids = append(ids, s.ID)
	}
	for _, id := range []string{"network.wifi", "network.ethernet"} {
		if !strings.Contains(" "+strings.Join(ids, " ")+" ", " "+id+" ") {
			t.Errorf("%s not offered offline: %v", id, ids)
		}
	}
}

// The password field takes what's typed (pasted text too) and nothing else.
func TestTextKey(t *testing.T) {
	for key, want := range map[string]string{"a": "a", " ": " ", "É": "É", "[hunter22]": "hunter22", "ab": "ab",
		"up": "", "ctrl+u": "", "alt+a": "", "f5": "", "enter": "", "tab": "", "backspace": ""} {
		if got := textKey(key); got != want {
			t.Errorf("textKey(%q) = %q, want %q", key, got, want)
		}
	}
}
