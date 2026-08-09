#!/usr/bin/env bash
# git pre-push hook: refuses a push whose commit range carries Go changes
# with no passing quality-pass receipt at the tip being pushed.
#
# scripts/quality-pass-gate.sh's Stop hook only guards *ending a session*
# with unpushed Go commits. It does not guard `git push` itself, and one
# session walked straight past it: the main session committed a docs change
# on top of an implementer's unreviewed Go commit and ran `git push` before
# review; the Go commit rode along silently underneath, and by the time the
# session ended there was nothing left unpushed for the Stop hook to catch.
# This is failure #1's shape from CLAUDE.md's ledger again, one layer
# closer to the actual event: a rule that says "run the pass before
# pushing" is not enforced by anyone remembering it. This is the structural
# fix at the one point a push cannot walk around - git refuses to complete
# the push if this hook exits non-zero.
#
# Git's protocol: invoked as `pre-push <remote-name> <remote-url>`, and on
# stdin one line per ref being pushed: `<local-ref> <local-sha> <remote-ref>
# <remote-sha>`.
#
# The pass/fail rules below mirror scripts/quality-pass-gate.sh's exactly,
# on purpose, the same way scripts/verify-receipts.sh's do: two gates
# reading the same receipt must never disagree about it, or the operator
# ends up trusting whichever one they ran last.
set -euo pipefail

cd "$(git rev-parse --show-toplevel)"

zero_sha="0000000000000000000000000000000000000000"
empty_tree="4b825dc642cb6eb9a060e54bf8d69288fbee4904"

blocked=0

while read -r local_ref local_sha remote_ref remote_sha; do
    [[ -z "${local_ref:-}" ]] && continue

    if [[ "$local_sha" == "$zero_sha" ]]; then
        # A branch deletion. There is no new tree to check.
        continue
    fi

    if [[ "$remote_sha" == "$zero_sha" ]]; then
        # A new branch, with nothing on the remote side to diff against.
        # Diffing the whole local history against the empty tree would flag
        # every Go file the branch has ever touched - including everything
        # already reviewed and pushed on other branches - as "new" here, so
        # scope to the commits this push actually introduces: those not yet
        # reachable from any remote-tracking ref. The range for the Go-file
        # check is then from the fork point (the parent of the oldest such
        # commit) to the tip.
        new_commits="$(git rev-list "$local_sha" --not --remotes 2>/dev/null || true)"
        if [[ -z "$new_commits" ]]; then
            # Every commit here is already known to some remote; nothing new
            # to review under this push.
            continue
        fi
        oldest="$(git rev-list "$local_sha" --not --remotes --reverse | head -1)"
        base="$(git rev-parse --verify --quiet "${oldest}^" 2>/dev/null || echo "$empty_tree")"
        range="${base}..${local_sha}"
    else
        range="${remote_sha}..${local_sha}"
    fi

    go_files="$(git diff --name-only "$range" -- '*.go')"
    if [[ -z "$go_files" ]]; then
        # Same carve-out the Stop hook makes: a docs-only push stays
        # frictionless because there is nothing here a receipt could speak
        # to.
        continue
    fi

    # The receipt is keyed to the *tip* sha being pushed, not to every
    # commit in the range. A push of several commits where only the tip
    # carries a receipt is the normal, correct case - the block is reviewed
    # as a whole and the receipt describes the final tree - so this does
    # not require one receipt per commit, only one that matches what is
    # actually landing on the remote.
    receipt_dir=".claude/receipts/${local_sha}"
    receipt="${receipt_dir}/quality-pass.json"

    if [[ ! -s "$receipt" ]]; then
        {
            echo "pre-push: ${local_ref} carries Go changes (${range}) with no quality-pass receipt at ${receipt}"
            echo "  run: bash scripts/quality-pass.sh"
        } >&2
        blocked=1
        continue
    fi

    if ! jq -e . "$receipt" >/dev/null 2>&1; then
        echo "pre-push: ${receipt} is not valid JSON - regenerate it with scripts/quality-pass.sh" >&2
        blocked=1
        continue
    fi

    receipt_sha="$(jq -r '.sha // ""' "$receipt")"
    if [[ "$receipt_sha" != "$local_sha" ]]; then
        {
            echo "pre-push: ${receipt} records sha ${receipt_sha}, but ${local_ref} is pushing ${local_sha}"
            echo "  the tree moved after the pass ran - re-run scripts/quality-pass.sh"
        } >&2
        blocked=1
        continue
    fi

    check_exit="$(jq -r '.check.exit_code // 1' "$receipt")"
    if [[ "$check_exit" != "0" ]] && [[ ! -s "${receipt_dir}/check.accepted" ]]; then
        {
            echo "pre-push: quality-pass receipt records 'just check' exit ${check_exit} - the suite does not pass"
            echo "  fix it, or record why it is acceptable in ${receipt_dir}/check.accepted"
        } >&2
        blocked=1
    fi

    tests_only="$(jq -r '.tests_only_block // false' "$receipt")"
    coverage_measured="$(jq -r '.coverage.measured // false' "$receipt")"
    if [[ "$tests_only" == "false" && "$coverage_measured" != "true" ]]; then
        if [[ ! -s "${receipt_dir}/coverage.accepted" ]]; then
            {
                echo "pre-push: this block changes production Go files but the receipt has no coverage measurement:"
                echo "  $(jq -r '.coverage.reason // "no reason recorded"' "$receipt")"
                echo "  fix it, or record why it is acceptable in ${receipt_dir}/coverage.accepted"
            } >&2
            blocked=1
        fi
    fi

    uncovered_count="$(jq -r '.coverage.uncovered | length' "$receipt")"
    if [[ "$uncovered_count" -gt 0 ]] && [[ ! -s "${receipt_dir}/coverage.accepted" ]]; then
        {
            echo "pre-push: ${uncovered_count} function(s) in the changed production files have 0% coverage:"
            jq -r '.coverage.uncovered[] | "  \(.file)  \(.func)"' "$receipt"
            echo "  test them, or record why in ${receipt_dir}/coverage.accepted"
        } >&2
        blocked=1
    fi
done

if (( blocked )); then
    echo "pre-push: a measured quality-pass failure is not a green pass - see CLAUDE.md \"Subagent contract\"." >&2
    exit 1
fi

exit 0
