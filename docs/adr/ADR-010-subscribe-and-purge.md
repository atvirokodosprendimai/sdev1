# ADR-010: One subscription primitive, and a purge that is a fan-out with per-sink acknowledgement

**Status:** Accepted
**Date:** 2026-09-04
**Owner:** M
**Spec:** None — no spec stage
**Cross-references:** `docs/adr/README.md`, `docs/adr/ADR-005-segment-format.md`, `docs/adr/ADR-006-erasure-coding.md`, `docs/adr/ADR-007-crypto-shredding.md`, `docs/adr/ADR-017-lock-free-read-path.md`, `docs/adr/FAILURES.md`, `docs/adr/BACKLOG.md`
**Governs:** `internal/core/subscribe/**`
**Enforced-by:** `internal/core/subscribe/purge_test.go::TestPurgeIsIncompleteWhileASinkHasNotAcknowledged`
**Invalidates:** none — checked; ADR-007 decided how a subject becomes unreadable and left who else must be told to this record
**Served-path change:** An operator who purges a subject is told which sinks have acknowledged it and which have not, instead of receiving a success that means only "the primary copy is gone".

## Context

Three different things get called "delete", and treating them as synonyms is how
data an operator believes is gone comes back.

**Marking** makes a subject invisible to queries. It is immediate and cheap, and
it changes nothing about the bytes: anyone holding them still has them. It is
what most systems mean by a delete.

**Shredding** destroys the key, which ADR-007 built. It is immediate and
irreversible, and it is the only one of the three that is erasure — it reaches
coded stripes, offline replicas and backups without visiting any of them.

**Sweeping** reclaims space. It is eventual, bounded by a retention horizon, and
it reaches neither a backup nor a coded stripe that has already been written
elsewhere.

⚠ **Only shredding is erasure**, and the other two are routinely mistaken for it.
An operator who marks a subject and reports it deleted has said something false;
an operator who waits for a sweep has said something that will be true locally
and never true of the backup.

The second half of this record is about the backup. ADR-005 made segments
immutable and this system replays them into sinks — a streaming backup, a read
model, an operator console. Those are three consumers of ONE primitive: a
subscription that reads a position and advances it.

⚠ **And that is what makes purge dangerous.** A purge that removes the primary
copy and reports success, while a sink nobody remembered is still holding the
data, produces exactly the failure this whole corpus is built to avoid: a restore
that resurrects what an operator was told was gone, months later, with nothing
having reported anything. So a purge is a FAN-OUT WITH ACKNOWLEDGEMENT, and a
sink that has not acknowledged makes the purge INCOMPLETE rather than failed —
the difference matters, because incomplete is a state an operator can act on.

## Existing Primitives Audit

- `internal/core/tail` (ADR-017): supplies the watermark and `Walk`. **Reused
  whole.** A subscription is a cursor over exactly that: a position, and a walk
  bounded by what is published. Nothing new is needed to make a subscription
  consistent, because a watermark already is a stable prefix.
- `internal/core/crypt` (ADR-007): supplies shredding. **Reused whole**, and this
  record adds no second erasure mechanism — it decides who must be TOLD, which
  ADR-007 explicitly left open.
- `internal/core/tx` (ADR-002): supplies the total order a cursor is expressed
  in. **Reused whole**: a cursor is a transaction identifier, not an offset, so
  it survives anything that renumbers positions.
- A message broker: **none adopted.** A subscription here is a cursor over a
  local structure; delivery over a network is a transport question and there is
  no transport. Adopting a broker now would decide the delivery semantics before
  the thing being delivered exists.

## Decision

**A subscription is a cursor. A purge is a fan-out that is not done until every
sink says so.**

1. **One subscription primitive, three consumers.** Streaming backup, read models
   and the console differ in what they DO with entries, not in how they get them.
   A second mechanism for any of them would drift from the first.

2. **A cursor is a transaction identifier, not an offset.** It survives
   compaction, renumbering and restart, and two subscribers comparing positions
   are comparing the same thing the rest of the system orders by.

3. **A subscription never skips.** It advances only past what it has
   acknowledged, so a sink that crashes resumes where it stopped. Delivery is
   therefore at-least-once and a sink must tolerate a repeat — which is stated
   here rather than discovered by whoever writes the first sink.

