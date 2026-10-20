package sounds

import (
	"encoding/binary"
	"math"
)

// The synth is a Web Audio graph done sample by sample: each note an oscillator with a
// 6 ms attack and a release (a shaped note swells or fades to its End over its length, then
// releases in 12 ms; any other holds, then releases in 35 ms), a slide an exponential glide;
// the notes mixed at 0.12 through a 3.2 kHz low-pass that takes the edge off the pulse voice (a
// bright option skips it). Every note starts and ends at zero, so nothing clicks.

const (
	Rate   = 48000 // what the audio tests play at: every codec and HDMI sink takes it
	gain   = 0.12
	cutoff = 3200.0
	q      = 1.122 // Web Audio's default Q for a low-pass, 1 dB
	// lead is the silence before a sound, so a codec waking from power saving doesn't swallow its
	// start (the audio tests' lead is 0.3 s; a blip can't wait that long)
	lead = 0.25
	tail = 0.05
)

// WAV is option o as a 16-bit stereo WAV at Rate, for aplay.
func WAV(o Option) []byte {
	length := 0.0
	for _, l := range o.Layers {
		t := 0.0
		for _, nt := range l {
			t += nt.Dur
		}
		length = max(length, t)
	}
	buf := make([]float64, int((lead+length+0.02+tail)*Rate))
	level := o.Level
	if level == 0 {
		level = 1
	}
	for _, l := range o.Layers {
		t := lead
		for _, nt := range l {
			if nt.Midi > 0 {
				nt.Vol *= level
				if nt.End >= 0 {
					nt.End *= level
				}
				addNote(buf, t, nt)
			}
			t += nt.Dur
		}
	}
	if !o.Bright {
		lowpass(buf)
	}
	s := make([]int16, 2*len(buf))
	for i, v := range buf {
		x := int16(max(-32767, min(32767, math.Round(v*gain*32767))))
		s[2*i], s[2*i+1] = x, x
	}
	return wavBytes(s)
}

func hz(midi float64) float64 { return 440 * math.Pow(2, (midi-69)/12) }

// addNote adds one note into buf from at seconds.
func addNote(buf []float64, at float64, nt Note) {
	f0, f1 := hz(nt.Midi), hz(nt.Midi)
	if nt.To > 0 {
		f1 = hz(nt.To)
	}
	i0 := int(at * Rate)
	n := int((nt.Dur + 0.02) * Rate)
	phase := 0.0
	for k := 0; k < n && i0+k < len(buf); k++ {
		t := float64(k) / Rate
		f := f1
		if t < nt.Dur {
			f = f0 * math.Pow(f1/f0, t/nt.Dur)
		}
		buf[i0+k] += osc(nt.Wave, phase, f) * env(nt, t)
		phase += f / Rate
		phase -= math.Floor(phase)
	}
}

// env is the note's volume t seconds in, as the gain ramps make it.
func env(nt Note, t float64) float64 {
	d, v := nt.Dur, nt.Vol
	switch {
	case d <= 0.007:
		return 0
	case t < 0.006:
		return v * t / 0.006
	case t >= d:
		return 0
	case nt.End >= 0:
		at := max(0.007, d-0.012)
		if t < at {
			return v + (nt.End-v)*(t-0.006)/(at-0.006)
		}
		return nt.End * (d - t) / (d - at)
	default:
		at := max(0.007, d-0.035)
		if t < at {
			return v
		}
		return v * (d - t) / (d - at)
	}
}

// osc is a voice at phase (0–1) for a note of f Hz: sine, triangle, or the 25 % pulse as Web
// Audio's periodic wave makes it (its first 31 harmonics, normalised to a peak of 1), kept
// under the Nyquist frequency.
func osc(w Wave, phase, f float64) float64 {
	switch w {
	case Sine:
		return math.Sin(2 * math.Pi * phase)
	case Pulse:
		v := 0.0
		for k := 1; k < 32 && float64(k)*f < Rate/2; k++ {
			v += pulseAmp[k] * math.Sin(2*math.Pi*float64(k)*phase)
		}
		return v / pulsePeak
	}
	// triangle: 0 at phase 0, up to 1 at a quarter, down to -1 at three quarters
	return 1 - 4*math.Abs(math.Mod(phase+0.75, 1)-0.5)
}

var pulseAmp, pulsePeak = func() ([32]float64, float64) {
	var a [32]float64
	for k := 1; k < 32; k++ {
		a[k] = 2 / (float64(k) * math.Pi) * math.Sin(float64(k)*math.Pi*0.25)
	}
	peak := 0.0
	for i := 0; i < 4096; i++ {
		v := 0.0
		for k := 1; k < 32; k++ {
			v += a[k] * math.Sin(2*math.Pi*float64(k)*float64(i)/4096)
		}
		peak = max(peak, math.Abs(v))
	}
	return a, peak
}()

// lowpass filters buf in place: a biquad low-pass at cutoff (the RBJ cookbook's, as Web Audio's
// BiquadFilterNode is).
func lowpass(buf []float64) {
	w := 2 * math.Pi * cutoff / Rate
	alpha := math.Sin(w) / (2 * q)
	cw := math.Cos(w)
	a0 := 1 + alpha
	b0, b1, b2 := (1-cw)/2/a0, (1-cw)/a0, (1-cw)/2/a0
	a1, a2 := -2*cw/a0, (1-alpha)/a0
	var x1, x2, y1, y2 float64
	for i, x := range buf {
		y := b0*x + b1*x1 + b2*x2 - a1*y1 - a2*y2
		x2, x1, y2, y1 = x1, x, y1, y
		buf[i] = y
	}
}

func wavBytes(s []int16) []byte {
	const ch = 2
	n := len(s) * 2
	b := make([]byte, 44+n)
	copy(b, "RIFF")
	binary.LittleEndian.PutUint32(b[4:], uint32(36+n))
	copy(b[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(b[16:], 16)
	binary.LittleEndian.PutUint16(b[20:], 1) // PCM
	binary.LittleEndian.PutUint16(b[22:], ch)
	binary.LittleEndian.PutUint32(b[24:], Rate)
	binary.LittleEndian.PutUint32(b[28:], Rate*ch*2)
	binary.LittleEndian.PutUint16(b[32:], ch*2)
	binary.LittleEndian.PutUint16(b[34:], 16)
	copy(b[36:], "data")
	binary.LittleEndian.PutUint32(b[40:], uint32(n))
	for i, v := range s {
		binary.LittleEndian.PutUint16(b[44+2*i:], uint16(v))
	}
	return b
}
