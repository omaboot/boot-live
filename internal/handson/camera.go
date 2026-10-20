package handson

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"image/jpeg"
	"math"
	"path"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/omaboot/boot-live/internal/boot"
	"github.com/omaboot/boot-live/internal/checks"
	g "github.com/omaboot/boot-live/internal/grid"
	"github.com/omaboot/boot-live/internal/i18n"
)

// camera.builtin, "Built-in webcam": the tool streams the camera (V4L2, camera_v4l2.go: YUYV,
// or MJPEG decoded here) and shows it live inside the frame as grey half-blocks, mirrored like
// a mirror; the person says whether they see themselves move (method observed, LIVE-PLAN
// §21.4).
//
//   - supported: the camera streamed frames and the tester saw themselves move in the preview;
//   - partial: frames came, but the tester saw a wrong picture (frozen, garbled, upside down,
//     or far too dark to make anything out);
//   - failed: no video device for the camera (automatic, see below); the device wouldn't
//     stream, or no frame came within 6 s (automatic); or the preview stayed black ("No
//     picture", observed).
//
// No /dev/video* at all decides failed at once, with what the Mac has in its place in the
// evidence, because that is what stock Omarchy gives too: the PCIe FaceTime HD camera
// (Broadcom 14e4:1570, most 2013–2017 MacBooks) has no driver in the kernel, it needs the
// out-of-tree facetimehd driver and its firmware, which Omarchy doesn't install (omacom/omarchy
// PR #11381 is open; research/findings/compat-ratings.yaml rates it 0.03); the T1 MacBook Pros'
// camera sits behind the T1, which needs firmware Omarchy's install wipes; and the 2006–2008
// iSight (05ac:8300) needs firmware loaded first (isight-firmware, AUR). The live image mirrors
// stock Omarchy and doesn't add facetimehd either, so a pass here never stands for a driver an
// install wouldn't have. The UVC cameras (FaceTime HD 05ac:85xx on USB, the T2's through
// apple-bce) stream with the kernel's uvcvideo.

func init() {
	Register(Spec{ID: "camera.builtin", Group: NoPlug, Order: 80, Title: i18n.M("Camera"),
		Brief: i18n.M("wave at the camera"), Expect: 20 * time.Second,
		New: func(*Env) Run { return &camera{} }, Demo: func(f *Fake) {
			f.Set("/sys/class/video4linux/video0/name", "FaceTime HD Camera (Built-in)")
			f.Set("/sys/class/video4linux/video0/index", "0")
			f.Set("/sys/class/video4linux/video1/name", "FaceTime HD Camera (Built-in)")
			f.Set("/sys/class/video4linux/video1/index", "1")
		}})
}

// CamFrame is a camera picture in grey, scaled down: W×H bytes, row by row.
type CamFrame struct {
	W, H int
	Y    []byte
}

// CamInfo is what the camera streams.
type CamInfo struct {
	Format string // YUYV, MJPG
	W, H   int
}

// Cam is a streaming camera. Its frames arrive on its own goroutine; nothing blocks.
type Cam interface {
	Frame() (CamFrame, bool) // the newest frame (false before the first)
	Frames() int             // frames received so far
	Info() CamInfo
	Err() error // streaming stopped by itself
	Close()     // stop streaming and close the device; safe to call more than once
}

// CamOpening is a camera being opened (it takes a moment: formats, buffers, stream on).
type CamOpening interface {
	Ready() (Cam, error, bool)
	// Cancel: the screen was left before it opened; the camera closes itself once open.
	Cancel()
}

// camIO opens a camera device (/dev/video0).
type camIO interface {
	OpenCamera(dev string) CamOpening
}

func (s *System) OpenCamera(dev string) CamOpening {
	if !s.Real() {
		return &opening{c: Now(camOpen{err: ErrNotReal})}
	}
	o := &opening{}
	o.c = Go(func() camOpen {
		c, err := openV4L2(dev)
		o.mu.Lock()
		defer o.mu.Unlock()
		if err == nil && o.cancelled {
			c.Close()
		}
		return camOpen{c, err}
	})
	return o
}

