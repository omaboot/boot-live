package checks

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/omaboot/boot-live/internal/identify"
	"github.com/omaboot/boot-live/internal/sysroot"
)

// Fix is a registered OmaBoot? fix that took effect on this Mac.
type Fix struct {
	ID    string // as in the fix registry (doesitomarchy data/fixes.yaml)
	Name  string // e.g. "radeon panel clock fix"
	Short string // the first word, for the log: radeon, applesmc
}

// Fix IDs the tool can detect.
const (
	FixRadeon   = "omaboot.radeon-imac10-1-panel-clock"
	FixApplesmc = "omaboot.applesmc-led-container-of"
	// FixLidWake: the lid's wake armed only when the lid is closed (the image's systemd-sleep hook
	// omaboot-lid-wake; the sleep test's second nap does the same). It takes effect in a hands-on
	// test, so the test sets it on its result rather than DetectFixes finding it.
	FixLidWake = "omaboot.lid-wake-closed-only"
	// FixTB3USBAwake: a Thunderbolt 3 chip's USB controller kept awake, since Linux doesn't wake
	// it for a plug-in on a Mac (no PCIe PME). The USB-C test does it and sets it on its result.
	FixTB3USBAwake = "omaboot.thunderbolt3-usb-awake"
)

// DetectFixes finds the OmaBoot? fixes that took effect, not just the ones on the image
// (LIVE-PLAN §14, step 2b):
//
//   - the radeon panel clock fix logs "iMac LVDS test: ATOM panel clock" when it corrects the
//     panel table;
//   - the applesmc fix is active when applesmc is loaded from the image's updates/ directory
//     (modules.dep, as modinfo -n resolves it) and registered smc::kbd_backlight, the LED whose
//     registration crashed without it.
//
// Only IDs in the catalog's fix registry are returned: the server rejects unknown ones.
func DetectFixes(r sysroot.Root, kmsg []string, cat *identify.Catalog) []Fix {
	var ids []string
	for _, l := range kmsg {
		if strings.Contains(l, "iMac LVDS test: ATOM panel clock") {
			ids = append(ids, FixRadeon)
			break
		}
	}
	if r.IsDir("/sys/module/applesmc") && r.Exists("/sys/class/leds/smc::kbd_backlight") && fromUpdates(r, "applesmc") {
		ids = append(ids, FixApplesmc)
	}
	var out []Fix
	for _, id := range ids {
		if cat == nil {
			continue
		}
		f := cat.Fix(id)
		if f == nil {
			continue
		}
		short, _, _ := strings.Cut(f.Name, " ")
		out = append(out, Fix{ID: id, Name: f.Name, Short: short})
	}
	return out
}

// fromUpdates reports whether depmod resolves a module to the updates/ directory, which is
// where the image's fixed modules go and which takes precedence over the kernel's own.
func fromUpdates(r sysroot.Root, module string) bool {
	kver := r.Read("/proc/sys/kernel/osrelease")
	for _, l := range r.Lines("/usr/lib/modules/" + kver + "/modules.dep") {
		p, _, _ := strings.Cut(l, ":")
		base := p[strings.LastIndex(p, "/")+1:]
		if name, _, _ := strings.Cut(base, ".ko"); name == module {
			return strings.HasPrefix(p, "updates/") || strings.Contains(p, "/updates/")
		}
	}
	return false
}

// ReadKmsg reads the whole kernel log (/dev/kmsg, from its oldest record) as messages. It reads
// everything and searches afterwards: never `dmesg | grep -q`, which can lose a match to SIGPIPE
// under pipefail. On the live system, when the ring buffer has already dropped the boot's first
// lines, the journal's copy of the kernel log is read too.
func ReadKmsg(r sysroot.Root) []string { return readKmsg(r, false) }

// ReadKmsgTimed is ReadKmsg with each record's time since boot in front, as dmesg shows it
// ("[    1.207] i915 …"), for the console's log command and the bugreport.
func ReadKmsgTimed(r sysroot.Root) []string { return readKmsg(r, true) }

// logRec is one kernel log record: its time since boot (µs, -1 when unknown) and message.
type logRec struct {
	us  int64
	msg string
}

