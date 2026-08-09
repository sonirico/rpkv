# rpkv

Key-value reads over Redpanda topics without duplicating values: a Pebble
secondary index `key → (partition, offset)`, values served from the log
itself. Sidecar first, Redpanda fork second, direct segment reads last and
gated. Full context lives in `docs/SPEC.md`; decisions in `docs/adr/`; work
state in `docs/ROADMAP.md`; the PM session's operating manual in
`docs/PM-BRIEF.md`.

## Session protocol

This project is built across many independent sessions. The repo is the
memory; no session may depend on conversational context from a previous one.

1. Start by reading `docs/ROADMAP.md`. The active phase is the first one with
   unchecked tasks. Pick the first unchecked task unless the user says
   otherwise.
2. Before implementing a stateful or algorithmic piece (index schema,
   checkpoint recovery, ingest ordering), propose the design in chat and wait
   for confirmation.
3. A task is done only when its verification criterion (stated inline in the
   roadmap) passes. Then check its box in `docs/ROADMAP.md`.
4. Any decision that changes or extends an ADR gets written to `docs/adr/`
   in the same session it was made.
5. Commit at the end of the session with the roadmap update included. Never
   leave a checked box uncommitted.
6. Do not start tasks from a later phase while the active phase has unchecked
   tasks, unless the roadmap marks them as parallelizable.

## Subagent contract

Implementation is delegated to subagents; the main session writes the spec,
reviews the result, runs the gate and pushes. This contract is vendored from
vigia, where every rule was paid for by a real failure — the eleven-entry
ledger lives in vigia's `CLAUDE.md` ("How this got its shape") and is worth
reading once. The common thread: every failure was caught by a person
looking, and every fix moved the catching into code. rpkv imports the rules
already-paid-for; a rule that seems pointless here probably has a ledger
entry there.

### Delegating

The delegation clauses (invoke `dev:go-patterns` before writing Go; report
every judgment call; stay inside the named blast radius; never push, never
write a receipt or `.accepted`) are **vendored as agent definitions** in
`.claude/agents/` — `implementer`, `reviewer-tests-quality`,
`reviewer-tests-coverage`, `reviewer-go-patterns`, `fixer` — each with
`model: sonnet` pinned in its frontmatter. Spawn them by `subagent_type` and
add only the block's spec. Do not re-type the clauses into a prompt: every
retyping is a chance to drift. When this file and an agent definition
disagree, fix both in the same commit.

### Reviewing

The main session's own review comes **first**, and it is about design: is
the blast radius right, does this hold at 10^8 keys, does it violate an ADR
(a cached value, a data-directory read). Sending mechanical fixes into a
diff whose design is still wrong just makes a bigger diff to throw away.

Two things the main session checks by hand, every time, because no tool
does them:

- **The diff against `dev:go-patterns`' rules.** The skill's *invocation*
  is recorded by the global hook (`~/.claude/receipts/skills/`); whether
  the agent followed it is not.
- **Assertion counts.** `bash scripts/assertion-counts.sh` — per changed
  test file, the count at the merge base against HEAD; investigate any
  decrease.

Reading the receipts is vendored: `bash scripts/verify-receipts.sh [sha]`
prints the quality-pass verdict, any `.accepted` beside it, and every
mutation receipt's `mutation_detected`/`restored_clean`, applying the Stop
hook's own forgiveness rules so the two cannot disagree.

### The quality pass

A **fresh Sonnet subagent with clean context** per role, spawned the moment
the implementer reports done and the main session's review has passed —
never the agent that wrote the code, and never one that read the
implementation transcript. Implementation and review are strictly
sequential.

1. Review runs as **three subagents, one skill each**: `dev:iterate
   dev:tests-quality branch`, `dev:iterate dev:tests-coverage branch`, and
   `dev:go-patterns` against the whole diff. Whatever `go-patterns` raises
   goes to a **fourth** agent (`fixer`) — the one that found it does not
   fix it.
2. **The two `dev:iterate` loops run one after the other, never at the same
   time** (`iterate`'s `--init` takes a worktree lock and refuses a second
   loop — a reviewer turned away by the lock is the rule working);
   `go-patterns` runs alongside either; the fixer runs after both. The main
   session's worktree must be **clean before any reviewer is spawned**.
3. **If `tests-quality`'s Step-0 guard fires on a tests-only block**, re-run
   with `dev:tests-quality --audit`. If the diff touches production code,
   the guard is right — restore or split.
4. **Report the skills' findings as findings, never as a pass.** Their
   output is testimony, not a receipt; nothing gates on it.
5. **Each reviewer works in its own turn.** A turn that ends without files
   on disk is a failed run, not a report.
6. **Findings land in `.claude/receipts/<sha>/findings/<role>.md`** — one
   file per reviewer (`tests-quality`, `tests-coverage`, `go-patterns`,
   `fixer`), never a shared one — and the main session reads the files, not
   the messages.

**The gate is `bash scripts/quality-pass.sh`.** It writes
`.claude/receipts/<sha>/quality-pass.json` recording only what a machine
decides: changed files, whether any is production Go, `just check`'s exit
code, and per-function coverage of changed production files. The Stop hook
(`scripts/quality-pass-gate.sh`) refuses to end a session with unpushed Go
commits lacking that receipt; git's pre-push hook
(`scripts/pre-push-gate.sh`, installed via `just setup` through the tracked
`githooks/` directory) reads the same receipt at the one point a push
cannot walk around it. **Accepting a measured failure is allowed** at the
cost of a `check.accepted` / `coverage.accepted` file beside the receipt
saying why — written by the **main session only**, never the gated agent.

