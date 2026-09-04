# ADR-019 Tasks

Implementation tasks for ADR-019: Inject faults from a seed, and keep a written
catalogue of every failure that does not recover. See the parent ADR for the
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
| T1 | Seeded fault injection over the core, and the catalogue it fills | done | — | `go test ./internal/core/chaos/... -race -run 'TestEveryInjectedFault\|TestScheduleIsReproducible\|TestFragmentLoss\|TestCorruptFragment\|TestWriterStopped\|TestCatalogueDistinguishes'` then the erasure, tail and durability suites |
| T2 | The composed cluster, and the fault classes one process cannot reach | pending | — | `go test ./internal/core/chaos/... -run 'TestComposedCluster\|TestOutOfMemoryKill\|TestPartitionedCluster\|TestCrashDuringWrite'` |

Status: `pending` | `partial` | `blocked` | `done`.

⚠ **T2 is `pending`, not `blocked`.** It cannot pass until a node binary exists,
but that binary is work this project will do — nothing outside this repository is
being waited on, and `blocked` would imply nobody here can make it sooner.

## Contract Coupling

| Producer | Contract | Consumer(s) | Ordering note |
|----------|----------|-------------|---------------|
| T1 | `chaos.Fault`, `chaos.Disposition`, `chaos.Schedule`, the catalogue format | T2 | T1 before T2 |

## Notes

- **The catalogue is the deliverable.** `docs/adr/FAILURES.md` is not a
  by-product of the tests; it is the thing being built, and the tests exist to
  keep it honest. What this system does NOT survive is the part an operator needs
  and the part nobody writes down.
- ⚠ **"Unrecoverable by design" is a correct answer.** Losing `m+1` fragments
  destroys the block and there is no recovery; a system that produced something
  anyway would be inventing. Cataloguing it as intended is what keeps the OPEN
  entries countable — without that distinction, twenty entries tell a reader
  nothing about which two matter.
- ⚠ **The 8GB test budget can manufacture findings.** A container the kernel's
  out-of-memory killer stops looks exactly like a node that crashed, which is the
  fault being injected. A run that hits a container limit is an environment
  failure and may not write to the catalogue. This is T2's hardest requirement,
  not a footnote.
- ⚠ **Seeds matter more than realism.** An unreproducible failure is a report
  rather than a bug, and the cost of confirming it lands on somebody else. Every
  schedule is a pure function of one integer, printed on failure.
- ⚠ **The catalogue check must look BOTH ways.** Every fault needs an entry —
  that catches a new fault nobody wrote up. Every entry needs a fault — that
  catches a fault which quietly stopped being injected while its entry still
  reads as current. The second is the direction that rots.
- The chaos package asserts what the OWNING record promised. It states no
  guarantee of its own, because a second statement of every guarantee would drift
  from the first.
