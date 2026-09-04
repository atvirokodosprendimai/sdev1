# ADR-002: Identify a transaction by a hybrid-logical-clock triple and query the two time axes independently

**Status:** Accepted
**Date:** 2026-09-04
**Owner:** M
**Spec:** None — no spec stage
**Cross-references:** `docs/adr/README.md`, `docs/adr/ADR-001-address-space.md`, `docs/adr/BACKLOG.md`
**Governs:** `internal/core/hlc/**`, `internal/core/tx/**`, `internal/core/temporal/**`
**Enforced-by:** `internal/core/temporal/temporal_test.go::TestLoneInstantBindsValidTimeOnly`
**Invalidates:** none — checked; ADR-001 decides addressing and says nothing about time
**Served-path change:** A time-travel query spanning more than one leaf returns a single coherent answer, because every datom carries a transaction identifier that is totally ordered across the whole cluster rather than only within its own leaf.

## Context

ADR-001 fixed how a key names a leaf. This record fixes how a write names a
moment. Both are encoded in every stored datom and neither is changeable
afterwards, which is why they precede the storage format.

Two facts about the requirement make the naive design wrong.

**First, `T` in EAVT is not a local counter once the system is distributed.** A
per-leaf sequence is monotonic within its leaf and says nothing about any other,
so `AS OF <t>` spanning leaves has no meaning: there is no relation that orders a
datom in leaf `0x3A` against a datom in leaf `0xF1`. The defect does not appear in
a single-node prototype, appears in every multi-leaf query, and is discovered
after data exists — which is the worst possible time, because `T` is written into
every datom ever stored.

**Second, and measured rather than reasoned:** the predecessor project in this
workspace, `temporaldbv1`, shipped exactly the two-axis defect this record exists
to prevent. Recorded 2026-09-03 in that project's own notes: its `AS OF <time>`
clause passed **the same instant** to both of its visibility parameters — `asOf`
(the transaction-time cutoff) and `validAt` (the business-time point). A write
made with `PUT ... AT <past date>` commits *now* but is valid *from the past*, so
`asOf = validAt = <past date>` excluded the write entirely, because its real
transaction time is after that cutoff. `GET ... AS OF <that past date>` returned
nothing instead of the backdated value.

The reason no test caught it is the part worth carrying: **every existing test
happened to write with `valid_from == tx_time`** — none used the backdating clause
to make the axes diverge — so the two parameters were never actually different in
any test, and roughly 140 tests including `-race` stayed green throughout. The
bug had structurally no test that could see it, however many tests existed.

## Existing Primitives Audit

- `internal/core/addr` (ADR-001, T1): supplies `LeafID`, which this record embeds
  in the transaction identifier so that a `T` names the leaf that minted it.
  **Reused as-is**; no change to ADR-001 is implied.
- Go standard library `time`: supplies the wall-clock reading an HLC needs.
  **Reused**, with the explicit caveat that a wall clock is an input to the
  algorithm and never the ordering itself.
- No third-party clock or ordering library is adopted. Hybrid logical clocks are
  roughly forty lines and the correctness argument is short enough to hold in
  one file; a dependency here would be larger than the thing it replaced.

## Decision

**A transaction identifier is the triple `TxID{HLC, LeafID, Seq}`, totally ordered
cluster-wide and strictly monotonic per leaf. A datom carries two independent time
axes, and a query qualifies them separately.**

1. **The clock is a hybrid logical clock.** Each node keeps `(wall, logical)`.
   On a local event, `wall = max(now, wall)` and `logical` increments when `wall`
   did not advance. On receiving a message carrying a remote HLC, both are merged
   forward. The result stays close to wall-clock time, never goes backwards, and
   orders causally related events correctly without any special hardware.
2. **`TxID` orders by `HLC`, then `LeafID`, then `Seq`.** The tie-breakers exist
   so the order is *total* rather than merely partial — two leaves can mint the
   same HLC reading, and a total order is what makes a cross-leaf `AS OF` a
   well-defined question at all.
3. **A datom carries `ValidFrom`, `ValidTo` and `TxID`.** `ValidFrom`/`ValidTo`
   are the business axis and are supplied by the writer. `TxID` is the system
   axis and is never supplied by the writer.
4. **A query carries two independent qualifiers**, `asOf` (transaction time) and
   `validAt` (business time), and either may be omitted.
5. ★**A single user-supplied instant binds to VALID time and leaves transaction
   time open.** This is the rule the predecessor project got wrong, and it is
   stated as a rule rather than left to a default because the wrong behaviour is
   the one a reasonable implementer writes.
