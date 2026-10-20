package checks

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/omaboot/boot-live/internal/boot"
	"github.com/omaboot/boot-live/internal/i18n"
	"github.com/omaboot/boot-live/internal/identify"
	"github.com/omaboot/boot-live/internal/sysroot"
)

// Probe is one render node's record from the GPU helper (helpers/egl-probe).
type Probe map[string]string

// ParseProbes reads the helper's key=value output: one record per node, from node= to end=1.
func ParseProbes(out string) []Probe {
	var ps []Probe
	var cur Probe
	sc := bufio.NewScanner(strings.NewReader(out))
	for sc.Scan() {
		k, v, ok := strings.Cut(sc.Text(), "=")
		if !ok {
			continue
		}
		if k == "node" {
			cur = Probe{}
			ps = append(ps, cur)
		}
		if cur != nil {
			cur[k] = v
		}
	}
	return ps
}

var glesRe = regexp.MustCompile(`OpenGL ES (\d+)\.(\d+)`)

// GLES is the OpenGL ES version a record reports, e.g. 3.0 as (3, 0).
func (p Probe) GLES() (int, int) {
	m := glesRe.FindStringSubmatch(p["version"])
	if m == nil {
		return 0, 0
	}
	a, _ := strconv.Atoi(m[1])
	b, _ := strconv.Atoi(m[2])
	return a, b
}

// Verdict decides graphics.integrated / graphics.discrete from a node's record alone, against the
// wording "Hyprland renders on this GPU with hardware acceleration, at OpenGL ES 3.0 or later,
// not with the llvmpipe software renderer … A GPU that reaches only OpenGL ES 2.0 fails on stock
// Omarchy" (gpuVerdict adds what the run around the probe found):
//
//   - supported: a hardware (not llvmpipe) OpenGL ES 3.0+ context, the test frame rendered right
//     (the pixel read back is the colour drawn), and the 1080p compositor-like load, run for
//     about 10 s, never fell below 30 fps in any one-second window (bench_min_fps: a pass is
//     exercised end to end, owner 2026-10-13, "no false passes");
//   - partial: a hardware OpenGL ES 3.0+ context whose test frame came out wrong, whose load's
//     slowest second ran below 30 fps (below 10: the GPU stalled during the test; owner,
//     2026-10-10, after an iMac10,1 run read 0.9 fps with a black screen), or whose load didn't
//     run or didn't report its slowest second;
//   - failed: no context, a software renderer, or OpenGL ES 2.0 only.
func (p Probe) Verdict() Result {
	if e := p["error"]; e != "" && p["renderer"] == "" {
		return fail("GPU helper: " + e)
	}
	major, minor := p.GLES()
	// Mesa names the chip, not Apple's model (an iMac18,3's Radeon Pro 570 is "RX 470")
	what := fmt.Sprintf("Mesa's name: %s, %s", p["renderer"], p["version"])
	switch {
	case p["llvmpipe"] == "1":
		return fail("only the software renderer: " + what)
	case p["renderer"] == "":
		return fail("no OpenGL ES context")
	case major < 3:
		return fail(fmt.Sprintf("OpenGL ES %d.%d only (stock Omarchy needs 3.0): %s", major, minor, what))
	case p["draw_ok"] != "1":
		return part(fmt.Sprintf("%s; the test frame came out wrong (pixel %s, want 64,128,191,255)", what, p["draw_pixel"]))
	}
	ev := what + "; test frame rendered right"
	avg, aerr := strconv.ParseFloat(p["bench_fps"], 64)
	low, lerr := strconv.ParseFloat(p["bench_min_fps"], 64)
	switch {
	case lerr == nil:
		ev += fmt.Sprintf("; 1080p compositor-like load for %s s: %s fps on average, %s fps in its slowest second", orNA(p["bench_s"]), orNA(p["bench_fps"]), p["bench_min_fps"])
	case aerr == nil:
		// a helper that ran the load but didn't time each second: the average alone can hide a stall
		low = avg
		ev += "; 1080p compositor-like load " + p["bench_fps"] + " fps on average"
	default:
		why := "the 1080p compositor-like load didn't run"
		if b := p["bench_error"]; b != "" {
			why += " (" + b + ")"
		}
		return part(ev + "; " + why)
	}
	switch {
	case low < 10:
		return part(ev + ": the GPU stalled during the test (below 10 fps)")
	case low < 30:
		return part(ev + ": it dips below 30 fps, too slow for a smooth desktop")
	case lerr != nil:
		return part(ev + "; its slowest second wasn't measured")
	}
	return pass(ev)
}

