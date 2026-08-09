#!/usr/bin/env bash
# Tests for the gates themselves.
#
# Six defects were found in this harness by hand in a single session, every
# one of them a green that measured nothing: a receipt written about a tree
# with an uncommitted production file in it, a mutation "caught" by a test
# that never compiled, a formatter check that passed because the formatter
# was absent. Each was demonstrated with a red test typed at a prompt, and
# each of those tests was then thrown away — which is the same mistake in a
# different place. A check that lives only in a transcript does not exist,
# and the gates are the one part of this repo where a silent failure is
# indistinguishable from success.
#
# So every case below is a defect that actually shipped, kept executable.
# They run against throwaway git repositories under TMPDIR, touch nothing in
# this checkout, and are wired into `just check` because a suite nobody is
# forced to run is a suite that rots.
set -uo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT

tests_run=0
tests_failed=0
current=""

ok() {
    tests_run=$((tests_run + 1))
    printf '  ok    %s\n' "$current"
}

bad() {
    tests_run=$((tests_run + 1))
    tests_failed=$((tests_failed + 1))
    printf '  FAIL  %s\n        %s\n' "$current" "$1"
    if [[ -n "${out:-}" ]]; then
        printf '%s\n' "$out" | sed 's/^/        > /'
    fi
}

# expect_exit <wanted> — assert on the exit code of the last `run` call.
expect_exit() {
    if [[ "$rc" == "$1" ]]; then ok; else bad "expected exit $1, got $rc"; fi
}

# expect_out <substring> — assert the last `run` said something specific.
# A gate that refuses for the wrong reason is a gate that will refuse the
# wrong thing later, so the message is part of the contract.
expect_out() {
    if [[ "$out" == *"$1"* ]]; then ok; else bad "expected output containing: $1"; fi
}

run() {
    current="$1"
    shift
    out="$("$@" 2>&1)"
    rc=$?
}

# new_repo <name> — a throwaway repository with one commit on main.
new_repo() {
    local dir="${work}/$1"
    mkdir -p "$dir"
    git -C "$dir" init -q
    git -C "$dir" symbolic-ref HEAD refs/heads/main
    git -C "$dir" config user.email harness@test
    git -C "$dir" config user.name harness
    echo "seed" > "${dir}/README"
    git -C "$dir" add -A
    git -C "$dir" commit -qm seed
    echo "$dir"
}

# new_repo_with_upstream <name> — the same, plus a bare remote it is level
# with, so `@{upstream}` resolves the way quality-pass-gate.sh needs.
new_repo_with_upstream() {
    local dir bare
    dir="$(new_repo "$1")"
    bare="${work}/$1.git"
    git init -q --bare "$bare"
    git -C "$dir" remote add origin "$bare"
    git -C "$dir" push -q -u origin main
    echo "$dir"
}

new_go_module() {
    local dir="${work}/$1"
    mkdir -p "$dir"
    git -C "$dir" init -q
    git -C "$dir" symbolic-ref HEAD refs/heads/main
    git -C "$dir" config user.email harness@test
    git -C "$dir" config user.name harness
    printf 'module example.com/%s\n\ngo 1.22\n' "$1" > "${dir}/go.mod"
    printf 'package m\n\nfunc Add(a, b int) int { return a + b }\n' > "${dir}/m.go"
    printf 'package m\n\nimport "testing"\n\nfunc TestAdd(t *testing.T) {\n\tif Add(1, 2) != 3 {\n\t\tt.Fatal("bad")\n\t}\n}\n' > "${dir}/m_test.go"
    git -C "$dir" add -A
    git -C "$dir" commit -qm seed
    echo "$dir"
}

