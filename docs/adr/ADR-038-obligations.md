# ADR-038: An obligation is a state rather than an event, so it outlives retention and only an acknowledgement clears it

**Status:** Accepted
**Date:** 2026-09-05
**Owner:** M
**Spec:** None — no spec stage
**Cross-references:** `docs/adr/README.md`, `docs/adr/ADR-010-subscribe-and-purge.md`, `docs/adr/ADR-012-observability.md`, `docs/adr/BACKLOG.md`
**Governs:** `internal/core/watch/**`
**Enforced-by:** `internal/core/watch/watch_test.go::TestAMonthOldObligationIsStillReported`
**Invalidates:** none — it fills `BACKLOG.md` §21's "watching" bullet, which ADR-010 deferred and ADR-012 could express but not answer
**Served-path change:** An incomplete purge is now remembered and reportable, oldest first. Until now ADR-010 recorded one, ADR-012 could emit an event about it, and nothing kept it.

## Context

ADR-010 leaves a purge INCOMPLETE when a sink has not acknowledged. It
deliberately does not escalate, and defers escalation to `BACKLOG.md` §21.
ADR-012 gave that a declared event kind, `subscribe.purge-incomplete`, with a
named reader. Nothing looks.

⚠ **§21 calls that "the one that matters" and says why: it is the exact failure
ADR-012 exists to prevent, one level up.** A declared thing whose reader exists on
paper and never runs. So the bar it sets is not "declare a watcher" — it is
*"whether an incomplete purge from a month ago would actually reach a person"*.

★ **Meeting that bar rules out the obvious design.** A watcher over the event
stream is what one reaches for, and it fails the test in three separate ways:

- ⚠ **The stream DROPS by design.** ADR-012 made a full buffer drop rather than
  block, because observability that can stall the thing it observes is worse than
  none. So the one event that mattered is exactly the one that can be lost — and
  it is most likely lost under load, which is when purges go incomplete.
- ⚠ **The stream is a stream.** An event announces something; it does not persist
  it. A month-old event scrolled past a month ago.
- ⚠ **Retention would silently resolve it.** §21 says retention should reuse
  ADR-010's `Horizon` rather than growing a second notion — correct, and it hides
  a trap: applying a horizon to the OBLIGATION rather than to the LOG means an
  incomplete purge quietly stops being reported at the retention boundary. The
  system would then answer "nothing is outstanding" precisely because the problem
  got old, which is the opposite of what age should do.

★ **The resolution: an incomplete purge is a STATE, not an event.** The event
announces it. The state persists until somebody says they dealt with it.

## Existing Primitives Audit

- `internal/core/observe` (ADR-012): supplies `Kind`, the declaration registry and
  the drop-counting `Stream`. **Reused for identity, NOT as the carrier** — see
  rule 5. A `Kind` names what an obligation is about; the stream does not deliver
  it.
- `internal/core/subscribe` (ADR-010): supplies `PurgeResult`, `PurgeIncomplete`
  and `Outstanding`, which already names WHO to chase. **Reused unchanged** — the
  data was always there and nothing kept it.
- `subscribe.Horizon` (ADR-010): **referenced and deliberately NOT applied to the
  obligation set.** See rule 3; that is the trap.
- `internal/core/tx` (ADR-002): supplies ordering. **Not used** — an obligation is
  ordered by wall-clock age because age is what an operator reads, and a
  transaction identifier answers a different question.
- A general alerting or notification system: **none.** This decides what is
  outstanding and how it is reported; who is woken is a deployment concern.

## Decision

**An obligation is raised by the emitter, cleared only by an acknowledgement,
reported oldest-first with its true age, and never aged out by retention.**

1. **An OBLIGATION is a state: something happened, it matters, and nobody has
   dealt with it.** The event is the announcement; the obligation is the thing
   that survives it.

2. **Only an explicit ACKNOWLEDGEMENT clears one, and it names who and when.**
   ⚠ Not time, not a retry, not the condition appearing to go away. An operator
   saying "I dealt with this" is the only thing that means it was dealt with.

3. ⚠ **Retention bounds the LOG and NEVER the obligation set.** ★ This is the
   trap §21 walks into by being right about reusing `Horizon`. Under a thirty-day
   horizon, a thirty-one-day-old incomplete purge must still be reported — the
   system must not answer "nothing is outstanding" because the problem got old.
   That is the falsifier in `Enforced-by:`.

