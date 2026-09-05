# Task ADR-038-T1: A ledger that does not forget, and that gets louder with age

**Depends-on:** none
**Covers:** none — no spec
**Estimated scope:** M
**Owner:** unassigned
**Produces:** `watch.Obligation`, `watch.Outstanding`, `watch.Ledger`, `watch.NewLedger`, `watch.FromPurge`, `watch.ErrNoSubject`, `watch.ErrNotOutstanding`
**Consumes:** `observe.Kind`, `observe.KindPurgeIncomplete` from ADR-012; `subscribe.PurgeResult`, `subscribe.PurgeIncomplete` from ADR-010
**Data dependency:** hermetic — the ledger is in-memory and every test supplies its own instants
**Proof map:** v1
**Rests-on:** `an obligation older than the retention horizon still being reported`, `re-raising an obligation keeping its first raised time`, `only an acknowledgement clearing an obligation`, `outstanding obligations being reported oldest first`

## Goal

Make `BACKLOG.md` §21's own test pass: an incomplete purge from a month ago
reaches a person, with its true age, at the top of the list.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `internal/core/watch/doc.go` | add | Why an obligation is a state, and why the stream cannot carry one. |
| `internal/core/watch/watch.go` | add | `Obligation`, `Ledger`, `Raise`, `Acknowledge`, `Outstanding`, `FromPurge`. |
| `internal/core/watch/watch_test.go` | add | The tests below. |

## Ordered Steps

1. [S1] Write the failing tests first (TDD red): `TestAMonthOldObligationIsStillReported`, `TestARetryDoesNotResetTheAge`, `TestOnlyAnAcknowledgementClearsIt`, `TestTheOldestUnansweredThingIsFirst`, `TestAnIncompletePurgeBecomesAnObligation`. Run the Acceptance fence and confirm it is red. ⚠Check each name is SELECTED by the fence's `-run` filter before running any mutant. [proof: acceptance]
2. [S2] Implement `Obligation` as a STATE keyed by what happened plus what it is about, so the same condition on the same subject is one obligation rather than a stream of them. [proof: mutation]
3. [S3] ⚠Report every outstanding obligation regardless of age, and take NO horizon parameter. ★The signature is the enforcement: a caller cannot age one out because there is nothing to age it out WITH. This is `BACKLOG.md` §21's trap — reusing ADR-010's `Horizon` is right for the log and inverts the meaning of age here. [proof: mutation]
4. [S4] Keep the FIRST raised time when the same obligation is raised again, updating only its detail. ⚠A purge that retries daily and fails daily must not look one day old forever — that disables the mechanism while leaving it apparently working. [proof: mutation]
5. [S5] Clear an obligation ONLY through `Acknowledge`, which names who and when, and refuse an acknowledgement of something not outstanding with `ErrNotOutstanding`. [proof: mutation]
6. [S6] Return outstanding obligations OLDEST FIRST with their age. ★§21's test is whether it reaches a PERSON, and newest-first buries an old unanswered thing further every day. [proof: mutation]
7. [S7] Provide `FromPurge` so an incomplete `subscribe.PurgeResult` becomes an obligation carrying the sinks it already named, and refuse to make one from a complete purge. [proof: mutation]

## Acceptance

```bash
set -o pipefail
go test ./internal/core/watch/... -race -run 'TestAMonthOldObligationIsStillReported|TestARetryDoesNotResetTheAge|TestOnlyAnAcknowledgementClearsIt|TestTheOldestUnansweredThingIsFirst|TestAnIncompletePurgeBecomesAnObligation' -count=1 2>&1 | tee /tmp/adr038-t1a.out \
  && ! grep -qE "no tests to run|matched no packages|^FAIL|^--- FAIL|DATA RACE" /tmp/adr038-t1a.out \
  && go test ./internal/core/watch/... ./internal/core/observe/... ./internal/core/subscribe/... -race -count=1 2>&1 | tee /tmp/adr038-t1b.out \
  && ! grep -qE "no tests to run|matched no packages|^FAIL|^--- FAIL|DATA RACE" /tmp/adr038-t1b.out
```

