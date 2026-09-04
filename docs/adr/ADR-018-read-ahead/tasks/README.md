# ADR-018 Tasks

Implementation tasks for ADR-018: Read-ahead is a budgeted hint that fetches the
nearest `k` and hedges only on a straggler. See the parent ADR for the decision.

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
| T1 | The plan — nearest k, the rest as hedge, refused when over budget | done | — | `go test ./internal/core/prefetch/... -race -run 'TestPlanFetchesExactlyK\|TestPlanChoosesTheNearestK\|TestOverBudgetPlanIsRefused\|TestTooFewFragmentsIsRefused\|TestPlanIsAValueWithNoSideEffects'` then the erasure and topology suites |
| T2 | How far ahead a budget reaches, and which reserve to try when one is late | done | — | `go test ./internal/core/prefetch/... -race -run 'TestWindowIsBoundedByBudget\|TestWindowOfZeroIsRefused\|TestHedgeIsDrawnOnlyWhenLate\|TestHedgePreservesNearestFirst\|TestHedgeExhaustionIsNamed'` |

Status: `pending` | `partial` | `blocked` | `done`.

⚠ **This record PLANS. It fetches nothing, caches nothing and evicts nothing** —
those need a transport (`BACKLOG.md` §18) and a cache (§24). Keeping the plan
separate from its execution is what makes the decision testable with no cluster.

## Contract Coupling

| Producer | Contract | Consumer(s) | Ordering note |
|----------|----------|-------------|---------------|
| T1 | `prefetch.Location`, `prefetch.Budget`, `prefetch.Plan`, `prefetch.PlanFetch` | T2 | T1 before T2 |

## Notes

- ⚠ **Fetch `k`, not `k+m`.** A stripe needs any `k` of its `k+m` fragments.
  Fetching all of them wastes `m/k` of the bandwidth on every healthy read — 25%
  at `RS(8,2)` — and that is the same link admission control sheds against, so an
  over-eager prefetch makes a node shed the reads it was trying to accelerate.
  Fetching everything is the simpler implementation and looks more robust, which
  is exactly why it is the mistake to expect.
- **The reserve is a HEDGE, not a discard.** Fetching it upfront is the waste
  above; throwing it away means one slow node stalls the block. Carrying it and
  drawing on it only when a fetch is late is the shape that avoids both.
- ⚠ **A prefetch is a HINT and correctness must never depend on it.** A plan is
  a value with no side effects and nothing to clean up, so a caller that ignores
  it is in exactly the same position as one that never asked. The moment a read
  is only correct because a prefetch succeeded, every memory-pressure event
  becomes a correctness event — and it shows up only under the load that caused
  the pressure.
- ⚠ **An over-budget plan is REFUSED, never truncated.** Fewer than `k`
  fragments cannot reconstruct, so a truncated plan spends bandwidth and delivers
  nothing — strictly worse than not prefetching, and the caller's fallback always
  works.
- **The budget is what makes "load the whole file into memory" a safe thing to
  ask for.** The answer is "as many blocks as your budget reaches": the whole
  file for a small one, a bounded prefix for a large one. The same request, two
  answers, neither of them an out-of-memory kill on a node serving other tenants.
- **Prefetch bytes count against the READ budget.** The link does not care which
  bytes are background, and excluding them would let a node shed user queries
  while its own prefetch saturated the interface.
- Choosing the nearest `k` concentrates load on near nodes — right for latency,
  wrong for balance. Nothing here spreads it and the tension is recorded rather
  than resolved (`BACKLOG.md` §24).
