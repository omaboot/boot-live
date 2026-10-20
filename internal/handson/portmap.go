package handson

import (
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/omaboot/boot-live/internal/checks"
	g "github.com/omaboot/boot-live/internal/grid"
	"github.com/omaboot/boot-live/internal/i18n"
	"github.com/omaboot/boot-live/internal/identify"
)

// The port map: the Mac's ports as DoesItOmarchy's port map
// drawing places them (the catalog snapshot's "ports", per configuration), shown across the band on
// the port tests with each port in its state for the test on screen. A test asks for one port
// at a time, in drawing order (the "next" port, ringed in violet); whatever shows up is matched
// to the port asked for, and the evidence records the match ("right-2 = 3-2 / 4-2") and what the
// firmware says about the port's place, so reports teach the catalog each model's wiring. Once
// every port of the test's kind has a state, the test decides by itself.
//
// Without a map for this Mac (a snapshot older than v0.14.4, a Mac not drawn yet), or with no
// port of the test's kind on it, the tests keep their plain flow: each port in turn, then Done.

// portSayRows are the text rows of Boot's box under the band on the port tests: two hold his
// longest line there at 120 columns, and the row saved keeps the answers on a 120×40 console.
const portSayRows = 2

// PortState is a port's state in the test on screen.
type PortState string

const (
	PortOther PortState = ""     // not this test's port: dim
	PortWait  PortState = "wait" // still to do: grey
	PortNext  PortState = "next" // plug in here: bright violet, ringed
	PortSeen  PortState = "seen" // something showed up on it: green
	PortBoot  PortState = "boot" // the OmaBoot? stick's, or the tethering phone's: green (it works, the Mac booted from it, the phone shares its connection through it)
	PortPart  PortState = "part" // it only partly worked (a Thunderbolt device not let in): amber
	PortDead  PortState = "dead" // marked as not working: red
)

// Color is the state's colour: the port's symbol, its leader line, icon and note.
func (s PortState) Color() g.Color {
	switch s {
	case PortWait:
		return g.GRY
	case PortNext:
		return g.BVIO
	case PortSeen, PortBoot:
		return g.BGRN
	case PortPart:
		return g.AMB
	case PortDead:
		return g.BRED
	}
	return g.DIM
}

// PortMap is the map as a test's screen shows it; internal/ui draws it across the band.
type PortMap struct {
	Sides []identify.PortSide
	State map[string]PortState // by port ID; absent: not this test's
	Note  map[string]string    // a short note by a port's name ("✓ 5 Gb/s"); the screen adds "plug in here" to the next port, or its arrow to the next port's own note ("reading…" while its check is under way)
	// NoteW is the widest note any port can show in this test: the screen plans the map's
	// layout for it, so nothing moves as states change.
	NoteW int
	// Choices are the port IDs the answers name, in their order (which port holds the
	// OmaBoot? stick); the screen rings the one under the cursor as Picked.
	Choices []string
	Picked  string
	// Phone is the tethering phone's note when the test places one ("iPhone"), for the whole
	// test so the layout plans for it; PickPhone: the question up is which port the phone is in,
	// not the stick.
	Phone     string
	PickPhone bool
	// Call names a kind's ports for this test, in place of the drawing's name: the USB-C tests
	// call a Thunderbolt 3 port "USB-C" (guide.callUSBC).
	Call map[string]string
}

// Name is a port's name on this map: the test's name for its kind, else PortName's.
func (m *PortMap) Name(p identify.Port) string {
	if n := m.Call[p.Kind]; n != "" {
		return n
	}
	return PortName(p)
}

// Label is a port's name on this map with its tag: "USB-C (B7)".
func (m *PortMap) Label(p identify.Port) string { return m.Name(p) + " (" + PortTag(p) + ")" }

