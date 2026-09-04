# ADR-026: A leaf is a directory of segments, and a read merges them by the datoms' own transaction identifiers

**Status:** Accepted
**Date:** 2026-09-04
**Owner:** M
**Spec:** None — no spec stage
**Cross-references:** `docs/adr/README.md`, `docs/adr/ADR-002-transaction-identity.md`, `docs/adr/ADR-003-transaction-boundary.md`, `docs/adr/ADR-017-lock-free-read-path.md`, `docs/adr/ADR-020-commit-point.md`, `docs/adr/ADR-022-write-statements.md`, `docs/adr/ADR-024-segment-store.md`, `docs/adr/ADR-025-datom-encoding.md`, `docs/adr/BACKLOG.md`
**Governs:** `internal/core/leafstore/**`
**Enforced-by:** `internal/core/leafstore/leafstore_test.go::TestTheAnswerDoesNotDependOnSegmentOrder`
**Invalidates:** none — ADR-024 decided what one segment file is and ADR-025 what a fact inside it is; neither says how MANY segments make a leaf, or how a read that spans them gets one answer
**Served-path change:** A fact written to a leaf survives the process that wrote it and is read back at a snapshot. `BACKLOG.md` §28 — "writes reach memory and stop there" — is what this closes.

## Context

There is a file of blocks (ADR-024) and a fact in bytes (ADR-025). Nothing puts
the second in the first, so an `ASSERT` still dies with the process.

The gap is not large, but it contains one decision that is easy to get wrong in a
way that stays hidden for a long time.

★ **A leaf's history is spread across many segments, so a read has to merge them —
and the obvious way to merge is in the order the files came back.** A directory
listing is sorted by name, so that reads as deterministic. It is not. It makes the
answer depend on what the files are CALLED: a rename reorders it, a copy that
preserves nothing but content reorders it, and a restore that lays files down in a
different order reorders it. Nothing about any of those looks like a data-loss
event, and the wrong answer is a plausible one — an older value winning over a
newer one, with no error anywhere.

⚠ `BACKLOG.md` §12 wrote this trap down before there was anything to trap: *"whatever
names a segment file must not encode anything a reader needs in order to interpret
it."* Sort order is exactly that.

The answer is already in the data. ADR-002 gives every datom a totally ordered
transaction identifier, so a merge can order itself and the filenames can carry
nothing at all.

## Existing Primitives Audit

- `internal/core/segstore` (ADR-024): supplies `Writer`, `Reader`, and the
  guarantee that a file at its path is complete. **Reused whole** — and it is what
  makes the directory listing a manifest, below.
- `internal/core/datom` (ADR-025): supplies `Encode` and `Decode`. **Reused
  whole** — one block holds one entity's run.
- `internal/core/temporal` (ADR-002): supplies `Visible`. **Reused, and this is
  load-bearing.** ⚠ §28 warns that the session must not become the specification;
  the same applies here in reverse. Visibility is decided at ADR-002's single
  comparison site, and a second implementation in a storage package would be a
  rule nobody wrote down.
- `internal/core/ports` (ADR-003): supplies `Store`, `Reader`, `Writer`,
  `Snapshot`. **Implemented** rather than extended — this record adds no method to
  those interfaces, because a storage engine that needed a wider contract than a
  read model would be a storage engine deciding what a read model may do.
- A manifest file naming which segments exist: **none, deliberately.** See rule 1.
- A log-structured merge library, or an embedded key-value store: **none.** Both
  bring a compaction policy, and when to seal or compact is `BACKLOG.md` §15 —
  adopting one would answer it by accident and in someone else's terms.

## Decision

**A leaf is a directory whose every file is a complete segment; a read merges all
of them by the datoms' own transaction identifiers, and the filenames carry
nothing.**

1. **The directory listing IS the manifest.** ⚠ ADR-024 publishes a segment by
   renaming it into place, so a file that is there is complete, and there is
   nothing to be told about. ★ This retires ADR-024's own follow-up about ordering
   two publications — a segment and the manifest naming it — because there is only
   one publication.

2. **A read merges by `TxID`, never by filename and never by the order files were
   opened.** ⚠ The record, and the falsifier. Every datom carries a totally
   ordered identifier (ADR-002), so the data orders itself and no property of the
   filesystem can change the answer.

3. **A segment's name is random and means nothing.** ★ Deliberately: a name that
   sorted would be a name something could come to depend on, and rule 2 would then
   be true only until somebody wrote the loop that broke it. A name that cannot be
   ordered cannot be depended on.

4. **One block per entity per segment, keyed by the entity name.** A read fetches
   one block per segment rather than scanning it. ⚠ It is still linear in the
   number of SEGMENTS, and that cost is real; compaction is `BACKLOG.md` §15.

5. **Sealing is explicit. This record decides the mechanism, not the policy.**
   ⚠ ADR-020 already fixed that acknowledgement means held in memory, not written
   to disk, so a segment is a durability tier and NOT the commit path. Saying so
   here is what stops someone later "fixing" the write path to flush, which would
   quietly move the commit point and the latency with it.

