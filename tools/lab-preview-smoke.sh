#!/usr/bin/env bash
# Boots the wildlife pack alone on a throwaway Bedrock server and checks it
# headless: loads cleanly, animals persist, prey rules hold, tick cost is flat.
set -euo pipefail

# Pure helpers, kept free of docker so tools/lab-preview-smoke.test.sh can source them.
# testfor prints "Found ..." on a match and "No targets matched" otherwise; any
# other output means the console line was dropped, which proves nothing either way.
probe_verdict() {
  if grep -q 'No targets matched' <<<"$1"; then echo none
  elif grep -q 'Found' <<<"$1"; then echo found
  else echo unknown
  fi
}

# Exact match per species: "tiger=1" must not accept tiger=10 or tiger=12.
census_ok() {
  local line=$1 count=$2 sp
  for sp in tiger cheetah lion black_leopard brown_bear hyena; do
    [[ " $line " == *" $sp=$count "* ]] || return 1
  done
}

[[ ${LAB_PREVIEW_SMOKE_SOURCE_ONLY:-} == 1 ]] && return 0

COUNT=1
while [[ $# -gt 0 ]]; do
  case $1 in
    --count) COUNT=${2:?--count needs a number}; shift 2 ;;
    -h | --help) echo "usage: lab-preview-smoke [--count N]   (N of each animal; 10 = scale run)"; exit 0 ;;
    *) echo "unknown argument: $1" >&2; exit 2 ;;
  esac
done
[[ $COUNT =~ ^[1-9][0-9]*$ ]] || { echo "error: --count must be a positive integer" >&2; exit 2; }

here=$(cd "$(dirname "$0")" && pwd)
repo=$(dirname "$here")
export LAB_PREVIEW_NAME=wildlife-smoke
export LAB_PREVIEW_PORT=19147
export LAB_PREVIEW_VERSION=${LAB_PREVIEW_VERSION:-1.26.60.6}
preview=$here/lab-preview
pack=$repo/packs/lab-mods-wildlife
SPECIES=(tiger cheetah lion black_leopard brown_bear hyena)
Y=-60 # surface of the flat world

stage=$(mktemp -d)
finished=0
bot_pid=
cleanup() {
  [[ -n $bot_pid ]] && kill "$bot_pid" 2>/dev/null || true
  # An early exit (a console command that never went through) still owes the tally.
  if ((finished == 0)); then echo "RESULT count=$COUNT pass=$pass fail=$fail (aborted)"; fi
  "$preview" down >/dev/null 2>&1 || true
  rm -rf "$stage"
}
trap cleanup EXIT

pass=0
fail=0
ok() { echo "PASS $1"; pass=$((pass + 1)); }
bad() { echo "FAIL $1: $2"; fail=$((fail + 1)); }

now() { date -u +%Y-%m-%dT%H:%M:%S.%NZ; }
# The first console write after a restart can find no server process yet.
run() {
  local i
  for ((i = 0; i < 5; i++)); do
    "$preview" cmd "$@" >/dev/null 2>&1 && return 0
    sleep 2
  done
  bad console-command "never went through: $*"
  exit 1
}
logs_since() { docker logs --since "$1" "$LAB_PREVIEW_NAME" 2>&1; }
# Captured first: grep -q exiting early gives docker logs SIGPIPE under pipefail.
log_has() {
  local out
  out=$(logs_since "$1")
  grep -q -- "$2" <<<"$out"
}

# Each area is a ticking area so its chunks load with nobody online.
area() {
  local t i
  t=$(now)
  run tickingarea add "$1" "$Y" "$2" "$(($1 + 32))" "$Y" "$(($2 + 32))" "$3"
  for ((i = 0; i < 30; i++)); do
    log_has "$t" 'Added ticking area' && return 0
    sleep 1
  done
  echo "error: ticking area $3 was not added" >&2
  return 1
}

# Radii are 30, wider than any wander in 60 s, so an animal that merely strolled off
# is not mistaken for a dead one; areas sit 200 apart so selectors never overlap.
# testfor prints "Found ..." when the selector matches and "No targets matched"
# otherwise; unlike a scoreboard count it needs no setup and survives a
# restart. Selectors carry x,y,z,r so animals from other checks never match.
# Sets PROBE to found, none or unknown for a selector.
probe() {
  local t out
  t=$(now)
  run testfor "$1"
  sleep 3
  out=$(logs_since "$t")
  PROBE=$(probe_verdict "$out")
}

# Fails closed: only an explicit "Found" counts as alive.
alive() { probe "$1"; [[ $PROBE == found ]]; }

# Succeeds only on an explicit "No targets matched"; a dropped line keeps
# retrying until the deadline instead of reading as "gone".
gone_within() {
  local deadline=$((SECONDS + $2))
  while ((SECONDS < deadline)); do
    probe "$1"
    [[ $PROBE == none ]] && return 0
    sleep 2
  done
  return 1
}

