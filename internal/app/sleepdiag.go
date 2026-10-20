package app

import (
	"context"
	"errors"
	"fmt"
	"path"
	"strings"
	"time"

	"github.com/omaboot/boot-live/internal/checks"
	"github.com/omaboot/boot-live/internal/command"
	"github.com/omaboot/boot-live/internal/handson"
	"github.com/omaboot/boot-live/internal/i18n"
	"github.com/omaboot/boot-live/internal/send"
	"github.com/omaboot/boot-live/internal/ui"
)

// Sleep diagnostics on the run's side (0.7.5; handson/sleepdiag.go has the kernel's side): the
// pre-sleep diagnostics saved to external storage right before each nap, the kernel's lines for
// each nap kept for bugreport, and the console's sleepcheck.

// system is the Mac's IO for the hands-on tests and sleepcheck: one, so every nap reaches the
// same diagnostics.
func (b *Backend) system() *handson.System {
	b.sysOnce.Do(func() {
		r := b.R
		b.sys = &handson.System{Root: r.Opt.Root}
		if !r.Fixture() {
			b.sys.NetFn = b.netNow // the status line's network (link.go)
			b.sys.Diag = sleepDiag{b}
		}
	})
	return b.sys
}

// nap is one nap's kernel lines, for bugreport.
type nap struct {
	at    time.Time
	how   handson.SleepHow
	mode  string
	sl    handson.Sleep
	saved string
}

// maxNaps is how many naps bugreport keeps (the newest).
const maxNaps = 8

// sleepDiag is the run's handson.SleepDiag.
type sleepDiag struct{ b *Backend }

// Before saves the pre-sleep diagnostics to external storage and says where (or why not): before
// every nap, sleepcheck's steps too (0.7.6: the iMac10,1's hang at the devices level left no
// kernel log), each of those in a file of its own (the step and the time in its name).
func (d sleepDiag) Before(how handson.SleepHow, mode string) string {
	r := d.b.R
	name := "presleep/" + r.diagBase() + "." + strings.ReplaceAll(how.Test, "/", "_") + ".txt"
	if how.Test == "sleepcheck" {
		step := how.Level
		switch {
		case step != "":
		case how.Deep:
			step = "deep"
		default:
			step = "s2idle"
		}
		if len(how.Unbound) > 0 {
			step += "-nosbp2"
		}
		name = "presleep/" + r.diagBase() + ".sleepcheck-" + step + "-" + time.Now().UTC().Format("150405Z") + ".txt"
	}
	ctx, cancel := context.WithTimeout(r.ctx, 30*time.Second)
	defer cancel()
	p, err := send.SaveBytes(ctx, r.Opt.Root, r.Opt.StickDir, []byte(r.preSleepText(how, mode)), name, "")
	if err != nil {
		return i18n.T("the diagnostics didn't save: %s", errText(err))
	}
	return i18n.T("diagnostics saved to external storage: %s", p)
}

// After keeps the nap's kernel lines for bugreport.
func (d sleepDiag) After(how handson.SleepHow, sl handson.Sleep) {
	b := d.b
	b.napMu.Lock()
	defer b.napMu.Unlock()
	b.naps = append(b.naps, nap{at: time.Now(), how: how, mode: sl.Mode, sl: sl, saved: sl.Saved})
	if len(b.naps) > maxNaps {
		b.naps = b.naps[len(b.naps)-maxNaps:]
	}
}

// napsText is the naps kept, for bugreport's sleep-log.txt ("" when there were none).
func (b *Backend) napsText() string {
	b.napMu.Lock()
	defer b.napMu.Unlock()
	var out []string
	for _, n := range b.naps {
		what := n.how.Test
		if n.how.Level != "" {
			what += " (pm_test " + n.how.Level + ")"
		}
		out = append(out, fmt.Sprintf("== %s %s, mode %s: slept %.1f s, back in %.1f s", n.at.UTC().Format("15:04:05Z"), what, n.mode,
			n.sl.Slept.Seconds(), n.sl.Took.Seconds()))
		if n.sl.Err != nil {
			out = append(out, "error: "+n.sl.Err.Error())
		}
		if n.saved != "" {
			out = append(out, n.saved)
		}
		if n.sl.Timing != "" {
			out = append(out, n.sl.Timing)
		}
		out = append(out, n.sl.Log...)
		out = append(out, "")
	}
	return strings.Join(out, "\n")
}

