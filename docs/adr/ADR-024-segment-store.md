# ADR-024: A sealed segment is blocks then an index then a trailer, and it exists only when it is complete

**Status:** Accepted
**Date:** 2026-09-04
**Owner:** M
**Spec:** None — no spec stage
**Cross-references:** `docs/adr/README.md`, `docs/adr/ADR-005-segment-format.md`, `docs/adr/ADR-006-erasure-coding.md`, `docs/adr/ADR-011-query-language.md`, `docs/adr/ADR-017-lock-free-read-path.md`, `docs/adr/ADR-020-commit-point.md`, `docs/adr/BACKLOG.md`
**Governs:** `internal/core/segstore/**`
**Enforced-by:** `internal/core/segstore/segstore_test.go::TestAnUnsealedSegmentDoesNotExistAtItsPath`
**Invalidates:** none — ADR-005 fixed what a BLOCK is and deliberately said nothing about how a run of them becomes a file, or how one is found again
**Served-path change:** Blocks can be written to a disk and read back by key, so data outlives a process for the first time — where before every byte this engine understood lived only in memory.

## Context

Twenty-three records describe how bytes are laid out, compressed, encrypted,
coded, addressed, versioned in time and searched. Nothing writes one to a disk.
Everything that runs today — the session, the index, the traversal — lives in a
map and is gone when the process exits.

This is the largest gap in the corpus and the one almost every remaining `pending`
task traces back to. It is also, unusually, blocked by nothing at all: writing
bytes to a file needs no dependency, no network and no cluster. It was simply not
done yet.

There is one rule that cannot be got wrong, and it was decided two records ago.

⚠ **ADR-017 says nobody observes a half-sealed segment.** Its read path takes no
locks precisely because sealed data is immutable — a reader that could see a
segment mid-write would have to coordinate with the writer, and the whole
lock-free argument collapses. A file being written under its final name IS a
half-sealed segment, visible to anyone who lists the directory.

★ So the question this record answers is not "what does the file look like" but
"when does the file *exist*". The layout follows from that.

## Existing Primitives Audit

- `internal/core/segment` (ADR-005): supplies `Header`, `BlockHeader`,
  `EncodeBlock`, `DecodeBlock` and `Checksum`. **Reused whole** — this record adds
  a container and changes nothing about what a block is. A second block format
  here would be the drift ADR-005's self-describing header exists to prevent.
- `internal/core/ports` (ADR-003): supplies `Datom`. **Not reached** — this
  record stores opaque blocks. What goes IN one is the caller's business, which
  keeps the store usable by the tail, the index and a backup alike.
- The filesystem's `rename(2)`: **relied on** for atomicity within a directory.
  ⚠ That is a real dependency on a real guarantee, and it is the mechanism the
  central rule rests on rather than an implementation detail.
- A key-value library: **none.** The lookup here is a binary search over a sorted
  index written once and never updated — an embedded database would bring a
  mutable store, a write path and a compaction story this record does not want.
- `golang.org/x/sys/unix` for `Mmap`/`Munmap`: **promoted from indirect to
  direct.** ⚠ It is ALREADY in this module's graph — `go.mod` carries
  `golang.org/x/sys v0.30.0 // indirect` behind `klauspost/cpuid` — so rule 9
  adds a dependency EDGE and not a dependency. The frozen `syscall` package
  offers the same two calls with no edge at all; `x/sys` is preferred because
  `syscall` is closed to correction and its per-platform surface no longer moves.

## Decision

**A segment is written to a temporary name and published by an atomic rename; its
index is written last and located from a fixed-size trailer at the end of the
file.**

1. **A segment file is: the ADR-005 header, then a run of blocks, then an index,
   then a fixed-size trailer.** Nothing else, and in that order.

2. **The file is created under a temporary name and renamed into place by
   `Seal`.** ⚠ This is the record. A reader that lists a directory sees only
   complete segments, so ADR-017's claim that sealed data is immutable holds
   without a lock, a flag file, or a convention everyone has to remember.

3. **The index is written LAST, and the trailer after it.** ★ A crash therefore
   leaves a file with no valid trailer — which is not a segment rather than a
   broken one. The distinction matters: "incomplete" is recoverable by deletion,
   "corrupt" needs a human.

