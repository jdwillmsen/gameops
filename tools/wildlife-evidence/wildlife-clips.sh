#!/usr/bin/env bash
# One command: boots the wildlife pack on a throwaway Bedrock server (RakNet,
# offline), joins a crew unit with the camera on, and records an MP4 of each of
# the six animals and of a tamed tiger walking, sitting and attacking, into an
# evidence bundle under ~/evidence/wildlife/<run-id>/.
#
# The pictures are the crew camera's software re-render of game packets, not a
# real client's pixels; every clip says so on screen. Never point this at the
# live lab or FWB: it only ever talks to the preview server it starts.
set -euo pipefail

self=$(readlink -f "${BASH_SOURCE[0]}")
here=$(dirname "$self")
repo=$(cd "$here/../.." && pwd)
preview=$repo/tools/lab-preview

usage() {
  cat <<'USAGE'
usage: wildlife-clips.sh [options]
  --out DIR          evidence root (default ~/evidence/wildlife)
  --only a,b         record only these clips (default all):
                     tiger,cheetah,lion,black_leopard,brown_bear,hyena,tiger-walk,tiger-sit,tiger-attack
  --seconds N        length of the animal clips (default 12)
  --attach           use the preview server already running under LAB_PREVIEW_NAME /
                     LAB_PREVIEW_PORT instead of booting (and removing) one
  --pw-dir DIR       where playwright-core resolves from (or PW_DIR)
env: LAB_PREVIEW_VERSION (default 1.26.60.6, the lab's), LAB_PREVIEW_NAME, LAB_PREVIEW_PORT
USAGE
}

# Sub-command used by the recorder's --drive: append one timeline line.
if [[ ${1:-} == __ev ]]; then
  shift
  now=$(date +%s%3N)
  python3 -I - "$TL_FILE" "$((now - RUN_EPOCH_MS))" "$@" <<'PY'
import json, sys
path, t, source, event, *kv = sys.argv[1:]
line = {"t_ms": int(t), "source": source, "scenario": __import__("os").environ.get("TL_SCENARIO", ""), "event": event}
for item in kv:
    k, _, v = item.partition("=")
    line[k] = v
open(path, "a").write(json.dumps(line) + "\n")
PY
  exit 0
fi

OUT=$HOME/evidence/wildlife
ONLY=
SECONDS_CLIP=12
ATTACH=0
PW=${PW_DIR:-}
while [[ $# -gt 0 ]]; do
  case $1 in
    --out) OUT=${2:?--out needs a directory}; shift 2 ;;
    --only) ONLY=${2:?--only needs a list}; shift 2 ;;
    --seconds) SECONDS_CLIP=${2:?--seconds needs a number}; shift 2 ;;
    --attach) ATTACH=1; shift ;;
    --pw-dir) PW=${2:?--pw-dir needs a directory}; shift 2 ;;
    -h | --help) usage; exit 0 ;;
    *) echo "unknown argument: $1" >&2; usage >&2; exit 2 ;;
  esac
done
[[ $SECONDS_CLIP =~ ^[0-9]+$ ]] || { echo "error: --seconds must be a whole number" >&2; exit 2; }
[[ -n $PW ]] && export PW_DIR=$PW

export LAB_PREVIEW_NAME=${LAB_PREVIEW_NAME:-wildlife-video}
export LAB_PREVIEW_PORT=${LAB_PREVIEW_PORT:-19151}
export LAB_PREVIEW_VERSION=${LAB_PREVIEW_VERSION:-1.26.60.6}
UNIT_PORT=${WILDLIFE_UNIT_PORT:-18151}
UNIT=http://127.0.0.1:$UNIT_PORT
CLIPS_ALL=(tiger cheetah lion black_leopard brown_bear hyena tiger-walk tiger-sit tiger-attack)
if [[ -z $ONLY ]]; then CLIPS=("${CLIPS_ALL[@]}"); else IFS=, read -ra CLIPS <<<"$ONLY"; fi
for c in "${CLIPS[@]}"; do
  [[ " ${CLIPS_ALL[*]} " == *" $c "* ]] || { echo "error: unknown clip $c" >&2; exit 2; }
done

sha=$(git -C "$repo" rev-parse --short HEAD 2>/dev/null || echo nogit)
run_id=$(date +%Y%m%d-%H%M%S)-$sha
bundle=$OUT/$run_id
mkdir -p "$bundle/videos"
export TL_FILE=$bundle/timeline.jsonl RUN_EPOCH_MS
RUN_EPOCH_MS=$(date +%s%3N)
: >"$TL_FILE"
stage=$(mktemp -d)
unit_pid=
cleanup() {
  [[ -n $unit_pid ]] && kill "$unit_pid" 2>/dev/null || true
  docker logs "$LAB_PREVIEW_NAME" >"$bundle/server.log" 2>&1 || true
  ((ATTACH == 0)) && "$preview" down >/dev/null 2>&1 || true
  rm -rf "$stage"
}
trap cleanup EXIT

