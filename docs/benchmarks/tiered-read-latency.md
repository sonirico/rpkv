# Tiered-storage-evicted read latency

Client-observed end-to-end `GET /v1/kv/{topic}/{key}` wall time on loopback
against a single-node dockerized Redpanda with tiered storage enabled
against a self-provisioned MinIO, for keys whose local segments have been
evicted by retention: a cold phase (tiered-storage read cache trimmed
before each read, forcing a first-touch fetch from object storage) and a
warm phase (the same keys re-read with the cache left alone).

## Workload

50000 keys, 256-byte values, 2 partitions, 300 cold reads and 300 warm
reads over the same distinct keys (uniform-random, no repeats), rng seed 7.
`retention.local.target.bytes=128` forces the broker to evict every local
segment it has already uploaded; the benchmark polls the admin API's
cloud storage status until `local_log_start_offset` has moved past
`cloud_log_start_offset` on both partitions before starting the cold phase.

## Environment

- Go: go version go1.26.4 linux/amd64
- OS: Linux 7.1.5-201.fc44.x86_64
- CPU: Intel(R) Core(TM) i7-8750H CPU @ 2.20GHz
- Redpanda image: docker.redpanda.com/redpandadata/redpanda:v26.1.15 (default pin in vago/testit/redpanda, used unmodified by internal/rptest/main.go)
- MinIO image: minio/minio:RELEASE.2025-09-07T16-13-09Z (default pin in vago/testit/minio, used unmodified by internal/rptest/main.go)

## Results

Commit: bc1d69a

| Metric | Cold (ms) | Warm (ms) |
|--------|----------:|----------:|
| p50    | 10.579713 | 11.191459 |
| p90    | 14.699995 | 15.314679 |
| p99    | 20.45509  | 20.366003 |
| mean   | 10.764885 | 11.332477 |
| min    | 2.998293  | 2.924774  |
| max    | 34.661441 | 21.244319 |

## Reproduce

```
just bench-tiered-read
```

The JSON lands at `docs/benchmarks/tiered-read-latency.json`.

## Caveats

Single-node loopback docker, MinIO running on the same host as Redpanda so
network latency to object storage is near-zero - a real S3-backed
deployment would show a larger cold/warm gap than measured here. Reads are
sequential, not a claim under concurrency. The tiered-storage read cache is
emptied via the admin API's cache-trim endpoint immediately before each
cold read, forcing that read off any previously fetched copy.
