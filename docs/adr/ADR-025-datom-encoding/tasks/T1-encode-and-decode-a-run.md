# Task ADR-025-T1: Encode a run of datoms, and refuse everything that is not one

**Depends-on:** none
**Covers:** none — no spec
**Estimated scope:** M
**Owner:** unassigned
**Produces:** `datom.Encode`, `datom.Decode`, `datom.FormatVersion`, `datom.MaxNameLen`, `datom.MaxValueLen`, `datom.HeaderSize`, `datom.FixedSize`, `datom.ErrShortRun`, `datom.ErrUnknownVersion`, `datom.ErrTooLong`, `datom.ErrReservedFlag`, `datom.ErrTrailingBytes`
**Consumes:** `ports.Datom` from ADR-003, `temporal.Interval` and `temporal.Forever` from ADR-002, `tx.TxID` and `hlc.Timestamp` from ADR-002, `addr.LeafID` from ADR-001
**Data dependency:** hermetic — byte slices only, no filesystem and no clock
**Proof map:** v1
**Rests-on:** `a truncated run being refused rather than partially decoded`, `both validity endpoints being written rather than defaulted`, `Assert and IsReference being read rather than inferred`, `a length being checked before anything is allocated`, `an unknown format version being refused before anything is read`

## Goal

Make a fact into bytes and back, so that everything a caller gets is either
exactly what was written or a named refusal — never a plausible fact that is not
the one recorded.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `internal/core/datom/doc.go` | add | Why the unit is a run, and why a short read is a refusal. |
| `internal/core/datom/datom.go` | add | The layout, the sentinels, `Encode` and `Decode`. |
| `internal/core/datom/datom_test.go` | add | The tests below, over byte slices. |

★ The tests need no filesystem, no clock and no fixture. That is the point of
keeping this package to byte slices — every property here is decidable with
nothing running.

## Ordered Steps

1. [S1] Write the failing tests first (TDD red): `TestATruncatedRunIsRefusedRatherThanZeroFilled`, `TestEveryFieldRoundTrips`, `TestAnUnboundedEndIsForeverNotZero`, `TestALengthIsCheckedBeforeItIsAllocated`, `TestAnUnknownVersionIsRefused`, `TestAReservedFlagBitIsRefused`, `TestANameTooLongIsRefusedAtEncode`, `TestAnEmptyRunIsNotATruncatedOne`, `TestOrderIsPreserved`, `TestTrailingBytesAreRefused`. Run the Acceptance fence and confirm it is red. ⚠Check each name is SELECTED by the fence's `-run` filter before running any mutant. [proof: acceptance]
2. [S2] Define the run header — format version and datom count — and the fixed part of a datom: flags, three length prefixes, both validity endpoints, and the whole transaction identifier. Big-endian throughout. [proof: mutation]
3. [S3] Implement `Encode`, writing EVERY field unconditionally. ⚠No field is omitted for being zero, empty or false. [proof: mutation]
4. [S4] Refuse an over-long entity name, attribute name or value at ENCODE time with `ErrTooLong`, rather than truncating a length prefix into a different number. [proof: mutation]
5. [S5] Implement `Decode` to check the version FIRST and refuse an unknown one before reading anything that follows. [proof: mutation]
6. [S6] Check every length against the bytes that remain BEFORE allocating anything. ⚠A length prefix is a number a corrupt block chooses. [proof: mutation]
7. [S7] Refuse a short buffer with `ErrShortRun` at every point it can be short — the header, the fixed part, and each of the three variable parts. ⚠**A partially filled `ports.Datom` has `Assert` false, which is a RETRACTION.** Returning one withdraws a fact and reports success. [proof: mutation]
8. [S8] Refuse a set reserved bit in the flags byte with `ErrReservedFlag`, and refuse trailing bytes after the last datom with `ErrTrailingBytes`. ★Within a known version neither can be anything but corruption or a spliced buffer.
9. [S9] Preserve the order given, and normalise a zero-length value to an empty non-nil slice — the one stated normalisation.

## Acceptance

```bash
set -o pipefail
go test ./internal/core/datom/... -race -run 'TestATruncatedRunIsRefusedRatherThanZeroFilled|TestEveryFieldRoundTrips|TestAnUnboundedEndIsForeverNotZero|TestALengthIsCheckedBeforeItIsAllocated|TestAnUnknownVersionIsRefused|TestAReservedFlagBitIsRefused|TestANameTooLongIsRefusedAtEncode|TestAnEmptyRunIsNotATruncatedOne|TestOrderIsPreserved|TestTrailingBytesAreRefused' -count=1 2>&1 | tee /tmp/adr025-t1a.out \
  && ! grep -qE "no tests to run|matched no packages|^FAIL|^--- FAIL|DATA RACE" /tmp/adr025-t1a.out \
  && go test ./internal/core/datom/... ./internal/core/ports/... ./internal/core/temporal/... ./internal/core/tx/... -race -count=1 2>&1 | tee /tmp/adr025-t1b.out \
  && ! grep -qE "no tests to run|matched no packages|^FAIL|^--- FAIL|DATA RACE" /tmp/adr025-t1b.out
```

