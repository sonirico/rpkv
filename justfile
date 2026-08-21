set shell := ["bash", "-uc"]

# CI installs the toolchain by running `just setup`, so these pins are the
# single source of truth for both. They are pins rather than @latest for one
# reason: with CI depending on them, @latest turns somebody else's release
# into a red build on a commit that did not touch anything.
golangci_version := "v2.12.2"
goimports_version := "v0.48.0"
golines_version := "v0.13.0"

local_prefix := "github.com/sonirico/rpkv"

# The manual dev-loop broker pin. Not authoritative: integration tests
# self-provision their broker via internal/rptest (ADR-006), whose image
# pin is the source of truth - keep this one matching it.
redpanda_image := "docker.redpanda.com/redpandadata/redpanda:v26.1.15"
redpanda_container := "rpkv-redpanda"
redpanda_port := "19092"

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

test:
    go test ./...

test-race:
    go test ./... -race

# Start the single-node dev-loop Redpanda container - a manual convenience
# for poking a broker with rpk by hand, NOT part of the test path since
# ADR-006 (tests self-provision via internal/rptest; to point them here,
# RPKV_TEST_BROKERS=localhost:{{ redpanda_port }}, accepting that this
# broker lacks rptest's cluster config). Dropping a .env file with
# RPKV_TEST_BROKERS=localhost:19092 in the integration package directory
# (e.g. fetch/.env) achieves the same without exporting.
# Already running is a no-op,
# stopped is restarted in place (never removed+recreated), absent is
# created. Existence/status comes from `docker ps` filters (non-empty
# output is the whole signal) rather than `docker inspect | jq`, so the
# recipe has no jq dependency and no failure mode where a missing jq
# silently reads as "container absent". Blocks until `rpk cluster health`
# reports healthy so a chained command never races the broker. The
# --add-host alias exists because the default bridge network resolves no
# container names, yet the advertised internal listener is
# {{ redpanda_container }}:9092 - without the alias any
# `docker exec ... rpk` command that dials brokers from metadata (group
# list, for one) fails on its own hostname.
redpanda-up:
    #!/usr/bin/env bash
    set -euo pipefail
    if [[ -n "$(docker ps -q --filter name=^{{ redpanda_container }}$ --filter status=running)" ]]; then
        : # already running - no-op
    elif [[ -n "$(docker ps -aq --filter name=^{{ redpanda_container }}$)" ]]; then
        docker start {{ redpanda_container }} >/dev/null
    else
        docker run -d --name {{ redpanda_container }} \
            -p {{ redpanda_port }}:{{ redpanda_port }} \
            --add-host {{ redpanda_container }}:127.0.0.1 \
            {{ redpanda_image }} \
            redpanda start --mode dev-container --smp 1 --overprovisioned \
            --kafka-addr internal://0.0.0.0:9092,external://0.0.0.0:{{ redpanda_port }} \
            --advertise-kafka-addr internal://{{ redpanda_container }}:9092,external://localhost:{{ redpanda_port }} \
            >/dev/null
    fi
    for _ in $(seq 1 60); do
        if docker exec {{ redpanda_container }} rpk cluster health 2>/dev/null | grep -q "Healthy:.*true"; then
            echo "redpanda-up: broker ready on localhost:{{ redpanda_port }}"
            exit 0
        fi
        sleep 1
    done
    echo "redpanda-up: broker did not report healthy within 60s"
    exit 1

# Remove the dev-loop container; idempotent, no error if it is already gone.
# Absence is checked with `docker ps -aq` up front so the only case
# tolerated is "nothing to remove" - a genuine `docker rm` failure (daemon
# down, permissions) still fails the recipe instead of reporting success.
redpanda-down:
    #!/usr/bin/env bash
    set -euo pipefail
    if [[ -z "$(docker ps -aq --filter name=^{{ redpanda_container }}$)" ]]; then
        exit 0
    fi
    docker rm -f {{ redpanda_container }} >/dev/null

# Integration tests self-provision one Redpanda per test binary through
# internal/rptest + testit/redpanda (ADR-006); the only requirement is a
# running Docker daemon. -p 1 serializes package binaries: testit's pool
# uses one fixed docker network name, and one broker at a time keeps runs
# deterministic under load - which benchmarks will rely on.
test-integration:
    go test -tags integration -p 1 ./... -race

# No -race: the race detector's instrumentation overhead skews latency
# measurements. The JSON it writes to docs/benchmarks/read-latency.json is
# the artifact the docs copy numbers from.
bench-read:
    RPKV_BENCH=1 RPKV_BENCH_OUT={{justfile_directory()}}/docs/benchmarks/read-latency.json go test -tags integration -p 1 -run TestReadLatencyBenchmark ./internal/app -v -count=1

bench-index-size:
    RPKV_BENCH=1 RPKV_BENCH_OUT={{justfile_directory()}}/docs/benchmarks/index-size.json go test -run TestIndexSizeBenchmark ./index -v -count=1

bench-shard-index-size:
    RPKV_BENCH=1 RPKV_BENCH_OUT={{justfile_directory()}}/docs/benchmarks/sharding-index-size.json go test -run TestShardingIndexSizeBenchmark ./index -v -count=1

bench-rebuild:
    RPKV_BENCH=1 RPKV_BENCH_OUT={{justfile_directory()}}/docs/benchmarks/rebuild-rate.json go test -tags integration -p 1 -run TestRebuildRateBenchmark ./internal/app -v -count=1

# RPKV_TEST_TIERED provisions a MinIO resource alongside the self-provisioned
# broker and boots it with tiered storage enabled; internal/rptest.Main
# handles both, no manual setup needed beyond a running Docker daemon.
bench-tiered-read:
    RPKV_BENCH=1 RPKV_TEST_TIERED=1 RPKV_BENCH_OUT={{justfile_directory()}}/docs/benchmarks/tiered-read-latency.json go test -tags integration -p 1 -run TestTieredReadLatencyBenchmark ./internal/app -v -count=1

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

# The pure-Go invariant (ADR-002): the binary must build with CGo off.
build:
    CGO_ENABLED=0 go build ./...

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
