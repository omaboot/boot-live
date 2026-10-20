package app

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/doesitomarchy/doesitomarchy/pkg/report"

	"github.com/omaboot/boot-live/assets"
	"github.com/omaboot/boot-live/internal/checks"
	"github.com/omaboot/boot-live/internal/command"
	"github.com/omaboot/boot-live/internal/handson"
	"github.com/omaboot/boot-live/internal/i18n"
	"github.com/omaboot/boot-live/internal/identify"
	"github.com/omaboot/boot-live/internal/send"
	"github.com/omaboot/boot-live/internal/ui"
)

// What the console's commands ask of the real run (LIVE-PLAN §24).

// The report's limits on its console entries (pkg/report's check).
const (
	maxConsole = 64
	maxCommand = 200
)

// IDs are this Mac's hardware IDs, as ids prints them: never a serial number (the tool reads
// none).
func (b *Backend) IDs() []command.ID {
	r := b.R
	h := r.HW
	ids := []command.ID{{Key: "model", ID: h.ProductName, Name: r.Ident.Name}}
	r.mu.Lock()
	config := r.config
	r.mu.Unlock()
	if config != "" {
		ids = append(ids, command.ID{Key: "config", ID: config})
	}
	ids = append(ids, command.ID{Key: "board", ID: h.BoardID}, command.ID{Key: "cpu", ID: identify.ShortCPU(h.CPU), Name: h.CPU})
	pci := func(key string, ds []identify.PCIDev, names map[string]string) {
		for _, d := range ds {
			name := strings.TrimSpace(names[d.ID()] + " " + d.Driver)
			ids = append(ids, command.ID{Key: key, ID: d.ID(), Name: name})
		}
	}
	pci("gpu", h.GPUs(), nil)
	pci("wifi", h.PCIByClass("0280"), checks.WifiChips)
	pci("ethernet", h.PCIByClass("0200"), nil)
	pci("audio", h.PCIByClass("0403"), nil)
	if h.Firmware != "" {
		ids = append(ids, command.ID{Key: "firmware", ID: h.Firmware})
	}
	return ids
}

// KernelLog is the kernel log as it is now, with times.
func (b *Backend) KernelLog() []string { return checks.ReadKmsgTimed(b.R.Opt.Root) }

// ReportJSON is the report as it stands, as Save would write it.
func (b *Backend) ReportJSON(handle, notice string) ([]byte, error) {
	return json.MarshalIndent(b.R.Report(handle, notice, ""), "", "  ")
}

// Note adds the tester's note to the report's notes.
func (b *Backend) Note(text string) {
	r := b.R
	r.mu.Lock()
	r.notes = append(r.notes, strings.TrimSpace(text))
	r.mu.Unlock()
}

// Console records a redo or change command for the report, in order, within the report's
// limits (64 entries, 200 characters each).
func (b *Backend) Console(cmd, kind string) {
	r := b.R
	if kind == "" {
		return
	}
	if utf8.RuneCountInString(cmd) > maxCommand {
		cmd = string([]rune(cmd)[:maxCommand])
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.console) >= maxConsole {
		if kind != report.ConsoleChange {
			return
		}
		// a change must never be dropped (it keeps the report from DoesItOmarchy.com): it takes
		// the last redo's place
		for i := len(r.console) - 1; i >= 0; i-- {
			if r.console[i].Kind != report.ConsoleChange {
				r.console = append(r.console[:i], r.console[i+1:]...)
				break
			}
		}
		if len(r.console) >= maxConsole {
			return
		}
	}
	r.console = append(r.console, report.ConsoleEntry{Command: cmd, Kind: kind})
}

// Target is where Submit sends.
func (b *Backend) Target() command.Target {
	t := b.R.Opt.Target
	return command.Target{URL: t.URL, Shown: b.R.Site(), Production: t.Production(), CanSubmit: t.CanSubmit()}
}

