package console

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/omaboot/boot-live/internal/identify"
)

// Screen is what we know about the display the console is on.
type Screen struct {
	Connector         string // e.g. card0-eDP-1
	WidthMM, HeightMM int    // physical size, from EDID
	ResX, ResY        int    // the console's framebuffer size in pixels
	Laptop            bool
}

// DetectScreen reads the connected display's EDID (only its size and preferred mode: never the
// serial number), the framebuffer size and whether this Mac is a laptop.
func DetectScreen() Screen {
	var s Screen
	conns, _ := filepath.Glob("/sys/class/drm/card*-*")
	best := -1
	for _, p := range conns {
		if strings.TrimSpace(readFile(p+"/status")) != "connected" {
			continue
		}
		edid, err := os.ReadFile(p + "/edid")
		if err != nil || len(edid) < 128 || edid[0] != 0 || edid[1] != 0xff {
			continue
		}
		name := filepath.Base(p)
		rank := 1
		if strings.Contains(name, "-eDP-") || strings.Contains(name, "-LVDS-") || strings.Contains(name, "-DSI-") {
			rank = 2 // the built-in panel is where the console shows on a Mac
		}
		if rank <= best {
			continue
		}
		best = rank
		s.Connector = name
		s.WidthMM, s.HeightMM = int(edid[21])*10, int(edid[22])*10
		d := edid[54:72] // the first detailed timing: preferred mode and size in mm
		if d[0] != 0 || d[1] != 0 {
			if w, h := int(d[12])|int(d[14]&0xf0)<<4, int(d[13])|int(d[14]&0x0f)<<8; w > 0 && h > 0 {
				s.WidthMM, s.HeightMM = w, h
			}
			s.ResX = int(d[2]) | int(d[4]&0xf0)<<4
			s.ResY = int(d[5]) | int(d[7]&0xf0)<<4
		}
	}
	if v := strings.Split(strings.TrimSpace(readFile("/sys/class/graphics/fb0/virtual_size")), ","); len(v) == 2 {
		x, err1 := strconv.Atoi(v[0])
		y, err2 := strconv.Atoi(v[1])
		if err1 == nil && err2 == nil && x > 0 && y > 0 {
			s.ResX, s.ResY = x, y // the console's real size (a 5K iMac's console runs at 3840×2160)
		}
	}
	// the identifier's family decides when it's a known Mac's (an iMac18,3 says chassis 9,
	// laptop); the chassis type only for anything else
	if l, known := identify.LaptopFamily(strings.TrimSpace(readFile("/sys/class/dmi/id/product_name"))); known {
		s.Laptop = l
		return s
	}
	switch strings.TrimSpace(readFile("/sys/class/dmi/id/chassis_type")) {
	case "8", "9", "10", "11", "14", "30", "31", "32":
		s.Laptop = true
	}
	return s
}

func readFile(p string) string {
	b, _ := os.ReadFile(p)
	return string(b)
}

// Sizes are the omaboot-v cell heights we ship.
var Sizes = []int{16, 24, 32}

// AimMM is the text line height that read well on the three test Macs (spike 1, 2026-10-08):
// closer on laptops, further on desktops.
func AimMM(laptop bool) float64 {
	if laptop {
		return 3.6
	}
	return 5.5
}

// PickPx chooses the cell height nearest to the aim for this screen.
func PickPx(s Screen) (int, string) {
	if s.ResX > 0 && s.WidthMM > 0 {
		aim := AimMM(s.Laptop) * float64(s.ResX) / float64(s.WidthMM)
		best, bestD := Sizes[0], math.Inf(1)
		for _, px := range Sizes {
			if d := math.Abs(float64(px) - aim); d < bestD-0.01 {
				best, bestD = px, d
			}
		}
		return best, fmt.Sprintf("%dpx wide over %dmm, aim %.1fpx (%.1fmm per line)", s.ResX, s.WidthMM, aim, AimMM(s.Laptop))
	}
	switch { // no physical size: go by resolution
	case s.ResX >= 3000:
		return 32, "no EDID size, wide framebuffer"
	case s.ResX > 0 && s.ResX <= 1440:
		return 16, "no EDID size, small framebuffer"
	}
	return 24, "no EDID size"
}

// FontDirs are searched in order: the image's copy, then the repo's assets in development.
func FontDirs() []string {
	dirs := []string{"/usr/share/omaboot/fonts", "assets/fonts"}
	if exe, err := os.Executable(); err == nil {
		d := filepath.Dir(exe)
		for i := 0; i < 3; i++ {
			dirs = append(dirs, filepath.Join(d, "assets", "fonts"))
			d = filepath.Dir(d)
		}
	}
	return dirs
}

// FindFont finds omaboot-v at a cell height.
func FindFont(px int) string {
	name := fmt.Sprintf("omaboot-v%dn.psf.gz", px)
	for _, d := range FontDirs() {
		p := filepath.Join(d, name)
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p
		}
	}
	return ""
}

func pickFont(s Screen) Font {
	px, why := PickPx(s)
	p := FindFont(px)
	if p == "" {
		return Font{Reason: why + "; omaboot-v not found"}
	}
	return Font{Name: "omaboot-v", Px: px, Path: p, Reason: why}
}
