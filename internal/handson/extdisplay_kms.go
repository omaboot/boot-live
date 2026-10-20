//go:build linux

package handson

import (
	"errors"
	"fmt"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"unsafe"

	"golang.org/x/sys/unix"
)

// The external display's test card, by KMS with a dumb buffer: enough of drm.h and
// drm_mode.h to put one picture on one connector. The live image runs no compositor, so the
// tool can be DRM master on the connector's card while the console (fbcon on the DRM fbdev
// emulation) keeps drawing to the internal panel; fbdev doesn't modeset while someone else is
// master. The sequence (showCard), each step's failure ending in Close and an error:
//
//  1. open /dev/dri/cardN, DRM_IOCTL_SET_MASTER;
//  2. MODE_GETRESOURCES; MODE_GETCONNECTOR on each connector (as master, the first call probes
//     the external one, so its modes are fresh); MODE_GETENCODER for which CRTC drives which
//     connector; the internal panel's CRTCs (eDP, LVDS, DSI) are off limits;
//  3. the CRTC: the one already driving the external connector (the console's clone) when it
//     drives nothing else, saved with MODE_GETCRTC to put back; else a free one (no connector,
//     no mode) its encoders can use;
//  4. for the preferred mode (then up to two others): MODE_CREATE_DUMB (32 bpp), MODE_MAP_DUMB,
//     mmap, draw the card, MODE_ADDFB (depth 24), MODE_SETCRTC with the external connector only.
//
// Close undoes it in reverse: the CRTC put back as saved (or switched off when it was free, or
// when putting back fails), MODE_RMFB, munmap, MODE_DESTROY_DUMB, DRM_IOCTL_DROP_MASTER, close.
// Closing the last descriptor on the card makes fbdev restore the console's own setup.

// drm_mode.h's structs, laid out as the kernel's (sizes checked below).
type drmCardRes struct {
	FbIDPtr, CrtcIDPtr, ConnectorIDPtr, EncoderIDPtr     uint64
	CountFbs, CountCrtcs, CountConnectors, CountEncoders uint32
	MinWidth, MaxWidth, MinHeight, MaxHeight             uint32
}

type drmModeInfo struct {
	Clock                                         uint32
	Hdisplay, HsyncStart, HsyncEnd, Htotal, Hskew uint16
	Vdisplay, VsyncStart, VsyncEnd, Vtotal, Vscan uint16
	Vrefresh, Flags, Type                         uint32
	Name                                          [32]byte
}

type drmCrtc struct {
	SetConnectorsPtr                                          uint64
	CountConnectors, CrtcID, FbID, X, Y, GammaSize, ModeValid uint32
	Mode                                                      drmModeInfo
}

type drmEncoder struct {
	EncoderID, EncoderType, CrtcID, PossibleCrtcs, PossibleClones uint32
}

type drmConnector struct {
	EncodersPtr, ModesPtr, PropsPtr, PropValuesPtr   uint64
	CountModes, CountProps, CountEncoders            uint32
	EncoderID, ConnectorID, ConnectorType, TypeID    uint32
	Connection, MmWidth, MmHeight, Subpixel, Padding uint32
}

type drmCreateDumb struct {
	Height, Width, Bpp, Flags, Handle, Pitch uint32
	Size                                     uint64
}

type drmMapDumb struct {
	Handle, Pad uint32
	Offset      uint64
}

type drmFbCmd struct {
	FbID, Width, Height, Pitch, Bpp, Depth, Handle uint32
}

type drmFbDirty struct {
	FbID, Flags, Color, NumClips uint32
	ClipsPtr                     uint64
}

// Compile-time checks that the structs are the kernel's sizes.
var (
	_ [unsafe.Sizeof(drmCardRes{}) - 64]byte
	_ [64 - unsafe.Sizeof(drmCardRes{})]byte
	_ [unsafe.Sizeof(drmCrtc{}) - 104]byte
	_ [104 - unsafe.Sizeof(drmCrtc{})]byte
	_ [unsafe.Sizeof(drmModeInfo{}) - 68]byte
	_ [68 - unsafe.Sizeof(drmModeInfo{})]byte
	_ [unsafe.Sizeof(drmEncoder{}) - 20]byte
	_ [20 - unsafe.Sizeof(drmEncoder{})]byte
	_ [unsafe.Sizeof(drmConnector{}) - 80]byte
	_ [80 - unsafe.Sizeof(drmConnector{})]byte
	_ [unsafe.Sizeof(drmCreateDumb{}) - 32]byte
	_ [32 - unsafe.Sizeof(drmCreateDumb{})]byte
	_ [unsafe.Sizeof(drmFbCmd{}) - 28]byte
	_ [28 - unsafe.Sizeof(drmFbCmd{})]byte
	_ [unsafe.Sizeof(drmFbDirty{}) - 24]byte
	_ [24 - unsafe.Sizeof(drmFbDirty{})]byte
)

