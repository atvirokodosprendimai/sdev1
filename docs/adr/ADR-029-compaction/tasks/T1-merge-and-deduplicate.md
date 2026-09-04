# Task ADR-029-T1: Merge the segments, and return a datom once

**Depends-on:** none
**Covers:** none — no spec
**Estimated scope:** M
**Owner:** unassigned
**Produces:** `leafstore.Store.Compact`, `leafstore.Store.ShouldCompact`, `leafstore.Policy.MaxSegments`
**Consumes:** `leafstore.Store`, `leafstore.Store.History` from ADR-026; `segstore.Create`, `segstore.Seal` from ADR-024; `datom.Encode`, `datom.Decode` from ADR-025; `ports.Datom` from ADR-003
**Data dependency:** hermetic — a leaf in a temporary directory the test owns
**Proof map:** v1
**Rests-on:** `a datom returned once however many segments hold it`, `a compaction that changes no answer`, `an identity that is full equality rather than a chosen key`, `deciding a compaction is due without performing one`

## Goal

Stop a leaf's read cost growing with the number of times it has been sealed, and
make an interrupted compaction harmless rather than corrupting.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `internal/core/leafstore/compact.go` | add | `Compact`, `ShouldCompact`, and the deduplication a read applies. |
| `internal/core/leafstore/compact_test.go` | add | The tests below, against a real directory. |
| `internal/core/leafstore/leafstore.go` | modify | `History` deduplicates what it gathered. |
| `internal/core/leafstore/policy.go` | modify | `Policy` gains `MaxSegments`. |

## Ordered Steps

