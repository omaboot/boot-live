// screens draws the S1 screens at chosen moments into files, for reviewing the layout without a
// console: plain text (.txt), the escape-coded rows as Bubble Tea gets them (.ans, `cat` it in a
// terminal), and a PNG drawn with the real omaboot-v font and our palette. The demo's screens
// come from its script; with -sysroot it also draws the real mode's screens, from a quick pass
// run on a fixture tree.
//
//	go run ./tools/screens -out /tmp/screens [-sysroot testdata/macbookair5,2] [-only finish]
package main

import (
	"flag"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/omaboot/boot-live/internal/boot"
	"github.com/omaboot/boot-live/internal/checks"
	"github.com/omaboot/boot-live/internal/demo"
	"github.com/omaboot/boot-live/internal/glyphs"
	"github.com/omaboot/boot-live/internal/grid"
	"github.com/omaboot/boot-live/internal/handson"
	"github.com/omaboot/boot-live/internal/i18n"
	"github.com/omaboot/boot-live/internal/network"
	"github.com/omaboot/boot-live/internal/pace"
	"github.com/omaboot/boot-live/internal/ui"
)

type shot struct {
	name string
	st   ui.State
	ed   ui.Edition
	opt  func(*ui.Options)
	f    *ui.Facts    // nil: the demo's, after the quick pass
	p    *pace.Player // nil: the demo's whole pass
}

// Sizes drawn: the smallest grid the screens are laid out for (a 21.5-inch iMac at 24 px), a
// 13-inch MacBook at 16 px, and the minimum the tool accepts.
var sizes = []struct{ w, h, px int }{{160, 45, 24}, {160, 50, 16}, {120, 40, 16}}

func st(s ui.Scene, t float64, f ...func(*ui.State)) ui.State {
	x := ui.NewState()
	x.Scene, x.T = s, t
	for _, fn := range f {
		fn(&x)
	}
	return x
}

func main() {
	out := flag.String("out", "screens", "where to write the files")
	fonts := flag.String("fonts", "assets/fonts", "the omaboot-v fonts")
	only := flag.String("only", "", "only shots whose name contains this")
	root := flag.String("sysroot", "", "also draw the real mode's screens from a quick pass on this fixture tree")
	size := flag.String("size", "", "draw at these sizes instead, COLSxROWSxPX[,…]: 180x56x16 a 13-inch MacBook Air, 240x67x32 a 27-inch 5K iMac")
	flag.Parse()
	if *size != "" {
		sizes = nil
		for _, z := range strings.Split(*size, ",") {
			var w, h, px int
			if _, err := fmt.Sscanf(z, "%dx%dx%d", &w, &h, &px); err != nil {
				must(fmt.Errorf("-size %q: want COLSxROWSxPX", z))
			}
			sizes = append(sizes, struct{ w, h, px int }{w, h, px})
		}
	}

	shots := append(append(demoShots(), whichMacShots()...), handsOnShots()...)
	shots = append(append(shots, consoleShots()...), conOver(shots)...)
	if *root != "" {
		shots = append(shots, liveShots(*root)...)
	}
	full := demo.Finished()
	whole := ui.NewPlayer(demo.Checks, len(demo.Checks))
	for _, sz := range sizes {
		font, err := glyphs.Load(filepath.Join(*fonts, fmt.Sprintf("omaboot-v%dn.psf.gz", sz.px)))
		must(err)
		dir := filepath.Join(*out, fmt.Sprintf("%dx%d", sz.w, sz.h))
		must(os.MkdirAll(dir, 0o755))
		for _, s := range shots {
			if *only != "" && !strings.Contains(s.name, *only) {
				continue
			}
			ed := s.ed
			if ed.Key == "" {
				ed = ui.Live
			}
			o := ui.Options{Edition: ed, Mode: grid.VT, Palette: grid.OmaBoot, Version: "0.7.7 dev", Font: "omaboot-v", FontPx: sz.px}
			if s.opt != nil {
				s.opt(&o)
			}
			gr := grid.New(sz.w, sz.h)
			stt := s.st
			if stt.HO == nil { // Quick results counts the hands-on tests
				stt.HO = ui.NewHandsOn(demo.HandsOnSetup())
			}
			sf, sp := full, whole
			if s.f != nil {
				sf = s.f
			}
			if s.p != nil {
				sp = s.p
			}
			ui.Render(gr, &o, &stt, sf, sp)
			base := filepath.Join(dir, s.name)
			must(os.WriteFile(base+".txt", []byte(gr.Text()), 0o644))
			must(os.WriteFile(base+".ans", []byte(strings.Join(gr.Lines(grid.TrueColor, &o.Palette), "\n")+"\n"), 0o644))
			must(writePNG(base+".png", gr, font, &o.Palette))
		}
		fmt.Println("wrote", dir)
	}
}

