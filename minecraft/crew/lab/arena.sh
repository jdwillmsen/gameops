#!/usr/bin/env bash
# Builds the Colosseum on a lab from the commands labcourses prints, prints
# the layout JSON the arena director reads (pens, iron-bar gates, arched gates
# and the squad start gate), or runs the director's waves against a lab.
#
# The Colosseum is centred x 0, z -760 on the already-lit flat square: a
# radius-45 fighting floor, twelve mob pens under the stands, three tiers and
# an outer wall of radius 70 with an arched gate on each side. Every area is
# a rebuilt box, so a k8s: target (a world real players build in) is refused
# without --confirm-overwrite.
#
# usage: arena.sh [--confirm-overwrite] [--plan <file>] build <lab>
#        arena.sh layout [--out <file>]
#        arena.sh run <lab> [--tier N] [--waves <file>] [--warriors <names>]
#                            [--timeout <duration>] [--max-live N] [--poll <d>]
#        arena.sh reset <lab>
#        arena.sh status <lab> [--warriors <names>]
#   <lab> is the compose service of the local lab, or k8s:<namespace>/<pod>
#   for the cluster lab's server pod.
#
# run drives minecraft/crew/cmd/arenadirector: it opens the squad gate, spawns
# each wave in its pens, counts survivors and stops on the boss, a squad wipe
# or a timeout. It prints one JSON result line (duration, waves cleared,
# warrior deaths). reset kills only the director's tagged mobs; status prints
# the live wave, boss and warrior counts. A k8s: run needs --confirm-overwrite
# because it puts buffed mobs into the shared world.
#
# --plan feeds a prebuilt `labcourses arena` output to build instead of
# running the generator from a checkout, for hosts that have no Go toolchain.
#
# Thousands of commands go straight to the server's stdin: send-command per
# line would take an exec each. The server runs about one command per tick
# and buffers the rest, so the script waits for a logged end marker rather
# than for the write to return.
set -euo pipefail

confirm=false
prebuilt=
out=
usage="usage: arena.sh [--confirm-overwrite] [--plan <file>] build <lab> | arena.sh layout [--out <file>] | arena.sh run <lab> [--tier N] [--waves <file>] [--warriors <names>] [--timeout <duration>] [--max-live N] [--poll <d>] | arena.sh reset <lab> | arena.sh status <lab> [--warriors <names>]"
while [[ ${1:-} == --* ]]; do
  case $1 in
    --confirm-overwrite) confirm=true ;;
    --plan)
      prebuilt=${2:?$usage}
      shift
      ;;
    --out)
      out=${2:?$usage}
      shift
      ;;
    *)
      echo "$usage" >&2
      exit 2
      ;;
  esac
  shift
done

action=${1:?$usage}
shift || true
here=$(cd "$(dirname "$0")" && pwd)
repo=$(cd "$here/../../.." && pwd)
go_run() { (cd "$repo" && go run "$@"); }

if [[ $action == layout ]]; then
  if [[ -n $out ]]; then
    go_run ./minecraft/crew/cmd/labcourses arena-layout >"$out"
  else
    go_run ./minecraft/crew/cmd/labcourses arena-layout
  fi
  exit 0
fi

case $action in
  run | reset | status) ;;
  build) ;;
  *)
    echo "$usage" >&2
    exit 2
    ;;
esac

lab=${1:?$usage}
shift || true

if [[ $action != build ]]; then
  case $action in
    run)
      if [[ $lab == k8s:*/* && $confirm != true ]]; then
        echo "arena.sh: $lab is a shared world; a run summons buffed mobs into it. Re-run with --confirm-overwrite to proceed." >&2
        exit 2
      fi
      layout=$(mktemp)
      trap 'rm -f "$layout"' EXIT
      go_run ./minecraft/crew/cmd/labcourses arena-layout >"$layout"
      go_run ./minecraft/crew/cmd/arenadirector run --lab "$lab" --layout "$layout" --compose "$here/compose.yaml" "$@"
      ;;
    reset)
      go_run ./minecraft/crew/cmd/arenadirector reset --lab "$lab" --compose "$here/compose.yaml" "$@"
      ;;
    status)
      go_run ./minecraft/crew/cmd/arenadirector status --lab "$lab" --compose "$here/compose.yaml" "$@"
      ;;
  esac
  exit 0
fi

if [[ $lab == k8s:*/* ]]; then
  if ! $confirm; then
    echo "arena.sh: $lab is a shared world; building the arena replaces everything in its boxes. Re-run with --confirm-overwrite to proceed." >&2
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

# Reads the whole log rather than using grep -q: under pipefail an early exit
# kills the log reader with SIGPIPE, and the failed pipeline reads as no match
# whenever the server keeps logging after the line.
logged() { server_logs "$started" | grep -F "$1" >/dev/null; }

plan=$(mktemp)
trap 'rm -f "$plan"' EXIT
if [[ -n $prebuilt ]]; then
  cp "$prebuilt" "$plan"
else
  (cd "$here/../../.." && go run ./minecraft/crew/cmd/labcourses arena) >"$plan"
fi

mapfile -t areas < <(sed -n 's/^# area //p' "$plan")
started=$(date -u +%Y-%m-%dT%H:%M:%SZ)

for i in "${!areas[@]}"; do
  read -r x0 z0 x1 z1 <<<"${areas[$i]}"
  cmd tickingarea add "$x0" 0 "$z0" "$x1" 0 "$z1" "crew-arena-$i"
done
cleanup_areas() {
  for i in "${!areas[@]}"; do cmd tickingarea remove "crew-arena-$i" || true; done
}
trap 'cleanup_areas; rm -f "$plan"' EXIT

# A loaded column answers testforblock with its coordinates; an unloaded one
# says "outside of the world".
for a in "${areas[@]}"; do
  read -r x0 z0 _ _ <<<"$a"
  for try in $(seq 1 60); do
    cmd testforblock "$x0" 319 "$z0" air
    sleep 2
    if logged "at $x0,319,$z0"; then break; fi
    if ((try == 60)); then
      echo "arena.sh: chunks at $x0,$z0 never loaded" >&2
      exit 1
    fi
  done
done

# The pens are carved air spaces between fills; a pause lets any entity that
# was left in a previous arena settle before the rebuild overwrites its cell.
sleep 10

read -r mx mz _ _ <<<"${areas[0]}"
marker="testforblock $mx 318 $mz air"
{ grep -v '^#' "$plan"; echo "$marker"; } >"$plan.cmds"
lines=$(wc -l <"$plan.cmds")
copy_in "$plan.cmds" /tmp/crew-arena.txt
rm -f "$plan.cmds"
# shellcheck disable=SC2016 # expands inside the server container, not here
"${exec_prefix[@]}" sh -c 'PID=$(find /proc -mindepth 2 -maxdepth 2 -name exe -lname "/data/bedrock_server-*" -printf "%h" -quit); cat /tmp/crew-arena.txt > "$PID/fd/0"; rm /tmp/crew-arena.txt'

# About 20 commands a second; allow a generous margin before giving up.
deadline=$((SECONDS + lines / 5 + 120))
until logged "at $mx,318,$mz"; do
  if ((SECONDS > deadline)); then
    echo "arena.sh: end marker not seen after $lines commands" >&2
    exit 1
  fi
  sleep 5
done

# Not failures: "0 blocks filled" where a rebuild lays the same block twice,
# and the probes' unloaded columns.
errors=$(server_logs "$started" | grep ERROR | grep -cv -e '] 0 blocks filled' -e 'outside of the world' -e 'already exists' || true)
echo "arena.sh: built arena ($lines commands, $errors errors)"
((errors == 0))