// Laptop reports whether the map is a laptop's sides (left and right), not a desktop's faces: it
// has a left or right side and no back, front or top. An iMac's drawing has its back and the SD
// slot on its right edge (iMac10,1): a desktop, its faces drawn one by one (0.7.2 review: drawn
// as a laptop, the USB test showed the right edge and not the back). Every layout in the
// catalog follows the rule (TestPortMapFaces in internal/ui).
func (m *PortMap) Laptop() bool {
	sides := false
	for _, s := range m.Sides {
		switch s.Side {
		case "left", "right":
			sides = true
		case "back", "front", "top":
			return false
		}
	}
	return sides
}

// PortName is a port's name on the screen: the drawing's, without the bracketed detail
// ("Thunderbolt 3 (USB-C)" → "Thunderbolt 3").
func PortName(p identify.Port) string {
	if i := strings.Index(p.Name, " ("); i > 0 && strings.HasSuffix(p.Name, ")") {
		return p.Name[:i]
	}
	return p.Name
}

// PortTag is a port's short tag on the map and in the answers, from its connector ID: left-2 →
// "L2", back-3 → "B3" (in brackets after the name, so a question can name one port exactly). An
// iMac's mirrored back has its own tags, numbered from the viewer's left
// (identify.MirrorBack): only the screens show tags, the report keeps the IDs.
func PortTag(p identify.Port) string {
	if p.Tag != "" {
		return p.Tag
	}
	side, n, ok := strings.Cut(p.ID, "-")
	if !ok || side == "" {
		return p.ID
	}
	return strings.ToUpper(side[:1]) + n
}

// PortLabel is a port's name with its tag: "USB 3 (L2)".
func PortLabel(p identify.Port) string { return PortName(p) + " (" + PortTag(p) + ")" }

// width is a note's width in cells.
func width(s string) int { return len([]rune(s)) }

// sideWord is a side as What I see's lines name it: "left side", "back".
func sideWord(side string) string {
	switch side {
	case "left":
		return i18n.T("left side")
	case "right":
		return i18n.T("right side")
	case "back":
		return i18n.T("back")
	case "front":
		return i18n.T("front")
	case "top":
		return i18n.T("top")
	}
	return side
}

// sideRank puts a laptop's left side before its right, and a desktop's back first.
var sideRank = map[string]int{"left": 0, "right": 1, "back": 2, "front": 3, "top": 4}

// ── the guided order ──────────────────────────────────────────────────────────

// guide walks the person through a test's ports one at a time, in drawing order (the left side
// before the right, each side left to right as you face it; an iMac's mirrored back left to right
// as seen from the front, B1 first), and keeps each port's state,
// note and match.
type guide struct {
	sides  []identify.PortSide
	ports  []identify.Port      // the test's ports, in drawing order
	side   map[string]string    // port ID → its side
	state  map[string]PortState // ports done: seen, boot, part or dead
	note   map[string]string
	match  map[string]string            // port ID → what the machine saw on it, for the evidence
	caught map[string]checks.PortAnswer // port ID → its paths and how they were matched, for the wiring
	noteW  int
	at     float64           // when a port last got its state
	call   map[string]string // PortMap.Call
}

// newGuide is the guided order over the ports of the given kinds, or nil when the Mac has no
// port map or no port of those kinds on it. noteW is the widest note the test can give a port.
func newGuide(e *Env, noteW int, kinds ...string) *guide {
	sides := e.Layout()
	want := map[string]bool{}
	for _, k := range kinds {
		want[k] = true
	}
	gd := &guide{sides: sides, side: map[string]string{}, state: map[string]PortState{}, note: map[string]string{},
		match: map[string]string{}, caught: map[string]checks.PortAnswer{}, noteW: noteW}
	order := append([]identify.PortSide(nil), sides...)
	sort.SliceStable(order, func(i, j int) bool { return rank(order[i].Side) < rank(order[j].Side) })
	for _, s := range order {
		var ps []identify.Port
		for _, p := range s.Ports {
			if want[p.Kind] {
				ps = append(ps, p)
				gd.side[p.ID] = s.Side
			}
		}
		// a face with ports in several rows (a Mac mini's, a Mac Pro's) goes by x, then y
		sort.SliceStable(ps, func(i, j int) bool {
			if ps[i].X != ps[j].X {
				return ps[i].X < ps[j].X
			}
			return ps[i].Y < ps[j].Y
		})
		gd.ports = append(gd.ports, ps...)
	}
	if len(gd.ports) == 0 {
		return nil
	}
	return gd
}