// diagBase is the name the diagnostics' files share with the copy kept before a test that
// sleeps ("iMac10,1-20261015T184500Z"): a new one when there's no copy yet.
func (r *Run) diagBase() string {
	r.mu.Lock()
	keep := r.keep
	r.mu.Unlock()
	if keep == "" {
		keep = send.FileName(r.Ident.Identifier, "", time.Now())
	}
	return strings.TrimSuffix(path.Base(keep), ".json")
}

// preSleepText is the pre-sleep diagnostics: what the kernel says about sleeping, the wakeup
// sources, the modules, the PCI devices and their drivers, and the kernel log so far.
func (r *Run) preSleepText(how handson.SleepHow, mode string) string {
	root := r.Opt.Root
	var b strings.Builder
	line := func(f string, a ...any) { fmt.Fprintf(&b, f+"\n", a...) }
	section := func(name string) { line("\n== %s", name) }
	line("OmaBoot? Live %s: diagnostics saved right before a nap", r.Opt.Version)
	line("test: %s (%s); mode %s; light sleep %v", how.Test, how.Title, mode, how.Light)
	if how.Level != "" {
		line("a test suspend: /sys/power/pm_test %s (the kernel goes that far, waits about 5 s and comes back)", how.Level)
	}
	if len(how.Unbound) > 0 {
		way := "unbound and the module unloaded (modprobe -r)"
		if how.SBP2Way == handson.SBP2Autoprobe {
			way = "unbound, the firewire bus's driver autoprobe off (the module couldn't be removed)"
		}
		line("nosbp2: firewire_sbp2 %s for this nap: %s", way, strings.Join(how.Unbound, "; "))
	}
	line("saved: %s, %.1f s after the kernel started", time.Now().UTC().Format(time.RFC3339), uptime(root).Seconds())
	line("Mac: %s, board %s, kernel %s", r.HW.ProductName, r.HW.BoardID, r.HW.Kernel)
	line("If this is the newest file here and the Mac had to be forced off, the nap didn't come back.")
	section("sleep")
	for _, p := range []string{"/sys/power/mem_sleep", "/sys/power/state", "/sys/power/pm_test", "/sys/power/pm_async",
		"/sys/power/pm_print_times", "/sys/power/pm_debug_messages", "/sys/module/printk/parameters/console_suspend",
		"/proc/sys/kernel/printk", "/sys/power/suspend_stats/success", "/sys/power/suspend_stats/fail",
		"/sys/power/suspend_stats/last_failed_dev", "/sys/power/suspend_stats/last_failed_step"} {
		line("%s: %s", p, root.Read(p))
	}
	section("/proc/acpi/wakeup")
	b.WriteString(string(root.Bytes("/proc/acpi/wakeup")))
	section("modules (/proc/modules: name, size, users, used by)")
	for _, l := range root.Lines("/proc/modules") {
		if f := strings.Fields(l); len(f) >= 4 {
			line("%s %s %s %s", f[0], f[1], f[2], strings.TrimSuffix(f[3], ","))
		}
	}
	section("PCI devices (address, vendor:device, class, driver)")
	for _, d := range root.Glob("/sys/bus/pci/devices/*") {
		drv := path.Base(root.Link(d + "/driver"))
		if drv == "." || drv == "/" {
			drv = "(none)"
		}
		line("%s %s:%s %s %s", path.Base(d), strings.TrimPrefix(root.Read(d+"/vendor"), "0x"), strings.TrimPrefix(root.Read(d+"/device"), "0x"),
			root.Read(d+"/class"), drv)
	}
	section("FireWire bus (/sys/bus/firewire/devices: node, vendor, model, guid; units specifier:version and driver; disks)")
	for _, l := range handson.FWBusText(root, root) {
		line("%s", l)
	}
	section("kernel log")
	for _, l := range checks.ReadKmsgTimed(root) {
		line("%s", l)
	}
	return b.String()
}

