# ADR-018: Read-ahead is a budgeted hint that fetches the nearest k and hedges only on a straggler

**Status:** Accepted
**Date:** 2026-09-04
**Owner:** M
**Spec:** None — no spec stage
**Cross-references:** `docs/adr/README.md`, `docs/adr/ADR-001-address-space.md`, `docs/adr/ADR-006-erasure-coding.md`, `docs/adr/ADR-015-admission-control.md`, `docs/adr/ADR-017-lock-free-read-path.md`, `docs/adr/BACKLOG.md`
**Governs:** `internal/core/prefetch/**`
**Enforced-by:** `internal/core/prefetch/prefetch_test.go::TestPlanFetchesExactlyKNotKPlusM`
**Invalidates:** none — checked; ADR-006 fixed what a stripe is and left which fragments to fetch, and in what order, entirely open
**Served-path change:** A sequential read of a large blob pulls the nearest `k` fragments per block ahead of the reader and streams from memory, instead of fetching each block on demand at `k` round trips.

## Context

Reading one block of a coded stripe costs `k` fragment fetches across `k`
failure domains — ADR-006's price for `(k+m)/k` storage instead of 3×. On a
sequential read of a large blob that cost is paid per block, serially, and the
reader waits for the slowest of `k` remote fetches every time.

Read-ahead fixes the serial part: fetch the next blocks while the caller is
still consuming this one, and serve from memory. Three things make it a decision
rather than a cache.

⚠ **It is a HINT, and correctness must never depend on it.** A prefetch that
failed, was evicted or never ran must be indistinguishable from one that never
happened. The moment a read is only correct because a prefetch succeeded, every
memory-pressure event becomes a correctness event.

⚠ **It must be BUDGETED.** "Load every part of the file into memory" is exactly
right for a 40MB blob and fatal for a 4TB one — the same instruction, and the
second one is an out-of-memory kill on a node that was serving other tenants
fine. A prefetch without a ceiling is a denial of service with good intentions.

⚠ **And it must fetch `k`, not `k+m`.** A stripe has `k+m` fragments and needs
any `k`. Fetching all of them wastes `m/k` of the bandwidth on every healthy
read — 25% at `RS(8,2)` — and that waste lands on the same link ADR-015 is
trying to keep under its ceiling. But fetching exactly `k` means one slow node
stalls the block, so the remaining `m` are a HEDGE: requested only when a fetch
is late, not upfront.

## Existing Primitives Audit

- `internal/core/erasure` (ADR-006): supplies `StripeHeader` with `k` and `m`,
  and the rule that any `k` verified fragments reconstruct. **Reused whole** —
  the plan's size comes from the stripe rather than from a second notion of how
  many fragments a read needs.
- `internal/core/topology` (ADR-001): supplies `Distance`. **Reused whole** to
  order candidates by nearness; a second distance metric here would disagree
  with placement's.
- `internal/core/placement` (ADR-001): supplies `Nearest`. **Reused rather than
  reimplemented**, so "near" means the same thing to a prefetch as it does to a
  client choosing a replica.
- `internal/core/admit` (ADR-015): supplies the read budget. **Relied on**: a
  prefetch is read work and must be counted as such, or shedding is computed
  against a number that excludes the largest consumer of the link.
- A cache library: **none.** The decision here is which fragments to ask for and
  whether the ask is allowed; eviction policy is a separate question and is
  deferred rather than bundled.

## Decision

**A prefetch plan names the nearest `k` fragments, holds `m` in reserve, and is
refused rather than truncated when it exceeds its budget.**

1. **A plan fetches exactly `k` fragments per stripe.** ★Fetching `k+m` wastes
   `m/k` of the bandwidth on every healthy read, and that bandwidth is the same
   resource ADR-015 sheds against — so an over-eager prefetch makes a node shed
   the work it was trying to accelerate.

2. **The `k` are the NEAREST `k`, by the topology's own distance.** Any `k`
   reconstruct, so which `k` is free to be chosen well, and nearness is what
   makes a degraded read cheaper rather than merely possible.

