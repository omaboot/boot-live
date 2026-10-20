// Package demo is the scripted run that --demo plays: the prototype's MacBook Air (13-inch,
// Mid 2012) [MacBookAir5,2] with made-up results. Prose is marked for the catalog with i18n.M
// and translated where it is drawn.
package demo

import (
	"time"

	"github.com/omaboot/boot-live/internal/boot"
	"github.com/omaboot/boot-live/internal/i18n"
	"github.com/omaboot/boot-live/internal/identify"
	"github.com/omaboot/boot-live/internal/pace"
	"github.com/omaboot/boot-live/internal/safety"
	"github.com/omaboot/boot-live/internal/ui"
)

// The identified Mac.
const (
	MacName   = "MacBook Air (13-inch, Mid 2012)"
	MacID     = "MacBookAir5,2"
	MacConfig = "Core i5 1.8 GHz, 4 GB"
	MacParts  = "Core i5 1.8 GHz · HD Graphics 4000 · 4 GB"
)

// Facts shown while starting up.
const (
	Omarchy        = "4.0.4"
	Disks          = "sda"
	DiskCount      = 1
	SnapshotDate   = "2026-10-08"
	SnapshotConfs  = 371
	RefreshedDate  = "2026-10-10"
	RefreshedConfs = 372
	WifiChip       = "BCM43224"
	SSID           = "maple-house-5G"
	IP             = "192.168.1.47"
	Band           = "5 GHz"
	Report         = "428e03024e" // a report code, as the API returns it; the site's page is /report/{code}
	Site           = "DoesItOmarchy.com"
	BoardID        = "Mac-2E6FAB96566FE58C"
	ReportKB       = 6
	FinishedIn     = "4 min 36 s"
	QuickPassIn    = "2 min 41 s"
	TimeScale      = 6 // the demo's quick pass is compressed: "time left" counts as if it were six times longer
)

// Networks in range, strongest first.
var Networks = []ui.WifiNet{{SSID: "maple-house", Bars: 4, Security: "WPA2"}, {SSID: "maple-house-5G", Bars: 4, Security: "WPA2"},
	{SSID: "TELUS2381", Bars: 3, Security: "WPA2"}, {SSID: "BELL442", Bars: 2, Security: "WPA2"},
	{SSID: "DIRECT-4F-HP M227", Bars: 1, Security: "WPA2"}, {SSID: "Cafe-Guest", Bars: 1, Security: i18n.M("open")}}

// Checks of the quick pass, in the order they finish, with when they started (thermal.fans comes
// from the thermal check, with the sensors).
var Checks = []ui.Check{
	{ID: "boot.installer-efi64", Label: i18n.M("Installer boots (64-bit EFI)"), Detail: i18n.M("booted via GRUB, linux-t2"),
		Effect: boot.PCI, Params: boot.Params{Items: []string{"00:00.0", "00:02.0", "00:14.0", "00:16.0", "00:1b.0", "00:1f.2", "02:00.0", "03:00.0"}},
		Begin: 0, Finish: 0.1, Status: pace.Pass, Say: i18n.M("we booted. that already counts for something.")},
	{ID: "boot.install", Label: i18n.M("Installation completes"), Detail: i18n.M("a live boot doesn't install"),
		Begin: 0.1, Finish: 0.15, Status: pace.Live},
	{ID: "boot.power-cycle", Label: i18n.M("Survives a power cycle"), Detail: i18n.M("a live boot can't restart an installed system"),
		Begin: 0.15, Finish: 0.2, Status: pace.Live},
	{ID: "display.native-resolution", Label: i18n.M("Internal display, native resolution"), Detail: i18n.M("eDP-1, 1440×900 preferred mode"),
		Effect: boot.Bars, Params: boot.Params{Label: "1440 × 900"},
		Begin: 0.2, Finish: 0.3, Status: pace.Pass, Say: i18n.M("1440×900. every pixel accounted for.")},
	{ID: "graphics.integrated", Label: i18n.M("Integrated GPU acceleration"), Detail: i18n.M("HD Graphics 4000, i915, GLES 3.2"),
		Effect: boot.Torus, Begin: 0.3, Finish: 2.9, Status: pace.Pass, Say: i18n.M("HD 4000 on i915. GLES 3.2. not bad for 2012.")},
	{ID: "power.battery-status", Label: i18n.M("Battery status"), Detail: i18n.M("BAT0 87%, 412 cycles"),
		Effect: boot.Battery, Params: boot.Params{Level: 0.87},
		Begin: 2.9, Finish: 3.0, Status: pace.Pass, Say: i18n.M("battery reports in. 412 cycles and counting.")},
	{ID: "audio.codec", Label: i18n.M("Audio codec found, driver bound"), Detail: i18n.M("Cirrus CS4206, snd_hda_intel bound"),
		Effect: boot.Wave, Begin: 3.0, Finish: 3.4, Status: pace.Pass, Say: i18n.M("CS4206 on snd_hda_intel. listening tests come later.")},
	{ID: "network.wifi", Label: i18n.M("Wi-Fi"), Detail: i18n.M("BCM43224, brcmsmac, joined; the hands-on test decides"),
		Effect: boot.Radar, Params: boot.Params{Count: 6},
		Begin: 3.4, Finish: 5.8, Status: pace.Skip, Say: i18n.M("BCM43224 on brcmsmac hears 6 networks. the hands-on test moves the data.")},
	{ID: "thermal.sensors", Label: i18n.M("Temperature sensors under load"), Detail: i18n.M("coretemp, 4 sensors, peak 78 °C"),
		Effect: boot.Heat, Params: boot.Params{From: 41, Peak: 78},
		Begin: 5.8, Finish: 18.0, Status: pace.Pass, Say: i18n.M("warming the CPU. 78 °C peak. watchdog is calm.")},
	{ID: "thermal.fans", Label: i18n.M("Fans respond to load"), Detail: i18n.M("applesmc, 1 fan, 2000 to 4100 rpm"), Fix: "applesmc",
		Begin: 5.8, Finish: 18.02, Status: pace.Pass, Say: i18n.M("the applesmc fix is on: the fans answer.")},
	{ID: "input.keyboard-backlight", Label: i18n.M("Keyboard backlight"), Detail: i18n.M("smc::kbd_backlight present; the hands-on test decides"),
		Begin: 18.02, Finish: 18.03, Status: pace.Skip},
	{ID: "input.ambient-light-sensor", Label: i18n.M("Ambient light sensor"), Detail: i18n.M("reads 38; the hands-on test decides"),
		Begin: 18.03, Finish: 18.04, Status: pace.Skip},
}

