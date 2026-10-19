#!/usr/bin/env bash
# Builds the gather-and-deposit scene on a crew lab: a stone-free platform, a
# quarry and a chest, then puts the crew unit on the platform with a pickaxe.
#
# usage: scene.sh [--confirm-overwrite] <lab> [unit-username]
#   <lab> is the compose service of the local lab, or k8s:<namespace>/<pod>
#   for the cluster lab's server pod. unit-username defaults to crew-1.
#
# The scene erases a 17x8x17 area around the origin (air above a new floor).
# The cluster lab is a world real players build in, so a k8s: target is
# refused without --confirm-overwrite.
#
# The unit is targeted by name, never with @a: @a would teleport real players
# and empty their inventories. Cheats are for this script only; crew itself
# never sends a command.
#
# send-command exits 0 even when the server rejects a command (for example an
# offline unit: "No targets matched selector"), so a zero exit does not prove
# the scene was built. Read the server log.
set -euo pipefail

confirm=false
if [[ ${1:-} == --confirm-overwrite ]]; then
  confirm=true
  shift
fi

lab=${1:?usage: scene.sh [--confirm-overwrite] <lab> [unit-username]}
unit=${2:-crew-1}

# Rejects @-selectors and anything that could break out of the quoted name.
if [[ ! $unit =~ ^[A-Za-z0-9_\ -]+$ ]]; then
  echo "scene.sh: unit name must match [A-Za-z0-9_ -]+ (no selectors)" >&2
  exit 2
fi

if [[ $lab == k8s:*/* ]]; then
  if ! $confirm; then
    echo "scene.sh: $lab is a shared world; this erases a 17x8x17 area around the origin. Re-run with --confirm-overwrite to proceed." >&2
    exit 2
  fi
  target=${lab#k8s:}
  exec_prefix=(kubectl -n "${target%%/*}" exec "${target#*/}" --)
else
  exec_prefix=(docker compose -f "$(dirname "$0")/compose.yaml" exec -T "$lab")
fi

cmd() { "${exec_prefix[@]}" send-command "$@"; }
# Quoted so a name with a space is one selector argument.
quoted="\"$unit\""

# A fill needs loaded chunks, and the lab's spawn can be far from the origin.
cmd tickingarea add -8 99 -8 8 106 8 crew-scene
sleep 5
cmd fill -8 100 -8 8 106 8 air
# Not stone: mining takes the nearest stone, which would be the platform the
# unit stands on rather than the quarry.
cmd fill -8 99 -8 8 99 8 smooth_stone
cmd fill 3 100 -3 6 101 3 stone
cmd setblock -4 100 0 chest
cmd tp "$quoted" 0 100 0
cmd give "$quoted" wooden_pickaxe
cmd clear "$quoted" cobblestone
cmd tickingarea remove crew-scene
