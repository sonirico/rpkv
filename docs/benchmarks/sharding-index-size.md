# Sharding index size

On-disk footprint of a Pebble index holding every partition's entries
(monolith) versus one holding a single partition's entries (shard), built
from the same candidate entries.

## Workload

1000000 candidate entries hashed round-robin across 4 partitions, applied
in batches of 1000. The monolith owns all 4 partitions; the shard owns
only partition 0.

## Environment

- Go: go version go1.26.4 linux/amd64
- OS: Linux 7.1.5-201.fc44.x86_64
- CPU: Intel(R) Core(TM) i7-8750H CPU @ 2.20GHz

## Results

Commit: 426dd20

|                    Metric |   Monolith |     Shard |
|--------------------------:|-----------:|----------:|
|                       Keys |    1000000 |    250000 |
|                Bytes total |   14688687 |   6666499 |
|                  Bytes/key |  14.688687 | 26.665996 |
| Shard-to-monolith bytes ratio |        - | 0.45385261 |

## Reproduce

```
just bench-shard-index-size
```

The JSON lands at `docs/benchmarks/sharding-index-size.json`.

## Caveats

Single run, not a service-level claim. Pebble WAL/manifest overhead and
compaction state at close make the ratio approximate.
