# Task ADR-041-T1: A gauge that remembers the worst moment, and a bound that needs both halves

**Depends-on:** none
**Covers:** none — no spec
**Estimated scope:** M
**Owner:** unassigned
**Produces:** `commit.Exposure`, `commit.Bound`, `commit.Meter`, `commit.NewMeter`, `commit.Meter.Committed`, `commit.Meter.Flushed`, `commit.Meter.Current`, `commit.Meter.Peak`, `commit.Meter.Exceeds`, `commit.ErrIncompleteBound`
**Consumes:** `tx.TxID` from ADR-002; `commit.Gate` from ADR-020 (as the thing this is DISTINCT from)
**Data dependency:** hermetic — commits, flushes and instants are all supplied
**Proof map:** v1
**Rests-on:** `the reported exposure being the peak rather than the value after a burst`, `a bound with only one half being refused`, `the peak resetting on a flush and on nothing else`, `an exceeded bound asking for a flush rather than refusing a write`

## Goal

Give `BACKLOG.md` §23 the counter it says nobody has, and report it as the number
that was true at the worst moment rather than the one that is true when somebody
looks.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `internal/core/commit/exposure.go` | add | `Exposure`, `Bound`, `Meter`, and the peak. |
| `internal/core/commit/exposure_test.go` | add | The tests below. |
| `internal/core/commit/doc.go` | modify | Why the peak and not the average, and why both bounds. |

## Ordered Steps

1. [S1] Write the failing tests first (TDD red): `TestTheReportedExposureIsThePeakNotTheCalmAfterIt`, `TestABoundNeedsBothHalves`, `TestThePeakResetsOnAFlushAndNothingElse`, `TestAnExceededBoundAsksForAFlush`. Run the Acceptance fence and confirm it is red. ⚠Check each name is SELECTED by the fence's `-run` filter before running any mutant. [proof: acceptance]
2. [S2] Track entries COMMITTED and not yet FLUSHED. ⚠Distinct from `Gate.Pending`, which is written-and-not-committed: the first is a promise that could still be broken, the second is data nobody was promised, and reporting one as the other says a busy node is exposed or an exposed one is calm. [proof: mutation]
3. [S3] Record the PEAK alongside the present value, updating it on every commit. ★§23's trap is an average, and the instantaneous reading has the same defect one step removed — asked after a burst it reports the calm. [proof: mutation]
4. [S4] Refuse a `Bound` missing either half with `ErrIncompleteBound`. ⚠Different from ADR-028 deliberately: size-only leaves a QUIET tenant unbounded in time, time-only leaves a BUSY one unbounded in bytes, so neither alone bounds anything. [proof: mutation]
5. [S5] Reset the peak on a FLUSH and on nothing else — never on being read. ⚠A gauge that clears when somebody looks reports a different number to the second reader, and two operators comparing notes would see a system disagreeing with itself. [proof: mutation]
6. [S6] `Exceeds` reports that a flush is DUE. It refuses nothing and blocks nothing: converting a durability exposure into an availability outage is the trade ADR-040 and ADR-015 both refuse. [proof: mutation]

## Acceptance

```bash
set -o pipefail
go test ./internal/core/commit/... -race -run 'TestTheReportedExposureIsThePeakNotTheCalmAfterIt|TestABoundNeedsBothHalves|TestThePeakResetsOnAFlushAndNothingElse|TestAnExceededBoundAsksForAFlush' -count=1 2>&1 | tee /tmp/adr041-t1a.out \
  && ! grep -qE "no tests to run|matched no packages|^FAIL|^--- FAIL|DATA RACE" /tmp/adr041-t1a.out \
  && go test ./internal/core/commit/... ./internal/core/tail/... ./internal/core/durability/... -race -count=1 2>&1 | tee /tmp/adr041-t1b.out \
  && ! grep -qE "no tests to run|matched no packages|^FAIL|^--- FAIL|DATA RACE" /tmp/adr041-t1b.out
```

The second command carries `tail` and `durability` because the commit point is
defined against both — the watermark ADR-020 advances and the policy that decides
when it may — and a change to either would move what "committed" means underneath
this meter.

## Tests

