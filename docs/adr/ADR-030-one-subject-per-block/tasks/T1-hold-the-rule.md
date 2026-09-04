# Task ADR-030-T1: Hold the rule the code already follows

**Depends-on:** none
**Covers:** none — no spec
**Estimated scope:** S
**Owner:** unassigned
**Produces:** no new surface — a gate over `leafstore`'s block grouping
**Consumes:** `leafstore.Store.Seal`, `leafstore.Store.Compact` from ADR-026 and ADR-029; `segstore.Open`, `segstore.Reader.Keys`, `segstore.Reader.Get` from ADR-024; `datom.Decode` from ADR-025
**Data dependency:** hermetic — a leaf in a temporary directory
**Proof map:** v1
**Rests-on:** `a block naming exactly one subject, after a seal and after a compaction`

## Goal

Turn a property the code happens to have into one a change has to argue with.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `internal/core/leafstore/subject_test.go` | add | The falsifier, over a real sealed segment and a real compacted one. |

★ No production file changes. That is the point: the behaviour is already right,
and what is missing is the thing that notices when it stops being.

## Ordered Steps

1. [S1] Write the failing test first (TDD red): `TestNoBlockMixesSubjects`. ⚠It cannot fail against the current code, so confirm instead that it FAILS against a deliberately mixed segment — the test builds one by hand and asserts its own checker rejects it, so the checker is not vacuous. Run the Acceptance fence. [proof: acceptance]
2. [S2] Seal a leaf holding several entities through the ordinary write path, reopen the file through `segstore`, and assert every block decodes to datoms naming exactly the key it was stored under. [proof: mutation]
3. [S3] Repeat the assertion after a COMPACTION, because compaction rewrites every block and is the operation least likely to be re-read. [proof: mutation]

## Acceptance

```bash
set -o pipefail
go test ./internal/core/leafstore/... -race -run 'TestNoBlockMixesSubjects' -count=1 2>&1 | tee /tmp/adr030-t1a.out \
  && ! grep -qE "no tests to run|matched no packages|^FAIL|^--- FAIL|DATA RACE" /tmp/adr030-t1a.out \
  && go test ./internal/core/leafstore/... ./internal/core/segstore/... ./internal/core/datom/... -race -count=1 2>&1 | tee /tmp/adr030-t1b.out \
  && ! grep -qE "no tests to run|matched no packages|^FAIL|^--- FAIL|DATA RACE" /tmp/adr030-t1b.out
```

## Tests

| Test name | File | Verifies | Covers | Steps |
|-----------|------|----------|--------|-------|
| `TestNoBlockMixesSubjects` | `internal/core/leafstore/subject_test.go` | Every block of a sealed segment, and of a compacted one, decodes to datoms naming exactly one entity — the key it is stored under. **The falsifier ADR-030 names in `Enforced-by:`.** ⚠ It also builds a deliberately MIXED segment and asserts its own checker rejects that, so a checker that passed everything cannot pass this | — | S2, S3 |

## Reachability

| Rung | How this task shows it |
|------|------------------------|
| 1 — exists | The test above, over files written by the ordinary path. |
| 2 — something selects it | `leafstore` is the only thing that groups datoms into blocks, and both of its writers — `Seal` and `Compact` — are exercised. |
| 3 — the caller can discover it | Nothing changes for a caller. The rule is about what the format may contain, and it is discoverable from the record. |
| 4 — it is used | The gate runs on every suite. ⚠ It guards a property rather than adding one. |

## Mutation Log

- 2026-09-04 · 7c73e8c* · mutant killed · exit 1 · `internal/core/leafstore/leafstore.go` · packs every subject of a seal into one block, which is the compression improvement this rule exists to refuse: one subject data becomes a probe for another, shredding becomes a rewrite of everything sharing the block, and reclaim stops working · acceptance-sha256:7f13d7ab06fe251ac054f734522a83cf0cd88658db50e3dd4a26974f7293c6f1 · covers:a block naming exactly one subject, after a seal and after a compaction
- 2026-09-04 · 7c73e8c* · mutant killed · exit 1 · `internal/core/leafstore/compact.go` · corrupts what compaction writes into each block, so the assertion after a compaction is shown to be reading the compacted FILE rather than inheriting the seal check · acceptance-sha256:7f13d7ab06fe251ac054f734522a83cf0cd88658db50e3dd4a26974f7293c6f1 · covers:a block naming exactly one subject, after a seal and after a compaction

## Invariants

- Every block names exactly one subject, after a seal and after a compaction.

## Risks

- ⚠ **A test that cannot fail is the whole hazard of this task.** The code already
  satisfies the rule, so a checker with a bug — reading no blocks, or comparing
  nothing — passes exactly as loudly as a correct one. The test therefore builds a
  segment that DOES mix subjects and asserts the checker rejects it, so the
  checker is proved able to fail before it is trusted to pass.
- ⚠ **Testing the writer's intent rather than the file proves nothing.** The
  assertion reads the sealed FILE back through `segstore`, not the in-memory
  grouping that produced it.
- ⚠ **Compaction is the likelier regression than sealing.** It rewrites every
  block at once and is the natural place to "improve" packing, so it is asserted
  separately rather than assumed to inherit the property.

## Stop Condition

Stop and ask before relaxing this to buy compression, however good the benchmark.
The gain is real; so is making one subject's data a probe for another's, and
making crypto-shredding a rewrite of everything sharing a block.

## Out of Scope

- Recovering the compression ratio by interning or a shared dictionary (deferred: `docs/adr/BACKLOG.md` §12)
- How large one subject's block may get (deferred: `docs/adr/BACKLOG.md` §15)
- What a block IS (permanent: boundary: ADR-005 owns its shape, header and checksum; this task asserts only what one contains)

## Verification Log
- 2026-09-04 · 7c73e8c* · exit 0 · `set -o pipefail …` · acceptance-sha256:7f13d7ab06fe251ac054f734522a83cf0cd88658db50e3dd4a26974f7293c6f1 · ms:4144
- 2026-09-04 · 7c73e8c* · exit 0 · `set -o pipefail …` · acceptance-sha256:7f13d7ab06fe251ac054f734522a83cf0cd88658db50e3dd4a26974f7293c6f1 · ms:4072
- 2026-09-04 · 7c73e8c* · exit 0 · `set -o pipefail …` · acceptance-sha256:7f13d7ab06fe251ac054f734522a83cf0cd88658db50e3dd4a26974f7293c6f1 · ms:4085