The second command re-runs the suites this encoding is built on, because it
encodes their types and must not land by changing what one of them means.

## Tests

| Test name | File | Verifies | Covers | Steps |
|-----------|------|----------|--------|-------|
| `TestATruncatedRunIsRefusedRatherThanZeroFilled` | `internal/core/datom/datom_test.go` | A run of ASSERTED datoms truncated at **every** length from 1 to one byte short is refused at every one — **the falsifier ADR-025 names in `Enforced-by:`**. Also asserts no datoms come back at all, so a decoder that returns a short slice alongside an error still fails | — | S7 |
| `TestEveryFieldRoundTrips` | `internal/core/datom/datom_test.go` | A matrix over both `Assert` values, both `IsReference` values, bounded and `Forever` ends, an empty value, a large value, and multi-byte names, all byte-identical after a round trip | — | S3, S9 |
| `TestAnUnboundedEndIsForeverNotZero` | `internal/core/datom/datom_test.go` | An interval ending at `temporal.Forever` decodes to `Forever` and specifically not to zero, and the encoded bytes are not all-zero at that offset | — | S2, S3 |
| `TestALengthIsCheckedBeforeItIsAllocated` | `internal/core/datom/datom_test.go` | A run whose value length is a gigabyte, in an 80-byte buffer, is refused **and** grows the heap by under 1 MiB — measured, because the error alone would be returned just as happily after the allocation. ⚠ A gigabyte rather than the `MaxUint32` a corrupt block could really carry: a mutant that removes the check then allocates that much, and four times this could destabilise the machine running the fence | — | S6 |
| `TestAnUnknownVersionIsRefused` | `internal/core/datom/datom_test.go` | A run whose version is not `FormatVersion` is `ErrUnknownVersion`, and is refused even when everything after the version is well-formed | — | S5 |
| `TestAReservedFlagBitIsRefused` | `internal/core/datom/datom_test.go` | A flags byte with a reserved bit set is `ErrReservedFlag` rather than masked off | — | S8 |
| `TestANameTooLongIsRefusedAtEncode` | `internal/core/datom/datom_test.go` | An entity or attribute longer than `MaxNameLen` is `ErrTooLong` at encode time, not a length prefix wrapped into a smaller number | — | S4 |
| `TestAnEmptyRunIsNotATruncatedOne` | `internal/core/datom/datom_test.go` | An empty run encodes to a header, decodes to zero datoms with no error, and is **not** confused with a truncated run — with the header's last byte removed it is `ErrShortRun` | — | S3, S7 |
| `TestOrderIsPreserved` | `internal/core/datom/datom_test.go` | Datoms given in a deliberately non-EAVT order come back in that order, so nothing sorted them on the way past | — | S9 |
| `TestTrailingBytesAreRefused` | `internal/core/datom/datom_test.go` | Bytes after the last datom are `ErrTrailingBytes`, so a spliced or over-long buffer does not decode as if it were exactly right | — | S8 |

## Reachability

| Rung | How this task shows it |
|------|------------------------|
| 1 — exists | The ten tests above, over byte slices. |
| 2 — something selects it | `Encode` is the only way a datom becomes bytes and `Decode` the only way bytes become datoms; there is no second path. |
| 3 — the caller can discover it | Every refusal is a named sentinel, so truncation, corruption, an unknown version and an over-long name are told apart from the API rather than from a message. |
| 4 — it is used | Nothing writes datoms to a disk yet — when a leaf store is wired onto ADR-024 (`BACKLOG.md` §28), this is what it will put in a block. |

## Mutation Log

