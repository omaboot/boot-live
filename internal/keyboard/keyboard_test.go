package keyboard

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/omaboot/boot-live/internal/sysroot"
)

// The NVRAM value as efivarfs gives it (attributes first, maybe NUL-terminated), and the layout
// each one starts the run with.
func TestDefault(t *testing.T) {
	attrs := []byte{0x07, 0, 0, 0} // non-volatile, boot service and runtime access
	cases := []struct {
		raw        string // the value after the attributes; "-" for no variable
		keymap     string
		how, nvram string
		byLang     bool
	}{
		{"-", "us", FromDefault, "", false},
		{"en:0", "us", FromMacOS, "en:0", false},
		{"en-GB:2\x00", "uk", FromMacOS, "en-GB:2", false},
		{"de-DE:3", "de", FromMacOS, "de-DE:3", false},
		{"fr:1", "fr", FromMacOS, "fr:1", false},
		{"de_CH:19", "de_CH-latin1", FromMacOS, "de_CH:19", false},
		{"en-GB:0", "us", FromMacOS, "en-GB:0", false},            // the ID wins over the language
		{"cs:-11013", "sk-qwertz", FromMacOS, "cs:-11013", false}, // a negative ID
		{"en-GB:99999", "uk", FromMacOS, "en-GB:99999", true},     // an unknown ID: the region decides
		{"fr-CA", "cf", FromMacOS, "fr-CA", true},                 // no ID at all
		{"uk:12345", "ua", FromMacOS, "uk:12345", true},           // uk is Ukrainian
		{"es-MX:4242", "la-latin1", FromMacOS, "es-MX:4242", true},
		{"bg:19528", "us", FromDefault, "bg:19528", false}, // a layout we don't map, a language we don't either
		{"", "us", FromDefault, "", false},
		{"\xff\xfe", "us", FromDefault, "", false}, // not ASCII
	}
	for _, c := range cases {
		dir := t.TempDir()
		if c.raw != "-" {
			p := filepath.Join(dir, EFIVar)
			if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(p, append(append([]byte(nil), attrs...), c.raw...), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		got := Default(sysroot.Root(dir))
		if got.Layout.Keymap != c.keymap || got.How != c.how || got.NVRAM != c.nvram || got.ByLanguage != c.byLang {
			t.Errorf("%q: got %s %s %q byLang=%v, want %s %s %q byLang=%v", c.raw,
				got.Layout.Keymap, got.How, got.NVRAM, got.ByLanguage, c.keymap, c.how, c.nvram, c.byLang)
		}
	}
}

// Every keymap the NVRAM and language tables name is one of the layouts offered.
func TestTablesOffered(t *testing.T) {
	for id, k := range byID {
		if _, ok := ByKeymap(k); !ok {
			t.Errorf("layout ID %d: keymap %s isn't offered", id, k)
		}
	}
	for _, m := range []map[string]string{byRegion, byLang} {
		for tag, k := range m {
			if _, ok := ByKeymap(k); !ok {
				t.Errorf("language %s: keymap %s isn't offered", tag, k)
			}
		}
	}
}
