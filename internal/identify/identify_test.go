package identify

import (
	"slices"
	"testing"

	"github.com/omaboot/boot-live/internal/sysroot"
)

// The three Macs the spikes ran on, read from their fixture trees (testdata/, built from the
// spikes' real probe data) and matched against the snapshot built into the binary.
func TestIdentifyFixtures(t *testing.T) {
	cat, err := Embedded()
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		dir, identifier, name, config, cpu string
		mem                                int
		exact                              bool
	}{
		{"macbookair5,2", "MacBookAir5,2", "MacBook Air (13-inch, Mid 2012)", "macbookair5-2-mid-2012-a", "Core i5-3427U", 3812, true},
		// the 21.5-inch iMac10,1 with the Radeon: its board ID tells it from the 27-inch, which has the same parts
		{"imac10,1", "iMac10,1", "iMac (21.5-inch, Late 2009)", "imac10-1-21-late-2009-b", "Core 2 Duo E7600", 11948, true},
		{"imac18,3", "iMac18,3", "", "imac18-3-5k-2017-a", "Core i5-7500", 31985, true},
	}
	for _, c := range cases {
		t.Run(c.dir, func(t *testing.T) {
			h := Read(sysroot.Root("../../testdata/" + c.dir))
			if got := ShortCPU(h.CPU); got != c.cpu {
				t.Errorf("CPU %q → %q, want %q", h.CPU, got, c.cpu)
			}
			if h.MemMiB != c.mem {
				t.Errorf("memory %d MiB, want %d", h.MemMiB, c.mem)
			}
			if h.EFIBits != 64 {
				t.Errorf("EFI %d-bit, want 64", h.EFIBits)
			}
			r := cat.Identify(h)
			if r.Identifier != c.identifier || !r.Known {
				t.Fatalf("identified %q (known %v), want %q", r.Identifier, r.Known, c.identifier)
			}
			if c.name != "" && r.Name != c.name {
				t.Errorf("name %q, want %q", r.Name, c.name)
			}
			if r.Exact != c.exact || r.Config != c.config {
				t.Errorf("config %q (exact %v), want %q; candidates %+v", r.Config, r.Exact, c.config, r.Candidates)
			}
			// sticks plugged into USB are not part of the Mac
			if p := h.Probe(); slices.Contains(p.USB, "0951:1643") {
				t.Errorf("the boot stick is in the probe's USB IDs: %v", p.USB)
			}
		})
	}
}

// A machine the catalog doesn't know (the image build's QEMU smoke test): no identifier, no
// configuration, but the product name is kept so a report can still be saved.
func TestIdentifyUnknown(t *testing.T) {
	cat, err := Embedded()
	if err != nil {
		t.Fatal(err)
	}
	h := Read(sysroot.Root("../../testdata/qemu"))
	r := cat.Identify(h)
	if r.Known || r.Config != "" || r.Best() != "" {
		t.Fatalf("QEMU identified as %+v", r)
	}
	if r.Identifier != "Standard PC (Q35 + ICH9, 2009)" {
		t.Errorf("identifier %q", r.Identifier)
	}
}

func TestIntegrated(t *testing.T) {
	for id, want := range map[string]bool{"8086:0166": true, "10de:0861": true, "10de:08a0": true, "10de:0a29": false, "1002:9488": false, "1002:67df": false} {
		d := PCIDev{Vendor: id[:4], Device: id[5:]}
		if Integrated(d) != want {
			t.Errorf("%s integrated = %v, want %v", id, !want, want)
		}
	}
}

// A snapshot's port maps: a configuration's own, the one its Mac's configurations share, and
// none in a snapshot without them.
func TestPorts(t *testing.T) {
	side := `{"side":"left","length":227,"height":17,"start":"back","ports":[{"id":"left-2","kind":"usb-a","name":"USB 3","x":42,"y":7.97}]}`
	snap := `{"match":{"macs":[{"identifier":"MacBookAir5,2","releases":[{"configs":[{"id":"a","ports":[` + side + `]},{"id":"b","ports":[` + side + `]}]}]},
		{"identifier":"iMac18,3","releases":[{"configs":[{"id":"c","ports":[` + side + `]},{"id":"d"}]}]}]}}`
	cat, err := Parse([]byte(snap), nil)
	if err != nil {
		t.Fatal(err)
	}
	if p := cat.Ports(Result{Identifier: "MacBookAir5,2"}); len(p) != 1 || p[0].Start != "back" || p[0].Ports[0].X != 42 {
		t.Errorf("shared: %+v", p)
	}
	if p := cat.Ports(Result{Identifier: "iMac18,3", Config: "c"}); len(p) != 1 {
		t.Errorf("own: %+v", p)
	}
	if p := cat.Ports(Result{Identifier: "iMac18,3"}); p != nil {
		t.Errorf("not shared: %+v", p)
	}
	emb, err := Embedded()
	if err != nil {
		t.Fatal(err)
	}
	if p := emb.Ports(Result{Identifier: "Nope1,1"}); p != nil {
		t.Errorf("unknown Mac: %+v", p)
	}
}

// Which Mac's view: the MacBook Air the spikes ran on matches one configuration, its family and
// year; a MacBookAir7,2 with the build-to-order Core i7 is the Early 2015 or the 2017 (the same
// board ID, CPU and GPU), so both fit and the search lands on both releases.
func TestView(t *testing.T) {
	cat, err := Embedded()
	if err != nil {
		t.Fatal(err)
	}
	h := Read(sysroot.Root("../../testdata/macbookair5,2"))
	v := cat.View(h, cat.Identify(h))
	if len(v.Hits) != 1 || v.Entries[v.Hits[0]].Release != "MacBook Air (13-inch, Mid 2012)" || Families[v.Family] != "MacBook Air" {
		t.Fatalf("hits %v, family %d", v.Hits, v.Family)
	}
	if w := v.When[v.Fits[0]]; w.Label != "Mid 2012" || w.Year != 2012.5 || v.From != 2008 || v.To != 2020 {
		t.Errorf("when %+v, family years %d–%d", w, v.From, v.To)
	}
	for _, p := range v.Parts {
		if c := p.Theirs[v.Fits[0]]; p.Key != "Model ID" && !c.Match {
			t.Errorf("%s: %q doesn't match %q", p.Key, p.Mine, c.Text)
		}
	}

	air := Hardware{ProductName: "MacBookAir7,2", BoardID: "Mac-937CB26E2E02BB01", CPU: "Intel(R) Core(TM) i7-5650U CPU @ 2.20GHz",
		PCI: []PCIDev{{Class: "030000", Vendor: "8086", Device: "1626", Driver: "i915", BootVGA: true}}}
	r := cat.Identify(air)
	v = cat.View(air, r)
	if r.Exact || len(v.Fits) != 2 || len(v.Hits) != 2 {
		t.Fatalf("exact %v, fits %v, hits %v", r.Exact, v.Fits, v.Hits)
	}
	if a, b := v.When[v.Fits[0]].Label, v.When[v.Fits[1]].Label; a != "Early 2015" || b != "2017" {
		t.Errorf("years %q, %q", a, b)
	}
}
