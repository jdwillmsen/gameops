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
work=$(mktemp -d /tmp/wildlife-e2e-XXXXXX)
export WILDLIFE_E2E_WORK=$work
keep=0
for a in "$@"; do [[ $a == --keep ]] && keep=1; done

py=
forward() { [[ -n $py ]] && kill -TERM "$py" 2>/dev/null || true; }
trap forward TERM INT

python3 -B "$here/e2e.py" "$@" &
py=$!
rc=0
wait "$py" || rc=$?
# A forwarded signal ends the first wait early; the run still has to finish cleaning up.
while kill -0 "$py" 2>/dev/null; do wait "$py" || rc=$?; done

# e2e.py cleans up after itself; this covers it dying without the chance (SIGKILL).
if ((rc >= 128 && rc != 130 && rc != 143)); then
  if [[ -f $work/pids ]]; then xargs -r kill <"$work/pids" 2>/dev/null || true; fi
  ((keep)) || LAB_PREVIEW_NAME=$WILDLIFE_E2E_NAME "$here/../lab-preview" down >/dev/null 2>&1 || true
fi
rm -rf "$work"
exit "$rc"
