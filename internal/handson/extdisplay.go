package handson

import (
	"errors"
	"fmt"
	"math/rand/v2"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/omaboot/boot-live/internal/boot"
	"github.com/omaboot/boot-live/internal/checks"
	g "github.com/omaboot/boot-live/internal/grid"
	"github.com/omaboot/boot-live/internal/i18n"
	"github.com/omaboot/boot-live/internal/identify"
)

// graphics.external-display, "External display output: each built-in video output drives an
// external display". The person plugs a monitor into a video port; the machine sees the DRM
// connector come up (connected, with an EDID and modes), then puts a test card on that output
// itself, by KMS (extdisplay_kms.go): a dark card with a white border and a big three-digit
// code, on a CRTC of its own, never the internal panel's. The person types the code the monitor
// shows (method challenge; proof.go's typed codes): the code is on the monitor only, never on
// the Mac's own screen (owner, 2026-10-13). A wrong code gets one more try with a new code;
// unplugging and plugging back in doesn't reset that count. The console mirroring itself to the
// new output (fbcon) proves nothing on its own any more: a yes to "do you see this screen?"
// can't be told from a guess (owner, 0.5.0 "no false passes"). The connector's name goes into
// the evidence, for the port that was tested.
//
//   - supported: a connector came up connected with an EDID and modes, the card went up on it,
//     and the tester typed its code (challenge);
//   - partial: the code typed on a connector that gave no EDID (the kernel fell back to
//     default modes; challenge); or detected (connected, modes listed) but the tester sees no
//     picture, or a wrong one (observed): the console may not light it where Hyprland would;
//   - failed: the tester plugged a monitor in and no connector came up ("Nothing shows up",
//     method observed);
//   - not_tested (uncertain): a wrong code twice; or the card couldn't be put on the output
//     (no DRM master, no free CRTC, an ioctl refused) and the tester says they see the mirrored
//     console: a yes alone isn't proof. "It stays dark" and "Something's wrong" still count
//     then (partial, observed);
//   - not_tested (other): a fixture tree (--sysroot) can't show a card;
//   - not_tested (no-equipment): skipped, no monitor or cable at hand.
//
// Whatever way the screen is left (or once it has decided), the card comes down and the output
// goes back to how it was (Card.Close).

func init() {
	Register(Spec{ID: "graphics.external-display", Group: Plug, Order: 20, Title: i18n.M("External display"),
		Brief: i18n.M("plug in a monitor and read the code on it"), Gear: i18n.M("a monitor and cable"), Expect: 60 * time.Second,
		New: func(e *Env) Run { return &extDisplay{gd: videoGuide(e)} }, Demo: func(f *Fake) {
			d := "/sys/class/drm/card1-DP-1/"
			f.At(4, func(f *Fake) {
				f.Set(d+"status", "connected")
				f.Set(d+"enabled", "enabled")
				f.Set(d+"modes", "1920x1200\n1920x1080\n1600x1200\n1280x1024\n1024x768")
				f.Set(d+"edid", DemoEDID("DELL U2412M"))
			})
		}})
}

// internalPanel: the built-in panel's connector kinds.
func internalPanel(name string) bool {
	return strings.HasPrefix(name, "eDP") || strings.HasPrefix(name, "LVDS") || strings.HasPrefix(name, "DSI")
}

// connector is one external DRM connector as read now.
type connector struct {
	dir          string // sysfs's name for it: card1-DP-1
	name, status string
	edid         []byte
	modes        []string
}

func externalConnectors(io IO) []connector {
	var out []connector
	for _, d := range io.Glob("/sys/class/drm/card*-*") {
		_, name, ok := strings.Cut(path.Base(d), "-")
		if !ok || internalPanel(name) || strings.HasPrefix(name, "Writeback") {
			continue
		}
		c := connector{dir: path.Base(d), name: name, status: io.Read(d + "/status"), edid: io.Bytes(d + "/edid")}
		for _, m := range strings.Split(io.Read(d+"/modes"), "\n") {
			if m = strings.TrimSpace(m); m != "" {
				c.modes = append(c.modes, m)
			}
		}
		out = append(out, c)
	}
	return out
}

