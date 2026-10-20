// Package app is the real run: it sets the Mac up safely, identifies it, gets online, runs the
// quick pass and gets the report out, either behind the screens (Backend) or unattended (Auto).
package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/doesitomarchy/doesitomarchy/pkg/report"

	"github.com/omaboot/boot-live/internal/boot"
	"github.com/omaboot/boot-live/internal/bootwatch"
	"github.com/omaboot/boot-live/internal/checks"
	"github.com/omaboot/boot-live/internal/handson"
	"github.com/omaboot/boot-live/internal/i18n"
	"github.com/omaboot/boot-live/internal/identify"
	"github.com/omaboot/boot-live/internal/keyboard"
	"github.com/omaboot/boot-live/internal/network"
	"github.com/omaboot/boot-live/internal/safety"
	"github.com/omaboot/boot-live/internal/send"
	"github.com/omaboot/boot-live/internal/sounds"
	"github.com/omaboot/boot-live/internal/sysroot"
	"github.com/omaboot/boot-live/internal/ui"
)

// Options are fixed for a run.
type Options struct {
	Root     sysroot.Root // "/" on the Mac; a fixture tree with --sysroot
	Edition  ui.Edition
	Target   send.Target
	Helper   string        // the GPU helper
	StickDir string        // save here instead of the BOOTLIVE stick (--stick)
	Load     time.Duration // the thermal check's CPU load
	Settle   time.Duration // how long an idle second GPU gets to suspend
	Version  string        // boot-live's version
	Omarchy  string        // the Omarchy version when the image doesn't say
}

// Run is one real run.
type Run struct {
	Opt Options

	ctx    context.Context
	cancel context.CancelFunc
	client *http.Client

	Disks    []safety.Disk
	diskBase map[string]int64 // bytes written to each internal disk when it was set read-only
	Fatal    error            // the run can't go on safely
	Watchdog *safety.Watchdog
	HW       identify.Hardware
	Cat      *identify.Catalog
	Ident    identify.Result
	Kmsg     []string
	Fixes    []checks.Fix

	mu        sync.Mutex
	config    string // the configuration: exact, picked, or "" (not sure / unknown)
	picked    bool
	net       network.State
	netFacts  checks.NetFacts
	wifi      network.Wifi
	aps       []network.AP
	refreshed string
	results   []checks.Result
	handsOn   []checks.Result // the hands-on tests' results so far (Record)
	passStart time.Time
	passEnd   time.Time
	code      string          // the report's code once sent
	keep      string          // the copy kept on the stick before a test that sleeps (Keep), "" before one
	saved     string          // the copy saved to the stick (Save), "" before one
	offer     *resumeNote     // an unfinished run's copy from this Mac to pick up (findResume), until answered
	older     []resumeNote    // this Mac's other unsent copies: set aside with the answer
	other     *resumeNote     // the newest unsent copy of this Mac's from another version: named, never offered
	sounds    sounds.Prefs    // the sound settings the last boot kept on the stick (loadSounds)
	kbd       keyboard.Choice // the keyboard layout in use (keyboard.go)
	// loadKeymap loads a layout picked on Hello on the console; nil off it (KeyboardLoaded)
	loadKeymap func(keymap string) error
	plan       *runPlan          // the run plan on the stick (loadPlan), nil for none
	bugMem     map[string][]byte // the bug reports saved in this run, by their name on the stick (bugsend.go)
	// quickRestored: the quick pass's results came from the copy picked up (no pass this start)
	quickRestored bool
	noteReq       uint64      // the note ops asked for (noteTicket)
	noteMu        sync.Mutex  // held by the note op running
	noteDone      uint64      // the last note op run
	resumed       *resumeNote // the copy this run was picked up from, or nil
	gone          time.Time   // when the stick the system runs from was pulled out (StickGone); results stop there
	started       func(checks.Check, float64)
	live          func(boot.Params)
	gate          func(context.Context, checks.Check) error // holds a check back (the GPU notice)
	upAtStart     time.Duration                             // the system's uptime when the tool started (0: unknown)

	// the console (console.go): its notes, the redo and change commands for the report, results
	// set by hand (by criterion: supported, partial, failed, not_tested), the first run of each
	// result measured again, and the check being measured again now
	notes     []string
	console   []report.ConsoleEntry
	forced    map[string]string
	first     map[string]checks.Result
	rerunning bool

	startupLines []string  // how the Mac's startup went (startup), read again until systemd says it finished
	startupAt    time.Time // when they were last read

	// boot timing (boottime.go): when this run began (wall clock, with upAtStart), when the
	// screens drew their first frame, and the OmaBoot? drive's speed
	startWall time.Time
	drawn     time.Time
	drive     *handson.BootRead
}

