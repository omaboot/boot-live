package handson

import (
	"fmt"
	"math/rand/v2"
	"slices"
	"strings"
	"time"

	audioclips "github.com/omaboot/boot-live/assets/audio"
	"github.com/omaboot/boot-live/internal/boot"
	"github.com/omaboot/boot-live/internal/checks"
	"github.com/omaboot/boot-live/internal/i18n"
	"github.com/omaboot/boot-live/internal/identify"
)

// The sound tests where the person listens: the built-in speakers, the headphone jack, and
// sound over HDMI / DisplayPort. The machine plays something only a working output can carry
// and the person answers what they heard (method challenge, LIVE-PLAN §21.3 and §11's step 3
// choices): a count of beeps on one side at a time (2 to 5, generated, language-free) and, for
// the speakers, a spoken word picked out of four clearly different ones (assets/audio, Piper's
// Amy voice). A wrong answer gets one retry with a new count or word; a second miss is a missed
// challenge. "I hear nothing" is the person's observation that a side is silent. The beeps are
// a 700 Hz tone (the usual Morse practice pitch); the ▶ cursor goes to "Play it again" each time
// a sound starts, and the current step says which side they're about.
//
// audio.speakers, "Built-in speakers" (one result; owner, 2026-10-11):
//
//   - supported: both sides' beeps counted right (method observed); the word picked right too
//     makes it a challenge; skipping the word leaves it supported (observed);
//   - partial: one side silent and the other counted right (or missed), or both sides right but
//     the word missed twice or not heard: the speakers play, speech doesn't come through;
//   - failed: nothing heard on either side (observed), or no playback device at all on the
//     built-in codec (automatic);
//   - not_tested (uncertain): a side's count missed twice with neither side silent.
//
// A Mac with one built-in speaker (identify.OneSpeaker: Mac mini, Mac Pro, the 2008 Airs) gets
// the same rounds, both sides through its one speaker, and the same rule: a side it doesn't
// play is sound lost (owner's Macmini4,1, 0.8.5 dev: the right side unheard).
//
// audio.headphone, "Headphone / analog audio output": the person plugs headphones in and puts
// them on; Ready comes once the jack control ("Headphone Jack", which is what PipeWire switches
// outputs by) reports them, as otherwise the beeps would come out of the speakers (owner,
// 2026-10-12). A card with no jack control gets Ready straight away. Then the same two counts in
// the headphones, quieter. With both counted right, 3 beeps play in both ears (by themselves,
// owner, 2026-10-15) and one question follows: did the speakers stay quiet while it beeped in
// the headphones? Macs' Cirrus codecs are known to keep the speakers playing with headphones in
// (criteria review, 0.5.0). The counts stay the proof that the headphones play; the answer only
// keeps a pass from hiding the speakers playing too. While the headphones play, the speaker
// pin's state and the speaker switches are read for the evidence (speakerState).
//
//   - supported: the jack reported the headphones, both sides were counted right, and the
//     speakers stayed quiet;
//   - partial: the speakers played too (observed); one side silent; or both sides right but
//     the jack never reported them (no jack control: sound comes out, but an installed system
//     won't switch by itself);
//   - failed: nothing heard on either side (observed); the person says they're plugged in and
//     the jack still hasn't noticed after hpWait, nothing played (observed); or no playback
//     device (automatic);
//   - not_tested (uncertain): a side missed twice with neither side silent.
//
// audio.display-out, "Audio over HDMI / DisplayPort": right after the external display test,
// with the monitor still plugged in. The HDMI/DP codec should get the monitor's audio
// description (its ELD); the beeps play through the HDMI/DP device whose jack control says
// plugged in (or, when no jack control tells, through all of them at once: only the connected
// one makes a sound).
//
//   - supported: a sink with a valid ELD and the count heard right through it;
//   - partial: counted right, but the monitor's ELD never arrived (the tester chose "Play
//     anyway"): sound plays, but an installed system's PipeWire hides outputs without one;
//   - failed: nothing heard from the monitor (observed), or no HDMI/DP audio device (automatic);
//   - not_tested (uncertain): the count missed twice.
//
// On a fixture tree nothing is played: the tests end not_tested (other). Without alsa-utils on
// the image likewise. Whatever way the screen is left, the sound stops and the mixer is put
// back as it was (audio_alsa.go).

func init() {
	Register(Spec{ID: "audio.speakers", Group: NoPlug, Order: 60, Title: i18n.M("Speakers"),
		Brief: i18n.M("count beeps on each side, then pick the word you hear"), Expect: time.Minute,
		New: newListen(outSpeakers), Demo: demoSound})
	Register(Spec{ID: "audio.display-out", Group: Plug, Order: 25, Title: i18n.M("Monitor speakers"),
		Brief: i18n.M("count beeps from the monitor's speakers"), Gear: i18n.M("a monitor or TV with speakers"), Expect: 30 * time.Second,
		New: newListen(outDisplay), Demo: func(f *Fake) {
			demoSound(f)
			d := "/sys/class/drm/card1-DP-1/"
			f.Set(d+"status", "connected")
			f.Set(d+"enabled", "enabled")
			f.Set(d+"modes", "1920x1080\n1280x720")
			f.Set(d+"edid", DemoEDID("LG TV"))
			f.At(1.5, func(f *Fake) { // the ELD arrives a moment after the picture
				f.Set("/proc/asound/card0/eld#3.0", "monitor_present\t\t1\neld_valid\t\t1\nmonitor_name\t\tLG TV\nconnection_type\t\tDisplayPort")
				f.Set("alsa:card0:HDMI/DP,pcm=3 Jack", "on")
			})
		}})
	Register(Spec{ID: "audio.headphone", Group: Plug, Order: 30, Title: i18n.M("Headphones"),
		Brief: i18n.M("plug in headphones, count the beeps"), Gear: i18n.M("headphones"), Expect: time.Minute,
		New: newListen(outHeadphones), Demo: func(f *Fake) {
			demoSound(f)
			f.At(3, func(f *Fake) { // plugged in: the codec's auto-mute turns the speaker pin off
				f.Set("alsa:card0:Headphone Jack", "on")
				f.Set("/proc/asound/card0/codec#0", demoCodec("0x00"))
			})
		}})
}

