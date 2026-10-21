#!/usr/bin/env bash
# Drives idle crew units through the lab's test fields and reports one line per
# case. Run it after the fields are built (courses.sh); --reset rebuilds one
# first.
#
# usage: fieldtest.sh [--list] [--units crew-04,crew-05]
#                      [--reset grounds|courses|trees|works --confirm-overwrite]
#                      k8s:<namespace> [case-glob] [timeout-s]
#   case-glob  bash glob over case names, default '*' (for example 'drops-*')
#   timeout-s  how long each task may run before it is cancelled, default 300
#   --list     prints the case names matching the glob and exits, touching nothing
#
# Output, one line per case as it finishes:
#   <case> <unit> <pass|fail> <reason> <seconds>
# then "summary pass=N fail=M units=K seconds=S". Exit 0 only when every
# selected case passed, 1 otherwise, 2 for bad arguments. Reasons contain no
# spaces.
#
# Only idle units are used (nothing queued or running, and nobody else has
# touched them for FIELDTEST_QUIET_S seconds, default 120), one case at a time per
# unit; a unit that turns out busy is dropped and its case goes to another.
# Tasks this script did not start are never cancelled. A goto is also checked
# on the server (querytarget), because "done" only says the unit believes it
# arrived. The token is held in a shell variable, never printed or written to
# disk. On a timeout, an error or INT/TERM, every task this run started that is
# still going is cancelled and the port-forwards are killed.
set -euo pipefail

here=$(cd "$(dirname "$0")" && pwd)

# One case per line: name|pre|task|expect
#   pre     "x y z" to goto first (a case that needs the unit already at a
#           field, or on one side of an obstacle), or empty
#   task    "goto x y z" or a template JSON object
#   expect  done or failed
# A goto that is expected to be done is also verified on the server. Cells are
# standing positions: feet y, so one above the block's top surface.
# Fields: trees at x -444..-301, courses at x -294..-151, grounds at x
# 150..293 and works at z -700..-557; cell (i,j) starts at x0+6+46i,
# z0+6+46j with z0 -500 for the southern fields and -700 for works.
cases_table() {
  cat <<'EOF'
# courses: drops cell (-288,-494)
drops-ledge-1||goto -284 65 -488|done
drops-ledge-2||goto -278 66 -488|done
drops-ledge-3||goto -272 67 -488|done
drops-ledge-4||goto -266 68 -488|done
drops-trench-1|-286 64 -477|goto -284 64 -477|done
drops-trench-2|-279 64 -477|goto -275 64 -477|done
drops-trench-3|-272 64 -477|goto -266 64 -477|done
drops-trench-4|-265 64 -477|goto -259 64 -477|done
drops-step-1||goto -285 65 -469|done
drops-step-2||goto -282 66 -469|done
drops-step-3||goto -279 67 -469|done
drops-step-4||goto -276 68 -469|done
drops-step-5||goto -273 69 -469|done
drops-step-6||goto -270 70 -469|done
# courses: shapes cell (-196,-494)
shapes-slab-bottom||goto -193 64 -490|done
shapes-slab-top||goto -191 65 -490|done
shapes-slab-ramp||goto -177 67 -490|done
shapes-stairs||goto -193 67 -482|done
shapes-door-closed||goto -192 64 -470|done
shapes-door-open||goto -176 64 -470|done
shapes-trapdoor||goto -192 64 -464|done
shapes-carpet||goto -193 64 -460|done
shapes-snow||goto -187 64 -460|done
# courses: numbered pads
pad-01||goto -277 64 -437|done
pad-07||goto -231 64 -419|done
pad-13||goto -268 64 -382|done
# grounds: work yards
gather-cobble-quarry|221 64 -460|{"template":"gather_and_deposit","item":"cobblestone","qty":16,"area":{"min":{"x":210,"y":64,"z":-486},"max":{"x":233,"y":79,"z":-463}},"chest":{"x":160,"y":64,"z":-440}}|done
gather-oak-grove|153 64 -470|{"template":"gather_and_deposit","item":"oak_log","qty":12,"area":{"min":{"x":156,"y":64,"z":-494},"max":{"x":195,"y":76,"z":-455}},"chest":{"x":164,"y":64,"z":-440}}|done
haul-storage|158 64 -446|{"template":"haul","items":{"cobblestone":8,"oak_log":8},"from":[{"x":162,"y":64,"z":-444}],"to":{"x":168,"y":64,"z":-440}}|done
# trees: fixed oaks 1-3 in cell (0,0), each confined to its own trunk, into
# the lumber yard's first chest; then the dense wood in cell (0,1)
fell-oak-1|-430 64 -498|{"template":"gather_and_deposit","item":"oak_log","qty":4,"area":{"min":{"x":-435,"y":64,"z":-491},"max":{"x":-425,"y":70,"z":-481}},"chest":{"x":-388,"y":64,"z":-398}}|done
fell-oak-2|-418 64 -498|{"template":"gather_and_deposit","item":"oak_log","qty":6,"area":{"min":{"x":-423,"y":64,"z":-491},"max":{"x":-413,"y":72,"z":-481}},"chest":{"x":-388,"y":64,"z":-398}}|done
fell-oak-3|-406 64 -498|{"template":"gather_and_deposit","item":"oak_log","qty":12,"area":{"min":{"x":-411,"y":64,"z":-491},"max":{"x":-401,"y":80,"z":-481}},"chest":{"x":-388,"y":64,"z":-398}}|done
fell-dense-wood|-420 64 -452|{"template":"gather_and_deposit","item":"oak_log","qty":8,"area":{"min":{"x":-438,"y":64,"z":-448},"max":{"x":-399,"y":76,"z":-409}},"chest":{"x":-388,"y":64,"z":-398}}|done
# works: bridges cell (2,1), north to south over the 3-, 2- and 1-wide decks
bridge-3-wide|253 64 -640|goto 253 64 -617|done
bridge-2-wide|258 64 -640|goto 258 64 -617|done
bridge-1-wide|263 64 -640|goto 263 64 -617|done
# works: climbing cell (1,1), the landing at y 76 puts feet at 77
climb-landing||goto 211 77 -642|done
# works: gates cell (0,1), north to south through row 0's iron door
gate-door|161 64 -642|goto 161 64 -638|done
EOF
}

