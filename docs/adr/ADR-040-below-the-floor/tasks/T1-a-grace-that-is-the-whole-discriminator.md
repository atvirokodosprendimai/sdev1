# Task ADR-040-T1: A grace that is the whole discriminator, and a status that never hides behind it

**Depends-on:** none
**Covers:** none — no spec
**Estimated scope:** M
**Owner:** unassigned
**Produces:** `durability.Shortfall`, `durability.Watchdog`, `durability.NewWatchdog`, `durability.Watchdog.Observe`, `durability.Watchdog.Status`, `durability.Watchdog.Report`, `durability.ErrNoGrace`, `observe.KindLeafBelowFloor`
**Consumes:** `durability.Policy`, `durability.Policy.Satisfied` from ADR-004; `watch.Ledger`, `watch.Obligation` from ADR-038; `observe.Kind` from ADR-012; `addr.LeafID` from ADR-001
**Data dependency:** hermetic — domain membership and instants are supplied
**Proof map:** v1
**Rests-on:** `a leaf short for longer than the grace producing an obligation, and one short for a moment producing none`, `the status being visible DURING the grace`, `a below-floor leaf staying on the read path`, `a watchdog with no declared grace being refused`

## Goal

Answer `BACKLOG.md` §10's hardest question — telling a restart from a shortfall —
with the only signal that actually distinguishes them, and without letting the
grace become a place a real shortfall can hide.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `internal/core/durability/shortfall.go` | add | `Shortfall`, `Watchdog`, the grace, and the obligation it raises. |
| `internal/core/observe/kinds.go` | modify | `KindLeafBelowFloor`, with its declared reader and fields. |
| `internal/core/durability/shortfall_test.go` | add | The tests below. |
| `internal/core/durability/doc.go` | modify | Why only age separates the two, and why the status does not wait. |

## Ordered Steps

1. [S1] Write the failing tests first (TDD red): `TestARestartAndAShortfallAreToldApartByAgeAlone`, `TestTheStatusDoesNotHideBehindTheGrace`, `TestABelowFloorLeafStaysReadable`, `TestAWatchdogWithNoGraceIsRefused`. Run the Acceptance fence and confirm it is red. ⚠Check each name is SELECTED by the fence's `-run` filter before running any mutant. [proof: acceptance]
2. [S2] Declare `observe.KindLeafBelowFloor`. ⚠Distinct from `KindWriteRefusedBelowFloor`, which is about a REFUSED WRITE — a leaf can be short of copies with nobody writing to it at all, and conflating them would make a quiet shortfall invisible. [proof: mutation]
3. [S3] Implement `Watchdog.Observe` to record WHEN a leaf first went below its floor, using `Policy.Satisfied` as the test rather than counting domains again. ★Recording the FIRST crossing is what makes age meaningful; re-observing a still-short leaf must not restart its clock, for the same reason ADR-038 rule 6 gives. [proof: mutation]
4. [S4] `Status` returns every leaf currently below its floor with its age — INCLUDING those inside the grace — oldest first. ⚠This is rule 4 and it is the step that stops the grace becoming a hiding place. [proof: mutation]
5. [S5] `Report` raises an ADR-038 obligation only for leaves short for LONGER than the grace. ★Age is the entire discriminator: instantaneously a restart and a shortfall are the same observation, so nothing else could make the distinction. [proof: mutation]
6. [S6] Clear a leaf's record when it returns above the floor, so `Status` reflects the present — but ⚠leave any obligation already raised standing, because only an acknowledgement clears one and a leaf that fell below and came back is what an operator should still see. [proof: mutation]
7. [S7] Refuse a watchdog with a non-positive grace, with `ErrNoGrace`. ⚠Zero pages on every restart and there is no way to spell "infinite" — both look like "not configured". [proof: mutation]

## Acceptance

