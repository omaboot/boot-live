// Package send builds the report (doesitomarchy's pkg/report, schema v1), checks it with the
// same report.Check the server runs, and gets it out: POST to the site's /api/v1/reports with the
// image's source key, or a file in results/ on the BOOTLIVE stick.
package send

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/doesitomarchy/doesitomarchy/pkg/report"

	"github.com/omaboot/boot-live/internal/sysroot"
)

// TargetConf is where the image says reports go, written at image build time.
const TargetConf = "/usr/share/omaboot/target.conf"

// Target is where reports go: a DoesItOmarchy site and the source key for it; and the
// bootwatchd the network checks test through.
type Target struct {
	URL string // e.g. https://doesitomarchy.com, or a staging site
	Key string // the boot-live source key, doi_…
	// Watch is the bootwatchd the Wi-Fi and Ethernet checks move their data through (and the
	// Network step's sign-in page check): DefaultWatch unless target.conf says watch=.
	Watch string
	// Bugs is the bootwatchd bug reports go to (PF-13), production's unless target.conf says
	// bugs=: a dev image tests its network through a LAN bootwatchd but sends bug reports to
	// boot.watch, the one that keeps them. BugKey is the image's upload key for it (bugkey=).
	Bugs, BugKey string
}

// DefaultWatch is the public bootwatchd.
const DefaultWatch = "https://boot.watch"

// LoadTarget reads target.conf: simple "url=", "key=", "watch=", "bugs=" and "bugkey=" lines;
// "#" starts a comment.
func LoadTarget(r sysroot.Root) Target {
	t := Target{Watch: DefaultWatch, Bugs: DefaultWatch}
	for _, l := range r.Lines(TargetConf) {
		l = strings.TrimSpace(l)
		if strings.HasPrefix(l, "#") {
			continue
		}
		k, v, ok := strings.Cut(l, "=")
		if !ok {
			continue
		}
		switch strings.TrimSpace(k) {
		case "url":
			t.URL = strings.TrimRight(strings.TrimSpace(v), "/")
		case "key":
			t.Key = strings.TrimSpace(v)
		case "watch":
			t.Watch = strings.TrimRight(strings.TrimSpace(v), "/")
		case "bugs":
			t.Bugs = strings.TrimRight(strings.TrimSpace(v), "/")
		case "bugkey":
			t.BugKey = strings.TrimSpace(v)
		}
	}
	return t
}

// Host is the target's host name, for the screens: "doesitomarchy.com".
func (t Target) Host() string {
	if u, err := url.Parse(t.URL); err == nil && u.Host != "" {
		return u.Host
	}
	return strings.TrimPrefix(strings.TrimPrefix(t.URL, "https://"), "http://")
}

// CanSubmit reports whether the target and key are set.
func (t Target) CanSubmit() bool { return t.URL != "" && t.Key != "" }

// Production reports whether the target is DoesItOmarchy.com itself (any other host is staging
// or a development server).
func (t Target) Production() bool {
	h := strings.ToLower(strings.TrimSuffix(t.Host(), "."))
	if i := strings.LastIndex(h, ":"); i >= 0 && !strings.Contains(h[i:], "]") {
		h = h[:i] // a port
	}
	return h == "doesitomarchy.com" || h == "www.doesitomarchy.com"
}

// ErrChanged: a report whose results were changed in the console (a console entry of kind
// change) never goes to DoesItOmarchy.com; staging and external storage still take it.
var ErrChanged = errors.New("results in this run were changed in the console: DoesItOmarchy.com doesn't take its report")

// Allowed is the console guard, the last word before anything is sent: a report with results
// changed in the console is refused for the production site, whatever the screens offered.
func Allowed(t Target, f *report.File) error {
	if t.Production() && Changed(f) {
		return ErrChanged
	}
	return nil
}

// Changed reports whether the report records a console command that changed a result.
func Changed(f *report.File) bool {
	for _, c := range f.Console {
		if strings.TrimSpace(c.Kind) == report.ConsoleChange {
			return true
		}
	}
	return false
}

// Receipt is the site's answer to a submitted report.
type Receipt struct {
	Code       string   `json:"code"`
	State      string   `json:"state"`
	Config     string   `json:"config"`
	Candidates []string `json:"candidates"`
	StatusURL  string   `json:"status_url"`
}

// Page is the report's page on the site, e.g. doesitomarchy.com/report/7c41e9a2d0.
func (t Target) Page(code string) string { return t.Host() + "/report/" + code }

// Rejected is a 4xx answer: the report (or the key) wasn't accepted.
type Rejected struct {
	Status   int
	Message  string
	Problems []string
}

func (e *Rejected) Error() string {
	if len(e.Problems) > 0 {
		return fmt.Sprintf("%s: %s", e.Message, strings.Join(e.Problems, "; "))
	}
	return e.Message
}

// Busy is a 429 answer: too many reports; try again after RetryAfter.
type Busy struct {
	RetryAfter time.Duration
	Message    string
}

func (e *Busy) Error() string { return e.Message }

// Submit POSTs the report to the target's /api/v1/reports with the source key (Authorization:
// Bearer). 201 gives a receipt; 4xx a *Rejected with the server's problems; 429 a *Busy with
// Retry-After; anything else an error.
func Submit(ctx context.Context, client *http.Client, t Target, f *report.File) (Receipt, error) {
	if !t.CanSubmit() {
		return Receipt{}, errors.New("no report target or key in this image")
	}
	if err := Allowed(t, f); err != nil {
		return Receipt{}, err
	}
	body, err := json.Marshal(f)
	if err != nil {
		return Receipt{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, t.URL+"/api/v1/reports", bytes.NewReader(body))
	if err != nil {
		return Receipt{}, err
	}
	req.Header.Set("Authorization", "Bearer "+t.Key)
	req.Header.Set("Content-Type", "application/json")
	res, err := client.Do(req)
	if err != nil {
		return Receipt{}, err
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	var fail struct {
		Error    string   `json:"error"`
		Problems []string `json:"problems"`
	}
	switch {
	case res.StatusCode == http.StatusCreated || res.StatusCode == http.StatusOK:
		var rc Receipt
		if err := json.Unmarshal(b, &rc); err != nil || rc.Code == "" {
			return Receipt{}, fmt.Errorf("the site answered %s without a report code", res.Status)
		}
		return rc, nil
	case res.StatusCode == http.StatusTooManyRequests:
		_ = json.Unmarshal(b, &fail)
		wait := 10 * time.Minute
		if s, err := strconv.Atoi(strings.TrimSpace(res.Header.Get("Retry-After"))); err == nil && s > 0 {
			wait = time.Duration(s) * time.Second
		}
		msg := fail.Error
		if msg == "" {
			msg = "too many reports"
		}
		return Receipt{}, &Busy{RetryAfter: wait, Message: msg}
	case res.StatusCode >= 400 && res.StatusCode < 500:
		_ = json.Unmarshal(b, &fail)
		msg := fail.Error
		if msg == "" {
			msg = res.Status
		}
		return Receipt{}, &Rejected{Status: res.StatusCode, Message: msg, Problems: fail.Problems}
	}
	return Receipt{}, fmt.Errorf("the site answered %s", res.Status)
}
