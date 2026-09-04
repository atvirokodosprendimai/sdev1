# Task ADR-020-T1: The commit condition, counted over distinct failure domains

**Depends-on:** none
**Covers:** none — no spec
**Estimated scope:** M (multi-file)
**Owner:** unassigned
**Produces:** `commit.Ack`, `commit.Condition`, `commit.NewCondition`, `commit.Satisfied`, `commit.ErrBelowFloor`, `commit.ErrOneDomain`, `commit.ErrStaleEpoch`
**Consumes:** `durability.Policy` from ADR-004, `lease.Epoch` from ADR-009
**Data dependency:** hermetic
**Proof map:** v1
**Rests-on:** `acknowledgements being counted over distinct failure domains`, `an acknowledgement under a superseded epoch not counting`, `a shortfall being refused rather than acknowledged`

## Goal

Decide whether a set of acknowledgements commits a write, and refuse the ones
that look sufficient and are not.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `internal/core/commit/doc.go` | add | Package comment: what a memory commit protects against and what it does not, and why the domain level differs from the disk tier's. |
| `internal/core/commit/commit.go` | add | `Ack`, `Condition`, `Satisfied`, and the three sentinels. |
| `internal/core/commit/commit_test.go` | add | The tests below, including the falsifier named in ADR-020's `Enforced-by:`. |

★ Three sentinels, not one, because the three failures need different operator
responses: below the floor means restore capacity, one domain means fix
placement, and a stale epoch means the writer lost the leaf and should stop.

## Ordered Steps

1. [S1] Write the failing tests first (TDD red): `TestReplicasInOneDomainDoNotCommit`, `TestDistinctDomainsCommit`, `TestStaleEpochAcknowledgementsDoNotCount`, `TestShortfallIsRefusedNotDowngraded`, `TestConditionNamesWhyItFailed`. Run the Acceptance fence and confirm it is red. ⚠Check each name is SELECTED by the fence's `-run` filter before running any mutant. [proof: acceptance]
2. [S2] Define `Ack` as a node, the failure domain it sits in, and the epoch it acknowledged under.
3. [S3] Define `Condition` from a `durability.Policy` and the domain level a memory commit is judged at. ★The level is explicit rather than defaulted, because for unflushed memory the failure is POWER and for a sealed segment it is a machine — and a rack is not a power boundary.
4. [S4] Implement `Satisfied`: count DISTINCT domains, not acknowledgements. [proof: mutation]
5. [S5] Discard acknowledgements made under an epoch below the current one, with `ErrStaleEpoch`. ★A replica acknowledging to a fenced-out writer is acknowledging to nobody, and counting it would let a superseded writer believe it committed.
6. [S6] Refuse a shortfall with `ErrBelowFloor` rather than reporting partial success. ★"Acknowledged with a warning" is how a cluster ends up holding data at a durability nobody chose, and the warning is read by nobody at the moment it matters.
7. [S7] Name the three failures separately, so an operator learns which of restore-capacity, fix-placement or stop-writing applies. [proof: human: a reader confirms the three errors say what to DO, not merely that the commit failed]

## Acceptance

```bash
set -o pipefail
go test ./internal/core/commit/... -race -run 'TestReplicasInOneDomain|TestDistinctDomainsCommit|TestStaleEpochAcknowledgements|TestShortfallIsRefused|TestConditionNamesWhy' -count=1 2>&1 | tee /tmp/adr020-t1a.out \
  && ! grep -qE "no tests to run|matched no packages|^FAIL|^--- FAIL|DATA RACE" /tmp/adr020-t1a.out \
  && go test ./internal/core/commit/... ./internal/core/durability/... ./internal/core/lease/... -race -count=1 2>&1 | tee /tmp/adr020-t1b.out \
  && ! grep -qE "no tests to run|matched no packages|^FAIL|^--- FAIL|DATA RACE" /tmp/adr020-t1b.out
```

## Tests

| Test name | File | Verifies | Covers | Steps |
|-----------|------|----------|--------|-------|
| `TestReplicasInOneDomainDoNotCommit` | `internal/core/commit/commit_test.go` | Enough acknowledgements from ONE failure domain do not commit — the case that looks like durability and is one failure away from nothing. **The falsifier ADR-020 names in `Enforced-by:`** | — | S4 |
| `TestDistinctDomainsCommit` | `internal/core/commit/commit_test.go` | The same count spread across distinct domains DOES commit, so the refusal above is about the domains rather than about a condition that never passes | — | S3, S4 |
| `TestStaleEpochAcknowledgementsDoNotCount` | `internal/core/commit/commit_test.go` | An acknowledgement made under a superseded epoch is discarded, so a fenced-out writer cannot reach a commit on replies meant for it | — | S5 |
| `TestShortfallIsRefusedNotDowngraded` | `internal/core/commit/commit_test.go` | One domain short yields `ErrBelowFloor` and no partial success of any kind, since an acknowledgement with a warning is how data ends up at a durability nobody chose | — | S6 |
| `TestConditionNamesWhyItFailed` | `internal/core/commit/commit_test.go` | The three failures are distinguishable, because restore-capacity, fix-placement and stop-writing are three different operator actions | — | S2, S7 |