type outKind int

const (
	outSpeakers outKind = iota
	outHeadphones
	outDisplay
)

func (k outKind) id() string {
	return [...]string{"audio.speakers", "audio.headphone", "audio.display-out"}[k]
}

// How a round ended.
const (
	heardOK     = "ok"      // counted (or picked) right, first or second try
	heardMissed = "missed"  // wrong twice
	heardNone   = "silent"  // "I hear nothing"
	heardSkip   = "skipped" // the speakers' word skipped: the beeps decide
	// the headphones' last question: did the speakers stay quiet?
	speakersQuiet  = "quiet"
	speakersPlayed = "played"
)

// listenRound is one thing to listen for: beeps on a side, or the word.
type listenRound struct {
	side    int
	word    bool
	quiet   bool     // the headphones' question: did the speakers stay quiet? (Play it again: 3 beeps in both ears)
	n       int      // beeps this try
	answer  string   // the word this try
	choices []string // the four words offered
	tries   int
	result  string
	trail   []string // English, for the evidence: "3 beeps, answered 3"
}

// confusable are pairs of words too alike to offer together.
var confusable = [][2]string{{"radio", "video"}, {"hello", "hotel"}, {"radio", "robot"}}

func alike(a, b string) bool {
	for _, p := range confusable {
		if p[0] == a && p[1] == b || p[0] == b && p[1] == a {
			return true
		}
	}
	return false
}

// pickWord picks a word (not one of avoid) and four clearly different choices, in random order.
func pickWord(rng *rand.Rand, words, avoid []string) (string, []string) {
	var pool []string
	for _, w := range words {
		if !slices.Contains(avoid, w) {
			pool = append(pool, w)
		}
	}
	if len(pool) == 0 {
		pool = words
	}
	answer := pool[rng.IntN(len(pool))]
	choices := []string{answer}
	for _, i := range rng.Perm(len(words)) {
		w := words[i]
		if len(choices) == 4 {
			break
		}
		ok := !slices.Contains(choices, w)
		for _, c := range choices {
			ok = ok && !alike(c, w)
		}
		if ok {
			choices = append(choices, w)
		}
	}
	rng.Shuffle(len(choices), func(i, j int) { choices[i], choices[j] = choices[j], choices[i] })
	return answer, choices
}

// newCount is a beep count from 2 to 5 other than not.
func newCount(rng *rand.Rand, not ...int) int {
	for {
		if n := 2 + rng.IntN(4); !slices.Contains(not, n) {
			return n
		}
	}
}

type listen struct {
	kind outKind
	mono bool // the speakers of a Mac with one (identify.OneSpeaker): both sides play through it
	snd  soundIO
	rng  *rand.Rand

	found   bool   // there is something to play through
	gone    string // what's missing when there isn't
	out     PCM    // the analog output (speakers, headphones)
	outDesc string
	targets []PCM // display-out: the HDMI/DP devices to play through
	words   []string

	// before the rounds: headphones out (speakers), in (headphones), a sink (display-out)
	ready    bool
	jacks    Future[map[string]bool]
	jack     string // the jack control's name, "" when the card has none
	phantom  string // a phantom headphone jack (no plug detection), when that's all the card has
	jackOn   bool
	jackSeen bool // the jack reported headphones at some point
	jackRead bool // the jack controls have been read once (jack is known)
	sink     *ELD
	anyway   bool // the person said "Ready" (headphones), "Play anyway" (no ELD) or "They're out"
	unplug   bool // speakers: headphones were in, and had to come out first
	polled   float64
	setups   []string // how the device took the sounds (Player.Setup), each once, for the evidence
	spkMix   Future[map[string]string]
	spkState string // headphones: the speakers' pin and switches while the headphones played

	rounds  []*listenRound
	players []Player
	playing bool
	nextAt  float64 // when the next play starts (-1: none waiting)
	say     string
	sayAt   float64
	face    boot.Expr
}

func newListen(k outKind) func(*Env) Run {
	return func(e *Env) Run {
		l := &listen{kind: k, nextAt: -1}
		l.mono = k == outSpeakers && e != nil && e.Checks != nil && identify.OneSpeaker(e.Checks.Ident.Identifier)
		return l
	}
}

func (l *listen) decide(c *Ctx, r checks.Result, say string) {
	l.stopSound()
	c.Decide(Outcome{Results: []checks.Result{r}, Say: say})
}

