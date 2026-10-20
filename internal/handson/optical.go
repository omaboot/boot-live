package handson

import (
	"fmt"
	"math"
	"strings"
	"sync"
	"time"

	"github.com/omaboot/boot-live/internal/boot"
	"github.com/omaboot/boot-live/internal/checks"
	g "github.com/omaboot/boot-live/internal/grid"
	"github.com/omaboot/boot-live/internal/i18n"
	"github.com/omaboot/boot-live/internal/sysroot"
)

// storage.optical, "Internal optical drive reads discs": the person
// inserts any CD or DVD; the machine asks the drive for its state (CDROM_DRIVE_STATUS, polled
// twice a second) until it has a disc, asks what kind it is (CDROM_DISC_STATUS), reads it, and
// ejects it (CDROMEJECT after unlocking the door: slot-loading drives have no button, so software
// ejects them), then asks the drive until it reports the disc gone (0.7.3 review: an eject that
// answered fine left the disc in an iMac10,1's drive). A data disc (or a mixed one) has its first
// MB read through the drive's block device, opened read-only; an audio CD has its table of
// contents read (its tracks and length). Nothing is mounted, and nothing is ever written: the
// device is only ever opened O_RDONLY|O_NONBLOCK (optical_linux.go). The quick pass has already
// found the drive (checks/optical.go); the test is offered only when it did.
//
//   - supported: the disc read (method automatic) and came back out;
//   - partial: the disc read and wouldn't come out (0.7.3 follow-ups);
//   - failed: the drive took a disc and couldn't read it twice (two discs: a scratched one
//     shouldn't fail the drive), or the tester inserted one and the drive never reported one
//     ("Nothing happens", observed, after 25 s);
//   - not_tested (no-equipment): skipped, "no disc".
//
// The disc is ejected whenever the test ends (0.7.3 review): after its read, and when the test is
// left any other way (skipped, "Nothing happens", Backspace, Esc) with a disc in the drive; a
// read under way is stopped at its next chunk first (the eject waits for it). "Eject disc" in the
// answers gives it back at any time but while it's being read. A Mac that sleeps with a disc in
// the drive or a read under way can hang (an iMac10,1 did): the tests that sleep wait for the
// drive (discguard.go).
//
// A disc in when the screen opens is read at once; the OmaBoot? disc itself (booted from this
// drive) passes it as it is, never ejected: the live system runs from it. A tray that's open (a
// tray-loading drive, a Mac Pro's) says so until it's closed. A read that stops making progress
// for 20 s, or takes more than 90 s, failed.
//
// The band shows a slot-loading drive from above, the disc waiting at its slot, drawn in while
// it reads (spinning, with how much is read), then pushed back out: a tray drive works the same
// way and shares the picture.

func init() {
	Register(Spec{ID: "storage.optical", Group: Plug, Order: 90, Title: i18n.M("Optical drive"),
		Brief: i18n.M("insert a CD or DVD"), Gear: i18n.M("a CD or DVD"), Without: i18n.M("no disc"), Expect: 40 * time.Second,
		Applies: func(e *Env) bool {
			if e.Checks == nil {
				return e.Optical
			}
			ds := checks.OpticalDrives(e.Checks.Root, e.Checks.Root)
			return len(ds) > 0 && !ds[0].USB
		},
		New: func(*Env) Run { return &optical{} }, Demo: func(f *Fake) { DemoDisc(f, "data") }})
}

// Drive states (linux/cdrom.h CDROM_DRIVE_STATUS) and disc kinds (CDROM_DISC_STATUS).
const (
	cdsNoInfo   = 0
	cdsNoDisc   = 1
	cdsTrayOpen = 2
	cdsNotReady = 3
	cdsDiscOK   = 4

	cdsAudio = 100
	cdsData1 = 101
	cdsData2 = 102
	cdsXA21  = 103
	cdsXA22  = 104
	cdsMixed = 105
)

// discKind names a CDROM_DISC_STATUS answer: "audio", "data", "mixed", or "" for none.
func discKind(n int) string {
	switch n {
	case cdsAudio:
		return "audio"
	case cdsData1, cdsData2, cdsXA21, cdsXA22:
		return "data"
	case cdsMixed:
		return "mixed"
	}
	return ""
}

// discLen is how much of a data disc is read.
const discLen = 1 << 20

const (
	opNothing = 25.0 // seconds before "Nothing happens" can be picked
	opStall   = 20 * time.Second
	opLimit   = 90 * time.Second
)

// DiscRead is how a disc went: what it is, what was read, and the eject.
type DiscRead struct {
	Kind     string // "audio", "data", "mixed"; "" when the drive couldn't say
	Tracks   int    // audio: the tracks in its table of contents
	Secs     int    // audio: its length
	Bytes    int64  // data: bytes read from its start
	TestDisc string // data: "ok" for the OMABOOT_TEST disc read right, "bad: ..." for one read wrong, "" for any other disc
	Took     time.Duration
	Fail     string // why it didn't read (English), "" when it did
	Ejected  bool   // the drive reported the disc out after the eject
	EjectEr  string // why it didn't come out, "" when it did (or no eject was tried)
	Skipped  string // why nothing was tried (not on the Mac), "" when it was
}

