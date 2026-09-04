# Task ADR-020-T2: Make the watermark advance at the commit point, and nowhere else

**Depends-on:** T1
**Covers:** none — no spec
**Estimated scope:** M (multi-file)
**Owner:** unassigned
**Produces:** `commit.Gate`, `commit.NewGate`, `commit.Gate.Write`, `commit.Gate.Acknowledge`, `commit.Gate.Committed`, `commit.Gate.Pending`, `commit.Gate.Why`
**Consumes:** `commit.Ack`, `commit.Condition`, `commit.Satisfied` (T1), `tail.Tail` and `tail.Watermark` from ADR-017, `lease.Epoch` from ADR-009
**Data dependency:** hermetic
**Proof map:** v1
**Rests-on:** `an uncommitted entry being unreachable rather than merely unmarked`, `the watermark advancing only when the condition is met`, `there being exactly one definition of committed`

## Goal

Make "committed" and "visible" the same event, so a reader cannot see a write
that has not committed and a writer cannot believe one that a reader cannot see.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `internal/core/commit/gate.go` | add | `Gate`, holding acknowledgements and advancing a tail's watermark when the condition is met. |
| `internal/core/commit/gate_test.go` | add | The tests below. |

★ The gate does not add a visibility mechanism. ADR-017's watermark already makes
an unpublished entry UNREACHABLE rather than half-visible, so this task only
decides WHEN it advances — and that makes the watermark the commit point rather
than a second thing beside it.

## Ordered Steps

1. [S1] Write the failing tests first (TDD red): `TestUncommittedEntryIsUnreachable`, `TestWatermarkAdvancesOnlyOnCommit`, `TestOneDefinitionOfCommitted`, `TestPendingEntriesAreCountable`, `TestLateAcknowledgementStillCommits`. Run the Acceptance fence and confirm it is red. ⚠Check each name is SELECTED by the fence's `-run` filter before running any mutant. [proof: acceptance]
2. [S2] Define `Gate` over a tail and a `Condition`, holding acknowledgements per pending entry.
3. [S3] Implement `Acknowledge`: record one replica's acknowledgement and re-evaluate the condition for that entry.
4. [S4] Advance the tail's watermark only when `Satisfied` returns nil for that entry, and publish entries IN ORDER. ★An entry whose own condition is met stays pending while an earlier one has not committed, because a watermark's meaning is a stable PREFIX — publishing past a gap would let a reader see a later write without an earlier one and still call it a prefix. One acknowledgement therefore commits the entry it satisfied plus every later entry already waiting behind it. [proof: mutation]
5. [S5] Make an uncommitted entry unreachable through the tail's own reader, so "not committed" and "not visible" are the same state rather than two that can disagree.
6. [S6] Implement `Pending`, counting entries written and not yet committed. ★It is the exposure window an operator needs during a degradation — how much has been acknowledged to nobody — and nothing else reports it.
7. [S7] Accept a late acknowledgement that arrives after the condition was already met, without double-counting or moving anything backwards.

## Acceptance

```bash
set -o pipefail
go test ./internal/core/commit/... -race -run 'TestUncommittedEntryIsUnreachable|TestWatermarkAdvancesOnlyOnCommit|TestOneDefinitionOfCommitted|TestPendingEntriesAreCountable|TestLateAcknowledgement' -count=1 2>&1 | tee /tmp/adr020-t2a.out \
  && ! grep -qE "no tests to run|matched no packages|^FAIL|^--- FAIL|DATA RACE" /tmp/adr020-t2a.out \
  && go test ./internal/core/commit/... ./internal/core/tail/... -race -count=1 2>&1 | tee /tmp/adr020-t2b.out \
  && ! grep -qE "no tests to run|matched no packages|^FAIL|^--- FAIL|DATA RACE" /tmp/adr020-t2b.out
```

## Tests

| Test name | File | Verifies | Covers | Steps |
|-----------|------|----------|--------|-------|
| `TestUncommittedEntryIsUnreachable` | `internal/core/commit/gate_test.go` | An entry written but not yet committed is invisible through the tail's reader — not merely flagged, but absent, because the watermark is what makes anything reachable | — | S2, S5 |
| `TestWatermarkAdvancesOnlyOnCommit` | `internal/core/commit/gate_test.go` | The watermark moves on the acknowledgement that satisfies the condition and on no earlier one, so partial replication is never visible; and a fully acknowledged LATER entry does not publish while an earlier one is pending, because the watermark names a prefix | — | S3, S4 |
| `TestOneDefinitionOfCommitted` | `internal/core/commit/gate_test.go` | `Committed` and the tail's watermark agree in every state — two definitions of committed would drift, and the one a reader uses would not be the one the writer waited for | — | S4, S5 |
| `TestPendingEntriesAreCountable` | `internal/core/commit/gate_test.go` | The count of written-but-uncommitted entries is readable, which is the exposure window an operator needs during a degradation | — | S6 |
| `TestLateAcknowledgementStillCommits` | `internal/core/commit/gate_test.go` | An acknowledgement arriving after the condition was met neither double-counts nor moves the watermark backwards | — | S7 |

## Reachability

