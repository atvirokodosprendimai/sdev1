# ADR-029: Compaction merges segments and drops nothing, and a datom seen twice is returned once

**Status:** Accepted
**Date:** 2026-09-04
**Owner:** M
**Spec:** None — no spec stage
**Cross-references:** `docs/adr/README.md`, `docs/adr/ADR-010-subscribe-and-purge.md`, `docs/adr/ADR-017-lock-free-read-path.md`, `docs/adr/ADR-024-segment-store.md`, `docs/adr/ADR-025-datom-encoding.md`, `docs/adr/ADR-026-leaf-store.md`, `docs/adr/ADR-028-seal-policy.md`, `docs/adr/BACKLOG.md`
**Governs:** `internal/core/leafstore/compact.go`
**Enforced-by:** `internal/core/leafstore/compact_test.go::TestAnInterruptedCompactionDoesNotDuplicate`
**Invalidates:** none — it narrows ADR-026's rejection of deduplication rather than reversing it; see the Decision
**Served-path change:** A leaf's read cost stops growing with the number of times it has been sealed. Before this, every read touched every segment ever written.

## Context

ADR-028 bounded the tail. Nothing bounds the number of SEGMENTS, and ADR-026 was
explicit that a read touches every one of them — so a leaf sealed a thousand times
costs a thousand block lookups to answer one question. That is now the largest
cost in the system and the last open half of `BACKLOG.md` §15.

Merging segments is easy. Publishing the merge is not, and the difficulty is
specific to ADR-026's design.

★ **ADR-026 has no manifest: the directory listing IS the set of segments**,
because ADR-024 publishes a segment by renaming it into place, so a file that is
there is complete. That works beautifully for a design where files are only ever
ADDED. Compaction is the first operation that REMOVES one.

⚠ **A compaction is therefore two changes to the directory**, and between them it
is observable. Publish the merged segment first and a reader briefly sees the
inputs AND the output — every datom twice. Remove the inputs first and a reader
briefly sees NEITHER — every datom missing. And a crash between the two leaves
whichever state it was in, permanently, for every future read.

The ordering fixes the crash's direction. It cannot fix the overlap.

## Existing Primitives Audit

- `internal/core/leafstore` (ADR-026): supplies the `Store`, its segment set and
  its merge. **Extended** — the merge is where a duplicate must be resolved, and
  it is already the one place segments are combined.
- `internal/core/segstore` (ADR-024): supplies `Create`/`Seal`/`Open`, and the
  rename that publishes. **Reused whole** — a compacted segment is an ordinary
  segment, written the ordinary way, so nothing about reading one is new.
- `internal/core/datom` (ADR-025): supplies the encoding, and with it the fact
  that a datom has a canonical byte form. **Relied on for identity**, below.
- ADR-010's purge and its horizon: **not reached.** Compaction drops nothing, so
  it is not a retention mechanism and must not become one.
- A manifest file, or a generation counter beside the leaf: **none, still.** It
  would make the overlap window closable, and it would reintroduce exactly the
  two-things-to-publish problem ADR-024's follow-up flagged and ADR-026 removed.

## Decision

**Compaction merges segments, drops nothing, publishes the output before removing
the inputs — and a datom that appears in more than one segment is returned once.**

1. **Compaction is a LAYOUT operation. It drops no fact.** ⚠ It is tempting to
   discard superseded datoms while rewriting them anyway, and it would be wrong
   twice over: this is a bitemporal store, so a superseded fact is still the
   answer to a question about the past; and dropping data is ADR-010's purge,
   which has a horizon, an acknowledgement protocol and an erasure story that a
   background merge has none of.

2. **The merged segment is published BEFORE the inputs are removed.** ⚠ The
   reverse loses data: a reader between a removal and a publish sees neither copy.
   This direction is recoverable — the worst a crash leaves is both copies.

3. **A datom appearing in more than one segment is returned ONCE.** ★ This is what
   makes rule 2 safe rather than merely better, and it is the whole reason the
   overlap window does not need to be closed. A crash between publish and remove
   leaves the overlap on disk PERMANENTLY, so an ordering alone would leave every
   later read wrong.

