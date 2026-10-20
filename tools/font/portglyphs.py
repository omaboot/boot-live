"""The port symbols: a port's opening as seen on the Mac's side, two cells wide.

Each symbol is a square of S×S pixels (S = the cell height), split into a left and a right glyph
at private-use code points U+E200 + 2k and +1. Shapes are drawn on a 16-unit grid and rasterised
at each size by sampling pixel centres, so they stay crisp at 16, 24 and 32 px.

The port shapes and their rasteriser.
"""
import math

BASE = 0xE200
# k-th symbol at BASE + 2k (left half) and BASE + 2k + 1 (right half). Never reorder: the code
# points are in internal/icons.
NAMES = ["usba", "usbc", "tb", "magsafe", "hp", "sd", "eth", "fw"]


def rrect(x0, y0, x1, y1, r=0.0, cham=0.0):
    """Inside test for a rectangle with rounded corners r and chamfered bottom corners cham."""
    def f(x, y):
        if not (x0 <= x <= x1 and y0 <= y <= y1):
            return False
        if r and (x < x0 + r or x > x1 - r) and (y < y0 + r or y > y1 - r):
            cx = x0 + r if x < x0 + r else x1 - r
            cy = y0 + r if y < y0 + r else y1 - r
            if math.hypot(x - cx, y - cy) > r:
                return False
        if cham:
            if y > y1 - cham and (x < x0 + (y - (y1 - cham)) or x > x1 - (y - (y1 - cham))):
                return False
        return True
    return f


def circle(cx, cy, r):
    return lambda x, y: math.hypot(x - cx, y - cy) <= r


def shapes(name, u, t):
    """Lists of (inside-test, mode) in pixel units: mode 'fill' or 'line' (outline of stroke t)."""
    U = lambda v: v * u
    if name == "usba":  # rectangle, the plastic tongue in its upper half
        return [(rrect(U(1), U(5), U(15), U(11)), "line"), (rrect(U(3), U(5) + t, U(13), U(8)), "fill")]
    if name == "usbc":  # a pill, the tongue a line across its middle
        return [(rrect(U(2.5), U(5.5), U(13.5), U(10.5), r=U(2.5)), "line"), (rrect(U(5), U(7.5), U(11), U(8.5)), "fill")]
    if name == "tb":  # Mini DisplayPort: bottom corners cut off
        return [(rrect(U(3), U(4.5), U(13), U(11.5), cham=U(2.5)), "line"), (rrect(U(5), U(6.5), U(11), U(7.5)), "fill")]
    if name == "magsafe":  # long rounded slot with the five pins
        out = [(rrect(U(0.5), U(5.5), U(15.5), U(10.5), r=U(2.5)), "line")]
        for k in range(5):
            out.append((rrect(U(3.7 + 2 * k), U(7.4), U(4.6 + 2 * k), U(8.6)), "fill"))
        return out
    if name == "hp":  # the jack: a ring
        return [(circle(U(8), U(8), U(3.5)), "line")]
    if name == "sd":  # the card slot
        return [(rrect(U(0.5), U(7), U(15.5), U(9)), "fill")]
    if name == "eth":  # RJ45: square with the latch notch at the bottom
        body = rrect(U(3), U(3.5), U(13), U(10.5))
        tab = rrect(U(6), U(10.5), U(10), U(12.5))
        return [(lambda x, y: body(x, y) or tab(x, y), "line")]
    if name == "fw":  # FireWire 800: a box with one rounded side
        return [(rrect(U(3), U(5), U(13), U(11), r=U(1.5)), "line")]
    raise KeyError(name)


def render(name, S):
    """The symbol as an S×S grid of booleans, rows top down."""
    u = S / 16.0
    t = max(1, round(S / 16))
    grid = [[False] * S for _ in range(S)]
    for f, mode in shapes(name, u, t):
        inside = [[f(x + 0.5, y + 0.5) for x in range(S)] for y in range(S)]
        for y in range(S):
            for x in range(S):
                if not inside[y][x]:
                    continue
                if mode == "fill":
                    grid[y][x] = True
                else:  # outline: inside, and within t of an outside pixel
                    edge = any(not (0 <= x + dx < S and 0 <= y + dy < S and inside[y + dy][x + dx])
                               for dx in range(-t, t + 1) for dy in range(-t, t + 1) if max(abs(dx), abs(dy)) <= t)
                    grid[y][x] = grid[y][x] or edge
    return grid