// sbp2Back gives firewire_sbp2 back after a nosbp2 step, the trail and the console told first,
// and says whether every disk came back (made read-only again).
func (b *Backend) sbp2Back(io *handson.System, off handson.SBP2Off, mark func(string), say func(bool, bool, ...command.Line)) bool {
	msg, err := io.Back(off, mark)
	if err != nil && msg == "" {
		msg = err.Error()
	}
	mark(msg)
	if err != nil {
		say(false, false, command.Line{command.S("nosbp2: "+msg, command.Bad)})
		return false
	}
	return true
}

// stepsArg is sleepcheck's argument for steps: the step, or all.
func stepsArg(steps []string) string {
	if len(steps) == 1 {
		return steps[0]
	}
	return "all"
}

// SleepInfo is what the kernel offers for sleeping (sleepcheck without arguments).
func (b *Backend) SleepInfo() (command.SleepInfo, error) {
	if b.R.Fixture() {
		return command.SleepInfo{}, errors.New(i18n.T("not on a fixture: nothing is put to sleep"))
	}
	d := handson.ReadPMDiag(b.system())
	return command.SleepInfo{MemSleep: d.MemSleep, States: d.States, Levels: d.Levels, ConsoleSuspend: d.ConsoleSuspend, PrintTimes: d.PrintTimes}, nil
}

// SleepCheck starts sleepcheck (command/sleepcheck.go): what is a pm_test level, "all",
// "s2idle" or "deep" (0.7.7: a real deep sleep), optionally followed by "nosbp2" (0.7.6:
// firewire_sbp2 taken off the FireWire disks for each step, given back after; 0.7.7: unloaded). It refuses what this kernel doesn't offer; the work goes on
// off the screen's goroutine, its lines coming as ui.SleepCheckMsg.
func (b *Backend) SleepCheck(what string) error {
	if b.R.Fixture() {
		return errors.New(i18n.T("not on a fixture: nothing is put to sleep"))
	}
	f := strings.Fields(what)
	nosbp2 := len(f) == 2 && f[1] == "nosbp2"
	if len(f) == 0 || len(f) > 2 || len(f) == 2 && !nosbp2 {
		return errors.New(i18n.T("sleepcheck all, a level, s2idle or deep; nosbp2 after it for the FireWire experiment"))
	}
	what = f[0]
	io := b.system()
	if nosbp2 && len(handson.SBP2Units(io)) == 0 {
		return errors.New(i18n.T("nosbp2: no FireWire disk with firewire_sbp2 bound to it: plug the disk in first"))
	}
	d := handson.ReadPMDiag(io)
	var steps []string
	switch what {
	case "s2idle":
		if !d.Offers("freeze") {
			return errors.New(i18n.T("this kernel doesn't offer light sleep (no freeze in /sys/power/state)"))
		}
		steps = []string{what}
	case "deep":
		if !d.Offers("mem") || !strings.Contains(" "+strings.NewReplacer("[", "", "]", "").Replace(d.MemSleep)+" ", " deep ") {
			return errors.New(i18n.T("this kernel doesn't offer deep sleep on this Mac (mem_sleep: %s)", d.MemSleep))
		}
		steps = []string{what}
	case "all":
		if len(d.Levels) == 0 {
			return errors.New(i18n.T("no /sys/power/pm_test on this kernel (it needs CONFIG_PM_DEBUG)"))
		}
		for _, l := range handson.PMLevels {
			if d.HasLevel(l) {
				steps = append(steps, l)
			}
		}
	default:
		if !d.HasLevel(what) {
			if len(d.Levels) == 0 {
				return errors.New(i18n.T("no /sys/power/pm_test on this kernel (it needs CONFIG_PM_DEBUG)"))
			}
			return errors.New(i18n.T("not a level: %s (all, %s, s2idle or deep)", what, strings.Join(d.Levels, ", ")))
		}
		steps = []string{what}
	}
	go b.sleepCheck(io, steps, d, nosbp2)
	return nil
}

