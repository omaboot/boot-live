package checks

import (
	"strings"

	"github.com/doesitomarchy/doesitomarchy/pkg/report"

	"github.com/omaboot/boot-live/internal/i18n"
	"github.com/omaboot/boot-live/internal/identify"
	"github.com/omaboot/boot-live/internal/pace"
)

// Applies guesses whether a criterion applies to this Mac. The catalog decides that from each
// configuration's features and ports, which the snapshot doesn't carry yet (LIVE-PLAN §18: "the
// snapshot will likely gain each configuration's applicable criteria"), so until it does the
// tool decides from what it can see: the identifier, the chassis, the GPUs, the battery, and
// the configuration's parts when it's known. A wrong guess costs little: the server flags an
// item that doesn't apply and never counts it. Criteria it can't guess are left out.
func Applies(id string, e *Env) bool {
	h := e.HW
	fam, major := identify.Model(e.Ident.Identifier)
	laptop := h.Laptop()
	imac := fam == "iMac" || fam == "iMacPro"
	screen := laptop || imac
	chip := e.Ident.Chip
	gpus := h.GPUs()
	has := func(kind string) bool { // a part of this kind in the known configuration, else true
		if e.Cat == nil || e.Ident.Config == "" {
			return true
		}
		cfg := e.Cat.Config(e.Ident.Config)
		if cfg == nil {
			return true
		}
		for _, p := range cfg.Parts {
			if p.Kind == kind {
				return true
			}
		}
		return false
	}
	usbCOnly := fam == "MacBook" && major >= 8 || fam == "MacBookPro" && major >= 13 || fam == "MacBookAir" && major >= 8
	battery := systemBattery(e) != "" || laptop
	switch id {
	case "boot.installer-efi64":
		return h.EFIBits != 32
	case "boot.installer-efi32":
		return h.EFIBits == 32
	case "boot.install", "boot.power-cycle", "power.sleep-wake", "thermal.sensors", "audio.speakers", "audio.headphone",
		"graphics.external-display":
		return true
	case "graphics.integrated", "graphics.discrete":
		for _, g := range gpus {
			if identify.Integrated(g) == (id == "graphics.integrated") {
				return true
			}
		}
		return false
	case "graphics.switching", "graphics.power-down":
		return len(gpus) >= 2
	case "display.native-resolution", "display.brightness":
		return screen
	case "input.keyboard", "input.trackpad", "power.lid":
		return laptop
	case "input.ambient-light-sensor":
		// iMacs have one too (applesmc ALV0/ALV1). The iMac11,2's SMC shows no light keys in
		// probes, but like every iMac it has a camera, so it's checked: a failure there is news.
		// The iMac Pro is unknown (research/findings/ambient-light-sensor.md).
		return laptop || fam == "iMac"
	case "input.keyboard-backlight":
		return laptop && !(fam == "MacBook" && major < 8)
	case "input.force-touch":
		// the mid-2015 15-inch (MacBookPro11,4 and 11,5) has it too
		mid2015 := e.Ident.Identifier == "MacBookPro11,4" || e.Ident.Identifier == "MacBookPro11,5"
		return fam == "MacBook" && major >= 8 || fam == "MacBookPro" && (major >= 12 || mid2015) || fam == "MacBookAir" && major >= 8
	case "input.touch-bar":
		return fam == "MacBookPro" && (chip == "t1" || chip == "t2")
	case "input.touch-id":
		return laptop && (chip == "t1" || chip == "t2")
	case "bridge.touch-bar-camera":
		return fam == "MacBookPro" && chip == "t2"
	case "input.ir-receiver":
		return has("ir") && e.Ident.Config != ""
	case "power.battery-status", "power.charging":
		return battery
	case "audio.microphone":
		return screen
	case "audio.display-out":
		return len(gpus) > 0
	case "camera.builtin":
		return has("camera") && screen
	case "network.wifi":
		return has("wifi")
	case "network.bluetooth":
		return has("bluetooth")
	case "network.ethernet":
		return has("ethernet") && len(ethernetDevices(e)) > 0
	case "ports.usb-a":
		return !usbCOnly
	case "ports.usb-c":
		return usbCOnly || imac && major >= 18 || fam == "iMacPro" || fam == "Macmini" && major >= 8 || fam == "MacPro" && major >= 7
	case "ports.usb-c-charging":
		return laptop && usbCOnly
	case "ports.thunderbolt":
		return has("thunderbolt") && e.Ident.Config != ""
	case "ports.firewire":
		return has("firewire") && e.Ident.Config != ""
	case "ports.sd-card":
		return has("card-reader") && e.Ident.Config != ""
	case "storage.optical":
		// the Mac mini servers came with a second disk instead
		server := strings.Contains(e.Ident.Config, "server")
		return imac && major <= 12 || fam == "MacBookPro" && major <= 9 || fam == "MacBook" && major <= 7 || fam == "Macmini" && major <= 4 && !server || fam == "MacPro" && major <= 5
	case "thermal.fans":
		return !(fam == "MacBook" && major >= 8)
	}
	return false // headset mic, line-in, optical audio, ExpressCard: can't be guessed
}