func (l *listen) Start(c *Ctx) {
	id := l.kind.id()
	snd, ok := c.IO.(soundIO)
	if !ok {
		l.decide(c, NotTested(id, checks.Other, "no sound access in this mode", i18n.T("no sound here")), "")
		return
	}
	l.snd = snd
	seed := snd.Seed()
	l.rng = rand.New(rand.NewPCG(seed, seed^0x9e3779b97f4a7c15))
	l.words = audioclips.Words()
	switch l.kind {
	case outDisplay:
		for _, p := range pcms(c.IO) {
			if p.HDMI() && p.Play {
				l.targets = append(l.targets, p)
			}
		}
		if len(l.targets) == 0 {
			l.gone = i18n.T("no HDMI/DP playback device in /proc/asound/pcm")
			l.decide(c, Result(id, checks.Failed, Automatic, "no HDMI/DisplayPort playback device in /proc/asound/pcm", i18n.T("no HDMI/DP sound device")),
				i18n.T("no HDMI or DisplayPort sound device. nothing to send sound to."))
			return
		}
	default:
		prefer := "speaker"
		if l.kind == outHeadphones {
			prefer = "headphone"
		}
		p, ok := builtInPCM(c.IO, false, prefer)
		if !ok {
			l.gone = i18n.T("no playback device on the built-in codec")
			l.decide(c, Result(id, checks.Failed, Automatic, "no playback device on the built-in codec in /proc/asound/pcm", i18n.T("no playback device")),
				i18n.T("the sound card has no playback device. nothing to play through."))
			return
		}
		l.out, l.outDesc = p, describe(c.IO, p)
		l.jacks = snd.Jacks(p.Card)
	}
	l.found = true
	if l.kind == outSpeakers {
		c.Select(-1) // nothing to answer until the first beeps: a stray Enter mustn't skip
	}
}

func (l *listen) Step(c *Ctx) {
	if l.jacks != nil {
		if js, ok := l.jacks.Ready(); ok {
			l.jacks = nil
			l.readJacks(js)
		}
	}
	if !l.ready {
		l.waitReady(c)
		return
	}
	if l.spkMix != nil {
		if mix, ok := l.spkMix.Ready(); ok {
			l.spkMix, l.spkState = nil, speakerState(c.IO, l.out.Card, mix)
		}
	}
	if l.kind == outHeadphones && !l.jackSeen && l.jacks == nil && c.T-l.polled >= 0.5 { // it may see them late
		l.polled = c.T
		l.jacks = l.snd.Jacks(l.out.Card)
	}
	if l.playing {
		if err, done := l.played(); done {
			l.playing = false
			for _, p := range l.players {
				if su := p.Setup(); su != "" && !slices.Contains(l.setups, su) {
					l.setups = append(l.setups, su)
				}
			}
			if err != nil {
				l.playFailed(c, err)
				return
			}
			l.say, l.sayAt, l.face = l.question(), c.T, boot.Idle
		}
		return
	}
	if l.nextAt >= 0 && c.T >= l.nextAt {
		l.nextAt = -1
		l.play(c)
	}
}

// readJacks takes in the jack controls: the headphone jack (speakers, headphones), or the
// HDMI/DP jacks (display-out: which of the devices has the monitor).
func (l *listen) readJacks(js map[string]bool) {
	if l.kind == outDisplay {
		for name, on := range js {
			var dev int
			if on && strings.HasPrefix(name, "HDMI/DP") && fmtScan(name, "HDMI/DP,pcm=%d Jack", &dev) {
				for _, p := range l.targets {
					if p.Dev == dev {
						l.targets = []PCM{p}
						return
					}
				}
			}
		}
		return
	}
	l.jack, l.jackOn = headphoneJack(js)
	l.phantom = phantomJack(js)
	l.jackRead = true
	l.jackSeen = l.jackSeen || l.jackOn
}

func fmtScan(s, format string, v *int) bool {
	n, err := fmt.Sscanf(s, format, v)
	return err == nil && n == 1
}

// waitReady is the part before the rounds.
func (l *listen) waitReady(c *Ctx) {
	poll := c.T-l.polled >= 0.5
	if poll {
		l.polled = c.T
	}
	switch l.kind {
	case outSpeakers:
		l.unplug = l.unplug || l.jackOn
		if l.jacks == nil && (!l.jackOn || l.anyway) && c.T >= 0.4 {
			l.begin(c)
		} else if poll && l.jacks == nil {
			l.jacks = l.snd.Jacks(l.out.Card)
		}
	case outHeadphones: // only once the person says Ready: they're on their ears
		if l.anyway {
			l.begin(c)
		} else if poll && l.jacks == nil {
			l.jacks = l.snd.Jacks(l.out.Card)
		}
	case outDisplay:
		if poll {
			for _, e := range elds(c.IO) {
				if e.Present && e.Valid && l.sink == nil {
					e := e
					l.sink = &e
					l.targets = onCard(l.targets, e.Card)
					l.jacks = l.snd.Jacks(e.Card) // which of the card's devices has it
				}
			}
		}
		if l.sink != nil && l.jacks == nil || l.anyway {
			l.begin(c)
		}
	}
}

// onCard keeps the devices on one card (a sink's).
func onCard(ps []PCM, card int) []PCM {
	var out []PCM
	for _, p := range ps {
		if p.Card == card {
			out = append(out, p)
		}
	}
	if len(out) == 0 {
		return ps
	}
	return out
}

// begin starts the first round.
func (l *listen) begin(c *Ctx) {
	l.ready = true
	if l.kind == outDisplay {
		l.next(c, &listenRound{side: sideBoth})
		return
	}
	l.next(c, &listenRound{side: sideLeft})
}

// next queues a round's first play.
func (l *listen) next(c *Ctx, r *listenRound) {
	if r.word {
		r.answer, r.choices = pickWord(l.rng, l.words, nil)
	} else if r.quiet {
		r.n = 3
	} else {
		var not []int
		for _, x := range l.rounds {
			not = append(not, x.n)
		}
		r.n = newCount(l.rng, not...)
	}
	r.tries = 1
	l.rounds = append(l.rounds, r)
	l.nextAt = c.T + 0.8
}

func (l *listen) cur() *listenRound {
	if len(l.rounds) == 0 {
		return nil
	}
	return l.rounds[len(l.rounds)-1]
}

