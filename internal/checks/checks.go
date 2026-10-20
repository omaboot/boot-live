// Package checks is the S1 quick pass (LIVE-PLAN §18): one automatic check per criterion a live
// boot can decide without a person, each with a time limit, returning a status, the method and
// the evidence. The pass/partial/fail rule of each check is written next to it, against the
// criterion's wording in doesitomarchy's data/capabilities.yaml. Criteria the pass doesn't
// cover go into the report as not_tested (items.go).
//
// Checks only read: sysfs, procfs, the kernel log, the GPU helper's offscreen frame, and the
// DRM mode getters and vblank counts on the panel's card (no master, no modesets). They move a
// little data over Wi-Fi or Ethernet to prove those carry traffic (the boot.watch test, bound to
// the interface, every byte checked both ways). The one load they make is CPU work for the
// thermal check, watched by the thermal watchdog; the one write, the fans' minimum raised for
// the load's last 10 s and put back on every way out (thermal.go; LIVE-PLAN §6's approved
// exception, owner 2026-10-13). A result is supported only when its function was exercised:
// presence alone is at most not tested (uncertain) (owner, 0.5.0).
package checks

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/doesitomarchy/doesitomarchy/pkg/report"

	"github.com/omaboot/boot-live/internal/boot"
	"github.com/omaboot/boot-live/internal/bootwatch"
	"github.com/omaboot/boot-live/internal/i18n"
	"github.com/omaboot/boot-live/internal/identify"
	"github.com/omaboot/boot-live/internal/pace"
	"github.com/omaboot/boot-live/internal/safety"
	"github.com/omaboot/boot-live/internal/sysroot"
)

// Report statuses, methods and reasons (pkg/report).
const (
	Supported = "supported"
	Partial   = "partial"
	Failed    = "failed"
	NotTested = "not_tested"

	Automatic = "automatic"

	LiveLimit    = report.ReasonLiveLimit
	NotInProfile = "not-in-profile"
	NoEquipment  = "no-equipment"
	Uncertain    = "uncertain"
	Other        = "other"
)

// Result is one check's outcome.
type Result struct {
	ID       string // the criterion, or an extra's ID (audio.codec)
	Extra    bool   // not a criterion: goes in the report's extras
	Status   string // supported, partial, failed, not_tested
	Method   string // automatic, unless not_tested
	Reason   string // not_tested only
	Evidence string // what was measured, for the report (English, like every report field)
	Label    string // the log's name for the check (translated)
	Detail   string // the log's short evidence (translated)
	FixID    string // the OmaBoot? fix it ran with, or ""
	// Ports are a port test's answers for DoesItOmarchy.com's port wiring (the report's "ports",
	// LIVE-PLAN §23): which connector each port it saw is, by its stable sysfs path.
	Ports    []PortAnswer
	Fix      string // that fix's short name for the log, e.g. applesmc
	Effect   boot.Effect
	Params   boot.Params
	Say      string  // Boot's line about it (translated), or ""
	Renderer string  // graphics: the GPU as Mesa names it, short ("HD Graphics 4000"), or ""
	Begin    float64 // when its check started, seconds into the pass
	Finish   float64 // when it finished, seconds into the pass
}

// PortAnswer is one connector a port test matched: the paths the machine saw on it ("usb2:0000:00:14.0/1",
// "pci:0000:03:00.0", "tb:0-1") and how the match came about: ring (a device showed up on the
// port in the ring), asked (the tester said which port, no wiring known), confirmed (the tester
// agreed with the wiring) or corrected (the tester picked another; WiringSaid is the wiring's).
type PortAnswer struct {
	Connector  string
	Paths      []string
	How        string
	WiringSaid string
}

// Pace is the result's status for the screen.
func (r Result) Pace() pace.Status {
	switch r.Status {
	case Supported:
		return pace.Pass
	case Partial:
		return pace.Partial
	case Failed:
		return pace.Fail
	}
	if r.Reason == LiveLimit {
		return pace.Live
	}
	return pace.Skip
}

func pass(evidence string) Result {
	return Result{Status: Supported, Method: Automatic, Evidence: evidence}
}
func part(evidence string) Result {
	return Result{Status: Partial, Method: Automatic, Evidence: evidence}
}
func fail(evidence string) Result {
	return Result{Status: Failed, Method: Automatic, Evidence: evidence}
}
func skip(reason, evidence string) Result {
	return Result{Status: NotTested, Reason: reason, Evidence: evidence}
}

