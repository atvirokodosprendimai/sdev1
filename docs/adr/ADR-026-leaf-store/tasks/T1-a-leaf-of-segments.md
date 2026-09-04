# Task ADR-026-T1: Read one answer out of many segments

**Depends-on:** none
**Covers:** none — no spec
**Estimated scope:** L (cross-boundary)
**Owner:** unassigned
**Produces:** `leafstore.Store`, `leafstore.Open`, `leafstore.Store.Append`, `leafstore.Store.Seal`, `leafstore.Store.History`, `leafstore.Store.Load`, `leafstore.Store.Attributes`, `leafstore.Store.Entities`, `leafstore.Store.Close`, `leafstore.Store.Pending`, `leafstore.Store.Segments`, `leafstore.Extension`, `leafstore.ErrNoSnapshot`, `leafstore.ErrClosed`
**Consumes:** `segstore.Create`, `segstore.Open`, `segstore.ErrNoSuchBlock` from ADR-024; `datom.Encode`, `datom.Decode` from ADR-025; `temporal.Visible` from ADR-002; `ports.Store`, `ports.Snapshot`, `ports.Datom` from ADR-003
**Data dependency:** hermetic — a temporary directory the test owns
**Proof map:** v1
**Rests-on:** `an answer that does not depend on what the segments are called`, `a fact surviving the process that wrote it`, `a retraction in a later segment still hiding an earlier fact`, `a sealed fact appearing exactly once rather than in both the tail and a segment`, `a zero snapshot being refused rather than answered with nothing`, `a snapshot filter that cannot be applied on one read path and forgotten on another`, `a real read error being propagated rather than swallowed as a missing entity`

## Goal

Make many segments answer as one leaf, so that the answer is a property of the
facts rather than of the filesystem holding them.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `internal/core/leafstore/doc.go` | add | Why the listing is the manifest, and why names mean nothing. |
| `internal/core/leafstore/leafstore.go` | add | `Store`, `Open`, `Append`, `Seal`, `History`, `Load`, `Attributes`, `Entities`, `Close`. |
| `internal/core/leafstore/leafstore_test.go` | add | The tests below, against a real directory. |
| `internal/core/temporal/qualifiers.go` | modify | `At` — assembling a bound query where the package that owns the comparison can see it. |
| `internal/core/temporal/temporal_test.go` | modify | `TestAtBindsBothAxesAndResolveDoesNot`. |
| `internal/core/ports/ports.go` | modify | `Snapshot.Query`, so no store names both axes in its own file. |
| `internal/core/ports/asymmetry_test.go` | modify | The exemption, with its reason. |

⚠ Four of those files are governed by ADR-002 and ADR-003, not by this record.
Both changes exist because those records' own guards refused the first attempt,
and the Acceptance re-runs their suites for that reason.

★ A real temporary directory again, not a filesystem abstraction: the falsifier is
about what happens when files are RENAMED, and an abstraction would be asserting
the abstraction.

## Ordered Steps