// netBase is the demo's network step with the Wi-Fi list up.
func netBase() ui.NetState {
	return ui.NetState{Phase: ui.NetList, At: 1.9, Ethernet: i18n.T("no adapter or cable"), Tether: i18n.T("no phone sharing its connection"),
		WifiChip: demo.WifiChip, CanScan: true, ListAt: 1.9, Networks: demo.Networks}
}

func net(mod func(s *ui.State)) func(*ui.State) {
	return func(s *ui.State) {
		s.Net, s.NetSel, s.NetPick = netBase(), 1, 1
		mod(s)
	}
}

func wifiUp(s *ui.State) {
	s.Net.Online, s.Net.Via, s.Net.Iface, s.Net.IP, s.Net.SSID, s.Net.At = true, "Wi-Fi", "wlan0", demo.IP, demo.SSID, 9.8
	s.Net.Info = demo.SSID + "  ·  " + demo.Band + "  ·  " + demo.IP
	s.Net.Refreshed, s.Net.Stick, s.Net.RefreshedAt = i18n.T("catalog snapshot refreshed: %s · %d configurations · from %s", demo.RefreshedDate+" (ca59a3bd)", demo.RefreshedConfs, "doesitomarchy.com"), i18n.T("on OmaBoot? device: %s · %d configurations · not used", "2026-10-11 (c2c40d7b)", demo.RefreshedConfs-4), 10.6
}

func cable(up bool) func(*ui.State) {
	return net(func(s *ui.State) {
		s.Net.Ethernet, s.Net.EthLink, s.Net.At = "enp0s20u1: cable in, asking for an address…", ui.LinkCarrier, 2.4
		if up {
			s.Net.Ethernet, s.Net.EthLink = "enp0s20u1: connected  ·  192.168.1.41", ui.LinkConnected
			s.Net.Online, s.Net.Via, s.Net.Iface, s.Net.IP, s.Net.At = true, "Ethernet", "enp0s20u1", "192.168.1.41", 5.4
			s.Net.Refreshed, s.Net.Stick, s.Net.RefreshedAt = i18n.T("catalog snapshot refreshed: %s · %d configurations · from %s", demo.RefreshedDate+" (ca59a3bd)", demo.RefreshedConfs, "doesitomarchy.com"), i18n.T("on OmaBoot? device: %s · %d configurations · not used", "2026-10-11 (c2c40d7b)", demo.RefreshedConfs-4), 6.2
		}
	})
}

