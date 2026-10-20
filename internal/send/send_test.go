package send

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/doesitomarchy/doesitomarchy/pkg/report"

	"github.com/omaboot/boot-live/internal/checks"
	"github.com/omaboot/boot-live/internal/identify"
	"github.com/omaboot/boot-live/internal/sysroot"
)

// A report built from each fixture's quick pass passes pkg/report's Check (the server's first
// pass), says it's a live boot from boot-live, lists the fixes that took effect, and carries no
// serial number although the fixtures have them.
func TestReportFromFixtures(t *testing.T) {
	cat, err := identify.Embedded()
	if err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{"macbookair5,2", "imac10,1", "imac18,3", "qemu"} {
		t.Run(dir, func(t *testing.T) {
			r := sysroot.Root("../../testdata/" + dir)
			h := identify.Read(r)
			e := &checks.Env{Root: r, HW: h, Ident: cat.Identify(h), Cat: cat, Kmsg: checks.ReadKmsg(r)}
			e.Fixes = checks.DetectFixes(r, e.Kmsg, cat)
			var rs []checks.Result
			checks.Run(context.Background(), e, checks.Plan(e), func(x checks.Result) { rs = append(rs, x) })
			items, extras := checks.Items(e, rs)
			var fixes []string
			for _, f := range e.Fixes {
				fixes = append(fixes, f.ID)
			}
			f := Build(Input{HW: h, Ident: e.Ident, Config: e.Ident.Config, Items: items, Extras: extras, Fixes: fixes,
				Omarchy: OmarchyVersion(r, "4.0.4"), Image: Image(r, "live"), Version: "0.2.0 dev", Handle: "sam_r",
				Notice: "the consent notice", TestedAt: time.Now()})
			if err := Check(f); err != nil {
				t.Fatal(err)
			}
			b, _ := json.Marshal(f)
			back, err := report.Parse(b)
			if err != nil {
				t.Fatalf("the report doesn't parse back: %v", err)
			}
			if err := Check(back); err != nil {
				t.Fatalf("parsed back: %v", err)
			}
			if strings.Contains(string(b), "SERIAL") {
				t.Errorf("a serial number got into the report: %s", b)
			}
			if f.Context != report.ContextLive || f.Source.ID != "boot-live" || f.Omarchy.Image != "omaboot-live dev 2026-10-10" || f.Omarchy.Version != "4.0.4" {
				t.Errorf("context %q source %q image %q omarchy %q", f.Context, f.Source.ID, f.Omarchy.Image, f.Omarchy.Version)
			}
			if dir == "imac10,1" && (len(f.Fixes) != 1 || f.Fixes[0] != checks.FixRadeon) {
				t.Errorf("fixes %v", f.Fixes)
			}
			if dir != "qemu" && f.Config == "" {
				t.Error("no config for a Mac the catalog has")
			}
		})
	}
}

// The "Name to credit" field allows only what the server accepts as a handle.
func TestHandle(t *testing.T) {
	for in, want := range map[string]string{"sam_r": "sam_r", "Carl H": "Carl_H", "-x": "x", "Zoë": "Zo", "": ""} {
		if got := Handle(in); got != want {
			t.Errorf("Handle(%q) = %q, want %q", in, got, want)
		}
		if h := Handle(in); h != "" && !report.HandleRe.MatchString(h) {
			t.Errorf("Handle(%q) = %q doesn't pass the server's check", in, h)
		}
	}
}

// The console guard is the last word before a request: a report with a change entry is refused
// for DoesItOmarchy.com without anything going out, and still sent to any other host.
func TestAllowedOnlyBlocksProduction(t *testing.T) {
	marked := &report.File{Console: []report.ConsoleEntry{{Command: "rerun boot.install", Kind: report.ConsoleRedo},
		{Command: "force boot.install pass", Kind: report.ConsoleChange}}}
	redone := &report.File{Console: []report.ConsoleEntry{{Command: "rerun boot.install", Kind: report.ConsoleRedo}}}
	prod := Target{URL: "https://doesitomarchy.com", Key: "doi_x"}
	staging := Target{URL: "https://staging.doesitomarchy.com", Key: "doi_x"}
	if err := Allowed(prod, marked); !errors.Is(err, ErrChanged) {
		t.Errorf("production, marked: %v", err)
	}
	for _, c := range []struct {
		t Target
		f *report.File
	}{{prod, redone}, {staging, marked}, {staging, redone}} {
		if err := Allowed(c.t, c.f); err != nil {
			t.Errorf("%s: %v", c.t.URL, err)
		}
	}
	client := &http.Client{Transport: roundTrip(func(*http.Request) (*http.Response, error) {
		t.Fatal("a marked report was about to be sent to production")
		return nil, nil
	})}
	if _, err := Submit(context.Background(), client, prod, marked); !errors.Is(err, ErrChanged) {
		t.Errorf("Submit: %v", err)
	}
	for host, want := range map[string]bool{"https://doesitomarchy.com": true, "https://WWW.doesitomarchy.com/": true,
		"http://doesitomarchy.com:8080": true, "https://doesitomarchy.com.evil.example": false, "http://localhost:8080": false, "": false} {
		if got := (Target{URL: host}).Production(); got != want {
			t.Errorf("Production(%q) = %v", host, got)
		}
	}
}

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
