package checks

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/doesitomarchy/doesitomarchy/pkg/report"

	"github.com/omaboot/boot-live/internal/bootwatch"
	"github.com/omaboot/boot-live/internal/identify"
	"github.com/omaboot/boot-live/internal/sysroot"
)

// fixtureEnv sets up the checks on a fixture tree as the real mode does on a Mac.
func fixtureEnv(t *testing.T, dir string) *Env {
	t.Helper()
	cat, err := identify.Embedded()
	if err != nil {
		t.Fatal(err)
	}
	r := sysroot.Root("../../testdata/" + dir)
	h := identify.Read(r)
	e := &Env{Root: r, HW: h, Ident: cat.Identify(h), Cat: cat, Kmsg: ReadKmsg(r)}
	e.Fixes = DetectFixes(r, e.Kmsg, cat)
	e.Net = NetFacts{Scanned: true, Networks: 6}
	return e
}

func runAll(e *Env) map[string]Result {
	out := map[string]Result{}
	plan := Plan(e)
	Run(context.Background(), e, plan, func(r Result) { out[r.ID] = r })
	return out
}

// The quick pass on the three Macs' fixture trees: each criterion's status (and reason), and
// the fixes that took effect.
func TestQuickPassFixtures(t *testing.T) {
	type want struct{ status, reason, fix string }
	cases := map[string]struct {
		fixes []string
		want  map[string]want
	}{
		"macbookair5,2": {[]string{FixApplesmc}, map[string]want{
			"boot.installer-efi64":       {Supported, "", ""},
			"boot.install":               {NotTested, LiveLimit, ""},
			"display.native-resolution":  {Supported, "", ""},
			"graphics.integrated":        {Supported, "", ""},
			"power.battery-status":       {Supported, "", ""},
			"audio.codec":                {Supported, "", ""},
			"network.wifi":               {NotTested, Uncertain, ""}, // scanned, not joined
			"thermal.sensors":            {Partial, "", ""},          // a fixture's temperatures never rise
			"thermal.fans":               {NotTested, Uncertain, ""},
			"input.keyboard-backlight":   {NotTested, Uncertain, ""}, // present: the hands-on test decides
			"input.ambient-light-sensor": {NotTested, Uncertain, ""}, // a reading: the cover test decides
		}},
		"imac10,1": {[]string{FixRadeon}, map[string]want{
			"boot.installer-efi64":      {Supported, "", ""},
			"display.native-resolution": {Supported, "", FixRadeon},
			"graphics.discrete":         {Supported, "", FixRadeon},
			"network.ethernet":          {NotTested, Uncertain, ""}, // a link, no address on a fixture
			"network.wifi":              {NotTested, Uncertain, ""}, // scanned, not joined
			"thermal.fans":              {NotTested, Uncertain, ""}, // the install's kernel (linux-omarchy): decided live; no fan sped up
		}},
		"imac18,3": {nil, map[string]want{
			"display.native-resolution": {Partial, "", ""},
			"graphics.discrete":         {Supported, "", ""},
			"network.ethernet":          {NotTested, NoEquipment, ""},
			"thermal.fans":              {NotTested, Uncertain, ""}, // the install's kernel: decided live
		}},
	}
	for dir, c := range cases {
		t.Run(dir, func(t *testing.T) {
			e := fixtureEnv(t, dir)
			var got []string
			for _, f := range e.Fixes {
				got = append(got, f.ID)
			}
			if len(got) != len(c.fixes) || len(got) > 0 && got[0] != c.fixes[0] {
				t.Errorf("fixes %v, want %v", got, c.fixes)
			}
			rs := runAll(e)
			for id, w := range c.want {
				r, ok := rs[id]
				if !ok {
					t.Errorf("%s: not in the pass", id)
					continue
				}
				if r.Status != w.status || r.Reason != w.reason || r.FixID != w.fix {
					t.Errorf("%s: %s/%s fix %q (%s), want %s/%s fix %q", id, r.Status, r.Reason, r.FixID, r.Evidence, w.status, w.reason, w.fix)
				}
			}
			if dir != "macbookair5,2" {
				for _, id := range []string{"power.battery-status", "input.keyboard-backlight"} {
					if _, ok := rs[id]; ok {
						t.Errorf("%s ran on a desktop", id)
					}
				}
				// the iMac18,3 says chassis 9 (laptop): the identifier decides
				for _, id := range []string{"input.keyboard", "input.trackpad", "input.keyboard-backlight", "power.lid", "power.battery-status", "power.charging"} {
					if Applies(id, e) {
						t.Errorf("%s applies to a desktop", id)
					}
				}
				// iMacs have a light sensor (applesmc ALV0/ALV1)
				if !Applies("input.ambient-light-sensor", e) {
					t.Errorf("input.ambient-light-sensor doesn't apply to an iMac")
				}
			}
			// a USB keyboard's battery (scope Device, its serial number in the name) isn't the Mac's
			if dir == "imac18,3" {
				r := battery(context.Background(), e)[0]
				if r.Status != Failed || strings.Contains(r.Evidence+r.Detail+r.Say, "hid-") {
					t.Errorf("battery with only a keyboard's: %s %q", r.Status, r.Evidence)
				}
			}
			if n := Count(e, Plan(e)); n != len(rs) {
				t.Errorf("Count says %d results, the pass gave %d", n, len(rs))
			}
			// the report's items: a real status for a criterion a live boot can't decide only
			// when a fix that took effect lifts the limit
			var all []Result
			for _, r := range rs {
				all = append(all, r)
			}
			items, extras := Items(e, all)
			if len(extras) != 1 || extras[0].ID != "audio.codec" {
				t.Errorf("extras %+v", extras)
			}
			for _, cp := range e.Cat.Snap.Capabilities {
				it, ok := items[cp.ID]
				if !ok || it.Status == NotTested {
					continue
				}
				if (cp.Live == report.LiveNo || cp.Live == report.LiveT2 && e.Ident.Chip != "t2") && e.lifted(cp.ID) == nil {
					t.Errorf("%s (live: %s) sent as %s", cp.ID, cp.Live, it.Status)
				}
			}
			if it := items["boot.power-cycle"]; it.Status != NotTested || it.Reason != LiveLimit {
				t.Errorf("boot.power-cycle %+v, want not_tested live-limit", it)
			}
			if it := items["power.sleep-wake"]; it.Status != NotTested || it.Reason != NotInProfile {
				t.Errorf("power.sleep-wake %+v, want not_tested not-in-profile", it)
			}
			if len(Groups(e, items)) == 0 {
				t.Error("no groups")
			}
		})
	}
}

