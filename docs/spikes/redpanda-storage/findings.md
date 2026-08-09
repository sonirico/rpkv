# Recon spike — Redpanda storage, duplication, license

Date: 2026-08-09. Source: shallow clone of the fork
(`git@github.com:sonirico/redpanda.git`, commit `ff54db0`) at `/redpanda/`.
Method: license files read directly; storage format and duplication studied
by two source-reading subagents; their full reports with file:line evidence
sit beside this file (`format-report.md`, `duplication-report.md`). Nothing
was built or executed.

## Q1 — Is the on-disk format self-contained and parseable?

**Yes — walkable by a standalone reader — but it is Redpanda's format, not
Kafka's.** A `.log` segment is a bare concatenation of record batches:
61-byte packed header + payload, no file header/footer. Read 61 bytes, get
`size_bytes`, skip, repeat (`storage/parser.h:87`). Divergences from
Apache Kafka's segment format: **little-endian** header (Kafka is
big-endian), reordered fields, a Redpanda-only `header_crc`, a
`record_batch_type` byte replacing Kafka's leader-epoch+magic. The records
*payload* after the header is exact Kafka v2 encoding (varints,
producer compression stored as-is — reading one value means decompressing
its batch).

## Q2 — Does the sparse index already exist?

**Yes.** Per segment, `.base_index` maps relative offset → byte position,
one 16-byte entry per 32 KiB of batch data, queried with
`find_nearest(offset)` → seek + scan ≤ 32 KiB
(`storage/segment_index.*`, `storage/index_state.h`). It is persisted
(serde envelope, version 11 — internal, churning format) and — the
load-bearing fact — **rewritten atomically with the segment swap on every
compaction pass** (`segment_utils.cc:344,609`).

Consequence for our design: **our index should store key → logical offset
only, never byte positions.** Resolve offset → bytepos at read time
through the broker's own `.base_index`, which compaction keeps fresh for
free. The compaction-hook-to-update-byte-positions idea is unnecessary for
correctness; a hook is only needed if we want an event ("segment
rewritten") for cache invalidation — and `(segment base_offset,
generation_id)` already serves as a staleness key (`segment.h:275`).

## Q3 — Key extraction: already half-built in the broker

For compacted topics the append path **already writes a persistent per-
segment key→offset file**: every batch's record keys flow through
`compacted_index_writer` → `spill_key_index` into `.compaction_index`
(key, offset, delta; CRC'd footer). Compaction then builds an in-memory
`key_offset_map` from those files, deduplicates, and throws it away.
Nothing queries any of it from outside compaction. The seams for an
in-broker index are exactly these: the `compacted_index_writer` call site
in `segment::append` (`segment.cc:604`), `maybe_track` with the physical
position in hand (`segment.cc:585`), and `batch_consumer` for rebuild
scans.

## Q4 — Duplication: does the product already exist?

**No.** No key-lookup surface exists anywhere: not in the Kafka handlers,
admin API, pandaproxy, transforms/WASM, or rpk. The internal
`storage/kvstore` is per-shard metadata with a closed keyspace, all in
RAM. The closest precedent is the Schema Registry: a hand-rolled,
in-memory, single-purpose materialized view over the compacted `_schemas`
topic — proof of the pattern's demand and of the absence of a general
surface.

**The biggest find: `src/v/lsm/` is a LevelDB port to Seastar** (memtable,
SSTs, bloom filters, own compaction, snapshots), with pluggable
persistence including object storage. Sole consumer today: the
cloud_topics level-one metastore (internal metadata rows). It is the
index engine an in-broker rpkv would want, already written.

Strategic caution: Redpanda is visibly building LSM-on-object-storage
infrastructure; extending it from metadata to user record keys is an
obvious internal next step for them. The window may close from inside.

## Q5 — License

- **Core is BSL 1.1** (`licenses/bsl.md`): copy, modify, derivative works,
  **redistribution and production use allowed**, with one carve-out
  (Additional Use Grant): no commercial "Streaming or Queuing Service" —
  an offering where **third parties** cause topic creation, i.e. a
  managed/SaaS broker. Selling a self-hosted product on a fork: permitted.
  Offering rpkv as a managed service: prohibited (a hosted KV whose `Put`
  creates/uses topics for customers plausibly triggers the clause).
  Every release converts to **Apache 2.0 four years after that release**.
  BSL must stay conspicuously displayed on all copies; violations
  terminate the license.
- **Enterprise code is RCL** (paid, not redistributable): all of
  `cloud_topics/` (including the lsm-backed metastore usage), `iceberg/`,
  `datalake/`, ~98 files of `cloud_storage/` (tiered storage), parts of
  `security/`, `cluster_link/`, and — noted — two of `src/v/lsm/io/`'s
  cloud-persistence files (`chunked_remote_file_reader`,
  `cloud_cache_persistence`). `src/v/lsm/` core is BSL.
- **Everything the product needs is BSL**: `storage/`, `compaction/`,
  `kafka/`, `model/`, `raft/`, `lsm/` core. A fork must not depend on RCL
  files; in particular, tiered-storage-aware reads would need care.

## Hard problems the direct-read path keeps (none fatal, all real)

1. **Format stability**: little-endian header, serde-versioned index files
   — internal formats with no compatibility promise. Only sane in-broker
   (or pinned to a fork), never as an external scraper — confirms ADR-004.
2. **Offset translation**: user-visible Kafka offsets ≠ on-disk log
   offsets (raft/config/control batches interleave). `.base_index` and
   `.compaction_index` are keyed by *log* offsets;
   `storage/offset_translator` state is required. An in-broker index can
   sidestep by indexing log offsets natively.
3. **Tiered storage**: local segments are deleted after upload under
   local-retention targets; direct local reads miss. Pin local retention,
   or degrade to the fetch path — and note the remote-read machinery is
   partly RCL.
4. **Partition locality**: in-broker, the index is per-partition (shard),
   so the pointer is just an offset — the sidecar variant needs
   `(partition, offset)`.

## Verdict

- **Product absent, demand pattern proven, plumbing half-built.** Key
  extraction, sparse offset→bytepos index, and an embedded LSM engine all
  exist in-tree, unassembled and unexposed.
- **The fork is cheaper than feared**: an in-broker index is mostly
  assembling existing BSL pieces at documented seams, and the
  "store logical offsets, resolve positions via `.base_index`" design
  removes the scariest part (byte-position maintenance under compaction).
- **License permits the business** in self-hosted form; managed-service
  form is the one clearly closed door until a given release's 4-year
  Apache flip.
