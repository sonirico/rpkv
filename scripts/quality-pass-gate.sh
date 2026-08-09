#!/usr/bin/env bash
# Stop hook: blocks ending a session with unpushed Go commits that have no
# quality-pass receipt. See CLAUDE.md's "Subagent contract" - the quality
# pass used to depend on the operator remembering to run it, and it wasn't.
# This is the structural replacement for that memory.
#
# It used to grep an ITERATE_SIGNAL line out of a .log file. That check was
# worthless: the signal originated as prose an agent composed, which
# dev:iterate's run.sh stored verbatim (`--write-results` is `cat > file`),
# so the gate was reading the agent's own claim about its own work. Twice in
# one session an agent that could not finish the loop wrote the log by hand
# instead, and only a human re-reading the file caught it.
#
# The gate now reads scripts/quality-pass.sh's JSON receipt, whose contents
# are measurements - exit codes, changed-file lists, per-function coverage -
# that the script computes itself. An agent can still decline to run it, but
# it cannot compose a passing one by writing paragraphs.
set -euo pipefail

payload="$(cat)"

stop_hook_active="$(jq -r '.stop_hook_active // false' <<<"$payload")"
if [[ "$stop_hook_active" == "true" ]]; then
    exit 0
fi

if ! git rev-parse --is-inside-work-tree >/dev/null 2>&1; then
    exit 0
fi

# Receipt paths and git ranges are repo-relative; do not trust the cwd the
# hook happened to inherit.
cd "$(git rev-parse --show-toplevel)"

current_branch="$(git rev-parse --abbrev-ref HEAD)"
if ! git rev-parse --abbrev-ref --symbolic-full-name "${current_branch}@{upstream}" >/dev/null 2>&1; then
    exit 0
fi

unpushed="$(git log '@{upstream}..HEAD' --oneline)"
if [[ -z "$unpushed" ]]; then
    exit 0
fi

go_files="$(git diff --name-only '@{upstream}..HEAD' -- '*.go')"
if [[ -z "$go_files" ]]; then
    exit 0
fi

head_sha="$(git rev-parse HEAD)"
receipt_dir=".claude/receipts/${head_sha}"
receipt="${receipt_dir}/quality-pass.json"

if [[ ! -s "$receipt" ]]; then
    {
        echo "unpushed Go commits (@{upstream}..HEAD) have no quality-pass receipt at ${receipt}"
        echo "run: bash scripts/quality-pass.sh"
    } >&2
    exit 2
fi

if ! jq -e . "$receipt" >/dev/null 2>&1; then
    echo "${receipt} is not valid JSON - regenerate it with scripts/quality-pass.sh" >&2
    exit 2
fi

# The receipt is keyed by directory name, but a receipt copied into the
# right directory would pass on that alone. Check the sha it recorded too.
receipt_sha="$(jq -r '.sha // ""' "$receipt")"
if [[ "$receipt_sha" != "$head_sha" ]]; then
    {
        echo "${receipt} records sha ${receipt_sha}, but HEAD is ${head_sha}"
        echo "the tree moved after the pass ran - re-run scripts/quality-pass.sh"
    } >&2
    exit 2
fi

blocked=0

# Accepting a measured failure stays possible - some gaps genuinely need a
# live server, or production seams that should not be cut for a coverage
# number - but it costs an explicit <name>.accepted file stating why, which
# is a decision on the record rather than a sentence in a chat log. Per
# CLAUDE.md that file is the main session's to write, never the agent's
# whose work is being gated.
check_exit="$(jq -r '.check.exit_code // 1' "$receipt")"
if [[ "$check_exit" != "0" ]] && [[ ! -s "${receipt_dir}/check.accepted" ]]; then
    {
        echo "quality-pass receipt records 'just check' exit ${check_exit} - the suite does not pass"
        echo "  fix it, or record why it is acceptable in ${receipt_dir}/check.accepted"
    } >&2
    blocked=1
fi

tests_only="$(jq -r '.tests_only_block // false' "$receipt")"
coverage_measured="$(jq -r '.coverage.measured // false' "$receipt")"
if [[ "$tests_only" == "false" && "$coverage_measured" != "true" ]]; then
    {
        echo "this block changes production Go files but the receipt has no coverage measurement:"
        echo "  $(jq -r '.coverage.reason // "no reason recorded"' "$receipt")"
        echo "  fix it, or record why it is acceptable in ${receipt_dir}/coverage.accepted"
    } >&2
    [[ -s "${receipt_dir}/coverage.accepted" ]] || blocked=1
fi

uncovered_count="$(jq -r '.coverage.uncovered | length' "$receipt")"
if [[ "$uncovered_count" -gt 0 ]] && [[ ! -s "${receipt_dir}/coverage.accepted" ]]; then
    {
        echo "${uncovered_count} function(s) in the changed production files have 0% coverage:"
        jq -r '.coverage.uncovered[] | "  \(.file)  \(.func)"' "$receipt"
        echo "  test them, or record why in ${receipt_dir}/coverage.accepted"
    } >&2
    blocked=1
fi

if (( blocked )); then
    echo "a measured quality-pass failure is not a green pass - see CLAUDE.md \"Subagent contract\"." >&2
    exit 2
fi

exit 0
