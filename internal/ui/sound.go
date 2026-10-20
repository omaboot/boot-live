package ui

import (
	"strconv"
	"strings"
	"time"

	"github.com/omaboot/boot-live/internal/boot"
	g "github.com/omaboot/boot-live/internal/grid"
	"github.com/omaboot/boot-live/internal/handson"
	"github.com/omaboot/boot-live/internal/i18n"
	"github.com/omaboot/boot-live/internal/pace"
	"github.com/omaboot/boot-live/internal/sounds"
)

// Boot's sounds: off until M turns them on, the setting and
// the picks kept on the stick for the next boot. Hello when Boot appears; All good as Which Mac
// finds the Mac (timed with the search landing); on Network, pass as a
// connection is made and fail as one fails (netMsg); in the quick pass one
// blip per result as its line shows (one per reaction of Boot's left the second of two "Omarchy
// required" lines silent, a burst getting one reaction), a hands-on
// test's when it's decided, at most one every 0.3 s; a chime by outcome when the tests are done (Quick results, then Finish after
// hands-on tests), and the call on "Boot needs you" each time its frame flashes. Nothing plays
// while something is measured: the quick pass's thermal load and idle GPU, a hands-on test
// before it's decided (the audio tests play their own sounds), sleepcheck. Boot's sounds are
// never evidence of anything. The console's sounds opens the sounds screen: the three options of
// each moment to pick from, in every build.

// blipGap is the least time between two blips (chimes don't wait): under the quick pass's
// least gap between lines (pace.Default.Line, 0.8 s), so every line gets its blip.
const blipGap = 300 * time.Millisecond

// attentionCalls are when "Boot needs you" calls, seconds into the screen: as its frame flashes
// (drawAttention).
var attentionCalls = []float64{0.2, 9}

// soundClock is what Boot's sounds have played, so each plays once.
type soundClock struct {
	saved     sounds.Prefs // the settings as last saved
	blipAt    time.Time
	shown     int              // the quick pass's lines shown at the last tick
	out       *handson.Outcome // the hands-on outcome last sounded
	hello     bool
	found     bool      // Which Mac's chime played
	quick     bool      // Quick results' chime played
	finish    bool      // Finish's chime played (after hands-on tests)
	callAt    time.Time // the "Boot needs you" screen the calls are counted for (when it opened)
	calls     int
	measuring string // the quick-pass check running now, when it's one that measures
	wavs      map[string][]byte
}

// statusSound is the moment of each result (none for a skip).
var statusSound = map[pace.Status]string{pace.Pass: "pass", pace.Partial: "partial", pace.Fail: "fail", pace.Live: "live"}

// measures reports whether a check (or a result's criterion) measures the Mac at rest or under
// load: nothing plays while it runs.
func measures(id string) bool {
	return id == "thermal" || strings.HasPrefix(id, "thermal.") || id == "graphics.power-down"
}

// soundTick plays what the screen on now calls for (Update's clock, every frame).
func (m *Model) soundTick() {
	st, c := &m.st, &m.snd
	switch st.Scene {
	case Hello:
		if !c.hello && st.T > 0.2 { // as Boot appears
			c.hello = true
			m.sound("hello")
		}
	case WhichMac:
		if !c.found && !m.f.Unknown && st.T > foundAt(m.f) { // as the search lands on this Mac
			c.found = true
			m.sound("good")
		}
	case Quick:
		if st.Paused {
			return
		}
		if n := m.player.At(st.T).Shown; n > c.shown {
			c.shown = n // a line that just showed: its result's blip
			m.sound(statusSound[m.player.Slots()[n-1].Status])
		}
	case Results:
		if !c.quick {
			c.quick = true
			m.sound(m.endSound())
		}
	case Attention:
		if c.callAt != m.starts[Attention] { // it opened again: its calls come again
			c.callAt, c.calls = m.starts[Attention], 0
		}
		if c.calls < len(attentionCalls) && st.T >= attentionCalls[c.calls] {
			c.calls++
			m.sound("summon")
		}
	case Test:
		if h := st.HO; h != nil && h.Out != nil && h.Out != c.out {
			c.out = h.Out
			m.sound(outcomeSound(*h.Out))
		}
	case Finish:
		if !c.finish && st.HO != nil && len(st.HO.Results) > 0 {
			c.finish = true
			m.sound(m.endSound())
		}
	}
}

