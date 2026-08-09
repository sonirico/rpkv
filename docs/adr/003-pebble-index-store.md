# ADR-003 — Pebble as the index store

Status: Accepted.

## Decision

The secondary index is stored in Pebble (`github.com/cockroachdb/pebble`):
one keyspace per indexed topic holding `key → Pointer` entries, plus one
checkpoint entry per partition. Pointer apply and checkpoint advance are
written in the same batch.

## Why

- **Write-optimized LSM matches the workload.** Index maintenance is an
  append-heavy stream of small upserts — the exact shape LSMs are built
  for. Reads are point lookups, Pebble's cheapest operation.
- **Embedded, pure Go, no CGo.** In-process with the sidecar (and with any
  Go control plane the fork grows later); no external service to operate;
  cross-compiles cleanly. RocksDB would buy nothing here but a CGo
  dependency.
- **Atomic batches give the checkpoint invariant for free.** SPEC requires
  index+checkpoint atomicity; `pebble.Batch` is precisely that primitive.
- **Proven at scale** as CockroachDB's storage engine; its snapshot and
  iterator model covers the consistency needs we have (as-of-offset reads).

## Shape of the keyspace

Decided at implementation time in phase 1 and recorded here when it
settles; the constraints it must satisfy are: per-topic isolation, binary
keys verbatim (no escaping that breaks ordering), checkpoint entries
adjacent enough to scan at startup, and room for the phase-4 byte-position
extension without rewriting existing entries.

## Reversal criterion

If phase-1 benchmarks show Pebble itself (not the fetch) dominating read
latency or index rebuild time at representative cardinalities (10^8 keys),
alternatives (bbolt, a plain sorted file + memtable) get a spike with the
same benchmark. No migration happens without that number.