1. [S1] Write the failing tests first (TDD red): `TestTheAnswerDoesNotDependOnSegmentOrder`, `TestAFactSurvivesReopening`, `TestTheLatestValueWinsAcrossSegments`, `TestARetractionInALaterSegmentHidesAnEarlierFact`, `TestASealedFactAppearsExactlyOnce`, `TestAnEntityWithNoFactsIsEmptyNotAnError`, `TestARealReadErrorIsNotAnEmptyAnswer`, `TestAZeroSnapshotIsRefused`, `TestSealingNothingWritesNoSegment`, `TestAPartialWriteIsIgnored`, `TestAttributesAreTheShapeNotTheHistory`, `TestLoadIsHistoryFilteredAtTheSnapshot`, `TestEntitiesListsWhatTheLeafHolds`. Run the Acceptance fence and confirm it is red. ⚠Check each name is SELECTED by the fence's `-run` filter before running any mutant. [proof: acceptance]
2. [S2] Implement `Open` to glob the directory for `*.seg`, skip dot-prefixed names, and open each through `segstore.Open`. ⚠A dot-prefixed file is what ADR-024 calls a partial write. [proof: mutation]
3. [S3] Implement `Append` to add datoms to an in-memory tail and nothing else. ⚠No flush — ADR-020 fixed the commit point at memory, and moving it here would change the latency contract as a side effect. [proof: mutation]
4. [S4] Implement `Seal` to group the tail by entity, encode each group through ADR-025, write one block per entity through ADR-024, and publish under a name with **no meaning** — random, unordered, carrying nothing a reader needs. [proof: mutation]
5. [S5] Clear the tail under the SAME exclusive hold that publishes the segment. ⚠Between a rename and a separate clear, a read sees the fact twice. [proof: mutation]
6. [S6] Sealing an empty tail writes no segment at all, rather than an empty one that every later read must open.
7. [S7] Implement `History` as the primitive: every datom the leaf holds for one entity, from every segment and the tail, sorted by `TxID`. ⚠Sorted by transaction, never by the order segments were opened. [proof: mutation]
8. [S8] Translate `segstore.ErrNoSuchBlock` to "this segment holds nothing for that entity" in exactly ONE place, and let every other error out. ⚠A refusal swallowed in a loop is how a real error becomes an empty answer — and both halves are proven, because a loop that swallowed everything would pass a test that only checks the missing-entity case. [proof: mutation]
9. [S9] Implement `Load` as `History` filtered by `temporal.Visible`, refusing a zero `ports.Snapshot` with `ErrNoSnapshot` first. ★One primitive and one filter, so a snapshot cannot be applied on one read path and forgotten on another. ⚠A zero `TxID` bounds the system axis at before-anything, so the read returns nothing and looks exactly like an entity with no facts. [proof: mutation]
10. [S10] Implement `Attributes` on top of `Load`, keeping the latest visible datom per attribute and only where it is an assertion — the present shape, not the history.
11. [S11] Implement `Entities` as the union of every segment's keys and the tail's. ★It is a directory listing of one leaf, not the language enumeration `BACKLOG.md` §20 defers — that one is about `SELECT` over unnamed entities and needs a planner.
12. [S12] Satisfy ADR-002's and ADR-003's guards rather than exempting this package from the first. ⚠`temporal` refuses any file outside it that names BOTH time axes, so building a `Query` here is the second comparison site that rule exists to prevent — the assembly moves to `temporal.At` and `ports.Snapshot.Query`. ADR-003's asymmetry guard does take an exemption, because this package IMPLEMENTS the write port rather than consuming one, and it is written down with its reason. [proof: acceptance]

## Acceptance

```bash
set -o pipefail
go test ./internal/core/leafstore/... -race -run 'TestTheAnswerDoesNotDependOnSegmentOrder|TestAFactSurvivesReopening|TestTheLatestValueWinsAcrossSegments|TestARetractionInALaterSegmentHidesAnEarlierFact|TestASealedFactAppearsExactlyOnce|TestAnEntityWithNoFactsIsEmptyNotAnError|TestARealReadErrorIsNotAnEmptyAnswer|TestAZeroSnapshotIsRefused|TestSealingNothingWritesNoSegment|TestAPartialWriteIsIgnored|TestAttributesAreTheShapeNotTheHistory|TestLoadIsHistoryFilteredAtTheSnapshot|TestEntitiesListsWhatTheLeafHolds' -count=1 2>&1 | tee /tmp/adr026-t1a.out \
  && ! grep -qE "no tests to run|matched no packages|^FAIL|^--- FAIL|DATA RACE" /tmp/adr026-t1a.out \
  && go test ./internal/core/leafstore/... ./internal/core/segstore/... ./internal/core/datom/... ./internal/core/temporal/... ./internal/core/ports/... -race -count=1 2>&1 | tee /tmp/adr026-t1b.out \
  && ! grep -qE "no tests to run|matched no packages|^FAIL|^--- FAIL|DATA RACE" /tmp/adr026-t1b.out \
  && go test ./internal/core/temporal/... -race -run 'TestAtBindsBothAxesAndResolveDoesNot' -count=1 2>&1 | tee /tmp/adr026-t1c.out \
  && ! grep -qE "no tests to run|matched no packages|^FAIL|^--- FAIL|DATA RACE" /tmp/adr026-t1c.out
```

The second command re-runs the suites this store is built out of, and the two whose
guards it had to satisfy: `temporal` holds the rule that only one place may name
both time axes, and `ports` holds the asymmetry exemption list.

## Tests

