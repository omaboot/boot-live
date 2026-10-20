package app

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"syscall"
	"time"

	"github.com/omaboot/boot-live/internal/checks"
	"github.com/omaboot/boot-live/internal/network"
	"github.com/omaboot/boot-live/internal/ui"
)

// AutoMarker starts the one line --auto ends with, which the image build's QEMU smoke test
// watches for on the serial port.
const AutoMarker = "OMABOOT-AUTO:"

// Serial is where --auto also writes its lines, when it exists (QEMU's first serial port).
const Serial = "/dev/ttyS0"

// autoNet is how long --auto waits for a connection that comes up on its own.
const autoNet = 20 * time.Second

// Auto runs without any input (QEMU tests): identify (the exact or best match),
// use whatever network comes up on its own within ~20 s, run the quick pass, then submit if
// there's a target, a key and a network, else save to the BOOTLIVE stick. It ends with one line,
// "OMABOOT-AUTO: submitted <code>", "OMABOOT-AUTO: saved <path>" or "OMABOOT-AUTO: failed
// <reason>", on stdout and on the serial port, and returns the exit status (0 or 1).
func Auto(r *Run, stdout io.Writer) int {
	var serial io.Writer = io.Discard
	if !r.Fixture() {
		if f, err := os.OpenFile(Serial, os.O_WRONLY|syscall.O_NOCTTY|syscall.O_NONBLOCK, 0); err == nil {
			defer f.Close()
			serial = f
		}
	}
	say := func(format string, a ...any) {
		line := fmt.Sprintf("boot-live: "+format, a...)
		fmt.Fprintln(os.Stderr, line)
		fmt.Fprint(serial, line+"\r\n")
	}
	end := func(status int, format string, a ...any) int {
		line := AutoMarker + " " + fmt.Sprintf(format, a...)
		fmt.Fprintln(stdout, line)
		fmt.Fprint(serial, line+"\r\n")
		return status
	}

	r.Setup()
	if r.Fatal != nil {
		return end(1, "failed %s", oneLine(r.Fatal.Error()))
	}
	say("%s", plain(r.DisksLine()))
	cfg := r.Ident.Best()
	r.mu.Lock()
	r.config = cfg // chosen by the tool, not a person: Pick isn't used
	r.mu.Unlock()
	notes := "Unattended run (boot-live --auto): no one was asked anything."
	if r.Ident.Known && !r.Ident.Exact && cfg != "" {
		notes += fmt.Sprintf(" The configuration is the best of %d candidates, picked by the tool.", len(r.Ident.Candidates))
	}
	switch {
	case !r.Ident.Known:
		say("%q isn't in the catalog: no configuration", r.Ident.Identifier)
	case r.Ident.Exact:
		say("identified %s, configuration %s", r.Ident.Identifier, cfg)
	default:
		say("identified %s, best of %d configurations: %s", r.Ident.Identifier, len(r.Ident.Candidates), cfg)
	}
	for _, f := range r.Fixes {
		say("fix took effect: %s", f.ID)
	}

	wait := autoNet
	if r.Fixture() {
		wait = time.Millisecond // a fixture's network never changes
	}
	ctx, cancel := context.WithTimeout(r.ctx, wait)
	st := network.Wait(ctx, r.Opt.Root, r.Opt.Target.URL, time.Second)
	cancel()
	if st.Online {
		r.mu.Lock()
		r.net, r.netFacts.Online, r.netFacts.Iface = st, true, st.Iface
		r.mu.Unlock()
		say("online through %s %s", network.Shown(st.Iface, st.Kind), st.IP)
		if why := r.SignInPage(); why != "" {
			say("this network wants a sign-in page: %s", why)
		}
		if s, _, stick := r.RefreshSnapshot(); s != "" {
			say("%s (%s)", s, stick)
		}
	} else {
		say("offline: no network came up within %s", wait)
	}

	total, _ := r.Plan()
	say("quick pass: %d checks", total)
	r.Quick(func(x checks.Result) {
		say("%-28s %-10s %s", x.ID, statusWord(x), oneLine(x.Evidence))
	})
	r.closeWifi()
	autoConsole(r, func(l string) { say("console: %s", plain(l)) })
	r.mu.Lock()
	n := len(r.results)
	r.mu.Unlock()
	notice := ui.ConsentNotice(&ui.Options{Edition: r.Opt.Edition}, &ui.Facts{Checks: make([]ui.Check, n), Site: r.Site()})
	f := r.Report("", notice, notes)
	if st.Online && r.Opt.Target.CanSubmit() {
		rc, err := r.Submit(f)
		if err == nil {
			say("report %s at %s", rc.Code, r.Opt.Target.Page(rc.Code))
			if p, err := r.Save(f); err == nil {
				say("copy saved to %s", p)
			}
			return end(0, "submitted %s", rc.Code)
		}
		say("submit failed: %s", oneLine(errText(err)))
		for _, p := range problems(err) {
			say("  %s", p)
		}
	}
	p, err := r.Save(f)
	if err != nil {
		return end(1, "failed %s", oneLine(errText(err)))
	}
	return end(0, "saved %s", p)
}

func statusWord(x checks.Result) string {
	if x.Status == checks.NotTested && x.Reason != "" {
		return x.Reason
	}
	return x.Status
}

func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

// plain drops the ellipsis and the like for the serial log.
func plain(s string) string { return strings.ReplaceAll(s, "…", "...") }
