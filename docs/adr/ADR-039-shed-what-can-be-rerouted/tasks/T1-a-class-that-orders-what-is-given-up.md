# Task ADR-039-T1: A class that orders what is given up, without a second budget

**Depends-on:** none
**Covers:** none — no spec
**Estimated scope:** M
**Owner:** unassigned
**Produces:** `admit.Class`, `admit.ClassUser`, `admit.ClassRepair`, `admit.Classes`, the changed `admit.Controller.Admits`
**Consumes:** `admit.Kind`, `admit.Controller`, `admit.State` from ADR-015
**Data dependency:** hermetic — the controller is told its utilisation, so every test supplies its own
**Proof map:** v1
**Rests-on:** `a withdrawn node refusing a user read and still serving a repair read`, `a node deciding to withdraw from its own utilisation alone`, `one read ceiling shared by both classes rather than one per class`

## Goal

Order what a saturated node gives up, using ADR-015's own elasticity argument, and
without a second budget, a second ceiling or a third state.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `internal/core/admit/priority.go` | add | `Class`, `Classes`, and what withdrawal means per class. |
| `internal/core/admit/admit.go` | modify | `Admits` takes a class. |
| `internal/core/admit/priority_test.go` | add | The tests below. |
| `internal/core/admit/admit_test.go`, `internal/core/admit/shed_test.go` | modify | Existing callers of `Admits` say what the read is for. |
| `internal/core/admit/doc.go` | modify | Why the order is elasticity, and why it is not a second budget. |

## Ordered Steps

1. [S1] Write the failing tests first (TDD red): `TestASaturatedNodeShedsTheUserReadAndKeepsTheRepair`, `TestWithdrawalIsDecidedFromThisNodeAlone`, `TestBothClassesShareOneReadCeiling`. Run the Acceptance fence and confirm it is red. ⚠Check each name is SELECTED by the fence's `-run` filter before running any mutant. [proof: acceptance]
2. [S2] Add `Class` with exactly two values and `Classes()` returning them SHED-FIRST FIRST, so the order is readable from the API rather than inferred from a comparison somewhere. [proof: mutation]
3. [S3] Change `Admits` to take a class, with no default. ⚠A caller must say what the read is FOR: an optional class is an ignorable one, and the ignored case is the user read that should have been shed. [proof: mutation]
4. [S4] Make a WITHDRAWN node refuse `ClassUser` and still admit `ClassRepair`. ★ADR-015's elasticity argument one level down: a peer can serve the user read, so shedding it re-routes; only this node holds the fragments a repair read wants, so shedding it CANCELS the work — which is what ADR-015 says makes a shed write an outage. [proof: mutation]
5. [S5] Leave `KindWrite` admitting unconditionally, for either class. Three tiers out of one state: writes always, repair while withdrawn, user only while joined. [proof: mutation]
6. [S6] ⚠Change NOTHING about the budget: one read ceiling, one utilisation, shared by both classes. A ceiling per class is the third budget kind ADR-015 refused and `BACKLOG.md` §22 forbids. [proof: mutation]
7. [S7] Document the ordering principle in `doc.go` — elasticity, not importance — so the next class proposed has a test to pass. [proof: acceptance]

## Acceptance

```bash
set -o pipefail
go test ./internal/core/admit/... -race -run 'TestASaturatedNodeShedsTheUserReadAndKeepsTheRepair|TestWithdrawalIsDecidedFromThisNodeAlone|TestBothClassesShareOneReadCeiling' -count=1 2>&1 | tee /tmp/adr039-t1a.out \
  && ! grep -qE "no tests to run|matched no packages|^FAIL|^--- FAIL|DATA RACE" /tmp/adr039-t1a.out \
  && go test ./internal/core/admit/... ./internal/core/observe/... -race -count=1 2>&1 | tee /tmp/adr039-t1b.out \
  && ! grep -qE "no tests to run|matched no packages|^FAIL|^--- FAIL|DATA RACE" /tmp/adr039-t1b.out
```

The second command runs the whole `admit` suite because `Admits` changes shape and
every existing test calls it, and `observe` because ADR-015's withdrawal emits a
declared event whose meaning must not drift.

## Tests

| Test name | File | Verifies | Covers | Steps |
|-----------|------|----------|--------|-------|
| `TestASaturatedNodeShedsTheUserReadAndKeepsTheRepair` | `internal/core/admit/priority_test.go` | **The falsifier ADR-039 names in `Enforced-by:`.** Driven above the withdraw threshold, the node refuses `ClassUser` and admits `ClassRepair`; below the rejoin threshold it admits both; and a write is admitted throughout, for either class. ⚠ All three tiers in one test, or "withdrawn refuses reads" would pass while the class did nothing | — | S4, S5 |
| `TestWithdrawalIsDecidedFromThisNodeAlone` | `internal/core/admit/priority_test.go` | Two controllers at the same utilisation reach the same state regardless of what the other did, including when one is already withdrawn — and `Decide` takes no argument at all, which is asserted by the call itself compiling with none. ★ §22's trap in its observable form: nothing about a peer can change this node's answer | — | S3 |
| `TestBothClassesShareOneReadCeiling` | `internal/core/admit/priority_test.go` | Repair load and user load move ONE utilisation: observing a rate and reading `Utilisation()` gives the same number whichever class generated it, and there is no per-class ceiling to set. ⚠ A per-class budget would let the two stop competing, which is the shared-budget failure ADR-015 exists to prevent, inverted | — | S6 |

