# rpkv - Specification

What the system is, its semantics, and its **frozen contracts**. How it is
built lives in `docs/adr/`; when, in `docs/ROADMAP.md`. Sections marked
**(frozen)** are decided: implementing sessions do not re-open them, do not
ask about them, and treat deviating from them as a spec violation to raise,
never a choice to make silently.

## One paragraph

rpkv turns Redpanda topics into a queryable key-value store **without
duplicating a single value**. It is a pure-Go sidecar beside an unmodified
Redpanda: it consumes the indexed topics with franz-go, maintains a Pebble
index `key -> (partition, offset)` - pointer-sized entries regardless of
value size - and serves `Get(topic, key)` by fetching exactly one record
from the log over the Kafka protocol. The trade is explicit: storage
economy and a single source of truth, paid with a broker round-trip per
read. (An in-broker variant that collapses that latency is a separate,
parked plan: `../redpanda/rpkv-plan/` - out of scope in this repo.)

## Hard constraints (frozen)

- **Pure Go.** No CGo anywhere, no C bindings. `CGO_ENABLED=0` must build.
- **Client is franz-go** (`github.com/twmb/franz-go`). No sarama, no
  confluent-kafka-go.
- **Index store is Pebble** (`github.com/cockroachdb/pebble`). (ADR-003)
- **Only the public Kafka protocol.** Nothing ever reads the broker's
  data directory, in any phase, for any reason. (ADR-004)
- **No value is ever stored, cached or copied.** (ADR-001)
- **Resilient to upstream compaction and retention** - by design, not by
  luck; the mechanisms are the "Compaction model" section below and their
  tests are roadmap exit criteria.

## Domain model

- **Topic** - a Redpanda topic used as the system of record; one Pebble
  database per indexed topic.
- **Key** - the Kafka record key, verbatim bytes. rpkv imposes no schema.
  A record with a null key is not indexable and is skipped (counted in
  metrics).
- **Pointer** - `(partition int32, offset int64)`: where the latest record
  for a key lives.
- **Tombstone** - a record with a **null** value (franz-go
  `Record.Value == nil`; an empty non-nil value is a normal value).
  Applying it deletes the key from the index.
- **Checkpoint** - per partition, the highest offset **applied** to the
  index. Index apply and checkpoint advance commit in one Pebble batch.

## Semantics

- **Last-write-wins per key**, exact within a partition; a key lives in
  one partition (producer partitioning), so cross-partition ordering never
  arises. rpkv does not verify producer partitioning; a key produced to
  two partitions gets last-applied-wins, documented as undefined.
- **Reads are as-of-checkpoint.** `Get` reflects the topic up to the
  partition's checkpoint; the response carries pointer and checkpoint so
  callers can reason about staleness.
- **Correctness invariant** (the property tests defend it): for any record
  sequence, index state == a naive `map[key]value` materialization at the
  same checkpoint.
- **Recovery** = resume from checkpoint + idempotent re-apply
  (at-least-once; applying record N twice writes the same pointer twice).

## Compaction model (frozen - this is the resilience design)

Redpanda compaction removes superseded records; it **preserves each key's
latest record at its original logical offset**. Logical offsets never
move. Three consequences, three mechanisms:

1. **Pointers cannot dangle in steady state.** The index always points at
   the latest offset it has seen for a key; compaction only removes
   *superseded* records. The race that remains: the log already holds a
   newer record for key K which rpkv has not applied yet, and compaction
   removes the version our pointer names. Detection is mechanism 2.
2. **Read verification protocol (frozen).** Kafka fetch semantics: a fetch
   at a compacted-away offset returns records from the next available
   offset. Therefore `fetch/` never trusts a fetch blindly:
   - Fetched record with `offset == pointer.Offset` and byte-equal key ->
     the value. (Batch-level subtlety: the fetch returns the containing
     batch; the reader selects the record with exactly `pointer.Offset`.)
   - Record at `pointer.Offset` absent from the response (first returned
     offset > pointer's, or offset present with different key - cannot
     happen at same offset, treated identically) -> **superseded**: a newer
     version existed; the server waits for the checkpoint to advance and
     re-resolves (bounded; see server contract), so the caller gets the
     newer value or a 503, never a wrong or missing value.
   - `pointer.Offset < log start offset` (retention `delete` removed it)
     -> **evicted**: surfaced as 410; rpkv does not resurrect what the log
     dropped.
3. **Rebuild convergence.** Replaying a *compacted* log yields the same
   final index state as replaying the full history (compaction keeps
   exactly the records whose pointers survive). Destroying the index and
   replaying is therefore always safe; a property test plus an integration
   test with forced compaction defend it.

Tombstones: Redpanda drops a tombstone only after older records for that
key are gone (`delete.retention.ms`); a rebuild after that sees nothing
for the key -> absent. Consistent with the live index, which deleted it.