// foundAt is when Which Mac finds the Mac: the catalog search lands (macFound), or, with no
// search to show, as Boot says he found it.
func foundAt(f *Facts) float64 {
	if f.View != nil {
		return macFound
	}
	return 0.3
}

// endSound is the chime for the results so far: problems if any failed, mixed if any works with
// limits, else all good.
func (m *Model) endSound() string {
	switch {
	case m.f.Tally(pace.Fail) > 0:
		return "problems"
	case m.f.Tally(pace.Partial) > 0:
		return "mixed"
	}
	return "good"
}

// outcomeSound is a hands-on outcome's blip, by its worst result (a result not tested counts
// least); "" when none was tested.
func outcomeSound(o handson.Outcome) string {
	order := []pace.Status{pace.Fail, pace.Partial, pace.Live, pace.Pass}
	ids := []string{"fail", "partial", "live", "pass"}
	for i, s := range order {
		for _, r := range o.Results {
			if r.Pace() == s {
				return ids[i]
			}
		}
	}
	return ""
}

// quiet reports whether nothing may play now: something is being measured, or the stick (and
// aplay with it) is gone.
func (m *Model) quiet() bool {
	st := &m.st
	if st.Gone || m.snd.measuring != "" || m.sleepChecking || measures(m.rerunning) {
		return true
	}
	h := st.HO
	return st.Scene == Test && h != nil && h.Run != nil && h.Out == nil
}

// sound plays moment id's pick, when sound is on, nothing is measured and the sounds screen
// isn't up (it plays only what's picked there); a blip waits blipGap after the last.
func (m *Model) sound(id string) {
	st := &m.st
	mo, ok := sounds.Find(id)
	if !ok || !st.Sound.On || st.Sounds || m.quiet() {
		return
	}
	if !mo.Big && time.Since(m.snd.blipAt) < blipGap {
		return
	}
	m.snd.blipAt = time.Now()
	m.play(mo, st.Sound.Pick(id))
}

// play plays option k of moment mo (each made once).
func (m *Model) play(mo sounds.Moment, k int) {
	key := mo.ID + "/" + strconv.Itoa(k)
	b, ok := m.snd.wavs[key]
	if !ok {
		b = sounds.WAV(mo.Options[k])
		if m.snd.wavs == nil {
			m.snd.wavs = map[string][]byte{}
		}
		m.snd.wavs[key] = b
	}
	m.be.Sound(b)
}

// toggleSound is M: sound on or off, kept for the next boot. Turned on, the pass blip says so.
func (m *Model) toggleSound() {
	st := &m.st
	st.Sound.On = !st.Sound.On
	m.saveSounds()
	if st.Sound.On && !st.Sounds {
		m.snd.blipAt = time.Time{}
		m.sound("pass")
	}
}

// saveSounds keeps the settings on the stick when they changed since the last save.
func (m *Model) saveSounds() {
	if !m.st.Sound.Same(m.snd.saved) {
		m.snd.saved = m.st.Sound
		m.be.SaveSounds(m.st.Sound)
	}
}

// soundKey reports whether M works here: not in a text field or over a box (the quit question,
// the full log), nor while a test has the keyboard. The sounds screen takes it itself.
func soundKey(st *State) bool {
	return st.Sounds || conAvail(st) && !textFocusOf(st) && st.Overlay == NoOverlay
}

// ── The sounds screen ─────────────────────────────────────────────────────────

// openSounds is the console's sounds: the screen comes up over the one on now (the quick pass
// pauses under it), its cursor on the first moment.
func (m *Model) openSounds() {
	m.st.Sounds, m.st.SoundRow = true, 0
	m.pause(true)
}

func (m *Model) closeSounds() {
	m.st.Sounds = false
	m.pause(false)
	m.saveSounds()
}

