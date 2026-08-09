# Roadmap

Phases are gated by exit criteria, never by calendar. The active phase is the
first with unchecked tasks. Tasks are written to be self-contained: package,
contract, verification. Sessions check boxes here — this file is the single
source of truth for project state.

`[x]` done, criterion met. `[ ]` not done. `[~]` **blocked**: the work is
implemented but its verification criterion cannot be met with what this
project has access to, and the reason is written into the task itself along
with what would unblock it. A `[~]` does not hold up the phase and does not
make it the active one — later tasks proceed — but it is not `[x]` and never
silently becomes one.

## Phase 0 — Foundations

- [x] `git init`, `go mod init github.com/sonirico/rpkv`
- [x] Harness vendored from vigia: `scripts/` gates, `githooks/pre-push`,
      Stop hook in `.claude/settings.json`, agent definitions in
      `.claude/agents/`, `justfile`, `.golangci.yml`.
      Verification: `bash scripts/harness-test.sh` green in this repo.
- [x] `CLAUDE.md` with invariants and session protocol; `docs/SPEC.md`;
      `docs/adr/001..005`; `docs/PM-BRIEF.md`.
- [ ] **Toolchain green on a trivial package.** A first real package (pick
      `clock/`, it has no design risk) with a table-driven test, so `just
      check` and `just quality-pass` exercise end to end on real Go.
      Verification: `just check` exits 0; `quality-pass.json` written and
      accepted by `scripts/verify-receipts.sh`.
- [ ] **Local Redpanda dev loop.** `contrib/` or justfile recipe starting a
      single-node Redpanda in Docker for integration tests; a smoke test
      produces and consumes one record through franz-go.
      Verification: `just test-integration` (recipe added here) green
      locally with Docker running.
- [ ] CI: GitHub Actions running `just setup` + `just check`.
      Verification: green run. (Pends on first push to a remote.)

## Phase 1 — Sidecar MVP (semantics are the deliverable)

The complete semantic core against unmodified Redpanda (ADR-002). Every
task below is contract-first: the tests written here are the acceptance
suite for phase 3.

- [ ] **`index/`: Pebble schema + store.** `Index` with `Apply(batch)`,
      `Get(key) (Pointer, ok)`, `Checkpoint(partition) int64`; pointer
      apply and checkpoint advance in one Pebble batch; tombstone deletes;
      keyspace shape recorded in ADR-003 when it settles.
      Verification: property test — for arbitrary record sequences, index
      state ≡ naive `map[key]value` materialization at the same
      checkpoint (`pgregory.net/rapid`); crash-recovery test — reopen
      mid-stream, resume from checkpoint, idempotent re-apply.
- [ ] **`ingest/`: topic consumer → index.** franz-go consumer feeding
      `Apply` in partition order; restart resumes from Pebble checkpoints
      (never broker-side consumer groups — the checkpoint must be the
      index's, atomically).
      Verification: integration test against dev-loop Redpanda — produce,
      kill, restart, produce, `Get` sees last-write-wins; no record
      applied twice observable.
- [ ] **`fetch/`: path-A reader.** Single-record fetch at
      `(partition, offset)` via franz-go; returns exactly the record the
      pointer names or a typed miss.
      Verification: integration test — every key produced is fetched
      byte-identical via its index pointer, including after topic
      compaction runs.
- [ ] **`server/` + `cmd/rpkv/`: query surface + wiring.** `Get(topic,
      key) → (value, pointer, checkpoint)`; wiring owner is `cmd/rpkv`
      only.
      Verification: end-to-end test — produce through Redpanda, read
      through the server, tombstone, read misses.
- [ ] **Benchmark on record (feeds ADR-004's gate).** Read latency
      distribution of path A (p50/p99) and index rebuild rate, at a stated
      cardinality and value size; committed under `docs/benchmarks/`.
      Verification: the numbers exist, with the command that reproduces
      them.

## Phase 2 — Redpanda recon (spike, read-only)

- [ ] **Storage-layer map.** Where segments, the offset index, and the
      compaction (log housekeeping) worker live in the Redpanda source;
      what a byte-position hook would attach to; what churns across recent
      releases. Findings under `docs/spikes/redpanda-storage/` and
      amended into ADR-004/005.
      Verification: the spike doc answers ADR-004 gate item 1, and names
      the flag/property mechanism phase 3 will use.

## Phase 3 — In-broker index (the fork)

Gated on: phase 1 suite green, phase 2 spike on record. Governed by
ADR-005 (own checkout, feature-flagged, additive, rebaseable).

- [ ] Fork checkout + build reproduced; upstream test suite green
      unmodified.
- [ ] Topic property enabling the index; background index builder in the
      broker; lookup endpoint. Flag off ⇒ byte-identical to upstream
      (that is a test).
- [ ] Phase-1 acceptance suite green against the fork's endpoint.
- [ ] First rebase onto a newer upstream tag, on record.

## Phase 4 — Direct reads (path B)

Gated on ADR-004's three-item gate, in order. Not planned in detail until
phase 2 reports; the one fixed task:

- [ ] Compaction-hook correctness red test: compaction pass over an
      indexed topic, then every key readable via its byte position.
