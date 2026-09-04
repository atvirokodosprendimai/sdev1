# ADR-004: Express durability as a per-tier policy over a declared failure domain, with a refusal floor

**Status:** Accepted
**Date:** 2026-09-04
**Owner:** M
**Spec:** None — no spec stage
**Cross-references:** `docs/adr/README.md`, `docs/adr/ADR-001-address-space.md`, `docs/adr/ADR-003-transaction-boundary.md`, `docs/adr/BACKLOG.md`
**Governs:** `internal/core/durability/**`
**Enforced-by:** `internal/core/durability/durability_test.go::TestPolicyBelowMinSizeIsRefused`
**Invalidates:** none — checked; ADR-001 decides where a leaf lives and says nothing about how many copies it has
**Served-path change:** A write is refused rather than accepted when fewer copies than the declared floor would be durable, so a cluster degrading past its own guarantee stops accepting data instead of silently taking it.

## Context

ADR-001 gave every leaf an address and a level hierarchy. ADR-003 fixed what a
transaction may touch. Neither says how many copies of a leaf exist, at what
spread, or what happens when the cluster cannot maintain that number — and the
last of those is the one that decides whether a degraded cluster loses data or
refuses writes.

Three requirements shape the answer, and two of them conflict.

**A minimum of two copies, always.** No data may ever be held once.

**Survive substantial loss:** two of three servers, or eight disks.

**And keep the storage overhead low**, which points at erasure coding rather
than whole copies.

⚠ **The second and third cannot both hold over one pool, and the arithmetic is
not a matter of tuning.** Surviving the loss of two servers out of three
requires each survivor to hold a complete recoverable copy, which is
three-way replication and 200% overhead by definition — no code beats it,
because a single survivor must suffice alone. An erasure code needs at least
`k+m` independent failure domains at the level it is meant to survive: a
(8,2) code across ten racks survives two rack losses at 25% overhead, and the
same code across three servers survives nothing at the server level however its
fragments are arranged. The number of failure domains BOUNDS what coding can
buy, and no configuration escapes that bound.

A second constraint points the same way from a different direction: consensus
needs whole replicas, because a fragment can neither serve a read independently
nor cast a vote. So the live tier could not be erasure-coded even if the
arithmetic allowed it. Two independent arguments reaching the same split is the
reason to trust it.

## Existing Primitives Audit

- `internal/core/topology` (ADR-001, T2): already supplies exactly what a
  failure domain needs — `Levels` names them, `AncestorAtLevel` answers "which
  rack is this replica in", and `Distance` orders candidates. **Reused as-is**;
  this record adds no new topology concept, it names a level and counts.
- `internal/core/placement` (ADR-001, T3): `Spread` already orders candidates so
  that a prefix occupies distinct domains. **Reused as-is** — this record decides
  how long that prefix is and what happens when the map cannot supply one.
- `internal/core/ports` (ADR-003, T1): the write path's port. **Reused**; the
  refusal below is a precondition on `Append`, not a new port.

## Decision

**Durability is a policy attached to a TIER, not to a cluster. A policy declares
a target copy count, a floor below which writes are REFUSED, and the topology
level its copies must be spread across.**

1. **Two knobs, not one.** `Size` is the target number of copies. `MinSize` is
   the floor: with fewer than `MinSize` copies currently durable, a write is
   refused. A cluster that has degraded stops accepting data rather than
   accepting it at a durability it does not have.
2. **`MinSize` is at least 2, and the value 1 is refused at policy construction.**
   A configuration that permits one copy will eventually be set to one copy.
3. ⚠**Two is a DURABILITY floor and not a CONSENSUS floor, and conflating them
   is the trap this rule exists to name.** Two voting members give a quorum of
   two, so losing either stops writes — a bare pair is LESS available than a
   single node while being more durable. The minimum viable live-tier shape is
   therefore two data replicas plus one witness that votes and stores no data.
4. **The live tier is replicated whole; the sealed tier is coded.** Consensus
   needs whole replicas. Once a segment seals and becomes immutable it is
   erasure-coded and the whole copies are dropped.
5. **A failure domain is a LEVEL in the topology map**, named by its label. A
   policy spreading across `rack` requires its copies to have distinct ancestors
   at that level.
6. **A policy the cluster cannot satisfy is refused when it is loaded, not
   discovered when a disk fails.** An erasure-coded policy needs at least
   `k+m` distinct domains at its level; a replicated policy needs at least
   `Size`. If the map offers fewer, the policy is rejected with a named error
   saying how many the map offers and how many the policy needs.

**What would falsify this decision.** The load-bearing claim is rule 6: that
feasibility is decidable up front from the map alone. It fails if a policy can be
feasible at load time and infeasible in practice — which it can, when servers are
down. That is why the check is stated as a bound on the DECLARED topology and the
runtime floor of rule 1 is a separate mechanism: the first catches a policy that
could never work, the second catches a cluster that has stopped working. Both are
tested, and neither substitutes for the other.

**Validity.** The arithmetic in rule 6 is valid for a topology whose declared
domains are genuinely independent. A map declaring ten racks that share one power
feed describes ten domains and has one, and nothing here can detect that — the
map is a declaration, and a declaration can be wrong.

## Alternatives Considered

- **One durability setting for the whole cluster.** Simplest to configure and to
  reason about. Rejected because the live and sealed tiers have incompatible
  requirements — consensus needs whole replicas, and low overhead needs coding —
  so a single setting must sacrifice one of them everywhere.

- **Erasure coding everywhere, including the live tier.** Best overhead.
  Rejected because a fragment cannot vote or serve a read alone, so the
  consensus group would have to be layered over reconstruction, making every
  quorum operation a gather.

