# ADR-017: Make the read path lock-free by publishing a watermark, never by guarding mutable state

**Status:** Accepted
**Date:** 2026-09-04
**Owner:** M
**Spec:** None — no spec stage
**Cross-references:** `docs/adr/README.md`, `docs/adr/ADR-002-transaction-identity.md`, `docs/adr/ADR-003-transaction-boundary.md`, `docs/adr/ADR-004-durability-policy.md`, `docs/adr/ADR-005-segment-format.md`, `docs/adr/BACKLOG.md`
**Governs:** `internal/core/tail/**`
**Enforced-by:** `internal/core/tail/guard_test.go::TestReadersTakeNoLock`
**Invalidates:** none — checked; ADR-003 decided the transaction boundary and that reads and writes scale independently, and this record decides the mechanism that makes the second half true rather than aspirational
**Served-path change:** A read never waits for a write and a write never waits for a read, so a read burst cannot slow ingest and a long scan cannot stall a writer.

## Context

ADR-003 established CQRS: one writer per entity, many readers, scaling
independently. That is a statement about topology. It does not say what happens
when a reader and a writer touch the same bytes at the same instant, and nothing
in the corpus does.

For sealed data the question is already answered and needs no mechanism. ADR-005
made a segment immutable, so a reader of sealed bytes races nothing — the bytes
are write-once, and a reader performs no synchronization at all. ADR-002 supplies
the rest: every datom carries a totally ordered transaction identifier and
storage is append-only, so a read bounded by a transaction point simply does not
include what a concurrent writer is appending at a higher one. That is MVCC, and
it is why writers do not block readers here.

**The live tail is the one mutable thing in the system, and it is the whole
decision.** A reader of the live tail is reading a structure a writer is
appending to. The obvious answer is a read-write lock, and it is the wrong one
for three reasons: a reader then blocks a writer for the duration of a scan; a
long scan on a hot leaf becomes a write stall on the leaf ADR-003 gave a single
writer to; and the cost is paid on every read forever to protect against a window
of a few nanoseconds.

The alternative is to make the unfinished state UNREACHABLE rather than guarded.
A writer completes an entry, then publishes it with one atomic store. A reader
loads that watermark once and reads only below it. A partially written entry is
not protected by a lock — it is simply not addressable, because nothing points at
it yet.

⚠ This record exists because the choice constrains every data structure the
storage engine may later use. A structure that must be mutated in place to be
read — a rebalancing tree, a slice that reallocates on append, a map — cannot be
made lock-free after the fact. Deciding it late means discovering it as a rewrite.

## Existing Primitives Audit

- `internal/core/tx` (ADR-002): supplies `TxID` and its total order. **Reused
  whole** as the read bound. A snapshot is a `TxID`; visibility is a comparison
  the corpus already has one implementation of.
- `internal/core/temporal` (ADR-002, T3): supplies `Visible`, the only place two
  timestamps are compared. **Reused whole.** This record adds no second
  visibility rule — a reader excludes a concurrent write because the write's
  identifier is above the reader's bound, which is the rule that already exists.
- `internal/core/segment` (ADR-005): supplies immutability for sealed data.
  **Relied on rather than reused**: it is the reason this record only has to
  solve the live tail.
- `sync/atomic`: **used directly.** A watermark is one unsigned integer with
  release-store and acquire-load semantics, which the standard library provides;
  wrapping it would add a layer over a primitive whose whole value is that it has
  no layers.

## Decision

**Nothing a reader can see is ever mutated in place. Publication is a single
atomic store, and it happens after the thing being published is complete.**

1. **A reader takes no lock, ever.** The only synchronization on a read path is
   an acquire-load. There is no reader-side mutex, no reference count, and no
   epoch to enter and leave.

2. **The live tail is append-only and holds entries in fixed chunks.** A chunk
   holds 256 entries, so the offset within a chunk is one byte and locating an
   entry is a shift and a mask — the same eight-bit step ADR-001 uses to descend
   the address space. A chunk, once written, is never moved; growth adds chunks
   rather than reallocating what readers hold.

3. **A writer publishes by advancing a watermark, and only after the entry is
   completely written.** The store is the release; a reader's load is the
   acquire. Everything the writer did before it is visible to a reader that
   observes it, and everything after is unreachable.

