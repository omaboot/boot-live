package handson

import "testing"

// The disc goes back out whenever the optical test ends (0.7.3 review: a disc left in the drive
// hung an iMac10,1's sleep test): after a read, skipped or left mid-read, "Eject disc"; an
// empty drive's tray is never opened by leaving, and the OmaBoot? disc is never ejected.
func TestOpticalEjectsWhenItEnds(t *testing.T) {
	s, _ := Lookup("storage.optical")
	cases := []struct {
		name   string
		kind   string
		until  float64
		act    map[float64]int // an answer by index (-1: Skip)
		ejects int             // ejects asked for, by the time the screen is left
		in     bool            // the disc still in the drive at the end
		busy   bool            // the drive still worked (a read or eject outlasting the test)
	}{
		{"read, then out", "data", 10, nil, 1, false, false},
		{"skipped mid-read", "data", 5, map[float64]int{4.8: -1}, 1, false, true},
		{"left mid-read", "data", 4.8, nil, 1, false, true},
		{"skipped with no disc", "none", 3, map[float64]int{2: -1}, 0, false, false},
		{"nothing happens", "none", 27, map[float64]int{26: 0}, 0, false, false},
		{"eject disc while waiting", "none", 4, map[float64]int{2: 1}, 1, false, false},
		{"won't come out", "stuck", 12, nil, 1, true, false},
	}
	for _, c := range cases {
		f := NewFake()
		DemoMac(f)
		DemoDisc(f, c.kind)
		r := Begin(s, &Env{Optical: true}, f)
		for tt := 0.0; tt <= c.until; tt += 0.1 {
			for at, a := range c.act {
				if at > tt-0.1 && at <= tt {
					if a < 0 {
						r.Skip()
					} else {
						r.Act(a)
					}
				}
			}
			r.Step(tt)
		}
		r.Leave()
		f.Advance(c.until + 0.05) // the screen's left: nothing more steps it
		d := f.Drive
		if d.Ejects != c.ejects {
			t.Errorf("%s: %d ejects, want %d", c.name, d.Ejects, c.ejects)
		}
		if in := d.Status == cdsDiscOK; !c.busy && in != c.in {
			t.Errorf("%s: disc in %v, want %v", c.name, in, c.in)
		}
		if c.busy && (d.reading == nil || !d.reading.stopped) {
			t.Errorf("%s: the read wasn't stopped", c.name)
		}
		if !c.busy && f.DiscBusy("sr0") != "" {
			t.Errorf("%s: the drive still %s", c.name, f.DiscBusy("sr0"))
		}
	}
}
