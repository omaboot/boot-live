package handson

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"path"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ALSA for the audio tests. What the cards and devices are comes from procfs (/proc/asound),
// read through the IO, so a fixture tree and the Fake answer it too. Playing, recording and the
// mixer go through alsa-utils (aplay, arecord, amixer, alsactl: in the live image from Arch's
// releng package list) rather than PCM ioctls of our own: plughw converts rate, format and
// channels for whatever the codec or HDMI sink takes, and a process is easy to stop for good.
// Nothing else holds the devices on the live image (no PipeWire), so hw devices are free.

// PCM is one ALSA PCM device (card and device numbers), as /proc/asound/pcm lists it.
type PCM struct {
	Card, Dev  int
	ID, Name   string // "CS4206 Analog", "HDMI 0"
	Play, Capt bool
}

// HW is the device as aplay and arecord name it ("hw:0,3").
func (p PCM) HW() string { return fmt.Sprintf("hw:%d,%d", p.Card, p.Dev) }

// HDMI reports whether the PCM is a display output (HDMI or DisplayPort).
func (p PCM) HDMI() bool {
	s := strings.ToUpper(p.ID + " " + p.Name)
	return strings.Contains(s, "HDMI") || strings.Contains(s, "DISPLAYPORT") || strings.HasPrefix(s, "DP")
}

// pcms are the PCM devices: "00-03: HDMI 0 : HDMI 0 : playback 1".
func pcms(io IO) []PCM {
	var out []PCM
	for _, ln := range strings.Split(io.Read("/proc/asound/pcm"), "\n") {
		head, rest, ok := strings.Cut(ln, ": ")
		cs, ds, ok2 := strings.Cut(strings.TrimSpace(head), "-")
		if !ok || !ok2 {
			continue
		}
		card, e1 := strconv.Atoi(cs)
		dev, e2 := strconv.Atoi(ds)
		if e1 != nil || e2 != nil {
			continue
		}
		f := strings.Split(rest, " : ")
		p := PCM{Card: card, Dev: dev, ID: strings.TrimSpace(f[0])}
		if len(f) > 1 {
			p.Name = strings.TrimSpace(f[1])
		}
		for _, x := range f[2:] {
			p.Play = p.Play || strings.HasPrefix(strings.TrimSpace(x), "playback")
			p.Capt = p.Capt || strings.HasPrefix(strings.TrimSpace(x), "capture")
		}
		out = append(out, p)
	}
	return out
}

// codecs are a card's HD Audio codecs ("Cirrus Logic CS4206").
func codecs(io IO, card int) []string {
	var out []string
	for _, f := range io.Glob(fmt.Sprintf("/proc/asound/card%d/codec#*", card)) {
		for _, ln := range strings.Split(io.Read(f), "\n") {
			if c, ok := strings.CutPrefix(ln, "Codec: "); ok {
				out = append(out, strings.TrimSpace(c))
				break
			}
		}
	}
	return out
}

// analogCodec is a card's first codec that isn't a display one, or "".
func analogCodec(io IO, card int) string {
	for _, c := range codecs(io, card) {
		if !strings.Contains(c, "HDMI") && !strings.Contains(c, "DisplayPort") {
			return c
		}
	}
	return ""
}

// builtInPCM picks the built-in codec's PCM for playback (or capture): not a display output, the
// one whose name says prefer ("Speaker", "Headphone", "Mic" on T2 Macs, where each has its own
// device) if there is one, else the first.
func builtInPCM(io IO, capture bool, prefer string) (PCM, bool) {
	var first *PCM
	for _, p := range pcms(io) {
		if p.HDMI() || capture && !p.Capt || !capture && !p.Play {
			continue
		}
		if prefer != "" && strings.Contains(strings.ToLower(p.ID+" "+p.Name), prefer) {
			return p, true
		}
		if first == nil {
			p := p
			first = &p
		}
	}
	if first == nil {
		return PCM{}, false
	}
	return *first, true
}

// describe is a PCM for the evidence: "hw:0,0 (CS4206 Analog, Cirrus Logic CS4206)".
func describe(io IO, p PCM) string {
	s := p.HW() + " (" + p.Name
	if c := analogCodec(io, p.Card); c != "" && !p.HDMI() {
		s += ", " + c
	}
	return s + ")"
}

// ELD is what a display sink told the HDMI/DP codec (/proc/asound/card*/eld#*).
type ELD struct {
	File      string // eld#3.1
	Card      int
	Present   bool // monitor_present
	Valid     bool // eld_valid: the sink sent its audio capabilities
	Monitor   string
	Connected string // connection_type: HDMI, DisplayPort
}

