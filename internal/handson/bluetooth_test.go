package handson

import (
	"fmt"
	"slices"
	"strings"
	"testing"
)

// The pick list in a busy room: it shows 8 at a time; names fill in where a device already is;
// a device that turns up with a name joins the page on screen (in the place of its weakest
// unnamed one) instead of hiding on a later page; "Mine isn't listed" shows the devices not seen
// yet before it decides; and the report counts named and unnamed devices, never by name.
func TestBluetoothBusyRoom(t *testing.T) {
	s, _ := Lookup("network.bluetooth")
	f := NewFake()
	DemoMac(f)
	s.Demo(f)
	f.script = nil // the demo's own three devices out: this room instead
	FakeBluetoothDevice(f, 3, "hci0", BTDevice{Key: "phone", Maker: "Apple", LE: true, RSSI: -44})
	FakeBluetoothDevice(f, 9, "hci0", BTDevice{Key: "phone", Name: "Carl's iPhone", Kind: "phone", RSSI: -44})
	FakeBluetoothDevice(f, 11, "hci0", BTDevice{Key: "laptop", Name: "ThinkPad X1", Kind: "computer", RSSI: -88})
	for i := 0; i < 15; i++ {
		FakeBluetoothDevice(f, 3.2, "hci0", BTDevice{Key: fmt.Sprintf("le%02d", i), Maker: "Apple", LE: true, RSSI: -50 - 2*i})
	}
	r := Begin(s, &Env{}, f)
	defer r.Stop()
	b := r.Run.(*bluetooth)
	step := func(to float64) {
		for tt := r.Ctx.T; tt <= to; tt += 0.1 {
			r.Step(tt)
		}
	}
	labels := func() []string {
		var out []string
		for _, a := range r.View().Actions {
			out = append(out, a.Label)
		}
		return out
	}
	step(8)
	if v := r.View(); len(b.onPage()) != btPickMax || !strings.Contains(v.Ask, "8 nearest of 16") {
		t.Fatalf("at 8 s: %d on the page, %q", len(b.onPage()), v.Ask)
	}
	step(9.5)
	if l := labels(); l[0] != "Carl's iPhone" {
		t.Errorf("the phone's name didn't fill in where it was: %v", l)
	}
	step(11.5)
	if l := labels(); !slices.Contains(l[:btPickMax], "ThinkPad X1") || len(b.onPage()) != btPickMax {
		t.Errorf("the laptop didn't join the page on screen: %v", l)
	}
	step(31)
	r.Act(btPickMax) // Mine isn't listed: the devices not seen yet first
	if b.page != 1 || r.Outcome() != nil {
		t.Fatalf("Mine isn't listed decided with devices unseen (page %d)", b.page)
	}
	step(31.5)
	r.Act(len(b.onPage()))
	o := r.Outcome()
	if o == nil || status(o) != "partial/observed" {
		t.Fatalf("outcome %+v", o)
	}
	ev := o.Results[0].Evidence
	if !strings.Contains(ev, "17 device(s), 2 named and 15 unnamed") || !strings.Contains(ev, "wasn't among the 17 listed") ||
		strings.Contains(ev, "Carl") || strings.Contains(ev, "ThinkPad") {
		t.Errorf("evidence: %s", ev)
	}
}