// Criteria are the MacBookAir5,2's criteria by category, in the catalog's order, with the demo's
// quick-pass result for each (Skip: not in this run). Audio's pass is the codec check.
var Criteria = []struct {
	Group, ID string
	Status    pace.Status
}{
	{i18n.M("Boot"), "boot.installer-efi64", pace.Pass}, {i18n.M("Boot"), "boot.install", pace.Live}, {i18n.M("Boot"), "boot.power-cycle", pace.Live},
	{i18n.M("Graphics"), "graphics.integrated", pace.Pass}, {i18n.M("Graphics"), "graphics.external-display", pace.Skip},
	{i18n.M("Display"), "display.native-resolution", pace.Pass}, {i18n.M("Display"), "display.brightness", pace.Skip},
	{i18n.M("Input"), "input.keyboard", pace.Skip}, {i18n.M("Input"), "input.trackpad", pace.Skip},
	{i18n.M("Input"), "input.keyboard-backlight", pace.Skip}, {i18n.M("Input"), "input.ambient-light-sensor", pace.Skip},
	{i18n.M("Power"), "power.sleep-wake", pace.Skip}, {i18n.M("Power"), "power.battery-status", pace.Pass},
	{i18n.M("Power"), "power.charging", pace.Skip}, {i18n.M("Power"), "power.lid", pace.Skip},
	{i18n.M("Audio"), "audio.speakers", pace.Pass}, {i18n.M("Audio"), "audio.microphone", pace.Skip},
	{i18n.M("Audio"), "audio.headphone", pace.Skip}, {i18n.M("Audio"), "audio.display-out", pace.Skip},
	{i18n.M("Camera"), "camera.builtin", pace.Skip},
	{i18n.M("Networking"), "network.wifi", pace.Skip}, {i18n.M("Networking"), "network.bluetooth", pace.Skip},
	{i18n.M("Ports"), "ports.usb-a", pace.Skip}, {i18n.M("Ports"), "ports.thunderbolt", pace.Skip}, {i18n.M("Ports"), "ports.sd-card", pace.Skip},
	{i18n.M("Thermal"), "thermal.sensors", pace.Pass}, {i18n.M("Thermal"), "thermal.fans", pace.Pass},
}

// GroupsWith are the criteria by category, with these results in place of the quick pass's.
func GroupsWith(over map[string]pace.Status) []ui.Group {
	var out []ui.Group
	for _, c := range Criteria {
		st := c.Status
		if s, ok := over[c.ID]; ok {
			st = s
		}
		if len(out) == 0 || out[len(out)-1].Name != c.Group {
			out = append(out, ui.Group{Name: c.Group})
		}
		out[len(out)-1].Items = append(out[len(out)-1].Items, st)
		out[len(out)-1].IDs = append(out[len(out)-1].IDs, c.ID)
	}
	return out
}

// Groups are the quick pass's groups.
var Groups = GroupsWith(nil)