func orNA(s string) string {
	if s == "" {
		return "?"
	}
	return s
}

// gpuVerdict decides a GPU's criterion from its probe record (Probe.Verdict) and what the run
// around the probe found:
//
//   - faults: kernel log lines for this GPU logged while it was tested (gpuFaults). Any line
//     makes a pass partial, quoted (rule 4: an error for the tested device during its test); a
//     lockup or ring stall (lockupMarks) makes it failed (0.7.3 review: the iMac10,1's RV730 at
//     1.3 fps with "ring 0 stalled" ×81 was logged partial);
//   - sc: the internal panel's scanout, when this GPU drives the panel (nil otherwise). No
//     vblanks on the panel's CRTC (or no CRTC on it at all) makes a pass partial, "the panel's
//     display pipe isn't running"; vblanks at under 50 % or over 150 % of the mode's refresh
//     make it partial too. When the vblanks can't be counted (the ioctl isn't supported, the
//     node can't be opened) the evidence says so and the verdict doesn't depend on it.
//
// So a pass needs hardware ES 3.0+, the right frame, a slowest second of 30 fps or more,
// vblanks on the panel's pipe when this GPU drives it, and no new GPU errors in the kernel log.
func gpuVerdict(p Probe, faults []string, sc *scanout) Result {
	r := p.Verdict()
	if sc != nil {
		problem, note := sc.verdict()
		switch {
		case problem != "" && r.Status != Failed:
			r.Status = Partial
			r.Evidence += "; " + problem
		case problem != "":
			r.Evidence += "; " + problem
		default:
			r.Evidence += "; " + note
		}
	}
	if len(faults) > 0 {
		q := make([]string, 0, 3)
		for i, l := range faults {
			if i == 3 {
				q = append(q, fmt.Sprintf("and %d more", len(faults)-3))
				break
			}
			q = append(q, "“"+cut(l, 160)+"”")
		}
		r.Evidence += "; the kernel logged GPU errors during the test: " + strings.Join(q, " ")
		if r.Status == Supported {
			r.Status = Partial
		}
		if n := lockups(faults); n > 0 { // a GPU that stopped answering under load (0.7.3 review, iMac10,1)
			r.Status = Failed
			r.Evidence += fmt.Sprintf("; the GPU locked up during the test (%d lockup or ring stall line(s))", n)
		}
	}
	return r
}

// lockups counts the lines that say a GPU stopped answering (lockupMarks).
func lockups(lines []string) int {
	n := 0
	for _, l := range lines {
		for _, m := range lockupMarks {
			if strings.Contains(l, m) {
				n++
				break
			}
		}
	}
	return n
}

// gpuTemps are the GPUs' temperatures now, by PCI slot, in °C: their driver's hwmon temp1_input
// (radeon, amdgpu, nouveau; i915 gives none), for the evidence before and after the load.
func gpuTemps(e *Env) map[string]float64 {
	out := map[string]float64{}
	for _, g := range e.HW.GPUs() {
		for _, f := range e.Root.Glob("/sys/bus/pci/devices/" + g.Slot + "/hwmon/hwmon*/temp1_input") {
			if v, ok := e.Root.Int(f); ok && v > 0 {
				out[g.Slot] = float64(v) / 1000
				break
			}
		}
	}
	return out
}

// tempText is a GPU's temperature at the start and end of the load, for the evidence, or "".
func tempText(before, after map[string]float64, slot string) string {
	b, okb := before[slot]
	a, oka := after[slot]
	switch {
	case okb && oka:
		return fmt.Sprintf("GPU temperature %.0f °C at the start of the load, %.0f °C at its end", b, a)
	case okb:
		return fmt.Sprintf("GPU temperature %.0f °C at the start of the load (none read at its end)", b)
	case oka:
		return fmt.Sprintf("GPU temperature %.0f °C at the end of the load", a)
	}
	return ""
}

