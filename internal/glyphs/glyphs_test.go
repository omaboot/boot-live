package glyphs

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/omaboot/boot-live/internal/i18n/potgen"
)

const root = "../.."

// printable collects every character the UI can print:
//   - every string and rune literal in the tool's Go source (box drawing, marks, band ramps,
//     sprite cells, demo data, key hints), escape sequences' control bytes aside;
//   - every message in the catalog as extracted from the source, and every translation in
//     locales/ (all languages).
//
// Numbers are formatted from ASCII digits, which every font has.
func printable(t *testing.T) map[rune][]string {
	t.Helper()
	found := map[rune][]string{}
	add := func(s, where string) {
		for _, r := range s {
			if r < 0x20 || r == 0x7f {
				continue
			}
			if len(found[r]) < 3 {
				found[r] = append(found[r], where)
			}
		}
	}
	fset := token.NewFileSet()
	for _, dir := range []string{"cmd", "internal"} {
		err := filepath.WalkDir(filepath.Join(root, dir), func(p string, e fs.DirEntry, err error) error {
			if err != nil || e.IsDir() || !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
				return err
			}
			f, err := parser.ParseFile(fset, p, nil, parser.SkipObjectResolution)
			if err != nil {
				return err
			}
			ast.Inspect(f, func(n ast.Node) bool {
				lit, ok := n.(*ast.BasicLit)
				if !ok || (lit.Kind != token.STRING && lit.Kind != token.CHAR) {
					return true
				}
				pos := fset.Position(lit.Pos())
				where := filepath.Base(pos.Filename) + ":" + strconv.Itoa(pos.Line)
				if lit.Kind == token.CHAR {
					if r, _, _, err := strconv.UnquoteChar(strings.Trim(lit.Value, "'"), '\''); err == nil {
						add(string(r), where)
					}
					return true
				}
				if s, err := strconv.Unquote(lit.Value); err == nil {
					add(s, where)
				}
				return true
			})
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	msgs, err := potgen.Extract(root, "cmd", "internal")
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range msgs {
		add(m.ID+m.Plural, "catalog "+m.Refs[0])
	}
	pos, _ := filepath.Glob(filepath.Join(root, "locales", "*", "LC_MESSAGES", "*.po"))
	for _, p := range pos {
		strs, err := potgen.Msgstrs(p)
		if err != nil {
			t.Fatal(err)
		}
		for _, s := range strs {
			add(s, p)
		}
	}
	return found
}

// TestEveryGlyphIsInOmabootV fails on any character the UI can print that omaboot-v lacks at
// 16, 24 or 32 px: the console would show a box or a question mark instead.
func TestEveryGlyphIsInOmabootV(t *testing.T) {
	chars := printable(t)
	if len(chars) < 100 {
		t.Fatalf("found only %d characters: is the source scan broken?", len(chars))
	}
	for _, px := range []int{16, 24, 32} {
		name := "omaboot-v" + strconv.Itoa(px) + "n.psf.gz"
		f, err := Load(filepath.Join(root, "assets", "fonts", name))
		if err != nil {
			t.Fatal(err)
		}
		var missing []rune
		for r := range chars {
			if !f.Has(r) {
				missing = append(missing, r)
			}
		}
		sort.Slice(missing, func(i, j int) bool { return missing[i] < missing[j] })
		for _, r := range missing {
			t.Errorf("%s has no %q (U+%04X), used in %s", name, r, r, strings.Join(chars[r], ", "))
		}
	}
}