type camOpen struct {
	cam Cam
	err error
}

type opening struct {
	c         Future[camOpen]
	mu        sync.Mutex
	cancelled bool
}

func (o *opening) Ready() (Cam, error, bool) {
	v, ok := o.c.Ready()
	return v.cam, v.err, ok
}

func (o *opening) Cancel() {
	o.mu.Lock()
	o.cancelled = true
	o.mu.Unlock()
	if v, ok := o.c.Ready(); ok && v.cam != nil {
		v.cam.Close()
	}
}

// camDev is a video device as sysfs lists it.
type camDev struct{ node, name string }

// builtinCamera is the camera's capture node: the main node (index 0) of a device named like
// Apple's built-in cameras, else the first main node.
func builtinCamera(io IO) (camDev, bool) {
	var first *camDev
	for _, d := range io.Glob("/sys/class/video4linux/video*") {
		if n, ok := io.Int(d + "/index"); ok && n != 0 {
			continue // a metadata node
		}
		c := camDev{node: "/dev/" + path.Base(d), name: io.Read(d + "/name")}
		for _, k := range []string{"FaceTime", "iSight", "Built-in"} {
			if strings.Contains(c.name, k) {
				return c, true
			}
		}
		if first == nil {
			first = &c
		}
	}
	if first == nil {
		return camDev{}, false
	}
	return *first, true
}

// missingCamera says what the Mac has in the camera's place when there's no video device:
// evidence (English), a line for "What I see" and Boot's line.
func missingCamera(io IO) (ev, seen, say string) {
	for _, d := range io.Glob("/sys/bus/pci/devices/*") {
		if io.Read(d+"/vendor") == "0x14e4" && io.Read(d+"/device") == "0x1570" {
			return fmt.Sprintf("no video device; the PCIe FaceTime HD camera (14e4:1570) at %s has no driver in the kernel: "+
					"it needs the out-of-tree facetimehd driver and firmware, which Omarchy doesn't install (omacom/omarchy PR #11381 open)", path.Base(d)),
				i18n.T("PCIe FaceTime HD (14e4:1570) at %s: no driver (needs facetimehd)", path.Base(d)),
				i18n.T("the FaceTime HD here needs facetimehd. omarchy doesn't ship it, so no camera.")
		}
	}
	for _, d := range io.Glob("/sys/bus/usb/devices/*") {
		if io.Read(d+"/idVendor") != "05ac" {
			continue
		}
		switch io.Read(d + "/idProduct") {
		case "8300":
			return "no video device; the iSight (05ac:8300) needs its firmware loaded first (isight-firmware), which Omarchy doesn't install",
				i18n.T("iSight (05ac:8300) on USB %s: waiting for its firmware", path.Base(d)),
				i18n.T("the iSight wants firmware omarchy doesn't load. no camera.")
		case "8600":
			return "no video device; the camera sits behind the T1 (iBridge 05ac:8600), which isn't running its camera firmware",
				i18n.T("T1 iBridge (05ac:8600) on USB %s: no camera behind it", path.Base(d)),
				i18n.T("the camera hides behind the T1, and the T1 isn't sharing. no camera.")
		}
	}
	return "no video device (/sys/class/video4linux is empty) and no known Apple camera on PCI or USB",
		i18n.T("no Apple camera on PCI or USB either"),
		i18n.T("no camera device at all. the kernel doesn't see one.")
}

type camera struct {
	dev    camDev
	gone   string // what's there instead, when there's no video device
	opener CamOpening
	cam    Cam
	openAt float64 // when it started streaming
	first  float64 // when the first frame came (-1: not yet)
	fps    float64
	mark   float64 // fps measuring: since when
	markN  int
}

const camFirstWait = 6.0 // seconds for the first frame

