package icons

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/omaboot/boot-live/internal/glyphs"
)

// TestEveryIconIsInOmabootV fails on any rune in this package that omaboot-v lacks at 16, 24 or
// 32 px, so a missing icon can never ship as a box. It reads every rune and string literal in
// icons.go, checks each icon is two cells, and that each port kind's symbol and icon resolve.
func TestEveryIconIsInOmabootV(t *testing.T) {
	f, err := parser.ParseFile(token.NewFileSet(), "icons.go", nil, parser.SkipObjectResolution)
	if err != nil {
		t.Fatal(err)
	}
	var runes []rune
	ast.Inspect(f, func(n ast.Node) bool {
		lit, ok := n.(*ast.BasicLit)
		switch {
		case ok && lit.Kind == token.CHAR:
			r, _, _, err := strconv.UnquoteChar(strings.Trim(lit.Value, "'"), '\'')
			if err != nil {
				t.Fatal(err)
			}
			runes = append(runes, r)
		case ok && lit.Kind == token.STRING && strings.HasPrefix(lit.Value, `"\u`):
			s, err := strconv.Unquote(lit.Value)
			if err != nil {
				t.Fatal(err)
			}
			if rs := []rune(s); len(rs) != 2 || rs[1] != rs[0]+1 {
				t.Errorf("icon %s isn't a left and a right half", lit.Value)
			}
			runes = append(runes, []rune(s)...)
		}
		return true
	})
	if len(runes) < 16+2*32 {
		t.Fatalf("found only %d runes in icons.go: is the scan broken?", len(runes))
	}
	for kind := range ports {
		sym, ok := Port(kind)
		if !ok {
			t.Errorf("Port(%q) has no symbol", kind)
		}
		runes = append(runes, sym[0], sym[1])
	}
	for kind := range portIcons {
		r, ok := PortIcon(kind)
		if !ok {
			t.Errorf("PortIcon(%q) has no icon", kind)
		}
		runes = append(runes, []rune(r)...)
	}
	if _, ok := Port("vga"); ok {
		t.Error("Port(\"vga\") should have no symbol")
	}
	for _, px := range []int{16, 24, 32} {
		name := "omaboot-v" + strconv.Itoa(px) + "n.psf.gz"
		font, err := glyphs.Load(filepath.Join("..", "..", "assets", "fonts", name))
		if err != nil {
			t.Fatal(err)
		}
		for _, r := range runes {
			if !font.Has(r) {
				t.Errorf("%s has no U+%04X", name, r)
			}
		}
	}
}
