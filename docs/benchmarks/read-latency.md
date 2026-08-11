# Read latency (local segments)

Client-observed end-to-end `GET /v1/kv/{topic}/{key}` wall time on loopback
against a single-node dockerized Redpanda, including the broker fetch
round-trip; plain topic, no compaction, no tiered storage.

## Workload

100000 keys, 256-byte values, 2 partitions, 5000 sequential uniform-random
reads after 100 warmup reads, rng seed 7.

## Environment

- Go: go version go1.26.4 linux/amd64
- OS: Linux 7.1.5-201.fc44.x86_64
- CPU: Intel(R) Core(TM) i7-8750H CPU @ 2.20GHz
- Redpanda image: docker.redpanda.com/redpandadata/redpanda:v26.1.15 (default pin in vago/testit/redpanda, used unmodified by internal/rptest/main.go)

## Results

Commit: fb00a9d

| Metric | Value (ms) |
|--------|-----------:|
| p50    | 11.066376  |
| p90    | 13.40362   |
| p99    | 16.851917  |
| mean   | 11.052773  |
| min    | 3.952992   |
| max    | 26.364248  |

## Reproduce

```
just bench-read
```

The JSON lands at `docs/benchmarks/read-latency.json`.

## Caveats

Loopback single-node docker baseline, sequential client reads, not a
service-level claim under concurrency. Tiered-storage-evicted latency is a
separate pending measurement.