func elds(io IO) []ELD {
	var out []ELD
	for _, f := range io.Glob("/proc/asound/card*/eld#*") {
		e := ELD{File: path.Base(f)}
		fmt.Sscanf(path.Base(path.Dir(f)), "card%d", &e.Card)
		for _, ln := range strings.Split(io.Read(f), "\n") {
			k, v, _ := strings.Cut(strings.TrimSpace(ln), "\t")
			v = strings.TrimSpace(v)
			switch k {
			case "monitor_present":
				e.Present = v == "1"
			case "eld_valid":
				e.Valid = v == "1"
			case "monitor_name":
				e.Monitor = v
			case "connection_type":
				e.Connected = v
			}
		}
		out = append(out, e)
	}
	return out
}

// ── What the tests need beyond IO ─────────────────────────────────────────────

// soundIO plays, records and reads the mixer's jacks. The Mac's (System) runs alsa-utils; a
// fixture run plays nothing (ErrNotReal); the Fake pretends, on its script's clock.
type soundIO interface {
	// Play plays a WAV through the PCM, with the card's mixer unmuted at a moderate level for
	// the time it plays and put back afterwards.
	Play(p PCM, wav []byte) Player
	// Record records the PCM (16 kHz mono) until stopped, with the capture switched on.
	Record(p PCM) Recorder
	// Jacks are the card's jack controls ("Headphone Jack", "HDMI/DP,pcm=3 Jack") and whether
	// each is plugged in; nil when they can't be read.
	Jacks(card int) Future[map[string]bool]
	// Mixer is the card's controls and their values ("Speaker Playback Switch": "on,on",
	// "Auto-Mute Mode": "Enabled"); nil when they can't be read.
	Mixer(card int) Future[map[string]string]
	// Seed is a seed for the tests' random beep counts and words: the Fake's is fixed, so the
	// demo and the screenshots are the same each time.
	Seed() uint64
}

// Player is a sound being played.
type Player interface {
	Done() (error, bool) // finished (nil: played through), without blocking
	Stop()               // stop now; safe to call more than once
	// Setup is how the device took the sound, for the evidence: the hardware's format and
	// channels, its channel map and the mixer controls set ("" until known, or when unknown).
	Setup() string
}

// Recorder is the microphone being recorded.
type Recorder interface {
	Windows() []Window // the 50 ms windows recorded since the last call
	Err() error        // the recording stopped by itself
	Stop()
}

// The mixer is the card's, not a sound's: the first sound on a card saves and sets it, the last
// one to end puts it back. So sounds that overlap (a stopped one still exiting as the next
// starts, or one per HDMI/DP device at once) never save another's settings as the original. A
// sound of the other kind joining (the microphone test's tone, played while it records) adds its
// controls to the saved state's changes, and the last one out puts all of it back.
var (
	mixMu      sync.Mutex
	mixUsers   = map[int]int{}
	mixRestore = map[int]func(){}
	mixSet     = map[int]string{}        // what was set, for the evidence
	mixKinds   = map[int]map[bool]bool{} // the kinds set (capture or playback), once saved
)

// holdMixer sets a card's mixer for a sound (if no other sound of its kind has) and returns its
// release and the controls set ("Master 65%, Speaker 90%").
func holdMixer(card int, capture, hdmi bool) (release func(), set string) {
	mixMu.Lock()
	switch {
	case mixUsers[card] == 0:
		restore, ok := saveMixer(card)
		mixRestore[card], mixSet[card], mixKinds[card] = restore, "not set (no alsactl)", nil
		if ok {
			mixSet[card], mixKinds[card] = applyMixer(card, capture, hdmi), map[bool]bool{capture: true}
		}
	case mixKinds[card] != nil && !mixKinds[card][capture]:
		mixKinds[card][capture] = true
		if more := applyMixer(card, capture, hdmi); more != "" {
			mixSet[card] = strings.Trim(mixSet[card]+"/"+more, "/")
		}
	}
	mixUsers[card]++
	set = mixSet[card]
	mixMu.Unlock()
	return func() {
		mixMu.Lock()
		defer mixMu.Unlock()
		if mixUsers[card]--; mixUsers[card] == 0 {
			mixRestore[card]()
			delete(mixRestore, card)
			delete(mixSet, card)
			delete(mixKinds, card)
		}
	}, set
}