4. **Outstanding obligations are reported OLDEST FIRST, with their age.**
   ★ Because §21's test is whether it *reaches a person*, and an alert that fired
   once and scrolled away does not. Age is the signal, so the oldest unanswered
   thing is at the top of the list every time anybody looks.

5. ⚠ **An obligation is raised by the EMITTER, never derived from the event
   stream.** The stream drops by design (ADR-012) and any future sampling would
   drop more, so a stream-fed ledger loses exactly the events that matter, under
   exactly the load that produces them. ★ This also settles §21's sampling
   warning for this path rather than by argument: an obligation never travels on
   the stream, so no sampling or drop policy can lose one.

6. ⚠ **Raising the same obligation twice keeps the FIRST raised time.** A purge
   that retries daily and fails daily must not look one day old forever. ★ Age is
   the whole signal, so anything that resets the clock disables the mechanism
   while leaving it apparently working — the worst available failure, and the one
   a retry loop produces by default.

7. **An obligation carries what an operator needs to ACT: what happened, about
   what, and who is outstanding.** ADR-010 already computes the last of these; it
   is carried through rather than recomputed or dropped.

**What would falsify this.** An obligation raised a month ago, under a thirty-day
retention horizon, that is not reported — or is reported as a day old because
something re-raised it. That is the falsifier in `Enforced-by:`, and it is exactly
the question §21 says to ask.

## Alternatives Considered

- **A watcher over the event stream.** The obvious design, and the one §21's
  wording invites. Rejected under rule 5: the stream drops by design, so it loses
  the events that matter under the load that produces them; it does not persist,
  so a month-old event is gone; and it would need its own retention, which is
  rule 3's trap.
- **Apply `Horizon` to the obligation set, for consistency with the log.**
  Consistent, and one retention notion rather than two. Rejected under rule 3:
  it makes an old problem stop being reported BECAUSE it is old, and the system
  then answers "nothing outstanding" with a straight face. Reusing the horizon for
  the log is right; reusing it here inverts what age means.
- **Clear an obligation when the condition stops recurring.** It removes the
  acknowledgement step and is how most alerting works. Rejected under rule 2: a
  purge that stopped being retried is not a purge that completed, and the two are
  indistinguishable from outside. Silence is not resolution.
- **Re-raise with a fresh timestamp so the newest occurrence is visible.** It
  keeps the report current. Rejected under rule 6: a daily retry then looks one
  day old forever, so the mechanism silently stops working while continuing to
  produce output.
- **Report newest first, like a log.** Familiar. Rejected under rule 4: the whole
  question is whether an OLD unanswered thing reaches somebody, and newest-first
  buries it further every day.
- **Make an obligation a datom in the reserved tenant, like ADR-033's grants.**
  Tempting, and it would make obligations durable and bitemporal for free.
  Rejected here as a coupling this record cannot justify yet: a purge obligation
  is about the store's own operation rather than about tenant data, and making the
  watchdog depend on the thing it watches is a failure mode of its own. ⚠ Revisit
  when durability is added — see the follow-up, and see the gap in Consequences.

## Component / Boundary Impact

One new component, `internal/core/watch`, owning one thing: what is outstanding.

⚠ The boundary, and it is why this is not part of `observe`: `observe` decides
what a component may SAY, and this decides what an operator must ANSWER FOR. They
have different reasons to change, and folding the second into the first is how the
ledger would end up fed by the stream — which rule 5 refuses.

It decides nothing about who is woken, how, or how loudly. That is a deployment
concern and it needs a transport that does not exist.

## Wiring & Contract Changes

| Surface | Change | Producer | Consumer(s) |
|---------|--------|----------|-------------|
| `watch.Obligation` | new — what is outstanding, and about what | T1 | operators |
| `watch.Outstanding` | new — an obligation plus its age | T1 | operators |
| `watch.Ledger` / `watch.NewLedger` | new — the set of outstanding obligations | T1 | `subscribe`, operators |
| `watch.Ledger.Raise` / `Acknowledge` / `Outstanding` / `Len` | new | T1 | operators |
| `watch.ErrNoSubject` / `watch.ErrNotOutstanding` | new sentinels | T1 | callers |
| `watch.FromPurge` | new — an incomplete purge as an obligation | T1 | `subscribe` callers |

## Inter-task Contracts

| Contract | Producing task | Consuming task(s) | Breaking? |
|----------|----------------|-------------------|-----------|
| `watch.Ledger` (T1) | T1 | a console or an alerting path (`BACKLOG.md` §21/§18) | No |

## Consequences

- **Positive:** §21's actual test can be answered: a month-old incomplete purge is
  reported, oldest first, with its true age.
