#!/usr/bin/env bash
# Checks the field-test case table and argument handling without a cluster.
set -euo pipefail
here=$(cd "$(dirname "$0")" && pwd)
fail() { echo "fieldtest.test.sh: $*" >&2; exit 1; }

# shellcheck source=fieldtest.sh
FIELDTEST_LIB=1 source "$here/fieldtest.sh"

in_field() {
  local x=$1 z=$2
  if ((x >= -444 && x <= -301)); then ((z >= -500 && z <= -357))
  elif ((x >= -294 && x <= -151)); then ((z >= -500 && z <= -357))
  elif ((x >= 150 && x <= 293)); then ((z >= -700 && z <= -357))
  else return 1; fi
}

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
  # Every coordinate must be inside one of the four fields.
  jq -r '[.. | objects | select(has("x") and has("z")) | "\(.x) \(.z)"] | .[]' <<<"$body" |
    while read -r x z; do
      in_field "$x" "$z" || fail "$n: $x,$z is outside all four fields"
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

[[ $(nearest_road_z grounds -300) == -360 && $(nearest_road_z courses -430) == -452 && $(nearest_road_z trees -600) == -498 ]] || fail "nearest_road_z south"
[[ $(nearest_road_z works -690) == -698 && $(nearest_road_z works -600) == -606 && $(nearest_road_z works -550) == -560 ]] || fail "nearest_road_z works"
[[ $(side_of -420 -480) == trees && $(side_of -200 -480) == courses && $(side_of 0 -480) == hub &&
   $(side_of 220 -480) == grounds && $(side_of 220 -640) == works ]] || fail "side_of"
[[ -z $(route 220 -470 230 -480) ]] || fail "a destination in reach needs no route"
[[ $(route 300 -493 -277 -437 | tr '\n' ';') == "300 -498;152 -498;152 -438;-154 -438;-154 -452;-277 -452;" ]] || fail "east to west route: $(route 300 -493 -277 -437 | tr '\n' ';')"
[[ $(route 0 -400 221 -460 | tr '\n' ';') == "0 -438;152 -438;152 -452;221 -452;" ]] || fail "hub to grounds route"
[[ $(route -277 -437 -420 -480 | tr '\n' ';') == "-277 -452;-291 -452;-291 -438;-303 -438;-303 -498;-420 -498;" ]] || fail "courses to trees route: $(route -277 -437 -420 -480 | tr '\n' ';')"
[[ $(route 220 -460 253 -640 | tr '\n' ';') == "220 -452;199 -452;199 -560;199 -652;253 -652;" ]] || fail "grounds to works route: $(route 220 -460 253 -640 | tr '\n' ';')"

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
