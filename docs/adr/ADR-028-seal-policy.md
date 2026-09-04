# ADR-028: A tail seals on the first of a size bound or an age bound, and the exposure it leaves is reported as a worst case

**Status:** Accepted
**Date:** 2026-09-04
**Owner:** M
**Spec:** None — no spec stage
**Cross-references:** `docs/adr/README.md`, `docs/adr/ADR-004-durability-policy.md`, `docs/adr/ADR-017-lock-free-read-path.md`, `docs/adr/ADR-020-commit-point.md`, `docs/adr/ADR-024-segment-store.md`, `docs/adr/ADR-025-datom-encoding.md`, `docs/adr/ADR-026-leaf-store.md`, `docs/adr/BACKLOG.md`
**Governs:** `internal/core/leafstore/policy.go`
**Enforced-by:** `internal/core/leafstore/policy_test.go::TestExposureReportsTheOldestNotTheAverage`
**Invalidates:** none — ADR-026 made sealing an explicit operation and deliberately left the POLICY open; this is that policy, and ADR-020 left the exposure it creates unmeasured
**Served-path change:** An operator can ask a leaf how much acknowledged data is not yet on a disk and how old the oldest of it is, and can state a bound that decides when it stops being true.

## Context

Two backlog entries turn out to be the same question seen from opposite sides,
and answering one without the other leaves a number nobody can act on.

`BACKLOG.md` §15 asks **when the tail is sealed**. A size threshold, an age, a
transaction count or an operator's instruction each give a different tail length,
and the tail's length is what a reader walks.

`BACKLOG.md` §23 asks **how long acknowledged data may stay unflushed**. ADR-020
acknowledges a write once N memory replicas in distinct failure domains hold it
and flushes afterwards — deliberately, and it is the whole performance argument.
It leaves an exposure window that nothing measures.

★ **The sealing trigger IS the flush bound.** Data is exposed from the moment it
is acknowledged until the moment it is sealed into a segment, so whatever decides
sealing decides the window. Deciding them separately would produce two constants
that must be kept consistent by hand.

⚠ §23 also names the trap, and it is the sharp part of this record: **stating the
window as an average.** The number that matters is the worst case at the instant
the power goes, which correlates with load — so the exposure is largest exactly
when a correlated failure is most likely, and an average hides that completely.

## Existing Primitives Audit

- `internal/core/leafstore` (ADR-026): supplies `Store`, its in-memory tail and an
  explicit `Seal`. **Extended, not changed** — this record adds a policy that
  DECIDES, and leaves sealing exactly as explicit as ADR-026 made it.
- `internal/core/datom` (ADR-025): supplies the encoding. **Extended by one
  function**, `SizeOf`, so a size bound measures the bytes that will actually be
  written rather than an estimate of them.
- `internal/core/tx` and `hlc` (ADR-002): supply the wall reading every datom
  carries. **Reused** — the age of unsealed data is read from the datoms
  themselves, so it needs no separate bookkeeping that could disagree with them.
- A timer, a ticker, or a background goroutine: **none.** This record decides WHEN
  a seal is due and refuses to also decide who asks. A store that sealed itself on
  a timer would put a flush on a schedule nobody declared, and ADR-020's commit
  point would start depending on it.
- ADR-015's admission control: **not reached.** Sealing is not a read and does not
  pass a read budget.

## Decision

**Seal on the first of a size bound or an age bound; both must be stated; and
report the exposure as the oldest unsealed datom, never as a mean.**

1. **The policy is a PAIR, and the first bound to trip wins.** ⚠ Neither alone is
   enough, and that is why this is one decision rather than two constants. With
   size only, a quiet tenant's acknowledged write stays in memory indefinitely —
   the exposure is unbounded for exactly the tenant nobody is watching. With age
   only, a busy tenant seals whatever happened to arrive between two ticks, so
   segment sizes track the clock instead of the data.

2. **A zero bound means DISABLED, and a policy with neither bound is REFUSED.**
   ⚠ "Never seal" is a legitimate choice — a test, an import, a leaf being
   rebuilt — but it must be said out loud. A zero value that silently means never
   is what you get by forgetting to configure anything, and the failure is that
   nothing is ever durable while everything reports success.

3. **The AGE bound wins over any minimum segment size.** ⚠ They conflict: an
   age-triggered seal on a quiet leaf writes a small segment, and ADR-006 pays a
   per-stripe overhead on it. Durability beats layout, because a small segment
   costs space and an unbounded exposure costs data.

