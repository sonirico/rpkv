set shell := ["bash", "-uc"]

# CI installs the toolchain by running `just setup`, so these pins are the
# single source of truth for both. They are pins rather than @latest for one
# reason: with CI depending on them, @latest turns somebody else's release
# into a red build on a commit that did not touch anything.
golangci_version := "v2.12.2"
goimports_version := "v0.48.0"
golines_version := "v0.13.0"

local_prefix := "github.com/sonirico/rpkv"

# Every hand-written Go file. .claude/ is excluded: agent worktrees are
# gitignored copies of this same repo; formatting them reaches outside the
# checkout and does nothing useful.
go_files := "$(find . -name '*.go' -not -path './.claude/*')"

_default:
    @just --list

# Install the dev toolchain (goimports, golines, golangci-lint).
setup: && install-hooks
    go install golang.org/x/tools/cmd/goimports@{{ goimports_version }}
    go install github.com/segmentio/golines@{{ golines_version }}
    go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@{{ golangci_version }}
    go mod download
    @echo "toolchain ready"

# Point git at the tracked githooks/ directory instead of the untracked,
# per-clone .git/hooks/ - a hook installed there is invisible to `git
# clone` and has to be reinstalled by hand on every checkout, which is
# exactly the kind of step CLAUDE.md's ledger says gets forgotten.
# core.hooksPath replaces the *entire* hooks directory, so warn about any
# untracked hook it would silently bypass.
install-hooks:
    #!/usr/bin/env bash
    set -euo pipefail
    existing="$(find .git/hooks -maxdepth 1 -type f ! -name '*.sample' 2>/dev/null || true)"
    if [[ -n "$existing" ]]; then
        echo "install-hooks: .git/hooks has untracked hook(s) that core.hooksPath will bypass:"
        echo "$existing" | sed 's/^/  /'
        echo "  (githooks/ takes over from here - pre-push in particular - move anything you still need into githooks/)"
    fi
    git config core.hooksPath githooks
    echo "install-hooks: core.hooksPath -> githooks/ (pre-push gate active)"

# Format (gofmt, goimports, golines).
fmt:
    gofmt -w {{ go_files }}
    goimports -local {{ local_prefix }} -w {{ go_files }}
    golines -w {{ go_files }}

# Same three, reporting instead of writing. Used by `just check`.
fmt-check:
    #!/usr/bin/env bash
    set -uo pipefail
    # A formatter that is not installed prints nothing to stdout, and the
    # emptiness of `unformatted` is the whole pass criterion - so a missing
    # tool read as "formatting clean" and exited 0. `just check`'s exit code
    # is what scripts/quality-pass.sh records as evidence, which makes a green
    # that measured nothing worse here than a red. Assert the tools first;
    # `just setup` installs them at the pins above.
    for tool in gofmt goimports golines; do
        if ! command -v "$tool" >/dev/null 2>&1; then
            echo "fmt-check: ${tool} is not installed - run 'just setup'"
            exit 1
        fi
    done
    files="$(find . -name '*.go' -not -path './.claude/*')"
    unformatted="$(gofmt -l $files; goimports -local {{ local_prefix }} -l $files; golines -l $files)"
    if [[ -n "$unformatted" ]]; then
        echo "not formatted (run 'just fmt'):"
        echo "$unformatted" | sort -u
        exit 1
    fi
    echo "formatting clean"

vet:
    go vet ./...

lint:
    golangci-lint run

build:
    go build ./...

test:
    go test ./...

test-race:
    go test ./... -race

# Public packages must not import internal/ (ADR-002's layout rule).
boundaries-check:
    bash scripts/check-boundaries.sh .

# No non-ASCII characters in any tracked file (docs, scripts, comments).
ascii-check:
    bash scripts/check-ascii.sh .

# The gates are themselves tested, first, so a green from any of them
# means something. Every case in the suite is a defect that shipped.
harness-test:
    bash scripts/harness-test.sh

check: harness-test ascii-check fmt-check vet lint build boundaries-check test-race

# The gate. Writes .claude/receipts/<sha>/quality-pass.json.
quality-pass:
    bash scripts/quality-pass.sh

# Operator ritual, not a check() step: verifies .claude/receipts/<sha>/
# deterministically - quality-pass.json's sha and exit codes, .accepted
# files, mutation-*.json - instead of re-deriving the same jq one-liner
# every block.
verify-receipts sha="":
    bash scripts/verify-receipts.sh {{ sha }}
