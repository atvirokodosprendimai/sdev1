# ADR-025: A datom is encoded in a versioned run, and a short read is a refusal rather than a retraction

**Status:** Accepted
**Date:** 2026-09-04
**Owner:** M
**Spec:** None — no spec stage
**Cross-references:** `docs/adr/README.md`, `docs/adr/ADR-002-transaction-identity.md`, `docs/adr/ADR-003-transaction-boundary.md`, `docs/adr/ADR-005-segment-format.md`, `docs/adr/ADR-022-write-statements.md`, `docs/adr/ADR-023-links-and-traversal.md`, `docs/adr/ADR-024-segment-store.md`, `docs/adr/BACKLOG.md`
**Governs:** `internal/core/datom/**`
**Enforced-by:** `internal/core/datom/datom_test.go::TestATruncatedRunIsRefusedRatherThanZeroFilled`
**Invalidates:** none — ADR-005 fixed what a BLOCK is and ADR-024 what a SEGMENT is; neither says what goes inside one, and nothing in the corpus has ever said what a datom looks like in bytes
**Served-path change:** None a caller can observe yet. This decides a byte layout, and it is the thing standing between a datom and a disk — everything downstream of "writes reach memory and stop there" (`BACKLOG.md` §28) needs it first.

## Context

There is a block format, a segment format, and a store that writes segments to a
disk and finds a block again by key. There is no way to put a **datom** in one.

`ports.Datom` exists only as a Go struct. Nothing serialises it, so the engine
can write bytes it cannot fill and read blocks it cannot interpret. That is the
whole of this gap, and it is the last purely local one: it needs no network, no
cluster, and no decision anybody has deferred.

An encoding is also the least retrofittable thing in this repository. A format is
wrong exactly once and then there is data in it, which is why this is a record and
not an implementation detail.

★ **One hazard drove this record and is worth stating before the decision.** A
retraction is a datom with `Assert` cleared, never an absent datom — ADR-003 fixed
that, because "this stopped being true" and "this was never recorded" are
different facts. Go's zero value for a `bool` is `false`. So a decoder that
tolerates a short buffer and returns what it managed to fill does not produce a
partial answer: it produces a **retraction of a fact that was asserted**. It
withdraws data, it reports success, and nothing about the result looks damaged.

## Existing Primitives Audit

- `internal/core/ports` (ADR-003): supplies `Datom`, including `Assert` and
  `IsReference`. **Reused whole** — this record encodes that struct and adds no
  field. A parallel wire type would be a second definition of what a fact is, and
  the two would drift.
- `internal/core/temporal` (ADR-002): supplies `Interval` and `Forever`.
  **Reused** — `Forever` is already the corpus's name for an unbounded end, so
  this encoding writes it rather than inventing a sentinel of its own.
- `internal/core/tx` and `internal/core/hlc` (ADR-002): supply `TxID`,
  `Timestamp` and `addr.LeafID`. **Reused whole**, written out in full; the
  alternative is examined and rejected below.
- `internal/core/segment` (ADR-005): supplies `Checksum` and the block header.
  **Deliberately not reached.** This record adds NO integrity mechanism — the
  block that carries a run already has one, and a second would be two answers to
  one question.
- `encoding/gob`, protobuf, or any reflective codec: **none.** A self-describing
  general format decides field presence, defaulting and evolution for you, and
  every one of those decisions is a decision this record has to make on purpose —
  starting with the one about `Assert` above, which a codec that omits zero values
  gets exactly wrong.

## Decision

**Datoms are encoded as a versioned RUN — a header naming the format and a count,
then fixed-width fields and length-prefixed bytes — and every refusal is named.**

1. **The unit is a run of datoms, not one datom.** ⚠ A format version per datom
   would cost a byte on every fact for a property that is constant across
   millions; a version nowhere would make the bytes unreadable by a future build.
   The run header carries it once. ★ The run is still self-contained, which is the
   level at which ADR-005's rule is actually stated: a block is readable from its
   own bytes, and a run is what a block holds.

2. **Every field is written, always.** No field is omitted because it is empty,
   zero, or false. ⚠ This is the record. An encoding that omits a false `Assert`
   saves one bit and makes a retraction indistinguishable from a truncation.

3. **A short buffer is a named refusal, never a partially filled datom.** ⚠ The
   falsifier, and the reason for it is in the Context: a zero-filled `Datom` is a
   **retraction**, so a tolerant decoder withdraws facts and reports success.

4. **Both validity endpoints are always present, and an unbounded end is
   `temporal.Forever`.** ⚠ Never absent and never zero. ADR-022 refuses an omitted
   `VALID` clause defaulting to zero because it would claim a fact had been true
   since the beginning of time; the same mistake is available here from the other
   direction, where a missing endpoint decodes as an interval that ended at the
   epoch.

