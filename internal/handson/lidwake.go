package handson

import "strings"

// The lid-wake cause and fix (research/findings/lid-wake-early-wake.md, spike-wake 2026-10-13):
// a Mac's lid _PSW sets the embedded controller's wake-on-lid-open bit, and Linux calls _PSW(1)
// before every sleep for each device enabled in /proc/acpi/wakeup, the lid by default. With the
// lid open, the EC then wakes the Mac a few seconds into the sleep. OmaBoot?'s fix (the image's
// systemd-sleep hook omaboot-lid-wake) turns the lid's wake off for a sleep that starts with the
// lid open; the sleep test does the same for its second nap.

// lidName is the lid's ACPI name ("LID0"), as /proc/acpi/wakeup lists it, or "".
func lidName(io IO) string {
	p := io.Read("/sys/bus/acpi/devices/PNP0C0D:00/path") // "\_SB_.LID0"
	if i := strings.LastIndex(p, "."); i >= 0 {
		return p[i+1:]
	}
	return ""
}

// lidIsOpen reports whether the lid switch says open.
func lidIsOpen(io IO) bool {
	for _, p := range io.Glob("/proc/acpi/button/lid/*/state") {
		if strings.Contains(io.Read(p), "open") {
			return true
		}
	}
	return false
}

// lidArmed reports whether /proc/acpi/wakeup has the lid enabled to wake the Mac.
func lidArmed(io IO, name string) bool {
	for _, l := range strings.Split(io.Read("/proc/acpi/wakeup"), "\n") {
		if f := strings.Fields(l); len(f) >= 3 && f[0] == name {
			return f[2] == "*enabled"
		}
	}
	return false
}

// setLidWake turns the lid's wake on or off (/proc/acpi/wakeup toggles a device per write).
func setLidWake(io IO, name string, on bool) bool {
	if lidArmed(io, name) != on {
		if io.Write("/proc/acpi/wakeup", name) != nil {
			return false
		}
	}
	return lidArmed(io, name) == on
}

// lidWokeIt reports whether the lid's wake woke the Mac: the lid's wakeup source fired, and
// neither the RTC alarm nor a PCI device (a USB or Thunderbolt controller, the network: their
// sources are named by PCI address) did. With the lid's wake armed, its source counts up on any
// resume, so it alone doesn't prove the lid did it. (With the lid's wake off, it doesn't count up.)
func lidWokeIt(sl Sleep) bool {
	lid, rtc := false, false
	for _, w := range sl.WokenBy {
		lid = lid || strings.Contains(w, "PNP0C0D")
		rtc = rtc || strings.HasPrefix(w, "RTC alarm") || strings.Contains(w, "0000:")
	}
	for _, e := range sl.Events {
		rtc = rtc || strings.HasPrefix(e, "RTC alarm")
	}
	return lid && !rtc
}