// iphone puts an iPhone on the phone line beside a working cable: waiting for Trust (from 1.6 s),
// ready (from 4.2 s), being switched to (from 8.6 s), carrying the run (from 10.4 s).
func iphone(stage int) func(*ui.State) {
	return func(s *ui.State) {
		cable(true)(s)
		s.Net.At = 0.6
		n := &s.Net
		n.Phone, n.PhoneApple, n.PhoneAt = "iPhone", true, 1.6
		n.Tether, n.TetLink, n.PhoneNeed = "iPhone found  ·  on the phone: tap Trust, type its passcode, Personal Hotspot on", ui.LinkCarrier, network.NeedTrust
		if stage == 0 {
			return
		}
		n.Tether, n.TetLink, n.PhoneNeed, n.PhoneAt = "iPhone ready  ·  172.20.10.3", ui.LinkReady, network.NeedNothing, 4.2
		switch stage {
		case 2: // switching
			n.Phase, n.To, n.At, n.Parked = ui.NetSwitching, "Phone", 8.6, "Ethernet"
			n.Ethernet, n.EthLink = ui.OffText(*n, "enp0s20u1"), ui.LinkOff
			n.Tether, n.TetLink = "iPhone: asking for an address…", ui.LinkCarrier
		case 3: // in use, the cable off
			n.Online, n.Via, n.Iface, n.IP, n.At, n.Parked = true, "Phone", "enx001122aabbcc", "172.20.10.3", 10.4, "Ethernet"
			n.Info = "iPhone  ·  172.20.10.3"
			n.Ethernet, n.EthLink = ui.OffText(*n, "enp0s20u1"), ui.LinkOff
			n.Tether, n.TetLink = "iPhone: connected  ·  172.20.10.3", ui.LinkConnected
		}
	}
}

// offlinePhone is the Wi-Fi list with no cable, and a phone or LTE stick on the phone line.
func offlinePhone(name, text string, link int, need network.Need) func(*ui.State) {
	return net(func(s *ui.State) {
		n := &s.Net
		n.Phone, n.Tether, n.TetLink, n.PhoneNeed, n.PhoneAt = name, text, link, need, 2.4
	})
}

func form(focus int, fields ...string) func(*ui.State) {
	return func(s *ui.State) {
		s.Net, s.NetSel = netBase(), len(demo.Networks)+2
		s.Form.Open, s.Form.Focus = true, focus
		for i, v := range fields {
			s.Form.Fields[i] = []rune(v)
		}
	}
}

