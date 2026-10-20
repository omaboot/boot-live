package handson

import (
	"errors"
	"math"
	"strconv"
	"strings"
	"testing"

	audioclips "github.com/omaboot/boot-live/assets/audio"
	"github.com/omaboot/boot-live/internal/checks"
)

// The WAV writer and reader agree, and every built-in clip reads as a short 16-bit word.
func TestWAV(t *testing.T) {
	in := []int16{0, 1, -1, 32767, -32768, 1234}
	rate, ch, s, err := parseWAV(wavBytes(48000, 2, in))
	if err != nil || rate != 48000 || ch != 2 || len(s) != len(in) || s[3] != 32767 || s[4] != -32768 {
		t.Fatalf("round trip: %d %d %v %v", rate, ch, s, err)
	}
	if len(audioclips.Words()) < 8 {
		t.Fatalf("only %d words", len(audioclips.Words()))
	}
	for _, w := range audioclips.Words() {
		clip := audioclips.Clip(w)
		rate, ch, s, err := parseWAV(clip)
		if err != nil || ch < 1 || rate < 8000 {
			t.Errorf("%s: %d Hz %d ch: %v", w, rate, ch, err)
			continue
		}
		if d := float64(len(s)/ch) / float64(rate); d < 0.2 || d > 2 {
			t.Errorf("%s: %.2f s", w, d)
		}
		word, err := wordWAV(clip, leadAnalog)
		if err != nil || wavSeconds(word) < 2*wavSeconds(clip) {
			t.Errorf("%s: word WAV %.2f s, %v", w, wavSeconds(word), err)
		}
	}
	if _, _, _, err := parseWAV([]byte("RIFF....WAVEjunk")); err == nil {
		t.Error("a WAV without data parsed")
	}
}

// bursts counts the beeps on one channel of a stereo WAV.
func bursts(t *testing.T, wav []byte, c int) int {
	t.Helper()
	rate, ch, s, err := parseWAV(wav)
	if err != nil || ch != 2 {
		t.Fatalf("beeps: %v", err)
	}
	n, on, quiet := 0, false, 0
	for i := c; i < len(s); i += 2 {
		loud := math.Abs(float64(s[i])) > 1000
		switch {
		case loud && !on:
			n, on = n+1, true
		case loud:
			quiet = 0
		case on:
			if quiet++; quiet > rate/50 { // 20 ms of quiet ends a beep
				on, quiet = false, 0
			}
		}
	}
	return n
}

// Beeps are the count asked for, on the side asked for only, and gentle.
func TestBeeps(t *testing.T) {
	for n := 2; n <= 5; n++ {
		w := beeps(n, sideLeft, leadAnalog, beepLevel)
		if l, r := bursts(t, w, 0), bursts(t, w, 1); l != n || r != 0 {
			t.Errorf("%d left: %d left, %d right", n, l, r)
		}
		w = beeps(n, sideRight, leadHDMI, headphoneLevel)
		if l, r := bursts(t, w, 0), bursts(t, w, 1); l != 0 || r != n {
			t.Errorf("%d right: %d left, %d right", n, l, r)
		}
	}
	_, _, s, _ := parseWAV(beeps(3, sideBoth, 0, beepLevel))
	peak := 0.0
	for _, v := range s {
		peak = math.Max(peak, math.Abs(float64(v))/32768)
	}
	if peak > beepLevel+0.01 || peak < beepLevel-0.02 {
		t.Errorf("peak %.3f, want %.2f", peak, beepLevel)
	}
	if s[0] != 0 || s[1] != 0 {
		t.Error("beeps don't start in silence")
	}
}

