package handson

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// readVirt reads a scripted disk through the real read, on its own clock.
func readVirt(d FakeDisk, size int64) DiskRead {
	if d.MBps == 0 {
		d.MBps = 100
	}
	v := &virtDisk{d: d, size: size, seen: map[int64]int{}}
	return readPasses(v, readHow{Base: "arch", Sector: 512}, func() time.Duration { return v.now }, func(ReadProgress) bool { return true })
}

// Each way a read can go wrong fails it; the image is checked against its own list, and an
// image without one (or with a list for another file) falls back to reading twice.
func TestReadPasses(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		d    FakeDisk
		size int64
		way  string
		kind string // "" for a good read
		note string
	}{
		{"read twice", FakeDisk{}, fakeStickSize, WayTwice, "", ""},
		{"passes differ", FakeDisk{Flaky: 7}, fakeStickSize, WayTwice, failDiffer, ""},
		{"I/O error", FakeDisk{IOErrAt: 18 << 20}, fakeStickSize, WayTwice, failIO, ""},
		{"short read", FakeDisk{Short: 20 << 20}, fakeStickSize, WayTwice, failShort, ""},
		{"small disk, read whole", FakeDisk{}, 16 << 20, WayTwice, "", ""},
		{"image matches", FakeDisk{Image: true}, fakeStickSize, WayImage, "", ""},
		{"image mismatch", FakeDisk{Image: true, BadChunk: 12}, fakeStickSize, WayImage, failImage, ""},
		{"image without a list: read twice", FakeDisk{Image: true, NoList: true}, fakeStickSize, WayTwice, "", "without checksums"},
		{"image, I/O error", FakeDisk{Image: true, IOErrAt: 20 << 20}, fakeStickSize, WayImage, failIO, ""},
		{"no medium", FakeDisk{}, 0, "", "", ""},
	}
	for _, c := range cases {
		r := readVirt(c.d, c.size)
		if r.Way != c.way || r.Kind != c.kind {
			t.Errorf("%s: way %q kind %q (%s), want %q %q", c.name, r.Way, r.Kind, r.Fail, c.way, c.kind)
		}
		if r.OK() != (c.kind == "" && c.way != "") {
			t.Errorf("%s: OK() = %v", c.name, r.OK())
		}
		if c.note != "" && !strings.Contains(r.Note, c.note) {
			t.Errorf("%s: note %q lacks %q", c.name, r.Note, c.note)
		}
		if c.kind != "" && r.Fail == "" {
			t.Errorf("%s: failed with no reason", c.name)
		}
	}
	if r := readVirt(FakeDisk{}, 16<<20); r.Bytes != 16<<20 {
		t.Errorf("small disk: read %d bytes", r.Bytes)
	}
	if r := readVirt(FakeDisk{}, 0); !r.NoMedium || r.OK() {
		t.Errorf("no medium: %+v", r)
	}
}

// The checksum list: the format the image build writes, and lists that aren't it.
func TestParseChunks(t *testing.T) {
	head := make([]byte, 3<<20)
	fakeFill(head, 0, 1)
	l, err := parseChunks(ChunkList(head, 5<<20))
	if err != nil || l.Size != 5<<20 || l.Chunk != chunkLen || len(l.Sums) != 3 || l.File != "airootfs.sfs" {
		t.Fatalf("parseChunks: %+v, %v", l, err)
	}
	ok := "omaboot-chunks 1\nsize 10\nchunk 1048576\n0 " + strings.Repeat("ab", 32) + "\n"
	for name, s := range map[string]string{
		"another format":  strings.Replace(ok, "chunks 1", "chunks 2", 1),
		"out of order":    strings.Replace(ok, "\n0 ", "\n1 ", 1),
		"not a sha256":    strings.Replace(ok, strings.Repeat("ab", 32), "xyz", 1),
		"no chunks":       "omaboot-chunks 1\nsize 10\nchunk 1048576\n",
		"no format line":  strings.TrimPrefix(ok, "omaboot-chunks 1\n"),
		"three fields":    ok + "2 a b\n",
		"no size":         strings.Replace(ok, "size 10\n", "", 1),
		"negative chunks": strings.Replace(ok, "chunk 1048576", "chunk -1", 1),
	} {
		if _, err := parseChunks([]byte(s)); err == nil {
			t.Errorf("%s: parsed", name)
		}
	}
	if _, err := parseChunks([]byte(ok)); err != nil {
		t.Errorf("ok: %v", err)
	}
}

// blockingDisk answers until limit bytes in, then blocks until released.
type blockingDisk struct {
	limit   int64
	release chan struct{}
}

func (b *blockingDisk) Size() int64 { return fakeStickSize }
func (b *blockingDisk) ReadAt(p []byte, off int64) (int, error) {
	if off+int64(len(p)) > b.limit {
		<-b.release
	}
	fakeFill(p, off, 3)
	return len(p), nil
}

