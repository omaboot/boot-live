package app

import (
	"errors"
	"io/fs"
	"os"
	"strings"
	"syscall"
	"time"

	"github.com/omaboot/boot-live/internal/send"
	"github.com/omaboot/boot-live/internal/ui"
)

// watchStick watches the stick the system runs from. Without copytoram the live system reads
// its files from the stick as it needs them: once the stick is pulled out, a program, driver or
// firmware not yet in memory can't load, so a check or test would fail for the wrong reason,
// and plugging the stick back in doesn't help (it comes back as a new device). So the run stops
// recording there and the screens say so (ui.StickGoneMsg).
func (b *Backend) watchStick() {
	r := b.R
	if r.Fixture() || strings.Contains(" "+r.HW.Cmdline+" ", " copytoram=y ") {
		return
	}
	disk, err := send.BootDisk(r.Opt.Root)
	if err != nil {
		return
	}
	// boot-live itself (about 18 MB, the word clips included) stays in memory, so it lives on to
	// say what happened rather than dying the next time the kernel drops a page of it; what it
	// allocates later is plain memory, nothing the stick holds
	syscall.Mlockall(syscall.MCL_CURRENT)
	p := "/sys/block/" + disk // kernel memory: reading it needs nothing from the stick
	t := time.NewTicker(500 * time.Millisecond)
	defer t.Stop()
	for {
		select {
		case <-r.ctx.Done():
			return
		case <-t.C:
		}
		if _, err := os.Stat(p); errors.Is(err, fs.ErrNotExist) {
			r.StickGone()
			b.post(ui.StickGoneMsg{})
			return
		}
	}
}
