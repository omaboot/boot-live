package ui

import (
	"strings"
	"time"

	"github.com/omaboot/boot-live/internal/boot"
	"github.com/omaboot/boot-live/internal/i18n"
	"github.com/omaboot/boot-live/internal/identify"
	"github.com/omaboot/boot-live/internal/network"
	"github.com/omaboot/boot-live/internal/pace"
	"github.com/omaboot/boot-live/internal/plan"
	"github.com/omaboot/boot-live/internal/safety"
	"github.com/omaboot/boot-live/internal/sounds"
)

// Check is one quick-pass result as the screens show it.
type Check struct {
	ID     string
	Label  string // translated with S where drawn
	Detail string // the log's evidence, translated with S where drawn
	Fix    string // the OmaBoot? fix it ran with (short name), or ""
	Effect boot.Effect
	Params boot.Params
	Begin  float64 // when its check really started, seconds into the pass
	Finish float64 // when it really finished, seconds into the pass
	Status pace.Status
	Say    string // Boot's line, translated with S where drawn
	// For the console's why: the report's evidence (English), how it was measured, and why it
	// wasn't (not tested).
	Evidence, Method, Reason string
}

// Group is one criteria category on Quick results: each criterion's result for this Mac
// (Skip = not in this run).
type Group struct {
	Name  string // translated with S where drawn
	Items []pace.Status
	IDs   []string // each item's criterion, where known (the console's force goes by it)
}

// WifiNet is one Wi-Fi network in range.
type WifiNet struct {
	SSID     string
	Bars     int
	Security string // translated with S where drawn
}

// Choice is one entry of a pick list.
type Choice struct {
	ID    string // what picking it means, e.g. a configuration ID ("" = none of these)
	Label string
	Note  string
}

// Facts are what the screens show about a run. The demo fills them from its script; the real
// mode from the probes at the start and from the checks as they finish.
type Facts struct {
	Demo bool

	Mac     MacInfo // the header's Mac
	Known   bool    // the Mac is identified (or picked)
	Parts   string  // the line under the Mac on Which Mac
	Omarchy string  // the Omarchy version the image carries

	// Disk is the first internal disk, drawn next to Hello's wordmark (prototype v54), MoreDisks
	// how many others there are; nil when there are none. DryRun: a fixture run, nothing was set
	// read-only. Written, when set, is how many bytes have gone to the internal disks since they
	// were set read-only (-1: sysfs doesn't say).
	Disk      *safety.Layout
	MoreDisks int
	DryRun    bool
	Written   func() int64

	DisksLine    string // Hello's setup lines
	SystemLine   string // Omarchy, kernel, Mesa, boot path
	ImageLine    string // the image and the fixes it applied
	SnapshotLine string
	Fatal        string // the run can't go on: why (Hello shows it and offers only quitting)

	MatchNote  string   // under the Mac on Which Mac
	Candidates []Choice // several configurations fit: Which Mac's pick list
	Configs    []Choice // every configuration of this Mac, for "Pick from the list"
	Unknown    bool     // the catalog doesn't have this Mac
	// View is what Which Mac draws the match from (the search, the line-up, the timeline, the
	// table), or nil (the screen then shows the Mac's name and parts only).
	View *identify.View
	// Resume is an unfinished run's copy to pick up from (Which Mac offers it), or nil.
	Resume *Resume
	// OtherCopy is one from another boot-live version (Which Mac names it), or nil.
	OtherCopy *OtherCopy
	// Sounds are the sound settings the last boot kept on the stick (off, the defaults: none).
	Sounds sounds.Prefs
	// Plan is the run plan found on the stick (runplan.go), nil for none.
	Plan *PlanFacts

	Site      string // where reports go, as shown: "DoesItOmarchy.com" (or the staging host)
	CanSubmit bool
	// CanSendBug: the image has a bug report upload key (PF-13); BugsWaiting is how many bug
	// reports on the stick wait to be sent from an online run.
	CanSendBug  bool
	BugsWaiting int
	NoSubmit    string // why Submit is greyed out (no key in the image)

	Static StaticTarget // where "Set static IP address" puts the address (asked when the form opens)

	Checks   []Check // the quick pass's results
	HandsOn  []Check // the hands-on tests' results so far (they replace a quick result for the same criterion)
	Groups   []Group
	FixNames []string // the fixes that took effect, short names
	Startup  []string // how long the Mac took to start and what took longest (English, from systemd)

	TestedOn  string // what the results stand for: Omarchy, kernel, Mesa with the GPU it named
	Image     string // the image, for the report's line
	StartedIn string // how long the Mac took to start, or ""
	// BootLine is the boot timing, short, for Quick results; SlowDrive the warning when the
	// OmaBoot? drive reads slowly; either "" (0.7.5)
	BootLine, SlowDrive string

	Headline  string // Quick results' headline
	AnswerSay string // Boot's line on Quick results
	DoneWord  string // the big word on Finish ("Yay!"; "Thanks!" replaces it once the report is sent)

	Report     string // the report's code once sent
	ReportPage string // its page as shown, e.g. DoesItOmarchy.com/report/7c41e9a2d0
	SavedFile  string // where it was saved on the stick
	ReportKB   int

	FinishedIn, QuickPassIn string
	TimeScale               float64 // the demo's compressed quick pass counts "time left" this much longer

	// Forced are results set by hand in the console (dev: force), by criterion: they show in place
	// of what was measured, and mark the run.
	Forced map[string]pace.Status
	// Production: reports go to DoesItOmarchy.com itself, which won't take a run whose results
	// were changed in the console (staging and the stick still do).
	Production bool
}