// ErrNoTool: alsa-utils isn't on this image.
var ErrNoTool = errors.New("alsa-utils (aplay, arecord, amixer) not found")

type donePlayer struct{ err error }

func (d donePlayer) Done() (error, bool) { return d.err, true }
func (d donePlayer) Stop()               {}
func (d donePlayer) Setup() string       { return "" }

type doneRecorder struct{ err error }

func (d doneRecorder) Windows() []Window { return nil }
func (d doneRecorder) Err() error        { return d.err }
func (d doneRecorder) Stop()             {}

// ── The Mac: alsa-utils ───────────────────────────────────────────────────────

func (s *System) Seed() uint64 { return uint64(time.Now().UnixNano()) }

func (s *System) Play(p PCM, wav []byte) Player { return s.play(p, wav, false) }

// play plays wav through p: a test's sound (Play), or one of Boot's (BootSound). A device takes
// one sound at a time, so a test's sound first stops Boot's and waits for its aplay to let go
// (else the test's own would fail, the device busy), and Boot's wait for the tests' (testSounds).
func (s *System) play(p PCM, wav []byte, boot bool) Player {
	if !s.Real() {
		return donePlayer{ErrNotReal}
	}
	if _, err := exec.LookPath("aplay"); err != nil {
		return donePlayer{ErrNoTool}
	}
	ctx, cancel := context.WithCancel(context.Background())
	pl := &procPlayer{cancel: cancel, done: make(chan error, 1), exited: make(chan struct{})}
	if !boot {
		testSounds.Add(1)
	}
	go func() {
		defer close(pl.exited)
		if !boot {
			defer testSounds.Add(-1)
			stopBoot()
		}
		release, mixed := holdMixer(p.Card, false, p.HDMI())
		defer release()
		var stderr bytes.Buffer
		cmd := exec.CommandContext(ctx, "aplay", "-q", "-D", "plug"+p.HW(), "-")
		cmd.Stdin, cmd.Stderr = bytes.NewReader(wav), &stderr
		err := cmd.Start()
		if err == nil {
			// how the device took the stream, read while it's open (the lead's silence)
			select {
			case <-time.After(150 * time.Millisecond):
				pl.setup(s.pcmSetup(p, mixed))
			case <-ctx.Done():
			}
			err = cmd.Wait()
		}
		if ctx.Err() != nil {
			err = nil // stopped on purpose
		} else if err != nil {
			err = fmt.Errorf("aplay %s: %v %s", p.HW(), err, strings.TrimSpace(stderr.String()))
		}
		pl.done <- err
	}()
	return pl
}

type procPlayer struct {
	cancel context.CancelFunc
	done   chan error
	exited chan struct{} // closed once aplay is gone and the mixer is back
	err    error
	over   bool
	mu     sync.Mutex
	set    string
}

func (p *procPlayer) setup(s string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.set = s
}

func (p *procPlayer) Setup() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.set
}

// pcmSetup is how an open playback device took the stream: the hardware parameters ALSA chose
// (/proc/asound/cardC/pcmDp/sub0/hw_params), the channel map the driver reports for it (the
// 'Playback Channel Map' control: which speaker each channel of the stream goes to) and the
// mixer controls set. The left/right question is answered
// from this: channel 1 of the WAV is the left one, and the map says where the driver sends it.
func (s *System) pcmSetup(p PCM, mixed string) string {
	var parts []string
	hw := map[string]string{}
	for _, ln := range strings.Split(s.Read(fmt.Sprintf("/proc/asound/card%d/pcm%dp/sub0/hw_params", p.Card, p.Dev)), "\n") {
		if k, v, ok := strings.Cut(ln, ":"); ok {
			hw[strings.TrimSpace(k)] = strings.Fields(v + " ?")[0]
		}
	}
	if hw["channels"] != "" {
		parts = append(parts, fmt.Sprintf("opened as %s ch %s %s Hz", hw["channels"], hw["format"], hw["rate"]))
	} else {
		parts = append(parts, "hw_params not readable")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "amixer", "-c", strconv.Itoa(p.Card), "cget",
		fmt.Sprintf("iface=PCM,name='Playback Channel Map',device=%d", p.Dev)).Output()
	if m := parseChmap(string(out)); err == nil && m != "" {
		parts = append(parts, "channel map "+m)
	} else {
		parts = append(parts, "no channel map control")
	}
	if mixed != "" {
		parts = append(parts, "mixer "+mixed)
	}
	return strings.Join(parts, ", ")
}

