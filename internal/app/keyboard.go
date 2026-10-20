package app

import (
	"fmt"

	"github.com/doesitomarchy/doesitomarchy/pkg/report"

	"github.com/omaboot/boot-live/internal/keyboard"
)

// The keyboard layout: macOS's last one from NVRAM (or English (US)) at Setup, loaded on the
// console by main before anything is typed; Hello's picker changes it. The report keeps it as
// the extra input.keyboard-layout: not a criterion, the context for what was typed (a Wi-Fi
// password, the console's commands).

// loadKeyboard reads the layout to start with (Setup).
func (r *Run) loadKeyboard() {
	r.mu.Lock()
	r.kbd = keyboard.Default(r.Opt.Root)
	r.mu.Unlock()
}

// StartKeymap is the keymap main loads with the console, before the screens: the layout
// Setup chose.
func (r *Run) StartKeymap() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.kbd.Layout.Keymap
}

// KeyboardLoaded is how the start layout went (main, once the console is set up): load is what
// loads a layout picked on Hello, nil off the Linux console (a fixture run, a terminal
// emulator), where nothing is loaded; err why the start layout didn't load (the console's own,
// US, stays).
func (r *Run) KeyboardLoaded(load func(keymap string) error, err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.loadKeymap = load
	r.kbd.Loaded = load != nil && err == nil
	if load != nil && err != nil {
		l := r.kbd.Layout
		r.kbd.Layout, r.kbd.How, r.kbd.ByLanguage, r.kbd.Loaded = keyboard.US, keyboard.FromDefault, false, true
		r.kbd.Tried, r.kbd.Failed = l, fmt.Sprintf("%s (keymap %s) didn't load: %v", l.Name, l.Keymap, err)
	}
}

// Keyboard is the layout as it stands.
func (r *Run) Keyboard() keyboard.Choice {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.kbd
}

// Keyboard loads the layout picked on Hello; one that doesn't load leaves the one in use, and
// says why.
func (b *Backend) Keyboard(l keyboard.Layout) keyboard.Choice {
	r := b.R
	r.mu.Lock()
	load := r.loadKeymap
	r.mu.Unlock()
	var err error
	if load != nil {
		err = load(l.Keymap)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if err != nil {
		r.kbd.Tried, r.kbd.Failed = l, fmt.Sprintf("%s (keymap %s) didn't load: %v", l.Name, l.Keymap, err)
		return r.kbd
	}
	r.kbd.Layout, r.kbd.How, r.kbd.ByLanguage, r.kbd.Tried, r.kbd.Failed = l, keyboard.FromTester, false, keyboard.Layout{}, ""
	r.kbd.Loaded = load != nil
	return r.kbd
}

// keyboardExtra is the report's input.keyboard-layout extra.
func (r *Run) keyboardExtra() report.FileExtra {
	return report.FileExtra{ID: "input.keyboard-layout", Label: "Keyboard layout", Detail: r.Keyboard().Detail()}
}
