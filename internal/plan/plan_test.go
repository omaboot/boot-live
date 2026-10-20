package plan

import (
	"strings"
	"testing"
)

var known = Known{
	Test: func(id string) bool {
		return strings.Contains(id, ".") && id != "nope.test" && id != "graphics.discrete"
	},
	Quick: func(id string) bool { return id == "graphics.discrete" },
}

const good = `version: 1
defaults:
  network: ethernet
  results: both
  start: console
  tests: {default: skip}
macs:
  - match: iMac18,3
    configuration: imac18-3-5k-2017-a
    bugreport: [after-quick, at-finish]
    network: {wifi: {ssid: Studio, password: "a long secret"}}
    tests:
      ports.sd-card: setup
      audio.speakers: hands-on
  - match: MacBookAir5,2
`

func TestParse(t *testing.T) {
	p, ps := Parse([]byte(good), known)
	if len(ps) > 0 {
		t.Fatal(ps)
	}
	r, ok := p.For("iMac18,3")
	if !ok || r.Configuration != "imac18-3-5k-2017-a" || r.Results != "both" || r.Finish != "wait" || r.Start != "console" {
		t.Fatalf("%+v", r)
	}
	if r.Network.Kind != "wifi" || r.Network.SSID != "Studio" || strings.Contains(r.Network.Text(), "secret") {
		t.Fatalf("network %+v", r.Network)
	}
	if r.Mode("ports.sd-card") != Setup || r.Mode("power.lid") != Skip || r.Unattended() {
		t.Fatal("modes")
	}
	if !r.Bugreport[AfterQuick] || r.Bugreport[OnProblem] {
		t.Fatal("bugreport")
	}
	air, ok := p.For("MacBookAir5,2")
	if !ok || air.Network.Kind != "ethernet" || !air.Bugreport[OnProblem] || !air.Unattended() {
		t.Fatalf("air %+v", air)
	}
	sent, _ := Parse([]byte("version: 1\nmacs:\n  - match: x\n    bugreport: {when: [at-finish], send: true}\n"), known)
	if r, _ := sent.For("x"); !r.BugSend || !r.Bugreport[AtFinish] || r.Bugreport[OnProblem] {
		t.Fatalf("bugreport send: %+v", r)
	}
	if _, ok := p.For("Macmini4,1", "macmini4-1-mid-2010-a"); ok {
		t.Fatal("no section")
	}
}

// Every refusal names the line and what to do instead.
func TestRefused(t *testing.T) {
	for _, c := range []struct{ src, want string }{
		{"version: 1\nmacs:\n  - match: iMac18,3\n    colour: red\n", "line 4: unknown key"},
		{"version: 2\nmacs:\n  - match: x\n", "line 1: version must be 1"},
		{"version: 1\nmacs:\n  - match: x\n    tests: {power.lid: setup}\n", "power.lid can't be setup"},
		{"version: 1\nmacs:\n  - match: x\n    tests: {nope.test: skip}\n", "line 4: macs[1] (x): tests: nope.test isn't a hands-on test"},
		{"version: 1\nmacs:\n  - match: x\n    tests: {graphics.discrete: skip}\n", "quick-pass check"},
		{"version: 1\nmacs:\n  - match: x\n    tests: {audio.speakers: phone}\n", "iPhone app"},
		{"version: 1\nmacs:\n  - match: x\n    network: {wifi: {ssid: A, password: short}}\n", "8 to 63"},
		{"version: 1\nmacs:\n  - match: x\n    bugreport: [sometimes]\n", "isn't one of after-quick"},
		{"version: 1\nmacs: []\n", "at least one section"},
		{"version: 1\nmacs:\n  - match: x\n    bugreport: {when: [at-finish], send: yes}\n", "send: true or false"},
		{"version: 1\nmacs:\n  - match: x\n    start: later\n", "line 4: macs[1] (x): start: \"later\" isn't one of auto, console"},
	} {
		_, ps := Parse([]byte(c.src), known)
		if len(ps) == 0 || !strings.Contains(ps[0].String(), c.want) {
			t.Errorf("%q: %v, want %q", c.src, ps, c.want)
		}
	}
}
