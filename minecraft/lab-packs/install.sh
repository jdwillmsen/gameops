#!/bin/sh
# Usage: install.sh <world-dir>
# Installs every pack theme under $PACKS_DIR into a Bedrock world and registers
# it. Temp files go next to their target because the caller's root filesystem
# is read-only; only the world dir is writable.
set -eu

packs=${PACKS_DIR:-/packs}
world=${1:?usage: install.sh <world-dir>}

if [ ! -d "$world" ]; then
  # The server creates the world on first boot; the next start installs.
  echo "world $world does not exist yet, nothing installed"
  exit 0
fi

die() { echo "install.sh: $*" >&2; exit 1; }

pending=
cleanup() { [ -z "$pending" ] || rm -rf "$pending"; }
trap cleanup EXIT

# Both lists are checked before anything is written so a corrupt one cannot
# leave the world half-installed.
for list in "$world/world_behavior_packs.json" "$world/world_resource_packs.json"; do
  if [ -s "$list" ]; then
    jq -e 'type == "array"' "$list" >/dev/null 2>&1 || die "$list is not a JSON array; refusing to touch the world"
  fi
done

register() { # list manifest
  list=$1
  manifest=$2
  id=$(jq -er '.header.uuid' "$manifest") || die "$manifest has no header.uuid"
  ver=$(jq -ec '.header.version' "$manifest") || die "$manifest has no header.version"
  [ -s "$list" ] || echo '[]' >"$list"
  pending="$list.new"
  jq --arg id "$id" --argjson ver "$ver" \
    'map(select(.pack_id != $id)) + [{pack_id: $id, version: $ver}]' "$list" >"$list.new" \
    || die "could not update $list"
  mv "$list.new" "$list"
  pending=
}

replace() { # source dest
  pending="$2.new"
  rm -rf "$2.new"
  cp -R "$1" "$2.new"
  rm -rf "$2"
  mv "$2.new" "$2"
  pending=
}

mkdir -p "$world/behavior_packs" "$world/resource_packs"
themes=
for dir in "$packs"/*/; do
  [ -d "$dir" ] || continue
  theme=$(basename "$dir")
  for kind in behavior resource; do
    src=$dir${kind}_pack
    [ -d "$src" ] || continue
    replace "$src" "$world/${kind}_packs/gameops-$theme"
    register "$world/world_${kind}_packs.json" "$src/manifest.json"
  done
  themes="$themes $theme"
done

for theme in $themes; do
  ver=$(jq -er '.header.version | map(tostring) | join(".")' "$packs/$theme/behavior_pack/manifest.json" 2>/dev/null \
    || jq -er '.header.version | map(tostring) | join(".")' "$packs/$theme/resource_pack/manifest.json")
  echo "installed gameops-$theme $ver"
done