// portKind is the kind of port behind a connector, as a Mac has them.
func portKind(name string) string {
	switch {
	case strings.HasPrefix(name, "DP"):
		return "Mini DisplayPort / Thunderbolt / USB-C"
	case strings.HasPrefix(name, "HDMI"):
		return "HDMI"
	case strings.HasPrefix(name, "DVI"):
		return "DVI"
	case strings.HasPrefix(name, "VGA"):
		return "VGA"
	}
	return name
}

// EDIDName is the monitor's name from an EDID's display descriptor (tag 0xFC), or its
// manufacturer's three letters, or "".
func EDIDName(b []byte) string {
	if len(b) < 128 {
		return ""
	}
	for off := 54; off+18 <= 126; off += 18 {
		d := b[off : off+18]
		if d[0] == 0 && d[1] == 0 && d[3] == 0xFC {
			s := string(d[5:])
			if i := strings.IndexByte(s, '\n'); i >= 0 {
				s = s[:i]
			}
			if s = strings.TrimSpace(s); s != "" {
				return s
			}
		}
	}
	m := uint16(b[8])<<8 | uint16(b[9])
	return string([]byte{byte('@' + m>>10&31), byte('@' + m>>5&31), byte('@' + m&31)})
}

// DemoEDID is a minimal 128-byte EDID naming a monitor, for the demo and tests.
func DemoEDID(name string) string {
	b := make([]byte, 128)
	copy(b, []byte{0, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0})
	b[8], b[9] = 0x10, 0xac // DEL
	d := b[72:90]
	d[3] = 0xFC
	copy(d[5:], []byte(name+"\n"))
	return string(b)
}

// ── The test card ─────────────────────────────────────────────────────────────

// cardIO puts a test card on an external output. The Mac's (System) does it by KMS on the
// connector's card (extdisplay_kms.go); a fixture run can't (ErrNotReal); the Fake pretends.
type cardIO interface {
	// ShowCard puts a card showing code on conn (sysfs's name: card1-DP-1). It takes a moment
	// (a modeset, a DisplayPort link coming up), so it answers later.
	ShowCard(conn, code string) CardShowing
}

// Card is a test card up on an external output. Its methods are called from the screen's
// goroutine; Close is safe to call more than once.
type Card interface {
	Code(code string) error // draw a new code on it
	// About is how it went up, in English for the evidence: "1920x1200 60 Hz on CRTC 51
	// (taken over from the console)".
	About() string
	Close() // take it down: the output and everything taken back as they were
}

// CardShowing is a card going up.
type CardShowing interface {
	Ready() (Card, error, bool)
	// Cancel: the screen was left before it went up; the card comes down once up.
	Cancel()
}

func (s *System) ShowCard(conn, code string) CardShowing {
	if !s.Real() {
		return &showing{c: Now(cardUp{err: ErrNotReal})}
	}
	sh := &showing{}
	sh.c = Go(func() cardUp {
		k, err := showCard(conn, code)
		if err == nil {
			sh.mu.Lock()
			if sh.cancelled {
				defer k.Close()
			} else {
				sh.up = k
			}
			sh.mu.Unlock()
		}
		return cardUp{k, err}
	})
	return sh
}

type cardUp struct {
	card Card
	err  error
}

// showing is a card going up on its own goroutine. Whichever comes second, the card going up
// or Cancel, takes it down: under mu, so a card can't slip through between the two.
type showing struct {
	c         Future[cardUp]
	mu        sync.Mutex
	cancelled bool
	up        Card // the card, once up and not cancelled
}

func (s *showing) Ready() (Card, error, bool) {
	v, ok := s.c.Ready()
	return v.card, v.err, ok
}

func (s *showing) Cancel() {
	s.mu.Lock()
	s.cancelled = true
	k := s.up
	s.up = nil
	s.mu.Unlock()
	if k != nil {
		k.Close()
	}
}

// ── The run ───────────────────────────────────────────────────────────────────