func rank(side string) int {
	if r, ok := sideRank[side]; ok {
		return r
	}
	return len(sideRank)
}

// next is the port to plug into now, or nil when every port has its state.
func (gd *guide) next() *identify.Port {
	for i := range gd.ports {
		if _, done := gd.state[gd.ports[i].ID]; !done {
			return &gd.ports[i]
		}
	}
	return nil
}

func (gd *guide) port(id string) *identify.Port {
	for i := range gd.ports {
		if gd.ports[i].ID == id {
			return &gd.ports[i]
		}
	}
	return nil
}

// set gives a port its state and note at the run's time t.
func (gd *guide) set(id string, st PortState, note string, t float64) {
	gd.state[id], gd.note[id], gd.at = st, note, t
}

// done reports whether every port has its state.
func (gd *guide) done() bool { return gd.next() == nil }

// count is how many ports are in state st.
func (gd *guide) count(st ...PortState) int {
	n := 0
	for _, p := range gd.ports {
		for _, s := range st {
			if gd.state[p.ID] == s {
				n++
			}
		}
	}
	return n
}

// view is the map for the screen; asking: the next port isn't asked for yet (a question first,
// its answers the ports without a state).
func (gd *guide) view(asking bool) *PortMap {
	m := &PortMap{Sides: gd.sides, State: map[string]PortState{}, Note: map[string]string{}, NoteW: gd.noteW, Call: gd.call}
	nx := gd.next()
	for _, p := range gd.ports {
		st, ok := gd.state[p.ID]
		if asking && !ok {
			m.Choices = append(m.Choices, p.ID)
		}
		switch {
		case ok:
			m.State[p.ID], m.Note[p.ID] = st, gd.note[p.ID]
		case !asking && nx != nil && p.ID == nx.ID:
			m.State[p.ID] = PortNext
		default:
			m.State[p.ID] = PortWait
		}
	}
	return m
}

// label is a port as What I see's lines and the answers name it: "USB 3 (L2)".
func (gd *guide) label(p identify.Port) string { return (&PortMap{Call: gd.call}).Label(p) }

// callUSBC has the screens name the Thunderbolt 3 ports "USB-C", by the connector a test that
// uses them as USB-C ports (USB, a monitor) needs: on a crowded iMac back the map shortened them
// to "TB 3", and the USB-C test read as asking for something else (0.8.8 dev runs). The
// evidence keeps the drawing's names. A nil guide stays nil.
func (gd *guide) callUSBC() *guide {
	if gd != nil {
		gd.call = map[string]string{"thunderbolt3": i18n.T("USB-C")}
	}
	return gd
}

// line is What I see's line for a port: "left side → [icon] USB 3 · text".
func (gd *guide) line(p identify.Port, text string) Line {
	st := gd.state[p.ID]
	if st == "" {
		st = PortWait
	}
	return Line{Side: sideWord(gd.side[p.ID]), Kind: p.Kind, Color: st.Color(), Text: gd.label(p) + " · " + text}
}

// choice names a port as an answer: "Left side, USB 3", "Back, USB 3 (port 2)".
func (gd *guide) choice(p identify.Port) string {
	w := []rune(sideWord(gd.side[p.ID]))
	if len(w) > 0 {
		w[0] = []rune(strings.ToUpper(string(w[0])))[0]
	}
	return i18n.T("%[1]s, %[2]s", string(w), gd.label(p))
}

// waiting is the spinner's line while a port is asked for.
func (gd *guide) waiting() Line {
	return Wait(i18n.T("%[1]d of %[2]d done. waiting for the port in the ring", len(gd.ports)-len(gd.pending()), len(gd.ports)))
}

