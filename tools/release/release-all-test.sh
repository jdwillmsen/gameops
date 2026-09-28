#!/usr/bin/env bash
# One component's failed release must not stop the others releasing, and the
# run must still end in failure so the broken component gets noticed.
set -uo pipefail
repo="$(git rev-parse --show-toplevel)"
work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT

# The stub records each call and fails for the component sorted first, the
# position that would hide every later one; it also drains stdin, as any
# child that reads it could.
cat > "$work/sr" <<'STUB'
#!/usr/bin/env bash
echo "$RELEASE_COMPONENT $*" >> "$CALLS"
cat > /dev/null
[[ "$RELEASE_COMPONENT" != afkbot ]]
STUB
chmod +x "$work/sr"

cd "$repo"
CALLS="$work/calls" SEMANTIC_RELEASE="$work/sr" tools/release/release-all.sh
status=$?

fail=0
for c in afkbot agent bridge; do
  if grep -q "^$c --tag-format $c-v\${version}$" "$work/calls" 2>/dev/null; then
    echo "ok   $c ran"
  else
    echo "FAIL $c did not run"; fail=1
  fi
done
if [[ $status -eq 0 ]]; then
  echo "FAIL release-all exited 0 although afkbot failed"; fail=1
else
  echo "ok   release-all exited $status"
fi
exit $fail
