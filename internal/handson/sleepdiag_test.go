package handson

import (
	"strings"
	"testing"
)

// The kernel's suspend lines (as a MacBookPro11,1 logged them with pm_print_times and
// pm_debug_messages) summed up for the evidence: phases, slowest calls, errors.
func TestPMTiming(t *testing.T) {
	log := []string{
		"[   10.000000] PM: suspend entry (deep)",
		"[   10.010000] Filesystems sync: 0.012 seconds",
		"[   10.020000] Freezing user space processes completed (elapsed 0.001 seconds)",
		"[   10.030000] Freezing remaining freezable tasks completed (elapsed 0.002 seconds)",
		"[   10.100000] snd_hda_intel 0000:00:1b.0: pci_pm_suspend+0x0/0x201 returned 0 after 33955 usecs",
		"[   10.110000] mei_me 0000:00:16.0: pci_pm_suspend+0x0/0x201 returned 0 after 92769 usecs",
		"[   10.120000] sd 0:0:0:0: scsi_bus_suspend+0x0/0x16 returned 0 after 100796 usecs",
		"[   10.130000] PM: suspend of devices complete after 124.844 msecs",
		"[   10.131000] PM: start suspend of devices complete after 125.139 msecs",
		"[   10.140000] PM: late suspend of devices complete after 12.695 msecs",
		"[   10.150000] xhci_hcd 0000:00:14.0: pci_pm_suspend_noirq+0x0/0x2dc returned 0 after 117479 usecs",
		"[   10.160000] PM: noirq suspend of devices complete after 121.621 msecs",
		"[   12.000000] PM: noirq resume of devices complete after 40.100 msecs",
		"[   12.100000] radeon 0000:01:00.0: pci_pm_resume+0x0/0xf0 returned 0 after 1201345 usecs",
		"[   12.200000] firewire_ohci 0000:04:00.0: pci_pm_resume+0x0/0xf0 [firewire_ohci] returned -5 after 900 usecs",
		"[   12.300000] PM: resume of devices complete after 1530.200 msecs",
		"[   12.400000] PM: suspend exit",
	}
	if got := NapLog(append([]string{"[ 1.0] PM: suspend entry (deep)", "[ 2.0] PM: suspend exit"}, log...)); len(got) != len(log) {
		t.Fatalf("NapLog kept %d lines, want the last nap's %d", len(got), len(log))
	}
	got := PMTiming(log)
	for _, want := range []string{
		"sync 0.01 s, freezing tasks 0.00 s",
		"suspend: devices 125 ms, late 13 ms, noirq 122 ms",
		"resume: noirq 40 ms, devices 1.5 s",
		"slowest: radeon 0000:01:00.0 pci_pm_resume 1.2 s, xhci_hcd 0000:00:14.0 pci_pm_suspend_noirq 117 ms, sd 0:0:0:0 scsi_bus_suspend 101 ms",
		"errors: firewire_ohci 0000:04:00.0 pci_pm_resume returned -5",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("PMTiming lacks %q:\n%s", want, got)
		}
	}
	if PMTiming([]string{"[ 1.0] PM: suspend entry (deep)", "[ 2.0] PM: suspend exit"}) != "" {
		t.Error("PMTiming without the diagnostics' lines should say nothing")
	}
}