// chmapPos are ALSA's channel positions (SNDRV_CHMAP_*), as amixer prints their numbers.
var chmapPos = []string{"UNKNOWN", "NA", "MONO", "FL", "FR", "RL", "RR", "FC", "LFE", "SL", "SR", "RC",
	"FLC", "FRC", "RLC", "RRC", "FLW", "FRW", "FLH", "FCH", "FRH", "TC", "TFL", "TFR", "TFC", "TRL",
	"TRR", "TRC", "TFLC", "TFRC", "TSL", "TSR", "LLFE", "RLFE", "BC", "BLC", "BRC"}

// parseChmap reads amixer cget's channel map: the current one (": values=3,4" is FL,FR), "".
func parseChmap(s string) string {
	for _, ln := range strings.Split(s, "\n") {
		v, ok := strings.CutPrefix(strings.TrimSpace(ln), ": values=")
		if !ok {
			continue
		}
		var names []string
		for _, x := range strings.Split(v, ",") {
			n, err := strconv.Atoi(strings.TrimSpace(x))
			switch {
			case err != nil:
				return ""
			case n >= 0 && n < len(chmapPos):
				names = append(names, chmapPos[n])
			default:
				names = append(names, strconv.Itoa(n))
			}
		}
		return strings.Join(names, ",")
	}
	return ""
}

func (p *procPlayer) Done() (error, bool) {
	if !p.over {
		select {
		case p.err = <-p.done:
			p.over = true
		default:
		}
	}
	return p.err, p.over
}

// Stop kills aplay; the mixer is put back by the player's goroutine as aplay exits.
func (p *procPlayer) Stop() { p.cancel() }

func (s *System) Record(p PCM) Recorder {
	if !s.Real() {
		return doneRecorder{ErrNotReal}
	}
	if _, err := exec.LookPath("arecord"); err != nil {
		return doneRecorder{ErrNoTool}
	}
	ctx, cancel := context.WithCancel(context.Background())
	r := &procRecorder{cancel: cancel}
	go func() {
		release, _ := holdMixer(p.Card, true, false)
		defer release()
		var stderr bytes.Buffer
		cmd := exec.CommandContext(ctx, "arecord", "-q", "-D", "plug"+p.HW(), "-f", "S16_LE", "-r", "16000", "-c", "1", "-t", "raw")
		cmd.Stderr = &stderr
		out, err := cmd.StdoutPipe()
		if err == nil {
			err = cmd.Start()
		}
		if err != nil {
			r.fail(fmt.Errorf("arecord %s: %v", p.HW(), err))
			return
		}
		buf := make([]byte, 1600) // 800 samples: 50 ms at 16 kHz
		for {
			if _, err := io.ReadFull(out, buf); err != nil {
				break
			}
			s := make([]int16, len(buf)/2)
			for i := range s {
				s[i] = int16(uint16(buf[2*i]) | uint16(buf[2*i+1])<<8)
			}
			r.add(windowOf(s))
		}
		err = cmd.Wait()
		if ctx.Err() == nil {
			r.fail(fmt.Errorf("arecord %s stopped: %v %s", p.HW(), err, strings.TrimSpace(stderr.String())))
		}
	}()
	return r
}

type procRecorder struct {
	cancel context.CancelFunc
	mu     sync.Mutex
	ws     []Window
	err    error
}

func (r *procRecorder) add(w Window) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.ws) < 1200 { // a minute nobody read: stop keeping more
		r.ws = append(r.ws, w)
	}
}

func (r *procRecorder) fail(err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.err = err
}

func (r *procRecorder) Windows() []Window {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := r.ws
	r.ws = nil
	return out
}

func (r *procRecorder) Err() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.err
}

func (r *procRecorder) Stop() { r.cancel() }

func (s *System) Jacks(card int) Future[map[string]bool] {
	if !s.Real() {
		return Now[map[string]bool](nil)
	}
	return Go(func() map[string]bool {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		out, err := exec.CommandContext(ctx, "amixer", "-c", strconv.Itoa(card), "contents").Output()
		if err != nil {
			return nil
		}
		return parseJacks(string(out))
	})
}