ev() { TL_SCENARIO=${SCENARIO:-} "$self" __ev "$@"; }
mc() { local i; for ((i = 0; i < 5; i++)); do "$preview" cmd "$@" >/dev/null 2>&1 && return 0; sleep 2; done; echo "error: console command never went through: $*" >&2; return 1; }
log() { echo "[$(date +%T)] $*" >&2; }

if ((ATTACH == 0)); then
  mkdir -p "$stage/packs/wildlife"
  cp -r "$repo/packs/lab-mods-wildlife/behavior_pack" "$repo/packs/lab-mods-wildlife/resource_pack" "$stage/packs/wildlife/"
  "$preview" down >/dev/null 2>&1 || true
  log "booting the preview server ($LAB_PREVIEW_VERSION, RakNet, offline) with the wildlife pack"
  "$preview" up "$stage/packs" >"$stage/up.log" 2>&1 || { tail -5 "$stage/up.log" >&2; exit 1; }
fi
ev harness preview_up version="$LAB_PREVIEW_VERSION" transport=raknet

log "building and starting the unit with the camera on $UNIT"
(cd "$repo/minecraft/crew" && go build -o "$stage/crew" ./cmd/crew)
mkdir -p "$HOME/.cache/wildlife-evidence/atlas"
MC_HOST=127.0.0.1 MC_PORT=$LAB_PREVIEW_PORT MC_USERNAME=cam MC_OFFLINE=true IDLE_WANDER=false \
  STATUS_ADDR=127.0.0.1:$UNIT_PORT CAMERA_ENABLED=true CAMERA_MAX_VIEWERS=4 \
  CAMERA_PACK_DIR=$repo/packs/lab-mods-wildlife/resource_pack CAMERA_ATLAS_DIR=$HOME/.cache/wildlife-evidence/atlas \
  "$stage/crew" >"$stage/crew.log" 2>&1 &
unit_pid=$!
for ((i = 0; i < 90; i++)); do
  docker logs --since 10m "$LAB_PREVIEW_NAME" 2>&1 | grep -q 'Player Spawned: cam' && break
  kill -0 "$unit_pid" 2>/dev/null || { tail -3 "$stage/crew.log" >&2; echo "error: the unit exited" >&2; exit 1; }
  sleep 1
done
ev bot unit_joined unit=cam

# The vanilla textures are downloaded once and cached; clips drawn before they
# arrive would be flat colours.
for ((i = 0; i < 600; i++)); do
  curl -sf -o /dev/null "$UNIT/v1/camera/atlas.json" && break
  sleep 1
done
((i < 600)) || log "warning: the texture atlas never arrived, so terrain is drawn in flat colours"

# A calm daytime arena: noon, no weather, no wild spawns to crowd the picture.
mc tickingarea add 0 -60 0 48 -60 48 arena
mc gamerule domobspawning false
mc gamerule dodaylightcycle false
mc gamerule doweathercycle false
mc time set 6000
mc weather clear
mc difficulty easy
sleep 3

SPOT="24 -60 24"
reset_arena() {
  mc kill '@e[type=!player]'
  mc tp cam $SPOT
  sleep 2
}
summon() { # species count
  local i
  for ((i = 0; i < $2; i++)); do mc summon "gameops:$1" $((22 + i * 3)) -60 $((26 + i % 2 * 2)); done
}
record() { # name title [recorder args...]
  local name=$1 title=$2
  shift 2
  ev harness record_start clip="$name"
  "$here/record.sh" --unit "$UNIT" --out "$bundle/videos/$name.mp4" --title "$title" --timeline "$TL_FILE" \
    --scenario "$name" --run-epoch-ms "$RUN_EPOCH_MS" "$@" >>"$bundle/recorder.log" || { log "recording $name failed"; return 1; }
  ev harness record_end clip="$name"
}
want() { [[ " ${CLIPS[*]} " == *" $1 "* ]]; }
selfcmd=$(printf '%q' "$self")
# drive builds one step for the recorder's --drive: a timeline line, then a console command.
drive() { # event kv console-command
  printf 'TL_SCENARIO=%q %s __ev harness %q %s; %s cmd %q' "$SCENARIO" "$selfcmd" "$1" "${2:-}" "$(printf '%q' "$preview")" "$3"
}