## Contracts (frozen)

Exact shapes. Names, types, encodings and status codes below are decided.
Anything genuinely not covered here is an implementation detail the
implementer chooses and reports; it is never worth a question to the
operator.

### Pebble layout - one DB per topic at `<data_dir>/topics/<topic>/`

Kafka topic names are `[a-zA-Z0-9._-]+`, filesystem-safe verbatim.

| Pebble key | Pebble value |
|---|---|
| `0x01 ++ user_key` | `partition int32 BE (4B) ++ offset int64 BE (8B)` |
| `0x02 ++ partition int32 BE (4B)` | `checkpoint offset int64 BE (8B)` - highest **applied** offset |

No other keys. All writes go through `pebble.Batch` with `Sync` on the
batch commit that carries a checkpoint advance.

### `index/`

```go
type Pointer struct { Partition int32; Offset int64 }
type Entry struct { Key []byte; Pointer Pointer; Tombstone bool }
type Lookup struct { Pointer Pointer; Found bool }

func NewIndex(db *pebble.DB) *Index
func (ix *Index) Apply(entries []Entry, checkpoints map[int32]int64) error // one atomic batch
func (ix *Index) Get(key []byte) (Lookup, error)
func (ix *Index) Checkpoint(partition int32) (int64, error) // -1 when none
```

`Apply` with an empty entries slice and a checkpoint advance is valid (a
poll that saw only skippable records still advances). `Close() error` per
the lifecycle rules.

### `ingest/`

```go
func NewIngester(client *kgo.Client, index *index.Index, topic string, logger *slog.Logger) *Ingester
func (in *Ingester) Run(ctx context.Context) error
```

Frozen behavior: **no consumer groups** - direct partition assignment of
all partitions (`kgo.ConsumePartitions`), each starting at
`checkpoint+1`, or the partition's log start when no checkpoint exists.
The checkpoint is the index's, atomically with applies; broker-side
offsets are never committed. One `Apply` per poll per partition batch,
preserving offset order. Null-key records: skipped, counted.

### `fetch/`

```go
type Result struct { Value []byte; Superseded bool; Evicted bool }

func NewFetcher(client *kgo.Client, topic string) *Fetcher
func (f *Fetcher) FetchAt(ctx context.Context, ptr index.Pointer, key []byte) (Result, error)
```

Implements the read verification protocol verbatim (Compaction model,
mechanism 2). `Superseded` and `Evicted` are mutually exclusive; `error`
is transport-level only.

### `server/`

`GET /v1/kv/{topic}/{key}` - `{key}` is percent-encoded raw bytes;
`?key_encoding=base64url` accepts RFC 4648 base64url instead.

| Case | Response |
|---|---|
| Hit | `200`, body = raw value bytes; headers `X-Rpkv-Partition`, `X-Rpkv-Offset`, `X-Rpkv-Checkpoint` |
| Key not in index | `404`, empty body |
| Evicted by retention | `410` |
| Superseded and catch-up did not resolve within budget | `503`, `Retry-After: 1` |
| Topic not indexed by this instance | `404` with body `topic not indexed` |

Supersede handling: on `Superseded`, the server polls
`index.Checkpoint(ptr.Partition)` until it passes the fetched-forward
offset, re-resolves and re-fetches; total budget **2s**, waits through the
`clock.Clock` interface (mockable). `GET /healthz` -> `200` always, JSON
body with per-partition checkpoint and log-end lag.

### `cmd/rpkv` configuration

Flags, each with an env fallback (flag wins): `--brokers`/`RPKV_BROKERS`
(comma-separated, required), `--topics`/`RPKV_TOPICS` (comma-separated,
required), `--data-dir`/`RPKV_DATA_DIR` (default `./rpkv-data`),
`--listen`/`RPKV_LISTEN` (default `:8080`). MVP speaks PLAINTEXT;
SASL/TLS is a later roadmap task (env names will follow the protocol:
`RPKV_SASL_USER`, not vendor names).

### Package layout

```
index/    ingest/    fetch/    server/    clock/    cmd/rpkv/    internal/
```

Public packages at the module root, never importing `internal/`
(`scripts/check-boundaries.sh`). `clock/` is the sole production caller of
`time.Now/After`.

## Non-goals

No range scans, no secondary indexes over value fields, no transactions,
no write path (`Put`/`Delete` = produce to the topic with any Kafka
client), no value caching (the founding claim dies there), no reading the
broker's data directory, no multi-instance coordination (one rpkv instance
owns its Pebble dir; HA = run another instance, they converge
independently).

## Honest trade-off

rpkv chooses space over read latency: topics whose values are too big to
duplicate, read at modest rates. Reads of keys whose segments were evicted
to tiered storage pay object-storage latency - measured and published in
phase 3, not hidden. Read-heavy hot state belongs in a materialized store;
that quadrant is conceded.
