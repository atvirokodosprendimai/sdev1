# Task ADR-005-T1: The block header, its checksum, and the version refusal

**Depends-on:** none
**Covers:** none — no spec
**Estimated scope:** M (multi-file)
**Owner:** unassigned
**Produces:** `segment.FormatVersion`, `segment.Header`, `segment.BlockHeader`, `segment.BlockHeader.Encode`, `segment.DecodeBlockHeader`, `segment.ErrUnknownVersion`, `segment.ErrCorruptBlock`
**Consumes:** `addr.LeafID` from ADR-001
**Data dependency:** hermetic
**Proof map:** v1
**Rests-on:** `the checksum being verified on read`, `the refusal of an unknown version`, `the header being fixed-width`

## Goal

Make a block self-describing and self-checking: readable from its own bytes, and
refused rather than returned when those bytes are wrong.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `internal/core/segment/segment.go` | add | `FormatVersion`, `Header`, `BlockHeader`, and the stage flags. |
| `internal/core/segment/header.go` | add | Fixed-width encoding and decoding of a block header, plus the checksum. |
| `internal/core/segment/doc.go` | add | Package comment: what a block carries, why it carries it rather than reading configuration, and the fixed pipeline order. |
| `internal/core/segment/segment_test.go` | add | The tests below, including the falsifier named in ADR-005's `Enforced-by:`. |

★ This package works on byte slices and never opens a file. The format is the
part that must be right before anything reaches a disk, and keeping it free of
I/O is what makes every property below testable with no storage engine.

## Ordered Steps

1. [S1] Write the failing tests first (TDD red): `TestBlockCarriesItsOwnCodec`, `TestCorruptBlockIsRefused`, `TestUnknownVersionIsRefused`, `TestHeaderIsFixedWidth`, `TestStageFlagsRecordThePipeline`, `TestHeaderRoundTrips`. Run the Acceptance fence and confirm it is red. ⚠Check each name is SELECTED by the fence's `-run` filter before running any mutant. [proof: acceptance]
2. [S2] Define `FormatVersion` and `Header`, carrying the version, the leaf the segment belongs to, and the block count.
3. [S3] Define `BlockHeader` carrying the codec identifier, the cipher identifier, the stage flags, the uncompressed and stored lengths, and the checksum of the stored bytes.
4. [S4] Implement a fixed-width encoding for the block header, big-endian, so a reader can stride over headers without decoding bodies.
5. [S5] Implement `Verify(stored []byte)`: recompute the checksum and return `ErrCorruptBlock` on mismatch, naming both values. ★This is what makes silent corruption a detected fault; without it an erasure decoder fed a rotten fragment returns wrong data with no error anywhere.
6. [S6] Refuse an unknown `FormatVersion` with `ErrUnknownVersion` rather than reading what follows.
7. [S7] Write the package comment stating that a block is interpretable from its own header, and stating the fixed compress-then-encrypt-then-code order with the reason for each half. [proof: human: a reader confirms the comment gives the ORDER and why reversing either step is wrong, not merely that stages exist]

## Acceptance

```bash
set -o pipefail
go test ./internal/core/segment/... -run 'TestBlock|TestCorrupt|TestUnknown|TestHeader|TestStage' -count=1 2>&1 | tee /tmp/adr005-t1.out \
  && ! grep -qE "no tests to run|matched no packages|^FAIL|^--- FAIL" /tmp/adr005-t1.out
```

## Tests

| Test name | File | Verifies | Covers | Steps |
|-----------|------|----------|--------|-------|
| `TestBlockCarriesItsOwnCodec` | `internal/core/segment/segment_test.go` | A block header round-trips its codec and cipher identifiers, so a block is interpretable from its own bytes and a configuration change cannot reinterpret it. **The falsifier ADR-005 names in `Enforced-by:`** | — | S3, S4 |
| `TestCorruptBlockIsRefused` | `internal/core/segment/segment_test.go` | A single flipped bit in the stored bytes yields `ErrCorruptBlock` rather than the bytes — the check that makes silent corruption detectable at all | — | S5 |
| `TestUnknownVersionIsRefused` | `internal/core/segment/segment_test.go` | A segment written by a future release is refused explicitly, so an incompatible change becomes a migration rather than a misread; and the segment header carries the version and the leaf it belongs to | — | S2, S6 |
| `TestHeaderIsFixedWidth` | `internal/core/segment/segment_test.go` | Every encoded block header is the same length whatever its field values, so a reader can stride | — | S4 |
| `TestStageFlagsRecordThePipeline` | `internal/core/segment/segment_test.go` | The flags say which stages ran, so a reader applies their inverses without being told and cannot guess the order | — | S3 |
| `TestHeaderRoundTrips` | `internal/core/segment/segment_test.go` | Property test over generated headers: encode then decode is the identity, so no field is silently dropped | — | S4 |

