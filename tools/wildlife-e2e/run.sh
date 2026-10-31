#!/usr/bin/env bash
# One command: builds the crew bot, boots a throwaway preview, runs the wildlife
# scenarios with two offline bots, writes the evidence bundle and removes everything.
# usage: tools/wildlife-e2e/run.sh [--only ID-PREFIX]... [--list] [--keep]
set -euo pipefail
here=$(cd "$(dirname "$0")" && pwd)

for need in docker go python3; do
  command -v "$need" >/dev/null || { echo "error: $need is required" >&2; exit 2; }
done

export WILDLIFE_E2E_NAME=${WILDLIFE_E2E_NAME:-wildlife-e2e}
cleanup() {
  # e2e.py cleans up after itself; this covers it being killed outright.
  pkill -f "wildlife-e2e-[a-zA-Z0-9_]*/crew" 2>/dev/null || true
  if [[ " $* " != *" --keep "* ]]; then
    LAB_PREVIEW_NAME=$WILDLIFE_E2E_NAME "$here/../lab-preview" down >/dev/null 2>&1 || true
  fi
}
trap 'cleanup "$@"' EXIT
python3 -B "$here/e2e.py" "$@"