case_names=()
declare -A case_pre case_task case_expect

load_cases() {
  local name pre task expect
  while IFS='|' read -r name pre task expect; do
    [[ -z $name || $name == \#* ]] && continue
    case_names+=("$name")
    case_pre[$name]=$pre
    case_task[$name]=$task
    case_expect[$name]=${expect:-done}
  done < <(cases_table)
}

# Prints the JSON body for a task spec: a "goto x y z" shorthand or raw JSON.
task_body() {
  local spec=$1 x y z
  if [[ $spec == goto\ * ]]; then
    read -r _ x y z <<<"$spec"
    printf '{"template":"goto","to":{"x":%s,"y":%s,"z":%s}}' "$x" "$y" "$z"
  else
    printf '%s' "$spec"
  fi
}

# Prints "x y z" for a goto spec and nothing for any other template.
goto_target() {
  [[ $1 == goto\ * ]] && echo "${1#goto }"
  return 0
}

# Echoes the names matching a glob, in table order.
select_cases() {
  local glob=$1 n
  for n in "${case_names[@]}"; do
    # shellcheck disable=SC2053 # the glob is the point
    [[ $n == $glob ]] && echo "$n"
  done
  return 0
}

die() {
  echo "fieldtest.sh: $*" >&2
  exit 1
}

usage_exit() {
  echo "fieldtest.sh: $*" >&2
  echo "usage: fieldtest.sh [--list] [--units a,b] [--reset grounds|courses|trees|works --confirm-overwrite] k8s:<namespace> [case-glob] [timeout-s]" >&2
  exit 2
}

parse_args() {
  list=false reset="" confirm=false only_units="" mode="" glob='*' timeout=300
  local positional=()
  while (($#)); do
    case $1 in
      --list) list=true ;;
      --confirm-overwrite) confirm=true ;;
      --reset)
        (($# >= 2)) || usage_exit "--reset needs grounds, courses, trees or works"
        reset=$2
        shift
        ;;
      --units)
        (($# >= 2)) || usage_exit "--units needs a comma-separated list"
        only_units=$2
        shift
        ;;
      -*) usage_exit "unknown option $1" ;;
      *) positional+=("$1") ;;
    esac
    shift
  done
  if [[ -n $reset && $reset != grounds && $reset != courses && $reset != trees && $reset != works ]]; then
    usage_exit "--reset wants grounds, courses, trees or works, got '$reset'"
  fi
  if [[ -n $reset ]] && ! $confirm; then
    usage_exit "--reset replaces a field in the shared world; add --confirm-overwrite"
  fi
  if [[ -n $only_units && ! $only_units =~ ^crew-[0-9]+(,crew-[0-9]+)*$ ]]; then
    usage_exit "--units wants names like crew-04,crew-05"
  fi
  if $list; then
    glob=${positional[0]:-*}
    return 0
  fi
  ((${#positional[@]} >= 1 && ${#positional[@]} <= 3)) || usage_exit "want k8s:<namespace> [case-glob] [timeout-s]"
  mode=${positional[0]}
  [[ $mode == k8s:* ]] || usage_exit "want k8s:<namespace>, got '$mode'"
  ns=${mode#k8s:}
  [[ $ns =~ ^[a-z0-9]([-a-z0-9]*[a-z0-9])?$ ]] || usage_exit "k8s:<namespace> needs a valid namespace, got '$ns'"
  glob=${positional[1]:-*}
  timeout=${positional[2]:-300}
  if [[ ! $timeout =~ ^[0-9]+$ ]] || ((timeout == 0)); then
    usage_exit "timeout-s must be a positive integer, got '$timeout'"
  fi
}

# Road geometry of the four fields. The three southern fields share their row
# spacing; works sits a field further north and has its own rows. Horizontal
# roads run between the cell rows, each field has a vertical road on its hub
# side, and everything on them is clear ground.
south_road_zs=(-498 -452 -406 -360)
works_road_zs=(-698 -652 -606 -560)
lane_z=-438
works_row_z=-560

# A unit's field, from its x and z: works is north of grounds and trees west
# of courses, so x alone cannot tell the east fields apart.
side_of() {
  local x=$1 z=$2
  if ((x >= 150)); then
    if ((z <= -557)); then echo works; else echo grounds; fi
  elif ((x <= -295)); then echo trees
  elif ((x <= -151)); then echo courses
  else echo hub
  fi
}

road_rows() {
  if [[ $1 == works ]]; then echo "${works_road_zs[*]}"; else echo "${south_road_zs[*]}"; fi
}

nearest_road_z() {
  local field=$1 z=$2 best r
  local -a rows
  read -ra rows <<<"$(road_rows "$field")"
  best=${rows[0]}
  for r in "${rows[@]}"; do
    if (($(abs $((z - r))) < $(abs $((z - best))))); then best=$r; fi
  done
  echo "$best"
}
abs() { echo "${1#-}"; }

# The fields form a chain: trees - courses - hub - grounds - works. A crossing
# leaves each field on this side's vertical road and enters the next on its
# own; grounds and works share the x 199 road, where the path north is.
chain_index() {
  case $1 in
    trees) echo 0 ;;
    courses) echo 1 ;;
    hub) echo 2 ;;
    grounds) echo 3 ;;
    works) echo 4 ;;
  esac
}

chain_next() {
  case $1:$2 in
    trees:*) echo courses ;;
    courses:1) echo hub ;;
    courses:-1) echo trees ;;
    hub:1) echo grounds ;;
    hub:-1) echo courses ;;
    grounds:1) echo works ;;
    grounds:-1) echo hub ;;
    works:*) echo grounds ;;
  esac
}