// NetFacts is what the network step found, for the Wi-Fi and Ethernet checks.
type NetFacts struct {
	Scanned  bool // a Wi-Fi scan ran
	Networks int  // networks it found
	ScanErr  string
	Joined   string // the Wi-Fi network joined, or ""
	Online   bool
	Iface    string // the interface the default route goes through
	// Scan runs a Wi-Fi scan when the network step didn't (an offline run): networks found.
	Scan func(ctx context.Context) (int, error)

	// JoinedIface is the interface that joined Joined.
	JoinedIface string
	// Watch runs the boot.watch test over iface, bound to it (network.Watch); Reach asks
	// boot.watch for an echo over the run's own connection. Both nil when there's no test
	// server (a fixture).
	Watch func(ctx context.Context, iface string) bootwatch.Outcome
	Reach func(ctx context.Context) bootwatch.Outcome
	// SiteVia is the interface the catalog refresh from the site worked over, or "": proof an
	// interface carries data when boot.watch doesn't answer over it.
	SiteVia string
	// Bytes and Addr stand in for an interface's byte counters and IPv4 address (tests); nil
	// reads the Mac's (network.Bytes, network.LinkAddr).
	Bytes func(iface string) (uint64, bool)
	Addr  func(iface string) string
}

// Env is what the checks work from.
type Env struct {
	Root     sysroot.Root
	HW       identify.Hardware
	Ident    identify.Result
	Cat      *identify.Catalog
	Fixes    []Fix // OmaBoot? fixes that took effect
	Kmsg     []string
	Net      NetFacts
	Helper   string // the GPU helper (/usr/lib/omaboot/egl-probe)
	Watchdog *safety.Watchdog
	Load     time.Duration             // how long the thermal check loads the CPU
	Settle   time.Duration             // how long graphics.power-down waits for the idle GPU to suspend
	Started  func(c Check, at float64) // told when each check starts (at: seconds into the pass), if set
	Live     func(p boot.Params)       // told the running check's readings as they come (the thermal load's temperature), if set
	// Gate, if set, is called before each check and may hold it back: the screens use it to
	// show the GPU notice before the graphics check really runs. A non-nil error stops the pass.
	Gate func(ctx context.Context, c Check) error
	// Drive, if set, says whether the OmaBoot? drive reads slowly ("" when it can't tell): a
	// check that runs out of time says so in its evidence, so a timeout from slow loading isn't
	// read as the hardware's fault.
	Drive func() string
}

// Fixed returns the fix that took effect with this ID, if any.
func (e *Env) Fixed(id string) *Fix {
	for i := range e.Fixes {
		if e.Fixes[i].ID == id {
			return &e.Fixes[i]
		}
	}
	return nil
}

// LiveKernel is doesitomarchy's live flag (from v0.14.1) for criteria a live boot decides only when
// it runs the kernel an install gives the Mac: the SMC ones (fans, light sensor, keyboard
// backlight). Kept here until boot-live moves to that release's pkg/report.
const LiveKernel = "kernel"

// InstallKernel reports whether kernel (uname -r) is the kernel an Omarchy install gives a Mac:
// linux-t2 on Macs with a T2 chip, linux-omarchy on the others. Same rule as doesitomarchy's
// report.InstallKernel, which the site checks.
func InstallKernel(t2 bool, kernel string) bool {
	kernel = strings.TrimSpace(kernel)
	if t2 {
		return strings.HasSuffix(kernel, "-t2")
	}
	return strings.HasSuffix(kernel, "-omarchy")
}

// installKernel: this live system runs the kernel an install gives this Mac, so its SMC driver
// (applesmc) behaves as installed and the SMC criteria count (OmaBoot? Live boots it by default).
func (e *Env) installKernel() bool { return InstallKernel(e.Ident.Chip == "t2", e.HW.Kernel) }

// lifted reports whether a fix that took effect targets a criterion: it then counts as a live
// result even where live boots normally can't decide it (applesmc on pre-T2 Macs).
func (e *Env) lifted(criterion string) *Fix {
	for i := range e.Fixes {
		if e.Cat == nil {
			continue
		}
		if f := e.Cat.Fix(e.Fixes[i].ID); f != nil {
			for _, c := range f.Criteria {
				if c == criterion {
					return &e.Fixes[i]
				}
			}
		}
	}
	return nil
}

