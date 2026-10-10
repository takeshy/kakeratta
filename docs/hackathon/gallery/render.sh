#!/usr/bin/env bash
set -euo pipefail

gallery_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
repo_dir="$(cd -- "$gallery_dir/../../.." && pwd)"
scratch_dir="$repo_dir/tmp/hackathon-gallery"
browser="${CHROMIUM:-chromium}"

mkdir -p "$scratch_dir"
"$browser" --headless --no-sandbox --disable-gpu --hide-scrollbars \
  --allow-file-access-from-files --no-first-run --no-default-browser-check \
  --user-data-dir="$scratch_dir/chrome-profile" --window-size=1000,850 \
  --virtual-time-budget=1000 --screenshot="$gallery_dir/infographic-example.png" \
  "file://$gallery_dir/infographic-example.html" > "$scratch_dir/chromium.log" 2>&1
node "$gallery_dir/build.mjs"
render() {
  local layout="$1" output="$2" dimensions="$3"
  "$browser" --headless --no-sandbox --disable-gpu --hide-scrollbars \
    --no-first-run --no-default-browser-check \
    --user-data-dir="$scratch_dir/chrome-profile" \
    --window-size="$dimensions" --virtual-time-budget=1000 \
    --screenshot="$output" "file://$scratch_dir/$layout.html" \
    > "$scratch_dir/chromium.log" 2>&1 || {
      cat "$scratch_dir/chromium.log" >&2
      return 1
    }
}
for layout in "$scratch_dir"/[0-9][0-9]-*.html; do
  [[ "$(basename -- "$layout")" == "14-choose-your-agent.html" ]] && continue
  name="$(basename -- "$layout" .html)"
  render "$name" "$gallery_dir/images/$name.png" 1800,1200
done
render overview "$gallery_dir/overview.png" 1824,4848
python3 "$gallery_dir/package.py"
