# ADR-034 Tasks

Implementation tasks for ADR-034: the read verb is `READ`, and `SELECT` stays
reserved so that typing it says so. See the parent ADR for the decision.

**Source of truth:** the task files' headers. This README is a derived index —
when it disagrees with a task file, the task file wins.

## Execution Order

One task. A rename is one change or it is a broken tree.

| Order | Task | Depends-on |
|-------|------|------------|
| 1 | T1 | none |

## Task Index

| ID | Title | Status | Covers | Acceptance |
|----|-------|--------|--------|------------|
| T1 | Make READ the verb and make typing SELECT say so | done | — | the three new tests plus both documentation gates, then `go test ./...`, then a grep of the published pages |

Status: `pending` | `partial` | `blocked` | `done`.

## Contract Coupling

| Producer | Contract | Consumer(s) | Ordering note |
|----------|----------|-------------|---------------|
| T1 | `ql.Read`, `eval.Read`, `ql.ErrSelectRenamed` | `session`, `cmd/sdev1-ql` | none within this record — one task |

## Notes

- ★ **`SELECT` was the one verb implying a family this store will never have.**
  There is no `INSERT`, no `UPDATE`, no `DELETE`, because the store appends —
  ADR-022 says so. A caller who reads `SELECT` expects its siblings, and the first
  thing they learn is that four of them do not exist.
- ⚠ **`SELECT` is RESERVED, not removed.** Removing it is the three-line version
  and it is worse: an unreserved `SELECT` lexes as an identifier, so
  `SELECT * FROM x` fails somewhere inside the projection with a message about
  attribute names and never mentions that the verb was the problem.
- ⚠ **It is NOT an alias.** Two spellings of one verb is two things to document,
  two to test, and a permanent question about which is canonical. The refusal is a
  migration aid; an alias is a second language.
- ★ **The Go type renames too.** A type called `Select` behind a keyword called
  `READ` is exactly the `Version`/`Generation` trap ADR-032 removed one record
  ago — a name that says one thing and means another, sitting where the next
  person will reach for it.
- **Backticks still reach it.** ADR-021 made every keyword addressable as
  `` `like this` ``, and reserving a word must not take an attribute name away.
- ⚠ **The markdown half of a rename has no compiler.** Two existing gates cover
  it: one parses every published example, one requires every export in the guide.
  The fence also greps both pages, so a stale `SELECT` outside a sentence about
  the rename fails the task.
