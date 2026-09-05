# ADR-042: A skewed clock is refused BEFORE it is absorbed, because absorbing it cannot be undone

**Status:** Accepted
**Date:** 2026-09-05
**Owner:** M
**Spec:** None — no spec stage
**Cross-references:** `docs/adr/README.md`, `docs/adr/ADR-002-transaction-identity.md`, `docs/adr/ADR-009-fenced-leases.md`, `docs/adr/ADR-012-observability.md`, `docs/adr/ADR-015-admission-control.md`, `docs/adr/ADR-040-below-the-floor.md`, `docs/adr/BACKLOG.md`
**Governs:** `internal/core/hlc/**`
**Enforced-by:** `internal/core/hlc/admission_test.go::TestARefusedRemoteLeavesTheClockUntouched`
**Invalidates:** none — `Clock.Merge`'s own comment says refusing a skewed remote "is a cluster-level policy and is not decided here"; `BACKLOG.md` §4 has carried it since
**Served-path change:** A remote timestamp far in the future can now be refused instead of adopted. Until now the only path took it, permanently.

## Context

`BACKLOG.md` §4 says a hybrid logical clock *"does move forward to match the
fastest clock it hears from. A node whose wall clock jumps hours ahead drags
every timestamp it touches with it, permanently — the cluster cannot come back,
because monotonicity is the property that forbids it."*

`Clock.Merge` says the same thing about itself, in its own comment: *"a remote
wall reading far in the future is adopted and, being monotonic, is never given
back."*

★ **That irreversibility is not an inconvenience around the decision — it IS the
decision.** If absorbing a bad timestamp cannot be undone, then a check performed
after absorbing is not a check. Everything else here follows from where the check
must go.

§4 asks three things: the maximum skew, how it is measured, and what happens past
it. ⚠ **The maximum is the only one that needs deployment data.** The other two are
answerable now, and the first is answerable in shape if not in value.

## Existing Primitives Audit

- `internal/core/hlc` (ADR-002): supplies `Timestamp`, `Clock`, `Merge`.
  **Extended, and `Merge` is kept** — see rule 4, which is the distinction this
  record would have got wrong.
- `internal/core/tx` (ADR-002): supplies `Minter.Observe`, the one production
  caller of `Merge`. **Its call is history read back from storage**, so rule 4
  leaves it alone.
- `internal/core/observe` (ADR-012): supplies the declared vocabulary. **One kind
  added** — a refusal nobody can see is a refusal nobody will fix.
- `internal/core/watch` (ADR-038): **not used**, deliberately. One refusal is a
  transient; a persistently skewed node is an obligation, and that needs the grace
  machinery ADR-040 built. Recorded as a follow-up rather than built a third time.
- NTP, or any external time authority: **out of scope.** This bounds what the
  cluster will ACCEPT; keeping clocks correct is an operational concern.

## Decision

**Skew is checked before absorption, measured by the receiver, and a message past
the bound is refused while the node is not.**

1. ⚠ **The check happens BEFORE the merge, and the clock is untouched when it
   fails.** ★ Monotonicity is the property that makes a merge irreversible, so a
   check afterwards is not a check — it is a report of damage already done. That
   is the falsifier in `Enforced-by:`.

2. ★ **Skew is measured by the RECEIVER, against its own wall reading. It is
   never self-reported.** A node that cannot be trusted about the time cannot be
   trusted about its own error, and asking it is asking the suspect to testify.
   ⚠ The receiver already has to compare the two readings in order to merge, so
   this costs nothing it was not doing.

3. ⚠ **And the honest limit: this measures the DIFFERENCE between two clocks, not
   either one's error.** A receiver whose own clock is wrong will refuse correct
   peers, and will do it confidently. That is a limit of the approach rather than
   of this implementation — the same shape as ADR-004's declared-domain limit —
   and it is stated rather than mitigated.

4. ★ **The bound applies to a timestamp arriving from another NODE, never to one
   read back from durable storage.** ⚠ This is the distinction that would have
   been got wrong: `Minter.Observe` merges timestamps rehydrated from a leaf, and
   refusing those would make committed data unreadable. Storage is
   already-accepted history — whatever skew it carries has already happened, and
   refusing it now punishes the reader rather than the writer. So `Merge` stays,
   and `Admit` is the network-boundary path.