// play plays the current round's sound.
func (l *listen) play(c *Ctx) {
	r := l.cur()
	targets := []PCM{l.out}
	lead, level := leadAnalog, beepLevel
	switch l.kind {
	case outDisplay:
		targets, lead = l.targets, leadHDMI
	case outHeadphones:
		level = headphoneLevel
	}
	var wav []byte
	if r.word {
		var err error
		if wav, err = wordWAV(audioclips.Clip(r.answer), lead); err != nil {
			l.playFailed(c, err)
			return
		}
	} else {
		wav = beeps(r.n, r.side, lead, level)
	}
	l.stopSound()
	for _, p := range targets {
		l.players = append(l.players, l.snd.Play(p, wav))
	}
	if l.kind == outHeadphones && l.spkState == "" && l.spkMix == nil { // the speakers' state, while it plays
		l.spkMix = l.snd.Mixer(l.out.Card)
	}
	l.playing, l.sayAt, l.face = true, c.T, boot.Work
	l.say = l.listening(r)
	c.Select(l.choices(r)) // "Play it again": the answers wait until it has played
}

// choices is how many answers the round offers before "Play it again" and "I hear nothing".
func (l *listen) choices(r *listenRound) int {
	switch {
	case r.word:
		return len(r.choices)
	case r.quiet:
		return 2
	}
	return 5
}

// played reports whether every player has finished, and an error when none could play (played
// through several HDMI/DP devices at once, one that has no monitor may refuse).
func (l *listen) played() (error, bool) {
	var first error
	failed := 0
	for _, p := range l.players {
		err, done := p.Done()
		if !done {
			return nil, false
		}
		if err != nil {
			failed++
			if first == nil {
				first = err
			}
		}
	}
	if failed < len(l.players) {
		return nil, true
	}
	return first, true
}

func (l *listen) stopSound() {
	for _, p := range l.players {
		p.Stop()
	}
	l.players = nil
}

func (l *listen) Stop(*Ctx) { l.stopSound() }

// playFailed: the sound couldn't be played at all.
func (l *listen) playFailed(c *Ctx, err error) {
	id := l.kind.id()
	switch err {
	case ErrNotReal:
		l.decide(c, NotTested(id, checks.Other, "fixture run: nothing is played", i18n.T("nothing played (not on a Mac)")), i18n.T("not on a real Mac, so no sound. skipped."))
	case ErrNoTool:
		l.decide(c, NotTested(id, checks.Other, "alsa-utils isn't on this image: nothing could be played", i18n.T("no aplay on this image")), i18n.T("no aplay on this image. that's on me, not the Mac."))
	default:
		l.decide(c, Result(id, checks.Failed, Automatic, l.evidence()+"; playback failed: "+err.Error(), i18n.T("playback failed")),
			i18n.T("the sound card refused to play. that's a fail."))
	}
}

// sideName is a side for the screen.
func sideName(side int, kind outKind) string {
	switch {
	case side == sideLeft && kind == outHeadphones:
		return i18n.T("left ear")
	case side == sideRight && kind == outHeadphones:
		return i18n.T("right ear")
	case side == sideLeft:
		return i18n.T("left speaker")
	case side == sideRight:
		return i18n.T("right speaker")
	}
	return i18n.T("monitor")
}

// where is where a sound plays, as a phrase: "on the left speaker", "in the left ear".
func where(side int, kind outKind, mono bool) string {
	switch {
	case side == sideLeft && mono: // the one speaker plays both
		return i18n.T("on the left side")
	case side == sideRight && mono:
		return i18n.T("on the right side")
	case side == sideLeft && kind == outHeadphones:
		return i18n.T("in the left ear")
	case side == sideRight && kind == outHeadphones:
		return i18n.T("in the right ear")
	case side == sideLeft:
		return i18n.T("on the left speaker")
	case side == sideRight:
		return i18n.T("on the right speaker")
	}
	return i18n.T("on the monitor")
}

// sideEN is a side for the evidence.
func sideEN(r *listenRound) string {
	switch {
	case r.word:
		return "word"
	case r.quiet:
		return "speakers with the headphones in"
	case r.side == sideLeft:
		return "left channel"
	case r.side == sideRight:
		return "right channel"
	}
	return "both channels"
}

// listening is Boot's line while a sound plays.
func (l *listen) listening(r *listenRound) string {
	switch {
	case r.quiet:
		return i18n.T("beeps in both ears. and the speakers?")
	case r.tries > 1 && r.word:
		return i18n.T("a different word this time…")
	case r.tries > 1:
		return i18n.T("a new count, same side…")
	case r.word:
		return i18n.T("one word, said twice…")
	case r.side == sideRight:
		return i18n.T("now %s…", where(r.side, l.kind, l.mono))
	}
	return i18n.T("beeps %s…", where(r.side, l.kind, l.mono))
}

// question is Boot's line once a sound has played.
func (l *listen) question() string {
	if l.cur().quiet {
		return i18n.T("did the speakers stay quiet?")
	}
	if l.cur().word {
		return i18n.T("which word was it?")
	}
	return i18n.T("how many beeps?")
}

// answers are the person's choices for the current round: the counts 1 to 5 (so the number
// keys match), or the four words; then, a row apart, "Play it again" and "I hear nothing". All
// greyed while it plays.
func (l *listen) answers() []Action {
	r := l.cur()
	var a []Action
	if r.quiet {
		a = []Action{{Label: i18n.T("Yes, quiet"), Does: i18n.T("only the headphones")},
			{Label: i18n.T("No, they played too"), Does: i18n.T("speakers beeped too")},
			{Label: i18n.T("Play it again"), Does: i18n.T("3 beeps in both ears"), Gap: true}}
		for i := range a {
			a[i].Off = l.playing || l.nextAt >= 0
		}
		return a
	}
	if r.word {
		for _, w := range r.choices {
			a = append(a, Action{Label: w}) // the clip's own English word: not translated
		}
	} else {
		for n := 1; n <= 5; n++ {
			a = append(a, Action{Label: i18n.N("%d beep", "%d beeps", n, n)})
		}
	}
	nothing := i18n.T("not a sound from this side")
	if r.word || r.side == sideBoth {
		nothing = i18n.T("no sound at all")
	}
	a = append(a, Action{Label: i18n.T("Play it again"), Does: i18n.T("the same sound once more")},
		Action{Label: i18n.T("I hear nothing"), Does: nothing})
	busy := l.playing || l.nextAt >= 0
	for i := range a {
		a[i].Off = busy
	}
	return a
}