# The vertical road a field leaves or enters on, for direction 1 (toward works)
# or -1 (toward trees).
port_x() {
  case $1:$2 in
    trees:*) echo -303 ;;
    courses:1) echo -154 ;;
    courses:-1) echo -291 ;;
    grounds:1) echo 199 ;;
    grounds:-1) echo 152 ;;
    works:*) echo 199 ;;
  esac
}

# The row a crossing between two fields runs along.
cross_z() {
  case $1:$2 in
    trees:courses | courses:trees) echo "$lane_z" ;;
    courses:hub | hub:courses | hub:grounds | grounds:hub) echo "$lane_z" ;;
    grounds:works | works:grounds) echo "$works_row_z" ;;
  esac
}

route_last=""
emit_point() {
  [[ $1 == "$route_last" ]] && return 0
  echo "$1"
  route_last=$1
}

# Prints the waypoints "x z" from the unit's position "px pz" to the road
# point beside the destination "dx dz", one per line. The destination itself
# is left to the task. A unit only knows the terrain near it and fields are full
# of solid blocks, so it travels along roads rather than in a straight line.
route() {
  local px=$1 pz=$2 dx=$3 dz=$4 ps ds dir f next bz ex row zrp zrd
  route_last=""
  ps=$(side_of "$px" "$pz")
  ds=$(side_of "$dx" "$dz")
  if [[ $ps == "$ds" && $ps != hub ]] &&
    (($(abs $((px - dx))) <= 64 && $(abs $((pz - dz))) <= 64)); then
    return 0
  fi
  if [[ $ps == "$ds" ]]; then
    if [[ $ps == hub ]]; then
      emit_point "$px $lane_z"
      return 0
    fi
    zrp=$(nearest_road_z "$ps" "$pz")
    zrd=$(nearest_road_z "$ps" "$dz")
    emit_point "$px $zrp"
    emit_point "$(port_x "$ps" 1) $zrp"
    emit_point "$(port_x "$ps" 1) $zrd"
    emit_point "$dx $zrd"
    return 0
  fi

  if (($(chain_index "$ps") < $(chain_index "$ds"))); then dir=1; else dir=-1; fi
  # Leave the source field: locally out to a road row, along it to the port,
  # then up or down the port to the crossing.
  if [[ $ps == hub ]]; then
    emit_point "$px $lane_z"
  else
    zrp=$(nearest_road_z "$ps" "$pz")
    emit_point "$px $zrp"
    emit_point "$(port_x "$ps" "$dir") $zrp"
  fi

  # Cross each field in turn. An intermediate field is crossed on the road row
  # nearest the destination, so most of the final approach is already behind it.
  f=$ps
  while [[ $f != "$ds" ]]; do
    next=$(chain_next "$f" "$dir")
    bz=$(cross_z "$f" "$next")
    if [[ $f != hub ]]; then
      emit_point "$(port_x "$f" "$dir") $bz"
    fi
    if [[ $next != hub ]]; then
      ex=$(port_x "$next" "$(( -dir ))")
      emit_point "$ex $bz"
      if [[ $next != "$ds" ]]; then
        row=$(nearest_road_z "$next" "$dz")
        emit_point "$ex $row"
        emit_point "$(port_x "$next" "$dir") $row"
      fi
    fi
    f=$next
  done

  if [[ $ds != hub ]]; then
    zrd=$(nearest_road_z "$ds" "$dz")
    emit_point "$(port_x "$ds" "$(( -dir ))") $zrd"
    emit_point "$dx $zrd"
  fi
}

