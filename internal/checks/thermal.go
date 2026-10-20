package checks

import (
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/omaboot/boot-live/internal/boot"
	"github.com/omaboot/boot-live/internal/i18n"
	"github.com/omaboot/boot-live/internal/safety"
	"github.com/omaboot/boot-live/internal/sysroot"
)

// Samples are the readings taken before and during the thermal load.
type Samples struct {
	CPU      []float64 // the hottest CPU sensor, °C, once a second; [0] is before the load
	Fans     [][]int   // per sample, each fan's rpm
	FanNames []string
	FanTops  []int  // each fan's maximum (applesmc's fanN_max), 0 when unreadable
	Sensors  int    // CPU sensors found
	Stopped  string // the watchdog stopped the load: why
	Load     time.Duration
	Raised   []FanRaise // the fans whose minimum was raised for the last fanRaise of the load
	RaiseErr string     // why the minimums couldn't be raised, or ""
}

// FanRaise is one fan's minimum raised during the load: from its own minimum to To rpm, from
// sample At on.
type FanRaise struct {
	Fan      int // index into the samples' fans
	From, To int // rpm
	At       int // the first sample with the minimum raised
}

// ThermalVerdict decides thermal.sensors, "Temperature sensors report under load": the CPU's
// temperature sensors read plausible values and follow a load.
//
//   - supported: the CPU sensors read plausible temperatures (5–115 °C) and the hottest rose by
//     at least 2 °C under the load (or the watchdog had to stop it);
//   - partial: plausible readings that didn't move under load (a stuck sensor);
//   - failed: no CPU temperature sensor, or only implausible readings.
func ThermalVerdict(s Samples) Result {
	if s.Sensors == 0 || len(s.CPU) == 0 {
		return fail("no CPU temperature sensor (coretemp or k10temp didn't load)")
	}
	from, peak := s.CPU[0], s.CPU[0]
	for _, v := range s.CPU {
		if v < 5 || v > 115 {
			return fail(fmt.Sprintf("implausible CPU temperature: %.1f °C", v))
		}
		peak = math.Max(peak, v)
	}
	ev := fmt.Sprintf("%d CPU sensors; hottest %.0f °C idle, %.0f °C peak over a %s load", s.Sensors, from, peak, s.Load.Round(time.Second))
	if s.Stopped != "" {
		ev += "; the thermal watchdog stopped the load at " + s.Stopped
	}
	if peak-from >= 2 || s.Stopped != "" {
		return pass(ev)
	}
	return part(ev + "; the readings didn't follow the load")
}

