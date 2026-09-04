# Task ADR-018-T2: How far ahead a budget reaches, and which reserve to try when one is late

**Depends-on:** T1
**Covers:** none — no spec
**Estimated scope:** M (multi-file)
**Owner:** unassigned
**Produces:** `prefetch.Window`, `prefetch.PlanWindow`, `prefetch.Hedge`, `prefetch.ErrNoHedgeLeft`
**Consumes:** `prefetch.Plan`, `prefetch.Budget`, `prefetch.PlanFetch` (T1), `erasure.StripeHeader` from ADR-006
**Data dependency:** hermetic
**Proof map:** v1
**Rests-on:** `a window bounded by the declared budget rather than by the blob`, `a hedge being drawn only when a fetch is late`, `a hedge preserving the nearest-first order`

## Goal

Turn "read ahead" into a number of blocks a caller can afford, and give a stalled
fetch somewhere to go without spending the reserve upfront.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `internal/core/prefetch/window.go` | add | `Window`, `PlanWindow`, `Hedge`, and `ErrNoHedgeLeft`. |
| `internal/core/prefetch/window_test.go` | add | The tests below. |

★ The window is what makes "load the whole file" safe to ask for: the answer is
"as many blocks as your budget reaches", which is the whole file for a small one
and a bounded prefix for a large one — the same request, two answers, neither of
them an out-of-memory kill.

## Ordered Steps

1. [S1] Write the failing tests first (TDD red): `TestWindowIsBoundedByBudgetNotBlobSize`, `TestWindowOfZeroIsRefusedNotSilent`, `TestHedgeIsDrawnOnlyWhenLate`, `TestHedgePreservesNearestFirst`, `TestHedgeExhaustionIsNamed`. Run the Acceptance fence and confirm it is red. ⚠Check each name is SELECTED by the fence's `-run` filter before running any mutant. [proof: acceptance]
2. [S2] Define `Window` as how many consecutive blocks a budget reaches, given a stripe's per-block fetch cost.
3. [S3] Implement `PlanWindow`: plan as many blocks as the budget affords and no more, whatever the blob's size. ★"Load every part into memory" is right for a 40MB blob and an out-of-memory kill for a 4TB one; the budget is what makes the same request safe in both cases.
4. [S4] Refuse a budget that affords ZERO blocks with a named error rather than returning an empty window. ★An empty window is indistinguishable from "prefetching is off", and a caller that meant to prefetch would never learn its budget was too small for even one block.
5. [S5] Implement `Hedge`: given a plan and the fragments still outstanding, return the next reserve fragment to try. [proof: mutation]
6. [S6] Draw from the hedge only when asked — the reserve is never part of the initial fetch. ★That is rule 1 of the record: fetching `k+m` upfront wastes `m/k` of the link on every healthy read.
7. [S7] Preserve nearest-first order in the hedge, so a hedge picks the next best node rather than an arbitrary one, and refuse with `ErrNoHedgeLeft` when the reserve is spent.

## Acceptance

```bash
set -o pipefail
go test ./internal/core/prefetch/... -race -run 'TestWindowIsBoundedByBudget|TestWindowOfZeroIsRefused|TestHedgeIsDrawnOnlyWhenLate|TestHedgePreservesNearestFirst|TestHedgeExhaustionIsNamed' -count=1 2>&1 | tee /tmp/adr018-t2a.out \
  && ! grep -qE "no tests to run|matched no packages|^FAIL|^--- FAIL|DATA RACE" /tmp/adr018-t2a.out \
  && go test ./internal/core/prefetch/... -race -count=1 2>&1 | tee /tmp/adr018-t2b.out \
  && ! grep -qE "no tests to run|matched no packages|^FAIL|^--- FAIL|DATA RACE" /tmp/adr018-t2b.out
```

## Tests

| Test name | File | Verifies | Covers | Steps |
|-----------|------|----------|--------|-------|
| `TestWindowIsBoundedByBudgetNotBlobSize` | `internal/core/prefetch/window_test.go` | A small blob is fully covered and a large one is covered to the budget's limit — the same request giving two answers, neither of them unbounded | — | S2, S3 |
| `TestWindowOfZeroIsRefusedNotSilent` | `internal/core/prefetch/window_test.go` | A budget too small for even one block is a named refusal, not an empty window that reads as "prefetching is off" | — | S4 |
| `TestHedgeIsDrawnOnlyWhenLate` | `internal/core/prefetch/window_test.go` | The initial plan contains no hedge fragment, and a hedge is produced only when one is asked for — so a healthy read costs `k` and no more | — | S5, S6 |
| `TestHedgePreservesNearestFirst` | `internal/core/prefetch/window_test.go` | Successive hedges return progressively further nodes, so a stalled fetch retries at the next best place rather than an arbitrary one | — | S7 |
| `TestHedgeExhaustionIsNamed` | `internal/core/prefetch/window_test.go` | When the reserve is spent, `ErrNoHedgeLeft` says so rather than returning a zero location a caller would fetch from nowhere | — | S7 |

## Reachability

| Rung | How this task shows it |
|------|------------------------|
| 1 — exists | The five tests above. |
| 2 — something selects it | `PlanWindow` is the only way a budget becomes a block count, and `Hedge` the only way a reserve fragment is drawn; both build on T1's plan. |
| 3 — the caller can discover it | A `Window` reports how many blocks it covers and what it will spend, so a caller sees the cost before paying it. |
| 4 — it is used | Nothing fetches yet; the window and the hedge are decidable and their execution is not. |

## Mutation Log