// OK reports whether the disc read.
func (r DiscRead) OK() bool { return r.Fail == "" && r.Skipped == "" }

// DiscProgress is where a disc read is: "kind", "read", and the bytes read.
type DiscProgress struct {
	Phase       string
	Done, Total int64
}

// DiscJob is a disc being read; none of its methods block.
type DiscJob interface {
	Progress() DiscProgress
	Ready() (DiscRead, bool)
	Stop() // the screen left: no more reads (one in the kernel ends at its chunk)
}

// EjectResult is how an eject went: whether the drive reported the disc out.
type EjectResult struct {
	Out   bool   // the drive reports no disc, or its tray open
	State string // the drive's state at the end ("no disc", "tray open", "disc ok")
	Err   string // why it isn't out (English), "" when it is
	Tries int    // ejects tried (0: none was needed)
}

// EjectJob is a disc being ejected; none of its methods block.
type EjectJob interface {
	Ready() (EjectResult, bool)
	Waiting() bool // still waiting for a read in the kernel to end first
}

// doneEject is an eject that ended as it began (nothing to do there).
type doneEject struct{ r EjectResult }

func (d doneEject) Ready() (EjectResult, bool) { return d.r, true }
func (d doneEject) Waiting() bool              { return false }

// discIO is an IO that works an optical drive: the Mac's (System) and the demo's (Fake).
type discIO interface {
	DriveStatus(dev string) (int, error) // CDROM_DRIVE_STATUS; may take a moment
	ReadDisc(dev string) DiscJob
	// EjectDisc unlocks the door and ejects, then asks until the disc is out; with always false
	// only a disc the drive reports is ejected (an empty tray stays shut).
	EjectDisc(dev string, always bool) EjectJob
	// DiscBusy is what still works the drive, whichever test started it: "reading", "ejecting",
	// or "".
	DiscBusy(dev string) string
}

type opPhase int

const (
	opWait  opPhase = iota // asking the drive until it has a disc
	opRead                 // the disc job: kind, read
	opEject                // the disc going back out (after a read, or "Eject disc")
	opRetry                // the first disc didn't read: another one, or "It doesn't read"
	opDone
)

type optical struct {
	drive   checks.Drive
	phase   opPhase
	status  int // the drive's last answer (-1: none yet)
	statErr string
	asking  Future[driveAns]
	polled  float64
	waitAt  float64 // when it started waiting for this disc
	inAt    float64 // when the disc showed up (-1: in before the test)
	job     DiscJob
	jobAt   float64
	cur     *DiscRead // the disc read last, going back out
	eject   EjectJob
	ejectAt float64
	back    opPhase   // after "Eject disc": the phase to go back to
	out     bool      // the drive reported the last disc out: nothing to eject when the test is left
	tried   bool      // an eject of this disc ended with it still in: leaving doesn't try again
	booted  bool      // the OmaBoot? disc is in this drive: never ejected
	first   *DiscRead // the disc that didn't read, before the retry
	res     *DiscRead
	resAt   float64
	trail   []string // the drive's states seen, for the evidence
	noIO    bool     // this IO can't work a drive (not on the Mac)
}

type driveAns struct {
	st  int
	err error
}

func (x *optical) Start(c *Ctx) {
	c.Select(-1) // waiting for a disc: a stray Enter answers nothing
	if ds := checks.OpticalDrives(c.IO, ioLinker{c.IO}); len(ds) > 0 {
		x.drive = ds[0]
	} else {
		x.drive = checks.Drive{Name: "sr0"}
	}
	x.status, x.polled, x.inAt = -1, -1, 0
	_, ok := c.IO.(discIO)
	x.noIO = !ok
	if bootDisk(c.IO) == x.drive.Name { // booted from a disc in this drive: it reads discs, and it stays in
		x.phase, x.booted = opDone, true
		c.Decide(Outcome{Results: []checks.Result{Result("storage.optical", checks.Supported, Automatic,
			"optical drive "+x.drive.Name+" ("+x.drive.Model+"): the OmaBoot? image was booted from the disc in it, and the live system reads from it; not ejected",
			i18n.T("booted from a disc in it"))}, Say: i18n.T("I came in on a disc in this drive. it reads.")})
	}
}

// ioLinker reads sysfs links through a test's IO (checks.OpticalDrives).
type ioLinker struct{ io IO }

func (l ioLinker) Link(p string) string { return link(l.io, p) }

// askDrive asks a drive for its state: off the screen's goroutine on the Mac (a drive spinning up
// can take a moment to answer), at once on a Fake.
func askDrive(c *Ctx, dev string) Future[driveAns] {
	d := c.IO.(discIO)
	q := func() driveAns {
		st, err := d.DriveStatus(dev)
		return driveAns{st, err}
	}
	if c.IO.Real() {
		return Go(q)
	}
	return Now(q())
}

func (x *optical) note(s string) {
	if n := len(x.trail); n == 0 || x.trail[n-1] != s {
		x.trail = append(x.trail, s)
	}
}

