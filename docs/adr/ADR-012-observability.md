# ADR-012: Every component emits one event shape, and a counter that nothing reads is a defect

**Status:** Accepted
**Date:** 2026-09-04
**Owner:** M
**Spec:** None — no spec stage
**Cross-references:** `docs/adr/README.md`, `docs/adr/ADR-004-durability-policy.md`, `docs/adr/ADR-008-prefix-routing.md`, `docs/adr/ADR-010-subscribe-and-purge.md`, `docs/adr/ADR-019-chaos-and-the-failure-catalogue.md`, `docs/adr/BACKLOG.md`
**Governs:** `internal/core/observe/**`
**Enforced-by:** `internal/core/observe/observe_test.go::TestEveryEmittedEventIsDeclared`
**Served-path change:** An operator sees why a request went where it went — the route it took, the durability it was accepted at, and the purges still outstanding — instead of inferring it from logs.

## Context

Every record in this corpus produces something an operator needs and none of
them says how it is seen. ADR-008's routing and placement disagree during a
repair and that disagreement is the most useful thing about it. ADR-004 refuses
writes below a floor and an operator needs to know which leaves are refusing.
ADR-010 leaves purges INCOMPLETE and nothing escalates them — its own record says
so and defers the escalation here.

Two things could go wrong, and both are the normal outcome.

**An event stream with no declared vocabulary becomes a log.** If any component
may emit any shape, a consumer has to pattern-match strings, every producer
invents its own field names, and the console becomes a grep. The cost is not
felt while there are three producers; it is unpayable at thirty.

**A counter nobody reads is worse than no counter.** It costs emission on a hot
path, it makes a dashboard look thorough, and it answers no question. ⚠ The
failure mode is specific: somebody adds a metric while building a feature,
nothing ever consults it, and it survives forever because deleting it looks
risky. Every corpus this size has dozens.

⚠ **And there is no transport, so nothing can ship an event anywhere.** The
vocabulary, the emission and the checks on both are decidable now; a console and
a wire format are not.

## Existing Primitives Audit

- `internal/core/subscribe` (ADR-010): supplies the subscription primitive, and
  ADR-010 already names the console as one of its three consumers. **Reused
  whole** — an event stream is a subscription, not a second delivery mechanism.
- `internal/core/tx` (ADR-002): supplies the ordering an event carries. **Reused**
  so an event is orderable against the data it describes rather than against wall
  time.
- `internal/core/addr` (ADR-001): supplies `LeafID`, which most events are about.
  **Reused whole.**
- A metrics library: **none adopted.** The decision here is what may be emitted
  and what proves it is read; a library decides neither, and adopting one now
  would fix an export format before there is anything to export to.

## Decision

**One declared vocabulary, and every counter has a named reader.**

1. **An event has one shape**: a kind from a closed set, the leaf it concerns,
   the transaction identifier that orders it, and typed fields. There is no
   free-form message.

2. **The set of kinds is CLOSED and declared in one place.** Emitting an
   undeclared kind is refused at registration, not at read time, so a
   vocabulary drift fails at startup rather than becoming a consumer's problem.

3. **Every declared kind names its READER** — the console panel, the alert, the
   catalogue entry that consumes it. ★A kind with no reader is refused. This is
   the whole of rule 2's value: without it a closed vocabulary just becomes a
   long list of things nobody looks at.

4. **A counter is declared with what question it answers.** Not a description of
   what it counts — that is the name — but the operator question it exists to
   settle. ⚠ A counter whose question cannot be written is a counter nobody
   needs, and writing the question is where that becomes obvious.

5. **The event stream is a subscription**, ADR-010's primitive, with the console
   as one sink among three. A second delivery mechanism would drift from the
   first and would need its own purge story.

6. **Emission never fails a request.** A full buffer drops events and increments
   a dropped counter; it does not block, and it does not error into the caller.
   ★Observability that can take down the thing it observes is worse than none,
   and this is the failure that actually happens.

7. **Dropped events are counted and the count is readable.** A stream that
   silently loses events under load is lying exactly when it matters most, so the
   drop count is itself a declared counter with a named reader.

**What would falsify this.** An event emitted whose kind is not declared, or a
declared kind with no reader. Both are checked by the falsifier named in
`Enforced-by:`, and both are checkable today with no console.

## Alternatives Considered

- **Free-form structured logging.** Universal, zero design cost, every library
  supports it. Rejected under rule 1: with no declared vocabulary a consumer
  pattern-matches strings, every producer invents its own field names, and the
  console becomes a grep. Cheap at three producers and unpayable at thirty.
- **A closed vocabulary with no reader requirement.** Half the discipline for a
  quarter of the argument. Rejected: it produces a long, tidy list of events
  nobody reads, which looks like observability and is not.
- **Adopt a metrics library and its conventions.** Mature, and solves export.
  Rejected for now: the decision is what may be emitted and what proves it is
  consumed, and a library decides neither. Choosing an export format before
  there is anything to export to is choosing on no information.