The second command carries `observe` and `subscribe` because an obligation is
identified by ADR-012's `Kind` and built from ADR-010's `PurgeResult`: a change in
either that altered what an incomplete purge reports would change what this
ledger holds, silently.

## Tests

| Test name | File | Verifies | Covers | Steps |
|-----------|------|----------|--------|-------|
| `TestAMonthOldObligationIsStillReported` | `internal/core/watch/watch_test.go` | **The falsifier ADR-038 names in `Enforced-by:`.** An obligation raised thirty-one days ago, alongside a thirty-day `subscribe.Horizon` in the same test, is still reported and reports its true age. ⚠ The horizon is CONSTRUCTED in the test precisely to show nothing consumes it — a test without one proves the horizon is unused only by omission | — | S3 |
| `TestARetryDoesNotResetTheAge` | `internal/core/watch/watch_test.go` | An obligation raised once and then re-raised thirty more times, a day apart, still reports its age from the FIRST raise. ★ Its detail DOES update, so the test distinguishes "keeps the first time" from "ignores the re-raise entirely" | — | S4 |
| `TestOnlyAnAcknowledgementClearsIt` | `internal/core/watch/watch_test.go` | Time passing does not clear an obligation; acknowledging it does, and the acknowledgement names who. Acknowledging something not outstanding is `ErrNotOutstanding` rather than a silent success — a no-op would let a typo read as "dealt with" | — | S5 |
| `TestTheOldestUnansweredThingIsFirst` | `internal/core/watch/watch_test.go` | Obligations raised out of order come back oldest first, and stay so after one in the middle is acknowledged. ⚠ Raised deliberately out of chronological order, or insertion order alone would satisfy the assertion | — | S6 |
| `TestAnIncompletePurgeBecomesAnObligation` | `internal/core/watch/watch_test.go` | A real `subscribe.PurgeResult` with an unacknowledged sink becomes an obligation naming that sink, and a complete purge produces none. ★ Built from a real `Mark` over a real registry rather than a hand-made struct, so the field ADR-010 populates is the field this reads | — | S7 |

## Reachability

| Rung | How this task shows it |
|------|------------------------|
| 1 — exists | The five tests. |
| 2 — something selects it | `FromPurge` is the bridge from ADR-010's result, and `Outstanding` the only way out. |
| 3 — the caller can discover it | Two named sentinels, and `Outstanding` takes no horizon — the signature says what it will not do. |
| 4 — it is used | ⚠ **Nothing calls `Raise` on a served path yet**, and nothing reads the ledger: there is no console and no transport (`BACKLOG.md` §18/§25). ADR-010 computes the incomplete purge, this keeps it, and a reader arrives with a surface. ⚠ **And the ledger is in memory, so a restart loses it** — the record names that gap in Consequences rather than implying otherwise. |

## Mutation Log

