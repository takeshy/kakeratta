#!/usr/bin/env python3
"""Draw the app icon: a kakera - one slab, broken, on a warm tile.

Deliberately simple and generated, so the mark can be regenerated at any size
without a design tool in the loop. The polygons below are the single source of
truth: the PNGs and the SVG favicon are both written from them. Run from the
repository root:

    python3 build/make-icons.py
"""
import struct
import zlib
from pathlib import Path

SIZE = 1024
SS = 3  # supersampling factor for smooth edges

TILE = (138, 90, 43)
SHARD = (253, 250, 243)
SHARD_SOFT = (237, 222, 200)

# One slab, split by a single crack, in fractions of the tile. The silhouette is
# one thing at a glance and two on a second look, which is the whole idea: a
# kakera is a piece of something that was whole. The crack is wide on purpose -
# a hairline vanishes at 16 pixels and the mark goes back to being a blob.
SHARDS = [
    ([(0.170, 0.240), (0.540, 0.195), (0.465, 0.455), (0.530, 0.620),
      (0.415, 0.815), (0.190, 0.800)], SHARD),
    ([(0.650, 0.190), (0.835, 0.220), (0.848, 0.800), (0.520, 0.828),
      (0.615, 0.625), (0.550, 0.450)], SHARD_SOFT),
]


def inside(polygon, x, y):
    """Ray casting, in tile fractions."""
    hit = False
    count = len(polygon)
    for index in range(count):
        ax, ay = polygon[index]
        bx, by = polygon[(index + 1) % count]
        if (ay > y) != (by > y) and x < (bx - ax) * (y - ay) / (by - ay) + ax:
            hit = not hit
    return hit


def rounded_tile(x, y, size, radius):
    margin = size * 0.055
    left, top = margin, margin
    right, bottom = size - margin, size - margin
    if not (left <= x <= right and top <= y <= bottom):
        return False
    if (x < left + radius or x > right - radius) and (y < top + radius or y > bottom - radius):
        cx = left + radius if x < left + radius else right - radius
        cy = top + radius if y < top + radius else bottom - radius
        return (x - cx) ** 2 + (y - cy) ** 2 <= radius ** 2
    return True


def shard_pixel(x, y, size):
    """Return the colour of the fragment at a point, or None."""
    u, v = x / size, y / size
    for polygon, colour in SHARDS:
        if inside(polygon, u, v):
            return colour
    return None


def render(size):
    radius = size * 0.22
    rows = []
    for py in range(size):
        row = bytearray()
        for px in range(size):
            r = g = b = a = 0
            for sy in range(SS):
                for sx in range(SS):
                    x = px + (sx + 0.5) / SS
                    y = py + (sy + 0.5) / SS
                    if not rounded_tile(x, y, size, radius):
                        continue
                    colour = shard_pixel(x, y, size) or TILE
                    r += colour[0]
                    g += colour[1]
                    b += colour[2]
                    a += 255
            samples = SS * SS
            if a:
                row += bytes((min(255, round(r * 255 / a)), min(255, round(g * 255 / a)),
                              min(255, round(b * 255 / a)), round(a / samples)))
            else:
                row += bytes((0, 0, 0, 0))
        rows.append(bytes(row))
    return rows


def chunk(tag, data):
    payload = tag + data
    return struct.pack(">I", len(data)) + payload + struct.pack(">I", zlib.crc32(payload))


def write_png(path, rows, size):
    raw = b"".join(b"\x00" + row for row in rows)
    png = (b"\x89PNG\r\n\x1a\n"
           + chunk(b"IHDR", struct.pack(">IIBBBBB", size, size, 8, 6, 0, 0, 0))
           + chunk(b"IDAT", zlib.compress(raw, 9))
           + chunk(b"IEND", b""))
    Path(path).parent.mkdir(parents=True, exist_ok=True)
    Path(path).write_bytes(png)
    return png


def scale(rows, size, target):
    out = []
    for y in range(target):
        row = bytearray()
        for x in range(target):
            sx = int(x * size / target)
            sy = int(y * size / target)
            row += rows[sy][sx * 4:sx * 4 + 4]
        out.append(bytes(row))
    return out


def write_wide(path, rows, width=310, height=150):
    """The store's wide tile: the square mark centred on a transparent strip."""
    mark = scale(rows, SIZE, height)
    out = []
    left = (width - height) // 2
    for y in range(height):
        row = bytearray(bytes(4) * width)
        row[left * 4:(left + height) * 4] = mark[y]
        out.append(bytes(row))
    raw = b"".join(b"\x00" + row for row in out)
    png = (b"\x89PNG\r\n\x1a\n"
           + chunk(b"IHDR", struct.pack(">IIBBBBB", width, height, 8, 6, 0, 0, 0))
           + chunk(b"IDAT", zlib.compress(raw, 9))
           + chunk(b"IEND", b""))
    Path(path).write_bytes(png)


def write_svg(path):
    """The favicon. Same polygons, so the tab and the home screen agree."""
    def points(polygon):
        return " ".join(f"{x * 64:.2f},{y * 64:.2f}" for x, y in polygon)

    def rgb(colour):
        return "#%02x%02x%02x" % colour

    shapes = "\n  ".join(
        f'<polygon points="{points(polygon)}" fill="{rgb(colour)}" />' for polygon, colour in SHARDS
    )
    svg = (
        '<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 64 64" role="img" aria-label="kakeratta">\n'
        f'  <rect x="3.52" y="3.52" width="56.96" height="56.96" rx="14.08" fill="{rgb(TILE)}" />\n'
        f'  {shapes}\n'
        "</svg>\n"
    )
    Path(path).write_text(svg, encoding="utf-8")


def main():
    root = Path(__file__).resolve().parent
    rows = render(SIZE)
    write_png(root / "appicon.png", rows, SIZE)
    # The page and the manifest load this one.
    write_png(root.parent / "frontend" / "public" / "icon.png", scale(rows, SIZE, 512), 512)
    write_svg(root.parent / "frontend" / "public" / "icon.svg")
    for name, target in (("windows/msix/Assets/Square44x44Logo.png", 44),
                         ("windows/msix/Assets/Square150x150Logo.png", 150),
                         ("windows/msix/Assets/StoreLogo.png", 50)):
        path = root / name
        if path.parent.exists():
            write_png(path, scale(rows, SIZE, target), target)
    wide = root / "windows/msix/Assets/Wide310x150Logo.png"
    if wide.parent.exists():
        write_wide(wide, rows)
    if (root / "windows").exists():
        ico_png = write_png(root / "windows" / "icon-256.png", scale(rows, SIZE, 256), 256)
        header = struct.pack("<HHH", 0, 1, 1) + struct.pack("<BBBBHHII", 0, 0, 0, 0, 1, 32, len(ico_png), 22)
        (root / "windows" / "icon.ico").write_bytes(header + ico_png)
        (root / "windows" / "icon-256.png").unlink()
    print("icons written")


if __name__ == "__main__":
    main()
