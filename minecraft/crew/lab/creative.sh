#!/usr/bin/env bash
# Puts crew units in creative mode on a crew lab, and again whenever one
# rejoins: an offline lab unit is a fresh survival player on every login, and
# `gamemode` is only settable from the server console.
#
# usage: creative.sh <lab> <unit-name>[,<unit-name>...] [--once] [options]
#   <lab> is the compose service of the local lab, or k8s:<namespace>/<pod>
#   for the cluster lab's server pod. Unit names are in-game names (MC_USERNAME).
#   Options pass to minecraft/crew/cmd/labcreative: --once, --poll 3s,
#   --max-duration 30m, --max-commands 100, --confirm-lab <lab>.
#
# It always stops: after --max-duration, after --max-commands gamemode
# commands, or on Ctrl-C. --once makes one pass and exits 3 if a named unit
# was not online. Other targets than the crew lab need --confirm-lab, and the
# FWB server is refused outright. The unit itself needs CREW_LAB=true when its
# role is mender or builder; this script only sets the gamemode.
set -euo pipefail

here=$(cd "$(dirname "$0")" && pwd)
repo=$(cd "$here/../../.." && pwd)

lab=${1:?usage: creative.sh <lab> <unit-name>[,<unit-name>...] [--once] [options]}
units=${2:?usage: creative.sh <lab> <unit-name>[,<unit-name>...] [--once] [options]}
shift 2

cd "$repo"
exec go run ./minecraft/crew/cmd/labcreative --compose "$here/compose.yaml" --lab "$lab" --units "$units" "$@"