6. **Defaults, stated as a table rather than as prose**, because a default that
   lives only in a sentence is a default nobody can check:

   | The caller wrote | `asOf` becomes | `validAt` becomes |
   |------------------|----------------|-------------------|
   | nothing | latest | now |
   | `AS OF t` | latest | `t` |
   | `AS OF t TRANSACTION u` | `u` | `t` |
   | `TRANSACTION u` | `u` | now |

**What would falsify this decision.** The claim carrying weight is rule 5 plus
the table: that binding a lone instant to valid time alone is correct, and binding
it to both is the defect. It is falsifiable today, on one node, with no cluster:
write a datom with `ValidFrom` in the past and a `TxID` minted now, then query at
that past instant. Under rule 5 the datom is returned; under the rejected
behaviour it is not. `TestLoneInstantBindsValidTimeOnly` is that probe, and it is
named in `Enforced-by:` so the check is reachable from this record.

**The criterion's validity.** The table above is valid for a query language with
exactly two time qualifiers. If ADR-011 introduces a third temporal notion —
decision time, or a user-visible ingestion time — this table is incomplete rather
than wrong, and this record must be revisited rather than quietly extended.

## Alternatives Considered

- **A per-leaf monotonic counter.** Simplest, needs no clock at all, and is
  correct within one leaf. Rejected because it makes cross-leaf time travel
  undefined, which is not a limitation but a silently wrong answer: a query
  spanning leaves returns *some* result, ordered by nothing, and looks fine.

- **TrueTime-style commit-wait (Spanner).** Bounds clock uncertainty with
  GPS and atomic clocks, then waits out the uncertainty window before
  acknowledging a commit, which buys external consistency. Rejected because it
  requires hardware this system cannot assume, and because the wait is added to
  every commit's latency — paying that for a guarantee stronger than ADR-003
  intends to promise.

- **Physical timestamps alone (NTP wall clock).** What most systems reach for.
  Rejected because NTP offers no monotonicity guarantee: a clock correction moves
  time backwards, two datoms then carry the same or inverted timestamps, and the
  event log — which is append-only and permanent — records an order that never
  happened.

- **Vector clocks.** Correct causality without any wall clock. Rejected because
  a vector clock grows with the number of participants, and with an unbounded
  number of leaves the identifier stops being a small fixed-size value that can
  sit in every datom's header. The cost lands in the storage format, which is
  the one place this system cannot afford variable-width identity.

- **One time axis (transaction time only).** Halves the design. Rejected because
  the requirement is explicitly bitemporal, and because the ability to record
  *when a fact was true* separately from *when we learned it* is what
  distinguishes a correction from a change — the distinction ADR-007's erasure
  model and any audit story both rest on.

## Component / Boundary Impact

| Component | Owns | One reason to change? |
|-----------|------|-----------------------|
| `internal/core/hlc` | The clock: `(wall, logical)`, local tick, remote merge. No I/O, no transport. | Yes — changes only if the clock algorithm changes. |
| `internal/core/tx` | `TxID`, its total order, and its fixed-width encoding. | Yes — changes only if transaction identity changes. |
| `internal/core/temporal` | `Interval`, and the `Visible(datom, asOf, validAt)` predicate that is the single place both axes are compared. | Yes — changes only if temporal semantics change. |

`hlc` depends on the standard library alone. `tx` depends on `hlc` and on
`addr` (ADR-001). `temporal` depends on `tx`. None of the three touches storage,
network or configuration, so the entire temporal model is testable with no
cluster, no disk and no real clock.

★**`Visible` is deliberately the only comparison site.** The predecessor project's
defect was a caller passing one value into two parameters; concentrating the
comparison in one predicate is what makes that mistake reviewable in one place
rather than at every call site.

## Wiring & Contract Changes

| Surface | Change | Producer | Consumer(s) |
|---------|--------|----------|-------------|
| `hlc.Clock`, `hlc.Timestamp`, `Now()`, `Merge()` | new | `internal/core/hlc` (T1) | `tx` (T2); every node's commit path |
| `tx.TxID` and its total order | new | `internal/core/tx` (T2) | `temporal` (T3); segment headers (ADR-005); the log (ADR-009) |
| `tx.TxID` fixed-width binary encoding | new | `internal/core/tx` (T2) | ADR-005's segment format |
| `temporal.Interval`, `temporal.Visible()` | new | `internal/core/temporal` (T3) | ADR-011's query evaluator |
| The qualifier-defaults table (rule 6) | new | `internal/core/temporal` (T3) | ADR-011, which must implement exactly this table |