// parseJacks reads amixer contents' jack controls:
//
//	numid=21,iface=CARD,name='Headphone Jack'
//	  ; type=BOOLEAN,access=r-------,values=1
//	  : values=on
func parseJacks(s string) map[string]bool {
	out := map[string]bool{}
	name := ""
	sc := bufio.NewScanner(strings.NewReader(s))
	for sc.Scan() {
		ln := strings.TrimSpace(sc.Text())
		switch {
		case strings.HasPrefix(ln, "numid="):
			name = ""
			if i := strings.Index(ln, "name='"); i >= 0 && strings.Contains(ln, "iface=CARD") {
				name = strings.TrimSuffix(ln[i+6:], "'")
			}
		case strings.HasPrefix(ln, ": values=") && strings.HasSuffix(name, " Jack"):
			out[name] = strings.TrimPrefix(ln, ": values=") == "on"
		}
	}
	return out
}

func (s *System) Mixer(card int) Future[map[string]string] {
	if !s.Real() {
		return Now[map[string]string](nil)
	}
	return Go(func() map[string]string {
		out, err := mixRun("amixer", "-c", strconv.Itoa(card), "contents")
		if err != nil {
			return nil
		}
		return parseControls(string(out))
	})
}

// parseControls reads amixer contents' controls and their values, an enumerated control's
// value as its item's name:
//
//	numid=10,iface=MIXER,name='Auto-Mute Mode'
//	  ; type=ENUMERATED,access=rw------,values=1,items=2
//	  ; Item #0 'Disabled'
//	  ; Item #1 'Enabled'
//	  : values=1
func parseControls(s string) map[string]string {
	out := map[string]string{}
	name := ""
	items := map[string]string{}
	for _, ln := range strings.Split(s, "\n") {
		ln = strings.TrimSpace(ln)
		switch {
		case strings.HasPrefix(ln, "numid="):
			name, items = "", map[string]string{}
			if i := strings.Index(ln, "name='"); i >= 0 {
				name = strings.TrimSuffix(ln[i+6:], "'")
			}
		case strings.HasPrefix(ln, "; Item #"):
			if n, item, ok := strings.Cut(strings.TrimPrefix(ln, "; Item #"), " "); ok {
				items[n] = strings.Trim(item, "'")
			}
		case strings.HasPrefix(ln, ": values=") && name != "":
			vs := strings.Split(strings.TrimPrefix(ln, ": values="), ",")
			for i, v := range vs {
				if item, ok := items[v]; ok {
					vs[i] = item
				}
			}
			out[name] = strings.Join(vs, ",")
		}
	}
	return out
}

// speakerState is what the card shows of its built-in speakers while something plays: the
// codec's internal speaker pins (/proc/asound/cardN/codec#*: a "[Fixed] Speaker" pin complex,
// its pin control's output bit and its output amp's mute) and the mixer's speaker switches and
// auto-mute mode, for the evidence. The driver's auto-mute turns a speaker pin's output off when
// headphones go in; Macs whose speaker amp hangs off a GPIO may keep the pin on and still be
// quiet, so this is evidence beside the tester's answer, not a verdict.
func speakerState(io IO, card int, mix map[string]string) string {
	var parts []string
	for _, f := range io.Glob(fmt.Sprintf("/proc/asound/card%d/codec#*", card)) {
		for _, node := range strings.Split(io.Read(f), "Node 0x")[1:] {
			if !strings.Contains(node, "[Pin Complex]") || !strings.Contains(node, "[Fixed] Speaker") {
				continue
			}
			id, _, _ := strings.Cut(node, " ")
			st := "pin-ctls not shown"
			for _, ln := range strings.Split(node, "\n") {
				ln = strings.TrimSpace(ln)
				if v, ok := strings.CutPrefix(ln, "Pin-ctls: "); ok {
					st = "pin-ctls " + strings.TrimRight(v, ": ")
					if strings.Contains(v, "OUT") {
						st += " (output on)"
					} else {
						st += " (output off)"
					}
				}
				if v, ok := strings.CutPrefix(ln, "Amp-Out vals:"); ok { // bit 7 of each channel's value: mute
					vals := strings.Fields(strings.NewReplacer("[", " ", "]", " ").Replace(v))
					muted := len(vals) > 0
					for _, x := range vals {
						n, err := strconv.ParseUint(strings.TrimPrefix(x, "0x"), 16, 8)
						muted = muted && err == nil && n&0x80 != 0
					}
					if muted {
						st += ", amp muted"
					}
				}
			}
			parts = append(parts, "speaker pin 0x"+id+" "+st)
		}
	}
	if len(parts) == 0 {
		parts = append(parts, "no speaker pin in the codec listing")
	}
	var ctls []string
	for name, v := range mix {
		if strings.Contains(name, "Speaker") && strings.HasSuffix(name, "Switch") || name == "Auto-Mute Mode" {
			ctls = append(ctls, name+" "+v)
		}
	}
	sort.Strings(ctls)
	if mix != nil && len(ctls) == 0 {
		ctls = append(ctls, "no speaker switch or auto-mute control")
	}
	return strings.Join(append(parts, ctls...), "; ")
}