func (k logRec) line(timed bool) string {
	if timed && k.us >= 0 {
		return fmt.Sprintf("[%5d.%03d] ", k.us/1e6, k.us%1e6/1e3) + k.msg
	}
	return k.msg
}

func readKmsg(r sysroot.Root, timed bool) []string {
	var recs []logRec
	// Raw syscalls, not os.File: Go puts a non-blocking fd into its poller, so at the end of
	// /dev/kmsg a Read would park until the kernel logs something new (it hung the tool on a
	// quiet system); syscall.Read returns EAGAIN there instead.
	fd, err := syscall.Open(r.P("/dev/kmsg"), syscall.O_RDONLY|syscall.O_NONBLOCK|syscall.O_CLOEXEC, 0)
	if err == nil {
		var all bytes.Buffer
		buf := make([]byte, 16<<10)
		for i := 0; i < 1<<16; i++ {
			n, err := syscall.Read(fd, buf)
			if n > 0 {
				all.Write(buf[:n])
			}
			if errors.Is(err, syscall.EPIPE) { // a record was overwritten while reading: go on
				continue
			}
			if err != nil || n <= 0 { // EAGAIN: the end of the log
				break
			}
		}
		syscall.Close(fd)
		for _, l := range strings.Split(all.String(), "\n") {
			if strings.HasPrefix(l, " ") || l == "" { // continuation lines: KEY=value
				continue
			}
			if head, msg, ok := strings.Cut(l, ";"); ok {
				recs = append(recs, logRec{kmsgUS(head), msg})
			} else {
				recs = append(recs, logRec{-1, l})
			}
		}
	}
	if r.IsSystem() && !startsAtBoot(recs) {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		b, _ := exec.CommandContext(ctx, "journalctl", "-k", "-b", "-o", "json", "--output-fields=MESSAGE,_SOURCE_MONOTONIC_TIMESTAMP", "--no-pager").Output()
		recs = withJournal(b, recs)
	}
	out := make([]string, len(recs))
	for i, k := range recs {
		out[i] = k.line(timed)
	}
	return out
}

// withJournal puts the journal's copy of the kernel log (journalctl -o json, one entry a line)
// in front of what /dev/kmsg still holds, up to kmsg's oldest record: the ring buffer dropped
// the boot's first lines (the Air's sleep tests' PM lines filled it), and the journal has them,
// with the kernel's own times. Without the cut, the stretch both hold came twice (0.7.12's Air run).
func withJournal(js []byte, recs []logRec) []logRec {
	from := int64(-1)
	if len(recs) > 0 {
		from = recs[0].us
	}
	var head []logRec
	for _, l := range bytes.Split(js, []byte("\n")) {
		var e struct {
			Msg json.RawMessage `json:"MESSAGE"`
			US  string          `json:"_SOURCE_MONOTONIC_TIMESTAMP"`
			Mon string          `json:"__MONOTONIC_TIMESTAMP"`
		}
		if json.Unmarshal(l, &e) != nil || len(e.Msg) == 0 {
			continue
		}
		us, err := strconv.ParseInt(e.US, 10, 64)
		if err != nil {
			if us, err = strconv.ParseInt(e.Mon, 10, 64); err != nil {
				us = -1
			}
		}
		if from >= 0 && us >= from {
			break
		}
		head = append(head, logRec{us, journalMsg(e.Msg)})
	}
	return append(head, recs...)
}

// journalMsg is a journal MESSAGE as /dev/kmsg writes it: a string, or an array of bytes when it
// isn't UTF-8; newlines inside as \x0a.
func journalMsg(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) != nil {
		var b []byte
		var ints []int
		if json.Unmarshal(raw, &ints) == nil {
			for _, n := range ints {
				b = append(b, byte(n))
			}
		}
		s = string(b)
	}
	return strings.ReplaceAll(s, "\n", `\x0a`)
}

// kmsgUS is a /dev/kmsg record's time ("prio,seq,usec,flags") in µs, or -1 when it has none.
func kmsgUS(head string) int64 {
	f := strings.Split(head, ",")
	if len(f) < 3 {
		return -1
	}
	us, err := strconv.ParseInt(f[2], 10, 64)
	if err != nil {
		return -1
	}
	return us
}

func startsAtBoot(recs []logRec) bool {
	return len(recs) > 0 && strings.HasPrefix(recs[0].msg, "Linux version")
}
