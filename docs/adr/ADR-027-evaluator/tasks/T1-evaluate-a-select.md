# Task ADR-027-T1: Evaluate a SELECT against a reader

**Depends-on:** none
**Covers:** none — no spec
**Estimated scope:** M
**Owner:** unassigned
**Produces:** `eval.Select`, `eval.Row`, `eval.ErrNotComparable`, `eval.ErrUnboundInstant`, `temporal.Query.Bounds`
**Consumes:** `ql.Select`, `ql.Predicate`, `ql.TimeClause.Resolve` from ADR-011; `temporal.Visible` from ADR-002; `ports.Reader`, `ports.Snapshot`, `ports.Datom` from ADR-003
**Data dependency:** hermetic — a fake reader the test controls, plus a real `leafstore` in a temporary directory
**Proof map:** v1
**Rests-on:** `a WHERE clause that actually filters`, `a predicate evaluated before the projection narrows the attributes`, `a SELECT costing one entity read rather than a leaf`, `a comparison that cannot be made being refused rather than answered false`, `one resolution per statement, reaching the store and the filter unchanged`

## Goal

Make a parsed `SELECT` produce rows, so that every clause a caller may write
either changes the answer or says why it did not.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `internal/core/eval/doc.go` | add | Why a clause is never ignored, and why the evaluator takes an instant. |
| `internal/core/eval/eval.go` | add | `Select`, `Row`, the predicate, the sentinels. |
| `internal/core/eval/eval_test.go` | add | The tests below. |
| `internal/core/temporal/qualifiers.go` | modify | `Bounds` — a resolved query as the two values a snapshot takes. |
| `internal/core/temporal/temporal_test.go` | modify | `TestBoundsRefusesAnUnboundBusinessAxis`. |

⚠ `internal/core/temporal/**` is governed by ADR-002. `Bounds` is added there
rather than in the evaluator because a `ports.Snapshot` needs both axes and only
`temporal` may name both — the same rule that moved `At` there in ADR-026.

## Ordered Steps

1. [S1] Write the failing tests first (TDD red): `TestAPredicateThatMatchesNothingReturnsNothing`, `TestAPredicateOnAnUnprojectedAttributeStillFilters`, `TestASelectReadsOneEntityOnce`, `TestAComparisonThatCannotBeMadeIsRefused`, `TestNumericIsAPropertyOfTheQueryNotTheData`, `TestOneSnapshotReachesTheReaderAndTheFilter`, `TestARetractedAttributeIsAbsent`, `TestEveryOperatorFilters`, `TestASelectRunsAgainstARealLeaf`. Run the Acceptance fence and confirm it is red. ⚠Check each name is SELECTED by the fence's `-run` filter before running any mutant. [proof: acceptance]
2. [S2] Add `temporal.Query.Bounds`, rendering a resolved query as a transaction identifier and an instant. ⚠An OPEN system axis becomes the MAXIMUM identifier, because a snapshot takes a bound and the bound that excludes nothing is the largest one. ⚠An UNBOUND business axis is refused rather than defaulted to zero — zero is the epoch, which is a different question, not an absent one. [proof: mutation]
3. [S3] Implement `Select` to resolve the time clause ONCE and hand the result to both the reader's snapshot and `temporal.Visible`, unchanged. ★It takes an instant rather than a clock, so reading the clock twice inside one statement is not expressible. [proof: mutation]
4. [S4] Load the named entity ONCE. ⚠Every `SELECT` in this language names exactly one entity; walking a leaf to answer one would make every statement need everything in memory. [proof: mutation]
5. [S5] Reduce to the latest visible datom per attribute, and let a retraction SUPPRESS the attribute rather than report it as a value. [proof: mutation]
6. [S6] Evaluate the predicate against the entity's WHOLE attribute set, before the projection narrows it. ⚠The obvious order is the other one, and it makes the published example `SELECT name FROM planet-7 WHERE class = 'terrestrial'` silently return nothing. [proof: mutation]
7. [S7] Compare numerically when the LITERAL was written as a number, and as bytes otherwise. ⚠A property of the query text, not of the data: otherwise the same statement changes meaning when the data does, and nothing in it says which was meant. [proof: mutation]
8. [S8] Refuse a comparison that cannot be made with `ErrNotComparable`. ⚠Not false — "this is not a number" and "this is not greater than five" are different answers. [proof: mutation]
9. [S9] Apply the projection last: `*` returns every qualifying attribute, a named list returns those it names.

