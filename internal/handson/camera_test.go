package handson

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"testing"

	g "github.com/omaboot/boot-live/internal/grid"
)

// Every pair of grey levels becomes a cell the console can show (a background from the first
// eight colours), and keeps both levels where it can.
func TestHalfCell(t *testing.T) {
	for top := 0; top < 4; top++ {
		for bot := 0; bot < 4; bot++ {
			r, fg, bg := halfCell(top, bot)
			if bg > 7 {
				t.Errorf("%d/%d: background %d", top, bot, bg)
			}
			var gt, gb g.Color
			switch r {
			case '▀':
				gt, gb = fg, bg
			case '▄':
				gt, gb = bg, fg
			case '█':
				gt, gb = fg, fg
			case ' ':
				gt, gb = bg, bg
			default:
				t.Fatalf("%d/%d: glyph %q", top, bot, r)
			}
			exact := !(top != bot && (top == 1 || top == 3) && (bot == 1 || bot == 3))
			if exact && (gt != greys[top] || gb != greys[bot]) {
				t.Errorf("%d/%d: shows %d/%d", top, bot, gt, gb)
			}
			if !exact && gt != greys[top] {
				t.Errorf("%d/%d: top shows %d", top, bot, gt)
			}
		}
	}
}

func TestGreyYUYV(t *testing.T) {
	// 4×2, stride 10 (padding): Y at every other byte
	b := []byte{10, 0, 20, 0, 30, 0, 40, 0, 9, 9, 50, 0, 60, 0, 70, 0, 80, 0, 9, 9}
	f, err := greyYUYV(b, 4, 2, 10)
	if err != nil || f.W != 4 || f.H != 2 || !bytes.Equal(f.Y, []byte{10, 20, 30, 40, 50, 60, 70, 80}) {
		t.Fatalf("%+v %v", f, err)
	}
	if _, err := greyYUYV(b[:12], 4, 2, 10); err == nil {
		t.Error("a short frame converted")
	}
	big := make([]byte, 640*480*2)
	if f, _ := greyYUYV(big, 640, 480, 1280); f.W > frameMaxW || f.H > frameMaxH || f.W == 0 {
		t.Errorf("640×480 kept at %d×%d", f.W, f.H)
	}
}

// A MJPEG frame without Huffman tables (as UVC cameras send them) decodes like the full JPEG.
func TestMJPEGWithoutDHT(t *testing.T) {
	img := image.NewGray(image.Rect(0, 0, 64, 48))
	for y := 0; y < 48; y++ {
		for x := 0; x < 64; x++ {
			img.SetGray(x, y, color.Gray{uint8(x * 4)})
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, nil); err != nil {
		t.Fatal(err)
	}
	full := buf.Bytes()
	var stripped []byte
	stripped = append(stripped, full[:2]...)
	sos, ok := forSegments(full, func(m byte, seg []byte) bool {
		if m != 0xC4 {
			stripped = append(stripped, seg...)
		}
		return true
	})
	if !ok {
		t.Fatal("no scan found")
	}
	stripped = append(stripped, full[sos:]...)
	if _, err := jpeg.Decode(bytes.NewReader(stripped)); err == nil {
		t.Fatal("the stripped JPEG still decodes: the test proves nothing")
	}
	a, err1 := greyMJPEG(full)
	b, err2 := greyMJPEG(stripped)
	if err1 != nil || err2 != nil || !bytes.Equal(a.Y, b.Y) || a.W != 64 {
		t.Fatalf("full %v, stripped %v", err1, err2)
	}
	if a.Y[0] > 20 || a.Y[63] < 200 {
		t.Errorf("gradient lost: %d … %d", a.Y[0], a.Y[63])
	}
}

func TestCameraRuns(t *testing.T) {
	cases := []struct {
		name   string
		script func(*Fake)
		picks  map[float64]int
		want   string
	}{
		{"seen", nil, map[float64]int{4: 0}, "supported/observed"},
		{"frozen", func(f *Fake) { f.Set("v4l2:picture", "frozen") }, map[float64]int{4: 1}, "partial/observed"},
		{"black", func(f *Fake) { f.Set("v4l2:picture", "black") }, map[float64]int{4: 2}, "failed/observed"},
		{"no frames", func(f *Fake) { f.Set("v4l2:frames", "none") }, nil, "failed/automatic"},
		{"won't stream", func(f *Fake) { f.Set("v4l2:fail", "VIDIOC_STREAMON: no space left on device") }, nil, "failed/automatic"},
		{"answers wait for the picture", nil, map[float64]int{1: 0}, "undecided"},
		{"FaceTime HD without facetimehd", func(f *Fake) {
			for p := range f.Files {
				if len(p) > 22 && p[:22] == "/sys/class/video4linux" {
					delete(f.Files, p)
				}
			}
			f.Set("/sys/bus/pci/devices/0000:02:00.0/vendor", "0x14e4")
			f.Set("/sys/bus/pci/devices/0000:02:00.0/device", "0x1570")
		}, nil, "failed/automatic"},
	}
	for _, c := range cases {
		s, _ := Lookup("camera.builtin")
		f := NewFake()
		DemoMac(f)
		s.Demo(f)
		if c.script != nil {
			c.script(f)
		}
		r := Begin(s, &Env{}, f)
		var o *Outcome
		for tt := 0.0; tt <= 10 && o == nil; tt += 0.1 {
			for at, a := range c.picks {
				if at > tt-0.1 && at <= tt {
					r.Act(a)
				}
			}
			r.Step(tt)
			r.View() // the preview draws from the frames
			o = r.Outcome()
		}
		r.Stop()
		if got := status(o); got != c.want {
			t.Errorf("%s: %s, want %s", c.name, got, c.want)
		}
		if n, _ := f.Int("alsa:open"); n != 0 {
			t.Errorf("%s: camera left open", c.name)
		}
	}
}

// The preview fits its box and draws only console-safe cells.
func TestCamPreview(t *testing.T) {
	gr := g.New(80, 20)
	camPreview(fakePicture(1))(gr, 2, 1, 60, 12, 0)
	n := 0
	for y := 0; y < 20; y++ {
		for x := 0; x < 80; x++ {
			c := gr.At(x, y)
			if c.BG > 7 {
				t.Fatalf("background %d at %d,%d", c.BG, x, y)
			}
			if c.R == '▀' || c.R == '▄' || c.R == '█' {
				n++
				if x < 3 || y < 2 || y > 12 {
					t.Fatalf("pixel outside the box at %d,%d", x, y)
				}
			}
		}
	}
	if n < 100 {
		t.Errorf("only %d picture cells", n)
	}
}
