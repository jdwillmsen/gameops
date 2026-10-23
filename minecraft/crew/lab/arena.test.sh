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

echo ok
