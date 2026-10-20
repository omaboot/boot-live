package app

import (
	"context"
	"fmt"
	"os/exec"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/doesitomarchy/doesitomarchy/pkg/report"

	"github.com/omaboot/boot-live/internal/handson"
	"github.com/omaboot/boot-live/internal/i18n"
	"github.com/omaboot/boot-live/internal/send"
	"github.com/omaboot/boot-live/internal/ui"
)

// Boot timing (0.7.5; owner, 2026-10-15): how long the Mac took to start, in systemd's parts and
// boot-live's own (started, first frame, quick pass), and how fast the OmaBoot? drive reads. One
// iMac10,1 boot was 4× slower than the one before, its stick reading 0.7 MB/s against 16.6, and
// the GPU check then ran out of time: the report now says so. It goes in the report as an extra
// (boot.timing: the schema's extras take an id, a label and a detail; no new fields), on Quick
// results as a line, with a warning when the drive is slow, and in bugreport (boot.txt).

// sdPart is one of systemd's startup parts: firmware, loader, kernel, initrd, userspace.
type sdPart struct {
	Name string
	D    time.Duration
}

// sdStartup is systemd's "Startup finished in …" line, read.
type sdStartup struct {
	Parts []sdPart
	Total time.Duration
	OK    bool
}

var sdPartRe = regexp.MustCompile(`^(.+?) \(([a-z]+)\)$`)

// parseStartup reads systemd-analyze's summary among the startup lines: "Startup finished in
// 2.1s (loader) + 12.003s (kernel) + 41.2s (initrd) + 38.1s (userspace) = 1min 33.4s".
func parseStartup(lines []string) sdStartup {
	line := ""
	for _, l := range lines {
		if strings.HasPrefix(l, "Startup finished in ") {
			line = l
		}
	}
	parts, total, ok := strings.Cut(strings.TrimPrefix(line, "Startup finished in "), " = ")
	if !ok {
		return sdStartup{}
	}
	var s sdStartup
	if s.Total, ok = sdDuration(total); !ok {
		return sdStartup{}
	}
	for _, p := range strings.Split(parts, " + ") {
		m := sdPartRe.FindStringSubmatch(strings.TrimSpace(p))
		if m == nil {
			continue
		}
		if d, ok := sdDuration(m[1]); ok {
			s.Parts = append(s.Parts, sdPart{m[2], d})
		}
	}
	s.OK = true
	return s
}

// sdDuration reads a systemd duration: "1min 5.1s", "938ms", "1h 2min 3s".
func sdDuration(s string) (time.Duration, bool) {
	var d time.Duration
	fs := strings.Fields(s)
	if len(fs) == 0 {
		return 0, false
	}
	for _, f := range fs {
		n := strings.TrimRight(f, "abcdefghijklmnopqrstuvwxyzµ")
		v, err := strconv.ParseFloat(n, 64)
		if err != nil {
			return 0, false
		}
		switch strings.TrimPrefix(f, n) {
		case "h":
			d += time.Duration(v * float64(time.Hour))
		case "min":
			d += time.Duration(v * float64(time.Minute))
		case "s":
			d += time.Duration(v * float64(time.Second))
		case "ms":
			d += time.Duration(v * float64(time.Millisecond))
		case "us", "µs":
			d += time.Duration(v * float64(time.Microsecond))
		default:
			return 0, false
		}
	}
	return d, true
}

// startedIn is how long the Mac took to start, from systemd's summary among the startup lines,
// as the screens write durations; "" when systemd didn't say.
func startedIn(lines []string) string {
	if s := parseStartup(lines); s.OK {
		return ui.Duration(s.Total)
	}
	return ""
}

// bootTimes is the boot's timing: systemd's parts, boot-live's moments since the kernel started
// (0 when unknown), and the drive's reading (nil before it's done).
type bootTimes struct {
	sd                     sdStartup
	started, drew, checked time.Duration
	drive                  *handson.BootRead
}

// clock is a time since the kernel started, "1:07".
func clock(d time.Duration) string {
	s := int(d.Round(time.Second).Seconds())
	return fmt.Sprintf("%d:%02d", s/60, s%60)
}

func secs(d time.Duration) string { return fmt.Sprintf("%.1f s", d.Seconds()) }

