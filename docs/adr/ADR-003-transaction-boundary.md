# ADR-003: Make the entity the transaction boundary, and give the write path its own reads

**Status:** Accepted
**Date:** 2026-09-04
**Owner:** M
**Spec:** None — no spec stage
**Cross-references:** `docs/adr/README.md`, `docs/adr/ADR-001-address-space.md`, `docs/adr/ADR-002-transaction-identity.md`, `docs/adr/BACKLOG.md`
**Governs:** `internal/core/ports/**`, `internal/core/command/**`
**Enforced-by:** `internal/core/ports/ports_test.go::TestReadModelCannotWrite`
**Invalidates:** none — checked; ADR-001 decides addressing and ADR-002 decides identity, and neither states a consistency level
**Served-path change:** A write touching one entity commits without any cross-node agreement beyond that entity's own replicas, and a reader is told which consistency it is getting rather than having to assume.

## Context

ADR-001 made everything about one entity resolve to one leaf. ADR-002 gave every
transaction an identifier ordered across the whole cluster. Neither states what a
reader is promised, and that promise decides whether distributed commit is needed
at all — which is the most expensive thing this system could accidentally
require.

Two facts constrain the choice.

**The entity is already the unit of locality.** A key hashes to one leaf, so a
transaction confined to one entity is confined to one leaf, and a leaf has one
writer. Such a transaction needs no agreement with any other leaf. The moment a
transaction may span entities, it may span leaves, and every commit becomes a
distributed one.

**A reference attribute makes spanning trivial to do by accident.** Nothing in
the datom model distinguishes "a value" from "a pointer to another entity", so a
write that updates two entities together is one line of caller code. If that is
permitted, it is permitted everywhere, and the cost lands on every commit rather
than on the ones that asked for it.

There is also a standing requirement about scale that shapes the read side:
reads and writes must scale independently, and the write path must not read
through the read models. Those are two separate claims and both are decided
here, because both are properties of the boundary rather than of any component.

## Existing Primitives Audit

- `internal/core/addr` (ADR-001): supplies the leaf a key resolves to, which is
  what makes "one entity, one writer" true rather than aspirational. **Reused
  as-is.**
- `internal/core/tx` (ADR-002): supplies the identifier a commit carries and the
  total order two entities' writes are compared by. **Reused as-is.**
- `internal/core/temporal` (ADR-002): supplies the visibility predicate a
  snapshot is evaluated with. **Reused as-is** — a snapshot is a transaction
  identifier plus that predicate, not a new mechanism.
- No storage, transport or consensus package exists yet, so this record defines
  the ports those will implement rather than adapting anything.

## Decision

**A transaction touches exactly one entity. Within an entity, operations are
linearizable. Across entities, a reader sees a consistent snapshot and nothing
stronger.**

1. **One entity per transaction, refused rather than discouraged.** A command
   naming a second entity is rejected with a named error at construction time,
   before anything is written. This is the whole reason the system needs no
   distributed commit, so it is a refusal and not a convention.
2. **Linearizable within an entity.** One writer per leaf, and a read of that
   entity through its leader observes every write that completed before it
   began.
3. **Snapshot isolation across entities.** A reader takes a transaction
   identifier and sees every entity as of that point. Two entities read under
   one snapshot are mutually consistent; they are not guaranteed to reflect a
   single instant of wall time, because no such instant exists across
   independent leaves.
4. ★**The write path never reads a read model.** Command validation — does this
   entity exist, what is the current value of this attribute, is this assertion
   a duplicate — is answered from the writer's own state on the leader. A write
   that consults a projection has made its correctness depend on a component
   that is eventually consistent and separately scaled, which reintroduces
   exactly the coupling the split removes.
5. **There are therefore TWO INDEX FAMILIES, and this is a structural
   commitment rather than a restatement.** The writer's index is minimal and
   leader-local, carrying only what validation needs. The read models' indexes
   are rich, shaped per query, and live wherever read replicas live. Both are
   built from the same log; they are not the same artifact.