// New prepares a run; Setup does the work.
func New(opt Options) *Run {
	ctx, cancel := context.WithCancel(context.Background())
	return &Run{Opt: opt, ctx: ctx, cancel: cancel, client: &http.Client{Timeout: 30 * time.Second}, upAtStart: uptime(opt.Root), startWall: time.Now()}
}

// Close stops everything the run started, and puts back a fan minimum the thermal check may
// still have raised (main defers it, so it runs however the tool ends, a panic included).
func (r *Run) Close() {
	r.cancel()
	checks.RestoreFans()
	r.closeWifi()
}

func (r *Run) closeWifi() {
	r.mu.Lock()
	if r.wifi != nil {
		r.wifi.Close()
		r.wifi = nil
	}
	r.mu.Unlock()
}

// Fixture reports whether the run reads a fixture tree (nothing is written then).
func (r *Run) Fixture() bool { return !r.Opt.Root.IsSystem() }

// Setup does what comes before anything else: on the live system it makes the
// internal disks read-only and checks them, and refuses to go on otherwise; then it starts the
// thermal watchdog, reads the hardware, loads the catalog snapshot and identifies the Mac.
func (r *Run) Setup() {
	root := r.Opt.Root
	r.loadKeyboard() // first: Hello shows it whatever happens next
	if root.IsSystem() {
		if err := safety.Guard(sysroot.System); err != nil {
			r.Fatal = err
			return
		}
	}
	ds, err := safety.Protect(root)
	r.Disks = ds
	if err != nil {
		r.Fatal = err
		return
	}
	r.diskBase = map[string]int64{}
	for _, d := range safety.Internal(ds) {
		r.diskBase[d.Name] = safety.Written(root, d.Name)
	}
	r.Watchdog = safety.Watch(r.ctx, root)
	r.HW = identify.Read(root)
	r.Cat, err = identify.Embedded()
	if err != nil {
		r.Fatal = fmt.Errorf("the built-in catalog snapshot is broken: %w", err)
		return
	}
	r.Ident = r.Cat.Identify(r.HW)
	r.config = r.Ident.Config
	r.Kmsg = checks.ReadKmsg(root)
	r.Fixes = checks.DetectFixes(root, r.Kmsg, r.Cat)
	r.findResume()
	r.loadSounds()
	r.loadPlan()
}

// Pick records the configuration the person picked ("" = not sure).
func (r *Run) Pick(config string) {
	r.mu.Lock()
	r.config, r.picked = config, true
	r.mu.Unlock()
}

// DisksLine is Hello's first setup line.
func (r *Run) DisksLine() string {
	in := safety.Internal(r.Disks)
	names := strings.Join(safety.Names(in), ", ")
	switch {
	case len(in) == 0:
		return i18n.T("no internal disks found: nothing to protect")
	case r.Fixture():
		return i18n.N("dry run: %d internal disk would be set read-only (%s)", "dry run: %d internal disks would be set read-only (%s)", len(in), len(in), names)
	}
	return i18n.N("%d internal disk set read-only (%s)", "%d internal disks set read-only (%s)", len(in), len(in), names)
}

