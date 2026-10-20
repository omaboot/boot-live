package handson

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Sleep diagnostics (0.7.5): the iMac10,1 hangs going to sleep, leaving an
// empty screen with a blinking cursor and nothing on record. For every nap the tool now:
//
//   - turns on the kernel's suspend diagnostics for that nap and puts them back after
//     (pmDebugOn): pm_print_times (each device's suspend and resume call, timed),
//     pm_debug_messages (the PM core's steps), console_suspend off (the console keeps printing
//     while devices suspend, as no_console_suspend does) and the console log level at 8, so the
//     steps print on the screen, under a heading (screenNote): if the Mac hangs, the last line on
//     the screen names the step, and a photo of it is the record;
//   - saves the pre-sleep diagnostics to external storage right before the kernel is asked to
//     sleep (System.Diag, the app's), synced, so a forced power-off keeps them;
//   - after the nap, keeps the kernel's lines for it (NapLog, for bugreport) and their times,
//     short, for the evidence (PMTiming).

const (
	pmTest    = "/sys/power/pm_test"
	suspendVT = "/dev/tty63" // SUSPEND_CONSOLE: the VT the kernel moves to while the Mac sleeps
)

// PMLevels are pm_test's levels from the least deep (Documentation/power/basic-pm-debugging.rst).
var PMLevels = []string{"freezer", "devices", "platform", "processors", "core"}

// pmDebug are the settings pmDebugOn changes: the file and the value it gets for the nap.
var pmDebug = []struct{ path, on string }{
	{"/sys/power/pm_print_times", "1"},
	{"/sys/power/pm_debug_messages", "1"},
	{"/sys/module/printk/parameters/console_suspend", "N"},
}

// pmDebugOn turns the kernel's suspend diagnostics on for one nap and returns what puts them
// back. A setting the kernel doesn't have is left alone. The console log level is the first
// field of /proc/sys/kernel/printk (boot-live keeps it at 1 while it runs, console.Setup).
func pmDebugOn(io IO) (restore func()) {
	type was struct{ path, v string }
	var saved []was
	for _, d := range pmDebug {
		old := io.Read(d.path)
		if old == "" || old == d.on {
			continue
		}
		if io.Write(d.path, d.on) == nil {
			saved = append(saved, was{d.path, old})
		}
	}
	const printk = "/proc/sys/kernel/printk"
	if f := strings.Fields(io.Read(printk)); len(f) > 0 && f[0] != "8" && io.Write(printk, "8") == nil {
		saved = append(saved, was{printk, f[0]})
	}
	return func() {
		for i := len(saved) - 1; i >= 0; i-- {
			io.Write(saved[i].path, saved[i].v)
		}
	}
}

// PMDiag says which of the kernel's suspend diagnostics this kernel has, for sleepcheck.
type PMDiag struct {
	MemSleep       string   // /sys/power/mem_sleep as it reads: "s2idle [deep]"
	States         string   // /sys/power/state: "freeze mem disk"
	Levels         []string // pm_test's levels (CONFIG_PM_DEBUG); nil without it
	PrintTimes     bool     // /sys/power/pm_print_times
	DebugMessages  bool     // /sys/power/pm_debug_messages
	ConsoleSuspend string   // printk's console_suspend parameter (Y, N), "" when it isn't there
}

// ReadPMDiag reads what the kernel offers.
func ReadPMDiag(io IO) PMDiag {
	d := PMDiag{MemSleep: io.Read("/sys/power/mem_sleep"), States: io.Read("/sys/power/state"),
		PrintTimes: io.Read("/sys/power/pm_print_times") != "", DebugMessages: io.Read("/sys/power/pm_debug_messages") != "",
		ConsoleSuspend: io.Read("/sys/module/printk/parameters/console_suspend")}
	for _, f := range strings.Fields(io.Read(pmTest)) {
		if l := strings.Trim(f, "[]"); l != "none" {
			d.Levels = append(d.Levels, l)
		}
	}
	return d
}

// Offers reports whether /sys/power/state lists a state ("freeze", "mem").
func (d PMDiag) Offers(state string) bool {
	for _, f := range strings.Fields(d.States) {
		if f == state {
			return true
		}
	}
	return false
}

// HasLevel reports whether pm_test offers a level.
func (d PMDiag) HasLevel(l string) bool {
	for _, x := range d.Levels {
		if x == l {
			return true
		}
	}
	return false
}

// noteLines is screenNote's heading.
func noteLines(how SleepHow, mode string, wake time.Duration, now time.Time) []string {
	what := how.Title
	if what == "" {
		what = how.Test
	}
	nap := fmt.Sprintf("%s sleep", orUnknown(mode))
	switch {
	case how.Level != "":
		nap = fmt.Sprintf("a test suspend to the %q level (%s sleep): back by itself in about 5 s", how.Level, orUnknown(mode))
	case wake > 0:
		nap += fmt.Sprintf(", the alarm set to wake it in %d s", int(wake.Seconds()))
	}
	return []string{
		fmt.Sprintf("OmaBoot? Live, %s: %s, %s UTC.", what, nap, now.UTC().Format("15:04:05")),
		"The kernel's suspend steps follow. If the Mac doesn't come back, photograph this screen:",
		"the last lines name the step it stopped in. A copy of the diagnostics is on external storage.",
	}
}