// A read stuck in the kernel still ends: no progress for 5 s fails it (the stall rule is the
// job's, by its clock), and the device's late answer changes nothing.
func TestReadStall(t *testing.T) {
	var now atomic.Int64
	clock := func() time.Duration { return time.Duration(now.Load()) }
	dev := &blockingDisk{limit: 12 << 20, release: make(chan struct{})}
	var wg sync.WaitGroup
	wg.Add(1)
	j := startJob("sdc", clock, func(prog func(ReadProgress) bool) DiskRead {
		defer wg.Done()
		return readPasses(dev, readHow{Base: "arch", Sector: 512}, clock, prog)
	})
	for j.Progress().Done < 12<<20 {
		time.Sleep(time.Millisecond)
	}
	now.Store(int64(4 * time.Second))
	if _, ok := j.Ready(); ok {
		t.Fatal("ready after 4 s without progress")
	}
	now.Store(int64(5 * time.Second))
	r, ok := j.Ready()
	if !ok || r.Kind != failStall || r.OK() || r.At != 12<<20 {
		t.Fatalf("after 5 s: %v %+v", ok, r)
	}
	close(dev.release)
	wg.Wait()
	if r2, _ := j.Ready(); r2.Kind != failStall {
		t.Errorf("the late answer replaced the stall: %+v", r2)
	}
	// and the whole read's limit
	if r, ok := stalled("sdc", ReadProgress{Pass: 2, Done: 1 << 20}, 0, readLimit-time.Second, readLimit); !ok || r.Kind != failSlow {
		t.Errorf("over the limit: %v %+v", ok, r)
	}
	if _, ok := stalled("sdc", ReadProgress{}, 0, 3*time.Second, 7*time.Second); ok {
		t.Error("stalled with progress 4 s ago")
	}
}

// A port whose read failed never lets the USB test pass while it stands: with a map (the
// Air) and without (Done); a good read on the same port afterwards resolves it.
func TestFailedReadNeverPasses(t *testing.T) {
	t.Parallel()
	air := &Env{Ports: DemoAirPorts()}
	onRight := func(d FakeDisk) func(*Fake) {
		return func(f *Fake) {
			demoXHCI(f)
			f.At(3.5, func(f *Fake) {
				fakeUSBDev(f, "3-2", "0781:5567", "SanDisk", "Cruzer Blade", "480", "08")
				FakeDiskOn(f, "3-2", "sdc", 15631122432, d)
			})
		}
	}
	fails := map[string]FakeDisk{
		"I/O error":     {MBps: 21, IOErrAt: 18 << 20},
		"short read":    {MBps: 21, Short: 9 << 20},
		"passes differ": {MBps: 30, Flaky: 3},
		"stall":         {MBps: 21, StallAt: 6 << 20},
	}
	for name, d := range fails {
		for _, until := range []float64{10, 20, 40} {
			if o := playOn(t, air, DemoMac, "ports.usb-a", until, onRight(d), map[float64]int{1: 0}); status(o) != "undecided" {
				t.Errorf("%s, map, %.0f s: %s, want undecided while the failed read stands", name, until, status(o))
			}
		}
		o := playOn(t, air, DemoMac, "ports.usb-a", 30, onRight(d), map[float64]int{1: 0, 25: 0}) // Done
		if got := status(o); got != "partial/automatic" || !strings.Contains(o.Results[0].Evidence, "enumerated, but a 32 MB read of sdc failed") {
			t.Errorf("%s, map, Done: %s (%v)", name, got, o)
		}
		o = playOn(t, &Env{}, DemoMac, "ports.usb-a", 30, onRight(d), map[float64]int{25: 0})
		if got := status(o); got != "partial/automatic" {
			t.Errorf("%s, no map, Done: %s", name, got)
		}
	}
	// the same stick reads fine when it goes back in
	o := playOn(t, air, DemoMac, "ports.usb-a", 20, func(f *Fake) { DemoUSBRead(f, "fail") }, map[float64]int{1: 0})
	if got := status(o); got != "supported/automatic" || !strings.Contains(o.Results[0].Evidence, "an I/O error 18 MiB into pass 1") {
		t.Errorf("retried: %s (%v)", status(o), o)
	}
	// and on another port: the failure points at the first one (the iMac, no map)
	o = playOn(t, &Env{}, func(*Fake) {}, "ports.usb-a", 20, func(f *Fake) {
		DemoIMac(f, "ports.usb-c") // the OmaBoot? stick in a Thunderbolt 3 port, nothing scripted
		cruzer := func(dev string, d FakeDisk) func(*Fake) {
			return func(f *Fake) {
				fakeUSBDev(f, dev, "0781:5567", "SanDisk", "Cruzer Blade", "480", "08")
				f.Set(usbDevices+dev+"/serial", "4C530001220528115034")
				FakeDiskOn(f, dev, nextFakeDisk(f), 15631122432, d)
			}
		}
		f.At(3, cruzer("1-2", FakeDisk{MBps: 21, IOErrAt: 18 << 20}))
		f.At(7, func(f *Fake) { fakeUnplug(f, "1-2") })
		f.At(8, cruzer("1-3", FakeDisk{MBps: 21}))
	}, map[float64]int{15: 0})
	if got := status(o); got != "partial/automatic" || !strings.Contains(o.Results[0].Evidence, "then read fine on 1-3 / 2-3, so the failure points at this port") {
		t.Errorf("points at the port: %s (%v)", got, o)
	}
}