// Rerun runs the quick-pass check that decides id again, without the GPU notice (whoever types
// it knows), and answers with a RerunMsg, then the summary.
func (b *Backend) Rerun(id string) (string, error) {
	r := b.R
	e := r.env()
	var c checks.Check
	found := false
	for _, x := range checks.Plan(e) {
		for _, item := range x.ItemIDs(e) {
			if item == id || x.ID == id {
				c, found = x, true
			}
		}
	}
	if !found {
		return "", fmt.Errorf("%s", i18n.T("%s isn't a quick-pass check on this Mac", id))
	}
	r.mu.Lock()
	if r.rerunning {
		r.mu.Unlock()
		return "", errors.New(i18n.T("a check is already running again"))
	}
	r.rerunning = true
	r.mu.Unlock()
	go func() {
		e := r.env()
		e.Started, e.Live, e.Gate = nil, nil, nil
		var got []checks.Result
		checks.Run(r.ctx, e, []checks.Check{c}, func(x checks.Result) { got = append(got, x) })
		got = r.Rerun(got)
		var cs []ui.Check
		for _, x := range got {
			cs = append(cs, toCheck(x))
		}
		b.post(ui.RerunMsg{ID: id, Checks: cs})
		b.post(r.Summary())
	}()
	expect := c.Expect
	if expect < time.Second {
		expect = time.Second
	}
	return i18n.T("about %s", ui.Duration(expect)), nil
}

// Rerun takes a check's new results: each replaces the one it measured again, carrying the
// first run in its evidence (owner, 2026-10-14); a result set by hand goes back to what was
// measured. The quick pass's timing stays the first run's.
func (r *Run) Rerun(got []checks.Result) []checks.Result {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.rerunning = false
	if !r.gone.IsZero() {
		return nil // the stick came out: nothing more is recorded
	}
	if r.first == nil {
		r.first = map[string]checks.Result{}
	}
	out := make([]checks.Result, 0, len(got))
	for _, x := range got {
		for i, old := range r.results {
			if old.ID != x.ID {
				continue
			}
			first, ok := r.first[x.ID]
			if !ok {
				first, r.first[x.ID] = old, old
			}
			x.Evidence = checks.AgainEvidence(x.Evidence, checks.Again(first, first.Finish-first.Begin))
			x.Begin, x.Finish = old.Begin, old.Finish
			r.results[i] = x
		}
		delete(r.forced, x.ID)
		out = append(out, x)
	}
	return out
}

// Force sets a result by hand (dev: the console's force): pass, partial, fail or skip.
func (b *Backend) Force(id, status string) error {
	r := b.R
	s := map[string]string{command.Pass: checks.Supported, command.Partial: checks.Partial, command.Fail: checks.Failed, command.Skip: checks.NotTested}[status]
	if s == "" {
		return errors.New("pass, partial, fail or skip")
	}
	e := r.env()
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.forced, id)
	for _, x := range r.merged(e) {
		if x.ID == id {
			if r.forced == nil {
				r.forced = map[string]string{}
			}
			r.forced[id] = s
			go func() { b.post(r.Summary()) }() // Quick results and Finish count it
			return nil
		}
	}
	return fmt.Errorf("%s", i18n.T("no result for %s in this run", id))
}

// forcedResult is a result set by hand: the status given, what was measured kept in its evidence.
func forcedResult(x checks.Result, status string) checks.Result {
	was := x.Status
	if x.Evidence != "" {
		was += ": " + x.Evidence
	}
	x.Status, x.Reason = status, ""
	switch {
	case status == checks.NotTested:
		x.Method, x.Reason = "", checks.Other
	case x.Method == "":
		x.Method = checks.Automatic
	}
	x.Evidence = checks.AgainEvidence("set by hand in the console (force)", "measured: "+was)
	return x
}

