# Task ADR-017-T2: Bound a read by a transaction identifier, and prove no reader takes a lock

**Depends-on:** T1
**Covers:** none — no spec
**Estimated scope:** M (multi-file)
**Owner:** unassigned
**Produces:** `tail.Snapshot`, `tail.Tail.Snapshot`, `tail.Tail.Read`
**Consumes:** `tail.Tail`, `tail.Watermark`, `tail.Entry` (T1), `tx.TxID` and `tx.TxID.Compare` from ADR-002
**Data dependency:** hermetic
**Proof map:** v1
**Rests-on:** `readers never acquiring the writer's mutex`, `a read being bounded by the transaction identifier it asked for`

## Goal

Give a reader a snapshot — a published position paired with the transaction
point it asked to see — and make "no reader takes a lock" a checked property
rather than a claim in a document.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `internal/core/tail/snapshot.go` | add | `Snapshot`, `Tail.Snapshot`, `Tail.Read`. |
| `internal/core/tail/guard_test.go` | add | The source-level guard and its positive control. |
| `internal/core/tail/snapshot_test.go` | add | The visibility tests below. |

★ The guard is a test that reads this package's own SOURCE. That is deliberate:
"no reader takes a lock" is a property of the code's shape, and no behavioural
test can observe it — a reader that locked would still return correct answers,
just slowly and while blocking a writer. The failure this record exists to
prevent is invisible to every test that only checks results.

## Ordered Steps

1. [S1] Write the failing tests first (TDD red): `TestReadersTakeNoLock`, `TestGuardFlagsAKnownOffender`, `TestSnapshotExcludesLaterTransactions`, `TestReadIsBoundedByTheSnapshot`. Run the Acceptance fence and confirm it is red. ⚠Check each name is SELECTED by the fence's `-run` filter before running any mutant. [proof: acceptance]
2. [S2] Define `Snapshot`: the `Watermark` a reader loaded, and the `tx.TxID` bound it asked for. ★The watermark says what has been PUBLISHED; the identifier says what the reader asked to SEE. A snapshot is the pair, and conflating them would make a read's meaning depend on when it happened to run.
3. [S3] Implement `Tail.Snapshot`: one acquire-load of the watermark, paired with the caller's bound. Nothing else, and in particular no lock.
4. [S4] Implement `Tail.Read`: walk entries below the snapshot's watermark, yielding those whose `tx.TxID` does not exceed the snapshot's bound. ★Two independent limits: the watermark excludes what is not yet published, the identifier excludes what the reader did not ask for. A concurrent write fails both.
5. [S5] Implement the guard: scan this package's non-test source for any lock, wait or reference-count operation reachable from a read-path function, and fail naming the offender. [proof: mutation]
6. [S6] Give the guard a POSITIVE CONTROL — a fixture the guard must flag — so a guard that has stopped looking is distinguishable from a package that is clean. ★Without it, the guard passes identically whether it is working or broken, which is the one failure mode a negative assertion always has.

## Acceptance

```bash
set -o pipefail
go test ./internal/core/tail/... -race -run 'TestReadersTakeNoLock|TestGuardFlags|TestSnapshotExcludes|TestReadIsBounded' -count=1 2>&1 | tee /tmp/adr017-t2a.out \
  && ! grep -qE "no tests to run|matched no packages|^FAIL|^--- FAIL|DATA RACE" /tmp/adr017-t2a.out \
  && go test ./internal/core/tail/... ./internal/core/tx/... ./internal/core/temporal/... -race -count=1 2>&1 | tee /tmp/adr017-t2b.out \
  && ! grep -qE "no tests to run|matched no packages|^FAIL|^--- FAIL|DATA RACE" /tmp/adr017-t2b.out
```

The first command is this task's own work and can carry the verdict alone; the
second is the regression half over the packages this task consumes from, and
cannot stand in for it because it does not name the new unit.

## Tests

| Test name | File | Verifies | Covers | Steps |
|-----------|------|----------|--------|-------|
| `TestReadersTakeNoLock` | `internal/core/tail/guard_test.go` | No read-path function in this package acquires a lock, waits, or takes a reference count. **The falsifier ADR-017 names in `Enforced-by:`** | — | S5 |
| `TestGuardFlagsAKnownOffender` | `internal/core/tail/guard_test.go` | The guard flags a fixture that DOES lock on a read path, so a guard that has stopped looking is distinguishable from a package that is clean | — | S6 |
| `TestSnapshotExcludesLaterTransactions` | `internal/core/tail/snapshot_test.go` | An entry appended at a transaction identifier above the snapshot's bound is not returned, even when it is published — the two limits are independent | — | S2, S4 |
| `TestReadIsBoundedByTheSnapshot` | `internal/core/tail/snapshot_test.go` | A read returns exactly the entries below the snapshot's watermark and within its bound, and appends made after the snapshot are absent however long the read runs | — | S3, S4 |

