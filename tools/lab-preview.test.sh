#!/usr/bin/env bash
# Checks the pure parts of lab-preview: manifest version rewrite and the world
# pack lists. No docker needed.
set -euo pipefail
here=$(cd "$(dirname "$0")" && pwd)
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

src=$tmp/packs/demo
mkdir -p "$src/behavior_pack" "$src/resource_pack"
cat >"$src/behavior_pack/manifest.json" <<'J'
{"header":{"uuid":"b-head","version":[0,0,3]},
 "modules":[{"uuid":"b-mod","version":[0,0,3]}],
 "dependencies":[{"uuid":"r-head","version":[0,0,3]},{"module_name":"@minecraft/server","version":"1.x.x"}]}
J
cat >"$src/resource_pack/manifest.json" <<'J'
{"header":{"uuid":"r-head","version":[0,0,3]},
 "modules":[{"uuid":"r-mod","version":[0,0,3]}]}
J
echo keep >"$src/resource_pack/file.txt"
before=$(sha256sum "$src"/*/manifest.json)

out=$(python3 -I "$here/lab-preview-stage.py" "$tmp/packs" "$tmp/world" 12345)
[[ $out == "staged behavior=1 resource=1 version=99.12345.0" ]] || { echo "unexpected summary: $out" >&2; exit 1; }
[[ $before == "$(sha256sum "$src"/*/manifest.json)" ]] || { echo "source manifests were modified" >&2; exit 1; }
[[ -f $tmp/world/resource_packs/demo/file.txt ]] || { echo "pack files not copied" >&2; exit 1; }

python3 -I - "$tmp/world" <<'PY'
import json, sys
w = sys.argv[1]
def load(p): return json.load(open(f"{w}/{p}"))
want = [99, 12345, 0]
b = load("behavior_packs/demo/manifest.json")
r = load("resource_packs/demo/manifest.json")
assert b["header"]["version"] == want
assert b["modules"][0]["version"] == want
assert b["dependencies"][0]["version"] == want
assert b["dependencies"][1]["version"] == "1.x.x"
assert r["header"]["version"] == want and r["modules"][0]["version"] == want
assert load("world_behavior_packs.json") == [{"pack_id": "b-head", "version": want}]
assert load("world_resource_packs.json") == [{"pack_id": "r-head", "version": want}]
PY

if python3 -I "$here/lab-preview-stage.py" "$tmp/empty" "$tmp/w2" 1 2>/dev/null; then
  echo "empty packs dir should fail" >&2
  exit 1
fi
if python3 -I "$here/lab-preview-stage.py" "$tmp/packs" "$tmp/w3" 65536 2>/dev/null; then
  echo "minor above 65535 should fail" >&2
  exit 1
fi
echo ok