// Written is how many bytes have gone to the internal disks since they were set read-only, or -1
// when sysfs doesn't say (Hello's "0 B written").
func (r *Run) Written() int64 {
	sum, known := int64(0), false
	for _, d := range safety.Internal(r.Disks) {
		base, now := r.diskBase[d.Name], safety.Written(r.Opt.Root, d.Name)
		if base < 0 || now < 0 {
			continue
		}
		sum, known = sum+now-base, true
	}
	if !known {
		return -1
	}
	return sum
}

// SnapshotLine is Hello's catalog line.
func (r *Run) SnapshotLine() string {
	return i18n.T("catalog snapshot %s, %d configurations", snapName(r.Cat), r.Cat.Configs())
}

// snapName is how a snapshot is named on screen: its date and version, "2026-10-11 (51bdb7c5)",
// or the version alone in a snapshot from before dates.
func snapName(c *identify.Catalog) string {
	if c.Date != "" {
		return c.Date + " (" + short(c.Snap.Version) + ")"
	}
	return short(c.Snap.Version)
}

func short(v string) string {
	if len(v) > 8 {
		return v[:8]
	}
	return v
}

// gb is memory in whole GB, as Apple names it.
func gb(mib int) int { return int(math.Round(float64(mib) / 1024)) }

// Site is where reports go, as the screens name it: the production site always reads
// "DoesItOmarchy.com" (readability); any other target (staging) by its host.
// Only what is shown changes; requests go to the target's URL as it is.
func (r *Run) Site() string { return SiteName(r.Opt.Target.Host()) }

// SiteName is a target host as shown on screen.
func SiteName(host string) string {
	h := strings.ToLower(strings.TrimSuffix(host, "."))
	if h == "" || h == "doesitomarchy.com" || h == "www.doesitomarchy.com" {
		return "DoesItOmarchy.com"
	}
	return host
}

