// boot-live is OmaBoot? Live's tester: it runs on the Linux console of an Intel Mac booted from
// an OmaBoot? image, tests the Mac without touching its disks and sends the results to
// DoesItOmarchy.com. --demo plays the flow with a scripted run on a MacBook Air (13-inch,
// Mid 2012) [MacBookAir5,2]; without it the real run starts, and only on the OmaBoot? live
// system. --sysroot points the real run's probes at a fixture tree instead (development: it
// then writes nothing to the machine).
package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/omaboot/boot-live/internal/app"
	"github.com/omaboot/boot-live/internal/buildinfo"
	"github.com/omaboot/boot-live/internal/console"
	"github.com/omaboot/boot-live/internal/demo"
	"github.com/omaboot/boot-live/internal/grid"
	"github.com/omaboot/boot-live/internal/handson"
	"github.com/omaboot/boot-live/internal/i18n"
	"github.com/omaboot/boot-live/internal/safety"
	"github.com/omaboot/boot-live/internal/send"
	"github.com/omaboot/boot-live/internal/sysroot"
	"github.com/omaboot/boot-live/internal/ui"
)

// Exit statuses.
const (
	exitOK      = 0
	exitFailed  = 1
	exitUsage   = 2
	exitRefused = 3  // not the OmaBoot? live system, or the disks couldn't be made read-only
	exitInstall = 10 // OmaBoot? Omarchy: the person chose "Install Omarchy now"
)

func main() {
	os.Exit(run())
}

func run() int {
	demoMode := flag.Bool("demo", false, "play the screens with a scripted run")
	auto := flag.Bool("auto", false, "run without any input (QEMU tests): quick pass, then submit or save to external storage")
	plain := flag.Bool("plain", false, "Boot layer off: no Boot, no band effects, no Boot lines")
	edition := flag.String("edition", "live", "the image this runs in: live or omarchy")
	termMode := flag.Bool("term", false, "development in a terminal emulator: no console font or palette setup")
	root := flag.String("sysroot", "/", "read /sys, /proc, /dev and /etc under this directory (a fixture tree; nothing is written then)")
	target := flag.String("target", "", "where reports go (overrides target.conf), e.g. http://localhost:8080")
	key := flag.String("key", "", "the source key for --target (overrides target.conf)")
	watch := flag.String("watch", "", "the bootwatchd the network checks test through (overrides target.conf; default "+send.DefaultWatch+"), e.g. http://localhost:8470")
	bugs := flag.String("bugs", "", "the bootwatchd bug reports go to (overrides target.conf's bugs=), e.g. http://localhost:8470")
	bugKey := flag.String("bugkey", "", "the bug report upload key for --bugs (overrides target.conf's bugkey=)")
	stick := flag.String("stick", "", "save reports under this directory instead of on external storage")
	helper := flag.String("helper", "/usr/lib/omaboot/egl-probe", "the GPU check's helper")
	load := flag.Duration("load", 0, "how long the thermal check loads the CPU (default 20s; 2s with --sysroot)")
	version := flag.Bool("version", false, "print the version and exit")
	flag.Parse()

	if *version {
		fmt.Println("boot-live " + buildinfo.String())
		return exitOK
	}
	var ed ui.Edition
	switch *edition {
	case "live":
		ed = ui.Live
	case "omarchy":
		ed = ui.Omarchy
	default:
		fmt.Fprintln(os.Stderr, i18n.T("boot-live: --edition must be live or omarchy"))
		return exitUsage
	}

	var r *app.Run
	if !*demoMode {
		sr := sysroot.Root(*root)
		// The real run makes the internal disks read-only: it never starts on a normal computer.
		if sr.IsSystem() {
			if err := safety.Guard(sysroot.System); err != nil {
				fmt.Fprintln(os.Stderr, "boot-live:", err)
				if *auto {
					fmt.Println(app.AutoMarker + " failed not the OmaBoot? live system")
				}
				return exitRefused
			}
		}
		t := send.LoadTarget(sr)
		if *target != "" {
			t.URL = *target
		}
		if *key != "" {
			t.Key = *key
		}
		if *watch != "" {
			t.Watch = strings.TrimRight(*watch, "/")
		}
		if *bugs != "" {
			t.Bugs = strings.TrimRight(*bugs, "/")
		}
		if *bugKey != "" {
			t.BugKey = *bugKey
		}
		opt := app.Options{Root: sr, Edition: ed, Target: t, Helper: *helper, StickDir: *stick, Load: *load,
			Settle: 12 * time.Second, Version: buildinfo.String(), Omarchy: buildinfo.Omarchy}
		if opt.Load == 0 {
			opt.Load = 20 * time.Second
			if !sr.IsSystem() {
				opt.Load = 2 * time.Second
			}
		}
		if !sr.IsSystem() {
			opt.Settle = 0
		}
		r = app.New(opt)
		defer r.Close()
		if *auto {
			return app.Auto(r, os.Stdout)
		}
		r.Setup()
	}

	// Ctrl+↑/↓ (the console's tall and short) need their own keymap lines on the console: the real
	// run only, never the demo or a fixture run; the keyboard layout too, loaded first
	tall := !*demoMode && sysroot.Root(*root).IsSystem()
	co := console.Options{ForceTerm: *termMode, Palette: grid.OmaBoot, TallKeys: tall}
	if tall {
		co.Keymap = r.StartKeymap()
	}
	con, err := console.Setup(co)
	if err != nil {
		fmt.Fprintln(os.Stderr, "boot-live:", err)
		return exitFailed
	}
	if r != nil {
		if tall && con.Mode == grid.VT {
			r.KeyboardLoaded(con.LoadKeymap, con.KeymapErr)
		} else {
			r.KeyboardLoaded(nil, nil) // a fixture run or a terminal emulator: nothing loaded
		}
	}
	defer con.Restore()
	defer func() { // a panic still gives the console back before it is reported
		if r := recover(); r != nil {
			con.Restore()
			panic(r)
		}
	}()

	o := ui.Options{Edition: ed, Plain: *plain, Mode: con.Mode, Palette: grid.OmaBoot,
		Version: buildinfo.String(), Font: con.Font.Name, FontPx: con.Font.Px, Term: con.Mode != grid.VT}
	if con.Mode == grid.VT {
		o.Blank, o.SetFont = con.Blank, con.SetFont
	}
	var m *ui.Model
	var be ui.Backend
	if *demoMode {
		d := demo.NewBackend()
		d.Audible = true
		be = d
		m = ui.NewModel(o, demo.Facts(), be)
	} else {
		be = app.NewBackend(r)
		m = ui.NewModel(o, r.Facts(&o), be)
	}
	p := tea.NewProgram(m, tea.WithAltScreen(), tea.WithFPS(ui.FPS))
	be.SetSend(p.Send)

	// Bubble Tea handles SIGINT and SIGTERM; a hangup (the console going away) ends the run too.
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGHUP, syscall.SIGQUIT)
	go func() {
		if _, ok := <-sig; ok {
			p.Kill()
		}
	}()
	_, err = p.Run()
	signal.Stop(sig)
	close(sig)
	con.Restore()
	if err != nil && !errors.Is(err, tea.ErrProgramKilled) {
		fmt.Fprintln(os.Stderr, "boot-live:", err)
		return exitFailed
	}
	if m.Exit != "" {
		fmt.Println(m.Exit)
	}
	return ending(m.End, r)
}

