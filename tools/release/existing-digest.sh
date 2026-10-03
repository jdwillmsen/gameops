#!/usr/bin/env bash
# Prints the digest an image reference already points at, or nothing when the
# reference does not exist yet. release.yml builds only on empty output: a tag
# that exists is never rebuilt, because a rebuild of the same source still
# produces a different digest and would move the tag under every consumer.
#
# Only output made up entirely of lines that end in a not-found phrase counts.
# A not-found line beside an authorization or any other error aborts, and so
# does one that carries more after the phrase ("not found: unexpected status
# 503"), where the registry was answering something else.
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
elif [[ "$(grep -vciE '(not found|name unknown|manifest unknown)[[:space:]]*$' <<<"$out")" -eq 0 ]] &&
  ! grep -qiE 'authori[sz]|token|denied|unauthenticated|forbidden' <<<"$out"; then
  echo "existing-digest: $ref does not exist yet" >&2
else
  echo "existing-digest: cannot tell whether $ref exists: $out" >&2
  exit 1
fi
