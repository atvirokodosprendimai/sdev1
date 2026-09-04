# ADR-011 Tasks

Implementation tasks for ADR-011: One query language where time is a clause, not
a family of verbs. See the parent ADR for the decision.

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
| T1 | The grammar, the parse, and a time clause that implements ADR-002's table | done | — | `go test ./internal/core/ql/... -race -run 'TestTimeClauseImplements\|TestLoneInstantBinds\|TestPackageComputesNoDefaults\|TestParseErrorNames\|TestLexerSpans\|TestSelectRoundTrips'` then the temporal suite |
| T2 | Shape matching where an optional leg binds nothing, and a policy clause for new data only | done | — | `go test ./internal/core/ql/... -race -run 'TestOptionalLeg\|TestShapeQueryRequires\|TestRequiredLeg\|TestPolicyClause'` then the temporal and segment suites |

Status: `pending` | `partial` | `blocked` | `done`.

⚠ **This record covers the LANGUAGE, not the evaluator.** There is no storage
engine, so a parse and the resolution of its time qualifiers are decidable now
and running a query is not. `BACKLOG.md` §20 carries evaluation and planning.

## Contract Coupling

| Producer | Contract | Consumer(s) | Ordering note |
|----------|----------|-------------|---------------|
| T1 | `ql.Lexer`, `ql.Statement`, `ql.TimeClause`, `ql.Parse` | T2 | T1 before T2 |

## Notes

- ⚠ **The defaults table has exactly one implementation, and it is ADR-002's.**
  `TimeClause.Resolve` calls `temporal.ResolveQualifiers` and branches on nothing
  itself. Two implementations of one table drift, and the drift is invisible
  until a query returns the wrong history.
- ⚠ **A lone instant binds VALID time and leaves transaction time OPEN.** This is
  the rule a reasonable implementer gets wrong. A predecessor project in this
  workspace passed one instant to both parameters, so a backdated write became
  invisible at the instant it was backdated to — and roughly 140 tests including
  `-race` stayed green, because every test happened to write with
  `valid_from == tx_time` and the two axes were never actually different in any
  of them. A defaults test that checks only the "nothing" and "both" rows would
  pass for that implementation.
- **Time is a clause because a verb family doubles.** `SELECT` and `SELECT AS
  OF`, `MATCH` and `MATCH AS OF` — every new statement doubles the list, and a
  per-leg qualifier would need a second grammar. As a clause it composes, and the
  per-leg form falls out.
- ⚠ **An optional leg that matches nothing yields an UNBOUND value and the row is
  RETURNED.** Dropping it makes `OPTIONAL` a synonym for `REQUIRE`, and that bug
  is undetectable on any dataset where the leg happens always to be present. The
  test for it needs its control: a REQUIRED leg that does drop the row, because
  "optional does not drop" says nothing if nothing ever drops.
- ⚠ **`SIMILARITY` gets no default threshold.** A default makes every unqualified
  shape query reproducible only by whoever knows the default, and the value is
  then a constant nobody wrote down.
- **A policy clause governs NEW writes only.** Every block records how it was
  written, so changing the codec reinterprets nothing — and the language
  deliberately has no syntax for re-encoding what exists.
- **A parse error is part of the public contract.** Position, what was found and
  what was expected. A parser that says "syntax error" has failed at its actual
  job.
