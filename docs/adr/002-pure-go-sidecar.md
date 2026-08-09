# ADR-002 — A pure-Go sidecar is the product

Status: Accepted. (Rewritten 2026-08-09; the original "sidecar first, fork
second" phasing moved out — the in-broker/upstream plan is parked in
`../redpanda/rpkv-plan/UPSTREAM-PLAN.md` and is out of scope here.)

## Decision

rpkv is a standalone, pure-Go process beside an unmodified Redpanda:
franz-go consumer → Pebble index → HTTP query surface, values read over
the public Kafka protocol. **No CGo, no C bindings** (`CGO_ENABLED=0`
builds), **franz-go** as the only Kafka client, **Pebble** as the only
store. This repo plans, builds and ships that product and nothing else.

## Why

- **Pure Go is the deployment story**: one static binary, trivial
  cross-compilation, no libc/librdkafka matrix. Both hard dependencies
  honor it — franz-go and Pebble are pure Go.
- **franz-go** is the only maintained Go client with the full protocol
  surface rpkv needs — direct partition assignment without consumer
  groups, and exact single-offset fetches — plus per-record control
  needed for tombstone detection (`Record.Value == nil`).
- **Sidecar against stock Redpanda** keeps the entire risk surface ours:
  semantics, not broker internals. It is independently shippable and its
  contract-test suite is deliberately reusable as the acceptance suite for
  the parked in-broker plan.

## What moved out, and where

The recon spike (storage format, compaction internals, license analysis)
and the in-broker/upstream contribution strategy live in the sibling
checkout: `../redpanda/rpkv-plan/` (branch `rpkv-plan`). Its resume gate
is defined there and consumes two artifacts this repo must produce: the
contract suite and the phase-3 benchmarks (including tiered-storage cold
reads). Nothing in this repo blocks on it.

## Reversal criterion

If franz-go proves unable to express the read verification protocol
(exact-offset fetch with batch-level record selection) or checkpoint-free
partition consumption, the client choice reopens — against evidence, in
this file. Pure-Go and sidecar-shape do not reopen inside this repo.
