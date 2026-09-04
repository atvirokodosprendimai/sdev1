# ADR-025 Tasks

Implementation tasks for ADR-025: a datom is encoded in a versioned run, and a
short read is a refusal rather than a retraction. See the parent ADR for the
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
| T1 | Encode a run of datoms, and refuse everything that is not one | done | — | `go test ./internal/core/datom/... -race -run '…ten tests…'` then the ports, temporal and tx suites |

Status: `pending` | `partial` | `blocked` | `done`.

## Contract Coupling

| Producer | Contract | Consumer(s) | Ordering note |
|----------|----------|-------------|---------------|
| T1 | `datom.Encode`, `datom.Decode` | a leaf store on segments (`BACKLOG.md` §28) | none within this record |

## Notes

- ★ **This is the last purely local gap.** There is a block format, a segment
  format, and a store that puts segments on a disk — and no way to put a *fact*
  in one. `ports.Datom` existed only as a Go struct.
- ⚠ **THE HAZARD THAT DROVE THE RECORD: a zero-filled `Datom` is a RETRACTION.**
  ADR-003 fixed that a retraction is a datom with `Assert` cleared rather than an
  absent datom, because "this stopped being true" and "this was never recorded"
  are different facts. Go's zero value for a `bool` is `false`. So a decoder that
  tolerates a short buffer and returns what it filled does not return a partial
  answer — it withdraws a fact, reports success, and looks completely healthy.
- **So every field is written on every datom, always**, and a short buffer is a
  named refusal that returns no datoms at all. ⚠ Not even alongside an error: a
  caller who checks the slice before the error is exactly the caller this hurts.
- ⚠ **An encoding is wrong exactly once and then there is data in it.** The format
  version is in the header from the first write rather than added when it is first
  needed — a format that acquires a version later cannot describe what came
  before it.
- **Every length is checked before anything is allocated.** A length prefix is a
  number a corrupt block chooses, and one test measures heap growth rather than
  the error, because the error would be returned just as truthfully after a
  four-gigabyte allocation.
- ⚠ **Interning was re-deferred on purpose, not skipped.** `BACKLOG.md` §12 has
  called it the largest available saving since ADR-005, and 49 of a datom's 74
  fixed bytes are the transaction identifier. It is one decision about where a
  dictionary lives, and taking it inside a record about a datom's fields would
  have answered it by accident.
- **No checksum here.** ADR-005 checksums the block and ADR-024 verifies it on
  read. A second mechanism would be a second answer to one question, and the two
  would eventually disagree.
