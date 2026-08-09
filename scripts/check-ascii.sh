#!/usr/bin/env bash
# Enforces plain ASCII across every tracked file: smart quotes, em dashes,
# arrows and math symbols (all of which crept into docs and comments) render
# inconsistently across terminals, editors and diff tools, and are easy to
# reintroduce one prose edit at a time. Catching the drift in the gate is the
# only way it stays fixed instead of getting re-typed in the next commit.
set -euo pipefail

root="${1:-$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)}"
cd "$root"

if ! command -v git >/dev/null 2>&1; then
    echo "check-ascii: git is not installed" >&2
    exit 1
fi

offenders="$(git ls-files -z | xargs -0 grep -lP '[^\x00-\x7F]' 2>/dev/null || true)"

if [[ -n "$offenders" ]]; then
    echo "check-ascii: non-ASCII characters found in tracked file(s):" >&2
    while IFS= read -r file; do
        echo "  $file" >&2
        grep -noP '[^\x00-\x7F]' "$file" | sed "s|^|    line |" >&2
    done <<<"$offenders"
    exit 1
fi

echo "check-ascii: clean"