// Bugreport saves the full log, the kernel log, the report, the screen under the console and
// the Wi-Fi log to the stick's results/ as one tar file, and answers with a BugMsg. Only to the stick: the kernel
// log may name serial numbers (owner, 2026-10-14).
func (b *Backend) Bugreport(f ui.BugFiles) {
	r := b.R
	go func() {
		rep, err := json.MarshalIndent(r.Report(f.Handle, f.Notice, ""), "", "  ")
		for i := range f.Log { // a run plan's Wi-Fi network isn't named in a bug report
			f.Log[i] = r.planMask(f.Log[i])
		}
		f.Screen = r.planMask(f.Screen)
		if err != nil {
			b.post(ui.BugMsg{Err: errText(err)})
			return
		}
		now := time.Now()
		files := []struct {
			name string
			body []byte
		}{
			{"full-log.txt", []byte(strings.Join(f.Log, "\n") + "\n")},
			{"kernel-log.txt", []byte(strings.Join(checks.ReadKmsgTimed(r.Opt.Root), "\n") + "\n")},
			{"report.json", append(rep, '\n')},
			{"screen.txt", []byte(f.Screen)},
			{"boot.txt", []byte(r.bootText())},
		}
		if wl := b.netText(); wl != "" { // what Wi-Fi was asked, the boot.watch tests: no names
			files = append(files, struct {
				name string
				body []byte
			}{"network-log.txt", []byte(wl)})
		}
		if naps := b.napsText(); naps != "" { // every nap's kernel lines, the diagnostics on
			files = append(files, struct {
				name string
				body []byte
			}{"sleep-log.txt", []byte(naps)})
		}
		var buf bytes.Buffer
		tw := tar.NewWriter(&buf)
		dir := strings.TrimSuffix(strings.TrimPrefix(BugName(r.Ident.Identifier, now), "results/"), ".tar")
		for _, x := range files {
			tw.WriteHeader(&tar.Header{Name: dir + "/" + x.name, Mode: 0o644, Size: int64(len(x.body)), ModTime: now, Typeflag: tar.TypeReg})
			tw.Write(x.body)
		}
		if err := tw.Close(); err != nil {
			b.post(ui.BugMsg{Err: errText(err)})
			return
		}
		ctx, cancel := context.WithTimeout(r.ctx, 60*time.Second)
		defer cancel()
		p, err := send.SaveBytes(ctx, r.Opt.Root, r.Opt.StickDir, buf.Bytes(), BugName(r.Ident.Identifier, now), "")
		if err != nil {
			b.post(ui.BugMsg{Err: errText(err)})
			return
		}
		r.planBug(p)
		r.mu.Lock()
		if r.bugMem == nil {
			r.bugMem = map[string][]byte{}
		}
		r.bugMem[p] = buf.Bytes()
		r.mu.Unlock()
		b.post(ui.BugMsg{Path: p, Bytes: buf.Len()})
	}()
}

// TestDrives are where testfile could write the test file, and where not (send.TestDrives).
func (b *Backend) TestDrives() []command.TestDrive {
	var out []command.TestDrive
	for _, d := range send.TestDrives(b.R.Opt.Root) {
		out = append(out, command.TestDrive{Dev: d.Dev, What: d.What, OK: d.OK, Why: d.Why})
	}
	return out
}

// WriteTestFile writes OmaBoot?'s test file to a drive the person named and confirmed, then
// reads it back from the disk raw, as the port tests will (TestFileMsg).
func (b *Backend) WriteTestFile(dev string) {
	r := b.R
	go func() {
		start := time.Now()
		ctx, cancel := context.WithTimeout(r.ctx, 3*time.Minute)
		defer cancel()
		err := send.WriteTestFile(ctx, r.Opt.Root, dev, command.TestFileName, handson.TestFile())
		if errors.Is(err, send.ErrAlready) {
			b.post(ui.TestFileMsg{Dev: dev, Already: true})
			return
		}
		if err != nil {
			b.post(ui.TestFileMsg{Dev: dev, Err: errText(err)})
			return
		}
		where, err := handson.CheckTestFile(send.WholeDisk(r.Opt.Root, "/dev/"+dev))
		if err != nil {
			b.post(ui.TestFileMsg{Dev: dev, Err: i18n.T("written, but reading it back failed: %s", err.Error())})
			return
		}
		b.post(ui.TestFileMsg{Dev: dev, Where: dev + ": " + where, Took: time.Since(start)})
	}()
}

// BugName is bugreport's file: results/bugreport-<identifier>-<UTC time>.tar.
func BugName(identifier string, at time.Time) string {
	base := strings.TrimSuffix(strings.TrimPrefix(send.FileName(identifier, "", at), "results/"), ".json")
	return "results/bugreport-" + base + ".tar"
}

// Catalog is the catalog snapshot the run matches against, for the console's catalog command:
// the image's own, or the one refreshed from the site in this run.
func (b *Backend) Catalog() command.Catalog {
	r := b.R
	if r.Cat == nil {
		return command.Catalog{}
	}
	e := r.env()
	r.mu.Lock()
	config, rs := r.config, r.merged(e)
	r.mu.Unlock()
	var ran []string
	for _, f := range r.Fixes {
		ran = append(ran, f.ID)
	}
	for _, x := range rs { // fixes a hands-on test applied itself (the lid-wake fix)
		if x.FixID != "" {
			ran = append(ran, x.FixID)
		}
	}
	c := ui.CatalogOf(r.Cat, r.Ident, config, ran)
	if r.Opt.Target.URL != "" {
		c.Site = r.Site()
	}
	c.Fetched, c.Bundled = r.Cat.Fetched, c.Version == bundledVersion()
	return c
}

// bundledVersion is the version of the snapshot built into the image.
var bundledVersion = sync.OnceValue(func() string {
	var s struct {
		Version string `json:"version"`
	}
	_ = json.Unmarshal(assets.Snapshot, &s)
	return s.Version
})
