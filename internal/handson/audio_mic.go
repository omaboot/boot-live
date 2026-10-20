package handson

import (
	"fmt"
	"math/rand/v2"
	"strings"
	"time"

	"github.com/omaboot/boot-live/internal/boot"
	"github.com/omaboot/boot-live/internal/checks"
	g "github.com/omaboot/boot-live/internal/grid"
	"github.com/omaboot/boot-live/internal/i18n"
)

// audio.microphone, "Built-in microphone": the tool records the built-in codec's capture device
// (16 kHz mono, capture switched on) and measures the room for a moment (the noise floor: the
// median level of 50 ms windows, and the room's own level at each note's frequency). A level
// rising is no proof: a key click, a bump or the fan lifts it with the microphone hearing only
// noise (criteria review, 0.5.0). So the microphone has to hear something the test made:
//
//  1. A loopback, automatic: the five notes of the Close Encounters of the Third Kind motif, D5
//     E5 C5 C4 G4 (micNotes), soft and faded in and out, one after another
//     in that order through the built-in speakers while it records, each found in the recording
//     with a Goertzel filter (noteHits: 10 dB over the frequencies around it, 10 dB over the room
//     at that frequency, over −75 dBFS and the loudest of the notes, in at least 8 windows of
//     50 ms). After the last note (so the tune plays whole), a control
//     round as long as a note with the speakers off: there the microphone must NOT hear any of
//     the notes (controlHits windows or more is "heard"). A note that keeps sounding, a stuck
//     capture buffer or a detector that answers yes whenever it listens fails the control, so
//     it can't pass on the notes. Headphones in the jack would take the
//     notes, so they have to come out first (as for the speakers test).
//  2. If the notes can't be played (no playback device on the built-in codec, the device
//     refused), a note isn't found, or the control round hears one: a clap challenge, "Clap N
//     times" (N random, 2 to 4), the claps counted as distinct peaks of the 10 ms envelope
//     (countClaps: clapOver dB over the room's peaks, clapGap apart). A wrong count gets one more
//     try with a new N.
//
//   - supported: every note found and the control round silent (method automatic), or the claps
//     counted right (challenge). The low C4 (262 Hz) alone may be missed: laptop speakers are
//     weak under about 300 Hz, so a MacBook Air may not play it at all; the evidence then says
//     "C4 not heard: too low for these speakers" (micRule). Two notes missed, any other note
//     missed, or a note that couldn't be played: the claps;
//   - partial: neither, but the level rose 6 dB over the room and over −60 dBFS: the microphone
//     hears something, not the notes or the claps (automatic);
//   - failed: no capture device on the built-in codec; the device stopped with an error; only
//     digital silence (every sample the same) the whole time (automatic); or the level didn't
//     rise and the person says they clapped and nothing moved (observed);
//   - not_tested (other): a fixture run, or no arecord on the image.
//
// The notes sit close to mains hum harmonics (E5 659 Hz is 11 × 60 Hz; G4 392 Hz, D5 587 Hz and
// E5 are within 13 Hz of 50 Hz ones): the room's own level at each note is measured first and a
// window must be 10 dB over it, and the control round catches a hum that rises later. The notes
// are 64 Hz and more apart (C5 and D5), outside the filter's main lobe (±40 Hz) but close enough
// that a strong note leaks into its neighbour, and C4 through small speakers comes out partly as
// C5 (its 2nd harmonic): a window hears only the loudest note (noteHits), so one note never
// reads as another.
//
// Left open: a codec that mixes its own output into the capture path (an analog loopback, or
// a "Stereo Mix" capture source) would hand the notes to a dead microphone. The Macs' Cirrus
// codecs have no such path, and the capture mixer is set to the microphone (captureMix).

func init() {
	Register(Spec{ID: "audio.microphone", Group: NoPlug, Order: 70, Title: i18n.M("Microphone"),
		Brief: i18n.M("stay quiet for five notes; clap if asked"), Expect: 30 * time.Second,
		New: func(*Env) Run { return &mic{best: -100} }, Demo: func(f *Fake) {
			demoSound(f)
			f.Set("alsa:loop", "-24") // the speakers reach the microphone
		}})
}

const (
	micSettle   = 0.3 // seconds of the recording thrown away (the converter's start-up pop)
	micFloor    = 1.8 // seconds measuring the room (settle included)
	micListen   = 8.0 // seconds to clap
	micFaint    = 6.0 // dB over the room: something heard
	toneTail    = 0.6 // seconds still listening after a note has played (the devices' buffers)
	clapQuiet   = 1.5 // seconds of quiet after the count is reached: the claps are over
	controlHits = toneHits / 2
)