4. **Mark, shred and sweep are three operations with three different
   guarantees**, and the API names them separately. There is deliberately no
   `Delete`. ★A single verb would be answered differently by each mechanism and
   an operator would not know which they got.

5. **A purge fans out to every REGISTERED sink and collects acknowledgements.**
   Its result names who has acknowledged and who has not.

6. **An unacknowledged sink makes a purge INCOMPLETE, never failed and never
   successful.** ⚠ Successful would be a lie that surfaces at the next restore.
   Failed would suggest nothing happened, when the primary copy is already gone.
   Incomplete is the truth and it is the only one of the three an operator can
   act on.

7. **A sink that is not registered cannot be acknowledged for**, so registration
   is the act that makes a sink visible to purge. A sink wired up outside the
   registry is invisible, and that is recorded as the residual risk rather than
   defended against — nothing here can see what nothing told it about.

8. **Retention bounds the sweep, and only the sweep.** A horizon says how long
   reclaimable space is kept reclaimable. It does not bound marking, which is
   immediate, and it must never be mistaken for bounding erasure, which is
   ADR-007's and reaches everywhere at once.

**What would falsify this.** A purge reporting complete while a sink still holds
the data. That is exactly the falsifier this record names in `Enforced-by:`, and
the case it covers is the one that actually happens: a sink registered, never
reachable, and quietly ignored.

## Alternatives Considered

- **One `Delete` verb.** What every caller expects and what most systems offer.
  Rejected under rule 4: it would be answered by a different mechanism depending
  on context, and an operator would not know whether they got invisibility,
  erasure, or a promise about space.
- **Purge as a local operation, with sinks catching up by themselves.** Simple,
  and eventually consistent. Rejected: "eventually" is not a property an erasure
  can have, and a sink that never catches up is indistinguishable from one that
  has.
- **Purge reports failure if any sink has not acknowledged.** Honest, and it is
  what an ordinary operation would do. Rejected as MISLEADING: the primary copy
  is already gone by then, so "failed" tells an operator to retry the whole thing
  when what they need to do is chase one sink.
- **Offsets rather than transaction identifiers for cursors.** Cheaper and
  familiar. Rejected under rule 2: an offset is meaningless after compaction, and
  two subscribers holding offsets cannot be compared with anything else in the
  system.
- **Exactly-once delivery.** What every sink author wants. Rejected as not
  purchasable at this layer: it requires the sink's own writes to be transactional
  with its cursor advance, which is the SINK's property and not this primitive's.
  Saying so is more useful than implying otherwise.
- **A message broker as the subscription mechanism.** Mature, and solves delivery
  properly. Rejected for now: there is no transport, so adopting one would decide
  delivery semantics before there is anything to deliver, and a broker's own
  retention would become a second sink nobody registered.

## Component / Boundary Impact

One new component, `internal/core/subscribe`, owning cursors, sink registration
and the purge fan-out. It has one reason to change: how a consumer follows the
log and how it is told to forget.

⚠ The boundary: this component decides WHO must be told and HOW completion is
reported. It does not make anything unreadable — that is ADR-007's key
destruction — and it does not reclaim any space, because nothing here opens a
file. A purge that reported "erased" would be claiming ADR-007's guarantee
without doing ADR-007's work.

## Wiring & Contract Changes

| Surface | Change | Producer | Consumer(s) |
|---------|--------|----------|-------------|
| `subscribe.Cursor` | new — a resumable position, expressed as a `tx.TxID` | T1 | T2, sinks |
| `subscribe.Subscription` | new — a named sink and its cursor | T1 | T2 |
| `subscribe.Registry` | new — the sinks a purge must reach | T1 | T2 |
| `subscribe.Mark` / `Shred` / `Sweep` | new — three verbs, three guarantees, and no `Delete` | T2 | operators |
| `subscribe.PurgeResult` | new — who acknowledged, who has not, and the resulting state | T2 | operators |
| `subscribe.StateIncomplete` | new — the third outcome, distinct from done and failed | T2 | operators |
| `subscribe.Horizon` | new — the retention bound, which bounds the sweep only | T2 | operators |

