# Task ADR-023-T1: A typed reference, and a walk that resolves at one instant

**Depends-on:** none
**Covers:** none — no spec
**Estimated scope:** M (multi-file)
**Owner:** unassigned
**Produces:** `link.Kind`, `link.KindLiteral`, `link.KindReference`, `link.Value`, `link.Literal`, `link.Ref`, `link.Resolver`, `link.Walk`, `link.Path`, `link.ErrDepthRequired`, `link.ErrCycle`
**Consumes:** `temporal.Query` from ADR-002; `ports.Datom` from ADR-003
**Data dependency:** hermetic
**Proof map:** v1
**Rests-on:** `every hop of a walk resolving at one instant`, `a reference being a stored kind rather than inferred from bytes`, `a cycle reported rather than truncated`, `a walk refusing an unbounded depth`, `a missing, retracted and erased target being one answer`

## Goal

Make a reference a thing the store knows about, and make walking references at a
past instant give an answer that was actually true then.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `internal/core/link/doc.go` | add | Why a link is a datom, and why the same-instant rule is the whole record. |
| `internal/core/link/value.go` | add | `Kind`, `Value`, `Literal`, `Ref`. |
| `internal/core/link/walk.go` | add | `Resolver`, `Walk`, `Path`, `ErrDepthRequired`, `ErrCycle`. |
| `internal/core/link/link_test.go` | add | The tests below. |

★ `Walk` takes a `Resolver` rather than reading anything. That is what lets the
same-instant rule be proved against a fixture whose shape CHANGES between
instants — which is the only way to catch the defect at all.

## Ordered Steps

1. [S1] Write the failing tests first (TDD red): `TestEveryHopResolvesAtOneInstant`, `TestAReferenceIsAStoredKindNotAGuess`, `TestACycleIsReportedNotTruncated`, `TestAWalkRefusesAnUnboundedDepth`, `TestAMissingRetractedAndErasedTargetAreOneAnswer`, `TestWalkRespectsItsDepthBound`. Run the Acceptance fence and confirm it is red. ⚠Check each name is SELECTED by the fence's `-run` filter before running any mutant. [proof: acceptance]
2. [S2] Define `Kind` as a closed pair — literal or reference — and `Value` as bytes plus a kind. ★Stored, never inferred: guessing from the shape of the bytes turns every string resembling an identifier into an edge, and the guess changes when unrelated data does. [proof: mutation]
3. [S3] Define `Resolver` as one method taking an entity AND a snapshot, so a caller structurally cannot resolve a hop without saying when. [proof: mutation]
4. [S4] Implement `Walk` to take one snapshot and pass THAT snapshot to every hop. ⚠The record's whole point: a walk that resolves hop *n+1* at a fresh instant assembles a tree out of parts that each existed and that as a whole never did. [proof: mutation]
5. [S5] Refuse a non-positive depth with `ErrDepthRequired`, rather than defaulting to unbounded. [proof: mutation]
6. [S6] Detect a revisited entity and return `ErrCycle` naming it, rather than stopping quietly. ⚠A truncated answer looks exactly like a complete one. [proof: mutation]
7. [S7] Treat an unresolvable reference as an ordinary absence — no error, no marker, nothing that distinguishes retracted from never-existed from ERASED. ⚠Distinguishing them rebuilds the existence oracle ADR-007 removed. [proof: mutation]

## Acceptance

```bash
set -o pipefail
go test ./internal/core/link/... -race -run 'TestEveryHopResolvesAtOneInstant|TestAReferenceIsAStoredKind|TestACycleIsReported|TestAWalkRefusesAnUnboundedDepth|TestAMissingRetractedAndErased|TestWalkRespectsItsDepthBound' -count=1 2>&1 | tee /tmp/adr023-t1a.out \
  && ! grep -qE "no tests to run|matched no packages|^FAIL|^--- FAIL|DATA RACE" /tmp/adr023-t1a.out \
  && go test ./internal/core/link/... ./internal/core/temporal/... ./internal/core/ports/... -race -count=1 2>&1 | tee /tmp/adr023-t1b.out \
  && ! grep -qE "no tests to run|matched no packages|^FAIL|^--- FAIL|DATA RACE" /tmp/adr023-t1b.out
```