// micNote is one of the notes the microphone test plays.
type micNote struct {
	name string // as the screen shows it: "C4"
	hz   float64
	low  bool // the one note that may go unheard (micRule): under what laptop speakers play well
}

// micN is how many notes the test plays.
const micN = 5

// micNotes are the Close Encounters of the Third Kind motif, in the order they play: D5, E5, C5,
// then C4 an octave lower, then G4.
var micNotes = [micN]micNote{{"D5", 587.33, false}, {"E5", 659.25, false}, {"C5", 523.25, false}, {"C4", 261.63, true}, {"G4", 392.00, false}}

// micFallback is the rule once neither the notes nor the claps came through: the room's level
// and the loudest window since, in dBFS, and whether anything but digital silence came in. ""
// means ask the person.
func micFallback(floor, best float64, sound bool) string {
	switch {
	case !sound:
		return checks.Failed
	case best-floor >= micFaint && best >= -60:
		return checks.Partial
	}
	return ""
}

type micPhase int

const (
	micRoom   micPhase = iota // measuring the room
	micUnplug                 // headphones in: the notes would go to them
	micTone                   // a note through the speakers (or the control round), listening
	micClap                   // clap N times
	micAsk                    // the claps' time is up and the level barely moved
)

// tone is one round of the loopback: a note played, or the control round (note -1: the speakers
// off), and what the recording showed of it.
type tone struct {
	note  int       // index into micNotes; -1 for the control round
	wins  int       // windows recorded while it played
	hits  int       // windows that heard the note (noteHits); the control round: of the note it heard most
	heard [micN]int // the control round: windows that heard each note
	over  []float64
	above []float64
	best  float64 // the best window's dB over the frequencies around it
	err   error   // it couldn't be played
	found bool    // the note was found; the control round: no note was heard
}

func (tn *tone) control() bool { return tn.note < 0 }

// controlOK is the control round's rule: no note heard in controlHits windows or more. A stuck
// detector (every window a hit) or a note still sounding can't pass it.
func controlOK(heard [micN]int) bool {
	for _, n := range heard {
		if n >= controlHits {
			return false
		}
	}
	return true
}

// evidence is the round's line in the evidence.
func (tn *tone) evidence(spk string) string {
	if tn.control() {
		s := fmt.Sprintf("control round (%.1f s, nothing played): ", leadAnalog+toneLen)
		var per []string
		for i, n := range micNotes {
			per = append(per, fmt.Sprintf("%s %d", n.name, tn.heard[i]))
		}
		if tn.found {
			return s + fmt.Sprintf("no note heard (windows hearing each: %s, of %d; %d would count)", strings.Join(per, ", "), tn.wins, controlHits)
		}
		return s + fmt.Sprintf("a note was heard with none playing (windows hearing each: %s, of %d; %d count): the notes can't count", strings.Join(per, ", "), tn.wins, controlHits)
	}
	n := micNotes[tn.note]
	if tn.err != nil {
		return fmt.Sprintf("note %s %.0f Hz through %s not played: %v", n.name, n.hz, spk, tn.err)
	}
	s := fmt.Sprintf("note %s %.0f Hz (%.1f s) through %s: ", n.name, n.hz, toneLen, spk)
	if tn.found {
		return s + fmt.Sprintf("found in %d of %d windows, median %+.0f dB over the frequencies around it and %+.0f dB over the room at %.0f Hz",
			tn.hits, tn.wins, medianOf(tn.over), medianOf(tn.above), n.hz)
	}
	return s + fmt.Sprintf("not found (%d of %d windows heard it, %d needed; best window %+.0f dB over the frequencies around it)",
		tn.hits, tn.wins, toneHits, tn.best)
}

