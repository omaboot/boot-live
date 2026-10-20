package handson

import (
	"encoding/binary"
	"errors"
	"math"
	"sort"
)

// The sounds the audio tests play, made here: beeps generated on the fly (no sound files for
// tones), the spoken word from the built-in clips (assets/audio), and the
// level maths for the microphone. Everything is 16-bit PCM in a WAV, which aplay takes as is.

// Sides a sound plays on.
const (
	sideBoth = iota
	sideLeft
	sideRight
)

const (
	beepRate  = 48000 // HDMI sinks all take 48 kHz; the analog codecs too
	beepHz    = 700   // the usual Morse practice pitch: clear on small laptop speakers, not shrill
	beepLen   = 0.15  // seconds of tone
	beepEvery = 0.45  // seconds from one beep to the next: easy to count
	beepEdge  = 0.015 // raised-cosine attack and release, so nothing clicks
	beepLevel = 0.25  // of full scale (−12 dBFS): gentle, with the mixer at a moderate level
	// The headphones' beeps, 12 dB under the speakers' (−24 dBFS): with the mixer as the tests
	// set it (Master 65%, Headphone 90% mapped: about −14 dB), some 38 dB under the jack's
	// loudest, which is around 65–70 dB SPL in sensitive earbuds (too loud).
	headphoneLevel = 0.063
	wordPeak       = 0.45 // the word's peak, of full scale
	wordGap        = 0.7  // seconds between the word's two plays
	// The microphone test's notes: 1.5 s each, at −18 dBFS (6 dB under the beeps), faded in and
	// out over 0.12 s so they start and end softly (not harsh).
	toneLen   = 1.5
	noteLevel = 0.125
	noteFade  = 0.12
	motifEdge = 0.03 // the encore's notes' edges, and their overlap: a cross-fade
)

// Leads: silence before the sound, so a codec waking up (or an HDMI sink locking on to the
// stream, which takes the better part of a second) doesn't swallow the first beep.
const (
	leadAnalog = 0.3
	leadHDMI   = 1.2
)

// beeps is n beeps on side at level (of full scale), after lead seconds of silence, as a 16-bit
// stereo WAV: channel 1 is the left, channel 2 the right (WAV's order, which ALSA keeps: FL, FR).
func beeps(n, side int, lead, level float64) []byte {
	total := lead + float64(n-1)*beepEvery + beepLen + 0.2
	s := make([]int16, int(total*beepRate)*2)
	for k := 0; k < n; k++ {
		addTone(s, lead+float64(k)*beepEvery, beepLen, beepHz, level, side)
	}
	return wavBytes(beepRate, 2, s)
}

// addTone writes a tone of hz into stereo samples s (at beepRate) from at seconds for secs, on
// side, with raised-cosine edges so nothing clicks.
func addTone(s []int16, at, secs, hz, level float64, side int) {
	addToneEdge(s, at, secs, hz, level, beepEdge, side)
}

// addToneEdge is addTone with edges edge seconds long. The tone is mixed into what s holds, so
// overlapping notes cross-fade: written over, the next note cut the last one's fade short with a
// pop at every note (a MacBook Air, 0.8.1 dev, the microphone's encore).
func addToneEdge(s []int16, at, secs, hz, level, edgeSecs float64, side int) {
	frames, from, tone := len(s)/2, int(at*beepRate), int(secs*beepRate)
	edge := edgeSecs * beepRate
	for i := 0; i < tone && from+i < frames; i++ {
		env := 1.0
		if fi := float64(i); fi < edge {
			env = 0.5 - 0.5*math.Cos(math.Pi*fi/edge)
		} else if fe := float64(tone - 1 - i); fe < edge {
			env = 0.5 - 0.5*math.Cos(math.Pi*fe/edge)
		}
		v := level * env * math.Sin(2*math.Pi*hz*float64(i)/beepRate) * 32767
		if side != sideRight {
			s[2*(from+i)] = mix(s[2*(from+i)], v)
		}
		if side != sideLeft {
			s[2*(from+i)+1] = mix(s[2*(from+i)+1], v)
		}
	}
}

// mix adds v to the sample a, kept in range.
func mix(a int16, v float64) int16 {
	return int16(max(-32768, min(32767, float64(a)+v)))
}

