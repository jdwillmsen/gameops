#!/usr/bin/env bash
# Builds the gather-and-deposit scene on a crew lab: a stone platform, a quarry
# and a chest, then puts the crew unit on the platform with a pickaxe.
#
# usage: scene.sh <lab-service> [unit-username]   (unit defaults to crew-1)
#
# The unit is targeted by name, never with @a: the cluster lab is a world real
# players join, and @a would teleport them and empty their inventories. Cheats
# are for this script only; crew itself never sends a command.
set -euo pipefail

service=${1:?usage: scene.sh <lab-service> [unit-username]}
unit=${2:-crew-1}
compose=(docker compose -f "$(dirname "$0")/compose.yaml")

cmd() { "${compose[@]}" exec -T "$service" send-command "$@"; }

# A fill needs loaded chunks, and the lab's spawn can be far from the origin.
cmd tickingarea add -8 99 -8 8 106 8 crew-scene
sleep 5
cmd fill -8 100 -8 8 106 8 air
# Not stone: mining takes the nearest stone, which would be the platform the
# unit stands on rather than the quarry.
cmd fill -8 99 -8 8 99 8 smooth_stone
cmd fill 3 100 -3 6 101 3 stone
cmd setblock -4 100 0 chest
cmd tp "$unit" 0 100 0
cmd give "$unit" wooden_pickaxe
cmd clear "$unit" cobblestone
