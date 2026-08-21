# Router read latency

Client-observed end-to-end `GET /v1/kv/{topic}/{key}` wall time on
loopback against a single-node dockerized Redpanda, for a monolith rpkv
owning every partition versus a router fanning the same reads out to 4
shard rpkv processes.

## Workload

100000 keys, 256-byte values, 4 partitions/shards, 5000 sequential
uniform-random reads after 100 warmup reads, rng seed 7.

## Environment

- Go: go version go1.26.4 linux/amd64
- OS: Linux 7.1.5-201.fc44.x86_64
- CPU: Intel(R) Core(TM) i7-8750H CPU @ 2.20GHz
- Redpanda image: docker.redpanda.com/redpandadata/redpanda:v26.1.15 (default pin in vago/testit/redpanda, used unmodified by internal/rptest/main.go)

## Results

Commit: 426dd20

|      Metric | Monolith (ms) | Router (ms) |
|-------------:|--------------:|------------:|
|          p50 |     10.497152 |    9.839492 |
|          p90 |     12.639356 |   14.468334 |
|          p99 |     15.121061 |   20.755282 |
|          min |      4.154659 |    2.426262 |
|          max |     21.549730 |   52.510459 |
|         mean |     10.519591 |   10.324255 |
| Router overhead (p50) |    - |   -0.657660 |

Baseline on record: `docs/benchmarks/read-latency.md` reports p50
11.066376 ms, p99 16.851917 ms at commit fb00a9d, 2 partitions. The
same-run monolith column above (4 partitions) is the like-for-like
comparison for this benchmark.

## Reproduce

```
just bench-router-read
```

The JSON lands at `docs/benchmarks/router-read-latency.json`.

## Caveats

Single run, loopback single-node docker, sequential client reads, not a
service-level claim. The router fans out to every shard and waits for all
answers, so its latency tracks the slowest shard.
