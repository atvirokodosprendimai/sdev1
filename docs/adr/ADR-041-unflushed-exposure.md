# ADR-041: The unflushed window is reported as its PEAK, and bounding it needs both a time and a size

**Status:** Accepted
**Date:** 2026-09-05
**Owner:** M
**Spec:** None — no spec stage
**Cross-references:** `docs/adr/README.md`, `docs/adr/ADR-004-durability-policy.md`, `docs/adr/ADR-012-observability.md`, `docs/adr/ADR-020-commit-point.md`, `docs/adr/ADR-028-seal-policy.md`, `docs/adr/ADR-040-below-the-floor.md`, `docs/adr/BACKLOG.md`
**Governs:** `internal/core/commit/**`
**Enforced-by:** `internal/core/commit/exposure_test.go::TestTheReportedExposureIsThePeakNotTheCalmAfterIt`
**Invalidates:** none — ADR-020 names the window in its own Consequences and leaves it unmeasured; `BACKLOG.md` §23 has carried it since
**Served-path change:** How much acknowledged data is not yet on stable storage is now a number, and it is the number that was true at the worst moment rather than the one that is true when somebody asks.

## Context

ADR-020 acknowledges a write once N memory replicas in distinct power domains
hold it, and flushes afterwards. That is deliberate and it is the whole
performance argument. `BACKLOG.md` §23 names what it leaves: an exposure window
nothing measures or bounds.

★ **§23 also names the counter that does not exist, and it is a different window
from the one that does.** `Gate.Pending` counts entries WRITTEN and not yet
COMMITTED — data this node accepted and promised to nobody. The exposure is
entries COMMITTED and not yet FLUSHED: data somebody WAS promised and could still
lose. ⚠ Two windows, opposite consequences, and only the second is a broken
promise.

⚠ **And §23 names the trap in reporting it:** *"stating the window as an average.
The number that matters is the worst case at the moment the power goes, which
correlates with load — so the exposure is largest exactly when a correlated
failure is most likely, and an average hides that completely."*

★ The instantaneous reading has the same defect one step removed: asked after the
burst has passed, it reports the calm. An operator budgeting for a power event
needs what the exposure REACHED, not what it happens to be while they are looking.

## Existing Primitives Audit

- `internal/core/commit` (ADR-020): supplies `Gate`, `Pending` and the commit
  point. **Extended in the same package** — a second place tracking what is
  committed would drift from the first, and the drift would show only under the
  partial failure this exists to describe.
- `ADR-028`'s `Policy` and `ErrNoBound`: **the shape is reused and the RULE is
  deliberately different.** ADR-028 requires at LEAST ONE bound; this requires
  BOTH. See rule 3 — the difference is argued rather than inherited.
- `internal/core/watch` (ADR-038): **not used here.** An exceeded bound is a
  signal to flush, not something an operator must answer for; making it an
  obligation would page a person for the system's own routine work.
- `internal/core/observe` (ADR-012): **not extended.** Nothing crosses a threshold
  that an operator must see as an event; the exposure is a gauge they read.

## Decision

**The exposure is entries COMMITTED and not yet FLUSHED; it is reported as its
PEAK as well as its present value; bounding it requires BOTH a time and a size;
and exceeding a bound asks for a flush rather than refusing a write.**

1. **The window measured is COMMITTED-and-not-FLUSHED.** ⚠ Distinct from
   `Gate.Pending`, which is written-and-not-committed. The first is a promise that
   could still be broken; the second is data nobody was promised. Reporting one as
   the other would say the system is exposed when it is merely busy, or the
   reverse.

2. ★ **The reported number is the PEAK since the last flush, alongside the
   present value.** §23's trap is an average; the instantaneous reading has the
   same defect one step removed, because asked after a burst it reports the calm.
   The peak is what an operator budgets a power event against.

