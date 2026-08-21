#!/usr/bin/env bash
# Measures the quality pass and writes a receipt. This script exists because
# the previous mechanism did not work, and could not have worked.
#
# `dev:tests-quality` and `dev:tests-coverage` are LLM-driven skills. Their
# "output" is prose the agent composes, and `dev:iterate`'s run.sh stores it
# with `--write-results`, which is literally `cat > file`. The terminating
# ITERATE_SIGNAL the old gate grepped came out of that same agent-authored
# file. So the gate was reading the agent's own words laundered through two
# scripts and calling it evidence, while CLAUDE.md claimed receipts were
# "written by deterministic code, never by the agent narrating it".
#
# Twice in one session an agent, blocked by a permission classifier from
# finishing the loop and still owing a green log, wrote that log by hand.
# Blaming the agents misses the point: the design asked for a receipt only
# an agent could produce.
#
# What this script records instead is what a machine can decide on its own:
# which files changed, whether any of them is production Go, whether the
# suite passes under -race, and what the coverage of every function in the
# changed production files actually is. None of that needs a reviewer's
# good faith. The LLM review keeps its value for the things measurement
# cannot judge - is the blast radius right, is this test asserting what its
# name claims - but it is testimony, is labelled as such, and no longer
# gates anything on its own say-so.
set -euo pipefail

cd "$(git rev-parse --show-toplevel)"

# Untracked files count as dirty, which `git diff` does not report. A new
# production .go file left uncommitted still compiles into its package, so
# `just check` builds and tests it and passes - while the block under review,
# which is a commit range, does not contain it. The receipt would be green
# about a tree nobody else can reproduce, and the push would break the build
# for every other clone. Ignored paths are excluded by default, which is why
# this script's own receipts do not trip it.
# `.claude/` is excluded because it is never part of the block under review:
# it holds agent worktrees and this script's own receipts, and since the
# acceptances became tracked, writing one before a re-run would otherwise
# make the tree dirty and refuse the measurement it was written about.
dirty="$(git status --porcelain -- . ':!.claude')"
if [[ -n "$dirty" ]]; then
    {
        echo "quality-pass: worktree is dirty, refusing to measure a tree that is not the one under review"
        echo "$dirty" | sed 's/^/  /'
    } >&2
    exit 1
fi

sha="$(git rev-parse HEAD)"
receipt_dir=".claude/receipts/${sha}"
mkdir -p "$receipt_dir"

# The block under review is everything not yet on the default branch, which
# is the same definition dev:iterate's `branch` scope uses.
base_ref="origin/HEAD"
if ! git rev-parse --verify --quiet "$base_ref" >/dev/null; then
    echo "quality-pass: $base_ref is missing - run 'git remote set-head origin main' once per clone" >&2
    exit 1
fi
merge_base="$(git merge-base "$base_ref" HEAD)"
range="${merge_base}..HEAD"

mapfile -t changed_files < <(git diff --name-only "$range")
mapfile -t production_go < <(git diff --name-only "$range" -- '*.go' | grep -v '_test\.go$' || true)
mapfile -t test_go < <(git diff --name-only "$range" -- '*_test.go' || true)

