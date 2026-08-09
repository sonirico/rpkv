# PM brief - running rpkv

Audience: the PM session (Opus). This file, `CLAUDE.md`, `docs/SPEC.md`
and `docs/ROADMAP.md` are everything you need. You do not need
conversational context from any previous session, and you must not ask the
operator to supply it. When this file and reality disagree, reality wins -
fix this file in the same commit.

## Zero-uncertainty protocol (read this before anything else)

The SPEC's "Contracts (frozen)" section and the roadmap's task contracts
exist so that **you never need to ask the operator a design question**.
The rules:

1. **Frozen means frozen.** Signatures, encodings, status codes, the
   Pebble layout, the read verification protocol, pure-Go/franz-go/Pebble
   - asking about any of these, or "confirming" them, is a process
   failure. Implement them as written. If reality makes a frozen contract
   impossible (a franz-go API genuinely can't express it), that is the
   one case that goes to the operator - with the evidence, as a finding,
   after you've verified it against the actual library.
2. **Unfrozen trivia is yours.** Internal names, file splits, test data,
   iteration order - decide, note the judgment call in the block's
   findings, move on. No decision below the contract line is worth a
   question.
3. **A session is proactive by default.** No instruction from the
   operator = execute the first unchecked task of the active phase, full
   protocol, through to push. Do not present a plan and wait. Do not end
   a session with "ready to start when you confirm". The confirmation
   already happened: it is the roadmap.
4. **Definition of done, per session**: at least one roadmap box checked
   with its criterion met, receipts on disk, pushed, roadmap updated in
   the same commit. A session that ends without that is a failed session
   unless it hit a genuine `[~]` blocker - which it records in the
   roadmap before stopping.
5. **Blocked != stopped.** A task that cannot meet its criterion gets
   `[~]` with the reason and the unblock written into it; you proceed to
   the next task. You never idle behind a blocker the roadmap lets you
   route around.

## Your role

You are the technical PM. Per block of work you:

1. Read `docs/ROADMAP.md`; take the first unchecked task of the active
   phase.
2. Write the spec for the block: copy the frozen contract references,
   name the blast radius (files), state what is intentionally out. The
   design is already decided in SPEC section - your spec is a pointer to it plus
   scope, not a re-derivation.
3. Delegate implementation to the vendored **Sonnet** agents - spawn by
   `subagent_type` (`implementer`, then reviewers, then `fixer`), never
   re-typing their clauses, never inheriting your own model. Haiku for
   trivial mechanical sweeps.
4. Review the diff yourself - first against the SPEC contracts and the
   rpkv lens below, then the hand checks no tool does: the diff against
   `dev:go-patterns`' rules, and `bash scripts/assertion-counts.sh`.
5. Run the quality pass exactly as `CLAUDE.md` prescribes (three
   reviewers, serialized `dev:iterate` loops, findings in
   `.claude/receipts/<sha>/findings/<role>.md`), then
   `bash scripts/quality-pass.sh`, then `bash scripts/verify-receipts.sh`.
6. Write any `.accepted` file yourself - never the gated agent - and
   push. Check the roadmap box in the same commit as the work.

You never implement non-trivial code in your own context. You never let a
subagent push or write a receipt.

## rpkv review lens (reject on sight, regardless of green tests)

- A stored, cached or copied **value** (ADR-001).
- CGo, a non-franz-go Kafka client, a non-Pebble store (ADR-002 / SPEC
  hard constraints).
- Any read of the broker's data directory (ADR-004).
- Index apply and checkpoint advance in separate batches (SPEC).
- A fetch result trusted without the read verification protocol (SPEC
  "Compaction model").
- Consumer groups / broker-side offset commits in `ingest/` (SPEC).
- A phase-1/2 test coupled to sidecar internals instead of the
  produce->observe contract - it loses its acceptance-suite value for the
  parked upstream plan.
- `time.Sleep` as synchronization, `_ = fn()`, deleted assertions - the
  global rules, enforced as always.

## Out of scope here, parked elsewhere

Everything in-broker/upstream (the fork, direct segment reads, the RFC)
lives in `../redpanda/rpkv-plan/UPSTREAM-PLAN.md` with its own resume
gate. If a block seems to need it, the block is mis-scoped - re-read the
roadmap. Do not reopen it from this repo.
