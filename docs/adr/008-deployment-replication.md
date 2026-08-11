# ADR-008 - Deployment: independent replicas, no replication

Status: Accepted.

## Decision

rpkv replicas are fully independent. Each instance consumes the indexed
topic with its own franz-go client under direct partition assignment (no
consumer group, SPEC "Contracts") and owns a private Pebble index. There
is no replication protocol, no consensus, and no shared state between
instances - not even coordination over which partitions each instance
reads, since every instance reads all of them.

Horizontal read scaling is running N instances behind a load balancer.
Failover and recovery are both the same operation: start a fresh instance
and let it rebuild by replay, or restart on the same volume and resume
from its checkpoint (ADR-003). Neither path is special-cased; both are the
ordinary startup sequence.

Consistency between replicas is not coordinated. Each replica's visible
state is its own checkpoint's projection of the log; staleness is
per-replica and bounded by ingest lag, which is already observable
through the ingest-lag metric. No mechanism holds replicas at the same
checkpoint.

## Why

ADR-001 makes the log the single source of truth and the index a
disposable, rebuildable projection. Under that decision, replica
divergence is transient by construction: every replica is consuming the
same log and converges toward the same state as it catches up, and losing
a replica loses no data - only the depth of the projection it happened to
have. Being lenient about cross-replica consistency here is a property of
the design, not a risk being accepted for expedience:

- **No consensus to build or operate.** Coordinating checkpoints across
  replicas would mean either a leader-election protocol or a shared
  index - both directly contradict ADR-001's "no second retention policy
  to operate" and ADR-003's per-instance Pebble store.
- **Convergence is proven, not assumed.** The rebuild-convergence test
  (`internal/app/rebuild_integration_test.go`) is the executable evidence
  that rebuild-by-replay reproduces the exact same state a naive
  materialization would reach - the same property that lets any two
  replicas at the same checkpoint agree exactly.
- **Failure handling needs no special case.** Because state is disposable
  and rebuild is deterministic, "a replica died" and "a replica is
  starting for the first time" are the same code path.

## Cost, accepted knowingly

Reads from two replicas can observe different checkpoints: there is no
read-your-writes and no monotonic-reads guarantee across replicas. A load
balancer that alternates requests across replicas exposes this directly -
a read immediately following a write can land on a replica that has not
yet ingested it, and a client bouncing between replicas can observe state
go "backwards" relative to its own last read.

Cold-start rebuild time scales with log size. The measured rate is
~996,000 keys/s for 256-byte values on a single node (see
`docs/benchmarks/rebuild-rate.md`); a replica joining against a large,
long-retained topic pays a proportional wait before it can serve current
state.

Each replica pays its own full ingest bandwidth against the broker: N
replicas means N times the fetch and consumer traffic of a single
instance, since none of them share a subscription or a cache of consumed
records.
