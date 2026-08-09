# Subagent report — existing KV surfaces / duplication hunt

Verbatim output of the source-reading subagent (2026-08-09, fork checkout).
Synthesis lives in `findings.md`.

---

## 1. `src/v/storage/kvstore.*` — internal metadata KV, not for user topic keys

- Purpose stated in the header doc: durable key→blob store, WAL + fully in-memory cache, built for tiny internal metadata ("tracking raft voted-for and log's base offset") — `src/v/storage/kvstore.h:34-75`. Explicit limitation: "The entire database is cached in memory, so users should not allow the set of unique keys to grow unbounded" (`kvstore.h:68-74`).
- API: `get(key_space, bytes_view) -> optional<iobuf>`, `put`, `remove` — `kvstore.h:131-133`.
- Namespacing is a closed enum of internal subsystems: `consensus, storage, controller, offset_translator, usage, stms, shard_placement, debug_bundle, crash_tracker` — `kvstore.h:97-110`. No topic/user keyspace.
- One instance per shard, constructed with `ss::shard_id` (`kvstore.h:112-116`, `kvstore.cc:42-48`, wired in `src/v/storage/api.h:38-51`). **Verdict: pure per-shard metadata store; architecturally unsuitable (all-in-RAM, closed keyspace) for indexing user topic keys.**

## 2. Compacted-topic key indexes — key→offset maps exist, but as throwaway compaction artifacts

- `src/v/compaction/key_offset_map.h:23-70` — abstract `key_offset_map` "Map containing the latest offsets of each key" with `put(key, offset)` / `get(key) -> optional<offset>`. Exactly the index your product needs — but implementations are transient, bounded (`simple_key_offset_map` default 1000 keys, `key_offset_map.h:76-80`; plus a hashed variant), and rebuilt per compaction round, then `reset()`.
- `src/v/storage/spill_key_index.h:32-46` — `spill_key_index : compacted_index_writer` builds an on-disk per-segment `.compaction_index` file (path via `to_compacted_index()`, `src/v/storage/fs_utils.h:144`) mapping key→`{base_offset, delta}`.
- These `.compaction_index` files persist next to segments, but the only readers are compaction internals: `compacted_index_reader` is consumed exclusively in `src/v/storage/segment_utils.cc` (e.g. `natural_index_of_entries_to_keep` at :201, `generate_compacted_list` at :324, `make_indices_readers` at :1058) to compute which offsets to keep, and files are removed after use (`segment_utils.cc:909`, `:1272`). No fetch/read path, RPC, or API touches them. **Verdict: key→offset mappings are materialized but are private, per-segment, sorted-and-discarded build artifacts — not a queryable index. They prove the plumbing (key extraction incl. transactional/control batch handling in `src/v/compaction/`) exists and is reusable.**

## 3. `src/v/lsm/` — a full LevelDB port on Seastar (the biggest finding)

- It is a modification of Google LevelDB: copyright header "The LevelDB Authors... Modifications copyright 2025 Redpanda Data" — `src/v/lsm/lsm.h:1-7`. Complete LSM engine: memtable, SSTs, block cache, bloom filters, compaction, snapshots, iterators (`src/v/lsm/db/{impl,memtable,compaction_task,version_set}.h`, `src/v/lsm/sst/`, `src/v/lsm/block/`).
- Notably cloud-native: pluggable persistence including object storage (`src/v/lsm/io/{persistence,disk_persistence,memory_persistence,cloud_cache_persistence,chunked_remote_file_reader}.h`) and a `database_epoch` option designed for raft-term-fenced WALs on shared object storage (`lsm.h:42-53`).
- Sole consumer today: `src/v/cloud_topics/level_one/metastore/lsm/` — the cloud-topics metastore stores its state in this LSM, replicated via a raft STM plus object storage (`replicated_persistence.h:22-34`, `stm.cc`). The keys are *metadata rows*, not user record keys: `metadata_row_key`, `extent_row_key`, `term_row_key`, `compaction_row_key`, `object_row_key` (`src/v/cloud_topics/level_one/metastore/lsm/keys.h:32-79`). Git history is shallow (single merge commit) so I can't date it from this checkout, but the 2025 copyright and cloud_topics-only usage mark it as recent. **Verdict: Redpanda now ships a general-purpose embedded LSM KV engine — this is the exact storage substrate your index would want, already written, but currently used only for internal cloud-topics metadata.**

