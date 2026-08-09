# ADR-004 — Read path: Fetch-by-offset baseline; direct segment reads only behind the compaction hook, and only with a benchmark

Status: Accepted (path A). Path B is gated, not decided.

## Decision

- **Path A (baseline, phases 1–3):** `Get(key)` resolves the pointer in
  Pebble and fetches the single record at `(partition, offset)` over the
  Kafka protocol. This is the only read path until phase 4's gate opens.
- **Path B (gated, phase 4):** direct segment read — Pebble additionally
  holds a byte position, maintained by the fork's storage layer and
  **updated by the compaction worker when segments are rewritten**; a read
  seeks into the segment and decodes one record batch, no fetch
  round-trip.
- **Forbidden permanently:** resolving byte positions by scraping the data
  directory from outside the broker (`fopen("/var/lib/redpanda/...")` from
  a sidecar). This is not a phasing question; it is closed.

## Why the scrape is closed

Four independent reasons, each sufficient:

1. A Kafka offset is a logical sequence number, not a byte position; the
   translation index is broker-private.
2. Segment format is private, versioned, compressed, batched — parsing it
   from outside couples rpkv to internals with no compatibility promise.
3. The broker is concurrently writing, compacting, rotating and deleting
   those files; an outside reader races all of it.
4. Tiered storage moves segments off local disk entirely.

Every one of these dissolves *inside* the broker, where segment lifecycle,
the offset index and batch decoding are first-class citizens — which is
why path B is a fork feature, not a sidecar feature.

## The gate for path B

Path B lands only with, in this order:

1. **The recon spike's findings on record** (phase 2): where Redpanda's
   compaction worker lives, what hook shape is viable, what the rebase
   cost looks like. Written into this ADR as an amendment.
2. **A benchmark on record** proving path A is the bottleneck for the
   target workload and estimating path B's win. If single-record fetch
   latency is acceptable, path B's maintenance burden is not bought.
3. **The compaction hook keeping byte positions correct under test** —
   a compaction pass over an indexed topic followed by reads of every key
   is the red test that must exist before the hook is trusted.

## Reversal criterion

Path A has none — it is the semantic contract and remains the fallback
forever (a path-B miss can always degrade to a fetch). Path B reverses by
simply not landing if its gate never opens; nothing else depends on it.
