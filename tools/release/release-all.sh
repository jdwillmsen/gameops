#!/usr/bin/env bash
# Runs semantic-release once per releasable component, in series so their tag
# pushes never race. SEMANTIC_RELEASE overrides the binary for tests.
set -euo pipefail
sr="${SEMANTIC_RELEASE:-tools/release/node_modules/.bin/semantic-release}"

# Read up front and run each release with stdin closed: inside a
# `| while read` loop, a child that read stdin would swallow the components
# still queued behind it.
# A command substitution, not <(...), so a failing tools/components run stops
# the script instead of looking like "nothing to release".
list="$(go run ./tools/components releasable)"
mapfile -t releasable <<<"$list"

# One component's failure must not stop the others releasing -- they are
# independent -- but the job still fails so the broken one is noticed.
failed=()
for line in "${releasable[@]}"; do
  [[ -n "$line" ]] || continue
  read -r name tag <<<"$line"
  if ! RELEASE_COMPONENT="$name" "$sr" --tag-format "${tag}-v\${version}" </dev/null; then
    echo "::error::release failed for $name"
    failed+=("$name")
  fi
done
if ((${#failed[@]})); then
  echo "failed: ${failed[*]}" >&2
  exit 1
fi
