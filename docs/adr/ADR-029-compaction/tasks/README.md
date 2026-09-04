# ADR-029 Tasks

Implementation tasks for ADR-029: compaction merges segments and drops nothing,
and a datom seen twice is returned once. See the parent ADR for the decision.

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
| T1 | Merge the segments, and return a datom once | done | — | `go test ./internal/core/leafstore/... -race -run '…five tests…'` then everything that reads a leaf |

Status: `pending` | `partial` | `blocked` | `done`.

## Contract Coupling

| Producer | Contract | Consumer(s) | Ordering note |
|----------|----------|-------------|---------------|
| T1 | `leafstore.Store.Compact`, `Policy.MaxSegments` | a caller that compacts on a schedule (`BACKLOG.md` §15) | none within this record |

## Notes

- ★ **ADR-028 bounded the tail; nothing bounded the number of SEGMENTS.** A read
  touches every one of them, so a leaf sealed a thousand times costs a thousand
  block lookups to answer one question. That was the largest cost in the system.
- ⚠ **Compaction is the first operation that REMOVES a segment file**, and
  ADR-026 has no manifest on purpose: the directory listing IS the set. That works
  beautifully while files are only added. A removal makes the change observable in
  the middle — publish first and a reader sees every datom TWICE; remove first and
  it sees NONE.
- **The ordering fixes the crash's direction and cannot fix the overlap.**
  Publishing before removing means the worst a crash leaves is both copies, which
  is recoverable. Removing first means it can leave neither, which is not.
- ★ **So a datom appearing in more than one segment is returned ONCE.** That is
  what makes the ordering safe rather than merely better: a crash leaves the
  overlap on disk PERMANENTLY, so without deduplication every later read
  double-counts forever.
- ⚠ **ADR-026 rejected deduplication and this does not contradict it.** Its reason
  was that deduplication "needs a key that says two datoms are the same fact, and
  this store does not get to invent one — two identical assertions from two
  transactions are two facts". Full-field equality INCLUDES the transaction, so it
  cannot conflate them, and ★ nothing is invented: ADR-025 already gave a datom a
  canonical form.
- ⚠ **Compaction is a LAYOUT operation and drops no fact.** Discarding superseded
  datoms while rewriting them anyway is what the word usually means and it is
  wrong twice: a superseded fact is still the answer to a question about the past,
  and dropping data is ADR-010's purge, which has a horizon and an acknowledgement
  protocol a background merge has none of.
- ⚠ **A "changes no answer" test must compare HISTORY, not a query.** A `SELECT`
  resolves to the latest visible datom, so dropping every superseded fact would
  leave queries answering identically while the past became unanswerable.
- **Merging everything into one segment is quadratic over a leaf's lifetime.**
  Tiering fixes it and is deferred — the ratios are the whole design, and choosing
  them against no measured leaf is choosing them on taste.
- ⚠ **Nothing compacts on its own.** `ShouldCompact` decides and never performs,
  the same shape ADR-028 fixed for sealing.