type mic struct {
	snd  soundIO
	rng  *rand.Rand
	pcm  PCM
	desc string
	rec  Recorder

	spk     PCM // the speakers' device, for the notes
	spkOK   bool
	spkDesc string

	jacks    Future[map[string]bool]
	jack     string // the headphone jack control, "" when the card has none
	jackOn   bool
	jackRead bool
	out      bool // the person said the headphones are out
	unplug   bool // the headphones had to come out first
	polled   float64

	tones   []*tone // the rounds, in play order
	cur     int     // the round playing
	player  Player
	encoreP Player  // the motif's encore after a pass: not stopped by Stop (see encore)
	doneAt  float64 // when the round finished playing (-1 while it plays)

	phase    micPhase
	from     float64   // when the phase began
	room     []float64 // the room's windows, dBFS
	roomPk   []float64 // the room's 10 ms peaks, dBFS
	noteRoom [micN][]float64
	roomAt   [micN]float64 // the room's level at each note, dBFS (median)
	margin   [micN]float64 // the latest window: how clearly each note is heard, dB (for the meter)
	floor    float64
	roomPeak float64
	best     float64 // loudest window since the room was measured
	now      float64 // the latest window
	sound    bool    // anything but digital silence

	claps int       // claps asked for this try
	env   []float64 // this try's 10 ms peaks
	heard int       // claps counted this try
	tries int       // clap tries
	trail []string  // English, for the evidence: "asked for 3 claps, counted 3"
	why   string    // why the claps came (English, for the evidence), or ""
	say   string
	sayAt float64
	face  boot.Expr
}

func (m *mic) Start(c *Ctx) {
	snd, ok := c.IO.(soundIO)
	if !ok {
		c.Decide(Outcome{Results: []checks.Result{NotTested("audio.microphone", checks.Other, "no sound access in this mode", i18n.T("no sound here"))}})
		return
	}
	p, ok := builtInPCM(c.IO, true, "mic")
	if !ok {
		c.Decide(Outcome{Results: []checks.Result{Result("audio.microphone", checks.Failed, Automatic,
			"no capture device on the built-in codec in /proc/asound/pcm", i18n.T("no capture device"))},
			Say: i18n.T("the sound card has no way in. no microphone to hear.")})
		return
	}
	seed := snd.Seed()
	m.rng = rand.New(rand.NewPCG(seed, seed^0x5851f42d4c957f2d))
	m.tones = micRounds()
	m.snd, m.pcm, m.desc = snd, p, describe(c.IO, p)
	if m.spk, m.spkOK = builtInPCM(c.IO, false, "speaker"); m.spkOK {
		m.spkDesc = describe(c.IO, m.spk)
		m.jacks = snd.Jacks(m.spk.Card)
	} else {
		m.jackRead = true // no notes, so no jack to mind
	}
	m.rec = snd.Record(p)
	m.now, m.doneAt, m.face = -100, -1, boot.Work
	m.say, m.sayAt = i18n.T("shh. measuring the room…"), 0.2
	c.Select(-1) // hands off: a stray Enter mustn't skip
}

// micRounds are the loopback's rounds: the notes in the motif's order, then the control round
// (it follows a note, so one still sounding shows; after the last, the tune plays whole).
func micRounds() []*tone {
	var out []*tone
	for i := range micNotes {
		out = append(out, &tone{note: i, best: -100})
	}
	return append(out, &tone{note: -1, best: -100})
}

// What the loopback's rounds so far come to (micRule).
const (
	loopGoing = iota // on to the next round
	loopPass         // every round played and the rule met: supported
	loopClaps        // a note missed (or the pause heard one): claps instead
)

// micRule is the loopback's rule over the rounds played so far (each decided), of total: every
// note found and the control round quiet. The low C4 alone may be missed, and only when it
// played and simply wasn't heard (laptop speakers are weak under about 300 Hz). Any other note
// missed, a second miss, a note that couldn't be played, the control round
// hearing a note, or no control round at all: the claps. The control round keeps its guard
// whichever notes were heard, so the exception can't let a stuck or noisy detector pass.
func micRule(played []*tone, total int) int {
	missed, control := 0, false
	for _, tn := range played {
		switch {
		case tn.control() && !tn.found:
			return loopClaps
		case tn.control():
			control = true
		case tn.found:
		case tn.err != nil || !micNotes[tn.note].low:
			return loopClaps
		default:
			missed++
		}
	}
	switch {
	case missed > 1:
		return loopClaps
	case len(played) < total:
		return loopGoing
	case !control:
		return loopClaps
	}
	return loopPass
}

// lowMissed is the low note's round when it played and wasn't heard, or nil.
func (m *mic) lowMissed() *tone {
	for _, tn := range m.tones[:min(m.cur+1, len(m.tones))] {
		if !tn.control() && micNotes[tn.note].low && !tn.found && tn.wins > 0 {
			return tn
		}
	}
	return nil
}

