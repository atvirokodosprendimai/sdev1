# Task ADR-011-T1: The grammar, the parse, and a time clause that implements ADR-002's table

**Depends-on:** none
**Covers:** none — no spec
**Estimated scope:** L (cross-boundary)
**Owner:** unassigned
**Produces:** `ql.Token`, `ql.Lexer`, `ql.Statement`, `ql.Select`, `ql.TimeClause`, `ql.TimeClause.Resolve`, `ql.Parse`, `ql.ParseError`
**Consumes:** `temporal.Query` and `temporal.ResolveQualifiers` from ADR-002, `tx.TxID` from ADR-002
**Data dependency:** hermetic
**Proof map:** v1
**Rests-on:** `a lone instant binding valid time and leaving transaction time open`, `the defaults table having exactly one implementation`, `a parse error naming its position and what was expected`

## Goal

Let a caller write one statement with an optional time clause, and make that
clause mean exactly what ADR-002 says it means.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `internal/core/ql/doc.go` | add | Package comment: why time is a clause rather than a verb family, the defaults table, and what a lone instant binds. |
| `internal/core/ql/lex.go` | add | `Token`, `Lexer`, and positions. |
| `internal/core/ql/ast.go` | add | `Statement`, `Select`, `TimeClause`, and `Resolve`. |
| `internal/core/ql/parse.go` | add | `Parse` and `ParseError`. |
| `internal/core/ql/ql_test.go` | add | The lexer and parser tests below. |
| `internal/core/ql/temporal_test.go` | add | The defaults-table falsifier and the single-implementation guard. |

★ `TimeClause.Resolve` calls `temporal.ResolveQualifiers`. It does not branch on
the four cases itself, and that is the point: two implementations of one table
drift, and the drift is invisible until a query returns the wrong history.

## Ordered Steps

1. [S1] Write the failing tests first (TDD red): `TestTimeClauseImplementsTheDefaultsTable`, `TestLoneInstantBindsValidTimeOnly`, `TestPackageComputesNoDefaultsOfItsOwn`, `TestParseErrorNamesPositionAndExpectation`, `TestLexerSpansAndPositions`, `TestSelectRoundTripsThroughTheAST`. Run the Acceptance fence and confirm it is red. ⚠Check each name is SELECTED by the fence's `-run` filter before running any mutant. [proof: acceptance]
2. [S2] Implement the lexer: identifiers, numbers, strings, punctuation, keywords, each carrying a byte offset. ★Positions are part of the contract, because for a public surface the error message is most of the usability.
3. [S3] Define `TimeClause` holding the qualifiers AS WRITTEN — an optional instant and an optional transaction — before any defaults are applied.
4. [S4] Implement `Resolve` by calling `temporal.ResolveQualifiers`, adding nothing. ★A lone instant binds VALID time and leaves transaction time open. This is the rule a reasonable implementer gets wrong, and a real project shipped the wrong version.
5. [S5] Implement `Parse` for a `SELECT` term with `WHERE`, `AS OF` and `TRANSACTION` clauses, producing a `Select`.
6. [S6] Make the time clause attach to a statement, and leave room for it to attach to a leg. ★Attaching per leg is why the clause form was chosen over a verb family; under a verb family it would need a second grammar.
7. [S7] Make every parse failure a `ParseError` carrying the position, what was found and what was expected. [proof: mutation]

## Acceptance

```bash
set -o pipefail
go test ./internal/core/ql/... -race -run 'TestTimeClauseImplements|TestLoneInstantBinds|TestPackageComputesNoDefaults|TestGuardFlagsABranching|TestResolveMatchesTheTemporal|TestParseErrorNames|TestLexerSpans|TestSelectRoundTrips' -count=1 2>&1 | tee /tmp/adr011-t1a.out \
  && ! grep -qE "no tests to run|matched no packages|^FAIL|^--- FAIL|DATA RACE" /tmp/adr011-t1a.out \
  && go test ./internal/core/ql/... ./internal/core/temporal/... -race -count=1 2>&1 | tee /tmp/adr011-t1b.out \
  && ! grep -qE "no tests to run|matched no packages|^FAIL|^--- FAIL|DATA RACE" /tmp/adr011-t1b.out
```

The first command is this task's own work. The second is the regression half over
ADR-002's temporal package, which this task must agree with rather than
reimplement — so a change there that broke agreement would show up here.

## Tests