- **Block the caller when the event buffer is full.** No events are lost, and the
  stream is authoritative. Rejected under rule 6: it makes the observability path
  able to stall the served path, which is the failure that actually happens and
  the one nobody expects.
- **Drop events silently when full.** Simplest, and what most implementations do.
  Rejected under rule 7: a stream that loses events without saying so is
  misleading exactly under the load an operator is investigating.

## Component / Boundary Impact

One new component, `internal/core/observe`, owning the vocabulary, the counters
and their declarations. It has one reason to change: what a component may say.

⚠ The boundary: it decides what may be emitted and proves it is consumed. It does
not decide what to DO about anything — shedding load is ADR-015's, refusing a
write is ADR-004's — and it does not render anything. A component that also acted
would make every emission a control decision.

## Wiring & Contract Changes

| Surface | Change | Producer | Consumer(s) |
|---------|--------|----------|-------------|
| `observe.Kind` | new — a declared event kind from a closed set | T1 | every component |
| `observe.Event` | new — kind, leaf, transaction, typed fields | T1 | the console sink |
| `observe.Declaration` | new — a kind, its reader, and its fields | T1 | the vocabulary check |
| `observe.Register` | new — refuses an undeclared kind and a reader-less declaration | T1 | every component |
| `observe.Counter` | new — a counter and the operator question it answers | T2 | the console |
| `observe.Stream` | new — non-blocking emission with a dropped count | T2 | ADR-010's sinks |
| `observe.ErrUndeclaredKind` / `ErrNoReader` / `ErrNoQuestion` | new sentinels | T1, T2 | callers |

## Inter-task Contracts

| Contract | Producing task | Consuming task(s) | Breaking? |
|----------|----------------|-------------------|-----------|
| `observe.Kind`, `observe.Event`, `observe.Declaration`, `observe.Register` | T1 | T2 | No — T2 is written against T1 |
| `observe.Counter`, `observe.Stream` | T2 | none yet | No |

## Implementation

Two tasks, sequential. See `docs/adr/ADR-012-observability/tasks/README.md`.

## Consequences

- **Positive:** A consumer reads typed fields rather than parsing messages, and a
  new event kind is a declaration rather than a convention.
- **Positive:** A counter that nobody reads cannot be added, so the usual drift
  towards a dashboard of unread numbers is closed at the point it starts.
- **Positive:** The stream cannot stall a request, and cannot lose events
  silently.
- **Negative:** Adding an event costs a declaration and a named reader, which is
  friction on exactly the path where a developer wants none. That friction is the
  mechanism.
- **Negative:** A closed vocabulary means an unforeseen event cannot be emitted
  ad hoc during an incident, which is when people most want to. The answer is to
  declare it and ship, and that is a real cost.
- **Neutral:** Nothing renders anything. The console is a sink over ADR-010's
  subscription and needs a transport.

## Out of Scope

- Rendering a console, and the transport an event travels over (deferred: `docs/adr/BACKLOG.md` §18)
- Deciding what to do about what is observed (permanent: boundary: ADR-015 owns shedding and ADR-004 owns refusing a write; a component that observed AND acted would make every emission a control decision)
- Exporting to any external metrics system (deferred: `docs/adr/BACKLOG.md` §21)
- Sampling, aggregation windows and retention of the event stream (deferred: `docs/adr/BACKLOG.md` §21)
- Escalating a purge that stays incomplete, which ADR-010 deferred here (deferred: `docs/adr/BACKLOG.md` §21 — the vocabulary can express it, but nothing yet watches)

## Risks

| Risk | Likelihood | Impact | Mitigation |
|------|------------|--------|------------|
| Counters accumulate that nobody reads | High — it is the normal outcome | Med — a dashboard that looks thorough and answers nothing | Every counter declares the operator question it settles, and a declaration without one is refused; writing the question is where a useless counter becomes obvious |
| The vocabulary drifts as components are added | High | High — the console becomes a grep | The kind set is closed and registration refuses an undeclared kind, so drift fails at startup rather than at read time |
| Emission stalls or fails a request under load | Med | Critical — observability takes down what it observes | Emission is non-blocking by construction and drops rather than blocking; the test fills the buffer and asserts the caller is unaffected |
| Events are dropped silently, exactly under the load being investigated | High without rule 7 | High | Drops are counted, and the drop counter is itself declared with a reader |

## Rollback

No persistent state, so a revert is a code revert. The declarations are the part
with a cost: once components emit a declared vocabulary, removing the declaration
requirement means every consumer goes back to pattern-matching, and the
vocabulary decays quickly once it is optional.

## Follow-ups

- [ ] When a transport exists (`BACKLOG.md` §18), confirm the console is a sink over ADR-010's subscription rather than a second delivery path — a second one would need its own purge story and would drift from the first.
- [ ] When ADR-015 lands, confirm shedding DECISIONS are emitted as events and that the shedding logic reads counters rather than duplicating them; two counts of one thing diverge.
- [ ] Watch purges that stay incomplete (`BACKLOG.md` §21). ADR-010 deferred the escalation here and this record can express it, but nothing yet looks — which is the same "declared and unread" failure this record is about, one level up.