func (l *listen) Act(c *Ctx, i int) {
	if !l.ready {
		l.actReady(c, i)
		return
	}
	r := l.cur()
	if r == nil || l.playing || l.nextAt >= 0 {
		return
	}
	choices := l.choices(r)
	if r.quiet && i == choices+1 { // no "I hear nothing" here
		return
	}
	switch {
	case r.quiet && i < choices:
		r.result = [...]string{speakersQuiet, speakersPlayed}[i]
		r.trail = append(r.trail, "the tester said "+map[string]string{speakersQuiet: "they stayed quiet", speakersPlayed: "they played too"}[r.result])
		l.after(c)
	case i == choices: // play it again
		r.trail = append(r.trail, "replayed")
		l.nextAt = c.T + 0.3
	case i == choices+1: // I hear nothing
		r.trail = append(r.trail, l.played1(r)+", answered nothing heard")
		r.result = heardNone
		l.after(c)
	case i >= 0 && i < choices:
		got := fmt.Sprint(i + 1)
		right := i+1 == r.n
		if r.word {
			got, right = r.choices[i], r.choices[i] == r.answer
		}
		r.trail = append(r.trail, l.played1(r)+", answered "+got)
		switch {
		case right:
			r.result = heardOK
			l.after(c)
		case r.tries == 1:
			r.tries = 2
			if r.word {
				r.answer, r.choices = pickWord(l.rng, l.words, []string{r.answer})
			} else {
				r.n = newCount(l.rng, r.n)
			}
			l.say, l.sayAt, l.face = i18n.T("not quite. one more try with a new one."), c.T, boot.Hmm
			l.nextAt = c.T + 1.6
		default:
			r.result = heardMissed
			l.after(c)
		}
	}
}

// Skip takes "Skip this one" during the speakers' word as no word: both sides' beeps were
// counted right to get there, and they decide the result (owner, 2026-10-11).
func (l *listen) Skip(c *Ctx) bool {
	r := l.cur()
	if !l.ready || r == nil || !r.word || r.result != "" {
		return false
	}
	r.trail = append(r.trail, "skipped by the tester")
	r.result, l.nextAt, l.playing = heardSkip, -1, false
	l.after(c)
	return true
}

// played1 is what played this try, for the evidence.
func (l *listen) played1(r *listenRound) string {
	if r.word {
		return fmt.Sprintf("\"%s\" (choices %s)", r.answer, strings.Join(r.choices, ", "))
	}
	return fmt.Sprintf("%d beeps", r.n)
}

// hpWait is how long the headphones wait for the jack before "They're in, nothing happens"
// is offered (owner, 2026-10-12).
const hpWait = 20.0

// headphonesIn says whether Ready can play: the jack reports headphones, or the card has no jack
// control to ask.
func (l *listen) headphonesIn() bool { return l.jackRead && (l.jack == "" || l.jackOn) }

// actReady: the answers before the rounds.
func (l *listen) actReady(c *Ctx, i int) {
	if l.kind == outHeadphones && i == 1 && !l.headphonesIn() && c.T >= hpWait { // They're in, nothing happens
		l.decide(c, Result(l.kind.id(), checks.Failed, Observed,
			fmt.Sprintf("the person said headphones are plugged in; %s never reported them in %.0f s; nothing played", l.jack, c.T),
			i18n.T("the jack never noticed the headphones")),
			i18n.T("the jack never noticed them. an installed system wouldn't switch to the headphones."))
		return
	}
	if i != 0 {
		return
	}
	switch l.kind {
	case outSpeakers: // "They're out": the jack control may be stuck
		l.anyway = true
	case outHeadphones: // Ready, once the jack sees them (or has no control to tell)
		if l.headphonesIn() {
			l.anyway = true
		}
	case outDisplay:
		if c.T >= 12 {
			l.anyway = true
		}
	}
}

// after moves on once a round has ended: the next round, or the decision.
func (l *listen) after(c *Ctx) {
	rs := l.rounds
	switch {
	case l.kind != outDisplay && len(rs) == 1:
		l.say, l.sayAt, l.face = i18n.T("noted. other side next."), c.T, boot.Idle
		l.next(c, &listenRound{side: sideRight})
		return
	case l.kind == outSpeakers && len(rs) == 2 && rs[0].result == heardOK && rs[1].result == heardOK:
		l.say, l.sayAt, l.face = i18n.T("both sides check out. one last thing: a word."), c.T, boot.Pleased
		l.next(c, &listenRound{side: sideBoth, word: true})
		return
	case l.kind == outHeadphones && len(rs) == 2 && rs[0].result == heardOK && rs[1].result == heardOK:
		l.say, l.sayAt, l.face = i18n.T("both ears check out. one question about the speakers."), c.T, boot.Pleased
		// its 3 beeps in both ears play by themselves, like every round's, so there's something to
		// listen to the speakers through (owner, 2026-10-15); play puts the cursor on "Play it
		// again", not a ready yes
		l.next(c, &listenRound{side: sideBoth, quiet: true})
		return
	}
	var results []string
	for _, r := range rs {
		results = append(results, r.result)
	}
	st, method, reason := listenVerdict(l.kind, results, l.jackSeen, l.sink != nil)
	id, ev := l.kind.id(), l.evidence()
	var x checks.Result
	if st == checks.NotTested {
		x = NotTested(id, reason, ev, l.detail(st))
	} else {
		x = Result(id, st, method, ev, l.detail(st))
	}
	l.decide(c, x, l.verdictSay(st, results))
}