5. **`Assert` and `IsReference` are bits in a flags byte that is always written.**
   ⚠ Neither is ever inferred. Inferring `IsReference` from the shape of a value
   is what ADR-023 forbids; inferring `Assert` from anything is what rule 2
   forbids. ⚠ **An unrecognised bit in that byte is a refusal, not an ignored
   bit** — within a known version it can only be corruption, and a decoder that
   masks it off returns a datom that decodes cleanly and means something else.

6. **Every length is checked against what remains BEFORE anything is allocated.**
   ⚠ A length field is a number a corrupt block chooses. Trusting one is how a
   flipped bit becomes a request for four gigabytes.

7. **Big-endian, matching ADR-005.** Not because it is faster — it is not — but
   because one endianness in one repository is one fewer thing to be wrong about,
   and a mixed-endian format fails only on the fields somebody forgot.

8. **The order given is the order written.** ★ The encoder does not sort. EAVT is
   a storage order and belongs to whatever decides layout (`BACKLOG.md` §15/§20);
   an encoder that sorted would make the order a caller actually wrote
   unrecoverable, and would do it silently.

9. **A zero-length value decodes to an empty non-nil slice.** This is the one
   normalisation, stated rather than accidental: in Go `nil` and `[]byte{}` differ,
   and as facts they do not. Round-tripping the difference would promote a
   language detail into a semantic one.

10. **No checksum.** ⚠ The block carrying this run is checksummed by ADR-005 and
    verified on read by ADR-024. A second checksum here would be a second answer
    to "are these bytes intact", and the two would eventually disagree.

**What would falsify this.** A truncated run decoding into datoms rather than
being refused. That is the falsifier in `Enforced-by:`, it needs nothing but a
byte slice, and it is exactly what the obvious implementation produces — because
"read what is there" is the natural way to write a decoder.

## Alternatives Considered

- **Elide `TxID.Leaf` and take it from the segment header.** It saves 33 bytes on
  every datom, and it is very probably always correct: a segment belongs to one
  leaf, and a transaction touching an entity is minted at the leaf that entity
  descends to. Rejected twice over. It makes a run readable only in the context of
  the segment around it, which is the property ADR-005 exists to protect. And
  "very probably always correct" is a cross-record invariant that nothing checks —
  the day it is false, the encoding loses information silently.
- **Intern entity names, attribute names and transaction identifiers in a
  per-segment dictionary.** This is the largest size reduction available and
  `BACKLOG.md` §12 has said so since ADR-005. Rejected *here* rather than
  dismissed: interning is one decision about where a dictionary lives, not three
  about which fields to shrink, and taking it inside a record about a datom's
  fields would answer it by accident. Re-deferred below with a fresh pointer.
- **Use `encoding/gob`, protobuf, or another reflective codec.** Far less code,
  and schema evolution comes free. Rejected under rules 2 and 5: these formats
  decide presence and defaulting for you, and several omit zero values on the
  wire — which turns `Assert: false` into an absent field and a retraction into a
  gap. The decision this record most needs to control is the one such a codec
  makes silently.
- **A variable-length integer encoding for the numeric fields.** Most datoms have
  small sequence numbers and short values, so it would shrink the fixed part
  substantially. Rejected for now on the same ground as interning: it is a size
  decision, and taking it before anything has measured a real corpus would be
  choosing on aesthetics. The format is versioned from the first write precisely
  so this stays available.
- **Tolerate a truncated run and return the datoms that decoded.** It is what a
  caller often wants from a damaged file, and it looks like robustness. Rejected
  under rule 3: the last datom of a truncated run is not missing, it is *wrong* —
  and specifically it is a retraction. Robustness that withdraws facts is not
  robustness.
- **Encode one datom per call rather than a run.** A simpler signature, and it
  composes. Rejected under rule 1: the version then has to live per datom or
  nowhere, and both are worse than paying for it once per run.

## Component / Boundary Impact

One new component, `internal/core/datom`, owning exactly one thing: the byte
layout of a fact. It has one reason to change — the layout.

⚠ The boundary: it does not decide **which** datoms are encoded together, what
they are keyed by, where the bytes go, or in what order they are stored. Those
belong to whatever puts a run in a block (`BACKLOG.md` §15, §28). Keeping this
package ignorant of storage is what lets the tail, a segment, a backup and a
replication stream all carry the same bytes.

## Wiring & Contract Changes

