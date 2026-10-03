#!/usr/bin/env bash
# Only a registry that says "not found" may be read as "build it"; every other
# outcome must either return the existing digest or stop the release.
set -uo pipefail
repo="$(git rev-parse --show-toplevel)"
work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT

cat > "$work/docker" <<'STUB'
#!/usr/bin/env bash
echo "$STUB_OUT"
exit "${STUB_STATUS:-0}"
STUB
chmod +x "$work/docker"

fail=0
digest="sha256:$(printf 'ab%.0s' {1..32})"
expect() {
  local name=$1 want_status=$2 want_out=$3 status out
  shift 3
  out="$(env "$@" DOCKER="$work/docker" "$repo/tools/release/existing-digest.sh" ghcr.io/o/i:1 2>/dev/null)"
  status=$?
  if [[ $status -eq $want_status && "$out" == "$want_out" ]]; then
    echo "ok   $name"
  else
    echo "FAIL $name: status $status (want $want_status), stdout '$out' (want '$want_out')"; fail=1
  fi
}

expect "existing tag prints its digest" 0 "$digest" STUB_OUT="$digest"
expect "missing tag prints nothing" 0 "" STUB_STATUS=1 STUB_OUT="ERROR: ghcr.io/o/i:1: not found"
expect "unknown repository prints nothing" 0 "" STUB_STATUS=1 STUB_OUT="ERROR: name unknown: repository not found"
expect "registry outage stops the release" 1 "" STUB_STATUS=1 STUB_OUT="ERROR: unexpected status 503 Service Unavailable"
expect "auth failure stops the release" 1 "" STUB_STATUS=1 STUB_OUT="ERROR: failed to authorize: 403 Forbidden"
expect "garbage output stops the release" 1 "" STUB_OUT="not a digest"
exit $fail
