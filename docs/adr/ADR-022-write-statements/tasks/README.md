# ADR-022 Tasks

Implementation tasks for ADR-022: The language asserts and retracts; valid time
is the caller's and transaction time is never. See the parent ADR for the
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
| T1 | ASSERT and RETRACT, with valid time and without transaction time | done | — | `go test ./internal/core/ql/... -race -run 'TestAWriteCannotSetTransactionTime\|TestWriteRoundTrips\|TestOmittedValidityIs\|TestAWriteNamesOneEntity\|TestWriteVerbsAreAClosedPair\|TestRetractCarriesItsInterval\|TestQueryLanguageDocIsComplete\|TestPublishedExamplesParse'` then the ql, temporal and command suites |
| T2 | A session that runs statements, and a binary that shows it | done | — | `go test ./internal/core/session/... -race -run 'TestAssertThenSelectReadsItBack\|TestAReadAtAPastInstant\|TestTheSessionAssignsTransactionTime\|TestAssertThenSearchFindsIt\|TestRetractedFactIsNotReturned\|TestUnsupportedStatementIsNamed'` then build AND RUN `cmd/sdev1-ql` |

Status: `pending` | `partial` | `blocked` | `done`.

## Contract Coupling

| Producer | Contract | Consumer(s) | Ordering note |
|----------|----------|-------------|---------------|
| T1 | `ql.Write`, `ql.WriteOp`, `ql.Write.Interval` | T2 | T1 before T2 |

## Notes

- ⚠ **VALID TIME IS THE CALLER'S; TRANSACTION TIME IS NEVER.** Backdating when a
  fact was TRUE is the ordinary, correct use of the axis. Stating when it was
  RECORDED is forgery: a caller who could do it would claim to have known
  something earlier than they did, and no query could detect it, because every
  query's evidence would be the value that was forged. A `TRANSACTION` clause on
  a write is a parse error, not a clause that is quietly ignored.
- ⚠ **There is no `UPDATE` and no `DELETE`, and there never will be.** An update
  is a new assertion; a deletion is a retraction; an erasure is a destroyed key.
  A CRUD verb describes a data model this store does not have, and everything a
  caller then infers about history and erasure is wrong — silently.
- ⚠ **An omitted `VALID` clause is the write's OWN instant, not zero.**
  Defaulting to zero silently claims the fact had been true since the beginning
  of time, and nothing about the resulting datom would look unusual.
- **A retraction is a datom, never an absence.** It suppresses the value in a
  later read while remaining in the log, because "stopped being true" and "was
  never recorded" are different facts.
- ⚠ **The session is NOT the storage engine**, and the danger is that it becomes
  the specification by accident. It builds only on packages the records already
  govern and adds no rule of its own, so the real engine will have to agree with
  the RECORDS rather than with this. The moment it decides something on its own,
  there are two specifications and the unwritten one is what people run.
- ⚠ **A read must not mint a transaction.** Minting advances the sequence, so
  reads would consume identifiers and the gaps would look like lost writes to
  anyone reading a transcript. Caught while writing T2 — the first version called
  the minter to find out what time it was.
- ★ **T2's fence RUNS the binary and greps its output.** A binary that compiles
  and does nothing would otherwise pass, which is this pipeline's most common
  shipped defect.