type extDisplay struct {
	gd      *guide     // the Mac's video ports on its port map (nil: no map, or none on it)
	port    string     // the port the monitor came up on, when only one video port has its connector type
	seen    *connector // the connector that came up
	at      float64    // when it came up
	before  map[string]string
	polled  float64
	waiting float64 // when the screen opened

	rng     *rand.Rand
	showing CardShowing // the card going up
	card    Card        // the card up
	cardErr error       // why the card couldn't go up (then the console's mirror is all there is)
	code    string      // the code on the card now
	codeAt  float64     // when it went up
	typed   typed       // the code field (its misses kept across a replug)
	trail   []string    // English, for the evidence: "code 482 on the card, typed 137"
	say     string
	sayAt   float64
}

func (x *extDisplay) Start(c *Ctx) {
	x.rng = proofRNG(c.IO)
	c.Select(-1) // waiting for a monitor: a stray Enter answers nothing
	x.before = map[string]string{}
	for _, k := range externalConnectors(c.IO) {
		x.before[k.name] = k.status
		if k.status == "connected" && x.seen == nil { // plugged in before the test: that's the one
			k := k
			x.seen, x.at = &k, 0
			x.port = x.matchPort(&k)
		}
	}
}

// cardDelay is how long after the connector comes up the card goes up: the EDID and modes
// arrive a moment after "connected", and the console lights the output first.
const cardDelay = 1.0

func (x *extDisplay) Step(c *Ctx) {
	if c.T-x.polled >= 0.5 {
		x.polled = c.T
		for _, k := range externalConnectors(c.IO) {
			switch {
			case x.seen != nil && k.name == x.seen.name:
				k := k
				x.seen = &k // EDID and modes can arrive a moment after "connected"
				if k.status != "connected" {
					x.seen, x.port = nil, "" // unplugged again: the card comes down, the misses stay
					x.takeDown()
					x.cardErr = nil
				}
			case x.seen == nil && k.status == "connected":
				k := k
				x.seen, x.at = &k, c.T
				x.port = x.matchPort(&k)
			}
		}
	}
	if x.seen == nil {
		return
	}
	if x.showing == nil && x.card == nil && x.cardErr == nil && c.T-x.at >= cardDelay {
		x.code = x.newCode()
		if cio, ok := c.IO.(cardIO); ok {
			x.showing = cio.ShowCard(x.seen.dir, x.code)
		} else {
			x.showing = &showing{c: Now(cardUp{err: ErrNotReal})}
		}
	}
	if x.showing != nil {
		k, err, ok := x.showing.Ready()
		if !ok {
			return
		}
		x.showing = nil
		switch {
		case errors.Is(err, ErrNotReal):
			x.decide(c, NotTested("graphics.external-display", checks.Other, x.evidence("wasn't asked: no display access here (a fixture tree) to put a code on it"),
				i18n.T("no display access here")), "")
		case err != nil:
			x.cardErr = err
			x.say, x.sayAt = i18n.T("I couldn't put a code on %s. do you see this screen there?", x.seen.name), c.T
		default:
			x.card, x.codeAt = k, c.T
			x.say, x.sayAt = i18n.T("there's a code on %s now. read it to me.", x.seen.name), c.T
		}
	}
}

func atoi(s string) int {
	n, _ := strconv.Atoi(s)
	return n
}

// newCode is a new code for the card, unlike the one before it.
func (x *extDisplay) newCode() string {
	var avoid []int
	if n, err := strconv.Atoi(x.code); err == nil {
		avoid = append(avoid, n)
	}
	return strconv.Itoa(proofCode(x.rng, avoid...))
}

// takeDown takes the card down (or calls off one going up).
func (x *extDisplay) takeDown() {
	if x.showing != nil {
		x.showing.Cancel()
		x.showing = nil
	}
	if x.card != nil {
		x.card.Close()
		x.card = nil
	}
}

func (x *extDisplay) decide(c *Ctx, r checks.Result, say string) {
	x.takeDown()
	c.Decide(Outcome{Results: []checks.Result{r}, Say: say})
}

