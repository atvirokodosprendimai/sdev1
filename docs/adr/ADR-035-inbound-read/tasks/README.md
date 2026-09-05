# ADR-035 Tasks

Implementation tasks for ADR-035: an entity that things point at is a table, and
reading it is a bounded set rather than a scan. See the parent ADR for the
decision.

**Source of truth:** the task files' headers. This README is a derived index —
when it disagrees with a task file, the task file wins.

## Execution Order

Two tasks, strictly in order: T2 evaluates what T1 makes sayable.

| Order | Task | Depends-on |
|-------|------|------------|
| 1 | T1 | none |
| 2 | T2 | T1 |

## Task Index

| ID | Title | Status | Covers | Acceptance |
|----|-------|--------|--------|------------|
| T1 | Say it in the language — `[e]`, `->a`, LIMIT and OFFSET | done | — | four parser tests plus both documentation gates, then `go test ./...` |
| T2 | Answer it from the datoms — the port, the scan, and the drop | done | — | five evaluator tests, then `go test ./...` |

Status: `pending` | `partial` | `blocked` | `done`.

## Contract Coupling

| Producer | Contract | Consumer(s) | Ordering note |
|----------|----------|-------------|---------------|
| T1 | `ql.Read.Inbound`, `ql.Read.Page`, `ql.Page` | T2 | T2 cannot evaluate a statement the parser cannot produce |
| T2 | `ports.Inbound`, `leafstore.Store.Referrers` | a served path | none within this record |

## Notes

- ★ **The bounded set enumeration was missing.** `BACKLOG.md` §20 defers "list all
  entities" because it needs a planner and routing. "The entities that point AT
  `e`" needs neither to be MEANINGFUL, because the target is addressable — which is
  why this is answerable now and a general scan is not.
- ⚠ **The referrers still live on their own leaves.** A complete cluster-wide
  answer is a routing question (`BACKLOG.md` §18/§20). This record decides the
  meaning and implements it against one reader; it does not claim to scale yet.
- ★ **The datoms decide; an index only proposes.** ADR-021's rule, reused. The
  thing a cached inbound index gets wrong is a RETRACTED reference — it never
  un-proposes — so confirmation against the datoms is what makes a later index a
  pure optimisation rather than a source of wrong answers.
- ⚠ **A member missing ANY named attribute is dropped entirely.** Projected,
  predicated, or failing the comparison — all three, and the member contributes no
  rows rather than a row with a hole.
- ★ **That is deliberately the OPPOSITE of `OPTIONAL` in `MATCH SHAPE`**, where an
  unmatched leg keeps the row with an unbound binding. A resemblance query treats a
  partial match as an answer; a table read does not. Two rules, both chosen, and
  recorded together so the difference is not read as an oversight.
- ⚠ **Paging is over MEMBERS, AFTER the drop, in a DETERMINISTIC order.** Each
  half is a separate way to produce a plausible wrong answer: cutting a member in
  half, unpredictable page sizes, or a page that repeats and skips.
- ⚠ **Paging is coherent only within one snapshot.** Across a moving present,
  members shift between pages; pinning `AS OF` / `TRANSACTION` is the caller's fix.
- ⚠ **`->a` versus a bare `a` is refused rather than treated as a synonym.** A bare
  name would mean the index entity's own attribute — the join. Keeping the two
  spellings distinct now is what lets the join be added later without changing what
  already-written statements mean.
- ⚠ **A reader that cannot scan REFUSES.** "Nothing points here" and "I cannot tell
  you what points here" are different answers.
