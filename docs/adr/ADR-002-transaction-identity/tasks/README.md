# ADR-002 Tasks

Implementation tasks for ADR-002: Identify a transaction by a hybrid-logical-clock
triple and query the two time axes independently. See the parent ADR for the
decision.

**Source of truth:** the task files' `Depends-on` / `Produces` / `Consumes` /
`Covers` headers. This README is a derived index — when it disagrees with a task
file, the task file wins and the README must be regenerated. Regenerate rather
than hand-edit.

## Execution Order

Four tasks, so sequential order only — no wave table and no DAG. The chain is
strictly linear: each task consumes the one before it.

| Order | Task | Depends-on |
|-------|------|------------|
| 1 | T1 | none |
| 2 | T2 | T1 |
| 3 | T3 | T2 |
| 4 | T4 | T2, T3 |

## Task Index

| ID | Title | Status | Covers | Acceptance |
|----|-------|--------|--------|------------|
| T1 | The hybrid logical clock | done | — | `go test ./internal/core/hlc/... -race` |
| T2 | The transaction identifier and its total order | done | — | `go test ./internal/core/tx/... -race` |
| T3 | Two-axis visibility and the qualifier defaults | pending | — | `go test ./internal/core/temporal/...` |
| T4 | A property suite that forces the two time axes apart | pending | — | `go test ./internal/core/temporal/... -run 'TestGenerator\|TestVisibleAgreesWithOracle'` |

Status: `pending` | `partial` | `blocked` | `done`.

The Acceptance column is abbreviated for reading. Each task file carries the full
fence, including the `set -o pipefail` guard and the regression commands chained
after the new unit.

## Contract Coupling

Derived from task-file `Produces`/`Consumes` headers.

| Producer | Contract | Consumer(s) | Ordering note |
|----------|----------|-------------|---------------|
| T1 | `hlc.Timestamp`, `hlc.Clock` | T2 | T1 before T2 |
| T2 | `tx.TxID`, its ordering and encoding | T3, T4 | T2 before T3 and T4 |
| T3 | `temporal.Visible()`, `temporal.ResolveQualifiers()` | T4 | T3 before T4 |

## Notes

- T2 consumes `addr.LeafID` from ADR-001-T1. ADR-001 must be `Accepted` and its
  T1 landed before ADR-002-T2 can be executed — a cross-record dependency, which
  is why T2's `Consumes` header names it explicitly.
- **T4 is not optional polish.** ADR-002 records that the predecessor project's
  two-axis defect survived roughly 140 green tests including `-race`, because no
  test ever made the two axes disagree. T3 without T4 reproduces exactly that
  condition: a correct-looking implementation with a suite structurally unable to
  falsify it.
- Every fence in this record is red at authoring time, because none of the
  packages exist. `go test` on a missing package exits non-zero rather than
  reporting an empty pass.
