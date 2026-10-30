#!/usr/bin/env bash
# Tests install.sh against a fixture world and, when docker is available, the
# built image under the same read-only-root constraints as the chart's init
# container.
set -euo pipefail
here=$(cd "$(dirname "$0")" && pwd)
root=$(cd "$here/../.." && pwd)
tmp=$(mktemp -d)
cleanup() { rm -rf "$tmp" 2>/dev/null; docker rmi lab-packs-test >/dev/null 2>&1 || true; }
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

# A list holding a non-object is rejected before anything is written.
odd=$tmp/odd
mkdir -p "$odd"
echo '[1]' >"$odd/world_resource_packs.json"
if err=$(PACKS_DIR=$packs "$here/install.sh" "$odd" 2>&1); then fail "non-object list exited 0"; fi
printf '%s\n' "$err" | grep -q 'world_resource_packs.json' || fail "non-object message unclear: $err"
[ ! -e "$odd/behavior_packs" ] || fail "world touched despite non-object list"

# A bad manifest in any theme stops the run before any pack is replaced.
badpacks=$tmp/badpacks
cp -R "$packs" "$badpacks"
mkpack "$badpacks/zeta/behavior_pack" x '[1,0,0]'
echo '{"header":{"version":[1,0,0]}}' >"$badpacks/zeta/behavior_pack/manifest.json"
bw=$tmp/badmanifest
mkdir -p "$bw"
if err=$(PACKS_DIR=$badpacks "$here/install.sh" "$bw" 2>&1); then fail "bad manifest exited 0"; fi
printf '%s\n' "$err" | grep -q 'zeta/behavior_pack/manifest.json' || fail "bad manifest message unclear: $err"
[ ! -e "$bw/behavior_packs/gameops-alpha" ] || fail "pack replaced despite bad manifest"
[ ! -e "$bw/world_behavior_packs.json" ] || fail "list written despite bad manifest"

# A theme folder with no packs is skipped, not reported.
emptypacks=$tmp/emptypacks
cp -R "$packs" "$emptypacks"
mkdir -p "$emptypacks/hollow"
ew=$tmp/emptyworld
mkdir -p "$ew"
out=$(PACKS_DIR=$emptypacks "$here/install.sh" "$ew" 2>&1) || fail "empty theme failed: $out"
printf '%s\n' "$out" | grep -q hollow && fail "empty theme reported: $out"
[ "$(printf '%s\n' "$out" | grep -c '^installed ')" = 2 ] || fail "expected two installed lines: $out"

# A theme folder whose name is not plain is reported and skipped: the name ends up in rm -rf paths
# and a space or glob character would split or expand it.
oddpacks=$tmp/oddpacks
cp -R "$packs" "$oddpacks"
for name in 'bad name' 'gl*b' '$(touch pwned)'; do
  mkpack "$oddpacks/$name/behavior_pack" "c0000000-0000-0000-0000-000000000009" "[0,9,9]"
done
ow=$tmp/oddworld
mkdir -p "$ow/behavior_packs/precious" "$ow/resource_packs"
echo keep >"$ow/behavior_packs/precious/keep.txt"
out=$(cd "$tmp" && PACKS_DIR=$oddpacks "$here/install.sh" "$ow" 2>"$tmp/odd.err") || fail "odd theme names failed: $(cat "$tmp/odd.err")"
for name in 'bad name' 'gl*b' '$(touch pwned)'; do
  grep -qF "skipping theme '$name'" "$tmp/odd.err" || fail "odd theme '$name' not reported: $(cat "$tmp/odd.err")"
done
[ "$(printf '%s\n' "$out" | grep -c '^installed ')" = 2 ] || fail "odd names were installed: $out"
[ -f "$ow/behavior_packs/precious/keep.txt" ] || fail "unrelated world pack was removed"
[ ! -e "$tmp/pwned" ] || fail "theme name was executed"

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
flags="--rm --read-only --cap-drop ALL --security-opt no-new-privileges --memory 64m --network none"

