package handson

import (
	"math"
	"strings"
	"testing"
)

// Every layout is complete: rows of 14.5 units, every key to press has a code, no code lights
// two keys, and the US ANSI letters, digits and symbols are all there.
func TestKeyboardLayouts(t *testing.T) {
	cases := []struct {
		ident, chip string
		id          devID
		fkeys, esc  bool
		table       string
	}{
		{"", "", devID{busUSB, 0x05ac, 0x024c}, true, true, "modern"},
		{"MacBookPro5,1", "", devID{busUSB, 0x05ac, 0x0236}, true, true, "modern"},
		{"MacBookPro2,2", "", devID{busUSB, 0x05ac, 0x021a}, true, true, "powerbook"},
		{"MacBook1,1", "", devID{busUSB, 0x05ac, 0x0218}, true, true, "powerbook"},
		{"MacBookAir3,2", "", devID{busUSB, 0x05ac, 0x023f}, true, true, "air"},
		{"MacBook9,1", "", devID{busSPI, 0, 0}, true, true, "modern"},
		{"MacBookPro14,1", "", devID{busSPI, 0, 0}, true, true, "modern"},
		{"MacBookPro14,3", "t1", devID{busSPI, 0, 0}, false, false, "modern"},
		{"MacBookPro15,1", "t2", devID{busUSB, 0x05ac, 0x027b}, false, false, "modern"},
		{"MacBookPro16,1", "t2", devID{busUSB, 0x05ac, 0x0340}, false, true, "modern"},
		{"MacBookAir9,1", "t2", devID{busUSB, 0x05ac, 0x0280}, true, true, "modern"},
	}
	ansi := []uint16{41, 12, 13, 14, 15, 26, 27, 43, 58, 39, 40, 28, 42, 51, 52, 53, 54, 29, 56, 125, 57, 126, 100, 103, 105, 108, 106}
	for c := uint16(2); c <= 11; c++ { // 1–0
		ansi = append(ansi, c)
	}
	for c := uint16(16); c <= 25; c++ { // q–p
		ansi = append(ansi, c)
	}
	for c := uint16(30); c <= 38; c++ { // a–l
		ansi = append(ansi, c)
	}
	for c := uint16(44); c <= 50; c++ { // z–m
		ansi = append(ansi, c)
	}
	for _, c := range cases {
		l := keyboardLayout(c.ident, c.chip, c.id, true)
		name := c.ident + " " + c.chip
		if l.table != c.table {
			t.Errorf("%s: fn table %s, want %s", name, l.table, c.table)
		}
		for r, row := range l.rows {
			w := 0.0
			for _, k := range row {
				w += k.w
			}
			if math.Abs(w-14.5) > 1e-9 {
				t.Errorf("%s: row %d is %.2f units", name, r, w)
			}
		}
		code, _ := l.lookup()
		ks, _ := l.keys()
		owner := map[uint16]string{}
		for _, k := range ks {
			if k.off != "" {
				continue
			}
			if len(k.codes) == 0 {
				t.Errorf("%s: %s has no code", name, k.id())
			}
			for _, x := range k.codes {
				if o, dup := owner[x]; dup {
					t.Errorf("%s: code %d lights both %s and %s", name, x, o, k.id())
				}
				owner[x] = k.id()
			}
		}
		for _, x := range ansi {
			if _, ok := code[x]; !ok {
				t.Errorf("%s: no key for code %d", name, x)
			}
		}
		if _, ok := code[fKeys[4]]; ok != c.fkeys {
			t.Errorf("%s: F-keys %v, want %v", name, ok, c.fkeys)
		}
		if _, ok := code[keyEsc]; ok != c.esc {
			t.Errorf("%s: esc %v, want %v", name, ok, c.esc)
		}
	}
	if l := keyboardLayout("MacBookAir5,2", "", devID{}, false); strings.Contains(strings.Join(func() []string {
		var ids []string
		ks, _ := l.keys()
		for _, k := range ks {
			if k.off == "" {
				ids = append(ids, k.id())
			}
		}
		return ids
	}(), " "), "fn") {
		t.Error("fn is to be pressed on a keyboard that doesn't report it")
	}
}

// The top row lights its F-key for the F-key code, the keyboard's own media key, and another
// table's media key at the same place.
func TestKeyboardTopRow(t *testing.T) {
	l := keyboardLayout("MacBook1,1", "", devID{busUSB, 0x05ac, 0x0218}, true)
	code, alt := l.lookup()
	ks, _ := l.keys()
	at := func(c uint16) string {
		if i, ok := code[c]; ok {
			return ks[i].id()
		}
		if i, ok := alt[c]; ok {
			return ks[i].id() + " (alt)"
		}
		return "none"
	}
	for c, want := range map[uint16]string{61: "F3", 113: "F3", 227: "F7", 120: "F3 (alt)", 165: "F7 (alt)", 224: "F1"} {
		if got := at(c); got != want {
			t.Errorf("code %d lights %s, want %s", c, got, want)
		}
	}
}

// replace swaps a demo device for another.
func replace(f *Fake, event, name string, caps map[string]string) {
	for p := range f.Files {
		if strings.HasPrefix(p, "/sys/class/input/"+event+"/") {
			delete(f.Files, p)
		}
	}
	f.Device(event, name, caps)
}