// listenVerdict is the rule (see the top of the file): the rounds' results in order (left,
// right, word for the speakers; left, right, the speakers' question for headphones; one for
// display-out), whether the headphone jack reported the headphones, and whether the monitor sent
// an ELD.
func listenVerdict(kind outKind, rs []string, jack, eld bool) (status, method, reason string) {
	if kind == outDisplay {
		switch rs[0] {
		case heardNone:
			return checks.Failed, Observed, ""
		case heardMissed:
			return checks.NotTested, "", checks.Uncertain
		}
		if !eld {
			return checks.Partial, Challenge, ""
		}
		return checks.Supported, Challenge, ""
	}
	l, r := rs[0], rs[1]
	switch {
	case l == heardNone && r == heardNone:
		return checks.Failed, Observed, ""
	case l == heardNone || r == heardNone:
		if l == heardOK || r == heardOK {
			return checks.Partial, Challenge, ""
		}
		return checks.Partial, Observed, ""
	case l == heardMissed || r == heardMissed:
		return checks.NotTested, "", checks.Uncertain
	}
	switch kind {
	case outSpeakers:
		word := heardSkip
		if len(rs) > 2 {
			word = rs[2]
		}
		switch word {
		case heardOK: // the word picked: a challenge met
			return checks.Supported, Challenge, ""
		case heardMissed:
			return checks.Partial, Challenge, ""
		case heardNone:
			return checks.Partial, Observed, ""
		}
		return checks.Supported, Observed, "" // the word skipped: the beeps decide
	case outHeadphones:
		quiet := ""
		if len(rs) > 2 {
			quiet = rs[2]
		}
		switch {
		case quiet == speakersPlayed: // the headphones play, and so do the speakers
			return checks.Partial, Observed, ""
		case !jack:
			return checks.Partial, Challenge, ""
		case quiet != speakersQuiet: // not asked: the counts alone don't make a pass
			return checks.NotTested, "", checks.Uncertain
		}
	}
	return checks.Supported, Challenge, ""
}

// evidence is what played where and what the tester answered, in English.
func (l *listen) evidence() string {
	var parts []string
	if l.kind == outDisplay {
		var ds []string
		for _, p := range l.targets {
			ds = append(ds, p.HW()+" ("+p.Name+")")
		}
		s := "played through " + strings.Join(ds, ", ")
		if l.sink != nil {
			s += fmt.Sprintf("; sink %s: %s over %s, ELD valid", l.sink.File, orUnnamed(l.sink.Monitor), orUnnamed(l.sink.Connected))
		} else {
			s += "; no sink sent an ELD (the tester chose to play anyway)"
		}
		parts = append(parts, s)
	} else {
		parts = append(parts, "played through "+l.outDesc)
		if l.mono {
			parts = append(parts, "the Mac has one built-in speaker: both channels should play through it")
		}
		switch {
		case l.jack == "" && l.phantom != "":
			parts = append(parts, "only \""+l.phantom+"\" (always reads plugged in: the codec can't sense the jack)")
		case l.jack == "":
			parts = append(parts, "no headphone jack control")
		case l.kind == outHeadphones && l.jackSeen:
			parts = append(parts, "\""+l.jack+"\" reported the headphones")
		case l.kind == outHeadphones:
			parts = append(parts, "\""+l.jack+"\" never reported the headphones (the tester went on without it)")
		}
		if l.spkState != "" {
			parts = append(parts, "while the headphones played: "+l.spkState)
		}
	}
	if len(l.setups) > 0 { // what the left/right answers rest on (see pcmSetup)
		parts = append(parts, strings.Join(l.setups, ", then ")+"; stereo WAV, left beeps on channel 1, right on 2")
	}
	for _, r := range l.rounds {
		if len(r.trail) > 0 {
			parts = append(parts, sideEN(r)+": "+strings.Join(r.trail, "; "))
		}
	}
	return strings.Join(parts, "; ")
}

func orUnnamed(s string) string {
	if s == "" {
		return "unnamed"
	}
	return s
}

// detail is the log's short version.
func (l *listen) detail(st string) string {
	rs := l.rounds
	res := func(i int) string {
		if i >= len(rs) {
			return i18n.T("not played")
		}
		switch rs[i].result {
		case heardOK:
			return i18n.T("ok")
		case heardNone:
			return i18n.T("silent")
		case heardSkip:
			return i18n.T("skipped")
		}
		return i18n.T("missed")
	}
	if len(rs) > 2 && rs[2].result == speakersPlayed {
		return i18n.T("left %s, right %s, speakers played too", res(0), res(1))
	}
	if len(rs) > 2 && rs[2].result == speakersQuiet && l.jackSeen {
		return i18n.T("left %s, right %s, speakers quiet", res(0), res(1))
	}
	switch l.kind {
	case outDisplay:
		if st == checks.Supported {
			return i18n.T("beeps heard on %s", orUnnamed(l.sink.Monitor))
		}
		return i18n.T("beeps: %s", res(0))
	case outSpeakers:
		if len(rs) < 3 {
			return i18n.T("left %s, right %s", res(0), res(1))
		}
		return i18n.T("left %s, right %s, word %s", res(0), res(1), res(2))
	}
	d := i18n.T("left %s, right %s", res(0), res(1))
	if l.jack != "" && !l.jackSeen {
		d = i18n.T("left %s, right %s, jack not seen", res(0), res(1))
	}
	return d
}