- 2026-09-04 · 5488e8a* · mutant killed · exit 1 · `internal/core/datom/datom.go` · tolerates a truncated run and returns the datoms that decoded, which is the rejected alternative: the caller then holds a partially filled Datom whose Assert is false, a retraction of a fact nobody retracted · acceptance-sha256:414748ef43b9acf719cacc5ab7ecf6386160f9958cb945aae0bb9bb32d788cd6 · covers:a truncated run being refused rather than partially decoded
- 2026-09-04 · 5488e8a* · mutant killed · exit 1 · `internal/core/datom/datom.go` · writes zero for the validity end instead of the value held, so an unbounded fact is stored as one that ended at the epoch with nothing about it looking unusual · acceptance-sha256:414748ef43b9acf719cacc5ab7ecf6386160f9958cb945aae0bb9bb32d788cd6 · covers:both validity endpoints being written rather than defaulted
- 2026-09-04 · 5488e8a* · mutant killed · exit 1 · `internal/core/datom/datom.go` · stops reading the Assert bit and assumes every datom is an assertion, so every retraction on disk decodes as the fact it was withdrawing · acceptance-sha256:414748ef43b9acf719cacc5ab7ecf6386160f9958cb945aae0bb9bb32d788cd6 · covers:Assert and IsReference being read rather than inferred
- 2026-09-04 · 5488e8a* · mutant killed · exit 1 · `internal/core/datom/datom.go` · disables the size check that runs before the value is allocated, so a length a corrupt block chose sizes the allocation directly; the test measures heap growth because the error alone would be returned just as truthfully after the allocation · acceptance-sha256:414748ef43b9acf719cacc5ab7ecf6386160f9958cb945aae0bb9bb32d788cd6 · covers:a length being checked before anything is allocated
- 2026-09-04 · 5488e8a* · mutant killed · exit 1 · `internal/core/datom/datom.go` · accepts any version below an arbitrary ceiling, so a run written by a build this one does not understand is reinterpreted field by field instead of refused · acceptance-sha256:414748ef43b9acf719cacc5ab7ecf6386160f9958cb945aae0bb9bb32d788cd6 · covers:an unknown format version being refused before anything is read

## Invariants

- Every field is written on every datom, whatever its value.
- A short buffer never produces a datom.
- Both validity endpoints are always present.
- `Assert` and `IsReference` come from the bytes, never from anything else.
- No length is used to size an allocation before it is checked.

## Risks

- ⚠ **A round-trip test alone proves almost nothing here.** Every field round-trips under a decoder that also happily decodes garbage; the failure this record is about is only visible on input the encoder never produced. Six of the ten tests hand `Decode` bytes that `Encode` did not write.
- ⚠ **The truncation test must assert NO DATOMS, not just an error.** A decoder that returns `(partial, err)` is the realistic mistake, and a caller checking the slice before the error then acts on a retraction. The test checks the returned length as well as the error.
- ⚠ **`TestALengthIsCheckedBeforeItIsAllocated` cannot be written as an assertion on the error.** The error would be returned just as truthfully after the allocation happened. It measures heap growth, which is the only observable difference between checking first and checking second.
- ⚠ **A test for the value-length ceiling would have to allocate four gigabytes**, so `MaxValueLen` is enforced by the same code path as `MaxNameLen` and proven only at the name. Recorded rather than papered over: the ceiling is arithmetic on a `uint32`, and the check is one branch away from the tested one.
- The 74-byte fixed cost per datom is arithmetic on the struct, not a measurement. Recorded as a follow-up on the parent record.

## Stop Condition

Stop and ask before making `Decode` return the datoms it managed to read
alongside an error, however useful that looks for recovering a damaged file. The
last datom of a truncated run is not missing — its `Assert` is false, which is a
retraction, and a caller that reads the slice before the error withdraws a fact
that was never withdrawn.

## Out of Scope

- Interning names or transaction identifiers (deferred: `docs/adr/BACKLOG.md` §12)
- A variable-length integer encoding (deferred: `docs/adr/BACKLOG.md` §12)
- Which datoms belong in one run (deferred: `docs/adr/BACKLOG.md` §15)
- Writing a run into a segment (deferred: `docs/adr/BACKLOG.md` §28)
- Compression, encryption or a checksum over these bytes (permanent: boundary: ADR-005 owns the pipeline over a block and ADR-024 verifies it on read; doing any of them here would do them twice)

## Verification Log
- 2026-09-04 · 5488e8a* · exit 0 · `set -o pipefail …` · acceptance-sha256:414748ef43b9acf719cacc5ab7ecf6386160f9958cb945aae0bb9bb32d788cd6 · ms:4197
- 2026-09-04 · 5488e8a* · exit 0 · `set -o pipefail …` · acceptance-sha256:414748ef43b9acf719cacc5ab7ecf6386160f9958cb945aae0bb9bb32d788cd6 · ms:3802
- 2026-09-04 · 5488e8a* · exit 0 · `set -o pipefail …` · acceptance-sha256:414748ef43b9acf719cacc5ab7ecf6386160f9958cb945aae0bb9bb32d788cd6 · ms:4105
- 2026-09-04 · 5488e8a* · exit 0 · `set -o pipefail …` · acceptance-sha256:414748ef43b9acf719cacc5ab7ecf6386160f9958cb945aae0bb9bb32d788cd6 · ms:3871
- 2026-09-04 · 5488e8a* · exit 0 · `set -o pipefail …` · acceptance-sha256:414748ef43b9acf719cacc5ab7ecf6386160f9958cb945aae0bb9bb32d788cd6 · ms:3964
- 2026-09-04 · 5488e8a* · exit 0 · `set -o pipefail …` · acceptance-sha256:414748ef43b9acf719cacc5ab7ecf6386160f9958cb945aae0bb9bb32d788cd6 · ms:4003
