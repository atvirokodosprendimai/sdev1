# ADR-039: Shed what a peer could serve instead, and never let a node consult its peers to decide

**Status:** Accepted
**Date:** 2026-09-05
**Owner:** M
**Spec:** None — no spec stage
**Cross-references:** `docs/adr/README.md`, `docs/adr/ADR-006-erasure-coding.md`, `docs/adr/ADR-015-admission-control.md`, `docs/adr/ADR-038-obligations.md`, `docs/adr/BACKLOG.md`
**Governs:** `internal/core/admit/**`
**Enforced-by:** `internal/core/admit/priority_test.go::TestASaturatedNodeShedsTheUserReadAndKeepsTheRepair`
**Invalidates:** none — it fills `BACKLOG.md` §22's "which reads matter more" bullet and makes its named trap unwritable
**Served-path change:** A saturated node stops taking user reads and keeps taking repair reads, instead of treating them alike. And the condition where every replica has withdrawn is reported as an obligation rather than being invisible.

## Context

ADR-015 lets a saturating node stop pulling read work, turning saturation into a
routing outcome rather than an error. `BACKLOG.md` §22 leaves two questions past
it, and says both "appear under exactly the load that makes them urgent".

**Every replica sheds at once.** §22 says choosing a response needs a cluster to
observe rather than an argument — and it names the trap precisely: ⚠ *"a node that
refuses to withdraw because its peers already have is a node that keeps taking
work it cannot serve, which is the error-returning behaviour ADR-015 rejected,
arrived at by a different route."*

★ **The trap is decidable even though the policy is not**, and separating the two
is what makes it unwritable rather than merely warned about. "Should I withdraw"
is a question about THIS node. "What do we do when everyone has" is a question
about the fleet. Conflating them is the only way to write the trap.

**Which reads matter more.** §22 says shedding is wrong in both directions and
constrains the answer: ⚠ *"must not grow a second budget dimension per class;
ADR-015 refuses a third budget kind deliberately, and priority within a budget is
a different mechanism from a budget per priority."*

★ **And ADR-015 already contains the ordering principle.** It sheds reads and never
writes, and its reason is elasticity: *"KindRead is elastic: any replica can serve
a read, so read work can be shed to a peer. KindWrite is not: a leaf has one
writer, so a shed write is an outage rather than a re-route."* Applied one level
down, that same property separates the two read classes — and it needs no new
principle, no second budget, and no third state.

## Existing Primitives Audit

- `internal/core/admit` (ADR-015): supplies `Kind`, `Ceiling`, `Budget`,
  `Controller`, `State` and the hysteresis band. **Extended in place** — one
  budget, one utilisation, one state, and a class that orders what is given up.
- ADR-015's elasticity argument: **reused as the ordering principle**, not
  restated. See rule 4.
- `internal/core/watch` (ADR-038): supplies `Ledger` and `Obligation`. **Reused
  for the fleet condition** — an all-withdrawn fleet is a state, it matters, and
  nobody has dealt with it, which is exactly what an obligation is.
- `internal/core/observe` (ADR-012): supplies the declared event vocabulary. **One
  kind added** for the fleet condition.
- A third `State` — "draining" — : **rejected**, and ADR-015 already rejected it.
- A per-class budget or ceiling: **rejected below**, and §22 forbids it.

## Decision

**A node decides to withdraw from its own utilisation alone; withdrawal gives up
the reads a peer could serve and keeps the ones only this node can; and a fleet
where every replica has withdrawn is reported, not resolved.**

1. ⚠ **`Decide` consults only this node's own utilisation, and TAKES NO PEER
   STATE.** ★ The signature is the enforcement, as in ADR-033: a node cannot
   refuse to withdraw because its peers already have, because it has nothing to
   ask with. That is §22's named trap, and this is what makes it unwritable rather
   than merely warned against.

2. **What happens when every replica has withdrawn is a FLEET-level question and
   a DIFFERENT mechanism.** Nothing about it may reach rule 1's decision. ★ The
   two questions are only confusable while they live in one place.

3. **That condition is REPORTED and not answered.** §22 is right that choosing
   between a withdrawal floor, group admission and letting the queue back up needs
   a cluster to observe. ★ So it is raised as an ADR-038 obligation — a state, it
   matters, nobody has dealt with it — which is the shape that makes it visible
   without pretending to a policy nobody can justify yet.