4. **Two datoms are the same datom when EVERY field is equal, the transaction
   identifier included.** ⚠ ADR-026 rejected deduplication because "it needs a key
   that says two datoms are the same fact, and this store does not get to invent
   one — two identical assertions from two transactions are two facts." That
   rejection stands and this does not contradict it: full-field equality INCLUDES
   the transaction, so two transactions can never be conflated. ★ Nothing is
   invented — a datom already has a canonical form, because ADR-025 gave it one.

5. **A compaction that fails leaves the leaf exactly as it was.** The output is
   written under a temporary name and published by ADR-024's rename, so a failure
   before the rename leaves nothing, and a failure after it leaves the safe
   overlap of rule 3.

6. **Compaction is explicit, and its policy is a segment count.** ⚠ Same shape as
   ADR-028: this decides WHETHER a compaction is due and never performs one on a
   schedule. A count rather than bytes, because the cost being paid is one block
   lookup per segment per read, and that is counted in segments.

7. **Every current segment is merged into one.** ★ Simple, and correct.
   ⚠ It is also quadratic over a leaf's lifetime — each compaction rewrites
   everything — and tiering is what fixes that. Deferred rather than guessed at,
   because a tiering scheme chosen before anything has measured a real leaf is a
   shape nobody has reason to believe.

**What would falsify this.** A leaf answering differently after a compaction that
published its output and did not remove its inputs. That is the falsifier in
`Enforced-by:`, it is exactly what a crash leaves, and it needs one directory.

## Alternatives Considered

- **Remove the inputs first, then publish the merge.** It never shows a duplicate.
  Rejected under rule 2: it shows an ABSENCE instead, and a crash in that window
  destroys data that was durable a moment earlier. A duplicate is recoverable by
  reading; a gap is not recoverable at all.
- **Close the window with a manifest naming the current segments.** It is the
  conventional answer and it makes the swap a single atomic publication.
  Rejected: it reintroduces the two-things-to-publish problem ADR-026 removed by
  having no manifest, and the manifest then needs its own atomicity story against
  the segment files it names. ⚠ It also does not help the crash case — a crash
  between writing the manifest and deleting the files leaves the same orphans, and
  now they are invisible rather than harmless.
- **Deduplicate by entity, attribute and transaction, without the value.** Cheaper
  — no value comparison. Rejected under rule 4: it is a key somebody chose, and it
  silently merges two datoms that disagree about their value, which is a
  malformed input this store does not get to repair by hiding one of them.
- **Drop superseded datoms while merging.** It is what compaction means in a store
  that overwrites, and it would shrink a leaf substantially. Rejected under rule
  1: a superseded fact is still the answer to a question about the past, which is
  the property this whole system exists for.
- **Compact in the background on a timer.** It is what everybody does. Rejected
  under rule 6, for ADR-028's reason: who runs it and how often is a deployment
  decision, and a package that started a goroutine would take it silently.
- **Tier the merge — compact recent segments together and older ones less often.**
  Strictly better asymptotically. Rejected as premature under rule 7: the ratios
  in a tiering scheme are the whole design, and choosing them against no measured
  leaf is choosing them on aesthetics.

## Component / Boundary Impact

No new component. `internal/core/leafstore` gains a merge and a deduplicating
read. ⚠ It gains no goroutine, no timer and no retention: compaction rewrites
where facts live and never changes which facts there are.

## Wiring & Contract Changes

| Surface | Change | Producer | Consumer(s) |
|---------|--------|----------|-------------|
| `leafstore.Store.Compact` | new — merge every current segment into one | T1 | callers |
| `leafstore.Store.ShouldCompact` | new — whether a compaction is due | T1 | callers |
| `leafstore.Policy.MaxSegments` | new field — the segment-count bound | T1 | callers |
| `leafstore.Store.History` / `Load` | changed — a datom seen twice is returned once | T1 | callers |

## Inter-task Contracts

