#!/usr/bin/env bash
# Enforces ADR-009's public/internal split as code: every package at the
# module root except ./cmd/..., ./internal/... and anything under docs/ is
# a public package a third party can embed, and none of them may depend on
# anything under this module's own internal/ — an embedder cannot name a
# type it imports. Roadmap task L1's verification criterion
# ("go list -deps ./engine | grep rpkv/internal" empty) generalised to
# every public package, so the next one that reaches into internal/ fails
# the build instead of being caught by hand, the way engine/loop.go leaking
# internal/extcmd.Command through NewLoop's signature was.
set -euo pipefail

if ! command -v go >/dev/null 2>&1; then
    echo "check-boundaries: go is not installed" >&2
    exit 1
fi

root="${1:-$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)}"
cd "$root"

module="$(go list -m)"

violations=0
while IFS= read -r pkg; do
    import_path="${pkg#"${module}"/}"
    case "$import_path" in
    cmd | cmd/* | internal | internal/* | docs | docs/*)
        continue
        ;;
    esac

    deps="$(go list -deps "$pkg" 2>&1)" || {
        echo "check-boundaries: go list -deps failed for $pkg" >&2
        echo "$deps" >&2
        exit 1
    }

    offenders="$(printf '%s\n' "$deps" | grep -E "^${module}/internal(/|$)" || true)"
    if [[ -n "$offenders" ]]; then
        violations=$((violations + 1))
        echo "check-boundaries: $pkg imports internal package(s):" >&2
        printf '%s\n' "$offenders" | sed 's/^/  /' >&2
    fi
done < <(go list ./...)

if ((violations > 0)); then
    echo "check-boundaries: $violations public package(s) import ${module}/internal (ADR-009)" >&2
    exit 1
fi

echo "check-boundaries: clean"
