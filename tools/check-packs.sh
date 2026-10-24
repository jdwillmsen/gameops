#!/usr/bin/env bash
# Validates every add-on pack under packs/. Usage: tools/check-packs.sh [pack-dir...]
set -euo pipefail
root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
exec python3 -I "$root/tools/packs/check_packs.py" "$@"