// scanout is what counting the internal panel's vblanks found (countVblanks).
type scanout struct {
	Card, Conn string  // card1, eDP-1
	Crtc       int     // the CRTC's index (the pipe)
	Mode       string  // the CRTC's mode, 1440x900
	ModeHz     float64 // its refresh
	Vblanks    int     // vblanks counted
	Secs       float64 // over this long
	Off        string  // why the panel's pipe isn't running, if it isn't
	Skip       string  // why the vblanks couldn't be counted: nothing decided
}

// verdict is the scanout's problem (the pipe doesn't run, or at an implausible rate), or a note
// for the evidence when there is none.
func (s scanout) verdict() (problem, note string) {
	where := fmt.Sprintf("panel %s on %s", s.Conn, s.Card)
	switch {
	case s.Skip != "":
		return "", where + ": vblanks not counted, " + s.Skip
	case s.Off != "":
		return fmt.Sprintf("the panel's display pipe isn't running (%s: %s)", where, s.Off), ""
	case s.Vblanks <= 0 || s.Secs <= 0:
		return fmt.Sprintf("the panel's display pipe isn't running (%s: no vblank in %.1f s)", where, s.Secs), ""
	}
	hz := float64(s.Vblanks) / s.Secs
	ev := fmt.Sprintf("%s scanning out: %d vblanks in %.2f s on CRTC %d (%.1f Hz", where, s.Vblanks, s.Secs, s.Crtc, hz)
	if s.ModeHz > 0 {
		ev += fmt.Sprintf("; mode %s at %.1f Hz", s.Mode, s.ModeHz)
		if hz < s.ModeHz*0.5 || hz > s.ModeHz*1.5 {
			return fmt.Sprintf("the panel's display pipe runs at an implausible rate (%s)", ev+")"), ""
		}
	}
	return "", ev + ")"
}

// panelDrive finds the internal panel's connector and the GPU (PCI slot) and DRM card that
// drive it, like display() does: connected, on a KMS driver; a boot VGA GPU's first. Nil when
// there's none.
func panelDrive(e *Env) *connector {
	var first *connector
	cs := connectors(e)
	for i := range cs {
		c := &cs[i]
		if !internalPanel(c.Name) || c.Status != "connected" || c.Driver == "" {
			continue
		}
		if e.Root.Read("/sys/bus/pci/devices/"+c.Slot+"/boot_vga") == "1" {
			return c
		}
		if first == nil {
			first = c
		}
	}
	return first
}

// measureScanout counts the panel's vblanks, or reads a fixture tree's record
// (/drm-vblank.txt, key=value: card, connector, crtc, mode, mode_hz, vblanks, secs, off, skip).
func measureScanout(ctx context.Context, e *Env, c *connector) scanout {
	if e.Root.IsSystem() {
		return countVblanks(ctx, c.Card, c.Name)
	}
	s := scanout{Card: c.Card, Conn: c.Name, Skip: "the fixture has no vblank record"}
	b := e.Root.Bytes("/drm-vblank.txt")
	if b == nil {
		return s
	}
	s.Skip = ""
	for _, l := range strings.Split(string(b), "\n") {
		k, v, _ := strings.Cut(l, "=")
		switch k {
		case "crtc":
			s.Crtc, _ = strconv.Atoi(v)
		case "mode":
			s.Mode = v
		case "mode_hz":
			s.ModeHz, _ = strconv.ParseFloat(v, 64)
		case "vblanks":
			s.Vblanks, _ = strconv.Atoi(v)
		case "secs":
			s.Secs, _ = strconv.ParseFloat(v, 64)
		case "off":
			s.Off = v
		case "skip":
			s.Skip = v
		}
	}
	return s
}

// shortRenderer trims Mesa's renderer string for the log: "Mesa Intel(R) HD Graphics 4000 (IVB
// GT2)" → "HD Graphics 4000"; "AMD Radeon RX 470 Graphics (radeonsi, …)" → "AMD Radeon RX 470
// Graphics".
func shortRenderer(s string) string {
	if i := strings.Index(s, " ("); i > 0 {
		s = s[:i]
	}
	s = strings.TrimPrefix(s, "Mesa ")
	return strings.TrimSpace(strings.NewReplacer("Intel(R) ", "", "(R)", "", "(TM)", "").Replace(s))
}

