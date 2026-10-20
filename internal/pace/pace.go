// Package pace plays check results back at a readable pace.
//
// Checks run as fast as they can and report when they really started and finished. The screen
// shows each result's log line at the later of its arrival and the previous line plus a minimum
// gap (longer for a check with a band effect, so the effect can be seen). A check that is still
// running is shown running (its band effect) from the later of its real start and the last line
// shown, until its own line shows; while one runs, the effects queued ahead of it hold only
// Rules.Face, so a long check's effect covers nearly all of its real run. Boot changes his face at most every
// Rules.Face seconds; results whose lines land close together form a burst that gets one reaction,
// the worst result of the burst, once its last line is shown. The progress bar follows the
// playback. Only the display is paced: finish times are never changed.
//
// Boot's lines: each check opens with its cue, typed as its band starts, and
// its slot lasts until the cue is typed and read. A check with something to say about its result
// (Item.Say: the screen passes it for partial and failed results and fixes) says it when its line
// shows, and the next check waits until that is typed and read. So no line is ever skipped or
// late: the display falls behind the real checks instead, and catches up during the long ones.
//
// Times are seconds on the playback clock, which starts with the pass; a check that finished at
// Finish arrives on screen at Rules.Start + Finish.
package pace

import (
	"math"
	"sort"
)

// Status is a check's result.
type Status int

const (
	Skip    Status = iota // not in this run: no reaction
	Pass                  // ✓
	Live                  // ▲ Omarchy required to test
	Partial               // ~
	Fail                  // ×
)

// Face is Boot's expression, as the playback wants it.
type Face string

const (
	Idle      Face = "idle"
	Work      Face = "work"
	Pleased   Face = "pleased"
	Hmm       Face = "hmm"
	Concerned Face = "concerned"
	Shrug     Face = "shrug"
)

// Reaction is the face for a result, or "" for none.
func Reaction(s Status) Face {
	switch s {
	case Pass:
		return Pleased
	case Live:
		return Shrug
	case Partial:
		return Hmm
	case Fail:
		return Concerned
	}
	return ""
}

func isReaction(f Face) bool { return f == Pleased || f == Shrug || f == Hmm || f == Concerned }

// Rules are the pacing constants.
type Rules struct {
	Start   float64 // results arrive on screen this long after they finish, and no line shows before Start + its hold
	Line    float64 // minimum gap between log lines
	Band    float64 // how long a band effect holds before its line shows
	Face    float64 // minimum time between Boot's face changes; lines closer than this form a burst
	React   float64 // how long a reaction is wanted after its burst's last line
	Read    float64 // reading time after one of Boot's lines is typed (intro, outro)
	CueRead float64 // reading time after a check's cue is typed
	SayRead float64 // reading time after a result comment is typed
	TypeCPS float64 // Boot's typing speed, characters per second
	Step    float64 // resolution of the face timeline
}

// Default is the pacing a run plays at.
var Default = Rules{Start: 1.0, Line: 0.8, Band: 3.0, Face: 1.5, React: 1.2, Read: 0.9, CueRead: 1.4, SayRead: 2.5, TypeCPS: 80, Step: 0.05}

// Item is one finished check.
type Item struct {
	ID     string
	Begin  float64 // when it really started, seconds after the pass started (0: not known)
	Finish float64 // when it really finished, seconds after the pass started; never changed
	Status Status
	Effect bool    // has a band effect: its slot holds Rules.Band (or Hold) instead of Rules.Line
	Hold   float64 // its own effect hold, if not zero
	Cue    string  // Boot's line as it starts, or "" (a check's second result has none)
	Say    string  // Boot's line about its result, or ""
}

// Slot is an item placed on the playback clock: from T0 the screen shows it running (spinner,
// band effect); at Shown its log line appears.
type Slot struct {
	Item
	T0, Shown float64
}

// Arrive is when the item's result reaches the screen.
func (s Slot) arrive(r Rules) float64 { return r.Start + s.Finish }

// Line is one of Boot's lines and when he starts typing it.
type Line struct {
	At   float64
	Text string
	Kind LineKind
}

// LineKind is what a line of Boot's is for: it sets how long it stays.
type LineKind int

const (
	Talk  LineKind = iota // intro and outro
	CueOf                 // a check starting
	SayOf                 // a check's result
)

// Player schedules results as they arrive.
type Player struct {
	r            Rules
	total        int
	intro, outro string
	items        []Item
	slots        []Slot
	says         []Line
	faces        []Face // face per Step, computed lazily from 0
	since        float64
	reacted      int
	runAt        float64 // when the check running now started (Started), or < 0
	runCue       string  // its cue
	next         float64 // the earliest the next slot may start (after the last result comment is read)
}