// The GPU rule against graphics.integrated/discrete's wording: hardware GLES 3.0+ and a right
// frame passes; a wrong frame is partial; llvmpipe, GLES 2.0 only or no context fail.
func TestGPUVerdict(t *testing.T) {
	hw := "node=/dev/dri/renderD128\napi=gles3\nrenderer=Mesa Intel(R) HD Graphics 4000 (IVB GT2)\nversion=OpenGL ES 3.0 Mesa 26.2.2\nllvmpipe=0\n"
	cases := []struct {
		name, out, want string
	}{
		{"hardware GLES 3.0", hw + "draw_ok=1\nbench_fps=206.4\nbench_min_fps=188.3\nend=1\n", Supported},
		{"wrong frame", hw + "draw_ok=0\ndraw_pixel=0,0,0,255\nend=1\n", Partial},
		{"average hides a stall", hw + "draw_ok=1\nbench_fps=180.2\nbench_min_fps=6.1\nend=1\n", Partial},
		{"dips below 30", hw + "draw_ok=1\nbench_fps=58.0\nbench_min_fps=24.5\nend=1\n", Partial},
		{"no slowest second", hw + "draw_ok=1\nbench_fps=206.4\nend=1\n", Partial},
		{"load didn't run", hw + "draw_ok=1\nbench_error=GL error 0x505\nend=1\n", Partial},
		{"llvmpipe", "node=--software\napi=gles3\nrenderer=llvmpipe (LLVM 22.1.8, 256 bits)\nversion=OpenGL ES 3.2 Mesa\nllvmpipe=1\ndraw_ok=1\nend=1\n", Failed},
		{"GLES 2.0 only", "node=/dev/dri/renderD128\napi=gles2\nrenderer=Mesa Intel(R) GMA 950\nversion=OpenGL ES 2.0 Mesa\nllvmpipe=0\ndraw_ok=1\nend=1\n", Failed},
		{"no context", "node=/dev/dri/renderD128\nerror=no OpenGL ES context (0x3001)\nend=1\n", Failed},
	}
	for _, c := range cases {
		ps := ParseProbes(c.out)
		if len(ps) != 1 {
			t.Fatalf("%s: %d records", c.name, len(ps))
		}
		if r := ps[0].Verdict(); r.Status != c.want {
			t.Errorf("%s: %s (%s), want %s", c.name, r.Status, r.Evidence, c.want)
		}
	}
	if s := shortRenderer("AMD Radeon RX 470 Graphics (radeonsi, polaris10, ACO, DRM 3.64)"); s != "AMD Radeon RX 470 Graphics" {
		t.Errorf("shortRenderer: %q", s)
	}
}

