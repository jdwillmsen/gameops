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

# Everything is checked before anything is written so a corrupt list or
# manifest cannot leave the world half-installed or a pack replaced but
# unregistered.
for list in "$world/world_behavior_packs.json" "$world/world_resource_packs.json"; do
  if [ -s "$list" ]; then
    jq -e 'type == "array" and all(.[]; type == "object")' "$list" >/dev/null 2>&1 \
      || die "$list is not a JSON array of objects; refusing to touch the world"
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

# A SIGTERM/SIGKILL mid-install can leave a "<dest>.new" folder (ash does not
# run the EXIT trap on SIGTERM); the next run removes it by name. Nothing reads
# it meanwhile because the server has not started yet when this runs.
replace() { # source dest
  pending="$2.new"
  rm -rf "$2.new"
  cp -R "$1" "$2.new"
  rm -rf "$2"
  mv "$2.new" "$2"
  pending=
}

themes=
for dir in "$packs"/*/; do
  [ -d "$dir" ] || continue
  name=$(basename "$dir")
  # The name lands in rm -rf paths below, so only plain names are allowed to reach them.
  case $name in
    *[!A-Za-z0-9._-]*)
      echo "install.sh: skipping theme '$name': names may only use letters, digits, . _ -" >&2
      continue ;;
  esac
  found=
  for kind in behavior resource; do
    manifest=$dir${kind}_pack/manifest.json
    [ -d "$dir${kind}_pack" ] || continue
    jq -e '(.header.uuid | type == "string") and (.header.version | type == "array")' "$manifest" >/dev/null 2>&1 \
      || die "$manifest needs header.uuid (string) and header.version (array)"
    found=1
  done
  [ -z "$found" ] || themes="$themes $name"
done
# $themes is split on spaces below; with globbing off a name can never expand to other paths.
set -f

mkdir -p "$world/behavior_packs" "$world/resource_packs"
for theme in $themes; do
  for kind in behavior resource; do
    src=$packs/$theme/${kind}_pack
    [ -d "$src" ] || continue
    replace "$src" "$world/${kind}_packs/gameops-$theme"
    register "$world/world_${kind}_packs.json" "$src/manifest.json"
  done
done

for theme in $themes; do
  for kind in behavior resource; do
    manifest=$packs/$theme/${kind}_pack/manifest.json
    [ -f "$manifest" ] || continue
    echo "installed gameops-$theme $(jq -r '.header.version | map(tostring) | join(".")' "$manifest")"
    break
  done
done
