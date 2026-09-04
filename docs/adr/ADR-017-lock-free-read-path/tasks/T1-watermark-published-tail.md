# Task ADR-017-T1: The append-only tail, and the watermark that publishes an entry

**Depends-on:** none
**Covers:** none — no spec
**Estimated scope:** M (multi-file)
**Owner:** unassigned
**Produces:** `tail.Tail`, `tail.New`, `tail.Watermark`, `tail.Entry`, `tail.ChunkSize`, `tail.WriterToken`, `tail.Tail.TakeWriter`, `tail.Tail.Append`, `tail.Tail.Watermark`, `tail.Tail.Walk`, `tail.ErrWriterNotHeld`
**Consumes:** `tx.TxID` from ADR-002, `ports.Datom` from ADR-003
**Data dependency:** hermetic
**Proof map:** v1
**Rests-on:** `the watermark advancing only after an entry is completely written`, `chunks never moving once written`, `the writer token being required to append`

## Goal

Make an appended entry become visible in one atomic step, so a reader sees a
whole entry or no entry, and never takes a lock to find out which.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `internal/core/tail/doc.go` | add | Package comment: why publication replaces guarding, what a reader's one acquire-load buys, and how the structure fails and recovers. |
| `internal/core/tail/tail.go` | add | `Tail`, `Entry`, `Watermark`, `ChunkSize`, the chunked storage and the publish step. |
| `internal/core/tail/tail_test.go` | add | The tests below. |

★ A chunk holds 256 entries so the offset within one is a single byte and
locating an entry is a shift and a mask — the same eight-bit step ADR-001 uses to
descend the address space. It is a layout choice, not a shard count.

## Ordered Steps

1. [S1] Write the failing tests first (TDD red): `TestPartialEntryIsNeverVisible`, `TestReadersAndWriterActuallyOverlap`, `TestSnapshotIsRepeatable`, `TestChunkGrowthDoesNotMoveEntries`, `TestAppendRequiresTheWriterToken`. Run the Acceptance fence and confirm it is red. ⚠Check each name is SELECTED by the fence's `-run` filter before running any mutant. [proof: acceptance]
2. [S2] Define `Entry` — one published transaction: its `tx.TxID` and its datoms — and `Watermark`, a published position.
3. [S3] Implement chunked storage: `ChunkSize` entries per chunk, chunks addressed through an index that is replaced rather than mutated when it grows. ★A chunk is never moved once written, so a reader holding an older index still addresses valid memory.
4. [S4] Implement `Append`: write the entry into its slot COMPLETELY, then advance the watermark with one atomic store. ★The order is the whole mechanism. An entry published before it is written is a torn read — data returned that never existed — and no amount of downstream checking recovers it.
5. [S5] Implement the reader side: one acquire-load of the watermark, then reads bounded by it. No mutex, no reference count, no epoch.
6. [S6] Require a writer token to append, refusing with `ErrWriterNotHeld` otherwise. ★ADR-003 gives a leaf ONE writer; a tail that accepts appends from anywhere would make the single-writer assumption a convention rather than a property, and this design's correctness rests on it.
7. [S7] Write the package comment stating why publication replaces guarding, and what a reader is guaranteed. [proof: human: a reader confirms the comment explains why an unfinished entry is UNREACHABLE rather than protected, which is the distinction the whole design turns on]

## Acceptance

```bash
set -o pipefail
go test ./internal/core/tail/... -race -run 'TestPartialEntry|TestReadersAndWriter|TestSnapshotIsRepeatable|TestChunkGrowth|TestAppendRequires' -count=1 2>&1 | tee /tmp/adr017-t1.out \
  && ! grep -qE "no tests to run|matched no packages|^FAIL|^--- FAIL|DATA RACE" /tmp/adr017-t1.out
```

⚠ `-race` is part of the fence rather than a convenience. The claim is about
concurrent access, and a suite that never runs the detector cannot observe the
class of fault this task exists to prevent. `DATA RACE` is in the grep because
the detector's report does not always change the exit code of the run that
produced it.

## Tests

| Test name | File | Verifies | Covers | Steps |
|-----------|------|----------|--------|-------|
| `TestPartialEntryIsNeverVisible` | `internal/core/tail/tail_test.go` | Under concurrent append and read, every entry a reader observes is complete — its identifier and its datoms both present and consistent — so publication happens after the write and never before | — | S2, S4 |
| `TestReadersAndWriterActuallyOverlap` | `internal/core/tail/tail_test.go` | The concurrency test actually overlapped: a reader observed the watermark ADVANCE during its run. A run that never saw growth fails rather than passing quietly | — | S4, S5 |
| `TestSnapshotIsRepeatable` | `internal/core/tail/tail_test.go` | A watermark loaded once yields the same entries however many times it is read, and appends made afterwards are not in it | — | S2, S5 |
| `TestChunkGrowthDoesNotMoveEntries` | `internal/core/tail/tail_test.go` | Entries written before a chunk-index growth are still readable, at the same positions, through an index captured before it | — | S3 |
| `TestAppendRequiresTheWriterToken` | `internal/core/tail/tail_test.go` | An append without the writer token is refused with `ErrWriterNotHeld`, so the single-writer assumption is a property rather than a convention | — | S6 |

