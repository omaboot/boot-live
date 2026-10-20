//go:build linux

package checks

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"strconv"
	"strings"
	"time"
	"unsafe"

	"golang.org/x/sys/unix"
)

// DRM ioctls (include/uapi/drm/drm.h, drm_mode.h), x86_64 numbers. Only getters and
// WAIT_VBLANK, which need no DRM master, plus DROP_MASTER.
const (
	drmIoctlDropMaster    = 0x641f
	drmIoctlWaitVblank    = 0xc018643a // union drm_wait_vblank, 24 bytes
	drmIoctlGetResources  = 0xc04064a0 // struct drm_mode_card_res, 64 bytes
	drmIoctlGetCrtc       = 0xc06864a1 // struct drm_mode_crtc, 104 bytes
	drmIoctlGetEncoder    = 0xc01464a6 // struct drm_mode_get_encoder, 20 bytes
	drmIoctlGetConnector  = 0xc05064a7 // struct drm_mode_get_connector, 80 bytes
	drmVblankRelative     = 0x1
	drmVblankSecondary    = 0x20000000
	drmVblankHighCrtcMask = 0x3e
)

type drmModeCardRes struct {
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

type drmModeCrtc struct {
	SetConnectorsPtr uint64
	CountConnectors  uint32
	CrtcID, FbID     uint32
	X, Y             uint32
	GammaSize        uint32
	ModeValid        uint32
	Mode             drmModeInfo
}

type drmModeGetEncoder struct {
	EncoderID, EncoderType, CrtcID, PossibleCrtcs, PossibleClones uint32
}

type drmModeGetConnector struct {
	EncodersPtr, ModesPtr, PropsPtr, PropValuesPtr         uint64
	CountModes, CountProps, CountEncoders                  uint32
	EncoderID, ConnectorID, ConnectorType, ConnectorTypeID uint32
	Connection, MmWidth, MmHeight, Subpixel, Pad           uint32
}

// drmWaitVblank is union drm_wait_vblank: the request (type, sequence, signal) and the reply
// (type, sequence, tval_sec, tval_usec) share it; long is Go's int on Linux.
type drmWaitVblank struct {
	Type, Sequence uint32
	Sec, Usec      int
}

func drmIoctl(fd int, req uintptr, arg unsafe.Pointer) error {
	for {
		_, _, e := unix.Syscall(unix.SYS_IOCTL, uintptr(fd), req, uintptr(arg))
		if e == unix.EINTR {
			continue
		}
		if e != 0 {
			return e
		}
		return nil
	}
}

// panelTypes are the DRM connector types of a built-in panel, by the name sysfs gives them.
var panelTypes = map[string]uint32{"LVDS": 7, "eDP": 14, "DSI": 16}

// countVblanks counts the vblanks of the CRTC that drives connector conn ("eDP-1") on the DRM
// primary node card (/dev/dri/card1) for about a second, with relative DRM_IOCTL_WAIT_VBLANK
// waits. It only reads: it never sets a mode and never takes DRM master (opening a primary node
// nobody holds makes the opener master, so it drops that at once; as a non-master the connector
// query is read-only and forces no probe of the panel).
func countVblanks(ctx context.Context, card, conn string) scanout {
	s := scanout{Card: card, Conn: conn}
	typ, idStr, _ := strings.Cut(conn, "-")
	ctype, ok := panelTypes[typ]
	cid, err := strconv.Atoi(idStr)
	if !ok || err != nil {
		s.Skip = "can't read the connector name " + conn
		return s
	}
	fd, err := unix.Open("/dev/dri/"+card, unix.O_RDWR|unix.O_CLOEXEC, 0)
	if err != nil {
		s.Skip = fmt.Sprintf("can't open /dev/dri/%s (%v)", card, err)
		return s
	}
	defer unix.Close(fd)
	_ = drmIoctl(fd, drmIoctlDropMaster, nil) // EINVAL when someone else is master: fine

	var res drmModeCardRes
	if err := drmIoctl(fd, drmIoctlGetResources, unsafe.Pointer(&res)); err != nil {
		s.Skip = fmt.Sprintf("DRM_IOCTL_MODE_GETRESOURCES: %v", err)
		return s
	}
	nc, nn := int(res.CountCrtcs), int(res.CountConnectors)
	if nc == 0 || nn == 0 {
		s.Off = "the GPU has no CRTC or connector"
		return s
	}
	crtcs, conns := make([]uint32, nc), make([]uint32, nn)
	res = drmModeCardRes{CrtcIDPtr: uint64(uintptr(unsafe.Pointer(&crtcs[0]))), CountCrtcs: uint32(nc),
		ConnectorIDPtr: uint64(uintptr(unsafe.Pointer(&conns[0]))), CountConnectors: uint32(nn)}
	err = drmIoctl(fd, drmIoctlGetResources, unsafe.Pointer(&res))
	runtime.KeepAlive(crtcs)
	runtime.KeepAlive(conns)
	if err != nil {
		s.Skip = fmt.Sprintf("DRM_IOCTL_MODE_GETRESOURCES: %v", err)
		return s
	}
	nc, nn = min(nc, int(res.CountCrtcs)), min(nn, int(res.CountConnectors))

	// the panel's connector, and the encoder it's routed through
	var gc drmModeGetConnector
	found := false
	for _, id := range conns[:nn] {
		modes := make([]drmModeInfo, 1) // a mode buffer: never a count of 0, which asks for a probe
		gc = drmModeGetConnector{ConnectorID: id, CountModes: 1, ModesPtr: uint64(uintptr(unsafe.Pointer(&modes[0])))}
		err := drmIoctl(fd, drmIoctlGetConnector, unsafe.Pointer(&gc))
		runtime.KeepAlive(modes)
		if err == nil && gc.ConnectorType == ctype && int(gc.ConnectorTypeID) == cid {
			found = true
			break
		}
	}
	if !found {
		s.Skip = "the GPU doesn't list " + conn
		return s
	}
	if gc.EncoderID == 0 {
		s.Off = "no encoder routes " + conn + " to a CRTC"
		return s
	}
	enc := drmModeGetEncoder{EncoderID: gc.EncoderID}
	if err := drmIoctl(fd, drmIoctlGetEncoder, unsafe.Pointer(&enc)); err != nil {
		s.Skip = fmt.Sprintf("DRM_IOCTL_MODE_GETENCODER: %v", err)
		return s
	}
	if enc.CrtcID == 0 {
		s.Off = "no CRTC drives " + conn
		return s
	}
	pipe := -1
	for i, id := range crtcs[:nc] {
		if id == enc.CrtcID {
			pipe = i
		}
	}
	crtc := drmModeCrtc{CrtcID: enc.CrtcID}
	if pipe < 0 || drmIoctl(fd, drmIoctlGetCrtc, unsafe.Pointer(&crtc)) != nil {
		s.Skip = "can't read the CRTC that drives " + conn
		return s
	}
	s.Crtc = pipe
	if crtc.ModeValid == 0 {
		s.Off = "the CRTC that drives " + conn + " has no mode"
		return s
	}
	m := crtc.Mode
	s.Mode = fmt.Sprintf("%dx%d", m.Hdisplay, m.Vdisplay)
	s.ModeHz = float64(m.Vrefresh)
	if m.Htotal > 0 && m.Vtotal > 0 && m.Clock > 0 {
		hz := float64(m.Clock) * 1000 / (float64(m.Htotal) * float64(m.Vtotal))
		if m.Flags&0x10 != 0 { // DRM_MODE_FLAG_INTERLACE
			hz *= 2
		}
		if m.Flags&0x20 != 0 { // DRM_MODE_FLAG_DBLSCAN
			hz /= 2
		}
		s.ModeHz = hz
	}

	// relative waits for the next vblank, about a second's worth; the reply's counter and
	// timestamps give the rate, so a missed wake-up doesn't skew it
	typ32 := uint32(drmVblankRelative)
	switch {
	case pipe == 1:
		typ32 |= drmVblankSecondary
	case pipe > 1:
		typ32 |= uint32(pipe<<1) & drmVblankHighCrtcMask
	}
	wait := func() (drmWaitVblank, error) {
		v := drmWaitVblank{Type: typ32, Sequence: 1}
		err := drmIoctl(fd, drmIoctlWaitVblank, unsafe.Pointer(&v))
		return v, err
	}
	first, err := wait()
	switch {
	case errors.Is(err, unix.EBUSY): // the kernel's 3 s wait ran out
		s.Off = "no vblank on its CRTC in 3 s"
		return s
	case errors.Is(err, unix.EINVAL), errors.Is(err, unix.EOPNOTSUPP), errors.Is(err, unix.ENOTTY):
		s.Skip = fmt.Sprintf("DRM_IOCTL_WAIT_VBLANK isn't supported on CRTC %d (%v)", pipe, err)
		return s
	case err != nil:
		s.Skip = fmt.Sprintf("DRM_IOCTL_WAIT_VBLANK: %v", err)
		return s
	}
	last := first
	start := time.Now()
	for time.Since(start) < time.Second && ctx.Err() == nil {
		v, err := wait()
		if errors.Is(err, unix.EBUSY) {
			s.Off = "its CRTC stopped sending vblanks"
			return s
		}
		if err != nil {
			break
		}
		last = v
	}
	s.Vblanks = int(last.Sequence - first.Sequence)
	t0 := float64(first.Sec) + float64(first.Usec)/1e6
	t1 := float64(last.Sec) + float64(last.Usec)/1e6
	s.Secs = t1 - t0
	if s.Secs <= 0 { // a driver without vblank timestamps: the wall clock's
		s.Secs = time.Since(start).Seconds()
	}
	return s
}