// Facts are the screens' starting facts for this run.
func (r *Run) Facts(o *ui.Options) *ui.Facts {
	f := &ui.Facts{Site: r.Site(), CanSubmit: r.Opt.Target.CanSubmit(), Production: r.Opt.Target.Production(), ReportKB: 6}
	if !f.CanSubmit {
		f.NoSubmit = i18n.T("no report key in this image: save the report to external storage")
	}
	if f.CanSendBug = r.Opt.Target.CanSendBug(); f.CanSendBug && r.Fatal == nil && (!r.Fixture() || r.Opt.StickDir != "") {
		f.BugsWaiting = len(r.unsentBugs())
	}
	if r.Fatal != nil {
		f.Fatal = r.Fatal.Error()
		f.Omarchy, f.Kernel = r.omarchy(), r.HW.Kernel
		return f
	}
	f.Omarchy, f.Kernel = r.omarchy(), r.HW.Kernel
	f.DisksLine, f.SnapshotLine = r.DisksLine(), r.SnapshotLine()
	if in := safety.Internal(r.Disks); len(in) > 0 {
		l := safety.DiskLayout(r.Opt.Root, in[0])
		f.Disk, f.MoreDisks, f.DryRun, f.Written = &l, len(in)-1, r.Fixture(), r.Written
	}
	f.SystemLine, f.ImageLine, f.Image = r.SystemLine(), r.ImageLine(), r.image()
	cpu := identify.ShortCPU(r.HW.CPU)
	f.Mac = ui.MacInfo{Name: r.Ident.Name, ID: r.Ident.Identifier, Config: fmt.Sprintf("%s, %d GB", cpu, gb(r.HW.MemMiB))}
	parts := []string{cpu}
	for _, g := range r.HW.GPUs() {
		parts = append(parts, strings.TrimSpace(g.ID()+" "+g.Driver))
	}
	f.Parts = strings.Join(append(parts, fmt.Sprintf("%d GB", gb(r.HW.MemMiB))), "  ·  ")
	f.Known = true
	for _, fx := range r.Fixes {
		f.FixNames = append(f.FixNames, fx.Short)
	}
	notSure := ui.Choice{ID: "", Label: i18n.T("Not sure"), Note: i18n.T("sent without a configuration: a reviewer matches it")}
	switch {
	case !r.Ident.Known:
		f.Unknown = true
		f.MatchNote = i18n.T("This Mac isn't in the catalog yet. The checks still run, and the report can be saved.")
	case r.Ident.Exact && r.HW.BoardID != "":
		f.MatchNote = i18n.T("The only match in the catalog (board ID %s). When several match, you pick yours from a list.", r.HW.BoardID)
	case r.Ident.Exact:
		f.MatchNote = i18n.T("The only match in the catalog. When several match, you pick yours from a list.")
	default:
		var ids []string
		for _, c := range r.Ident.Candidates {
			ids = append(ids, c.Config)
		}
		labels := r.Cat.Labels(ids)
		for i, c := range r.Ident.Candidates {
			note := i18n.T("fits every part it reports")
			if len(c.NotReported) > 0 {
				note = i18n.T("not seen: %s", strings.Join(c.NotReported, ", "))
			}
			if labels[i] != c.Config {
				note += "  ·  " + c.Config
			}
			f.Candidates = append(f.Candidates, ui.Choice{ID: c.Config, Label: labels[i], Note: note})
		}
		f.Candidates = append(f.Candidates, notSure)
	}
	if ids := r.Cat.ConfigIDs(r.Ident.Identifier); len(ids) > 1 {
		labels := r.Cat.Labels(ids)
		for i, id := range ids {
			c := ui.Choice{ID: id, Label: labels[i]}
			if labels[i] != id {
				c.Note = id
			}
			f.Configs = append(f.Configs, c)
		}
		f.Configs = append(f.Configs, notSure)
	}
	if r.Cat != nil {
		v := r.Cat.View(r.HW, r.Ident)
		f.View = &v
	}
	f.Resume, f.OtherCopy = r.resumeFacts(), r.otherFacts()
	f.Sounds = r.sounds
	f.Keyboard = r.Keyboard()
	f.Plan = r.planFacts()
	return f
}

func (r *Run) omarchy() string { return send.OmarchyVersion(r.Opt.Root, r.Opt.Omarchy) }

// env is the checks' environment, with what the network step found.
func (r *Run) env() *checks.Env {
	r.mu.Lock()
	defer r.mu.Unlock()
	ident := r.Ident
	ident.Config = r.config
	e := &checks.Env{Root: r.Opt.Root, HW: r.HW, Ident: ident, Cat: r.Cat, Fixes: r.Fixes, Kmsg: r.Kmsg, Net: r.netFacts,
		Helper: r.Opt.Helper, Watchdog: r.Watchdog, Load: r.Opt.Load, Settle: r.Opt.Settle, Started: r.started, Live: r.live, Gate: r.gate}
	if !r.Fixture() {
		e.Drive = r.driveNote
	}
	if !r.Fixture() && r.Opt.Target.Watch != "" { // the Wi-Fi and Ethernet checks move their data through it
		watch := r.Opt.Target.Watch
		e.Net.Watch = func(ctx context.Context, iface string) bootwatch.Outcome {
			return network.Watch(watch, iface).Test(ctx)
		}
		e.Net.Reach = func(ctx context.Context) bootwatch.Outcome { return network.Watch(watch, "").Reach(ctx) }
	}
	if !e.Net.Scanned {
		e.Net.Scan = func(ctx context.Context) (int, error) {
			w, err := r.wifiDev()
			if err != nil {
				return 0, err
			}
			aps, err := w.Scan(ctx)
			return len(aps), err
		}
	}
	return e
}