## Reachability

| Rung | How this task shows it |
|------|------------------------|
| 1 — exists | The three tests. |
| 2 — something selects it | `Admits` is the only admission question, and it now cannot be asked without a class. |
| 3 — the caller can discover it | `Classes()` returns them shed-first first, so the order is readable rather than inferred. |
| 4 — it is used | ⚠ **Nothing routes yet**, so nothing calls `Admits` on a served path — there is no transport (`BACKLOG.md` §18). ADR-015 decided the rule with the same gap, and this narrows what the rule says rather than adding a caller. Recorded rather than implied. |

## Mutation Log

- 2026-09-05 · 02e8933* · mutant killed · exit 1 · `internal/core/admit/priority.go` · swaps the shed order so a withdrawn node gives up REPAIR reads and keeps user reads — the intuitive order, since repair looks like background work, and the wrong one: shedding a repair read does not re-route the work to a peer because only this node holds those fragments, so it cancels it, and it prolongs the very degradation generating the load · acceptance-sha256:8254651b4f0a4e7e638c64ca38bad74857c606bca58bbb448ff57ec091d5557c · covers:a withdrawn node refusing a user read and still serving a repair read
- 2026-09-05 · 02e8933* · mutant killed · exit 1 · `internal/core/admit/shed.go` · makes a joined node stay joined above the withdraw threshold until its link is completely saturated — the shape a peer-aware rule takes, where a node keeps taking work it cannot serve on the strength of something other than its own utilisation, which is the error-returning behaviour ADR-015 rejected reached by another route · acceptance-sha256:8254651b4f0a4e7e638c64ca38bad74857c606bca58bbb448ff57ec091d5557c · covers:a node deciding to withdraw from its own utilisation alone
- 2026-09-05 · 02e8933* · mutant killed · exit 1 · `internal/core/admit/admit.go` · splits the read ceiling in two as a per-class budget would, so each class measures against half the link and the two stop competing for the resource they share — the shared-budget failure this package exists to prevent, inverted, and the third budget kind BACKLOG §22 forbids · acceptance-sha256:8254651b4f0a4e7e638c64ca38bad74857c606bca58bbb448ff57ec091d5557c · covers:one read ceiling shared by both classes rather than one per class

## Invariants

- A withdrawn node refuses user reads and admits repair reads.
- A write is admitted whatever the state and whatever the class.
- `Decide` takes no peer state.
- There is one read ceiling and one read utilisation.

## Risks

- ⚠ **The falsifier must assert all three tiers.** "A withdrawn node refuses reads" passes with the class ignored entirely; only checking that repair is still admitted proves the class does anything.
- ⚠ **Shedding repair first is the intuitive order** — repair looks like background work — and it is the one to get wrong. The test must fail if the two classes are swapped, which means asserting on both, not on one.
- ⚠ **`Admits` gaining a parameter breaks every existing caller.** That is deliberate: a defaulted class is an ignorable one, and the ignored case is the user read that should have been shed. The existing tests are updated rather than the signature softened.
- ⚠ **A test that only checks the joined state proves nothing.** The class matters only when withdrawn, so the fixture must actually cross the withdraw threshold.
- The starvation risk in ADR-039 rule 7 is not testable here and is not mitigated here — it is bounded by `BACKLOG.md` §3. Recorded on the parent record rather than implied away.

## Stop Condition

Stop and ask before giving repair its own ceiling, its own budget, or its own
threshold. `BACKLOG.md` §22 forbids a budget per class explicitly, and it is the
third budget kind ADR-015 refused — priority within a budget is a different
mechanism, and this task is that mechanism.

## Out of Scope

- The fleet-level all-withdrawn condition (deferred: T2)
- Bounding repair traffic (deferred: `docs/adr/BACKLOG.md` §3)
- Routing a shed read anywhere (deferred: `docs/adr/BACKLOG.md` §18)
- A third read class (deferred: `docs/adr/BACKLOG.md` §22 — it would have to pass the elasticity test first)

## Verification Log
- 2026-09-05 · 02e8933* · exit 0 · `set -o pipefail …` · acceptance-sha256:8254651b4f0a4e7e638c64ca38bad74857c606bca58bbb448ff57ec091d5557c · ms:3404
- 2026-09-05 · 02e8933* · exit 0 · `set -o pipefail …` · acceptance-sha256:8254651b4f0a4e7e638c64ca38bad74857c606bca58bbb448ff57ec091d5557c · ms:3420
- 2026-09-05 · 02e8933* · exit 0 · `set -o pipefail …` · acceptance-sha256:8254651b4f0a4e7e638c64ca38bad74857c606bca58bbb448ff57ec091d5557c · ms:3516
- 2026-09-05 · 02e8933* · exit 0 · `set -o pipefail …` · acceptance-sha256:8254651b4f0a4e7e638c64ca38bad74857c606bca58bbb448ff57ec091d5557c · ms:3431