| Test name | File | Verifies | Covers | Steps |
|-----------|------|----------|--------|-------|
| `TestTheAnswerDoesNotDependOnSegmentOrder` | `internal/core/leafstore/leafstore_test.go` | A leaf sealed three times answers identically after **every segment is renamed** so that lexical order is reversed — **the falsifier ADR-026 names in `Enforced-by:`**. Also asserts the rename really did reverse the listing, so the test cannot pass by having changed nothing | — | S2, S7 |
| `TestAFactSurvivesReopening` | `internal/core/leafstore/leafstore_test.go` | A fact appended, sealed and closed is read back byte-identical by a `Store` opened fresh on the same directory | — | S2, S4 |
| `TestTheLatestValueWinsAcrossSegments` | `internal/core/leafstore/leafstore_test.go` | An attribute asserted in the first segment and re-asserted in the third resolves to the later value. ⚠ The fixture RENAMES the files so the later value sits in the alphabetically earlier one, and the assertion takes the LAST `mass` in the returned order rather than the maximum by `TxID` — picking the maximum would re-do the store's job inside the test, and it would then pass with the store's ordering deleted | — | S7 |
| `TestARetractionInALaterSegmentHidesAnEarlierFact` | `internal/core/leafstore/leafstore_test.go` | A retraction sealed after the assertion it withdraws still wins across the segment boundary, so `Attributes` no longer carries the attribute while `History` still returns both datoms | — | S7, S10 |
| `TestASealedFactAppearsExactlyOnce` | `internal/core/leafstore/leafstore_test.go` | A fact appended and then sealed is returned once, not once from the tail and once from the segment — with a second fact left unsealed, so the tail is genuinely non-empty at read time | — | S5 |
| `TestAnEntityWithNoFactsIsEmptyNotAnError` | `internal/core/leafstore/leafstore_test.go` | An entity no segment holds is an empty slice and a nil error, across a leaf whose segments each hold a different entity | — | S8 |
| `TestARealReadErrorIsNotAnEmptyAnswer` | `internal/core/leafstore/leafstore_test.go` | A byte flipped inside a block's stored data, leaving the index and trailer intact, makes `History` return an ERROR rather than an empty answer — the other half of the translation, which a test that only covers a missing entity leaves entirely unproven | — | S8 |
| `TestAZeroSnapshotIsRefused` | `internal/core/leafstore/leafstore_test.go` | A zero `ports.Snapshot` is `ErrNoSnapshot` from both `Load` and `Attributes`, rather than the empty answer it would otherwise produce | — | S9 |
| `TestSealingNothingWritesNoSegment` | `internal/core/leafstore/leafstore_test.go` | `Seal` with an empty tail leaves the directory unchanged, so a caller that seals on a timer does not fill a leaf with empty files | — | S6 |
| `TestAPartialWriteIsIgnored` | `internal/core/leafstore/leafstore_test.go` | A dot-prefixed leftover and an unrelated file in the directory are skipped, and the leaf still opens and answers | — | S2 |
| `TestAttributesAreTheShapeNotTheHistory` | `internal/core/leafstore/leafstore_test.go` | `Attributes` lists exactly the attributes whose latest visible datom is an assertion, while `History` still holds the retracted one | — | S10 |
| `TestLoadIsHistoryFilteredAtTheSnapshot` | `internal/core/leafstore/leafstore_test.go` | For several snapshots, `Load` equals `History` filtered by `temporal.Visible` — computed in the test rather than asserted from a fixture, so the two read paths cannot drift | — | S9 |
| `TestEntitiesListsWhatTheLeafHolds` | `internal/core/leafstore/leafstore_test.go` | `Entities` is the union of the sealed segments and the tail, sorted and without duplicates when an entity appears in both | — | S11 |
| `TestAtBindsBothAxesAndResolveDoesNot` | `internal/core/temporal/temporal_test.go` | `temporal.At` binds both axes while `ResolveQualifiers` leaves the system axis open on a lone instant — so the two constructors cannot quietly become the same thing | — | S12 |

## Reachability

| Rung | How this task shows it |
|------|------------------------|
| 1 — exists | The thirteen tests above, against a real directory. |
| 2 — something selects it | `Open` is the only way a leaf is read and `Seal` the only way one grows; `Store` satisfies `ports.Store`, asserted at compile time. |
| 3 — the caller can discover it | The refusals are named sentinels, and `Pending`/`Segments` make the split between tail and disk visible without reading the directory. |
| 4 — it is used | T2 wires the session onto it, so `ASSERT` survives a restart through `cmd/sdev1-ql`. |

## Mutation Log

