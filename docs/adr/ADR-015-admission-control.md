# ADR-015: Shed reads by withdrawing from the queue, with separate budgets and hysteresis

**Status:** Accepted
**Date:** 2026-09-04
**Owner:** M
**Spec:** None — no spec stage
**Cross-references:** `docs/adr/README.md`, `docs/adr/ADR-003-transaction-boundary.md`, `docs/adr/ADR-006-erasure-coding.md`, `docs/adr/ADR-009-fenced-leases.md`, `docs/adr/ADR-010-subscribe-and-purge.md`, `docs/adr/ADR-012-observability.md`, `docs/adr/BACKLOG.md`
**Governs:** `internal/core/admit/**`
**Enforced-by:** `internal/core/admit/admit_test.go::TestReadSheddingNeverStopsWrites`
**Invalidates:** none — checked; ADR-012 decided what is measured and explicitly left what to DO about it here
**Served-path change:** A saturating node stops receiving read work instead of failing it, and its writes continue at full rate while it sheds.

## Context

A node saturates. What it does then is a decision, and the obvious answers are
all worse than they look.

**Returning an error to the client** turns saturation into visible failure and
invites a retry, which arrives at the same node and makes it worse. **Redirecting
the client** makes every client a participant in load balancing and needs them to
agree about who is loaded. **Doing nothing** means latency climbs until something
times out, which is the same failure with less information.

The better answer is that a loaded node stops PULLING work. Reads are served by
whichever replica takes them from a shared queue, so a node that withdraws simply
stops being offered any — the client is told nothing, retries nothing, and its
request is served by a peer. Saturation becomes a routing outcome rather than an
error.

Three things have to be decided or that does not work.

⚠ **Read and write budgets must be SEPARATE.** A leaf has one writer, and if a
read burst could exhaust a shared budget it would stall that leaf's ingest.
Reads are elastic and can be served by any replica; a write has exactly one place
to go. A single budget makes a read storm into a write outage, which is the
failure that actually matters.

⚠ **The ceiling is on what saturates, and it is DECLARED.** Bandwidth, not
request count: a request count says nothing about a degraded read pulling `k`
fragments across `k` failure domains, which is ADR-006's cost and is many times a
healthy read. And it is declared by an operator rather than discovered, because a
node that measures its own ceiling learns it by exceeding it.

⚠ **Shedding must be hysteretic.** Withdrawing at the ceiling and rejoining at
the ceiling makes a node oscillate in and out of the queue at the threshold, and
the oscillation costs more than the load did — every rejoin brings a burst that
pushes it straight back over. The rejoin point must be meaningfully below the
withdraw point, and that gap is a decision rather than a tuning constant.

## Existing Primitives Audit

- `internal/core/observe` (ADR-012): supplies counters that must state their
  question, and the event vocabulary. **Reused whole.** Admission reads counters
  rather than keeping its own; two counts of one thing diverge, and ADR-012's
  follow-up asks for exactly this.
- `internal/core/subscribe` (ADR-010): supplies the subscription primitive.
  **Reused as the mechanism**: withdrawing from a queue is a subscription
  operation, not a second load-balancing system.
- `internal/core/lease` (ADR-009): supplies the fact that a leaf has one writer.
  **Relied on** for rule 1 — it is why a write cannot be shed to a peer and a
  read can.
- `internal/core/erasure` (ADR-006): supplies the reason bandwidth is the ceiling
  rather than request count. **Relied on, not called.**
- A load balancer: **none.** The queue is the balancer, and a node's only lever
  is whether it is in the group.

## Decision

**A node with a declared ceiling withdraws from the read queue when it
saturates, rejoins well below, and never sheds a write.**

1. **Two budgets, read and write, never one.** ★A read burst must not be able to
   stop a leaf's writes. Reads are elastic — any replica can serve them — and a
   write has one destination, so making them share a budget converts a read storm
   into a write outage.

