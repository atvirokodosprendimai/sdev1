# ADR-003 Tasks

Implementation tasks for ADR-003: Make the entity the transaction boundary, and
give the write path its own reads. See the parent ADR for the decision.

**Source of truth:** the task files' `Depends-on` / `Produces` / `Consumes` /
`Covers` headers. This README is a derived index — when it disagrees with a task
file, the task file wins and the README must be regenerated. Regenerate rather
than hand-edit.

## Execution Order

Three tasks, so sequential order only — no wave table and no DAG.

| Order | Task | Depends-on |
|-------|------|------------|
| 1 | T1 | none |
| 2 | T2 | T1 |
| 3 | T3 | T1, T2 |

## Task Index

| ID | Title | Status | Covers | Acceptance |
|----|-------|--------|--------|------------|
| T1 | The read/write port asymmetry | done | — | `go test ./internal/core/ports/... -run 'TestReadModel\|TestReader\|TestStore\|TestSnapshot\|TestWriter\|TestPublisher\|TestDatom'` |
| T2 | The single-entity transaction, and its refusal | done | — | `go test ./internal/core/command/... -run 'TestNew\|TestAssert\|TestRetract\|TestRefusal\|TestTransaction\|TestDatoms'` |
| T3 | The structural guard that keeps the asymmetry real | done | — | `go test ./internal/core/ports/... -run 'TestNoReadPackage\|TestExemption\|TestGuard'` |

Status: `pending` | `partial` | `blocked` | `done`.

The Acceptance column is abbreviated for reading. Each task file carries the full
fence, including the `set -o pipefail` guard and the regression commands chained
after the new unit.

## Contract Coupling

Derived from task-file `Produces`/`Consumes` headers.

| Producer | Contract | Consumer(s) | Ordering note |
|----------|----------|-------------|---------------|
| T1 | `ports.Reader`, `ports.Writer`, `ports.Store`, `ports.Publisher`, `ports.Snapshot` | T2, T3 | T1 before T2 and T3 |
| T2 | `command.Transaction`, `command.ErrCrossEntity` | T3 | T2 before T3 |

## Notes

- T1 and T2 also consume from other records: `addr` (ADR-001/T1), `tx`
  (ADR-002/T2). Both are landed, so neither is a live dependency — the qualified
  form in the `Consumes` headers records the relationship rather than a wait.
  ⚠ Write a cross-record reference with the SLASH form (`ADR-002/T2`), not the
  hyphen form: `adr-lint` reads a trailing `-T2` as this record's own sibling T2
  and reports a dependency cycle that does not exist.
- **T3 is not optional polish.** ADR-003's asymmetry is a compile-time property,
  and a compile-time property with no guard degrades silently the first time
  somebody is in a hurry. T3 is what makes rule 7 true next year.
- Every fence in this record is red at authoring time, because none of the
  packages exist. `go test` on a missing package exits non-zero rather than
  reporting an empty pass.
- ⚠ When adding a test during implementation, check its name is SELECTED by the
  fence's `-run` filter before running any mutant. A falsifier beside the command
  rather than inside it proves nothing, and it produced three spurious surviving
  mutants across ADR-002.