// The OmaBoot? stick: its own read checks against the image; booted with copytoram it's found
// by the image's UUID, moves to the other port and is checked there; a mouse counts by its
// moves.
func TestUSBReadFlows(t *testing.T) {
	air := &Env{Ports: DemoAirPorts()}
	cases := []struct {
		name, variant string
		until         float64
		want          string
		ev            []string
	}{
		{"copytoram: the stick moves", "ram", 20, "supported/automatic", []string{
			"3-1 / 4-1: Kingston DataTraveler 3.0 (0951:1666) at 480 Mb/s (USB 2): the OmaBoot? drive: 32 MiB of airootfs.sfs read from sdb, verified against the image's checksums",
			"3-2 / 4-2: Kingston DataTraveler 3.0 (0951:1666) at 480 Mb/s (USB 2): the OmaBoot? drive: 32 MiB of airootfs.sfs read from sdc, verified against the image's checksums",
			"booted with copytoram"}},
		{"a mouse moves", "mouse", 10, "supported/automatic", []string{"Logitech USB Optical Mouse (046d:c077) at 1.5 Mb/s (USB 1.1): 2 input events from it (a move or a key)"}},
	}
	for _, c := range cases {
		o := playOn(t, air, DemoMac, "ports.usb-a", c.until, func(f *Fake) { DemoUSBRead(f, c.variant) }, map[float64]int{1: 0})
		if got := status(o); got != c.want {
			t.Errorf("%s: %s, want %s", c.name, got, c.want)
			continue
		}
		for _, e := range c.ev {
			if !strings.Contains(o.Results[0].Evidence, e) {
				t.Errorf("%s: evidence %q lacks %q", c.name, o.Results[0].Evidence, e)
			}
		}
	}
	f := NewFake()
	DemoUSBRead(f, "ram")
	if !copyToRAM(f) || bootDisk(f) != "sdb" || bootStickUSB(f) != "3-1" {
		t.Errorf("copytoram: %v %q %q", copyToRAM(f), bootDisk(f), bootStickUSB(f))
	}
	g := NewFake()
	demoXHCI(g)
	if copyToRAM(g) || bootDisk(g) != "sdb" {
		t.Errorf("no copytoram: %v %q", copyToRAM(g), bootDisk(g))
	}
}