func statusWord(st int) string {
	return map[int]string{cdsNoInfo: "no info", cdsNoDisc: "no disc", cdsTrayOpen: "tray open", cdsNotReady: "not ready", cdsDiscOK: "disc ok"}[st]
}

func (x *optical) Step(c *Ctx) {
	if x.noIO || x.phase == opDone {
		return
	}
	switch x.phase {
	case opRead:
		x.stepRead(c)
		return
	case opEject:
		x.stepEject(c)
		return
	}
	if x.asking != nil {
		a, ok := x.asking.Ready()
		if !ok {
			return
		}
		x.asking = nil
		x.status, x.statErr = a.st, ""
		if a.err != nil {
			x.status, x.statErr = -1, a.err.Error()
			x.note("couldn't ask: " + x.statErr)
		} else {
			x.note(statusWord(a.st))
		}
		if x.status == cdsDiscOK {
			if c.T < 1 && x.first == nil {
				x.inAt = -1 // in before the test began
			} else {
				x.inAt = c.T
			}
			x.out, x.tried = false, false
			x.job, x.jobAt, x.phase = c.IO.(discIO).ReadDisc(x.drive.Name), c.T, opRead
			return
		}
	}
	if c.T-x.polled < 0.5 || x.asking != nil {
		return
	}
	x.polled = c.T
	x.asking = askDrive(c, x.drive.Name)
}

// stepRead: once the read ends, the disc goes back out (stepEject decides).
func (x *optical) stepRead(c *Ctx) {
	r, ok := x.job.Ready()
	if !ok {
		return
	}
	x.job = nil
	if r.Skipped != "" {
		x.phase, x.statErr = opWait, r.Skipped
		x.noIO = true
		return
	}
	x.cur = &r
	x.startEject(c, true, opDone)
}

// startEject sends the disc back out; back is the phase to return to after "Eject disc".
func (x *optical) startEject(c *Ctx, always bool, back opPhase) {
	x.eject, x.ejectAt, x.back, x.phase = c.IO.(discIO).EjectDisc(x.drive.Name, always), c.T, back, opEject
	x.asking = nil
}

func (x *optical) stepEject(c *Ctx) {
	e, ok := x.eject.Ready()
	if !ok {
		return
	}
	x.eject = nil
	x.out, x.tried = e.Out, !e.Out
	if e.Out {
		x.note(e.State + " after the eject")
	} else {
		x.note("still in after the eject")
	}
	r := x.cur
	if r == nil { // "Eject disc" while waiting for one
		x.phase, x.status, x.polled = x.back, -1, c.T
		return
	}
	x.cur = nil
	r.Ejected, r.EjectEr = e.Out, e.Err
	switch {
	case r.OK():
		x.res, x.resAt, x.phase = r, c.T, opDone
		x.decide(c)
	case x.first == nil: // one more disc: a scratched one shouldn't fail the drive
		x.first, x.res, x.resAt, x.phase = r, r, c.T, opRetry
		x.status, x.waitAt = -1, c.T
	default:
		x.res, x.resAt, x.phase = r, c.T, opDone
		x.decide(c)
	}
}

// readText is a disc's read in English: "data disc, 1.0 MB read in 0.4 s (2.6 MB/s)".
func readText(r DiscRead) string {
	var s string
	switch r.Kind {
	case "audio":
		s = fmt.Sprintf("audio CD, table of contents read: %d tracks, %d:%02d", r.Tracks, r.Secs/60, r.Secs%60)
	case "":
		s = "a disc the drive couldn't name"
	default:
		s = fmt.Sprintf("%s disc", r.Kind)
		if r.Bytes > 0 {
			s += fmt.Sprintf(", %.1f MB read in %.1f s (%.1f MB/s)", float64(r.Bytes)/1e6, r.Took.Seconds(), discMBps(r))
		}
	}
	switch {
	case r.TestDisc == "ok":
		s += "; it is the " + testDiscLabel + " disc and its contents read back exactly"
	case r.TestDisc != "":
		s += "; it is the " + testDiscLabel + " disc, " + strings.TrimPrefix(r.TestDisc, "bad: ")
	}
	if r.Fail != "" {
		s += ": " + r.Fail
	}
	switch {
	case r.Ejected:
		s += "; ejected, and the drive reported it out"
	case r.EjectEr != "":
		s += "; it didn't come out: " + r.EjectEr
	}
	return s
}

func discMBps(r DiscRead) float64 {
	if r.Took <= 0 {
		return 0
	}
	return float64(r.Bytes) / r.Took.Seconds() / 1e6
}

// evidence is the drive and what happened, in English.
func (x *optical) evidence() string {
	ev := "optical drive " + x.drive.Name
	if x.drive.Model != "" {
		ev += " (" + x.drive.Model + ")"
	}
	if x.first != nil {
		ev += "; first disc: " + readText(*x.first)
	}
	if x.res != nil && x.res != x.first {
		if x.first != nil {
			ev += "; second disc: "
		} else {
			ev += ": "
		}
		ev += readText(*x.res)
		if x.inAt < 0 {
			ev += "; it was in before the test began"
		}
	}
	return ev + "; drive states seen: " + strings.Join(x.trail, ", ")
}