## Acceptance

```bash
set -o pipefail
go test ./internal/core/eval/... -race -run 'TestAPredicateThatMatchesNothingReturnsNothing|TestAPredicateOnAnUnprojectedAttributeStillFilters|TestASelectReadsOneEntityOnce|TestAComparisonThatCannotBeMadeIsRefused|TestNumericIsAPropertyOfTheQueryNotTheData|TestOneSnapshotReachesTheReaderAndTheFilter|TestARetractedAttributeIsAbsent|TestEveryOperatorFilters|TestASelectRunsAgainstARealLeaf' -count=1 2>&1 | tee /tmp/adr027-t1a.out \
  && ! grep -qE "no tests to run|matched no packages|^FAIL|^--- FAIL|DATA RACE" /tmp/adr027-t1a.out \
  && go test ./internal/core/temporal/... -race -run 'TestBoundsRefusesAnUnboundBusinessAxis' -count=1 2>&1 | tee /tmp/adr027-t1b.out \
  && ! grep -qE "no tests to run|matched no packages|^FAIL|^--- FAIL|DATA RACE" /tmp/adr027-t1b.out \
  && go test ./internal/core/eval/... ./internal/core/temporal/... ./internal/core/ql/... ./internal/core/leafstore/... -race -count=1 2>&1 | tee /tmp/adr027-t1c.out \
  && ! grep -qE "no tests to run|matched no packages|^FAIL|^--- FAIL|DATA RACE" /tmp/adr027-t1c.out
```

The third command re-runs the suites this evaluator is built on, because it
composes their meaning and must not land by changing one of them.

## Tests

| Test name | File | Verifies | Covers | Steps |
|-----------|------|----------|--------|-------|
| `TestAPredicateThatMatchesNothingReturnsNothing` | `internal/core/eval/eval_test.go` | `SELECT * FROM e WHERE mass = "999"` over an entity whose mass is 5 returns NO rows — **the falsifier ADR-027 names in `Enforced-by:`**, and the exact behaviour shipped before this task | — | S6 |
| `TestAPredicateOnAnUnprojectedAttributeStillFilters` | `internal/core/eval/eval_test.go` | `SELECT name FROM e WHERE class = 'terrestrial'` returns the `name` row when `class` matches and nothing when it does not — the published example, which narrowing before filtering silently answers with nothing | — | S6, S9 |
| `TestASelectReadsOneEntityOnce` | `internal/core/eval/eval_test.go` | A counting reader records exactly ONE `Load`, for the named entity, whatever else the leaf holds | — | S4 |
| `TestAComparisonThatCannotBeMadeIsRefused` | `internal/core/eval/eval_test.go` | `WHERE mass > 5` against a value that is not a number is `ErrNotComparable`, and specifically not an empty result | — | S8 |
| `TestNumericIsAPropertyOfTheQueryNotTheData` | `internal/core/eval/eval_test.go` | `WHERE v < 9` and `WHERE v < "9"` over the stored value `10` disagree — numeric says no, textual says yes — so the comparison is decided by how the query was written | — | S7 |
| `TestOneSnapshotReachesTheReaderAndTheFilter` | `internal/core/eval/eval_test.go` | The snapshot the reader is handed matches what the clause resolved to, and a datom invisible at that snapshot is absent from the rows — one resolution reaching both | — | S2, S3 |
| `TestARetractedAttributeIsAbsent` | `internal/core/eval/eval_test.go` | An attribute whose latest visible datom is a retraction produces no row, while an earlier assertion of it does not resurface | — | S5 |
| `TestEveryOperatorFilters` | `internal/core/eval/eval_test.go` | All seven operators change the answer — a table over `=`, `==`, `!=`, `<`, `>`, `<=`, `>=` with a matching and a non-matching case each, so none of them is quietly accepted and ignored | — | S7 |
| `TestASelectRunsAgainstARealLeaf` | `internal/core/eval/eval_test.go` | The same statement evaluated against a `leafstore.Store` in a temporary directory gives the same answer as against the fake — the port is the contract, not the implementation | — | S3, S4 |
| `TestBoundsRefusesAnUnboundBusinessAxis` | `internal/core/temporal/temporal_test.go` | `Bounds` refuses a query with no business instant rather than rendering it as zero, and renders an OPEN system axis as an identifier nothing exceeds | — | S2 |