## Reachability

| Rung | How this task shows it |
|------|------------------------|
| 1 — exists | The six tests above. |
| 2 — something selects it | T2's codec registry writes and reads these headers; its fence builds against this package. |
| 3 — the caller can discover it | Exported doc comments and two named sentinels; `go doc ./internal/core/segment` is the check, and for a format package the doc IS the specification. |
| 4 — it is used | Nothing measures this yet; no storage engine exists. |

## Mutation Log

- 2026-09-04 · 17256f5* · mutant killed · exit 1 · `internal/core/segment/header.go` · makes the checksum comparison always false, so a rotten block passes Verify and the bytes are returned as though they were sound · acceptance-sha256:a4c83bf669b9cf0a46adf29c36c4b1fcf6c8682cbc8c0b9987cb2d5d63148428 · covers:the checksum being verified on read
- 2026-09-04 · 17256f5* · mutant killed · exit 1 · `internal/core/segment/segment.go` · removes the version gate, so a segment written by a future release is read with this build assumptions instead of being refused · acceptance-sha256:a4c83bf669b9cf0a46adf29c36c4b1fcf6c8682cbc8c0b9987cb2d5d63148428 · covers:the refusal of an unknown version
- 2026-09-04 · 17256f5* · mutant killed · exit 1 · `internal/core/segment/header.go` · changes the on-disk header width while leaving every field offset valid, so encode-decode round trips and striding both still succeed and only an assertion that pins the actual layout can see it · acceptance-sha256:a4c83bf669b9cf0a46adf29c36c4b1fcf6c8682cbc8c0b9987cb2d5d63148428 · covers:the header being fixed-width

## Invariants

- A block is interpretable from its own header. No field needed to read it lives in configuration.
- The checksum covers the STORED bytes — after compression and encryption — because those are the bytes a disk can rot.
- An unknown format version is refused, never partially read.
- The block header is fixed-width, so headers can be strided over.
- This package performs no file I/O.

## Risks

- A checksum over the wrong bytes is worse than none, because it reports health it did not check. The tests state which bytes are covered rather than leaving it to be inferred from the implementation.
- A fixed-width header wastes a few bytes per block on fields that are usually small. That is paid deliberately for striding, and it bounds how small a block can usefully be — which is recorded rather than discovered.

## Stop Condition

Stop and ask if the checksum needs to be cryptographic rather than a detection
code. It is sized here for accidental corruption; defending against a
deliberately altered block is a different threat model and needs the key
material ADR-007 owns.

## Out of Scope

- The codecs themselves — that is T2.
- Anything that opens a file (deferred: `docs/adr/BACKLOG.md` §12)

## Verification Log
- 2026-09-04 · 17256f5* · exit 0 · `set -o pipefail …` · acceptance-sha256:a4c83bf669b9cf0a46adf29c36c4b1fcf6c8682cbc8c0b9987cb2d5d63148428 · ms:579
- 2026-09-04 · 17256f5* · exit 0 · `set -o pipefail …` · acceptance-sha256:a4c83bf669b9cf0a46adf29c36c4b1fcf6c8682cbc8c0b9987cb2d5d63148428 · ms:709
- 2026-09-04 · 17256f5* · exit 0 · `set -o pipefail …` · acceptance-sha256:a4c83bf669b9cf0a46adf29c36c4b1fcf6c8682cbc8c0b9987cb2d5d63148428 · ms:621
- 2026-09-04 · 17256f5* · exit 0 · `set -o pipefail …` · acceptance-sha256:a4c83bf669b9cf0a46adf29c36c4b1fcf6c8682cbc8c0b9987cb2d5d63148428 · ms:590
- 2026-09-04 · 17256f5* · exit 0 · `set -o pipefail …` · acceptance-sha256:a4c83bf669b9cf0a46adf29c36c4b1fcf6c8682cbc8c0b9987cb2d5d63148428 · ms:647
- 2026-09-04 · 09ec963* · exit 0 · `set -o pipefail …` · acceptance-sha256:a4c83bf669b9cf0a46adf29c36c4b1fcf6c8682cbc8c0b9987cb2d5d63148428 · ms:651
