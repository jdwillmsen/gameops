#!/usr/bin/env bash
# Checks creative.sh hands the right arguments to labcreative without running
# Go or a server.
set -euo pipefail
here=$(cd "$(dirname "$0")" && pwd)
fail() { echo "creative.test.sh: $*" >&2; exit 1; }

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
mkdir -p "$tmp/bin"
cat >"$tmp/bin/go" <<'EOF'
#!/usr/bin/env bash
printf '%s\n' "$@" >"$CALL_LOG"
EOF
chmod +x "$tmp/bin/go"

CALL_LOG="$tmp/calls" PATH="$tmp/bin:$PATH" "$here/creative.sh" lab Agate,Basalt --once --poll 5s
for want in ./minecraft/crew/cmd/labcreative "$here/compose.yaml" lab Agate,Basalt --once 5s; do
  grep -Fxq -- "$want" "$tmp/calls" || fail "labcreative never got $want"
done

if "$here/creative.sh" lab 2>/dev/null; then fail "a missing unit list must be refused"; fi