func (k *camera) Start(c *Ctx) {
	c.Select(-1) // the answers wait for the picture; a stray Enter mustn't pick one
	k.first = -1
	d, ok := builtinCamera(c.IO)
	if !ok {
		ev, seen, say := missingCamera(c.IO)
		k.gone = seen
		c.Decide(Outcome{Results: []checks.Result{Result("camera.builtin", checks.Failed, Automatic, ev, i18n.T("no video device"))}, Say: say})
		return
	}
	k.dev = d
	cio, ok := c.IO.(camIO)
	if !ok {
		c.Decide(Outcome{Results: []checks.Result{NotTested("camera.builtin", checks.Other, "no camera access in this mode", i18n.T("no camera access here"))}})
		return
	}
	k.opener = cio.OpenCamera(d.node)
}

func (k *camera) Step(c *Ctx) {
	if k.cam == nil {
		if k.opener == nil {
			return
		}
		cam, err, ok := k.opener.Ready()
		if !ok {
			return
		}
		k.opener = nil
		if err != nil {
			k.failed(c, err)
			return
		}
		k.cam, k.openAt, k.mark = cam, c.T, c.T
		return
	}
	if err := k.cam.Err(); err != nil {
		k.failed(c, err)
		return
	}
	n := k.cam.Frames()
	if n > 0 && k.first < 0 {
		k.first = c.T
	}
	if c.T-k.mark >= 1 {
		k.fps, k.mark, k.markN = float64(n-k.markN)/(c.T-k.mark), c.T, n
	}
	if k.first < 0 && c.T-k.openAt >= camFirstWait {
		k.close()
		in := k.cam.Info()
		c.Decide(Outcome{Results: []checks.Result{Result("camera.builtin", checks.Failed, Automatic,
			fmt.Sprintf("%s (%s) started streaming %s %dx%d, and no frame came in %.0f s", k.dev.node, k.dev.name, in.Format, in.W, in.H, camFirstWait),
			i18n.T("streaming, but no frames"))},
			Say: i18n.T("the camera says it's streaming, but sends nothing. that's a fail.")})
	}
}

func (k *camera) failed(c *Ctx, err error) {
	k.close()
	if errors.Is(err, ErrNotReal) {
		c.Decide(Outcome{Results: []checks.Result{NotTested("camera.builtin", checks.Other, "fixture run: no camera is opened", i18n.T("not on a Mac"))},
			Say: i18n.T("not on a real Mac, so no camera. skipped.")})
		return
	}
	c.Decide(Outcome{Results: []checks.Result{Result("camera.builtin", checks.Failed, Automatic,
		fmt.Sprintf("%s (%s): %v", k.dev.node, k.dev.name, err), i18n.T("the camera wouldn't stream"))},
		Say: i18n.T("the camera wouldn't stream. that's a fail.")})
}

func (k *camera) close() {
	if k.opener != nil {
		k.opener.Cancel()
		k.opener = nil
	}
	if k.cam != nil {
		k.cam.Close()
	}
}

func (k *camera) Stop(*Ctx) { k.close() }

func (k *camera) evidence(saw string) string {
	in := k.cam.Info()
	return fmt.Sprintf("%s (%s) streamed %s %dx%d, %d frames at about %.0f fps; the tester %s",
		k.dev.node, k.dev.name, in.Format, in.W, in.H, k.cam.Frames(), k.fps, saw)
}

// ready: the answers wait until the preview has run for a moment.
func (k *camera) ready(c *Ctx) bool { return k.first >= 0 && c.T-k.first >= 2 }

func (k *camera) Act(c *Ctx, i int) {
	if !k.ready(c) {
		return
	}
	var o Outcome
	switch i {
	case 0:
		o = Outcome{Results: []checks.Result{Result("camera.builtin", checks.Supported, Observed, k.evidence("saw themselves move in the preview"),
			i18n.T("%s, picture seen", k.cam.Info().Format))}, Say: i18n.T("there you are. the camera works.")}
	case 1:
		o = Outcome{Results: []checks.Result{Result("camera.builtin", checks.Partial, Observed, k.evidence("saw a wrong picture (frozen, garbled, upside down or far too dark)"),
			i18n.T("frames, but a wrong picture"))}, Say: i18n.T("frames come, but not a right picture. partial.")}
	case 2:
		o = Outcome{Results: []checks.Result{Result("camera.builtin", checks.Failed, Observed, k.evidence("saw no picture (black)"),
			i18n.T("frames, but no picture"))}, Say: i18n.T("frames, but nothing in them. that's a fail.")}
	default:
		return
	}
	k.close()
	c.Decide(o)
}

