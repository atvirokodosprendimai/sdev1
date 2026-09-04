# Task ADR-011-T2: Shape matching where an optional leg binds nothing, and a policy clause for new data only

**Depends-on:** T1
**Covers:** none — no spec
**Estimated scope:** M (multi-file)
**Owner:** unassigned
**Produces:** `ql.ShapeQuery`, `ql.Leg`, `ql.Binding`, `ql.Unbound`, `ql.PolicyClause`, `ql.ErrNoThreshold`
**Consumes:** `ql.Parse`, `ql.Statement`, `ql.TimeClause` (T1), `segment.CodecID` from ADR-005
**Data dependency:** hermetic
**Proof map:** v1
**Rests-on:** `an optional leg yielding an unbound value rather than dropping the row`, `a shape query requiring a stated metric and threshold`, `a policy clause setting the policy for new data only`

## Goal

Make "find things like this" a query somebody can reproduce, and make `OPTIONAL`
mean something different from `REQUIRE`.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `internal/core/ql/shape.go` | add | `ShapeQuery`, `Leg`, `Binding`, `Unbound`, and `ErrNoThreshold`. |
| `internal/core/ql/policy.go` | add | `PolicyClause`, naming a codec ADR-005 already knows. |
| `internal/core/ql/parse.go` | edit | The two new clauses join the grammar T1 defined. |
| `internal/core/ql/shape_test.go` | add | The tests below, including the falsifier for the optional leg. |

★ A `Binding` is either bound or UNBOUND, and both are values in a returned row.
A row is never dropped for having an unbound leg — that is the whole distinction
between the two kinds of leg.

## Ordered Steps

1. [S1] Write the failing tests first (TDD red): `TestOptionalLegYieldsAnUnboundValue`, `TestOptionalLegNeverDropsTheRow`, `TestShapeQueryRequiresAMetricAndThreshold`, `TestRequiredLegDoesDropTheRow`, `TestPolicyClauseAppliesToNewDataOnly`. Run the Acceptance fence and confirm it is red. ⚠Check each name is SELECTED by the fence's `-run` filter before running any mutant. [proof: acceptance]
2. [S2] Define `Leg` with a required/optional kind, and `Binding` as a value that is either bound or `Unbound`.
3. [S3] Parse `MATCH SHAPE LIKE <subject> REQUIRE <legs> OPTIONAL <legs> SIMILARITY >= <threshold>`, attaching T1's time clause. ★A time clause may attach per leg, which is why it was made a clause.
4. [S4] Refuse a shape query with no metric or no threshold with `ErrNoThreshold`. ★Similarity without a stated threshold is a result nobody can reproduce, so it is a parse error rather than a default.
5. [S5] Make an optional leg that matches nothing produce a row with an `Unbound` binding. [proof: mutation]
6. [S6] Make a REQUIRED leg that matches nothing drop the row, so the two kinds are observably different rather than differing only in a field nobody reads.
7. [S7] Parse `WITH COMPRESSION <codec>` into a `PolicyClause` naming a `segment.CodecID`, and record in the AST that it applies to new data only. ★Every block records how it was written, so a policy change reinterprets nothing; the language deliberately cannot express "re-encode what exists".

## Acceptance

```bash
set -o pipefail
go test ./internal/core/ql/... -race -run 'TestOptionalLeg|TestShapeQueryRequires|TestRequiredLeg|TestPolicyClause' -count=1 2>&1 | tee /tmp/adr011-t2a.out \
  && ! grep -qE "no tests to run|matched no packages|^FAIL|^--- FAIL|DATA RACE" /tmp/adr011-t2a.out \
  && go test ./internal/core/ql/... ./internal/core/temporal/... ./internal/core/segment/... -race -count=1 2>&1 | tee /tmp/adr011-t2b.out \
  && ! grep -qE "no tests to run|matched no packages|^FAIL|^--- FAIL|DATA RACE" /tmp/adr011-t2b.out
```

## Tests

| Test name | File | Verifies | Covers | Steps |
|-----------|------|----------|--------|-------|
| `TestOptionalLegYieldsAnUnboundValue` | `internal/core/ql/shape_test.go` | An optional leg that matches nothing produces a row carrying an `Unbound` binding for it, rather than no binding at all. **The falsifier for ADR-011's rule 5** | — | S2, S5 |
| `TestOptionalLegNeverDropsTheRow` | `internal/core/ql/shape_test.go` | The row is RETURNED. Dropping it would make `OPTIONAL` a synonym for `REQUIRE`, which is undetectable on data where the leg is always present | — | S5 |
| `TestShapeQueryRequiresAMetricAndThreshold` | `internal/core/ql/shape_test.go` | A well-formed shape query parses to required legs, optional legs, a metric and a threshold with its time clause attached; one written without a metric or without a threshold is a parse error naming what is missing, rather than acquiring a default nobody stated | — | S3, S4 |
| `TestRequiredLegDoesDropTheRow` | `internal/core/ql/shape_test.go` | A required leg that matches nothing drops the row, so the two kinds of leg are observably different — without this the optional test could pass for an implementation that never drops anything | — | S6 |
| `TestPolicyClauseAppliesToNewDataOnly` | `internal/core/ql/shape_test.go` | `WITH COMPRESSION` parses to a codec the segment format knows, and the AST records that it governs new writes; there is no syntax for re-encoding what exists | — | S7 |

## Reachability

