# ADR-040: A leaf below the floor is reported and never evicted, and only its AGE tells a restart from a shortfall

**Status:** Accepted
**Date:** 2026-09-05
**Owner:** M
**Spec:** None — no spec stage
**Cross-references:** `docs/adr/README.md`, `docs/adr/ADR-004-durability-policy.md`, `docs/adr/ADR-012-observability.md`, `docs/adr/ADR-015-admission-control.md`, `docs/adr/ADR-028-seal-policy.md`, `docs/adr/ADR-038-obligations.md`, `docs/adr/ADR-039-shed-what-can-be-rerouted.md`, `docs/adr/BACKLOG.md`
**Governs:** `internal/core/durability/**`
**Enforced-by:** `internal/core/durability/shortfall_test.go::TestARestartAndAShortfallAreToldApartByAgeAlone`
**Invalidates:** none — ADR-004 refuses the write and deliberately stops there; `BACKLOG.md` §10 has carried the rest since
**Served-path change:** A leaf that has been short of copies for an hour now produces an obligation naming it, and one short for a second does not. Until now both were invisible.

## Context

ADR-004 refuses a write to a leaf holding fewer than `MinSize` distinct failure
domains. `BACKLOG.md` §10 says why that is only half an answer: *"the leaf is
still readable, still degraded, and still degrading."*

★ **§10's hardest question answers itself once it is stated precisely.** It asks
how an operator distinguishes *"briefly degraded during a restart"* from
*"genuinely short of copies"*, noting the two *"look identical for the first few
seconds and demand opposite responses"*.

They do not merely look identical — **instantaneously they ARE identical.** A leaf
holding two of three domains is holding two of three, whatever the reason. No
richer instantaneous measurement separates them, because there is nothing to
measure: the difference is entirely in what happens NEXT.

⚠ **So the discriminator can only be TIME, and it must be declared rather than
inferred.** A restart resolves in seconds; a shortfall does not. Anything else —
a heuristic on the shape of the loss, a guess from which domains went — is
inventing a signal that is not there.

## Existing Primitives Audit

- `internal/core/durability` (ADR-004): supplies `Policy` and `Satisfied`, which
  counts DISTINCT domains rather than copies. **Reused as the test itself** — this
  record decides what to do with its answer over time, not what the answer is.
- `internal/core/watch` (ADR-038): supplies `Ledger` and `Obligation`. **Reused
  for the second time** — a leaf short of copies is a state, it matters, and
  nobody has dealt with it, which is what an obligation is. See rule 5.
- `internal/core/observe` (ADR-012): supplies the declared vocabulary. **One kind
  added.** ⚠ Distinct from `KindWriteRefusedBelowFloor`, which is about a REFUSED
  WRITE; this is about the leaf's state, and a leaf can be short of copies with
  nobody writing to it at all.
- ADR-028's `ErrNoBound`: **the shape reused for rule 6** — a policy that bounds
  nothing is refused rather than silently doing nothing.
- Automatic re-replication: **deferred**, it needs consensus.

## Decision

**A below-floor leaf is reported and never evicted; the restart-versus-shortfall
distinction is age past a DECLARED grace; and the status is visible throughout
that grace while only the obligation waits.**

1. ⚠ **A leaf below the floor is NOT evicted from the read path.** It is degraded,
   not wrong: its data is readable and correct. ★ Evicting it converts a
   durability problem into an availability outage — strictly worse, and it helps
   nobody, because the copies that remain are exactly the ones still holding the
   data. This is ADR-015's argument again: a shed write is an outage rather than a
   re-route, and so is an evicted leaf.

2. **Below the floor is a STATE with an age, not an event.** The moment of
   crossing is not the useful fact; how long it has stayed crossed is.

3. ★ **Only AGE separates a restart from a shortfall, and the threshold is
   DECLARED.** Instantaneously the two are the same observation. A grace is
   therefore not a tuning knob bolted on — it is the entire discriminator, and it
   must be stated by an operator because only they know what a restart costs in
   their deployment.

