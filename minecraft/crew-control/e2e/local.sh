#!/usr/bin/env bash
# Builds crew-control, starts it with a throwaway operator and a made-up
# invite, and runs the sign-in browser test against it with a virtual
# authenticator. This is what CI runs; it needs Go, Node, openssl and a
# Chromium that Playwright can find (npx playwright-core install chromium).
#
#   minecraft/crew-control/e2e/local.sh
#
# E2E_PORT moves the two ports it uses (default 18480 and the next one).
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
root="$(cd "$here/../../.." && pwd)"
work="$(mktemp -d)"
port="${E2E_PORT:-18480}"
pid=""
cleanup() {
  [[ -n "$pid" ]] && kill "$pid" 2>/dev/null || true
  rm -rf "$work"
}
trap cleanup EXIT

# A leftover server on the port would answer for ours and make the run lie.
if (exec 3<>"/dev/tcp/127.0.0.1/$port") 2>/dev/null; then echo "port $port is in use; set E2E_PORT" >&2; exit 2; fi

(cd "$root" && go build -o "$work/crew-control" ./minecraft/crew-control/cmd/crew-control </dev/null)
[[ -d "$here/node_modules" ]] || (cd "$here" && npm ci --no-fund --no-audit >/dev/null)

mkdir "$work/data" "$work/invites"
# Made here, readable by this user only, printed by nothing, and of no use
# once this script ends: the server and its data go with it.
(umask 077 && head -c 24 /dev/urandom | od -An -tx1 | tr -d ' \n' > "$work/invites/owner")
openssl req -x509 -newkey rsa:2048 -nodes -days 1 -subj /CN=crew.test \
  -addext subjectAltName=DNS:crew.test -keyout "$work/key.pem" -out "$work/cert.pem" 2>/dev/null

PUBLIC_ORIGIN=https://crew.test CREW_OPERATORS='[{"id":"owner","name":"Owner"}]' \
  CREW_INVITES_DIR="$work/invites" DATA_DIR="$work/data" LISTEN_ADDR="127.0.0.1:$port" \
  CREW_UNITS='[{"id":"crew-01","name":"Agate","url":"http://127.0.0.1:9"}]' WORK_STORE=memory \
  "$work/crew-control" > "$work/server.log" 2>&1 &
pid=$!
for _ in $(seq 1 100); do
  curl -sf "http://127.0.0.1:$port/healthz" >/dev/null && break
  kill -0 "$pid" 2>/dev/null || { cat "$work/server.log" >&2; exit 2; }
  sleep 0.1
done
curl -sf "http://127.0.0.1:$port/healthz" >/dev/null || { echo "crew-control did not start" >&2; cat "$work/server.log" >&2; exit 2; }

status=0
(cd "$here" && E2E_UPSTREAM="127.0.0.1:$port" E2E_TLS_PORT="$((port + 1))" E2E_WORK="$work" npm test) || status=$?
# The log names events and never a code or credential, so it is safe to show
# when the run failed.
[[ "$status" -eq 0 ]] || { echo "--- server log ---" >&2; cat "$work/server.log" >&2; }
exit "$status"
