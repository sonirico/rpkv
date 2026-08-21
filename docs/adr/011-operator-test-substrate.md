# ADR-011 - Operator test substrate: envtest for reconcile, kind for e2e

Status: Accepted.

## Context

The operator (`operator/`) reconciles a topic's partition count into a
shard StatefulSet's replica count, grow-only, driven by the shards'
own `/healthz`. Its correctness lives almost entirely in reconcile
logic against the Kubernetes API - watch events, object diffing,
status conditions - none of which the dockerized-broker substrate
ADR-006 built for the rest of the repo can exercise: that substrate
provisions a Redpanda container, not a control plane. The operator
needs something heavier: a real (or realistic) Kubernetes API server to
reconcile against, and, for the end-to-end path, a real cluster to run
the built operator image against a real Redpanda pod.

## Decision

Two substrates, one per test level, both throwaway and both
receipt-writing:

- **Reconcile logic: `envtest`.** `just operator-envtest` pins
  `kube-apiserver`/`etcd` binaries via
  `sigs.k8s.io/controller-runtime/tools/setup-envtest`, downloading them
  on first run and caching them thereafter, then runs the reconcile
  suite (`operator/internal/controller`, `TestEnvtest`) against the
  spun-up API server. The suite is gated on `RPKV_ENVTEST=1` so it does
  not run as part of the module's plain `go test`.
- **End-to-end: `kind`.** `just kind-operator-e2e`
  (`scripts/kind-operator-e2e.sh`) stands up a throwaway `kind` cluster,
  loads the built operator image into it alongside a real Redpanda pod,
  applies the CRD and a sample `RpkvIndex`, grows the topic's partition
  count, and observes the StatefulSet's replica count follow.

Both substrates are provisioned fresh per run and torn down after;
neither persists state between runs, matching ADR-006's determinism
rationale for the rest of the repo.

This ADR's number was reserved provisionally as "ADR-010" in the
master plan for Phase 4; that label was reassigned to ADR-010 (router
fan-out) once that decision landed first, so the operator test
substrate is recorded here as ADR-011 instead.

## Consequences

- `just operator-envtest` downloads `kube-apiserver`/`etcd` binaries on
  its first run in any environment (cached afterward by
  `setup-envtest`); a machine with no network access on that first run
  cannot execute the suite.
- `just kind-operator-e2e` is minutes-scale wall time: cluster
  bring-up, image load, a real reconcile loop observing a real
  StatefulSet, and teardown, versus the sub-second reconcile-suite
  runs under `envtest`.
- Both substrates run locally, not in hosted CI, for now; local
  execution before commit is the operator's only verification until
  that changes.
- `operator/go.mod` gains `sigs.k8s.io/controller-runtime`'s `envtest`
  and `setup-envtest` tooling as test-only dependencies, scoped to the
  operator module and not the root module.