func (m *mic) Step(c *Ctx) {
	if m.rec == nil {
		return
	}
	if err := m.rec.Err(); err != nil {
		m.failed(c, err)
		return
	}
	if m.jacks != nil {
		if js, ok := m.jacks.Ready(); ok {
			m.jacks = nil
			m.jack, m.jackOn = headphoneJack(js)
			m.jackRead = true
		}
	}
	t := c.T
	for _, w := range m.rec.Windows() {
		m.window(t, w)
	}
	switch m.phase {
	case micRoom:
		if t-m.from >= micFloor && len(m.room) > 0 && m.jackRead {
			m.floor, m.roomPeak = medianOf(m.room), medianOf(m.roomPk)
			for i := range micNotes {
				m.roomAt[i] = medianOf(m.noteRoom[i])
			}
			switch {
			case !m.spkOK:
				m.startClaps(c, "", "no playback device on the built-in codec: no notes played")
			case m.jackOn:
				m.phase, m.from, m.unplug = micUnplug, t, true
				m.say, m.sayAt, m.face = i18n.T("headphones are in: the notes would go to them."), t, boot.Idle
			default:
				m.startTone(c, 0)
			}
		}
	case micUnplug:
		if !m.jackOn || m.out {
			m.startTone(c, 0)
		} else if m.jacks == nil && t-m.polled >= 0.5 {
			m.polled, m.jacks = t, m.snd.Jacks(m.spk.Card)
		}
	case micTone:
		tn := m.tones[m.cur]
		switch {
		case m.doneAt >= 0:
			if t-m.doneAt >= toneTail {
				m.toneOver(c)
			}
		case tn.control(): // nothing plays: it lasts as long as a note would
			if t-m.from >= leadAnalog+toneLen {
				m.doneAt = t
			}
		default:
			if err, done := m.player.Done(); done {
				m.doneAt, tn.err = t, err
			}
		}
	case micClap:
		var last int
		m.heard, last = countClaps(m.env, clapThreshold(m.roomPeak))
		quiet := last >= 0 && float64(len(m.env)-last)/100 >= clapQuiet
		if m.heard >= m.claps && quiet || t-m.from >= micListen {
			m.clapsOver(c)
		}
	}
}

// window takes in one recorded window.
func (m *mic) window(t float64, w Window) {
	d := dB(w.RMS)
	if w.Flat {
		d = -100
	} else {
		m.sound = true
	}
	m.now = d
	var at, around [micN]float64
	for i, n := range micNotes {
		at[i], around[i] = toneLevel(w.S, n.hz)
		if m.phase != micRoom {
			m.margin[i] = min(at[i]-around[i], at[i]-m.roomAt[i])
		}
	}
	hit := noteHits(at, around, m.roomAt)
	switch m.phase {
	case micRoom:
		if t-m.from >= micSettle {
			m.room = append(m.room, d)
			m.roomPk = append(m.roomPk, frames(w)...)
			for i := range micNotes {
				m.noteRoom[i] = append(m.noteRoom[i], at[i])
			}
		}
	case micTone:
		m.best = max(m.best, d)
		tn := m.tones[m.cur]
		tn.wins++
		if tn.control() {
			for i := range micNotes {
				if hit[i] {
					tn.heard[i]++
					tn.hits = max(tn.hits, tn.heard[i])
				}
			}
			return
		}
		i := tn.note
		tn.best = max(tn.best, at[i]-around[i])
		if hit[i] {
			tn.hits++
			tn.over, tn.above = append(tn.over, at[i]-around[i]), append(tn.above, at[i]-m.roomAt[i])
		}
	case micClap:
		m.best = max(m.best, d)
		m.env = append(m.env, frames(w)...)
	case micAsk:
		m.best = max(m.best, d)
	}
}

// startTone starts the i-th round: a note through the speakers, or the control round (nothing).
func (m *mic) startTone(c *Ctx, i int) {
	m.cur, m.phase, m.from, m.doneAt = i, micTone, c.T, -1
	m.stopPlayer()
	tn := m.tones[i]
	m.sayAt, m.face = c.T, boot.Work
	if tn.control() {
		m.say = i18n.T("and now nothing plays. still listening…")
		return
	}
	m.player = m.snd.Play(m.spk, toneWAV(micNotes[tn.note].hz, leadAnalog))
	m.say = i18n.T("shh. a note from the speakers…")
	if i > 0 {
		m.say = i18n.T("next note…")
	}
}

