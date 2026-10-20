package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/omaboot/boot-live/internal/checks"
	"github.com/omaboot/boot-live/internal/handson"
	"github.com/omaboot/boot-live/internal/identify"
	"github.com/omaboot/boot-live/internal/sysroot"
	"github.com/omaboot/boot-live/internal/ui"
)

// Picking a run up again (owner, 0.7.3 review): only the newest unsent copy from this Mac, by this
// version, whose report is still there is offered; a note from another Mac, one used or set
// aside, one cut short, or one whose copy is gone never is; one from another version is named,
// never offered (0.7.5). Once answered, it isn't offered again, and picking up restores the
// quick pass's results; the final save drops its note.
func TestResumeOffer(t *testing.T) {
	dir := t.TempDir()
	r := New(Options{Root: sysroot.Root("../../testdata/macbookair5,2"), Edition: ui.Live, StickDir: dir, Version: "0.7.4 dev"})
	defer r.Close()
	r.HW = identify.Hardware{ProductName: "iMac10,1", BoardID: "Mac-F2268CC8", CPU: "Intel(R) Core(TM)2 Duo CPU E7600 @ 3.06GHz", MemMiB: 4096,
		PCI: []identify.PCIDev{{Vendor: "1002", Device: "9488"}}}
	other := r.HW
	other.MemMiB = 8192
	at := time.Date(2026, 10, 15, 18, 45, 0, 0, time.UTC)
	version := "0.7.4 dev"
	note := func(name string, hw identify.Hardware, state string, saved time.Time, copy bool) {
		n := resumeNote{Copy: "results/" + name, Test: "power.sleep-wake", Title: "Sleep and wake", Fingerprint: fingerprint(hw),
			Version: version, Saved: saved, State: state, Of: 17,
			Tests:      []resumeTest{{ID: "storage.optical", Results: []checks.Result{{ID: "storage.optical", Status: checks.Supported, Method: checks.Automatic}}}},
			Quick:      []checks.Result{{ID: "graphics.discrete", Status: checks.Partial, Method: checks.Automatic}},
			QuickStart: saved.Add(-time.Hour), QuickEnd: saved.Add(-time.Hour + 2*time.Minute)}
		b, _ := json.Marshal(n)
		write(t, filepath.Join(dir, "resume", name), b)
		if copy {
			write(t, filepath.Join(dir, "results", name), []byte("{}"))
		}
	}
	note("a.json", r.HW, noteUnsent, at, true)
	note("b.json", r.HW, noteUnsent, at.Add(time.Hour), true)    // newest: offered
	note("c.json", other, noteUnsent, at.Add(2*time.Hour), true) // another Mac
	note("d.json", r.HW, noteUsed, at.Add(3*time.Hour), true)
	note("e.json", r.HW, noteSetAside, at.Add(3*time.Hour), true)
	note("f.json", r.HW, noteUnsent, at.Add(4*time.Hour), false) // its copy is gone
	write(t, filepath.Join(dir, "resume", "g.json"), []byte(`{"copy": "results/g.json", "state": "unse`))
	version = "0.7.3 dev"
	note("h.json", r.HW, noteUnsent, at.Add(5*time.Hour), true) // the newest, but another version's: never offered

	r.findResume()
	u := r.resumeFacts()
	if u == nil || !u.Saved.Equal(at.Add(time.Hour)) || u.Test != "power.sleep-wake" || u.Of != 17 || u.Done["storage.optical"].Results[0].Status != checks.Supported ||
		len(u.Quick) != 1 || u.QuickIn != "2 min 0 s" {
		t.Fatalf("offered %+v, want b.json's, its quick pass with it", u)
	}
	if o := r.otherFacts(); o != nil {
		t.Errorf("named another version's copy %+v while offering one", o)
	}
	restored, err := r.Resume(true)
	if err != nil {
		t.Fatal(err)
	}
	if r.keep != "results/b.json" || r.resumed == nil || !restored || len(r.results) != 1 || r.results[0].ID != "graphics.discrete" || !r.quickRestored {
		t.Errorf("picked up: keep %q, resumed %v, quick pass restored %v %+v", r.keep, r.resumed, restored, r.results)
	}
	r.findResume()
	if u := r.resumeFacts(); u != nil {
		t.Errorf("after picking b up, offered %+v, want nothing (a.json is older: set aside with it)", u)
	}
	r.dropNote() // the final save
	if _, err := os.Stat(filepath.Join(dir, "resume", "b.json")); !os.IsNotExist(err) {
		t.Errorf("b.json's note is still there after the final save: %v", err)
	}
	if r.resumedNote() == "" {
		t.Error("the report doesn't say the run was picked up")
	}
}

