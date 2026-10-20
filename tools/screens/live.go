package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/omaboot/boot-live/internal/app"
	"github.com/omaboot/boot-live/internal/checks"
	"github.com/omaboot/boot-live/internal/network"
	"github.com/omaboot/boot-live/internal/send"
	"github.com/omaboot/boot-live/internal/sysroot"
	"github.com/omaboot/boot-live/internal/ui"
)

// liveShots runs the real mode's setup and quick pass on a fixture tree (nothing is written)
// and returns its screens at chosen moments. The network step's states are set by hand: a
// fixture has no iwd to talk to.
func liveShots(root string) []shot {
	r := app.New(app.Options{Root: sysroot.Root(root), Edition: ui.Live, Target: send.Target{URL: "http://localhost:8080", Key: "doi_example"},
		Load: time.Millisecond, Version: "0.4.0 dev"})
	defer r.Close()
	r.Setup()
	if r.Fatal != nil {
		fmt.Fprintln(os.Stderr, "screens:", r.Fatal)
		os.Exit(1)
	}
	o := ui.Options{Edition: ui.Live}
	start := r.Facts(&o)
	start.Static = app.NewBackend(r).StaticTarget()
	f := *start
	f.Checks = nil
	r.Quick(func(x checks.Result) {
		f.Checks = append(f.Checks, ui.Check{ID: x.ID, Label: x.Label, Detail: x.Detail, Fix: x.Fix, Effect: x.Effect,
			Params: x.Params, Begin: x.Begin, Finish: x.Finish, Status: x.Pace(), Say: x.Say})
	})
	sum := r.Summary()
	f.Groups, f.Headline, f.AnswerSay, f.DoneWord = sum.Groups, sum.Headline, sum.AnswerSay, sum.DoneWord
	f.QuickPassIn, f.ReportKB, f.FinishedIn = sum.QuickPassIn, sum.ReportKB, "3 min 12 s"
	p := ui.NewPlayer(f.Checks, len(f.Checks))

	nokey := f
	nokey.CanSubmit, nokey.NoSubmit = false, "no report key in this image: save the report to external storage"
	saved := f
	saved.SavedFile = send.FileName(f.Mac.ID, "", time.Date(2026, 10, 10, 7, 41, 0, 0, time.UTC))
	sent := f
	sent.Report = "428e03024e"
	sent.ReportPage = r.Site() + "/report/" + sent.Report

	var nets []ui.WifiNet
	if w, err := network.NewWifi(sysroot.Root(root)); err == nil {
		aps, _ := w.Scan(context.Background())
		for _, a := range aps {
			sec := "WPA2"
			if a.Open() {
				sec = "open"
			}
			nets = append(nets, ui.WifiNet{SSID: a.SSID, Bars: a.Bars(), Security: sec})
		}
	}
	chip := ""
	for _, d := range r.HW.PCIByClass("0280") {
		chip = checks.WifiChips[d.ID()]
	}
	base := ui.NetState{Phase: ui.NetList, At: 1.2, Ethernet: "no adapter or cable", Tether: "no phone sharing its connection",
		WifiChip: chip, CanScan: chip != "", ListAt: 1.2, Networks: nets}
	net := func(mod func(s *ui.State)) func(*ui.State) {
		return func(s *ui.State) {
			s.Net, s.NetSel, s.NetPick = base, 1, 1
			mod(s)
		}
	}
	wired := func(s *ui.State) {
		s.Net.Online, s.Net.Via, s.Net.Iface, s.Net.IP, s.Net.At = true, "Ethernet", "eth0", "192.168.1.52", 1.5
		s.Net.Ethernet, s.Net.EthLink, s.Net.Info = "eth0: connected  ·  192.168.1.52", ui.LinkConnected, "eth0  ·  192.168.1.52"
		s.Net.Refreshed, s.Net.Stick, s.Net.RefreshedAt = "catalog snapshot 3af1f643 is the newest · 372 configurations · from doesitomarchy.com", "on OmaBoot? device: the same snapshot, active", 2.5
	}
	shots := []shot{
		{name: "live-1-hello", st: st(ui.Hello, 8), f: start},
		{name: "live-2-whichmac", st: st(ui.WhichMac, 8), f: start},
		{name: "live-3-network-list", st: st(ui.Network, 6, net(func(s *ui.State) {})), f: start},
		{name: "live-3-network-wired", st: st(ui.Network, 5, net(wired)), f: start},
		{name: "live-3-static", st: st(ui.Network, 3, net(func(s *ui.State) { s.Form.Open = true })), f: start},
		{name: "live-4-quick", st: st(ui.Quick, p.End()*0.6, net(wired)), f: &f},
		{name: "live-4-quick-end", st: st(ui.Quick, p.End()+0.5, net(wired)), f: &f},
		{name: "live-5-results", st: st(ui.Results, 10, net(wired)), f: &f},
		{name: "live-5-results-log", st: st(ui.Results, 10, net(wired), func(s *ui.State) { s.Overlay = ui.LogOverlay }), f: &f},
		{name: "live-6-finish", st: st(ui.Finish, 6, net(wired)), f: &f},
		{name: "live-6-finish-nokey", st: st(ui.Finish, 6, net(wired)), f: &nokey},
		{name: "live-6-finish-saved", st: st(ui.Finish, 20, net(wired), func(s *ui.State) { s.SavedAt, s.FinishSel = 12, 1 }), f: &saved},
		{name: "live-6-finish-sent", st: st(ui.Finish, 20, net(wired), func(s *ui.State) { s.SentAt, s.FinishSel = 11.4, 1 }), f: &sent},
		{name: "live-7-report", st: st(ui.Report, 4, net(wired)), f: &sent},
	}
	// the hands-on tests on the fixture tree: what this Mac gets, and the first test a moment in
	// (a fixture writes nothing and never sleeps)
	setup := app.NewBackend(r).HandsOn()
	ho := func() *ui.HandsOn { return ui.NewHandsOn(setup) }
	shots = append(shots, shot{name: "live-h1-attention", st: st(ui.Attention, 8, net(wired), func(s *ui.State) { s.HO = ho() }), f: &f})
	if len(setup.Plan) > 0 {
		h := ho()
		h.Play(0, 6.5)
		h.Stop()
		shots = append(shots, shot{name: "live-h2-" + setup.Plan[0].ID, st: st(ui.Test, 6.5, net(wired), func(s *ui.State) { s.HO = h }), f: &f})
	}
	for i := range shots {
		if shots[i].st.HO == nil {
			shots[i].st.HO = ho()
		}
	}
	if len(start.Configs) > 0 {
		shots = append(shots, shot{name: "live-2-whichmac-list", st: st(ui.WhichMac, 8, func(s *ui.State) { s.Picking, s.PickSel = true, 1 }), f: start})
	}
	if len(start.Candidates) > 1 {
		shots = append(shots, shot{name: "live-2-whichmac-candidates", st: st(ui.WhichMac, 8), f: start})
	}
	for i := range shots {
		shots[i].p = p
	}
	return shots
}
