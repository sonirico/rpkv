# Rebuild rate (keys/s)

Time for a fresh rpkv to ingest an existing log into an empty index,
client-observed from process start to quiescence.

## Workload

200000 keys, 256-byte values, 2 partitions, seed 11.

## Environment

- Go: go version go1.26.4 linux/amd64
- OS: Linux 7.1.5-201.fc44.x86_64
- CPU: Intel(R) Core(TM) i7-8750H CPU @ 2.20GHz
- Redpanda image: docker.redpanda.com/redpandadata/redpanda:v26.1.15 (default pin in vago/testit/redpanda, used unmodified by internal/rptest/main.go)

## Results

Commit: 4f6e488

| Metric        | Value       |
|---------------|------------:|
| elapsed (s)   | 0.202445    |
| keys/s        | 987922.66   |

## Reproduce

```
just bench-rebuild
```

The JSON lands at `docs/benchmarks/rebuild-rate.json`.

## Caveats

Quiescence is detected by polling, so elapsed slightly overstates true
ingest time. Single-node loopback docker, in-process app (no HTTP serving
load during ingest), not a service-level claim.