// Mixer controls set while a test plays or records (simple controls, mapped volume), the ones
// a card has: outputs unmuted at a moderate level; the capture switched on, with a mild boost.
var (
	playMix = map[string]string{"Master": "65% unmute", "PCM": "90% unmute", "Front": "90% unmute", "Speaker": "90% unmute",
		"Headphone": "90% unmute", "Bass Speaker": "90% unmute"}
	captureMix = map[string]string{"Capture": "80% cap", "Mic Boost": "30%", "Internal Mic Boost": "30%", "Internal Mic": "80% cap", "Mic": "80% cap"}
)

// mixRun runs an alsa-utils command with a timeout.
func mixRun(args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	return exec.CommandContext(ctx, args[0], args[1:]...).Output()
}

// saveMixer saves a card's mixer (alsactl store) and returns what puts it back; ok is false
// when it couldn't be saved, and then nothing may be changed (so nothing is left changed).
func saveMixer(card int) (restore func(), ok bool) {
	f, err := os.CreateTemp("", "boot-live-mixer-*.state")
	if err != nil {
		return func() {}, false
	}
	f.Close()
	cs := strconv.Itoa(card)
	if _, err := mixRun("alsactl", "-f", f.Name(), "store", cs); err != nil {
		os.Remove(f.Name())
		return func() {}, false
	}
	return func() {
		mixRun("alsactl", "-f", f.Name(), "restore", cs)
		os.Remove(f.Name())
	}, true
}

// applyMixer sets the controls a test needs (playback or capture) and returns what it set.
// Best effort: a control the card hasn't is left alone.
func applyMixer(card int, capture, hdmi bool) (set string) {
	cs := strconv.Itoa(card)
	run := mixRun
	out, _ := run("amixer", "-c", cs, "scontrols")
	want := playMix
	if capture {
		want = captureMix
	}
	var did []string
	for _, ln := range strings.Split(string(out), "\n") {
		// Simple mixer control 'Master',0
		i, j := strings.Index(ln, "'"), strings.LastIndex(ln, "'")
		if i < 0 || j <= i {
			continue
		}
		name, idx := ln[i+1:j], strings.TrimPrefix(ln[j+1:], ",")
		ctl := name + "," + idx
		switch v, ok := want[name]; {
		case ok:
			if _, err := run(append([]string{"amixer", "-q", "-M", "-c", cs, "sset", ctl}, strings.Fields(v)...)...); err == nil {
				did = append(did, name+" "+strings.Fields(v)[0])
			}
		case hdmi && name == "IEC958": // each HDMI/DP output's own switch
			run("amixer", "-q", "-c", cs, "sset", ctl, "unmute")
		}
	}
	return strings.Join(did, "/")
}

// ── The Fake ──────────────────────────────────────────────────────────────────
//
// The Fake's ALSA is files with an "alsa:" prefix: "alsa:card0:Headphone Jack" = on|off for a
// jack control, "alsa:mix0:<control>" = a mixer control's value, "alsa:mic" = the microphone's
// room level in dBFS (or "flat" for digital silence), "alsa:loop" = the gain in dB from what
// plays to the microphone (absent: the microphone doesn't hear the speakers), "alsa:claps" =
// the run times (seconds, space-separated) of claps near the microphone, "alsa:hold" = any value
// for a device stuck replaying: what played keeps sounding, looped, after it ended or was
// stopped (the microphone test's control round must catch it), "alsa:lowcut" = a pitch in Hz
// the speakers don't reach: a tone under it plays as silence (laptop speakers and the low C4),
// "alsa:fail" = an error
// for Play and Record, "alsa:seed" = the random seed (default 1). "alsa:played" collects what
// was played, "alsa:open" counts players, recorders and cameras still open (0 once a test has
// stopped).

var (
	_ soundIO = (*System)(nil)
	_ soundIO = (*Fake)(nil)
)

func (f *Fake) Seed() uint64 {
	n, err := strconv.ParseUint(f.Read("alsa:seed"), 10, 64)
	if err != nil {
		return 1
	}
	return n
}