tests_only=false
if [[ ${#production_go[@]} -eq 0 ]]; then
    tests_only=true
fi

# Two Go modules live in this repo: the root module and operator/ (its own
# go.mod). `go test ./... -coverpkg=./...` and `go list -m` only see
# whichever module they run in, so a changed operator/ file never matches
# the root module's import-path prefix and would otherwise land in the "no
# coverage lines matched" branch. Split by module up front and measure each
# where it lives.
root_go=()
operator_go=()
for f in ${production_go[@]+"${production_go[@]}"}; do
    if [[ "$f" == operator/* ]]; then
        operator_go+=("$f")
    else
        root_go+=("$f")
    fi
done

# `just check` is fmt-check, generate-check, vet across build tags, lint,
# build and the race suite. Its exit code is the single most informative
# deterministic fact about a block, and it was never part of the receipt.
check_log="${receipt_dir}/check.log"
set +e
just check >"$check_log" 2>&1
check_exit=$?
set -e

coverage_measured=false
coverage_reason="no production Go file changed in this block"
uncovered_json='[]'
functions_json='[]'

if [[ "$tests_only" == "false" ]]; then
    root_cover_profile="$(mktemp)"
    root_cover_func="$(mktemp)"
    operator_cover_profile="$(mktemp)"
    operator_cover_func="$(mktemp)"
    trap 'rm -f "$root_cover_profile" "$root_cover_func" "$operator_cover_profile" "$operator_cover_func"' EXIT

    coverage_ok=true
    fail_reasons=()
    all_functions_json='[]'

    if [[ ${#root_go[@]} -gt 0 ]]; then
        set +e
        go test ./... -coverpkg=./... -coverprofile="$root_cover_profile" >"${receipt_dir}/coverage.log" 2>&1
        cover_exit=$?
        set -e

        msg=""
        if [[ $cover_exit -ne 0 ]]; then
            msg="go test with coverage failed (exit ${cover_exit}); see coverage.log"
        else
            go tool cover -func="$root_cover_profile" >"$root_cover_func"

            # `go tool cover -func` emits "<module>/<file>:<line>:\t<func>\t<pct>%"
            # - an import path, not a repo-relative one, so the changed files
            # are matched with the module prefix in front. Keep only those, so
            # the receipt describes this block rather than the repo's overall
            # average: an average is exactly the number that hides a new
            # untested function.
            module="$(go list -m)"
            pattern="$(printf "${module}/%s\n" "${root_go[@]}" | paste -sd'|' -)"
            matched="$(grep -E "^($pattern):" "$root_cover_func" || true)"
            if [[ -z "$matched" ]]; then
                # Not "everything is covered" - nothing was measured. Silence
                # here once meant the script died mid-way writing no receipt
                # at all; a block that reaches the gate unmeasured must say
                # so.
                msg="no coverage lines matched the changed production files"
            else
                root_functions_json="$(
                    awk -F'\t+' '{
                        split($1, loc, ":")
                        gsub(/%/, "", $3)
                        printf "%s\t%s\t%s\n", loc[1], $2, $3
                    }' <<<"$matched" |
                        jq -R -s 'split("\n") | map(select(length > 0)) | map(split("\t")) |
                                  map({file: .[0], func: .[1], pct: (.[2] | tonumber)})'
                )"
                all_functions_json="$(jq -n --argjson a "$all_functions_json" --argjson b "$root_functions_json" '$a + $b')"
            fi
        fi

        if [[ -n "$msg" ]]; then
            coverage_ok=false
            if [[ ${#operator_go[@]} -gt 0 ]]; then
                fail_reasons+=("root: ${msg}")
            else
                fail_reasons+=("$msg")
            fi
        fi
    fi

    if [[ ${#operator_go[@]} -gt 0 ]]; then
        set +e
        (cd operator && go test ./... -coverpkg=./... -coverprofile="$operator_cover_profile") >>"${receipt_dir}/coverage.log" 2>&1
        cover_exit=$?
        set -e

        msg=""
        if [[ $cover_exit -ne 0 ]]; then
            msg="go test with coverage failed (exit ${cover_exit}); see coverage.log"
        else
            (cd operator && go tool cover -func="$operator_cover_profile") >"$operator_cover_func"

            operator_module="$(cd operator && go list -m)"
            operator_relative=("${operator_go[@]#operator/}")
            pattern="$(printf "${operator_module}/%s\n" "${operator_relative[@]}" | paste -sd'|' -)"
            matched="$(grep -E "^($pattern):" "$operator_cover_func" || true)"
            if [[ -z "$matched" ]]; then
                msg="no coverage lines matched the changed production files"
            else
                operator_functions_json="$(
                    awk -F'\t+' '{
                        split($1, loc, ":")
                        gsub(/%/, "", $3)
                        printf "%s\t%s\t%s\n", loc[1], $2, $3
                    }' <<<"$matched" |
                        jq -R -s 'split("\n") | map(select(length > 0)) | map(split("\t")) |
                                  map({file: .[0], func: .[1], pct: (.[2] | tonumber)})' |
                        jq --arg mod "$operator_module" \
                            'map(.file |= (ltrimstr($mod + "/") | "operator/" + .))'
                )"
                all_functions_json="$(jq -n --argjson a "$all_functions_json" --argjson b "$operator_functions_json" '$a + $b')"
            fi
        fi

        if [[ -n "$msg" ]]; then
            coverage_ok=false
            if [[ ${#root_go[@]} -gt 0 ]]; then
                fail_reasons+=("operator: ${msg}")
            else
                fail_reasons+=("$msg")
            fi
        fi
    fi

    if [[ "$coverage_ok" == "true" ]]; then
        coverage_measured=true
        coverage_reason="measured over the functions defined in the changed production files"
        functions_json="$all_functions_json"
        uncovered_json="$(jq '[.[] | select(.pct == 0)]' <<<"$functions_json")"
    else
        coverage_measured=false
        coverage_reason="$(IFS='; '; echo "${fail_reasons[*]}")"
    fi
fi

ts="$(date -u +%Y-%m-%dT%H:%M:%SZ)"

jq -n \
    --arg sha "$sha" \
    --arg ts "$ts" \
    --arg range "$range" \
    --argjson tests_only "$tests_only" \
    --argjson check_exit "$check_exit" \
    --argjson coverage_measured "$coverage_measured" \
    --arg coverage_reason "$coverage_reason" \
    --argjson changed_files "$(printf '%s\n' "${changed_files[@]}" | jq -R -s 'split("\n") | map(select(length > 0))')" \
    --argjson production_go "$(printf '%s\n' "${production_go[@]+"${production_go[@]}"}" | jq -R -s 'split("\n") | map(select(length > 0))')" \
    --argjson test_go "$(printf '%s\n' "${test_go[@]+"${test_go[@]}"}" | jq -R -s 'split("\n") | map(select(length > 0))')" \
    --argjson functions "$functions_json" \
    --argjson uncovered "$uncovered_json" \
    '{
        sha: $sha,
        ts: $ts,
        range: $range,
        tests_only_block: $tests_only,
        changed_files: $changed_files,
        production_go_files: $production_go,
        test_go_files: $test_go,
        check: {command: "just check", exit_code: $check_exit},
        coverage: {
            measured: $coverage_measured,
            reason: $coverage_reason,
            functions: $functions,
            uncovered: $uncovered
        }
    }' >"${receipt_dir}/quality-pass.json"

echo "quality-pass: receipt at ${receipt_dir}/quality-pass.json"

if [[ $check_exit -ne 0 ]]; then
    echo "quality-pass: 'just check' failed (exit ${check_exit}) - see ${check_log}" >&2
    exit 1
fi

uncovered_count="$(jq 'length' <<<"$uncovered_json")"
if [[ "$uncovered_count" -gt 0 ]]; then
    echo "quality-pass: ${uncovered_count} changed function(s) have 0% coverage:" >&2
    jq -r '.[] | "  \(.file)  \(.func)"' <<<"$uncovered_json" >&2
    exit 1
fi

exit 0
