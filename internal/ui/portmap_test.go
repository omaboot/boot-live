package ui

import (
	"slices"
	"strconv"
	"strings"
	"testing"

	g "github.com/omaboot/boot-live/internal/grid"
	"github.com/omaboot/boot-live/internal/handson"
	"github.com/omaboot/boot-live/internal/identify"
)

// Every port map in the catalog is drawn the right way for every port test: a laptop's sides
// for a laptop, a desktop's faces for a desktop (0.7.2 review: the iMac10,1's back and right
// edge were drawn as a laptop's sides, and the USB test showed the right edge). The faces drawn
// are those with the test's ports, all of them, and every test port's tag is on the screen. An
// iMac's back is mirrored, as seen reaching round from the front (0.7.3 review): its ports keep
// their IDs, run left to right as B1, B2 … on the face, and its title says so; every other Mac's
// faces are drawn from behind as before.
func TestPortMapFaces(t *testing.T) {
	cat, err := identify.Embedded()
	if err != nil {
		t.Fatal(err)
	}
	tests := map[string][]string{ // each port test's kinds (ports.go, ports_sd.go, ports_tb.go, firewire.go)
		"ports.usb-a": {"usb-a"}, "ports.usb-c": {"usb-c", "thunderbolt3"}, "ports.sd-card": {"sd"},
		"ports.thunderbolt": {"thunderbolt", "thunderbolt3"}, "ports.firewire": {"firewire400", "firewire800"},
	}
	n, imacs := 0, 0
	for _, mac := range cat.Snap.Match.Macs {
		laptop, known := identify.LaptopFamily(mac.Identifier)
		for _, r := range mac.Releases {
			for _, cf := range r.Configs {
				sides := cat.Layouts[cf.ID]
				if len(sides) == 0 {
					continue
				}
				if identify.AllInOne(mac.Identifier) {
					mirrored := identify.MirrorBack(sides)
					checkMirrored(t, cf.ID, sides, mirrored)
					sides = mirrored
					imacs++
				}
				for test, kinds := range tests {
					m := &handson.PortMap{Sides: sides, State: map[string]handson.PortState{}, Note: map[string]string{}}
					var mine []identify.Port
					withPorts := map[string]bool{}
					for _, s := range sides {
						for _, p := range s.Ports {
							if slices.Contains(kinds, p.Kind) {
								m.State[p.ID] = handson.PortWait
								mine = append(mine, p)
								withPorts[s.Side] = true
							}
						}
					}
					if len(mine) == 0 {
						continue
					}
					n++
					where := cf.ID + " " + test
					if known && m.Laptop() != laptop {
						t.Errorf("%s: drawn as a laptop: %v, but %s is a laptop: %v", where, m.Laptop(), mac.Identifier, laptop)
						continue
					}
					if m.Laptop() {
						for _, s := range sides {
							if withPorts[s.Side] && s.Side != "left" && s.Side != "right" {
								t.Errorf("%s: test ports on the %s, which a laptop's map doesn't draw", where, s.Side)
							}
						}
					} else {
						for _, w := range []int{92, 132} { // the band's field at 120 and 160 columns
							var drawn []string
							for _, f := range facePlan(m, w, 11) {
								drawn = append(drawn, f.side.Side)
								if f.w < 20 || f.x+f.w > w || f.y+f.h > 11 {
									t.Errorf("%s at %d: the %s's box %d+%d wide, %d+%d tall doesn't fit", where, w, f.side.Side, f.x, f.w, f.y, f.h)
								}
							}
							for side := range withPorts {
								if !slices.Contains(drawn, side) {
									t.Errorf("%s at %d: the %s has test ports and isn't drawn (drawn: %v)", where, w, side, drawn)
								}
							}
							for _, side := range drawn {
								if !withPorts[side] {
									t.Errorf("%s at %d: the %s has no test ports and is drawn", where, w, side)
								}
							}
						}
					}
					gr := g.New(132, 14)
					drawPortMap(gr, m, 0, 0, 132, 14, 0)
					text := gr.Text()
					for _, p := range mine {
						if !strings.Contains(text, handson.PortTag(p)) {
							t.Errorf("%s: %s (%s) isn't on the map", where, p.ID, handson.PortTag(p))
						}
					}
				}
			}
		}
	}
	if imacs < 10 {
		t.Errorf("only %d iMac layouts mirrored", imacs)
	}
	if n < 100 {
		t.Errorf("only %d layouts × tests looked at: is the snapshot's port data there?", n)
	}
}

// checkMirrored: an iMac's back, mirrored, keeps every port's ID and kind, puts each at the
// face's length less its x, and tags its ports B1, B2 … from the viewer's left; its title is the
// mirrored view's. Other faces are untouched.
func checkMirrored(t *testing.T, cfg string, before, after []identify.PortSide) {
	t.Helper()
	for i, s := range after {
		b := before[i]
		if s.Side != "back" {
			if s.Mirrored || len(s.Ports) != len(b.Ports) || len(s.Ports) > 0 && s.Ports[0] != b.Ports[0] {
				t.Errorf("%s: the %s changed when the back was mirrored", cfg, s.Side)
			}
			continue
		}
		if !s.Mirrored || b.Mirrored {
			t.Errorf("%s: the back isn't mirrored, or the catalog's was changed", cfg)
		}
		if !strings.HasPrefix(faceTitle(s), "Back, reaching round from the front") {
			t.Errorf("%s: mirrored back titled %q", cfg, faceTitle(s))
		}
		for j, p := range s.Ports {
			q := b.Ports[j]
			if p.ID != q.ID || p.Kind != q.Kind || p.X != s.Length-q.X || q.Tag != "" {
				t.Errorf("%s: %s mirrored as %+v from %+v", cfg, q.ID, p, q)
			}
		}
		f := layoutFace(&handson.PortMap{Sides: after, State: map[string]handson.PortState{}}, s, 92)
		for k, p := range f.ports {
			if want := "B" + strconv.Itoa(k+1); handson.PortTag(p) != want {
				t.Errorf("%s: the %d. port from the viewer's left (%s) is tagged %s, want %s", cfg, k+1, p.ID, handson.PortTag(p), want)
			}
		}
	}
}
