# ADR-040 Tasks

Implementation tasks for ADR-040: a leaf below the floor is reported and never
evicted, and only its age tells a restart from a shortfall. See the parent ADR for
the decision.

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
| T1 | A grace that is the whole discriminator, and a status that never hides behind it | done | — | four shortfall tests, then the durability, observe and watch suites |

Status: `pending` | `partial` | `blocked` | `done`.

## Contract Coupling

| Producer | Contract | Consumer(s) | Ordering note |
|----------|----------|-------------|---------------|
| T1 | `durability.Watchdog` | a console or alerting path (`BACKLOG.md` §18/§25) | none within this record |

## Notes

- ★ **`BACKLOG.md` §10's hardest question answers itself once stated precisely.**
  It asks how an operator tells "briefly degraded during a restart" from "genuinely
  short of copies", noting they *"look identical for the first few seconds"*. They
  do not merely look identical — **instantaneously they ARE identical.** A leaf
  holding two of three domains is holding two of three, whatever the reason.
- ⚠ **So the discriminator can only be TIME.** A restart resolves in seconds; a
  shortfall does not. Any instantaneous rule — the shape of the loss, which domains
  went — is inventing a signal that is not in the observation.
- ⚠ **And the grace must be DECLARED, not defaulted.** What a restart costs is a
  property of a deployment. A constant here pages the wrong deployments and stays
  silent for the others, and nobody wrote the number down.
- ⚠ **THE TRAP: "suppress for N seconds" is the obvious single rule and it is
  wrong.** It conflates hiding the STATUS with withholding the OBLIGATION. The
  status must show a short leaf THROUGHOUT the grace — an operator watching a
  rolling restart wants to see the dip and its recovery, they simply do not want to
  be answerable for it. Suppressing both makes the grace a window where a real
  shortfall is invisible.
- ⚠ **A below-floor leaf is NOT evicted from the read path.** It is degraded, not
  wrong: its data is readable and correct, so eviction trades a durability risk for
  a certain outage — and it removes exactly the copies that still exist. Same
  argument ADR-015 uses for a shed write.
- ★ **Past the grace it is an ADR-038 obligation** — the second time that record
  pays off, after ADR-039's all-withdrawn fleet. A leaf short of copies is a state,
  it matters, nobody has dealt with it. It does not clear when the leaf recovers.
- ⚠ **Re-observing a still-short leaf must not restart its clock**, which is
  ADR-038 rule 6 arriving in a different package: a watchdog polled every second
  would otherwise show every leaf as one second old forever.
- ⚠ **Re-replication stays open** (`BACKLOG.md` §19 — it needs consensus). The
  report is what makes that choice measurable, the same move ADR-039 made for the
  all-withdrawn fleet.