// FanVerdict decides thermal.fans, "Fans respond to load", from the same samples. Live boots can
// read fans only with OmaBoot?'s applesmc fix (the caller checks that). A load alone may not be
// enough for the SMC to speed a fan up, so for the load's last fanRaise the check raises each
// fan's minimum (applesmc's fanN_min, the SMC still in charge) and the fan has to follow
// (owner, 2026-10-13; LIVE-PLAN §6's approved exception).
//
//   - supported: a fan whose minimum was raised went at least halfway to it (and 100 rpm or
//     more), or a fan sped up by at least 100 rpm during the load;
//   - not_tested (uncertain): fans readable but steady, raised minimum and all (a fan that didn't
//     follow may be the SMC ignoring the minimum, not a broken fan);
//   - failed: applesmc reports no fans.
func FanVerdict(s Samples) Result {
	if len(s.Fans) == 0 || len(s.Fans[0]) == 0 {
		return fail("applesmc reports no fans")
	}
	first, last := s.Fans[0], s.Fans[len(s.Fans)-1]
	name := func(i int) string {
		if i < len(s.FanNames) && s.FanNames[i] != "" {
			return s.FanNames[i]
		}
		return fmt.Sprintf("fan%d", i+1)
	}
	rise := 0 // before any minimum was raised: the load's own doing
	until := len(s.Fans)
	if len(s.Raised) > 0 {
		until = min(until, s.Raised[0].At)
	}
	var parts []string
	for i := range first {
		hi := first[i]
		for _, f := range s.Fans[:max(1, until)] {
			if i < len(f) && f[i] > hi {
				hi = f[i]
			}
		}
		rise = max(rise, hi-first[i])
		parts = append(parts, fmt.Sprintf("%s %d to %d rpm under load (end %d)", name(i), first[i], hi, last[min(i, len(last)-1)]))
	}
	ev := "applesmc: " + strings.Join(parts, ", ")
	followed := false
	for _, r := range s.Raised {
		if r.At >= len(s.Fans) || r.Fan >= len(s.Fans[r.At]) {
			continue
		}
		base, hi := s.Fans[max(0, r.At-1)][r.Fan], 0
		for _, f := range s.Fans[r.At:] {
			if r.Fan < len(f) {
				hi = max(hi, f[r.Fan])
			}
		}
		ok := hi-base >= max(100, (r.To-base)/2)
		followed = followed || ok
		verb := "followed"
		if !ok {
			verb = "didn't follow"
		}
		ev += fmt.Sprintf("; %s's minimum raised from %d to %d rpm for the load's last %s: %s (%d to %d rpm), put back to %d after",
			name(r.Fan), r.From, r.To, fanRaise.Round(time.Second), verb, base, hi, r.From)
	}
	if s.RaiseErr != "" {
		ev += "; the fans' minimum couldn't be raised: " + s.RaiseErr
	}
	if followed || rise >= 100 {
		return pass(ev)
	}
	// a fan flat out from the start has nowhere to go (owner's Macmini4,1, 0.8.5 dev: 5511 rpm
	// throughout, its drive swapped for an SSD; the SMC runs the fans flat out when a temperature
	// sensor it expects is missing)
	for i := range first {
		if i < len(s.FanTops) && s.FanTops[i] > 0 && first[i] >= s.FanTops[i]-100 {
			ev += fmt.Sprintf("; %s was already at its maximum (%d rpm) before the load, so it couldn't speed up (one cause: a temperature sensor the SMC expects is missing, as after some drive swaps)", name(i), s.FanTops[i])
		}
	}
	return skip(Uncertain, ev+"; no fan sped up")
}

// ── Raising the fans' minimum (LIVE-PLAN §6's approved exception, owner 2026-10-13) ──────────
//
// The only write the quick pass makes: each fan's minimum (applesmc's fanN_min, which the driver
// writes to the SMC's F<n>Mn key), raised for fanRaise at the end of the thermal load, never
// above the fan's maximum, never the manual mode: the SMC keeps control and can still run the
// fan faster when it's hot, and the thermal watchdog watches the load as always (a hot chip
// stops the load, and the minimum goes back with it). The original minimum goes back on every
// way out: when the raise is over, when the load stops (the watchdog, the pass cancelled), when
// the check panics (deferred), and when the tool exits mid-raise (RestoreFans, from app.Run's
// Close, which main defers). Only a hard power-off mid-raise skips it (an SMC reset then puts
// the minimum back).

// fanRaise is how long the fans' minimum stays raised, at the end of the load.
var fanRaise = 10 * time.Second

// errFixture: a fixture tree is never written to.
var errFixture = errors.New("a fixture tree: nothing is written")

// fanWrite writes one applesmc file (a variable, so the tests write into a fixture tree).
var fanWrite = func(r sysroot.Root, p, v string) error {
	if !r.IsSystem() {
		return errFixture
	}
	return os.WriteFile(r.P(p), []byte(v), 0)
}

// fanUndo are the minimums raised and not put back yet; once closed (the tool is exiting), no
// minimum is raised again.
var fanUndo struct {
	mu     sync.Mutex
	root   sysroot.Root
	orig   map[string]string // fanN_min path → its value before
	closed bool
}

// fanTarget is the minimum to raise a fan to: halfway from its minimum to its maximum, at least
// 800 rpm over its speed now, never over its maximum less 100; ok false when that leaves under
// 300 rpm to see it climb (already near its top).
func fanTarget(min0, max0, now int) (int, bool) {
	t := max(min0+(max0-min0)/2, now+800)
	t = min(t, max0-100) / 100 * 100
	return t, t-now >= 300 && t > min0
}