// Nothing in this package can write a disk: the raw read's open flags are read-only (also
// diskread_linux_test.go), no file is opened for writing, created or written at an offset.
// Three files open character devices for writing, which their ioctls need, by fixed paths that
// can't be a disk.
func TestNoDiskWrites(t *testing.T) {
	charDevs := map[string]string{
		"camera_v4l2.go":     "a V4L2 capture node (/dev/video*): VIDIOC_* need O_RDWR",
		"extdisplay_kms.go":  "a DRM card (/dev/dri/card*): KMS needs O_RDWR",
		"sleepnote_linux.go": "the console the kernel moves to while the Mac sleeps (/dev/tty63): a heading written on it",
	}
	files, _ := filepath.Glob("*.go")
	fset := token.NewFileSet()
	opens := 0
	for _, fn := range files {
		if strings.HasSuffix(fn, "_test.go") || charDevs[fn] != "" {
			continue
		}
		f, err := parser.ParseFile(fset, fn, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(f, func(n ast.Node) bool {
			switch x := n.(type) {
			case *ast.Ident:
				switch x.Name {
				case "O_WRONLY", "O_RDWR", "O_CREAT", "O_TRUNC", "O_APPEND", "WriteAt", "Pwrite", "BLKROSET":
					t.Errorf("%s: %s", fset.Position(x.Pos()), x.Name)
				}
			case *ast.SelectorExpr:
				if pkg, ok := x.X.(*ast.Ident); ok && (pkg.Name == "os" && x.Sel.Name == "Create" ||
					(pkg.Name == "unix" || pkg.Name == "syscall") && (x.Sel.Name == "Open" || x.Sel.Name == "Openat")) {
					t.Errorf("%s: %s.%s (disks open through openRaw only)", fset.Position(x.Pos()), pkg.Name, x.Sel.Name)
				}
			case *ast.CallExpr:
				if sel, ok := x.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "OpenFile" && len(x.Args) == 3 {
					opens++
					flags := exprString(x.Args[1])
					if !strings.Contains(flags, "O_RDONLY") && flags != "rawFlags" && flags != "rawFlagsCached" && flags != "opticalFlags" {
						t.Errorf("%s: OpenFile with %s", fset.Position(x.Pos()), flags)
					}
				}
			}
			return true
		})
	}
	if opens == 0 {
		t.Error("no OpenFile found: the check checks nothing")
	}
}

func exprString(e ast.Expr) string {
	switch x := e.(type) {
	case *ast.Ident:
		return x.Name
	case *ast.SelectorExpr:
		return exprString(x.X) + "." + x.Sel.Name
	case *ast.BinaryExpr:
		return exprString(x.X) + x.Op.String() + exprString(x.Y)
	}
	return "?"
}

// Findings, not fails: a USB 3 device at USB 2 speed on a USB 3 port, a slow read at 5 Gb/s.
func TestUSBFindings(t *testing.T) {
	read := func(d usbDev, took time.Duration) *devCheck {
		return &devCheck{Dev: d, State: proofData, Read: &DiskRead{Way: WayTwice, Bytes: 32 << 20, Took: took}}
	}
	p := &usbPort{Key: "3-2 / 4-2", Checks: []*devCheck{
		read(usbDev{Name: "Ultra Fit", Version: "3.00", Speed: "480"}, time.Second),
		read(usbDev{Name: "Slow Stick", Version: "3.00", Speed: "5000"}, 2*time.Second),
		read(usbDev{Name: "Fast Stick", Version: "3.10", Speed: "5000"}, 300*time.Millisecond),
		read(usbDev{Name: "Old Stick", Version: "2.00", Speed: "480"}, 2*time.Second),
		read(usbDev{Name: "Disk 20", Version: "2.10", SuperSpeed: true, Speed: "480"}, time.Second), // USB 3 on a USB 2 link
	}}
	got := strings.Join(p.findings(), "\n")
	for _, want := range []string{"Ultra Fit (a USB 3 device) came up at USB 2 speed", "Disk 20 (a USB 3 device) came up", "Slow Stick read at 16.8 MB/s at 5 Gb/s"} {
		if !strings.Contains(got, want) {
			t.Errorf("findings %q lack %q", got, want)
		}
	}
	if strings.Contains(got, "Fast Stick") || strings.Contains(got, "Old Stick") {
		t.Errorf("findings %q", got)
	}
	if f := (&usbPort{Key: "3-1 / 4-1", Boot: true, Checks: p.Checks[1:2]}).findings(); len(f) != 0 { // the OmaBoot? drive's port
		t.Errorf("OmaBoot? drive: %q", f)
	}
	if f := (&usbPort{Key: "3-1 / 4-1", Boot: true, Checks: p.Checks[4:5]}).findings(); len(f) != 1 { // its link speed still counts
		t.Errorf("OmaBoot? drive at USB 2 speed: %q", f)
	}
	// bos_descriptors as the kernel gives them: a USB 3 stick, a USB 3 hub's USB 2 half (bcdUSB
	// 2.10 at 480 Mb/s, still SuperSpeed), a USB 2 device's (USB 2.0 extension only).
	for _, c := range []struct {
		bos  []byte
		want bool
	}{
		{[]byte{0x05, 0x0f, 0x16, 0x00, 0x02, 0x07, 0x10, 0x02, 0x06, 0x00, 0x00, 0x00, 0x0a, 0x10, 0x03, 0x00, 0x0e, 0x00, 0x03, 0x00, 0x00, 0x00}, true},
		{[]byte{0x05, 0x0f, 0x49, 0x00, 0x05, 0x07, 0x10, 0x02, 0x06, 0x00, 0x00, 0x00, 0x0a, 0x10, 0x03, 0x00, 0x0e, 0x00, 0x01, 0x04, 0xe7, 0x00}, true},
		{[]byte{0x05, 0x0f, 0x0c, 0x00, 0x01, 0x07, 0x10, 0x02, 0x06, 0x00, 0x00, 0x00}, false},
		{nil, false},
	} {
		if got := bosSuperSpeed(c.bos); got != c.want {
			t.Errorf("bosSuperSpeed(% x) = %v", c.bos, got)
		}
	}
	if f := (&usbPort{Key: "2-1.3", Checks: p.Checks[:1]}).findings(); len(f) != 0 { // a USB 2 port
		t.Errorf("USB 2 port: %q", f)
	}
}