# Sourced by the unit test: stop before touching a cluster.
if [[ ${FIELDTEST_LIB:-} == 1 ]]; then
  load_cases
  # shellcheck disable=SC2317 # exit covers the script being run, not sourced
  return 0 2>/dev/null || exit 0
fi

load_cases
parse_args "$@"
mapfile -t selected < <(select_cases "$glob")
if $list; then
  ((${#selected[@]})) || { echo "fieldtest.sh: no case matches '$glob'" >&2; exit 1; }
  printf '%s\n' "${selected[@]}"
  exit 0
fi
((${#selected[@]})) || { echo "fieldtest.sh: no case matches '$glob'" >&2; exit 2; }

tmp=$(mktemp -d)
pids=()
declare -A port_of name_of
started=$SECONDS

# The header goes through stdin so the token is not in the process list.
api() {
  local port=$1 path=$2
  shift 2
  printf 'Authorization: Bearer %s' "$token" |
    curl -sS --fail-with-body --max-time 10 -H @- "$@" "http://127.0.0.1:$port$path"
}

# Cancels every task this run started and has not seen finish, then stops
# the children and the port-forwards. Invoked through the traps.
# shellcheck disable=SC2317
cleanup() {
  trap - EXIT INT TERM
  local f unit id
  # Children first: a child still polling could otherwise resubmit nothing,
  # but would race the cancel below.
  jobs -p | xargs -r kill 2>/dev/null || true
  for f in "$tmp"/task.*; do
    [[ -s $f ]] || continue
    unit=${f##*/task.}
    id=$(<"$f")
    if [[ -n ${port_of[$unit]:-} ]]; then
      api "${port_of[$unit]}" "/v1/tasks/$id/cancel" -X POST >/dev/null 2>&1 || true
    fi
  done
  if ((${#pids[@]})); then kill "${pids[@]}" 2>/dev/null || true; fi
  rm -rf "$tmp"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

portbusy() { (exec 3<>"/dev/tcp/127.0.0.1/$1") 2>/dev/null; }

awaitforward() {
  local port=$1 pid=$2 deadline=$((SECONDS + 20)) code
  while ((SECONDS < deadline)); do
    kill -0 "$pid" 2>/dev/null || return 1
    code=$(curl -s -o /dev/null -w '%{http_code}' --max-time 2 "http://127.0.0.1:$port/healthz" || true)
    if [[ $code == 200 ]] && kill -0 "$pid" 2>/dev/null; then return 0; fi
    sleep 0.5
  done
  return 1
}

token=$(kubectl -n "$ns" get secret crew-tasks -o jsonpath='{.data.token}' | base64 -d) ||
  die "could not read Secret crew-tasks in $ns"
[[ -n $token ]] || die "Secret crew-tasks in $ns has no token"

pod=$(kubectl -n "$ns" get pods -o name | sed -n 's|^pod/||; /minecraft-bedrock-0$/p' | head -1)
[[ -n $pod ]] || die "no minecraft-bedrock-0 server pod in $ns"
ctr=$(kubectl -n "$ns" get pod "$pod" -o jsonpath='{.spec.containers[0].name}')

if [[ -n $reset ]]; then
  "$here/courses.sh" --confirm-overwrite "k8s:$ns/$pod" "$reset" >&2 || die "reset of $reset failed"
fi

# crew-NN deployments and the in-game name each logs in as.
mapfile -t found < <(kubectl -n "$ns" get deploy -o json | jq -r '
  .items[] | select(.metadata.name | test("^crew-[0-9]+$"))
  | [.metadata.name, (.spec.template.spec.containers[0].env // [] | map(select(.name == "MC_USERNAME") | .value) | first // "")]
  | @tsv')
candidates=()
for line in "${found[@]}"; do
  IFS=$'\t' read -r unit uname <<<"$line"
  [[ -n $uname ]] || continue
  if [[ -n $only_units && ,$only_units, != *,"$unit",* ]]; then continue; fi
  name_of[$unit]=$uname
  candidates+=("$unit")
done
((${#candidates[@]})) || die "no crew units found in $ns"

fwd_pid=()
for unit in "${candidates[@]}"; do
  port=$((19000 + 10#${unit#crew-}))
  if portbusy "$port"; then die "local port $port is already in use"; fi
  port_of[$unit]=$port
  kubectl -n "$ns" port-forward --address 127.0.0.1 "deploy/$unit" "$port:8080" >/dev/null 2>&1 &
  fwd_pid[${#fwd_pid[@]}]=$!
  pids+=($!)
done

# Seconds a unit must have been untouched by anyone else before it counts as
# idle: another session's loop can leave gaps of a few seconds between tasks.
quiet=${FIELDTEST_QUIET_S:-120}

# Succeeds when the unit has nothing queued or running and no task this run did
# not start was touched within the quiet window.
idle() {
  local port=$1 unit=${2:-} mine='[]'
  if [[ -n $unit && -s $tmp/mine.$unit ]]; then mine=$(jq -R . <"$tmp/mine.$unit" | jq -s .); fi
  api "$port" /v1/tasks 2>/dev/null |
    jq -e --argjson mine "$mine" --argjson q "$quiet" 'all(.[];
      (.state != "queued" and .state != "running") and
      (. as $t | ($mine | index($t.id)) != null or
        (now - (.updated | sub("\\.[0-9]+"; "") | fromdateiso8601)) > $q))' >/dev/null 2>&1
}

free=()
for i in "${!candidates[@]}"; do
  unit=${candidates[$i]}
  if awaitforward "${port_of[$unit]}" "${fwd_pid[$i]}" && idle "${port_of[$unit]}"; then
    free+=("$unit")
  else
    kill "${fwd_pid[$i]}" 2>/dev/null || true
    echo "fieldtest.sh: skipping $unit (busy, used in the last ${quiet}s, or unreachable)" >&2
  fi
done
((${#free[@]})) || die "no idle unit in $ns"

# Polls the unit's task until it ends or the timeout cancels it. Prints
# "<state> <reason>" with the reason's spaces replaced.
await_task() {
  local port=$1 id=$2 deadline=$((SECONDS + timeout)) fails=0 t st
  while ((SECONDS < deadline)); do
    if t=$(api "$port" /v1/tasks | jq -ce --arg id "$id" '.[] | select(.id == $id)'); then
      fails=0
      st=$(jq -r .state <<<"$t")
      case $st in
        done | failed | cancelled)
          echo "$st $(jq -r '.reason // "-"' <<<"$t" | tr ' ' '_')"
          return 0
          ;;
      esac
    else
      fails=$((fails + 1))
      if ((fails >= 5)); then
        echo "unreachable poll_failed_5_times"
        return 0
      fi
    fi
    sleep 2
  done
  api "$port" "/v1/tasks/$id/cancel" -X POST >/dev/null 2>&1 || true
  echo "timeout ${timeout}s"
}

# Submits a task body and prints its id; records it for the cleanup trap.
submit() {
  local unit=$1 body=$2 id
  id=$(api "${port_of[$unit]}" /v1/tasks -d "$body" | jq -er .id) || return 1
  echo "$id" >"$tmp/task.$unit"
  echo "$id" >>"$tmp/mine.$unit"
  echo "$id"
}

# The server's own view of where the unit stands (feet), "x y z". Serialised:
# the console answer carries no name, so two queries at once could not be told
# apart in the log.
server_pos() {
  local uname=$1 block
  (
    flock 9
    kubectl -n "$ns" exec -c "$ctr" "$pod" -- send-command "querytarget @a[name=$uname]" >/dev/null 2>&1 || exit 1
    sleep 1.5
    block=$(kubectl -n "$ns" logs -c "$ctr" "$pod" --since=10s 2>/dev/null |
      awk '/Target data: \[/ {buf="["; inb=1; next} inb {buf = buf $0} inb && /^\]/ {last=buf; inb=0} END {print last}')
    [[ -n $block ]] || exit 1
    jq -er '.[0].position | "\(.x) \(.y - 1.62) \(.z)"' <<<"$block"
  ) 9>"$tmp/lock"
}

# Succeeds when the server places the unit within tolerance of x y z: 2.5
# blocks across, 2 up or down.
near() {
  awk -v p="$1" -v t="$2" 'BEGIN {
    split(p, a, " "); split(t, b, " ")
    dx = a[1] - b[1] - 0.5; dz = a[3] - b[3] - 0.5; dy = a[2] - b[2]
    if (dx < 0) dx = -dx
    if (dz < 0) dz = -dz
    if (dy < 0) dy = -dy
    exit !(dx <= 2.5 && dz <= 2.5 && dy <= 2)
  }'
}

# Walks the unit to the road point beside dest in hops of at most 64 blocks,
# each ending on a waypoint or a straight line to one. Sets approach_err.
approach() {
  local unit=$1 dest=$2 pos px pz dx dz wx wz next res id n
  read -r dx _ dz <<<"$dest"
  pos=$(server_pos "${name_of[$unit]}") || { approach_err=server_position_unreadable; return 1; }
  read -r px _ pz <<<"$pos"
  px=$(printf '%.0f' "$px")
  pz=$(printf '%.0f' "$pz")
  while read -r wx wz; do
    [[ -n $wx ]] || continue
    for ((n = 0; n < 20; n++)); do
      next=$(awk -v px="$px" -v pz="$pz" -v wx="$wx" -v wz="$wz" 'BEGIN {
        dx = wx - px; dz = wz - pz; d = sqrt(dx * dx + dz * dz)
        if (d == 0) exit
        if (d <= 64) printf "%d %d", wx, wz
        else printf "%.0f %.0f", px + dx * 64 / d, pz + dz * 64 / d
      }')
      [[ -n $next ]] || break
      read -r hx hz <<<"$next"
      id=$(submit "$unit" "$(task_body "goto $hx 64 $hz")") || { approach_err=hop_rejected; return 1; }
      res=$(await_task "${port_of[$unit]}" "$id")
      if [[ ${res%% *} != "done" ]]; then
        approach_err="hop_${hx},${hz}_${res%% *}:${res#* }"
        return 1
      fi
      px=$hx pz=$hz
    done
  done < <(route "$px" "$pz" "$dx" "$dz")
}

# Runs one case on one unit in a subshell and writes its result line to
# $tmp/res.<case>, or BUSY when the unit took other work meanwhile.
run_case() {
  local name=$1 unit=$2 begin=$SECONDS port
  port=${port_of[$unit]}
  local pre=${case_pre[$name]} spec=${case_task[$name]} want=${case_expect[$name]}
  local id res state reason target pos dest
  finish() { echo "$name $unit $1 $2 $((SECONDS - begin))" >"$tmp/res.$name"; : >"$tmp/task.$unit"; }

  if ! idle "$port" "$unit"; then
    echo BUSY >"$tmp/res.$name"
    return 0
  fi
  dest=${pre:-$(goto_target "$spec")}
  if [[ -n $dest ]] && ! approach "$unit" "$dest"; then
    finish fail "approach_$approach_err"
    return 0
  fi
  if [[ -n $pre ]]; then
    id=$(submit "$unit" "$(task_body "goto $pre")") || { finish fail pre_submit_rejected; return 0; }
    res=$(await_task "$port" "$id")
    state=${res%% *}
    [[ $state == "done" ]] || { finish fail "pre_goto_$state:${res#* }"; return 0; }
  fi
  id=$(submit "$unit" "$(task_body "$spec")") || { finish fail submit_rejected; return 0; }
  res=$(await_task "$port" "$id")
  state=${res%% *}
  reason=${res#* }
  if [[ $state != "$want" ]]; then
    finish fail "task_$state:$reason"
    return 0
  fi
  target=$(goto_target "$spec")
  if [[ $want == "done" && -n $target ]]; then
    if ! pos=$(server_pos "${name_of[$unit]}"); then
      finish fail server_position_unreadable
    elif near "$pos" "$target"; then
      finish pass "done,server_pos=$(awk '{printf "%.1f,%.1f,%.1f", $1, $2, $3}' <<<"$pos")"
    else
      finish fail "server_pos=$(awk '{printf "%.1f,%.1f,%.1f", $1, $2, $3}' <<<"$pos"),want=${target// /,}"
    fi
    return 0
  fi
  finish pass "$state"
}

pending=("${selected[@]}")
declare -A job_pid job_case
npass=0 nfail=0
units_used=${#free[@]}

while :; do
  while ((${#pending[@]} && ${#free[@]})); do
    c=${pending[0]}
    pending=("${pending[@]:1}")
    u=${free[0]}
    free=("${free[@]:1}")
    run_case "$c" "$u" &
    job_pid[$u]=$!
    job_case[$u]=$c
  done
  ((${#job_pid[@]})) || break
  sleep 1
  for u in "${!job_pid[@]}"; do
    kill -0 "${job_pid[$u]}" 2>/dev/null && continue
    wait "${job_pid[$u]}" || true
    c=${job_case[$u]}
    unset "job_pid[$u]" "job_case[$u]"
    line=$(cat "$tmp/res.$c" 2>/dev/null || echo "$c $u fail case_crashed 0")
    if [[ $line == BUSY ]]; then
      echo "fieldtest.sh: $u took other work; retiring it" >&2
      pending=("$c" "${pending[@]}")
      units_used=$((units_used - 1))
      continue
    fi
    echo "$line"
    if [[ $line == "$c $u pass "* ]]; then npass=$((npass + 1)); else nfail=$((nfail + 1)); fi
    free+=("$u")
  done
  if ((${#pending[@]} && !${#free[@]} && !${#job_pid[@]})); then
    for c in "${pending[@]}"; do
      echo "$c - fail no_idle_unit 0"
      nfail=$((nfail + 1))
    done
    break
  fi
done

echo "summary pass=$npass fail=$nfail units=$units_used seconds=$((SECONDS - started))"
((nfail == 0))