func (x *optical) decide(c *Ctx) {
	r := *x.res
	if r.OK() {
		what := i18n.T("data disc read, %.1f MB/s", discMBps(r))
		if r.TestDisc == "ok" {
			what = i18n.T("OMABOOT_TEST disc, contents verified, %.1f MB/s", discMBps(r))
		}
		if r.Kind == "audio" {
			what = i18n.N("audio CD, %d track", "audio CD, %d tracks", r.Tracks, r.Tracks)
		}
		if !r.Ejected { // it reads, and keeps the disc (0.7.3 follow-ups)
			c.Decide(Outcome{Results: []checks.Result{Result("storage.optical", checks.Partial, Automatic, x.evidence(), i18n.T("read, but the disc won't come out"))},
				Say: i18n.T("read it, but it won't give the disc back. that's partial.")})
			return
		}
		c.Decide(Outcome{Results: []checks.Result{Result("storage.optical", checks.Supported, Automatic, x.evidence(), what)},
			Say: i18n.T("read it, and gave it back. the drive works.")})
		return
	}
	c.Decide(Outcome{Results: []checks.Result{Result("storage.optical", checks.Failed, Automatic, x.evidence(), i18n.T("two discs, neither read"))},
		Say: i18n.T("two discs in, nothing read. that's a fail.")})
}

// The answers' meanings, in View's order (actions).
const (
	opActNothing = iota // "Nothing happens"
	opActNoRead         // "It doesn't read"
	opActEject          // "Eject disc"
)

// actions are the answers now, with what each means.
func (x *optical) actions(c *Ctx) ([]Action, []int) {
	eject := Action{Label: i18n.T("Eject disc"), Does: i18n.T("the drive gives the disc back")}
	switch x.phase {
	case opWait:
		return []Action{{Label: i18n.T("Nothing happens"), Does: i18n.T("the disc is in: the drive didn't notice"), Off: c.T-x.waitAt < opNothing}, eject},
			[]int{opActNothing, opActEject}
	case opRead:
		eject.Off, eject.Does = true, i18n.T("not while the disc is being read")
		return []Action{eject}, []int{opActEject}
	case opEject:
		eject.Off, eject.Does = true, i18n.T("on its way out")
		return []Action{eject}, []int{opActEject}
	case opRetry:
		return []Action{{Label: i18n.T("It doesn't read"), Does: i18n.T("no other disc: it goes in as failed")}, eject},
			[]int{opActNoRead, opActEject}
	}
	return nil, nil
}

func (x *optical) Act(c *Ctx, i int) {
	as, what := x.actions(c)
	if i < 0 || i >= len(as) || as[i].Off {
		return
	}
	switch what[i] {
	case opActNoRead: // the first disc decides
		x.res, x.phase = x.first, opDone
		c.Decide(Outcome{Results: []checks.Result{Result("storage.optical", checks.Failed, Automatic, x.evidence()+"; no second disc tried",
			i18n.T("the disc didn't read"))}, Say: i18n.T("the drive took the disc and couldn't read it. that's a fail.")})
	case opActNothing:
		x.phase = opDone
		c.Decide(Outcome{Results: []checks.Result{Result("storage.optical", checks.Failed, Observed,
			x.evidence()+"; the tester inserted a disc and the drive never reported one", i18n.T("the drive never saw a disc"))},
			Say: i18n.T("a disc in, and the drive never noticed. that's a fail.")})
	case opActEject:
		if !x.noIO {
			x.note("Eject disc")
			x.startEject(c, true, x.phase)
		}
	}
}

// Stop: whatever ended the test, a read under way stops, and a disc still in the drive goes back
// out (never the OmaBoot? disc). It doesn't wait: the eject goes on by itself, and the tests that
// sleep wait for it (DiscBusy).
func (x *optical) Stop(c *Ctx) {
	if x.job != nil {
		x.job.Stop()
	}
	d, ok := c.IO.(discIO)
	if !ok || x.noIO || x.booted || x.out || x.tried || x.eject != nil {
		return
	}
	d.EjectDisc(x.drive.Name, false)
}

