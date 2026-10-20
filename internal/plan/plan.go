// Package plan reads a run plan: omaboot-plan.yaml on the stick's OMABOOT partition, which
// answers a run's questions ahead and lets a run with no hands-on test go with nobody at the
// Mac (PF-15 v1, LIVE-PLAN §30). It only parses, checks and resolves the file for a Mac; the
// screens and the backend act on it.
//
//	version: 1
//	defaults:                 # every Mac, unless its section says otherwise
//	  network: ethernet       # ethernet · offline · phone · {wifi: {ssid: …, password: …}}
//	  results: save           # send · save · both
//	  tester: carl            # optional handle
//	  finish: wait            # wait · power-off · reboot
//	  start: auto             # auto (Hello counts down) · console (it waits for plan run)
//	  bugreport: [on-problem] # after-quick · at-finish · on-problem, or none; or {when: [...], send: true}
//	  tests: {default: skip}
//	macs:
//	  - match: iMac18,3       # a model identifier, or a configuration ID
//	    configuration: imac18-3-5k-2017-a
//	    tests: {network.ethernet: setup, ports.sd-card: setup}
//
// A section's keys override the defaults; its tests merge with them, its own entries winning.
package plan

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/goccy/go-yaml"
)

// File is where the plan lives on the stick, from the partition's root.
const File = "omaboot-plan.yaml"

// The modes a test can run in.
const (
	HandsOn = "hands-on" // as without a plan: a person at the Mac
	Skip    = "skip"     // not opened: not tested, "skipped by the run plan"
	Setup   = "setup"    // the equipment is in place before the run; the test decides alone
)

// SetupTests are the tests that can run as setup: they decide with no keypress when their
// device is there from the start (the Wi-Fi test once the Network step has joined).
var SetupTests = map[string]bool{"network.ethernet": true, "ports.sd-card": true, "storage.optical": true, "network.wifi": true}

// Bug report moments.
const (
	AfterQuick = "after-quick"
	AtFinish   = "at-finish"
	OnProblem  = "on-problem"
)

// Network is how a plan connects: Kind is ethernet, offline, phone or wifi; "" (the plan
// doesn't say) is whatever comes up by itself within the Network step's wait, else offline when
// nobody is there, else the person's choice.
type Network struct {
	Kind           string
	SSID, Password string
}

// section is defaults: or one Mac's section, as written.
type section struct {
	Match         string            `yaml:"match"`
	Configuration string            `yaml:"configuration"`
	Network       any               `yaml:"network"`
	Results       string            `yaml:"results"`
	Tester        string            `yaml:"tester"`
	Finish        string            `yaml:"finish"`
	Start         string            `yaml:"start"`
	Bugreport     any               `yaml:"bugreport"`
	Tests         map[string]string `yaml:"tests"`
}

type doc struct {
	Version  int       `yaml:"version"`
	Defaults section   `yaml:"defaults"`
	Macs     []section `yaml:"macs"`
}

// Plan is the file as read and checked.
type Plan struct {
	Version  int
	defaults section
	macs     []section
}

// Run is the plan for one Mac: its section over the defaults.
type Run struct {
	Section       string // the section's match, "" for the defaults alone (never: a Mac needs a section)
	Configuration string
	Network       Network
	Results       string // send, save or both
	Tester        string
	Finish        string // wait, power-off or reboot
	Start         string // auto (Hello counts down) or console (it waits for the console's plan run)
	Bugreport     map[string]bool
	BugSend       bool              // the bug reports it saves go to OmaBoot? too (bugreport: {when: …, send: true}; PF-13)
	Default       string            // the mode of a test the plan doesn't name
	Tests         map[string]string // the tests it names, by ID
}

// Known says whether a test ID is a hands-on test this build has (handson.Lookup); quick reports
// whether it is a quick-pass check instead.
type Known struct {
	Test  func(id string) bool
	Quick func(id string) bool
}

// Problem is one thing wrong with the file: its line (0 when unknown) and what to do.
type Problem struct {
	Line int
	Text string
}

