//go:build linux

package handson

import (
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"path"
	"sync"
	"time"
	"unsafe"

	"github.com/godbus/dbus/v5"
	"golang.org/x/sys/unix"

	"github.com/omaboot/boot-live/internal/checks"
)

// ── evdev ─────────────────────────────────────────────────────────────────────

// evSize is struct input_event: a struct timeval, then type, code (u16) and value (s32).
var evSize = int(unsafe.Sizeof(unix.Timeval{})) + 8

// eviocgrab is EVIOCGRAB, _IOW('E', 0x90, int).
const eviocgrab = 0x40044590

type evdev struct {
	dev  Device
	f    *os.File
	mu   sync.Mutex
	evs  []Event
	err  error
	once sync.Once
}

// openInput opens a device node and reads it on its own goroutine until Close. The file sits
// in Go's poller, so Close ends the blocked read.
func openInput(d Device, grab bool) (Input, error) {
	f, err := os.OpenFile(d.Path(), os.O_RDONLY, 0)
	if err != nil {
		return nil, err
	}
	in := &evdev{dev: d, f: f}
	if grab {
		rc, err := f.SyscallConn()
		if err == nil {
			cerr := rc.Control(func(fd uintptr) { err = unix.IoctlSetInt(int(fd), eviocgrab, 1) })
			if cerr != nil {
				err = cerr
			}
		}
		if err != nil {
			f.Close()
			return nil, fmt.Errorf("grab %s: %w", d.Event, err)
		}
	}
	go in.read()
	return in, nil
}

func (in *evdev) read() {
	buf := make([]byte, evSize*64)
	for {
		n, err := in.f.Read(buf)
		in.mu.Lock()
		for i := 0; i+evSize <= n; i += evSize {
			e := buf[i+evSize-8:]
			in.evs = append(in.evs, Event{Dev: in.dev.Event, Type: binary.LittleEndian.Uint16(e[0:]),
				Code: binary.LittleEndian.Uint16(e[2:]), Value: int32(binary.LittleEndian.Uint32(e[4:]))})
		}
		if len(in.evs) > 4096 { // nobody is reading: keep the newest
			in.evs = in.evs[len(in.evs)-4096:]
		}
		if err != nil {
			if !errors.Is(err, os.ErrClosed) {
				in.err = err
			}
			in.mu.Unlock()
			return
		}
		in.mu.Unlock()
	}
}

func (in *evdev) Events() []Event {
	in.mu.Lock()
	defer in.mu.Unlock()
	out := in.evs
	in.evs = nil
	return out
}

func (in *evdev) Err() error {
	in.mu.Lock()
	defer in.mu.Unlock()
	return in.err
}

// Close releases the device (and its grab, which goes with the file).
func (in *evdev) Close() { in.once.Do(func() { in.f.Close() }) }

// ── suspend ───────────────────────────────────────────────────────────────────

// asleep is CLOCK_BOOTTIME minus CLOCK_MONOTONIC: the time the system has spent suspended.
func asleep() time.Duration {
	var b, m unix.Timespec
	unix.ClockGettime(unix.CLOCK_BOOTTIME, &b)
	unix.ClockGettime(unix.CLOCK_MONOTONIC, &m)
	return time.Duration(b.Nano() - m.Nano())
}

const rtcAlarm = "/sys/class/rtc/rtc0/wakealarm"

