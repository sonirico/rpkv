# ADR-002 — Sidecar first, fork second

Status: Accepted.

## Decision

The end state is a Redpanda fork with the index in the broker. We do not
start there. Phase 1 builds the complete semantic core as a **Go sidecar**
against an *unmodified* Redpanda: franz-go consumer → Pebble index → query
API → path-A reads. Only after the sidecar's semantics suite is green and
its benchmarks are on record does fork work begin (phase 3), and the
sidecar suite becomes the fork's acceptance suite.

## Why

- **Semantics are the risk that is ours; the broker is the risk that is
  Redpanda's.** Last-write-wins, tombstones, atomic checkpointing, replay
  rebuild, the naive-materialization equivalence property — every one of
  these is testable in Go against a stock broker in weeks of surface, not
  a Seastar/C++ codebase of millions of lines. Discovering a semantic
  mistake inside the fork multiplies its cost by the fork's iteration
  time.
- **The suite transfers; the code does not have to.** The property tests
  and golden semantics of phase 1 are written against the *contract*
  (produce records → observe Get results), so they run unchanged against
  the in-broker implementation. That is the payoff that makes the sidecar
  a stepping stone rather than a detour.
- **The sidecar is independently shippable.** If the fork stalls, a
  correct sidecar with path-A reads is still a usable product — the
  original form of the idea.

## Layout consequence

Public packages at the module root (`index/`, `ingest/`, `fetch/`,
`server/`, `clock/`), wiring in `cmd/rpkv/` and `internal/`. Public
packages never import `internal/` (`scripts/check-boundaries.sh` enforces
it). The fork lives in its own checkout (`/redpanda/`, gitignored here) —
never vendored into this module (ADR-005).

## Reversal criterion

If the phase-2 recon spike finds that Redpanda already exposes an
extension point that makes the in-broker index cheaper to build directly
than the sidecar (e.g. a supported server-side transform/index hook),
phases collapse and this ADR is amended with the evidence.
