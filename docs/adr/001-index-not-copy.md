# ADR-001 — Index, not copy

Status: Accepted.

## Decision

rpkv never stores a value. The key-value surface over a topic is a
secondary index `key → (partition, offset)`; the value is read from the
topic's log at query time. Pebble holds pointers and checkpoints, nothing
else.

## Why

This is the founding decision — everything that makes rpkv a product
rather than a worse Kafka Streams follows from it:

- **Zero payload duplication.** A state store duplicates every value; for
  TB-scale topics that is the dominant cost. Index entries are ~tens of
  bytes per key regardless of value size.
- **One source of truth.** The log is authoritative; the index is a
  disposable projection, rebuildable by replay. There is no divergence to
  reconcile and no second retention policy to operate.
- **Compaction alignment.** Kafka log compaction retains exactly the
  record each index entry points at (the latest per key), so a logical
  pointer can never dangle. See SPEC "Semantics".

## Cost, accepted knowingly

Every read pays a value fetch (broker round-trip on path A). Random
single-record fetches are the broker's worst access pattern. The roadmap's
whole arc — ending in direct segment reads (ADR-004) — exists to pay this
cost down. Workloads that read hot state at high rates should not use
rpkv; that is Kafka Streams' quadrant and we do not contest it.

## Reversal criterion

If phase 1 benchmarks show path-A read latency makes the product unusable
for its target workload (large values, modest read rates) *and* phase 4 is
shown infeasible in the recon spike, the founding bet is wrong and the
project pivots or stops. It does not quietly grow a value cache — that is
the one move this ADR forbids (SPEC "Non-goals").
