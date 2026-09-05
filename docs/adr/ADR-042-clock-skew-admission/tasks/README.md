# ADR-042 Tasks

Implementation tasks for ADR-042: a skewed clock is refused before it is
absorbed, because absorbing it cannot be undone. See the parent ADR for the
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
| T1 | Check before you absorb, and leave the clock alone when you refuse | done | — | four admission tests, then the hlc, tx and observe suites |

Status: `pending` | `partial` | `blocked` | `done`.

## Contract Coupling

| Producer | Contract | Consumer(s) | Ordering note |
|----------|----------|-------------|---------------|
| T1 | `hlc.Clock.Admit` | a transport (`BACKLOG.md` §18) | none within this record |

## Notes

- ★ **The irreversibility is not context around the decision — it IS the
  decision.** `BACKLOG.md` §4 says a skewed remote is adopted "permanently — the
  cluster cannot come back, because monotonicity is the property that forbids it",
  and `Clock.Merge`'s own comment says the same. If absorbing cannot be undone,
  a check performed afterwards is not a check; it is a report of damage.
- ⚠ **So the falsifier asserts the CLOCK, not the error.** Checking after merging
  and returning an error looks identical from the caller's side, and it is exactly
  the defect. `Last()` must be byte-identical after a refusal.
- ★ **Skew is measured by the RECEIVER and never self-reported.** A node whose
  clock is wrong is the node whose self-assessment is wrong, and asking it is
  asking the suspect to testify. The receiver already compares the two readings in
  order to merge, so this costs nothing new.
- ⚠ **The honest limit, stated rather than mitigated:** this measures the
  DIFFERENCE between two clocks, not either one's error. A receiver whose own
  clock is wrong refuses correct peers, confidently. In a cluster where one node is
  wrong that is right; where the majority is wrong it is exactly backwards, and
  nothing here can tell those apart.
- ⚠ **THE DISTINCTION THIS RECORD WOULD OTHERWISE HAVE GOT WRONG:** the bound
  applies to a timestamp arriving from another NODE, never to one read back from
  DURABLE STORAGE. `tx.Minter.Observe` merges timestamps rehydrated from a leaf;
  bounding those would make a leaf written by a formerly-skewed node permanently
  unreadable — a clock problem converted into data loss, over skew that already
  happened. So `Merge` stays unbounded and `Admit` is the network path.
- ⚠ **A message is refused; the node is not evicted.** Third time this corpus
  makes that trade, and here there is an extra reason: a skewed node is otherwise
  healthy. Its data is correct and its storage is fine — only its timestamps are
  wrong, and refusing its messages already stops the spread.
- ★ **The bound is required and never defaulted.** A datacentre and a WAN tolerate
  different skew, so a constant is wrong somewhere and nobody chose it — the same
  refusal ADR-040 makes for its grace and ADR-041 for its two bounds.
- ⚠ **Nothing calls `Admit`** (`BACKLOG.md` §18 — no transport). This decides the
  rule and the signature, as ADR-033 did for authorization.
