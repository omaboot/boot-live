package ui

import (
	g "github.com/omaboot/boot-live/internal/grid"
	"github.com/omaboot/boot-live/internal/i18n"
	"github.com/omaboot/boot-live/internal/keyboard"
)

// Hello's keyboard layout: a setup line says which layout the console types with and where it
// came from (macOS's last one, the default, or picked), and Tab opens the picker over the setup
// box: the main layouts and "Other layouts…", ↑↓ (←→ between columns) and Enter, which loads the
// layout at once; Esc keeps the one in use. Tab, because it is the same key on every layout:
// until the right layout is in, a letter key may not be where its legend says.

// kbdKey is the key that opens the picker, kbdCap its cap on Hello (as the hint bar draws keys).
const (
	kbdKey = "tab"
	kbdCap = " Tab "
)

// kbdHow is where the layout in use came from, as Hello says it.
func kbdHow(c keyboard.Choice) string {
	switch c.How {
	case keyboard.FromMacOS:
		return i18n.T("from macOS")
	case keyboard.FromTester:
		return i18n.T("picked")
	}
	return i18n.T("the default")
}

// kbdLine is Hello's setup line about the layout, and whether it tells of a failure (amber).
func kbdLine(f *Facts) (string, bool) {
	c := f.Keyboard
	s := i18n.T("keyboard: %s, %s", i18n.S(c.Layout.Name), kbdHow(c))
	switch {
	case c.Tried.Keymap != "":
		return s + " · " + i18n.T("%s didn't load: kept %s", i18n.S(c.Tried.Name), i18n.S(c.Layout.Name)), true
	case !c.Loaded && !f.Demo:
		s += " " + i18n.T("(not loaded: not the Linux console)")
	}
	return s, false
}

// kbdOther is the picker's "Other layouts…" entry, at the end of the main list.
var kbdOther = keyboard.Layout{Name: "…"}

// kbdItems are the picker's entries: the main layouts and "Other layouts…", or the others.
func kbdItems(st *State) []keyboard.Layout {
	if st.KbdOther {
		return keyboard.Other()
	}
	return append(keyboard.Main(), kbdOther)
}

// kbdIndex is the layout's place in the list, or -1.
func kbdIndex(ls []keyboard.Layout, keymap string) int {
	for i, l := range ls {
		if l.Keymap == keymap {
			return i
		}
	}
	return -1
}

// openKbd opens the picker on the layout in use (the main list, or the others when it's one of
// them).
func (m *Model) openKbd() {
	st := &m.st
	st.Kbd, st.KbdOther = true, !m.f.Keyboard.Layout.Main && m.f.Keyboard.Layout.Keymap != ""
	st.KbdSel = max(0, kbdIndex(kbdItems(st), m.f.Keyboard.Layout.Keymap))
}

// kbdPickKey takes the keys while the picker is open.
func (m *Model) kbdPickKey(key string) {
	st := &m.st
	ls := kbdItems(st)
	rows := max(1, st.KbdRows)
	switch key {
	case "up", "k":
		st.KbdSel = max(0, st.KbdSel-1)
	case "down", "j":
		st.KbdSel = min(len(ls)-1, st.KbdSel+1)
	case "left":
		if st.KbdSel-rows >= 0 {
			st.KbdSel -= rows
		}
	case "right":
		if st.KbdSel+rows < len(ls) {
			st.KbdSel += rows
		}
	case "backspace":
		if st.KbdOther { // back to the main list, on "Other layouts…"
			st.KbdOther, st.KbdSel = false, len(keyboard.Main())
			return
		}
		st.Kbd = false
	case "esc", kbdKey:
		st.Kbd = false
	case "enter":
		l := ls[min(st.KbdSel, len(ls)-1)]
		if l.Keymap == "" { // Other layouts…
			st.KbdOther = true
			st.KbdSel = max(0, kbdIndex(keyboard.Other(), m.f.Keyboard.Layout.Keymap))
			return
		}
		m.f.Keyboard = m.be.Keyboard(l)
		st.Kbd = false
	}
}

// drawKbd draws the picker at x, y, at most w wide and h tall: a box of the layouts in columns
// as tall as the room allows, the one in use marked •, and what Enter and Esc do under it.
func drawKbd(gr *g.Grid, st *State, f *Facts, x, y, w, h int) {
	ls := kbdItems(st)
	nameW, mapW := 0, 0
	for _, l := range ls {
		nameW, mapW = max(nameW, width(kbdName(l))), max(mapW, width(l.Keymap))
	}
	colW := 2 + nameW + 2 + mapW + 2 + 3 // ▶, name, keymap, •, the gap to the next column
	rows := max(1, min(len(ls), h-3))    // the box's edges and the line under it
	cols := (len(ls) + rows - 1) / rows
	rows = (len(ls) + cols - 1) / cols // as even as the columns can be
	st.KbdRows = rows
	bw := min(w, max(cols*colW+3, 48))
	title := i18n.T("Keyboard layout")
	if st.KbdOther {
		title = i18n.T("Other keyboard layouts")
	}
	gr.Fill(x, y, w, min(h, rows+3), ' ', g.GRY, g.K)
	box(gr, x, y, bw, rows+2, g.DIM, title, g.GRY)
	in := f.Keyboard.Layout.Keymap
	for i, l := range ls {
		cx, cy := x+2+(i/rows)*colW, y+1+i%rows
		if cx+colW-3 > x+bw-1 {
			break // no room (never at 120 columns)
		}
		c := g.GRY
		if i == st.KbdSel {
			gr.Put(cx, cy, "▶", g.BVIO)
			c = g.WHT
		}
		gr.Put(cx+2, cy, kbdName(l), c)
		gr.Put(cx+2+nameW+2, cy, l.Keymap, g.DIM)
		if l.Keymap == in && in != "" {
			gr.Put(cx+2+nameW+2+mapW+1, cy, "•", g.BVIO)
		}
	}
	hint := i18n.T("Enter loads it now · Esc keeps %s", i18n.S(f.Keyboard.Layout.Name))
	if st.KbdOther {
		hint = i18n.T("Enter loads it now · delete: the main list · Esc keeps %s", i18n.S(f.Keyboard.Layout.Name))
	}
	gr.Put(x+2, y+rows+2, cut(hint, w-2), g.DIM)
}

// kbdName is an entry's name on screen.
func kbdName(l keyboard.Layout) string {
	if l.Keymap == "" {
		return i18n.T("Other layouts…")
	}
	return i18n.S(l.Name)
}
