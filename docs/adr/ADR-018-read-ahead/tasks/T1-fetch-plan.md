# Task ADR-018-T1: The plan — nearest k, the rest as hedge, refused when over budget

**Depends-on:** none
**Covers:** none — no spec
**Estimated scope:** M (multi-file)
**Owner:** unassigned
**Produces:** `prefetch.Location`, `prefetch.Budget`, `prefetch.Plan`, `prefetch.PlanFetch`, `prefetch.ErrOverBudget`, `prefetch.ErrTooFewFragments`
**Consumes:** `erasure.StripeHeader` from ADR-006, `topology.Map` and `topology.Distance` from ADR-001
**Data dependency:** hermetic
**Proof map:** v1
**Rests-on:** `a plan fetching exactly k rather than k+m`, `the k chosen being the nearest k`, `an over-budget plan being refused rather than truncated`

## Goal

Decide which fragments a read should ask for, so a healthy read costs `k`
fetches and a slow node does not stall the block.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `internal/core/prefetch/doc.go` | add | Package comment: why a prefetch is a hint, why it is budgeted, and why it fetches `k` rather than `k+m`. |
| `internal/core/prefetch/prefetch.go` | add | `Location`, `Budget`, `Plan`, `PlanFetch`, and the two sentinels. |
| `internal/core/prefetch/prefetch_test.go` | add | The tests below, including the falsifier named in ADR-018's `Enforced-by:`. |

★ A `Plan` is a VALUE with no side effects and nothing to clean up. That is what
makes rule 6 true rather than aspirational: a caller that ignores a plan is in
exactly the same position as one that never asked for it.

## Ordered Steps

1. [S1] Write the failing tests first (TDD red): `TestPlanFetchesExactlyKNotKPlusM`, `TestPlanChoosesTheNearestK`, `TestOverBudgetPlanIsRefusedNotTruncated`, `TestTooFewFragmentsIsRefused`, `TestPlanIsAValueWithNoSideEffects`. Run the Acceptance fence and confirm it is red. ⚠Check each name is SELECTED by the fence's `-run` filter before running any mutant. [proof: acceptance]
2. [S2] Define `Location` as a fragment index and the node holding it, and `Budget` as declared bytes for one read.
3. [S3] Implement `PlanFetch`: take the stripe's `k` from its header rather than from any second notion of how many fragments a read needs.
4. [S4] Choose the NEAREST `k` by `topology.Distance`, reusing the existing metric so "near" means the same thing here as it does to placement. [proof: mutation]
5. [S5] Put the remaining locations in a HEDGE list rather than discarding them. ★Fetching them upfront wastes `m/k` of the link on every healthy read; discarding them means one slow node stalls the block. Carrying them is the shape that avoids both.
6. [S6] Refuse a plan whose bytes exceed the budget with `ErrOverBudget`, returning NO partial plan. ★A truncated plan cannot reconstruct, so it spends bandwidth and delivers nothing — strictly worse than not prefetching, and the caller's fallback always works.
7. [S7] Refuse a stripe with fewer than `k` locations with `ErrTooFewFragments`, rather than planning a fetch that cannot succeed.

## Acceptance

```bash
set -o pipefail
go test ./internal/core/prefetch/... -race -run 'TestPlanFetchesExactlyK|TestPlanChoosesTheNearestK|TestOverBudgetPlanIsRefused|TestTooFewFragmentsIsRefused|TestPlanIsAValueWithNoSideEffects' -count=1 2>&1 | tee /tmp/adr018-t1a.out \
  && ! grep -qE "no tests to run|matched no packages|^FAIL|^--- FAIL|DATA RACE" /tmp/adr018-t1a.out \
  && go test ./internal/core/prefetch/... ./internal/core/erasure/... ./internal/core/topology/... -race -count=1 2>&1 | tee /tmp/adr018-t1b.out \
  && ! grep -qE "no tests to run|matched no packages|^FAIL|^--- FAIL|DATA RACE" /tmp/adr018-t1b.out
```

## Tests

| Test name | File | Verifies | Covers | Steps |
|-----------|------|----------|--------|-------|
| `TestPlanFetchesExactlyKNotKPlusM` | `internal/core/prefetch/prefetch_test.go` | The fetch list is exactly `k` long and the remaining `m` are in the hedge list rather than absent — fetching `k+m` wastes `m/k` of the link on every healthy read. **The falsifier ADR-018 names in `Enforced-by:`** | — | S3, S5 |
| `TestPlanChoosesTheNearestK` | `internal/core/prefetch/prefetch_test.go` | The `k` fetched are the nearest by the topology's own distance, and the hedge is ordered by nearness too, so a hedge picks the next best rather than an arbitrary node | — | S4 |
| `TestOverBudgetPlanIsRefusedNotTruncated` | `internal/core/prefetch/prefetch_test.go` | A plan exceeding its budget yields `ErrOverBudget` and NO plan at all — not a shorter one, since fewer than `k` fragments cannot reconstruct | — | S6 |
| `TestTooFewFragmentsIsRefused` | `internal/core/prefetch/prefetch_test.go` | Fewer than `k` known locations yields `ErrTooFewFragments` rather than a plan that cannot succeed | — | S7 |
| `TestPlanIsAValueWithNoSideEffects` | `internal/core/prefetch/prefetch_test.go` | Planning twice from the same inputs gives the same plan and changes nothing a caller passed in, so ignoring a plan costs exactly nothing | — | S2 |

