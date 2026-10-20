package main

import (
	"time"

	"github.com/omaboot/boot-live/internal/demo"
	"github.com/omaboot/boot-live/internal/grid"
	"github.com/omaboot/boot-live/internal/ui"
)

// consoleShots are the command console (LIVE-PLAN §24, prototype v52) over the demo's screens,
// the console worked by typed keys (ui.ScriptConsole): "\t" Tab, "\n" Enter, "^up" Ctrl+↑. The
// dev-only commands (force) need the omaboot_dev tag: go run -tags omaboot_dev ./tools/screens.
func consoleShots() []shot {
	opt := ui.Options{Edition: ui.Live, Mode: grid.VT, Palette: grid.OmaBoot, Version: "0.7.7 dev", Font: "omaboot-v", FontPx: 16}
	online := func(s *ui.State) { wifiUp(s) }
	done := func(s *ui.State) { s.PassDone = true }
	con := func(name string, s ui.State, f *ui.Facts, steps ...string) shot {
		return shot{name: name, st: ui.ScriptConsole(opt, s, f, demo.NewBackend(), steps...), f: f}
	}
	full := demo.Finished
	// sound on, the cursor on the fourth moment, its second option picked
	soundsOn := func(s shot) shot {
		s.st.Sound = s.st.Sound.With("live", 1)
		s.st.Sound.On, s.st.SoundRow = true, 3
		return s
	}
	// the console opened half a second ago: slid up, Boot still glancing down at it
	opening := func(s ui.State) ui.State {
		s.Con.At = time.Now().Add(-500 * time.Millisecond)
		return s
	}
	marked := func(f *ui.Facts, prod bool) *ui.Facts {
		f.Production = prod
		if !prod {
			f.Site = "staging.doesitomarchy.com"
		}
		return f
	}
	stagingFacts := marked(full(), false)
	sent := demo.Finished()
	sent.Report, sent.ReportPage = demo.Report, demo.Site+"/report/"+demo.Report
	guard := func(s *ui.State) {
		s.Guard, s.Con.Marks = true, []string{"force display.native-resolution pass", "force thermal.fans fail"}
	}

	return []shot{
		con("console-1-network-why", st(ui.Network, 13, net(online), done), full(), "why net", "\t", "\n"),
		con("console-1-network-ids", st(ui.Network, 13, net(online), done), full(), "why net", "\t", "\n", "ids", "\n"),
		con("console-1-network-ids-pgup", st(ui.Network, 13, net(online), done), full(), "why net", "\t", "\n", "ids", "\n", "pgup"),
		{name: "console-1-network-opening", st: opening(ui.ScriptConsole(opt, st(ui.Network, 13, net(online), done), full(), demo.NewBackend())), f: full()},
		con("console-2-quick-log", st(ui.Quick, 30, online), full(), "log", "\n"),
		con("console-2-quick-log-tall", st(ui.Quick, 30, online), full(), "log", "\n", "^up"),
		con("console-2-quick-log-tall-fps", st(ui.Quick, 30, online), full(), "log", "\n", "^up", "fps", "\n"),
		con("console-3-results-help", st(ui.Results, 10, online, done), full(), "^up", "?", "\n"),
		con("console-3-results-tab-matches", st(ui.Results, 10, online, done), full(), "re", "\t"),
		con("console-3-results-tab-ghost", st(ui.Results, 10, online, done), full(), "re", "\t", "r", "\t", "display"),
		con("console-3-results-rerun", st(ui.Results, 10, online, done), full(), "rerun display", "\t", "\n"),
		con("console-3-results-report", st(ui.Results, 10, online, done), full(), "report", "\n", "note fans got loud at the end", "\n"),
		con("console-3-results-report-json", st(ui.Results, 10, online, done), full(), "^up", "report json", "\n"),
		con("console-3-results-unknown", st(ui.Results, 10, online, done), full(), "frobnicate", "\n", "fro"),
		con("console-3-results-bugreport", st(ui.Results, 10, online, done), full(), "bugreport", "\n", "boot say hi there", "\n"),
		con("console-4-finish-force", st(ui.Finish, 6, online, done), full(), "force display.native-resolution pass", "\n"),
		{name: "console-4-finish-guard-staging", st: st(ui.Finish, 6, online, done, guard), f: stagingFacts},
		{name: "console-4-finish-guard-production", st: st(ui.Finish, 6, online, done, guard), f: full()},
		{name: "console-4-finish-marked", st: st(ui.Finish, 6, online, done, func(s *ui.State) { s.Con.Marks = []string{"force thermal.fans fail"} }), f: full()},
		// Finish's Send a bug report (PF-13): the action, its form, sending, sent, waiting
		{name: "console-4-finish-bug", st: st(ui.Finish, 6, online, done, func(s *ui.State) { s.FinishSel = 5 }), f: bugFacts()},
		{name: "console-4-finish-bug-form", st: st(ui.Finish, 6, online, done, func(s *ui.State) { s.BugForm = true }), f: bugFacts()},
		{name: "console-4-finish-bug-sending", st: st(ui.Finish, 6, online, done, func(s *ui.State) { s.BugForm, s.BugSending = true, true }), f: bugFacts()},
		{name: "console-4-finish-bug-sent", st: st(ui.Finish, 6, online, done, func(s *ui.State) { s.BugForm, s.BugCode = true, "B-7K2Q-9MXD" }), f: bugFacts()},
		{name: "console-4-finish-bug-sent-list", st: st(ui.Finish, 6, online, done, func(s *ui.State) { s.BugCode, s.FinishSel = "B-7K2Q-9MXD", 5 }), f: bugFacts()},
		{name: "console-4-finish-bug-later", st: st(ui.Finish, 6, done, func(s *ui.State) {
			s.BugForm, s.BugNote = true, "Not sent yet (couldn't reach boot.watch): it waits on external storage, and the console's bugreport send sends it from an online run."
		}), f: bugFacts()},
		testCon("console-5-test-usb-a", playShot("", "ports.usb-a", 6, nil), "why ports.usb-a", "\n"),
		testCon("console-5-test-backlight", playShot("", "input.keyboard-backlight", 4, nil), "report", "\n"),
		// the keyboard test has the keyboard: the console's key dims, and ` types into the test
		playShot("console-5-test-keyboard-takes-keys", "input.keyboard", 4, nil),
		// tall is half the screen, empty or not (owner, 2026-10-15)
		con("console-6-tall-empty", st(ui.Results, 10, online, done), full(), "^up"),
		// boot say: in Boot's own box on the screen showing, the console only echoes it
		said(con("console-7-say-hello", st(ui.Hello, 8), demo.Facts(), "boot say hello from the console", "\n")),
		said(con("console-7-say-network", st(ui.Network, 13, net(online), done), full(), "boot say this line has exactly sixty characters, the most Boot takes.", "\n", "close", "\n")),
		said(con("console-7-say-quick", st(ui.Quick, 30, online), full(), "boot say this line has exactly sixty characters, the most Boot takes.", "\n")),
		said(con("console-7-say-finish", st(ui.Finish, 6, online, done), full(), "boot say all done here", "\n", "close", "\n")),
		said(testCon("console-7-say-test", playShot("", "ports.usb-a", 6, nil), "boot say this line has exactly sixty characters, the most Boot takes.", "\n")),
		con("console-7-say-too-long", st(ui.Results, 10, online, done), full(), "boot say this line has exactly sixty characters, the most Boot takes.!", "\n"),
		// catalog, offline
		con("console-8-catalog", st(ui.Results, 10, online, done), full(), "^up", "catalog", "\n", "catalog source", "\n"),
		con("console-8-catalog-mac", st(ui.Results, 10, online, done), full(), "^up", "catalog mac", "\n", "catalog ports", "\n"),
		con("console-8-catalog-search", st(ui.Results, 10, online, done), full(), "^up", "catalog search macbook pro", "\n", "catalog fixes", "\n"),
		con("console-8-catalog-tab", st(ui.Results, 10, online, done), full(), "catalog ", "\t"),
		// quit, restart, shutdown: Esc's own question with that choice selected, the console closed
		// sleepcheck (0.7.5): what the kernel offers, then "all" as it comes back level by level
		con("console-10-sleepcheck", st(ui.Results, 10, online, done), full(), "^up", "sleepcheck", "\n"),
		sleepCheck(con("console-10-sleepcheck-all", st(ui.Results, 10, online, done), full(), "^up", "sleepcheck all", "\n")),
		// 0.7.6: nosbp2, the FireWire disk's driver off for the step and back after (0.7.7: unloaded)
		sleepCheckOf("devices nosbp2", con("console-11-sleepcheck-nosbp2", st(ui.Results, 10, online, done), full(), "^up", "sleepcheck devices nosbp2", "\n")),
		con("console-11-sleepcheck-tab", st(ui.Results, 10, online, done), full(), "sleepcheck devices ", "\t"),
		// 0.7.7: nosbp2 unloads the module; deep is a real deep sleep (with nosbp2, the workaround's test)
		sleepCheckOf("deep nosbp2", con("console-12-sleepcheck-deep-nosbp2", st(ui.Results, 10, online, done), full(), "^up", "sleepcheck deep nosbp2", "\n")),
		con("console-12-sleepcheck-tab-steps", st(ui.Results, 10, online, done), full(), "sleepcheck ", "\t"),
		con("console-9-quit", st(ui.Results, 10, online, done), full(), "quit", "\n"),
		con("console-9-restart", st(ui.Finish, 6, online, done), full(), "restart", "\n"),
		con("console-9-shutdown", st(ui.Network, 13, net(online), done), full(), "shutdown", "\n"),
		con("console-9-exit-sent", st(ui.Finish, 20, online, done, func(s *ui.State) { s.SentAt = 11.4 }), sent, "exit", "\n"),
		testCon("console-9-restart-hands-on", playShot("", "ports.usb-a", 6, nil), "restart", "\n"),
		con("console-9-help", st(ui.Results, 10, online, done), full(), "^up", "?", "\n"),
		con("console-13-testfile", st(ui.Results, 10, online, done), full(), "^up", "testfile", "\n", "testfile write sdc1", "\n", "y", "\n"),
		// 0.7.3: PgUp stops at the oldest full page; smc reads a key raw; Boot dozes over the console
		con("console-10-pgup-oldest", st(ui.Network, 13, net(online), done), full(), "why net", "\t", "\n", "ids", "\n", "pgup", "pgup", "pgup", "pgup", "pgup", "pgup"),
		con("console-10-smc", st(ui.Results, 10, online, done), full(), "smc ALV0", "\n", "smc ALV1", "\n", "smc ZZZZ", "\n"),
		con("console-10-doze", st(ui.Results, 10, online, done, func(s *ui.State) { s.Doze, s.DozeT = true, 30 }), full(), "ids", "\n"),
		// sounds: the sounds screen over the screen on now; off, then on with a moment picked
		con("console-14-sounds-off", st(ui.Results, 10, online, done), full(), "sounds", "\n"),
		soundsOn(con("console-14-sounds-on", st(ui.Results, 10, online, done), full(), "sounds", "\n")),
		soundsOn(shot{name: "console-14-sound-on-results", st: st(ui.Results, 10, online, done), f: full()}),
	}
}

