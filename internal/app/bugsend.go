package app

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/doesitomarchy/doesitomarchy/pkg/scrub"

	"github.com/omaboot/boot-live/internal/send"
	"github.com/omaboot/boot-live/internal/ui"
)

// Sending bug reports to OmaBoot? (PF-13): a bug report saved on the stick (Bugreport) goes to
// the bug server (target.conf bugs=, boot.watch) only when the person asks (the console's
// bugreport send) or the run plan says send. Every file is scrubbed first with DoesItOmarchy's
// rules (pkg/scrub: serials, MAC and IP addresses, host and user names, e-mail addresses), and
// the server scrubs again. The copy on the stick stays whole. One that can't go (offline) is
// marked on the stick (<name>.unsent) for an online run to send; one that went gets <name>.sent
// with its code.

// bugManifest says what a sent bug report is, for whoever reads it.
type bugManifest struct {
	Version       string `json:"version"`          // OmaBoot?'s
	Identifier    string `json:"identifier"`       // the model identifier
	Configuration string `json:"configuration"`    // the catalog configuration, when known
	Report        string `json:"report,omitempty"` // the run's DoesItOmarchy report code, when it went
	Saved         string `json:"saved"`            // its name on the stick
}

// SendBug sends the bug report saved as name, or with "" the ones saved in this run and those
// waiting on the stick; a BugSentMsg for each.
func (b *Backend) SendBug(name string) {
	r := b.R
	go func() {
		names := []string{name}
		if name == "" {
			names = r.bugsToSend()
		}
		if len(names) == 0 {
			b.post(ui.BugSentMsg{Err: "no bug report to send: bugreport saves one first"})
			return
		}
		for _, n := range names {
			b.post(r.sendBug(n))
		}
	}()
}

// bugsToSend are this run's saved bug reports not sent yet, then the stick's unsent ones.
func (r *Run) bugsToSend() []string {
	seen := map[string]bool{}
	var out []string
	r.mu.Lock()
	for n := range r.bugMem {
		out = append(out, n)
		seen[n] = true
	}
	r.mu.Unlock()
	sort.Strings(out)
	for _, n := range r.unsentBugs() {
		if !seen[n] {
			out = append(out, n)
		}
	}
	sent := r.sentBugs()
	keep := out[:0]
	for _, n := range out {
		if !sent[n] {
			keep = append(keep, n)
		}
	}
	return keep
}

// unsentBugs are the bug reports marked on the stick to send (results/<name>.unsent).
func (r *Run) unsentBugs() []string {
	return r.stickNames(".unsent")
}

// sentBugs are the bug reports on the stick that went (results/<name>.sent).
func (r *Run) sentBugs() map[string]bool {
	out := map[string]bool{}
	for _, n := range r.stickNames(".sent") {
		out[n] = true
	}
	return out
}

// stickNames are the bug reports on the stick with a marker file ending in suffix.
func (r *Run) stickNames(suffix string) []string {
	var out []string
	ctx, cancel := context.WithTimeout(r.ctx, 20*time.Second)
	defer cancel()
	send.WithStick(ctx, r.Opt.Root, r.Opt.StickDir, false, func(base string) error {
		m, _ := filepath.Glob(filepath.Join(base, "results", "bugreport-*.tar"+suffix))
		for _, p := range m {
			out = append(out, "results/"+strings.TrimSuffix(filepath.Base(p), suffix))
		}
		return nil
	})
	sort.Strings(out)
	return out
}

// sendBug sends one bug report: scrubbed, with its manifest; marked on the stick either way.
func (r *Run) sendBug(name string) ui.BugSentMsg {
	r.mu.Lock()
	body := r.bugMem[name]
	man := bugManifest{Version: r.Opt.Version, Identifier: r.Ident.Identifier, Configuration: r.config, Report: r.code, Saved: path.Base(name)}
	r.mu.Unlock()
	if man.Configuration == "" {
		man.Configuration = r.Ident.Config
	}
	ctx, cancel := context.WithTimeout(r.ctx, 60*time.Second)
	defer cancel()
	if body == nil {
		err := send.WithStick(ctx, r.Opt.Root, r.Opt.StickDir, false, func(base string) error {
			var err error
			body, err = os.ReadFile(filepath.Join(base, filepath.FromSlash(name)))
			return err
		})
		if err != nil {
			return ui.BugSentMsg{Path: name, Err: "can't read it from the stick: " + errText(err)}
		}
	}
	out, err := bugForSending(body, man)
	if err != nil {
		return ui.BugSentMsg{Path: name, Err: errText(err)}
	}
	code, err := send.SendBug(ctx, r.client, r.Opt.Target, r.macHash(), r.Opt.Version, out)
	var refused *send.BugRefused
	switch {
	case errors.As(err, &refused) && refused.Retry == 0:
		return ui.BugSentMsg{Path: name, Err: "not taken: " + refused.Message}
	case err != nil: // not reached, or busy: it waits on the stick
		send.SaveBytes(ctx, r.Opt.Root, r.Opt.StickDir, []byte("waiting to be sent to OmaBoot? (bugreport send)\n"), name+".unsent", "")
		why := "couldn't reach " + strings.TrimPrefix(strings.TrimPrefix(r.Opt.Target.Bugs, "https://"), "http://")
		if refused != nil {
			why = refused.Message
		}
		return ui.BugSentMsg{Path: name, Later: true, Err: why}
	}
	send.SaveBytes(ctx, r.Opt.Root, r.Opt.StickDir, []byte(code+"\n"), name+".sent", name+".unsent")
	r.mu.Lock()
	delete(r.bugMem, name)
	if r.plan != nil && r.plan.run != nil {
		r.plan.events = append(r.plan.events, "bug report sent as "+code)
	}
	r.mu.Unlock()
	return ui.BugSentMsg{Path: name, Code: code}
}

// bugForSending is a saved bug report as it's sent: each file scrubbed, under one directory,
// with manifest.json.
func bugForSending(body []byte, man bugManifest) ([]byte, error) {
	tr := tar.NewReader(bytes.NewReader(body))
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	dir := strings.TrimSuffix(man.Saved, ".tar")
	now := time.Now()
	put := func(n string, data []byte) {
		tw.WriteHeader(&tar.Header{Name: dir + "/" + n, Mode: 0o644, Size: int64(len(data)), ModTime: now, Typeflag: tar.TypeReg})
		tw.Write(data)
	}
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}
		if h.Typeflag != tar.TypeReg {
			continue
		}
		data, err := io.ReadAll(tr)
		if err != nil {
			return nil, err
		}
		put(path.Base(h.Name), []byte(scrub.Text(string(data))))
	}
	j, _ := json.MarshalIndent(man, "", "  ")
	put("manifest.json", append(j, '\n'))
	if err := tw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// macHash stands for this Mac at the bug server, for its per-Mac limit only (never kept there):
// a salted hash of the firmware's product UUID, which never leaves the Mac; without one, of
// what identifies the model.
func (r *Run) macHash() string {
	id := strings.TrimSpace(r.Opt.Root.Read("/sys/class/dmi/id/product_uuid"))
	if id == "" {
		id = r.HW.ProductName + "|" + r.HW.BoardID + "|" + r.HW.CPU
	}
	sum := sha256.Sum256([]byte("omaboot-bugs-v1\n" + id))
	return hex.EncodeToString(sum[:])
}