func (x *optical) View(c *Ctx) Screen {
	s := Screen{Face: boot.Idle, OutTitle: i18n.T("What I see"), SkipDoes: i18n.T("goes in as not tested")}
	s.Steps = []string{i18n.T("Insert a CD or DVD into the drive, label up."), i18n.T("Wait while it's read: a slow drive can take a minute."),
		i18n.T("It comes back out by itself: take it.")}
	s.Happens = i18n.T("Any disc will do: music, data, a DVD. Its start is read; nothing is written, nothing opened as files.")
	s.Hint = i18n.T("a slot drive takes it with a gentle push; a tray closes with one.")
	name := x.drive.Name
	if x.drive.Model != "" {
		name += " (" + x.drive.Model + ")"
	}
	s.Live = []Line{Fact(i18n.T("drive: %s", name))}
	s.Actions, _ = x.actions(c)
	a := opArt{t0: x.inAt, ejectAt: -1}
	o := c.Outcome()
	switch {
	case o != nil && len(o.Results) > 0 && o.Results[0].Status == checks.NotTested:
		a.state, a.words = opSkipped, i18n.T("skipped")
	case x.booted:
		s.Step = len(s.Steps)
		s.Live = append(s.Live, Fact(i18n.T("the OmaBoot? disc: booted from it, so it stays in")))
		a.state, a.words, a.detail, a.ok = opIn, i18n.T("read"), i18n.T("the OmaBoot? disc"), true
	case x.phase == opDone && x.res == nil: // "Nothing happens"
		s.Step = len(s.Steps)
		s.Live = append(s.Live, Bad(i18n.T("the drive never reported a disc")))
		a.words, a.bad = i18n.T("no disc seen"), true
	case x.noIO:
		s.Live = append(s.Live, Gone(i18n.T("the drive can't be asked here")))
		s.Say, s.SayAt = i18n.T("I can't reach the drive from here."), 0.2
		a.state = opSkipped
	case x.phase == opWait:
		s.Say, s.SayAt = i18n.T("a disc, please. I'm listening to the drive."), 0.2
		s.Waiting = i18n.T("·  waiting for a disc")
		switch x.status {
		case cdsTrayOpen:
			s.Live = append(s.Live, Wait(i18n.T("tray open: put the disc in and close it")))
			a.words = i18n.T("tray open")
		case cdsNotReady:
			s.Live = append(s.Live, Wait(i18n.T("a disc: the drive is spinning it up")))
			s.Step, a.state, a.words = 1, opSpin, i18n.T("spinning up")
			s.Say, s.SayAt, s.Face = i18n.T("a disc. the drive is spinning it up…"), x.waitAt, boot.Work
		case cdsNoInfo:
			s.Live = append(s.Live, Wait(i18n.T("the drive doesn't say yet")))
		case -1:
			if x.statErr != "" {
				s.Live = append(s.Live, Gone(i18n.T("the drive didn't answer: %s", x.statErr)))
			}
			s.Live = append(s.Live, Wait(i18n.T("waiting for a disc")))
		default:
			s.Live = append(s.Live, Wait(i18n.T("no disc yet")))
		}
		if a.words == "" {
			a.words = i18n.T("insert a disc")
		}
	case x.phase == opRead:
		p := x.job.Progress()
		s.Step, s.Face = 1, boot.Work
		s.Say, s.SayAt = i18n.T("got it. reading… a slow drive takes its time."), x.jobAt
		s.Live = append(s.Live, Fact(i18n.T("a disc: reading it")))
		a.state, a.t0 = opReading, x.jobAt
		switch p.Phase {
		case "read":
			a.done, a.total = p.Done, p.Total
			s.Live = append(s.Live, Wait(i18n.T("read %s of %s", size(p.Done), size(p.Total))))
			a.words = i18n.T("reading")
		default:
			a.words = i18n.T("what kind of disc…")
		}
		s.Waiting = i18n.T("·  reading the disc")
	case x.phase == opEject:
		s.Step, s.Face = 2, boot.Work
		s.Say, s.SayAt = i18n.T("giving it back…"), x.ejectAt
		if r := x.cur; r != nil {
			if r.OK() {
				s.Live = append(s.Live, Fact(discLine(*r)))
			} else {
				s.Live = append(s.Live, Bad(discLine(*r)))
			}
		} else {
			s.Step = 0
		}
		a.state, a.t0, a.words = opEjecting, x.ejectAt, i18n.T("ejecting")
		if x.cur == nil { // "Eject disc" with no disc read: nothing to draw going out
			a.state = opOut
		}
		if x.eject != nil && x.eject.Waiting() {
			s.Live = append(s.Live, Wait(i18n.T("waiting for the drive to finish its read, then ejecting")))
			a.state, a.words = opIn, i18n.T("finishing its read")
		} else {
			s.Live = append(s.Live, Wait(i18n.T("ejecting it; asking the drive until it's out")))
		}
		s.Waiting = i18n.T("·  ejecting the disc")
	case x.phase == opRetry:
		r := x.first
		s.Steps = []string{i18n.T("Try another disc: insert it now."), i18n.T("Wait while it's read: a slow drive can take a minute."),
			i18n.T("It comes back out by itself: take it.")}
		s.Say, s.SayAt, s.Face = i18n.T("that disc didn't read. got another one?"), x.resAt, boot.Hmm
		s.Live = append(s.Live, Bad(discLine(*r)))
		if !r.Ejected && r.EjectEr != "" {
			s.Live = append(s.Live, Bad(i18n.T("it didn't come out: %s", r.EjectEr)))
		}
		if x.status == cdsNotReady || x.status == cdsDiscOK {
			s.Step = 1
		}
		a.state, a.words, a.detail, a.bad = opOut, i18n.T("didn't read"), i18n.T("another disc?"), true
		s.Waiting = i18n.T("·  waiting for another disc")
	case x.res != nil:
		r := *x.res
		s.Step = len(s.Steps)
		if r.OK() {
			s.Live = append(s.Live, Fact(discLine(r)))
			a.state, a.words, a.detail, a.ok = opOut, i18n.T("read"), discShort(r), true
		} else {
			s.Live = append(s.Live, Bad(discLine(r)))
			a.state, a.words, a.bad = opOut, i18n.T("didn't read"), true
		}
		if r.Ejected {
			s.Live = append(s.Live, Fact(i18n.T("it came back out: take it")))
		} else if r.EjectEr != "" {
			s.Steps = s.Steps[:2] // it didn't come back out: that step isn't ticked
			s.Step = len(s.Steps)
			s.Live = append(s.Live, Bad(i18n.T("it didn't come out: %s", r.EjectEr)))
			a.state, a.detail = opIn, i18n.T("the disc is still in")
			if r.OK() {
				a.ok, a.bad, a.words = false, false, i18n.T("read, still in")
			}
		}
	}
	s.Anim = a.draw
	return s
}

