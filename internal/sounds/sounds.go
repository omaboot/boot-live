// Package sounds is Boot's sounds (LIVE-PLAN §5.7): short, quiet 8-bit blips and chimes, made
// here at run time (no sound files), one per moment, each with three options to pick from (the
// prototype's "Dev menu: sounds", boot-live-proto v55; the owner's picks are the defaults,
// 2026-10-19, but call twice for "Boot needs you", 0.8.3 review). Off until M turns them on; the
// picks and M's setting are kept on the stick.
package sounds

import (
	"encoding/json"

	"github.com/omaboot/boot-live/internal/i18n"
)

// Wave is a note's voice.
type Wave int

const (
	Triangle Wave = iota
	Pulse         // a 25 % pulse, the classic 8-bit voice
	Sine
)

// Note is one note: a MIDI note number (fractional for the traced R2 sounds; 0 is a rest), its
// length in seconds, voice and volume (0–1), and optionally a slide to another note over its
// length (To) and a volume it swells or fades to (End, else -1: it holds, then releases).
type Note struct {
	Midi, Dur float64
	Wave      Wave
	Vol       float64
	To, End   float64
}

func n(m, d float64, w Wave, v float64) Note { return Note{m, d, w, v, 0, -1} }
func slide(m, d float64, w Wave, v, to float64) Note {
	return Note{m, d, w, v, to, -1}
}
func shaped(m, d float64, w Wave, v, to, end float64) Note { return Note{m, d, w, v, to, end} }
func rest(d float64) Note                                  { return Note{Dur: d, End: -1} }

// Option is one of a moment's sounds: its name (msgid) and its notes, in layers that start
// together (most have one). Bright ones skip the filter that softens the square-ish voices.
// Level scales its notes' volumes (0: as written).
type Option struct {
	Name   string
	Layers [][]Note
	Bright bool
	Level  float64
}

func one(name string, notes ...Note) Option { return Option{Name: name, Layers: [][]Note{notes}} }

// Moment is when a sound plays: its ID (as Prefs keeps it), label and when (msgids), its options
// and the one picked by default. A big one (a chime) is never held back by the blips' gap.
type Moment struct {
	ID, Label, When string
	Big             bool
	Default         int
	Options         []Option
}