// drmIOWR is _IOWR('d', nr, size).
func drmIOWR(nr, size uintptr) uintptr { return 3<<30 | size<<16 | 'd'<<8 | nr }

var (
	drmSetMaster    = uintptr(0x641e) // _IO('d', 0x1e)
	drmDropMaster   = uintptr(0x641f) // _IO('d', 0x1f)
	drmGetResources = drmIOWR(0xA0, unsafe.Sizeof(drmCardRes{}))
	drmGetCrtc      = drmIOWR(0xA1, unsafe.Sizeof(drmCrtc{}))
	drmSetCrtc      = drmIOWR(0xA2, unsafe.Sizeof(drmCrtc{}))
	drmGetEncoder   = drmIOWR(0xA6, unsafe.Sizeof(drmEncoder{}))
	drmGetConnector = drmIOWR(0xA7, unsafe.Sizeof(drmConnector{}))
	drmAddFb        = drmIOWR(0xAE, unsafe.Sizeof(drmFbCmd{}))
	drmRmFb         = drmIOWR(0xAF, 4)
	drmDirtyFb      = drmIOWR(0xB1, unsafe.Sizeof(drmFbDirty{}))
	drmCreateDumbIo = drmIOWR(0xB2, unsafe.Sizeof(drmCreateDumb{}))
	drmMapDumbIo    = drmIOWR(0xB3, unsafe.Sizeof(drmMapDumb{}))
	drmDestroyDumb  = drmIOWR(0xB4, 4)
)

const (
	drmConnected     = 1
	drmModePreferred = 1 << 3
)

// drmConnectorNames are the kernel's names for connector types (drm_connector.c), as sysfs
// writes them in card1-DP-1.
var drmConnectorNames = []string{"Unknown", "VGA", "DVI-I", "DVI-D", "DVI-A", "Composite", "SVIDEO", "LVDS", "Component",
	"DIN", "DP", "HDMI-A", "HDMI-B", "TV", "eDP", "Virtual", "DSI", "DPI", "Writeback", "SPI", "USB"}

func drmIoctl(fd int, req uintptr, arg unsafe.Pointer) error {
	for {
		_, _, e := unix.Syscall(unix.SYS_IOCTL, uintptr(fd), req, uintptr(arg))
		switch e {
		case 0:
			return nil
		case unix.EINTR, unix.EAGAIN:
			continue
		}
		return e
	}
}

// ptr is a slice's first element's address for a struct's pointer field (0 for none).
func ptr[T any](s []T) uint64 {
	if len(s) == 0 {
		return 0
	}
	return uint64(uintptr(unsafe.Pointer(&s[0])))
}

type kmsCard struct {
	mu     sync.Mutex
	fd     int
	master bool

	conn   uint32
	crtc   uint32
	prev   *drmCrtc // the CRTC as it was, when the card took it over from the console
	set    bool     // the CRTC shows the card
	mode   drmModeInfo
	about  string
	handle uint32 // the dumb buffer
	fb     uint32
	mem    []byte // the buffer, mapped
	w, h   int
	pitch  int
	closed bool
}

