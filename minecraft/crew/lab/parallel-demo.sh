#!/usr/bin/env bash
# Runs the three-unit demo: Agate mines cobblestone into outbox A, Basalt fells
# oak into outbox B, Cobble hauls both into the store. Run parallel-scene.sh
# first.
#
# usage: parallel-demo.sh <compose|k8s:<namespace>> [timeout-s]
#   compose reads the token from $TASKS_TOKEN and the units on 127.0.0.1:8081-8083.
#   k8s:<ns> reads it from Secret crew-tasks (key token) and port-forwards
#   deploy/crew-01..03 to 127.0.0.1:18081-18083.
#
# Prints "<id> <name> <state> <reason> <seconds>" per unit and exits non-zero
# unless all three are done. The token is never printed.
set -euo pipefail

here=$(dirname "$0")
# shellcheck source=parallel-layout.sh
source "$here/parallel-layout.sh"

mode=${1:?usage: parallel-demo.sh <compose|k8s:<namespace>> [timeout-s]}
timeout=${2:-600}
units=(crew-01 crew-02 crew-03)
pids=()

if [[ $mode == compose ]]; then
  : "${TASKS_TOKEN:?export TASKS_TOKEN, the same value compose passed to the units}"
  ports=(8081 8082 8083)
  # Read per call so the token is not stored anywhere but the environment.
  auth() { printf 'Authorization: Bearer %s' "$TASKS_TOKEN"; }
elif [[ $mode == k8s:* ]]; then
  ns=${mode#k8s:}
  ports=(18081 18082 18083)
  cleanup() {
    if ((${#pids[@]})); then kill "${pids[@]}" 2>/dev/null || true; fi
  }
  trap cleanup EXIT
  for i in 0 1 2; do
    kubectl -n "$ns" port-forward "deploy/${units[$i]}" "${ports[$i]}:8080" >/dev/null &
    pids+=($!)
  done
  sleep 3
  auth() {
    printf 'Authorization: Bearer %s' \
      "$(kubectl -n "$ns" get secret crew-tasks -o jsonpath='{.data.token}' | base64 -d)"
  }
else
  echo "parallel-demo.sh: want compose or k8s:<namespace>, got $mode" >&2
  exit 2
fi

# The header goes through stdin so the token is not in the process list.
api() {
  local port=$1 path=$2
  shift 2
  auth | curl -sS --fail-with-body -H @- "$@" "http://127.0.0.1:$port$path"
}

chest() { printf '{"x":%s,"y":%s,"z":%s}' "$1" "$2" "$3"; }
a=$(chest "${OUTBOX_A[@]}")
b=$(chest "${OUTBOX_B[@]}")
s=$(chest "${STORE[@]}")

bodies=(
  "{\"template\":\"gather_and_deposit\",\"item\":\"cobblestone\",\"qty\":16,\"chest\":$a}"
  "{\"template\":\"gather_and_deposit\",\"item\":\"oak_log\",\"qty\":16,\"chest\":$b}"
  "{\"template\":\"haul\",\"items\":{\"cobblestone\":16,\"oak_log\":16},\"from\":[$a,$b],\"to\":$s}"
)

ids=()
start=$(date +%s)
for i in 0 1 2; do
  ids+=("$(api "${ports[$i]}" /v1/tasks -d "${bodies[$i]}" | jq -r .id)")
done

declare -A state reason secs
pending=3
while ((pending > 0)); do
  if (($(date +%s) - start > timeout)); then
    echo "parallel-demo.sh: timed out after ${timeout}s" >&2
    break
  fi
  pending=0
  for i in 0 1 2; do
    [[ -n ${state[$i]:-} ]] && continue
    t=$(api "${ports[$i]}" /v1/tasks | jq -c --arg id "${ids[$i]}" '.[] | select(.id == $id)')
    st=$(jq -r .state <<<"$t")
    case $st in
      done | failed | cancelled)
        state[$i]=$st
        reason[$i]=$(jq -r '.reason // ""' <<<"$t")
        secs[$i]=$(($(date +%s) - start))
        ;;
      *) pending=$((pending + 1)) ;;
    esac
  done
  ((pending > 0)) && sleep 2
done

names=(Agate Basalt Cobble)
ok=true
for i in 0 1 2; do
  st=${state[$i]:-timeout}
  echo "${ids[$i]} ${names[$i]} $st ${reason[$i]:--} ${secs[$i]:-$timeout}"
  [[ $st == "done" ]] || ok=false
done
$ok