| Rung | How this task shows it |
|------|------------------------|
| 1 — exists | The five tests above. |
| 2 — something selects it | `Acknowledge` is the only path from a replica's reply to a visible entry, and the watermark is the only thing that makes an entry readable; removing the condition check breaks two tests. |
| 3 — the caller can discover it | `Pending` and `Committed` are the two questions a writer and an operator ask, and both are answered from the same state. |
| 4 — it is used | `Pending` is a real measurement from the first write. |

## Mutation Log

- 2026-09-04 · e226454* · mutant killed · exit 1 · `internal/core/commit/gate.go` · publishes a satisfied LATER entry past an unsatisfied earlier one, so the watermark names a set with a gap rather than a prefix — and a reader sees a later write without the earlier one it depended on · acceptance-sha256:690b1c0be001730bb56ca68af6c66718edff386495f998c5e69dfa56f707e754 · covers:an uncommitted entry being unreachable rather than merely unmarked
- 2026-09-04 · e226454* · mutant killed · exit 1 · `internal/core/commit/gate.go` · publishes every pending entry regardless of how many replicas hold it, so a write is visible and acknowledged before any replica confirms — the partial replication a reader must never see · acceptance-sha256:690b1c0be001730bb56ca68af6c66718edff386495f998c5e69dfa56f707e754 · covers:the watermark advancing only when the condition is met
- 2026-09-04 · e226454* · mutant inconclusive · exit 1 · `internal/core/commit/gate.go` · makes Committed answer from a different state than the watermark was advanced from, so the gate and a reader disagree — and the drift shows only under partial failure, which is when nobody is reading test output · acceptance-sha256:690b1c0be001730bb56ca68af6c66718edff386495f998c5e69dfa56f707e754 · covers:there being exactly one definition of committed
  ```
  the fence failed on a build/parse error, not an assertion
  ```
- 2026-09-04 · e226454* · mutant killed · exit 1 · `internal/core/commit/gate.go` · makes Committed answer from a different state than the watermark was advanced from, so the gate says committed while a reader cannot see the entry — and the drift shows only under partial failure, which is when nobody is reading test output · acceptance-sha256:690b1c0be001730bb56ca68af6c66718edff386495f998c5e69dfa56f707e754 · covers:there being exactly one definition of committed

## Invariants

- An uncommitted entry is unreachable, not merely unmarked.
- The watermark advances only when the condition is satisfied.
- Entries publish in order; a satisfied entry waits behind an unsatisfied earlier one, because a watermark names a PREFIX.
- `Committed` and the watermark never disagree.
- A late acknowledgement neither double-counts nor moves anything backwards.

## Risks

- ⚠ **A visibility test that checks a flag rather than a READ would pass for an implementation that publishes uncommitted entries and marks them.** The test reads through the tail and asserts absence, because a flag a reader must remember to check is a flag some reader will not check.
- A "watermark advances on commit" test that acknowledges everything at once cannot see an early advance. The test acknowledges one replica at a time and asserts the watermark after each, so an implementation advancing on the first is caught.
- ⚠ Two definitions of committed drift silently and the drift only shows under partial failure, which is exactly when nobody is reading test output. `TestOneDefinitionOfCommitted` compares them in every state rather than at the end.

## Stop Condition

Stop and ask before adding any second way to observe whether an entry is
committed — a flag on the entry, a separate index, a callback. Two definitions
drift, and the one a reader uses will not be the one the writer waited for.

## Out of Scope

- Actually replicating an entry to another node (deferred: `docs/adr/BACKLOG.md` §18)
- Flushing to disk, and bounding how long unflushed data stays unflushed (deferred: `docs/adr/BACKLOG.md` §23)
- Deciding the policy itself (permanent: boundary: ADR-004 owns `Size`, `MinSize` and the domain level; this task applies a condition rather than choosing one)

## Verification Log
- 2026-09-04 · e226454* · exit 0 · `set -o pipefail …` · acceptance-sha256:690b1c0be001730bb56ca68af6c66718edff386495f998c5e69dfa56f707e754 · ms:3721
- 2026-09-04 · e226454* · exit 0 · `set -o pipefail …` · acceptance-sha256:690b1c0be001730bb56ca68af6c66718edff386495f998c5e69dfa56f707e754 · ms:3718
- 2026-09-04 · e226454* · exit 0 · `set -o pipefail …` · acceptance-sha256:690b1c0be001730bb56ca68af6c66718edff386495f998c5e69dfa56f707e754 · ms:3820
- 2026-09-04 · e226454* · exit 0 · `set -o pipefail …` · acceptance-sha256:690b1c0be001730bb56ca68af6c66718edff386495f998c5e69dfa56f707e754 · ms:3723
- 2026-09-04 · e226454* · exit 0 · `set -o pipefail …` · acceptance-sha256:690b1c0be001730bb56ca68af6c66718edff386495f998c5e69dfa56f707e754 · ms:3616
- 2026-09-04 · 09ec963* · exit 0 · `set -o pipefail …` · acceptance-sha256:690b1c0be001730bb56ca68af6c66718edff386495f998c5e69dfa56f707e754 · ms:3686
