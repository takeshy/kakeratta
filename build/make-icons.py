#!/usr/bin/env python3
"""Draw the app icon: an open book on a rounded tile.

Deliberately simple and generated, so the mark can be regenerated at any size
without a design tool in the loop. Run from the repository root:

    python3 build/make-icons.py
"""
import struct
import zlib
from pathlib import Path

SIZE = 1024
SS = 3  # supersampling factor for smooth edges

TILE = (138, 90, 43)
PAGE = (253, 250, 243)
LINE = (206, 190, 164)
SPINE = (92, 58, 26)


def rounded_tile(x, y, size, radius):
    margin = size * 0.055
    left, top = margin, margin
    right, bottom = size - margin, size - margin
    if not (left <= x <= right and top <= y <= bottom):
        return False
    for cx, cy in ((left + radius, top + radius), (right - radius, top + radius),
                   (left + radius, bottom - radius), (right - radius, bottom - radius)):
        if (x < left + radius or x > right - radius) and (y < top + radius or y > bottom - radius):
            if (x - cx) ** 2 + (y - cy) ** 2 > radius ** 2:
                continue
            return True
    if (x < left + radius or x > right - radius) and (y < top + radius or y > bottom - radius):
        return False
    return True


def book_pixel(x, y, size):
    """Return the colour of the open book at a point, or None."""
    w, h = size * 0.54, size * 0.36
    cx, cy = size / 2, size * 0.53
    left, right = cx - w / 2, cx + w / 2
    top, bottom = cy - h / 2, cy + h / 2
    if not (left <= x <= right and top <= y <= bottom):
        return None
    # The pages lift towards the spine: the top edge curves up in the middle.
    lift = size * 0.055 * (1 - abs(x - cx) / (w / 2)) ** 0.7
    if y < top + size * 0.02 - lift:
        return None
    if y > bottom - size * 0.02 * (abs(x - cx) / (w / 2)):
        return None
    if abs(x - cx) < size * 0.012:
        return SPINE
    # Ruled lines, skipped near the spine and the outer edge.
    rel = (y - top) / h
    if 0.22 < rel < 0.9 and abs(x - cx) > size * 0.04 and abs(x - cx) < w / 2 - size * 0.045:
        for line in (0.36, 0.56, 0.76):
            if abs(rel - line) < 0.028:
                return LINE
    return PAGE


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
                    colour = None
                    if rounded_tile(x, y, size, radius):
                        colour = book_pixel(x, y, size) or TILE
                    if colour:
                        r += colour[0]
                        g += colour[1]
                        b += colour[2]
                        a += 255
            samples = SS * SS
            if a:
                row += bytes((r // samples * samples // max(a // 255, 1) % 256,
                              g // samples * samples // max(a // 255, 1) % 256,
                              b // samples * samples // max(a // 255, 1) % 256,
                              a // samples))
            else:
                row += bytes((0, 0, 0, 0))
        rows.append(bytes(row))
    return rows


def write_png(path, rows, size):
    raw = b"".join(b"\x00" + row for row in rows)
    def chunk(tag, data):
        payload = tag + data
        return struct.pack(">I", len(data)) + payload + struct.pack(">I", zlib.crc32(payload))
    png = (b"\x89PNG\r\n\x1a\n"
           + chunk(b"IHDR", struct.pack(">IIBBBBB", size, size, 8, 6, 0, 0, 0))
           + chunk(b"IDAT", zlib.compress(raw, 9))
           + chunk(b"IEND", b""))
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
    left = (width - height) // 2
    out = []
    for y in range(height):
        row = bytearray(bytes(4) * width)
        row[left * 4:(left + height) * 4] = mark[y]
        out.append(bytes(row))
    raw = b"".join(b"\x00" + row for row in out)

    def chunk(tag, data):
        payload = tag + data
        return struct.pack(">I", len(data)) + payload + struct.pack(">I", zlib.crc32(payload))

    png = (b"\x89PNG\r\n\x1a\n"
           + chunk(b"IHDR", struct.pack(">IIBBBBB", width, height, 8, 6, 0, 0, 0))
           + chunk(b"IDAT", zlib.compress(raw, 9))
           + chunk(b"IEND", b""))
    Path(path).write_bytes(png)


def main():
    root = Path(__file__).resolve().parent
    rows = render(SIZE)
    write_png(root / "appicon.png", rows, SIZE)
    for name, target in (("windows/msix/Assets/Square44x44Logo.png", 44),
                         ("windows/msix/Assets/Square150x150Logo.png", 150),
                         ("windows/msix/Assets/StoreLogo.png", 50)):
        path = root / name
        if path.parent.exists():
            write_png(path, scale(rows, SIZE, target), target)
    wide = root / "windows/msix/Assets/Wide310x150Logo.png"
    if wide.parent.exists():
        write_wide(wide, rows)
    ico_png = write_png(root / "windows" / "icon-256.png", scale(rows, SIZE, 256), 256)
    header = struct.pack("<HHH", 0, 1, 1) + struct.pack("<BBBBHHII", 0, 0, 0, 0, 1, 32, len(ico_png), 22)
    (root / "windows" / "icon.ico").write_bytes(header + ico_png)
    (root / "windows" / "icon-256.png").unlink()
    print("icons written")


if __name__ == "__main__":
    main()
