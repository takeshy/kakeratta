#!/usr/bin/env python3
"""Write the captures' sizes into the pages that show them.

The captures are phone screens at 2x, so each is shown at half its pixel
size. The manual and the landing page carry width/height on their <img> so
the page does not jump while an image loads; the README, which GitHub renders
at the image's own size, is told the display width explicitly. Run after
`npm run captures`.
"""
import re
import struct
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
CAPTURES = ROOT / "docs" / "captures"
SCALE = 2


def size(lang: str, name: str) -> tuple[int, int] | None:
    png = CAPTURES / lang / f"{name}.png"
    if not png.exists():
        return None
    with png.open("rb") as f:
        head = f.read(24)
    width, height = struct.unpack(">II", head[16:24])
    return width // SCALE, height // SCALE


def html(path: Path) -> None:
    def sized(match: re.Match) -> str:
        tag, lang, name = match.group(0), match.group(1), match.group(2)
        dims = size(lang, name)
        if not dims:
            return tag
        tag = re.sub(r'\s+(width|height)="\d+"', "", tag)
        return tag[:-3] + f' width="{dims[0]}" height="{dims[1]}" />'

    text = path.read_text(encoding="utf-8")
    text = re.sub(r'<img [^>]*src="/captures/(ja|en)/([\w-]+)\.png"[^>]*/>', sized, text)
    path.write_text(text, encoding="utf-8")


def markdown(path: Path) -> None:
    def tag(alt: str, lang: str, name: str) -> str:
        dims = size(lang, name)
        width = dims[0] if dims else 390
        return f'<img src="docs/captures/{lang}/{name}.png" alt="{alt}" width="{width}">'

    text = path.read_text(encoding="utf-8")
    # Markdown images become tags, and tags already there get the current width.
    text = re.sub(r'!\[([^\]]*)\]\(docs/captures/(ja|en)/([\w-]+)\.png\)',
                  lambda m: tag(m.group(1), m.group(2), m.group(3)), text)
    text = re.sub(r'<img src="docs/captures/(ja|en)/([\w-]+)\.png" alt="([^"]*)" width="\d+">',
                  lambda m: tag(m.group(3), m.group(1), m.group(2)), text)
    path.write_text(text, encoding="utf-8")


for page in [ROOT / "frontend" / "manual" / "index.html", ROOT / "frontend" / "lp" / "index.html"]:
    html(page)
for readme in [ROOT / "README.md", ROOT / "README_ja.md"]:
    markdown(readme)
print("sizes written")
