package identify

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"

	"github.com/doesitomarchy/doesitomarchy/pkg/match"
	"github.com/doesitomarchy/doesitomarchy/pkg/report"

	"github.com/omaboot/boot-live/assets"
)

// Catalog is the catalog snapshot (GET /api/v1/snapshot) with a matcher over it, and the Macs'
// names (GET /api/v1/macs).
type Catalog struct {
	Snap    report.CatalogSnapshot
	Names   map[string]string     // identifier → name, e.g. "MacBook Air (13-inch, Mid 2012)"
	Release map[string]string     // configuration → its release's name, when the snapshot has them
	Panels  map[string]string     // configuration → its built-in display's resolution, e.g. "5120x2880"
	Date    string                // the day the catalog's content last changed (YYYY-MM-DD), "" in older snapshots
	Layouts map[string][]PortSide // configuration → its port map, when the snapshot has one
	// Wiring is configuration → connector → the stable paths accepted reports agree on (the
	// snapshot's "wiring"), when the snapshot has any.
	Wiring  map[string]map[string][]string
	Fetched bool // refreshed from the site during this run
	m       *match.Matcher
}

// Parse reads a snapshot and, optionally, the Macs list.
func Parse(snap, macs []byte) (*Catalog, error) {
	c := &Catalog{Names: map[string]string{}, Release: map[string]string{}, Panels: map[string]string{}, Layouts: map[string][]PortSide{},
		Wiring: map[string]map[string][]string{}}
	if err := json.Unmarshal(snap, &c.Snap); err != nil {
		return nil, fmt.Errorf("catalog snapshot: %w", err)
	}
	// release names (v0.14.1), panel resolutions (v0.14.3), the date and the port maps (v0.14.4)
	// arrived in the snapshot after v0.14.0: read them on their own, so an older snapshot still
	// parses
	var rel struct {
		Date  string `json:"date"`
		Match struct {
			Macs []struct {
				Releases []struct {
					Name    string `json:"name"`
					Configs []struct {
						ID     string     `json:"id"`
						Panel  string     `json:"panel"`
						Ports  []PortSide `json:"ports"`
						Wiring []struct {
							Connector string   `json:"connector"`
							Paths     []string `json:"paths"`
						} `json:"wiring"`
					} `json:"configs"`
				} `json:"releases"`
			} `json:"macs"`
		} `json:"match"`
	}
	if json.Unmarshal(snap, &rel) == nil {
		c.Date = rel.Date
		for _, m := range rel.Match.Macs {
			for _, r := range m.Releases {
				for _, cf := range r.Configs {
					if r.Name != "" {
						c.Release[cf.ID] = r.Name
					}
					if cf.Panel != "" {
						c.Panels[cf.ID] = cf.Panel
					}
					if len(cf.Ports) > 0 {
						c.Layouts[cf.ID] = cf.Ports
					}
					for _, w := range cf.Wiring {
						if c.Wiring[cf.ID] == nil {
							c.Wiring[cf.ID] = map[string][]string{}
						}
						c.Wiring[cf.ID][w.Connector] = w.Paths
					}
				}
			}
		}
	}
	if len(c.Snap.Match.Macs) == 0 {
		return nil, fmt.Errorf("catalog snapshot: no Macs in it")
	}
	if len(macs) > 0 {
		var list struct {
			Macs []struct{ Identifier, Name string } `json:"macs"`
		}
		if err := json.Unmarshal(macs, &list); err == nil {
			for _, m := range list.Macs {
				c.Names[m.Identifier] = m.Name
			}
		}
	}
	c.m = match.New(c.Snap.Match)
	return c, nil
}

// Embedded is the snapshot built into the binary.
func Embedded() (*Catalog, error) { return Parse(assets.Snapshot, assets.Macs) }

// Configs counts the snapshot's configurations.
func (c *Catalog) Configs() int {
	n := 0
	for _, m := range c.Snap.Match.Macs {
		for _, r := range m.Releases {
			n += len(r.Configs)
		}
	}
	return n
}

// Mac returns a Mac by identifier, or nil.
func (c *Catalog) Mac(identifier string) *match.SnapshotMac {
	for i := range c.Snap.Match.Macs {
		if strings.EqualFold(c.Snap.Match.Macs[i].Identifier, identifier) {
			return &c.Snap.Match.Macs[i]
		}
	}
	return nil
}

// Config returns a configuration by ID, or nil.
func (c *Catalog) Config(id string) *match.SnapshotConfig {
	for i := range c.Snap.Match.Macs {
		for j := range c.Snap.Match.Macs[i].Releases {
			r := &c.Snap.Match.Macs[i].Releases[j]
			for k := range r.Configs {
				if r.Configs[k].ID == id {
					return &r.Configs[k]
				}
			}
		}
	}
	return nil
}

