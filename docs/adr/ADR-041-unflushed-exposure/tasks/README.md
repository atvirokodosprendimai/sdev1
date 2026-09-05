# ADR-041 Tasks

Implementation tasks for ADR-041: the unflushed window is reported as its peak,
and bounding it needs both a time and a size. See the parent ADR for the decision.

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
| T1 | A gauge that remembers the worst moment, and a bound that needs both halves | done | — | four exposure tests, then the commit, tail and durability suites |

Status: `pending` | `partial` | `blocked` | `done`.

## Contract Coupling

| Producer | Contract | Consumer(s) | Ordering note |
|----------|----------|-------------|---------------|
| T1 | `commit.Meter` | a flusher and a console (`BACKLOG.md` §12/§25) | none within this record |

## Notes

- ★ **`BACKLOG.md` §23 names the counter that does not exist, and it is a
  DIFFERENT window from the one that does.** `Gate.Pending` counts entries WRITTEN
  and not COMMITTED — data this node accepted and promised to nobody. The exposure
  is entries COMMITTED and not FLUSHED: data somebody WAS promised and could still
  lose. Two windows, opposite consequences, and only the second is a broken
  promise.
- ⚠ **§23 also names the trap: reporting the window as an AVERAGE.** In its own
  words, the exposure *"correlates with load — so the exposure is largest exactly
  when a correlated failure is most likely, and an average hides that completely."*
- ★ **The instantaneous reading has the same defect one step removed.** Asked
  after the burst has passed it reports the calm. An operator budgeting for a power
  event needs what the exposure REACHED, so the peak is reported alongside the
  present value.
- ⚠ **A bound needs BOTH a maximum age and a maximum size, and this differs from
  ADR-028 on purpose.** ADR-028 requires at least one and that is right for
  sealing; here each single-bound policy leaves a named class of tenant unbounded:
  size-only lets a QUIET tenant's single entry sit forever, and time-only lets a
  BUSY tenant commit an arbitrary volume inside the interval. §23 says it exactly:
  *"the pair is a decision rather than two constants."*
- ⚠ **An exceeded bound asks for a FLUSH; it refuses nothing.** Refusing a write
  because earlier data is unflushed converts a durability exposure into an
  availability outage — the same trade ADR-040 refuses for a below-floor leaf and
  ADR-015 refuses for a shed write. The node is behind, not unsafe.
- ⚠ **The peak resets on a flush and on NOTHING else** — in particular not on
  being read. A gauge that clears when somebody looks gives the second reader a
  different answer about the same window, and reassures them because the first one
  looked.
- ⚠ **Nothing flushes yet** (`BACKLOG.md` §12), so this measures a window whose
  closing edge does not exist. The measurement is right and it is not yet measuring
  anything real, which the parent record states rather than implies.