// Hardware is what the demo's Mac reports to identification: its board ID, CPU, GPU and Wi-Fi.
func Hardware() identify.Hardware {
	return identify.Hardware{ProductName: MacID, BoardID: BoardID, CPU: "Intel(R) Core(TM) i5-3427U CPU @ 1.80GHz", MemMiB: 3812,
		PCI: []identify.PCIDev{{Slot: "0000:00:02.0", Class: "030000", Vendor: "8086", Device: "0166", Driver: "i915", BootVGA: true},
			{Slot: "0000:02:00.0", Class: "028000", Vendor: "14e4", Device: "4353", Driver: "bcma-pci-bridge"}}}
}

// View is Which Mac's view of the demo's Mac, matched against the snapshot built in.
func View() *identify.View {
	cat, err := identify.Embedded()
	if err != nil {
		return nil
	}
	h := Hardware()
	v := cat.View(h, cat.Identify(h))
	return &v
}

// Facts are the demo's run for the screens, as it starts: the results come from the backend.
func Facts() *ui.Facts {
	return &ui.Facts{
		View:      View(),
		Demo:      true,
		Mac:       ui.MacInfo{Name: MacName, ID: MacID, Config: MacConfig},
		Known:     true,
		Parts:     MacParts,
		Omarchy:   Omarchy,
		DisksLine: i18n.N("%d internal disk set read-only (%s)", "%d internal disks set read-only (%s)", DiskCount, DiskCount, Disks),
		// the MacBook Air's SSD as Hello draws it (prototype v54)
		Disk: &safety.Layout{Name: "sda", Model: "APPLE SSD SM128E", Bytes: 121332826112, Parts: []safety.Part{
			{Name: "sda1", Label: "EFI System Partition", Bytes: 209715200},
			{Name: "sda2", Label: "Macintosh HD", Bytes: 120473067520},
			{Name: "sda3", Label: "Recovery HD", Bytes: 650002432}}},
		Written:      func() int64 { return 0 },
		SystemLine:   "Omarchy " + Omarchy + " · " + i18n.T("%s (the kernel an install gives this Mac)", "linux-omarchy 7.2.5-1") + " · Mesa 26.2.2 · " + i18n.T("64-bit EFI"),
		ImageLine:    i18n.T("image %s", Image) + " · " + i18n.T("fixes: %s", "applesmc"),
		Image:        Image,
		SnapshotLine: i18n.T("catalog snapshot %s, %d configurations", SnapshotDate+" (51bdb7c5)", SnapshotConfs),
		MatchNote:    i18n.T("The only match in the catalog (board ID %s). When several match, you pick yours from a list.", BoardID),
		Site:         Site,
		Production:   true,
		CanSubmit:    true,
		FixNames:     []string{"applesmc"},
		FinishedIn:   FinishedIn,
		TimeScale:    TimeScale,
	}
}

// Summary is what the backend sends when the demo's quick pass is done.
func Summary() ui.QuickDoneMsg {
	return ui.QuickDoneMsg{Groups: Groups, Headline: i18n.T("This Mac boots Omarchy."),
		AnswerSay: i18n.T("the applesmc fix got the SMC talking: fans, glow, light sensor. the ▲ ones need Omarchy installed."),
		DoneWord:  i18n.T("Yay!"), QuickPassIn: QuickPassIn, ReportKB: ReportKB, Startup: Startup,
		TestedOn: "Omarchy " + Omarchy + " · linux-omarchy 7.2.5-1 · Mesa 26.2.2, HD Graphics 4000", StartedIn: ui.Duration(41 * time.Second),
		BootLine: i18n.T("Booted in %s: %s", ui.Duration(41*time.Second), "firmware 1 s, loader 2 s, kernel 3 s, userspace 35 s") + " · " +
			i18n.T("boot-live drew at %s", ui.Duration(43*time.Second)) + " · " + i18n.T("checks at %s", ui.Duration(58*time.Second)) + " · " + i18n.T("drive %.1f MB/s", 31.4)}
}

// Image is the demo's image name, with its build date.
const Image = "omaboot-live dev a79585d (2026-10-12)"

// Startup is the full log's startup section, as systemd reports it on a Mac.
var Startup = []string{
	"OmaBoot? Live started 41s after the kernel",
	"Startup finished in 1.2s (firmware) + 2.1s (loader) + 3.4s (kernel) + 34.6s (userspace) = 41.3s",
	"slow: 12.8s omaboot-live-hook.service",
	"slow: 6.1s systemd-udev-settle.service",
	"slow: 2.3s iwd.service",
}

// Finished is the demo's facts after the quick pass, for drawing the later screens directly.
func Finished() *ui.Facts {
	f := Facts()
	f.Checks = append([]ui.Check(nil), Checks...)
	s := Summary()
	f.Groups, f.Headline, f.AnswerSay, f.DoneWord, f.QuickPassIn, f.ReportKB, f.Startup = s.Groups, s.Headline, s.AnswerSay, s.DoneWord, s.QuickPassIn, s.ReportKB, s.Startup
	f.TestedOn, f.StartedIn, f.BootLine, f.SlowDrive = s.TestedOn, s.StartedIn, s.BootLine, s.SlowDrive
	return f
}
