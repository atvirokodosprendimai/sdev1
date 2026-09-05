# ADR-038 Tasks

Implementation tasks for ADR-038: an obligation is a state rather than an event,
so it outlives retention and only an acknowledgement clears it. See the parent ADR
for the decision.

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
| T1 | A ledger that does not forget, and that gets louder with age | done | — | five ledger tests, then the watch, observe and subscribe suites |

Status: `pending` | `partial` | `blocked` | `done`.

## Contract Coupling

| Producer | Contract | Consumer(s) | Ordering note |
|----------|----------|-------------|---------------|
| T1 | `watch.Ledger`, `watch.FromPurge` | a console or alerting path (`BACKLOG.md` §21/§18) | none within this record |

## Notes

- ★ **`BACKLOG.md` §21 sets the bar and it is not "declare a watcher".** It is
  *"whether an incomplete purge from a month ago would actually reach a person"* —
  because a declared reader that never runs is the exact failure ADR-012 exists to
  prevent, one level up.
- ⚠ **That bar rules out the obvious design.** A watcher over the event stream
  fails three ways: the stream DROPS by design (ADR-012), so it loses the events
  that matter under the load that produces them; a stream does not persist, so a
  month-old event is gone; and it would need retention, which is the trap below.
- ★ **So an incomplete purge is a STATE, not an event.** The event announces it;
  the state survives until somebody says they dealt with it.
- ⚠ **THE TRAP, and §21 walks toward it while being right.** §21 says retention
  should reuse ADR-010's `Horizon` rather than growing a second notion — correct
  for the LOG. Applied to the OBLIGATION it inverts the meaning of age: a
  thirty-one-day-old incomplete purge stops being reported under a thirty-day
  horizon, and the system answers "nothing is outstanding" precisely because the
  problem got old. `Outstanding` therefore takes no horizon at all; the signature
  is the enforcement.
- ⚠ **A retry must not reset the age.** A purge that fails daily would look one day
  old forever — the mechanism disabled while still producing output, which is the
  worst available failure.
- ⚠ **Only an ACKNOWLEDGEMENT clears one.** Silence is not resolution: a purge
  nobody retried is indistinguishable from one that completed.
- ★ **Oldest first.** The question is whether an old unanswered thing reaches
  somebody, and newest-first buries it further every day.
- ★ **Raised by the EMITTER, never from the stream** — which also settles §21's
  sampling warning for this path structurally rather than by argument: an
  obligation never travels on a stream that can drop or sample it.
- ⚠ **THE GAP: the ledger is in memory, so a restart loses it.** Rule 2 says time
  and retention do not clear an obligation; a restart currently does. Named on the
  parent record and in a follow-up rather than implied away.
