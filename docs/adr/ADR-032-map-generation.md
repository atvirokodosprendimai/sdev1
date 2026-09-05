# ADR-032: A topology map carries a generation identified by a transaction, and a placement against a map that cannot say which it is, is refused

**Status:** Accepted
**Date:** 2026-09-05
**Owner:** M
**Spec:** None — no spec stage
**Cross-references:** `docs/adr/README.md`, `docs/adr/ADR-001-address-space.md`, `docs/adr/ADR-002-transaction-identity.md`, `docs/adr/ADR-024-segment-store.md`, `docs/adr/ADR-026-leaf-store.md`, `docs/adr/BACKLOG.md`
**Governs:** `internal/core/topology/generation.go`
**Enforced-by:** `internal/core/placement/generation_test.go::TestPlacementRefusesAMapThatCannotSayWhichItIs`
**Invalidates:** none — ADR-001 T3's Stop Condition raised this and `BACKLOG.md` §6 has carried it since
**Served-path change:** A placement is now reproducible against the map it was made under, or refused. Before this a map had no identity, so "where was this leaf placed last March" had no answer even in principle.

## Context

`BACKLOG.md` §6 says placement is really a function of `(leaf, map version)`, and
warns that settling it after callers exist is too late.

★ **Two things about that turned out to be different from how it was written, and
both matter.**

First, the signature is already right. `placement.Resolve(leaf, topology.Map)`
takes the map as a parameter — it never reads a global "current map". What is
missing is not a parameter but an IDENTITY: a `Map` cannot say which map it is,
so two different maps are indistinguishable to anything holding one.

⚠ Second, and worse: **the field a reader would reach for is already taken.**
`topology.Map` has a `Version` field, and it means the FILE FORMAT version — a
constant this build compares against and which has never changed. Anyone
implementing §6 would very reasonably set the map's identity there, and every map
in the cluster would then claim to be the same map, forever, with nothing failing.

## Existing Primitives Audit

- `internal/core/topology` (ADR-001): supplies `Map`, `Load` and the authored
  file format. **Extended by one field**, and one existing field is renamed to
  stop it being the trap above.
- `internal/core/tx` (ADR-002): supplies `TxID`, its total order and its
  fixed-width encoding. **Reused as the generation itself** — see rule 1.
- `internal/core/placement` (ADR-001): supplies `Resolve`. **Gains a refusal and
  no parameter**, because it already takes the map.
- A monotonic counter, or a content hash of the map: **rejected below.** Both are
  a second way of ordering things in a system that already has one.

## Decision

**A map's generation IS a transaction identifier; a map may be loaded without
one; and a placement against a map without one is refused.**

1. **The generation is a `tx.TxID`.** ★ ADR-002's identifier is the only total
   order in this system. A counter would be a second clock, and two clocks
   disagree — which here means two maps claiming the same generation, or an
   ordering between maps that contradicts the ordering between the writes made
   under them.

2. **It is called `Generation`, and the format version is renamed to
   `FormatVersion`.** ⚠ Not cosmetic. A field called `Version` sitting on a map,
   meaning something else, is the single most likely way to implement this
   decision wrongly — and the wrong implementation looks completely correct and
   never fails.

3. **A map may be LOADED without a generation.** Reading a map to inspect a
   cluster's shape is a legitimate thing to do with a file nobody published, and
   requiring an identity for it would be requiring one of maps that never place
   anything.

4. **A placement against a map with no generation is REFUSED.** ⚠ This is the
   record, and the refusal is here rather than at load because this is where the
   consequence is: a placement you cannot reproduce is a segment you cannot find.
   ★ A zero generation must never be treated as "generation zero" — that reads as
   an answer and it means every map is the same map.

5. **A generation may not be retired while anything placed under it still
   exists.** ⚠ Retiring a map is data loss with extra steps: the segments placed
   under it become unlocatable, and nothing about the retirement looks like
   deleting data. Map retention is therefore bounded below by the lifetime of what
   it placed, not by a policy about maps.

6. **Every generation is retained by default.** A map is small — kilobytes — and
   the alternative is a retention rule whose correctness nobody can check without
   scanning every segment in the cluster. ⚠ Stated as a decision so that "we
   should prune old maps" arrives as a proposal against a written default rather
   than as tidying.

7. **The generation is authored, not assigned at load.** ★ Two processes loading
   the same file must agree about which map it is; a generation minted at load
   would give the same file a different identity in every process, which is the
   original failure wearing a new hat.

**What would falsify this.** A placement succeeding against a map that cannot say
which map it is. That is the falsifier in `Enforced-by:`, and it is what the zero
value produces if nothing refuses it.

## Alternatives Considered

- **Use the existing `Version` field.** No new field, no format change, and it is
  right there. Rejected under rule 2: it is the FORMAT version, a constant, so
  every map would share a generation and historical placement would remain
  unresolvable while appearing solved.
- **A monotonic integer counter per map publication.** Simpler to author and to
  read. Rejected under rule 1: it is a second ordering in a system that has one,
  and reconciling "map 7" with the transactions written under it needs a mapping
  nobody maintains. A `TxID` already orders against the writes.
- **A content hash of the map.** It needs no authoring and cannot collide by
  accident. Rejected under rule 1 and rule 5: hashes do not ORDER, so "which map
  came first" becomes unanswerable — and two publications of an identical map
  would share an identity, which is right for content and wrong for a generation.
