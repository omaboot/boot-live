package main

import (
	"github.com/omaboot/boot-live/internal/handson"
	"github.com/omaboot/boot-live/internal/identify"
	"github.com/omaboot/boot-live/internal/ui"
)

// The external display's screen when no code card could go up (no DRM master, no free CRTC):
// a yes doesn't count then. And its video ports on other Macs' maps (owner, 2026-10-19): the
// Mac mini's HDMI and Mini DisplayPort (one of each, so the monitor's port is known), the
// iMac18,1's two Thunderbolt 3 ports (which one can't be told).
func init() {
	testShots = append(testShots, func() []shot {
		return []shot{
			variantShot("h5-display-no-card", "graphics.external-display", 3, func(f *handson.Fake) {
				d := "/sys/class/drm/card1-DP-1/"
				f.Set(d+"status", "connected")
				f.Set(d+"modes", "1920x1200\n1920x1080")
				f.Set(d+"edid", handson.DemoEDID("DELL U2412M"))
				f.Set("kms:fail", "DRM master refused: device or resource busy")
			}),
			catShot("h5-display-mini-waiting", "macmini4-1-mid-2010-server", &miniInfo, 2.5, nil),
			catShot("h5-display-mini-hdmi", "macmini4-1-mid-2010-server", &miniInfo, 8, hdmiIO),
			catShot("h5-display-mini-dp", "macmini4-1-mid-2010-server", &miniInfo, 8, nil),
			catShot("h5-display-imac18-1", "imac18-1-21-2017-a", &imac181Info, 8, nil),
		}
	})
}

var (
	miniInfo    = ui.MacInfo{Name: "Mac mini (Mid 2010)", ID: "Macmini4,1", Config: "Core 2 Duo 2.66 GHz, GeForce 320M"}
	imac181Info = ui.MacInfo{Name: "iMac (21.5-inch, 2017)", ID: "iMac18,1", Config: "Core i5 2.3 GHz, Iris Plus 640"}
)

// catShot is the external display test on a Mac whose port map comes from the snapshot built in.
func catShot(name, config string, mac *ui.MacInfo, t float64, io func(handson.Spec) handson.IO) shot {
	cat, err := identify.Embedded()
	if err != nil {
		panic(err)
	}
	ports := cat.Layouts[config]
	if identify.AllInOne(mac.ID) {
		ports = identify.MirrorBack(ports)
	}
	return macShotOf(name, "graphics.external-display", t, &handson.Env{Ports: ports}, io, mac)
}

// hdmiIO is the demo Mac with a monitor on HDMI from the start.
func hdmiIO(handson.Spec) handson.IO {
	f := handson.NewFake()
	handson.DemoMac(f)
	d := "/sys/class/drm/card0-HDMI-A-1/"
	f.Set(d+"status", "connected")
	f.Set(d+"enabled", "enabled")
	f.Set(d+"modes", "1920x1080\n1280x720")
	f.Set(d+"edid", handson.DemoEDID("SONY TV"))
	return f
}
