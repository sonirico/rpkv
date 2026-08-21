# Sharding ingest rate

Client-observed wall time for rpkv to reach quiescence (ingest lag zero on
every owned partition) against a single-node dockerized Redpanda, for the
same topic under two configurations: a monolith owning every partition,
and a single shard owning only partition 0.

## Workload

1000000 keys, 256-byte values, 4 partitions, produced in chunks of 1000,
rng seed 13.

## Environment

- Go: go version go1.26.4 linux/amd64
- OS: Linux 7.1.5-201.fc44.x86_64
- CPU: Intel(R) Core(TM) i7-8750H CPU @ 2.20GHz
- Redpanda image: docker.redpanda.com/redpandadata/redpanda:v26.1.15 (default pin in vago/testit/redpanda, used unmodified by internal/rptest/main.go)

## Results

Commit: 426dd20

|                      Metric |    Monolith |       Shard |
|-----------------------------:|------------:|------------:|
|                          Keys |     1000000 |      249558 |
|                    Elapsed (s) | 1.204288913 | 0.404402613 |
|                    Keys/s | 830365.528741 | 617102.837563 |
| Shard-to-monolith elapsed ratio |           - | 0.335801990 |

## Reproduce

```
just bench-shard-ingest
```

The JSON lands at `docs/benchmarks/sharding-ingest-rate.json`.

## Caveats

Single run, loopback single-node docker, not a service-level claim. The
producer's hash partitioner makes per-partition key counts uneven, so the
shard's key count is measured from its end offset rather than assumed to
be 1/4.