4. **The trailer is FIXED WIDTH and read from the end of the file.** One seek
   finds it, whatever the segment's size, with no scan and no separate metadata.
   ⚠ Putting it at the front would mean either knowing the index's size before
   writing the blocks, or seeking back to patch it — and a patch is a second
   write to a published file, which is what rule 2 exists to forbid.

5. **The trailer carries a magic number, a format version and a checksum of the
   index.** ⚠ A truncated or corrupted index must be REFUSED by name, never
   parsed into plausible offsets. An index is a list of byte positions; a wrong
   one reads arbitrary bytes as a block and only the block's own checksum stands
   behind it.

6. **The index is sorted by key and searched, never scanned.** A segment holds
   many blocks and a scan makes every read proportional to the file rather than
   to the answer. ⚠ A key may therefore appear ONCE: a second entry under the same
   key is a block that was written, is paid for on disk, and can never be reached
   by a binary search, so it is refused when it is appended rather than stored
   where nothing will ever find it.

7. **A block is verified on read, by ADR-005's own checksum.** This record adds
   no second integrity mechanism — it locates bytes and hands them to the format
   that knows how to check them.

8. **A missing key is a named refusal, not an empty result.** ⚠ "No such block"
   and "a block containing nothing" are different answers, and a caller acting on
   the second when the first is true writes over an absence it never confirmed.

9. **Reads go through a memory mapping.** A sealed segment is immutable, so a
   mapping can be shared by any number of readers with no coordination — which is
   ADR-017's argument applied to a file — and the kernel's page cache is better at
   deciding what stays resident than this record could be. Targets are macOS and
   Linux.

10. **A caller receives OWNED bytes, never a view into the mapping.** ⚠ A
    returned sub-slice of a mapping outlives the `Reader` that owns it, and
    becomes a dangling pointer the moment `Close` unmaps — a use-after-free in a
    memory-safe language, arriving as a segfault with no stack naming the cause.
    ★ It costs nothing here: ADR-005's `DecodeBlock` already allocates its result,
    so the mapping is used for the parts that stay internal — the header, the
    index, the stored bytes — and never escapes.

11. ⚠ **An I/O error on a mapped page is a SIGBUS, not an error return, and that
    is the price of rule 9.** A failing disk kills the process instead of failing
    the read. It is accepted because a sealed segment is complete and never
    rewritten — rule 2 guarantees that — so the realistic cause is hardware
    rather than a race. It is stated here because it is invisible in the code and
    would otherwise be discovered during an incident.

**What would falsify this.** A segment appearing at its final path before it is
sealed. That is the falsifier in `Enforced-by:`, it is checkable today with a real
filesystem and no cluster, and it is precisely what writing directly to the final
name would produce — which is the simpler implementation.

## Alternatives Considered

- **Write directly to the final path and mark completeness with a flag file or a
  status byte.** Fewer moving parts, one file operation. Rejected under rule 2:
  the segment is observable while incomplete, so every reader must now check the
  flag — and the one that forgets is fast, correct-looking, and wrong only when
  it races a writer.
- **Put the index at the FRONT of the file.** Reads then need no seek to the end.
  Rejected under rule 4: it requires either knowing the index size in advance or
  seeking back to patch the header, and patching a file after its blocks are
  written is a second write to something a reader may already hold.
- **Scan blocks to find a key, and skip the index entirely.** Much simpler, and
  fine for a small segment. Rejected under rule 6: it makes every read cost the
  size of the file, and a segment is deliberately large — that is why blocks are
  batched into one at all.
- **Use an embedded key-value store for the index.** Mature, and it would give
  range scans for free. Rejected in the audit: it brings a mutable store with its
  own write path and compaction, into a design whose central property is that a
  sealed segment never changes.
- **Return an empty block for a missing key.** Convenient for callers that treat
  absence as empty. Rejected under rule 8: it conflates "not here" with "here and
  empty", and the second is a legitimate value.