6. **Read capacity and write capacity are configured separately.** Adding a
   read replica must not change the write quorum, slow a commit, or require a
   consensus reconfiguration.
7. **The asymmetry is carried by the type system, not by prose.** A read model
   is handed a port that has no write method, so it cannot write. A rule that
   lives only in a document is a rule that holds until somebody is in a hurry.

**What would falsify this decision.** The load-bearing claim is rule 1: that
confining a transaction to one entity removes the need for distributed commit.
It fails if a legitimate domain operation cannot be expressed within one entity —
if, for example, a transfer of value between two entities turns out to be
required and cannot be modelled as two transactions plus a compensating one.
That is a domain question this record cannot settle alone, and the honest
position is that it has not been tested against a real domain yet. Nothing here
is falsifiable by a unit test; what IS falsifiable, and is tested, is that the
refusal actually fires and that a read model cannot write.

**Validity.** Rules 2 and 3 describe what a reader is promised once replication
exists. Until ADR-009 lands there is one replica and the distinction is not yet
observable, so the promise is stated here and measured there.

## Alternatives Considered

- **Serializable across entities, via two-phase commit or parallel commits.**
  The strongest useful guarantee, and what a caller would naively expect.
  Rejected because it makes every commit a distributed commit — a coordinator,
  a prepare phase, participant timeouts, and a recovery path for a coordinator
  that dies mid-transaction — in exchange for a guarantee no stated requirement
  asks for. It also cannot be scoped: once available it is used everywhere, and
  the cost is paid by transactions that did not need it.

- **Strict serializability (external consistency).** Serializable, plus real-time
  ordering across the whole cluster. Rejected for the reason ADR-002 rejected
  commit-wait: it needs bounded clock uncertainty and pays for it on every
  commit's latency.

- **Eventual consistency within an entity too.** Cheapest, and it removes leader
  election entirely. Rejected because it makes read-your-own-write false, which
  is the one anomaly users notice immediately and the one no application can
  work around locally.

- **Letting the write path query the read models.** Superficially attractive
  because the read models already hold rich indexes the writer would otherwise
  duplicate. Rejected: it couples the correctness of a write to a component
  that is deliberately stale and separately scaled, so a lagging replica becomes
  a source of wrong writes rather than merely slow reads. The duplication is the
  price of the separation, and rule 5 states it plainly rather than pretending
  the two indexes are one.

- **A transaction bounded to one LEAF rather than one entity.** Slightly more
  permissive: several entities that happen to share a leaf could commit
  together. Rejected because the set of entities sharing a leaf is an accident
  of hashing and of the cluster's current depth, so a transaction that succeeds
  today can fail tomorrow for a reason no caller can see. A boundary must be
  something the caller can reason about.

## Component / Boundary Impact

| Component | Owns | One reason to change? |
|-----------|------|-----------------------|
| `internal/core/ports` | The read/write port asymmetry, and the publisher. Interfaces only — no implementation, no I/O. | Yes — changes only when the shape of the read/write split changes. |
| `internal/core/command` | The single-entity transaction: construction, the refusal of a second entity, and the assertions it carries. | Yes — changes only when the transaction boundary changes. |

`ports` depends on the standard library and on `core` types alone. `command`
depends on `addr`, `tx` and `ports`. Neither touches storage or transport, so
the boundary is testable with no cluster and no disk.

## Wiring & Contract Changes

| Surface | Change | Producer | Consumer(s) |
|---------|--------|----------|-------------|
| `ports.Reader` (load only) | new | `internal/core/ports` (T1) | every read model, ADR-012's console |
| `ports.Writer` (append only) | new | `internal/core/ports` (T1) | the write path only |
| `ports.Store` (Reader + Writer) | new | `internal/core/ports` (T1) | the write path only |
| `ports.Publisher` (notify by id) | new | `internal/core/ports` (T1) | the write path; ADR-010's subscriptions |
| `command.Transaction`, `command.New()` | new | `internal/core/command` (T2) | every caller that writes |
| `command.ErrCrossEntity` | new | `internal/core/command` (T2) | every caller that writes |