⚠ `TestReadersAndWriterActuallyOverlap` exists because a clean race-detector run
proves nothing if the reader and the writer never ran at the same time. A
concurrency test that happens to serialize is indistinguishable from one that is
correct, and it is green either way. Asserting that overlap OCCURRED is what
makes the other tests' silence meaningful.

## Reachability

| Rung | How this task shows it |
|------|------------------------|
| 1 — exists | The five tests above. |
| 2 — something selects it | `Append` is the only way an entry enters the tail and the watermark is the only way one leaves it; every test goes through both. T2's snapshot is the first consumer. |
| 3 — the caller can discover it | Exported doc comments and a named sentinel; the reader API takes a watermark and returns entries, so its signature states that a read is bounded. |
| 4 — it is used | Nothing measures this yet; no storage engine exists. |

## Mutation Log

- 2026-09-04 · ed35910* · mutant killed · exit 1 · `internal/core/tail/tail.go` · publishes an entry whose payload was not written, which is exactly what a reader observes if the watermark advances before the write completes: the identifier is present and the datoms are not · acceptance-sha256:5e2353cd9cf6a5d4ea52404a44d9c96e9ff1a3cd12043137983c5644a968e3bf · covers:the watermark advancing only after an entry is completely written
- 2026-09-04 · ed35910* · mutant killed · exit 1 · `internal/core/tail/tail.go` · drops the most recent chunk when the index grows so a fresh empty one replaces it, which is what happens whenever growth does not carry existing chunks forward by pointer: entries written before a growth become unreadable · acceptance-sha256:5e2353cd9cf6a5d4ea52404a44d9c96e9ff1a3cd12043137983c5644a968e3bf · covers:chunks never moving once written
- 2026-09-04 · ed35910* · mutant killed · exit 1 · `internal/core/tail/tail.go` · accepts an append from any caller including the zero token, so the single-writer assumption becomes a convention and two appenders would compute the same slot · acceptance-sha256:5e2353cd9cf6a5d4ea52404a44d9c96e9ff1a3cd12043137983c5644a968e3bf · covers:the writer token being required to append

## Invariants

- The watermark advances only after the entry it publishes is completely written.
- A chunk is never moved or reallocated once written.
- A reader performs exactly one acquire-load and takes no lock.
- An append without the writer token is refused.
- The tail is append-only: no published entry is ever modified.

## Risks

- A concurrency test can pass by accident. `TestReadersAndWriterActuallyOverlap` asserts the overlap happened, and the fence runs under `-race`, so a green result means the detector looked at overlapping access rather than at nothing.
- Go's memory model makes the publish-then-load pattern correct only if BOTH sides use atomics. A plain load on the reader side would usually work and would be a race the detector catches — which is why the detector is in the fence rather than in a nightly job.

## Stop Condition

Stop and ask if anything requires a reader to block. That is the one thing this
design does not permit, and it would mean the read path has grown a structure
that cannot be published atomically — which is a decision to revisit rather than
an exception to grant.

## Out of Scope

- The `tx.TxID` bound on a read, and the no-lock source guard — that is T2.
- When the tail is sealed into a segment (deferred: `docs/adr/BACKLOG.md` §15)
- Any index over the tail (deferred: `docs/adr/BACKLOG.md` §15)

## Verification Log
- 2026-09-04 · ed35910* · exit 0 · `set -o pipefail …` · acceptance-sha256:5e2353cd9cf6a5d4ea52404a44d9c96e9ff1a3cd12043137983c5644a968e3bf · ms:2070
- 2026-09-04 · ed35910* · exit 0 · `set -o pipefail …` · acceptance-sha256:5e2353cd9cf6a5d4ea52404a44d9c96e9ff1a3cd12043137983c5644a968e3bf · ms:1950
- 2026-09-04 · ed35910* · exit 0 · `set -o pipefail …` · acceptance-sha256:5e2353cd9cf6a5d4ea52404a44d9c96e9ff1a3cd12043137983c5644a968e3bf · ms:2005
- 2026-09-04 · ed35910* · exit 0 · `set -o pipefail …` · acceptance-sha256:5e2353cd9cf6a5d4ea52404a44d9c96e9ff1a3cd12043137983c5644a968e3bf · ms:2044
- 2026-09-04 · ed35910* · exit 0 · `set -o pipefail …` · acceptance-sha256:5e2353cd9cf6a5d4ea52404a44d9c96e9ff1a3cd12043137983c5644a968e3bf · ms:2051
