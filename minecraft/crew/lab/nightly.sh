#!/usr/bin/env bash
# Rebuilds the lab's test fields from prebuilt plans, so a test starts from a
# clean field. Runs from the lab chart's nightly job, but works from any shell
# with kubectl access to the server pod.
#
# usage: nightly.sh
#   LAB    k8s:<namespace>/<pod>, the lab server's pod
#   FIELDS space-separated fields to rebuild, in order
#   PLANS  directory holding <field>.plan and courses.sh (labcourses -o)
#   NON_HUMAN  space- or comma-separated player-name patterns that do not
#              block a rebuild (an override for bots the xuid check below
#              cannot tell apart from a person)
#   STALE_AFTER  seconds to wait out a rebuild that holds the crew-course-*
#                areas before clearing them as stale (default 900: a build
#                of every field finishes well within it, and the wait plus a
#                build must fit the job's activeDeadlineSeconds)
#   RECHECK_S    seconds between re-checks in that wait (default 60; a knob
#                for the test, which shrinks it)
#
# A rebuild replaces everything in a field's box, so it never starts while a
# human is in the world, or while another rebuild holds its ticking areas.
# A player is human unless NON_HUMAN names them or their latest
# `Player connected: ..., xuid: ...` line in the server log has an empty
# xuid: people sign in with an Xbox ID, offline-auth bots never do. Without
# a connect line they count as human, the safe way to be wrong.
# A human is a skip, not a failure: it exits 0 so a busy night does not page.
# Held areas are waited out rather than skipped: their holder removes them
# when it ends, unless its server pod restarted mid-build and killed the trap
# that would. Such leftovers would otherwise skip every night forever, so
# past STALE_AFTER they are removed as stale and the rebuild proceeds.
set -uo pipefail

: "${LAB:?LAB is required}" "${FIELDS:?FIELDS is required}" "${PLANS:?PLANS is required}" "${NON_HUMAN:?NON_HUMAN is required}"
target=${LAB#k8s:}
ns=${target%%/*}
pod=${target#*/}
stale_after=${STALE_AFTER:-900}
recheck_s=${RECHECK_S:-60}

emit() { echo "nightly.sh: $*"; }

# Console replies land in the server log, not on the exec's output, and the
# map's live layer buries them within seconds: send, then read a short window.
# Returns 2 when the command was not delivered, which means the server process
# is not up (mid-restart), as opposed to up but unreadable.
console() {
  kubectl -n "$ns" exec "$pod" -- send-command "$@" >/dev/null 2>&1 || return 2
  sleep 2
  kubectl -n "$ns" logs "$pod" --since=30s 2>/dev/null
}

# Sets $busy to why the lab must be left alone, or empty if it is free, and
# $holders to the crew-course-* area names behind that reason, one per line,
# so a wait that outlives their builder can clear them by name.
# Returns 1 when the lab's state cannot be read: an unreadable list is not
# evidence of an empty server, and the cost of the two mistakes is not equal.
# Returns 2 when the server is not accepting commands.
check_free() {
  busy='' holders=
  local out block name pattern non_humans="${NON_HUMAN//,/ }"
  local connected logs_read=false line xuid
  out=$(console list) || return
  # `grep -a`: the log carries binary bytes, and grep otherwise refuses it.
  block=$(grep -a -A1 'players online:' <<<"$out" | tail -n 2 | tr -d '\r')
  [[ -n $block ]] || return 1
  if [[ $block != *'There are 0/'* ]]; then
    for name in $(tail -n 1 <<<"$block" | tr ',' ' '); do
      local known=false
      for pattern in $non_humans; do
        # shellcheck disable=SC2053 # Chart values deliberately use shell globs.
        if [[ $name == $pattern ]]; then
          known=true
          break
        fi
      done
      [[ $known == true ]] && continue
      # Read the whole log at most once per check: only an unnamed player
      # needs it, and it grows all day.
      if [[ $logs_read == false ]]; then
        connected=$(kubectl -n "$ns" logs "$pod" 2>/dev/null |
          grep -a 'Player connected: ' || true)
        logs_read=true
      fi
      # The last connect line wins: a player can reconnect, and only the
      # current session says whether they signed in.
      line=$(grep -aF "Player connected: $name, xuid:" <<<"$connected" | tail -n 1)
      xuid=${line#*xuid:}
      if [[ -z $line || $xuid == *[![:space:]]* ]]; then
        busy="player $name is online"
        return 0
      fi
    done
  fi

  out=$(console tickingarea list) || return
  grep -aq 'ticking areas in use' <<<"$out" || return 1
  if grep -aq '^- crew-course-' <<<"$out"; then
    busy="a rebuild already holds ticking areas"
    holders=$(sed -n 's/^- \(crew-course-[^:]*\):.*/\1/p' <<<"$out")
  fi
  return 0
}

# check_free as the run wants it: a server that is not accepting commands is
# a mid-restart pod (the very thing that orphans areas), so it is a skip, and
# unreadable state is a failure, never permission to build.
probe() {
  check_free
  case $? in
    0)
      ;;
    2)
      emit "skipped: server is not accepting commands"
      exit 0
      ;;
    *)
      emit "failed: could not read the server's players or ticking areas before $1"
      exit 1
      ;;
  esac
}

# Waits out the rebuild behind $holders instead of skipping the night: a
# live one removes its areas in an EXIT trap, so they vanish when it ends,
# while one whose server pod restarted mid-build cannot, and its areas stay
# in the world forever. The wait counts sleeps rather than clock time, so a
# stubbed sleep makes it instant in the test. Exits the script (skip or
# failure) if the wait itself meets a blocker; returns once the rebuild can
# go ahead: the areas are gone, or have been removed as stale.
wait_out_holders() {
  local field=$1 waited=0 name
  emit "waiting out the rebuild that holds ${holders//$'\n'/ } (up to ${stale_after}s)"
  while :; do
    sleep "$recheck_s"
    ((waited += recheck_s))
    probe "$field"
    [[ -z $busy ]] && return 0
    if [[ -z $holders ]]; then
      emit "skipped $field: $busy"
      exit 0
    fi
    if ((waited >= stale_after)); then
      emit "removing stale ticking areas: ${holders//$'\n'/ }"
      for name in $holders; do
        console tickingarea remove "$name" >/dev/null ||
          { emit "failed: could not remove stale ticking area $name"; exit 1; }
      done
      return 0
    fi
  done
}

if ! kubectl -n "$ns" get pod "$pod" -o jsonpath='{.status.phase}' 2>/dev/null | grep -q Running; then
  emit "skipped: server pod is not running"
  exit 0
fi

failed=0
for field in $FIELDS; do
  probe "$field"
  if [[ -n $busy && -z $holders ]]; then
    emit "skipped $field: $busy"
    exit 0
  fi
  if [[ -n $holders ]]; then
    wait_out_holders "$field"
  fi
  emit "rebuilding $field"
  if ! "$PLANS/courses.sh" --confirm-overwrite --plan "$PLANS/$field.plan" "$LAB" "$field"; then
    emit "failed: $field"
    failed=1
  fi
done
exit "$failed"