func (k *camera) View(c *Ctx) Screen {
	s := Screen{Face: boot.Idle, OutTitle: i18n.T("Found"),
		Steps:   []string{i18n.T("Look at the camera, at the top of the screen."), i18n.T("Wave at it."), i18n.T("Pick what the picture up top shows.")},
		Happens: i18n.T("Up top is what the camera sees: grey, coarse and mirrored."),
		Say:     i18n.T("say cheese. well, wave."), SayAt: 0.2,
		Hint: i18n.T("the picture is rough: a hand waved close to the lens shows best.")}
	if k.gone != "" {
		s.Live = []Line{Bad(i18n.T("no video device (/dev/video*)")), Gone(k.gone)}
	}
	if k.dev.node == "" {
		s.Steps, s.Happens, s.Hint = []string{i18n.T("No camera to look at.")}, "", ""
		if k.gone != "" { // the lens that won't open (prototype v54)
			s.Anim = lensShut(k.gone)
		}
		return s
	}
	s.Live = []Line{Fact(i18n.T("%s: %s", path.Base(k.dev.node), k.dev.name))}
	s.Anim = camLens(i18n.T("opening the camera"))
	switch {
	case k.cam == nil:
		s.Live = append(s.Live, Wait(i18n.T("opening the camera")))
		s.Face, s.Waiting = boot.Work, "·  "+i18n.T("opening the camera")
	case k.first < 0:
		in := k.cam.Info()
		s.Live = append(s.Live, Wait(i18n.T("streaming %s %d×%d, waiting for the first frame", in.Format, in.W, in.H)))
		s.Face, s.Waiting = boot.Work, "·  "+i18n.T("waiting for the first frame")
		s.Anim = camLens(i18n.T("waiting for the first frame"))
	default:
		in := k.cam.Info()
		s.Step = 1
		s.Live = append(s.Live, Fact(i18n.T("streaming %s %d×%d, about %.0f fps", in.Format, in.W, in.H, k.fps)))
		if fr, ok := k.cam.Frame(); ok {
			s.Anim = camPreview(fr)
		}
		s.Waiting = "·  " + i18n.T("streaming")
	}
	if k.ready(c) {
		s.Step = 2
		s.Say, s.SayAt = i18n.T("is that you? wave again if you're not sure."), k.first+2
		s.Waiting = "·  " + i18n.T("waiting for your answer")
	}
	if c.Outcome() != nil {
		s.Step = len(s.Steps)
	}
	off := !k.ready(c)
	s.Actions = []Action{{Label: i18n.T("Yes, I see myself move"), Does: i18n.T("a rough grey me, following along"), Off: off},
		{Label: i18n.T("The picture is wrong"), Does: i18n.T("frozen, garbled, upside down or dark"), Off: off},
		{Label: i18n.T("No picture"), Does: i18n.T("it stays black"), Off: off}}
	return s
}

// camLens is the camera test's animation before the first frame: the camera's lens at the top
// of a screen, and what the test waits for.
func camLens(what string) Panel {
	return func(gr *g.Grid, x, y, w, h int, t float64) {
		sw := min(30, w/2)
		if sw < 12 || h < 6 {
			return
		}
		sx := x + 2
		outline(gr, sx, y+1, sw, h-1, g.DIM)
		cx := sx + sw/2
		lc := g.BCYN
		if int(t*2)%2 == 0 {
			lc = g.CYN
		}
		gr.Put(cx-1, y+2, "(", g.DIM)
		gr.Put(cx, y+2, "•", lc)
		gr.Put(cx+1, y+2, ")", g.DIM)
		gr.Put(cx, y+4, "↑", g.DIM)
		lbl := i18n.T("the camera")
		gr.Put(cx-len([]rune(lbl))/2, y+5, lbl, g.DIM)
		gr.Put(sx+sw+4, y+3, cutRunes(what, x+w-sx-sw-4), g.GRY)
	}
}