// raiseFans raises each fan's minimum (its speeds now in rpm) and notes how to put it back.
func raiseFans(e *Env, dir string, now []int, at int) ([]FanRaise, error) {
	fanUndo.mu.Lock()
	defer fanUndo.mu.Unlock()
	if fanUndo.closed {
		return nil, errors.New("the tool is exiting")
	}
	var out []FanRaise
	var first error
	for i, rpm := range now {
		p := fmt.Sprintf("%s/fan%d_min", dir, i+1)
		orig := e.Root.Read(p)
		min0, ok1 := e.Root.Int(p)
		max0, ok2 := e.Root.Int(fmt.Sprintf("%s/fan%d_max", dir, i+1))
		to, ok := fanTarget(min0, max0, rpm)
		if !ok1 || !ok2 || !ok {
			continue
		}
		if fanUndo.orig == nil {
			fanUndo.orig = map[string]string{}
		}
		if _, held := fanUndo.orig[p]; !held { // noted before the write: whatever happens, it goes back
			fanUndo.orig[p], fanUndo.root = orig, e.Root
		}
		if err := fanWrite(e.Root, p, fmt.Sprint(to)); err != nil {
			delete(fanUndo.orig, p)
			if first == nil {
				first = err
			}
			continue
		}
		out = append(out, FanRaise{Fan: i, From: min0, To: to, At: at})
	}
	return out, first
}

// restoreFans puts every raised minimum back.
func restoreFans() {
	fanUndo.mu.Lock()
	defer fanUndo.mu.Unlock()
	for p, v := range fanUndo.orig {
		fanWrite(fanUndo.root, p, v)
		delete(fanUndo.orig, p)
	}
}

// RestoreFans puts back any fan minimum the thermal check still has raised, and stops it raising
// one again: the tool calls it on its way out (app.Run's Close), whichever way it ends.
func RestoreFans() {
	fanUndo.mu.Lock()
	fanUndo.closed = true
	fanUndo.mu.Unlock()
	restoreFans()
}

// smcDir is applesmc's sysfs directory (the light sensor; the fans too with the older driver,
// fanDir): an ACPI device on recent kernels, a platform device on older ones.
func smcDir(e *Env) string {
	for _, pat := range []string{"/sys/bus/acpi/devices/APP0001:*", "/sys/devices/platform/applesmc.*"} {
		for _, d := range e.Root.Glob(pat) {
			if e.Root.Exists(d+"/fan1_input") || e.Root.Exists(d+"/light") {
				return d
			}
		}
	}
	return ""
}

// fanDir is where applesmc's fan files are (fanN_input, _min, _max, _label): its own directory
// with the older driver, its hwmon device under it with the hwmon one (linux-omarchy 7.2.8, Omarchy
// 4.0.5: omarchy-pkgs 0740-hwmon-updates.patch), where fanN_manual and _output became pwmN_enable
// and fanN_target. "" when no fans are found either way.
func fanDir(e *Env) string {
	for _, pat := range []string{"/sys/bus/acpi/devices/APP0001:*", "/sys/devices/platform/applesmc.*"} {
		for _, d := range e.Root.Glob(pat) {
			if e.Root.Exists(d + "/fan1_input") {
				return d
			}
			for _, h := range e.Root.Glob(d + "/hwmon/hwmon*") {
				if e.Root.Exists(h + "/fan1_input") {
					return h
				}
			}
		}
	}
	return ""
}

func readFans(e *Env, dir string) ([]int, []string) {
	var rpm []int
	var names []string
	for i := 1; i <= 8 && dir != ""; i++ {
		v, ok := e.Root.Int(fmt.Sprintf("%s/fan%d_input", dir, i))
		if !ok {
			break
		}
		rpm = append(rpm, v)
		names = append(names, e.Root.Read(fmt.Sprintf("%s/fan%d_label", dir, i)))
	}
	return rpm, names
}

func cpuSensors(e *Env) []safety.Sensor {
	var out []safety.Sensor
	for _, s := range safety.Sensors(e.Root) {
		if s.Chip == "coretemp" || s.Chip == "k10temp" {
			out = append(out, s)
		}
	}
	return out
}

func hottest(e *Env, ss []safety.Sensor) (float64, bool) {
	best, ok := 0.0, false
	for _, s := range ss {
		if v, good := s.Read(e.Root); good && (!ok || v > best) {
			best, ok = v, true
		}
	}
	return best, ok
}