func (x *extDisplay) evidence(saw string) string {
	k := x.seen
	ev := fmt.Sprintf("%s (%s) connected", k.name, portKind(k.name))
	if len(k.edid) > 0 {
		ev += fmt.Sprintf(", EDID %d bytes", len(k.edid))
		if n := EDIDName(k.edid); n != "" {
			ev += " (" + n + ")"
		}
	} else {
		ev += ", no EDID"
	}
	if len(k.modes) > 0 {
		ev += fmt.Sprintf(", preferred mode %s of %d", k.modes[0], len(k.modes))
	}
	switch {
	case x.card != nil:
		ev += "; a test card with a code went up on it, " + x.card.About()
	case x.cardErr != nil:
		ev += fmt.Sprintf("; couldn't show a code on the external display (%v); the console mirrors it, so a yes alone isn't proof", x.cardErr)
	}
	if p := x.gd.portOf(x.port); p != nil {
		ev += fmt.Sprintf("; port %s, the Mac's only %s port", p.ID, videoClass(p.Kind))
	}
	if len(x.trail) > 0 {
		ev += "; " + strings.Join(x.trail, "; ")
	}
	return ev + "; the tester " + saw
}

// Answers: with the card up (the code typed in the field above them), "It stays dark" (d) and
// "Something's wrong" (w), letters since the digits type the code; without it, "Yes, I see it"
// (never a pass), "It stays dark" and "Something's wrong"; while it goes up, the card's answers
// greyed.
const (
	cardDark  = 0
	cardWrong = 1
)

func (x *extDisplay) Act(c *Ctx, i int) {
	if x.seen == nil {
		if i == 0 && c.T >= 15 {
			var ks []string
			for _, k := range externalConnectors(c.IO) {
				ks = append(ks, k.name+" "+k.status)
			}
			x.decide(c, Result("graphics.external-display", checks.Failed, Observed,
				"the tester plugged a monitor in and no connector came up: "+strings.Join(ks, ", "), i18n.T("no connector came up")),
				i18n.T("plugged in, and no port saw it. that's a fail."))
		}
		return
	}
	k := x.seen
	switch {
	case x.card != nil:
		switch i {
		case cardDark:
			x.dark(c)
		case cardWrong:
			x.wrong(c)
		}
	case x.cardErr != nil:
		switch i {
		case 0:
			x.decide(c, NotTested("graphics.external-display", checks.Uncertain, x.evidence("says they see this screen on it (the console's mirror)"),
				i18n.T("%s: no code shown, a yes can't pass", k.name)),
				i18n.T("I couldn't put a code there, so a yes can't count. not tested."))
		case 1:
			x.dark(c)
		case 2:
			x.wrong(c)
		}
	}
}

// CodeKey takes the code typed off the monitor (Coder): right, it passes (partial without an
// EDID); wrong, a new code on the card and one more try; wrong twice, not tested.
func (x *extDisplay) CodeKey(c *Ctx, key string) {
	if x.card == nil || c.T-x.codeAt < proofSettle {
		return
	}
	got, ok := x.typed.key(key)
	if !ok {
		return
	}
	k, edid := x.seen, len(x.seen.edid) > 0
	x.trail = append(x.trail, fmt.Sprintf("code %s on the card, typed %s", x.code, got))
	switch {
	case got == x.code && edid:
		x.decide(c, Result("graphics.external-display", checks.Supported, Challenge, x.evidence("typed the code the card showed"),
			i18n.T("%s, %s, code read", k.name, first(k.modes))), i18n.T("code read off %s. external display works.", k.name))
	case got == x.code:
		x.decide(c, Result("graphics.external-display", checks.Partial, Challenge, x.evidence("typed the code the card showed"),
			i18n.T("%s: code read, but no EDID", k.name)), i18n.T("a picture, but the monitor didn't say what it is. partial."))
	case x.typed.miss(got, atoi(x.code)):
		x.decide(c, NotTested("graphics.external-display", checks.Uncertain, x.evidence("typed a wrong code twice"),
			i18n.T("%s: wrong code twice", k.name)), i18n.T("two wrong codes. I can't tell what the monitor shows. not tested."))
	default:
		x.code = x.newCode()
		if err := x.card.Code(x.code); err != nil {
			x.decide(c, NotTested("graphics.external-display", checks.Uncertain, x.evidence(fmt.Sprintf("typed a wrong code once, and the card couldn't take a new code (%v)", err)),
				i18n.T("%s: wrong code", k.name)), i18n.T("that wasn't the code, and I couldn't put up another. not tested."))
			return
		}
		x.codeAt = c.T
		x.typed.wrong = i18n.T("not %s. a new code is up there: one more try", got)
		x.say, x.sayAt = i18n.T("not quite. there's a new code up. one more try."), c.T
	}
}