| Test name | File | Verifies | Covers | Steps |
|-----------|------|----------|--------|-------|
| `TestTimeClauseImplementsTheDefaultsTable` | `internal/core/ql/temporal_test.go` | All four rows of ADR-002's table, driven from the table rather than from branching prose. **The falsifier ADR-011 names in `Enforced-by:`** | — | S3, S4 |
| `TestLoneInstantBindsValidTimeOnly` | `internal/core/ql/temporal_test.go` | `AS OF t` alone leaves the transaction qualifier OPEN, so a backdated write is visible at the instant it was backdated to — the defect a predecessor project shipped | — | S4 |
| `TestPackageComputesNoDefaultsOfItsOwn` | `internal/core/ql/temporal_test.go` | A source-level check finds no second implementation of the table in this package, so it cannot drift from ADR-002 | — | S4 |
| `TestGuardFlagsABranchingResolve` | `internal/core/ql/temporal_test.go` | The positive control: the guard flags a `Resolve` that plainly branches. Without it the guard above passes whether it works or has stopped looking, and the two are identical from outside. ⚠It is INSIDE the acceptance fence deliberately — a control that sits outside the command it validates proves nothing about that command | — | S4 |
| `TestResolveMatchesTheTemporalPackageDirectly` | `internal/core/ql/temporal_test.go` | Forwarding is behavioural as well as structural: the same clause resolves to the same answer the temporal package gives, across all four shapes | — | S4 |
| `TestParseErrorNamesPositionAndExpectation` | `internal/core/ql/ql_test.go` | Every parse failure carries a byte position, what was found and what was expected — not a bare "syntax error" | — | S7 |
| `TestLexerSpansAndPositions` | `internal/core/ql/ql_test.go` | Tokens carry accurate positions across whitespace, strings and numbers, so an error can point at the right place | — | S2 |
| `TestSelectRoundTripsThroughTheAST` | `internal/core/ql/ql_test.go` | A statement parses to the AST a caller expects, including with each of the four time-clause shapes attached | — | S5, S6 |

## Reachability

| Rung | How this task shows it |
|------|------------------------|
| 1 — exists | The six tests above. |
| 2 — something selects it | `Parse` is the only way text becomes an AST, and `Resolve` the only way a clause becomes qualifiers; the guard fails if a second defaults implementation appears. |
| 3 — the caller can discover it | `ParseError` carries position and expectation, which for a public contract is part of the surface rather than a diagnostic. |
| 4 — it is used | Nothing evaluates a query yet; the parse is the measurement. |

## Mutation Log

- 2026-09-04 · 4016ba8* · mutant killed · exit 1 · `internal/core/ql/parse.go` · binds a lone instant to BOTH time axes, which is what a reasonable implementer writes and what a predecessor project shipped: a write committed now and valid from the past is then excluded by its own commit time, so a query at the instant it was backdated to returns nothing · acceptance-sha256:e983867be52a1ef1d6839abe99e5ec6cbeee9f440f307433636785eebfab03f2 · covers:a lone instant binding valid time and leaving transaction time open
- 2026-09-04 · 4016ba8* · mutant killed · exit 1 · `internal/core/ql/ast.go` · reimplements the defaults table here instead of forwarding to the package that owns it — behaviourally identical today and free to drift tomorrow, with the drift invisible until a query returns the wrong history · acceptance-sha256:e983867be52a1ef1d6839abe99e5ec6cbeee9f440f307433636785eebfab03f2 · covers:the defaults table having exactly one implementation
- 2026-09-04 · 4016ba8* · mutant killed · exit 1 · `internal/core/ql/parse.go` · drops what was expected from every parse error, reducing a public contract to the bare syntax-error message that tells a caller nothing about what to write instead · acceptance-sha256:e983867be52a1ef1d6839abe99e5ec6cbeee9f440f307433636785eebfab03f2 · covers:a parse error naming its position and what was expected

## Invariants

- A lone instant binds valid time and leaves transaction time open.
- The defaults table has exactly one implementation, in `internal/core/temporal`.
- Every parse failure is a `ParseError` with a position.
- Parsing produces an AST and evaluates nothing.

## Risks

- ⚠ **A defaults test that checks only the "nothing" and "both" rows would pass for an implementation that binds a lone instant to both axes.** The falsifier drives all FOUR rows, and the lone-instant row is the one that matters — it is the case a real project got wrong with roughly 140 green tests, because every test happened to write with `valid_from == tx_time` and the two axes were never actually different.
- A source-level guard for "no second implementation" can pass by having stopped looking. It asserts it found the resolver call it expects, so a rename empties it loudly.

## Stop Condition

Stop and ask before adding a convenience that binds both time axes from one
value — a `AT <t>` shorthand, say. It is the exact defect ADR-002 exists to
prevent, and it is what a caller will ask for.

## Out of Scope

- Shape matching and the storage-policy clause — that is T2.
- Evaluating anything (deferred: `docs/adr/BACKLOG.md` §20)
- Query planning (deferred: `docs/adr/BACKLOG.md` §20)

## Verification Log
- 2026-09-04 · 4016ba8* · exit 1 · `set -o pipefail …` · acceptance-sha256:e983867be52a1ef1d6839abe99e5ec6cbeee9f440f307433636785eebfab03f2 · ms:3429
  ```
  --- last 8 line(s) of stdout
  ok  	github.com/atvirokodosprendimai/sdev1/internal/core/ql	1.032s
  ok  	github.com/atvirokodosprendimai/sdev1/internal/core/ql	1.029s
  --- FAIL: TestVisibleIsTheOnlyComparisonSite (0.01s)
      temporal_test.go:214: these files outside internal/core/temporal name BOTH time axes: [../../../internal/core/ql/ast.go ../../../internal/core/ql/doc.go ../../../internal/core/ql/parse.go ../../../internal/core/ql/ql_test.go ../../../internal/core/ql/temporal_test.go]
          the two axes are compared in exactly one place, so that a caller passing one instant into both parameters is reviewable in one file
  FAIL
  FAIL	github.com/atvirokodosprendimai/sdev1/internal/core/temporal	0.049s
  FAIL
  ```