4. **Exposure is the OLDEST unsealed datom, not the average and not the newest.**
   ⚠ The falsifier. An average is smallest when the tail is fullest, because a
   burst of recent writes drags the mean down at exactly the moment the worst case
   is worst. The newest is worse still: it approaches zero as writes continue and
   reports near-perfect safety on a leaf that has been holding one acknowledged
   fact in memory for an hour.

5. **The policy DECIDES; it does not seal.** ⚠ ADR-020 fixed the commit point at
   N memory replicas. A store that sealed itself inside `Append` would put a flush
   on the write path, and the acknowledged latency would change without the record
   that fixed it changing.

6. **Size is measured as the ENCODED size, from ADR-025.** ★ A bound in bytes
   should count the bytes that will be written. Counting values and ignoring the
   74-byte fixed part would under-report a tail of many small facts by an order of
   magnitude, which is exactly the shape a busy tenant has.

7. **Sealing MOVES data; it does not drop any.** ★ A correction to §15, which
   framed sealing as two publications — the segment becoming readable and the tail
   entries becoming redundant — and worried about a reader holding a snapshot from
   before the swap. That worry does not arise here: ADR-026 merges by transaction
   over segments AND tail, so moving a datom between them does not change the
   merged set, and ADR-026 already clears the tail under the same hold that
   publishes. ⚠ The concern is real but it belongs to COMPACTION, which does drop
   things, and it is re-deferred there rather than answered twice.

**What would falsify this.** A leaf reporting a small exposure while holding an
old acknowledged datom. That is the falsifier in `Enforced-by:`, it needs one
tail and no cluster, and it is what both obvious implementations — the mean and
the most recent — produce.

## Alternatives Considered

- **A transaction-count threshold.** Simple, and trivially observable. Rejected
  under rule 6: a tail of one large datom and a tail of a thousand tiny ones count
  the same, while the cost a reader pays and the cost a stripe pays are both in
  bytes. It would make segment size a function of write shape rather than of
  anything decided.
- **Size alone.** It is what a storage engine usually does, and it makes every
  segment the same size. Rejected under rule 1: a quiet tenant never reaches the
  threshold, so its acknowledged writes stay in memory without bound — and it is
  the tenant generating no load whose exposure nobody notices.
- **Age alone.** It bounds the exposure directly, which is the thing §23 asks for.
  Rejected under rule 1 for the mirror reason: segment sizes then track the clock,
  so a burst produces one enormous segment and a lull produces empty ones.
- **Seal automatically inside `Append` once the bound trips.** Fewer moving parts
  and nothing to forget to call. Rejected under rule 5: it puts a flush on the
  write path, so the acknowledged latency acquires a periodic spike that ADR-020's
  argument does not account for — and the commit point moves as a side effect of a
  layout decision.
- **Report the exposure as a mean, or as a moving average.** It is the number a
  dashboard usually shows, and it is stable. Rejected under rule 4: it is smallest
  when the risk is largest, because recent writes drag it down precisely when the
  tail is fullest.
- **Report a histogram or percentiles.** Strictly more information. Rejected as
  premature rather than wrong: percentiles need a retention and a bucketing
  decision, and the one number an operator needs during a power event is the
  worst case. Recorded as a follow-up rather than guessed at now.
- **A minimum segment size that can defer an age-triggered seal.** It would stop
  a quiet leaf writing tiny segments. Rejected under rule 3: it makes the
  exposure bound conditional on write volume, which is the guarantee being asked
  for.

## Component / Boundary Impact

No new component. `internal/core/leafstore` gains a policy and a measurement, both
of which read the tail it already owns. ⚠ It gains no timer, no goroutine and no
call to `Seal` — the boundary is that this decides WHETHER a seal is due and never
performs one.

`internal/core/datom` gains one function that reports the size of what it would
encode.

## Wiring & Contract Changes

| Surface | Change | Producer | Consumer(s) |
|---------|--------|----------|-------------|
| `leafstore.Policy` | new — the size and age bounds | T1 | callers |
| `leafstore.Store.ShouldSeal` | new — whether a seal is due under a policy | T1 | callers |
| `leafstore.Exposure` / `leafstore.Store.Exposure` | new — acknowledged and not yet on a disk | T1 | callers, operators |
| `leafstore.ErrNoBound` | new sentinel — a policy that would never seal | T1 | callers |
| `datom.SizeOf` | new — the encoded size of one datom | T1 | T1 |

