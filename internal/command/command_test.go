package command

import (
	"reflect"
	"slices"
	"strings"
	"testing"
)

// fakeEnv answers with fixed facts and notes what was recorded.
type fakeEnv struct {
	tests    []string
	recorded []string
	forceErr error
	said     []string
	con      *Console
	written  []string
}

func (e *fakeEnv) Tests() []string { return e.tests }
func (e *fakeEnv) Result(id string) (Result, bool) {
	return Result{ID: id, Status: Pass, Evidence: "measured"}, true
}
func (e *fakeEnv) IDs() []ID                   { return []ID{{Key: "model", ID: "MacBookAir5,2"}} }
func (e *fakeEnv) Summary() Summary            { return Summary{} }
func (e *fakeEnv) ReportJSON() ([]byte, error) { return []byte(`{"a": 1}`), nil }
func (e *fakeEnv) Log() []string               { return nil }
func (e *fakeEnv) KernelLog() []string {
	return []string{"[    1.207] i915 0000:00:02.0: ok", "[    1.300] applesmc: x"}
}
func (e *fakeEnv) Note(string) error                    { return nil }
func (e *fakeEnv) Bugreport() error                     { return nil }
func (e *fakeEnv) SendBug() error                       { return nil }
func (e *fakeEnv) Plan(bool) ([]string, error)          { return nil, nil }
func (e *fakeEnv) PlanRun() ([]string, error)           { return nil, nil }
func (e *fakeEnv) PlanStop() ([]string, error)          { return nil, nil }
func (e *fakeEnv) SleepInfo() (SleepInfo, error)        { return SleepInfo{}, nil }
func (e *fakeEnv) SleepCheck(string) error              { return nil }
func (e *fakeEnv) Rerun(string) (string, error)         { return "about 2 s", nil }
func (e *fakeEnv) Force(string, string) (string, error) { return Partial, e.forceErr }
func (e *fakeEnv) Screens() []string                    { return nil }
func (e *fakeEnv) Goto(string) error                    { return nil }
func (e *fakeEnv) Font(int) error                       { return nil }
func (e *fakeEnv) Frames() Frames                       { return Frames{} }
func (e *fakeEnv) Target() Target                       { return Target{} }
func (e *fakeEnv) Width() int                           { return 112 }
func (e *fakeEnv) Record(c string, k Kind)              { e.recorded = append(e.recorded, k.Word()+" "+c) }
func (e *fakeEnv) Say(text string)                      { e.said = append(e.said, text) }
func (e *fakeEnv) End(string) error                     { return nil }
func (e *fakeEnv) Catalog() Catalog                     { return Catalog{} }
func (e *fakeEnv) Gear() []string                       { return []string{"headphones"} }
func (e *fakeEnv) Console() *Console                    { return e.con }
func (e *fakeEnv) TestDrives() []TestDrive {
	return []TestDrive{{Dev: "sdc1", What: "SanDisk Ultra · FAT32 · 15 GB", OK: true}, {Dev: "sda", What: "internal SSD", Why: "internal: read-only for the whole run"}}
}
func (e *fakeEnv) WriteTestFile(dev string) error { e.written = append(e.written, dev); return nil }
func (e *fakeEnv) Sounds() error                  { return nil }
func (e *fakeEnv) SMC(key string) (SMCKey, error) {
	return SMCKey{Name: key, Type: "{alv", Data: []byte{0, 0x1c, 0, 0, 1, 0, 0, 0x98, 0, 0}}, nil
}

// a change command of the tests' own: release builds have none (force is dev-only)
func init() {
	Register(Cmd{Name: "zz-change", Kind: Change, Order: 999, Run: func(env Env, args []string, out *Out) error {
		if len(args) == 0 {
			return Err("needs an argument")
		}
		return nil
	}})
}

func TestComplete(t *testing.T) {
	env := &fakeEnv{tests: []string{"display.native-resolution", "graphics.integrated", "graphics.discrete"}}
	for _, c := range []struct {
		in, rest string
		matches  []string
	}{
		{"", "", nil},  // nothing typed: no completion
		{"?", "", nil}, // ? isn't offered
		{"re", "", []string{"report", "rerun", "restart"}},
		{"rer", "un ", []string{"rerun"}},
		{"rerun ", "", env.tests[:3]}, // several: listed, nothing added
		{"rerun gr", "aphics.", []string{"graphics.integrated", "graphics.discrete"}},
		{"rerun di", "splay.native-resolution ", []string{"display.native-resolution"}},
		{"rerun display.native-resolution x", "", nil},                                 // no third argument
		{"why  di", "splay.native-resolution ", []string{"display.native-resolution"}}, // runs of spaces
		{"nosuch a", "", nil},
		{"log i9", "15 ", []string{"i915"}},
		{"report j", "son ", []string{"json"}},
	} {
		rest, m := Complete(env, c.in)
		if rest != c.rest || !reflect.DeepEqual(m, c.matches) {
			t.Errorf("Complete(%q) = %q %q, want %q %q", c.in, rest, m, c.rest, c.matches)
		}
	}
}