// showCard puts the card up on conn (card1-DP-1) by KMS; see the top of the file.
func showCard(conn, code string) (Card, error) {
	cardName, connName, ok := strings.Cut(conn, "-")
	if !ok || !strings.HasPrefix(cardName, "card") {
		return nil, fmt.Errorf("not a DRM connector: %s", conn)
	}
	fd, err := unix.Open("/dev/dri/"+cardName, unix.O_RDWR|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, fmt.Errorf("open /dev/dri/%s: %w", cardName, err)
	}
	k := &kmsCard{fd: fd}
	fail := func(step string, err error) (Card, error) {
		k.Close()
		if err == nil {
			return nil, errors.New(step)
		}
		return nil, fmt.Errorf("%s: %w", step, err)
	}
	if err := drmIoctl(fd, drmSetMaster, nil); err != nil {
		return fail("DRM master refused (another program drives the display?)", err)
	}
	k.master = true

	// the card's CRTCs, connectors and encoders
	var res drmCardRes
	if err := drmIoctl(fd, drmGetResources, unsafe.Pointer(&res)); err != nil {
		return fail("MODE_GETRESOURCES", err)
	}
	crtcs := make([]uint32, res.CountCrtcs)
	conns := make([]uint32, res.CountConnectors)
	res2 := drmCardRes{CrtcIDPtr: ptr(crtcs), ConnectorIDPtr: ptr(conns), CountCrtcs: res.CountCrtcs, CountConnectors: res.CountConnectors}
	err = drmIoctl(fd, drmGetResources, unsafe.Pointer(&res2))
	runtime.KeepAlive(crtcs)
	runtime.KeepAlive(conns)
	if err != nil {
		return fail("MODE_GETRESOURCES", err)
	}
	crtcs, conns = crtcs[:min(res2.CountCrtcs, res.CountCrtcs)], conns[:min(res2.CountConnectors, res.CountConnectors)]

	// who drives what: connector → CRTC through its encoder
	crtcOf := func(enc uint32) uint32 {
		if enc == 0 {
			return 0
		}
		e := drmEncoder{EncoderID: enc}
		if drmIoctl(fd, drmGetEncoder, unsafe.Pointer(&e)) != nil {
			return 0
		}
		return e.CrtcID
	}
	users := map[uint32]int{}     // CRTC → connectors on it
	internal := map[uint32]bool{} // CRTCs driving the internal panel
	var target *drmConnector      // the external connector
	var encoders []uint32         // its encoders
	var modes []drmModeInfo       // its modes
	for _, id := range conns {
		c, encs, ms, err := getConnector(fd, id)
		if err != nil { // it might be the internal panel's: without knowing, touch nothing
			return fail(fmt.Sprintf("MODE_GETCONNECTOR %d", id), err)
		}
		name := "Unknown"
		if int(c.ConnectorType) < len(drmConnectorNames) {
			name = drmConnectorNames[c.ConnectorType]
		}
		name += "-" + strconv.Itoa(int(c.TypeID))
		on := crtcOf(c.EncoderID)
		if on != 0 {
			users[on]++
			if internalPanel(name) {
				internal[on] = true
			}
		}
		if name == connName {
			c := c
			target, encoders, modes = &c, encs, ms
		}
	}
	switch {
	case target == nil:
		return fail("no connector "+connName+" on "+cardName, nil)
	case target.Connection != drmConnected:
		return fail(connName+" isn't connected (KMS)", nil)
	case len(modes) == 0:
		return fail(connName+" lists no modes (KMS)", nil)
	}
	k.conn = target.ConnectorID

	// the CRTC: the connector's own (the console's clone) if nothing else is on it, else a free
	// one; never one the internal panel is on (moving the connector off it would modeset it)
	cur := crtcOf(target.EncoderID)
	if internal[cur] {
		return fail(connName+" shares the internal panel's CRTC", nil)
	}
	if cur != 0 && users[cur] == 1 {
		prev := drmCrtc{CrtcID: cur}
		if err := drmIoctl(fd, drmGetCrtc, unsafe.Pointer(&prev)); err != nil {
			return fail("MODE_GETCRTC", err)
		}
		k.crtc, k.prev = cur, &prev
	} else {
	find:
		for _, enc := range encoders {
			e := drmEncoder{EncoderID: enc}
			if drmIoctl(fd, drmGetEncoder, unsafe.Pointer(&e)) != nil {
				continue
			}
			for i, id := range crtcs {
				if e.PossibleCrtcs&(1<<i) == 0 || users[id] > 0 || internal[id] {
					continue
				}
				st := drmCrtc{CrtcID: id}
				if drmIoctl(fd, drmGetCrtc, unsafe.Pointer(&st)) != nil || st.ModeValid != 0 || st.FbID != 0 {
					continue
				}
				k.crtc = id
				break find
			}
		}
		if k.crtc == 0 {
			return fail("no free CRTC for "+connName+" (the internal panel's isn't touched)", nil)
		}
	}

	// the preferred mode first, then the kernel's order; up to three tries
	order := make([]drmModeInfo, 0, len(modes))
	for _, m := range modes {
		if m.Type&drmModePreferred != 0 {
			order = append(order, m)
		}
	}
	for _, m := range modes {
		if m.Type&drmModePreferred == 0 {
			order = append(order, m)
		}
	}
	var tried []string
	for i, m := range order {
		if i == 3 {
			break
		}
		err := k.up(m, code)
		if err == nil {
			how := "a free one"
			if k.prev != nil {
				how = "taken over from the console"
			}
			k.about = fmt.Sprintf("%dx%d %d Hz on CRTC %d (%s)", m.Hdisplay, m.Vdisplay, m.Vrefresh, k.crtc, how)
			return k, nil
		}
		tried = append(tried, fmt.Sprintf("%dx%d: %v", m.Hdisplay, m.Vdisplay, err))
	}
	return fail("MODE_SETCRTC refused "+strings.Join(tried, ", "), nil)
}