```bash
set -o pipefail
go test ./internal/core/durability/... -race -run 'TestARestartAndAShortfallAreToldApartByAgeAlone|TestTheStatusDoesNotHideBehindTheGrace|TestABelowFloorLeafStaysReadable|TestAWatchdogWithNoGraceIsRefused' -count=1 2>&1 | tee /tmp/adr040-t1a.out \
  && ! grep -qE "no tests to run|matched no packages|^FAIL|^--- FAIL|DATA RACE" /tmp/adr040-t1a.out \
  && go test ./internal/core/durability/... ./internal/core/observe/... ./internal/core/watch/... -race -count=1 2>&1 | tee /tmp/adr040-t1b.out \
  && ! grep -qE "no tests to run|matched no packages|^FAIL|^--- FAIL|DATA RACE" /tmp/adr040-t1b.out
```

The second command carries `observe` and `watch` because the condition is a
declared kind and an obligation: a change to what is declared, or to what clears
an obligation, would change what this reports without touching this package.

## Tests

| Test name | File | Verifies | Covers | Steps |
|-----------|------|----------|--------|-------|
| `TestARestartAndAShortfallAreToldApartByAgeAlone` | `internal/core/durability/shortfall_test.go` | **The falsifier ADR-040 names in `Enforced-by:`.** Two leaves with IDENTICAL domain counts against identical policies: one recovers inside the grace and produces no obligation, the other stays short past it and produces one. ★ Identical on purpose — if the fixtures differed in any way but time, the test would not show that time is what separates them | — | S5 |
| `TestTheStatusDoesNotHideBehindTheGrace` | `internal/core/durability/shortfall_test.go` | A leaf one second below its floor appears in `Status` with its age, while `Report` raises nothing for it. ⚠ Both halves in one test: the status showing AND the obligation withheld is the whole of rule 4, and either alone is a different rule | — | S4 |
| `TestABelowFloorLeafStaysReadable` | `internal/core/durability/shortfall_test.go` | Nothing on `Watchdog` removes, disables or hides a leaf: after reporting, the leaf is still in `Status` and the watchdog offers no method that would take it out of service. ⚠ Asserted rather than left unexercised, because "evict it" is the response that feels safest and is the one that turns a durability risk into an outage | — | S3 |
| `TestAWatchdogWithNoGraceIsRefused` | `internal/core/durability/shortfall_test.go` | A zero or negative grace is `ErrNoGrace`. ★ The grace is the entire discriminator, so a watchdog without one is not a watchdog with a default — it is one that cannot answer the question it exists for | — | S7 |

## Reachability

| Rung | How this task shows it |
|------|------------------------|
| 1 — exists | The four tests. |
| 2 — something selects it | `Observe` is the only way a leaf's state enters, and `Status`/`Report` the only ways anything leaves. |
| 3 — the caller can discover it | `KindLeafBelowFloor` is declared with a named reader; `NewWatchdog` refuses without a grace. |
| 4 — it is used | ⚠ **Nothing feeds it on a served path.** Domain membership comes from a cluster that does not exist (`BACKLOG.md` §18), and nothing reads the ledger (`BACKLOG.md` §25). ADR-004 computes the floor and this decides what being under it MEANS over time. Recorded rather than implied. |

## Mutation Log

- 2026-09-05 · 344a068* · mutant killed · exit 1 · `internal/core/durability/shortfall.go` · reports every below-floor leaf immediately, ignoring the grace — so a rolling restart pages an operator exactly like a genuine shortfall, which is the pair BACKLOG §10 says look identical for the first few seconds and demand opposite responses · acceptance-sha256:c855cad21e92576156176ee5086506e5b47095cb27435f03689e170af6f772bc · covers:a leaf short for longer than the grace producing an obligation, and one short for a moment producing none
- 2026-09-05 · 344a068* · mutant killed · exit 1 · `internal/core/durability/shortfall.go` · suppresses the STATUS during the grace as well as the obligation — the obvious single "suppress for N seconds" rule — so the grace becomes a window in which a genuine shortfall is invisible, and an operator cannot watch a rolling restart dip and recover · acceptance-sha256:c855cad21e92576156176ee5086506e5b47095cb27435f03689e170af6f772bc · covers:the status being visible DURING the grace
- 2026-09-05 · 344a068* · mutant killed · exit 1 · `internal/core/durability/shortfall.go` · takes the leaf out of the status once it has been reported, which is eviction wearing a bookkeeping costume — the leaf is still short and still degrading, and an operator looking at the status now sees a healthy cluster · acceptance-sha256:c855cad21e92576156176ee5086506e5b47095cb27435f03689e170af6f772bc · covers:a below-floor leaf staying on the read path
- 2026-09-05 · 344a068* · mutant inconclusive · exit 1 · `internal/core/durability/shortfall.go` · accepts a watchdog with no declared grace, silently substituting a constant — so a deployment that never configured one pages on the wrong schedule and nobody wrote the number down, which is the failure the refusal exists to prevent · acceptance-sha256:c855cad21e92576156176ee5086506e5b47095cb27435f03689e170af6f772bc · covers:a watchdog with no declared grace being refused
  ```
  the fence failed on a build/parse error, not an assertion
  ```