func TestListenVerdict(t *testing.T) {
	ok, miss, none := heardOK, heardMissed, heardNone
	cases := []struct {
		kind      outKind
		rs        []string
		jack, eld bool
		want      string
	}{
		{outSpeakers, []string{ok, ok, ok}, false, false, "supported/challenge"},
		{outSpeakers, []string{ok, ok, miss}, false, false, "partial/challenge"},
		{outSpeakers, []string{ok, ok, none}, false, false, "partial/observed"},
		{outSpeakers, []string{ok, ok, heardSkip}, false, false, "supported/observed"},
		{outSpeakers, []string{none, ok}, false, false, "partial/challenge"},
		{outSpeakers, []string{ok, none}, false, false, "partial/challenge"},
		{outSpeakers, []string{none, miss}, false, false, "partial/observed"},
		{outSpeakers, []string{none, none}, false, false, "failed/observed"},
		{outSpeakers, []string{miss, ok}, false, false, "not_tested/uncertain"},
		{outSpeakers, []string{ok, miss}, false, false, "not_tested/uncertain"},
		{outHeadphones, []string{ok, ok, speakersQuiet}, true, false, "supported/challenge"},
		{outHeadphones, []string{ok, ok, speakersPlayed}, true, false, "partial/observed"},
		{outHeadphones, []string{ok, ok}, true, false, "not_tested/uncertain"}, // the speakers never asked about
		{outHeadphones, []string{ok, ok, speakersQuiet}, false, false, "partial/challenge"},
		{outHeadphones, []string{none, ok}, true, false, "partial/challenge"},
		{outHeadphones, []string{none, none}, true, false, "failed/observed"},
		{outHeadphones, []string{miss, miss}, true, false, "not_tested/uncertain"},
		{outDisplay, []string{ok}, false, true, "supported/challenge"},
		{outDisplay, []string{ok}, false, false, "partial/challenge"},
		{outDisplay, []string{none}, false, true, "failed/observed"},
		{outDisplay, []string{miss}, false, true, "not_tested/uncertain"},
	}
	for _, c := range cases {
		st, m, reason := listenVerdict(c.kind, c.rs, c.jack, c.eld)
		got := st + "/" + m
		if st == checks.NotTested {
			got = st + "/" + reason
		}
		if got != c.want {
			t.Errorf("%s %v jack %v eld %v: %s, want %s", c.kind.id(), c.rs, c.jack, c.eld, got, c.want)
		}
	}
}

func TestMicFallback(t *testing.T) {
	cases := []struct {
		floor, best float64
		sound       bool
		want        string
	}{
		{-58, -30, true, checks.Partial}, // loud, but neither the tone nor the claps: never a pass
		{-58, -50, true, checks.Partial},
		{-58, -56, true, ""},
		{-100, -100, false, checks.Failed},
		{-80, -62, true, ""},
	}
	for _, c := range cases {
		if got := micFallback(c.floor, c.best, c.sound); got != c.want {
			t.Errorf("floor %v best %v sound %v: %q, want %q", c.floor, c.best, c.sound, got, c.want)
		}
	}
}

// noise is n samples of repeatable uniform noise at rms (of full scale).
func noise(n int, rms float64, seed uint32) []float64 {
	out := make([]float64, n)
	for i := range out {
		seed ^= seed << 13
		seed ^= seed >> 17
		seed ^= seed << 5
		out[i] = (float64(seed)/float64(1<<32)*2 - 1) * rms * math.Sqrt(3)
	}
	return out
}

func samples(xs []float64) []int16 {
	s := make([]int16, len(xs))
	for i, x := range xs {
		s[i] = int16(max(-32768, min(32767, x*32768)))
	}
	return s
}

// levels are one window's levels at each note and around it, for noteHits.
func levels(s []int16) (at, around [micN]float64) {
	for i, n := range micNotes {
		at[i], around[i] = toneLevel(s, n.hz)
	}
	return at, around
}

// roomAt is a quiet room's level at every note.
func roomAt(d float64) (r [micN]float64) {
	for i := range r {
		r[i] = d
	}
	return r
}