3. ⚠ **A bound requires BOTH a maximum age and a maximum size, and one alone is
   REFUSED.** ★ This differs from ADR-028 on purpose, and the reason is §23's:
   *"a quiet tenant needs the first while a busy one needs the second"*.
   - Size only: a quiet tenant's single committed entry sits unflushed forever,
     because the size bound is never reached. Unbounded in TIME.
   - Time only: a busy tenant can commit an arbitrary volume inside the interval.
     Unbounded in BYTES.

   Neither alone bounds the exposure, so the pair is the decision — which is
   exactly what §23 says: *"the pair is a decision rather than two constants."*

4. **Exceeding a bound is a SIGNAL TO FLUSH, never a refusal.** ⚠ Refusing a
   write because earlier data is unflushed converts a durability exposure into an
   availability outage — the same trade ADR-040 refuses for a below-floor leaf and
   ADR-015 refuses for a shed write. The node is not unsafe; it is behind.

5. **The peak resets when the window EMPTIES, and on nothing else.** ⚠ Not on
   being read: a gauge that clears when somebody looks reports a different number
   to the second reader, and two operators comparing notes would see a system that
   disagrees with itself. ⚠ And not on a PARTIAL flush, because what it left
   behind is still at risk.

6. ⚠ **A flush is PARTIAL, and that is what makes rule 2 mean anything.** Entries
   committed while a flush runs are still unflushed when it finishes. ★ This was
   found by a mutant that survived: the first design tracked a running total that
   only a full flush could reset, so the window never fell and `Peak` could not
   differ from `Current` by construction — the safeguard could not fail because it
   could not differ from the thing it guarded. A window that only grows until it
   hits zero does not need a peak; a window that drains partially does.

**What would falsify this.** An exposure that rose during a burst and is reported
as the smaller number that is true after it. That is the falsifier in
`Enforced-by:`, and it is §23's trap in its observable form.

## Alternatives Considered

- **Report the average window.** It is the natural summary and it is what a
  metrics system asks for. Rejected under rule 2, in §23's own words: the exposure
  correlates with load, so it is largest exactly when a correlated failure is most
  likely, and an average hides that completely.
- **Report only the instantaneous value.** Simple, and honest about the present.
  Rejected under rule 2: asked after a burst it reports the calm, and an operator
  reading it during an incident learns what is true now rather than what was at
  risk.
- **Reuse `Gate.Pending` as the exposure.** One counter, no new concept. Rejected
  under rule 1: it measures written-and-not-committed, which is data nobody was
  promised. Conflating the two would report a busy node as an exposed one.
- **Require at least one bound, as ADR-028 does.** Consistent with the sealing
  policy next door. Rejected under rule 3: the two are not the same problem. A
  sealing policy with one bound still seals; a flush policy with one bound leaves
  a whole class of tenant unbounded, and which class depends on which bound was
  chosen.
- **Refuse writes once the bound is exceeded, so the exposure cannot grow.** It
  bounds the window absolutely. Rejected under rule 4: it converts a durability
  exposure into an availability outage, and the node is behind rather than unsafe.
- **Raise an ADR-038 obligation when a bound is exceeded.** It would make the
  condition impossible to ignore. Rejected: flushing is the system's own routine
  work, and an obligation is something a PERSON must answer for. Paging somebody
  because a flush is due teaches them to ignore the ledger, which costs more than
  this gains.
- **Clear the peak when it is read.** It gives each reader the peak since they
  last looked. Rejected under rule 5: two operators reading in sequence would get
  different answers about the same window, and the second would be reassured by
  the first one's looking.

## Component / Boundary Impact

No new component. `internal/core/commit` gains a meter over the entries it
already knows are committed.

⚠ The boundary: this MEASURES and BOUNDS. It does not flush — nothing flushes,
because there is no storage engine on the write path yet — and it does not decide
what a flush costs.

## Wiring & Contract Changes

| Surface | Change | Producer | Consumer(s) |
|---------|--------|----------|-------------|
| `commit.Exposure` | new — entries, bytes and age not yet flushed | T1 | operators |
| `commit.Bound` | new — a maximum age AND a maximum size | T1 | callers |
| `commit.Meter` / `commit.NewMeter` | new — the gauge | T1 | operators |
| `commit.Meter.Committed` / `Flushed` / `Current` / `Peak` / `Exceeds` | new | T1 | operators |
| `commit.ErrIncompleteBound` | new sentinel — rule 3 | T1 | callers |