// runHelper runs the GPU helper on every render node, or reads a fixture tree's recorded output
// (/egl-probe.txt) instead.
func runHelper(ctx context.Context, e *Env) (string, error) {
	if !e.Root.IsSystem() {
		if b := e.Root.Bytes("/egl-probe.txt"); b != nil {
			return string(b), nil
		}
		return "", os.ErrNotExist
	}
	if _, err := os.Stat(e.Helper); err != nil {
		return "", err
	}
	out, err := exec.CommandContext(ctx, e.Helper).Output()
	if err != nil && len(out) == 0 {
		return "", err
	}
	return string(out), nil
}

// graphics decides graphics.integrated and graphics.discrete: each GPU kind present gets the
// verdict of its render node (the boot GPU's when there are two of a kind), with the kernel log
// read before and after the test and, for the GPU that drives the internal panel, its vblanks
// counted (gpuVerdict). A GPU with no driver bound fails; a run booted with nomodeset, or an
// image without the helper, is not_tested (other).
func graphics(ctx context.Context, e *Env) []Result {
	before, kmsgOK := readKmsgRecs(e.Root)
	t0 := gpuTemps(e)
	out, herr := runHelper(ctx, e)
	t1 := gpuTemps(e)
	probes := ParseProbes(out)
	nodeOf := map[string]Probe{} // PCI slot → record
	for _, p := range probes {
		node := path.Base(p["node"])
		if slot := e.Root.Uevent("/sys/class/drm/" + node + "/device/uevent")["PCI_SLOT_NAME"]; slot != "" {
			nodeOf[slot] = p
		}
	}
	nomodeset := strings.Contains(" "+e.HW.Cmdline+" ", " nomodeset ")
	// the panel's scanout, while still in the kernel log's window
	panel := panelDrive(e)
	var sc *scanout
	if _, ok := nodeOf[panelSlot(panel)]; ok && !nomodeset {
		s := measureScanout(ctx, e, panel)
		sc = &s
	}
	var during []kmsgRec
	if kmsgOK {
		during = kmsgWindow(e.Root, before)
	}
	gpus := e.HW.GPUs()
	var rs []Result
	done := map[string]bool{}
	for _, g := range gpus {
		id := "graphics.discrete"
		if identify.Integrated(g) {
			id = "graphics.integrated"
		}
		if done[id] {
			continue
		}
		done[id] = true
		dev := fmt.Sprintf("%s %s (%s)", g.ID(), g.Subsys(), orNone(g.Driver))
		var r Result
		p, ok := nodeOf[g.Slot]
		switch {
		case ok:
			var gsc *scanout
			if panel != nil && panel.Slot == g.Slot {
				gsc = sc
			}
			faults := gpuFaults(during, g, gpus)
			r = gpuVerdict(p, faults, gsc)
			if !kmsgOK {
				r.Evidence += "; the kernel log couldn't be read, so it wasn't checked for GPU errors"
			}
			r.Evidence = dev + ": " + r.Evidence
			if tt := tempText(t0, t1, g.Slot); tt != "" {
				r.Evidence += "; " + tt
			}
			major, minor := p.GLES()
			short := shortRenderer(p["renderer"])
			r.Detail = fmt.Sprintf("%s, %s, GLES %d.%d", short, orNone(g.Driver), major, minor)
			r.Renderer = short
			if r.Status == Failed && lockups(faults) > 0 {
				r.Detail = i18n.T("%s: locked up under load", short)
			}
			if r.Status == Partial {
				switch {
				case len(faults) > 0:
					r.Detail = i18n.T("%s: GPU errors in the kernel log", short)
				case gsc != nil && strings.Contains(r.Evidence, "the panel's display pipe"):
					r.Detail = i18n.T("%s: the panel's display pipe isn't running right", short)
				case strings.Contains(r.Evidence, "stalled"):
					r.Detail = i18n.T("%s: stalled under load", short)
				case strings.Contains(r.Evidence, "dips below 30 fps"):
					r.Detail = i18n.T("%s: dips below 30 fps", short)
				}
			}
		case nomodeset:
			r = skip(Other, dev+": booted with nomodeset (safe graphics): no GPU driver")
			r.Detail = i18n.T("booted with nomodeset")
		case g.Driver == "":
			r = fail(dev + ": no kernel driver bound to the GPU")
			r.Detail = i18n.T("no driver for %s", g.ID())
		case herr != nil:
			r = skip(Other, dev+": the GPU helper isn't on this image ("+e.Helper+"), so nothing was rendered")
			r.Detail = i18n.T("GPU helper missing")
		default:
			r = fail(dev + ": the driver is bound but the GPU has no render node")
			r.Detail = i18n.T("%s bound, no render node", g.Driver)
		}
		r.ID = id
		if f := e.fixFor(g); f != nil && r.Status != NotTested {
			r.FixID, r.Fix = f.ID, f.Short
		}
		if r.Status == Supported {
			r.Say = i18n.T("%s on %s. GLES %s. hardware, not llvmpipe.", shortRenderer(p["renderer"]), g.Driver, glesString(p))
		}
		rs = append(rs, r)
	}
	return rs
}

