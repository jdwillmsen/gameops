#!/usr/bin/env bash
set -euo pipefail

here=$(cd "$(dirname "$0")" && pwd)
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
mkdir -p "$work/bin" "$work/plans"

cat >"$work/bin/kubectl" <<'EOF'
#!/usr/bin/env bash
case " $* " in
  *" get pod "*) printf Running ;;
  *" exec "*) exit 0 ;;
  *" logs "*) cat "$KUBECTL_LOG" ;;
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

run() {
  local players=$1
  printf 'There are %s/30 players online:\n%s\nThere are 0 ticking areas in use\n' "$2" "$players" >"$work/log"
  PATH="$work/bin:$PATH" KUBECTL_LOG="$work/log" COURSES_CALL="$work/call" \
    LAB=k8s:lab/server FIELDS=grounds PLANS="$work/plans" NON_HUMAN='Agate load-*' \
    bash "$here/nightly.sh"
}

run Alex 1 >"$work/human.out"
grep -Fx 'nightly.sh: skipped grounds: player Alex is online' "$work/human.out"
[[ ! -e $work/call ]]

run 'Agate, load-1' 2 >"$work/automation.out"
grep -Fx 'nightly.sh: rebuilding grounds' "$work/automation.out"
grep -Fx -- "--confirm-overwrite --plan $work/plans/grounds.plan k8s:lab/server grounds" "$work/call"

echo 'PASS: nightly non-human players'
