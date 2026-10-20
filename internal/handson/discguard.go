package handson

import (
	"github.com/omaboot/boot-live/internal/checks"
	"github.com/omaboot/boot-live/internal/i18n"
)

// discGuard keeps a test that puts the Mac to sleep (sleep and wake, the lid) from starting while
// the optical drive holds a disc or works one (a read still in the kernel, an eject): the
// iMac10,1 hung going to sleep with a disc in its drive after a slow read (0.7.3 review). It asks
// the drive once a second until the nap; the test offers "Eject disc" meanwhile. A Mac without a
// drive, or booted from the disc in it (the live system needs it), is never held.
type discGuard struct {
	dev    string // the drive watched; "" for none
	io     discIO
	asking Future[driveAns]
	polled float64
	state  int    // the drive's last answer: -1 before the first, -2 when it couldn't be asked
	busy   string // DiscBusy's word
	eject  EjectJob
	ejRes  *EjectResult
}

func (d *discGuard) start(c *Ctx) {
	d.state = -1
	io, ok := c.IO.(discIO)
	if !ok {
		return
	}
	ds := checks.OpticalDrives(c.IO, ioLinker{c.IO})
	if len(ds) == 0 || bootDisk(c.IO) == ds[0].Name {
		return
	}
	d.dev, d.io = ds[0].Name, io
	d.ask(c)
}

func (d *discGuard) ask(c *Ctx) {
	d.polled = c.T
	d.asking = askDrive(c, d.dev)
}

// step takes the drive's answers, and asks again once a second unless quiet (the nap is
// coming: nothing may be asking the drive then).
func (d *discGuard) step(c *Ctx, quiet bool) {
	if d.dev == "" {
		return
	}
	d.busy = d.io.DiscBusy(d.dev)
	if d.eject != nil {
		r, ok := d.eject.Ready()
		if !ok {
			return
		}
		d.eject, d.ejRes, d.state = nil, &r, -1
		if r.Out {
			d.state = cdsNoDisc
		}
	}
	if d.asking != nil {
		a, ok := d.asking.Ready()
		if !ok {
			return
		}
		d.asking, d.state = nil, a.st
		if a.err != nil {
			d.state = -2
		}
	}
	if !quiet && c.T-d.polled >= 1 {
		d.ask(c)
	}
}

// holds reports whether the nap must wait: a disc in the drive (or spinning up), a read or an
// eject under way, or the drive not asked yet.
func (d *discGuard) holds() bool {
	if d.dev == "" {
		return false
	}
	return d.eject != nil || d.busy != "" || d.state == -1 || d.state == cdsDiscOK || d.state == cdsNotReady
}

// asked reports whether no question to the drive is under way (the nap may begin).
func (d *discGuard) asked() bool { return d.asking == nil }

// ejectNow is "Eject disc".
func (d *discGuard) ejectNow() {
	if d.dev != "" && d.eject == nil {
		d.eject, d.ejRes = d.io.EjectDisc(d.dev, false), nil
	}
}

// action is "Eject disc" while the guard holds, else nil.
func (d *discGuard) action() *Action {
	if !d.holds() || d.state == -1 && d.busy == "" && d.eject == nil {
		return nil
	}
	a := Action{Label: i18n.T("Eject disc"), Does: i18n.T("the drive gives the disc back")}
	if d.eject != nil || d.busy != "" {
		a.Off, a.Does = true, i18n.T("the drive is busy")
	}
	return &a
}

// line is What I see's line on the drive while the guard holds (nil when it doesn't).
func (d *discGuard) line() *Line {
	var l Line
	switch {
	case !d.holds():
		if d.ejRes != nil && d.ejRes.Out {
			l = Fact(i18n.T("the disc is out of the optical drive: the nap can go ahead"))
			return &l
		}
		return nil
	case d.eject != nil && d.eject.Waiting(), d.busy == "reading":
		l = Wait(i18n.T("the optical drive is still reading a disc: waiting for it to finish"))
	case d.eject != nil, d.busy == "ejecting":
		l = Wait(i18n.T("ejecting the disc; asking the drive until it's out"))
	case d.state == -1:
		l = Wait(i18n.T("asking the optical drive whether it holds a disc"))
	case d.ejRes != nil && !d.ejRes.Out:
		l = Bad(i18n.T("the disc didn't come out: %s", d.ejRes.Err))
	default:
		l = Bad(i18n.T("a disc is in the optical drive (%s)", d.dev))
	}
	return &l
}

// say is Boot's line while the guard holds a disc in ("" when it doesn't).
func (d *discGuard) say() string {
	switch {
	case !d.holds() || d.state == -1 && d.busy == "" && d.eject == nil:
		return ""
	case d.eject != nil || d.busy != "":
		return i18n.T("the drive is still busy with a disc. the nap waits for it.")
	case d.ejRes != nil && !d.ejRes.Out:
		return i18n.T("the disc won't come out, and I won't nap with it in. skip this one?")
	}
	return i18n.T("a disc is in the drive. I won't nap with it in: eject it first.")
}

// note explains the wait, under the answers.
func (d *discGuard) note() string {
	if !d.holds() || d.state == -1 && d.busy == "" && d.eject == nil {
		return ""
	}
	return i18n.T("A Mac that sleeps with a disc in its drive, or a read under way, can hang: an iMac10,1 did. Take the disc out first.")
}

// DiscHeld says why the Mac shouldn't sleep now because of the optical drive, in words (a disc
// in it, a read or an eject under way), or "": the console's sleepcheck asks it once, off the
// screen's goroutine (the drive may take a moment to answer).
func DiscHeld(io IO) string {
	dio, ok := io.(discIO)
	if !ok {
		return ""
	}
	ds := checks.OpticalDrives(io, ioLinker{io})
	if len(ds) == 0 || bootDisk(io) == ds[0].Name {
		return ""
	}
	dev := ds[0].Name
	switch dio.DiscBusy(dev) {
	case "reading":
		return i18n.T("the optical drive (%s) is still reading a disc", dev)
	case "ejecting":
		return i18n.T("the optical drive (%s) is ejecting a disc", dev)
	}
	if st, err := dio.DriveStatus(dev); err == nil && (st == cdsDiscOK || st == cdsNotReady) {
		return i18n.T("a disc is in the optical drive (%s)", dev)
	}
	return ""
}
