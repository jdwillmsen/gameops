#!/usr/bin/env bash
set -euo pipefail

here=$(cd "$(dirname "$0")" && pwd)
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
mkdir -p "$work/bin" "$work/plans"

# The stub answers a console command by writing the log window the command's
# reply would appear in, so each `exec` is followed by a `logs` of just that
# reply. $AREAS_LEFT drives the `tickingarea list` reply: 0 never reports
# crew-course-* areas, N reports them for the first N calls, and "always"
# reports them on every call.
cat >"$work/bin/kubectl" <<'EOF'
#!/usr/bin/env bash
case " $* " in
  *" get pod "*) printf Running ;;
  *" exec "*)
    printf '%s\n' "$*" >>"$CMDS"
    case " $* " in
      *" send-command list "*)
        printf 'There are %s/30 players online:\n%s\n' "$PLAYERS_N" "$PLAYERS" >"$KUBECTL_LOG"
        ;;
      *" send-command tickingarea list "*)
        left=$(<"$AREAS_LEFT")
        if [[ $left == always ]] || (( left > 0 )); then
          [[ $left == always ]] || printf '%d\n' "$((left - 1))" >"$AREAS_LEFT"
          printf 'There are 0/30 players online:\nThere are 1 ticking areas in use\n- crew-course-0: 16 0 -800 to 31 0 -785\n' >"$KUBECTL_LOG"
        else
          printf 'There are 0/30 players online:\nThere are 0 ticking areas in use\n' >"$KUBECTL_LOG"
        fi
        ;;
    esac
    ;;
  *" logs "*)
    # The reply window is read with --since; the xuid check reads the whole
    # container log.
    if [[ " $* " == *" --since="* ]]; then cat "$KUBECTL_LOG"; else cat "$CONTAINER_LOG"; fi
    ;;
  *) exit 1 ;;
esac
EOF
cat >"$work/bin/sleep" <<'EOF'
#!/usr/bin/env bash
exit 0
EOF
cat >"$work/plans/courses.sh" <<'EOF'
#!/usr/bin/env bash
printf '%s\n' "$*" >"$COURSES_CALL"
EOF
chmod +x "$work/bin/kubectl" "$work/bin/sleep" "$work/plans/courses.sh"

# <players> is the `list` reply's player line and <count> its count;
# <connected> is the whole server log the xuid check greps. Extra KEY=value
# arguments become nightly.sh's environment, for the tiny STALE_AFTER and
# re-check interval that keep the waits instant.
run() {
  local players=$1 count=$2 areas=$3 connected=$4
  shift 4
  rm -f "$work/cmds" "$work/call"
  printf '%s\n' "$areas" >"$work/areas_left"
  printf '%s\n' "$connected" >"$work/serverlog"
  env PATH="$work/bin:$PATH" KUBECTL_LOG="$work/log" CONTAINER_LOG="$work/serverlog" \
    CMDS="$work/cmds" COURSES_CALL="$work/call" \
    AREAS_LEFT="$work/areas_left" PLAYERS="$players" PLAYERS_N="$count" \
    LAB=k8s:lab/server FIELDS=grounds PLANS="$work/plans" NON_HUMAN='Agate load-*' \
    "$@" bash "$here/nightly.sh"
}

run Alex 1 0 '' >"$work/human.out"
grep -Fx 'nightly.sh: skipped grounds: player Alex is online' "$work/human.out"
[[ ! -e $work/call ]]

# Probe is an offline-auth test bot: its connect line carries no xuid, so it
# is not a human even though NON_HUMAN does not name it.
run Probe 1 0 'Player connected: Probe, xuid: ' >"$work/bot.out"
grep -Fx 'nightly.sh: rebuilding grounds' "$work/bot.out"
grep -Fx -- "--confirm-overwrite --plan $work/plans/grounds.plan k8s:lab/server grounds" "$work/call"

# A real client signs in with an Xbox ID, so it blocks the rebuild.
run Dotablaze 1 0 'Player connected: Dotablaze, xuid: 2535473803383948' >"$work/xuid.out"
grep -Fx 'nightly.sh: skipped grounds: player Dotablaze is online' "$work/xuid.out"
[[ ! -e $work/call ]]

# With no connect line there is no proof the player is a bot, so they count
# as human.
run Ghost 1 0 '' >"$work/unknown.out"
grep -Fx 'nightly.sh: skipped grounds: player Ghost is online' "$work/unknown.out"
[[ ! -e $work/call ]]

run 'Agate, load-1' 2 0 '' >"$work/automation.out"
grep -Fx 'nightly.sh: rebuilding grounds' "$work/automation.out"
grep -Fx -- "--confirm-overwrite --plan $work/plans/grounds.plan k8s:lab/server grounds" "$work/call"

run 'Agate, load-1' 2 1 '' STALE_AFTER=3 RECHECK_S=1 >"$work/gone.out"
grep -Fx 'nightly.sh: waiting out the rebuild that holds crew-course-0 (up to 3s)' "$work/gone.out"
grep -Fx 'nightly.sh: rebuilding grounds' "$work/gone.out"
grep -Fx -- "--confirm-overwrite --plan $work/plans/grounds.plan k8s:lab/server grounds" "$work/call"
grep -q 'tickingarea remove' "$work/cmds" && { echo 'areas removed although only waited out' >&2; exit 1; }

run 'Agate, load-1' 2 always '' STALE_AFTER=3 RECHECK_S=1 >"$work/stale.out"
grep -Fx 'nightly.sh: waiting out the rebuild that holds crew-course-0 (up to 3s)' "$work/stale.out"
grep -Fx 'nightly.sh: removing stale ticking areas: crew-course-0' "$work/stale.out"
grep -F -- '-n lab exec server -- send-command tickingarea remove crew-course-0' "$work/cmds"
grep -Fx 'nightly.sh: rebuilding grounds' "$work/stale.out"
grep -Fx -- "--confirm-overwrite --plan $work/plans/grounds.plan k8s:lab/server grounds" "$work/call"

echo 'PASS: nightly rebuild'
