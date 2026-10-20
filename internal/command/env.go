package command

// Env is all a command reaches the tool through: the screens (internal/ui) give it the run's
// facts and pass the rest on to the backend. Work that takes time (rerun, bugreport) starts here
// and answers later; the screens print its answer into the console.
type Env interface {
	// Tests are the IDs a command can name: the run's results and its hands-on tests.
	Tests() []string
	// Result is a result's evidence (why); false when this run has none with that ID.
	Result(id string) (Result, bool)
	// IDs are this Mac's hardware IDs.
	IDs() []ID
	// Summary is the run's report so far, counted.
	Summary() Summary
	// ReportJSON is the run's report as it stands, as it would be sent.
	ReportJSON() ([]byte, error)
	// Log is the run's full log as text (what L shows).
	Log() []string
	// KernelLog is the kernel log, each line "[    1.234] driver: message" where the time is known.
	KernelLog() []string
	// Note adds a note to the report's notes.
	Note(text string) error
	// Bugreport saves the logs, the report and the screen to the stick; the answer comes later.
	Bugreport() error
	// SendBug sends the saved bug reports (this run's, and the stick's waiting ones) to
	// OmaBoot? (PF-13); the answers come later.
	SendBug() error
	// Plan is the run plan in use (never its password), or with check, the file on the stick
	// read again and checked.
	Plan(check bool) ([]string, error)
	// PlanRun starts the run plan from the stick (read again and checked) from the screen the
	// run is on; its lines say how it went. PlanStop hands the run back to the person.
	PlanRun() ([]string, error)
	PlanStop() ([]string, error)
	// Rerun measures a test again: a quick-pass check runs again (its answer comes later), a
	// hands-on test opens again. It says what happens meanwhile.
	Rerun(id string) (string, error)
	// Force sets a result by hand (dev): pass, partial, fail or skip. It returns the status before.
	Force(id, status string) (string, error)
	// Screens are the screens goto can open now (dev): the ones seen in this run, any in the demo.
	Screens() []string
	Goto(screen string) error
	// Font loads omaboot-v at another size (dev): 16, 24 or 32.
	Font(px int) error
	// Frames is the frame timing (dev).
	Frames() Frames
	// Target is where Submit sends (dev).
	Target() Target
	// Record notes a redo or change command for the report (read-only ones never are).
	Record(command string, kind Kind)
	// Width is the box's inside, in cells: where long output wraps.
	Width() int
	// Say puts text in Boot's box on the screen showing, in place of his own line (boot say).
	Say(text string)
	// End opens the quit question with a choice selected (quit, restart, shutdown) and closes
	// the console; an error when the screen can't ask it now.
	End(choice string) error
	// Catalog is the catalog snapshot this run matches against, read offline.
	Catalog() Catalog
	// Gear is what the hands-on tests this run offers need at hand, each once, in plan order.
	Gear() []string
	// Console is the console the command runs in.
	Console() *Console
	// SMC reads one SMC key raw (smc): read-only, through applesmc.
	SMC(key string) (SMCKey, error)
	// SleepInfo is what the kernel offers for sleeping and for testing it (sleepcheck).
	SleepInfo() (SleepInfo, error)
	// SleepCheck starts a test suspend at a pm_test level, each level in turn ("all"), or a short
	// real light sleep ("s2idle"); its lines come later, each step saved to external storage
	// before it starts.
	SleepCheck(what string) error
	// TestDrives are where testfile could write OmaBoot?'s test file, and the drives it won't
	// write to, with why.
	TestDrives() []TestDrive
	// WriteTestFile writes the test file to a drive TestDrives allows (dev: "sdc1"); the answer
	// comes later.
	WriteTestFile(dev string) error
	// Sounds opens the sounds screen (Boot's sounds: an option for each moment) over the screen.
	Sounds() error
}

// TestDrive is a drive as testfile lists it.
type TestDrive struct {
	Dev  string // sdc1, or sdc for a drive with no partition table
	What string // "SanDisk Ultra · FAT32 · 15 GB"
	OK   bool   // the test file can go on it
	Why  string // why not, when it can't
}

// SleepInfo is what the kernel offers for sleeping, as sleepcheck shows it.
type SleepInfo struct {
	MemSleep       string   // /sys/power/mem_sleep: "s2idle [deep]"
	States         string   // /sys/power/state: "freeze mem disk"
	Levels         []string // pm_test's levels; nil without CONFIG_PM_DEBUG
	ConsoleSuspend string   // printk's console_suspend (Y, N), "" when the kernel lacks it
	PrintTimes     bool     // pm_print_times is there
}

// SMCKey is an SMC key as smc shows it: its name, type ("{alv", "ui16") and raw bytes.
type SMCKey struct {
	Name, Type string
	Data       []byte
}

// Result is a result as why shows it.
type Result struct {
	ID, Label string
	Status    string // Pass, Partial, Fail, Live, Skip
	Detail    string // the log's short evidence
	Evidence  string // the report's evidence
	Method    string // automatic, challenge, observed …; "" when not tested
	Reason    string // not tested: why (live-limit, no-equipment …)
	Fix       string // the OmaBoot? fix it ran with, or ""
	Forced    bool   // set by hand in the console
	HandsOn   bool   // a hands-on test's
}

// ID is one hardware ID: what it is, the ID, and a name for it when known.
type ID struct{ Key, ID, Name string }

// Summary counts the report so far and says where it stands.
type Summary struct {
	Pass, Partial, Fail, Live int
	HandsOn                   int    // hands-on results
	State                     string // "not sent yet …", "sent …", "saved …"
	Console                   int    // console commands the report records
}

// Frames is the frame timing measured in the tick.
type Frames struct {
	RenderMS float64 // drawing a frame, on average over the last second
	FPS      float64 // frames drawn a second
	Rows     int     // rows redrawn a frame, on average (Bubble Tea rewrites changed rows only)
	W, H     int
}

// Target is where Submit sends.
type Target struct {
	URL        string
	Shown      string // as the screens name it
	Production bool   // DoesItOmarchy.com itself: a marked run can't go there
	CanSubmit  bool
}

// Catalog is the catalog snapshot as catalog shows it.
type Catalog struct {
	Site     string // the site it refreshes from ("" with no target)
	Fetched  bool   // the site answered in this run
	Bundled  bool   // the snapshot in use is the one in the image
	Version  string
	Date     string // the day its content last changed; "" in older snapshots
	Macs     int
	Configs  int
	Mac      CatalogMac
	Releases []Release // every release, in catalog order (search)
	Fixes    []Fix     // the fixes it lists for this configuration
	Ports    []Port    // this release's ports, side by side; nil without a port map
}

// CatalogMac is this Mac as the catalog matched it.
type CatalogMac struct {
	Identifier, Release, Config string // "" when the catalog doesn't know it (Config: not settled)
	Known                       bool
	By                          string   // what identified the model: product_name, board_id or devices
	Matched                     []string // the hardware IDs the configuration matched ("cpu:…", "board:…", "pci:…")
}

// Release is one of the catalog's releases.
type Release struct {
	Identifier, Name string
	Configs          int
}

// Fix is a fix the catalog lists for this configuration.
type Fix struct {
	ID, Name string
	Ran      bool // it took effect in this run
}

// Port is one of the release's ports.
type Port struct{ Side, Name string }
