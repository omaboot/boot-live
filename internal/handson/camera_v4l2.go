//go:build linux && amd64

package handson

import (
	"encoding/binary"
	"errors"
	"fmt"
	"sync"
	"time"
	"unsafe"

	"golang.org/x/sys/unix"
)

// V4L2 streaming capture with mmap'd buffers, by ioctl: enough of linux/videodev2.h for a UVC
// camera. The structs are byte arrays at their amd64 offsets (the live image is x86_64 only):
// v4l2_capability 104 bytes, v4l2_fmtdesc 64, v4l2_format 208 (its union starts at 8),
// v4l2_requestbuffers 20, v4l2_buffer 88.
const (
	vidiocQuerycap  = 0x80685600 // _IOR('V', 0, v4l2_capability)
	vidiocEnumFmt   = 0xC0405602 // _IOWR('V', 2, v4l2_fmtdesc)
	vidiocSFmt      = 0xC0D05605 // _IOWR('V', 5, v4l2_format)
	vidiocReqbufs   = 0xC0145608 // _IOWR('V', 8, v4l2_requestbuffers)
	vidiocQuerybuf  = 0xC0585609 // _IOWR('V', 9, v4l2_buffer)
	vidiocQbuf      = 0xC058560F // _IOWR('V', 15, v4l2_buffer)
	vidiocDqbuf     = 0xC0585611 // _IOWR('V', 17, v4l2_buffer)
	vidiocStreamon  = 0x40045612 // _IOW('V', 18, int)
	vidiocStreamoff = 0x40045613 // _IOW('V', 19, int)

	v4l2BufTypeCapture = 1
	v4l2MemoryMmap     = 1
	v4l2FieldNone      = 1
	v4l2CapCapture     = 0x00000001
	v4l2CapStreaming   = 0x04000000
	v4l2CapDeviceCaps  = 0x80000000
	v4l2BufFlagError   = 0x40

	fourccYUYV = 'Y' | 'U'<<8 | 'Y'<<16 | 'V'<<24
	fourccMJPG = 'M' | 'J'<<8 | 'P'<<16 | 'G'<<24
)

var v4le = binary.LittleEndian

func v4l2ioctl(fd int, req uintptr, arg []byte) error {
	for {
		_, _, e := unix.Syscall(unix.SYS_IOCTL, uintptr(fd), req, uintptr(unsafe.Pointer(&arg[0])))
		switch e {
		case 0:
			return nil
		case unix.EINTR:
			continue
		}
		return e
	}
}

type v4l2Cam struct {
	fd     int
	bufs   [][]byte
	info   CamInfo
	stride int
	stop   chan struct{}
	once   sync.Once

	mu     sync.Mutex
	frame  CamFrame
	have   bool
	frames int
	err    error
}

// openV4L2 opens a capture node, picks YUYV at 320×240 (or MJPEG at 640×480 when the camera
// has no YUYV), maps four buffers and starts streaming; frames are read on a goroutine until
// Close.
func openV4L2(dev string) (Cam, error) {
	fd, err := unix.Open(dev, unix.O_RDWR|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", dev, err)
	}
	c := &v4l2Cam{fd: fd, stop: make(chan struct{})}
	if err := c.setup(); err != nil {
		c.release()
		return nil, err
	}
	go c.run()
	return c, nil
}