// The Goertzel filter finds each of the five notes (D5 E5 C5 C4 G4) under noise, at its level,
// and finds nothing in noise alone, however loud (a clap, a bump, the fan); one note never reads
// as another, even in a quiet room where a loud note leaks 25 dB over the background into its
// neighbour 64 Hz away (only the loudest note counts), nor C4 as C5 when the speakers turn it
// into its 2nd harmonic; so the control round can't hear the last note in the next's place. A
// mains hum harmonic on a note (660 Hz, 11 × 60) measured with the room doesn't make it heard,
// and every note's comparison band stays out of the rumble and off its own harmonics.
func TestToneLevel(t *testing.T) {
	const n = micRate / 20
	sine := func(xs []float64, hz, amp float64) {
		for i := range xs {
			xs[i] += amp * math.Sin(2*math.Pi*hz*float64(i)/micRate)
		}
	}
	for _, note := range micNotes {
		fs := toneAround(note.hz)
		if len(fs) != aroundN {
			t.Errorf("%s: %d comparison frequencies", note.name, len(fs))
		}
		for _, f := range fs {
			if f < aroundLow || math.Abs(f-note.hz) < 100 || nearHarmonic(f, note.hz) {
				t.Errorf("%s: comparison frequency %.0f Hz", note.name, f)
			}
		}
	}
	for k, note := range micNotes {
		for _, c := range []struct {
			name       string
			noise, amp float64
		}{{"room around −50", 0.003, 0.02}, {"quiet room, loud note", 0.00003, 0.1}} {
			xs := noise(n, c.noise, 7)
			sine(xs, note.hz, c.amp)
			at, around := levels(samples(xs))
			if math.Abs(at[k]-dB(c.amp)) > 1 || !toneHit(at[k], around[k], -70) {
				t.Errorf("%s, %s: %.1f dBFS, %.1f around: not heard", note.name, c.name, at[k], around[k])
			}
			if toneHit(at[k], around[k], at[k]-5) {
				t.Errorf("%s, %s: heard though the room is as loud at that pitch", note.name, c.name)
			}
			hit := noteHits(at, around, roomAt(-70))
			for j := range micNotes {
				if hit[j] != (j == k) {
					t.Errorf("%s playing, %s: %s heard %v (%.1f dBFS, %.1f around)", note.name, c.name, micNotes[j].name, hit[j], at[j], around[j])
				}
			}
		}
	}
	// C4 through small speakers: a weak fundamental under a louder 2nd harmonic (C5) is not C4
	c4 := 3
	xs := noise(n, 0.001, 5)
	sine(xs, micNotes[c4].hz, 0.005)
	sine(xs, 2*micNotes[c4].hz, 0.02)
	if at, around := levels(samples(xs)); noteHits(at, around, roomAt(-70))[c4] {
		t.Errorf("C4 heard though its harmonic was louder (%.1f, C5 %.1f)", at[c4], at[2])
	}
	// loud noise: no note heard in as many windows as the control round counts, in 200
	var hits [micN]int
	for k := 0; k < 200; k++ {
		at, around := levels(samples(noise(n, 0.3, uint32(k+1))))
		for i, h := range noteHits(at, around, roomAt(-70)) {
			if h {
				hits[i]++
			}
		}
	}
	for i, h := range hits {
		if h >= controlHits {
			t.Errorf("loud noise: %d of 200 windows heard %s", h, micNotes[i].name)
		}
	}
	// rumble (a fan, a bump on the desk) under 200 Hz, 20 dB over the room: not C4
	rumble := noise(n, 0.003, 11)
	sine(rumble, 90, 0.1)
	sine(rumble, 140, 0.05)
	if at, around := levels(samples(rumble)); noteHits(at, around, roomAt(dB(0.003)))[c4] {
		t.Errorf("rumble under 200 Hz heard as C4 (%.1f, %.1f around)", at[c4], around[c4])
	}
	// hum at E5's 660 Hz, as loud in the room as the note will be: never heard over the room
	e5 := 1
	hum := noise(n, 0.003, 9)
	sine(hum, 660, 0.02)
	room, _ := toneLevel(samples(hum), micNotes[e5].hz)
	if at, around := toneLevel(samples(hum), micNotes[e5].hz); toneHit(at, around, room) {
		t.Errorf("hum at 660 Hz heard as E5 over the room (%.1f, room %.1f)", at, room)
	}
	if at, _ := toneLevel(make([]int16, n), micNotes[0].hz); at > -99 {
		t.Errorf("digital silence at %.0f dBFS", at)
	}
	// the notes as played: soft (−18 dBFS peak), faded in and out, nothing at the very start
	for _, note := range micNotes {
		_, _, s, _ := parseWAV(toneWAV(note.hz, 0))
		peak := 0.0
		for _, v := range s {
			peak = math.Max(peak, math.Abs(float64(v))/32768)
		}
		if peak > noteLevel+0.005 || peak < noteLevel-0.01 {
			t.Errorf("%s peak %.3f, want %.3f", note.name, peak, noteLevel)
		}
		if early := math.Abs(float64(s[2*int(0.02*beepRate)])) / 32768; early > noteLevel/4 {
			t.Errorf("%s: 20 ms in, the note is already at %.3f: no fade in", note.name, early)
		}
	}
}