// New makes a player for a pass of total checks. Boot opens with intro and closes with outro.
func New(r Rules, total int, intro, outro string) *Player {
	p := &Player{r: r, total: total, intro: intro, outro: outro, runAt: -1}
	p.schedule()
	return p
}

// Add records a finished check (in any order) and reschedules.
func (p *Player) Add(it Item) {
	if p.runAt >= 0 && it.Begin >= p.runAt-1e-9 {
		p.runAt = -1 // the running check's result is in
	}
	p.items = append(p.items, it)
	sort.SliceStable(p.items, func(i, j int) bool { return p.items[i].Finish < p.items[j].Finish })
	p.schedule()
}

// Started records that a check began at, seconds after the pass started, and is still running:
// the playback shows it running, with Boot typing cue, from the later of at and the end of the
// last slot until its result arrives (Add with the same Begin).
func (p *Player) Started(at float64, cue string) {
	if p.Complete() {
		return
	}
	p.runAt, p.runCue = at, cue
	p.schedule()
}

// Total is how many checks the pass has.
func (p *Player) Total() int { return p.total }

// Complete reports whether every check of the pass has arrived.
func (p *Player) Complete() bool { return len(p.items) >= p.total }

// Slots is the schedule so far, in display order.
func (p *Player) Slots() []Slot { return p.slots }

// Says is Boot's lines so far.
func (p *Player) Says() []Line { return p.says }

// End is when the last line has shown and Boot's comment on it, if any, has been read (the
// pass's playback is over once Complete).
func (p *Player) End() float64 { return p.next }

// hold is how long item i's slot lasts at least when it starts at t0: Rules.Line, or its band
// effect's hold; that shortens to Rules.Face while a later check is already running at t0, so
// the running check's effect isn't kept waiting behind effects of checks long finished.
// At least, the slot lasts until its cue is typed and read.
func (p *Player) hold(i int, t0 float64) float64 {
	it := p.items[i]
	h := p.r.Line
	if it.Effect {
		h = p.r.Band
		if it.Hold > 0 {
			h = it.Hold
		}
		if p.busyAt(i, t0) {
			h = math.Min(h, p.r.Face)
		}
	}
	if c := p.cue(i); c != "" {
		h = math.Max(h, p.show(Line{Text: c, Kind: CueOf}))
	}
	return h
}

// cue is item i's cue: none for a check's second result (the same known start as the one before).
func (p *Player) cue(i int) string {
	if i > 0 && p.items[i].Begin > 0 && p.items[i].Begin == p.items[i-1].Begin {
		return ""
	}
	return p.items[i].Cue
}

// busyAt reports whether a check after item i (not one that started with it) was running at t0.
func (p *Player) busyAt(i int, t0 float64) bool {
	for _, it := range p.items[i+1:] {
		if it.Begin > p.items[i].Begin && it.Begin <= t0 && it.Finish > t0 {
			return true
		}
	}
	return p.runAt > p.items[i].Begin && p.runAt <= t0
}

// show is how long one of Boot's lines stays before the next may start: typed, then read.
func (p *Player) show(l Line) float64 {
	typed := float64(len([]rune(l.Text))) / p.r.TypeCPS
	switch l.Kind {
	case CueOf:
		return math.Max(p.r.Face, typed+p.r.CueRead)
	case SayOf:
		return math.Max(p.r.Face, typed+p.r.SayRead)
	}
	return math.Max(p.r.Face, typed+p.r.Read)
}

func (p *Player) schedule() {
	p.slots = p.slots[:0]
	p.says = []Line{{At: 0, Text: p.intro, Kind: Talk}}
	acc := p.r.Start
	for i, it := range p.items {
		s := Slot{Item: it, T0: math.Max(acc, it.Begin)}
		s.Shown = math.Max(p.r.Start+it.Finish, s.T0+p.hold(i, s.T0))
		if c := p.cue(i); c != "" {
			p.says = append(p.says, Line{At: s.T0, Text: c, Kind: CueOf})
		}
		acc = s.Shown
		if it.Say != "" {
			l := Line{At: s.Shown, Text: it.Say, Kind: SayOf}
			p.says = append(p.says, l)
			acc += p.show(l)
		}
		p.slots = append(p.slots, s)
	}
	p.next = acc
	if p.runAt >= 0 && p.runCue != "" && !p.Complete() {
		p.says = append(p.says, Line{At: math.Max(acc, p.runAt), Text: p.runCue, Kind: CueOf})
	}
	if p.Complete() && p.outro != "" {
		p.says = append(p.says, Line{At: p.End(), Text: p.outro, Kind: Talk}) // every slot outlasts its cue
	}
	p.faces = p.faces[:0]
}