// All are the run's results: the quick pass's, then the hands-on tests', a hands-on result
// replacing the quick pass's for the same criterion (the keyboard backlight).
func (f *Facts) All() []Check {
	if len(f.Forced) > 0 {
		return f.forced(f.all())
	}
	return f.all()
}

// forced puts the results set by hand in the console in place.
func (f *Facts) forced(cs []Check) []Check {
	out := append([]Check(nil), cs...)
	for i, c := range out {
		if s, ok := f.Forced[c.ID]; ok {
			out[i].Status = s
		}
	}
	return out
}

func (f *Facts) all() []Check {
	if len(f.HandsOn) == 0 {
		return f.Checks
	}
	by := map[string]bool{}
	for _, c := range f.HandsOn {
		by[c.ID] = true
	}
	var out []Check
	for _, c := range f.Checks {
		if !by[c.ID] {
			out = append(out, c)
		}
	}
	return append(out, f.HandsOn...)
}

// Criteria are the groups with the results set by hand in the console in place.
func (f *Facts) Criteria() []Group {
	if len(f.Forced) == 0 {
		return f.Groups
	}
	out := make([]Group, len(f.Groups))
	for i, grp := range f.Groups {
		items := append([]pace.Status(nil), grp.Items...)
		for j, id := range grp.IDs {
			if s, ok := f.Forced[id]; ok && j < len(items) {
				items[j] = s
			}
		}
		out[i] = Group{Name: grp.Name, Items: items, IDs: grp.IDs}
	}
	return out
}

// Tally gives how many criteria ended with s, as the groups' rows show them: the headline's
// numbers add up to the rows (the Air's 0.7.12 run: "24 passed" over rows of 23, the audio codec
// check, not a criterion, counted too). Before there are groups, every result counts.
func (f *Facts) Tally(s pace.Status) int {
	if len(f.Groups) == 0 {
		return f.Count(s)
	}
	n := 0
	for _, grp := range f.Criteria() {
		for _, it := range grp.Items {
			if it == s {
				n++
			}
		}
	}
	return n
}

// Count gives how many of the run's results ended with s.
func (f *Facts) Count(s pace.Status) int {
	n := 0
	for _, c := range f.All() {
		if c.Status == s {
			n++
		}
	}
	return n
}