- **Refuse to LOAD a map with no generation.** Stricter, and it catches the
  mistake earlier. Rejected under rule 3: it makes every fixture, inspection tool
  and hand-written example carry an identity it has no use for, and the refusal
  would land far from the consequence it protects.
- **Default a missing generation to the zero `TxID` and carry on.** It is what
  Go's zero value does for free. Rejected under rule 4: it is precisely the
  failure — a value that reads as an answer, so every map is the same map and
  nothing ever fails.
- **Record the generation in the segment header now.** §6 asks for it, and it is
  where it will eventually have to live. Rejected as premature: nothing places a
  segment yet — `Resolve` is called by a demonstration binary and by the
  prefetcher, and no writer consults it — so the header field would be written by
  nobody and read by nobody. Deferred to whatever wires placement to storage, with
  a follow-up rather than a guess at the field's shape.

## Component / Boundary Impact

No new component. `internal/core/topology` gains a generation and loses a
misleading field name; `internal/core/placement` gains a refusal.

⚠ The boundary: this decides what identifies a map and when a placement may
proceed. It does not decide who publishes a map, how one is distributed, or where
a segment records the generation it was placed under — the last needs something
that places segments, and nothing does.

## Wiring & Contract Changes

| Surface | Change | Producer | Consumer(s) |
|---------|--------|----------|-------------|
| `topology.Map.Generation` | new field — which map this is | T1 | placement, callers |
| `topology.Map.FormatVersion` | RENAMED from `Version` — the file format, not the map | T1 | callers |
| `topology.Map.Placeable` | new — whether the map can be placed against | T1 | placement |
| authored `generation` field | new, optional — the hex of `tx.TxID.Encode()` | T1 | operators |
| `topology.ErrBadGeneration` | new sentinel — a generation that will not decode | T1 | callers |
| `placement.ErrNoGeneration` | new sentinel — placing against an unidentified map | T1 | callers |

## Inter-task Contracts

| Contract | Producing task | Consuming task(s) | Breaking? |
|----------|----------------|-------------------|-----------|
| `topology.Map.Generation` | T1 | whatever places segments (`BACKLOG.md` §6/§18) | No |

## Consequences

- **Positive:** A placement is reproducible or refused. `BACKLOG.md` §6's central
  question is answered while it is still free to answer.
- **Positive:** The `Version`/`Generation` trap is removed at the source rather
  than documented around it.
- **Negative:** Every map that will ever place must be authored with a
  generation, and getting one wrong is as consequential as getting the shape
  wrong. That is inherent: an identity nobody chose is an identity nobody can
  reproduce.
- **Negative:** Maps accumulate forever under rule 6. They are small, and the cost
  is stated rather than discovered.
- **Neutral:** No segment records a generation yet, because nothing places one.
  The record says so rather than implying the loop is closed.

## Out of Scope

- Recording the generation a segment was placed under, in its header (deferred: `docs/adr/BACKLOG.md` §6 — nothing places a segment yet, so the field would be written and read by nobody)
- Publishing and distributing a map to nodes (deferred: `docs/adr/BACKLOG.md` §18)
- Migrating data when the map changes (deferred: `docs/adr/BACKLOG.md` §1)
- Retiring a generation, and what proves nothing references it (deferred: `docs/adr/BACKLOG.md` §6)
- Who mints a map's generation, and under what authority (deferred: `docs/adr/BACKLOG.md` §19)
- Ordering maps against each other by anything but their transaction (permanent: boundary: rule 1 — ADR-002's identifier is the system's only total order, and a second one would contradict it)

## Risks

| Risk | Likelihood | Impact | Mitigation |
|------|------------|--------|------------|
| The generation is put in the existing `Version` field | High — it is named for it and already there | Critical — every map claims the same generation, historical placement stays unresolvable, and nothing fails | Rule 2 renames the field, so the trap is gone rather than warned about |
| A missing generation defaults to the zero `TxID` | High — it is Go's zero value | Critical — the zero reads as an answer, so every unidentified map is the same map | Rule 4, and the falsifier places against exactly that map |
| A generation is minted at load | Med — it removes the authoring burden | High — the same file gets a different identity in each process, which is the original failure | Rule 7, and `Load` never generates one |
| A counter or hash is used instead of a `TxID` | Med — both are easier to author | High — a second ordering that contradicts the writes, or no ordering at all | Rule 1, argued from there being one clock |
| Old maps are pruned as tidying | Med | Critical — segments placed under a retired map become unlocatable, and it does not look like deleting data | Rules 5 and 6, so pruning arrives as a proposal against a written default |

## Rollback

Reverting removes a field and a refusal; nothing on disk depends on either,
because nothing places a segment yet. ⚠ That freedom ends the moment something
does — which is exactly why §6 said to settle this before callers exist, and why
it is settled now rather than when a segment header would have to change with it.

## Follow-ups

- [ ] When something places a segment (`BACKLOG.md` §6/§18), record the generation in its header and decide the field's width there — ADR-005's header is versioned, so this is a format bump rather than a rewrite, and it should be taken with the placer rather than guessed at now.
- [ ] Decide what proves no segment references a generation before it may be retired (`BACKLOG.md` §6); rule 5 states the constraint and nothing can currently check it.
- [ ] When a map is distributed to nodes (`BACKLOG.md` §18), confirm two nodes holding the same generation hold the same map — the generation is authored, so nothing yet stops two different files claiming one.
