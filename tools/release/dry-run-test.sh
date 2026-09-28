#!/usr/bin/env bash
# Dry-runs the release glue against a scratch history built from this tree, so
# a change to it is tested without tagging anything real.
set -euo pipefail
# shellcheck source=tools/release/fixture.sh
source "$(dirname "$0")/fixture.sh"

fail=0
expect() {
  local component=$1 want=$2 out
  out="$(RELEASE_COMPONENT=$component "$sr" --dry-run --no-ci --branches main \
    --repository-url "file://$work/remote.git" --tag-format "$component-v\${version}" \
    --plugins ./tools/release/component-filter.mjs 2>&1)" || { echo "$out"; fail=1; return; }
  if grep -q "The next release version is $want" <<<"$out"; then
    echo "ok   $component -> $want"
  else
    echo "FAIL $component: want $want"; grep -E "next release|no relevant|commits affect|triggered|branch" <<<"$out" || tail -5 <<<"$out"; fail=1
  fi
}
# agent: feat (own dir) + deps -> minor. bridge: fix + deps -> patch.
# afkbot: only deps -> patch; the root-only feat must not reach it.
expect agent 1.1.0
expect bridge 1.0.1
expect afkbot 1.0.1
exit $fail
