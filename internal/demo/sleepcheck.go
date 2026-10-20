package demo

import (
	"errors"
	"strings"
	"time"

	"github.com/omaboot/boot-live/internal/command"
	"github.com/omaboot/boot-live/internal/i18n"
	"github.com/omaboot/boot-live/internal/ui"
)

// The demo's sleepcheck: the kernel's offer as on linux-omarchy 7.2.5-3, and a scripted "all"
// that comes back at every level (s2idle and deep, 0.7.7, each a real nap) (the real run's is internal/app/sleepdiag.go). Nothing sleeps.

// SleepInfo is linux-omarchy 7.2.5-3's offer.
func (b *Backend) SleepInfo() (command.SleepInfo, error) {
	return command.SleepInfo{MemSleep: "s2idle [deep]", States: "freeze mem disk", Levels: []string{"core", "processors", "platform", "devices", "freezer"},
		ConsoleSuspend: "Y", PrintTimes: true}, nil
}

// SleepCheck plays SleepCheckSteps a step every 2 s.
func (b *Backend) SleepCheck(what string) error {
	lv, _, _ := strings.Cut(what, " ")
	if strings.HasSuffix(what, " nosbp2") {
		what = lv + " nosbp2"
	} else {
		what = lv
	}
	if lv != "all" && lv != "freezer" && lv != "devices" && lv != "platform" && lv != "processors" && lv != "core" && lv != "s2idle" && lv != "deep" {
		return errors.New(i18n.T("not a level: %s (all, %s, s2idle or deep)", lv, "core, processors, platform, devices, freezer"))
	}
	for i, m := range SleepCheckSteps(what) {
		after(time.Duration(i+1)*2*time.Second, func() { b.post(m) })
	}
	return nil
}

// SleepCheckSteps are the demo's sleepcheck messages, in order, for the demo and tools/screens.
// "devices nosbp2" (any step, then nosbp2) unloads firewire_sbp2 from the demo's FireWire disk
// first and loads it again after, as on the iMac10,1 (0.7.7); after a real sleep the drive is
// back as a new node.
func SleepCheckSteps(what string) []ui.SleepCheckMsg {
	what, nosbp2 := strings.CutSuffix(what, " nosbp2")
	levels := []string{"freezer", "devices", "platform", "processors", "core"}
	if what != "all" {
		levels = []string{what}
	}
	clock := time.Date(2026, 10, 15, 21, 4, 10, 0, time.UTC)
	var out []ui.SleepCheckMsg
	timing := map[string]string{
		"freezer":    "the kernel's steps: sync 0.01 s, freezing tasks 0.01 s",
		"devices":    "the kernel's steps: sync 0.01 s, freezing tasks 0.01 s; suspend: devices 412 ms; resume: devices 1.6 s; slowest: radeon 0000:01:00.0 pci_pm_resume 1.2 s",
		"platform":   "the kernel's steps: suspend: devices 405 ms, late 2 ms, noirq 31 ms; resume: noirq 44 ms, early 1 ms, devices 1.6 s",
		"processors": "the kernel's steps: suspend: devices 398 ms, late 2 ms, noirq 30 ms; resume: noirq 41 ms, early 1 ms, devices 1.5 s",
		"core":       "the kernel's steps: suspend: devices 401 ms, late 2 ms, noirq 30 ms; resume: noirq 43 ms, early 1 ms, devices 1.6 s",
		"s2idle":     "the kernel's steps: suspend: devices 380 ms; resume: devices 1.4 s",
		"deep":       "the kernel's steps: sync 0.02 s, freezing tasks 0.03 s; suspend: devices 1.1 s, late 3 ms, noirq 32 ms; resume: noirq 45 ms, early 2 ms, devices 4.6 s; slowest: radeon 0000:01:00.0 pci_pm_resume 4.3 s",
	}
	mark := func(s string) ui.SleepCheckMsg {
		return ui.SleepCheckMsg{Lines: []command.Line{{command.S(clock.Format("15:04:05")+" ", command.Label), command.S("nosbp2: "+s, command.Arg)}}}
	}
	for _, l := range levels {
		start := i18n.T("%s: the kernel goes this far, waits about 5 s and comes back (no real sleep)", l)
		res := l + ": came back after 5.4 s"
		real := l == "s2idle" || l == "deep"
		switch l {
		case "s2idle":
			start = i18n.T("s2idle: a real 15 s nap in light sleep; the alarm wakes it (if not within a minute, press a key)")
			res = "s2idle: slept 14.8 s of 15, back in 16.9 s; woken by the RTC alarm"
		case "deep":
			start = i18n.T("deep: a real 20 s deep sleep; the alarm wakes it (if not within a minute, press a key or the power button)")
			res = "deep: slept 18.6 s of 20, back in 6.2 s; woken by the RTC alarm"
		}
		if nosbp2 { // the WD drive: fw1.0 is the disk, fw1.1 has none
			start += "; " + i18n.T("firewire_sbp2 is unloaded")
			out = append(out, mark("unbinding fw1.0 from firewire_sbp2 (echo fw1.0 > /sys/bus/firewire/drivers/firewire_sbp2/unbind)"),
				mark("unbinding fw1.1 from firewire_sbp2 (echo fw1.1 > /sys/bus/firewire/drivers/firewire_sbp2/unbind)"),
				mark("modprobe -r firewire_sbp2"),
				mark("firewire_sbp2 unloaded (modprobe -r; /sys/module/firewire_sbp2 gone); released: fw1.0, fw1.1; gone: sdc"))
		}
		out = append(out, ui.SleepCheckMsg{Lines: []command.Line{{command.S(clock.Format("15:04:05")+" ", command.Label), command.T(start)}}},
			ui.SleepCheckMsg{Back: true, Lines: append([]command.Line{{command.S(res, command.Value)}}, command.TimingLines(timing[l])...)})
		clock = clock.Add(9 * time.Second)
		if nosbp2 {
			if real { // the sleep reset the bus: the drive is back as a new node, and udev loaded the driver for it
				out = append(out, mark("firewire_sbp2 is loaded already (udev loaded it for the drive)"),
					mark("back: fw2.0 sdd (was fw1.0 sdc), bound to firewire_sbp2, made read-only"))
			} else {
				out = append(out, mark("modprobe firewire_sbp2"), mark("back: fw1.0 sdd (was fw1.0 sdc), bound to firewire_sbp2, made read-only"))
			}
			clock = clock.Add(4 * time.Second)
		}
	}
	out = append(out, ui.SleepCheckMsg{Done: true, Lines: []command.Line{{command.S(i18n.T("sleepcheck done; its trail is on external storage:")+" ", command.Label),
		command.S("sleepcheck/"+MacID+"-20261015T210410Z.txt", command.Arg)}}})
	return out
}