# Informational only. The tick probe below reads 50.0 ms on an idle 20 tps
# server whatever the load, so it detects lag but not cost; CPU and memory show cost.
stats() { echo "stats $1: $(docker stats --no-stream --format '{{.CPUPerc}} {{.MemUsage}}' "$LAB_PREVIEW_NAME")"; }

perf() {
  local t line i
  t=$(now)
  run scriptevent gameops:perf
  for ((i = 0; i < 30; i++)); do
    line=$(logs_since "$t" | grep -o 'gameops:perf mean_tick_ms=[0-9.]*' || true)
    line=${line%%$'\n'*}
    if [[ -n $line ]]; then echo "${line##*=}"; return 0; fi
    sleep 1
  done
  return 1
}

mkdir -p "$stage/wildlife"
cp -r "$pack/behavior_pack" "$pack/resource_pack" "$stage/wildlife/"
"$preview" down >/dev/null 2>&1 || true
echo "booting $LAB_PREVIEW_VERSION with $COUNT of each animal"
boot_log=$("$preview" up "$stage" 2>&1) || { bad boot "up failed: $(tail -3 <<<"$boot_log")"; exit 1; }

# a. boot
# The world's first boot runs without the pack, so only lines after the final restart count.
# The first boot has no pack, so everything is bounded by the final restart.
started=$(docker inspect -f '{{.State.StartedAt}}' "$LAB_PREVIEW_NAME")
errs=$(logs_since "$started" |
  grep -iE 'was not found|\[(Scripting|Behavior|Resource).*(error|warn)|pack.*(error|invalid|failed)|manifest.*error|\[Scripting\].*(error|exception)' | sort -u || true)
if log_has "$started" 'Server started' && [[ -z $errs ]]; then
  ok boot
else
  bad boot "${errs:-no 'Server started'}"
fi

# b. script loaded
if log_has "$started" 'gameops:wildlife script loaded'; then
  ok script-loaded
else
  bad script-loaded "gameops:wildlife script loaded not in logs"
fi

# g (baseline). Taken before any animal exists.
area 0 0 census
area 200 0 hunt
area 400 0 named
area 600 0 tamed
area 800 0 hyenas
area 1000 0 aura
sleep 10
stats before-summon
base=$(perf || echo "")

# c. census
# Hyenas are the rivals of lion, cheetah and leopard and get hunted by them, so
# they live in their own area; the census is about persistence, not rivalry.
for sp in "${SPECIES[@]}"; do
  base_x=5
  [[ $sp == hyena ]] && base_x=805
  for ((i = 0; i < COUNT; i++)); do
    run summon "gameops:$sp" "$((base_x + (i * 7 + ${#sp} * 3) % 20))" "$Y" "$((5 + (i * 11 + ${#sp} * 5) % 20))"
  done
done
sleep 20
t=$(now)
run scriptevent gameops:census
sleep 3
line=$(logs_since "$t" | grep -o 'gameops:census tiger=.*' || true)
line=${line%%$'\n'*}
if [[ -z $line ]]; then
  bad census "no census line logged"
else
  if census_ok "$line" "$COUNT"; then ok census; else bad census "expected $COUNT each, got: $line"; fi
fi

# d. prey: wild lion hunts an unnamed sheep; a named cow is spared.
run summon gameops:lion 210 "$Y" 10
run summon sheep 214 "$Y" 10
run summon gameops:tiger 410 "$Y" 10
run summon cow '"Bessie"' 414 "$Y" 10
cow_at=$SECONDS
# Without this, a failed summon would read as "the lion ate it".
if ! alive "@e[type=sheep,x=214,y=$Y,z=10,r=30]"; then
  bad prey-sheep-hunted "sheep was not there after summon (setup failed)"
elif gone_within "@e[type=sheep,x=214,y=$Y,z=10,r=30]" 60; then
  ok prey-sheep-hunted
else
  bad prey-sheep-hunted "sheep not confirmed gone in 60s after meeting a wild lion (last probe: $PROBE)"
fi
# A full 60 s from the cow's own summon, however long the sheep check took.
((SECONDS - cow_at < 60)) && sleep $((60 - SECONDS + cow_at))
if alive "@e[type=cow,name=Bessie,x=414,y=$Y,z=10,r=30]"; then
  ok prey-named-cow-spared
else
  bad prey-named-cow-spared "named cow gone after 60s beside a wild tiger"
fi