## Inter-task Contracts

| Contract | Producing task | Consuming task(s) | Breaking? |
|----------|----------------|-------------------|-----------|
| `ports.Reader`, `ports.Writer`, `ports.Store`, `ports.Publisher` | T1 | T2, T3 | No — new surface |
| `command.Transaction`, `command.ErrCrossEntity` | T2 | T3 | No — new surface |

## Implementation

Three tasks. See [`ADR-003-transaction-boundary/tasks/README.md`](ADR-003-transaction-boundary/tasks/README.md).

## Consequences

- **Positive:** no distributed commit. A commit involves one entity's leaf and
  its replicas, and nothing else — which is what makes the write path's cost
  independent of cluster size.
- **Positive:** the read tier scales without touching the write tier, because
  nothing on the write path consults it and adding a reader changes no quorum.
- **Negative:** the caller carries what a multi-entity transaction would have
  given them. An operation spanning entities becomes several transactions plus
  a compensating one, and the intermediate states are visible. That is real work
  pushed onto the domain, and the domain has not yet been tested against it.
- **Negative:** two index families is real duplication. The same fact is indexed
  twice for two different purposes, and both must be maintained.
- **Neutral:** snapshot isolation admits write skew — two transactions reading
  overlapping state and writing disjoint state can produce a result no serial
  order would. That is inherent to the level and is stated so nobody is
  surprised by it.

## Out of Scope

- How a leaf elects the writer that makes rule 2 true (permanent: boundary: ADR-009 owns consensus; this record states the promise, that record makes it hold)
- The storage engine behind either index family (permanent: boundary: ADR-005 owns the segment format)
- Read replicas, and the mechanism by which they scale independently (deferred: `docs/adr/BACKLOG.md` §5)
- Bounded-staleness reads, which need a published closed timestamp (deferred: `docs/adr/BACKLOG.md` §5)
- Whether any real domain operation genuinely needs a multi-entity transaction (deferred: `docs/adr/BACKLOG.md` §8)

## Risks

| Risk | Likelihood | Impact | Mitigation |
|------|------------|--------|------------|
| A real domain operation turns out to need a multi-entity transaction | Medium | **High** — it would reopen the central decision and pull in distributed commit | Recorded as the falsifier above and deferred to `BACKLOG.md` §8 rather than assumed away; the refusal is a named error, so the case surfaces loudly the first time somebody tries |
| Someone lets the write path query a read model for convenience | Medium | High — a lagging replica becomes a source of wrong writes | Rule 7: the write path is handed a `Store`, a read model is handed a `Reader`, and a read model has no write method to call. `TestReadModelCannotWrite` is named in `Enforced-by:` |
| Write skew under snapshot isolation surprises a caller | Medium | Medium | Stated as a Neutral consequence rather than discovered; a domain needing serializability must say so and pay for it |
| Two index families drift, so a read model answers differently from the writer's own view | Medium | Medium | Both are built from one log, and ADR-010's subscription is the single path from log to projection; the drift check belongs with that record |

## Rollback

This governs no persistent state directly — it constrains what a caller may do
and which port a component is handed. Before any caller exists, revert the
branch.

After callers exist, **widening** the boundary is cheap and **narrowing** it is
not. Permitting multi-entity transactions later is additive: existing
single-entity callers keep working. Withdrawing that permission afterwards means
finding every caller that relied on it, which is why the record starts narrow.

The read/write port asymmetry is likewise a compile-time constraint: relaxing it
compiles, tightening it later does not.

## Follow-ups

- [ ] When ADR-009 lands, verify rules 2 and 3 are actually observable and that adding a read replica changes no quorum.
- [ ] When a real domain is modelled, revisit the falsifier: does any required operation resist expression as one entity per transaction?
