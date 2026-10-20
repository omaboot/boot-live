"""Rebuild omaboot-v{16,24,32}n.psf.gz: drop Greek and unused symbols, add the port symbols and the Nerd icons.

The current .psf.gz files are the only source of the Terminus-derived glyphs, so this tool edits
them in place (see assets/fonts/NOTICE for what the font is). It processes the three sizes
together and:

  1. removes every Greek code point (U+0370-U+03FF) from the Unicode table; a glyph left with
     no code point is freed. Glyphs shared with other scripts (A, Ω as the ohm sign, ε as ∈, π as
     Cyrillic п ...) stay under their other code points;
  1b. removes the symbols boot-live never draws (UNUSED: double-line box drawing, DOS leftovers),
     to make room for the icons' second cells; every letter stays;
  2. merges glyphs whose bitmaps are identical at all three sizes (g and Cyrillic д, u and и,
     ɛ and ∈): one glyph, both code points;
  3. draws the port symbols (portglyphs.py) and the marks (MARKS: the partial mark ◐, the
     console key's ` at U+E210),
     and rasterises the Nerd icons from JetBrains Mono Nerd Font, each into the slot that already
     has its code point or the lowest free slot.

Idempotent: running it on its own output writes the same bytes. Fails, writing nothing, if the
new glyphs don't fit in 512.

usage: uv run --with pillow --with fonttools python tools/font/build.py [--fonts DIR] [--out DIR]
       [--ttf PATH] [--preview FILE]
"""
import argparse, gzip, io, math, os, struct, sys

from PIL import Image, ImageDraw, ImageFont
from fontTools.ttLib import TTFont

import portglyphs

SIZES = (16, 24, 32)
PSF2_MAGIC = 0x864AB572
GREEK = range(0x0370, 0x0400)
# Symbols no boot-live screen draws: Terminus's double-line box drawing and CP437 leftovers.
# ‡ went to make room for the partial mark ◐.
UNUSED = set(map(ord, "╬╪║╒╔╗╘╙╚╛╝╞╟╠╡╣╦╧╨╩" "⌐⌠⌡₧ⁿ≡∩" "♦○↕↔" "‡" "¤"))  # ¤ made room for the console key (2026-10-15)
TTF = "/usr/share/fonts/TTF/JetBrainsMonoNerdFont-Regular.ttf"

# The Nerd icons, two cells each (one cell read as too small), drawn from their
# Nerd Fonts code point at TALL of the cell height and split into a left and a right glyph at our own
# private code points ICON_BASE + 2i and + 2i + 1 (U+E000 up), in this list's order. Not at the Nerd code points:
# the Linux console's Unicode map holds 16-bit code points only (Material Design's U+F0000 up
# can't be mapped), and it shows U+F000-U+F1FF as "glyph number n" directly (Font Awesome's
# icons live there). "fa-" are Font Awesome icons, "md-" Material Design Icons (the TTF's glyph
# names). Keep in step with internal/icons/icons.go; only ever append, so code points stay put.
ICON_BASE = 0xE000
TALL = 0.75
ICONS = [
    # ports
    ("usb", 0xF287),          # fa-usb
    ("thunderbolt", 0xF140B),  # md-lightning_bolt
    ("magsafe", 0xF06A5),     # md-power_plug
    ("headphones", 0xF02CB),  # md-headphones
    ("line-in", 0xF036C),     # md-microphone (0.7.4: was md-audio_input_stereo_minijack)
    ("optical", 0xF12B8),     # md-toslink
    ("sd", 0xF0479),          # md-sd
    ("ethernet", 0xF0200),    # md-ethernet
    ("firewire", 0xF05BE),    # md-firewire
    ("hdmi", 0xF0841),        # md-video_input_hdmi
    ("video-out", 0xF0379),   # md-monitor
    ("expresscard", 0xF0FB2),  # md-expansion_card_variant
    ("ir", 0xF0454),          # md-remote
    ("lock", 0xF033E),        # md-lock
    # network
    ("wifi", 0xF1EB),         # fa-wifi
    # categories and hardware
    ("power", 0xF011),        # fa-power_off
    ("gpu", 0xF08AE),         # md-expansion_card
    ("display", 0xF108),      # fa-desktop
    ("keyboard", 0xF11C),     # fa-keyboard_o
    ("battery", 0xF241),      # fa-battery_three_quarters
    ("speaker", 0xF028),      # fa-volume_up
    ("mic", 0xF130),          # fa-microphone
    ("camera", 0xF030),       # fa-camera
    ("bluetooth", 0xF293),    # fa-bluetooth
    ("disk", 0xF0A0),         # fa-hdd_o
    ("thermo", 0xF2C9),       # fa-thermometer_half
    ("fan", 0xF0210),         # md-fan
    ("sleep", 0xF186),        # fa-moon_o
    ("trackpad", 0xF07F8),    # md-trackpad (F0933 is md-trackpad_lock)
    ("check", 0xF00C),        # fa-check
    ("warn", 0xF071),         # fa-warning
    ("phone", 0xF0121),       # md-cellphone_link: USB tethering
]