// driveEvidence is the drive's reading in the report's English, or "".
func (bt bootTimes) driveEvidence() string {
	d := bt.drive
	if d == nil {
		return ""
	}
	if d.Bytes == 0 {
		return "the OmaBoot? drive's speed couldn't be read: " + d.Err
	}
	where := "on the disk"
	if d.Image {
		where = "inside airootfs.sfs"
	}
	s := fmt.Sprintf("the OmaBoot? drive (%s) read %.1f MB/s (%d MiB %s, O_DIRECT)", d.Disk, d.MBps(), d.Bytes>>20, where)
	if d.MBps() < handson.SlowDrive {
		s += ": slow (usually 10 MB/s or more), so whatever loads from it is slow too"
	}
	return s
}

// evidence is the boot timing in the report's English (boot.timing's detail).
func (bt bootTimes) evidence() string {
	var parts []string
	if bt.sd.OK {
		var ps []string
		for _, p := range bt.sd.Parts {
			ps = append(ps, p.Name+" "+secs(p.D))
		}
		parts = append(parts, fmt.Sprintf("booted in %s (systemd): %s", clock(bt.sd.Total), strings.Join(ps, ", ")))
	}
	var at []string
	if bt.started > 0 {
		at = append(at, "boot-live started at "+clock(bt.started))
	}
	if bt.drew > 0 {
		at = append(at, "first frame at "+clock(bt.drew))
	}
	if bt.checked > 0 {
		at = append(at, "quick pass at "+clock(bt.checked))
	}
	if len(at) > 0 {
		parts = append(parts, strings.Join(at, ", ")+" after the kernel started")
	}
	if d := bt.driveEvidence(); d != "" {
		parts = append(parts, d)
	}
	return strings.Join(parts, "; ")
}

// line is Quick results' boot line (translated), or "".
func (bt bootTimes) line() string {
	var parts []string
	if bt.sd.OK {
		var ps []string
		for _, p := range bt.sd.Parts {
			ps = append(ps, i18n.T("%s %s", p.Name, ui.Duration(p.D)))
		}
		parts = append(parts, i18n.T("Booted in %s: %s", ui.Duration(bt.sd.Total), strings.Join(ps, ", ")))
	}
	if bt.drew > 0 {
		parts = append(parts, i18n.T("boot-live drew at %s", ui.Duration(bt.drew)))
	}
	if bt.checked > 0 {
		parts = append(parts, i18n.T("checks at %s", ui.Duration(bt.checked)))
	}
	if d := bt.drive; d != nil && d.Bytes > 0 {
		parts = append(parts, i18n.T("drive %.1f MB/s", d.MBps()))
	}
	return strings.Join(parts, " · ")
}

// slow is Quick results' warning on the drive (translated), or "".
func (bt bootTimes) slow() string {
	if d := bt.drive; d != nil && d.Bytes > 0 && d.MBps() < handson.SlowDrive {
		return i18n.T("The OmaBoot? drive is reading slowly (%.1f MB/s; usually 10+): tests that load from it may time out.", d.MBps())
	}
	return ""
}

// atKernel is a wall-clock moment of this run as time since the kernel started (0 unknown).
func (r *Run) atKernel(t time.Time) time.Duration {
	if t.IsZero() || r.upAtStart <= 0 {
		return 0
	}
	return r.upAtStart + t.Sub(r.startWall)
}

// boot is the boot timing as it stands.
func (r *Run) boot() bootTimes {
	r.mu.Lock()
	defer r.mu.Unlock()
	bt := bootTimes{sd: parseStartup(r.startupLines), started: r.upAtStart, drive: r.drive}
	bt.drew = r.atKernel(r.drawn)
	if !r.quickRestored { // a run picked up from its copy didn't run the pass on this start
		bt.checked = r.atKernel(r.passStart)
	}
	return bt
}

// bootExtra is the report's boot.timing extra; false off the Mac (nothing measured).
func (r *Run) bootExtra() (report.FileExtra, bool) {
	if r.Fixture() || r.upAtStart <= 0 {
		return report.FileExtra{}, false
	}
	ev := r.boot().evidence()
	if ev == "" {
		return report.FileExtra{}, false
	}
	return report.FileExtra{ID: "boot.timing", Label: "Boot timing", Detail: ev}, true
}

// Drawn: the screens drew their first frame (once; ui calls it when the backend has it).
func (b *Backend) Drawn() {
	r := b.R
	r.mu.Lock()
	if r.drawn.IsZero() {
		r.drawn = time.Now()
	}
	r.mu.Unlock()
}

