# ADR-007 - Metrics: a label-free facade in public packages, Prometheus only in wiring

Status: Accepted (2026-08-11).

## Decision

`fetch/`, `ingest/` and `server/` report metrics through the `metrics/`
package's `Counter`, `Gauge` and `Histogram` interfaces - `Inc`/`Add`,
`Set`, `Observe` - structurally matching Prometheus's own contracts for
those types with no adapter needed. Each package defines its own `Metrics`
struct (`fetch.Metrics`, `ingest.Metrics`, `server.Metrics`) of these
primitives and a `WithMetrics` option that assigns non-nil fields, leaving
noops (`metrics.NewNoopCounter` and friends) for anything left unset. These
primitives are label-free: a package like `fetch` knows it increments "a
hit counter", never that the counter is `rpkv_fetch_outcomes_total{outcome="hit",topic="..."}`.

The Prometheus implementation - registry, metric names, label values,
bucket boundaries - lives entirely in `internal/promsink`, imported only by
`cmd/rpkv`'s wiring (`internal/app`). `promsink.Sinks` owns a private
`*prometheus.Registry` (never the global default registry), builds and
registers every metric, and hands out already-label-bound `fetch.Metrics` /
`ingest.Metrics` / `server.Metrics` values per topic. `promsink.Sinks.Handler()`
serves `GET /metrics` against that private registry.

Ingest lag (`rpkv_ingest_lag`) is the one metric not updated by application
code on a hot path: `internal/promsink`'s `lagCollector` implements
`prometheus.Collector` and computes `max(0, logEnd-checkpoint-1)` per
partition at scrape time, reading the same checkpoint and log-end-offset
sources (`*index.Index`, `*offsets.Source`) that `GET /healthz` already
reads, duck-typed through private `checkpointReader`/`logEndSource`
interfaces.

## Why

1. **Public packages stay embeddable without dragging in Prometheus.**
   ADR-009's public/internal split (`scripts/check-boundaries.sh`) already
   forbids public packages from importing `internal/`; the same discipline
   extends to third-party dependencies that only wiring needs. An embedder
   who wants a different metrics backend swaps `internal/promsink` for
   their own sink - the public packages never notice.
2. **Phase 3's benchmarks reuse the facade, not Prometheus.** Numbers on
   record (`docs/benchmarks/`) read `fetch.Metrics`/`ingest.Metrics`
   directly against a benchmark-local sink; nothing in the measured path
   depends on the exposition format or a registry.
3. **Label-free primitives keep the facade small and stable.** A `Counter`
   with `Inc`/`Add` is a contract that will never need to change; label
   sets and metric names are an operational concern that changes far more
   often, and belong entirely in the wiring layer that owns them.
4. **Scrape-time lag avoids a second write path.** Computing
   `rpkv_ingest_lag` from a stored gauge updated by the ingester would mean
   two places writing lag state (the ingest hot path and whatever exposes
   it) that could drift. A collector that reads the same data `/healthz`
   reads, on demand, has exactly one source of truth and adds no
   allocation or lock contention to `ingest/`'s hot path.

## Consequences

- `go.mod` gains a direct dependency on `github.com/prometheus/client_golang`
  (pure Go); nothing outside `internal/promsink` and `cmd/rpkv`'s wiring
  imports it.
- Every metric name, label set and bucket boundary is frozen in SPEC
  "Contracts (frozen)" section `metrics/`, alongside the facade
  interfaces themselves.
- `lagCollector.Collect` calls `LogEndOffsets` under a 5s
  `context.WithTimeout(context.Background(), ...)` - a wiring-boundary
  direct use of `context.Background()`/`time`, permitted because
  `internal/promsink` is wiring, not a public package's hot path. A
  partition whose checkpoint or log-end read fails is skipped and logged,
  never fails the whole scrape.
- `promsink.Sinks.RegisterLag` is called once per topic from `internal/app.New`;
  a registration failure (duplicate metric, bad label set) fails app
  construction the same way a Pebble-open or kgo-client failure already
  does, with the same partial-teardown discipline.