- 2026-09-04 · c3af224* · mutant killed · exit 1 · `internal/core/leafstore/leafstore.go` · makes the transaction-order sort a no-op, so datoms come back in the order segments were opened — which is directory order, which is filename order; the leaf then answers differently after a rename · acceptance-sha256:bd708e21930cb0ec85ea44e61c8294ced0b155fff467805de947a8f374f97174 · covers:an answer that does not depend on what the segments are called
- 2026-09-04 · c3af224* · mutant killed · exit 1 · `internal/core/leafstore/leafstore.go` · inverts what Open admits, so real segments are skipped and leftovers are opened instead: a reopened leaf holds nothing it wrote and tries to parse a partial write · acceptance-sha256:bd708e21930cb0ec85ea44e61c8294ced0b155fff467805de947a8f374f97174 · covers:a fact surviving the process that wrote it
- 2026-09-04 · c3af224* · mutant killed · exit 1 · `internal/core/leafstore/leafstore.go` · lists an attribute whose latest visible datom is a RETRACTION, so an entity keeps carrying a fact that was withdrawn in a later segment · acceptance-sha256:bd708e21930cb0ec85ea44e61c8294ced0b155fff467805de947a8f374f97174 · covers:a retraction in a later segment still hiding an earlier fact
- 2026-09-04 · c3af224* · mutant killed · exit 1 · `internal/core/leafstore/leafstore.go` · publishes the segment without clearing the tail, so every sealed fact is returned twice — once from the tail and once from the file that now holds it, which reads as a fact asserted twice rather than as an error · acceptance-sha256:bd708e21930cb0ec85ea44e61c8294ced0b155fff467805de947a8f374f97174 · covers:a sealed fact appearing exactly once rather than in both the tail and a segment
- 2026-09-04 · c3af224* · mutant killed · exit 1 · `internal/core/leafstore/leafstore.go` · stops refusing a zero snapshot unless the business instant is also negative, so an ordinary zero snapshot is answered with nothing at all — indistinguishable from an entity that has no facts · acceptance-sha256:bd708e21930cb0ec85ea44e61c8294ced0b155fff467805de947a8f374f97174 · covers:a zero snapshot being refused rather than answered with nothing
- 2026-09-04 · c3af224* · mutant killed · exit 1 · `internal/core/leafstore/leafstore.go` · lets every datom through regardless of the snapshot, so Load stops being History filtered and returns facts that were not true at the instant asked about · acceptance-sha256:bd708e21930cb0ec85ea44e61c8294ced0b155fff467805de947a8f374f97174 · covers:a snapshot filter that cannot be applied on one read path and forgotten on another
- 2026-09-04 · c3af224* · mutant killed · exit 1 · `internal/core/leafstore/leafstore.go` · swallows EVERY read error as "this segment holds nothing for that entity", so a corrupt block on disk becomes an entity that simply has no facts · acceptance-sha256:bd708e21930cb0ec85ea44e61c8294ced0b155fff467805de947a8f374f97174 · covers:a real read error being propagated rather than swallowed as a missing entity
- 2026-09-04 · c3af224* · mutant killed · exit 1 · `internal/core/leafstore/leafstore.go` · makes the transaction-order sort a no-op, so datoms come back in the order segments were opened — which is directory order, which is filename order; the leaf then answers differently after a rename · acceptance-sha256:9413a4cc55effa0d686f21387e9c8290f09d538653e7c94a61b37db069b47e24 · covers:an answer that does not depend on what the segments are called
- 2026-09-04 · c3af224* · mutant killed · exit 1 · `internal/core/leafstore/leafstore.go` · inverts what Open admits, so real segments are skipped and leftovers are opened instead: a reopened leaf holds nothing it wrote · acceptance-sha256:9413a4cc55effa0d686f21387e9c8290f09d538653e7c94a61b37db069b47e24 · covers:a fact surviving the process that wrote it
- 2026-09-04 · c3af224* · mutant killed · exit 1 · `internal/core/leafstore/leafstore.go` · lists an attribute whose latest visible datom is a RETRACTION, so an entity keeps carrying a fact that was withdrawn in a later segment · acceptance-sha256:9413a4cc55effa0d686f21387e9c8290f09d538653e7c94a61b37db069b47e24 · covers:a retraction in a later segment still hiding an earlier fact
- 2026-09-04 · c3af224* · mutant killed · exit 1 · `internal/core/leafstore/leafstore.go` · publishes the segment without clearing the tail, so every sealed fact is returned twice — which reads as a fact asserted twice rather than as an error · acceptance-sha256:9413a4cc55effa0d686f21387e9c8290f09d538653e7c94a61b37db069b47e24 · covers:a sealed fact appearing exactly once rather than in both the tail and a segment
- 2026-09-04 · c3af224* · mutant killed · exit 1 · `internal/core/leafstore/leafstore.go` · stops refusing a zero snapshot unless the business instant is also negative, so an ordinary zero snapshot is answered with nothing at all · acceptance-sha256:9413a4cc55effa0d686f21387e9c8290f09d538653e7c94a61b37db069b47e24 · covers:a zero snapshot being refused rather than answered with nothing
- 2026-09-04 · c3af224* · mutant killed · exit 1 · `internal/core/leafstore/leafstore.go` · lets every datom through regardless of the snapshot, so Load stops being History filtered and returns facts that were not true at the instant asked about · acceptance-sha256:9413a4cc55effa0d686f21387e9c8290f09d538653e7c94a61b37db069b47e24 · covers:a snapshot filter that cannot be applied on one read path and forgotten on another
- 2026-09-04 · c3af224* · mutant killed · exit 1 · `internal/core/leafstore/leafstore.go` · swallows EVERY read error as "this segment holds nothing for that entity", so a corrupt block on disk becomes an entity that simply has no facts · acceptance-sha256:9413a4cc55effa0d686f21387e9c8290f09d538653e7c94a61b37db069b47e24 · covers:a real read error being propagated rather than swallowed as a missing entity