- **Trust the index and skip the block checksum on read.** One less verification
  per read. Rejected under rule 7: an index is a list of offsets, so a wrong one
  produces arbitrary bytes that look exactly like a block — the checksum is the
  only thing that distinguishes them.
- **Read with `ReadAt` instead of mapping the file.** No mapping to own, an I/O
  error arrives as an error rather than a signal, and the package builds
  everywhere Go does. Rejected under rule 9: every block read becomes a syscall
  and a copy into a buffer this package allocates, while the kernel is already
  holding those exact pages — and a sealed segment is immutable, which is the
  one condition that makes a shared mapping free of coordination. The cost of
  the rejection is stated in rule 11 rather than hidden.
- **Hand the caller a sub-slice of the mapping and skip the copy.** It is the
  reason to map a file at all, and for a caller that only inspects bytes it is
  free. Rejected under rule 10: the slice outlives the `Reader`, so a caller
  holding one past `Close` reads unmapped memory — and it works perfectly until
  the first `Close`, which is the worst schedule a defect can have.

## Component / Boundary Impact

One new component, `internal/core/segstore`, owning the container: how a run of
blocks becomes a file and how one is found again. It has one reason to change:
the on-disk shape of a sealed segment.

⚠ The boundary: it stores OPAQUE BLOCKS. It does not know what a datom is, does
not compress or encrypt — ADR-005 already does both — and does not decide what
belongs in one segment. Keeping it ignorant of its contents is what lets the
live tail, the search index and a backup all use it.

## Wiring & Contract Changes

| Surface | Change | Producer | Consumer(s) |
|---------|--------|----------|-------------|
| `segstore.Writer` / `Create` | new — open a segment under a temporary name | T1 | callers |
| `segstore.Writer.Append` | new — add one block under a key | T1 | callers |
| `segstore.Writer.Seal` | new — write index and trailer, fsync, rename | T1 | callers |
| `segstore.Writer.Abort` | new — discard the temporary file | T1 | callers |
| `segstore.Reader` / `Open` | new — open a sealed segment | T1 | callers |
| `segstore.Reader.Get` / `Keys` | new — locate a block by key | T1 | callers |
| `segstore.Reader.Leaf` | new — which leaf the segment was written for | T1 | callers |
| `segstore.TrailerSize` / `TrailerMagic` | new — the fixed footer | T1 | callers |
| `segstore.ErrNoSuchBlock` / `ErrNotASegment` / `ErrIndexCorrupt` / `ErrClosed` / `ErrSealed` / `ErrDuplicateKey` | new sentinels | T1 | callers |

## Inter-task Contracts

| Contract | Producing task | Consuming task(s) | Breaking? |
|----------|----------------|-------------------|-----------|
| `segstore.Writer`, `segstore.Reader` | T1 | future storage work (`BACKLOG.md` §12) | No |

## Consequences

- **Positive:** Data outlives a process. Everything downstream of "there is no
  storage engine" gains a floor to stand on.
- **Positive:** ADR-017's lock-free read path holds on real files, because an
  incomplete segment is not addressable rather than being guarded.
- **Positive:** A crash leaves a file that is *not a segment*, which is safe to
  delete without judgement.
- **Negative:** Publishing by rename means a segment is written twice as far as
  the directory entry is concerned, and requires the temporary file to live on
  the same filesystem as its destination. That is a real deployment constraint.
- **Negative:** The index is held in memory while writing, so a segment's block
  count is bounded by the writer's memory rather than by the disk.
- **Negative:** A sealed segment cannot be appended to. That is the point, and it
  means the decision of when to seal (`BACKLOG.md` §15) matters more now.
- **Neutral:** Nothing decides what goes in a segment, or when. This stores what
  it is given.
- **Positive:** A read costs no syscall and no copy into this package's own
  buffer, and any number of readers share one mapping of one sealed file — which
  is ADR-017's immutability argument paying for itself at the page level.
- **Negative:** An I/O error on a mapped page raises SIGBUS and kills the
  process, where a `read` would have returned an error (rule 11).
- **Negative:** The package builds on macOS and Linux only. ★ That is
  deliberate: a platform it was not designed for fails to COMPILE, naming the
  file somebody would have to write, rather than building and refusing at run
  time — or worse, silently taking a different read path.