// measureDrive reads the OmaBoot? drive's speed once, early (off the screens' goroutine).
func (r *Run) measureDrive() {
	if r.Fixture() {
		return
	}
	disk, err := send.BootDisk(r.Opt.Root)
	if err != nil {
		return
	}
	base := "arch"
	for _, f := range strings.Fields(r.HW.Cmdline) {
		if v, ok := strings.CutPrefix(f, "archisobasedir="); ok && v != "" {
			base = v
		}
	}
	d := handson.BootDriveSpeed(disk, base)
	r.mu.Lock()
	r.drive = &d
	r.mu.Unlock()
}

// driveNote is the quick pass's word on the drive for a check that ran out of time
// (checks.Env.Drive): whether the drive was slow, so a timeout from slow loading isn't read as
// the hardware's fault.
func (r *Run) driveNote() string {
	r.mu.Lock()
	d := r.drive
	r.mu.Unlock()
	switch {
	case d == nil:
		return "the OmaBoot? drive's speed wasn't measured yet"
	case d.Bytes == 0:
		return ""
	case d.MBps() < handson.SlowDrive:
		return fmt.Sprintf("the OmaBoot? drive was reading slowly (%.1f MB/s; usually 10+): the check may have been waiting on loading from it", d.MBps())
	}
	return fmt.Sprintf("the OmaBoot? drive read %.1f MB/s, not slow", d.MBps())
}

