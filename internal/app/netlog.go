package app

import (
	"fmt"
	"strings"
	"time"
)

// The network log is what the network step and the Wi-Fi test asked of iwd and how it went, and
// each boot.watch test with the address and gateway of the interface it ran over, for
// bugreport's network-log.txt (the Air's 0.8.1 run: a join failed and nothing on the stick said
// why; the 0.8.5 runs: boot.watch didn't answer over three Macs' cables, on a VLAN whose
// firewall stopped its port, and nothing said which network they were on). It never holds a
// network's name or a password: the scans' counts, the security picked, the outcomes and
// iwd's own errors. (wifi-log.txt before 0.8.7.)

// maxNetLog is how many lines bugreport keeps (the newest).
const maxNetLog = 200

// netLog adds a line to the network log, stamped with the time.
func (b *Backend) netLog(f string, a ...any) {
	b.netLogMu.Lock()
	defer b.netLogMu.Unlock()
	b.netLines = append(b.netLines, time.Now().UTC().Format("15:04:05Z")+"  "+fmt.Sprintf(f, a...))
	if len(b.netLines) > maxNetLog {
		b.netLines = b.netLines[len(b.netLines)-maxNetLog:]
	}
}

// netText is the network log for bugreport, "" when nothing was logged.
func (b *Backend) netText() string {
	b.netLogMu.Lock()
	defer b.netLogMu.Unlock()
	if len(b.netLines) == 0 {
		return ""
	}
	return strings.Join(b.netLines, "\n") + "\n"
}