# new_go_module_with_internal_import <name> — a throwaway module whose one
# public package, pub, imports the module's own internal/priv. The fixture
# check-boundaries.sh is for: a public package reaching into internal/.
new_go_module_with_internal_import() {
    local dir="${work}/$1"
    mkdir -p "$dir/pub" "$dir/internal/priv"
    git -C "$dir" init -q
    git -C "$dir" symbolic-ref HEAD refs/heads/main
    git -C "$dir" config user.email harness@test
    git -C "$dir" config user.name harness
    printf 'module example.com/%s\n\ngo 1.22\n' "$1" > "${dir}/go.mod"
    printf 'package priv\n\nfunc Do() {}\n' > "${dir}/internal/priv/priv.go"
    printf 'package pub\n\nimport "example.com/%s/internal/priv"\n\nfunc Do() { priv.Do() }\n' \
        "$1" > "${dir}/pub/pub.go"
    git -C "$dir" add -A
    git -C "$dir" commit -qm seed
    echo "$dir"
}

# new_repo_with_origin_head <name> — the same as new_repo_with_upstream,
# plus refs/remotes/origin/HEAD set explicitly. `git push -u` does not set
# it (only `git clone` or an explicit `remote set-head` do), and
# assertion-counts.sh resolves origin/HEAD directly rather than via
# @{upstream}, matching scripts/quality-pass.sh.
new_repo_with_origin_head() {
    local dir
    dir="$(new_repo_with_upstream "$1")"
    git -C "$dir" remote set-head origin main
    echo "$dir"
}

# A Stop hook payload. The hook exits early when it is already active, so
# the tests must say it is not.
payload='{"stop_hook_active": false}'

gate() {
    ( cd "$1" && printf '%s' "$payload" | bash "${root}/scripts/quality-pass-gate.sh" )
}

write_receipt() {
    local dir="$1" sha="$2" json="$3"
    mkdir -p "${dir}/.claude/receipts/${sha}"
    printf '%s' "$json" > "${dir}/.claude/receipts/${sha}/quality-pass.json"
}

# Adds an unpushed commit touching a .go file — the condition that makes the
# gate care at all.
add_unpushed_go_commit() {
    local dir="$1"
    printf 'package main\n\nfunc main() {}\n' > "${dir}/main.go"
    git -C "$dir" add -A
    git -C "$dir" commit -qm "add go"
}

echo "harness-test: quality-pass.sh"

repo="$(new_repo qp_dirty)"
echo "changed" >> "${repo}/README"
run "quality-pass refuses a modified tracked file" bash -c "cd '$repo' && bash '${root}/scripts/quality-pass.sh'"
expect_exit 1
expect_out "worktree is dirty"

# The defect: `git diff --quiet` does not report untracked files, so a new
# production .go file compiled into `just check` and passed, while the block
# under review — a commit range — did not contain it.
repo="$(new_repo qp_untracked)"
printf 'package m\n\nfunc Leftover() {}\n' > "${repo}/leftover.go"
run "quality-pass refuses an untracked .go file" bash -c "cd '$repo' && bash '${root}/scripts/quality-pass.sh'"
expect_exit 1
expect_out "leftover.go"

# The counterpart: .claude/ must not count, or writing an acceptance would
# refuse the re-measurement it was written about.
repo="$(new_repo qp_claude_excluded)"
mkdir -p "${repo}/.claude/receipts/abc"
echo "because" > "${repo}/.claude/receipts/abc/coverage.accepted"
run "quality-pass ignores .claude when deciding dirty" bash -c "cd '$repo' && bash '${root}/scripts/quality-pass.sh'"
expect_out "origin/HEAD is missing"

# origin/HEAD absent makes `branch` scope resolve to nothing, which every
# skill then reports success over. It must be an error, not a default.
repo="$(new_repo qp_no_head)"
run "quality-pass refuses a clone without origin/HEAD" bash -c "cd '$repo' && bash '${root}/scripts/quality-pass.sh'"
expect_exit 1
expect_out "git remote set-head"

echo "harness-test: quality-pass-gate.sh"

repo="$(new_repo_with_upstream gate_no_receipt)"
add_unpushed_go_commit "$repo"
run "gate blocks unpushed Go commits with no receipt" gate "$repo"
expect_exit 2
expect_out "no quality-pass receipt"

repo="$(new_repo_with_upstream gate_no_go)"
echo "prose" > "${repo}/NOTES.md"
git -C "$repo" add -A
git -C "$repo" commit -qm docs
run "gate allows unpushed commits that touch no Go" gate "$repo"
expect_exit 0