func (x *extDisplay) dark(c *Ctx) {
	k := x.seen
	x.decide(c, Result("graphics.external-display", checks.Partial, Observed, x.evidence("sees no picture on it"),
		i18n.T("%s detected, no picture", k.name)), i18n.T("the port sees the monitor, the monitor sees nothing. partial."))
}

func (x *extDisplay) wrong(c *Ctx) {
	k := x.seen
	x.decide(c, Result("graphics.external-display", checks.Partial, Observed, x.evidence("sees a wrong picture on it (garbled, cut off or flickering)"),
		i18n.T("%s: wrong picture", k.name)), i18n.T("a picture, but not a right one. partial."))
}

func first(s []string) string {
	if len(s) == 0 {
		return "?"
	}
	return strings.Replace(s[0], "x", "×", 1)
}

func (x *extDisplay) Stop(*Ctx) { x.takeDown() }

func (x *extDisplay) View(c *Ctx) Screen {
	s := x.view(c)
	// with Boot's line under the port map, the column is two rows shorter: reading and typing the
	// code are one step, and the skip's note fits on one line (so it shows at 120×40)
	if x.gd != nil {
		if len(s.Steps) == 3 {
			s.Steps = []string{s.Steps[0], i18n.T("Type the code on the monitor.")}
			s.Step = min(s.Step, 1)
		}
		s.SkipDoes = i18n.T("no monitor? not tested")
	}
	return s
}