- 2026-09-04 · bfac0e5* · mutant killed · exit 1 · `internal/core/prefetch/window.go` · covers the whole blob whatever the budget says, so "load every part into memory" is honoured literally — correct for a 40MB blob and an out-of-memory kill for a 4TB one, on a node that was serving other tenants fine · acceptance-sha256:dc37b108f2a3aaa556260f93c2970028bccbbc9de36488d09513318e42777658 · covers:a window bounded by the declared budget rather than by the blob
- 2026-09-04 · bfac0e5* · mutant killed · exit 1 · `internal/core/prefetch/window.go` · reads one past the reserve, so an exhausted hedge indexes out of range instead of saying so — a caller chasing a straggler gets a crash rather than the named refusal it can act on · acceptance-sha256:dc37b108f2a3aaa556260f93c2970028bccbbc9de36488d09513318e42777658 · covers:a hedge preserving the nearest-first order
- 2026-09-04 · bfac0e5* · mutant killed · exit 1 · `internal/core/prefetch/window.go` · returns an empty window instead of refusing when the budget affords no whole block, which is indistinguishable from prefetching being switched off — a caller that meant to prefetch never learns why nothing happened · acceptance-sha256:dc37b108f2a3aaa556260f93c2970028bccbbc9de36488d09513318e42777658 · covers:a hedge being drawn only when a fetch is late
- 2026-09-04 · bfac0e5* · mutant killed · exit 1 · `internal/core/prefetch/prefetch.go` · puts fragments that are already being fetched into the reserve as well, so drawing a hedge re-requests something in flight and a healthy read no longer costs exactly k — the reserve stops being a reserve · acceptance-sha256:dc37b108f2a3aaa556260f93c2970028bccbbc9de36488d09513318e42777658 · covers:a hedge being drawn only when a fetch is late
- 2026-09-04 · bfac0e5* · mutant killed · exit 1 · `internal/core/prefetch/window.go` · draws the reserve furthest-first, so a stalled fetch retries at the worst remaining node instead of the next best one — the retry is slower than the fetch it was meant to rescue · acceptance-sha256:dc37b108f2a3aaa556260f93c2970028bccbbc9de36488d09513318e42777658 · covers:a hedge preserving the nearest-first order

## Invariants

- A window never exceeds its budget, whatever the blob's size.
- A budget affording zero blocks is refused by name.
- No hedge fragment appears in an initial plan.
- Hedges are drawn nearest-first and exhaustion is named.

## Risks

- ⚠ **A window test on a small blob proves nothing about the bound**, because the blob is the limit rather than the budget. Both cases are tested: one where the blob binds and one where the budget does, and the second is the one that matters.
- ⚠ **"Hedge only when late" is easy to test by checking the hedge list is non-empty**, which would pass for an implementation that fetched it upfront. The test asserts the initial plan's FETCH list contains no hedge location, which is the observable form.
- Exhaustion is easy to signal with a zero value, and a caller would fetch from an empty node name. It is a named error instead.
- ⚠ **Two mutants in the log above were bound to the WRONG claim on their first run, and the rows are left in place.** A bounds-check mutant (`drawn >= len` → `drawn > len`) was recorded against *nearest-first order*, and a zero-window mutant (`afford < 1` → `afford < 0`) against *hedge drawn only when late*. Both were killed, so both look like evidence — and neither exercised the mechanism it named. A mutant bound to the wrong claim is worse than no mutant: it makes `Rests-on` read as proved when nothing tested it. The later rows carry the correct pairings, and the earlier ones stay because deleting them would hide that this happened.

## Stop Condition

Stop and ask before letting a window grow to cover a whole blob regardless of
budget, however small the blob is expected to be. The expectation is what fails:
the same code meets a 4TB blob eventually, and the failure is an out-of-memory
kill on a node serving other tenants.

## Out of Scope

- Detecting that a fetch IS late, which needs a transport with timings (deferred: `docs/adr/BACKLOG.md` §18)
- Holding fetched blocks and evicting them (deferred: `docs/adr/BACKLOG.md` §24)
- Deciding whether a read is sequential enough to prefetch (deferred: `docs/adr/BACKLOG.md` §24)

## Verification Log
- 2026-09-04 · bfac0e5* · exit 0 · `set -o pipefail …` · acceptance-sha256:dc37b108f2a3aaa556260f93c2970028bccbbc9de36488d09513318e42777658 · ms:3584
- 2026-09-04 · bfac0e5* · exit 0 · `set -o pipefail …` · acceptance-sha256:dc37b108f2a3aaa556260f93c2970028bccbbc9de36488d09513318e42777658 · ms:3720
- 2026-09-04 · bfac0e5* · exit 0 · `set -o pipefail …` · acceptance-sha256:dc37b108f2a3aaa556260f93c2970028bccbbc9de36488d09513318e42777658 · ms:3602
- 2026-09-04 · bfac0e5* · exit 0 · `set -o pipefail …` · acceptance-sha256:dc37b108f2a3aaa556260f93c2970028bccbbc9de36488d09513318e42777658 · ms:3644
- 2026-09-04 · bfac0e5* · exit 0 · `set -o pipefail …` · acceptance-sha256:dc37b108f2a3aaa556260f93c2970028bccbbc9de36488d09513318e42777658 · ms:3747
- 2026-09-04 · bfac0e5* · exit 0 · `set -o pipefail …` · acceptance-sha256:dc37b108f2a3aaa556260f93c2970028bccbbc9de36488d09513318e42777658 · ms:3609
- 2026-09-04 · 09ec963* · exit 0 · `set -o pipefail …` · acceptance-sha256:dc37b108f2a3aaa556260f93c2970028bccbbc9de36488d09513318e42777658 · ms:3706
