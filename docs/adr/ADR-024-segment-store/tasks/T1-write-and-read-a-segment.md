# Task ADR-024-T1: Write a segment to a disk, and find a block in one

**Depends-on:** none
**Covers:** none — no spec
**Estimated scope:** L (cross-boundary)
**Owner:** unassigned
**Produces:** `segstore.Writer`, `segstore.Create`, `segstore.Writer.Append`, `segstore.Writer.Seal`, `segstore.Writer.Abort`, `segstore.Reader`, `segstore.Open`, `segstore.Reader.Get`, `segstore.Reader.Keys`, `segstore.Reader.Leaf`, `segstore.Reader.Close`, `segstore.TrailerSize`, `segstore.TrailerMagic`, `segstore.FormatVersion`, `segstore.ErrNoSuchBlock`, `segstore.ErrNotASegment`, `segstore.ErrIndexCorrupt`, `segstore.ErrClosed`, `segstore.ErrSealed`, `segstore.ErrDuplicateKey`
**Consumes:** `segment.Header`, `segment.BlockHeader`, `segment.EncodeBlock`, `segment.DecodeBlock`, `segment.Checksum` from ADR-005
**Data dependency:** hermetic — writes to a temporary directory the test owns
**Proof map:** v1
**Rests-on:** `a segment existing at its path only once sealed`, `the index checksum refusing a corrupted index`, `a truncated file being refused rather than parsed`, `a missing key being a named refusal rather than an empty block`, `a block being verified on read rather than trusted from the index`, `a returned block being owned by the caller rather than a view into the mapping`

## Goal

Make data outlive a process, without ever letting a reader see a segment that is
not finished.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `internal/core/segstore/doc.go` | add | Why the file exists only when complete, and what the layout follows from. |
| `internal/core/segstore/format.go` | add | The trailer, the index encoding, and the sentinels. |
| `internal/core/segstore/writer.go` | add | `Writer`, `Create`, `Append`, `Seal`, `Abort`. |
| `internal/core/segstore/reader.go` | add | `Reader`, `Open`, `Get`, `Keys`, `Close`. |
| `internal/core/segstore/mmap_unix.go` | add | The mapping and its build constraint — macOS and Linux, nothing else. |
| `internal/core/segstore/segstore_test.go` | add | The tests below, against a real filesystem. |

★ The tests use a REAL temporary directory rather than an in-memory filesystem.
The central claim is about when a directory entry appears, and an abstraction over
the filesystem would be asserting the abstraction rather than the behaviour.

## Ordered Steps

1. [S1] Write the failing tests first (TDD red): `TestAnUnsealedSegmentDoesNotExistAtItsPath`, `TestRoundTripsEveryBlock`, `TestACorruptIndexIsRefused`, `TestATruncatedFileIsNotASegment`, `TestAMissingKeyIsNamed`, `TestACorruptBlockIsRefusedOnRead`, `TestAbortLeavesNothingBehind`, `TestADuplicateKeyIsRefused`, `TestAppendAfterSealIsRefused`, `TestABlockOutlivesTheReaderThatReturnedIt`, `TestGetAfterCloseIsRefused`, `TestAReadDoesNotRaceAClose`. Run the Acceptance fence and confirm it is red. ⚠Check each name is SELECTED by the fence's `-run` filter before running any mutant. [proof: acceptance]
2. [S2] Define the trailer: magic, format version, index offset, index length, index checksum — FIXED WIDTH, read from the end of the file. ★One seek finds it whatever the segment's size. [proof: mutation]
3. [S3] Implement `Create` to open a TEMPORARY name beside the destination, never the destination itself. [proof: mutation]
4. [S4] Implement `Append` to encode a block through ADR-005 and record its key, offset and length in an in-memory index, refusing a key already in this segment and a write after `Seal` by name. ★No second block format is introduced here; the container stays ignorant of what it holds. ⚠A duplicate key is not a last-write-wins: it is a block on disk that a binary search can never reach. [proof: mutation]
5. [S5] Implement `Seal`: sort the index by key, write it, write the trailer, fsync, then RENAME into place. ⚠The rename is last and it is the publication — everything before it is invisible to a reader. [proof: mutation]
6. [S6] Implement `Open` to map the file, read the trailer, check the magic and version, verify the index checksum, and refuse by name otherwise. ⚠A wrong index yields arbitrary byte offsets, and arbitrary bytes read as a block are indistinguishable from a real one until the block's own checksum says otherwise. [proof: mutation]
7. [S7] Implement `Get` by binary search over the sorted index, refusing a missing key with `ErrNoSuchBlock` rather than returning an empty block. [proof: mutation]
8. [S8] Verify each block on read through ADR-005's own check rather than adding a second one. [proof: mutation]
9. [S9] Implement `Abort` to close and remove the temporary file, so a discarded segment leaves nothing.
10. [S10] Put the mapping behind `//go:build darwin || linux`, with no fallback for other platforms. ⚠Check the file is at least a trailer long BEFORE mapping it — a zero-length mapping is refused by the kernel, and that refusal would surface instead of `ErrNotASegment`. [proof: mutation]
11. [S11] Copy every block OUT of the mapping before returning it, and refuse `Get` after `Close` by name. ⚠A sub-slice of the mapping is a dangling pointer the instant `Close` unmaps, and it behaves perfectly until then. ★The copy is free: ADR-005's `DecodeBlock` already allocates its result. [proof: mutation]