// Items builds the report's items and extras from the quick pass's results. Every criterion the
// pass didn't decide and that applies goes in as not_tested: reason live-limit where a live boot
// can't decide it (live: no, or t2 on a Mac without a T2 chip) and no fix that took effect lifts
// that, otherwise not-in-profile (S2's hands-on tests).
func Items(e *Env, rs []Result) (map[string]report.FileItem, []report.FileExtra) {
	items := map[string]report.FileItem{}
	var extras []report.FileExtra
	known := map[string]bool{}
	if e.Cat != nil {
		for _, c := range e.Cat.Snap.Capabilities {
			known[c.ID] = true
		}
	}
	for _, r := range rs {
		// a result under an ID the catalog doesn't know would get the whole report rejected:
		// it goes in as an extra instead
		if r.Extra || e.Cat != nil && !known[r.ID] {
			extras = append(extras, report.FileExtra{ID: r.ID, Label: r.Label, Status: r.Status, Detail: r.Evidence})
			continue
		}
		it := report.FileItem{Status: r.Status, Reason: r.Reason, Evidence: r.Evidence}
		if r.Status != NotTested {
			it.Method = r.Method
			if it.Method == "" {
				it.Method = Automatic
			}
		}
		if r.FixID != "" {
			it.Note = "ran with OmaBoot? " + r.FixID
		}
		items[r.ID] = it
	}
	if e.Cat == nil {
		return items, extras
	}
	for _, c := range e.Cat.Snap.Capabilities {
		if c.Retired || !Applies(c.ID, e) {
			continue
		}
		if _, done := items[c.ID]; done {
			continue
		}
		reason := NotInProfile
		if liveLimited(e, c) {
			reason = LiveLimit
		}
		items[c.ID] = report.FileItem{Status: NotTested, Reason: reason}
	}
	return items, extras
}

// liveLimited: a live boot can't decide the criterion on this Mac (live: no; t2 without a T2
// chip; kernel without the install's kernel), and no fix that took effect lifts that.
func liveLimited(e *Env, c report.SnapshotCriterion) bool {
	return (c.Live == report.LiveNo || c.Live == report.LiveT2 && e.Ident.Chip != "t2" || c.Live == LiveKernel && !e.installKernel()) && e.lifted(c.ID) == nil
}

// capability is the catalog's criterion with this ID, or nil.
func capability(e *Env, id string) *report.SnapshotCriterion {
	if e.Cat == nil {
		return nil
	}
	for i := range e.Cat.Snap.Capabilities {
		if c := &e.Cat.Snap.Capabilities[i]; c.ID == id {
			return c
		}
	}
	return nil
}

// Known reports whether the catalog has the criterion and still tests it (a result under any
// other ID would go in as an extra). Without a catalog everything counts as known.
func Known(e *Env, id string) bool {
	if e.Cat == nil {
		return true
	}
	c := capability(e, id)
	return c != nil && !c.Retired
}

// LiveLimited reports whether a live boot can't decide the criterion on this Mac: the report
// then carries it as not_tested (live-limit), whatever a test found (the hands-on tests aren't
// offered for it).
func LiveLimited(e *Env, id string) bool {
	c := capability(e, id)
	return c != nil && liveLimited(e, *c)
}

// Group is one criteria category on Quick results: each applicable criterion's result.
type Group struct {
	Name  string // translated
	Items []pace.Status
	IDs   []string // each item's criterion
}

// Category names, as the site shows them.
var categories = map[string]string{
	"boot": i18n.M("Boot"), "graphics": i18n.M("Graphics"), "display": i18n.M("Display"), "input": i18n.M("Input"),
	"power": i18n.M("Power"), "audio": i18n.M("Audio"), "camera": i18n.M("Camera"), "network": i18n.M("Networking"),
	"ports": i18n.M("Ports"), "storage": i18n.M("Storage"), "thermal": i18n.M("Thermal"), "bridge": i18n.M("Bridge"),
}

// CategoryOrder is the categories' ids in the catalog's order.
var CategoryOrder = []string{"boot", "graphics", "display", "input", "power", "audio", "camera", "network", "ports", "storage", "thermal", "bridge"}

// Category is a criterion's category: its id ("power") and name (a msgid, "Power"); the id is
// the criterion's prefix ("power.charging"; "graphics" alone is its own).
func Category(criterion string) (id, name string) {
	id, _, _ = strings.Cut(criterion, ".")
	return id, categories[id]
}

// Groups are the report's criteria by category, in the catalog's order.
func Groups(e *Env, items map[string]report.FileItem) []Group {
	var out []Group
	idx := map[string]int{}
	for _, c := range e.Cat.Snap.Capabilities {
		it, ok := items[c.ID]
		if !ok {
			continue
		}
		cat := c.Category
		if cat == "" {
			cat, _, _ = strings.Cut(c.ID, ".")
		}
		i, seen := idx[cat]
		if !seen {
			name := categories[cat]
			if name == "" {
				name = cat
			}
			out = append(out, Group{Name: i18n.S(name)})
			i = len(out) - 1
			idx[cat] = i
		}
		out[i].Items = append(out[i].Items, Result{Status: it.Status, Reason: it.Reason}.Pace())
		out[i].IDs = append(out[i].IDs, c.ID)
	}
	return out
}