func TestSplitAndKnown(t *testing.T) {
	if n, a := Split("  rerun   graphics.integrated  "); n != "rerun" || !reflect.DeepEqual(a, []string{"graphics.integrated"}) {
		t.Errorf("Split: %q %q", n, a)
	}
	if n, a := Split("   "); n != "" || a != nil {
		t.Errorf("Split of spaces: %q %q", n, a)
	}
	for in, want := range map[string]bool{"": true, "re": true, "rerun": true, "rerun x": true, "rex": false, "rex y": false, "?": true} {
		if New().Known(in) != want {
			t.Errorf("Known(%q) = %v", in, !want)
		}
	}
}

// Exec records redo and change commands (as typed, spaces tidied) and nothing else; only a change
// that worked marks the run.
func TestExecRecordsAndMarks(t *testing.T) {
	env := &fakeEnv{}
	c := New()
	c.Exec(env, "ids")
	c.Exec(env, "note  fans   loud")
	c.Exec(env, "rerun   graphics.integrated")
	c.Exec(env, "zz-change")    // fails: neither recorded nor marked
	c.Exec(env, "nosuch thing") // red, nothing else
	if c.Marked() {
		t.Fatal("marked before any change worked")
	}
	c.Exec(env, "zz-change  x")
	want := []string{"redo rerun graphics.integrated", "change zz-change x"}
	if !reflect.DeepEqual(env.recorded, want) {
		t.Errorf("recorded %q, want %q", env.recorded, want)
	}
	if !reflect.DeepEqual(c.Marks, []string{"zz-change x"}) {
		t.Errorf("marks %q", c.Marks)
	}
	log := strings.Join(c.Log, "\n")
	for _, s := range []string{"> ids", "MacBookAir5,2", "nosuch: no such command", "this run is marked"} {
		if !strings.Contains(log, s) {
			t.Errorf("the full log is missing %q:\n%s", s, log)
		}
	}
	c.Exec(env, "clear")
	if len(c.Out) != 0 || !strings.Contains(strings.Join(c.Log, "\n"), "> clear") {
		t.Errorf("clear: %d lines left; the log keeps it: %v", len(c.Out), c.Log[len(c.Log)-1])
	}
}

func TestHistory(t *testing.T) {
	env := &fakeEnv{}
	c := New()
	for _, l := range []string{"ids", "ids", "report"} {
		c.Exec(env, l)
	}
	if !reflect.DeepEqual(c.Hist, []string{"ids", "report"}) {
		t.Fatalf("history %q: a repeat isn't kept twice", c.Hist)
	}
	steps := []struct {
		d    int
		want string
	}{{-1, "report"}, {-1, "ids"}, {-1, "ids"}, {1, "report"}, {1, ""}, {1, ""}}
	for i, s := range steps {
		c.History(s.d)
		if string(c.Input) != s.want {
			t.Errorf("step %d: %q, want %q", i, string(c.Input), s.want)
		}
	}
}

func TestColouredLines(t *testing.T) {
	if l := KernelLine("[    3.104] applesmc: warning: light sensor read 0"); len(l) != 3 || l[1].Text != "applesmc:" || l[2].FG != Mark {
		t.Errorf("warning line: %+v", l)
	}
	if l := KernelLine("[   12.555] usb 1-1: device descriptor read/64, error -71"); l[1].Text != "usb 1-1:" || l[2].FG != Bad {
		t.Errorf("error line: %+v", l)
	}
	if l := JSONLine(`  "schema": 1,`); l.Text() != `  "schema": 1,` || l[1].FG != Text || l[4].FG != Value {
		t.Errorf("json line: %+v", l)
	}
}