// Fixed is how many results ran with an OmaBoot? fix, quick pass and hands-on, and the fixes'
// short names in order.
func (f *Facts) Fixed() (int, []string) {
	n, seen := 0, map[string]bool{}
	var names []string
	for _, c := range append(append([]Check{}, f.Checks...), f.HandsOn...) {
		if c.Fix == "" {
			continue
		}
		n++
		if !seen[c.Fix] {
			seen[c.Fix], names = true, append(names, c.Fix)
		}
	}
	return n, names
}

// Check returns the check with this ID, or an empty one.
func (f *Facts) Check(id string) Check {
	for _, c := range f.Checks {
		if c.ID == id {
			return c
		}
	}
	return Check{ID: id}
}

// NewPlayer makes the pace player for a pass of total results and adds the ones given.
func NewPlayer(cs []Check, total int) *pace.Player {
	p := pace.New(pace.Default, total, i18n.T("here we go."), i18n.T("quick pass done. let's see."))
	for _, c := range cs {
		p.Add(Item(c))
	}
	return p
}

// Item is a check as the pace player takes it: Boot's cue opens it; he comments on the result
// only when it matters (partial, failed, or passed with an OmaBoot? fix), so passes keep the pace.
func Item(c Check) pace.Item {
	return pace.Item{ID: c.ID, Begin: c.Begin, Finish: c.Finish, Status: c.Status, Effect: c.Effect != boot.Plasma,
		Cue: Cue(c.ID), Say: comment(c)}
}

// comment is Boot's line on a result worth one, or "".
func comment(c Check) string {
	say := i18n.S(c.Say)
	switch {
	case c.Status == pace.Partial && say == "":
		return i18n.T("hmm. %s.", i18n.S(c.Detail))
	case c.Status == pace.Fail && say == "":
		return i18n.T("that one didn't work: %s.", i18n.S(c.Detail))
	case c.Status == pace.Partial || c.Status == pace.Fail:
		return say
	case c.Status == pace.Pass && c.Fix != "":
		if say == "" {
			say = i18n.T("works, with the OmaBoot? %s fix.", c.Fix)
		}
		return say
	}
	return ""
}

// cues are Boot's lines as each check starts, by check or criterion ID.
var cues = map[string]string{
	"boot.installer-efi64":       i18n.M("first: how did we get here? checking the boot path…"),
	"boot.install":               i18n.M("installing needs Omarchy itself. noting that…"),
	"boot.power-cycle":           i18n.M("a restart needs it installed too. noting that…"),
	"display.native-resolution":  i18n.M("the screen. counting pixels…"),
	"graphics":                   i18n.M("GPU time. let's see how many donuts it can spin…"),
	"graphics.power-down":        i18n.M("the spare GPU should nap when idle. watching…"),
	"power.battery-status":       i18n.M("battery. how much juice is left…"),
	"audio.codec":                i18n.M("sound chip. who's singing in there…"),
	"network.wifi":               i18n.M("Wi-Fi. listening for networks…"),
	"network.ethernet":           i18n.M("Ethernet. any cable plugged in…"),
	"thermal":                    i18n.M("now the heat. making the CPU sweat for a bit…"),
	"input.keyboard-backlight":   i18n.M("keyboard glow. flicking the lights…"),
	"input.ambient-light-sensor": i18n.M("light sensor. how bright is it in here…"),
	"ports.firewire":             i18n.M("FireWire. is the bus up…"),
	"storage.optical":            i18n.M("the disc drive. anyone home…"),
}

// Cue is Boot's line as a check starts (translated), or "": by its ID, or for a criterion of a
// check that decides several (graphics.discrete, thermal.fans) by the check's.
func Cue(id string) string {
	if c, ok := cues[id]; ok {
		return i18n.S(c)
	}
	if k, _, ok := strings.Cut(id, "."); ok {
		if c, ok := cues[k]; ok {
			return i18n.S(c)
		}
	}
	return ""
}

// NetPhase is what the network step is doing.
type NetPhase int

const (
	NetScanning  NetPhase = iota // looking for cables, phones and Wi-Fi
	NetList                      // idle: the Wi-Fi list (if any) is up, nothing going on
	NetJoining                   // joining a Wi-Fi network
	NetDHCP                      // asking for an address
	NetApplying                  // setting a static address
	NetSwitching                 // switching to another wired connection (NetState.To)
)