## 4. cloud_topics and datalake — no per-key access

- `cloud_topics` (`src/v/cloud_topics/README.md`) is tiered/leaderless topic data on object storage; the level-one `metastore` API is offset/extent oriented: `get_offsets`, `get_first_ge(topic_id_partition, kafka::offset)`, `get_first_offset_for_bytes` (`src/v/cloud_topics/level_one/metastore/metastore.h:206-278`). Nothing keyed by record key.
- `datalake/` writes topics to Iceberg/Parquet (`record_multiplexer.cc`, `serde_parquet_writer.cc`, `catalog_schema_manager.cc`, partitioning via `partition_key_path.cc` = Iceberg partition specs, not record-key lookup). Per-key access would come from an *external* query engine over Iceberg — full value duplication, high latency. **Verdict: neither exposes key lookup; datalake is the opposite of "zero value duplication".**

## 5. User-facing key lookup — absent everywhere

- Kafka handlers (`src/v/kafka/server/handlers/`) are the standard protocol set (fetch, produce, list_offsets...); no key-based read exists in the protocol or as an extension.
- Admin API (`src/v/redpanda/admin/`): topic/partition/cluster ops only; no record-by-key route in `api-doc`.
- Pandaproxy REST (`src/v/pandaproxy/rest/handlers.h:35-50`): produce/consume/offsets only.
- Data transforms (`src/v/transform/`, `src/v/wasm/`): stateless record-to-record pipelines (source topic → sink topic); no state store, no lookup API, no `by_key` anything.
- Notable internal precedent: the Schema Registry (`src/v/pandaproxy/schema_registry/store.h:54`, `sharded_store.*`, `seq_writer.*`) is a hand-rolled materialized KV view over the compacted `_schemas` topic — held fully in memory, internal, single-purpose. It's evidence Redpanda solves this pattern ad hoc rather than generically.
- No RFC in `docs/rfcs/` mentions key-value querying or materialized views (only unrelated hits in ghost-batches and cluster-bootstrap RFCs). rpk lives at `src/go/rpk` in this tree; nothing KV-related surfaced.

## 6. Verdict

**Your product does not exist in Redpanda. It is roughly half-built as unassembled pieces:**

| Piece | Status | Reusable? |
|---|---|---|
| Key extraction from batches (incl. tx/control semantics) | Built | Yes — `src/v/compaction/` (`compaction_key`, reducers) |
| key→offset map structure | Built but transient/bounded | Interface reusable: `src/v/compaction/key_offset_map.h` |
| Persistent, scalable, queryable index engine | **Built** — Seastar LevelDB port with object-storage persistence | Yes — `src/v/lsm/`; the single strongest asset |
| Replicated/fenced index persistence pattern | Built for metastore | Pattern reusable: `cloud_topics/level_one/metastore/lsm/replicated_persistence.*` |
| Read-value-from-log-by-offset | Built (core fetch path) | Trivially — gives you zero value duplication |
| Index maintenance hooked to the write/compaction path for user topics | **Absent** | — |
| Any query API (Kafka ext, admin, REST, transforms) | **Absent** | — |

Two strategic cautions: (a) `src/v/lsm/` plus the level-one metastore shows Redpanda is actively building LSM-on-object-storage infrastructure — extending it from metadata rows to user record keys is an obvious internal next step for them, so the gap may close from inside; (b) the Schema Registry precedent shows demand for the pattern and that Redpanda's answer so far is per-feature in-memory materialization, not a general surface. The "queryable KV over a topic with zero value duplication" surface itself — index maintenance on user topics + a lookup API — is genuinely absent today.