func (f *Fake) opened(d int) {
	n, _ := f.Int("alsa:open")
	f.Set("alsa:open", strconv.Itoa(n+d))
}

func (f *Fake) Play(p PCM, wav []byte) Player {
	if e := f.Read("alsa:fail"); e != "" {
		return donePlayer{errors.New(e)}
	}
	f.Set("alsa:played", f.Files["alsa:played"]+fmt.Sprintf("%s %.2fs\n", p.HW(), wavSeconds(wav)))
	f.opened(1)
	pl := &fakePlayer{f: f, start: f.now, end: f.now + wavSeconds(wav), stop: math.Inf(1)}
	if rate, ch, s, err := parseWAV(wav); err == nil {
		pl.rate = rate
		for i := 0; i < len(s); i += ch {
			pl.left = append(pl.left, s[i])
		}
		if cut, err := strconv.ParseFloat(f.Read("alsa:lowcut"), 64); err == nil && pitch(pl.left, rate) < cut {
			clear(pl.left)
		}
	}
	f.sounds = append(f.sounds, pl)
	return pl
}

// pitch is a sound's frequency in Hz from its upward zero crossings (0 for silence).
func pitch(s []int16, rate int) float64 {
	n, first, last := 0, -1, -1
	for i := 1; i < len(s); i++ {
		if s[i-1] < 0 && s[i] >= 0 {
			if first < 0 {
				first = i
			}
			n, last = n+1, i
		}
	}
	if n < 2 {
		return 0
	}
	return float64(n-1) * float64(rate) / float64(last-first)
}

type fakePlayer struct {
	f                *Fake
	start, end, stop float64 // run times: began, plays to, stopped at
	rate             int
	left             []int16 // channel 1, what the microphone hears of it
	closed           bool
}

// at is the sound playing at run time t (0 before, after, or once stopped; looped on with
// alsa:hold).
func (p *fakePlayer) at(t float64, hold bool) float64 {
	if t < p.start || p.rate == 0 || len(p.left) == 0 {
		return 0
	}
	i := int((t - p.start) * float64(p.rate))
	if hold {
		return float64(p.left[i%len(p.left)]) / 32768
	}
	if t >= p.stop || i >= len(p.left) {
		return 0
	}
	return float64(p.left[i]) / 32768
}

func (p *fakePlayer) Done() (error, bool) {
	if p.f.now >= p.end {
		p.close()
		return nil, true
	}
	return nil, p.closed
}

func (p *fakePlayer) close() {
	if !p.closed {
		p.closed = true
		p.f.opened(-1)
	}
}

func (p *fakePlayer) Stop() {
	if !p.closed {
		p.stop = p.f.now
	}
	p.close()
}

// Setup is what the demo Mac's CS4206 would report.
func (p *fakePlayer) Setup() string {
	return "opened as 2 ch S16_LE 48000 Hz, channel map FL,FR, mixer Master 65%/PCM 90%/Speaker 90%"
}

func (f *Fake) Record(p PCM) Recorder {
	if e := f.Read("alsa:fail"); e != "" {
		return doneRecorder{errors.New(e)}
	}
	f.opened(1)
	return &fakeRecorder{f: f, from: f.now}
}

type fakeRecorder struct {
	f      *Fake
	from   float64 // recorded up to here
	n      int
	closed bool
}

// Windows makes the windows since the last call: noise at the scripted room level with a little
// (repeatable) wobble, plus what plays (at alsa:loop's gain) and the scripted claps.
func (r *fakeRecorder) Windows() []Window {
	if r.closed {
		return nil
	}
	var out []Window
	const n = micRate / 20
	for ; r.from+0.05 <= r.f.now+1e-9; r.from += 0.05 {
		r.n++
		lvl := r.f.Read("alsa:mic")
		if lvl == "flat" {
			out = append(out, windowOf(make([]int16, n)))
			continue
		}
		d, err := strconv.ParseFloat(lvl, 64)
		if err != nil {
			d = -60
		}
		d += float64(r.n*7%5) - 2
		noise := math.Pow(10, d/20) * math.Sqrt(3) // uniform noise of that RMS
		loop := 0.0
		if g, err := strconv.ParseFloat(r.f.Read("alsa:loop"), 64); err == nil {
			loop = math.Pow(10, g/20)
		}
		hold := r.f.Read("alsa:hold") != ""
		var claps []float64
		for _, c := range strings.Fields(r.f.Read("alsa:claps")) {
			if tc, err := strconv.ParseFloat(c, 64); err == nil {
				claps = append(claps, tc)
			}
		}
		s := make([]int16, n)
		seed := uint32(r.n*2654435761) | 1
		for i := range s {
			t := r.from + float64(i)/micRate
			seed ^= seed << 13
			seed ^= seed >> 17
			seed ^= seed << 5
			u := float64(seed)/float64(1<<32)*2 - 1
			v := u * noise
			for _, pl := range r.f.sounds {
				v += loop * pl.at(t, hold)
			}
			for _, tc := range claps {
				if dt := t - tc; dt >= 0 && dt < 0.3 {
					v += 0.4 * math.Exp(-dt/0.03) * math.Copysign(1, u)
				}
			}
			s[i] = int16(max(-32768, min(32767, v*32768)))
		}
		out = append(out, windowOf(s))
	}
	return out
}

