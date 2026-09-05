# Task ADR-042-T1: Check before you absorb, and leave the clock alone when you refuse

**Depends-on:** none
**Covers:** none — no spec
**Estimated scope:** M
**Owner:** unassigned
**Produces:** `hlc.Bound`, `hlc.Skew`, `hlc.ErrSkewTooLarge`, `hlc.ErrNoBound`, `hlc.Clock.Admit`, `observe.KindClockSkewRefused`
**Consumes:** `hlc.Timestamp`, `hlc.Clock`, `hlc.Clock.Merge` from ADR-002; `observe.Kind` from ADR-012
**Data dependency:** hermetic — the wall reading is injected, which is why every property here is testable
**Proof map:** v1
**Rests-on:** `a refused remote leaving the clock byte-identical`, `skew being measured against the receiver's own reading rather than reported by the sender`, `a bound being required rather than defaulted`, `Merge remaining unbounded so history read from storage stays readable`

## Goal

Put the skew check where it can work — before the absorption that cannot be
undone — and keep it away from the storage path, where refusing would turn a
clock problem into data loss.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `internal/core/hlc/admission.go` | add | `Bound`, `Skew`, `Admit`, and the two sentinels. |
| `internal/core/hlc/hlc.go` | modify | `Merge`'s comment says WHEN it is the right one, now that there are two. |
| `internal/core/hlc/admission_test.go` | add | The tests below. |
| `internal/core/observe/kinds.go` | modify | `KindClockSkewRefused`, with its declared reader and fields. |
| `internal/core/hlc/doc.go` | modify | Why the check precedes the merge, and why storage is exempt. |

## Ordered Steps

1. [S1] Write the failing tests first (TDD red): `TestARefusedRemoteLeavesTheClockUntouched`, `TestSkewIsMeasuredByTheReceiver`, `TestABoundIsRequired`, `TestHistoryFromStorageIsStillMergeable`. Run the Acceptance fence and confirm it is red. ⚠Check each name is SELECTED by the fence's `-run` filter before running any mutant. [proof: acceptance]
2. [S2] Declare `observe.KindClockSkewRefused`. ⚠A refusal nobody can see is a refusal nobody will fix, and the symptom — one node's writes quietly not landing — looks like a network problem from every other angle. [proof: mutation]
3. [S3] Add `Bound` and refuse a non-positive one with `ErrNoBound`. ★The VALUE needs deployment data, so this record requires one and invents none — the same move ADR-040 makes for its grace and ADR-041 for its two constants. [proof: mutation]
4. [S4] Measure skew as the difference between the REMOTE reading and the receiver's OWN wall, in `Skew`. ⚠Never self-reported: a node whose clock is wrong is the node whose self-assessment is wrong. [proof: mutation]
5. [S5] ⚠In `Admit`, check FIRST and merge only if the check passes — and on refusal leave the clock byte-identical. ★The merge is irreversible, so a check afterwards is a report of damage rather than a gate. This is the whole record. [proof: mutation]
6. [S6] Leave `Merge` unbounded, and say in its doc when each is right. ⚠`tx.Minter.Observe` merges timestamps rehydrated from a leaf, and bounding those would make committed data unreadable — the skew already happened, and refusing now punishes the reader. [proof: mutation]

## Acceptance

```bash
set -o pipefail
go test ./internal/core/hlc/... -race -run 'TestARefusedRemoteLeavesTheClockUntouched|TestSkewIsMeasuredByTheReceiver|TestABoundIsRequired|TestHistoryFromStorageIsStillMergeable' -count=1 2>&1 | tee /tmp/adr042-t1a.out \
  && ! grep -qE "no tests to run|matched no packages|^FAIL|^--- FAIL|DATA RACE" /tmp/adr042-t1a.out \
  && go test ./internal/core/hlc/... ./internal/core/tx/... ./internal/core/observe/... -race -count=1 2>&1 | tee /tmp/adr042-t1b.out \
  && ! grep -qE "no tests to run|matched no packages|^FAIL|^--- FAIL|DATA RACE" /tmp/adr042-t1b.out
```

The second command carries `tx` because `Minter.Observe` is the one production
caller of `Merge`, and rule 4 is precisely the claim that it must keep working —
a change that bounded `Merge` would break it, and the suite is where that shows.

## Tests

| Test name | File | Verifies | Covers | Steps |
|-----------|------|----------|--------|-------|
| `TestARefusedRemoteLeavesTheClockUntouched` | `internal/core/hlc/admission_test.go` | **The falsifier ADR-042 names in `Enforced-by:`.** After a refused `Admit`, `Last()` is byte-identical to before — and the next `Now()` follows the ORIGINAL reading rather than the rejected one. ⚠ The assertion is on the CLOCK, not on the error: returning an error while having already merged is the exact defect, and it looks like success from the caller's side | — | S5 |
| `TestSkewIsMeasuredByTheReceiver` | `internal/core/hlc/admission_test.go` | `Skew` is computed from the receiver's injected wall against the remote reading, so two receivers with different clocks measure the same remote differently. ★ That is rule 3's honest limit made visible rather than hidden: this measures disagreement, not error | — | S4 |
| `TestABoundIsRequired` | `internal/core/hlc/admission_test.go` | A zero or negative bound is `ErrNoBound`, and a remote exactly at the bound is accepted while one past it is not — so the boundary is not off by one in the direction that refuses honest peers | — | S3 |
| `TestHistoryFromStorageIsStillMergeable` | `internal/core/hlc/admission_test.go` | `Merge` accepts a timestamp far beyond any bound, and a `tx.Minter` rehydrating such history still advances. ⚠ Asserted explicitly, because "apply the bound everywhere" is the tidier-looking rule and it makes a leaf written by a formerly-skewed node permanently unreadable | — | S6 |