// ending does what the person chose on the last screen: shut down, restart, or hand over to the
// Omarchy installer (by exit status, for the image's script). The demo and a fixture run only
// say it and quit.
func ending(e ui.Ending, r *app.Run) int {
	if r == nil { // the demo
		switch e {
		case ui.EndInstall:
			fmt.Println(i18n.T("demo: the Omarchy installer would start now."))
		case ui.EndPoweroff:
			fmt.Println(i18n.T("demo: the Mac would shut down now."))
		case ui.EndReboot:
			fmt.Println(i18n.T("demo: the Mac would restart now."))
		}
		return exitOK
	}
	if e == ui.EndQuit || e == ui.EndPoweroff || e == ui.EndReboot { // a disc left in the drive goes back out (0.7.3 review)
		if said := handson.EjectAtExit(r.Opt.Root, 20*time.Second); said != "" {
			fmt.Println("boot-live: " + said)
		}
	}
	switch e {
	case ui.EndRefused:
		return exitRefused
	case ui.EndInstall:
		return exitInstall
	case ui.EndPoweroff, ui.EndReboot:
		if r.Fixture() {
			if e == ui.EndReboot {
				fmt.Println(i18n.T("fixture run: the Mac would restart now."))
			} else {
				fmt.Println(i18n.T("fixture run: the Mac would shut down now."))
			}
			return exitOK
		}
		r.Close()
		if r.Gone() {
			// systemctl lives on the stick that came out: ask the kernel directly. Nothing to
			// unmount first (the internal disks are read-only, a saved copy is unmounted after
			// writing); sync flushes anything still waiting.
			syscall.Sync()
			cmd := syscall.LINUX_REBOOT_CMD_POWER_OFF
			if e == ui.EndReboot {
				cmd = syscall.LINUX_REBOOT_CMD_RESTART
			}
			if err := syscall.Reboot(cmd); err != nil {
				fmt.Fprintln(os.Stderr, "boot-live:", err)
				return exitFailed
			}
			return exitOK
		}
		if err := exec.Command("systemctl", string(e)).Run(); err != nil {
			fmt.Fprintln(os.Stderr, "boot-live:", err)
			return exitFailed
		}
	}
	return exitOK
}