// sleepCheck runs the steps in order, stopping at the first that fails. Before each, its start
// goes into the trail on external storage (synced), then into the console, and the screen gets a
// moment to show it. A disc in the drive or a FireWire disk attached (0.7.6: it hung the
// iMac10,1 at the devices level) stops it before any step; nosbp2 instead takes firewire_sbp2
// off the FireWire disks before each step (0.7.7: unbound, then the module unloaded, or the bus's
// driver autoprobe off) and gives it back after (noting each move in the trail first), the
// FireWire guard set aside for this experiment only.
func (b *Backend) sleepCheck(io *handson.System, steps []string, d handson.PMDiag, nosbp2 bool) {
	r := b.R
	say := func(back, done bool, ls ...command.Line) { b.post(ui.SleepCheckMsg{Lines: ls, Back: back, Done: done}) }
	held := func() bool {
		if why := handson.DiscHeld(io); why != "" {
			say(false, true, command.Line{command.S("sleepcheck: "+why+": "+i18n.T("eject it first (a Mac that sleeps with a disc in can hang)"), command.Bad)})
			return true
		}
		if why := handson.FireWireHeld(io); why != "" && !nosbp2 {
			say(false, true, command.Line{command.S("sleepcheck: "+why+": "+i18n.T("unplug it first: a FireWire disk can hang a Mac's sleep under Linux (it did on an iMac10,1)"), command.Bad)},
				command.Line{command.S(i18n.T("or try it with the disk's driver unloaded: sleepcheck %s nosbp2", stepsArg(steps)), command.Label)})
			return true
		}
		return false
	}
	if held() {
		return
	}
	title := strings.Join(steps, " ")
	if nosbp2 {
		title += " nosbp2"
	}
	name := "sleepcheck/" + strings.TrimSuffix(path.Base(send.FileName(r.Ident.Identifier, "", time.Now())), ".json") + ".txt"
	trail := []string{
		fmt.Sprintf("OmaBoot? Live %s: sleepcheck %s on %s (%s), kernel %s", r.Opt.Version, title, r.Ident.Identifier, r.HW.BoardID, r.HW.Kernel),
		"mem_sleep: " + d.MemSleep + "; states: " + d.States + "; pm_test: " + strings.Join(d.Levels, " "),
		"Each step is written here before it starts: if the Mac had to be forced off, the last \"start\" line names the step it hung in.",
		"",
	}
	saved := ""
	save := func() {
		ctx, cancel := context.WithTimeout(r.ctx, 30*time.Second)
		defer cancel()
		p, err := send.SaveBytes(ctx, r.Opt.Root, r.Opt.StickDir, []byte(strings.Join(trail, "\n")+"\n"), name, "")
		switch {
		case err != nil:
			say(false, false, command.Line{command.S(i18n.T("the trail didn't save to external storage: %s", errText(err)), command.Mark)})
		case saved == "":
			saved = p
		}
	}
	// mark notes a step of the experiment in the trail, saved, then in the console, before it's done
	mark := func(s string) {
		trail = append(trail, time.Now().UTC().Format("15:04:05Z")+" nosbp2: "+s)
		save()
		say(false, false, command.Line{command.S(time.Now().Format("15:04:05")+" ", command.Label), command.S("nosbp2: "+s, command.Arg)})
	}
	for i, step := range steps {
		if i > 0 && held() { // a disc or a FireWire disk went in between steps
			break
		}
		how := handson.SleepHow{Test: "sleepcheck", Title: "sleepcheck " + step, Level: step}
		wake := time.Duration(0)
		start := i18n.T("%s: the kernel goes this far, waits about 5 s and comes back (no real sleep)", step)
		switch step {
		case "s2idle":
			how = handson.SleepHow{Test: "sleepcheck", Title: "sleepcheck s2idle", Light: true}
			wake = 15 * time.Second
			start = i18n.T("s2idle: a real 15 s nap in light sleep; the alarm wakes it (if not within a minute, press a key)")
		case "deep":
			how = handson.SleepHow{Test: "sleepcheck", Title: "sleepcheck deep", Deep: true}
			wake = 20 * time.Second
			start = i18n.T("deep: a real 20 s deep sleep; the alarm wakes it (if not within a minute, press a key or the power button)")
		}
		var off handson.SBP2Off
		if nosbp2 {
			how.Title += " nosbp2"
			var err error
			off, err = io.Unload(mark)
			if err != nil {
				text := "nosbp2: " + err.Error()
				if len(off.Problems) > 0 {
					text += " (" + strings.Join(off.Problems, "; ") + ")"
				}
				trail = append(trail, time.Now().UTC().Format("15:04:05Z")+" "+text+"; the step isn't run", "")
				save()
				say(false, false, command.Line{command.S(text+"; "+i18n.T("the step isn't run"), command.Bad)})
				if off.Taken {
					b.sbp2Back(io, off, mark, say)
				}
				break
			}
			how.Unbound, how.SBP2Way = off.Labels, off.Way
			gone := strings.Join(off.Disks, " ")
			if gone == "" {
				gone = "no block device"
			}
			if off.Way == handson.SBP2Unloaded {
				start += "; " + i18n.T("firewire_sbp2 is unloaded")
				mark(fmt.Sprintf("firewire_sbp2 unloaded (modprobe -r; /sys/module/firewire_sbp2 gone); released: %s; gone: %s", strings.Join(off.Units, ", "), gone))
			} else {
				start += "; " + i18n.T("the FireWire disk's driver is unbound, autoprobe off")
				mark(fmt.Sprintf("modprobe -r didn't work: %s unbound and the firewire bus's driver autoprobe off; gone: %s", strings.Join(off.Units, ", "), gone))
			}
		}
		trail = append(trail, time.Now().UTC().Format("15:04:05Z")+" start "+step)
		save()
		say(false, false, command.Line{command.S(time.Now().Format("15:04:05")+" ", command.Label), command.T(start)})
		time.Sleep(1500 * time.Millisecond) // the console shows it before the Mac goes
		if nosbp2 {
			if why := handson.SBP2Still(io, off); why != "" { // udev, or a bus reset, undid it meanwhile
				mark(why + "; the step isn't run")
				say(false, false, command.Line{command.S("nosbp2: "+why+"; "+i18n.T("the step isn't run"), command.Bad)})
				b.sbp2Back(io, off, mark, say)
				break
			}
		}
		f := io.Suspend(wake, how)
		var sl handson.Sleep
		for {
			v, ok := f.Ready()
			if ok {
				sl = v
				break
			}
			time.Sleep(100 * time.Millisecond)
		}
		var res command.Line
		var text string
		switch {
		case sl.Err != nil:
			text = fmt.Sprintf("%s: failed after %.1f s: %v", step, sl.Took.Seconds(), sl.Err)
			if sl.Failed != "" {
				text += "; last failed: " + sl.Failed
			}
			res = command.Line{command.S(text, command.Bad)}
		case wake > 0:
			text = fmt.Sprintf("%s: slept %.1f s of %d, back in %.1f s", step, sl.Slept.Seconds(), int(wake.Seconds()), sl.Took.Seconds())
			if len(sl.WokenBy) > 0 {
				text += "; woken by " + strings.Join(sl.WokenBy, ", ")
			}
			res = command.Line{command.S(text, command.Value)}
		default:
			text = fmt.Sprintf("%s: came back after %.1f s", step, sl.Took.Seconds())
			res = command.Line{command.S(text, command.Value)}
		}
		trail = append(trail, time.Now().UTC().Format("15:04:05Z")+" "+text)
		if sl.Timing != "" {
			trail = append(trail, "  "+sl.Timing)
		}
		for _, l := range sl.Log {
			trail = append(trail, "  | "+l)
		}
		trail = append(trail, "")
		save()
		ls := append([]command.Line{res}, command.TimingLines(sl.Timing)...)
		say(true, false, ls...)
		if nosbp2 {
			if !b.sbp2Back(io, off, mark, say) {
				break // the disk isn't back: the next step would run without it
			}
		}
		if sl.Err != nil {
			break
		}
	}
	end := command.Line{command.S(i18n.T("sleepcheck done; the kernel's lines are in bugreport"), command.Label)}
	if saved != "" {
		end = command.Line{command.S(i18n.T("sleepcheck done; its trail is on external storage:")+" ", command.Label), command.S(saved, command.Arg)}
	}
	say(false, true, end)
}