// wifiDev opens Wi-Fi (iwd) once.
func (r *Run) wifiDev() (network.Wifi, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.wifi != nil {
		return r.wifi, nil
	}
	w, err := network.NewWifi(r.Opt.Root)
	if err != nil {
		return nil, err
	}
	r.wifi = w
	return w, nil
}

// Quick runs the quick pass, handing each result to emit as it finishes.
func (r *Run) Quick(emit func(checks.Result)) {
	e := r.env()
	plan := checks.Plan(e)
	r.mu.Lock()
	r.passStart = time.Now()
	r.mu.Unlock()
	checks.Run(r.ctx, e, plan, func(x checks.Result) {
		r.mu.Lock()
		if r.gone.IsZero() { // after the stick came out, a result may fail for the wrong reason
			r.results = append(r.results, x)
		}
		r.mu.Unlock()
		emit(x)
	})
	r.mu.Lock()
	r.passEnd = time.Now()
	r.mu.Unlock()
}

// Plan says how many results the quick pass gives and about how long it takes.
func (r *Run) Plan() (int, float64) {
	e := r.env()
	plan := checks.Plan(e)
	expect := 0.0
	for _, c := range plan {
		expect += c.Expect.Seconds() + 0.1
	}
	return checks.Count(e, plan), expect
}

// Report builds the report from the results so far.
func (r *Run) Report(handle, notice, notes string) *report.File {
	e := r.env()
	r.mu.Lock()
	rs := r.merged(e)
	config, picked, at, gone := r.config, r.picked, r.passStart, r.gone
	r.mu.Unlock()
	items, extras := checks.Items(e, rs)
	if x, ok := r.bootExtra(); ok {
		extras = append(extras, x)
	}
	extras = append(extras, r.keyboardExtra())
	var fixes []string
	seen := map[string]bool{}
	for _, f := range r.Fixes {
		fixes, seen[f.ID] = append(fixes, f.ID), true
	}
	for _, x := range rs { // fixes a hands-on test applied itself (the lid-wake fix)
		if x.FixID != "" && !seen[x.FixID] {
			fixes, seen[x.FixID] = append(fixes, x.FixID), true
		}
	}
	var ports []report.PortAnswer // the connectors only mean something with the configuration's layout
	for _, x := range rs {
		for _, p := range x.Ports {
			if config != "" {
				ports = append(ports, report.PortAnswer{Test: x.ID, Connector: p.Connector, Paths: p.Paths, How: p.How, WiringSaid: p.WiringSaid})
			}
		}
	}
	if at.IsZero() {
		at = time.Now()
	}
	r.mu.Lock()
	ns := append([]string(nil), r.notes...) // the tester's own, from the console, first
	cons := append([]report.ConsoleEntry(nil), r.console...)
	r.mu.Unlock()
	if notes != "" {
		ns = append(ns, notes)
	}
	if picked && config != "" && !r.Ident.Exact {
		ns = append(ns, fmt.Sprintf("Configuration picked by the tester from %d candidates.", len(r.Ident.Candidates)))
	}
	if picked && config == "" && len(r.Ident.Candidates) > 1 {
		ns = append(ns, "The tester wasn't sure which configuration this is.")
	}
	if n := r.resumedNote(); n != "" {
		ns = append(ns, n)
	}
	if n := r.planNote(); n != "" {
		ns = append(ns, n)
	}
	if !gone.IsZero() {
		ns = append(ns, "The OmaBoot? drive the system runs from was pulled out during the run, at "+
			gone.UTC().Format("15:04:05")+" UTC; nothing after that is recorded.")
	}
	f := send.Build(send.Input{HW: r.HW, Ident: r.Ident, Config: config, Items: items, Extras: extras, Fixes: fixes, Ports: ports,
		Omarchy: r.omarchy(), Image: send.Image(r.Opt.Root, r.Opt.Edition.Key), Version: r.Opt.Version,
		Handle: send.Handle(handle), Notice: notice, Notes: strings.Join(ns, " "), TestedAt: at})
	f.Console = cons
	return f
}