// Moments are the moments, in the sounds screen's order. The notes are the prototype's, as is.
var Moments = []Moment{
	{ID: "pass", Label: i18n.M("Pass"), When: i18n.M("a check passed"), Default: 0, Options: []Option{
		one(i18n.M("two-step up"), n(84, 0.05, Triangle, 0.55), n(91, 0.07, Triangle, 0.5)),
		one(i18n.M("ding"), n(88, 0.1, Triangle, 0.5)),
		one(i18n.M("pip-pip-pip"), n(79, 0.03, Pulse, 0.3), n(84, 0.03, Pulse, 0.3), n(88, 0.05, Pulse, 0.3))}},
	{ID: "partial", Label: i18n.M("Partial"), When: i18n.M("works, with limits"), Default: 0, Options: []Option{
		one(i18n.M("same twice"), n(79, 0.06, Pulse, 0.4), rest(0.03), n(79, 0.08, Pulse, 0.3)),
		one(i18n.M("hm?"), n(79, 0.07, Triangle, 0.5), n(81, 0.11, Triangle, 0.5)),
		one(i18n.M("shrug slide"), slide(81, 0.16, Triangle, 0.5, 78))}},
	{ID: "fail", Label: i18n.M("Fail"), When: i18n.M("a check failed"), Default: 1, Options: []Option{
		one(i18n.M("uh-oh"), n(64, 0.08, Triangle, 0.55), rest(0.03), n(63, 0.18, Triangle, 0.5)),
		one(i18n.M("tritone"), n(64, 0.08, Triangle, 0.55), rest(0.03), n(58, 0.2, Triangle, 0.5)),
		one(i18n.M("womp"), slide(64, 0.24, Triangle, 0.55, 60))}},
	{ID: "live", Label: i18n.M("Omarchy required"), When: i18n.M("a live boot can't decide it"), Default: 2, Options: []Option{
		one(i18n.M("tick"), n(69, 0.045, Triangle, 0.45)),
		one(i18n.M("tick-tick"), n(69, 0.03, Triangle, 0.35), rest(0.05), n(69, 0.03, Triangle, 0.35)),
		one(i18n.M("low pluck"), n(64, 0.06, Pulse, 0.3))}},
	{ID: "hello", Label: i18n.M("Hello"), When: i18n.M("Boot appears"), Default: 0, Options: []Option{
		one(i18n.M("three up"), n(72, 0.06, Pulse, 0.35), n(79, 0.06, Pulse, 0.35), n(84, 0.11, Pulse, 0.35)),
		one(i18n.M("power-on sweep"), slide(60, 0.26, Triangle, 0.45, 84), n(91, 0.06, Pulse, 0.3)),
		one(i18n.M("hi!"), n(79, 0.07, Pulse, 0.35), n(88, 0.13, Pulse, 0.35))}},
	{ID: "good", Label: i18n.M("All good"), When: i18n.M("Mac found; all tests passed"), Big: true, Default: 1, Options: []Option{
		one(i18n.M("arpeggio"), n(60, 0.09, Pulse, 0.4), n(64, 0.09, Pulse, 0.4), n(67, 0.09, Pulse, 0.4), n(72, 0.38, Triangle, 0.6)),
		one(i18n.M("fanfare"), n(55, 0.08, Pulse, 0.4), n(60, 0.08, Pulse, 0.4), n(64, 0.08, Pulse, 0.4), n(67, 0.08, Pulse, 0.4), rest(0.05),
			n(72, 0.1, Pulse, 0.45), n(76, 0.34, Triangle, 0.55)),
		one(i18n.M("soft bells"), n(76, 0.15, Triangle, 0.5), n(79, 0.15, Triangle, 0.5), n(84, 0.42, Triangle, 0.55))}},
	{ID: "mixed", Label: i18n.M("Mixed"), When: i18n.M("tests done, some limits"), Big: true, Default: 2, Options: []Option{
		one(i18n.M("held note"), n(60, 0.09, Pulse, 0.4), n(64, 0.09, Pulse, 0.4), n(67, 0.09, Pulse, 0.4), n(72, 0.12, Triangle, 0.5), n(74, 0.36, Triangle, 0.45)),
		one(i18n.M("up, then sideways"), n(60, 0.09, Pulse, 0.4), n(64, 0.09, Pulse, 0.4), n(67, 0.09, Pulse, 0.4), n(65, 0.4, Triangle, 0.5)),
		one(i18n.M("up and back"), n(60, 0.1, Triangle, 0.5), n(67, 0.1, Triangle, 0.5), n(64, 0.1, Triangle, 0.5), n(62, 0.34, Triangle, 0.5))}},
	{ID: "problems", Label: i18n.M("Problems"), When: i18n.M("tests done, something failed"), Big: true, Default: 2, Options: []Option{
		one(i18n.M("uh-uh"), n(57, 0.16, Triangle, 0.55), rest(0.06), n(52, 0.5, Triangle, 0.55)),
		one(i18n.M("low buzz"), n(45, 0.14, Pulse, 0.4), rest(0.07), n(41, 0.45, Pulse, 0.4)),
		r2Sigh}},
	{ID: "summon", Label: i18n.M("Boot needs you"), When: i18n.M("before the hands-on tests"), Big: true, Default: 0, Options: []Option{
		one(i18n.M("call twice"), n(79, 0.09, Pulse, 0.45), n(84, 0.13, Pulse, 0.45), rest(0.1), n(79, 0.09, Pulse, 0.45), n(84, 0.22, Pulse, 0.45)),
		// traced from the owner's reference (worried.mp3, 1.1 s): E7 whistle, drop to A6 fading
		// out, a breath, A6 swelling back, glide up to ~G7, hold, fade; at half the prototype's
		// volume (owner, 0.8.3 review: too loud, its whistle high where the ear hears best)
		{Name: i18n.M("are you ok?"), Level: 0.5, Layers: [][]Note{{shaped(100.1, 0.23, Sine, 0.42, 0, 0.5), shaped(93.1, 0.12, Sine, 0.3, 0, 0.06),
			rest(0.1), shaped(93.1, 0.1, Sine, 0.08, 0, 0.28), shaped(93.2, 0.33, Sine, 0.28, 102.7, 0.55),
			shaped(102.7, 0.06, Sine, 0.55, 0, 0.45), shaped(102.7, 0.14, Sine, 0.45, 0, 0.01)}}},
		one(i18n.M("ding-dong"), n(88, 0.16, Triangle, 0.55), n(84, 0.42, Triangle, 0.55))}},
}

