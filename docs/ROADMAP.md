# Roadmap

Phases are gated by exit criteria, never by calendar. The active phase is the
first with unchecked tasks. Tasks are self-contained: package, contract,
verification - the contract details live in `docs/SPEC.md` "Contracts
(frozen)", referenced as SPEC section. **Nothing in this file is a proposal**: a
session's job is to implement the named task against its frozen contract,
not to re-decide it. Sessions check boxes here - this file is the single
source of truth for project state.

`[x]` done, criterion met. `[ ]` not done. `[~]` **blocked**: implemented
but its verification criterion cannot be met with what this project has
access to; the reason is written into the task, later tasks proceed, and it
never silently becomes `[x]`.

## Phase 0 - Foundations

- [x] `git init`, `go mod init github.com/sonirico/rpkv`
- [x] Harness vendored from vigia (scripts, hooks, agents, justfile);
      `bash scripts/harness-test.sh` green in this repo.
- [x] `CLAUDE.md`, `docs/SPEC.md` with frozen contracts, `docs/adr/`,
      `docs/PM-BRIEF.md`. Recon spike done and parked with the upstream
      plan in `../redpanda/rpkv-plan/`.
- [x] **`clock/`: the Clock interface.** `Clock` with `Now() time.Time`
      and `After(d time.Duration) <-chan time.Time`; `NewSystemClock()`;
      mock in `clock/clocktest/` with `Advance(d)`. Table-driven tests.
      Verification: `just check` exits 0 with the toolchain installed
      (`just setup` first); `just quality-pass` writes a receipt that
      `bash scripts/verify-receipts.sh` accepts.
- [x] **Local Redpanda dev loop.** justfile recipes `redpanda-up` /
      `redpanda-down`: single-node Redpanda in Docker
      (`docker.redpanda.com/redpandadata/redpanda`, fixed version pin,
      port 19092 external), plus `test-integration` running
      `go test -tags integration ./... -race`. First integration test:
      produce one record with franz-go, consume it back, byte-equal.
      Verification: `just redpanda-up && just test-integration` green
      locally, `just redpanda-down` leaves no container.
- [ ] CI: GitHub Actions running `just setup` + `just check`.
      Verification: green run. (Pends on first push to a remote.)

## Phase 1 - Core (contracts SPEC section; every package lands with its tests)

- [ ] **`index/`** per SPEC section `index/` and SPEC section Pebble-layout, verbatim
      signatures. Includes `Close`.
      Verification: property test (`pgregory.net/rapid`) - arbitrary
      entry sequences == naive map materialization at same checkpoints;
      atomicity test - reopen after simulated crash between polls, no
      checkpoint without its entries; tombstone test; `Checkpoint` -1
      semantics; unit-only, no broker.
- [ ] **`ingest/`** per SPEC section `ingest/`: partition assignment without
      groups, resume at checkpoint+1 or log start, one atomic `Apply` per
      poll per partition, null-key skip counted.
      Verification: integration test (dev loop) - produce N keys across
      partitions, kill ingester mid-stream, restart, produce more; final
      index == naive materialization; no broker-side group exists
      (`rpk group list` empty).
- [ ] **`fetch/`** per SPEC section `fetch/` implementing SPEC "Compaction model"
      mechanism 2 verbatim (batch-level record selection at the exact
      offset; `Superseded`/`Evicted` classification).
      Verification: integration tests - (a) every produced key fetched
      byte-identical via its pointer; (b) *superseded*: produce v2 for a
      key, force compaction (topic with aggressive
      `max.compaction.lag.ms`/segment size), fetch with the stale v1
      pointer -> `Superseded=true`; (c) *evicted*: delete-retention topic,
      pointer below log start -> `Evicted=true`.
- [ ] **`server/`** per SPEC section `server/`: routes, status codes, headers,
      2s supersede budget through `clock.Clock`.
      Verification: unit tests with fake index/fetcher covering every row
      of the SPEC response table, including supersede-then-resolve and
      supersede-then-503 via mock clock `Advance`; no `time.Sleep`
      anywhere.
- [ ] **`cmd/rpkv` + `internal/`** wiring per SPEC section configuration -
      wiring owner only, constructors receive dependencies.
      Verification: end-to-end integration test - start Redpanda, start
      rpkv, produce, `GET` returns the value with correct headers;
      tombstone -> 404; `CGO_ENABLED=0 go build ./...` succeeds and is
      asserted in CI.

## Phase 2 - Resilience proof (the compaction claims become tests)

- [ ] **Compaction contract suite.** Long-running integration test:
      compacted topic, thousands of overwrites across keys, compaction
      forced repeatedly, ingester restarted twice mid-run; at each
      quiescent point every live key `GET`s its latest value and every
      tombstoned key 404s. This is the suite the parked upstream plan
      reuses as acceptance - keep it black-box (produce -> observe HTTP).
- [ ] **Rebuild convergence.** Delete the Pebble dir after the suite
      above; re-ingest from the (compacted) log; assert the rebuilt index
      state equals the pre-delete state.
- [ ] **Crash-consistency sweep.** Kill -9 the process at randomized
      points under write load (harness script, seeded); on restart the
      invariants hold (no checkpoint ahead of applied entries; property
      re-check against naive consumer).

## Phase 3 - Numbers and release

- [ ] **Benchmarks on record** under `docs/benchmarks/` with reproduce
      commands: read p50/p99 (local segments), read latency for
      tiered-storage-evicted keys (or `[~]` with the exact blocker if no
      object store is available locally - MinIO is the unblock), index
      rebuild rate (keys/s), index bytes per key at 10^6 keys.
- [ ] **README for release**: what it is, the honest trade-off, quickstart
      against the dev loop, the compaction-resilience story, the
      benchmark table.
- [ ] Tag `v0.1.0`. Verification: fresh clone + `just setup && just
      check` green + quickstart works as written.

Phase 3's artifacts (contract suite + benchmarks) are the resume gate for
`../redpanda/rpkv-plan/UPSTREAM-PLAN.md`. When they exist, that plan wakes
up - over there, not here.
