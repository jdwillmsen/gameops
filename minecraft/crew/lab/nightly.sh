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
#              block a rebuild (crew units and load-test bots, such as load-*)
#
# A rebuild replaces everything in a field's box, so it never starts while a
# human is in the world, or while another rebuild holds its ticking areas.
# Both are skips, not failures: they exit 0 so a busy night does not page.
set -uo pipefail

: "${LAB:?LAB is required}" "${FIELDS:?FIELDS is required}" "${PLANS:?PLANS is required}" "${NON_HUMAN:?NON_HUMAN is required}"
target=${LAB#k8s:}
ns=${target%%/*}
pod=${target#*/}

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

# Sets $busy to why the lab must be left alone, or empty if it is free.
# Returns 1 when the lab's state cannot be read: an unreadable list is not
# evidence of an empty server, and the cost of the two mistakes is not equal.
# Returns 2 when the server is not accepting commands.
check_free() {
  busy=
  local out block name pattern non_humans="${NON_HUMAN//,/ }"
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
      if [[ $known != true ]]; then
        busy="player $name is online"
        return 0
      fi
    done
  fi

  out=$(console tickingarea list) || return
  grep -aq 'ticking areas in use' <<<"$out" || return 1
  if grep -aq '^- crew-course-' <<<"$out"; then
    busy="a rebuild already holds ticking areas"
  fi
  return 0
}

if ! kubectl -n "$ns" get pod "$pod" -o jsonpath='{.status.phase}' 2>/dev/null | grep -q Running; then
  emit "skipped: server pod is not running"
  exit 0
fi

failed=0
for field in $FIELDS; do
  check_free
  case $? in
    0) ;;
    2)
      emit "skipped: server is not accepting commands"
      exit 0
      ;;
    *)
      emit "failed: could not read the server's players or ticking areas before $field"
      exit 1
      ;;
  esac
  if [[ -n $busy ]]; then
    emit "skipped $field: $busy"
    exit 0
  fi
  emit "rebuilding $field"
  if ! "$PLANS/courses.sh" --confirm-overwrite --plan "$PLANS/$field.plan" "$LAB" "$field"; then
    emit "failed: $field"
    failed=1
  fi
done
exit "$failed"
