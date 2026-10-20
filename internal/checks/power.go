package checks

import (
	"context"
	"fmt"
	"math"
	"path"
	"regexp"
	"strconv"
	"strings"

	"github.com/omaboot/boot-live/internal/boot"
	"github.com/omaboot/boot-live/internal/i18n"
)

// ChargeLevel is the charge of the battery in dir, in percent of what it holds when full.
func ChargeLevel(intOf func(string) (int, bool), dir string) (int, bool) {
	level, ok, _, _, _ := charge(intOf, dir)
	return int(math.Round(level)), ok
}

// Health is how much the battery in dir holds when full, in percent of what it held new
// (full/design, as energy or charge), and its charge cycles (0: not reported).
func Health(intOf func(string) (int, bool), dir string) (health int, cycles int, ok bool) {
	_, _, _, full, unit := charge(intOf, dir)
	cycles, _ = intOf(dir + "/cycle_count")
	if unit == "" {
		return 0, max(0, cycles), false
	}
	d, has := intOf(dir + "/" + unit + "_full_design")
	if !has || d <= 0 {
		return 0, max(0, cycles), false
	}
	return int(math.Round(100 * float64(full) / float64(d))), max(0, cycles), true
}

// charge is the battery's charge level: now/full when it gives both, as energy or charge (on
// some Macs the kernel's capacity is now/design, so a full, worn battery read "75 %": the Air's
// 0.7.9 run, Full, 99 % of full, 76 % of design), with the pair it came from; capacity is the
// fallback.
func charge(intOf func(string) (int, bool), dir string) (level float64, ok bool, now, full int, unit string) {
	for _, k := range []string{"energy", "charge"} {
		n, a := intOf(dir + "/" + k + "_now")
		f, b := intOf(dir + "/" + k + "_full")
		if a && b {
			now, full, unit = n, f, k
			if f > 0 && float64(n) <= float64(f)*1.1 {
				return min(100, 100*float64(n)/float64(f)), true, now, full, unit
			}
			break
		}
	}
	if c, has := intOf(dir + "/capacity"); has && c >= 0 && c <= 100 {
		return float64(c), true, now, full, unit
	}
	return -1, false, now, full, unit
}

// battery decides power.battery-status, "Battery status and charge reporting": the battery's
// power-supply device reports whether it's charging and how full it is.
//
//   - supported: a Battery power supply is present, its status is one of the kernel's states
//     and its charge level is readable and sane (capacity 0–100 %, or a now/full pair with
//     full > 0 and now ≤ full + 10 %);
//   - partial: present, but the status or the charge level is missing or out of range;
//   - failed: no battery power supply on a Mac that has one (the driver didn't bind).
func battery(_ context.Context, e *Env) []Result {
	dir := systemBattery(e)
	if dir == "" {
		r := fail("no battery power supply (/sys/class/power_supply has no Battery)")
		r.Detail = i18n.T("no battery found")
		return []Result{r}
	}
	name := supplyName(dir)
	if p := e.Root.Read(dir + "/present"); p == "0" {
		r := fail(name + ": present = 0 (no battery seen)")
		r.Detail = i18n.T("%s: no battery seen", name)
		return []Result{r}
	}
	status := e.Root.Read(dir + "/status")
	okStatus := map[string]bool{"Charging": true, "Discharging": true, "Full": true, "Not charging": true}[status]
	// the kernel's capacity is named in the evidence when it disagrees with the level (charge)
	level, ok, now, full, unit := charge(e.Root.Int, dir)
	capacity, hasCap := e.Root.Int(dir + "/capacity")
	hasCap = hasCap && capacity >= 0 && capacity <= 100
	cycles, hasCycles := e.Root.Int(dir + "/cycle_count")
	ev := fmt.Sprintf("%s: status %q", name, status)
	if level >= 0 {
		ev += fmt.Sprintf(", %.0f %%", level)
	}
	if unit != "" {
		ev += fmt.Sprintf(", %s now/full %d/%d", unit, now, full)
		if d, has := e.Root.Int(dir + "/" + unit + "_full_design"); has && d > 0 {
			ev += fmt.Sprintf(" (%.0f %% of design)", 100*float64(full)/float64(d))
		}
	}
	if hasCycles && cycles > 0 {
		ev += fmt.Sprintf(", %d cycles", cycles)
	}
	if hasCap && unit != "" && math.Abs(float64(capacity)-level) > 5 {
		ev += fmt.Sprintf("; the kernel's capacity reads %d %%", capacity)
	}
	var r Result
	switch {
	case ok && okStatus:
		r = pass(ev)
	case ok:
		r = part(ev + "; the status isn't one of the kernel's states")
	default:
		r = part(ev + "; no readable charge level")
	}
	if hasCycles && cycles > 0 {
		r.Detail = i18n.T("%s %.0f%%, %d cycles", name, level, cycles)
		r.Say = i18n.T("battery reports in. %d cycles and counting.", cycles)
	} else {
		r.Detail = i18n.T("%s %.0f%%, %s", name, level, strings.ToLower(status))
		r.Say = i18n.T("battery reports in: %.0f%%.", level)
	}
	r.Params = boot.Params{Level: max(0, level) / 100}
	return []Result{r}
}

