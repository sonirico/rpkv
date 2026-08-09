# ADR-004 - Read path: fetch-by-offset, verified; the data directory is closed

Status: Accepted. (Rewritten 2026-08-09; the gated "path B" direct segment
read moved to `../redpanda/rpkv-plan/` with the rest of the in-broker
plan.)

## Decision

`Get(key)` resolves the pointer in Pebble and fetches the single record at
`(partition, offset)` over the Kafka protocol, applying the **read
verification protocol** frozen in SPEC "Compaction model": verify offset
and key on the fetched batch, classify mismatches as *superseded*
(newer value exists - wait for checkpoint and re-resolve) or *evicted*
(below log start - 410). This is the product's only read path.

**Forbidden permanently:** resolving values by opening the broker's data
directory (`/var/lib/redpanda/...`) from this process, in any mode, for
any phase.

## Why the scrape is closed

Confirmed by the recon spike (evidence in
`../redpanda/rpkv-plan/format-report.md`, file:line throughout):

1. A Kafka offset is logical, not a byte position; the translation index
   (`.base_index`) is broker-private, serde-versioned (v11 and counting).
2. The segment format is Redpanda-flavored (little-endian 61-byte header,
   own CRC) with no stability promise.
3. Compaction rewrites segment files in place via rename while the broker
   serves them; an outside reader races every pass.
4. Tiered storage deletes local segments after upload; the bytes are
   simply gone.

Inside the broker these all dissolve - which is exactly why the direct
read path lives in the parked in-broker plan, not here.

## Why verified fetch is enough here

Compaction preserves each key's latest record at a stable logical offset,
so a verified fetch is correct under compaction by construction; the
verification protocol turns the one real race (pointer behind a newer
write at compaction time) into a detected, retried state instead of a
wrong answer. The latency cost (a broker round-trip, object-storage
latency for tiered-evicted segments) is the product's declared trade-off
(ADR-001) and is measured, not hidden - phase 3 publishes the numbers, and
those numbers double as the motivation dossier for the parked upstream
plan.