// Record takes the hands-on tests' results so far, replacing the last set: the run's one report
// carries them, a hands-on result replacing the quick pass's for the same criterion.
func (r *Run) Record(rs []checks.Result) {
	r.mu.Lock()
	if r.gone.IsZero() {
		r.handsOn = append([]checks.Result(nil), rs...)
	}
	r.mu.Unlock()
}

// StickGone marks the stick the system runs from as pulled out: nothing more is recorded, and
// the report says so.
func (r *Run) StickGone() {
	r.mu.Lock()
	if r.gone.IsZero() {
		r.gone = time.Now()
	}
	r.mu.Unlock()
}

// Gone reports whether the stick the system runs from was pulled out.
func (r *Run) Gone() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return !r.gone.IsZero()
}

// merged are the quick pass's results with the hands-on ones in place (r.mu held). A hands-on
// result for a criterion a live boot can't decide here stays not_tested (live-limit): the tests
// aren't offered for those, and this keeps it so.
func (r *Run) merged(e *checks.Env) []checks.Result {
	by := map[string]bool{}
	for _, x := range r.handsOn {
		by[x.ID] = true
	}
	var out []checks.Result
	for _, x := range r.results {
		if !by[x.ID] {
			out = append(out, x)
		}
	}
	for _, x := range r.handsOn {
		if checks.LiveLimited(e, x.ID) && x.Status != checks.NotTested {
			x = checks.Result{ID: x.ID, Label: x.Label, Status: checks.NotTested, Reason: checks.LiveLimit,
				Evidence: "a live boot can't decide this here (the hands-on result was: " + x.Status + ")"}
		}
		out = append(out, x)
	}
	for i, x := range out { // set by hand in the console (dev: force)
		if s, ok := r.forced[x.ID]; ok {
			out[i] = forcedResult(x, s)
		}
	}
	return out
}

// ErrAlreadySent: a run sends one report; a new report needs a new quick pass.
var ErrAlreadySent = errors.New("this run's report was already sent")

// Submit checks the report and sends it to the target, once per run.
func (r *Run) Submit(f *report.File) (send.Receipt, error) {
	r.mu.Lock()
	sent := r.code != ""
	r.mu.Unlock()
	if sent {
		return send.Receipt{}, ErrAlreadySent
	}
	if err := send.Allowed(r.Opt.Target, f); err != nil { // the console guard, before anything else
		return send.Receipt{}, err
	}
	if err := send.Check(f); err != nil {
		return send.Receipt{}, fmt.Errorf("the report failed its own check: %w", err)
	}
	ctx, cancel := context.WithTimeout(r.ctx, 45*time.Second)
	defer cancel()
	rc, err := send.Submit(ctx, r.client, r.Opt.Target, f)
	if err == nil {
		r.mu.Lock()
		r.code = rc.Code
		r.mu.Unlock()
	}
	return rc, err
}

// Save checks the report and writes it to the BOOTLIVE stick's results/.
func (r *Run) Save(f *report.File) (string, error) {
	if err := send.Check(f); err != nil {
		return "", fmt.Errorf("the report failed its own check: %w", err)
	}
	r.mu.Lock()
	code, keep, saved := r.code, r.keep, r.saved
	r.mu.Unlock()
	ctx, cancel := context.WithTimeout(r.ctx, 60*time.Second)
	defer cancel()
	// an earlier copy is replaced (the last one saved, else the one kept before a test that
	// sleeps): overwritten under its own name, or removed once the report has its code's name
	last := keep
	if saved != "" {
		last = saved
	}
	name, drop := send.FileName(r.Ident.Identifier, code, time.Now()), last
	if code == "" && last != "" {
		name = last
	}
	p, err := send.Save(ctx, r.Opt.Root, r.Opt.StickDir, f, name, drop)
	if err == nil { // the final report: the copy's note goes, so it isn't offered to pick up
		r.mu.Lock()
		r.saved = name
		r.mu.Unlock()
		r.noteOp(r.noteTicket(), r.dropNote)
	}
	return p, err
}