6. **A sealed fact appears exactly once.** ⚠ Publishing the segment and clearing
   the tail happen under one exclusive hold. Between a rename and a separate
   clear, a read sees the fact in both places and returns it twice — and a
   duplicated datom is not an obvious error, it is a fact that looks asserted
   twice.

7. **An entity that a segment does not hold is an empty result, not a refusal.**
   ⚠ ADR-024 is right that a missing key is a named refusal — at the block layer,
   where absence is exceptional. Here it is the common case: most segments hold
   most entities not at all. The translation happens in exactly one place, and it
   is named, because a refusal swallowed in a loop is how a real error becomes an
   empty answer.

8. **A zero snapshot is a named refusal, not an empty answer.** ⚠ A zero `TxID`
   bounds the system axis at before-anything, so every fact is invisible and the
   read returns nothing at all — which is indistinguishable from an entity that has
   no facts. "You asked as of the beginning of time" and "there is nothing here"
   are different answers, and the first is always a bug.

9. **`Load` returns history; `Attributes` returns present shape.** `Load` gives
   every visible datom, assertions and retractions alike, because a caller
   reasoning about time needs both. `Attributes` resolves the latest visible datom
   per attribute and keeps only the asserted ones — an attribute that was retracted
   is not carried. ★ It is derived from `Load`, so the two cannot disagree about
   what an entity has.

10. **A dot-prefixed file is skipped.** That is what ADR-024 calls a partial
    write, and opening one is how a crash that was safely survivable becomes a
    read error at start-up.

11. **A leaf can list the entities it holds, and that is NOT the enumeration
    `BACKLOG.md` §20 defers.** ★ A segment already knows its own keys, so this is
    a directory listing of one leaf. §20 is a different question — `SELECT` over
    entities nobody named, across leaves, which needs a planner and a routing
    decision. ⚠ Stated because the two look alike from the outside, and answering
    the second by quietly generalising the first is how a deferred decision gets
    taken without a record.

12. **`History` is the primitive; `Load` is `History` filtered at a snapshot.**
    ⚠ Not two gatherings with a filter on one of them. A caller rebuilding state
    needs every datom, and no snapshot returns all of history — an instant on the
    business axis selects the facts true AT it. Deriving one from the other is what
    stops the filter being applied on one read path and forgotten on the other.

**What would falsify this.** A leaf answering differently when its segments are
renamed. That is the falsifier in `Enforced-by:`, it needs one directory and no
cluster, and it is exactly what merging in listing order produces — which is the
implementation anyone writes first.

## Alternatives Considered

- **Merge segments in the order the directory listing returns them.** It is the
  shortest correct-looking code, and on one machine that never renames anything it
  works forever. Rejected under rule 2: it makes the answer a property of the
  filenames, so a rename, a copy, or a restore silently reorders history, and the
  wrong answer is a plausible one with no error anywhere.
- **Name segments with a zero-padded monotonic sequence and merge by name.** It
  fixes the ordering honestly and is easy to read in a directory listing.
  Rejected under rules 2 and 3, and by `BACKLOG.md` §12's trap: a reader would then
  need the filename in order to interpret the contents, the padding becomes
  load-bearing (an unpadded 10 sorts before 9), and a leaf restored under new names
  is silently wrong rather than loudly broken.
- **Keep a manifest file listing the segments in order.** It is the conventional
  answer and it makes the set explicit. Rejected under rule 1: the rename already
  makes every present file complete, so a manifest adds a SECOND thing to publish
  atomically — and ADR-024's own follow-up flagged that ordering two publications
  is the hard part. A manifest solves a problem this design does not have and
  creates one it does not want.
- **Flush on `Append` so a write is durable when it is acknowledged.** It is what
  most people mean by durable and it removes the explicit seal. Rejected under rule
  5: ADR-020 decided the commit point is N memory replicas in distinct failure
  domains, on the record, with the trade stated. Changing it here would move the
  commit point and the latency as a side effect of a storage decision.
- **Deduplicate on read instead of clearing the tail under the publish.**
  Tolerant, and it removes a lock. Rejected under rule 6: deduplication needs a
  key that says two datoms are the same fact, and this store does not get to invent
  one — two identical assertions from two transactions are two facts.
- **Adopt an embedded key-value store or an LSM library for the leaf.** Mature,
  and compaction comes free. Rejected in the audit: compaction policy is
  `BACKLOG.md` §15, and free compaction is compaction decided by somebody else, in
  terms this corpus has not agreed to.

## Component / Boundary Impact

One new component, `internal/core/leafstore`, owning one thing: how many segments
become one answer. It has one reason to change — how a leaf's files are arranged.

⚠ The boundary: it decides no policy. Not when to seal, not when to compact, not
what visibility means, not what a block or a fact is. It implements `ports.Store`
and adds no method to it, so a read model handed the read half still cannot write.

## Wiring & Contract Changes