4. **A reader loads the watermark ONCE and reads against that bound.** Repeatable
   reads fall out rather than being built: a scan sees a fixed prefix of the tail,
   and a concurrent append is not in it. Reading the same snapshot twice gives the
   same answer, always.

5. **Sealing publishes an immutable manifest by an atomic pointer swap.** A
   manifest is never edited. A reader loads the pointer once and holds a
   consistent view of which segments exist; a segment appearing or being reclaimed
   is a new manifest, not an edit to the one somebody is reading.

6. **Writers serialize only with each other.** A single writer per leaf is
   ADR-003's boundary and ADR-009's mechanism, and it is a different question from
   this one. Writer-versus-writer contention is real; writer-versus-reader
   contention does not exist.

7. **A read is bounded by a `tx.TxID`, not by wall time.** The watermark is a
   position; the transaction identifier is the meaning. The first says what has
   been published, the second says what the reader asked to see, and a snapshot
   is the pair.

**What would falsify this.** A reader that must block for correctness. If any
future structure on the read path cannot be published atomically — an index that
must be rebalanced in place, a cache that must be invalidated synchronously —
then the rule is broken and the record must be revisited rather than the rule
quietly relaxed for that one structure. The check is mechanical and is this
record's `Enforced-by:`.

## Alternatives Considered

- **A read-write mutex over the live tail.** Correct, obvious, and available in
  ten lines. Rejected because a reader then blocks the writer: `sync.RWMutex`
  makes readers cheap relative to each other, not free relative to a writer, and a
  long scan on a hot leaf becomes a write stall on exactly the leaf that is busy.
  It also inverts the cost — paid on every read forever, to close a window of
  nanoseconds.
- **Copy-on-write of the whole tail per append.** Readers hold an immutable
  snapshot, which is simple and obviously correct. Rejected on cost: it is O(n)
  allocation and copying per append on the write path, and the live tail is
  exactly the structure that is appended to most.
- **Epoch-based reclamation or hazard pointers.** The general solution, and
  necessary when memory under a reader must be reclaimed. Rejected as unnecessary
  HERE: chunks are never moved and the tail is bounded by the sealing interval, so
  a reader's chunks stay alive because the tail holds them, and Go's garbage
  collector already handles the manifest swap. Adopting a reclamation scheme
  before anything needs one would be paying its complexity for nothing.
- **A lock-free structure from a library.** Rejected because the requirement is
  narrow — append-only, single writer, many readers — and that shape is a
  watermark and a chunk list. A general concurrent structure would solve
  multi-writer problems this design does not have, at a cost in code nobody here
  can audit.
- **A sequence lock (seqlock) over each entry.** Readers retry on a torn read.
  Rejected: it makes a reader's cost unbounded under write pressure, which is the
  behaviour this record is trying to avoid, and it protects mutation in place —
  the thing rule 1 refuses on principle.

## Component / Boundary Impact

One new component, `internal/core/tail`, owning the live tail and its
publication. It has one reason to change: how an unpublished entry becomes a
published one.

⚠ The boundary that matters: this component owns PUBLICATION, not durability and
not ordering. ADR-002 mints the identifiers, ADR-004 decides how many copies
exist, and ADR-009 will decide who is allowed to write. A tail that also decided
any of those would be a second authority over a settled question.

## Wiring & Contract Changes

| Surface | Change | Producer | Consumer(s) |
|---------|--------|----------|-------------|
| `tail.Tail` | new — append-only live tail, one writer, many lock-free readers | T1 | T2, and any future storage engine |
| `tail.Watermark` | new — a published position, loaded once per read | T1 | T2 |
| `tail.Entry` | new — one published transaction's datoms plus its `tx.TxID` | T1 | T2 |
| `tail.Snapshot` | new — a `Watermark` paired with the `tx.TxID` bound a reader asked for | T2 | callers |
| `tail.ErrWriterNotHeld` | new sentinel — an append attempted without the writer token | T1 | callers |
| `internal/core/tx` | consumed unchanged | — | — |

## Inter-task Contracts