// Resave writes the report again over a copy saved before it was sent, now under its code's
// name and as sent (the tester's handle in it), so the stick never holds a sent report that
// looks unsent. ok is false when no copy was saved.
func (r *Run) Resave(f *report.File) (p string, ok bool, err error) {
	r.mu.Lock()
	saved := r.saved
	r.mu.Unlock()
	if saved == "" {
		return "", false, nil
	}
	p, err = r.Save(f)
	return p, true, err
}

// Keep saves a copy of the report as it stands before a test that may not wake the Mac, with
// its note for picking the run up again (resume.go); each copy (and the final save) replaces the
// last.
func (r *Run) Keep(f *report.File, k ui.KeepInfo, t uint64) (string, error) {
	if err := send.Check(f); err != nil {
		return "", fmt.Errorf("the report failed its own check: %w", err)
	}
	r.mu.Lock()
	if r.keep == "" {
		r.keep = send.FileName(r.Ident.Identifier, "", time.Now())
	}
	name := r.keep
	r.mu.Unlock()
	ctx, cancel := context.WithTimeout(r.ctx, 60*time.Second)
	defer cancel()
	p, err := send.Save(ctx, r.Opt.Root, r.Opt.StickDir, f, name, "")
	if err == nil && k.Test != "" {
		var nerr error
		r.noteOp(t, func() { nerr = r.keepNote(name, k) })
		if nerr != nil {
			return p, fmt.Errorf("the copy is saved, but its note for picking the run up again isn't: %w", nerr)
		}
	}
	return p, err
}

// CanRefresh says whether RefreshSnapshot will check the target (false: no target, or a fixture).
func (r *Run) CanRefresh() bool { return r.Opt.Target.URL != "" && !r.Fixture() }

// SignInPage asks boot.watch for one echo over the connection just up (plain HTTP first,
// where a captive portal shows itself) and says why it looks like a sign-in page, or "" when it
// doesn't or can't tell. It never decides whether the Mac is online: boot.watch being down
// mustn't stop a report reaching the site.
func (r *Run) SignInPage() string {
	if r.Fixture() || r.Opt.Target.Watch == "" {
		return ""
	}
	ctx, cancel := context.WithTimeout(r.ctx, 10*time.Second)
	defer cancel()
	if o := network.Watch(r.Opt.Target.Watch, "").Reach(ctx); o.Problem == bootwatch.SignIn {
		return o.Err
	}
	return ""
}

// RefreshSnapshot fetches a newer catalog snapshot from the target once online, and says what
// it found ("" when there's no target).
// It also reports whether the check worked, and a line on the snapshot built into the stick,
// saying whether it's still the one in use.
func (r *Run) RefreshSnapshot() (line string, ok bool, stick string) {
	if !r.CanRefresh() {
		return "", false, ""
	}
	before, beforeN := snapName(r.Cat), r.Cat.Configs()
	r.mu.Lock()
	via := r.netFacts.Iface // the default route's: where the refresh goes out
	r.mu.Unlock()
	ctx, cancel := context.WithTimeout(r.ctx, 15*time.Second)
	defer cancel()
	changed, err := r.Cat.Refresh(ctx, r.client, r.Opt.Target.URL)
	if err == nil { // the network checks' proof that this interface carries data, should boot.watch not answer
		r.mu.Lock()
		r.netFacts.SiteVia = via
		r.mu.Unlock()
	}
	switch {
	case err != nil:
		return i18n.T("catalog snapshot not refreshed from %s: %s", r.Site(), errText(err)), false,
			i18n.T("on OmaBoot? device: %s · %d configurations · active", before, beforeN)
	case changed:
		return i18n.T("catalog snapshot refreshed: %s · %d configurations · from %s", snapName(r.Cat), r.Cat.Configs(), r.Site()), true,
			i18n.T("on OmaBoot? device: %s · %d configurations · not used", before, beforeN)
	}
	return i18n.T("catalog snapshot %s is the newest · %d configurations · from %s", snapName(r.Cat), r.Cat.Configs(), r.Site()), true,
		i18n.T("on OmaBoot? device: the same snapshot, active")
}

