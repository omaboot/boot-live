package main

import (
	"github.com/omaboot/boot-live/internal/demo"
	"github.com/omaboot/boot-live/internal/handson"
	"github.com/omaboot/boot-live/internal/ui"
)

// The Wi-Fi and Ethernet tests (internal/handson/net_wifi.go, net_ethernet.go) on the demo Mac:
// the Wi-Fi run starts offline (prototype scene "Wi-Fi"), and the person's password is typed
// into the field (netShot's keys).
func init() {
	testShots = append(testShots, func() []shot {
		pick := netDo{At: 3, Act: 0}
		typed := netDo{At: 3.6, Keys: "maple syrup 22"}
		join := netDo{At: 6.4, Keys: "\n"}
		set := func(k, v string) func(*handson.Fake) { return func(f *handson.Fake) { f.Set("/fake/net/"+k, v) } }
		return []shot{
			netShot("h20-wifi-scanning", "network.wifi", 1.2, nil, offline),
			netShot("h20-wifi-list", "network.wifi", 3, nil, offline),
			netShot("h20-wifi-password", "network.wifi", 6, nil, offline, pick, typed),
			netShot("h20-wifi-joining", "network.wifi", 7.2, nil, joining, pick, typed, join),
			netShot("h20-wifi-moving", "network.wifi", 8.6, nil, wifiUp, pick, typed, join),
			netShot("h20-wifi-pass", "network.wifi", 11, nil, wifiUp, pick, typed, join),
			netShot("h20-wifi-refused", "network.wifi", 8.4, set("join", "refuse"), offline, pick, typed, join),
			netShot("h20-wifi-wont-join", "network.wifi", 13, set("join", "refuse"), offline, pick, typed, join,
				netDo{At: 9, Act: 7}, netDo{At: 9.5, Keys: "maple syrup 22\n"}, netDo{At: 12, Act: 7}),
			netShot("h20-wifi-broken", "network.wifi", 11, set("watch", "broken"), wifiUp, pick, typed, join),
			// a bootwatchd of its own (target.conf watch=, or --watch): the Output names it
			netShot("h20-wifi-moving-server", "network.wifi", 8.6, set("server", "http://192.168.1.20:8470"), wifiUp, pick, typed, join),
			netShot("h20-wifi-pass-server", "network.wifi", 11, set("server", "http://192.168.1.20:8470"), wifiUp, pick, typed, join),
			netShot("h20-wifi-none-heard", "network.wifi", 3, set("aps", ""), offline),
			netShot("h21-ethernet-waiting", "network.ethernet", 2, nil, wifiUp),
			netShot("h21-ethernet-address", "network.ethernet", 3.6, nil, wifiUp),
			netShot("h21-ethernet-moving", "network.ethernet", 5.2, nil, wifiUp),
			netShot("h21-ethernet-pass", "network.ethernet", 8, nil, wifiUp),
			netShot("h21-ethernet-no-link", "network.ethernet", 12, func(*handson.Fake) {}, wifiUp),
		}
	})
}

// netDo is one thing the person does during a network test: at At, pick answer Act, or type
// Keys ("\n" is Enter).
type netDo struct {
	At   float64
	Act  int
	Keys string
}

func offline(s *ui.State) { s.Offline = true }

func joining(s *ui.State) { s.Net.Phase, s.Net.SSID = ui.NetJoining, "maple-house-5G" }

// netShot plays a network test on the demo Mac to t with what the person does, and net the
// status line's network. A script plays it on a variant of the demo Mac (variantShot's way: the
// Demo as it stands at 0 s, its timed changes dropped, then the script's).
func netShot(name, id string, t float64, script func(*handson.Fake), net func(*ui.State), dos ...netDo) shot {
	setup := demo.HandsOnSetup()
	io := setup.IO
	setup.IO = func(s handson.Spec) handson.IO {
		f := io(s).(*handson.Fake)
		if s.ID == id && script != nil { // the Demo as it stands at 0 s, then script's changes
			f.Advance(0)
			g := handson.NewFake()
			for k, v := range f.Files {
				g.Files[k] = v
			}
			script(g)
			return g
		}
		return f
	}
	h := ui.NewHandsOn(setup)
	i := planIndex(h, id)
	h.Open(i, 0)
	next := 0
	for at := 0.0; at <= t+1e-9; at += 1.0 / ui.FPS {
		for next < len(dos) && dos[next].At <= at {
			d := dos[next]
			switch {
			case h.Run == nil || h.Out != nil:
			case d.Keys != "":
				for _, k := range d.Keys {
					key := string(k)
					if k == '\n' {
						key = "enter"
					}
					h.Run.Key(key)
				}
			default:
				h.Run.Act(d.Act)
			}
			h.KeyAt = at
			next++
		}
		h.Step(at)
	}
	s := st(ui.Test, t, net, func(s *ui.State) { s.HO = h })
	f := demo.Finished()
	f.HandsOn = h.Checks()
	return shot{name: name, st: s, f: f}
}