func (x *extDisplay) view(c *Ctx) Screen {
	s := Screen{Face: boot.Idle, OutTitle: i18n.T("What I see"), Anim: x.anim()}
	if x.gd != nil { // the Mac's video ports across the band, the picture beside the steps (owner, 2026-10-19)
		s.Anim, s.Map, s.SayRows = nil, x.portMap(), portSayRows
		s.Side, s.SideH = x.small(), smallH
	}
	s.Steps = []string{i18n.T("Plug in a monitor and turn it on."), i18n.T("Read the code on the monitor."),
		i18n.T("Type the code: no Enter needed.")}
	s.Happens = i18n.T("The code shows on the monitor only, not here.")
	if x.gd != nil { // the small picture says it, and with Boot's line under the map the column has no room
		s.Happens = ""
	}
	s.SkipDoes = i18n.T("no monitor? it goes in as not tested")
	if x.seen == nil {
		s.Say, s.SayAt = i18n.T("plug in a monitor. I'm watching the ports."), 0.2
		s.Hint = i18n.T("any video port counts: Mini DisplayPort, Thunderbolt, HDMI, USB-C. is the monitor on?")
		for _, k := range externalConnectors(c.IO) {
			s.Live = append(s.Live, Gone(i18n.T("%s (%s): %s", k.name, portKind(k.name), k.status)))
		}
		if len(s.Live) == 0 {
			s.Live = []Line{Gone(i18n.T("no external video connectors found"))}
		}
		s.Live = append(s.Live, Wait(i18n.T("waiting for a monitor")))
		s.Actions = []Action{{Label: i18n.T("Nothing shows up"), Does: i18n.T("it's plugged in and on, and no port lights up here"), Off: c.T < 15}}
		s.Waiting = i18n.T("·  waiting for a monitor")
		return s
	}
	s.Step = 1
	k := x.seen
	name := EDIDName(k.edid)
	if name == "" {
		name = i18n.T("a monitor")
	}
	s.Say, s.SayAt, s.Face = i18n.T("%s on %s. let me put a code on it.", name, k.name), x.at, boot.Pleased
	if x.say != "" {
		s.Say, s.SayAt = x.say, x.sayAt
	}
	s.Live = []Line{Fact(i18n.T("%s (%s): connected", k.name, portKind(k.name)))}
	if p := x.gd.portOf(x.port); p != nil {
		s.Live = append(s.Live, x.gd.line(*p, i18n.T("the monitor's port")))
	}
	if len(k.edid) > 0 {
		s.Live = append(s.Live, Fact(i18n.T("EDID: %s, %d bytes", name, len(k.edid))))
	} else {
		s.Live = append(s.Live, Gone(i18n.T("no EDID: the kernel uses default modes")))
	}
	if len(k.modes) > 0 {
		s.Live = append(s.Live, Fact(i18n.N("%d mode, preferred %s", "%d modes, preferred %s", len(k.modes), len(k.modes), first(k.modes))))
	} else {
		s.Live = append(s.Live, Wait(i18n.T("waiting for its modes")))
	}
	if x.cardErr != nil {
		s.Steps = []string{s.Steps[0], i18n.T("Does the monitor show this screen?")}
		s.Happens = i18n.T("I couldn't put a code on it, so a yes goes in the report as not tested. \"It stays dark\" and \"Something's wrong\" still count.")
		s.Face = boot.Hmm
		s.Live = append(s.Live, Bad(i18n.T("no code on the monitor: %s", cut(x.cardErr.Error(), 60))))
		s.Actions = []Action{{Label: i18n.T("Yes, I see it"), Does: i18n.T("it shows this screen")},
			{Label: i18n.T("It stays dark"), Does: i18n.T("no picture, \"no signal\"")},
			{Label: i18n.T("Something's wrong"), Does: i18n.T("garbled or flickering")}}
		s.Waiting = i18n.T("·  waiting for your answer")
		return s
	}
	switch {
	case x.card != nil:
		s.Live = append(s.Live, Fact(i18n.T("a code card on %s: the code shows there only", k.name)))
		s.Code = x.typed.field(i18n.T("The code on the monitor"))
		if x.typed.value != "" || len(x.typed.misses) > 0 {
			s.Step = 2
		}
		s.Hint = i18n.T("no code on the monitor? some take a few seconds to wake up.")
		s.Waiting = i18n.T("·  waiting for the code")
	case c.Outcome() == nil: // (once decided, the card is down already)
		s.Live = append(s.Live, Wait(i18n.T("putting a code on the monitor")))
		s.Waiting = i18n.T("·  putting a code on the monitor")
	}
	for _, v := range x.typed.tried {
		s.Live = append(s.Live, Bad(i18n.T("you typed %s: not the code on the monitor", v)))
	}
	s.Actions = []Action{{Label: i18n.T("It stays dark"), Does: i18n.T("no picture, \"no signal\""), Key: "d", Off: x.card == nil},
		{Label: i18n.T("Something's wrong"), Does: i18n.T("garbled or flickering"), Key: "w", Off: x.card == nil}}
	return s
}

// anim is the band's picture: this Mac, a cable, and the monitor, dark until one comes up, then
// with its code card, the code itself left out: it shows on the monitor only. With a port map
// in the band, small draws it over What I see instead.
func (x *extDisplay) anim() Panel {
	seen, up, port, name := x.seen != nil, x.card != nil, "", ""
	if x.seen != nil {
		port, name = x.seen.name, EDIDName(x.seen.edid)
	}
	fail := x.cardErr != nil
	return func(gr *g.Grid, ax, y, w, h int, t float64) {
		mx, mw := ax+2, 14
		box(gr, mx, y+2, mw, 6, g.GRY)
		gr.Put(mx+3, y+4, i18n.T("this Mac"), g.WHT)
		dx := mx + mw + 14
		dw := min(30, max(16, ax+w-dx-2))
		lc := g.DIM
		if seen {
			lc = g.BVIO
		}
		for i := mx + mw; i < dx; i++ {
			gr.Set(i, y+5, '─', lc)
		}
		if port != "" {
			gr.Put(mx+mw+2, y+4, cut(port, 11), g.GRY)
		}
		col := g.DIM
		if seen {
			col = g.GRY
		}
		box(gr, dx, y+1, dw, 8, col)
		gr.Put(dx+(dw-3)/2, y+9, "▀▀▀", col)
		label := i18n.T("no signal")
		switch {
		case up:
			for k := 0; k < 3; k++ { // the card's digits, hidden: they're on the monitor
				box(gr, dx+(dw-17)/2+k*6, y+3, 5, 3, g.BVIO)
				gr.Put(dx+(dw-17)/2+k*6+2, y+4, "?", g.BVIO)
			}
			label = i18n.T("the code is on the monitor")
		case fail:
			label = i18n.T("no code card")
		case seen:
			label = i18n.T("putting a code on it…")
		}
		if !up {
			gr.Put(dx+max(1, (dw-width(label))/2), y+4, cut(label, dw-2), col)
		} else {
			gr.Put(dx, y+10, cut(label, w-(dx-ax)), g.DIM)
		}
		if name != "" {
			gr.Put(dx, y, cut(name, dw), g.GRY)
		}
	}
}

