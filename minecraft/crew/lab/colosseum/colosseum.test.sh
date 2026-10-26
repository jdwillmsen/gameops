#!/usr/bin/env bash
# Generates the Colosseum and checks what the rest of the lab depends on: the
# layout the director reads matches the checked-in copy, every fill fits the
# build box and the server's per-command limit, and the plan arena.sh feeds
# starts with ticking areas small enough to load.
set -euo pipefail
here=$(cd "$(dirname "$0")" && pwd)
fail() { echo "colosseum.test.sh: $*" >&2; exit 1; }

python3 -c 'import numpy' 2>/dev/null || { echo "skip: numpy is not installed"; exit 0; }

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
python3 "$here/colosseum.py" "$tmp" >/dev/null || fail "generator failed (a pen may not be sealed)"

diff -q "$tmp/layout.json" "$here/layout.json" >/dev/null ||
  fail "layout.json is stale: cp the generator's layout.json over colosseum/layout.json"

python3 - "$tmp" <<'PY' || fail "build output is out of bounds"
import json, re, sys

work = sys.argv[1]
layout = json.load(open(f"{work}/layout.json"))
cx, cz = layout["center"]["x"], layout["center"]["z"]
assert len(layout["pens"]) == 8
for p in layout["pens"]:
    g = p["gate"]
    assert sorted([g["x1"] - g["x0"] + 1, g["z1"] - g["z0"] + 1]) == [1, 3], p["id"]
    assert g["y1"] - g["y0"] + 1 == 3 and p["open"].endswith(" air"), p["id"]

pat = re.compile(r"fill (-?\d+) (-?\d+) (-?\d+) (-?\d+) (-?\d+) (-?\d+) (\S+)")
for name in ("clear.cmds", "build.cmds"):
    for line in open(f"{work}/{name}"):
        m = pat.match(line)
        assert m, line
        x0, y0, z0, x1, y1, z1 = map(int, m.groups()[:6])
        assert cx - 120 <= x0 <= x1 <= cx + 119 and cz - 106 <= z0 <= z1 <= cz + 105 and 36 <= y0 <= y1 <= 122, line
        assert (x1 - x0 + 1) * (y1 - y0 + 1) * (z1 - z0 + 1) <= 32768, line
        assert not m.group(7).startswith("__"), line

areas = [l.split() for l in open(f"{work}/plan.txt") if l.startswith("# area")]
assert len(areas) == 4
for _, _, x0, z0, x1, z1 in areas:
    chunks = ((int(x1) >> 4) - (int(x0) >> 4) + 1) * ((int(z1) >> 4) - (int(z0) >> 4) + 1)
    assert chunks <= 100, chunks
PY
echo ok
