#!/usr/bin/env bash
# Checks the field-test case table and argument handling without a cluster.
set -euo pipefail
here=$(cd "$(dirname "$0")" && pwd)
fail() { echo "fieldtest.test.sh: $*" >&2; exit 1; }

FIELDTEST_LIB=1 source "$here/fieldtest.sh"

((${#case_names[@]} >= 20)) || fail "only ${#case_names[@]} cases"
declare -A seen
for n in "${case_names[@]}"; do
  [[ -z ${seen[$n]:-} ]] || fail "duplicate case $n"
  seen[$n]=1
  [[ $n =~ ^[a-z0-9-]+$ ]] || fail "case name '$n' must be lowercase words and dashes"
  [[ ${case_expect[$n]} == "done" || ${case_expect[$n]} == failed ]] || fail "$n: expect must be done or failed"
  body=$(task_body "${case_task[$n]}")
  jq -e . >/dev/null <<<"$body" || fail "$n: task is not JSON"
  if [[ -n ${case_pre[$n]} ]]; then
    [[ ${case_pre[$n]} =~ ^-?[0-9]+\ -?[0-9]+\ -?[0-9]+$ ]] || fail "$n: pre must be 'x y z'"
  fi
  # Every coordinate must be inside one of the two fields.
  jq -r '[.. | objects | select(has("x") and has("z")) | "\(.x) \(.z)"] | .[]' <<<"$body" |
    while read -r x z; do
      if ! { ((x >= -294 && x <= -151)) || ((x >= 150 && x <= 293)); } || ((z < -500 || z > -357)); then
        fail "$n: $x,$z is outside both fields"
      fi
    done
  jq -e 'if .template == "gather_and_deposit" and .area then
           ([.area.max.x - .area.min.x, .area.max.y - .area.min.y, .area.max.z - .area.min.z] | all(. < 64))
         else true end' >/dev/null <<<"$body" || fail "$n: area spans more than 64"
done

[[ $(task_body "goto 1 2 3") == '{"template":"goto","to":{"x":1,"y":2,"z":3}}' ]] || fail "goto shorthand"
[[ $(goto_target "goto 1 2 3") == "1 2 3" ]] || fail "goto_target"
[[ -z $(goto_target '{"template":"haul"}') ]] || fail "goto_target on a non-goto"
[[ $(select_cases 'pad-*' | wc -l) -eq 3 ]] || fail "pad-* should match 3 cases"
[[ -z $(select_cases 'nothing-*') ]] || fail "unmatched glob should select nothing"

[[ $(nearest_road_z -300) == -360 && $(nearest_road_z -430) == -452 && $(nearest_road_z -600) == -498 ]] || fail "nearest_road_z"
[[ -z $(route 220 -470 230 -480) ]] || fail "a destination in reach needs no route"
[[ $(route 300 -493 -277 -437 | tr '\n' ';') == "300 -498;152 -498;152 -438;-154 -438;-154 -452;-277 -452;" ]] || fail "east to west route: $(route 300 -493 -277 -437 | tr '\n' ';')"
[[ $(route 0 -400 221 -460 | tr '\n' ';') == "0 -438;152 -438;152 -452;221 -452;" ]] || fail "hub to grounds route"

script="$here/fieldtest.sh"
code=0; "$script" --bogus >/dev/null 2>&1 || code=$?
((code == 2)) || fail "unknown option exits $code, want 2"
code=0; "$script" compose >/dev/null 2>&1 || code=$?
((code == 2)) || fail "non-k8s target exits $code, want 2"
code=0; "$script" --reset courses k8s:x >/dev/null 2>&1 || code=$?
((code == 2)) || fail "--reset without --confirm-overwrite exits $code, want 2"
code=0; "$script" --reset moon --confirm-overwrite k8s:x >/dev/null 2>&1 || code=$?
((code == 2)) || fail "--reset of an unknown field exits $code, want 2"
code=0; "$script" k8s:x '*' 0 >/dev/null 2>&1 || code=$?
((code == 2)) || fail "zero timeout exits $code, want 2"
[[ $("$script" --list 'pad-1*') == "pad-13" ]] || fail "--list glob"
echo ok
