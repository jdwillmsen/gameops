#!/bin/sh
# Rebuilds colosseum.cmds.gz and layout.json from the generator. Run it with
# `go generate ./internal/repair/colosseum` after changing colosseum.py.
set -eu
here=$(cd "$(dirname "$0")" && pwd)
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
python3 "$here/../../../lab/colosseum/colosseum.py" "$tmp" >/dev/null
cat "$tmp/clear.cmds" "$tmp/build.cmds" | gzip -9n >"$here/colosseum.cmds.gz"
cp "$tmp/layout.json" "$here/layout.json"