# A receipt copied into the right directory would otherwise pass on its
# directory name alone; and a receipt made before the last commit describes
# a tree that is no longer the one being pushed.
repo="$(new_repo_with_upstream gate_stale_sha)"
add_unpushed_go_commit "$repo"
sha="$(git -C "$repo" rev-parse HEAD)"
write_receipt "$repo" "$sha" '{"sha":"0000000000000000000000000000000000000000","tests_only_block":false,"check":{"exit_code":0},"coverage":{"measured":true,"uncovered":[]}}'
run "gate rejects a receipt whose sha is not HEAD" gate "$repo"
expect_exit 2
expect_out "the tree moved after the pass ran"

repo="$(new_repo_with_upstream gate_check_failed)"
add_unpushed_go_commit "$repo"
sha="$(git -C "$repo" rev-parse HEAD)"
write_receipt "$repo" "$sha" "{\"sha\":\"${sha}\",\"tests_only_block\":false,\"check\":{\"exit_code\":1},\"coverage\":{\"measured\":true,\"uncovered\":[]}}"
run "gate blocks a receipt recording a failed check" gate "$repo"
expect_exit 2
expect_out "the suite does not pass"

echo "accepted because the linter needs a live server" > "${repo}/.claude/receipts/${sha}/check.accepted"
run "gate allows a failed check with an acceptance beside it" gate "$repo"
expect_exit 0

repo="$(new_repo_with_upstream gate_uncovered)"
add_unpushed_go_commit "$repo"
sha="$(git -C "$repo" rev-parse HEAD)"
write_receipt "$repo" "$sha" "{\"sha\":\"${sha}\",\"tests_only_block\":false,\"check\":{\"exit_code\":0},\"coverage\":{\"measured\":true,\"uncovered\":[{\"file\":\"m.go\",\"func\":\"Untested\"}]}}"
run "gate blocks a function at 0% coverage" gate "$repo"
expect_exit 2
expect_out "Untested"

# An empty acceptance is not an acceptance: the gate tests -s, so a file
# touched to get past it does not.
: > "${repo}/.claude/receipts/${sha}/coverage.accepted"
run "gate is not satisfied by an empty acceptance" gate "$repo"
expect_exit 2

echo "the sixteen are interface markers" > "${repo}/.claude/receipts/${sha}/coverage.accepted"
run "gate allows 0% coverage with a written acceptance" gate "$repo"
expect_exit 0

# Production Go with no measurement at all is the case that must never read
# as a pass — silence here once meant the script had died writing nothing.
repo="$(new_repo_with_upstream gate_unmeasured)"
add_unpushed_go_commit "$repo"
sha="$(git -C "$repo" rev-parse HEAD)"
write_receipt "$repo" "$sha" "{\"sha\":\"${sha}\",\"tests_only_block\":false,\"check\":{\"exit_code\":0},\"coverage\":{\"measured\":false,\"reason\":\"nothing matched\",\"uncovered\":[]}}"
run "gate blocks production Go with no coverage measurement" gate "$repo"
expect_exit 2
expect_out "no coverage measurement"

repo="$(new_repo_with_upstream gate_tests_only)"
printf 'package m\n\nimport "testing"\n\nfunc TestX(t *testing.T) {}\n' > "${repo}/m_test.go"
git -C "$repo" add -A
git -C "$repo" commit -qm tests
sha="$(git -C "$repo" rev-parse HEAD)"
write_receipt "$repo" "$sha" "{\"sha\":\"${sha}\",\"tests_only_block\":true,\"check\":{\"exit_code\":0},\"coverage\":{\"measured\":false,\"reason\":\"no production Go\",\"uncovered\":[]}}"
run "gate allows a tests-only block with no coverage" gate "$repo"
expect_exit 0

echo "harness-test: pre-push-gate.sh"