// burst is a run of lines shown less than Rules.Face apart.
type burst struct {
	first, end float64
	worst      Status
}

// bursts groups the slots known by time t (finished by then) into bursts.
func (p *Player) bursts(t float64) []burst {
	var out []burst
	for _, s := range p.slots {
		if s.Finish > t {
			continue
		}
		if n := len(out); n > 0 && s.Shown-out[n-1].end < p.r.Face {
			out[n-1].end = s.Shown
			if s.Status > out[n-1].worst {
				out[n-1].worst = s.Status
			}
			continue
		}
		out = append(out, burst{first: s.Shown, end: s.Shown, worst: s.Status})
	}
	return out
}

// running reports whether a check is shown as running at t.
func (p *Player) running(t float64) bool {
	known := 0
	for _, s := range p.slots {
		if s.Finish > t {
			continue
		}
		known++
		if t >= s.T0 && t < s.Shown {
			return true
		}
	}
	return known < p.total && t >= p.r.Start
}

// faceTo extends the face timeline to step n. Only what was known at each step is used, so the
// timeline is the same whether results are added as they arrive or all at once.
func (p *Player) faceTo(n int) {
	cur, since, reacted := Idle, math.Inf(-1), -1
	if k := len(p.faces); k > 0 { // carry on from where the timeline stopped
		cur, since, reacted = p.faces[k-1], p.since, p.reacted
	}
	for i := len(p.faces); i <= n; i++ {
		t := float64(i) * p.r.Step
		bs := p.bursts(t)
		bi := -1
		for j := len(bs) - 1; j >= 0; j-- {
			if bs[j].end <= t {
				bi = j
				break
			}
		}
		var want Face
		if bi >= 0 && Reaction(bs[bi].worst) != "" && (bi > reacted || t-bs[bi].end < p.r.React) {
			want = Reaction(bs[bi].worst)
		} else if p.running(t) || inBurst(bs, t) {
			want = Work
		} else {
			want = Idle
		}
		soon := false // a burst ends within Face: no detour, or its reaction would come late
		for _, b := range bs {
			if b.end > t && b.end-t < p.r.Face && Reaction(b.worst) != "" {
				soon = true
			}
		}
		switch {
		case want != cur && t-since >= p.r.Face-1e-9 && !(!isReaction(want) && soon):
			cur, since = want, t
			if isReaction(want) {
				reacted = bi
			}
		case want == cur && isReaction(want):
			reacted = max(reacted, bi)
		}
		p.faces = append(p.faces, cur)
	}
	p.since, p.reacted = since, reacted
}

func inBurst(bs []burst, t float64) bool {
	for _, b := range bs {
		if t >= b.first && t < b.end {
			return true
		}
	}
	return false
}

// Frame is what the screen shows at one moment of the playback.
type Frame struct {
	Current  int     // index of the slot shown as running, or -1
	Running  bool    // no slot is: the check running now (Started) is shown as running
	Local    float64 // seconds into the current slot, or into the running check
	Shown    int     // how many log lines are shown
	Face     Face
	Say      Line    // Boot's current line
	Progress float64 // 0–1, follows the playback
}

// At gives the frame at playback time t.
func (p *Player) At(t float64) Frame {
	f := Frame{Current: -1}
	for i, s := range p.slots {
		if t >= s.Shown {
			f.Shown = i + 1
		} else if t >= s.T0 && f.Current < 0 {
			f.Current, f.Local = i, t-s.T0
		}
	}
	if t0 := math.Max(p.next, p.runAt); f.Current < 0 && p.runAt >= 0 && t >= t0 {
		f.Running, f.Local = true, t-t0
	}
	for _, l := range p.says {
		if l.At <= t {
			f.Say = l
		}
	}
	if t >= 0 {
		n := int(t / p.r.Step)
		if n >= len(p.faces) {
			p.faceTo(n)
		}
		f.Face = p.faces[n]
	} else {
		f.Face = Idle
	}
	if p.total > 0 {
		done := float64(f.Shown)
		if f.Current >= 0 {
			s := p.slots[f.Current]
			done += f.Local / (s.Shown - s.T0)
		}
		f.Progress = math.Min(1, math.Max(0, done/float64(p.total)))
	}
	return f
}
