package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/omaboot/boot-live/internal/boot"
	"github.com/omaboot/boot-live/internal/checks"
	"github.com/omaboot/boot-live/internal/handson"
	"github.com/omaboot/boot-live/internal/identify"
	"github.com/omaboot/boot-live/internal/send"
	"github.com/omaboot/boot-live/internal/ui"
)

// Picking up where a run left off (owner, 0.7.3 review): before a test that sleeps, the copy of
// the report goes on external storage with a note beside it, resume/<the copy's name>: the test
// about to run, the quick pass's results, the hands-on tests decided so far, this Mac's hardware
// fingerprint and the boot-live that wrote it. A Mac that never comes back is forced off; on the
// next start, Which Mac offers the newest note that is unsent and from this Mac (ui.Resume); the
// answer covers this Mac's older unsent notes too. The note's state then says what became of it:
// "unsent" (offered), "used" (picked up), "set-aside" (started fresh, or another version's); it is
// removed once the run's final report is saved or sent, and a Keep writes a new one. The copy
// itself is a plain report, as before.
//
// Picking up (0.7.5; owner, 2026-10-15) restores the quick pass's results from the note and goes
// on to the hands-on tests without running the pass again: the test it hung in failed, then the
// next. A note without the quick pass's results (cut down, or older) runs the pass again, Which
// Mac saying why. A note from another boot-live version is never offered: a report names one tool
// version, so results from two versions don't mix. Which Mac says one was found, that this
// version tests everything again, and that the copy stays on external storage (the site can still
// import it); the note is set aside once Which Mac is answered. A note that doesn't read (cut
// short, another format) is passed over.

const (
	noteUnsent   = "unsent"
	noteUsed     = "used"
	noteSetAside = "set-aside"
)

type resumeNote struct {
	Copy        string       `json:"copy"` // the report copy beside it: results/…json
	Test        string       `json:"test"` // the hands-on test about to run
	Title       string       `json:"title"`
	Fingerprint string       `json:"fingerprint"`
	Identifier  string       `json:"identifier"`
	Version     string       `json:"version"`
	Saved       time.Time    `json:"saved"`
	State       string       `json:"state"`
	Of          int          `json:"of"`    // hands-on tests in the plan
	Tests       []resumeTest `json:"tests"` // the hands-on tests decided before it
	// the quick pass's results and when it ran (0.7.5); none in older notes
	Quick      []checks.Result `json:"quick,omitempty"`
	QuickStart time.Time       `json:"quick_start,omitzero"`
	QuickEnd   time.Time       `json:"quick_end,omitzero"`
}

type resumeTest struct {
	ID      string          `json:"id"`
	Say     string          `json:"say,omitempty"`
	Results []checks.Result `json:"results"`
}

// noteName is the note beside a copy: results/x.json → resume/x.json.
func noteName(copy string) string { return "resume/" + path.Base(copy) }

// fingerprint is this Mac's hardware, as far as the run reads it, without anything personal: the
// model, Apple's board ID, the CPU, the memory and every PCI function's IDs. Two Macs of the
// same model with the same parts share it: the offer says when the copy was saved, and "Start
// fresh" is always there.
func fingerprint(hw identify.Hardware) string {
	var pci []string
	for _, d := range hw.PCI {
		pci = append(pci, d.ID()+" "+d.Subsys())
	}
	sort.Strings(pci)
	s := strings.Join(append([]string{hw.ProductName, hw.BoardID, hw.CPU, fmt.Sprint(hw.MemMiB)}, pci...), "\n")
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:8])
}

// findResume reads the notes on external storage and keeps the newest that is unsent, from this
// Mac, by this version, with its copy still there; the newest such note from another version is
// kept to be named (never offered). Nothing is mounted rw; no drive, no offer.
func (r *Run) findResume() {
	if r.Fixture() && r.Opt.StickDir == "" {
		return
	}
	fp := fingerprint(r.HW)
	var best, other *resumeNote
	var all []resumeNote
	ctx, cancel := context.WithTimeout(r.ctx, 20*time.Second)
	defer cancel()
	send.WithStick(ctx, r.Opt.Root, r.Opt.StickDir, false, func(base string) error {
		names, _ := filepath.Glob(filepath.Join(base, "resume", "*.json"))
		for _, n := range names {
			b, err := os.ReadFile(n)
			if err != nil {
				continue
			}
			var nt resumeNote
			if json.Unmarshal(b, &nt) != nil || nt.State != noteUnsent || nt.Fingerprint != fp || nt.Test == "" || nt.Saved.IsZero() {
				continue
			}
			if _, err := os.Stat(filepath.Join(base, filepath.Clean("/"+nt.Copy))); err != nil {
				continue
			}
			all = append(all, nt)
			if nt.Version != r.Opt.Version {
				if other == nil || nt.Saved.After(other.Saved) {
					other = &nt
				}
				continue
			}
			if best == nil || nt.Saved.After(best.Saved) {
				best = &nt
			}
		}
		return nil
	})
	var older []resumeNote
	for _, nt := range all { // set aside with the answer: every unsent note of this Mac's but the one offered
		if best == nil || nt.Copy != best.Copy {
			older = append(older, nt)
		}
	}
	r.mu.Lock()
	r.offer, r.older, r.other = best, older, other
	r.mu.Unlock()
}

// otherFacts is another version's copy as Which Mac names it, or nil (only when nothing is
// offered).
func (r *Run) otherFacts() *ui.OtherCopy {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.offer != nil || r.other == nil {
		return nil
	}
	return &ui.OtherCopy{Version: r.other.Version, Saved: r.other.Saved, Copy: r.other.Copy}
}