// toneOver decides on the round that played: the next one, the pass, or the claps.
func (m *mic) toneOver(c *Ctx) {
	tn := m.tones[m.cur]
	if tn.control() {
		tn.found = controlOK(tn.heard)
	} else {
		tn.found = tn.err == nil && tn.hits >= toneHits
	}
	m.stopPlayer()
	low := m.lowMissed()
	switch micRule(m.tones[:m.cur+1], len(m.tones)) {
	case loopGoing:
		m.startTone(c, m.cur+1)
		if tn == low {
			m.say = i18n.T("no low C. too low for these speakers, maybe. next note…")
		}
	case loopPass:
		if low != nil {
			m.end(c, Outcome{Results: []checks.Result{Result("audio.microphone", checks.Supported, Automatic,
				m.evidence()+"; C4 not heard: too low for these speakers (the one note allowed to go unheard)",
				i18n.T("heard 4 of 5 notes (C4 too low)"))},
				Say: i18n.T("four of five, and a quiet pause. the low C is past these speakers. the microphone works.")})
			m.encore()
			return
		}
		m.end(c, Outcome{Results: []checks.Result{Result("audio.microphone", checks.Supported, Automatic, m.evidence(),
			i18n.T("heard all 5 notes, and the silence"))},
			Say: i18n.T("five notes in, and quiet when nothing played. the microphone works.")})
		m.encore()
	case loopClaps:
		if tn.control() {
			m.startClaps(c, i18n.T("I heard a note when none played. can't trust that: claps instead."),
				"the control round heard a note with the speakers off")
			return
		}
		why := fmt.Sprintf("note %s not found", micNotes[tn.note].name)
		if low != nil && low != tn {
			why += fmt.Sprintf(", after %s wasn't either", micNotes[low.note].name)
		}
		m.startClaps(c, i18n.T("that note didn't come through. claps instead."), why)
	}
}

// startClaps asks for a new number of claps (2 to 4, not the last one asked).
func (m *mic) startClaps(c *Ctx, say, why string) {
	n := m.claps
	for n == m.claps {
		n = 2 + m.rng.IntN(3)
	}
	m.claps, m.env, m.heard, m.phase, m.from = n, nil, 0, micClap, c.T
	m.tries++
	if why != "" {
		m.why = why
	}
	if say == "" {
		say = i18n.T("clap %d times for me.", n)
	}
	m.say, m.sayAt, m.face = say, c.T, boot.Idle
	c.Select(-1) // nothing to pick while the claps are counted
}

// clapsOver decides on a try's claps.
func (m *mic) clapsOver(c *Ctx) {
	m.trail = append(m.trail, fmt.Sprintf("asked for %d claps, counted %d", m.claps, m.heard))
	switch {
	case m.heard == m.claps:
		m.end(c, Outcome{Results: []checks.Result{Result("audio.microphone", checks.Supported, Challenge, m.evidence(),
			i18n.T("counted your %d claps", m.claps))},
			Say: i18n.T("%d claps, and I counted %d. the microphone works.", m.claps, m.heard)})
		return
	case m.tries < 2:
		m.startClaps(c, "", "")
		m.say = i18n.T("I counted %d. once more: clap %d times.", m.heard, m.claps)
		return
	}
	switch micFallback(m.floor, m.best, m.sound) {
	case checks.Failed:
		m.end(c, Outcome{Results: []checks.Result{Result("audio.microphone", checks.Failed, Automatic, m.evidence(), i18n.T("digital silence only"))},
			Say: i18n.T("flat zeros the whole time. nothing gets through.")})
	case checks.Partial:
		m.end(c, Outcome{Results: []checks.Result{Result("audio.microphone", checks.Partial, Automatic, m.evidence(),
			i18n.T("heard something, not the notes or the claps"))},
			Say: i18n.T("something came through, but not the notes or the claps. partial.")})
	default:
		m.phase, m.from = micAsk, c.T
		m.say, m.sayAt, m.face = i18n.T("hmm. the level barely moved."), c.T, boot.Hmm
		c.Select(0) // Clap again
	}
}

func (m *mic) evidence() string {
	ev := fmt.Sprintf("recorded %s at 16 kHz mono; room %.0f dBFS (median of %d windows of 50 ms)", m.desc, m.floor, len(m.room))
	if !m.spkOK {
		ev += "; no playback device on the built-in codec: no notes played"
	}
	if m.out && m.jackOn {
		ev += fmt.Sprintf("; the tester said the headphones were out while \"%s\" still reported them", m.jack)
	}
	for _, tn := range m.tones {
		if tn.wins > 0 || tn.err != nil {
			ev += "; " + tn.evidence(m.spkDesc)
		}
	}
	if len(m.trail) > 0 {
		if m.why != "" {
			ev += "; claps because " + m.why
		}
		ev += fmt.Sprintf("; claps (10 ms peaks over %.0f dBFS, %d ms apart at least): %s",
			clapThreshold(m.roomPeak), clapGap*10, strings.Join(m.trail, "; "))
	}
	ev += fmt.Sprintf("; loudest window since %.0f dBFS (%+.0f dB)", m.best, m.best-m.floor)
	if !m.sound {
		ev += "; every sample was the same (digital silence)"
	}
	return ev
}