- **Replication everywhere.** Simplest, and the live tier does it anyway.
  Rejected for the sealed tier on cost: identical fault tolerance at eight times
  the storage is not a trade worth making for immutable data.

- **A single `Size` with no separate floor.** What most systems ship. Rejected
  because it leaves no answer to "the cluster is degraded, do we still accept
  writes" other than yes — which means accepting data at a durability the
  operator believes they have and do not.

- **Deciding feasibility lazily, when a placement fails.** Less code, and the
  error surfaces where the problem is. Rejected because the failure then appears
  during a repair or a rebalance, which is precisely when an operator has least
  attention to spare, and because a policy that can never be satisfied is a
  configuration error rather than an operational one.

## Component / Boundary Impact

| Component | Owns | One reason to change? |
|-----------|------|-----------------------|
| `internal/core/durability` | The policy type, its validation against a topology map, and the two refusals. Pure computation; no I/O, no cluster state. | Yes — changes only when the durability model changes. |

It depends on `topology` and on nothing else. Whether a policy is currently
satisfied at runtime is a question about live state, which this package
deliberately does not hold; it answers whether a given set of domains satisfies a
policy, and the caller supplies the set.

## Wiring & Contract Changes

| Surface | Change | Producer | Consumer(s) |
|---------|--------|----------|-------------|
| `durability.Policy`, `durability.Tier` | new | `internal/core/durability` (T1) | the write path; ADR-006's coder; ADR-009's replication |
| `durability.ErrBelowFloor`, `durability.ErrInsufficientDomains` | new | `internal/core/durability` (T1, T2) | every caller that loads a policy or accepts a write |
| `Policy.Validate(topology.Map) error` | new | `internal/core/durability` (T2) | node startup, and any tool that lints a configuration |
| `Policy.Satisfied(domains []string) error` | new | `internal/core/durability` (T3) | the write path, before accepting |

## Inter-task Contracts

| Contract | Producing task | Consuming task(s) | Breaking? |
|----------|----------------|-------------------|-----------|
| `durability.Policy`, `durability.Tier`, `Policy.DomainsNeeded()` | T1 | T2 | No — new surface |
| `Policy.Validate()`, `Policy.Satisfied()` | T2 | none in this record | No — new surface |

## Implementation

Two tasks. See [`ADR-004-durability-policy/tasks/README.md`](ADR-004-durability-policy/tasks/README.md).

The split is by the QUESTION each answers rather than by code size. T1 makes a
policy impossible to construct in an unsafe shape. T2 answers the two separate
questions about a given policy: could this cluster EVER satisfy it, and does it
satisfy it RIGHT NOW. Those two are deliberately not one check — the first
catches a misconfiguration at startup, the second catches a degraded cluster at
write time, and a design with only one of them fails in the other's case.

## Consequences

- **Positive:** a degraded cluster refuses writes instead of accepting them at a
  durability nobody has. That is the behaviour an operator assumes they have and
  usually does not.
- **Positive:** an impossible policy is caught at startup with a message naming
  the shortfall, rather than at repair time.
- **Negative:** refusing writes is an availability cost, taken deliberately. A
  cluster that has lost too many copies becomes read-only for the affected
  leaves, and an operator must act.
- **Negative:** two policies means two code paths for durability, and a segment
  changes policy when it seals. That transition is a real moving part.
- **Neutral:** the minimum viable live shape is three participants for two
  copies, because of the witness. An operator expecting "two replicas means two
  machines" will be surprised, and the record says so rather than letting them
  discover it.

## Out of Scope

- The erasure code itself and its parameters (permanent: boundary: ADR-006 owns coding; this record decides that a coded tier exists and how many domains it needs)
- Leader election and what a witness actually votes in (permanent: boundary: ADR-009 owns consensus)
- Detecting that two declared domains secretly share a failure mode (permanent: fact: a topology map is a declaration of independence rather than evidence of it, and no property of the map can distinguish ten independent racks from ten sharing a power feed; citation: file `docs/adr/ADR-004-durability-policy.md:1`)
- Claiming a spare and releasing it after repair (deferred: `docs/adr/BACKLOG.md` §7)
- What a cluster does with leaves that are below the floor: alarm, re-replicate, or evict (deferred: `docs/adr/BACKLOG.md` §10)

## Risks

| Risk | Likelihood | Impact | Mitigation |
|------|------------|--------|------------|
| An operator sets a floor of 1 to get writes flowing during an incident | **High** | High — data held once, silently | Rule 2 refuses 1 at construction. It is a refusal rather than a warning precisely because the moment it would be relaxed is the moment nobody is reading warnings |
| A policy is declared that the topology can never satisfy | Medium | Medium — discovered during a repair | Rule 6 validates against the map at load time, naming the shortfall |
| Two is read as a consensus floor and a two-node cluster is deployed | Medium | High — writes stop on any single failure, which is worse than one node | Rule 3 states it, and the record names the witness shape rather than leaving the operator to derive it |
| A map declares domains that are not independent | Medium | **High** — the whole guarantee is void and nothing detects it | Stated as an Out of Scope fact rather than mitigated, because no property of a declaration can verify itself |

## Rollback

The policy governs behaviour rather than stored bytes, so a policy change is a
configuration change and reverting it is too — with one exception. Which tier a
segment is in, and therefore how it is stored, IS persistent: a segment that has
sealed and been coded does not become whole again by changing a policy. Reverting
the sealed-tier policy applies to future segments and requires a rewrite for
past ones, which is the same shape as every other format decision in this corpus.

Before any data exists, revert the branch.

## Follow-ups

- [ ] When ADR-006 lands, verify the coded tier's `k+m` is checked against the same domain count this record validates, rather than against a second definition.
- [ ] When ADR-009 lands, confirm a witness is expressible: a voting member holding no data is a shape the consensus layer must actually support, and this record assumes it.