| Surface | Change | Producer | Consumer(s) |
|---------|--------|----------|-------------|
| `leafstore.Store` / `Open` | new — open a leaf directory | T1 | callers |
| `leafstore.Store.Append` | new — add datoms to the tail (`ports.Writer`) | T1 | callers |
| `leafstore.Store.Seal` | new — write the tail into one segment | T1 | callers |
| `leafstore.Store.History` | new — every datom for one entity, the primitive `Load` filters | T1 | callers |
| `leafstore.Store.Load` / `Attributes` | new — read at a snapshot (`ports.Reader`) | T1 | callers |
| `leafstore.Store.Entities` | new — which entities this leaf holds | T1 | T2 |
| `leafstore.Store.Close` | new — release the mappings | T1 | callers |
| `leafstore.Store.Pending` / `Segments` | new — what is unsealed, and how many files | T1 | callers |
| `leafstore.Extension` | new — the suffix a segment file carries | T1 | callers |
| `leafstore.ErrNoSnapshot` / `ErrClosed` | new sentinels | T1 | callers |

## Inter-task Contracts

| Contract | Producing task | Consuming task(s) | Breaking? |
|----------|----------------|-------------------|-----------|
| `leafstore.Store` | T1 | T2 | T1 before T2 |

## Consequences

- **Positive:** A fact survives the process that wrote it. `BACKLOG.md` §28 closes.
- **Positive:** The answer is a property of the DATA, so a leaf can be copied,
  renamed, restored or re-laid-out by any tool that preserves file contents.
- **Positive:** There is exactly one thing to publish, so the ordering problem
  ADR-024 flagged between a segment and its manifest does not arise.
- **Negative:** A read touches every segment in the leaf. That is linear in seal
  count and it is the cost that makes compaction (`BACKLOG.md` §15) matter, rather
  than a cost this record hides.
- **Negative:** Segment filenames are meaningless, so an operator cannot tell from
  a listing which segment is newest. The information is in the data — every datom
  carries its transaction — and that is the trade rule 3 makes on purpose.
- **Negative:** The whole tail is held in memory until a seal, so an unsealed leaf
  is bounded by memory and loses everything on a crash. ADR-020 says that is what
  acknowledgement means here; it is restated because it surprises people.
- **Neutral:** Nothing decides when to seal. A caller does.

## Out of Scope

- When the tail is sealed, and compaction of many segments into fewer (deferred: `docs/adr/BACKLOG.md` §15)
- An index over the tail, and over a leaf's segments (deferred: `docs/adr/BACKLOG.md` §15)
- Erasure-coding a leaf's sealed segments (deferred: `docs/adr/BACKLOG.md` §12)
- Reclaiming a segment whose facts are all superseded (deferred: `docs/adr/BACKLOG.md` §15)
- Enumerating the entities in a leaf (deferred: `docs/adr/BACKLOG.md` §20)
- Replicating a leaf to another server (deferred: `docs/adr/BACKLOG.md` §18)
- What visibility means (permanent: boundary: ADR-002 owns the one comparison site where a validity bound and a transaction identifier are both compared, and a second implementation here would be a rule nobody wrote down)
- Making a write durable at acknowledgement (permanent: boundary: ADR-020 fixed the commit point at N memory replicas in distinct failure domains, and moving it as a side effect of a storage decision would change the latency contract without a record)

## Risks

| Risk | Likelihood | Impact | Mitigation |
|------|------------|--------|------------|
| Segments are merged in listing order | High — it is the shortest code and looks deterministic | Critical — an older value wins over a newer one after a rename, copy or restore, with no error anywhere | Rule 2, and the falsifier renames every segment and asserts the answer is unchanged |
| A sealed fact is returned twice | Med — publishing and clearing are two operations | High — a duplicated datom reads as a fact asserted twice rather than as an error | Rule 6: one exclusive hold covers both |
| A missing block is swallowed as an empty result everywhere | Med — the refusal is inconvenient in a loop | High — a genuine read error becomes "this entity has no facts" | Rule 7, translated in one named place |
| A zero snapshot returns nothing and looks like an empty entity | Med — a caller forgets one of two fields | High — a read silently answers about the beginning of time | Rule 8, and a test asserting the refusal |
| A leftover partial file is opened at start-up | Med — a crash leaves one by design | Med — start-up fails on a file that was safe to ignore | Rule 10, with a test that puts one in the directory |

## Rollback

A leaf directory holds only segments, so reverting means deleting the directory.
⚠ There is no earlier layout to migrate from — but that ends the moment a leaf
holds data somebody wants, which is why rule 2 is settled now: a store that merged
by filename could not be changed to merge by transaction without rewriting every
leaf, because the fix would need the order the filenames used to imply.

## Follow-ups

- [ ] When compaction exists (`BACKLOG.md` §15), confirm it preserves rule 2 — a compactor that writes a merged segment must not assume its output is read after the inputs, because nothing about the filenames says so.
- [ ] Measure read cost against segment count before choosing a seal policy (`BACKLOG.md` §15); "linear in seals" is arithmetic here, not a measurement.
- [ ] When a leaf is replicated (`BACKLOG.md` §18), confirm a follower can be built by copying files in any order — rule 2 is what should make that true, and it is worth checking rather than assuming.
