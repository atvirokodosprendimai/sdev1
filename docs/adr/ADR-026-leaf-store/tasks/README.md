# ADR-026 Tasks

Implementation tasks for ADR-026: a leaf is a directory of segments, and a read
merges them by the datoms' own transaction identifiers. See the parent ADR for the
decision.

**Source of truth:** the task files' headers. This README is a derived index —
when it disagrees with a task file, the task file wins.

## Execution Order

Two tasks, sequential.

| Order | Task | Depends-on |
|-------|------|------------|
| 1 | T1 | none |
| 2 | T2 | T1 |

## Task Index

| ID | Title | Status | Covers | Acceptance |
|----|-------|--------|--------|------------|
| T1 | Read one answer out of many segments | done | — | `go test ./internal/core/leafstore/... -race -run '…thirteen tests…'` then the segstore, datom, temporal and ports suites |
| T2 | Make a fact survive a restart, from the language | pending | — | `go test ./internal/core/session/... -race -run '…four tests…'`, then RUN `cmd/sdev1-ql` twice against one directory and grep the second run's output |

Status: `pending` | `partial` | `blocked` | `done`.

## Contract Coupling

| Producer | Contract | Consumer(s) | Ordering note |
|----------|----------|-------------|---------------|
| T1 | `leafstore.Store`, `leafstore.Open`, `leafstore.Store.History`, `leafstore.Store.Entities` | T2 | T1 before T2 |

## Notes

- ★ **This is what closes `BACKLOG.md` §28** — "writes reach memory and stop
  there". There was a file of blocks (ADR-024) and a fact in bytes (ADR-025), and
  nothing put the second in the first.
- ⚠ **THE DEFECT THIS RECORD EXISTS TO PREVENT: merging segments in listing
  order.** A directory listing is sorted by name, so it reads as deterministic. It
  makes the answer depend on what the files are CALLED — a rename reorders it, a
  copy reorders it, a restore that lays files down differently reorders it. None of
  those looks like a data-loss event, and the wrong answer is a plausible one: an
  older value winning over a newer one, with no error anywhere.
- **So the merge orders by the datoms' own `TxID`**, which ADR-002 already made
  total, and ★ **a segment's name is random and means nothing** — a name that
  sorted is a name something could come to depend on. `BACKLOG.md` §12 wrote the
  trap down before there was anything to trap: whatever names a segment file must
  not encode anything a reader needs in order to interpret it.
- ★ **The directory listing is the manifest.** ADR-024 publishes by rename, so a
  file that is there is complete. This retires ADR-024's own follow-up about
  ordering two publications — there is only one.
- ⚠ **A segment is a durability tier, not the commit path.** ADR-020 fixed
  acknowledgement at N memory replicas in distinct failure domains. Sealing is
  explicit and the policy is `BACKLOG.md` §15; saying so here is what stops
  someone later "fixing" the write path to flush and moving the commit point as a
  side effect.
- **`History` is the primitive and `Load` is `History` filtered at a snapshot.**
  ⚠ Rehydration needs `History`, because no single snapshot returns all of it —
  an instant on the business axis selects the facts true AT it, not every fact
  there ever was.
- ⚠ **A rehydration that restores the datoms and forgets the search index is the
  quietest failure in T2.** `SELECT` works, the restart obviously worked, and
  `SEARCH` returns nothing with no error. Rehydration therefore goes through the
  same code path as a live write, so there is no second place to forget.
- ⚠ **A zero snapshot is refused.** A zero `TxID` bounds the system axis at
  before-anything, so the read returns nothing — indistinguishable from an entity
  that has no facts, and always a bug.