- **Neutral:** A mapping pins the file, so a sealed segment cannot be truncated
  under a reader. Nothing truncates a sealed segment — that is rule 2 — so it
  costs nothing, and it is recorded because it is a real property of the
  mechanism rather than of this design.

## Out of Scope

- When the live tail is sealed into a segment (deferred: `docs/adr/BACKLOG.md` §15)
- Erasure-coding a sealed segment across failure domains (deferred: `docs/adr/BACKLOG.md` §12)
- A manifest naming which segments exist (deferred: `docs/adr/BACKLOG.md` §15)
- Wiring the session onto real storage (deferred: `docs/adr/BACKLOG.md` §28)
- Range scans and iteration order beyond `Keys` (deferred: `docs/adr/BACKLOG.md` §20)
- What a block contains (permanent: boundary: ADR-005 owns the block; this record owns the container, and keeping it ignorant of the contents is what lets the tail, the index and a backup share it)
- Atomicity across filesystems (permanent: fact: `rename(2)` is atomic only within a filesystem, so a temporary file on another device cannot be published this way; citation: url https://pubs.opengroup.org/onlinepubs/9699919799)
- Platforms other than macOS and Linux (permanent: boundary: rule 9 maps the file and the mapping call is per-platform; an unsupported target fails to compile rather than silently taking a different read path)

## Risks

| Risk | Likelihood | Impact | Mitigation |
|------|------------|--------|------------|
| A segment is written directly to its final path | High — it is the simpler implementation and looks identical when nothing crashes | Critical — a reader can observe a half-written segment, and ADR-017's lock-free read path is unsound | The falsifier asserts the final path does NOT exist between the first append and the seal |
| The index is trusted without verifying its checksum | Med | Critical — a corrupt index yields arbitrary offsets, and arbitrary bytes read as a block | Rule 5, with a test that corrupts the index and asserts a named refusal |
| A truncated file is parsed as a valid segment | Med | High — plausible-looking garbage rather than a refusal | The trailer's magic and size are checked before anything is read |
| A missing key returns an empty block | Med | High — a caller cannot tell absence from emptiness and may overwrite a fact it never read | Rule 8, and a test asserting a named error rather than a zero value |
| The temporary file is left behind on a crash | High — by design | Low — wasted space, and it is recognisable | `Abort` removes it; a leftover has no valid trailer, so it can be deleted without inspection |
| A block handed to a caller is a view into the mapping | High — it is the obvious way to write it, and the reason to map a file at all | Critical — a use-after-free that appears only after `Close`, arriving as a signal with no stack naming the cause | Rule 10; ADR-005's `DecodeBlock` already allocates, so the copy costs nothing, and a test reads a block AFTER closing the reader that returned it |
| A read races a `Close` and touches unmapped memory | Med — a reader is shared and one goroutine closes it | Critical — the same signal, and only under a schedule | The mapping's LIFETIME is guarded rather than its data: readers never block each other, and `Get` after `Close` is a named refusal |

## Rollback

The on-disk format is new, so nothing written before it exists. Reverting means
deleting segments — there is no earlier format to migrate from. ⚠ That freedom
lasts exactly as long as nothing is stored, which is why the trailer carries a
format version from the first write rather than being added when it is needed.

## Follow-ups

- [ ] When a manifest exists (`BACKLOG.md` §15), confirm publishing a segment and publishing the manifest that names it are ordered so a manifest never points at a file that is not there — the rename makes each file atomic and says nothing about a set of them.
- [ ] Measure the index's memory cost per block before choosing a segment size (`BACKLOG.md` §15); the block count is bounded by the writer's memory and nothing currently says by how much.
- [ ] When the session is wired to real storage (`BACKLOG.md` §28), confirm a read still verifies the block checksum rather than trusting the index — rule 7 is one line and its absence is invisible until an offset is wrong.
- [ ] When a segment is erasure-coded across failure domains (`BACKLOG.md` §12), decide whether a remote fragment can be mapped at all — rule 9 assumes a local file, and nothing yet says what the read path is when the bytes are on another server.
