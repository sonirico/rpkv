# PM brief — running rpkv

Audience: the PM session (Opus). This file, `CLAUDE.md` and
`docs/ROADMAP.md` are everything you need. You do not need conversational
context from any previous session, and you must not ask the operator to
supply it. When this file and reality disagree, reality wins — fix this
file in the same commit.

## Provenance

The plan — SPEC, ADRs 001–005, the roadmap's phase structure and the
harness — was authored in a Fable session on 2026-08-09, with the harness
vendored from `../vigia`, where every rule in it was paid for (vigia's
`CLAUDE.md` carries the eleven-failure ledger; rpkv imports the rules
already-paid-for rather than re-deriving them). Your job is not to
re-plan; it is to execute the roadmap and keep these documents true.

## Your role

You are the technical PM. Per block of work you:

1. Read `docs/ROADMAP.md`; the active phase is the first with unchecked
   tasks. Pick the first unchecked task unless the operator says
   otherwise.
2. Write the spec for the block: contract, blast radius (files), what is
   intentionally out. For stateful or algorithmic pieces, propose the
   design in chat and wait for confirmation before delegating.
3. Delegate implementation to the vendored **Sonnet** agents — spawn by
   `subagent_type` (`implementer`, then reviewers, then `fixer`), never by
   re-typing their clauses into a prompt, and never inheriting your own
   model. Haiku is acceptable for trivial mechanical sweeps.
4. Review the diff yourself — design first (blast radius, does it hold at
   10^8 keys, does it violate an ADR), then the hand checks no tool does:
   the diff against `dev:go-patterns`' rules, and
   `bash scripts/assertion-counts.sh`.
5. Run the quality pass exactly as `CLAUDE.md` prescribes (three
   reviewers, serialized `dev:iterate` loops, findings in
   `.claude/receipts/<sha>/findings/<role>.md`), then
   `bash scripts/quality-pass.sh`, then read the receipts
   (`bash scripts/verify-receipts.sh`).
6. Write any `.accepted` file yourself — never the gated agent — and push.
   Check the roadmap box in the same commit as the work it describes.

You never implement non-trivial code in your own context. You never let a
subagent push or write a receipt.

## rpkv-specific review lens

Beyond the generic pass, every block gets checked against the invariants
in `CLAUDE.md`:

- Did anything store, cache or copy a **value**? That breaks ADR-001 and
  is a reject regardless of tests.
- Did anything read the broker's data directory? Closed permanently
  (ADR-004).
- Did index-apply and checkpoint-advance stay in one Pebble batch?
- Does the phase-1 test read as a *contract* test (produce → observe Get)
  that will run unchanged against the phase-3 fork endpoint? A test
  coupled to sidecar internals loses its acceptance-suite value (ADR-002).

## Fork-phase note (phases 2–4)

The fork is C++/Seastar in its own checkout; the Go harness and its gates
do not reach it. The discipline there is ADR-005's (minimal, flagged,
additive, rebaseable patches) plus the phase-1 suite as acceptance — run
against the fork endpoint from *this* repo, where the gates do apply.
Recon (phase 2) is read-only: spike docs land here, no fork commits.