5. **A message past the bound is REFUSED; the node is not evicted.** ⚠ Third time
   this corpus makes this trade, and here there is an extra reason: a node with a
   skewed clock is otherwise healthy. Its data is correct and its storage is fine;
   only its timestamps are wrong. Evicting it loses a working replica over a
   clock.

6. **A bound must be declared, and a non-positive one is refused.** ★ The VALUE
   needs deployment data — a datacentre and a WAN tolerate different skew — and
   this record deliberately invents neither, exactly as ADR-040 refuses to invent
   a grace and ADR-041 refuses to invent its two constants.

7. **A refusal is observable.** ⚠ A refusal nobody can see is a refusal nobody
   will fix, and the symptom — writes from one node quietly not landing — looks
   like a network problem from every other angle.

**What would falsify this.** A remote timestamp beyond the bound that changes the
local clock. That is the falsifier in `Enforced-by:`: not that the call returns an
error, but that `Last()` is byte-identical afterwards — because the damage is the
absorption, not the return value.

## Alternatives Considered

- **Check the skew after merging and alarm on it.** Simpler, and it needs no new
  path. Rejected under rule 1: the merge is irreversible by construction, so this
  reports damage rather than preventing it. The cluster cannot come back.
- **Trust the sender's own report of its skew.** It is the only party that knows
  its NTP state. Rejected under rule 2: a node whose clock is wrong is exactly the
  node whose self-assessment is wrong, and a node deliberately misreporting is
  indistinguishable from one that is merely broken.
- **Apply the bound to every merge, including timestamps read from storage.**
  Consistent, one rule, no exception to remember. Rejected under rule 4: it makes
  a leaf written by a formerly-skewed node permanently unreadable, which converts
  a clock problem into data loss — and the skew it would be refusing already
  happened.
- **Evict a node past the bound.** It stops the problem spreading. Rejected under
  rule 5: the node's data is correct and its storage is fine. Refusing its
  messages already stops the spread, and eviction additionally loses a working
  replica.
- **Pick a default bound — five seconds, say.** It would make the mechanism work
  out of the box. Rejected under rule 6: a datacentre and a WAN tolerate different
  skew, so a default is a number nobody chose that is wrong somewhere.
- **Reject silently, since the sender will retry.** Less noise. Rejected under
  rule 7: writes from one node quietly failing looks like a network problem from
  every other angle, and the one fact that would identify it is the one being
  suppressed.
- **Raise an ADR-038 obligation on every refusal.** It makes the condition
  impossible to ignore. Rejected as too eager: one refusal is a transient, and a
  persistently skewed node is the obligation. That needs ADR-040's grace, and it
  is a follow-up rather than a third copy of the same watchdog.

## Component / Boundary Impact

No new component. `internal/core/hlc` gains an admission path beside its merge
path, and `internal/core/observe` gains one kind.

⚠ The boundary: this decides what the cluster will ACCEPT. It does not keep
clocks correct, does not measure a node's true error, and does not decide who
gets told.

## Wiring & Contract Changes

| Surface | Change | Producer | Consumer(s) |
|---------|--------|----------|-------------|
| `hlc.Bound` | new — the maximum skew a remote may exhibit | T1 | callers |
| `hlc.Skew` | new — the measured difference, and its direction | T1 | callers |
| `hlc.ErrSkewTooLarge` / `hlc.ErrNoBound` | new sentinels | T1 | callers |
| `hlc.Clock.Admit` | new — check, then merge; refuses without touching the clock | T1 | a transport (`BACKLOG.md` §18) |
| `hlc.Clock.Merge` | unchanged, and its comment now says WHEN it is the right one | T1 | `tx.Minter` |
| `observe.KindClockSkewRefused` | new declared kind | T1 | operators |

## Inter-task Contracts

| Contract | Producing task | Consuming task(s) | Breaking? |
|----------|----------------|-------------------|-----------|
| `hlc.Clock.Admit` (T1) | T1 | a transport (`BACKLOG.md` §18) | No — `Merge` keeps its signature, so `tx.Minter` is untouched |

