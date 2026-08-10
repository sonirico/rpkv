# ADR-006 - Integration substrate: self-provisioned broker per test run

Status: Accepted (2026-08-11).

## Decision

Integration tests (and, in phase 3, benchmarks) provision their own
Redpanda broker through `github.com/sonirico/vago/testit/redpanda`
(dockertest): each test binary's `TestMain` calls `rptest.Main`, which
starts a fresh container, applies the cluster configuration, runs the
tests and tears the container down. The shared dev-loop container
(`just redpanda-up`) is no longer part of the test path; it survives only
as a manual convenience for poking a broker with `rpk` by hand.

`internal/rptest` is the single owner of the broker definition: image pin,
cluster config (`log_compaction_interval_ms` and whatever later tests
need), and the bootstrap address handed to tests via `rptest.Brokers()`.
`RPKV_TEST_BROKERS` still short-circuits provisioning and points the run
at an existing broker - the escape hatch for a fast local loop - with the
caveat that such a broker must already carry rptest's cluster config.

## Why

1. **Determinism.** The dev-loop model ran tests against whatever state
   the long-lived container had accumulated - and `fetch`'s fixture had
   to mutate *global* cluster config (`log_compaction_interval_ms`) via
   `docker exec` against a hardcoded container name, an out-of-band
   dependency on how the broker was started. Per-run provisioning makes
   every run start from the same image, the same declarative config, an
   empty log. Benchmarks (phase 3) inherit this for free: numbers on
   record must be reproducible from a cold start, not from a warm
   container of unknown history.
2. **CI runs the real suite.** CI previously ran `just check` only - no
   integration tests, because nothing provisioned a broker. Self
   -provisioning removes that excuse; `just test-integration` now runs in
   CI as its own job.
3. **One definition, not two.** Broker flags lived in the justfile
   recipe; test expectations lived in Go. Drift between them was invisible
   until a test flaked. Now the Go side owns the definition and the
   justfile recipe is explicitly non-authoritative.

## Consequences

- `go test -tags integration` requires a Docker daemon (or
  `RPKV_TEST_BROKERS`). Unit tests (`just test`, `just check`) still need
  neither.
- Package test binaries are serialized (`-p 1` in `just
  test-integration`): testit's pool uses one fixed docker network name,
  and one broker at a time keeps runs deterministic under load anyway.
- The image pin appears twice (rptest for tests, justfile for the manual
  recipe); the justfile comment marks rptest as the source of truth.
- rpkv gains test-only dependencies: `vago/testit`, `vago/testit/redpanda`
  and ory/dockertest. Nothing in production imports them; `internal/`
  placement plus the build tag keep them out of the binary.