// The control round passes only when no note is heard in it: a stuck detector (every window a
// hit) or a note still sounding fails it; the rounds play the motif in order (D5 E5 C5 C4 G4)
// and put the control after the first note and before the last.
func TestControlRound(t *testing.T) {
	if !controlOK([micN]int{0, 1, controlHits - 1}) {
		t.Error("a few stray windows failed the control round")
	}
	if controlOK([micN]int{36, 36, 36, 36, 36}) || controlOK([micN]int{0, 0, 0, controlHits, 0}) {
		t.Error("a stuck detector passed the control round")
	}
	rs := micRounds()
	ctl, order := -1, ""
	for i, r := range rs {
		if r.control() {
			ctl = i
		} else {
			order += micNotes[r.note].name
		}
	}
	if len(rs) != micN+1 || order != "D5E5C5C4G4" || ctl != micN {
		t.Fatalf("rounds %s, control at %d", order, ctl)
	}
}

// The loopback's rule: every note and a quiet pause pass; C4 alone may be missed (it played and
// wasn't heard), nothing else may; two misses, a note that couldn't play, a pause that heard a
// note, or no pause at all never pass, so the C4 exception can't turn a stuck or noisy detector
// into a pass.
func TestMicRule(t *testing.T) {
	// rounds builds the rounds D5 E5 [pause] C5 C4 G4: miss lists the notes not found, err the
	// note that couldn't play, ctl whether the pause was quiet (-1: no pause)
	rounds := func(ctl int, err string, miss ...string) []*tone {
		var out []*tone
		for i, n := range micNotes {
			if i == 2 && ctl >= 0 {
				out = append(out, &tone{note: -1, found: ctl == 1})
			}
			tn := &tone{note: i, found: true, wins: 30}
			for _, m := range miss {
				if m == n.name {
					tn.found = false
				}
			}
			if err == n.name {
				tn.found, tn.err = false, errors.New("device busy")
			}
			out = append(out, tn)
		}
		return out
	}
	cases := []struct {
		name string
		ts   []*tone
		want int
	}{
		{"every note, quiet pause", rounds(1, ""), loopPass},
		{"C4 missed", rounds(1, "", "C4"), loopPass},
		{"G4 missed", rounds(1, "", "G4"), loopClaps},
		{"D5 missed", rounds(1, "", "D5"), loopClaps},
		{"C4 and G4 missed", rounds(1, "", "C4", "G4"), loopClaps},
		{"C4 couldn't play", rounds(1, "C4"), loopClaps},
		{"the pause heard a note", rounds(0, ""), loopClaps},
		{"the pause heard a note, C4 missed", rounds(0, "", "C4"), loopClaps},
		{"no pause", rounds(-1, ""), loopClaps},
	}
	for _, c := range cases {
		if got := micRule(c.ts, len(c.ts)); got != c.want {
			t.Errorf("%s: %d, want %d", c.name, got, c.want)
		}
	}
	// partway: C4 missed goes on to G4; a second miss stops there
	ts := rounds(1, "", "C4", "G4")
	if got := micRule(ts[:5], len(ts)); got != loopGoing {
		t.Errorf("after C4 missed: %d, want going on", got)
	}
	if got := micRule(ts[:2], len(ts)); got != loopGoing {
		t.Errorf("after two notes: %d, want going on", got)
	}
}

// Claps are counted as distinct peaks: a clap's ring and echo don't count twice, and steady
// noise doesn't count at all.
func TestCountClaps(t *testing.T) {
	clap := func(times ...float64) []float64 {
		xs := noise(3*micRate, 0.002, 3)
		for _, tc := range times {
			for i := int(tc * micRate); i < len(xs) && i < int((tc+0.3)*micRate); i++ {
				dt := float64(i)/micRate - tc
				xs[i] += 0.4 * math.Exp(-dt/0.03) * math.Copysign(1, xs[i])
			}
			for i := int((tc + 0.06) * micRate); i < len(xs) && i < int((tc+0.1)*micRate); i++ {
				xs[i] += 0.05 * math.Copysign(1, xs[i]) // an echo 60 ms later
			}
		}
		var env []float64
		s := samples(xs)
		for i := 0; i+micRate/20 <= len(s); i += micRate / 20 {
			env = append(env, frames(windowOf(s[i:i+micRate/20]))...)
		}
		return env
	}
	thr := clapThreshold(dB(0.002 * math.Sqrt(3)))
	cases := []struct {
		times []float64
		want  int
	}{
		{nil, 0},
		{[]float64{0.5}, 1},
		{[]float64{0.5, 0.9, 1.3}, 3},
		{[]float64{0.5, 0.75, 1.0, 1.25}, 4}, // quick claps, 250 ms apart
	}
	for _, c := range cases {
		if n, _ := countClaps(clap(c.times...), thr); n != c.want {
			t.Errorf("claps at %v: counted %d", c.times, n)
		}
	}
}