# Builds <dir>/worlds/crew-lab owned by $2 (uid:gid), mode 755, from a
# throwaway root container with default capabilities.
mkworld() {
  mkdir -p "$1"
  docker run --rm -v "$1:/data" --entrypoint sh lab-packs-test -c \
    "mkdir -p /data/worlds/crew-lab && chown -R $2 /data && chmod -R 755 /data"
}
verify() {
  w=$1/worlds/crew-lab
  printf '%s\n' "$2" | grep -q '^installed gameops-lab-mods-wildlife ' || fail "image did not install wildlife: $2"
  printf '%s\n' "$2" | grep -q 'stage0' && fail "image installed stage0"
  [ -f "$w/behavior_packs/gameops-lab-mods-wildlife/manifest.json" ] || fail "image: wildlife behavior pack missing"
  [ -f "$w/resource_packs/gameops-lab-mods-wildlife/manifest.json" ] || fail "image: wildlife resource pack missing"
  if find "$w" \( -name '*.test.js' -o -name policy.txt -o -name 'README*' -o -name assets-src \) | grep -q .; then
    fail "image shipped files that should have been pruned"
  fi
  [ "$(jq length "$w/world_behavior_packs.json")" -ge 1 ] || fail "image: behavior list empty"
}
# The helper containers leave root-owned files, so removal also goes through one.
rmworld() { docker run --rm -v "$1:/data" --entrypoint sh lab-packs-test -c 'rm -rf /data/* /data/.[!.]*' || true; }

# (a) the image's default user (root), caps dropped, root-owned world. The
# live pod runs as uid 1000 / gid 3000 (case b); this case keeps root coverage
# for other callers of the image.
a=$tmp/a; mkworld "$a" 0:0
# shellcheck disable=SC2086
out=$(docker run $flags -v "$a:/data" lab-packs-test /data/worlds/crew-lab) || fail "(a) root on root-owned world failed: $out"
verify "$a" "$out"; echo "(a) PASS: default user, root-owned 755 world"
rmworld "$a"

# (b) the lab's real shape: pod runs uid 1000 / gid 3000, the world is owned
# 1000:2000 with setgid directories (2775) and 644 files.
b=$tmp/b; mkworld "$b" 1000:2000
docker run --rm -v "$b:/data" --entrypoint sh lab-packs-test -c \
  "echo '[]' >/data/worlds/crew-lab/world_behavior_packs.json && chown -R 1000:2000 /data && find /data -type d -exec chmod 2775 {} + && find /data -type f -exec chmod 644 {} +"
# shellcheck disable=SC2086
out=$(docker run $flags --user 1000:3000 -v "$b:/data" lab-packs-test /data/worlds/crew-lab) || fail "(b) uid 1000:3000 on the lab-shaped world failed: $out"
verify "$b" "$out"
owner=$(docker run --rm -v "$b:/data" --entrypoint stat lab-packs-test -c '%u' /data/worlds/crew-lab/behavior_packs/gameops-lab-mods-wildlife/manifest.json /data/worlds/crew-lab/world_behavior_packs.json | sort -u)
[ "$owner" = 1000 ] || fail "(b) installed files not owned by uid 1000: $owner"
echo "(b) PASS: --user 1000:3000, 1000:2000-owned setgid 2775 world, files owned by uid 1000"
rmworld "$b"

# (c) informational: root without CAP_DAC_OVERRIDE cannot write another
# uid's 755 files, so the chart's init container user must own the world.
c=$tmp/c; mkworld "$c" 1000:1000
# shellcheck disable=SC2086
if out=$(docker run $flags -v "$c:/data" lab-packs-test /data/worlds/crew-lab 2>&1); then
  echo "(c) PASS: root with caps dropped could write a 1000-owned 755 world: $out"
else
  echo "(c) EXPECTED-FAIL: root with caps dropped on a 1000-owned 755 world: $out"
fi
rmworld "$c"
echo "image: ok"