// demoShots are the demo's screens, in the flow's order.
func demoShots() []shot {
	start := demo.Facts()
	start.Static = ui.StaticTarget{Iface: "enp0s20u1", What: i18n.T("USB Ethernet adapter  ·  cable in, no address offered")}
	plain := func(o *ui.Options) { o.Plain = true }
	full := demo.Finished()
	online := func(s *ui.State) { wifiUp(s) }

	// the quick pass around the GPU notice: three results in, the notice up, then the rest
	// finishing 13 s later, as when the person presses Enter with 2 s left
	pre := ui.NewPlayer(demo.Checks[:3], len(demo.Checks))
	at := pre.End()
	shifted := append([]ui.Check(nil), demo.Checks...)
	for i := 3; i < len(shifted); i++ {
		d := at + 13 - pace.Default.Start - demo.Checks[2].Finish
		shifted[i].Begin += d
		shifted[i].Finish += d
	}
	post := ui.NewPlayer(shifted, len(shifted))
	// the thermal check still running (its result not in yet): the band shows its effect
	hi := 0
	for i, c := range shifted {
		if c.Effect == boot.Heat {
			hi = i
		}
	}
	heat := shifted[hi]
	running := ui.NewPlayer(shifted[:hi], len(shifted))
	running.Started(heat.Begin, ui.Cue("thermal"))
	runAt := running.End() + 4
	runningHeat := func(s *ui.State) {
		s.Run = ui.Running{ID: "thermal", Label: heat.Label, Effect: heat.Effect, Params: boot.Params{Now: 71}}
	}
	runningFacts := demo.Finished()
	runningFacts.Checks = shifted[:hi]
	quit := func(s *ui.State) { s.Overlay = ui.ConfirmOverlay }
	done := func(s *ui.State) { s.PassDone = true }
	// a long Wi-Fi list (a block of flats), the cursor on the actions after Tab
	long := netBase()
	for i := 0; len(long.Networks) < 23; i++ {
		long.Networks = append(long.Networks, ui.WifiNet{SSID: fmt.Sprintf("apt-%d%02d", 2+i/8, i%8+1), Bars: 1 + i%3, Security: "WPA2"})
	}
	longList := func(sel, pick int) func(*ui.State) {
		return func(s *ui.State) { s.Net, s.NetSel, s.NetPick = long, sel, pick }
	}
	shiftedFacts := demo.Finished()
	shiftedFacts.Checks = shifted
	gpu := func(t0, end float64) func(*ui.State) {
		return func(s *ui.State) {
			s.GPU = ui.GPUNotice{Waiting: end < 0, ID: "graphics.integrated", Label: demo.Checks[3].Label, At: t0, End: end}
			wifiUp(s)
		}
	}
	end := post.End()

	sent := demo.Finished()
	sent.Report, sent.ReportPage = demo.Report, demo.Site+"/report/"+demo.Report
	saved := demo.Finished()
	saved.SavedFile = "results/MacBookAir5,2-20261010T074100Z.json"
	nokey := demo.Finished()
	nokey.CanSubmit, nokey.NoSubmit = false, i18n.T("no report key in this image: save the report to external storage")

	shots := []shot{
		{name: "1-hello-start", st: st(ui.Hello, 2.6), f: start},
		{name: "1-hello", st: st(ui.Hello, 8), f: start},
		{name: "1-hello-omarchy", st: st(ui.Hello, 8), f: start, ed: ui.Omarchy},
		{name: "1-hello-quit", st: st(ui.Hello, 8, quit), f: start},
		{name: "2-whichmac", st: st(ui.WhichMac, 6), f: start},
		{name: "2-whichmac-resume", st: st(ui.WhichMac, 6), f: resumeFacts(start)},
		{name: "2-whichmac-other-version", st: st(ui.WhichMac, 6), f: otherFacts(start)},
		// 0.7.6: picking a configuration from the list answers another version's copy too
		{name: "2-whichmac-other-version-picked", st: st(ui.WhichMac, 6, func(s *ui.State) { s.Picked, s.Answered = true, true }), f: pickedFacts(otherFacts(start))},
		{name: "3-network-scanning", st: st(ui.Network, 1.5, net(func(s *ui.State) { s.Net.Phase, s.Net.Networks = ui.NetScanning, nil })), f: start},
		{name: "3-network-list", st: st(ui.Network, 6, net(func(s *ui.State) {})), f: start},
		{name: "3-network-actions", st: st(ui.Network, 6, net(func(s *ui.State) { s.NetSel = len(demo.Networks) })), f: start},
		{name: "3-network-long", st: st(ui.Network, 6, longList(12, 12)), f: start},
		{name: "3-network-long-tab", st: st(ui.Network, 6, longList(23, 12)), f: start},
		{name: "3-network-quit", st: st(ui.Network, 6, net(quit)), f: start},
		{name: "3-network-password", st: st(ui.Network, 6.8, net(func(s *ui.State) { s.Pw, s.PwLen, s.Net.SSID, s.Net.At = true, 9, demo.SSID, 5 })), f: start},
		{name: "3-network-password-shown", st: st(ui.Network, 7.3, net(func(s *ui.State) {
			s.Pw, s.PwLen, s.PwShow, s.PwShown, s.Net.SSID, s.Net.At = true, 13, true, []rune("correct horse"), demo.SSID, 5
		})), f: start},
		{name: "3-network-joining", st: st(ui.Network, 8.2, net(func(s *ui.State) {
			s.Pw, s.PwLen, s.Net.SSID, s.Net.Phase, s.Net.At = true, 13, demo.SSID, ui.NetJoining, 7.6
		})), f: start},
		{name: "3-network-refused", st: st(ui.Network, 9, net(func(s *ui.State) { s.Net.Err, s.Net.At = demo.SSID+" refused the password", 8 })), f: start},
		{name: "3-network-up", st: st(ui.Network, 13, net(online)), f: start},
		{name: "3-network-cable", st: st(ui.Network, 3.5, cable(false)), f: start},
		{name: "3-network-cable-up", st: st(ui.Network, 9, cable(true)), f: start},
		{name: "3-network-switch-ask", st: st(ui.Network, 9, func(s *ui.State) {
			cable(true)(s)
			s.Browse, s.Overlay, s.SwitchVia, s.SwitchTo = true, ui.SwitchOverlay, "Wi-Fi", 0
		}), f: start},
		{name: "3-network-switch-joining", st: st(ui.Network, 9, func(s *ui.State) {
			cable(true)(s)
			s.Browse, s.Net.Phase, s.Net.SSID, s.Net.Parked, s.Net.To, s.Net.At = true, ui.NetJoining, demo.SSID, "Ethernet", "Wi-Fi", 8.5
		}), f: start},
		{name: "3-network-wifi-parked", st: st(ui.Network, 13, net(func(s *ui.State) {
			online(s)
			s.Net.Parked, s.LineSel = "Ethernet", ui.LineEthernet
			s.Net.Ethernet, s.Net.EthLink = ui.OffText(s.Net, "enp0s20u1"), ui.LinkOff
		})), f: start},
		{name: "3-network-phone-trust", st: st(ui.Network, 3.5, iphone(0)), f: start},
		{name: "3-network-phone-ready", st: st(ui.Network, 7, iphone(1), func(s *ui.State) { s.LineSel = ui.LinePhone }), f: start},
		{name: "3-network-phone-wifi-line", st: st(ui.Network, 7, iphone(1), func(s *ui.State) { s.LineSel = ui.LineWiFi }), f: start},
		{name: "3-network-phone-ask", st: st(ui.Network, 7.5, iphone(1), func(s *ui.State) {
			s.Overlay, s.SwitchVia = ui.SwitchOverlay, "Phone"
		}), f: start},
		{name: "3-network-phone-switching", st: st(ui.Network, 9.2, iphone(2)), f: start},
		{name: "3-network-phone-in-use", st: st(ui.Network, 15, iphone(3)), f: start},
		{name: "3-network-phone-back-ask", st: st(ui.Network, 15, iphone(3), func(s *ui.State) {
			s.Overlay, s.SwitchVia = ui.SwitchOverlay, "Ethernet"
		}), f: start},
		{name: "3-network-android-charging", st: st(ui.Network, 6, offlinePhone("Pixel 7",
			"Pixel 7 found  ·  charging only: unlock it, turn on USB tethering", ui.LinkCarrier, network.NeedTethering)), f: start},
		{name: "3-network-android-up", st: st(ui.Network, 12, offlinePhone("Pixel 7",
			"Pixel 7: connected  ·  192.168.42.129", ui.LinkConnected, network.NeedNothing), func(s *ui.State) {
			s.Net.Online, s.Net.Via, s.Net.Iface, s.Net.IP, s.Net.At = true, "Phone", "enx001122aabbcc", "192.168.42.129", 7.6
		}), f: start},
		{name: "3-network-lte-modem", st: st(ui.Network, 6, offlinePhone("Sierra Wireless EM7455",
			"Sierra Wireless EM7455  ·  an LTE modem: it needs dialing, which OmaBoot? can't do yet", ui.LinkLimit, network.NeedNothing)), f: start},
		{name: "3-network-back-after-pass", st: st(ui.Network, 30, net(func(s *ui.State) { wifiUp(s); s.PassDone = true })), f: full},
		{name: "3-network-sign-in-page", st: st(ui.Network, 14, net(func(s *ui.State) {
			wifiUp(s)
			s.Net.SignIn, s.Net.At = true, 11.2 // boot.watch's echo came back as a hotel's sign-in page
		})), f: start},
		{name: "3-static-fields", st: st(ui.Network, 4, form(1, "10.0.4.23/24", "10.0.4.")), f: start},
		{name: "3-static-error", st: st(ui.Network, 7, form(1, "10.0.4.23/24", "10.0.5.1", "10.0.4.1"), func(s *ui.State) {
			s.Form.Err = "the gateway 10.0.5.1 isn't in 10.0.4.0/24"
		}), f: start},
		{name: "3-static-applying", st: st(ui.Network, 8, form(3, "10.0.4.23/24", "10.0.4.1", "10.0.4.1"), func(s *ui.State) {
			s.Form.Requested, s.Net.Phase, s.Net.Iface, s.Net.At = true, ui.NetApplying, "enp0s20u1", 7.6
		}), f: start},
		{name: "3-static-done", st: st(ui.Network, 12, form(3, "10.0.4.23/24", "10.0.4.1", "10.0.4.1"), func(s *ui.State) {
			s.Form.Requested, s.Net.Online, s.Net.Static, s.Net.Via, s.Net.Iface, s.Net.IP, s.Net.Gateway, s.Net.At = true, true, true, "Ethernet", "enp0s20u1", "10.0.4.23", "10.0.4.1", 9.4
			s.Net.Ethernet, s.Net.EthLink = "enp0s20u1: connected  ·  10.0.4.23  (static)", ui.LinkConnected
		}), f: start},
		{name: "4-quick-pci", st: st(ui.Quick, 2.5, online), p: pre},
		{name: "4-quick-notice", st: st(ui.Quick, at+2, gpu(at, -1)), p: pre},
		{name: "4-quick-notice-starting", st: st(ui.Quick, at+13.2, gpu(at, at+13)), p: pre},
		{name: "4-quick-torus", st: st(ui.Quick, at+13+2.3, gpu(at, at+13)), f: shiftedFacts, p: post},
		{name: "4-quick-heat", st: st(ui.Quick, end-5, gpu(at, at+13)), f: shiftedFacts, p: post},
		{name: "4-quick-burst", st: st(ui.Quick, end-0.4, gpu(at, at+13)), f: shiftedFacts, p: post},
		{name: "4-quick-log", st: st(ui.Quick, end-5, gpu(at, at+13), func(s *ui.State) { s.Overlay, s.LogTop = ui.LogOverlay, 99 }), f: shiftedFacts, p: post},
		{name: "4-quick-heat-running", st: st(ui.Quick, runAt, gpu(at, at+13), runningHeat), f: runningFacts, p: running},
		{name: "4-quick-confirm", st: st(ui.Quick, end-5, gpu(at, at+13), func(s *ui.State) { s.Overlay, s.Paused = ui.ConfirmOverlay, true }), f: shiftedFacts, p: post},
		{name: "4-quick-stick-gone", st: st(ui.Quick, end-5, gpu(at, at+13), func(s *ui.State) { s.Overlay, s.Paused, s.Gone, s.StickSel = ui.StickOverlay, true, true, 1 }), f: shiftedFacts, p: post},
		{name: "5-results", st: st(ui.Results, 10, online)},
		{name: "5-results-slow-drive", st: st(ui.Results, 10, online), f: slowDrive()},
		{name: "5-results-quit", st: st(ui.Results, 10, online, done, quit)},
		{name: "5-results-log", st: st(ui.Results, 10, online, func(s *ui.State) { s.Overlay = ui.LogOverlay })},
		{name: "5-results-log-end", st: st(ui.Results, 10, online, func(s *ui.State) { s.Overlay, s.LogTop = ui.LogOverlay, 99 })},
		{name: "6-finish", st: st(ui.Finish, 6, online)},
		{name: "6-finish-quit", st: st(ui.Finish, 6, online, done, quit)},
		{name: "6-finish-shutdown-ask", st: st(ui.Finish, 6, online, done, func(s *ui.State) { s.Overlay, s.EndWhat = ui.EndOverlay, ui.EndPoweroff })},
		{name: "6-finish-restart-ask", st: st(ui.Finish, 6, online, done, func(s *ui.State) { s.Overlay, s.EndWhat = ui.EndOverlay, ui.EndReboot })},
		{name: "6-finish-quit-omarchy", st: st(ui.Finish, 6, online, done, quit), ed: ui.Omarchy},
		{name: "6-finish-anim", st: st(ui.Finish, 1.2, online)},
		{name: "6-finish-omarchy", st: st(ui.Finish, 6, online), ed: ui.Omarchy},
		{name: "6-finish-offline", st: st(ui.Finish, 6)},
		{name: "6-finish-stick-gone", st: st(ui.Finish, 6, online, done, func(s *ui.State) { s.Gone, s.FinishSel = true, 1 })},
		{name: "6-finish-nokey", st: st(ui.Finish, 6, online), f: nokey},
		{name: "6-finish-submit", st: st(ui.Finish, 9, online, func(s *ui.State) { s.Submitting, s.SubAt, s.Name = true, 4.6, []rune("sam_r") })},
		{name: "6-finish-submit-actions", st: st(ui.Finish, 9, online, func(s *ui.State) { s.Submitting, s.SubAt, s.SubFocus, s.Name = true, 4.6, 1, []rune("sam_r") })},
		{name: "6-finish-sending", st: st(ui.Finish, 10, online, func(s *ui.State) {
			s.Submitting, s.SubAt, s.SubFocus, s.SendAt, s.Name = true, 4.6, 1, 9.2, []rune("sam_r")
		})},
		{name: "6-finish-send-refused", st: st(ui.Finish, 12, online, func(s *ui.State) {
			s.Submitting, s.SubAt, s.SubFocus, s.Name = true, 4.6, 1, []rune("sam_r")
			s.SendErr = "the site didn't accept the report: invalid report"
			s.Problems = []string{"items.network.wifi: a live boot can't decide network.wifi (Wi-Fi); send it as not_tested with reason live-limit"}
		})},
		{name: "6-finish-send-waiting", st: st(ui.Finish, 10, online, func(s *ui.State) {
			s.Submitting, s.SubAt, s.SubFocus, s.SendAt, s.Name = true, 4.6, 1, 9.2, []rune("sam_r")
			s.SendNote = "the Pixel 7 went away: waiting up to 15 s for it to come back…  ·  turn USB tethering back on: Android turns it off when unplugged"
		})},
		{name: "6-finish-send-offline", st: st(ui.Finish, 14, online, func(s *ui.State) {
			s.SendErrAt, s.FinishSel, s.Net.Online = 12, 1, false
			s.SendErr = "offline: the connection went away and didn't come back. Save a copy to external storage and send it later."
		})},
		{name: "6-finish-sent-receiving", st: st(ui.Finish, 12.2, online, func(s *ui.State) { s.SentAt = 11.4 }), f: sent},
		{name: "6-finish-sent-start", st: st(ui.Finish, 13.4, online, func(s *ui.State) { s.SentAt = 11.4 }), f: sent},
		{name: "6-finish-sent-quit", st: st(ui.Finish, 20, online, done, quit, func(s *ui.State) { s.SentAt = 11.4 }), f: sent},
		{name: "6-finish-sent", st: st(ui.Finish, 20, online, func(s *ui.State) { s.SentAt, s.FinishSel = 11.4, 1 }), f: sent},
		{name: "6-finish-sent-omarchy", st: st(ui.Finish, 20, online, func(s *ui.State) { s.SentAt = 11.4 }), f: sent, ed: ui.Omarchy},
		{name: "6-finish-saved", st: st(ui.Finish, 20, online, func(s *ui.State) { s.SavedAt, s.FinishSel = 12, 1 }), f: saved},
		{name: "6-finish-saved-quit", st: st(ui.Finish, 20, online, done, quit, func(s *ui.State) { s.SavedAt = 12 }), f: saved},
		{name: "7-report", st: st(ui.Report, 4, online)},
		{name: "7-report-sent", st: st(ui.Report, 4, online), f: sent},
		{name: "8-doze", st: st(ui.Results, 200, online, func(s *ui.State) { s.Doze, s.DozeT = true, 17 })},
		{name: "8-doze-network", st: st(ui.Network, 200, net(online), func(s *ui.State) { s.Doze, s.DozeT = true, 4 }), f: start},
		{name: "plain-1-hello", st: st(ui.Hello, 8), f: start, opt: plain},
		{name: "plain-3-network", st: st(ui.Network, 6, net(func(s *ui.State) {})), f: start, opt: plain},
		{name: "plain-4-quick-notice", st: st(ui.Quick, at+2, gpu(at, -1)), p: pre, opt: plain},
		{name: "plain-5-results", st: st(ui.Results, 10, online), opt: plain},
		{name: "plain-5-results-quit", st: st(ui.Results, 10, online, done, quit), opt: plain},
		{name: "plain-8-doze", st: st(ui.Results, 200, online, func(s *ui.State) { s.Doze, s.DozeT = true, 17 }), opt: plain},
		{name: "plain-6-finish", st: st(ui.Finish, 20, online, func(s *ui.State) { s.SentAt = 11.4 }), f: sent, opt: plain},
	}
	return shots
}

