#!/usr/bin/env bash
# Checks the scene holds more than the tasks ask for, and stays inside the
# cleared box.
set -euo pipefail
here=$(dirname "$0")
# shellcheck source=parallel-layout.sh
source "$here/parallel-layout.sh"

logs=$((${#GROVE_X[@]} * ${#GROVE_Z[@]} * 2))
((logs >= 20)) || { echo "grove has $logs logs, want at least 20" >&2; exit 1; }
quarry=$(((QUARRY_TO[0] - QUARRY_FROM[0] + 1) * (QUARRY_TO[1] - QUARRY_FROM[1] + 1) * (QUARRY_TO[2] - QUARRY_FROM[2] + 1)))
((quarry >= 24)) || { echo "quarry has $quarry blocks, want at least 24" >&2; exit 1; }
for x in "${GROVE_X[@]}"; do
  ((x >= AREA_MIN[0] && x <= AREA_MAX[0])) || { echo "grove x $x outside the area" >&2; exit 1; }
done
for z in "${GROVE_Z[@]}"; do
  ((z >= AREA_MIN[2] && z <= AREA_MAX[2])) || { echo "grove z $z outside the area" >&2; exit 1; }
done
for i in 0 1 2; do
  ((KIT_CHEST[i] >= AREA_MIN[i] && KIT_CHEST[i] <= AREA_MAX[i])) || { echo "kit chest axis $i outside the area" >&2; exit 1; }
done
((KIT_CHEST[1] == 100)) || { echo "kit chest must stand on the floor at y=100" >&2; exit 1; }
((KIT_SWORDS <= 26 && KIT_BREAD <= 64)) || { echo "kit stock does not fit a chest slot" >&2; exit 1; }
for taken in "${OUTBOX_A[*]}" "${OUTBOX_B[*]}" "${STORE[*]}" "${START_AGATE[*]}" "${START_BASALT[*]}" "${START_COBBLE[*]}"; do
  [[ $taken != "${KIT_CHEST[*]}" ]] || { echo "kit chest shares a cell with another feature" >&2; exit 1; }
done
echo ok
