package main

import (
	"strings"

	"github.com/omaboot/boot-live/internal/demo"
	"github.com/omaboot/boot-live/internal/handson"
	"github.com/omaboot/boot-live/internal/i18n"
	"github.com/omaboot/boot-live/internal/identify"
	"github.com/omaboot/boot-live/internal/keyboard"
	"github.com/omaboot/boot-live/internal/plan"
	"github.com/omaboot/boot-live/internal/ui"
)

// whichMacShots are Which Mac's search and its two-configurations case (0.7.9): a MacBook Air
// [MacBookAir7,2] with the build-to-order Core i7 is the Early 2015 or the 2017, which share its
// board ID, CPU and GPU.
func whichMacShots() []shot {
	two := twoFitFacts()
	return []shot{
		{name: "2-whichmac-search", st: st(ui.WhichMac, 0.9), f: demo.Facts()},
		{name: "2-whichmac-two-fit", st: st(ui.WhichMac, 6), f: two},
		{name: "2-whichmac-two-fit-second", st: st(ui.WhichMac, 6, func(s *ui.State) { s.PickSel = 1 }), f: two},
		{name: "2-whichmac-two-fit-all", st: st(ui.WhichMac, 6, func(s *ui.State) { s.Picking, s.PickSel = true, 0 }), f: two},
		{name: "2-whichmac-two-fit-picked", st: st(ui.WhichMac, 6, func(s *ui.State) { s.Picked, s.PickedID = true, two.Candidates[1].ID }), f: picked(two)},
		// the run plan on the stick: counting down, ignored, waiting for plan run, with problems
		{name: "1-hello-plan", st: st(ui.Hello, 3.4), f: planFacts(planSample)},
		{name: "1-hello-plan-ignored", st: st(ui.Hello, 8, func(s *ui.State) { s.PlanOff = true }), f: planFacts(planSample)},
		{name: "1-hello-plan-console", st: st(ui.Hello, 12, func(s *ui.State) { s.PlanWait = true }), f: planFacts(strings.Replace(planSample, "results: both,", "results: both, start: console,", 1))},
		{name: "1-hello-plan-problems", st: st(ui.Hello, 8), f: planFacts("version: 1\nmacs:\n  - match: MacBookAir5,2\n    tests: {power.lid: setup, colour: red}\n")},
		// the keyboard layout: macOS's last one, the picker (main list, the others), one that didn't load
		{name: "1-hello-keyboard", st: st(ui.Hello, 8), f: kbdFacts(uk)},
		{name: "1-hello-keyboard-picker", st: st(ui.Hello, 8, func(s *ui.State) { s.Kbd, s.KbdSel = true, 1 }), f: kbdFacts(uk)},
		{name: "1-hello-keyboard-other", st: st(ui.Hello, 8, func(s *ui.State) { s.Kbd, s.KbdOther, s.KbdSel = true, true, 3 }), f: kbdFacts(uk)},
		{name: "1-hello-keyboard-failed", st: st(ui.Hello, 8), f: kbdFacts(failed)},
	}
}

// The keyboard layouts the Hello shots show: English (UK) from macOS, and German picked but not
// loaded.
var (
	uk     = keyboard.Choice{Layout: mustLayout("uk"), How: keyboard.FromMacOS, NVRAM: "en-GB:2", Loaded: true}
	failed = keyboard.Choice{Layout: mustLayout("uk"), How: keyboard.FromMacOS, NVRAM: "en-GB:2", Loaded: true,
		Tried: mustLayout("de"), Failed: "German (keymap de) didn't load: loadkeys de: cannot open file de"}
)

func mustLayout(km string) keyboard.Layout {
	l, ok := keyboard.ByKeymap(km)
	if !ok {
		panic("no layout " + km)
	}
	return l
}

// kbdFacts are the demo's facts with another keyboard layout.
func kbdFacts(c keyboard.Choice) *ui.Facts {
	f := demo.Facts()
	f.Keyboard = c
	return f
}

// planSample is a run plan for the demo's MacBook Air: unattended, Ethernet, three setup tests.
const planSample = `version: 1
defaults: {network: ethernet, results: both, tests: {default: skip}}
macs:
  - match: MacBookAir5,2
    tests: {network.ethernet: setup, ports.sd-card: setup, network.wifi: setup}
`

// planFacts are the demo's facts with the run plan src on the stick.
func planFacts(src string) *ui.Facts {
	f := demo.Facts()
	p, probs := plan.Parse([]byte(src), plan.Known{Test: func(id string) bool { _, ok := handson.Lookup(id); return ok }})
	pf := &ui.PlanFacts{File: plan.File}
	for _, x := range probs {
		pf.Problems = append(pf.Problems, x.String())
	}
	if p != nil {
		if r, ok := p.For("MacBookAir5,2"); ok {
			pf.Run = &r
		}
	}
	f.Plan = pf
	return f
}

// picked is that Mac once the 2017 was picked from the list.
func picked(f *ui.Facts) *ui.Facts {
	c := *f
	c.MatchNote = i18n.T("Picked from the list.")
	return &c
}

// twoFitFacts are the demo's facts on that MacBook Air, matched against the snapshot built in,
// its lists made as the real mode makes them.
func twoFitFacts() *ui.Facts {
	f := demo.Facts()
	cat, err := identify.Embedded()
	must(err)
	h := identify.Hardware{ProductName: "MacBookAir7,2", BoardID: "Mac-937CB26E2E02BB01", CPU: "Intel(R) Core(TM) i7-5650U CPU @ 2.20GHz", MemMiB: 8192,
		PCI: []identify.PCIDev{{Slot: "0000:00:02.0", Class: "030000", Vendor: "8086", Device: "1626", Driver: "i915", BootVGA: true},
			{Slot: "0000:03:00.0", Class: "028000", Vendor: "14e4", Device: "43a0", Driver: "bcma-pci-bridge"}}}
	r := cat.Identify(h)
	v := cat.View(h, r)
	f.View, f.MatchNote = &v, ""
	f.Mac = ui.MacInfo{Name: r.Name, ID: r.Identifier, Config: "Core i7 2.2 GHz, 8 GB"}
	f.Parts = "Core i7 2.2 GHz  ·  8086:1626 i915  ·  8 GB"
	notSure := ui.Choice{Label: i18n.T("Not sure"), Note: i18n.T("sent without a configuration: a reviewer matches it")}
	var ids []string
	for _, c := range r.Candidates {
		ids = append(ids, c.Config)
	}
	f.Candidates = nil
	for i, l := range cat.Labels(ids) {
		note := i18n.T("fits every part it reports")
		if nr := r.Candidates[i].NotReported; len(nr) > 0 {
			note = i18n.T("not seen: %s", strings.Join(nr, ", "))
		}
		f.Candidates = append(f.Candidates, ui.Choice{ID: ids[i], Label: l, Note: note + "  ·  " + ids[i]})
	}
	f.Candidates = append(f.Candidates, notSure)
	all := cat.ConfigIDs(r.Identifier)
	f.Configs = nil
	for i, l := range cat.Labels(all) {
		f.Configs = append(f.Configs, ui.Choice{ID: all[i], Label: l, Note: all[i]})
	}
	f.Configs = append(f.Configs, notSure)
	return f
}
