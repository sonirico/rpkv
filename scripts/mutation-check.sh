#!/usr/bin/env bash
# Applies a single mutation to a file, proves the target test catches it,
# restores the file byte-identical, and writes a receipt. The receipt is
# the point: a mutation check nobody can re-run is worth nothing (see
# CLAUDE.md's "Subagent contract"), so this script - not an agent narrating
# a re-run - is the thing that produces the evidence on disk.
#
# Restore is always `cp` from the backup, never `git checkout` - a prior
# incident lost uncommitted work that way.
set -euo pipefail

if [[ $# -lt 3 || $# -gt 4 ]]; then
    echo "usage: mutation-check.sh <file> <sed-expression> <go-test-pkg> [go-test-run-pattern]" >&2
    exit 1
fi

file="$1"
sed_expr="$2"
test_pkg="$3"
test_run="${4:-}"

if [[ ! -f "$file" ]]; then
    echo "mutation-check: $file does not exist" >&2
    exit 1
fi

if ! git diff --quiet -- "$file"; then
    echo "mutation-check: $file already has uncommitted changes, refusing to run" >&2
    exit 1
fi

test_args=(test "$test_pkg")
if [[ -n "$test_run" ]]; then
    test_args+=(-run "$test_run")
fi

# The baseline. "The test fails under mutation" only means something if the
# test passed without it: against an already-red test every mutation looks
# detected, and the receipt would record a proof that was never run.
set +e
baseline_output="$(go "${test_args[@]}" 2>&1)"
baseline_exit=$?
set -e
if [[ $baseline_exit -ne 0 ]]; then
    {
        echo "mutation-check: the target test does not pass before mutating (exit ${baseline_exit})"
        echo "a mutation is only caught by a test that was green to begin with"
        echo "$baseline_output"
    } >&2
    exit 1
fi

backup_dir="$(mktemp -d)"
backup_file="${backup_dir}/$(basename "$file")"
cp "$file" "$backup_file"

restore() {
    cp "$backup_file" "$file"
}

# An interrupt between the sed and the explicit restore must not leave the
# mutation in the tree. restore is idempotent, so firing it again on a
# clean exit is harmless.
trap 'restore; rm -rf "$backup_dir"' EXIT

sed -i "$sed_expr" "$file"

if cmp -s "$backup_file" "$file"; then
    restore
    echo "mutation-check: sed expression did not change $file - mutation did not apply" >&2
    exit 1
fi

# A mutation that stops the package compiling is not a behavioural mutation:
# `go test` exits non-zero for a build failure exactly as it does for a failing
# assertion, so without this the script recorded mutation_detected=true for a
# typo that no test ever ran against. `-run '^$'` builds the test binary and
# runs nothing, which is the compile check and only the compile check.
set +e
build_output="$(go test -run '^$' "$test_pkg" 2>&1)"
build_exit=$?
set -e
if [[ $build_exit -ne 0 ]]; then
    restore
    {
        echo "mutation-check: the mutation does not compile - that is a broken edit, not a caught bug"
        echo "$build_output"
    } >&2
    exit 1
fi

set +e
test_output="$(go "${test_args[@]}" 2>&1)"
test_exit_code=$?
set -e

restore

if ! git diff --quiet -- "$file"; then
    echo "mutation-check: restore left $file dirty" >&2
    echo "$test_output" >&2
    exit 1
fi
restored_clean=true

mutation_detected=false
if [[ "$test_exit_code" -ne 0 ]]; then
    mutation_detected=true
fi

head_sha="$(git rev-parse HEAD)"
receipt_dir=".claude/receipts/${head_sha}"
mkdir -p "$receipt_dir"

expr_hash="$(printf '%s' "$sed_expr" | sha1sum | cut -c1-8)"
receipt_file="${receipt_dir}/mutation-$(basename "$file")-${expr_hash}.json"
ts="$(date -u +%Y-%m-%dT%H:%M:%SZ)"

jq -n \
    --arg file "$file" \
    --arg mutation "$sed_expr" \
    --arg test_pkg "$test_pkg" \
    --arg test_run "$test_run" \
    --argjson baseline_exit_code "$baseline_exit" \
    --argjson test_exit_code "$test_exit_code" \
    --argjson mutation_detected "$mutation_detected" \
    --argjson restored_clean "$restored_clean" \
    --arg ts "$ts" \
    '{file: $file, mutation: $mutation, test_pkg: $test_pkg, test_run: $test_run, baseline_exit_code: $baseline_exit_code, test_exit_code: $test_exit_code, mutation_detected: $mutation_detected, restored_clean: $restored_clean, ts: $ts}' \
    >"$receipt_file"

if [[ "$mutation_detected" != "true" ]]; then
    echo "mutation-check: test did not fail under mutation (exit $test_exit_code) - mutation not detected" >&2
    exit 1
fi

echo "mutation-check: OK - receipt at $receipt_file"
exit 0
