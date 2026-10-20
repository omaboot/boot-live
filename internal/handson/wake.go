package handson

import (
	"fmt"
	"path"
	"sort"
	"strconv"
	"strings"
)

// What woke the Mac (owner, 2026-10-12: the MacBookAir5,2 kept waking ~3 s into a 20 s nap and
// the report couldn't say why). /sys/power/pm_wakeup_irq stays empty when the firmware wakes
// the Mac through an ACPI GPE or fixed event, the usual case on a Mac, so the sleep tests also
// read the kernel's wakeup sources (/sys/class/wakeup) and the firmware's event counters
// (/sys/firmware/acpi/interrupts) just before and just after the suspend; what went up is what
// fired. /proc/acpi/wakeup names the devices allowed to wake it (XHC1, LID0…).

// wakeSnap is one reading of the counters.
type wakeSnap struct {
	sources map[string]int // wakeup source name → event_count
	events  map[string]int // gpeXX, ff_rt_clk… → count
}

func readWake(io IO) wakeSnap {
	w := wakeSnap{sources: map[string]int{}, events: map[string]int{}}
	for _, d := range io.Glob("/sys/class/wakeup/wakeup*") {
		name := io.Read(d + "/name")
		if n, ok := io.Int(d + "/event_count"); ok && name != "" {
			w.sources[name] += n
		}
	}
	for _, p := range io.Glob("/sys/firmware/acpi/interrupts/*") {
		k := path.Base(p)
		if !strings.HasPrefix(k, "gpe") && !strings.HasPrefix(k, "ff_") || k == "gpe_all" {
			continue
		}
		if f := strings.Fields(io.Read(p)); len(f) > 0 {
			if n, err := strconv.Atoi(f[0]); err == nil {
				w.events[k] = n
			}
		}
	}
	return w
}

// acpiWakeup reads /proc/acpi/wakeup: the ACPI name of each device by its sysfs name
// ("0000:00:14.0" → "XHC1") and the devices enabled to wake the Mac, in the file's order.
func acpiWakeup(text string) (names map[string]string, armed []string) {
	names = map[string]string{}
	for _, line := range strings.Split(text, "\n") {
		f := strings.Fields(line)
		if len(f) < 3 || f[0] == "Device" {
			continue
		}
		if f[2] == "*enabled" {
			armed = append(armed, f[0])
		}
		for _, node := range f[3:] { // "pci:0000:00:14.0", "platform:PNP0C0D:00"
			if _, dev, ok := strings.Cut(node, ":"); ok {
				names[dev] = f[0]
			}
		}
	}
	return names, armed
}

// eventName says what an ACPI interrupt counter is.
func eventName(k string) string {
	switch k {
	case "ff_rt_clk":
		return "RTC alarm event"
	case "ff_pwr_btn":
		return "power button event"
	case "ff_slp_btn":
		return "sleep button event"
	}
	if g, ok := strings.CutPrefix(k, "gpe"); ok {
		return "GPE 0x" + strings.ToUpper(g)
	}
	return k
}

// wokenBy compares two readings: the wakeup sources that fired (with their ACPI names, and the
// RTC's device marked as such) and the firmware events that went up.
func wokenBy(before, after wakeSnap, names map[string]string, rtc string) (sources, events []string) {
	for name, n := range after.sources {
		if d := n - before.sources[name]; d > 0 {
			label := name
			switch {
			case rtc != "" && name == rtc || name == "rtc0":
				label = "RTC alarm (" + name + ")"
			case names[name] != "":
				label = names[name] + " (" + name + ")"
			}
			sources = append(sources, label)
		}
	}
	for k, n := range after.events {
		if d := n - before.events[k]; d > 0 && k != "ff_pmtimer" && k != "ff_gbl_lock" {
			events = append(events, fmt.Sprintf("%s +%d", eventName(k), d))
		}
	}
	sort.Strings(sources)
	sort.Strings(events)
	return sources, events
}

// wakeEvidence is the report's wording, "" when there is nothing to say.
func wakeEvidence(sl Sleep) string {
	var parts []string
	if len(sl.WokenBy) > 0 {
		parts = append(parts, "wakeup sources that fired: "+strings.Join(sl.WokenBy, ", "))
	}
	if len(sl.Events) > 0 {
		parts = append(parts, "ACPI events: "+strings.Join(sl.Events, ", "))
	}
	if sl.Probed && len(sl.WokenBy) == 0 && len(sl.Events) == 0 {
		parts = append(parts, "no wakeup source or ACPI event counted")
	}
	if len(sl.Armed) > 0 {
		parts = append(parts, "allowed to wake it: "+strings.Join(sl.Armed, " "))
	}
	return strings.Join(parts, "; ")
}