// said shows Boot's line from the console typed out (it types from when it was said).
// sleepCheck prints the demo's sleepcheck all into the console, as its messages would.
func sleepCheck(s shot) shot { return sleepCheckOf("all", s) }

// sleepCheckOf prints the demo's sleepcheck what into the console.
func sleepCheckOf(what string, s shot) shot {
	for _, m := range demo.SleepCheckSteps(what) {
		s.st.Con.Print(m.Lines...)
	}
	return s
}

func said(s shot) shot {
	s.st.Said.At, s.st.Said.Wait = time.Time{}, false
	return s
}

// conOver is every demo and hands-on shot again with the console open, short and tall (its help
// list makes it tall), named con-short-… and con-tall-…: a frame shrunk below the screens' layout
// is cut at its bottom edge, and nothing may land on another line's row.
func conOver(base []shot) []shot {
	opt := ui.Options{Edition: ui.Live, Mode: grid.VT, Palette: grid.OmaBoot, Version: "0.7.7 dev", Font: "omaboot-v", FontPx: 16}
	var out []shot
	for _, b := range base {
		f := b.f
		if f == nil {
			f = demo.Finished()
		}
		for _, tall := range []bool{false, true} {
			s := b
			s.st.Con = nil // a console of its own
			steps, name := []string{"ids", "\n"}, "con-short-"+b.name
			if tall {
				steps, name = []string{"^up", "?", "\n"}, "con-tall-"+b.name
			}
			s.name, s.st = name, ui.ScriptConsole(opt, s.st, f, demo.NewBackend(), steps...)
			out = append(out, s)
		}
	}
	return out
}

// testCon opens the console over a hands-on test's shot and types into it.
func testCon(name string, s shot, steps ...string) shot {
	opt := ui.Options{Edition: ui.Live, Mode: grid.VT, Palette: grid.OmaBoot, Version: "0.7.7 dev", Font: "omaboot-v", FontPx: 16}
	s.st.PassDone = true
	s.name, s.st = name, ui.ScriptConsole(opt, s.st, s.f, demo.NewBackend(), steps...)
	return s
}

// bugFacts are a finished run's facts in an image that can send bug reports.
func bugFacts() *ui.Facts {
	f := demo.Finished()
	f.CanSendBug = true
	return f
}