// toneWAV is one soft note of hz on both sides for toneLen seconds (faded in and out over
// noteFade) after lead seconds of silence: what the microphone test plays through the speakers
// to find in its recording.
func toneWAV(hz, lead float64) []byte {
	s := make([]int16, int((lead+toneLen+0.2)*beepRate)*2)
	addToneEdge(s, lead, toneLen, hz, noteLevel, noteFade, sideBoth)
	return wavBytes(beepRate, 2, s)
}

// motifWAV is the five notes again, the way the film plays them ("just for fun"): quicker, joined,
// the low C a little longer and the G held, after lead seconds of
// silence. It plays once the notes have passed, after the recording; nothing listens to it.
func motifWAV(lead float64) []byte {
	lens := [micN]float64{0.42, 0.42, 0.42, 0.62, 1.5}
	total := lead + 0.2
	for _, l := range lens {
		total += l
	}
	s := make([]int16, int(total*beepRate)*2)
	at := lead
	for i, n := range micNotes {
		addToneEdge(s, at, lens[i]+motifEdge, n.hz, noteLevel, motifEdge, sideBoth) // each fades out as the next fades in: legato
		at += lens[i]
	}
	return wavBytes(beepRate, 2, s)
}

// wordWAV is a word's clip as a stereo WAV: scaled to a gentle peak, said twice with a gap,
// after lead seconds of silence.
func wordWAV(clip []byte, lead float64) ([]byte, error) {
	rate, ch, s, err := parseWAV(clip)
	if err != nil {
		return nil, err
	}
	mono := make([]float64, len(s)/ch)
	peak := 0.0
	for i := range mono {
		v := 0.0
		for c := 0; c < ch; c++ {
			v += float64(s[i*ch+c])
		}
		mono[i] = v / float64(ch)
		peak = max(peak, math.Abs(mono[i]))
	}
	if peak == 0 {
		return nil, errors.New("the clip is silent")
	}
	g := wordPeak * 32767 / peak
	lf, gf := int(lead*float64(rate)), int(wordGap*float64(rate))
	out := make([]int16, 2*(lf+2*len(mono)+gf+rate/5))
	for rep := 0; rep < 2; rep++ {
		at := lf + rep*(len(mono)+gf)
		for i, v := range mono {
			x := int16(v * g)
			out[2*(at+i)], out[2*(at+i)+1] = x, x
		}
	}
	return wavBytes(rate, 2, out), nil
}

