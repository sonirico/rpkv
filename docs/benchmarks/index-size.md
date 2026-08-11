# Index size (bytes per key at 1e6 keys)

Measures rpkv's own Pebble index footprint after applying one million
entries: no broker, no franz-go involved, just `Index.Apply` followed by
summing every regular file under the Pebble directory once it is closed.

## Workload

1000000 keys (`k-%07d`), 2 partitions, applied in batches of 1000.

## Environment

- Go: go version go1.26.4 linux/amd64
- OS: Linux 7.1.5-201.fc44.x86_64
- CPU: Intel(R) Core(TM) i7-8750H CPU @ 2.20GHz

## Results

Commit: 1af8a9d

| Metric        |        Value |
|---------------|-------------:|
| bytes total   |     14160120 |
| bytes per key |     14.16012 |

## Reproduce

```
just bench-index-size
```

The JSON lands at `docs/benchmarks/index-size.json`.

## Caveats

Single run, not averaged. Post-Close file sizes include Pebble WAL and
manifest overhead alongside the sstables, so this is the index's on-disk
footprint, not a pure per-key payload size. The number also depends on
Pebble's compaction state at close, which can vary run to run.