// Summary is what Quick results and Finish say about the results.
func (r *Run) Summary() ui.QuickDoneMsg {
	e := r.env()
	r.mu.Lock()
	rs := r.merged(e)
	took := r.passEnd.Sub(r.passStart)
	r.mu.Unlock()
	items, _ := checks.Items(e, rs)
	failed := 0
	booted := false
	for _, x := range rs {
		if x.Status == checks.Failed {
			failed++
		}
		if x.ID == "boot.installer-efi64" && x.Status == checks.Supported {
			booted = true
		}
	}
	m := ui.QuickDoneMsg{Groups: toGroups(checks.Groups(e, items)), QuickPassIn: ui.Duration(took)}
	m.Startup = r.startupNow() // read again until systemd says startup finished: Summary comes again with each hands-on result
	m.TestedOn, m.StartedIn = r.testedOn(rs), startedIn(m.Startup)
	bt := r.boot()
	m.BootLine, m.SlowDrive = bt.line(), bt.slow()
	m.Headline = i18n.T("This Mac boots Omarchy.")
	if !booted {
		m.Headline = i18n.T("This Mac runs OmaBoot? Live.")
	}
	var names []string
	for _, f := range r.Fixes {
		names = append(names, f.Short)
	}
	switch {
	case failed > 0:
		m.AnswerSay = i18n.N("%d check failed. the log has the evidence.", "%d checks failed. the log has the evidence.", failed, failed)
		m.DoneWord = i18n.T("Done.")
	case len(names) > 0:
		m.AnswerSay = i18n.T("the %s fix took effect here. the ▲ ones need Omarchy installed to test.", strings.Join(names, ", "))
		m.DoneWord = i18n.T("Yay!")
	default:
		m.AnswerSay = i18n.T("nothing failed. the ▲ ones need Omarchy installed to test.")
		m.DoneWord = i18n.T("Yay!")
	}
	if b, err := json.Marshal(r.Report("", "", "")); err == nil {
		m.ReportKB = (len(b) + 1023) / 1024
	}
	return m
}

func toGroups(gs []checks.Group) []ui.Group {
	out := make([]ui.Group, 0, len(gs))
	for _, g := range gs {
		out = append(out, ui.Group{Name: g.Name, Items: g.Items, IDs: g.IDs})
	}
	return out
}

// toCheck is a result as the screens show it.
func toCheck(x checks.Result) ui.Check { return ui.CheckOf(x) }

// errText is an error for the screen: the sending errors in words.
func errText(err error) string {
	var rej *send.Rejected
	var busy *send.Busy
	switch {
	case errors.As(err, &rej):
		return i18n.T("the site didn't accept the report: %s", rej.Message)
	case errors.As(err, &busy):
		return i18n.T("the site is busy: try again in %d min", int(math.Ceil(busy.RetryAfter.Minutes())))
	case errors.Is(err, send.ErrNoStick):
		msg := i18n.T("no room for reports on the OmaBoot? drive: plug in a drive labelled BOOTLIVE and try again")
		if _, why, ok := strings.Cut(err.Error(), " ("); ok {
			msg += " (" + why
		}
		return msg
	case errors.Is(err, send.ErrChanged):
		return i18n.T("results in this run were changed in the console: DoesItOmarchy.com doesn't take its report; save a copy to external storage")
	case errors.Is(err, context.DeadlineExceeded):
		return i18n.T("no answer in time")
	}
	var ue *url.Error
	if errors.As(err, &ue) {
		return i18n.T("couldn't reach the site: %s", ue.Err.Error())
	}
	return err.Error()
}