// systemSupplies are the Mac's own power supplies (its battery, its adapter): every
// /sys/class/power_supply entry but a peripheral's (scope "Device": a wireless or USB keyboard's,
// mouse's or trackpad's battery, whose name carries its serial number, e.g.
// hid-F0T036401FNJKNCAQ-battery-144), and never one named hid-*.
func systemSupplies(e *Env) []string {
	var out []string
	for _, d := range e.Root.Glob("/sys/class/power_supply/*") {
		if strings.EqualFold(e.Root.Read(d+"/scope"), "Device") || strings.HasPrefix(path.Base(d), "hid-") {
			continue
		}
		out = append(out, d)
	}
	return out
}

// systemBattery is the Mac's own battery's power-supply directory, or "".
func systemBattery(e *Env) string {
	for _, d := range systemSupplies(e) {
		if e.Root.Read(d+"/type") == "Battery" {
			return d
		}
	}
	return ""
}

// supplyNameRe is a system power supply's name as the kernel's ACPI and SMC drivers make them:
// BAT0, BAT1, ADP1, ACAD, AC.
var supplyNameRe = regexp.MustCompile(`^[A-Za-z]{2,4}[0-9]{0,2}$`)

// supplyName is a power supply's name for evidence and the screen: its sysfs name when it looks
// like a system supply's, else just "battery" (a name that might carry a serial number never
// goes into a report).
func supplyName(dir string) string {
	if n := path.Base(dir); supplyNameRe.MatchString(n) {
		return n
	}
	return "battery"
}

var (
	codecRe = regexp.MustCompile(`(?m)^Codec:\s*(.+)$`)
	ssidRe  = regexp.MustCompile(`(?m)^Subsystem Id:\s*(0x[0-9a-fA-F]+)`)
)

// audio is an extra, not a criterion (S1): the HD Audio controller has its driver bound and a
// codec answered. Playback and the microphone are hands-on tests for S2.
//
//   - supported: an analog (non-HDMI) codec found and the controller's driver bound;
//   - failed: an HD Audio controller with no driver, or no codec answered.
func audio(_ context.Context, e *Env) []Result {
	var codecs, hdmi, ssids []string
	for _, f := range e.Root.Glob("/proc/asound/card*/codec#*") {
		t := e.Root.Read(f)
		m := codecRe.FindStringSubmatch(t)
		if m == nil {
			continue
		}
		c := strings.TrimSpace(m[1])
		if strings.Contains(c, "HDMI") || strings.Contains(c, "DisplayPort") {
			hdmi = append(hdmi, c)
			continue
		}
		codecs = append(codecs, c)
		// the subsystem ID is what Apple-specific codec drivers match on (the iMac18,1's CS8409,
		// 0.8.5 dev: the stock driver has no Apple fixup, the out-of-tree ones check this ID)
		if id := ssidRe.FindStringSubmatch(t); id != nil {
			ssids = append(ssids, c+" "+strings.ToLower(id[1]))
		}
	}
	ctrls := e.HW.PCIByClass("0403")
	var drivers []string
	unbound := false
	for _, c := range ctrls {
		if c.Driver == "" {
			unbound = true
		} else if !contains(drivers, c.Driver) {
			drivers = append(drivers, c.Driver)
		}
	}
	ev := fmt.Sprintf("codecs: %s; HDMI/DP: %s; controllers: %d, drivers: %s", orNone(strings.Join(codecs, ", ")),
		orNone(strings.Join(hdmi, ", ")), len(ctrls), orNone(strings.Join(drivers, ", ")))
	if len(ssids) > 0 {
		ev += "; subsystem IDs: " + strings.Join(ssids, ", ")
	}
	var r Result
	switch {
	case len(ctrls) == 0 && len(codecs) == 0:
		r = skip(Other, "no HD Audio controller (T2 Macs play audio through the T2)")
		r.Detail = i18n.T("no HD Audio controller")
	case len(codecs) == 0 || unbound && len(drivers) == 0:
		r = fail(ev)
		r.Detail = i18n.T("no codec answered")
	default:
		r = pass(ev)
		short := shortCodec(codecs[0])
		r.Detail = i18n.T("%s, %s bound", short, strings.Join(drivers, ", "))
		r.Say = i18n.T("%s on %s. listening tests come later.", short, strings.Join(drivers, ", "))
	}
	r.ID, r.Extra = "audio.codec", true
	return []Result{r}
}

// shortCodec: "Cirrus Logic CS4206" → "Cirrus CS4206".
func shortCodec(s string) string {
	return strings.NewReplacer("Cirrus Logic", "Cirrus", "Realtek ", "", "Analog Devices", "ADI", "IDT ", "IDT ", "SigmaTel", "SigmaTel").Replace(s)
}

func contains(s []string, x string) bool {
	for _, v := range s {
		if v == x {
			return true
		}
	}
	return false
}

func atoi(s string) int {
	n, _ := strconv.Atoi(strings.TrimSpace(s))
	return n
}
