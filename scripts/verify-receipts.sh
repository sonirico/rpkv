#!/usr/bin/env bash
# Deterministic verification of the receipts for a given sha. This replaces
# the `jq` one-liner over quality-pass.json and mutation-*.json the PM
# session was re-typing inline every block — see docs/PM-BRIEF.md Step 0.3
# ("the second time you find yourself writing the same inline script, stop
# and vendor it").
#
# The pass/fail decision mirrors scripts/quality-pass-gate.sh's forgiveness
# rules exactly, on purpose: this script and the Stop hook must never
# disagree about whether a receipt is good enough, or the operator ends up
# trusting whichever one they ran last. It does not reimplement that
# decision from scratch; it reads the same receipt fields with the same
# .accepted forgiveness.
set -euo pipefail

cd "$(git rev-parse --show-toplevel)"

target="${1:-HEAD}"
sha="$(git rev-parse --verify --quiet "$target")" || {
    echo "verify-receipts: '$target' does not resolve to a commit" >&2
    exit 1
}

receipt_dir=".claude/receipts/${sha}"
receipt="${receipt_dir}/quality-pass.json"

if [[ ! -d "$receipt_dir" ]]; then
    echo "verify-receipts: no receipt directory at ${receipt_dir}" >&2
    exit 1
fi

if [[ ! -s "$receipt" ]]; then
    echo "verify-receipts: no quality-pass.json at ${receipt}" >&2
    echo "  run: bash scripts/quality-pass.sh" >&2
    exit 1
fi

if ! jq -e . "$receipt" >/dev/null 2>&1; then
    echo "verify-receipts: ${receipt} is not valid JSON" >&2
    exit 1
fi

receipt_sha="$(jq -r '.sha // ""' "$receipt")"
if [[ "$receipt_sha" != "$sha" ]]; then
    echo "verify-receipts: ${receipt} records sha ${receipt_sha}, but the target is ${sha}" >&2
    exit 1
fi

blocked=0

echo "verify-receipts: ${receipt_dir}"
echo

check_exit="$(jq -r '.check.exit_code // 1' "$receipt")"
tests_only="$(jq -r '.tests_only_block // false' "$receipt")"
coverage_measured="$(jq -r '.coverage.measured // false' "$receipt")"
coverage_reason="$(jq -r '.coverage.reason // "no reason recorded"' "$receipt")"
uncovered_count="$(jq -r '.coverage.uncovered | length' "$receipt")"

echo "quality-pass.json:"
echo "  check.exit_code:      ${check_exit}"
echo "  tests_only_block:     ${tests_only}"
echo "  coverage.measured:    ${coverage_measured}"
echo "  coverage.reason:      ${coverage_reason}"
echo "  coverage.uncovered:   ${uncovered_count}"
if [[ "$uncovered_count" -gt 0 ]]; then
    jq -r '.coverage.uncovered[] | "    \(.file)  \(.func)"' "$receipt"
fi
echo

if [[ "$check_exit" != "0" ]] && [[ ! -s "${receipt_dir}/check.accepted" ]]; then
    echo "  FAIL: check exited ${check_exit} with no check.accepted" >&2
    blocked=1
fi

if [[ "$tests_only" == "false" && "$coverage_measured" != "true" ]]; then
    if [[ ! -s "${receipt_dir}/coverage.accepted" ]]; then
        echo "  FAIL: coverage not measured (${coverage_reason}) with no coverage.accepted" >&2
        blocked=1
    fi
fi

if [[ "$uncovered_count" -gt 0 ]] && [[ ! -s "${receipt_dir}/coverage.accepted" ]]; then
    echo "  FAIL: ${uncovered_count} uncovered function(s) with no coverage.accepted" >&2
    blocked=1
fi

# .accepted files are what turn a measured failure into a recorded decision
# — list every one present, whether or not it was needed above, so the
# operator sees what has been forgiven and why.
accepted_files=()
while IFS= read -r -d '' f; do
    accepted_files+=("$f")
done < <(find "$receipt_dir" -maxdepth 1 -name '*.accepted' -print0 | sort -z)

echo ".accepted files: ${#accepted_files[@]}"
for f in "${accepted_files[@]+"${accepted_files[@]}"}"; do
    name="$(basename "$f")"
    rationale="$(head -n1 "$f")"
    echo "  ${name}: ${rationale}"
done
echo

mutation_files=()
while IFS= read -r -d '' f; do
    mutation_files+=("$f")
done < <(find "$receipt_dir" -maxdepth 1 -name 'mutation-*.json' -print0 | sort -z)

echo "mutation receipts: ${#mutation_files[@]}"
if [[ ${#mutation_files[@]} -eq 0 ]]; then
    echo "  none — most blocks have none, this is not by itself a failure"
else
    for f in "${mutation_files[@]}"; do
        m_file="$(jq -r '.file // ""' "$f")"
        m_mutation="$(jq -r '.mutation // ""' "$f")"
        m_test_pkg="$(jq -r '.test_pkg // ""' "$f")"
        m_detected="$(jq -r '.mutation_detected // false' "$f")"
        m_restored="$(jq -r '.restored_clean // false' "$f")"
        echo "  $(basename "$f"):"
        echo "    file:              ${m_file}"
        echo "    mutation:          ${m_mutation}"
        echo "    test_pkg:          ${m_test_pkg}"
        echo "    mutation_detected: ${m_detected}"
        echo "    restored_clean:    ${m_restored}"
        if [[ "$m_detected" != "true" || "$m_restored" != "true" ]]; then
            echo "    FAIL: mutation_detected and restored_clean must both be true" >&2
            blocked=1
        fi
    done
fi

if (( blocked )); then
    echo >&2
    echo "verify-receipts: FAIL — see above" >&2
    exit 1
fi

echo
echo "verify-receipts: OK"
exit 0