// suspend sets the RTC alarm and writes "mem" ("freeze" for light sleep) to /sys/power/state,
// which returns once the Mac is back. The alarm is cleared afterwards either way. Around the write
// the kernel's suspend diagnostics are on (sleepdiag.go: its steps print on the screen, the
// pre-sleep diagnostics go to external storage first); a test suspend (how.Level) sets
// /sys/power/pm_test for the write and puts "none" back after; a deep one (how.Deep) sets
// /sys/power/mem_sleep to deep when the Mac's default is s2idle, and puts it back after.
func suspend(s *System, wake time.Duration, how SleepHow) Sleep {
	sl := Sleep{Mode: memSleep(s), Asked: wake, Success: -1, Light: how.Light}
	state := "mem"
	if how.Light {
		sl.Mode, state = "s2idle", "freeze"
	}
	if how.Deep && !how.Light {
		if m := sl.Mode; m != "deep" {
			if err := s.Write(memSleepPath, "deep"); err != nil {
				sl.Err = fmt.Errorf("couldn't set %s to deep: %w", memSleepPath, err)
				return sl
			}
			if m != "" {
				defer s.Write(memSleepPath, m)
			}
		}
		sl.Mode = "deep"
	}
	before := suspendCount(s)
	failed := s.Read("/sys/power/suspend_stats/fail")
	if s.Diag != nil { // first: saving can take a while on a slow drive, and the alarm counts from when it's set
		sl.Saved = s.Diag.Before(how, sl.Mode)
	}
	if wake > 0 {
		s.Write(rtcAlarm, "0")
		if err := s.Write(rtcAlarm, fmt.Sprintf("+%d", int(wake.Seconds()))); err != nil {
			sl.Err = fmt.Errorf("couldn't set the RTC wake alarm: %w", err)
			return sl
		}
	}
	if how.Level != "" {
		if err := s.Write(pmTest, how.Level); err != nil {
			sl.Err = fmt.Errorf("couldn't set %s to %s: %w", pmTest, how.Level, err)
			return sl
		}
		defer s.Write(pmTest, "none")
	}
	restore := pmDebugOn(s)
	screenNote(how, sl.Mode, wake)
	w0 := readWake(s)
	a0, t0 := asleep(), time.Now()
	sl.Err = os.WriteFile("/sys/power/state", []byte(state), 0)
	sl.Slept, sl.Took = asleep()-a0, time.Since(t0)
	restore()
	names, armed := acpiWakeup(s.Read("/proc/acpi/wakeup"))
	rtc := ""
	if l, err := os.Readlink("/sys/class/rtc/rtc0/device"); err == nil {
		rtc = path.Base(l)
	}
	sl.WokenBy, sl.Events = wokenBy(w0, readWake(s), names, rtc)
	sl.Armed, sl.Probed = armed, len(w0.sources)+len(w0.events) > 0
	if wake > 0 {
		s.Write(rtcAlarm, "0")
	}
	sl.WakeIRQ = s.Read("/sys/power/pm_wakeup_irq")
	if after := suspendCount(s); before >= 0 && after >= 0 {
		sl.Success = after - before
	}
	if f := s.Read("/sys/power/suspend_stats/fail"); f != failed {
		sl.Failed = s.Read("/sys/power/suspend_stats/last_failed_dev")
		if sl.Failed == "" {
			sl.Failed = s.Read("/sys/power/suspend_stats/last_failed_step")
		}
	}
	sl.Log = NapLog(checks.ReadKmsgTimed(s.Root))
	sl.Timing = PMTiming(sl.Log)
	if s.Diag != nil {
		s.Diag.After(how, sl)
	}
	return sl
}

// ── logind ────────────────────────────────────────────────────────────────────

// inhibitLid takes a logind "block" inhibitor on handle-lid-switch, so closing the lid doesn't
// make logind suspend the Mac as well; closing the returned descriptor releases it. Without
// logind (or D-Bus) nothing else reacts to the lid, and it does nothing.
func inhibitLid() func() {
	conn, err := dbus.ConnectSystemBus()
	if err != nil {
		return func() {}
	}
	var fd dbus.UnixFD
	err = conn.Object("org.freedesktop.login1", "/org/freedesktop/login1").Call("org.freedesktop.login1.Manager.Inhibit", 0,
		"handle-lid-switch", "OmaBoot? Live", "the lid test puts the Mac to sleep itself", "block").Store(&fd)
	if err != nil {
		conn.Close()
		return func() {}
	}
	var once sync.Once
	return func() {
		once.Do(func() {
			unix.Close(int(fd))
			conn.Close()
		})
	}
}
