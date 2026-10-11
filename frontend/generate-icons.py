"""Generate web icons from the user-supplied root kakeratta.jpg."""
from base64 import b64encode
from pathlib import Path
import re
from PIL import Image

root = Path(__file__).resolve().parent.parent
public = root / "frontend/public"
with Image.open(root / "kakeratta.jpg") as original:
    source = original.convert("RGB")
    icon = source.resize((512, 512), Image.Resampling.LANCZOS)
    icon.save(public / "icon.png", optimize=True)
    # Preserve all artwork inside the central maskable safe circle.
    maskable = Image.new("RGB", (512, 512), source.getpixel((0, 0)))
    maskable.paste(source.resize((352, 352), Image.Resampling.LANCZOS), (80, 80))
    maskable.save(public / "icon-maskable.png", optimize=True)
    encoded = b64encode((public / "icon.png").read_bytes()).decode("ascii")
    (public / "icon.svg").write_text(
        '<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 512 512" role="img" aria-label="kakeratta">\n'
        f'  <image width="512" height="512" href="data:image/png;base64,{encoded}" />\n'
        '</svg>\n'
    )
# Keep the static site's wordmark identical to the application header.
screen = (root / "frontend/src/screen.js").read_text()
wordmark = re.search(r"const WORDMARK = '([^']+)';", screen)
if not wordmark:
    raise ValueError("Application WORDMARK SVG was not found")
(public / "wordmark.svg").write_text(
    wordmark[1].replace('<svg ', '<svg xmlns="http://www.w3.org/2000/svg" ', 1) + "\n"
)
# A complete, self-contained brand lockup for both READMEs. The light backing
# keeps the outlined letters readable in GitHub's light and dark themes.
paths = re.search(r"<svg[^>]*>(.*)</svg>", wordmark[1])[1]
(public / "logo.svg").write_text(
    '<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 304 80" role="img" aria-label="kakeratta">\n'
    '  <rect width="304" height="80" rx="12" fill="#f6f3ed" />\n'
    '  <defs><clipPath id="icon"><rect x="8" y="8" width="64" height="64" rx="12" /></clipPath></defs>\n'
    f'  <image x="8" y="8" width="64" height="64" clip-path="url(#icon)" href="data:image/png;base64,{encoded}" />\n'
    '  <g transform="translate(88 20) scale(1.8)" fill="none" stroke="#23201c" stroke-width="2.4" stroke-linecap="round" stroke-linejoin="round">\n'
    f'    {paths}\n'
    '  </g>\n</svg>\n'
)
print("Generated web icons, application wordmark, and README logo.")
