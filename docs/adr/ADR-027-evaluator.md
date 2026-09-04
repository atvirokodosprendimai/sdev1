# ADR-027: A statement is evaluated against a read port, and a clause is evaluated or refused — never ignored

**Status:** Accepted
**Date:** 2026-09-04
**Owner:** M
**Spec:** None — no spec stage
**Cross-references:** `docs/adr/README.md`, `docs/adr/ADR-002-transaction-identity.md`, `docs/adr/ADR-003-transaction-boundary.md`, `docs/adr/ADR-011-query-language.md`, `docs/adr/ADR-022-write-statements.md`, `docs/adr/ADR-026-leaf-store.md`, `docs/adr/BACKLOG.md`
**Governs:** `internal/core/eval/**`
**Enforced-by:** `internal/core/eval/eval_test.go::TestAPredicateThatMatchesNothingReturnsNothing`
**Invalidates:** none — ADR-011 decided what a caller may WRITE and what it MEANS, and deferred running it to `BACKLOG.md` §20; this is the evaluation half of that entry
**Served-path change:** `WHERE` filters. Until now it parsed and was discarded, so `SELECT * FROM planet-3 WHERE mass = "999"` returned every attribute of planet-3 — and a `SELECT` now costs one entity read rather than a leaf held in memory.

## Context

`BACKLOG.md` §20 waited on storage. Storage now exists (ADR-024, ADR-025,
ADR-026), so the evaluation half of that entry is unblocked. Planning and
similarity are not, and are re-deferred below with their reasons.

★ **Writing this record found a live defect, and it is what the record is about.**
`Select.Where` has been parsed since ADR-011 and evaluated nowhere. Run today:

```
SELECT * FROM planet-3 WHERE mass = "999"
  planet-3   mass         5
  planet-3   radius       6371
```

Nothing matched `mass = "999"`. Every row came back anyway. There is no error, no
warning, and no way for the caller to tell — they asked a narrower question and
were given a wider answer that looks exactly like a correct one.

⚠ **A clause that parses and is silently discarded is worse than one that is
refused**, and worse than one that does not exist. "This is not implemented" is an
answer a caller can act on. A filtered result that was never filtered is not.

## Existing Primitives Audit

- `internal/core/ql` (ADR-011): supplies `Select`, `Predicate`, `TimeClause` and
  `TimeClause.Resolve`. **Reused whole.** ⚠ Resolution stays there: it is ADR-002
  rule 6's table and there is exactly one implementation of it.
- `internal/core/temporal` (ADR-002): supplies `Visible`. **Reused, and this is
  the constraint §20 names explicitly** — the parser's resolved qualifiers go
  straight to it and nothing is re-derived, because re-deriving is where the two
  axes get conflated again.
- `internal/core/ports` (ADR-003): supplies `Reader` and `Snapshot`.
  **Consumed** — the evaluator is handed a `Reader`, so it structurally cannot
  write. This is the first thing in the corpus for which that asymmetry does real
  work rather than being asserted.
- `internal/core/leafstore` (ADR-026): a `Reader` the evaluator can run against.
  **Not depended on** — the evaluator names the port, never the implementation, so
  the same statement runs against a leaf or against anything else that reads.
- `internal/core/session` (ADR-022): has a `SELECT` implementation today.
  **Replaced, not duplicated.** Two implementations of one projection is how the
  session becomes the specification, which §28 warned about.
- An expression library or a general predicate engine: **none.** One comparison
  against one attribute is the whole of `WHERE` in this language; a library would
  bring precedence, coercion and null semantics this record would then be stuck
  with.

## Decision

**Evaluation reads through `ports.Reader` at one resolved instant, and every
clause the parser accepts is either evaluated or refused by name.**

1. **A clause is evaluated or REFUSED. Never ignored.** ⚠ The record. A refusal
   is an answer; a silently dropped clause is a wrong answer wearing the shape of
   a right one.

2. **The evaluator is handed a `ports.Reader`.** Writing is not expressible from
   inside it — ADR-003's asymmetry, doing work at last rather than being asserted.

3. **One resolution per statement, passed through untouched.** ⚠ §20's explicit
   constraint. `TimeClause.Resolve` runs once, and its result reaches both the
   store's snapshot and `temporal.Visible` without being adjusted, re-derived or
   recomputed anywhere.