- 2026-09-05 · 344a068* · mutant killed · exit 1 · `internal/core/durability/shortfall.go` · accepts an undeclared grace by silently substituting a constant, so a deployment that never configured one pages on a schedule nobody chose and nobody wrote down — and "not configured" becomes indistinguishable from "configured to thirty seconds" · acceptance-sha256:c855cad21e92576156176ee5086506e5b47095cb27435f03689e170af6f772bc · covers:a watchdog with no declared grace being refused

## Invariants

- Only age separates a restart from a shortfall.
- The status shows a short leaf throughout the grace.
- Re-observing a still-short leaf does not restart its clock.
- Nothing here takes a leaf out of service.

## Risks

- ⚠ **"Suppress for N seconds" is the obvious single rule and it is wrong.** It conflates hiding the STATUS with withholding the OBLIGATION, and the first makes a genuine shortfall invisible for the grace. The test asserts both halves in one place so they cannot drift apart.
- ⚠ **The falsifier's two leaves must be IDENTICAL except in duration.** If they differ in domain count, policy or anything else, the test shows that something distinguishes them — not that TIME does, which is the claim.
- ⚠ **Re-observing must not restart the clock.** A watchdog polled every second would otherwise show every leaf as one second old forever, which is ADR-038 rule 6 arriving in a new package.
- ⚠ **A recovered leaf leaves `Status` and does NOT clear its obligation.** Two different lifetimes in one type, and conflating them either keeps a healthy leaf in the status list or silently resolves an incident.
- Nothing feeds this on a served path, so the task adds a rule and not a behaviour. Recorded on the parent record.

## Stop Condition

Stop and ask before adding anything that removes a below-floor leaf from service.
That converts a durability risk into a certain outage, and the copies it takes out
are exactly the ones still holding the data.

## Out of Scope

- Re-replicating (deferred: `docs/adr/BACKLOG.md` §19)
- Feeding real domain membership (deferred: `docs/adr/BACKLOG.md` §18)
- Waking anybody (deferred: `docs/adr/BACKLOG.md` §25)
- Choosing a default grace (permanent: boundary: what a restart costs is a property of a deployment)

## Verification Log
- 2026-09-05 · 344a068* · exit 0 · `set -o pipefail …` · acceptance-sha256:c855cad21e92576156176ee5086506e5b47095cb27435f03689e170af6f772bc · ms:3645
- 2026-09-05 · 344a068* · exit 0 · `set -o pipefail …` · acceptance-sha256:c855cad21e92576156176ee5086506e5b47095cb27435f03689e170af6f772bc · ms:3657
- 2026-09-05 · 344a068* · exit 0 · `set -o pipefail …` · acceptance-sha256:c855cad21e92576156176ee5086506e5b47095cb27435f03689e170af6f772bc · ms:3641
- 2026-09-05 · 344a068* · exit 0 · `set -o pipefail …` · acceptance-sha256:c855cad21e92576156176ee5086506e5b47095cb27435f03689e170af6f772bc · ms:3583
- 2026-09-05 · 344a068* · exit 0 · `set -o pipefail …` · acceptance-sha256:c855cad21e92576156176ee5086506e5b47095cb27435f03689e170af6f772bc · ms:3530
- 2026-09-05 · 344a068* · exit 0 · `set -o pipefail …` · acceptance-sha256:c855cad21e92576156176ee5086506e5b47095cb27435f03689e170af6f772bc · ms:3566