## Inter-task Contracts

| Contract | Producing task | Consuming task(s) | Breaking? |
|----------|----------------|-------------------|-----------|
| `commit.Meter` (T1) | T1 | a flusher and a console (`BACKLOG.md` §12/§25) | No |

## Consequences

- **Positive:** The exposure ADR-020 accepted is now a number, and it is the
  number that was true at the worst moment.
- **Positive:** Rule 3's argument is specific rather than a preference — each
  single-bound policy leaves a named class of tenant unbounded.
- **Negative:** ⚠ **Nothing flushes.** The meter measures a window whose closing
  edge does not exist yet, so today `Flushed` is only ever called by a test. The
  measurement is right and it is not yet measuring anything real, which is stated
  rather than implied.
- **Negative:** The peak is per-meter and in memory, so a restart loses it —
  which is also when the exposure it was describing was realised. ⚠ An operator
  investigating after a power event will not find the peak that mattered; they
  need it exported before the event, not after.
- **Neutral:** No obligation, no event kind. This is a gauge.

## Out of Scope

- Actually flushing anything (deferred: `docs/adr/BACKLOG.md` §12 — there is no storage engine on the write path)
- Choosing the two bound VALUES (permanent: boundary: rule 3 decides that both are required and refuses to invent either; what they should be is a property of a deployment's hardware and its tolerance for loss)
- Exporting the peak so it survives the event it describes (deferred: `docs/adr/BACKLOG.md` §21 — it is the export half, and it is what makes the peak useful after a power event rather than only before one)
- Making an exceeded bound page somebody (permanent: boundary: rule 4 and the alternatives — flushing is routine work, and an obligation is for something a person must answer for)
- Per-tenant exposure (deferred: `docs/adr/BACKLOG.md` §22 — it is the same isolation question the block cache defers)

## Risks

| Risk | Likelihood | Impact | Mitigation |
|------|------------|--------|------------|
| The window is reported as an average | **High — it is what a metrics pipeline asks for** | High — the exposure correlates with load, so an average hides the peak exactly when a correlated failure is most likely | Rule 2, and it is the record's falsifier |
| The instantaneous value is reported alone | High — it is the obvious gauge | High — read after a burst it reports the calm, so an operator learns what is true now rather than what was at risk | Rule 2: the peak is reported alongside it |
| `Pending` is reused as the exposure | Med — it is right there and it counts something similar | High — it measures data nobody was promised, so a busy node reads as an exposed one and a genuinely exposed one reads as calm | Rule 1 |
| One bound is accepted | **High — ADR-028 next door requires only one** | High — size-only leaves a quiet tenant unbounded in time, time-only leaves a busy one unbounded in bytes | Rule 3, with a named refusal and the argument spelled out |
| An exceeded bound refuses writes | Med — it bounds the window absolutely | High — a durability exposure becomes an availability outage, and the node was behind rather than unsafe | Rule 4 |
| The peak clears when read | Med — it seems to give each reader a fresh window | Med — two operators reading in sequence disagree about the same window | Rule 5 |

## Rollback

Removing the meter removes a number, not a behaviour: ADR-020 still acknowledges
on N memory replicas and the window still exists. ⚠ That is exactly why it could
be allowed to rot — nothing breaks when it stops being read, and the first sign
would be a power event nobody could size afterwards.

## Follow-ups

- [ ] When something flushes (`BACKLOG.md` §12), call `Flushed` from it and check rule 5 holds on the real path — a peak that resets anywhere but a flush is a peak that lies.
- [ ] When the stream can be exported (`BACKLOG.md` §21), export the PEAK. In memory it dies with the process, and a power event is exactly the process death that destroys the number describing it.
- [ ] Choose the two bound values once there is hardware to measure against, and record which hardware and on what date — rule 3 requires both and deliberately invents neither.
