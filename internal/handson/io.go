package handson

import (
	"errors"
	"os"
	"path"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/omaboot/boot-live/internal/sysroot"
)

// IO is the Mac as a test sees it: sysfs and procfs to read and (a few) to write, the input
// devices, and the slow things (sleep, the network) as futures. The real mode's is the Mac's
// (System); a --sysroot run reads a fixture tree and writes nothing; the demo and tools/screens
// use a scripted Fake.
type IO interface {
	Read(p string) string         // a file, trimmed; "" when unreadable
	Bytes(p string) []byte        // a file as it is (an EDID)
	Int(p string) (int, bool)     // a file holding one integer
	Glob(pattern string) []string // matching paths, sorted
	Write(p, value string) error  // a sysfs write (a fixture run keeps it in memory)
	Inputs() []Device             // the input devices (from sysfs)
	Open(d Device, grab bool) (Input, error)
	// Suspend puts the Mac to sleep with the RTC alarm set to wake it after wake (0: no
	// alarm), the way how says. It answers once the Mac is back (or the kernel refused).
	Suspend(wake time.Duration, how SleepHow) Future[Sleep]
	// NetNow is the network as the status line has it now, never blocking: on the Mac the
	// backend's own state (online through the link in use, by watchLink's rule: the link there
	// with its carrier, up, and its address; a default route through it and the site's name
	// resolving when it came up), so a test's "the network is back" and the status line can't
	// disagree (owner, 0.6.0 review: the lid test said so seconds before "Ethernet up").
	NetNow() Net
	// InhibitLid holds back logind's own lid handling (it would suspend the Mac too) until the
	// returned function is called.
	InhibitLid() (release func())
	Advance(t float64) // the run's clock moved to t: a Fake plays its script; the Mac's does nothing
	Real() bool        // writes and suspends reach the Mac
}

// Future is an answer still to come; Ready never blocks.
type Future[T any] interface {
	Ready() (T, bool)
}

// Go runs f off the screen's goroutine and answers with its result.
func Go[T any](f func() T) Future[T] {
	c := &chanFuture[T]{ch: make(chan T, 1)}
	go func() { c.ch <- f() }()
	return c
}

type chanFuture[T any] struct {
	ch   chan T
	v    T
	done bool
}

func (c *chanFuture[T]) Ready() (T, bool) {
	if !c.done {
		select {
		case c.v = <-c.ch:
			c.done = true
		default:
		}
	}
	return c.v, c.done
}

// Now is a future that is already answered.
func Now[T any](v T) Future[T] { return &chanFuture[T]{v: v, done: true} }

// Net is the network as the status line has it.
type Net struct {
	Up  bool
	Via string // the connection it's online through, "en0, Ethernet" (as Env.Online), or ""
	Why string // when not up: why ("no default route", "doesitomarchy.com didn't resolve")
}

// Sleep is how a suspend went.
type Sleep struct {
	Mode    string        // /sys/power/mem_sleep's choice: deep or s2idle
	Asked   time.Duration // the RTC alarm
	Slept   time.Duration // time spent suspended (CLOCK_BOOTTIME gained over CLOCK_MONOTONIC)
	Took    time.Duration // from the request to being back
	WakeIRQ string        // /sys/power/pm_wakeup_irq after resume, if the kernel says
	Success int           // suspend_stats: successful suspends gained (-1 unknown)
	Failed  string        // suspend_stats' last failed device or step, when one was added
	Err     error         // the kernel refused (EBUSY, EIO…) or the suspend couldn't be tried
	// What woke it (wake.go): the wakeup sources and ACPI events counted during the nap, and the
	// devices /proc/acpi/wakeup allows to wake the Mac. Probed: the counters were read.
	WokenBy, Events, Armed []string
	Probed                 bool
	// Light: the nap was light sleep (s2idle) by choice (SleepHow.Light), not the Mac's default.
	Light bool
	// The kernel's own account of the nap (sleepdiag.go): Timing is its steps' times, short, for
	// the evidence; Log its lines from "PM: suspend entry" on, for bugreport; Saved where the
	// diagnostics saved before it went (or why they weren't), "" when none were asked for.
	Timing string
	Log    []string
	Saved  string
}

