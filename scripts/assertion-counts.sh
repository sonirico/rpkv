#!/usr/bin/env bash
# Assertion-count diff for the current block: for every changed *_test.go
# file, compares `grep -c 'require\.\|assert\.'` at HEAD against the same
# file on the base branch. CLAUDE.md's "Subagent contract" has the PM run
# this by hand every block - "a silently dropped assertion during a
# call-site rewrite is exactly what slips through everything else" - this
# vendors it instead of re-typing the grep each time (docs/PM-BRIEF.md
# Step 0.3).
#
# Block definition matches scripts/quality-pass.sh exactly:
# merge_base(origin/HEAD, HEAD)..HEAD. Two receipts describing different
# blocks for the same review would be worse than no receipt at all.
set -euo pipefail

cd "$(git rev-parse --show-toplevel)"

base_ref="${1:-origin/HEAD}"
if ! git rev-parse --verify --quiet "$base_ref" >/dev/null; then
    echo "assertion-counts: $base_ref is missing - run 'git remote set-head origin main' once per clone" >&2
    exit 1
fi

merge_base="$(git merge-base "$base_ref" HEAD)"
range="${merge_base}..HEAD"

# `git show <ref>:<path>` fails when the path does not exist at that ref -
# a new or deleted file, exactly the cases this script has to call out
# rather than silently score as "decreased to zero". Missing -> 0, not an
# error.
count_at() {
    local ref="$1" path="$2"
    git show "${ref}:${path}" 2>/dev/null | grep -c 'require\.\|assert\.' || true
}

mapfile -t changed < <(git diff --name-status "$range" -- '*_test.go')

if [[ ${#changed[@]} -eq 0 ]]; then
    echo "assertion-counts: no *_test.go files changed in ${range}"
    exit 0
fi

decreased=0

printf 'assertion-counts: %s\n\n' "$range"
printf '%-10s %-8s %-8s %s\n' "STATUS" "BASE" "HEAD" "FILE"

for entry in "${changed[@]}"; do
    IFS=$'\t' read -r status path path2 <<<"$entry"

    case "$status" in
    A)
        head_count="$(count_at HEAD "$path")"
        printf '%-10s %-8s %-8s %s\n' "NEW" "-" "$head_count" "$path"
        ;;
    D)
        base_count="$(count_at "$merge_base" "$path")"
        printf '%-10s %-8s %-8s %s\n' "DELETED" "$base_count" "-" "$path"
        echo "  assertions gone with the file - confirm that is intended"
        ;;
    R*)
        base_count="$(count_at "$merge_base" "$path")"
        head_count="$(count_at HEAD "$path2")"
        if [[ "$head_count" -lt "$base_count" ]]; then
            printf '%-10s %-8s %-8s %s -> %s\n' "DECREASED" "$base_count" "$head_count" "$path" "$path2"
            decreased=1
        else
            printf '%-10s %-8s %-8s %s -> %s\n' "RENAMED" "$base_count" "$head_count" "$path" "$path2"
        fi
        ;;
    *)
        base_count="$(count_at "$merge_base" "$path")"
        head_count="$(count_at HEAD "$path")"
        if [[ "$head_count" -lt "$base_count" ]]; then
            printf '%-10s %-8s %-8s %s\n' "DECREASED" "$base_count" "$head_count" "$path"
            decreased=1
        else
            printf '%-10s %-8s %-8s %s\n' "OK" "$base_count" "$head_count" "$path"
        fi
        ;;
    esac
done

echo

if (( decreased )); then
    echo "assertion-counts: at least one file's assertion count decreased - see DECREASED rows above" >&2
    exit 1
fi

echo "assertion-counts: no decreases"
exit 0