// Check is one entry of the quick pass.
type Check struct {
	ID     string
	Label  string        // msgid, marked with i18n.M
	Limit  time.Duration // the time limit
	Expect time.Duration // how long it usually takes, for "time left"
	Effect boot.Effect
	run    func(ctx context.Context, e *Env) []Result // a check may decide more than one criterion
	// late, if set, decides from what's left when the check runs out of time (e.g. a GPU lockup in
	// the kernel log); criteria it doesn't return stay not_tested (uncertain)
	late func(e *Env) []Result
}

// Plan lists the quick pass for this Mac, in the order it runs.
func Plan(e *Env) []Check {
	var out []Check
	add := func(c Check, ok bool) {
		if ok {
			out = append(out, c)
		}
	}
	gpus := e.HW.GPUs()
	integrated, discrete := false, false
	for _, g := range gpus {
		if identify.Integrated(g) {
			integrated = true
		} else {
			discrete = true
		}
	}
	add(Check{ID: "boot.installer-efi64", Label: i18n.M("Installer boots (64-bit EFI)"), Limit: time.Second, Effect: boot.PCI, run: bootEFI}, e.HW.EFIBits != 32)
	add(Check{ID: "boot.install", Label: i18n.M("Installation completes"), Limit: time.Second, run: bootInstall}, true)
	add(Check{ID: "boot.power-cycle", Label: i18n.M("Survives a power cycle"), Limit: time.Second, run: bootPowerCycle}, true)
	add(Check{ID: "display.native-resolution", Label: i18n.M("Internal display, native resolution"), Limit: 2 * time.Second, Effect: boot.Bars, run: display},
		Applies("display.native-resolution", e))
	gpuLimit := 45 * time.Second
	if !strings.Contains(" "+e.HW.Cmdline+" ", " copytoram=y ") {
		gpuLimit = 90 * time.Second // Mesa loads from the stick: slow over USB 2
	}
	add(Check{ID: "graphics", Label: i18n.M("GPU acceleration"), Limit: gpuLimit, Expect: 15 * time.Second, Effect: boot.Torus, run: graphics, late: gpuLockup},
		integrated || discrete)
	add(Check{ID: "graphics.power-down", Label: i18n.M("Inactive GPU powers down"), Limit: 20 * time.Second, Expect: e.Settle, run: powerDown}, len(gpus) >= 2)
	add(Check{ID: "power.battery-status", Label: i18n.M("Battery status"), Limit: 2 * time.Second, Effect: boot.Battery, run: battery},
		Applies("power.battery-status", e))
	add(Check{ID: "audio.codec", Label: i18n.M("Audio codec found, driver bound"), Limit: 2 * time.Second, Effect: boot.Wave, run: audio}, true)
	add(Check{ID: "network.wifi", Label: i18n.M("Wi-Fi"), Limit: 25 * time.Second, Expect: 2 * time.Second, Effect: boot.Radar, run: wifi}, len(wifiDevices(e)) > 0)
	add(Check{ID: "network.ethernet", Label: i18n.M("Ethernet"), Limit: 2 * time.Second, Expect: time.Second, run: ethernet}, len(ethernetDevices(e)) > 0)
	add(Check{ID: "thermal", Label: i18n.M("Temperature sensors under load"), Limit: e.Load + 15*time.Second, Expect: e.Load, Effect: boot.Heat, run: thermal}, true)
	add(Check{ID: "input.keyboard-backlight", Label: i18n.M("Keyboard backlight"), Limit: 2 * time.Second, run: kbdBacklight}, Applies("input.keyboard-backlight", e))
	add(Check{ID: "input.ambient-light-sensor", Label: i18n.M("Ambient light sensor"), Limit: 2 * time.Second, run: lightSensor}, Applies("input.ambient-light-sensor", e))
	add(Check{ID: "ports.firewire", Label: i18n.M("FireWire"), Limit: 2 * time.Second, run: fireWire}, Applies("ports.firewire", e) && !LiveLimited(e, "ports.firewire"))
	add(Check{ID: "storage.optical", Label: i18n.M("Optical drive"), Limit: 2 * time.Second, run: optical}, Applies("storage.optical", e) && !LiveLimited(e, "storage.optical"))
	return out
}