// SleepHow says how a nap is taken.
type SleepHow struct {
	Test  string // the test (or console command) sleeping, for the diagnostics: "power.sleep-wake"
	Title string // its name, English, for the screen the kernel's steps print on
	Light bool   // light sleep (s2idle, "freeze") instead of the Mac's default
	// Level is a test suspend (/sys/power/pm_test: freezer, devices, platform, processors,
	// core): the kernel goes that far, waits about 5 s and comes back without sleeping.
	Level string
	// Deep is a real deep sleep whatever the Mac's default ("mem" with /sys/power/mem_sleep set
	// to deep for the nap and put back after; sleepcheck deep, 0.7.7).
	Deep bool
	// Unbound are the FireWire disks firewire_sbp2 was taken off for this nap (sleepcheck's
	// nosbp2 experiment), for the diagnostics; SBP2Way how (SBP2Unloaded, SBP2Autoprobe).
	Unbound []string
	SBP2Way string
}

// SleepDiag is told about each nap on the Mac (System.Diag): Before right before the kernel is
// asked to sleep (the pre-sleep diagnostics go to external storage; it says where, or why not),
// After once the Mac is back.
type SleepDiag interface {
	Before(how SleepHow, mode string) string
	After(how SleepHow, sl Sleep)
}

// ErrNotReal: a fixture run or the demo never puts the machine to sleep.
var ErrNotReal = errors.New("not on the Mac itself: nothing is put to sleep")

// System is the IO of the real mode: the Mac's files under root. On a fixture tree (--sysroot)
// it reads the tree and keeps writes in memory, opens no input device and never suspends.
type System struct {
	Root sysroot.Root
	// NetFn is the network as the status line has it now (NetNow); nil means "can't tell".
	NetFn func() Net
	// Diag, if set, hears of every nap (the pre-sleep diagnostics, the kernel's log of it).
	Diag SleepDiag

	mu      sync.Mutex
	written map[string]string // a fixture run's writes
}

func (s *System) Real() bool { return s.Root.IsSystem() }

func (s *System) overlay(p string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.written[p]
	return v, ok
}

func (s *System) Read(p string) string {
	if v, ok := s.overlay(p); ok {
		return v
	}
	return s.Root.Read(p)
}

func (s *System) Bytes(p string) []byte {
	if v, ok := s.overlay(p); ok {
		return []byte(v)
	}
	return s.Root.Bytes(p)
}

func (s *System) Int(p string) (int, bool) {
	n, err := strconv.Atoi(strings.TrimSpace(s.Read(p)))
	return n, err == nil
}

func (s *System) Glob(pattern string) []string { return s.Root.Glob(pattern) }

func (s *System) Write(p, v string) error {
	if !s.Real() {
		s.mu.Lock()
		if s.written == nil {
			s.written = map[string]string{}
		}
		s.written[p] = v
		s.mu.Unlock()
		return nil
	}
	return os.WriteFile(p, []byte(v), 0)
}

func (s *System) Inputs() []Device { return devices(s) }

func (s *System) Open(d Device, grab bool) (Input, error) {
	if !s.Real() {
		return nil, ErrNotReal
	}
	return openInput(d, grab)
}

func (s *System) Suspend(wake time.Duration, how SleepHow) Future[Sleep] {
	if !s.Real() {
		return Now(Sleep{Err: ErrNotReal})
	}
	return Go(func() Sleep { return suspend(s, wake, how) })
}

func (s *System) NetNow() Net {
	if s.NetFn == nil {
		return Net{Why: "can't tell here"}
	}
	return s.NetFn()
}

func (s *System) InhibitLid() func() {
	if !s.Real() {
		return func() {}
	}
	return inhibitLid()
}

func (s *System) Advance(float64) {}

// suspendState reads what suspend_stats says, for before/after (-1 when the kernel doesn't
// have it).
func suspendCount(io IO) int {
	if n, ok := io.Int("/sys/power/suspend_stats/success"); ok {
		return n
	}
	return -1
}