// thermal loads every CPU thread for Env.Load while sampling the CPU sensors (and the fans) once a
// second; the thermal watchdog stops the load at once near a critical temperature. It decides
// thermal.sensors and, where the Mac has fans, thermal.fans.
func thermal(ctx context.Context, e *Env) []Result {
	ss := cpuSensors(e)
	dir := fanDir(e)
	s := Samples{Sensors: len(ss)}
	sample := func() {
		if v, ok := hottest(e, ss); ok {
			s.CPU = append(s.CPU, v)
			if e.Live != nil { // the screen shows the temperature as it rises, not only at the end
				e.Live(boot.Params{From: s.CPU[0], Now: v})
			}
		}
		if f, names := readFans(e, dir); len(f) > 0 {
			s.Fans, s.FanNames = append(s.Fans, f), names
		}
	}
	sample()
	for i := range s.FanNames {
		top, _ := e.Root.Int(fmt.Sprintf("%s/fan%d_max", dir, i+1))
		s.FanTops = append(s.FanTops, top)
	}
	// the fans' minimum is raised for the load's last fanRaise where the fans count live
	raise := dir != "" && len(s.Fans) > 0 && Applies("thermal.fans", e) && (e.lifted("thermal.fans") != nil || e.installKernel())
	defer restoreFans() // a panic, or any way out of here, puts the minimums back
	start := time.Now()
	if len(ss) > 0 && e.Load > 0 {
		var stop atomic.Bool
		for i := 0; i < runtime.NumCPU(); i++ {
			go func() {
				x := 1.0001
				for !stop.Load() {
					for j := 0; j < 100000; j++ {
						x = x*1.0000001 + 1e-9
					}
				}
				_ = x
			}()
		}
		tick := time.NewTicker(time.Second)
	loop:
		for time.Since(start) < e.Load {
			select {
			case <-ctx.Done():
				break loop
			case <-tick.C:
				sample()
				if raise && time.Since(start) >= e.Load-fanRaise && !e.Watchdog.Hot() {
					raise = false
					var err error
					s.Raised, err = raiseFans(e, dir, s.Fans[len(s.Fans)-1], len(s.Fans))
					if err != nil && !errors.Is(err, errFixture) {
						s.RaiseErr = err.Error()
					}
				}
				if e.Watchdog.Hot() {
					if st := e.Watchdog.Stops(); len(st) > 0 {
						s.Stopped = st[len(st)-1]
					}
					break loop
				}
			}
		}
		tick.Stop()
		stop.Store(true)
	}
	restoreFans() // the raise is over with the load
	s.Load = time.Since(start)
	sens := ThermalVerdict(s)
	sens.ID = "thermal.sensors"
	if len(s.CPU) > 0 {
		from, peak := s.CPU[0], s.CPU[0]
		for _, v := range s.CPU {
			peak = math.Max(peak, v)
		}
		chip := ss[0].Chip
		sens.Detail = i18n.T("%s, %d sensors, peak %.0f °C", chip, len(ss), peak)
		sens.Params = boot.Params{From: from, Peak: peak}
		if sens.Status == Supported {
			sens.Say = i18n.T("warming the CPU. %.0f °C peak. watchdog is calm.", peak)
			if s.Stopped != "" {
				sens.Say = i18n.T("too warm: the watchdog stopped the load at %.0f °C.", peak)
			}
		}
	}
	out := []Result{sens}
	if !Applies("thermal.fans", e) {
		return out
	}
	var fans Result
	f := e.lifted("thermal.fans")
	switch {
	case f != nil || e.installKernel():
		fans = FanVerdict(s)
		if f != nil && fans.Status != NotTested {
			fans.FixID, fans.Fix = f.ID, f.Short
		}
	default:
		ev := "fan speeds need the installed system's kernel and SMC driver"
		if len(s.Fans) > 0 {
			ev += fmt.Sprintf(" (read here: %d fans, %v rpm)", len(s.Fans[0]), s.Fans[0])
		}
		fans = skip(LiveLimit, ev)
		fans.Detail = i18n.T("needs the installed system's SMC driver")
	}
	fans.ID = "thermal.fans"
	if fans.Status != NotTested && len(s.Fans) > 0 {
		fans.Detail = i18n.N("applesmc, %d fan, %d to %d rpm", "applesmc, %d fans, %d to %d rpm", len(s.Fans[0]), len(s.Fans[0]), s.Fans[0][0], maxFan(s, 0))
	}
	if fans.Status == Supported {
		fans.Say = i18n.T("the fans answer the load.")
		if len(s.Raised) > 0 {
			fans.Say = i18n.T("asked the fans for more, and they spun up.")
		}
		if f != nil {
			fans.Say = i18n.T("the applesmc fix is on: the fans answer.")
		}
	}
	return append(out, fans)
}