## Consequences

- **Positive:** The propagation path `Merge`'s own comment warns about now has a
  gate, and the gate is in the only place that can work.
- **Positive:** `Merge` keeps its signature, so the one production caller —
  rehydrating history from a leaf — is untouched, which is also the correct
  behaviour rather than a convenience.
- **Negative:** ⚠ **Nothing calls `Admit`.** There is no transport, so the
  enforcement point arrives with `BACKLOG.md` §18. This decides the rule and the
  signature, as ADR-033 did for authorization. Recorded rather than implied.
- **Negative:** ⚠ Rule 3's limit is real: a receiver with a bad clock refuses
  correct peers. In a cluster where one node is wrong this is right; in one where
  the majority is wrong it is exactly backwards, and nothing here can tell those
  apart.
- **Negative:** A skewed node is refused per message, so a persistently skewed one
  produces a stream of refusals and no single durable fact. That is the follow-up.
- **Neutral:** No default bound, by rule 6.

## Out of Scope

- Choosing the bound VALUE (permanent: boundary: rule 6 — a datacentre and a WAN tolerate different skew, so any constant here is wrong somewhere)
- Keeping clocks correct (permanent: boundary: this bounds what the cluster accepts; NTP and its ilk are an operational concern outside the code)
- Deciding a node's TRUE clock error, as opposed to its difference from the receiver's (permanent: fact: two clocks can only measure their disagreement, and identifying which is wrong needs a third party this system does not have; citation: file `internal/core/hlc/hlc.go:26`)
- Making a persistently skewed node an ADR-038 obligation (deferred: `docs/adr/BACKLOG.md` §4 — it needs ADR-040's grace, since one refusal is a transient and only a sustained one is somebody's problem)
- Calling `Admit` from anywhere (deferred: `docs/adr/BACKLOG.md` §18 — there is no transport)
- Evicting or fencing a skewed node (deferred: `docs/adr/BACKLOG.md` §19 — ADR-009 fences on epoch, which is a different question from a clock)

## Risks

| Risk | Likelihood | Impact | Mitigation |
|------|------------|--------|------------|
| The skew is checked after the merge | **High — it is the easier place to put it, and it reads as equivalent** | **Critical** — the merge is irreversible, so the check reports damage instead of preventing it and the cluster cannot come back | Rule 1, and the falsifier asserts the clock is UNCHANGED rather than that an error was returned |
| The bound is applied to timestamps read from storage | High — one rule with no exception looks cleaner | **Critical** — a leaf written by a formerly-skewed node becomes permanently unreadable, turning a clock problem into data loss | Rule 4, with `Merge` kept and its doc saying when each is right |
| The sender's self-reported skew is trusted | Med — it is the only party with its NTP state | High — the node whose clock is wrong is the node whose self-assessment is wrong | Rule 2 |
| A node past the bound is evicted | Med — it stops the spread | High — its data is correct and its storage is fine; refusing its messages already stops the spread | Rule 5 |
| A default bound is invented | Med | Med — wrong somewhere, and nobody chose it | Rule 6, with a named refusal |
| Refusals are silent | Med | High — writes from one node quietly failing looks like a network problem from every other angle | Rule 7 |

## Rollback

Removing `Admit` returns to today: `Merge` takes whatever it is given. ⚠ Nothing
calls `Admit` yet, so removing it breaks nothing — which is also why it could be
quietly left uncalled once a transport exists. The follow-up is where that gets
checked.

## Follow-ups

- [ ] When a transport exists (`BACKLOG.md` §18), call `Admit` at the network boundary and confirm rule 4 survives the wiring — the tempting version applies the bound to every merge, including the storage path, and that one turns a clock problem into data loss.
- [ ] Make a PERSISTENTLY skewed node an ADR-038 obligation, using ADR-040's grace: one refusal is a transient and only a sustained one is somebody's problem.
- [ ] Revisit rule 3 if a third party ever exists to arbitrate. Two clocks can only measure their disagreement, and a majority-wrong cluster is the case this record gets exactly backwards.