## Reachability

| Rung | How this task shows it |
|------|------------------------|
| 1 — exists | The four tests above. |
| 2 — something selects it | `Tail.Snapshot` is the only way to obtain one and `Tail.Read` the only way to spend one; the guard runs on every fence, so the no-lock rule is checked rather than remembered. |
| 3 — the caller can discover it | `Read` takes a `Snapshot` and nothing else, so its signature states that a read is bounded and that the bound is the caller's to choose. |
| 4 — it is used | Nothing measures this yet; no storage engine exists. |

## Mutation Log

- 2026-09-04 · ed35910* · mutant killed · exit 1 · `internal/core/tail/tail.go` · makes the read path take the writer mutex, which is the exact regression this record forbids and which no behavioural test can see: the answers stay correct while every reader now blocks the writer · acceptance-sha256:ca83d95ee365cddeb5ebbe2413dfdd30dc9be5a1e48695cd66af55ba50691445 · covers:readers never acquiring the writer's mutex
- 2026-09-04 · ed35910* · mutant killed · exit 1 · `internal/core/tail/snapshot.go` · drops the transaction bound so a read returns everything published rather than what the reader asked to see, making the answer depend on when the read happened to run · acceptance-sha256:ca83d95ee365cddeb5ebbe2413dfdd30dc9be5a1e48695cd66af55ba50691445 · covers:a read being bounded by the transaction identifier it asked for

## Invariants

- A reader acquires no lock, waits on nothing, and holds no reference count.
- A snapshot is loaded once and never refreshed mid-read.
- Two independent limits apply to every read: the published watermark and the transaction bound.
- The guard has a positive control, so it cannot pass by having stopped looking.

## Risks

- ⚠ **A negative assertion over a clean package is unfalsifiable on its own.** `TestReadersTakeNoLock` passes when the package is clean AND when the guard is broken, and those look identical. `TestGuardFlagsAKnownOffender` is the control that separates them, and it is the reason the guard is trustworthy at all.
- The guard reads source rather than behaviour, so it can be fooled by a lock reached through an indirection it does not follow. It is a floor, not a proof: it catches the direct case, which is the one that actually happens, and the record's Follow-ups carry the wider obligation.
- A guard scanning source can silently match nothing if the package layout changes — the same class as a fence whose filter selects no tests. The positive control fails in that case too, because it also stops being found.

## Stop Condition

Stop and ask if a read path genuinely needs to block. Adding a lock "just here"
is how this guarantee is lost, and the record says so: it is a decision to
revisit rather than an exception to grant.

## Out of Scope

- Applying the no-lock rule beyond this package (deferred: ADR-017 Follow-ups; the guard covers `internal/core/tail` and the rule is meant to cover every reader)
- Bitemporal visibility of individual datoms (permanent: boundary: ADR-002 owns `temporal.Visible` and this task bounds a read by transaction identifier, which is a different and coarser limit)
- Any index over the tail (deferred: `docs/adr/BACKLOG.md` §15)

## Verification Log
- 2026-09-04 · ed35910* · exit 0 · `set -o pipefail …` · acceptance-sha256:ca83d95ee365cddeb5ebbe2413dfdd30dc9be5a1e48695cd66af55ba50691445 · ms:4109
- 2026-09-04 · ed35910* · exit 0 · `set -o pipefail …` · acceptance-sha256:ca83d95ee365cddeb5ebbe2413dfdd30dc9be5a1e48695cd66af55ba50691445 · ms:3963
- 2026-09-04 · ed35910* · exit 0 · `set -o pipefail …` · acceptance-sha256:ca83d95ee365cddeb5ebbe2413dfdd30dc9be5a1e48695cd66af55ba50691445 · ms:3922
- 2026-09-04 · 09ec963* · exit 0 · `set -o pipefail …` · acceptance-sha256:ca83d95ee365cddeb5ebbe2413dfdd30dc9be5a1e48695cd66af55ba50691445 · ms:4465
