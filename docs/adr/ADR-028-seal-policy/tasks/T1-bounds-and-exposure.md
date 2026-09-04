# Task ADR-028-T1: State the bounds, and measure what is exposed

**Depends-on:** none
**Covers:** none — no spec
**Estimated scope:** M
**Owner:** unassigned
**Produces:** `leafstore.Policy`, `leafstore.Store.ShouldSeal`, `leafstore.Exposure`, `leafstore.Store.Exposure`, `leafstore.ErrNoBound`, `datom.SizeOf`
**Consumes:** `leafstore.Store` and its tail from ADR-026; `datom.Encode`, `datom.FixedSize`, `datom.HeaderSize` from ADR-025; `ports.Datom` from ADR-003
**Data dependency:** hermetic — a leaf in a temporary directory
**Proof map:** v1
**Rests-on:** `an exposure reported as the oldest unsealed datom rather than a mean`, `a policy with no bounds being refused rather than never sealing`, `either bound tripping a seal on its own`, `a size measured in the bytes that will actually be written`, `deciding a seal is due without performing one`

## Goal

Give a leaf a bound it can be held to, and a number an operator can read during a
power event rather than after one.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `internal/core/leafstore/policy.go` | add | `Policy`, `ShouldSeal`, `Exposure`, `ErrNoBound`. |
| `internal/core/leafstore/policy_test.go` | add | The tests below. |
| `internal/core/datom/datom.go` | modify | `SizeOf` — the encoded size of one datom, without encoding it. |
| `internal/core/datom/datom_test.go` | modify | `TestSizeOfAgreesWithTheEncoder`. |

⚠ `internal/core/datom/**` is governed by ADR-025, not by this record. `SizeOf` is
added there because only that package knows what an encoded datom costs, and the
Acceptance re-runs its suite for that reason.

## Ordered Steps

1. [S1] Write the failing tests first (TDD red): `TestExposureReportsTheOldestNotTheAverage`, `TestAPolicyWithNoBoundsIsRefused`, `TestEitherBoundTripsASeal`, `TestExposureCountsEncodedBytes`, `TestShouldSealDoesNotSeal`, `TestSizeOfAgreesWithTheEncoder`. Run the Acceptance fence and confirm it is red. ⚠Check each name is SELECTED by the fence's `-run` filter before running any mutant. [proof: acceptance]
2. [S2] Add `datom.SizeOf`, returning what one datom costs on the wire without encoding it, and pin it against the encoder in a test. ⚠A bound in bytes that did not count the fixed part would under-report a tail of many small facts by an order of magnitude. [proof: mutation]
3. [S3] Implement `Exposure` over the tail: how many datoms, how many encoded bytes, and the age of the OLDEST of them. ⚠Not the mean and not the newest — see the Risks. [proof: mutation]
4. [S4] Implement `Policy` with a size bound and an age bound, each disabled by a zero, and refuse a policy where BOTH are zero with `ErrNoBound`. ⚠"Never seal" is a legitimate choice that must be said out loud rather than being what you get by configuring nothing. [proof: mutation]
5. [S5] Make `ShouldSeal` trip on the FIRST bound reached, so either one alone is enough. [proof: mutation]
6. [S6] Make `ShouldSeal` decide and nothing else — no seal, no timer, no goroutine. ⚠ADR-020 fixed the commit point at memory, and a store that sealed itself would move it as a side effect. [proof: mutation]

## Acceptance

```bash
set -o pipefail
go test ./internal/core/leafstore/... -race -run 'TestExposureReportsTheOldestNotTheAverage|TestAPolicyWithNoBoundsIsRefused|TestEitherBoundTripsASeal|TestExposureCountsEncodedBytes|TestShouldSealDoesNotSeal' -count=1 2>&1 | tee /tmp/adr028-t1a.out \
  && ! grep -qE "no tests to run|matched no packages|^FAIL|^--- FAIL|DATA RACE" /tmp/adr028-t1a.out \
  && go test ./internal/core/datom/... -race -run 'TestSizeOfAgreesWithTheEncoder' -count=1 2>&1 | tee /tmp/adr028-t1b.out \
  && ! grep -qE "no tests to run|matched no packages|^FAIL|^--- FAIL|DATA RACE" /tmp/adr028-t1b.out \
  && go test ./internal/core/leafstore/... ./internal/core/datom/... -race -count=1 2>&1 | tee /tmp/adr028-t1c.out \
  && ! grep -qE "no tests to run|matched no packages|^FAIL|^--- FAIL|DATA RACE" /tmp/adr028-t1c.out
```