# The result marks drawn here, one cell each: ◐, a circle with its left half filled, is the partial
# mark (it was "~"). Drawn like the other marks,
# sampling pixel centres, so it is crisp at 16, 24 and 32 px.
def half_circle(w, h):
    """◐ as an h-row grid of w booleans: the ring of stroke h/16, the left half filled."""
    cx, cy, r = w / 2, h * 0.56, w * 0.48
    t = max(1, round(h / 16))
    grid = []
    for y in range(h):
        row = []
        for x in range(w):
            d = math.hypot(x + 0.5 - cx, y + 0.5 - cy)
            row.append(d <= r and (d > r - t or x + 0.5 < cx))
        grid.append(row)
    return grid


def key_grave(w, h):
    """The console key's ` for the hint bar's key cap: an even stroke in the font's weight, about
    a third of the cell high and centred, where the font's own ` is small and at the top edge
   . Our own code point: typing ` is unchanged."""
    x0, y0, x1, y1 = w * .25, h * .27, w * .75, h * .62
    r = 1.5 * max(1, round(h / 16)) / 2
    grid = []
    for y in range(h):
        row = []
        for x in range(w):
            px, py, dx, dy = x + .5, y + .5, x1 - x0, y1 - y0
            u = max(0, min(1, ((px - x0) * dx + (py - y0) * dy) / (dx * dx + dy * dy)))
            row.append(math.hypot(px - (x0 + u * dx), py - (y0 + u * dy)) <= r)
        grid.append(row)
    return grid


MARKS = {0x25D0: half_circle, 0xE210: key_grave}


# --- PSF2 -------------------------------------------------------------------------------------

class Font:
    def __init__(self, raw):
        magic, ver, hsize, flags, n, gsize, h, w = struct.unpack("<8I", raw[:32])
        if magic != PSF2_MAGIC or not flags & 1:
            raise SystemExit("not a PSF2 font with a Unicode table")
        self.w, self.h, self.bpr = w, h, (w + 7) // 8
        self.header = raw[:hsize]
        self.glyphs = [raw[hsize + i * gsize:hsize + (i + 1) * gsize] for i in range(n)]
        self.cps, self.seqs = [], []  # per glyph: single code points; raw 0xFE sequences
        p = hsize + n * gsize
        for _ in range(n):
            end = raw.index(b"\xff", p)
            single, _, seq = raw[p:end].partition(b"\xfe")
            self.cps.append([ord(c) for c in single.decode("utf-8")])
            self.seqs.append(b"\xfe" + seq if seq else b"")
            p = end + 1

    def slot(self, cp):
        for i, cps in enumerate(self.cps):
            if cp in cps:
                return i
        return None

    def free(self):
        return [i for i, cps in enumerate(self.cps) if not cps and not self.seqs[i]]

    def bytes(self):
        out = bytearray(self.header)
        for g in self.glyphs:
            out += g
        for cps, seq in zip(self.cps, self.seqs):
            out += "".join(map(chr, cps)).encode("utf-8") + seq + b"\xff"
        return bytes(out)


def pack(grid, x0, w, h):
    """Rows of grid[y][x0:x0+w] as PSF bitmap bytes."""
    bpr = (w + 7) // 8
    out = bytearray()
    for y in range(h):
        row = 0
        for i in range(w):
            if grid[y][x0 + i]:
                row |= 1 << (bpr * 8 - 1 - i)
        out += row.to_bytes(bpr, "big")
    return bytes(out)


