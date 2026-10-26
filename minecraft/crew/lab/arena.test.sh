#!/usr/bin/env bash
# Checks arena.sh argument handling without a server: a relative --waves path
# must survive the cd into the module root.
set -euo pipefail
here=$(cd "$(dirname "$0")" && pwd)
fail() { echo "arena.test.sh: $*" >&2; exit 1; }

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
mkdir -p "$tmp/bin" "$tmp/run"
cat >"$tmp/bin/go" <<'EOF'
#!/usr/bin/env bash
printf '%s\n' "$@" >>"$CALL_LOG"
prev=
for a in "$@"; do
  if [[ $prev == --layout ]]; then cp "$a" "$CALL_LOG.layout"; fi
  prev=$a
done
if [[ ${2:-} == ./minecraft/crew/cmd/labcourses ]]; then
  echo '{"layout":true}'
fi
EOF
chmod +x "$tmp/bin/go"
: >"$tmp/waves.yaml"

(cd "$tmp" && CALL_LOG="$tmp/calls" PATH="$tmp/bin:$PATH" "$here/arena.sh" run testlab --waves waves.yaml >/dev/null)

grep -Fxq -- "$tmp/waves.yaml" "$tmp/calls" ||
  fail "relative --waves was not made absolute against the caller's cwd"
grep -Fxq -- "run" "$tmp/calls" || fail "the director never ran"

: >"$tmp/calls"
(cd "$tmp" && CALL_LOG="$tmp/calls" PATH="$tmp/bin:$PATH" "$here/arena.sh" boss k8s:ns/pod --mob warden --log runs/log.jsonl >/dev/null)
grep -Fxq -- "$tmp/runs/log.jsonl" "$tmp/calls" ||
  fail "relative --log was not made absolute against the caller's cwd"
grep -Fxq -- "boss" "$tmp/calls" || fail "boss never reached the director"

rm -f "$tmp/calls.layout"
(cd "$tmp" && CALL_LOG="$tmp/calls" PATH="$tmp/bin:$PATH" "$here/arena.sh" run testlab >/dev/null)
grep -Fxq '{"layout":true}' "$tmp/calls.layout" || fail "without --colosseum the director must get the labcourses layout"

: >"$tmp/calls"
rm -f "$tmp/calls.layout"
(cd "$tmp" && CALL_LOG="$tmp/calls" PATH="$tmp/bin:$PATH" "$here/arena.sh" --colosseum run testlab >/dev/null)
cmp -s "$tmp/calls.layout" "$here/colosseum/layout.json" || fail "--colosseum must hand the director colosseum/layout.json"
if grep -Fxq arena-layout "$tmp/calls"; then fail "--colosseum must not ask labcourses for a layout"; fi

"$here/arena.sh" --colosseum layout | cmp -s - "$here/colosseum/layout.json" || fail "--colosseum layout must print colosseum/layout.json"
"$here/arena.sh" --colosseum --out "$tmp/out.json" layout
cmp -s "$tmp/out.json" "$here/colosseum/layout.json" || fail "--out must receive the colosseum layout"

echo ok