// verdictSay is Boot's line on the result.
func (l *listen) verdictSay(st string, rs []string) string {
	switch st {
	case checks.Supported:
		switch l.kind {
		case outSpeakers:
			if len(rs) < 3 || rs[2] != heardOK {
				return i18n.T("left and right both counted. the speakers work.")
			}
			return i18n.T("left, right, and a word. the speakers work.")
		case outHeadphones:
			return i18n.T("jack seen, both ears counted, speakers quiet. headphones work.")
		}
		return i18n.T("sound over the cable. the monitor plays it.")
	case checks.NotTested:
		return i18n.T("two misses. can't tell from that, so: uncertain.")
	case checks.Failed:
		return i18n.T("not a peep. that's a fail.")
	}
	silent := slices.Contains(rs[:min(2, len(rs))], heardNone) // a side, not the word
	switch {
	case l.kind == outHeadphones && len(rs) > 2 && rs[2] == speakersPlayed:
		return i18n.T("the headphones play, but the speakers kept going too. partial.")
	case l.kind == outDisplay:
		return i18n.T("sound plays, but the monitor never described itself. partial.")
	case silent && l.mono:
		return i18n.T("the one speaker plays only one side. partial.")
	case silent:
		return i18n.T("one side stays quiet. partial.")
	case l.kind == outHeadphones:
		return i18n.T("both ears fine, but the jack never noticed. partial.")
	}
	return i18n.T("beeps fine, but the word didn't come through. partial.")
}

// steps are "What to do", one per round (the headphones' first is plugging them in, the
// speakers' first unplugging them when they were in), and the current one.
func (l *listen) steps() ([]string, int) {
	var s []string
	at := len(l.rounds) - 1 // the round on now
	if l.ready && len(l.rounds) > 0 && l.cur().result != "" {
		at++ // answered: on to the next
	}
	switch l.kind {
	case outSpeakers:
		if l.unplug {
			s = append(s, i18n.T("Unplug the headphones."))
			at++
		}
		if l.mono { // one speaker: both sides should come out of it
			s = append(s, i18n.T("Count the beeps from the speaker (sent left)."), i18n.T("Count the beeps from the speaker (sent right)."),
				i18n.T("Pick the word you hear."))
		} else {
			s = append(s, i18n.T("Count the beeps from the left speaker."), i18n.T("Count the beeps from the right speaker."),
				i18n.T("Pick the word you hear."))
		}
	case outHeadphones:
		s = []string{i18n.T("Plug in headphones and put them on."), i18n.T("Count the beeps in your left ear."),
			i18n.T("Count the beeps in your right ear."), i18n.T("Listen to the Mac's speakers while the headphones beep.")}
		at++
	case outDisplay:
		s = []string{i18n.T("Keep the monitor plugged in, its speakers on."), i18n.T("Count the beeps from the monitor.")}
		at++
	}
	if !l.ready {
		at = 0
	}
	return s, max(0, at)
}

func (l *listen) View(c *Ctx) Screen {
	s := Screen{Say: l.say, SayAt: l.sayAt, Face: l.face, OutTitle: i18n.T("What I play")}
	s.Steps, s.Step = l.steps()
	switch l.kind {
	case outSpeakers:
		s.Happens = i18n.T("Beeps on one side at a time, then a word.")
	case outHeadphones:
		s.Happens = i18n.T("Quiet beeps, one ear at a time, then a question.")
	case outDisplay:
		s.Happens = i18n.T("Once the monitor says it takes sound (its ELD), beeps go over the cable.")
	}
	if !l.found {
		if l.gone != "" {
			s.Live = []Line{Bad(l.gone)}
		}
		return s
	}
	if !l.ready {
		return l.viewReady(c, s)
	}
	r := l.cur()
	busy := l.playing || l.nextAt >= 0
	s.Anim = l.anim(r.side, l.playing, r.quiet)
	switch {
	case busy:
		s.Waiting = "·  " + i18n.T("playing")
	case r.quiet:
		s.Waiting = "·  " + i18n.T("waiting for your answer")
		s.Hint = i18n.T("not sure? take the headphones off, Play it again, and listen to the Mac itself.")
	case r.word:
		s.Waiting = "·  " + i18n.T("waiting for the word")
		s.Hint = i18n.T("missed it? Play it again says the same word once more.")
	default:
		s.Waiting = "·  " + i18n.T("waiting for your count")
		s.Hint = i18n.T("lost count? Play it again plays the same beeps.")
	}
	s.Live = l.devLines()
	for _, x := range l.rounds[:len(l.rounds)-1] {
		s.Live = append(s.Live, l.roundLine(x))
	}
	if r.result != "" {
		s.Live = append(s.Live, l.roundLine(r))
	}
	if busy { // never the count: only where it plays
		what := Wait(i18n.T("playing %s", where(r.side, l.kind, l.mono)))
		switch {
		case !l.playing:
			what = Wait(i18n.T("about to play"))
		case r.quiet:
			what = Wait(i18n.T("playing in both ears"))
		case r.word:
			what = Wait(i18n.T("playing a word on both sides"))
		}
		s.Live = append(s.Live, what)
	}
	s.Actions = l.answers() // the step says which side: no line over them
	if r.word && l.kind == outSpeakers {
		s.SkipDoes = i18n.T("no word: the beeps decide")
	}
	return s
}

// anim is the band's picture for the round: the speakers, the headphones (with the Mac's
// speakers beside them for the quiet question) or the monitor, waves where it plays.
func (l *listen) anim(side int, playing, quiet bool) Panel {
	switch l.kind {
	case outHeadphones:
		return headphonesArt(side, playing, l.jackState(), quiet, hpStill)
	case outDisplay:
		name := ""
		if l.sink != nil {
			name = l.sink.Monitor
		}
		return monitorArt(name, l.sink != nil, playing)
	}
	return speakersArt(side, playing, false)
}