| Surface | Change | Producer | Consumer(s) |
|---------|--------|----------|-------------|
| `datom.Encode` | new — a run of datoms to bytes | T1 | callers |
| `datom.Decode` | new — bytes back to a run of datoms | T1 | callers |
| `datom.FormatVersion` | new — the run format's version | T1 | callers |
| `datom.MaxNameLen` / `datom.MaxValueLen` | new — what the length prefixes can express | T1 | callers |
| `datom.ErrShortRun` / `datom.ErrUnknownVersion` / `datom.ErrTooLong` / `datom.ErrReservedFlag` | new sentinels | T1 | callers |

## Inter-task Contracts

| Contract | Producing task | Consuming task(s) | Breaking? |
|----------|----------------|-------------------|-----------|
| `datom.Encode`, `datom.Decode` | T1 | a leaf store on segments (`BACKLOG.md` §28) | No |

## Consequences

- **Positive:** A datom can be written to a disk and read back. Everything that
  was blocked on "there is no storage engine" and then on "there is no way to put
  a fact in one" now has both halves.
- **Positive:** The one failure that would have been worst — a truncation
  presenting as a retraction — is a named refusal with a test that fails when the
  refusal is removed.
- **Negative:** A datom costs 74 fixed bytes plus its names and value, and 49 of
  those are the transaction identifier. That is a real cost on a store whose whole
  purpose is holding many small facts, and it is paid deliberately so that the
  dictionary question is answered once, on purpose, rather than three times by
  accident.
- **Negative:** Refusing an unrecognised flag bit means a version-1 decoder cannot
  read a version-1 run written by a build that added a flag without bumping the
  version. That is intended: such a build is the defect.
- **Neutral:** No compression, no checksum, no interning. The layers that own
  those already run over these bytes.

## Out of Scope

- Interning entity names, attribute names or transaction identifiers (deferred: `docs/adr/BACKLOG.md` §12)
- A variable-length integer encoding for the numeric fields (deferred: `docs/adr/BACKLOG.md` §12)
- Which datoms are grouped into one run, and what a run is keyed by (deferred: `docs/adr/BACKLOG.md` §15)
- Storing a run in a segment, and reading a leaf back from disk (deferred: `docs/adr/BACKLOG.md` §28)
- The order datoms are stored in, EAVT or otherwise (deferred: `docs/adr/BACKLOG.md` §20)
- Compression and encryption of these bytes (permanent: boundary: ADR-005 owns the pipeline that runs over a block, and a run is what a block holds; doing either here would apply it twice)
- Verifying these bytes are intact (permanent: boundary: rule 10 — ADR-005 checksums the block and ADR-024 verifies it on read, and a second mechanism would be a second answer to one question)
- Encoding a value's TYPE beyond reference-or-not (permanent: fact: `Datom.Value` is declared as untyped bytes and only `IsReference` distinguishes a link; citation: file `internal/core/ports/ports.go:21`)

## Risks

| Risk | Likelihood | Impact | Mitigation |
|------|------------|--------|------------|
| A decoder returns what it managed to read from a truncated run | High — "read what is there" is the natural way to write one | Critical — the last datom decodes as a RETRACTION, so a damaged file silently withdraws facts and the read reports success | Rule 3, and the falsifier asserts the refusal rather than the count |
| A length prefix is trusted before it is checked | High — it is one line shorter | High — a flipped bit becomes a multi-gigabyte allocation from a corrupt block | Rule 6, with a test that hands the decoder an enormous length in a short buffer |
| `Assert` or `IsReference` is inferred rather than read | Med | Critical — a retraction becomes an assertion, or every identifier-shaped string becomes a link | Rule 5, and both bits round-trip in the test matrix |
| An unbounded validity end is written as zero | Med — the zero value is right there | High — the fact reads as having ended at the epoch, and nothing about it looks unusual | Rule 4, and `temporal.Forever` round-trips explicitly |
| A future field is added without bumping the version | Med | High — old and new builds disagree about what the same bytes mean | Rules 1 and 5: the version is checked before anything is read, and a reserved bit that is set is refused |

## Rollback

Nothing is written in this format yet, so reverting means deleting nothing. ⚠ That
freedom ends the moment the first run reaches a disk, which is why the version is
in the header from the first write rather than added when it is first needed —
a format that acquires a version later has no way to describe what came before it.

## Follow-ups

- [ ] When something groups datoms into runs (`BACKLOG.md` §15), confirm a run holds one entity's datoms — the 33 bytes of leaf repeated per datom become obviously removable at that point, and the interning trade should be re-read then rather than rediscovered.
- [ ] Measure the encoded size against a real corpus before taking the interning or varint decisions (`BACKLOG.md` §12); the 74-byte figure here is arithmetic on the struct, not a measurement of anything.
- [ ] When a replication stream exists (`BACKLOG.md` §18/§19), confirm it carries these bytes rather than a second encoding — two wire forms for one fact is how the two drift.