# Feeds the hook stdin exactly as git's pre-push protocol does: one line of
# `<local-ref> <local-sha> <remote-ref> <remote-sha>`, argv is `<remote-name>
# <remote-url>`. push_gate takes the pre-built line rather than assembling it
# itself, because several cases below need to reuse the same line twice
# (once to see it blocked, again after writing a .accepted file).
push_gate() {
    local dir="$1" stdin_line="$2"
    ( cd "$dir" && printf '%s\n' "$stdin_line" | bash "${root}/scripts/pre-push-gate.sh" origin origin-url )
}

repo="$(new_repo_with_upstream pp_no_receipt)"
add_unpushed_go_commit "$repo"
local_sha="$(git -C "$repo" rev-parse HEAD)"
remote_sha="$(git -C "$repo" rev-parse origin/main)"
run "pre-push blocks a Go push with no receipt" \
    push_gate "$repo" "refs/heads/main ${local_sha} refs/heads/main ${remote_sha}"
expect_exit 1
# "no quality-pass receipt at" is this branch's own wording — distinct from
# the "is not valid JSON" message a missing file would also produce via the
# jq check further down, which happens to share the same receipt path.
expect_out "no quality-pass receipt at .claude/receipts/${local_sha}/quality-pass.json"

repo="$(new_repo_with_upstream pp_stale_sha)"
add_unpushed_go_commit "$repo"
local_sha="$(git -C "$repo" rev-parse HEAD)"
remote_sha="$(git -C "$repo" rev-parse origin/main)"
write_receipt "$repo" "$local_sha" '{"sha":"0000000000000000000000000000000000000000","tests_only_block":false,"check":{"exit_code":0},"coverage":{"measured":true,"uncovered":[]}}'
run "pre-push blocks a receipt whose sha is not the tip being pushed" \
    push_gate "$repo" "refs/heads/main ${local_sha} refs/heads/main ${remote_sha}"
expect_exit 1
expect_out "records sha"

repo="$(new_repo_with_upstream pp_clean)"
add_unpushed_go_commit "$repo"
local_sha="$(git -C "$repo" rev-parse HEAD)"
remote_sha="$(git -C "$repo" rev-parse origin/main)"
write_receipt "$repo" "$local_sha" "{\"sha\":\"${local_sha}\",\"tests_only_block\":false,\"check\":{\"exit_code\":0},\"coverage\":{\"measured\":true,\"uncovered\":[]}}"
run "pre-push allows a Go push with a clean passing receipt" \
    push_gate "$repo" "refs/heads/main ${local_sha} refs/heads/main ${remote_sha}"
expect_exit 0

repo="$(new_repo_with_upstream pp_check_failed)"
add_unpushed_go_commit "$repo"
local_sha="$(git -C "$repo" rev-parse HEAD)"
remote_sha="$(git -C "$repo" rev-parse origin/main)"
write_receipt "$repo" "$local_sha" "{\"sha\":\"${local_sha}\",\"tests_only_block\":false,\"check\":{\"exit_code\":1},\"coverage\":{\"measured\":true,\"uncovered\":[]}}"
stdin_line="refs/heads/main ${local_sha} refs/heads/main ${remote_sha}"
run "pre-push blocks a receipt recording a failed check" push_gate "$repo" "$stdin_line"
expect_exit 1
expect_out "the suite does not pass"

echo "accepted because ci flaked" > "${repo}/.claude/receipts/${local_sha}/check.accepted"
run "pre-push allows a failed check forgiven by check.accepted" push_gate "$repo" "$stdin_line"
expect_exit 0

repo="$(new_repo_with_upstream pp_unmeasured)"
add_unpushed_go_commit "$repo"
local_sha="$(git -C "$repo" rev-parse HEAD)"
remote_sha="$(git -C "$repo" rev-parse origin/main)"
write_receipt "$repo" "$local_sha" "{\"sha\":\"${local_sha}\",\"tests_only_block\":false,\"check\":{\"exit_code\":0},\"coverage\":{\"measured\":false,\"reason\":\"nothing matched\",\"uncovered\":[]}}"
run "pre-push blocks production Go with no coverage measurement" \
    push_gate "$repo" "refs/heads/main ${local_sha} refs/heads/main ${remote_sha}"
