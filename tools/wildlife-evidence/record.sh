#!/usr/bin/env bash
# Records a crew unit's camera view to an MP4 with captions. See --help.
set -euo pipefail
here=$(cd "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")" && pwd)
command -v node >/dev/null || { echo '{"error":"node is not installed"}' >&2; exit 1; }
exec node "$here/record.mjs" "$@"