// r2Sigh is traced from the owner's reference (6.mp3, 3 s): slow high beeps, then fast chatter,
// over a low tone that rises faster and faster and fades.
var r2Sigh = func() Option {
	s := func(m, d, v float64) Note { return n(m, d, Sine, v) }
	return Option{Name: i18n.M("R2 sigh"), Bright: true, Layers: [][]Note{
		{rest(0.05), s(101.0, 0.17, 0.32), rest(0.15), s(107.2, 0.16, 0.28), rest(0.01), s(109.3, 0.15, 0.28), s(101.2, 0.17, 0.32), rest(0.15),
			s(110.8, 0.16, 0.26), s(105.6, 0.16, 0.28), rest(0.15), s(111.9, 0.16, 0.25), s(104.6, 0.13, 0.28), rest(0.07),
			s(109.9, 0.07, 0.25), s(106.5, 0.05, 0.25), s(115.2, 0.03, 0.18), s(111.3, 0.04, 0.22), s(106.9, 0.04, 0.25), rest(0.03),
			s(113.7, 0.02, 0.2), s(109.3, 0.04, 0.24), rest(0.02), s(106.7, 0.02, 0.24), s(106.3, 0.03, 0.24), rest(0.04), s(104.7, 0.02, 0.24),
			s(100.1, 0.02, 0.25), rest(0.02), s(113.6, 0.03, 0.2), rest(0.03), s(100.1, 0.02, 0.25), s(107.0, 0.02, 0.24), s(106.5, 0.03, 0.24),
			rest(0.01), s(112.1, 0.02, 0.22), s(115.2, 0.01, 0.18), s(103.1, 0.03, 0.25), rest(0.01), s(106.6, 0.02, 0.24), rest(0.01), s(106.0, 0.01, 0.24),
			s(102.0, 0.02, 0.25), s(102.3, 0.03, 0.25), rest(0.02), s(101.1, 0.03, 0.25), s(109.3, 0.03, 0.22), rest(0.05), s(111.0, 0.03, 0.2),
			s(101.4, 0.03, 0.22), s(98.6, 0.03, 0.22)},
		{rest(1.34), shaped(74.5, 0.14, Triangle, 0.25, 74.3, 0.25), shaped(74.3, 0.3, Triangle, 0.22, 75.6, 0.26), shaped(75.6, 0.47, Triangle, 0.26, 77.2, 0.34),
			shaped(77.2, 0.23, Triangle, 0.34, 80.4, 0.42), shaped(80.4, 0.27, Triangle, 0.42, 83.0, 0.48), shaped(83.0, 0.27, Triangle, 0.48, 87.2, 0.01)},
	}}
}()

// Find is the moment with ID id.
func Find(id string) (Moment, bool) {
	for _, m := range Moments {
		if m.ID == id {
			return m, true
		}
	}
	return Moment{}, false
}

// Prefs are the sound settings: on or off (M), and the picks that differ from the defaults.
type Prefs struct {
	On    bool           `json:"on"`
	Picks map[string]int `json:"picks,omitempty"` // by moment ID: the option picked, when not the default
}

// Pick is the option picked for moment id.
func (p Prefs) Pick(id string) int {
	m, ok := Find(id)
	if !ok {
		return 0
	}
	if k, ok := p.Picks[id]; ok && k >= 0 && k < len(m.Options) {
		return k
	}
	return m.Default
}

// With is p with option k picked for moment id (a default isn't kept: it follows the defaults).
func (p Prefs) With(id string, k int) Prefs {
	picks := map[string]int{}
	for mid, v := range p.Picks {
		picks[mid] = v
	}
	picks[id] = k
	p.Picks = picks
	return p.clean()
}

// clean keeps the picks of moments this version has, in range and not the default.
func (p Prefs) clean() Prefs {
	var picks map[string]int
	for _, m := range Moments {
		if k, ok := p.Picks[m.ID]; ok && k != m.Default && k >= 0 && k < len(m.Options) {
			if picks == nil {
				picks = map[string]int{}
			}
			picks[m.ID] = k
		}
	}
	p.Picks = picks
	return p
}

// Same reports whether p and q set the same.
func (p Prefs) Same(q Prefs) bool {
	if p.On != q.On {
		return false
	}
	for _, m := range Moments {
		if p.Pick(m.ID) != q.Pick(m.ID) {
			return false
		}
	}
	return true
}

// File is where the settings are kept on the stick (the BOOTLIVE stick, else OMABOOT).
const File = "settings/sounds.json"

// Parse reads the settings file; anything it can't read is the defaults, off.
func Parse(b []byte) Prefs {
	var p Prefs
	if json.Unmarshal(b, &p) != nil {
		return Prefs{}
	}
	return p.clean()
}

// JSON is the settings file.
func (p Prefs) JSON() []byte {
	b, _ := json.MarshalIndent(p, "", "  ")
	return append(b, '\n')
}