- 2026-09-04 · 4016ba8* · exit 1 · `set -o pipefail …` · acceptance-sha256:e983867be52a1ef1d6839abe99e5ec6cbeee9f440f307433636785eebfab03f2 · ms:3526
  ```
  --- last 8 line(s) of stdout
  ok  	github.com/atvirokodosprendimai/sdev1/internal/core/ql	1.033s
  ok  	github.com/atvirokodosprendimai/sdev1/internal/core/ql	1.029s
  --- FAIL: TestVisibleIsTheOnlyComparisonSite (0.01s)
      temporal_test.go:214: these files outside internal/core/temporal name BOTH time axes: [../../../internal/core/ql/ast.go ../../../internal/core/ql/doc.go ../../../internal/core/ql/parse.go ../../../internal/core/ql/ql_test.go ../../../internal/core/ql/temporal_test.go]
          the two axes are compared in exactly one place, so that a caller passing one instant into both parameters is reviewable in one file
  FAIL
  FAIL	github.com/atvirokodosprendimai/sdev1/internal/core/temporal	0.050s
  FAIL
  ```
- 2026-09-04 · 4016ba8* · exit 1 · `set -o pipefail …` · acceptance-sha256:e983867be52a1ef1d6839abe99e5ec6cbeee9f440f307433636785eebfab03f2 · ms:3368
  ```
  --- last 8 line(s) of stdout
  ok  	github.com/atvirokodosprendimai/sdev1/internal/core/ql	1.032s
  ok  	github.com/atvirokodosprendimai/sdev1/internal/core/ql	1.029s
  --- FAIL: TestVisibleIsTheOnlyComparisonSite (0.01s)
      temporal_test.go:214: these files outside internal/core/temporal name BOTH time axes: [../../../internal/core/ql/ast.go ../../../internal/core/ql/doc.go ../../../internal/core/ql/parse.go ../../../internal/core/ql/ql_test.go ../../../internal/core/ql/temporal_test.go]
          the two axes are compared in exactly one place, so that a caller passing one instant into both parameters is reviewable in one file
  FAIL
  FAIL	github.com/atvirokodosprendimai/sdev1/internal/core/temporal	0.049s
  FAIL
  ```
- 2026-09-04 · 4016ba8* · exit 1 · `set -o pipefail …` · acceptance-sha256:e983867be52a1ef1d6839abe99e5ec6cbeee9f440f307433636785eebfab03f2 · ms:3227
  ```
  --- last 8 line(s) of stdout
  ok  	github.com/atvirokodosprendimai/sdev1/internal/core/ql	1.033s
  ok  	github.com/atvirokodosprendimai/sdev1/internal/core/ql	1.030s
  --- FAIL: TestVisibleIsTheOnlyComparisonSite (0.01s)
      temporal_test.go:214: these files outside internal/core/temporal name BOTH time axes: [../../../internal/core/ql/ast.go ../../../internal/core/ql/doc.go ../../../internal/core/ql/parse.go ../../../internal/core/ql/ql_test.go ../../../internal/core/ql/temporal_test.go]
          the two axes are compared in exactly one place, so that a caller passing one instant into both parameters is reviewable in one file
  FAIL
  FAIL	github.com/atvirokodosprendimai/sdev1/internal/core/temporal	0.050s
  FAIL
  ```
- 2026-09-04 · 4016ba8* · exit 0 · `set -o pipefail …` · acceptance-sha256:e983867be52a1ef1d6839abe99e5ec6cbeee9f440f307433636785eebfab03f2 · ms:3489
- 2026-09-04 · 4016ba8* · exit 0 · `set -o pipefail …` · acceptance-sha256:e983867be52a1ef1d6839abe99e5ec6cbeee9f440f307433636785eebfab03f2 · ms:3420
- 2026-09-04 · 4016ba8* · exit 0 · `set -o pipefail …` · acceptance-sha256:e983867be52a1ef1d6839abe99e5ec6cbeee9f440f307433636785eebfab03f2 · ms:3466
- 2026-09-04 · 4016ba8* · exit 0 · `set -o pipefail …` · acceptance-sha256:e983867be52a1ef1d6839abe99e5ec6cbeee9f440f307433636785eebfab03f2 · ms:3522
- 2026-09-04 · 4016ba8* · exit 0 · `set -o pipefail …` · acceptance-sha256:e983867be52a1ef1d6839abe99e5ec6cbeee9f440f307433636785eebfab03f2 · ms:3534