**The gates are themselves tested.** `scripts/harness-test.sh` is a
dependency of `just check` and runs first, over throwaway repositories.
Every case in it is a defect that actually shipped (in vigia). Add to it
whenever a gate is fixed.

**Mutation checks** run via `scripts/mutation-check.sh <file> <sed-expr>
<pkg> [run-pattern]`, never as a narrated re-run. Verify
`mutation_detected` and `restored_clean` are both `true` in the JSON
receipt. A claimed mutation check with no receipt on disk is worth nothing.

**Only the main session pushes**, and only after reading the receipts.

### Isolation policy

Isolation is a policy, not a per-call judgment:

| Agent | Isolation |
|---|---|
| Read-only reviewers (`reviewer-go-patterns`) | No worktree; own findings file. |
| `dev:iterate` loops | Main checkout, serialized, lock-enforced — their scope is `branch`, the diff under review *is* this checkout's branch. |
| One implementer, or the fixer | Main checkout; implementation and review are strictly sequential, nothing else is writing. |
| Parallel implementers | **Forbidden without `isolation: worktree`**, plus an explicit merge-back owned by the main session. |

### Scope

**Scope is `branch`, and the sequence is: commit locally, do not push, run
the pass, then push.** `branch` diffs against the merge base with
`origin/<default>`, so unpushed local commits are exactly the block under
review. `uncommitted` degenerates on iteration 2; `commit` reviews the
loop's own homework — both are wrong for the pass. `commit` remains right
for a standalone review of an already-pushed block: a repair, not the
workflow.

Three standing traps: `dev:tests-quality` cannot run on a block that
changes an interface (hand-review the flagged files, assertion counts
included); `refs/remotes/origin/HEAD` must exist or `branch` scope
silently resolves to an empty diff (`git remote set-head origin main`,
once per clone); a subagent's skill *invocation* is verifiable via the
global PostToolUse hook — its absence is a finding, not a silence to
interpret charitably.

## Invariants (non-negotiable, see ADRs for rationale)

- **No value is ever stored, cached or copied by rpkv.** The index holds
  pointers and checkpoints; the log holds values. A value cache is the one
  forbidden move. (ADR-001)
- **The index is a projection.** Destroy it and it rebuilds by replaying
  the topic; its visible state must equal a naive materialization at the
  same checkpoint — that equivalence is a property test, not a comment.
  (SPEC "Semantics")
- **Index apply and checkpoint advance commit in one Pebble batch.** A
  crash between them is unrepresentable; recovery is resume + idempotent
  re-apply. (ADR-003)
- **Sidecar phases speak only the public Kafka protocol.** Reading the
  broker's data directory from outside is closed permanently, not phased.
  (ADR-004)
- **The fork is a minimal, feature-flagged, rebaseable patch series** in
  its own checkout, never vendored here. Flag off ⇒ byte-identical to
  upstream, verified by test. (ADR-005)
- **Packages are importable libraries.** Public packages at the module
  root (`index/`, `ingest/`, `fetch/`, `server/`, `clock/`); `internal/`
  is wiring and glue; public packages never import `internal/`
  (`scripts/check-boundaries.sh` enforces it). No CGo.

## Performance discipline

- Idiomatic Go first. The sidecar is I/O-bound; the broker round-trip
  dominates path-A reads, so micro-optimizing around it is noise.
- Two hot paths get allocation discipline from the start and a benchmark
  in CI: index apply (the ingest write path) and pointer lookup. Everywhere
  else, clarity wins.
- No lock-free structure, pool, or in-place mutation lands without a pprof
  profile or benchmark proving the idiomatic version is the bottleneck.
- The path-B decision is made by benchmark on record, never by taste.
  (ADR-004)

## Testing discipline

- Index semantics: property-based tests with `pgregory.net/rapid` — the
  naive-materialization equivalence, crash-recovery idempotence, replay
  determinism.
- Phase-1 tests are **contract tests** (produce records → observe `Get`),
  written to run unchanged against the phase-3 fork endpoint; coupling
  them to sidecar internals loses their acceptance-suite value. (ADR-002)
- Integration tests need a real Redpanda: the dockerized dev loop
  (roadmap, phase 0) behind a build tag. No testcontainers in unit tests.
- All time via a `Clock` interface; mock clock with `Advance()` in tests.
- Everything else per the global `CLAUDE.md` and `dev:go-patterns`: AAA,
  table-driven, `newTestXxx` fixtures, no `_ = fn()`, no `time.Sleep`
  synchronization, never delete an assertion to make a refactor fit.

## Layout

```
index/              Pebble store: key→Pointer, checkpoints, atomic batches
ingest/             topic consumer → index apply (franz-go)
fetch/              path-A reader: single-record fetch by (partition, offset)
server/             query surface: Get(topic, key) → value, pointer, checkpoint
clock/              injectable Clock; sole production caller of time.Now/After

cmd/rpkv/           main: wiring owner, and nothing but wiring
internal/           this binary's flags, env, process glue

docs/spikes/        recon spikes (phase 2: redpanda-storage/)
docs/benchmarks/    numbers on record, with the commands that reproduce them
redpanda/           (gitignored) the fork checkout — its own repo, ADR-005
```
