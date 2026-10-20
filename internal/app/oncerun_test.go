package app

import (
	"errors"
	"testing"
	"time"

	"github.com/omaboot/boot-live/internal/send"
	"github.com/omaboot/boot-live/internal/sysroot"
	"github.com/omaboot/boot-live/internal/ui"
)

// One run sends one report: once a code came back, Submit refuses before
// building a request, whatever the screens do.
func TestOneReportPerRun(t *testing.T) {
	r := New(Options{Root: sysroot.Root("../../testdata/macbookair5,2"), Edition: ui.Live,
		Target: send.Target{URL: "http://127.0.0.1:9", Key: "doi_test"}})
	defer r.Close()
	r.code = "428e03024e"
	if _, err := r.Submit(nil); !errors.Is(err, ErrAlreadySent) {
		t.Fatalf("second Submit: %v, want ErrAlreadySent", err)
	}
}

func TestStartedIn(t *testing.T) {
	for want, lines := range map[string][]string{
		"41 s":      {"OmaBoot? Live started 41s after the kernel", "Startup finished in 1.2s (firmware) + 2.1s (loader) + 3.4s (kernel) + 34.6s (userspace) = 41.3s"},
		"1 min 5 s": {"Startup finished in 3s (kernel) + 1min 2.1s (userspace) = 1min 5.1s"},
		"":          {"OmaBoot? Live started 41s after the kernel", "waiting: 12 foo.service start running"},
	} {
		if got := startedIn(lines); got != want {
			t.Errorf("startedIn(%q) = %q, want %q", lines, got, want)
		}
	}
	// the parts, for the boot timing (0.7.5)
	s := parseStartup([]string{"Startup finished in 12.003s (kernel) + 41.226s (initrd) + 1min 2.5s (userspace) = 1min 55.729s"})
	bt := bootTimes{sd: s, started: 58 * time.Second}
	if want := "booted in 1:56 (systemd): kernel 12.0 s, initrd 41.2 s, userspace 62.5 s; boot-live started at 0:58 after the kernel started"; bt.evidence() != want {
		t.Errorf("boot timing:\n%s\nwant\n%s", bt.evidence(), want)
	}
}