1. [S1] Write the failing tests first (TDD red): `TestAnInterruptedCompactionDoesNotDuplicate`, `TestCompactionChangesNoAnswer`, `TestCompactionPublishesBeforeRemoving`, `TestTwoTransactionsAreNotOneDatom`, `TestShouldCompactDoesNotCompact`. Run the Acceptance fence and confirm it is red. ⚠Check each name is SELECTED by the fence's `-run` filter before running any mutant. [proof: acceptance]
2. [S2] Deduplicate in `History` by FULL equality — every field, transaction identifier included. ⚠Not a chosen key: two identical assertions from different transactions are two facts, and full equality can never conflate them. [proof: mutation]
3. [S3] Implement `Compact` to gather every entity in the leaf, write one segment holding all of their datoms, and publish it through ADR-024. [proof: mutation]
4. [S4] Publish the merged segment BEFORE removing the inputs. ⚠The reverse loses data: a reader between a removal and a publish sees neither copy, and a crash there destroys what was durable a moment earlier. [proof: human: a reader confirms `w.Seal()` — ADR-024's rename, and the publication — precedes every `os.Remove`. ⚠**No test can see this ordering.** Absent a crash both orders end in the same directory with the same facts, and the only observation that separates them is a failure injected between the two points, which needs a filesystem this suite cannot have. What IS proven is the consequence that matters: the state the chosen order can leave behind is harmless, by `TestAnInterruptedCompactionDoesNotDuplicate`]
5. [S5] Leave the leaf untouched when a compaction fails, so the worst outcome is the harmless overlap of S2 rather than a partial leaf. [proof: human: a reader confirms the merged segment is built through `segstore.Create` and published only by `Seal`'s rename, so a failure before it leaves nothing at the destination and a failure after it leaves the overlap S2 handles — provoking a mid-rename failure needs a filesystem this test cannot have]
6. [S6] Add `Policy.MaxSegments` and `ShouldCompact`, deciding and never performing — the same shape ADR-028 fixed for sealing. [proof: mutation]

## Acceptance

```bash
set -o pipefail
go test ./internal/core/leafstore/... -race -run 'TestAnInterruptedCompactionDoesNotDuplicate|TestCompactionChangesNoAnswer|TestCompactionPublishesBeforeRemoving|TestTwoTransactionsAreNotOneDatom|TestShouldCompactDoesNotCompact' -count=1 2>&1 | tee /tmp/adr029-t1a.out \
  && ! grep -qE "no tests to run|matched no packages|^FAIL|^--- FAIL|DATA RACE" /tmp/adr029-t1a.out \
  && go test ./internal/core/leafstore/... ./internal/core/segstore/... ./internal/core/datom/... ./internal/core/eval/... ./internal/core/session/... -race -count=1 2>&1 | tee /tmp/adr029-t1b.out \
  && ! grep -qE "no tests to run|matched no packages|^FAIL|^--- FAIL|DATA RACE" /tmp/adr029-t1b.out
```

The second command re-runs everything that reads a leaf, because deduplication
changes what `History` returns and the evaluator and the session both go through
it.

## Tests

| Test name | File | Verifies | Covers | Steps |
|-----------|------|----------|--------|-------|
| `TestAnInterruptedCompactionDoesNotDuplicate` | `internal/core/leafstore/compact_test.go` | A leaf whose merged segment was published and whose inputs were NOT removed — exactly what a crash leaves — answers identically to one that was never compacted. **The falsifier ADR-029 names in `Enforced-by:`**, and the state is built by copying the merged file back rather than by mocking anything | — | S2, S4 |
| `TestCompactionChangesNoAnswer` | `internal/core/leafstore/compact_test.go` | `History` for every entity is byte-identical before and after a compaction, including superseded and retracted datoms — compaction is layout and drops no fact | — | S1, S3 |
| `TestCompactionPublishesBeforeRemoving` | `internal/core/leafstore/compact_test.go` | After a compaction the leaf holds exactly ONE segment, that segment is not one of the inputs, and every fact survives. ⚠ It does NOT prove the ordering — see S4 — it proves the outcome: the merge published under a name of its own and nothing was left behind | — | S3 |
| `TestTwoTransactionsAreNotOneDatom` | `internal/core/leafstore/compact_test.go` | Two datoms identical in entity, attribute and value but from DIFFERENT transactions both survive deduplication, and two identical in every field collapse to one — so the identity is equality rather than a chosen key | — | S2 |
| `TestShouldCompactDoesNotCompact` | `internal/core/leafstore/compact_test.go` | `ShouldCompact` on a leaf well past its segment bound leaves the segment count unchanged | — | S6 |

## Reachability

| Rung | How this task shows it |
|------|------------------------|
| 1 — exists | The five tests above, against a real directory. |
| 2 — something selects it | `Compact` is the only thing that removes a segment file, and `History` the only place a leaf's datoms are gathered. |
| 3 — the caller can discover it | `ShouldCompact` and `Policy.MaxSegments` say what the bound is; the deduplication is invisible by design, which is what makes an interrupted compaction harmless. |
| 4 — it is used | Deduplication is on every read the moment this lands. ⚠ `Compact` itself is called by nothing yet — who runs it is a deployment decision (`BACKLOG.md` §15). |

## Mutation Log

- 2026-09-04 · 6765feb* · mutant killed · exit 1 · `internal/core/leafstore/leafstore.go` · stops deduplicating, so a leaf carrying an interrupted compaction counts every datom twice — and it carries that state permanently, because nothing cleans the overlap up · acceptance-sha256:e9834d529360fb241ad20e349fdff996d2688e30ea5d6126b0b2ddbdd3831eb9 · covers:a datom returned once however many segments hold it
- 2026-09-04 · 6765feb* · mutant inconclusive · exit 1 · `internal/core/leafstore/compact.go` · drops the transaction from the identity, so two assertions from DIFFERENT transactions with the same entity, attribute and value collapse into one — which ADR-026 named as the reason not to deduplicate at all · acceptance-sha256:e9834d529360fb241ad20e349fdff996d2688e30ea5d6126b0b2ddbdd3831eb9 · covers:an identity that is full equality rather than a chosen key
  ```
  the fence failed on a build/parse error, not an assertion
  ```
- 2026-09-04 · 6765feb* · mutant survived · exit 0 · `internal/core/leafstore/compact.go` · removes the inputs before opening the merged segment, which is the ordering that loses data: a reader between the removal and the publish sees neither copy · acceptance-sha256:e9834d529360fb241ad20e349fdff996d2688e30ea5d6126b0b2ddbdd3831eb9 · covers:the merged segment published before the inputs are removed
  ```
  the fence passed with the mechanism broken; it may not materialize, compile, load, or assert on the changed path
  ```
- 2026-09-04 · 6765feb* · mutant killed · exit 1 · `internal/core/leafstore/compact.go` · silently drops any entity whose whole history is a single datom, so compaction stops being a layout operation and starts deciding what a leaf holds · acceptance-sha256:e9834d529360fb241ad20e349fdff996d2688e30ea5d6126b0b2ddbdd3831eb9 · covers:a compaction that changes no answer
- 2026-09-04 · 6765feb* · mutant killed · exit 1 · `internal/core/leafstore/compact.go` · compacts from inside the decision, so asking whether a compaction is due performs one — the I/O lands wherever ShouldCompact is called from, and who runs a compaction stops being a deployment decision · acceptance-sha256:e9834d529360fb241ad20e349fdff996d2688e30ea5d6126b0b2ddbdd3831eb9 · covers:deciding a compaction is due without performing one
- 2026-09-04 · 6765feb* · mutant killed · exit 1 · `internal/core/leafstore/compact.go` · drops the transaction from the identity, so two assertions from DIFFERENT transactions with the same entity, attribute and value collapse into one — which is precisely the reason ADR-026 gave for not deduplicating at all · acceptance-sha256:e9834d529360fb241ad20e349fdff996d2688e30ea5d6126b0b2ddbdd3831eb9 · covers:an identity that is full equality rather than a chosen key
- 2026-09-04 · 6765feb* · mutant killed · exit 1 · `internal/core/leafstore/leafstore.go` · stops deduplicating, so a leaf carrying an interrupted compaction counts every datom twice — permanently, because nothing cleans the overlap up · acceptance-sha256:e9834d529360fb241ad20e349fdff996d2688e30ea5d6126b0b2ddbdd3831eb9 · covers:a datom returned once however many segments hold it
- 2026-09-04 · 6765feb* · mutant killed · exit 1 · `internal/core/leafstore/compact.go` · silently drops any entity whose whole history is a single datom, so compaction stops being a layout operation and starts deciding what a leaf holds · acceptance-sha256:e9834d529360fb241ad20e349fdff996d2688e30ea5d6126b0b2ddbdd3831eb9 · covers:a compaction that changes no answer
- 2026-09-04 · 6765feb* · mutant killed · exit 1 · `internal/core/leafstore/compact.go` · compacts from inside the decision, so asking whether a compaction is due performs one and the I/O lands wherever ShouldCompact is called from · acceptance-sha256:e9834d529360fb241ad20e349fdff996d2688e30ea5d6126b0b2ddbdd3831eb9 · covers:deciding a compaction is due without performing one

## Invariants

- A datom is returned once however many segments hold it.
- Compaction changes no answer.
- The output is published before any input is removed.
- `ShouldCompact` changes nothing.

## Risks

- ⚠ **The ordering alone looks sufficient and is not.** Publishing before removing bounds a crash's damage to a duplicate rather than a gap — but the duplicate is then on disk permanently, so without deduplication every later read double-counts. The falsifier constructs exactly that surviving state.
- ⚠ **A deduplication test with only identical datoms proves the wrong half.** It must also show two datoms that are equal in entity, attribute and value but differ in transaction SURVIVING, or the implementation is free to use a shorter key that merges two facts.
- ⚠ **"Compaction changes no answer" must compare HISTORY, not a query.** A `SELECT` resolves to the latest visible datom, so dropping every superseded fact would leave a query answering identically while the past became unanswerable.
- ⚠ **THE PUBLISH-BEFORE-REMOVE ORDERING IS NOT FALSIFIABLE BY A TEST HERE, and a mutant found that rather than a person.** A mutant that moved the removal ahead of the publication SURVIVED the whole suite — correctly, because absent a crash both orders leave the same directory holding the same facts. The claim was withdrawn from `Rests-on:` rather than propped up with a check that reads the source, and the ordering is held by the code, the record and a human sign-off instead. ★ What the suite DOES prove is the consequence: the state the chosen order can leave is harmless.
- Merging every segment into one is quadratic over a leaf's lifetime. Stated in the parent record; tiering is deferred rather than approximated here.

## Stop Condition

Stop and ask before dropping a superseded datom during a merge, however much
space it would reclaim. It is what compaction means in a store that overwrites,
and here it silently changes the answer to every question about the past —
which is the property the whole system exists for.

## Out of Scope

- Tiering, and merging a subset of segments (deferred: `docs/adr/BACKLOG.md` §15)
- Reclaiming orphaned inputs after an interrupted compaction (deferred: `docs/adr/BACKLOG.md` §15)
- Who calls `ShouldCompact` (deferred: `docs/adr/BACKLOG.md` §15)
- Dropping any fact (permanent: boundary: ADR-010's purge owns removal, with a horizon and an acknowledgement protocol a background merge has none of)

## Verification Log
- 2026-09-04 · 6765feb* · exit 0 · `set -o pipefail …` · acceptance-sha256:e9834d529360fb241ad20e349fdff996d2688e30ea5d6126b0b2ddbdd3831eb9 · ms:4101
- 2026-09-04 · 6765feb* · exit 0 · `set -o pipefail …` · acceptance-sha256:e9834d529360fb241ad20e349fdff996d2688e30ea5d6126b0b2ddbdd3831eb9 · ms:3990
- 2026-09-04 · 6765feb* · exit 0 · `set -o pipefail …` · acceptance-sha256:e9834d529360fb241ad20e349fdff996d2688e30ea5d6126b0b2ddbdd3831eb9 · ms:4095
- 2026-09-04 · 6765feb* · exit 0 · `set -o pipefail …` · acceptance-sha256:e9834d529360fb241ad20e349fdff996d2688e30ea5d6126b0b2ddbdd3831eb9 · ms:4126
- 2026-09-04 · 6765feb* · exit 0 · `set -o pipefail …` · acceptance-sha256:e9834d529360fb241ad20e349fdff996d2688e30ea5d6126b0b2ddbdd3831eb9 · ms:4135
- 2026-09-04 · 6765feb* · exit 0 · `set -o pipefail …` · acceptance-sha256:e9834d529360fb241ad20e349fdff996d2688e30ea5d6126b0b2ddbdd3831eb9 · ms:4130
- 2026-09-04 · 6765feb* · exit 0 · `set -o pipefail …` · acceptance-sha256:e9834d529360fb241ad20e349fdff996d2688e30ea5d6126b0b2ddbdd3831eb9 · ms:4153
- 2026-09-04 · 6765feb* · exit 0 · `set -o pipefail …` · acceptance-sha256:e9834d529360fb241ad20e349fdff996d2688e30ea5d6126b0b2ddbdd3831eb9 · ms:4224
- 2026-09-04 · 6765feb* · exit 0 · `set -o pipefail …` · acceptance-sha256:e9834d529360fb241ad20e349fdff996d2688e30ea5d6126b0b2ddbdd3831eb9 · ms:4015
- 2026-09-04 · 6765feb* · exit 0 · `set -o pipefail …` · acceptance-sha256:e9834d529360fb241ad20e349fdff996d2688e30ea5d6126b0b2ddbdd3831eb9 · ms:4213
- 2026-09-04 · 6765feb* · exit 0 · `set -o pipefail …` · acceptance-sha256:e9834d529360fb241ad20e349fdff996d2688e30ea5d6126b0b2ddbdd3831eb9 · ms:4122