# e. a tamed lion leaves livestock alone
ts=$(now)
run summon gameops:lion 610 "$Y" 10
tamed=0
for ((i = 0; i < 5 && tamed == 0; i++)); do
  sleep 3
  t=$(now)
  run event entity "@e[type=gameops:lion,x=610,y=$Y,z=10,r=5]" gameops:on_tamed
  sleep 2
  log_has "$t" 'on_tamed successfully executed' && tamed=1
  # The console occasionally drops a line, so a missing lion is summoned again.
  ((tamed)) || run summon gameops:lion 610 "$Y" 10
done
# A lion that never got tamed would hunt the pig and fail this check for the wrong reason.
if ((tamed == 0)); then
  bad tamed-lion-spares-pig "setup failed: no lion matched to tame; log: $(logs_since "$ts" | grep -vE '^$|INFO\] *$' | tail -6 | tr '\n' '|')"
else
  run summon pig 614 "$Y" 10
  sleep 60
  if alive "@e[type=pig,x=614,y=$Y,z=10,r=30]"; then
    ok tamed-lion-spares-pig
  else
    why="pig gone after 60s beside a tamed lion"
    alive "@e[type=pig]" && why+=" (a pig exists elsewhere)"
    alive "@e[type=gameops:lion,x=610,y=$Y,z=10,r=40]" && why+=" (lion still near)" || why+=" (lion gone too)"
    bad tamed-lion-spares-pig "$why"
  fi
fi

# e2. aura: a lion tamed by a real joined player gives that player Strength. Only a player
# can own an animal, so this is the one check that exercises the stored owner.
effects_of() {
  local t line
  t=$(now)
  run scriptevent gameops:effects smokebot
  sleep 2
  line=$(logs_since "$t" | grep -o "gameops:effects smokebot strength=[a-z0-9]* resistance=[a-z0-9]*" || true)
  echo "${line%%$'\n'*}"
}
if ! command -v go >/dev/null 2>&1; then
  echo "SKIP aura-lion-buffs-owner: go is not installed, so no player can be joined"
else
  if (cd "$repo" && go build -o "$stage/crew" ./minecraft/crew/cmd/crew) 2>"$stage/build.err"; then
    MC_HOST=127.0.0.1 MC_PORT=$LAB_PREVIEW_PORT MC_USERNAME=smokebot MC_OFFLINE=true IDLE_WANDER=false \
      STATUS_ADDR=127.0.0.1:18097 "$stage/crew" >"$stage/crew.log" 2>&1 &
    bot_pid=$!
    joined=0
    for ((i = 0; i < 60; i++)); do
      log_has "$started" 'Player Spawned: smokebot' && { joined=1; break; }
      sleep 1
    done
    if ((joined == 0)); then
      bad aura-lion-buffs-owner "smokebot never spawned; crew log: $(tail -2 "$stage/crew.log" | tr '\n' '|')"
    else
      run tp smokebot 1005 "$Y" 5
      sleep 2
      run summon gameops:lion 1008 "$Y" 8
      sleep 2
      before=$(effects_of)
      run execute as smokebot run scriptevent gameops:tame
      got=
      for ((i = 0; i < 15; i++)); do
        got=$(effects_of)
        [[ $got == *"strength=0"* ]] && break
        sleep 2
      done
      if [[ $before != *"strength=none"* ]]; then
        bad aura-lion-buffs-owner "setup failed: player already had Strength before taming (${before:-no effects line})"
      elif [[ $got == *"strength=0"* ]]; then
        ok aura-lion-buffs-owner
      else
        bad aura-lion-buffs-owner "no Strength within 30 s of taming (last: ${got:-no effects line})"
      fi
    fi
  else
    bad aura-lion-buffs-owner "crew bot did not build: $(tail -2 "$stage/build.err" | tr '\n' '|')"
  fi
fi

# f. static player safety
if "$here/check-packs.sh" "$pack" >/dev/null 2>&1; then
  ok check-packs
else
  bad check-packs "tools/check-packs.sh failed on packs/lab-mods-wildlife"
fi

# g. perf
sleep 30
stats after-summon
after=$(perf || echo "")
if [[ -z $base || -z $after ]]; then
  bad perf "no perf line (baseline='$base' after='$after')"
elif awk -v b="$base" -v a="$after" 'BEGIN { exit !(a <= b * 1.10) }'; then
  ok "perf baseline=$base after=$after"
else
  bad perf "mean_tick_ms $after is over 110% of baseline $base"
fi

# Boot only covers startup; a handler throwing later also shows as a [Scripting] error.
serr=$(logs_since "$(docker inspect -f '{{.State.StartedAt}}' "$LAB_PREVIEW_NAME")" | grep -E '\[Scripting\].*(ERROR|error|exception)' || true)
if [[ -z $serr ]]; then ok script-errors; else bad script-errors "$(head -3 <<<"$serr")"; fi

finished=1
echo "RESULT count=$COUNT pass=$pass fail=$fail"
[[ $fail -eq 0 ]]