// Count is how many results a plan gives (graphics gives one per GPU kind, thermal two when the
// Mac has fans).
func Count(e *Env, plan []Check) int {
	n := 0
	for _, c := range plan {
		n += len(itemIDs(e, c))
	}
	return n
}

// itemIDs are the criteria a check decides: graphics one per GPU kind, thermal the sensors and,
// when the Mac has fans, the fans; any other check its own ID.
func itemIDs(e *Env, c Check) []string {
	switch c.ID {
	case "graphics":
		var ids []string
		seen := map[string]bool{}
		for _, g := range e.HW.GPUs() {
			id := "graphics.discrete"
			if identify.Integrated(g) {
				id = "graphics.integrated"
			}
			if !seen[id] {
				seen[id] = true
				ids = append(ids, id)
			}
		}
		return ids
	case "thermal":
		if Applies("thermal.fans", e) {
			return []string{"thermal.sensors", "thermal.fans"}
		}
		return []string{"thermal.sensors"}
	}
	return []string{c.ID}
}

// ItemIDs are the criteria (or extra) a check decides, in order.
func (c Check) ItemIDs(e *Env) []string { return itemIDs(e, c) }

// LabelOf is the log's name (msgid) for a criterion a check decides.
func (c Check) LabelOf(id string) string {
	if l, ok := labels[id]; ok {
		return l
	}
	return c.Label
}

// Labels for criteria a check decides besides its own ID.
var labels = map[string]string{
	"graphics.integrated": i18n.M("Integrated GPU acceleration"),
	"graphics.discrete":   i18n.M("Discrete GPU acceleration"),
	"thermal.sensors":     i18n.M("Temperature sensors under load"),
	"thermal.fans":        i18n.M("Fans respond to load"),
}

// Run runs the plan in order and hands each result to emit as it finishes, with Finish set.
func Run(ctx context.Context, e *Env, plan []Check, emit func(Result)) {
	start := time.Now()
	for _, c := range plan {
		if ctx.Err() != nil {
			return
		}
		if e.Gate != nil {
			if err := e.Gate(ctx, c); err != nil {
				return
			}
		}
		begin := time.Since(start).Seconds()
		if e.Started != nil {
			e.Started(c, begin)
		}
		cctx, cancel := context.WithTimeout(ctx, c.Limit)
		done := make(chan []Result, 1)
		go func() { done <- c.run(cctx, e) }()
		var rs []Result
		select {
		case rs = <-done:
		case <-cctx.Done():
			// a check that doesn't answer in time decides nothing: each of its criteria is
			// not_tested (uncertain), under the criterion's own ID
			for _, id := range itemIDs(e, c) {
				r := skip(Uncertain, "no answer within "+c.Limit.String())
				if e.Drive != nil {
					if d := e.Drive(); d != "" {
						r.Evidence += "; " + d
					}
				}
				if ctx.Err() != nil {
					r = skip(Other, "the run was stopped")
				}
				r.ID = id
				rs = append(rs, r)
			}
			if c.late != nil && ctx.Err() == nil {
				for _, l := range c.late(e) {
					for i := range rs {
						if rs[i].ID == l.ID {
							rs[i] = l
						}
					}
				}
			}
		}
		cancel()
		for _, r := range rs {
			if r.ID == "" {
				r.ID = c.ID
			}
			if r.Label == "" {
				r.Label = c.Label
				if l, ok := labels[r.ID]; ok {
					r.Label = l
				}
			}
			if r.Effect == "" && r.Status != NotTested {
				r.Effect = c.Effect
			}
			if r.Status == NotTested {
				r.Method = ""
				r.Effect = ""
			}
			if r.Detail == "" {
				r.Detail = cut(r.Evidence, 60)
			}
			r.Begin, r.Finish = begin, time.Since(start).Seconds()
			if c.ID == "graphics" && r.Evidence != "" { // the owner: the GPU check felt slow one boot (0.7.5)
				r.Evidence += fmt.Sprintf("; the GPU check took %.1f s", r.Finish-r.Begin)
			}
			emit(r)
		}
	}
}

func cut(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return strings.TrimSpace(string(r[:n-1])) + "…"
}
