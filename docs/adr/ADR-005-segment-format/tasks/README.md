# ADR-005 Tasks

Implementation tasks for ADR-005: Store datoms in immutable segments of
independently coded blocks. See the parent ADR for the decision.

**Source of truth:** the task files' headers. This README is a derived index —
when it disagrees with a task file, the task file wins.

## Execution Order

Two tasks, sequential.

| Order | Task | Depends-on |
|-------|------|------------|
| 1 | T1 | none |
| 2 | T2 | T1 |

## Task Index

| ID | Title | Status | Covers | Acceptance |
|----|-------|--------|--------|------------|
| T1 | The block header, its checksum, and the version refusal | done | — | `go test ./internal/core/segment/... -run 'TestBlock\|TestCorrupt\|TestUnknown\|TestHeader\|TestStage'` |
| T2 | The codec registry, and the round trip through the pipeline | done | — | `go test ./internal/core/segment/... -run 'TestEncode\|TestDecode\|TestUnregistered\|TestCodec\|TestBlock\|TestCorrupt\|TestUnknown\|TestHeader\|TestStage'` |

Status: `pending` | `partial` | `blocked` | `done`.

## Contract Coupling

| Producer | Contract | Consumer(s) | Ordering note |
|----------|----------|-------------|---------------|
| T1 | `segment.BlockHeader`, `segment.FormatVersion`, the checksum | T2 | T1 before T2 |

## Notes

- **This package never opens a file, and that is deliberate.** The format is what
  must be right before anything reaches a disk, and keeping it to byte slices is
  what makes every property testable with no storage engine. The segment writer
  and the block index are `BACKLOG.md` §12.
- ⚠ **The pipeline order is fixed and is not a tuning choice:** compress, then
  encrypt, then erasure-code. Encrypting first destroys compressibility, because
  ciphertext does not compress. Coding first means compressing parity. The block
  header records which stages ran so a reader applies their inverses without
  being told.
- ⚠ **The checksum covers the STORED bytes**, after compression and encryption,
  because those are the bytes a disk can rot. It is verified BEFORE decoding —
  handing rotten bytes to a decompressor produces a confusing failure at best and
  plausible garbage at worst. It is also a prerequisite for erasure coding rather
  than a nicety: decoding assumes it knows which fragments are missing, so a
  present-but-rotten fragment yields wrong data with no error unless something
  else caught it.
- ⚠ When adding a test during implementation, check its name is SELECTED by the
  fence's `-run` filter before running any mutant.