expect_exit 1
expect_out "no coverage measurement"

repo="$(new_repo_with_upstream pp_uncovered)"
add_unpushed_go_commit "$repo"
local_sha="$(git -C "$repo" rev-parse HEAD)"
remote_sha="$(git -C "$repo" rev-parse origin/main)"
write_receipt "$repo" "$local_sha" "{\"sha\":\"${local_sha}\",\"tests_only_block\":false,\"check\":{\"exit_code\":0},\"coverage\":{\"measured\":true,\"uncovered\":[{\"file\":\"main.go\",\"func\":\"Untested\"}]}}"
stdin_line="refs/heads/main ${local_sha} refs/heads/main ${remote_sha}"
run "pre-push blocks a function at 0% coverage" push_gate "$repo" "$stdin_line"
expect_exit 1
expect_out "Untested"

echo "the sixteen are interface markers" > "${repo}/.claude/receipts/${local_sha}/coverage.accepted"
run "pre-push allows 0% coverage forgiven by coverage.accepted" push_gate "$repo" "$stdin_line"
expect_exit 0

repo="$(new_repo_with_upstream pp_docs_only)"
echo "prose" > "${repo}/NOTES.md"
git -C "$repo" add -A
git -C "$repo" commit -qm docs
local_sha="$(git -C "$repo" rev-parse HEAD)"
remote_sha="$(git -C "$repo" rev-parse origin/main)"
run "pre-push allows a docs-only push with no receipt at all" \
    push_gate "$repo" "refs/heads/main ${local_sha} refs/heads/main ${remote_sha}"
expect_exit 0

repo="$(new_repo_with_upstream pp_delete)"
remote_sha="$(git -C "$repo" rev-parse origin/main)"
run "pre-push allows a branch deletion" \
    push_gate "$repo" "refs/heads/main 0000000000000000000000000000000000000000 refs/heads/main ${remote_sha}"
expect_exit 0

# The fork-point logic for a brand-new branch (remote-sha all zeroes): scope
# the diff to commits not yet reachable from any remote-tracking ref instead
# of diffing the whole local history against the empty tree.
repo="$(new_repo_with_upstream pp_new_branch)"
git -C "$repo" checkout -qb feature
add_unpushed_go_commit "$repo"
local_sha="$(git -C "$repo" rev-parse HEAD)"
run "pre-push blocks a brand-new branch carrying Go changes with no receipt" \
    push_gate "$repo" "refs/heads/feature ${local_sha} refs/heads/feature 0000000000000000000000000000000000000000"
expect_exit 1
expect_out "quality-pass.json"

echo "harness-test: mutation-check.sh"

mod="$(new_go_module mut_ok)"
run "mutation-check reports a mutation the test catches" \
    bash -c "cd '$mod' && bash '${root}/scripts/mutation-check.sh' m.go 's/a + b/a - b/' ./... TestAdd"
expect_exit 0
current="mutation-check restores the file byte-identical"
if git -C "$mod" diff --quiet -- m.go; then ok; else bad "m.go left dirty after the run"; fi
current="mutation-check receipt records detection and restoration"
sha="$(git -C "$mod" rev-parse HEAD)"
receipt="$(ls "${mod}/.claude/receipts/${sha}"/mutation-*.json 2>/dev/null | head -1)"
if [[ -n "$receipt" ]] && [[ "$(jq -r '.mutation_detected, .restored_clean, .baseline_exit_code' "$receipt" | paste -sd, -)" == "true,true,0" ]]; then
    ok
else
    bad "receipt missing or does not record detected/restored/baseline"
fi

# The defect: `go test` exits non-zero for a build failure exactly as it does
# for a failed assertion, so a mutation to an undefined identifier came out
# as a caught bug, having run no test at all.
mod="$(new_go_module mut_nocompile)"
run "mutation-check refuses a mutation that does not compile" \
    bash -c "cd '$mod' && bash '${root}/scripts/mutation-check.sh' m.go 's/a + b/a + bZZZ/' ./... TestAdd"