// Link states for the Ethernet and phone lines.
const (
	LinkNone      = 0 // nothing plugged in
	LinkCarrier   = 1 // a cable or phone is there, not online through it (yet)
	LinkConnected = 2 // online through it: in use
	LinkReady     = 3 // a working connection that isn't carrying the run: Enter switches to it
	LinkOff       = 4 // turned off while another carries the run: picking it switches back
	LinkLimit     = 5 // there, but OmaBoot? can't use it (an LTE modem that needs dialing): ▲
)

// NetState is the network step as the backend sees it. Times are scene times, stamped when a
// message arrives.
type NetState struct {
	Phase NetPhase
	At    float64 // when the phase began

	Ethernet, Tether string // the found lines' text: "no adapter or cable", "eth0: connected · …"
	EthLink, TetLink int    // LinkNone … LinkLimit: how the lines are lit
	WifiChip         string // e.g. BCM43224; "" when the Mac has no Wi-Fi
	CanScan          bool   // there is a Wi-Fi interface to scan with
	NoWifi           string // why there's no Wi-Fi list, when there isn't

	Phone      string       // the device on the phone line by its USB name ("iPhone", "Pixel 7"), or ""
	PhoneApple bool         // it's an iPhone: Trust and Personal Hotspot, no USB tethering switch
	PhoneNeed  network.Need // what it needs before it shares a connection (Trust, USB tethering…)
	PhoneAt    float64      // when the phone line last changed (Boot's line follows it)

	ListAt   float64 // when the list arrived (networks appear one by one from here)
	Networks []WifiNet
	SSID     string // the Wi-Fi network being joined, or joined

	Online  bool   // the Mac is online (kept when the person goes back to the step)
	Via     string // how: "Ethernet", "Phone" (a phone or LTE stick over USB) or "Wi-Fi"
	To      string // while switching (NetSwitching, or joining Wi-Fi from a wired one), the connection it's switching to, as Via
	Iface   string
	IP      string
	Info    string // e.g. "maple-house-5G · 5 GHz · 192.168.1.47"
	Static  bool   // online through a static address set on this step
	Gateway string
	// SignIn: the connection wants a sign-in page (a captive portal answered for boot.watch).
	SignIn bool

	// Parked is the connection turned off for another (owner, 2026-10-11/12: one connection
	// carries the run), as Via names it; picking its line switches back. "" when none is.
	Parked string

	Refreshed string // "catalog snapshot refreshed: …", or while Checking what it checks
	Checking  bool   // the newer-snapshot check is running (a spinner follows Refreshed)
	// RefreshFailed: the check didn't reach the site (× instead of ✓); Stick: the line on the
	// snapshot built into the stick, under it, dim (in use, or not used)
	RefreshFailed bool
	Stick         string
	RefreshedAt   float64
	Err           string // the last attempt's problem, e.g. a refused password
}

// StaticTarget is the interface a static address would go on, as the form shows it.
type StaticTarget struct {
	Iface string // "" when the Mac has no network interface at all
	What  string // e.g. "USB Ethernet adapter  ·  cable in, no address offered"
}

// Duration writes how long something took: "2 min 41 s", "48 s".
func Duration(d time.Duration) string {
	s := int(d.Round(time.Second).Seconds())
	if s >= 60 {
		return i18n.T("%d min %d s", s/60, s%60)
	}
	return i18n.T("%d s", s)
}

// PlanFacts is the run plan on the stick (internal/plan, LIVE-PLAN §30): this Mac's section to
// run, or why it isn't used.
type PlanFacts struct {
	File      string    // its name on the stick
	Problems  []string  // why it can't be used ("line 4: …"); then Run is nil
	NoSection string    // the Mac it has no section for, when that's why it isn't used
	Run       *plan.Run // this Mac's plan, nil when it isn't used
	Best      string    // the configuration the tool would pick when several fit and the plan doesn't say
}