## Reachability

| Rung | How this task shows it |
|------|------------------------|
| 1 — exists | The ten tests above, against a fake reader and a real leaf. |
| 2 — something selects it | `eval.Select` is the only evaluation path; T2 makes the session call it and deletes the second implementation. |
| 3 — the caller can discover it | Both refusals are named sentinels, so a type mismatch is told apart from an empty answer at the API. |
| 4 — it is used | T2 wires the session onto it, so `WHERE` filters for a caller of `cmd/sdev1-ql`. |

## Mutation Log

- 2026-09-04 · ed55798* · mutant killed · exit 1 · `internal/core/eval/eval.go` · skips the predicate entirely, which is exactly what shipped: the clause parses, the statement succeeds, and every row comes back however narrow the question was · acceptance-sha256:34e44f1daa1114f605490e69bd8c57fa41be02fde64271b724ef7aae2f524176 · covers:a WHERE clause that actually filters
- 2026-09-04 · ed55798* · mutant killed · exit 1 · `internal/core/eval/eval.go` · tests the predicate against the PROJECTED attributes, so SELECT name ... WHERE class = ... has nothing to test against and silently returns nothing on data where it should return a row · acceptance-sha256:34e44f1daa1114f605490e69bd8c57fa41be02fde64271b724ef7aae2f524176 · covers:a predicate evaluated before the projection narrows the attributes
- 2026-09-04 · ed55798* · mutant killed · exit 1 · `internal/core/eval/eval.go` · reads other entities before the named one, which is what walking a leaf looks like; the answer stays correct, so only a test that COUNTS the reads can see it · acceptance-sha256:34e44f1daa1114f605490e69bd8c57fa41be02fde64271b724ef7aae2f524176 · covers:a SELECT costing one entity read rather than a leaf
- 2026-09-04 · ed55798* · mutant killed · exit 1 · `internal/core/eval/eval.go` · answers false for a comparison it could not make, hiding a type error inside an ordinary-looking empty result rather than naming it · acceptance-sha256:34e44f1daa1114f605490e69bd8c57fa41be02fde64271b724ef7aae2f524176 · covers:a comparison that cannot be made being refused rather than answered false
- 2026-09-04 · ed55798* · mutant inconclusive · exit 1 · `internal/core/eval/eval.go` · hands the store the raw clock reading instead of the instant the clause resolved to, so the store and the visibility filter answer about two different moments · acceptance-sha256:34e44f1daa1114f605490e69bd8c57fa41be02fde64271b724ef7aae2f524176 · covers:one resolution per statement, reaching the store and the filter unchanged
  ```
  the fence failed on a build/parse error, not an assertion
  ```
- 2026-09-04 · ed55798* · mutant survived · exit 0 · `internal/core/temporal/qualifiers.go` · stops refusing an unbound business axis whenever a transaction bound is present, so the instant silently becomes zero — the epoch, which is a different question rather than an absent one · acceptance-sha256:34e44f1daa1114f605490e69bd8c57fa41be02fde64271b724ef7aae2f524176 · covers:one resolution per statement, reaching the store and the filter unchanged
  ```
  the fence passed with the mechanism broken; it may not materialize, compile, load, or assert on the changed path
  ```
- 2026-09-04 · ed55798* · mutant killed · exit 1 · `internal/core/eval/eval.go` · adjusts the resolved instant on its way to the store, so the store and the visibility filter answer about two different moments while one resolution appears to have been used · acceptance-sha256:34e44f1daa1114f605490e69bd8c57fa41be02fde64271b724ef7aae2f524176 · covers:one resolution per statement, reaching the store and the filter unchanged
- 2026-09-04 · ed55798* · mutant killed · exit 1 · `internal/core/temporal/qualifiers.go` · stops refusing an unbound business axis whenever a transaction bound is present, so the instant silently becomes zero — the epoch, which is a different question rather than an absent one · acceptance-sha256:34e44f1daa1114f605490e69bd8c57fa41be02fde64271b724ef7aae2f524176 · covers:one resolution per statement, reaching the store and the filter unchanged

## Invariants