| Contract | Producing task | Consuming task(s) | Breaking? |
|----------|----------------|-------------------|-----------|
| `tail.Tail`, `tail.Watermark`, `tail.Entry` | T1 | T2 | No — T2 is written against T1 and does not exist before it |
| `tail.Snapshot`, the no-lock guard | T2 | none yet | No |

## Implementation

Two tasks, sequential. See `docs/adr/ADR-017-lock-free-read-path/tasks/README.md`.

## Consequences

- **Positive:** A read burst cannot slow ingest, and a long scan cannot stall a
  writer. The two paths ADR-003 said scale independently actually do.
- **Positive:** Repeatable reads are free rather than built. A snapshot is one
  loaded integer, so a scan is consistent by construction and nothing has to hold
  anything open.
- **Positive:** The rule is mechanically checkable. "No reader takes a lock" is a
  property a test can assert over the source, so it degrades visibly rather than
  silently.
- **Negative:** The structure is constrained. Anything on the read path must be
  publishable atomically, which rules out in-place rebalancing and means a future
  index has to be designed around the rule rather than fitted to it afterwards.
  That constraint is the point, and it is why the record exists now.
- **Negative:** Chunked storage wastes the tail of a partially filled chunk, and
  costs an indirection per entry lookup against a flat slice.
- **Neutral:** Writers still contend with each other. Nothing here makes a leaf
  accept two concurrent writers, and nothing here is meant to.

## Out of Scope

- Who is allowed to be the writer, and how that is decided or fenced (permanent: boundary: ADR-009 owns leader election and fencing epochs; this record owns only what a writer does once it is the writer)
- Durability of anything in the tail — an entry is published, which is not the same as being safe (permanent: boundary: ADR-004 owns how many copies exist and the floor below which a write is refused)
- When the tail is sealed into a segment, and what triggers it (deferred: `docs/adr/BACKLOG.md` §15)
- Memory reclamation of chunks a reader may still hold (permanent: fact: chunks are never moved and are held by the tail itself, so Go's garbage collector reclaims a chunk only once no reader references it; citation: url https://go.dev/ref/mem)
- Any index over the tail, and whether one can be published under this rule (deferred: `docs/adr/BACKLOG.md` §15)
- Lock-freedom on the WRITE path (permanent: boundary: a leaf has one writer by ADR-003, so writer-versus-writer contention is a consensus question and not a data-structure one)

## Risks

| Risk | Likelihood | Impact | Mitigation |
|------|------------|--------|------------|
| A future structure on the read path cannot be published atomically and a lock is added "just here" | Med | High — the guarantee is gone and nothing reports it | `TestReadersTakeNoLock` is a source-level guard over the package, with a positive control proving it can fail |
| An entry is published before it is completely written, so a reader sees a partial one | Low | Critical — a torn read returns data that never existed | The watermark advances only after the write; a concurrency test runs writers and readers together under the race detector and asserts every observed entry is whole |
| The race detector reports clean because the test never actually overlaps a reader and a writer | Med | High — a green suite proving nothing | The test asserts that overlap OCCURRED, not only that nothing raced: a run where the reader saw the watermark advance is required, and a run that never observed growth fails |
| Chunk growth reallocates an index a reader is holding | Low | High | Growth publishes a new chunk index by atomic store; existing chunks are never moved, so a reader holding an older index still addresses valid memory |

## Rollback

No persistent state, so rollback is a code revert. Nothing this record decides is
written to a disk: a watermark is a runtime position, and the format records own
everything that outlives a process.

The constraint it imposes is the part that is expensive to undo — a structure
designed to be published atomically is not harmed by later being locked, but the
reverse is a rewrite, which is the asymmetry that argues for deciding it now.

## Follow-ups

- [ ] When the storage engine lands, confirm the segment manifest is swapped rather than edited, and that no read path acquires the writer's mutex — the guard covers `internal/core/tail` only, and the rule is meant to cover every reader.
- [ ] When an index over the tail is designed (`BACKLOG.md` §15), verify it can be published under this rule before it is built, not after.
- [ ] When ADR-015's admission control lands, check that shedding a read does not require a reader-side lock; a shed decision on the read path would reintroduce exactly what this record removes.