## Reachability

| Rung | How this task shows it |
|------|------------------------|
| 1 — exists | The four tests. |
| 2 — something selects it | `Admit` is the only bounded path and `Merge` the only unbounded one; the doc says which is for what. |
| 3 — the caller can discover it | Two named sentinels, and `Bound` has no usable zero value. |
| 4 — it is used | ⚠ **Nothing calls `Admit`.** There is no transport (`BACKLOG.md` §18), so this decides the rule and the signature the way ADR-033 did for authorization. `Merge` keeps its one production caller, which is rule 4 working rather than a gap. Recorded rather than implied. |

## Mutation Log

- 2026-09-05 · f2ba50c* · mutant killed · exit 1 · `internal/core/hlc/admission.go` · merges FIRST and reports the skew afterwards — which looks identical to a caller, returns the same error, and is the whole defect: monotonicity makes the absorption irreversible, so the skewed reading has already been adopted permanently and the cluster cannot come back · acceptance-sha256:5f24d934a9be084a799075dabbe39a1994192f6b55433b357cd173c84b15b850 · covers:a refused remote leaving the clock byte-identical
- 2026-09-05 · f2ba50c* · mutant killed · exit 1 · `internal/core/hlc/admission.go` · takes the skew from the remote's own timestamp instead of measuring it against the receiver's wall — the suspect testifying, since a node whose clock is wrong is exactly the node whose self-assessment is wrong · acceptance-sha256:5f24d934a9be084a799075dabbe39a1994192f6b55433b357cd173c84b15b850 · covers:skew being measured against the receiver's own reading rather than reported by the sender
- 2026-09-05 · f2ba50c* · mutant killed · exit 1 · `internal/core/hlc/admission.go` · accepts an undeclared bound as though it were a declared zero, so a caller that forgot to configure one refuses every remote even a nanosecond ahead — and "not configured" becomes indistinguishable from "configured to tolerate nothing" · acceptance-sha256:5f24d934a9be084a799075dabbe39a1994192f6b55433b357cd173c84b15b850 · covers:a bound being required rather than defaulted
- 2026-09-05 · f2ba50c* · mutant killed · exit 1 · `internal/core/hlc/hlc.go` · bounds Merge itself, so a timestamp rehydrated from a leaf written by a formerly-skewed node is silently ignored — the tidier one-rule version, and it makes committed data unreadable by turning a clock problem into data loss over skew that already happened · acceptance-sha256:5f24d934a9be084a799075dabbe39a1994192f6b55433b357cd173c84b15b850 · covers:Merge remaining unbounded so history read from storage stays readable

## Invariants

- A refused remote leaves the clock byte-identical.
- Skew is the receiver's measurement, never the sender's claim.
- `Merge` stays unbounded, so stored history stays readable.
- A bound is declared, never defaulted.

## Risks

- ⚠ **The falsifier must assert the CLOCK, not the error.** Checking after merging and returning an error looks identical to a caller, and it is the defect — the damage is the absorption. `Last()` before and after, plus the next `Now()`.
- ⚠ **"Apply the bound everywhere" is the tidier rule and it is the dangerous one.** It makes a leaf written by a formerly-skewed node unreadable, which converts a clock problem into data loss. The storage path is tested explicitly so removing the exemption fails.
- ⚠ **A boundary off by one in the refusing direction rejects honest peers.** A remote exactly AT the bound is accepted; only past it is refused.
- ⚠ **Rule 3's limit must be visible in a test rather than only in prose.** Two receivers measuring one remote differently is what makes "this measures disagreement, not error" a fact a reader can see.
- Nothing calls `Admit`, so this adds a rule and not an enforcement point. Recorded on the parent record.

## Stop Condition

Stop and ask before bounding `Merge` itself. It is the path `tx.Minter` uses to
rehydrate history from a leaf, and refusing there makes committed data unreadable
— a clock problem turned into data loss.

## Out of Scope

- Choosing the bound value (permanent: boundary: a datacentre and a WAN tolerate different skew)
- Calling `Admit` anywhere (deferred: `docs/adr/BACKLOG.md` §18)
- Making a persistently skewed node an obligation (deferred: `docs/adr/BACKLOG.md` §4)
- Keeping clocks correct (permanent: boundary: an operational concern outside the code)

## Verification Log
- 2026-09-05 · f2ba50c* · exit 0 · `set -o pipefail …` · acceptance-sha256:5f24d934a9be084a799075dabbe39a1994192f6b55433b357cd173c84b15b850 · ms:3886
- 2026-09-05 · f2ba50c* · exit 0 · `set -o pipefail …` · acceptance-sha256:5f24d934a9be084a799075dabbe39a1994192f6b55433b357cd173c84b15b850 · ms:3912
- 2026-09-05 · f2ba50c* · exit 0 · `set -o pipefail …` · acceptance-sha256:5f24d934a9be084a799075dabbe39a1994192f6b55433b357cd173c84b15b850 · ms:3869
- 2026-09-05 · f2ba50c* · exit 0 · `set -o pipefail …` · acceptance-sha256:5f24d934a9be084a799075dabbe39a1994192f6b55433b357cd173c84b15b850 · ms:3820
- 2026-09-05 · f2ba50c* · exit 0 · `set -o pipefail …` · acceptance-sha256:5f24d934a9be084a799075dabbe39a1994192f6b55433b357cd173c84b15b850 · ms:3985