func panelSlot(c *connector) string {
	if c == nil {
		return ""
	}
	return c.Slot
}

// kmsgRec is one kernel log record: its level (0 emerg … 7 debug), sequence number and message.
type kmsgRec struct {
	Level int
	Seq   uint64
	Msg   string
}

// parseKmsgRec reads one /dev/kmsg record, "6,1234,5678901,-;message".
func parseKmsgRec(l string) (kmsgRec, bool) {
	head, msg, ok := strings.Cut(l, ";")
	if !ok || strings.HasPrefix(l, " ") {
		return kmsgRec{}, false
	}
	f := strings.Split(head, ",")
	if len(f) < 2 {
		return kmsgRec{}, false
	}
	pri, err1 := strconv.Atoi(f[0])
	seq, err2 := strconv.ParseUint(f[1], 10, 64)
	if err1 != nil || err2 != nil {
		return kmsgRec{}, false
	}
	return kmsgRec{Level: pri & 7, Seq: seq, Msg: msg}, true
}

// readKmsgRecs reads /dev/kmsg's records with their sequence numbers (ReadKmsg's way: raw
// non-blocking reads until EAGAIN); ok is false when it can't be opened.
func readKmsgRecs(r sysroot.Root) ([]kmsgRec, bool) {
	fd, err := syscall.Open(r.P("/dev/kmsg"), syscall.O_RDONLY|syscall.O_NONBLOCK|syscall.O_CLOEXEC, 0)
	if err != nil {
		return nil, false
	}
	defer syscall.Close(fd)
	var out []kmsgRec
	var all bytes.Buffer
	buf := make([]byte, 16<<10)
	for i := 0; i < 1<<16; i++ {
		n, err := syscall.Read(fd, buf)
		if n > 0 {
			all.Write(buf[:n])
		}
		if errors.Is(err, syscall.EPIPE) {
			continue
		}
		if err != nil || n <= 0 {
			break
		}
	}
	for _, l := range strings.Split(all.String(), "\n") {
		if rec, ok := parseKmsgRec(l); ok {
			out = append(out, rec)
		}
	}
	return out, true
}

// kmsgWindow is what the kernel logged since before: the records past before's last sequence
// number. A fixture tree records the lines its helper run caused in /egl-probe.kmsg (the same
// record format), as /egl-probe.txt records its output.
func kmsgWindow(r sysroot.Root, before []kmsgRec) []kmsgRec {
	var last uint64
	if n := len(before); n > 0 {
		last = before[n-1].Seq
	}
	after, _ := readKmsgRecs(r)
	if !r.IsSystem() {
		for _, l := range r.Lines("/egl-probe.kmsg") {
			if rec, ok := parseKmsgRec(l); ok {
				after = append(after, rec)
			}
		}
	}
	var out []kmsgRec
	for _, rec := range after {
		if rec.Seq > last {
			out = append(out, rec)
		}
	}
	return out
}

