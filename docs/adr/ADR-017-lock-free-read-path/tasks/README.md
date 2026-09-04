# ADR-017 Tasks

Implementation tasks for ADR-017: Make the read path lock-free by publishing a
watermark, never by guarding mutable state. See the parent ADR for the decision.

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
| T1 | The append-only tail, and the watermark that publishes an entry | done | — | `go test ./internal/core/tail/... -race -run 'TestPartialEntry\|TestReadersAndWriter\|TestSnapshotIsRepeatable\|TestChunkGrowth\|TestAppendRequires'` |
| T2 | Bound a read by a transaction identifier, and prove no reader takes a lock | done | — | `go test ./internal/core/tail/... -race -run 'TestReadersTakeNoLock\|TestGuardFlags\|TestSnapshotExcludes\|TestReadIsBounded'` then the tx and temporal suites |

Status: `pending` | `partial` | `blocked` | `done`.

## Contract Coupling

| Producer | Contract | Consumer(s) | Ordering note |
|----------|----------|-------------|---------------|
| T1 | `tail.Tail`, `tail.Watermark`, `tail.Entry` | T2 | T1 before T2 |

## Notes

- ⚠ **The order of the two steps in `Append` IS the mechanism.** Write the entry
  completely, then advance the watermark. Reversed, a reader can observe an entry
  that is not finished — a torn read returns data that never existed, and nothing
  downstream recovers from it.
- ⚠ **An unfinished entry is UNREACHABLE, not protected.** That is the difference
  between this design and a lock, and it is why a reader pays one acquire-load
  instead of contending with the writer. Nothing a reader can see is ever mutated
  in place.
- ⚠ **`-race` is part of both fences, not a convenience.** The claim is about
  concurrent access; a suite that never runs the detector cannot observe the fault
  class these tasks exist to prevent. `DATA RACE` is grepped explicitly because
  the detector's report does not always change the exit code of the run.
- ⚠ **A clean race-detector run proves nothing if the goroutines never
  overlapped.** `TestReadersAndWriterActuallyOverlap` asserts the overlap
  happened. Without it, a concurrency test that quietly serialized is
  indistinguishable from one that is correct, and both are green.
- ⚠ **The no-lock guard needs its positive control.** A negative assertion over a
  clean package passes whether the guard works or has stopped looking, and those
  two are identical from the outside. `TestGuardFlagsAKnownOffender` is what makes
  the guard mean anything.
- Writer-versus-writer contention is NOT this record's subject. A leaf has one
  writer by ADR-003, and who that writer is, is ADR-009's.