// Thermal and fan rules against "Temperature sensors report under load" and "Fans respond to load".
func TestThermalVerdicts(t *testing.T) {
	cases := []struct {
		name string
		s    Samples
		want string
	}{
		{"rises under load", Samples{Sensors: 3, CPU: []float64{41, 55, 70, 78}}, Supported},
		{"watchdog stop", Samples{Sensors: 3, CPU: []float64{90, 90.5}, Stopped: "coretemp Core 0 96 °C"}, Supported},
		{"stuck", Samples{Sensors: 2, CPU: []float64{40, 40, 40.5}}, Partial},
		{"implausible", Samples{Sensors: 2, CPU: []float64{-127, -127}}, Failed},
		{"no sensor", Samples{}, Failed},
	}
	for _, c := range cases {
		if r := ThermalVerdict(c.s); r.Status != c.want {
			t.Errorf("sensors, %s: %s (%s), want %s", c.name, r.Status, r.Evidence, c.want)
		}
	}
	fans := []struct {
		name string
		s    Samples
		want string
	}{
		{"speeds up", Samples{Fans: [][]int{{2000}, {2004}, {2900}, {3400}}}, Supported},
		{"steady", Samples{Fans: [][]int{{1200}, {1200}, {1210}}}, NotTested},
		{"none", Samples{}, Failed},
	}
	for _, c := range fans {
		if r := FanVerdict(c.s); r.Status != c.want {
			t.Errorf("fans, %s: %s (%s), want %s", c.name, r.Status, r.Evidence, c.want)
		}
	}
}

// A GPU test that runs out of time fails when the kernel logged a lockup for that GPU (the
// iMac10,1 on radeon's default options), and stays uncertain otherwise.
func TestGPULockup(t *testing.T) {
	e := fixtureEnv(t, "imac10,1")
	if rs := gpuLockup(e); rs != nil {
		t.Fatalf("no lockup in the fixture's log, got %+v", rs)
	}
	dir := t.TempDir()
	if err := os.MkdirAll(dir+"/dev", 0o755); err != nil {
		t.Fatal(err)
	}
	log := "6,1,1,-;[drm] radeon kernel modesetting enabled.\n" +
		"4,2,2,-;radeon 0000:02:00.0: ring 0 stalled for more than 10475msec\n" +
		"4,3,3,-;radeon 0000:02:00.0: GPU lockup (current fence id 0x2c last fence id 0x2d on ring 0)\n"
	if err := os.WriteFile(dir+"/dev/kmsg", []byte(log), 0o644); err != nil {
		t.Fatal(err)
	}
	e.Root = sysroot.Root(dir)
	rs := gpuLockup(e)
	if len(rs) != 1 || rs[0].ID != "graphics.discrete" || rs[0].Status != Failed {
		t.Fatalf("got %+v, want graphics.discrete failed", rs)
	}
}

