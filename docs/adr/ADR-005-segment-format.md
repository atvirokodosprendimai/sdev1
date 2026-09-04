# ADR-005: Store datoms in immutable segments of independently coded blocks

**Status:** Accepted
**Date:** 2026-09-04
**Owner:** M
**Spec:** None — no spec stage
**Cross-references:** `docs/adr/README.md`, `docs/adr/ADR-001-address-space.md`, `docs/adr/ADR-002-transaction-identity.md`, `docs/adr/ADR-004-durability-policy.md`, `docs/adr/BACKLOG.md`
**Governs:** `internal/core/segment/**`
**Enforced-by:** `internal/core/segment/segment_test.go::TestBlockCarriesItsOwnCodec`
**Invalidates:** none — checked; no record has yet decided how bytes are laid out
**Served-path change:** A read fetches and decompresses one block rather than a whole file, and a corrupt block is detected on read instead of returned as data.

## Context

ADR-001 gave a leaf an address, ADR-002 an ordering, ADR-003 a boundary and
ADR-004 a durability policy. None of them says what is actually on disk. This
record does, and it is the most format-locked decision in the corpus: every byte
ever stored is written under it.

Four requirements shape it.

**Compression is controllable per write.** An operator can enable or disable it,
which means the choice varies between writes and therefore cannot live only in
configuration.

**Space must be reclaimable cheaply**, which points at many small units that can
be dropped whole rather than one large file that must be rewritten.

**A block should be a few megabytes** — large enough for compression to work and
for an erasure fragment to be meaningful, small enough that a random read does
not pull an unreasonable amount.

**Data will be encrypted per subject and erasure-coded**, both of which act on
these bytes, so their order relative to compression is fixed by this record
rather than chosen later.

## Existing Primitives Audit

- `internal/core/tx` (ADR-002, T2): supplies a fixed-width, byte-comparable
  transaction identifier. **Reused as the sort key's tail** — the whole reason
  that encoding exists is so an index can order without decoding, and this is the
  index.
- `internal/core/addr` (ADR-001, T1, as amended by ADR-016): supplies the leaf
  identifier a segment header records. **Reused as-is.**
- `internal/core/ports` (ADR-003, T1): supplies the datom. **Reused**; this
  record encodes it and does not redefine it.
- Go's `hash/crc32` and `compress/*`: **reused** for the checksum and the
  no-dependency codec. A third-party codec is registered rather than assumed, so
  the format does not depend on any one library being present to read a block
  written without it.

## Decision

**A segment is an immutable file holding many independently coded blocks, with a
block index at its tail. Every block records how it was written.**

1. **A block is self-describing.** Its header carries the codec, the cipher, the
   uncompressed and stored lengths, and a checksum of the stored bytes. A block
   is readable by anything that can read the header, without consulting
   configuration.
2. ★**The codec is recorded per block, never only configured.** A block written
   with one codec is only readable by something that knows which. Holding the
   choice in configuration alone means a settings change reinterprets existing
   data — the failure this corpus has now rejected for the fan-out, the erasure
   scheme and the tenant width. A query clause may set the codec for NEW blocks;
   it can never change what an existing block means.
3. **The block size is a recorded value, not a constant.** The default is 4 MiB,
   chosen as a starting point rather than measured, and the header records the
   actual sizes so the default may change without reinterpreting anything.
4. ★**The pipeline order is fixed: compress, then encrypt, then erasure-code.**
   It is not a choice. Encrypting first destroys compressibility, because
   ciphertext does not compress. Coding first means compressing parity, which is
   waste and breaks the fragment structure. The header records which stages ran,
   so a reader applies their inverses in the right order without being told.
5. **Every block carries a checksum of its stored bytes, verified on read.** At
   scale, silent corruption is routine rather than hypothetical, and a coded
   stripe makes it worse: erasure decoding assumes it knows WHICH fragments are
   missing, so feeding it a present-but-rotten fragment returns wrong data with
   no error. The checksum is what turns that into a detected fault.
6. **A segment header carries a format version**, and an unknown version is
   refused rather than partially read.
7. **Datoms within a block are sorted by a byte-comparable key** — entity,
   attribute, then transaction identifier — so a reader can binary-search a block
   and an index can compare without decoding.

**What would falsify this decision.** The load-bearing claim is rule 2: that a
block is interpretable from its own header alone. It fails if any stage of
reading a block needs a value the block does not carry. That is checkable today
with no cluster: write a block under one configuration, read it under a
different one, and require the bytes back.
`TestBlockCarriesItsOwnCodec` is that probe.

**Validity.** The 4 MiB default is a starting figure and is explicitly not a
measured optimum. Nothing here justifies it beyond being a reasonable order of
magnitude, and the header exists so it can be changed on evidence.

## Alternatives Considered

- **One file per block.** Reclaiming space becomes an unlink, the path names the
  block directly, and nothing needs an index. Rejected on inode count: at the
  scale this system targets that is billions of files, and traversal, repair and
  backup all become proportional to file count rather than to bytes, while every
  block read costs an open and a directory lookup. It also forecloses object
  storage later, where one file per block means millions of individual requests.

- **One large file per leaf, compacted in place.** Fewest files. Rejected
  because reclaiming any space requires rewriting the file, so the cost of
  dropping one expired block is proportional to everything that is not expired.