## Acceptance

```bash
set -o pipefail
go test ./internal/core/segstore/... -race -run 'TestAnUnsealedSegmentDoesNotExistAtItsPath|TestRoundTripsEveryBlock|TestACorruptIndexIsRefused|TestATruncatedFileIsNotASegment|TestAMissingKeyIsNamed|TestACorruptBlockIsRefusedOnRead|TestAbortLeavesNothingBehind|TestADuplicateKeyIsRefused|TestAppendAfterSealIsRefused|TestABlockOutlivesTheReaderThatReturnedIt|TestGetAfterCloseIsRefused|TestAReadDoesNotRaceAClose' -count=1 2>&1 | tee /tmp/adr024-t1a.out \
  && ! grep -qE "no tests to run|matched no packages|^FAIL|^--- FAIL|DATA RACE" /tmp/adr024-t1a.out \
  && go test ./internal/core/segstore/... ./internal/core/segment/... -race -count=1 2>&1 | tee /tmp/adr024-t1b.out \
  && ! grep -qE "no tests to run|matched no packages|^FAIL|^--- FAIL|DATA RACE" /tmp/adr024-t1b.out
```

The second command re-runs ADR-005's suite, because this container is built on
that block format and must not land by breaking it.

## Tests

| Test name | File | Verifies | Covers | Steps |
|-----------|------|----------|--------|-------|
| `TestAnUnsealedSegmentDoesNotExistAtItsPath` | `internal/core/segstore/segstore_test.go` | After `Create` and several `Append`s the destination path does NOT exist, and it appears only after `Seal` — **the falsifier ADR-024 names in `Enforced-by:`**. Also asserts the temporary file does exist meanwhile, so the test cannot pass against a writer that writes nothing at all | — | S3, S5 |
| `TestRoundTripsEveryBlock` | `internal/core/segstore/segstore_test.go` | Every block written comes back byte-identical, including an empty one and a large one, and `Keys` returns them sorted | — | S4, S7 |
| `TestACorruptIndexIsRefused` | `internal/core/segstore/segstore_test.go` | Flipping a byte inside the index region makes `Open` fail with `ErrIndexCorrupt` rather than yielding plausible offsets | — | S2, S6 |
| `TestATruncatedFileIsNotASegment` | `internal/core/segstore/segstore_test.go` | A file shorter than the trailer, and one with the trailer removed, are `ErrNotASegment` — the shape a crash mid-write leaves | — | S2, S6 |
| `TestAMissingKeyIsNamed` | `internal/core/segstore/segstore_test.go` | A key that was never written is `ErrNoSuchBlock`, and specifically not an empty block — with an EMPTY block also written, so the two cases are distinguished rather than merely one being absent | — | S7 |
| `TestACorruptBlockIsRefusedOnRead` | `internal/core/segstore/segstore_test.go` | Corrupting a block's bytes while leaving the index valid is caught on read, so the index being right does not make the data trusted | — | S8 |
| `TestAbortLeavesNothingBehind` | `internal/core/segstore/segstore_test.go` | `Abort` removes the temporary file and never creates the destination | — | S9 |
| `TestADuplicateKeyIsRefused` | `internal/core/segstore/segstore_test.go` | A key appended twice is `ErrDuplicateKey`, so a block that no binary search could reach is never written at all | — | S4 |
| `TestAppendAfterSealIsRefused` | `internal/core/segstore/segstore_test.go` | `Append` and `Seal` after a successful `Seal` are `ErrSealed`, and `Abort` after one is a no-op — so `defer Abort()` beside `Seal()` is the safe shape rather than a guaranteed second error | — | S4, S9 |
| `TestABlockOutlivesTheReaderThatReturnedIt` | `internal/core/segstore/segstore_test.go` | A block read from a segment is still byte-identical AFTER the `Reader` that returned it is closed — the falsifier for rule 10, and it fails as a signal rather than an assertion if the bytes are a view into the mapping | — | S11 |
| `TestGetAfterCloseIsRefused` | `internal/core/segstore/segstore_test.go` | `Get` and `Keys` on a closed `Reader` return `ErrClosed` rather than reading unmapped memory | — | S11 |
| `TestAReadDoesNotRaceAClose` | `internal/core/segstore/segstore_test.go` | Concurrent `Get`s alongside a `Close`, under `-race`, either return the block or `ErrClosed` and never garbage — the mapping's lifetime is what is guarded, not its data | — | S10, S11 |

