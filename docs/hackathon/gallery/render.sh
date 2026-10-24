#!/usr/bin/env bash
set -euo pipefail

gallery_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
repo_dir="$(cd -- "$gallery_dir/../../.." && pwd)"
scratch_dir="$repo_dir/tmp/hackathon-gallery"
browser="${CHROMIUM:-chromium}"

mkdir -p "$scratch_dir"
if [[ -z "${GALLERY_ONLY:-}" ]]; then
  "$browser" --headless --no-sandbox --disable-gpu --hide-scrollbars \
  --allow-file-access-from-files --no-first-run --no-default-browser-check \
  --user-data-dir="$scratch_dir/chrome-profile" --window-size=1000,880 --force-device-scale-factor=2 \
  --virtual-time-budget=1000 --screenshot="$gallery_dir/infographic-example.png" \
  "file://$gallery_dir/infographic-example.html" > "$scratch_dir/chromium.log" 2>&1
fi
node "$gallery_dir/build.mjs"
node "$gallery_dir/render.mjs"
python3 "$gallery_dir/package.py"
