# ADR-010 - Router mode: fan-out, never re-derive the partitioner

Status: Accepted.

## Context

Sharding (ADR-009) splits a topic's partitions across independent shard
processes, each with its own index. A client that talks to one shard
directly can only see the keys that shard's partitions own; a request
routed to the wrong shard is a false `404`, indistinguishable from the
key not existing at all. Something in front of the shards has to know
which shard, or shards, might hold a given key.

The obvious shortcut - have the router compute the producer's partition
for the key and address only that shard - is closed. rpkv's own read
path is read-verify-only, never compute (ADR-004, SPEC "Compaction
model"): the router has no more standing to recompute a producer's
partitioner than the sidecar has to recompute a value. Doing so would
also break silently across a re-partitioning: the producer's
partitioner output changes with partition count, but keys written under
the old count stay where they were written. A router that recomputes
the partition for the new count would look up the wrong shard for every
key still living in `p_old`, exactly for the topics this phase's
partition-refresh work (Phase 4, tasks 1 and 3) exists to keep serving
correctly.

## Decision

The router asks every shard and waits for every answer. There is no
shortcut and no cache of which shard owns which key by default.

Response status is chosen by precedence across the shard answers: `200`
(a hit) beats `410` (evicted) beats `503` (superseded, still catching
up) beats `400` (bad request) beats a transport error or any other
unexpected status (surfaced as `502`) beats `404` (no shard has the
key). A hit anywhere outranks every other outcome; an evicted answer is
still stronger evidence of past existence than a busy or malformed one;
`404` is the weakest answer because it is what every shard that simply
does not own the key also returns.

Two or more `200` answers for the same key are possible: a
re-partitioning leaves a live record for the same key in two partitions
until compaction catches up, and those partitions can be owned by two
different shards. The router resolves this honestly rather than
silently: it picks the highest `X-Rpkv-Timestamp` among the `200`
answers (best-effort, since producer clocks are not guaranteed to
agree or be monotonic), and marks the response `X-Rpkv-Ambiguous: true`
plus increments `rpkv_router_ambiguous_keys_total`, so the caller and
the metrics both see that the answer was arbitrated rather than unique.

A memoized key -> shard map is not forbidden by ADR-001 - it would
cache a pointer's location, not a value, the same category ADR-001
already allows for the index's own key -> pointer index. It stays off
for now: the fan-out cost has not been measured yet, and turning on a
cache before that measurement would make it impossible to attribute the
resulting numbers to fan-out avoidance versus everything else the
router does. It is deferred to whatever number Phase 4 task 5 produces
against `docs/benchmarks/read-latency.md`.

## Consequences

Every router read costs N shard requests instead of one, where N is the
shard count - fan-out concurrency bounds the added latency to the
slowest shard, not the sum, but it is still N times the connection and
request overhead of a single-shard read. Phase 4 task 5 measures this
against `docs/benchmarks/read-latency.md` and is this phase's exit
criterion: if the fan-out cost is not paid back, tasks 3 and 4
(partition-affine index and router mode) are reverted and ADR-008's
independent, unsharded replicas stand unchanged.