const memSleepPath = "/sys/power/mem_sleep"

// memSleep is the suspend mode "mem" uses: the bracketed one in /sys/power/mem_sleep.
func memSleep(io IO) string {
	for _, f := range strings.Fields(io.Read("/sys/power/mem_sleep")) {
		if strings.HasPrefix(f, "[") {
			return strings.Trim(f, "[]")
		}
	}
	return ""
}

// ── Input devices ─────────────────────────────────────────────────────────────

// Event types and codes the tests use (linux/input-event-codes.h).
const (
	EvSyn = 0x00
	EvKey = 0x01
	EvRel = 0x02
	EvAbs = 0x03
	EvMsc = 0x04
	EvSw  = 0x05

	SwLid = 0x00
)

// Device is one /dev/input/event* device, as sysfs describes it.
type Device struct {
	Event string // event3
	Name  string // "Apple Inc. Apple Internal Keyboard / Trackpad", "Lid Switch"
	Phys  string
	Caps  map[string][]uint64 // capabilities/{ev,key,sw,abs,rel,msc,led}: bit words, lowest first
}

// Path is the device node.
func (d Device) Path() string { return "/dev/input/" + d.Event }

// Has reports whether the device reports code under kind ("key", "sw", "abs", "rel", "ev").
func (d Device) Has(kind string, code int) bool {
	w := d.Caps[kind]
	i := code / 64
	return i < len(w) && w[i]&(1<<(code%64)) != 0
}

// devices lists the input devices from /sys/class/input/event*/device.
func devices(io IO) []Device {
	var out []Device
	for _, ev := range io.Glob("/sys/class/input/event*") {
		dir := ev + "/device"
		d := Device{Event: path.Base(ev), Name: io.Read(dir + "/name"), Phys: io.Read(dir + "/phys"), Caps: map[string][]uint64{}}
		for _, k := range []string{"ev", "key", "sw", "abs", "rel", "msc", "led"} {
			d.Caps[k] = bitmap(io.Read(dir + "/capabilities/" + k))
		}
		out = append(out, d)
	}
	sort.Slice(out, func(i, j int) bool { return eventNum(out[i].Event) < eventNum(out[j].Event) })
	return out
}

func eventNum(s string) int {
	n, _ := strconv.Atoi(strings.TrimPrefix(s, "event"))
	return n
}

// bitmap reads a sysfs capability bitmap ("120013 0 3", highest word first) into words, lowest
// first.
func bitmap(s string) []uint64 {
	f := strings.Fields(s)
	out := make([]uint64, len(f))
	for i, w := range f {
		v, _ := strconv.ParseUint(w, 16, 64)
		out[len(f)-1-i] = v
	}
	return out
}

// Event is one input event.
type Event struct {
	Dev   string // the device's event name (event3)
	Type  uint16
	Code  uint16
	Value int32
}

// Input is an open input device (or several). Events never blocks: it hands over what arrived
// since the last call. A device that goes away (unplugged, or re-made after a resume) ends its
// stream: Err says so, and the test can open it again.
type Input interface {
	Events() []Event
	Err() error
	Close()
}

// OpenAll opens every device want accepts, as one stream; devices that can't be opened are
// left out. grab takes them for this program alone: the console sees none of their keys (no VT
// switching, no Ctrl+Alt+Del, no keys reaching the screen's own handling) until Close.
func OpenAll(io IO, want func(Device) bool, grab bool) (Input, []Device) {
	var ins []Input
	var ds []Device
	for _, d := range io.Inputs() {
		if !want(d) {
			continue
		}
		if in, err := io.Open(d, grab); err == nil {
			ins = append(ins, in)
			ds = append(ds, d)
		}
	}
	return multi(ins), ds
}

type multi []Input

func (m multi) Events() []Event {
	var out []Event
	for _, in := range m {
		out = append(out, in.Events()...)
	}
	return out
}

func (m multi) Err() error {
	for _, in := range m {
		if err := in.Err(); err != nil {
			return err
		}
	}
	return nil
}

func (m multi) Close() {
	for _, in := range m {
		in.Close()
	}
}
