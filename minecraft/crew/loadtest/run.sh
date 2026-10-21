#!/usr/bin/env bash
# Camera-wall load test: M crew units, N headless viewers spread across them,
# against a lab server. Appends one markdown table row to the results file.
#
# usage: run.sh -b CREW_BIN -c CHROME -w PLAYWRIGHT_DIR -a ATLAS_DIR \
#          -m UNITS -n VIEWERS [-H LAB_HOST] [-P LAB_PORT] [-p STATUS_BASE_PORT] \
#          [-u NAME_PREFIX] [-r RADIUS] [-k VIEWER_CAP] [-d WINDOW_SECONDS] [-g swiftshader|gpu] -o RESULTS.md
set -euo pipefail

HOST=127.0.0.1 LPORT=19132 BASE=18100 PREFIX=load- RADIUS=4 CAP=4 WINDOW=30 GL=swiftshader
while getopts b:c:w:a:m:n:H:P:p:u:r:k:d:g:o: o; do
  case $o in
    b) BIN=$OPTARG;; c) CHROME=$OPTARG;; w) PWDIR=$OPTARG;; a) ATLAS=$OPTARG;;
    m) UNITS=$OPTARG;; n) VIEWERS=$OPTARG;; H) HOST=$OPTARG;; P) LPORT=$OPTARG;;
    p) BASE=$OPTARG;; u) PREFIX=$OPTARG;; r) RADIUS=$OPTARG;; k) CAP=$OPTARG;;
    d) WINDOW=$OPTARG;; g) GL=$OPTARG;; o) OUT=$OPTARG;;
    *) exit 2;;
  esac
done
: "${BIN:?-b}" "${CHROME:?-c}" "${PWDIR:?-w}" "${ATLAS:?-a}" "${UNITS:?-m}" "${VIEWERS:?-n}" "${OUT:?-o}"
HERE=$(cd "$(dirname "$0")" && pwd)
TICKS=$(getconf CLK_TCK)

pids=() tmp=$(mktemp -d)
# Only PIDs recorded here are ever signalled; never match processes by name.
# Waiting matters for back-to-back runs: a unit still shutting down keeps its
# status port, so the next run's unit fails to bind and the old one answers
# the readiness check. Anything still up after 10 s is killed.
cleanup() {
  for p in "${pids[@]}"; do kill "$p" 2>/dev/null || true; done
  for _ in $(seq 50); do
    alive=0
    for p in "${pids[@]}"; do kill -0 "$p" 2>/dev/null && alive=1; done
    ((alive)) || break
    sleep 0.2
  done
  for p in "${pids[@]}"; do kill -9 "$p" 2>/dev/null || true; done
  wait 2>/dev/null || true
  rm -rf "$tmp"
}
trap cleanup EXIT

cpu_ticks() { awk '{print $14+$15}' "/proc/$1/stat"; }
rss_kb() { awk '/^VmRSS/{print $2}' "/proc/$1/status"; }
stat_field() { curl -s "http://127.0.0.1:$1/v1/camera/stats" | sed -E "s/.*\"$2\":([0-9]+).*/\1/"; }

ports=()
for ((i = 0; i < UNITS; i++)); do
  port=$((BASE + i)); ports+=("$port")
  MC_HOST=$HOST MC_PORT=$LPORT MC_OFFLINE=true MC_USERNAME="$PREFIX$((i + 1))" \
    STATUS_ADDR="127.0.0.1:$port" CAMERA_ENABLED=true CAMERA_MAX_VIEWERS=$CAP \
    CAMERA_ATLAS_DIR=$ATLAS "$BIN" >"$tmp/crew-$i.log" 2>&1 &
  pids+=("$!")
done

# A unit is ready for viewers once it is in the world and has its position.
for port in "${ports[@]}"; do
  for _ in $(seq 120); do
    curl -s "http://127.0.0.1:$port/v1/state" | grep -q '"position_known":true' && break
    sleep 1
  done
  curl -s "http://127.0.0.1:$port/v1/state" | grep -q '"position_known":true' \
    || { echo "unit on $port never joined" >&2; exit 1; }
done
sleep 10 # let the join's chunk burst finish so it is not billed to the viewers

portlist=$(IFS=,; echo "${ports[*]}")
c0=() b0=() s0=()
for i in "${!ports[@]}"; do
  c0+=("$(cpu_ticks "${pids[$i]}")"); b0+=("$(stat_field "${ports[$i]}" bytes_sent)")
  s0+=("$(date +%s.%N)")