// getConnector reads a connector with its encoders and modes (two passes: counts, then lists).
func getConnector(fd int, id uint32) (drmConnector, []uint32, []drmModeInfo, error) {
	for range 3 {
		c := drmConnector{ConnectorID: id}
		if err := drmIoctl(fd, drmGetConnector, unsafe.Pointer(&c)); err != nil { // probes, as master
			return c, nil, nil, err
		}
		encs := make([]uint32, c.CountEncoders)
		modes := make([]drmModeInfo, c.CountModes)
		c2 := drmConnector{ConnectorID: id, EncodersPtr: ptr(encs), CountEncoders: c.CountEncoders, ModesPtr: ptr(modes), CountModes: c.CountModes}
		err := drmIoctl(fd, drmGetConnector, unsafe.Pointer(&c2))
		runtime.KeepAlive(encs)
		runtime.KeepAlive(modes)
		if err != nil {
			return c, nil, nil, err
		}
		if c2.CountEncoders <= c.CountEncoders && c2.CountModes <= c.CountModes { // nothing changed in between
			return c2, encs[:c2.CountEncoders], modes[:c2.CountModes], nil
		}
	}
	return drmConnector{}, nil, nil, errors.New("connector kept changing")
}

// up makes a buffer the mode's size, draws the card on it and shows it on the CRTC with the
// external connector only. On failure what it made is gone again.
func (k *kmsCard) up(m drmModeInfo, code string) error {
	w, h := int(m.Hdisplay), int(m.Vdisplay)
	if w == 0 || h == 0 {
		return errors.New("empty mode")
	}
	cd := drmCreateDumb{Width: uint32(w), Height: uint32(h), Bpp: 32}
	if err := drmIoctl(k.fd, drmCreateDumbIo, unsafe.Pointer(&cd)); err != nil {
		return fmt.Errorf("MODE_CREATE_DUMB: %w", err)
	}
	k.handle, k.w, k.h, k.pitch = cd.Handle, w, h, int(cd.Pitch)
	md := drmMapDumb{Handle: cd.Handle}
	if err := drmIoctl(k.fd, drmMapDumbIo, unsafe.Pointer(&md)); err != nil {
		k.freeBuffer()
		return fmt.Errorf("MODE_MAP_DUMB: %w", err)
	}
	mem, err := unix.Mmap(k.fd, int64(md.Offset), int(cd.Size), unix.PROT_READ|unix.PROT_WRITE, unix.MAP_SHARED)
	if err != nil {
		k.freeBuffer()
		return fmt.Errorf("mmap: %w", err)
	}
	k.mem = mem
	k.draw(code)
	fb := drmFbCmd{Width: uint32(w), Height: uint32(h), Pitch: cd.Pitch, Bpp: 32, Depth: 24, Handle: cd.Handle}
	if err := drmIoctl(k.fd, drmAddFb, unsafe.Pointer(&fb)); err != nil {
		k.freeBuffer()
		return fmt.Errorf("MODE_ADDFB: %w", err)
	}
	k.fb = fb.FbID
	conns := []uint32{k.conn}
	sc := drmCrtc{SetConnectorsPtr: ptr(conns), CountConnectors: 1, CrtcID: k.crtc, FbID: k.fb, ModeValid: 1, Mode: m}
	err = drmIoctl(k.fd, drmSetCrtc, unsafe.Pointer(&sc))
	runtime.KeepAlive(conns)
	if err != nil {
		k.freeBuffer()
		return fmt.Errorf("MODE_SETCRTC: %w", err)
	}
	k.set, k.mode = true, m
	return nil
}

// draw paints the card into the mapped buffer: drawn in memory, then copied in one go (the
// mapping is usually write-combined).
func (k *kmsCard) draw(code string) {
	px := drawCard(k.w, k.h, k.pitch, code)
	copy(k.mem, px)
}

// freeBuffer removes the framebuffer and the dumb buffer (the CRTC must not show it).
func (k *kmsCard) freeBuffer() {
	if k.fb != 0 {
		fb := k.fb
		drmIoctl(k.fd, drmRmFb, unsafe.Pointer(&fb))
		k.fb = 0
	}
	if k.mem != nil {
		unix.Munmap(k.mem)
		k.mem = nil
	}
	if k.handle != 0 {
		h := k.handle
		drmIoctl(k.fd, drmDestroyDumb, unsafe.Pointer(&h))
		k.handle = 0
	}
}

