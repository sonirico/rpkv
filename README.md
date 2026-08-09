# rpkv

Key-value reads over Redpanda topics, without duplicating values.

A topic already holds every value; what it cannot answer cheaply is
"latest value for key X". rpkv is a pure-Go sidecar that maintains a
secondary index `key -> (partition, offset)` in embedded Pebble -
pointer-sized entries, regardless of value size - and serves `Get` by
fetching exactly that record from the log over the Kafka protocol
(franz-go). Kafka Streams answers the same question by copying every
value into RocksDB; rpkv makes the opposite trade: space over read
latency, honestly declared.

Design highlights (full spec in `docs/SPEC.md`, decisions in `docs/adr/`):

- **Zero value duplication** - the index stores pointers and checkpoints,
  nothing else; the log stays the single source of truth.
- **Compaction-resilient by construction** - pointers are logical offsets
  (stable across compaction, which always preserves each key's latest
  record); every fetch is verified, and the one real race (a pointer
  superseded mid-compaction) is detected and resolved, never served
  wrong.
- **Disposable index** - destroy the Pebble dir and it rebuilds by
  replaying the (compacted) log to an identical state.
- **Pure Go, static binary** - `CGO_ENABLED=0`, franz-go + Pebble, no
  external dependencies beyond your existing Redpanda.

Sweet spot: big values, modest read rates, on-prem. Reads of keys whose
segments were evicted to tiered storage pay object-storage latency - the
benchmarks publish that number rather than hiding it.

Work state: `docs/ROADMAP.md`. Dev setup: `just setup`, then `just check`.
