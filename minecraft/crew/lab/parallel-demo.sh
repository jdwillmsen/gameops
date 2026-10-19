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
# unless all three are done. The token is never printed or written to disk.
# On a timeout, an error or INT/TERM, every task still running is cancelled.
set -euo pipefail

here=$(dirname "$0")
# shellcheck source=parallel-layout.sh
source "$here/parallel-layout.sh"

mode=${1:?usage: parallel-demo.sh <compose|k8s:<namespace>> [timeout-s]}
timeout=${2:-600}
units=(crew-01 crew-02 crew-03)
names=(Agate Basalt Cobble)
pids=()
ids=()
declare -A state reason secs fails

die() {
  echo "parallel-demo.sh: $*" >&2
  exit 1
}

# The header goes through stdin so the token is not in the process list; the
# token itself lives only in this shell variable.
api() {
  local port=$1 path=$2
  shift 2
  printf 'Authorization: Bearer %s' "$token" |
    curl -sS --fail-with-body --max-time 10 -H @- "$@" "http://127.0.0.1:$port$path"
}

# Stops every task still running, then the port-forwards, so a failed or
# interrupted demo leaves no unit working. Invoked through the traps.
# shellcheck disable=SC2317
cleanup() {
  trap - EXIT INT TERM
  local i
  for i in 0 1 2; do
    if [[ -n ${ids[$i]:-} && ( -z ${state[$i]:-} || ${state[$i]} == unreachable ) ]]; then
      api "${ports[$i]}" "/v1/tasks/${ids[$i]}/cancel" -X POST >/dev/null 2>&1 || true
    fi
  done
  if ((${#pids[@]})); then kill "${pids[@]}" 2>/dev/null || true; fi
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

portbusy() { (exec 3<>"/dev/tcp/127.0.0.1/$1") 2>/dev/null; }

# Waits for the forward to answer the unauthenticated probe while its process
# is alive, so the token is only ever sent to our own forward.
awaitforward() {
  local port=$1 pid=$2 deadline=$((SECONDS + 20)) code
  while ((SECONDS < deadline)); do
    kill -0 "$pid" 2>/dev/null || die "port-forward on $port exited"
    code=$(curl -s -o /dev/null -w '%{http_code}' --max-time 2 "http://127.0.0.1:$port/healthz" || true)
    if [[ $code == 200 ]] && kill -0 "$pid" 2>/dev/null; then return 0; fi
    sleep 0.5
  done
  die "port-forward on $port not ready after 20s"
}

if [[ $mode == compose ]]; then
  : "${TASKS_TOKEN:?export TASKS_TOKEN, the same value compose passed to the units}"
  token=$TASKS_TOKEN
  ports=(8081 8082 8083)
elif [[ $mode == k8s:* ]]; then
  ns=${mode#k8s:}
  if [[ ! $ns =~ ^[a-z0-9]([-a-z0-9]*[a-z0-9])?$ ]]; then
    echo "parallel-demo.sh: k8s:<namespace> needs a valid namespace, got '$ns'" >&2
    exit 2
  fi
  ports=(18081 18082 18083)
  for p in "${ports[@]}"; do
    if portbusy "$p"; then die "local port $p is already in use"; fi
  done
  token=$(kubectl -n "$ns" get secret crew-tasks -o jsonpath='{.data.token}' | base64 -d) ||
    die "could not read Secret crew-tasks in $ns"
  [[ -n $token ]] || die "Secret crew-tasks in $ns has no token"
  for i in 0 1 2; do
    kubectl -n "$ns" port-forward --address 127.0.0.1 "deploy/${units[$i]}" "${ports[$i]}:8080" >/dev/null &
    pids+=($!)
  done
  for i in 0 1 2; do awaitforward "${ports[$i]}" "${pids[$i]}"; done
else
  echo "parallel-demo.sh: want compose or k8s:<namespace>, got $mode" >&2
  exit 2
fi

chest() { printf '{"x":%s,"y":%s,"z":%s}' "$1" "$2" "$3"; }
a=$(chest "${OUTBOX_A[@]}")
b=$(chest "${OUTBOX_B[@]}")
s=$(chest "${STORE[@]}")
# The cleared box bounds what the gatherers may break.
area=$(printf '{"min":%s,"max":%s}' "$(chest "${AREA_MIN[@]}")" "$(chest "${AREA_MAX[@]}")")

bodies=(
  "{\"template\":\"gather_and_deposit\",\"item\":\"cobblestone\",\"qty\":16,\"area\":$area,\"chest\":$a}"
  "{\"template\":\"gather_and_deposit\",\"item\":\"oak_log\",\"qty\":16,\"area\":$area,\"chest\":$b}"
  "{\"template\":\"haul\",\"items\":{\"cobblestone\":16,\"oak_log\":16},\"from\":[$a,$b],\"to\":$s}"
)

start=$(date +%s)
for i in 0 1 2; do
  id=$(api "${ports[$i]}" /v1/tasks -d "${bodies[$i]}" | jq -er .id) ||
    die "could not submit the task to ${names[$i]}"
  ids+=("$id")
done

pending=3
while ((pending > 0)); do
  if (($(date +%s) - start > timeout)); then
    echo "parallel-demo.sh: timed out after ${timeout}s" >&2
    break
  fi
  pending=0
  for i in 0 1 2; do
    [[ -n ${state[$i]:-} ]] && continue
    if ! t=$(api "${ports[$i]}" /v1/tasks | jq -ce --arg id "${ids[$i]}" '.[] | select(.id == $id)'); then
      # A few failed polls are a blip; five in a row mean the unit is gone.
      if ((${fails[$i]:-0} + 1 >= 5)); then
        state[$i]=unreachable
        reason[$i]="poll failed 5 times"
        secs[$i]=$(($(date +%s) - start))
        continue
      fi
      fails[$i]=$((${fails[$i]:-0} + 1))
      pending=$((pending + 1))
      continue
    fi
    fails[$i]=0
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

ok=true
for i in 0 1 2; do
  st=${state[$i]:-timeout}
  echo "${ids[$i]} ${names[$i]} $st ${reason[$i]:--} ${secs[$i]:-$timeout}"
  [[ $st == "done" ]] || ok=false
done
$ok