- **Compress the whole segment rather than each block.** Better ratio, since the
  compressor sees more context. Rejected because a random read would then have
  to decompress from the start of the file, which is the operation the block
  structure exists to avoid.

- **A codec fixed for the whole cluster.** Simplest, and it removes a header
  field. Rejected because the requirement is that compression is controllable
  per write, and because a cluster-wide codec is exactly the configuration-held
  format assumption this corpus has rejected three times.

- **Encrypt before compressing.** Would let the compressor run on already-safe
  bytes. Rejected because ciphertext does not compress: the stage would cost CPU
  and save nothing.

## Component / Boundary Impact

| Component | Owns | One reason to change? |
|-----------|------|-----------------------|
| `internal/core/segment` | The on-disk shapes: segment header, block header, the block index, the codec registry, and the encode/decode path. No file I/O — it works on byte slices and readers so the format is testable without a disk. | Yes — changes only when the format changes, which is the most expensive kind of change here. |

## Wiring & Contract Changes

| Surface | Change | Producer | Consumer(s) |
|---------|--------|----------|-------------|
| `segment.FormatVersion` | new | `internal/core/segment` (T1) | every reader and writer of stored bytes |
| `segment.BlockHeader`, `segment.Header` | new | `internal/core/segment` (T1) | ADR-006's coder, ADR-010's sweep |
| `segment.Codec`, `segment.RegisterCodec`, `segment.CodecID` | new | `internal/core/segment` (T2) | the query clause that selects a codec |
| `segment.Writer`, `segment.Reader`, `segment.Index` | new | `internal/core/segment` (T3) | the storage engine, when it exists |

## Inter-task Contracts

| Contract | Producing task | Consuming task(s) | Breaking? |
|----------|----------------|-------------------|-----------|
| `segment.BlockHeader`, `segment.FormatVersion`, the checksum | T1 | T2 | No — new surface |
| `segment.Codec`, the registry | T2 | none in this record | No — new surface |

## Implementation

Two tasks. See [`ADR-005-segment-format/tasks/README.md`](ADR-005-segment-format/tasks/README.md).

The block index and the segment writer are deliberately NOT in this record. They
need the storage engine's file handling to be worth building, and a format that
is correct on byte slices is the part that must be right before anything is
written to a disk.

## Consequences

- **Positive:** a block is interpretable from its own bytes, so a settings change
  can never reinterpret stored data and a reader from a future release can still
  read an old block or refuse it explicitly.
- **Positive:** a random read decompresses one block, not a file.
- **Positive:** silent corruption becomes a detected fault rather than wrong data
  returned as fact — which is a prerequisite for erasure coding rather than a
  nicety, since decoding cannot detect a rotten fragment by itself.
- **Negative:** per-block compression compresses worse than whole-file, because
  the compressor sees less context each time. That is paid deliberately for
  random access.
- **Negative:** a header per block is fixed overhead on every block, which
  matters more the smaller blocks get and bounds how small they can usefully be.
- **Neutral:** the format is versioned, which means a reader must handle refusal
  as a normal outcome rather than an error state.

## Out of Scope

- The erasure code itself (permanent: boundary: ADR-006 owns coding; this record fixes only where it sits in the pipeline and that the header records it ran)
- Per-subject encryption keys and their destruction (permanent: boundary: ADR-007 owns erasure; this record fixes that the cipher is recorded per block)
- The segment writer, the block index, and anything touching a file (deferred: `docs/adr/BACKLOG.md` §12)
- Attribute and entity interning, the largest available size reduction (deferred: `docs/adr/BACKLOG.md` §12)
- Whether one compression block may mix subjects (deferred: `docs/adr/BACKLOG.md` §13)

## Risks

| Risk | Likelihood | Impact | Mitigation |
|------|------------|--------|------------|
| A codec is held in configuration rather than in the header | Medium | **High** — a settings change silently reinterprets stored data | Rule 2, and `TestBlockCarriesItsOwnCodec` reads a block back under a different configuration and requires the bytes |
| A rotten block is fed to an erasure decoder and returns wrong data | Medium | **High** — wrong data returned as fact, with no error anywhere | Rule 5: every block is checksummed and verified on read, which is what makes the fault detectable at all |
| The pipeline order is rearranged for a plausible-sounding reason | Low | High — either compression stops working or parity gets compressed | Rule 4 states the order and why; the header records which stages ran so a reader cannot guess |
| 4 MiB proves wrong by an order of magnitude | Medium | Low | It is a recorded value rather than a constant, so changing it reinterprets nothing |
| Compressing before encrypting leaks through size when a block mixes subjects | Medium | Medium | Named and deferred to `BACKLOG.md` §13 rather than assumed away; the mitigation is a packing rule, which belongs with whatever record decides packing |

## Rollback

This governs stored bytes, so after data exists there is no rollback — only a
rewrite of every segment. That is the nature of a format decision and the reason
this record is written before a storage engine exists.

Before data exists: revert the branch.

The version field is what makes a FUTURE change survivable: a reader meeting a
version it does not know refuses explicitly rather than misreading, so an
incompatible change becomes a migration rather than a corruption.

## Follow-ups

- [ ] When ADR-006 lands, confirm the coder writes its stage into the block header rather than assuming every block in a sealed segment is coded.
- [ ] When ADR-007 lands, confirm the cipher identifier is per block and the key identifier is not stored beside the ciphertext, since storing them together defeats the point of destroying the key.
