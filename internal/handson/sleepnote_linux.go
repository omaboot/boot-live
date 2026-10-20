//go:build linux

package handson

import (
	"os"
	"strings"
	"time"

	"golang.org/x/sys/unix"
)

// screenNote writes a heading on the console the kernel switches to while the Mac sleeps
// (SUSPEND_CONSOLE, the last VT): opening it makes it, so when the kernel moves there its
// suspend steps print under the heading. A Mac that hangs then shows what it was doing, rather
// than an empty screen with a blinking cursor (the iMac10,1, 0.7.4). Where the kernel doesn't
// switch consoles, its steps print over boot-live's screen instead, which repaints after.
func screenNote(how SleepHow, mode string, wake time.Duration) {
	f, err := os.OpenFile(suspendVT, os.O_WRONLY|unix.O_NOCTTY|unix.O_NONBLOCK, 0)
	if err != nil {
		return
	}
	defer f.Close()
	f.WriteString("\x1b[0m\x1b[2J\x1b[H" + strings.Join(noteLines(how, mode, wake, time.Now()), "\r\n") + "\r\n\r\n")
}