func (p Problem) String() string {
	if p.Line > 0 {
		return fmt.Sprintf("line %d: %s", p.Line, p.Text)
	}
	return p.Text
}

var yamlPos = regexp.MustCompile(`^\[(\d+):\d+\]\s*`)

// Parse reads and checks a plan. Any problem makes the whole plan unusable: the run ignores it
// and says why.
func Parse(src []byte, k Known) (*Plan, []Problem) {
	var d doc
	if err := yaml.UnmarshalWithOptions(src, &d, yaml.DisallowUnknownField()); err != nil {
		msg := strings.SplitN(yaml.FormatError(err, false, false), "\n", 2)[0]
		line := 0
		if m := yamlPos.FindStringSubmatch(msg); m != nil {
			fmt.Sscan(m[1], &line)
			msg = yamlPos.ReplaceAllString(msg, "")
		}
		msg = strings.Replace(msg, "unknown field", "unknown key", 1)
		return nil, []Problem{{line, msg + " (see the template on omaboot.live, or `plan check` in the console)"}}
	}
	var ps []Problem
	at := func(key string) int { return lineOf(src, key) }
	if d.Version != 1 {
		ps = append(ps, Problem{at("version"), fmt.Sprintf("version must be 1 (this OmaBoot? reads version 1 plans), not %d", d.Version)})
	}
	if len(d.Macs) == 0 {
		ps = append(ps, Problem{at("macs"), "macs: needs at least one section, each with match: (a model identifier like iMac18,3, or a configuration ID)"})
	}
	check := func(s section, where string) {
		if s.Network != nil {
			if _, err := network(s.Network); err != "" {
				ps = append(ps, Problem{at("network"), where + "network: " + err})
			}
		}
		if s.Results != "" && s.Results != "send" && s.Results != "save" && s.Results != "both" {
			ps = append(ps, Problem{at("results"), fmt.Sprintf("%sresults: %q isn't one of send, save, both", where, s.Results)})
		}
		if s.Finish != "" && s.Finish != "wait" && s.Finish != "power-off" && s.Finish != "reboot" {
			ps = append(ps, Problem{at("finish"), fmt.Sprintf("%sfinish: %q isn't one of wait, power-off, reboot", where, s.Finish)})
		}
		if s.Start != "" && s.Start != "auto" && s.Start != "console" {
			ps = append(ps, Problem{at("start"), fmt.Sprintf("%sstart: %q isn't one of auto, console", where, s.Start)})
		}
		if s.Bugreport != nil {
			if _, err := moments(s.Bugreport); err != "" {
				ps = append(ps, Problem{at("bugreport"), where + "bugreport: " + err})
			}
		}
		if s.Tester != "" && !handle.MatchString(s.Tester) {
			ps = append(ps, Problem{at("tester"), where + "tester: letters, digits, dot, dash and underscore only, at most 39"})
		}
		ids := make([]string, 0, len(s.Tests))
		for id := range s.Tests {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		for _, id := range ids {
			mode := s.Tests[id]
			switch {
			case id == "default":
				if mode == Setup {
					ps = append(ps, Problem{at(id), where + "tests: default: setup isn't possible (only " + setupList() + " can be setup)"})
				} else if mode != HandsOn && mode != Skip {
					ps = append(ps, Problem{at(id), fmt.Sprintf("%stests: default: %q isn't one of hands-on, skip", where, mode)})
				}
			case k.Quick != nil && k.Quick(id):
				ps = append(ps, Problem{at(id), fmt.Sprintf("%stests: %s is a quick-pass check: those always run, leave it out", where, id)})
			case k.Test != nil && !k.Test(id):
				ps = append(ps, Problem{at(id), fmt.Sprintf("%stests: %s isn't a hands-on test this OmaBoot? has (the quick pass's checks always run: leave them out)", where, id)})
			case mode == "phone":
				ps = append(ps, Problem{at(id), fmt.Sprintf("%stests: %s: phone isn't possible yet (it needs the OmaBoot? iPhone app)", where, id)})
			case mode == Setup && !SetupTests[id]:
				ps = append(ps, Problem{at(id), fmt.Sprintf("%stests: %s can't be setup (only %s can); make it hands-on or skip", where, id, setupList())})
			case mode != HandsOn && mode != Skip && mode != Setup:
				ps = append(ps, Problem{at(id), fmt.Sprintf("%stests: %s: %q isn't one of hands-on, skip, setup", where, id, mode)})
			}
		}
	}
	check(d.Defaults, "defaults: ")
	if d.Defaults.Match != "" || d.Defaults.Configuration != "" {
		ps = append(ps, Problem{at("match"), "defaults: match and configuration belong in a section under macs:"})
	}
	seen := map[string]bool{}
	for i, s := range d.Macs {
		where := fmt.Sprintf("macs[%d] (%s): ", i+1, s.Match)
		if s.Match == "" {
			ps = append(ps, Problem{0, fmt.Sprintf("macs[%d]: needs match: (a model identifier like iMac18,3, or a configuration ID)", i+1)})
		}
		if seen[strings.ToLower(s.Match)] {
			ps = append(ps, Problem{0, where + "a second section for the same match"})
		}
		seen[strings.ToLower(s.Match)] = true
		check(s, where)
	}
	if len(ps) > 0 {
		return nil, ps
	}
	return &Plan{Version: d.Version, defaults: d.Defaults, macs: d.Macs}, nil
}

// handle is the tester handle Finish takes.
var handle = regexp.MustCompile(`^[A-Za-z0-9._-]{1,39}$`)

func setupList() string {
	ids := make([]string, 0, len(SetupTests))
	for id := range SetupTests {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return strings.Join(ids, ", ")
}

// lineOf is the first line with key and a colon, at its start (after spaces and a list dash)
// or inside a {…} map, or 0.
func lineOf(src []byte, key string) int {
	re := regexp.MustCompile(`(^\s*(-\s+)?|[{,]\s*)` + regexp.QuoteMeta(key) + `\s*:`)
	for i, l := range strings.Split(string(src), "\n") {
		if re.MatchString(l) {
			return i + 1
		}
	}
	return 0
}

// network reads network: as written, or why it can't.
func network(v any) (Network, string) {
	switch x := v.(type) {
	case string:
		switch x {
		case "ethernet", "offline", "phone":
			return Network{Kind: x}, ""
		case "wifi":
			return Network{}, "wifi needs its network: {wifi: {ssid: Studio, password: \"…\"}} (no password for an open network)"
		}
		return Network{}, fmt.Sprintf("%q isn't one of ethernet, offline, phone, or {wifi: {ssid: …, password: …}}", x)
	case map[string]any:
		w, ok := x["wifi"].(map[string]any)
		if len(x) != 1 || !ok {
			return Network{}, "the only map it takes is {wifi: {ssid: …, password: …}}"
		}
		n := Network{Kind: "wifi"}
		for key, val := range w {
			s, ok := val.(string)
			if !ok {
				s = fmt.Sprint(val) // a number as a password
			}
			switch key {
			case "ssid":
				n.SSID = s
			case "password":
				n.Password = s
			default:
				return Network{}, fmt.Sprintf("wifi: unknown key %q (ssid and password only)", key)
			}
		}
		if n.SSID == "" {
			return Network{}, "wifi: needs ssid:"
		}
		if n.Password != "" && (len(n.Password) < 8 || len(n.Password) > 63) {
			return Network{}, "wifi: a WPA password is 8 to 63 characters"
		}
		return n, ""
	}
	return Network{}, "ethernet, offline, phone, or {wifi: {ssid: …, password: …}}"
}

// moments reads bugreport: a list of moments, one moment, none, or [], or a map of them (when:)
// and send:.
func moments(v any) (map[string]bool, string) {
	out, _, err := bugreport(v)
	return out, err
}

// bugreport reads bugreport: its moments and whether they're sent too.
func bugreport(v any) (map[string]bool, bool, string) {
	if m, ok := v.(map[string]any); ok {
		send := false
		for k, x := range m {
			switch k {
			case "when":
			case "send":
				b, ok := x.(bool)
				if !ok {
					return nil, false, "send: true or false"
				}
				send = b
			default:
				return nil, false, fmt.Sprintf("unknown key %q (when and send only)", k)
			}
		}
		w, ok := m["when"]
		if !ok {
			w = []any{OnProblem}
		}
		if _, nested := w.(map[string]any); nested {
			return nil, false, "when: a list of after-quick, at-finish, on-problem, or none"
		}
		out, err := moments(w)
		return out, send, err
	}
	out := map[string]bool{}
	var items []any
	switch x := v.(type) {
	case string:
		if x == "none" {
			return out, false, ""
		}
		items = []any{x}
	case []any:
		items = x
	default:
		return nil, false, "a list of after-quick, at-finish, on-problem, or none; or {when: [...], send: true}"
	}
	for _, it := range items {
		s, _ := it.(string)
		switch s {
		case AfterQuick, AtFinish, OnProblem:
			out[s] = true
		default:
			return nil, false, fmt.Sprintf("%v isn't one of after-quick, at-finish, on-problem", it)
		}
	}
	return out, false, ""
}

// For resolves the plan for a Mac by its model identifier and its configuration (when one is
// known): the first section whose match is either. ok false: no section for this Mac.
func (p *Plan) For(identifier string, configs ...string) (Run, bool) {
	for _, s := range p.macs {
		m := strings.ToLower(s.Match)
		hit := m == strings.ToLower(identifier)
		for _, c := range configs {
			hit = hit || c != "" && m == strings.ToLower(c)
		}
		if hit {
			return p.resolve(s), true
		}
	}
	return Run{}, false
}

func (p *Plan) resolve(s section) Run {
	d := p.defaults
	r := Run{Section: s.Match, Configuration: s.Configuration, Results: pick(s.Results, d.Results, "save"),
		Tester: pick(s.Tester, d.Tester, ""), Finish: pick(s.Finish, d.Finish, "wait"), Start: pick(s.Start, d.Start, "auto"), Tests: map[string]string{}}
	nv := s.Network
	if nv == nil {
		nv = d.Network
	}
	if nv != nil {
		r.Network, _ = network(nv)
	}
	bv := s.Bugreport
	if bv == nil {
		bv = d.Bugreport
	}
	r.Bugreport = map[string]bool{OnProblem: true}
	if bv != nil {
		r.Bugreport, r.BugSend, _ = bugreport(bv)
	}
	for _, t := range []map[string]string{d.Tests, s.Tests} {
		for id, m := range t {
			if id == "default" {
				r.Default = m
			} else {
				r.Tests[id] = m
			}
		}
	}
	if r.Default == "" {
		r.Default = HandsOn
	}
	return r
}

func pick(a, b, def string) string {
	switch {
	case a != "":
		return a
	case b != "":
		return b
	}
	return def
}

// Mode is how the plan runs a test.
func (r Run) Mode(id string) string {
	if m, ok := r.Tests[id]; ok {
		return m
	}
	return r.Default
}

// Unattended reports whether the plan runs no test hands-on: nobody needs to be at the Mac.
func (r Run) Unattended() bool {
	if r.Default == HandsOn {
		return false
	}
	for _, m := range r.Tests {
		if m == HandsOn {
			return false
		}
	}
	return true
}

// Counts are how many of ids run in each mode.
func (r Run) Counts(ids []string) map[string]int {
	out := map[string]int{}
	for _, id := range ids {
		out[r.Mode(id)]++
	}
	return out
}

// Text is the network as the screens and the console may show it: never the password.
func (n Network) Text() string {
	switch n.Kind {
	case "wifi":
		if n.Password != "" {
			return "Wi-Fi " + n.SSID + " (password in the plan)"
		}
		return "Wi-Fi " + n.SSID + " (open)"
	case "":
		return "whatever comes up by itself"
	}
	return n.Kind
}