func TestParseJacks(t *testing.T) {
	out := `numid=20,iface=CARD,name='HDMI/DP,pcm=3 Jack'
  ; type=BOOLEAN,access=r-------,values=1
  : values=on
numid=21,iface=CARD,name='Headphone Jack'
  ; type=BOOLEAN,access=r-------,values=1
  : values=off
numid=2,iface=MIXER,name='Master Playback Switch'
  ; type=BOOLEAN,access=rw------,values=1
  : values=on
`
	js := parseJacks(out)
	if len(js) != 2 || !js["HDMI/DP,pcm=3 Jack"] || js["Headphone Jack"] {
		t.Errorf("jacks %v", js)
	}
	cs := parseControls(out + `numid=10,iface=MIXER,name='Auto-Mute Mode'
  ; type=ENUMERATED,access=rw------,values=1,items=2
  ; Item #0 'Disabled'
  ; Item #1 'Enabled'
  : values=1
`)
	if cs["Auto-Mute Mode"] != "Enabled" || cs["Master Playback Switch"] != "on" {
		t.Errorf("controls %v", cs)
	}
}

func TestPCMs(t *testing.T) {
	f := NewFake()
	demoSound(f)
	f.Set("/proc/asound/pcm", f.Files["/proc/asound/pcm"]+"\n01-00: Speaker : Speaker : playback 1\n01-01: Headphone : Headphone : playback 1")
	ps := pcms(f)
	if len(ps) != 6 || !ps[0].Capt || !ps[0].Play || ps[1].HDMI() == false || ps[0].HDMI() {
		t.Fatalf("pcms %+v", ps)
	}
	if p, _ := builtInPCM(f, false, "headphone"); p.HW() != "hw:1,1" {
		t.Errorf("headphones on %s", p.HW())
	}
	if p, _ := builtInPCM(f, true, "mic"); p.HW() != "hw:0,0" {
		t.Errorf("capture on %s", p.HW())
	}
	if d := describe(f, ps[0]); d != "hw:0,0 (CS4206 Analog, Cirrus Logic CS4206)" {
		t.Errorf("describe %q", d)
	}
}

// answerer picks the person's answer to a listening round: right, wrong or nothing.
type answerer func(r *listenRound) int

func rightAnswer(r *listenRound) int {
	if r.quiet {
		return 0 // only in the headphones: the speakers stayed quiet
	}
	if r.word {
		for i, w := range r.choices {
			if w == r.answer {
				return i
			}
		}
	}
	return r.n - 1
}

func wrongAnswer(r *listenRound) int {
	if r.quiet {
		return 1 // from the Mac's speakers too
	}
	if r.word {
		return (rightAnswer(r) + 1) % len(r.choices)
	}
	return r.n % 5 // n+1 beeps
}

func nothingAnswer(r *listenRound) int {
	if r.word {
		return len(r.choices) + 1
	}
	return 6
}

func skipAnswer(*listenRound) int { return -1 } // "Skip this one"