| Rung | How this task shows it |
|------|------------------------|
| 1 — exists | The five tests above. |
| 2 — something selects it | The clauses join T1's grammar, so `Parse` is the only way either reaches an AST; deleting the optional-leg handling breaks the falsifier. |
| 3 — the caller can discover it | The two leg kinds are separate constructors and `Unbound` is a named value, so a consumer reading the AST cannot miss that a binding may be absent. |
| 4 — it is used | Nothing evaluates a shape query yet; the parse and the binding shape are the measurement. |

## Mutation Log

- 2026-09-04 · 4016ba8* · mutant killed · exit 1 · `internal/core/ql/shape.go` · drops a row whose optional leg matched nothing, which makes OPTIONAL a synonym for REQUIRE — a difference undetectable on any dataset where the leg happens always to be present, and therefore on the data anyone tests with · acceptance-sha256:b3e4c7f81b1f5a7d1b5a52934499a7d9b03813881ecb1682d0594cd082410394 · covers:an optional leg yielding an unbound value rather than dropping the row
- 2026-09-04 · 4016ba8* · mutant survived · exit 0 · `internal/core/ql/shape.go` · accepts a shape query with no metric and no threshold, so similarity becomes a closeness nobody stated and a result nobody can reproduce · acceptance-sha256:b3e4c7f81b1f5a7d1b5a52934499a7d9b03813881ecb1682d0594cd082410394 · covers:a shape query requiring a stated metric and threshold
  ```
  the fence passed with the mechanism broken; it may not materialize, compile, load, or assert on the changed path
  ```
- 2026-09-04 · 4016ba8* · mutant killed · exit 1 · `internal/core/ql/policy.go` · adds a second policy scope, which is what a syntax for re-encoding existing data would need — every block already records how it was written, so the second scope can only mean rewriting bytes the language should not be able to request · acceptance-sha256:b3e4c7f81b1f5a7d1b5a52934499a7d9b03813881ecb1682d0594cd082410394 · covers:a policy clause setting the policy for new data only
- 2026-09-04 · 4016ba8* · mutant killed · exit 1 · `internal/core/ql/shape.go` · removes the guard that requires a SIMILARITY clause. A later check still errors, so a test that only counts errors passes with the mechanism gone — this mutant SURVIVED that weaker test and the assertion was strengthened to cite what the error must say · acceptance-sha256:b3e4c7f81b1f5a7d1b5a52934499a7d9b03813881ecb1682d0594cd082410394 · covers:a shape query requiring a stated metric and threshold

## Invariants

- An optional leg that matches nothing yields `Unbound`, and the row is returned.
- A required leg that matches nothing drops the row.
- A shape query without a metric and a threshold is a parse error.
- A policy clause governs new writes only; no syntax re-encodes existing data.

## Risks

- ⚠ **`TestOptionalLegYieldsAnUnboundValue` alone would pass for an implementation that never drops any row.** `TestRequiredLegDoesDropTheRow` is the control that makes the pair meaningful — without it, "optional does not drop" says nothing, because nothing does.
- A threshold test that supplies one and checks it parses proves nothing about the refusal. The test omits the metric and the threshold SEPARATELY, so each refusal is exercised on its own.
- ⚠ **Asserting that "an error occurred" is not enough, and a surviving mutant proved it here.** With the `SIMILARITY` guard removed, the NEXT check in the parser still failed, so the first version of this test passed with the mechanism gone. Every case now asserts what the error SAYS — that a missing `SIMILARITY` clause cites the requirement itself — because an error raised by some later check is indistinguishable from the one this case is about. The `mutant survived` row is left in the log above; it is what actually happened.
- `Unbound` is easy to conflate with a zero value. The test asserts an unbound binding is distinguishable from a binding of the empty string, which is the case a consumer would otherwise get wrong.

## Stop Condition

Stop and ask before giving `SIMILARITY` a default threshold. A default makes
every unqualified shape query reproducible only by whoever knows the default, and
the value would then be a constant nobody wrote down.

## Out of Scope

- Computing similarity — the metric is named and stated, and running it needs data (deferred: `docs/adr/BACKLOG.md` §20)
- Evaluating a shape query (deferred: `docs/adr/BACKLOG.md` §20)
- Re-encoding existing data when a policy changes (deferred: `docs/adr/BACKLOG.md` §14)

## Verification Log
- 2026-09-04 · 4016ba8* · exit 0 · `set -o pipefail …` · acceptance-sha256:b3e4c7f81b1f5a7d1b5a52934499a7d9b03813881ecb1682d0594cd082410394 · ms:3838
- 2026-09-04 · 4016ba8* · exit 0 · `set -o pipefail …` · acceptance-sha256:b3e4c7f81b1f5a7d1b5a52934499a7d9b03813881ecb1682d0594cd082410394 · ms:3814
- 2026-09-04 · 4016ba8* · exit 0 · `set -o pipefail …` · acceptance-sha256:b3e4c7f81b1f5a7d1b5a52934499a7d9b03813881ecb1682d0594cd082410394 · ms:4014
- 2026-09-04 · 4016ba8* · exit 0 · `set -o pipefail …` · acceptance-sha256:b3e4c7f81b1f5a7d1b5a52934499a7d9b03813881ecb1682d0594cd082410394 · ms:3817
- 2026-09-04 · 4016ba8* · exit 0 · `set -o pipefail …` · acceptance-sha256:b3e4c7f81b1f5a7d1b5a52934499a7d9b03813881ecb1682d0594cd082410394 · ms:3845
- 2026-09-04 · 4016ba8* · exit 0 · `set -o pipefail …` · acceptance-sha256:b3e4c7f81b1f5a7d1b5a52934499a7d9b03813881ecb1682d0594cd082410394 · ms:3938