4. **A `SELECT` costs ONE read of ONE entity.** Every `SELECT` in this language
   names exactly one entity, so evaluating one must not walk a leaf. ★ This is
   what lets a statement run against a store rather than against everything held
   in memory.

5. **`WHERE` qualifies the ENTITY; the projection is applied afterwards.** ⚠ The
   order matters and the obvious order is wrong. `SELECT name FROM planet-7 WHERE
   class = 'terrestrial'` is in the published guide, so a predicate must be able
   to name an attribute the projection does not return — narrowing to the
   projected attributes first leaves nothing to test the predicate against, and
   the query silently returns nothing.

6. **A comparison is NUMERIC only when the literal was written as a number.**
   ★ It is a property of the query text, visible where it is written, rather than
   of whatever happens to be stored. ⚠ Otherwise the same query changes meaning
   when the data changes: `"10" < "9"` is true as text and false as numbers, and
   nothing in the statement says which was meant.

7. **A comparison whose operands are not comparable is a REFUSAL, not false.**
   ⚠ "This value is not a number" and "this value is not greater than five" are
   different answers, and returning the second for the first is rule 1 again one
   level down.

8. **An attribute the predicate names and the entity does not carry does not
   qualify** — no rows, no error. That is an answer about the data, not about the
   query.

9. **The latest visible datom per attribute is what a row reports, and a
   retraction suppresses the attribute rather than being reported as a value.**
   One implementation, in one place.

10. **A statement this cannot evaluate is a named refusal**, and specifically not
    an empty result — the rule ADR-022 already applies to `MATCH SHAPE`.

**What would falsify this.** A `WHERE` clause that matches nothing returning rows.
That is the falsifier in `Enforced-by:`, it is exactly the behaviour shipped
today, and it needs one entity and no cluster to check.

## Alternatives Considered

- **Leave `WHERE` unevaluated and document it as parse-only.** It is what the
  code does today and it costs nothing. Rejected under rule 1: the statement runs,
  returns rows, and reports success, so the documentation is the only thing
  standing between a caller and a wrong answer — and a caller who has run the
  query has already been told it worked.
- **Refuse `WHERE` instead of evaluating it.** Honest, and much less code.
  Rejected because it is strictly worse than evaluating it once storage exists:
  the only thing that made evaluation hard was the absence of a store, and there
  is one now. ★ Had storage still been missing, this would have been the right
  answer — the refusal is the fallback, not the loss.
- **Apply the projection first, then the predicate.** One pass and less state.
  Rejected under rule 5: it makes `SELECT name … WHERE class = …` return nothing
  on data where it should return a row, which is invisible on every query whose
  predicate happens to name a projected attribute.
- **Compare numerically whenever the stored value parses as a number.** More
  forgiving, and it does what most callers mean. Rejected under rule 6: the same
  query then changes meaning when the data changes, and nothing in the statement
  records which comparison was intended.
- **Return false for a comparison that cannot be made.** Conventional, and it
  keeps a query running over messy data. Rejected under rule 7: it hides a type
  error inside an ordinary-looking empty result, which is the defect this record
  exists to remove.
- **Let the evaluator take a clock rather than an instant.** More convenient for
  callers. Rejected under rule 3: a clock can be read twice, and two readings
  inside one statement is how a query spans two instants — the same defect ADR-023
  fixed for traversal, arriving from a different direction.
- **Keep the session's `SELECT` and add a second implementation for stores.**
  Less disruptive. Rejected in the audit: two implementations of one projection
  drift, and the one that drifts is whichever nobody is reading.

## Component / Boundary Impact

One new component, `internal/core/eval`, owning one thing: turning a parsed
statement into rows. It has one reason to change — what a statement means when it
runs.

⚠ The boundary: it does not decide what a statement may SAY (ADR-011), what a
fact is (ADR-003), what visibility means (ADR-002), or where bytes live
(ADR-026). It names `ports.Reader` and no implementation, so the same statement
runs against a leaf, against memory, or against anything that reads.

## Wiring & Contract Changes

| Surface | Change | Producer | Consumer(s) |
|---------|--------|----------|-------------|
| `eval.Select` | new — evaluate a `SELECT` against a reader | T1 | T2, callers |
| `eval.Row` | new — one attribute of one entity, as evaluated | T1 | T2, callers |
| `eval.ErrNotComparable` / `eval.ErrUnboundInstant` | new sentinels | T1 | callers |
| `temporal.Query.Bounds` | new — a resolved query as the two values a snapshot takes | T1 | T1 |
| `session.Session.selectFrom` | rewritten to call `eval.Select` | T2 | — |
| `ports.Reader` over the session's own datoms | new, unexported | T2 | — |

