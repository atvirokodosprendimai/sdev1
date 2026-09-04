# ADR-024 Tasks

Implementation tasks for ADR-024: a sealed segment is blocks then an index then a
trailer, and it exists only when it is complete. See the parent ADR for the
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
| T1 | Write a segment to a disk, and find a block in one | done | — | `go test ./internal/core/segstore/... -race -run '…twelve tests…'` then ADR-005's suite |

Status: `pending` | `partial` | `blocked` | `done`.

## Contract Coupling

| Producer | Contract | Consumer(s) | Ordering note |
|----------|----------|-------------|---------------|
| T1 | `segstore.Writer`, `segstore.Reader` | future storage work (`BACKLOG.md` §12, §15, §28) | none within this record |

## Notes

- ★ **This is the first thing in the corpus that outlives a process.** Twenty-three
  records describe how bytes are laid out, compressed, coded, addressed, versioned
  in time and searched; every one of them ran in a map that vanished at exit.
- ⚠ **THE QUESTION IS NOT WHAT THE FILE LOOKS LIKE BUT WHEN IT EXISTS.** ADR-017's
  read path takes no locks because sealed data is immutable. A file being written
  under its final name IS a half-sealed segment, visible to anyone who lists the
  directory — and one reader observing one would make that whole argument unsound.
  So the bytes go to a temporary name and the rename is the publication.
- **A crash therefore leaves a file that is NOT A SEGMENT**, rather than a broken
  one. That distinction is worth the trailer: "incomplete" can be deleted without
  judgement, "corrupt" needs someone to look.
- ⚠ **An index is a list of byte offsets, so a wrong one does not fail** — it reads
  arbitrary bytes, and arbitrary bytes are indistinguishable from a block until the
  block's own checksum says otherwise. Everything the index claims is checked
  before any offset from it is followed: its checksum, its bounds, that it is
  sorted, and that the header's block count agrees with it.
- **Reads go through a memory mapping**, which is the same immutability argument
  applied to a file: any number of readers share one mapping with no coordination.
  ⚠ Two prices are paid for it and both are recorded rather than discovered — an
  I/O error on a mapped page is a SIGBUS and not an error return, and a block
  handed to a caller must be OWNED, because a view into the mapping is a dangling
  pointer the instant `Close` unmaps and behaves perfectly until then.
- **Targets are macOS and Linux.** There is no fallback, so another platform fails
  to compile rather than quietly taking a different read path.
- What genuinely remains: when to seal (`BACKLOG.md` §15), a manifest naming which
  segments exist (§15), erasure-coding a sealed one (§12), and wiring the session
  onto it (§28).
