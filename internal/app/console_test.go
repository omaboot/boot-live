package app

import (
	"errors"
	"testing"

	"github.com/omaboot/boot-live/internal/send"
	"github.com/omaboot/boot-live/internal/sysroot"
	"github.com/omaboot/boot-live/internal/ui"
)

// The console guard (block production only): a run with a result changed in
// the console never reaches DoesItOmarchy.com, whatever the screens offered; staging still takes
// it, and a rerun alone doesn't mark the run.
func TestMarkedRunNeverReachesProduction(t *testing.T) {
	run := func(url string, cmds ...[2]string) error {
		r := New(Options{Root: sysroot.Root("../../testdata/macbookair5,2"), Edition: ui.Live,
			Target: send.Target{URL: url, Key: "doi_test"}})
		defer r.Close()
		b := NewBackend(r)
		for _, c := range cmds {
			b.Console(c[0], c[1])
		}
		_, err := r.Submit(r.Report("", "notice", ""))
		return err
	}
	force := [2]string{"force display.native-resolution pass", "change"}
	rerun := [2]string{"rerun graphics.integrated", "redo"}
	for _, url := range []string{"https://doesitomarchy.com", "https://www.DoesItOmarchy.com/", "https://doesitomarchy.com:443"} {
		if err := run(url, rerun, force); !errors.Is(err, send.ErrChanged) {
			t.Errorf("%s, marked run: %v, want ErrChanged", url, err)
		}
		if err := run(url, rerun); errors.Is(err, send.ErrChanged) {
			t.Errorf("%s, a rerun alone: refused as changed", url)
		}
	}
	if err := run("http://127.0.0.1:9", force); errors.Is(err, send.ErrChanged) {
		t.Error("staging refused a marked run")
	}
}

// The report keeps a change entry even when the console's entries reach the report's limit.
func TestConsoleKeepsChanges(t *testing.T) {
	r := New(Options{Root: sysroot.Root("../../testdata/macbookair5,2"), Edition: ui.Live})
	defer r.Close()
	b := NewBackend(r)
	for i := 0; i < maxConsole; i++ {
		b.Console("rerun boot.install", "redo")
	}
	b.Console("force boot.install pass", "change")
	b.Console("", "")
	f := r.Report("", "notice", "")
	if len(f.Console) != maxConsole || !send.Changed(f) {
		t.Fatalf("%d entries, changed %v", len(f.Console), send.Changed(f))
	}
}