func (m *mic) end(c *Ctx, o Outcome) {
	m.stop()
	c.Decide(o)
}

// encore plays the motif once more at the film's pace, after a pass (the recording has
// stopped). It has its own player, which Stop leaves alone: the screen stops a run the moment it
// decides, and the encore comes right after that (0.6.2 played nothing). It lasts about 4 s.
func (m *mic) encore() {
	if m.snd != nil {
		m.encoreP = m.snd.Play(m.spk, motifWAV(leadAnalog))
	}
}

func (m *mic) failed(c *Ctx, err error) {
	var o Outcome
	switch err {
	case ErrNotReal:
		o = Outcome{Results: []checks.Result{NotTested("audio.microphone", checks.Other, "fixture run: nothing is recorded", i18n.T("nothing recorded (not on a Mac)"))},
			Say: i18n.T("not on a real Mac, so nothing to hear. skipped.")}
	case ErrNoTool:
		o = Outcome{Results: []checks.Result{NotTested("audio.microphone", checks.Other, "alsa-utils isn't on this image: nothing could be recorded", i18n.T("no arecord on this image"))},
			Say: i18n.T("no arecord on this image. that's on me, not the Mac.")}
	default:
		o = Outcome{Results: []checks.Result{Result("audio.microphone", checks.Failed, Automatic, m.desc+": recording failed: "+err.Error(), i18n.T("recording failed"))},
			Say: i18n.T("the sound card wouldn't record. that's a fail.")}
	}
	m.end(c, o)
}

func (m *mic) Act(c *Ctx, i int) {
	switch {
	case m.phase == micUnplug && i == 0: // "They're out": the jack control may be stuck
		m.out = true
	case m.phase == micAsk && i == 0:
		m.startClaps(c, "", "")
	case m.phase == micAsk && i == 1:
		m.end(c, Outcome{Results: []checks.Result{Result("audio.microphone", checks.Failed, Observed,
			m.evidence()+"; the tester clapped and the level didn't rise", i18n.T("the level didn't rise"))},
			Say: i18n.T("you clapped and the level didn't move. that's a fail.")})
	}
}

func (m *mic) stopPlayer() {
	if m.player != nil {
		m.player.Stop()
		m.player = nil
	}
}

func (m *mic) stop() {
	m.stopPlayer()
	if m.rec != nil {
		m.rec.Stop()
	}
}

func (m *mic) Stop(*Ctx) { m.stop() }

// Leave stops the encore, if it's still playing, once the screen is left.
func (m *mic) Leave(*Ctx) {
	if m.encoreP != nil {
		m.encoreP.Stop()
		m.encoreP = nil
	}
}

// steps are "What to do": stay quiet, unplug the headphones (when they were in), keep quiet
// through the notes (when there are speakers), clap (once it comes to that); and the current one.
func (m *mic) steps() ([]string, int) {
	s := []string{i18n.T("Stay quiet near the Mac.")}
	cur := 0
	if m.unplug {
		s = append(s, i18n.T("Unplug the headphones."))
		if m.phase == micUnplug {
			cur = len(s) - 1
		}
	}
	if m.spkOK {
		s = append(s, i18n.T("Keep quiet while the notes play."))
		if m.phase == micTone {
			cur = len(s) - 1
		}
	}
	if m.tries > 0 {
		s = append(s, i18n.N("Clap %d time, near the keyboard.", "Clap %d times, near the keyboard.", m.claps, m.claps))
		cur = len(s) - 1
	}
	return s, cur
}

