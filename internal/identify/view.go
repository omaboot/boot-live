package identify

import (
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/doesitomarchy/doesitomarchy/pkg/match"

	"github.com/omaboot/boot-live/internal/i18n"
)

// View is what Which Mac draws the match from: every release in the
// catalog for the search, the Mac's family and years for the line-up and the timeline, and what
// the Mac reported next to what each of its configurations lists.
type View struct {
	Entries []Entry // every release in the snapshot, in catalog order
	Hits    []int   // the entries that fit (several when the match isn't exact)
	Board   string  // the board ID the search looks for ("" when the Mac reports none)
	Family  int     // index into Families, -1 when not one of them
	// From and To are the family's first and last year in the catalog (0 when unknown).
	From, To int
	When     map[string]When // configuration → its release's season and year, when its name says
	Fits     []string        // the configurations that fit best: one when the match is exact
	Parts    []PartRow       // board ID, CPU, GPUs, Wi-Fi, model identifier
}

// Entry is one release in the catalog.
type Entry struct{ Board, Identifier, Release string }

// When is a release's season and year: "Mid 2012" at 2012.5.
type When struct {
	Label string
	Year  float64
}

// PartRow is one row of the match table: what this Mac reported, and what each configuration lists.
type PartRow struct {
	Key    string          // Board ID, CPU, GPU, Wi-Fi or Model ID (English: the screen translates it)
	Mine   string          // what the Mac reported ("" when it reported nothing of the kind: not seen)
	Theirs map[string]Cell // configuration → what it lists; empty for the model identifier
}

// Cell is what a configuration lists for a row, and whether the Mac's report matches it.
type Cell struct {
	Text  string
	Match bool
}

// Families are the line-up's Intel Mac families, in its order.
var Families = []string{i18n.M("MacBook"), i18n.M("MacBook Air"), i18n.M("MacBook Pro"), i18n.M("iMac"), i18n.M("Mac mini"), i18n.M("Mac Pro")}

// FamilyOf is a model identifier's family (an index into Families), or -1.
func FamilyOf(identifier string) int {
	for _, p := range []struct {
		prefix string
		i      int
	}{{"MacBookAir", 1}, {"MacBookPro", 2}, {"MacBook", 0}, {"iMac", 3}, {"Macmini", 4}, {"MacPro", 5}} {
		if strings.HasPrefix(identifier, p.prefix) {
			return p.i
		}
	}
	return -1
}

var whenRe = regexp.MustCompile(`(?:(Early|Mid|Late) )?((?:19|20)\d\d)\)\s*$`)

// WhenOf reads a release name's season and year: "MacBook Air (13-inch, Mid 2012)" → Mid 2012,
// 2012.5; a name without a year gives ok = false.
func WhenOf(release string) (When, bool) {
	m := whenRe.FindStringSubmatch(release)
	if m == nil {
		return When{}, false
	}
	y, _ := strconv.Atoi(m[2])
	off := map[string]float64{"Early": 0.2, "Mid": 0.5, "Late": 0.8, "": 0.5}[m[1]]
	return When{Label: strings.TrimSpace(m[1] + " " + m[2]), Year: float64(y) + off}, true
}

// View builds Which Mac's view of an identification.
func (c *Catalog) View(h Hardware, r Result) View {
	v := View{Board: h.BoardID, Family: FamilyOf(r.Identifier), When: map[string]When{}}
	if v.Family < 0 {
		v.Family = FamilyOf(strings.TrimSpace(h.ProductName))
	}
	fits := map[string]bool{}
	switch {
	case r.Config != "":
		fits[r.Config] = true
	case len(r.Candidates) > 0:
		for _, cd := range r.Candidates {
			if cd.Score == r.Candidates[0].Score {
				fits[cd.Config] = true
			}
		}
	}
	for _, m := range c.Snap.Match.Macs {
		fam := FamilyOf(m.Identifier)
		for _, rel := range m.Releases {
			board := ""
			if len(rel.BoardIDs) > 0 {
				board = rel.BoardIDs[0]
			} else if len(m.BoardIDs) > 0 {
				board = m.BoardIDs[0]
			}
			hit := false
			for _, cf := range rel.Configs {
				if w, ok := WhenOf(rel.Name); ok {
					v.When[cf.ID] = w
				}
				if fits[cf.ID] && strings.EqualFold(m.Identifier, r.Identifier) {
					hit = true
					v.Fits = append(v.Fits, cf.ID)
				}
			}
			if hit {
				v.Hits = append(v.Hits, len(v.Entries))
			}
			v.Entries = append(v.Entries, Entry{Board: board, Identifier: m.Identifier, Release: rel.Name})
			if w, ok := WhenOf(rel.Name); ok && fam >= 0 && fam == v.Family {
				y := int(w.Year)
				if v.From == 0 || y < v.From {
					v.From = y
				}
				v.To = max(v.To, y)
			}
		}
	}
	mac := c.Mac(r.Identifier)
	if mac == nil {
		return v
	}
	v.Parts = c.parts(h, r, mac)
	return v
}