// Panel is the built-in display's resolution for an identification, e.g. "5120x2880": its
// configuration's, or the one all the Mac's configurations share; "" when the snapshot doesn't
// say (no built-in display, or a snapshot older than v0.14.3).
func (c *Catalog) Panel(r Result) string {
	if p := c.Panels[r.Config]; p != "" {
		return p
	}
	shared := ""
	for i, id := range c.ConfigIDs(r.Identifier) {
		p := c.Panels[id]
		if p == "" || i > 0 && p != shared {
			return ""
		}
		shared = p
	}
	return shared
}

// PortSide is one side (a laptop's left or right) or face (a desktop's back, front or top) of a
// configuration's port map, from the site's drawing, in millimetres to scale: x runs left to
// right as you face that side, from its outline's left end, y down from its top.
type PortSide struct {
	Side   string  `json:"side"`   // left, right, back, front or top
	Length float64 `json:"length"` // along x (a laptop's depth, a desktop's width)
	Height float64 `json:"height"` // along y
	// Start is the end at x=0: back or front on a laptop's side; left on a face, as you face it
	// (from behind for the back).
	Start string `json:"start"`
	Ports []Port `json:"ports"` // in the site's connector order
	// Mirrored: an iMac's back as boot-live draws it (MirrorBack), seen reaching round from the
	// front: x runs left to right as the tester sees it from the front. Never in the snapshot.
	Mirrored bool `json:"-"`
}

// Port is one port on a side.
type Port struct {
	ID   string  `json:"id"`   // the connector, <side>-<n>: left-2
	Kind string  `json:"kind"` // usb-a, usb-c, thunderbolt, thunderbolt3, sd, magsafe2, headphone…
	Name string  `json:"name"` // as the drawing names it: USB 3, Thunderbolt 3 (USB-C)
	X    float64 `json:"x"`    // the centre of its opening, mm
	Y    float64 `json:"y"`
	// Tag is its short tag on the screens when it isn't read off its ID: a mirrored back's ports
	// are numbered from the viewer's left (MirrorBack). Never in the snapshot; reports keep ID.
	Tag string `json:"-"`
}

// AllInOne reports whether a Mac's identifier is an iMac's (iMac, iMac Pro): its ports are on
// its back, behind the screen the tester faces.
func AllInOne(identifier string) bool {
	f, _ := Model(identifier)
	return f == "iMac" || f == "iMacPro"
}

// OneSpeaker reports whether a Mac has a single built-in speaker (Macmini4,1, 0.8.5 dev:
// the speakers test's right channel unheard): the Mac mini and Mac Pro, and the first two
// MacBook Airs (2008); every other Intel Mac has a pair.
func OneSpeaker(identifier string) bool {
	f, _ := Model(identifier)
	return f == "Macmini" || f == "MacPro" || identifier == "MacBookAir1,1" || identifier == "MacBookAir2,1"
}

// MirrorBack is a port map with its back face as seen reaching round from the front, the way
// someone sitting at an iMac finds its ports (0.7.3 review): x mirrored, so the ports run left to
// right as the tester sees them, and each port tagged from the viewer's left (B1 leftmost). The
// ports keep their IDs (back-9 stays back-9, whatever its tag), so reports and the wiring don't
// change. The other faces, and the map passed in, are left as they are.
func MirrorBack(sides []PortSide) []PortSide {
	out := make([]PortSide, len(sides))
	for i, s := range sides {
		out[i] = s
		if s.Side != "back" || s.Mirrored || s.Length <= 0 {
			continue
		}
		ps := make([]Port, len(s.Ports))
		for j, p := range s.Ports {
			p.X = s.Length - p.X
			ps[j] = p
		}
		order := make([]int, len(ps))
		for j := range order {
			order[j] = j
		}
		sort.SliceStable(order, func(a, b int) bool {
			pa, pb := ps[order[a]], ps[order[b]]
			if pa.X != pb.X {
				return pa.X < pb.X
			}
			return pa.Y < pb.Y
		})
		for n, j := range order {
			ps[j].Tag = fmt.Sprintf("B%d", n+1)
		}
		out[i].Ports, out[i].Mirrored = ps, true
	}
	return out
}

// Ports is the port map for an identification: its configuration's, or the one all the Mac's
// configurations share (as with Panel); nil when the snapshot has none (older than v0.14.4, or
// no drawing for this Mac yet).
func (c *Catalog) Ports(r Result) []PortSide {
	if p := c.Layouts[r.Config]; len(p) > 0 {
		return p
	}
	var shared []PortSide
	for i, id := range c.ConfigIDs(r.Identifier) {
		p := c.Layouts[id]
		if len(p) == 0 || i > 0 && !sameLayout(p, shared) {
			return nil
		}
		shared = p
	}
	return shared
}

