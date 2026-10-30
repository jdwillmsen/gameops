#!/usr/bin/env bash
# Tests install.sh against a fixture world and, when docker is available, the
# built image under the same read-only-root constraints as the chart's init
# container.
set -euo pipefail
here=$(cd "$(dirname "$0")" && pwd)
root=$(cd "$here/../.." && pwd)
tmp=$(mktemp -d)
cleanup() { rm -rf "$tmp"; docker rmi lab-packs-test >/dev/null 2>&1 || true; }
trap cleanup EXIT

fail() { echo "FAIL: $*" >&2; exit 1; }
count() { jq --arg id "$2" '[.[] | select(.pack_id == $id)] | length' "$1"; }
version() { jq -c --arg id "$2" '.[] | select(.pack_id == $id) | .version' "$1"; }

mkpack() { # dir uuid version-json
  mkdir -p "$1"
  printf '{"header":{"uuid":"%s","version":%s},"modules":[]}\n' "$2" "$3" >"$1/manifest.json"
  echo data >"$1/file.txt"
}

packs=$tmp/packs
for t in alpha beta; do
  n=$([ "$t" = alpha ] && echo 1 || echo 2)
  mkpack "$packs/$t/behavior_pack" "b0000000-0000-0000-0000-00000000000$n" "[0,$n,3]"
  mkpack "$packs/$t/resource_pack" "r0000000-0000-0000-0000-00000000000$n" "[0,$n,4]"
  mkdir -p "$packs/$t/behavior_pack/scripts"
  echo js >"$packs/$t/behavior_pack/scripts/real.js"
done

world=$tmp/world
mkdir -p "$world/behavior_packs/gameops-alpha"
echo stale >"$world/behavior_packs/gameops-alpha/stale.txt"
echo '[{"pack_id":"other-uuid","version":[1,0,0]}]' >"$world/world_behavior_packs.json"
echo '[{"pack_id":"alpha-old","version":[1,0,0]}]' >"$world/world_resource_packs.json"

# Red check: the installer does not exist yet.
[ -x "$here/install.sh" ] || fail "install.sh missing or not executable"

out1=$(PACKS_DIR=$packs "$here/install.sh" "$world")
out2=$(PACKS_DIR=$packs "$here/install.sh" "$world")
[ "$out1" = "$out2" ] || fail "output differs between runs"
printf '%s\n' "$out1" | grep -qx 'installed gameops-alpha 0.1.3' || fail "no alpha line: $out1"
printf '%s\n' "$out1" | grep -qx 'installed gameops-beta 0.2.3' || fail "no beta line: $out1"

for pair in "b0000000-0000-0000-0000-000000000001 [0,1,3] world_behavior_packs.json" \
            "b0000000-0000-0000-0000-000000000002 [0,2,3] world_behavior_packs.json" \
            "r0000000-0000-0000-0000-000000000001 [0,1,4] world_resource_packs.json" \
            "r0000000-0000-0000-0000-000000000002 [0,2,4] world_resource_packs.json"; do
  read -r id ver file <<<"$pair"
  [ "$(count "$world/$file" "$id")" = 1 ] || fail "$id not registered exactly once in $file"
  [ "$(version "$world/$file" "$id")" = "$ver" ] || fail "$id has wrong version in $file"
done
[ "$(count "$world/world_behavior_packs.json" other-uuid)" = 1 ] || fail "unrelated behavior entry lost"
[ "$(count "$world/world_resource_packs.json" alpha-old)" = 1 ] || fail "unrelated resource entry lost"
[ "$(jq length "$world/world_behavior_packs.json")" = 3 ] || fail "unexpected behavior list length"

[ -f "$world/behavior_packs/gameops-alpha/file.txt" ] || fail "alpha behavior files missing"
[ -f "$world/resource_packs/gameops-beta/file.txt" ] || fail "beta resource files missing"
[ ! -e "$world/behavior_packs/gameops-alpha/stale.txt" ] || fail "old pack contents not replaced"
for f in "$world"/*.new "$world"/*/*.new; do [ ! -e "$f" ] || fail "temp file left: $f"; done

# Missing world: fail open.
out=$(PACKS_DIR=$packs "$here/install.sh" "$tmp/nope") || fail "missing world exited non-zero"
printf '%s\n' "$out" | grep -q 'does not exist' || fail "missing world gave no message: $out"
[ ! -e "$tmp/nope" ] || fail "missing world was created"

# Corrupt list: fail loudly and touch nothing.
bad=$tmp/bad
mkdir -p "$bad/behavior_packs"
echo '{not json' >"$bad/world_behavior_packs.json"
echo '[]' >"$bad/world_resource_packs.json"
if err=$(PACKS_DIR=$packs "$here/install.sh" "$bad" 2>&1); then fail "corrupt list exited 0"; fi
printf '%s\n' "$err" | grep -q 'world_behavior_packs.json' || fail "corrupt list message unclear: $err"
[ "$(cat "$bad/world_behavior_packs.json")" = '{not json' ] || fail "corrupt list was clobbered"
[ ! -e "$bad/behavior_packs/gameops-alpha" ] || fail "packs installed despite corrupt list"
[ "$(cat "$bad/world_resource_packs.json")" = '[]' ] || fail "resource list changed"

# Empty and absent lists become [].
fresh=$tmp/fresh
mkdir -p "$fresh"
: >"$fresh/world_behavior_packs.json"
PACKS_DIR=$packs "$here/install.sh" "$fresh" >/dev/null
[ "$(jq length "$fresh/world_behavior_packs.json")" = 2 ] || fail "empty list not initialised"
[ "$(jq length "$fresh/world_resource_packs.json")" = 2 ] || fail "absent list not created"

echo "installer: ok"

if ! command -v docker >/dev/null 2>&1 || ! docker info >/dev/null 2>&1; then
  echo "SKIP: docker unavailable, image half not run"
  exit 0
fi

docker build -q -f "$here/Dockerfile" -t lab-packs-test "$root" >/dev/null
imgworld=$tmp/imgworld
mkdir -p "$imgworld/worlds/crew-lab"
chmod -R a+rwX "$imgworld"
imgout=$(docker run --rm --user "$(id -u):$(id -g)" --read-only --cap-drop ALL --memory 64m \
  -v "$imgworld:/data" lab-packs-test /data/worlds/crew-lab)
printf '%s\n' "$imgout" | grep -q '^installed gameops-lab-mods-wildlife ' || fail "image did not install wildlife: $imgout"
printf '%s\n' "$imgout" | grep -q 'stage0' && fail "image installed stage0"
w=$imgworld/worlds/crew-lab
[ -f "$w/behavior_packs/gameops-lab-mods-wildlife/manifest.json" ] || fail "image: wildlife behavior pack missing"
[ -f "$w/resource_packs/gameops-lab-mods-wildlife/manifest.json" ] || fail "image: wildlife resource pack missing"
if find "$w" \( -name '*.test.js' -o -name policy.txt -o -name 'README*' -o -name assets-src \) | grep -q .; then
  fail "image shipped files that should have been pruned"
fi
[ "$(jq length "$w/world_behavior_packs.json")" -ge 1 ] || fail "image: behavior list empty"
echo "image: ok"