func (m *mic) View(c *Ctx) Screen {
	s := Screen{Face: m.face, OutTitle: i18n.T("What I hear"), Say: m.say, SayAt: m.sayAt}
	if m.rec == nil {
		return s
	}
	s.Steps, s.Step = m.steps()
	s.Happens = i18n.T("Five soft notes, D E C C G, play through the speakers, then a pause where nothing plays. I must hear each note (the low C may be too low for laptop speakers) and nothing in the pause. If not, you clap instead.")
	if !m.spkOK {
		s.Happens = i18n.T("No speakers to play notes through, so I count your claps instead.")
	}
	s.Live = []Line{Fact(i18n.T("%s: %s, 16 kHz", m.pcm.HW(), m.pcm.Name))}
	switch m.phase {
	case micRoom:
		s.Waiting = "·  " + i18n.T("measuring the room")
		s.Live = append(s.Live, Wait(i18n.T("measuring the room")))
	case micUnplug:
		s.Waiting = "·  " + i18n.T("waiting for the jack to empty")
		s.Hint = i18n.T("nothing in the jack? it may be stuck: They're out goes on anyway.")
		s.Live = append(s.Live, m.roomLine(), Fact(i18n.T("%s: plugged in", m.jack)), Wait(i18n.T("waiting for the jack to empty")))
		s.Actions = []Action{{Label: i18n.T("They're out"), Does: i18n.T("nothing is plugged in; go on")}}
	case micTone:
		s.Waiting = "·  " + i18n.T("listening")
		s.Hint = i18n.T("voices or a loud fan can drown the notes. a quiet moment helps.")
		s.Live = append(s.Live, m.roomLine())
		s.Live = append(s.Live, m.toneLines(m.cur+1)...)
	case micClap:
		s.Waiting = "·  " + i18n.T("counting your claps")
		s.Hint = i18n.T("short, sharp claps a moment apart work best.")
		s.Live = append(s.Live, m.roomLine())
		s.Live = append(s.Live, m.toneLines(m.cur+1)...)
		s.Live = append(s.Live, Fact(i18n.N("%d clap heard", "%d claps heard", m.heard, m.heard)),
			Wait(i18n.T("listening, %d s left", max(0, int(micListen-(c.T-m.from)+0.99)))))
	case micAsk:
		s.Waiting = "·  " + i18n.T("waiting for your answer")
		s.Hint = i18n.T("louder, or closer to the keyboard: the microphone sits near it on most Macs.")
		s.Live = append(s.Live, m.roomLine())
		s.Live = append(s.Live, m.toneLines(m.cur+1)...)
		s.Live = append(s.Live, Fact(i18n.T("loudest: %.0f dB (%+.0f dB)", m.best, m.best-m.floor)))
		s.Actions = []Action{{Label: i18n.T("Clap again"), Does: i18n.T("a new count, %.0f more seconds", micListen)},
			{Label: i18n.T("Nothing moved"), Does: i18n.T("I clapped, and the level stayed put")}}
	}
	s.Anim = m.anim()
	if m.phase == micUnplug { // take the headphones out: the plug coming out
		s.Anim = headphonesArt(sideBoth, false, "in", false, hpUnplug)
	}
	return s
}

// toneLines are the first n rounds under "What I hear" (none before the notes began).
func (m *mic) toneLines(n int) []Line {
	if !m.spkOK || m.phase == micRoom || m.phase == micUnplug || len(m.tones) == 0 {
		if !m.spkOK {
			return []Line{Gone(i18n.T("no speakers for the notes"))}
		}
		return nil
	}
	var out []Line
	for i, tn := range m.tones[:min(n, len(m.tones))] {
		over := i < m.cur || m.phase != micTone
		switch {
		case tn.control() && !over:
			out = append(out, Wait(i18n.T("pause: nothing plays, listening")))
		case tn.control() && tn.found:
			out = append(out, Fact(i18n.T("pause: nothing heard, as it should be")))
		case tn.control():
			out = append(out, Bad(i18n.T("pause: heard a note with none playing")))
		case tn.found:
			out = append(out, Fact(i18n.T("note %s (%.0f Hz): heard", micNotes[tn.note].name, micNotes[tn.note].hz)))
		case !over && i == m.cur:
			out = append(out, Wait(i18n.T("note %s (%.0f Hz): listening", micNotes[tn.note].name, micNotes[tn.note].hz)))
		default:
			out = append(out, Gone(i18n.T("note %s (%.0f Hz): not heard", micNotes[tn.note].name, micNotes[tn.note].hz)))
		}
	}
	return out
}

// roomLine is the room's level under "What I hear".
func (m *mic) roomLine() Line {
	if m.floor <= -99 {
		return Gone(i18n.T("room: digital silence (every sample zero)"))
	}
	return Fact(i18n.T("room: %.0f dB", m.floor))
}

