# ADR-001 Tasks

Implementation tasks for ADR-001: Address the key space as a 256-way trie of
static fan-out and dynamic depth. See the parent ADR for the decision.

**Source of truth:** the task files' `Depends-on` / `Produces` / `Consumes` /
`Covers` headers. This README is a derived index — when it disagrees with a task
file, the task file wins and the README must be regenerated. Regenerate rather
than hand-edit.

## Execution Order

Four tasks, so sequential order only — no wave table and no DAG. T1 and T2 have
no dependency on each other and may be built in parallel by two writers, provided
each owns its own package directory and neither touches `go.mod` after T1 creates
it.

| Order | Task | Depends-on |
|-------|------|------------|
| 1 | T1 | none |
| 2 | T2 | none |
| 3 | T3 | T1, T2 |
| 4 | T4 | T1, T2, T3 |

## Task Index

| ID | Title | Status | Covers | Acceptance |
|----|-------|--------|--------|------------|
| T1 | The key type, the leaf identifier, and the byte-wise descent | done | — | `go test ./internal/core/addr/... -run 'TestFanOut\|TestDescend\|TestLeafID'` |
| T2 | The topology map as a nested-set interval tree over declared level labels | done | — | `go test ./internal/core/topology/... -run 'TestLoad\|TestMap\|TestLevels\|TestDistance\|TestInterval\|TestAncestor\|TestNested'` |
| T3 | Deterministic placement, and client-local nearest-first ordering | done | — | `go test ./internal/core/placement/... -run 'TestResolve\|TestSpread\|TestNearest'` |
| T4 | An operator command that shows a key's descent and placement | done | — | `go test ./cmd/sdev1-addr/... -run 'TestCommand'` then build and run the binary |

Status: `pending` | `partial` | `blocked` | `done`.

The Acceptance column is abbreviated for reading. The task file carries the full
fence, including the `set -o pipefail` guard and the regression commands chained
after the new unit — an abbreviated fence is not the fence, and `adr-verify`
records the one in the task file.

## Contract Coupling

Derived from task-file `Produces`/`Consumes` headers.

| Producer | Contract | Consumer(s) | Ordering note |
|----------|----------|-------------|---------------|
| T1 | `addr.Key`, `addr.LeafID`, `addr.Descend()` | T3, T4 | T1 before T3 and T4 |
| T2 | `topology.Map`, `topology.Load()` | T3, T4 | T2 before T3 and T4 |
| T3 | `placement.Resolve()` | T4 | T3 before T4 |

## Notes

- T1 creates `go.mod`. If T1 and T2 are executed in parallel, T1 must land the
  module file first — `go.mod` is a shared file and two writers on it is the
  concurrency defect this project's own conventions warn about.
- The parent record is `Proposed`. No task may be executed until it is `Accepted`.
- Every fence in this record is red at authoring time, because none of the
  packages exist. That is deliberate: a fence that passes before the work is done
  proves nothing, and `go test` on a missing package exits non-zero rather than
  reporting an empty pass.