| Contract | Producing task | Consuming task(s) | Breaking? |
|----------|----------------|-------------------|-----------|
| `leafstore.Store.Compact` | T1 | a caller that compacts on a schedule (`BACKLOG.md` §15) | No |

## Consequences

- **Positive:** A leaf's read cost stops growing with how many times it has been
  sealed. `BACKLOG.md` §15 closes.
- **Positive:** A crash mid-compaction is harmless rather than corrupting, and
  the same mechanism makes a partially-copied or restored leaf harmless too.
- **Positive:** ADR-026's manifest-free design survives its first removal
  operation, so there is still exactly one thing to publish.
- **Negative:** Every read now pays a deduplication pass over the datoms it
  gathered. It is proportional to what was read rather than to the leaf, and it is
  the price of rule 3.
- **Negative:** Merging everything into one segment is quadratic over a leaf's
  lifetime. That is rule 7's stated cost, and tiering is deferred.
- **Negative:** Orphaned input segments left by a crash are never cleaned up by
  anything. They are harmless and they occupy space; reclaiming them needs to know
  no reader holds them, which is a lifetime question this record does not open.
- **Neutral:** Nothing compacts on its own. A caller decides.

## Out of Scope

- Tiering, and merging a subset of segments (deferred: `docs/adr/BACKLOG.md` §15)
- Reclaiming orphaned inputs left by an interrupted compaction (deferred: `docs/adr/BACKLOG.md` §15)
- Who calls `ShouldCompact`, and on what schedule (deferred: `docs/adr/BACKLOG.md` §15)
- Compacting across leaves (deferred: `docs/adr/BACKLOG.md` §18)
- Erasure-coding a compacted segment (deferred: `docs/adr/BACKLOG.md` §12)
- Dropping any fact (permanent: boundary: ADR-010's purge owns removal, with a horizon and an acknowledgement protocol; a background merge that also deleted would be a retention policy nobody wrote down)
- Inventing an identity for a datom (permanent: fact: a datom already has a canonical byte form, so equality is defined rather than chosen; citation: file `internal/core/datom/datom.go:1`)

## Risks

| Risk | Likelihood | Impact | Mitigation |
|------|------------|--------|------------|
| Inputs are removed before the output is published | Med — it avoids ever showing a duplicate | Critical — a crash in that window destroys data that was durable, and a gap cannot be recovered by reading | Rule 2, and the falsifier exercises the surviving state |
| The overlap is left to the ordering alone, with no deduplication | High — the ordering looks sufficient | Critical — a crash leaves the overlap on disk permanently, so every later read double-counts | Rule 3, with a test that reopens a leaf in exactly that state |
| Deduplication uses a chosen key rather than full equality | Med — a shorter key is cheaper | High — two datoms that disagree about their value are silently merged, hiding a malformed write | Rule 4, and a test where the same slot holds two different values |
| Compaction drops superseded facts | Med — it is what the word usually means | Critical — a bitemporal store's answers about the past change, and nothing reports it | Rule 1, with a test asserting history is identical across a compaction |
| Compaction runs on a timer inside the package | Med | Med — a deployment decision taken silently, and I/O nobody scheduled | Rule 6; `ShouldCompact` returns a bool |

## Rollback

Compaction is additive: reverting it leaves leaves with more segments than they
need and every read still correct. ⚠ The deduplication is NOT safely revertible
once any leaf has been compacted, because a leaf carrying an interrupted
compaction's overlap depends on it — which is why rules 2 and 3 land together
rather than the ordering first.

## Follow-ups

- [ ] When something compacts on a schedule (`BACKLOG.md` §15), decide what reclaims orphaned inputs — they are harmless and they accumulate, and knowing no reader holds one is a lifetime question this record left closed.
- [ ] Measure a real leaf before choosing a tiering scheme (`BACKLOG.md` §15); rule 7's quadratic cost is arithmetic, and the ratios that fix it should come from a distribution rather than from taste.
- [ ] When erasure coding reaches sealed segments (`BACKLOG.md` §12), confirm compaction re-codes its output rather than inheriting a stripe layout from its inputs — a merged segment is a new segment and ADR-006's scheme is recorded per stripe.