// EjectAtExit gives back a disc left in an optical drive as boot-live ends (quit, shut down,
// restart; 0.7.3 review): it waits for a read or an eject still under way, then ejects a disc
// the drive reports, and asks until it's out; never the disc the image booted from, and nothing
// on a fixture tree. It waits at most limit in all and says what it did, in English ("" for
// nothing).
func EjectAtExit(root sysroot.Root, limit time.Duration) string {
	if !root.IsSystem() {
		return ""
	}
	io := &System{Root: root}
	boot := bootDisk(io)
	end := time.Now().Add(limit)
	var said []string
	for _, d := range checks.OpticalDrives(io, ioLinker{io}) {
		if d.Name == boot {
			continue
		}
		if st, err := io.DriveStatus(d.Name); err == nil && discOutState(st) && io.DiscBusy(d.Name) == "" {
			continue
		}
		j := io.EjectDisc(d.Name, false)
		for {
			if r, ok := j.Ready(); ok {
				switch {
				case r.Tries == 0:
				case r.Out:
					said = append(said, "ejected the disc in "+d.Name)
				default:
					said = append(said, "the disc in "+d.Name+" didn't come out: "+r.Err)
				}
				break
			}
			if time.Now().After(end) {
				said = append(said, "the disc in "+d.Name+" wasn't ejected: the drive was still busy")
				break
			}
			time.Sleep(100 * time.Millisecond)
		}
	}
	return strings.Join(said, "; ")
}

// discOutState: the drive holds no disc (or its tray is open, the disc on it).
func discOutState(st int) bool { return st == cdsNoDisc || st == cdsTrayOpen }

// discLine is What I see's line for a disc: "data disc · 1.0 MB · 2.6 MB/s", "audio CD · 12
// tracks · 48:12", or why it didn't read.
func discLine(r DiscRead) string {
	if !r.OK() {
		return i18n.T("didn't read: %s", r.Fail)
	}
	if r.Kind == "audio" {
		return i18n.N("audio CD · %[1]d track · %[2]d:%02[3]d", "audio CD · %[1]d tracks · %[2]d:%02[3]d", r.Tracks, r.Tracks, r.Secs/60, r.Secs%60)
	}
	return i18n.T("%[1]s disc · %[2]s read · %.1[3]f MB/s", r.Kind, size(r.Bytes), discMBps(r))
}

// discShort is what was read, short, for the band: "1.0 MB, 2.6 MB/s", "12 tracks".
func discShort(r DiscRead) string {
	if r.Kind == "audio" {
		return i18n.N("%d track", "%d tracks", r.Tracks, r.Tracks)
	}
	return i18n.T("%[1]s, %.1[2]f MB/s", size(r.Bytes), discMBps(r))
}

// ── the band ──────────────────────────────────────────────────────────────────

type opLook int

const (
	opOutside  opLook = iota // at the slot, nudging in: insert it
	opSpin                   // drawn in, spinning up
	opReading                // in, spinning, read so far
	opEjecting               // on its way out
	opOut                    // out again (done: read, or not)
	opIn                     // in, and it stayed (no eject)
	opSkipped                // all dim
)

// opArt is the optical test's band: a slot-loading drive from above, its slot in
// its right edge; the disc (silver, its hub dark) waits in front of the slot, nudging at it; it
// slides in, spins (a violet sector going round) while the drive reads, with a bar of how much
// is read; and slides back out. The picture sits at the band's right end with its words just
// left of it: white while it works, green once it read (green only for that), red
// when it didn't, all dim when skipped.
type opArt struct {
	state       opLook
	t0          float64 // when the state began, the run's time (-1: long ago)
	ejectAt     float64
	done, total int64
	words       string
	detail      string // under the words, grey (what was read)
	ok, bad     bool
}

