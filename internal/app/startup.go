package app

import (
	"context"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/omaboot/boot-live/internal/sysroot"
)

// uptime is how long the system has been up (/proc/uptime), or 0 off a real system.
func uptime(root sysroot.Root) time.Duration {
	if !root.IsSystem() {
		return 0
	}
	f := strings.Fields(string(root.Bytes("/proc/uptime")))
	if len(f) == 0 {
		return 0
	}
	s, err := strconv.ParseFloat(f[0], 64)
	if err != nil {
		return 0
	}
	return time.Duration(s * float64(time.Second))
}

// startup is how the Mac's startup went, for the full log: when the tool started, systemd's
// summary, and the slowest units (or the jobs still waiting, when startup hasn't finished). Lines
// are systemd's own English; slow starts on old Macs over USB 2 are what this is for.
func (r *Run) startup() []string {
	if !r.Opt.Root.IsSystem() {
		return nil
	}
	var out []string
	if r.upAtStart > 0 {
		out = append(out, "OmaBoot? Live started "+r.upAtStart.Round(time.Second).String()+" after the kernel")
	}
	run := func(args ...string) []string {
		ctx, cancel := context.WithTimeout(r.ctx, 5*time.Second)
		defer cancel()
		b, _ := exec.CommandContext(ctx, args[0], args[1:]...).CombinedOutput()
		var ls []string
		for _, l := range strings.Split(string(b), "\n") {
			if l = strings.TrimSpace(l); l != "" {
				ls = append(ls, l)
			}
		}
		return ls
	}
	sum := run("systemd-analyze", "--no-pager")
	if len(sum) > 0 && strings.HasPrefix(sum[0], "Startup finished") {
		out = append(out, sum[0])
		blame := run("systemd-analyze", "blame", "--no-pager")
		if len(blame) > 8 {
			blame = blame[:8]
		}
		for _, l := range blame {
			out = append(out, "slow: "+strings.Join(strings.Fields(l), " "))
		}
		return out
	}
	out = append(out, sum...)
	jobs := run("systemctl", "list-jobs", "--no-legend", "--no-pager")
	if len(jobs) > 8 {
		jobs = jobs[:8]
	}
	for _, l := range jobs {
		out = append(out, "waiting: "+strings.Join(strings.Fields(l), " "))
	}
	return out
}