3. **The remaining `m` are a HEDGE, requested only when a fetch is late.**
   Fetching them upfront is rule 1's waste; never fetching them means one slow
   node stalls the block. The hedge is the shape that avoids both, and it is why
   the plan carries them rather than discarding them.

4. **A plan that exceeds its budget is REFUSED, not truncated.** ⚠ A truncated
   plan is worse than no plan: fewer than `k` fragments cannot reconstruct, so
   the work is spent and the block still has to be fetched properly. Refusing
   returns the caller to the un-prefetched path, which always works.

5. **The budget is bytes, declared, and per read.** "Every part of the file"
   is not a budget. A caller says how much memory this read may use, and the
   plan says how far ahead that reaches.

6. **A prefetch is advisory in the strongest sense: nothing reads its result
   without also being able to fetch normally.** A missing prefetch is a slower
   read, never a failed one, and there is no state a caller must clean up.

7. **Prefetch bytes count against the read budget.** A prefetch is read work. Not
   counting it would compute shedding against a number excluding the biggest
   consumer of the link, and the node would shed user queries while its own
   prefetch saturated the interface.

**What would falsify this.** A plan that fetches `k+m` fragments on a healthy
stripe. That is the falsifier named in `Enforced-by:`, it is checkable today with
no transport, and it is the mistake a reasonable implementation makes — fetching
everything is simpler and looks more robust.

## Alternatives Considered

- **Fetch all `k+m` fragments and use whichever arrive first.** Simplest, most
  robust to a slow node, and what a naive implementation does. Rejected under
  rule 1: it wastes `m/k` of the bandwidth on every healthy read, and that
  bandwidth is exactly what ADR-015 sheds against — so the prefetch would make a
  node shed the reads it was accelerating.
- **Fetch exactly `k` and wait, with no hedge.** Minimum bandwidth. Rejected
  under rule 3: one slow node then stalls the block, and at planetary scale one
  of any `k` nodes being slow is the normal case rather than the exception.
- **Truncate a plan that exceeds its budget.** Uses whatever budget there is.
  Rejected under rule 4: fewer than `k` fragments cannot reconstruct, so a
  truncated plan spends bandwidth and delivers nothing — strictly worse than
  not prefetching.
- **Prefetch the whole blob, as asked.** Exactly what was requested, and right
  for a small file. Rejected as a general rule under rule 5: the same
  instruction is an out-of-memory kill on a large one, on a node serving other
  tenants.
- **Make the prefetch authoritative — a read waits for it.** Removes a
  duplicate fetch path. Rejected under rule 6: it makes every memory-pressure
  event a correctness event, and the failure appears only under the load that
  caused the pressure.
- **Exclude prefetch bytes from the read budget, since it is "background".**
  Keeps user-visible latency out of the shedding decision. Rejected under rule
  7: the link does not care which bytes are background, and a node would shed
  user queries while its own prefetch saturated the interface.

## Component / Boundary Impact

One new component, `internal/core/prefetch`, owning the plan. It has one reason
to change: which fragments a read should ask for, and whether it may.

⚠ The boundary: it PLANS. It fetches nothing, holds nothing in memory, and
evicts nothing — those need a transport and a cache, and neither exists. Keeping
the plan separate from its execution is what makes the decision testable with no
cluster.

## Wiring & Contract Changes

| Surface | Change | Producer | Consumer(s) |
|---------|--------|----------|-------------|
| `prefetch.Location` | new — a fragment index and the node holding it | T1 | T2 |
| `prefetch.Budget` | new — declared bytes for one read | T1 | T2 |
| `prefetch.Plan` | new — the `k` to fetch, the `m` held as hedge, and the bytes | T1 | callers |
| `prefetch.PlanFetch` | new — nearest `k`, hedge the rest, refuse over budget | T1 | callers |
| `prefetch.ErrOverBudget` / `ErrTooFewFragments` | new sentinels | T1 | callers |
| `prefetch.Window` | new — how many blocks ahead a budget reaches | T2 | callers |
| `prefetch.Hedge` | new — which reserve fragment to try when one is late | T2 | callers |