- No clause the parser accepts is silently discarded.
- The predicate sees the entity's whole attribute set; the projection runs last.
- One `Load`, for the named entity.
- One resolution, reaching the reader and the filter unchanged.

## Risks

- ⚠ **A `WHERE` test that filters on a PROJECTED attribute passes under the wrong order.** `SELECT * FROM e WHERE mass = …` narrows to everything, so the bug in rule 5 is invisible. One test filters on an attribute the projection excludes, which is the only shape that can see it.
- ⚠ **A test for one operator proves nothing about the other six.** `TestEveryOperatorFilters` runs all seven with a matching and a non-matching case, because an operator that is accepted and ignored looks exactly like one that matched.
- ⚠ **`TestASelectReadsOneEntityOnce` must count, not merely assert the answer.** An evaluator that loaded every entity and then discarded all but one returns exactly the right rows.
- ⚠ **Comparing against a real leaf as well as a fake is not redundancy.** The fake is what makes the read count and the snapshot observable; the leaf is what proves the port was the contract rather than the fake's shape.
- The evaluator handles `SELECT` only. `SEARCH` and `TRAVERSE` still answer from session state, and that split is recorded as a follow-up on the parent record rather than hidden.

## Stop Condition

Stop and ask before making a failed comparison return false, or before applying
the projection ahead of the predicate. Both make the evaluator simpler, both are
what a reasonable implementer writes first, and both turn a wrong answer into one
that is indistinguishable from a right one.

## Out of Scope

- Planning, index selection and term ordering (deferred: `docs/adr/BACKLOG.md` §15)
- `MATCH SHAPE` and a similarity metric (deferred: `docs/adr/BACKLOG.md` §20)
- Enumerating entities without a name (deferred: `docs/adr/BACKLOG.md` §20)
- `AND` / `OR` / parentheses (deferred: `docs/adr/BACKLOG.md` §20)
- Wiring the session onto this (deferred: T2 of this record)
- Resolving the two-axis defaults (permanent: boundary: ADR-002 rule 6's table has one implementation, in `ql.TimeClause.Resolve`, and re-deriving a bound is the defect `BACKLOG.md` §20 names)

## Verification Log
- 2026-09-04 · ed55798* · exit 0 · `set -o pipefail …` · acceptance-sha256:34e44f1daa1114f605490e69bd8c57fa41be02fde64271b724ef7aae2f524176 · ms:6404
- 2026-09-04 · ed55798* · exit 0 · `set -o pipefail …` · acceptance-sha256:34e44f1daa1114f605490e69bd8c57fa41be02fde64271b724ef7aae2f524176 · ms:5714
- 2026-09-04 · ed55798* · exit 0 · `set -o pipefail …` · acceptance-sha256:34e44f1daa1114f605490e69bd8c57fa41be02fde64271b724ef7aae2f524176 · ms:5629
- 2026-09-04 · ed55798* · exit 0 · `set -o pipefail …` · acceptance-sha256:34e44f1daa1114f605490e69bd8c57fa41be02fde64271b724ef7aae2f524176 · ms:5843
- 2026-09-04 · ed55798* · exit 0 · `set -o pipefail …` · acceptance-sha256:34e44f1daa1114f605490e69bd8c57fa41be02fde64271b724ef7aae2f524176 · ms:5825
- 2026-09-04 · ed55798* · exit 0 · `set -o pipefail …` · acceptance-sha256:34e44f1daa1114f605490e69bd8c57fa41be02fde64271b724ef7aae2f524176 · ms:5774
- 2026-09-04 · ed55798* · exit 0 · `set -o pipefail …` · acceptance-sha256:34e44f1daa1114f605490e69bd8c57fa41be02fde64271b724ef7aae2f524176 · ms:5839
- 2026-09-04 · ed55798* · exit 0 · `set -o pipefail …` · acceptance-sha256:34e44f1daa1114f605490e69bd8c57fa41be02fde64271b724ef7aae2f524176 · ms:5629
- 2026-09-04 · ed55798* · exit 0 · `set -o pipefail …` · acceptance-sha256:34e44f1daa1114f605490e69bd8c57fa41be02fde64271b724ef7aae2f524176 · ms:5785
- 2026-09-04 · ed55798* · exit 0 · `set -o pipefail …` · acceptance-sha256:34e44f1daa1114f605490e69bd8c57fa41be02fde64271b724ef7aae2f524176 · ms:5675
