package demo

import (
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/omaboot/boot-live/internal/checks"
	"github.com/omaboot/boot-live/internal/command"
	"github.com/omaboot/boot-live/internal/i18n"
	"github.com/omaboot/boot-live/internal/identify"
	"github.com/omaboot/boot-live/internal/pace"
	"github.com/omaboot/boot-live/internal/send"
	"github.com/omaboot/boot-live/internal/ui"
)

// The console's commands on the demo's Mac: made-up IDs and kernel log,
// and a report made up from the demo's results.

// IDs are the demo Mac's hardware IDs.
func (b *Backend) IDs() []command.ID {
	return []command.ID{{Key: "model", ID: MacID, Name: MacName}, {Key: "board", ID: BoardID},
		{Key: "cpu", ID: "i5-3427U", Name: "Intel Core i5 @ 1.80GHz"}, {Key: "gpu", ID: "8086:0166", Name: "HD Graphics 4000 i915"},
		{Key: "wifi", ID: "14e4:4353", Name: "BCM43224 bcma-pci-bridge"}, {Key: "audio", ID: "8086:1e20", Name: "Cirrus CS4206 snd_hda_intel"}}
}

// Kmsg is the demo Mac's kernel log.
var Kmsg = []string{
	"[    0.000] Linux version 7.2.5-1-omarchy (linux-omarchy@archlinux) #1 SMP PREEMPT_DYNAMIC",
	"[    0.412] i915 0000:00:02.0: vgaarb: deactivate vga console",
	"[    1.207] [drm] Initialized i915 1.6.0 for 0000:00:02.0 on minor 0",
	"[    1.733] brcmsmac bcma0:1: mfg 4bf core 812 rev 17 class 0 irq 17",
	"[    2.018] snd_hda_codec_cirrus hdaudioC0D0: CS4206: line_outs=2",
	"[    2.540] applesmc: key=561 fan=1 temp=29 index=28 acc=0 lux=2 kbd=1",
	"[    2.541] applesmc: patched module from updates/ loaded",
	"[    3.104] applesmc: warning: light sensor read 0 once, retrying",
	"[   12.555] usb 1-1: device descriptor read/64, error -71",
	"[   41.902] i915 0000:00:02.0: [drm] fb0: i915drmfb frame buffer device",
	"[   42.110] mesa: GLES 3.2 on HD Graphics 4000 (IVB GT2)",
	"[   42.300] omaboot: donuts 1912 fps, 4.0 s",
	"[   43.004] i915: GPU frequency 350 → 1150 MHz",
}

// KernelLog is the demo's kernel log.
func (b *Backend) KernelLog() []string { return Kmsg }

// ReportJSON is a report made up from the demo's results: the real one's shape, cut short.
func (b *Backend) ReportJSON(handle, _ string) ([]byte, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	type item struct {
		Status   string `json:"status"`
		Method   string `json:"method,omitempty"`
		Evidence string `json:"evidence,omitempty"`
	}
	items := map[string]item{}
	for _, c := range Checks {
		st, method := "not_tested", ""
		switch c.Status {
		case pace.Pass:
			st, method = checks.Supported, checks.Automatic
		case pace.Partial:
			st, method = checks.Partial, checks.Automatic
		case pace.Fail:
			st, method = checks.Failed, checks.Automatic
		}
		if s, ok := b.forced[c.ID]; ok {
			st = s
		}
		items[c.ID] = item{Status: st, Method: method, Evidence: c.Detail}
	}
	return json.MarshalIndent(map[string]any{
		"schema": 1, "identifier": MacID, "config": "MacBookAir5,2-i5-4gb", "context": "live",
		"source": map[string]string{"id": send.SourceID, "version": "demo", "profile": "quick"},
		"tester": map[string]string{"handle": handle}, "notes": strings.Join(b.notes, " "),
		"console": b.console, "items": items,
	}, "", "  ")
}

// Note keeps the note for the made-up report.
func (b *Backend) Note(text string) {
	b.mu.Lock()
	b.notes = append(b.notes, text)
	b.mu.Unlock()
}

// Console keeps the command for the made-up report.
func (b *Backend) Console(cmd, kind string) {
	b.mu.Lock()
	b.console = append(b.console, map[string]string{"command": cmd, "kind": kind})
	b.mu.Unlock()
}

// Target: the demo pretends to send to DoesItOmarchy.com.
func (b *Backend) Target() command.Target {
	return command.Target{URL: "https://doesitomarchy.com", Shown: Site, Production: true, CanSubmit: true}
}