# Each animal on its own, wild: all variants of the species in view.
for sp in tiger cheetah lion black_leopard brown_bear hyena; do
  want "$sp" || continue
  SCENARIO=$sp
  reset_arena
  case $sp in tiger | lion | hyena) n=3 ;; *) n=1 ;; esac
  summon "$sp" "$n"
  ev harness summoned species="$sp" count="$n"
  sleep 3
  record "$sp" "gameops:$sp (wild, $n in view)" --follow "$sp" --seconds "$SECONDS_CLIP" --dist 5 --el 14 --az 40 --spin 22
done

tame_tiger() {
  reset_arena
  summon tiger 1
  sleep 2
  mc tp cam '@e[type=gameops:tiger,c=1]'
  sleep 2
  mc execute as cam run scriptevent gameops:tame
  ev harness tame_requested method=gameops:tame_hook
  sleep 2
}

if want tiger-walk; then
  SCENARIO=tiger-walk
  tame_tiger
  record tiger-walk "tamed tiger walking: its owner walks away and it follows" --follow tiger --seconds 20 --dist 7 --el 18 --az 70 \
    --drive "for i in 1 2 3 4 5 6; do $(drive owner_moves "blocks=8" 'execute as cam at @s run tp @s ~8 ~ ~'); sleep 2.5; done"
fi

if want tiger-sit; then
  SCENARIO=tiger-sit
  tame_tiger
  ev harness sit_requested method=console_event note="the server flag for sitting is not set by a console event"
  record tiger-sit "tamed tiger sitting (pose forced by the recorder)" --follow tiger --seconds 14 --dist 5 --el 12 --az 60 --spin 14 --sit \
    --drive "$(drive sit_event event=gameops:on_sit 'event entity @e[type=gameops:tiger] gameops:on_sit')"
fi

if want tiger-attack; then
  SCENARIO=tiger-attack
  tame_tiger
  ev harness zombie_summoned note="fire and damage resistant so it lasts"
  record tiger-attack "tamed tiger attacking a zombie" --follow tiger --seconds 16 --dist 6 --el 16 --az 100 \
    --drive "$(drive zombie_summoned '' 'summon zombie 30 -60 24'); sleep 1; $(drive zombie_buffed 'effects=fire_resistance,resistance' 'effect @e[type=zombie] fire_resistance 999 1 true'); $(drive '' '' 'effect @e[type=zombie] resistance 999 4 true')"
fi

python3 -I - "$bundle" "$run_id" "$sha" "$LAB_PREVIEW_VERSION" <<'PY'
import glob, json, os, sys
bundle, run_id, sha, version = sys.argv[1:]
clips = []
for meta in sorted(glob.glob(os.path.join(bundle, "videos", "*.json"))):
    if os.path.basename(meta) == "index.json":
        continue
    m = json.load(open(meta))
    clips.append({"clip": os.path.basename(meta)[:-5], "video": "videos/" + os.path.basename(meta)[:-5] + ".mp4",
                  "captions": "videos/" + os.path.basename(meta)[:-5] + ".srt", "metrics": "videos/" + os.path.basename(meta),
                  "size": f'{m["video"]["width"]}x{m["video"]["height"]}', "seconds": m["video"]["duration_s"],
                  "render_fps_mean": m["render"]["fps_mean"], "render_fps_min": m["render"]["fps_min"]})
json.dump({"run_id": run_id, "commit": sha, "server": {"version": version, "transport": "raknet"}, "clips": clips},
          open(os.path.join(bundle, "videos", "index.json"), "w"), indent=2)
open(os.path.join(bundle, "README.md"), "w").write(f"""# Wildlife camera clips, run {run_id}

Commit {sha}. Server: Bedrock {version} over RakNet, offline, a throwaway preview.

**What the pictures are.** The crew camera's software re-render of the game
packets a crew unit received: Chrome with SwiftShader (no GPU) drew a
three.js scene, and ffmpeg grabbed the window. They are **not** a real
Minecraft client's pixels: no shaders, no UI, no particles, and the
animations are the pack's own animation expressions evaluated by the page, not
the game's engine. Each clip says so in its top banner.

- `videos/<clip>.mp4`: 1280x720, H.264, with the timeline burned in as captions
- `videos/<clip>.srt`: the same captions
- `videos/<clip>.json`: what was measured (rendered frame rate per second, renderer)
- `videos/index.json`: the clips with their frame rates
- `timeline.jsonl`: what the harness did, as `{{t_ms, source, scenario, event}}`
- `server.log`: the preview server's log
- `recorder.log`: one line per clip from the recorder
""")
PY
log "done: $bundle"
echo "$bundle"