// ── Pictures ──────────────────────────────────────────────────────────────────

// frameMaxW, frameMaxH are the largest a CamFrame is kept: plenty for the console's half-blocks.
const frameMaxW, frameMaxH = 128, 96

// camShrink is the grey picture w×h (at(x, y) its pixels, 0–255) box-averaged down to fit
// frameMaxW×frameMaxH.
func camShrink(w, h int, at func(x, y int) byte) CamFrame {
	step := max(1, (w+frameMaxW-1)/frameMaxW, (h+frameMaxH-1)/frameMaxH)
	fw, fh := w/step, h/step
	f := CamFrame{W: fw, H: fh, Y: make([]byte, fw*fh)}
	for y := 0; y < fh; y++ {
		for x := 0; x < fw; x++ {
			sum := 0
			for j := 0; j < step; j++ {
				for i := 0; i < step; i++ {
					sum += int(at(x*step+i, y*step+j))
				}
			}
			f.Y[y*fw+x] = byte(sum / (step * step))
		}
	}
	return f
}

// greyYUYV is a YUYV (4:2:2) picture's brightness: every other byte is Y.
func greyYUYV(b []byte, w, h, stride int) (CamFrame, error) {
	if stride < 2*w {
		stride = 2 * w
	}
	if w <= 0 || h <= 0 || len(b) < stride*(h-1)+2*w {
		return CamFrame{}, fmt.Errorf("short YUYV frame: %d bytes for %dx%d", len(b), w, h)
	}
	return camShrink(w, h, func(x, y int) byte { return b[y*stride+2*x] }), nil
}

// greyMJPEG decodes a MJPEG frame (a JPEG, often without its Huffman tables: UVC cameras leave
// out the standard ones) and keeps its brightness.
func greyMJPEG(b []byte) (CamFrame, error) {
	img, err := jpeg.Decode(bytes.NewReader(withDHT(b)))
	if err != nil {
		return CamFrame{}, err
	}
	r := img.Bounds()
	if yc, ok := img.(*image.YCbCr); ok {
		return camShrink(r.Dx(), r.Dy(), func(x, y int) byte { return yc.Y[yc.YOffset(r.Min.X+x, r.Min.Y+y)] }), nil
	}
	return camShrink(r.Dx(), r.Dy(), func(x, y int) byte {
		cr, cg, cb, _ := img.At(r.Min.X+x, r.Min.Y+y).RGBA()
		return byte((299*cr + 587*cg + 114*cb) / 1000 >> 8)
	}), nil
}

// standardDHT is the JPEG standard's Huffman tables (ITU T.81 Annex K) as a DHT segment,
// taken from what Go's own encoder writes.
var standardDHT = sync.OnceValue(func() []byte {
	var buf bytes.Buffer
	jpeg.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 8, 8)), nil)
	var out []byte
	forSegments(buf.Bytes(), func(marker byte, seg []byte) bool {
		if marker == 0xC4 {
			out = append(out, seg...)
		}
		return true
	})
	return out
})

// forSegments walks a JPEG's marker segments up to the scan (SOS): fn gets each marker and
// the whole segment (FF xx and its length included); false stops. It reports whether the scan
// was reached and where it starts.
func forSegments(b []byte, fn func(marker byte, seg []byte) bool) (sos int, ok bool) {
	if len(b) < 4 || b[0] != 0xFF || b[1] != 0xD8 {
		return 0, false
	}
	for p := 2; p+4 <= len(b); {
		if b[p] != 0xFF {
			return 0, false
		}
		m := b[p+1]
		if m == 0xDA {
			return p, true
		}
		n := int(b[p+2])<<8 | int(b[p+3])
		if p+2+n > len(b) || !fn(m, b[p:p+2+n]) {
			return 0, false
		}
		p += 2 + n
	}
	return 0, false
}

