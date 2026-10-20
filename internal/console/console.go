// Package console prepares the screen the tool draws on. On the Linux virtual console it sets
// our 16-colour palette (\e]P), loads the omaboot-v font sized from the screen's physical size
// (EDID), and puts both back on exit; in a terminal emulator (--term, or when no VT is found) it
// only sets the default colours, and the screens use our palette's RGB values directly.
//
// Raw mode, the hidden cursor and line-diffed redraws come from Bubble Tea: its renderer
// rewrites only the rows that changed since the last frame, never the whole screen.
package console

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"

	"github.com/omaboot/boot-live/internal/grid"
)

// Options configure Setup.
type Options struct {
	ForceTerm bool         // treat the screen as a terminal emulator even on a VT
	Palette   grid.Palette // the colours to use
	// TallKeys loads the keymap lines that give Ctrl+↑ and Ctrl+↓ their own sequences (the real
	// run on the console only; TallKeymap).
	TallKeys bool
}

// TallKeymap makes Ctrl+↑ and Ctrl+↓ send xterm's sequences, which Bubble Tea reads as ctrl+up and
// ctrl+down: the console's tall and short (LIVE-PLAN §24; owner, 2026-10-14). The stock keymap
// sends plain arrows for them. Only these two bindings change: Up is keycode 103, Down 108, and
// F100/F101 are function keys no keymap uses.
const TallKeymap = `string F100 = "\033[1;5A"
string F101 = "\033[1;5B"
control keycode 103 = F100
control keycode 108 = F101
`

// Font is the console font in use.
type Font struct {
	Name   string // "omaboot-v", or "" when we kept the console's own font
	Px     int    // cell height
	Path   string
	Reason string // how the size was chosen
}

// Console is the prepared screen. Restore puts everything back; it is safe to call more than once.
type Console struct {
	Mode grid.Mode
	Font Font

	out       *os.File
	savedCmap []byte
	origFont  string // the console font we started with, saved by setfont -O
	keymap    string // the keymap we started with, saved by dumpkeys, when TallKeys changed it
	printk    string // the kernel's console log level we started with, when we lowered it
	tmpDir    string
	once      sync.Once
}

// IsVT reports whether stdout is a Linux virtual console.
func IsVT() bool {
	return os.Getenv("TERM") == "linux" && isConsole(int(os.Stdout.Fd()))
}

// Setup prepares the screen. Errors in the optional parts (font, palette save) are not fatal:
// the tool runs with whatever the console has.
func Setup(o Options) (*Console, error) {
	c := &Console{Mode: grid.TrueColor, out: os.Stdout}
	if o.ForceTerm || !IsVT() {
		// default foreground and background of the terminal emulator, restored by OSC 110/111
		fmt.Fprintf(c.out, "\x1b]10;#%s\x1b\\\x1b]11;#%s\x1b\\", o.Palette.Hex(int(grid.GRY)), o.Palette.Hex(int(grid.K)))
		return c, nil
	}
	c.Mode = grid.VT
	fd := int(c.out.Fd())
	// kernel messages printed to the console would land on top of the screen (a boot without
	// "quiet", a USB device plugged in): only emergencies get through while the tool runs
	if f := strings.Fields(readFile("/proc/sys/kernel/printk")); len(f) > 0 && f[0] != "1" {
		if os.WriteFile("/proc/sys/kernel/printk", []byte("1"), 0) == nil {
			c.printk = f[0]
		}
	}
	if m, err := getCmap(fd); err == nil {
		c.savedCmap = m
	}
	if o.TallKeys {
		c.tallKeys()
	}
	c.Font = pickFont(DetectScreen())
	if c.Font.Path != "" {
		if dir := c.temp(); dir != "" {
			c.origFont = filepath.Join(dir, "console-font.psf")
		}
		args := []string{c.Font.Path}
		if c.origFont != "" {
			args = []string{"-O", c.origFont, c.Font.Path} // save the current font, then load ours, in one go
		}
		if out, err := exec.Command("setfont", args...).CombinedOutput(); err != nil {
			c.Font = Font{Reason: fmt.Sprintf("setfont: %v %s", err, strings.TrimSpace(string(out)))}
			c.origFont = ""
		}
	}
	var b strings.Builder
	for i := 0; i < 16; i++ {
		fmt.Fprintf(&b, "\x1b]P%X%s", i, o.Palette.Hex(i))
	}
	b.WriteString("\x1b[0m\x1b[2J\x1b[H\x1b[?25l")
	c.out.WriteString(b.String())
	return c, nil
}

