#!/usr/bin/env bash
# Builds a crew test field on a lab from the commands labcourses prints:
#   grounds  work yards east of the spawn hub (x 150..293, z -500..-357)
#   courses  physics, shapes and numbered pads west of it (x -294..-151)
#
# usage: courses.sh [--confirm-overwrite] <lab> grounds|courses
#   <lab> is the compose service of the local lab, or k8s:<namespace>/<pod>
#   for the cluster lab's server pod.
#
# A field replaces everything in its box from the floor up, so a k8s: target
# (a world real players build in) is refused without --confirm-overwrite.
#
# Thousands of commands go straight to the server's stdin: send-command per
# line would take an exec each. The server runs about one command per tick
# and buffers the rest, so the script waits for a logged end marker rather
# than for the write to return.
set -euo pipefail

confirm=false
if [[ ${1:-} == --confirm-overwrite ]]; then
  confirm=true
  shift
fi

usage="usage: courses.sh [--confirm-overwrite] <lab> grounds|courses"
lab=${1:?$usage}
field=${2:?$usage}
here=$(cd "$(dirname "$0")" && pwd)
repo=$(cd "$here/../../.." && pwd)

if [[ $lab == k8s:*/* ]]; then
  if ! $confirm; then
    echo "courses.sh: $lab is a shared world; building $field replaces everything in its box. Re-run with --confirm-overwrite to proceed." >&2
    exit 2
  fi
  target=${lab#k8s:}
  ns=${target%%/*}
  pod=${target#*/}
  exec_prefix=(kubectl -n "$ns" exec "$pod" --)
  copy_in() { kubectl -n "$ns" cp "$1" "$pod:$2"; }
  server_logs() { kubectl -n "$ns" logs "$pod" --since-time="$1"; }
else
  compose=(docker compose -f "$here/compose.yaml")
  exec_prefix=("${compose[@]}" exec -T "$lab")
  copy_in() { "${compose[@]}" cp "$1" "$lab:$2"; }
  server_logs() { "${compose[@]}" logs --no-log-prefix --since "$1" "$lab"; }
fi

cmd() { "${exec_prefix[@]}" send-command "$@" >/dev/null; }

plan=$(mktemp)
trap 'rm -f "$plan"' EXIT
(cd "$repo" && go run ./minecraft/crew/cmd/labcourses "$field") >"$plan"

mapfile -t areas < <(sed -n 's/^# area //p' "$plan")
started=$(date -u +%Y-%m-%dT%H:%M:%SZ)

for i in "${!areas[@]}"; do
  read -r x0 z0 x1 z1 <<<"${areas[$i]}"
  cmd tickingarea add "$x0" 0 "$z0" "$x1" 0 "$z1" "crew-course-$i"
done
cleanup_areas() {
  for i in "${!areas[@]}"; do cmd tickingarea remove "crew-course-$i" || true; done
}
trap 'cleanup_areas; rm -f "$plan"' EXIT

# A loaded column answers testforblock with its coordinates; an unloaded one
# says "outside of the world".
for a in "${areas[@]}"; do
  read -r x0 z0 _ _ <<<"$a"
  for try in $(seq 1 60); do
    cmd testforblock "$x0" 319 "$z0" air
    sleep 2
    if server_logs "$started" | grep -q "at $x0,319,$z0"; then break; fi
    if ((try == 60)); then
      echo "courses.sh: chunks at $x0,$z0 never loaded" >&2
      exit 1
    fi
  done
done

# Entities load after their chunk's blocks; without this pause the pens'
# kill-before-summon misses the old animals and every rebuild adds more.
sleep 15

read -r mx mz _ _ <<<"${areas[0]}"
marker="testforblock $mx 318 $mz air"
{ grep -v '^#' "$plan"; echo "$marker"; } >"$plan.cmds"
lines=$(wc -l <"$plan.cmds")
copy_in "$plan.cmds" /tmp/crew-course.txt
rm -f "$plan.cmds"
# shellcheck disable=SC2016 # expands inside the server container, not here
"${exec_prefix[@]}" sh -c 'PID=$(find /proc -mindepth 2 -maxdepth 2 -name exe -lname "/data/bedrock_server-*" -printf "%h" -quit); cat /tmp/crew-course.txt > "$PID/fd/0"; rm /tmp/crew-course.txt'

# About 20 commands a second; allow a generous margin before giving up.
deadline=$((SECONDS + lines / 5 + 120))
until server_logs "$started" | grep -q "at $mx,318,$mz"; do
  if ((SECONDS > deadline)); then
    echo "courses.sh: end marker not seen after $lines commands" >&2
    exit 1
  fi
  sleep 5
done

# Not failures: "0 blocks filled" (an air-on-air fill), "No targets matched"
# (a first build has no animals to kill), and the probes' unloaded columns.
errors=$(server_logs "$started" | grep ERROR | grep -cv -e '] 0 blocks filled' -e 'outside of the world' -e 'already exists' -e 'No targets matched selector' || true)
echo "courses.sh: built $field ($lines commands, $errors errors)"
((errors == 0))
