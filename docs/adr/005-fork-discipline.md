# ADR-005 — Fork discipline

Status: Accepted (binding from phase 3; the recon spike in phase 2 obeys
its read-only parts).

## Decision

The Redpanda fork is managed as a **minimal, rebaseable patch series**,
never a divergence:

- **Own checkout.** The fork lives in its own repository/checkout
  (locally `/redpanda/`, gitignored here). This repo holds the sidecar,
  the semantics suite, the docs and the harness; it never vendors broker
  sources.
- **Feature-flagged.** Everything rpkv adds is behind a topic property
  (working name `redpanda.kv_index.enabled`, settled in phase 3). With the
  flag off, the fork's behavior and performance are byte-identical to
  upstream — that is a test, not an aspiration.
- **Additive surface.** No changes to the Kafka wire protocol, existing
  on-disk formats, or existing config semantics. The index is a new
  background fiber + a new store beside the log, and (path B) a
  subscription to compaction events. Touching the segment writer itself is
  out of scope until an amendment here says otherwise.
- **Patch hygiene.** The series stays small enough to rebase onto upstream
  releases; each patch has one concern; a patch that upstream would
  plausibly accept is preferred over one they would not. Periodic rebases
  onto upstream tags are roadmap tasks, not background hope.
- **Acceptance is the sidecar suite** (ADR-002): the phase-1 semantics
  tests run against the fork's endpoint before any fork change merges.

## Why

Forks die of divergence. Every upstream release either costs a rebase or
costs staleness, and both costs grow with diff size. Keeping the diff
minimal, flagged and additive is what keeps the fork a *feature branch of
Redpanda* rather than a new broker we now own alone. The upstream-accept
preference is deliberate: the best end state for path A's in-broker index
is upstreaming it, and the patch hygiene keeps that door open.

## Reversal criterion

If two consecutive upstream rebases each require rewriting the compaction
hook (the format/worker churns faster than we can track), path B is
re-evaluated against staying on path A in-broker — the fork keeps the
index but drops byte positions. That decision lands as an amendment here
with the rebase logs as evidence.