done

node "$HERE/viewers.mjs" --pw-dir "$PWDIR" --chrome "$CHROME" --ports "$portlist" \
  --viewers "$VIEWERS" --radius "$RADIUS" --window "$WINDOW" --gl "$GL" >"$tmp/viewers.out" 2>"$tmp/viewers.err" &
vpid=$!
pids+=("$vpid")

until grep -q SETTLED "$tmp/viewers.out" 2>/dev/null; do
  kill -0 "$vpid" 2>/dev/null || { cat "$tmp/viewers.err" >&2; exit 1; }
  sleep 1
done

# Replay phase: everything since the viewers started.
replay_bytes=0
c1=() b1=() t1=()
for i in "${!ports[@]}"; do
  c1+=("$(cpu_ticks "${pids[$i]}")"); b1+=("$(stat_field "${ports[$i]}" bytes_sent)"); t1+=("$(date +%s.%N)")
  replay_bytes=$((replay_bytes + b1[i] - b0[i]))
done

wait "$vpid"
grep '^renderer:' "$tmp/viewers.err" >&2 || true
# Steady phase: the viewers' own window, bracketed by the same SETTLED/exit
# edges, so crew CPU and bytes cover the span the frame times do.
c2=() b2=() t2=() rss=0
for i in "${!ports[@]}"; do
  c2+=("$(cpu_ticks "${pids[$i]}")"); b2+=("$(stat_field "${ports[$i]}" bytes_sent)"); t2+=("$(date +%s.%N)")
  r=$(rss_kb "${pids[$i]}"); ((r > rss)) && rss=$r
done

row=$(awk -v ticks="$TICKS" -v m="$UNITS" -v n="$VIEWERS" -v rad="$RADIUS" -v rss="$rss" \
  -v rb="$replay_bytes" -v vj="$(tail -n1 "$tmp/viewers.out")" \
  -v c1="${c1[*]}" -v c2="${c2[*]}" -v b1="${b1[*]}" -v b2="${b2[*]}" -v t1="${t1[*]}" -v t2="${t2[*]}" '
  BEGIN {
    split(c1, C1, " "); split(c2, C2, " "); split(b1, B1, " "); split(b2, B2, " ")
    split(t1, T1, " "); split(t2, T2, " ")
    for (i = 1; i <= m; i++) {
      dt = T2[i] - T1[i]; cpu = (C2[i] - C1[i]) / ticks / dt * 100
      if (cpu > cpumax) cpumax = cpu; cpusum += cpu
      bytes += B2[i] - B1[i]; span = dt
    }
    # Viewer JSON is flat enough to total with a regex pass.
    s = vj; w = 0; wm = 0; fps = 0; st = 0; k = 0
    while (match(s, /"settledMs":[0-9.]+/)) { st += substr(s, RSTART + 12, RLENGTH - 12); s = substr(s, RSTART + RLENGTH); k++ }
    s = vj; while (match(s, /"workMsAvg":[0-9.]+/)) { w += substr(s, RSTART + 12, RLENGTH - 12); s = substr(s, RSTART + RLENGTH) }
    s = vj; while (match(s, /"fps":[0-9.]+/)) { fps += substr(s, RSTART + 6, RLENGTH - 6); s = substr(s, RSTART + RLENGTH) }
    s = vj; while (match(s, /"workMaxMs":[0-9.]+/)) { v = substr(s, RSTART + 12, RLENGTH - 12) + 0; if (v > wm) wm = v; s = substr(s, RSTART + RLENGTH) }
    if (k == 0) k = 1
    printf "| %d | %d | %d | %.1f%% / %.1f%% | %.0f | %.1f | %.1f | %.1f | %.1f | %.1f | %.0f | %.1f |\n",
      m, n, rad, cpusum / m, cpumax, rss / 1024, bytes / m / span / 1024, bytes / n / span / 1024,
      rb / 1048576, st / k / 1000, w / k, wm, fps / k
  }')
if [[ ! -s $OUT ]]; then
  {
    echo '| Units | Viewers | Radius | Crew CPU mean / max | Crew RSS max (MB) | Steady KB/s per unit | Steady KB/s per viewer | Replay MB (all units) | Settle (s) | Frame work avg (ms) | Frame work max (ms) | Page fps |'
    echo '|---|---|---|---|---|---|---|---|---|---|---|---|'
  } >"$OUT"
fi
echo "$row" >>"$OUT"
echo "$row"