func (a opArt) draw(gr *g.Grid, x, y, w, h int, t float64) {
	ph := 2 * (h - 1)  // square pixels: two to a row
	d := min(16, ph-8) // the disc's width
	if d < 8 || w < 50 {
		return
	}
	d -= d % 2
	bw, bh := d+12, d+8 // the drive: bigger than the disc, its slot in its right edge
	pw := bw + d + 4
	p := newSndPix(pw, ph)
	by := (ph - bh) / 2
	frame, lip := byte('w'), byte('W')
	if a.state == opSkipped {
		frame, lip = 'K', 'K'
	}
	cy := float64(by + bh/2)
	// the slot, as wide as the disc, in the right edge: two pixels thick, its lips bright
	slot := func(j int) bool { return math.Abs(float64(by+j)+0.5-cy) <= float64(d)/2+1 }
	bezel := func() {
		for j := 0; j < bh; j++ {
			if !slot(j) {
				p.set(bw-2, by+j, frame)
				p.set(bw-1, by+j, frame)
			}
		}
		for _, j := range []int{bh/2 - d/2 - 2, bh/2 + d/2 + 1} {
			p.set(bw-2, by+j, lip)
			p.set(bw-1, by+j, lip)
		}
	}
	for i := 0; i < bw; i++ {
		p.set(i, by, frame)
		p.set(i, by+bh-1, frame)
	}
	for j := 0; j < bh; j++ {
		p.set(0, by+j, frame)
	}
	r := float64(d) / 2
	in := float64(bw / 2)      // the disc's centre inside the drive
	out := float64(bw) + r + 1 // in front of the slot
	since := t - a.t0
	if a.t0 < 0 {
		since = 1e6
	}
	ease := func(k float64) float64 { k = max(0, min(1, k)); return k * k * (3 - 2*k) }
	cx := out
	disc, hub, sector := byte('W'), byte('K'), byte(0)
	switch a.state {
	case opOutside:
		cx = out - 2*math.Abs(math.Sin(t*math.Pi/0.8)) // nudging at the slot
	case opSpin, opReading:
		cx = out + (in-out)*ease(since/0.6) // drawn in over 0.6 s
		disc, sector = 'w', 'M'
	case opIn:
		cx, disc = in, 'w'
	case opEjecting:
		cx = in + (out-in)*ease(since/0.8)
		disc = 'w'
	case opSkipped:
		disc, hub = 'K', 'k'
	}
	cx = math.Round(cx) // whole pixels: its edge stays round as it moves
	p.disc(cx, cy, 1.6, r, disc)
	p.disc(cx, cy, 0, 1.6, hub)
	if sector != 0 && cx < out-r { // spinning: a sector going round, faster while it reads
		speed := 2.0
		if a.state == opReading {
			speed = 6
		}
		p.arc(cx, cy, r-1.5, math.Mod(t*speed*360, 360), 30, sector)
		p.arc(cx, cy, r-2.5, math.Mod(t*speed*360, 360), 30, sector)
	}
	bezel() // over the disc, as it goes through the slot
	px := x + w - pw - 1
	p.draw(gr, px, y)
	// the words, just left of the picture
	tw := w - pw - 4
	if tw < 8 {
		return
	}
	ty := y + (h-1)/2 - 1
	wc := g.WHT
	switch {
	case a.state == opSkipped:
		wc = g.DIM
	case a.ok:
		wc = g.BGRN
	case a.bad:
		wc = g.BRED
	}
	word := a.words
	if a.ok {
		word = "✓ " + word
	} else if a.bad {
		word = "× " + word
	}
	var bar string
	if a.state == opReading && a.total > 0 && tw > 24 {
		bar = i18n.T("%s of %s", size(a.done), size(a.total))
	}
	bw2 := max(width(word), width(a.detail))
	if a.state == opReading && a.total > 0 {
		bw2 = max(bw2, 10)
		if bar != "" {
			bw2 = max(bw2, 11+width(bar))
		}
	}
	bw2 = min(bw2, tw)
	tx := px - 3 - bw2
	gr.Put(tx, ty, cut(word, tw), wc)
	if a.detail != "" {
		gr.Put(tx, ty+2, cut(a.detail, tw), g.GRY)
	}
	if a.state == opReading && a.total > 0 {
		n := int(math.Round(float64(a.done) / float64(a.total) * 10))
		for i := 0; i < 10; i++ {
			if i < n {
				gr.Set(tx+i, ty+2, '█', g.BVIO)
			} else {
				gr.Set(tx+i, ty+2, '░', g.DIM)
			}
		}
		if bar != "" {
			gr.Put(tx+11, ty+2, cut(bar, bw2-11), g.GRY)
		}
	}
}

// ── the demo's drive ──────────────────────────────────────────────────────────

// FakeDrive is the demo's optical drive: its state over the run (a disc in at 3 s), the disc
// it reads, and how fast.
type FakeDrive struct {
	Status int
	Disc   DiscRead      // what reading it gives (Took: how long the read takes)
	Eject  time.Duration // how long the eject takes
	Stuck  bool          // the disc never comes out
	Ejects int           // ejects asked for (a disc in, or always)

	reading *fakeDisc
	eject   *fakeEject
}