2. **The ceiling is declared bandwidth, not measured and not a request count.**
   An operator states what the link is; the node states what fraction of it it
   will use. A request count would treat a healthy read and a degraded one
   pulling `k` fragments as the same thing, and they differ by the coding factor.

3. **Shedding is withdrawal from the read queue.** The node stops taking work; it
   does not refuse work it has taken, and it never tells a client anything. A
   request already accepted is completed.

4. **Withdrawal and rejoin have DIFFERENT thresholds, and the gap is part of the
   decision.** ⚠ Equal thresholds oscillate: the node rejoins at the level that
   made it leave, immediately takes a burst, and leaves again — and the flapping
   costs more than the original load. The rejoin level is meaningfully lower and
   is stated, not tuned in production.

5. **A shedding node still serves writes at full rate**, still emits events, and
   still answers a request it already accepted. Shedding removes only the
   pull of new read work.

6. **Every state change is an observable event.** A node entering or leaving the
   queue is exactly what an operator needs to see during a load incident, and
   under ADR-012 that means a declared kind with a named reader.

7. **Admission reads ADR-012's counters and keeps none of its own.** Two counts
   of one quantity diverge, and the one the operator sees will not be the one the
   shedding logic used.

**What would falsify this.** A write refused or delayed because reads saturated
the node. That is the falsifier named in `Enforced-by:` and it is checkable
today: drive read utilisation past the ceiling and assert the write budget is
untouched and writes still admit.

## Alternatives Considered

- **Return an error when saturated.** Honest, simple, and what most systems do.
  Rejected: it turns saturation into visible failure and invites a retry that
  arrives at the same node, making it worse exactly when it is least able to
  cope.
- **Redirect the client to a less loaded node.** Better than an error and uses
  ADR-008's mechanism. Rejected: it makes every client a participant in load
  balancing and requires them to agree about who is loaded, which is a consensus
  problem hiding in a redirect.
- **One budget covering reads and writes.** Simpler, and one number to tune.
  Rejected under rule 1: it makes a read burst able to stop a leaf's ingest, and
  the two have completely different elasticity.
- **A request-count ceiling.** Easy to measure, no bandwidth accounting needed.
  Rejected under rule 2: a degraded read costs `k` fragment fetches and a healthy
  one costs one, so a count treats a cluster running on parity as if it were
  healthy — which is precisely when it is not.
- **A measured ceiling, discovered by watching latency.** Adapts to real
  hardware. Rejected: a node discovers its ceiling by exceeding it, so the
  discovery IS the incident. An operator declares it instead.
- **One threshold for withdrawal and rejoin.** One number instead of two.
  Rejected under rule 4: it oscillates by construction, and the flapping costs
  more than the load.

## Component / Boundary Impact

One new component, `internal/core/admit`, owning budgets, thresholds and the
join/withdraw decision. It has one reason to change: when a node stops taking
work.

⚠ The boundary: it decides WHETHER to take work. It does not measure — ADR-012's
counters do — it does not route, and it does not refuse a write for durability
reasons, which is ADR-004's floor and a different question with a different
answer.

## Wiring & Contract Changes

| Surface | Change | Producer | Consumer(s) |
|---------|--------|----------|-------------|
| `admit.Ceiling` | new — declared bandwidth and the two thresholds | T1 | T2 |
| `admit.Budget` | new — one budget, its ceiling and its utilisation | T1 | T2 |
| `admit.Kind` (read, write) | new — the two budgets, and there is no third | T1 | T2 |
| `admit.Controller` | new — holds both budgets; reads never touch the write one | T1 | T2 |
| `admit.State` (joined, withdrawn) | new — a node's queue membership | T2 | ADR-010's queue |
| `admit.Controller.Decide` | new — hysteretic join/withdraw | T2 | callers |
| `admit.ErrNoCeiling` / `ErrThresholdsInverted` | new sentinels | T1 | callers |
| `observe.KindQueueWithdrawn` / `KindQueueRejoined` | new declared kinds, each with a reader | T2 | ADR-012's console |