// gpuFaultRe matches kernel log phrases of a GPU in trouble, besides lockupMarks: i915 ("GPU
// HANG", "Resetting rcs0", "*ERROR* … FIFO underrun"), amdgpu ("ring gfx timeout", "[gfxhub]
// page fault", "Fence fallback timer expired on ring sdma0", "GPU reset"), radeon ("GPU fault
// detected", "VM fault", "GPU lockup"), nouveau ("fifo: fault", "gr: TRAP", "SCHED_ERROR",
// "channel … killed"), and WARN splats from drivers/gpu/drm.
var gpuFaultRe = regexp.MustCompile(`(?i)gpu hang|gpu lockup|gpu fault|gpu reset|gpu recovery|page fault|vm fault|mmu fault|protection_fault|\*error\*|fence fallback timer expired|stalled for more than|timeout|timed out|resetting (chip|[a-z]+cs\d)|fifo underrun|\btrap\b|\bfault\b|warning:|\bhung\b|sched_error|context lost|banned|killed`)

// gpuDrivers are the KMS drivers a Mac's GPU can have, for telling which GPU a line is about.
var gpuDrivers = []string{"i915", "xe", "amdgpu", "radeon", "nouveau", "gma500", "bochs-drm", "simpledrm"}

// gpuFaults are the records in the window that report trouble with GPU g: a line at error level
// (or worse) or with a fault phrase (gpuFaultRe, lockupMarks), about g. A line is about g when
// it names g's PCI slot, or names no GPU slot of this Mac and names g's driver, or names no GPU
// driver at all but comes from DRM ("[drm", "drm:", drivers/gpu/drm): an unattributed DRM error
// counts against every GPU.
func gpuFaults(window []kmsgRec, g identify.PCIDev, gpus []identify.PCIDev) []string {
	var out []string
	for _, rec := range window {
		l := strings.TrimSpace(rec.Msg)
		if !aboutGPU(l, g, gpus) {
			continue
		}
		bad := rec.Level <= 3 || gpuFaultRe.MatchString(l)
		for _, m := range lockupMarks {
			bad = bad || strings.Contains(l, m)
		}
		if bad {
			out = append(out, l)
		}
	}
	return out
}

func aboutGPU(l string, g identify.PCIDev, gpus []identify.PCIDev) bool {
	for _, o := range gpus {
		if o.Slot != "" && strings.Contains(l, o.Slot) {
			return o.Slot == g.Slot
		}
	}
	named := false
	for _, d := range gpuDrivers {
		if wordIn(l, d) {
			if d == g.Driver {
				return true
			}
			named = true
		}
	}
	if named {
		return false
	}
	return strings.Contains(l, "[drm") || strings.Contains(l, "drm:") || strings.Contains(l, "drivers/gpu/drm")
}

// wordIn reports whether w appears in l as a word of its own ("i915" in "[drm:intel_x [i915]]",
// not "radeon" in "radeonsi").
func wordIn(l, w string) bool {
	for i := 0; ; {
		j := strings.Index(l[i:], w)
		if j < 0 {
			return false
		}
		a, b := i+j, i+j+len(w)
		if (a == 0 || !wordByte(l[a-1])) && (b == len(l) || !wordByte(l[b])) {
			return true
		}
		i = a + 1
	}
}

func wordByte(c byte) bool {
	return c == '_' || c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
}

func glesString(p Probe) string {
	a, b := p.GLES()
	return fmt.Sprintf("%d.%d", a, b)
}

func orNone(s string) string {
	if s == "" {
		return "no driver"
	}
	return s
}

// fixFor is the fix that took effect on a device, if any (the fix registry's devices).
func (e *Env) fixFor(d identify.PCIDev) *Fix {
	if e.Cat == nil {
		return nil
	}
	for i := range e.Fixes {
		f := e.Cat.Fix(e.Fixes[i].ID)
		if f == nil {
			continue
		}
		for _, fd := range f.Devices {
			if fd.ID == "pci:"+d.ID() && (fd.Subsystem == "" || fd.Subsystem == d.Subsys()) {
				return &e.Fixes[i]
			}
		}
	}
	return nil
}

