# ADR-010 Tasks

Implementation tasks for ADR-010: One subscription primitive, and a purge that is
a fan-out with per-sink acknowledgement. See the parent ADR for the decision.

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
| T1 | The cursor, the subscription, and the registry a purge must reach | done | — | `go test ./internal/core/subscribe/... -race -run 'TestCursorAdvances\|TestCrashedSink\|TestCursorIsATransaction\|TestUnregisteredSink\|TestDuplicateRegistration'` |
| T2 | Three verbs, three guarantees, and a purge that is not done until every sink says so | done | — | `go test ./internal/core/subscribe/... -race -run 'TestPurgeIsIncomplete\|TestPurgeNamesWho\|TestThreeVerbs\|TestOnlyShredding\|TestThereIsNoDelete'` then the crypt and tail suites |

Status: `pending` | `partial` | `blocked` | `done`.

## Contract Coupling

| Producer | Contract | Consumer(s) | Ordering note |
|----------|----------|-------------|---------------|
| T1 | `subscribe.Cursor`, `subscribe.Subscription`, `subscribe.Registry` | T2 | T1 before T2 |

## Notes

- ⚠ **Three things get called "delete", and only one of them is erasure.**
  MARKING makes a subject invisible and changes no bytes — anyone holding them
  still has them. SHREDDING destroys the key and is the only one that reaches
  coded stripes, offline replicas and backups. SWEEPING reclaims space eventually
  and reaches neither. An operator who marks a subject and reports it erased has
  said something false, and there is deliberately no `Delete` verb for that
  reason.
- ⚠ **A purge that reports success while a sink still holds the data is the
  failure this record exists to prevent.** It surfaces months later as a restore
  that resurrects what an operator was told was gone, with nothing having
  reported anything in between. So a purge is a fan-out with per-sink
  ACKNOWLEDGEMENT, and the sinks it can reach are exactly the ones registered.
- ⚠ **An unacknowledged sink makes a purge INCOMPLETE — not done, not failed.**
  "Done" is a lie. "Failed" suggests nothing happened, when the primary copy is
  already gone, and would send an operator to retry the whole thing instead of
  chasing one sink. Incomplete is the only one of the three that is both true and
  actionable.
- **A cursor is a transaction identifier, not an offset.** It survives
  compaction and renumbering, and it is comparable with everything else the
  system orders by — including a snapshot a reader is holding.
- **Delivery is at-least-once and sinks must be idempotent.** Exactly-once needs
  the sink's own writes to be transactional with its cursor advance, which is the
  SINK's property. Saying so is more useful than implying a guarantee this layer
  cannot make.
- ⚠ When testing a purge, do not let every sink acknowledge — that is the case
  that proves nothing. The dangerous sink is one that is registered and never
  answers, which is what a silently-unwired backup looks like from here.