// The input tests' rules, on scripted Macs.
func TestInputRules(t *testing.T) {
	demo := func(id string) func(*Fake) {
		s, _ := Lookup(id)
		return s.Demo
	}
	tpOnly := func(n int) func(*Fake) { // the demo trackpad, its first n frames' worth of time
		return func(f *Fake) {
			demoTrackpad(f)
			var keep []fakeEvent
			for _, e := range f.events {
				if e.at < float64(n) {
					keep = append(keep, e)
				}
			}
			f.events = keep
		}
	}
	cases := []struct {
		name, id string
		until    float64
		script   func(*Fake)
		picks    map[float64]int
		want     string
	}{
		{"keyboard, every key", "input.keyboard", 40, demo("input.keyboard"), map[float64]int{32.5: 0}, "supported/automatic"},
		{"keyboard, one key dead", "input.keyboard", 40, demo("input.keyboard"), map[float64]int{32.5: 1}, "partial/observed"},
		{"keyboard, nothing at all", "input.keyboard", 30, nil, map[float64]int{22: 1}, "failed/observed"},
		{"keyboard, no built-in one", "input.keyboard", 1, func(f *Fake) {
			replace(f, "event2", "Logitech USB Keyboard", map[string]string{"key": capWords(keyA, keySpace)})
			demoID(f, "event2", busUSB, 0x046d, 0xc31c)
		}, nil, "failed/automatic"},
		{"keyboard skipped", "input.keyboard", 2, nil, map[float64]int{1: -1}, "not_tested/other"},
		{"trackpad, everything", "input.trackpad", 15, demo("input.trackpad"), nil, "supported/automatic"},
		{"trackpad, no pinch or swipe", "input.trackpad", 15, tpOnly(8), map[float64]int{12: 0}, "partial/observed"},
		{"trackpad, nothing happens", "input.trackpad", 15, tpOnly(0), map[float64]int{9: 0}, "failed/observed"},
		{"trackpad, mouse mode", "input.trackpad", 5, func(f *Fake) {
			replace(f, "event3", "Apple Inc. Apple Internal Keyboard / Trackpad", map[string]string{"ev": "7", "key": capWords(btnLeft), "rel": capWords(relX, relY)})
			for i := 0; i < 20; i++ {
				f.Event(1+float64(i)*0.05, "event3", EvRel, relX, 30)
				f.Event(1+float64(i)*0.05, "event3", EvSyn, evSynReport, 0)
			}
			f.Event(2.5, "event3", EvKey, btnLeft, 1)
			f.Event(2.5, "event3", EvSyn, evSynReport, 0)
		}, nil, "partial/automatic"},
		{"trackpad, none", "input.trackpad", 1, func(f *Fake) { replace(f, "event3", "", nil) }, nil, "failed/automatic"},
		{"force touch, click and pressure", "input.force-touch", 8, demo("input.force-touch"), nil, "supported/automatic"},
		{"force touch, no pressure axis", "input.force-touch", 8, func(f *Fake) {
			d := demoBcm5974(f, "event3", "bcm5974")
			d.frame(1, false, [2]float64{0.5, 0.5})
			d.frame(2, true, [2]float64{0.5, 0.5})
		}, nil, "partial/automatic"},
		{"force touch, pressure flat", "input.force-touch", 12, func(f *Fake) {
			d := demoBcm5974(f, "event3", "Apple Inc. Apple Internal Keyboard / Trackpad", absMtPressure)
			f.Set("/dev/input/event3/absinfo/58", "0 253")
			d.p = []int{60}
			d.frame(1, false, [2]float64{0.5, 0.5})
			d.frame(2, true, [2]float64{0.5, 0.5})
		}, map[float64]int{6: 0, 7.5: 0}, "partial/observed"},
		{"force touch, no click", "input.force-touch", 12, nil, map[float64]int{9: 0}, "failed/observed"},
	}
	for _, c := range cases {
		if got := status(play(t, c.id, c.until, c.script, c.picks)); got != c.want {
			t.Errorf("%s: %s, want %s", c.name, got, c.want)
		}
	}
}

// A partial keyboard lists the keys that never registered; the way out works from evdev alone.
func TestKeyboardEvidence(t *testing.T) {
	o := play(t, "input.keyboard", 40, demo(t, "input.keyboard"), map[float64]int{32.5: 1})
	if o == nil || !strings.Contains(o.Results[0].Evidence, "not registered: right option;") {
		t.Errorf("evidence: %+v", o)
	}
	s, _ := Lookup("input.keyboard")
	f := NewFake()
	DemoMac(f)
	r := Begin(s, &Env{}, f)
	defer r.Stop()
	r.Step(1)
	if !r.TakesKeys() {
		t.Fatal("the keyboard test doesn't take the keys")
	}
	r.Key("esc") // from another keyboard
	if r.TakesKeys() {
		t.Error("esc from another keyboard didn't hand the keys back")
	}
}

func demo(t *testing.T, id string) func(*Fake) {
	t.Helper()
	s, ok := Lookup(id)
	if !ok {
		t.Fatalf("no test %s", id)
	}
	return s.Demo
}