## Reachability

| Rung | How this task shows it |
|------|------------------------|
| 1 — exists | The five tests above. |
| 2 — something selects it | `PlanFetch` is the only way a stripe becomes a set of fetches, and its `k` comes from the stripe header rather than a parameter a caller could get wrong. |
| 3 — the caller can discover it | `Plan` carries `Fetch`, `Hedge` and `Bytes` separately, so a caller sees what it will spend before spending it. |
| 4 — it is used | Nothing fetches yet; the plan is decidable and its execution is not. |

## Mutation Log

- 2026-09-04 · bfac0e5* · mutant killed · exit 1 · `internal/core/prefetch/prefetch.go` · fetches every fragment instead of k, wasting m/k of the bandwidth on every healthy read — and that is the same link admission control sheds against, so the prefetch makes a node shed the reads it was accelerating · acceptance-sha256:b8a67e3b7cff4394c68b9b0bec3b263d43d88c2743db2e1516f203411fa0c9af · covers:a plan fetching exactly k rather than k+m
- 2026-09-04 · bfac0e5* · mutant killed · exit 1 · `internal/core/prefetch/prefetch.go` · returns the locations in the order they arrived rather than nearest-first, so the k fetched are arbitrary — any k reconstruct, which is precisely why choosing them well is free and choosing them badly is invisible · acceptance-sha256:b8a67e3b7cff4394c68b9b0bec3b263d43d88c2743db2e1516f203411fa0c9af · covers:the k chosen being the nearest k
- 2026-09-04 · bfac0e5* · mutant killed · exit 1 · `internal/core/prefetch/prefetch.go` · truncates the plan to whatever the budget affords, so fewer than k fragments are fetched and the block cannot reconstruct — bandwidth spent for nothing, strictly worse than not prefetching at all · acceptance-sha256:b8a67e3b7cff4394c68b9b0bec3b263d43d88c2743db2e1516f203411fa0c9af · covers:an over-budget plan being refused rather than truncated

## Invariants

- A plan's fetch list is exactly `k` long.
- The remaining locations are in the hedge list, never discarded.
- Both lists are ordered by nearness.
- An over-budget plan is refused with no partial result.
- Planning has no side effects and is a pure function of its inputs.

## Risks

- ⚠ **A test that only counts the fetch list would pass for an implementation that discarded the hedge.** Discarding is the other failure — it means one slow node stalls the block — so the test asserts the hedge holds exactly the remainder, and that the two together cover every location.
- A nearness test with one obviously-nearest node can pass for an implementation that returns the input order. The topology puts the nearest nodes LAST in the input, so returning the input order fails.
- An over-budget test that checks only for an error would pass for one returning both an error and a usable plan. The test asserts the returned plan is empty.

## Stop Condition

Stop and ask before making a plan fetch `k+m` "for safety". It is the simpler
implementation, it looks more robust, and it spends `m/k` of the link on every
healthy read — the same link admission control is shedding against, so the
prefetch would make a node shed the reads it was accelerating.

## Out of Scope

- The window and the hedge trigger — that is T2.
- Actually fetching anything (deferred: `docs/adr/BACKLOG.md` §18)
- Caching and eviction (deferred: `docs/adr/BACKLOG.md` §24)
- Where fragments are (permanent: boundary: ADR-004's policy and the placement service decide that; this task takes locations as given and chooses among them)

## Verification Log
- 2026-09-04 · bfac0e5* · exit 0 · `set -o pipefail …` · acceptance-sha256:b8a67e3b7cff4394c68b9b0bec3b263d43d88c2743db2e1516f203411fa0c9af · ms:3863
- 2026-09-04 · bfac0e5* · exit 0 · `set -o pipefail …` · acceptance-sha256:b8a67e3b7cff4394c68b9b0bec3b263d43d88c2743db2e1516f203411fa0c9af · ms:3846
- 2026-09-04 · bfac0e5* · exit 0 · `set -o pipefail …` · acceptance-sha256:b8a67e3b7cff4394c68b9b0bec3b263d43d88c2743db2e1516f203411fa0c9af · ms:3798
- 2026-09-04 · bfac0e5* · exit 0 · `set -o pipefail …` · acceptance-sha256:b8a67e3b7cff4394c68b9b0bec3b263d43d88c2743db2e1516f203411fa0c9af · ms:3732
- 2026-09-04 · bfac0e5* · exit 0 · `set -o pipefail …` · acceptance-sha256:b8a67e3b7cff4394c68b9b0bec3b263d43d88c2743db2e1516f203411fa0c9af · ms:3714
- 2026-09-04 · 09ec963* · exit 0 · `set -o pipefail …` · acceptance-sha256:b8a67e3b7cff4394c68b9b0bec3b263d43d88c2743db2e1516f203411fa0c9af · ms:3646