## Inter-task Contracts

| Contract | Producing task | Consuming task(s) | Breaking? |
|----------|----------------|-------------------|-----------|
| `admit.Ceiling`, `admit.Budget`, `admit.Controller` | T1 | T2 | No — T2 is written against T1 |
| `admit.State`, `Decide`, the two event kinds | T2 | none yet | No |

## Implementation

Two tasks, sequential. See `docs/adr/ADR-015-admission-control/tasks/README.md`.

## Consequences

- **Positive:** Saturation becomes a routing outcome rather than an error. A
  client is never told about it and never retries into a node that cannot cope.
- **Positive:** A read storm cannot stop a leaf's writes, which is the failure
  that would otherwise turn a load spike into data not being accepted.
- **Positive:** Flapping is designed out rather than discovered, and the two
  thresholds are visible in the record instead of tuned in production.
- **Negative:** A declared ceiling can be wrong, and a node with too high a
  ceiling never sheds while a node with too low a one sheds constantly. That is
  an operator's error to make, and it is preferred to a node that learns its
  ceiling by exceeding it.
- **Negative:** Withdrawing removes capacity from the queue, so if every replica
  sheds at once the queue has nowhere to put work. Nothing here prevents that,
  and it is recorded rather than defended against.
- **Neutral:** Nothing measures bandwidth yet — the counters exist and nothing
  populates them, because that needs a transport.

## Out of Scope

- Measuring actual bandwidth, which needs a transport (deferred: `docs/adr/BACKLOG.md` §18)
- What happens when EVERY replica sheds at once (deferred: `docs/adr/BACKLOG.md` §22)
- Prioritising between classes of read — a repair versus a user query (deferred: `docs/adr/BACKLOG.md` §22)
- Refusing a write for durability reasons (permanent: boundary: ADR-004 owns the floor; it refuses because a write would not be safe, which is a different question from whether a node is busy, and conflating them would let a busy node look unsafe)
- Choosing which replica serves a read (permanent: boundary: the queue does that, and a node's only lever here is whether it is in the group)
- Measuring anything (permanent: boundary: ADR-012 owns counters and their questions; two counts of one quantity diverge, and the one an operator sees would not be the one the shedding logic used)

## Risks

| Risk | Likelihood | Impact | Mitigation |
|------|------------|--------|------------|
| A read burst stops a leaf's writes | High under one budget | Critical — a load spike becomes data not accepted | Two budgets that share nothing; the falsifier drives reads past the ceiling and asserts the write budget is untouched |
| A node oscillates in and out of the queue at the threshold | High with one threshold | High — flapping costs more than the load | Distinct withdraw and rejoin levels, with the inverted case refused at construction |
| The declared ceiling is wrong | Med | Med — constant shedding, or none | Preferred to a measured ceiling, because measuring means discovering it by exceeding it; the record says which failure it chose |
| Every replica sheds at once and the queue has nowhere to go | Low | High | Recorded as a consequence and deferred rather than defended against, because the answer needs a cluster to observe |

## Rollback

No persistent state, so a revert is a code revert. The operator-visible part is
the declared ceiling: once it exists in configuration, removing it means a node
has no shedding point and saturation returns to being a latency problem nobody
sees.

## Follow-ups

- [ ] When a transport exists (`BACKLOG.md` §18), confirm bandwidth is measured into ADR-012's counters and that admission reads THOSE rather than growing its own — two counts of one quantity diverge, and the one the operator sees would not be the one that shed.
- [ ] When ADR-010's queue is real, confirm withdrawal removes the node from the group without dropping work it has already taken; a withdrawal that abandoned in-flight reads would turn shedding back into failure.
- [ ] Decide what happens when every replica sheds (`BACKLOG.md` §22) before the first real load test, because that is when it will happen.