// Blank turns the console's screen off (on) or back on, for the screensaver: the kernel's own
// blanking (TIOCLINUX, TIOCL_BLANKSCREEN), which a key press doesn't undo, so the tool decides
// when to wake. It does nothing in a terminal emulator and reports false.
func (c *Console) Blank(on bool) bool {
	if c.Mode != grid.VT {
		return false
	}
	return blank(int(c.out.Fd()), on) == nil
}

// temp is our directory for what Restore puts back, made the first time it's needed ("" if it can't be).
func (c *Console) temp() string {
	if c.tmpDir == "" {
		if dir, err := os.MkdirTemp("", "boot-live-"); err == nil {
			c.tmpDir = dir
		}
	}
	return c.tmpDir
}

// tallKeys saves the keymap (dumpkeys) and loads TallKeymap over it (loadkeys, both from kbd, like
// setfont). Without a saved copy the two bindings stay after the tool ends; they only make
// Ctrl+↑/↓ send their own sequences.
func (c *Console) tallKeys() {
	if dir := c.temp(); dir != "" {
		if km, err := exec.Command("dumpkeys", "--full-table").Output(); err == nil && len(km) > 0 {
			p := filepath.Join(dir, "keymap")
			if os.WriteFile(p, km, 0o600) == nil {
				c.keymap = p
			}
		}
	}
	dir := c.temp()
	if dir == "" {
		return
	}
	p := filepath.Join(dir, "tall.map")
	err := os.WriteFile(p, []byte(TallKeymap), 0o600)
	if err == nil {
		err = exec.Command("loadkeys", p).Run()
	}
	os.Remove(p)
	if err != nil && c.keymap != "" {
		os.Remove(c.keymap) // nothing changed: nothing to put back
		c.keymap = ""
	}
}

// SetFont loads omaboot-v at another cell height (16, 24 or 32): the console's font command
// (dev). The console resizes; Bubble Tea hears of it (SIGWINCH). Restore still puts the font we
// started with back.
func (c *Console) SetFont(px int) error {
	if c.Mode != grid.VT {
		return fmt.Errorf("not on the Linux console")
	}
	p := FindFont(px)
	if p == "" {
		return fmt.Errorf("omaboot-v %d not found", px)
	}
	if out, err := exec.Command("setfont", p).CombinedOutput(); err != nil {
		return fmt.Errorf("setfont: %v %s", err, strings.TrimSpace(string(out)))
	}
	c.Font = Font{Name: "omaboot-v", Px: px, Path: p, Reason: "picked in the console"}
	return nil
}

// Restore puts the palette, the font and the cursor back.
func (c *Console) Restore() {
	c.once.Do(func() {
		if c.Mode != grid.VT {
			c.out.WriteString("\x1b]110\x1b\\\x1b]111\x1b\\\x1b[0m\x1b[?25h")
			return
		}
		blank(int(c.out.Fd()), false) // never leave the screen blanked
		if c.printk != "" {
			os.WriteFile("/proc/sys/kernel/printk", []byte(c.printk), 0)
		}
		fd := int(c.out.Fd())
		if c.savedCmap == nil || setCmap(fd, c.savedCmap) != nil {
			c.out.WriteString("\x1b]R") // the kernel's default palette
		}
		if c.origFont != "" {
			exec.Command("setfont", c.origFont).Run()
			os.Remove(c.origFont)
		}
		if c.keymap != "" {
			exec.Command("loadkeys", c.keymap).Run()
			os.Remove(c.keymap)
		}
		if c.tmpDir != "" {
			os.Remove(c.tmpDir) // empty by now; never a recursive delete
		}
		c.out.WriteString("\x1b[0m\x1b[2J\x1b[H\x1b[?25h")
	})
}
