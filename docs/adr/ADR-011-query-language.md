# ADR-011: One query language where time is a clause, not a family of verbs

**Status:** Accepted
**Date:** 2026-09-04
**Owner:** M
**Spec:** None — no spec stage
**Cross-references:** `docs/adr/README.md`, `docs/adr/ADR-002-transaction-identity.md`, `docs/adr/ADR-003-transaction-boundary.md`, `docs/adr/ADR-005-segment-format.md`, `docs/adr/BACKLOG.md`
**Governs:** `internal/core/ql/**`
**Enforced-by:** `internal/core/ql/temporal_test.go::TestTimeClauseImplementsTheDefaultsTable`
**Invalidates:** none — checked; ADR-002 fixed the meaning of the two time axes and explicitly deferred the surface syntax to this record
**Served-path change:** A caller writes one statement with an optional time clause instead of choosing between a present-tense verb and a historical one, and a shape query returns rows whose optional legs are unbound rather than missing.

## Context

This is the public contract. Everything else in the corpus is reachable only
through it, and the surface a caller learns is the hardest thing to change later
— harder than a storage format, because a format has a version field and a
language has users.

Three things force decisions now.

**Time could be a family of verbs or a clause.** `SELECT` and `SELECT AS OF`,
`GRAPH` and `GRAPH AS OF`, `MATCH` and `MATCH AS OF` — the verb family doubles
with every new statement and makes a per-leg time qualifier impossible without
doubling again. A clause composes with every term instead, which keeps a
combined language small and makes "this leg as of last year, that one now" fall
out rather than being designed.

**ADR-002 already decided what time MEANS**, and got there by watching a
predecessor project get it wrong: it passed one instant to both visibility
parameters, so a backdated write became invisible to a query at the instant it
was backdated to. ADR-002's rule 5 says a lone instant binds VALID time and
leaves transaction time open, with a four-row defaults table. ⚠ This record's job
is to implement that table EXACTLY — not approximately, and not with a
convenience that quietly binds both.

**"Find things like this" needs a stated metric and a stated threshold**, or it
is not a query at all. A shape query with required and optional legs is a real
requirement here, and the trap is the optional leg: if a leg that matches nothing
drops the row, OPTIONAL means the same as REQUIRE, and the difference only shows
up on data where the leg is sometimes absent — which is not the data anyone
tests with.

⚠ **Nothing evaluates a query yet, and this record does not pretend otherwise.**
There is no storage engine, so the language, its parse and the resolution of its
time qualifiers are decidable now; running one is not.

## Existing Primitives Audit

- `internal/core/temporal` (ADR-002, T3): supplies `Query`, `ResolveQualifiers`
  and `Visible`. **Reused whole.** The parser produces a `temporal.Query` and
  resolution goes through the existing function, so there is exactly one
  implementation of the defaults table and this record cannot drift from ADR-002
  — which is the failure mode the predecessor project demonstrated.
- `internal/core/tx` (ADR-002): supplies `TxID`. **Reused** as the transaction
  qualifier's type, so `TRANSACTION u` is the same thing the storage layer orders
  by rather than a parallel notion.
- `internal/core/segment` (ADR-005): supplies `CodecID` and the per-block record
  of how a block was written. **Reused**: a storage-policy clause names a codec
  the format already knows, and sets it for new data only.
- A parser generator: **none.** The grammar is small and hand-written recursive
  descent keeps the error messages good, which for a public contract is most of
  the usability.

## Decision

**One language. Time is a clause. An optional leg binds nothing rather than
dropping a row.**

1. **A single statement grammar with optional clauses**, not a verb per
   combination. `SELECT`, graph traversal and shape matching are terms; `AS OF`,
   `TRANSACTION` and the storage-policy clause attach to them.

2. **The time clause implements ADR-002's table exactly**, by calling ADR-002's
   own resolver rather than reimplementing it:

   | the caller wrote | `AsOf` | `ValidAt` |
   |------------------|--------|-----------|
   | nothing | open | now |
   | `AS OF t` | open | `t` |
   | `AS OF t TRANSACTION u` | `u` | `t` |
   | `TRANSACTION u` | `u` | now |

   ★A lone instant binds VALID time and leaves transaction time open. This is the
   rule a reasonable implementer gets wrong, and the reason it is a rule rather
   than a default.

