#!/usr/bin/env bash
# A tag semantic-release has already pushed must reach the publish job even
# when a later plugin fails -- in production, creating the GitHub release --
# or it names an image no registry has. Runs a real release (not a dry run)
# into the fixture's local remote with the GitHub plugin swapped for one that
# always fails.
set -euo pipefail
# shellcheck source=tools/release/fixture.sh
source "$(dirname "$0")/fixture.sh"

cat > fail-publish.mjs <<'MJS'
export const publish = () => { throw new Error('simulated GitHub release failure'); };
MJS
jq '.plugins |= map(if (type == "array" and .[0] == "@semantic-release/github") then "./fail-publish.mjs" else . end)' \
  .releaserc.json > releaserc.test.json
mv releaserc.test.json .releaserc.json
git add -A && git commit -qm "chore: swap github for a failing publish" && git push -q origin main

released="$work/released"
: > "$released"
if RELEASE_COMPONENT=agent RELEASED_FILE="$released" "$sr" --no-ci --branches main \
  --repository-url "file://$work/remote.git" --tag-format 'agent-v${version}' >"$work/log" 2>&1; then
  echo "FAIL the simulated publish failure did not fail the release"; exit 1
fi
fail=0
if git ls-remote --tags "$work/remote.git" | grep -q 'refs/tags/agent-v1.1.0$'; then
  echo "ok   agent-v1.1.0 was pushed"
else
  echo "FAIL agent-v1.1.0 was not pushed"; tail -20 "$work/log"; fail=1
fi
if grep -qx 'agent agent-v1.1.0' "$released"; then
  echo "ok   agent-v1.1.0 was recorded for publishing"
else
  echo "FAIL agent-v1.1.0 was pushed but not recorded; RELEASED_FILE: $(cat "$released")"; fail=1
fi
exit $fail