// listenRun plays a listening test on the demo Mac (plus script), pressing Play whenever it's
// offered (not the headphones' Ready: a pick), answering each round with the answerer for its
// index (the last one for any after), picks at given times (-1 skip, -2 stop the screen), and
// returns its outcome and the Fake.
func listenRun(t *testing.T, id string, until float64, script func(*Fake), ans []answerer, picks map[float64]int) (*Outcome, *Fake) {
	t.Helper()
	s, _ := Lookup(id)
	f := NewFake()
	DemoMac(f)
	s.Demo(f)
	if script != nil {
		script(f)
	}
	r := Begin(s, &Env{}, f)
	defer r.Stop()
	for tt := 0.0; tt <= until; tt += 0.1 {
		for at, a := range picks {
			if at > tt-0.1 && at <= tt {
				switch a {
				case -1:
					r.Skip()
				case -2:
					r.Stop()
					return nil, f
				default:
					r.Act(a)
				}
			}
		}
		r.Step(tt)
		if o := r.Outcome(); o != nil {
			return o, f
		}
		l, ok := r.Run.(*listen)
		switch {
		case !ok || l.goAt >= 0: // counting down
		case !l.ready && l.set && l.kind != outHeadphones:
			r.Act(0) // Play
		case l.ready && !l.playing && l.nextAt < 0 && len(l.rounds) > 0 && l.cur().hold:
			r.Act(0) // Play: the headphones off, near the ears
		case l.ready && !l.playing && l.nextAt < 0 && len(l.rounds) > 0:
			if cur := l.cur(); cur.result == "" {
				if a := ans[min(len(l.rounds), len(ans))-1](cur); a < 0 {
					r.Skip()
				} else {
					r.Act(a)
				}
			}
		}
	}
	return r.Outcome(), f
}

// The listening tests, played through.
func TestListenRuns(t *testing.T) {
	right, wrong, nothing, skip := answerer(rightAnswer), answerer(wrongAnswer), answerer(nothingAnswer), answerer(skipAnswer)
	unplugged := func(f *Fake) { f.script = nil } // the jack never says plugged
	cases := []struct {
		name, id string
		script   func(*Fake)
		ans      []answerer
		picks    map[float64]int
		want     string
	}{
		{"speakers", "audio.speakers", nil, []answerer{right}, nil, "supported/challenge"},
		{"speakers silent", "audio.speakers", nil, []answerer{nothing}, nil, "failed/observed"},
		{"speakers left silent", "audio.speakers", nil, []answerer{nothing, right}, nil, "partial/challenge"},
		{"speakers left missed", "audio.speakers", nil, []answerer{wrong, right}, nil, "not_tested/uncertain"},
		{"speakers word missed", "audio.speakers", nil, []answerer{right, right, wrong}, nil, "partial/challenge"},
		{"speakers word skipped", "audio.speakers", nil, []answerer{right, right, skip}, nil, "supported/observed"},
		{"speakers, aplay fails", "audio.speakers", func(f *Fake) { f.Set("alsa:fail", "device busy") }, []answerer{right}, nil, "failed/automatic"},
		{"speakers skipped", "audio.speakers", nil, []answerer{right}, map[float64]int{0.5: -1}, "not_tested/other"},
		{"speakers wait for the headphones to come out", "audio.speakers", func(f *Fake) {
			f.Set("alsa:card0:Headphone Jack", "on")
			f.SetAt(4, "alsa:card0:Headphone Jack", "off")
		}, []answerer{right}, nil, "supported/challenge"},
		{"headphones", "audio.headphone", nil, []answerer{right}, map[float64]int{4: 0}, "supported/challenge"},
		// false-pass route: both ears counted right, but the beeps came from the speakers too (Cirrus codecs)
		{"headphones, the speakers played too", "audio.headphone", nil, []answerer{right, right, wrong}, map[float64]int{4: 0}, "partial/observed"},
		// Ready waits for the jack: pressed too early it does nothing
		{"headphones, Ready before the jack saw them", "audio.headphone", nil, []answerer{right}, map[float64]int{0.5: 0, 4: 0}, "supported/challenge"},
		{"headphones, jack never sees them", "audio.headphone", unplugged, []answerer{right}, map[float64]int{9: 0, 21: 1}, "failed/observed"},
		{"headphones, no jack control", "audio.headphone", func(f *Fake) {
			f.script = nil
			delete(f.Files, "alsa:card0:Headphone Jack")
		}, []answerer{right}, map[float64]int{1: 0}, "partial/challenge"},
		{"headphones skipped", "audio.headphone", nil, nil, map[float64]int{1: -1}, "not_tested/no-equipment"},
		{"display out", "audio.display-out", nil, []answerer{right}, nil, "supported/challenge"},
		{"display out silent", "audio.display-out", nil, []answerer{nothing}, nil, "failed/observed"},
		// Play anyway (after 12 s without an ELD) counts down like Play
		{"display out, no ELD", "audio.display-out", func(f *Fake) { f.script = nil }, []answerer{right}, map[float64]int{13: 0}, "partial/challenge"},
		{"display out, no HDMI device", "audio.display-out", func(f *Fake) {
			f.Set("/proc/asound/pcm", "00-00: CS4206 Analog : CS4206 Analog : playback 1 : capture 1")
		}, nil, nil, "failed/automatic"},
	}
	for _, c := range cases {
		o, f := listenRun(t, c.id, 60, c.script, c.ans, c.picks)
		if got := status(o); got != c.want {
			ev := ""
			if o != nil && len(o.Results) > 0 {
				ev = o.Results[0].Evidence
			}
			t.Errorf("%s: %s, want %s (%s)", c.name, got, c.want, ev)
		}
		if n, _ := f.Int("alsa:open"); n != 0 {
			t.Errorf("%s: %d sounds left open", c.name, n)
		}
	}
	// the display-out test plays through the device whose jack has the monitor
	_, f := listenRun(t, "audio.display-out", 60, nil, []answerer{right}, nil)
	if p := f.Read("alsa:played"); p == "" || p[:6] != "hw:0,3" {
		t.Errorf("display out played %q", p)
	}
	// the speaker pin's state while the headphones played is in the evidence
	o, f := listenRun(t, "audio.headphone", 60, nil, []answerer{right}, map[float64]int{4: 0})
	if ev := o.Results[0].Evidence; !strings.Contains(ev, "speaker pin 0x10 pin-ctls 0x00 (output off)") ||
		!strings.Contains(ev, "Speaker Playback Switch on,on") || !strings.Contains(ev, "the speakers stayed quiet") {
		t.Errorf("headphones evidence: %s", ev)
	}
	// the speakers' question plays its 3 beeps once Play is pressed: left, right, then both ears
	if n := len(strings.Split(strings.TrimSpace(f.Read("alsa:played")), "\n")); n != 3 {
		t.Errorf("headphones played %d times, want 3:\n%s", n, f.Read("alsa:played"))
	}
	// nothing plays before Play and its countdown
	_, f = listenRun(t, "audio.speakers", 60, nil, nil, map[float64]int{startCount - 0.2: -2})
	if f.Read("alsa:played") != "" {
		t.Errorf("played before the countdown was over: %q", f.Read("alsa:played"))
	}
	// leaving mid-beep stops the sound
	_, f = listenRun(t, "audio.speakers", 60, nil, nil, map[float64]int{startCount + 0.6: -2})
	if n, _ := f.Int("alsa:open"); n != 0 || f.Read("alsa:played") == "" {
		t.Errorf("stopped mid-play: %d open, played %q", n, f.Read("alsa:played"))
	}
}