// resumeFacts is the offer as Which Mac shows it, or nil.
func (r *Run) resumeFacts() *ui.Resume {
	r.mu.Lock()
	nt := r.offer
	r.mu.Unlock()
	if nt == nil {
		return nil
	}
	u := &ui.Resume{Test: nt.Test, Title: nt.Title, Saved: nt.Saved, Version: nt.Version, Of: nt.Of, Done: map[string]handson.Outcome{}, Quick: nt.Quick}
	if !nt.QuickStart.IsZero() && nt.QuickEnd.After(nt.QuickStart) {
		u.QuickIn = ui.Duration(nt.QuickEnd.Sub(nt.QuickStart))
	}
	if s, ok := handson.Lookup(nt.Test); ok {
		u.Title = s.Title
	}
	for _, t := range nt.Tests {
		u.Done[t.ID] = handson.Outcome{Results: t.Results, Say: t.Say}
	}
	return u
}

// Resume answers the offer: picked up (the copy becomes this run's own, overwritten by its next
// copy and replaced by its final save; the quick pass's results come back from the note) or set
// aside; the note says so, so it isn't offered again. Called with nothing offered, it sets aside
// this Mac's unsent notes from another version (Which Mac named the newest). It reports whether
// the quick pass's results came back.
func (r *Run) Resume(use bool) (restored bool, err error) {
	r.mu.Lock()
	nt, older := r.offer, r.older
	r.offer, r.older, r.other = nil, nil, nil
	if nt != nil && use {
		r.resumed, r.keep = nt, nt.Copy
		if len(nt.Quick) > 0 {
			r.results = append([]checks.Result(nil), nt.Quick...)
			r.passStart, r.passEnd, r.quickRestored = nt.QuickStart, nt.QuickEnd, true
			restored = true
		}
	}
	r.mu.Unlock()
	for _, o := range older { // this Mac's other unsent notes: the answer covers them too
		o.State = noteSetAside
		r.writeNote(&o)
	}
	if nt == nil {
		return false, nil
	}
	cp := *nt
	cp.State = noteSetAside
	if use {
		cp.State = noteUsed
	}
	return restored, r.writeNote(&cp)
}

func (r *Run) writeNote(nt *resumeNote) error {
	b, err := json.MarshalIndent(nt, "", "  ")
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(r.ctx, 60*time.Second)
	defer cancel()
	_, err = send.SaveBytes(ctx, r.Opt.Root, r.Opt.StickDir, append(b, '\n'), noteName(nt.Copy), "")
	return err
}

// keepNote writes the note beside the copy just kept.
func (r *Run) keepNote(copy string, k ui.KeepInfo) error {
	nt := &resumeNote{Copy: copy, Test: k.Test, Title: k.Title, Fingerprint: fingerprint(r.HW), Identifier: r.Ident.Identifier,
		Version: r.Opt.Version, Saved: time.Now().UTC(), State: noteUnsent, Of: k.Of}
	r.mu.Lock()
	for _, x := range r.results { // the quick pass's, for picking up without running it again
		x.Effect, x.Params = "", boot.Params{}
		nt.Quick = append(nt.Quick, x)
	}
	nt.QuickStart, nt.QuickEnd = r.passStart.UTC(), r.passEnd.UTC()
	r.mu.Unlock()
	ids := make([]string, 0, len(k.Done))
	for id := range k.Done {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		o := k.Done[id]
		t := resumeTest{ID: id, Say: o.Say}
		for _, x := range o.Results {
			x.Effect, x.Params = "", boot.Params{} // the band's effect doesn't come back
			t.Results = append(t.Results, x)
		}
		nt.Tests = append(nt.Tests, t)
	}
	return r.writeNote(nt)
}

// noteTicket orders the note's writes and removals as they were asked for (each runs on its own
// goroutine): noteOp skips an op once a later one has run, so a removal for one test that sleeps
// can't undo the note written before the next.
func (r *Run) noteTicket() uint64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.noteReq++
	return r.noteReq
}

func (r *Run) noteOp(t uint64, op func()) {
	r.noteMu.Lock()
	defer r.noteMu.Unlock()
	if t < r.noteDone {
		return
	}
	r.noteDone = t
	op()
}

// Woke drops the note: a test that sleeps is over and the Mac came back from it.
func (r *Run) Woke(t uint64) { r.noteOp(t, r.dropNote) }

// dropNote removes the note beside the run's copy: its final report was saved or sent.
func (r *Run) dropNote() {
	r.mu.Lock()
	keep := r.keep
	r.mu.Unlock()
	if keep == "" {
		return
	}
	ctx, cancel := context.WithTimeout(r.ctx, 30*time.Second)
	defer cancel()
	send.Remove(ctx, r.Opt.Root, r.Opt.StickDir, noteName(keep)) // a note left behind is only offered again
}

// resumedNote is the report's note on a run picked up from a copy, or "".
func (r *Run) resumedNote() string {
	r.mu.Lock()
	nt := r.resumed
	r.mu.Unlock()
	if nt == nil {
		return ""
	}
	again := "the quick pass ran again on this start"
	if len(nt.Quick) > 0 {
		again = "the quick pass's results are the copy's (it didn't run again)"
	}
	return fmt.Sprintf("Picked up from the copy saved before the %s test at %s UTC (boot-live %s): the Mac didn't come back from that test, which is failed; %s.",
		nt.Title, nt.Saved.UTC().Format("2006-01-02 15:04"), nt.Version, again)
}
