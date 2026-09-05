# ADR-039 Tasks

Implementation tasks for ADR-039: shed what a peer could serve instead, and never
let a node consult its peers to decide. See the parent ADR for the decision.

**Source of truth:** the task files' headers. This README is a derived index —
when it disagrees with a task file, the task file wins.

## Execution Order

Two tasks. T2 needs the class T1 adds only for its fixtures, but the ordering is
real: T1 narrows what withdrawal MEANS, and T2 reports a condition about it.

| Order | Task | Depends-on |
|-------|------|------------|
| 1 | T1 | none |
| 2 | T2 | T1 |

## Task Index

| ID | Title | Status | Covers | Acceptance |
|----|-------|--------|--------|------------|
| T1 | A class that orders what is given up, without a second budget | done | — | three priority tests, then the admit and observe suites |
| T2 | A fleet condition that is reported, not answered | done | — | two fleet tests, then the admit, observe and watch suites |

Status: `pending` | `partial` | `blocked` | `done`.

## Contract Coupling

| Producer | Contract | Consumer(s) | Ordering note |
|----------|----------|-------------|---------------|
| T1 | `admit.Class`, `admit.Controller.Admits` | T2 | T2's fixtures ask what a saturated node admits |
| T2 | `admit.Fleet`, `observe.KindFleetWithdrawn` | a queue or operator (`BACKLOG.md` §18/§22) | none within this record |

## Notes

- ⚠ **`BACKLOG.md` §22 NAMES ITS OWN TRAP:** *"a node that refuses to withdraw
  because its peers already have is a node that keeps taking work it cannot serve,
  which is the error-returning behaviour ADR-015 rejected, arrived at by a
  different route."*
- ★ **The trap is decidable even though the policy is not, and separating the two
  is what makes it unwritable.** "Should I withdraw" is about THIS node; "what do
  we do when everyone has" is about the fleet. Conflating them is the only way to
  write the trap. So `Decide` takes no peer state — the signature is the
  enforcement, as in ADR-033 — and the fleet view holds STATES rather than
  controllers, so there is nothing to reach through.
- ★ **ADR-015 already contained the ordering principle, and it is ELASTICITY, not
  importance.** It sheds reads and never writes because *"any replica can serve a
  read, so read work can be shed to a peer"* while a shed write *"is an outage
  rather than a re-route"*. One level down: a user read is elastic — a peer holds
  the data — and a repair read is not, because it reads the fragments THIS node
  holds. Shedding a repair read does not move the work, it cancels it.
- ⚠ **So a withdrawn node refuses USER reads and keeps serving REPAIR reads.**
  Three tiers — writes always, repair while withdrawn, user only while joined —
  out of one budget, one utilisation and one state. No second ceiling, no third
  state, both of which ADR-015 refused and §22 forbids.
- ★ **Shedding repair first is the intuitive order and it is wrong**, which is why
  it is the one the tests must be able to fail on. §16 corroborates: a degraded
  read costs `k` fragment fetches, so the reads a repair eliminates are also the
  expensive ones.
- ⚠ **The starvation risk ships OPEN.** A node saturated by repair stays withdrawn
  and keeps refusing user reads. What bounds it is a bound on repair traffic —
  `BACKLOG.md` §3, still open. Naming the owner is the honest thing available;
  a cap invented here would be a constant nobody can justify.
- ★ **An all-withdrawn fleet is an ADR-038 OBLIGATION**, not a new notion of
  "somebody should look". A state, it matters, nobody has dealt with it — and it
  does not clear when the fleet recovers, because a cluster that shed everything
  and came back is exactly what an operator should still see.
- ⚠ **T2 WITHDREW one of its `Rests-on:` claims rather than fake evidence for
  it.** "The fleet cannot change a node's decision" is a claim that a COUPLING
  DOES NOT EXIST, and mutation testing works by changing code that is there —
  falsifying it means ADDING the coupling, which is a redesign rather than a
  mutation. It is recorded as `[proof: human]` with that reason. ★ A claim in
  `Rests-on:` that no mutant can bind is exactly what the gate exists to surface;
  leaving it there unbound is the failure, not removing it.