3. **A time clause may attach to a whole statement or to a single leg.** That
   falls out of time being a clause, and it is why the clause form was chosen —
   under a verb family it would need a second grammar.

4. **A shape query names required legs, optional legs, a metric and a
   threshold.** Similarity without a stated metric is not a query, and a
   threshold that is not written down is one nobody can reproduce.

5. **An optional leg that matches nothing yields an UNBOUND value in the row.**
   ⚠ The row is returned. Dropping it would make `OPTIONAL` a synonym for
   `REQUIRE`, and that bug is invisible on any dataset where the leg happens
   always to be present.

6. **A storage-policy clause sets the policy for NEW data only.** `WITH
   COMPRESSION zstd` changes what the next write produces and reinterprets
   nothing already written, because every block records how it was written. The
   language cannot express "re-encode what exists", and that is deliberate.

7. **Parsing produces an AST and nothing else.** No evaluation, no planning, no
   optimisation. Those need a storage engine and this record does not pretend to
   have one.

8. **A parse error names the position and what was expected.** For a public
   contract the error message is part of the contract, and a parser that says
   "syntax error" has failed at its actual job.

**What would falsify this.** A parsed query whose resolved qualifiers differ from
ADR-002's table in any row. That is the falsifier named in `Enforced-by:`, it is
table-driven against the four rows, and it is checkable today with no storage —
which is the whole reason the table was written as a table.

## Alternatives Considered

- **A verb family: `SELECT` and `SELECT AS OF` as separate statements.** More
  familiar to anyone who has used a flashback syntax. Rejected under rule 1: the
  family doubles with every new statement, and a per-leg qualifier would need a
  second grammar rather than falling out of the first.
- **Two languages — one relational, one temporal.** Each stays simpler. Rejected:
  every real question crosses them, so a caller would have to choose a language
  before knowing which they needed, and the two would drift.
- **Bind a lone instant to both axes.** What the predecessor project did, and what
  a reasonable implementer writes. Rejected because it is the measured defect
  ADR-002 exists to prevent: a backdated write becomes invisible at the instant it
  was backdated to.
- **Re-implement the defaults table in the parser.** Fewer indirections, and the
  parser is where the syntax lives. Rejected: two implementations of one table
  drift, and the drift is invisible until a query returns the wrong history. The
  parser calls ADR-002's resolver.
- **Drop rows whose optional legs are unbound.** Simpler result shape, no null
  handling. Rejected under rule 5: it makes `OPTIONAL` mean `REQUIRE`, and the
  difference is undetectable on data where the leg is always present.
- **Similarity as an unspecified "closeness".** What most shape queries ship.
  Rejected: a metric nobody stated is a threshold nobody can reproduce, and a
  result nobody can check.
- **A parser generator.** Less code, a formal grammar. Rejected for now: the
  grammar is small, and hand-written recursive descent gives the error messages
  that rule 8 makes part of the contract.

## Component / Boundary Impact

One new component, `internal/core/ql`, owning the grammar, the parse and the AST.
It has one reason to change: what a caller may write.

⚠ The boundary: it decides what a query MEANS syntactically. It does not decide
what time means — ADR-002 does, and this record calls its resolver rather than
restating it. It does not evaluate anything, plan anything, or touch storage.

## Wiring & Contract Changes

| Surface | Change | Producer | Consumer(s) |
|---------|--------|----------|-------------|
| `ql.Token` / `ql.Lexer` | new — the lexical surface | T1 | T1 |
| `ql.Statement` / `ql.Select` | new — the AST | T1 | T2, future evaluator |
| `ql.TimeClause` | new — the qualifiers as written, before defaults | T1 | T2 |
| `ql.TimeClause.Resolve` | new — calls `temporal.ResolveQualifiers`; there is no second table | T1 | callers |
| `ql.Parse` | new — text to AST, with positioned errors | T1 | callers |
| `ql.ParseError` | new — position, what was found, what was expected | T1 | callers |
| `ql.ShapeQuery` | new — required legs, optional legs, metric, threshold | T2 | future evaluator |
| `ql.Binding` | new — a bound or UNBOUND value in a result row | T2 | future evaluator |
| `ql.PolicyClause` | new — `WITH COMPRESSION <codec>`, for new data only | T2 | writers |
| `internal/core/temporal` | consumed unchanged | — | — |