// soundsKey: ↑↓ the moment, ←→ (or 1–3) its option, which plays; Enter plays it again; M sound
// on or off; Esc or Backspace back to the screen under it.
func (m *Model) soundsKey(key string) {
	st := &m.st
	n := len(sounds.Moments)
	st.SoundRow = max(0, min(n-1, st.SoundRow))
	mo := sounds.Moments[st.SoundRow]
	k, opts := st.Sound.Pick(mo.ID), len(mo.Options)
	switch key {
	case "esc", "backspace":
		m.closeSounds()
	case "up", "k":
		st.SoundRow = (st.SoundRow + n - 1) % n
	case "down", "j":
		st.SoundRow = (st.SoundRow + 1) % n
	case "left", "right":
		if key == "right" {
			k = (k + 1) % opts
		} else {
			k = (k + opts - 1) % opts
		}
		st.Sound = st.Sound.With(mo.ID, k)
		m.preview()
	case "1", "2", "3":
		if i := int(key[0] - '1'); i < opts {
			st.Sound = st.Sound.With(mo.ID, i)
			m.preview()
		}
	case "enter", " ":
		m.preview()
	case "m", "M":
		m.toggleSound()
	}
}

// preview plays the moment under the cursor's pick, when sound is on and nothing is measured.
func (m *Model) preview() {
	st := &m.st
	if !st.Sound.On || m.quiet() {
		return
	}
	mo := sounds.Moments[st.SoundRow]
	m.play(mo, st.Sound.Pick(mo.ID))
}

// drawSounds is the sounds screen (the "Dev menu: sounds"): each moment, when it
// plays, and its three options, the pick in brackets.
func drawSounds(gr *g.Grid, o *Options, st *State, f *Facts) Box {
	quiet := st.Hush
	b := frame(gr, o, st, f.Mac, frameSpec{title: i18n.T("Sounds"), step: -1, right: netRight(f, st), mac: f.Known,
		keys: Keys{Esc: true, EscClose: true, Back: true}})
	l := layout(b, o.Plain)
	y := b.Y + 1
	if !o.Plain {
		boot.Draw(gr, l.BootX, y, boot.Idle, life(st, st.T))
	}
	fw := b.X + b.W - l.FieldX
	gr.Put(l.FieldX, y+1, cut(i18n.T("Boot's sounds"), fw), g.WHT)
	gr.Put(l.FieldX, y+3, cut(i18n.T("Pick one of three for each moment."), fw), g.DIM)
	switch {
	case !st.Sound.On:
		gr.Put(l.FieldX, y+5, cut(i18n.T("Sound is off. M turns it on; then each option plays as you pick it."), fw), g.AMB)
	case quiet:
		gr.Put(l.FieldX, y+5, cut(i18n.T("Something is being measured: nothing plays until it's done."), fw), g.AMB)
	default:
		gr.Put(l.FieldX, y+5, cut(i18n.T("Sound is on. Each option plays as you pick it; Enter plays it again."), fw), g.DIM)
	}
	gr.Put(l.FieldX, y+6, cut(i18n.T("M and your picks are kept on the stick for the next boot."), fw), g.DIM)

	cx := b.X + 2
	ty := y + 9
	gr.Put(cx, ty, i18n.T("Moment"), g.DIM)
	gr.Put(cx+18, ty, i18n.T("Plays when"), g.DIM)
	gr.Put(cx+52, ty, i18n.T("Options"), g.DIM)
	gr.Put(cx, ty+1, strings.Repeat("─", max(0, b.X+b.W-cx)), g.DIM)
	end := b.X + b.W
	for i, mo := range sounds.Moments {
		ry, on := ty+2+2*i, i == st.SoundRow
		lc := g.GRY
		if on {
			gr.Put(b.X, ry, "▶", g.BVIO)
			lc = g.WHT
		}
		gr.Put(cx, ry, cut(i18n.S(mo.Label), 17), lc)
		gr.Put(cx+18, ry, cut(i18n.S(mo.When), 33), g.DIM)
		x, pick := cx+52, st.Sound.Pick(mo.ID)
		for k, op := range mo.Options {
			label := strconv.Itoa(k+1) + " " + i18n.S(op.Name)
			if x+width(label)+2 > end {
				break
			}
			if k == pick {
				bc, tc := g.VIO, g.GRY
				if on {
					bc, tc = g.BVIO, g.WHT
				}
				gr.Put(x, ry, "[", bc)
				gr.Put(gr.Put(x+1, ry, label, tc), ry, "]", bc)
			} else {
				gr.Put(x+1, ry, label, g.DIM)
			}
			x += width(label) + 4
		}
	}
	gr.Put(cx, b.Y+b.H-1, cut(i18n.T("↑↓  moment    ←→  option, it plays    1–3  pick    Enter  play it again"), end-cx), g.DIM)
	return b
}