4. ★ **A read is shed if a PEER COULD SERVE IT INSTEAD, and kept if not.** This is
   ADR-015's own elasticity argument one level down:
   - A **user read** is elastic. Any replica holds the data, so shedding it
     re-routes it, which is what the mechanism is for.
   - A **repair read** is not. It reads fragments THIS node holds, so shedding it
     does not re-route the work — it cancels it, which is what ADR-015 says makes
     a shed write an outage rather than a re-route.

   ⚠ So a withdrawn node refuses user reads and keeps serving repair reads. Three
   tiers — writes always, repair while withdrawn, user only while joined — out of
   one budget, one utilisation and one state.

5. **The class is an ORDER, never a second budget.** ⚠ There is still one ceiling
   and one utilisation for reads. §22 forbids a budget per class, and a per-class
   ceiling is exactly the "third budget kind" ADR-015 refused; priority within a
   budget is a different mechanism, and this is that mechanism.

6. **§16's cost argument points the same way, and is a second reason rather than
   the first.** A degraded read costs `k` fragment fetches, so the reads a repair
   makes unnecessary are also the expensive ones — shedding repair prolongs the
   degradation that is generating the load. ★ Recorded as corroboration; rule 4
   stands on elasticity, which is ADR-015's own principle rather than a new one.

7. ⚠ **The starvation risk is real, is stated, and is NOT bounded by this
   record.** A node saturated by repair work stays withdrawn and keeps refusing
   user reads. What bounds it is a bound on repair traffic, which is
   `BACKLOG.md` §3 and is open. Naming the owner is the honest thing available;
   inventing a cap here would be a constant nobody can justify.

**What would falsify this.** A node above its withdraw threshold that still admits
a user read — for any reason, including that its peers have withdrawn. That is the
falsifier in `Enforced-by:`, and it is §22's trap in its observable form.

## Alternatives Considered

- **Let a node consider its peers before withdrawing.** It is the direct answer to
  "every replica sheds at once", and it is what anyone would try first. Rejected
  under rule 1: §22 names it as the trap — a node that stays joined because its
  peers left keeps taking work it cannot serve, which is the error-returning
  behaviour ADR-015 rejected, reached by another route.
- **A withdrawal floor: at most N replicas withdrawn at once.** It bounds the
  problem directly. Rejected under rule 3 as premature — it is one of the three
  answers §22 says needs a cluster to choose between, and it is rule 1's trap
  wearing a quota. Reporting the condition leaves all three open.
- **A budget per class: separate ceilings for repair and user reads.** It makes
  the priority explicit and tunable. Rejected under rule 5, and §22 forbids it
  outright: it is the third budget kind ADR-015 refused, and it would let repair
  load and user load stop competing — which is the shared-budget failure the
  package exists to prevent, inverted.
- **A third state, "draining", that sheds user reads before repair ones.** It
  makes the tiers explicit. Rejected: ADR-015 already refused a third state, and
  the class achieves the same ordering without one — the state says whether the
  node is saturated, and the class says what saturation costs you.
- **Shed repair first, keep user reads.** The intuitive order: repair is
  background work. Rejected under rules 4 and 6: a user read can be served by a
  peer and a repair read cannot, so shedding repair cancels work rather than
  moving it — and §16 says the degraded reads repair eliminates are the expensive
  ones, so it also borrows capacity at a bad rate.
- **Treat both alike, as today.** No change. Rejected: §22 says it is wrong in
  both directions, and the elasticity argument says which direction to fix first.
- **Decide the all-withdrawn response now.** It would close §22 completely.
  Rejected under rule 3: the three candidate answers differ sharply and choosing
  needs observation, and a wrong choice here is exercised only under the
  cluster-wide load that makes it matter.

## Component / Boundary Impact

No new component. `internal/core/admit` gains a class and a fleet view; the fleet
view raises an ADR-038 obligation rather than acting.

⚠ The boundary that matters is rule 2's, and it is a boundary INSIDE the package:
the node's own decision and the fleet's condition are separate functions with
separate inputs, so nothing about the fleet can reach the node's decision. That
separation is the mechanism, not a tidiness preference.

## Wiring & Contract Changes

| Surface | Change | Producer | Consumer(s) |
|---------|--------|----------|-------------|
| `admit.Class` / `admit.ClassUser` / `admit.ClassRepair` | new — what a read is for | T1 | callers |
| `admit.Classes` | new — the two, shed-first first | T1 | callers |
| `admit.Controller.Admits` | **CHANGED** — takes a class as well as a kind | T1 | callers |
| `admit.Fleet` / `admit.NewFleet` | new — replica states, and the all-withdrawn condition | T2 | operators |
| `admit.Fleet.Report` / `Observe` / `AllWithdrawn` | new | T2 | operators |
| `observe.KindFleetWithdrawn` | new declared kind | T2 | operators |

## Inter-task Contracts

