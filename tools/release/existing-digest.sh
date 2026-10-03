#!/usr/bin/env bash
# Prints the digest an image reference already points at, or nothing when the
# reference does not exist yet. release.yml builds only on empty output: a tag
# that exists is never rebuilt, because a rebuild of the same source still
# produces a different digest and would move the tag under every consumer.
#
# Any failure other than "not found" aborts instead of printing nothing, so a
# registry hiccup can never be mistaken for a missing image and trigger a
# rebuild. DOCKER overrides the binary for tests.
set -uo pipefail
ref="${1:?usage: existing-digest.sh <image:tag>}"
docker="${DOCKER:-docker}"

if out="$("$docker" buildx imagetools inspect "$ref" --format '{{.Manifest.Digest}}' 2>&1)"; then
  if [[ ! "$out" =~ ^sha256:[0-9a-f]{64}$ ]]; then
    echo "existing-digest: $ref resolved to an unexpected digest: $out" >&2
    exit 1
  fi
  echo "$out"
elif grep -qiE 'not found|name unknown|manifest unknown' <<<"$out"; then
  echo "existing-digest: $ref does not exist yet" >&2
else
  echo "existing-digest: cannot tell whether $ref exists: $out" >&2
  exit 1
fi