## Reachability

| Rung | How this task shows it |
|------|------------------------|
| 1 — exists | The seven tests above, against a real filesystem. |
| 2 — something selects it | `Create` is the only way a segment file begins and `Seal` the only way one is published; `Open` is the only way one is read. |
| 3 — the caller can discover it | Every refusal is a named sentinel, so the completeness rule and the missing-key rule are learnable from the API. |
| 4 — it is used | Nothing writes segments in anger yet — when the tail is sealed (`BACKLOG.md` §15) and the session is wired to storage (§28), this is what they will use. |

## Mutation Log

- 2026-09-04 · 96fd506* · mutant killed · exit 1 · `internal/core/segstore/writer.go` · writes straight to the final path, which is the simpler implementation ADR-024 names as its falsifier: the destination then exists from the first byte and anyone listing the directory sees a half-written segment · acceptance-sha256:d645543b9b89158b9a7d5e2514fe9e1e65c46c708f89a93994aac352281c12b6 · covers:a segment existing at its path only once sealed
- 2026-09-04 · 96fd506* · mutant killed · exit 1 · `internal/core/segstore/reader.go` · compares the trailer value with itself instead of computing the checksum over the index, so a corrupted index that still decodes, sorts and fits is accepted · acceptance-sha256:d645543b9b89158b9a7d5e2514fe9e1e65c46c708f89a93994aac352281c12b6 · covers:the index checksum refusing a corrupted index
- 2026-09-04 · 96fd506* · mutant killed · exit 1 · `internal/core/segstore/format.go` · drops the magic check, so the last thirty bytes of a file truncated mid-write are PARSED as a trailer rather than refused, which is the shape a crash leaves · acceptance-sha256:d645543b9b89158b9a7d5e2514fe9e1e65c46c708f89a93994aac352281c12b6 · covers:a truncated file being refused rather than parsed
- 2026-09-04 · 96fd506* · mutant killed · exit 1 · `internal/core/segstore/reader.go` · returns an empty block for a key that was never written, which is the alternative ADR-024 rejects: a caller cannot then tell absence from emptiness, and the segment holds an empty block for real · acceptance-sha256:d645543b9b89158b9a7d5e2514fe9e1e65c46c708f89a93994aac352281c12b6 · covers:a missing key being a named refusal rather than an empty block
- 2026-09-04 · 96fd506* · mutant killed · exit 1 · `internal/core/segstore/reader.go` · hands back the stored bytes without ADR-005 checking them, so an index that located the right offset makes the data trusted; the returned bytes are still OWNED, which keeps this mutant separate from the ownership claim · acceptance-sha256:d645543b9b89158b9a7d5e2514fe9e1e65c46c708f89a93994aac352281c12b6 · covers:a block being verified on read rather than trusted from the index
- 2026-09-04 · 96fd506* · mutant killed · exit 1 · `internal/core/segstore/reader.go` · keeps the checksum but returns a VIEW into the mapping; run alone, TestABlockOutlivesTheReaderThatReturnedIt fails with SIGSEGV, which is the failure mode rule 10 predicts and the reason no test that reads before closing can see this · acceptance-sha256:d645543b9b89158b9a7d5e2514fe9e1e65c46c708f89a93994aac352281c12b6 · covers:a returned block being owned by the caller rather than a view into the mapping
- 2026-09-04 · 96fd506* · mutant killed · exit 1 · `internal/core/segstore/reader.go` · removes the length check that runs BEFORE the mapping, so an empty file reaches mmap and comes back as the kernel EINVAL instead of ErrNotASegment · acceptance-sha256:d645543b9b89158b9a7d5e2514fe9e1e65c46c708f89a93994aac352281c12b6 · covers:a truncated file being refused rather than parsed

## Invariants

- The destination path exists only after a successful `Seal`.
- An index is verified before any offset from it is used.
- A missing key is a named refusal.
- A block is verified on read, by the format that owns it.