// withDHT is the JPEG with the standard Huffman tables put in before the scan when it has none.
func withDHT(b []byte) []byte {
	has := false
	sos, ok := forSegments(b, func(m byte, _ []byte) bool {
		has = has || m == 0xC4
		return true
	})
	if !ok || has {
		return b
	}
	out := make([]byte, 0, len(b)+500)
	out = append(out, b[:sos]...)
	out = append(out, standardDHT()...)
	return append(out, b[sos:]...)
}

// The preview's greys: the console has four (and only the first eight colours as a
// background, so of these only K and GRY can be the lower half's).
var greys = [4]g.Color{g.K, g.DIM, g.GRY, g.WHT}

// bayer is the 4×4 ordered-dither matrix: four greys look like more.
var bayer = [4][4]int{{0, 8, 2, 10}, {12, 4, 14, 6}, {3, 11, 1, 9}, {15, 7, 13, 5}}

// halfCell is one cell showing two pixels' grey levels (0–3), top and bottom: the upper half
// block in the top's colour over the bottom's as the background. A level that can't be a
// background (DIM, WHT) goes to the top, or, when both can't, the bottom moves to the nearest
// one that can.
func halfCell(top, bot int) (rune, g.Color, g.Color) {
	bgOK := func(l int) bool { return l == 0 || l == 2 }
	switch {
	case top == bot && top == 0:
		return ' ', g.K, g.K
	case top == bot:
		return '█', greys[top], g.K
	case bgOK(bot):
		return '▀', greys[top], greys[bot]
	case bgOK(top):
		return '▄', greys[bot], greys[top]
	}
	return '▀', greys[top], greys[bot-1] // DIM → K, WHT → GRY
}

// camLevels are a frame's pixels as grey levels 0–3 at cols×rows (rows: two per cell), mirrored,
// stretched to the picture's own range (2nd to 98th percentile) and dithered.
func camLevels(f CamFrame, cols, rows int) [][]int {
	vals := append([]byte(nil), f.Y...)
	sort.Slice(vals, func(i, j int) bool { return vals[i] < vals[j] })
	lo, hi := float64(vals[len(vals)*2/100]), float64(vals[len(vals)*98/100])
	if hi-lo < 24 { // a flat picture stays flat: don't blow up the noise
		lo, hi = max(0, (lo+hi)/2-12), min(255, (lo+hi)/2+12)
	}
	out := make([][]int, rows)
	for y := range out {
		out[y] = make([]int, cols)
		sy := min(f.H-1, y*f.H/rows)
		for x := range out[y] {
			sx := f.W - 1 - min(f.W-1, x*f.W/cols) // mirrored
			v := (float64(f.Y[sy*f.W+sx]) - lo) / (hi - lo)
			v = math.Max(0, math.Min(1, v))
			out[y][x] = min(3, max(0, int(v*3+0.5+(float64(bayer[y%4][x%4])/16-0.47)*0.6)))
		}
	}
	return out
}

// camPreview draws a frame as half-blocks, as large as fits with square pixels, framed in dim.
func camPreview(f CamFrame) Panel {
	return func(gr *g.Grid, x, y, w, h int, _ float64) {
		if f.W == 0 || f.H == 0 || len(f.Y) < f.W*f.H || h < 3 || w < 6 {
			return
		}
		rows := 2 * (h - 2)
		cols := rows * f.W / f.H
		if cols > w-2 {
			cols = w - 2
			rows = (cols * f.H / f.W) &^ 1
		}
		if cols < 2 || rows < 2 {
			return
		}
		lv := camLevels(f, cols, rows)
		gr.Put(x, y, "┌"+strings.Repeat("─", cols)+"┐", g.DIM)
		for cy := 0; cy < rows/2; cy++ {
			gr.Put(x, y+1+cy, "│", g.DIM)
			for cx := 0; cx < cols; cx++ {
				r, fg, bg := halfCell(lv[2*cy][cx], lv[2*cy+1][cx])
				gr.SetBG(x+1+cx, y+1+cy, r, fg, bg)
			}
			gr.Put(x+1+cols, y+1+cy, "│", g.DIM)
		}
		gr.Put(x, y+1+rows/2, "└"+strings.Repeat("─", cols)+"┘", g.DIM)
	}
}