## Inter-task Contracts

| Contract | Producing task | Consuming task(s) | Breaking? |
|----------|----------------|-------------------|-----------|
| `leafstore.Policy`, `leafstore.Exposure` | T1 | a caller that seals on a schedule (`BACKLOG.md` §15) | No |

## Consequences

- **Positive:** `BACKLOG.md` §23 closes. The exposure window has a stated bound
  and a number an operator can read, and the number is the one that matters.
- **Positive:** §15's "when sealing happens" is answered, and answered once —
  the flush bound and the seal trigger are the same decision rather than two
  constants somebody has to keep consistent.
- **Positive:** A tail is bounded, so ADR-026's linear walk of the tail is bounded
  too. What remains unbounded is the number of SEGMENTS, which is compaction.
- **Negative:** An age-triggered seal on a quiet leaf writes a small segment, and
  ADR-006 pays per-stripe overhead on it. That is rule 3 choosing durability over
  layout, and it is the cost of the guarantee rather than an oversight.
- **Negative:** Nothing calls `ShouldSeal` yet. The policy is decidable and
  measurable and no scheduler consults it, which is stated rather than implied —
  see the task's Reachability.
- **Neutral:** No timer, no goroutine, no background work. Who asks is a
  deployment decision this record deliberately does not take.

## Out of Scope

- Compaction, and the reader-visible ordering it genuinely does require (deferred: `docs/adr/BACKLOG.md` §15)
- An index over the tail or over a leaf's segments (deferred: `docs/adr/BACKLOG.md` §15)
- Who calls `ShouldSeal`, and on what schedule (deferred: `docs/adr/BACKLOG.md` §15)
- Percentiles or a histogram of the exposure (deferred: `docs/adr/BACKLOG.md` §23)
- Re-coding a sealed segment when the durability tier changes (deferred: `docs/adr/BACKLOG.md` §14)
- Moving the commit point (permanent: boundary: ADR-020 fixed it at N memory replicas in distinct failure domains, and a sealing policy that changed it would change the latency contract without a record)
- Deciding the actual numbers for a deployment (permanent: boundary: a threshold is valid for a configuration and never in the abstract, so this record fixes the SHAPE of the pair and refuses to invent values for a cluster nobody has measured)

## Risks

| Risk | Likelihood | Impact | Mitigation |
|------|------------|--------|------------|
| The exposure is reported as an average | High — it is the number a dashboard shows | High — it is smallest exactly when the tail is fullest, so it under-reports at the moment a correlated failure is most likely | Rule 4, and the falsifier holds an old datom beside new ones |
| A policy with no bounds is accepted and never seals | High — it is the zero value | Critical — nothing is ever durable and everything reports success | Rule 2, with a named refusal |
| Sealing is done inside `Append` | Med — it removes a call the caller must remember | High — a flush lands on the write path and the acknowledged latency changes without ADR-020 changing | Rule 5; `ShouldSeal` returns a bool and touches nothing |
| The size bound counts only values | Med — it is the obvious measurement | Med — a tail of many small facts is under-reported by an order of magnitude, which is exactly a busy tenant's shape | Rule 6, with `datom.SizeOf` asserted against the encoder |
| A minimum segment size is added later and defers the age bound | Med — small segments look wasteful | High — the exposure bound becomes conditional on write volume | Rule 3, stated as an ordering between the two |

## Rollback

The policy is advisory and nothing consults it, so reverting removes a decision
rather than a behaviour. ⚠ That stops being true the moment something seals on a
schedule, because the pair then defines the durability window — which is why the
shape is settled now, while it is still free.

## Follow-ups

- [ ] When something seals on a schedule (`BACKLOG.md` §15), confirm it reports the exposure it observed at each tick rather than at the end — a bound that is only checked when it is already satisfied measures nothing.
- [ ] When erasure coding reaches sealed segments (`BACKLOG.md` §12), re-read rule 3: sealing becomes the moment data moves from a replicated tier to a coded one, so the age bound becomes the bound on how long data sits at the more expensive tier, and the small-segment cost gets a second, larger term.
- [ ] Revisit percentiles (`BACKLOG.md` §23) once a real deployment produces a distribution; the worst case is the number for an incident and a distribution is the number for capacity, and this record deliberately only answers the first.