// powerDown decides graphics.power-down, "Inactive GPU powers down correctly", on Macs with two
// GPUs: the GPU that isn't driving the console (not boot_vga) should reach runtime suspend when
// idle.
//
//   - supported: its runtime PM status reads "suspended" within the settle time;
//   - failed: it stays "active", or runtime PM is unsupported for it.
func powerDown(ctx context.Context, e *Env) []Result {
	gpus := e.HW.GPUs()
	var idle *identify.PCIDev
	for i := range gpus {
		if !gpus[i].BootVGA {
			idle = &gpus[i]
			break
		}
	}
	if idle == nil {
		idle = &gpus[len(gpus)-1]
	}
	p := "/sys/bus/pci/devices/" + idle.Slot + "/power/runtime_status"
	deadline := time.Now().Add(e.Settle)
	st := e.Root.Read(p)
	for st == "active" && time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			deadline = time.Now()
		case <-time.After(500 * time.Millisecond):
		}
		st = e.Root.Read(p)
	}
	dev := fmt.Sprintf("%s (%s, %s)", idle.ID(), orNone(idle.Driver), idle.ShortSlot())
	switch st {
	case "suspended":
		r := pass(dev + ": runtime-suspended while idle")
		r.Detail = i18n.T("%s suspended while idle", idle.ID())
		return []Result{r}
	case "":
		return []Result{fail(dev + ": no runtime power management state")}
	}
	r := fail(fmt.Sprintf("%s: still %s after %s idle (runtime PM control: %s)", dev, st, e.Settle, orNone(e.Root.Read("/sys/bus/pci/devices/"+idle.Slot+"/power/control"))))
	r.Detail = i18n.T("%s still %s", idle.ID(), st)
	return []Result{r}
}

// connector is one DRM connector.
type connector struct {
	Name   string // eDP-1
	Status string
	Modes  []string
	Driver string
	Card   string // card1
	Slot   string // its GPU's PCI slot
}

func connectors(e *Env) []connector {
	var out []connector
	for _, c := range e.Root.Glob("/sys/class/drm/card*-*") {
		base := path.Base(c)
		card, name, _ := strings.Cut(base, "-")
		cn := connector{Name: name, Status: e.Root.Read(c + "/status"), Modes: e.Root.Lines(c + "/modes"), Card: card}
		ue := e.Root.Uevent("/sys/class/drm/" + card + "/device/uevent")
		cn.Driver, cn.Slot = ue["DRIVER"], ue["PCI_SLOT_NAME"]
		out = append(out, cn)
	}
	return out
}

func internalPanel(name string) bool {
	return strings.HasPrefix(name, "eDP") || strings.HasPrefix(name, "LVDS") || strings.HasPrefix(name, "DSI")
}

// display decides display.native-resolution, "Internal display at native resolution": the
// built-in panel is driven by the GPU's own KMS driver at the panel's preferred mode (the first
// mode the kernel lists, from the panel's EDID), and that mode is the catalog's panel resolution
// (from the snapshot, v0.14.3). A 5K iMac's EDID asks for 3840×2160, which the panel scales up
// (research/findings/5k-imac-stock-display.md), so the EDID alone can't stand for "native"; with
// no panel in the snapshot it has to.
//
//   - supported: the internal connector (eDP, LVDS or DSI) is connected and the console's
//     framebuffer runs on the KMS driver at the preferred mode, which isn't below the panel's;
//   - partial: connected on the KMS driver, but at a smaller mode than the preferred one, or the
//     preferred mode has fewer pixels than the panel (a scaled picture);
//   - failed: no internal panel connected on a KMS driver;
//   - not_tested (other): booted with nomodeset (only the firmware's framebuffer).
func display(_ context.Context, e *Env) []Result {
	fbName := e.Root.Read("/sys/class/graphics/fb0/name")
	fbSize := strings.Replace(e.Root.Read("/sys/class/graphics/fb0/virtual_size"), ",", "x", 1)
	var panel *connector
	cs := connectors(e)
	for i := range cs {
		if internalPanel(cs[i].Name) && cs[i].Status == "connected" && cs[i].Driver != "" {
			panel = &cs[i]
			break
		}
	}
	nomodeset := strings.Contains(" "+e.HW.Cmdline+" ", " nomodeset ")
	if panel == nil {
		if nomodeset || strings.HasPrefix(fbName, "simpledrm") || strings.HasPrefix(fbName, "EFI") {
			r := skip(Other, fmt.Sprintf("no KMS driver (booted with nomodeset or the GPU driver didn't load); console on %s at %s", orNone(fbName), fbSize))
			r.Detail = i18n.T("no KMS driver: firmware framebuffer only")
			return []Result{r}
		}
		r := fail(fmt.Sprintf("no internal panel connected on a KMS driver; console on %s at %s", orNone(fbName), fbSize))
		r.Detail = i18n.T("no internal panel found")
		return []Result{r}
	}
	pref := ""
	if len(panel.Modes) > 0 {
		pref = panel.Modes[0]
	}
	native := ""
	if e.Cat != nil {
		native = e.Cat.Panel(e.Ident)
	}
	ev := fmt.Sprintf("%s connected on %s, preferred mode %s, console at %s on %s", panel.Name, panel.Driver, orNone(pref), fbSize, orNone(fbName))
	if native != "" {
		ev += ", panel " + native + " (catalog)"
	}
	var r Result
	switch {
	case pref == "":
		r = fail(ev + "; the panel lists no modes")
	case fbSize != pref:
		r = part(ev + "; not at the preferred mode")
	case native != "" && pixels(pref) < pixels(native):
		r = part(fmt.Sprintf("%s; the panel scales a %s picture up to its %s pixels", ev, pref, native))
		r.Detail = i18n.T("%s picture scaled to the %s panel", times(pref), times(native))
		r.Say = i18n.T("%s picture on a %s panel. scaled up, not native.", times(pref), times(native))
	default:
		r = pass(ev)
		r.Say = i18n.T("%s. every pixel accounted for.", times(pref))
	}
	if r.Detail == "" {
		r.Detail = i18n.T("%s, %s preferred mode", panel.Name, times(pref))
	}
	r.Params = boot.Params{Label: strings.Replace(times(pref), "×", " × ", 1)}
	if f := e.Fixed("omaboot.radeon-imac10-1-panel-clock"); f != nil && panel.Driver == "radeon" {
		r.FixID, r.Fix = f.ID, f.Short
	}
	return []Result{r}
}