// copyFixture copies a fixture tree to a temporary directory a test can change.
func copyFixture(t *testing.T, dir string) string {
	t.Helper()
	src, dst := "../../testdata/"+dir, t.TempDir()
	err := filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		to := filepath.Join(dst, rel)
		switch {
		case d.Type()&fs.ModeSymlink != 0:
			l, err := os.Readlink(p)
			if err != nil {
				return err
			}
			return os.Symlink(l, to)
		case d.IsDir():
			return os.MkdirAll(to, 0o755)
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(to, b, 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
	return dst
}

// The graphics check's false-pass routes (rule 6), scripted on the MacBook Air's fixture: none
// may come out supported. The fixture records what its helper run caused: its output
// (/egl-probe.txt), the kernel log lines logged during it (/egl-probe.kmsg) and the panel's
// vblanks (/drm-vblank.txt).
func TestGPUFalsePasses(t *testing.T) {
	probe := "node=/dev/dri/renderD128\napi=gles3\nrenderer=Mesa Intel(R) HD Graphics 4000 (IVB GT2)\nversion=OpenGL ES 3.0 Mesa 26.2.2\nllvmpipe=0\ndraw_ok=1\n"
	noise := "4,90,99000000,-;usb 1-1: device descriptor read/64, error -110\n" +
		"3,91,99100000,-;radeon 0000:02:00.0: GPU lockup (current fence id 0x2c)\n" // no GPU of this Mac
	cases := []struct {
		name  string
		files map[string]string
		want  string // in the evidence; "" for supported
	}{
		{"other devices' errors", map[string]string{"/egl-probe.kmsg": noise}, ""},
		{"GPU hang during the probe", map[string]string{"/egl-probe.kmsg": noise +
			"3,92,99200000,-;i915 0000:00:02.0: [drm] GPU HANG: ecode 7:1:85dffffb, in egl-probe [1234]\n"}, "the GPU locked up during the test"},
		{"ring stall during the load", map[string]string{"/egl-probe.kmsg": "4,92,99200000,-;i915 0000:00:02.0: ring 0 stalled for more than 10475msec\n"},
			"the GPU locked up during the test"},
		{"unattributed DRM error", map[string]string{"/egl-probe.kmsg": "3,92,99200000,-;[drm:intel_cpu_fifo_underrun_irq_handler] *ERROR* CPU pipe A FIFO underrun\n"}, "FIFO underrun"},
		{"no vblanks", map[string]string{"/drm-vblank.txt": "vblanks=0\nsecs=1.0\nmode_hz=60\n"}, "display pipe isn't running"},
		{"no CRTC on the panel", map[string]string{"/drm-vblank.txt": "off=no CRTC drives eDP-1\n"}, "display pipe isn't running"},
		{"vblanks at a crawl", map[string]string{"/drm-vblank.txt": "vblanks=12\nsecs=1.0\nmode_hz=60\n"}, "implausible rate"},
		{"stall the average hides", map[string]string{"/egl-probe.txt": probe + "bench_fps=180.2\nbench_min_fps=6.1\nend=1\n"}, "stalled"},
		{"dips below 30 fps", map[string]string{"/egl-probe.txt": probe + "bench_fps=61.0\nbench_min_fps=27.9\nend=1\n"}, "dips below 30 fps"},
	}
	for _, c := range cases {
		e := fixtureEnv(t, "macbookair5,2")
		dir := copyFixture(t, "macbookair5,2")
		for p, b := range c.files {
			if err := os.WriteFile(dir+p, []byte(b), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		e.Root = sysroot.Root(dir)
		rs := graphics(context.Background(), e)
		if len(rs) != 1 {
			t.Fatalf("%s: %d results", c.name, len(rs))
		}
		r := rs[0]
		switch {
		case c.want == "" && r.Status != Supported:
			t.Errorf("%s: %s (%s), want supported", c.name, r.Status, r.Evidence)
		case c.want != "" && (r.Status == Supported || !strings.Contains(r.Evidence, c.want)):
			t.Errorf("%s: %s (%s), want not supported, with %q", c.name, r.Status, r.Evidence, c.want)
		case strings.Contains(c.want, "locked up") && r.Status != Failed: // a lockup fails (0.7.3 review)
			t.Errorf("%s: %s, want failed", c.name, r.Status)
		}
	}
}

// The GPU's temperature at the start and the end of the load goes in the evidence, where its
// driver gives one (0.7.3 review).
func TestGPUTemperature(t *testing.T) {
	e := fixtureEnv(t, "macbookair5,2")
	dir := copyFixture(t, "macbookair5,2")
	hw := dir + "/sys/bus/pci/devices/0000:00:02.0/hwmon/hwmon7"
	if err := os.MkdirAll(hw, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(hw+"/temp1_input", []byte("61000\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	e.Root = sysroot.Root(dir)
	rs := graphics(context.Background(), e)
	if len(rs) != 1 || !strings.Contains(rs[0].Evidence, "GPU temperature 61 °C at the start of the load, 61 °C at its end") {
		t.Errorf("no GPU temperature in %+v", rs)
	}
}

// The network checks' verdicts (supported means exercised end to end; data verified both ways
// through boot.watch): a scan, a join somewhere else, a link or an
// address alone never pass; a failed test counts against the Mac only when boot.watch was there
// to answer, and a sign-in page, the Mac's clock or boot.watch itself never do.
func TestNetworkVerdicts(t *testing.T) {
	const kib = 1 << 10
	verified := bootwatch.Outcome{Reached: true, Down: 1344 * kib, DownOK: 1344 * kib, Up: 64 * kib, UpOK: 64 * kib}
	wrong := bootwatch.Outcome{Reached: true, Down: 64 * kib, DownOK: 900, Problem: bootwatch.Broken, Err: "byte 900 of the 64 KB part was wrong"}
	dead := bootwatch.Outcome{Problem: bootwatch.Broken, Err: "dial tcp: connect: network is unreachable"}
	up, down := &bootwatch.Outcome{Reached: true}, &bootwatch.Outcome{Problem: bootwatch.Broken, Err: "timeout"}
	joined := func(f NetFacts) NetFacts {
		f.Scanned, f.Networks, f.Joined, f.JoinedIface = true, 6, "HomeNet", "wlan0"
		return f
	}
	only := NetFacts{Online: true, Iface: "wlan0"} // Wi-Fi carries the run alone
	wired := NetFacts{Online: true, Iface: "eth0"} // a cable carries the run
	onEth := NetFacts{Online: true, Iface: "eth0"} // the Ethernet check's own port carries the run
	grows := []uint64{10 * kib, 2000 * kib}        // the counters before and after the test
	flat := []uint64{10 * kib, 20 * kib}
	cases := []struct {
		name, id string
		net      NetFacts
		addr     string
		bytes    []uint64 // the counters on each read (the last one sticks); nil: unreadable
		test     *bootwatch.Outcome
		reach    *bootwatch.Outcome // over the run's own connection; nil: no Reach
		want     string
	}{
		{"wifi, scan only", "network.wifi", NetFacts{Scanned: true, Networks: 6}, "", nil, nil, nil, "not_tested/uncertain"},
		{"wifi, joined on a USB dongle", "network.wifi", NetFacts{Scanned: true, Networks: 6, Joined: "HomeNet", JoinedIface: "wlan1"}, "10.0.0.2", grows, &verified, nil, "not_tested/uncertain"},
		{"wifi, verified both ways", "network.wifi", joined(only), "10.0.0.2", grows, &verified, nil, "supported/automatic"},
		{"wifi, verified, but the counters didn't grow", "network.wifi", joined(only), "10.0.0.2", flat, &verified, nil, "not_tested/uncertain"},
		{"wifi, verified, the counters a second late", "network.wifi", joined(only), "10.0.0.2", append(flat, 2000*kib), &verified, nil, "supported/automatic"},
		{"wifi, counters unreadable", "network.wifi", joined(only), "10.0.0.2", nil, &verified, nil, "not_tested/uncertain"},
		{"wifi, no test server", "network.wifi", joined(only), "10.0.0.2", grows, nil, nil, "not_tested/uncertain"},
		{"wifi, a sign-in page", "network.wifi", joined(only), "10.0.0.2", grows, &bootwatch.Outcome{Problem: bootwatch.SignIn, Err: "a sign-in page"}, nil, "not_tested/uncertain"},
		{"wifi, the Mac's clock is off", "network.wifi", joined(only), "10.0.0.2", grows, &bootwatch.Outcome{Problem: bootwatch.Clock, Err: "clock"}, nil, "not_tested/other"},
		{"wifi, boot.watch busy", "network.wifi", joined(only), "10.0.0.2", grows, &bootwatch.Outcome{Reached: true, Problem: bootwatch.Busy, Err: "busy"}, nil, "not_tested/other"},
		{"wifi, wrong bytes after boot.watch answered", "network.wifi", joined(only), "10.0.0.2", grows, &wrong, nil, "partial/automatic"},
		{"wifi, dead, boot.watch answers over the cable", "network.wifi", joined(wired), "10.0.0.2", grows, &dead, up, "partial/automatic"},
		{"wifi, dead, boot.watch answers nowhere", "network.wifi", joined(wired), "10.0.0.2", grows, &dead, down, "not_tested/other"},
		{"wifi, only link, dead, the site worked over it", "network.wifi", joined(NetFacts{Online: true, Iface: "wlan0", SiteVia: "wlan0"}), "10.0.0.2", grows, &dead, up, "not_tested/other"},
		{"wifi, only link, nothing works", "network.wifi", joined(only), "10.0.0.2", flat, &dead, up, "partial/automatic"},
		{"wifi, dead, then left the network", "network.wifi", joined(only), "", flat, &dead, nil, "not_tested/uncertain"},
		{"ethernet, link only", "network.ethernet", onEth, "", grows, &verified, nil, "not_tested/uncertain"},
		{"ethernet, verified both ways", "network.ethernet", onEth, "10.0.0.3", grows, &verified, nil, "supported/automatic"},
		{"ethernet, not carrying the run, verified", "network.ethernet", NetFacts{Online: true, Iface: "wlan0"}, "10.0.0.3", grows, &verified, nil, "supported/automatic"},
		{"ethernet, wrong bytes", "network.ethernet", onEth, "10.0.0.3", grows, &wrong, nil, "partial/automatic"},
		{"ethernet, dead, boot.watch answers over Wi-Fi", "network.ethernet", NetFacts{Online: true, Iface: "wlan0"}, "10.0.0.3", grows, &dead, up, "partial/automatic"},
		{"ethernet, only link, nothing works", "network.ethernet", onEth, "10.0.0.3", flat, &dead, nil, "not_tested/uncertain"},
		{"ethernet, a sign-in page", "network.ethernet", onEth, "10.0.0.3", grows, &bootwatch.Outcome{Problem: bootwatch.SignIn, Err: "a sign-in page"}, nil, "not_tested/uncertain"},
	}
	defer func(d time.Duration) { counterLag = d }(counterLag)
	counterLag = 40 * time.Millisecond
	for _, c := range cases {
		e := fixtureEnv(t, "imac10,1")
		e.Net = c.net
		e.Net.Addr = func(string) string { return c.addr }
		reads := 0
		e.Net.Bytes = func(string) (uint64, bool) {
			if len(c.bytes) == 0 {
				return 0, false
			}
			reads++
			return c.bytes[min(reads, len(c.bytes))-1], true
		}
		if c.test != nil {
			e.Net.Watch = func(context.Context, string) bootwatch.Outcome { return *c.test }
		}
		if c.reach != nil {
			e.Net.Reach = func(context.Context) bootwatch.Outcome { return *c.reach }
		}
		var r Result
		switch {
		case c.id == "network.ethernet": // the hands-on test saw the link
			p, _ := FindEthernet(e)
			r = EthernetOver(context.Background(), e, p)
		case e.Net.Joined != "": // the hands-on test joined the network on JoinedIface
			w, _ := FindWifi(e)
			r = WifiJoined(context.Background(), e, w, e.Net.JoinedIface, e.Net.Networks)
		default:
			r = wifi(context.Background(), e)[0]
		}
		if r.ID != c.id && (c.id == "network.ethernet" || e.Net.Joined != "") {
			t.Errorf("%s: result for %q", c.name, r.ID)
		}
		got := r.Status + "/" + r.Method
		if r.Status == NotTested {
			got = r.Status + "/" + r.Reason
		}
		if got != c.want {
			t.Errorf("%s: %s (%s), want %s", c.name, got, r.Evidence, c.want)
		}
		if strings.Contains(r.Evidence, "HomeNet") {
			t.Errorf("%s: the network's name in the evidence: %s", c.name, r.Evidence)
		}
	}
}

// The quick pass reports what it sees on Wi-Fi and Ethernet and never passes them, even with a
// network joined on the Network screen and boot.watch answering: the hands-on tests move the
// data and decide.
func TestQuickNetworkNeverPasses(t *testing.T) {
	const kib = 1 << 10
	verified := bootwatch.Outcome{Reached: true, Down: 1344 * kib, DownOK: 1344 * kib, Up: 64 * kib, UpOK: 64 * kib}
	e := fixtureEnv(t, "imac10,1")
	e.Net = NetFacts{Scanned: true, Networks: 6, Joined: "HomeNet", JoinedIface: "wlan0", Online: true, Iface: "wlan0"}
	e.Net.Addr = func(string) string { return "10.0.0.2" }
	reads := uint64(0)
	e.Net.Bytes = func(string) (uint64, bool) { reads += 2000 * kib; return reads, true }
	e.Net.Watch = func(context.Context, string) bootwatch.Outcome {
		t.Error("the quick pass ran the boot.watch test")
		return verified
	}
	for _, r := range append(wifi(context.Background(), e), ethernet(context.Background(), e)...) {
		if r.Status == Supported || r.Status == Partial {
			t.Errorf("%s: %s in the quick pass (%s)", r.ID, r.Status, r.Evidence)
		}
	}
}