## Inter-task Contracts

| Contract | Producing task | Consuming task(s) | Breaking? |
|----------|----------------|-------------------|-----------|
| `subscribe.Cursor`, `subscribe.Subscription`, `subscribe.Registry` | T1 | T2 | No — T2 is written against T1 and does not exist before it |
| `subscribe.PurgeResult`, the three verbs | T2 | none yet | No |

## Implementation

Two tasks, sequential. See `docs/adr/ADR-010-subscribe-and-purge/tasks/README.md`.

## Consequences

- **Positive:** An operator asking to remove a subject learns which of the three
  things they got, and which sinks still hold it. Today most systems answer that
  question with silence.
- **Positive:** One primitive serves backup, read models and the console, so a
  change to how following works reaches all three at once.
- **Positive:** A cursor is a transaction identifier, so a subscriber's position
  is comparable with everything else the system orders — including a snapshot a
  reader is holding.
- **Negative:** Delivery is at-least-once and sinks must be idempotent. That is
  a real cost pushed onto every sink author, and it is stated rather than papered
  over with a guarantee this layer cannot make.
- **Negative:** A purge can sit incomplete indefinitely if a sink never comes
  back. Nothing here escalates, and an operator has to notice — which is a gap
  ADR-012's console should close rather than this record.
- **Neutral:** A sink outside the registry is invisible to purge. Registration is
  the whole mechanism, and nothing can see what nothing told it about.

## Out of Scope

- Delivering entries over a network to a remote sink (deferred: `docs/adr/BACKLOG.md` §18)
- Reclaiming any actual space, which needs something that opens a file (deferred: `docs/adr/BACKLOG.md` §12)
- Making a subject unreadable (permanent: boundary: ADR-007 owns key destruction; this record decides who must be TOLD, and a purge claiming "erased" would be claiming ADR-007's guarantee without doing its work)
- Exactly-once delivery (permanent: boundary: it requires a sink's own writes to be transactional with its cursor advance, which is the sink's property and not this primitive's)
- Escalating a purge that stays incomplete (deferred: ADR-012, whose console is where an operator would see it)
- Authorizing who may purge (deferred: `docs/adr/BACKLOG.md` §11)

## Risks

| Risk | Likelihood | Impact | Mitigation |
|------|------------|--------|------------|
| A purge reports success while a sink still holds the data, and a restore resurrects it months later | High without this record | Critical — the failure is invisible until it is a compliance incident | Purge is a fan-out with per-sink acknowledgement, and an unacknowledged sink yields INCOMPLETE; the falsifier covers the registered-but-never-acknowledging case |
| An operator marks a subject and reports it erased | High — the three are habitually conflated | Critical | Three separately named verbs and no `Delete`; the record and the package comment both state which one is erasure |
| A sink is wired up outside the registry and never hears about a purge | Med | High | Stated as a residual risk rather than defended against, because nothing can see what nothing told it about; registration is the mechanism and its absence is the gap |
| A subscription skips entries after a crash and a backup is silently short | Med | High — a backup that is missing entries looks exactly like one that is complete | A cursor advances only past acknowledged entries, and the test crashes a sink mid-stream and asserts nothing between the cursor and the watermark was lost |

## Rollback

No persistent state — cursors and registrations are in memory and nothing here
opens a file — so rollback is a code revert.

The operator-visible contract is the expensive part: once purge reports three
states rather than two, collapsing them back to success-or-failure means either
lying or alarming. That is a compatibility question, and it is why the third
state is in the record from the start rather than added when somebody is bitten.

## Follow-ups

- [ ] When a transport exists (`BACKLOG.md` §18), confirm a remote sink's acknowledgement means the sink has DURABLY forgotten, not that it received the message — an acknowledgement of receipt would make a purge report complete on the strength of a message in flight.
- [ ] When ADR-012's console lands, surface purges that have been incomplete for a long time; nothing here escalates and an operator otherwise has to remember to look.
- [ ] When the segment writer lands (`BACKLOG.md` §12), confirm a sweep's retention horizon is checked against what a coded stripe still holds — a sweep that reclaims a block whose stripe is still referenced would destroy data no mark or shred asked to remove.