// headphoneJack is a card's headphone jack control (the first by name) and whether it reports
// something plugged in; "" when the card has none. A phantom jack ("Headphone Phantom Jack":
// the driver's stand-in for a jack the codec can't sense) always reads plugged in, so it isn't
// one (the iMac18,1's and iMac18,3's CS8409, 0.8.7 dev: headphones "in" with none there, before
// every sound test); phantomJack names it for the evidence.
func headphoneJack(js map[string]bool) (name string, on bool) {
	for n, o := range js {
		if strings.Contains(n, "Headphone") && !strings.Contains(n, "Phantom") && (name == "" || n < name) {
			name, on = n, o
		}
	}
	return name, on
}

// phantomJack is a card's phantom headphone jack control, or "".
func phantomJack(js map[string]bool) string {
	name := ""
	for n := range js {
		if strings.Contains(n, "Headphone") && strings.Contains(n, "Phantom") && (name == "" || n < name) {
			name = n
		}
	}
	return name
}

// micBarStep is a note bar's step in the animation: 4 wide, 2 apart.
const micBarStep = 6

// anim is the band's picture: the microphone, its level now on a −70…0 dB bar with the room
// marked, and one bar per note for how clearly it's heard (the line across them is where a
// window counts), the note playing in violet; in the pause, the bars should stay down.
func (m *mic) anim() Panel {
	now, floor, margin := m.now, m.floor, m.margin
	phase, measured := m.phase, m.phase != micRoom
	playing, control := -1, false
	var found [micN]bool
	for i, tn := range m.tones {
		if tn.control() {
			if i == m.cur && phase == micTone {
				control = true
			}
			continue
		}
		found[tn.note] = tn.found
		if i == m.cur && phase == micTone && m.doneAt < 0 {
			playing = tn.note
		}
	}
	notes := m.spkOK
	return func(gr *g.Grid, x, y, w, h int, t float64) {
		top := y + max(0, (h-10)/2)
		micArt(gr, x+2, top+1, now > floor+micFaint && measured)
		gr.Put(x+3, top+8, i18n.T("mic"), g.DIM)
		// the level now, the room under it
		lx := x + 13
		bw := min(40, x+w-lx-16)
		if bw < 8 {
			return
		}
		at := func(d float64) int { return int((min(0, max(-70, d)) + 70) / 70 * float64(bw)) }
		k := at(now)
		gr.Put(lx, top, i18n.T("level"), g.DIM)
		bx := lx + 7
		for i := 0; i < bw; i++ {
			if i < k {
				gr.SetBG(bx+i, top, '█', g.BVIO, g.K)
			} else {
				gr.SetBG(bx+i, top, '░', g.DIM, g.K)
			}
		}
		gr.Put(bx+bw+1, top, i18n.T("%.0f dB", now), g.GRY)
		if measured {
			if f := at(floor); f < bw {
				gr.Put(bx+f, top+1, "└ "+i18n.T("room"), g.DIM)
			}
		}
		if !notes {
			return
		}
		// the notes: one bar each, in the order they play, labelled with their octave, 0 to 30 dB
		// clearer than the frequencies around them and the room; a window counts over the line
		// (10 dB). Under the level bar, from its label, so the five fit at 120 columns.
		bt, bh := top+3, 5
		thr := bh - int(float64(bh)*toneOverAround/30+0.5)
		for i, n := range micNotes {
			cx := lx + i*micBarStep
			v := 0.0
			if measured {
				v = max(0, min(30, margin[i]))
			}
			fill := int(v/30*float64(bh) + 0.5)
			c := g.VIO
			if i == playing {
				c = g.BVIO
			}
			for j := 0; j < bh; j++ {
				switch {
				case bh-j <= fill:
					gr.Put(cx, bt+j, "████", c)
				case j == thr:
					gr.Put(cx, bt+j, "────", g.DIM)
				}
			}
			lc := g.DIM
			switch {
			case i == playing:
				lc = g.BVIO
			case found[i]:
				lc = g.WHT
			}
			gr.Put(cx+1, bt+bh, n.name, lc)
		}
		// what it listens for now, beside the bars
		sx := lx + micN*micBarStep + 1
		var what string
		switch {
		case phase == micRoom:
			what = i18n.T("the room first")
		case control:
			what = i18n.T("nothing plays: no bar may cross the line")
		case playing >= 0:
			what = i18n.T("playing %s", micNotes[playing].name)
		case phase == micClap || phase == micAsk:
			what = i18n.T("counting claps")
		}
		sndText(gr, sx, bt, x+w-sx-1, bh, what, g.GRY)
	}
}