- **Positive:** Rule 5 makes the sampling warning §21 raises moot for obligations
  — they never travel on a stream that can drop or sample them.
- **Positive:** ADR-010 already computed who was outstanding, and it was being
  thrown away. Nothing new had to be discovered, only kept.
- **Negative:** ⚠ **The ledger is IN MEMORY, so a restart loses it.** Rule 2 says
  time and retention do not clear an obligation, and a restart currently does —
  which means the honest reading of §21's test today is "a month-old obligation
  reaches a person, in a process that has been up a month". That is a real gap, it
  is named in the follow-ups, and it is not a design.
- **Negative:** An acknowledgement is a human act, so obligations accumulate if
  nobody acts. That is the intent — the alternative is a system that resolves its
  own problems by forgetting them — but it means the list is only useful if
  someone reads it.
- **Neutral:** Nothing wakes anybody. This decides what is outstanding, not who
  hears about it.

## Out of Scope

- Making the ledger survive a restart (deferred: `docs/adr/BACKLOG.md` §12 — the gap named in Consequences; a store exists now, so this is a real next step rather than a blocked one)
- Waking a person: paging, email, a console (deferred: `docs/adr/BACKLOG.md` §18/§25 — there is no transport and no surface)
- Exporting the event stream to an external metrics system (deferred: `docs/adr/BACKLOG.md` §21 — choosing a format before a transport or a consumer would be choosing on no information)
- Sampling and aggregating the stream itself (deferred: `docs/adr/BACKLOG.md` §21 — rule 5 removes the risk for OBLIGATIONS; the stream's own sampling is untouched and still needs the separate drop and sample counts §21 names)
- Applying retention to the log (deferred: `docs/adr/BACKLOG.md` §21 — §21 is right that it should reuse ADR-010's `Horizon`; rule 3 only says it must not reach the obligation set)
- Deciding what else raises an obligation beyond an incomplete purge (permanent: boundary: an obligation is raised where the condition is detected, so each is its own decision by the record that owns that condition)

## Risks

| Risk | Likelihood | Impact | Mitigation |
|------|------------|--------|------------|
| A horizon is applied to the obligation set | **High — §21 says to reuse `Horizon`, and applying it everywhere looks consistent** | **Critical** — an old problem stops being reported because it is old, and the system reports "nothing outstanding" | Rule 3, and it is the record's falsifier: age past the horizon and it must still appear |
| The ledger is fed from the event stream | High — it is the obvious design and the stream is right there | Critical — the stream drops by design, so the lost events are the ones that mattered, lost under the load that caused them | Rule 5, with a test that raises obligations while the stream is dropping |
| A retry resets an obligation's age | High — re-raising on each attempt is the natural implementation | **Critical** — a daily retry looks one day old forever, so the mechanism stops working while still producing output | Rule 6, with a test that re-raises and checks the age |
| An obligation is cleared by the condition ceasing | Med — it is how most alerting works | High — a purge nobody retried is indistinguishable from one that completed | Rule 2: only an acknowledgement clears one |
| Reported newest-first | Med — every log is | Med — the old unanswered thing is buried further every day, which is the failure §21 describes | Rule 4 |
| The restart gap is forgotten | Med — the tests all pass within one process | High — the record's own claim is only true for an uninterrupted process | Named in Consequences and in a follow-up rather than left implicit |

## Rollback

Removing the ledger returns to today's behaviour: ADR-010 reports an incomplete
purge, ADR-012 can emit an event about it, and nothing keeps it. ⚠ Nothing depends
on the ledger, so removing it loses information rather than breaking anything —
which is also the reason it could be quietly allowed to stop working, and why rule
4's report is the thing to check rather than the ledger's existence.

## Follow-ups

- [ ] Make the ledger durable (`BACKLOG.md` §12). Until then rule 2's "not a restart" is a rule the implementation does not yet satisfy, and the record says so rather than implying otherwise. Revisit the rejected "obligation as a datom" alternative at the same time — the objection was coupling the watchdog to what it watches, and that trade looks different once the alternative is losing the ledger on every restart.
- [ ] When a console or an alerting path exists (`BACKLOG.md` §18/§25), check rule 4 against it: oldest-first is only useful if what reads the ledger preserves the order, and a UI that sorts by recency would undo this record without changing a line of it.
- [ ] When stream sampling is added (`BACKLOG.md` §21), keep the sampled count and the dropped count SEPARATE, as §21 warns — this record removes that risk for obligations and leaves it entirely open for the stream.