## Inter-task Contracts

| Contract | Producing task | Consuming task(s) | Breaking? |
|----------|----------------|-------------------|-----------|
| `ql.Lexer`, `ql.Statement`, `ql.TimeClause`, `ql.Parse` | T1 | T2 | No — T2 extends the grammar T1 defines |
| `ql.ShapeQuery`, `ql.Binding`, `ql.PolicyClause` | T2 | none yet | No |

## Implementation

Two tasks, sequential. See `docs/adr/ADR-011-query-language/tasks/README.md`.

## Consequences

- **Positive:** One surface to learn, and a time qualifier that composes with
  everything rather than doubling the statement list.
- **Positive:** The defaults table has exactly one implementation, so this record
  cannot drift from ADR-002 — which is the specific failure the predecessor
  project shipped.
- **Positive:** A per-leg time qualifier is free rather than a feature, because
  time was made a clause.
- **Negative:** An unbound value means every consumer must handle it. That cost
  is real and is paid to keep `OPTIONAL` meaning something.
- **Negative:** Hand-written recursive descent is more code than a generated
  parser and has to be kept in step with the grammar by hand.
- **Neutral:** Nothing runs a query. The language is decidable and the evaluator
  is not, and the task status says which is which.

## Out of Scope

- Evaluating a query against stored data (deferred: `docs/adr/BACKLOG.md` §20)
- Query planning, index selection and cost estimation (deferred: `docs/adr/BACKLOG.md` §20)
- The graph traversal operator's own syntax beyond a single hop (deferred: `docs/adr/BACKLOG.md` §20)
- Re-encoding existing data when a storage-policy clause changes (permanent: boundary: ADR-005 and ADR-006 both record how each block was written, so a policy clause sets what the NEXT write produces; re-encoding is `BACKLOG.md` §14)
- What time MEANS (permanent: boundary: ADR-002 owns the two axes and the defaults table; this record calls its resolver, and a second implementation would drift invisibly)
- Authorization of a query (deferred: `docs/adr/BACKLOG.md` §11, including the trap that a query `AS OF` a past instant must be authorized by TODAY's grants)

## Risks

| Risk | Likelihood | Impact | Mitigation |
|------|------------|--------|------------|
| The parser binds a lone instant to both time axes | High — it is what a reasonable implementer writes, and a real project shipped it | Critical — backdated writes become invisible and no ordinary test sees it | The parser calls ADR-002's resolver rather than reimplementing; the falsifier is table-driven against all four rows |
| An optional leg drops its row, making `OPTIONAL` mean `REQUIRE` | Med | High — invisible on any dataset where the leg is always present | The falsifier uses a shape where the optional leg matches NOTHING, and asserts the row is returned with an unbound value |
| A second implementation of the defaults table appears later, for convenience | Med | Critical — the two drift and the drift is invisible until a query returns the wrong history | A source-level check asserts the package computes no defaults of its own |
| Similarity ships without a stated threshold | Med | Med — results nobody can reproduce | The AST requires a metric and a threshold; a shape query without both is a parse error |

## Rollback

No persistent state, so a revert is a code revert. The expensive part is the
opposite: once callers write statements in this syntax, changing the surface
breaks them in a way no format version helps with. That asymmetry is why the
grammar's shape — a clause rather than a verb family — is decided now rather than
grown.

## Follow-ups

- [ ] When an evaluator exists (`BACKLOG.md` §20), confirm it passes the parser's resolved qualifiers straight to `temporal.Visible` without re-deriving anything; re-deriving is where the two axes get conflated again.
- [ ] When authorization lands (`BACKLOG.md` §11), confirm a query `AS OF` a past instant is authorized against TODAY's grants and never that instant's — the symmetry is tempting and it is a leak.
- [ ] When the graph operator grows past one hop, re-check that the time clause still attaches per leg; that property is the reason the clause form was chosen and it should not be quietly lost.