## Inter-task Contracts

| Contract | Producing task | Consuming task(s) | Breaking? |
|----------|----------------|-------------------|-----------|
| `hlc.Timestamp`, `hlc.Clock` | T1 | T2 | No — new surface |
| `tx.TxID`, its ordering and encoding | T2 | T3, T4 | No — new surface |
| `temporal.Visible()`, `temporal.Interval` | T3 | T4 | No — new surface |

## Implementation

Four tasks. See [`ADR-002-transaction-identity/tasks/README.md`](ADR-002-transaction-identity/tasks/README.md).

## Consequences

- **Positive:** a cross-leaf `AS OF` is a well-defined question, and the answer is
  the same from any node, because the order is total and does not depend on who
  is asking.
- **Positive:** no special hardware. A hybrid logical clock needs only a
  reasonably-synchronised wall clock, and degrades to a logical clock — still
  correct, merely further from wall time — when synchronisation is poor.
- **Negative:** `TxID` is 16 bytes in every datom. At datom scale that is the
  single largest fixed overhead this design imposes, and ADR-005 must earn it
  back by interning the other fields.
- **Negative:** an HLC reading can drift ahead of true wall time when a node's
  clock jumps forward, and it never comes back. The drift is bounded by the worst
  clock in the cluster, so a badly-skewed node degrades everyone's timestamps
  toward that node's reading, permanently.
- **Neutral:** two independent qualifiers make some queries more verbose than a
  single-time system would. That verbosity is the feature — it is what makes the
  axes visibly separate at the call site.

## Out of Scope

- The syntax a user writes to express the two qualifiers (permanent: boundary: ADR-011 owns the query language; this record fixes the semantics the syntax must express, and the defaults table is binding on it)
- Refusing a write whose `ValidFrom` is absurdly far in the future or the past (permanent: boundary: a validity window is the writer's business statement, and this record deliberately declines to police the domain's own semantics)
- Bounding clock skew between nodes, and what to do about a node whose clock is badly wrong (deferred: `docs/adr/BACKLOG.md` §4)
- Closed timestamps, which bounded-staleness follower reads require (deferred: `docs/adr/BACKLOG.md` §5)
- The 16-byte identifier's storage cost, and whether it can be delta-encoded within a segment (permanent: fact: a datom's transaction identifier must be comparable without decoding neighbouring datoms, so any delta scheme is a segment-format concern rather than an identity one; citation: file `docs/adr/ADR-002-transaction-identity.md:1`)

## Risks

| Risk | Likelihood | Impact | Mitigation |
|------|------------|--------|------------|
| A caller passes one instant into both `Visible` parameters — the exact defect the predecessor shipped | **High** | High — silently wrong time-travel results, invisible to a green suite | Rule 5 and the defaults table are binding; `TestLoneInstantBindsValidTimeOnly` is the falsifier; `Visible` is the single comparison site so review has one place to look |
| Tests are written that never diverge the two axes, so the suite is green and proves nothing | **High** | High — this is precisely how the predecessor's ~140 green tests missed it | T4 exists solely to generate divergent-axis cases by property test rather than by hand; a hand-written fixture encodes what the author expected and cannot falsify the expectation |
| A node with a badly-skewed clock drags cluster timestamps forward permanently | Medium | Medium | Recorded as a Negative consequence; the mitigation (skew bounds and eviction) is deferred to `BACKLOG.md` §4 rather than pretended at |
| 16 bytes per datom proves too expensive at real scale | Medium | Medium | Named as the largest fixed overhead; ADR-005 owns earning it back, and the cost is stated here so that record inherits a number rather than a feeling |

## Rollback

This governs persistent state — `TxID` is written into every datom — so rollback
is a migration once data exists, not a revert.

**Before any data is written** (the state at authoring): revert the branch. The
three packages have no callers.

**After data exists**, changing transaction identity means rewriting every datom,
which is a full re-ingest. The mitigation is that the two decisions most likely to
change are *not* the identity: the defaults table (rule 6) is query-time and may be
corrected without touching stored bytes, and the clock's skew handling is
node-local. Only the triple's shape is permanent, which is why it is deliberately
the smallest part of this record.

## Follow-ups

- [ ] When ADR-011 is authored, verify its qualifier syntax implements rule 6's table exactly, and that a lone instant reaches `validAt` and not `asOf`.
- [ ] When ADR-005 is authored, confirm `tx.TxID`'s encoding is fixed-width and comparable as bytes, so a segment index can order on it without decoding.