// ── The Fake's camera ─────────────────────────────────────────────────────────
//
// "v4l2:fail" = an error for opening; "v4l2:picture" = "frozen" (the same frame over and
// over), "black", or "" (someone swaying in front of a wall); "v4l2:frames" = "none" sends no
// frames at all.

func (f *Fake) OpenCamera(dev string) CamOpening {
	o := &fakeOpening{f: f, at: f.now + 0.4}
	if e := f.Read("v4l2:fail"); e != "" {
		o.err = errors.New(e)
		return o
	}
	f.opened(1)
	o.cam = &fakeCam{f: f, from: o.at}
	return o
}

type fakeOpening struct {
	f   *Fake
	at  float64
	cam *fakeCam
	err error
}

func (o *fakeOpening) Ready() (Cam, error, bool) {
	if o.f.now < o.at {
		return nil, nil, false
	}
	if o.cam == nil {
		return nil, o.err, true
	}
	return o.cam, nil, true
}

func (o *fakeOpening) Cancel() {
	if o.cam != nil {
		o.cam.Close()
	}
}

type fakeCam struct {
	f      *Fake
	from   float64
	closed bool
}

const fakeFPS = 15

func (k *fakeCam) Frames() int {
	if k.f.Read("v4l2:frames") == "none" || k.f.now < k.from {
		return 0
	}
	return int((k.f.now-k.from)*fakeFPS) + 1
}

func (k *fakeCam) Frame() (CamFrame, bool) {
	if k.Frames() == 0 {
		return CamFrame{}, false
	}
	t := k.f.now
	switch k.f.Read("v4l2:picture") {
	case "frozen":
		t = k.from
	case "black":
		return CamFrame{W: 64, H: 48, Y: make([]byte, 64*48)}, true
	}
	return fakePicture(t), true
}

func (k *fakeCam) Info() CamInfo { return CamInfo{Format: "YUYV", W: 320, H: 240} }
func (k *fakeCam) Err() error    { return nil }

func (k *fakeCam) Close() {
	if !k.closed {
		k.closed = true
		k.f.opened(-1)
	}
}

// fakePicture is a person swaying in front of a lit wall at t: a head and shoulders.
func fakePicture(t float64) CamFrame {
	const w, h = 96, 72
	f := CamFrame{W: w, H: h, Y: make([]byte, w*h)}
	cx := 48 + 16*math.Sin(t*1.4)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			v := 70 + 60*float64(x)/w + 20*float64(y)/h // the wall, lit from the side
			dx, dy := (float64(x)-cx)/13, (float64(y)-30)/16
			if dx*dx+dy*dy < 1 {
				v = 200 - 30*dy // the face, a little darker towards the chin
				for _, ex := range []float64{-5, 5} {
					if ax, ay := float64(x)-cx-ex, float64(y)-26; ax*ax+ay*ay < 4 {
						v = 40 // eyes
					}
				}
				if ax, ay := (float64(x)-cx)/5, float64(y)-38; ax*ax+ay*ay < 1 {
					v = 90 // mouth
				}
			}
			if sx, sy := (float64(x)-cx*0.7-14.4)/30, (float64(y)-80)/30; sx*sx+sy*sy < 1 {
				v = 35 // a dark shirt
			}
			if hx, hy := (float64(x)-cx)/14, (float64(y)-18)/9; hx*hx+hy*hy < 1 && float64(y) < 22 {
				v = 30 // hair
			}
			f.Y[y*w+x] = byte(min(255, max(0, v)))
		}
	}
	return f
}