// startupNow brings the startup lines up to date: systemd's summary is read again (at most every
// 20 s) until startup has finished.
func (r *Run) startupNow() []string {
	r.mu.Lock()
	need := !r.Fixture() && !parseStartup(r.startupLines).OK && time.Since(r.startupAt) > 20*time.Second
	if need {
		r.startupAt = time.Now()
	}
	r.mu.Unlock()
	if need {
		ls := r.startup()
		r.mu.Lock()
		r.startupLines = ls
		r.mu.Unlock()
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.startupLines
}

// bootText is bugreport's boot.txt: the boot timing, systemd's summary and its ten slowest units,
// the drive's reads, the launch and the journal around it, and the USB devices on the drive's bus.
func (r *Run) bootText() string {
	var b strings.Builder
	line := func(f string, a ...any) { fmt.Fprintf(&b, f+"\n", a...) }
	bt := r.boot()
	line("boot timing: %s", bt.evidence())
	if d := bt.drive; d != nil {
		line("drive reads: %s", strings.Join(d.Reads, "; "))
		if d.Err != "" {
			line("drive read error: %s", d.Err)
		}
	}
	run := func(args ...string) string {
		ctx, cancel := context.WithTimeout(r.ctx, 10*time.Second)
		defer cancel()
		out, _ := exec.CommandContext(ctx, args[0], args[1:]...).CombinedOutput()
		return strings.TrimRight(string(out), "\n")
	}
	if !r.Fixture() {
		line("\n== systemd-analyze\n%s", run("systemd-analyze", "--no-pager"))
		blame := strings.Split(run("systemd-analyze", "blame", "--no-pager"), "\n")
		if len(blame) > 10 {
			blame = blame[:10]
		}
		line("\n== systemd-analyze blame (slowest 10)\n%s", strings.Join(blame, "\n"))
		// what boot-live's tty1 waited for (the Air's 0.7.9 run: it started as the 18.7 s wait
		// for the network's time ended; this says whether that was why)
		line("\n== systemd-analyze critical-chain getty@tty1.service\n%s", run("systemd-analyze", "critical-chain", "--no-pager", "getty@tty1.service"))
		line("\n== systemd-analyze critical-chain time-sync.target\n%s", run("systemd-analyze", "critical-chain", "--no-pager", "time-sync.target"))
	}
	if ls := r.launchLines(bt, run); len(ls) > 0 {
		line("\n== launch (seconds since the kernel started)")
		for _, l := range ls {
			line("%s", l)
		}
	}
	if ls := launchJournal(r.launchLines(bt, run), run, r.Fixture()); len(ls) > 0 {
		line("\n== journal around the launch (every unit, seconds since the kernel started)")
		for _, l := range ls {
			line("%s", l)
		}
	}
	if disk, err := send.BootDisk(r.Opt.Root); err == nil {
		line("\n== USB devices on the OmaBoot? drive's bus (%s)", disk)
		for _, l := range usbBusmates(r, disk) {
			line("%s", l)
		}
	}
	return b.String()
}

// launchLines are the steps from tty1's autologin to boot-live, in time order: getty@tty1
// becoming active (systemd), the launchers' marks (/run/omaboot/launch: omaboot-bootlive, its
// udevadm settle, omaboot-live) and boot-live's own start. The Air's 0.7.12 run had about 7 s
// between the first and the last that nothing accounted for.
func (r *Run) launchLines(bt bootTimes, run func(...string) string) []string {
	type step struct {
		at   float64
		what string
	}
	var steps []step
	if !r.Fixture() {
		if us, err := strconv.ParseInt(strings.TrimSpace(run("systemctl", "show", "-p", "ActiveEnterTimestampMonotonic", "--value", "getty@tty1.service")), 10, 64); err == nil && us > 0 {
			steps = append(steps, step{float64(us) / 1e6, "getty@tty1 active (autologin on tty1)"})
		}
	}
	for _, l := range r.Opt.Root.Lines("/run/omaboot/launch") {
		at, what, ok := strings.Cut(strings.TrimSpace(l), " ")
		if f, err := strconv.ParseFloat(at, 64); ok && err == nil {
			steps = append(steps, step{f, what})
		}
	}
	if len(steps) == 0 {
		return nil
	}
	if bt.started > 0 {
		steps = append(steps, step{bt.started.Seconds(), "boot-live started"})
	}
	sort.SliceStable(steps, func(i, j int) bool { return steps[i].at < steps[j].at })
	out := make([]string, len(steps))
	for i, s := range steps {
		out[i] = fmt.Sprintf("%7.2f  %s", s.at, s.what)
		if i > 0 {
			out[i] += fmt.Sprintf("  (+%.2f s)", s.at-steps[i-1].at)
		}
	}
	return out
}

// journalAt is a short-monotonic journal line's time: "[   17.641234] host unit[1]: …".
var journalAt = regexp.MustCompile(`^\[\s*(\d+\.\d+)\]`)

// launchJournal is the whole journal (every unit, not only the kernel's) from 3 s before the
// launch's first step to 1 s after its last: the Air's 0.8.0 run had 5 s with no kernel line
// between getty@tty1 starting and fbcon taking the console (owner, 0.8.0 review). At most 400
// lines.
func launchJournal(launch []string, run func(...string) string, fixture bool) []string {
	if fixture || len(launch) == 0 {
		return nil
	}
	at := func(l string) float64 {
		f, _ := strconv.ParseFloat(strings.Fields(l)[0], 64)
		return f
	}
	from, to := at(launch[0])-3, at(launch[len(launch)-1])+1
	var out []string
	for _, l := range strings.Split(run("journalctl", "-b", "-o", "short-monotonic", "--no-pager", "--no-hostname"), "\n") {
		m := journalAt.FindStringSubmatch(l)
		if m == nil {
			continue
		}
		if t, _ := strconv.ParseFloat(m[1], 64); t >= from && t <= to {
			out = append(out, l)
		}
	}
	if len(out) > 400 {
		out = append(out[:400], fmt.Sprintf("… %d more lines", len(out)-400))
	}
	return out
}

var usbBusRe = regexp.MustCompile(`^usb(\d+)$`)

// usbBusmates lists the USB devices on the same bus as disk ("sdb"): port, IDs, speed, names; the
// drive's own marked.
func usbBusmates(r *Run, disk string) []string {
	root := r.Opt.Root
	link := root.Link("/sys/block/" + disk)
	bus, own := "", ""
	for _, seg := range strings.Split(link, "/") {
		if m := usbBusRe.FindStringSubmatch(seg); m != nil {
			bus = m[1]
		}
		if bus != "" && strings.HasPrefix(seg, bus+"-") && !strings.Contains(seg, ":") {
			own = seg
		}
	}
	if bus == "" {
		return []string{"not on USB: " + link}
	}
	var out []string
	for _, d := range root.Glob("/sys/bus/usb/devices/*") {
		n := path.Base(d)
		if n != "usb"+bus && !strings.HasPrefix(n, bus+"-") || strings.Contains(n, ":") {
			continue
		}
		s := fmt.Sprintf("%s %s:%s %s Mb/s %s %s", n, root.Read(d+"/idVendor"), root.Read(d+"/idProduct"), root.Read(d+"/speed"),
			root.Read(d+"/manufacturer"), root.Read(d+"/product"))
		if n == own {
			s += "  <- the OmaBoot? drive"
		}
		out = append(out, strings.TrimSpace(s))
	}
	return out
}