- 2026-09-05 · 8dbc813* · mutant killed · exit 1 · `internal/core/watch/watch.go` · applies ADR-010's retention horizon to the obligation set as well as the log, so an incomplete purge stops being reported once it passes thirty days — the system then answers "nothing is outstanding" precisely BECAUSE the problem got old, which inverts what age means and is the exact trap BACKLOG §21 walks toward while being right about reusing Horizon for the log · acceptance-sha256:3a309256215adc6d82c36111403027ebe9ed4ed3886e0215b291758af749e34b · covers:an obligation older than the retention horizon still being reported
- 2026-09-05 · 8dbc813* · mutant killed · exit 1 · `internal/core/watch/watch.go` · resets the raised time on every re-raise, so a purge that retries daily and fails daily looks one day old forever — the mechanism is disabled while continuing to produce output, and it is disabled precisely for the recurring failures that matter most · acceptance-sha256:3a309256215adc6d82c36111403027ebe9ed4ed3886e0215b291758af749e34b · covers:re-raising an obligation keeping its first raised time
- 2026-09-05 · 8dbc813* · mutant killed · exit 1 · `internal/core/watch/watch.go` · makes acknowledging something that is not outstanding a silent success, so a mistyped subject reads as "dealt with" — an operator believes they cleared an obligation that is still there, which is worse than the obligation simply remaining · acceptance-sha256:3a309256215adc6d82c36111403027ebe9ed4ed3886e0215b291758af749e34b · covers:only an acknowledgement clearing an obligation
- 2026-09-05 · 8dbc813* · mutant killed · exit 1 · `internal/core/watch/watch.go` · reports newest first, like a log, so the oldest unanswered obligation is buried further down the list every day that passes — which is exactly the failure BACKLOG §21 describes, an alert that fired once and scrolled away · acceptance-sha256:3a309256215adc6d82c36111403027ebe9ed4ed3886e0215b291758af749e34b · covers:outstanding obligations being reported oldest first

## Invariants

- Nothing but an acknowledgement removes an obligation.
- Re-raising keeps the first raised time.
- `Outstanding` takes no horizon and filters on no age.
- The oldest unanswered obligation is first.

## Risks

- ⚠ **The falsifier must construct a `Horizon` it does not use.** A test that simply ages an obligation and finds it present proves nothing about retention — the horizon has to be visible in the test for its absence from the call to mean anything.
- ⚠ **`TestARetryDoesNotResetTheAge` must also check the detail UPDATES.** Otherwise "keeps the first raised time" is satisfied by an implementation that ignores the second raise entirely, which loses the current list of outstanding sinks.
- ⚠ **Acknowledging something not outstanding must ERROR.** A silent success lets a mistyped subject read as "dealt with", which is the one outcome worse than the obligation remaining.
- ⚠ **Oldest-first must be tested with out-of-order raises.** Raising in chronological order makes insertion order and age order the same, and the test would pass with no sorting at all.
- ⚠ **The restart gap is real and the tests cannot see it.** Everything here happens in one process. Recorded on the parent record and in a follow-up rather than left for someone to discover.

## Stop Condition

Stop and ask before giving `Outstanding` a horizon, an age filter, or a limit,
however reasonable it looks next to ADR-010's retention. That parameter is the
defect: it makes an old problem stop being reported BECAUSE it is old, and the
system then answers "nothing is outstanding" with a straight face.

## Out of Scope

- Durability across a restart (deferred: `docs/adr/BACKLOG.md` §12 — the gap named on the parent record)
- Waking anybody (deferred: `docs/adr/BACKLOG.md` §18/§25)
- Exporting or sampling the event stream (deferred: `docs/adr/BACKLOG.md` §21)
- What else raises an obligation (permanent: boundary: each condition is owned by the record that detects it)

## Verification Log
- 2026-09-05 · 8dbc813* · exit 0 · `set -o pipefail …` · acceptance-sha256:3a309256215adc6d82c36111403027ebe9ed4ed3886e0215b291758af749e34b · ms:3554
- 2026-09-05 · 8dbc813* · exit 0 · `set -o pipefail …` · acceptance-sha256:3a309256215adc6d82c36111403027ebe9ed4ed3886e0215b291758af749e34b · ms:3651
- 2026-09-05 · 8dbc813* · exit 0 · `set -o pipefail …` · acceptance-sha256:3a309256215adc6d82c36111403027ebe9ed4ed3886e0215b291758af749e34b · ms:3752
- 2026-09-05 · 8dbc813* · exit 0 · `set -o pipefail …` · acceptance-sha256:3a309256215adc6d82c36111403027ebe9ed4ed3886e0215b291758af749e34b · ms:3530
- 2026-09-05 · 8dbc813* · exit 0 · `set -o pipefail …` · acceptance-sha256:3a309256215adc6d82c36111403027ebe9ed4ed3886e0215b291758af749e34b · ms:3492
