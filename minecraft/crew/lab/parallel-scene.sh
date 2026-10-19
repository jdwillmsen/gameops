#!/usr/bin/env bash
# Builds the parallel scene on a crew lab: a quarry for Agate, a grove for
# Basalt, an outbox chest for each, a store chest for Cobble's haul and a kit
# chest of swords and bread for units started with KIT_CHEST at its position.
#
# usage: parallel-scene.sh [--confirm-overwrite] <lab> [agate] [basalt] [cobble]
#   <lab> is the compose service of the local lab, or k8s:<namespace>/<pod>
#   for the cluster lab's server pod. Names default to Agate, Basalt, Cobble.
#
# The scene erases a 25x8x25 area around the origin (air above a new floor).
# The cluster lab is a world real players build in, so a k8s: target is
# refused without --confirm-overwrite.
#
# Units are targeted by name, never with @a: @a would teleport real players
# and empty their inventories. Cheats are for this script only; crew itself
# never sends a command.
#
# send-command exits 0 even when the server rejects a command (for example an
# offline unit: "No targets matched selector"), so a zero exit does not prove
# the scene was built. Read the server log.
set -euo pipefail

here=$(dirname "$0")
# shellcheck source=parallel-layout.sh
source "$here/parallel-layout.sh"

usage="usage: parallel-scene.sh [--confirm-overwrite] <lab> [agate] [basalt] [cobble]"
confirm=false
if [[ ${1:-} == --confirm-overwrite ]]; then
  confirm=true
  shift
fi

lab=${1:?$usage}
agate=${2:-${DEFAULT_NAMES[0]}}
basalt=${3:-${DEFAULT_NAMES[1]}}
cobble=${4:-${DEFAULT_NAMES[2]}}

# Rejects @-selectors, anything that could break out of the quoted name and
# names the server would not accept as a username.
for n in "$agate" "$basalt" "$cobble"; do
  if [[ ! $n =~ ^[A-Za-z0-9_\ -]+$ ]]; then
    echo "parallel-scene.sh: unit name must match [A-Za-z0-9_ -]+ (no selectors)" >&2
    exit 2
  fi
  if ((${#n} > 16)); then
    echo "parallel-scene.sh: unit name '$n' is over the 16-character Bedrock limit" >&2
    exit 2
  fi
done

if [[ $lab == k8s:*/* ]]; then
  if ! $confirm; then
    echo "parallel-scene.sh: $lab is a shared world; this erases a 25x8x25 area around the origin. Re-run with --confirm-overwrite to proceed." >&2
    exit 2
  fi
  target=${lab#k8s:}
  exec_prefix=(kubectl -n "${target%%/*}" exec "${target#*/}" --)
else
  exec_prefix=(docker compose -f "$here/compose.yaml" exec -T "$lab")
fi

cmd() { "${exec_prefix[@]}" send-command "$@"; }

# A fill needs loaded chunks, and the lab's spawn can be far from the origin.
cmd tickingarea add "${AREA_MIN[0]}" "${AREA_MIN[1]}" "${AREA_MIN[2]}" \
  "${AREA_MAX[0]}" "${AREA_MAX[1]}" "${AREA_MAX[2]}" crew-parallel
sleep 5
cmd fill "${AREA_MIN[0]}" 100 "${AREA_MIN[2]}" "${AREA_MAX[0]}" "${AREA_MAX[1]}" "${AREA_MAX[2]}" air
# Not stone: mining takes the nearest stone, which would be the floor the
# units stand on rather than the quarry.
cmd fill "${AREA_MIN[0]}" 99 "${AREA_MIN[2]}" "${AREA_MAX[0]}" 99 "${AREA_MAX[2]}" smooth_stone
cmd fill "${QUARRY_FROM[@]}" "${QUARRY_TO[@]}" stone
for x in "${GROVE_X[@]}"; do
  for z in "${GROVE_Z[@]}"; do
    cmd fill "$x" 100 "$z" "$x" 101 "$z" oak_log
  done
done
cmd setblock "${OUTBOX_A[@]}" chest
cmd setblock "${OUTBOX_B[@]}" chest
cmd setblock "${STORE[@]}" chest
cmd setblock "${KIT_CHEST[@]}" chest
# Swords do not stack, so each takes a slot. setblock replaces an old chest,
# which empties it.
for ((slot = 0; slot < KIT_SWORDS; slot++)); do
  cmd replaceitem block "${KIT_CHEST[@]}" slot.container "$slot" stone_sword 1
done
cmd replaceitem block "${KIT_CHEST[@]}" slot.container "$KIT_SWORDS" bread "$KIT_BREAD"

# Cleared first so a rerun never leaves a unit holding last run's stack.
cmd clear "\"$agate\""
cmd clear "\"$basalt\""
cmd clear "\"$cobble\""
cmd tp "\"$agate\"" "${START_AGATE[@]}"
cmd tp "\"$basalt\"" "${START_BASALT[@]}"
cmd tp "\"$cobble\"" "${START_COBBLE[@]}"
cmd give "\"$agate\"" wooden_pickaxe
# A wooden axe takes about 30 ticks per log, the whole break budget; a stone
# axe takes about 15.
cmd give "\"$basalt\"" stone_axe
cmd tickingarea remove crew-parallel