// micRun plays the microphone test on the demo Mac (plus script), pressing Start straight
// away; claps gives how many claps the person makes when asked for n (from 1 s after the ask,
// 0.4 s apart; 0 for none), picks are answers at given times. It returns the outcome and the
// Fake.
func micRun(t *testing.T, script func(*Fake), claps func(n int) int, picks map[float64]int) (*Outcome, *Fake) {
	t.Helper()
	s, _ := Lookup("audio.microphone")
	f := NewFake()
	DemoMac(f)
	s.Demo(f)
	if script != nil {
		script(f)
	}
	r := Begin(s, &Env{}, f)
	defer r.Leave()
	m, _ := r.Run.(*mic)
	asked := 0
	for tt := 0.0; tt <= 40; tt += 0.1 {
		for at, a := range picks {
			if at > tt-0.1 && at <= tt {
				r.Act(a)
			}
		}
		r.Step(tt)
		if o := r.Outcome(); o != nil {
			return o, f
		}
		if m.phase == micStart && m.goAt < 0 {
			r.Act(0) // Start
		}
		if m.phase == micClap && m.tries > asked && claps != nil {
			asked = m.tries
			var ts []string
			for k := 0; k < claps(m.claps); k++ {
				ts = append(ts, strconv.FormatFloat(tt+1+0.4*float64(k), 'f', 2, 64))
			}
			f.Set("alsa:claps", f.Read("alsa:claps")+" "+strings.Join(ts, " "))
		}
	}
	return r.Outcome(), f
}