# --- the new glyphs ---------------------------------------------------------------------------

def icon_raster(ttf, cp, w, h):
    """The icon scaled to fit w-2 wide and TALL of h high, centred."""
    ref = ImageFont.truetype(ttf, 100)
    x0, y0, x1, y1 = ref.getbbox(chr(cp), anchor="ls")
    sc = min((w - 2) / (x1 - x0), (h * TALL) / (y1 - y0))
    f = ImageFont.truetype(ttf, max(4, 100 * sc))
    x0, y0, x1, y1 = f.getbbox(chr(cp), anchor="ls")
    img = Image.new("L", (w, h), 0)
    ImageDraw.Draw(img).text(((w - (x1 - x0)) / 2 - x0, (h - (y1 - y0)) / 2 - y0), chr(cp), font=f, fill=255, anchor="ls")
    px = img.load()
    return [[px[x, y] >= 110 for x in range(w)] for y in range(h)]


def icon_slots():
    """[(name, Nerd code point, our left half's code point)] for the icons; the right half is +1."""
    return [(name, cp, ICON_BASE + 2 * i) for i, (name, cp) in enumerate(ICONS)]


def new_glyphs(fonts, ttf):
    """{code point: {size: bitmap bytes}} for the port symbols, the marks and the icons in the TTF."""
    cmap = TTFont(ttf).getBestCmap()
    out = {}
    for k, name in enumerate(portglyphs.NAMES):
        for half in (0, 1):
            out[portglyphs.BASE + 2 * k + half] = {}
        for px, f in fonts.items():
            assert 2 * f.w == f.h, (px, f.w, f.h)
            grid = portglyphs.render(name, f.h)
            for half in (0, 1):
                out[portglyphs.BASE + 2 * k + half][px] = pack(grid, half * f.w, f.w, f.h)
    for cp, draw in MARKS.items():
        out[cp] = {px: pack(draw(f.w, f.h), 0, f.w, f.h) for px, f in fonts.items()}
    for name, cp, ours in icon_slots():
        if cp not in cmap:
            print(f"skipped {name} U+{cp:04X}: not in {os.path.basename(ttf)}", file=sys.stderr)
            continue
        for half in (0, 1):
            out[ours + half] = {}
        for px, f in fonts.items():
            grid = icon_raster(ttf, cp, 2 * f.w, f.h)
            for half in (0, 1):
                out[ours + half][px] = pack(grid, half * f.w, f.w, f.h)
    return out


def drop_nerd_points(fonts):
    """Takes the icons off the Nerd code points an earlier build put them at (U+F000-U+F8FF and
    beyond U+FFFF: nothing else of ours lives there)."""
    for f in fonts.values():
        f.cps = [[c for c in cps if not (0xF000 <= c <= 0xF8FF or c > 0xFFFF)] for cps in f.cps]


# --- the edit ---------------------------------------------------------------------------------

def drop_greek(fonts):
    """Drops Greek and the UNUSED symbols."""
    dropped = {}
    for px, f in fonts.items():
        before = len(f.free())
        f.cps = [[c for c in cps if c not in GREEK and c not in UNUSED] for cps in f.cps]
        dropped[px] = len(f.free()) - before
    return dropped


def merge_duplicates(fonts, keep_apart):
    """Merges glyphs identical at every size into one; returns the merged-away code point sets."""
    ref = fonts[SIZES[0]]
    groups = {}
    for i, cps in enumerate(ref.cps):
        if not cps or ref.seqs[i] or any(c in keep_apart for c in cps):
            continue
        slots = {px: f.slot(cps[0]) for px, f in fonts.items()}
        for px, f in fonts.items():
            if slots[px] is None or sorted(f.cps[slots[px]]) != sorted(cps) or f.seqs[slots[px]]:
                raise SystemExit(f"the sizes' Unicode tables differ at U+{cps[0]:04X}")
        key = tuple(fonts[px].glyphs[slots[px]] for px in SIZES)
        groups.setdefault(key, []).append(cps[0])
    merged = []
    for firsts in groups.values():
        if len(firsts) < 2:
            continue
        firsts.sort()
        for f in fonts.values():
            keep = f.slot(firsts[0])
            for cp in firsts[1:]:
                gone = f.slot(cp)
                f.cps[keep] += f.cps[gone]
                f.cps[gone] = []
        merged.append(firsts)
    return merged


