# ADR-027 Tasks

Implementation tasks for ADR-027: a statement is evaluated against a read port,
and a clause is evaluated or refused — never ignored. See the parent ADR for the
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
| T1 | Evaluate a SELECT against a reader | done | — | `go test ./internal/core/eval/... -race -run '…nine tests…'`, then `temporal`, then the suites it composes |
| T2 | Delete the session's own SELECT | done | — | `go test ./internal/core/session/... -race -run '…three tests…'`, then RUN `cmd/sdev1-ql` three times and grep each answer |

Status: `pending` | `partial` | `blocked` | `done`.

## Contract Coupling

| Producer | Contract | Consumer(s) | Ordering note |
|----------|----------|-------------|---------------|
| T1 | `eval.Select`, `eval.Row` | T2 | T1 before T2 |

## Notes

- ★ **This record began with a live defect.** `Select.Where` has been parsed since
  ADR-011 and evaluated nowhere. `SELECT * FROM planet-3 WHERE mass = "999"`
  returned every attribute of planet-3 — no error, no warning, and no way for the
  caller to tell that the question they asked was not the question answered.
- ⚠ **A clause that parses and is silently discarded is worse than one that is
  refused, and worse than one that does not exist.** "Not implemented" is an
  answer a caller can act on. A filtered result that was never filtered is not.
- ⚠ **The order of predicate and projection is the subtle half.** The published
  guide has `SELECT name FROM planet-7 WHERE class = 'terrestrial'`, so a
  predicate must be able to name an attribute the projection does not return.
  Narrowing first leaves nothing to test the predicate against, and the query
  silently returns nothing — invisible on every query whose predicate happens to
  name a projected attribute.
- ★ **A comparison is numeric only when the LITERAL was written as a number.** It
  is a property of the query text, readable where it was written. ⚠ Deciding it
  from the stored value instead makes the same statement change meaning when the
  data changes: `"10" < "9"` is true as text and false as numbers.
- ⚠ **A comparison that cannot be made is refused, not answered false.** "This is
  not a number" and "this is not greater than five" are different answers, and
  returning the second for the first is the same defect one level down.
- ★ **The evaluator takes an INSTANT, not a clock.** Reading a clock twice inside
  one statement is then not expressible — the defect ADR-023 fixed for traversal,
  arriving from a different direction.
- **A `SELECT` costs ONE read of ONE entity**, which is what lets a statement run
  against a leaf instead of against everything held in memory.
- What §20 still defers, with its reasons: PLANNING, because there is no index
  (§15) and an execution strategy guessed against no storage layer is a guess;
  SIMILARITY, because a metric chosen against no corpus is a number nobody has
  reason to believe; ENUMERATION and multi-hop syntax, which need a planner and a
  language decision respectively.
