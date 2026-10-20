// Package glyphs reads our console fonts (PSF2, optionally gzipped) and their Unicode tables, so
// tests can check that every character the UI prints exists in omaboot-v, and dev tools can draw
// screens with the real font.
package glyphs

import (
	"bytes"
	"compress/gzip"
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"strings"
	"unicode/utf8"
)

// Font is a loaded console font.
type Font struct {
	W, H   int
	BPR    int      // bytes per glyph row
	Glyphs [][]byte // H*BPR bytes each
	Map    map[rune]int
}

// Load reads a .psf or .psf.gz file.
func Load(path string) (*Font, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if strings.HasSuffix(path, ".gz") {
		zr, err := gzip.NewReader(bytes.NewReader(raw))
		if err != nil {
			return nil, err
		}
		if raw, err = io.ReadAll(zr); err != nil {
			return nil, err
		}
	}
	return Parse(raw)
}

const psf2Magic = 0x864ab572

// Parse reads a PSF2 font with a Unicode table.
func Parse(b []byte) (*Font, error) {
	if len(b) < 32 || binary.LittleEndian.Uint32(b) != psf2Magic {
		return nil, fmt.Errorf("not a PSF2 font")
	}
	hdr := make([]uint32, 8)
	for i := range hdr {
		hdr[i] = binary.LittleEndian.Uint32(b[4*i:])
	}
	hsize, flags, n, gsize, h, w := int(hdr[2]), hdr[3], int(hdr[4]), int(hdr[5]), int(hdr[6]), int(hdr[7])
	if flags&1 == 0 {
		return nil, fmt.Errorf("font has no Unicode table")
	}
	if hsize+n*gsize > len(b) {
		return nil, fmt.Errorf("font is truncated")
	}
	f := &Font{W: w, H: h, BPR: (w + 7) / 8, Map: map[rune]int{}}
	for i := 0; i < n; i++ {
		f.Glyphs = append(f.Glyphs, b[hsize+i*gsize:hsize+(i+1)*gsize])
	}
	p := hsize + n*gsize
	for i := 0; i < n && p < len(b); i++ {
		end := bytes.IndexByte(b[p:], 0xff)
		if end < 0 {
			return nil, fmt.Errorf("unicode table is truncated at glyph %d", i)
		}
		entry := b[p : p+end]
		if s := bytes.IndexByte(entry, 0xfe); s >= 0 {
			entry = entry[:s] // sequences after 0xFE are combining forms; single code points come first
		}
		for len(entry) > 0 {
			r, size := utf8.DecodeRune(entry)
			if r == utf8.RuneError && size <= 1 {
				return nil, fmt.Errorf("bad UTF-8 in unicode table at glyph %d", i)
			}
			f.Map[r] = i
			entry = entry[size:]
		}
		p += end + 1
	}
	return f, nil
}

// Has reports whether the font has a glyph for r.
func (f *Font) Has(r rune) bool {
	_, ok := f.Map[r]
	return ok
}

// Glyph returns r's bitmap, or the "?" glyph when the font lacks it.
func (f *Font) Glyph(r rune) []byte {
	if i, ok := f.Map[r]; ok {
		return f.Glyphs[i]
	}
	return f.Glyphs[f.Map['?']]
}

// Pixel reports whether pixel (x, y) of glyph g is set.
func (f *Font) Pixel(g []byte, x, y int) bool {
	return g[y*f.BPR+x/8]&(0x80>>(x%8)) != 0
}