// boot say goes to Boot's box, and only as much as his box holds on the smallest console.
func TestBootSay(t *testing.T) {
	env := &fakeEnv{}
	c := New()
	env.con = c
	c.Exec(env, "boot say "+strings.Repeat("x", SayMax))
	c.Exec(env, "boot say "+strings.Repeat("x", SayMax+1))
	if len(env.said) != 1 || len([]rune(env.said[0])) != SayMax {
		t.Errorf("said %q", env.said)
	}
	if got := c.Out[len(c.Out)-1].Text(); !strings.Contains(got, "at most 60 characters") {
		t.Errorf("too long: %q", got)
	}
	if n := len(c.Out); n != 3 {
		t.Errorf("%d lines in the console, want the two echoes and the error", n)
	}
}

// Unlisted commands are never in help nor offered by Tab, and the ones that depend on another
// don't exist until it's set: their names read as no command at all.
func TestUnlistedCommands(t *testing.T) {
	env := &fakeEnv{}
	c := New()
	env.con = c
	gated := []string{"god", "noclip", "kill", "impulse", "r_speeds", "map"}
	check := func(on bool) {
		t.Helper()
		var out Out
		if err := help(env, nil, &out); err != nil {
			t.Fatal(err)
		}
		var listed []string
		for _, l := range out.Lines {
			listed = append(listed, l.Text())
		}
		all := strings.Join(listed, "\n")
		for _, name := range append([]string{"sv_cheats"}, gated...) {
			if strings.Contains(all, name) {
				t.Errorf("on %v: help lists %s", on, name)
			}
			for i := 1; i <= len(name); i++ {
				if _, m := Complete(env, name[:i]); slices.Contains(m, name) {
					t.Errorf("on %v: Tab offers %s for %q", on, name, name[:i])
				}
			}
		}
		for _, name := range gated {
			c.Exec(env, name)
			got := c.Out[len(c.Out)-1].Text()
			if none := strings.Contains(got, "no such command"); none == on {
				t.Errorf("on %v: %s answered %q", on, name, got)
			}
			if c.Known(name) != on || c.Known(name[:1]+"\x00") {
				t.Errorf("on %v: %s reads as known %v", on, name, !on)
			}
		}
	}
	check(false)
	c.Exec(env, "sv_cheats 1")
	if !c.X.On || len(env.said) != 1 {
		t.Fatalf("sv_cheats 1: on %v, said %q", c.X.On, env.said)
	}
	check(true)
	c.Exec(env, "sv_cheats 0")
	if c.X != (Extra{}) {
		t.Errorf("sv_cheats 0 left %+v", c.X)
	}
	check(false)
	if len(env.recorded) > 0 {
		t.Errorf("recorded for the report: %q", env.recorded)
	}
}

// PgUp stops at the oldest full page, and output arriving while scrolled back keeps the view
// put without passing it (0.7.2 review: PgUp scrolled past the oldest line and cleared the box).
func TestScrollClamp(t *testing.T) {
	c := New()
	for i := 0; i < 10; i++ {
		c.Print(Line{T("line")})
	}
	c.Rows = 3
	for i := 0; i < 10; i++ {
		c.ScrollBy(3)
	}
	if c.Scroll != 7 {
		t.Errorf("scrolled back %d, want 7 (10 lines, 3 rows)", c.Scroll)
	}
	c.Print(Line{T("more")})
	if c.Scroll != 8 {
		t.Errorf("after a new line: %d, want 8", c.Scroll)
	}
	c.Rows = 20 // tall: everything fits
	c.ScrollBy(0)
	if c.Scroll != 0 {
		t.Errorf("tall, all 11 lines shown: scrolled %d", c.Scroll)
	}
	c.ScrollBy(-3)
	if c.Scroll != 0 {
		t.Errorf("PgDn at the newest: %d", c.Scroll)
	}
}

// testfile writes only after a y, only to a drive it allows, and is never recorded.
func TestTestfileAsks(t *testing.T) {
	c := New()
	env := &fakeEnv{con: c}
	c.Exec(env, "testfile write sdc1")
	if len(env.written) != 0 || c.Ask == nil {
		t.Fatalf("wrote before asking: %v", env.written)
	}
	c.Exec(env, "n")
	if len(env.written) != 0 || c.Ask != nil {
		t.Fatalf("n wrote: %v", env.written)
	}
	c.Exec(env, "testfile write sdc1")
	c.Exec(env, "y")
	if !slices.Equal(env.written, []string{"sdc1"}) {
		t.Fatalf("y wrote %v", env.written)
	}
	c.Exec(env, "testfile write sda")
	if c.Ask != nil || len(env.written) != 1 {
		t.Fatal("asked to write to a refused drive")
	}
	if len(env.recorded) != 0 {
		t.Errorf("recorded %v", env.recorded)
	}
}