// smallH is the small picture's height in rows.
const smallH = 3

// small is anim's picture over What I see, with the port map in the band: the same Mac, cable
// and monitor in smallH rows, the monitor's name in its edge and its code left out.
func (x *extDisplay) small() Panel {
	seen, up, port, name := x.seen != nil, x.card != nil, "", ""
	if x.seen != nil {
		port, name = x.seen.name, EDIDName(x.seen.edid)
	}
	fail := x.cardErr != nil
	return func(gr *g.Grid, ax, y, w, h int, t float64) {
		mx, mw := ax+2, 12
		box(gr, mx, y, mw, 3, g.GRY)
		gr.Put(mx+2, y+1, i18n.T("this Mac"), g.WHT)
		dx := mx + mw + 12
		dw := min(34, max(24, ax+w-dx-1))
		lc, col := g.DIM, g.DIM
		if seen {
			lc, col = g.BVIO, g.GRY
		}
		for i := mx + mw; i < dx; i++ {
			gr.Set(i, y+1, '─', lc)
		}
		if port != "" {
			p := " " + cut(port, 9) + " "
			gr.Put(mx+mw+(dx-mx-mw-width(p))/2, y+1, p, g.GRY)
		}
		box(gr, dx, y, dw, 3, col)
		if name != "" {
			gr.Put(dx+2, y, " "+cut(name, dw-6)+" ", g.GRY)
		}
		label, lcol := i18n.T("no signal"), col
		switch {
		case up:
			label, lcol = i18n.T("? ? ?  on the monitor"), g.BVIO
		case fail:
			label = i18n.T("no code card")
		case seen:
			label = i18n.T("putting a code on it…")
		}
		gr.Put(dx+2, y+1, cut(label, dw-4), lcol)
	}
}

// box draws a w×h box at x, y in col.
func box(gr *g.Grid, x, y, w, h int, col g.Color) {
	if w < 2 || h < 2 {
		return
	}
	gr.Put(x, y, "┌"+strings.Repeat("─", w-2)+"┐", col)
	for j := 1; j < h-1; j++ {
		gr.Set(x, y+j, '│', col)
		gr.Set(x+w-1, y+j, '│', col)
	}
	gr.Put(x, y+h-1, "└"+strings.Repeat("─", w-2)+"┘", col)
}

// ── The Fake ──────────────────────────────────────────────────────────────────
//
// The Fake's card is files with a "kms:" prefix: "kms:fail" = an error for ShowCard (no DRM
// master, no free CRTC…); "kms:code" = the code on the card while it's up ("" once down),
// "kms:conn" = the connector it went up on; "kms:up" counts cards up (0 once a test has
// stopped).

func (f *Fake) ShowCard(conn, code string) CardShowing {
	sh := &fakeShowing{f: f, at: f.now + 0.5}
	if e := f.Read("kms:fail"); e != "" {
		sh.err = errors.New(e)
		return sh
	}
	n, _ := f.Int("kms:up")
	f.Set("kms:up", strconv.Itoa(n+1))
	f.Set("kms:code", code)
	f.Set("kms:conn", conn)
	sh.card = &fakeCard{f: f}
	return sh
}

type fakeShowing struct {
	f    *Fake
	at   float64
	card *fakeCard
	err  error
}