func (gd *guide) pending() []identify.Port {
	var out []identify.Port
	for _, p := range gd.ports {
		if _, ok := gd.state[p.ID]; !ok {
			out = append(out, p)
		}
	}
	return out
}

// evidence is the map's part of the report: each port of the test with what happened on it.
func (gd *guide) evidence() string {
	var parts []string
	for _, p := range gd.ports {
		s := fmt.Sprintf("%s %s", p.ID, p.Name)
		if m := gd.match[p.ID]; m != "" {
			s += " = " + m
		} else {
			switch gd.state[p.ID] {
			case PortDead:
				s += ": marked as not working by the tester"
			case "":
				s += ": not tried"
			}
		}
		parts = append(parts, s)
	}
	return "port map, in the order asked: " + strings.Join(parts, "; ")
}

// wiredTo is the connector the wiring puts a port's stable paths on, among the guide's ports, or
// "" when the wiring doesn't know them (or puts them on two connectors).
func (gd *guide) wiredTo(e *Env, paths []string) string {
	wiring := e.wiring()
	if len(wiring) == 0 {
		return ""
	}
	found := ""
	for _, p := range gd.ports {
		for _, w := range wiring[p.ID] {
			for _, k := range paths {
				if k == w && found != p.ID {
					if found != "" {
						return ""
					}
					found = p.ID
				}
			}
		}
	}
	return found
}

// catch keeps what a port's match found for DoesItOmarchy.com's wiring: its paths (none: nothing
// to keep) and how it was matched (checks.PortAnswer).
func (gd *guide) catch(id, how, said string, paths ...string) {
	var ps []string
	for _, p := range paths {
		if p != "" && !slices.Contains(ps, p) {
			ps = append(ps, p)
		}
	}
	if len(ps) > 0 {
		gd.caught[id] = checks.PortAnswer{Connector: id, Paths: ps, How: how, WiringSaid: said}
	}
}

// answers are the ports caught, in drawing order.
func (gd *guide) answers() []checks.PortAnswer {
	var out []checks.PortAnswer
	for _, p := range gd.ports {
		if a, ok := gd.caught[p.ID]; ok {
			out = append(out, a)
		}
	}
	return out
}

// PortAnswers are a mapped port test's answers (porter); nil without a map.
func (gd *guide) PortAnswers() []checks.PortAnswer {
	if gd == nil {
		return nil
	}
	return gd.answers()
}

// index is a port's place among the pending ports (the which-port answers), or -1.
func (gd *guide) index(id string) int {
	for i, p := range gd.pending() {
		if p.ID == id {
			return i
		}
	}
	return -1
}

// ── firmware ──────────────────────────────────────────────────────────────────

// firmwarePlace is what the firmware's ACPI _PLD says about a hub port's place in the case, as
// Linux (6.0 and later) shows it in the port's physical_location (Documentation/ABI/testing/
// sysfs-devices-physical_location): "panel=left, vertical=upper, horizontal=left", or "" when
// the kernel doesn't say. Whether Macs fill it in is what the evidence is for.
func firmwarePlace(io IO, portDir string) string {
	d := portDir + "/physical_location/"
	panel := io.Read(d + "panel")
	if panel == "" {
		return ""
	}
	s := "panel=" + panel
	if v := io.Read(d + "vertical_position"); v != "" {
		s += ", vertical=" + v
	}
	if h := io.Read(d + "horizontal_position"); h != "" {
		s += ", horizontal=" + h
	}
	for _, k := range []string{"dock", "lid"} {
		if io.Read(d+k) == "yes" {
			s += ", " + k + "=yes"
		}
	}
	return s
}

// usbFirmwarePlace is firmwarePlace for the physical port a USB device is on: its hub port's,
// else its USB 2 / USB 3 peer's.
func usbFirmwarePlace(io IO, dev string) string {
	if s := firmwarePlace(io, usbPortDir(dev)); s != "" {
		return s
	}
	if peer := usbPeer(io, dev); peer != "" {
		return firmwarePlace(io, usbPortDir(peer))
	}
	return ""
}