func (c *v4l2Cam) setup() error {
	cp := make([]byte, 104)
	if err := v4l2ioctl(c.fd, vidiocQuerycap, cp); err != nil {
		return fmt.Errorf("VIDIOC_QUERYCAP: %w", err)
	}
	caps := v4le.Uint32(cp[84:])
	if caps&v4l2CapDeviceCaps != 0 {
		caps = v4le.Uint32(cp[88:])
	}
	if caps&v4l2CapCapture == 0 || caps&v4l2CapStreaming == 0 {
		return errors.New("not a streaming capture device")
	}
	formats := map[uint32]bool{}
	for i := uint32(0); i < 64; i++ {
		fd := make([]byte, 64)
		v4le.PutUint32(fd[0:], i)
		v4le.PutUint32(fd[4:], v4l2BufTypeCapture)
		if v4l2ioctl(c.fd, vidiocEnumFmt, fd) != nil {
			break
		}
		formats[v4le.Uint32(fd[44:])] = true
	}
	pix, w, h := uint32(fourccYUYV), 320, 240
	if !formats[fourccYUYV] {
		if !formats[fourccMJPG] {
			return errors.New("the camera offers neither YUYV nor MJPEG")
		}
		pix, w, h = fourccMJPG, 640, 480
	}
	f := make([]byte, 208)
	v4le.PutUint32(f[0:], v4l2BufTypeCapture)
	v4le.PutUint32(f[8:], uint32(w))
	v4le.PutUint32(f[12:], uint32(h))
	v4le.PutUint32(f[16:], pix)
	v4le.PutUint32(f[20:], v4l2FieldNone)
	if err := v4l2ioctl(c.fd, vidiocSFmt, f); err != nil {
		return fmt.Errorf("VIDIOC_S_FMT: %w", err)
	}
	c.info = CamInfo{W: int(v4le.Uint32(f[8:])), H: int(v4le.Uint32(f[12:]))}
	c.stride = int(v4le.Uint32(f[24:]))
	switch v4le.Uint32(f[16:]) {
	case fourccYUYV:
		c.info.Format = "YUYV"
	case fourccMJPG:
		c.info.Format = "MJPG"
	default:
		return errors.New("the camera took neither YUYV nor MJPEG")
	}
	rb := make([]byte, 20)
	v4le.PutUint32(rb[0:], 4)
	v4le.PutUint32(rb[4:], v4l2BufTypeCapture)
	v4le.PutUint32(rb[8:], v4l2MemoryMmap)
	if err := v4l2ioctl(c.fd, vidiocReqbufs, rb); err != nil {
		return fmt.Errorf("VIDIOC_REQBUFS: %w", err)
	}
	n := int(v4le.Uint32(rb[0:]))
	if n < 1 {
		return errors.New("the camera gave no buffers")
	}
	for i := 0; i < n; i++ {
		b := c.buffer(i)
		if err := v4l2ioctl(c.fd, vidiocQuerybuf, b); err != nil {
			return fmt.Errorf("VIDIOC_QUERYBUF: %w", err)
		}
		m, err := unix.Mmap(c.fd, int64(v4le.Uint32(b[64:])), int(v4le.Uint32(b[72:])), unix.PROT_READ|unix.PROT_WRITE, unix.MAP_SHARED)
		if err != nil {
			return fmt.Errorf("mmap: %w", err)
		}
		c.bufs = append(c.bufs, m)
		if err := v4l2ioctl(c.fd, vidiocQbuf, b); err != nil {
			return fmt.Errorf("VIDIOC_QBUF: %w", err)
		}
	}
	t := make([]byte, 4)
	v4le.PutUint32(t, v4l2BufTypeCapture)
	if err := v4l2ioctl(c.fd, vidiocStreamon, t); err != nil {
		return fmt.Errorf("VIDIOC_STREAMON: %w", err)
	}
	return nil
}

// buffer is a v4l2_buffer for buffer i (index, type, memory set).
func (c *v4l2Cam) buffer(i int) []byte {
	b := make([]byte, 88)
	v4le.PutUint32(b[0:], uint32(i))
	v4le.PutUint32(b[4:], v4l2BufTypeCapture)
	v4le.PutUint32(b[60:], v4l2MemoryMmap)
	return b
}

// run takes frames until Close: wait for one, convert it (MJPEG at most every 100 ms: decoding
// is the costly part on an old Mac), give the buffer back.
func (c *v4l2Cam) run() {
	defer c.release()
	var last time.Time
	for {
		select {
		case <-c.stop:
			return
		default:
		}
		pfd := []unix.PollFd{{Fd: int32(c.fd), Events: unix.POLLIN}}
		if n, err := unix.Poll(pfd, 200); n <= 0 || err != nil {
			continue
		}
		b := c.buffer(0)
		if err := v4l2ioctl(c.fd, vidiocDqbuf, b); err != nil {
			if errors.Is(err, unix.EAGAIN) {
				continue
			}
			c.fail(fmt.Errorf("VIDIOC_DQBUF: %w", err))
			return
		}
		i, used, flags := int(v4le.Uint32(b[0:])), int(v4le.Uint32(b[8:])), v4le.Uint32(b[12:])
		if i < len(c.bufs) && flags&v4l2BufFlagError == 0 && used > 0 {
			data := c.bufs[i][:min(used, len(c.bufs[i]))]
			var fr CamFrame
			var err error
			convert := true
			if c.info.Format == "MJPG" {
				convert = time.Since(last) >= 100*time.Millisecond
			}
			if convert {
				last = time.Now()
				if c.info.Format == "YUYV" {
					fr, err = greyYUYV(data, c.info.W, c.info.H, c.stride)
				} else {
					fr, err = greyMJPEG(data)
				}
			}
			c.mu.Lock()
			c.frames++
			if convert && err == nil {
				c.frame, c.have = fr, true
			}
			c.mu.Unlock()
		}
		if err := v4l2ioctl(c.fd, vidiocQbuf, b); err != nil {
			c.fail(fmt.Errorf("VIDIOC_QBUF: %w", err))
			return
		}
	}
}

func (c *v4l2Cam) fail(err error) {
	c.mu.Lock()
	c.err = err
	c.mu.Unlock()
}

// release stops the stream, unmaps the buffers and closes the device.
func (c *v4l2Cam) release() {
	t := make([]byte, 4)
	v4le.PutUint32(t, v4l2BufTypeCapture)
	v4l2ioctl(c.fd, vidiocStreamoff, t)
	for _, m := range c.bufs {
		unix.Munmap(m)
	}
	c.bufs = nil
	unix.Close(c.fd)
}

func (c *v4l2Cam) Frame() (CamFrame, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.frame, c.have
}

func (c *v4l2Cam) Frames() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.frames
}

func (c *v4l2Cam) Info() CamInfo { return c.info }

func (c *v4l2Cam) Err() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.err
}

// Close ends the stream; the reading goroutine lets go of the device within 200 ms.
func (c *v4l2Cam) Close() { c.once.Do(func() { close(c.stop) }) }
