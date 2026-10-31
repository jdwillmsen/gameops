#!/usr/bin/env bash
# Smoke test: renders the cheetah in fast mode and checks the outputs exist and
# decode. Needs Chrome and network on the first run (the page is then cached).
set -euo pipefail
here=$(cd "$(dirname "$0")" && pwd)
out=$(mktemp -d)
trap 'rm -rf "$out"' EXIT

"$here/render.sh" --animal cheetah --fast --out "$out" >/dev/null

for f in cheetah_default.turntable.mp4 cheetah_default.walk.mp4 cheetah.contact-sheet.png manifest.json; do
  [[ -s $out/$f ]] || { echo "FAIL missing $f" >&2; exit 1; }
done
frames=$(ffprobe -v error -count_frames -select_streams v:0 -show_entries stream=nb_read_frames -of csv=p=0 "$out/cheetah_default.walk.mp4")
[[ $frames -ge 2 ]] || { echo "FAIL walk clip has $frames frames" >&2; exit 1; }

if "$here/render.sh" --animal nosuchcat --fast --out "$out/x" >/dev/null 2>&1; then
  echo "FAIL unknown animal accepted" >&2; exit 1
fi
echo ok