func (r *fakeRecorder) Err() error { return nil }

func (r *fakeRecorder) Stop() {
	if !r.closed {
		r.closed = true
		r.f.opened(-1)
	}
}

func (f *Fake) Jacks(card int) Future[map[string]bool] {
	out := map[string]bool{}
	pre := fmt.Sprintf("alsa:card%d:", card)
	for k, v := range f.Files {
		if name, ok := strings.CutPrefix(k, pre); ok {
			out[name] = strings.TrimSpace(v) == "on"
		}
	}
	return timed[map[string]bool]{f, f.now, out}
}

func (f *Fake) Mixer(card int) Future[map[string]string] {
	out := map[string]string{}
	pre := fmt.Sprintf("alsa:mix%d:", card)
	for k, v := range f.Files {
		if name, ok := strings.CutPrefix(k, pre); ok {
			out[name] = strings.TrimSpace(v)
		}
	}
	return timed[map[string]string]{f, f.now, out}
}

// demoCodec is the demo Mac's Cirrus CS4206 as /proc/asound/card0/codec#0 lists it, cut to its
// internal speaker pin, with that pin's control (0x40: output on; 0x00: auto-muted).
func demoCodec(pinCtl string) string {
	out := "OUT"
	if pinCtl == "0x00" {
		out = ""
	}
	return "Codec: Cirrus Logic CS4206\nAddress: 0\nVendor Id: 0x10134206\n" +
		"Node 0x10 [Pin Complex] wcaps 0x400501: Stereo Amp-Out\n  Amp-Out caps: ofs=0x00, nsteps=0x00, stepsize=0x00, mute=1\n" +
		"  Amp-Out vals:  [0x00 0x00]\n  Pincap 0x00000010: OUT\n  Pin Default 0x90100110: [Fixed] Speaker at Int N/A\n" +
		"  Pin-ctls: " + pinCtl + ": " + out + "\n  Connection: 1\n     0x02\n"
}

// demoSound is the demo Mac's sound as ALSA shows it: a MacBook Air (13-inch, Mid 2012)'s
// HDA Intel PCH card with the Cirrus CS4206 and the PantherPoint HDMI codec, nothing plugged in.
func demoSound(f *Fake) {
	f.Set("/proc/asound/cards", " 0 [PCH            ]: HDA-Intel - HDA Intel PCH\n                      HDA Intel PCH at 0xa0610000 irq 52")
	f.Set("/proc/asound/pcm", "00-00: CS4206 Analog : CS4206 Analog : playback 1 : capture 1\n00-03: HDMI 0 : HDMI 0 : playback 1\n"+
		"00-07: HDMI 1 : HDMI 1 : playback 1\n00-08: HDMI 2 : HDMI 2 : playback 1")
	f.Set("/proc/asound/card0/codec#0", demoCodec("0x40"))
	f.Set("/proc/asound/card0/codec#3", "Codec: Intel PantherPoint HDMI\nAddress: 3\nVendor Id: 0x80862806")
	for i, pin := range []string{"3.0", "3.1", "3.2"} {
		f.Set("/proc/asound/card0/eld#"+pin, "monitor_present\t\t0\neld_valid\t\t0")
		f.Set(fmt.Sprintf("alsa:card0:HDMI/DP,pcm=%d Jack", []int{3, 7, 8}[i]), "off")
	}
	f.Set("alsa:card0:Headphone Jack", "off")
	f.Set("alsa:mix0:Speaker Playback Switch", "on,on")
	f.Set("alsa:mix0:Headphone Playback Switch", "on,on")
	f.Set("alsa:mix0:Auto-Mute Mode", "Enabled")
	f.Set("alsa:mic", "-58")
}