## Risks

- ⚠ **A completeness test that only checks the file exists after sealing proves half of it.** The test asserts the destination does NOT exist while writing, and that the temporary file DOES — otherwise a writer that buffered everything in memory and wrote nothing would pass.
- ⚠ **A missing-key test with no empty block written proves nothing about the distinction.** The fixture writes an empty block AND queries an absent key, so "absent" and "present but empty" are actually told apart.
- ⚠ **A corrupt-index test that corrupts a block instead would pass for the wrong reason** — the block checksum would catch it and the index check would remain unproven. The corruption is applied inside the index region specifically.
- ⚠ **Verifying the block on read looks redundant while the index is correct**, which is exactly why it is easy to delete. An index is a list of offsets; a wrong one produces bytes that look like a block, and the checksum is the only thing standing behind it.
- `fsync` is called before the rename, and nothing here proves the ordering survives a real power loss — that needs hardware this test cannot have. The call is present and its absence would be a silent durability defect rather than a visible one.
- The index is held in memory while writing, so a segment's block count is bounded by the writer's memory. Recorded in the parent record as a stated consequence.
- ⚠ **The obvious mmap implementation returns a sub-slice, and it passes every round-trip test.** Nothing about a view into the mapping is observable while the `Reader` is open; the defect appears only after `Close`, in the caller's code, as a signal. `TestABlockOutlivesTheReaderThatReturnedIt` closes first and reads second, which is the only order that can see it.
- ⚠ **A `Close` that unmaps while a `Get` is mid-flight is the same defect with a schedule instead of an ordering.** The lock taken here guards the mapping's LIFETIME and never its contents — readers do not block each other, and the only writer is `Close`, so ADR-017's lock-free read path is untouched.
- An I/O error on a mapped page arrives as SIGBUS and no test here can provoke one without failing hardware. Recorded in the parent record as an accepted consequence rather than proven absent.

## Stop Condition

Stop and ask before writing a segment directly to its final path, however much
simpler it looks when nothing crashes. A reader that can observe a half-written
segment makes ADR-017's lock-free read path unsound, and the failure appears only
under a race nobody reproduces on purpose.

## Out of Scope

- When to seal a segment (deferred: `docs/adr/BACKLOG.md` §15)
- A manifest naming which segments exist (deferred: `docs/adr/BACKLOG.md` §15)
- Erasure-coding a sealed segment (deferred: `docs/adr/BACKLOG.md` §12)
- Wiring the session onto real storage (deferred: `docs/adr/BACKLOG.md` §28)
- Platforms other than macOS and Linux (permanent: boundary: the mapping call is per-platform, and an unsupported target fails to compile rather than taking a different read path)

## Verification Log
- 2026-09-04 · 96fd506* · exit 0 · `set -o pipefail …` · acceptance-sha256:d645543b9b89158b9a7d5e2514fe9e1e65c46c708f89a93994aac352281c12b6 · ms:4132
- 2026-09-04 · 96fd506* · exit 0 · `set -o pipefail …` · acceptance-sha256:d645543b9b89158b9a7d5e2514fe9e1e65c46c708f89a93994aac352281c12b6 · ms:4243
- 2026-09-04 · 96fd506* · exit 0 · `set -o pipefail …` · acceptance-sha256:d645543b9b89158b9a7d5e2514fe9e1e65c46c708f89a93994aac352281c12b6 · ms:4270
- 2026-09-04 · 96fd506* · exit 0 · `set -o pipefail …` · acceptance-sha256:d645543b9b89158b9a7d5e2514fe9e1e65c46c708f89a93994aac352281c12b6 · ms:4030
- 2026-09-04 · 96fd506* · exit 0 · `set -o pipefail …` · acceptance-sha256:d645543b9b89158b9a7d5e2514fe9e1e65c46c708f89a93994aac352281c12b6 · ms:4223
- 2026-09-04 · 96fd506* · exit 0 · `set -o pipefail …` · acceptance-sha256:d645543b9b89158b9a7d5e2514fe9e1e65c46c708f89a93994aac352281c12b6 · ms:4201
- 2026-09-04 · 96fd506* · exit 0 · `set -o pipefail …` · acceptance-sha256:d645543b9b89158b9a7d5e2514fe9e1e65c46c708f89a93994aac352281c12b6 · ms:4214
- 2026-09-04 · 96fd506* · exit 0 · `set -o pipefail …` · acceptance-sha256:d645543b9b89158b9a7d5e2514fe9e1e65c46c708f89a93994aac352281c12b6 · ms:4235
