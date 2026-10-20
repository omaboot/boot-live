package command

import (
	"strings"

	"github.com/omaboot/boot-live/internal/i18n"
)

// sleepcheck tests the kernel's suspend a step at a time (0.7.5; the iMac10,1 hangs going to
// sleep): /sys/power/pm_test makes a suspend go only so far (freezer, devices, platform,
// processors, core), wait about 5 s and come back without sleeping; all runs the levels from the
// least deep, stopping at the first that fails; s2idle is a real 15 s nap in light sleep, the RTC
// alarm waking it, to compare with deep. Each step is written to external storage before it
// starts, so a hang leaves a trail of the level that started last. Read-only for the report (it
// records nothing), so release builds have it too.
//
// nosbp2 after a level, all or s2idle (0.7.6; the iMac10,1 hung at the devices level with a
// FireWire disk attached) is an experiment: firewire_sbp2 is taken off the FireWire disks before
// each step and given back after, each move written to the trail first. Without it, sleepcheck
// refuses while a FireWire disk is attached, as the sleep and lid tests do. 0.7.7: nosbp2
// unloads the module for the step (a real sleep resets the bus, and the driver bound to the
// drive again mid-suspend), and deep is a real 20 s deep sleep, the RTC alarm waking it, like the
// Sleep and wake test: with nosbp2, the real test of the workaround.
func init() {
	Register(Cmd{Name: "sleepcheck", Args: i18n.M("[step]"), Help: i18n.M("test suspend step by step (Tab lists the steps)"), Order: 47,
		Complete: func(_ Env, args []string) []string {
			switch len(args) {
			case 0:
				return []string{"all", "freezer", "devices", "platform", "processors", "core", "s2idle", "deep"}
			case 1:
				return []string{"nosbp2"}
			}
			return nil
		},
		Run: func(env Env, args []string, out *Out) error {
			if len(args) > 2 || len(args) == 2 && args[1] != "nosbp2" {
				return Err(i18n.T("one at a time: sleepcheck all, a level, s2idle or deep (nosbp2 after it for the FireWire experiment)"))
			}
			if len(args) >= 1 {
				if err := env.SleepCheck(strings.Join(args, " ")); err != nil {
					return err
				}
				out.Dim(i18n.T("starting: each step prints here, saved to external storage before it begins"))
				if len(args) == 2 {
					out.Dim(i18n.T("nosbp2: firewire_sbp2 is unloaded before each step (modprobe -r) and loaded again after"))
				}
				return nil
			}
			in, err := env.SleepInfo()
			if err != nil {
				return err
			}
			out.Line(S(pad(i18n.T("sleep modes"), 17), Label), S(orDash(in.MemSleep), Value))
			out.Line(S(pad(i18n.T("states"), 17), Label), S(orDash(in.States), Value))
			if len(in.Levels) > 0 {
				out.Line(S(pad("pm_test", 17), Label), S(strings.Join(in.Levels, " "), Arg))
			} else {
				out.Line(S(pad("pm_test", 17), Label), S(i18n.T("not on this kernel (it needs CONFIG_PM_DEBUG)"), Bad))
			}
			switch in.ConsoleSuspend {
			case "":
				out.Line(S(pad("console_suspend", 17), Label), S(i18n.T("not on this kernel: the screen goes quiet while devices suspend"), Mark))
			default:
				out.Line(S(pad("console_suspend", 17), Label), S(in.ConsoleSuspend, Value), T("  "+i18n.T("(off for each nap, so the kernel's steps print)")))
			}
			out.Dim(i18n.T("sleepcheck all: freezer to core, about 5 s each, no real sleep; sleepcheck s2idle: a real 15 s light sleep"))
			out.Dim(i18n.T("sleepcheck deep: a real 20 s deep sleep, as in Sleep and wake"))
			out.Dim(i18n.T("with a FireWire disk attached: sleepcheck deep nosbp2 (firewire_sbp2 unloaded for the step)"))
			return nil
		}})
}

func orDash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}

// TimingLines are a nap's kernel step times (handson.PMTiming's words) as sleepcheck prints
// them: one part a line, indented, dim, so long ones don't run off the box.
func TimingLines(timing string) []Line {
	var out []Line
	for _, p := range strings.Split(strings.TrimPrefix(timing, "the kernel's steps: "), "; ") {
		if p != "" {
			out = append(out, Line{S("  "+p, Label)})
		}
	}
	return out
}
