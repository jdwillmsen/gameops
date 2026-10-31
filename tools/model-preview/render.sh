#!/usr/bin/env bash
# Renders turntables, animation loops and contact sheets of the wildlife pack
# models. See README.md. Needs node, ffmpeg (libx264) and Chrome.
set -euo pipefail
here=$(cd "$(dirname "$0")" && pwd)

for tool in node ffmpeg npm; do
  command -v "$tool" >/dev/null || { echo "render.sh: $tool is required" >&2; exit 1; }
done
[[ -x ${CHROME:-/opt/google/chrome/chrome} ]] || { echo "render.sh: Chrome not found, set CHROME=/path/to/chrome" >&2; exit 1; }

if [[ ! -d $here/node_modules/playwright-core ]]; then
  (cd "$here" && npm ci --no-audit --no-fund --silent)
fi

exec node "$here/render.mjs" "$@"
