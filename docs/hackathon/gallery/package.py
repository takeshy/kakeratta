"""Validate the upload dimensions and size, then package the gallery PNGs."""
import json
import struct
import zipfile
from pathlib import Path
from PIL import Image, ImageDraw

gallery = Path(__file__).resolve().parent
captions = json.loads((gallery / "captions.json").read_text())
assert 1 <= len(captions) <= 15
expected = {item["file"] for item in captions}
actual = {str(path.relative_to(gallery)) for path in (gallery / "images").glob("*.png")}
assert actual == expected, "Image files do not match captions.json"

for item in captions:
    path = gallery / item["file"]
    data = path.read_bytes()
    assert data[:8] == b"\x89PNG\r\n\x1a\n", f"Not a PNG: {path}"
    width, height = struct.unpack(">II", data[16:24])
    assert (width, height) == (3600, 2400), f"Unexpected dimensions: {path}"
    assert len(data) < 5_000_000, f"Too large: {path}"
    print(f"{path.name}: {width} x {height}, {len(data):,} bytes")

# Compose the review sheet from finished PNGs, one image at a time. Rendering
# fifteen full browser layouts at once can exhaust the headless renderer.
thumb_width, thumb_height, gap, margin = 864, 576, 24, 36
rows = (len(captions) + 1) // 2
sheet = Image.new("RGB", (2 * thumb_width + gap + 2 * margin,
                          rows * thumb_height + (rows - 1) * gap + 2 * margin), "#dddcd2")
mask = Image.new("L", (thumb_width, thumb_height), 0)
ImageDraw.Draw(mask).rounded_rectangle((0, 0, thumb_width - 1, thumb_height - 1), radius=10, fill=255)
for index, item in enumerate(captions):
    with Image.open(gallery / item["file"]) as source:
        thumb = source.convert("RGB").resize((thumb_width, thumb_height), Image.Resampling.LANCZOS)
    sheet.paste(thumb, (margin + (index % 2) * (thumb_width + gap),
                       margin + (index // 2) * (thumb_height + gap)), mask)
sheet.save(gallery / "overview.png")
print(f"Contact sheet: {sheet.width} x {sheet.height}")

archive = gallery / "kakeratta-hackathon-gallery.zip"
with zipfile.ZipFile(archive, "w", zipfile.ZIP_DEFLATED) as bundle:
    for item in captions:
        path = gallery / item["file"]
        bundle.write(path, arcname=path.name)
with zipfile.ZipFile(archive) as bundle:
    assert bundle.testzip() is None
    assert len(bundle.namelist()) == len(captions)
print(f"Packaged {len(captions)} images: {archive.name}")