## Invariants

- The answer depends on the datoms, never on the filenames or the open order.
- A sealed fact is returned exactly once.
- Visibility is decided only by `temporal.Visible`, on one path.
- A missing block is translated in one place; every other error propagates.

## Risks

- ⚠ **A test that merely reads back what it wrote cannot see the ordering defect.** Merging in listing order is correct whenever the listing order happens to match the seal order, which it does in every test that does not interfere. The falsifier renames the files, and asserts the rename actually reversed the listing — otherwise it proves nothing while looking thorough.
- ⚠ **`TestTheLatestValueWinsAcrossSegments` must arrange for the LATER value to sit in the alphabetically EARLIER file**, or a listing-order merge passes it. The fixture checks that arrangement rather than hoping for it.
- ⚠ **The exactly-once test has to seal in the middle**, not before or after everything. A fact that is only ever in the tail, or only ever in a segment, is returned once by every implementation including a broken one.
- ⚠ **`TestLoadIsHistoryFilteredAtTheSnapshot` computes the expected answer rather than hard-coding it.** A hard-coded fixture would agree with whatever the code does on the day it was written; recomputing the filter in the test is what makes a divergence between the two read paths visible.
- Sealing writes one block per entity, so a tail spanning many entities produces a segment with many blocks. Nothing here bounds how many; that is the seal policy, deferred.
- Read cost is linear in segment count and this task does not measure it. Recorded as a follow-up on the parent record.

## Stop Condition

Stop and ask before ordering segments by filename, or before assuming the order
`Open` received them in means anything. The result is correct on the machine that
wrote them and wrong on the one that restored them, and the wrong answer is a
plausible value rather than an error.

## Out of Scope

- When to seal, and compaction (deferred: `docs/adr/BACKLOG.md` §15)
- An index over a leaf's segments (deferred: `docs/adr/BACKLOG.md` §15)
- Enumerating entities from the LANGUAGE, across leaves and without a name (deferred: `docs/adr/BACKLOG.md` §20)
- Wiring the session and the CLI onto this store (deferred: T2 of this record)
- Erasure-coding a sealed segment (deferred: `docs/adr/BACKLOG.md` §12)
- What visibility means (permanent: boundary: ADR-002 owns the single comparison site, and a second implementation here would be a rule nobody wrote down)