| Test name | File | Verifies | Covers | Steps |
|-----------|------|----------|--------|-------|
| `TestTheReportedExposureIsThePeakNotTheCalmAfterIt` | `internal/core/commit/exposure_test.go` | **The falsifier ADR-041 names in `Enforced-by:`.** A burst commits many entries, a flush clears most, and the peak still reports the burst while `Current` reports the calm. ⚠ Both numbers asserted in one test: the peak alone could be a stuck maximum, and the current alone is the defect | — | S3 |
| `TestABoundNeedsBothHalves` | `internal/core/commit/exposure_test.go` | A bound with only an age, only a size, or neither is `ErrIncompleteBound`; with both it is accepted. ★ And the reason is shown rather than asserted: under a size-only bound a single entry sits forever without exceeding, and under an age-only bound an arbitrary volume fits inside the interval | — | S4 |
| `TestThePeakResetsOnAFlushAndNothingElse` | `internal/core/commit/exposure_test.go` | Reading `Peak` twice gives the same answer; `Current` does not reset it; a flush does. ⚠ The double read is the point — a gauge that clears on read reassures the second operator because the first one looked | — | S5 |
| `TestAnExceededBoundAsksForAFlush` | `internal/core/commit/exposure_test.go` | Past either half of the bound, `Exceeds` is true and `Committed` still accepts more — nothing is refused and nothing blocks. ⚠ Asserted, because "stop accepting" is the response that looks safe and turns a durability exposure into an outage | — | S6 |

## Reachability

| Rung | How this task shows it |
|------|------------------------|
| 1 — exists | The four tests. |
| 2 — something selects it | `Committed` and `Flushed` are the only ways state moves; `Current`, `Peak` and `Exceeds` the only ways it is read. |
| 3 — the caller can discover it | `NewMeter` refuses an incomplete bound by name, so a caller learns the rule at construction rather than from a number that never fires. |
| 4 — it is used | ⚠ **Nothing flushes**, so `Flushed` is called only by tests and the window's closing edge does not exist yet (`BACKLOG.md` §12). ADR-020 named this window in its own Consequences and left it unmeasured; this measures it. Recorded rather than implied. |

## Mutation Log

- 2026-09-05 · 3aa9c78* · mutant survived · exit 0 · `internal/core/commit/exposure.go` · makes the "peak" track the present value instead of remembering the worst, so a burst that rises and subsides reports the calm after it — which is BACKLOG §23 trap one step removed from the average it names, and it hides the exposure exactly when a correlated failure is most likely · acceptance-sha256:8731fac27397398ca3ed295896a5dc3cd85acfa5c2df7260714f91e98d8e7f3e · covers:the reported exposure being the peak rather than the value after a burst
  ```
  the fence passed with the mechanism broken; it may not materialize, compile, load, or assert on the changed path
  ```
- 2026-09-05 · 3aa9c78* · mutant killed · exit 1 · `internal/core/commit/exposure.go` · makes the "peak" track the present value instead of remembering the worst, so a burst that a partial flush has drained reports the calm left behind — which is BACKLOG §23 trap one step removed from the average it names, and it hides the exposure exactly when a correlated failure is most likely · acceptance-sha256:8731fac27397398ca3ed295896a5dc3cd85acfa5c2df7260714f91e98d8e7f3e · covers:the reported exposure being the peak rather than the value after a burst
- 2026-09-05 · 3aa9c78* · mutant killed · exit 1 · `internal/core/commit/exposure.go` · accepts a bound with only one half, as ADR-028 does for sealing — but here each single-bound policy leaves a whole class of tenant unbounded: size-only lets a quiet tenant one entry sit unflushed forever, and age-only lets a busy one commit an arbitrary volume inside the interval · acceptance-sha256:8731fac27397398ca3ed295896a5dc3cd85acfa5c2df7260714f91e98d8e7f3e · covers:a bound with only one half being refused
- 2026-09-05 · 3aa9c78* · mutant killed · exit 1 · `internal/core/commit/exposure.go` · clears the peak when it is READ, so two operators reading the same window in sequence get different answers — and the second is reassured precisely because the first one looked, which is the worst way for a gauge to be wrong · acceptance-sha256:8731fac27397398ca3ed295896a5dc3cd85acfa5c2df7260714f91e98d8e7f3e · covers:the peak resetting on a flush and on nothing else
- 2026-09-05 · 3aa9c78* · mutant killed · exit 1 · `internal/core/commit/exposure.go` · stops accepting commits once the bound is exceeded, converting a durability exposure into an availability outage — the node is behind rather than unsafe, and this is the trade ADR-015 refuses for a shed write and ADR-040 refuses for a below-floor leaf · acceptance-sha256:8731fac27397398ca3ed295896a5dc3cd85acfa5c2df7260714f91e98d8e7f3e · covers:an exceeded bound asking for a flush rather than refusing a write