// NapLog is the kernel log's lines for the last nap: from its last "PM: suspend entry" on (all
// of the log's tail when there's none).
func NapLog(lines []string) []string {
	for i := len(lines) - 1; i >= 0; i-- {
		if strings.Contains(lines[i], "PM: suspend entry") {
			return append([]string(nil), lines[i:]...)
		}
	}
	return nil
}

var (
	// "PM: late suspend of devices complete after 1.234 msecs" (dpm_show_time, pm_debug_messages)
	pmPhaseRe = regexp.MustCompile(`PM: (?:(late|noirq|early) )?(suspend|resume) of devices (complete|aborted) after ([0-9.]+) msecs`)
	// "radeon 0000:01:00.0: pci_pm_resume+0x0/0xf0 returned 0 after 1201345 usecs" (pm_print_times)
	pmCallRe = regexp.MustCompile(`^(?:\[[^\]]*\]\s*)?(.+?): (?:PM: )?([A-Za-z0-9_.]+)(?:\+0x[0-9a-f]+/0x[0-9a-f]+)?(?: \[[^\]]+\])? returned (-?\d+) after (\d+) usecs`)
	// "Freezing user space processes completed (elapsed 0.001 seconds)", "Filesystems sync: 0.012 seconds"
	pmFreezeRe = regexp.MustCompile(`Freezing (?:user space processes|remaining freezable tasks) (?:completed|failed) \(elapsed ([0-9.]+) seconds\)`)
	pmSyncRe   = regexp.MustCompile(`Filesystems sync: ([0-9.]+) seconds`)
)

// PMTiming sums up a nap's kernel lines (NapLog) for the evidence, short: how long the file
// systems' sync and the freezer took, each device phase on the way down and up, the slowest
// device calls (50 ms or more, three at most) and any call that returned an error. "" when the
// lines say nothing (the diagnostics were off).
func PMTiming(lines []string) string {
	var sync, freeze float64
	var syncSeen, freezeSeen bool
	var down, up []string
	type call struct {
		dev, cb string
		us      int64
		ret     int
	}
	var calls []call
	for _, l := range lines {
		if m := pmSyncRe.FindStringSubmatch(l); m != nil {
			v, _ := strconv.ParseFloat(m[1], 64)
			sync, syncSeen = sync+v, true
			continue
		}
		if m := pmFreezeRe.FindStringSubmatch(l); m != nil {
			v, _ := strconv.ParseFloat(m[1], 64)
			freeze, freezeSeen = freeze+v, true
			continue
		}
		if m := pmPhaseRe.FindStringSubmatch(l); m != nil {
			v, _ := strconv.ParseFloat(m[4], 64)
			phase := m[1]
			if phase == "" {
				phase = "devices"
			}
			s := fmt.Sprintf("%s %s", phase, ms(v))
			if m[3] == "aborted" {
				s += " (aborted)"
			}
			if m[2] == "suspend" {
				down = append(down, s)
			} else {
				up = append(up, s)
			}
			continue
		}
		if m := pmCallRe.FindStringSubmatch(l); m != nil {
			us, _ := strconv.ParseInt(m[4], 10, 64)
			ret, _ := strconv.Atoi(m[3])
			calls = append(calls, call{dev: m[1], cb: m[2], us: us, ret: ret})
		}
	}
	var parts []string
	if syncSeen || freezeSeen {
		var p []string
		if syncSeen {
			p = append(p, fmt.Sprintf("sync %.2f s", sync))
		}
		if freezeSeen {
			p = append(p, fmt.Sprintf("freezing tasks %.2f s", freeze))
		}
		parts = append(parts, strings.Join(p, ", "))
	}
	if len(down) > 0 {
		parts = append(parts, "suspend: "+strings.Join(down, ", "))
	}
	if len(up) > 0 {
		parts = append(parts, "resume: "+strings.Join(up, ", "))
	}
	var bad []string
	for _, c := range calls {
		if c.ret != 0 {
			bad = append(bad, fmt.Sprintf("%s %s returned %d", c.dev, c.cb, c.ret))
		}
	}
	sort.SliceStable(calls, func(i, j int) bool { return calls[i].us > calls[j].us })
	var slow []string
	for _, c := range calls {
		if c.us < 50000 || len(slow) == 3 {
			break
		}
		slow = append(slow, fmt.Sprintf("%s %s %s", c.dev, c.cb, ms(float64(c.us)/1000)))
	}
	if len(slow) > 0 {
		parts = append(parts, "slowest: "+strings.Join(slow, ", "))
	}
	if len(bad) > 0 {
		if len(bad) > 3 {
			bad = append(bad[:3], fmt.Sprintf("%d more", len(bad)-3))
		}
		parts = append(parts, "errors: "+strings.Join(bad, ", "))
	}
	if len(parts) == 0 {
		return ""
	}
	return "the kernel's steps: " + strings.Join(parts, "; ")
}

// ms is a time in milliseconds as the evidence writes it: "152 ms", "1.9 s".
func ms(v float64) string {
	if v >= 1000 {
		return fmt.Sprintf("%.1f s", v/1000)
	}
	return fmt.Sprintf("%.0f ms", v)
}