## Verification Log
- 2026-09-04 · c3af224* · exit 1 · `set -o pipefail …` · acceptance-sha256:451e3ca24611d2edf6a1b27b1b08b49707e8c9cedbb44d52f146eca940a19124 · ms:4122
  ```
  --- last 10 line(s) of stdout
  ok  	github.com/atvirokodosprendimai/sdev1/internal/core/leafstore	1.289s
  ok  	github.com/atvirokodosprendimai/sdev1/internal/core/leafstore	1.291s
  ok  	github.com/atvirokodosprendimai/sdev1/internal/core/segstore	1.320s
  ok  	github.com/atvirokodosprendimai/sdev1/internal/core/datom	1.031s
  --- FAIL: TestVisibleIsTheOnlyComparisonSite (0.02s)
      temporal_test.go:254: these files outside internal/core/temporal name BOTH time axes: [../../../internal/core/leafstore/leafstore.go]
          the two axes are compared in exactly one place, so that a caller passing one instant into both parameters is reviewable in one file
  FAIL
  FAIL	github.com/atvirokodosprendimai/sdev1/internal/core/temporal	0.055s
  FAIL
  ```
- 2026-09-04 · c3af224* · exit 0 · `set -o pipefail …` · acceptance-sha256:c2a1c407a846cd75866876f16abf4a8c70cb12b1a7baa66a195622077fb6b14c · ms:4108
- 2026-09-04 · c3af224* · exit 0 · `set -o pipefail …` · acceptance-sha256:bd708e21930cb0ec85ea44e61c8294ced0b155fff467805de947a8f374f97174 · ms:4266
- 2026-09-04 · c3af224* · exit 0 · `set -o pipefail …` · acceptance-sha256:bd708e21930cb0ec85ea44e61c8294ced0b155fff467805de947a8f374f97174 · ms:4119
- 2026-09-04 · c3af224* · exit 0 · `set -o pipefail …` · acceptance-sha256:bd708e21930cb0ec85ea44e61c8294ced0b155fff467805de947a8f374f97174 · ms:4136
- 2026-09-04 · c3af224* · exit 0 · `set -o pipefail …` · acceptance-sha256:bd708e21930cb0ec85ea44e61c8294ced0b155fff467805de947a8f374f97174 · ms:4270
- 2026-09-04 · c3af224* · exit 0 · `set -o pipefail …` · acceptance-sha256:bd708e21930cb0ec85ea44e61c8294ced0b155fff467805de947a8f374f97174 · ms:4142
- 2026-09-04 · c3af224* · exit 0 · `set -o pipefail …` · acceptance-sha256:bd708e21930cb0ec85ea44e61c8294ced0b155fff467805de947a8f374f97174 · ms:4106
- 2026-09-04 · c3af224* · exit 0 · `set -o pipefail …` · acceptance-sha256:bd708e21930cb0ec85ea44e61c8294ced0b155fff467805de947a8f374f97174 · ms:4054
- 2026-09-04 · c3af224* · exit 0 · `set -o pipefail …` · acceptance-sha256:bd708e21930cb0ec85ea44e61c8294ced0b155fff467805de947a8f374f97174 · ms:4175
- 2026-09-04 · c3af224* · exit 0 · `set -o pipefail …` · acceptance-sha256:9413a4cc55effa0d686f21387e9c8290f09d538653e7c94a61b37db069b47e24 · ms:5974
- 2026-09-04 · c3af224* · exit 0 · `set -o pipefail …` · acceptance-sha256:9413a4cc55effa0d686f21387e9c8290f09d538653e7c94a61b37db069b47e24 · ms:5738
- 2026-09-04 · c3af224* · exit 0 · `set -o pipefail …` · acceptance-sha256:9413a4cc55effa0d686f21387e9c8290f09d538653e7c94a61b37db069b47e24 · ms:5625
- 2026-09-04 · c3af224* · exit 0 · `set -o pipefail …` · acceptance-sha256:9413a4cc55effa0d686f21387e9c8290f09d538653e7c94a61b37db069b47e24 · ms:5792
- 2026-09-04 · c3af224* · exit 0 · `set -o pipefail …` · acceptance-sha256:9413a4cc55effa0d686f21387e9c8290f09d538653e7c94a61b37db069b47e24 · ms:5695
- 2026-09-04 · c3af224* · exit 0 · `set -o pipefail …` · acceptance-sha256:9413a4cc55effa0d686f21387e9c8290f09d538653e7c94a61b37db069b47e24 · ms:5756
- 2026-09-04 · c3af224* · exit 0 · `set -o pipefail …` · acceptance-sha256:9413a4cc55effa0d686f21387e9c8290f09d538653e7c94a61b37db069b47e24 · ms:5693
- 2026-09-04 · c3af224* · exit 0 · `set -o pipefail …` · acceptance-sha256:9413a4cc55effa0d686f21387e9c8290f09d538653e7c94a61b37db069b47e24 · ms:5654