// jackState is the headphone jack for the picture: "" no jack control, "in", "out".
func (l *listen) jackState() string {
	switch {
	case l.jack == "" && l.jackRead:
		return ""
	case l.jackOn:
		return "in"
	}
	return "out"
}

// devLines are where the sound goes.
func (l *listen) devLines() []Line {
	if l.kind == outDisplay {
		var out []Line
		if l.sink != nil {
			out = append(out, Fact(i18n.T("%s: %s over %s", l.sink.File, orUnnamed(l.sink.Monitor), orUnnamed(l.sink.Connected))))
		}
		var ds []string
		for _, p := range l.targets {
			ds = append(ds, p.HW())
		}
		return append(out, Fact(i18n.T("playing through %s", strings.Join(ds, ", "))))
	}
	out := []Line{Fact(i18n.T("%s: %s", l.out.HW(), l.out.Name))}
	if l.kind == outHeadphones {
		switch {
		case l.jack == "":
			out = append(out, Gone(i18n.T("no headphone jack control on this card")))
		case l.jackOn:
			out = append(out, Fact(i18n.T("%s: plugged in", l.jack)))
		default:
			out = append(out, Gone(i18n.T("%s: nothing plugged in", l.jack)))
		}
	}
	return out
}

// roundLine is a finished listenRound under "What I see".
func (l *listen) roundLine(r *listenRound) Line {
	what := sideName(r.side, l.kind)
	if r.word {
		what = i18n.T("the word")
	}
	switch r.result {
	case speakersQuiet:
		return Fact(i18n.T("speakers: stayed quiet"))
	case speakersPlayed:
		return Gone(i18n.T("speakers: played too"))
	case heardOK:
		return Fact(i18n.T("%s: heard right", what))
	case heardNone:
		return Gone(i18n.T("%s: nothing heard", what))
	case heardSkip:
		return Gone(i18n.T("%s: skipped", what))
	}
	return Gone(i18n.T("%s: missed twice", what))
}

// viewReady is the screen before the rounds.
func (l *listen) viewReady(c *Ctx, s Screen) Screen {
	switch l.kind {
	case outSpeakers:
		s.Live = l.devLines()
		s.Anim = speakersArt(sideBoth, false, l.jackOn)
		if l.jackOn {
			s.Anim = headphonesArt(sideBoth, false, "in", false, hpUnplug) // take them out: the plug coming out
			s.Say, s.SayAt = i18n.T("headphones are in. the speakers stay quiet while they are."), 0.2
			s.Hint = i18n.T("nothing in the jack? it may be stuck: They're out goes on anyway.")
			s.Waiting = "·  " + i18n.T("waiting for the jack to empty")
			s.Live = append(s.Live, Fact(i18n.T("%s: plugged in", l.jack)), Wait(i18n.T("waiting for the jack to empty")))
			s.Actions = []Action{{Label: i18n.T("They're out"), Does: i18n.T("nothing is plugged in; go on")}}
			return s
		}
		s.Waiting = "·  " + i18n.T("getting ready")
		s.Live = append(s.Live, Wait(i18n.T("getting ready")))
	case outHeadphones: // nothing plays until the person says Ready
		s.Say, s.SayAt = i18n.T("waiting for the jack to notice some headphones."), 0.2
		if l.headphonesIn() {
			s.Say = i18n.T("there they are. ready when you are.")
		}
		s.Hint = i18n.T("still waiting? push the plug all the way in: the jack has to notice it.")
		motion := hpReach // plug them in: the plug reaching for the jack
		if l.headphonesIn() {
			motion = hpStill
		}
		s.Anim = headphonesArt(sideBoth, false, l.jackState(), false, motion)
		s.Live = l.devLines()
		ready := i18n.T("they're on: play the beeps")
		switch { // devLines shows the jack's state
		case !l.jackRead:
			ready = i18n.T("checking the headphone jack")
		case l.jack != "" && !l.jackOn:
			ready = i18n.T("waiting for the jack to notice the headphones")
		}
		s.Waiting = "·  " + i18n.T("waiting for the headphones")
		s.Actions = []Action{{Label: i18n.T("Ready"), Does: ready, Off: !l.headphonesIn()}}
		if !l.headphonesIn() && c.T >= hpWait {
			s.Actions = append(s.Actions, Action{Label: i18n.T("They're in, nothing happens"),
				Does: i18n.T("the jack doesn't notice them: record that, play nothing")})
		}
	case outDisplay:
		s.Say, s.SayAt = i18n.T("the monitor's speakers next. waiting for it to say it takes sound."), 0.2
		s.Hint = i18n.T("some monitors only describe their sound once their speakers are on. Play anyway tries regardless.")
		s.Anim = monitorArt("", false, false)
		for _, k := range externalConnectors(c.IO) {
			if k.status == "connected" {
				s.Live = append(s.Live, Fact(i18n.T("%s (%s): connected", k.name, portKind(k.name))))
			}
		}
		for _, e := range elds(c.IO) {
			if e.Present && !e.Valid {
				s.Live = append(s.Live, Gone(i18n.T("%s: a monitor, but no audio description yet", e.File)))
			}
		}
		s.Live = append(s.Live, Wait(i18n.T("waiting for a monitor that takes sound (its ELD)")))
		s.Waiting = "·  " + i18n.T("waiting for the monitor")
		s.Actions = []Action{{Label: i18n.T("Play anyway"), Does: i18n.T("it has speakers; send the beeps to every HDMI/DP output"), Off: c.T < 12}}
	}
	return s
}