// wavBytes is interleaved 16-bit samples as a WAV file.
func wavBytes(rate, ch int, s []int16) []byte {
	n := len(s) * 2
	b := make([]byte, 44+n)
	copy(b, "RIFF")
	binary.LittleEndian.PutUint32(b[4:], uint32(36+n))
	copy(b[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(b[16:], 16)
	binary.LittleEndian.PutUint16(b[20:], 1) // PCM
	binary.LittleEndian.PutUint16(b[22:], uint16(ch))
	binary.LittleEndian.PutUint32(b[24:], uint32(rate))
	binary.LittleEndian.PutUint32(b[28:], uint32(rate*ch*2))
	binary.LittleEndian.PutUint16(b[32:], uint16(ch*2))
	binary.LittleEndian.PutUint16(b[34:], 16)
	copy(b[36:], "data")
	binary.LittleEndian.PutUint32(b[40:], uint32(n))
	for i, v := range s {
		binary.LittleEndian.PutUint16(b[44+2*i:], uint16(v))
	}
	return b
}

// parseWAV reads a 16-bit PCM WAV: its rate, channels and interleaved samples. Chunks other
// than fmt and data (LIST, fact) are skipped.
func parseWAV(b []byte) (rate, ch int, s []int16, err error) {
	if len(b) < 12 || string(b[0:4]) != "RIFF" || string(b[8:12]) != "WAVE" {
		return 0, 0, nil, errors.New("not a WAV file")
	}
	bits := 0
	for p := 12; p+8 <= len(b); {
		id, size := string(b[p:p+4]), int(binary.LittleEndian.Uint32(b[p+4:]))
		body := b[p+8 : min(len(b), p+8+size)]
		switch id {
		case "fmt ":
			if len(body) < 16 || binary.LittleEndian.Uint16(body) != 1 {
				return 0, 0, nil, errors.New("not 16-bit PCM")
			}
			ch, rate, bits = int(binary.LittleEndian.Uint16(body[2:])), int(binary.LittleEndian.Uint32(body[4:])), int(binary.LittleEndian.Uint16(body[14:]))
		case "data":
			if bits != 16 || ch < 1 || rate <= 0 {
				return 0, 0, nil, errors.New("not 16-bit PCM")
			}
			s = make([]int16, len(body)/2)
			for i := range s {
				s[i] = int16(binary.LittleEndian.Uint16(body[2*i:]))
			}
			return rate, ch, s, nil
		}
		p += 8 + size + size%2 // chunks are padded to even sizes
	}
	return 0, 0, nil, errors.New("no data chunk")
}

// wavSeconds is how long a WAV plays (0 when it isn't one).
func wavSeconds(b []byte) float64 {
	rate, ch, s, err := parseWAV(b)
	if err != nil {
		return 0
	}
	return float64(len(s)/ch) / float64(rate)
}

// ── Levels ────────────────────────────────────────────────────────────────────

// Window is one short stretch of recorded sound (50 ms).
type Window struct {
	RMS, Peak float64 // of full scale, 0–1
	Flat      bool    // every sample the same (digital silence, or a stuck converter)
	S         []int16 // the samples (16 kHz mono), for the tone and the claps
}

// windowOf measures samples.
func windowOf(s []int16) Window {
	if len(s) == 0 {
		return Window{Flat: true}
	}
	var sum float64
	peak, flat := 0.0, true
	for _, v := range s {
		x := float64(v) / 32768
		sum += x * x
		peak = max(peak, math.Abs(x))
		if v != s[0] {
			flat = false
		}
	}
	return Window{RMS: math.Sqrt(sum / float64(len(s))), Peak: peak, Flat: flat, S: s}
}

// dB is a level of full scale in dBFS, floored at −100.
func dB(x float64) float64 {
	if x <= 1e-5 {
		return -100
	}
	return 20 * math.Log10(x)
}

// medianOf is the median of a list (0 when empty).
func medianOf(xs []float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	s := append([]float64(nil), xs...)
	sort.Float64s(s)
	return s[len(s)/2]
}

// ── Finding a tone, counting claps ────────────────────────────────────────────

// micRate is the recordings' sample rate (System.Record asks arecord for it).
const micRate = 16000

// goertzel is the amplitude (of full scale) of the hz component in a window of samples taken
// at micRate, Hann-windowed so a strong tone doesn't leak into frequencies a few hundred Hz away:
// a full-scale sine at hz reads 1, another frequency far less.
func goertzel(s []int16, hz float64) float64 {
	n := len(s)
	if n < 2 {
		return 0
	}
	w := 2 * math.Pi * hz / micRate
	coeff := 2 * math.Cos(w)
	var q1, q2 float64
	for i, v := range s {
		h := 0.5 - 0.5*math.Cos(2*math.Pi*float64(i)/float64(n-1))
		q1, q2 = coeff*q1-q2+h*float64(v)/32768, q1
	}
	re, im := q1-q2*math.Cos(w), q2*math.Sin(w)
	return 4 * math.Hypot(re, im) / float64(n) // the Hann window's coherent gain is ½
}

// Where toneLevel measures a tone's background (re-derived for the five notes): the aroundN
// frequencies nearest the tone on a 50 Hz grid of offsets, from ±100 Hz out, lower first on a tie.
// ±100 Hz is past the windowed tone's main lobe (±40 Hz at 50 ms) and its first side lobes (the
// tone leaks about −50 dB there), close enough to share its noise: a clap, speech or a bump raises
// them as much as the tone's own frequency. Each sits at aroundLow Hz or more, out of the rumble
// where a laptop's microphone and its fan are loudest (for the low C4, 262 Hz, all but one sit
// above it, 162–862 Hz), and aroundHarm Hz or more from the tone's 2nd to 4th harmonics, which
// small speakers add to a low note (C4's 2nd is C5, 523 Hz).
const (
	aroundN    = 8
	aroundLow  = 150.0
	aroundHarm = 60.0
)

// toneAround are the frequencies (Hz) around hz where toneLevel measures its background.
func toneAround(hz float64) []float64 {
	var out []float64
	for d := 100.0; len(out) < aroundN && d < 2000; d += 50 {
		for _, f := range []float64{hz - d, hz + d} {
			if len(out) < aroundN && f >= aroundLow && !nearHarmonic(f, hz) {
				out = append(out, f)
			}
		}
	}
	return out
}

// nearHarmonic: f is within aroundHarm of hz's 2nd, 3rd or 4th harmonic.
func nearHarmonic(f, hz float64) bool {
	for k := 2.0; k <= 4; k++ {
		if math.Abs(f-k*hz) < aroundHarm {
			return true
		}
	}
	return false
}

// toneLevel is one window's level at hz and the median level of the frequencies around it, in
// dBFS.
func toneLevel(s []int16, hz float64) (at, around float64) {
	var ns []float64
	for _, f := range toneAround(hz) {
		ns = append(ns, dB(goertzel(s, f)))
	}
	return dB(goertzel(s, hz)), medianOf(ns)
}

// The tone's thresholds: a window hears it when its level at the tone's frequency is at least
// toneOverAround dB over the frequencies around it, toneOverRoom dB over the room's own level
// at that frequency (a fan's or the mains' steady tone can't pass) and louder than toneMin;
// a tone is found when toneHits windows (0.4 s of its 1.5 s) hear it (noteHits: and it's the
// loudest of the notes).
const (
	toneOverAround = 10.0
	toneOverRoom   = 10.0
	toneMin        = -75.0
	toneHits       = 8
)

// toneHit is the rule for one window: its level at the tone's frequency, the frequencies
// around it, and the room's level at the tone's frequency, all dBFS.
func toneHit(at, around, room float64) bool {
	return at-around >= toneOverAround && at-room >= toneOverRoom && at >= toneMin
}

// noteHits are the notes one window hears: toneHit at the note's frequency, and the note the
// loudest of the notes in the window. The notes sit as close as 64 Hz (C5 and D5): a strong one
// leaks 35 dB or so under its own level into its neighbour, which can still be 10 dB over the
// background there; and C4 through small speakers often comes out mostly as its 2nd harmonic,
// which is C5. Only the loudest counts, so one note never reads as another.
func noteHits(at, around, room [micN]float64) (hit [micN]bool) {
	for i := range at {
		if !toneHit(at[i], around[i], room[i]) {
			continue
		}
		hit[i] = true
		for j := range at {
			if j != i && at[j] > at[i] {
				hit[i] = false
			}
		}
	}
	return hit
}

// clapFrame is the claps' envelope resolution: the peak of each 10 ms of the recording.
const clapFrame = micRate / 100

// frames are a window's 10 ms peaks in dBFS.
func frames(w Window) []float64 {
	var out []float64
	for i := 0; i+clapFrame <= len(w.S); i += clapFrame {
		p := 0.0
		for _, v := range w.S[i : i+clapFrame] {
			p = max(p, math.Abs(float64(v))/32768)
		}
		out = append(out, dB(p))
	}
	return out
}

// The claps' thresholds: a clap is the 10 ms peaks rising to at least clapOver dB over the
// room's (and at least clapMin dBFS); another counts only clapGap frames (120 ms) later and
// once the level has fallen clapDrop dB under the last one's top, or back under the threshold.
const (
	clapOver = 15.0
	clapMin  = -55.0
	clapGap  = 12
	clapDrop = 12.0
)

// clapThreshold is the level a clap must reach, from the room's median 10 ms peak (dBFS).
func clapThreshold(roomPeak float64) float64 { return max(roomPeak+clapOver, clapMin) }

// countClaps counts the distinct peaks in an envelope of 10 ms peaks (dBFS) over thr, and the
// frame where the last one began (-1 for none).
func countClaps(env []float64, thr float64) (n, last int) {
	last = -1
	armed, top := true, -100.0
	for i, v := range env {
		switch {
		case armed && v >= thr && (last < 0 || i-last >= clapGap):
			n, last, armed, top = n+1, i, false, v
		case !armed:
			top = max(top, v)
			if v < thr || v < top-clapDrop {
				armed = true
			}
		}
	}
	return n, last
}