4. ⚠ **The STATUS is visible throughout the grace; only the OBLIGATION waits.**
   ★ Otherwise the grace becomes a way to hide a real shortfall for its duration.
   An operator watching a rolling restart wants to SEE the dip — they simply do
   not want to be answerable for it — and those are two different things that a
   single "suppress for N seconds" would conflate.

5. **Past the grace it becomes an ADR-038 obligation.** So it never ages out of
   the report, it sorts oldest-first, and it clears only when somebody says they
   dealt with it — not when the leaf happens to recover, because a leaf that fell
   below its floor and came back is exactly what an operator should still see.

6. **A watchdog with no declared grace is REFUSED.** ⚠ Zero pages on every
   restart and infinity never pages at all, and both look identical to "not
   configured yet". Same shape as ADR-028's `ErrNoBound`.

7. **Whether the cluster RE-REPLICATES automatically is not decided here.** It
   needs consensus (`BACKLOG.md` §19) and it is a different question. ★ The report
   is what makes that choice measurable rather than argued — which is the same
   move ADR-039 made for an all-withdrawn fleet.

**What would falsify this.** A leaf below its floor for longer than the declared
grace that produces no obligation — or one below it for a moment that produces
one. That is the falsifier in `Enforced-by:`, and it is §10's question in its
observable form.

## Alternatives Considered

- **Evict a below-floor leaf from the read path.** It stops serving data the
  cluster cannot promise to keep, and it feels safer. Rejected under rule 1: the
  data is correct and readable, so eviction trades a durability risk for a certain
  outage. It also removes from service exactly the copies that still exist.
- **Distinguish a restart from a shortfall by inspecting WHICH domains went, or
  how many went at once.** It would answer instantly rather than after a grace.
  Rejected under rule 3: a rolling restart and a rack failure can present
  identically, so this invents a signal that is not in the observation. The
  difference is in what happens next, and only time reveals it.
- **Pick a sensible grace as a constant — thirty seconds, say.** No configuration
  needed. Rejected under rule 6: what a restart costs is a property of a
  deployment, and a constant here is a number nobody wrote down that pages the
  wrong deployments and stays silent for the others.
- **Suppress the status during the grace too.** Simpler — one rule instead of
  two. Rejected under rule 4: it turns the grace into a window in which a genuine
  shortfall is invisible, and the operator loses the ability to watch a restart
  dip and recover, which is the thing that tells them the restart was fine.
- **Clear the obligation when the leaf recovers.** It keeps the list short.
  Rejected under rule 5, which is ADR-038 rule 2: a leaf that fell below its floor
  and came back is precisely what somebody should still see, and silence is not
  resolution.
- **Decide automatic re-replication here too.** It would close §10 completely.
  Rejected under rule 7: it needs consensus to decide who acts, and a wrong choice
  is exercised only during the failure that makes it matter.
- **Put the watchdog in `watch` rather than `durability`.** It is an obligation
  producer, so it looks like it belongs there. Rejected: what "below the floor"
  MEANS is `durability`'s, and splitting the test from the policy it tests is how
  the two drift. `admit.Fleet` sits the same way for the same reason.

## Component / Boundary Impact

No new component. `internal/core/durability` gains a watchdog over the answer
`Satisfied` already gives; `internal/core/observe` gains one declared kind.

⚠ The boundary: this decides what a below-floor leaf MEANS over time. It does not
re-replicate, does not route, and does not decide who is woken.

## Wiring & Contract Changes

| Surface | Change | Producer | Consumer(s) |
|---------|--------|----------|-------------|
| `durability.Shortfall` | new — a leaf below its floor, and since when | T1 | operators |
| `durability.Watchdog` / `durability.NewWatchdog` | new — the grace, and what is short | T1 | operators |
| `durability.Watchdog.Observe` / `Status` / `Report` | new | T1 | operators |
| `durability.ErrNoGrace` | new sentinel — rule 6 | T1 | callers |
| `observe.KindLeafBelowFloor` | new declared kind | T1 | operators |