func TestMicRuns(t *testing.T) {
	noLoop := func(f *Fake) { delete(f.Files, "alsa:loop") } // the microphone doesn't hear the speakers
	right := func(n int) int { return n }
	cases := []struct {
		name   string
		script func(*Fake)
		claps  func(n int) int
		picks  map[float64]int
		want   string
	}{
		{"every note heard, the pause quiet", nil, nil, nil, "supported/automatic"},
		// laptop speakers that don't reach C4 (262 Hz): the one note allowed to go unheard
		{"C4 too low for the speakers", func(f *Fake) { f.Set("alsa:lowcut", "300") }, nil, nil, "supported/automatic"},
		// not G4 (392 Hz) as well: two notes missed is the claps (none here)
		{"C4 and G4 too low for the speakers", func(f *Fake) { f.Set("alsa:lowcut", "450") }, nil, nil, "partial/automatic"},
		{"C4 and G4 too low, claps counted", func(f *Fake) { f.Set("alsa:lowcut", "450") }, right, nil, "supported/challenge"},
		// the C4 exception doesn't excuse a note still sounding into the pause
		{"C4 too low, a note still sounding in the pause", func(f *Fake) {
			f.Set("alsa:lowcut", "300")
			f.Set("alsa:hold", "1")
		}, nil, nil, "partial/automatic"},
		// false-pass route: a device stuck replaying keeps a note sounding into the pause; the
		// control round hears it, so the notes don't count (claps instead; none here)
		{"a note still sounding in the pause", func(f *Fake) { f.Set("alsa:hold", "1") }, nil, nil, "partial/automatic"},
		{"no tone, claps counted", noLoop, right, nil, "supported/challenge"},
		{"no tone, one clap short, then right", noLoop, func() func(int) int {
			short := true
			return func(n int) int {
				defer func() { short = false }()
				if short {
					return n - 1
				}
				return n
			}
		}(), nil, "supported/challenge"},
		{"no speakers, claps counted", func(f *Fake) {
			f.Set("/proc/asound/pcm", "00-00: CS4206 Analog : CS4206 Analog : capture 1")
		}, right, nil, "supported/challenge"},
		{"headphones in, then out", func(f *Fake) {
			f.Set("alsa:card0:Headphone Jack", "on")
			f.SetAt(4, "alsa:card0:Headphone Jack", "off")
		}, nil, nil, "supported/automatic"},
		// false-pass routes: a level that rises without the tone or the claps is never a pass
		{"noise burst during the notes, no claps", func(f *Fake) {
			noLoop(f)
			f.SetAt(5, "alsa:mic", "-20")
			f.SetAt(7, "alsa:mic", "-58")
		}, nil, nil, "partial/automatic"},
		{"a bump during the claps", noLoop, func(int) int { return 1 }, nil, "partial/automatic"},
		{"too many claps", noLoop, func(n int) int { return n + 1 }, nil, "partial/automatic"},
		{"digital silence", func(f *Fake) { f.Set("alsa:mic", "flat") }, right, nil, "failed/automatic"},
		// the claps missed twice, and the tester says the level bar stayed put
		{"the bar stayed put", noLoop, nil, map[float64]int{26: 1}, "failed/observed"},
		{"no capture device", func(f *Fake) {
			f.Set("/proc/asound/pcm", "00-00: CS4206 Analog : CS4206 Analog : playback 1")
		}, nil, nil, "failed/automatic"},
	}
	for _, c := range cases {
		o, f := micRun(t, c.script, c.claps, c.picks)
		if got := status(o); got != c.want {
			ev := ""
			if o != nil && len(o.Results) > 0 {
				ev = o.Results[0].Evidence
			}
			t.Errorf("%s: %s, want %s (%s)", c.name, got, c.want, ev)
		}
		if n, _ := f.Int("alsa:open"); n != 0 {
			t.Errorf("%s: %d sounds left open", c.name, n)
		}
	}
	// the C4 exception says so in the evidence
	o, _ := micRun(t, func(f *Fake) { f.Set("alsa:lowcut", "300") }, nil, nil)
	if o == nil || !strings.Contains(o.Results[0].Evidence, "C4 not heard: too low for these speakers") {
		t.Errorf("C4 missed: evidence doesn't say so: %+v", o)
	}
}