// DemoDisc is the iMac10,1's SuperDrive with a disc going in at 3 s: kind "data" (a DVD), "audio"
// (a music CD), "bad" (a scratched disc, then a good one at 12 s), "tray" (an open tray, closed
// with the disc at 4 s), "stuck" (a DVD that reads and won't come out), "in" (a disc in the drive
// from the start) or "none" (nothing comes).
func DemoDisc(f *Fake, kind string) {
	dev := "/sys/devices/pci0000:00/0000:00:0b.0/ata2/host1/target1:0:0/1:0:0:0/"
	f.Set(dev+"block/sr0/size", "0")
	FakeLink(f, "/sys/block/sr0", "../devices/pci0000:00/0000:00:0b.0/ata2/host1/target1:0:0/1:0:0:0/block/sr0")
	f.Set("/sys/block/sr0/device/vendor", "HL-DT-ST")
	f.Set("/sys/block/sr0/device/model", "DVDRW  GA11N")
	f.Drive = &FakeDrive{Status: cdsNoDisc, Eject: 1500 * time.Millisecond,
		Disc: DiscRead{Kind: "data", Bytes: discLen, Took: 1400 * time.Millisecond}}
	in := func(at float64) {
		f.At(at, func(f *Fake) { f.Drive.Status = cdsNotReady })
		f.At(at+1.2, func(f *Fake) { f.Drive.Status = cdsDiscOK })
	}
	switch kind {
	case "data":
		in(3)
	case "audio":
		f.Drive.Disc = DiscRead{Kind: "audio", Tracks: 12, Secs: 48*60 + 12, Took: 600 * time.Millisecond}
		in(3)
	case "bad":
		f.Drive.Disc = DiscRead{Kind: "data", Took: 3 * time.Second, Fail: "I/O error 256 KB in"}
		in(3)
		f.At(10, func(f *Fake) { f.Drive.Disc = DiscRead{Kind: "data", Bytes: discLen, Took: 1400 * time.Millisecond} })
		in(12)
	case "tray":
		f.Drive.Status = cdsTrayOpen
		in(4)
	case "stuck":
		f.Drive.Stuck = true
		in(3)
	case "in":
		f.Drive.Status = cdsDiscOK
	}
}

func (f *Fake) DriveStatus(string) (int, error) {
	if f.Drive == nil {
		return 0, fmt.Errorf("no drive")
	}
	return f.Drive.Status, nil
}

func (f *Fake) ReadDisc(string) DiscJob {
	if f.Drive == nil {
		return &fakeDisc{res: DiscRead{Skipped: "no drive"}}
	}
	j := &fakeDisc{f: f, at: f.now, res: f.Drive.Disc}
	f.Drive.reading = j
	return j
}

// EjectDisc is the demo drive's eject, in the run's time: the disc out after Eject (never, when
// Stuck); with always false, nothing happens without a disc.
func (f *Fake) EjectDisc(_ string, always bool) EjectJob {
	d := f.Drive
	if d == nil {
		return doneEject{EjectResult{Err: "no drive"}}
	}
	if !always && d.Status != cdsDiscOK && d.Status != cdsNotReady {
		return doneEject{EjectResult{Out: true, State: statusWord(d.Status)}}
	}
	d.Ejects++
	d.eject = &fakeEject{f: f, at: f.now}
	return d.eject
}

// DiscBusy: the demo drive reading (a read not stopped, not done) or ejecting.
func (f *Fake) DiscBusy(string) string {
	d := f.Drive
	switch {
	case d == nil:
	case d.reading != nil && !d.reading.stopped && !d.reading.done():
		return "reading"
	case d.eject != nil && !d.eject.done():
		return "ejecting"
	}
	return ""
}

// fakeDisc is a disc read in the run's time: the kind at once, then the read over Took.
type fakeDisc struct {
	f       *Fake
	at      float64
	res     DiscRead
	stopped bool
	mu      sync.Mutex
}

func (j *fakeDisc) done() bool { return j.f == nil || j.f.now-j.at >= 0.3+j.res.Took.Seconds() }

func (j *fakeDisc) Progress() DiscProgress {
	if j.f == nil {
		return DiscProgress{}
	}
	el := j.f.now - j.at - 0.3 // the kind first
	switch {
	case el < 0:
		return DiscProgress{Phase: "kind"}
	case j.res.Kind == "audio":
		return DiscProgress{Phase: "read"}
	}
	total := int64(discLen)
	return DiscProgress{Phase: "read", Done: min(total, int64(el/max(j.res.Took.Seconds(), 1e-3)*float64(total))), Total: total}
}

func (j *fakeDisc) Ready() (DiscRead, bool) {
	if j.f == nil {
		return j.res, true
	}
	if !j.done() {
		return DiscRead{}, false
	}
	r := j.res
	if r.Fail != "" && r.Bytes == 0 {
		r.Bytes = 256 << 10
	}
	return r, true
}

func (j *fakeDisc) Stop() {
	j.mu.Lock()
	j.stopped = true
	j.mu.Unlock()
}

// fakeEject is the demo drive's eject: the disc out after the drive's Eject time, unless Stuck.
type fakeEject struct {
	f  *Fake
	at float64
}

func (j *fakeEject) done() bool { return j.f.now-j.at >= j.f.Drive.Eject.Seconds() }

func (j *fakeEject) Ready() (EjectResult, bool) {
	if !j.done() {
		return EjectResult{}, false
	}
	d := j.f.Drive
	if d.Stuck {
		return EjectResult{State: statusWord(cdsDiscOK), Tries: 2, Err: "the drive still reports disc ok after 2 ejects"}, true
	}
	if d.Status == cdsDiscOK || d.Status == cdsNotReady {
		d.Status = cdsNoDisc
	}
	return EjectResult{Out: true, State: statusWord(d.Status), Tries: 1}, true
}

func (j *fakeEject) Waiting() bool { return false }