func (k *kmsCard) Code(code string) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.closed || k.mem == nil {
		return errors.New("the card is down")
	}
	k.draw(code)
	d := drmFbDirty{FbID: k.fb} // no clips: all of it; drivers that scan out directly don't need it
	drmIoctl(k.fd, drmDirtyFb, unsafe.Pointer(&d))
	return nil
}

func (k *kmsCard) About() string { return k.about }

// Close puts the CRTC back as it was (or off), frees the buffers, drops master and closes the
// card.
func (k *kmsCard) Close() {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.closed {
		return
	}
	k.closed = true
	if k.set {
		restored := false
		if p := k.prev; p != nil && p.ModeValid != 0 && p.FbID != 0 {
			conns := []uint32{k.conn}
			sc := drmCrtc{SetConnectorsPtr: ptr(conns), CountConnectors: 1, CrtcID: k.crtc, FbID: p.FbID, X: p.X, Y: p.Y, ModeValid: 1, Mode: p.Mode}
			restored = drmIoctl(k.fd, drmSetCrtc, unsafe.Pointer(&sc)) == nil
			runtime.KeepAlive(conns)
		}
		if !restored {
			off := drmCrtc{CrtcID: k.crtc} // no fb, no connectors, no mode: off
			drmIoctl(k.fd, drmSetCrtc, unsafe.Pointer(&off))
		}
		k.set = false
	}
	k.freeBuffer()
	if k.master {
		drmIoctl(k.fd, drmDropMaster, nil)
		k.master = false
	}
	unix.Close(k.fd)
}

// ── The card's picture ────────────────────────────────────────────────────────

// digitFont is 5×7 digits, a row per string, '#' lit.
var digitFont = [10][7]string{
	{" ### ", "#   #", "#  ##", "# # #", "##  #", "#   #", " ### "},
	{"  #  ", " ##  ", "  #  ", "  #  ", "  #  ", "  #  ", " ### "},
	{" ### ", "#   #", "    #", "   # ", "  #  ", " #   ", "#####"},
	{"#####", "   # ", "  #  ", "   # ", "    #", "#   #", " ### "},
	{"   # ", "  ## ", " # # ", "#  # ", "#####", "   # ", "   # "},
	{"#####", "#    ", "#### ", "    #", "    #", "#   #", " ### "},
	{"  ## ", " #   ", "#    ", "#### ", "#   #", "#   #", " ### "},
	{"#####", "    #", "   # ", "  #  ", " #   ", " #   ", " #   "},
	{" ### ", "#   #", "#   #", " ### ", "#   #", "#   #", " ### "},
	{" ### ", "#   #", "#   #", " ####", "    #", "   # ", " ##  "},
}

// drawCard is the card as XRGB8888 rows of pitch bytes: a near-black background, a white
// border (cut-off edges show), and the code's digits big and white in the middle.
func drawCard(w, h, pitch int, code string) []byte {
	px := make([]byte, pitch*h)
	fill := func(x0, y0, x1, y1 int, r, g, b byte) {
		x0, y0, x1, y1 = max(x0, 0), max(y0, 0), min(x1, w), min(y1, h)
		for y := y0; y < y1; y++ {
			row := px[y*pitch:]
			for x := x0; x < x1; x++ {
				row[4*x], row[4*x+1], row[4*x+2], row[4*x+3] = b, g, r, 0xff
			}
		}
	}
	fill(0, 0, w, h, 0x12, 0x12, 0x1a)
	t := max(4, min(w, h)/40)
	fill(0, 0, w, t, 0xf0, 0xf0, 0xf0)
	fill(0, h-t, w, h, 0xf0, 0xf0, 0xf0)
	fill(0, 0, t, h, 0xf0, 0xf0, 0xf0)
	fill(w-t, 0, w, h, 0xf0, 0xf0, 0xf0)
	n := len(code)
	if n == 0 {
		return px
	}
	cols := 6*n - 1 // 5 per digit, 1 between
	s := max(1, min(w*7/10/cols, h*45/100/7))
	x0, y0 := (w-cols*s)/2, (h-7*s)/2
	for i, ch := range code {
		if ch < '0' || ch > '9' {
			continue
		}
		g := digitFont[ch-'0']
		for row := range 7 {
			for col := range 5 {
				if g[row][col] == '#' {
					x := x0 + (6*i+col)*s
					y := y0 + row*s
					fill(x, y, x+s, y+s, 0xff, 0xff, 0xff)
				}
			}
		}
	}
	return px
}