// ── the demo's Macs ───────────────────────────────────────────────────────────

// DemoAirPorts is the MacBook Air (13-inch, Mid 2012) [MacBookAir5,2]'s port map, as the
// catalog snapshot has it (macbookair5-2-mid-2012-a): the demo's Mac.
func DemoAirPorts() []identify.PortSide {
	return []identify.PortSide{
		{Side: "left", Length: 227, Height: 17, Start: "back", Ports: []identify.Port{
			{ID: "left-1", Kind: "magsafe2", Name: "MagSafe 2", X: 23.8, Y: 7.67},
			{ID: "left-2", Kind: "usb-a", Name: "USB 3", X: 42, Y: 7.97},
			{ID: "left-3", Kind: "headphone", Name: "Headphone", X: 60.2, Y: 8.27}}},
		{Side: "right", Length: 227, Height: 17, Start: "front", Ports: []identify.Port{
			{ID: "right-1", Kind: "sd", Name: "SD card", X: 170.3, Y: 8.21},
			{ID: "right-2", Kind: "usb-a", Name: "USB 3", X: 194.55, Y: 7.81},
			{ID: "right-3", Kind: "thunderbolt", Name: "Thunderbolt", X: 210, Y: 7.56}}},
	}
}

// DemoIMacPorts is the iMac (Retina 5K, 27-inch, 2017) [iMac18,3]'s port map
// (imac18-3-5k-2017-a), for the screens of a desktop's back.
func DemoIMacPorts() []identify.PortSide {
	back := identify.PortSide{Side: "back", Length: 650, Height: 72, Start: "left"}
	for i, p := range []struct {
		kind, name string
		x          float64
	}{{"headphone", "Headphone", 72.7}, {"sd", "SD card", 95.15}, {"usb-a", "USB 3", 118.2}, {"usb-a", "USB 3", 131.5},
		{"usb-a", "USB 3", 144.85}, {"usb-a", "USB 3", 158.15}, {"thunderbolt3", "Thunderbolt 3 (USB-C)", 170.3},
		{"thunderbolt3", "Thunderbolt 3 (USB-C)", 181.3}, {"ethernet", "Gigabit Ethernet", 196.5}} {
		back.Ports = append(back.Ports, identify.Port{ID: fmt.Sprintf("back-%d", i+1), Kind: p.kind, Name: p.name, X: p.x, Y: 24})
	}
	return []identify.PortSide{back}
}

// DemoIMac10Ports is the iMac (21.5-inch, Late 2009) [iMac10,1]'s port map (imac10-1-21-late-2009-a):
// its back, and the SD slot on its right edge, for the screens of a desktop with a side slot
// (0.7.2 review) and its FireWire 800 port.
func DemoIMac10Ports() []identify.PortSide {
	back := identify.PortSide{Side: "back", Length: 546, Height: 67, Start: "left"}
	for i, p := range []struct {
		kind, name string
		x          float64
	}{{"headphone", "Headphone + optical out", 58.7}, {"line-in", "Line in + optical in", 69.9}, {"usb-a", "USB 2", 81.7}, {"usb-a", "USB 2", 94},
		{"usb-a", "USB 2", 106.3}, {"usb-a", "USB 2", 118.6}, {"firewire800", "FireWire 800", 133.5}, {"mini-displayport", "Mini DisplayPort", 147.95},
		{"ethernet", "Gigabit Ethernet", 162.8}} {
		back.Ports = append(back.Ports, identify.Port{ID: fmt.Sprintf("back-%d", i+1), Kind: p.kind, Name: p.name, X: p.x, Y: 25})
	}
	right := identify.PortSide{Side: "right", Length: 30, Height: 100.6, Start: "front",
		Ports: []identify.Port{{ID: "right-1", Kind: "sd", Name: "SD card", X: 15, Y: 45.6}}}
	return []identify.PortSide{back, right}
}
