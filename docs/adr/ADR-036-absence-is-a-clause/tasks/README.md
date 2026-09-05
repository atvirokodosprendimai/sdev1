# ADR-036 Tasks

Implementation tasks for ADR-036: absence is a clause of its own, so "has this
and lacks that" needs no boolean algebra. See the parent ADR for the decision.

**Source of truth:** the task files' headers. This README is a derived index —
when it disagrees with a task file, the task file wins.

## Execution Order

Two tasks. T1 reserves the keyword T2 reuses.

| Order | Task | Depends-on |
|-------|------|------------|
| 1 | T1 | none |
| 2 | T2 | T1 |

## Task Index

| ID | Title | Status | Covers | Acceptance |
|----|-------|--------|--------|------------|
| T1 | WITHOUT on a read — and never requiring what it excludes | done | — | four tests plus both documentation gates, then `go test ./...` |
| T2 | WITHOUT in a shape — a third leg kind that filters and never scores | done | — | three tests plus both documentation gates, then `go test ./...` |

Status: `pending` | `partial` | `blocked` | `done`.

## Contract Coupling

| Producer | Contract | Consumer(s) | Ordering note |
|----------|----------|-------------|---------------|
| T1 | the `WITHOUT` keyword, `ql.Read.Without` | T2 | T2 reuses the keyword rather than adding a second |
| T2 | `ql.LegExcluded`, `ql.BuildRow` | a shape evaluator (`BACKLOG.md` §20) | none within this record |

## Notes

- ★ **The reason absence is a CLAUSE and not a predicate.** `WHERE` holds exactly
  one predicate by ADR-011 — no `AND`, no `OR`, no parentheses. Making absence a
  predicate would force boolean composition into the grammar just to express "has
  `a` = x and lacks `b`", which is the only form anybody actually asks. Two
  clauses conjoin by being two clauses.
- ⚠ **THE TRAP, and it is T1's whole job.** ADR-035 rule 4 drops a member missing
  any attribute the statement NAMES. A `WITHOUT` attribute is named in order to be
  absent, so the untouched drop rule makes the clause unsatisfiable — and it fails
  by returning NOTHING, which is indistinguishable from a correct answer about
  data that does not exist.
- ★ **Absence is defined by `ports.Carried` and by nothing new.** That already
  gets three histories right: never asserted, asserted then RETRACTED, and
  asserted over an interval not covering the instant. A second definition would
  drift from the first on exactly the retracted case.
- ⚠ **Absence is SNAPSHOT-RELATIVE.** "Does not have one at this instant", never
  "never had one". A caller reading it the second way is wrong about every entity
  whose attribute was retracted — and being able to ask the first is what makes
  retraction mean anything.
- ⚠ **An excluded leg binds NOTHING.** `Unbound` already means "an optional leg
  matched nothing", and reusing it would make "had no value to give" and "was
  required to have none" render identically. This narrows ADR-011's "one binding
  per leg" to "one per leg that projects" — a change to a documented invariant,
  recorded rather than absorbed.
- ⚠ **An excluded leg FILTERS and never scores.** Decided before a metric exists,
  because once there are scores to preserve the tempting version is to treat a
  carried excluded attribute as "less similar" rather than as not a candidate.
- ⚠ **Nothing evaluates a shape query yet** (`BACKLOG.md` §20). T2 decides the ROW
  rules, which `BuildRow` enforces and which are tested directly.
