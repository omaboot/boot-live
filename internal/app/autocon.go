package app

import (
	"encoding/json"
	"errors"

	"github.com/omaboot/boot-live/internal/checks"
	"github.com/omaboot/boot-live/internal/command"
)

// autoCommands are the console commands --auto runs after the quick pass, read-only ones only,
// so the image build's QEMU smoke test exercises the console's registry without a screen.
var autoCommands = []string{"ids", "report", "why boot.installer-efi64", "log Linux version"}

// autoEnv is the console's way into an unattended run: what's read-only works, the rest says so.
type autoEnv struct {
	b *Backend
	c *command.Console
}

var errAuto = errors.New("not in an unattended run")

func (e autoEnv) results() []checks.Result {
	r := e.b.R
	ev := r.env()
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.merged(ev)
}

func (e autoEnv) Tests() []string {
	var out []string
	for _, x := range e.results() {
		out = append(out, x.ID)
	}
	return out
}

func (e autoEnv) Result(id string) (command.Result, bool) {
	for _, x := range e.results() {
		if x.ID == id {
			w := map[string]string{checks.Supported: command.Pass, checks.Partial: command.Partial, checks.Failed: command.Fail}[x.Status]
			if w == "" {
				w = command.Skip
				if x.Reason == checks.LiveLimit {
					w = command.Live
				}
			}
			return command.Result{ID: x.ID, Status: w, Evidence: x.Evidence, Method: x.Method, Reason: x.Reason, Fix: x.Fix}, true
		}
	}
	return command.Result{}, false
}

func (e autoEnv) IDs() []command.ID { return e.b.IDs() }

func (e autoEnv) SMC(string) (command.SMCKey, error) { return command.SMCKey{}, errAuto }

func (e autoEnv) Summary() command.Summary {
	var s command.Summary
	for _, x := range e.results() {
		switch {
		case x.Status == checks.Supported:
			s.Pass++
		case x.Status == checks.Partial:
			s.Partial++
		case x.Status == checks.Failed:
			s.Fail++
		case x.Reason == checks.LiveLimit:
			s.Live++
		}
	}
	s.State = "unattended run: submitted or saved next"
	return s
}

func (e autoEnv) ReportJSON() ([]byte, error) {
	return json.MarshalIndent(e.b.R.Report("", "", ""), "", "  ")
}
func (e autoEnv) Log() []string                         { return nil }
func (e autoEnv) KernelLog() []string                   { return e.b.KernelLog() }
func (e autoEnv) Note(string) error                     { return errAuto }
func (e autoEnv) Bugreport() error                      { return errAuto }
func (e autoEnv) SendBug() error                        { return errAuto }
func (e autoEnv) Plan(bool) ([]string, error)           { return nil, errAuto }
func (e autoEnv) PlanRun() ([]string, error)            { return nil, errAuto }
func (e autoEnv) PlanStop() ([]string, error)           { return nil, errAuto }
func (e autoEnv) TestDrives() []command.TestDrive       { return nil }
func (e autoEnv) WriteTestFile(string) error            { return errAuto }
func (e autoEnv) SleepInfo() (command.SleepInfo, error) { return command.SleepInfo{}, errAuto }
func (e autoEnv) SleepCheck(string) error               { return errAuto }
func (e autoEnv) Rerun(string) (string, error)          { return "", errAuto }
func (e autoEnv) Force(string, string) (string, error)  { return "", errAuto }
func (e autoEnv) Screens() []string                     { return nil }
func (e autoEnv) Goto(string) error                     { return errAuto }
func (e autoEnv) Font(int) error                        { return errAuto }
func (e autoEnv) Sounds() error                         { return errAuto }
func (e autoEnv) Frames() command.Frames                { return command.Frames{} }
func (e autoEnv) Target() command.Target                { return e.b.Target() }
func (e autoEnv) Record(string, command.Kind)           {}
func (e autoEnv) Width() int                            { return 112 }
func (e autoEnv) Say(string)                            {}
func (e autoEnv) End(string) error                      { return errAuto }
func (e autoEnv) Catalog() command.Catalog              { return e.b.Catalog() }
func (e autoEnv) Gear() []string                        { return nil }
func (e autoEnv) Console() *command.Console             { return e.c }

// autoConsole runs autoCommands and hands each output line to say.
func autoConsole(r *Run, say func(string)) {
	c := command.New()
	env := autoEnv{NewBackend(r), c}
	for _, l := range autoCommands {
		c.Exec(env, l)
	}
	for _, l := range c.Log {
		say(l)
	}
}