## Invariants

- The exposure is committed-and-not-flushed, never written-and-not-committed.
- The peak is reported alongside the present value.
- The peak resets on a flush and on nothing else.
- Nothing here refuses or blocks a commit.

## Risks

- ⚠ **The falsifier must assert BOTH numbers.** A peak alone could be a maximum that never falls; a current alone is the defect being guarded against. Only the pair — peak high, current low, at the same instant — shows the rule.
- ⚠ **A MUTANT SURVIVED AND FOUND A DESIGN FLAW, NOT A MISSING TEST.** The first version tracked the window as a running total that only a full flush could reset. Under that design the exposure NEVER FALLS within a window, so `Peak` and `Current` are the same number by construction — and a mutant replacing the peak's `if greater` with a plain assignment changed nothing observable. ★ The peak was not under-tested; it was REDUNDANT, and rule 2 was vacuous. ⚠ Which means the record would have shipped a safeguard that could not fail because it could not differ from the thing it was safeguarding against. The fix was to the DESIGN: a flush is PARTIAL, entries committed while it runs survive it, and the peak resets only when the window EMPTIES. Only then can the present value fall below the worst moment, which is the entire property `BACKLOG.md` §23 asks for. The re-run mutant died immediately.
- ⚠ **`TestABoundNeedsBothHalves` should demonstrate WHY, not just that.** A refusal test proves the guard fires; showing that a single entry never exceeds a size-only bound, and that a volume fits inside an age-only one, proves the guard is right.
- ⚠ **Reading `Peak` twice is the assertion for rule 5**, not reading it once. Clearing on read is invisible to a single reader and is exactly what fools the second one.
- ⚠ **The meter must not refuse anything.** "Stop accepting until flushed" is the response that looks safe, and it converts a durability exposure into an availability outage — which ADR-040 and ADR-015 both refuse for the same reason.
- Nothing flushes on a served path, so this measures a window whose closing edge does not exist. Recorded on the parent record as a consequence rather than hidden.

## Stop Condition

Stop and ask before making an exceeded bound refuse or block a commit. The node
is behind, not unsafe, and trading a durability exposure for a certain outage is
the trade this corpus refuses in three other places.

## Out of Scope

- Flushing (deferred: `docs/adr/BACKLOG.md` §12)
- Choosing the bound values (permanent: boundary: a property of a deployment's hardware and its tolerance for loss)
- Exporting the peak so it survives the event it describes (deferred: `docs/adr/BACKLOG.md` §21)
- Per-tenant exposure (deferred: `docs/adr/BACKLOG.md` §22)

## Verification Log
- 2026-09-05 · 3aa9c78* · exit 0 · `set -o pipefail …` · acceptance-sha256:8731fac27397398ca3ed295896a5dc3cd85acfa5c2df7260714f91e98d8e7f3e · ms:4079
- 2026-09-05 · 3aa9c78* · exit 0 · `set -o pipefail …` · acceptance-sha256:8731fac27397398ca3ed295896a5dc3cd85acfa5c2df7260714f91e98d8e7f3e · ms:4015
- 2026-09-05 · 3aa9c78* · exit 0 · `set -o pipefail …` · acceptance-sha256:8731fac27397398ca3ed295896a5dc3cd85acfa5c2df7260714f91e98d8e7f3e · ms:3900
- 2026-09-05 · 3aa9c78* · exit 0 · `set -o pipefail …` · acceptance-sha256:8731fac27397398ca3ed295896a5dc3cd85acfa5c2df7260714f91e98d8e7f3e · ms:3976
- 2026-09-05 · 3aa9c78* · exit 0 · `set -o pipefail …` · acceptance-sha256:8731fac27397398ca3ed295896a5dc3cd85acfa5c2df7260714f91e98d8e7f3e · ms:3989
- 2026-09-05 · 3aa9c78* · exit 0 · `set -o pipefail …` · acceptance-sha256:8731fac27397398ca3ed295896a5dc3cd85acfa5c2df7260714f91e98d8e7f3e · ms:3931