## Inter-task Contracts

| Contract | Producing task | Consuming task(s) | Breaking? |
|----------|----------------|-------------------|-----------|
| `prefetch.Location`, `prefetch.Budget`, `prefetch.Plan`, `prefetch.PlanFetch` | T1 | T2 | No — T2 is written against T1 |
| `prefetch.Window`, `prefetch.Hedge` | T2 | none yet | No |

## Consequences

- **Positive:** A sequential read stops paying `k` serial round trips per block,
  which is the difference between streaming and stepping.
- **Positive:** Bandwidth per healthy read is `k` fragments rather than `k+m`, so
  the prefetch does not compete with itself through ADR-015's ceiling.
- **Positive:** A refused plan degrades to the ordinary read path, which always
  works — so the worst case of the whole feature is "no faster than before".
- **Negative:** A hedge means some reads fetch more than `k`, and that extra is
  spent exactly when the cluster is already slow. It is bounded by `m` per stripe
  and is the price of not stalling on one node.
- **Negative:** Choosing the nearest `k` concentrates load on near nodes, which
  is right for latency and wrong for balance. Nothing here spreads it, and the
  tension is real.
- **Neutral:** Nothing fetches, caches or evicts. The plan is decidable and its
  execution is not.

## Out of Scope

- Actually fetching anything (deferred: `docs/adr/BACKLOG.md` §18)
- Holding fetched blocks in memory, and evicting them (deferred: `docs/adr/BACKLOG.md` §24)
- Detecting that a fetch is late, which needs a transport with timings (deferred: `docs/adr/BACKLOG.md` §18)
- Deciding whether a read is sequential enough to prefetch at all (deferred: `docs/adr/BACKLOG.md` §24)
- Balancing load across nodes rather than choosing the nearest (deferred: `docs/adr/BACKLOG.md` §24)
- Where fragments are (permanent: boundary: ADR-004's policy and the placement service decide that; this record takes their locations as given and chooses among them)

## Risks

| Risk | Likelihood | Impact | Mitigation |
|------|------------|--------|------------|
| A plan fetches `k+m`, wasting `m/k` of the link on every healthy read | High — it is the simpler implementation and looks more robust | High — the prefetch competes with itself through ADR-015's ceiling | The falsifier asserts the plan's fetch list is exactly `k`, with the remainder in the hedge list rather than absent |
| A plan is truncated to fit its budget and delivers fewer than `k` | Med | High — bandwidth spent for nothing, strictly worse than not prefetching | Over-budget is a named refusal; the test asserts no partial plan is ever returned |
| A read becomes dependent on a prefetch having succeeded | Med | Critical — every memory-pressure event becomes a correctness event | The plan is a value with no side effects and nothing to clean up; a caller that ignores it is in the same position as one that never asked |
| Prefetch bytes are excluded from the read budget as "background" | Med | High — a node sheds user queries while its own prefetch saturates the link | Rule 7 and a follow-up against ADR-015; the link does not care which bytes are background |

## Rollback

No persistent state and no format, so a revert is a code revert. Because the plan
is advisory, removing it degrades performance and nothing else — which is the
same property that makes rule 6 worth insisting on.

## Follow-ups

- [ ] When a transport exists (`BACKLOG.md` §18), confirm prefetch bytes are counted into ADR-015's READ budget — a node that sheds user queries while its own prefetch saturates the link is the specific failure rule 7 exists to prevent.
- [ ] When a cache exists (`BACKLOG.md` §24), confirm a read still works with every prefetched block evicted; if it does not, rule 6 has been lost and the loss will only show under memory pressure.
- [ ] Measure whether hedging on a straggler actually helps before tuning when to hedge (`BACKLOG.md` §16 owns the degraded-read measurement); the hedge is reasoned rather than measured, and it costs bandwidth exactly when the cluster is already slow.