## Inter-task Contracts

| Contract | Producing task | Consuming task(s) | Breaking? |
|----------|----------------|-------------------|-----------|
| `eval.Select`, `eval.Row` | T1 | T2 | T1 before T2 |

## Consequences

- **Positive:** `WHERE` filters. The defect above is gone, and a test fails if it
  comes back.
- **Positive:** A `SELECT` costs one entity read, so it runs against a leaf
  without loading it. That is the first statement in this system that does not
  need everything in memory.
- **Positive:** There is one projection implementation, so the session cannot
  drift from the engine.
- **Negative:** `WHERE` on an attribute the projection excludes now requires the
  entity's whole attribute set, so the evaluator reads more than it returns. That
  is what rule 5 costs, and it is bounded by one entity.
- **Negative:** A comparison against a value that will not parse is now an ERROR
  where it was previously an unfiltered row. Callers relying on the old behaviour
  were relying on a defect, but they will notice.
- **Neutral:** No planner, no index selection, no similarity. Those are re-deferred
  below with their reasons rather than guessed at.

## Out of Scope

- Query planning, index selection and term ordering (deferred: `docs/adr/BACKLOG.md` §15)
- A similarity metric and threshold for `MATCH SHAPE` (deferred: `docs/adr/BACKLOG.md` §20)
- Enumerating entities from the language, without a name (deferred: `docs/adr/BACKLOG.md` §20)
- Multi-hop traversal syntax (deferred: `docs/adr/BACKLOG.md` §20)
- `AND` / `OR` / parentheses in a predicate (deferred: `docs/adr/BACKLOG.md` §20)
- Evaluating `SEARCH` and `TRAVERSE` through a reader (deferred: `docs/adr/BACKLOG.md` §20)
- What a statement may say (permanent: boundary: ADR-011 owns the language, and an evaluator that extended it would be deciding syntax by implementing it)
- Resolving the two-axis defaults (permanent: boundary: ADR-002 rule 6's table has exactly one implementation, in `ql.TimeClause.Resolve`, and re-deriving bounds is the specific defect §20 names)
- Choosing a similarity metric now (permanent: fact: a metric picked against no corpus is a number nobody has reason to believe, and this repository has no corpus; citation: file `docs/adr/BACKLOG.md:475`)

## Risks

| Risk | Likelihood | Impact | Mitigation |
|------|------------|--------|------------|
| A clause is added to the grammar and left unevaluated | High — it is exactly what happened to `WHERE` | Critical — a wrong answer that looks correct, with no error anywhere | Rule 1, and the falsifier is the shipped defect stated as a test |
| The projection is applied before the predicate | High — it is the natural single pass | High — a documented query silently returns nothing | Rule 5, with a test that filters on an unprojected attribute |
| The evaluator re-derives a bound instead of passing the resolved one | Med | Critical — the two time axes conflate again, the defect ADR-002 was written against | Rule 3; the evaluator takes an INSTANT, not a clock, so it cannot resolve twice |
| A comparison silently returns false on a type mismatch | Med — it is the conventional behaviour | High — a type error hides inside an empty result | Rule 7, with a named sentinel |
| The evaluator walks a leaf to answer one `SELECT` | Med | High — every statement then needs everything in memory, and nothing says so | Rule 4, with a test counting reads |

## Rollback

The evaluator is new and nothing depends on it until T2 wires the session to it.
Reverting T2 restores the previous behaviour — which is the defect, so a rollback
is a decision to ship a `WHERE` that does not filter, and should be recorded as
one rather than done quietly.

## Follow-ups

- [ ] When enumeration lands (`BACKLOG.md` §20), confirm `SEARCH` and `TRAVERSE` move onto the reader too — they answer from session state today, and the split is invisible until a statement disagrees with itself.
- [ ] When an index exists (`BACKLOG.md` §15), revisit rule 4: one read per `SELECT` is right for a named entity and says nothing about a statement that has to find its entities.
- [ ] Re-read rule 6 against a real corpus before the language grows types. Deciding numeric-ness from the query text is right while values are opaque bytes, and a typed value would make it a different question.