## Reachability

| Rung | How this task shows it |
|------|------------------------|
| 1 — exists | The five tests above. |
| 2 — something selects it | `Satisfied` is the only way a set of acknowledgements becomes a verdict, and T2's gate calls exactly it before advancing a watermark. |
| 3 — the caller can discover it | Three named sentinels; a caller handling them separately is handling three different operator situations. |
| 4 — it is used | Nothing replicates yet; the condition is decidable and its satisfaction is not. |

## Mutation Log

- 2026-09-04 · e226454* · mutant killed · exit 1 · `internal/core/commit/commit.go` · counts acknowledgements instead of distinct domains, so three replies from three processes on one power feed commit a write — one failure domain wearing three names, reading as triple durability right up until the feed drops · acceptance-sha256:06a94407025d6a9c7afcaffb614513f52a2d8cc4404cc6dcb03896076483c848 · covers:acknowledgements being counted over distinct failure domains
- 2026-09-04 · e226454* · mutant killed · exit 1 · `internal/core/commit/commit.go` · counts replies made to a writer that has since lost the leaf, so a fenced-out writer reaches a commit on acknowledgements meant for a lease it no longer holds · acceptance-sha256:06a94407025d6a9c7afcaffb614513f52a2d8cc4404cc6dcb03896076483c848 · covers:an acknowledgement under a superseded epoch not counting
- 2026-09-04 · e226454* · mutant killed · exit 1 · `internal/core/commit/commit.go` · reports a shortfall as a successful commit, which is the acknowledge-with-a-warning behaviour that leaves a cluster holding data at a durability nobody chose and nobody was told about · acceptance-sha256:06a94407025d6a9c7afcaffb614513f52a2d8cc4404cc6dcb03896076483c848 · covers:a shortfall being refused rather than acknowledged

## Invariants

- Distinct failure domains are counted, never acknowledgements.
- An acknowledgement below the current epoch is discarded.
- A shortfall is refused; there is no partial success.
- The domain level is explicit and never defaulted.

## Risks

- ⚠ **A test that commits with three acknowledgements from three nodes proves nothing unless the nodes are in different domains.** The falsifier puts them in ONE domain, which is the case that reads as triple durability and is one failure from nothing; the companion test spreads the same count and asserts it does commit, so the refusal is about domains rather than about a condition that never passes.
- Three sentinels are easy to collapse into one during a refactor, and the collapse loses the operator's next action rather than any behaviour. The test asserts they are distinguishable.

## Stop Condition

Stop and ask before adding a way to acknowledge with a warning, or a "best
achieved" result. Both are what a caller will want during a degradation, and both
are how a cluster ends up holding data at a durability nobody chose.

## Out of Scope

- The gate that advances a watermark — that is T2.
- Actually replicating anything (deferred: `docs/adr/BACKLOG.md` §18)
- How many copies are wanted (permanent: boundary: ADR-004 owns the policy and the floor; this task says what each copy must have DONE)

## Verification Log
- 2026-09-04 · e226454* · exit 0 · `set -o pipefail …` · acceptance-sha256:06a94407025d6a9c7afcaffb614513f52a2d8cc4404cc6dcb03896076483c848 · ms:3384
- 2026-09-04 · e226454* · exit 0 · `set -o pipefail …` · acceptance-sha256:06a94407025d6a9c7afcaffb614513f52a2d8cc4404cc6dcb03896076483c848 · ms:3319
- 2026-09-04 · e226454* · exit 0 · `set -o pipefail …` · acceptance-sha256:06a94407025d6a9c7afcaffb614513f52a2d8cc4404cc6dcb03896076483c848 · ms:3393
- 2026-09-04 · e226454* · exit 0 · `set -o pipefail …` · acceptance-sha256:06a94407025d6a9c7afcaffb614513f52a2d8cc4404cc6dcb03896076483c848 · ms:3360
- 2026-09-04 · e226454* · exit 0 · `set -o pipefail …` · acceptance-sha256:06a94407025d6a9c7afcaffb614513f52a2d8cc4404cc6dcb03896076483c848 · ms:3460
- 2026-09-04 · 09ec963* · exit 0 · `set -o pipefail …` · acceptance-sha256:06a94407025d6a9c7afcaffb614513f52a2d8cc4404cc6dcb03896076483c848 · ms:3497
