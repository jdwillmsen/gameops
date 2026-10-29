#!/usr/bin/env bash
# Renders every shipped add-on model and checks each sheet has real content.
set -euo pipefail
here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
n=0
for geo in "$here"/../packs/*/resource_pack/models/entity/*.geo.json; do
  name=$(basename "$geo" .geo.json)
  tex=$(dirname "$geo")/../../textures/entity/$name.png
  [[ -f $tex ]] || continue
  python3 -I "$here/render-geo.py" "$geo" "$tex" "$tmp/$name.png" --scale 2 >/dev/null
  python3 -I - "$tmp/$name.png" <<'PY'
import sys
from PIL import Image
im = Image.open(sys.argv[1]).convert("RGBA")
bg = im.getpixel((0, 0))
px = list(im.getdata())
ratio = sum(1 for p in px if p != bg) / len(px)
assert ratio > 0.03, f"{sys.argv[1]}: only {ratio:.1%} of the sheet is model"
PY
  n=$((n + 1))
done
((n > 0)) || { echo "no models rendered" >&2; exit 1; }
echo "ok ($n models)"