## Tests

| Test name | File | Verifies | Covers | Steps |
|-----------|------|----------|--------|-------|
| `TestEveryHopResolvesAtOneInstant` | `internal/core/link/link_test.go` | Against a fixture whose graph SHAPE differs between two instants, a walk's answer matches one instant exactly and is never a mixture — **the falsifier ADR-023 names in `Enforced-by:`**. Also records every snapshot the resolver was asked with and asserts they are all identical | — | S3, S4 |
| `TestAReferenceIsAStoredKindNotAGuess` | `internal/core/link/link_test.go` | A literal whose bytes spell an existing entity name is NOT followed, so edges come from the stored kind rather than from what the value looks like | — | S2 |
| `TestACycleIsReportedNotTruncated` | `internal/core/link/link_test.go` | A loop returns `ErrCycle` naming the repeated entity, rather than a partial path that reads as complete | — | S6 |
| `TestAWalkRefusesAnUnboundedDepth` | `internal/core/link/link_test.go` | Depth zero and negative are `ErrDepthRequired`, so an unbounded walk cannot be asked for by omission | — | S5 |
| `TestAMissingRetractedAndErasedTargetAreOneAnswer` | `internal/core/link/link_test.go` | All three unresolvable cases produce byte-identical results, so a traversal cannot be used to ask whether a subject was erased | — | S7 |
| `TestWalkRespectsItsDepthBound` | `internal/core/link/link_test.go` | A chain longer than the bound is cut at the bound, and what is returned is the prefix nearest the root | — | S5 |

## Reachability

| Rung | How this task shows it |
|------|------------------------|
| 1 — exists | The six tests above. |
| 2 — something selects it | `Walk` is the only way a reference becomes a reachable set, and `Ref` the only way a value becomes one. |
| 3 — the caller can discover it | Both refusals are named errors, so the depth requirement and the cycle rule are learnable from the API. |
| 4 — it is used | Nothing writes a link through the language yet; T2 is `pending` on §29. |

## Mutation Log

- 2026-09-04 · e0950fa* · mutant killed · exit 1 · `internal/core/link/walk.go` · resolves hops past the first at a fresh instant instead of the snapshot the caller gave, which is what every naive traversal does because each hop is a read and a read defaults to now. The answer is a tree assembled from two instants: every node in it is real, every edge existed at some point, and the shape never existed at any moment — and nothing about it looks wrong · acceptance-sha256:4deac449ec1a404e3ff52dcb396e53143da13902ba7f8887d1db2be90ea360dc · covers:every hop of a walk resolving at one instant
- 2026-09-04 · e0950fa* · mutant killed · exit 1 · `internal/core/link/value.go` · treats every value as a reference regardless of its stored kind, which is what inferring from the bytes amounts to: any string that happens to spell an entity name becomes an edge, and the graph changes whenever unrelated data does · acceptance-sha256:4deac449ec1a404e3ff52dcb396e53143da13902ba7f8887d1db2be90ea360dc · covers:a reference being a stored kind rather than inferred from bytes
- 2026-09-04 · e0950fa* · mutant killed · exit 1 · `internal/core/link/walk.go` · skips an already-seen entity instead of reporting the loop, so the walk terminates and returns a partial path that reads exactly like a complete one. The caller has no way to tell that the graph doubled back · acceptance-sha256:4deac449ec1a404e3ff52dcb396e53143da13902ba7f8887d1db2be90ea360dc · covers:a cycle reported rather than truncated
- 2026-09-04 · e0950fa* · mutant killed · exit 1 · `internal/core/link/walk.go` · treats a missing depth as unbounded instead of refusing it, which reads as a helpful default. A caller who omits the bound then walks a graph they do not control to its full extent, which is the scan the bound exists to prevent · acceptance-sha256:4deac449ec1a404e3ff52dcb396e53143da13902ba7f8887d1db2be90ea360dc · covers:a walk refusing an unbounded depth
- 2026-09-04 · e0950fa* · mutant killed · exit 1 · `internal/core/link/walk.go` · distinguishes an erased target from an ordinary absence, which is the kinder-seeming diagnostic and is an existence oracle: a caller can then discover whether a subject was erased simply by walking to it, which is the property crypto-shredding spent a whole record removing · acceptance-sha256:4deac449ec1a404e3ff52dcb396e53143da13902ba7f8887d1db2be90ea360dc · covers:a missing, retracted and erased target being one answer