## Inter-task Contracts

| Contract | Producing task | Consuming task(s) | Breaking? |
|----------|----------------|-------------------|-----------|
| `durability.Watchdog` (T1) | T1 | a console or alerting path (`BACKLOG.md` §18/§25) | No |

## Consequences

- **Positive:** §10's hardest question is answered, and answered by admitting that
  no instantaneous measurement could have answered it.
- **Positive:** A real shortfall stops being invisible, and a rolling restart
  stops being noise — with one declared number separating them.
- **Positive:** ADR-038 pays off a second time. A leaf below its floor and an
  all-withdrawn fleet are the same shape, and neither needed a new mechanism.
- **Negative:** ⚠ The grace is a number an operator has to get right. Too long
  hides a shortfall for its duration; too short pages on every restart. Rule 4
  limits the damage of "too long" — the status is visible throughout — but it does
  not eliminate it.
- **Negative:** Nothing feeds the watchdog on a served path. Domain membership
  comes from a cluster that does not exist yet (`BACKLOG.md` §18).
- **Neutral:** Re-replication stays open, and the report is what will let it be
  chosen on evidence.

## Out of Scope

- Re-replicating automatically, and who decides to (deferred: `docs/adr/BACKLOG.md` §19 — it needs consensus, and rule 7 makes the choice measurable first)
- Feeding the watchdog real domain membership (deferred: `docs/adr/BACKLOG.md` §18 — there is no transport)
- Waking anybody (deferred: `docs/adr/BACKLOG.md` §18/§25)
- Choosing a default grace (permanent: boundary: rule 6 — what a restart costs is a property of a deployment, and a constant here would page the wrong deployments and stay silent for the others)
- Measuring how slow a degraded READ is (deferred: `docs/adr/BACKLOG.md` §16 — a different question about the same degradation)

## Risks

| Risk | Likelihood | Impact | Mitigation |
|------|------------|--------|------------|
| A below-floor leaf is evicted from the read path | Med — it feels like the safe response | **Critical** — a durability risk becomes a certain outage, and the copies removed are the ones that still exist | Rule 1 |
| The status is suppressed during the grace, not just the obligation | **High — "suppress for N seconds" is the obvious single rule** | High — a genuine shortfall is invisible for the grace, and an operator cannot watch a restart dip and recover | Rule 4, with a test that reads the status inside the grace |
| A restart is told from a shortfall by something other than time | Med — an instant answer is attractive | High — a rolling restart and a rack failure can present identically, so the signal is invented | Rule 3, and the record says plainly that no instantaneous measurement can do it |
| The grace defaults to a constant | Med | Med — pages the wrong deployments, silent for the others, and nobody wrote the number down | Rule 6, with a named refusal |
| The obligation clears when the leaf recovers | Med — it keeps the list short | High — a leaf that fell below its floor and came back is exactly what should still be seen | Rule 5, which is ADR-038 rule 2 |

## Rollback

Removing the watchdog returns to ADR-004's half-answer: the write is refused and
nothing else happens. ⚠ Nothing depends on it, so it could be quietly allowed to
stop working — which is why rule 4's status is the thing to check, not the
watchdog's existence.

## Follow-ups

- [ ] When there is a cluster (`BACKLOG.md` §18/§19), decide re-replication, and use the report to choose on evidence rather than argument.
- [ ] Revisit rule 3's grace once restarts have been observed: it is the one number here that a deployment can measure and that this record deliberately refuses to guess.
- [ ] When a console exists (`BACKLOG.md` §25), check that it shows the status DURING the grace and not only the obligation after it — rule 4 is undone by a UI that only renders obligations.