// times writes a mode with a multiplication sign: 1440x900 → 1440×900.
func times(mode string) string { return strings.Replace(mode, "x", "×", 1) }

// pixels counts a mode's pixels ("5120x2880"); 0 when it doesn't parse.
func pixels(mode string) int {
	w, h, ok := strings.Cut(mode, "x")
	wi, err1 := strconv.Atoi(w)
	hi, err2 := strconv.Atoi(h)
	if !ok || err1 != nil || err2 != nil {
		return 0
	}
	return wi * hi
}

// lockupMarks are kernel log phrases for a GPU that stopped answering: radeon ("ring 0 stalled for
// more than …", "GPU lockup"), amdgpu ("ring gfx timeout"), i915 ("GPU HANG").
var lockupMarks = []string{"GPU lockup", "stalled for more than", "ring gfx timeout", "GPU HANG", "GPU hang"}

// gpuLockup decides the graphics criteria when the GPU test ran out of time: a GPU whose driver
// logged a lockup failed (the iMac10,1's RV730 with radeon's default options locks up this way).
// A lockup line that names no GPU of this Mac counts against every GPU.
func gpuLockup(e *Env) []Result {
	var hits []string
	for _, l := range ReadKmsg(e.Root) {
		for _, m := range lockupMarks {
			if strings.Contains(l, m) {
				hits = append(hits, strings.TrimSpace(l))
				break
			}
		}
	}
	if len(hits) == 0 {
		return nil
	}
	gpus := e.HW.GPUs()
	named := false
	for _, h := range hits {
		for _, g := range gpus {
			if g.Slot != "" && strings.Contains(h, g.Slot) {
				named = true
			}
		}
	}
	var rs []Result
	done := map[string]bool{}
	for _, g := range gpus {
		id := "graphics.discrete"
		if identify.Integrated(g) {
			id = "graphics.integrated"
		}
		if done[id] {
			continue
		}
		line := ""
		for _, h := range hits {
			if !named || g.Slot != "" && strings.Contains(h, g.Slot) {
				line = h
				break
			}
		}
		if line == "" {
			continue
		}
		done[id] = true
		r := fail(fmt.Sprintf("%s %s (%s): the GPU locked up under load (kernel: %s)", g.ID(), g.Subsys(), orNone(g.Driver), cut(line, 120)))
		r.ID = id
		r.Detail = i18n.T("the GPU locked up under load")
		r.Say = i18n.T("the GPU locked up under load. it stopped answering.")
		if f := e.fixFor(g); f != nil {
			r.FixID, r.Fix = f.ID, f.Short
		}
		rs = append(rs, r)
	}
	return rs
}
