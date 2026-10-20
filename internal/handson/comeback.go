package handson

import (
	"strings"
	"time"

	"github.com/omaboot/boot-live/internal/i18n"
)

// What a nap should bring back, shared by the sleep and lid tests: every device from before it
// and, when the Mac was online, the network, each within sleepBack seconds of waking.
//
// The network is the status line's (IO.NetNow: the backend's state, by watchLink's rule), so
// the Output never says it's back while the status line says it isn't (owner, 0.6.0 review:
// "the network is back" seconds before "Ethernet up"). After a wake it must also hold for
// netSettle seconds: a link reads as up for a moment after the resume before a Thunderbolt
// adapter re-enumerates and drops it.

// netCheck is how long the tests look for the network before a nap: 10 s, or 30 s when the run
// was online earlier (a connection may still be coming back from the previous nap: a
// Thunderbolt adapter re-enumerates after a wake).
func netCheck(e *Env) time.Duration {
	if e != nil && e.Online != "" {
		return time.Duration(sleepBack) * time.Second
	}
	return 10 * time.Second
}

// netSettle is how long the network has to stay up after a wake to count as back (seconds).
const netSettle = 3.0

// netWatch follows the network as the status line has it, polled: when it last came up and
// stayed up.
type netWatch struct {
	since float64 // when it came up and has stayed up since (-1: not up)
	last  Net
	seen  bool // polled at least once
}

// see takes a poll at run time t.
func (w *netWatch) see(t float64, n Net) {
	if !w.seen || !n.Up {
		w.since = -1
	}
	if n.Up && w.since < 0 {
		w.since = t
	}
	w.last, w.seen = n, true
}

// up: the status line says online now.
func (w *netWatch) up() bool { return w.seen && w.since >= 0 }

// back: online, and for at least settle seconds by t.
func (w *netWatch) back(t, settle float64) bool { return w.up() && t-w.since >= settle-1e-6 }

// before is the network check made before a nap: online as the status line has it, waited for
// up to netCheck.
type before struct {
	at   float64
	wait float64
	net  Net
	done bool
}

func (b *before) start(c *Ctx) { b.at, b.wait = c.T, netCheck(c.Env).Seconds() }

func (b *before) step(c *Ctx) {
	if b.done {
		return
	}
	b.net = c.IO.NetNow()
	b.done = b.net.Up || c.T-b.at >= b.wait
}

// line is the screen's word on the network before the nap.
func (b *before) line(e *Env) Line {
	earlier := ""
	if e != nil {
		earlier = e.Online
	}
	switch {
	case !b.done && earlier != "":
		return Wait(i18n.T("checking the network (online earlier through %s)", via(earlier)))
	case !b.done:
		return Wait(i18n.T("checking the network"))
	case b.net.Up:
		return Fact(i18n.T("online through %s: it should come back after the nap", via(b.net.Via)))
	case earlier != "":
		return Bad(i18n.T("offline: %s. it was online earlier through %s and isn't back", b.net.Why, via(earlier)))
	}
	return Gone(i18n.T("offline: %s. the network isn't checked after the nap", b.net.Why))
}

// evidence is the report's word on it, "" when the Mac was online.
func (b *before) evidence(e *Env) string {
	if b.net.Up {
		return ""
	}
	why := b.net.Why
	if !b.done {
		why = "still checking when the nap began"
	}
	ev := "offline before the nap (" + why + ")"
	if e != nil && e.Online != "" {
		ev += "; the run was online earlier through " + e.Online + " and it wasn't back"
	}
	return ev
}

// comeback watches what came back after the nap.
type comeback struct {
	devices map[string][]string // before the nap
	up      bool                // online before the nap
	at      float64             // when the Mac woke
	now     float64             // the run's time at the last step
	polled  float64
	checked bool
	missing []string
	net     netWatch
}

// arm records what's there just before the nap.
func (b *comeback) arm(c *Ctx, up bool) {
	b.devices, b.up = devicesNow(c.IO), up
}

// woke starts the watch.
func (b *comeback) woke(c *Ctx) {
	b.at, b.net = c.T, netWatch{since: -1}
}

// step polls once a second; it reports whether the watch is over: everything back, or
// sleepBack seconds gone.
func (b *comeback) step(c *Ctx) bool {
	b.now = c.T
	if !b.checked || c.T-b.polled >= 1 {
		b.polled, b.checked = c.T, true
		b.missing = gone(b.devices, devicesNow(c.IO))
		if b.up {
			b.net.see(c.T, c.IO.NetNow())
		}
	}
	return len(b.missing) == 0 && !b.netWait(c.T) || c.T-b.at >= sleepBack
}

// netWait: the network was up before the nap and isn't back (settled) yet.
func (b *comeback) netWait(t float64) bool { return b.up && !b.net.back(t, netSettle) }

// netLost: once the watch is over, the network didn't come back.
func (b *comeback) netLost() bool { return b.up && !b.net.back(b.now, netSettle) }

// problems is what didn't come back, for the evidence.
func (b *comeback) problems() []string {
	var bad []string
	if len(b.missing) > 0 {
		bad = append(bad, "not back after 30 s: "+strings.Join(b.missing, ", "))
	}
	if b.netLost() {
		why := ""
		if b.net.last.Why != "" {
			why = " (" + b.net.last.Why + ")"
		}
		bad = append(bad, "the network didn't come back within 30 s"+why)
	}
	return bad
}

// lines is the screen's word on the watch.
func (b *comeback) lines() []Line {
	var out []Line
	if len(b.missing) > 0 {
		out = append(out, Wait(i18n.T("not back yet: %s", strings.Join(b.missing, ", "))))
	} else if b.checked {
		out = append(out, Fact(i18n.T("every device is back")))
	}
	switch {
	case !b.up:
	case b.net.back(b.now, netSettle):
		out = append(out, Fact(i18n.T("the network is back: %s", via(b.net.last.Via))))
	case b.now-b.at < sleepBack && b.net.up():
		out = append(out, Wait(i18n.T("online through %s again: making sure it stays", via(b.net.last.Via))))
	case b.now-b.at < sleepBack:
		out = append(out, Wait(i18n.T("waiting for the network")))
	default:
		out = append(out, Bad(i18n.T("the network didn't come back: %s", b.net.last.Why)))
	}
	return out
}