expect_exit 1
expect_out "does not compile"
current="mutation-check writes no receipt for a non-compiling mutation"
sha="$(git -C "$mod" rev-parse HEAD)"
if ! ls "${mod}/.claude/receipts/${sha}"/mutation-*.json >/dev/null 2>&1; then ok; else bad "a receipt was written anyway"; fi

# "The test fails under mutation" is only evidence if it passed without one.
mod="$(new_go_module mut_red_baseline)"
printf 'package m\n\nimport "testing"\n\nfunc TestAdd(t *testing.T) {\n\tt.Fatal("red on purpose")\n}\n' > "${mod}/m_test.go"
git -C "$mod" add -A
git -C "$mod" commit -qm "red test"
run "mutation-check refuses an already-failing target test" \
    bash -c "cd '$mod' && bash '${root}/scripts/mutation-check.sh' m.go 's/a + b/a - b/' ./... TestAdd"
expect_exit 1
expect_out "does not pass before mutating"

mod="$(new_go_module mut_dirty)"
printf 'package m\n\nfunc Add(a, b int) int { return b + a }\n' > "${mod}/m.go"
run "mutation-check refuses a target file with uncommitted changes" \
    bash -c "cd '$mod' && bash '${root}/scripts/mutation-check.sh' m.go 's/b + a/a - b/' ./... TestAdd"
expect_exit 1
expect_out "already has uncommitted changes"

echo "harness-test: check-boundaries.sh"

mod="$(new_go_module_with_internal_import bound_violation)"
run "check-boundaries reports a public package importing internal/" \
    bash "${root}/scripts/check-boundaries.sh" "$mod"
expect_exit 1
expect_out "example.com/bound_violation/pub"
expect_out "example.com/bound_violation/internal/priv"

printf 'package pub\n\nfunc Do() {}\n' > "${mod}/pub/pub.go"
run "check-boundaries is clean once the internal import is removed" \
    bash "${root}/scripts/check-boundaries.sh" "$mod"
expect_exit 0

echo "harness-test: verify-receipts.sh"

repo="$(new_repo vr_missing)"
run "verify-receipts fails when no receipt exists" \
    bash -c "cd '$repo' && bash '${root}/scripts/verify-receipts.sh'"
expect_exit 1
expect_out "no receipt directory"

repo="$(new_repo vr_bad_sha)"
sha="$(git -C "$repo" rev-parse HEAD)"
write_receipt "$repo" "$sha" '{"sha":"0000000000000000000000000000000000000000","tests_only_block":true,"check":{"exit_code":0},"coverage":{"measured":false,"uncovered":[]}}'
run "verify-receipts rejects a receipt whose sha disagrees with the target" \
    bash -c "cd '$repo' && bash '${root}/scripts/verify-receipts.sh'"
expect_exit 1
expect_out "records sha"

repo="$(new_repo vr_check_failed)"
sha="$(git -C "$repo" rev-parse HEAD)"
write_receipt "$repo" "$sha" "{\"sha\":\"${sha}\",\"tests_only_block\":true,\"check\":{\"exit_code\":1},\"coverage\":{\"measured\":false,\"reason\":\"no production go\",\"uncovered\":[]}}"
run "verify-receipts fails on a recorded check failure with no acceptance" \
    bash -c "cd '$repo' && bash '${root}/scripts/verify-receipts.sh'"
expect_exit 1
expect_out "check exited 1"

echo "accepted because ci flaked" > "${repo}/.claude/receipts/${sha}/check.accepted"
run "verify-receipts passes a check failure forgiven by check.accepted" \
    bash -c "cd '$repo' && bash '${root}/scripts/verify-receipts.sh'"
expect_exit 0
expect_out "mutation receipts: 0"
expect_out "none — most blocks have none"

repo="$(new_repo vr_mut_undetected)"
sha="$(git -C "$repo" rev-parse HEAD)"
write_receipt "$repo" "$sha" "{\"sha\":\"${sha}\",\"tests_only_block\":true,\"check\":{\"exit_code\":0},\"coverage\":{\"measured\":false,\"reason\":\"no production go\",\"uncovered\":[]}}"
printf '{"file":"m.go","mutation":"s/a/b/","test_pkg":"./...","mutation_detected":false,"restored_clean":true}' \
    >"${repo}/.claude/receipts/${sha}/mutation-m.go-aaaa.json"
