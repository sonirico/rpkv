# rpkv

Key-value reads over Redpanda topics, without duplicating values.

A topic already holds every value; what it cannot answer cheaply is
"latest value for key X". rpkv maintains a secondary index
`key → (partition, offset)` in embedded Pebble — pointer-sized entries,
regardless of value size — and serves reads from the log itself. Kafka
Streams answers the same question by copying every value into RocksDB;
rpkv's bet is the opposite trade: space over read latency, then engineering
the latency back down.

The arc, in order (details in `docs/SPEC.md`, decisions in `docs/adr/`):

1. **Sidecar** — a Go process beside unmodified Redpanda: franz-go
   ingest → Pebble index → `Get` API, values fetched by exact
   `(partition, offset)` over the Kafka protocol.
2. **In-broker** — a minimal, feature-flagged Redpanda fork running the
   index as a background worker, the sidecar's test suite as its
   acceptance suite.
3. **Direct reads** — the fork's compaction worker maintains byte-accurate
   segment positions, turning a read into seek + decode, no fetch
   round-trip. Gated on a benchmark proving it's worth owning.

Work state: `docs/ROADMAP.md`. Dev setup: `just setup`, then `just check`.