// parts are the match table's rows for a Mac in the catalog: one cell per configuration of it.
func (c *Catalog) parts(h Hardware, r Result, mac *match.SnapshotMac) []PartRow {
	cand := map[string]match.Candidate{}
	for _, cd := range r.Candidates {
		cand[cd.Config] = cd
	}
	have := map[string]bool{}
	for _, d := range h.PCI {
		have["pci:"+strings.ToLower(d.ID())] = true
	}
	for _, u := range h.USB {
		have["usb:"+strings.ToLower(u.ID())] = true
	}
	board := PartRow{Key: i18n.M("Board ID"), Mine: h.BoardID, Theirs: map[string]Cell{}}
	cpu := PartRow{Key: i18n.M("CPU"), Mine: TidyCPU(h.CPU), Theirs: map[string]Cell{}}
	wifi := PartRow{Key: i18n.M("Wi-Fi"), Theirs: map[string]Cell{}}
	var gpus []PartRow
	var gpuIDs []string
	for _, d := range h.GPUs() {
		gpuIDs = append(gpuIDs, "pci:"+strings.ToLower(d.ID()))
		mine := d.ID()
		if d.Driver != "" {
			mine += " (" + d.Driver + ")"
		}
		gpus = append(gpus, PartRow{Key: i18n.M("GPU"), Mine: mine, Theirs: map[string]Cell{}})
	}
	anyWifi := false
	for _, rel := range mac.Releases {
		boards := rel.BoardIDs // a release tied to its own board IDs (the 21.5- and 27-inch iMac10,1)
		if len(boards) == 0 {
			boards = mac.BoardIDs
		}
		for _, cf := range rel.Configs {
			// the board: tied to this release, else listed for this Mac
			cell := Cell{}
			for _, b := range boards {
				if strings.EqualFold(b, h.BoardID) {
					cell = Cell{Text: b, Match: true}
					break
				}
			}
			if cell.Text == "" && len(boards) > 0 {
				cell.Text = boards[0]
			}
			board.Theirs[cf.ID] = cell
			// the CPU: the model the Mac's CPU name contains, else all of them
			cell = Cell{Text: strings.Join(cf.CPUs, " or ")}
			for _, id := range cand[cf.ID].Matched {
				if tok, ok := strings.CutPrefix(id, "cpu:"); ok {
					for _, m := range cf.CPUs {
						if strings.Contains(strings.ToLower(m), tok) {
							cell = Cell{Text: m, Match: true}
						}
					}
				}
			}
			cpu.Theirs[cf.ID] = cell
			// GPUs and Wi-Fi: the part with the Mac's device ID, else the config's own
			var cgpu, cwifi []string
			for _, p := range cf.Parts {
				switch p.Kind {
				case "gpu":
					cgpu = append(cgpu, p.IDs...)
				case "wifi":
					cwifi = append(cwifi, p.IDs...)
				}
			}
			for i := range gpus {
				cell := Cell{Text: bare(other(cgpu, len(gpus), i))}
				for _, g := range cgpu {
					if g == gpuIDs[i] {
						cell = Cell{Text: bare(g), Match: true}
					}
				}
				gpus[i].Theirs[cf.ID] = cell
			}
			if len(cwifi) > 0 {
				anyWifi = true
				cell := Cell{Text: bare(cwifi[0])}
				for _, w := range cwifi {
					if have[w] {
						cell = Cell{Text: bare(w), Match: true}
						wifi.Mine = bare(w)
					}
				}
				wifi.Theirs[cf.ID] = cell
			}
		}
	}
	rows := []PartRow{board, cpu}
	rows = append(rows, gpus...)
	if anyWifi {
		rows = append(rows, wifi)
	}
	return append(rows, PartRow{Key: i18n.M("Model ID"), Mine: h.ProductName})
}

// other is the configuration's GPU to show against the Mac's i-th GPU when none matches: the
// one at the same place in its list, else its first.
func other(cgpu []string, n, i int) string {
	switch {
	case len(cgpu) == 0:
		return ""
	case i < len(cgpu) && len(cgpu) == n:
		return cgpu[i]
	}
	return cgpu[0]
}

// bare drops a hardware ID's bus: "pci:8086:0166" → "8086:0166".
func bare(id string) string {
	if _, rest, ok := strings.Cut(id, ":"); ok && strings.Count(id, ":") == 2 {
		return rest
	}
	return id
}

// TidyCPU is a CPU's name without the trademark marks: "Intel(R) Core(TM) i5-3427U CPU @
// 1.80GHz" → "Intel Core i5-3427U @ 1.80GHz".
func TidyCPU(name string) string {
	s := strings.NewReplacer("(R)", "", "(TM)", "", "(tm)", "", " CPU", "").Replace(name)
	return strings.TrimSpace(spaces.ReplaceAllString(s, " "))
}

// Labels name configurations for Which Mac's lists by their release ("MacBook Air (13-inch,
// Early 2015)"). Configurations in ids that share a release add the part of
// their IDs that differs ("iMac (21.5-inch, Late 2009)  ·  a"); one without a release keeps its ID.
func (c *Catalog) Labels(ids []string) []string {
	group := map[string][]string{}
	for _, id := range ids {
		group[c.Release[id]] = append(group[c.Release[id]], id)
	}
	out := make([]string, len(ids))
	for i, id := range ids {
		rel := c.Release[id]
		switch g := group[rel]; {
		case rel == "":
			out[i] = id
		case len(g) == 1:
			out[i] = rel
		default:
			out[i] = rel + "  ·  " + Suffixes(g)[slices.Index(g, id)]
		}
	}
	return out
}

// Suffixes are what tells configuration IDs apart: each without the dash-ended prefix they all
// share ("imac10-1-21-late-2009-a", "…-b" → "a", "b").
func Suffixes(ids []string) []string {
	p := ""
	if len(ids) > 1 {
		p = ids[0]
		for _, id := range ids[1:] {
			for !strings.HasPrefix(id, p) {
				p = p[:len(p)-1]
			}
		}
		p = p[:strings.LastIndex(p, "-")+1]
	}
	out := make([]string, len(ids))
	for i, id := range ids {
		out[i] = strings.TrimPrefix(id, p)
	}
	return out
}