// SendBug pretends to send to OmaBoot?.
func (b *Backend) SendBug(name string) {
	if name == "" {
		name = "results/bugreport-" + MacID + "-" + time.Now().UTC().Format("20060102T150405Z") + ".tar"
	}
	after(1200*time.Millisecond, func() { b.post(ui.BugSentMsg{Path: name, Code: "B-7K2Q-9MXD"}) })
}

// Bugreport pretends to save to the stick.
func (b *Backend) Bugreport(ui.BugFiles) {
	after(900*time.Millisecond, func() {
		b.post(ui.BugMsg{Path: "results/bugreport-" + MacID + "-" + time.Now().UTC().Format("20060102T150405Z") + ".tar", Bytes: 1887436})
	})
}

// TestDrives: the demo's made-up drives.
func (b *Backend) TestDrives() []command.TestDrive {
	return []command.TestDrive{
		{Dev: "sdc1", What: "SanDisk Ultra  ·  FAT  ·  15 GB", OK: true},
		{Dev: "sdb3", What: "the OmaBoot? drive's OMABOOT partition  ·  FAT  ·  67 MB", OK: true},
		{Dev: "sdd2", What: "WD My Passport  ·  HFS+  ·  1000 GB", Why: i18n.T("Linux writes HFS+ only with its journal off")},
		{Dev: "sda", What: "APPLE SSD SM128E", Why: i18n.T("internal: read-only for the whole run")},
	}
}

// WriteTestFile pretends to write and read back.
func (b *Backend) WriteTestFile(dev string) {
	after(2100*time.Millisecond, func() {
		b.post(ui.TestFileMsg{Dev: dev, Where: dev + ": OMABOOT-TEST.BIN (FAT32, MBR partition 1)", Took: 2100 * time.Millisecond})
	})
}

// Rerun measures a demo check again: the same result a moment later, the first run in its evidence.
func (b *Backend) Rerun(id string) (string, error) {
	for _, c := range Checks {
		if c.ID != id {
			continue
		}
		first := checks.Result{ID: c.ID, Status: map[pace.Status]string{pace.Pass: checks.Supported, pace.Partial: checks.Partial, pace.Fail: checks.Failed}[c.Status], Evidence: c.Detail}
		if first.Status == "" {
			first.Status = checks.NotTested
		}
		took := max(1.5, c.Finish-c.Begin)
		again := c
		again.Evidence = checks.AgainEvidence(c.Detail, checks.Again(first, c.Finish-c.Begin))
		after(time.Duration(took*float64(time.Second)), func() {
			b.mu.Lock()
			delete(b.forced, id)
			b.mu.Unlock()
			b.post(ui.RerunMsg{ID: id, Checks: []ui.Check{again}})
			b.post(b.summary())
		})
		return i18n.T("about %s", ui.Duration(time.Duration(took*float64(time.Second)))), nil
	}
	return "", errors.New(i18n.T("%s isn't a quick-pass check on this Mac", id))
}

// Force keeps a result set by hand for the made-up report, and brings the summary's groups up to
// date with it.
func (b *Backend) Force(id, status string) error {
	s := map[string]string{command.Pass: checks.Supported, command.Partial: checks.Partial, command.Fail: checks.Failed, command.Skip: checks.NotTested}[status]
	b.mu.Lock()
	if b.forced == nil {
		b.forced = map[string]string{}
	}
	b.forced[id] = s
	b.mu.Unlock()
	b.post(b.summary())
	return nil
}

// demoCatalog is the snapshot in the image with the demo Mac identified in it.
var demoCatalog = sync.OnceValues(func() (*identify.Catalog, identify.Result) {
	cat, err := identify.Embedded()
	if err != nil {
		return nil, identify.Result{}
	}
	pci := func(id string) identify.PCIDev { return identify.PCIDev{Vendor: id[:4], Device: id[5:]} }
	return cat, cat.Identify(identify.Hardware{ProductName: MacID, BoardID: BoardID, CPU: "Intel(R) Core(TM) i5-3427U CPU @ 1.80GHz",
		PCI: []identify.PCIDev{pci("8086:0166"), pci("14e4:4353"), pci("8086:1e20")}})
})

// Catalog is the image's snapshot, the demo Mac matched in it, the applesmc fix in effect.
func (b *Backend) Catalog() command.Catalog {
	cat, r := demoCatalog()
	c := ui.CatalogOf(cat, r, r.Best(), []string{"omaboot.applesmc-led-container-of"})
	c.Site, c.Bundled = "DoesItOmarchy.com", true
	return c
}