func must(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func writePNG(path string, gr *grid.Grid, f *glyphs.Font, pal *grid.Palette) error {
	img := image.NewRGBA(image.Rect(0, 0, gr.W*f.W, gr.H*f.H))
	rgb := func(c grid.Color) color.RGBA { v := pal[c]; return color.RGBA{v[0], v[1], v[2], 255} }
	for y := 0; y < gr.H; y++ {
		for x := 0; x < gr.W; x++ {
			c := gr.At(x, y)
			gl := f.Glyph(c.R)
			fg, bg := rgb(c.FG), rgb(c.BG)
			for py := 0; py < f.H; py++ {
				for px := 0; px < f.W; px++ {
					if f.Pixel(gl, px, py) {
						img.SetRGBA(x*f.W+px, y*f.H+py, fg)
					} else {
						img.SetRGBA(x*f.W+px, y*f.H+py, bg)
					}
				}
			}
		}
	}
	w, err := os.Create(path)
	if err != nil {
		return err
	}
	defer w.Close()
	return png.Encode(w, img)
}

// resumeFacts is the demo's Mac with an unfinished run's copy on external storage, from the
// sleep test it didn't come back from (0.7.4: pick up where you left off).
func resumeFacts(f *ui.Facts) *ui.Facts {
	c := *f
	done := map[string]handson.Outcome{}
	for _, s := range demo.HandsOnSetup().Plan {
		if s.ID != "power.sleep-wake" && s.ID != "power.lid" {
			done[s.ID] = handson.Outcome{}
		}
	}
	var quick []checks.Result
	for _, x := range demo.Checks {
		quick = append(quick, checks.Result{ID: x.ID, Status: checks.Supported})
	}
	c.Resume = &ui.Resume{Test: "power.sleep-wake", Title: "Sleep and wake", Saved: time.Date(2026, 10, 15, 18, 45, 0, 0, time.UTC),
		Version: "0.7.7 dev", Done: done, Of: len(demo.HandsOnSetup().Plan), Quick: quick, QuickIn: "2 min 4 s"}
	return &c
}

// slowDrive is the demo's results with the OmaBoot? drive reading slowly (0.7.5).
func slowDrive() *ui.Facts {
	f := demo.Finished()
	f.SlowDrive = i18n.T("The OmaBoot? drive is reading slowly (%.1f MB/s; usually 10+): tests that load from it may time out.", 0.7)
	return f
}

// otherFacts is the demo's Mac with an unfinished run's copy from another boot-live version:
// named, never picked up (0.7.5).
func otherFacts(f *ui.Facts) *ui.Facts {
	c := *f
	c.OtherCopy = &ui.OtherCopy{Version: "0.7.5 dev", Saved: time.Date(2026, 10, 15, 18, 45, 0, 0, time.UTC), Copy: "results/iMac10,1-20261015T184500Z.json"}
	return &c
}

// pickedFacts is Which Mac after a configuration was picked from the list.
func pickedFacts(f *ui.Facts) *ui.Facts {
	c := *f
	if len(c.Configs) > 1 {
		c.MatchNote = i18n.T("Picked from the list.")
	}
	return &c
}