// Wired is the port wiring for an identification's configuration (never shared between
// configurations: their boards may differ); nil when none is known.
func (c *Catalog) Wired(r Result) map[string][]string {
	return c.Wiring[r.Config]
}

func sameLayout(a, b []PortSide) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		x, y := a[i], b[i]
		if x.Side != y.Side || x.Length != y.Length || x.Start != y.Start || len(x.Ports) != len(y.Ports) {
			return false
		}
		for j := range x.Ports {
			if x.Ports[j] != y.Ports[j] {
				return false
			}
		}
	}
	return true
}

// ConfigIDs lists a Mac's configurations in catalog order.
func (c *Catalog) ConfigIDs(identifier string) []string {
	var out []string
	if m := c.Mac(identifier); m != nil {
		for _, r := range m.Releases {
			for _, cfg := range r.Configs {
				out = append(out, cfg.ID)
			}
		}
	}
	return out
}

// Fix returns a registered fix by ID, or nil.
func (c *Catalog) Fix(id string) *report.BootFix {
	for i := range c.Snap.Fixes {
		if c.Snap.Fixes[i].ID == id {
			return &c.Snap.Fixes[i]
		}
	}
	return nil
}

// Refresh fetches a newer snapshot from a DoesItOmarchy site (base URL, e.g.
// http://localhost:8080), sending the current version as If-None-Match. It reports whether the
// snapshot changed.
func (c *Catalog) Refresh(ctx context.Context, client *http.Client, base string) (bool, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(base, "/")+"/api/v1/snapshot", nil)
	if err != nil {
		return false, err
	}
	if c.Snap.Version != "" {
		req.Header.Set("If-None-Match", `"`+c.Snap.Version+`"`)
	}
	res, err := client.Do(req)
	if err != nil {
		return false, err
	}
	defer res.Body.Close()
	switch res.StatusCode {
	case http.StatusNotModified:
		c.Fetched = true
		return false, nil
	case http.StatusOK:
	default:
		return false, fmt.Errorf("snapshot: %s", res.Status)
	}
	b, err := io.ReadAll(io.LimitReader(res.Body, 16<<20))
	if err != nil {
		return false, err
	}
	n, err := Parse(b, nil)
	if err != nil {
		return false, err
	}
	changed := !bytes.Equal([]byte(n.Snap.Version), []byte(c.Snap.Version))
	c.Snap, c.m, c.Fetched = n.Snap, n.m, true
	if len(n.Layouts) > 0 {
		c.Layouts = n.Layouts
	}
	c.Wiring = n.Wiring // the site's latest: what accepted reports agree on now
	return changed, nil
}

// Result is what identification found.
type Result struct {
	Identifier string // the catalog's identifier, or the product name when the catalog lacks it
	Name       string // the Mac's name, or ""
	Known      bool   // the catalog has this Mac
	By         string // product_name, board_id or devices
	Exact      bool   // exactly one configuration fits best
	Candidates []match.Candidate
	Config     string // the configuration: the exact match, or the one the person picked
	Chip       string // t1, t2 or ""
}

// Identify matches the hardware against the snapshot.
func (c *Catalog) Identify(h Hardware) Result {
	mr := c.m.Match(h.Probe())
	r := Result{Identifier: mr.Identifier, By: mr.By, Exact: mr.Exact, Candidates: mr.Candidates}
	if r.Identifier == "" {
		r.Identifier = strings.TrimSpace(h.ProductName)
	} else {
		r.Known = true
	}
	if m := c.Mac(r.Identifier); m != nil {
		r.Chip = m.SecurityChip
	}
	if r.Exact {
		r.Config = mr.Best()
	}
	r.Name = c.name(r)
	return r
}

// name is the Mac's name for the header: the matched configuration's release ("iMac (21.5-inch,
// Late 2009)"), or the release all candidates share. When the candidates span releases (or the
// snapshot has no release names), the identifier's name is only right for one of them, so a
// Mac with several releases is named by its family alone ("iMac").
func (c *Catalog) name(r Result) string {
	if n := c.Release[r.Config]; n != "" {
		return n
	}
	shared := ""
	for i, cd := range r.Candidates {
		n := c.Release[cd.Config]
		if n == "" || i > 0 && n != shared {
			shared = ""
			break
		}
		shared = n
	}
	if shared != "" {
		return shared
	}
	n := c.Names[r.Identifier]
	if m := c.Mac(r.Identifier); m != nil && len(m.Releases) > 1 {
		if fam, _, ok := strings.Cut(n, " ("); ok {
			return fam
		}
	}
	return n
}

// Best is the configuration --auto uses: the exact match, else the best-scored candidate.
func (r Result) Best() string {
	if r.Config != "" {
		return r.Config
	}
	if len(r.Candidates) > 0 {
		return r.Candidates[0].Config
	}
	return ""
}