The third command re-runs both packages whole, because this adds to one and
extends the other and must not land by breaking either.

## Tests

| Test name | File | Verifies | Covers | Steps |
|-----------|------|----------|--------|-------|
| `TestExposureReportsTheOldestNotTheAverage` | `internal/core/leafstore/policy_test.go` | A tail holding one OLD datom and many recent ones reports the old one's age — **the falsifier ADR-028 names in `Enforced-by:`**. The fixture is arranged so the mean and the newest are both far smaller, and the test names both wrong answers, so an implementation that computes either fails with a message saying which | — | S3 |
| `TestAPolicyWithNoBoundsIsRefused` | `internal/core/leafstore/policy_test.go` | A `Policy{}` is `ErrNoBound`, while a policy with only a size bound and one with only an age bound are both accepted — so the refusal is about having NO bound rather than about having both | — | S4 |
| `TestEitherBoundTripsASeal` | `internal/core/leafstore/policy_test.go` | A tail over the size bound but well inside the age bound is due, and a tail inside the size bound but past the age bound is due — each bound alone, so neither is quietly ignored | — | S5 |
| `TestExposureCountsEncodedBytes` | `internal/core/leafstore/policy_test.go` | The reported byte count equals what `datom.Encode` produces for the same tail, so a tail of many small facts is not under-reported | — | S2, S3 |
| `TestShouldSealDoesNotSeal` | `internal/core/leafstore/policy_test.go` | Calling `ShouldSeal` on a tail well past both bounds leaves the segment count and the pending count exactly as they were | — | S6 |
| `TestSizeOfAgreesWithTheEncoder` | `internal/core/datom/datom_test.go` | For a table of datoms — empty value, large value, multi-byte names — `SizeOf` equals the encoder's output minus the run header, so the two cannot drift | — | S2 |

## Reachability

| Rung | How this task shows it |
|------|------------------------|
| 1 — exists | The six tests above. |
| 2 — something selects it | `ShouldSeal` is the only thing that reads a `Policy`, and `Exposure` the only thing that measures the tail. |
| 3 — the caller can discover it | `ErrNoBound` is a named refusal, and `Exposure` names its fields for what they are — `Oldest`, not `Average`. |
| 4 — it is used | ⚠ **Nothing consults it yet.** No scheduler exists, and this record deliberately does not add one: who asks is a deployment decision (`BACKLOG.md` §15). Recorded rather than implied. |

## Mutation Log

- 2026-09-04 · 406eb85* · mutant killed · exit 1 · `internal/core/datom/datom.go` · counts only the variable parts, so a tail of many small facts is under-reported by the whole fixed cost of each datom — which is the shape a busy writer produces · acceptance-sha256:f63c353147d298284613e6d81c154e8cfb8320f2eae2dadfa6af9b5226424e10 · covers:a size measured in the bytes that will actually be written
- 2026-09-04 · 406eb85* · mutant killed · exit 1 · `internal/core/leafstore/policy.go` · reports the age of the NEWEST unsealed datom instead of the oldest; it approaches zero as writes continue, so a leaf holding one acknowledged fact for an hour reports near-perfect safety while anything else is moving · acceptance-sha256:f63c353147d298284613e6d81c154e8cfb8320f2eae2dadfa6af9b5226424e10 · covers:an exposure reported as the oldest unsealed datom rather than a mean
- 2026-09-04 · 406eb85* · mutant killed · exit 1 · `internal/core/leafstore/policy.go` · accepts a policy with neither bound set, so a caller who configured nothing gets a leaf that never seals while every call reports success · acceptance-sha256:f63c353147d298284613e6d81c154e8cfb8320f2eae2dadfa6af9b5226424e10 · covers:a policy with no bounds being refused rather than never sealing
- 2026-09-04 · 406eb85* · mutant killed · exit 1 · `internal/core/leafstore/policy.go` · requires BOTH bounds before sealing, so a busy tenant over the size bound waits for the clock: the pair becomes an AND and each bound stops being a guarantee on its own · acceptance-sha256:f63c353147d298284613e6d81c154e8cfb8320f2eae2dadfa6af9b5226424e10 · covers:either bound tripping a seal on its own
- 2026-09-04 · 406eb85* · mutant inconclusive · exit 1 · `internal/core/leafstore/policy.go` · seals from inside the decision, which puts a flush wherever ShouldSeal is called from — ADR-020 fixed the commit point at memory, and this moves it as a side effect of asking a question · acceptance-sha256:f63c353147d298284613e6d81c154e8cfb8320f2eae2dadfa6af9b5226424e10 · covers:deciding a seal is due without performing one
  ```
  the fence failed on a build/parse error, not an assertion
  ```