## Invariants

- Every hop of one walk uses one snapshot.
- A reference is a stored kind, never inferred.
- A cycle is named; a depth is required.
- Missing, retracted and erased targets are one answer.

## Risks

- ⚠ **A same-instant test on a STATIC graph proves nothing**, because every instant gives the same answer. The fixture's shape differs between the two instants, so a per-hop resolution produces a mixture that matches neither.
- ⚠ **And asserting only the final answer is weaker than it looks.** The test also records every snapshot the resolver was handed and asserts they are all identical — an implementation could reach the right answer on this fixture by luck.
- ⚠ **"A dangling reference is fine" is easy to satisfy while still leaking.** The three cases must be BYTE-IDENTICAL; a distinguishable marker, count or ordering for the erased case restores the oracle.
- ⚠ **A cycle test on a self-loop is the easy case.** The fixture uses a longer loop, because a walk that only checks "did I just come from here" passes the self-loop and hangs on the real one.
- The walk reads through a `Resolver` and stores nothing, so nothing here proves a real storage layer will pass one snapshot rather than taking a fresh read per hop. The record carries that as a follow-up.

## Stop Condition

Stop and ask before letting any hop of a traversal take its own instant, however
much simpler it makes the fetch. The tree it produces never existed, every part
of it is real, and nothing about the answer looks wrong.

## Out of Scope

- Writing a link in the language (deferred: `docs/adr/BACKLOG.md` §29)
- Inbound edges — "what points at this" (deferred: `docs/adr/BACKLOG.md` §29)
- Durability (deferred: `docs/adr/BACKLOG.md` §12)

## Verification Log
- 2026-09-04 · e0950fa* · exit 0 · `set -o pipefail …` · acceptance-sha256:4deac449ec1a404e3ff52dcb396e53143da13902ba7f8887d1db2be90ea360dc · ms:3352
- 2026-09-04 · e0950fa* · exit 0 · `set -o pipefail …` · acceptance-sha256:4deac449ec1a404e3ff52dcb396e53143da13902ba7f8887d1db2be90ea360dc · ms:3304
- 2026-09-04 · e0950fa* · exit 0 · `set -o pipefail …` · acceptance-sha256:4deac449ec1a404e3ff52dcb396e53143da13902ba7f8887d1db2be90ea360dc · ms:3465
- 2026-09-04 · e0950fa* · exit 0 · `set -o pipefail …` · acceptance-sha256:4deac449ec1a404e3ff52dcb396e53143da13902ba7f8887d1db2be90ea360dc · ms:3467
- 2026-09-04 · e0950fa* · exit 0 · `set -o pipefail …` · acceptance-sha256:4deac449ec1a404e3ff52dcb396e53143da13902ba7f8887d1db2be90ea360dc · ms:3484
- 2026-09-04 · e0950fa* · exit 0 · `set -o pipefail …` · acceptance-sha256:4deac449ec1a404e3ff52dcb396e53143da13902ba7f8887d1db2be90ea360dc · ms:3449
- 2026-09-04 · 09ec963* · exit 0 · `set -o pipefail …` · acceptance-sha256:4deac449ec1a404e3ff52dcb396e53143da13902ba7f8887d1db2be90ea360dc · ms:3610