func maxFan(s Samples, i int) int {
	hi := 0
	for _, f := range s.Fans {
		if i < len(f) && f[i] > hi {
			hi = f[i]
		}
	}
	return hi
}

// kbdBacklight looks at input.keyboard-backlight, "Keyboard backlight". Live boots decide it on
// T2 Macs (live: t2), and on others only with OmaBoot?'s applesmc fix, which is what registers
// the backlight there. The check reads only, and a registered LED is presence, not a working
// light (criteria review, 2026-10-13: an LED whose keys never light passed here): the hands-on
// test (internal/handson, backlight.go) blinks the light with a person counting the blinks, and
// its result replaces this one.
//
//   - not_tested (uncertain): the backlight's LED device is registered with a brightness range
//     (> 0): present, the hands-on test decides;
//   - failed: no keyboard backlight LED registered;
//   - not_tested (live-limit): a pre-T2 Mac without the applesmc fix.
func kbdBacklight(_ context.Context, e *Env) []Result {
	f := e.lifted("input.keyboard-backlight")
	if e.Ident.Chip != "t2" && f == nil && !e.installKernel() {
		r := skip(LiveLimit, "the keyboard backlight needs the installed system's kernel and SMC driver (running "+e.HW.Kernel+")")
		r.Detail = i18n.T("needs the installed system's SMC driver")
		return []Result{r}
	}
	for _, d := range e.Root.Glob("/sys/class/leds/*kbd_backlight*") {
		mx, ok := e.Root.Int(d + "/max_brightness")
		if !ok || mx <= 0 {
			continue
		}
		name := d[strings.LastIndex(d, "/")+1:]
		r := skip(Uncertain, fmt.Sprintf("%s registered, brightness %s of %d: present; the hands-on test decides (the quick pass doesn't change the light)", name, e.Root.Read(d+"/brightness"), mx))
		r.Detail = i18n.T("%s present; the hands-on test decides", name) // the hands-on result names the fix
		return []Result{r}
	}
	r := fail("no keyboard backlight LED registered (/sys/class/leds/*kbd_backlight*)")
	r.Detail = i18n.T("no backlight LED")
	return []Result{r}
}

// lightSensor looks at input.ambient-light-sensor, "Ambient light sensor". Live boots can decide
// it only with OmaBoot?'s applesmc fix or the install's kernel (live: kernel). A reading alone
// proves nothing (criteria review, 2026-10-13: a value stuck at 0 or 255 passed here): the
// hands-on test (internal/handson, lightsensor.go) has the tester cover the sensor and watches
// the reading follow, and its result replaces this one.
//
//   - not_tested (uncertain): applesmc's light sensor gives a reading: a reading, not yet a
//     cover test;
//   - failed: applesmc is there but has no light reading;
//   - not_tested (live-limit): no applesmc fix on this Mac.
func lightSensor(_ context.Context, e *Env) []Result {
	f := e.lifted("input.ambient-light-sensor")
	dir := smcDir(e)
	light := ""
	if dir != "" {
		light = e.Root.Read(dir + "/light")
	}
	if f == nil && !e.installKernel() {
		ev := "the light sensor needs the installed system's kernel and SMC driver (running " + e.HW.Kernel + ")"
		if light != "" {
			ev += " (read here: " + light + ")"
		}
		r := skip(LiveLimit, ev)
		r.Detail = i18n.T("needs the installed system's SMC driver")
		return []Result{r}
	}
	var r Result
	if light == "" {
		r = fail("applesmc has no light reading")
		r.Detail = i18n.T("no light reading")
	} else {
		// applesmc gives "(left,right)"; one-sensor Macs read the left
		lvl := strings.TrimPrefix(strings.SplitN(light, ",", 2)[0], "(")
		r = skip(Uncertain, fmt.Sprintf("the light sensor reads %s (applesmc light %s): a reading, not yet a cover test", lvl, light))
		r.Detail = i18n.T("reads %s; the hands-on test decides", lvl)
	}
	if f != nil && r.Status != NotTested { // the hands-on result names the fix once it decides
		r.FixID, r.Fix = f.ID, f.Short
	}
	return []Result{r}
}
