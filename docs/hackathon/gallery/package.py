"""Validate the upload dimensions and size, then package the gallery PNGs."""
import json
import struct
import zipfile
from pathlib import Path

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
    assert (width, height) == (1800, 1200), f"Unexpected dimensions: {path}"
    assert len(data) < 5_000_000, f"Too large: {path}"
    print(f"{path.name}: {width} x {height}, {len(data):,} bytes")

archive = gallery / "kakeratta-hackathon-gallery.zip"
with zipfile.ZipFile(archive, "w", zipfile.ZIP_DEFLATED) as bundle:
    for item in captions:
        path = gallery / item["file"]
        bundle.write(path, arcname=path.name)
with zipfile.ZipFile(archive) as bundle:
    assert bundle.testzip() is None
    assert len(bundle.namelist()) == len(captions)
print(f"Packaged {len(captions)} images: {archive.name}")
