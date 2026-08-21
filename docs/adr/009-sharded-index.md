# ADR-009 - Sharded index: ownership recorded in Pebble

Status: Accepted.

## Decision

A shard owns a configured subset of a topic's partitions
(`--partitions`/`RPKV_PARTITIONS`, or `--partition-from-ordinal` deriving
the partition from the StatefulSet pod's hostname suffix). Ownership is
recorded as a single value in the shard's own index, under Pebble key
`0x03` (SPEC "Contracts"): mode byte plus the partition list. On open,
`EnsureOwnership` compares the recorded set against the configured one -
equal continues, different refuses the open with `ErrOwnershipMismatch`.
A data directory with no ownership record but existing checkpoints
predates sharding and is treated as the implicit "all" set. A data
directory with no record and no checkpoints adopts the configured set and
records it.

Topic shape reporting (`internal` topicshape collection feeding
`/healthz` and the Prometheus gauges) is deliberately left unfiltered by
ownership: a shard reports the full topic's partition count, not just its
own, so an operator can see a topic re-partitioned outside a shard's
configured set. `/healthz` reports the shard's own `partitions` alongside
the topic's overall `partition_count`.

## Why

A StatefulSet gives each shard a stable identity (pod ordinal) and a
stable PVC, but nothing stops the orchestrator from reattaching a PVC to
the wrong ordinal after a rolling restart or a manual intervention - the
volume for partitions `[0,1]` mounted onto the pod configured to own
`[2,3]`. Recording ownership inside the index itself, rather than trusting
the StatefulSet's assignment alone, makes that misconfiguration a refused
open instead of a shard silently serving keys it does not own or missing
keys it should.

Recording ownership as index state (not a side file or an external
coordinator) keeps the invariant local to the thing being protected and
needs no new dependency: the same Pebble batch and Get/Set path used for
pointers and checkpoints carries it.

Leaving topic shape reporting unfiltered means a shard is not blind to
re-partitioning that lands keys outside its configured set - the signal
`ingest/` and `/healthz` already carry for drift detection (Phase 4,
tasks 1 and 2) stays whole-topic, not narrowed to what this shard happens
to own.

## Consequences

Every open pays one extra Pebble read and, on first run, one extra write.
Reattaching a PVC to the wrong ordinal is caught at startup rather than
producing wrong answers at read time - the failure mode this ADR exists
to convert.

Sharding numbers on record (Phase 4, task 5) are this phase's exit
criterion: if the fan-out cost measured there does not pay back against a
single monolith, that task reverts sharding and ADR-008's independent,
unsharded replicas stand unchanged.