def build(fonts, ttf):
    add = new_glyphs(fonts, ttf)
    drop_nerd_points(fonts)
    stats = {"greek": drop_greek(fonts), "merged": merge_duplicates(fonts, set(add))}
    for f in fonts.values():
        for i in f.free():
            f.glyphs[i] = bytes(len(f.glyphs[i]))
    need = sum(1 for cp in add if fonts[SIZES[0]].slot(cp) is None)
    if need > len(fonts[SIZES[0]].free()):
        raise SystemExit(f"{need} new glyphs, only {len(fonts[SIZES[0]].free())} free slots")
    for cp in sorted(add, key=lambda c: (c < portglyphs.BASE or c > portglyphs.BASE + 15, c)):
        for px, f in fonts.items():
            i = f.slot(cp)
            if i is None:
                i = f.free()[0]
                f.cps[i] = [cp]
            f.glyphs[i] = add[cp][px]
    stats["added"] = need
    stats["new"] = len(add)
    stats["free"] = {px: len(f.free()) for px, f in fonts.items()}
    return stats


def preview(fonts, path):
    """ASCII art of the 16 px port symbols and a few icons, read back from the font."""
    f = fonts[SIZES[0]]
    rows = lambda cp: [[bool(f.glyphs[f.slot(cp)][y * f.bpr + x // 8] & (0x80 >> (x % 8))) for x in range(f.w)] for y in range(f.h)]
    art = lambda grid: "\n".join("".join("█" if c else "·" for c in row) for row in grid)
    out = []
    for k, name in enumerate(portglyphs.NAMES):
        l, r = rows(portglyphs.BASE + 2 * k), rows(portglyphs.BASE + 2 * k + 1)
        out.append(f"{name} U+{portglyphs.BASE + 2 * k:04X}+1\n" + art([a + b for a, b in zip(l, r)]))
    for cp in MARKS:
        out.append(f"U+{cp:04X} {chr(cp)}\n" + art(rows(cp)))
    for name, _, cp in icon_slots():
        if f.slot(cp) is not None:
            out.append(f"{name} U+{cp:04X}+1\n" + art([a + b for a, b in zip(rows(cp), rows(cp + 1))]))
    with open(path, "w") as fh:
        fh.write("\n\n".join(out) + "\n")


def main():
    here = os.path.dirname(os.path.abspath(__file__))
    ap = argparse.ArgumentParser(description=__doc__.split("\n")[0])
    ap.add_argument("--fonts", default=os.path.join(here, "..", "..", "assets", "fonts"), help="where to read omaboot-v*n.psf.gz")
    ap.add_argument("--out", help="where to write them (default: --fonts, in place)")
    ap.add_argument("--ttf", default=TTF, help="JetBrains Mono Nerd Font Regular")
    ap.add_argument("--preview", help="also write an ASCII preview of the 16 px glyphs here")
    a = ap.parse_args()
    out = a.out or a.fonts
    fonts = {px: Font(gzip.open(os.path.join(a.fonts, f"omaboot-v{px}n.psf.gz")).read()) for px in SIZES}
    s = build(fonts, a.ttf)
    os.makedirs(out, exist_ok=True)
    for px, f in fonts.items():
        buf = io.BytesIO()
        with gzip.GzipFile(filename=f"omaboot-v{px}n.psf", mode="wb", fileobj=buf, compresslevel=9, mtime=0) as z:
            z.write(f.bytes())
        with open(os.path.join(out, f"omaboot-v{px}n.psf.gz"), "wb") as fh:
            fh.write(buf.getvalue())
    print(f"Greek-only and unused glyphs freed: {s['greek']}; duplicates merged: "
          + (", ".join("+".join(chr(c) for c in m) for m in s["merged"]) or "none")
          + f"; new glyphs: {s['new']} ({s['added']} newly placed); free slots: {s['free']}")
    if a.preview:
        preview(fonts, a.preview)


if __name__ == "__main__":
    main()
