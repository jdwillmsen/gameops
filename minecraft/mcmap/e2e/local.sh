#!/usr/bin/env bash
# Builds the harness, starts it over its made-up world with service sessions
# on and a secret made here and thrown away, and runs the browser checks
# against it. This is what CI runs; it needs Go, Node and a Chromium that
# Playwright can find (npx playwright-core install chromium).
#
#   minecraft/mcmap/e2e/local.sh [arguments for run.mjs]
#
# HARNESS_QUIET=1 runs against a world with nobody in it. HARNESS_WEB=<dir>
# serves another build of the page. E2E_PORT moves the two ports.
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
root="$(cd "$here/../../.." && pwd)"
work="$(mktemp -d)"
port="${E2E_PORT:-18080}"
harness_pid=""
cleanup() {
  [[ -n "$harness_pid" ]] && kill "$harness_pid" 2>/dev/null || true
  rm -rf "$work"
}
trap cleanup EXIT

(cd "$root" && go build -o "$work/harness" ./minecraft/mcmap/e2e/harness </dev/null)
[[ -d "$here/node_modules" ]] || (cd "$here" && npm ci --no-fund --no-audit >/dev/null)

# Made here, kept in a file only this user can read, given to the harness in
# its environment and to the runner by the file's name, and printed by
# nothing. It is of no use beyond this script: the harness dies with it.
(umask 077 && head -c 32 /dev/urandom | od -An -tx1 | tr -d ' \n' > "$work/secret")

SERVICE_SESSION_SECRET="$(cat "$work/secret")" HTTP_ADDR="127.0.0.1:$port" INTERNAL_ADDR="127.0.0.1:$((port + 1000))" \
  "$work/harness" > "$work/harness.log" 2>&1 &
harness_pid=$!
for _ in $(seq 1 100); do
  grep -q '"harness listening"' "$work/harness.log" && break
  kill -0 "$harness_pid" 2>/dev/null || { cat "$work/harness.log" >&2; exit 2; }
  sleep 0.1
done
grep -q '"harness listening"' "$work/harness.log" || { echo "the harness did not start listening" >&2; cat "$work/harness.log" >&2; exit 2; }

status=0
node "$here/run.mjs" --public-url "https://127.0.0.1:$port" --internal-url "http://127.0.0.1:$((port + 1000))" \
  --secret-file "$work/secret" --insecure-local-tls "$@" || status=$?

# The way a run from outside a cluster takes the secret and reaches the
# exchange, with a stand-in for kubectl: it answers the two things the
# runner asks of the real one, from the harness and the made-up secret, and
# fails on anything else, so the runner is shown to ask for nothing more.
if [[ "$status" -eq 0 && -z "${E2E_SKIP_EXTERNAL:-}" ]]; then
  mkdir "$work/bin"
  cat > "$work/bin/kubectl" <<FAKE
#!/bin/sh
case "\$1" in
  get) [ "\$*" = "get secret map-checks -n games -o jsonpath={.data.secret}" ] || exit 64; base64 -w0 "$work/secret" ;;
  port-forward) [ "\$*" = "port-forward -n games service/map :9090 --address 127.0.0.1" ] || exit 64
    echo "Forwarding from 127.0.0.1:$((port + 1000)) -> 9090"; exec sleep 60 ;;
  *) exit 64 ;;
esac
FAKE
  chmod +x "$work/bin/kubectl"
  PATH="$work/bin:$PATH" node "$here/run.mjs" --public-url "https://127.0.0.1:$port" --insecure-local-tls \
    --secret-from-kube games/map-checks/secret --port-forward games/map:9090 --approved-by local-harness \
    --only exchange,map-loads,session,scope || status=$?
fi

# The map's own record of the run: one exchange, from this machine, and
# the secret in none of it.
if grep -qF "$(cat "$work/secret")" "$work/harness.log"; then
  echo "the harness logged the secret" >&2
  status=2
fi
grep -c '"map service session issued"' "$work/harness.log" | sed 's/^/service sessions issued: /'
exit "$status"
