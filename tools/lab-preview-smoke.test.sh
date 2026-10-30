#!/usr/bin/env bash
# Checks the pure helpers of lab-preview-smoke with canned output. No docker needed.
set -euo pipefail
here=$(cd "$(dirname "$0")" && pwd)
LAB_PREVIEW_SMOKE_SOURCE_ONLY=1 source "$here/lab-preview-smoke.sh"

expect() { [[ $1 == "$2" ]] || { echo "FAIL $3: got '$1', want '$2'" >&2; exit 1; }; }

expect "$(probe_verdict 'INFO Found Pig')" found found
expect "$(probe_verdict 'ERROR No targets matched selector')" none none
expect "$(probe_verdict '')" unknown empty
expect "$(probe_verdict 'INFO something unrelated')" unknown unrelated

line='gameops:census tiger=1 cheetah=1 lion=1 black_leopard=1 brown_bear=1 hyena=1'
census_ok "$line" 1 || { echo "FAIL census exact" >&2; exit 1; }
for bad in 'tiger=10 cheetah=1 lion=1 black_leopard=1 brown_bear=1 hyena=1' \
  'tiger=1 cheetah=1 lion=1 black_leopard=1 brown_bear=1 hyena=12' \
  'tiger=1 cheetah=1 lion=1 black_leopard=1 brown_bear=1 hyena=0'; do
  if census_ok "gameops:census $bad" 1; then echo "FAIL census accepted: $bad" >&2; exit 1; fi
done
if census_ok "${line//=1/=10}" 1; then echo "FAIL census accepted =10 for 1" >&2; exit 1; fi
census_ok "${line//=1/=10}" 10 || { echo "FAIL census 10" >&2; exit 1; }
echo ok