- 2026-09-04 · 406eb85* · mutant killed · exit 1 · `internal/core/leafstore/policy.go` · makes asking whether a seal is due change the tail — the state-changing half of a seal without the durability half, which is strictly worse than sealing and is exactly what a ShouldSeal that quietly does something looks like · acceptance-sha256:f63c353147d298284613e6d81c154e8cfb8320f2eae2dadfa6af9b5226424e10 · covers:deciding a seal is due without performing one

## Invariants

- The exposure reports the oldest unsealed datom.
- A policy with no bounds is refused.
- Either bound alone is enough to make a seal due.
- `ShouldSeal` changes nothing.

## Risks

- ⚠ **An average is the natural implementation and it is wrong in the specific direction that matters.** It is smallest when the tail is fullest, because a burst of recent writes drags the mean down at exactly the moment the worst case is worst — so the number looks best when the risk is highest. The test's fixture makes the mean and the maximum differ by more than an order of magnitude.
- ⚠ **Taking the NEWEST datom's age is worse still and looks even more reasonable**, because it is what "how long since we wrote" means in conversation. It approaches zero as writes continue, so a leaf holding one acknowledged fact for an hour reports near-perfect safety as long as anything else is being written.
- ⚠ **A test that trips both bounds at once proves neither.** One case is over the size bound and inside the age bound; the other is the reverse.
- ⚠ **`SizeOf` must be pinned against the encoder rather than against a constant.** A hand-computed expected number agrees with whatever the layout was on the day it was written, and the encoder is the thing that has to stay true.
- The age bound reads the wall from the datoms' own transaction identifiers, so a clock that goes backwards reports a negative age. That is ADR-002's territory — the hybrid logical clock is what stops it — and it is not re-decided here.

## Stop Condition

Stop and ask before making `ShouldSeal` perform the seal, or before adding a
timer that calls it. Both are small, both look like completing the feature, and
both move ADR-020's commit point by putting a flush on a schedule nobody declared.

## Out of Scope

- Who calls `ShouldSeal`, and how often (deferred: `docs/adr/BACKLOG.md` §15)
- Compaction of many segments into fewer (deferred: `docs/adr/BACKLOG.md` §15)
- Percentiles or a histogram of the exposure (deferred: `docs/adr/BACKLOG.md` §23)
- Choosing actual bound values for a deployment (permanent: boundary: a threshold is valid for a configuration and never in the abstract, and this repository has measured no cluster)
- Where a datom's bytes go (permanent: boundary: ADR-024 and ADR-026 own that; this task measures how many there are)

## Verification Log
- 2026-09-04 · 406eb85* · exit 0 · `set -o pipefail …` · acceptance-sha256:f63c353147d298284613e6d81c154e8cfb8320f2eae2dadfa6af9b5226424e10 · ms:5613
- 2026-09-04 · 406eb85* · exit 0 · `set -o pipefail …` · acceptance-sha256:f63c353147d298284613e6d81c154e8cfb8320f2eae2dadfa6af9b5226424e10 · ms:5495
- 2026-09-04 · 406eb85* · exit 0 · `set -o pipefail …` · acceptance-sha256:f63c353147d298284613e6d81c154e8cfb8320f2eae2dadfa6af9b5226424e10 · ms:5520
- 2026-09-04 · 406eb85* · exit 0 · `set -o pipefail …` · acceptance-sha256:f63c353147d298284613e6d81c154e8cfb8320f2eae2dadfa6af9b5226424e10 · ms:5660
- 2026-09-04 · 406eb85* · exit 0 · `set -o pipefail …` · acceptance-sha256:f63c353147d298284613e6d81c154e8cfb8320f2eae2dadfa6af9b5226424e10 · ms:5449
- 2026-09-04 · 406eb85* · exit 0 · `set -o pipefail …` · acceptance-sha256:f63c353147d298284613e6d81c154e8cfb8320f2eae2dadfa6af9b5226424e10 · ms:5600
- 2026-09-04 · 406eb85* · exit 0 · `set -o pipefail …` · acceptance-sha256:f63c353147d298284613e6d81c154e8cfb8320f2eae2dadfa6af9b5226424e10 · ms:5676