run "verify-receipts fails on a mutation receipt that was not detected" \
    bash -c "cd '$repo' && bash '${root}/scripts/verify-receipts.sh'"
expect_exit 1
expect_out "mutation_detected and restored_clean must both be true"

printf '{"file":"m.go","mutation":"s/a/b/","test_pkg":"./...","mutation_detected":true,"restored_clean":true}' \
    >"${repo}/.claude/receipts/${sha}/mutation-m.go-aaaa.json"
run "verify-receipts passes a mutation receipt with both flags true" \
    bash -c "cd '$repo' && bash '${root}/scripts/verify-receipts.sh'"
expect_exit 0
expect_out "mutation receipts: 1"

echo "harness-test: assertion-counts.sh"

repo="$(new_repo ac_no_head)"
run "assertion-counts refuses a clone without origin/HEAD" \
    bash -c "cd '$repo' && bash '${root}/scripts/assertion-counts.sh'"
expect_exit 1
expect_out "git remote set-head"

repo="$(new_repo_with_origin_head ac_decrease)"
printf 'package m\n\nimport (\n\t"testing"\n\n\t"github.com/stretchr/testify/require"\n)\n\nfunc TestX(t *testing.T) {\n\trequire.Equal(t, 1, 1)\n\trequire.Equal(t, 2, 2)\n}\n' \
    >"${repo}/x_test.go"
git -C "$repo" add -A
git -C "$repo" commit -qm "add test with two assertions"
git -C "$repo" push -q origin main
printf 'package m\n\nimport (\n\t"testing"\n\n\t"github.com/stretchr/testify/require"\n)\n\nfunc TestX(t *testing.T) {\n\trequire.Equal(t, 1, 1)\n}\n' \
    >"${repo}/x_test.go"
git -C "$repo" add -A
git -C "$repo" commit -qm "drop an assertion"
run "assertion-counts reports a file whose count decreased" \
    bash -c "cd '$repo' && bash '${root}/scripts/assertion-counts.sh'"
expect_exit 1
expect_out "DECREASED"
expect_out "x_test.go"

repo="$(new_repo_with_origin_head ac_increase)"
printf 'package m\n\nimport (\n\t"testing"\n\n\t"github.com/stretchr/testify/require"\n)\n\nfunc TestX(t *testing.T) {\n\trequire.Equal(t, 1, 1)\n}\n' \
    >"${repo}/x_test.go"
git -C "$repo" add -A
git -C "$repo" commit -qm "add test with one assertion"
git -C "$repo" push -q origin main
printf 'package m\n\nimport (\n\t"testing"\n\n\t"github.com/stretchr/testify/require"\n)\n\nfunc TestX(t *testing.T) {\n\trequire.Equal(t, 1, 1)\n\trequire.Equal(t, 2, 2)\n}\n' \
    >"${repo}/x_test.go"
git -C "$repo" add -A
git -C "$repo" commit -qm "add an assertion"
run "assertion-counts passes a file whose count increased" \
    bash -c "cd '$repo' && bash '${root}/scripts/assertion-counts.sh'"
expect_exit 0
expect_out "OK"

repo="$(new_repo_with_origin_head ac_new_file)"
printf 'package m\n\nimport "testing"\n\nfunc TestNew(t *testing.T) {}\n' >"${repo}/new_test.go"
git -C "$repo" add -A
git -C "$repo" commit -qm "add new test file"
run "assertion-counts reports a new test file without treating it as a decrease" \
    bash -c "cd '$repo' && bash '${root}/scripts/assertion-counts.sh'"
expect_exit 0
expect_out "NEW"
expect_out "new_test.go"

echo
if (( tests_failed > 0 )); then
    echo "harness-test: ${tests_failed} of ${tests_run} checks FAILED"
    exit 1
fi
echo "harness-test: ${tests_run} checks passed"
exit 0