| Contract | Producing task | Consuming task(s) | Breaking? |
|----------|----------------|-------------------|-----------|
| `admit.Class`, `admit.Controller.Admits` (T1) | T1 | T2 | ⚠ Yes — `Admits` gains a parameter, deliberately: a caller must say what the read is FOR, and defaulting it would make the class optional and therefore ignorable |
| `admit.Fleet` (T2) | T2 | a queue or an operator (`BACKLOG.md` §18/§22) | No |

## Consequences

- **Positive:** §22's named trap is unwritable rather than warned about — a node
  has no peer state to consult.
- **Positive:** The read ordering follows from ADR-015's own elasticity argument,
  so it introduces no new principle and needs no new budget, ceiling or state.
- **Positive:** The all-withdrawn condition stops being invisible without anyone
  choosing a policy for it, and it reuses ADR-038 rather than growing a second
  notion of "somebody needs to look at this".
- **Negative:** ⚠ `Admits` gains a parameter, so every caller must say what a read
  is for. That is the intent — an optional class is an ignorable one — and it is a
  breaking change to an internal surface with few callers, taken now for that
  reason.
- **Negative:** ⚠ Rule 7's starvation risk ships open. A node saturated by repair
  stays withdrawn and keeps refusing user reads, and what bounds it is §3's
  unbounded repair traffic. Named, with its owner, rather than capped by a
  constant nobody can justify.
- **Neutral:** Nothing routes, so nothing consumes `Admits` on a served path yet.

## Out of Scope

- Choosing what a fleet DOES when every replica has withdrawn (deferred: `docs/adr/BACKLOG.md` §22 — §22 says the three candidate answers need a cluster to choose between; rule 3 reports the condition instead)
- Bounding repair traffic, which is what bounds rule 7's starvation risk (deferred: `docs/adr/BACKLOG.md` §3)
- Measuring utilisation (permanent: boundary: ADR-015 is told its utilisation, because two counts of one quantity diverge and the one an operator sees would not be the one that shed)
- More read classes than two (deferred: `docs/adr/BACKLOG.md` §22 — a third would need its own elasticity argument, and rule 4 is the test it would have to pass)
- Routing a shed read to a peer (deferred: `docs/adr/BACKLOG.md` §18 — there is no transport)
- The nearest-`k` versus load-spread tension (deferred: `docs/adr/BACKLOG.md` §22 — ADR-018 and ADR-037 both record it, and it needs the same cluster observation)

## Risks

| Risk | Likelihood | Impact | Mitigation |
|------|------------|--------|------------|
| A node consults its peers before withdrawing | **High — it is the direct answer to "every replica sheds at once"** | **Critical** — a node keeps taking work it cannot serve, which is the error-returning behaviour ADR-015 rejected, reached by another route | Rule 1: `Decide` takes no peer state, so the question cannot be asked. It is the record's falsifier |
| Repair is shed before user reads | High — repair looks like background work | High — shedding it cancels work rather than re-routing it, and prolongs the degradation generating the load | Rule 4, from ADR-015's own elasticity argument, with rule 6 corroborating |
| A per-class budget is added | Med — it makes priority tunable | High — it is the third budget kind ADR-015 refused, and it stops the classes competing for one resource | Rule 5, and §22 forbids it explicitly |
| A third state is added to express the tiers | Med | Med — ADR-015 refused one, and every rule about what it does with new work collapses into the two that exist | Rule 4 gets three tiers from one state |
| Repair starves user reads | **Med, and unbounded today** | High — a node saturated by repair never takes a user read again | Rule 7: stated, with §3 named as what bounds it. NOT mitigated here |
| The all-withdrawn condition is answered rather than reported | Med — reporting feels incomplete | Med — a policy chosen without observation is exercised only under the load that makes it matter | Rule 3, and the alternatives record all three candidates |

## Rollback

Reverting rule 4 returns to treating both read classes alike, which is today's
behaviour and is what §22 calls wrong in both directions. ⚠ Rule 1 is the one not
to revert casually: it looks like a missing feature — a node that cannot see its
peers — and it is the mechanism.

## Follow-ups

- [ ] When there is a cluster to observe (`BACKLOG.md` §22), choose what an all-withdrawn fleet does, from the three answers §22 names. Rule 3's report is what makes the choice measurable rather than argued.
- [ ] When repair traffic is bounded (`BACKLOG.md` §3), revisit rule 7 — that bound is what makes rule 4 safe, and until it exists the starvation risk is real rather than theoretical.
- [ ] If a third read class is ever proposed, make it pass rule 4's test first: can a peer serve this instead? A class that cannot answer that has no place in the order.
