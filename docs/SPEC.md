# rpkv — Specification

What the system is and what its semantics are. How it is built lives in
`docs/adr/`; when it gets built lives in `docs/ROADMAP.md`.

## One paragraph

rpkv turns Redpanda topics into a queryable key-value store **without
duplicating a single value**. Kafka Streams / RocksDB state stores answer
"latest value of key X" by copying every value into a local database —
paying the full payload twice. rpkv instead maintains only a secondary
index `key → (partition, offset)` (in Pebble; tiny, pointer-sized entries)
and serves reads from where the value already lives: the topic's own log.
The trade it makes is explicit — storage economy and a single source of
truth, paid for with read latency — and the whole roadmap is about driving
that read latency down, ending in a Redpanda fork whose compaction worker
maintains byte-accurate positions for direct segment reads.

## Domain model

- **Topic** — a Redpanda topic used as the system of record. Compacted
  topics are the natural fit but not required.
- **Key** — the Kafka record key, verbatim bytes. rpkv imposes no schema.
- **Pointer** — what the index stores per key:
  `(partition int32, offset int64)`, later extended with a byte position
  for the direct read path (phase 4).
- **Index** — a Pebble database mapping `key → Pointer` per indexed topic,
  plus one checkpoint entry per partition recording the highest offset
  applied. The index is a *projection*: destroy it and it rebuilds by
  replaying the topic.
- **Tombstone** — a record with a null value. Applying it deletes the key
  from the index; the value-read path never sees the key again.

## Semantics

- **Last-write-wins per key.** The index always points at the highest
  offset seen for a key. Records are applied in partition-offset order, so
  within a partition this is exact; keys are partitioned by the producer,
  so a key lives in exactly one partition and cross-partition ordering
  never arises.
- **Reads are as-of-offset.** A `Get(key)` reflects the topic up to the
  per-partition checkpoint at the time of the lookup — the same contract a
  Kafka Streams store gives. rpkv reports the checkpoint alongside the
  value so callers can reason about staleness.
- **Correctness invariant (the one property tests defend):** for any
  sequence of records, the index's visible state is byte-identical to a
  naive materialization (`map[key]value` built by consuming the whole
  topic) at the same checkpoint.
- **Checkpoint and index commit atomically** — same Pebble batch. A crash
  between record-apply and checkpoint-write is unrepresentable; recovery
  resumes from the checkpoint with at-least-once apply, which is idempotent
  because applying record N twice writes the same pointer twice.
- **Compaction is an ally, not a threat, for the logical pointer.** Kafka
  log compaction preserves each key's *latest* record and its offset —
  exactly the record every index entry points at. Compaction can never
  remove a record the index references. What compaction *does* invalidate
  is any cached **byte position** (segments are rewritten), which is why
  the direct read path requires hooking the compaction worker (phase 4)
  rather than caching file positions from outside.

## The two read paths

- **Path A — Fetch by offset (baseline).** Resolve the pointer in Pebble,
  then fetch exactly that record over the Kafka protocol
  (single-record fetch at `(partition, offset)`). Supported, correct,
  survives upgrades and tiered storage. Cost: one broker round-trip per
  read, and random single-record fetches are the broker's worst access
  pattern.
- **Path B — Direct segment read (the fork's payoff).** The broker's own
  storage layer knows the byte position of every record; the fork's index
  keeps it, and the compaction worker updates it when segments are
  rewritten. A read becomes: Pebble lookup → open segment → seek → decode
  one record batch. No fetch round-trip. This is only sane *inside* the
  broker (or a process the broker coordinates with), where segment
  lifecycle, record-batch decoding and compaction are first-class — never
  by scraping `/var/lib/redpanda` from outside, which races the broker and
  couples to a private, versioned format. ADR-004 gates this path on a
  benchmark; ADR-005 governs the fork.

## Deployment shapes (in roadmap order)

1. **Sidecar** (phases 1): a Go process beside unmodified Redpanda —
   consumes indexed topics, maintains Pebble, serves `Get` over an API,
   reads via path A. Proves the semantics; its test suite becomes the
   fork's acceptance suite.
2. **In-broker** (phase 3): the Redpanda fork, `rpkv` enabled per topic by
   a topic property. The broker runs the index builder in the background;
   lookups are served from a broker endpoint. Path A internally at first.
3. **In-broker, direct reads** (phase 4): path B, byte positions
   maintained by the compaction hook.

## Non-goals

- Not a general database: no range scans over values, no secondary indexes
  over value fields, no transactions across keys. `Get(key)`,
  `Delete(key)` (= produce tombstone), `Put(key, value)` (= produce) —
  the topic remains the only write path.
- No caching of values in rpkv. The moment values are cached, the founding
  claim (zero duplication) is gone and Kafka Streams won.
- No compatibility promise for reading Redpanda's data directory from
  outside the broker. That road is explicitly closed (ADR-004).

## Honest trade-off (kept here so nobody re-litigates it by accident)

rpkv chooses space over read latency: huge topics whose values would be
prohibitively expensive to duplicate, read at modest rates. Read-heavy
workloads over small state should use a materialized store instead. The
product bet is that phase 4 collapses the latency gap enough that the
choice stops being painful.
