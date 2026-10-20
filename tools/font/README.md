# tools/font: rebuild omaboot-v

Rebuilds `assets/fonts/omaboot-v{16,24,32}n.psf.gz` (PSF2, 512 glyphs, 8×16, 12×24, 16×32)
from the current files, which are the only source of the Terminus-derived glyphs. From the
repository root:

    uv run --with pillow --with fonttools python tools/font/build.py

It edits the three files in place:

1. drops Greek (U+0370–U+03FF) and the symbols boot-live never draws (`UNUSED`: double-line
   box drawing, CP437 leftovers) from the Unicode table, freeing their glyphs; glyphs shared
   with Latin, Cyrillic or symbols keep their other code points;
2. merges glyphs identical at all three sizes (ù/ѝ, g/д, u/и, ɛ/∈);
3. adds the two-cell port symbols (`portglyphs.py`, U+E200–U+E20F), the drawn result marks
   (`MARKS` in `build.py`: the partial mark ◐, U+25D0, and the console key cap's `, U+E210; one cell each) and the two-cell Nerd
   icons (the `ICONS` list in `build.py`, rasterised from JetBrains Mono Nerd Font at 75% of the
   cell height and split in halves) at our own code points U+E000 + 2i and + 2i + 1, each into the slot that already holds its code point or the lowest
   free one. Not at the Nerd code points: the Linux console maps only 16-bit code points, and
   shows U+F000–U+F1FF as raw glyph numbers, so those icons came out as other characters.

Running it again on its output writes the same bytes. It fails, writing nothing, if the new
glyphs don't fit.

Options: `--out DIR` writes elsewhere instead of in place, `--preview FILE` writes ASCII art of
the 16 px port symbols and icons as read back from the font, `--ttf PATH` picks the Nerd Font
(default `/usr/share/fonts/TTF/JetBrainsMonoNerdFont-Regular.ttf`; the current icons came from
Nerd Fonts 3.5.1, Arch's ttf-jetbrains-mono-nerd-basic 3.5.1-1; another version may rasterise
slightly differently).

After a change: keep `internal/icons/icons.go` in step with the lists, update
`assets/fonts/NOTICE`, and run `make check` (its glyph tests load the fonts).
