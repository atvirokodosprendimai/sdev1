# ADR-030 Tasks

Implementation tasks for ADR-030: a compression block holds one subject's datoms,
because a shared block is a compression oracle. See the parent ADR for the
decision.

**Source of truth:** the task files' headers. This README is a derived index —
when it disagrees with a task file, the task file wins.

## Execution Order

One task.

| Order | Task | Depends-on |
|-------|------|------------|
| 1 | T1 | none |

## Task Index

| ID | Title | Status | Covers | Acceptance |
|----|-------|--------|--------|------------|
| T1 | Hold the rule the code already follows | done | — | `go test ./internal/core/leafstore/... -race -run 'TestNoBlockMixesSubjects'` then the segstore and datom suites |

Status: `pending` | `partial` | `blocked` | `done`.

## Contract Coupling

| Producer | Contract | Consumer(s) | Ordering note |
|----------|----------|-------------|---------------|
| none — this task adds no surface | — | — | — |

## Notes

- ★ **Every segment this system has written already holds one subject per block**,
  because that is what `leafstore` happened to do when it was built. It was never
  decided. ⚠ An undecided property that happens to hold is one refactor away from
  not holding — and the refactor that breaks this one arrives with a benchmark
  showing an improvement.
- ⚠ **A SHARED BLOCK IS A COMPRESSION ORACLE.** A codec's output size is a
  function of everything inside it, so two subjects in one block make each
  subject's data a probe for the other's: write data you control, watch the block
  shrink, learn about data you do not. That is why `BACKLOG.md` §13 called this a
  confidentiality decision wearing a performance costume.
- **Erasure requires it.** A block mixing subjects is either encrypted under ONE
  key — so shredding one subject means rewriting everything sharing its block,
  the find-and-delete model crypto-shredding replaced — or it is not a single
  ciphertext, which changes what ADR-005 says a block is.
- **Reclaim requires it.** Space is reclaimed by dropping a block whole. One
  subject's block is droppable when that subject is gone; a thousand subjects'
  block is droppable when all thousand are, which in practice is never.
- ★ **The container already assumed it.** ADR-024 keys a block by one key and
  `Get` is a lookup; a block of many subjects would need a key that is a range,
  and finding one subject would become a scan of its neighbours.
- ⚠ **The cost is real and named: worse compression.** Attribute names and value
  shapes repeat across subjects and a per-subject block cannot exploit that.
  Recovering it is the interning question (§12) — a decision about where a
  dictionary lives, not a licence to merge blocks.
- ⚠ **The hazard in the TASK is a test that cannot fail.** The code already
  satisfies the rule, so a checker with a bug passes exactly as loudly as a
  correct one. The test builds a deliberately MIXED segment and asserts its own
  checker rejects it, before trusting it to pass on the real ones.