func (s *fakeShowing) Ready() (Card, error, bool) {
	if s.f.now < s.at {
		return nil, nil, false
	}
	if s.card == nil {
		return nil, s.err, true
	}
	return s.card, nil, true
}

func (s *fakeShowing) Cancel() {
	if s.card != nil {
		s.card.Close()
	}
}

type fakeCard struct {
	f      *Fake
	closed bool
}

func (k *fakeCard) Code(code string) error {
	if k.closed {
		return errors.New("the card is down")
	}
	k.f.Set("kms:code", code)
	return nil
}

func (k *fakeCard) About() string { return "1920x1200 60 Hz on CRTC 51 (the demo's)" }

func (k *fakeCard) Close() {
	if !k.closed {
		k.closed = true
		n, _ := k.f.Int("kms:up")
		k.f.Set("kms:up", strconv.Itoa(n-1))
		k.f.Set("kms:code", "")
	}
}

// ── The port map ──────────────────────────────────────────────────────────────

// videoNotes are the catalog's port kinds that carry a picture, each with its note on the map:
// the connector a monitor's cable needs, where the port's name doesn't say it ("" where it
// does: HDMI, DVI, Mini DisplayPort, and USB-C, a name the map gives the Thunderbolt 3 ports
// too: guide.callUSBC).
var videoNotes = map[string]string{
	"mini-displayport": "",
	"thunderbolt":      i18n.M("Mini DisplayPort"),
	"thunderbolt3":     "",
	"usb-c":            "",
	"hdmi":             "",
	"dvi":              "",
	"mini-dvi":         i18n.M("DVI/VGA adapter"),
	"micro-dvi":        i18n.M("DVI/VGA adapter"),
}

// videoNote is a video port's note, translated.
func videoNote(kind string) string {
	if n := videoNotes[kind]; n != "" {
		return i18n.S(n)
	}
	return ""
}

// videoClass is the kind of DRM connector behind a video port: DP, HDMI or DVI (DVI and VGA
// connectors: Apple's mini- and micro-DVI adapters carry analog VGA too).
func videoClass(kind string) string {
	switch kind {
	case "hdmi":
		return "HDMI"
	case "dvi", "mini-dvi", "micro-dvi":
		return "DVI"
	}
	return "DP"
}

// connClass is a DRM connector's class, as videoClass names them.
func connClass(name string) string {
	switch {
	case strings.HasPrefix(name, "HDMI"):
		return "HDMI"
	case strings.HasPrefix(name, "DVI"), strings.HasPrefix(name, "VGA"):
		return "DVI"
	}
	return "DP"
}

// videoGuide is the Mac's video ports on its port map, or nil.
func videoGuide(e *Env) *guide {
	kinds, w := make([]string, 0, len(videoNotes)), 0
	for k := range videoNotes {
		kinds = append(kinds, k)
		w = max(w, width(videoNote(k)))
	}
	return newGuide(e, w, kinds...).callUSBC()
}

// matchPort is the video port a connector came up on, when only one of the Mac's video ports
// has its type ("" otherwise: two Thunderbolt ports can't be told apart).
func (x *extDisplay) matchPort(k *connector) string {
	if x.gd == nil || k == nil {
		return ""
	}
	id := ""
	for _, p := range x.gd.ports {
		if videoClass(p.Kind) == connClass(k.name) {
			if id != "" {
				return ""
			}
			id = p.ID
		}
	}
	return id
}

// portOf is the guide's port id, or nil (no guide, no id).
func (gd *guide) portOf(id string) *identify.Port {
	if gd == nil || id == "" {
		return nil
	}
	return gd.port(id)
}

// portMap is the map for the screen: every video port grey with its connector, the monitor's
// green once it's known.
func (x *extDisplay) portMap() *PortMap {
	m := &PortMap{Sides: x.gd.sides, State: map[string]PortState{}, Note: map[string]string{}, NoteW: x.gd.noteW}
	for _, p := range x.gd.ports {
		m.State[p.ID], m.Note[p.ID] = PortWait, videoNote(p.Kind)
		if p.ID == x.port {
			m.State[p.ID] = PortSeen
		}
	}
	return m
}