// A copy from another boot-live version is never picked up (owner, 2026-10-15: a report names one
// version): Which Mac names it, and once answered its note is set aside; the copy stays.
func TestResumeOtherVersion(t *testing.T) {
	dir := t.TempDir()
	r := New(Options{Root: sysroot.Root("../../testdata/macbookair5,2"), Edition: ui.Live, StickDir: dir, Version: "0.7.5 dev"})
	defer r.Close()
	r.HW = identify.Hardware{ProductName: "iMac10,1", BoardID: "Mac-F2268CC8"}
	saved := time.Date(2026, 10, 15, 18, 45, 0, 0, time.UTC)
	b, _ := json.Marshal(resumeNote{Copy: "results/a.json", Test: "power.sleep-wake", Title: "Sleep and wake", Fingerprint: fingerprint(r.HW),
		Version: "0.7.4 dev", Saved: saved, State: noteUnsent, Of: 17})
	write(t, filepath.Join(dir, "resume", "a.json"), b)
	write(t, filepath.Join(dir, "results", "a.json"), []byte("{}"))
	r.findResume()
	if u := r.resumeFacts(); u != nil {
		t.Fatalf("offered another version's copy: %+v", u)
	}
	if o := r.otherFacts(); o == nil || o.Version != "0.7.4 dev" || !o.Saved.Equal(saved) {
		t.Fatalf("named %+v, want 0.7.4 dev's copy", o)
	}
	if restored, err := r.Resume(false); err != nil || restored {
		t.Fatalf("Resume(false): %v, %v", restored, err)
	}
	r.findResume()
	if o := r.otherFacts(); o != nil {
		t.Errorf("named again after Which Mac was answered: %+v", o)
	}
	if _, err := os.Stat(filepath.Join(dir, "results", "a.json")); err != nil {
		t.Errorf("the copy went: %v", err)
	}
}

// The test the Mac didn't come back from goes in as failed; the tests before it come back, and
// the run carries on after it.
func TestResumeRestores(t *testing.T) {
	plan := []handson.Spec{}
	for _, id := range []string{"storage.optical", "power.lid", "power.sleep-wake"} {
		s, _ := handson.Lookup(id)
		plan = append(plan, s)
	}
	h := ui.NewHandsOn(handson.Setup{Env: &handson.Env{}, Plan: plan})
	h.Restore(&ui.Resume{Test: "power.lid", Saved: time.Now(), Version: "0.7.3 dev",
		Done: map[string]handson.Outcome{"storage.optical": {Results: []checks.Result{{ID: "storage.optical", Status: checks.Supported, Method: checks.Automatic}}}}})
	if got := h.Results["power.lid"]; len(got.Results) != 1 || got.Results[0].Status != checks.Failed {
		t.Errorf("power.lid: %+v, want failed", got)
	}
	if h.Resume() != 2 {
		t.Errorf("carries on from test %d, want 2 (sleep and wake)", h.Resume())
	}

	// hung in Sleep and wake (the last test): it opens again, offering light sleep (0.7.5)
	h = ui.NewHandsOn(handson.Setup{Env: &handson.Env{}, Plan: plan})
	h.Restore(&ui.Resume{Test: "power.sleep-wake", Saved: time.Now(), Version: "0.7.4 dev"})
	if _, ok := h.Setup.Env.Hung["power.sleep-wake"]; !ok || h.Resume() != 2 {
		t.Errorf("hung in sleep and wake: Env.Hung %v, opens test %d, want it marked and 2", h.Setup.Env.Hung, h.Resume())
	}
}

func write(t *testing.T, p string, b []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, b, 0o644); err != nil {
		t.Fatal(err)
	}
}
